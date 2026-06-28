package server

import (
	"html"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"zipfast/internal/auth"
	"zipfast/internal/models"
	"zipfast/internal/parser"
)

// embed.go renders the server-side "view" pages for files and short URLs.
//
// There is no React here: we emit small, self-contained HTML documents. The
// <head> carries the OpenGraph / Twitter meta tags link unfurlers (Discord,
// Twitter, Slack, ...) read; the <body> shows the content to a human visitor —
// the media inline (image/video/audio/pdf/text), a password-entry form for
// protected resources, or a redirect notice for short URLs.
//
// Upstream Zipline server-rendered these pages with a dedicated React entry
// (ssr-view / ssr-view-url). We dropped server React, so the password prompt and
// inline viewer are rendered directly here instead. The interactive SPA does not
// own the /view routes.
//
// All interpolated values are HTML-escaped (see embedEsc) so a hostile file name,
// original name, or destination can never break out of an attribute or inject
// markup.

// embedCSS is the minimal dark theme shared by every view page.
const embedCSS = `*{box-sizing:border-box}html,body{margin:0;height:100%}` +
	`body{background:#1a1b1e;color:#e9ecef;font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,Helvetica,Arial,sans-serif;` +
	`display:flex;align-items:center;justify-content:center;min-height:100vh;padding:24px}` +
	`.card{background:#25262b;border:1px solid #2c2e33;border-radius:10px;padding:28px;max-width:960px;width:100%;` +
	`box-shadow:0 8px 30px rgba(0,0,0,.35)}` +
	`.card.center{max-width:420px;text-align:center}` +
	`h1{font-size:18px;font-weight:600;margin:0 0 14px;word-break:break-word}` +
	`p{margin:0 0 14px;color:#c1c2c5;font-size:14px}` +
	`.muted{color:#909296}.error{color:#ff6b6b}` +
	`form{display:flex;flex-direction:column;gap:12px;margin-top:6px}` +
	`input{background:#1a1b1e;border:1px solid #373a40;border-radius:6px;color:#e9ecef;padding:10px 12px;font-size:14px}` +
	`input:focus{outline:none;border-color:#4c6ef5}` +
	`button,.btn{display:inline-block;background:#4c6ef5;color:#fff;border:none;border-radius:6px;padding:10px 16px;` +
	`font-size:14px;font-weight:600;cursor:pointer;text-decoration:none;text-align:center}` +
	`button:hover,.btn:hover{background:#4263eb}` +
	`.media{display:block;max-width:100%;max-height:78vh;margin:0 auto;border-radius:6px}` +
	`img.media{object-fit:contain}` +
	`iframe.media{width:100%;height:78vh;border:0;background:#fff}` +
	`audio.media{width:100%}` +
	`.row{margin-top:18px;display:flex;gap:10px;justify-content:flex-end}`

// embedEsc escapes a string for safe inclusion in HTML text or a
// double-quoted attribute value.
func embedEsc(s string) string { return html.EscapeString(s) }

// embedMeta accumulates the <head> meta lines for a view page.
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

// embedDoc assembles a complete HTML document from rendered <head> meta and a
// body fragment, injecting the shared stylesheet.
func embedDoc(headMeta, body string) string {
	var b strings.Builder
	b.WriteString("<!DOCTYPE html>\n<html lang=\"en\">\n<head>\n")
	b.WriteString("  <meta charset=\"utf-8\" />\n")
	b.WriteString("  <meta name=\"viewport\" content=\"width=device-width, initial-scale=1\" />\n")
	b.WriteString(headMeta)
	b.WriteString("  <style>" + embedCSS + "</style>\n")
	b.WriteString("</head>\n<body>\n")
	b.WriteString(body)
	b.WriteString("\n</body>\n</html>\n")
	return b.String()
}

// passwordFormBody renders the password-entry card. action is the path the form
// POSTs to (the same /view path); showError adds an "incorrect password" notice.
func passwordFormBody(action string, showError bool) string {
	var b strings.Builder
	b.WriteString(`<main class="card center">`)
	b.WriteString(`<h1>Password Protected</h1>`)
	b.WriteString(`<p class="muted">Enter the password to view this content.</p>`)
	if showError {
		b.WriteString(`<p class="error">Incorrect password. Try again.</p>`)
	}
	b.WriteString(`<form method="POST" action="` + embedEsc(action) + `">`)
	b.WriteString(`<input type="password" name="password" placeholder="Password" autocomplete="current-password" autofocus required />`)
	b.WriteString(`<button type="submit">Unlock</button>`)
	b.WriteString(`</form></main>`)
	return b.String()
}

// fileDisplayName prefers the original upload name, falling back to the stored name.
func fileDisplayName(f *models.File) string {
	if f.OriginalName != nil && *f.OriginalName != "" {
		return *f.OriginalName
	}
	return f.Name
}

// viewMediaBody renders the inline viewer for an unlocked file. rawURL already
// includes the access token query when the file is password-protected.
func viewMediaBody(file *models.File, rawURL string) string {
	name := embedEsc(fileDisplayName(file))
	esc := embedEsc(rawURL)
	typ := file.Type

	var b strings.Builder
	b.WriteString(`<main class="card">`)
	b.WriteString(`<h1>` + name + `</h1>`)
	switch {
	case strings.HasPrefix(typ, "image"):
		b.WriteString(`<img class="media" src="` + esc + `" alt="` + name + `" />`)
	case strings.HasPrefix(typ, "video"):
		b.WriteString(`<video class="media" src="` + esc + `" controls playsinline></video>`)
	case strings.HasPrefix(typ, "audio"):
		b.WriteString(`<audio class="media" src="` + esc + `" controls></audio>`)
	case typ == "application/pdf", strings.HasPrefix(typ, "text"):
		b.WriteString(`<iframe class="media" src="` + esc + `" title="` + name + `"></iframe>`)
	default:
		b.WriteString(`<p class="muted">No inline preview is available for this file type.</p>`)
	}
	b.WriteString(`<div class="row"><a class="btn" href="` + esc + `" download>Download</a></div>`)
	b.WriteString(`</main>`)
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

// handleViewFile renders the view page for a file (GET /view/{id}).
//
// Password-protected files without a valid access token get only a password
// form (no file details, no media meta). Unlocked files get OG/Twitter meta for
// crawlers plus an inline viewer and download link for humans.
func (a *App) handleViewFile(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	file, err := a.Store.GetFileByName(r.Context(), id)
	if err != nil || file == nil || file.UserID == nil {
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

	// Folder protection (outer gate): a file inside a password-protected folder
	// sends the visitor to the folder gate first.
	if a.fileFolderBlocked(w, r, file) {
		return
	}

	token := r.URL.Query().Get("token")
	protected := file.Password != nil && *file.Password != ""
	unlocked := !protected || (token != "" && auth.VerifyAccessToken(token, "file", file.ID, a.Cfg.Core.Secret))

	// Locked: render only the password form.
	if !unlocked {
		var m embedMeta
		m.title("Password Protected")
		body := passwordFormBody(r.URL.Path, r.URL.Query().Get("error") != "")
		a.writeHTML(w, http.StatusOK, embedDoc(m.b.String(), body))
		return
	}

	base := a.BaseURL(r)
	rawURL := base + "/raw/" + file.Name
	if protected && token != "" {
		rawURL += "?token=" + url.QueryEscape(token)
	}
	viewURL := base + "/view/" + file.Name

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
		m.metaProperty("og:url", viewURL)
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
		m.metaProperty("og:url", viewURL)
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
		m.metaProperty("og:url", viewURL)
		m.metaProperty("og:audio", rawURL)
		m.metaProperty("og:audio:secure_url", rawURL)
		m.metaProperty("og:audio:type", typ)

	case showRichOg:
		// Any other type, with full embed enabled.
		m.metaProperty("og:url", viewURL)
	}

	// Title is always present: original name preferred, else stored name.
	m.title(fileDisplayName(file))

	a.logFor(r).Debug("view page rendered", "name", file.Name, "type", file.Type)
	a.writeHTML(w, http.StatusOK, embedDoc(m.b.String(), viewMediaBody(file, rawURL)))
}

// handleViewFilePassword verifies a posted password (POST /view/{id}). On
// success it issues a 5-minute file access token and redirects to the view page
// with ?token=...; on failure it redirects back with ?error=1.
func (a *App) handleViewFilePassword(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	file, err := a.Store.GetFileByName(r.Context(), id)
	if err != nil || file == nil {
		a.embedNotFound(w, http.StatusNotFound, "Not Found")
		return
	}
	if file.Password == nil || *file.Password == "" {
		http.Redirect(w, r, "/view/"+file.Name, http.StatusFound)
		return
	}

	_ = r.ParseForm()
	ok, _ := auth.VerifyPassword(*file.Password, r.PostFormValue("password"))
	if !ok {
		http.Redirect(w, r, r.URL.Path+"?error=1", http.StatusFound)
		return
	}

	token, terr := auth.CreateAccessToken("file", file.ID, a.Cfg.Core.Secret)
	if terr != nil {
		a.embedNotFound(w, http.StatusInternalServerError, "Error")
		return
	}
	http.Redirect(w, r, r.URL.Path+"?token="+url.QueryEscape(token), http.StatusFound)
}

// handleViewURL renders the view page for a short URL (GET /view/url/{id}).
//
// For a password-protected URL with no valid token we show only the password
// form and do not reveal the destination. Otherwise we increment the view
// counter and forward the visitor to the destination.
func (a *App) handleViewURL(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	u, err := a.lookupURL(r, id)
	if err != nil || u == nil || !u.Enabled {
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

	token := r.URL.Query().Get("token")
	protected := u.Password != nil && *u.Password != ""
	unlocked := !protected || (token != "" && auth.VerifyAccessToken(token, "url", u.ID, a.Cfg.Core.Secret))

	if !unlocked {
		var m embedMeta
		m.title("Password Protected")
		body := passwordFormBody(r.URL.Path, r.URL.Query().Get("error") != "")
		a.writeHTML(w, http.StatusOK, embedDoc(m.b.String(), body))
		return
	}

	// Valid (or no password): count the view and forward to the destination.
	if _, err := a.Store.IncrementURLViews(r.Context(), u.ID); err != nil {
		a.Log.Debug("embed: increment url views", "id", u.ID, "err", err)
	}

	var m embedMeta
	m.line(`<meta http-equiv="refresh" content="0; url=` + embedEsc(u.Destination) + `" />`)
	m.metaProperty("og:url", u.Destination)
	m.title(u.Code)
	body := `<main class="card center"><p>Redirecting to <a class="btn" href="` +
		embedEsc(u.Destination) + `">` + embedEsc(u.Destination) + `</a></p></main>`
	a.writeHTML(w, http.StatusOK, embedDoc(m.b.String(), body))
}

// handleViewURLPassword verifies a posted password for a short URL
// (POST /view/url/{id}). On success it issues a url access token and redirects
// to the view page with ?token=...; on failure it redirects back with ?error=1.
func (a *App) handleViewURLPassword(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	u, err := a.lookupURL(r, id)
	if err != nil || u == nil {
		a.embedNotFound(w, http.StatusNotFound, "Not Found")
		return
	}
	if u.Password == nil || *u.Password == "" {
		http.Redirect(w, r, r.URL.Path, http.StatusFound)
		return
	}

	_ = r.ParseForm()
	ok, _ := auth.VerifyPassword(*u.Password, r.PostFormValue("password"))
	if !ok {
		http.Redirect(w, r, r.URL.Path+"?error=1", http.StatusFound)
		return
	}

	token, terr := auth.CreateAccessToken("url", u.ID, a.Cfg.Core.Secret)
	if terr != nil {
		a.embedNotFound(w, http.StatusInternalServerError, "Error")
		return
	}
	http.Redirect(w, r, r.URL.Path+"?token="+url.QueryEscape(token), http.StatusFound)
}

// lookupURL resolves a short URL by id, code, or vanity. The /u redirect for a
// protected URL targets /view/url/{id} (by id), while GetURLByCode matches only
// code/vanity, so we query all three here.
func (a *App) lookupURL(r *http.Request, id string) (*models.Url, error) {
	u, err := a.Store.GetURLByCode(r.Context(), id)
	if err == nil && u != nil {
		return u, nil
	}
	row := a.Store.Pool.QueryRow(r.Context(),
		`SELECT id, code, vanity, destination, enabled, views, max_views, password
		   FROM urls WHERE id=$1 LIMIT 1`, id)
	var m models.Url
	if scanErr := row.Scan(&m.ID, &m.Code, &m.Vanity, &m.Destination, &m.Enabled,
		&m.Views, &m.MaxViews, &m.Password); scanErr != nil {
		return nil, scanErr
	}
	return &m, nil
}

// embedNotFound writes a tiny HTML document with the given status and message.
func (a *App) embedNotFound(w http.ResponseWriter, status int, msg string) {
	var m embedMeta
	m.title(msg)
	body := `<main class="card center"><h1>` + embedEsc(msg) + `</h1></main>`
	a.writeHTML(w, status, embedDoc(m.b.String(), body))
}

// writeHTML writes an HTML response with the given status.
func (a *App) writeHTML(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}
