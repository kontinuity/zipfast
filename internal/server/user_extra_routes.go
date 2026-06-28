package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"zipfast/internal/auth"
	"zipfast/internal/db"
	"zipfast/internal/models"
)

// registerUserExtraRoutes mounts the remaining authenticated /api/user/* routes
// that the vendored React client depends on but which were not registered by
// registerUserRoutes. Like that group, every handler is guarded by RequireUser
// (except the URL password verification endpoint, which the original leaves
// unauthenticated so a logged-out visitor can unlock a protected short link) and
// every query is scoped to the authenticated user's id.
//
// Response shapes here are matched, field-for-field, to the original Zipline
// routes under src/server/routes/api/user/* because the client was written
// against those contracts:
//   - GET/DELETE /api/user/sessions -> { current, other } (UserSession shapes).
//   - GET/POST   /api/user/avatar   -> the raw avatar data URL string.
//   - GET        /api/user/activity -> { days, series, totals } daily counts.
//   - GET/DELETE /api/user/files/incomplete -> IncompleteFile[] / { count }.
//   - PATCH/DELETE /api/user/files/transaction -> bulk { count, name? }.
//   - GET        /api/user/files/{id}/raw -> the raw file bytes (range-aware).
//   - POST       /api/user/urls/{id}/password -> { success, token }.
//
// These coexist with the existing /api/user/files/{id} and /api/user/urls/{id}
// routes: chi gives static path segments priority over {id} wildcards, so the
// literal /transaction, /incomplete and /raw segments resolve here first.
func (a *App) registerUserExtraRoutes(r chi.Router) {
	// Authenticated group.
	r.Group(func(r chi.Router) {
		r.Use(a.RequireUser)

		// Sessions.
		r.Get("/api/user/sessions", a.uexGetSessions)
		r.Delete("/api/user/sessions", a.uexDeleteSessions)

		// Avatar.
		r.Get("/api/user/avatar", a.uexGetAvatar)
		r.Post("/api/user/avatar", a.uexPostAvatar)

		// Activity timeline.
		r.Get("/api/user/activity", a.uexGetActivity)

		// Incomplete (chunked) uploads.
		r.Get("/api/user/files/incomplete", a.uexGetIncomplete)
		r.Delete("/api/user/files/incomplete", a.uexDeleteIncomplete)

		// Bulk file operations.
		r.Patch("/api/user/files/transaction", a.uexPatchTransaction)
		r.Delete("/api/user/files/transaction", a.uexDeleteTransaction)

		// Raw bytes for an owned file.
		r.Get("/api/user/files/{id}/raw", a.uexGetFileRaw)
	})

	// Unauthenticated: verifying a short URL's password must work for logged-out
	// visitors, exactly like the original urls/[id]/password.ts (no userMiddleware).
	r.Post("/api/user/urls/{id}/password", a.uexUrlPassword)
}

// --- sessions ---

const uexSessionColumns = `id, created_at, ua, client, device, user_id`

func uexScanSession(row pgx.Row) (*models.UserSession, error) {
	var s models.UserSession
	if err := row.Scan(&s.ID, &s.CreatedAt, &s.UA, &s.Client, &s.Device, &s.UserID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, db.ErrNotFound
		}
		return nil, err
	}
	return &s, nil
}

// uexListSessions loads every tracked session for a user, ordered newest-first.
func (a *App) uexListSessions(r *http.Request, userID string) ([]models.UserSession, error) {
	rows, err := a.Store.Pool.Query(r.Context(),
		`SELECT `+uexSessionColumns+` FROM user_sessions WHERE user_id=$1 ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.UserSession{}
	for rows.Next() {
		s, serr := uexScanSession(rows)
		if serr != nil {
			return nil, serr
		}
		out = append(out, *s)
	}
	return out, rows.Err()
}

// uexCurrentSessionID returns the user_sessions.id of the request's session, as
// stored in the encrypted session cookie. Empty when there is no cookie session
// (e.g. a token-authenticated API request).
func (a *App) uexCurrentSessionID(r *http.Request) string {
	if a.Sessions == nil {
		return ""
	}
	return a.Sessions.Get(r).SessionID
}

// uexSessionsResponse partitions the user's sessions into the current one and the
// rest, matching the original { current, other } shape (current is null when the
// cookie session can't be found among the tracked rows).
func uexSessionsResponse(sessions []models.UserSession, currentID string) map[string]any {
	var current any
	other := []models.UserSession{}
	for i := range sessions {
		if currentID != "" && sessions[i].ID == currentID {
			current = sessions[i]
			continue
		}
		other = append(other, sessions[i])
	}
	return map[string]any{
		"current": current,
		"other":   other,
	}
}

// uexGetSessions mirrors GET /api/user/sessions: { current, other }.
func (a *App) uexGetSessions(w http.ResponseWriter, r *http.Request) {
	u := UserFromContext(r.Context())
	sessions, err := a.uexListSessions(r, u.ID)
	if err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to load sessions")
		return
	}
	a.WriteJSON(w, http.StatusOK, uexSessionsResponse(sessions, a.uexCurrentSessionID(r)))
}

type uexDeleteSessionsBody struct {
	SessionID *string `json:"sessionId"`
	All       *bool   `json:"all"`
}

// uexDeleteSessions mirrors DELETE /api/user/sessions: invalidate one session by
// id, or all sessions except the current one. Returns the refreshed
// { current, other } payload.
func (a *App) uexDeleteSessions(w http.ResponseWriter, r *http.Request) {
	u := UserFromContext(r.Context())
	ctx := r.Context()
	currentID := a.uexCurrentSessionID(r)

	var body uexDeleteSessionsBody
	if err := a.ReadJSON(r, &body); err != nil {
		a.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if body.All != nil && *body.All {
		if _, err := a.Store.Pool.Exec(ctx,
			`DELETE FROM user_sessions WHERE user_id=$1 AND id<>$2`, u.ID, currentID); err != nil {
			a.Error(w, http.StatusInternalServerError, "failed to delete sessions")
			return
		}
		sessions, err := a.uexListSessions(r, u.ID)
		if err != nil {
			a.Error(w, http.StatusInternalServerError, "failed to load sessions")
			return
		}
		a.WriteJSON(w, http.StatusOK, uexSessionsResponse(sessions, currentID))
		return
	}

	if body.SessionID == nil || *body.SessionID == "" {
		a.Error(w, http.StatusBadRequest, "sessionId is required")
		return
	}
	// The original refuses to delete the current session via this endpoint
	// (ApiError 1021) and 404s an unknown session (ApiError 1031).
	if *body.SessionID == currentID {
		a.Error(w, http.StatusBadRequest, "cannot delete the current session")
		return
	}

	tag, err := a.Store.Pool.Exec(ctx,
		`DELETE FROM user_sessions WHERE id=$1 AND user_id=$2`, *body.SessionID, u.ID)
	if err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to delete session")
		return
	}
	if tag.RowsAffected() == 0 {
		a.Error(w, http.StatusNotFound, "session not found")
		return
	}

	sessions, err := a.uexListSessions(r, u.ID)
	if err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to load sessions")
		return
	}
	a.WriteJSON(w, http.StatusOK, uexSessionsResponse(sessions, currentID))
}

// --- avatar ---

// uexGetAvatar mirrors GET /api/user/avatar: it returns the stored avatar as a
// raw data-URL string (text/plain), not JSON. A missing avatar is a 404
// (ApiError 9002 in the original). The client (useAvatar) reads res.text().
func (a *App) uexGetAvatar(w http.ResponseWriter, r *http.Request) {
	u := UserFromContext(r.Context())

	var avatar *string
	if err := a.Store.Pool.QueryRow(r.Context(),
		`SELECT avatar FROM users WHERE id=$1`, u.ID).Scan(&avatar); err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to load avatar")
		return
	}
	if avatar == nil || *avatar == "" {
		a.Error(w, http.StatusNotFound, "no avatar")
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(*avatar))
}

type uexAvatarBody struct {
	Avatar *string `json:"avatar"`
}

// uexPostAvatar sets or clears the user's avatar (stored as a data URL string on
// users.avatar) and returns it as a raw string, matching the GET shape. The
// client primarily updates the avatar via PATCH /api/user; this endpoint exists
// so the POST contract is also satisfied.
func (a *App) uexPostAvatar(w http.ResponseWriter, r *http.Request) {
	u := UserFromContext(r.Context())

	var body uexAvatarBody
	if err := a.ReadJSON(r, &body); err != nil {
		a.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	var avatar any
	if body.Avatar != nil && *body.Avatar != "" {
		avatar = *body.Avatar
	} else {
		avatar = nil
	}

	if _, err := a.Store.Pool.Exec(r.Context(),
		`UPDATE users SET avatar=$1, updated_at=now() WHERE id=$2`, avatar, u.ID); err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to update avatar")
		return
	}

	if avatar == nil {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(avatar.(string)))
}

// --- activity ---

const (
	uexActivityMaxDays     = 90
	uexActivityDefaultDays = 14
)

type uexActivityDay struct {
	Date    string `json:"date"`
	Uploads int    `json:"uploads"`
	Logins  int    `json:"logins"`
}

// uexGetActivity mirrors GET /api/user/activity: daily upload (files) and login
// (sessions) counts over a recent window, returned as { days, series, totals }.
// The window is bounded to [1, 90] days, defaulting to 14, like the original.
func (a *App) uexGetActivity(w http.ResponseWriter, r *http.Request) {
	u := UserFromContext(r.Context())

	days := queryInt(r, "days", uexActivityDefaultDays)
	if days < 1 {
		days = 1
	}
	if days > uexActivityMaxDays {
		days = uexActivityMaxDays
	}

	// Inclusive window: from the start of (today - (days-1)) to now.
	now := time.Now()
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).
		AddDate(0, 0, -(days - 1))

	uploadsByDay, err := a.uexCountByDay(r, `files`, u.ID, start)
	if err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to load activity")
		return
	}
	loginsByDay, err := a.uexCountByDay(r, `user_sessions`, u.ID, start)
	if err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to load activity")
		return
	}

	series := make([]uexActivityDay, 0, days)
	totalUploads, totalLogins := 0, 0
	for i := days - 1; i >= 0; i-- {
		day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).
			AddDate(0, 0, -i)
		key := day.Format("2006-01-02")
		uploads := uploadsByDay[key]
		logins := loginsByDay[key]
		totalUploads += uploads
		totalLogins += logins
		series = append(series, uexActivityDay{
			// The original emits an ISO-8601 timestamp for the day boundary.
			Date:    day.UTC().Format(time.RFC3339),
			Uploads: uploads,
			Logins:  logins,
		})
	}

	a.WriteJSON(w, http.StatusOK, map[string]any{
		"days":   days,
		"series": series,
		"totals": map[string]any{
			"uploads": totalUploads,
			"logins":  totalLogins,
		},
	})
}

// uexCountByDay returns a map of YYYY-MM-DD -> row count for a user's rows in the
// given table created at or after start. Only the trusted, hard-coded table names
// "files" and "user_sessions" are ever passed in, so interpolating it is safe.
func (a *App) uexCountByDay(r *http.Request, table, userID string, start time.Time) (map[string]int, error) {
	rows, err := a.Store.Pool.Query(r.Context(),
		`SELECT to_char(created_at, 'YYYY-MM-DD') AS d, COUNT(*)
		   FROM `+table+`
		  WHERE user_id=$1 AND created_at >= $2
		  GROUP BY d`, userID, start)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var key string
		var n int
		if err := rows.Scan(&key, &n); err != nil {
			return nil, err
		}
		out[key] = n
	}
	return out, rows.Err()
}

// --- incomplete files ---

// uexGetIncomplete mirrors GET /api/user/files/incomplete: a bare array of the
// user's incomplete (chunked) uploads. metadata is emitted as the raw JSON object
// it is stored as (the client reads metadata.file.filename / .type).
func (a *App) uexGetIncomplete(w http.ResponseWriter, r *http.Request) {
	u := UserFromContext(r.Context())

	rows, err := a.Store.Pool.Query(r.Context(),
		`SELECT id, created_at, updated_at, status, chunks_total, chunks_complete, metadata
		   FROM incomplete_files WHERE user_id=$1 ORDER BY created_at DESC`, u.ID)
	if err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to load incomplete files")
		return
	}
	defer rows.Close()

	out := []map[string]any{}
	for rows.Next() {
		var (
			id, status                  string
			createdAt, updatedAt        time.Time
			chunksTotal, chunksComplete int
			metadata                    []byte
		)
		if err := rows.Scan(&id, &createdAt, &updatedAt, &status, &chunksTotal, &chunksComplete, &metadata); err != nil {
			a.Error(w, http.StatusInternalServerError, "failed to read incomplete files")
			return
		}
		out = append(out, map[string]any{
			"id":             id,
			"createdAt":      createdAt,
			"updatedAt":      updatedAt,
			"status":         status,
			"chunksTotal":    chunksTotal,
			"chunksComplete": chunksComplete,
			"metadata":       uexRawJSON(metadata),
		})
	}
	if err := rows.Err(); err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to read incomplete files")
		return
	}
	a.WriteJSON(w, http.StatusOK, out)
}

type uexIncompleteDeleteBody struct {
	ID []string `json:"id"`
}

// uexDeleteIncomplete mirrors DELETE /api/user/files/incomplete: delete the
// user's incomplete-file records whose ids are listed, returning { count }.
func (a *App) uexDeleteIncomplete(w http.ResponseWriter, r *http.Request) {
	u := UserFromContext(r.Context())

	var body uexIncompleteDeleteBody
	if err := a.ReadJSON(r, &body); err != nil {
		a.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(body.ID) == 0 {
		a.WriteJSON(w, http.StatusOK, map[string]any{"count": 0})
		return
	}

	tag, err := a.Store.Pool.Exec(r.Context(),
		`DELETE FROM incomplete_files WHERE user_id=$1 AND id = ANY($2)`, u.ID, body.ID)
	if err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to delete incomplete files")
		return
	}
	a.WriteJSON(w, http.StatusOK, map[string]any{"count": tag.RowsAffected()})
}

// --- bulk file transactions ---

type uexTransactionPatchBody struct {
	Files    []string `json:"files"`
	Favorite *bool    `json:"favorite"`
	Folder   *string  `json:"folder"`
}

// uexPatchTransaction mirrors PATCH /api/user/files/transaction: bulk
// favorite/unfavorite (when "favorite" is present) or move into a folder (when
// "folder" is present) for the listed files, scoped to the user. Returns
// { count } for favorites and { count, name } for a folder move.
func (a *App) uexPatchTransaction(w http.ResponseWriter, r *http.Request) {
	u := UserFromContext(r.Context())
	ctx := r.Context()

	var body uexTransactionPatchBody
	if err := a.ReadJSON(r, &body); err != nil {
		a.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(body.Files) == 0 {
		a.Error(w, http.StatusBadRequest, "files is required")
		return
	}

	if body.Favorite != nil {
		tag, err := a.Store.Pool.Exec(ctx,
			`UPDATE files SET favorite=$1, updated_at=now() WHERE user_id=$2 AND id = ANY($3)`,
			*body.Favorite, u.ID, body.Files)
		if err != nil {
			a.Error(w, http.StatusInternalServerError, "failed to update files")
			return
		}
		if tag.RowsAffected() == 0 {
			a.Error(w, http.StatusNotFound, "no files updated")
			return
		}
		a.WriteJSON(w, http.StatusOK, map[string]any{"count": tag.RowsAffected()})
		return
	}

	if body.Folder == nil || *body.Folder == "" {
		a.Error(w, http.StatusBadRequest, "folder is required")
		return
	}

	// Confirm the destination folder belongs to the user, and grab its name for
	// the response (matching the original's { count, name }).
	var folderName string
	if err := a.Store.Pool.QueryRow(ctx,
		`SELECT name FROM folders WHERE id=$1 AND user_id=$2`, *body.Folder, u.ID).Scan(&folderName); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			a.Error(w, http.StatusNotFound, "folder not found")
			return
		}
		a.Error(w, http.StatusInternalServerError, "failed to load folder")
		return
	}

	tag, err := a.Store.Pool.Exec(ctx,
		`UPDATE files SET folder_id=$1, updated_at=now() WHERE user_id=$2 AND id = ANY($3)`,
		*body.Folder, u.ID, body.Files)
	if err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to move files")
		return
	}
	if tag.RowsAffected() == 0 {
		a.Error(w, http.StatusNotFound, "no files moved")
		return
	}
	a.WriteJSON(w, http.StatusOK, map[string]any{"count": tag.RowsAffected(), "name": folderName})
}

type uexTransactionDeleteBody struct {
	Files                []string `json:"files"`
	DeleteDatasourceFile *bool    `json:"delete_datasourceFiles"`
}

// uexDeleteTransaction mirrors DELETE /api/user/files/transaction: bulk-delete
// the listed files scoped to the user and, when delete_datasourceFiles is set,
// remove the underlying objects from the datasource. Returns { count }.
func (a *App) uexDeleteTransaction(w http.ResponseWriter, r *http.Request) {
	u := UserFromContext(r.Context())
	ctx := r.Context()

	var body uexTransactionDeleteBody
	if err := a.ReadJSON(r, &body); err != nil {
		a.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(body.Files) == 0 {
		a.Error(w, http.StatusBadRequest, "files is required")
		return
	}

	// Load the owned files first so we know which datasource objects to remove
	// and so the delete is scoped to this user's files only.
	rows, err := a.Store.Pool.Query(ctx,
		`SELECT id, name FROM files WHERE user_id=$1 AND id = ANY($2)`, u.ID, body.Files)
	if err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to load files")
		return
	}
	type ownedFile struct{ id, name string }
	owned := []ownedFile{}
	for rows.Next() {
		var f ownedFile
		if err := rows.Scan(&f.id, &f.name); err != nil {
			rows.Close()
			a.Error(w, http.StatusInternalServerError, "failed to read files")
			return
		}
		owned = append(owned, f)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to read files")
		return
	}
	if len(owned) == 0 {
		a.Error(w, http.StatusNotFound, "no files deleted")
		return
	}

	ids := make([]string, 0, len(owned))
	for i := range owned {
		ids = append(ids, owned[i].id)
	}

	if body.DeleteDatasourceFile != nil && *body.DeleteDatasourceFile && a.DS != nil {
		for i := range owned {
			if err := a.DS.Delete(owned[i].name); err != nil {
				a.Log.Warn("failed to delete file from datasource", "name", owned[i].name, "err", err)
			}
		}
	}

	tag, err := a.Store.Pool.Exec(ctx,
		`DELETE FROM files WHERE user_id=$1 AND id = ANY($2)`, u.ID, ids)
	if err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to delete files")
		return
	}
	a.WriteJSON(w, http.StatusOK, map[string]any{"count": tag.RowsAffected()})
}

// --- raw bytes for an owned file ---

// uexGetFileRaw mirrors GET /api/user/files/:id/raw: stream the raw bytes of a
// file owned by the authenticated user, by id or short name. Streaming, HTTP
// Range support, Content-Disposition, password-token enforcement, expiry and
// max-views are all delegated to the shared serveRawByFile helper.
func (a *App) uexGetFileRaw(w http.ResponseWriter, r *http.Request) {
	u := UserFromContext(r.Context())
	id := chi.URLParam(r, "id")

	f, err := a.userLoadOwnedFile(r.Context(), id, u.ID)
	if err != nil {
		// The id may be a thumbnail object key for one of the user's own files.
		// The owner is authenticated and ownership-checked, so no folder gate.
		if fid, terr := a.Store.GetThumbnailFileID(r.Context(), id); terr == nil && fid != "" {
			if parent, perr := a.userLoadOwnedFile(r.Context(), fid, u.ID); perr == nil && parent != nil {
				a.streamThumbnail(w, r, id)
				return
			}
		}
		a.userHandleLookupErr(w, err, "file")
		return
	}
	a.serveRawByFile(w, r, f)
}

// --- url password verification ---

type uexUrlPasswordBody struct {
	Password string `json:"password"`
}

// uexUrlPassword mirrors POST /api/user/urls/:id/password: verify a
// password-protected short URL (by id, code or vanity) and, on success, return a
// 5-minute access token of type "url" for it. This endpoint is intentionally
// unauthenticated, like the original, so a logged-out visitor can unlock a link.
func (a *App) uexUrlPassword(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	var body uexUrlPasswordBody
	if err := a.ReadJSON(r, &body); err != nil {
		a.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	var urlID string
	var password *string
	err := a.Store.Pool.QueryRow(r.Context(),
		`SELECT id, password FROM urls WHERE id=$1 OR code=$1 OR vanity=$1 LIMIT 1`, id).
		Scan(&urlID, &password)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			a.Error(w, http.StatusNotFound, "url not found")
			return
		}
		a.Error(w, http.StatusInternalServerError, "failed to load url")
		return
	}
	// The original 404s (ApiError 9002) both for a missing URL and for one
	// without a password, and on an incorrect password.
	if password == nil || *password == "" {
		a.Error(w, http.StatusNotFound, "url not found")
		return
	}

	ok, verr := auth.VerifyPassword(*password, body.Password)
	if verr != nil || !ok {
		a.Error(w, http.StatusNotFound, "url not found")
		return
	}

	token, terr := auth.CreateAccessToken("url", urlID, a.Cfg.Core.Secret)
	if terr != nil {
		a.Error(w, http.StatusInternalServerError, "failed to create access token")
		return
	}
	a.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "token": token})
}

// uexRawJSON wraps stored JSONB bytes so they serialize as the original JSON
// object rather than a base64 string. Invalid/empty input becomes an empty
// object so the client's metadata.file access never dereferences null.
func uexRawJSON(b []byte) json.RawMessage {
	if len(b) == 0 || !json.Valid(b) {
		return json.RawMessage(`{}`)
	}
	return json.RawMessage(b)
}
