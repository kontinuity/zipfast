package server

import (
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"

	"zipfast/internal/importer"
)

// impMaxImportBody caps the size of an uploaded export to a generous 2 GiB so a
// large instance dump can be ingested while still bounding memory use.
const impMaxImportBody = 2 << 30 // 2 GiB

// registerImportRoutes wires the admin-only Zipline data-import endpoints. Both
// accept a raw Zipline export JSON body and return an importer.Summary.
//
// NOTE: call this from mountFeatureRoutes (routes.go) during integration:
//
//	a.registerImportRoutes(r)
func (a *App) registerImportRoutes(r chi.Router) {
	r.Route("/api/server/import", func(ir chi.Router) {
		ir.Use(a.RequireAdmin)
		ir.Post("/v4", a.impHandleImportV4)
		ir.Post("/v3", a.impHandleImportV3)
	})
}

// impHandleImportV4 ingests a Zipline v4 export.
func (a *App) impHandleImportV4(w http.ResponseWriter, r *http.Request) {
	body, ok := a.impReadImportBody(w, r)
	if !ok {
		return
	}
	log := a.logFor(r)
	log.Info("import started", "version", "v4", "bytes", len(body))
	summary, err := importer.ImportV4(r.Context(), a.Store.Pool, body)
	if err != nil {
		log.Warn("import failed", "version", "v4", "err", err)
		a.Error(w, http.StatusBadRequest, "failed to parse v4 export: "+err.Error())
		return
	}
	log.Info("import finished", "version", "v4")
	a.WriteJSON(w, http.StatusOK, summary)
}

// impHandleImportV3 ingests a legacy Zipline v3 export.
func (a *App) impHandleImportV3(w http.ResponseWriter, r *http.Request) {
	body, ok := a.impReadImportBody(w, r)
	if !ok {
		return
	}
	log := a.logFor(r)
	log.Info("import started", "version", "v3", "bytes", len(body))
	summary, err := importer.ImportV3(r.Context(), a.Store.Pool, body)
	if err != nil {
		log.Warn("import failed", "version", "v3", "err", err)
		a.Error(w, http.StatusBadRequest, "failed to parse v3 export: "+err.Error())
		return
	}
	log.Info("import finished", "version", "v3")
	a.WriteJSON(w, http.StatusOK, summary)
}

// impReadImportBody reads the (potentially large) request body up to the import
// size cap. It writes an error response and returns ok=false on failure so the
// caller can simply return.
func (a *App) impReadImportBody(w http.ResponseWriter, r *http.Request) (body []byte, ok bool) {
	defer r.Body.Close()
	limited := http.MaxBytesReader(w, r.Body, impMaxImportBody)
	buf, err := io.ReadAll(limited)
	if err != nil {
		a.Error(w, http.StatusBadRequest, "failed to read import body: "+err.Error())
		return nil, false
	}
	if len(buf) == 0 {
		a.Error(w, http.StatusBadRequest, "empty import body")
		return nil, false
	}
	return buf, true
}
