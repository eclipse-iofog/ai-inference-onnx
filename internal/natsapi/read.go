package natsapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/nats-io/nats.go"

	"github.com/eclipse-iofog/ai-inference-onnx/internal/batch"
)

const (
	modelPlatform = "onnxruntime_onnx"

	indexBodyEmpty    = "repository index body is empty"
	indexBodyInvalid  = "repository index body is invalid"
	indexReadyNotBool = "repository index ready is not a boolean"
)

func (h *Handler) serverDocument() serverMetadata {
	return serverMetadata{
		Name:       h.cfg.Name,
		Version:    h.cfg.Version,
		Extensions: serverExtensions(h.cfg.ExecutionProviders, h.cfg.ORTVersion),
	}
}

func serverExtensions(providers []string, ortVersion string) []string {
	return []string{
		"binary_tensor_data",
		"model_repository",
		"parameters",
		"schedule_policy",
		"execution_providers:" + strings.Join(providers, ","),
		"ort_version:" + ortVersion,
	}
}

// serveReady is 200 when any model can serve and 400 otherwise, with an empty body.
func (h *Handler) serveReady(conn publisher, msg *nats.Msg) {
	if h.cfg.Ready != nil && h.cfg.Ready() {
		h.writeEmpty(conn, msg, http.StatusOK)
		return
	}
	h.writeEmpty(conn, msg, http.StatusBadRequest)
}

func (h *Handler) serveIndex(conn publisher, msg *nats.Msg) {
	readyOnly, err := indexReady(msg.Data)
	if err != nil {
		h.writeError(conn, msg, err)
		return
	}
	h.writeDocument(conn, msg, http.StatusOK, batch.Index(h.directories(), readyOnly))
}

func (h *Handler) serveModelMetadata(conn publisher, msg *nats.Msg, sub Subject) {
	dir, chosen, err := batch.Select(h.directories(), sub.Model, sub.Version)
	if err != nil {
		h.writeError(conn, msg, err)
		return
	}
	h.writeDocument(conn, msg, http.StatusOK, modelMetadata{
		Name:     dir.Name,
		Versions: batch.VersionNames(dir.Versions),
		Platform: modelPlatform,
		Inputs:   tensorDocuments(chosen.Signature.Inputs),
		Outputs:  tensorDocuments(chosen.Signature.Outputs),
	})
}

func (h *Handler) serveModelReady(conn publisher, msg *nats.Msg, sub Subject) {
	if _, _, err := batch.Select(h.directories(), sub.Model, sub.Version); err != nil {
		h.writeError(conn, msg, err)
		return
	}
	h.writeEmpty(conn, msg, http.StatusOK)
}

func indexReady(body []byte) (bool, error) {
	if len(bytes.TrimSpace(body)) == 0 {
		return false, &batch.InvalidError{Reason: indexBodyEmpty}
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil || raw == nil {
		return false, &batch.InvalidError{Reason: indexBodyInvalid}
	}
	value, ok := raw["ready"]
	if !ok {
		return false, nil
	}
	switch string(bytes.TrimSpace(value)) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, &batch.InvalidError{Reason: indexReadyNotBool}
	}
}

func tensorDocuments(specs []batch.TensorSpec) []tensorMetadata {
	out := make([]tensorMetadata, 0, len(specs))
	for _, spec := range specs {
		out = append(out, tensorMetadata{
			Name:     spec.Name,
			Datatype: string(spec.Datatype),
			Shape:    wireShape(spec.Shape),
		})
	}
	return out
}

func wireShape(shape []int64) []int64 {
	out := make([]int64, len(shape))
	for i, dim := range shape {
		out[i] = max(dim, -1)
	}
	return out
}

type serverMetadata struct {
	Name       string   `json:"name"`
	Version    string   `json:"version"`
	Extensions []string `json:"extensions"`
}

type modelList struct {
	Models []string `json:"models"`
}

type modelMetadata struct {
	Name     string           `json:"name"`
	Versions []string         `json:"versions"`
	Platform string           `json:"platform"`
	Inputs   []tensorMetadata `json:"inputs"`
	Outputs  []tensorMetadata `json:"outputs"`
}

type tensorMetadata struct {
	Name     string  `json:"name"`
	Datatype string  `json:"datatype"`
	Shape    []int64 `json:"shape"`
}

type errorDocument struct {
	Error string `json:"error"`
}
