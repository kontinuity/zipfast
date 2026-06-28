package server

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/lucsky/cuid"

	"zipfast/internal/auth"
	"zipfast/internal/models"
	"zipfast/internal/oauth"
)

// oauthStateCookieName is the short-lived, HttpOnly cookie that carries the CSRF
// state (and, for OIDC, the PKCE verifier) between the start and callback legs of
// the flow. Its value is "provider:state:verifier".
const oauthStateCookieName = "zf_oauth"

// oauthStateCookieMaxAge bounds how long an in-flight authorization may take.
const oauthStateCookieMaxAge = 10 * 60 // 10 minutes

// oauthHTTPClient is the shared client for token/userinfo calls. A fixed timeout
// prevents a slow or hostile provider from tying up a request goroutine.
var oauthHTTPClient = &http.Client{Timeout: 15 * time.Second}

// oauthState holds the parsed contents of the zf_oauth cookie.
type oauthState struct {
	Provider string
	State    string
	Verifier string
}

// registerOAuthRoutes mounts the OAuth login endpoints: a per-provider start
// (redirect to the provider's authorize URL) and callback (code exchange +
// session establishment). Provider configuration is read from a.Cfg.OAuth.
func (a *App) registerOAuthRoutes(r chi.Router) {
	r.Get("/api/auth/oauth/{provider}", a.handleOAuthStart)
	r.Get("/api/auth/oauth/{provider}/callback", a.handleOAuthCallback)
}

// oauthCfgFor returns the per-provider config block for a lowercase provider
// name, plus ok=false if the name is not a known provider.
func (a *App) oauthCfgFor(name string) (cfgClientID, cfgClientSecret, cfgRedirectURI string, allowed, denied []string, ok bool) {
	switch oauth.Name(name) {
	case oauth.Discord:
		c := a.Cfg.OAuth.Discord
		return c.ClientID, c.ClientSecret, c.RedirectURI, c.AllowedIDs, c.DeniedIDs, true
	case oauth.GitHub:
		c := a.Cfg.OAuth.Github
		return c.ClientID, c.ClientSecret, c.RedirectURI, nil, nil, true
	case oauth.Google:
		c := a.Cfg.OAuth.Google
		return c.ClientID, c.ClientSecret, c.RedirectURI, nil, nil, true
	case oauth.OIDC:
		c := a.Cfg.OAuth.OIDC
		return c.ClientID, c.ClientSecret, c.RedirectURI, nil, nil, true
	default:
		return "", "", "", nil, nil, false
	}
}

// oauthProvider resolves the static provider definition (with OIDC endpoints
// filled in from config) for the named provider.
func (a *App) oauthProvider(name string) (oauth.Provider, bool) {
	return oauth.ProviderFor(name,
		a.Cfg.OAuth.OIDC.AuthorizeURL,
		a.Cfg.OAuth.OIDC.TokenURL,
		a.Cfg.OAuth.OIDC.UserinfoURL,
	)
}

// oauthRedirectURI returns the redirect URI to advertise to the provider: the
// configured override when set, otherwise the default callback under BaseURL.
func (a *App) oauthRedirectURI(r *http.Request, name, configured string) string {
	if configured != "" {
		return configured
	}
	return a.BaseURL(r) + "/api/auth/oauth/" + name + "/callback"
}

// handleOAuthStart begins the authorization-code flow: it generates CSRF state
// (and a PKCE verifier for OIDC), stores them in the zf_oauth cookie, and 302s
// the browser to the provider's authorize endpoint.
func (a *App) handleOAuthStart(w http.ResponseWriter, r *http.Request) {
	name := strings.ToLower(chi.URLParam(r, "provider"))

	prov, ok := a.oauthProvider(name)
	if !ok {
		a.Error(w, http.StatusNotFound, "unknown oauth provider")
		return
	}
	clientID, _, redirectCfg, _, _, _ := a.oauthCfgFor(name)
	if clientID == "" {
		a.Error(w, http.StatusNotFound, "oauth provider not configured")
		return
	}
	if prov.UsesPKCE && (prov.AuthorizeURL == "" || prov.TokenURL == "" || prov.UserinfoURL == "") {
		a.Error(w, http.StatusNotFound, "oauth provider not configured")
		return
	}

	state := auth.RandomString(32)
	redirectURI := a.oauthRedirectURI(r, name, redirectCfg)

	// Build the authorize URL query.
	q := url.Values{}
	q.Set("client_id", clientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("response_type", "code")
	q.Set("scope", prov.Scope)
	q.Set("state", state)

	var verifier string
	if prov.UsesPKCE {
		// PKCE: a high-entropy verifier (43-128 chars) and its S256 challenge.
		verifier = auth.RandomString(64)
		q.Set("code_challenge", oauthPKCEChallenge(verifier))
		q.Set("code_challenge_method", "S256")
	}

	a.oauthSetStateCookie(w, name, state, verifier)

	sep := "?"
	if strings.Contains(prov.AuthorizeURL, "?") {
		sep = "&"
	}
	http.Redirect(w, r, prov.AuthorizeURL+sep+q.Encode(), http.StatusFound)
}

// handleOAuthCallback completes the flow: validate state, exchange the code for
// an access token, fetch userinfo, then link to an existing account or (when
// permitted) provision a new one, and finally establish a session.
func (a *App) handleOAuthCallback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	name := strings.ToLower(chi.URLParam(r, "provider"))

	prov, ok := a.oauthProvider(name)
	if !ok {
		a.Error(w, http.StatusNotFound, "unknown oauth provider")
		return
	}
	clientID, clientSecret, redirectCfg, allowed, denied, _ := a.oauthCfgFor(name)
	if clientID == "" {
		a.Error(w, http.StatusNotFound, "oauth provider not configured")
		return
	}

	// Validate the CSRF state against the cookie, then clear the cookie so it
	// cannot be replayed regardless of how the rest of the handler proceeds.
	st, err := a.oauthReadStateCookie(r)
	a.oauthClearStateCookie(w)
	if err != nil || st.Provider != name {
		a.Error(w, http.StatusBadRequest, "invalid oauth state")
		return
	}
	queryState := r.URL.Query().Get("state")
	if queryState == "" || subtle.ConstantTimeCompare([]byte(queryState), []byte(st.State)) != 1 {
		a.Error(w, http.StatusBadRequest, "invalid oauth state")
		return
	}

	code := r.URL.Query().Get("code")
	if code == "" {
		a.Error(w, http.StatusBadRequest, "missing authorization code")
		return
	}

	redirectURI := a.oauthRedirectURI(r, name, redirectCfg)

	accessToken, err := a.oauthExchangeCode(ctx, prov, clientID, clientSecret, code, redirectURI, st.Verifier)
	if err != nil {
		a.Log.Warn("oauth token exchange failed", "provider", name, "error", err)
		a.Error(w, http.StatusBadGateway, "failed to exchange authorization code")
		return
	}

	info, err := a.oauthFetchUserinfo(ctx, prov, accessToken)
	if err != nil {
		a.Log.Warn("oauth userinfo failed", "provider", name, "error", err)
		a.Error(w, http.StatusBadGateway, "failed to fetch user information")
		return
	}

	identity, err := prov.ExtractIdentity(info)
	if err != nil {
		a.Log.Warn("oauth identity extraction failed", "provider", name, "error", err)
		a.Error(w, http.StatusBadGateway, "invalid user information from provider")
		return
	}

	// Discord supports server-side allow/deny lists on the provider id.
	if name == string(oauth.Discord) {
		if !oauthIDAllowed(identity.ID, allowed, denied) {
			a.Error(w, http.StatusForbidden, "your account is not permitted to sign in")
			return
		}
	}

	user, err := a.oauthResolveUser(ctx, prov, identity, accessToken)
	if err != nil {
		switch {
		case errors.Is(err, errOAuthRegistrationDisabled):
			a.Error(w, http.StatusForbidden, "oauth registration disabled")
		default:
			a.Log.Warn("oauth user resolution failed", "provider", name, "error", err)
			a.Error(w, http.StatusInternalServerError, "failed to sign in")
		}
		return
	}

	// Establish a session, matching the credential-login flow.
	s := a.Sessions.Get(r)
	s.UserID = user.ID
	s.SessionID = auth.RandomString(32)
	if err := a.Sessions.Save(w, s); err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to create session")
		return
	}
	// Best-effort session row; reuses the shared helper from auth_routes.go.
	if err := a.authInsertSession(r, s.SessionID, user.ID); err != nil {
		a.Log.Warn("failed to record user session", "error", err, "user", user.ID)
	}

	http.Redirect(w, r, "/dashboard", http.StatusFound)
}

// errOAuthRegistrationDisabled signals that no linked account exists and the
// instance has OAuth self-registration turned off.
var errOAuthRegistrationDisabled = errors.New("oauth: registration disabled")

// oauthResolveUser links the provider identity to a user: it returns the user
// behind an existing oauth_providers row, or (when registration is enabled)
// provisions a fresh user plus oauth_providers row.
func (a *App) oauthResolveUser(ctx context.Context, prov oauth.Provider, identity oauth.Identity, accessToken string) (*models.User, error) {
	var userID string
	err := a.Store.Pool.QueryRow(ctx,
		`SELECT user_id FROM oauth_providers WHERE provider=$1 AND oauth_id=$2`,
		string(prov.Type), identity.ID).Scan(&userID)
	switch {
	case err == nil:
		return a.Store.GetUserByID(ctx, userID)
	case errors.Is(err, pgx.ErrNoRows):
		// fall through to registration
	default:
		return nil, err
	}

	if !a.Cfg.Features.OAuthRegistration {
		return nil, errOAuthRegistrationDisabled
	}
	return a.oauthCreateUser(ctx, prov, identity, accessToken)
}

// oauthCreateUser provisions a new USER from a provider identity (deduping the
// username) and links it via an oauth_providers row, in a single transaction.
func (a *App) oauthCreateUser(ctx context.Context, prov oauth.Provider, identity oauth.Identity, accessToken string) (*models.User, error) {
	username, err := a.oauthUniqueUsername(ctx, identity.Username)
	if err != nil {
		return nil, err
	}

	user := &models.User{
		ID:       cuid.New(),
		Username: username,
		Token:    auth.CreateToken(),
		Role:     models.RoleUser,
		View:     models.UserViewEmbed{},
	}

	tx, err := a.Store.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx,
		`INSERT INTO users (id, created_at, updated_at, username, token, role, view)
		 VALUES ($1, now(), now(), $2, $3, $4, '{}'::jsonb)`,
		user.ID, user.Username, user.Token, user.Role); err != nil {
		return nil, err
	}

	oauthID := identity.ID
	if _, err := tx.Exec(ctx,
		`INSERT INTO oauth_providers (id, created_at, updated_at, user_id, provider, username, access_token, oauth_id)
		 VALUES ($1, now(), now(), $2, $3, $4, $5, $6)`,
		cuid.New(), user.ID, string(prov.Type), identity.Username, accessToken, oauthID); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return user, nil
}

// oauthUniqueUsername returns base unchanged if it is free, otherwise the first
// available "base1", "base2", ... It also sanitises an empty base to "user".
func (a *App) oauthUniqueUsername(ctx context.Context, base string) (string, error) {
	base = strings.TrimSpace(base)
	if base == "" {
		base = "user"
	}

	if free, err := a.oauthUsernameFree(ctx, base); err != nil {
		return "", err
	} else if free {
		return base, nil
	}

	for i := 1; i < 10000; i++ {
		candidate := fmt.Sprintf("%s%d", base, i)
		if free, err := a.oauthUsernameFree(ctx, candidate); err != nil {
			return "", err
		} else if free {
			return candidate, nil
		}
	}
	// Astronomically unlikely; fall back to a random suffix.
	return base + auth.RandomString(8), nil
}

// oauthUsernameFree reports whether no user currently has the given username.
func (a *App) oauthUsernameFree(ctx context.Context, username string) (bool, error) {
	var exists bool
	err := a.Store.Pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM users WHERE username=$1)`, username).Scan(&exists)
	if err != nil {
		return false, err
	}
	return !exists, nil
}

// oauthExchangeCode posts the authorization code to the provider's token endpoint
// and returns the access token. The request is form-encoded with Accept JSON;
// PKCE providers also send the code_verifier.
func (a *App) oauthExchangeCode(ctx context.Context, prov oauth.Provider, clientID, clientSecret, code, redirectURI, verifier string) (string, error) {
	form := url.Values{}
	form.Set("client_id", clientID)
	form.Set("client_secret", clientSecret)
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	if prov.UsesPKCE && verifier != "" {
		form.Set("code_verifier", verifier)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, prov.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := oauthHTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("token endpoint returned status %d", resp.StatusCode)
	}

	var tok struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		Error       string `json:"error"`
		ErrorDesc   string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &tok); err != nil {
		return "", fmt.Errorf("decode token response: %w", err)
	}
	if tok.Error != "" {
		return "", fmt.Errorf("token endpoint error: %s", tok.Error)
	}
	if tok.AccessToken == "" {
		return "", errors.New("token endpoint returned no access token")
	}
	return tok.AccessToken, nil
}

// oauthFetchUserinfo GETs the provider's userinfo endpoint with a Bearer token
// and returns the decoded JSON object.
func (a *App) oauthFetchUserinfo(ctx context.Context, prov oauth.Provider, accessToken string) (map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, prov.UserinfoURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	if prov.AcceptHeader != "" {
		req.Header.Set("Accept", prov.AcceptHeader)
	} else {
		req.Header.Set("Accept", "application/json")
	}

	resp, err := oauthHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("userinfo endpoint returned status %d", resp.StatusCode)
	}

	var info map[string]any
	if err := json.Unmarshal(body, &info); err != nil {
		return nil, fmt.Errorf("decode userinfo response: %w", err)
	}
	return info, nil
}

// --- state cookie helpers ---

// oauthSetStateCookie writes the zf_oauth cookie ("provider:state:verifier"),
// HttpOnly + SameSite=Lax, Secure when HTTPS URLs are configured.
func (a *App) oauthSetStateCookie(w http.ResponseWriter, provider, state, verifier string) {
	value := provider + ":" + state + ":" + verifier
	http.SetCookie(w, &http.Cookie{
		Name:     oauthStateCookieName,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   a.Cfg.Core.ReturnHTTPSURLs,
		MaxAge:   oauthStateCookieMaxAge,
	})
}

// oauthReadStateCookie parses the zf_oauth cookie into its three components.
func (a *App) oauthReadStateCookie(r *http.Request) (oauthState, error) {
	c, err := r.Cookie(oauthStateCookieName)
	if err != nil {
		return oauthState{}, err
	}
	// SplitN(3): a verifier is normally absent (empty) for non-PKCE providers.
	parts := strings.SplitN(c.Value, ":", 3)
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return oauthState{}, errors.New("malformed oauth state cookie")
	}
	st := oauthState{Provider: parts[0], State: parts[1]}
	if len(parts) == 3 {
		st.Verifier = parts[2]
	}
	return st, nil
}

// oauthClearStateCookie expires the zf_oauth cookie.
func (a *App) oauthClearStateCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     oauthStateCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   a.Cfg.Core.ReturnHTTPSURLs,
		MaxAge:   -1,
	})
}

// --- small helpers ---

// oauthPKCEChallenge returns the S256 PKCE code challenge for a verifier:
// base64url(sha256(verifier)) without padding (RFC 7636).
func oauthPKCEChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// oauthIDAllowed applies Discord-style allow/deny lists to a provider user id:
// denied always wins; if an allow list is present, the id must appear in it.
func oauthIDAllowed(id string, allowed, denied []string) bool {
	for _, d := range denied {
		if d == id {
			return false
		}
	}
	if len(allowed) > 0 {
		for _, a := range allowed {
			if a == id {
				return true
			}
		}
		return false
	}
	return true
}
