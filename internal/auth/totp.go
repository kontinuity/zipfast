package auth

import "github.com/pquerna/otp/totp"

// GenerateTOTP creates a new TOTP secret for the given issuer and account
// (typically the username or email). It returns the base32 secret to persist for
// the user and the otpauth:// URL used to render a provisioning QR code. Defaults
// (SHA1, 6 digits, 30s period) match standard authenticator apps and Zipline.
func GenerateTOTP(issuer, account string) (secret string, otpauthURL string, err error) {
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      issuer,
		AccountName: account,
	})
	if err != nil {
		return "", "", err
	}
	return key.Secret(), key.URL(), nil
}

// ValidateTOTP reports whether code is a currently valid TOTP for secret. It
// returns false for malformed or expired codes.
func ValidateTOTP(secret, code string) bool {
	return totp.Validate(code, secret)
}
