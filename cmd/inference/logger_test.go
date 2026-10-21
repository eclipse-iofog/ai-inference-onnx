package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/eclipse-iofog/ai-inference-onnx/internal/session"
)

type logRecord struct {
	Level string `json:"level"`
	Msg   string `json:"msg"`
	State string `json:"state"`
}

func TestModelStateLines(t *testing.T) {
	var buf bytes.Buffer
	logger := newLogger(&buf, "info")
	logModelState(logger, "add", session.Ready)
	logModelState(logger, "add", session.Loading)
	logModelState(logger, "broken", session.Unavailable)
	logger.Debug("request timing")

	lines := decodeLog(t, buf.String())
	if len(lines) != 3 {
		t.Fatalf("lines %d: %s", len(lines), buf.String())
	}
	if !strings.EqualFold(lines[0].Level, "info") || lines[0].State != string(session.Ready) {
		t.Fatalf("ready line %+v", lines[0])
	}
	if !strings.EqualFold(lines[1].Level, "info") || lines[1].State != string(session.Loading) || !strings.Contains(strings.ToLower(lines[1].Msg), "load") {
		t.Fatalf("load line %+v", lines[1])
	}
	if !strings.EqualFold(lines[2].Level, "warn") || lines[2].State != string(session.Unavailable) {
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
