package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/eclipse-iofog/ai-inference-onnx/internal/config"
	"github.com/eclipse-iofog/ai-inference-onnx/internal/session"
)

type logRecord struct {
	Level     string   `json:"level"`
	Msg       string   `json:"msg"`
	State     string   `json:"state"`
	Reason    string   `json:"reason"`
	Transport string   `json:"transport"`
	Providers []string `json:"providers"`
}

func TestModelStateLines(t *testing.T) {
	var buf bytes.Buffer
	logger := newLogger(&buf, "info")
	logModelState(logger, "add", session.Ready, nil)
	logModelState(logger, "add", session.Loading, nil)
	logModelState(logger, "broken", session.Unavailable, []session.CatalogModel{{
		Name:   "broken",
		Reason: "model file is missing",
	}})
	logger.Debug("request timing")

	lines := decodeLog(t, buf.String())
	if len(lines) != 3 {
		t.Fatalf("lines %d: %s", len(lines), buf.String())
	}
	if !strings.EqualFold(lines[0].Level, "info") || lines[0].Msg != "model" || lines[0].State != string(session.Ready) {
		t.Fatalf("ready line %+v", lines[0])
	}
	if !strings.EqualFold(lines[1].Level, "info") || lines[1].Msg != "model" || lines[1].State != string(session.Loading) {
		t.Fatalf("load line %+v", lines[1])
	}
	if !strings.EqualFold(lines[2].Level, "warn") || lines[2].Msg != "model" || lines[2].State != string(session.Unavailable) || lines[2].Reason != "model file is missing" {
		t.Fatalf("unavailable line %+v", lines[2])
	}
	if strings.Contains(strings.ToLower(buf.String()), "debug") || strings.Contains(buf.String(), "request timing") {
		t.Fatalf("debug line present: %s", buf.String())
	}
}

func TestServerErrorLogIsJSON(t *testing.T) {
	var buf bytes.Buffer
	logger := newLogger(&buf, "info")
	httpErrorLog(logger).Print("read header timeout")
	lines := decodeLog(t, buf.String())
	if len(lines) != 1 || !strings.EqualFold(lines[0].Level, "error") || !strings.Contains(lines[0].Msg, "read header timeout") {
		t.Fatalf("error log %+v raw %s", lines, buf.String())
	}
}

func TestStartLineOmitsSecrets(t *testing.T) {
	var buf bytes.Buffer
	logger := newLogger(&buf, "info").With("service", serviceName, "server_id", "engine-1")
	const token = "live-token-7c2e91ab44d06f83e1a9b0c5"
	logStart(logger, config.Config{
		AuthMode:        config.AuthStatic,
		AuthStaticToken: token,
		HTTPPort:        8000,
		GRPCPort:        8001,
		MetricsPort:     8002,
		TLSEnabled:      true,
		TLSCert:         "/run/secrets/tls-cert-marker",
		TLSKey:          "/run/secrets/tls-key-marker",
		NATSServerURL:   "nats://user:nats-password-marker@127.0.0.1:4222",
		ModelsPath:      "/models",
		KnowledgePath:   "/knowledge",
		MetricsInterval: 10 * time.Second,
	})
	out := buf.String()
	for _, secret := range []string{token, "tls-cert-marker", "tls-key-marker", "nats-password-marker"} {
		if strings.Contains(out, secret) {
			t.Fatalf("start line contains %q:\n%s", secret, out)
		}
	}
	if !strings.Contains(out, `"msg":"start"`) || !strings.Contains(out, `"server_id":"engine-1"`) || !strings.Contains(out, `"version":"dev"`) || !strings.Contains(out, `"nats_enabled":false`) {
		t.Fatalf("start line %s", out)
	}
}

func decodeLog(t *testing.T, out string) []logRecord {
	t.Helper()
	var lines []logRecord
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var rec logRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("log line %q: %v", line, err)
		}
		lines = append(lines, rec)
	}
	return lines
}
