//go:build !cgo

package session

import (
	"context"
	"errors"
)

// errRuntimeUnavailable is returned when the process was built without the runtime library.
var errRuntimeUnavailable = errors.New("onnx runtime library is unavailable")

// Repository is the set of loaded models.
// Without the runtime library the set is empty.
type Repository struct{}

// Load reports that no model can be loaded.
// The returned repository has an empty model set.
// OnState is accepted and is not called, because this build does not load models.
func Load(cfg Config, pool *Pool) (*Repository, error) {
	repo := &Repository{}
	repo.OnState(cfg.OnState)
	return repo, errRuntimeUnavailable
}

// OnState accepts a state function.
// This build does not load models, so the function is not called.
func (*Repository) OnState(StateFunc) {}

// Admit reports that the model was not found.
func (r *Repository) Admit(name, version string) Admission {
	return Admission{Status: StatusMissing, Reason: "model was not found"}
}

// Run reports that a model cannot be executed.
func (r *Repository) Run(ctx context.Context, name, version string, inputs []Input) ([]Output, error) {
	return nil, errRuntimeUnavailable
}

// Models returns an empty set.
func (r *Repository) Models() []Model {
	return nil
}

// Catalog returns no directories.
func (r *Repository) Catalog() []CatalogModel {
	return nil
}

// Release does nothing.
func (r *Repository) Release(out Output) {}

// Close does nothing.
func (r *Repository) Close() error {
	return nil
}
