package main

import (
	"io"
	"log"
	"log/slog"

	"github.com/eclipse-iofog/ai-inference-onnx/internal/session"
)

// newLogger writes JSON to w. level is debug, info, warn, or error.
// Any other level is info.
func newLogger(w io.Writer, level string) *slog.Logger {
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level: slogLevel(level),
	}))
}

func slogLevel(level string) slog.Level {
	switch level {
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
	return slog.NewLogLogger(logger.Handler(), slog.LevelError)
}

// logModelState records a model state that has already been stored.
// Ready and the start of a load are info. Unavailable is warn.
func logModelState(logger *slog.Logger, name string, state session.State) {
	if logger == nil {
		return
	}
	switch state {
	case session.Ready:
		logger.Info("model ready", "model", name, "state", string(state))
	case session.Loading:
		logger.Info("model load started", "model", name, "state", string(state))
	case session.Unavailable:
		logger.Warn("model unavailable", "model", name, "state", string(state))
	}
}
