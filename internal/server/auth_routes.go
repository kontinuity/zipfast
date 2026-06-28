package server

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/lucsky/cuid"

	"zipfast/internal/auth"
	"zipfast/internal/models"
)

// registerAuthRoutes mounts the credential-based authentication endpoints
// (login, logout, registration, and first-run setup). These mirror the original
// Zipline auth routes so the existing SPA and clients keep working.
func (a *App) registerAuthRoutes(r chi.Router) {
	r.Post("/api/auth/login", a.handleAuthLogin)
	// The client (and upstream Zipline) log out via GET; also accept POST.
	r.Get("/api/auth/logout", a.handleAuthLogout)
	r.Post("/api/auth/logout", a.handleAuthLogout)
	r.Post("/api/auth/register", a.handleAuthRegister)
	r.Post("/api/setup", a.handleAuthSetup)
}

// --- Zipline error shape ---
//
// The vendored React client parses non-OK JSON bodies via fetchApi/ApiError and
// reads `error` (the message) and `code` (a numeric Zipline error code, e.g.
// `ApiError.check(error, 1044)`). The shared App.Error helper emits a different
// shape ({ statusCode, message }), so the auth handlers emit Zipline-shaped
// errors directly. Messages mirror ApiError.toJSON()'s "E<code>: <message>"
// format and the codes/statuses match src/lib/api/errors.ts.

// authErrorMessages maps the Zipline error codes used by these endpoints to
// their human-readable messages (see src/lib/api/errors.ts: API_ERRORS).
var authErrorMessages = map[int]string{
	1000: "Invalid request schema",
	1035: "Invalid invite code",
	1036: "Invites aren't enabled",
	1037: "User registration is disabled",
	1039: "Username is taken",
	1044: "Invalid username or password",
	1045: "Invalid code",
	9001: "Forbidden",
	9004: "Internal server error",
}

// authCodeToStatus maps a Zipline error code to its HTTP status, mirroring
// ApiError.codeToHttpStatus (1xxx -> 400, plus the 9xxx overrides used here).
func authCodeToStatus(code int) int {
	switch code {
	case 9001:
		return http.StatusForbidden
	case 9004:
		return http.StatusInternalServerError
	}
	if code >= 1000 && code < 2000 {
		return http.StatusBadRequest
	}
	return http.StatusInternalServerError
}

// authError writes a Zipline-shaped error body the client's fetchApi/ApiError
// can parse: { error: "E<code>: <message>", code: <code>, statusCode: <status> }.
func (a *App) authError(w http.ResponseWriter, code int) {
	msg, ok := authErrorMessages[code]
	status := authCodeToStatus(code)
	formatted := msg
	if ok {
		formatted = "E" + strconv.Itoa(code) + ": " + msg
	}
	a.WriteJSON(w, status, map[string]any{
		"error":      formatted,
		"code":       code,
		"statusCode": status,
	})
}

// --- request payloads ---

// authCredsReq is the shared body for login/register/setup: a username, a
// password, and an optional TOTP/MFA code.
type authCredsReq struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Code     string `json:"code"`
}

// authParseCreds decodes and trims the credential body, returning false (after
// writing a 400) when the body is malformed or required fields are missing.
func (a *App) authParseCreds(w http.ResponseWriter, r *http.Request) (authCredsReq, bool) {
	var body authCredsReq
	if err := a.ReadJSON(r, &body); err != nil {
		a.authError(w, 1000)
		return body, false
	}
	body.Username = strings.TrimSpace(body.Username)
	body.Code = strings.TrimSpace(body.Code)
	if body.Username == "" || body.Password == "" {
		a.authError(w, 1000)
		return body, false
	}
	return body, true
}

// authClientDevice derives a best-effort client (browser) and device label from
// a User-Agent string. It is intentionally simple: enough for the sessions list
// without pulling in a UA-parsing dependency.
func authClientDevice(ua string) (client, device string) {
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

// authInsertSession records a tracked session row for the given user. Best-effort
// client/device are derived from the request's User-Agent.
func (a *App) authInsertSession(r *http.Request, sessionID, userID string) error {
	ua := r.UserAgent()
	client, device := authClientDevice(ua)
	_, err := a.Store.Pool.Exec(r.Context(),
		`INSERT INTO user_sessions (id, created_at, ua, client, device, user_id)
		 VALUES ($1, now(), $2, $3, $4, $5)`,
		sessionID, ua, client, device, userID)
	return err
}

// --- handlers ---

// handleAuthLogin verifies username/password (and TOTP when enabled for the
// user), establishes a session, records it, and returns the user.
func (a *App) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	log := a.logFor(r)
	body, ok := a.authParseCreds(w, r)
	if !ok {
		return
	}

	user, err := a.Store.GetUserByUsername(r.Context(), body.Username)
	if err != nil || user == nil || user.Password == nil || *user.Password == "" {
		log.Debug("login rejected", "reason", "unknown user or no password")
		a.authError(w, 1044)
		return
	}

	matched, err := auth.VerifyPassword(*user.Password, body.Password)
	if err != nil || !matched {
		log.Debug("login rejected", "reason", "invalid credentials")
		a.authError(w, 1044)
		return
	}

	// TOTP handling mirrors the original route: when the user has a secret and a
	// code was supplied, validate it (invalid -> 1045). When the user has a
	// secret but supplied no code, signal the client to open its TOTP modal by
	// returning { totp: true } (200) without creating a session.
	hasTotp := user.TotpSecret != nil && *user.TotpSecret != ""
	if hasTotp && body.Code != "" {
		if !auth.ValidateTOTP(*user.TotpSecret, body.Code) {
			log.Debug("login rejected", "reason", "invalid totp code", "userId", user.ID)
			a.authError(w, 1045)
			return
		}
		log.Debug("totp verified", "userId", user.ID)
	}
	if hasTotp && body.Code == "" {
		log.Debug("totp required", "userId", user.ID)
		a.WriteJSON(w, http.StatusOK, map[string]any{"totp": true})
		return
	}

	s := a.Sessions.Get(r)
	s.UserID = user.ID
	s.SessionID = auth.RandomString(32)
	if err := a.Sessions.Save(w, s); err != nil {
		a.authError(w, 9004)
		return
	}

	// Best-effort: a failure to record the session row should not block login.
	if err := a.authInsertSession(r, s.SessionID, user.ID); err != nil {
		a.Log.Warn("failed to record user session", "error", err, "user", user.ID)
	}

	log.Info("login", "userId", user.ID, "method", "password", "mfa", hasTotp)
	a.WriteJSON(w, http.StatusOK, map[string]any{"user": user})
}

// handleAuthLogout clears the session cookie and removes the tracked session row
// (when its id is known).
func (a *App) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	log := a.logFor(r)
	var userID string
	if a.Sessions != nil {
		s := a.Sessions.Get(r)
		userID = s.UserID
		if s.SessionID != "" {
			if _, err := a.Store.Pool.Exec(r.Context(),
				`DELETE FROM user_sessions WHERE id=$1`, s.SessionID); err != nil {
				a.Log.Warn("failed to delete user session", "error", err)
			}
		}
		a.Sessions.Clear(w)
	}
	log.Info("logout", "userId", userID)
	// The client's ApiLogoutResponse is { loggedOut?: boolean }.
	a.WriteJSON(w, http.StatusOK, map[string]any{"loggedOut": true})
}

// handleAuthRegister creates a USER account when public registration is enabled
// or a valid invite code is supplied. A consumed invite has its use count bumped.
func (a *App) handleAuthRegister(w http.ResponseWriter, r *http.Request) {
	log := a.logFor(r)
	body, ok := a.authParseCreds(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	code := strings.TrimSpace(body.Code)

	// Permission checks mirror the original route's ApiError codes:
	//   code present but invites disabled       -> 1036
	//   no code and user registration disabled  -> 1037
	if code != "" && !a.Cfg.Invites.Enabled {
		a.authError(w, 1036)
		return
	}
	if code == "" && !a.Cfg.Features.UserRegistration {
		a.authError(w, 1037)
		return
	}

	// Reject duplicate usernames up front (a unique constraint also guards this).
	if existing, err := a.Store.GetUserByUsername(ctx, body.Username); err == nil && existing != nil {
		a.authError(w, 1039)
		return
	}

	// Validate the invite (if any) after the duplicate check, matching the
	// original ordering. Any missing/expired/exhausted invite -> 1035.
	var inviteID string
	if code != "" {
		id, err := a.authConsumableInvite(ctx, code)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				a.authError(w, 1035)
				return
			}
			a.authError(w, 9004)
			return
		}
		inviteID = id
	}

	hashed, err := auth.HashPassword(body.Password)
	if err != nil {
		a.authError(w, 9004)
		return
	}

	user := &models.User{
		ID:       cuid.New(),
		Username: body.Username,
		Password: &hashed,
		Token:    auth.CreateToken(),
		Role:     models.RoleUser,
		View:     models.UserViewEmbed{},
	}
	if err := a.Store.CreateUser(ctx, user); err != nil {
		if authIsUniqueViolation(err) {
			a.authError(w, 1039)
			return
		}
		a.authError(w, 9004)
		return
	}

	// Consume the invite only after the user is created. Best-effort: the account
	// already exists, so a failed increment should not fail the request.
	if inviteID != "" {
		if _, err := a.Store.Pool.Exec(ctx,
			`UPDATE invites SET uses = uses + 1, updated_at = now() WHERE id=$1`, inviteID); err != nil {
			a.Log.Warn("failed to increment invite uses", "error", err, "invite", inviteID)
		}
	}

	log.Info("user registered", "userId", user.ID, "role", user.Role, "viaInvite", inviteID != "")
	a.WriteJSON(w, http.StatusOK, map[string]any{"user": user})
}

// handleAuthSetup creates the first administrator. It is only valid on a fresh
// install (before the settings row has been marked as set up).
func (a *App) handleAuthSetup(w http.ResponseWriter, r *http.Request) {
	log := a.logFor(r)
	body, ok := a.authParseCreds(w, r)
	if !ok {
		return
	}
	ctx := r.Context()

	data, firstSetup, err := a.Store.LoadSettings(ctx)
	if err != nil {
		a.authError(w, 9004)
		return
	}
	// The original /api/setup route rejects with ApiError(9001) once setup is done.
	if !firstSetup {
		a.authError(w, 9001)
		return
	}

	if existing, err := a.Store.GetUserByUsername(ctx, body.Username); err == nil && existing != nil {
		a.authError(w, 1039)
		return
	}

	hashed, err := auth.HashPassword(body.Password)
	if err != nil {
		a.authError(w, 9004)
		return
	}

	user := &models.User{
		ID:       cuid.New(),
		Username: body.Username,
		Password: &hashed,
		Token:    auth.CreateToken(),
		Role:     models.RoleSuperAdmin,
		View:     models.UserViewEmbed{},
	}
	if err := a.Store.CreateUser(ctx, user); err != nil {
		if authIsUniqueViolation(err) {
			a.authError(w, 1039)
			return
		}
		a.authError(w, 9004)
		return
	}

	// Mark the install as set up so this endpoint can't be used again.
	if err := a.Store.SaveSettings(ctx, data, false); err != nil {
		a.authError(w, 9004)
		return
	}

	log.Info("first-run setup complete", "userId", user.ID, "role", user.Role)
	// Match the original /api/setup response: { firstSetup, user }, where
	// firstSetup is the pre-flip value (always true here, since we returned 9001
	// above otherwise). The client (setup.tsx) then logs in via /api/auth/login.
	a.WriteJSON(w, http.StatusOK, map[string]any{"firstSetup": true, "user": user})
}

// authConsumableInvite looks up an invite by code that still has uses remaining
// (uses < max_uses, or max_uses is NULL for unlimited) and is not expired. It
// returns the invite id, or pgx.ErrNoRows when no usable invite matches.
func (a *App) authConsumableInvite(ctx context.Context, code string) (string, error) {
	var id string
	err := a.Store.Pool.QueryRow(ctx,
		`SELECT id FROM invites
		 WHERE code = $1
		   AND (max_uses IS NULL OR uses < max_uses)
		   AND (expires_at IS NULL OR expires_at > $2)
		 LIMIT 1`,
		code, time.Now()).Scan(&id)
	return id, err
}

// authIsUniqueViolation reports whether err is a PostgreSQL unique-constraint
// violation (SQLSTATE 23505), used to map a racing duplicate insert to a 409.
func authIsUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505"
	}
	return false
}
