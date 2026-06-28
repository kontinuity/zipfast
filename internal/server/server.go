// Package server wires configuration, storage, the datasource and HTTP routes.
// All HTTP handlers live in this package (split across files) so they can share
// the App dependency holder and helpers without import cycles.
package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"zipfast/internal/auth"
	"zipfast/internal/config"
	"zipfast/internal/datasource"
	"zipfast/internal/db"
	"zipfast/internal/models"
)

// App holds shared dependencies for all handlers.
type App struct {
	Cfg      *config.Config
	Store    *db.Store
	DS       datasource.Datasource
	Log      *slog.Logger
	Version  string
	Sessions *auth.SessionManager

	// SPAIndex is the contents of the SPA index.html used by the embed/meta endpoint
	// (may be empty if the SPA is served entirely from a CDN).
	SPAIndex []byte
}

// Router builds the root HTTP handler. Feature route groups are registered by
// the register* methods (defined across the package's files).
func (a *App) Router() http.Handler {
	r := chi.NewRouter()

	if a.Cfg.Core.TrustProxy {
		r.Use(middleware.RealIP)
	}
	r.Use(middleware.Recoverer)
	r.Use(a.requestLogger)
	r.Use(a.cors)
	r.Use(a.RateLimit)

	// Always-on
	r.Get("/api/healthcheck", a.handleHealthcheck)
	r.Get("/api/version", a.handleVersion)

	// Feature route groups (auth/oauth/mfa, user, export, admin, import, server,
	// upload, serve, static).
	a.mountFeatureRoutes(r)

	// SPA fallback: anything not matched above and not a reserved/API path is
	// served from the static web dir (index.html for client routes).
	r.NotFound(a.spaFallback)

	return r
}

func (a *App) handleHealthcheck(w http.ResponseWriter, r *http.Request) {
	if err := a.Store.Pool.Ping(r.Context()); err != nil {
		a.Error(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	a.WriteJSON(w, http.StatusOK, map[string]bool{"pass": true})
}

func (a *App) handleVersion(w http.ResponseWriter, r *http.Request) {
	ver := a.Version
	if ver == "" {
		ver = "dev"
	}
	const repo = "https://github.com/diced/zipline"

	// Self-consistent "on latest" payload matching the original /api/version shape
	// ({ data, details, cached }). No external update check (works offline).
	a.WriteJSON(w, http.StatusOK, map[string]any{
		"data": map[string]any{
			"isUpstream": false,
			"isRelease":  true,
			"isLatest":   true,
			"version":    map[string]any{"tag": ver, "sha": "", "url": repo},
			"latest":     map[string]any{"tag": ver, "url": repo + "/releases/latest"},
		},
		"details": map[string]any{"version": ver, "sha": nil},
		"cached":  true,
	})
}

// --- shared helpers (handlers must use these; do not redefine) ---

// WriteJSON writes v as a JSON response with the given status.
func (a *App) WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

// Error writes a Zipline-style JSON error ({ statusCode, message }).
func (a *App) Error(w http.ResponseWriter, status int, message string) {
	a.WriteJSON(w, status, map[string]any{"statusCode": status, "message": message})
}

// ReadJSON decodes the request body into v.
func (a *App) ReadJSON(r *http.Request, v any) error {
	return json.NewDecoder(r.Body).Decode(v)
}

// --- user context ---

type ctxKey int

const userCtxKey ctxKey = iota

// WithUser returns a request whose context carries the authenticated user.
func WithUser(r *http.Request, u *models.User) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), userCtxKey, u))
}

// UserFromContext returns the authenticated user, or nil.
func UserFromContext(ctx context.Context) *models.User {
	u, _ := ctx.Value(userCtxKey).(*models.User)
	return u
}

func (a *App) requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
		a.Log.Debug("request", "method", r.Method, "path", r.URL.Path)
	})
}

// cors enables credentialed cross-origin requests so the SPA can be hosted on a CDN.
func (a *App) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, "+
				"x-zipline-format, x-zipline-image-compression-percent, x-zipline-image-compression-type, "+
				"x-zipline-password, x-zipline-max-views, x-zipline-no-json, x-zipline-original-name, "+
				"x-zipline-folder, x-zipline-filename, x-zipline-domain, x-zipline-file-extension, "+
				"x-zipline-deletes-at, content-range, x-zipline-p-filename, x-zipline-p-content-type, "+
				"x-zipline-p-identifier, x-zipline-p-lastchunk, x-zipline-p-content-length")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, PUT, OPTIONS")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
