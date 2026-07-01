// Package logging provides the engine's structured logger.
//
// Structured, local, and secret-safe (NFR-6): the logger never receives secret values (callers pass
// secret NAMES only). Uses the standard library slog so there is no external dependency.
package logging

import (
	"io"
	"log/slog"
	"os"
	"strings"
)

// New returns a structured logger writing to w. level is one of debug|info|warn|error
// (default info). format is "text" (human) or "json" (machine); default text.
func New(w io.Writer, level, format string) *slog.Logger {
	opts := &slog.HandlerOptions{Level: parseLevel(level)}
	var h slog.Handler
	if strings.EqualFold(format, "json") {
		h = slog.NewJSONHandler(w, opts)
	} else {
		h = slog.NewTextHandler(w, opts)
	}
	return slog.New(h)
}

// Default returns a logger to stderr at info level, text format.
func Default() *slog.Logger { return New(os.Stderr, "info", "text") }

func parseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
