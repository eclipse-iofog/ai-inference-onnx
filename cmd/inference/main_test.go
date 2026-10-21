package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"github.com/eclipse-iofog/ai-inference-onnx/api/openinference/v2"
	"github.com/eclipse-iofog/ai-inference-onnx/internal/auth"
)

const shortToken = "short-token-6b1d0e4a9c"

func TestDisabledMissingDirectoriesServesLiveNotReady(t *testing.T) {
	t.Parallel()
	ports := pickPorts(t)
	models := filepath.Join(t.TempDir(), "models")
	knowledge := filepath.Join(t.TempDir(), "knowledge")
	requireAbsent(t, models)
	requireAbsent(t, knowledge)

	proc := startProcess(t, processEnv(t, ports, map[string]string{
		"AUTH_MODE":      "disabled",
		"MODELS_PATH":    models,
		"KNOWLEDGE_PATH": knowledge,
	}))
	t.Cleanup(proc.kill)

	waitLive(t, ports.http)
	assertGET(t, ports.http, "/v2/health/live", http.StatusOK)
	assertGET(t, ports.http, "/v2/health/ready", http.StatusBadRequest)
	assertGRPCHealth(t, ports.grpc, false)
	assertMetricsOpen(t, ports.metrics)
	assertAccepts(t, hostPort(ports.grpc))

	if err := proc.signal(syscall.SIGTERM); err != nil {
		t.Fatalf("signal: %v", err)
	}
	if err := proc.waitTimeout(t, 5*time.Second); err != nil {
		t.Fatalf("exit after signal: %v\n%s", err, proc.output())
	}
}

func TestEmptyRepositoryReadRoutes(t *testing.T) {
	t.Parallel()
	ports := pickPorts(t)
	proc := startProcess(t, processEnv(t, ports, map[string]string{
		"AUTH_MODE": "disabled",
	}))
	t.Cleanup(proc.kill)
	waitLive(t, ports.http)

	status, body := get(t, ports.http, "/v2")
	if status != http.StatusOK || string(body) != `{"name":"inference","version":"dev","extensions":["binary_tensor_data","model_repository","parameters","schedule_policy"]}` {
		t.Fatalf("metadata %d %s", status, body)
	}
	status, body = get(t, ports.http, "/v2/models")
	if status != http.StatusOK || string(body) != `{"models":[]}` {
		t.Fatalf("list %d %s", status, body)
	}
	status, body = post(t, ports.http, "/v2/repository/index", `{}`)
	if status != http.StatusOK || string(body) != "[]" {
		t.Fatalf("index %d %s", status, body)
	}
	status, body = post(t, ports.http, "/v2/models/add/infer", "")
	if status != http.StatusNotFound || string(body) != `{"error":"model \"add\" was not found"}` {
		t.Fatalf("infer %d %s", status, body)
	}
	status, body = post(t, ports.http, "/v2/models/add/versions/1/infer", "")
	if status != http.StatusNotFound || string(body) != `{"error":"model \"add\" was not found"}` {
		t.Fatalf("versioned infer %d %s", status, body)
	}
	status, body = post(t, ports.http, "/v2/repository/models/missing/load", "")
	if status != http.StatusNotImplemented || string(body) != `{"error":"loading and unloading models is not supported"}` {
		t.Fatalf("load %d %s", status, body)
	}
	status, body = post(t, ports.http, "/v2/repository/models/missing/unload", "")
	if status != http.StatusNotImplemented || string(body) != `{"error":"loading and unloading models is not supported"}` {
		t.Fatalf("unload %d %s", status, body)
	}
	assertGRPCInferMissing(t, ports.grpc)
	assertGET(t, ports.http, "/v2/health/live", http.StatusOK)
}

func TestAuthModeUnsetExitsBeforeListen(t *testing.T) {
	t.Parallel()
	ports := pickPorts(t)
	proc := requireExitWithoutListen(t, processEnv(t, ports, nil), ports)
	lines := decodeLog(t, proc.stdout.String())
	if len(lines) == 0 || !strings.EqualFold(lines[0].Level, "error") {
		t.Fatalf("configuration log %+v", lines)
	}
	if strings.TrimSpace(proc.stderr.String()) != "" {
		t.Fatalf("stderr %s", proc.stderr.String())
	}
}

func TestEdgeletMissingKeyExitsBeforeListen(t *testing.T) {
	t.Parallel()
	if _, err := os.Stat(auth.PublicJWKPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("signing key %s must be absent: %v", auth.PublicJWKPath, err)
	}
	ports := pickPorts(t)
	requireExitWithoutListen(t, processEnv(t, ports, map[string]string{
		"AUTH_MODE": "edgelet",
	}), ports)
}

func TestStaticShortTokenExitsWithoutLoggingToken(t *testing.T) {
	t.Parallel()
	if len(shortToken) >= 32 {
		t.Fatalf("token length %d", len(shortToken))
	}
	ports := pickPorts(t)
	proc := requireExitWithoutListen(t, processEnv(t, ports, map[string]string{
		"AUTH_MODE":         "static",
		"AUTH_STATIC_TOKEN": shortToken,
	}), ports)
	out := proc.stdout.String() + proc.stderr.String()
	if strings.Contains(out, shortToken) {
		t.Fatalf("logs contain the token:\n%s", out)
	}
	lines := decodeLog(t, proc.stdout.String())
	if len(lines) == 0 || !strings.EqualFold(lines[0].Level, "error") {
		t.Fatalf("configuration log %+v", lines)
	}
	if strings.TrimSpace(proc.stderr.String()) != "" {
		t.Fatalf("stderr %s", proc.stderr.String())
	}
}

func TestStaticTokenAbsentFromLiveLog(t *testing.T) {
	t.Parallel()
	const token = "live-token-7c2e91ab44d06f83e1a9b0c5"
	if len(token) < 32 {
		t.Fatalf("token length %d", len(token))
	}
	ports := pickPorts(t)
	proc := startProcess(t, processEnv(t, ports, map[string]string{
		"AUTH_MODE":         "static",
		"AUTH_STATIC_TOKEN": token,
	}))
	t.Cleanup(proc.kill)

	waitLive(t, ports.http)
	status, body := getAuth(t, ports.http, "/v2/health/live", token)
	if status != http.StatusOK || len(body) != 0 {
		t.Fatalf("live status %d body %q", status, body)
	}
	stdout := waitLog(t, proc, "grpc listener is up")
	if strings.Contains(stdout, token) || strings.Contains(proc.stderr.String(), token) {
		t.Fatalf("logs contain the token:\n%s%s", stdout, proc.stderr.String())
	}
	if strings.TrimSpace(proc.stderr.String()) != "" {
		t.Fatalf("stderr %s", proc.stderr.String())
	}
	var listeners int
	for _, line := range decodeLog(t, stdout) {
		if strings.EqualFold(line.Level, "info") && strings.Contains(line.Msg, "listener is up") {
			listeners++
		}
	}
	if listeners < 2 {
		t.Fatalf("listener lines %d:\n%s", listeners, stdout)
	}
}

func TestDependenciesIncludeNATSExcludeRuntime(t *testing.T) {
	t.Parallel()
	cmd := exec.Command("go", "list", "-deps", "./cmd/inference")
	cmd.Dir = moduleRoot(t)
	cmd.Env = withoutCGO(os.Environ())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	var client, prometheus bool
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "github.com/nats-io/nats.go" {
			client = true
		}
		if strings.HasPrefix(line, "github.com/prometheus/client_golang") {
			prometheus = true
		}
		if strings.Contains(strings.ToLower(line), "onnxruntime") {
			t.Errorf("unexpected dependency %s", line)
		}
		if strings.HasPrefix(line, "github.com/nats-io/nats-server") {
			t.Errorf("the process links the NATS server %s", line)
		}
	}
	if !client {
		t.Error("the process does not link github.com/nats-io/nats.go")
	}
	if !prometheus {
		t.Error("the process does not link github.com/prometheus/client_golang")
	}
}

type listenPorts struct {
	http    int
	grpc    int
	metrics int
}

func pickPorts(t *testing.T) listenPorts {
	t.Helper()
	used := map[int]struct{}{}
	next := func() int {
		t.Helper()
		for range 20 {
			port := freePort(t)
			if _, ok := used[port]; ok {
				continue
			}
			used[port] = struct{}{}
			return port
		}
		t.Fatal("could not reserve a port")
		return 0
	}
	return listenPorts{http: next(), grpc: next(), metrics: next()}
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	tcpAddr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		_ = ln.Close()
		t.Fatalf("address %T", ln.Addr())
	}
	port := tcpAddr.Port
	if err := ln.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if port == 8000 || port == 8001 || port == 8002 {
		return freePort(t)
	}
	return port
}

func processEnv(t *testing.T, ports listenPorts, extra map[string]string) []string {
	t.Helper()
	values := map[string]string{
		"CONFIG_PATH":    filepath.Join(t.TempDir(), "missing.yaml"),
		"MODELS_PATH":    filepath.Join(t.TempDir(), "models"),
		"KNOWLEDGE_PATH": filepath.Join(t.TempDir(), "knowledge"),
		"HTTP_PORT":      strconv.Itoa(ports.http),
		"GRPC_PORT":      strconv.Itoa(ports.grpc),
		"METRICS_PORT":   strconv.Itoa(ports.metrics),
	}
	for key, value := range extra {
		values[key] = value
	}
	env := make([]string, 0, len(values))
	for key, value := range values {
		env = append(env, key+"="+value)
	}
	return env
}

func requireAbsent(t *testing.T, path string) {
	t.Helper()
	_, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	t.Fatalf("%s exists", path)
}

func requireExitWithoutListen(t *testing.T, env []string, ports listenPorts) *running {
	t.Helper()
	httpAddr := hostPort(ports.http)
	grpcAddr := hostPort(ports.grpc)
	accepted := make(chan string, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go watchPorts(ctx, accepted, httpAddr, grpcAddr)

	proc := startProcess(t, env)
	t.Cleanup(proc.kill)
	err := proc.waitTimeout(t, 5*time.Second)
	cancel()

	select {
	case addr := <-accepted:
		t.Fatalf("accepted a connection on %s\n%s", addr, proc.output())
	default:
	}
	assertRefused(t, httpAddr)
	assertRefused(t, grpcAddr)

	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("exit: %v\n%s", err, proc.output())
	}
	return proc
}

func watchPorts(ctx context.Context, accepted chan<- string, addrs ...string) {
	for ctx.Err() == nil {
		for _, addr := range addrs {
			conn, err := net.DialTimeout("tcp", addr, 20*time.Millisecond)
			if err != nil {
				continue
			}
			_ = conn.Close()
			select {
			case accepted <- addr:
			default:
			}
			return
		}
	}
}

func assertGET(t *testing.T, port int, path string, status int) {
	t.Helper()
	got, body := get(t, port, path)
	if got != status {
		t.Fatalf("%s status %d, want %d, body %q", path, got, status, body)
	}
	if len(body) != 0 {
		t.Fatalf("%s body %q", path, body)
	}
}

func waitLive(t *testing.T, port int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var last error
	for time.Now().Before(deadline) {
		status, body, err := getErr(port, "/v2/health/live")
		if err == nil && status == http.StatusOK && len(body) == 0 {
			return
		}
		if err != nil {
			last = err
		} else {
			last = fmt.Errorf("status %d body %q", status, body)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("live: %v", last)
}

func getAuth(t *testing.T, port int, path, token string) (int, []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d%s", port, path), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := healthClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, body
}

func waitLog(t *testing.T, proc *running, contains string) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		out := proc.stdout.String()
		if strings.Contains(out, contains) {
			return out
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("log missing %q\nstdout %s\nstderr %s", contains, proc.stdout.String(), proc.stderr.String())
	return ""
}

func post(t *testing.T, port int, path, body string) (int, []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("http://127.0.0.1:%d%s", port, path), strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
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
	return resp.StatusCode, payload
}

func get(t *testing.T, port int, path string) (int, []byte) {
	t.Helper()
	status, body, err := getErr(port, path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return status, body
}

func getErr(port int, path string) (int, []byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d%s", port, path), nil)
	if err != nil {
		return 0, nil, err
	}
	resp, err := healthClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, err
	}
	return resp.StatusCode, body, nil
}

var healthClient = &http.Client{
	Timeout: 2 * time.Second,
	Transport: &http.Transport{
		DisableKeepAlives: true,
	},
}

func assertGRPCInferMissing(t *testing.T, port int) {
	t.Helper()
	conn, err := grpc.NewClient(hostPort(port), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial grpc: %v", err)
	}
	defer func() {
		if err := conn.Close(); err != nil {
			t.Errorf("close grpc: %v", err)
		}
	}()
	client := openinference.NewGRPCInferenceServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = client.ModelInfer(ctx, &openinference.ModelInferRequest{ModelName: "add"})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("infer %v", err)
	}
	_, err = client.RepositoryModelLoad(ctx, &openinference.RepositoryModelLoadRequest{ModelName: "add"})
	if status.Code(err) != codes.Unimplemented {
		t.Fatalf("load %v", err)
	}
	_, err = client.RepositoryModelUnload(ctx, &openinference.RepositoryModelUnloadRequest{ModelName: "add"})
	if status.Code(err) != codes.Unimplemented {
		t.Fatalf("unload %v", err)
	}
}

func assertGRPCHealth(t *testing.T, port int, readyWant bool) {
	t.Helper()
	conn, err := grpc.NewClient(
		hostPort(port),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial grpc: %v", err)
	}
	defer func() {
		if err := conn.Close(); err != nil {
			t.Errorf("close grpc: %v", err)
		}
	}()
	client := openinference.NewGRPCInferenceServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	live, err := client.ServerLive(ctx, &openinference.ServerLiveRequest{})
	if err != nil {
		t.Fatalf("server live: %v", err)
	}
	if !live.GetLive() {
		t.Fatal("server live is false")
	}
	ready, err := client.ServerReady(ctx, &openinference.ServerReadyRequest{})
	if err != nil {
		t.Fatalf("server ready: %v", err)
	}
	if ready.GetReady() != readyWant {
		t.Fatalf("server ready %v", ready.GetReady())
	}
}

func assertMetricsOpen(t *testing.T, port int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var last error
	for time.Now().Before(deadline) {
		status, body, err := getErr(port, "/metrics")
		if err == nil && status == http.StatusOK && expositionReady(body) {
			return
		}
		if err != nil {
			last = err
		} else {
			last = fmt.Errorf("status %d body %q", status, body)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("metrics: %v", last)
}

func expositionReady(body []byte) bool {
	text := string(body)
	for _, line := range []string{
		"# TYPE inference_request_success counter",
		"# TYPE inference_request_failure counter",
		"# TYPE inference_count counter",
		"# TYPE inference_exec_count counter",
		"# TYPE inference_pending_request_count gauge",
		"# TYPE inference_request_duration_us counter",
		"# TYPE inference_queue_duration_us counter",
		"# TYPE inference_compute_infer_duration_us counter",
		"# TYPE inference_model_ready gauge",
	} {
		if !strings.Contains(text, line) {
			return false
		}
	}
	return strings.Contains(text, "process_resident_memory_bytes")
}

func assertRefused(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 50*time.Millisecond)
		if err == nil {
			if closeErr := conn.Close(); closeErr != nil {
				t.Errorf("close %s: %v", addr, closeErr)
			}
			t.Fatalf("%s accepted a connection", addr)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func assertAccepts(t *testing.T, addr string) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("close %s: %v", addr, err)
	}
}

func hostPort(port int) string {
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		info, err := os.Stat(filepath.Join(dir, "go.mod"))
		if err == nil && !info.IsDir() {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}

var (
	binaryOnce sync.Once
	binaryPath string
	binaryErr  error
)

func inferenceBinary(t *testing.T) string {
	t.Helper()
	binaryOnce.Do(func() {
		dir, err := os.MkdirTemp("", "inference-process-")
		if err != nil {
			binaryErr = err
			return
		}
		binaryPath = filepath.Join(dir, "inference")
		env := withoutCGO(os.Environ())
		envCmd := exec.Command("go", "env", "CGO_ENABLED")
		envCmd.Env = env
		got, err := envCmd.Output()
		if err != nil {
			binaryErr = fmt.Errorf("go env CGO_ENABLED: %w", err)
			return
		}
		if strings.TrimSpace(string(got)) != "0" {
			binaryErr = fmt.Errorf("CGO_ENABLED=%q", strings.TrimSpace(string(got)))
			return
		}
		cmd := exec.Command("go", "build", "-o", binaryPath, ".")
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil {
			binaryErr = fmt.Errorf("build: %w\n%s", err, out)
		}
	})
	if binaryErr != nil {
		t.Fatal(binaryErr)
	}
	return binaryPath
}

func withoutCGO(env []string) []string {
	filtered := make([]string, 0, len(env)+1)
	for _, entry := range env {
		if strings.HasPrefix(entry, "CGO_ENABLED=") {
			continue
		}
		filtered = append(filtered, entry)
	}
	return append(filtered, "CGO_ENABLED=0")
}

type running struct {
	cmd    *exec.Cmd
	out    lockedBuf
	stdout lockedBuf
	stderr lockedBuf
	once   sync.Once
	err    error
	waited chan struct{}
}

func startProcess(t *testing.T, env []string) *running {
	t.Helper()
	cmd := exec.Command(inferenceBinary(t))
	cmd.Env = env
	proc := &running{cmd: cmd, waited: make(chan struct{})}
	cmd.Stdout = &proc.stdout
	cmd.Stderr = &proc.stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	return proc
}

func (p *running) signal(sig os.Signal) error {
	return p.cmd.Process.Signal(sig)
}

func (p *running) output() string {
	return p.out.String() + p.stdout.String() + p.stderr.String()
}

func (p *running) wait() error {
	p.once.Do(func() {
		p.err = p.cmd.Wait()
		close(p.waited)
	})
	return p.err
}

func (p *running) kill() {
	if p.cmd.Process == nil {
		return
	}
	select {
	case <-p.waited:
		return
	default:
	}
	_ = p.cmd.Process.Kill()
	_ = p.wait()
}

func (p *running) waitTimeout(t *testing.T, d time.Duration) error {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		done <- p.wait()
	}()
	select {
	case err := <-done:
		return err
	case <-time.After(d):
		p.kill()
		t.Fatalf("timed out\n%s", p.output())
	}
	return nil
}

type lockedBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuf) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuf) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
