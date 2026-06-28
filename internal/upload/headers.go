package upload

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"

	"zipfast/internal/config"
)

// Header names understood by ParseHeaders. They mirror the x-zipline-* headers
// emitted by the Zipline web UI and ShareX-style clients.
const (
	HeaderDeletesAt          = "x-zipline-deletes-at"
	HeaderFormat             = "x-zipline-format"
	HeaderCompressionType    = "x-zipline-image-compression-type"
	HeaderCompressionPercent = "x-zipline-image-compression-percent"
	HeaderPassword           = "x-zipline-password"
	HeaderMaxViews           = "x-zipline-max-views"
	HeaderNoJSON             = "x-zipline-no-json"
	HeaderOriginalName       = "x-zipline-original-name"
	HeaderFolder             = "x-zipline-folder"
	HeaderFilename           = "x-zipline-filename"
	HeaderDomain             = "x-zipline-domain"
	HeaderFileExtension      = "x-zipline-file-extension"

	// Partial / chunked upload headers.
	HeaderContentRange   = "content-range"
	HeaderContentLength  = "content-length"
	HeaderPFilename      = "x-zipline-p-filename"
	HeaderPContentType   = "x-zipline-p-content-type"
	HeaderPIdentifier    = "x-zipline-p-identifier"
	HeaderPLastchunk     = "x-zipline-p-lastchunk"
	HeaderPContentLength = "x-zipline-p-content-length"
)

// Compression describes the requested image-compression settings.
type Compression struct {
	// Type is the target compression/output format (e.g. "jpg").
	Type string
	// Percent is the compression strength, 0..100.
	Percent int
}

// Partial describes a single chunk of a chunked ("partial") upload as conveyed
// by the content-range and x-zipline-p-* headers.
type Partial struct {
	Filename    string
	ContentType string
	Identifier  string
	Lastchunk   bool
	Start       int64
	End         int64
	Total       int64
	// ContentLength is the byte length of this chunk's body.
	ContentLength int64
}

// Options is the fully-parsed set of per-upload directives derived from the
// request headers. Pointer fields are nil when the corresponding header was
// absent, distinguishing "unset" from a zero value.
type Options struct {
	// DeletesAt is the absolute expiry time, or nil for "never"/unset.
	DeletesAt *time.Time
	// Format is the requested file-name format (one of KnownFormats). Empty when
	// the header was not supplied (caller applies its default).
	Format string
	// Password, when non-empty, protects the uploaded file.
	Password string
	// MaxViews limits how many times the file may be viewed before deletion.
	MaxViews *int
	// NoJSON requests a plain-text (non-JSON) response.
	NoJSON bool
	// AddOriginalName requests that the original client file name be retained.
	AddOriginalName bool
	// Compression holds image-compression settings, or nil when not requested.
	Compression *Compression
	// Folder is the destination folder id, if any.
	Folder string
	// OverrideFilename forces a specific output name (before extension).
	OverrideFilename string
	// OverrideReturnDomain is the domain to use when building the returned URL.
	OverrideReturnDomain string
	// OverrideExtension forces a specific file extension (without the dot).
	OverrideExtension string
	// Partial holds chunked-upload metadata, or nil for a normal upload.
	Partial *Partial
}

// ParseHeaders reads the x-zipline-* upload headers from h and returns the
// parsed Options. It validates each value and returns a descriptive error for
// the first malformed header it encounters. files supplies the relevant limits
// and defaults (notably MaxExpiration and the set of valid formats).
func ParseHeaders(h http.Header, files config.Files) (*Options, error) {
	opts := &Options{}

	// --- deletes-at / expiry ---------------------------------------------
	if v := strings.TrimSpace(h.Get(HeaderDeletesAt)); v != "" {
		expiry, err := ParseExpiry(v)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", HeaderDeletesAt, err)
		}
		if expiry != nil {
			if err := enforceMaxExpiration(*expiry, files.MaxExpiration); err != nil {
				return nil, fmt.Errorf("%s: %w", HeaderDeletesAt, err)
			}
		}
		opts.DeletesAt = expiry
	}

	// --- format ----------------------------------------------------------
	if v := strings.TrimSpace(h.Get(HeaderFormat)); v != "" {
		format := strings.ToLower(v)
		if !ValidFormat(format) {
			return nil, fmt.Errorf("%s: unknown format %q", HeaderFormat, v)
		}
		opts.Format = format
	}

	// --- image compression ----------------------------------------------
	compType := strings.TrimSpace(h.Get(HeaderCompressionType))
	compPctRaw := strings.TrimSpace(h.Get(HeaderCompressionPercent))
	if compType != "" || compPctRaw != "" {
		c := &Compression{Type: strings.ToLower(compType)}
		if compPctRaw != "" {
			pct, err := strconv.Atoi(compPctRaw)
			if err != nil {
				return nil, fmt.Errorf("%s: invalid integer %q", HeaderCompressionPercent, compPctRaw)
			}
			if pct < 0 || pct > 100 {
				return nil, fmt.Errorf("%s: %d out of range (0-100)", HeaderCompressionPercent, pct)
			}
			c.Percent = pct
		}
		opts.Compression = c
	}

	// --- password --------------------------------------------------------
	if v := h.Get(HeaderPassword); v != "" {
		opts.Password = v
	}

	// --- max views -------------------------------------------------------
	if v := strings.TrimSpace(h.Get(HeaderMaxViews)); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("%s: invalid integer %q", HeaderMaxViews, v)
		}
		if n < 0 {
			return nil, fmt.Errorf("%s: must not be negative", HeaderMaxViews)
		}
		opts.MaxViews = &n
	}

	// --- boolean flags ---------------------------------------------------
	if v := strings.TrimSpace(h.Get(HeaderNoJSON)); v != "" {
		b, err := parseBool(v)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", HeaderNoJSON, err)
		}
		opts.NoJSON = b
	}
	if v := strings.TrimSpace(h.Get(HeaderOriginalName)); v != "" {
		b, err := parseBool(v)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", HeaderOriginalName, err)
		}
		opts.AddOriginalName = b
	}

	// --- folder ----------------------------------------------------------
	if v := strings.TrimSpace(h.Get(HeaderFolder)); v != "" {
		opts.Folder = v
	}

	// --- override filename ----------------------------------------------
	if v := strings.TrimSpace(h.Get(HeaderFilename)); v != "" {
		opts.OverrideFilename = SanitizeFilename(v)
	}

	// --- return domain (comma list -> pick one at random) ----------------
	if v := strings.TrimSpace(h.Get(HeaderDomain)); v != "" {
		domains := splitDomains(v)
		if len(domains) > 0 {
			opts.OverrideReturnDomain = pickString(domains)
		}
	}

	// --- override extension ---------------------------------------------
	if v := strings.TrimSpace(h.Get(HeaderFileExtension)); v != "" {
		opts.OverrideExtension = sanitizeExtension(v)
	}

	// --- partial / chunked upload ----------------------------------------
	partial, err := parsePartial(h)
	if err != nil {
		return nil, err
	}
	opts.Partial = partial

	return opts, nil
}

// parsePartial reads the chunked-upload headers. It returns nil (no error) when
// none of the partial headers are present, signalling a normal upload.
func parsePartial(h http.Header) (*Partial, error) {
	contentRange := strings.TrimSpace(h.Get(HeaderContentRange))
	pFilename := strings.TrimSpace(h.Get(HeaderPFilename))
	pContentType := strings.TrimSpace(h.Get(HeaderPContentType))
	pIdentifier := strings.TrimSpace(h.Get(HeaderPIdentifier))
	pLastchunk := strings.TrimSpace(h.Get(HeaderPLastchunk))
	pContentLength := strings.TrimSpace(h.Get(HeaderPContentLength))

	// If nothing partial-related is set, this is a normal upload.
	if contentRange == "" && pFilename == "" && pContentType == "" &&
		pIdentifier == "" && pLastchunk == "" && pContentLength == "" {
		return nil, nil
	}

	p := &Partial{
		Filename:    pFilename,
		ContentType: pContentType,
		Identifier:  pIdentifier,
	}

	if pLastchunk != "" {
		b, err := parseBool(pLastchunk)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", HeaderPLastchunk, err)
		}
		p.Lastchunk = b
	}

	if pContentLength != "" {
		n, err := strconv.ParseInt(pContentLength, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%s: invalid integer %q", HeaderPContentLength, pContentLength)
		}
		if n < 0 {
			return nil, fmt.Errorf("%s: must not be negative", HeaderPContentLength)
		}
		p.ContentLength = n
	}

	if contentRange != "" {
		start, end, total, err := parseContentRange(contentRange)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", HeaderContentRange, err)
		}
		p.Start, p.End, p.Total = start, end, total
	}

	return p, nil
}

// parseContentRange parses an HTTP "bytes start-end/total" Content-Range value
// and returns the three numbers. The unit must be "bytes" and total must be a
// concrete number (the "*" form is not supported for uploads).
func parseContentRange(s string) (start, end, total int64, err error) {
	const prefix = "bytes "
	v := strings.TrimSpace(s)
	if !strings.HasPrefix(strings.ToLower(v), prefix) {
		return 0, 0, 0, fmt.Errorf("expected %q prefix in %q", strings.TrimSpace(prefix), s)
	}
	v = strings.TrimSpace(v[len(prefix):])

	rangePart, totalPart, ok := strings.Cut(v, "/")
	if !ok {
		return 0, 0, 0, fmt.Errorf("missing '/' separating range and total in %q", s)
	}

	startPart, endPart, ok := strings.Cut(rangePart, "-")
	if !ok {
		return 0, 0, 0, fmt.Errorf("missing '-' separating start and end in %q", s)
	}

	if start, err = strconv.ParseInt(strings.TrimSpace(startPart), 10, 64); err != nil {
		return 0, 0, 0, fmt.Errorf("invalid range start in %q", s)
	}
	if end, err = strconv.ParseInt(strings.TrimSpace(endPart), 10, 64); err != nil {
		return 0, 0, 0, fmt.Errorf("invalid range end in %q", s)
	}
	if total, err = strconv.ParseInt(strings.TrimSpace(totalPart), 10, 64); err != nil {
		return 0, 0, 0, fmt.Errorf("invalid total in %q", s)
	}

	if start < 0 || end < 0 || total < 0 {
		return 0, 0, 0, fmt.Errorf("negative value in range %q", s)
	}
	if end < start {
		return 0, 0, 0, fmt.Errorf("range end %d before start %d", end, start)
	}
	return start, end, total, nil
}

// enforceMaxExpiration rejects an expiry that exceeds the configured maximum.
// maxExpiration is a relative duration string (see ParseDuration); an empty
// value means "no maximum".
func enforceMaxExpiration(expiry time.Time, maxExpiration string) error {
	maxExpiration = strings.TrimSpace(maxExpiration)
	if maxExpiration == "" {
		return nil
	}
	maxDur, err := ParseDuration(maxExpiration)
	if err != nil {
		// A misconfigured maximum should not break uploads; treat as no limit.
		return nil
	}
	if maxDur <= 0 {
		return nil
	}
	latest := time.Now().Add(maxDur)
	if expiry.After(latest) {
		return fmt.Errorf("expiry exceeds maximum allowed (%s)", maxExpiration)
	}
	return nil
}

// parseBool parses the boolean header values clients send. It accepts the common
// truthy/falsey spellings and treats a bare present header ("") as true, which
// matches how flag-style headers are often emitted.
func parseBool(s string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "true", "1", "yes", "on":
		return true, nil
	case "false", "0", "no", "off":
		return false, nil
	default:
		return false, fmt.Errorf("invalid boolean %q", s)
	}
}

// splitDomains splits a comma-separated domain list, trimming blanks.
func splitDomains(v string) []string {
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// sanitizeExtension normalises a requested file extension: it drops a leading
// dot, lower-cases it, and strips any character that is not alphanumeric. The
// returned value never contains a leading dot.
func sanitizeExtension(v string) string {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, ".")
	v = strings.ToLower(v)

	var b strings.Builder
	b.Grow(len(v))
	for _, r := range v {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// pickString returns a cryptographically-random element of items. items must be
// non-empty.
func pickString(items []string) string {
	if len(items) == 1 {
		return items[0]
	}
	idx, err := rand.Int(rand.Reader, big.NewInt(int64(len(items))))
	if err != nil {
		return items[0]
	}
	return items[idx.Int64()]
}
