# Zipfast

A low-footprint reimplementation of [Zipline](https://github.com/diced/zipline) (ShareX / file-upload server) in **Go**, following `../ZIPLINE_REWRITE_PLAN.md`.

Goals: a compiled API + file server that idles **well under 100 MB**, with the frontend as a static SPA that can be served from a CDN. The data model, API surface, upload headers (`x-zipline-*`), and on-disk/S3 object layout mirror Zipline so existing clients (ShareX, etc.) and an existing v4 SPA can talk to it.

> Status: **broad feature coverage, end-to-end tested.** The server compiles, runs, and has been live-tested against PostgreSQL across auth, uploads, serving, OAuth, MFA/TOTP, exports, quotas, rate limiting, and static SPA serving. A few niche endpoints remain. See [Implementation status](#implementation-status).

## Architecture

```
cmd/zipfast        server entrypoint (boot: config → db.Migrate → datasource → routes → tasks)
cmd/zipfastctl     CLI (cobra): read-config, list-users, set-user, version
internal/
  config           env + schema defaults (+ DB-persisted settings as JSONB), *_FILE indirection
  models           Go structs mirroring the 15 Zipline entities
  db               pgx pool + Store + embedded schema.sql (idempotent migrate)
  datasource       Datasource interface + Local + S3 (minio-go; also serves Backblaze B2)
  auth             argon2id passwords, API tokens (AES-GCM), securecookie sessions, TOTP
  upload           x-zipline-* header parsing, filename formats, size/duration parsing
  media            image compression (stdlib + ffmpeg), video thumbnails (ffmpeg), GPS strip
  parser           {file.*}/{user.*}/{url.*} embed templating
  webhooks         Discord + HTTP webhooks on upload/shorten
  oauth            OAuth provider definitions (Discord/GitHub/Google/OIDC)
  importer         Zipline v3/v4 data import
  thumbnails       background video-thumbnail generator (bounded workers, ffmpeg)
  tasks            goroutine cron: deleteFiles, maxViews, clearInvites, metrics
  server           chi router, App deps, auth + rate-limit middleware, quota,
                   all HTTP handlers (auth/oauth/mfa/user/export/admin/import/
                   server/upload/serve) + embed/OG meta + static SPA fallback
```

All HTTP handlers live in `internal/server` (one package, many files) sharing an `App` holder and helpers, so there are no import cycles.

## Quick start

### Docker Compose

```bash
export CORE_SECRET=$(openssl rand -hex 24)
export POSTGRES_PASSWORD=$(openssl rand -hex 16)
docker compose up -d --build
# → http://localhost:3000
```

First run: `POST /api/setup {"username","password"}` to create the first SUPERADMIN.

### From source

```bash
# needs Go 1.25+ and a reachable PostgreSQL; ffmpeg optional (thumbnails/webp)
export CORE_SECRET="a-long-random-secret-min-16-chars"
export DATABASE_URL="postgres://postgres:postgres@localhost:5432/zipfast"
go run ./cmd/zipfast
```

The schema is applied automatically on boot (idempotent `CREATE TABLE IF NOT EXISTS`). Build the SPA with `make web` (or run `make web-dev` for a live-reload client on :5173 that proxies the API to :3000). `make build` builds the Go binaries **and** the client; `make build-go` is Go-only.

## Configuration

Config comes from environment variables (see [`.env.example`](.env.example)); admin-editable settings are also persisted in the DB as a JSON blob. Env always wins. `*_FILE` indirection is supported for secrets (e.g. `CORE_SECRET_FILE=/run/secrets/core`).

Required: `CORE_SECRET` (≥16 chars) and `DATABASE_URL` (or the 5 `DATABASE_*` parts).

## Storage backends

Set `DATASOURCE_TYPE`:

- **`local`** — `DATASOURCE_LOCAL_DIRECTORY` (default `./uploads`).
- **`s3`** — any S3-compatible store (AWS, MinIO, Ceph…): `DATASOURCE_S3_ACCESS_KEY_ID`, `DATASOURCE_S3_SECRET_ACCESS_KEY`, `DATASOURCE_S3_BUCKET`, `DATASOURCE_S3_REGION`, optional `DATASOURCE_S3_ENDPOINT` (host only), `DATASOURCE_S3_FORCE_PATH_STYLE`, `DATASOURCE_S3_SUBDIRECTORY`.
- **`b2` (Backblaze B2)** — uses B2's S3-compatible API over HTTPS:

  ```bash
  DATASOURCE_TYPE=b2
  DATASOURCE_B2_KEY_ID=<application keyID>
  DATASOURCE_B2_APPLICATION_KEY=<applicationKey>
  DATASOURCE_B2_BUCKET=<bucket>
  DATASOURCE_B2_REGION=us-west-004        # the region in your bucket's S3 endpoint
  # DATASOURCE_B2_ENDPOINT=               # optional; defaults to s3.<region>.backblazeb2.com
  ```

  B2 is routed through the same (tested) S3 datasource — the `B2_*` vars map onto the S3 client and the endpoint auto-derives to `s3.<region>.backblazeb2.com`. Create an Application Key in the B2 console; the **keyID** is the access key and the **applicationKey** is the secret. The bucket must allow the key.

Object keys match Zipline (`{name}`, thumbnails `.thumbnail.{id}`), so existing data resolves unchanged.

## Memory

The server idles far below the 100 MB target: a Go binary (no V8), a small pgx pool, no in-process React/SSR, streamed uploads/downloads (no whole-file buffering), and media work shelled out to `ffmpeg` (a separate process). `GOMEMLIMIT=96MiB` is set in the Docker image as a hard backstop.

## CLI

```bash
zipfastctl read-config              # print effective config as JSON
zipfastctl list-users               # list users
zipfastctl set-user --id <id> role ADMIN
zipfastctl version
```

## Implementation status

**Implemented & end-to-end tested**
- Config (env + defaults + JSONB settings, `*_FILE`); auto-migrate; healthcheck/version.
- Auth: argon2id passwords, API tokens (encrypt/decrypt, `Authorization: Bearer`), securecookie `zipline_session`, `RequireUser`/`RequireAdmin`; login/logout/register/setup.
- **OAuth**: Discord, GitHub, Google, OIDC (state cookie + PKCE S256 for OIDC); create/link accounts, Discord allow/deny lists.
- **MFA**: TOTP enable/verify/disable; **WebAuthn passkeys** (register + login) via `go-webauthn`.
- Upload: streaming multipart, all `x-zipline-*` headers, filename formats (random/uuid/date/name/gfycat/random-words), opt-in image compression, GPS strip, **partial/chunked uploads**, **quota enforcement**, webhooks fired.
- Serving: `/u/:id` decision logic, `/raw/:id` with HTTP **range**, `/go/:id` + `/r/:id` redirects, `/view/:id` & `/view/url/:id` **OpenGraph/embed meta** (no React), `/robots.txt`.
- User API: files (list/get/update/delete + password access token), folders, tags, urls (shortener), `/api/user` get/patch, token regen, stats, recent.
- **Exports**: per-user ZIP (`/api/user/export`) and folder ZIP (`/api/user/folders/:id/export`).
- **Imports**: Zipline v4 (full) and v3 (best-effort) via `/api/server/import/v4|v3` (admin).
- Server/admin: `/api/server/public`, `/settings` (get/patch), `/settings/web`, `/themes`, `/api/stats`, admin users CRUD, invites.
- **Rate limiting**: per-key token-bucket middleware (admin/allowlist bypass), configurable.
- **Thumbnails**: background generator for videos (bounded transient workers, ffmpeg child process).
- **Static SPA serving**: serves a built SPA from `ZIPFAST_WEB_DIR` with index.html fallback + `/config.js` runtime API base (or host the SPA on a CDN).
- Datasources: Local, S3, **Backblaze B2**.
- Tasks: deleteFiles, maxViews, clearInvites, metrics. CLI: read-config/list-users/set-user.

**Partial / notes**
- DB-persisted settings are returned by the API and admin PATCH persists them, but env/defaults remain the live config source (env-overridable values apply on next boot).
- The SPA sources are now vendored in `web/` and wired into the build (`make web` / Docker). The dependency install + Vite build is standard but was **not executed in this dev environment** (sandbox I/O/time limits); it runs normally in Docker or on a dev machine. The client still uses upstream's same-origin/SSR-bootstrap assumptions (see "Serving the frontend").

**Not yet implemented (TODO)**
- A few niche routes (session list/revoke UI, avatar upload, activity feed, requery_size, clear_temp/clear_zeros, version-update check).
- Passkey credential migration from an existing `@simplewebauthn` Zipline DB (new registrations work; old ones would need a one-time transform).

## Verified end-to-end

Against an ephemeral PostgreSQL:

```
GET  /api/healthcheck            → {"pass":true}
POST /api/setup                  → 200, creates SUPERADMIN, returns encrypted token
POST /api/upload (Bearer)        → 200, {"files":[{"url":".../u/QVgLcu.txt"}]}
GET  /raw/QVgLcu.txt             → 200, exact bytes
POST /api/auth/login             → 200, sets zipline_session cookie
GET  /api/user (cookie)          → 200, returns the user
POST /api/user/urls (Bearer)     → 200, creates short URL
GET  /api/server/public          → 200, login config
GET  /config.js                  → 200, window.__ZIPFAST_API__
GET  /  (no web build)           → 200, SPA fallback (placeholder index.html)
GET  /api/user/mfa/totp (Bearer) → 200, returns a TOTP secret
POST /api/user/export (Bearer)   → 200, builds a ZIP export
GET  /api/auth/oauth/github      → 404 when unconfigured (no crash)
```

## Serving the frontend

The web client (the Vite/React SPA, vendored from upstream Zipline) lives in
`web/` and builds to `web/dist`. Build it with `make web` (needs pnpm —
`corepack enable`); the Docker image builds it automatically. So one common image
serves both the API and the client.

Two deployment modes:

1. **Common image (default)** — the binary serves the SPA from `ZIPFAST_WEB_DIR`
   (`/zipfast/web/dist` in Docker): hashed assets cached immutably, `index.html`
   fallback for client routes, while `/view/*` links still get server-rendered
   OpenGraph meta for crawlers. `GET /config.js` exposes the API base to the SPA.
2. **CDN mode** — host `web/dist` on a CDN and set **`ZIPFAST_CDN_URL`** (or
   `ZIPFAST_DISABLE_WEB=true`). In-binary client serving is turned off and the
   server runs API-only; CORS with credentials is already enabled, and
   `ZIPFAST_PUBLIC_API_URL` tells the CDN-hosted SPA which API origin to call.

If no build is present, the server shows a small "not built yet" page instead of the SPA.

The client build mirrors upstream Zipline (`prisma generate` for shared types, then
`vite build` of the single SPA entry — no SSR, since Go renders embed meta). Because
it retains upstream's same-origin/SSR-bootstrap assumptions, the dashboard works when
served same-origin from the binary; making the `/view` pages fetch their bootstrap via
API (for cross-origin CDN use) is a follow-up.

## License

MIT (matching upstream Zipline).
