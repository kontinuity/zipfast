// Package server wires configuration, storage, the datasource and HTTP routes.
// All HTTP handlers live in this package (split across files) so they can share
// the App dependency holder and helpers without import cycles.
package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

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

	// Tampered lists the setting keys overridden by environment variables (locked
	// in the admin UI). cfgMu guards live swaps of Cfg/Tampered on settings change.
	Tampered []string
	cfgMu    sync.Mutex

	// SPAIndex is the contents of the SPA index.html used by the embed/meta endpoint
	// (may be empty if the SPA is served entirely from a CDN).
	SPAIndex []byte
}

// ReloadSettings rebuilds the effective config from the DB-persisted settings
// blob (env still wins) and swaps it in live, so admin Settings changes take
// effect without a restart — matching Zipline. Best-effort: on error the current
// config is kept.
func (a *App) ReloadSettings(ctx context.Context) {
	data, _, err := a.Store.LoadSettings(ctx)
	if err != nil {
		a.Log.Warn("reload settings: load failed", "err", err)
		return
	}
	var blob map[string]any
	if len(data) > 0 {
		if uerr := json.Unmarshal(data, &blob); uerr != nil {
			a.Log.Warn("reload settings: decode failed", "err", uerr)
			return
		}
	}
	eff, berr := config.BuildEffective(blob)
	if berr != nil {
		a.Log.Warn("reload settings: build effective failed", "err", berr)
		return
	}
	a.cfgMu.Lock()
	a.Cfg = eff
	a.Tampered = config.EnvTamperedKeys()
	a.cfgMu.Unlock()
}

// Router builds the root HTTP handler. Feature route groups are registered by
// the register* methods (defined across the package's files).
func (a *App) Router() http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	if a.Cfg.Core.TrustProxy {
		// Only trust proxy headers when the operator opts in via CORE_TRUST_PROXY,
		// so RealIP's X-Forwarded-For spoofing caveat does not apply here.
		r.Use(middleware.RealIP) //nolint:staticcheck // intentional, gated by TrustProxy
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

// logFor returns a request-scoped logger carrying the request id (and the
// authenticated user id when present) so handler logs correlate with the access
// log. It never attaches secrets, tokens, or request bodies.
func (a *App) logFor(r *http.Request) *slog.Logger {
	l := a.Log
	if id := middleware.GetReqID(r.Context()); id != "" {
		l = l.With("reqId", id)
	}
	if u := UserFromContext(r.Context()); u != nil {
		l = l.With("userId", u.ID)
	}
	return l
}

// requestLogger emits one access-log line per request. At INFO it is a concise
// summary (method, path, status, duration, bytes) — the path never contains the
// query string, so no secrets leak. At DEBUG it adds client IP, user-agent,
// referer and the query string with sensitive parameters redacted. The
// healthcheck is logged at DEBUG only to avoid flooding INFO.
func (a *App) requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

		next.ServeHTTP(ww, r)

		status := ww.Status()
		if status == 0 {
			status = http.StatusOK
		}
		reqID := middleware.GetReqID(r.Context())

		base := []any{
			"reqId", reqID,
			"method", r.Method,
			"path", r.URL.Path,
			"status", status,
			"durationMs", time.Since(start).Milliseconds(),
			"bytes", ww.BytesWritten(),
		}

		if r.URL.Path == "/api/healthcheck" {
			a.Log.Debug("request", base...)
			return
		}

		a.Log.Info("request", base...)

		if a.Log.Enabled(r.Context(), slog.LevelDebug) {
			detail := []any{
				"reqId", reqID,
				"remoteIp", clientIP(r),
				"ua", r.UserAgent(),
			}
			if ref := r.Referer(); ref != "" {
				detail = append(detail, "referer", ref)
			}
			if q := redactQuery(r.URL.RawQuery); q != "" {
				detail = append(detail, "query", q)
			}
			a.Log.Debug("request detail", detail...)
		}
	})
}

// sensitiveQueryKeys are query parameters whose values must never be logged.
var sensitiveQueryKeys = map[string]bool{
	"token": true, "password": true, "code": true, "secret": true,
	"client_secret": true, "access_token": true, "refresh_token": true,
	"state": true, "key": true, "id_token": true,
}

// redactQuery returns the query string with the values of sensitive parameters
// replaced by "redacted". Unparseable input is dropped entirely (fail closed).
func redactQuery(raw string) string {
	if raw == "" {
		return ""
	}
	values, err := url.ParseQuery(raw)
	if err != nil {
		return "redacted"
	}
	for k := range values {
		if sensitiveQueryKeys[strings.ToLower(k)] {
			values.Set(k, "redacted")
		}
	}
	return values.Encode()
}

// clientIP returns the request's remote address without the port.
func clientIP(r *http.Request) string {
	addr := r.RemoteAddr
	if i := strings.LastIndex(addr, ":"); i > 0 {
		return addr[:i]
	}
	return addr
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
