package main

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/eclipse-iofog/ai-inference-onnx/internal/config"
	"github.com/eclipse-iofog/ai-inference-onnx/internal/natsapi"
)

// startNATS serves the inference subjects when both the NATS server URL and the credentials path are set.
// Otherwise it returns nil and nothing dials.
// The dial runs in the background. A server that cannot be reached is retried and does not stop the process.
// Canceling ctx cancels requests that are queued or running.
func startNATS(ctx context.Context, cfg config.Config, models *modelRuntime, logger *slog.Logger) *natsapi.Listener {
	if !cfg.NATSEnabled() {
		return nil
	}
	handler := natsapi.NewHandler(natsapi.Config{
		Catalog:         models,
		Runner:          models,
		Ready:           models.ready,
		MaxPayloadBytes: cfg.MaxPayloadBytes,
		Buffers:         models.buffers(),
		Context:         ctx,
		Logger:          logger,
	})
	listener, err := natsapi.Dial(natsapi.ListenerConfig{
		URL:       cfg.NATSServerURL,
		CredsPath: cfg.NATSCredsPath,
		CAFile:    cfg.NATSCAFile,
		Handler:   handler,
		Logger:    logger,
	})
	if err != nil {
		logger.Error(fmt.Sprintf("nats: %v", err))
		return nil
	}
	return listener
}
