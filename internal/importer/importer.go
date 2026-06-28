// Package importer ingests Zipline data exports (the v4 JSON export produced by
// Zipline 4.x and the older v3 "Prisma dump" format) into the Zipfast database.
//
// The importer is intentionally defensive: a single malformed row never aborts
// the whole import. Rows that fail to insert are recorded and skipped, and the
// returned Summary reports how many rows of each kind were inserted. All inserts
// use ON CONFLICT DO NOTHING so an import can be retried idempotently and so two
// overlapping exports can be merged without error.
//
// Heavy lifting lives here (accepting a *pgxpool.Pool) so the HTTP layer in
// package server stays thin.
package importer

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lucsky/cuid"
)

// Summary counts the rows inserted by an import, plus any non-fatal errors that
// were encountered (and skipped) along the way.
type Summary struct {
	Users          int      `json:"users"`
	Files          int      `json:"files"`
	Folders        int      `json:"folders"`
	Urls           int      `json:"urls"`
	Tags           int      `json:"tags"`
	FileTags       int      `json:"fileTags"`
	Invites        int      `json:"invites"`
	OAuthProviders int      `json:"oauthProviders"`
	Quotas         int      `json:"quotas"`
	Thumbnails     int      `json:"thumbnails"`
	Metrics        int      `json:"metrics"`
	Skipped        int      `json:"skipped"`
	Errors         []string `json:"errors,omitempty"`
}

// addErr records a non-fatal error and bumps the skip counter. The error list is
// capped so a pathological export cannot balloon the response.
func (s *Summary) addErr(format string, args ...any) {
	s.Skipped++
	if len(s.Errors) < 200 {
		s.Errors = append(s.Errors, fmt.Sprintf(format, args...))
	}
}

// --- v4 export shapes -------------------------------------------------------

// V4Export mirrors the top-level shape of a Zipline v4 export file.
//
// Fields whose shapes are not needed for insertion (settings, passkeys, metrics
// payloads, ...) are kept as json.RawMessage so the importer never fails to
// parse an export just because an unrelated section changed shape.
type V4Export struct {
	Versions struct {
		Zipline string `json:"zipline"`
		Node    string `json:"node"`
		Export  string `json:"export"`
	} `json:"versions"`

	Request json.RawMessage `json:"request"`

	Data V4Data `json:"data"`
}

// V4Data is the "data" object of a v4 export. Core entities are typed; the rest
// are retained as raw JSON.
type V4Data struct {
	Settings           json.RawMessage `json:"settings"`
	Users              []V4User        `json:"users"`
	UserPasskeys       json.RawMessage `json:"userPasskeys"`
	UserQuotas         []V4Quota       `json:"userQuotas"`
	UserOauthProviders []V4OAuth       `json:"userOauthProviders"`
	UserTags           []V4Tag         `json:"userTags"`
	Invites            []V4Invite      `json:"invites"`
	Folders            []V4Folder      `json:"folders"`
	Urls               []V4Url         `json:"urls"`
	Files              []V4File        `json:"files"`
	Thumbnails         []V4Thumbnail   `json:"thumbnails"`
	Metrics            []V4Metric      `json:"metrics"`
}

// V4User is a user row from a v4 export.
type V4User struct {
	ID         string          `json:"id"`
	CreatedAt  *time.Time      `json:"createdAt"`
	UpdatedAt  *time.Time      `json:"updatedAt"`
	Username   string          `json:"username"`
	Password   *string         `json:"password"`
	Avatar     *string         `json:"avatar"`
	Token      string          `json:"token"`
	Role       string          `json:"role"`
	View       json.RawMessage `json:"view"`
	TotpSecret *string         `json:"totpSecret"`
}

// V4Quota is a user_quotas row from a v4 export.
type V4Quota struct {
	ID         string     `json:"id"`
	CreatedAt  *time.Time `json:"createdAt"`
	UpdatedAt  *time.Time `json:"updatedAt"`
	FilesQuota string     `json:"filesQuota"`
	// MaxBytes is stored as TEXT in the schema but may arrive as a string or a
	// JSON number depending on the exporter version.
	MaxBytes flexString `json:"maxBytes"`
	MaxFiles *int       `json:"maxFiles"`
	MaxUrls  *int       `json:"maxUrls"`
	UserID   *string    `json:"userId"`
}

// V4OAuth is an oauth_providers row from a v4 export.
type V4OAuth struct {
	ID           string     `json:"id"`
	CreatedAt    *time.Time `json:"createdAt"`
	UpdatedAt    *time.Time `json:"updatedAt"`
	UserID       string     `json:"userId"`
	Provider     string     `json:"provider"`
	Username     string     `json:"username"`
	AccessToken  string     `json:"accessToken"`
	RefreshToken *string    `json:"refreshToken"`
	OAuthID      *string    `json:"oauthId"`
}

// V4Tag is a tags row from a v4 export.
type V4Tag struct {
	ID        string     `json:"id"`
	CreatedAt *time.Time `json:"createdAt"`
	UpdatedAt *time.Time `json:"updatedAt"`
	Name      string     `json:"name"`
	Color     string     `json:"color"`
	UserID    *string    `json:"userId"`
}

// V4Invite is an invites row from a v4 export.
type V4Invite struct {
	ID        string     `json:"id"`
	CreatedAt *time.Time `json:"createdAt"`
	UpdatedAt *time.Time `json:"updatedAt"`
	ExpiresAt *time.Time `json:"expiresAt"`
	Code      string     `json:"code"`
	Uses      int        `json:"uses"`
	MaxUses   *int       `json:"maxUses"`
	// Zipline names this inviterId; tolerate the older "inviter" alias too.
	InviterID string `json:"inviterId"`
	Inviter   string `json:"inviter"`
}

// V4Folder is a folders row from a v4 export.
type V4Folder struct {
	ID           string     `json:"id"`
	CreatedAt    *time.Time `json:"createdAt"`
	UpdatedAt    *time.Time `json:"updatedAt"`
	Name         string     `json:"name"`
	Public       bool       `json:"public"`
	AllowUploads bool       `json:"allowUploads"`
	ParentID     *string    `json:"parentId"`
	UserID       string     `json:"userId"`
}

// V4Url is a urls row from a v4 export.
type V4Url struct {
	ID          string     `json:"id"`
	CreatedAt   *time.Time `json:"createdAt"`
	UpdatedAt   *time.Time `json:"updatedAt"`
	Code        string     `json:"code"`
	Vanity      *string    `json:"vanity"`
	Destination string     `json:"destination"`
	Views       int        `json:"views"`
	MaxViews    *int       `json:"maxViews"`
	Password    *string    `json:"password"`
	Enabled     *bool      `json:"enabled"`
	UserID      *string    `json:"userId"`
}

// V4File is a files row from a v4 export. Tags may be embedded as an array of
// tag ids (or tag objects); both are tolerated.
type V4File struct {
	ID           string          `json:"id"`
	CreatedAt    *time.Time      `json:"createdAt"`
	UpdatedAt    *time.Time      `json:"updatedAt"`
	DeletesAt    *time.Time      `json:"deletesAt"`
	Name         string          `json:"name"`
	OriginalName *string         `json:"originalName"`
	Size         flexInt64       `json:"size"`
	Type         string          `json:"type"`
	Views        int             `json:"views"`
	MaxViews     *int            `json:"maxViews"`
	Favorite     bool            `json:"favorite"`
	Password     *string         `json:"password"`
	Anonymous    *bool           `json:"anonymous"`
	UserID       *string         `json:"userId"`
	FolderID     *string         `json:"folderId"`
	Tags         json.RawMessage `json:"tags"`
}

// V4Thumbnail is a thumbnails row from a v4 export.
type V4Thumbnail struct {
	ID        string     `json:"id"`
	CreatedAt *time.Time `json:"createdAt"`
	UpdatedAt *time.Time `json:"updatedAt"`
	Path      string     `json:"path"`
	FileID    string     `json:"fileId"`
}

// V4Metric is a metrics row from a v4 export. The payload is opaque.
type V4Metric struct {
	ID        string          `json:"id"`
	CreatedAt *time.Time      `json:"createdAt"`
	UpdatedAt *time.Time      `json:"updatedAt"`
	Data      json.RawMessage `json:"data"`
}

// ImportV4 parses a Zipline v4 export and inserts its rows into the database.
// Insertion order respects foreign keys: users → quotas/oauth/tags/folders →
// files → file_tags/thumbnails, with urls/invites/metrics interleaved once their
// owning rows exist. Every statement is ON CONFLICT DO NOTHING.
func ImportV4(ctx context.Context, pool *pgxpool.Pool, data []byte) (Summary, error) {
	var s Summary

	var exp V4Export
	if err := json.Unmarshal(data, &exp); err != nil {
		return s, fmt.Errorf("parse v4 export: %w", err)
	}

	// 1) Users first — almost everything references a user.
	for _, u := range exp.Data.Users {
		if u.Username == "" {
			s.addErr("user %q: missing username", u.ID)
			continue
		}
		id := orNewID(u.ID)
		token := u.Token
		if token == "" {
			// token has a NOT NULL UNIQUE constraint; synthesize one.
			token = "imported-" + cuid.New()
		}
		view := rawOrEmptyObject(u.View)
		if err := execInsert(ctx, pool,
			`INSERT INTO users (id, created_at, updated_at, username, password, avatar, token, role, view, totp_secret)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
			 ON CONFLICT (id) DO NOTHING`,
			id, tsOrNow(u.CreatedAt), tsOrNow(u.UpdatedAt), u.Username, u.Password, u.Avatar,
			token, normalizeRole(u.Role), view, u.TotpSecret); err != nil {
			s.addErr("user %q: %v", u.Username, err)
			continue
		}
		s.Users++
	}

	// 2) User quotas (one per user).
	for _, q := range exp.Data.UserQuotas {
		if q.UserID == nil || *q.UserID == "" {
			s.addErr("quota %q: missing userId", q.ID)
			continue
		}
		if err := execInsert(ctx, pool,
			`INSERT INTO user_quotas (id, created_at, updated_at, files_quota, max_bytes, max_files, max_urls, user_id)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			 ON CONFLICT (id) DO NOTHING`,
			orNewID(q.ID), tsOrNow(q.CreatedAt), tsOrNow(q.UpdatedAt), normalizeQuota(q.FilesQuota),
			q.MaxBytes.ptr(), q.MaxFiles, q.MaxUrls, q.UserID); err != nil {
			s.addErr("quota %q: %v", q.ID, err)
			continue
		}
		s.Quotas++
	}

	// 3) OAuth providers.
	for _, o := range exp.Data.UserOauthProviders {
		if o.UserID == "" {
			s.addErr("oauth %q: missing userId", o.ID)
			continue
		}
		if err := execInsert(ctx, pool,
			`INSERT INTO oauth_providers (id, created_at, updated_at, user_id, provider, username, access_token, refresh_token, oauth_id)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
			 ON CONFLICT (id) DO NOTHING`,
			orNewID(o.ID), tsOrNow(o.CreatedAt), tsOrNow(o.UpdatedAt), o.UserID,
			normalizeProvider(o.Provider), o.Username, o.AccessToken, o.RefreshToken, o.OAuthID); err != nil {
			s.addErr("oauth %q: %v", o.ID, err)
			continue
		}
		s.OAuthProviders++
	}

	// 4) Tags (user-scoped; referenced by file_tags).
	for _, t := range exp.Data.UserTags {
		if t.Name == "" {
			s.addErr("tag %q: missing name", t.ID)
			continue
		}
		color := t.Color
		if color == "" {
			color = "#ffffff"
		}
		if err := execInsert(ctx, pool,
			`INSERT INTO tags (id, created_at, updated_at, name, color, user_id)
			 VALUES ($1, $2, $3, $4, $5, $6)
			 ON CONFLICT (id) DO NOTHING`,
			orNewID(t.ID), tsOrNow(t.CreatedAt), tsOrNow(t.UpdatedAt), t.Name, color, t.UserID); err != nil {
			s.addErr("tag %q: %v", t.Name, err)
			continue
		}
		s.Tags++
	}

	// 5) Folders (self-referential parent_id; insert with parent unset first,
	//    then fix up parents so insertion order never trips the FK).
	for _, f := range exp.Data.Folders {
		if f.UserID == "" {
			s.addErr("folder %q: missing userId", f.ID)
			continue
		}
		if err := execInsert(ctx, pool,
			`INSERT INTO folders (id, created_at, updated_at, name, public, allow_uploads, parent_id, user_id)
			 VALUES ($1, $2, $3, $4, $5, $6, NULL, $7)
			 ON CONFLICT (id) DO NOTHING`,
			orNewID(f.ID), tsOrNow(f.CreatedAt), tsOrNow(f.UpdatedAt), f.Name, f.Public, f.AllowUploads, f.UserID); err != nil {
			s.addErr("folder %q: %v", f.Name, err)
			continue
		}
		s.Folders++
	}
	// Second pass: set parent_id now that every folder exists.
	for _, f := range exp.Data.Folders {
		if f.ParentID == nil || *f.ParentID == "" || f.ID == "" {
			continue
		}
		if err := execInsert(ctx, pool,
			`UPDATE folders SET parent_id=$1 WHERE id=$2 AND EXISTS (SELECT 1 FROM folders WHERE id=$1)`,
			*f.ParentID, f.ID); err != nil {
			s.addErr("folder %q parent: %v", f.ID, err)
		}
	}

	// 6) Files.
	for _, f := range exp.Data.Files {
		if f.Name == "" {
			s.addErr("file %q: missing name", f.ID)
			continue
		}
		id := orNewID(f.ID)
		anon := false
		if f.Anonymous != nil {
			anon = *f.Anonymous
		}
		if err := execInsert(ctx, pool,
			`INSERT INTO files (id, created_at, updated_at, deletes_at, name, original_name, size, type, views, max_views, favorite, password, anonymous, user_id, folder_id)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
			 ON CONFLICT (id) DO NOTHING`,
			id, tsOrNow(f.CreatedAt), tsOrNow(f.UpdatedAt), f.DeletesAt, f.Name, f.OriginalName,
			int64(f.Size), f.Type, f.Views, f.MaxViews, f.Favorite, f.Password, anon, f.UserID, f.FolderID); err != nil {
			s.addErr("file %q: %v", f.Name, err)
			continue
		}
		s.Files++

		// Embedded tag associations on the file, if any.
		for _, tagID := range parseTagIDs(f.Tags) {
			if tagID == "" {
				continue
			}
			if err := execInsert(ctx, pool,
				`INSERT INTO file_tags (file_id, tag_id) VALUES ($1, $2)
				 ON CONFLICT (file_id, tag_id) DO NOTHING`, id, tagID); err != nil {
				s.addErr("file_tag (%s,%s): %v", id, tagID, err)
				continue
			}
			s.FileTags++
		}
	}

	// 7) Thumbnails (each references a file).
	for _, t := range exp.Data.Thumbnails {
		if t.FileID == "" || t.Path == "" {
			s.addErr("thumbnail %q: missing fileId/path", t.ID)
			continue
		}
		if err := execInsert(ctx, pool,
			`INSERT INTO thumbnails (id, created_at, updated_at, path, file_id)
			 VALUES ($1, $2, $3, $4, $5)
			 ON CONFLICT (id) DO NOTHING`,
			orNewID(t.ID), tsOrNow(t.CreatedAt), tsOrNow(t.UpdatedAt), t.Path, t.FileID); err != nil {
			s.addErr("thumbnail %q: %v", t.ID, err)
			continue
		}
		s.Thumbnails++
	}

	// 8) URLs.
	for _, u := range exp.Data.Urls {
		if u.Code == "" || u.Destination == "" {
			s.addErr("url %q: missing code/destination", u.ID)
			continue
		}
		enabled := true
		if u.Enabled != nil {
			enabled = *u.Enabled
		}
		if err := execInsert(ctx, pool,
			`INSERT INTO urls (id, created_at, updated_at, code, vanity, destination, views, max_views, password, enabled, user_id)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
			 ON CONFLICT (id) DO NOTHING`,
			orNewID(u.ID), tsOrNow(u.CreatedAt), tsOrNow(u.UpdatedAt), u.Code, u.Vanity, u.Destination,
			u.Views, u.MaxViews, u.Password, enabled, u.UserID); err != nil {
			s.addErr("url %q: %v", u.Code, err)
			continue
		}
		s.Urls++
	}

	// 9) Invites.
	for _, inv := range exp.Data.Invites {
		if inv.Code == "" {
			s.addErr("invite %q: missing code", inv.ID)
			continue
		}
		inviter := inv.InviterID
		if inviter == "" {
			inviter = inv.Inviter
		}
		if inviter == "" {
			s.addErr("invite %q: missing inviterId", inv.ID)
			continue
		}
		if err := execInsert(ctx, pool,
			`INSERT INTO invites (id, created_at, updated_at, expires_at, code, uses, max_uses, inviter_id)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			 ON CONFLICT (id) DO NOTHING`,
			orNewID(inv.ID), tsOrNow(inv.CreatedAt), tsOrNow(inv.UpdatedAt), inv.ExpiresAt,
			inv.Code, inv.Uses, inv.MaxUses, inviter); err != nil {
			s.addErr("invite %q: %v", inv.Code, err)
			continue
		}
		s.Invites++
	}

	// 10) Metrics (opaque JSON payloads).
	for _, m := range exp.Data.Metrics {
		if err := execInsert(ctx, pool,
			`INSERT INTO metrics (id, created_at, updated_at, data)
			 VALUES ($1, $2, $3, $4)
			 ON CONFLICT (id) DO NOTHING`,
			orNewID(m.ID), tsOrNow(m.CreatedAt), tsOrNow(m.UpdatedAt), rawOrEmptyObject(m.Data)); err != nil {
			s.addErr("metric %q: %v", m.ID, err)
			continue
		}
		s.Metrics++
	}

	return s, nil
}

// --- v3 export shapes -------------------------------------------------------

// V3Export mirrors the legacy Zipline v3 export/dump. In v3 the export was a flat
// object whose top-level keys are collections. Users were commonly keyed by id (a
// map) in older dumps, but some tooling emitted arrays; both are tolerated via
// flexV3Users. Files/urls reference users by userId (or the legacy "user").
type V3Export struct {
	Users flexV3Users     `json:"users"`
	Files []V3File        `json:"files"`
	Urls  []V3Url         `json:"urls"`
	Tags  []V3Tag         `json:"tags"`
	Stats json.RawMessage `json:"stats"`
}

// V3User is a legacy user record.
type V3User struct {
	ID            string          `json:"id"`
	CreatedAt     *time.Time      `json:"createdAt"`
	UpdatedAt     *time.Time      `json:"updatedAt"`
	Username      string          `json:"username"`
	Password      *string         `json:"password"`
	Avatar        *string         `json:"avatar"`
	Token         string          `json:"token"`
	Role          string          `json:"role"`
	Administrator *bool           `json:"administrator"`
	SuperAdmin    *bool           `json:"superAdmin"`
	TotpSecret    *string         `json:"totpSecret"`
	Embed         json.RawMessage `json:"embed"`
}

// V3File is a legacy file record. v3 used integer ids, so flexString tolerates a
// numeric or string id. The slug field changed names over versions (name/fileName).
type V3File struct {
	ID           flexString `json:"id"`
	CreatedAt    *time.Time `json:"createdAt"`
	UpdatedAt    *time.Time `json:"updatedAt"`
	ExpiresAt    *time.Time `json:"expiresAt"`
	Name         string     `json:"name"`
	FileName     string     `json:"fileName"`
	OriginalName *string    `json:"originalName"`
	Size         flexInt64  `json:"size"`
	MimeType     string     `json:"mimetype"`
	Type         string     `json:"type"`
	Views        int        `json:"views"`
	MaxViews     *int       `json:"maxViews"`
	Favorite     bool       `json:"favorite"`
	Password     *string    `json:"password"`
	UserID       flexString `json:"userId"`
	User         flexString `json:"user"`
	FolderID     flexString `json:"folderId"`
}

// V3Url is a legacy short-url record.
type V3Url struct {
	ID          flexString `json:"id"`
	CreatedAt   *time.Time `json:"createdAt"`
	UpdatedAt   *time.Time `json:"updatedAt"`
	Code        string     `json:"code"`
	Vanity      *string    `json:"vanity"`
	Destination string     `json:"destination"`
	Origin      string     `json:"origin"`
	Views       int        `json:"views"`
	MaxViews    *int       `json:"maxViews"`
	UserID      flexString `json:"userId"`
	User        flexString `json:"user"`
}

// V3Tag is a legacy tag record (rare in v3 but tolerated).
type V3Tag struct {
	ID        flexString `json:"id"`
	CreatedAt *time.Time `json:"createdAt"`
	UpdatedAt *time.Time `json:"updatedAt"`
	Name      string     `json:"name"`
	Color     string     `json:"color"`
	UserID    flexString `json:"userId"`
}

// ImportV3 parses a legacy Zipline v3 export and best-effort maps it onto the
// current tables. Because v3 ids were small integers, incoming ids are preserved
// as text where present and otherwise generated.
func ImportV3(ctx context.Context, pool *pgxpool.Pool, data []byte) (Summary, error) {
	var s Summary

	var exp V3Export
	if err := json.Unmarshal(data, &exp); err != nil {
		return s, fmt.Errorf("parse v3 export: %w", err)
	}

	// Users.
	for _, u := range exp.Users.list() {
		if u.Username == "" {
			s.addErr("user %q: missing username", u.ID)
			continue
		}
		id := orNewID(u.ID)
		token := u.Token
		if token == "" {
			token = "imported-" + cuid.New()
		}
		role := normalizeRole(u.Role)
		// v3 used boolean flags rather than a role enum.
		if u.SuperAdmin != nil && *u.SuperAdmin {
			role = "SUPERADMIN"
		} else if u.Administrator != nil && *u.Administrator {
			role = "ADMIN"
		}
		view := rawOrEmptyObject(u.Embed)
		if err := execInsert(ctx, pool,
			`INSERT INTO users (id, created_at, updated_at, username, password, avatar, token, role, view, totp_secret)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
			 ON CONFLICT (id) DO NOTHING`,
			id, tsOrNow(u.CreatedAt), tsOrNow(u.UpdatedAt), u.Username, u.Password, u.Avatar,
			token, role, view, u.TotpSecret); err != nil {
			s.addErr("user %q: %v", u.Username, err)
			continue
		}
		s.Users++
	}

	// Tags.
	for _, t := range exp.Tags {
		if t.Name == "" {
			s.addErr("tag %q: missing name", t.ID.String())
			continue
		}
		color := t.Color
		if color == "" {
			color = "#ffffff"
		}
		if err := execInsert(ctx, pool,
			`INSERT INTO tags (id, created_at, updated_at, name, color, user_id)
			 VALUES ($1, $2, $3, $4, $5, $6)
			 ON CONFLICT (id) DO NOTHING`,
			orNewID(t.ID.String()), tsOrNow(t.CreatedAt), tsOrNow(t.UpdatedAt), t.Name, color, t.UserID.ptr()); err != nil {
			s.addErr("tag %q: %v", t.Name, err)
			continue
		}
		s.Tags++
	}

	// Files.
	for _, f := range exp.Files {
		name := f.Name
		if name == "" {
			name = f.FileName
		}
		if name == "" {
			s.addErr("file %q: missing name", f.ID.String())
			continue
		}
		typ := f.Type
		if typ == "" {
			typ = f.MimeType
		}
		userID := f.UserID.ptr()
		if userID == nil {
			userID = f.User.ptr()
		}
		if err := execInsert(ctx, pool,
			`INSERT INTO files (id, created_at, updated_at, deletes_at, name, original_name, size, type, views, max_views, favorite, password, anonymous, user_id, folder_id)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, false, $13, $14)
			 ON CONFLICT (id) DO NOTHING`,
			orNewID(f.ID.String()), tsOrNow(f.CreatedAt), tsOrNow(f.UpdatedAt), f.ExpiresAt, name, f.OriginalName,
			int64(f.Size), typ, f.Views, f.MaxViews, f.Favorite, f.Password, userID, f.FolderID.ptr()); err != nil {
			s.addErr("file %q: %v", name, err)
			continue
		}
		s.Files++
	}

	// URLs.
	for _, u := range exp.Urls {
		dest := u.Destination
		if dest == "" {
			dest = u.Origin
		}
		if u.Code == "" || dest == "" {
			s.addErr("url %q: missing code/destination", u.ID.String())
			continue
		}
		userID := u.UserID.ptr()
		if userID == nil {
			userID = u.User.ptr()
		}
		if err := execInsert(ctx, pool,
			`INSERT INTO urls (id, created_at, updated_at, code, vanity, destination, views, max_views, password, enabled, user_id)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NULL, true, $9)
			 ON CONFLICT (id) DO NOTHING`,
			orNewID(u.ID.String()), tsOrNow(u.CreatedAt), tsOrNow(u.UpdatedAt), u.Code, u.Vanity, dest,
			u.Views, u.MaxViews, userID); err != nil {
			s.addErr("url %q: %v", u.Code, err)
			continue
		}
		s.Urls++
	}

	return s, nil
}

// --- helpers ---------------------------------------------------------------

// execInsert runs a write and swallows pgx.ErrNoRows (which an INSERT ... ON
// CONFLICT DO NOTHING can surface in some drivers). Real errors propagate.
func execInsert(ctx context.Context, pool *pgxpool.Pool, sql string, args ...any) error {
	_, err := pool.Exec(ctx, sql, args...)
	if err == pgx.ErrNoRows {
		return nil
	}
	return err
}

// orNewID returns id when non-empty, otherwise a fresh cuid. Preserving incoming
// ids keeps cross-references (folder→file, file→tag, ...) intact on import.
func orNewID(id string) string {
	if strings.TrimSpace(id) == "" {
		return cuid.New()
	}
	return id
}

// tsOrNow returns *t when set, otherwise the current time. Timestamps are
// preserved from the export when present.
func tsOrNow(t *time.Time) time.Time {
	if t != nil && !t.IsZero() {
		return *t
	}
	return time.Now().UTC()
}

// rawOrEmptyObject returns valid JSON for a JSONB column, defaulting to "{}".
func rawOrEmptyObject(r json.RawMessage) []byte {
	trimmed := strings.TrimSpace(string(r))
	if len(trimmed) == 0 || trimmed == "null" {
		return []byte("{}")
	}
	return []byte(trimmed)
}

// normalizeRole maps an incoming role string onto the schema's CHECK set.
func normalizeRole(role string) string {
	switch strings.ToUpper(strings.TrimSpace(role)) {
	case "SUPERADMIN":
		return "SUPERADMIN"
	case "ADMIN":
		return "ADMIN"
	default:
		return "USER"
	}
}

// normalizeQuota maps an incoming files-quota mode onto the schema's CHECK set.
func normalizeQuota(q string) string {
	switch strings.ToUpper(strings.TrimSpace(q)) {
	case "BY_FILES":
		return "BY_FILES"
	default:
		return "BY_BYTES"
	}
}

// normalizeProvider maps an incoming provider onto the schema's CHECK set,
// defaulting to OIDC for anything unrecognized.
func normalizeProvider(p string) string {
	switch strings.ToUpper(strings.TrimSpace(p)) {
	case "DISCORD":
		return "DISCORD"
	case "GOOGLE":
		return "GOOGLE"
	case "GITHUB":
		return "GITHUB"
	default:
		return "OIDC"
	}
}

// parseTagIDs extracts tag ids from a file's embedded "tags" value, which may be
// a JSON array of strings (ids), an array of objects with an "id" field, or null.
func parseTagIDs(raw json.RawMessage) []string {
	trimmed := strings.TrimSpace(string(raw))
	if len(trimmed) == 0 || trimmed == "null" {
		return nil
	}
	// Try []string first.
	var ids []string
	if err := json.Unmarshal(raw, &ids); err == nil {
		return ids
	}
	// Then []{id}.
	var objs []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &objs); err == nil {
		out := make([]string, 0, len(objs))
		for _, o := range objs {
			if o.ID != "" {
				out = append(out, o.ID)
			}
		}
		return out
	}
	return nil
}

// flexString unmarshals a JSON string OR number into a Go string. Zipline v3 used
// numeric ids; some fields also arrive as either string or number across versions.
type flexString struct {
	val string
	set bool
}

func (f *flexString) UnmarshalJSON(b []byte) error {
	trimmed := strings.TrimSpace(string(b))
	if trimmed == "" || trimmed == "null" {
		return nil
	}
	if trimmed[0] == '"' {
		var sv string
		if err := json.Unmarshal(b, &sv); err != nil {
			return err
		}
		f.val, f.set = sv, sv != ""
		return nil
	}
	// number (or anything else): keep the literal text.
	f.val, f.set = trimmed, true
	return nil
}

func (f flexString) String() string { return f.val }

// ptr returns a *string (nil when unset/empty) for nullable columns.
func (f flexString) ptr() *string {
	if !f.set || f.val == "" {
		return nil
	}
	v := f.val
	return &v
}

// flexInt64 unmarshals a JSON number OR a numeric string into an int64. File
// sizes are emitted as BigInt (string) in v4 and as a number in v3.
type flexInt64 int64

func (f *flexInt64) UnmarshalJSON(b []byte) error {
	trimmed := strings.TrimSpace(string(b))
	if trimmed == "" || trimmed == "null" {
		return nil
	}
	trimmed = strings.Trim(trimmed, `"`)
	if trimmed == "" {
		return nil
	}
	n, err := strconv.ParseInt(trimmed, 10, 64)
	if err != nil {
		// Tolerate floats (e.g. 1024.0) by truncating.
		if fl, ferr := strconv.ParseFloat(trimmed, 64); ferr == nil {
			*f = flexInt64(int64(fl))
			return nil
		}
		return err
	}
	*f = flexInt64(n)
	return nil
}

// flexV3Users tolerates v3 "users" being either an array or an id-keyed object.
type flexV3Users []V3User

func (u *flexV3Users) UnmarshalJSON(b []byte) error {
	trimmed := strings.TrimSpace(string(b))
	if trimmed == "" || trimmed == "null" {
		return nil
	}
	if trimmed[0] == '[' {
		var arr []V3User
		if err := json.Unmarshal(b, &arr); err != nil {
			return err
		}
		*u = arr
		return nil
	}
	// id-keyed object: { "1": {..}, "2": {..} }.
	var m map[string]V3User
	if err := json.Unmarshal(b, &m); err != nil {
		return err
	}
	out := make([]V3User, 0, len(m))
	for key, v := range m {
		if v.ID == "" {
			v.ID = key
		}
		out = append(out, v)
	}
	*u = out
	return nil
}

func (u flexV3Users) list() []V3User { return []V3User(u) }
