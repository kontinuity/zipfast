package server

// static.go optionally serves the built single-page frontend (the Vite client
// ported from the original Zipline) directly from the Go binary. When a web
// directory is present it serves hashed assets with long-lived caching and falls
// back to index.html for client-side routes; the API and all reserved serving
// paths are never hijacked. It also exposes /config.js, a tiny script that tells
// the SPA which API origin to talk to at runtime.
//
// All identifiers here are prefixed with "static" to avoid collisions with the
// rest of the (parallel-edited) server package.

import (
	"encoding/json"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
)

// staticReservedPrefixes are request-path prefixes the SPA fallback must never
// claim: the API, the file/url serving surface, embed/meta pages, and the small
// well-known static endpoints. A request matching any of these is allowed to
// fall through to a real 404 instead of being answered with index.html.
//
// Note: the configured Files.Route and Urls.Route (defaults "/u" and "/go") are
// added dynamically in staticIsReserved since they are not known at init time.
var staticReservedPrefixes = []string{
	"/api",
	"/raw",
	"/r",
	"/view",
	"/go",
	"/favicon",
	"/robots.txt",
	"/manifest.json",
	"/config.js",
}

// staticWebDir resolves the directory containing the built SPA. It honours the
// ZIPFAST_WEB_DIR environment variable and defaults to "./web/dist".
func (a *App) staticWebDir() string {
	if dir := os.Getenv("ZIPFAST_WEB_DIR"); dir != "" {
		return dir
	}
	return "./web/dist"
}

// staticServingDisabled reports whether in-binary client serving is turned off.
// This is the "CDN option": when the SPA is hosted on a CDN, set ZIPFAST_CDN_URL
// (or ZIPFAST_DISABLE_WEB=true) and the server becomes API-only — the SPA fallback
// stops answering navigations so they don't shadow a 404.
func (a *App) staticServingDisabled() bool {
	if os.Getenv("ZIPFAST_CDN_URL") != "" {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("ZIPFAST_DISABLE_WEB"))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// registerStaticRoutes wires the runtime SPA configuration endpoint. The SPA
// fallback itself is installed separately as the router's NotFound handler
// (r.NotFound(a.spaFallback)).
func (a *App) registerStaticRoutes(r chi.Router) {
	r.Get("/config.js", a.staticServeConfigJS)
	r.Get("/manifest.json", a.staticServeManifest)
}

// staticServeManifest serves the PWA web app manifest. The vendored client
// references /manifest.json unconditionally, so we always return a valid manifest
// (built from the PWA / website config) rather than a 404.
func (a *App) staticServeManifest(w http.ResponseWriter, _ *http.Request) {
	name := staticFirstNonEmpty(a.Cfg.PWA.Title, a.Cfg.Website.Title, "Zipfast")
	short := staticFirstNonEmpty(a.Cfg.PWA.ShortName, name)
	manifest := map[string]any{
		"name":             name,
		"short_name":       short,
		"description":      a.Cfg.PWA.Description,
		"start_url":        "/",
		"scope":            "/",
		"display":          "standalone",
		"theme_color":      staticFirstNonEmpty(a.Cfg.PWA.ThemeColor, "#000000"),
		"background_color": staticFirstNonEmpty(a.Cfg.PWA.BackgroundColor, "#000000"),
		"icons":            []any{},
	}
	body, err := json.Marshal(manifest)
	if err != nil {
		a.Error(w, http.StatusInternalServerError, "could not build manifest")
		return
	}
	w.Header().Set("Content-Type", "application/manifest+json; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// staticFirstNonEmpty returns the first non-empty (after trimming) string.
func staticFirstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// staticServeConfigJS serves /config.js: a one-line script exposing the API base
// URL to the SPA at runtime. The base comes from ZIPFAST_PUBLIC_API_URL and may
// be empty, in which case the SPA talks to the same origin it was served from.
func (a *App) staticServeConfigJS(w http.ResponseWriter, _ *http.Request) {
	base := os.Getenv("ZIPFAST_PUBLIC_API_URL")
	cdn := os.Getenv("ZIPFAST_CDN_URL")
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=60")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(
		"window.__ZIPFAST_API__ = " + staticJSString(base) + ";\n" +
			"window.__ZIPFAST_CDN__ = " + staticJSString(cdn) + ";\n"))
}

// spaFallback is the chi NotFound handler. For navigable (GET/HEAD) requests
// that are not reserved it serves a matching static file from the web directory,
// or index.html for client-side routes. Everything else is left as a genuine
// 404 so the API surface keeps its JSON error contract.
func (a *App) spaFallback(w http.ResponseWriter, r *http.Request) {
	// Only GET/HEAD can be SPA navigations; anything else is a real 404.
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		a.Error(w, http.StatusNotFound, "not found")
		return
	}

	// CDN option: when client serving is disabled, the server is API-only and
	// unmatched routes are genuine 404s (the SPA lives on the CDN).
	if a.staticServingDisabled() {
		a.Error(w, http.StatusNotFound, "not found")
		return
	}

	reqPath := r.URL.Path

	// Upstream Zipline relies on the server sending the root to the dashboard; the
	// SPA has no index route for "/", so without this "/" renders the 404 catch-all.
	if reqPath == "/" {
		http.Redirect(w, r, "/dashboard", http.StatusFound)
		return
	}

	// Never hijack the API or any reserved serving path: let the real 404 stand.
	if a.staticIsReserved(reqPath) {
		a.Error(w, http.StatusNotFound, "not found")
		return
	}

	webDir := a.staticWebDir()

	// Try to serve a concrete file from the web directory (e.g. /assets/app.js).
	if full, ok := staticResolveFile(webDir, reqPath); ok {
		if staticIsImmutableAsset(reqPath) {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "public, max-age=300")
		}
		http.ServeFile(w, r, full)
		return
	}

	// SPA fallback: serve index.html for client-side routes (no caching so a new
	// deploy is picked up immediately).
	indexPath := filepath.Join(webDir, "index.html")
	if staticFileExists(indexPath) {
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFile(w, r, indexPath)
		return
	}

	// No build present: render a friendly placeholder instead of a bare 404.
	staticServeNotBuilt(w)
}

// staticIsReserved reports whether reqPath belongs to the API or one of the
// reserved serving paths (including the configured file/url routes), and so must
// not be answered by the SPA fallback.
func (a *App) staticIsReserved(reqPath string) bool {
	for _, p := range staticReservedPrefixes {
		if reqPath == p || strings.HasPrefix(reqPath, p+"/") {
			return true
		}
	}
	for _, p := range []string{a.Cfg.Files.Route, a.Cfg.Urls.Route} {
		if p == "" || p == "/" {
			continue
		}
		if reqPath == p || strings.HasPrefix(reqPath, p+"/") {
			return true
		}
	}
	return false
}

// staticResolveFile maps a request path to an existing regular file under webDir.
// It cleans the path and rejects traversal attempts; it returns ok=false for the
// root, directories, and missing files (so those flow to the index.html
// fallback).
func staticResolveFile(webDir, reqPath string) (full string, ok bool) {
	if webDir == "" {
		return "", false
	}
	// Reject obvious traversal before cleaning.
	if strings.Contains(reqPath, "..") {
		return "", false
	}

	clean := path.Clean("/" + reqPath)
	if clean == "/" {
		return "", false // root -> index.html fallback
	}
	// Guard again after cleaning.
	if clean == ".." || strings.HasPrefix(clean, "../") || strings.Contains(clean, "/../") {
		return "", false
	}

	full = filepath.Join(webDir, filepath.FromSlash(clean))

	// Ensure the resolved path stays within webDir.
	absDir, err1 := filepath.Abs(webDir)
	absFull, err2 := filepath.Abs(full)
	if err1 == nil && err2 == nil {
		if absFull != absDir && !strings.HasPrefix(absFull, absDir+string(os.PathSeparator)) {
			return "", false
		}
	}

	info, err := os.Stat(full)
	if err != nil || info.IsDir() {
		return "", false
	}
	return full, true
}

// staticIsImmutableAsset reports whether reqPath points at a build-hashed asset
// that is safe to cache forever (Vite emits these under /assets/).
func staticIsImmutableAsset(reqPath string) bool {
	return strings.HasPrefix(reqPath, "/assets/")
}

// staticFileExists reports whether p exists and is a regular file.
func staticFileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

// staticServeNotBuilt renders a small inline page (HTTP 200) explaining that the
// SPA has not been built yet and how to build it. Used only when no index.html
// is present in the web directory.
func staticServeNotBuilt(w http.ResponseWriter) {
	const page = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Zipfast</title>
</head>
<body style="font-family:system-ui,sans-serif;max-width:40rem;margin:4rem auto;padding:0 1rem;line-height:1.5">
<h1>Zipfast is running</h1>
<p>The API is up, but the web frontend has not been built into this binary yet.</p>
<p>To serve the SPA, build the Vite client from the original Zipline and drop the
output into <code>web/dist</code> (or point <code>ZIPFAST_WEB_DIR</code> at the
build directory).</p>
<p>The JSON API is available under <code>/api</code>.</p>
</body>
</html>
`
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(page))
}

// staticJSString renders s as a safe, double-quoted JavaScript string literal,
// escaping the characters that could break out of the string or the surrounding
// <script> context.
func staticJSString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '<':
			// Avoid accidentally closing a <script> tag (e.g. "</script>").
			b.WriteString(`<`)
		case '>':
			b.WriteString(`>`)
		case '&':
			b.WriteString(`&`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
