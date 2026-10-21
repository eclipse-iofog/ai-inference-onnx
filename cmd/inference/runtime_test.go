//go:build cgo

package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/eclipse-iofog/ai-inference-onnx/internal/ort"
)

func TestRuntimeModelBecomesReady(t *testing.T) {
	t.Parallel()
	requireRuntime(t)

	ports := pickPorts(t)
	models := filepath.Join(t.TempDir(), "models")
	knowledge := filepath.Join(t.TempDir(), "knowledge")
	copyAddModel(t, models)
	if err := os.MkdirAll(knowledge, 0o755); err != nil {
		t.Fatal(err)
	}
	nats, dials := listenForDial(t)

	proc := startCgoProcess(t, runtimeEnv(t, ports, map[string]string{
		"AUTH_MODE":       "disabled",
		"MODELS_PATH":     models,
		"KNOWLEDGE_PATH":  knowledge,
		"NATS_SERVER_URL": "nats://" + nats,
		"NATS_CREDS_PATH": filepath.Join(t.TempDir(), "missing.creds"),
	}))
	t.Cleanup(proc.kill)

	waitStatus(t, ports.http, "/v2/health/live", http.StatusOK, 20*time.Second)
	waitStatus(t, ports.http, "/v2/health/ready", http.StatusOK, 20*time.Second)
	assertGRPCHealth(t, ports.grpc, true)
	assertAddInfer(t, ports.http)
	assertGRPCAddInfer(t, ports.grpc)
	assertGET(t, ports.http, "/v2/health/live", http.StatusOK)
	assertGET(t, ports.http, "/v2/health/ready", http.StatusOK)
	assertMetricsOpen(t, ports.metrics)
	// Both NATS variables are set, so the process dials even when the credentials file is missing.
	// That retry leaves live and ready on the model.
	deadline := time.Now().Add(5 * time.Second)
	for dials.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if dials.Load() == 0 {
		t.Fatal("nats did not dial while the server URL and credentials path were set")
	}
	assertGET(t, ports.http, "/v2/health/live", http.StatusOK)
	assertGET(t, ports.http, "/v2/health/ready", http.StatusOK)
	stopProcess(t, proc)
	var chains, cacheWarns int
	for _, line := range decodeLog(t, proc.stdout.String()) {
		if strings.EqualFold(line.Level, "info") && line.Msg == "execution providers: CPUExecutionProvider" {
			chains++
		}
		if strings.EqualFold(line.Level, "warn") && strings.Contains(line.Msg, "user cache directory") {
			cacheWarns++
		}
	}
	if chains != 1 || cacheWarns != 1 {
		t.Fatalf("provider lines chains=%d cacheWarns=%d\n%s", chains, cacheWarns, proc.stdout.String())
	}
}

func TestRuntimeNotReady(t *testing.T) {
	t.Parallel()
	requireRuntime(t)

	t.Run("missing models", func(t *testing.T) {
		t.Parallel()
		ports := pickPorts(t)
		models := filepath.Join(t.TempDir(), "models")
		requireAbsent(t, models)
		proc := startCgoProcess(t, runtimeEnv(t, ports, map[string]string{
			"AUTH_MODE":   "disabled",
			"MODELS_PATH": models,
		}))
		t.Cleanup(proc.kill)
		assertLiveNotReady(t, ports)
		assertMetricsOpen(t, ports.metrics)
		stopProcess(t, proc)
	})

	t.Run("garbage model", func(t *testing.T) {
		t.Parallel()
		ports := pickPorts(t)
		models := filepath.Join(t.TempDir(), "models")
		writeGarbageModel(t, models, "broken")
		proc := startCgoProcess(t, runtimeEnv(t, ports, map[string]string{
			"AUTH_MODE":   "disabled",
			"MODELS_PATH": models,
		}))
		t.Cleanup(proc.kill)
		assertLiveNotReady(t, ports)
		stopProcess(t, proc)
	})
}

func TestRuntimeReadyBesideUnavailable(t *testing.T) {
	t.Parallel()
	requireRuntime(t)

	ports := pickPorts(t)
	models := filepath.Join(t.TempDir(), "models")
	copyAddModel(t, models)
	writeGarbageModel(t, models, "broken")
	proc := startCgoProcess(t, runtimeEnv(t, ports, map[string]string{
		"AUTH_MODE":   "disabled",
		"MODELS_PATH": models,
	}))
	t.Cleanup(proc.kill)

	waitStatus(t, ports.http, "/v2/health/live", http.StatusOK, 20*time.Second)
	waitStatus(t, ports.http, "/v2/health/ready", http.StatusOK, 20*time.Second)
	assertMetricsOpen(t, ports.metrics)
	stopProcess(t, proc)
}

func TestRuntimeMissingLibraryStaysUp(t *testing.T) {
	t.Parallel()
	requireRuntime(t)

	ports := pickPorts(t)
	proc := startCgoProcess(t, runtimeEnv(t, ports, map[string]string{
		"AUTH_MODE":    "disabled",
		"ORT_LIB_PATH": filepath.Join(t.TempDir(), "missing-onnxruntime"),
	}))
	t.Cleanup(proc.kill)

	assertLiveNotReady(t, ports)
	if err := proc.signal(syscall.Signal(0)); err != nil {
		t.Fatalf("process exited\n%s", proc.output())
	}
	var runtimeLogged bool
	for _, line := range decodeLog(t, proc.stdout.String()) {
		if strings.EqualFold(line.Level, "error") && strings.Contains(line.Msg, "runtime:") {
			runtimeLogged = true
			break
		}
	}
	if !runtimeLogged {
		t.Fatalf("runtime failure was not logged\n%s", proc.stdout.String())
	}
	if strings.TrimSpace(proc.stderr.String()) != "" {
		t.Fatalf("stderr %s", proc.stderr.String())
	}
	assertMetricsOpen(t, ports.metrics)
	stopProcess(t, proc)
}

func TestCgoDependenciesIncludeRuntime(t *testing.T) {
	t.Parallel()
	requireRuntime(t)

	cmd := exec.Command("go", "list", "-deps", "./cmd/inference")
	cmd.Dir = moduleRoot(t)
	cmd.Env = cgoEnv(os.Environ())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	var runtime, client bool
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		switch line {
		case "github.com/microsoft/onnxruntime/go/onnxruntime":
			runtime = true
		case "github.com/nats-io/nats.go":
			client = true
		}
		if strings.HasPrefix(line, "github.com/nats-io/nats-server") {
			t.Errorf("the process links the NATS server %s", line)
		}
		if strings.Contains(line, "ai-inference-onnx/dashboard") {
			t.Errorf("the process links the dashboard module %s", line)
		}
	}
	if !runtime {
		t.Fatalf("runtime module missing:\n%s", out)
	}
	if !client {
		t.Error("the process does not link github.com/nats-io/nats.go")
	}
}

func assertLiveNotReady(t *testing.T, ports listenPorts) {
	t.Helper()
	waitStatus(t, ports.http, "/v2/health/live", http.StatusOK, 20*time.Second)
	waitStatus(t, ports.http, "/v2/health/ready", http.StatusBadRequest, 20*time.Second)
	assertGRPCHealth(t, ports.grpc, false)
}

func waitStatus(t *testing.T, port int, path string, status int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last error
	for time.Now().Before(deadline) {
		got, body, err := getErr(port, path)
		if err == nil && got == status && len(body) == 0 {
			return
		}
		if err != nil {
			last = err
		} else {
			last = fmt.Errorf("status %d body %q", got, body)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s: %v", path, last)
}

func assertAddInfer(t *testing.T, port int) {
	t.Helper()
	for _, path := range []string{"/v2/models/add/infer", "/v2/models/add/versions/1/infer"} {
		status, hdr, body := postInfer(t, port, path, []byte(addJSON("add-json")), nil)
		assertThreesJSON(t, path, status, hdr, body, "add-json")
		status, hdr, body = postInfer(t, port, path, addBinary("add-bin"), map[string]string{
			"Inference-Header-Content-Length": strconv.Itoa(len(addBinaryHeader("add-bin"))),
		})
		assertThreesBinary(t, path, status, hdr, body, "add-bin")
	}
	status, _, body := postInfer(t, port, "/v2/models/add/versions/2/infer", []byte(addJSON("x")), nil)
	assertInferStatus(t, status, body, http.StatusNotFound)
	status, _, body = postInfer(t, port, "/v2/models/add/versions/latest/infer", []byte(addJSON("x")), nil)
	assertInferStatus(t, status, body, http.StatusNotFound)
	status, _, body = postInfer(t, port, "/v2/models/missing/infer", []byte(addJSON("x")), nil)
	assertInferStatus(t, status, body, http.StatusNotFound)
	status, _, body = postInfer(t, port, "/v2/models/add/infer", []byte(`{"inputs":[{"name":"A","datatype":"INT32","shape":[2,3],"data":[1,1,1,1,1,1]},{"name":"B","datatype":"FP32","shape":[2,3],"data":[1,1,1,1,1,1]}]}`), nil)
	assertInferStatus(t, status, body, http.StatusBadRequest)
	status, _, body = postInfer(t, port, "/v2/models/add/infer", float32LE(1, 2, 3), map[string]string{
		"Inference-Header-Content-Length": "0",
	})
	assertInferStatus(t, status, body, http.StatusBadRequest)
}

func assertThreesJSON(t *testing.T, path string, status int, hdr http.Header, body []byte, id string) {
	t.Helper()
	if status != http.StatusOK {
		t.Fatalf("%s status %d body %s", path, status, body)
	}
	if hdr.Get("Content-Type") != "application/json" {
		t.Fatalf("%s Content-Type %q", path, hdr.Get("Content-Type"))
	}
	if hdr.Get("Inference-Header-Content-Length") != "" {
		t.Fatalf("%s binary header %q", path, hdr.Get("Inference-Header-Content-Length"))
	}
	doc := decodeInfer(t, body)
	if doc.ModelName != "add" || doc.ModelVersion != "1" || doc.ID != id {
		t.Fatalf("%s response %+v", path, doc)
	}
	if len(doc.Outputs) != 1 || doc.Outputs[0].Name != "C" || doc.Outputs[0].Datatype != "FP32" {
		t.Fatalf("%s outputs %+v", path, doc.Outputs)
	}
	if len(doc.Outputs[0].Shape) != 2 || doc.Outputs[0].Shape[0] != 2 || doc.Outputs[0].Shape[1] != 3 {
		t.Fatalf("%s shape %v", path, doc.Outputs[0].Shape)
	}
	if len(doc.Outputs[0].Data) != 6 {
		t.Fatalf("%s data %v", path, doc.Outputs[0].Data)
	}
	for i, value := range doc.Outputs[0].Data {
		if value != 3 {
			t.Fatalf("%s output[%d]=%v", path, i, value)
		}
	}
}

func assertThreesBinary(t *testing.T, path string, status int, hdr http.Header, body []byte, id string) {
	t.Helper()
	if status != http.StatusOK {
		t.Fatalf("%s status %d body %s", path, status, body)
	}
	if hdr.Get("Content-Type") != "application/octet-stream" {
		t.Fatalf("%s Content-Type %q", path, hdr.Get("Content-Type"))
	}
	n, err := strconv.Atoi(hdr.Get("Inference-Header-Content-Length"))
	if err != nil || n <= 0 || n >= len(body) {
		t.Fatalf("%s header %q body %d", path, hdr.Get("Inference-Header-Content-Length"), len(body))
	}
	doc := decodeInfer(t, body[:n])
	if doc.ModelName != "add" || doc.ModelVersion != "1" || doc.ID != id {
		t.Fatalf("%s header %+v", path, doc)
	}
	if len(doc.Outputs) != 1 || doc.Outputs[0].Name != "C" {
		t.Fatalf("%s outputs %+v", path, doc.Outputs)
	}
	suffix := body[n:]
	if len(suffix) != 24 {
		t.Fatalf("%s suffix %d", path, len(suffix))
	}
	for i := 0; i < 6; i++ {
		value := math.Float32frombits(binary.LittleEndian.Uint32(suffix[i*4:]))
		if value != 3 {
			t.Fatalf("%s suffix[%d]=%v", path, i, value)
		}
	}
}

func assertInferStatus(t *testing.T, status int, body []byte, want int) {
	t.Helper()
	if status != want {
		t.Fatalf("status %d body %s", status, body)
	}
	var doc struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &doc); err != nil || doc.Error == "" {
		t.Fatalf("body %s", body)
	}
}

type inferOutput struct {
	Name     string    `json:"name"`
	Datatype string    `json:"datatype"`
	Shape    []int64   `json:"shape"`
	Data     []float64 `json:"data"`
}

type inferDocument struct {
	ModelName    string        `json:"model_name"`
	ModelVersion string        `json:"model_version"`
	ID           string        `json:"id"`
	Outputs      []inferOutput `json:"outputs"`
}

func decodeInfer(t *testing.T, body []byte) inferDocument {
	t.Helper()
	var doc inferDocument
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("json %s: %v", body, err)
	}
	return doc
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

func float32LE(vals ...float32) []byte {
	buf := make([]byte, 4*len(vals))
	for i, value := range vals {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(value))
	}
	return buf
}

func postInfer(t *testing.T, port int, path string, body []byte, headers map[string]string) (int, http.Header, []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("http://127.0.0.1:%d%s", port, path), strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := healthClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, resp.Header, payload
}

func listenForDial(t *testing.T) (string, *atomic.Int32) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	var dials atomic.Int32
	go func() {
		for {
			conn, acceptErr := ln.Accept()
			if acceptErr != nil {
				return
			}
			dials.Add(1)
			_ = conn.Close()
		}
	}()
	return ln.Addr().String(), &dials
}

func runtimeEnv(t *testing.T, ports listenPorts, extra map[string]string) []string {
	t.Helper()
	if _, ok := extra["ORT_LIB_PATH"]; !ok {
		extra["ORT_LIB_PATH"] = os.Getenv("ORT_LIB_PATH")
	}
	return processEnv(t, ports, extra)
}

func copyAddModel(t *testing.T, modelsRoot string) {
	t.Helper()
	copyTree(t, filepath.Join(moduleRoot(t), "testdata", "models", "add"), filepath.Join(modelsRoot, "add"))
}

func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		from := filepath.Join(src, entry.Name())
		to := filepath.Join(dst, entry.Name())
		if entry.IsDir() {
			copyTree(t, from, to)
			continue
		}
		data, readErr := os.ReadFile(from)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if writeErr := os.WriteFile(to, data, 0o644); writeErr != nil {
			t.Fatal(writeErr)
		}
	}
}

func writeGarbageModel(t *testing.T, modelsRoot, name string) {
	t.Helper()
	dir := filepath.Join(modelsRoot, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "model.onnx"), []byte("not-an-onnx-model"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func stopProcess(t *testing.T, proc *running) {
	t.Helper()
	if err := proc.signal(syscall.SIGTERM); err != nil {
		t.Fatalf("signal: %v\n%s", err, proc.output())
	}
	if err := proc.waitTimeout(t, 15*time.Second); err != nil {
		t.Fatalf("exit after signal: %v\n%s", err, proc.output())
	}
}

var (
	runtimeOnce sync.Once
	runtimeErr  error
)

func requireRuntime(t *testing.T) {
	t.Helper()
	runtimeOnce.Do(func() {
		if os.Getenv("ORT_LIB_PATH") == "" {
			runtimeErr = errors.New("ORT_LIB_PATH is unset")
			return
		}
		if err := ort.Init(); err != nil {
			runtimeErr = err
			return
		}
		version, err := ort.GetVersion()
		if err != nil {
			runtimeErr = err
			return
		}
		if version != "1.31.0" {
			runtimeErr = fmt.Errorf("onnx runtime version %q", version)
		}
	})
	if runtimeErr != nil {
		t.Fatal(runtimeErr)
	}
}

var (
	cgoBinaryOnce sync.Once
	cgoBinaryPath string
	cgoBinaryErr  error
)

func cgoInferenceBinary(t *testing.T) string {
	t.Helper()
	cgoBinaryOnce.Do(func() {
		dir, err := os.MkdirTemp("", "inference-cgo-")
		if err != nil {
			cgoBinaryErr = err
			return
		}
		cgoBinaryPath = filepath.Join(dir, "inference")
		cmd := exec.Command("go", "build", "-o", cgoBinaryPath, ".")
		cmd.Env = cgoEnv(os.Environ())
		out, err := cmd.CombinedOutput()
		if err != nil {
			cgoBinaryErr = fmt.Errorf("build: %w\n%s", err, out)
		}
	})
	if cgoBinaryErr != nil {
		t.Fatal(cgoBinaryErr)
	}
	return cgoBinaryPath
}

func cgoEnv(env []string) []string {
	filtered := make([]string, 0, len(env)+1)
	for _, entry := range env {
		if strings.HasPrefix(entry, "CGO_ENABLED=") {
			continue
		}
		filtered = append(filtered, entry)
	}
	return append(filtered, "CGO_ENABLED=1")
}

func startCgoProcess(t *testing.T, env []string) *running {
	t.Helper()
	cmd := exec.Command(cgoInferenceBinary(t))
	cmd.Env = env
	proc := &running{cmd: cmd, waited: make(chan struct{})}
	cmd.Stdout = &proc.stdout
	cmd.Stderr = &proc.stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	return proc
}
