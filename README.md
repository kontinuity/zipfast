# Zipfast (Experimental)

[![CI](https://github.com/kontinuity/zipfast/actions/workflows/ci.yml/badge.svg)](https://github.com/kontinuity/zipfast/actions/workflows/ci.yml)
[![Release](https://github.com/kontinuity/zipfast/actions/workflows/release.yml/badge.svg)](https://github.com/kontinuity/zipfast/actions/workflows/release.yml)
[![Latest release](https://img.shields.io/github/v/release/kontinuity/zipfast?sort=semver)](https://github.com/kontinuity/zipfast/releases)
[![License: MIT](https://img.shields.io/github/license/kontinuity/zipfast)](LICENSE)
[![Go](https://img.shields.io/github/go-mod/go-version/kontinuity/zipfast?logo=go)](go.mod)
[![GHCR image](https://img.shields.io/badge/ghcr.io-kontinuity%2Fzipfast-2496ED?logo=docker&logoColor=white)](https://github.com/kontinuity/zipfast/pkgs/container/zipfast)
[![Platforms](https://img.shields.io/badge/platforms-amd64%20%7C%20arm64-informational)](#container-images)
[![Idle RAM](https://img.shields.io/badge/idle%20RAM-%3C50MB-1f6feb)](#memory)
[![Zipline parity](https://img.shields.io/badge/Zipline%20parity-v4.6.3-blueviolet)](ZIPLINE_PARITY)

A low-footprint reimplementation of [Zipline](https://github.com/diced/zipline) — the
ShareX-compatible file & URL sharing server — written in **Go**. It keeps Zipline v4's
data model, API, and `x-zipline-*` upload contract, so existing clients (ShareX, etc.)
and the v4 SPA work unchanged — but it idles in a fraction of the memory.

> 🧪 **Experimental — use at your own risk.** Zipfast is a young, community-built
> project and a labor of love. It's end-to-end tested and runs happily day to day,
> but it hasn't seen large-scale production use yet — so please keep backups of
> anything important 💾. Bugs get fixed as they surface, and issues & PRs are always
> welcome 🙌 — that's how it gets better. 💛

## Why I ported Zipline to Go

I love Zipline. It's a great self-hosted file/URL sharing server and I run it on a
small VPS. The catch: the Node.js build idled at nearly **500 MB of RAM** — a lot when
the whole box only has a few hundred megabytes to spare for everything else.

So I reimplemented the backend in Go with one hard goal: **keep idle memory under
50 MB.** A compiled binary (no V8, no in-process React/SSR), streamed
uploads/downloads, and media work shelled out to `ffmpeg` get it there — while keeping
full parity with Zipline's API and on-disk/S3 layout so it's a drop-in swap. The
frontend is the same Vite/React SPA as upstream (vendored), served by the Go binary or
from a CDN.

## Features

- ShareX-compatible uploads (`x-zipline-*` headers), multiple filename formats, partial/chunked uploads
- File hosting + URL shortener, folders, tags, password-protected files & folders
- Per-user and per-folder ZIP exports; Zipline v3/v4 data import
- Auth: argon2id passwords, API tokens, sessions; OAuth (Discord/GitHub/Google/OIDC); MFA TOTP + **WebAuthn passkeys**
- Server-rendered OpenGraph/embed meta (crawlers don't need the SPA)
- Local or S3-compatible storage (AWS, MinIO, Ceph, **Backblaze B2**)
- Image compression + video thumbnails via `ffmpeg`; quotas; rate limiting
- Multi-arch images (amd64 + arm64); PostgreSQL only

## Container images

Published to the GitHub Container Registry: **`ghcr.io/kontinuity/zipfast`**

| Tag | Meaning |
| :--- | :--- |
| `latest` | Newest stable release |
| `X.Y.Z` (e.g. `0.1.0`) | A specific release (immutable) |
| `main` / `edge` | Tip of `main`, rebuilt on every merge |
| `main-<sha>` | A specific commit build (immutable, pin-able) |

```bash
docker pull ghcr.io/kontinuity/zipfast:latest
```

All tags are multi-arch manifests (`linux/amd64` + `linux/arm64`). See **[DOCKER.md](DOCKER.md)** for the full image guide (env vars, volumes, compose).

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

The schema applies automatically on boot (idempotent `CREATE TABLE IF NOT EXISTS`).
Build the SPA with `make web` (or `make web-dev` for a live-reload client on :5173 that
proxies the API to :3000). `make build` builds the Go binaries **and** the client.

## Configuration

Config comes from environment variables (see [`.env.example`](.env.example));
admin-editable settings are also persisted in the DB as a JSON blob, and **env always
wins**. `*_FILE` indirection is supported for secrets (e.g.
`CORE_SECRET_FILE=/run/secrets/core`). Required: `CORE_SECRET` (≥16 chars) and
`DATABASE_URL` (or the five `DATABASE_*` parts).

## Storage

Set `DATASOURCE_TYPE`:

- **`local`** — `DATASOURCE_LOCAL_DIRECTORY` (default `./uploads`).
- **`s3`** — any S3-compatible store (AWS, MinIO, Ceph, **Backblaze B2**).

Object keys match Zipline (`{name}`, thumbnails `.thumbnail.{id}`), so existing data
resolves unchanged. S3/B2 specifics are in [DOCKER.md](DOCKER.md#s3--backblaze-b2-storage).

## Memory

A Go binary with a small pgx pool, no V8/SSR, and streamed I/O keeps idle memory under
the **50 MB** goal — roughly a tenth of the Node.js build. Heavy media work runs as a
separate `ffmpeg` process. Details in [STATUS.md](STATUS.md#memory).

## CLI

```bash
zipfastctl read-config              # print effective config as JSON
zipfastctl list-users
zipfastctl set-user --id <id> role ADMIN
zipfastctl version
```

## Project status & architecture

The architecture map, implementation status (done / partial / TODO), and what's been
verified end-to-end live in **[STATUS.md](STATUS.md)**.

## License

[MIT](LICENSE), matching upstream Zipline.
