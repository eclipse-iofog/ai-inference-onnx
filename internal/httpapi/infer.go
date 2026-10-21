package httpapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/eclipse-iofog/ai-inference-onnx/internal/batch"
)

const (
	headerContentLength = "Inference-Header-Content-Length"
	payloadTooLarge     = "request body is larger than the payload limit"
	headerLengthInvalid = "Inference-Header-Content-Length must be a non-negative integer"
)

// Runner submits one decoded infer request to the queue of a model version.
// version is the version that serves the call.
type Runner interface {
	Run(ctx context.Context, name, version string, req *batch.Request) (*batch.Reply, error)
}

// InferConfig is the infer route.
type InferConfig struct {
	// Catalog is the model snapshot used to choose a version and decode the body.
	// A nil catalog keeps the catalog already registered.
	Catalog Catalog
	// Runner queues the decoded request. A nil runner reports that the model was not found.
	Runner Runner
	// MaxPayloadBytes is the largest infer body that is read.
	// A larger body is rejected and is not submitted.
	MaxPayloadBytes int
	// Logger records infer outcomes. A nil logger records nothing.
	// Success is debug and includes timing. A rejected caller is warn. A run that did not finish is error.
	// The log does not include the tensor body or the caller's credential.
	Logger *slog.Logger
}

// RegisterInfer serves POST infer for a model and for one version of a model.
func (s *Server) RegisterInfer(cfg InferConfig) {
	if s == nil {
		return
	}
	if cfg.Catalog != nil {
		s.catalog = cfg.Catalog
	}
	s.runner = cfg.Runner
	s.payload = cfg.MaxPayloadBytes
	s.logger = cfg.Logger
	s.mux.HandleFunc("/v2/models/{name}/infer", s.handleInfer)
	s.mux.HandleFunc("/v2/models/{name}/versions/{version}/infer", s.handleInfer)
}

func (s *Server) handleInfer(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	name, requested := modelRef(r.URL.Path)
	if !requireMethod(w, r, http.MethodPost) {
		s.logMethod(name, requested, started)
		return
	}
	dir, chosen, err := batch.Select(s.directories(), name, requested)
	logged := requested
	if err == nil {
		logged = chosen.Version
	}
	if err != nil {
		batch.NoteFailure(s.runner, dir.Name, chosen.Version, err)
		s.logInfer(name, logged, started, err)
		writeStatus(w, err)
		return
	}
	body, err := readPayload(r, s.payload)
	if err != nil {
		batch.NoteFailure(s.runner, dir.Name, chosen.Version, err)
		s.logInfer(name, logged, started, err)
		writeStatus(w, err)
		return
	}
	header, err := inferenceHeaderLength(r)
	if err != nil {
		batch.NoteFailure(s.runner, dir.Name, chosen.Version, err)
		s.logInfer(name, logged, started, err)
		writeStatus(w, err)
		return
	}
	req, err := batch.DecodeHTTP(chosen.Signature, body, header)
	if err != nil {
		batch.NoteFailure(s.runner, dir.Name, chosen.Version, err)
		s.logInfer(name, logged, started, err)
		writeStatus(w, err)
		return
	}
	reply, err := s.run(r.Context(), dir.Name, chosen.Version, req)
	if reply != nil {
		defer reply.Release()
	}
	if err != nil {
		s.logInfer(name, logged, started, err)
		writeStatus(w, err)
		return
	}
	outputs, err := responseOutputs(req, reply)
	if err != nil {
		s.logInfer(name, logged, started, err)
		writeStatus(w, err)
		return
	}
	encoded, err := batch.EncodeHTTP(batch.Response{
		ModelName:    dir.Name,
		ModelVersion: chosen.Version,
		ID:           req.ID,
		Outputs:      outputs,
	})
	if err != nil {
		s.logInfer(name, logged, started, err)
		writeStatus(w, err)
		return
	}
	s.logInfer(name, logged, started, nil)
	writeInfer(w, encoded)
}

func (s *Server) run(ctx context.Context, name, version string, req *batch.Request) (*batch.Reply, error) {
	if s == nil || s.runner == nil {
		return nil, batch.NotFound(fmt.Sprintf("model %q was not found", name))
	}
	return s.runner.Run(ctx, name, version, req)
}

func responseOutputs(req *batch.Request, reply *batch.Reply) ([]batch.ResponseOutput, error) {
	if reply == nil {
		return nil, errors.New("model returned no outputs")
	}
	byName := make(map[string]batch.Tensor, len(reply.Outputs))
	for _, out := range reply.Outputs {
		byName[out.Name] = out
	}
	outputs := make([]batch.ResponseOutput, len(req.Outputs))
	for i, want := range req.Outputs {
		got, ok := byName[want.Name]
		if !ok {
			return nil, fmt.Errorf("output %q was not produced", want.Name)
		}
		shape := got.Shape
		if shape == nil {
			shape = []int64{}
		}
		outputs[i] = batch.ResponseOutput{
			Tensor: batch.Tensor{
				Name:     want.Name,
				Datatype: want.Datatype,
				Shape:    shape,
				Data:     got.Data,
			},
			Binary: want.Binary,
		}
	}
	return outputs, nil
}

func readPayload(r *http.Request, ceiling int) ([]byte, error) {
	if ceiling < 0 {
		ceiling = 0
	}
	if r.ContentLength > int64(ceiling) {
		return nil, &batch.InvalidError{Reason: payloadTooLarge}
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, int64(ceiling)+1))
	if err != nil {
		if cerr := r.Context().Err(); cerr != nil {
			return nil, cerr
		}
		return nil, fmt.Errorf("read infer body: %w", err)
	}
	if len(body) > ceiling {
		return nil, &batch.InvalidError{Reason: payloadTooLarge}
	}
	return body, nil
}

func inferenceHeaderLength(r *http.Request) (int64, error) {
	raw := r.Header.Get(headerContentLength)
	if raw == "" {
		return batch.NoHeaderLength, nil
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 0 {
		return 0, &batch.InvalidError{Reason: headerLengthInvalid}
	}
	return n, nil
}

func writeInfer(w http.ResponseWriter, encoded batch.Encoded) {
	if encoded.Binary {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set(headerContentLength, strconv.Itoa(encoded.HeaderLength))
	} else {
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(encoded.Body); err != nil {
		return
	}
}

func (s *Server) logInfer(name, version string, started time.Time, err error) {
	if s == nil || s.logger == nil {
		return
	}
	result := batch.Status(err)
	msg := "infer finished"
	attrs := []any{
		"model", name,
		"version", version,
		"status", result.HTTP,
		"duration", time.Since(started).String(),
	}
	if err != nil {
		msg = "infer rejected"
		if result.Level >= slog.LevelError {
			msg = "infer did not finish"
		}
		attrs = append(attrs, "error", result.Message)
	}
	s.logger.Log(context.Background(), result.Level, msg, attrs...)
}

func (s *Server) logMethod(name, version string, started time.Time) {
	if s == nil || s.logger == nil {
		return
	}
	s.logger.Warn("infer rejected",
		"model", name,
		"version", version,
		"status", http.StatusMethodNotAllowed,
		"duration", time.Since(started).String(),
		"error", "method not allowed",
	)
}
