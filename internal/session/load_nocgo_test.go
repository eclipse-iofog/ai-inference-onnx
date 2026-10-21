//go:build !cgo

package session

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/eclipse-iofog/ai-inference-onnx/internal/ort"
)

func TestStateFunctionWithoutLibrary(t *testing.T) {
	root := t.TempDir()
	writeBytes(t, filepath.Join(root, "add", "model.onnx"), []byte("model"))
	pool, err := NewPool(DefaultCeiling)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	fn := func(string, State, []CatalogModel) { calls++ }
	repo, err := Load(Config{
		ModelsRoot:    root,
		KnowledgeRoot: t.TempDir(),
		IntraOp:       1,
		InterOp:       1,
		Providers:     []ort.Provider{{Name: ort.CPUExecutionProvider}},
		OnState:       fn,
	}, pool)
	if err == nil {
		t.Fatal("Load succeeded without the runtime library")
	}
	if repo == nil {
		t.Fatal("missing repository")
	}
	repo.OnState(fn)
	repo.OnState(nil)
	if got := repo.Admit("add", "1"); got.Status != StatusMissing {
		t.Fatalf("status %d", got.Status)
	}
	if _, err := repo.Run(context.Background(), "add", "1", nil); err == nil {
		t.Fatal("Run succeeded without the runtime library")
	}
	if len(repo.Models()) != 0 {
		t.Fatal("model set is not empty")
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("calls %d", calls)
	}
}

func TestLoadWithoutLibrary(t *testing.T) {
	root := t.TempDir()
	writeBytes(t, filepath.Join(root, "add", "model.onnx"), []byte("model"))
	pool, err := NewPool(DefaultCeiling)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := Load(Config{
		ModelsRoot:    root,
		KnowledgeRoot: t.TempDir(),
		IntraOp:       1,
		InterOp:       1,
		Providers:     []ort.Provider{{Name: ort.CPUExecutionProvider}},
	}, pool)
	if err == nil {
		t.Fatal("Load succeeded without the runtime library")
	}
	if repo == nil || len(repo.Models()) != 0 {
		t.Fatal("model set is not empty")
	}
	if _, err := repo.Run(context.Background(), "add", "1", nil); err == nil {
		t.Fatal("Run succeeded without the runtime library")
	}
	if got := repo.Admit("add", "1"); got.Status != StatusMissing {
		t.Fatalf("status %d", got.Status)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
}

func writeBytes(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}
