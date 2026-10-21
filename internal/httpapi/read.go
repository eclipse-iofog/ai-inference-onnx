package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/eclipse-iofog/ai-inference-onnx/internal/batch"
)

const (
	serverName     = "inference"
	serverVersion  = "dev"
	modelPlatform  = "onnxruntime_onnx"
	repositoryNote = "loading and unloading models is not supported"

	indexBodyEmpty    = "repository index body is empty"
	indexBodyInvalid  = "repository index body is invalid"
	indexReadyNotBool = "repository index ready is not a boolean"
)

// Catalog is a read-only snapshot of model directories.
type Catalog interface {
	// Models returns the directories as they stand now.
	Models() []batch.Directory
}

type emptyCatalog struct{}

func (emptyCatalog) Models() []batch.Directory { return nil }

// RegisterReads serves server metadata, the model list, model metadata, model ready, and the repository index.
// Load and unload report that this server does not load or unload models.
// Inference is not registered.
func (s *Server) RegisterReads(catalog Catalog) {
	if s == nil {
		return
	}
	if catalog == nil {
		catalog = emptyCatalog{}
	}
	s.catalog = catalog
	s.mux.HandleFunc("/v2", s.handleServerMetadata)
	s.mux.HandleFunc("/v2/models", s.handleModelList)
	s.mux.HandleFunc("/v2/models/{name}", s.handleModelMetadata)
	s.mux.HandleFunc("/v2/models/{name}/ready", s.handleModelReady)
	s.mux.HandleFunc("/v2/models/{name}/versions/{version}", s.handleModelMetadata)
	s.mux.HandleFunc("/v2/models/{name}/versions/{version}/ready", s.handleModelReady)
	s.mux.HandleFunc("/v2/repository/index", s.handleRepositoryIndex)
	s.mux.HandleFunc("/v2/repository/models/{name}/load", s.handleRepositoryChange)
	s.mux.HandleFunc("/v2/repository/models/{name}/unload", s.handleRepositoryChange)
}

func (s *Server) handleServerMetadata(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	writeDocument(w, http.StatusOK, serverMetadata{
		Name:    serverName,
		Version: serverVersion,
		Extensions: []string{
			"binary_tensor_data",
			"model_repository",
			"parameters",
			"schedule_policy",
		},
	})
}

func (s *Server) handleModelList(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	writeDocument(w, http.StatusOK, modelList{Models: batch.Names(s.directories())})
}

func (s *Server) handleModelMetadata(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	name, version := modelRef(r.URL.Path)
	dir, chosen, err := batch.Select(s.directories(), name, version)
	if err != nil {
		writeStatus(w, err)
		return
	}
	writeDocument(w, http.StatusOK, modelMetadata{
		Name:     dir.Name,
		Versions: batch.VersionNames(dir.Versions),
		Platform: modelPlatform,
		Inputs:   tensorDocuments(chosen.Signature.Inputs),
		Outputs:  tensorDocuments(chosen.Signature.Outputs),
	})
}

func (s *Server) handleModelReady(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	name, version := modelRef(r.URL.Path)
	if _, _, err := batch.Select(s.directories(), name, version); err != nil {
		writeStatus(w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleRepositoryIndex(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeStatus(w, &batch.InvalidError{Reason: indexBodyInvalid})
		return
	}
	readyOnly, err := indexReady(body)
	if err != nil {
		writeStatus(w, err)
		return
	}
	writeDocument(w, http.StatusOK, batch.Index(s.directories(), readyOnly))
}

func (s *Server) handleRepositoryChange(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	writeStatus(w, batch.Unimplemented(repositoryNote))
}

func (s *Server) directories() []batch.Directory {
	if s == nil || s.catalog == nil {
		return nil
	}
	return s.catalog.Models()
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
	ready, ok := jsonBool(value)
	if !ok {
		return false, &batch.InvalidError{Reason: indexReadyNotBool}
	}
	return ready, nil
}

func jsonBool(value json.RawMessage) (bool, bool) {
	switch string(bytes.TrimSpace(value)) {
	case "true":
		return true, true
	case "false":
		return false, true
	default:
		return false, false
	}
}

func modelRef(path string) (string, string) {
	parts := splitPath(path)
	if len(parts) < 3 {
		return "", ""
	}
	name := parts[2]
	if len(parts) >= 5 && parts[3] == "versions" {
		return name, parts[4]
	}
	return name, ""
}

func splitPath(path string) []string {
	path = strings.Trim(path, "/")
	if path == "" {
		return nil
	}
	return strings.Split(path, "/")
}

func requireMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method == method {
		return true
	}
	writeAllowed(w, method)
	return false
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
	if len(shape) == 0 {
		return []int64{}
	}
	out := make([]int64, len(shape))
	for i, dim := range shape {
		if dim < 0 {
			out[i] = -1
			continue
		}
		out[i] = dim
	}
	return out
}

func writeStatus(w http.ResponseWriter, err error) {
	result := batch.Status(err)
	writeDocument(w, result.HTTP, errorDocument{Error: result.Message})
}

func writeDocument(w http.ResponseWriter, status int, document any) {
	body, err := json.Marshal(document)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, `{"error":"response could not be encoded"}`)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if _, err := w.Write(body); err != nil {
		return
	}
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
