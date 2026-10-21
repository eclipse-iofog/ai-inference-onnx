//go:build !cgo

package main

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/eclipse-iofog/ai-inference-onnx/internal/batch"
	"github.com/eclipse-iofog/ai-inference-onnx/internal/config"
	"github.com/eclipse-iofog/ai-inference-onnx/internal/httpapi"
	"github.com/eclipse-iofog/ai-inference-onnx/internal/metrics"
)

var (
	_ httpapi.Catalog = (*modelRuntime)(nil)
	_ httpapi.Runner  = (*modelRuntime)(nil)
)

// modelRuntime is the set of models loaded for this process.
// This build does not load the runtime library, so the set stays empty and ready stays false.
type modelRuntime struct{}

// startModels leaves the model set empty.
// notes is unused because this build does not load models.
func startModels(config.Config, *slog.Logger, chan<- []metrics.ModelState) *modelRuntime {
	return &modelRuntime{}
}

// modelStates returns no versions. This build does not load models.
func (m *modelRuntime) modelStates() []metrics.ModelState {
	return []metrics.ModelState{}
}

// ready reports that no model can serve.
func (m *modelRuntime) ready() bool {
	var states []httpapi.State
	return httpapi.AnyReady(states...)
}

// Models returns no directories. This build does not load the runtime library.
func (m *modelRuntime) Models() []batch.Directory {
	return nil
}

// useRegistry counts nothing. This build does not load models.
func (m *modelRuntime) useRegistry(*metrics.Registry) {}

// Run reports that the model was not found. This build does not load the runtime library.
func (m *modelRuntime) Run(_ context.Context, name, _ string, _ *batch.Request) (*batch.Reply, error) {
	return nil, batch.NotFound(fmt.Sprintf("model %q was not found", name))
}

// buffers is nil. This build has no session buffer pool.
func (m *modelRuntime) buffers() batch.Pool {
	return nil
}

// shutdown releases nothing.
func (m *modelRuntime) shutdown() error {
	return nil
}
