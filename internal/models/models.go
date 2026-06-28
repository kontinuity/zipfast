// Package models defines the data structures that mirror the Zipline PostgreSQL
// schema (originally a Prisma schema). Field names and JSON tags are chosen to
// match the existing API contract so the SPA and ShareX clients keep working.
package models

import "time"

// Role is a user role.
type Role string

const (
	RoleUser       Role = "USER"
	RoleAdmin      Role = "ADMIN"
	RoleSuperAdmin Role = "SUPERADMIN"
)

// RoleRank is used for hierarchical comparisons (higher = more privileged).
func RoleRank(r Role) int {
	switch r {
	case RoleSuperAdmin:
		return 3
	case RoleAdmin:
		return 2
	case RoleUser:
		return 1
	default:
		return 0
	}
}

// UserFilesQuota selects how a user's quota is measured.
type UserFilesQuota string

const (
	QuotaByBytes UserFilesQuota = "BY_BYTES"
	QuotaByFiles UserFilesQuota = "BY_FILES"
)

// OAuthProviderType enumerates supported OAuth providers.
type OAuthProviderType string

const (
	OAuthDiscord OAuthProviderType = "DISCORD"
	OAuthGoogle  OAuthProviderType = "GOOGLE"
	OAuthGithub  OAuthProviderType = "GITHUB"
	OAuthOIDC    OAuthProviderType = "OIDC"
)

// IncompleteFileStatus tracks chunked-upload assembly progress.
type IncompleteFileStatus string

const (
	IncompletePending    IncompleteFileStatus = "PENDING"
	IncompleteProcessing IncompleteFileStatus = "PROCESSING"
	IncompleteComplete   IncompleteFileStatus = "COMPLETE"
	IncompleteFailed     IncompleteFileStatus = "FAILED"
)

// UserViewEmbed holds the per-user "view"/embed settings stored as JSON on User.view.
type UserViewEmbed struct {
	Enabled          bool   `json:"enabled,omitempty"`
	Embed            bool   `json:"embed,omitempty"`
	EmbedMediaOnly   bool   `json:"embedMediaOnly,omitempty"`
	Align            string `json:"align,omitempty"`
	ShowMimetype     bool   `json:"showMimetype,omitempty"`
	ShowTags         bool   `json:"showTags,omitempty"`
	ShowFolder       bool   `json:"showFolder,omitempty"`
	Content          string `json:"content,omitempty"`
	EmbedTitle       string `json:"embedTitle,omitempty"`
	EmbedDescription string `json:"embedDescription,omitempty"`
	EmbedSiteName    string `json:"embedSiteName,omitempty"`
	EmbedColor       string `json:"embedColor,omitempty"`
	DisableTextFiles bool   `json:"disableTextFiles,omitempty"`
}

// User mirrors the User model.
type User struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`

	Username string        `json:"username"`
	Password *string       `json:"-"`
	Avatar   *string       `json:"avatar,omitempty"`
	Token    string        `json:"-"`
	Role     Role          `json:"role"`
	View     UserViewEmbed `json:"view"`

	TotpSecret *string `json:"-"`

	// Relations (populated on demand).
	Quota          *UserQuota      `json:"quota,omitempty"`
	OAuthProviders []OAuthProvider `json:"oauthProviders,omitempty"`
	Passkeys       []UserPasskey   `json:"passkeys,omitempty"`
	Sessions       []UserSession   `json:"sessions,omitempty"`
}

// UserSession is a tracked login session (id stored in the encrypted cookie).
type UserSession struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"createdAt"`
	UA        string    `json:"ua"`
	Client    string    `json:"client"`
	Device    string    `json:"device"`
	UserID    string    `json:"-"`
}

// UserQuota mirrors the UserQuota model.
type UserQuota struct {
	ID         string         `json:"id"`
	CreatedAt  time.Time      `json:"createdAt"`
	UpdatedAt  time.Time      `json:"updatedAt"`
	FilesQuota UserFilesQuota `json:"filesQuota"`
	MaxBytes   *string        `json:"maxBytes,omitempty"`
	MaxFiles   *int           `json:"maxFiles,omitempty"`
	MaxUrls    *int           `json:"maxUrls,omitempty"`
	UserID     *string        `json:"-"`
}

// UserPasskey stores a WebAuthn credential as JSON (reg).
type UserPasskey struct {
	ID        string     `json:"id"`
	CreatedAt time.Time  `json:"createdAt"`
	UpdatedAt time.Time  `json:"updatedAt"`
	LastUsed  *time.Time `json:"lastUsed,omitempty"`
	Name      string     `json:"name"`
	Reg       []byte     `json:"reg"`
	UserID    string     `json:"-"`
}

// OAuthProvider links a user to an external identity.
type OAuthProvider struct {
	ID           string            `json:"id"`
	CreatedAt    time.Time         `json:"createdAt"`
	UpdatedAt    time.Time         `json:"updatedAt"`
	UserID       string            `json:"-"`
	Provider     OAuthProviderType `json:"provider"`
	Username     string            `json:"username"`
	AccessToken  string            `json:"-"`
	RefreshToken *string           `json:"-"`
	OAuthID      *string           `json:"oauthId,omitempty"`
}

// File mirrors the File model. Size is serialized as a JSON number (matching the
// original BigInt.toJSON override).
type File struct {
	ID        string     `json:"id"`
	CreatedAt time.Time  `json:"createdAt"`
	UpdatedAt time.Time  `json:"updatedAt"`
	DeletesAt *time.Time `json:"deletesAt,omitempty"`

	Name         string  `json:"name"`
	OriginalName *string `json:"originalName,omitempty"`
	Size         int64   `json:"size"`
	Type         string  `json:"type"`
	Views        int     `json:"views"`
	MaxViews     *int    `json:"maxViews,omitempty"`
	Favorite     bool    `json:"favorite"`
	Password     *string `json:"password,omitempty"` // redacted/omitted in most responses
	Anonymous    bool    `json:"anonymous"`

	UserID   *string `json:"-"`
	FolderID *string `json:"folderId,omitempty"`

	Tags      []Tag      `json:"tags,omitempty"`
	Thumbnail *Thumbnail `json:"thumbnail,omitempty"`
}

// Thumbnail mirrors the Thumbnail model.
type Thumbnail struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	Path      string    `json:"path"`
	FileID    string    `json:"-"`
}

// Folder mirrors the Folder model.
type Folder struct {
	ID           string    `json:"id"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
	Name         string    `json:"name"`
	Public       bool      `json:"public"`
	AllowUploads bool      `json:"allowUploads"`
	ParentID     *string   `json:"parentId,omitempty"`
	UserID       string    `json:"-"`
	Password     *string   `json:"-"` // redacted; exposed only as passwordProtected
	Files        []File    `json:"files,omitempty"`
}

// IncompleteFile tracks a chunked upload in progress.
type IncompleteFile struct {
	ID             string               `json:"id"`
	CreatedAt      time.Time            `json:"createdAt"`
	UpdatedAt      time.Time            `json:"updatedAt"`
	Status         IncompleteFileStatus `json:"status"`
	ChunksTotal    int                  `json:"chunksTotal"`
	ChunksComplete int                  `json:"chunksComplete"`
	Metadata       []byte               `json:"metadata"`
	UserID         string               `json:"-"`
}

// Tag mirrors the Tag model.
type Tag struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	Name      string    `json:"name"`
	Color     string    `json:"color"`
	UserID    *string   `json:"-"`
}

// Url mirrors the Url model.
type Url struct {
	ID          string    `json:"id"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
	Code        string    `json:"code"`
	Vanity      *string   `json:"vanity,omitempty"`
	Destination string    `json:"destination"`
	Views       int       `json:"views"`
	MaxViews    *int      `json:"maxViews,omitempty"`
	Password    *string   `json:"password,omitempty"`
	Enabled     bool      `json:"enabled"`
	UserID      *string   `json:"-"`
}

// Metric stores an aggregated stats snapshot (data is JSON).
type Metric struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	Data      []byte    `json:"data"`
}

// Invite mirrors the Invite model.
type Invite struct {
	ID        string     `json:"id"`
	CreatedAt time.Time  `json:"createdAt"`
	UpdatedAt time.Time  `json:"updatedAt"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
	Code      string     `json:"code"`
	Uses      int        `json:"uses"`
	MaxUses   *int       `json:"maxUses,omitempty"`
	InviterID string     `json:"inviterId"`
}

// Export mirrors the Export model.
type Export struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	Completed bool      `json:"completed"`
	Path      string    `json:"path"`
	Files     int       `json:"files"`
	Size      string    `json:"size"`
	UserID    string    `json:"-"`
}
