package metrics

import "time"

// Reason is why a request failed.
type Reason string

const (
	// ReasonRejected is a request refused before it ran because the queue was full or a wait timed out.
	ReasonRejected Reason = "REJECTED"
	// ReasonCanceled is a request the caller canceled.
	ReasonCanceled Reason = "CANCELED"
	// ReasonBackend is a request the runtime could not execute.
	ReasonBackend Reason = "BACKEND"
	// ReasonOther is any other failed request.
	ReasonOther Reason = "OTHER"
)

// State is the load state of one model version.
type State string

const (
	// StateReady means the version can run.
	StateReady State = "READY"
	// StateLoading means the version is being loaded.
	StateLoading State = "LOADING"
	// StateUnavailable means the version is known and cannot run.
	StateUnavailable State = "UNAVAILABLE"
)

// Execution is one batch of finished requests.
// Requests that share a run belong to the same Execution.
type Execution struct {
	// Model is the model name. A dotted name is one value.
	Model string
	// Version is the model version.
	Version string
	// Ran reports whether this batch was executed.
	Ran bool
	// Compute is the run duration. Each request in an executed batch adds it once.
	Compute time.Duration
	// Requests are the finished requests.
	Requests []Request
}

// Request is one finished request inside an execution.
type Request struct {
	// Success reports whether the request returned a result.
	Success bool
	// Reason classifies a failed request. It is ignored when Success is true.
	// A value other than the four known reasons is counted as other.
	Reason Reason
	// BatchSize is added to the inference count when the request succeeds.
	// The caller passes 1 when the model does not batch.
	BatchSize uint64
	// Elapsed is the request's end-to-end time.
	Elapsed time.Duration
	// Queued is the time the request waited before it ran.
	Queued time.Duration
}

// Record adds one execution.
// A successful request counts as one success and adds its batch size.
// Requests that share an executed batch add one execution together.
// A failed execution adds one execution and does not add batch sizes.
// A request that never ran adds one failure and does not add an execution or a batch size.
// Each finished request adds its own end-to-end time and queue time.
// Each request that ran adds the run duration once. A request that did not run adds no compute time.
func (r *Registry) Record(ex Execution) {
	if len(ex.Requests) == 0 {
		return
	}
	for _, req := range ex.Requests {
		if req.Success {
			r.success.WithLabelValues(ex.Model, ex.Version).Add(1)
			if req.BatchSize > 0 {
				r.count.WithLabelValues(ex.Model, ex.Version).Add(float64(req.BatchSize))
			}
		} else {
			r.failure.WithLabelValues(ex.Model, ex.Version, string(knownReason(req.Reason))).Add(1)
		}
		r.elapsed.WithLabelValues(ex.Model, ex.Version).Add(micros(req.Elapsed))
		r.waiting.WithLabelValues(ex.Model, ex.Version).Add(micros(req.Queued))
		var compute float64
		if ex.Ran {
			compute = micros(ex.Compute)
		}
		r.compute.WithLabelValues(ex.Model, ex.Version).Add(compute)
	}
	if ex.Ran {
		r.executed.WithLabelValues(ex.Model, ex.Version).Add(1)
	}
}

func knownReason(reason Reason) Reason {
	switch reason {
	case ReasonRejected, ReasonCanceled, ReasonBackend, ReasonOther:
		return reason
	default:
		return ReasonOther
	}
}

func micros(d time.Duration) float64 {
	if d <= 0 {
		return 0
	}
	us := d.Microseconds()
	if us < 0 {
		return 0
	}
	return float64(us)
}
