package server

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"zipfast/internal/models"
)

// srvCodeJSON is the syntax-highlighting code map (ext/mime/name) served to the
// client for the text-upload language picker. It must be a JSON array.
//
//go:embed code.json
var srvCodeJSON []byte

func srvCodeMap() json.RawMessage {
	if len(srvCodeJSON) == 0 {
		return json.RawMessage("[]")
	}
	return json.RawMessage(srvCodeJSON)
}

// registerServerRoutes wires the public/config/settings/themes/stats endpoints
// that power the SPA's login page, dashboard loader, theme picker and stats view.
func (a *App) registerServerRoutes(r chi.Router) {
	// Public config for the login page. No authentication.
	r.Get("/api/server/public", a.handleServerPublic)

	// Themes are safe to expose unauthenticated (the login page needs them).
	r.Get("/api/server/themes", a.handleServerThemes)

	// Web settings feed the dashboard loader; any authenticated user may read.
	r.With(a.RequireUser).Get("/api/server/settings/web", a.handleServerWebSettings)

	// Full effective settings + mutation are admin-only.
	r.With(a.RequireAdmin).Get("/api/server/settings", a.handleServerGetSettings)
	r.With(a.RequireAdmin).Patch("/api/server/settings", a.handleServerPatchSettings)

	// Live aggregate stats.
	r.With(a.RequireUser).Get("/api/stats", a.handleStats)
}

// --- public login-page config ---

func (a *App) handleServerPublic(w http.ResponseWriter, r *http.Request) {
	_, firstSetup, err := a.Store.LoadSettings(r.Context())
	if err != nil {
		// First-setup detection is best-effort; default to false on error.
		firstSetup = false
	}

	c := a.Cfg

	// Nullable fields are emitted as JSON null when unset (matches the client schema).
	var maxExpiration any
	if c.Files.MaxExpiration != "" {
		maxExpiration = c.Files.MaxExpiration
	}

	features := map[string]any{
		"oauthRegistration": c.Features.OAuthRegistration,
		"userRegistration":  c.Features.UserRegistration,
	}
	if c.Features.MetricsAdminOnly {
		features["metrics"] = map[string]any{"adminOnly": true}
	}

	domains := c.Domains
	if domains == nil {
		domains = []string{}
	}

	// Shape must match the SPA's expected public config (nested), see the original
	// /api/server/public contract.
	resp := map[string]any{
		"oauth": map[string]any{
			"bypassLocalLogin": c.OAuth.BypassLocalLogin,
			"loginOnly":        c.OAuth.LoginOnly,
		},
		"oauthEnabled": map[string]bool{
			"discord": c.OAuth.Discord.ClientID != "",
			"github":  c.OAuth.Github.ClientID != "",
			"google":  c.OAuth.Google.ClientID != "",
			"oidc":    c.OAuth.OIDC.ClientID != "",
		},
		"website": map[string]any{
			"loginBackground":     nil,
			"loginBackgroundBlur": true,
			"title":               c.Website.Title,
			"tos":                 false,
		},
		"features": features,
		"mfa": map[string]any{
			"passkeys": c.MFA.PasskeysEnabled && c.MFA.PasskeysRPID != "" && c.MFA.PasskeysOrigin != "",
		},
		"files": map[string]any{
			"maxFileSize":   c.Files.MaxFileSize,
			"defaultFormat": c.Files.DefaultFormat,
			"maxExpiration": maxExpiration,
		},
		"chunks": map[string]any{
			"enabled": c.Chunks.Enabled,
			"max":     c.Chunks.Max,
			"size":    c.Chunks.Size,
		},
		"firstSetup":  firstSetup,
		"domains":     domains,
		"returnHttps": c.Core.ReturnHTTPSURLs,
	}
	a.WriteJSON(w, http.StatusOK, resp)
}

// --- settings ---

// srvEffectiveSettings builds the effective settings object from config-derived
// defaults overlaid with the stored JSONB blob. The shape mirrors the Zipline
// settings contract closely enough for the SPA's loaders.
func (a *App) srvEffectiveSettings(ctx context.Context) map[string]any {
	base := a.srvDefaultSettings()

	if data, _, err := a.Store.LoadSettings(ctx); err == nil && len(data) > 0 {
		var stored map[string]any
		if json.Unmarshal(data, &stored) == nil {
			srvMergeMaps(base, stored)
		}
	}
	return base
}

// srvDefaultSettings derives the effective dashboard config from the loaded config.
// The shape mirrors Zipline's safeConfig(config) exactly (the full Config minus
// secret sections, plus oauthEnabled/oauth/version) so every client `config.X.Y`
// access resolves. Env vars win at boot; the stored JSON overlays this.
func (a *App) srvDefaultSettings() map[string]any {
	c := a.Cfg

	strOrNil := func(s string) any {
		if s == "" {
			return nil
		}
		return s
	}
	emptyStrSlice := func(s []string) []string {
		if s == nil {
			return []string{}
		}
		return s
	}
	domains := emptyStrSlice(c.Domains)

	return map[string]any{
		"chunks": map[string]any{
			"max":     c.Chunks.Max,
			"size":    c.Chunks.Size,
			"enabled": c.Chunks.Enabled,
		},
		"tasks": map[string]any{
			"deleteInterval":          srvDurString(c.Tasks.DeleteInterval),
			"clearInvitesInterval":    srvDurString(c.Tasks.ClearInvitesInterval),
			"maxViewsInterval":        srvDurString(c.Tasks.MaxViewsInterval),
			"thumbnailsInterval":      srvDurString(c.Tasks.ThumbnailsInterval),
			"metricsInterval":         srvDurString(c.Tasks.MetricsInterval),
			"cleanThumbnailsInterval": srvDurString(c.Tasks.CleanThumbnailsInterval),
		},
		"files": map[string]any{
			"route":                    c.Files.Route,
			"length":                   c.Files.Length,
			"defaultFormat":            c.Files.DefaultFormat,
			"disabledTypes":            []string{},
			"disabledTypesDefault":     nil,
			"disabledExtensions":       emptyStrSlice(c.Files.DisabledExtensions),
			"maxFileSize":              c.Files.MaxFileSize,
			"defaultExpiration":        strOrNil(c.Files.DefaultExpiration),
			"maxExpiration":            strOrNil(c.Files.MaxExpiration),
			"assumeMimetypes":          c.Files.AssumeMimetypes,
			"defaultDateFormat":        c.Files.DefaultDateFormat,
			"removeGpsMetadata":        c.Files.RemoveGPSMetadata,
			"randomWordsNumAdjectives": c.Files.RandomWordsNumAdjectives,
			"randomWordsSeparator":     c.Files.RandomWordsSeparator,
			"defaultCompressionFormat": c.Files.DefaultCompressionFormat,
			"maxFilesPerUpload":        c.Files.MaxFilesPerUpload,
			"extensionlessUrls":        c.Files.ExtensionlessUrls,
		},
		"urls": map[string]any{
			"route":  c.Urls.Route,
			"length": c.Urls.Length,
		},
		"datasource": map[string]any{
			"type": c.Datasource.Type,
		},
		"features": map[string]any{
			"imageCompression":  c.Features.ImageCompression,
			"robotsTxt":         c.Features.RobotsTxt,
			"healthcheck":       c.Features.Healthcheck,
			"userRegistration":  c.Features.UserRegistration,
			"oauthRegistration": c.Features.OAuthRegistration,
			"deleteOnMaxViews":  c.Features.DeleteOnMaxViews,
			"thumbnails": map[string]any{
				"enabled":       c.Features.ThumbnailsEnabled,
				"num_threads":   c.Features.ThumbnailsThreads,
				"format":        c.Features.ThumbnailsFormat,
				"instantaneous": c.Features.ThumbnailsInstant,
			},
			"metrics": map[string]any{
				"enabled":          c.Features.MetricsEnabled,
				"adminOnly":        c.Features.MetricsAdminOnly,
				"showUserSpecific": c.Features.MetricsShowUserSpec,
			},
			"versionChecking": c.Features.VersionChecking,
			"versionAPI":      "https://zipline-version.diced.sh/",
		},
		"domains": domains,
		"invites": map[string]any{
			"enabled": c.Invites.Enabled,
			"length":  c.Invites.Length,
		},
		"website": map[string]any{
			"title":     c.Website.Title,
			"titleLogo": nil,
			"externalLinks": []map[string]any{
				{"name": "GitHub", "url": "https://github.com/diced/zipline"},
				{"name": "Documentation", "url": "https://zipline.diced.sh"},
			},
			"loginBackground":     nil,
			"loginBackgroundBlur": true,
			"defaultAvatar":       nil,
			"theme": map[string]any{
				"default": c.Website.ThemeDefault,
				"dark":    c.Website.ThemeDark,
				"light":   c.Website.ThemeLight,
			},
			"tos": nil,
		},
		"mfa": map[string]any{
			"totp": map[string]any{
				"enabled": c.MFA.TotpEnabled,
				"issuer":  c.MFA.TotpIssuer,
			},
			"passkeys": map[string]any{
				"enabled": c.MFA.PasskeysEnabled,
				"rpID":    strOrNil(c.MFA.PasskeysRPID),
				"origin":  strOrNil(c.MFA.PasskeysOrigin),
			},
		},
		"pwa": map[string]any{
			"enabled":         c.PWA.Enabled,
			"title":           c.PWA.Title,
			"shortName":       c.PWA.ShortName,
			"description":     c.PWA.Description,
			"themeColor":      c.PWA.ThemeColor,
			"backgroundColor": c.PWA.BackgroundColor,
		},
		"oauthEnabled": map[string]bool{
			"discord": c.OAuth.Discord.ClientID != "",
			"github":  c.OAuth.Github.ClientID != "",
			"google":  c.OAuth.Google.ClientID != "",
			"oidc":    c.OAuth.OIDC.ClientID != "",
		},
		"oauth": map[string]any{
			"bypassLocalLogin": c.OAuth.BypassLocalLogin,
			"loginOnly":        c.OAuth.LoginOnly,
		},
		"version": a.Version,
	}
}

// srvDurString renders a duration in a compact, ms-parseable form (e.g. "30m", "1d").
func srvDurString(d time.Duration) string {
	switch {
	case d == 0:
		return "0"
	case d%(24*time.Hour) == 0:
		return fmt.Sprintf("%dd", d/(24*time.Hour))
	case d%time.Hour == 0:
		return fmt.Sprintf("%dh", d/time.Hour)
	case d%time.Minute == 0:
		return fmt.Sprintf("%dm", d/time.Minute)
	default:
		return fmt.Sprintf("%ds", d/time.Second)
	}
}

// srvFlatSettings builds the FLAT Zipline settings-row shape consumed by the admin
// "Server Settings" pages. The admin SPA reads each value as data.settings.<flatKey>
// (the Prisma `Zipline` model column names, camelCased), so this returns every one of
// those columns: config-derived defaults overlaid with the stored JSONB blob so that
// admin-saved values round-trip. Nullable columns are emitted as JSON null when empty.
//
// This is intentionally separate from srvEffectiveSettings/srvDefaultSettings (the
// nested safeConfig shape used by /api/server/settings/web, which must not change).
func (a *App) srvFlatSettings(ctx context.Context) map[string]any {
	c := a.Cfg

	strOrNil := func(s string) any {
		if s == "" {
			return nil
		}
		return s
	}
	emptyStrSlice := func(s []string) []string {
		if s == nil {
			return []string{}
		}
		return s
	}

	flat := map[string]any{
		// core
		"coreReturnHttpsUrls": c.Core.ReturnHTTPSURLs,
		"coreDefaultDomain":   strOrNil(c.Core.DefaultDomain),
		"coreTempDirectory":   c.Core.TempDirectory,
		"coreTrustProxy":      c.Core.TrustProxy,

		// chunks
		"chunksEnabled": c.Chunks.Enabled,
		"chunksMax":     c.Chunks.Max,
		"chunksSize":    c.Chunks.Size,

		// tasks
		"tasksDeleteInterval":          srvDurString(c.Tasks.DeleteInterval),
		"tasksClearInvitesInterval":    srvDurString(c.Tasks.ClearInvitesInterval),
		"tasksMaxViewsInterval":        srvDurString(c.Tasks.MaxViewsInterval),
		"tasksThumbnailsInterval":      srvDurString(c.Tasks.ThumbnailsInterval),
		"tasksMetricsInterval":         srvDurString(c.Tasks.MetricsInterval),
		"tasksCleanThumbnailsInterval": srvDurString(c.Tasks.CleanThumbnailsInterval),

		// files
		"filesRoute":                    c.Files.Route,
		"filesLength":                   c.Files.Length,
		"filesDefaultFormat":            c.Files.DefaultFormat,
		"filesDisabledTypes":            []string{},
		"filesDisabledTypesDefault":     nil,
		"filesDisabledExtensions":       emptyStrSlice(c.Files.DisabledExtensions),
		"filesMaxFileSize":              c.Files.MaxFileSize,
		"filesDefaultExpiration":        strOrNil(c.Files.DefaultExpiration),
		"filesMaxExpiration":            strOrNil(c.Files.MaxExpiration),
		"filesAssumeMimetypes":          c.Files.AssumeMimetypes,
		"filesDefaultDateFormat":        c.Files.DefaultDateFormat,
		"filesRemoveGpsMetadata":        c.Files.RemoveGPSMetadata,
		"filesRandomWordsNumAdjectives": c.Files.RandomWordsNumAdjectives,
		"filesRandomWordsSeparator":     c.Files.RandomWordsSeparator,
		"filesDefaultCompressionFormat": c.Files.DefaultCompressionFormat,
		"filesMaxFilesPerUpload":        c.Files.MaxFilesPerUpload,
		"filesExtensionlessUrls":        c.Files.ExtensionlessUrls,

		// urls
		"urlsRoute":  c.Urls.Route,
		"urlsLength": c.Urls.Length,

		// features
		"featuresImageCompression":        c.Features.ImageCompression,
		"featuresRobotsTxt":               c.Features.RobotsTxt,
		"featuresHealthcheck":             c.Features.Healthcheck,
		"featuresUserRegistration":        c.Features.UserRegistration,
		"featuresOauthRegistration":       c.Features.OAuthRegistration,
		"featuresDeleteOnMaxViews":        c.Features.DeleteOnMaxViews,
		"featuresThumbnailsEnabled":       c.Features.ThumbnailsEnabled,
		"featuresThumbnailsNumberThreads": c.Features.ThumbnailsThreads,
		"featuresThumbnailsFormat":        c.Features.ThumbnailsFormat,
		"featuresThumbnailsInstantaneous": c.Features.ThumbnailsInstant,
		"featuresMetricsEnabled":          c.Features.MetricsEnabled,
		"featuresMetricsAdminOnly":        c.Features.MetricsAdminOnly,
		"featuresMetricsShowUserSpecific": c.Features.MetricsShowUserSpec,
		"featuresVersionChecking":         c.Features.VersionChecking,
		"featuresVersionAPI":              "https://zipline-version.diced.sh",

		// invites
		"invitesEnabled": c.Invites.Enabled,
		"invitesLength":  c.Invites.Length,

		// website
		"websiteTitle":     c.Website.Title,
		"websiteTitleLogo": nil,
		"websiteExternalLinks": []map[string]any{
			{"name": "GitHub", "url": "https://github.com/diced/zipline"},
			{"name": "Documentation", "url": "https://zipline.diced.sh/"},
		},
		"websiteLoginBackground":     nil,
		"websiteLoginBackgroundBlur": true,
		"websiteDefaultAvatar":       nil,
		"websiteTos":                 nil,
		"websiteThemeDefault":        c.Website.ThemeDefault,
		"websiteThemeDark":           c.Website.ThemeDark,
		"websiteThemeLight":          c.Website.ThemeLight,

		// oauth
		"oauthBypassLocalLogin": c.OAuth.BypassLocalLogin,
		"oauthLoginOnly":        c.OAuth.LoginOnly,

		"oauthDiscordClientId":     strOrNil(c.OAuth.Discord.ClientID),
		"oauthDiscordClientSecret": strOrNil(c.OAuth.Discord.ClientSecret),
		"oauthDiscordRedirectUri":  strOrNil(c.OAuth.Discord.RedirectURI),
		"oauthDiscordAllowedIds":   emptyStrSlice(c.OAuth.Discord.AllowedIDs),
		"oauthDiscordDeniedIds":    emptyStrSlice(c.OAuth.Discord.DeniedIDs),

		"oauthGoogleClientId":     strOrNil(c.OAuth.Google.ClientID),
		"oauthGoogleClientSecret": strOrNil(c.OAuth.Google.ClientSecret),
		"oauthGoogleRedirectUri":  strOrNil(c.OAuth.Google.RedirectURI),

		"oauthGithubClientId":     strOrNil(c.OAuth.Github.ClientID),
		"oauthGithubClientSecret": strOrNil(c.OAuth.Github.ClientSecret),
		"oauthGithubRedirectUri":  strOrNil(c.OAuth.Github.RedirectURI),

		"oauthOidcClientId":     strOrNil(c.OAuth.OIDC.ClientID),
		"oauthOidcClientSecret": strOrNil(c.OAuth.OIDC.ClientSecret),
		"oauthOidcAuthorizeUrl": strOrNil(c.OAuth.OIDC.AuthorizeURL),
		"oauthOidcTokenUrl":     strOrNil(c.OAuth.OIDC.TokenURL),
		"oauthOidcUserinfoUrl":  strOrNil(c.OAuth.OIDC.UserinfoURL),
		"oauthOidcRedirectUri":  strOrNil(c.OAuth.OIDC.RedirectURI),

		// mfa
		"mfaTotpEnabled":     c.MFA.TotpEnabled,
		"mfaTotpIssuer":      c.MFA.TotpIssuer,
		"mfaPasskeysEnabled": c.MFA.PasskeysEnabled,
		"mfaPasskeysRpID":    strOrNil(c.MFA.PasskeysRPID),
		"mfaPasskeysOrigin":  strOrNil(c.MFA.PasskeysOrigin),

		// ratelimit
		"ratelimitEnabled":     c.Ratelimit.Enabled,
		"ratelimitMax":         c.Ratelimit.Max,
		"ratelimitWindow":      nil,
		"ratelimitAdminBypass": c.Ratelimit.AdminBypass,
		"ratelimitAllowList":   emptyStrSlice(c.Ratelimit.AllowList),

		// http webhooks
		"httpWebhookOnUpload":  strOrNil(c.Webhooks.HTTPOnUpload),
		"httpWebhookOnShorten": strOrNil(c.Webhooks.HTTPOnShorten),

		// discord webhooks
		"discordWebhookUrl": nil,
		"discordUsername":   nil,
		"discordAvatarUrl":  nil,

		"discordOnUploadWebhookUrl": strOrNil(c.Webhooks.DiscordOnUploadWebhookURL),
		"discordOnUploadUsername":   nil,
		"discordOnUploadAvatarUrl":  nil,
		"discordOnUploadContent":    nil,
		"discordOnUploadEmbed":      nil,

		"discordOnShortenWebhookUrl": strOrNil(c.Webhooks.DiscordOnShortenWebhookURL),
		"discordOnShortenUsername":   nil,
		"discordOnShortenAvatarUrl":  nil,
		"discordOnShortenContent":    nil,
		"discordOnShortenEmbed":      nil,

		// pwa
		"pwaEnabled":         c.PWA.Enabled,
		"pwaTitle":           c.PWA.Title,
		"pwaShortName":       c.PWA.ShortName,
		"pwaDescription":     c.PWA.Description,
		"pwaThemeColor":      c.PWA.ThemeColor,
		"pwaBackgroundColor": c.PWA.BackgroundColor,

		// domains
		"domains": emptyStrSlice(c.Domains),
	}

	// Snapshot env-overridden (tampered) keys: their effective values come from
	// a.Cfg (env wins) and must not be replaced by the DB blob below.
	tampered := a.tamperedList()
	tsnap := make(map[string]any, len(tampered))
	for _, k := range tampered {
		if v, ok := flat[k]; ok {
			tsnap[k] = v
		}
	}

	// Overlay the stored JSONB blob so admin-saved values round-trip — including
	// settings not modeled in Config (e.g. external links, discord embeds). The
	// blob holds flat keys (the PATCH body shape); replace wholesale per key.
	if data, _, err := a.Store.LoadSettings(ctx); err == nil && len(data) > 0 {
		var stored map[string]any
		if json.Unmarshal(data, &stored) == nil {
			for k, v := range stored {
				flat[k] = v
			}
		}
	}

	// Restore env-winning values for tampered keys.
	for k, v := range tsnap {
		flat[k] = v
	}

	return flat
}

func (a *App) handleServerWebSettings(w http.ResponseWriter, r *http.Request) {
	a.WriteJSON(w, http.StatusOK, map[string]any{
		"config":  a.srvEffectiveSettings(r.Context()),
		"codeMap": srvCodeMap(),
	})
}

func (a *App) handleServerGetSettings(w http.ResponseWriter, r *http.Request) {
	a.WriteJSON(w, http.StatusOK, map[string]any{
		"settings": a.srvFlatSettings(r.Context()),
		"tampered": a.tamperedList(),
	})
}

// tamperedList returns the env-overridden setting keys (never nil, so the client
// always receives a JSON array it can use to lock those inputs).
func (a *App) tamperedList() []string {
	if a.Tampered == nil {
		return []string{}
	}
	return a.Tampered
}

func (a *App) handleServerPatchSettings(w http.ResponseWriter, r *http.Request) {
	var patch map[string]any
	if err := a.ReadJSON(r, &patch); err != nil {
		a.Error(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	data, firstSetup, err := a.Store.LoadSettings(r.Context())
	if err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to load settings")
		return
	}

	stored := map[string]any{}
	if len(data) > 0 {
		_ = json.Unmarshal(data, &stored)
	}

	// Merge the request's (flat) top-level keys into the stored blob.
	srvMergeMaps(stored, patch)

	out, err := json.Marshal(stored)
	if err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to encode settings")
		return
	}
	if err := a.Store.SaveSettings(r.Context(), out, firstSetup); err != nil {
		a.Error(w, http.StatusInternalServerError, "failed to save settings")
		return
	}

	// Apply the saved settings to the live config so the change takes effect
	// immediately (no restart), exactly like Zipline.
	a.ReloadSettings(r.Context())

	// Return the same { settings, tampered } shape as GET, with the new blob applied.
	a.WriteJSON(w, http.StatusOK, map[string]any{
		"settings": a.srvFlatSettings(r.Context()),
		"tampered": a.tamperedList(),
	})
}

// srvMergeMaps recursively merges src into dst (src wins). Nested maps are merged
// key-by-key; all other values (including slices) replace wholesale.
func srvMergeMaps(dst, src map[string]any) {
	for k, sv := range src {
		if sm, ok := sv.(map[string]any); ok {
			if dm, ok := dst[k].(map[string]any); ok {
				srvMergeMaps(dm, sm)
				continue
			}
		}
		dst[k] = sv
	}
}

// --- themes ---

// srvTheme is a minimal theme descriptor for the SPA's theme picker.
type srvTheme struct {
	ID                  string `json:"id"`
	Name                string `json:"name"`
	ColorScheme         string `json:"colorScheme"`
	MainBackgroundColor string `json:"mainBackgroundColor"`
}

func (a *App) handleServerThemes(w http.ResponseWriter, r *http.Request) {
	a.WriteJSON(w, http.StatusOK, map[string]any{
		"themes": []srvTheme{
			{
				ID:                  "builtin:dark_gray",
				Name:                "Dark Gray",
				ColorScheme:         "dark",
				MainBackgroundColor: "#0a0a0a",
			},
			{
				ID:                  "builtin:light_gray",
				Name:                "Light Gray",
				ColorScheme:         "light",
				MainBackgroundColor: "#fafafa",
			},
		},
		// The client expects an object ({default,dark,light}), not a bare string.
		"defaultTheme": map[string]any{
			"default": a.Cfg.Website.ThemeDefault,
			"dark":    a.Cfg.Website.ThemeDark,
			"light":   a.Cfg.Website.ThemeLight,
		},
	})
}

// --- stats ---

// handleStats serves /api/stats. It returns the latest metric snapshot plus a
// time-series of points from the metrics table over the requested range, matching
// the original { latest: Metric|null, points: MetricsPoint[] } contract.
func (a *App) handleStats(w http.ResponseWriter, r *http.Request) {
	if !a.Cfg.Features.MetricsEnabled {
		a.Error(w, http.StatusForbidden, "metrics are disabled")
		return
	}
	if a.Cfg.Features.MetricsAdminOnly {
		u := UserFromContext(r.Context())
		if u == nil || models.RoleRank(u.Role) < models.RoleRank(models.RoleAdmin) {
			a.Error(w, http.StatusForbidden, "forbidden")
			return
		}
	}

	ctx := r.Context()
	q := r.URL.Query()
	all := q.Get("all") == "true"

	now := time.Now()
	fromDate := now.AddDate(0, 0, -7) // default: last 7 days
	toDate := now
	if v := q.Get("from"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			fromDate = t
		}
	}
	if v := q.Get("to"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			toDate = t
		}
	}
	if !all && fromDate.After(toDate) {
		a.Error(w, http.StatusBadRequest, "the from date is after the to date")
		return
	}

	a.WriteJSON(w, http.StatusOK, map[string]any{
		"latest": a.statsLatest(ctx, all, fromDate, toDate),
		"points": a.statsPoints(ctx, all, fromDate, toDate),
	})
}

// statsLatest returns the most recent metric (within range unless all), or nil.
func (a *App) statsLatest(ctx context.Context, all bool, from, to time.Time) map[string]any {
	query := `SELECT id, created_at, updated_at, data FROM metrics`
	var args []any
	if !all {
		query += ` WHERE created_at >= $1 AND created_at <= $2`
		args = []any{from, to}
	}
	query += ` ORDER BY created_at DESC LIMIT 1`

	var (
		id                   string
		createdAt, updatedAt time.Time
		data                 []byte
	)
	if err := a.Store.Pool.QueryRow(ctx, query, args...).Scan(&id, &createdAt, &updatedAt, &data); err != nil {
		return nil
	}
	if len(data) == 0 {
		data = []byte("{}")
	}
	return map[string]any{
		"id":        id,
		"createdAt": createdAt,
		"updatedAt": updatedAt,
		"data":      json.RawMessage(data),
	}
}

// statsPoints returns the metric time-series (projected to the point shape) over
// the range, newest first. COALESCE keeps it resilient to older snapshot shapes.
func (a *App) statsPoints(ctx context.Context, all bool, from, to time.Time) []map[string]any {
	query := `SELECT id, created_at,
		COALESCE((data->>'users')::int, 0),
		COALESCE((data->>'files')::int, 0),
		COALESCE((data->>'fileViews')::int, 0),
		COALESCE((data->>'urls')::int, 0),
		COALESCE((data->>'urlViews')::int, 0),
		COALESCE((data->>'storage')::bigint, 0)
	FROM metrics`
	var args []any
	if !all {
		query += ` WHERE created_at >= $1 AND created_at <= $2`
		args = []any{from, to}
	}
	query += ` ORDER BY created_at DESC`

	out := []map[string]any{}
	rows, err := a.Store.Pool.Query(ctx, query, args...)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id                                      string
			createdAt                               time.Time
			users, files, fileViews, urls, urlViews int
			storage                                 int64
		)
		if err := rows.Scan(&id, &createdAt, &users, &files, &fileViews, &urls, &urlViews, &storage); err != nil {
			return out
		}
		out = append(out, map[string]any{
			"id": id, "createdAt": createdAt,
			"users": users, "files": files, "fileViews": fileViews,
			"urls": urls, "urlViews": urlViews, "storage": storage,
		})
	}
	return out
}
