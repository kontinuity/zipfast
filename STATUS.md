# Zipfast — status & architecture

Companion to the [README](README.md): internal architecture, memory rationale,
implementation status, and what's been verified end-to-end.

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

## Memory

The project's design goal is to **idle under 50 MB** — roughly a tenth of the
~500 MB the upstream Node.js build used on the same small VPS. What gets it there:

- a compiled Go binary (no V8, no in-process React/SSR),
- a small pgx connection pool,
- streamed uploads/downloads (no whole-file buffering), and
- media work shelled out to `ffmpeg`, which runs as a separate process.

The Docker image sets `GOMEMLIMIT=96MiB` (with `GOGC=75`). That is a **soft ceiling
for burst workloads** (e.g. building a ZIP export or compressing an image), not the
idle figure — steady-state usage sits well below it. Lower it for very constrained
hosts, or raise it if you routinely export large archives.

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
- Datasources: Local, S3 (incl. Backblaze B2 via the S3 endpoint).
- Tasks: deleteFiles, maxViews, clearInvites, metrics. CLI: read-config/list-users/set-user.
- Two-level structured logging (`LOG_LEVEL` info/debug) with secret/PII redaction.

**Partial / notes**

- DB-persisted settings are returned by the API and admin PATCH persists them, but env/defaults remain the live config source (env-overridable values apply on next boot).
- The SPA sources are vendored in `web/` and wired into the build (`make web` / Docker). The client still uses upstream's same-origin/SSR-bootstrap assumptions (see "Serving the frontend").

**Not yet implemented (TODO)**

- A few niche routes (activity feed, `requery_size`, `clear_temp`/`clear_zeros`, version-update check).
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
GET  /api/user/mfa/totp (Bearer) → 200, returns a TOTP secret
POST /api/user/export (Bearer)   → 200, builds a ZIP export
GET  /api/auth/oauth/github      → 404 when unconfigured (no crash)
```

## Serving the frontend

The web client (the Vite/React SPA, vendored from upstream Zipline) lives in `web/`
and builds to `web/dist`. Build it with `make web` (needs pnpm — `corepack enable`);
the Docker image builds it automatically, so one image serves both the API and client.

Two deployment modes:

1. **Common image (default)** — the binary serves the SPA from `ZIPFAST_WEB_DIR`
   (`/zipfast/web/dist` in Docker): hashed assets cached immutably, `index.html`
   fallback for client routes, while `/view/*` links still get server-rendered
   OpenGraph meta for crawlers. `GET /config.js` exposes the API base to the SPA.
2. **CDN mode** — host `web/dist` on a CDN and set **`ZIPFAST_CDN_URL`** (or
   `ZIPFAST_DISABLE_WEB=true`). In-binary client serving is turned off and the
   server runs API-only; CORS with credentials is enabled, and
   `ZIPFAST_PUBLIC_API_URL` tells the CDN-hosted SPA which API origin to call.

If no build is present, the server shows a small "not built yet" page instead of the SPA.

The client build mirrors upstream Zipline (`prisma generate` for shared types, then
`vite build` of the single SPA entry — no SSR, since Go renders embed meta). Because
it retains upstream's same-origin/SSR-bootstrap assumptions, the dashboard works when
served same-origin from the binary; making the `/view` pages fetch their bootstrap via
API (for cross-origin CDN use) is a follow-up.
