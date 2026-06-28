package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/lucsky/cuid"

	"zipfast/internal/auth"
	"zipfast/internal/db"
	"zipfast/internal/models"
)

// registerAdminRoutes wires the admin-only user-management and invite endpoints.
// Every route here requires an authenticated ADMIN (or SUPERADMIN).
func (a *App) registerAdminRoutes(r chi.Router) {
	r.Route("/api/users", func(ar chi.Router) {
		ar.Use(a.RequireAdmin)
		ar.Get("/", a.handleAdminListUsers)
		ar.Post("/", a.handleAdminCreateUser)
		ar.Get("/{id}", a.handleAdminGetUser)
		ar.Patch("/{id}", a.handleAdminPatchUser)
		ar.Delete("/{id}", a.handleAdminDeleteUser)
	})

	r.Route("/api/auth/invites", func(ir chi.Router) {
		ir.Use(a.RequireAdmin)
		ir.Get("/", a.handleAdminListInvites)
		ir.Post("/", a.handleAdminCreateInvite)
		ir.Delete("/{id}", a.handleAdminDeleteInvite)
	})
}

// adminUser is the redacted, API-safe view of a user matching the client's
// LimitedUser shape (userSchema omitting oauthProviders, totpSecret, passkeys,
// sessions, password, token). Password/token/totp are never included.
type adminUser struct {
	ID        string               `json:"id"`
	CreatedAt time.Time            `json:"createdAt"`
	UpdatedAt time.Time            `json:"updatedAt"`
	Username  string               `json:"username"`
	Avatar    *string              `json:"avatar,omitempty"`
	Role      models.Role          `json:"role"`
	View      models.UserViewEmbed `json:"view"`
	Quota     *models.UserQuota    `json:"quota,omitempty"`
}

// --- users ---

func (a *App) handleAdminListUsers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// The client requests `?noincl=true` to exclude the current admin from the
	// results (mirrors the original querySchema).
	noincl := r.URL.Query().Get("noincl") == "true"
	var excludeID string
	if actor := UserFromContext(ctx); actor != nil {
		excludeID = actor.ID
	}

	query := `
		SELECT u.id, u.created_at, u.updated_at, u.username, u.avatar, u.role, u.view
		FROM users u`
	args := []any{}
	if noincl && excludeID != "" {
		query += ` WHERE u.id <> $1`
		args = append(args, excludeID)
	}
	query += ` ORDER BY u.created_at ASC`

	rows, err := a.Store.Pool.Query(ctx, query, args...)
	if err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to list users")
		return
	}
	defer rows.Close()

	users := make([]adminUser, 0)
	for rows.Next() {
		var (
			au   adminUser
			view []byte
		)
		if err := rows.Scan(&au.ID, &au.CreatedAt, &au.UpdatedAt, &au.Username, &au.Avatar,
			&au.Role, &view); err != nil {
			a.Error(w, http.StatusInternalServerError, "failed to read users")
			return
		}
		if len(view) > 0 {
			_ = json.Unmarshal(view, &au.View)
		}
		au.Quota = a.adminLoadQuota(ctx, au.ID)
		users = append(users, au)
	}
	if rows.Err() != nil {
		a.Error(w, http.StatusInternalServerError, "failed to read users")
		return
	}

	a.WriteJSON(w, http.StatusOK, users)
}

// canInteract mirrors the original lib/role.ts canInteract(current, target):
// a SUPERADMIN may act on USER or ADMIN; an ADMIN may act on USER only. Note a
// SUPERADMIN may NOT create/act on another SUPERADMIN, matching the original.
func canInteract(current, target models.Role) bool {
	switch current {
	case models.RoleSuperAdmin:
		return target == models.RoleUser || target == models.RoleAdmin
	case models.RoleAdmin:
		return target == models.RoleUser
	default:
		return false
	}
}

// adminCreateUserBody is the accepted shape for POST /api/users. Mirrors the
// original body schema: username & password are trimmed/required, role defaults
// to USER, avatar is an optional base64 data string.
type adminCreateUserBody struct {
	Username string       `json:"username"`
	Password string       `json:"password"`
	Avatar   *string      `json:"avatar"`
	Role     *models.Role `json:"role"`
}

// handleAdminCreateUser creates a new user (admin only), returning the redacted
// LimitedUser shape — the same serializer GET /api/users uses. Never leaks the
// password or token. Matches the original POST /api/users behaviour.
func (a *App) handleAdminCreateUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body adminCreateUserBody
	if err := a.ReadJSON(r, &body); err != nil {
		a.Error(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	// zStringTrimmed: trim then require min length 1.
	username := strings.TrimSpace(body.Username)
	password := strings.TrimSpace(body.Password)
	if username == "" {
		a.Error(w, http.StatusBadRequest, "username is required")
		return
	}
	if password == "" {
		a.Error(w, http.StatusBadRequest, "password is required")
		return
	}

	// Username uniqueness -> 1040 ("A user with this username already exists").
	if _, err := a.Store.GetUserByUsername(ctx, username); err == nil {
		a.Error(w, http.StatusConflict, "A user with this username already exists")
		return
	} else if !errors.Is(err, db.ErrNotFound) {
		a.Error(w, http.StatusInternalServerError, "failed to check username")
		return
	}

	// Role defaults to USER. If a role is supplied, the requester must be allowed
	// to create it per canInteract -> 3008 ("You cannot create this role").
	role := models.RoleUser
	if body.Role != nil && *body.Role != "" {
		switch *body.Role {
		case models.RoleUser, models.RoleAdmin, models.RoleSuperAdmin:
		default:
			a.Error(w, http.StatusBadRequest, "invalid role")
			return
		}
		role = *body.Role
		actor := UserFromContext(ctx)
		if actor == nil || !canInteract(actor.Role, role) {
			a.Error(w, http.StatusForbidden, "You cannot create this role")
			return
		}
	}

	hash, herr := auth.HashPassword(password)
	if herr != nil {
		a.Error(w, http.StatusInternalServerError, "failed to hash password")
		return
	}

	id := cuid.New()
	token := auth.CreateToken()
	var avatar *string
	if body.Avatar != nil && *body.Avatar != "" {
		avatar = body.Avatar
	}

	if _, err := a.Store.Pool.Exec(ctx,
		`INSERT INTO users (id, created_at, updated_at, username, password, token, role, avatar, view)
		 VALUES ($1, now(), now(), $2, $3, $4, $5, $6, '{}'::jsonb)`,
		id, username, hash, token, role, avatar); err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to create user")
		return
	}

	au, err := a.adminGetUser(r, id)
	if err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to load created user")
		return
	}
	a.logFor(r).Info("admin created user", "newUserId", id, "role", role)
	a.WriteJSON(w, http.StatusOK, au)
}

func (a *App) handleAdminGetUser(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	au, err := a.adminGetUser(r, id)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			a.Error(w, http.StatusNotFound, "user not found")
			return
		}
		a.Error(w, http.StatusInternalServerError, "failed to load user")
		return
	}
	a.WriteJSON(w, http.StatusOK, au)
}

// adminPatchUserBody is the accepted shape for user updates. The quota object
// mirrors the client: filesType selects the mode (BY_BYTES/BY_FILES/NONE).
type adminPatchUserBody struct {
	Username *string      `json:"username"`
	Password *string      `json:"password"`
	Role     *models.Role `json:"role"`

	Quota *struct {
		FilesType *string `json:"filesType"`
		MaxBytes  *string `json:"maxBytes"`
		MaxFiles  *int    `json:"maxFiles"`
		MaxUrls   *int    `json:"maxUrls"`
	} `json:"quota"`
}

func (a *App) handleAdminPatchUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")

	target, err := a.Store.GetUserByID(ctx, id)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			a.Error(w, http.StatusNotFound, "user not found")
			return
		}
		a.Error(w, http.StatusInternalServerError, "failed to load user")
		return
	}

	var body adminPatchUserBody
	if err := a.ReadJSON(r, &body); err != nil {
		a.Error(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	actor := UserFromContext(ctx)

	// Role changes are restricted: only a SUPERADMIN may grant/alter roles, and
	// no one may modify a user ranked at or above themselves (loose enforcement).
	if body.Role != nil && *body.Role != target.Role {
		if actor == nil || models.RoleRank(actor.Role) < models.RoleRank(models.RoleSuperAdmin) {
			a.Error(w, http.StatusForbidden, "only a superadmin may change roles")
			return
		}
		if models.RoleRank(target.Role) >= models.RoleRank(actor.Role) {
			a.Error(w, http.StatusForbidden, "cannot modify a user with an equal or higher role")
			return
		}
		switch *body.Role {
		case models.RoleUser, models.RoleAdmin, models.RoleSuperAdmin:
		default:
			a.Error(w, http.StatusBadRequest, "invalid role")
			return
		}
	}

	// Apply username / password / role with a single dynamic UPDATE.
	if body.Username != nil || body.Password != nil || body.Role != nil {
		newUsername := target.Username
		if body.Username != nil {
			newUsername = *body.Username
		}

		newRole := target.Role
		if body.Role != nil {
			newRole = *body.Role
		}

		var passwordArg any = target.Password // keep existing by default
		if body.Password != nil {
			if *body.Password == "" {
				passwordArg = nil
			} else {
				hash, herr := auth.HashPassword(*body.Password)
				if herr != nil {
					a.Error(w, http.StatusInternalServerError, "failed to hash password")
					return
				}
				passwordArg = hash
			}
		}

		if _, err := a.Store.Pool.Exec(ctx,
			`UPDATE users SET username=$1, role=$2, password=$3, updated_at=now() WHERE id=$4`,
			newUsername, newRole, passwordArg, id); err != nil {
			a.Error(w, http.StatusInternalServerError, "failed to update user")
			return
		}
	}

	// Upsert quota when supplied. Replicates the original filesType handling:
	// BY_BYTES -> store maxBytes (null when <= 0), clear maxFiles; BY_FILES ->
	// store maxFiles, clear maxBytes; NONE -> BY_BYTES with both cleared. maxUrls
	// is stored only when > 0, otherwise null.
	if body.Quota != nil {
		filesQuota := models.QuotaByBytes
		var maxBytes *string
		var maxFiles *int

		filesType := ""
		if body.Quota.FilesType != nil {
			filesType = *body.Quota.FilesType
		}
		switch filesType {
		case "BY_FILES":
			filesQuota = models.QuotaByFiles
			maxFiles = body.Quota.MaxFiles
			maxBytes = nil
		case "NONE":
			filesQuota = models.QuotaByBytes
			maxFiles = nil
			maxBytes = nil
		default: // BY_BYTES (and any unspecified): keep the initial QuotaByBytes
			maxBytes = body.Quota.MaxBytes
			maxFiles = nil
		}

		var maxUrls *int
		if body.Quota.MaxUrls != nil && *body.Quota.MaxUrls > 0 {
			maxUrls = body.Quota.MaxUrls
		}

		if _, err := a.Store.Pool.Exec(ctx, `
			INSERT INTO user_quotas (id, created_at, updated_at, files_quota, max_bytes, max_files, max_urls, user_id)
			VALUES ($1, now(), now(), $2, $3, $4, $5, $6)
			ON CONFLICT (user_id) DO UPDATE
			SET files_quota = EXCLUDED.files_quota,
			    max_bytes   = EXCLUDED.max_bytes,
			    max_files   = EXCLUDED.max_files,
			    max_urls    = EXCLUDED.max_urls,
			    updated_at  = now()`,
			cuid.New(), filesQuota, maxBytes, maxFiles, maxUrls, id); err != nil {
			a.Error(w, http.StatusInternalServerError, "failed to update quota")
			return
		}
	}

	au, err := a.adminGetUser(r, id)
	if err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to reload user")
		return
	}
	a.logFor(r).Info("admin updated user", "targetUserId", id)
	a.WriteJSON(w, http.StatusOK, au)
}

// adminDeleteUserBody mirrors the original DELETE body schema: an optional
// `delete` flag that, when true, cascades deletion of the user's files & urls.
type adminDeleteUserBody struct {
	Delete bool `json:"delete"`
}

func (a *App) handleAdminDeleteUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")

	// Body is optional; ignore decode errors and treat as empty (delete=false).
	var body adminDeleteUserBody
	_ = a.ReadJSON(r, &body)

	// Load the target user (with role) so we can enforce interaction rules.
	target, err := a.Store.GetUserByID(ctx, id)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			a.Error(w, http.StatusNotFound, "user not found")
			return
		}
		a.Error(w, http.StatusInternalServerError, "failed to load user")
		return
	}

	actor := UserFromContext(ctx)
	// 3010: a user cannot delete themselves.
	if actor != nil && actor.ID == target.ID {
		a.Error(w, http.StatusBadRequest, "cannot delete yourself")
		return
	}
	// 3009: the requester must be permitted to act on the target's role.
	if actor == nil || !canInteract(actor.Role, target.Role) {
		a.Error(w, http.StatusForbidden, "you cannot delete this user")
		return
	}

	// Capture a redacted snapshot before deletion for the response.
	au, err := a.adminGetUser(r, id)
	if err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to load user")
		return
	}

	// When `delete` is set, cascade-delete the user's files (from the datasource
	// and DB) and urls, mirroring the original. Otherwise files/urls are detached
	// (user_id ON DELETE SET NULL) when the user row is removed.
	if body.Delete {
		names, nerr := a.adminListFileNames(ctx, id)
		if nerr != nil {
			a.Error(w, http.StatusInternalServerError, "failed to load user's files")
			return
		}
		if _, derr := a.Store.Pool.Exec(ctx, `DELETE FROM files WHERE user_id=$1`, id); derr != nil {
			a.Error(w, http.StatusInternalServerError, "failed to delete user's files")
			return
		}
		if _, derr := a.Store.Pool.Exec(ctx, `DELETE FROM urls WHERE user_id=$1`, id); derr != nil {
			a.Error(w, http.StatusInternalServerError, "failed to delete user's urls")
			return
		}
		if a.DS != nil {
			for _, name := range names {
				if err := a.DS.Delete(name); err != nil {
					a.Log.Warn("failed to delete file from datasource", "name", name, "err", err)
				}
			}
		}
	}

	// oauth_providers, sessions, quotas and passkeys are removed via ON DELETE
	// CASCADE when the user row is deleted.
	tag, err := a.Store.Pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, id)
	if err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to delete user")
		return
	}
	if tag.RowsAffected() == 0 {
		a.Error(w, http.StatusNotFound, "user not found")
		return
	}

	a.logFor(r).Info("admin deleted user", "deletedUserId", id)
	a.WriteJSON(w, http.StatusOK, au)
}

// adminListFileNames returns the stored names of every file owned by a user, so
// they can be removed from the datasource before the rows are deleted.
func (a *App) adminListFileNames(ctx context.Context, userID string) ([]string, error) {
	rows, err := a.Store.Pool.Query(ctx, `SELECT name FROM files WHERE user_id=$1`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	names := make([]string, 0)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

// adminGetUser loads a single redacted user with its quota relation.
func (a *App) adminGetUser(r *http.Request, id string) (*adminUser, error) {
	ctx := r.Context()

	var (
		au   adminUser
		view []byte
	)
	err := a.Store.Pool.QueryRow(ctx, `
		SELECT u.id, u.created_at, u.updated_at, u.username, u.avatar, u.role, u.view
		FROM users u WHERE u.id = $1`, id).
		Scan(&au.ID, &au.CreatedAt, &au.UpdatedAt, &au.Username, &au.Avatar, &au.Role, &view)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, db.ErrNotFound
		}
		return nil, err
	}
	if len(view) > 0 {
		_ = json.Unmarshal(view, &au.View)
	}
	au.Quota = a.adminLoadQuota(ctx, id)
	return &au, nil
}

// adminLoadQuota returns the user's quota or nil when none is set.
func (a *App) adminLoadQuota(ctx context.Context, userID string) *models.UserQuota {
	var q models.UserQuota
	err := a.Store.Pool.QueryRow(ctx, `
		SELECT id, created_at, updated_at, files_quota, max_bytes, max_files, max_urls, user_id
		FROM user_quotas WHERE user_id = $1`, userID).
		Scan(&q.ID, &q.CreatedAt, &q.UpdatedAt, &q.FilesQuota, &q.MaxBytes, &q.MaxFiles, &q.MaxUrls, &q.UserID)
	if err != nil {
		return nil
	}
	return &q
}

// --- invites ---

// adminInviter is the trimmed inviter relation embedded in invite responses
// (matches inviteInviterSelect: username, id, role).
type adminInviter struct {
	ID       string      `json:"id"`
	Username string      `json:"username"`
	Role     models.Role `json:"role"`
}

// adminInvite is the API-safe invite shape including its inviter relation,
// matching the client's inviteSchema.
type adminInvite struct {
	ID        string        `json:"id"`
	CreatedAt time.Time     `json:"createdAt"`
	UpdatedAt time.Time     `json:"updatedAt"`
	ExpiresAt *time.Time    `json:"expiresAt"`
	Code      string        `json:"code"`
	Uses      int           `json:"uses"`
	MaxUses   *int          `json:"maxUses"`
	InviterID string        `json:"inviterId"`
	Inviter   *adminInviter `json:"inviter,omitempty"`
}

func (a *App) handleAdminListInvites(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rows, err := a.Store.Pool.Query(ctx, `
		SELECT i.id, i.created_at, i.updated_at, i.expires_at, i.code, i.uses, i.max_uses, i.inviter_id,
		       u.id, u.username, u.role
		FROM invites i
		JOIN users u ON u.id = i.inviter_id
		ORDER BY i.created_at DESC`)
	if err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to list invites")
		return
	}
	defer rows.Close()

	invites := make([]adminInvite, 0)
	for rows.Next() {
		var (
			inv adminInvite
			inr adminInviter
		)
		if err := rows.Scan(&inv.ID, &inv.CreatedAt, &inv.UpdatedAt, &inv.ExpiresAt,
			&inv.Code, &inv.Uses, &inv.MaxUses, &inv.InviterID,
			&inr.ID, &inr.Username, &inr.Role); err != nil {
			a.Error(w, http.StatusInternalServerError, "failed to read invites")
			return
		}
		inv.Inviter = &inr
		invites = append(invites, inv)
	}
	if rows.Err() != nil {
		a.Error(w, http.StatusInternalServerError, "failed to read invites")
		return
	}
	a.WriteJSON(w, http.StatusOK, invites)
}

type adminCreateInviteBody struct {
	ExpiresAt *time.Time `json:"expiresAt"`
	MaxUses   *int       `json:"maxUses"`
}

func (a *App) handleAdminCreateInvite(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body adminCreateInviteBody
	// Body is optional; ignore decode errors and treat as empty.
	_ = a.ReadJSON(r, &body)

	actor := UserFromContext(ctx)
	if actor == nil {
		a.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	length := a.Cfg.Invites.Length
	if length <= 0 {
		length = 6
	}

	inv := adminInvite{
		ID:        cuid.New(),
		Code:      auth.RandomString(length),
		Uses:      0,
		ExpiresAt: body.ExpiresAt,
		MaxUses:   body.MaxUses,
		InviterID: actor.ID,
	}

	err := a.Store.Pool.QueryRow(ctx, `
		INSERT INTO invites (id, created_at, updated_at, expires_at, code, uses, max_uses, inviter_id)
		VALUES ($1, now(), now(), $2, $3, 0, $4, $5)
		RETURNING created_at, updated_at`,
		inv.ID, inv.ExpiresAt, inv.Code, inv.MaxUses, inv.InviterID).
		Scan(&inv.CreatedAt, &inv.UpdatedAt)
	if err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to create invite")
		return
	}

	// The original includes the inviter relation on the created invite.
	inv.Inviter = &adminInviter{
		ID:       actor.ID,
		Username: actor.Username,
		Role:     actor.Role,
	}

	a.logFor(r).Info("invite created", "inviteId", inv.ID, "inviterId", actor.ID)
	a.WriteJSON(w, http.StatusOK, inv)
}

func (a *App) handleAdminDeleteInvite(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")

	// Load the invite (with inviter) before deleting so we can return the full
	// deleted invite, matching the client's ApiAuthInvitesIdResponse (Invite).
	var (
		inv adminInvite
		inr adminInviter
	)
	err := a.Store.Pool.QueryRow(ctx, `
		SELECT i.id, i.created_at, i.updated_at, i.expires_at, i.code, i.uses, i.max_uses, i.inviter_id,
		       u.id, u.username, u.role
		FROM invites i
		JOIN users u ON u.id = i.inviter_id
		WHERE i.id = $1`, id).
		Scan(&inv.ID, &inv.CreatedAt, &inv.UpdatedAt, &inv.ExpiresAt,
			&inv.Code, &inv.Uses, &inv.MaxUses, &inv.InviterID,
			&inr.ID, &inr.Username, &inr.Role)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			a.Error(w, http.StatusNotFound, "invite not found")
			return
		}
		a.Error(w, http.StatusInternalServerError, "failed to load invite")
		return
	}
	inv.Inviter = &inr

	tag, err := a.Store.Pool.Exec(ctx, `DELETE FROM invites WHERE id=$1`, id)
	if err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to delete invite")
		return
	}
	if tag.RowsAffected() == 0 {
		a.Error(w, http.StatusNotFound, "invite not found")
		return
	}

	a.logFor(r).Info("invite deleted", "inviteId", id)
	a.WriteJSON(w, http.StatusOK, inv)
}
