package store

import (
	"bytes"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const fixtureEngineA = `{
  "schema": 1,
  "server_id": "engine-a",
  "live": true,
  "ready": true,
  "nats_connected": true,
  "node_uid": "node-1",
  "microservice_name": "wafer-engine",
  "application_name": "line",
  "models": [
    {
      "model": "wafer-defect",
      "version": "1",
      "state": "READY",
      "reason": "",
      "inference_request_success": 2,
      "inference_request_failure": {"REJECTED": 0, "CANCELED": 0, "BACKEND": 0, "OTHER": 0},
      "inference_count": 2,
      "inference_exec_count": 2,
      "inference_pending_request_count": 0,
      "inference_request_duration_us": 100,
      "inference_queue_duration_us": 10,
      "inference_compute_infer_duration_us": 80,
      "inference_model_ready": 1
    }
  ]
}`

const fixtureEngineB = `{
  "schema": 1,
  "server_id": "engine-b",
  "live": true,
  "ready": true,
  "nats_connected": true,
  "node_uid": "node-2",
  "microservice_name": "wafer-engine-b",
  "application_name": "line",
  "models": [
    {
      "model": "wafer-defect",
      "version": "1",
      "state": "READY",
      "reason": "",
      "inference_request_success": 2,
      "inference_request_failure": {"REJECTED": 0, "CANCELED": 0, "BACKEND": 0, "OTHER": 1},
      "inference_count": 8,
      "inference_exec_count": 2,
      "inference_pending_request_count": 1,
      "inference_request_duration_us": 300,
      "inference_queue_duration_us": 0,
      "inference_compute_infer_duration_us": 0,
      "inference_model_ready": 1
    }
  ]
}`

func fixtureAt() time.Time {
	return time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
}

func keepFixtures(t *testing.T) *Store {
	t.Helper()
	at := fixtureAt()
	now := at
	s := New(func() time.Time { return now })
	if !s.Accept([]byte(fixtureEngineA)) {
		t.Fatal("engine-a was dropped")
	}
	now = at.Add(10 * time.Second)
	if !s.Accept([]byte(fixtureEngineB)) {
		t.Fatal("engine-b was dropped")
	}
	return s
}

func keepEngineA(t *testing.T) *Store {
	t.Helper()
	at := fixtureAt()
	s := New(func() time.Time { return at })
	if !s.Accept([]byte(fixtureEngineA)) {
		t.Fatal("engine-a was dropped")
	}
	return s
}

func TestFixturesShareOneLatency(t *testing.T) {
	s := keepFixtures(t)
	engines := s.Engines()
	if len(engines) != 2 {
		t.Fatalf("engines %d", len(engines))
	}
	if engines[0].ServerID != "engine-a" || engines[1].ServerID != "engine-b" {
		t.Fatalf("order %s %s", engines[0].ServerID, engines[1].ServerID)
	}

	a := engines[0]
	if a.MicroserviceName != "wafer-engine" || a.NodeUID != "node-1" || a.ApplicationName != "line" {
		t.Fatalf("engine-a identity %+v", a)
	}
	if !a.Live || !a.Ready || !a.NATSConnected {
		t.Fatal("engine-a flags")
	}
	if len(a.Samples) != 1 || !a.Samples[0].At.Equal(fixtureAt()) {
		t.Fatalf("engine-a samples %d", len(a.Samples))
	}
	if !a.Samples[0].Latency.Known || a.Samples[0].Latency.Whole() != 50 {
		t.Fatalf("engine-a sample latency %+v", a.Samples[0].Latency)
	}
	if len(a.Models) != 1 {
		t.Fatalf("engine-a models %d", len(a.Models))
	}
	modelA := a.Models[0]
	if modelA.Name != "wafer-defect" || modelA.Version != "1" || modelA.State != StateReady || modelA.Reason != "" {
		t.Fatalf("engine-a model %+v", modelA)
	}
	batchA := modelA.BatchSize()
	if !batchA.Known || batchA.Numerator != 2 || batchA.Divisor != 2 || batchA.Whole() != 1 {
		t.Fatalf("engine-a batch %+v", batchA)
	}
	if modelA.Failure.Sum() != 0 || modelA.Pending != 0 || modelA.QueueDuration != 10 || modelA.ComputeDuration != 80 || modelA.ModelReady != 1 {
		t.Fatalf("engine-a counters %+v", modelA)
	}

	b := engines[1]
	if b.MicroserviceName != "wafer-engine-b" || b.NodeUID != "node-2" || b.ApplicationName != "line" {
		t.Fatalf("engine-b identity %+v", b)
	}
	if len(b.Samples) != 1 || !b.Samples[0].At.Equal(fixtureAt().Add(10*time.Second)) {
		t.Fatal("engine-b receipt")
	}
	if !b.Samples[0].Latency.Known || b.Samples[0].Latency.Whole() != 150 {
		t.Fatalf("engine-b sample latency %+v", b.Samples[0].Latency)
	}
	modelB := b.Models[0]
	if modelB.Pending != 1 || modelB.Failure.Other != 1 || modelB.Failure.Sum() != 1 {
		t.Fatalf("engine-b failure %+v pending %d", modelB.Failure, modelB.Pending)
	}
	if modelB.Failure.Rejected != 0 || modelB.Failure.Canceled != 0 || modelB.Failure.Backend != 0 {
		t.Fatalf("engine-b other reasons %+v", modelB.Failure)
	}
	batchB := modelB.BatchSize()
	if !batchB.Known || batchB.Numerator != 8 || batchB.Divisor != 2 || batchB.Whole() != 4 {
		t.Fatalf("engine-b batch %+v", batchB)
	}

	rows := s.Models()
	if len(rows) != 1 {
		t.Fatalf("rows %d", len(rows))
	}
	row := rows[0]
	if row.Name != "wafer-defect" {
		t.Fatalf("name %s", row.Name)
	}
	if len(row.ServerIDs) != 2 || row.ServerIDs[0] != "engine-a" || row.ServerIDs[1] != "engine-b" {
		t.Fatalf("server ids %v", row.ServerIDs)
	}
	if !row.Latency.Known || row.Latency.Numerator != 400 || row.Latency.Divisor != 4 || row.Latency.Whole() != 100 {
		t.Fatalf("latency %+v", row.Latency)
	}

	nodes := s.Nodes()
	if len(nodes) != 2 || nodes[0].NodeUID != "node-1" || nodes[1].NodeUID != "node-2" {
		t.Fatalf("nodes %+v", nodeIDs(nodes))
	}
	if len(nodes[0].Engines) != 1 || nodes[0].Engines[0].ServerID != "engine-a" {
		t.Fatal("node-1 engines")
	}
	if len(nodes[1].Engines) != 1 || nodes[1].Engines[0].ServerID != "engine-b" {
		t.Fatal("node-2 engines")
	}
}

func TestSecondModelNameIsAnotherRow(t *testing.T) {
	at := fixtureAt()
	s := New(func() time.Time { return at })
	if !s.Accept([]byte(fixtureEngineA)) {
		t.Fatal("engine-a was dropped")
	}
	other := strings.Replace(fixtureEngineB, `"model": "wafer-defect"`, `"model": "surface"`, 1)
	if !s.Accept([]byte(other)) {
		t.Fatal("engine-b was dropped")
	}
	rows := s.Models()
	if len(rows) != 2 {
		t.Fatalf("rows %d", len(rows))
	}
	if rows[0].Name != "surface" || len(rows[0].ServerIDs) != 1 || rows[0].ServerIDs[0] != "engine-b" {
		t.Fatalf("surface %+v", rows[0])
	}
	if !rows[0].Latency.Known || rows[0].Latency.Whole() != 150 {
		t.Fatalf("surface latency %+v", rows[0].Latency)
	}
	if rows[1].Name != "wafer-defect" || len(rows[1].ServerIDs) != 1 || rows[1].ServerIDs[0] != "engine-a" {
		t.Fatalf("wafer %+v", rows[1])
	}
	if !rows[1].Latency.Known || rows[1].Latency.Whole() != 50 {
		t.Fatalf("wafer latency %+v", rows[1].Latency)
	}
}

func TestOneServerIDIsOneEngine(t *testing.T) {
	at := fixtureAt()
	now := at
	s := New(func() time.Time { return now })
	if !s.Accept([]byte(fixtureEngineA)) {
		t.Fatal("first snapshot was dropped")
	}
	now = at.Add(10 * time.Second)
	second := strings.Replace(fixtureEngineA, `"inference_pending_request_count": 0`, `"inference_pending_request_count": 7`, 1)
	if !s.Accept([]byte(second)) {
		t.Fatal("second snapshot was dropped")
	}
	engines := s.Engines()
	if len(engines) != 1 {
		t.Fatalf("engines %d", len(engines))
	}
	eng := engines[0]
	if eng.ServerID != "engine-a" || len(eng.Samples) != 2 {
		t.Fatalf("samples %d", len(eng.Samples))
	}
	if !eng.Samples[0].At.Equal(at) || eng.Samples[0].Models[0].Pending != 0 {
		t.Fatal("oldest sample")
	}
	if !eng.Samples[1].At.Equal(at.Add(10*time.Second)) || eng.Samples[1].Models[0].Pending != 7 {
		t.Fatal("newest sample")
	}
	if eng.Models[0].Pending != 7 {
		t.Fatalf("newest pending %d", eng.Models[0].Pending)
	}
	rows := s.Models()
	if len(rows) != 1 || len(rows[0].ServerIDs) != 1 {
		t.Fatalf("rows %+v", rows)
	}
}

func TestDroppedDocumentLeavesTheEngine(t *testing.T) {
	for _, tc := range droppedBodies() {
		t.Run(tc.name, func(t *testing.T) {
			if bytes.Equal(tc.body, []byte(fixtureEngineA)) {
				t.Fatal("case did not change the fixture")
			}
			s := keepEngineA(t)
			if s.Accept(tc.body) {
				t.Fatal("dropped document was kept")
			}
			engines := s.Engines()
			if len(engines) != 1 || engines[0].ServerID != "engine-a" || len(engines[0].Samples) != 1 {
				t.Fatalf("engines %d", len(engines))
			}
			if engines[0].Models[0].Name != "wafer-defect" {
				t.Fatal("kept model was erased")
			}

			fresh := New(func() time.Time { return fixtureAt() })
			if fresh.Accept(tc.body) {
				t.Fatal("dropped document was kept on an empty store")
			}
			if _, status := fresh.Lookup("engine-a"); status != StatusUnknown {
				t.Fatalf("status %d", status)
			}
		})
	}
}

func TestSampleLimitDropsOldest(t *testing.T) {
	at := fixtureAt()
	now := at
	s := New(func() time.Time { return now })
	for i := range 360 {
		now = at.Add(time.Duration(i) * time.Millisecond)
		if !s.Accept(withPending(i)) {
			t.Fatalf("sample %d was dropped", i)
		}
	}
	eng, status := s.Lookup("engine-a")
	if status != StatusLive || len(eng.Samples) != 360 {
		t.Fatalf("status %d samples %d", status, len(eng.Samples))
	}
	if eng.Samples[0].Models[0].Pending != 0 || eng.Samples[359].Models[0].Pending != 359 {
		t.Fatalf("ends %d %d", eng.Samples[0].Models[0].Pending, eng.Samples[359].Models[0].Pending)
	}

	now = at.Add(360 * time.Millisecond)
	if !s.Accept(withPending(360)) {
		t.Fatal("sample 360 was dropped")
	}
	eng, status = s.Lookup("engine-a")
	if status != StatusLive || len(eng.Samples) != 360 {
		t.Fatalf("after limit status %d samples %d", status, len(eng.Samples))
	}
	if eng.Samples[0].Models[0].Pending != 1 {
		t.Fatalf("oldest pending %d", eng.Samples[0].Models[0].Pending)
	}
	if eng.Samples[359].Models[0].Pending != 360 {
		t.Fatalf("newest pending %d", eng.Samples[359].Models[0].Pending)
	}
	if !eng.Samples[0].At.Equal(at.Add(time.Millisecond)) {
		t.Fatal("oldest receipt was kept")
	}
	if len(s.Engines()) != 1 {
		t.Fatal("sample limit split the engine")
	}
}

func TestEngineIsLiveAt29SecondsAndStaleAt30(t *testing.T) {
	at := fixtureAt()
	now := at
	s := New(func() time.Time { return now })
	if !s.Accept([]byte(fixtureEngineA)) {
		t.Fatal("engine-a was dropped")
	}

	now = at.Add(29 * time.Second)
	if len(s.Engines()) != 1 || len(s.Models()) != 1 || len(s.Nodes()) != 1 {
		t.Fatal("age 29 seconds left the live view")
	}
	if _, status := s.Lookup("engine-a"); status != StatusLive {
		t.Fatalf("age 29 status %d", status)
	}

	now = at.Add(30 * time.Second)
	if len(s.Engines()) != 0 || len(s.Models()) != 0 || len(s.Nodes()) != 0 {
		t.Fatal("age 30 seconds stayed on the live view")
	}
	eng, status := s.Lookup("engine-a")
	if status != StatusStale || eng.ServerID != "" || len(eng.Samples) != 0 {
		t.Fatalf("stale status %d id %q samples %d", status, eng.ServerID, len(eng.Samples))
	}
	if _, missing := s.Lookup("engine-missing"); missing != StatusUnknown {
		t.Fatalf("missing status %d", missing)
	}

	now = at.Add(31 * time.Second)
	if !s.Accept([]byte(fixtureEngineA)) {
		t.Fatal("later snapshot was dropped")
	}
	eng, status = s.Lookup("engine-a")
	if status != StatusLive || len(eng.Samples) != 1 {
		t.Fatalf("restored status %d samples %d", status, len(eng.Samples))
	}
	if !eng.Samples[0].At.Equal(at.Add(31 * time.Second)) {
		t.Fatal("restored receipt")
	}
	rows := s.Models()
	if len(rows) != 1 || rows[0].Name != "wafer-defect" {
		t.Fatal("restored model row")
	}
	if len(s.Engines()) != 1 || len(s.Nodes()) != 1 {
		t.Fatal("restored lists")
	}
}

func TestZeroSuccessDoesNotDivide(t *testing.T) {
	at := fixtureAt()
	s := New(func() time.Time { return at })
	quietA := strings.Replace(fixtureEngineA, `"inference_request_success": 2`, `"inference_request_success": 0`, 1)
	quietB := strings.Replace(fixtureEngineB, `"inference_request_success": 2`, `"inference_request_success": 0`, 1)
	if !s.Accept([]byte(quietA)) || !s.Accept([]byte(quietB)) {
		t.Fatal("snapshot was dropped")
	}
	rows := s.Models()
	if len(rows) != 1 || rows[0].Name != "wafer-defect" {
		t.Fatalf("rows %d", len(rows))
	}
	if rows[0].Latency.Known || rows[0].Latency.Numerator != 0 || rows[0].Latency.Divisor != 0 || rows[0].Latency.Whole() != 0 {
		t.Fatalf("latency %+v", rows[0].Latency)
	}
	eng, status := s.Lookup("engine-a")
	if status != StatusLive || len(eng.Samples) != 1 || eng.Samples[0].Latency.Known || eng.Samples[0].Latency.Whole() != 0 {
		t.Fatalf("sample latency %+v", eng.Samples[0].Latency)
	}

	quiet := New(func() time.Time { return at })
	if !quiet.Accept([]byte(counterSnapshot("engine-z", "wafer-defect", "1", 4, 0))) {
		t.Fatal("zero duration was dropped")
	}
	rows = quiet.Models()
	if len(rows) != 1 || !rows[0].Latency.Known || rows[0].Latency.Numerator != 0 || rows[0].Latency.Divisor != 4 || rows[0].Latency.Whole() != 0 {
		t.Fatalf("known zero latency %+v", rows)
	}
}

func TestZeroExecDoesNotDivide(t *testing.T) {
	at := fixtureAt()
	s := New(func() time.Time { return at })
	body := strings.Replace(fixtureEngineA, `"inference_exec_count": 2`, `"inference_exec_count": 0`, 1)
	if !s.Accept([]byte(body)) {
		t.Fatal("snapshot was dropped")
	}
	eng, status := s.Lookup("engine-a")
	if status != StatusLive || len(eng.Models) != 1 {
		t.Fatal("engine missing")
	}
	batch := eng.Models[0].BatchSize()
	if batch.Known || batch.Numerator != 0 || batch.Divisor != 0 || batch.Whole() != 0 {
		t.Fatalf("batch %+v", batch)
	}
	if eng.Models[0].Count != 2 {
		t.Fatalf("count %d", eng.Models[0].Count)
	}
}

func TestImageFieldIsIgnored(t *testing.T) {
	at := fixtureAt()
	now := at
	s := New(func() time.Time { return now })
	body := strings.Replace(fixtureEngineA, "{\n", "{\n  \"image\": \"cpu\",\n", 1)
	body = strings.Replace(body, `"model": "wafer-defect"`, `"image": "cpu", "model": "wafer-defect"`, 1)
	if !s.Accept([]byte(body)) {
		t.Fatal("engine-a was dropped")
	}
	now = at.Add(10 * time.Second)
	if !s.Accept([]byte(fixtureEngineB)) {
		t.Fatal("engine-b was dropped")
	}
	rows := s.Models()
	if len(rows) != 1 || rows[0].Name != "wafer-defect" || rows[0].Latency.Whole() != 100 {
		t.Fatalf("latency %+v", rows)
	}
	eng, status := s.Lookup("engine-a")
	if status != StatusLive || eng.MicroserviceName != "wafer-engine" || eng.Models[0].Name != "wafer-defect" {
		t.Fatalf("engine-a %+v", eng.ServerID)
	}
	if eng.ORTVersion != "" || len(eng.ExecutionProviders) != 0 {
		t.Fatalf("runtime %+v %q", eng.ExecutionProviders, eng.ORTVersion)
	}
}

func TestRuntimeFields(t *testing.T) {
	at := fixtureAt()
	s := New(func() time.Time { return at })
	body := strings.Replace(fixtureEngineA, `"nats_connected": true,`, `"nats_connected": true, "execution_providers": ["TensorrtExecutionProvider", "", "CUDAExecutionProvider"], "ort_version": "1.31.0",`, 1)
	if !s.Accept([]byte(body)) {
		t.Fatal("snapshot was dropped")
	}
	eng, status := s.Lookup("engine-a")
	if status != StatusLive || eng.ORTVersion != "1.31.0" || len(eng.ExecutionProviders) != 2 || eng.ExecutionProviders[0] != "TensorrtExecutionProvider" || eng.ExecutionProviders[1] != "CUDAExecutionProvider" {
		t.Fatalf("runtime %+v %q", eng.ExecutionProviders, eng.ORTVersion)
	}
}

func TestLatencyIsTheRatioOfSums(t *testing.T) {
	at := fixtureAt()
	s := New(func() time.Time { return at })
	if !s.Accept([]byte(counterSnapshot("engine-a", "wafer-defect", "1", 1, 10))) {
		t.Fatal("engine-a was dropped")
	}
	if !s.Accept([]byte(counterSnapshot("engine-b", "wafer-defect", "1", 3, 300))) {
		t.Fatal("engine-b was dropped")
	}
	rows := s.Models()
	if len(rows) != 1 {
		t.Fatalf("rows %d", len(rows))
	}
	if !rows[0].Latency.Known || rows[0].Latency.Numerator != 310 || rows[0].Latency.Divisor != 4 || rows[0].Latency.Whole() != 77 {
		t.Fatalf("latency %+v", rows[0].Latency)
	}
}

func TestVersionsOfOneNameShareTheRow(t *testing.T) {
	at := fixtureAt()
	s := New(func() time.Time { return at })
	const body = `{
	  "schema": 1,
	  "server_id": "engine-a",
	  "live": true,
	  "ready": true,
	  "nats_connected": true,
	  "models": [
	    {
	      "model": "wafer-defect",
	      "version": "10",
	      "state": "LOADING",
	      "reason": "reading",
	      "inference_request_success": 3,
	      "inference_request_failure": {"REJECTED": 0, "CANCELED": 0, "BACKEND": 0, "OTHER": 0},
	      "inference_count": 3,
	      "inference_exec_count": 1,
	      "inference_pending_request_count": 0,
	      "inference_request_duration_us": 300,
	      "inference_queue_duration_us": 0,
	      "inference_compute_infer_duration_us": 0,
	      "inference_model_ready": 0
	    },
	    {
	      "model": "wafer-defect",
	      "version": "9",
	      "state": "UNAVAILABLE",
	      "reason": "missing",
	      "inference_request_success": 1,
	      "inference_request_failure": {"REJECTED": 1, "CANCELED": 0, "BACKEND": 0, "OTHER": 0},
	      "inference_count": 1,
	      "inference_exec_count": 1,
	      "inference_pending_request_count": 0,
	      "inference_request_duration_us": 10,
	      "inference_queue_duration_us": 0,
	      "inference_compute_infer_duration_us": 0,
	      "inference_model_ready": 0
	    }
	  ]
	}`
	if !s.Accept([]byte(body)) {
		t.Fatal("snapshot was dropped")
	}
	eng, status := s.Lookup("engine-a")
	if status != StatusLive || len(eng.Models) != 2 {
		t.Fatalf("models %d", len(eng.Models))
	}
	if eng.Models[0].Version != "9" || eng.Models[1].Version != "10" {
		t.Fatalf("order %s %s", eng.Models[0].Version, eng.Models[1].Version)
	}
	if eng.Models[0].State != StateUnavailable || eng.Models[1].State != StateLoading {
		t.Fatalf("states %s %s", eng.Models[0].State, eng.Models[1].State)
	}
	rows := s.Models()
	if len(rows) != 1 || len(rows[0].ServerIDs) != 1 || rows[0].ServerIDs[0] != "engine-a" {
		t.Fatalf("row %+v", rows)
	}
	if !rows[0].Latency.Known || rows[0].Latency.Numerator != 310 || rows[0].Latency.Whole() != 77 {
		t.Fatalf("latency %+v", rows[0].Latency)
	}
	if !eng.Samples[0].Latency.Known || eng.Samples[0].Latency.Whole() != 77 {
		t.Fatalf("sample latency %+v", eng.Samples[0].Latency)
	}
}

func TestNodesPutAnUnsetNodeLast(t *testing.T) {
	at := fixtureAt()
	s := New(func() time.Time { return at })
	if !s.Accept([]byte(fixtureEngineA)) || !s.Accept([]byte(fixtureEngineB)) {
		t.Fatal("fixture was dropped")
	}
	const absent = `{
	  "schema": 1,
	  "server_id": "engine-c",
	  "live": true,
	  "ready": false,
	  "nats_connected": false,
	  "models": []
	}`
	const empty = `{
	  "schema": 1,
	  "server_id": "engine-d",
	  "live": false,
	  "ready": false,
	  "nats_connected": false,
	  "node_uid": "",
	  "microservice_name": "",
	  "application_name": "",
	  "models": []
	}`
	if !s.Accept([]byte(absent)) || !s.Accept([]byte(empty)) {
		t.Fatal("unset node was dropped")
	}
	nodes := s.Nodes()
	if len(nodes) != 3 {
		t.Fatalf("nodes %v", nodeIDs(nodes))
	}
	if nodes[0].NodeUID != "node-1" || nodes[1].NodeUID != "node-2" || nodes[2].NodeUID != "" {
		t.Fatalf("order %v", nodeIDs(nodes))
	}
	last := nodes[2].Engines
	if len(last) != 2 || last[0].ServerID != "engine-c" || last[1].ServerID != "engine-d" {
		t.Fatalf("unset engines %d", len(last))
	}
	if last[0].NodeUID != "" || last[1].MicroserviceName != "" || last[1].ApplicationName != "" {
		t.Fatal("empty platform names were kept")
	}
	if last[0].Ready || last[0].NATSConnected || last[1].Live {
		t.Fatal("flags")
	}
	if len(s.Models()) != 1 {
		t.Fatal("an engine with no models invented a row")
	}
}

func TestEmptyStore(t *testing.T) {
	s := New(func() time.Time { return fixtureAt() })
	if len(s.Engines()) != 0 || len(s.Models()) != 0 || len(s.Nodes()) != 0 {
		t.Fatal("new store was not empty")
	}
	if _, status := s.Lookup("engine-a"); status != StatusUnknown {
		t.Fatalf("status %d", status)
	}
	var empty *Store
	if empty.Accept([]byte(fixtureEngineA)) || len(empty.Engines()) != 0 || len(empty.Models()) != 0 || len(empty.Nodes()) != 0 {
		t.Fatal("nil store kept a snapshot")
	}
	if _, status := empty.Lookup("engine-a"); status != StatusUnknown {
		t.Fatalf("nil status %d", status)
	}
}

func TestNilClockKeepsAFreshSnapshot(t *testing.T) {
	s := New(nil)
	if !s.Accept([]byte(fixtureEngineA)) {
		t.Fatal("snapshot was dropped")
	}
	engines := s.Engines()
	if len(engines) != 1 || engines[0].ServerID != "engine-a" {
		t.Fatalf("engines %d", len(engines))
	}
}

func TestAcceptRacesWithReads(t *testing.T) {
	at := fixtureAt()
	s := New(func() time.Time { return at })
	var group sync.WaitGroup
	var dropped atomic.Int32
	for range 8 {
		group.Add(1)
		go func() {
			defer group.Done()
			for range 40 {
				if !s.Accept([]byte(fixtureEngineA)) {
					dropped.Add(1)
				}
				_ = s.Engines()
				_ = s.Models()
				_ = s.Nodes()
				_, _ = s.Lookup("engine-a")
			}
		}()
	}
	group.Wait()
	if dropped.Load() != 0 {
		t.Fatal("a valid snapshot was dropped")
	}
	if len(s.Engines()) != 1 {
		t.Fatal("race lost the engine")
	}
}

func droppedBodies() []struct {
	name string
	body []byte
} {
	return []struct {
		name string
		body []byte
	}{
		{name: "schema 2", body: []byte(strings.Replace(fixtureEngineA, `"schema": 1`, `"schema": 2`, 1))},
		{name: "fractional schema", body: []byte(strings.Replace(fixtureEngineA, `"schema": 1`, `"schema": 1.5`, 1))},
		{name: "null models", body: []byte(`{"schema":1,"server_id":"engine-a","live":true,"ready":true,"nats_connected":true,"models":null}`)},
		{name: "missing models", body: []byte(`{"schema":1,"server_id":"engine-a","live":true,"ready":true,"nats_connected":true}`)},
		{name: "missing failure key", body: []byte(strings.Replace(fixtureEngineA, `, "OTHER": 0`, "", 1))},
		{name: "negative counter", body: []byte(strings.Replace(fixtureEngineA, `"inference_request_success": 2`, `"inference_request_success": -1`, 1))},
		{name: "fractional counter", body: []byte(strings.Replace(fixtureEngineA, `"inference_request_success": 2`, `"inference_request_success": 1.5`, 1))},
		{name: "string counter", body: []byte(strings.Replace(fixtureEngineA, `"inference_count": 2`, `"inference_count": "2"`, 1))},
		{name: "state", body: []byte(strings.Replace(fixtureEngineA, `"state": "READY"`, `"state": "OFFLINE"`, 1))},
		{name: "dotted server id", body: []byte(strings.Replace(fixtureEngineA, `"server_id": "engine-a"`, `"server_id": "engine.a"`, 1))},
		{name: "star server id", body: []byte(strings.Replace(fixtureEngineA, `"server_id": "engine-a"`, `"server_id": "eng*"`, 1))},
		{name: "greater server id", body: []byte(strings.Replace(fixtureEngineA, `"server_id": "engine-a"`, `"server_id": "eng>"`, 1))},
		{name: "slash server id", body: []byte(strings.Replace(fixtureEngineA, `"server_id": "engine-a"`, `"server_id": "eng/1"`, 1))},
		{name: "empty server id", body: []byte(strings.Replace(fixtureEngineA, `"server_id": "engine-a"`, `"server_id": ""`, 1))},
		{name: "missing live", body: []byte(strings.Replace(fixtureEngineA, "  \"live\": true,\n", "", 1))},
		{name: "provider string", body: []byte(strings.Replace(fixtureEngineA, `"nats_connected": true,`, `"nats_connected": true, "execution_providers": "CPUExecutionProvider",`, 1))},
		{name: "version number", body: []byte(strings.Replace(fixtureEngineA, `"nats_connected": true,`, `"nats_connected": true, "ort_version": 1,`, 1))},
		{name: "not an object", body: []byte(`[]`)},
		{name: "not utf-8", body: []byte("{\"schema\":1,\"server_id\":\"a\xffb\",\"live\":true,\"ready\":true,\"nats_connected\":true,\"models\":[]}")},
		{name: "trailing value", body: append([]byte(fixtureEngineA), []byte(`{"schema":1}`)...)},
	}
}

func withPending(n int) []byte {
	return []byte(strings.Replace(fixtureEngineA,
		`"inference_pending_request_count": 0`,
		`"inference_pending_request_count": `+strconv.Itoa(n), 1))
}

func counterSnapshot(serverID, model, version string, success, duration int) string {
	return `{
	  "schema": 1,
	  "server_id": "` + serverID + `",
	  "live": true,
	  "ready": true,
	  "nats_connected": true,
	  "models": [{
	    "model": "` + model + `",
	    "version": "` + version + `",
	    "state": "READY",
	    "reason": "",
	    "inference_request_success": ` + strconv.Itoa(success) + `,
	    "inference_request_failure": {"REJECTED": 0, "CANCELED": 0, "BACKEND": 0, "OTHER": 0},
	    "inference_count": 1,
	    "inference_exec_count": 1,
	    "inference_pending_request_count": 0,
	    "inference_request_duration_us": ` + strconv.Itoa(duration) + `,
	    "inference_queue_duration_us": 0,
	    "inference_compute_infer_duration_us": 0,
	    "inference_model_ready": 1
	  }]
	}`
}

func nodeIDs(nodes []Node) []string {
	ids := make([]string, len(nodes))
	for i, node := range nodes {
		ids[i] = node.NodeUID
	}
	return ids
}
