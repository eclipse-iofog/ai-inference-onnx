package batch

import (
	"context"
	"log/slog"
	"time"
)

// maxRequestIDBytes is the longest caller request id copied onto a log line.
const maxRequestIDBytes = 256

// LogInfer writes one infer outcome.
// transport is http, grpc, or nats.
// id is the caller request id. An id longer than 256 bytes is cut. An empty id is recorded as empty.
// A nil logger writes nothing.
// Success is debug. A rejected caller is warn. A run that did not finish is error.
// The line names the transport, model, version, request id, HTTP status, and duration in microseconds.
// gRPC also names the status code. A failure adds the error text and its class.
// The line does not include tensor values or a caller credential.
func LogInfer(ctx context.Context, logger *slog.Logger, transport, model, version, id string, started time.Time, err error) {
	LogInferResult(ctx, logger, transport, model, version, id, started, Status(err))
}

// LogInferResult writes one infer outcome that already has its status row.
// A nil logger writes nothing.
func LogInferResult(ctx context.Context, logger *slog.Logger, transport, model, version, id string, started time.Time, result Result) {
	if logger == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	attrs := []any{
		"transport", transport,
		"model", model,
		"version", version,
		"id", cutRequestID(id),
		"status", result.HTTP,
		"duration_us", time.Since(started).Microseconds(),
	}
	if transport == "grpc" {
		attrs = append(attrs, "grpc_code", result.GRPC.String())
	}
	if result.Message != "" {
		attrs = append(attrs, "error", result.Message, "error_kind", result.ErrorKind)
	}
	logger.Log(ctx, result.Level, "infer", attrs...)
}

func cutRequestID(id string) string {
	if len(id) <= maxRequestIDBytes {
		return id
	}
	return id[:maxRequestIDBytes]
}
