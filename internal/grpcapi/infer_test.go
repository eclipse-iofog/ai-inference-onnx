package grpcapi

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/eclipse-iofog/ai-inference-onnx/api/openinference/v2"
	"github.com/eclipse-iofog/ai-inference-onnx/internal/batch"
	"github.com/eclipse-iofog/ai-inference-onnx/internal/config"
)

func TestInferValuesAndRawContents(t *testing.T) {
	t.Parallel()
	runner := &spyRunner{reply: threesReply()}
	client := startInfer(t, []batch.Directory{addDirectory()}, runner, 1<<20, nil)
	for _, version := range []string{"", "1"} {
		resp := inferAdd(t, client, version, "add-values", false)
		assertThrees(t, resp, "add-values")
		resp = inferAdd(t, client, version, "add-raw", true)
		assertThrees(t, resp, "add-raw")
	}
	if runner.calls.Load() != 4 {
		t.Fatalf("runs %d", runner.calls.Load())
	}
}

func TestInferRejections(t *testing.T) {
	t.Parallel()
	runner := &spyRunner{reply: threesReply()}
	client := startInfer(t, []batch.Directory{addDirectory(), halfDirectory()}, runner, 1<<20, nil)

	for _, version := range []string{"2", "latest", "01"} {
		_, err := client.ModelInfer(callCtx(t), addRequest(version, "x", false))
		assertStatus(t, err, codes.NotFound, `version "`+version+`" was not found`)
	}
	_, err := client.ModelInfer(callCtx(t), addRequest("", "x", false))
	if status.Code(err) != codes.OK {
		t.Fatal(err)
	}
	_, err = client.ModelInfer(callCtx(t), &openinference.ModelInferRequest{ModelName: "missing", Id: "x"})
	assertStatus(t, err, codes.NotFound, `model "missing" was not found`)

	both := addRequest("", "x", false)
	both.RawInputContents = [][]byte{float32LE(1, 1, 1, 1, 1, 1), float32LE(2, 2, 2, 2, 2, 2)}
	_, err = client.ModelInfer(callCtx(t), both)
	assertStatus(t, err, codes.InvalidArgument, "request sets raw input contents and tensor contents")

	_, err = client.ModelInfer(callCtx(t), &openinference.ModelInferRequest{
		ModelName: "half",
		Inputs: []*openinference.ModelInferRequest_InferInputTensor{{
			Name:     "h",
			Datatype: "FP16",
			Shape:    []int64{2},
			Contents: &openinference.InferTensorContents{Fp32Contents: []float32{1, 2}},
		}},
	})
	assertStatus(t, err, codes.InvalidArgument, `FP16 input "h" must be sent as raw contents`)

	sequenced := addRequest("", "x", false)
	sequenced.Parameters = map[string]*openinference.InferParameter{
		"sequence_id": {ParameterChoice: &openinference.InferParameter_Int64Param{Int64Param: 7}},
	}
	_, err = client.ModelInfer(callCtx(t), sequenced)
	assertStatus(t, err, codes.InvalidArgument, `request parameter "sequence_id" is not supported`)

	if runner.calls.Load() != 1 {
		t.Fatalf("rejected requests ran %d times", runner.calls.Load())
	}
}

func TestInferPayloadDoesNotRun(t *testing.T) {
	t.Parallel()
	runner := &spyRunner{reply: threesReply()}
	client := startInfer(t, []batch.Directory{wideDirectory()}, runner, 32, nil)
	_, err := client.ModelInfer(callCtx(t), &openinference.ModelInferRequest{
		ModelName: "wide",
		Inputs: []*openinference.ModelInferRequest_InferInputTensor{{
			Name:     "x",
			Datatype: "FP32",
			Shape:    []int64{9},
			Contents: &openinference.InferTensorContents{Fp32Contents: []float32{1}},
		}},
	})
	assertStatus(t, err, codes.InvalidArgument, payloadTooLarge)
	if runner.calls.Load() != 0 {
		t.Fatal("oversized tensor was queued")
	}
}

func TestInferQueueFullLoadingAndUnavailable(t *testing.T) {
	t.Parallel()
	const loadingReason = "weights are opening"
	const unavailableReason = "execution provider could not start"
	if batch.Status(batch.ErrQueueFull).Message == loadingReason {
		t.Fatal("queue and loading text are the same")
	}
	slot := newHoldSlot()
	t.Cleanup(slot.release)
	client := startInfer(t, []batch.Directory{
		boxDirectory(),
		{Name: "zeta", State: batch.StateLoading, Reason: loadingReason, Versions: []batch.ModelVersion{{Version: "1"}}},
		{Name: "alpha", State: batch.StateUnavailable, Reason: unavailableReason, Versions: []batch.ModelVersion{{Version: "1"}}},
	}, batchRunner{batcher: newBatcher(t, slot, 1, nil)}, 1<<20, nil)

	holder := inferAsync(client, boxRequest(0))
	waitEntered(t, slot)
	queued := inferAsync(client, boxRequest(0))
	extra := inferAsync(client, boxRequest(0))
	other, fullErr := firstErr(t, queued, extra)
	assertStatus(t, fullErr, codes.Unavailable, batch.Status(batch.ErrQueueFull).Message)
	if status.Convert(fullErr).Message() == loadingReason {
		t.Fatal("queue text is the loading text")
	}

	_, err := client.ModelInfer(callCtx(t), &openinference.ModelInferRequest{
		ModelName: "zeta",
		Inputs:    boxRequest(0).GetInputs(),
	})
	assertStatus(t, err, codes.Unavailable, loadingReason)
	_, err = client.ModelInfer(callCtx(t), &openinference.ModelInferRequest{
		ModelName:    "alpha",
		ModelVersion: "1",
		Inputs:       boxRequest(0).Inputs,
	})
	assertStatus(t, err, codes.FailedPrecondition, unavailableReason)

	slot.release()
	if err := waitCall(t, holder); err != nil {
		t.Fatal(err)
	}
	if err := waitCall(t, other); err != nil {
		t.Fatal(err)
	}
}

func TestInferQueueTimeout(t *testing.T) {
	t.Parallel()
	slot := newHoldSlot()
	t.Cleanup(slot.release)
	clock := newManualClock()
	client := startInfer(t, []batch.Directory{boxDirectory()}, batchRunner{batcher: newBatcher(t, slot, 4, clock)}, 1<<20, nil)

	holder := inferAsync(client, boxRequest(0))
	waitEntered(t, slot)
	timed := inferAsync(client, boxRequest(1000))
	waitArmed(t, clock)
	clock.Advance(time.Millisecond)
	err := waitCall(t, timed)
	assertStatus(t, err, codes.DeadlineExceeded, batch.Status(batch.ErrQueueTimeout).Message)
	slot.release()
	if err := waitCall(t, holder); err != nil {
		t.Fatal(err)
	}
}

func TestInferLogs(t *testing.T) {
	t.Parallel()
	const token = "Bearer planted-token-9f3c2ab1"
	const planted = float32(424242)
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	runner := &spyRunner{reply: threesReply()}
	client := startInfer(t, []batch.Directory{addDirectory()}, runner, 1<<20, logger)

	req := addRequest("", "add-json", false)
	req.Inputs[0].Contents.Fp32Contents[0] = planted
	resp, err := client.ModelInfer(withAuth(callCtx(t), token), req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetId() != "add-json" {
		t.Fatalf("id %q", resp.GetId())
	}
	_, err = client.ModelInfer(withAuth(callCtx(t), token), &openinference.ModelInferRequest{ModelName: "add"})
	assertStatus(t, err, codes.InvalidArgument, "", token)
	runner.err = errors.New("backend stopped")
	_, err = client.ModelInfer(withAuth(callCtx(t), token), addRequest("", "add-json", false))
	assertStatus(t, err, codes.Internal, "", token)

	text := buf.String()
	if strings.Contains(text, token) || strings.Contains(text, "planted-token") || strings.Contains(text, "424242") {
		t.Fatalf("log contains a credential or a tensor value:\n%s", text)
	}
	lines := decodeInferLog(t, text)
	if len(lines) != 3 {
		t.Fatalf("lines %d:\n%s", len(lines), text)
	}
	if !strings.EqualFold(lines[0].Level, "debug") || !strings.Contains(lines[0].Msg, "infer finished") || lines[0].Status != int(codes.OK) || lines[0].Duration == "" {
		t.Fatalf("success log %+v", lines[0])
	}
	if !strings.EqualFold(lines[1].Level, "warn") || !strings.Contains(lines[1].Msg, "infer rejected") {
		t.Fatalf("reject log %+v", lines[1])
	}
	if !strings.EqualFold(lines[2].Level, "error") || !strings.Contains(lines[2].Msg, "infer did not finish") || !strings.Contains(lines[2].Error, "backend stopped") {
		t.Fatalf("failure log %+v", lines[2])
	}
}

func TestInferStaticTokenRuns(t *testing.T) {
	t.Parallel()
	runner := &spyRunner{reply: threesReply()}
	checker := mustChecker(t, config.Config{AuthMode: config.AuthStatic, AuthStaticToken: staticToken}, "")
	srv := New(checker, func() bool { return true })
	srv.RegisterInfer(InferConfig{
		Catalog:         directoryCatalog{directories: []batch.Directory{addDirectory()}},
		Runner:          runner,
		MaxPayloadBytes: 1 << 20,
	})
	client := dialServer(t, srv)
	_, err := client.ModelInfer(callCtx(t), addRequest("", "add-json", false))
	assertStatus(t, err, codes.Unauthenticated, "unauthenticated", staticToken)
	if runner.calls.Load() != 0 {
		t.Fatal("unauthenticated infer ran")
	}
	resp, err := client.ModelInfer(withAuth(callCtx(t), "Bearer "+staticToken), addRequest("", "add-json", false))
	if err != nil {
		t.Fatal(err)
	}
	assertThrees(t, resp, "add-json")
}

type spyRunner struct {
	calls atomic.Int32
	reply *batch.Reply
	err   error
}

func (s *spyRunner) Run(context.Context, string, string, *batch.Request) (*batch.Reply, error) {
	s.calls.Add(1)
	if s.err != nil {
		return nil, s.err
	}
	return s.reply, nil
}

type batchRunner struct {
	batcher *batch.Batcher
}

func (b batchRunner) Run(ctx context.Context, _, _ string, req *batch.Request) (*batch.Reply, error) {
	return b.batcher.Infer(ctx, req)
}

type memPool struct{}

func (memPool) Get(n int) ([]byte, error) { return make([]byte, n), nil }

func (memPool) Put([]byte) {}

type holdSlot struct {
	entered     chan struct{}
	done        chan struct{}
	enterOnce   sync.Once
	releaseOnce sync.Once
}

func newHoldSlot() *holdSlot {
	return &holdSlot{entered: make(chan struct{}), done: make(chan struct{})}
}

func (h *holdSlot) run(context.Context, []batch.Tensor) ([]batch.Tensor, func(), error) {
	h.enterOnce.Do(func() { close(h.entered) })
	<-h.done
	return []batch.Tensor{{Name: "y", Shape: []int64{1}, Data: float32LE(1)}}, nil, nil
}

func (h *holdSlot) release() {
	h.releaseOnce.Do(func() { close(h.done) })
}

func newBatcher(t *testing.T, slot *holdSlot, capacity int, clock batch.Clock) *batch.Batcher {
	t.Helper()
	b, err := batch.New(batch.Config{
		Key:          batch.Key{Model: "box", Version: "1"},
		MaxBatchSize: 0,
		Capacity:     capacity,
		Pool:         memPool{},
		Clock:        clock,
		Slot:         slot.run,
	})
	if err != nil {
		t.Fatalf("batcher: %v", err)
	}
	return b
}

type manualClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*manualTimer
	armed  chan struct{}
}

type manualTimer struct {
	clock *manualClock
	when  time.Time
	c     chan time.Time
}

func newManualClock() *manualClock {
	return &manualClock{now: time.Unix(0, 0), armed: make(chan struct{}, 4)}
}

func (c *manualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *manualClock) NewTimer(d time.Duration) batch.Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	timer := &manualTimer{clock: c, when: c.now.Add(d), c: make(chan time.Time, 1)}
	if d <= 0 {
		timer.c <- c.now
		return timer
	}
	c.timers = append(c.timers, timer)
	select {
	case c.armed <- struct{}{}:
	default:
	}
	return timer
}

func (c *manualClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	kept := c.timers[:0]
	for _, timer := range c.timers {
		if timer.when.After(c.now) {
			kept = append(kept, timer)
			continue
		}
		timer.c <- c.now
	}
	c.timers = kept
}

func (t *manualTimer) C() <-chan time.Time { return t.c }

func (t *manualTimer) Stop() bool {
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

func startInfer(t *testing.T, directories []batch.Directory, runner Runner, ceiling int, logger *slog.Logger) openinference.GRPCInferenceServiceClient {
	t.Helper()
	srv := New(disabledChecker(t), func() bool { return false })
	catalog := directoryCatalog{directories: directories}
	srv.RegisterReads(catalog)
	srv.RegisterInfer(InferConfig{
		Catalog:         catalog,
		Runner:          runner,
		MaxPayloadBytes: ceiling,
		Logger:          logger,
	})
	return dialServer(t, srv)
}

func addDirectory() batch.Directory {
	return batch.Directory{
		Name:  "add",
		State: batch.StateReady,
		Versions: []batch.ModelVersion{{
			Version: "1",
			Signature: batch.Signature{
				Inputs: []batch.TensorSpec{
					{Name: "A", Datatype: batch.TypeFP32, Shape: []int64{2, 3}},
					{Name: "B", Datatype: batch.TypeFP32, Shape: []int64{2, 3}},
				},
				Outputs: []batch.TensorSpec{
					{Name: "C", Datatype: batch.TypeFP32, Shape: []int64{2, 3}},
				},
			},
		}},
	}
}

func halfDirectory() batch.Directory {
	return batch.Directory{
		Name:  "half",
		State: batch.StateReady,
		Versions: []batch.ModelVersion{{
			Version: "1",
			Signature: batch.Signature{
				Inputs:  []batch.TensorSpec{{Name: "h", Datatype: batch.TypeFP16, Shape: []int64{2}}},
				Outputs: []batch.TensorSpec{{Name: "h", Datatype: batch.TypeFP16, Shape: []int64{2}}},
			},
		}},
	}
}

func wideDirectory() batch.Directory {
	return batch.Directory{
		Name:  "wide",
		State: batch.StateReady,
		Versions: []batch.ModelVersion{{
			Version: "1",
			Signature: batch.Signature{
				Inputs:  []batch.TensorSpec{{Name: "x", Datatype: batch.TypeFP32, Shape: []int64{-1}}},
				Outputs: []batch.TensorSpec{{Name: "y", Datatype: batch.TypeFP32, Shape: []int64{-1}}},
			},
		}},
	}
}

func boxDirectory() batch.Directory {
	return batch.Directory{
		Name:  "box",
		State: batch.StateReady,
		Versions: []batch.ModelVersion{{
			Version: "1",
			Signature: batch.Signature{
				Inputs:  []batch.TensorSpec{{Name: "x", Datatype: batch.TypeFP32, Shape: []int64{1}}},
				Outputs: []batch.TensorSpec{{Name: "y", Datatype: batch.TypeFP32, Shape: []int64{1}}},
			},
		}},
	}
}

func addRequest(version, id string, raw bool) *openinference.ModelInferRequest {
	req := &openinference.ModelInferRequest{
		ModelName:    "add",
		ModelVersion: version,
		Id:           id,
		Inputs: []*openinference.ModelInferRequest_InferInputTensor{
			{Name: "A", Datatype: "FP32", Shape: []int64{2, 3}},
			{Name: "B", Datatype: "FP32", Shape: []int64{2, 3}},
		},
	}
	if raw {
		req.RawInputContents = [][]byte{float32LE(1, 1, 1, 1, 1, 1), float32LE(2, 2, 2, 2, 2, 2)}
		return req
	}
	req.Inputs[0].Contents = &openinference.InferTensorContents{Fp32Contents: []float32{1, 1, 1, 1, 1, 1}}
	req.Inputs[1].Contents = &openinference.InferTensorContents{Fp32Contents: []float32{2, 2, 2, 2, 2, 2}}
	return req
}

func boxRequest(timeout int64) *openinference.ModelInferRequest {
	req := &openinference.ModelInferRequest{
		ModelName: "box",
		Inputs: []*openinference.ModelInferRequest_InferInputTensor{{
			Name:     "x",
			Datatype: "FP32",
			Shape:    []int64{1},
			Contents: &openinference.InferTensorContents{Fp32Contents: []float32{1}},
		}},
	}
	if timeout != 0 {
		req.Parameters = map[string]*openinference.InferParameter{
			"timeout": {ParameterChoice: &openinference.InferParameter_Int64Param{Int64Param: timeout}},
		}
	}
	return req
}

func threesReply() *batch.Reply {
	return &batch.Reply{Outputs: []batch.Tensor{{
		Name:  "C",
		Shape: []int64{2, 3},
		Data:  float32LE(3, 3, 3, 3, 3, 3),
	}}}
}

func float32LE(vals ...float32) []byte {
	buf := make([]byte, 4*len(vals))
	for i, value := range vals {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(value))
	}
	return buf
}

func inferAdd(t *testing.T, client openinference.GRPCInferenceServiceClient, version, id string, raw bool) *openinference.ModelInferResponse {
	t.Helper()
	resp, err := client.ModelInfer(callCtx(t), addRequest(version, id, raw))
	if err != nil {
		t.Fatalf("version %q raw %v: %v", version, raw, err)
	}
	return resp
}

func assertThrees(t *testing.T, resp *openinference.ModelInferResponse, id string) {
	t.Helper()
	if resp.GetModelName() != "add" || resp.GetModelVersion() != "1" || resp.GetId() != id {
		t.Fatalf("response name %q version %q id %q", resp.GetModelName(), resp.GetModelVersion(), resp.GetId())
	}
	if len(resp.GetOutputs()) != 1 || resp.GetOutputs()[0].GetName() != "C" || resp.GetOutputs()[0].GetDatatype() != "FP32" {
		t.Fatalf("outputs %+v", resp.GetOutputs())
	}
	if !slices.Equal(resp.GetOutputs()[0].GetShape(), []int64{2, 3}) {
		t.Fatalf("shape %v", resp.GetOutputs()[0].GetShape())
	}
	got := resp.GetOutputs()[0].GetContents().GetFp32Contents()
	if len(got) != 6 {
		t.Fatalf("values %v", got)
	}
	for i, value := range got {
		if value != 3 {
			t.Fatalf("output[%d]=%v", i, value)
		}
	}
}

type inferResult struct {
	resp *openinference.ModelInferResponse
	err  error
}

func inferAsync(client openinference.GRPCInferenceServiceClient, req *openinference.ModelInferRequest) <-chan inferResult {
	ch := make(chan inferResult, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		resp, err := client.ModelInfer(ctx, req)
		ch <- inferResult{resp: resp, err: err}
	}()
	return ch
}

func waitEntered(t *testing.T, slot *holdSlot) {
	t.Helper()
	select {
	case <-slot.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("request did not reach the slot")
	}
}

func waitArmed(t *testing.T, clock *manualClock) {
	t.Helper()
	select {
	case <-clock.armed:
	case <-time.After(5 * time.Second):
		t.Fatal("queue timer was not started")
	}
}

func waitCall(t *testing.T, ch <-chan inferResult) error {
	t.Helper()
	select {
	case result := <-ch:
		return result.err
	case <-time.After(5 * time.Second):
		t.Fatal("request did not return")
		return nil
	}
}

func firstErr(t *testing.T, a, b <-chan inferResult) (<-chan inferResult, error) {
	t.Helper()
	select {
	case result := <-a:
		return b, result.err
	case result := <-b:
		return a, result.err
	case <-time.After(5 * time.Second):
		t.Fatal("request did not return")
		return nil, nil
	}
}

type inferLog struct {
	Level    string `json:"level"`
	Msg      string `json:"msg"`
	Status   int    `json:"status"`
	Duration string `json:"duration"`
	Error    string `json:"error"`
}

func decodeInferLog(t *testing.T, out string) []inferLog {
	t.Helper()
	var lines []inferLog
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var rec inferLog
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("log %q: %v", line, err)
		}
		lines = append(lines, rec)
	}
	return lines
}
