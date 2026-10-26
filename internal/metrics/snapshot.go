package metrics

import (
	"encoding/json"
	"math"
	"slices"
	"strconv"
	"strings"

	dto "github.com/prometheus/client_model/go"
)

const schemaVersion = 1

// subjectPrefix is the core subject that carries one engine snapshot.
const subjectPrefix = "inference.metrics.engines."

// ModelState is one model version after its state has been stored.
type ModelState struct {
	// Model is the model name. A dotted name is one value.
	Model string
	// Version is the model version.
	Version string
	// State is ready, loading, or unavailable.
	State State
	// Reason is empty when State is ready.
	Reason string
}

// SnapshotMeta is the process identity and the answers copied into one snapshot.
type SnapshotMeta struct {
	// ServerID is the resolved id of this process.
	ServerID string
	// MicroserviceName is set only when the platform provided it.
	MicroserviceName string
	// NodeUID is set only when the platform provided it.
	NodeUID string
	// ApplicationName is set only when the platform provided it.
	ApplicationName string
	// Live is the same answer as server live.
	Live bool
	// Ready is the same answer as server ready.
	Ready bool
	// NATSConnected is true only while the leaf is connected.
	NATSConnected bool
	// ExecutionProviders is the provider chain appended for every session, in that order.
	// An empty list means the runtime library did not load.
	ExecutionProviders []string
	// ORTVersion is the loaded ONNX Runtime version.
	// Empty means the runtime library did not load.
	ORTVersion string
}

// Subject is the core subject for one engine snapshot.
func Subject(serverID string) string {
	return subjectPrefix + serverID
}

// Observe records the model list.
// A version that remains keeps its counters. A version that is gone is removed.
// Ready is 1. Loading and unavailable are 0.
func (r *Registry) Observe(models []ModelState) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	next := make(map[versionKey]ModelState, len(models))
	for _, item := range models {
		key := versionKey{model: item.Model, version: item.Version}
		next[key] = item
		r.SetState(item.Model, item.Version, item.State)
	}
	for key := range r.known {
		if _, ok := next[key]; ok {
			continue
		}
		r.Remove(key.model, key.version)
	}
	r.known = next
}

// Snapshot writes one JSON document from the model list Observe stored and the counters recorded since.
// The document is not retained.
func (r *Registry) Snapshot(meta SnapshotMeta) ([]byte, error) {
	if r == nil {
		return nil, errNoRegistry
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	values, err := r.gatherValues()
	if err != nil {
		return nil, err
	}
	models := make([]snapshotModel, 0, len(r.known))
	for _, item := range r.known {
		models = append(models, modelDocument(item, values))
	}
	sortModels(models)
	providers := meta.ExecutionProviders
	if providers == nil {
		providers = []string{}
	}
	doc := snapshotDoc{
		Schema:             schemaVersion,
		ServerID:           meta.ServerID,
		Live:               meta.Live,
		Ready:              meta.Ready,
		NATSConnected:      meta.NATSConnected,
		ExecutionProviders: providers,
		ORTVersion:         meta.ORTVersion,
		Models:             models,
		MicroserviceName:   meta.MicroserviceName,
		NodeUID:            meta.NodeUID,
		ApplicationName:    meta.ApplicationName,
	}
	body, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	return body, nil
}

type snapshotDoc struct {
	Schema             int             `json:"schema"`
	ServerID           string          `json:"server_id"`
	Live               bool            `json:"live"`
	Ready              bool            `json:"ready"`
	NATSConnected      bool            `json:"nats_connected"`
	ExecutionProviders []string        `json:"execution_providers"`
	ORTVersion         string          `json:"ort_version"`
	Models             []snapshotModel `json:"models"`
	MicroserviceName   string          `json:"microservice_name,omitempty"`
	NodeUID            string          `json:"node_uid,omitempty"`
	ApplicationName    string          `json:"application_name,omitempty"`
}

type snapshotModel struct {
	Model   string `json:"model"`
	Version string `json:"version"`
	State   string `json:"state"`
	Reason  string `json:"reason"`
	Success uint64 `json:"inference_request_success"`
	Failure counts `json:"inference_request_failure"`
	Count   uint64 `json:"inference_count"`
	Exec    uint64 `json:"inference_exec_count"`
	Pending uint64 `json:"inference_pending_request_count"`
	Elapsed uint64 `json:"inference_request_duration_us"`
	Waiting uint64 `json:"inference_queue_duration_us"`
	Compute uint64 `json:"inference_compute_infer_duration_us"`
	Ready   uint64 `json:"inference_model_ready"`
}

type counts struct {
	Rejected uint64 `json:"REJECTED"`
	Canceled uint64 `json:"CANCELED"`
	Backend  uint64 `json:"BACKEND"`
	Other    uint64 `json:"OTHER"`
}

type seriesKey struct {
	name    string
	model   string
	version string
	reason  string
}

func modelDocument(item ModelState, values map[seriesKey]float64) snapshotModel {
	labels := seriesKey{model: item.Model, version: item.Version}
	return snapshotModel{
		Model:   item.Model,
		Version: item.Version,
		State:   string(item.State),
		Reason:  item.Reason,
		Success: whole(values[labels.named(nameSuccess)]),
		Failure: counts{
			Rejected: whole(values[labels.named(nameFailure).withReason(string(ReasonRejected))]),
			Canceled: whole(values[labels.named(nameFailure).withReason(string(ReasonCanceled))]),
			Backend:  whole(values[labels.named(nameFailure).withReason(string(ReasonBackend))]),
			Other:    whole(values[labels.named(nameFailure).withReason(string(ReasonOther))]),
		},
		Count:   whole(values[labels.named(nameCount)]),
		Exec:    whole(values[labels.named(nameExec)]),
		Pending: whole(values[labels.named(namePending)]),
		Elapsed: whole(values[labels.named(nameElapsed)]),
		Waiting: whole(values[labels.named(nameWaiting)]),
		Compute: whole(values[labels.named(nameCompute)]),
		Ready:   whole(values[labels.named(nameReady)]),
	}
}

func (k seriesKey) named(name string) seriesKey {
	k.name = name
	return k
}

func (k seriesKey) withReason(reason string) seriesKey {
	k.reason = reason
	return k
}

func sortModels(models []snapshotModel) {
	slices.SortFunc(models, func(a, b snapshotModel) int {
		if c := strings.Compare(a.Model, b.Model); c != 0 {
			return c
		}
		return compareVersion(a.Version, b.Version)
	})
}

func compareVersion(a, b string) int {
	ai, aerr := strconv.ParseUint(a, 10, 64)
	bi, berr := strconv.ParseUint(b, 10, 64)
	if aerr == nil && berr == nil && ai != bi {
		if ai < bi {
			return -1
		}
		return 1
	}
	return strings.Compare(a, b)
}

func (r *Registry) gatherValues() (map[seriesKey]float64, error) {
	families, err := r.prom.Gather()
	if err != nil && len(families) == 0 {
		return nil, err
	}
	values := make(map[seriesKey]float64)
	for _, family := range families {
		name := family.GetName()
		for _, metric := range family.GetMetric() {
			value, ok := metricValue(metric)
			if !ok {
				continue
			}
			key := seriesKey{name: name}
			for _, label := range metric.GetLabel() {
				switch label.GetName() {
				case "model":
					key.model = label.GetValue()
				case "version":
					key.version = label.GetValue()
				case "reason":
					key.reason = label.GetValue()
				}
			}
			values[key] = value
		}
	}
	return values, nil
}

func metricValue(metric *dto.Metric) (float64, bool) {
	if metric.GetCounter() != nil {
		return metric.GetCounter().GetValue(), true
	}
	if metric.GetGauge() != nil {
		return metric.GetGauge().GetValue(), true
	}
	return 0, false
}

func whole(v float64) uint64 {
	if v <= 0 || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return uint64(math.Round(v))
}
