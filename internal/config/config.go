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

// Load builds a Config from defaults overlaid with environment variables.
// DB-persisted settings can later be overlaid via ApplySettings before env is re-applied.
func Load() (*Config, error) {
	c := Defaults()
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
