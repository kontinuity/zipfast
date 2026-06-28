// Package tasks runs Zipfast's periodic background jobs, mirroring the cron-style
// workers in the original Zipline server (delete expired files, enforce max views,
// clear used/expired invites, and snapshot metrics).
//
// Integrator note: start these from cmd/zipfast/main.go after the HTTP server has
// started, e.g.:
//
//	tasks.Start(ctx, store, ds, cfg, log)
//
// where ctx is the process context that is cancelled on shutdown. Each task runs
// once immediately and then on its configured interval until ctx is done.
package tasks

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/lucsky/cuid"

	"zipfast/internal/config"
	"zipfast/internal/datasource"
	"zipfast/internal/db"
)

// Start launches one goroutine per background task. Each task executes once
// immediately, then repeatedly on a ticker at its configured interval, and stops
// when ctx is cancelled. Start returns immediately; the goroutines run in the
// background.
func Start(ctx context.Context, store *db.Store, ds datasource.Datasource, cfg *config.Config, log *slog.Logger) {
	log = log.With("component", "tasks")

	run(ctx, "deleteFiles", cfg.Tasks.DeleteInterval, log, func(ctx context.Context, l *slog.Logger) error {
		return deleteFiles(ctx, store, ds, l)
	})

	run(ctx, "maxViews", cfg.Tasks.MaxViewsInterval, log, func(ctx context.Context, l *slog.Logger) error {
		if !cfg.Features.DeleteOnMaxViews {
			return nil
		}
		return maxViews(ctx, store, ds, l)
	})

	run(ctx, "clearInvites", cfg.Tasks.ClearInvitesInterval, log, func(ctx context.Context, l *slog.Logger) error {
		return clearInvites(ctx, store, l)
	})

	if cfg.Features.MetricsEnabled {
		run(ctx, "metrics", cfg.Tasks.MetricsInterval, log, func(ctx context.Context, l *slog.Logger) error {
			return metrics(ctx, store, l)
		})
	}
}

// run starts a goroutine that invokes fn once immediately and then on every tick
// of a ticker at the given interval, stopping when ctx is cancelled. A non-positive
// interval disables the ticker but the task still runs once at startup.
func run(ctx context.Context, name string, interval time.Duration, log *slog.Logger, fn func(context.Context, *slog.Logger) error) {
	l := log.With("task", name)
	exec := func() {
		start := time.Now()
		l.Debug("task running")
		if err := fn(ctx, l); err != nil {
			l.Error("task failed", "err", err, "elapsed", time.Since(start))
			return
		}
		l.Debug("task complete", "elapsed", time.Since(start))
	}

	go func() {
		exec()

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
				exec()
			}
		}
	}()
}

// deleteFiles removes files whose deletes_at deadline has passed, deleting both
// the stored object and the database row for each.
func deleteFiles(ctx context.Context, store *db.Store, ds datasource.Datasource, log *slog.Logger) error {
	rows, err := store.Pool.Query(ctx,
		`SELECT id, name FROM files WHERE deletes_at IS NOT NULL AND deletes_at <= now()`)
	if err != nil {
		return err
	}

	type target struct{ id, name string }
	var targets []target
	for rows.Next() {
		var t target
		if err := rows.Scan(&t.id, &t.name); err != nil {
			rows.Close()
			return err
		}
		targets = append(targets, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, t := range targets {
		if err := ds.Delete(t.name); err != nil {
			log.Error("delete object failed", "id", t.id, "name", t.name, "err", err)
			continue
		}
		if _, err := store.Pool.Exec(ctx, `DELETE FROM files WHERE id=$1`, t.id); err != nil {
			log.Error("delete file row failed", "id", t.id, "err", err)
			continue
		}
		log.Debug("deleted expired file", "id", t.id, "name", t.name)
	}
	if len(targets) > 0 {
		log.Debug("deleteFiles processed", "count", len(targets))
	}
	return nil
}

// maxViews deletes files and URLs that have reached their max_views limit. For
// files the stored object is removed as well.
func maxViews(ctx context.Context, store *db.Store, ds datasource.Datasource, log *slog.Logger) error {
	rows, err := store.Pool.Query(ctx,
		`SELECT id, name FROM files WHERE max_views IS NOT NULL AND views >= max_views`)
	if err != nil {
		return err
	}

	type target struct{ id, name string }
	var targets []target
	for rows.Next() {
		var t target
		if err := rows.Scan(&t.id, &t.name); err != nil {
			rows.Close()
			return err
		}
		targets = append(targets, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, t := range targets {
		if err := ds.Delete(t.name); err != nil {
			log.Error("delete object failed", "id", t.id, "name", t.name, "err", err)
			continue
		}
		if _, err := store.Pool.Exec(ctx, `DELETE FROM files WHERE id=$1`, t.id); err != nil {
			log.Error("delete file row failed", "id", t.id, "err", err)
			continue
		}
		log.Debug("deleted file at max views", "id", t.id, "name", t.name)
	}

	tag, err := store.Pool.Exec(ctx,
		`DELETE FROM urls WHERE max_views IS NOT NULL AND views >= max_views`)
	if err != nil {
		return err
	}

	if len(targets) > 0 || tag.RowsAffected() > 0 {
		log.Debug("maxViews processed", "files", len(targets), "urls", tag.RowsAffected())
	}
	return nil
}

// clearInvites removes invites that have expired or reached their max uses.
func clearInvites(ctx context.Context, store *db.Store, log *slog.Logger) error {
	tag, err := store.Pool.Exec(ctx,
		`DELETE FROM invites
		 WHERE (expires_at IS NOT NULL AND expires_at <= now())
		    OR (max_uses IS NOT NULL AND uses >= max_uses)`)
	if err != nil {
		return err
	}
	if tag.RowsAffected() > 0 {
		log.Debug("clearInvites processed", "count", tag.RowsAffected())
	}
	return nil
}

// metricData is the JSONB payload stored in the metrics table. It mirrors the
// original Zipline MetricData shape so /api/stats and the metrics dashboard can
// project points and render the per-user / per-type tables.
type metricData struct {
	Users     int   `json:"users"`
	Files     int   `json:"files"`
	FileViews int   `json:"fileViews"`
	Urls      int   `json:"urls"`
	UrlViews  int   `json:"urlViews"`
	Storage   int64 `json:"storage"`

	FilesUsers []metricFilesUser `json:"filesUsers"`
	UrlsUsers  []metricUrlsUser  `json:"urlsUsers"`
	Types      []metricType      `json:"types"`
}

type metricFilesUser struct {
	Username string `json:"username"`
	Sum      int    `json:"sum"`
	Storage  int64  `json:"storage"`
	Views    int    `json:"views"`
}

type metricUrlsUser struct {
	Username string `json:"username"`
	Sum      int    `json:"sum"`
	Views    int    `json:"views"`
}

type metricType struct {
	Type string `json:"type"`
	Sum  int    `json:"sum"`
}

// metrics computes instance-wide aggregates and inserts a JSONB snapshot row.
func metrics(ctx context.Context, store *db.Store, log *slog.Logger) error {
	d := metricData{
		FilesUsers: []metricFilesUser{},
		UrlsUsers:  []metricUrlsUser{},
		Types:      []metricType{},
	}

	if err := store.Pool.QueryRow(ctx,
		`SELECT COUNT(*), COALESCE(SUM(size), 0), COALESCE(SUM(views), 0) FROM files`).
		Scan(&d.Files, &d.Storage, &d.FileViews); err != nil {
		return err
	}
	if err := store.Pool.QueryRow(ctx,
		`SELECT COUNT(*), COALESCE(SUM(views), 0) FROM urls`).Scan(&d.Urls, &d.UrlViews); err != nil {
		return err
	}
	if err := store.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM users`).Scan(&d.Users); err != nil {
		return err
	}

	if rows, err := store.Pool.Query(ctx,
		`SELECT COALESCE(u.username, 'unknown'), COUNT(*), COALESCE(SUM(f.size), 0), COALESCE(SUM(f.views), 0)
		 FROM files f LEFT JOIN users u ON u.id = f.user_id GROUP BY u.username`); err == nil {
		for rows.Next() {
			var fu metricFilesUser
			if err := rows.Scan(&fu.Username, &fu.Sum, &fu.Storage, &fu.Views); err == nil {
				d.FilesUsers = append(d.FilesUsers, fu)
			}
		}
		rows.Close()
	}

	if rows, err := store.Pool.Query(ctx,
		`SELECT COALESCE(u.username, 'unknown'), COUNT(*), COALESCE(SUM(ur.views), 0)
		 FROM urls ur LEFT JOIN users u ON u.id = ur.user_id GROUP BY u.username`); err == nil {
		for rows.Next() {
			var uu metricUrlsUser
			if err := rows.Scan(&uu.Username, &uu.Sum, &uu.Views); err == nil {
				d.UrlsUsers = append(d.UrlsUsers, uu)
			}
		}
		rows.Close()
	}

	if rows, err := store.Pool.Query(ctx,
		`SELECT type, COUNT(*) FROM files GROUP BY type`); err == nil {
		for rows.Next() {
			var t metricType
			if err := rows.Scan(&t.Type, &t.Sum); err == nil {
				d.Types = append(d.Types, t)
			}
		}
		rows.Close()
	}

	data, err := json.Marshal(d)
	if err != nil {
		return err
	}
	if _, err := store.Pool.Exec(ctx,
		`INSERT INTO metrics (id, data) VALUES ($1, $2)`, cuid.New(), data); err != nil {
		return err
	}

	log.Debug("metrics snapshot stored", "files", d.Files, "urls", d.Urls, "users", d.Users, "storage", d.Storage)
	return nil
}
