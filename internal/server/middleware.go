package server

import (
	"net/http"
	"strings"

	"zipfast/internal/auth"
	"zipfast/internal/models"
)

// authenticate resolves the request's user from an Authorization token (API
// clients / ShareX) or the session cookie. Returns nil when unauthenticated.
func (a *App) authenticate(r *http.Request) *models.User {
	ctx := r.Context()

	if authz := r.Header.Get("Authorization"); authz != "" {
		token := strings.TrimSpace(strings.TrimPrefix(authz, "Bearer "))
		// Tokens may be sent encrypted; try to decrypt, otherwise use as-is.
		if dec, err := auth.DecryptToken(token, a.Cfg.Core.Secret); err == nil && dec != "" {
			token = dec
		}
		if u, err := a.Store.GetUserByToken(ctx, token); err == nil {
			return u
		}
	}

	if a.Sessions != nil {
		s := a.Sessions.Get(r)
		if s.UserID != "" {
			if u, err := a.Store.GetUserByID(ctx, s.UserID); err == nil {
				return u
			}
		}
	}
	return nil
}

// RequireUser is chi middleware that rejects unauthenticated requests (401) and
// attaches the authenticated user to the request context.
func (a *App) RequireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := a.authenticate(r)
		if u == nil {
			a.Error(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next.ServeHTTP(w, WithUser(r, u))
	})
}

// RequireAdmin requires an authenticated ADMIN or SUPERADMIN.
func (a *App) RequireAdmin(next http.Handler) http.Handler {
	return a.RequireUser(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := UserFromContext(r.Context())
		if u == nil || models.RoleRank(u.Role) < models.RoleRank(models.RoleAdmin) {
			a.Error(w, http.StatusForbidden, "forbidden")
			return
		}
		next.ServeHTTP(w, r)
	}))
}

// Scheme returns "https" or "http" for building absolute URLs.
func (a *App) Scheme(r *http.Request) string {
	if a.Cfg.Core.ReturnHTTPSURLs {
		return "https"
	}
	if strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		return "https"
	}
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

// BaseURL returns scheme://host, preferring a configured default domain.
func (a *App) BaseURL(r *http.Request) string {
	host := r.Host
	if a.Cfg.Core.DefaultDomain != "" {
		host = a.Cfg.Core.DefaultDomain
	}
	return a.Scheme(r) + "://" + host
}
