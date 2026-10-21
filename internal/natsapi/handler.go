package natsapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/eclipse-iofog/ai-inference-onnx/internal/batch"
)

// Catalog is a read-only snapshot of model directories.
type Catalog interface {
	// Models returns the directories as they stand now.
	Models() []batch.Directory
}

// Runner submits one decoded infer request to the queue of a model version.
// version is the version that serves the call.
type Runner interface {
	Run(ctx context.Context, name, version string, req *batch.Request) (*batch.Reply, error)
}

// Config is what the request handler serves with.
type Config struct {
	// Catalog is the model snapshot used to choose a version and decode the body. Nil is an empty set.
	Catalog Catalog
	// Runner queues decoded requests. A nil runner reports that the model was not found.
	Runner Runner
	// Ready reports whether any model can serve. A nil func means not ready.
	Ready func() bool
	// MaxPayloadBytes is the largest infer body that is decoded. A larger body is rejected and is not submitted.
	MaxPayloadBytes int
	// Buffers holds the body of a rendezvous upload while its slices arrive.
	// Nil allocates each upload body on its own.
	Buffers batch.Pool
	// After starts the timer that abandons a rendezvous upload whose next slice has not arrived.
	// Nil is time.After.
	After func(time.Duration) <-chan time.Time
	// Context is passed to every run. Canceling it at shutdown cancels queued and running requests.
	// Nil is a context that is never canceled.
	Context context.Context
	// Logger records infer outcomes and replies that could not be sent. A nil logger records nothing.
	// Success is debug and includes timing. A rejected caller is warn. A run that did not finish is error.
	// The log does not include the tensor body or any header the caller sent.
	Logger *slog.Logger
}

// Handler answers request messages on the inference subjects.
// Messages are not checked for a caller credential. The NATS server's account rules decide who may publish.
type Handler struct {
	cfg      Config
	inflight sync.WaitGroup
}

// NewHandler returns a handler that serves with cfg.
func NewHandler(cfg Config) *Handler {
	if cfg.Context == nil {
		cfg.Context = context.Background()
	}
	return &Handler{cfg: cfg}
}

// Wait blocks until every request already received has been answered.
// Call it after the subscriptions are closed.
func (h *Handler) Wait() {
	h.inflight.Wait()
}

// receive answers msg on its own goroutine so a long infer does not hold back other requests on the subject.
func (h *Handler) receive(conn publisher, msg *nats.Msg) {
	if msg.Reply == "" {
		return
	}
	h.inflight.Add(1)
	go func() {
		defer h.inflight.Done()
		h.serve(conn, msg)
	}()
}

func (h *Handler) serve(conn publisher, msg *nats.Msg) {
	sub, err := ParseSubject(msg.Subject)
	if err != nil {
		h.writeError(conn, msg, subjectError(err))
		return
	}
	switch sub.Op {
	case OpLive:
		h.writeEmpty(conn, msg, http.StatusOK)
	case OpReady:
		h.serveReady(conn, msg)
	case OpServerMetadata:
		h.writeDocument(conn, msg, http.StatusOK, serverDocument())
	case OpModelList:
		h.writeDocument(conn, msg, http.StatusOK, modelList{Models: batch.Names(h.directories())})
	case OpRepositoryIndex:
		h.serveIndex(conn, msg)
	case OpModelMetadata:
		h.serveModelMetadata(conn, msg, sub)
	case OpModelReady:
		h.serveModelReady(conn, msg, sub)
	case OpModelInfer:
		h.infer(conn, msg, sub)
	default:
		h.writeError(conn, msg, batch.NotFound("subject names no inference call"))
	}
}

// subjectError is 400 for a wildcard and 404 for a subject that names no call.
func subjectError(err error) error {
	if errors.Is(err, ErrWildcard) {
		return &batch.InvalidError{Reason: err.Error()}
	}
	return batch.NotFound(err.Error())
}

func (h *Handler) directories() []batch.Directory {
	if h.cfg.Catalog == nil {
		return nil
	}
	return h.cfg.Catalog.Models()
}

func (h *Handler) writeEmpty(conn publisher, msg *nats.Msg, status int) {
	h.send(conn, msg, status, nil, nil)
}

func (h *Handler) writeError(conn publisher, msg *nats.Msg, err error) {
	result := batch.Status(err)
	h.writeDocument(conn, msg, result.HTTP, errorDocument{Error: result.Message})
}

func (h *Handler) writeDocument(conn publisher, msg *nats.Msg, status int, document any) {
	body, err := json.Marshal(document)
	if err != nil {
		status = http.StatusInternalServerError
		body = []byte(`{"error":"response could not be encoded"}`)
	}
	h.send(conn, msg, status, nats.Header{HeaderContentType: []string{contentJSON}}, body)
}

func (h *Handler) send(conn publisher, msg *nats.Msg, status int, header nats.Header, body []byte) {
	err := writeReply(conn, msg.Reply, status, header, body)
	if err != nil && h.cfg.Logger != nil {
		h.cfg.Logger.Error("reply could not be sent", "subject", msg.Subject, "status", status, "error", err.Error())
	}
}
