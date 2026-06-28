// Package oauth holds static, provider-specific definitions (authorize/token/
// userinfo endpoints, scopes, and how to read the provider's user identity out
// of a userinfo response) for Zipfast's OAuth login flows. It deliberately holds
// no HTTP or server wiring: the server package drives the flow and uses these
// definitions to stay provider-agnostic.
//
// Only Discord, GitHub, Google and a generic OIDC provider are supported, mirroring
// the original Zipline. Google and OIDC are OpenID Connect providers; OIDC also
// uses PKCE (S256).
package oauth

import (
	"fmt"
	"strconv"

	"zipfast/internal/models"
)

// Name is a lowercase provider identifier as it appears in URLs
// (/api/auth/oauth/{provider}).
type Name string

const (
	Discord Name = "discord"
	GitHub  Name = "github"
	Google  Name = "google"
	OIDC    Name = "oidc"
)

// Provider is the static definition of an OAuth provider. The Authorize/Token/
// Userinfo URLs are fixed for Discord/GitHub/Google and supplied from config for
// OIDC (left empty here and filled in by ProviderFor).
type Provider struct {
	Name         Name
	Type         models.OAuthProviderType
	AuthorizeURL string
	TokenURL     string
	UserinfoURL  string
	Scope        string

	// UsesPKCE indicates the authorize/token exchange must include a PKCE
	// (S256) code challenge + verifier. Only the generic OIDC provider does.
	UsesPKCE bool

	// AcceptHeader, when non-empty, is sent as the Accept header on the userinfo
	// request (GitHub wants its vendor media type).
	AcceptHeader string
}

// Providers returns the built-in, fixed provider definitions keyed by name. The
// OIDC entry has empty endpoint URLs; callers must populate them from config
// (see ProviderFor).
func Providers() map[Name]Provider {
	return map[Name]Provider{
		Discord: {
			Name:         Discord,
			Type:         models.OAuthDiscord,
			AuthorizeURL: "https://discord.com/api/oauth2/authorize",
			TokenURL:     "https://discord.com/api/oauth2/token",
			UserinfoURL:  "https://discord.com/api/users/@me",
			Scope:        "identify",
		},
		GitHub: {
			Name:         GitHub,
			Type:         models.OAuthGithub,
			AuthorizeURL: "https://github.com/login/oauth/authorize",
			TokenURL:     "https://github.com/login/oauth/access_token",
			UserinfoURL:  "https://api.github.com/user",
			Scope:        "read:user",
			AcceptHeader: "application/vnd.github+json",
		},
		Google: {
			Name:         Google,
			Type:         models.OAuthGoogle,
			AuthorizeURL: "https://accounts.google.com/o/oauth2/v2/auth",
			TokenURL:     "https://oauth2.googleapis.com/token",
			UserinfoURL:  "https://www.googleapis.com/oauth2/v2/userinfo",
			Scope:        "openid email profile",
		},
		OIDC: {
			Name:     OIDC,
			Type:     models.OAuthOIDC,
			Scope:    "openid email profile",
			UsesPKCE: true,
		},
	}
}

// ProviderFor returns the built-in definition for the named provider. For OIDC,
// the authorize/token/userinfo URLs must be supplied (from config); they replace
// the empty defaults. It returns ok=false for an unknown provider name.
func ProviderFor(name string, oidcAuthorize, oidcToken, oidcUserinfo string) (Provider, bool) {
	p, ok := Providers()[Name(name)]
	if !ok {
		return Provider{}, false
	}
	if p.Name == OIDC {
		p.AuthorizeURL = oidcAuthorize
		p.TokenURL = oidcToken
		p.UserinfoURL = oidcUserinfo
	}
	return p, true
}

// Identity is the minimal user identity extracted from a provider's userinfo
// response: a stable provider-scoped id and a display username.
type Identity struct {
	ID       string
	Username string
}

// ExtractIdentity reads the provider-scoped user id and a username out of a
// decoded userinfo JSON object, applying each provider's field conventions:
//
//	discord: id="id"            username="username"
//	github:  id="id" (number)   username="login"
//	google:  id="id" or "sub"   username="email" or "name"
//	oidc:    id="sub"           username="preferred_username" or "email"
//
// It returns an error when the required id field is missing or empty. A missing
// username falls back to the id so callers always have a base to derive from.
func (p Provider) ExtractIdentity(info map[string]any) (Identity, error) {
	var id Identity

	switch p.Name {
	case Discord:
		id.ID = asString(info["id"])
		id.Username = asString(info["username"])
	case GitHub:
		id.ID = asString(info["id"])
		id.Username = asString(info["login"])
	case Google:
		id.ID = firstNonEmpty(asString(info["id"]), asString(info["sub"]))
		id.Username = firstNonEmpty(asString(info["email"]), asString(info["name"]))
	case OIDC:
		id.ID = asString(info["sub"])
		id.Username = firstNonEmpty(asString(info["preferred_username"]), asString(info["email"]))
	default:
		return id, fmt.Errorf("oauth: unknown provider %q", p.Name)
	}

	if id.ID == "" {
		return id, fmt.Errorf("oauth: %s userinfo response missing user id", p.Name)
	}
	if id.Username == "" {
		id.Username = id.ID
	}
	return id, nil
}

// asString coerces a JSON value to a string. JSON numbers decode to float64 via
// encoding/json; GitHub's numeric id is rendered without a fractional part.
func asString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", t)
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
