package metrics

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestSnapshotSchema(t *testing.T) {
	reg := New()
	reg.Record(Execution{
		Model: "my.model", Version: "1", Ran: true, Compute: 1500 * time.Microsecond,
		Requests: []Request{{
			Success: true, BatchSize: 8,
			Elapsed: 3 * time.Millisecond, Queued: 2 * time.Millisecond,
		}},
	})
	reg.Record(Execution{
		Model: "my.model", Version: "1",
		Requests: []Request{{Success: false, Reason: ReasonOther, Elapsed: time.Microsecond}},
	})
	reg.SetPending("my.model", "1", 4)
	reg.Observe([]ModelState{{
		Model: "my.model", Version: "1", State: StateReady,
	}})
	before := inferenceText(scrape(t, reg))
	body := mustSnapshot(t, reg, SnapshotMeta{ServerID: "engine-1", Live: true, Ready: true, NATSConnected: true})
	if inferenceText(scrape(t, reg)) != before {
		t.Fatal("reading the snapshot changed the counters")
	}
	if !utf8.Valid(body) {
		t.Fatal("snapshot is not UTF-8")
	}

	raw := decodeObject(t, body)
	if string(raw["schema"]) != "1" {
		t.Fatalf("schema %s", raw["schema"])
	}
	if string(raw["server_id"]) != `"engine-1"` {
		t.Fatalf("server id %s", raw["server_id"])
	}
	if string(raw["live"]) != "true" || string(raw["ready"]) != "true" || string(raw["nats_connected"]) != "true" {
		t.Fatalf("flags live %s ready %s nats %s", raw["live"], raw["ready"], raw["nats_connected"])
	}
	for _, absent := range []string{"microservice_name", "node_uid", "application_name", "inputs", "outputs", "process_resident_memory_bytes"} {
		if _, ok := raw[absent]; ok {
			t.Fatalf("field %s is present", absent)
		}
	}
	assertNoSnapshotFields(t, body)

	var doc snapshotDoc
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Models) != 1 {
		t.Fatalf("models %d", len(doc.Models))
	}
	got := doc.Models[0]
	if got.Model != "my.model" || got.Version != "1" || got.State != string(StateReady) || got.Reason != "" {
		t.Fatalf("model %+v", got)
	}
	if got.Success != 1 || got.Count != 8 || got.Exec != 1 || got.Pending != 4 || got.Elapsed != 3001 || got.Waiting != 2000 || got.Compute != 1500 || got.Ready != 1 {
		t.Fatalf("counters %+v", got)
	}
	if got.Failure != (counts{Other: 1}) {
		t.Fatalf("failures %+v", got.Failure)
	}
	_, samples := parseBody(t, scrape(t, reg))
	requireValue(t, samples, nameSuccess, labels("my.model", "1"), float64(got.Success))
	requireValue(t, samples, nameCount, labels("my.model", "1"), float64(got.Count))
	requireValue(t, samples, nameExec, labels("my.model", "1"), float64(got.Exec))
	requireValue(t, samples, namePending, labels("my.model", "1"), float64(got.Pending))
	requireValue(t, samples, nameElapsed, labels("my.model", "1"), float64(got.Elapsed))
	requireValue(t, samples, nameWaiting, labels("my.model", "1"), float64(got.Waiting))
	requireValue(t, samples, nameCompute, labels("my.model", "1"), float64(got.Compute))
	requireValue(t, samples, nameReady, labels("my.model", "1"), float64(got.Ready))
	requireValue(t, samples, nameFailure, failureLabels("my.model", "1", ReasonOther), 1)
}

func TestSnapshotEmptyModels(t *testing.T) {
	body := mustSnapshot(t, New(), SnapshotMeta{ServerID: "engine-1", Live: true, NATSConnected: false})
	raw := decodeObject(t, body)
	if string(raw["models"]) != "[]" {
		t.Fatalf("models %s", raw["models"])
	}
	if string(raw["ready"]) != "false" || string(raw["nats_connected"]) != "false" || string(raw["live"]) != "true" {
		t.Fatalf("flags %+v", raw)
	}
	if _, ok := raw["microservice_name"]; ok {
		t.Fatal("an unset name is present")
	}
}

func TestSnapshotEdgeletNames(t *testing.T) {
	body := mustSnapshot(t, New(), SnapshotMeta{
		ServerID:         "engine-1",
		MicroserviceName: "vision",
		NodeUID:          "node-1",
		ApplicationName:  "factory",
		Live:             true,
		NATSConnected:    true,
	})
	raw := decodeObject(t, body)
	if string(raw["microservice_name"]) != `"vision"` || string(raw["node_uid"]) != `"node-1"` || string(raw["application_name"]) != `"factory"` {
		t.Fatalf("names microservice %s node %s application %s", raw["microservice_name"], raw["node_uid"], raw["application_name"])
	}
}

func TestSnapshotModelOrder(t *testing.T) {
	reg := New()
	reg.Observe([]ModelState{
		{Model: "b", Version: "10", State: StateUnavailable, Reason: "missing"},
		{Model: "a", Version: "10", State: StateLoading, Reason: "model is loading"},
		{Model: "b", Version: "9", State: StateReady},
		{Model: "a", Version: "2", State: StateReady},
	})
	var doc snapshotDoc
	if err := json.Unmarshal(mustSnapshot(t, reg, SnapshotMeta{ServerID: "engine-1", Live: true}), &doc); err != nil {
		t.Fatal(err)
	}
	got := make([]string, len(doc.Models))
	for i, model := range doc.Models {
		got[i] = model.Model + "/" + model.Version
		if model.State == string(StateReady) && model.Ready != 1 {
			t.Fatalf("ready gauge %+v", model)
		}
		if model.State != string(StateReady) && model.Ready != 0 {
			t.Fatalf("ready gauge %+v", model)
		}
	}
	want := []string{"a/2", "a/10", "b/9", "b/10"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("order %v", got)
	}
	if doc.Models[0].Reason != "" || doc.Models[3].Reason != "missing" || doc.Models[1].Reason != "model is loading" {
		t.Fatalf("reasons %+v", doc.Models)
	}
}

func TestSnapshotKeepsCountersAcrossLoading(t *testing.T) {
	reg := New()
	reg.Record(ran("add", "1", Request{Success: true, BatchSize: 1}))
	reg.Observe([]ModelState{{Model: "add", Version: "1", State: StateReady}})
	reg.Observe([]ModelState{{Model: "add", Version: "1", State: StateLoading, Reason: "model is loading"}})
	loading := mustModel(t, reg, "add", "1")
	if loading.Success != 1 || loading.Ready != 0 || loading.State != string(StateLoading) {
		t.Fatalf("loading %+v", loading)
	}
	reg.Observe([]ModelState{{Model: "add", Version: "1", State: StateReady}})
	ready := mustModel(t, reg, "add", "1")
	if ready.Success != 1 || ready.Ready != 1 {
		t.Fatalf("ready again %+v", ready)
	}
	reg.Observe(nil)
	body := mustSnapshot(t, reg, SnapshotMeta{ServerID: "engine-1", Live: true})
	raw := decodeObject(t, body)
	if string(raw["models"]) != "[]" {
		t.Fatalf("models %s", raw["models"])
	}
	text := scrape(t, reg)
	if strings.Contains(text, `model="add"`) {
		t.Fatalf("removed version is still scraped:\n%s", text)
	}
}

func TestSubject(t *testing.T) {
	if got := Subject("engine-1"); got != "inference.metrics.engines.engine-1" {
		t.Fatalf("subject %s", got)
	}
}

func mustSnapshot(t *testing.T, reg *Registry, meta SnapshotMeta) []byte {
	t.Helper()
	body, err := reg.Snapshot(meta)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func mustModel(t *testing.T, reg *Registry, model, version string) snapshotModel {
	t.Helper()
	var doc snapshotDoc
	if err := json.Unmarshal(mustSnapshot(t, reg, SnapshotMeta{ServerID: "engine-1", Live: true}), &doc); err != nil {
		t.Fatal(err)
	}
	for _, item := range doc.Models {
		if item.Model == model && item.Version == version {
			return item
		}
	}
	t.Fatalf("missing %s/%s", model, version)
	return snapshotModel{}
}

func decodeObject(t *testing.T, body []byte) map[string]json.RawMessage {
	t.Helper()
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatal(err)
	}
	return raw
}

func assertNoSnapshotFields(t *testing.T, body []byte) {
	t.Helper()
	var value any
	if err := json.Unmarshal(body, &value); err != nil {
		t.Fatal(err)
	}
	var walk func(any)
	walk = func(node any) {
		switch typed := node.(type) {
		case map[string]any:
			for key, child := range typed {
				switch key {
				case "inputs", "outputs", "process_resident_memory_bytes":
					t.Fatalf("field %s in %s", key, body)
				}
				walk(child)
			}
		case []any:
			for _, child := range typed {
				walk(child)
			}
		}
	}
	walk(value)
}
