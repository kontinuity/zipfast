package server

import (
	"archive/zip"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/lucsky/cuid"

	"zipfast/internal/db"
	"zipfast/internal/models"
)

// registerExportRoutes mounts the authenticated export endpoints. The whole group
// is guarded by RequireUser, so every handler can rely on UserFromContext and
// scopes every query to that user's id for security.
//
// Endpoints:
//
//	POST   /api/user/export              create a ZIP of all the user's files (synchronous)
//	GET    /api/user/export              list the user's exports
//	GET    /api/user/export/{id}         download a previously created export ZIP
//	DELETE /api/user/export/{id}         delete an export row and its temp file
//	GET    /api/user/folders/{id}/export stream a ZIP of a folder's files (recursive)
func (a *App) registerExportRoutes(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(a.RequireUser)

		r.Post("/api/user/export", a.expCreateExport)
		r.Get("/api/user/export", a.expListExports)
		r.Get("/api/user/export/{id}", a.expDownloadExport)
		r.Delete("/api/user/export/{id}", a.expDeleteExport)

		r.Get("/api/user/folders/{id}/export", a.expExportFolder)
	})
}

// expExportColumns is the standard select list for the exports table.
const expExportColumns = `id, created_at, updated_at, completed, path, files, size, user_id`

func expScanExport(row pgx.Row) (*models.Export, error) {
	var e models.Export
	if err := row.Scan(&e.ID, &e.CreatedAt, &e.UpdatedAt, &e.Completed, &e.Path,
		&e.Files, &e.Size, &e.UserID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, db.ErrNotFound
		}
		return nil, err
	}
	return &e, nil
}

// expFileRow is the minimal projection needed to build a ZIP entry.
type expFileRow struct {
	Name         string
	OriginalName *string
}

// expEntryName picks the in-zip entry name for a file, preferring the original
// upload name and falling back to the stored name.
func expEntryName(f expFileRow) string {
	if f.OriginalName != nil && strings.TrimSpace(*f.OriginalName) != "" {
		return *f.OriginalName
	}
	return f.Name
}

// expDedupeName returns a unique entry name within an archive, appending " (n)"
// before the extension when a collision occurs (mirroring the OS-style rename).
func expDedupeName(seen map[string]int, name string) string {
	if name == "" {
		name = "file"
	}
	if _, ok := seen[name]; !ok {
		seen[name] = 0
		return name
	}
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for {
		seen[name]++
		candidate := base + " (" + strconv.Itoa(seen[name]) + ")" + ext
		if _, ok := seen[candidate]; !ok {
			seen[candidate] = 0
			return candidate
		}
	}
}

// expWriteFilesToZip copies each file's datasource object into the zip writer,
// deduping entry names. Files that fail to read from the datasource are logged
// and skipped. Returns the number of files actually written and the total bytes.
func (a *App) expWriteFilesToZip(zw *zip.Writer, files []expFileRow) (count int, total int64) {
	seen := make(map[string]int, len(files))
	for _, f := range files {
		if a.DS == nil {
			break
		}
		rc, err := a.DS.Get(f.Name)
		if err != nil {
			a.Log.Warn("export: failed to read file from datasource", "name", f.Name, "err", err)
			continue
		}
		if rc == nil {
			a.Log.Warn("export: file missing from datasource", "name", f.Name)
			continue
		}
		entry := expDedupeName(seen, expEntryName(f))
		w, err := zw.Create(entry)
		if err != nil {
			_ = rc.Close()
			a.Log.Warn("export: failed to create zip entry", "name", entry, "err", err)
			continue
		}
		n, err := io.Copy(w, rc)
		_ = rc.Close()
		if err != nil {
			a.Log.Warn("export: failed to copy file into zip", "name", f.Name, "err", err)
			// The entry header is already written; keep going with the next file.
			continue
		}
		count++
		total += n
	}
	return count, total
}

// expUserFiles loads the minimal file rows for a user.
func (a *App) expUserFiles(ctx context.Context, userID string) ([]expFileRow, error) {
	rows, err := a.Store.Pool.Query(ctx,
		`SELECT name, original_name FROM files WHERE user_id=$1 ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return expScanFileRows(rows)
}

func expScanFileRows(rows pgx.Rows) ([]expFileRow, error) {
	out := []expFileRow{}
	for rows.Next() {
		var f expFileRow
		if err := rows.Scan(&f.Name, &f.OriginalName); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// --- POST /api/user/export ---

func (a *App) expCreateExport(w http.ResponseWriter, r *http.Request) {
	u := UserFromContext(r.Context())
	ctx := r.Context()

	files, err := a.expUserFiles(ctx, u.ID)
	if err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to query files")
		return
	}

	if err := os.MkdirAll(a.Cfg.Core.TempDirectory, 0o755); err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to prepare temp directory")
		return
	}

	id := cuid.New()
	filename := "export_" + id + ".zip"
	path := filepath.Join(a.Cfg.Core.TempDirectory, filename)

	out, err := os.Create(path)
	if err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to create export file")
		return
	}

	zw := zip.NewWriter(out)
	count, total := a.expWriteFilesToZip(zw, files)

	if err := zw.Close(); err != nil {
		_ = out.Close()
		_ = os.Remove(path)
		a.Error(w, http.StatusInternalServerError, "failed to finalize export archive")
		return
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(path)
		a.Error(w, http.StatusInternalServerError, "failed to close export file")
		return
	}

	sizeStr := strconv.FormatInt(total, 10)
	if _, err := a.Store.Pool.Exec(ctx,
		`INSERT INTO exports (id, created_at, updated_at, completed, path, files, size, user_id)
		 VALUES ($1, now(), now(), true, $2, $3, $4, $5)`,
		id, filename, count, sizeStr, u.ID); err != nil {
		_ = os.Remove(path)
		a.Error(w, http.StatusInternalServerError, "failed to record export")
		return
	}

	a.logFor(r).Info("export created", "exportId", id, "files", count, "size", sizeStr)
	a.WriteJSON(w, http.StatusOK, map[string]any{
		"id":    id,
		"files": count,
		"size":  sizeStr,
	})
}

// --- GET /api/user/export ---

func (a *App) expListExports(w http.ResponseWriter, r *http.Request) {
	u := UserFromContext(r.Context())
	rows, err := a.Store.Pool.Query(r.Context(),
		`SELECT `+expExportColumns+` FROM exports WHERE user_id=$1 ORDER BY created_at DESC`, u.ID)
	if err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to query exports")
		return
	}
	defer rows.Close()

	exports := []models.Export{}
	for rows.Next() {
		e, err := expScanExport(rows)
		if err != nil {
			a.Error(w, http.StatusInternalServerError, "failed to read exports")
			return
		}
		exports = append(exports, *e)
	}
	if err := rows.Err(); err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to read exports")
		return
	}
	a.WriteJSON(w, http.StatusOK, exports)
}

// expLoadOwnedExport loads an export scoped to a user, returning db.ErrNotFound
// when it does not exist or belongs to another user.
func (a *App) expLoadOwnedExport(ctx context.Context, id, userID string) (*models.Export, error) {
	return expScanExport(a.Store.Pool.QueryRow(ctx,
		`SELECT `+expExportColumns+` FROM exports WHERE id=$1 AND user_id=$2`, id, userID))
}

// expExportPath resolves the on-disk path for an export, tolerating both a stored
// bare filename and an absolute path.
func (a *App) expExportPath(e *models.Export) string {
	if filepath.IsAbs(e.Path) {
		return e.Path
	}
	return filepath.Join(a.Cfg.Core.TempDirectory, e.Path)
}

// --- GET /api/user/export/{id} ---

func (a *App) expDownloadExport(w http.ResponseWriter, r *http.Request) {
	u := UserFromContext(r.Context())
	id := chi.URLParam(r, "id")

	e, err := a.expLoadOwnedExport(r.Context(), id, u.ID)
	if err != nil {
		a.expHandleLookupErr(w, err)
		return
	}

	path := a.expExportPath(e)
	f, err := os.Open(path)
	if err != nil {
		a.Error(w, http.StatusNotFound, "export file not found")
		return
	}
	defer f.Close()

	name := "export_" + e.ID + ".zip"
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+expSanitizeFilename(name)+"\"")
	if fi, statErr := f.Stat(); statErr == nil {
		w.Header().Set("Content-Length", strconv.FormatInt(fi.Size(), 10))
	}
	_, _ = io.Copy(w, f)
}

// --- DELETE /api/user/export/{id} ---

func (a *App) expDeleteExport(w http.ResponseWriter, r *http.Request) {
	u := UserFromContext(r.Context())
	id := chi.URLParam(r, "id")
	ctx := r.Context()

	e, err := a.expLoadOwnedExport(ctx, id, u.ID)
	if err != nil {
		a.expHandleLookupErr(w, err)
		return
	}

	if _, err := a.Store.Pool.Exec(ctx,
		`DELETE FROM exports WHERE id=$1 AND user_id=$2`, id, u.ID); err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to delete export")
		return
	}

	if err := os.Remove(a.expExportPath(e)); err != nil && !errors.Is(err, os.ErrNotExist) {
		a.Log.Warn("export: failed to remove temp file", "path", e.Path, "err", err)
	}

	a.logFor(r).Info("export deleted", "exportId", id)
	a.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// --- GET /api/user/folders/{id}/export ---

func (a *App) expExportFolder(w http.ResponseWriter, r *http.Request) {
	u := UserFromContext(r.Context())
	id := chi.URLParam(r, "id")
	ctx := r.Context()

	// Verify the folder belongs to the user and grab its name.
	var folderName string
	if err := a.Store.Pool.QueryRow(ctx,
		`SELECT name FROM folders WHERE id=$1 AND user_id=$2`, id, u.ID).Scan(&folderName); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			a.Error(w, http.StatusNotFound, "folder not found")
			return
		}
		a.Error(w, http.StatusInternalServerError, "failed to load folder")
		return
	}

	folderIDs, err := a.expFolderTree(ctx, id, u.ID)
	if err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to walk folders")
		return
	}

	files, err := a.expFilesInFolders(ctx, u.ID, folderIDs)
	if err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to query folder files")
		return
	}

	zipName := folderName
	if strings.TrimSpace(zipName) == "" {
		zipName = "folder"
	}
	zipName += ".zip"

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+expSanitizeFilename(zipName)+"\"")

	a.logFor(r).Info("folder export (zip)", "folder", id, "files", len(files))
	zw := zip.NewWriter(w)
	a.expWriteFilesToZip(zw, files)
	if err := zw.Close(); err != nil {
		// Headers/body may already be partially written; just log.
		a.Log.Warn("export: failed to finalize folder archive", "folder", id, "err", err)
	}
}

// expFolderTree returns the folder id plus all nested descendant folder ids owned
// by the user (recursive).
func (a *App) expFolderTree(ctx context.Context, rootID, userID string) ([]string, error) {
	ids := []string{rootID}
	queue := []string{rootID}
	seen := map[string]bool{rootID: true}

	for len(queue) > 0 {
		parent := queue[0]
		queue = queue[1:]

		rows, err := a.Store.Pool.Query(ctx,
			`SELECT id FROM folders WHERE parent_id=$1 AND user_id=$2`, parent, userID)
		if err != nil {
			return nil, err
		}
		var children []string
		for rows.Next() {
			var cid string
			if err := rows.Scan(&cid); err != nil {
				rows.Close()
				return nil, err
			}
			children = append(children, cid)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		for _, cid := range children {
			if !seen[cid] {
				seen[cid] = true
				ids = append(ids, cid)
				queue = append(queue, cid)
			}
		}
	}
	return ids, nil
}

// expFilesInFolders loads the file rows that belong to any of the given folder ids
// (already scoped to the user) for a user.
func (a *App) expFilesInFolders(ctx context.Context, userID string, folderIDs []string) ([]expFileRow, error) {
	if len(folderIDs) == 0 {
		return []expFileRow{}, nil
	}
	rows, err := a.Store.Pool.Query(ctx,
		`SELECT name, original_name FROM files WHERE user_id=$1 AND folder_id = ANY($2) ORDER BY created_at DESC`,
		userID, folderIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return expScanFileRows(rows)
}

// expHandleLookupErr writes a 404 for not-found lookups and a 500 otherwise.
func (a *App) expHandleLookupErr(w http.ResponseWriter, err error) {
	if errors.Is(err, db.ErrNotFound) {
		a.Error(w, http.StatusNotFound, "export not found")
		return
	}
	a.Error(w, http.StatusInternalServerError, "failed to load export")
}

// expSanitizeFilename strips characters that would break a Content-Disposition
// header value (quotes, path separators, control chars).
func expSanitizeFilename(name string) string {
	name = strings.Map(func(r rune) rune {
		switch r {
		case '"', '\\', '/', '\n', '\r':
			return '_'
		}
		if r < 0x20 {
			return '_'
		}
		return r
	}, name)
	if name == "" {
		return "export.zip"
	}
	return name
}
