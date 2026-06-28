package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// accessTokenTTL is how long an access token stays valid. Zipline issues these
// for password-protected file/url access with a 5 minute lifetime.
const accessTokenTTL = 5 * time.Minute

// randomStringAlphabet matches Zipline's randomCharacters() alphabet: the 62
// URL-safe alphanumeric characters [A-Za-z0-9].
const randomStringAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

// b64 is the URL-safe, unpadded base64 encoding used for every token component,
// matching Zipline's use of base64url without padding.
var b64 = base64.RawURLEncoding

// RandomString returns a cryptographically random string of n characters drawn
// from [A-Za-z0-9]. It panics only if the system CSPRNG fails, which should not
// happen in practice.
func RandomString(n int) string {
	if n <= 0 {
		return ""
	}
	// Read n random bytes and map each into the alphabet. Using modulo over a
	// 62-character alphabet introduces a negligible bias that matches the
	// behaviour of the original implementation.
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		panic(fmt.Sprintf("auth: failed to read random bytes: %v", err))
	}
	out := make([]byte, n)
	for i, b := range buf {
		out[i] = randomStringAlphabet[int(b)%len(randomStringAlphabet)]
	}
	return string(out)
}

// nowMillis returns the current Unix time in milliseconds, the granularity
// Zipline encodes into tokens.
func nowMillis() int64 {
	return time.Now().UnixMilli()
}

// CreateToken builds an unencrypted API token in Zipline's format:
//
//	base64url(<unixMillis as decimal string>) + "." + base64url(RandomString(32))
//
// The timestamp half lets the server recover the issue time; the random half is
// the actual secret material.
func CreateToken() string {
	ts := strconv.FormatInt(nowMillis(), 10)
	return b64.EncodeToString([]byte(ts)) + "." + b64.EncodeToString([]byte(RandomString(32)))
}

// deriveKey derives a 32-byte AES-256 key from secret using SHA-256, matching
// Zipline's key derivation for token encryption.
func deriveKey(secret string) []byte {
	sum := sha256.Sum256([]byte(secret))
	return sum[:]
}

// newGCM constructs an AES-256-GCM AEAD from the secret-derived key.
func newGCM(secret string) (cipher.AEAD, error) {
	block, err := aes.NewCipher(deriveKey(secret))
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// EncryptToken encrypts an unencrypted token (such as one from CreateToken) with
// AES-256-GCM, returning:
//
//	base64url(<unixMillis as decimal string>) + "." + base64url(nonce || ciphertext)
//
// The key is sha256(secret). A fresh random nonce is generated per call and
// prepended to the ciphertext so DecryptToken can recover it.
func EncryptToken(token, secret string) (string, error) {
	gcm, err := newGCM(secret)
	if err != nil {
		return "", err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}

	// Seal appends the ciphertext (and auth tag) to its first argument; passing
	// nonce makes the output nonce || ciphertext in a single slice.
	sealed := gcm.Seal(nonce, nonce, []byte(token), nil)

	ts := strconv.FormatInt(nowMillis(), 10)
	return b64.EncodeToString([]byte(ts)) + "." + b64.EncodeToString(sealed), nil
}

// DecryptToken reverses EncryptToken, returning the original plaintext token. It
// validates the format, GCM nonce length, and authentication tag; any tampering
// or wrong secret yields an error.
func DecryptToken(enc, secret string) (string, error) {
	parts := strings.SplitN(enc, ".", 2)
	if len(parts) != 2 {
		return "", errors.New("auth: invalid token format")
	}

	// The timestamp segment is informational; we only need the payload half.
	sealed, err := b64.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("auth: invalid token payload: %w", err)
	}

	gcm, err := newGCM(secret)
	if err != nil {
		return "", err
	}

	ns := gcm.NonceSize()
	if len(sealed) < ns {
		return "", errors.New("auth: token payload too short")
	}
	nonce, ciphertext := sealed[:ns], sealed[ns:]

	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("auth: token decryption failed: %w", err)
	}
	return string(plain), nil
}

// accessTokenClaims is the JSON payload encrypted inside an access token. The
// field names match Zipline ("type", "id", "expiry") so tokens are mutually
// decryptable across both servers.
type accessTokenClaims struct {
	Type   string `json:"type"`
	ID     string `json:"id"`
	Expiry int64  `json:"expiry"` // Unix milliseconds
}

// CreateAccessToken issues a short-lived (5 minute) access token for the given
// resource type and id. The claims {type, id, expiry} are JSON-encoded and
// encrypted with AES-256-GCM under sha256(secret). The output is the raw
// base64url-encoded nonce||ciphertext (no timestamp prefix), matching how
// Zipline stores access tokens.
func CreateAccessToken(typ, id, secret string) (string, error) {
	claims := accessTokenClaims{
		Type:   typ,
		ID:     id,
		Expiry: nowMillis() + accessTokenTTL.Milliseconds(),
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}

	gcm, err := newGCM(secret)
	if err != nil {
		return "", err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}

	sealed := gcm.Seal(nonce, nonce, payload, nil)
	return b64.EncodeToString(sealed), nil
}

// VerifyAccessToken reports whether tok is a valid, unexpired access token for
// the given type and id. It returns false on any decode/decrypt error, claim
// mismatch, or expiry in the past, never panicking on malformed input.
func VerifyAccessToken(tok, typ, id, secret string) bool {
	sealed, err := b64.DecodeString(tok)
	if err != nil {
		return false
	}

	gcm, err := newGCM(secret)
	if err != nil {
		return false
	}

	ns := gcm.NonceSize()
	if len(sealed) < ns {
		return false
	}
	nonce, ciphertext := sealed[:ns], sealed[ns:]

	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return false
	}

	var claims accessTokenClaims
	if err := json.Unmarshal(plain, &claims); err != nil {
		return false
	}

	return claims.Type == typ && claims.ID == id && claims.Expiry > nowMillis()
}
