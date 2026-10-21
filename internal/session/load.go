//go:build cgo

package session

import (
	"errors"
	"fmt"
	"sync"

	"github.com/eclipse-iofog/ai-inference-onnx/internal/discover"
	"github.com/eclipse-iofog/ai-inference-onnx/internal/ort"
)

// beforeRuntime runs while a version holds its execution slot, before the runtime is called.
// It is nil outside tests.
var beforeRuntime func(backing []tensorMemory)

// whileLoading runs after a model is marked loading and before its sessions are opened.
// A reload calls it before the in-flight run's session is closed.
// It is nil outside tests.
var (
	loadingMu    sync.Mutex
	whileLoading func(*Repository)
)

func callWhileLoading(repo *Repository) {
	loadingMu.Lock()
	fn := whileLoading
	loadingMu.Unlock()
	if fn != nil {
		fn(repo)
	}
}

func setWhileLoading(fn func(*Repository)) {
	loadingMu.Lock()
	whileLoading = fn
	loadingMu.Unlock()
}

// tensorMemory is the bytes one input tensor uses for the run that holds the slot.
type tensorMemory struct {
	name string
	data []byte
}

// Repository is the set of models loaded from one models root.
type Repository struct {
	pool    *Pool
	mu      sync.Mutex
	models  map[string]*loadedModel
	order   []string
	onState StateFunc
}

type loadedModel struct {
	name          string
	state         State
	reason        string
	queueDelay    uint64
	queueDelaySet bool
	versions      []*versionSession
	// selected is the version names this directory resolved to.
	// It stays set while a session is opening and after a load fails.
	selected []string
	epoch    uint64
	opens    uint64
	id       modelID
	haveID   bool
	live     map[string]int
	high     map[string]int
}

type versionSession struct {
	version   string
	session   *ort.Session
	inputs    []ort.TensorInfo
	outputs   []ort.TensorInfo
	batchable bool
	maxBatch  int
	warmed    bool
	slot      sync.Mutex
}

// Load resolves every model directory and opens one session per selected version.
// A directory that cannot be served is recorded as unavailable and has no session.
// Thread counts and the provider chain come from cfg. The loader passes the chain through unchanged.
func Load(cfg Config, pool *Pool) (*Repository, error) {
	if pool == nil {
		return nil, errors.New("buffer pool is required")
	}
	discovered, err := discover.Resolve(cfg.ModelsRoot)
	if err != nil {
		return nil, err
	}
	repo := &Repository{pool: pool}
	repo.OnState(cfg.OnState)
	for _, model := range discovered {
		repo.loadOne(cfg, model)
	}
	return repo, nil
}

// OnState registers fn to run after a model state is stored.
// A nil function clears the registration. fn runs without the repository lock.
func (r *Repository) OnState(fn StateFunc) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.onState = fn
	r.mu.Unlock()
}

// note reports a state that has already been stored.
// The function receives a copy of the model list and runs without the repository lock.
func (r *Repository) note(name string, state State) {
	if r == nil {
		return
	}
	fn, models := r.storedState()
	if fn == nil {
		return
	}
	fn(name, state, models)
}

// storedState returns the state function and a copy of the model list.
func (r *Repository) storedState() (StateFunc, []CatalogModel) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.onState == nil {
		return nil, nil
	}
	return r.onState, r.catalogLocked()
}

func (r *Repository) loadOne(cfg Config, discovered discover.Model) {
	id := identify(cfg, discovered)
	if !id.loadable {
		item := unavailableModel(discovered.Name, id.reason)
		item.selected = selectedNames(discovered)
		item.id = id
		item.haveID = true
		r.add(item)
		r.note(discovered.Name, Unavailable)
		return
	}
	item := &loadedModel{
		name:          discovered.Name,
		state:         Loading,
		reason:        reasonLoading,
		queueDelay:    queueDelayOf(discovered.MaxQueueDelayMicroseconds),
		queueDelaySet: discovered.MaxQueueDelayMicroseconds != nil,
		selected:      selectedNames(discovered),
	}
	r.add(item)
	r.note(discovered.Name, Loading)
	callWhileLoading(r)
	opened, err := r.openVersions(cfg, item, discovered)
	if err != nil {
		r.fail(item, err.Error(), id)
		r.note(discovered.Name, Unavailable)
		return
	}
	r.mu.Lock()
	item.versions = opened
	item.state = Ready
	item.reason = ""
	item.epoch = 1
	item.opens = uint64(len(opened))
	item.id = id
	item.haveID = true
	r.mu.Unlock()
	r.note(discovered.Name, Ready)
}

func unavailableModel(name, reason string) *loadedModel {
	return &loadedModel{name: name, state: Unavailable, reason: reason}
}

func (r *Repository) add(item *loadedModel) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.models == nil {
		r.models = make(map[string]*loadedModel)
	}
	r.models[item.name] = item
	r.order = append(r.order, item.name)
}

func (r *Repository) fail(item *loadedModel, reason string, id modelID) {
	r.mu.Lock()
	item.versions = nil
	item.state = Unavailable
	item.reason = reason
	item.id = id
	item.haveID = true
	r.mu.Unlock()
}

func (r *Repository) openVersions(cfg Config, item *loadedModel, discovered discover.Model) ([]*versionSession, error) {
	opened := make([]*versionSession, 0, len(discovered.Versions))
	for _, version := range discovered.Versions {
		sess, err := ort.Open(version.Path, cfg.IntraOp, cfg.InterOp, cfg.Providers)
		if err != nil {
			r.retire(item, opened)
			return nil, err
		}
		running := &versionSession{version: version.Version, session: sess}
		running.prepare(discovered.MaxBatchSize)
		r.mu.Lock()
		item.track(version.Version, 1)
		r.mu.Unlock()
		if err = running.warm(); err != nil {
			opened = append(opened, running)
			r.retire(item, opened)
			return nil, fmt.Errorf("warm up model: %w", err)
		}
		opened = append(opened, running)
	}
	return opened, nil
}

func (r *Repository) retire(item *loadedModel, versions []*versionSession) {
	for _, version := range versions {
		if version == nil || version.session == nil {
			continue
		}
		_ = version.session.Close()
		version.session = nil
		r.mu.Lock()
		item.track(version.version, -1)
		r.mu.Unlock()
	}
}

// Admit is the call gate for one model version.
// An empty version selects the version the policy kept.
// Loading returns status 503. An unknown name, or a version that was not selected, returns status 404.
// An unavailable model keeps its reason and uses neither of those statuses.
func (r *Repository) Admit(name, version string) Admission {
	if r == nil {
		return Admission{Status: StatusMissing, Reason: "model was not found"}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	item := r.models[name]
	if item == nil {
		return Admission{Status: StatusMissing, Reason: fmt.Sprintf("model %q was not found", name)}
	}
	switch item.state {
	case Loading:
		return Admission{Status: StatusLoading, Reason: item.reason}
	case Ready:
		chosen, ok := item.lookup(version)
		if !ok {
			return Admission{Status: StatusMissing, Reason: fmt.Sprintf("version %q was not found", version)}
		}
		return Admission{Version: chosen.version}
	default:
		return Admission{Reason: item.reason}
	}
}

func (m *loadedModel) lookup(version string) (*versionSession, bool) {
	if len(m.versions) == 0 {
		return nil, false
	}
	if version == "" {
		return m.versions[0], true
	}
	for _, item := range m.versions {
		if item.version == version {
			return item, true
		}
	}
	return nil, false
}

// Models returns the load result for every child directory.
func (r *Repository) Models() []Model {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Model, 0, len(r.order))
	for _, name := range r.order {
		item := r.models[name]
		view := Model{
			Name:                   item.name,
			State:                  item.state,
			Reason:                 item.reason,
			QueueDelayMicroseconds: item.queueDelay,
			Versions:               make([]Version, 0, len(item.versions)),
		}
		for _, version := range item.versions {
			view.Versions = append(view.Versions, Version{
				Version:      version.version,
				MaxBatchSize: version.maxBatch,
				Batchable:    version.batchable,
				Warmed:       version.warmed,
			})
		}
		out = append(out, view)
	}
	return out
}

// Release returns an output buffer to the pool.
func (r *Repository) Release(out Output) {
	if r == nil || r.pool == nil {
		return
	}
	r.pool.Put(out.Buffer)
}

// Close releases every session this load opened.
func (r *Repository) Close() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var errs []error
	for _, name := range r.order {
		item := r.models[name]
		for _, version := range item.versions {
			version.slot.Lock()
			if version.session != nil {
				if err := version.session.Close(); err != nil {
					errs = append(errs, err)
				}
				version.session = nil
			}
			version.slot.Unlock()
		}
		item.versions = nil
	}
	return errors.Join(errs...)
}

func (r *Repository) sessionsOpen() int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, item := range r.models {
		for _, version := range item.versions {
			if version.session != nil {
				n++
			}
		}
	}
	return n
}

func queueDelayOf(value *uint64) uint64 {
	if value == nil {
		return 0
	}
	return *value
}

func (v *versionSession) prepare(override *int) {
	v.inputs = v.session.Inputs()
	v.outputs = v.session.Outputs()
	v.batchable = shapesBatchable(v.inputs, v.outputs)
	v.maxBatch = effectiveMaxBatch(v.batchable, override)
}

func shapesBatchable(inputs, outputs []ort.TensorInfo) bool {
	if len(inputs) == 0 || len(outputs) == 0 {
		return false
	}
	for _, info := range inputs {
		if !leadingDynamic(info.Shape) {
			return false
		}
	}
	for _, info := range outputs {
		if !leadingDynamic(info.Shape) {
			return false
		}
	}
	return true
}

func leadingDynamic(shape []int64) bool {
	return len(shape) > 0 && shape[0] < 0
}

func effectiveMaxBatch(batchable bool, override *int) int {
	if !batchable {
		return 0
	}
	if override != nil {
		return *override
	}
	return 1
}
