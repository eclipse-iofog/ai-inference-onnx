package batch

import (
	"bytes"
	"context"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/eclipse-iofog/ai-inference-onnx/internal/session"
)

const waitLimit = 5 * time.Second

// fakeClock moves only when the test advances it.
type fakeClock struct {
	mu      sync.Mutex
	now     time.Time
	timers  []*fakeTimer
	created int
}

type fakeTimer struct {
	clock *fakeClock
	when  time.Time
	c     chan time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Unix(1_000_000, 0)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) NewTimer(d time.Duration) Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.created++
	t := &fakeTimer{clock: c, when: c.now.Add(d), c: make(chan time.Time, 1)}
	if d <= 0 {
		t.c <- c.now
		return t
	}
	c.timers = append(c.timers, t)
	return t
}

func (t *fakeTimer) C() <-chan time.Time { return t.c }

func (t *fakeTimer) Stop() bool {
	c := t.clock
	c.mu.Lock()
	defer c.mu.Unlock()
	i := slices.Index(c.timers, t)
	if i < 0 {
		return false
	}
	c.timers = slices.Delete(c.timers, i, i+1)
	return true
}

// Advance moves the clock and fires every timer that is due.
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	kept := c.timers[:0]
	for _, t := range c.timers {
		if t.when.After(c.now) {
			kept = append(kept, t)
			continue
		}
		t.c <- c.now
	}
	c.timers = kept
}

func (c *fakeClock) pending() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.timers)
}

func (c *fakeClock) timersCreated() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.created
}

// recordingPool is the server's buffer pool, recording every buffer it lends and takes back.
type recordingPool struct {
	pool *session.Pool
	mu   sync.Mutex
	gets [][]byte
	puts [][]byte
}

func newRecordingPool(t *testing.T) *recordingPool {
	t.Helper()
	p, err := session.NewPool(session.DefaultCeiling)
	if err != nil {
		t.Fatalf("new pool: %v", err)
	}
	return &recordingPool{pool: p}
}

func (r *recordingPool) Get(n int) ([]byte, error) {
	buf, err := r.pool.Get(n, nil)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	r.gets = append(r.gets, buf.Data)
	r.mu.Unlock()
	return buf.Data, nil
}

func (r *recordingPool) Put(b []byte) {
	r.mu.Lock()
	r.puts = append(r.puts, b)
	r.mu.Unlock()
	r.pool.Put(session.Buffer{Data: b, Pooled: true})
}

func (r *recordingPool) lent(b []byte) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return containsArray(r.gets, b)
}

func (r *recordingPool) returned(b []byte) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return containsArray(r.puts, b)
}

func (r *recordingPool) getCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.gets)
}

func containsArray(list [][]byte, b []byte) bool {
	for _, x := range list {
		if sameArray(x, b) {
			return true
		}
	}
	return false
}

func sameArray(a, b []byte) bool {
	return len(a) > 0 && len(b) > 0 && &a[0] == &b[0]
}

// slotCall is one call the fake slot received.
type slotCall struct {
	// inputs are the tensors exactly as passed, so their backing arrays can be compared.
	inputs []Tensor
	// data is a copy of each input's bytes at call time.
	data [][]byte
}

// fakeSlot echoes input 0 as output "y". It records each call and whether two calls overlapped.
type fakeSlot struct {
	hold     chan struct{}
	entered  chan struct{}
	err      error
	active   atomic.Int32
	overlap  atomic.Bool
	released atomic.Int32

	mu    sync.Mutex
	calls []slotCall
}

// newFakeSlot returns a slot. When held, each call waits for one receive from hold, or for hold to close.
func newFakeSlot(held bool) *fakeSlot {
	f := &fakeSlot{entered: make(chan struct{}, 64)}
	if held {
		f.hold = make(chan struct{})
	}
	return f
}

func (f *fakeSlot) run(ctx context.Context, inputs []Tensor) ([]Tensor, func(), error) {
	if f.active.Add(1) > 1 {
		f.overlap.Store(true)
	}
	defer f.active.Add(-1)
	call := slotCall{inputs: inputs, data: make([][]byte, len(inputs))}
	for i, in := range inputs {
		call.data[i] = bytes.Clone(in.Data)
	}
	f.mu.Lock()
	f.calls = append(f.calls, call)
	f.mu.Unlock()
	f.entered <- struct{}{}
	if f.hold != nil {
		select {
		case <-f.hold:
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		}
	}
	if f.err != nil {
		return nil, nil, f.err
	}
	in := inputs[0]
	out := Tensor{Name: "y", Datatype: in.Datatype, Shape: slices.Clone(in.Shape), Data: bytes.Clone(in.Data)}
	return []Tensor{out}, func() { f.released.Add(1) }, nil
}

func (f *fakeSlot) release()       { f.hold <- struct{}{} }
func (f *fakeSlot) releaseAll()    { close(f.hold) }
func (f *fakeSlot) callCount() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.calls) }

func (f *fakeSlot) call(t *testing.T, i int) slotCall {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if i >= len(f.calls) {
		t.Fatalf("slot has %d calls, want call %d", len(f.calls), i)
	}
	return f.calls[i]
}

func (f *fakeSlot) waitEntered(t *testing.T) {
	t.Helper()
	select {
	case <-f.entered:
	case <-time.After(waitLimit):
		t.Fatal("slot was not called")
	}
}

// gate is a call gate the test can flip.
type gate struct {
	mu  sync.Mutex
	err error
}

func (g *gate) set(err error) {
	g.mu.Lock()
	g.err = err
	g.mu.Unlock()
}

func (g *gate) admit() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.err
}

type rig struct {
	b     *Batcher
	slot  *fakeSlot
	clock *fakeClock
	pool  *recordingPool
	gate  *gate
}

type rigOptions struct {
	maxBatch int
	delay    time.Duration
	capacity int
	held     bool
	recorder Recorder
	model    string
	version  string
}

func newRig(t *testing.T, o rigOptions) *rig {
	t.Helper()
	if o.capacity == 0 {
		o.capacity = 8
	}
	r := &rig{slot: newFakeSlot(o.held), clock: newFakeClock(), pool: newRecordingPool(t), gate: &gate{}}
	model, version := o.model, o.version
	if model == "" {
		model, version = "m", "1"
	}
	b, err := New(Config{
		Key:          Key{Model: model, Version: version},
		MaxBatchSize: o.maxBatch,
		Delay:        o.delay,
		Capacity:     o.capacity,
		Admit:        r.gate.admit,
		Pool:         r.pool,
		Clock:        r.clock,
		Slot:         r.slot.run,
		Recorder:     o.recorder,
	})
	if err != nil {
		t.Fatalf("new batcher: %v", err)
	}
	r.b = b
	t.Cleanup(func() {
		if r.slot.overlap.Load() {
			t.Error("two slot calls overlapped")
		}
	})
	return r
}

func (r *rig) waiting() int {
	r.b.mu.Lock()
	defer r.b.mu.Unlock()
	return len(r.b.queue)
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(waitLimit)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// pending is an Infer call running in its own goroutine.
type pending struct {
	resp *Reply
	err  error
	done chan struct{}
}

func (r *rig) submit(ctx context.Context, req *Request) *pending {
	p := &pending{done: make(chan struct{})}
	go func() {
		defer close(p.done)
		p.resp, p.err = r.b.Infer(ctx, req)
	}()
	return p
}

// submitQueued submits req and waits until the queue holds want waiters.
func (r *rig) submitQueued(ctx context.Context, t *testing.T, req *Request, want int) *pending {
	t.Helper()
	p := r.submit(ctx, req)
	waitFor(t, "request to queue", func() bool { return r.waiting() == want })
	return p
}

func (p *pending) result(t *testing.T) (*Reply, error) {
	t.Helper()
	select {
	case <-p.done:
		return p.resp, p.err
	case <-time.After(waitLimit):
		t.Fatal("infer did not return")
		return nil, nil
	}
}

func (p *pending) finished() bool {
	select {
	case <-p.done:
		return true
	default:
		return false
	}
}

// rowsRequest is input x of FP32 shape [rows, len(vals)/rows].
func rowsRequest(rows int64, vals ...float32) *Request {
	tail := int64(len(vals)) / rows
	return &Request{Inputs: []Tensor{{Name: "x", Datatype: TypeFP32, Shape: []int64{rows, tail}, Data: fp32Bytes(vals...)}}}
}

func wantOutput(t *testing.T, resp *Reply, shape []int64, vals ...float32) {
	t.Helper()
	if resp == nil || len(resp.Outputs) != 1 {
		t.Fatalf("response has no single output: %+v", resp)
	}
	out := resp.Outputs[0]
	if out.Name != "y" || !slices.Equal(out.Shape, shape) {
		t.Fatalf("output %q shape %v, want y %v", out.Name, out.Shape, shape)
	}
	if !bytes.Equal(out.Data, fp32Bytes(vals...)) {
		t.Fatalf("output bytes %v, want %v", out.Data, fp32Bytes(vals...))
	}
}
