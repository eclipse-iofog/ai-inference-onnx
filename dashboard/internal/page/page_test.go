package page_test

import (
	"bytes"
	"errors"
	"io"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/eclipse-iofog/ai-inference-onnx/dashboard/internal/page"
	"github.com/eclipse-iofog/ai-inference-onnx/dashboard/internal/store"
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

func TestFixtureViews(t *testing.T) {
	snapshots, _ := keepFixtures(t)

	models := render(t, func(w io.Writer) error {
		return page.Models(w, snapshots, page.ConnectionUp)
	})
	if strings.Count(models, "wafer-defect") != 1 {
		t.Fatalf("wafer-defect count %d", strings.Count(models, "wafer-defect"))
	}
	if strings.Count(models, `class="model"`) != 1 {
		t.Fatal("model rows")
	}
	if !strings.Contains(models, `<span class="unknown">unknown</span>`) || strings.Contains(models, "0.050 ms") || strings.Contains(models, "0.100 ms") {
		t.Fatal("latency")
	}
	if !strings.Contains(models, `href="/engines/engine-a">engine-a</a>`) || !strings.Contains(models, `href="/engines/engine-b">engine-b</a>`) {
		t.Fatal("server ids")
	}
	if strings.Contains(models, "NATS is not configured.") || strings.Contains(models, "NATS is not connected.") {
		t.Fatal("connection sentence")
	}
	if strings.Contains(models, "No engines.") {
		t.Fatal("empty sentence")
	}
	if !strings.Contains(models, `<a class="nav active" href="/models">Models</a>`) {
		t.Fatal("models link")
	}
	if strings.Contains(models, `class="nav active" href="/nodes"`) {
		t.Fatal("nodes link marked active")
	}

	nodes := render(t, func(w io.Writer) error {
		return page.Nodes(w, snapshots, page.ConnectionUp)
	})
	if strings.Count(nodes, `class="card"`) != 2 || strings.Count(nodes, `class="engine"`) != 2 {
		t.Fatal("cards")
	}
	if strings.Index(nodes, "node-1") > strings.Index(nodes, "node-2") {
		t.Fatal("node order")
	}
	if !strings.Contains(nodes, `href="/engines/engine-a">wafer-engine</a>`) || !strings.Contains(nodes, `href="/engines/engine-b">wafer-engine-b</a>`) {
		t.Fatal("engine links")
	}
	if strings.Count(nodes, `chip-on">Live</span>`) != 2 || strings.Count(nodes, `chip-on">Ready</span>`) != 2 || strings.Count(nodes, `chip-on">NATS</span>`) != 2 {
		t.Fatal("status chips")
	}
	if !strings.Contains(nodes, `<a class="nav active" href="/nodes">Nodes</a>`) {
		t.Fatal("nodes link")
	}

	engineB := render(t, func(w io.Writer) error {
		return page.Engine(w, snapshots, "engine-b", page.ConnectionUp)
	})
	for _, text := range []string{
		"<h1>wafer-engine-b</h1>",
		"<dt>Server id</dt><dd>engine-b</dd>",
		"<dt>Node</dt><dd>node-2</dd>",
		"<dt>Application</dt><dd>line</dd>",
		`<dt>Live</dt><dd><span class="chip chip-on">Live</span></dd>`,
		`<dt>Ready</dt><dd><span class="chip chip-on">Ready</span></dd>`,
		`<dt>NATS connected</dt><dd><span class="chip chip-on">NATS</span></dd>`,
		`<dt>ONNX Runtime</dt><dd><span class="chip unknown">unknown</span></dd>`,
		`<dt>Execution providers</dt><dd><span class="chip unknown">unknown</span></dd>`,
		"<h2>wafer-defect</h2>",
		`<span class="chip">version:1</span>`,
		"<p>Queue depth 1</p>",
		`<span class="chip chip-on">Success 2</span>`,
		`<span class="chip">REJECTED 0</span>`,
		`<span class="chip">CANCELED 0</span>`,
		`<span class="chip">BACKEND 0</span>`,
		`<span class="chip chip-off">OTHER 1</span>`,
		`<span class="chip chip-off">Failures 1</span>`,
		"<p>Average batch size 4.00</p>",
		`<span class="state-ready">READY</span>`,
		"Last snapshot 2026-10-16T12:00:10Z",
		"<h2>Request latency</h2>",
		"<h2>Request rate</h2>",
		"<h2>Queue latency</h2>",
		"<h2>Compute latency</h2>",
		"<h2>Queue depth</h2>",
		"Last 15 minutes.",
		"Waiting for the next snapshot.",
		"Refreshes every 10 seconds.",
	} {
		if !strings.Contains(engineB, text) {
			t.Fatalf("missing %s", text)
		}
	}
	if strings.Contains(engineB, `class="sample"`) || strings.Count(engineB, "<svg") != 1 {
		t.Fatal("sample table or a latency graph before the second snapshot")
	}
	if strings.Contains(engineB, "This engine is stale.") || strings.Contains(engineB, "This engine is unknown.") {
		t.Fatal("failure sentence")
	}
	if strings.Contains(engineB, `class="nav active"`) {
		t.Fatal("sidebar marked a link active")
	}
	if strings.Contains(engineB, "<canvas") {
		t.Fatal("canvas chart")
	}

	engineA := render(t, func(w io.Writer) error {
		return page.Engine(w, snapshots, "engine-a", page.ConnectionUp)
	})
	if !strings.Contains(engineA, "<h1>wafer-engine</h1>") || !strings.Contains(engineA, "<p>Queue depth 0</p>") {
		t.Fatal("engine-a queue")
	}
	if !strings.Contains(engineA, "<p>Average batch size 1.00</p>") || !strings.Contains(engineA, "Request latency unknown") {
		t.Fatal("engine-a batch or sample latency")
	}
	if strings.Contains(engineA, "0.050 ms") {
		t.Fatal("engine-a lifetime latency")
	}
}

func TestIntervalGraph(t *testing.T) {
	at := fixtureAt()
	now := at
	snapshots := store.New(func() time.Time { return now })
	if !snapshots.Accept([]byte(fixtureEngineA)) {
		t.Fatal("first snapshot was dropped")
	}
	now = at.Add(10 * time.Second)
	next := strings.Replace(fixtureEngineA, `"inference_request_success": 2`, `"inference_request_success": 4`, 1)
	next = strings.Replace(next, `"inference_request_duration_us": 100`, `"inference_request_duration_us": 300`, 1)
	next = strings.Replace(next, `"inference_queue_duration_us": 10`, `"inference_queue_duration_us": 30`, 1)
	next = strings.Replace(next, `"inference_compute_infer_duration_us": 80`, `"inference_compute_infer_duration_us": 180`, 1)
	next = strings.Replace(next, `"inference_request_failure": {"REJECTED": 0, "CANCELED": 0, "BACKEND": 0, "OTHER": 0}`, `"inference_request_failure": {"REJECTED": 0, "CANCELED": 0, "BACKEND": 0, "OTHER": 1}`, 1)
	if !snapshots.Accept([]byte(next)) {
		t.Fatal("second snapshot was dropped")
	}
	engine := render(t, func(w io.Writer) error {
		return page.Engine(w, snapshots, "engine-a", page.ConnectionUp)
	})
	for _, text := range []string{
		"<svg",
		"<circle",
		"0.100 ms",
		"0.010 ms",
		"0.050 ms",
		"0.300 /s",
		"33.3%",
		"<li class=\"rate-success\">Success</li>",
		"<li class=\"rate-failure\">Failure</li>",
		"<li class=\"rate-other\">OTHER</li>",
		"wafer-defect:1 2026-10-16T12:00:10Z 0.100 ms",
	} {
		if !strings.Contains(engine, text) {
			t.Fatalf("missing %s", text)
		}
	}
	if strings.Contains(engine, "<canvas") || strings.Contains(engine, "0.075 ms") || strings.Contains(engine, ">REJECTED</li>") {
		t.Fatal("lifetime latency or a canvas chart")
	}
	if strings.Contains(engine, "<li class=\"rate-rejected\">REJECTED</li>") || strings.Contains(engine, "<li class=\"rate-canceled\">CANCELED</li>") {
		t.Fatal("zero failure reasons")
	}
	selected := render(t, func(w io.Writer) error {
		return page.EngineModel(w, snapshots, "engine-a", "wafer-defect", page.ConnectionUp)
	})
	if !strings.Contains(selected, "0.100 ms") || !strings.Contains(selected, `chip-on" href="/engines/engine-a?model=wafer-defect"`) {
		t.Fatal("model filter")
	}
	missing := render(t, func(w io.Writer) error {
		return page.EngineModel(w, snapshots, "engine-a", "missing", page.ConnectionUp)
	})
	if strings.Count(missing, "This model is not on the latest snapshot.") != 5 || strings.Contains(missing, "<svg") || strings.Contains(missing, "version:1") {
		t.Fatal("missing model")
	}

	now = at.Add(20 * time.Second)
	third := strings.Replace(next, `"inference_request_success": 4`, `"inference_request_success": 6`, 1)
	third = strings.Replace(third, `"inference_request_duration_us": 300`, `"inference_request_duration_us": 500`, 1)
	if !snapshots.Accept([]byte(third)) {
		t.Fatal("third snapshot was dropped")
	}
	line := render(t, func(w io.Writer) error {
		return page.Engine(w, snapshots, "engine-a", page.ConnectionUp)
	})
	if !strings.Contains(line, "<polyline") || strings.Contains(line, "<canvas") {
		t.Fatal("latency line")
	}

	now = at.Add(30 * time.Second)
	reset := strings.Replace(third, `"inference_request_success": 6`, `"inference_request_success": 0`, 1)
	reset = strings.Replace(reset, `"inference_request_duration_us": 500`, `"inference_request_duration_us": 0`, 1)
	reset = strings.Replace(reset, `"inference_request_duration_us": 300`, `"inference_request_duration_us": 0`, 1)
	if !snapshots.Accept([]byte(reset)) {
		t.Fatal("reset was dropped")
	}
	after := render(t, func(w io.Writer) error {
		return page.Engine(w, snapshots, "engine-a", page.ConnectionUp)
	})
	if strings.Contains(after, "Request latency unknown") || !strings.Contains(after, "0.100 ms") {
		t.Fatal("reset interval")
	}
}

func TestQuietIntervalKeepsTheWindow(t *testing.T) {
	at := fixtureAt()
	now := at
	snapshots := store.New(func() time.Time { return now })
	if !snapshots.Accept([]byte(counterSnapshot("engine-a", "wafer-defect", "1", 2, 100))) {
		t.Fatal("first snapshot was dropped")
	}
	now = at.Add(10 * time.Second)
	if !snapshots.Accept([]byte(counterSnapshot("engine-a", "wafer-defect", "1", 4, 300))) {
		t.Fatal("second snapshot was dropped")
	}
	now = at.Add(20 * time.Second)
	if !snapshots.Accept([]byte(counterSnapshot("engine-a", "wafer-defect", "1", 4, 300))) {
		t.Fatal("quiet snapshot was dropped")
	}
	engine := render(t, func(w io.Writer) error {
		return page.Engine(w, snapshots, "engine-a", page.ConnectionUp)
	})
	if strings.Contains(engine, "Request latency unknown") || !strings.Contains(engine, "0.100 ms") || !strings.Contains(engine, "0.100 /s") {
		t.Fatal("quiet interval cleared the window")
	}
	models := render(t, func(w io.Writer) error {
		return page.Models(w, snapshots, page.ConnectionUp)
	})
	row := between(t, models, "<tbody>", "</tbody>")
	if !strings.Contains(row, "0.100 ms") || !strings.Contains(row, "0.100 /s") || strings.Contains(row, "unknown") {
		t.Fatal("models window")
	}
	nodes := render(t, func(w io.Writer) error {
		return page.Nodes(w, snapshots, page.ConnectionUp)
	})
	if !strings.Contains(nodes, "0.100 ms") || strings.Contains(between(t, nodes, "<tbody>", "</tbody>"), "unknown") {
		t.Fatal("nodes window")
	}
}

func TestGraphKeepsFifteenMinutes(t *testing.T) {
	at := fixtureAt()
	now := at
	snapshots := store.New(func() time.Time { return now })
	for i := 0; i <= 120; i++ {
		now = at.Add(time.Duration(i) * 10 * time.Second)
		if !snapshots.Accept([]byte(counterSnapshot("engine-a", "wafer-defect", "1", 1+i, i*100))) {
			t.Fatalf("snapshot %d was dropped", i)
		}
	}
	engine := render(t, func(w io.Writer) error {
		return page.Engine(w, snapshots, "engine-a", page.ConnectionUp)
	})
	if strings.Contains(engine, "2026-10-16T12:00:10Z") {
		t.Fatal("a point older than 15 minutes stayed on the graph")
	}
	if !strings.Contains(engine, "2026-10-16T12:05:10Z 0.100 ms") || !strings.Contains(engine, "2026-10-16T12:20:00Z 0.100 ms") {
		t.Fatal("newest latency")
	}
}

func TestLatencyGraphUsesALogScale(t *testing.T) {
	at := fixtureAt()
	now := at
	snapshots := store.New(func() time.Time { return now })
	for i, success := range []int{1, 2} {
		now = at.Add(time.Duration(i) * 10 * time.Second)
		body := `{
		  "schema": 1,
		  "server_id": "engine-a",
		  "live": true,
		  "ready": true,
		  "nats_connected": true,
		  "models": [
		    {"model": "turbofan-rul", "version": "1", "state": "READY", "reason": "",
		      "inference_request_success": ` + strconv.Itoa(success) + `,
		      "inference_request_failure": {"REJECTED": 0, "CANCELED": 0, "BACKEND": 0, "OTHER": 0},
		      "inference_count": 1, "inference_exec_count": 1, "inference_pending_request_count": 0,
		      "inference_request_duration_us": ` + strconv.Itoa(success*200) + `,
		      "inference_queue_duration_us": 0, "inference_compute_infer_duration_us": ` + strconv.Itoa(success*200) + `,
		      "inference_model_ready": 1},
		    {"model": "wafer-defect", "version": "1", "state": "READY", "reason": "",
		      "inference_request_success": ` + strconv.Itoa(success) + `,
		      "inference_request_failure": {"REJECTED": 0, "CANCELED": 0, "BACKEND": 0, "OTHER": 0},
		      "inference_count": 1, "inference_exec_count": 1, "inference_pending_request_count": 0,
		      "inference_request_duration_us": ` + strconv.Itoa(success*800000) + `,
		      "inference_queue_duration_us": 0, "inference_compute_infer_duration_us": ` + strconv.Itoa(success*800000) + `,
		      "inference_model_ready": 1}
		  ]
		}`
		if !snapshots.Accept([]byte(body)) {
			t.Fatalf("snapshot %d was dropped", i)
		}
	}
	engine := render(t, func(w io.Writer) error {
		return page.Engine(w, snapshots, "engine-a", page.ConnectionUp)
	})
	if strings.Count(engine, "milliseconds, log scale") != 2 || strings.Contains(engine, "Queue latency</h2>\n<p class=\"unit\">milliseconds, log scale") {
		t.Fatal("log scale")
	}
	if !strings.Contains(engine, "0.200 ms") || !strings.Contains(engine, "800.000 ms") {
		t.Fatal("both latencies")
	}
}

func TestStaleEngineLeavesTheLists(t *testing.T) {
	snapshots, now := keepFixtures(t)
	at := fixtureAt()

	*now = at.Add(29 * time.Second)
	nodes := render(t, func(w io.Writer) error {
		return page.Nodes(w, snapshots, page.ConnectionUp)
	})
	if strings.Count(nodes, `class="engine"`) != 2 || !strings.Contains(nodes, "engine-a") || !strings.Contains(nodes, "node-1") {
		t.Fatal("age 29 seconds left the nodes list")
	}

	*now = at.Add(30 * time.Second)
	nodes = render(t, func(w io.Writer) error {
		return page.Nodes(w, snapshots, page.ConnectionUp)
	})
	if strings.Contains(nodes, "engine-a") || strings.Contains(nodes, "node-1") || strings.Contains(nodes, "No engines.") {
		t.Fatal("age 30 seconds stayed on the nodes list")
	}
	if strings.Count(nodes, `class="engine"`) != 1 || !strings.Contains(nodes, "node-2") || !strings.Contains(nodes, "engine-b") {
		t.Fatal("the newer engine left the nodes list")
	}
	models := render(t, func(w io.Writer) error {
		return page.Models(w, snapshots, page.ConnectionUp)
	})
	if strings.Contains(models, "engine-a") || !strings.Contains(models, "engine-b") || strings.Contains(models, "0.050 ms") || strings.Contains(models, "0.150 ms") {
		t.Fatal("age 30 seconds stayed on the models list")
	}
	if strings.Count(models, "wafer-defect") != 1 {
		t.Fatal("model row")
	}
	stale := render(t, func(w io.Writer) error {
		return page.Engine(w, snapshots, "engine-a", page.ConnectionUp)
	})
	if strings.Count(stale, "This engine is stale.") != 2 {
		t.Fatalf("stale text %d", strings.Count(stale, "This engine is stale."))
	}
	if !strings.Contains(stale, `<h1 class="stale">This engine is stale.</h1>`) {
		t.Fatal("stale color")
	}
	for _, text := range []string{"This engine is unknown.", "wafer-defect", "Queue depth", "ONNX Runtime", "Execution providers", "No engines."} {
		if strings.Contains(stale, text) {
			t.Fatalf("stale page contains %s", text)
		}
	}
	live := render(t, func(w io.Writer) error {
		return page.Engine(w, snapshots, "engine-b", page.ConnectionUp)
	})
	if strings.Contains(live, "This engine is stale.") || !strings.Contains(live, "<p>Queue depth 1</p>") {
		t.Fatal("engine-b was marked stale")
	}

	*now = at.Add(40 * time.Second)
	nodes = render(t, func(w io.Writer) error {
		return page.Nodes(w, snapshots, page.ConnectionUp)
	})
	models = render(t, func(w io.Writer) error {
		return page.Models(w, snapshots, page.ConnectionUp)
	})
	if !strings.Contains(nodes, "No engines.") || strings.Contains(nodes, "engine-b") || strings.Count(nodes, `class="engine"`) != 0 {
		t.Fatal("both engines stayed on the nodes list")
	}
	if !strings.Contains(models, "No engines.") || strings.Contains(models, "wafer-defect") || strings.Count(models, `class="model"`) != 0 {
		t.Fatal("both engines stayed on the models list")
	}
	staleB := render(t, func(w io.Writer) error {
		return page.Engine(w, snapshots, "engine-b", page.ConnectionDown)
	})
	if !strings.Contains(staleB, "This engine is stale.") || !strings.Contains(staleB, "NATS is not connected.") {
		t.Fatal("stale page connection")
	}
	if strings.Contains(staleB, "NATS is not configured.") {
		t.Fatal("configured sentence")
	}

	*now = at.Add(41 * time.Second)
	if !snapshots.Accept([]byte(fixtureEngineA)) {
		t.Fatal("later snapshot was dropped")
	}
	nodes = render(t, func(w io.Writer) error {
		return page.Nodes(w, snapshots, page.ConnectionUp)
	})
	if !strings.Contains(nodes, "engine-a") || !strings.Contains(nodes, "node-1") {
		t.Fatal("later snapshot stayed off the nodes list")
	}
	restored := render(t, func(w io.Writer) error {
		return page.Engine(w, snapshots, "engine-a", page.ConnectionUp)
	})
	if strings.Contains(restored, "This engine is stale.") || !strings.Contains(restored, "wafer-defect") {
		t.Fatal("later snapshot stayed stale")
	}
}

func TestUnknownEngine(t *testing.T) {
	snapshots, _ := keepFixtures(t)
	unknown := render(t, func(w io.Writer) error {
		return page.Engine(w, snapshots, "engine-missing", page.ConnectionNotConfigured)
	})
	if strings.Count(unknown, "This engine is unknown.") != 2 {
		t.Fatalf("unknown text %d", strings.Count(unknown, "This engine is unknown."))
	}
	if !strings.Contains(unknown, `<h1 class="unknown">This engine is unknown.</h1>`) {
		t.Fatal("unknown color")
	}
	if !strings.Contains(unknown, "NATS is not configured.") || strings.Contains(unknown, "NATS is not connected.") {
		t.Fatal("connection sentence")
	}
	for _, text := range []string{"This engine is stale.", "wafer-defect", "engine-a", "Queue depth", "ONNX Runtime", "Execution providers", "No engines."} {
		if strings.Contains(unknown, text) {
			t.Fatalf("unknown page contains %s", text)
		}
	}

	var empty *store.Store
	again := render(t, func(w io.Writer) error {
		return page.Engine(w, empty, "engine-a", page.ConnectionUp)
	})
	if !strings.Contains(again, "This engine is unknown.") || strings.Contains(again, "No engines.") {
		t.Fatal("nil store")
	}
}

func TestConnectionSentences(t *testing.T) {
	snapshots, _ := keepFixtures(t)
	views := []struct {
		name  string
		write func(io.Writer, page.Connection) error
	}{
		{name: "nodes", write: func(w io.Writer, conn page.Connection) error {
			return page.Nodes(w, snapshots, conn)
		}},
		{name: "models", write: func(w io.Writer, conn page.Connection) error {
			return page.Models(w, snapshots, conn)
		}},
		{name: "engine", write: func(w io.Writer, conn page.Connection) error {
			return page.Engine(w, snapshots, "engine-b", conn)
		}},
	}
	for _, view := range views {
		t.Run(view.name, func(t *testing.T) {
			up := render(t, func(w io.Writer) error { return view.write(w, page.ConnectionUp) })
			if strings.Contains(up, "NATS is not configured.") || strings.Contains(up, "NATS is not connected.") {
				t.Fatal("up")
			}
			down := render(t, func(w io.Writer) error { return view.write(w, page.ConnectionDown) })
			if !strings.Contains(down, "NATS is not connected.") || strings.Contains(down, "NATS is not configured.") {
				t.Fatal("down")
			}
			if !strings.Contains(down, "engine-b") || strings.Contains(down, "No engines.") {
				t.Fatal("down hid the engine")
			}
			unset := render(t, func(w io.Writer) error { return view.write(w, page.ConnectionNotConfigured) })
			if !strings.Contains(unset, "NATS is not configured.") || strings.Contains(unset, "NATS is not connected.") {
				t.Fatal("not configured")
			}
			if !strings.Contains(unset, "engine-b") {
				t.Fatal("not configured hid the engine")
			}
		})
	}

	empty := store.New(func() time.Time { return fixtureAt() })
	for _, write := range []func(io.Writer) error{
		func(w io.Writer) error { return page.Nodes(w, empty, page.ConnectionUp) },
		func(w io.Writer) error { return page.Models(w, empty, page.ConnectionUp) },
	} {
		body := render(t, write)
		if !strings.Contains(body, "No engines.") {
			t.Fatal("empty live set")
		}
		if strings.Contains(body, "NATS is not configured.") || strings.Contains(body, "NATS is not connected.") {
			t.Fatal("empty live set connection")
		}
	}
	unset := render(t, func(w io.Writer) error {
		return page.Nodes(w, empty, page.ConnectionNotConfigured)
	})
	if !strings.Contains(unset, "No engines.") || !strings.Contains(unset, "NATS is not configured.") || strings.Contains(unset, "NATS is not connected.") {
		t.Fatal("unset empty nodes")
	}
}

func TestSnapshotTextIsEscaped(t *testing.T) {
	at := fixtureAt()
	snapshots := store.New(func() time.Time { return at })
	body := strings.Replace(fixtureEngineA, `"model": "wafer-defect"`, `"model": "<script><img>"`, 1)
	body = strings.Replace(body, `"reason": ""`, `"reason": "<script>"`, 1)
	body = strings.Replace(body, `"server_id": "engine-a"`, `"server_id": "eng<script"`, 1)
	if !snapshots.Accept([]byte(body)) {
		t.Fatal("snapshot was dropped")
	}
	models := render(t, func(w io.Writer) error {
		return page.Models(w, snapshots, page.ConnectionUp)
	})
	if strings.Contains(models, "<script") || strings.Contains(models, "<img") {
		t.Fatal("models page emitted a tag")
	}
	if !strings.Contains(models, "&lt;script&gt;&lt;img&gt;") || !strings.Contains(models, "eng&lt;script") {
		t.Fatal("escaped text was dropped")
	}
	engine := render(t, func(w io.Writer) error {
		return page.Engine(w, snapshots, "eng<script", page.ConnectionUp)
	})
	if strings.Contains(engine, "<script") || strings.Contains(engine, "<img") {
		t.Fatal("engine page emitted a tag")
	}
	if !strings.Contains(engine, "Reason &lt;script&gt;") || !strings.Contains(engine, "&lt;script&gt;&lt;img&gt;") {
		t.Fatal("engine text was dropped")
	}
}

func TestUnsetNodeIsLast(t *testing.T) {
	at := fixtureAt()
	snapshots := store.New(func() time.Time { return at })
	if !snapshots.Accept([]byte(fixtureEngineB)) || !snapshots.Accept([]byte(fixtureEngineA)) {
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
	if !snapshots.Accept([]byte(absent)) || !snapshots.Accept([]byte(empty)) {
		t.Fatal("unset node was dropped")
	}
	nodes := render(t, func(w io.Writer) error {
		return page.Nodes(w, snapshots, page.ConnectionUp)
	})
	node1 := strings.Index(nodes, "node-1")
	node2 := strings.Index(nodes, "node-2")
	unknown := strings.Index(nodes, "Node unknown")
	if node1 < 0 || node2 < node1 || unknown < node2 {
		t.Fatal("node order")
	}
	if strings.Count(nodes, `class="card unknown"`) != 1 {
		t.Fatal("unknown card")
	}
	card := nodes[unknown:]
	engineC := strings.Index(card, "engine-c")
	engineD := strings.Index(card, "engine-d")
	if engineC < 0 || engineD < engineC {
		t.Fatal("engine order")
	}
	if !strings.Contains(card, `href="/engines/engine-c">engine-c</a>`) || !strings.Contains(card, `chip-on">Live</span>`) || !strings.Contains(card, `chip-off">Ready</span>`) || !strings.Contains(card, `chip-off">NATS</span>`) {
		t.Fatal("engine-c")
	}
	if !strings.Contains(card, `href="/engines/engine-d">engine-d</a>`) || !strings.Contains(card, `chip-off">Live</span>`) {
		t.Fatal("engine-d")
	}
	models := render(t, func(w io.Writer) error {
		return page.Models(w, snapshots, page.ConnectionUp)
	})
	if strings.Count(models, `class="model"`) != 1 || strings.Contains(models, "No engines.") {
		t.Fatal("models page")
	}
	detail := render(t, func(w io.Writer) error {
		return page.Engine(w, snapshots, "engine-d", page.ConnectionUp)
	})
	if !strings.Contains(detail, "<h1>engine-d</h1>") {
		t.Fatal("heading")
	}
	if !strings.Contains(detail, `<dt>Node</dt><dd class="unknown">unknown</dd>`) || !strings.Contains(detail, `<dt>Application</dt><dd class="unknown">unknown</dd>`) {
		t.Fatal("unset platform names")
	}
	if !strings.Contains(detail, `<dt>Live</dt><dd><span class="chip chip-off">Live</span></dd>`) {
		t.Fatal("live")
	}
}

func TestRatiosAndSamples(t *testing.T) {
	at := fixtureAt()
	now := at
	snapshots := store.New(func() time.Time { return now })
	for i, pending := range []int{1, 2, 4} {
		now = at.Add(time.Duration(i) * 10 * time.Second)
		duration := 100
		if pending == 4 {
			duration = 300
		}
		if !snapshots.Accept([]byte(withCounters(pending, duration))) {
			t.Fatalf("sample %d was dropped", i)
		}
	}
	models := render(t, func(w io.Writer) error {
		return page.Models(w, snapshots, page.ConnectionUp)
	})
	if !strings.Contains(models, "0 /s") || strings.Contains(models, "0.150 ms") || strings.Contains(models, "0.100 ms") || strings.Contains(models, "0.083 ms") {
		t.Fatal("models latency")
	}
	if strings.Count(models, `href="/engines/engine-a"`) != 1 {
		t.Fatal("server ids")
	}
	engine := render(t, func(w io.Writer) error {
		return page.Engine(w, snapshots, "engine-a", page.ConnectionUp)
	})
	if !strings.Contains(engine, "<p>Queue depth 4</p>") || strings.Contains(engine, "<p>Queue depth 7</p>") || strings.Contains(engine, "<p>Queue depth 1</p>") {
		t.Fatal("queue depth")
	}
	if !strings.Contains(engine, "<svg") || strings.Contains(engine, "<canvas") {
		t.Fatal("queue graph")
	}
	first := strings.Index(engine, "wafer-defect:1 2026-10-16T12:00:00Z 1")
	second := strings.Index(engine, "wafer-defect:1 2026-10-16T12:00:10Z 2")
	third := strings.Index(engine, "wafer-defect:1 2026-10-16T12:00:20Z 4")
	if first < 0 || second < first || third < second {
		t.Fatal("sample order")
	}
	if !strings.Contains(engine, "No completed requests in this window.") || strings.Contains(engine, "0.150 ms") || strings.Contains(engine, "0.050 ms") {
		t.Fatal("sample latency")
	}
	nodes := render(t, func(w io.Writer) error {
		return page.Nodes(w, snapshots, page.ConnectionUp)
	})
	if strings.Count(nodes, `class="engine"`) != 1 || strings.Count(nodes, `class="card"`) != 1 {
		t.Fatal("one engine")
	}

	quiet := store.New(func() time.Time { return at })
	silentA := strings.Replace(fixtureEngineA, `"inference_request_success": 2`, `"inference_request_success": 0`, 1)
	silentB := strings.Replace(fixtureEngineB, `"inference_request_success": 2`, `"inference_request_success": 0`, 1)
	if !quiet.Accept([]byte(silentA)) || !quiet.Accept([]byte(silentB)) {
		t.Fatal("snapshot was dropped")
	}
	models = render(t, func(w io.Writer) error {
		return page.Models(w, quiet, page.ConnectionUp)
	})
	if !strings.Contains(models, `<span class="unknown">unknown</span>`) {
		t.Fatal("unknown latency")
	}
	if strings.Contains(models, "NaN") || strings.Contains(models, "+Inf") || strings.Contains(models, "-Inf") {
		t.Fatal("divide")
	}
	sample := render(t, func(w io.Writer) error {
		return page.Engine(w, quiet, "engine-a", page.ConnectionUp)
	})
	if !strings.Contains(sample, "Request latency unknown") || strings.Contains(sample, "NaN") || strings.Contains(sample, "+Inf") || strings.Contains(sample, "-Inf") {
		t.Fatal("sample latency")
	}

	zeroAt := at
	zero := store.New(func() time.Time { return zeroAt })
	if !zero.Accept([]byte(counterSnapshot("engine-z", "wafer-defect", "1", 2, 100))) {
		t.Fatal("zero duration was dropped")
	}
	zeroAt = at.Add(10 * time.Second)
	if !zero.Accept([]byte(counterSnapshot("engine-z", "wafer-defect", "1", 4, 100))) {
		t.Fatal("zero duration interval was dropped")
	}
	models = render(t, func(w io.Writer) error {
		return page.Models(w, zero, page.ConnectionUp)
	})
	row := between(t, models, "<tbody>", "</tbody>")
	if !strings.Contains(row, "0.000 ms") || strings.Contains(row, "unknown") {
		t.Fatal("known zero latency")
	}

	fractionAt := at
	fraction := store.New(func() time.Time { return fractionAt })
	if !fraction.Accept([]byte(fixtureEngineA)) {
		t.Fatal("snapshot was dropped")
	}
	fractionAt = at.Add(10 * time.Second)
	partial := strings.Replace(fixtureEngineA, `"inference_request_success": 2`, `"inference_request_success": 6`, 1)
	partial = strings.Replace(partial, `"inference_request_duration_us": 100`, `"inference_request_duration_us": 410`, 1)
	if !fraction.Accept([]byte(partial)) {
		t.Fatal("interval was dropped")
	}
	models = render(t, func(w io.Writer) error {
		return page.Models(w, fraction, page.ConnectionUp)
	})
	if !strings.Contains(models, "0.077 ms") || strings.Contains(models, "0.078") || strings.Contains(models, "77.5") {
		t.Fatal("truncated latency")
	}

	batch := store.New(func() time.Time { return at })
	ratio := strings.Replace(fixtureEngineA, `"inference_count": 2`, `"inference_count": 5`, 1)
	ratio = strings.Replace(ratio, `"inference_exec_count": 2`, `"inference_exec_count": 3`, 1)
	if !batch.Accept([]byte(ratio)) {
		t.Fatal("snapshot was dropped")
	}
	engine = render(t, func(w io.Writer) error {
		return page.Engine(w, batch, "engine-a", page.ConnectionUp)
	})
	if !strings.Contains(engine, "<p>Average batch size 1.66</p>") || strings.Contains(engine, "1.67") {
		t.Fatal("truncated batch")
	}

	noExec := store.New(func() time.Time { return at })
	stopped := strings.Replace(fixtureEngineA, `"inference_exec_count": 2`, `"inference_exec_count": 0`, 1)
	if !noExec.Accept([]byte(stopped)) {
		t.Fatal("snapshot was dropped")
	}
	engine = render(t, func(w io.Writer) error {
		return page.Engine(w, noExec, "engine-a", page.ConnectionUp)
	})
	if !strings.Contains(engine, `<p class="unknown">Average batch size unknown</p>`) || strings.Contains(engine, "NaN") || strings.Contains(engine, "+Inf") || strings.Contains(engine, "-Inf") {
		t.Fatal("unknown batch")
	}
}

func TestSecondModelNameAndStates(t *testing.T) {
	at := fixtureAt()
	snapshots := store.New(func() time.Time { return at })
	if !snapshots.Accept([]byte(fixtureEngineA)) {
		t.Fatal("engine-a was dropped")
	}
	other := strings.Replace(fixtureEngineB, `"model": "wafer-defect"`, `"model": "surface"`, 1)
	if !snapshots.Accept([]byte(other)) {
		t.Fatal("engine-b was dropped")
	}
	models := render(t, func(w io.Writer) error {
		return page.Models(w, snapshots, page.ConnectionUp)
	})
	if strings.Count(models, `class="model"`) != 2 {
		t.Fatal("rows")
	}
	if strings.Index(models, "surface") > strings.Index(models, "wafer-defect") {
		t.Fatal("row order")
	}
	if strings.Count(models, "wafer-defect") != 1 || strings.Count(models, "surface") != 1 {
		t.Fatal("names")
	}
	if !strings.Contains(models, `<span class="unknown">unknown</span>`) || strings.Contains(models, "0.050 ms") || strings.Contains(models, "0.150 ms") {
		t.Fatal("latency")
	}

	versions := store.New(func() time.Time { return at })
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
	if !versions.Accept([]byte(body)) {
		t.Fatal("snapshot was dropped")
	}
	models = render(t, func(w io.Writer) error {
		return page.Models(w, versions, page.ConnectionUp)
	})
	if strings.Count(models, `class="model"`) != 1 || !strings.Contains(models, `<span class="state-unavailable">UNAVAILABLE</span>`) {
		t.Fatal("one row")
	}
	engine := render(t, func(w io.Writer) error {
		return page.Engine(w, versions, "engine-a", page.ConnectionUp)
	})
	if strings.Count(engine, `class="card version"`) != 2 {
		t.Fatal("versions")
	}
	unavailable := strings.Index(engine, `<span class="state-unavailable">UNAVAILABLE</span>`)
	loading := strings.Index(engine, `<span class="state-loading">LOADING</span>`)
	if unavailable < 0 || loading < unavailable {
		t.Fatal("state order")
	}
	if !strings.Contains(engine, "Reason missing") || !strings.Contains(engine, "Reason reading") {
		t.Fatal("reasons")
	}
	if !strings.Contains(engine, `<span class="chip chip-off">REJECTED 1</span>`) || !strings.Contains(engine, `<span class="chip chip-off">Failures 1</span>`) {
		t.Fatal("failure sum")
	}
	if !strings.Contains(engine, "<p>Average batch size 1.00</p>") || !strings.Contains(engine, "<p>Average batch size 3.00</p>") {
		t.Fatal("batch")
	}
}

func TestRuntimeBadges(t *testing.T) {
	at := fixtureAt()
	now := at
	snapshots := store.New(func() time.Time { return now })
	body := strings.Replace(fixtureEngineA, "{\n", "{\n  \"image\": \"cpu\",\n", 1)
	body = strings.Replace(body, `"model": "wafer-defect"`, `"image": "cpu", "model": "wafer-defect"`, 1)
	body = strings.Replace(body, `"nats_connected": true,`, `"nats_connected": true, "execution_providers": ["TensorrtExecutionProvider", "CUDAExecutionProvider"], "ort_version": "1.31.0",`, 1)
	if !snapshots.Accept([]byte(body)) {
		t.Fatal("engine-a was dropped")
	}
	now = at.Add(10 * time.Second)
	if !snapshots.Accept([]byte(fixtureEngineB)) {
		t.Fatal("engine-b was dropped")
	}
	engineA := render(t, func(w io.Writer) error {
		return page.Engine(w, snapshots, "engine-a", page.ConnectionUp)
	})
	for _, text := range []string{
		`<dt>ONNX Runtime</dt><dd><span class="chip">1.31.0</span></dd>`,
		`<span class="chip">TensorrtExecutionProvider</span>`,
		`<span class="chip">CUDAExecutionProvider</span>`,
	} {
		if !strings.Contains(engineA, text) || strings.Contains(engineA, "Provider image") || strings.Contains(engineA, "cpu") {
			t.Fatalf("runtime badges %s", text)
		}
	}
	if strings.Index(engineA, "TensorrtExecutionProvider") > strings.Index(engineA, "CUDAExecutionProvider") {
		t.Fatal("provider order")
	}
	engineB := render(t, func(w io.Writer) error {
		return page.Engine(w, snapshots, "engine-b", page.ConnectionUp)
	})
	if !strings.Contains(engineB, `<span class="chip unknown">unknown</span>`) || strings.Contains(engineB, "1.31.0") || strings.Contains(engineB, "cpu") {
		t.Fatal("engine-b runtime")
	}
	models := render(t, func(w io.Writer) error {
		return page.Models(w, snapshots, page.ConnectionUp)
	})
	if strings.Contains(models, "cpu") || strings.Contains(models, "TensorrtExecutionProvider") || strings.Contains(models, "1.31.0") {
		t.Fatal("models page")
	}
}

func TestDroppedDocumentStaysOnThePage(t *testing.T) {
	at := fixtureAt()
	snapshots := store.New(func() time.Time { return at })
	if !snapshots.Accept([]byte(fixtureEngineA)) {
		t.Fatal("engine-a was dropped")
	}
	for _, body := range []string{
		strings.Replace(fixtureEngineA, `"schema": 1`, `"schema": 2`, 1),
		`{"schema":1,"server_id":"engine-a","live":true,"ready":true,"nats_connected":true,"models":null}`,
	} {
		if snapshots.Accept([]byte(body)) {
			t.Fatal("dropped document was kept")
		}
	}
	models := render(t, func(w io.Writer) error {
		return page.Models(w, snapshots, page.ConnectionUp)
	})
	if strings.Count(models, "wafer-defect") != 1 || !strings.Contains(models, "engine-a") {
		t.Fatal("kept engine left the models page")
	}
	nodes := render(t, func(w io.Writer) error {
		return page.Nodes(w, snapshots, page.ConnectionUp)
	})
	if !strings.Contains(nodes, "node-1") || !strings.Contains(nodes, "engine-a") {
		t.Fatal("kept engine left the nodes page")
	}
}

func TestWriterErrors(t *testing.T) {
	snapshots, _ := keepFixtures(t)
	var writer io.Writer
	if err := page.Nodes(writer, snapshots, page.ConnectionUp); err == nil {
		t.Fatal("nil writer")
	}
	broken := errWriter{}
	for _, write := range []func(io.Writer) error{
		func(w io.Writer) error { return page.Nodes(w, snapshots, page.ConnectionUp) },
		func(w io.Writer) error { return page.Models(w, snapshots, page.ConnectionUp) },
		func(w io.Writer) error { return page.Engine(w, snapshots, "engine-b", page.ConnectionUp) },
	} {
		if err := write(broken); err == nil {
			t.Fatal("write error")
		}
	}
}

func TestRenderRaces(t *testing.T) {
	snapshots, _ := keepFixtures(t)
	var group sync.WaitGroup
	for range 8 {
		group.Add(1)
		go func() {
			defer group.Done()
			for range 20 {
				var buf bytes.Buffer
				if err := page.Nodes(&buf, snapshots, page.ConnectionUp); err != nil {
					t.Error(err)
				}
				buf.Reset()
				if err := page.Models(&buf, snapshots, page.ConnectionUp); err != nil {
					t.Error(err)
				}
				buf.Reset()
				if err := page.Engine(&buf, snapshots, "engine-b", page.ConnectionUp); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	group.Wait()
}

func render(t *testing.T, write func(io.Writer) error) string {
	t.Helper()
	var buf bytes.Buffer
	if err := write(&buf); err != nil {
		t.Fatalf("render: %v", err)
	}
	body := buf.String()
	assertShell(t, body)
	return body
}

func assertShell(t *testing.T, body string) {
	t.Helper()
	for _, token := range []string{
		"--page: #111827;",
		"--card: #ffffff;",
		"--card-border: #374151;",
		"--card-radius: 16px;",
		"--text: #f3f4f6;",
		"--muted: #9ca3af;",
		"--nav: #d1d5db;",
		"--nav-hover: #1f2937;",
		"--nav-active: #374151;",
		"--nav-active-text: #ffffff;",
		"--rule: #6b7280;",
		"--ready: #16A34A;",
		"--loading: #FBBF24;",
		"--unavailable: #DC2626;",
		"--stale: #7A3BFF;",
		"--unknown: #9CA3AF;",
		`http-equiv="refresh"`,
		`content="10"`,
		"rgba(255, 255, 255, 0.05)",
		"rgba(55, 65, 81, 0.5)",
		`font-family: system-ui, -apple-system, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif;`,
		"a.nav:hover",
		"a.nav.active",
		"font-weight: 600",
	} {
		if !strings.Contains(body, token) {
			t.Fatalf("missing %s", token)
		}
	}
	lower := strings.ToLower(body)
	for _, bad := range []string{
		"#26d6f1",
		"#ff585d",
		"<script",
		"<img",
		"<canvas",
		"@font-face",
		"fonts.googleapis.com",
		"fonts.gstatic.com",
		"typekit",
		".woff",
		"tailwind",
		"url(",
	} {
		if strings.Contains(lower, bad) {
			t.Fatalf("forbidden %s", bad)
		}
	}
	if strings.Contains(lower, "<link") || strings.Count(body, "<style>") != 1 {
		t.Fatal("stylesheet")
	}
	nav := between(t, body, "<nav", "</nav>")
	if !strings.Contains(nav, "Inference") || strings.Count(nav, "<a ") != 2 {
		t.Fatalf("sidebar %s", nav)
	}
	if !strings.Contains(nav, `href="/nodes"`) || !strings.Contains(nav, `href="/models"`) || strings.Contains(nav, "/engines/") {
		t.Fatalf("sidebar links %s", nav)
	}
}

func between(t *testing.T, body, start, end string) string {
	t.Helper()
	i := strings.Index(body, start)
	j := strings.Index(body, end)
	if i < 0 || j < i {
		t.Fatalf("missing %s", start)
	}
	return body[i:j]
}

func keepFixtures(t *testing.T) (*store.Store, *time.Time) {
	t.Helper()
	at := fixtureAt()
	now := at
	snapshots := store.New(func() time.Time { return now })
	if !snapshots.Accept([]byte(fixtureEngineA)) {
		t.Fatal("engine-a was dropped")
	}
	now = at.Add(10 * time.Second)
	if !snapshots.Accept([]byte(fixtureEngineB)) {
		t.Fatal("engine-b was dropped")
	}
	return snapshots, &now
}

func fixtureAt() time.Time {
	return time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
}

func withCounters(pending, duration int) string {
	body := strings.Replace(fixtureEngineA, `"inference_pending_request_count": 0`, `"inference_pending_request_count": `+strconv.Itoa(pending), 1)
	return strings.Replace(body, `"inference_request_duration_us": 100`, `"inference_request_duration_us": `+strconv.Itoa(duration), 1)
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

type errWriter struct{}

func (errWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}
