// Package parser ports Zipline's embed/template string parser.
//
// Zipline lets administrators write template strings (for embeds, webhooks,
// notifications, etc.) that interpolate fields from the surrounding context.
// A template contains tokens of the form:
//
//	{type.prop}                       -> the raw value of ctx.<type>.<prop>
//	{type.prop::modifier}             -> the value passed through a modifier
//	{type.prop::[ifTruthy||ifFalsy]}  -> a conditional (ternary) expression
//
// For example, given a file context, this template:
//
//	"{file.name} ({file.size}) uploaded by {user.username}"
//
// might render as:
//
//	"abc123.png (10485760) uploaded by alice"
//
// Supported types are file, user, url, link, metricsUser and metricsZipline.
// Unknown tokens are left untouched (the literal "{...}" is returned) so that
// stray braces in user content are never silently eaten. Tokens that reference a
// nil context pointer resolve to the empty string.
//
// Security: any access to a sensitive property (password, token, totpSecret) is
// always redacted to "***", regardless of whether the underlying value is set.
package parser

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"zipfast/internal/models"
)

// Link is a rendered upload/shorten link. Returned is what is shown to the
// client (it may be an embed-friendly or extensionless URL); Raw is the direct
// URL to the resource.
type Link struct {
	Returned string
	Raw      string
}

// Metrics is an aggregated stats snapshot used by the metrics* token families.
type Metrics struct {
	Files      int
	Urls       int
	Storage    int64
	FilesViews int
	UrlsViews  int
}

// Context carries every value a template can interpolate. Any pointer field may
// be nil; tokens referencing a nil pointer resolve to the empty string.
type Context struct {
	File           *models.File
	User           *models.User
	URL            *models.Url
	Link           Link
	MetricsUser    Metrics
	MetricsZipline Metrics
}

// redacted is returned for any sensitive property access.
const redacted = "***"

// tokenRe matches a single template token and captures its logical parts:
//
//	group 1: the body, e.g. "file.name" or "user.role"
//	group 2: a conditional body "ifTruthy||ifFalsy" (only when "::[...]" is used)
//	group 3: a plain modifier, e.g. "upper" (only when "::word" is used)
//
// At most one of group 2 / group 3 is populated for a given token.
var tokenRe = regexp.MustCompile(`\{([a-zA-Z]+\.[a-zA-Z0-9]+)(?:::(?:\[([^\]]*)\]|([a-zA-Z]+)))?\}`)

// sensitiveProps are always redacted, no matter which type they appear on. Keys
// are compared lower-cased.
var sensitiveProps = map[string]bool{
	"password":   true,
	"token":      true,
	"totpsecret": true,
}

// ParseString renders tmpl against ctx and returns the result.
//
// Recognized token forms (see the package doc for details):
//
//	{type.prop}
//	{type.prop::modifier}      modifier in {upper, lower, length}
//	{type.prop::[a||b]}        conditional: a if the value is truthy, else b
//
// A value is "truthy" when it is non-empty and is not "0" or "false". Unknown
// tokens are returned verbatim.
func ParseString(tmpl string, ctx Context) string {
	return tokenRe.ReplaceAllStringFunc(tmpl, func(match string) string {
		groups := tokenRe.FindStringSubmatch(match)
		if groups == nil {
			return match
		}
		body := groups[1]        // e.g. "file.name"
		conditional := groups[2] // contents of [...] when present
		modifier := groups[3]    // bare modifier when present

		dot := strings.IndexByte(body, '.')
		if dot < 0 {
			return match
		}
		typ := body[:dot]
		prop := body[dot+1:]

		value, ok := resolve(typ, prop, ctx)
		if !ok {
			// Unknown type/prop: leave the literal token untouched.
			return match
		}

		switch {
		case strings.Contains(match, "::["):
			// Conditional form {type.prop::[a||b]}.
			return applyConditional(value, conditional)
		case modifier != "":
			return applyModifier(value, modifier)
		default:
			return value
		}
	})
}

// resolve returns the string value for a given type/prop pair. The boolean
// result is false when the type or property is unknown (so the caller can keep
// the literal token), and true otherwise — including when the value resolves to
// the empty string because of a nil context pointer.
func resolve(typ, prop string, ctx Context) (string, bool) {
	// Sensitive properties are redacted regardless of type, but only for known
	// types so genuinely unknown tokens still pass through unchanged.
	switch typ {
	case "file", "user", "url", "link", "metricsUser", "metricsZipline":
		if sensitiveProps[strings.ToLower(prop)] {
			return redacted, true
		}
	}

	switch typ {
	case "file":
		return resolveFile(prop, ctx.File)
	case "user":
		return resolveUser(prop, ctx.User)
	case "url":
		return resolveURL(prop, ctx.URL)
	case "link":
		return resolveLink(prop, ctx.Link)
	case "metricsUser":
		return resolveMetrics(prop, ctx.MetricsUser)
	case "metricsZipline":
		return resolveMetrics(prop, ctx.MetricsZipline)
	default:
		return "", false
	}
}

func resolveFile(prop string, f *models.File) (string, bool) {
	// Validate the property name first so unknown props pass through even when
	// the context pointer is nil.
	switch prop {
	case "id", "name", "originalName", "size", "type", "views", "maxViews", "createdAt", "updatedAt":
	default:
		return "", false
	}
	if f == nil {
		return "", true
	}
	switch prop {
	case "id":
		return f.ID, true
	case "name":
		return f.Name, true
	case "originalName":
		return deref(f.OriginalName), true
	case "size":
		return strconv.FormatInt(f.Size, 10), true
	case "type":
		return f.Type, true
	case "views":
		return strconv.Itoa(f.Views), true
	case "maxViews":
		return derefInt(f.MaxViews), true
	case "createdAt":
		return formatTime(f.CreatedAt), true
	case "updatedAt":
		return formatTime(f.UpdatedAt), true
	}
	return "", false
}

func resolveUser(prop string, u *models.User) (string, bool) {
	switch prop {
	case "id", "username", "role", "createdAt":
	default:
		return "", false
	}
	if u == nil {
		return "", true
	}
	switch prop {
	case "id":
		return u.ID, true
	case "username":
		return u.Username, true
	case "role":
		return string(u.Role), true
	case "createdAt":
		return formatTime(u.CreatedAt), true
	}
	return "", false
}

func resolveURL(prop string, u *models.Url) (string, bool) {
	switch prop {
	case "id", "code", "vanity", "destination", "views", "maxViews":
	default:
		return "", false
	}
	if u == nil {
		return "", true
	}
	switch prop {
	case "id":
		return u.ID, true
	case "code":
		return u.Code, true
	case "vanity":
		return deref(u.Vanity), true
	case "destination":
		return u.Destination, true
	case "views":
		return strconv.Itoa(u.Views), true
	case "maxViews":
		return derefInt(u.MaxViews), true
	}
	return "", false
}

func resolveLink(prop string, l Link) (string, bool) {
	switch prop {
	case "raw":
		return l.Raw, true
	case "returned":
		return l.Returned, true
	default:
		return "", false
	}
}

func resolveMetrics(prop string, m Metrics) (string, bool) {
	switch prop {
	case "files":
		return strconv.Itoa(m.Files), true
	case "urls":
		return strconv.Itoa(m.Urls), true
	case "storage":
		return strconv.FormatInt(m.Storage, 10), true
	case "filesViews":
		return strconv.Itoa(m.FilesViews), true
	case "urlsViews":
		return strconv.Itoa(m.UrlsViews), true
	default:
		return "", false
	}
}

// applyModifier transforms value according to a bare modifier. Unknown
// modifiers leave the value unchanged.
func applyModifier(value, modifier string) string {
	switch strings.ToLower(modifier) {
	case "upper":
		return strings.ToUpper(value)
	case "lower":
		return strings.ToLower(value)
	case "length":
		return strconv.Itoa(len(value))
	default:
		return value
	}
}

// applyConditional implements the ternary form {type.prop::[a||b]}: it returns a
// when value is truthy, otherwise b. The two branches are split on the first
// "||"; a missing branch is treated as the empty string.
func applyConditional(value, conditional string) string {
	ifTruthy, ifFalsy := conditional, ""
	if i := strings.Index(conditional, "||"); i >= 0 {
		ifTruthy = conditional[:i]
		ifFalsy = conditional[i+2:]
	}
	if truthy(value) {
		return ifTruthy
	}
	return ifFalsy
}

// truthy reports whether value should be considered "true" in a conditional.
// Empty strings and the literals "0" and "false" are falsy.
func truthy(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "0", "false":
		return false
	default:
		return true
	}
}

// formatTime renders a time as RFC3339, matching the rest of the API contract.
// The zero time renders as the empty string.
func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

// deref returns the pointed-to string or "" when the pointer is nil.
func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// derefInt returns the pointed-to int as a string, or "" when the pointer is nil.
func derefInt(n *int) string {
	if n == nil {
		return ""
	}
	return strconv.Itoa(*n)
}
