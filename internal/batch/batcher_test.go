package batch

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
)

const loadingReason = "model is loading"

func TestTwoCompatibleRequestsAreOneSlotCall(t *testing.T) {
	r := newRig(t, rigOptions{maxBatch: 4, held: true})
	ctx := context.Background()

	first := r.submit(ctx, rowsRequest(1, 0, 0))
	r.slot.waitEntered(t)
	reqA := rowsRequest(1, 1, 2)
	reqB := rowsRequest(2, 3, 4, 5, 6)
	a := r.submitQueued(ctx, t, reqA, 1)
	b := r.submitQueued(ctx, t, reqB, 2)

	r.slot.release()
	r.slot.waitEntered(t)
	call := r.slot.call(t, 1)
	if len(call.inputs) != 1 {
		t.Fatalf("stacked call has %d inputs", len(call.inputs))
	}
	in := call.inputs[0]
	if !slices.Equal(in.Shape, []int64{3, 2}) {
		t.Fatalf("stacked shape %v, want [3 2]", in.Shape)
	}
	if !bytes.Equal(call.data[0], fp32Bytes(1, 2, 3, 4, 5, 6)) {
		t.Fatalf("stacked bytes are not the axis-0 concatenation: %v", call.data[0])
	}
	if sameArray(in.Data, reqA.Inputs[0].Data) || sameArray(in.Data, reqB.Inputs[0].Data) {
		t.Fatal("stacked input is a caller slice")
	}
	if !r.pool.lent(in.Data) || cap(in.Data) != 4<<10 {
		t.Fatalf("stacked input is not a pool buffer from the 4 KiB class (cap %d)", cap(in.Data))
	}
	r.slot.release()

	respA, err := a.result(t)
	if err != nil {
		t.Fatalf("first stacked caller: %v", err)
	}
	respB, err := b.result(t)
	if err != nil {
		t.Fatalf("second stacked caller: %v", err)
	}
	wantOutput(t, respA, []int64{1, 2}, 1, 2)
	wantOutput(t, respB, []int64{2, 2}, 3, 4, 5, 6)
	if _, err := first.result(t); err != nil {
		t.Fatalf("held request: %v", err)
	}
	if !r.pool.returned(in.Data) {
		t.Fatal("stacked input buffer was not returned to the pool")
	}
	if got := r.slot.released.Load(); got != 1 {
		t.Fatalf("stacked slot outputs released %d times, want 1", got)
	}
	outA := respA.Outputs[0].Data
	respA.Release()
	respB.Release()
	if !r.pool.returned(outA) {
		t.Fatal("caller rows were not returned to the pool on release")
	}
	if n := r.slot.callCount(); n != 2 {
		t.Fatalf("slot called %d times, want 2", n)
	}
}

func TestOneRequestPassesTheCallerArray(t *testing.T) {
	r := newRig(t, rigOptions{maxBatch: 4})
	req := rowsRequest(2, 1, 2, 3, 4)
	resp, err := r.b.Infer(context.Background(), req)
	if err != nil {
		t.Fatalf("infer: %v", err)
	}
	call := r.slot.call(t, 0)
	if !sameArray(call.inputs[0].Data, req.Inputs[0].Data) {
		t.Fatal("slot did not receive the caller's backing array")
	}
	if r.pool.getCount() != 0 {
		t.Fatalf("a one-request batch took %d pool buffers", r.pool.getCount())
	}
	wantOutput(t, resp, []int64{2, 2}, 1, 2, 3, 4)
	resp.Release()
	if r.slot.released.Load() != 1 {
		t.Fatal("release did not reach the slot's outputs")
	}
}

func TestBatchAboveCapIsInvalidAndNotQueued(t *testing.T) {
	r := newRig(t, rigOptions{maxBatch: 2})
	_, err := r.b.Infer(context.Background(), rowsRequest(3, 1, 2, 3))
	wantInvalid(t, err, "max_batch_size is 2")
	if res := Status(err); res.HTTP != http.StatusBadRequest || res.GRPC != codes.InvalidArgument {
		t.Fatalf("status %+v", res)
	}
	if r.waiting() != 0 || r.slot.callCount() != 0 {
		t.Fatal("an oversized request was queued or run")
	}
}

func TestInputsThatDisagreeOnBatchAreInvalid(t *testing.T) {
	r := newRig(t, rigOptions{maxBatch: 4})
	req := &Request{Inputs: []Tensor{
		{Name: "a", Datatype: TypeFP32, Shape: []int64{1, 1}, Data: fp32Bytes(1)},
		{Name: "b", Datatype: TypeFP32, Shape: []int64{2, 1}, Data: fp32Bytes(1, 2)},
	}}
	_, err := r.b.Infer(context.Background(), req)
	wantInvalid(t, err, "batch dimension")
}

func TestTailMismatchRunsAlone(t *testing.T) {
	r := newRig(t, rigOptions{maxBatch: 4, held: true})
	ctx := context.Background()
	first := r.submit(ctx, rowsRequest(1, 0, 0))
	r.slot.waitEntered(t)
	reqA := rowsRequest(1, 1, 2)
	reqB := rowsRequest(1, 3, 4, 5)
	a := r.submitQueued(ctx, t, reqA, 1)
	b := r.submitQueued(ctx, t, reqB, 2)
	r.slot.releaseAll()

	respA, errA := a.result(t)
	respB, errB := b.result(t)
	if errA != nil || errB != nil {
		t.Fatalf("results: %v, %v", errA, errB)
	}
	if _, err := first.result(t); err != nil {
		t.Fatal(err)
	}
	if n := r.slot.callCount(); n != 3 {
		t.Fatalf("slot called %d times, want 3", n)
	}
	if !sameArray(r.slot.call(t, 1).inputs[0].Data, reqA.Inputs[0].Data) ||
		!sameArray(r.slot.call(t, 2).inputs[0].Data, reqB.Inputs[0].Data) {
		t.Fatal("a request with a different tail did not run alone on its own array")
	}
	wantOutput(t, respA, []int64{1, 2}, 1, 2)
	wantOutput(t, respB, []int64{1, 3}, 3, 4, 5)
}

func TestUnbatchedModelDoesNotStackOrCap(t *testing.T) {
	r := newRig(t, rigOptions{maxBatch: 0, held: true})
	ctx := context.Background()
	big := rowsRequest(5, 1, 2, 3, 4, 5)
	first := r.submit(ctx, big)
	r.slot.waitEntered(t)
	reqA := rowsRequest(1, 1, 2)
	reqB := rowsRequest(1, 3, 4)
	a := r.submitQueued(ctx, t, reqA, 1)
	b := r.submitQueued(ctx, t, reqB, 2)
	r.slot.releaseAll()

	for _, p := range []*pending{first, a, b} {
		if _, err := p.result(t); err != nil {
			t.Fatalf("result: %v", err)
		}
	}
	if n := r.slot.callCount(); n != 3 {
		t.Fatalf("slot called %d times, want 3 separate runs", n)
	}
	for i, req := range []*Request{big, reqA, reqB} {
		if !sameArray(r.slot.call(t, i).inputs[0].Data, req.Inputs[0].Data) {
			t.Fatalf("call %d did not receive its caller's array", i)
		}
	}
}

func TestZeroDelayRunsWithoutATimer(t *testing.T) {
	r := newRig(t, rigOptions{maxBatch: 4})
	resp, err := r.b.Infer(context.Background(), rowsRequest(1, 1, 2))
	if err != nil {
		t.Fatal(err)
	}
	wantOutput(t, resp, []int64{1, 2}, 1, 2)
	if n := r.clock.timersCreated(); n != 0 {
		t.Fatalf("delay 0 created %d timers", n)
	}
}

func TestDelayWaitsOnTheClock(t *testing.T) {
	r := newRig(t, rigOptions{maxBatch: 4, delay: 100 * time.Microsecond})
	req := rowsRequest(1, 1, 2)
	p := r.submit(context.Background(), req)
	waitFor(t, "delay timer", func() bool { return r.clock.pending() == 1 })
	if r.slot.callCount() != 0 || p.finished() {
		t.Fatal("a lone request ran before the delay elapsed")
	}
	r.clock.Advance(99 * time.Microsecond)
	if r.slot.callCount() != 0 {
		t.Fatal("request ran before the full delay")
	}
	r.clock.Advance(time.Microsecond)
	resp, err := p.result(t)
	if err != nil {
		t.Fatal(err)
	}
	wantOutput(t, resp, []int64{1, 2}, 1, 2)
	if !sameArray(r.slot.call(t, 0).inputs[0].Data, req.Inputs[0].Data) {
		t.Fatal("lone request after the delay was copied")
	}
}

func TestDelayEndsWhenTheBatchIsFull(t *testing.T) {
	r := newRig(t, rigOptions{maxBatch: 3, delay: time.Hour})
	ctx := context.Background()
	a := r.submit(ctx, rowsRequest(1, 1, 2))
	waitFor(t, "delay timer", func() bool { return r.clock.pending() == 1 })
	b := r.submit(ctx, rowsRequest(2, 3, 4, 5, 6))
	respA, err := a.result(t)
	if err != nil {
		t.Fatal(err)
	}
	respB, err := b.result(t)
	if err != nil {
		t.Fatal(err)
	}
	if n := r.slot.callCount(); n != 1 {
		t.Fatalf("slot called %d times, want 1", n)
	}
	if got := r.slot.call(t, 0).inputs[0].Shape; !slices.Equal(got, []int64{3, 2}) {
		t.Fatalf("stacked shape %v", got)
	}
	wantOutput(t, respA, []int64{1, 2}, 1, 2)
	wantOutput(t, respB, []int64{2, 2}, 3, 4, 5, 6)
}

func TestQueueHoldsCapacityWaiters(t *testing.T) {
	for _, maxBatch := range []int{4, 0} {
		r := newRig(t, rigOptions{maxBatch: maxBatch, held: true})
		ctx := context.Background()
		first := r.submit(ctx, rowsRequest(1, 0, 0))
		r.slot.waitEntered(t)
		waiters := make([]*pending, 0, 8)
		for i := range 8 {
			waiters = append(waiters, r.submitQueued(ctx, t, rowsRequest(1, float32(i), 0), i+1))
		}
		_, err := r.b.Infer(ctx, rowsRequest(1, 9, 9))
		if !errors.Is(err, ErrQueueFull) {
			t.Fatalf("max batch %d: ninth waiter got %v", maxBatch, err)
		}
		if r.waiting() != 8 {
			t.Fatalf("max batch %d: ninth waiter was queued", maxBatch)
		}
		full, loading := Status(err), Status(Loading(loadingReason))
		if full.HTTP != http.StatusServiceUnavailable || full.GRPC != codes.Unavailable {
			t.Fatalf("queue full status %+v", full)
		}
		if full.Message == loading.Message {
			t.Fatal("queue-full text equals the loading text")
		}
		r.slot.releaseAll()
		for _, p := range append(waiters, first) {
			if _, err := p.result(t); err != nil {
				t.Fatalf("max batch %d: accepted waiter failed: %v", maxBatch, err)
			}
		}
	}
}

func TestPriorityDoesNotReorder(t *testing.T) {
	r := newRig(t, rigOptions{maxBatch: 1, held: true})
	ctx := context.Background()
	first := r.submit(ctx, rowsRequest(1, 0))
	r.slot.waitEntered(t)
	var waiters []*pending
	for i, priority := range []int64{0, 1, -5} {
		req := rowsRequest(1, float32(i+1))
		req.Parameters.Priority = priority
		waiters = append(waiters, r.submitQueued(ctx, t, req, i+1))
	}
	r.slot.releaseAll()
	for _, p := range append(waiters, first) {
		if _, err := p.result(t); err != nil {
			t.Fatalf("priority request failed: %v", err)
		}
	}
	for i := range 3 {
		if got := r.slot.call(t, i+1).data[0]; !bytes.Equal(got, fp32Bytes(float32(i+1))) {
			t.Fatalf("run %d was %v, want arrival order", i+1, got)
		}
	}
}

func TestZeroTimeoutDoesNotExpire(t *testing.T) {
	r := newRig(t, rigOptions{maxBatch: 4, held: true})
	ctx := context.Background()
	first := r.submit(ctx, rowsRequest(1, 0, 0))
	r.slot.waitEntered(t)
	p := r.submitQueued(ctx, t, rowsRequest(1, 1, 2), 1)
	r.clock.Advance(24 * time.Hour)
	if n := r.clock.timersCreated(); n != 0 {
		t.Fatalf("timeout 0 created %d timers", n)
	}
	if p.finished() || r.waiting() != 1 {
		t.Fatal("timeout 0 expired the waiter")
	}
	r.slot.releaseAll()
	if _, err := p.result(t); err != nil {
		t.Fatal(err)
	}
	if _, err := first.result(t); err != nil {
		t.Fatal(err)
	}
}

func TestTimeoutFiresWhileWaiting(t *testing.T) {
	r := newRig(t, rigOptions{maxBatch: 4, held: true})
	ctx := context.Background()
	first := r.submit(ctx, rowsRequest(1, 0, 0))
	r.slot.waitEntered(t)
	req := rowsRequest(1, 1, 2)
	req.Parameters.Timeout = 100
	p := r.submitQueued(ctx, t, req, 1)
	waitFor(t, "timeout timer", func() bool { return r.clock.pending() == 1 })
	r.clock.Advance(100 * time.Microsecond)
	_, err := p.result(t)
	if !errors.Is(err, ErrQueueTimeout) {
		t.Fatalf("got %v, want the queue timeout", err)
	}
	if res := Status(err); res.HTTP != http.StatusGatewayTimeout || res.GRPC != codes.DeadlineExceeded {
		t.Fatalf("status %+v", res)
	}
	if r.waiting() != 0 {
		t.Fatal("expired waiter stayed in the queue")
	}
	r.slot.releaseAll()
	if _, err := first.result(t); err != nil {
		t.Fatal(err)
	}
	if n := r.slot.callCount(); n != 1 {
		t.Fatalf("slot called %d times; the expired waiter ran", n)
	}
}

func TestTimeoutDuringRunDoesNotFailTheRun(t *testing.T) {
	r := newRig(t, rigOptions{maxBatch: 4, held: true})
	req := rowsRequest(1, 1, 2)
	req.Parameters.Timeout = 100
	p := r.submit(context.Background(), req)
	r.slot.waitEntered(t)
	waitFor(t, "timeout timer", func() bool { return r.clock.pending() == 1 })
	r.clock.Advance(time.Second)
	r.slot.release()
	resp, err := p.result(t)
	if err != nil {
		t.Fatalf("a timeout during the run failed it: %v", err)
	}
	wantOutput(t, resp, []int64{1, 2}, 1, 2)
}

func TestCancelWhileWaiting(t *testing.T) {
	r := newRig(t, rigOptions{maxBatch: 4, held: true})
	first := r.submit(context.Background(), rowsRequest(1, 0, 0))
	r.slot.waitEntered(t)
	ctx, cancel := context.WithCancel(context.Background())
	p := r.submitQueued(ctx, t, rowsRequest(1, 1, 2), 1)
	cancel()
	_, err := p.result(t)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	if res := Status(err); res.HTTP != http.StatusBadRequest || res.GRPC != codes.Canceled {
		t.Fatalf("status %+v", res)
	}
	if r.waiting() != 0 {
		t.Fatal("canceled waiter stayed in the queue")
	}
	r.slot.releaseAll()
	if _, err := first.result(t); err != nil {
		t.Fatal(err)
	}
	if r.slot.callCount() != 1 {
		t.Fatal("canceled waiter ran")
	}
}

func TestContextDeadlineWhileWaiting(t *testing.T) {
	r := newRig(t, rigOptions{maxBatch: 4, held: true})
	first := r.submit(context.Background(), rowsRequest(1, 0, 0))
	r.slot.waitEntered(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	p := r.submit(ctx, rowsRequest(1, 1, 2))
	_, err := p.result(t)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v", err)
	}
	res := Status(err)
	if res.HTTP != http.StatusGatewayTimeout || res.GRPC != codes.DeadlineExceeded {
		t.Fatalf("status %+v", res)
	}
	if res.Message == Status(ErrQueueTimeout).Message {
		t.Fatal("context deadline text equals the queue timeout text")
	}
	r.slot.releaseAll()
	if _, err := first.result(t); err != nil {
		t.Fatal(err)
	}
}

func TestCancelDuringRunCancelsTheSlot(t *testing.T) {
	r := newRig(t, rigOptions{maxBatch: 4, held: true})
	ctx, cancel := context.WithCancel(context.Background())
	p := r.submit(ctx, rowsRequest(1, 1, 2))
	r.slot.waitEntered(t)
	cancel()
	if _, err := p.result(t); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}

func TestStackedRunOutlivesOneCanceledCaller(t *testing.T) {
	r := newRig(t, rigOptions{maxBatch: 4, held: true})
	bg := context.Background()
	first := r.submit(bg, rowsRequest(1, 0, 0))
	r.slot.waitEntered(t)
	ctx, cancel := context.WithCancel(bg)
	a := r.submitQueued(ctx, t, rowsRequest(1, 1, 2), 1)
	b := r.submitQueued(bg, t, rowsRequest(1, 3, 4), 2)
	r.slot.release()
	r.slot.waitEntered(t)
	cancel()
	r.slot.release()
	if _, err := first.result(t); err != nil {
		t.Fatal(err)
	}
	resp, err := b.result(t)
	if err != nil {
		t.Fatalf("other caller lost its run: %v", err)
	}
	wantOutput(t, resp, []int64{1, 2}, 3, 4)
	if resp, err := a.result(t); err != nil {
		t.Fatalf("canceled caller whose run finished: %v", err)
	} else {
		resp.Release()
	}
}

func TestLoadingAfterAdmissionStopsWaiters(t *testing.T) {
	r := newRig(t, rigOptions{maxBatch: 4, held: true})
	ctx := context.Background()
	first := r.submit(ctx, rowsRequest(1, 1, 2))
	r.slot.waitEntered(t)
	waiter := r.submitQueued(ctx, t, rowsRequest(1, 3, 4), 1)
	r.gate.set(Loading(loadingReason))
	r.slot.release()

	resp, err := first.result(t)
	if err != nil {
		t.Fatalf("in-flight run: %v", err)
	}
	wantOutput(t, resp, []int64{1, 2}, 1, 2)
	_, err = waiter.result(t)
	res := Status(err)
	if res.HTTP != http.StatusServiceUnavailable || res.GRPC != codes.Unavailable || res.Message != loadingReason {
		t.Fatalf("waiter got %+v", res)
	}
	if n := r.slot.callCount(); n != 1 {
		t.Fatalf("slot called %d times; the waiter ran after loading", n)
	}
	if _, err := r.b.Infer(ctx, rowsRequest(1, 5, 6)); Status(err).Message != loadingReason {
		t.Fatalf("submit while loading: %v", err)
	}
}

func TestRuntimeErrorReachesEveryStackedCaller(t *testing.T) {
	r := newRig(t, rigOptions{maxBatch: 4, held: true})
	ctx := context.Background()
	first := r.submit(ctx, rowsRequest(1, 0, 0))
	r.slot.waitEntered(t)
	a := r.submitQueued(ctx, t, rowsRequest(1, 1, 2), 1)
	b := r.submitQueued(ctx, t, rowsRequest(1, 3, 4), 2)
	r.slot.err = errors.New("kernel failed")
	r.slot.releaseAll()
	for _, p := range []*pending{first, a, b} {
		_, err := p.result(t)
		if res := Status(err); res.HTTP != http.StatusInternalServerError || res.GRPC != codes.Internal {
			t.Fatalf("got %v, %+v", err, res)
		}
	}
}

func TestStatusTable(t *testing.T) {
	unavailableReason := "model file could not be parsed"
	cases := []struct {
		name string
		err  error
		http int
		code codes.Code
		text string
	}{
		{"success", nil, http.StatusOK, codes.OK, ""},
		{"loading", Loading(loadingReason), http.StatusServiceUnavailable, codes.Unavailable, loadingReason},
		{"not found", NotFound(`model "x" was not found`), http.StatusNotFound, codes.NotFound, `model "x" was not found`},
		{"unavailable", Unavailable(unavailableReason), http.StatusBadRequest, codes.FailedPrecondition, unavailableReason},
		{"queue full", ErrQueueFull, http.StatusServiceUnavailable, codes.Unavailable, queueFullText},
		{"queue timeout", ErrQueueTimeout, http.StatusGatewayTimeout, codes.DeadlineExceeded, queueTimeoutText},
		{"context deadline", context.DeadlineExceeded, http.StatusGatewayTimeout, codes.DeadlineExceeded, deadlineText},
		{"cancel", context.Canceled, http.StatusBadRequest, codes.Canceled, canceledText},
		{"invalid", invalidf("bad shape"), http.StatusBadRequest, codes.InvalidArgument, "bad shape"},
		{"runtime", errors.New("onnx failure"), http.StatusInternalServerError, codes.Internal, "model run failed: onnx failure"},
		{"unimplemented", Unimplemented("load is not supported"), http.StatusNotImplemented, codes.Unimplemented, "load is not supported"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := Status(c.err)
			if res.HTTP != c.http || res.GRPC != c.code || res.Message != c.text {
				t.Fatalf("got %+v, want %d %v %q", res, c.http, c.code, c.text)
			}
		})
	}
	if Status(Loading(loadingReason)).Message == Status(ErrQueueFull).Message {
		t.Fatal("loading and queue-full texts are equal")
	}
	if Status(ErrQueueTimeout).Message == Status(context.DeadlineExceeded).Message {
		t.Fatal("queue timeout and context deadline texts are equal")
	}
	if Status(errors.Join(errors.New("wrapped"), context.Canceled)).GRPC != codes.Canceled {
		t.Fatal("a wrapped cancel is not the cancel row")
	}
}

func TestStatusLevels(t *testing.T) {
	if Status(nil).Level != slog.LevelDebug {
		t.Fatal("success is not debug")
	}
	for _, err := range []error{ErrQueueFull, Loading(loadingReason), invalidf("x"), context.Canceled} {
		if Status(err).Level != slog.LevelWarn {
			t.Fatalf("rejected caller %v is not warn", err)
		}
	}
	if Status(errors.New("boom")).Level != slog.LevelError {
		t.Fatal("a failed run is not error")
	}
}

func TestGateError(t *testing.T) {
	if err := GateError(0, ""); err != nil {
		t.Fatalf("an open gate returned %v", err)
	}
	if res := Status(GateError(http.StatusServiceUnavailable, loadingReason)); res.GRPC != codes.Unavailable || res.Message != loadingReason {
		t.Fatalf("loading gate %+v", res)
	}
	if res := Status(GateError(http.StatusNotFound, "gone")); res.HTTP != http.StatusNotFound {
		t.Fatalf("missing gate %+v", res)
	}
	if res := Status(GateError(0, "bad file")); res.HTTP != http.StatusBadRequest || res.GRPC != codes.FailedPrecondition || res.Message != "bad file" {
		t.Fatalf("unavailable gate %+v", res)
	}
}

func TestNewRejectsBadConfig(t *testing.T) {
	pool := newRecordingPool(t)
	slot := newFakeSlot(false).run
	for name, cfg := range map[string]Config{
		"no slot":        {Pool: pool, Capacity: 8},
		"no pool":        {Slot: slot, Capacity: 8},
		"zero capacity":  {Slot: slot, Pool: pool},
		"negative batch": {Slot: slot, Pool: pool, Capacity: 8, MaxBatchSize: -1},
		"negative delay": {Slot: slot, Pool: pool, Capacity: 8, Delay: -time.Second},
	} {
		if _, err := New(cfg); err == nil {
			t.Fatalf("%s: accepted", name)
		}
	}
}

func TestLogHasTimingAndNoTensorValues(t *testing.T) {
	var buf bytes.Buffer
	pool := newRecordingPool(t)
	slot := newFakeSlot(false)
	b, err := New(Config{
		Key:          Key{Model: "m", Version: "1"},
		MaxBatchSize: 4,
		Capacity:     8,
		Pool:         pool,
		Clock:        newFakeClock(),
		Slot:         slot.run,
		Logger:       slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})),
	})
	if err != nil {
		t.Fatal(err)
	}
	req := &Request{Inputs: []Tensor{{Name: "x", Datatype: TypeBytes, Shape: []int64{1, 1}, Data: bytesElements("planted-secret-value")}}}
	resp, err := b.Infer(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Release()
	slot.err = errors.New("kernel failed")
	if _, err := b.Infer(context.Background(), req); err == nil {
		t.Fatal("slot failure was not returned")
	}
	log := buf.String()
	if !strings.Contains(log, `"level":"DEBUG","msg":"batch ran"`) || !strings.Contains(log, `"level":"ERROR"`) {
		t.Fatalf("log is missing timing or the failure: %s", log)
	}
	if strings.Contains(log, "planted-secret-value") {
		t.Fatal("log contains a tensor value")
	}
}

func TestSplitBytesRows(t *testing.T) {
	out := Tensor{Name: "s", Datatype: TypeBytes, Shape: []int64{3, 1}, Data: bytesElements("a", "bb", "ccc")}
	parts, err := splitRows(out, []int64{1, 2})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(parts[0], bytesElements("a")) || !bytes.Equal(parts[1], bytesElements("bb", "ccc")) {
		t.Fatalf("parts %v", parts)
	}
	if _, err := splitRows(Tensor{Name: "s", Datatype: TypeFP32, Shape: []int64{2}, Data: fp32Bytes(1, 2)}, []int64{1, 2}); err == nil {
		t.Fatal("an output without the stacked rows was split")
	}
}
