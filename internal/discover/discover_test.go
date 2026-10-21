package discover

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eclipse-iofog/ai-inference-onnx/internal/config"
)

func TestFlatDirectory(t *testing.T) {
	t.Parallel()

	t.Run("model.onnx wins", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		dir := filepath.Join(root, "detector")
		model := filepath.Join(dir, "model.onnx")
		touch(t, model)
		touch(t, model+".data")
		touch(t, filepath.Join(dir, "other.onnx"))

		got := oneModel(t, root)
		requireResolved(t, got, 1)
		requireVersion(t, got.Versions[0], "1", model, true)
		requireSelected(t, got, "", "1", model)
		requireSelected(t, got, "1", "1", model)
		requireNotSelected(t, got, "2")
		requireNotSelected(t, got, "latest")
		requireNotSelected(t, got, "v1")
		requireNotSelected(t, got, "01")
	})

	t.Run("one other file is version 1", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		path := filepath.Join(root, "wafer-defect", "wafer-defect.onnx")
		touch(t, path)

		got := oneModel(t, root)
		requireResolved(t, got, 1)
		requireVersion(t, got.Versions[0], "1", path, false)
		requireSelected(t, got, "", "1", path)
		if got.MaxBatchSize != nil {
			t.Fatalf("batch size %d", *got.MaxBatchSize)
		}
	})

	t.Run("model.onnx wins over a named file", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		dir := filepath.Join(root, "detector")
		model := filepath.Join(dir, "model.onnx")
		touch(t, model)
		touch(t, filepath.Join(dir, "other.onnx"))
		writeFile(t, filepath.Join(dir, "config.pbtxt"), `default_model_filename: "other.onnx"`)

		got := oneModel(t, root)
		requireResolved(t, got, 1)
		requireVersion(t, got.Versions[0], "1", model, false)
	})

	t.Run("one file wins over a missing name", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		path := filepath.Join(root, "detector", "only.onnx")
		touch(t, path)
		writeFile(t, filepath.Join(root, "detector", "config.pbtxt"), `default_model_filename: "missing.onnx"`)

		got := oneModel(t, root)
		requireResolved(t, got, 1)
		requireVersion(t, got.Versions[0], "1", path, false)
	})

	t.Run("directory form is not a flat model", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		touch(t, filepath.Join(root, "detector", "model.onnx", "model.onnx"))

		got := oneModel(t, root)
		requireReason(t, got, "no ONNX model in directory")
	})
}

func TestNumericVersions(t *testing.T) {
	t.Parallel()

	t.Run("10 outranks 9", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		dir := filepath.Join(root, "detector")
		touch(t, filepath.Join(dir, "8", "model.onnx"))
		touch(t, filepath.Join(dir, "9", "model.onnx"))
		highest := filepath.Join(dir, "10", "model.onnx")
		touch(t, highest)
		touch(t, filepath.Join(dir, "beside.onnx"))

		got := oneModel(t, root)
		requireResolved(t, got, 1)
		requireVersion(t, got.Versions[0], "10", highest, false)
		requireSelected(t, got, "", "10", highest)
		requireSelected(t, got, "10", "10", highest)
		requireNotSelected(t, got, "9")
		requireNotSelected(t, got, "8")
		requireNotSelected(t, got, "latest")
		requireNotSelected(t, got, "v1")
		requireNotSelected(t, got, "01")
		requireNotSelected(t, got, "1")
	})

	t.Run("model.onnx directory inside a version", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		dir := filepath.Join(root, "detector")
		inner := filepath.Join(dir, "1", "model.onnx", "model.onnx")
		touch(t, inner)
		touch(t, filepath.Join(dir, "1", "model.onnx", "other.bin"))
		touch(t, filepath.Join(dir, "beside.onnx"))

		got := oneModel(t, root)
		requireResolved(t, got, 1)
		requireVersion(t, got.Versions[0], "1", inner, false)
		if strings.HasSuffix(got.Versions[0].Path, "beside.onnx") {
			t.Fatalf("path %s", got.Versions[0].Path)
		}
	})

	t.Run("version 2 is not flattened to 1", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		path := filepath.Join(root, "detector", "2", "model.onnx")
		touch(t, path)

		got := oneModel(t, root)
		requireResolved(t, got, 1)
		requireVersion(t, got.Versions[0], "2", path, false)
		requireSelected(t, got, "", "2", path)
		requireNotSelected(t, got, "1")
	})

	t.Run("empty version directory falls back", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		dir := filepath.Join(root, "detector")
		touch(t, filepath.Join(dir, "1", "readme.txt"))
		path := filepath.Join(dir, "model.onnx")
		touch(t, path)

		got := oneModel(t, root)
		requireResolved(t, got, 1)
		requireVersion(t, got.Versions[0], "1", path, false)
	})

	t.Run("file beside version directories is ignored", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		dir := filepath.Join(root, "detector")
		path := filepath.Join(dir, "1", "model.onnx")
		touch(t, path)
		touch(t, filepath.Join(dir, "other.onnx"))

		got := oneModel(t, root)
		requireResolved(t, got, 1)
		requireVersion(t, got.Versions[0], "1", path, false)
	})
}

func TestVersionPolicy(t *testing.T) {
	t.Parallel()

	t.Run("latest of 2", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		dir := filepath.Join(root, "detector")
		writeVersions(t, dir, "8", "9", "10")
		body := `version_policy: { latest: { num_versions: 2}}`
		writeFile(t, filepath.Join(dir, "config.pbtxt"), body)

		got := oneModel(t, root)
		requireResolved(t, got, 2)
		requireVersion(t, got.Versions[0], "10", filepath.Join(dir, "10", "model.onnx"), false)
		requireVersion(t, got.Versions[1], "9", filepath.Join(dir, "9", "model.onnx"), false)
		requireNotSelected(t, got, "8")
		requireSelected(t, got, "", "10", filepath.Join(dir, "10", "model.onnx"))
		parsed := config.ParseModelFile("detector", []byte(body))
		if parsed.VersionPolicy == nil || parsed.VersionPolicy.NumVersions != 2 {
			t.Fatalf("parser %+v", parsed.VersionPolicy)
		}
	})

	t.Run("all", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		dir := filepath.Join(root, "detector")
		writeVersions(t, dir, "8", "9", "10")
		writeFile(t, filepath.Join(dir, "config.pbtxt"), `version_policy: { all: {}}`)

		got := oneModel(t, root)
		requireResolved(t, got, 3)
		requireVersion(t, got.Versions[0], "10", filepath.Join(dir, "10", "model.onnx"), false)
		requireVersion(t, got.Versions[1], "9", filepath.Join(dir, "9", "model.onnx"), false)
		requireVersion(t, got.Versions[2], "8", filepath.Join(dir, "8", "model.onnx"), false)
		requireSelected(t, got, "8", "8", filepath.Join(dir, "8", "model.onnx"))
		requireSelected(t, got, "", "10", filepath.Join(dir, "10", "model.onnx"))
		requireNotSelected(t, got, "7")
		requireNotSelected(t, got, "latest")
		requireNotSelected(t, got, "v1")
		requireNotSelected(t, got, "01")
	})

	t.Run("specific", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		dir := filepath.Join(root, "detector")
		writeVersions(t, dir, "1", "2", "10")
		writeFile(t, filepath.Join(dir, "config.pbtxt"), `version_policy: { specific: { versions: [1, 10]}}`)

		got := oneModel(t, root)
		requireResolved(t, got, 2)
		requireVersion(t, got.Versions[0], "10", filepath.Join(dir, "10", "model.onnx"), false)
		requireVersion(t, got.Versions[1], "1", filepath.Join(dir, "1", "model.onnx"), false)
		requireNotSelected(t, got, "2")
		requireSelected(t, got, "", "10", filepath.Join(dir, "10", "model.onnx"))
	})

	t.Run("specific keeps a partial match", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		dir := filepath.Join(root, "detector")
		writeVersions(t, dir, "1", "2")
		writeFile(t, filepath.Join(dir, "config.pbtxt"), `version_policy: { specific: { versions: [2, 9]}}`)

		got := oneModel(t, root)
		requireResolved(t, got, 1)
		requireVersion(t, got.Versions[0], "2", filepath.Join(dir, "2", "model.onnx"), false)
		requireSelected(t, got, "", "2", filepath.Join(dir, "2", "model.onnx"))
	})

	t.Run("filename among several", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		dir := filepath.Join(root, "detector")
		touch(t, filepath.Join(dir, "a.onnx"))
		chosen := filepath.Join(dir, "b.onnx")
		touch(t, chosen)
		touch(t, filepath.Join(dir, "c.onnx"))
		writeFile(t, filepath.Join(dir, "config.pbtxt"), `default_model_filename: "b.onnx"`)

		got := oneModel(t, root)
		requireResolved(t, got, 1)
		requireVersion(t, got.Versions[0], "1", chosen, false)
		if got.MaxBatchSize != nil {
			t.Fatalf("batch size %d", *got.MaxBatchSize)
		}
	})

	t.Run("filename inside a version directory", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		dir := filepath.Join(root, "detector", "3")
		touch(t, filepath.Join(dir, "a.onnx"))
		chosen := filepath.Join(dir, "b.onnx")
		touch(t, chosen)
		writeFile(t, filepath.Join(root, "detector", "config.pbtxt"), `default_model_filename: "b.onnx"`)

		got := oneModel(t, root)
		requireResolved(t, got, 1)
		requireVersion(t, got.Versions[0], "3", chosen, false)
	})
}

func TestDottedName(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := filepath.Join(root, "my.detector")
	path := filepath.Join(dir, "model.onnx")
	touch(t, path)
	writeFile(t, filepath.Join(dir, "config.pbtxt"), `
name: "my.detector"
platform: "onnxruntime_onnx"
`)

	got := oneModel(t, root)
	if got.Name != "my.detector" {
		t.Fatalf("name %q", got.Name)
	}
	requireResolved(t, got, 1)
	requireVersion(t, got.Versions[0], "1", path, false)
	requireSelected(t, got, "", "1", path)
}

func TestUnavailable(t *testing.T) {
	t.Parallel()

	t.Run("no onnx file", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		touch(t, filepath.Join(root, "tokenizer", "vocab.json"))

		got := oneModel(t, root)
		requireReason(t, got, "no ONNX model in directory")
		if _, ok := got.Selected(""); ok {
			t.Fatal("selected")
		}
	})

	t.Run("nested files", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		touch(t, filepath.Join(root, "detector", "nested", "model.onnx"))
		touch(t, filepath.Join(root, "detector", "deep", "dir", "a.onnx"))

		got := oneModel(t, root)
		requireReason(t, got, "no ONNX model in directory")
	})

	t.Run("nested file inside a version directory", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		touch(t, filepath.Join(root, "detector", "3", "nested", "model.onnx"))

		got := oneModel(t, root)
		requireReason(t, got, "no ONNX model in directory")
	})

	t.Run("01 is not a version", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		touch(t, filepath.Join(root, "detector", "01", "model.onnx"))

		got := oneModel(t, root)
		requireReason(t, got, "no ONNX model in directory")
	})

	t.Run("01 beside a real version", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		dir := filepath.Join(root, "detector")
		touch(t, filepath.Join(dir, "01", "model.onnx"))
		path := filepath.Join(dir, "2", "model.onnx")
		touch(t, path)

		got := oneModel(t, root)
		requireResolved(t, got, 1)
		requireVersion(t, got.Versions[0], "2", path, false)
		requireNotSelected(t, got, "01")
		requireNotSelected(t, got, "1")
	})

	t.Run("v1 and latest are not versions", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		dir := filepath.Join(root, "detector")
		touch(t, filepath.Join(dir, "v1", "model.onnx"))
		touch(t, filepath.Join(dir, "latest", "model.onnx"))

		got := oneModel(t, root)
		requireReason(t, got, "no ONNX model in directory")
	})

	t.Run("several files", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		dir := filepath.Join(root, "detector")
		touch(t, filepath.Join(dir, "a.onnx"))
		touch(t, filepath.Join(dir, "b.onnx"))

		got := oneModel(t, root)
		requireUnavailable(t, got)
		if got.Reason == "no ONNX model in directory" {
			t.Fatalf("reason %q", got.Reason)
		}
	})

	t.Run("several files inside one version", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		dir := filepath.Join(root, "detector")
		touch(t, filepath.Join(dir, "1", "a.onnx"))
		touch(t, filepath.Join(dir, "1", "b.onnx"))
		touch(t, filepath.Join(dir, "2", "model.onnx"))

		got := oneModel(t, root)
		requireUnavailable(t, got)
		if got.Reason == "no ONNX model in directory" {
			t.Fatalf("reason %q", got.Reason)
		}
	})

	t.Run("rejected names", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		touch(t, filepath.Join(root, "bad*name", "model.onnx"))
		touch(t, filepath.Join(root, "bad>name", "model.onnx"))
		ok := filepath.Join(root, "ok", "model.onnx")
		touch(t, ok)
		touch(t, filepath.Join(root, "notes.txt"))

		got := mustResolve(t, root)
		if len(got) != 3 {
			t.Fatalf("models %d", len(got))
		}
		byName := map[string]Model{}
		for _, model := range got {
			byName[model.Name] = model
		}
		for _, name := range []string{"bad*name", "bad>name"} {
			model, ok := byName[name]
			if !ok {
				t.Fatalf("missing %s", name)
			}
			requireUnavailable(t, model)
			if !strings.Contains(model.Reason, "rejected") {
				t.Fatalf("reason %q", model.Reason)
			}
		}
		requireResolved(t, byName["ok"], 1)
		requireVersion(t, byName["ok"].Versions[0], "1", ok, false)
	})

	t.Run("num_versions below 1", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		dir := filepath.Join(root, "detector")
		touch(t, filepath.Join(dir, "model.onnx"))
		body := []byte(`version_policy: { latest: { num_versions: 0}}`)
		writeBytes(t, filepath.Join(dir, "config.pbtxt"), body)

		parsed := config.ParseModelFile("detector", body)
		if !parsed.Available {
			t.Fatalf("parser unavailable: %s", parsed.Reason)
		}
		got := oneModel(t, root)
		requireUnavailable(t, got)
	})

	t.Run("specific list matches nothing", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		dir := filepath.Join(root, "detector")
		writeVersions(t, dir, "1", "2")
		writeFile(t, filepath.Join(dir, "config.pbtxt"), `version_policy: { specific: { versions: [9]}}`)

		got := oneModel(t, root)
		requireUnavailable(t, got)
	})

	t.Run("empty specific list", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		dir := filepath.Join(root, "detector")
		touch(t, filepath.Join(dir, "model.onnx"))
		writeFile(t, filepath.Join(dir, "config.pbtxt"), `version_policy: { specific: { }}`)

		got := oneModel(t, root)
		requireUnavailable(t, got)
	})

	t.Run("parser", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		dir := filepath.Join(root, "detector")
		touch(t, filepath.Join(dir, "model.onnx"))
		body := []byte("name: \"other\"\n")
		writeBytes(t, filepath.Join(dir, "config.pbtxt"), body)

		got := oneModel(t, root)
		requireUnavailable(t, got)
		want := config.ParseModelFile("detector", body)
		if got.Reason != want.Reason {
			t.Fatalf("reason %q want %q", got.Reason, want.Reason)
		}
	})

	t.Run("unparsed file", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		dir := filepath.Join(root, "detector")
		touch(t, filepath.Join(dir, "model.onnx"))
		body := []byte("this is not protobuf text")
		writeBytes(t, filepath.Join(dir, "config.pbtxt"), body)

		got := oneModel(t, root)
		requireUnavailable(t, got)
		want := config.ParseModelFile("detector", body)
		if got.Reason != want.Reason {
			t.Fatalf("reason %q want %q", got.Reason, want.Reason)
		}
	})
}

func TestRootEntries(t *testing.T) {
	t.Parallel()

	t.Run("file is ignored", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		touch(t, filepath.Join(root, "readme.txt"))
		touch(t, filepath.Join(root, "model.onnx"))
		alpha := filepath.Join(root, "alpha", "weights.onnx")
		touch(t, alpha)
		beta := filepath.Join(root, "beta", "model.onnx")
		touch(t, beta)

		got := mustResolve(t, root)
		if len(got) != 2 {
			t.Fatalf("models %d", len(got))
		}
		if got[0].Name != "alpha" || got[1].Name != "beta" {
			t.Fatalf("names %q %q", got[0].Name, got[1].Name)
		}
		requireVersion(t, got[0].Versions[0], "1", alpha, false)
		requireVersion(t, got[1].Versions[0], "1", beta, false)
	})

	t.Run("symlink is the model", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		versionName := "..2026_10_21_22_35_44.597280736"
		touch(t, filepath.Join(root, versionName, "wafer-defect", "model.onnx"))
		model := filepath.Join(root, "wafer-defect", "model.onnx")
		if err := os.Symlink(versionName, filepath.Join(root, "..data")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join("..data", "wafer-defect"), filepath.Join(root, "wafer-defect")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(versionName, "missing"), filepath.Join(root, "gone")); err != nil {
			t.Fatal(err)
		}

		got := mustResolve(t, root)
		if len(got) != 1 {
			t.Fatalf("models %d", len(got))
		}
		requireResolved(t, got[0], 1)
		if got[0].Name != "wafer-defect" {
			t.Fatalf("name %q", got[0].Name)
		}
		requireVersion(t, got[0].Versions[0], "1", model, false)
	})

	t.Run("empty root", func(t *testing.T) {
		t.Parallel()
		got := mustResolve(t, t.TempDir())
		if len(got) != 0 {
			t.Fatalf("models %d", len(got))
		}
	})

	t.Run("missing root", func(t *testing.T) {
		t.Parallel()
		_, err := Resolve(filepath.Join(t.TempDir(), "missing"))
		if err == nil {
			t.Fatal("expected an error")
		}
	})
}

func TestBatchSizeStaysUnset(t *testing.T) {
	t.Parallel()

	t.Run("omitted", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		touch(t, filepath.Join(root, "detector", "model.onnx"))

		got := oneModel(t, root)
		requireResolved(t, got, 1)
		if got.MaxBatchSize != nil {
			t.Fatalf("batch size %d", *got.MaxBatchSize)
		}
		if got.MaxQueueDelayMicroseconds != nil {
			t.Fatalf("delay %d", *got.MaxQueueDelayMicroseconds)
		}
	})

	t.Run("zero", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		dir := filepath.Join(root, "detector")
		touch(t, filepath.Join(dir, "model.onnx"))
		writeFile(t, filepath.Join(dir, "config.pbtxt"), `max_batch_size: 0`)

		got := oneModel(t, root)
		requireResolved(t, got, 1)
		if got.MaxBatchSize == nil || *got.MaxBatchSize != 0 {
			t.Fatalf("batch %+v", got.MaxBatchSize)
		}
	})

	t.Run("override", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		dir := filepath.Join(root, "detector")
		touch(t, filepath.Join(dir, "model.onnx"))
		writeFile(t, filepath.Join(dir, "config.pbtxt"), `
max_batch_size: 4
dynamic_batching { max_queue_delay_microseconds: 100 }
`)

		got := oneModel(t, root)
		requireResolved(t, got, 1)
		if got.MaxBatchSize == nil || *got.MaxBatchSize != 4 {
			t.Fatalf("batch %+v", got.MaxBatchSize)
		}
		if got.MaxQueueDelayMicroseconds == nil || *got.MaxQueueDelayMicroseconds != 100 {
			t.Fatalf("delay %+v", got.MaxQueueDelayMicroseconds)
		}
	})
}

func TestKnowledgeFileIsNotRead(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := filepath.Join(root, "detector")
	path := filepath.Join(dir, "model.onnx")
	touch(t, path)
	writeFile(t, filepath.Join(dir, "knowledge"), "*\n../secret\n")

	got := oneModel(t, root)
	requireResolved(t, got, 1)
	requireVersion(t, got.Versions[0], "1", path, false)
}

func TestProcessDoesNotResolveModels(t *testing.T) {
	t.Parallel()
	deps := goList(t, "./cmd/inference")
	if strings.Contains(deps, "internal/discover") {
		t.Fatal("process resolves model directories")
	}
}

func TestDoesNotLinkRuntime(t *testing.T) {
	t.Parallel()
	deps := goList(t, "./internal/discover")
	if strings.Contains(deps, "onnxruntime") {
		t.Fatal("directory resolution links ONNX Runtime")
	}
}

func writeVersions(t *testing.T, dir string, versions ...string) {
	t.Helper()
	for _, version := range versions {
		touch(t, filepath.Join(dir, version, "model.onnx"))
	}
}

func touch(t *testing.T, path string) {
	t.Helper()
	writeBytes(t, path, nil)
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	writeBytes(t, path, []byte(body))
}

func writeBytes(t *testing.T, path string, body []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustResolve(t *testing.T, root string) []Model {
	t.Helper()
	got, err := Resolve(root)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return got
}

func oneModel(t *testing.T, root string) Model {
	t.Helper()
	got := mustResolve(t, root)
	if len(got) != 1 {
		t.Fatalf("models %d: %+v", len(got), got)
	}
	return got[0]
}

func requireResolved(t *testing.T, got Model, versions int) {
	t.Helper()
	if !got.Available {
		t.Fatalf("unavailable: %s", got.Reason)
	}
	if got.Reason != "" {
		t.Fatalf("reason %q", got.Reason)
	}
	if len(got.Versions) != versions {
		t.Fatalf("versions %d: %+v", len(got.Versions), got.Versions)
	}
}

func requireUnavailable(t *testing.T, got Model) {
	t.Helper()
	if got.Available {
		t.Fatal("available")
	}
	if got.Reason == "" {
		t.Fatal("empty reason")
	}
	if len(got.Versions) != 0 {
		t.Fatalf("versions %+v", got.Versions)
	}
}

func requireReason(t *testing.T, got Model, reason string) {
	t.Helper()
	requireUnavailable(t, got)
	if got.Reason != reason {
		t.Fatalf("reason %q want %q", got.Reason, reason)
	}
}

func requireVersion(t *testing.T, got Version, version, path string, dataPresent bool) {
	t.Helper()
	if got.Version != version || got.Path != path || got.DataPresent != dataPresent {
		t.Fatalf("got version %q path %q data %v, want version %q path %q data %v", got.Version, got.Path, got.DataPresent, version, path, dataPresent)
	}
	if got.DataPath != path+".data" {
		t.Fatalf("data path %q", got.DataPath)
	}
}

func requireSelected(t *testing.T, got Model, query, version, path string) {
	t.Helper()
	selected, ok := got.Selected(query)
	if !ok {
		t.Fatalf("version %q not selected", query)
	}
	if selected.Version != version || selected.Path != path {
		t.Fatalf("selected version %q path %q, want version %q path %q", selected.Version, selected.Path, version, path)
	}
}

func requireNotSelected(t *testing.T, got Model, version string) {
	t.Helper()
	if _, ok := got.Selected(version); ok {
		t.Fatalf("version %q selected", version)
	}
}

func goList(t *testing.T, pattern string) string {
	t.Helper()
	rootCmd := exec.Command("go", "list", "-m", "-f", "{{.Dir}}")
	rootOut, err := rootCmd.Output()
	if err != nil {
		t.Fatalf("module root: %v", err)
	}
	cmd := exec.Command("go", "list", "-deps", pattern)
	cmd.Dir = strings.TrimSpace(string(rootOut))
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	return string(out)
}
