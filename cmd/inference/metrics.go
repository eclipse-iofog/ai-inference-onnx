package main

import (
	"errors"
	"net"
	"net/http"

	"github.com/eclipse-iofog/ai-inference-onnx/internal/metrics"
)

// newMetricsHandler serves GET /metrics from registry.
// Any other method on that path is rejected. Any other path is not found.
// The handler does not read an Authorization header.
func newMetricsHandler(registry *metrics.Registry) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", registry)
	return mux
}

// serveMetrics accepts connections on ln as plain HTTP.
// It does not present a certificate.
func serveMetrics(server *http.Server, ln net.Listener) error {
	err := server.Serve(ln)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
