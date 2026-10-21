package main

import (
	"bytes"
	"context"
	"encoding/json"
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
)

func TestRoutesWhenNATSIsUnset(t *testing.T) {
	t.Parallel()
	port := freePort(t)
	proc := startProcess(t, processEnv(t, port, nil))
	t.Cleanup(proc.kill)
	waitHealth(t, port)

	status, body := get(t, port, "/health")
	if status != http.StatusOK || len(body) != 0 {
		t.Fatalf("health %d %q", status, body)
	}
	status, body = get(t, port, "/")
	if status != http.StatusOK || !strings.Contains(string(body), "NATS is not configured.") || !strings.Contains(string(body), "No engines.") {
		t.Fatalf("root %d %s", status, body)
	}
	status, body = get(t, port, "/nodes")
	if status != http.StatusOK || !strings.Contains(string(body), "NATS is not configured.") || !strings.Contains(string(body), "<h1>Nodes</h1>") {
		t.Fatalf("nodes %d %s", status, body)
	}
	status, body = get(t, port, "/models")
	if status != http.StatusOK || !strings.Contains(string(body), "NATS is not configured.") {
		t.Fatalf("models %d %s", status, body)
	}
	status, body = get(t, port, "/engines/engine-a")
	if status != http.StatusOK || !strings.Contains(string(body), "This engine is unknown.") {
		t.Fatalf("engine %d %s", status, body)
	}

	for _, path := range []string{"/health", "/nodes", "/models", "/engines/engine-a", "/"} {
		status, _ = call(t, http.MethodPost, port, path, "bearer-token-6b1d0e4a9c", "tensor-body-9f3c")
		if status != http.StatusMethodNotAllowed {
			t.Fatalf("POST %s status %d", path, status)
		}
	}
	status, _ = call(t, http.MethodPut, port, "/nodes", "", "")
	if status != http.StatusMethodNotAllowed {
		t.Fatalf("PUT /nodes %d", status)
	}
	status, body = call(t, http.MethodPost, port, "/v2/models/wafer-defect/infer", "", `{"inputs":[1]}`)
	if status != http.StatusNotFound {
		t.Fatalf("infer %d %s", status, body)
	}
	status, body = get(t, port, "/metrics")
	if status != http.StatusNotFound {
		t.Fatalf("metrics %d %s", status, body)
	}
	status, body = get(t, port, "/v2")
	if status != http.StatusNotFound {
		t.Fatalf("v2 %d %s", status, body)
	}

	stdout := proc.stdout.String()
	if strings.Contains(stdout, "nats listener is up") || strings.Contains(stdout, "nats connected") || strings.Contains(stdout, "retrying") {
		t.Fatalf("dialed without NATS settings:\n%s", stdout)
	}
	if strings.Contains(stdout, "bearer-token-6b1d0e4a9c") || strings.Contains(stdout, "tensor-body-9f3c") {
		t.Fatalf("log contains a request secret:\n%s", stdout)
	}
	if strings.Contains(stdout, `"msg":"request"`) {
		t.Fatalf("info log has request timing:\n%s", stdout)
	}
	var listeners int
	for _, line := range decodeLog(t, stdout) {
		if strings.EqualFold(line.Level, "info") && line.Msg == "listener is up" {
			listeners++
		}
	}
	if listeners != 1 {
		t.Fatalf("listener lines %d:\n%s", listeners, stdout)
	}
	if strings.TrimSpace(proc.stderr.String()) != "" {
		t.Fatalf("stderr %s", proc.stderr.String())
	}
}

func TestEitherNATSVariableUnsetDoesNotDial(t *testing.T) {
	t.Parallel()
	srv := runPlainServer(t)
	creds := filepath.Join(t.TempDir(), "user.creds")
	writeFile(t, creds, []byte("unused-creds-body-4e8b"))

	cases := []struct {
		name  string
		url   string
		creds string
	}{
		{name: "url only", url: srv.ClientURL(), creds: ""},
		{name: "creds only", url: "", creds: creds},
		{name: "blank url", url: "  ", creds: creds},
		{name: "blank creds", url: srv.ClientURL(), creds: " \t "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			port := freePort(t)
			before := srv.NumClients()
			proc := startProcess(t, processEnv(t, port, map[string]string{
				"NATS_SERVER_URL": tc.url,
				"NATS_CREDS_PATH": tc.creds,
			}))
			t.Cleanup(proc.kill)
			waitHealth(t, port)
			body := getBody(t, port, "/models")
			if !strings.Contains(body, "NATS is not configured.") {
				t.Fatalf("body %s", body)
			}
			time.Sleep(200 * time.Millisecond)
			if srv.NumClients() != before {
				t.Fatalf("clients %d, was %d", srv.NumClients(), before)
			}
			out := proc.stdout.String()
			if strings.Contains(out, "nats listener is up") || strings.Contains(out, "retrying") || strings.Contains(out, "unused-creds-body-4e8b") {
				t.Fatalf("dialed:\n%s", out)
			}
		})
	}
}

func TestClosedPortStaysUp(t *testing.T) {
	t.Parallel()
	port := freePort(t)
	creds := filepath.Join(t.TempDir(), "user.creds")
	contents, userJWT, seed := newIdentity(t).credentials(t, nil, nil, nil)
	writeFile(t, creds, contents)
	proc := startProcess(t, processEnv(t, port, map[string]string{
		"NATS_SERVER_URL": "nats://" + refusedAddress(t),
		"NATS_CREDS_PATH": creds,
	}))
	t.Cleanup(proc.kill)
	waitHealth(t, port)
	body := getBody(t, port, "/")
	if !strings.Contains(body, "NATS is not connected.") || strings.Contains(body, "NATS is not configured.") {
		t.Fatalf("body %s", body)
	}
	status, health := get(t, port, "/health")
	if status != http.StatusOK || len(health) != 0 {
		t.Fatalf("health %d %q", status, health)
	}
	waitLog(t, proc, "nats connection attempt failed, retrying")
	if proc.exited() {
		t.Fatalf("exited\n%s", proc.output())
	}
	out := proc.stdout.String() + proc.stderr.String()
	if strings.Contains(out, userJWT) || strings.Contains(out, seed) || strings.Contains(out, "BEGIN NKEY") {
		t.Fatal("log contains credentials")
	}
}

func TestMissingCredentialsStayUp(t *testing.T) {
	t.Parallel()
	srv := runPlainServer(t)
	port := freePort(t)
	missing := filepath.Join(t.TempDir(), "missing.creds")
	proc := startProcess(t, processEnv(t, port, map[string]string{
		"NATS_SERVER_URL": srv.ClientURL(),
		"NATS_CREDS_PATH": missing,
	}))
	t.Cleanup(proc.kill)
	waitHealth(t, port)
	waitLog(t, proc, "nats credentials could not be read, retrying")
	body := getBody(t, port, "/nodes")
	if !strings.Contains(body, "NATS is not connected.") || strings.Contains(body, "NATS is not configured.") {
		t.Fatalf("body %s", body)
	}
	status, health := get(t, port, "/health")
	if status != http.StatusOK || len(health) != 0 {
		t.Fatalf("health %d %q", status, health)
	}
	if proc.exited() {
		t.Fatalf("exited\n%s", proc.output())
	}
	if srv.NumClients() != 0 {
		t.Fatalf("clients %d", srv.NumClients())
	}
}

func TestGarbageCredentialsAreNotLogged(t *testing.T) {
	t.Parallel()
	const garbage = "not-a-credentials-file-5e7b"
	srv := runPlainServer(t)
	port := freePort(t)
	creds := filepath.Join(t.TempDir(), "user.creds")
	writeFile(t, creds, []byte(garbage))
	proc := startProcess(t, processEnv(t, port, map[string]string{
		"NATS_SERVER_URL": srv.ClientURL(),
		"NATS_CREDS_PATH": creds,
	}))
	t.Cleanup(proc.kill)
	waitHealth(t, port)
	waitLog(t, proc, "nats credentials could not be read, retrying")
	out := proc.stdout.String() + proc.stderr.String()
	if strings.Contains(out, garbage) {
		t.Fatalf("log contains the credentials file:\n%s", out)
	}
	if proc.exited() {
		t.Fatalf("exited\n%s", proc.output())
	}
}

func TestDebugRequestTimingOmitsTheBody(t *testing.T) {
	t.Parallel()
	port := freePort(t)
	proc := startProcess(t, processEnv(t, port, map[string]string{"LOG_LEVEL": "debug"}))
	t.Cleanup(proc.kill)
	waitHealth(t, port)
	status, _ := call(t, http.MethodPost, port, "/health", "bearer-token-6b1d0e4a9c", "tensor-body-9f3c")
	if status != http.StatusMethodNotAllowed {
		t.Fatalf("status %d", status)
	}
	stdout := waitLog(t, proc, `"msg":"request"`)
	if strings.Contains(stdout, "bearer-token-6b1d0e4a9c") || strings.Contains(stdout, "tensor-body-9f3c") {
		t.Fatalf("log contains a request secret:\n%s", stdout)
	}
	var found bool
	for _, line := range decodeLog(t, stdout) {
		if strings.EqualFold(line.Level, "debug") && line.Msg == "request" && line.Path == "/health" && line.Duration > 0 {
			found = true
		}
	}
	if !found {
		t.Fatalf("debug timing missing:\n%s", stdout)
	}
}

func TestTakenPortExits(t *testing.T) {
	t.Parallel()
	port := freePort(t)
	ln, err := net.Listen("tcp", listenAddr(port))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	proc := startProcess(t, processEnv(t, port, nil))
	t.Cleanup(proc.kill)
	waitErr := proc.waitTimeout(t, 5*time.Second)
	var exitErr *exec.ExitError
	if !errors.As(waitErr, &exitErr) {
		t.Fatalf("exit: %v\n%s", waitErr, proc.output())
	}
	if strings.Contains(proc.stdout.String(), "listener is up") {
		t.Fatalf("listener started\n%s", proc.stdout.String())
	}
	lines := decodeLog(t, proc.stdout.String())
	if len(lines) == 0 || !strings.EqualFold(lines[0].Level, "error") {
		t.Fatalf("listen log %+v", lines)
	}
	if strings.TrimSpace(proc.stderr.String()) != "" {
		t.Fatalf("stderr %s", proc.stderr.String())
	}
}

func TestBadSettingsExit(t *testing.T) {
	t.Parallel()
	cases := []map[string]string{
		{"LOG_LEVEL": "trace"},
		{"TLS_ENABLED": "yes"},
		{"TLS_ENABLED": ""},
		{"PORT": "nope"},
		{"PORT": "0"},
		{"PORT": "65536"},
	}
	for _, extra := range cases {
		t.Run(fmt.Sprint(extra), func(t *testing.T) {
			t.Parallel()
			port := freePort(t)
			env := processEnv(t, port, extra)
			if _, ok := extra["PORT"]; ok {
				env = processEnv(t, 0, extra)
				proc := startProcess(t, env)
				t.Cleanup(proc.kill)
				err := proc.waitTimeout(t, 5*time.Second)
				var exitErr *exec.ExitError
				if !errors.As(err, &exitErr) {
					t.Fatalf("exit: %v\n%s", err, proc.output())
				}
				if strings.Contains(proc.stdout.String(), "listener is up") {
					t.Fatalf("listener started\n%s", proc.stdout.String())
				}
				return
			}
			proc := requireExit(t, env, port)
			lines := decodeLog(t, proc.stdout.String())
			if len(lines) == 0 || !strings.EqualFold(lines[0].Level, "error") {
				t.Fatalf("log %+v", lines)
			}
		})
	}
}

func TestEngineConfigFileIsNotOpened(t *testing.T) {
	t.Parallel()
	port := freePort(t)
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	proc := startProcess(t, processEnv(t, port, map[string]string{
		"CONFIG_PATH": path,
		"LOG_LEVEL":   "",
	}))
	t.Cleanup(proc.kill)
	waitHealth(t, port)
	stdout := proc.stdout.String()
	var info bool
	for _, line := range decodeLog(t, stdout) {
		if strings.EqualFold(line.Level, "info") && line.Msg == "listener is up" {
			info = true
		}
	}
	if !info {
		t.Fatalf("listener log:\n%s", stdout)
	}
}

func TestDependencies(t *testing.T) {
	t.Parallel()
	cmd := exec.Command("go", "list", "-deps", "./cmd/dashboard")
	cmd.Dir = moduleRoot(t)
	cmd.Env = withoutCGO(os.Environ())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	var client bool
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "github.com/nats-io/nats.go" {
			client = true
		}
		if strings.HasPrefix(line, "github.com/prometheus/client_golang") {
			t.Errorf("prometheus dependency %s", line)
		}
		if strings.Contains(strings.ToLower(line), "onnxruntime") {
			t.Errorf("onnx runtime dependency %s", line)
		}
		if strings.HasPrefix(line, "github.com/nats-io/nats-server") {
			t.Errorf("the process links the NATS server %s", line)
		}
		if line == "github.com/eclipse-iofog/ai-inference-onnx" || strings.HasPrefix(line, "github.com/eclipse-iofog/ai-inference-onnx/") && !strings.HasPrefix(line, "github.com/eclipse-iofog/ai-inference-onnx/dashboard") {
			t.Errorf("engine module dependency %s", line)
		}
	}
	if !client {
		t.Fatal("the process does not link github.com/nats-io/nats.go")
	}
}

type logRecord struct {
	Level    string `json:"level"`
	Msg      string `json:"msg"`
	Path     string `json:"path"`
	Duration int64  `json:"duration_us"`
}

func decodeLog(t *testing.T, out string) []logRecord {
	t.Helper()
	var lines []logRecord
	for _, raw := range strings.Split(out, "\n") {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		var line logRecord
		if err := json.Unmarshal([]byte(raw), &line); err != nil {
			t.Fatalf("log line %q: %v", raw, err)
		}
		lines = append(lines, line)
	}
	return lines
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	tcpAddr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		_ = ln.Close()
		t.Fatalf("address %T", ln.Addr())
	}
	port := tcpAddr.Port
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}

func refusedAddress(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	return addr
}

func processEnv(t *testing.T, port int, extra map[string]string) []string {
	t.Helper()
	values := map[string]string{}
	if port > 0 {
		values["PORT"] = strconv.Itoa(port)
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

func requireExit(t *testing.T, env []string, port int) *running {
	t.Helper()
	addr := hostPort(port)
	accepted := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		for ctx.Err() == nil {
			conn, err := net.DialTimeout("tcp", addr, 20*time.Millisecond)
			if err != nil {
				continue
			}
			_ = conn.Close()
			select {
			case accepted <- struct{}{}:
			default:
			}
			return
		}
	}()
	proc := startProcess(t, env)
	t.Cleanup(proc.kill)
	err := proc.waitTimeout(t, 5*time.Second)
	cancel()
	select {
	case <-accepted:
		t.Fatalf("accepted a connection on %s\n%s", addr, proc.output())
	default:
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("exit: %v\n%s", err, proc.output())
	}
	return proc
}

func waitHealth(t *testing.T, port int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var last error
	for time.Now().Before(deadline) {
		status, body, err := getErr(port, "/health")
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
	t.Fatalf("health: %v", last)
}

func waitLog(t *testing.T, proc *running, contains string) string {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		out := proc.stdout.String()
		if strings.Contains(out, contains) {
			return out
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("log missing %q\nstdout %s\nstderr %s", contains, proc.stdout.String(), proc.stderr.String())
	return ""
}

func getBody(t *testing.T, port int, path string) string {
	t.Helper()
	status, body := get(t, port, path)
	if status != http.StatusOK {
		t.Fatalf("GET %s %d %s", path, status, body)
	}
	return string(body)
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
	resp, err := httpClient.Do(req)
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

func call(t *testing.T, method string, port int, path, token, body string) (int, []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, fmt.Sprintf("http://127.0.0.1:%d%s", port, path), strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, payload
}

var httpClient = &http.Client{
	Timeout: 2 * time.Second,
	Transport: &http.Transport{
		DisableKeepAlives: true,
	},
}

func hostPort(port int) string {
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
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

var (
	binaryOnce sync.Once
	binaryPath string
	binaryErr  error
)

func dashboardBinary(t *testing.T) string {
	t.Helper()
	binaryOnce.Do(func() {
		dir, err := os.MkdirTemp("", "dashboard-process-")
		if err != nil {
			binaryErr = err
			return
		}
		binaryPath = filepath.Join(dir, "dashboard")
		cmd := exec.Command("go", "build", "-o", binaryPath, "./cmd/dashboard")
		cmd.Dir = moduleRoot(t)
		cmd.Env = withoutCGO(os.Environ())
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

type running struct {
	cmd    *exec.Cmd
	stdout lockedBuf
	stderr lockedBuf
	once   sync.Once
	err    error
	waited chan struct{}
}

func startProcess(t *testing.T, env []string) *running {
	t.Helper()
	cmd := exec.Command(dashboardBinary(t))
	cmd.Env = env
	proc := &running{cmd: cmd, waited: make(chan struct{})}
	cmd.Stdout = &proc.stdout
	cmd.Stderr = &proc.stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	return proc
}

func (p *running) output() string {
	return p.stdout.String() + p.stderr.String()
}

func (p *running) exited() bool {
	select {
	case <-p.waited:
		return true
	default:
		return false
	}
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
