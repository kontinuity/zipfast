package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/lucsky/cuid"

	"zipfast/internal/auth"
	"zipfast/internal/db"
	"zipfast/internal/models"
)

// registerUserRoutes mounts the authenticated user resource routes under
// /api/user. The entire group is guarded by RequireUser so every handler can
// rely on UserFromContext returning a non-nil user, and every query is scoped to
// that user's id for security.
//
// Response shapes here are matched, field-for-field, to the original Zipline
// routes under src/server/routes/api/user/* because the vendored React client
// was written against those contracts. In particular:
//   - File/Url password fields are emitted as booleans (presence), never the hash.
//   - File responses include a computed "url", a "tags" array, and a "thumbnail".
//   - Folder responses include "_count" {children, files} and (unless noincl) "files".
//   - Tag responses include a "files" array of { id }.
//   - List endpoints return bare arrays where the client expects arrays, and
//     object-wrapped payloads ({page,total,pages} / {token} / {success}) elsewhere.
func (a *App) registerUserRoutes(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(a.RequireUser)

		// Account.
		r.Get("/api/user", a.userGetSelf)
		r.Patch("/api/user", a.userPatchSelf)

		// API token.
		r.Get("/api/user/token", a.userGetToken)
		r.Patch("/api/user/token", a.userPatchToken)

		// Avatar (base64 data URL).
		r.Get("/api/user/avatar", a.userGetAvatar)

		// Files.
		r.Get("/api/user/files", a.userListFiles)
		r.Get("/api/user/files/{id}", a.userGetFile)
		r.Patch("/api/user/files/{id}", a.userPatchFile)
		r.Delete("/api/user/files/{id}", a.userDeleteFile)
		r.Post("/api/user/files/{id}/password", a.userFilePassword)

		// Folders.
		r.Get("/api/user/folders", a.userListFolders)
		r.Post("/api/user/folders", a.userCreateFolder)
		r.Get("/api/user/folders/{id}", a.userGetFolder)
		r.Patch("/api/user/folders/{id}", a.userPatchFolder)
		r.Delete("/api/user/folders/{id}", a.userDeleteFolder)

		// Tags.
		r.Get("/api/user/tags", a.userListTags)
		r.Post("/api/user/tags", a.userCreateTag)
		r.Patch("/api/user/tags/{id}", a.userPatchTag)
		r.Delete("/api/user/tags/{id}", a.userDeleteTag)

		// URLs.
		r.Get("/api/user/urls", a.userListURLs)
		r.Post("/api/user/urls", a.userCreateURL)
		r.Get("/api/user/urls/{id}", a.userGetURL)
		r.Patch("/api/user/urls/{id}", a.userPatchURL)
		r.Delete("/api/user/urls/{id}", a.userDeleteURL)

		// Stats & recent.
		r.Get("/api/user/stats", a.userStats)
		r.Get("/api/user/recent", a.userRecent)
	})
}

// --- account ---

// userGetSelf mirrors GET /api/user: { user } (token is sourced from the cookie
// in the original and is therefore not part of this JSON body).
func (a *App) userGetSelf(w http.ResponseWriter, r *http.Request) {
	u := UserFromContext(r.Context())
	a.WriteJSON(w, http.StatusOK, map[string]any{"user": u})
}

type userPatchSelfBody struct {
	Username *string               `json:"username"`
	Password *string               `json:"password"`
	Avatar   *string               `json:"avatar"`
	View     *models.UserViewEmbed `json:"view"`
}

func (a *App) userPatchSelf(w http.ResponseWriter, r *http.Request) {
	u := UserFromContext(r.Context())

	var body userPatchSelfBody
	if err := a.ReadJSON(r, &body); err != nil {
		a.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	sets := newUserSetBuilder()
	if body.Username != nil {
		sets.add("username", *body.Username)
	}
	if body.Password != nil && *body.Password != "" {
		hashed, err := auth.HashPassword(*body.Password)
		if err != nil {
			a.Error(w, http.StatusInternalServerError, "failed to hash password")
			return
		}
		sets.add("password", hashed)
	}
	if body.Avatar != nil {
		sets.add("avatar", *body.Avatar)
	}
	if body.View != nil {
		viewJSON, _ := json.Marshal(body.View)
		if len(viewJSON) == 0 {
			viewJSON = []byte("{}")
		}
		sets.add("view", viewJSON)
	}

	if !sets.empty() {
		sets.add("updated_at", nowExpr{})
		q, args := sets.build("UPDATE users SET ", " WHERE id=$%d", u.ID)
		if _, err := a.Store.Pool.Exec(r.Context(), q, args...); err != nil {
			a.Error(w, http.StatusInternalServerError, "failed to update user")
			return
		}
	}

	updated, err := a.Store.GetUserByID(r.Context(), u.ID)
	if err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to load user")
		return
	}
	a.WriteJSON(w, http.StatusOK, map[string]any{"user": updated})
}

// userGetAvatar mirrors GET /api/user/avatar: the current user's avatar as a
// base64 data URL string. Unlike the original (which 404s when unset), we return
// 204 No Content so a user without an avatar doesn't generate a noisy 404 on
// every page load; the client renders its placeholder either way.
func (a *App) userGetAvatar(w http.ResponseWriter, r *http.Request) {
	u := UserFromContext(r.Context())

	var avatar *string
	if err := a.Store.Pool.QueryRow(r.Context(),
		`SELECT avatar FROM users WHERE id=$1`, u.ID).Scan(&avatar); err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to load avatar")
		return
	}
	if avatar == nil || *avatar == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "private, max-age=60")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(*avatar))
}

// --- token ---

// userGetToken mirrors GET /api/user/token: { token } (encrypted).
func (a *App) userGetToken(w http.ResponseWriter, r *http.Request) {
	u := UserFromContext(r.Context())
	enc, err := auth.EncryptToken(u.Token, a.Cfg.Core.Secret)
	if err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to encrypt token")
		return
	}
	a.WriteJSON(w, http.StatusOK, map[string]any{"token": enc})
}

// userPatchToken mirrors PATCH /api/user/token: { user, token } where token is
// the freshly-rotated, encrypted token.
func (a *App) userPatchToken(w http.ResponseWriter, r *http.Request) {
	u := UserFromContext(r.Context())
	tok := auth.CreateToken()
	if _, err := a.Store.Pool.Exec(r.Context(),
		`UPDATE users SET token=$1, updated_at=now() WHERE id=$2`, tok, u.ID); err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to regenerate token")
		return
	}

	enc, err := auth.EncryptToken(tok, a.Cfg.Core.Secret)
	if err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to encrypt token")
		return
	}

	updated, err := a.Store.GetUserByID(r.Context(), u.ID)
	if err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to load user")
		return
	}
	a.WriteJSON(w, http.StatusOK, map[string]any{"user": updated, "token": enc})
}

// --- files ---

func (a *App) userListFiles(w http.ResponseWriter, r *http.Request) {
	u := UserFromContext(r.Context())
	ctx := r.Context()

	page := queryInt(r, "page", 1)
	if page < 1 {
		page = 1
	}
	perPage := queryInt(r, "perpage", 15)
	if perPage < 1 {
		perPage = 15
	}
	filter := r.URL.Query().Get("filter")
	favoriteOnly := r.URL.Query().Get("favorite") == "true"
	sortBy := userFileSortColumn(r.URL.Query().Get("sortBy"))
	order := userSortOrder(r.URL.Query().Get("order"))
	searchField := r.URL.Query().Get("searchField")
	searchQuery := r.URL.Query().Get("searchQuery")
	folder := r.URL.Query().Get("folder")

	where := "f.user_id=$1"
	args := []any{u.ID}

	if filter == "dashboard" {
		where += " AND (f.type LIKE 'image/%' OR f.type LIKE 'video/%' OR f.type LIKE 'audio/%' OR f.type LIKE 'text/%')"
	}
	if favoriteOnly && filter != "all" {
		where += " AND f.favorite = true"
	}
	if folder != "" {
		args = append(args, folder)
		where += " AND f.folder_id = $" + strconv.Itoa(len(args))
	}

	// Search mode: returns { page, search } (no total/pages), matching the original.
	if searchQuery != "" {
		switch searchField {
		case "name", "originalName", "type", "id":
			col := map[string]string{
				"name":         "f.name",
				"originalName": "f.original_name",
				"type":         "f.type",
				"id":           "f.id",
			}[searchField]
			args = append(args, "%"+searchQuery+"%")
			where += " AND " + col + " ILIKE $" + strconv.Itoa(len(args))
		case "tags":
			// Files carrying ALL of the requested tag ids.
			tagIDs := splitCSV(searchQuery)
			if len(tagIDs) == 0 {
				a.WriteJSON(w, http.StatusOK, map[string]any{
					"page":   []any{},
					"search": map[string]any{"field": "tags", "query": tagIDs},
				})
				return
			}
			args = append(args, tagIDs, len(tagIDs))
			where += " AND f.id IN (SELECT ft.file_id FROM file_tags ft WHERE ft.tag_id = ANY($" +
				strconv.Itoa(len(args)-1) + ") GROUP BY ft.file_id HAVING COUNT(DISTINCT ft.tag_id) = $" +
				strconv.Itoa(len(args)) + ")"
		default:
			args = append(args, "%"+searchQuery+"%")
			where += " AND f.name ILIKE $" + strconv.Itoa(len(args))
		}

		offset := (page - 1) * perPage
		args = append(args, perPage, offset)
		rows, err := a.Store.Pool.Query(ctx,
			`SELECT `+userFileColumns+` FROM files f WHERE `+where+
				` ORDER BY `+sortBy+` `+order+` LIMIT $`+strconv.Itoa(len(args)-1)+
				` OFFSET $`+strconv.Itoa(len(args)), args...)
		if err != nil {
			a.Error(w, http.StatusInternalServerError, "failed to query files")
			return
		}
		files, err := userScanFiles(rows)
		rows.Close()
		if err != nil {
			a.Error(w, http.StatusInternalServerError, "failed to read files")
			return
		}
		if err := a.userHydrateFiles(ctx, files); err != nil {
			a.Error(w, http.StatusInternalServerError, "failed to read files")
			return
		}

		var queryOut any = searchQuery
		if searchField == "tags" {
			queryOut = splitCSV(searchQuery)
		}
		field := searchField
		if field == "" {
			field = "name"
		}
		a.WriteJSON(w, http.StatusOK, map[string]any{
			"page":   a.fileResponses(r, files),
			"search": map[string]any{"field": field, "query": queryOut},
		})
		return
	}

	var total int
	if err := a.Store.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM files f WHERE `+where, args...).Scan(&total); err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to count files")
		return
	}

	offset := (page - 1) * perPage
	args = append(args, perPage, offset)
	rows, err := a.Store.Pool.Query(ctx,
		`SELECT `+userFileColumns+` FROM files f WHERE `+where+
			` ORDER BY `+sortBy+` `+order+` LIMIT $`+strconv.Itoa(len(args)-1)+
			` OFFSET $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to query files")
		return
	}
	files, err := userScanFiles(rows)
	rows.Close()
	if err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to read files")
		return
	}
	if err := a.userHydrateFiles(ctx, files); err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to read files")
		return
	}

	pages := 0
	if total > 0 {
		pages = (total + perPage - 1) / perPage
	}
	a.WriteJSON(w, http.StatusOK, map[string]any{
		"page":  a.fileResponses(r, files),
		"total": total,
		"pages": pages,
	})
}

func (a *App) userGetFile(w http.ResponseWriter, r *http.Request) {
	u := UserFromContext(r.Context())
	id := chi.URLParam(r, "id")

	f, err := a.userLoadOwnedFile(r.Context(), id, u.ID)
	if err != nil {
		a.userHandleLookupErr(w, err, "file")
		return
	}
	if err := a.userHydrateFile(r.Context(), f); err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to load file")
		return
	}
	a.WriteJSON(w, http.StatusOK, a.fileResponse(r, f))
}

type userPatchFileBody struct {
	Favorite     *bool     `json:"favorite"`
	MaxViews     *int      `json:"maxViews"`
	Password     *string   `json:"password"`
	OriginalName *string   `json:"originalName"`
	Type         *string   `json:"type"`
	Anonymous    *bool     `json:"anonymous"`
	Name         *string   `json:"name"`
	Tags         *[]string `json:"tags"`
	FolderID     *string   `json:"folderId"`
}

func (a *App) userPatchFile(w http.ResponseWriter, r *http.Request) {
	u := UserFromContext(r.Context())
	id := chi.URLParam(r, "id")
	ctx := r.Context()

	existing, err := a.userLoadOwnedFile(ctx, id, u.ID)
	if err != nil {
		a.userHandleLookupErr(w, err, "file")
		return
	}

	var body userPatchFileBody
	if err := a.ReadJSON(r, &body); err != nil {
		a.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	sets := newUserSetBuilder()
	if body.Favorite != nil {
		sets.add("favorite", *body.Favorite)
	}
	if body.MaxViews != nil {
		sets.add("max_views", *body.MaxViews)
	}
	if body.OriginalName != nil {
		sets.add("original_name", *body.OriginalName)
	}
	if body.Type != nil {
		sets.add("type", *body.Type)
	}
	if body.Anonymous != nil {
		sets.add("anonymous", *body.Anonymous)
	}
	if body.Name != nil {
		sets.add("name", *body.Name)
	}
	if body.Password != nil {
		if *body.Password == "" {
			sets.add("password", nil)
		} else {
			hashed, err := auth.HashPassword(*body.Password)
			if err != nil {
				a.Error(w, http.StatusInternalServerError, "failed to hash password")
				return
			}
			sets.add("password", hashed)
		}
	}
	if body.FolderID != nil {
		if *body.FolderID == "" {
			sets.add("folder_id", nil)
		} else {
			sets.add("folder_id", *body.FolderID)
		}
	}

	if body.Name != nil && *body.Name != "" && *body.Name != existing.Name && a.DS != nil {
		if err := a.DS.Rename(existing.Name, *body.Name); err != nil {
			a.Log.Warn("failed to rename file in datasource", "from", existing.Name, "to", *body.Name, "err", err)
		}
	}

	if !sets.empty() {
		sets.add("updated_at", nowExpr{})
		q, args := sets.build("UPDATE files SET ", " WHERE id=$%d AND user_id=$%d", existing.ID, u.ID)
		if _, err := a.Store.Pool.Exec(ctx, q, args...); err != nil {
			a.Error(w, http.StatusInternalServerError, "failed to update file")
			return
		}
	}

	// Replace the file<->tag associations when tags are provided.
	if body.Tags != nil {
		if _, err := a.Store.Pool.Exec(ctx, `DELETE FROM file_tags WHERE file_id=$1`, existing.ID); err != nil {
			a.Error(w, http.StatusInternalServerError, "failed to update tags")
			return
		}
		for _, tid := range *body.Tags {
			if tid == "" {
				continue
			}
			if _, err := a.Store.Pool.Exec(ctx,
				`INSERT INTO file_tags (file_id, tag_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
				existing.ID, tid); err != nil {
				a.Error(w, http.StatusInternalServerError, "failed to update tags")
				return
			}
		}
	}

	updated, err := a.userLoadOwnedFile(ctx, existing.ID, u.ID)
	if err != nil {
		a.userHandleLookupErr(w, err, "file")
		return
	}
	if err := a.userHydrateFile(ctx, updated); err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to load file")
		return
	}
	a.WriteJSON(w, http.StatusOK, a.fileResponse(r, updated))
}

func (a *App) userDeleteFile(w http.ResponseWriter, r *http.Request) {
	u := UserFromContext(r.Context())
	id := chi.URLParam(r, "id")
	ctx := r.Context()

	f, err := a.userLoadOwnedFile(ctx, id, u.ID)
	if err != nil {
		a.userHandleLookupErr(w, err, "file")
		return
	}
	if err := a.userHydrateFile(ctx, f); err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to load file")
		return
	}

	if _, err := a.Store.Pool.Exec(ctx,
		`DELETE FROM files WHERE id=$1 AND user_id=$2`, f.ID, u.ID); err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to delete file")
		return
	}

	if a.DS != nil {
		if err := a.DS.Delete(f.Name); err != nil {
			a.Log.Warn("failed to delete file from datasource", "name", f.Name, "err", err)
		}
	}

	// The original returns the deleted file object.
	a.WriteJSON(w, http.StatusOK, a.fileResponse(r, f))
}

type userFilePasswordBody struct {
	Password string `json:"password"`
}

// userFilePassword mirrors POST /api/user/files/:id/password: { success, token }.
func (a *App) userFilePassword(w http.ResponseWriter, r *http.Request) {
	u := UserFromContext(r.Context())
	id := chi.URLParam(r, "id")

	var body userFilePasswordBody
	if err := a.ReadJSON(r, &body); err != nil {
		a.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	f, err := a.userLoadOwnedFile(r.Context(), id, u.ID)
	if err != nil {
		a.userHandleLookupErr(w, err, "file")
		return
	}

	if f.Password == nil || *f.Password == "" {
		a.Error(w, http.StatusNotFound, "file not found")
		return
	}

	ok, err := auth.VerifyPassword(*f.Password, body.Password)
	if err != nil || !ok {
		a.Error(w, http.StatusForbidden, "incorrect password")
		return
	}

	token, err := auth.CreateAccessToken("file", f.ID, a.Cfg.Core.Secret)
	if err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to create access token")
		return
	}
	a.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "token": token})
}

// --- folders ---

const userFolderColumns = `id, created_at, updated_at, name, public, allow_uploads, parent_id, user_id, password`

func userScanFolder(row pgx.Row) (*models.Folder, error) {
	var f models.Folder
	if err := row.Scan(&f.ID, &f.CreatedAt, &f.UpdatedAt, &f.Name, &f.Public,
		&f.AllowUploads, &f.ParentID, &f.UserID, &f.Password); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, db.ErrNotFound
		}
		return nil, err
	}
	return &f, nil
}

func (a *App) userListFolders(w http.ResponseWriter, r *http.Request) {
	u := UserFromContext(r.Context())
	ctx := r.Context()

	noincl := r.URL.Query().Get("noincl") == "true"
	root := r.URL.Query().Get("root") == "true"
	parentID := r.URL.Query().Get("parentId")

	where := "user_id=$1"
	args := []any{u.ID}
	if root {
		where += " AND parent_id IS NULL"
	}
	if parentID != "" {
		args = append(args, parentID)
		where += " AND parent_id = $" + strconv.Itoa(len(args))
	}

	rows, err := a.Store.Pool.Query(ctx,
		`SELECT `+userFolderColumns+` FROM folders WHERE `+where+` ORDER BY created_at DESC`, args...)
	if err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to query folders")
		return
	}
	defer rows.Close()

	folders := []models.Folder{}
	for rows.Next() {
		f, err := userScanFolder(rows)
		if err != nil {
			a.Error(w, http.StatusInternalServerError, "failed to read folders")
			return
		}
		folders = append(folders, *f)
	}
	if err := rows.Err(); err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to read folders")
		return
	}

	out := make([]map[string]any, 0, len(folders))
	for i := range folders {
		resp, err := a.folderResponse(r, &folders[i], !noincl)
		if err != nil {
			a.Error(w, http.StatusInternalServerError, "failed to read folders")
			return
		}
		out = append(out, resp)
	}
	a.WriteJSON(w, http.StatusOK, out)
}

type userCreateFolderBody struct {
	Name     string   `json:"name"`
	IsPublic *bool    `json:"isPublic"`
	Files    []string `json:"files"`
	ParentID *string  `json:"parentId"`
}

func (a *App) userCreateFolder(w http.ResponseWriter, r *http.Request) {
	u := UserFromContext(r.Context())
	ctx := r.Context()

	var body userCreateFolderBody
	if err := a.ReadJSON(r, &body); err != nil {
		a.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(body.Name) == "" {
		a.Error(w, http.StatusBadRequest, "name is required")
		return
	}

	public := false
	if body.IsPublic != nil {
		public = *body.IsPublic
	}

	id := cuid.New()
	row := a.Store.Pool.QueryRow(ctx,
		`INSERT INTO folders (id, created_at, updated_at, name, public, allow_uploads, parent_id, user_id)
		 VALUES ($1, now(), now(), $2, $3, false, $4, $5)
		 RETURNING `+userFolderColumns,
		id, body.Name, public, body.ParentID, u.ID)
	folder, err := userScanFolder(row)
	if err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to create folder")
		return
	}

	// Attach any provided files that belong to the current user.
	for _, fid := range body.Files {
		if fid == "" {
			continue
		}
		if _, err := a.Store.Pool.Exec(ctx,
			`UPDATE files SET folder_id=$1 WHERE id=$2 AND user_id=$3`, folder.ID, fid, u.ID); err != nil {
			a.Error(w, http.StatusInternalServerError, "failed to attach files")
			return
		}
	}

	resp, err := a.folderResponse(r, folder, true)
	if err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to create folder")
		return
	}
	a.WriteJSON(w, http.StatusOK, resp)
}

func (a *App) userGetFolder(w http.ResponseWriter, r *http.Request) {
	u := UserFromContext(r.Context())
	id := chi.URLParam(r, "id")
	noincl := r.URL.Query().Get("noincl") == "true"

	row := a.Store.Pool.QueryRow(r.Context(),
		`SELECT `+userFolderColumns+` FROM folders WHERE id=$1 AND user_id=$2`, id, u.ID)
	folder, err := userScanFolder(row)
	if err != nil {
		a.userHandleLookupErr(w, err, "folder")
		return
	}

	resp, err := a.folderResponse(r, folder, !noincl)
	if err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to load folder")
		return
	}
	// The detail endpoint additionally includes children and a parent chain.
	children, err := a.folderChildren(r.Context(), folder.ID)
	if err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to load folder")
		return
	}
	resp["children"] = children
	if folder.ParentID != nil {
		chain, err := a.folderParentChain(r.Context(), *folder.ParentID)
		if err != nil {
			a.Error(w, http.StatusInternalServerError, "failed to load folder")
			return
		}
		resp["parent"] = chain
	} else {
		resp["parent"] = nil
	}
	a.WriteJSON(w, http.StatusOK, resp)
}

type userPatchFolderBody struct {
	Name         *string `json:"name"`
	IsPublic     *bool   `json:"isPublic"`
	AllowUploads *bool   `json:"allowUploads"`
	ParentID     *string `json:"parentId"`
	// Password sets (non-empty) or clears (empty string) the folder gate
	// password. Omitting the field leaves it unchanged.
	Password *string `json:"password"`
}

func (a *App) userPatchFolder(w http.ResponseWriter, r *http.Request) {
	u := UserFromContext(r.Context())
	id := chi.URLParam(r, "id")
	ctx := r.Context()

	var body userPatchFolderBody
	if err := a.ReadJSON(r, &body); err != nil {
		a.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	sets := newUserSetBuilder()
	if body.Name != nil {
		sets.add("name", *body.Name)
	}
	if body.IsPublic != nil {
		sets.add("public", *body.IsPublic)
	}
	if body.AllowUploads != nil {
		sets.add("allow_uploads", *body.AllowUploads)
	}
	if body.ParentID != nil {
		if *body.ParentID == "" {
			sets.add("parent_id", nil)
		} else {
			sets.add("parent_id", *body.ParentID)
		}
	}
	if body.Password != nil {
		if *body.Password == "" {
			sets.add("password", nil)
		} else {
			hash, herr := auth.HashPassword(*body.Password)
			if herr != nil {
				a.Error(w, http.StatusInternalServerError, "failed to hash password")
				return
			}
			sets.add("password", hash)
		}
	}
	if sets.empty() {
		a.Error(w, http.StatusBadRequest, "no fields to update")
		return
	}
	sets.add("updated_at", nowExpr{})

	q, args := sets.build("UPDATE folders SET ", " WHERE id=$%d AND user_id=$%d RETURNING "+userFolderColumns, id, u.ID)
	folder, err := userScanFolder(a.Store.Pool.QueryRow(ctx, q, args...))
	if err != nil {
		a.userHandleLookupErr(w, err, "folder")
		return
	}
	resp, err := a.folderResponse(r, folder, false)
	if err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to load folder")
		return
	}
	a.WriteJSON(w, http.StatusOK, resp)
}

type userDeleteFolderBody struct {
	Delete         string  `json:"delete"`
	ID             *string `json:"id"`
	ChildrenAction *string `json:"childrenAction"`
	TargetFolderID *string `json:"targetFolderId"`
}

func (a *App) userDeleteFolder(w http.ResponseWriter, r *http.Request) {
	u := UserFromContext(r.Context())
	id := chi.URLParam(r, "id")
	ctx := r.Context()

	// Confirm ownership of the folder being acted on.
	owned := a.Store.Pool.QueryRow(ctx,
		`SELECT `+userFolderColumns+` FROM folders WHERE id=$1 AND user_id=$2`, id, u.ID)
	folder, err := userScanFolder(owned)
	if err != nil {
		a.userHandleLookupErr(w, err, "folder")
		return
	}

	var body userDeleteFolderBody
	if err := a.ReadJSON(r, &body); err != nil {
		// Default to deleting the folder itself when no/invalid body is provided.
		body = userDeleteFolderBody{Delete: "folder"}
	}
	if body.Delete == "" {
		body.Delete = "folder"
	}

	if body.Delete == "file" {
		if body.ID == nil || *body.ID == "" {
			a.Error(w, http.StatusBadRequest, "id is required")
			return
		}
		if _, err := a.Store.Pool.Exec(ctx,
			`UPDATE files SET folder_id=NULL, updated_at=now() WHERE id=$1 AND folder_id=$2 AND user_id=$3`,
			*body.ID, folder.ID, u.ID); err != nil {
			a.Error(w, http.StatusInternalServerError, "failed to remove file from folder")
			return
		}
		resp, err := a.folderResponse(r, folder, false)
		if err != nil {
			a.Error(w, http.StatusInternalServerError, "failed to load folder")
			return
		}
		a.WriteJSON(w, http.StatusOK, map[string]any{"folder": resp})
		return
	}

	// Deleting the folder. Reparent or cascade children/files per childrenAction.
	action := ""
	if body.ChildrenAction != nil {
		action = *body.ChildrenAction
	}
	switch action {
	case "root":
		_, _ = a.Store.Pool.Exec(ctx, `UPDATE folders SET parent_id=NULL WHERE parent_id=$1`, folder.ID)
		_, _ = a.Store.Pool.Exec(ctx, `UPDATE files SET folder_id=NULL WHERE folder_id=$1`, folder.ID)
	case "folder":
		if body.TargetFolderID != nil && *body.TargetFolderID != "" {
			_, _ = a.Store.Pool.Exec(ctx, `UPDATE folders SET parent_id=$1 WHERE parent_id=$2`, *body.TargetFolderID, folder.ID)
			_, _ = a.Store.Pool.Exec(ctx, `UPDATE files SET folder_id=$1 WHERE folder_id=$2`, *body.TargetFolderID, folder.ID)
		}
	case "cascade", "cascade-files":
		if action == "cascade-files" {
			// Best-effort delete of file blobs before the rows cascade away.
			names, _ := a.collectFolderFileNames(ctx, folder.ID)
			if a.DS != nil {
				for _, n := range names {
					if err := a.DS.Delete(n); err != nil {
						a.Log.Warn("failed to delete file from datasource", "name", n, "err", err)
					}
				}
			}
		}
		// Child folders reference this row via ON DELETE SET NULL; for a full
		// cascade we delete descendant folders explicitly.
		_ = a.deleteFolderTree(ctx, folder.ID, action == "cascade-files")
	}

	if _, err := a.Store.Pool.Exec(ctx,
		`DELETE FROM folders WHERE id=$1 AND user_id=$2`, folder.ID, u.ID); err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to delete folder")
		return
	}
	a.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}

// --- tags ---

const userTagColumns = `id, created_at, updated_at, name, color, user_id`

func userScanTag(row pgx.Row) (*models.Tag, error) {
	var t models.Tag
	if err := row.Scan(&t.ID, &t.CreatedAt, &t.UpdatedAt, &t.Name, &t.Color, &t.UserID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, db.ErrNotFound
		}
		return nil, err
	}
	return &t, nil
}

func (a *App) userListTags(w http.ResponseWriter, r *http.Request) {
	u := UserFromContext(r.Context())
	ctx := r.Context()
	rows, err := a.Store.Pool.Query(ctx,
		`SELECT `+userTagColumns+` FROM tags WHERE user_id=$1 ORDER BY created_at DESC`, u.ID)
	if err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to query tags")
		return
	}
	defer rows.Close()

	tags := []models.Tag{}
	for rows.Next() {
		t, err := userScanTag(rows)
		if err != nil {
			a.Error(w, http.StatusInternalServerError, "failed to read tags")
			return
		}
		tags = append(tags, *t)
	}
	if err := rows.Err(); err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to read tags")
		return
	}

	out := make([]map[string]any, 0, len(tags))
	for i := range tags {
		resp, err := a.tagResponse(ctx, &tags[i])
		if err != nil {
			a.Error(w, http.StatusInternalServerError, "failed to read tags")
			return
		}
		out = append(out, resp)
	}
	a.WriteJSON(w, http.StatusOK, out)
}

type userCreateTagBody struct {
	Name  string `json:"name"`
	Color string `json:"color"`
}

func (a *App) userCreateTag(w http.ResponseWriter, r *http.Request) {
	u := UserFromContext(r.Context())
	ctx := r.Context()

	var body userCreateTagBody
	if err := a.ReadJSON(r, &body); err != nil {
		a.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(body.Name) == "" {
		a.Error(w, http.StatusBadRequest, "name is required")
		return
	}

	id := cuid.New()
	row := a.Store.Pool.QueryRow(ctx,
		`INSERT INTO tags (id, created_at, updated_at, name, color, user_id)
		 VALUES ($1, now(), now(), $2, $3, $4)
		 RETURNING `+userTagColumns,
		id, body.Name, body.Color, u.ID)
	tag, err := userScanTag(row)
	if err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to create tag")
		return
	}
	resp, err := a.tagResponse(ctx, tag)
	if err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to create tag")
		return
	}
	a.WriteJSON(w, http.StatusOK, resp)
}

type userPatchTagBody struct {
	Name  *string `json:"name"`
	Color *string `json:"color"`
}

func (a *App) userPatchTag(w http.ResponseWriter, r *http.Request) {
	u := UserFromContext(r.Context())
	id := chi.URLParam(r, "id")
	ctx := r.Context()

	var body userPatchTagBody
	if err := a.ReadJSON(r, &body); err != nil {
		a.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	sets := newUserSetBuilder()
	if body.Name != nil {
		sets.add("name", *body.Name)
	}
	if body.Color != nil {
		sets.add("color", *body.Color)
	}
	if sets.empty() {
		a.Error(w, http.StatusBadRequest, "no fields to update")
		return
	}
	sets.add("updated_at", nowExpr{})

	q, args := sets.build("UPDATE tags SET ", " WHERE id=$%d AND user_id=$%d RETURNING "+userTagColumns, id, u.ID)
	tag, err := userScanTag(a.Store.Pool.QueryRow(ctx, q, args...))
	if err != nil {
		a.userHandleLookupErr(w, err, "tag")
		return
	}
	resp, err := a.tagResponse(ctx, tag)
	if err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to load tag")
		return
	}
	a.WriteJSON(w, http.StatusOK, resp)
}

func (a *App) userDeleteTag(w http.ResponseWriter, r *http.Request) {
	u := UserFromContext(r.Context())
	id := chi.URLParam(r, "id")
	tag, err := a.Store.Pool.Exec(r.Context(),
		`DELETE FROM tags WHERE id=$1 AND user_id=$2`, id, u.ID)
	if err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to delete tag")
		return
	}
	if tag.RowsAffected() == 0 {
		a.Error(w, http.StatusNotFound, "tag not found")
		return
	}
	a.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}

// --- urls ---

func (a *App) userListURLs(w http.ResponseWriter, r *http.Request) {
	u := UserFromContext(r.Context())
	ctx := r.Context()

	searchField := r.URL.Query().Get("searchField")
	searchQuery := r.URL.Query().Get("searchQuery")

	where := "user_id=$1"
	args := []any{u.ID}
	if searchQuery != "" {
		col := map[string]string{
			"destination": "destination",
			"vanity":      "vanity",
			"code":        "code",
		}[searchField]
		if col == "" {
			col = "destination"
		}
		args = append(args, "%"+searchQuery+"%")
		where += " AND " + col + " ILIKE $" + strconv.Itoa(len(args))
	}

	rows, err := a.Store.Pool.Query(ctx,
		`SELECT `+userURLColumns+` FROM urls WHERE `+where+` ORDER BY created_at DESC`, args...)
	if err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to query urls")
		return
	}
	defer rows.Close()

	urls := []models.Url{}
	for rows.Next() {
		url, err := userScanURL(rows)
		if err != nil {
			a.Error(w, http.StatusInternalServerError, "failed to read urls")
			return
		}
		urls = append(urls, *url)
	}
	if err := rows.Err(); err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to read urls")
		return
	}

	out := make([]map[string]any, 0, len(urls))
	for i := range urls {
		out = append(out, urlResponse(&urls[i]))
	}
	a.WriteJSON(w, http.StatusOK, out)
}

type userCreateURLBody struct {
	Destination string  `json:"destination"`
	Vanity      *string `json:"vanity"`
	MaxViews    *int    `json:"maxViews"`
	Password    *string `json:"password"`
	Enabled     *bool   `json:"enabled"`
}

func (a *App) userCreateURL(w http.ResponseWriter, r *http.Request) {
	u := UserFromContext(r.Context())

	var body userCreateURLBody
	if err := a.ReadJSON(r, &body); err != nil {
		a.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.Destination == "" {
		a.Error(w, http.StatusBadRequest, "destination is required")
		return
	}

	codeLen := a.Cfg.Urls.Length
	if codeLen <= 0 {
		codeLen = 6
	}
	code := auth.RandomString(codeLen)

	var vanity *string
	if body.Vanity != nil && *body.Vanity != "" {
		vanity = body.Vanity
	}

	// max-views and password may also arrive via ShareX-style headers.
	maxViews := body.MaxViews
	if hv := r.Header.Get("x-zipline-max-views"); hv != "" {
		if n, err := strconv.Atoi(hv); err == nil {
			maxViews = &n
		}
	}

	var password *string
	rawPassword := ""
	if body.Password != nil && *body.Password != "" {
		rawPassword = *body.Password
	}
	if hp := r.Header.Get("x-zipline-password"); hp != "" {
		rawPassword = hp
	}
	if rawPassword != "" {
		hashed, err := auth.HashPassword(rawPassword)
		if err != nil {
			a.Error(w, http.StatusInternalServerError, "failed to hash password")
			return
		}
		password = &hashed
	}

	enabled := true
	if body.Enabled != nil {
		enabled = *body.Enabled
	}

	id := cuid.New()
	row := a.Store.Pool.QueryRow(r.Context(),
		`INSERT INTO urls (id, created_at, updated_at, code, vanity, destination, max_views, password, enabled, user_id)
		 VALUES ($1, now(), now(), $2, $3, $4, $5, $6, $7, $8)
		 RETURNING `+userURLColumns,
		id, code, vanity, body.Destination, maxViews, password, enabled, u.ID)
	url, err := userScanURL(row)
	if err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to create url")
		return
	}

	// Build the public-facing short URL. Domain may be overridden via header.
	domain := a.BaseURL(r)
	if hd := r.Header.Get("x-zipline-domain"); hd != "" {
		parts := strings.Split(hd, ",")
		host := strings.TrimSpace(parts[0])
		scheme := "http"
		if a.Cfg.Core.ReturnHTTPSURLs {
			scheme = "https"
		}
		domain = scheme + "://" + host
	}
	route := a.Cfg.Urls.Route
	prefix := ""
	if route != "/" && route != "" {
		prefix = route
	}
	slug := url.Code
	if url.Vanity != nil && *url.Vanity != "" {
		slug = *url.Vanity
	}
	responseURL := domain + prefix + "/" + slug

	// Plain-text response for no-json clients.
	if nj := r.Header.Get("x-zipline-no-json"); strings.EqualFold(nj, "true") {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(responseURL))
		return
	}

	resp := urlResponse(url)
	resp["url"] = responseURL
	a.WriteJSON(w, http.StatusOK, resp)
}

func (a *App) userGetURL(w http.ResponseWriter, r *http.Request) {
	u := UserFromContext(r.Context())
	id := chi.URLParam(r, "id")
	row := a.Store.Pool.QueryRow(r.Context(),
		`SELECT `+userURLColumns+` FROM urls WHERE id=$1 AND user_id=$2`, id, u.ID)
	url, err := userScanURL(row)
	if err != nil {
		a.userHandleLookupErr(w, err, "url")
		return
	}
	a.WriteJSON(w, http.StatusOK, urlResponse(url))
}

type userPatchURLBody struct {
	Destination *string `json:"destination"`
	Vanity      *string `json:"vanity"`
	MaxViews    *int    `json:"maxViews"`
	Password    *string `json:"password"`
	Enabled     *bool   `json:"enabled"`
}

func (a *App) userPatchURL(w http.ResponseWriter, r *http.Request) {
	u := UserFromContext(r.Context())
	id := chi.URLParam(r, "id")

	var body userPatchURLBody
	if err := a.ReadJSON(r, &body); err != nil {
		a.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	sets := newUserSetBuilder()
	if body.Destination != nil {
		sets.add("destination", *body.Destination)
	}
	if body.Vanity != nil {
		if *body.Vanity == "" {
			sets.add("vanity", nil)
		} else {
			sets.add("vanity", *body.Vanity)
		}
	}
	if body.MaxViews != nil {
		sets.add("max_views", *body.MaxViews)
	}
	if body.Password != nil {
		if *body.Password == "" {
			sets.add("password", nil)
		} else {
			hashed, err := auth.HashPassword(*body.Password)
			if err != nil {
				a.Error(w, http.StatusInternalServerError, "failed to hash password")
				return
			}
			sets.add("password", hashed)
		}
	}
	if body.Enabled != nil {
		sets.add("enabled", *body.Enabled)
	}
	if sets.empty() {
		a.Error(w, http.StatusBadRequest, "no fields to update")
		return
	}
	sets.add("updated_at", nowExpr{})

	q, args := sets.build("UPDATE urls SET ", " WHERE id=$%d AND user_id=$%d RETURNING "+userURLColumns, id, u.ID)
	url, err := userScanURL(a.Store.Pool.QueryRow(r.Context(), q, args...))
	if err != nil {
		a.userHandleLookupErr(w, err, "url")
		return
	}
	a.WriteJSON(w, http.StatusOK, urlResponse(url))
}

func (a *App) userDeleteURL(w http.ResponseWriter, r *http.Request) {
	u := UserFromContext(r.Context())
	id := chi.URLParam(r, "id")

	row := a.Store.Pool.QueryRow(r.Context(),
		`DELETE FROM urls WHERE id=$1 AND user_id=$2 RETURNING `+userURLColumns, id, u.ID)
	url, err := userScanURL(row)
	if err != nil {
		a.userHandleLookupErr(w, err, "url")
		return
	}
	// The original returns the deleted url object (password omitted).
	a.WriteJSON(w, http.StatusOK, urlResponse(url))
}

// --- stats & recent ---

func (a *App) userStats(w http.ResponseWriter, r *http.Request) {
	u := UserFromContext(r.Context())
	ctx := r.Context()

	var filesUploaded int
	var sumViews, sumSize, avgViews, avgSize float64
	if err := a.Store.Pool.QueryRow(ctx,
		`SELECT COUNT(*), COALESCE(SUM(views),0), COALESCE(SUM(size),0),
		        COALESCE(AVG(views),0), COALESCE(AVG(size),0)
		   FROM files WHERE user_id=$1`,
		u.ID).Scan(&filesUploaded, &sumViews, &sumSize, &avgViews, &avgSize); err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to compute file stats")
		return
	}

	var favoriteFiles int
	if err := a.Store.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM files WHERE user_id=$1 AND favorite=true`, u.ID).Scan(&favoriteFiles); err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to compute file stats")
		return
	}

	var urlsCreated int
	var urlViews float64
	if err := a.Store.Pool.QueryRow(ctx,
		`SELECT COUNT(*), COALESCE(SUM(views),0) FROM urls WHERE user_id=$1`,
		u.ID).Scan(&urlsCreated, &urlViews); err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to compute url stats")
		return
	}

	sortTypeCount := map[string]int{}
	rows, err := a.Store.Pool.Query(ctx, `SELECT type FROM files WHERE user_id=$1`, u.ID)
	if err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to compute file stats")
		return
	}
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			rows.Close()
			a.Error(w, http.StatusInternalServerError, "failed to compute file stats")
			return
		}
		sortTypeCount[t]++
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to compute file stats")
		return
	}

	a.WriteJSON(w, http.StatusOK, map[string]any{
		"filesUploaded":  filesUploaded,
		"favoriteFiles":  favoriteFiles,
		"views":          int64(sumViews),
		"avgViews":       avgViews,
		"storageUsed":    int64(sumSize),
		"avgStorageUsed": avgSize,
		"urlsCreated":    urlsCreated,
		"urlViews":       int64(urlViews),
		"sortTypeCount":  sortTypeCount,
	})
}

func (a *App) userRecent(w http.ResponseWriter, r *http.Request) {
	u := UserFromContext(r.Context())
	ctx := r.Context()

	take := queryInt(r, "take", 3)
	if take < 1 {
		take = 3
	}
	if take > 100 {
		take = 100
	}

	rows, err := a.Store.Pool.Query(ctx,
		`SELECT `+userFileColumns+` FROM files f WHERE f.user_id=$1 ORDER BY f.created_at DESC LIMIT $2`, u.ID, take)
	if err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to query recent files")
		return
	}
	files, err := userScanFiles(rows)
	rows.Close()
	if err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to read recent files")
		return
	}
	if err := a.userHydrateFiles(ctx, files); err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to read recent files")
		return
	}
	// The original returns a bare array of files.
	a.WriteJSON(w, http.StatusOK, a.fileResponses(r, files))
}

// --- response builders (control exact JSON shapes the client expects) ---

// fileResponse builds the JSON object for a single file. It mirrors the original
// fileSchema + cleanFile: password is a boolean (presence), a computed "url" is
// added, "tags" is always an array, and "thumbnail" is { path } or null.
func (a *App) fileResponse(r *http.Request, f *models.File) map[string]any {
	tags := f.Tags
	if tags == nil {
		tags = []models.Tag{}
	}
	tagList := make([]map[string]any, 0, len(tags))
	for i := range tags {
		tagList = append(tagList, map[string]any{
			"id":        tags[i].ID,
			"createdAt": tags[i].CreatedAt,
			"updatedAt": tags[i].UpdatedAt,
			"name":      tags[i].Name,
			"color":     tags[i].Color,
		})
	}

	var thumb any
	if f.Thumbnail != nil {
		thumb = map[string]any{"path": f.Thumbnail.Path}
	} else {
		thumb = nil
	}

	m := map[string]any{
		"id":           f.ID,
		"createdAt":    f.CreatedAt,
		"updatedAt":    f.UpdatedAt,
		"deletesAt":    f.DeletesAt,
		"favorite":     f.Favorite,
		"originalName": f.OriginalName,
		"name":         f.Name,
		"size":         f.Size,
		"type":         f.Type,
		"views":        f.Views,
		"maxViews":     f.MaxViews,
		"folderId":     f.FolderID,
		"anonymous":    f.Anonymous,
		"password":     f.Password != nil && *f.Password != "",
		"thumbnail":    thumb,
		"tags":         tagList,
		"url":          a.fileURL(f.Name),
	}
	return m
}

func (a *App) fileResponses(r *http.Request, files []models.File) []map[string]any {
	out := make([]map[string]any, 0, len(files))
	for i := range files {
		out = append(out, a.fileResponse(r, &files[i]))
	}
	return out
}

// fileURL mirrors formatRootUrl(config.files.route, name): a route-prefixed path.
func (a *App) fileURL(name string) string {
	route := a.Cfg.Files.Route
	if route == "/" {
		route = ""
	}
	return route + "/" + name
}

// urlResponse builds the JSON object for a URL with the password field redacted
// to a boolean (matching cleanUrlPasswords / omit:{password} in the original).
func urlResponse(u *models.Url) map[string]any {
	return map[string]any{
		"id":          u.ID,
		"createdAt":   u.CreatedAt,
		"updatedAt":   u.UpdatedAt,
		"code":        u.Code,
		"vanity":      u.Vanity,
		"destination": u.Destination,
		"views":       u.Views,
		"maxViews":    u.MaxViews,
		"password":    u.Password != nil && *u.Password != "",
		"enabled":     u.Enabled,
		"userId":      u.UserID,
	}
}

// folderResponse builds the JSON object for a folder including "_count"
// {children, files} and, when includeFiles is set, the folder's "files" array.
func (a *App) folderResponse(r *http.Request, f *models.Folder, includeFiles bool) (map[string]any, error) {
	ctx := r.Context()

	var childCount, fileCount int
	if err := a.Store.Pool.QueryRow(ctx,
		`SELECT (SELECT COUNT(*) FROM folders c WHERE c.parent_id=$1),
		        (SELECT COUNT(*) FROM files fi WHERE fi.folder_id=$1)`,
		f.ID).Scan(&childCount, &fileCount); err != nil {
		return nil, err
	}

	m := map[string]any{
		"id":                f.ID,
		"createdAt":         f.CreatedAt,
		"updatedAt":         f.UpdatedAt,
		"name":              f.Name,
		"public":            f.Public,
		"allowUploads":      f.AllowUploads,
		"parentId":          f.ParentID,
		"userId":            f.UserID,
		"passwordProtected": f.Password != nil && *f.Password != "",
		"_count": map[string]any{
			"children": childCount,
			"files":    fileCount,
		},
	}

	if includeFiles {
		rows, err := a.Store.Pool.Query(ctx,
			`SELECT `+userFileColumns+` FROM files f WHERE f.folder_id=$1 ORDER BY f.created_at DESC`, f.ID)
		if err != nil {
			return nil, err
		}
		files, err := userScanFiles(rows)
		rows.Close()
		if err != nil {
			return nil, err
		}
		if err := a.userHydrateFiles(ctx, files); err != nil {
			return nil, err
		}
		m["files"] = a.fileResponses(r, files)
	}

	return m, nil
}

// folderChildren returns child folders (each with their own _count) for the
// detail endpoint.
func (a *App) folderChildren(ctx context.Context, parentID string) ([]map[string]any, error) {
	rows, err := a.Store.Pool.Query(ctx,
		`SELECT `+userFolderColumns+` FROM folders WHERE parent_id=$1 ORDER BY created_at DESC`, parentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	children := []map[string]any{}
	var scanned []models.Folder
	for rows.Next() {
		f, err := userScanFolder(rows)
		if err != nil {
			return nil, err
		}
		scanned = append(scanned, *f)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for i := range scanned {
		var childCount, fileCount int
		if err := a.Store.Pool.QueryRow(ctx,
			`SELECT (SELECT COUNT(*) FROM folders c WHERE c.parent_id=$1),
			        (SELECT COUNT(*) FROM files fi WHERE fi.folder_id=$1)`,
			scanned[i].ID).Scan(&childCount, &fileCount); err != nil {
			return nil, err
		}
		children = append(children, map[string]any{
			"id":           scanned[i].ID,
			"createdAt":    scanned[i].CreatedAt,
			"updatedAt":    scanned[i].UpdatedAt,
			"name":         scanned[i].Name,
			"public":       scanned[i].Public,
			"allowUploads": scanned[i].AllowUploads,
			"parentId":     scanned[i].ParentID,
			"userId":       scanned[i].UserID,
			"_count": map[string]any{
				"children": childCount,
				"files":    fileCount,
			},
		})
	}
	return children, nil
}

// folderParentChain mirrors buildParentChain: a nested { id, name, parentId, parent }.
func (a *App) folderParentChain(ctx context.Context, parentID string) (map[string]any, error) {
	var id, name string
	var pid *string
	err := a.Store.Pool.QueryRow(ctx,
		`SELECT id, name, parent_id FROM folders WHERE id=$1`, parentID).Scan(&id, &name, &pid)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	m := map[string]any{"id": id, "name": name, "parentId": pid}
	if pid != nil {
		parent, err := a.folderParentChain(ctx, *pid)
		if err != nil {
			return nil, err
		}
		m["parent"] = parent
	} else {
		m["parent"] = nil
	}
	return m, nil
}

// tagResponse builds the JSON object for a tag including its "files" array of { id }.
func (a *App) tagResponse(ctx context.Context, t *models.Tag) (map[string]any, error) {
	rows, err := a.Store.Pool.Query(ctx, `SELECT file_id FROM file_tags WHERE tag_id=$1`, t.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	files := []map[string]any{}
	for rows.Next() {
		var fid string
		if err := rows.Scan(&fid); err != nil {
			return nil, err
		}
		files = append(files, map[string]any{"id": fid})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return map[string]any{
		"id":        t.ID,
		"createdAt": t.CreatedAt,
		"updatedAt": t.UpdatedAt,
		"name":      t.Name,
		"color":     t.Color,
		"files":     files,
	}, nil
}

// --- hydration helpers (load tags + thumbnail relations onto files) ---

// userHydrateFile loads the tags and thumbnail for a single file in place.
func (a *App) userHydrateFile(ctx context.Context, f *models.File) error {
	one := []models.File{*f}
	if err := a.userHydrateFiles(ctx, one); err != nil {
		return err
	}
	f.Tags = one[0].Tags
	f.Thumbnail = one[0].Thumbnail
	return nil
}

// userHydrateFiles loads the tags and thumbnail for each file in place.
func (a *App) userHydrateFiles(ctx context.Context, files []models.File) error {
	for i := range files {
		// tags
		rows, err := a.Store.Pool.Query(ctx,
			`SELECT t.id, t.created_at, t.updated_at, t.name, t.color, t.user_id
			   FROM tags t JOIN file_tags ft ON ft.tag_id = t.id
			  WHERE ft.file_id = $1
			  ORDER BY t.created_at DESC`, files[i].ID)
		if err != nil {
			return err
		}
		var tags []models.Tag
		for rows.Next() {
			var t models.Tag
			if err := rows.Scan(&t.ID, &t.CreatedAt, &t.UpdatedAt, &t.Name, &t.Color, &t.UserID); err != nil {
				rows.Close()
				return err
			}
			tags = append(tags, t)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		files[i].Tags = tags

		// thumbnail
		var th models.Thumbnail
		err = a.Store.Pool.QueryRow(ctx,
			`SELECT id, created_at, updated_at, path, file_id FROM thumbnails WHERE file_id=$1`,
			files[i].ID).Scan(&th.ID, &th.CreatedAt, &th.UpdatedAt, &th.Path, &th.FileID)
		if err == nil {
			files[i].Thumbnail = &th
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		} else {
			files[i].Thumbnail = nil
		}
	}
	return nil
}

// --- folder delete tree helpers ---

func (a *App) collectFolderFileNames(ctx context.Context, folderID string) ([]string, error) {
	var names []string
	rows, err := a.Store.Pool.Query(ctx, `SELECT name FROM files WHERE folder_id=$1`, folderID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			rows.Close()
			return nil, err
		}
		names = append(names, n)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Recurse into child folders.
	childRows, err := a.Store.Pool.Query(ctx, `SELECT id FROM folders WHERE parent_id=$1`, folderID)
	if err != nil {
		return names, err
	}
	var childIDs []string
	for childRows.Next() {
		var id string
		if err := childRows.Scan(&id); err != nil {
			childRows.Close()
			return names, err
		}
		childIDs = append(childIDs, id)
	}
	childRows.Close()
	for _, cid := range childIDs {
		more, err := a.collectFolderFileNames(ctx, cid)
		if err != nil {
			return names, err
		}
		names = append(names, more...)
	}
	return names, nil
}

func (a *App) deleteFolderTree(ctx context.Context, folderID string, deleteFiles bool) error {
	rows, err := a.Store.Pool.Query(ctx, `SELECT id FROM folders WHERE parent_id=$1`, folderID)
	if err != nil {
		return err
	}
	var childIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		childIDs = append(childIDs, id)
	}
	rows.Close()
	for _, cid := range childIDs {
		if err := a.deleteFolderTree(ctx, cid, deleteFiles); err != nil {
			return err
		}
	}
	if deleteFiles {
		if _, err := a.Store.Pool.Exec(ctx, `DELETE FROM files WHERE folder_id=$1`, folderID); err != nil {
			return err
		}
	}
	// Child folders are deleted by their own recursion above; delete this node.
	if _, err := a.Store.Pool.Exec(ctx, `DELETE FROM folders WHERE id=$1`, folderID); err != nil {
		return err
	}
	return nil
}

// --- shared file scanning (local to this file to avoid collisions) ---

const userFileColumns = `f.id, f.created_at, f.updated_at, f.deletes_at, f.name, f.original_name, f.size, f.type, f.views, f.max_views, f.favorite, f.password, f.anonymous, f.user_id, f.folder_id`

func userScanFile(row pgx.Row) (*models.File, error) {
	var f models.File
	if err := row.Scan(&f.ID, &f.CreatedAt, &f.UpdatedAt, &f.DeletesAt, &f.Name, &f.OriginalName,
		&f.Size, &f.Type, &f.Views, &f.MaxViews, &f.Favorite, &f.Password, &f.Anonymous,
		&f.UserID, &f.FolderID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, db.ErrNotFound
		}
		return nil, err
	}
	return &f, nil
}

func userScanFiles(rows pgx.Rows) ([]models.File, error) {
	files := []models.File{}
	for rows.Next() {
		f, err := userScanFile(rows)
		if err != nil {
			return nil, err
		}
		files = append(files, *f)
	}
	return files, rows.Err()
}

// userLoadOwnedFile loads a file scoped to a user, matching by id OR short name
// (the original matches on either), returning db.ErrNotFound when no owned file
// matches.
func (a *App) userLoadOwnedFile(ctx context.Context, id, userID string) (*models.File, error) {
	return userScanFile(a.Store.Pool.QueryRow(ctx,
		`SELECT `+userFileColumns+` FROM files f WHERE (f.id=$1 OR f.name=$1) AND f.user_id=$2 LIMIT 1`, id, userID))
}

const userURLColumns = `id, created_at, updated_at, code, vanity, destination, views, max_views, password, enabled, user_id`

func userScanURL(row pgx.Row) (*models.Url, error) {
	var u models.Url
	if err := row.Scan(&u.ID, &u.CreatedAt, &u.UpdatedAt, &u.Code, &u.Vanity, &u.Destination,
		&u.Views, &u.MaxViews, &u.Password, &u.Enabled, &u.UserID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, db.ErrNotFound
		}
		return nil, err
	}
	return &u, nil
}

// userHandleLookupErr writes a 404 for not-found lookups and a 500 otherwise.
func (a *App) userHandleLookupErr(w http.ResponseWriter, err error, resource string) {
	if errors.Is(err, db.ErrNotFound) {
		a.Error(w, http.StatusNotFound, resource+" not found")
		return
	}
	a.Error(w, http.StatusInternalServerError, "failed to load "+resource)
}

// queryInt reads a positive integer query parameter, falling back to def.
func queryInt(r *http.Request, key string, def int) int {
	if v := r.URL.Query().Get(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

// splitCSV splits a comma-separated value into trimmed, non-empty parts.
func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// userFileSortColumn maps a client sortBy value to a safe, qualified column.
func userFileSortColumn(sortBy string) string {
	switch sortBy {
	case "id":
		return "f.id"
	case "createdAt":
		return "f.created_at"
	case "updatedAt":
		return "f.updated_at"
	case "deletesAt":
		return "f.deletes_at"
	case "name":
		return "f.name"
	case "originalName":
		return "f.original_name"
	case "size":
		return "f.size"
	case "type":
		return "f.type"
	case "views":
		return "f.views"
	case "favorite":
		return "f.favorite"
	default:
		return "f.created_at"
	}
}

// userSortOrder maps a client order value to ASC/DESC (default DESC).
func userSortOrder(order string) string {
	if strings.EqualFold(order, "asc") {
		return "ASC"
	}
	return "DESC"
}

// --- dynamic SET builder for partial updates ---

// nowExpr is a sentinel meaning the column should be set to SQL now() rather than
// a bound parameter.
type nowExpr struct{}

// userSetBuilder accumulates column assignments for a partial UPDATE, emitting
// numbered placeholders ($1, $2, ...) and collecting their bound values. Columns
// assigned a nowExpr value are rendered as now() inline (no placeholder).
type userSetBuilder struct {
	cols []string
	vals []any
}

func newUserSetBuilder() *userSetBuilder {
	return &userSetBuilder{}
}

func (b *userSetBuilder) add(col string, val any) {
	b.cols = append(b.cols, col)
	b.vals = append(b.vals, val)
}

func (b *userSetBuilder) empty() bool {
	// Only "real" value columns count; a lone updated_at=now() is not a meaningful update.
	for _, v := range b.vals {
		if _, isNow := v.(nowExpr); !isNow {
			return false
		}
	}
	return true
}

// build renders the full statement. prefix is e.g. "UPDATE files SET ", and
// suffixFmt is a template whose %d placeholders are filled, in order, with the
// parameter indexes for trailing args (e.g. " WHERE id=$%d AND user_id=$%d").
// Those trailing args are appended after the SET values.
func (b *userSetBuilder) build(prefix, suffixFmt string, trailing ...any) (string, []any) {
	q := prefix
	args := make([]any, 0, len(b.vals)+len(trailing))
	idx := 1
	for i, col := range b.cols {
		if i > 0 {
			q += ", "
		}
		if _, isNow := b.vals[i].(nowExpr); isNow {
			q += col + "=now()"
			continue
		}
		q += col + "=$" + strconv.Itoa(idx)
		args = append(args, b.vals[i])
		idx++
	}

	// Fill the suffix's %d placeholders with successive parameter indexes.
	suffix := suffixFmt
	for range trailing {
		suffix = replaceFirst(suffix, "%d", strconv.Itoa(idx))
		idx++
	}
	args = append(args, trailing...)
	return q + suffix, args
}

// replaceFirst replaces the first occurrence of old in s with replacement.
func replaceFirst(s, old, replacement string) string {
	for i := 0; i+len(old) <= len(s); i++ {
		if s[i:i+len(old)] == old {
			return s[:i] + replacement + s[i+len(old):]
		}
	}
	return s
}
