// Package auth implements Zipfast's authentication primitives: password hashing,
// API/access token creation and encryption, session cookies, and TOTP. The on-disk
// and on-the-wire formats deliberately match the original Zipline (TypeScript)
// server so that existing password hashes, API tokens, and sessions keep working
// after the rewrite.
package auth

import "github.com/alexedwards/argon2id"

// HashPassword hashes a plaintext password using argon2id with the library's
// default parameters. The result is a PHC-formatted string (e.g.
// "$argon2id$v=19$m=...,t=...,p=...$<salt>$<hash>"), the same format Zipline
// stored, so hashes produced here are interchangeable with the original.
func HashPassword(pw string) (string, error) {
	return argon2id.CreateHash(pw, argon2id.DefaultParams)
}

// VerifyPassword reports whether pw matches the given PHC-formatted argon2id
// hash. Because existing Zipline hashes are standard PHC strings, this verifies
// both legacy and newly created hashes. The parameters (memory, iterations,
// parallelism, salt) are read from the hash itself.
func VerifyPassword(hash, pw string) (bool, error) {
	return argon2id.ComparePasswordAndHash(pw, hash)
}
