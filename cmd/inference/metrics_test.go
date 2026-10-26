package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

const metricsToken = "metrics-scrape-7c2e91ab44d06f83e1a9b0c5"

func TestMetricsScrape(t *testing.T) {
	t.Parallel()
	if len(metricsToken) < 32 {
		t.Fatalf("token length %d", len(metricsToken))
	}
	ports := pickPorts(t)
	proc := startProcess(t, processEnv(t, ports, map[string]string{
		"AUTH_MODE":         "static",
		"AUTH_STATIC_TOKEN": metricsToken,
	}))
	t.Cleanup(proc.kill)

	waitLive(t, ports.http)
	assertGET(t, ports.http, "/v2/health/live", http.StatusOK)
	assertGET(t, ports.http, "/v2/health/ready", http.StatusBadRequest)
	assertMetricsOpen(t, ports.metrics)

	status, body := get(t, ports.metrics, "/metrics")
	if status != http.StatusOK || !expositionReady(body) {
		t.Fatalf("scrape %d %s", status, body)
	}
	status, body = callMethod(t, http.MethodGet, ports.metrics, "/metrics", "Bearer not-the-token")
	if status != http.StatusOK || !expositionReady(body) {
		t.Fatalf("bearer scrape %d %s", status, body)
	}
	status, body = callMethod(t, http.MethodPost, ports.metrics, "/metrics", "")
	if status != http.StatusMethodNotAllowed {
		t.Fatalf("POST /metrics %d %s", status, body)
	}
	status, body = get(t, ports.metrics, "/health")
	if status != http.StatusNotFound {
		t.Fatalf("GET /health %d %s", status, body)
	}

	status, body = get(t, ports.http, "/metrics")
	if status != http.StatusNotFound {
		t.Fatalf("http /metrics %d %s", status, body)
	}
	if strings.Contains(string(body), "inference_request_success") {
		t.Fatalf("http port served the scrape:\n%s", body)
	}

	stdout := waitLog(t, proc, `"transport":"metrics"`)
	var metricsUp bool
	for _, line := range decodeLog(t, stdout) {
		if strings.Contains(strings.ToLower(line.Msg), "nats") {
			t.Fatalf("nats log line %+v", line)
		}
		if line.Msg == "listener" && line.Transport == "metrics" {
			if !strings.EqualFold(line.Level, "info") {
				t.Fatalf("metrics listener line %+v", line)
			}
			metricsUp = true
		}
	}
	if !metricsUp {
		t.Fatalf("metrics listener was not logged\n%s", stdout)
	}
	out := stdout + proc.stderr.String()
	if strings.Contains(out, metricsToken) || strings.Contains(out, "not-the-token") {
		t.Fatal("log contains a token")
	}
	if strings.TrimSpace(proc.stderr.String()) != "" {
		t.Fatalf("stderr %s", proc.stderr.String())
	}
}

func TestMetricsStaysPlainWhenTLSEnabled(t *testing.T) {
	t.Parallel()
	ports := pickPorts(t)
	server := mintCert(t, nil, certKindServer)
	certPath, keyPath := writePair(t, server)
	proc := startProcess(t, processEnv(t, ports, map[string]string{
		"AUTH_MODE":   "disabled",
		"TLS_ENABLED": "true",
		"TLS_CERT":    certPath,
		"TLS_KEY":     keyPath,
	}))
	t.Cleanup(proc.kill)

	waitHTTPS(t, ports.http, server.cert, nil, true)
	assertMetricsOpen(t, ports.metrics)
	assertNotTLS(t, ports.metrics)
	if _, _, _, err := httpsGetErr(ports.metrics, server.cert, nil, true, "/metrics"); err == nil {
		t.Fatal("metrics presented a certificate")
	}
	out := proc.stdout.String() + proc.stderr.String()
	assertNoKey(t, out, server)
}

func TestMetricsPortTakenExits(t *testing.T) {
	t.Parallel()
	const token = "metrics-taken-6f1c8a04b7e2d953aa10c4e8"
	if len(token) < 32 {
		t.Fatalf("token length %d", len(token))
	}
	ports := pickPorts(t)
	ln, err := net.Listen("tcp", net.JoinHostPort("", strconv.Itoa(ports.metrics)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	proc := startProcess(t, processEnv(t, ports, map[string]string{
		"AUTH_MODE":         "static",
		"AUTH_STATIC_TOKEN": token,
	}))
	t.Cleanup(proc.kill)
	waitErr := proc.waitTimeout(t, 5*time.Second)
	var exitErr *exec.ExitError
	if !errors.As(waitErr, &exitErr) {
		t.Fatalf("exit: %v\n%s", waitErr, proc.output())
	}
	out := proc.stdout.String() + proc.stderr.String()
	if strings.Contains(out, token) {
		t.Fatalf("logs contain the token:\n%s", out)
	}
	if strings.Contains(out, "listener is up") {
		t.Fatalf("listener started\n%s", out)
	}
	lines := decodeLog(t, proc.stdout.String())
	var listenError bool
	for _, line := range lines {
		if strings.EqualFold(line.Level, "error") && line.Msg == "listen" {
			listenError = true
		}
	}
	if !listenError {
		t.Fatalf("listen log %+v", lines)
	}
	if strings.TrimSpace(proc.stderr.String()) != "" {
		t.Fatalf("stderr %s", proc.stderr.String())
	}
	assertRefused(t, hostPort(ports.http))
	assertRefused(t, hostPort(ports.grpc))
}

func callMethod(t *testing.T, method string, port int, path, authorization string) (int, []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, fmt.Sprintf("http://127.0.0.1:%d%s", port, path), nil)
	if err != nil {
		t.Fatal(err)
	}
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	resp, err := healthClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, body
}
