// Package db provides the PostgreSQL data layer (pgx) and a Store with helper
// queries. Handlers may also use Store.Pool directly for queries not covered here.
package db

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"zipfast/internal/models"
)

//go:embed schema.sql
var schemaSQL string

// ErrNotFound is returned when a lookup yields no row.
var ErrNotFound = errors.New("not found")

// Store wraps a pgx connection pool.
type Store struct {
	Pool *pgxpool.Pool
}

// New connects to PostgreSQL and returns a Store.
func New(ctx context.Context, url string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	// Keep the idle pool small so idle RSS stays low.
	cfg.MaxConns = 10
	cfg.MinConns = 0
	cfg.MaxConnIdleTime = 5 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("ping: %w", err)
	}
	return &Store{Pool: pool}, nil
}

// Migrate applies the idempotent baseline schema.
func (s *Store) Migrate(ctx context.Context) error {
	_, err := s.Pool.Exec(ctx, schemaSQL)
	return err
}

func (s *Store) Close() { s.Pool.Close() }

// userColumns is the standard select list for users.
const userColumns = `id, created_at, updated_at, username, password, avatar, token, role, view, totp_secret`

func scanUser(row pgx.Row) (*models.User, error) {
	var u models.User
	var view []byte
	if err := row.Scan(&u.ID, &u.CreatedAt, &u.UpdatedAt, &u.Username, &u.Password,
		&u.Avatar, &u.Token, &u.Role, &view, &u.TotpSecret); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if len(view) > 0 {
		_ = json.Unmarshal(view, &u.View)
	}
	return &u, nil
}

func (s *Store) GetUserByID(ctx context.Context, id string) (*models.User, error) {
	return scanUser(s.Pool.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE id=$1`, id))
}

func (s *Store) GetUserByUsername(ctx context.Context, username string) (*models.User, error) {
	return scanUser(s.Pool.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE username=$1`, username))
}

func (s *Store) GetUserByToken(ctx context.Context, token string) (*models.User, error) {
	return scanUser(s.Pool.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE token=$1`, token))
}

func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

// CreateUser inserts a user. view is marshaled to JSON.
func (s *Store) CreateUser(ctx context.Context, u *models.User) error {
	view, _ := json.Marshal(u.View)
	if len(view) == 0 {
		view = []byte("{}")
	}
	_, err := s.Pool.Exec(ctx,
		`INSERT INTO users (id, created_at, updated_at, username, password, token, role, view, totp_secret)
		 VALUES ($1, now(), now(), $2, $3, $4, $5, $6, $7)`,
		u.ID, u.Username, u.Password, u.Token, u.Role, view, u.TotpSecret)
	return err
}

// GetFileByName looks up a file by its stored name (the URL slug). Tags/thumbnail
// are not populated here; callers needing them can query separately.
func (s *Store) GetFileByName(ctx context.Context, name string) (*models.File, error) {
	return scanFile(s.Pool.QueryRow(ctx, `SELECT `+fileColumns+` FROM files WHERE name=$1 ORDER BY created_at DESC LIMIT 1`, name))
}

func (s *Store) GetFileByID(ctx context.Context, id string) (*models.File, error) {
	return scanFile(s.Pool.QueryRow(ctx, `SELECT `+fileColumns+` FROM files WHERE id=$1`, id))
}

// GetThumbnailFileID returns the parent file id for a thumbnail object key
// (thumbnails.path), or ErrNotFound when no thumbnail row has that path. Used by
// the raw routes to resolve and serve thumbnail objects, which are not file rows.
func (s *Store) GetThumbnailFileID(ctx context.Context, path string) (string, error) {
	var fid string
	err := s.Pool.QueryRow(ctx, `SELECT file_id FROM thumbnails WHERE path=$1 LIMIT 1`, path).Scan(&fid)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return fid, err
}

const fileColumns = `id, created_at, updated_at, deletes_at, name, original_name, size, type, views, max_views, favorite, password, anonymous, user_id, folder_id`

func scanFile(row pgx.Row) (*models.File, error) {
	var f models.File
	if err := row.Scan(&f.ID, &f.CreatedAt, &f.UpdatedAt, &f.DeletesAt, &f.Name, &f.OriginalName,
		&f.Size, &f.Type, &f.Views, &f.MaxViews, &f.Favorite, &f.Password, &f.Anonymous,
		&f.UserID, &f.FolderID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &f, nil
}

// IncrementFileViews bumps the view counter and returns the new count.
func (s *Store) IncrementFileViews(ctx context.Context, id string) (int, error) {
	var views int
	err := s.Pool.QueryRow(ctx, `UPDATE files SET views = views + 1 WHERE id=$1 RETURNING views`, id).Scan(&views)
	return views, err
}

const urlColumns = `id, created_at, updated_at, code, vanity, destination, views, max_views, password, enabled, user_id`

func scanURL(row pgx.Row) (*models.Url, error) {
	var u models.Url
	if err := row.Scan(&u.ID, &u.CreatedAt, &u.UpdatedAt, &u.Code, &u.Vanity, &u.Destination,
		&u.Views, &u.MaxViews, &u.Password, &u.Enabled, &u.UserID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &u, nil
}

// GetURLByCode looks up a short URL by code or vanity.
func (s *Store) GetURLByCode(ctx context.Context, code string) (*models.Url, error) {
	return scanURL(s.Pool.QueryRow(ctx,
		`SELECT `+urlColumns+` FROM urls WHERE code=$1 OR vanity=$1 LIMIT 1`, code))
}

func (s *Store) IncrementURLViews(ctx context.Context, id string) (int, error) {
	var views int
	err := s.Pool.QueryRow(ctx, `UPDATE urls SET views = views + 1 WHERE id=$1 RETURNING views`, id).Scan(&views)
	return views, err
}

// --- settings (single JSONB row) ---

// LoadSettings returns the persisted settings JSON and first-setup flag.
func (s *Store) LoadSettings(ctx context.Context) (data []byte, firstSetup bool, err error) {
	err = s.Pool.QueryRow(ctx,
		`INSERT INTO zipline_settings (id) VALUES ('default')
		 ON CONFLICT (id) DO UPDATE SET id = zipline_settings.id
		 RETURNING data, first_setup`).Scan(&data, &firstSetup)
	return
}

// SaveSettings persists the settings JSON.
func (s *Store) SaveSettings(ctx context.Context, data []byte, firstSetup bool) error {
	_, err := s.Pool.Exec(ctx,
		`UPDATE zipline_settings SET data=$1, first_setup=$2, updated_at=now() WHERE id='default'`,
		data, firstSetup)
	return err
}
