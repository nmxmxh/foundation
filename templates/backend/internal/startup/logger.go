// Package startup initializes infrastructure dependencies for the application.
package startup

import (
	"log/slog"
	"os"
	"strings"
)

// NewLogger creates a new structured logger based on environment
func NewLogger(env, level string) *slog.Logger {
	var handler slog.Handler

	logLevel := parseLogLevel(level)

	opts := &slog.HandlerOptions{
		Level: logLevel,
	}

	if env == "production" {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		handler = slog.NewTextHandler(os.Stdout, opts)
	}

	return slog.New(handler)
}

func parseLogLevel(level string) slog.Level {
	switch strings.ToLower(level) {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
