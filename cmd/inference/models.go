//go:build cgo

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/eclipse-iofog/ai-inference-onnx/internal/batch"
	"github.com/eclipse-iofog/ai-inference-onnx/internal/config"
	"github.com/eclipse-iofog/ai-inference-onnx/internal/httpapi"
	"github.com/eclipse-iofog/ai-inference-onnx/internal/metrics"
	"github.com/eclipse-iofog/ai-inference-onnx/internal/ort"
	"github.com/eclipse-iofog/ai-inference-onnx/internal/session"
)

var (
	_ httpapi.Catalog = (*modelRuntime)(nil)
	_ httpapi.Runner  = (*modelRuntime)(nil)
)

// modelRuntime is the set of models loaded for this process.
type modelRuntime struct {
	repo      *session.Repository
	watcher   *session.Watcher
	started   bool
	providers []ort.Provider
	queues    inferQueues

	mu    sync.Mutex
	notes chan<- []metrics.ModelState
}

// startModels loads the shared library, opens one session per ready model version,
// and watches the model and knowledge directories.
// Thread counts come from server settings. Each session uses the provider chain the loaded library supports.
// A library that cannot be loaded, or a models directory that cannot be read, leaves the set empty.
// The process still answers, and ready stays false until a model can serve.
// notes receives a copy of the model list after the load and after each later state change.
func startModels(cfg config.Config, logger *slog.Logger, notes chan<- []metrics.ModelState) *modelRuntime {
	if err := ort.Init(); err != nil {
		logger.Error(fmt.Sprintf("runtime: %v", err))
		return &modelRuntime{}
	}
	running := &modelRuntime{started: true}
	available, err := ort.AvailableProviders()
	if err != nil {
		logger.Error(fmt.Sprintf("runtime: %v", err))
		return running
	}
	cacheRoot, err := os.UserCacheDir()
	if err != nil {
		logger.Warn(fmt.Sprintf("user cache directory: %v", err))
		cacheRoot = ""
	}
	running.providers = ort.SelectProviders(available, cacheRoot)
	logExecutionProviders(logger, running.providers)
	repo, pool, err := openRepository(cfg, logger, running)
	if err != nil {
		logger.Error(fmt.Sprintf("models: %v", err))
		return running
	}
	running.repo = repo
	running.queues.capacity = cfg.QueueCapacity
	running.queues.delayDefault = delayFromMicroseconds(cfg.BatchDelayMicroseconds)
	running.queues.pool = bufferPool{pool: pool}
	running.queues.logger = logger
	running.queues.slot = running.slot
	running.queues.admit = running.admit
	running.queues.settings = running.batchSettings
	running.forwardEnable(notes)
	running.emit(notes)
	watcher, err := watchRepository(cfg, repo, running.providers)
	if err != nil {
		logger.Error(fmt.Sprintf("watch: %v", err))
		return running
	}
	running.watcher = watcher
	return running
}

func openRepository(cfg config.Config, logger *slog.Logger, running *modelRuntime) (*session.Repository, *session.Pool, error) {
	pool, err := session.NewPool(cfg.MaxPayloadBytes)
	if err != nil {
		return nil, nil, err
	}
	sessCfg := sessionConfig(cfg, running.providers)
	sessCfg.OnState = func(name string, state session.State, models []session.CatalogModel) {
		logModelState(logger, name, state)
		running.forward(models)
	}
	repo, err := session.Load(sessCfg, pool)
	if err != nil {
		return nil, nil, err
	}
	return repo, pool, nil
}

func (m *modelRuntime) forwardEnable(notes chan<- []metrics.ModelState) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.notes = notes
	m.mu.Unlock()
}

// emit sends the current model list. It does not run from the state callback.
func (m *modelRuntime) emit(notes chan<- []metrics.ModelState) {
	if m == nil || m.repo == nil || notes == nil {
		return
	}
	select {
	case notes <- modelStatesFrom(m.repo.Catalog()):
	default:
	}
}

// forward sends the copy the loader already took. It does not read the repository again.
func (m *modelRuntime) forward(models []session.CatalogModel) {
	if m == nil {
		return
	}
	m.mu.Lock()
	notes := m.notes
	m.mu.Unlock()
	if notes == nil {
		return
	}
	select {
	case notes <- modelStatesFrom(models):
	default:
	}
}

// modelStates returns a copy of the loaded model list.
func (m *modelRuntime) modelStates() []metrics.ModelState {
	if m == nil || m.repo == nil {
		return []metrics.ModelState{}
	}
	return modelStatesFrom(m.repo.Catalog())
}

func modelStatesFrom(models []session.CatalogModel) []metrics.ModelState {
	out := make([]metrics.ModelState, 0)
	for _, model := range models {
		for _, version := range model.Versions {
			out = append(out, metrics.ModelState{
				Model:   model.Name,
				Version: version.Version,
				State:   metrics.State(model.State),
				Reason:  model.Reason,
			})
		}
	}
	return out
}

func watchRepository(cfg config.Config, repo *session.Repository, providers []ort.Provider) (*session.Watcher, error) {
	watchCfg := sessionConfig(cfg, providers)
	// A missing knowledge directory is not watched.
	// Models still load, and a named item that is absent stays unavailable.
	if !directory(cfg.KnowledgePath) {
		watchCfg.KnowledgeRoot = ""
	}
	return session.Watch(context.Background(), repo, watchCfg, session.PollPeriod, session.QuietPeriod)
}

func sessionConfig(cfg config.Config, providers []ort.Provider) session.Config {
	return session.Config{
		ModelsRoot:    cfg.ModelsPath,
		KnowledgeRoot: cfg.KnowledgePath,
		IntraOp:       cfg.IntraOpThreads,
		InterOp:       cfg.InterOpThreads,
		Providers:     providers,
	}
}

func logExecutionProviders(logger *slog.Logger, providers []ort.Provider) {
	if logger == nil {
		return
	}
	names := make([]string, len(providers))
	for i, provider := range providers {
		names[i] = provider.Name
	}
	logger.Info("execution providers: " + strings.Join(names, ", "))
}

func directory(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}

// Models returns the directories currently recorded.
// An empty runtime has none.
func (m *modelRuntime) Models() []batch.Directory {
	if m == nil || m.repo == nil {
		return nil
	}
	return directoriesFrom(m.repo.Catalog())
}

func directoriesFrom(models []session.CatalogModel) []batch.Directory {
	if len(models) == 0 {
		return nil
	}
	out := make([]batch.Directory, len(models))
	for i, model := range models {
		versions := make([]batch.ModelVersion, len(model.Versions))
		for j, version := range model.Versions {
			versions[j] = batch.ModelVersion{
				Version: version.Version,
				Signature: batch.Signature{
					Inputs:       tensorSpecs(version.Inputs),
					Outputs:      tensorSpecs(version.Outputs),
					MaxBatchSize: version.MaxBatchSize,
				},
			}
		}
		out[i] = batch.Directory{
			Name:     model.Name,
			State:    string(model.State),
			Reason:   model.Reason,
			Versions: versions,
		}
	}
	return out
}

func tensorSpecs(tensors []session.CatalogTensor) []batch.TensorSpec {
	out := make([]batch.TensorSpec, len(tensors))
	for i, tensor := range tensors {
		shape := append([]int64(nil), tensor.Shape...)
		if len(shape) == 0 {
			shape = []int64{}
		}
		out[i] = batch.TensorSpec{
			Name:     tensor.Name,
			Datatype: batch.Datatype(tensor.Datatype),
			Shape:    shape,
		}
	}
	return out
}

// useRegistry counts finished requests on reg.
// Nil leaves the queues uncounted.
func (m *modelRuntime) useRegistry(reg *metrics.Registry) {
	if m == nil || reg == nil {
		return
	}
	m.queues.recorder = reg
}

// ReportFailure counts a request that failed before the queue accepted it.
// A missing model is not counted.
func (m *modelRuntime) ReportFailure(model, version string, err error) {
	if m == nil {
		return
	}
	batch.RecordRefusal(m.queues.recorder, model, version, err)
}

// Run submits one decoded request to the queue of the named version.
// The queue calls the session that is already open for that version.
func (m *modelRuntime) Run(ctx context.Context, name, version string, req *batch.Request) (*batch.Reply, error) {
	if m == nil || m.repo == nil {
		return nil, batch.NotFound(fmt.Sprintf("model %q was not found", name))
	}
	return m.queues.submit(ctx, name, version, req)
}

func (m *modelRuntime) admit(name, version string) error {
	if m == nil || m.repo == nil {
		return batch.NotFound(fmt.Sprintf("model %q was not found", name))
	}
	admission := m.repo.Admit(name, version)
	if admission.Proceed() {
		return nil
	}
	if err := batch.GateError(admission.Status, admission.Reason); err != nil {
		return err
	}
	return batch.NotFound(fmt.Sprintf("model %q was not found", name))
}

func (m *modelRuntime) batchSettings(name, version string) (int, time.Duration, bool) {
	if m == nil || m.repo == nil {
		return 0, 0, false
	}
	for _, model := range m.repo.Catalog() {
		if model.Name != name {
			continue
		}
		delay := m.queues.delayDefault
		if model.QueueDelaySet {
			delay = unsignedDelay(model.QueueDelayMicroseconds)
		}
		for _, item := range model.Versions {
			if item.Version == version {
				return item.MaxBatchSize, delay, true
			}
		}
		return 0, 0, false
	}
	return 0, 0, false
}

func (m *modelRuntime) slot(name, version string) batch.Slot {
	return func(ctx context.Context, inputs []batch.Tensor) ([]batch.Tensor, func(), error) {
		if m == nil || m.repo == nil {
			return nil, nil, batch.NotFound(fmt.Sprintf("model %q was not found", name))
		}
		converted := make([]session.Input, len(inputs))
		for i, in := range inputs {
			converted[i] = session.Input{Name: in.Name, Shape: in.Shape, Data: in.Data}
		}
		outputs, err := m.repo.Run(ctx, name, version, converted)
		if err != nil {
			return nil, nil, runError(err)
		}
		tensors := make([]batch.Tensor, len(outputs))
		for i, out := range outputs {
			tensors[i] = batch.Tensor{Name: out.Name, Shape: out.Shape, Data: out.Buffer.Data}
		}
		release := func() {
			for _, out := range outputs {
				m.repo.Release(out)
			}
		}
		return tensors, release, nil
	}
}

func runError(err error) error {
	var status *session.StatusError
	if errors.As(err, &status) {
		if mapped := batch.GateError(status.Status, status.Reason); mapped != nil {
			return mapped
		}
		return batch.NotFound("model was not found")
	}
	return err
}

// buffers is the session buffer pool, or nil when no models directory was opened.
func (m *modelRuntime) buffers() batch.Pool {
	if m == nil || m.queues.pool == nil {
		return nil
	}
	return m.queues.pool
}

// bufferPool adapts the session buffer pool to the batcher.
type bufferPool struct {
	pool *session.Pool
}

func (p bufferPool) Get(n int) ([]byte, error) {
	if p.pool == nil {
		return nil, errors.New("buffer pool is closed")
	}
	buf, err := p.pool.Get(n, nil)
	if err != nil {
		return nil, err
	}
	return buf.Data, nil
}

func (p bufferPool) Put(b []byte) {
	if p.pool == nil || cap(b) == 0 {
		return
	}
	p.pool.Put(session.Buffer{Data: b, Pooled: true})
}

// ready reports whether any loaded model can serve.
func (m *modelRuntime) ready() bool {
	if m == nil || m.repo == nil {
		return httpapi.AnyReady()
	}
	listed := m.repo.Models()
	states := make([]httpapi.State, len(listed))
	for i, model := range listed {
		states[i] = httpapi.State(model.State)
	}
	return httpapi.AnyReady(states...)
}

// shutdown stops directory watches, lets an in-flight run finish, closes sessions, and then releases the runtime.
func (m *modelRuntime) shutdown() error {
	if m == nil {
		return nil
	}
	var errs []error
	if m.watcher != nil {
		errs = append(errs, m.watcher.Close())
	}
	if m.repo != nil {
		errs = append(errs, m.repo.Close())
	}
	if m.started {
		errs = append(errs, ort.Shutdown())
	}
	return errors.Join(errs...)
}
