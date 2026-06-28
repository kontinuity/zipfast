package auth

import (
	"crypto/sha256"
	"net/http"

	"github.com/gorilla/securecookie"
)

// SessionCookieName is the name of the encrypted session cookie. It must remain
// "zipline_session" so that sessions issued by the original Zipline server (and
// vice versa) are recognised.
const SessionCookieName = "zipline_session"

// sessionMaxAge is the cookie lifetime: 14 days, matching Zipline.
const sessionMaxAge = 60 * 60 * 24 * 14

// Session is the decoded contents of the session cookie.
type Session struct {
	UserID       string
	SessionID    string
	PKCEVerifier string
	TokenAuth    bool
}

// SessionManager encodes and decodes session cookies using gorilla/securecookie
// (authenticated + encrypted). Keys are derived deterministically from the
// configured secret so all server instances share them.
type SessionManager struct {
	sc     *securecookie.SecureCookie
	secure bool // whether to set the Secure flag on cookies
}

// NewSessionManager builds a SessionManager. The 32-byte hash key (HMAC) and
// 32-byte block key (AES) are derived from secret via SHA-256 over distinct
// domain-separated inputs. secure controls the cookie's Secure attribute and
// should be true when the site is served over HTTPS.
func NewSessionManager(secret string, secure bool) *SessionManager {
	hashKey := sha256.Sum256([]byte(secret + "hash"))
	blockKey := sha256.Sum256([]byte(secret + "block"))
	return &SessionManager{
		sc:     securecookie.New(hashKey[:], blockKey[:]),
		secure: secure,
	}
}

// Get reads and decodes the session cookie from r. Any failure (missing cookie,
// decode/MAC error, expiry) yields the zero Session rather than an error, so
// callers can treat a zero Session as "not logged in".
func (m *SessionManager) Get(r *http.Request) Session {
	var s Session
	c, err := r.Cookie(SessionCookieName)
	if err != nil {
		return Session{}
	}
	if err := m.sc.Decode(SessionCookieName, c.Value, &s); err != nil {
		return Session{}
	}
	return s
}

// Save encodes s and writes it as the session cookie on w. The cookie is
// HttpOnly, Path "/", SameSite=Lax, Secure per the manager's configuration, and
// expires after 14 days.
func (m *SessionManager) Save(w http.ResponseWriter, s Session) error {
	encoded, err := m.sc.Encode(SessionCookieName, s)
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    encoded,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   m.secure,
		MaxAge:   sessionMaxAge,
	})
	return nil
}

// Clear removes the session cookie by writing an expired one (MaxAge -1), which
// instructs the browser to delete it immediately.
func (m *SessionManager) Clear(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   m.secure,
		MaxAge:   -1,
	})
}
