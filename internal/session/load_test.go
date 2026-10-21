//go:build cgo

package session

import (
	"context"
	"encoding/binary"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"github.com/eclipse-iofog/ai-inference-onnx/internal/ort"
)

func TestSessions(t *testing.T) {
	if os.Getenv("ORT_LIB_PATH") == "" {
		t.Fatal("ORT_LIB_PATH is unset")
	}
	if err := ort.Init(); err != nil {
		t.Fatal(err)
	}
	version, err := ort.GetVersion()
	if err != nil {
		t.Fatal(err)
	}
	if version != "1.31.0" {
		t.Fatalf("onnx runtime version %q", version)
	}

	t.Run("committed", testCommitted)
	t.Run("batch", testBatch)
	t.Run("inner dynamic", testInnerDynamic)
	t.Run("knowledge", testKnowledge)
	t.Run("garbage", testGarbage)
	t.Run("vocab", testVocab)
	t.Run("filename", testFilename)
	t.Run("failed version", testFailedVersion)
	t.Run("provider", testProvider)
	t.Run("cuda provider", testCUDAProvider)
	t.Run("slot", testSlot)
	t.Run("shutdown", testShutdown)
}

func testCommitted(t *testing.T) {
	var seen Admission
	whileLoading = func(repo *Repository) {
		seen = repo.Admit("add", "")
	}
	t.Cleanup(func() { whileLoading = nil })

	repo := loadRoots(t, committedRoot(t), t.TempDir(), "")
	if seen.Status != StatusLoading || !strings.Contains(seen.Reason, "loading") {
		t.Fatalf("loading admission %+v", seen)
	}
	model := findModel(t, repo, "add")
	if model.State != Ready || model.Reason != "" {
		t.Fatalf("state %s reason %q", model.State, model.Reason)
	}
	if model.QueueDelayMicroseconds != 0 {
		t.Fatalf("queue delay %d", model.QueueDelayMicroseconds)
	}
	if repo.sessionsOpen() != 1 {
		t.Fatalf("sessions %d", repo.sessionsOpen())
	}
	ver := oneVersion(t, model)
	if ver.Version != "1" || ver.MaxBatchSize != 0 || ver.Batchable || !ver.Warmed {
		t.Fatalf("version %+v", ver)
	}

	for _, version := range []string{"", "1"} {
		got := repo.Admit("add", version)
		if !got.Proceed() || got.Version != "1" || got.Status != 0 {
			t.Fatalf("version %q admission %+v", version, got)
		}
	}
	for _, version := range []string{"2", "latest", "v1", "01"} {
		got := repo.Admit("add", version)
		if got.Status != StatusMissing || got.Proceed() {
			t.Fatalf("version %q admission %+v", version, got)
		}
	}
	if got := repo.Admit("missing", ""); got.Status != StatusMissing || got.Proceed() {
		t.Fatalf("unknown model %+v", got)
	}

	pool, err := NewPool(DefaultCeiling)
	if err != nil {
		t.Fatal(err)
	}
	aBuf, err := pool.Get(24, nil)
	if err != nil {
		t.Fatal(err)
	}
	ones := float32Bytes(1, 1, 1, 1, 1, 1)
	twos := float32Bytes(2, 2, 2, 2, 2, 2)
	copy(aBuf.Data, ones)
	bData := make([]byte, len(twos), 128)
	copy(bData, twos)
	beforeRuntime = func(backing []tensorMemory) {
		if !sameBytes(memoryNamed(backing, "A"), aBuf.Data) || !sameBytes(memoryNamed(backing, "B"), bData) {
			t.Error("input bytes were copied")
		}
	}
	t.Cleanup(func() { beforeRuntime = nil })

	out := runAdd(t, repo, "", aBuf.Data, bData)
	if !out.Buffer.Pooled || cap(out.Buffer.Data) <= len(out.Buffer.Data) {
		t.Fatalf("output pooled=%v len=%d cap=%d", out.Buffer.Pooled, len(out.Buffer.Data), cap(out.Buffer.Data))
	}
	if len(out.Shape) != 2 || out.Shape[0] != 2 || out.Shape[1] != 3 {
		t.Fatalf("shape %v", out.Shape)
	}
	assertThrees(t, out.Buffer.Data)
	for i := range aBuf.Data {
		aBuf.Data[i] = 0
	}
	for i := range bData {
		bData[i] = 0
	}
	assertThrees(t, out.Buffer.Data)
	repo.Release(out)
}

func testBatch(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, "add", "model.onnx"), batchLeading)
		repo := loadRoots(t, root, t.TempDir(), "")
		ver := readyVersion(t, repo)
		if !ver.Batchable || ver.MaxBatchSize != 1 || !ver.Warmed {
			t.Fatalf("%+v", ver)
		}
		if findModel(t, repo, "add").QueueDelayMicroseconds != 0 {
			t.Fatalf("queue delay %d", findModel(t, repo, "add").QueueDelayMicroseconds)
		}
	})

	t.Run("override 4", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, "add", "model.onnx"), batchLeading)
		writeFile(t, filepath.Join(root, "add", "config.pbtxt"), []byte("max_batch_size: 4\ndynamic_batching { max_queue_delay_microseconds: 2500 }\n"))
		repo := loadRoots(t, root, t.TempDir(), "")
		ver := readyVersion(t, repo)
		model := findModel(t, repo, "add")
		if !ver.Batchable || ver.MaxBatchSize != 4 || !ver.Warmed {
			t.Fatalf("%+v", ver)
		}
		if model.QueueDelayMicroseconds != 2500 {
			t.Fatalf("queue delay %d", model.QueueDelayMicroseconds)
		}
	})

	t.Run("override 0", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, "add", "model.onnx"), batchLeading)
		writeFile(t, filepath.Join(root, "add", "config.pbtxt"), []byte("max_batch_size: 0\n"))
		repo := loadRoots(t, root, t.TempDir(), "")
		ver := readyVersion(t, repo)
		if !ver.Batchable || ver.MaxBatchSize != 0 || !ver.Warmed {
			t.Fatalf("%+v", ver)
		}
		if findModel(t, repo, "add").QueueDelayMicroseconds != 0 {
			t.Fatalf("queue delay %d", findModel(t, repo, "add").QueueDelayMicroseconds)
		}
	})
}

func testInnerDynamic(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "add", "model.onnx"), batchInner)
	repo := loadRoots(t, root, t.TempDir(), "")
	model := findModel(t, repo, "add")
	if model.State != Ready || model.Reason != "" {
		t.Fatalf("state %s reason %q", model.State, model.Reason)
	}
	ver := oneVersion(t, model)
	if ver.Warmed || ver.Batchable || ver.MaxBatchSize != 0 {
		t.Fatalf("%+v", ver)
	}
	if repo.sessionsOpen() != 1 {
		t.Fatalf("sessions %d", repo.sessionsOpen())
	}
}

func testKnowledge(t *testing.T) {
	root := t.TempDir()
	knowledge := t.TempDir()
	writeFile(t, filepath.Join(root, "add", "model.onnx"), committedModel(t))
	writeFile(t, filepath.Join(root, "add", "knowledge"), []byte("corpus\n"))

	missing := loadRoots(t, root, knowledge, "")
	assertUnavailable(t, missing, "add", "corpus")

	if err := os.Mkdir(filepath.Join(knowledge, "corpus"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(knowledge, "corpus", "notes.txt"), []byte("labels"))
	ready := loadRoots(t, root, knowledge, "")
	ver := readyVersion(t, ready)
	if !ver.Warmed || ready.sessionsOpen() != 1 {
		t.Fatalf("version %+v sessions %d", ver, ready.sessionsOpen())
	}
}

func testGarbage(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "add", "model.onnx"), []byte("not an onnx model"))
	repo := loadRoots(t, root, t.TempDir(), "")
	assertUnavailable(t, repo, "add", "")
}

func testVocab(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "add", "model.onnx"), committedModel(t))
	vocab := filepath.Join(root, "add", "vocab.json")
	writeFile(t, vocab, []byte("{"))
	if err := os.Chmod(vocab, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(vocab, 0o644) })

	repo := loadRoots(t, root, t.TempDir(), "")
	ver := readyVersion(t, repo)
	if !ver.Warmed || repo.sessionsOpen() != 1 {
		t.Fatalf("version %+v sessions %d", ver, repo.sessionsOpen())
	}
}

func testFilename(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "add", "weights.onnx"), committedModel(t))
	writeFile(t, filepath.Join(root, "add", "config.pbtxt"), []byte("default_model_filename: \"weights.onnx\"\n"))
	repo := loadRoots(t, root, t.TempDir(), "")
	ver := readyVersion(t, repo)
	if ver.Version != "1" || !ver.Warmed || ver.MaxBatchSize != 0 {
		t.Fatalf("%+v", ver)
	}
}

func testFailedVersion(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "add", "2", "model.onnx"), committedModel(t))
	writeFile(t, filepath.Join(root, "add", "1", "model.onnx"), []byte("not an onnx model"))
	writeFile(t, filepath.Join(root, "add", "config.pbtxt"), []byte("version_policy: { all: {} }\n"))
	repo := loadRoots(t, root, t.TempDir(), "")
	assertUnavailable(t, repo, "add", "")
}

func testProvider(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "add", "model.onnx"), committedModel(t))
	repo := loadRoots(t, root, t.TempDir(), "NoSuchProvider")
	model := findModel(t, repo, "add")
	if model.State != Unavailable || model.Reason == "" {
		t.Fatalf("state %s reason %q", model.State, model.Reason)
	}
	if !strings.Contains(model.Reason, "NoSuchProvider") || strings.Contains(model.Reason, "create session") {
		t.Fatalf("reason %q", model.Reason)
	}
	if len(model.Versions) != 0 || repo.sessionsOpen() != 0 {
		t.Fatalf("versions %d sessions %d", len(model.Versions), repo.sessionsOpen())
	}
	got := repo.Admit("add", "")
	if got.Status == StatusLoading || got.Status == StatusMissing || got.Reason != model.Reason {
		t.Fatalf("admission %+v", got)
	}
}

func testCUDAProvider(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "add", "model.onnx"), committedModel(t))
	repo := loadRoots(t, root, t.TempDir(), ort.CUDAExecutionProvider)
	model := findModel(t, repo, "add")
	if model.State != Unavailable || model.Reason == "" {
		t.Fatalf("state %s reason %q", model.State, model.Reason)
	}
	if !strings.Contains(strings.ToLower(model.Reason), "cuda") || strings.Contains(model.Reason, "create session") {
		t.Fatalf("reason %q", model.Reason)
	}
	if len(model.Versions) != 0 || repo.sessionsOpen() != 0 {
		t.Fatalf("versions %d sessions %d", len(model.Versions), repo.sessionsOpen())
	}
}

func testSlot(t *testing.T) {
	repo := loadRoots(t, committedRoot(t), t.TempDir(), "")
	ones := float32Bytes(1, 1, 1, 1, 1, 1)
	twos := float32Bytes(2, 2, 2, 2, 2, 2)

	t.Run("canceled", func(t *testing.T) {
		called := false
		beforeRuntime = func([]tensorMemory) { called = true }
		t.Cleanup(func() { beforeRuntime = nil })
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := repo.Run(ctx, "add", "1", addInputs(ones, twos))
		if !errors.Is(err, context.Canceled) || err.Error() != context.Canceled.Error() {
			t.Fatalf("error %v", err)
		}
		if called {
			t.Fatal("runtime hook ran for a canceled call")
		}
		out := runAdd(t, repo, "1", ones, twos)
		assertThrees(t, out.Buffer.Data)
		repo.Release(out)
	})

	t.Run("overlap", func(t *testing.T) {
		holdAndRelease(t, repo, ones, twos, false)
	})

	t.Run("canceled while holding", func(t *testing.T) {
		holdAndRelease(t, repo, ones, twos, true)
	})
}

func holdAndRelease(t *testing.T, repo *Repository, ones, twos []byte, cancelFirst bool) {
	t.Helper()
	release := make(chan struct{})
	started := make(chan struct{})
	var once sync.Once
	unlock := func() { once.Do(func() { close(release) }) }
	var entered atomic.Int32
	var wg sync.WaitGroup
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	beforeRuntime = func([]tensorMemory) {
		if cancelFirst {
			cancel()
		}
		if entered.Add(1) == 1 {
			close(started)
			<-release
		}
	}
	defer func() {
		unlock()
		wg.Wait()
		beforeRuntime = nil
	}()

	firstDone := make(chan error, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, err := repo.Run(ctx, "add", "", addInputs(ones, twos))
		firstDone <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("first run did not hold the slot")
	}

	secondDone := make(chan result, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		out, err := repo.Run(context.Background(), "add", "", addInputs(ones, twos))
		secondDone <- result{out: out, err: err}
	}()
	select {
	case <-secondDone:
		t.Fatal("second run overlapped the first")
	case <-time.After(100 * time.Millisecond):
	}
	unlock()

	err := <-firstDone
	if cancelFirst {
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("first run: %v", err)
		}
	} else if err != nil {
		t.Fatal(err)
	}
	res := <-secondDone
	if res.err != nil {
		t.Fatal(res.err)
	}
	if entered.Load() != 2 {
		t.Fatalf("runs %d", entered.Load())
	}
	if len(res.out) != 1 {
		t.Fatalf("outputs %d", len(res.out))
	}
	assertThrees(t, res.out[0].Buffer.Data)
	repo.Release(res.out[0])
}

func testShutdown(t *testing.T) {
	repo := loadRoots(t, committedRoot(t), t.TempDir(), "")
	if err := ort.Shutdown(); err == nil {
		t.Fatal("shutdown succeeded while a session is open")
	}
	version, err := ort.GetVersion()
	if err != nil {
		t.Fatal(err)
	}
	if version != "1.31.0" {
		t.Fatalf("onnx runtime version %q", version)
	}
	ones := float32Bytes(1, 1, 1, 1, 1, 1)
	twos := float32Bytes(2, 2, 2, 2, 2, 2)
	out := runAdd(t, repo, "", ones, twos)
	assertThrees(t, out.Buffer.Data)
	repo.Release(out)
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ort.Shutdown(); err != nil {
		t.Fatal(err)
	}
	if err := ort.Init(); err == nil {
		t.Fatal("Init succeeded after shutdown")
	}
}

type result struct {
	out []Output
	err error
}

func loadRoots(t *testing.T, models, knowledge, provider string) *Repository {
	t.Helper()
	if provider == "" {
		provider = ort.CPUExecutionProvider
	}
	pool, err := NewPool(DefaultCeiling)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := Load(Config{
		ModelsRoot:    models,
		KnowledgeRoot: knowledge,
		IntraOp:       1,
		InterOp:       1,
		Providers:     []ort.Provider{{Name: provider}},
	}, pool)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := repo.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})
	return repo
}

func committedRoot(t *testing.T) string {
	t.Helper()
	root := filepath.Join("..", "..", "testdata", "models")
	if _, err := os.Stat(filepath.Join(root, "add", "model.onnx")); err != nil {
		t.Fatal(err)
	}
	return root
}

func committedModel(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(committedRoot(t), "add", "model.onnx"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func findModel(t *testing.T, repo *Repository, name string) Model {
	t.Helper()
	for _, model := range repo.Models() {
		if model.Name == name {
			return model
		}
	}
	t.Fatalf("model %s not loaded", name)
	return Model{}
}

func oneVersion(t *testing.T, model Model) Version {
	t.Helper()
	if len(model.Versions) != 1 {
		t.Fatalf("versions %+v", model.Versions)
	}
	return model.Versions[0]
}

func readyVersion(t *testing.T, repo *Repository) Version {
	t.Helper()
	model := findModel(t, repo, "add")
	if model.State != Ready || model.Reason != "" {
		t.Fatalf("state %s reason %q", model.State, model.Reason)
	}
	return oneVersion(t, model)
}

func assertUnavailable(t *testing.T, repo *Repository, name, reasonPart string) {
	t.Helper()
	model := findModel(t, repo, name)
	if model.State != Unavailable || model.Reason == "" {
		t.Fatalf("state %s reason %q", model.State, model.Reason)
	}
	if reasonPart != "" && !strings.Contains(model.Reason, reasonPart) {
		t.Fatalf("reason %q", model.Reason)
	}
	if len(model.Versions) != 0 || repo.sessionsOpen() != 0 {
		t.Fatalf("versions %d sessions %d", len(model.Versions), repo.sessionsOpen())
	}
	for _, version := range []string{"", "1", "2", "latest", "v1", "01"} {
		got := repo.Admit(name, version)
		if got.Status == StatusLoading || got.Status == StatusMissing || got.Proceed() || got.Reason != model.Reason {
			t.Fatalf("version %q admission %+v", version, got)
		}
	}
}

func addInputs(a, b []byte) []Input {
	return []Input{
		{Name: "A", Shape: []int64{2, 3}, Data: a},
		{Name: "B", Shape: []int64{2, 3}, Data: b},
	}
}

func runAdd(t *testing.T, repo *Repository, version string, a, b []byte) Output {
	t.Helper()
	outputs, err := repo.Run(context.Background(), "add", version, addInputs(a, b))
	if err != nil {
		t.Fatal(err)
	}
	if len(outputs) != 1 || outputs[0].Name != "C" {
		t.Fatalf("outputs %+v", outputNames(outputs))
	}
	return outputs[0]
}

func outputNames(outputs []Output) []string {
	names := make([]string, len(outputs))
	for i, out := range outputs {
		names[i] = out.Name
	}
	return names
}

func assertThrees(t *testing.T, data []byte) {
	t.Helper()
	values := float32Values(t, data)
	if len(values) != 6 {
		t.Fatalf("len %d", len(values))
	}
	for i, value := range values {
		if value != 3 {
			t.Fatalf("output[%d]=%v", i, value)
		}
	}
}

func float32Bytes(vals ...float32) []byte {
	buf := make([]byte, 4*len(vals))
	for i, value := range vals {
		binary.NativeEndian.PutUint32(buf[i*4:], math.Float32bits(value))
	}
	return buf
}

func float32Values(t *testing.T, buf []byte) []float32 {
	t.Helper()
	if len(buf)%4 != 0 {
		t.Fatalf("len %d", len(buf))
	}
	out := make([]float32, len(buf)/4)
	for i := range out {
		out[i] = math.Float32frombits(binary.NativeEndian.Uint32(buf[i*4:]))
	}
	return out
}

func sameBytes(a, b []byte) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	return unsafe.Pointer(&a[0]) == unsafe.Pointer(&b[0])
}

func memoryNamed(backing []tensorMemory, name string) []byte {
	for _, item := range backing {
		if item.name == name {
			return item.data
		}
	}
	return nil
}
