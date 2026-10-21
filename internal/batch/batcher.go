package batch

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"slices"
	"sync"
	"sync/atomic"
	"time"
)

// Pool lends the byte buffers that hold a stacked input or one caller's rows of a stacked output.
type Pool interface {
	// Get returns a buffer of length n.
	Get(n int) ([]byte, error)
	// Put returns a buffer that Get returned.
	Put(b []byte)
}

// Slot runs one batch on a version's single execution slot.
// inputs are in model order. A one-request batch passes that caller's tensors unchanged.
// release returns the memory behind outputs and may be nil. The batcher calls it once, after it has read the outputs.
type Slot func(ctx context.Context, inputs []Tensor) (outputs []Tensor, release func(), err error)

// Key names one model version.
type Key struct {
	// Model is the model directory name.
	Model string
	// Version is the decimal version.
	Version string
}

// Config is what one version's batcher runs with.
type Config struct {
	// Key is the version this queue serves.
	Key Key
	// MaxBatchSize is the recorded batch cap. Zero runs each request as sent and never stacks.
	MaxBatchSize int
	// Delay is the recorded queue delay. Zero starts no timer.
	Delay time.Duration
	// Capacity is how many requests may wait. The batch inside the slot is not counted.
	Capacity int
	// Admit is the call gate. It returns nil when a run may proceed. A nil function always admits.
	Admit func() error
	// Pool lends buffers for stacked batches.
	Pool Pool
	// Clock is the time source. Nil is the wall clock.
	Clock Clock
	// Slot runs one batch.
	Slot Slot
	// Logger records batch timing at debug and failed runs at error. Nil logs nothing.
	// The caller's outcome is logged by the transport that wrote it.
	Logger *slog.Logger
	// Recorder counts each finished request and how many are waiting.
	// Nil counts nothing.
	Recorder Recorder
}

// Batcher is the queue of one model version.
// It stacks compatible waiting requests on axis 0 and calls the slot one batch at a time.
type Batcher struct {
	cfg  Config
	wake chan struct{}

	mu       sync.Mutex
	queue    []*waiter
	draining bool
}

// Reply is the outputs of one caller.
type Reply struct {
	// Outputs are in the order the slot returned them. Each holds only this caller's rows.
	Outputs []Tensor

	release func()
}

// Release returns the output memory. Outputs must not be read afterward.
func (r *Reply) Release() {
	if r == nil || r.release == nil {
		return
	}
	r.release()
	r.release = nil
}

type waiter struct {
	ctx     context.Context
	inputs  []Tensor
	rows    int64
	arrival time.Time
	done    chan outcome
	taken   bool
}

type outcome struct {
	resp *Reply
	err  error
}

var (
	errNoSlot     = errors.New("batcher needs a slot function")
	errNoPool     = errors.New("batcher needs a buffer pool")
	errCapacity   = errors.New("queue capacity must be at least 1")
	errBatchSize  = errors.New("max batch size must not be negative")
	errDelay      = errors.New("queue delay must not be negative")
	errNilRequest = errors.New("infer request is missing")
)

// New returns the queue of one version.
func New(cfg Config) (*Batcher, error) {
	switch {
	case cfg.Slot == nil:
		return nil, errNoSlot
	case cfg.Pool == nil:
		return nil, errNoPool
	case cfg.Capacity < 1:
		return nil, errCapacity
	case cfg.MaxBatchSize < 0:
		return nil, errBatchSize
	case cfg.Delay < 0:
		return nil, errDelay
	default:
	}
	if cfg.Clock == nil {
		cfg.Clock = SystemClock()
	}
	return &Batcher{cfg: cfg, wake: make(chan struct{}, 1)}, nil
}

// Infer queues req and returns this caller's outputs.
// The call gate is checked here and again when the request reaches the slot.
// A batch dimension above the cap is invalid and is not queued. A full queue returns ErrQueueFull and does not queue.
// The timeout parameter and ctx apply while the request waits. Once it is in the slot, ctx is the run's context and the timeout no longer applies.
func (b *Batcher) Infer(ctx context.Context, req *Request) (*Reply, error) {
	if req == nil {
		return nil, errNilRequest
	}
	if err := ctx.Err(); err != nil {
		b.refuse(err)
		return nil, err
	}
	if err := b.admit(); err != nil {
		b.refuse(err)
		return nil, err
	}
	rows, err := b.rows(req.Inputs)
	if err != nil {
		b.refuse(err)
		return nil, err
	}
	w := &waiter{ctx: ctx, inputs: req.Inputs, rows: rows, done: make(chan outcome, 1)}

	b.mu.Lock()
	if len(b.queue) >= b.cfg.Capacity {
		b.mu.Unlock()
		b.refuse(ErrQueueFull)
		return nil, ErrQueueFull
	}
	w.arrival = b.cfg.Clock.Now()
	b.queue = append(b.queue, w)
	b.publishPending()
	b.signal()
	if !b.draining {
		b.draining = true
		go b.drain()
	}
	b.mu.Unlock()

	return b.wait(w, queueTimeout(req.Parameters.Timeout))
}

func queueTimeout(microseconds int64) time.Duration {
	if microseconds > math.MaxInt64/int64(time.Microsecond) {
		return time.Duration(math.MaxInt64)
	}
	return time.Duration(microseconds) * time.Microsecond
}

func (b *Batcher) wait(w *waiter, timeout time.Duration) (*Reply, error) {
	var expired <-chan time.Time
	if timeout > 0 {
		t := b.cfg.Clock.NewTimer(timeout)
		defer t.Stop()
		expired = t.C()
	}
	canceled := w.ctx.Done()
	for {
		select {
		case o := <-w.done:
			return o.resp, o.err
		case <-expired:
			if b.withdraw(w) {
				b.refuseWaiter(w, ErrQueueTimeout)
				return nil, ErrQueueTimeout
			}
			expired = nil
		case <-canceled:
			if b.withdraw(w) {
				b.refuseWaiter(w, w.ctx.Err())
				return nil, w.ctx.Err()
			}
			canceled = nil
		}
	}
}

// withdraw removes a waiter that has not reached the slot. It reports false when the waiter is already in a batch.
func (b *Batcher) withdraw(w *waiter) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if w.taken {
		return false
	}
	if i := slices.Index(b.queue, w); i >= 0 {
		b.queue = slices.Delete(b.queue, i, i+1)
	}
	b.publishPending()
	b.signal()
	return true
}

func (b *Batcher) admit() error {
	if b.cfg.Admit == nil {
		return nil
	}
	return b.cfg.Admit()
}

// rows returns the client's batch size, or -1 when the request never shares a batch.
func (b *Batcher) rows(inputs []Tensor) (int64, error) {
	if b.cfg.MaxBatchSize == 0 || len(inputs) == 0 {
		return -1, nil
	}
	lead := int64(-1)
	for _, in := range inputs {
		if len(in.Shape) == 0 {
			return 0, invalidf("input %q has no batch dimension", in.Name)
		}
		if lead >= 0 && in.Shape[0] != lead {
			return 0, invalidf("input %q batch dimension is %d and the other inputs have %d", in.Name, in.Shape[0], lead)
		}
		lead = in.Shape[0]
	}
	if lead > int64(b.cfg.MaxBatchSize) {
		return 0, invalidf("batch dimension is %d and the model's max_batch_size is %d", lead, b.cfg.MaxBatchSize)
	}
	if lead == 0 {
		return -1, nil
	}
	return lead, nil
}

// signal wakes the drain loop. The caller holds mu.
func (b *Batcher) signal() {
	select {
	case b.wake <- struct{}{}:
	default:
	}
}

// drain runs batches until the queue is empty. Only one drain runs at a time, so the slot is never called twice at once.
func (b *Batcher) drain() {
	for {
		b.mu.Lock()
		select {
		case <-b.wake:
		default:
		}
		if len(b.queue) == 0 {
			b.draining = false
			b.mu.Unlock()
			return
		}
		n, wait := b.form()
		if wait > 0 {
			b.mu.Unlock()
			t := b.cfg.Clock.NewTimer(wait)
			select {
			case <-t.C():
			case <-b.wake:
			}
			t.Stop()
			continue
		}
		batch := slices.Clone(b.queue[:n])
		for _, w := range batch {
			w.taken = true
		}
		b.queue = slices.Delete(b.queue, 0, n)
		b.publishPending()
		b.mu.Unlock()
		b.run(batch)
	}
}

// form returns how many waiters from the head make the next batch, and how long to wait before running it.
// The batch is the longest run of waiters, in arrival order, that are compatible with the head and fit under the cap.
// A batch that is full, or that cannot grow because the next waiter does not fit, runs at once.
// Otherwise it waits out the delay, counted from the oldest waiter. The caller holds mu.
func (b *Batcher) form() (int, time.Duration) {
	head := b.queue[0]
	if head.rows < 0 {
		return 1, 0
	}
	n, sum := 1, head.rows
	for _, w := range b.queue[1:] {
		if !compatible(head, w) || sum+w.rows > int64(b.cfg.MaxBatchSize) {
			break
		}
		sum += w.rows
		n++
	}
	if b.cfg.Delay == 0 || sum >= int64(b.cfg.MaxBatchSize) || n < len(b.queue) {
		return n, 0
	}
	if wait := head.arrival.Add(b.cfg.Delay).Sub(b.cfg.Clock.Now()); wait > 0 {
		return n, wait
	}
	return n, 0
}

// compatible reports whether w may share a batch with head: same input names and datatypes, and equal dimensions after dimension 0.
func compatible(head, w *waiter) bool {
	if w.rows < 0 || len(w.inputs) != len(head.inputs) {
		return false
	}
	for i, in := range w.inputs {
		h := head.inputs[i]
		if in.Name != h.Name || in.Datatype != h.Datatype || !slices.Equal(in.Shape[1:], h.Shape[1:]) {
			return false
		}
	}
	return true
}

func (b *Batcher) run(batch []*waiter) {
	if err := b.admit(); err != nil {
		b.recordEach(false, 0, time.Time{}, b.cfg.Clock.Now(), batch, func(*waiter) error { return err }, false)
		for _, w := range batch {
			w.done <- outcome{err: err}
		}
		return
	}
	var skipped []*waiter
	live := make([]*waiter, 0, len(batch))
	for _, w := range batch {
		if err := w.ctx.Err(); err != nil {
			skipped = append(skipped, w)
			continue
		}
		live = append(live, w)
	}
	if len(skipped) > 0 {
		b.recordEach(false, 0, time.Time{}, b.cfg.Clock.Now(), skipped, func(w *waiter) error { return w.ctx.Err() }, false)
		for _, w := range skipped {
			w.done <- outcome{err: w.ctx.Err()}
		}
	}
	if len(live) > 1 && b.runStacked(live) {
		return
	}
	for _, w := range live {
		b.runAlone(w)
	}
}

// runAlone passes one caller's tensors to the slot without a copy.
func (b *Batcher) runAlone(w *waiter) {
	start := b.cfg.Clock.Now()
	outputs, release, err := b.cfg.Slot(w.ctx, w.inputs)
	end := b.cfg.Clock.Now()
	b.logRun(start, []*waiter{w}, err)
	b.recordEach(err == nil || failureRan(err), end.Sub(start), start, end, []*waiter{w}, func(*waiter) error { return err }, true)
	if err != nil {
		if release != nil {
			release()
		}
		w.done <- outcome{err: err}
		return
	}
	w.done <- outcome{resp: &Reply{Outputs: outputs, release: release}}
}

// runStacked concatenates the callers on axis 0 into pool buffers, runs once, and splits the outputs back.
// It reports false, without calling the slot, when the pool cannot hold the stacked inputs.
func (b *Batcher) runStacked(live []*waiter) bool {
	inputs, buffers, ok := b.stack(live)
	if !ok {
		return false
	}
	ctx, stop := mergedContext(live)
	start := b.cfg.Clock.Now()
	outputs, release, err := b.cfg.Slot(ctx, inputs)
	stop()
	for _, buf := range buffers {
		b.cfg.Pool.Put(buf)
	}
	b.logRun(start, live, err)
	var responses []*Reply
	if err == nil {
		responses, err = b.split(live, outputs)
	}
	if release != nil {
		release()
	}
	end := b.cfg.Clock.Now()
	b.recordEach(err == nil || failureRan(err), end.Sub(start), start, end, live, func(w *waiter) error {
		if err == nil {
			return nil
		}
		if ctxErr := w.ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return err
	}, true)
	for i, w := range live {
		switch ctxErr := w.ctx.Err(); {
		case err == nil:
			w.done <- outcome{resp: responses[i]}
		case ctxErr != nil:
			w.done <- outcome{err: ctxErr}
		default:
			w.done <- outcome{err: err}
		}
	}
	return true
}

func (b *Batcher) stack(live []*waiter) ([]Tensor, [][]byte, bool) {
	head := live[0]
	var rows int64
	for _, w := range live {
		rows += w.rows
	}
	inputs := make([]Tensor, len(head.inputs))
	buffers := make([][]byte, 0, len(head.inputs))
	for i, in := range head.inputs {
		size := 0
		for _, w := range live {
			size += len(w.inputs[i].Data)
		}
		var data []byte
		if size > 0 {
			buf, err := b.cfg.Pool.Get(size)
			if err != nil {
				for _, got := range buffers {
					b.cfg.Pool.Put(got)
				}
				return nil, nil, false
			}
			buffers = append(buffers, buf)
			data = buf[:0]
			for _, w := range live {
				data = append(data, w.inputs[i].Data...)
			}
		}
		shape := slices.Clone(in.Shape)
		shape[0] = rows
		inputs[i] = Tensor{Name: in.Name, Datatype: in.Datatype, Shape: shape, Data: data}
	}
	return inputs, buffers, true
}

// split gives each caller its own rows of every output, copied into pool buffers.
// No caller receives rows when an output cannot be split.
func (b *Batcher) split(live []*waiter, outputs []Tensor) ([]*Reply, error) {
	rows := make([]int64, len(live))
	for i, w := range live {
		rows[i] = w.rows
	}
	parts := make([][][]byte, len(outputs))
	for j, out := range outputs {
		p, err := splitRows(out, rows)
		if err != nil {
			return nil, err
		}
		parts[j] = p
	}
	responses := make([]*Reply, len(live))
	for i := range live {
		resp := &Reply{Outputs: make([]Tensor, len(outputs))}
		var held [][]byte
		for j, out := range outputs {
			shape := slices.Clone(out.Shape)
			shape[0] = rows[i]
			data := b.copyOut(parts[j][i], &held)
			resp.Outputs[j] = Tensor{Name: out.Name, Datatype: out.Datatype, Shape: shape, Data: data}
		}
		resp.release = func() {
			for _, buf := range held {
				b.cfg.Pool.Put(buf)
			}
		}
		responses[i] = resp
	}
	return responses, nil
}

func (b *Batcher) copyOut(src []byte, held *[][]byte) []byte {
	if len(src) == 0 {
		return nil
	}
	buf, err := b.cfg.Pool.Get(len(src))
	if err != nil {
		return slices.Clone(src)
	}
	*held = append(*held, buf)
	copy(buf, src)
	return buf
}

// mergedContext is canceled once every caller's context is done, so one caller leaving does not stop the others' run.
func mergedContext(live []*waiter) (context.Context, func()) {
	ctx, cancel := context.WithCancel(context.Background())
	var remaining atomic.Int64
	remaining.Store(int64(len(live)))
	stops := make([]func() bool, len(live))
	for i, w := range live {
		stops[i] = context.AfterFunc(w.ctx, func() {
			if remaining.Add(-1) == 0 {
				cancel()
			}
		})
	}
	return ctx, func() {
		for _, stop := range stops {
			stop()
		}
		cancel()
	}
}

func (b *Batcher) logRun(start time.Time, batch []*waiter, err error) {
	if b.cfg.Logger == nil {
		return
	}
	k := b.cfg.Key
	if err != nil {
		result := Status(err)
		b.cfg.Logger.Log(context.Background(), result.Level, "model run did not finish",
			"model", k.Model, "version", k.Version, "requests", len(batch), "error", result.Message)
		return
	}
	var rows int64
	for _, w := range batch {
		rows += max(w.rows, 0)
	}
	b.cfg.Logger.Debug("batch ran",
		"model", k.Model,
		"version", k.Version,
		"requests", len(batch),
		"rows", rows,
		"queue_wait", start.Sub(batch[0].arrival).String(),
		"run", b.cfg.Clock.Now().Sub(start).String(),
	)
}
