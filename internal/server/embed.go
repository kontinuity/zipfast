package server

import (
	"html"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"zipfast/internal/auth"
	"zipfast/internal/models"
	"zipfast/internal/parser"
)

// embed.go renders server-side OpenGraph / Twitter "embed" pages for files and
// short URLs. There is no React here: we emit a small, self-contained HTML
// document whose <head> carries the meta tags link unfurlers (Discord, Twitter,
// Slack, ...) read, and whose <body> contains a human-visible link to the raw
// resource. This mirrors Zipline's ssr-view / ssr-view-url servers.
//
// All interpolated values are HTML-escaped (see embedEsc) so a hostile file name
// or original name can never break out of an attribute or inject markup.

// embedEsc escapes a string for safe inclusion in HTML text or a
// double-quoted attribute value.
func embedEsc(s string) string { return html.EscapeString(s) }

// embedMeta accumulates the <head> meta lines for an embed page.
type embedMeta struct {
	b strings.Builder
}

func (m *embedMeta) line(s string) {
	m.b.WriteString("  ")
	m.b.WriteString(s)
	m.b.WriteByte('\n')
}

// metaProperty appends <meta property="k" content="v" /> (OpenGraph style).
func (m *embedMeta) metaProperty(k, v string) {
	m.line(`<meta property="` + embedEsc(k) + `" content="` + embedEsc(v) + `" />`)
}

// metaName appends <meta name="k" content="v" /> (Twitter style).
func (m *embedMeta) metaName(k, v string) {
	m.line(`<meta name="` + embedEsc(k) + `" content="` + embedEsc(v) + `" />`)
}

func (m *embedMeta) title(t string) {
	m.line("<title>" + embedEsc(t) + "</title>")
}

// embedPage assembles a complete HTML document from the rendered <head> meta and
// a body containing a single link to rawURL (label is the visible link text).
func embedPage(headMeta, rawURL, label string) string {
	var b strings.Builder
	b.WriteString("<!DOCTYPE html>\n<html lang=\"en\">\n<head>\n")
	b.WriteString("  <meta charset=\"utf-8\" />\n")
	b.WriteString("  <meta name=\"viewport\" content=\"width=device-width, initial-scale=1\" />\n")
	b.WriteString(headMeta)
	b.WriteString("</head>\n<body>\n")
	if rawURL != "" {
		b.WriteString(`  <a href="` + embedEsc(rawURL) + `">` + embedEsc(label) + "</a>\n")
	}
	b.WriteString("</body>\n</html>\n")
	return b.String()
}

// embedRenderString interpolates a user-supplied embed template (embedTitle,
// embedDescription, embedColor, ...) against the file/user context.
func embedRenderString(tmpl string, file *models.File, user *models.User) string {
	if tmpl == "" {
		return ""
	}
	return parser.ParseString(tmpl, parser.Context{File: file, User: user})
}

// handleViewFile renders the embed/meta HTML for a file (GET /view/{id}).
func (a *App) handleViewFile(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	file, err := a.Store.GetFileByName(r.Context(), id)
	if err != nil || file == nil {
		a.embedNotFound(w, http.StatusNotFound, "Not Found")
		return
	}
	if file.UserID == nil {
		a.embedNotFound(w, http.StatusNotFound, "Not Found")
		return
	}

	// Expiry / max-views gates (mirror the SSR view server).
	if file.MaxViews != nil && file.Views >= *file.MaxViews {
		a.embedNotFound(w, http.StatusGone, "Gone")
		return
	}
	if file.DeletesAt != nil && !file.DeletesAt.After(time.Now()) {
		a.embedNotFound(w, http.StatusGone, "Expired")
		return
	}

	owner, err := a.Store.GetUserByID(r.Context(), *file.UserID)
	if err != nil || owner == nil {
		a.embedNotFound(w, http.StatusNotFound, "Not Found")
		return
	}

	base := a.BaseURL(r)
	rawURL := base + "/raw/" + file.Name

	// Password-protected files with no valid token: only a generic title, no
	// file details, no media meta.
	if file.Password != nil && *file.Password != "" {
		token := r.URL.Query().Get("token")
		if token == "" || !auth.VerifyAccessToken(token, "file", file.ID, a.Cfg.Core.Secret) {
			var m embedMeta
			m.title("Password Protected")
			a.writeHTML(w, http.StatusOK, embedPage(m.b.String(), "", ""))
			return
		}
	}

	view := owner.View
	viewEnabled := view.Enabled
	showRichOg := viewEnabled && view.Embed
	showMediaOg := viewEnabled && (view.Embed || view.EmbedMediaOnly)

	var m embedMeta

	// Rich text meta (title/description/site name/color) — only when full embed
	// is enabled and the corresponding template is configured.
	if showRichOg {
		if v := embedRenderString(view.EmbedTitle, file, owner); v != "" {
			m.metaProperty("og:title", v)
		}
		if v := embedRenderString(view.EmbedDescription, file, owner); v != "" {
			m.metaProperty("og:description", v)
		}
		if v := embedRenderString(view.EmbedSiteName, file, owner); v != "" {
			m.metaProperty("og:site_name", v)
		}
		if v := embedRenderString(view.EmbedColor, file, owner); v != "" {
			m.metaProperty("theme-color", v)
		}
	}

	typ := file.Type
	switch {
	case showMediaOg && strings.HasPrefix(typ, "image"):
		m.metaProperty("og:type", "image")
		m.metaProperty("og:image", rawURL)
		m.metaProperty("og:url", base+"/view/"+file.Name)
		m.metaProperty("twitter:card", "summary_large_image")
		m.metaProperty("twitter:image", rawURL)
		if showRichOg {
			m.metaProperty("twitter:title", file.Name)
		}

	case showMediaOg && strings.HasPrefix(typ, "video"):
		if file.Thumbnail != nil && file.Thumbnail.Path != "" {
			m.metaProperty("og:image", base+"/raw/"+file.Thumbnail.Path)
		}
		m.metaProperty("og:type", "video.other")
		m.metaProperty("og:url", base+"/view/"+file.Name)
		m.metaProperty("og:video:url", rawURL)
		m.metaProperty("og:video:width", "1920")
		m.metaProperty("og:video:height", "1080")

	case showMediaOg && strings.HasPrefix(typ, "audio"):
		m.metaName("twitter:card", "player")
		m.metaName("twitter:player", rawURL)
		m.metaName("twitter:player:stream", rawURL)
		m.metaName("twitter:player:stream:content_type", typ)
		if showRichOg {
			m.metaName("twitter:title", file.Name)
		}
		m.metaName("twitter:player:width", "720")
		m.metaName("twitter:player:height", "480")
		m.metaProperty("og:type", "music.song")
		m.metaProperty("og:url", base+"/view/"+file.Name)
		m.metaProperty("og:audio", rawURL)
		m.metaProperty("og:audio:secure_url", rawURL)
		m.metaProperty("og:audio:type", typ)

	case showRichOg:
		// Any other type, with full embed enabled.
		m.metaProperty("og:url", base+"/view/"+file.Name)
	}

	// Title is always present: original name preferred, else stored name.
	titleText := file.Name
	if file.OriginalName != nil && *file.OriginalName != "" {
		titleText = *file.OriginalName
	}
	m.title(titleText)

	a.writeHTML(w, http.StatusOK, embedPage(m.b.String(), rawURL, titleText))
}

// handleViewURL renders the embed/meta HTML for a short URL (GET /view/url/{id}).
//
// For a password-protected URL with no valid token we emit a generic
// "Password Protected" page and do not reveal the destination. Otherwise we
// increment the view counter and emit a page that links to (and meta-refreshes
// toward) the destination so a human visitor is forwarded.
func (a *App) handleViewURL(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	u, err := a.Store.GetURLByCode(r.Context(), id)
	if err != nil || u == nil {
		a.embedNotFound(w, http.StatusNotFound, "Not Found")
		return
	}
	if !u.Enabled {
		a.embedNotFound(w, http.StatusNotFound, "Not Found")
		return
	}

	if u.MaxViews != nil && u.Views >= *u.MaxViews {
		if a.Cfg.Features.DeleteOnMaxViews {
			_, _ = a.Store.Pool.Exec(r.Context(), `DELETE FROM urls WHERE id=$1`, u.ID)
		}
		a.embedNotFound(w, http.StatusGone, "Gone")
		return
	}

	if u.Password != nil && *u.Password != "" {
		token := r.URL.Query().Get("token")
		if token == "" || !auth.VerifyAccessToken(token, "url", u.ID, a.Cfg.Core.Secret) {
			var m embedMeta
			m.title("Password Protected")
			a.writeHTML(w, http.StatusOK, embedPage(m.b.String(), "", ""))
			return
		}
	}

	// Valid (or no password): count the view and forward to the destination.
	if _, err := a.Store.IncrementURLViews(r.Context(), u.ID); err != nil {
		a.Log.Debug("embed: increment url views", "id", u.ID, "err", err)
	}

	var m embedMeta
	m.line(`<meta http-equiv="refresh" content="0; url=` + embedEsc(u.Destination) + `" />`)
	m.metaProperty("og:url", u.Destination)
	m.title(u.Code)
	a.writeHTML(w, http.StatusOK, embedPage(m.b.String(), u.Destination, u.Destination))
}

// embedNotFound writes a tiny HTML document with the given status and message.
func (a *App) embedNotFound(w http.ResponseWriter, status int, msg string) {
	var m embedMeta
	m.title(msg)
	a.writeHTML(w, status, embedPage(m.b.String(), "", ""))
}

// writeHTML writes an HTML response with the given status.
func (a *App) writeHTML(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}
