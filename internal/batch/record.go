package batch

import (
	"context"
	"errors"
	"time"

	"github.com/eclipse-iofog/ai-inference-onnx/internal/metrics"
)

// Recorder counts finished requests for one model version and how many are waiting to run.
// A nil Recorder counts nothing.
type Recorder interface {
	// Record adds one finished batch of requests.
	Record(ex metrics.Execution)
	// SetPending sets how many requests are waiting and not yet running.
	SetPending(model, version string, waiting uint64)
}

// FailureReporter counts one request that failed before the queue accepted it.
type FailureReporter interface {
	// ReportFailure records err against model and version.
	ReportFailure(model, version string, err error)
}

// NoteFailure counts a request that failed before it reached the queue.
// runner may implement FailureReporter. A missing model or version is not counted.
// A nil runner counts nothing.
func NoteFailure(runner any, model, version string, err error) {
	if runner == nil || err == nil || model == "" || version == "" {
		return
	}
	if _, ok := failureReason(err, false); !ok {
		return
	}
	reporter, ok := runner.(FailureReporter)
	if !ok {
		return
	}
	reporter.ReportFailure(model, version, err)
}

// RecordRefusal counts one request that failed before it ran.
// A missing model or version is not counted. A nil recorder counts nothing.
func RecordRefusal(rec Recorder, model, version string, err error) {
	if rec == nil || err == nil || model == "" || version == "" {
		return
	}
	reason, ok := failureReason(err, false)
	if !ok {
		return
	}
	rec.Record(metrics.Execution{
		Model:   model,
		Version: version,
		Requests: []metrics.Request{{
			Success: false,
			Reason:  reason,
		}},
	})
}

// publishPending reports how many requests are waiting. The caller holds mu.
func (b *Batcher) publishPending() {
	if b == nil || b.cfg.Recorder == nil {
		return
	}
	b.cfg.Recorder.SetPending(b.cfg.Key.Model, b.cfg.Key.Version, uint64(len(b.queue)))
}

func (b *Batcher) refuse(err error) {
	if b == nil || b.cfg.Recorder == nil {
		return
	}
	reason, ok := failureReason(err, false)
	if !ok {
		return
	}
	b.cfg.Recorder.Record(metrics.Execution{
		Model:   b.cfg.Key.Model,
		Version: b.cfg.Key.Version,
		Requests: []metrics.Request{{
			Success: false,
			Reason:  reason,
		}},
	})
}

func (b *Batcher) refuseWaiter(w *waiter, err error) {
	b.recordEach(false, 0, time.Time{}, b.cfg.Clock.Now(), []*waiter{w}, func(*waiter) error { return err }, false)
}

func (b *Batcher) recordEach(ran bool, compute time.Duration, runStart, end time.Time, waiters []*waiter, errFor func(*waiter) error, duringRun bool) {
	if b == nil || b.cfg.Recorder == nil || len(waiters) == 0 {
		return
	}
	if end.IsZero() {
		end = b.cfg.Clock.Now()
	}
	reqs := make([]metrics.Request, 0, len(waiters))
	for _, w := range waiters {
		callErr := errFor(w)
		if callErr == nil {
			reqs = append(reqs, b.successRequest(w, runStart, end))
			continue
		}
		req, ok := b.failureRequest(w, runStart, end, callErr, duringRun)
		if ok {
			reqs = append(reqs, req)
		}
	}
	if len(reqs) == 0 {
		return
	}
	b.cfg.Recorder.Record(metrics.Execution{
		Model:    b.cfg.Key.Model,
		Version:  b.cfg.Key.Version,
		Ran:      ran,
		Compute:  compute,
		Requests: reqs,
	})
}

func (b *Batcher) successRequest(w *waiter, runStart, end time.Time) metrics.Request {
	elapsed, queued := b.span(w, runStart, end, true)
	return metrics.Request{
		Success:   true,
		BatchSize: b.batchSize(w),
		Elapsed:   elapsed,
		Queued:    queued,
	}
}

func (b *Batcher) failureRequest(w *waiter, runStart, end time.Time, err error, duringRun bool) (metrics.Request, bool) {
	reason, ok := failureReason(err, duringRun)
	if !ok {
		return metrics.Request{}, false
	}
	elapsed, queued := b.span(w, runStart, end, duringRun)
	size := uint64(1)
	if w != nil {
		size = b.batchSize(w)
	}
	return metrics.Request{
		Success:   false,
		Reason:    reason,
		BatchSize: size,
		Elapsed:   elapsed,
		Queued:    queued,
	}, true
}

func (b *Batcher) batchSize(w *waiter) uint64 {
	if w == nil || b.cfg.MaxBatchSize == 0 || w.rows < 1 {
		return 1
	}
	return uint64(w.rows)
}

func (b *Batcher) span(w *waiter, runStart, end time.Time, duringRun bool) (time.Duration, time.Duration) {
	if w == nil || w.arrival.IsZero() || end.IsZero() {
		return 0, 0
	}
	elapsed := end.Sub(w.arrival)
	if elapsed < 0 {
		elapsed = 0
	}
	if !duringRun || runStart.IsZero() {
		return elapsed, elapsed
	}
	queued := runStart.Sub(w.arrival)
	if queued < 0 {
		queued = 0
	}
	return elapsed, queued
}

// failureRan reports whether a slot error still counts as an executed batch.
// A bad tensor and an unknown model do not. Cancel and a deadline during the slot do.
func failureRan(err error) bool {
	if err == nil {
		return true
	}
	var invalid *InvalidError
	if errors.As(err, &invalid) {
		return false
	}
	var classified *Error
	if errors.As(err, &classified) && classified.Kind == KindNotFound {
		return false
	}
	return true
}

// failureReason classifies err. The boolean is false when the failure must not create a series.
// duringRun is true once the request has entered the slot.
func failureReason(err error, duringRun bool) (metrics.Reason, bool) {
	if err == nil {
		return "", false
	}
	var invalid *InvalidError
	if errors.As(err, &invalid) {
		return metrics.ReasonOther, true
	}
	var classified *Error
	if errors.As(err, &classified) {
		switch classified.Kind {
		case KindNotFound:
			return "", false
		case KindQueueFull, KindQueueTimeout:
			return metrics.ReasonRejected, true
		case KindRuntime:
			return metrics.ReasonBackend, true
		default:
			return metrics.ReasonOther, true
		}
	}
	switch {
	case errors.Is(err, context.Canceled):
		return metrics.ReasonCanceled, true
	case errors.Is(err, context.DeadlineExceeded):
		if duringRun {
			return metrics.ReasonOther, true
		}
		return metrics.ReasonRejected, true
	default:
		if duringRun {
			return metrics.ReasonBackend, true
		}
		return metrics.ReasonOther, true
	}
}
