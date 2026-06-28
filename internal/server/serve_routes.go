package server

import (
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"zipfast/internal/auth"
	"zipfast/internal/media"
	"zipfast/internal/models"
	"zipfast/internal/upload"
)

// serve_routes.go implements the public-facing serving surface: the pretty file
// route (/u/{id}), the raw byte stream with HTTP Range support (/raw/{id}), the
// short-URL redirect (/go/{id}), and the small static endpoints (robots.txt,
// favicon.ico, /r/{id}, /view/*). It ports Zipline's files.dy / urls.dy / raw
// handlers.

// registerServeRoutes wires the file/url serving and embed routes onto r.
func (a *App) registerServeRoutes(r chi.Router) {
	// Pretty file route, e.g. GET /u/{id}.
	r.Get(a.Cfg.Files.Route+"/{id}", a.serveFileRoute)

	// Raw byte stream with range support.
	r.Get("/raw/{id}", a.serveRawFile)

	// Short-URL redirect routes.
	r.Get(a.Cfg.Urls.Route+"/{id}", a.serveURLRedirect)
	if a.Cfg.Urls.Route != "/go" {
		// /go is Zipline's canonical short-url path; keep it available even when
		// the configured route differs.
		r.Get("/go/{id}", a.serveURLRedirect)
	}

	// /r/{id} -> 301 /raw/{id}
	r.Get("/r/{id}", func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, "/raw/"+chi.URLParam(req, "id"), http.StatusMovedPermanently)
	})

	// View pages (server-rendered: media viewer + password form).
	r.Get("/view/{id}", a.handleViewFile)
	r.Post("/view/{id}", a.handleViewFilePassword)
	r.Get("/view/url/{id}", a.handleViewURL)
	r.Post("/view/url/{id}", a.handleViewURLPassword)

	// Public folder gate (server-rendered password form for protected folders).
	r.Get("/folder/{id}", a.handleFolderGate)
	r.Post("/folder/{id}", a.handleFolderGatePassword)
	r.Get("/folder/{id}/upload", a.handleFolderUploadGate)

	// Small static endpoints.
	r.Get("/robots.txt", a.serveRobotsTxt)
	r.Get("/favicon.ico", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
}

// serveFileRoute handles GET {files.route}/{id}: it decides whether to serve the
// raw bytes inline or to redirect to the rich /view embed page, matching
// Zipline's filesRoute.
func (a *App) serveFileRoute(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	file, err := a.Store.GetFileByName(r.Context(), id)
	if err != nil || file == nil {
		a.serveNotFound(w)
		return
	}

	viewURL := "/view/" + file.Name
	a.logFor(r).Debug("serve file route", "name", file.Name, "type", file.Type,
		"passwordProtected", file.Password != nil && *file.Password != "")

	// Password-protected files always go through the view page.
	if file.Password != nil && *file.Password != "" {
		http.Redirect(w, r, viewURL, http.StatusFound)
		return
	}

	// Load the owner once so we can consult their view/embed settings.
	var owner *models.User
	if file.UserID != nil {
		if o, oerr := a.Store.GetUserByID(r.Context(), *file.UserID); oerr == nil {
			owner = o
		}
	}

	// Text files render in the viewer (unless the owner disabled that).
	if strings.HasPrefix(file.Type, "text/") {
		if owner != nil && owner.View.DisableTextFiles {
			a.serveRawByFile(w, r, file)
			return
		}
		http.Redirect(w, r, viewURL, http.StatusFound)
		return
	}

	// If the owner enabled the embed view, redirect there.
	if owner != nil && owner.View.Enabled {
		http.Redirect(w, r, viewURL, http.StatusFound)
		return
	}

	// Otherwise serve the raw bytes directly.
	a.serveRawByFile(w, r, file)
}

// serveRawFile handles GET /raw/{id}: it looks the file up by name and streams
// it (with Range support) via serveRawByFile.
func (a *App) serveRawFile(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	name := upload.SanitizeFilename(id)
	if name == "" {
		a.serveNotFound(w)
		return
	}

	file, err := a.Store.GetFileByName(r.Context(), name)
	if err != nil || file == nil {
		// The id may be a thumbnail object key rather than a file slug. Use the
		// raw id (not the sanitized name): thumbnail keys begin with a dot, which
		// SanitizeFilename trims. The key is validated against the thumbnails
		// table before any datasource read, so there is no traversal risk.
		if a.serveThumbnailIfMatch(w, r, id) {
			return
		}
		a.serveNotFound(w)
		return
	}
	a.serveRawByFile(w, r, file)
}

// serveThumbnailIfMatch serves a thumbnail object when name is a thumbnail key
// (thumbnails.path). It applies the same gates as the parent file: a thumbnail
// for a file in a password-protected folder redirects to the folder gate, and a
// thumbnail for a password-protected file requires the file access token. It
// returns true when it has handled (served or redirected) the request.
func (a *App) serveThumbnailIfMatch(w http.ResponseWriter, r *http.Request, name string) bool {
	fid, err := a.Store.GetThumbnailFileID(r.Context(), name)
	if err != nil || fid == "" {
		return false
	}
	parent, perr := a.Store.GetFileByID(r.Context(), fid)
	if perr != nil || parent == nil {
		return false
	}

	// Outer gate: folder protection.
	if a.fileFolderBlocked(w, r, parent) {
		return true
	}
	// Parent file password: the preview is protected too.
	if parent.Password != nil && *parent.Password != "" {
		token := r.URL.Query().Get("token")
		if token == "" || !auth.VerifyAccessToken(token, "file", parent.ID, a.Cfg.Core.Secret) {
			http.Redirect(w, r, "/view/"+parent.Name, http.StatusFound)
			return true
		}
	}

	a.streamThumbnail(w, r, name)
	return true
}

// streamThumbnail writes a thumbnail object's bytes (no Range, no view counting).
func (a *App) streamThumbnail(w http.ResponseWriter, _ *http.Request, thumbPath string) {
	rc, err := a.DS.Get(thumbPath)
	if err != nil || rc == nil {
		a.serveNotFound(w)
		return
	}
	defer rc.Close()

	w.Header().Set("Content-Type", thumbnailContentType(thumbPath))
	w.Header().Set("Cache-Control", "private, max-age=600")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, rc)
}

// thumbnailContentType derives the image mime from a thumbnail object key's
// extension (keys look like ".thumbnail.<id>.<format>").
func thumbnailContentType(path string) string {
	ext := path
	if i := strings.LastIndex(path, "."); i >= 0 {
		ext = path[i+1:]
	}
	return media.ThumbnailMime(ext)
}

// serveRawByFile streams the bytes for an already-resolved file. It enforces
// expiry and max-views, supports HTTP Range requests, sets Content-Disposition
// for downloads, requires a valid token for password-protected files, and
// increments the view counter on a full GET.
func (a *App) serveRawByFile(w http.ResponseWriter, r *http.Request, file *models.File) {
	// Expiry: deletes_at in the past -> gone (best-effort delete, then 404).
	if file.DeletesAt != nil && !file.DeletesAt.After(time.Now()) {
		_ = a.DS.Delete(file.Name)
		_, _ = a.Store.Pool.Exec(r.Context(), `DELETE FROM files WHERE id=$1`, file.ID)
		a.serveNotFound(w)
		return
	}

	// Folder protection (outer gate): a file inside a password-protected folder
	// requires a valid folder token, even via a direct /raw or /u URL.
	if a.fileFolderBlocked(w, r, file) {
		return
	}

	// Password protection: require a valid access token.
	if file.Password != nil && *file.Password != "" {
		token := r.URL.Query().Get("token")
		if token == "" || !auth.VerifyAccessToken(token, "file", file.ID, a.Cfg.Core.Secret) {
			http.Redirect(w, r, "/view/"+file.Name, http.StatusFound)
			return
		}
	}

	// Max views: if already at/over the limit, the file is gone.
	if file.MaxViews != nil && file.Views >= *file.MaxViews {
		if a.Cfg.Features.DeleteOnMaxViews {
			_ = a.DS.Delete(file.Name)
			_, _ = a.Store.Pool.Exec(r.Context(), `DELETE FROM files WHERE id=$1`, file.ID)
		}
		w.WriteHeader(http.StatusGone)
		return
	}

	// Determine the object size: prefer the stored size, fall back to the
	// datasource.
	size := file.Size
	if size <= 0 {
		if s, serr := a.DS.Size(file.Name); serr == nil && s >= 0 {
			size = s
		}
	}

	contentType := file.Type
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	if strings.HasPrefix(contentType, "text/") && !strings.Contains(contentType, "charset") {
		contentType = contentType + "; charset=utf-8"
	}

	download := r.URL.Query().Get("download") != ""
	disposition := serveContentDisposition(file, download)

	rangeHeader := r.Header.Get("Range")

	// --- Range request -------------------------------------------------------
	if rangeHeader != "" && size > 0 {
		start, end, ok := serveParseRange(rangeHeader, size)
		if !ok || start >= size || end >= size {
			// Unsatisfiable range: fall back to the whole object with 416, as
			// Zipline does.
			rc, gerr := a.DS.Get(file.Name)
			if gerr != nil || rc == nil {
				a.serveNotFound(w)
				return
			}
			defer rc.Close()

			w.Header().Set("Content-Type", contentType)
			w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
			if disposition != "" {
				w.Header().Set("Content-Disposition", disposition)
			}
			a.serveCountView(r, file, rangeHeader)
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			_, _ = io.Copy(w, rc)
			return
		}

		rc, gerr := a.DS.Range(file.Name, start, end)
		if gerr != nil || rc == nil {
			a.serveNotFound(w)
			return
		}
		defer rc.Close()

		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Content-Range", "bytes "+strconv.FormatInt(start, 10)+"-"+strconv.FormatInt(end, 10)+"/"+strconv.FormatInt(size, 10))
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Content-Length", strconv.FormatInt(end-start+1, 10))
		if disposition != "" {
			w.Header().Set("Content-Disposition", disposition)
		}
		a.serveCountView(r, file, rangeHeader)
		w.WriteHeader(http.StatusPartialContent)
		_, _ = io.Copy(w, rc)
		return
	}

	// --- Full GET ------------------------------------------------------------
	a.logFor(r).Debug("datasource get", "key", file.Name, "size", size)
	rc, gerr := a.DS.Get(file.Name)
	if gerr != nil || rc == nil {
		a.serveNotFound(w)
		return
	}
	defer rc.Close()

	w.Header().Set("Content-Type", contentType)
	if size > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	}
	w.Header().Set("Accept-Ranges", "bytes")
	if disposition != "" {
		w.Header().Set("Content-Disposition", disposition)
	}
	a.serveCountView(r, file, rangeHeader)
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, rc)
	a.logFor(r).Debug("raw served", "name", file.Name, "size", size, "download", download)
}

// serveCountView increments the file view counter when this request should count
// as a view: a full GET, or a range request starting at byte 0 (matching
// Zipline's "isView" rule). Counting failures are non-fatal.
func (a *App) serveCountView(r *http.Request, file *models.File, rangeHeader string) {
	isView := rangeHeader == "" || strings.HasPrefix(rangeHeader, "bytes=0")
	if !isView {
		return
	}
	if _, err := a.Store.IncrementFileViews(r.Context(), file.ID); err != nil {
		a.Log.Debug("serve: increment file views", "id", file.ID, "err", err)
	}
}

// serveContentDisposition builds the Content-Disposition header value. When the
// file has an original name we always set a filename* (RFC 5987) so the browser
// preserves it; download forces "attachment". With no original name we only set
// the header for explicit downloads.
func serveContentDisposition(file *models.File, download bool) string {
	if file.OriginalName != nil && *file.OriginalName != "" {
		prefix := ""
		if download {
			prefix = "attachment; "
		}
		return prefix + "filename*=utf-8''" + serveRFC5987(*file.OriginalName)
	}
	if download {
		return "attachment;"
	}
	return ""
}

// serveURLRedirect handles GET {urls.route}/{id} and /go/{id}: it resolves the
// short URL, enforces enabled/max-views/password, increments the counter, and
// 302-redirects to the destination. Ports Zipline's urlsRoute.
func (a *App) serveURLRedirect(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	u, err := a.Store.GetURLByCode(r.Context(), id)
	if err != nil || u == nil {
		a.serveNotFound(w)
		return
	}
	if !u.Enabled {
		a.serveNotFound(w)
		return
	}

	if u.MaxViews != nil && u.Views >= *u.MaxViews {
		if a.Cfg.Features.DeleteOnMaxViews {
			_, _ = a.Store.Pool.Exec(r.Context(), `DELETE FROM urls WHERE id=$1`, u.ID)
		}
		w.WriteHeader(http.StatusGone)
		return
	}

	if u.Password != nil && *u.Password != "" {
		token := r.URL.Query().Get("token")
		if token == "" || !auth.VerifyAccessToken(token, "url", u.ID, a.Cfg.Core.Secret) {
			http.Redirect(w, r, "/view/url/"+u.ID, http.StatusFound)
			return
		}
	}

	if _, err := a.Store.IncrementURLViews(r.Context(), u.ID); err != nil {
		a.Log.Debug("serve: increment url views", "id", u.ID, "err", err)
	}

	a.logFor(r).Debug("url redirect", "code", u.Code)
	http.Redirect(w, r, u.Destination, http.StatusFound)
}

// serveRobotsTxt serves /robots.txt when the feature is enabled, else 404.
func (a *App) serveRobotsTxt(w http.ResponseWriter, _ *http.Request) {
	if !a.Cfg.Features.RobotsTxt {
		a.serveNotFound(w)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "User-Agent: *\nDisallow: /\n")
}

// serveNotFound writes a Zipline-style JSON 404.
func (a *App) serveNotFound(w http.ResponseWriter) {
	a.Error(w, http.StatusNotFound, "not found")
}

// serveParseRange parses a single-range HTTP Range header ("bytes=start-end")
// against a known size and returns an inclusive [start,end]. It supports the
// open-ended "bytes=start-" and suffix "bytes=-n" forms. ok is false when the
// header is absent, malformed, or specifies multiple ranges.
func serveParseRange(header string, size int64) (start, end int64, ok bool) {
	const prefix = "bytes="
	if !strings.HasPrefix(header, prefix) {
		return 0, 0, false
	}
	spec := strings.TrimSpace(header[len(prefix):])
	if spec == "" || strings.Contains(spec, ",") {
		return 0, 0, false
	}

	dash := strings.IndexByte(spec, '-')
	if dash < 0 {
		return 0, 0, false
	}
	startStr := strings.TrimSpace(spec[:dash])
	endStr := strings.TrimSpace(spec[dash+1:])

	switch {
	case startStr == "" && endStr == "":
		return 0, 0, false
	case startStr == "":
		// Suffix range: last N bytes.
		n, err := strconv.ParseInt(endStr, 10, 64)
		if err != nil || n <= 0 {
			return 0, 0, false
		}
		if n > size {
			n = size
		}
		return size - n, size - 1, true
	case endStr == "":
		// Open-ended range: start..end-of-file.
		s, err := strconv.ParseInt(startStr, 10, 64)
		if err != nil || s < 0 {
			return 0, 0, false
		}
		return s, size - 1, true
	default:
		s, err1 := strconv.ParseInt(startStr, 10, 64)
		e, err2 := strconv.ParseInt(endStr, 10, 64)
		if err1 != nil || err2 != nil || s < 0 || e < 0 || e < s {
			return 0, 0, false
		}
		if e > size-1 {
			e = size - 1
		}
		return s, e, true
	}
}

// serveRFC5987 percent-encodes a string for use in an RFC 5987 ext-value
// (filename*=utf-8”...). All bytes outside the unreserved attr-char set are
// %-encoded, matching encodeURIComponent closely enough for header safety.
func serveRFC5987(s string) string {
	const upperhex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') ||
			c == '-' || c == '.' || c == '_' || c == '~' {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(upperhex[c>>4])
		b.WriteByte(upperhex[c&0x0f])
	}
	return b.String()
}
