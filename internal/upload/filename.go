package upload

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"zipfast/internal/config"
)

// nowFunc is the time source used by the "date" format. It is a package
// variable so tests can override it; production code always uses time.Now.
var nowFunc = time.Now

// Known file-name formats accepted by FormatFileName and the x-zipline-format
// header. Unknown formats fall back to FormatRandom.
const (
	FormatRandom      = "random"
	FormatUUID        = "uuid"
	FormatDate        = "date"
	FormatName        = "name"
	FormatGfycat      = "gfycat"
	FormatRandomWords = "random-words"
)

// alphanumeric is the character set used by RandomString: [A-Za-z0-9].
const alphanumeric = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

// KnownFormats lists every supported file-name format. It is used both as a set
// for validation (see ValidFormat) and to document the accepted values.
var KnownFormats = []string{
	FormatRandom,
	FormatUUID,
	FormatDate,
	FormatName,
	FormatGfycat,
	FormatRandomWords,
}

// ValidFormat reports whether s is one of the known file-name formats.
func ValidFormat(s string) bool {
	for _, f := range KnownFormats {
		if s == f {
			return true
		}
	}
	return false
}

// RandomString returns a cryptographically-random string of length n drawn from
// the alphanumeric alphabet [A-Za-z0-9]. A non-positive n yields the empty
// string.
func RandomString(n int) string {
	if n <= 0 {
		return ""
	}
	b := make([]byte, n)
	max := big.NewInt(int64(len(alphanumeric)))
	for i := range b {
		idx, err := rand.Int(rand.Reader, max)
		if err != nil {
			// crypto/rand failing is effectively fatal for the process, but we
			// avoid panicking in a pure helper: fall back to the first rune.
			b[i] = alphanumeric[0]
			continue
		}
		b[i] = alphanumeric[idx.Int64()]
	}
	return string(b)
}

// randomUUID returns an RFC 4122 version-4 UUID string generated from
// crypto/rand. We implement it directly to avoid pulling in an external uuid
// dependency.
func randomUUID() (string, error) {
	var u [16]byte
	if _, err := rand.Read(u[:]); err != nil {
		return "", fmt.Errorf("generate uuid: %w", err)
	}
	// Set the version (4) and variant (RFC 4122) bits.
	u[6] = (u[6] & 0x0f) | 0x40
	u[8] = (u[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", u[0:4], u[4:6], u[6:8], u[8:10], u[10:16]), nil
}

// randomWords builds a gfycat-style name by joining numAdjectives randomly
// chosen adjectives followed by a single random noun, joined with sep. If
// numAdjectives is non-positive it defaults to 1.
func randomWords(numAdjectives int, sep string) string {
	if numAdjectives < 0 {
		numAdjectives = 0
	}

	parts := make([]string, 0, numAdjectives+1)
	for i := 0; i < numAdjectives; i++ {
		parts = append(parts, pick(adjectives))
	}
	parts = append(parts, pick(nouns))
	return strings.Join(parts, sep)
}

// pick returns a cryptographically-random element of words. It assumes words is
// non-empty (both built-in lists are).
func pick(words []string) string {
	if len(words) == 0 {
		return ""
	}
	idx, err := rand.Int(rand.Reader, big.NewInt(int64(len(words))))
	if err != nil {
		return words[0]
	}
	return words[idx.Int64()]
}

// SanitizeFilename returns name with path separators and control characters
// removed and runs of whitespace collapsed to a single space. It never returns
// a path component: directory separators (both / and \) are stripped, so the
// result is always a single safe segment. Leading/trailing dots and spaces are
// trimmed to avoid hidden or empty names.
func SanitizeFilename(name string) string {
	// Drop any directory component first (handles both separators).
	name = strings.ReplaceAll(name, "\\", "/")
	if idx := strings.LastIndex(name, "/"); idx >= 0 {
		name = name[idx+1:]
	}

	var b strings.Builder
	b.Grow(len(name))
	lastWasSpace := false
	for _, r := range name {
		switch {
		case r == '/' || r == '\\':
			// Path separators are never allowed in a single segment.
			continue
		case unicode.IsControl(r):
			// Strip control characters (including NUL, newlines, etc.).
			continue
		case unicode.IsSpace(r):
			// Collapse any run of whitespace into a single ASCII space.
			if !lastWasSpace {
				b.WriteRune(' ')
				lastWasSpace = true
			}
		default:
			b.WriteRune(r)
			lastWasSpace = false
		}
	}

	// Trim surrounding spaces and dots (avoids "", ".", ".." and hidden names).
	return strings.Trim(b.String(), " .")
}

// FormatFileName produces the output file name (without extension) for an upload
// according to the requested format. originalName is the client-supplied name,
// used only by the "name" format. Unknown formats fall back to "random".
//
// The supported formats are:
//
//	"random"                  -> RandomString(files.Length)
//	"uuid"                    -> a v4 UUID string
//	"date"                    -> time.Now().Format(files.DefaultDateFormat)
//	"name"                    -> sanitized base name of originalName, no extension
//	"gfycat" / "random-words" -> N adjectives + 1 noun joined by the separator
func FormatFileName(format, originalName string, files config.Files) (string, error) {
	switch format {
	case FormatUUID:
		return randomUUID()

	case FormatDate:
		return nowFunc().Format(files.DefaultDateFormat), nil

	case FormatName:
		// Strip the extension, then sanitize what remains.
		base := filepath.Base(originalName)
		ext := filepath.Ext(base)
		name := SanitizeFilename(strings.TrimSuffix(base, ext))
		if name == "" {
			// Fall back to a random name if the original sanitizes to nothing.
			return RandomString(files.Length), nil
		}
		return name, nil

	case FormatGfycat, FormatRandomWords:
		return randomWords(files.RandomWordsNumAdjectives, files.RandomWordsSeparator), nil

	case FormatRandom:
		return RandomString(files.Length), nil

	default:
		// Unknown formats degrade gracefully to "random".
		return RandomString(files.Length), nil
	}
}
