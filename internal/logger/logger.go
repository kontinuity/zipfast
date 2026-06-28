// Package logger provides a tiny structured logging helper built on the stdlib slog.
// Using slog keeps the binary dependency-free for logging.
package logger

import (
	"log/slog"
	"os"
	"strings"
)

var base *slog.Logger

func init() {
	level := slog.LevelInfo
	switch strings.ToLower(os.Getenv("LOG_LEVEL")) {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}

	handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level})
	base = slog.New(handler)
}

// Log returns a logger scoped with a "component" attribute, mirroring the
// channel-based logging used in the original Zipline (log('server').c('vite')).
func Log(component string) *slog.Logger {
	return base.With("component", component)
}

// Default returns the root logger.
func Default() *slog.Logger {
	return base
}
