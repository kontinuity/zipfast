package server

import "github.com/go-chi/chi/v5"

// mountFeatureRoutes wires the feature route groups. It is the single integration
// point: as handler files are added (auth, user, upload, serve, server/admin),
// their register* methods are called here. Kept empty in the foundation build.
func (a *App) mountFeatureRoutes(r chi.Router) {
	a.registerAuthRoutes(r)
	a.registerOAuthRoutes(r)
	a.registerMfaRoutes(r)
	a.registerUserRoutes(r)
	a.registerUserExtraRoutes(r)
	a.registerExportRoutes(r)
	a.registerAdminRoutes(r)
	a.registerImportRoutes(r)
	a.registerServerRoutes(r)
	a.registerServerActionRoutes(r)
	a.registerUploadRoutes(r)
	a.registerServeRoutes(r)
	a.registerStaticRoutes(r)
}
