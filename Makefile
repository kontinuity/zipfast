# Zipfast — local development Makefile (no Docker).
#
# Assumes a local Go toolchain and a local PostgreSQL (createdb/psql on PATH).
# Configuration is read from a .env file if present (see `make env`), otherwise
# the dev defaults below are used. Override any of them inline, e.g.
#   make run CORE_PORT=4000
#
# Quick start:
#   make env        # create .env from .env.example
#   make db-create  # create the local 'zipfast' database
#   make web        # build the SPA into web/dist (needs pnpm; `corepack enable`)
#   make run        # build deps, apply schema, start the server on :3000
#
# `make build` builds the Go binaries AND the web client; `make build-go` is
# Go-only. For an SPA hot-reload loop, run `make run` in one terminal and
# `make web-dev` in another (Vite on :5173 proxies the API to :3000).

GO         ?= go
CMD_SERVER := ./cmd/zipfast
CMD_CTL    := ./cmd/zipfastctl
BIN_DIR    := bin
VERSION    ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo dev)
LDFLAGS    := -s -w -X main.version=$(VERSION)

# Web client (the Vite SPA under ./web). PM is the JS package manager; pnpm is
# what upstream Zipline uses — enable it with `corepack enable` if needed.
WEB_DIR    := web
PM         ?= pnpm

# --- local dev defaults (overridden by .env or `make VAR=...`) ---
CORE_SECRET                ?= dev-secret-change-me-please-32chars
CORE_PORT                  ?= 3000
DATASOURCE_LOCAL_DIRECTORY ?= ./uploads
DB_NAME                    ?= zipfast
# No user => libpq/pgx use the current OS user (typical local Postgres setup).
DATABASE_URL               ?= postgres://localhost:5432/$(DB_NAME)?sslmode=disable

# Source .env (if any) then fall back to the defaults above for anything unset.
# Used by targets that actually run the binaries against the database.
ENV_PREAMBLE = set -a; [ -f .env ] && . ./.env; set +a; \
	CORE_SECRET="$${CORE_SECRET:-$(CORE_SECRET)}" \
	CORE_PORT="$${CORE_PORT:-$(CORE_PORT)}" \
	DATABASE_URL="$${DATABASE_URL:-$(DATABASE_URL)}" \
	DATASOURCE_LOCAL_DIRECTORY="$${DATASOURCE_LOCAL_DIRECTORY:-$(DATASOURCE_LOCAL_DIRECTORY)}"

.DEFAULT_GOAL := help

.PHONY: help env run watch ctl build build-go install web web-install web-build web-dev web-clean \
        test test-race fmt fmt-check vet tidy lint check clean db-create db-drop db-reset psql tools

help: ## Show this help
	@echo "Zipfast — local dev targets:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

env: ## Create .env from .env.example (if missing)
	@if [ -f .env ]; then echo ".env already exists"; else cp .env.example .env && echo "created .env (edit it before running)"; fi

run: ## Run the API server (schema auto-applies on boot)
	@mkdir -p $(DATASOURCE_LOCAL_DIRECTORY)
	@$(ENV_PREAMBLE) $(GO) run $(CMD_SERVER)

watch: ## Run the server with live reload (requires 'air'; `make tools`)
	@command -v air >/dev/null 2>&1 || { echo "air not found — install with 'make tools' or just use 'make run'"; exit 1; }
	@mkdir -p $(DATASOURCE_LOCAL_DIRECTORY)
	@$(ENV_PREAMBLE) air \
		--build.cmd "$(GO) build -o $(BIN_DIR)/zipfast $(CMD_SERVER)" \
		--build.bin "$(BIN_DIR)/zipfast"

ctl: ## Run zipfastctl, e.g. `make ctl ARGS="list-users"`
	@$(ENV_PREAMBLE) $(GO) run $(CMD_CTL) $(ARGS)

build: build-go web ## Build everything: server, ctl, and the web client

build-go: ## Build server + ctl into ./bin
	@mkdir -p $(BIN_DIR)
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/zipfast $(CMD_SERVER)
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/zipfastctl $(CMD_CTL)

install: ## go install both binaries into GOBIN
	$(GO) install -ldflags "$(LDFLAGS)" $(CMD_SERVER) $(CMD_CTL)

# --- web client (Vite SPA) ---

web: web-build ## Build the web client into web/dist

web-install: ## Install web client dependencies (needs pnpm; `corepack enable`)
	cd $(WEB_DIR) && $(PM) install --ignore-scripts

web-build: web-install ## Build the web client SPA (-> web/dist)
	cd $(WEB_DIR) && $(PM) run build

web-dev: ## Run the Vite dev server (proxies /api etc. to :3000)
	cd $(WEB_DIR) && $(PM) run dev

web-clean: ## Remove web build artifacts (dist, node_modules, generated prisma)
	rm -rf $(WEB_DIR)/dist $(WEB_DIR)/node_modules $(WEB_DIR)/src/prisma

test: ## Run tests
	$(GO) test ./...

test-race: ## Run tests with the race detector
	$(GO) test -race ./...

fmt: ## Format the code (gofmt -w)
	gofmt -w ./internal ./cmd

fmt-check: ## Fail if any file is not gofmt-clean
	@out=$$(gofmt -l ./internal ./cmd); if [ -n "$$out" ]; then echo "needs gofmt:"; echo "$$out"; exit 1; fi

vet: ## Run go vet
	$(GO) vet ./...

tidy: ## Tidy go.mod / go.sum
	$(GO) mod tidy

lint: ## Run golangci-lint if installed
	@command -v golangci-lint >/dev/null 2>&1 && golangci-lint run || echo "golangci-lint not installed (skip); 'make tools' to add it"

check: fmt-check vet test ## Format-check, vet, and test

clean: web-clean ## Remove all build artifacts (Go + web)
	rm -rf $(BIN_DIR)
	$(GO) clean

# --- local PostgreSQL helpers (no Docker; need createdb/dropdb/psql on PATH) ---

db-create: ## Create the local database (DB_NAME, default zipfast)
	@createdb $(DB_NAME) 2>/dev/null && echo "created database $(DB_NAME)" || echo "database $(DB_NAME) already exists (or createdb unavailable)"

db-drop: ## Drop the local database (DB_NAME, default zipfast)
	@dropdb --if-exists $(DB_NAME) && echo "dropped database $(DB_NAME)"

db-reset: db-drop db-create ## Drop and recreate the local database

psql: ## Open a psql shell on the local database
	@psql $(DB_NAME)

tools: ## Install optional dev tools (air, golangci-lint)
	$(GO) install github.com/air-verse/air@latest
	$(GO) install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
