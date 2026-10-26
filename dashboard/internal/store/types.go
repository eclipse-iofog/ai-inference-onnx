package store

import "time"

const (
	// StateReady is a model version that can serve.
	StateReady = "READY"
	// StateLoading is a model version that is loading.
	StateLoading = "LOADING"
	// StateUnavailable is a model version that cannot serve.
	StateUnavailable = "UNAVAILABLE"
)

// Status reports whether an engine id is on the live view.
type Status int

const (
	// StatusUnknown means the store has never kept this engine id.
	StatusUnknown Status = iota
	// StatusLive means the newest snapshot is less than 30 seconds old.
	StatusLive
	// StatusStale means a snapshot was kept and the newest one is at least 30 seconds old.
	// Those snapshots have been discarded.
	StatusStale
)

// Ratio is a non-negative quotient of two counters.
// Known is false when the divisor is zero. The quotient is not computed in that case,
// and Numerator and Divisor are zero.
type Ratio struct {
	// Known is false when the divisor is zero.
	Known bool
	// Numerator is the dividend when Known is true.
	Numerator uint64
	// Divisor is the divisor when Known is true.
	Divisor uint64
}

// Whole returns the quotient truncated toward zero.
// It returns 0 when Known is false.
func (r Ratio) Whole() uint64 {
	if !r.Known || r.Divisor == 0 {
		return 0
	}
	return r.Numerator / r.Divisor
}

// Failure counts requests that did not return a result, by reason.
type Failure struct {
	// Rejected counts queue timeout and a full queue.
	Rejected uint64
	// Canceled counts a caller cancel.
	Canceled uint64
	// Backend counts a runtime failure.
	Backend uint64
	// Other counts any other failure.
	Other uint64
}

// Sum is the total of the four reasons.
func (f Failure) Sum() uint64 {
	return f.Rejected + f.Canceled + f.Backend + f.Other
}

// Model is one model version on one snapshot.
type Model struct {
	// Name is the model name.
	Name string
	// Version is the model version.
	Version string
	// State is StateReady, StateLoading, or StateUnavailable.
	State string
	// Reason is empty when State is StateReady.
	Reason string
	// Success is the number of requests that returned a result.
	Success uint64
	// Failure counts requests that did not return a result.
	Failure Failure
	// Count is the sum of batch sizes.
	Count uint64
	// ExecCount is the number of batches that were executed.
	ExecCount uint64
	// Pending is the number of requests waiting on the in-flight batch.
	Pending uint64
	// RequestDuration is the cumulative end-to-end time, in microseconds.
	RequestDuration uint64
	// QueueDuration is the cumulative time waiting to run, in microseconds.
	QueueDuration uint64
	// ComputeDuration is the cumulative run time, in microseconds.
	ComputeDuration uint64
	// ModelReady is 1 when the version is ready and 0 otherwise.
	ModelReady uint64
}

// BatchSize is Count divided by ExecCount.
// Known is false when ExecCount is zero.
func (m Model) BatchSize() Ratio {
	return newRatio(m.Count, m.ExecCount)
}

// Sample is one kept snapshot for an engine.
type Sample struct {
	// At is when the snapshot was accepted.
	At time.Time
	// Latency is this snapshot's request duration divided by its success count,
	// across every model on the engine. Known is false when that success count is zero.
	Latency Ratio
	// Models is the model list on this snapshot, ordered by name and then by version.
	Models []Model
}

// Engine is one live engine from its newest snapshot.
// Samples are the snapshots still kept for it, oldest first.
type Engine struct {
	// ServerID is the engine id.
	ServerID string
	// MicroserviceName is the platform microservice name. Empty means it was not set.
	MicroserviceName string
	// NodeUID is the platform node id. Empty means it was not set.
	NodeUID string
	// ApplicationName is the platform application name. Empty means it was not set.
	ApplicationName string
	// Live is the engine's live bit on the newest snapshot.
	Live bool
	// Ready is the engine's ready bit on the newest snapshot.
	Ready bool
	// NATSConnected is true when the newest snapshot says the leaf is connected.
	NATSConnected bool
	// ExecutionProviders is the provider chain on the newest snapshot, in the order the process appended it.
	// Empty means that snapshot did not name a provider.
	ExecutionProviders []string
	// ORTVersion is the ONNX Runtime version on the newest snapshot.
	// Empty means that snapshot did not name a version.
	ORTVersion string
	// Models is the newest snapshot's model list, ordered by name and then by version.
	Models []Model
	// Samples are kept snapshots, oldest first.
	Samples []Sample
}

// ModelRow is one model name across every live engine and version.
type ModelRow struct {
	// Name is the model name.
	Name string
	// ServerIDs are the live engines that serve Name, ordered by id.
	ServerIDs []string
	// Latency is the newest snapshots' request duration divided by their success count.
	// Known is false when that success count is zero.
	Latency Ratio
}

// Node is the live engines that share one node id.
type Node struct {
	// NodeUID is the node id. Empty means the platform did not set one.
	NodeUID string
	// Engines are ordered by server id.
	Engines []Engine
}

func newRatio(numerator, divisor uint64) Ratio {
	if divisor == 0 {
		return Ratio{}
	}
	return Ratio{Known: true, Numerator: numerator, Divisor: divisor}
}

func latencyOf(models []Model) Ratio {
	var numerator, divisor uint64
	for _, model := range models {
		numerator += model.RequestDuration
		divisor += model.Success
	}
	return newRatio(numerator, divisor)
}
