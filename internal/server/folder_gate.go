package server

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"zipfast/internal/auth"
	"zipfast/internal/models"
)

// folder_gate.go implements server-rendered password protection for public
// folders. A protected folder's listing page (/folder/{id}) shows a password
// form; on success we mint a longer-lived "folder" access token, store it in an
// HttpOnly cookie, and let the normal SPA folder page load. The folder listing
// API (sactFolder) enforces the same token, so the listing can't be read without
// unlocking first.
//
// This protects the folder LISTING. Individual files keep their own (separate)
// password and remain reachable by their direct random URL if shared.

// folderTokenTTL is how long a folder unlock lasts. Longer than the 5-minute
// file/url access token because a visitor browses and paginates a folder.
const folderTokenTTL = 12 * time.Hour

// folderCookieName is the per-folder unlock cookie. Folder ids are cuids
// (alphanumeric), so they are always valid cookie-name characters.
func folderCookieName(folderID string) string { return "zf_folder_" + folderID }

// folderTokenValid reports whether the request carries a valid unlock token for
// the folder, via the unlock cookie or a ?token= query parameter.
func (a *App) folderTokenValid(r *http.Request, folderID string) bool {
	if c, err := r.Cookie(folderCookieName(folderID)); err == nil && c.Value != "" {
		if auth.VerifyAccessToken(c.Value, "folder", folderID, a.Cfg.Core.Secret) {
			return true
		}
	}
	if t := r.URL.Query().Get("token"); t != "" {
		if auth.VerifyAccessToken(t, "folder", folderID, a.Cfg.Core.Secret) {
			return true
		}
	}
	return false
}

// gateFolder is the minimal folder record the gate needs.
type gateFolder struct {
	ID           string
	Name         string
	Public       bool
	AllowUploads bool
	Password     *string
}

func (f *gateFolder) protected() bool { return f.Password != nil && *f.Password != "" }

// lookupGateFolder resolves a folder by id or name for the gate.
func (a *App) lookupGateFolder(ctx context.Context, id string) (*gateFolder, error) {
	var f gateFolder
	err := a.Store.Pool.QueryRow(ctx,
		`SELECT id, name, public, allow_uploads, password
		   FROM folders WHERE id=$1 OR name=$1 LIMIT 1`, id).
		Scan(&f.ID, &f.Name, &f.Public, &f.AllowUploads, &f.Password)
	if err != nil {
		return nil, err
	}
	return &f, nil
}

// handleFolderGate serves GET /folder/{id}. For a protected, not-yet-unlocked
// folder it renders the password form; otherwise it serves the SPA so the normal
// public folder page loads.
func (a *App) handleFolderGate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	f, err := a.lookupGateFolder(r.Context(), id)
	if err != nil || f == nil || (!f.Public && !f.AllowUploads) || !f.protected() || a.folderTokenValid(r, f.ID) {
		// Not found / not viewable / not protected / already unlocked: let the SPA
		// render (it will show the folder or its own not-found state).
		a.serveSPA(w, r)
		return
	}

	a.logFor(r).Debug("folder gate: locked, showing password form", "folder", f.ID)
	var m embedMeta
	m.title("Password Protected")
	body := passwordFormBody(r.URL.Path, r.URL.Query().Get("error") != "")
	a.writeHTML(w, http.StatusOK, embedDoc(m.b.String(), body))
}

// handleFolderGatePassword verifies a posted password (POST /folder/{id}). On
// success it sets the unlock cookie and redirects to the folder page; on failure
// it redirects back with ?error=1.
func (a *App) handleFolderGatePassword(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	f, err := a.lookupGateFolder(r.Context(), id)
	if err != nil || f == nil || !f.protected() {
		http.Redirect(w, r, "/folder/"+id, http.StatusFound)
		return
	}

	_ = r.ParseForm()
	ok, _ := auth.VerifyPassword(*f.Password, r.PostFormValue("password"))
	if !ok {
		a.logFor(r).Debug("folder gate: wrong password", "folder", f.ID)
		http.Redirect(w, r, r.URL.Path+"?error=1", http.StatusFound)
		return
	}

	token, terr := auth.CreateAccessTokenTTL("folder", f.ID, a.Cfg.Core.Secret, folderTokenTTL)
	if terr != nil {
		a.embedNotFound(w, http.StatusInternalServerError, "Error")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     folderCookieName(f.ID),
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   requestIsHTTPS(r),
		MaxAge:   int(folderTokenTTL.Seconds()),
	})
	a.logFor(r).Info("folder unlocked", "folder", f.ID)
	http.Redirect(w, r, "/folder/"+f.ID, http.StatusFound)
}

// handleFolderUploadGate serves GET /folder/{id}/upload. If the folder is
// protected and not unlocked, send the visitor to the gate first; otherwise let
// the SPA render the upload page.
func (a *App) handleFolderUploadGate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	f, err := a.lookupGateFolder(r.Context(), id)
	if err == nil && f != nil && f.protected() && !a.folderTokenValid(r, f.ID) {
		http.Redirect(w, r, "/folder/"+f.ID, http.StatusFound)
		return
	}
	a.serveSPA(w, r)
}

// fileFolderProtected reports the file's folder id and whether that folder is
// password-protected. Returns ("", false) when the file is not in a folder or
// the folder lookup fails.
func (a *App) fileFolderProtected(ctx context.Context, file *models.File) (string, bool) {
	if file.FolderID == nil || *file.FolderID == "" {
		return "", false
	}
	var pw *string
	if err := a.Store.Pool.QueryRow(ctx,
		`SELECT password FROM folders WHERE id=$1`, *file.FolderID).Scan(&pw); err != nil {
		return *file.FolderID, false
	}
	return *file.FolderID, pw != nil && *pw != ""
}

// fileFolderBlocked enforces folder-level protection for a file. If the file's
// folder is password-protected and the request lacks a valid folder token, it
// renders a short notice telling the visitor the file lives in a protected
// folder (with a link to open the folder gate) and returns true (the caller must
// stop). This is the "gate direct file access" path: even with a direct /raw,
// /u, or /view URL, a file inside a locked folder requires unlocking the folder
// first. The notice replaces an earlier auto-redirect to /folder/{id}, which
// landed on the SPA's generic 404 for protected (non-public) folders.
func (a *App) fileFolderBlocked(w http.ResponseWriter, r *http.Request, file *models.File) bool {
	fid, prot := a.fileFolderProtected(r.Context(), file)
	if prot && !a.folderTokenValid(r, fid) {
		a.logFor(r).Debug("file access gated by folder password", "folder", fid, "name", file.Name)
		// Served as 200 with an explanatory page rather than a redirect/error so the
		// message renders directly on the file URL and isn't swallowed by the SPA.
		var m embedMeta
		m.title("Protected Folder")
		a.writeHTML(w, http.StatusOK, embedDoc(m.b.String(), folderProtectedBody(fid)))
		return true
	}
	return false
}

// serveSPA renders the single-page app shell (index.html) for a client route by
// delegating to the static fallback handler.
func (a *App) serveSPA(w http.ResponseWriter, r *http.Request) {
	a.spaFallback(w, r)
}

// requestIsHTTPS reports whether the original request was over TLS, honouring a
// reverse proxy's X-Forwarded-Proto so the unlock cookie gets the Secure flag in
// production deployments behind Traefik/nginx.
func requestIsHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}
