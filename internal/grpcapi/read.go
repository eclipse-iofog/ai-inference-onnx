package grpcapi

import (
	"context"

	"google.golang.org/grpc/status"

	"github.com/eclipse-iofog/ai-inference-onnx/api/openinference/v2"
	"github.com/eclipse-iofog/ai-inference-onnx/internal/batch"
)

const (
	serverName     = "inference"
	serverVersion  = "dev"
	modelPlatform  = "onnxruntime_onnx"
	repositoryNote = "loading and unloading models is not supported"

	repositoryNameRejected = "repository name must be empty"
)

// Catalog is a read-only snapshot of model directories.
type Catalog interface {
	// Models returns the directories as they stand now.
	Models() []batch.Directory
}

type emptyCatalog struct{}

func (emptyCatalog) Models() []batch.Directory { return nil }

// RegisterReads serves server metadata, model metadata, model ready, and the repository index from catalog.
// Load and unload report that this server does not load or unload models.
// A nil catalog is an empty repository.
func (s *Server) RegisterReads(catalog Catalog) {
	if s == nil {
		return
	}
	if catalog == nil {
		catalog = emptyCatalog{}
	}
	s.catalog = catalog
}

// ServerMetadata reports the server name, version, and extensions.
func (s *Server) ServerMetadata(context.Context, *openinference.ServerMetadataRequest) (*openinference.ServerMetadataResponse, error) {
	return &openinference.ServerMetadataResponse{
		Name:    serverName,
		Version: serverVersion,
		Extensions: []string{
			"binary_tensor_data",
			"model_repository",
			"parameters",
			"schedule_policy",
		},
	}, nil
}

// ModelMetadata reports the selected version's signature.
// A version the policy did not select is not found. A model that cannot serve keeps its stored reason.
func (s *Server) ModelMetadata(_ context.Context, req *openinference.ModelMetadataRequest) (*openinference.ModelMetadataResponse, error) {
	dir, chosen, err := batch.Select(s.directories(), req.GetName(), req.GetVersion())
	if err != nil {
		return nil, statusFrom(err)
	}
	return &openinference.ModelMetadataResponse{
		Name:     dir.Name,
		Versions: batch.VersionNames(dir.Versions),
		Platform: modelPlatform,
		Inputs:   tensorMetadata(chosen.Signature.Inputs),
		Outputs:  tensorMetadata(chosen.Signature.Outputs),
	}, nil
}

// ModelReady reports that the selected version can serve.
// A version that is not ready returns the stored reason instead of a false ready flag.
func (s *Server) ModelReady(_ context.Context, req *openinference.ModelReadyRequest) (*openinference.ModelReadyResponse, error) {
	if _, _, err := batch.Select(s.directories(), req.GetName(), req.GetVersion()); err != nil {
		return nil, statusFrom(err)
	}
	return &openinference.ModelReadyResponse{Ready: true}, nil
}

// RepositoryIndex lists this process's repository.
// An empty repository name is that repository. Any other name is rejected.
// ready keeps only versions that can serve.
func (s *Server) RepositoryIndex(_ context.Context, req *openinference.RepositoryIndexRequest) (*openinference.RepositoryIndexResponse, error) {
	if req.GetRepositoryName() != "" {
		return nil, statusFrom(&batch.InvalidError{Reason: repositoryNameRejected})
	}
	rows := batch.Index(s.directories(), req.GetReady())
	models := make([]*openinference.RepositoryIndexResponse_ModelIndex, 0, len(rows))
	for _, row := range rows {
		models = append(models, &openinference.RepositoryIndexResponse_ModelIndex{
			Name:    row.Name,
			Version: row.Version,
			State:   row.State,
			Reason:  row.Reason,
		})
	}
	return &openinference.RepositoryIndexResponse{Models: models}, nil
}

// RepositoryModelLoad reports that this server does not load models.
// The model name is not looked up.
func (s *Server) RepositoryModelLoad(context.Context, *openinference.RepositoryModelLoadRequest) (*openinference.RepositoryModelLoadResponse, error) {
	return nil, statusFrom(batch.Unimplemented(repositoryNote))
}

// RepositoryModelUnload reports that this server does not unload models.
// The model name is not looked up.
func (s *Server) RepositoryModelUnload(context.Context, *openinference.RepositoryModelUnloadRequest) (*openinference.RepositoryModelUnloadResponse, error) {
	return nil, statusFrom(batch.Unimplemented(repositoryNote))
}

func (s *Server) directories() []batch.Directory {
	if s == nil || s.catalog == nil {
		return nil
	}
	return s.catalog.Models()
}

func statusFrom(err error) error {
	result := batch.Status(err)
	return status.Error(result.GRPC, result.Message)
}

func tensorMetadata(specs []batch.TensorSpec) []*openinference.ModelMetadataResponse_TensorMetadata {
	out := make([]*openinference.ModelMetadataResponse_TensorMetadata, 0, len(specs))
	for _, spec := range specs {
		out = append(out, &openinference.ModelMetadataResponse_TensorMetadata{
			Name:     spec.Name,
			Datatype: string(spec.Datatype),
			Shape:    wireShape(spec.Shape),
		})
	}
	return out
}

// wireShape reports a variable dimension as -1.
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
