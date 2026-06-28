// Package config loads Zipfast configuration. Core values (port, secret, database,
// datasource) come from the environment. The remaining settings mirror the Zipline
// "Zipline" settings row: they have schema defaults, may be persisted in the DB, and
// can be overridden by environment variables (env wins).
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Core       Core
	Datasource Datasource
	Chunks     Chunks
	Tasks      Tasks
	Files      Files
	Urls       Urls
	Features   Features
	Invites    Invites
	Website    Website
	MFA        MFA
	OAuth      OAuth
	Ratelimit  Ratelimit
	Webhooks   Webhooks
	PWA        PWA
	Domains    []string
}

type Core struct {
	Port            int
	Hostname        string
	Secret          string
	DatabaseURL     string
	ReturnHTTPSURLs bool
	DefaultDomain   string
	TempDirectory   string
	TrustProxy      bool
}

type Datasource struct {
	Type  string // "local" | "s3"
	Local LocalDS
	S3    S3DS
}

type LocalDS struct {
	Directory string
}

type S3DS struct {
	AccessKeyID     string
	SecretAccessKey string
	Region          string
	Bucket          string
	Endpoint        string
	ForcePathStyle  bool
	Subdirectory    string
}

type Chunks struct {
	Enabled bool
	Max     string
	Size    string
}

type Tasks struct {
	DeleteInterval          time.Duration
	ClearInvitesInterval    time.Duration
	MaxViewsInterval        time.Duration
	ThumbnailsInterval      time.Duration
	MetricsInterval         time.Duration
	CleanThumbnailsInterval time.Duration
}

type Files struct {
	Route                    string
	Length                   int
	DefaultFormat            string
	DisabledExtensions       []string
	MaxFileSize              string
	DefaultExpiration        string
	MaxExpiration            string
	AssumeMimetypes          bool
	DefaultDateFormat        string
	RemoveGPSMetadata        bool
	RandomWordsNumAdjectives int
	RandomWordsSeparator     string
	DefaultCompressionFormat string
	MaxFilesPerUpload        int
	ExtensionlessUrls        bool
}

type Urls struct {
	Route  string
	Length int
}

type Features struct {
	ImageCompression    bool
	RobotsTxt           bool
	Healthcheck         bool
	UserRegistration    bool
	OAuthRegistration   bool
	DeleteOnMaxViews    bool
	ThumbnailsEnabled   bool
	ThumbnailsThreads   int
	ThumbnailsFormat    string
	ThumbnailsInstant   bool
	MetricsEnabled      bool
	MetricsAdminOnly    bool
	MetricsShowUserSpec bool
	VersionChecking     bool
}

type Invites struct {
	Enabled bool
	Length  int
}

type Website struct {
	Title        string
	ThemeDefault string
	ThemeDark    string
	ThemeLight   string
}

type MFA struct {
	TotpEnabled     bool
	TotpIssuer      string
	PasskeysEnabled bool
	PasskeysRPID    string
	PasskeysOrigin  string
}

type OAuthProviderCfg struct {
	ClientID     string
	ClientSecret string
	RedirectURI  string
	// OIDC-specific
	AuthorizeURL string
	TokenURL     string
	UserinfoURL  string
	// Discord-specific allow/deny lists
	AllowedIDs []string
	DeniedIDs  []string
}

type OAuth struct {
	BypassLocalLogin bool
	LoginOnly        bool
	Discord          OAuthProviderCfg
	Google           OAuthProviderCfg
	Github           OAuthProviderCfg
	OIDC             OAuthProviderCfg
}

type Ratelimit struct {
	Enabled     bool
	Max         int
	Window      int
	AdminBypass bool
	AllowList   []string
}

type Webhooks struct {
	HTTPOnUpload               string
	HTTPOnShorten              string
	DiscordOnUploadWebhookURL  string
	DiscordOnShortenWebhookURL string
}

type PWA struct {
	Enabled         bool
	Title           string
	ShortName       string
	Description     string
	ThemeColor      string
	BackgroundColor string
}

// Defaults returns a Config populated with the same defaults as the Prisma schema.
func Defaults() *Config {
	return &Config{
		Core: Core{
			Port:          3000,
			Hostname:      "0.0.0.0",
			TempDirectory: os.TempDir() + "/zipline",
		},
		Datasource: Datasource{Type: "local", Local: LocalDS{Directory: "./uploads"}},
		Chunks:     Chunks{Enabled: true, Max: "95mb", Size: "25mb"},
		Tasks: Tasks{
			DeleteInterval:          30 * time.Minute,
			ClearInvitesInterval:    30 * time.Minute,
			MaxViewsInterval:        30 * time.Minute,
			ThumbnailsInterval:      30 * time.Minute,
			MetricsInterval:         30 * time.Minute,
			CleanThumbnailsInterval: 24 * time.Hour,
		},
		Files: Files{
			Route:                    "/u",
			Length:                   6,
			DefaultFormat:            "random",
			MaxFileSize:              "100mb",
			AssumeMimetypes:          false,
			DefaultDateFormat:        "2006-01-02_15:04:05",
			RandomWordsNumAdjectives: 2,
			RandomWordsSeparator:     "-",
			DefaultCompressionFormat: "jpg",
			MaxFilesPerUpload:        1000,
		},
		Urls: Urls{Route: "/go", Length: 6},
		Features: Features{
			ImageCompression: true, RobotsTxt: true, Healthcheck: true,
			DeleteOnMaxViews: true, ThumbnailsEnabled: true, ThumbnailsThreads: 4,
			ThumbnailsFormat: "jpg", MetricsEnabled: true, MetricsShowUserSpec: true,
			VersionChecking: true,
		},
		Invites: Invites{Enabled: true, Length: 6},
		Website: Website{
			Title: "Zipline", ThemeDefault: "system",
			ThemeDark: "builtin:dark_gray", ThemeLight: "builtin:light_gray",
		},
		MFA:       MFA{TotpIssuer: "Zipline"},
		Ratelimit: Ratelimit{Enabled: true, Max: 10, AdminBypass: true},
		PWA: PWA{
			Title: "Zipline", ShortName: "Zipline", Description: "Zipline",
			ThemeColor: "#000000", BackgroundColor: "#000000",
		},
	}
}

// Load builds a Config from defaults overlaid with environment variables. Used
// for the bootstrap pass (to obtain the database URL); the effective config is
// rebuilt with BuildEffective once DB-persisted settings are available.
func Load() (*Config, error) {
	return BuildEffective(nil)
}

// BuildEffective constructs the effective config exactly like Zipline's read():
// defaults, overlaid with the DB-persisted settings blob, then overlaid with
// environment variables (env wins). Pass nil for the bootstrap (env-only) pass.
func BuildEffective(blob map[string]any) (*Config, error) {
	c := Defaults()
	c.ApplySettings(blob)
	c.applyEnv()

	if c.Core.Secret == "" {
		return nil, fmt.Errorf("CORE_SECRET is required")
	}
	if len(c.Core.Secret) < 16 {
		return nil, fmt.Errorf("CORE_SECRET must be at least 16 characters")
	}
	if c.Core.DatabaseURL == "" {
		built, err := buildDBURLFromParts()
		if err != nil {
			return nil, err
		}
		c.Core.DatabaseURL = built
	}
	return c, nil
}

// applyEnv overlays environment variables onto c. Supports the *_FILE indirection
// used by the original (read the value from a file path).
func (c *Config) applyEnv() {
	c.Core.Port = envInt("CORE_PORT", c.Core.Port)
	c.Core.Hostname = envStr("CORE_HOSTNAME", c.Core.Hostname)
	c.Core.Secret = envStr("CORE_SECRET", c.Core.Secret)
	c.Core.DatabaseURL = envStr("DATABASE_URL", c.Core.DatabaseURL)
	c.Core.ReturnHTTPSURLs = envBool("CORE_RETURN_HTTPS_URLS", c.Core.ReturnHTTPSURLs)
	c.Core.DefaultDomain = envStr("CORE_DEFAULT_DOMAIN", c.Core.DefaultDomain)
	c.Core.TempDirectory = envStr("CORE_TEMP_DIRECTORY", c.Core.TempDirectory)
	c.Core.TrustProxy = envBool("CORE_TRUST_PROXY", c.Core.TrustProxy)

	c.Datasource.Type = envStr("DATASOURCE_TYPE", c.Datasource.Type)
	c.Datasource.Local.Directory = envStr("DATASOURCE_LOCAL_DIRECTORY", c.Datasource.Local.Directory)
	c.Datasource.S3.AccessKeyID = envStr("DATASOURCE_S3_ACCESS_KEY_ID", c.Datasource.S3.AccessKeyID)
	c.Datasource.S3.SecretAccessKey = envStr("DATASOURCE_S3_SECRET_ACCESS_KEY", c.Datasource.S3.SecretAccessKey)
	c.Datasource.S3.Region = envStr("DATASOURCE_S3_REGION", c.Datasource.S3.Region)
	c.Datasource.S3.Bucket = envStr("DATASOURCE_S3_BUCKET", c.Datasource.S3.Bucket)
	c.Datasource.S3.Endpoint = envStr("DATASOURCE_S3_ENDPOINT", c.Datasource.S3.Endpoint)
	c.Datasource.S3.ForcePathStyle = envBool("DATASOURCE_S3_FORCE_PATH_STYLE", c.Datasource.S3.ForcePathStyle)
	c.Datasource.S3.Subdirectory = envStr("DATASOURCE_S3_SUBDIRECTORY", c.Datasource.S3.Subdirectory)

	// Backblaze B2 speaks the S3 API. Map the friendlier B2_* vars onto the S3
	// datasource and default the endpoint to s3.<region>.backblazeb2.com.
	if strings.EqualFold(c.Datasource.Type, "b2") {
		c.Datasource.S3.AccessKeyID = envStr("DATASOURCE_B2_KEY_ID", c.Datasource.S3.AccessKeyID)
		c.Datasource.S3.SecretAccessKey = envStr("DATASOURCE_B2_APPLICATION_KEY", c.Datasource.S3.SecretAccessKey)
		c.Datasource.S3.Bucket = envStr("DATASOURCE_B2_BUCKET", c.Datasource.S3.Bucket)
		c.Datasource.S3.Region = envStr("DATASOURCE_B2_REGION", c.Datasource.S3.Region)
		c.Datasource.S3.Endpoint = envStr("DATASOURCE_B2_ENDPOINT", c.Datasource.S3.Endpoint)
		c.Datasource.S3.Subdirectory = envStr("DATASOURCE_B2_SUBDIRECTORY", c.Datasource.S3.Subdirectory)
		if c.Datasource.S3.Endpoint == "" && c.Datasource.S3.Region != "" {
			c.Datasource.S3.Endpoint = "s3." + c.Datasource.S3.Region + ".backblazeb2.com"
		}
	}

	c.Files.Route = envStr("FILES_ROUTE", c.Files.Route)
	c.Files.Length = envInt("FILES_LENGTH", c.Files.Length)
	c.Files.DefaultFormat = envStr("FILES_DEFAULT_FORMAT", c.Files.DefaultFormat)
	c.Files.MaxFileSize = envStr("FILES_MAX_FILE_SIZE", c.Files.MaxFileSize)
	c.Files.DefaultCompressionFormat = envStr("FILES_DEFAULT_COMPRESSION_FORMAT", c.Files.DefaultCompressionFormat)
	c.Files.MaxFilesPerUpload = envInt("FILES_MAX_FILES_PER_UPLOAD", c.Files.MaxFilesPerUpload)
	c.Files.ExtensionlessUrls = envBool("FILES_EXTENSIONLESS_URLS", c.Files.ExtensionlessUrls)
	c.Files.RemoveGPSMetadata = envBool("FILES_REMOVE_GPS_METADATA", c.Files.RemoveGPSMetadata)
	c.Files.AssumeMimetypes = envBool("FILES_ASSUME_MIMETYPES", c.Files.AssumeMimetypes)

	c.Urls.Route = envStr("URLS_ROUTE", c.Urls.Route)
	c.Urls.Length = envInt("URLS_LENGTH", c.Urls.Length)

	c.Features.ThumbnailsEnabled = envBool("FEATURES_THUMBNAILS_ENABLED", c.Features.ThumbnailsEnabled)
	c.Features.ThumbnailsThreads = envInt("FEATURES_THUMBNAILS_NUM_THREADS", c.Features.ThumbnailsThreads)
	c.Features.ImageCompression = envBool("FEATURES_IMAGE_COMPRESSION", c.Features.ImageCompression)
	c.Features.UserRegistration = envBool("FEATURES_USER_REGISTRATION", c.Features.UserRegistration)
	c.Features.OAuthRegistration = envBool("FEATURES_OAUTH_REGISTRATION", c.Features.OAuthRegistration)
	c.Features.MetricsEnabled = envBool("FEATURES_METRICS_ENABLED", c.Features.MetricsEnabled)

	c.Website.Title = envStr("WEBSITE_TITLE", c.Website.Title)

	c.MFA.TotpEnabled = envBool("MFA_TOTP_ENABLED", c.MFA.TotpEnabled)
	c.MFA.TotpIssuer = envStr("MFA_TOTP_ISSUER", c.MFA.TotpIssuer)
	c.MFA.PasskeysEnabled = envBool("MFA_PASSKEYS_ENABLED", c.MFA.PasskeysEnabled)
	c.MFA.PasskeysRPID = envStr("MFA_PASSKEYS_RP_ID", c.MFA.PasskeysRPID)
	c.MFA.PasskeysOrigin = envStr("MFA_PASSKEYS_ORIGIN", c.MFA.PasskeysOrigin)

	c.OAuth.Discord.ClientID = envStr("OAUTH_DISCORD_CLIENT_ID", c.OAuth.Discord.ClientID)
	c.OAuth.Discord.ClientSecret = envStr("OAUTH_DISCORD_CLIENT_SECRET", c.OAuth.Discord.ClientSecret)
	c.OAuth.Discord.RedirectURI = envStr("OAUTH_DISCORD_REDIRECT_URI", c.OAuth.Discord.RedirectURI)
	c.OAuth.Github.ClientID = envStr("OAUTH_GITHUB_CLIENT_ID", c.OAuth.Github.ClientID)
	c.OAuth.Github.ClientSecret = envStr("OAUTH_GITHUB_CLIENT_SECRET", c.OAuth.Github.ClientSecret)
	c.OAuth.Github.RedirectURI = envStr("OAUTH_GITHUB_REDIRECT_URI", c.OAuth.Github.RedirectURI)
	c.OAuth.Google.ClientID = envStr("OAUTH_GOOGLE_CLIENT_ID", c.OAuth.Google.ClientID)
	c.OAuth.Google.ClientSecret = envStr("OAUTH_GOOGLE_CLIENT_SECRET", c.OAuth.Google.ClientSecret)
	c.OAuth.Google.RedirectURI = envStr("OAUTH_GOOGLE_REDIRECT_URI", c.OAuth.Google.RedirectURI)
	c.OAuth.OIDC.ClientID = envStr("OAUTH_OIDC_CLIENT_ID", c.OAuth.OIDC.ClientID)
	c.OAuth.OIDC.ClientSecret = envStr("OAUTH_OIDC_CLIENT_SECRET", c.OAuth.OIDC.ClientSecret)
	c.OAuth.OIDC.AuthorizeURL = envStr("OAUTH_OIDC_AUTHORIZE_URL", c.OAuth.OIDC.AuthorizeURL)
	c.OAuth.OIDC.TokenURL = envStr("OAUTH_OIDC_TOKEN_URL", c.OAuth.OIDC.TokenURL)
	c.OAuth.OIDC.UserinfoURL = envStr("OAUTH_OIDC_USERINFO_URL", c.OAuth.OIDC.UserinfoURL)
	c.OAuth.OIDC.RedirectURI = envStr("OAUTH_OIDC_REDIRECT_URI", c.OAuth.OIDC.RedirectURI)

	c.PWA.Enabled = envBool("PWA_ENABLED", c.PWA.Enabled)
	if v := envStr("DOMAINS", ""); v != "" {
		c.Domains = splitCSV(v)
	}
}

func buildDBURLFromParts() (string, error) {
	u := os.Getenv("DATABASE_USERNAME")
	p := os.Getenv("DATABASE_PASSWORD")
	h := os.Getenv("DATABASE_HOST")
	port := os.Getenv("DATABASE_PORT")
	name := os.Getenv("DATABASE_NAME")
	if u == "" || h == "" || port == "" || name == "" {
		return "", fmt.Errorf("either DATABASE_URL or all of DATABASE_USERNAME/PASSWORD/HOST/PORT/NAME must be set")
	}
	return fmt.Sprintf("postgresql://%s:%s@%s:%s/%s", u, p, h, port, name), nil
}

// ApplySettings overlays the DB-persisted settings blob (the flat key/value
// object the admin Settings UI saves) onto c. It is the inverse of the server's
// flat-settings projection. Only keys present in the blob are applied; env vars
// (applied afterwards in applyEnv) still win. Keys with no Config field, and
// values of the wrong type, are ignored.
func (c *Config) ApplySettings(blob map[string]any) {
	if len(blob) == 0 {
		return
	}

	// core
	c.Core.ReturnHTTPSURLs = sbBool(blob, "coreReturnHttpsUrls", c.Core.ReturnHTTPSURLs)
	c.Core.DefaultDomain = sbStr(blob, "coreDefaultDomain", c.Core.DefaultDomain)
	c.Core.TempDirectory = sbStr(blob, "coreTempDirectory", c.Core.TempDirectory)
	c.Core.TrustProxy = sbBool(blob, "coreTrustProxy", c.Core.TrustProxy)

	// chunks
	c.Chunks.Enabled = sbBool(blob, "chunksEnabled", c.Chunks.Enabled)
	c.Chunks.Max = sbStr(blob, "chunksMax", c.Chunks.Max)
	c.Chunks.Size = sbStr(blob, "chunksSize", c.Chunks.Size)

	// tasks (ms-style duration strings, e.g. "30m", "1d")
	c.Tasks.DeleteInterval = sbDur(blob, "tasksDeleteInterval", c.Tasks.DeleteInterval)
	c.Tasks.ClearInvitesInterval = sbDur(blob, "tasksClearInvitesInterval", c.Tasks.ClearInvitesInterval)
	c.Tasks.MaxViewsInterval = sbDur(blob, "tasksMaxViewsInterval", c.Tasks.MaxViewsInterval)
	c.Tasks.ThumbnailsInterval = sbDur(blob, "tasksThumbnailsInterval", c.Tasks.ThumbnailsInterval)
	c.Tasks.MetricsInterval = sbDur(blob, "tasksMetricsInterval", c.Tasks.MetricsInterval)
	c.Tasks.CleanThumbnailsInterval = sbDur(blob, "tasksCleanThumbnailsInterval", c.Tasks.CleanThumbnailsInterval)

	// files
	c.Files.Route = sbStr(blob, "filesRoute", c.Files.Route)
	c.Files.Length = sbInt(blob, "filesLength", c.Files.Length)
	c.Files.DefaultFormat = sbStr(blob, "filesDefaultFormat", c.Files.DefaultFormat)
	c.Files.DisabledExtensions = sbStrSlice(blob, "filesDisabledExtensions", c.Files.DisabledExtensions)
	c.Files.MaxFileSize = sbStr(blob, "filesMaxFileSize", c.Files.MaxFileSize)
	c.Files.DefaultExpiration = sbStr(blob, "filesDefaultExpiration", c.Files.DefaultExpiration)
	c.Files.MaxExpiration = sbStr(blob, "filesMaxExpiration", c.Files.MaxExpiration)
	c.Files.AssumeMimetypes = sbBool(blob, "filesAssumeMimetypes", c.Files.AssumeMimetypes)
	c.Files.DefaultDateFormat = sbStr(blob, "filesDefaultDateFormat", c.Files.DefaultDateFormat)
	c.Files.RemoveGPSMetadata = sbBool(blob, "filesRemoveGpsMetadata", c.Files.RemoveGPSMetadata)
	c.Files.RandomWordsNumAdjectives = sbInt(blob, "filesRandomWordsNumAdjectives", c.Files.RandomWordsNumAdjectives)
	c.Files.RandomWordsSeparator = sbStr(blob, "filesRandomWordsSeparator", c.Files.RandomWordsSeparator)
	c.Files.DefaultCompressionFormat = sbStr(blob, "filesDefaultCompressionFormat", c.Files.DefaultCompressionFormat)
	c.Files.MaxFilesPerUpload = sbInt(blob, "filesMaxFilesPerUpload", c.Files.MaxFilesPerUpload)
	c.Files.ExtensionlessUrls = sbBool(blob, "filesExtensionlessUrls", c.Files.ExtensionlessUrls)

	// urls
	c.Urls.Route = sbStr(blob, "urlsRoute", c.Urls.Route)
	c.Urls.Length = sbInt(blob, "urlsLength", c.Urls.Length)

	// features
	c.Features.ImageCompression = sbBool(blob, "featuresImageCompression", c.Features.ImageCompression)
	c.Features.RobotsTxt = sbBool(blob, "featuresRobotsTxt", c.Features.RobotsTxt)
	c.Features.Healthcheck = sbBool(blob, "featuresHealthcheck", c.Features.Healthcheck)
	c.Features.UserRegistration = sbBool(blob, "featuresUserRegistration", c.Features.UserRegistration)
	c.Features.OAuthRegistration = sbBool(blob, "featuresOauthRegistration", c.Features.OAuthRegistration)
	c.Features.DeleteOnMaxViews = sbBool(blob, "featuresDeleteOnMaxViews", c.Features.DeleteOnMaxViews)
	c.Features.ThumbnailsEnabled = sbBool(blob, "featuresThumbnailsEnabled", c.Features.ThumbnailsEnabled)
	c.Features.ThumbnailsThreads = sbInt(blob, "featuresThumbnailsNumberThreads", c.Features.ThumbnailsThreads)
	c.Features.ThumbnailsFormat = sbStr(blob, "featuresThumbnailsFormat", c.Features.ThumbnailsFormat)
	c.Features.ThumbnailsInstant = sbBool(blob, "featuresThumbnailsInstantaneous", c.Features.ThumbnailsInstant)
	c.Features.MetricsEnabled = sbBool(blob, "featuresMetricsEnabled", c.Features.MetricsEnabled)
	c.Features.MetricsAdminOnly = sbBool(blob, "featuresMetricsAdminOnly", c.Features.MetricsAdminOnly)
	c.Features.MetricsShowUserSpec = sbBool(blob, "featuresMetricsShowUserSpecific", c.Features.MetricsShowUserSpec)
	c.Features.VersionChecking = sbBool(blob, "featuresVersionChecking", c.Features.VersionChecking)

	// invites
	c.Invites.Enabled = sbBool(blob, "invitesEnabled", c.Invites.Enabled)
	c.Invites.Length = sbInt(blob, "invitesLength", c.Invites.Length)

	// website
	c.Website.Title = sbStr(blob, "websiteTitle", c.Website.Title)
	c.Website.ThemeDefault = sbStr(blob, "websiteThemeDefault", c.Website.ThemeDefault)
	c.Website.ThemeDark = sbStr(blob, "websiteThemeDark", c.Website.ThemeDark)
	c.Website.ThemeLight = sbStr(blob, "websiteThemeLight", c.Website.ThemeLight)

	// oauth
	c.OAuth.BypassLocalLogin = sbBool(blob, "oauthBypassLocalLogin", c.OAuth.BypassLocalLogin)
	c.OAuth.LoginOnly = sbBool(blob, "oauthLoginOnly", c.OAuth.LoginOnly)
	c.OAuth.Discord.ClientID = sbStr(blob, "oauthDiscordClientId", c.OAuth.Discord.ClientID)
	c.OAuth.Discord.ClientSecret = sbStr(blob, "oauthDiscordClientSecret", c.OAuth.Discord.ClientSecret)
	c.OAuth.Discord.RedirectURI = sbStr(blob, "oauthDiscordRedirectUri", c.OAuth.Discord.RedirectURI)
	c.OAuth.Discord.AllowedIDs = sbStrSlice(blob, "oauthDiscordAllowedIds", c.OAuth.Discord.AllowedIDs)
	c.OAuth.Discord.DeniedIDs = sbStrSlice(blob, "oauthDiscordDeniedIds", c.OAuth.Discord.DeniedIDs)
	c.OAuth.Google.ClientID = sbStr(blob, "oauthGoogleClientId", c.OAuth.Google.ClientID)
	c.OAuth.Google.ClientSecret = sbStr(blob, "oauthGoogleClientSecret", c.OAuth.Google.ClientSecret)
	c.OAuth.Google.RedirectURI = sbStr(blob, "oauthGoogleRedirectUri", c.OAuth.Google.RedirectURI)
	c.OAuth.Github.ClientID = sbStr(blob, "oauthGithubClientId", c.OAuth.Github.ClientID)
	c.OAuth.Github.ClientSecret = sbStr(blob, "oauthGithubClientSecret", c.OAuth.Github.ClientSecret)
	c.OAuth.Github.RedirectURI = sbStr(blob, "oauthGithubRedirectUri", c.OAuth.Github.RedirectURI)
	c.OAuth.OIDC.ClientID = sbStr(blob, "oauthOidcClientId", c.OAuth.OIDC.ClientID)
	c.OAuth.OIDC.ClientSecret = sbStr(blob, "oauthOidcClientSecret", c.OAuth.OIDC.ClientSecret)
	c.OAuth.OIDC.AuthorizeURL = sbStr(blob, "oauthOidcAuthorizeUrl", c.OAuth.OIDC.AuthorizeURL)
	c.OAuth.OIDC.TokenURL = sbStr(blob, "oauthOidcTokenUrl", c.OAuth.OIDC.TokenURL)
	c.OAuth.OIDC.UserinfoURL = sbStr(blob, "oauthOidcUserinfoUrl", c.OAuth.OIDC.UserinfoURL)
	c.OAuth.OIDC.RedirectURI = sbStr(blob, "oauthOidcRedirectUri", c.OAuth.OIDC.RedirectURI)

	// mfa
	c.MFA.TotpEnabled = sbBool(blob, "mfaTotpEnabled", c.MFA.TotpEnabled)
	c.MFA.TotpIssuer = sbStr(blob, "mfaTotpIssuer", c.MFA.TotpIssuer)
	c.MFA.PasskeysEnabled = sbBool(blob, "mfaPasskeysEnabled", c.MFA.PasskeysEnabled)
	c.MFA.PasskeysRPID = sbStr(blob, "mfaPasskeysRpID", c.MFA.PasskeysRPID)
	c.MFA.PasskeysOrigin = sbStr(blob, "mfaPasskeysOrigin", c.MFA.PasskeysOrigin)

	// ratelimit
	c.Ratelimit.Enabled = sbBool(blob, "ratelimitEnabled", c.Ratelimit.Enabled)
	c.Ratelimit.Max = sbInt(blob, "ratelimitMax", c.Ratelimit.Max)
	c.Ratelimit.AdminBypass = sbBool(blob, "ratelimitAdminBypass", c.Ratelimit.AdminBypass)
	c.Ratelimit.AllowList = sbStrSlice(blob, "ratelimitAllowList", c.Ratelimit.AllowList)

	// webhooks
	c.Webhooks.HTTPOnUpload = sbStr(blob, "httpWebhookOnUpload", c.Webhooks.HTTPOnUpload)
	c.Webhooks.HTTPOnShorten = sbStr(blob, "httpWebhookOnShorten", c.Webhooks.HTTPOnShorten)
	c.Webhooks.DiscordOnUploadWebhookURL = sbStr(blob, "discordOnUploadWebhookUrl", c.Webhooks.DiscordOnUploadWebhookURL)
	c.Webhooks.DiscordOnShortenWebhookURL = sbStr(blob, "discordOnShortenWebhookUrl", c.Webhooks.DiscordOnShortenWebhookURL)

	// pwa
	c.PWA.Enabled = sbBool(blob, "pwaEnabled", c.PWA.Enabled)
	c.PWA.Title = sbStr(blob, "pwaTitle", c.PWA.Title)
	c.PWA.ShortName = sbStr(blob, "pwaShortName", c.PWA.ShortName)
	c.PWA.Description = sbStr(blob, "pwaDescription", c.PWA.Description)
	c.PWA.ThemeColor = sbStr(blob, "pwaThemeColor", c.PWA.ThemeColor)
	c.PWA.BackgroundColor = sbStr(blob, "pwaBackgroundColor", c.PWA.BackgroundColor)

	// domains
	c.Domains = sbStrSlice(blob, "domains", c.Domains)
}

// settingEnvVars maps each editable setting's flat key to the environment
// variable that overrides it (only the settings applyEnv actually reads). A key
// whose env var is present is "tampered": env wins and the admin UI locks it.
var settingEnvVars = map[string]string{
	"coreReturnHttpsUrls":             "CORE_RETURN_HTTPS_URLS",
	"coreDefaultDomain":               "CORE_DEFAULT_DOMAIN",
	"coreTempDirectory":               "CORE_TEMP_DIRECTORY",
	"coreTrustProxy":                  "CORE_TRUST_PROXY",
	"filesRoute":                      "FILES_ROUTE",
	"filesLength":                     "FILES_LENGTH",
	"filesDefaultFormat":              "FILES_DEFAULT_FORMAT",
	"filesMaxFileSize":                "FILES_MAX_FILE_SIZE",
	"filesDefaultCompressionFormat":   "FILES_DEFAULT_COMPRESSION_FORMAT",
	"filesMaxFilesPerUpload":          "FILES_MAX_FILES_PER_UPLOAD",
	"filesExtensionlessUrls":          "FILES_EXTENSIONLESS_URLS",
	"filesRemoveGpsMetadata":          "FILES_REMOVE_GPS_METADATA",
	"filesAssumeMimetypes":            "FILES_ASSUME_MIMETYPES",
	"urlsRoute":                       "URLS_ROUTE",
	"urlsLength":                      "URLS_LENGTH",
	"featuresThumbnailsEnabled":       "FEATURES_THUMBNAILS_ENABLED",
	"featuresThumbnailsNumberThreads": "FEATURES_THUMBNAILS_NUM_THREADS",
	"featuresImageCompression":        "FEATURES_IMAGE_COMPRESSION",
	"featuresUserRegistration":        "FEATURES_USER_REGISTRATION",
	"featuresOauthRegistration":       "FEATURES_OAUTH_REGISTRATION",
	"featuresMetricsEnabled":          "FEATURES_METRICS_ENABLED",
	"websiteTitle":                    "WEBSITE_TITLE",
	"mfaTotpEnabled":                  "MFA_TOTP_ENABLED",
	"mfaTotpIssuer":                   "MFA_TOTP_ISSUER",
	"mfaPasskeysEnabled":              "MFA_PASSKEYS_ENABLED",
	"mfaPasskeysRpID":                 "MFA_PASSKEYS_RP_ID",
	"mfaPasskeysOrigin":               "MFA_PASSKEYS_ORIGIN",
	"oauthDiscordClientId":            "OAUTH_DISCORD_CLIENT_ID",
	"oauthDiscordClientSecret":        "OAUTH_DISCORD_CLIENT_SECRET",
	"oauthDiscordRedirectUri":         "OAUTH_DISCORD_REDIRECT_URI",
	"oauthGithubClientId":             "OAUTH_GITHUB_CLIENT_ID",
	"oauthGithubClientSecret":         "OAUTH_GITHUB_CLIENT_SECRET",
	"oauthGithubRedirectUri":          "OAUTH_GITHUB_REDIRECT_URI",
	"oauthGoogleClientId":             "OAUTH_GOOGLE_CLIENT_ID",
	"oauthGoogleClientSecret":         "OAUTH_GOOGLE_CLIENT_SECRET",
	"oauthGoogleRedirectUri":          "OAUTH_GOOGLE_REDIRECT_URI",
	"oauthOidcClientId":               "OAUTH_OIDC_CLIENT_ID",
	"oauthOidcClientSecret":           "OAUTH_OIDC_CLIENT_SECRET",
	"oauthOidcAuthorizeUrl":           "OAUTH_OIDC_AUTHORIZE_URL",
	"oauthOidcTokenUrl":               "OAUTH_OIDC_TOKEN_URL",
	"oauthOidcUserinfoUrl":            "OAUTH_OIDC_USERINFO_URL",
	"oauthOidcRedirectUri":            "OAUTH_OIDC_REDIRECT_URI",
	"pwaEnabled":                      "PWA_ENABLED",
	"domains":                         "DOMAINS",
}

// EnvTamperedKeys returns the flat setting keys whose environment variable is
// set, so the admin UI can lock those inputs (env overrides the DB value).
func EnvTamperedKeys() []string {
	keys := make([]string, 0)
	for flat, env := range settingEnvVars {
		if _, ok := envRaw(env); ok {
			keys = append(keys, flat)
		}
	}
	return keys
}

// --- settings-blob value helpers (JSON-decoded values: bool, float64, string, []any) ---

func sbBool(b map[string]any, k string, cur bool) bool {
	if v, ok := b[k]; ok {
		if bb, ok2 := v.(bool); ok2 {
			return bb
		}
	}
	return cur
}

func sbStr(b map[string]any, k string, cur string) string {
	if v, ok := b[k]; ok {
		switch t := v.(type) {
		case string:
			return t
		case nil:
			return ""
		}
	}
	return cur
}

func sbInt(b map[string]any, k string, cur int) int {
	if v, ok := b[k]; ok {
		switch t := v.(type) {
		case float64:
			return int(t)
		case int:
			return t
		case string:
			if n, err := strconv.Atoi(strings.TrimSpace(t)); err == nil {
				return n
			}
		}
	}
	return cur
}

func sbStrSlice(b map[string]any, k string, cur []string) []string {
	if v, ok := b[k]; ok {
		if arr, ok2 := v.([]any); ok2 {
			out := make([]string, 0, len(arr))
			for _, e := range arr {
				if s, ok3 := e.(string); ok3 {
					out = append(out, s)
				}
			}
			return out
		}
	}
	return cur
}

func sbDur(b map[string]any, k string, cur time.Duration) time.Duration {
	if v, ok := b[k]; ok {
		if s, ok2 := v.(string); ok2 {
			if d, ok3 := parseMsDuration(s); ok3 {
				return d
			}
		}
	}
	return cur
}

// parseMsDuration parses Zipline's ms-style duration strings ("30m", "1d", "1h",
// "45s", "500ms", "1w"). time.ParseDuration handles all but day/week.
func parseMsDuration(s string) (time.Duration, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	if strings.HasSuffix(s, "w") {
		if n, err := strconv.ParseFloat(strings.TrimSuffix(s, "w"), 64); err == nil {
			return time.Duration(n * float64(7*24*time.Hour)), true
		}
	}
	if strings.HasSuffix(s, "d") {
		if n, err := strconv.ParseFloat(strings.TrimSuffix(s, "d"), 64); err == nil {
			return time.Duration(n * float64(24*time.Hour)), true
		}
	}
	if d, err := time.ParseDuration(s); err == nil {
		return d, true
	}
	return 0, false
}

// --- env helpers (with *_FILE indirection) ---

func envRaw(key string) (string, bool) {
	if fp := os.Getenv(key + "_FILE"); fp != "" {
		if b, err := os.ReadFile(fp); err == nil {
			return strings.TrimSpace(string(b)), true
		}
	}
	v, ok := os.LookupEnv(key)
	return v, ok
}

func envStr(key, def string) string {
	if v, ok := envRaw(key); ok {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v, ok := envRaw(key); ok {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return n
		}
	}
	return def
}

func envBool(key string, def bool) bool {
	if v, ok := envRaw(key); ok {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "true", "1", "yes", "on":
			return true
		case "false", "0", "no", "off":
			return false
		}
	}
	return def
}

func splitCSV(v string) []string {
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}
