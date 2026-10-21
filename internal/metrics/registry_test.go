package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNamesWithoutAModel(t *testing.T) {
	body := scrape(t, New())
	types, samples := parseBody(t, body)
	requireTypes(t, types)
	if types[nameResident] != "gauge" {
		t.Fatalf("resident memory type %q", types[nameResident])
	}
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "inference_") {
			t.Fatalf("sample before any model: %s", line)
		}
	}
	positiveResident(t, samples)
	assertNoForbiddenNames(t, body)
	assertNoServerLabel(t, samples)
	if strings.Contains(body, "server_id") || strings.Contains(body, "serverId") {
		t.Fatal("exposition names a server id")
	}
}

func positiveResident(t *testing.T, samples []sample) {
	t.Helper()
	got, ok := findSample(t, samples, nameResident, map[string]string{})
	if !ok {
		t.Fatal("missing resident memory sample")
	}
	if got.value <= 0 {
		t.Fatalf("resident memory %v", got.value)
	}
}

func TestScrapeDoesNotChangeCounters(t *testing.T) {
	reg := New()
	reg.Record(Execution{
		Model: "add", Version: "1", Ran: true,
		Requests: []Request{{Success: true, BatchSize: 1, Elapsed: time.Millisecond, Queued: time.Millisecond}},
	})
	first := inferenceText(scrape(t, reg))
	second := inferenceText(scrape(t, reg))
	if first != second {
		t.Fatalf("second scrape changed counters\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

func inferenceText(body string) string {
	var b strings.Builder
	for _, line := range strings.Split(body, "\n") {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "inference_") || strings.Contains(trim, "inference_") {
			_, _ = b.WriteString(trim)
			_ = b.WriteByte('\n')
		}
	}
	return b.String()
}

func TestSuccessLabels(t *testing.T) {
	reg := New()
	reg.Record(ran("my.model", "1", Request{Success: true, BatchSize: 1}))
	reg.Record(ran("my.model", "2", Request{Success: true, BatchSize: 1}))
	body := scrape(t, reg)
	_, samples := parseBody(t, body)
	assertNoServerLabel(t, samples)

	var got []sample
	for _, s := range samples {
		if s.name != nameSuccess {
			continue
		}
		if len(s.labels) != 2 || s.labels["model"] != "my.model" {
			t.Fatalf("success labels %v", s.labels)
		}
		if _, ok := s.labels["reason"]; ok {
			t.Fatalf("success sample has a reason label: %v", s.labels)
		}
		got = append(got, s)
	}
	if len(got) != 2 {
		t.Fatalf("success samples %d, want 2", len(got))
	}
	requireValue(t, samples, nameSuccess, labels("my.model", "1"), 1)
	requireValue(t, samples, nameSuccess, labels("my.model", "2"), 1)
	if strings.Count(body, `model="my.model"`) < 2 {
		t.Fatalf("dotted model name was not kept as one value:\n%s", body)
	}
}

func TestFailureReasons(t *testing.T) {
	reg := New()
	for _, reason := range []Reason{ReasonRejected, ReasonCanceled, ReasonBackend, ReasonOther} {
		reg.Record(Execution{
			Model: "add", Version: "1", Ran: false,
			Requests: []Request{{Success: false, Reason: reason}},
		})
	}
	reg.Record(Execution{
		Model: "add", Version: "1", Ran: false,
		Requests: []Request{{Success: false, Reason: "NOPE"}},
	})
	_, samples := parseBody(t, scrape(t, reg))
	requireValue(t, samples, nameFailure, failureLabels("add", "1", ReasonRejected), 1)
	requireValue(t, samples, nameFailure, failureLabels("add", "1", ReasonCanceled), 1)
	requireValue(t, samples, nameFailure, failureLabels("add", "1", ReasonBackend), 1)
	requireValue(t, samples, nameFailure, failureLabels("add", "1", ReasonOther), 2)
	requireAbsent(t, samples, nameFailure, failureLabels("add", "1", "NOPE"))
	for _, s := range samples {
		if s.name != nameFailure {
			continue
		}
		if len(s.labels) != 3 {
			t.Fatalf("failure labels %v", s.labels)
		}
	}
}

func TestPending(t *testing.T) {
	reg := New()
	reg.SetPending("add", "1", 4)
	_, samples := parseBody(t, scrape(t, reg))
	requireValue(t, samples, namePending, labels("add", "1"), 4)

	reg.SetPending("add", "1", 0)
	_, samples = parseBody(t, scrape(t, reg))
	requireValue(t, samples, namePending, labels("add", "1"), 0)
}

func TestReadyLoadingKeepsCounters(t *testing.T) {
	reg := New()
	reg.Record(ran("add", "1", Request{Success: true, BatchSize: 1}))
	reg.Record(Execution{
		Model: "add", Version: "1", Ran: false,
		Requests: []Request{{Success: false, Reason: ReasonRejected}},
	})
	reg.SetState("add", "1", StateReady)
	_, samples := parseBody(t, scrape(t, reg))
	requireValue(t, samples, nameReady, labels("add", "1"), 1)
	requireValue(t, samples, nameSuccess, labels("add", "1"), 1)
	requireReadyLabels(t, samples)

	reg.SetState("add", "1", StateLoading)
	_, samples = parseBody(t, scrape(t, reg))
	requireValue(t, samples, nameReady, labels("add", "1"), 0)
	requireValue(t, samples, nameSuccess, labels("add", "1"), 1)
	requireValue(t, samples, nameFailure, failureLabels("add", "1", ReasonRejected), 1)

	reg.SetState("add", "1", StateUnavailable)
	_, samples = parseBody(t, scrape(t, reg))
	requireValue(t, samples, nameReady, labels("add", "1"), 0)
	requireValue(t, samples, nameSuccess, labels("add", "1"), 1)

	reg.SetState("add", "1", StateReady)
	_, samples = parseBody(t, scrape(t, reg))
	requireValue(t, samples, nameReady, labels("add", "1"), 1)
	requireValue(t, samples, nameSuccess, labels("add", "1"), 1)
}

func requireReadyLabels(t *testing.T, samples []sample) {
	t.Helper()
	for _, s := range samples {
		if s.name != nameReady {
			continue
		}
		if len(s.labels) != 2 {
			t.Fatalf("ready labels %v", s.labels)
		}
		if _, ok := s.labels["reason"]; ok {
			t.Fatalf("ready sample has a reason label: %v", s.labels)
		}
		if _, ok := s.labels["state"]; ok {
			t.Fatalf("ready sample has a state label: %v", s.labels)
		}
	}
}

func TestRemoveVersionDropsSamples(t *testing.T) {
	reg := New()
	reg.Record(ran("add", "1", Request{Success: true, BatchSize: 2}))
	reg.Record(Execution{
		Model: "add", Version: "1", Ran: false,
		Requests: []Request{{Success: false, Reason: ReasonOther}},
	})
	reg.SetPending("add", "1", 3)
	reg.SetState("add", "1", StateReady)
	reg.Record(ran("add", "2", Request{Success: true, BatchSize: 1}))
	reg.SetState("add", "2", StateReady)

	reg.Remove("add", "1")
	body := scrape(t, reg)
	types, samples := parseBody(t, body)
	requireTypes(t, types)
	requireAbsent(t, samples, nameSuccess, labels("add", "1"))
	requireAbsent(t, samples, nameCount, labels("add", "1"))
	requireAbsent(t, samples, nameExec, labels("add", "1"))
	requireAbsent(t, samples, namePending, labels("add", "1"))
	requireAbsent(t, samples, nameReady, labels("add", "1"))
	requireAbsent(t, samples, nameFailure, failureLabels("add", "1", ReasonOther))
	requireAbsent(t, samples, nameElapsed, labels("add", "1"))
	requireValue(t, samples, nameSuccess, labels("add", "2"), 1)
	requireValue(t, samples, nameReady, labels("add", "2"), 1)
	if strings.Contains(body, `version="1"`) {
		t.Fatalf("removed version still in exposition:\n%s", body)
	}
}

func TestUnrecordedModelHasNoSeries(t *testing.T) {
	reg := New()
	reg.Record(ran("add", "1", Request{Success: true, BatchSize: 1}))
	body := scrape(t, reg)
	if strings.Contains(body, `model="missing"`) {
		t.Fatalf("unrecorded model created a series:\n%s", body)
	}
}

func TestConcurrentUpdates(t *testing.T) {
	reg := New()
	var wg sync.WaitGroup
	fail := make(chan int, 1)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < 20; n++ {
				reg.Record(ran("add", "1", Request{Success: true, BatchSize: 1}))
				reg.SetPending("add", "1", uint64(n))
				reg.SetState("add", "1", StateLoading)
				reg.Remove("add", "2")
				rec := httptest.NewRecorder()
				reg.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
				if rec.Code != http.StatusOK {
					select {
					case fail <- rec.Code:
					default:
					}
					return
				}
			}
		}()
	}
	wg.Wait()
	select {
	case code := <-fail:
		t.Fatalf("status %d", code)
	default:
	}
}

func ran(model, version string, requests ...Request) Execution {
	return Execution{Model: model, Version: version, Ran: true, Requests: requests}
}
