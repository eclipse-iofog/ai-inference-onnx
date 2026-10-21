package batch

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"google.golang.org/grpc/codes"
)

// Kind is the class of a refused or failed infer call.
type Kind int

const (
	// KindRuntime is a run the model could not finish.
	KindRuntime Kind = iota
	// KindLoading is a model that is being loaded.
	KindLoading
	// KindNotFound is an unknown model name, or a version the policy did not select.
	KindNotFound
	// KindUnavailable is a model that is known and cannot run. The reason is the stored load reason.
	KindUnavailable
	// KindQueueFull is a version whose queue already holds its waiting limit.
	KindQueueFull
	// KindQueueTimeout is a request whose timeout parameter elapsed while it waited in the queue.
	KindQueueTimeout
	// KindUnimplemented is an operation this engine does not perform.
	KindUnimplemented
)

const (
	queueFullText    = "request queue for this model is full"
	queueTimeoutText = "request timeout elapsed while waiting in the queue"
	deadlineText     = "call deadline elapsed"
	canceledText     = "call was canceled"
	runtimeText      = "model run failed"
)

// Error is a refused or failed infer call. Its text never contains tensor values.
type Error struct {
	// Kind selects the status row.
	Kind Kind
	// Reason is the text returned to the caller.
	Reason string
}

// Error returns the reason.
func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return e.Reason
}

// ErrQueueFull is returned when a version's queue already holds its waiting limit. The request was not queued.
var ErrQueueFull error = &Error{Kind: KindQueueFull, Reason: queueFullText}

// ErrQueueTimeout is returned when a request's timeout parameter elapsed while it waited in the queue.
var ErrQueueTimeout error = &Error{Kind: KindQueueTimeout, Reason: queueTimeoutText}

// Loading returns the result for a model that is being loaded. reason is the text the repository recorded.
func Loading(reason string) error {
	return &Error{Kind: KindLoading, Reason: reason}
}

// NotFound returns the result for an unknown model name or a version the policy did not select.
func NotFound(reason string) error {
	return &Error{Kind: KindNotFound, Reason: reason}
}

// Unavailable returns the result for a model that cannot run. reason is the stored load reason.
func Unavailable(reason string) error {
	return &Error{Kind: KindUnavailable, Reason: reason}
}

// Unimplemented returns the result for an operation this engine does not perform.
func Unimplemented(reason string) error {
	return &Error{Kind: KindUnimplemented, Reason: reason}
}

// GateError converts a call-gate answer into a result.
// status is 503 while the model is loading and 404 for an unknown name or version.
// Any other status with a non-empty reason is an unavailable model. Otherwise the call may proceed and the result is nil.
func GateError(status int, reason string) error {
	switch {
	case status == http.StatusServiceUnavailable:
		return Loading(reason)
	case status == http.StatusNotFound:
		return NotFound(reason)
	case reason != "":
		return Unavailable(reason)
	default:
		return nil
	}
}

// Result is how one transport writes an infer outcome.
type Result struct {
	// HTTP is the response status code.
	HTTP int
	// GRPC is the status code.
	GRPC codes.Code
	// Message is the error text. It is empty on success.
	Message string
	// Level is the log level of the outcome: debug for success, warn for a rejected caller, error for a failed run.
	Level slog.Level
}

// Status maps an infer outcome to its HTTP and gRPC result. A nil error is success.
// A context deadline and a canceled context are their own rows. An error this package did not classify is a runtime failure.
func Status(err error) Result {
	if err == nil {
		return Result{HTTP: http.StatusOK, GRPC: codes.OK, Level: slog.LevelDebug}
	}
	var invalid *InvalidError
	if errors.As(err, &invalid) {
		return rejected(http.StatusBadRequest, codes.InvalidArgument, invalid.Error())
	}
	var e *Error
	if errors.As(err, &e) {
		switch e.Kind {
		case KindLoading, KindQueueFull:
			return rejected(http.StatusServiceUnavailable, codes.Unavailable, e.Reason)
		case KindNotFound:
			return rejected(http.StatusNotFound, codes.NotFound, e.Reason)
		case KindUnavailable:
			return rejected(http.StatusBadRequest, codes.FailedPrecondition, e.Reason)
		case KindQueueTimeout:
			return rejected(http.StatusGatewayTimeout, codes.DeadlineExceeded, e.Reason)
		case KindUnimplemented:
			return rejected(http.StatusNotImplemented, codes.Unimplemented, e.Reason)
		default:
		}
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return rejected(http.StatusGatewayTimeout, codes.DeadlineExceeded, deadlineText)
	case errors.Is(err, context.Canceled):
		return rejected(http.StatusBadRequest, codes.Canceled, canceledText)
	default:
	}
	return Result{
		HTTP:    http.StatusInternalServerError,
		GRPC:    codes.Internal,
		Message: runtimeText + ": " + err.Error(),
		Level:   slog.LevelError,
	}
}

func rejected(httpStatus int, code codes.Code, message string) Result {
	return Result{HTTP: httpStatus, GRPC: code, Message: message, Level: slog.LevelWarn}
}
