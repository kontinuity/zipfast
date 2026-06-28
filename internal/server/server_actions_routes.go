package server

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"zipfast/internal/models"
	"zipfast/internal/thumbnails"
)

// registerServerActionRoutes wires the admin "server actions" endpoints (disk
// status, clear temp/zeros, requery size, thumbnails, export) plus the public
// folder view endpoint. These mirror the original Zipline /api/server/* routes
// 1:1 in method, auth and JSON shape so the vendored SPA keeps working.
//
// The integrator wires this into routes.go (a.registerServerActionRoutes(r)).
func (a *App) registerServerActionRoutes(r chi.Router) {
	// GET /api/server/status (admin) — disk status for the configured datasource.
	r.With(a.RequireAdmin).Get("/api/server/status", a.sactStatus)

	// /api/server/clear_temp — delete temporary files (admin). The original route
	// is a DELETE (the SPA's ClearTempButton sends DELETE); we also accept POST
	// for callers/spec that use POST. Both return { status }.
	r.With(a.RequireAdmin).Delete("/api/server/clear_temp", a.sactClearTemp)
	r.With(a.RequireAdmin).Post("/api/server/clear_temp", a.sactClearTemp)

	// /api/server/clear_zeros (admin):
	//   GET    -> { files: [{ id, name }] } (candidates; used to size the modal)
	//   DELETE -> { status } (delete from DB + datasource)
	r.With(a.RequireAdmin).Get("/api/server/clear_zeros", a.sactClearZerosList)
	r.With(a.RequireAdmin).Delete("/api/server/clear_zeros", a.sactClearZeros)

	// POST /api/server/requery_size (admin) -> { status }.
	r.With(a.RequireAdmin).Post("/api/server/requery_size", a.sactRequerySize)

	// POST /api/server/thumbnails (admin) -> { status }.
	r.With(a.RequireAdmin).Post("/api/server/thumbnails", a.sactThumbnails)

	// GET /api/server/export (SUPERADMIN) -> v4 export bundle, or counts with
	// ?counts=true. The original enforces SUPERADMIN inside the handler.
	r.With(a.RequireAdmin).Get("/api/server/export", a.sactExport)

	// GET /api/server/folder/{id} (PUBLIC) -> { folder, page, total, pages }.
	r.Get("/api/server/folder/{id}", a.sactFolder)
}

// --- status ---

// sactStatus returns { datasource, storage: { used, total, available, path } }.
// total/available are JSON null for the s3 datasource (matches diskStatusSchema).
func (a *App) sactStatus(w http.ResponseWriter, r *http.Request) {
	c := a.Cfg

	type storage struct {
		Used      int64  `json:"used"`
		Total     *int64 `json:"total"`
		Available *int64 `json:"available"`
		Path      string `json:"path"`
	}

	var st storage

	if c.Datasource.Type == "s3" {
		used, err := a.DS.TotalSize()
		if err != nil {
			a.Error(w, http.StatusInternalServerError, "failed to compute storage size")
			return
		}
		path := c.Datasource.S3.Bucket
		if sub := sactTrimTrailingSlash(c.Datasource.S3.Subdirectory); sub != "" {
			path += "/" + sub
		}
		st = storage{Used: used, Total: nil, Available: nil, Path: path}
	} else {
		dir := c.Datasource.Local.Directory
		var fs syscall.Statfs_t
		if err := syscall.Statfs(dir, &fs); err != nil {
			a.Error(w, http.StatusInternalServerError, "failed to stat datasource directory")
			return
		}
		bsize := int64(fs.Bsize)
		total := int64(fs.Blocks) * bsize
		available := int64(fs.Bavail) * bsize
		used := total - int64(fs.Bfree)*bsize
		st = storage{Used: used, Total: &total, Available: &available, Path: dir}
	}

	a.WriteJSON(w, http.StatusOK, map[string]any{
		"datasource": c.Datasource.Type,
		"storage":    st,
	})
}

func sactTrimTrailingSlash(s string) string {
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}

// --- clear_temp ---

// sactClearTemp deletes files in the configured temp directory (best-effort) and
// returns { status } with a human message, matching clearTemp().
func (a *App) sactClearTemp(w http.ResponseWriter, r *http.Request) {
	dir := a.Cfg.Core.TempDirectory

	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		a.WriteJSON(w, http.StatusOK, map[string]any{
			"status": "Temp directory does not exist, so no files were cleared.",
		})
		return
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to read temp directory")
		return
	}
	if len(entries) == 0 {
		a.WriteJSON(w, http.StatusOK, map[string]any{
			"status": "No temporary zipline files found, so no files were cleared.",
		})
		return
	}

	count := 0
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err == nil {
			count++
		}
	}

	a.WriteJSON(w, http.StatusOK, map[string]any{
		"status": "Cleared " + strconv.Itoa(count) + " temporary zipline files.",
	})
}

// --- clear_zeros ---

type sactZeroFile struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// sactZeroFiles returns the DB files whose size is exactly 0.
func (a *App) sactZeroFiles(ctx context.Context) ([]sactZeroFile, error) {
	rows, err := a.Store.Pool.Query(ctx, `SELECT id, name FROM files WHERE size = 0`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	files := make([]sactZeroFile, 0)
	for rows.Next() {
		var f sactZeroFile
		if err := rows.Scan(&f.ID, &f.Name); err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	return files, rows.Err()
}

// sactClearZerosList (GET) returns { files: [{ id, name }] }.
func (a *App) sactClearZerosList(w http.ResponseWriter, r *http.Request) {
	files, err := a.sactZeroFiles(r.Context())
	if err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to scan for zero-byte files")
		return
	}
	a.WriteJSON(w, http.StatusOK, map[string]any{"files": files})
}

// sactClearZeros (DELETE) removes zero-byte files from the DB and the datasource
// and returns { status }, matching clearZeros().
func (a *App) sactClearZeros(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	files, err := a.sactZeroFiles(ctx)
	if err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to scan for zero-byte files")
		return
	}

	count := 0
	for _, f := range files {
		if _, err := a.Store.Pool.Exec(ctx, `DELETE FROM files WHERE id = $1`, f.ID); err != nil {
			continue
		}
		count++
		_ = a.DS.Delete(f.Name)
	}

	a.WriteJSON(w, http.StatusOK, map[string]any{
		"status": "Cleared " + strconv.Itoa(count) + " files with a size of 0.",
	})
}

// --- requery_size ---

type sactRequerySizeBody struct {
	ForceDelete bool `json:"forceDelete"`
	ForceUpdate bool `json:"forceUpdate"`
}

// sactRequerySize recomputes each file's size from the datasource and updates the
// DB, returning { status }. Mirrors requerySize(): when forceUpdate is false only
// zero-size rows are inspected; when forceDelete is set, rows missing from the
// datasource are deleted, otherwise their presence flips the returned message.
func (a *App) sactRequerySize(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body sactRequerySizeBody
	// Body is optional; ignore decode errors and fall back to defaults (false).
	_ = a.ReadJSON(r, &body)

	query := `SELECT id, name, size FROM files`
	if !body.ForceUpdate {
		query += ` WHERE size = 0`
	}

	rows, err := a.Store.Pool.Query(ctx, query)
	if err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to query files")
		return
	}

	type fileRow struct {
		id   string
		name string
		size int64
	}
	files := make([]fileRow, 0)
	for rows.Next() {
		var fr fileRow
		if err := rows.Scan(&fr.id, &fr.name, &fr.size); err != nil {
			rows.Close()
			a.Error(w, http.StatusInternalServerError, "failed to read files")
			return
		}
		files = append(files, fr)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to read files")
		return
	}

	notFound := false
	for _, fr := range files {
		size, err := a.DS.Size(fr.name)
		if err != nil || size < 0 {
			// Missing from the datasource.
			if body.ForceDelete {
				_, _ = a.Store.Pool.Exec(ctx, `DELETE FROM files WHERE id = $1`, fr.id)
				continue
			}
			notFound = true
			continue
		}

		if size == 0 {
			// Leave zero-byte files untouched (matches the original).
			continue
		}

		_, _ = a.Store.Pool.Exec(ctx,
			`UPDATE files SET size = $1, updated_at = now() WHERE id = $2`, size, fr.id)
	}

	message := "Finished requerying all files."
	if notFound {
		message = "At least one file did not exist within the datasource but was on the " +
			"database, re run the script with the force delete option on to remove these files."
	}

	a.WriteJSON(w, http.StatusOK, map[string]any{"status": message})
}

// --- thumbnails ---

type sactThumbnailsBody struct {
	Rerun bool `json:"rerun"`
}

// sactThumbnails triggers thumbnail generation in the background and returns the
// original's ack { status }. When rerun is requested we (re)generate for every
// file; otherwise the background generator picks up files lacking a thumbnail.
func (a *App) sactThumbnails(w http.ResponseWriter, r *http.Request) {
	var body sactThumbnailsBody
	_ = a.ReadJSON(r, &body)

	// Run asynchronously so the request returns promptly (the original kicks off a
	// background task and tells the user to watch the logs).
	go func() {
		ctx := context.Background()
		if body.Rerun {
			ids, err := a.sactAllFileIDs(ctx)
			if err == nil && len(ids) > 0 {
				thumbnails.GenerateFor(ctx, a.Store, a.DS, a.Cfg, a.Log, ids)
			}
			return
		}
		ids, err := a.sactFilesMissingThumbnails(ctx)
		if err == nil && len(ids) > 0 {
			thumbnails.GenerateFor(ctx, a.Store, a.DS, a.Cfg, a.Log, ids)
		}
	}()

	status := "Thumbnails are being generated. This may take a while, check your logs for progress."
	if body.Rerun {
		status = "Thumbnails are being generated (rerun). This may take a while, check your logs for progress."
	}

	a.WriteJSON(w, http.StatusOK, map[string]any{"status": status})
}

func (a *App) sactAllFileIDs(ctx context.Context) ([]string, error) {
	rows, err := a.Store.Pool.Query(ctx, `SELECT id FROM files`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return sactScanIDs(rows)
}

func (a *App) sactFilesMissingThumbnails(ctx context.Context) ([]string, error) {
	rows, err := a.Store.Pool.Query(ctx,
		`SELECT f.id FROM files f
		 LEFT JOIN thumbnails t ON t.file_id = f.id
		 WHERE t.id IS NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return sactScanIDs(rows)
}

func sactScanIDs(rows pgx.Rows) ([]string, error) {
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// --- export ---

// sactExport mirrors the original /api/server/export: SUPERADMIN-only, returns
// aggregate counts with ?counts=true, otherwise a version-4 export bundle as a
// JSON attachment. File contents are never included (only metadata).
func (a *App) sactExport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	u := UserFromContext(ctx)
	if u == nil || u.Role != models.RoleSuperAdmin {
		a.Error(w, http.StatusForbidden, "forbidden")
		return
	}

	if r.URL.Query().Get("counts") != "" {
		counts, err := a.sactExportCounts(ctx)
		if err != nil {
			a.Error(w, http.StatusInternalServerError, "failed to compute counts")
			return
		}
		a.WriteJSON(w, http.StatusOK, counts)
		return
	}

	noMetrics := r.URL.Query().Get("nometrics") != ""

	export, err := a.sactBuildExport(ctx, u, noMetrics)
	if err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to build export")
		return
	}

	w.Header().Set("Content-Disposition",
		"attachment; filename=zipline4_export_"+strconv.FormatInt(time.Now().UnixMilli(), 10)+".json")
	a.WriteJSON(w, http.StatusOK, export)
}

func (a *App) sactExportCounts(ctx context.Context) (map[string]any, error) {
	count := func(table string) (int, error) {
		var n int
		err := a.Store.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM `+table).Scan(&n)
		return n, err
	}

	users, err := count("users")
	if err != nil {
		return nil, err
	}
	files, err := count("files")
	if err != nil {
		return nil, err
	}
	urls, err := count("urls")
	if err != nil {
		return nil, err
	}
	folders, err := count("folders")
	if err != nil {
		return nil, err
	}
	invites, err := count("invites")
	if err != nil {
		return nil, err
	}
	thumbs, err := count("thumbnails")
	if err != nil {
		return nil, err
	}
	metrics, err := count("metrics")
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"users":      users,
		"files":      files,
		"urls":       urls,
		"folders":    folders,
		"invites":    invites,
		"thumbnails": thumbs,
		"metrics":    metrics,
	}, nil
}

// sactBuildExport assembles a version-4 export bundle from the database. The
// shape mirrors export4Schema: { versions, request, data:{ ... } }.
func (a *App) sactBuildExport(ctx context.Context, requester *models.User, noMetrics bool) (map[string]any, error) {
	env := map[string]string{}
	for _, kv := range os.Environ() {
		for i := 0; i < len(kv); i++ {
			if kv[i] == '=' {
				env[kv[:i]] = kv[i+1:]
				break
			}
		}
	}

	host, _ := os.Hostname()

	data := map[string]any{
		"settings":           a.sactExportSettings(ctx),
		"users":              a.sactExportUsers(ctx),
		"userPasskeys":       a.sactExportPasskeys(ctx),
		"userQuotas":         a.sactExportQuotas(ctx),
		"userOauthProviders": a.sactExportOAuth(ctx),
		"userTags":           a.sactExportTags(ctx),
		"invites":            a.sactExportInvites(ctx),
		"folders":            a.sactExportFolders(ctx),
		"urls":               a.sactExportUrls(ctx),
		"files":              a.sactExportFiles(ctx),
		"thumbnails":         a.sactExportThumbnails(ctx),
		"metrics":            []any{},
	}
	if !noMetrics {
		data["metrics"] = a.sactExportMetrics(ctx)
	}

	return map[string]any{
		"versions": map[string]any{
			"export":  "4",
			"node":    "",
			"zipline": a.Version,
		},
		"request": map[string]any{
			"date": time.Now().UTC().Format(time.RFC3339),
			"env":  env,
			"user": requester.ID + ":" + requester.Username,
			"os": map[string]any{
				"arch":     runtime.GOARCH,
				"cpus":     runtime.NumCPU(),
				"hostname": host,
				"platform": runtime.GOOS,
				"release":  "",
			},
		},
		"data": data,
	}, nil
}

func (a *App) sactExportSettings(ctx context.Context) any {
	// The stored settings blob (zipline_settings); may be empty.
	data, _, err := a.Store.LoadSettings(ctx)
	if err != nil || len(data) == 0 {
		return map[string]any{}
	}
	return sactRawJSON(data)
}

func (a *App) sactExportUsers(ctx context.Context) []map[string]any {
	out := make([]map[string]any, 0)
	rows, err := a.Store.Pool.Query(ctx,
		`SELECT id, created_at, username, password, avatar, role, view, totp_secret FROM users ORDER BY created_at ASC`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id, username     string
			password, avatar *string
			role             string
			view             []byte
			totp             *string
			createdAt        time.Time
		)
		if err := rows.Scan(&id, &createdAt, &username, &password, &avatar, &role, &view, &totp); err != nil {
			continue
		}
		out = append(out, map[string]any{
			"createdAt":  createdAt.UTC().Format(time.RFC3339),
			"id":         id,
			"username":   username,
			"password":   password,
			"avatar":     avatar,
			"role":       role,
			"view":       sactRawJSON(view),
			"totpSecret": totp,
		})
	}
	return out
}

func (a *App) sactExportPasskeys(ctx context.Context) []map[string]any {
	out := make([]map[string]any, 0)
	rows, err := a.Store.Pool.Query(ctx,
		`SELECT id, created_at, last_used, name, reg, user_id FROM user_passkeys ORDER BY created_at ASC`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id, name, userID string
			lastUsed         *time.Time
			reg              []byte
			createdAt        time.Time
		)
		if err := rows.Scan(&id, &createdAt, &lastUsed, &name, &reg, &userID); err != nil {
			continue
		}
		out = append(out, map[string]any{
			"createdAt": createdAt.UTC().Format(time.RFC3339),
			"id":        id,
			"lastUsed":  sactTimePtr(lastUsed),
			"name":      name,
			"reg":       sactRawJSON(reg),
			"userId":    userID,
		})
	}
	return out
}

func (a *App) sactExportQuotas(ctx context.Context) []map[string]any {
	out := make([]map[string]any, 0)
	rows, err := a.Store.Pool.Query(ctx,
		`SELECT id, created_at, files_quota, max_bytes, max_files, max_urls, user_id FROM user_quotas ORDER BY created_at ASC`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id, filesQuota string
			maxBytes       *string
			maxFiles       *int
			maxUrls        *int
			userID         *string
			createdAt      time.Time
		)
		if err := rows.Scan(&id, &createdAt, &filesQuota, &maxBytes, &maxFiles, &maxUrls, &userID); err != nil {
			continue
		}
		out = append(out, map[string]any{
			"createdAt":  createdAt.UTC().Format(time.RFC3339),
			"id":         id,
			"filesQuota": filesQuota,
			"maxBytes":   maxBytes,
			"maxFiles":   maxFiles,
			"maxUrls":    maxUrls,
			"userId":     userID,
		})
	}
	return out
}

func (a *App) sactExportOAuth(ctx context.Context) []map[string]any {
	out := make([]map[string]any, 0)
	rows, err := a.Store.Pool.Query(ctx,
		`SELECT id, created_at, provider, username, access_token, refresh_token, oauth_id, user_id FROM oauth_providers ORDER BY created_at ASC`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id, provider, username, accessToken, userID string
			refreshToken, oauthID                       *string
			createdAt                                   time.Time
		)
		if err := rows.Scan(&id, &createdAt, &provider, &username, &accessToken, &refreshToken, &oauthID, &userID); err != nil {
			continue
		}
		out = append(out, map[string]any{
			"createdAt":    createdAt.UTC().Format(time.RFC3339),
			"id":           id,
			"provider":     provider,
			"username":     username,
			"accessToken":  accessToken,
			"refreshToken": refreshToken,
			"oauthId":      oauthID,
			"userId":       userID,
		})
	}
	return out
}

func (a *App) sactExportTags(ctx context.Context) []map[string]any {
	out := make([]map[string]any, 0)
	rows, err := a.Store.Pool.Query(ctx,
		`SELECT id, created_at, name, color, user_id FROM tags ORDER BY created_at ASC`)
	if err != nil {
		return out
	}
	defer rows.Close()
	type tagRow struct {
		id, name, color string
		userID          *string
		createdAt       time.Time
	}
	tagRows := make([]tagRow, 0)
	for rows.Next() {
		var t tagRow
		if err := rows.Scan(&t.id, &t.createdAt, &t.name, &t.color, &t.userID); err != nil {
			continue
		}
		tagRows = append(tagRows, t)
	}
	rows.Close()

	for _, t := range tagRows {
		out = append(out, map[string]any{
			"createdAt": t.createdAt.UTC().Format(time.RFC3339),
			"id":        t.id,
			"name":      t.name,
			"color":     t.color,
			"files":     a.sactFileIDsForTag(ctx, t.id),
			"userId":    t.userID,
		})
	}
	return out
}

func (a *App) sactFileIDsForTag(ctx context.Context, tagID string) []string {
	rows, err := a.Store.Pool.Query(ctx, `SELECT file_id FROM file_tags WHERE tag_id = $1`, tagID)
	if err != nil {
		return []string{}
	}
	defer rows.Close()
	ids, _ := sactScanIDs(rows)
	return ids
}

func (a *App) sactExportInvites(ctx context.Context) []map[string]any {
	out := make([]map[string]any, 0)
	rows, err := a.Store.Pool.Query(ctx,
		`SELECT id, created_at, code, uses, max_uses, expires_at, inviter_id FROM invites ORDER BY created_at ASC`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id, code, inviterID string
			uses                int
			maxUses             *int
			expiresAt           *time.Time
			createdAt           time.Time
		)
		if err := rows.Scan(&id, &createdAt, &code, &uses, &maxUses, &expiresAt, &inviterID); err != nil {
			continue
		}
		out = append(out, map[string]any{
			"createdAt": createdAt.UTC().Format(time.RFC3339),
			"id":        id,
			"code":      code,
			"uses":      uses,
			"maxUses":   maxUses,
			"expiresAt": sactTimePtr(expiresAt),
			"inviterId": inviterID,
		})
	}
	return out
}

func (a *App) sactExportFolders(ctx context.Context) []map[string]any {
	out := make([]map[string]any, 0)
	rows, err := a.Store.Pool.Query(ctx,
		`SELECT id, created_at, name, public, allow_uploads, user_id, parent_id FROM folders ORDER BY created_at ASC`)
	if err != nil {
		return out
	}
	defer rows.Close()
	type folderRow struct {
		id, name             string
		public, allowUploads bool
		userID               string
		parentID             *string
		createdAt            time.Time
	}
	folderRows := make([]folderRow, 0)
	for rows.Next() {
		var f folderRow
		if err := rows.Scan(&f.id, &f.createdAt, &f.name, &f.public, &f.allowUploads, &f.userID, &f.parentID); err != nil {
			continue
		}
		folderRows = append(folderRows, f)
	}
	rows.Close()

	for _, f := range folderRows {
		out = append(out, map[string]any{
			"createdAt":    f.createdAt.UTC().Format(time.RFC3339),
			"id":           f.id,
			"name":         f.name,
			"public":       f.public,
			"allowUploads": f.allowUploads,
			"userId":       f.userID,
			"files":        a.sactFileIDsForFolder(ctx, f.id),
			"parentId":     f.parentID,
		})
	}
	return out
}

func (a *App) sactFileIDsForFolder(ctx context.Context, folderID string) []string {
	rows, err := a.Store.Pool.Query(ctx, `SELECT id FROM files WHERE folder_id = $1`, folderID)
	if err != nil {
		return []string{}
	}
	defer rows.Close()
	ids, _ := sactScanIDs(rows)
	return ids
}

func (a *App) sactExportUrls(ctx context.Context) []map[string]any {
	out := make([]map[string]any, 0)
	rows, err := a.Store.Pool.Query(ctx,
		`SELECT id, created_at, code, vanity, destination, views, max_views, password, enabled, user_id FROM urls ORDER BY created_at ASC`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id, code, destination string
			vanity, password      *string
			views                 int
			maxViews              *int
			enabled               bool
			userID                *string
			createdAt             time.Time
		)
		if err := rows.Scan(&id, &createdAt, &code, &vanity, &destination, &views, &maxViews, &password, &enabled, &userID); err != nil {
			continue
		}
		out = append(out, map[string]any{
			"createdAt":   createdAt.UTC().Format(time.RFC3339),
			"id":          id,
			"code":        code,
			"vanity":      vanity,
			"destination": destination,
			"views":       views,
			"maxViews":    maxViews,
			"password":    password,
			"enabled":     enabled,
			"userId":      userID,
		})
	}
	return out
}

func (a *App) sactExportFiles(ctx context.Context) []map[string]any {
	out := make([]map[string]any, 0)
	rows, err := a.Store.Pool.Query(ctx,
		`SELECT id, created_at, deletes_at, name, size, favorite, original_name, type, views, max_views, password, user_id, folder_id FROM files ORDER BY created_at ASC`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id, name, typ          string
			deletesAt              *time.Time
			size                   int64
			favorite               bool
			originalName, password *string
			views                  int
			maxViews               *int
			userID, folderID       *string
			createdAt              time.Time
		)
		if err := rows.Scan(&id, &createdAt, &deletesAt, &name, &size, &favorite, &originalName, &typ, &views, &maxViews, &password, &userID, &folderID); err != nil {
			continue
		}
		out = append(out, map[string]any{
			"createdAt":    createdAt.UTC().Format(time.RFC3339),
			"deletesAt":    sactTimePtr(deletesAt),
			"id":           id,
			"name":         name,
			"size":         size,
			"favorite":     favorite,
			"originalName": originalName,
			"type":         typ,
			"views":        views,
			"maxViews":     maxViews,
			"password":     password,
			"userId":       userID,
			"folderId":     folderID,
		})
	}
	return out
}

func (a *App) sactExportThumbnails(ctx context.Context) []map[string]any {
	out := make([]map[string]any, 0)
	rows, err := a.Store.Pool.Query(ctx,
		`SELECT id, created_at, path, file_id FROM thumbnails ORDER BY created_at ASC`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id, path, fileID string
			createdAt        time.Time
		)
		if err := rows.Scan(&id, &createdAt, &path, &fileID); err != nil {
			continue
		}
		out = append(out, map[string]any{
			"createdAt": createdAt.UTC().Format(time.RFC3339),
			"id":        id,
			"path":      path,
			"fileId":    fileID,
		})
	}
	return out
}

func (a *App) sactExportMetrics(ctx context.Context) []map[string]any {
	out := make([]map[string]any, 0)
	rows, err := a.Store.Pool.Query(ctx,
		`SELECT id, created_at, data FROM metrics ORDER BY created_at ASC`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id        string
			data      []byte
			createdAt time.Time
		)
		if err := rows.Scan(&id, &createdAt, &data); err != nil {
			continue
		}
		out = append(out, map[string]any{
			"createdAt": createdAt.UTC().Format(time.RFC3339),
			"id":        id,
			"data":      sactRawJSON(data),
		})
	}
	return out
}

// --- folder (public) ---

// sactFolder mirrors the public /api/server/folder/:id route: it returns a folder
// (matched by id OR name) with its paginated files, but only when the folder is
// public or allows uploads. Response shape: { folder, page, total, pages }.
func (a *App) sactFolder(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")

	var (
		folderID, name string
		public         bool
		allowUploads   bool
		parentID       *string
		userID         string
		password       *string
		createdAt      time.Time
		updatedAt      time.Time
	)
	err := a.Store.Pool.QueryRow(ctx,
		`SELECT id, created_at, updated_at, name, public, allow_uploads, parent_id, user_id, password
		   FROM folders WHERE id = $1 OR name = $1 LIMIT 1`, id).
		Scan(&folderID, &createdAt, &updatedAt, &name, &public, &allowUploads, &parentID, &userID, &password)
	if errors.Is(err, pgx.ErrNoRows) {
		a.Error(w, http.StatusNotFound, "folder not found")
		return
	}
	if err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to load folder")
		return
	}

	// Only public folders (or upload-only folders) are visible here.
	if !public && !allowUploads {
		a.Error(w, http.StatusNotFound, "folder not found")
		return
	}

	// Password gate: a protected folder's listing requires a valid folder token
	// (set as a cookie by the /folder/{id} gate page, or passed as ?token=).
	protected := password != nil && *password != ""
	if protected && !a.folderTokenValid(r, folderID) {
		a.WriteJSON(w, http.StatusForbidden, map[string]any{
			"error":             "folder is password protected",
			"passwordProtected": true,
			"folder":            map[string]any{"id": folderID, "name": name, "passwordProtected": true},
		})
		return
	}

	pageStr := r.URL.Query().Get("page")
	perpage := queryInt(r, "perpage", 15)
	if perpage <= 0 {
		perpage = 15
	}

	// Upload-only folders with no page requested: return a minimal descriptor and
	// no file listing (matches the original early-return branch).
	if pageStr == "" && allowUploads {
		a.WriteJSON(w, http.StatusOK, map[string]any{
			"folder": map[string]any{
				"id":                folderID,
				"name":              name,
				"allowUploads":      allowUploads,
				"public":            public,
				"passwordProtected": protected,
			},
			"page":  []any{},
			"total": 0,
			"pages": 0,
		})
		return
	}

	page := 1
	if pageStr != "" {
		if n, perr := strconv.Atoi(pageStr); perr == nil && n > 0 {
			page = n
		}
	}

	var total int
	if err := a.Store.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM files WHERE folder_id = $1`, folderID).Scan(&total); err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to count files")
		return
	}
	pages := 0
	if total > 0 {
		pages = (total + perpage - 1) / perpage
	}

	sortBy := userFileSortColumn(r.URL.Query().Get("sortBy"))
	order := "DESC"
	if o := r.URL.Query().Get("order"); o == "asc" || o == "ASC" {
		order = "ASC"
	}

	rows, err := a.Store.Pool.Query(ctx,
		`SELECT `+userFileColumns+` FROM files f WHERE f.folder_id = $1 ORDER BY `+sortBy+` `+order+
			` LIMIT $2 OFFSET $3`, folderID, perpage, (page-1)*perpage)
	if err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to list files")
		return
	}
	files, err := userScanFiles(rows)
	rows.Close()
	if err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to read files")
		return
	}
	if err := a.userHydrateFiles(ctx, files); err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to hydrate files")
		return
	}

	// Build the cleaned folder: _count, public children, and a public parent chain.
	childCount, fileCount := a.sactFolderCounts(ctx, folderID)
	folder := map[string]any{
		"id":                folderID,
		"createdAt":         createdAt,
		"updatedAt":         updatedAt,
		"name":              name,
		"public":            public,
		"allowUploads":      allowUploads,
		"parentId":          parentID,
		"userId":            userID,
		"passwordProtected": protected,
		"_count": map[string]any{
			"children": childCount,
			"files":    fileCount,
		},
		"children": a.sactPublicChildren(ctx, folderID),
	}
	if parentID != nil {
		folder["parent"] = a.sactPublicParentChain(ctx, *parentID)
	}

	a.WriteJSON(w, http.StatusOK, map[string]any{
		"folder": folder,
		"page":   a.fileResponses(r, files),
		"total":  total,
		"pages":  pages,
	})
}

func (a *App) sactFolderCounts(ctx context.Context, folderID string) (childCount, fileCount int) {
	_ = a.Store.Pool.QueryRow(ctx,
		`SELECT (SELECT COUNT(*) FROM folders c WHERE c.parent_id = $1 AND c.public = true),
		        (SELECT COUNT(*) FROM files fi WHERE fi.folder_id = $1)`,
		folderID).Scan(&childCount, &fileCount)
	return
}

// sactPublicChildren returns the public child folders (each with their own
// _count), matching the original include { children: { where: { public } } }.
func (a *App) sactPublicChildren(ctx context.Context, parentID string) []map[string]any {
	rows, err := a.Store.Pool.Query(ctx,
		`SELECT id, name, created_at, updated_at, public FROM folders
		   WHERE parent_id = $1 AND public = true ORDER BY created_at DESC`, parentID)
	if err != nil {
		return []map[string]any{}
	}
	defer rows.Close()

	type childRow struct {
		id, name             string
		createdAt, updatedAt time.Time
		public               bool
	}
	childRows := make([]childRow, 0)
	for rows.Next() {
		var c childRow
		if err := rows.Scan(&c.id, &c.name, &c.createdAt, &c.updatedAt, &c.public); err != nil {
			continue
		}
		childRows = append(childRows, c)
	}
	rows.Close()

	children := make([]map[string]any, 0, len(childRows))
	for _, c := range childRows {
		cc, fc := a.sactFolderCounts(ctx, c.id)
		children = append(children, map[string]any{
			"id":        c.id,
			"name":      c.name,
			"createdAt": c.createdAt,
			"updatedAt": c.updatedAt,
			"public":    c.public,
			"_count": map[string]any{
				"children": cc,
				"files":    fc,
			},
		})
	}
	return children
}

// sactPublicParentChain mirrors buildPublicParentChain: a nested
// { id, name, public, parentId, parent } that stops at the first non-public
// ancestor (returning nil there).
func (a *App) sactPublicParentChain(ctx context.Context, parentID string) any {
	var (
		id, name string
		public   bool
		pid      *string
	)
	err := a.Store.Pool.QueryRow(ctx,
		`SELECT id, name, public, parent_id FROM folders WHERE id = $1`, parentID).
		Scan(&id, &name, &public, &pid)
	if err != nil || !public {
		return nil
	}
	m := map[string]any{
		"id":       id,
		"name":     name,
		"public":   public,
		"parentId": pid,
	}
	if pid != nil {
		m["parent"] = a.sactPublicParentChain(ctx, *pid)
	} else {
		m["parent"] = nil
	}
	return m
}

// --- small helpers ---

// sactRawJSON returns a value that marshals as the given raw JSON bytes (so JSONB
// columns are emitted as JSON rather than a base64 string). Falls back to an empty
// object when the bytes are empty or invalid.
func sactRawJSON(b []byte) any {
	if len(b) == 0 {
		return map[string]any{}
	}
	return sactJSONRaw(b)
}

// sactJSONRaw is a thin wrapper so json.Marshal emits the bytes verbatim.
type sactJSONRaw []byte

func (j sactJSONRaw) MarshalJSON() ([]byte, error) {
	if len(j) == 0 {
		return []byte("{}"), nil
	}
	return j, nil
}

func sactTimePtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format(time.RFC3339)
}
