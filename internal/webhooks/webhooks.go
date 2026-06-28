// Package webhooks dispatches Zipline's upload/shorten webhooks.
//
// Two flavours are supported, either or both of which may be configured:
//
//   - Discord webhooks: a Discord-formatted JSON payload ({content, username,
//     avatar_url, embeds}) POSTed to a Discord webhook URL. The textual fields
//     are run through the parser so administrators can interpolate context with
//     tokens such as "{file.name} uploaded by {user.username}".
//   - Generic HTTP webhooks: a JSON envelope {type, data:{user, file|url, link}}
//     POSTed to an arbitrary endpoint with identifying x-zipline-webhook headers.
//
// All network I/O happens on a detached goroutine (fire-and-forget): callers are
// never blocked and a failing webhook can never panic the request that triggered
// it. Failures are logged via logger.Log("webhooks").
//
// Secrets are never transmitted. The models json:"-"-tag their sensitive fields
// (password, token, totpSecret), so json.Marshal of a user/file/url is already
// safe, and the parser independently redacts any sensitive token access.
package webhooks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"zipfast/internal/config"
	"zipfast/internal/logger"
	"zipfast/internal/models"
	"zipfast/internal/parser"
)

// httpClient is shared by all webhook deliveries. The short timeout keeps a slow
// or unresponsive endpoint from leaking goroutines indefinitely.
var httpClient = &http.Client{Timeout: 10 * time.Second}

// Default Discord presentation. Zipline historically posts under a "Zipline"
// username; administrators can customise this later if needed.
const (
	discordUsername       = "Zipline"
	defaultUploadContent  = "**New file uploaded:** {link.returned}"
	defaultShortenContent = "**New URL shortened:** {link.returned}"
)

// discordPayload is the subset of the Discord webhook execute body we send.
type discordPayload struct {
	Content   string         `json:"content,omitempty"`
	Username  string         `json:"username,omitempty"`
	AvatarURL string         `json:"avatar_url,omitempty"`
	Embeds    []discordEmbed `json:"embeds,omitempty"`
}

// discordEmbed is a single Discord rich embed.
type discordEmbed struct {
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	URL         string `json:"url,omitempty"`
}

// httpUploadData is the data payload for an HTTP upload webhook.
type httpUploadData struct {
	User *models.User `json:"user"`
	File *models.File `json:"file"`
	Link parser.Link  `json:"link"`
}

// httpShortenData is the data payload for an HTTP shorten webhook.
type httpShortenData struct {
	User *models.User `json:"user"`
	URL  *models.Url  `json:"url"`
	Link parser.Link  `json:"link"`
}

// httpEnvelope wraps an HTTP webhook payload with its type discriminator.
type httpEnvelope struct {
	Type string      `json:"type"`
	Data interface{} `json:"data"`
}

// OnUpload fires the configured upload webhooks for a newly uploaded file. It
// returns immediately; deliveries run on detached goroutines.
func OnUpload(cfg *config.Config, file *models.File, user *models.User, link parser.Link) {
	if cfg == nil {
		return
	}

	ctx := parser.Context{File: file, User: user, Link: link}

	if url := cfg.Webhooks.DiscordOnUploadWebhookURL; url != "" {
		payload := discordPayload{
			Content:   parser.ParseString(defaultUploadContent, ctx),
			Username:  discordUsername,
			AvatarURL: avatarURL(cfg, user),
			Embeds: []discordEmbed{{
				Title:       parser.ParseString("{file.name}", ctx),
				Description: parser.ParseString("Size: {file.size} bytes • Type: {file.type}", ctx),
				URL:         link.Returned,
			}},
		}
		dispatchJSON("discord upload", url, payload, nil)
	}

	if url := cfg.Webhooks.HTTPOnUpload; url != "" {
		env := httpEnvelope{
			Type: "upload",
			Data: httpUploadData{User: user, File: file, Link: link},
		}
		dispatchJSON("http upload", url, env, httpWebhookHeaders("upload"))
	}
}

// OnShorten fires the configured shorten webhooks for a newly created short URL.
// It returns immediately; deliveries run on detached goroutines.
func OnShorten(cfg *config.Config, url *models.Url, user *models.User, link parser.Link) {
	if cfg == nil {
		return
	}

	ctx := parser.Context{URL: url, User: user, Link: link}

	if hook := cfg.Webhooks.DiscordOnShortenWebhookURL; hook != "" {
		payload := discordPayload{
			Content:   parser.ParseString(defaultShortenContent, ctx),
			Username:  discordUsername,
			AvatarURL: avatarURL(cfg, user),
			Embeds: []discordEmbed{{
				Title:       parser.ParseString("{url.code}", ctx),
				Description: parser.ParseString("{url.destination}", ctx),
				URL:         link.Returned,
			}},
		}
		dispatchJSON("discord shorten", hook, payload, nil)
	}

	if hook := cfg.Webhooks.HTTPOnShorten; hook != "" {
		env := httpEnvelope{
			Type: "shorten",
			Data: httpShortenData{User: user, URL: url, Link: link},
		}
		dispatchJSON("http shorten", hook, env, httpWebhookHeaders("shorten"))
	}
}

// httpWebhookHeaders returns the identifying headers attached to generic HTTP
// webhooks. kind is "upload" or "shorten".
func httpWebhookHeaders(kind string) map[string]string {
	return map[string]string{
		"x-zipline-webhook":      "true",
		"x-zipline-webhook-type": kind,
	}
}

// avatarURL derives an avatar URL for the Discord message. A per-user avatar is
// preferred; otherwise the configured default domain is used as a fallback so
// the embed has a sensible icon.
func avatarURL(cfg *config.Config, user *models.User) string {
	if user != nil && user.Avatar != nil && *user.Avatar != "" {
		return *user.Avatar
	}
	return cfg.Core.DefaultDomain
}

// dispatchJSON marshals payload to JSON and POSTs it to url on a detached
// goroutine. extraHeaders may be nil. The label is used only for logging.
//
// This function never blocks the caller and never panics it: marshaling and the
// HTTP round-trip both happen inside the goroutine, and any error is logged.
func dispatchJSON(label, url string, payload interface{}, extraHeaders map[string]string) {
	body, err := json.Marshal(payload)
	if err != nil {
		// Marshaling failed; log synchronously since there is nothing to send.
		logWebhook(label, url, "marshal", err)
		return
	}

	go func() {
		// A context timeout backstops the client timeout and guarantees the
		// goroutine is reclaimed even if the transport misbehaves.
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			logWebhook(label, url, "build request", err)
			return
		}
		req.Header.Set("Content-Type", "application/json")
		for k, v := range extraHeaders {
			req.Header.Set(k, v)
		}

		resp, err := httpClient.Do(req)
		if err != nil {
			logWebhook(label, url, "send", err)
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode >= 300 {
			logWebhook(label, url, "non-2xx response", &statusError{code: resp.StatusCode})
		}
	}()
}

// statusError describes a non-success HTTP status returned by a webhook target.
type statusError struct{ code int }

func (e *statusError) Error() string {
	return fmt.Sprintf("status %d", e.code)
}

// logWebhook records a webhook delivery failure. It is the single place errors
// from this package surface, keeping the dispatch path free of panics.
func logWebhook(label, url, stage string, err error) {
	logger.Log("webhooks").Error("webhook delivery failed",
		"webhook", label,
		"url", url,
		"stage", stage,
		"err", err,
	)
}
