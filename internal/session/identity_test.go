//go:build cgo

package session

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/eclipse-iofog/ai-inference-onnx/internal/ort"
)

func TestReload(t *testing.T) {
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

	t.Run("unchanged", testUnchangedScan)
	t.Run("held", testReloadHeld)
	t.Run("failed", testReloadFailed)
	t.Run("burst", testReloadBurst)
	t.Run("notify", testReloadNotify)
	t.Run("poll", testReloadPoll)
	t.Run("writes", testReloadWrites)
	t.Run("data", testReloadData)
	t.Run("knowledge", testReloadKnowledge)
	t.Run("add remove", testReloadAddRemove)
}

func testUnchangedScan(t *testing.T) {
	repo, cfg, onnx := loadAdd(t, "")
	if repo.epoch("add") != 1 || repo.opens("add") != 1 || repo.openHigh("add", "1") != 1 {
		t.Fatalf("epoch %d opens %d high %d", repo.epoch("add"), repo.opens("add"), repo.openHigh("add", "1"))
	}
	w := startWatch(t, repo, cfg, time.Hour, time.Hour)
	if err := w.Scan(); err != nil {
		t.Fatal(err)
	}
	if repo.epoch("add") != 1 || repo.opens("add") != 1 {
		t.Fatalf("epoch %d opens %d", repo.epoch("add"), repo.opens("add"))
	}

	id := statFile(onnx)
	data, err := os.ReadFile(onnx)
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)-1] ^= 0xff
	if err := os.WriteFile(onnx, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(onnx, id.modTime, id.modTime); err != nil {
		t.Fatal(err)
	}
	if got := statFile(onnx); !got.same(id) {
		t.Fatalf("stat inode %d size %d time %s", got.inode, got.size, got.modTime)
	}
	if err := w.Scan(); err != nil {
		t.Fatal(err)
	}
	if repo.epoch("add") != 1 || repo.opens("add") != 1 || findModel(t, repo, "add").State != Ready {
		t.Fatalf("epoch %d opens %d state %s", repo.epoch("add"), repo.opens("add"), findModel(t, repo, "add").State)
	}
	out := runAdd(t, repo, "", float32Bytes(1, 1, 1, 1, 1, 1), float32Bytes(2, 2, 2, 2, 2, 2))
	assertThrees(t, out.Buffer.Data)
	repo.Release(out)
}

func testReloadHeld(t *testing.T) {
	repo, cfg, onnx := loadAdd(t, "other")
	w := startWatch(t, repo, cfg, time.Hour, time.Hour)
	ones := float32Bytes(1, 1, 1, 1, 1, 1)
	twos := float32Bytes(2, 2, 2, 2, 2, 2)

	release, done := startHeld(t, repo, "add", ones, twos)
	prev := statFile(onnx)
	next := rewriteFile(t, onnx, committedModel(t), prev)

	seen := make(chan struct{})
	setWhileLoading(func(repo *Repository) {
		if repo.openNow("add") != 1 {
			t.Errorf("open sessions %d", repo.openNow("add"))
		}
		close(seen)
	})
	t.Cleanup(func() { setWhileLoading(nil) })

	finished := make(chan error, 1)
	go func() {
		finished <- w.Scan()
	}()
	select {
	case <-seen:
	case <-time.After(5 * time.Second):
		t.Fatal("reload did not mark the model loading")
	}
	if findModel(t, repo, "add").State != Loading {
		t.Fatalf("state %s", findModel(t, repo, "add").State)
	}
	if got := repo.Admit("add", ""); got.Status != StatusLoading || got.Proceed() {
		t.Fatalf("admission %+v", got)
	}
	if got := repo.Admit("other", ""); !got.Proceed() || repo.epoch("other") != 1 {
		t.Fatalf("other admission %+v epoch %d", got, repo.epoch("other"))
	}

	refused := make(chan error, 1)
	go func() {
		_, err := repo.Run(context.Background(), "add", "", addInputs(ones, twos))
		refused <- err
	}()
	select {
	case err := <-refused:
		var status *StatusError
		if !errors.As(err, &status) || status.Status != StatusLoading {
			t.Fatalf("during load: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("call during load did not return")
	}

	release()
	got := <-done
	if got.err != nil {
		t.Fatal(got.err)
	}
	if len(got.out) != 1 {
		t.Fatalf("outputs %d", len(got.out))
	}
	assertThrees(t, got.out[0].Buffer.Data)
	repo.Release(got.out[0])

	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	model := findModel(t, repo, "add")
	if model.State != Ready || model.Reason != "" {
		t.Fatalf("state %s reason %q", model.State, model.Reason)
	}
	if repo.epoch("add") != 2 || repo.epoch("other") != 1 {
		t.Fatalf("epoch add %d other %d", repo.epoch("add"), repo.epoch("other"))
	}
	if repo.openNow("add") != 1 || repo.openHigh("add", "1") != 1 {
		t.Fatalf("open %d high %d", repo.openNow("add"), repo.openHigh("add", "1"))
	}
	onnxID, dataID, ok := repo.fileIdentity("add", "1")
	if !ok || !onnxID.same(next) || dataID.present {
		t.Fatalf("identity %+v data %+v", onnxID, dataID)
	}
	out := runAdd(t, repo, "", ones, twos)
	assertThrees(t, out.Buffer.Data)
	repo.Release(out)
}

func testReloadFailed(t *testing.T) {
	repo, cfg, onnx := loadAdd(t, "other")
	w := startWatch(t, repo, cfg, time.Hour, time.Hour)
	ones := float32Bytes(1, 1, 1, 1, 1, 1)
	twos := float32Bytes(2, 2, 2, 2, 2, 2)
	release, done := startHeld(t, repo, "add", ones, twos)

	if err := os.WriteFile(onnx, []byte("not an onnx model"), 0o644); err != nil {
		t.Fatal(err)
	}
	seen := make(chan struct{})
	setWhileLoading(func(repo *Repository) {
		if got := repo.Admit("add", ""); got.Status != StatusLoading {
			t.Errorf("admission %+v", got)
		}
		if got := repo.Admit("other", ""); !got.Proceed() || repo.epoch("other") != 1 {
			t.Errorf("other admission %+v epoch %d", got, repo.epoch("other"))
		}
		close(seen)
	})
	t.Cleanup(func() { setWhileLoading(nil) })

	finished := make(chan error, 1)
	go func() { finished <- w.Scan() }()
	select {
	case <-seen:
	case <-time.After(5 * time.Second):
		t.Fatal("reload did not mark the model loading")
	}
	release()
	got := <-done
	if got.err != nil {
		t.Fatal(got.err)
	}
	assertThrees(t, got.out[0].Buffer.Data)
	repo.Release(got.out[0])
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	model := findModel(t, repo, "add")
	if model.State != Unavailable || model.Reason == "" || len(model.Versions) != 0 || repo.openNow("add") != 0 {
		t.Fatalf("state %s reason %q versions %d open %d", model.State, model.Reason, len(model.Versions), repo.openNow("add"))
	}
	if got := repo.Admit("add", ""); got.Status == StatusLoading || got.Status == StatusMissing || got.Proceed() || got.Reason != model.Reason {
		t.Fatalf("admission %+v", got)
	}
	if repo.openNow("add") != 0 || repo.openHigh("add", "1") != 1 {
		t.Fatalf("open %d high %d", repo.openNow("add"), repo.openHigh("add", "1"))
	}
	if repo.epoch("other") != 1 || findModel(t, repo, "other").State != Ready {
		t.Fatalf("other epoch %d state %s", repo.epoch("other"), findModel(t, repo, "other").State)
	}
}

func testReloadBurst(t *testing.T) {
	repo, cfg, onnx := loadAdd(t, "")
	const quiet = 30 * time.Millisecond
	w := startWatch(t, repo, cfg, time.Hour, quiet)
	before := repo.opens("add")
	original := statFile(onnx)

	entered := make(chan struct{})
	letGo := make(chan struct{})
	var once sync.Once
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(letGo) }) }
	setWhileLoading(func(*Repository) {
		once.Do(func() {
			close(entered)
			<-letGo
		})
	})
	t.Cleanup(func() {
		release()
		setWhileLoading(nil)
	})

	first := rewriteFile(t, onnx, committedModel(t), original)
	w.Notify("add")
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("reload did not start")
	}
	final := first
	for range 8 {
		final = rewriteFile(t, onnx, committedModel(t), final)
		w.Notify("add")
	}
	release()

	waitIdentity(t, repo, final, 3*time.Second)
	time.Sleep(3 * quiet)
	got := repo.opens("add") - before
	if got == 0 || got > 2 {
		t.Fatalf("opens %d", got)
	}
	if repo.openHigh("add", "1") != 1 {
		t.Fatalf("open high %d", repo.openHigh("add", "1"))
	}
	if findModel(t, repo, "add").State != Ready {
		t.Fatalf("state %s", findModel(t, repo, "add").State)
	}
}

func testReloadNotify(t *testing.T) {
	repo, cfg, onnx := loadAdd(t, "")
	w := startWatch(t, repo, cfg, time.Hour, 20*time.Millisecond)
	next := rewriteFile(t, onnx, committedModel(t), statFile(onnx))
	w.Notify("add")
	waitIdentity(t, repo, next, 2*time.Second)
	if repo.epoch("add") != 2 {
		t.Fatalf("epoch %d", repo.epoch("add"))
	}
	epoch := repo.epoch("add")
	w.Notify("add")
	time.Sleep(80 * time.Millisecond)
	if repo.epoch("add") != epoch || repo.opens("add") != 2 {
		t.Fatalf("epoch %d opens %d", repo.epoch("add"), repo.opens("add"))
	}
}

func testReloadPoll(t *testing.T) {
	repo, cfg, onnx := loadAdd(t, "")
	startWatch(t, repo, cfg, 20*time.Millisecond, time.Hour)
	next := rewriteFile(t, onnx, committedModel(t), statFile(onnx))
	waitIdentity(t, repo, next, 2*time.Second)
	if repo.epoch("add") < 2 {
		t.Fatalf("epoch %d", repo.epoch("add"))
	}
}

func testReloadWrites(t *testing.T) {
	repo, cfg, onnx := loadAdd(t, "")
	const quiet = 40 * time.Millisecond
	startWatch(t, repo, cfg, time.Hour, quiet)
	before := repo.opens("add")
	id := statFile(onnx)
	for range 12 {
		id = rewriteFile(t, onnx, committedModel(t), id)
	}
	waitIdentity(t, repo, id, 3*time.Second)
	time.Sleep(3 * quiet)
	got := repo.opens("add") - before
	if got == 0 || got > 2 {
		t.Fatalf("opens %d", got)
	}
	if repo.openHigh("add", "1") != 1 {
		t.Fatalf("open high %d", repo.openHigh("add", "1"))
	}
}

func testReloadData(t *testing.T) {
	repo, cfg, onnx := loadAdd(t, "")
	w := startWatch(t, repo, cfg, time.Hour, time.Hour)
	dataPath := onnx + ".data"
	if err := os.WriteFile(dataPath, []byte("external"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := w.Scan(); err != nil {
		t.Fatal(err)
	}
	if repo.epoch("add") != 2 || findModel(t, repo, "add").State != Ready {
		t.Fatalf("epoch %d state %s reason %q", repo.epoch("add"), findModel(t, repo, "add").State, findModel(t, repo, "add").Reason)
	}
	_, dataID, ok := repo.fileIdentity("add", "1")
	want := statFile(dataPath)
	if !ok || !dataID.same(want) {
		t.Fatalf("data %+v want %+v", dataID, want)
	}

	next := rewriteFile(t, dataPath, []byte("external-2"), want)
	if err := w.Scan(); err != nil {
		t.Fatal(err)
	}
	if repo.epoch("add") != 3 {
		t.Fatalf("epoch %d", repo.epoch("add"))
	}
	_, dataID, ok = repo.fileIdentity("add", "1")
	if !ok || !dataID.same(next) || findModel(t, repo, "add").State != Ready {
		t.Fatalf("data %+v state %s reason %q", dataID, findModel(t, repo, "add").State, findModel(t, repo, "add").Reason)
	}
	out := runAdd(t, repo, "", float32Bytes(1, 1, 1, 1, 1, 1), float32Bytes(2, 2, 2, 2, 2, 2))
	assertThrees(t, out.Buffer.Data)
	repo.Release(out)
}

func testReloadKnowledge(t *testing.T) {
	root := t.TempDir()
	knowledge := t.TempDir()
	copyAdd(t, root, "add")
	writeFile(t, filepath.Join(root, "add", "knowledge"), []byte("corpus\n"))
	if err := os.Mkdir(filepath.Join(knowledge, "corpus"), 0o755); err != nil {
		t.Fatal(err)
	}
	notes := filepath.Join(knowledge, "corpus", "notes.txt")
	writeFile(t, notes, []byte("labels"))
	cfg := testConfig(root, knowledge)
	repo := loadCfg(t, cfg)
	if repo.epoch("add") != 1 || findModel(t, repo, "add").State != Ready {
		t.Fatalf("epoch %d state %s", repo.epoch("add"), findModel(t, repo, "add").State)
	}
	w := startWatch(t, repo, cfg, time.Hour, time.Hour)

	writeFile(t, notes, []byte("labels changed"))
	writeFile(t, filepath.Join(root, "add", "vocab.json"), []byte("{}\n"))
	nameFile := filepath.Join(root, "add", "knowledge")
	before := statFile(nameFile)
	writeFile(t, nameFile, []byte("# note\ncorpus\n"))
	if statFile(nameFile).same(before) {
		t.Fatal("name file identity did not change")
	}
	if err := w.Scan(); err != nil {
		t.Fatal(err)
	}
	if repo.epoch("add") != 1 || repo.opens("add") != 1 {
		t.Fatalf("epoch %d opens %d", repo.epoch("add"), repo.opens("add"))
	}

	ones := float32Bytes(1, 1, 1, 1, 1, 1)
	twos := float32Bytes(2, 2, 2, 2, 2, 2)
	release, done := startHeld(t, repo, "add", ones, twos)
	if err := os.RemoveAll(filepath.Join(knowledge, "corpus")); err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { finished <- w.Scan() }()
	waitState(t, repo, "add", Loading)
	release()
	got := <-done
	if got.err != nil {
		t.Fatal(got.err)
	}
	assertThrees(t, got.out[0].Buffer.Data)
	repo.Release(got.out[0])
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	assertUnavailable(t, repo, "add", "corpus")
	if repo.openNow("add") != 0 {
		t.Fatalf("open %d", repo.openNow("add"))
	}

	if err := os.Mkdir(filepath.Join(knowledge, "corpus"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := w.Scan(); err != nil {
		t.Fatal(err)
	}
	if findModel(t, repo, "add").State != Ready || repo.epoch("add") != 2 || repo.openNow("add") != 1 {
		t.Fatalf("epoch %d open %d state %s", repo.epoch("add"), repo.openNow("add"), findModel(t, repo, "add").State)
	}
}

func testReloadAddRemove(t *testing.T) {
	repo, cfg, _ := loadAdd(t, "")
	w := startWatch(t, repo, cfg, time.Hour, time.Hour)
	if repo.epoch("add") != 1 {
		t.Fatalf("epoch %d", repo.epoch("add"))
	}
	copyAdd(t, cfg.ModelsRoot, "other")
	if err := w.Scan(); err != nil {
		t.Fatal(err)
	}
	if findModel(t, repo, "other").State != Ready || repo.epoch("other") != 1 || repo.epoch("add") != 1 {
		t.Fatalf("other epoch %d state %s add epoch %d", repo.epoch("other"), findModel(t, repo, "other").State, repo.epoch("add"))
	}

	ones := float32Bytes(1, 1, 1, 1, 1, 1)
	twos := float32Bytes(2, 2, 2, 2, 2, 2)
	release, done := startHeld(t, repo, "other", ones, twos)
	if err := os.RemoveAll(filepath.Join(cfg.ModelsRoot, "other")); err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { finished <- w.Scan() }()
	deadline := time.Now().Add(5 * time.Second)
	for repo.Admit("other", "").Status != StatusMissing {
		if time.Now().After(deadline) {
			t.Fatal("removed model was still admitted")
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case got := <-done:
		t.Fatalf("in-flight finished early: %v", got.err)
	default:
	}
	release()
	got := <-done
	if got.err != nil {
		t.Fatal(got.err)
	}
	assertThrees(t, got.out[0].Buffer.Data)
	repo.Release(got.out[0])
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	if adm := repo.Admit("other", ""); adm.Status != StatusMissing || adm.Proceed() {
		t.Fatalf("admission %+v", adm)
	}
	if _, err := repo.Run(context.Background(), "other", "", addInputs(ones, twos)); err == nil {
		t.Fatal("run after remove succeeded")
	} else {
		var status *StatusError
		if !errors.As(err, &status) || status.Status != StatusMissing {
			t.Fatalf("after remove: %v", err)
		}
	}
	if repo.epoch("add") != 1 || findModel(t, repo, "add").State != Ready || repo.sessionsOpen() != 1 {
		t.Fatalf("add epoch %d sessions %d", repo.epoch("add"), repo.sessionsOpen())
	}
}

func loadAdd(t *testing.T, other string) (*Repository, Config, string) {
	t.Helper()
	root := t.TempDir()
	cfg := testConfig(root, t.TempDir())
	onnx := copyAdd(t, root, "add")
	if other != "" {
		copyAdd(t, root, other)
	}
	return loadCfg(t, cfg), cfg, onnx
}

func loadCfg(t *testing.T, cfg Config) *Repository {
	t.Helper()
	pool, err := NewPool(DefaultCeiling)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := Load(cfg, pool)
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

func testConfig(models, knowledge string) Config {
	return Config{
		ModelsRoot:    models,
		KnowledgeRoot: knowledge,
		IntraOp:       1,
		InterOp:       1,
		Providers:     []ort.Provider{{Name: ort.CPUExecutionProvider}},
	}
}

func startWatch(t *testing.T, repo *Repository, cfg Config, poll, quiet time.Duration) *Watcher {
	t.Helper()
	w, err := Watch(context.Background(), repo, cfg, poll, quiet)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := w.Close(); err != nil {
			t.Errorf("watch: %v", err)
		}
	})
	return w
}

func copyAdd(t *testing.T, modelsRoot, name string) string {
	t.Helper()
	dst := filepath.Join(modelsRoot, name)
	copyDir(t, filepath.Join(committedRoot(t), "add"), dst)
	return filepath.Join(dst, "model.onnx")
}

func copyDir(t *testing.T, src, dst string) {
	t.Helper()
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		from := filepath.Join(src, entry.Name())
		to := filepath.Join(dst, entry.Name())
		if entry.IsDir() {
			copyDir(t, from, to)
			continue
		}
		data, err := os.ReadFile(from)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(to, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func rewriteFile(t *testing.T, path string, data []byte, prev fileID) fileID {
	t.Helper()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	id := statFile(path)
	if id.same(prev) {
		next := prev.modTime.Add(time.Second)
		if err := os.Chtimes(path, next, next); err != nil {
			t.Fatal(err)
		}
		id = statFile(path)
	}
	if !id.present || id.same(prev) {
		t.Fatal("file identity did not change")
	}
	return id
}

func startHeld(t *testing.T, repo *Repository, name string, ones, twos []byte) (func(), <-chan result) {
	t.Helper()
	started := make(chan struct{})
	letGo := make(chan struct{})
	var entered sync.Once
	var released sync.Once
	beforeRuntime = func([]tensorMemory) {
		entered.Do(func() {
			close(started)
			<-letGo
		})
	}
	out := make(chan result, 1)
	go func() {
		outputs, err := repo.Run(context.Background(), name, "", addInputs(ones, twos))
		out <- result{out: outputs, err: err}
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("run did not hold the slot")
	}
	release := func() {
		released.Do(func() { close(letGo) })
		beforeRuntime = nil
	}
	t.Cleanup(release)
	return release, out
}

func waitIdentity(t *testing.T, repo *Repository, want fileID, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for {
		onnx, _, ok := repo.fileIdentity("add", "1")
		if ok && onnx.same(want) && findModel(t, repo, "add").State == Ready {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("identity inode %d size %d time %s state %s", onnx.inode, onnx.size, onnx.modTime, findModel(t, repo, "add").State)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func waitState(t *testing.T, repo *Repository, name string, state State) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if findModel(t, repo, name).State == state {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("state %s", findModel(t, repo, name).State)
		}
		time.Sleep(time.Millisecond)
	}
}
