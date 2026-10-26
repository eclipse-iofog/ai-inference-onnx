package main

import (
	"io"
	"log"
	"log/slog"

	"github.com/eclipse-iofog/ai-inference-onnx/internal/buildinfo"
	"github.com/eclipse-iofog/ai-inference-onnx/internal/config"
	"github.com/eclipse-iofog/ai-inference-onnx/internal/session"
)

const serviceName = "inference"

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

// logStart records the settings a process is serving with.
// The static token, NATS URL, credentials path, and certificate paths stay out of the line.
func logStart(logger *slog.Logger, cfg config.Config) {
	if logger == nil {
		return
	}
	logger.Info("start",
		"version", buildinfo.Version,
		"auth_mode", cfg.AuthMode,
		"http_port", cfg.HTTPPort,
		"grpc_port", cfg.GRPCPort,
		"metrics_port", cfg.MetricsPort,
		"tls", cfg.TLSEnabled,
		"models_path", cfg.ModelsPath,
		"knowledge_path", cfg.KnowledgePath,
		"queue_capacity", cfg.QueueCapacity,
		"max_payload_bytes", cfg.MaxPayloadBytes,
		"intra_op_threads", cfg.IntraOpThreads,
		"inter_op_threads", cfg.InterOpThreads,
		"batch_delay_us", cfg.BatchDelayMicroseconds,
		"metrics_interval", cfg.MetricsInterval.String(),
		"nats_enabled", cfg.NATSEnabled(),
	)
}

// logModelState records a model state that has already been stored.
// Ready and the start of a load are info. Unavailable is warn and includes the load reason.
func logModelState(logger *slog.Logger, name string, state session.State, models []session.CatalogModel) {
	if logger == nil {
		return
	}
	attrs := []any{"model", name, "state", string(state)}
	if state == session.Unavailable {
		if reason := modelReason(name, models); reason != "" {
			attrs = append(attrs, "reason", reason)
		}
		logger.Warn("model", attrs...)
		return
	}
	switch state {
	case session.Ready, session.Loading:
		logger.Info("model", attrs...)
	}
}

func modelReason(name string, models []session.CatalogModel) string {
	for _, model := range models {
		if model.Name == name {
			return model.Reason
		}
	}
	return ""
}
