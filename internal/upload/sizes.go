// Package upload contains the pure request-shaping logic for file uploads:
// parsing human-friendly byte sizes and durations, generating output file names,
// and parsing the x-zipline-* upload headers. It has no dependencies on the
// database, datasource, or network layers so it can be unit-tested in isolation.
package upload

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// byteUnits maps a (lower-cased) size suffix to its multiplier in bytes. The
// suffixes are checked longest-first so that "kb" wins over the shorter "b".
var byteUnits = []struct {
	suffix     string
	multiplier int64
}{
	{"tb", 1 << 40},
	{"gb", 1 << 30},
	{"mb", 1 << 20},
	{"kb", 1 << 10},
	{"b", 1},
}

// ParseBytes parses a human-friendly size string into a number of bytes.
//
// It accepts an optional decimal number followed by an optional unit suffix
// (case-insensitive): b, kb, mb, gb, tb. A bare number is treated as a raw byte
// count. Examples: "100mb", "1.5gb", "500kb", "1024".
func ParseBytes(s string) (int64, error) {
	raw := strings.TrimSpace(s)
	if raw == "" {
		return 0, fmt.Errorf("empty size string")
	}

	lower := strings.ToLower(raw)

	// Find the matching unit suffix (longest first). A value with no suffix is a
	// raw byte count and uses a multiplier of 1.
	var multiplier int64 = 1
	numPart := lower
	for _, u := range byteUnits {
		if strings.HasSuffix(lower, u.suffix) {
			multiplier = u.multiplier
			numPart = strings.TrimSpace(lower[:len(lower)-len(u.suffix)])
			break
		}
	}

	if numPart == "" {
		return 0, fmt.Errorf("invalid size %q: missing number", s)
	}

	value, err := strconv.ParseFloat(numPart, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid size %q: %w", s, err)
	}
	if value < 0 {
		return 0, fmt.Errorf("invalid size %q: must not be negative", s)
	}

	return int64(value * float64(multiplier)), nil
}

// durationUnits maps a single-character duration suffix to its time.Duration
// value. Days and weeks are not part of the stdlib so we define them explicitly.
var durationUnits = map[byte]time.Duration{
	's': time.Second,
	'm': time.Minute,
	'h': time.Hour,
	'd': 24 * time.Hour,
	'w': 7 * 24 * time.Hour,
}

// ParseDuration parses a human-friendly duration string such as "30s", "30m",
// "1h", "1d", "7d" or "1w". The supported units are s, m, h, d and w. A decimal
// magnitude is allowed (e.g. "1.5h"). Unlike time.ParseDuration it understands
// days and weeks but only accepts a single unit-terminated component.
func ParseDuration(s string) (time.Duration, error) {
	raw := strings.TrimSpace(strings.ToLower(s))
	if raw == "" {
		return 0, fmt.Errorf("empty duration string")
	}

	unitByte := raw[len(raw)-1]
	unit, ok := durationUnits[unitByte]
	if !ok {
		return 0, fmt.Errorf("invalid duration %q: unknown unit %q (want s, m, h, d or w)", s, string(unitByte))
	}

	numPart := strings.TrimSpace(raw[:len(raw)-1])
	if numPart == "" {
		return 0, fmt.Errorf("invalid duration %q: missing number", s)
	}

	value, err := strconv.ParseFloat(numPart, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q: %w", s, err)
	}
	if value < 0 {
		return 0, fmt.Errorf("invalid duration %q: must not be negative", s)
	}

	return time.Duration(value * float64(unit)), nil
}

// ParseExpiry parses an expiry directive as sent in the x-zipline-deletes-at
// header and returns the absolute time at which the resource should expire.
//
//   - "never" (case-insensitive) returns (nil, nil): no expiry.
//   - "date=2025-01-01" (or any layout accepted by parseDate) returns that
//     absolute date.
//   - anything else is treated as a relative duration (see ParseDuration) added
//     to the current time, e.g. "1h", "7d".
//
// Expiry times that resolve to a moment in the past are rejected with an error.
func ParseExpiry(header string) (*time.Time, error) {
	raw := strings.TrimSpace(header)
	if raw == "" {
		return nil, fmt.Errorf("empty expiry string")
	}

	if strings.EqualFold(raw, "never") {
		return nil, nil
	}

	now := time.Now()

	if rest, ok := cutPrefixFold(raw, "date="); ok {
		t, err := parseDate(strings.TrimSpace(rest))
		if err != nil {
			return nil, fmt.Errorf("invalid expiry date %q: %w", rest, err)
		}
		if t.Before(now) {
			return nil, fmt.Errorf("expiry date %q is in the past", rest)
		}
		return &t, nil
	}

	// Relative duration added to now.
	d, err := ParseDuration(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid expiry %q: %w", header, err)
	}
	if d <= 0 {
		return nil, fmt.Errorf("expiry %q resolves to the past", header)
	}
	t := now.Add(d)
	return &t, nil
}

// dateLayouts lists the absolute-date formats accepted by parseDate, most
// specific first.
var dateLayouts = []string{
	time.RFC3339,
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05",
	"2006-01-02",
	"01/02/2006",
}

// parseDate tries each supported absolute-date layout in turn.
func parseDate(s string) (time.Time, error) {
	for _, layout := range dateLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognized date format %q", s)
}

// cutPrefixFold is a case-insensitive variant of strings.CutPrefix.
func cutPrefixFold(s, prefix string) (string, bool) {
	if len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix) {
		return s[len(prefix):], true
	}
	return "", false
}
