package main

import (
	"io"
	"log"
	"log/slog"
	"strings"
)

// newLogger writes JSON to w.
// level is debug, info, warn, or error. Any other level is info.
func newLogger(w io.Writer, level string) *slog.Logger {
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level: slogLevel(level),
	}))
}

func slogLevel(level string) slog.Level {
	switch strings.ToLower(level) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// httpErrorLog returns the HTTP server error log.
// It writes through the same handler as logger.
func httpErrorLog(logger *slog.Logger) *log.Logger {
	if logger == nil {
		return log.New(io.Discard, "", 0)
	}
	return slog.NewLogLogger(logger.Handler(), slog.LevelError)
}
