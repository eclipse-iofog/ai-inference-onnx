package httpapi

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/eclipse-iofog/ai-inference-onnx/internal/batch"
	"github.com/eclipse-iofog/ai-inference-onnx/internal/config"
)

func TestInferJSONAndBinary(t *testing.T) {
	t.Parallel()
	runner := &spyRunner{reply: threesReply()}
	srv := inferServer(t, []batch.Directory{addDirectory()}, runner, 1<<20, nil)
	for _, path := range []string{"/v2/models/add/infer", "/v2/models/add/versions/1/infer"} {
		rec := postInfer(t, srv, path, []byte(addJSON("add-json")), nil)
		assertThreesJSON(t, path, rec, "add-json")
		body := addBinary("add-bin")
		rec = postInfer(t, srv, path, body, map[string]string{
			"Inference-Header-Content-Length": strconv.Itoa(len(addBinaryHeader("add-bin"))),
		})
		assertThreesBinary(t, path, rec, "add-bin")
	}
	if runner.calls.Load() != 4 {
		t.Fatalf("runs %d", runner.calls.Load())
	}
}

func TestInferRejections(t *testing.T) {
	t.Parallel()
	runner := &spyRunner{reply: threesReply()}
	srv := inferServer(t, []batch.Directory{addDirectory()}, runner, 1<<20, nil)

	for _, path := range []string{
		"/v2/models/add/versions/2/infer",
		"/v2/models/add/versions/latest/infer",
		"/v2/models/missing/infer",
	} {
		rec := postInfer(t, srv, path, []byte(addJSON("x")), nil)
		assertError(t, rec, http.StatusNotFound)
	}
	bad := postInfer(t, srv, "/v2/models/add/infer", []byte(`{"inputs":[{"name":"A","datatype":"INT32","shape":[2,3],"data":[1,1,1,1,1,1]},{"name":"B","datatype":"FP32","shape":[2,3],"data":[1,1,1,1,1,1]}]}`), nil)
	assertError(t, bad, http.StatusBadRequest)
	raw := postInfer(t, srv, "/v2/models/add/infer", float32LE(1, 2, 3), map[string]string{
		"Inference-Header-Content-Length": "0",
	})
	assertError(t, raw, http.StatusBadRequest)
	limited := inferServer(t, []batch.Directory{addDirectory()}, runner, 32, nil)
	large := postInfer(t, limited, "/v2/models/add/infer", bytes.Repeat([]byte("a"), 64), nil)
	assertErrorText(t, large, http.StatusBadRequest, payloadTooLarge)
	method := call(t, srv, http.MethodGet, "/v2/models/add/infer", "")
	assertError(t, method, http.StatusMethodNotAllowed)
	if method.Header().Get("Allow") != http.MethodPost {
		t.Fatalf("Allow %q", method.Header().Get("Allow"))
	}
	if runner.calls.Load() != 0 {
		t.Fatalf("rejected requests ran %d times", runner.calls.Load())
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
	srv := inferServer(t, []batch.Directory{
		boxDirectory(),
		{Name: "zeta", State: batch.StateLoading, Reason: loadingReason, Versions: []batch.ModelVersion{{Version: "1"}}},
		{Name: "alpha", State: batch.StateUnavailable, Reason: unavailableReason, Versions: []batch.ModelVersion{{Version: "1"}}},
	}, batchRunner{batcher: newBatcher(t, slot, 1, nil)}, 1<<20, nil)

	holder := postInferAsync(srv, "/v2/models/box/infer", []byte(boxJSON(0)))
	waitEntered(t, slot)
	queued := postInferAsync(srv, "/v2/models/box/infer", []byte(boxJSON(0)))
	extra := postInferAsync(srv, "/v2/models/box/infer", []byte(boxJSON(0)))
	full, other := firstOf(t, queued, extra)
	assertErrorText(t, full, http.StatusServiceUnavailable, batch.Status(batch.ErrQueueFull).Message)
	if full.Body.String() == `{"error":"`+loadingReason+`"}` {
		t.Fatalf("queue body %s", full.Body.String())
	}

	loading := postInfer(t, srv, "/v2/models/zeta/infer", []byte(boxJSON(0)), nil)
	assertErrorText(t, loading, http.StatusServiceUnavailable, loadingReason)
	unavailable := postInfer(t, srv, "/v2/models/alpha/versions/1/infer", []byte(boxJSON(0)), nil)
	assertErrorText(t, unavailable, http.StatusBadRequest, unavailableReason)

	slot.release()
	waitRec(t, holder)
	waitRec(t, other)
}

func TestInferQueueTimeout(t *testing.T) {
	t.Parallel()
	slot := newHoldSlot()
	t.Cleanup(slot.release)
	clock := newManualClock()
	srv := inferServer(t, []batch.Directory{boxDirectory()}, batchRunner{batcher: newBatcher(t, slot, 4, clock)}, 1<<20, nil)

	holder := postInferAsync(srv, "/v2/models/box/infer", []byte(boxJSON(0)))
	waitEntered(t, slot)
	timed := postInferAsync(srv, "/v2/models/box/infer", []byte(boxJSON(1000)))
	waitArmed(t, clock)
	clock.Advance(time.Millisecond)
	rec := waitRec(t, timed)
	assertErrorText(t, rec, http.StatusGatewayTimeout, batch.Status(batch.ErrQueueTimeout).Message)
	slot.release()
	waitRec(t, holder)
}

func TestInferCancel(t *testing.T) {
	t.Parallel()
	slot := newHoldSlot()
	t.Cleanup(slot.release)
	clock := newManualClock()
	srv := inferServer(t, []batch.Directory{boxDirectory()}, batchRunner{batcher: newBatcher(t, slot, 4, clock)}, 1<<20, nil)

	holder := postInferAsync(srv, "/v2/models/box/infer", []byte(boxJSON(0)))
	waitEntered(t, slot)
	ctx, cancel := context.WithCancel(context.Background())
	waiting := postInferContext(ctx, srv, "/v2/models/box/infer", []byte(boxJSON(60_000_000)))
	waitArmed(t, clock)
	cancel()
	rec := waitRec(t, waiting)
	assertErrorText(t, rec, http.StatusBadRequest, batch.Status(context.Canceled).Message)
	slot.release()
	waitRec(t, holder)
}

func TestInferLogs(t *testing.T) {
	t.Parallel()
	const token = "Bearer planted-token-9f3c2ab1"
	const planted = "424242"
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	runner := &spyRunner{reply: threesReply()}
	srv := inferServer(t, []batch.Directory{addDirectory()}, runner, 1<<20, logger)

	body := strings.Replace(addJSON("add-json"), "1,1,1,1,1,1", planted+",1,1,1,1,1", 1)
	rec := postInfer(t, srv, "/v2/models/add/infer", []byte(body), map[string]string{"Authorization": token})
	if rec.Code != http.StatusOK {
		t.Fatalf("success %d %s", rec.Code, rec.Body.String())
	}
	bad := postInfer(t, srv, "/v2/models/add/infer", []byte(`{"inputs":[]}`), map[string]string{"Authorization": token})
	assertError(t, bad, http.StatusBadRequest)
	runner.err = errors.New("backend stopped")
	failed := postInfer(t, srv, "/v2/models/add/infer", []byte(addJSON("add-json")), map[string]string{"Authorization": token})
	assertError(t, failed, http.StatusInternalServerError)

	text := buf.String()
	if strings.Contains(text, token) || strings.Contains(text, "planted-token") || strings.Contains(text, planted) {
		t.Fatalf("log contains a credential or a tensor value:\n%s", text)
	}
	lines := decodeInferLog(t, text)
	if len(lines) != 3 {
		t.Fatalf("lines %d:\n%s", len(lines), text)
	}
	if !strings.EqualFold(lines[0].Level, "debug") || lines[0].Msg != "infer" || lines[0].Status != http.StatusOK || lines[0].DurationUS == nil || lines[0].Transport != "http" || lines[0].ID != "add-json" {
		t.Fatalf("success log %+v", lines[0])
	}
	if !strings.EqualFold(lines[1].Level, "warn") || lines[1].Msg != "infer" || lines[1].ErrorKind != "invalid" {
		t.Fatalf("reject log %+v", lines[1])
	}
	if !strings.EqualFold(lines[2].Level, "error") || lines[2].Msg != "infer" || lines[2].ErrorKind != "runtime" || !strings.Contains(lines[2].Error, "backend stopped") {
		t.Fatalf("failure log %+v", lines[2])
	}
}

func TestInferAuthAndHealth(t *testing.T) {
	t.Parallel()
	runner := &spyRunner{reply: threesReply()}
	checker := mustChecker(t, config.Config{AuthMode: config.AuthStatic, AuthStaticToken: staticToken}, "")
	srv := New(checker, func() bool { return true })
	srv.RegisterInfer(InferConfig{Runner: runner, MaxPayloadBytes: 1 << 20})
	assertEmpty(t, call(t, srv, http.MethodGet, "/v2/health/live", ""), http.StatusOK)
	assertEmpty(t, call(t, srv, http.MethodGet, "/v2/health/ready", ""), http.StatusOK)
	assertError(t, call(t, srv, http.MethodPost, "/v2/models/add/infer", ""), http.StatusUnauthorized, staticToken)
	if runner.calls.Load() != 0 {
		t.Fatal("unauthenticated infer ran")
	}

	open := inferServer(t, nil, runner, 1<<20, nil)
	assertEmpty(t, call(t, open, http.MethodGet, "/v2/health/live", ""), http.StatusOK)
	missing := postInfer(t, open, "/v2/models/add/infer", []byte(`{}`), nil)
	assertErrorText(t, missing, http.StatusNotFound, `model "add" was not found`)
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
	t := &manualTimer{clock: c, when: c.now.Add(d), c: make(chan time.Time, 1)}
	if d <= 0 {
		t.c <- c.now
		return t
	}
	c.timers = append(c.timers, t)
	select {
	case c.armed <- struct{}{}:
	default:
	}
	return t
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

func inferServer(t *testing.T, directories []batch.Directory, runner Runner, ceiling int, logger *slog.Logger) *Server {
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
	return srv
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

func boxJSON(timeout int) string {
	if timeout == 0 {
		return `{"inputs":[{"name":"x","datatype":"FP32","shape":[1],"data":[1]}]}`
	}
	return `{"parameters":{"timeout":` + strconv.Itoa(timeout) + `},"inputs":[{"name":"x","datatype":"FP32","shape":[1],"data":[1]}]}`
}

func addJSON(id string) string {
	return `{"id":"` + id + `","inputs":[` +
		`{"name":"A","datatype":"FP32","shape":[2,3],"data":[1,1,1,1,1,1]},` +
		`{"name":"B","datatype":"FP32","shape":[2,3],"data":[2,2,2,2,2,2]}]}`
}

func addBinaryHeader(id string) string {
	return `{"id":"` + id + `","parameters":{"binary_data_output":true},"inputs":[` +
		`{"name":"A","datatype":"FP32","shape":[2,3],"parameters":{"binary_data_size":24}},` +
		`{"name":"B","datatype":"FP32","shape":[2,3],"parameters":{"binary_data_size":24}}]}`
}

func addBinary(id string) []byte {
	body := append([]byte(addBinaryHeader(id)), float32LE(1, 1, 1, 1, 1, 1)...)
	return append(body, float32LE(2, 2, 2, 2, 2, 2)...)
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

func postInfer(t *testing.T, handler http.Handler, path string, body []byte, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func postInferAsync(handler http.Handler, path string, body []byte) <-chan *httptest.ResponseRecorder {
	ch := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		ch <- rec
	}()
	return ch
}

func postInferContext(ctx context.Context, handler http.Handler, path string, body []byte) <-chan *httptest.ResponseRecorder {
	ch := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequestWithContext(ctx, http.MethodPost, path, bytes.NewReader(body))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		ch <- rec
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

func waitRec(t *testing.T, ch <-chan *httptest.ResponseRecorder) *httptest.ResponseRecorder {
	t.Helper()
	select {
	case rec := <-ch:
		return rec
	case <-time.After(5 * time.Second):
		t.Fatal("request did not return")
		return nil
	}
}

func firstOf(t *testing.T, a, b <-chan *httptest.ResponseRecorder) (*httptest.ResponseRecorder, <-chan *httptest.ResponseRecorder) {
	t.Helper()
	select {
	case rec := <-a:
		return rec, b
	case rec := <-b:
		return rec, a
	case <-time.After(5 * time.Second):
		t.Fatal("request did not return")
		return nil, nil
	}
}

func assertThreesJSON(t *testing.T, path string, rec *httptest.ResponseRecorder, id string) {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("%s status %d body %s", path, rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("%s Content-Type %q", path, rec.Header().Get("Content-Type"))
	}
	if rec.Header().Get("Inference-Header-Content-Length") != "" {
		t.Fatalf("%s binary header %q", path, rec.Header().Get("Inference-Header-Content-Length"))
	}
	var doc jsonInferResponse
	decodeJSON(t, rec.Body.Bytes(), &doc)
	if doc.ModelName != "add" || doc.ModelVersion != "1" || doc.ID != id {
		t.Fatalf("%s response %+v", path, doc)
	}
	if len(doc.Outputs) != 1 || doc.Outputs[0].Name != "C" || len(doc.Outputs[0].Data) != 6 {
		t.Fatalf("%s outputs %+v", path, doc.Outputs)
	}
	for i, value := range doc.Outputs[0].Data {
		if value != 3 {
			t.Fatalf("%s output[%d]=%v", path, i, value)
		}
	}
}

func assertThreesBinary(t *testing.T, path string, rec *httptest.ResponseRecorder, id string) {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("%s status %d body %s", path, rec.Code, rec.Body.Bytes())
	}
	if rec.Header().Get("Content-Type") != "application/octet-stream" {
		t.Fatalf("%s Content-Type %q", path, rec.Header().Get("Content-Type"))
	}
	n, err := strconv.Atoi(rec.Header().Get("Inference-Header-Content-Length"))
	if err != nil || n <= 0 || n >= rec.Body.Len() {
		t.Fatalf("%s header %q", path, rec.Header().Get("Inference-Header-Content-Length"))
	}
	body := rec.Body.Bytes()
	var doc binaryInferHeader
	decodeJSON(t, body[:n], &doc)
	if doc.ModelName != "add" || doc.ModelVersion != "1" || doc.ID != id || len(doc.Outputs) != 1 || doc.Outputs[0].Name != "C" {
		t.Fatalf("%s header %+v", path, doc)
	}
	suffix := body[n:]
	if len(suffix) != 24 {
		t.Fatalf("%s suffix %d", path, len(suffix))
	}
	for i := 0; i < 6; i++ {
		if math.Float32frombits(binary.LittleEndian.Uint32(suffix[i*4:])) != 3 {
			t.Fatalf("%s suffix[%d]", path, i)
		}
	}
}

type jsonInferOutput struct {
	Name string    `json:"name"`
	Data []float64 `json:"data"`
}

type jsonInferResponse struct {
	ModelName    string            `json:"model_name"`
	ModelVersion string            `json:"model_version"`
	ID           string            `json:"id"`
	Outputs      []jsonInferOutput `json:"outputs"`
}

type binaryInferOutput struct {
	Name string `json:"name"`
}

type binaryInferHeader struct {
	ModelName    string              `json:"model_name"`
	ModelVersion string              `json:"model_version"`
	ID           string              `json:"id"`
	Outputs      []binaryInferOutput `json:"outputs"`
}

type inferLog struct {
	Level      string `json:"level"`
	Msg        string `json:"msg"`
	Status     int    `json:"status"`
	DurationUS *int64 `json:"duration_us"`
	Error      string `json:"error"`
	ErrorKind  string `json:"error_kind"`
	ID         string `json:"id"`
	Transport  string `json:"transport"`
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
