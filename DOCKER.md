# Zipfast

![License](https://img.shields.io/badge/license-MIT-green)
![Platforms](https://img.shields.io/badge/platforms-amd64%20%7C%20arm64-informational)
![Base](https://img.shields.io/badge/base-alpine%203.20-0D597F?logo=alpinelinux)
![Parity](https://img.shields.io/badge/Zipline%20parity-v4.6.3-blueviolet)

A low-footprint, **Go** reimplementation of [Zipline](https://github.com/diced/zipline) — the ShareX / file-upload & URL-shortener server. Same data model, API surface, `x-zipline-*` upload headers, and on-disk/S3 object layout as Zipline v4, so existing clients (ShareX, etc.) and the v4 SPA work unchanged — but the server idles **well under 100 MB**.

- **Tiny footprint** — a single static Go binary (no Node/V8, no in-process React/SSR), streamed uploads/downloads, `GOMEMLIMIT=96MiB` baked in as a backstop.
- **Drop-in compatible** — ShareX configs, `x-zipline-*` headers, and Zipline object keys all resolve unchanged; v3/v4 data import included.
- **Batteries included** — OAuth (Discord/GitHub/Google/OIDC), MFA TOTP + WebAuthn passkeys, folders, tags, URL shortener, per-user & folder ZIP exports, quotas, rate limiting, OpenGraph/embed meta rendered server-side, and video thumbnails via bundled `ffmpeg`.
- **Local or S3 storage** — local disk or any S3-compatible store (AWS, MinIO, Ceph, Backblaze B2).
- **Multi-arch** — `linux/amd64` and `linux/arm64`.

## Supported tags

| Tag | Meaning |
| :--- | :--- |
| `latest` | Newest stable release. |
| `X.Y.Z` (e.g. `0.1.0`) | A specific stable release (immutable). |
| `main` | Tip of the default branch (rolling, rebuilt on every merge). |
| `edge` | Alias for the latest `main` build. |
| `main-<sha>` | A specific commit build (immutable, pin-able). |

All tags are multi-arch manifests (`linux/amd64` + `linux/arm64`). Per-arch tags (`…-amd64` / `…-arm64`) are also published if you need to pin an architecture.

## Image registries

The same image is published to both:

```
ghcr.io/kontinuity/zipfast:latest        # GitHub Container Registry
docker.io/<dockerhub-namespace>/zipfast:latest
```

## Quick start

Zipfast needs a **PostgreSQL** database and two required settings: `CORE_SECRET` (≥16 chars) and `DATABASE_URL`. The fastest path is Docker Compose (below). To run the container against an existing Postgres:

```bash
docker run -d --name zipfast -p 3000:3000 \
  -e CORE_SECRET="$(openssl rand -hex 24)" \
  -e DATABASE_URL="postgres://user:pass@db-host:5432/zipfast" \
  -v zipfast_uploads:/zipfast/uploads \
  ghcr.io/kontinuity/zipfast:latest
```

Then open `http://localhost:3000` and create the first admin:

```bash
curl -X POST http://localhost:3000/api/setup \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"a-strong-password"}'
```

This returns the first **SUPERADMIN** account and an API token you can drop straight into a ShareX custom uploader.

## Docker Compose

```yaml
services:
  postgres:
    image: postgres:16-alpine
    restart: unless-stopped
    environment:
      POSTGRES_USER: zipfast
      POSTGRES_PASSWORD: ${POSTGRES_PASSWORD:?set POSTGRES_PASSWORD}
      POSTGRES_DB: zipfast
    volumes:
      - pgdata:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD", "pg_isready", "-U", "zipfast"]
      interval: 10s
      timeout: 5s
      retries: 5

  zipfast:
    image: ghcr.io/kontinuity/zipfast:latest
    restart: unless-stopped
    depends_on:
      postgres:
        condition: service_healthy
    environment:
      CORE_SECRET: ${CORE_SECRET:?set CORE_SECRET (>=16 chars)}
      DATABASE_URL: postgres://zipfast:${POSTGRES_PASSWORD}@postgres:5432/zipfast
      DATASOURCE_TYPE: local
      DATASOURCE_LOCAL_DIRECTORY: /zipfast/uploads
    ports:
      - "3000:3000"
    volumes:
      # Named volume — Docker initializes it with the image dir's ownership
      # (uid 10001), so the non-root app can write. A host bind mount would be
      # root-owned and fail unless you `chown 10001:10001` it on the host first.
      - uploads:/zipfast/uploads

volumes:
  pgdata:
  uploads:
```

```bash
export CORE_SECRET=$(openssl rand -hex 24)
export POSTGRES_PASSWORD=$(openssl rand -hex 16)
docker compose up -d
# → http://localhost:3000
```

## Configuration

Everything is configured by environment variables; admin-editable settings are also persisted in the database (env always wins). Every secret variable supports `*_FILE` indirection for Docker/Kubernetes secrets, e.g. `CORE_SECRET_FILE=/run/secrets/core_secret`. The most common variables:

| Variable | Default | Description |
| :--- | :--- | :--- |
| `CORE_SECRET` | — | **Required.** ≥16 chars; seals session cookies and encrypts tokens. |
| `DATABASE_URL` | — | **Required.** Postgres DSN. (Or set `DATABASE_USERNAME/PASSWORD/HOST/PORT/NAME`.) |
| `CORE_PORT` | `3000` | Port the server listens on. |
| `CORE_HOSTNAME` | `0.0.0.0` | Bind address. |
| `CORE_RETURN_HTTPS_URLS` | `false` | Emit `https://` URLs and mark cookies `Secure` (set `true` behind TLS). |
| `CORE_TRUST_PROXY` | `false` | Honor `X-Forwarded-*` when behind a reverse proxy. |
| `DATASOURCE_TYPE` | `local` | `local` or `s3`. |
| `DATASOURCE_LOCAL_DIRECTORY` | `/zipfast/uploads` | Upload dir for `local` storage (in-image default). |
| `FILES_ROUTE` | `/u` | Public file route prefix. |
| `FILES_LENGTH` | `6` | Generated file-slug length. |
| `FILES_DEFAULT_FORMAT` | `random` | `random` / `uuid` / `date` / `name` / `gfycat` / `random-words`. |
| `FILES_MAX_FILE_SIZE` | `100mb` | Per-file upload cap. |
| `URLS_ROUTE` | `/go` | Short-URL route prefix. |
| `FEATURES_USER_REGISTRATION` | `false` | Allow public sign-ups. |
| `FEATURES_THUMBNAILS_ENABLED` | `true` | Generate video thumbnails (`ffmpeg`). |
| `FEATURES_IMAGE_COMPRESSION` | `true` | Opt-in image compression. |
| `LOG_LEVEL` | `info` | `debug` / `info` / `warn` / `error`. Use `debug` for verbose diagnostics. |
| `GOMEMLIMIT` | `96MiB` | Go soft memory limit (baked in; raise for very large workloads). |

> See [`.env.example`](https://github.com/kontinuity/zipfast/blob/main/.env.example) for the complete list (S3 options, webhooks, MFA, CDN/SPA serving, etc.).

### S3 / Backblaze B2 storage

Set `DATASOURCE_TYPE=s3` plus credentials. Any S3-compatible store works:

```bash
DATASOURCE_TYPE=s3
DATASOURCE_S3_ACCESS_KEY_ID=...
DATASOURCE_S3_SECRET_ACCESS_KEY=...
DATASOURCE_S3_BUCKET=zipfast
DATASOURCE_S3_REGION=us-east-1
DATASOURCE_S3_ENDPOINT=             # host only; blank = AWS. e.g. MinIO or B2 endpoint
DATASOURCE_S3_FORCE_PATH_STYLE=false
```

For **Backblaze B2**, use `s3` and point `DATASOURCE_S3_ENDPOINT` at your bucket's S3 endpoint (e.g. `s3.us-west-004.backblazeb2.com`, region `us-west-004`); the B2 `keyID` is the access key and `applicationKey` the secret. There is no separate `b2` type — this mirrors upstream Zipline.

## Serving the frontend

By default the binary also serves the bundled SPA from `/zipfast/web/dist` with an `index.html` fallback, while `/view/*` links still get server-rendered OpenGraph meta for crawlers. To run **API-only** and host the SPA on a CDN, set `ZIPFAST_CDN_URL=https://cdn.example.com` (or `ZIPFAST_DISABLE_WEB=true`) and `ZIPFAST_PUBLIC_API_URL` so the CDN-hosted client knows which API origin to call.

## Volumes

| Path | Purpose |
| :--- | :--- |
| `/zipfast/uploads` | Uploaded files when `DATASOURCE_TYPE=local`. Persist it. |

The container runs as the **non-root** user `zipfast` (uid `10001`). Prefer a named volume so Docker seeds the correct ownership; for a host bind mount, `chown 10001:10001` the directory first. When using `s3` storage no upload volume is needed.

## Ports & health

- Exposes **`3000`** (override with `CORE_PORT`).
- A built-in `HEALTHCHECK` polls `GET /api/healthcheck` (`{"pass":true}`), so `docker ps` and orchestrators report real readiness.

## Image internals

- Multi-stage build → final image on **Alpine 3.20**.
- Includes `ffmpeg` (video thumbnails + webp/jxl transcode), `ca-certificates` (HTTPS to S3/webhooks), and `tzdata`.
- Ships two binaries: `zipfast` (server, entrypoint) and `zipfastctl` (admin CLI).
- Schema is applied automatically on boot (idempotent `CREATE TABLE IF NOT EXISTS`) — no migration step to run.
- Tuned Go runtime: `GOMEMLIMIT=96MiB`, `GOGC=75`, `GODEBUG=madvdontneed=1`.

## Admin CLI

`zipfastctl` is bundled for one-off operations:

```bash
docker exec -it zipfast zipfastctl read-config           # print effective config (JSON)
docker exec -it zipfast zipfastctl list-users
docker exec -it zipfast zipfastctl set-user --id <id> role ADMIN
docker exec -it zipfast zipfastctl version
```

## Versioning & Zipline parity

Zipfast follows its **own** version line (`X.Y.Z`). Each release records the upstream Zipline version it has reached feature parity with as the `sh.zipfast.zipline-parity` image label (currently **4.6.3**):

```bash
docker inspect --format '{{ index .Config.Labels "sh.zipfast.zipline-parity" }}' \
  ghcr.io/kontinuity/zipfast:latest
```

## Links

- **Source & issues:** https://github.com/kontinuity/zipfast
- **Upstream Zipline:** https://github.com/diced/zipline
- **License:** MIT (matching upstream Zipline)
