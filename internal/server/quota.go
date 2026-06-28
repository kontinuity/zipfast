package server

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"zipfast/internal/models"
)

// QuotaError indicates that an upload or shorten request would exceed the
// authenticated user's configured quota. It is a distinct type so callers
// (the upload / url handlers) can map it to an HTTP 4xx response and surface
// Message to the client.
type QuotaError struct {
	Message string
}

func (e *QuotaError) Error() string {
	if e == nil || e.Message == "" {
		return "quota exceeded"
	}
	return e.Message
}

// ErrQuotaExceeded is a sentinel for errors.Is checks. Concrete failures return
// a *QuotaError (which reports true for errors.Is against this), so handlers may
// either compare with errors.Is(err, ErrQuotaExceeded) or type-assert for the
// human-readable Message.
var ErrQuotaExceeded = errors.New("quota exceeded")

// Is lets errors.Is(err, ErrQuotaExceeded) match any *QuotaError.
func (e *QuotaError) Is(target error) bool {
	return target == ErrQuotaExceeded
}

// quotaRow is the subset of the user_quotas row the enforcement logic needs.
type quotaRow struct {
	filesQuota models.UserFilesQuota
	maxBytes   *string
	maxFiles   *int
	maxUrls    *int
}

// quotaLoad fetches the user's user_quotas row. When the user has no quota row
// (the common case) it returns (nil, nil): no quota configured means unlimited.
func (a *App) quotaLoad(ctx context.Context, userID string) (*quotaRow, error) {
	var q quotaRow
	err := a.Store.Pool.QueryRow(ctx, `
		SELECT files_quota, max_bytes, max_files, max_urls
		FROM user_quotas WHERE user_id = $1`, userID).
		Scan(&q.filesQuota, &q.maxBytes, &q.maxFiles, &q.maxUrls)
	if err != nil {
		// pgx.ErrNoRows (and the db package's wrapped ErrNotFound) both mean the
		// user simply has no quota configured.
		if quotaIsNoRows(err) {
			return nil, nil
		}
		return nil, err
	}
	return &q, nil
}

// quotaIsNoRows reports whether err signals an empty result set, tolerating both
// the raw pgx error and any wrapped "not found" variant without importing pgx.
func quotaIsNoRows(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "no rows") || strings.Contains(msg, "not found")
}

// EnforceFileQuota checks whether adding addBytes across addFiles new files would
// push the user past their configured file quota. It returns a *QuotaError when
// the limit would be exceeded, nil when the upload is allowed (including when the
// user has no quota configured), or a non-quota error on a database failure.
//
// The quota is interpreted per the user's files_quota mode:
//   - BY_BYTES: SUM(size) of the user's existing files + addBytes must not exceed
//     max_bytes (a humanized size string such as "100mb").
//   - BY_FILES: COUNT(files) for the user + addFiles must not exceed max_files.
//
// It is standalone and side-effect free; the caller invokes it before committing
// an upload.
func (a *App) EnforceFileQuota(ctx context.Context, userID string, addBytes int64, addFiles int) error {
	if userID == "" {
		return nil
	}
	q, err := a.quotaLoad(ctx, userID)
	if err != nil {
		return err
	}
	if q == nil {
		return nil // no quota configured: unlimited
	}

	switch q.filesQuota {
	case models.QuotaByBytes:
		maxBytes := quotaParseBytes(q.maxBytes)
		if maxBytes <= 0 {
			return nil // unset or unparseable: treat as unlimited
		}
		var used int64
		if err := a.Store.Pool.QueryRow(ctx,
			`SELECT COALESCE(SUM(size), 0) FROM files WHERE user_id = $1`, userID).
			Scan(&used); err != nil {
			return err
		}
		if used+quotaMax64(addBytes, 0) > maxBytes {
			return &QuotaError{Message: fmt.Sprintf(
				"upload would exceed your storage quota (%d of %d bytes used)", used, maxBytes)}
		}

	case models.QuotaByFiles:
		if q.maxFiles == nil || *q.maxFiles <= 0 {
			return nil // unset: unlimited
		}
		var count int
		if err := a.Store.Pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM files WHERE user_id = $1`, userID).
			Scan(&count); err != nil {
			return err
		}
		if count+quotaMaxInt(addFiles, 0) > *q.maxFiles {
			return &QuotaError{Message: fmt.Sprintf(
				"upload would exceed your file count quota (%d of %d files used)", count, *q.maxFiles)}
		}
	}

	return nil
}

// EnforceURLQuota checks whether creating addUrls new short URLs would push the
// user past their configured max_urls quota. Semantics mirror EnforceFileQuota:
// a *QuotaError when exceeded, nil when allowed or unconfigured, or a non-quota
// error on a database failure.
func (a *App) EnforceURLQuota(ctx context.Context, userID string, addUrls int) error {
	if userID == "" {
		return nil
	}
	q, err := a.quotaLoad(ctx, userID)
	if err != nil {
		return err
	}
	if q == nil || q.maxUrls == nil || *q.maxUrls <= 0 {
		return nil // no quota / no url limit configured: unlimited
	}

	var count int
	if err := a.Store.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM urls WHERE user_id = $1`, userID).
		Scan(&count); err != nil {
		return err
	}
	if count+quotaMaxInt(addUrls, 0) > *q.maxUrls {
		return &QuotaError{Message: fmt.Sprintf(
			"creating this link would exceed your URL quota (%d of %d used)", count, *q.maxUrls)}
	}
	return nil
}

// quotaParseBytes converts a stored max_bytes value into a byte count. The value
// is a humanized size string (e.g. "100mb", "1.5gb", "1024"); on a nil pointer,
// empty string, or parse failure it returns 0, which callers treat as unlimited.
func quotaParseBytes(s *string) int64 {
	if s == nil {
		return 0
	}
	raw := strings.TrimSpace(*s)
	if raw == "" {
		return 0
	}
	lower := strings.ToLower(raw)

	// Suffixes checked longest-first so "kb" wins over the shorter "b".
	units := []struct {
		suffix string
		mult   int64
	}{
		{"tb", 1 << 40},
		{"gb", 1 << 30},
		{"mb", 1 << 20},
		{"kb", 1 << 10},
		{"b", 1},
	}
	var mult int64 = 1
	numPart := lower
	for _, u := range units {
		if strings.HasSuffix(lower, u.suffix) {
			mult = u.mult
			numPart = strings.TrimSpace(lower[:len(lower)-len(u.suffix)])
			break
		}
	}
	if numPart == "" {
		return 0
	}
	value, err := strconv.ParseFloat(numPart, 64)
	if err != nil || value < 0 {
		return 0
	}
	return int64(value * float64(mult))
}

func quotaMax64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func quotaMaxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
