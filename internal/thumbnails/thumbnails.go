// Package thumbnails generates poster-frame thumbnails for uploaded videos in the
// background, mirroring the thumbnail worker from the original Zipline server.
//
// Design goal: LOW MEMORY. There is no permanently-running worker pool. Instead a
// single lightweight goroutine wakes on an interval (or on demand), queries the
// database for videos that still lack a thumbnail, and spins up a *transient*,
// bounded pool of workers for the duration of that one scan. When the scan
// finishes the workers exit, so idle memory cost is ~0. The heavy lifting (video
// decoding) is delegated to the ffmpeg CLI as a child process (see internal/media),
// so we never hold a decoded video in our own address space.
//
// Integrator note: start this from cmd/zipfast/main.go after the HTTP server has
// started, e.g.:
//
//	thumbnails.Start(ctx, store, ds, cfg, log)
//
// where ctx is the process context that is cancelled on shutdown. Start returns
// immediately; the scanning goroutine runs in the background.
package thumbnails

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/lucsky/cuid"

	"zipfast/internal/config"
	"zipfast/internal/datasource"
	"zipfast/internal/db"
	"zipfast/internal/media"
)

// fileTarget is the minimal set of columns the per-file worker needs.
type fileTarget struct {
	id   string
	name string
	size int64
}

// Start launches the background thumbnail generator. If thumbnails are disabled
// in the config, or ffmpeg is not available on this system, it logs once and
// returns a no-op (no goroutine is started, so there is zero ongoing cost).
//
// Otherwise it starts a single goroutine that runs a scan once immediately and
// then on every tick of cfg.Tasks.ThumbnailsInterval, stopping when ctx is
// cancelled. Each scan finds videos missing a thumbnail and processes them with a
// transient, bounded worker pool; the pool exists only for the duration of the
// scan, so idle memory stays ~0.
func Start(ctx context.Context, store *db.Store, ds datasource.Datasource, cfg *config.Config, log *slog.Logger) {
	log = log.With("component", "thumbnails")

	if !cfg.Features.ThumbnailsEnabled {
		log.Info("thumbnail generation disabled; not starting worker")
		return
	}
	if !media.HasFFmpeg() {
		log.Info("ffmpeg not found on PATH; thumbnail generation unavailable")
		return
	}

	interval := cfg.Tasks.ThumbnailsInterval

	go func() {
		scan := func() {
			start := time.Now()
			n, err := scanOnce(ctx, store, ds, cfg, log)
			if err != nil {
				log.Error("thumbnail scan failed", "err", err, "elapsed", time.Since(start))
				return
			}
			if n > 0 {
				log.Debug("thumbnail scan complete", "generated", n, "elapsed", time.Since(start))
			}
		}

		// Run once immediately on startup.
		scan()

		if interval <= 0 {
			return
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				scan()
			}
		}
	}()
}

// GenerateFor generates thumbnails immediately for the given file IDs (used for
// the "instantaneous" mode that runs right after an upload). It uses the same
// per-file logic and the same transient bounded pool as the periodic scan. It is
// a no-op when thumbnails are disabled, ffmpeg is missing, or fileIDs is empty.
// GenerateFor blocks until all requested files have been processed (or ctx is
// cancelled).
func GenerateFor(ctx context.Context, store *db.Store, ds datasource.Datasource, cfg *config.Config, log *slog.Logger, fileIDs []string) {
	log = log.With("component", "thumbnails")

	if !cfg.Features.ThumbnailsEnabled || !media.HasFFmpeg() || len(fileIDs) == 0 {
		return
	}

	targets, err := videoTargetsByIDs(ctx, store, fileIDs)
	if err != nil {
		log.Error("thumbnail lookup by ids failed", "err", err)
		return
	}
	if len(targets) == 0 {
		return
	}

	processTargets(ctx, store, ds, cfg, log, targets)
}

// scanOnce queries for videos missing a thumbnail and processes them. It returns
// the number of files for which a thumbnail was successfully generated.
func scanOnce(ctx context.Context, store *db.Store, ds datasource.Datasource, cfg *config.Config, log *slog.Logger) (int, error) {
	targets, err := videoTargetsNeedingThumbnails(ctx, store)
	if err != nil {
		return 0, err
	}
	if len(targets) == 0 {
		return 0, nil
	}
	return processTargets(ctx, store, ds, cfg, log, targets), nil
}

// videoTargetsNeedingThumbnails returns video files with a positive size that do
// not yet have a thumbnail row.
func videoTargetsNeedingThumbnails(ctx context.Context, store *db.Store) ([]fileTarget, error) {
	rows, err := store.Pool.Query(ctx,
		`SELECT id, name, size FROM files f
		 WHERE f.type LIKE 'video/%'
		   AND f.size > 0
		   AND NOT EXISTS (SELECT 1 FROM thumbnails t WHERE t.file_id = f.id)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var targets []fileTarget
	for rows.Next() {
		var t fileTarget
		if err := rows.Scan(&t.id, &t.name, &t.size); err != nil {
			return nil, err
		}
		targets = append(targets, t)
	}
	return targets, rows.Err()
}

// videoTargetsByIDs returns the subset of the given file IDs that are videos with
// a positive size and no existing thumbnail. Files that are not videos, are
// empty, or already have a thumbnail are silently skipped.
func videoTargetsByIDs(ctx context.Context, store *db.Store, fileIDs []string) ([]fileTarget, error) {
	rows, err := store.Pool.Query(ctx,
		`SELECT id, name, size FROM files f
		 WHERE f.id = ANY($1)
		   AND f.type LIKE 'video/%'
		   AND f.size > 0
		   AND NOT EXISTS (SELECT 1 FROM thumbnails t WHERE t.file_id = f.id)`,
		fileIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var targets []fileTarget
	for rows.Next() {
		var t fileTarget
		if err := rows.Scan(&t.id, &t.name, &t.size); err != nil {
			return nil, err
		}
		targets = append(targets, t)
	}
	return targets, rows.Err()
}

// processTargets runs the per-file thumbnail logic across targets using a
// transient, bounded pool of cfg.Features.ThumbnailsThreads goroutines (a
// semaphore caps concurrency). The pool is created here and torn down before the
// function returns, so it consumes no memory while idle. It returns the number of
// thumbnails successfully generated. Cancellation is respected between files: once
// ctx is done no further files are scheduled.
func processTargets(ctx context.Context, store *db.Store, ds datasource.Datasource, cfg *config.Config, log *slog.Logger, targets []fileTarget) int {
	threads := cfg.Features.ThumbnailsThreads
	if threads < 1 {
		threads = 1
	}

	sem := make(chan struct{}, threads)
	var wg sync.WaitGroup
	var mu sync.Mutex
	generated := 0

	for _, t := range targets {
		// Respect cancellation between files so a shutdown doesn't queue more work.
		if ctx.Err() != nil {
			break
		}

		// Acquire a worker slot, but bail out promptly if ctx is cancelled while
		// the pool is saturated.
		select {
		case <-ctx.Done():
			wg.Wait()
			return generated
		case sem <- struct{}{}:
		}

		wg.Add(1)
		go func(t fileTarget) {
			defer wg.Done()
			defer func() { <-sem }()

			ok, err := generateOne(ctx, store, ds, cfg, log, t)
			if err != nil {
				log.Error("thumbnail generation failed", "id", t.id, "name", t.name, "err", err)
				return
			}
			if ok {
				mu.Lock()
				generated++
				mu.Unlock()
				log.Debug("thumbnail generated", "id", t.id, "name", t.name)
			} else {
				log.Debug("no thumbnail produced", "id", t.id, "name", t.name)
			}
		}(t)
	}

	wg.Wait()
	return generated
}

// generateOne performs the full thumbnail pipeline for a single file:
//
//  1. Stream the source object from the datasource to a temp file on disk.
//  2. Ask ffmpeg (via media.VideoThumbnail) to extract a poster frame to a second
//     temp file whose extension selects the output format.
//  3. On success, store the thumbnail bytes back in the datasource under the
//     ".thumbnail.{id}.{fmt}" key and insert a thumbnails row.
//
// All temp files are removed before returning (success or failure). It returns
// (true, nil) when a thumbnail was produced and persisted, (false, nil) when the
// input simply has no usable frame (e.g. an audio-only "video/*") or the source
// object is missing, and (false, err) on a genuine failure.
func generateOne(ctx context.Context, store *db.Store, ds datasource.Datasource, cfg *config.Config, log *slog.Logger, t fileTarget) (bool, error) {
	if ctx.Err() != nil {
		return false, ctx.Err()
	}

	format := normalizeFormat(cfg.Features.ThumbnailsFormat)

	// 1. Copy the source object to a temp file. ffmpeg needs a seekable path and
	// we deliberately avoid buffering the whole video in memory.
	srcPath, cleanupSrc, err := copyObjectToTemp(ds, cfg.Core.TempDirectory, t.id, t.name)
	if err != nil {
		return false, err
	}
	defer cleanupSrc()
	if srcPath == "" {
		// Object not found in the datasource; nothing to do.
		log.Debug("source object missing for thumbnail", "id", t.id, "name", t.name)
		return false, nil
	}

	// 2. Extract a frame to a temp output file whose extension drives the encoder.
	outPath := filepath.Join(tempDir(cfg.Core.TempDirectory), "zipfast-thumb-"+t.id+"."+format)
	defer os.Remove(outPath)

	ok, err := media.VideoThumbnail(srcPath, outPath, format)
	if err != nil {
		return false, err
	}
	if !ok {
		// No usable video frame (e.g. audio-only). Not an error.
		return false, nil
	}

	// 3. Read the produced thumbnail and persist it.
	data, err := os.ReadFile(outPath)
	if err != nil {
		return false, fmt.Errorf("read thumbnail output: %w", err)
	}
	if len(data) == 0 {
		return false, nil
	}

	if ctx.Err() != nil {
		return false, ctx.Err()
	}

	key := thumbnailKey(t.id, format)
	if err := ds.Put(key, bytes.NewReader(data), int64(len(data)), datasource.PutOptions{
		Mimetype: media.ThumbnailMime(format),
	}); err != nil {
		return false, fmt.Errorf("store thumbnail object: %w", err)
	}

	// Insert the thumbnails row. ON CONFLICT guards the rare race where the same
	// file is picked up twice (file_id is UNIQUE); if a row already exists we
	// remove the duplicate object we just wrote so it isn't orphaned.
	tag, err := store.Pool.Exec(ctx,
		`INSERT INTO thumbnails (id, path, file_id) VALUES ($1, $2, $3)
		 ON CONFLICT (file_id) DO NOTHING`,
		cuid.New(), key, t.id)
	if err != nil {
		_ = ds.Delete(key) // best-effort cleanup of the object we just stored
		return false, fmt.Errorf("insert thumbnail row: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// A thumbnail already existed for this file; drop the duplicate object.
		_ = ds.Delete(key)
		return false, nil
	}

	return true, nil
}

// copyObjectToTemp streams the object named `name` from the datasource into a
// freshly created temp file in tmpDir and returns its path along with a cleanup
// function that removes it. If the object does not exist it returns ("", noop, nil).
func copyObjectToTemp(ds datasource.Datasource, tmpDir, id, name string) (path string, cleanup func(), err error) {
	cleanup = func() {}

	rc, err := ds.Get(name)
	if err != nil {
		return "", cleanup, fmt.Errorf("get source object: %w", err)
	}
	if rc == nil {
		// Not found.
		return "", cleanup, nil
	}
	defer rc.Close()

	dir := tempDir(tmpDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", cleanup, fmt.Errorf("create temp dir: %w", err)
	}

	f, err := os.CreateTemp(dir, "zipfast-thumbsrc-"+id+"-*"+filepath.Ext(name))
	if err != nil {
		return "", cleanup, fmt.Errorf("create temp source: %w", err)
	}
	tmpPath := f.Name()
	cleanup = func() { _ = os.Remove(tmpPath) }

	if _, err := io.Copy(f, rc); err != nil {
		f.Close()
		cleanup()
		return "", func() {}, fmt.Errorf("copy source to temp: %w", err)
	}
	if err := f.Close(); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("close temp source: %w", err)
	}

	return tmpPath, cleanup, nil
}

// thumbnailKey builds the datasource object key for a thumbnail, matching the
// original Zipline convention of a ".thumbnail.{id}" prefix (here suffixed with
// the chosen format's extension).
func thumbnailKey(id, format string) string {
	return ".thumbnail." + id + "." + format
}

// normalizeFormat lowercases and de-dots the configured thumbnail format,
// defaulting to "jpg" when unset.
func normalizeFormat(format string) string {
	f := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(format)), ".")
	if f == "" {
		return "jpg"
	}
	return f
}

// tempDir returns the temp directory to use, falling back to the OS temp dir when
// the configured value is empty.
func tempDir(configured string) string {
	if strings.TrimSpace(configured) == "" {
		return os.TempDir()
	}
	return configured
}
