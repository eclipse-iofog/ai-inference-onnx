package natsapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/eclipse-iofog/ai-inference-onnx/internal/batch"
)

const (
	payloadTooLarge     = "request body is larger than the payload limit"
	headerLengthInvalid = "Inference-Header-Content-Length must be a non-negative integer"
	transferRepeated    = "Inference-Transfer must have one value"
)

// transfer serves an infer request whose body does not travel in the request message.
// It writes the reply stream itself.
type transfer func(h *Handler, conn publisher, msg *nats.Msg, sub Subject, started time.Time)

// transfers are the Inference-Transfer values this server accepts.
// A value that is not listed is refused before a model is chosen.
var transfers = map[string]transfer{
	transferRendezvous: rendezvous,
}

// infer serves an infer subject. Without Inference-Transfer the request message holds the whole REST body.
func (h *Handler) infer(conn publisher, msg *nats.Msg, sub Subject) {
	started := time.Now()
	values := msg.Header.Values(HeaderTransfer)
	if len(values) == 0 {
		h.inline(conn, msg, sub, started)
		return
	}
	if len(values) > 1 {
		h.refuseInfer(conn, msg, sub, started, &batch.InvalidError{Reason: transferRepeated})
		return
	}
	run, ok := transfers[values[0]]
	if !ok {
		h.refuseInfer(conn, msg, sub, started, &batch.InvalidError{Reason: fmt.Sprintf("Inference-Transfer %q is not supported", values[0])})
		return
	}
	run(h, conn, msg, sub, started)
}

func (h *Handler) refuseInfer(conn publisher, msg *nats.Msg, sub Subject, started time.Time, err error) {
	h.logInfer(sub.Model, sub.Version, "", started, err)
	h.writeError(conn, msg, err)
}

// inline decodes the request message body with the HTTP codec, runs it, and writes the HTTP result.
func (h *Handler) inline(conn publisher, msg *nats.Msg, sub Subject, started time.Time) {
	dir, chosen, err := batch.Select(h.directories(), sub.Model, sub.Version)
	if err != nil {
		batch.NoteFailure(h.cfg.Runner, dir.Name, chosen.Version, err)
		h.refuseInfer(conn, msg, sub, started, err)
		return
	}
	encoded, id, err := h.inlineBody(dir, chosen, msg)
	h.logInfer(sub.Model, chosen.Version, id, started, err)
	if err != nil {
		h.writeError(conn, msg, err)
		return
	}
	h.writeInfer(conn, msg, encoded)
}

func (h *Handler) inlineBody(dir batch.Directory, chosen batch.ModelVersion, msg *nats.Msg) (batch.Encoded, string, error) {
	if len(msg.Data) > max(h.cfg.MaxPayloadBytes, 0) {
		err := &batch.InvalidError{Reason: payloadTooLarge}
		batch.NoteFailure(h.cfg.Runner, dir.Name, chosen.Version, err)
		return batch.Encoded{}, "", err
	}
	headerLength, err := inferenceHeaderLength(msg.Header)
	if err != nil {
		batch.NoteFailure(h.cfg.Runner, dir.Name, chosen.Version, err)
		return batch.Encoded{}, "", err
	}
	return h.execute(dir, chosen, msg.Data, headerLength)
}

// execute decodes one HTTP infer body, queues it on the chosen version, and encodes the HTTP response body.
// headerLength is batch.NoHeaderLength when the request did not set Inference-Header-Content-Length.
func (h *Handler) execute(dir batch.Directory, chosen batch.ModelVersion, body []byte, headerLength int64) (batch.Encoded, string, error) {
	req, err := batch.DecodeHTTP(chosen.Signature, body, headerLength)
	if err != nil {
		batch.NoteFailure(h.cfg.Runner, dir.Name, chosen.Version, err)
		return batch.Encoded{}, "", err
	}
	reply, err := h.run(h.cfg.Context, dir.Name, chosen.Version, req)
	if reply != nil {
		defer reply.Release()
	}
	if err != nil {
		return batch.Encoded{}, req.ID, err
	}
	outputs, err := responseOutputs(req, reply)
	if err != nil {
		return batch.Encoded{}, req.ID, err
	}
	encoded, err := batch.EncodeHTTP(batch.Response{
		ModelName:    dir.Name,
		ModelVersion: chosen.Version,
		ID:           req.ID,
		Outputs:      outputs,
	})
	return encoded, req.ID, err
}

func (h *Handler) run(ctx context.Context, name, version string, req *batch.Request) (*batch.Reply, error) {
	if h.cfg.Runner == nil {
		return nil, batch.NotFound(fmt.Sprintf("model %q was not found", name))
	}
	return h.cfg.Runner.Run(ctx, name, version, req)
}

func (h *Handler) writeInfer(conn publisher, msg *nats.Msg, encoded batch.Encoded) {
	header := nats.Header{HeaderContentType: []string{contentJSON}}
	if encoded.Binary {
		header.Set(HeaderContentType, contentBinary)
		header.Set(HeaderContentLength, strconv.Itoa(encoded.HeaderLength))
	}
	h.send(conn, msg, http.StatusOK, header, encoded.Body)
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

func inferenceHeaderLength(header nats.Header) (int64, error) {
	raw := header.Get(HeaderContentLength)
	if raw == "" {
		return batch.NoHeaderLength, nil
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 0 {
		return 0, &batch.InvalidError{Reason: headerLengthInvalid}
	}
	return n, nil
}

func (h *Handler) logInfer(name, version, id string, started time.Time, err error) {
	batch.LogInfer(h.cfg.Context, h.cfg.Logger, "nats", name, version, id, started, err)
}
