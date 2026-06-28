# Zipfast — common image that serves both the API/file server and the SPA.
#
# Three stages: build the web client, build the Go binaries, then assemble a
# small runtime image. The server serves the SPA from /zipfast/web/dist unless
# the CDN option is enabled (ZIPFAST_CDN_URL set, or ZIPFAST_DISABLE_WEB=true),
# in which case client serving is turned off and the API runs standalone.

# --- 1) web client build (Vite SPA -> web/dist) ---
FROM node:22-alpine AS webbuild
WORKDIR /web
RUN corepack enable
# Dependencies first for layer caching.
COPY web/package.json web/pnpm-lock.yaml ./
RUN pnpm install --ignore-scripts
# Then the sources, and build (runs `prisma generate && vite build`).
COPY web/ ./
RUN pnpm run build

# --- 2) Go build (static, cgo-free) ---
FROM golang:1.26-alpine AS gobuild
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG ZIPFAST_VERSION=docker
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${ZIPFAST_VERSION}" -o /out/zipfast ./cmd/zipfast \
 && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/zipfastctl ./cmd/zipfastctl

# --- 3) runtime ---
FROM alpine:3.20
# ffmpeg: video thumbnails + webp/jxl transcode; ca-certificates: HTTPS (S3/webhooks);
# wget: healthcheck.
RUN apk add --no-cache ffmpeg ca-certificates tzdata wget && adduser -D -u 10001 zipfast
WORKDIR /zipfast

COPY --from=gobuild /out/zipfast /out/zipfastctl /usr/local/bin/
COPY --from=webbuild /web/dist ./web/dist

ENV CORE_PORT=3000 \
    CORE_HOSTNAME=0.0.0.0 \
    DATASOURCE_TYPE=local \
    DATASOURCE_LOCAL_DIRECTORY=/zipfast/uploads \
    ZIPFAST_WEB_DIR=/zipfast/web/dist \
    GOMEMLIMIT=96MiB \
    GOGC=75 \
    GODEBUG=madvdontneed=1
# CDN mode (client served elsewhere): set ZIPFAST_CDN_URL (or ZIPFAST_DISABLE_WEB=true)
# to disable in-binary client serving and run the API standalone.

# The app creates the uploads dir at startup; make /zipfast writable by the
# non-root runtime user (uid 10001) so that mkdir doesn't fail with EACCES.
RUN mkdir -p /zipfast/uploads && chown -R 10001:10001 /zipfast

EXPOSE 3000
USER zipfast

HEALTHCHECK --interval=15s --timeout=3s --retries=3 \
  CMD wget -qO- http://localhost:3000/api/healthcheck >/dev/null 2>&1 || exit 1

ENTRYPOINT ["zipfast"]
