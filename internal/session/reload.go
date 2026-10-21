//go:build cgo

package session

import (
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"time"

	"github.com/eclipse-iofog/ai-inference-onnx/internal/discover"
)

// allModels schedules one pass over every model directory.
// A model name cannot be this value: names containing * are rejected.
const allModels = "*"

// fileID is the inode, size, and modification time of one regular file.
// An absent file is the zero value. The bytes are not hashed.
type fileID struct {
	present bool
	inode   uint64
	size    int64
	modTime time.Time
}

func (a fileID) same(b fileID) bool {
	if a.present != b.present {
		return false
	}
	if !a.present {
		return true
	}
	return a.inode == b.inode && a.size == b.size && a.modTime.Equal(b.modTime)
}

type versionID struct {
	version string
	onnx    fileID
	data    fileID
}

// modelID is the identity of one model directory.
// It records each selected ONNX file, its external-data sibling when that path is considered, and which named knowledge directories exist.
// Rewriting the name file with the same names, while those directories stay, is the same model.
type modelID struct {
	versions []versionID
	names    []string
	present  []bool
	loadable bool
	reason   string
}

func (a modelID) same(b modelID) bool {
	if a.loadable != b.loadable {
		return false
	}
	if !a.loadable && a.reason != b.reason {
		return false
	}
	if !slices.Equal(a.names, b.names) || !slices.Equal(a.present, b.present) {
		return false
	}
	if len(a.versions) != len(b.versions) {
		return false
	}
	for _, left := range a.versions {
		matched := false
		for _, right := range b.versions {
			if left.version == right.version && left.onnx.same(right.onnx) && left.data.same(right.data) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

func identify(cfg Config, model discover.Model) modelID {
	if !model.Available {
		return modelID{reason: model.Reason}
	}
	knowledge := discover.ReadKnowledge(filepath.Join(cfg.ModelsRoot, model.Name), cfg.KnowledgeRoot)
	id := modelID{
		names:   slices.Clone(knowledge.Names),
		present: slices.Clone(knowledge.Fingerprint.Exists),
	}
	for _, version := range model.Versions {
		id.versions = append(id.versions, versionID{
			version: version.Version,
			onnx:    statFile(version.Path),
			data:    statFile(version.DataPath),
		})
	}
	if !knowledge.Ready {
		id.reason = knowledge.Reason
		return id
	}
	id.loadable = true
	return id
}

func statFile(path string) fileID {
	if path == "" {
		return fileID{}
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return fileID{}
	}
	id := fileID{present: true, size: info.Size(), modTime: info.ModTime()}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if ok {
		id.inode = uint64(stat.Ino)
	}
	return id
}

// apply loads one directory when its identity changed, or removes nothing.
// The caller runs one apply at a time for a given model.
func (r *Repository) apply(cfg Config, discovered discover.Model) {
	id := identify(cfg, discovered)
	r.mu.Lock()
	item := r.models[discovered.Name]
	if item == nil {
		r.mu.Unlock()
		r.loadOne(cfg, discovered)
		return
	}
	if item.haveID && item.id.same(id) {
		r.mu.Unlock()
		return
	}
	r.mu.Unlock()
	r.reload(cfg, item, discovered, id)
}

// reload lets the in-flight run finish, closes that session, and only then loads the new file.
// Calls that arrive while the model is loading receive status 503 and are not run later.
func (r *Repository) reload(cfg Config, item *loadedModel, discovered discover.Model, id modelID) {
	r.mu.Lock()
	item.state = Loading
	item.reason = reasonLoading
	item.selected = selectedNames(discovered)
	old := slices.Clone(item.versions)
	r.mu.Unlock()
	r.note(item.name, Loading)

	callWhileLoading(r)

	for _, version := range old {
		if version == nil {
			continue
		}
		version.slot.Lock()
	}
	defer func() {
		for i := len(old) - 1; i >= 0; i-- {
			if old[i] != nil {
				old[i].slot.Unlock()
			}
		}
	}()

	r.mu.Lock()
	for _, version := range old {
		if version == nil || version.session == nil {
			continue
		}
		_ = version.session.Close()
		version.session = nil
		item.track(version.version, -1)
	}
	item.versions = nil
	r.mu.Unlock()

	if !id.loadable {
		r.mu.Lock()
		item.state = Unavailable
		item.reason = id.reason
		item.versions = nil
		item.id = id
		item.haveID = true
		r.mu.Unlock()
		r.note(item.name, Unavailable)
		return
	}

	opened, err := r.openVersions(cfg, item, discovered)
	r.mu.Lock()
	item.id = id
	item.haveID = true
	if err != nil {
		item.versions = nil
		item.state = Unavailable
		item.reason = err.Error()
		r.mu.Unlock()
		r.note(item.name, Unavailable)
		return
	}
	item.versions = opened
	item.state = Ready
	item.reason = ""
	item.queueDelay = queueDelayOf(discovered.MaxQueueDelayMicroseconds)
	item.queueDelaySet = discovered.MaxQueueDelayMicroseconds != nil
	item.epoch++
	item.opens += uint64(len(opened))
	r.mu.Unlock()
	r.note(item.name, Ready)
}

// remove drops a model that is no longer in the models root.
// The in-flight run finishes, the session is closed, and a later call is status 404.
func (r *Repository) remove(name string) {
	r.mu.Lock()
	item := r.models[name]
	if item == nil {
		r.mu.Unlock()
		return
	}
	old := slices.Clone(item.versions)
	delete(r.models, name)
	r.order = slices.DeleteFunc(r.order, func(existing string) bool { return existing == name })
	r.mu.Unlock()

	for _, version := range old {
		if version == nil {
			continue
		}
		version.slot.Lock()
	}
	for _, version := range old {
		if version == nil || version.session == nil {
			continue
		}
		_ = version.session.Close()
		version.session = nil
	}
	for i := len(old) - 1; i >= 0; i-- {
		if old[i] != nil {
			old[i].slot.Unlock()
		}
	}
}

// track records how many sessions of one version are open.
// The caller holds the repository lock.
func (m *loadedModel) track(version string, delta int) {
	if m.live == nil {
		m.live = make(map[string]int)
		m.high = make(map[string]int)
	}
	next := m.live[version] + delta
	if next < 0 {
		next = 0
	}
	m.live[version] = next
	if next > m.high[version] {
		m.high[version] = next
	}
}

func (r *Repository) modelNames() []string {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.order)
}

func (r *Repository) epoch(name string) uint64 {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	item := r.models[name]
	if item == nil {
		return 0
	}
	return item.epoch
}

func (r *Repository) opens(name string) uint64 {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	item := r.models[name]
	if item == nil {
		return 0
	}
	return item.opens
}

func (r *Repository) openHigh(name, version string) int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	item := r.models[name]
	if item == nil || item.high == nil {
		return 0
	}
	return item.high[version]
}

func (r *Repository) openNow(name string) int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	item := r.models[name]
	if item == nil {
		return 0
	}
	n := 0
	for _, version := range item.versions {
		if version != nil && version.session != nil {
			n++
		}
	}
	return n
}

func (r *Repository) fileIdentity(name, version string) (onnx, data fileID, ok bool) {
	if r == nil {
		return fileID{}, fileID{}, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	item := r.models[name]
	if item == nil || !item.haveID {
		return fileID{}, fileID{}, false
	}
	for _, itemVersion := range item.id.versions {
		if itemVersion.version == version {
			return itemVersion.onnx, itemVersion.data, true
		}
	}
	return fileID{}, fileID{}, false
}
