package metrics

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

type sample struct {
	name   string
	labels map[string]string
	value  float64
}

func scrape(t *testing.T, h http.Handler) string {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != expositionType {
		t.Fatalf("content type %q", got)
	}
	return rec.Body.String()
}

func parseBody(t *testing.T, body string) (map[string]string, []sample) {
	t.Helper()
	types := map[string]string{}
	var samples []sample
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "":
			continue
		case strings.HasPrefix(line, "# TYPE "):
			parts := strings.Fields(line)
			if len(parts) != 4 {
				t.Fatalf("type line %q", line)
			}
			types[parts[2]] = parts[3]
		case strings.HasPrefix(line, "#"):
			continue
		default:
			samples = append(samples, parseSample(t, line))
		}
	}
	return types, samples
}

func parseSample(t *testing.T, line string) sample {
	t.Helper()
	fields := strings.Fields(line)
	if len(fields) < 2 || len(fields) > 3 {
		t.Fatalf("sample %q", line)
	}
	head := fields[0]
	name := head
	labels := map[string]string{}
	if i := strings.IndexByte(head, '{'); i >= 0 {
		if !strings.HasSuffix(head, "}") {
			t.Fatalf("labels %q", line)
		}
		name = head[:i]
		labels = parseLabels(t, head[i+1:len(head)-1])
	}
	value, err := strconv.ParseFloat(fields[1], 64)
	if err != nil {
		t.Fatalf("value in %q: %v", line, err)
	}
	return sample{name: name, labels: labels, value: value}
}

func parseLabels(t *testing.T, raw string) map[string]string {
	t.Helper()
	labels := map[string]string{}
	if raw == "" {
		return labels
	}
	for _, pair := range strings.Split(raw, ",") {
		name, value, ok := strings.Cut(pair, "=")
		if !ok || len(value) < 2 || value[0] != '"' || value[len(value)-1] != '"' {
			t.Fatalf("label %q", pair)
		}
		labels[name] = unescapeLabel(value[1 : len(value)-1])
	}
	return labels
}

func unescapeLabel(value string) string {
	var b strings.Builder
	for i := 0; i < len(value); i++ {
		if value[i] != '\\' || i+1 == len(value) {
			_ = b.WriteByte(value[i])
			continue
		}
		i++
		if value[i] == 'n' {
			_ = b.WriteByte('\n')
			continue
		}
		_ = b.WriteByte(value[i])
	}
	return b.String()
}

func labels(model, version string) map[string]string {
	return map[string]string{"model": model, "version": version}
}

func failureLabels(model, version string, reason Reason) map[string]string {
	return map[string]string{"model": model, "version": version, "reason": string(reason)}
}

func findSample(t *testing.T, samples []sample, name string, want map[string]string) (sample, bool) {
	t.Helper()
	var found sample
	seen := false
	for _, s := range samples {
		if s.name != name || len(s.labels) != len(want) {
			continue
		}
		match := true
		for k, v := range want {
			if s.labels[k] != v {
				match = false
				break
			}
		}
		if !match {
			continue
		}
		if seen {
			t.Fatalf("duplicate %s %v", name, want)
		}
		found = s
		seen = true
	}
	return found, seen
}

func requireValue(t *testing.T, samples []sample, name string, wantLabels map[string]string, want float64) {
	t.Helper()
	got, ok := findSample(t, samples, name, wantLabels)
	if !ok {
		t.Fatalf("missing %s %v", name, wantLabels)
	}
	if got.value != want {
		t.Fatalf("%s %v = %v, want %v", name, wantLabels, got.value, want)
	}
}

func requireAbsent(t *testing.T, samples []sample, name string, wantLabels map[string]string) {
	t.Helper()
	if _, ok := findSample(t, samples, name, wantLabels); ok {
		t.Fatalf("unexpected %s %v", name, wantLabels)
	}
}

func requireTypes(t *testing.T, types map[string]string) {
	t.Helper()
	for _, s := range inferenceSpecs {
		got, ok := types[s.name]
		if !ok {
			t.Fatalf("missing type line for %s", s.name)
		}
		if got != typeName(s.kind) {
			t.Fatalf("%s type %s, want %s", s.name, got, typeName(s.kind))
		}
	}
}

func assertNoForbiddenNames(t *testing.T, body string) {
	t.Helper()
	forbidden := []string{
		"nv_inference_",
		"nv_cpu_",
		"nv_gpu_",
		"nv_energy_consumption",
		"nv_cache",
		"nv_pinned_memory",
		"quantile",
		"_bucket",
		"request_preprocess_seconds",
		"request_predict_seconds",
		"request_postprocess_seconds",
		"request_explain_seconds",
	}
	for _, name := range forbidden {
		if strings.Contains(body, name) {
			t.Fatalf("exposition contains %s", name)
		}
	}
}

func assertNoServerLabel(t *testing.T, samples []sample) {
	t.Helper()
	for _, s := range samples {
		for _, key := range []string{"server", "server_id", "serverId"} {
			if _, ok := s.labels[key]; ok {
				t.Fatalf("%s has label %s", s.name, key)
			}
		}
	}
}
