package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/lucsky/cuid"

	"zipfast/internal/auth"
	"zipfast/internal/models"
)

// registerMfaRoutes mounts the multi-factor authentication endpoints: TOTP
// enable/disable and WebAuthn passkey registration/login/management. TOTP and
// passkey-management routes require an authenticated user; the WebAuthn login
// endpoints are public (they are how a user proves a second/primary factor).
//
// All package-level identifiers added by this file are prefixed with "mfa" to
// avoid collisions with the rest of the (concurrently edited) server package.
func (a *App) registerMfaRoutes(r chi.Router) {
	// Authenticated: TOTP + passkey management/registration.
	r.Group(func(r chi.Router) {
		r.Use(a.RequireUser)

		// TOTP.
		r.Get("/api/user/mfa/totp", a.mfaTotpGenerate)
		r.Post("/api/user/mfa/totp", a.mfaTotpEnable)
		r.Delete("/api/user/mfa/totp", a.mfaTotpDisable)

		// Passkey management.
		r.Get("/api/user/mfa/passkey", a.mfaPasskeyList)
		r.Delete("/api/user/mfa/passkey/{id}", a.mfaPasskeyDelete)

		// Passkey registration ceremony.
		r.Post("/api/user/mfa/passkey/register/begin", a.mfaPasskeyRegisterBegin)
		r.Post("/api/user/mfa/passkey/register/finish", a.mfaPasskeyRegisterFinish)
	})

	// Public: WebAuthn login ceremony.
	r.Post("/api/auth/webauthn/begin", a.mfaWebAuthnLoginBegin)
	r.Post("/api/auth/webauthn/finish", a.mfaWebAuthnLoginFinish)
}

// --- TOTP ---

// mfaTotpGenerate returns a fresh TOTP secret and otpauth URL. The secret is NOT
// persisted; the client must confirm a valid code via mfaTotpEnable to store it.
func (a *App) mfaTotpGenerate(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	if user == nil {
		a.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	secret, otpauthURL, err := auth.GenerateTOTP(a.Cfg.MFA.TotpIssuer, user.Username)
	if err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to generate totp secret")
		return
	}
	a.WriteJSON(w, http.StatusOK, map[string]any{
		"secret":     secret,
		"otpauthUrl": otpauthURL,
	})
}

// mfaTotpEnableReq is the body for confirming and persisting a TOTP secret.
type mfaTotpEnableReq struct {
	Secret string `json:"secret"`
	Code   string `json:"code"`
}

// mfaTotpEnable validates the supplied code against the supplied secret and, on
// success, persists the secret on the user (enabling TOTP).
func (a *App) mfaTotpEnable(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	if user == nil {
		a.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var body mfaTotpEnableReq
	if err := a.ReadJSON(r, &body); err != nil {
		a.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	body.Secret = strings.TrimSpace(body.Secret)
	body.Code = strings.TrimSpace(body.Code)
	if body.Secret == "" {
		a.Error(w, http.StatusBadRequest, "secret is required")
		return
	}

	if !auth.ValidateTOTP(body.Secret, body.Code) {
		a.Error(w, http.StatusBadRequest, "invalid code")
		return
	}

	if _, err := a.Store.Pool.Exec(r.Context(),
		`UPDATE users SET totp_secret=$1, updated_at=now() WHERE id=$2`,
		body.Secret, user.ID); err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to enable totp")
		return
	}

	a.WriteJSON(w, http.StatusOK, map[string]any{"enabled": true})
}

// mfaTotpCodeReq is the body for operations that require a current TOTP code.
type mfaTotpCodeReq struct {
	Code string `json:"code"`
}

// mfaTotpDisable requires a currently valid code (against the stored secret) and
// then clears the secret, disabling TOTP for the user.
func (a *App) mfaTotpDisable(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	if user == nil {
		a.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if user.TotpSecret == nil || *user.TotpSecret == "" {
		a.Error(w, http.StatusBadRequest, "totp is not enabled")
		return
	}

	var body mfaTotpCodeReq
	if err := a.ReadJSON(r, &body); err != nil {
		a.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	body.Code = strings.TrimSpace(body.Code)

	if !auth.ValidateTOTP(*user.TotpSecret, body.Code) {
		a.Error(w, http.StatusBadRequest, "invalid code")
		return
	}

	if _, err := a.Store.Pool.Exec(r.Context(),
		`UPDATE users SET totp_secret=NULL, updated_at=now() WHERE id=$1`,
		user.ID); err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to disable totp")
		return
	}

	a.WriteJSON(w, http.StatusOK, map[string]any{"enabled": false})
}

// --- WebAuthn: relying party + user adapter ---

// mfaWebAuthn builds a WebAuthn relying party from configuration, falling back to
// the request's hostname/origin when explicit values are not configured. It is
// constructed per request (cheap) so it always reflects the host actually serving
// the ceremony, which matters for setups without CORE_DEFAULT_DOMAIN.
func (a *App) mfaWebAuthn(r *http.Request) (*webauthn.WebAuthn, error) {
	rpID := a.Cfg.MFA.PasskeysRPID
	if rpID == "" {
		// Derive the effective domain (host without port) from the request.
		host := r.Host
		if h, _, err := splitHostPortMFA(host); err == nil && h != "" {
			host = h
		}
		rpID = host
	}

	origin := a.Cfg.MFA.PasskeysOrigin
	if origin == "" {
		origin = a.BaseURL(r)
	}

	return webauthn.New(&webauthn.Config{
		RPID:          rpID,
		RPDisplayName: "Zipfast",
		RPOrigins:     []string{origin},
	})
}

// splitHostPortMFA splits a "host:port" or bare "host" into its host component.
// It tolerates the missing-port case (returning the input as the host) so it can
// be used directly on r.Host.
func splitHostPortMFA(hostport string) (host, port string, err error) {
	if hostport == "" {
		return "", "", errors.New("empty host")
	}
	if !strings.Contains(hostport, ":") {
		return hostport, "", nil
	}
	// IPv6 literals are wrapped in brackets; URL parsing handles both forms.
	u := url.URL{Host: hostport}
	return u.Hostname(), u.Port(), nil
}

// mfaWebAuthnUser adapts a models.User (plus its stored passkey credentials) to
// the webauthn.User interface required by go-webauthn.
type mfaWebAuthnUser struct {
	user        *models.User
	credentials []webauthn.Credential
}

// mfaLoadWebAuthnUser builds a mfaWebAuthnUser, loading and unmarshaling the
// user's stored credentials from user_passkeys.
func (a *App) mfaLoadWebAuthnUser(ctx context.Context, user *models.User) (*mfaWebAuthnUser, error) {
	creds, err := a.mfaLoadCredentials(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	return &mfaWebAuthnUser{user: user, credentials: creds}, nil
}

// mfaLoadCredentials reads every passkey for a user and unmarshals the reg JSONB
// blobs into webauthn.Credential values.
func (a *App) mfaLoadCredentials(ctx context.Context, userID string) ([]webauthn.Credential, error) {
	rows, err := a.Store.Pool.Query(ctx,
		`SELECT reg FROM user_passkeys WHERE user_id=$1`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var creds []webauthn.Credential
	for rows.Next() {
		var reg []byte
		if err := rows.Scan(&reg); err != nil {
			return nil, err
		}
		var c webauthn.Credential
		if err := json.Unmarshal(reg, &c); err != nil {
			// Skip credentials that fail to decode rather than failing the whole
			// ceremony; a single corrupt row should not lock the user out.
			continue
		}
		creds = append(creds, c)
	}
	return creds, rows.Err()
}

// WebAuthnID returns the user handle (the user's cuid as raw bytes).
func (u *mfaWebAuthnUser) WebAuthnID() []byte { return []byte(u.user.ID) }

// WebAuthnName returns the human-palatable account name (the username).
func (u *mfaWebAuthnUser) WebAuthnName() string { return u.user.Username }

// WebAuthnDisplayName returns the display name (the username).
func (u *mfaWebAuthnUser) WebAuthnDisplayName() string { return u.user.Username }

// WebAuthnCredentials returns the user's registered credentials.
func (u *mfaWebAuthnUser) WebAuthnCredentials() []webauthn.Credential { return u.credentials }

// WebAuthnIcon returns an empty icon URL (deprecated in the spec; unused here).
func (u *mfaWebAuthnUser) WebAuthnIcon() string { return "" }

// --- WebAuthn: ceremony session storage (cookies) ---

const (
	mfaRegSessionCookie   = "zf_pk_reg"
	mfaLoginSessionCookie = "zf_pk_login"
	mfaSessionMaxAge      = 5 * 60 // 5 minutes, ample for a ceremony
)

// mfaSaveSession serializes WebAuthn SessionData to a short-lived HttpOnly cookie.
func (a *App) mfaSaveSession(w http.ResponseWriter, r *http.Request, name string, data *webauthn.SessionData) error {
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    base64URLEncodeMFA(raw),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   a.Scheme(r) == "https",
		MaxAge:   mfaSessionMaxAge,
	})
	return nil
}

// mfaLoadSession reads and decodes WebAuthn SessionData from the named cookie.
func (a *App) mfaLoadSession(r *http.Request, name string) (*webauthn.SessionData, error) {
	c, err := r.Cookie(name)
	if err != nil {
		return nil, err
	}
	raw, err := base64URLDecodeMFA(c.Value)
	if err != nil {
		return nil, err
	}
	var data webauthn.SessionData
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, err
	}
	return &data, nil
}

// mfaClearSession expires the named ceremony cookie.
func (a *App) mfaClearSession(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// --- WebAuthn: registration ---

// mfaPasskeyRegisterBegin starts a registration ceremony for the authenticated
// user, returning the credential-creation options and stashing the matching
// SessionData in an HttpOnly cookie for the finish step.
func (a *App) mfaPasskeyRegisterBegin(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	if user == nil {
		a.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	wa, err := a.mfaWebAuthn(r)
	if err != nil {
		a.Error(w, http.StatusInternalServerError, "webauthn not configured")
		return
	}

	waUser, err := a.mfaLoadWebAuthnUser(r.Context(), user)
	if err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to load credentials")
		return
	}

	creation, sessionData, err := wa.BeginRegistration(waUser)
	if err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to begin registration")
		return
	}

	if err := a.mfaSaveSession(w, r, mfaRegSessionCookie, sessionData); err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to persist ceremony")
		return
	}

	a.WriteJSON(w, http.StatusOK, creation)
}

// mfaPasskeyRegisterFinish completes a registration ceremony, persisting the new
// credential under the supplied name (from the "name" query parameter).
func (a *App) mfaPasskeyRegisterFinish(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	if user == nil {
		a.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	sessionData, err := a.mfaLoadSession(r, mfaRegSessionCookie)
	if err != nil {
		a.Error(w, http.StatusBadRequest, "registration ceremony not found or expired")
		return
	}
	defer a.mfaClearSession(w, mfaRegSessionCookie)

	wa, err := a.mfaWebAuthn(r)
	if err != nil {
		a.Error(w, http.StatusInternalServerError, "webauthn not configured")
		return
	}

	waUser, err := a.mfaLoadWebAuthnUser(r.Context(), user)
	if err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to load credentials")
		return
	}

	credential, err := wa.FinishRegistration(waUser, *sessionData, r)
	if err != nil {
		a.Error(w, http.StatusBadRequest, "failed to verify registration")
		return
	}

	regJSON, err := json.Marshal(credential)
	if err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to encode credential")
		return
	}

	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if name == "" {
		name = "Passkey"
	}

	if _, err := a.Store.Pool.Exec(r.Context(),
		`INSERT INTO user_passkeys (id, created_at, updated_at, name, reg, user_id)
		 VALUES ($1, now(), now(), $2, $3, $4)`,
		cuid.New(), name, regJSON, user.ID); err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to save passkey")
		return
	}

	a.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// --- WebAuthn: login ---

// mfaUsernameReq is the body for the public WebAuthn login endpoints.
type mfaUsernameReq struct {
	Username string `json:"username"`
}

// mfaWebAuthnLoginBegin starts a login ceremony for the named user, returning the
// assertion options and stashing the SessionData in an HttpOnly cookie.
func (a *App) mfaWebAuthnLoginBegin(w http.ResponseWriter, r *http.Request) {
	var body mfaUsernameReq
	if err := a.ReadJSON(r, &body); err != nil {
		a.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	body.Username = strings.TrimSpace(body.Username)
	if body.Username == "" {
		a.Error(w, http.StatusBadRequest, "username is required")
		return
	}

	user, err := a.Store.GetUserByUsername(r.Context(), body.Username)
	if err != nil || user == nil {
		a.Error(w, http.StatusUnauthorized, "invalid username")
		return
	}

	wa, err := a.mfaWebAuthn(r)
	if err != nil {
		a.Error(w, http.StatusInternalServerError, "webauthn not configured")
		return
	}

	waUser, err := a.mfaLoadWebAuthnUser(r.Context(), user)
	if err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to load credentials")
		return
	}
	if len(waUser.credentials) == 0 {
		a.Error(w, http.StatusBadRequest, "no passkeys registered for this user")
		return
	}

	assertion, sessionData, err := wa.BeginLogin(waUser)
	if err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to begin login")
		return
	}

	if err := a.mfaSaveSession(w, r, mfaLoginSessionCookie, sessionData); err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to persist ceremony")
		return
	}

	a.WriteJSON(w, http.StatusOK, assertion)
}

// mfaWebAuthnLoginFinish completes a login ceremony. On success it updates the
// credential's last_used timestamp, establishes a session (matching the
// password-login flow), records the session row, and returns the user.
func (a *App) mfaWebAuthnLoginFinish(w http.ResponseWriter, r *http.Request) {
	var body mfaUsernameReq
	if err := a.ReadJSON(r, &body); err != nil {
		a.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	body.Username = strings.TrimSpace(body.Username)
	if body.Username == "" {
		a.Error(w, http.StatusBadRequest, "username is required")
		return
	}

	sessionData, err := a.mfaLoadSession(r, mfaLoginSessionCookie)
	if err != nil {
		a.Error(w, http.StatusBadRequest, "login ceremony not found or expired")
		return
	}
	defer a.mfaClearSession(w, mfaLoginSessionCookie)

	user, err := a.Store.GetUserByUsername(r.Context(), body.Username)
	if err != nil || user == nil {
		a.Error(w, http.StatusUnauthorized, "invalid username")
		return
	}

	wa, err := a.mfaWebAuthn(r)
	if err != nil {
		a.Error(w, http.StatusInternalServerError, "webauthn not configured")
		return
	}

	waUser, err := a.mfaLoadWebAuthnUser(r.Context(), user)
	if err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to load credentials")
		return
	}

	credential, err := wa.FinishLogin(waUser, *sessionData, r)
	if err != nil {
		a.Error(w, http.StatusUnauthorized, "failed to verify login")
		return
	}

	// Best-effort: bump last_used for the credential that was used. The stored
	// reg blob's "id" is the base64url-encoded credential ID.
	if err := a.mfaTouchCredential(r.Context(), user.ID, credential); err != nil {
		a.Log.Warn("failed to update passkey last_used", "error", err, "user", user.ID)
	}

	// Establish a session, mirroring the password-login flow.
	s := a.Sessions.Get(r)
	s.UserID = user.ID
	s.SessionID = auth.RandomString(32)
	if err := a.Sessions.Save(w, s); err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to create session")
		return
	}

	if err := a.mfaInsertSession(r, s.SessionID, user.ID); err != nil {
		a.Log.Warn("failed to record user session", "error", err, "user", user.ID)
	}

	a.WriteJSON(w, http.StatusOK, map[string]any{"user": user})
}

// mfaTouchCredential updates the last_used timestamp of the passkey whose stored
// credential matches the one just used in a login ceremony. It matches on the
// credential ID encoded inside the reg JSONB blob.
func (a *App) mfaTouchCredential(ctx context.Context, userID string, credential *webauthn.Credential) error {
	if credential == nil {
		return nil
	}
	credID := base64URLEncodeMFA(credential.ID)
	_, err := a.Store.Pool.Exec(ctx,
		`UPDATE user_passkeys SET last_used=now(), updated_at=now()
		 WHERE user_id=$1 AND reg->>'id' = $2`,
		userID, credID)
	return err
}

// mfaInsertSession records a tracked session row, deriving best-effort
// client/device labels from the request's User-Agent. It is self-contained to
// avoid depending on helpers defined in concurrently edited files.
func (a *App) mfaInsertSession(r *http.Request, sessionID, userID string) error {
	ua := r.UserAgent()
	client, device := mfaClientDevice(ua)
	_, err := a.Store.Pool.Exec(r.Context(),
		`INSERT INTO user_sessions (id, created_at, ua, client, device, user_id)
		 VALUES ($1, now(), $2, $3, $4, $5)`,
		sessionID, ua, client, device, userID)
	return err
}

// mfaClientDevice derives a coarse client (browser) and device label from a
// User-Agent string for the sessions list.
func mfaClientDevice(ua string) (client, device string) {
	l := strings.ToLower(ua)

	switch {
	case strings.Contains(l, "edg/") || strings.Contains(l, "edge"):
		client = "Edge"
	case strings.Contains(l, "opr/") || strings.Contains(l, "opera"):
		client = "Opera"
	case strings.Contains(l, "firefox"):
		client = "Firefox"
	case strings.Contains(l, "chrome") || strings.Contains(l, "chromium"):
		client = "Chrome"
	case strings.Contains(l, "safari"):
		client = "Safari"
	case strings.Contains(l, "curl"):
		client = "curl"
	default:
		client = "Unknown"
	}

	switch {
	case strings.Contains(l, "android"):
		device = "Android"
	case strings.Contains(l, "iphone"):
		device = "iPhone"
	case strings.Contains(l, "ipad"):
		device = "iPad"
	case strings.Contains(l, "windows"):
		device = "Windows"
	case strings.Contains(l, "mac os") || strings.Contains(l, "macintosh"):
		device = "macOS"
	case strings.Contains(l, "linux"):
		device = "Linux"
	default:
		device = "Unknown"
	}
	return client, device
}

// --- WebAuthn: passkey management ---

// mfaPasskeyInfo is the public shape of a stored passkey (the reg blob is never
// exposed).
type mfaPasskeyInfo struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	CreatedAt time.Time  `json:"createdAt"`
	LastUsed  *time.Time `json:"lastUsed,omitempty"`
}

// mfaPasskeyList returns the authenticated user's registered passkeys.
func (a *App) mfaPasskeyList(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	if user == nil {
		a.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	rows, err := a.Store.Pool.Query(r.Context(),
		`SELECT id, name, created_at, last_used FROM user_passkeys
		 WHERE user_id=$1 ORDER BY created_at DESC`, user.ID)
	if err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to list passkeys")
		return
	}
	defer rows.Close()

	passkeys := []mfaPasskeyInfo{}
	for rows.Next() {
		var p mfaPasskeyInfo
		if err := rows.Scan(&p.ID, &p.Name, &p.CreatedAt, &p.LastUsed); err != nil {
			a.Error(w, http.StatusInternalServerError, "failed to read passkeys")
			return
		}
		passkeys = append(passkeys, p)
	}
	if err := rows.Err(); err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to read passkeys")
		return
	}

	a.WriteJSON(w, http.StatusOK, map[string]any{"passkeys": passkeys})
}

// mfaPasskeyDelete removes one of the authenticated user's passkeys by id. The
// delete is scoped to the user so a caller cannot remove another user's key.
func (a *App) mfaPasskeyDelete(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	if user == nil {
		a.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	id := chi.URLParam(r, "id")
	if id == "" {
		a.Error(w, http.StatusBadRequest, "id is required")
		return
	}

	tag, err := a.Store.Pool.Exec(r.Context(),
		`DELETE FROM user_passkeys WHERE id=$1 AND user_id=$2`, id, user.ID)
	if err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to delete passkey")
		return
	}
	if tag.RowsAffected() == 0 {
		a.Error(w, http.StatusNotFound, "passkey not found")
		return
	}

	a.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// --- small encoding helpers (mfa-prefixed to avoid collisions) ---

// mfaB64 is URL-safe base64 without padding, matching the encoding go-webauthn
// uses for credential IDs in its JSON output.
var mfaB64 = base64.RawURLEncoding

// base64URLEncodeMFA encodes bytes using URL-safe base64 without padding.
func base64URLEncodeMFA(b []byte) string {
	return mfaB64.EncodeToString(b)
}

// base64URLDecodeMFA reverses base64URLEncodeMFA.
func base64URLDecodeMFA(s string) ([]byte, error) {
	return mfaB64.DecodeString(s)
}
