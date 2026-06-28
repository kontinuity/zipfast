-- Zipfast baseline schema. Faithful to the Zipline data model, with two pragmatic
-- choices for a from-scratch build: enums are TEXT+CHECK, and the settings row is a
-- single JSONB blob (the API still returns the expected shape; env vars remain primary).
-- All statements are idempotent so this can run on every boot.

CREATE TABLE IF NOT EXISTS zipline_settings (
  id          TEXT PRIMARY KEY DEFAULT 'default',
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  first_setup BOOLEAN NOT NULL DEFAULT true,
  data        JSONB NOT NULL DEFAULT '{}'::jsonb
);

CREATE TABLE IF NOT EXISTS users (
  id          TEXT PRIMARY KEY,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  username    TEXT NOT NULL UNIQUE,
  password    TEXT,
  avatar      TEXT,
  token       TEXT NOT NULL UNIQUE,
  role        TEXT NOT NULL DEFAULT 'USER' CHECK (role IN ('USER','ADMIN','SUPERADMIN')),
  view        JSONB NOT NULL DEFAULT '{}'::jsonb,
  totp_secret TEXT
);

CREATE TABLE IF NOT EXISTS user_sessions (
  id         TEXT PRIMARY KEY,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  ua         TEXT NOT NULL DEFAULT '',
  client     TEXT NOT NULL DEFAULT '',
  device     TEXT NOT NULL DEFAULT '',
  user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS user_quotas (
  id          TEXT PRIMARY KEY,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  files_quota TEXT NOT NULL CHECK (files_quota IN ('BY_BYTES','BY_FILES')),
  max_bytes   TEXT,
  max_files   INTEGER,
  max_urls    INTEGER,
  user_id     TEXT UNIQUE REFERENCES users(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS user_passkeys (
  id         TEXT PRIMARY KEY,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_used  TIMESTAMPTZ,
  name       TEXT NOT NULL,
  reg        JSONB NOT NULL,
  user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS oauth_providers (
  id            TEXT PRIMARY KEY,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  user_id       TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  provider      TEXT NOT NULL CHECK (provider IN ('DISCORD','GOOGLE','GITHUB','OIDC')),
  username      TEXT NOT NULL,
  access_token  TEXT NOT NULL,
  refresh_token TEXT,
  oauth_id      TEXT,
  UNIQUE (provider, oauth_id)
);

CREATE TABLE IF NOT EXISTS folders (
  id            TEXT PRIMARY KEY,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  name          TEXT NOT NULL,
  public        BOOLEAN NOT NULL DEFAULT false,
  allow_uploads BOOLEAN NOT NULL DEFAULT false,
  parent_id     TEXT REFERENCES folders(id) ON DELETE SET NULL,
  user_id       TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS files (
  id            TEXT PRIMARY KEY,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  deletes_at    TIMESTAMPTZ,
  name          TEXT NOT NULL,
  original_name TEXT,
  size          BIGINT NOT NULL DEFAULT 0,
  type          TEXT NOT NULL DEFAULT '',
  views         INTEGER NOT NULL DEFAULT 0,
  max_views     INTEGER,
  favorite      BOOLEAN NOT NULL DEFAULT false,
  password      TEXT,
  anonymous     BOOLEAN NOT NULL DEFAULT false,
  user_id       TEXT REFERENCES users(id) ON DELETE SET NULL,
  folder_id     TEXT REFERENCES folders(id) ON DELETE SET NULL
);
CREATE INDEX IF NOT EXISTS files_name_idx ON files (name);
CREATE INDEX IF NOT EXISTS files_folder_created_idx ON files (folder_id, created_at);

CREATE TABLE IF NOT EXISTS thumbnails (
  id         TEXT PRIMARY KEY,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  path       TEXT NOT NULL,
  file_id    TEXT NOT NULL UNIQUE REFERENCES files(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS tags (
  id         TEXT PRIMARY KEY,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  name       TEXT NOT NULL UNIQUE,
  color      TEXT NOT NULL,
  user_id    TEXT REFERENCES users(id) ON DELETE SET NULL
);

CREATE TABLE IF NOT EXISTS file_tags (
  file_id TEXT NOT NULL REFERENCES files(id) ON DELETE CASCADE,
  tag_id  TEXT NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
  PRIMARY KEY (file_id, tag_id)
);

CREATE TABLE IF NOT EXISTS urls (
  id          TEXT PRIMARY KEY,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  code        TEXT NOT NULL,
  vanity      TEXT,
  destination TEXT NOT NULL,
  views       INTEGER NOT NULL DEFAULT 0,
  max_views   INTEGER,
  password    TEXT,
  enabled     BOOLEAN NOT NULL DEFAULT true,
  user_id     TEXT REFERENCES users(id) ON DELETE SET NULL
);
CREATE INDEX IF NOT EXISTS urls_code_idx ON urls (code);

CREATE TABLE IF NOT EXISTS incomplete_files (
  id              TEXT PRIMARY KEY,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  status          TEXT NOT NULL CHECK (status IN ('PENDING','PROCESSING','COMPLETE','FAILED')),
  chunks_total    INTEGER NOT NULL,
  chunks_complete INTEGER NOT NULL,
  metadata        JSONB NOT NULL DEFAULT '{}'::jsonb,
  user_id         TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS metrics (
  id         TEXT PRIMARY KEY,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  data       JSONB NOT NULL DEFAULT '{}'::jsonb
);

CREATE TABLE IF NOT EXISTS invites (
  id         TEXT PRIMARY KEY,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at TIMESTAMPTZ,
  code       TEXT NOT NULL UNIQUE,
  uses       INTEGER NOT NULL DEFAULT 0,
  max_uses   INTEGER,
  inviter_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS exports (
  id         TEXT PRIMARY KEY,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  completed  BOOLEAN NOT NULL DEFAULT false,
  path       TEXT NOT NULL,
  files      INTEGER NOT NULL DEFAULT 0,
  size       TEXT NOT NULL DEFAULT '0',
  user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE
);
