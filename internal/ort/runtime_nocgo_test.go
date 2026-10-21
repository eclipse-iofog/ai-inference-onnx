//go:build !cgo

package ort

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestLibraryUnavailable(t *testing.T) {
	if err := Init(); err == nil {
		t.Fatal("Init succeeded without the runtime library")
	}
	if _, err := GetVersion(); err == nil {
		t.Fatal("GetVersion succeeded without the runtime library")
	}
	if _, err := AvailableProviders(); err == nil {
		t.Fatal("AvailableProviders succeeded without the runtime library")
	}
	session, err := Open("model.onnx", 1, 1, []Provider{{Name: CPUExecutionProvider}})
	if err == nil || session != nil {
		t.Fatal("Open succeeded without the runtime library")
	}
	tensor, err := NewTensor[float32]([]int64{1}, []float32{0})
	if err == nil || tensor != nil {
		t.Fatal("tensor create succeeded without the runtime library")
	}
	if err := Shutdown(); err != nil {
		t.Fatal(err)
	}
	if err := Init(); err == nil {
		t.Fatal("Init succeeded without the runtime library")
	}
}

func TestRuntimeModuleAbsent(t *testing.T) {
	env := make([]string, 0, len(os.Environ())+1)
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "CGO_ENABLED=") {
			env = append(env, kv)
		}
	}
	cmd := exec.Command("go", "list", "-deps", "-f", "{{if .Module}}{{.Module.Path}}{{end}}", ".")
	cmd.Env = append(env, "CGO_ENABLED=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps: %v\n%s", err, out)
	}
	if strings.Contains(string(out), "github.com/microsoft/onnxruntime/go") {
		t.Fatalf("runtime module linked:\n%s", out)
	}
}
