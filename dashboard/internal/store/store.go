// Package store keeps about an hour of engine snapshots in memory.
// Engines that serve one model name share one latency figure.
// An engine drops off the live view 30 seconds after its newest snapshot.
package store

import (
	"slices"
	"strings"
	"sync"
	"time"
)

// sampleLimit is one hour of snapshots at a 10-second interval.
const sampleLimit = 360

// staleDuration is how long the newest snapshot keeps an engine on the live view.
const staleDuration = 30 * time.Second

// Clock reports the current time.
// It must not call the store. Tests move the clock instead of sleeping.
type Clock func() time.Time

// Store keeps schema 1 snapshots in memory.
// A restart is a new empty store. A document this store cannot read is dropped
// and does not remove an engine that was already kept.
type Store struct {
	mu      sync.Mutex
	now     Clock
	engines map[string]*engineRec
}

type engineRec struct {
	samples []sample
}

type sample struct {
	at   time.Time
	snap snapshot
}

type snapshot struct {
	serverID         string
	microserviceName string
	nodeUID          string
	applicationName  string
	live             bool
	ready            bool
	natsConnected    bool
	models           []Model
}

// New returns an empty store.
// now is read when a snapshot is kept and when the live view is read.
// A nil clock reads the wall clock.
func New(now Clock) *Store {
	return &Store{now: now, engines: map[string]*engineRec{}}
}

// Accept keeps body when it is one schema 1 snapshot.
// It returns false when the body is dropped. A drop does not remove an engine already kept.
// The receipt time is the store clock.
func (s *Store) Accept(body []byte) bool {
	if s == nil {
		return false
	}
	doc, ok := decode(body)
	if !ok {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	at := s.clock()
	s.sweep(at)
	s.keep(doc, at)
	return true
}

// Engines returns the live engines, ordered by server id.
func (s *Store) Engines() []Engine {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweep(s.clock())
	return s.liveEngines()
}

// Models returns one row per model name on the live view, ordered by name.
// Latency uses the newest snapshot of each live engine.
func (s *Store) Models() []ModelRow {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweep(s.clock())
	return s.modelRows()
}

// Nodes groups live engines by node id.
// A group whose node id was not set is last. Engines in a group are ordered by server id.
func (s *Store) Nodes() []Node {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweep(s.clock())
	return s.nodeGroups()
}

// Lookup returns the live engine for serverID.
// A stale id returns StatusStale and a zero Engine. An id that was never kept returns StatusUnknown.
func (s *Store) Lookup(serverID string) (Engine, Status) {
	if s == nil {
		return Engine{}, StatusUnknown
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweep(s.clock())
	rec := s.engines[serverID]
	if rec == nil {
		return Engine{}, StatusUnknown
	}
	if len(rec.samples) == 0 {
		return Engine{}, StatusStale
	}
	return engineView(serverID, rec), StatusLive
}

func (s *Store) clock() time.Time {
	if s.now == nil {
		return time.Now()
	}
	return s.now()
}

func (s *Store) sweep(now time.Time) {
	for _, rec := range s.engines {
		rec.discardExpired(now)
	}
}

func (e *engineRec) discardExpired(now time.Time) {
	if len(e.samples) == 0 {
		return
	}
	newest := e.samples[len(e.samples)-1].at
	if !now.Before(newest.Add(staleDuration)) {
		e.samples = nil
	}
}

func (s *Store) keep(doc snapshot, at time.Time) {
	if s.engines == nil {
		s.engines = map[string]*engineRec{}
	}
	rec := s.engines[doc.serverID]
	if rec == nil {
		rec = &engineRec{}
		s.engines[doc.serverID] = rec
	}
	if len(rec.samples) >= sampleLimit {
		rec.samples[0] = sample{}
		rec.samples = rec.samples[1:]
	}
	rec.samples = append(rec.samples, sample{at: at, snap: doc})
}

func (s *Store) liveIDs() []string {
	ids := make([]string, 0, len(s.engines))
	for id, rec := range s.engines {
		if len(rec.samples) == 0 {
			continue
		}
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

func (s *Store) liveEngines() []Engine {
	ids := s.liveIDs()
	out := make([]Engine, 0, len(ids))
	for _, id := range ids {
		out = append(out, engineView(id, s.engines[id]))
	}
	return out
}

func (s *Store) modelRows() []ModelRow {
	type acc struct {
		numerator uint64
		divisor   uint64
		ids       []string
	}
	grouped := make(map[string]*acc)
	names := make([]string, 0)
	for _, id := range s.liveIDs() {
		last := s.engines[id].samples[len(s.engines[id].samples)-1]
		seen := make(map[string]bool)
		for _, model := range last.snap.models {
			row := grouped[model.Name]
			if row == nil {
				row = &acc{}
				grouped[model.Name] = row
				names = append(names, model.Name)
			}
			row.numerator += model.RequestDuration
			row.divisor += model.Success
			if seen[model.Name] {
				continue
			}
			seen[model.Name] = true
			row.ids = append(row.ids, id)
		}
	}
	slices.Sort(names)
	rows := make([]ModelRow, 0, len(names))
	for _, name := range names {
		row := grouped[name]
		slices.Sort(row.ids)
		rows = append(rows, ModelRow{
			Name:      name,
			ServerIDs: row.ids,
			Latency:   newRatio(row.numerator, row.divisor),
		})
	}
	return rows
}

func (s *Store) nodeGroups() []Node {
	grouped := make(map[string][]Engine)
	ids := make([]string, 0)
	for _, id := range s.liveIDs() {
		eng := engineView(id, s.engines[id])
		if _, ok := grouped[eng.NodeUID]; !ok {
			ids = append(ids, eng.NodeUID)
		}
		grouped[eng.NodeUID] = append(grouped[eng.NodeUID], eng)
	}
	slices.SortFunc(ids, func(a, b string) int {
		if a == "" {
			if b == "" {
				return 0
			}
			return 1
		}
		if b == "" {
			return -1
		}
		return strings.Compare(a, b)
	})
	nodes := make([]Node, 0, len(ids))
	for _, id := range ids {
		nodes = append(nodes, Node{NodeUID: id, Engines: grouped[id]})
	}
	return nodes
}

func engineView(id string, rec *engineRec) Engine {
	last := rec.samples[len(rec.samples)-1]
	samples := make([]Sample, len(rec.samples))
	for i, item := range rec.samples {
		samples[i] = Sample{
			At:      item.at,
			Latency: latencyOf(item.snap.models),
			Models:  cloneModels(item.snap.models),
		}
	}
	snap := last.snap
	return Engine{
		ServerID:         id,
		MicroserviceName: snap.microserviceName,
		NodeUID:          snap.nodeUID,
		ApplicationName:  snap.applicationName,
		Live:             snap.live,
		Ready:            snap.ready,
		NATSConnected:    snap.natsConnected,
		Models:           cloneModels(snap.models),
		Samples:          samples,
	}
}

func cloneModels(in []Model) []Model {
	if len(in) == 0 {
		return nil
	}
	out := make([]Model, len(in))
	copy(out, in)
	return out
}
