package batch

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	"github.com/eclipse-iofog/ai-inference-onnx/internal/metrics"
)

func TestCountRows(t *testing.T) {
	ctx := context.Background()

	t.Run("one request of batch 1", func(t *testing.T) {
		r, reg := countedRig(t, rigOptions{})
		if _, err := r.b.Infer(ctx, rowsRequest(1, 1, 2)); err != nil {
			t.Fatal(err)
		}
		assertCounts(t, reg, 1, 1, 1)
		assertCounts(t, reg, 1, 1, 1)
	})

	t.Run("one request of batch 8", func(t *testing.T) {
		r, reg := countedRig(t, rigOptions{maxBatch: 8})
		if _, err := r.b.Infer(ctx, rowsRequest(8, ones(8)...)); err != nil {
			t.Fatal(err)
		}
		assertCounts(t, reg, 1, 8, 1)
	})

	t.Run("batch 1 and batch 8 run separately", func(t *testing.T) {
		r, reg := countedRig(t, rigOptions{maxBatch: 8, held: true})
		first := r.submit(ctx, rowsRequest(8, ones(8)...))
		r.slot.waitEntered(t)
		second := r.submitQueued(ctx, t, rowsRequest(1, 1), 1)
		assertPending(t, reg, 1)
		r.slot.releaseAll()
		for _, p := range []*pending{first, second} {
			if _, err := p.result(t); err != nil {
				t.Fatal(err)
			}
		}
		if n := r.slot.callCount(); n != 2 {
			t.Fatalf("slot called %d times", n)
		}
		assertCounts(t, reg, 2, 9, 2)
	})

	t.Run("two batch-1 requests share a run", func(t *testing.T) {
		r, reg := countedRig(t, rigOptions{maxBatch: 4, delay: 100 * time.Microsecond})
		shareTwo(t, r, rowsRequest(1, 1, 2), rowsRequest(1, 3, 4))
		assertCounts(t, reg, 2, 2, 1)
	})

	t.Run("batch 1 and batch 8 share a run", func(t *testing.T) {
		r, reg := countedRig(t, rigOptions{maxBatch: 16, delay: 100 * time.Microsecond})
		shareTwo(t, r, rowsRequest(1, 1, 2), rowsRequest(8, ones(16)...))
		assertCounts(t, reg, 2, 9, 1)
	})

	t.Run("unbatched requests count as 1", func(t *testing.T) {
		r, reg := countedRig(t, rigOptions{maxBatch: 0, held: true})
		first := r.submit(ctx, rowsRequest(8, ones(8)...))
		r.slot.waitEntered(t)
		second := r.submitQueued(ctx, t, rowsRequest(1, 1, 2), 1)
		assertPending(t, reg, 1)
		r.slot.releaseAll()
		for _, p := range []*pending{first, second} {
			if _, err := p.result(t); err != nil {
				t.Fatal(err)
			}
		}
		assertCounts(t, reg, 2, 2, 2)
	})
}

func TestFailureReasons(t *testing.T) {
	ctx := context.Background()

	t.Run("queue full", func(t *testing.T) {
		for _, maxBatch := range []int{4, 0} {
			r, reg := countedRig(t, rigOptions{maxBatch: maxBatch, held: true})
			first := r.submit(ctx, rowsRequest(1, 0, 0))
			r.slot.waitEntered(t)
			waiters := make([]*pending, 0, 8)
			for i := range 8 {
				waiters = append(waiters, r.submitQueued(ctx, t, rowsRequest(1, float32(i), 0), i+1))
			}
			_, err := r.b.Infer(ctx, rowsRequest(1, 9, 9))
			if !errors.Is(err, ErrQueueFull) {
				t.Fatalf("max batch %d: %v", maxBatch, err)
			}
			full, loading := Status(err), Status(Loading(loadingReason))
			if full.HTTP != http.StatusServiceUnavailable || full.GRPC != codes.Unavailable {
				t.Fatalf("status %+v", full)
			}
			if full.Message == loading.Message {
				t.Fatal("queue-full text equals the loading text")
			}
			assertPending(t, reg, 8)
			assertFailure(t, reg, metrics.ReasonRejected, 1)
			assertNoSample(t, reg, "inference_exec_count")
			r.slot.releaseAll()
			for _, p := range append(waiters, first) {
				if _, err := p.result(t); err != nil {
					t.Fatalf("max batch %d: %v", maxBatch, err)
				}
			}
		}
	})

	t.Run("queue timeout", func(t *testing.T) {
		r, reg := countedRig(t, rigOptions{maxBatch: 4, held: true})
		first := r.submit(ctx, rowsRequest(1, 0, 0))
		r.slot.waitEntered(t)
		req := rowsRequest(1, 1, 2)
		req.Parameters.Timeout = 100
		p := r.submitQueued(ctx, t, req, 1)
		waitFor(t, "timeout timer", func() bool { return r.clock.pending() == 1 })
		r.clock.Advance(100 * time.Microsecond)
		_, err := p.result(t)
		if !errors.Is(err, ErrQueueTimeout) {
			t.Fatal(err)
		}
		if res := Status(err); res.HTTP != http.StatusGatewayTimeout || res.GRPC != codes.DeadlineExceeded {
			t.Fatalf("status %+v", res)
		}
		assertFailure(t, reg, metrics.ReasonRejected, 1)
		assertNoSample(t, reg, "inference_exec_count")
		assertNoSample(t, reg, "inference_count")
		assertValue(t, reg, "inference_compute_infer_duration_us", nil, 0)
		assertValue(t, reg, "inference_queue_duration_us", nil, 100)
		r.slot.releaseAll()
		if _, err := first.result(t); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("deadline while waiting", func(t *testing.T) {
		r, reg := countedRig(t, rigOptions{maxBatch: 4, held: true})
		first := r.submit(ctx, rowsRequest(1, 0, 0))
		r.slot.waitEntered(t)
		deadline, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
		defer cancel()
		p := r.submit(deadline, rowsRequest(1, 1, 2))
		_, err := p.result(t)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
		res := Status(err)
		if res.HTTP != http.StatusGatewayTimeout || res.GRPC != codes.DeadlineExceeded {
			t.Fatalf("status %+v", res)
		}
		if res.Message == Status(ErrQueueTimeout).Message {
			t.Fatal("context deadline text equals the queue timeout text")
		}
		assertFailure(t, reg, metrics.ReasonRejected, 1)
		assertNoSample(t, reg, "inference_exec_count")
		r.slot.releaseAll()
		if _, err := first.result(t); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("cancel while waiting", func(t *testing.T) {
		r, reg := countedRig(t, rigOptions{maxBatch: 4, held: true})
		first := r.submit(ctx, rowsRequest(1, 0, 0))
		r.slot.waitEntered(t)
		cancelCtx, cancel := context.WithCancel(ctx)
		p := r.submitQueued(cancelCtx, t, rowsRequest(1, 1, 2), 1)
		cancel()
		_, err := p.result(t)
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		if res := Status(err); res.HTTP != http.StatusBadRequest || res.GRPC != codes.Canceled {
			t.Fatalf("status %+v", res)
		}
		assertFailure(t, reg, metrics.ReasonCanceled, 1)
		assertNoSample(t, reg, "inference_exec_count")
		assertNoSample(t, reg, "inference_count")
		r.slot.releaseAll()
		if _, err := first.result(t); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("cancel during run", func(t *testing.T) {
		r, reg := countedRig(t, rigOptions{maxBatch: 4, held: true})
		cancelCtx, cancel := context.WithCancel(ctx)
		p := r.submit(cancelCtx, rowsRequest(1, 1, 2))
		r.slot.waitEntered(t)
		cancel()
		_, err := p.result(t)
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		assertFailure(t, reg, metrics.ReasonCanceled, 1)
		assertCounts(t, reg, 0, 0, 1)
	})

	t.Run("deadline during run", func(t *testing.T) {
		r, reg := countedRig(t, rigOptions{maxBatch: 4, held: true})
		deadline, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
		defer cancel()
		p := r.submit(deadline, rowsRequest(1, 1, 2))
		r.slot.waitEntered(t)
		_, err := p.result(t)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
		res := Status(err)
		if res.HTTP != http.StatusGatewayTimeout || res.Message == Status(ErrQueueTimeout).Message {
			t.Fatalf("status %+v", res)
		}
		assertFailure(t, reg, metrics.ReasonOther, 1)
		assertCounts(t, reg, 0, 0, 1)
	})

	t.Run("runtime error", func(t *testing.T) {
		r, reg := countedRig(t, rigOptions{maxBatch: 4})
		r.slot.err = errors.New("kernel failed")
		_, err := r.b.Infer(ctx, rowsRequest(1, 1, 2))
		if res := Status(err); res.HTTP != http.StatusInternalServerError || res.GRPC != codes.Internal {
			t.Fatalf("status %+v", res)
		}
		assertFailure(t, reg, metrics.ReasonBackend, 1)
		assertCounts(t, reg, 0, 0, 1)
	})

	t.Run("stacked runtime error", func(t *testing.T) {
		r, reg := countedRig(t, rigOptions{maxBatch: 4, delay: 100 * time.Microsecond})
		r.slot.err = errors.New("kernel failed")
		a := r.submit(ctx, rowsRequest(1, 1, 2))
		waitFor(t, "delay timer", func() bool { return r.clock.pending() == 1 })
		b := r.submit(ctx, rowsRequest(1, 3, 4))
		waitFor(t, "both queued", func() bool { return r.waiting() == 2 })
		r.clock.Advance(100 * time.Microsecond)
		for _, p := range []*pending{a, b} {
			if _, err := p.result(t); Status(err).HTTP != http.StatusInternalServerError {
				t.Fatal(err)
			}
		}
		if n := r.slot.callCount(); n != 1 {
			t.Fatalf("slot called %d times", n)
		}
		assertFailure(t, reg, metrics.ReasonBackend, 2)
		assertCounts(t, reg, 0, 0, 1)
	})

	t.Run("bad tensor", func(t *testing.T) {
		r, reg := countedRig(t, rigOptions{maxBatch: 2})
		_, err := r.b.Infer(ctx, rowsRequest(3, 1, 2, 3))
		if res := Status(err); res.HTTP != http.StatusBadRequest || res.GRPC != codes.InvalidArgument {
			t.Fatalf("status %+v", res)
		}
		assertFailure(t, reg, metrics.ReasonOther, 1)
		assertNoSample(t, reg, "inference_exec_count")
		assertNoSample(t, reg, "inference_count")
		assertNoSample(t, reg, "inference_request_success")
		if r.slot.callCount() != 0 {
			t.Fatal("a bad tensor ran")
		}
	})

	t.Run("loading", func(t *testing.T) {
		r, reg := countedRig(t, rigOptions{maxBatch: 4})
		r.gate.set(Loading(loadingReason))
		_, err := r.b.Infer(ctx, rowsRequest(1, 1, 2))
		res := Status(err)
		if res.HTTP != http.StatusServiceUnavailable || res.GRPC != codes.Unavailable || res.Message != loadingReason {
			t.Fatalf("status %+v", res)
		}
		if res.Message == Status(ErrQueueFull).Message {
			t.Fatal("loading text equals the queue-full text")
		}
		assertFailure(t, reg, metrics.ReasonOther, 1)
		assertNoSample(t, reg, "inference_exec_count")
		if r.slot.callCount() != 0 {
			t.Fatal("a loading model ran")
		}
	})

	t.Run("unavailable", func(t *testing.T) {
		r, reg := countedRig(t, rigOptions{maxBatch: 4})
		r.gate.set(Unavailable("model file could not be parsed"))
		_, err := r.b.Infer(ctx, rowsRequest(1, 1, 2))
		if res := Status(err); res.HTTP != http.StatusBadRequest || res.GRPC != codes.FailedPrecondition {
			t.Fatalf("status %+v", res)
		}
		assertFailure(t, reg, metrics.ReasonOther, 1)
		assertNoSample(t, reg, "inference_exec_count")
	})
}

func TestRecordedDurations(t *testing.T) {
	r, reg := countedRig(t, rigOptions{maxBatch: 4, delay: time.Millisecond, held: true})
	ctx := context.Background()
	a := r.submit(ctx, rowsRequest(1, 1, 2))
	waitFor(t, "delay timer", func() bool { return r.clock.pending() == 1 })
	b := r.submit(ctx, rowsRequest(1, 3, 4))
	waitFor(t, "both queued", func() bool { return r.waiting() == 2 })
	r.clock.Advance(time.Millisecond)
	r.slot.waitEntered(t)
	r.clock.Advance(4 * time.Millisecond)
	r.slot.release()
	for _, p := range []*pending{a, b} {
		if _, err := p.result(t); err != nil {
			t.Fatal(err)
		}
	}
	if n := r.slot.callCount(); n != 1 {
		t.Fatalf("slot called %d times", n)
	}
	assertCounts(t, reg, 2, 2, 1)
	assertValue(t, reg, "inference_queue_duration_us", nil, 2000)
	assertValue(t, reg, "inference_compute_infer_duration_us", nil, 8000)
	assertValue(t, reg, "inference_request_duration_us", nil, 10000)
}

func TestNotFoundDoesNotCount(t *testing.T) {
	r, reg := countedRig(t, rigOptions{})
	if _, err := r.b.Infer(context.Background(), rowsRequest(1, 1, 2)); err != nil {
		t.Fatal(err)
	}
	r.gate.set(NotFound(`model "missing" was not found`))
	_, err := r.b.Infer(context.Background(), rowsRequest(1, 3, 4))
	if Status(err).HTTP != http.StatusNotFound {
		t.Fatal(err)
	}
	assertCounts(t, reg, 1, 1, 1)
	assertNoSample(t, reg, "inference_request_failure")
	RecordRefusal(reg, "missing", "1", NotFound(`model "missing" was not found`))
	assertNoSample(t, reg, "inference_request_failure")
	if got := sampleValue(t, reg, "inference_request_success", map[string]string{"model": "missing", "version": "1"}); got != 0 {
		t.Fatalf("missing model success %v", got)
	}
}

func TestPendingDropsWhenTheRequestRuns(t *testing.T) {
	r, reg := countedRig(t, rigOptions{maxBatch: 4, held: true})
	ctx := context.Background()
	first := r.submit(ctx, rowsRequest(1, 0, 0))
	r.slot.waitEntered(t)
	second := r.submitQueued(ctx, t, rowsRequest(1, 1, 2), 1)
	assertPending(t, reg, 1)
	r.slot.release()
	r.slot.waitEntered(t)
	assertPending(t, reg, 0)
	r.slot.releaseAll()
	for _, p := range []*pending{first, second} {
		if _, err := p.result(t); err != nil {
			t.Fatal(err)
		}
	}
}

func countedRig(t *testing.T, o rigOptions) (*rig, *metrics.Registry) {
	t.Helper()
	reg := metrics.New()
	o.recorder = reg
	if o.model == "" {
		o.model = "add"
		o.version = "1"
	}
	return newRig(t, o), reg
}

func shareTwo(t *testing.T, r *rig, aReq, bReq *Request) {
	t.Helper()
	ctx := context.Background()
	a := r.submit(ctx, aReq)
	waitFor(t, "delay timer", func() bool { return r.clock.pending() == 1 })
	b := r.submit(ctx, bReq)
	waitFor(t, "both queued", func() bool { return r.waiting() == 2 })
	r.clock.Advance(100 * time.Microsecond)
	for _, p := range []*pending{a, b} {
		if _, err := p.result(t); err != nil {
			t.Fatal(err)
		}
	}
	if n := r.slot.callCount(); n != 1 {
		t.Fatalf("slot called %d times", n)
	}
}

func ones(n int) []float32 {
	out := make([]float32, n)
	for i := range out {
		out[i] = 1
	}
	return out
}

type metricSample struct {
	name   string
	labels map[string]string
	value  float64
}

func scrapeRegistry(t *testing.T, reg *metrics.Registry) []metricSample {
	t.Helper()
	rec := httptest.NewRecorder()
	reg.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("scrape status %d", rec.Code)
	}
	var samples []metricSample
	for _, line := range strings.Split(rec.Body.String(), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		samples = append(samples, parseMetric(t, line))
	}
	return samples
}

func parseMetric(t *testing.T, line string) metricSample {
	t.Helper()
	fields := strings.Fields(line)
	if len(fields) < 2 {
		t.Fatalf("sample %q", line)
	}
	head := fields[0]
	name := head
	labels := map[string]string{}
	if i := strings.IndexByte(head, '{'); i >= 0 {
		name = head[:i]
		raw := strings.TrimSuffix(head[i+1:], "}")
		for _, pair := range strings.Split(raw, ",") {
			if pair == "" {
				continue
			}
			key, value, ok := strings.Cut(pair, "=")
			if !ok || len(value) < 2 {
				t.Fatalf("label %q", pair)
			}
			labels[key] = strings.Trim(value, `"`)
		}
	}
	value, err := strconv.ParseFloat(fields[1], 64)
	if err != nil {
		t.Fatalf("value %q: %v", line, err)
	}
	return metricSample{name: name, labels: labels, value: value}
}

func addLabels() map[string]string {
	return map[string]string{"model": "add", "version": "1"}
}

func findMetric(samples []metricSample, name string, labels map[string]string) (metricSample, bool) {
	for _, sample := range samples {
		if sample.name != name || !sameLabels(sample.labels, labels) {
			continue
		}
		return sample, true
	}
	return metricSample{}, false
}

func sameLabels(got, want map[string]string) bool {
	if want == nil {
		return got["model"] == "add" && got["version"] == "1" && len(got) == 2
	}
	if len(got) != len(want) {
		return false
	}
	for key, value := range want {
		if got[key] != value {
			return false
		}
	}
	return true
}

func assertCounts(t *testing.T, reg *metrics.Registry, success, count, exec float64) {
	t.Helper()
	assertValue(t, reg, "inference_request_success", nil, success)
	assertValue(t, reg, "inference_count", nil, count)
	assertValue(t, reg, "inference_exec_count", nil, exec)
}

func assertPending(t *testing.T, reg *metrics.Registry, want float64) {
	t.Helper()
	assertValue(t, reg, "inference_pending_request_count", nil, want)
}

func assertFailure(t *testing.T, reg *metrics.Registry, reason metrics.Reason, want float64) {
	t.Helper()
	labels := map[string]string{"model": "add", "version": "1", "reason": string(reason)}
	assertValue(t, reg, "inference_request_failure", labels, want)
}

func assertValue(t *testing.T, reg *metrics.Registry, name string, labels map[string]string, want float64) {
	t.Helper()
	if labels == nil {
		labels = addLabels()
	}
	got := sampleValue(t, reg, name, labels)
	if got != want {
		t.Fatalf("%s = %v, want %v", name, got, want)
	}
}

func assertNoSample(t *testing.T, reg *metrics.Registry, name string) {
	t.Helper()
	for _, sample := range scrapeRegistry(t, reg) {
		if sample.name == name && sample.labels["model"] == "add" && sample.labels["version"] == "1" {
			t.Fatalf("%s is present at %v", name, sample.value)
		}
	}
}

func sampleValue(t *testing.T, reg *metrics.Registry, name string, labels map[string]string) float64 {
	t.Helper()
	sample, ok := findMetric(scrapeRegistry(t, reg), name, labels)
	if !ok {
		return 0
	}
	return sample.value
}
