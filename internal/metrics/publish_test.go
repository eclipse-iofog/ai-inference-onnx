package metrics

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestPublisherTickAndStateChange(t *testing.T) {
	reg := New()
	reg.Record(ran("add", "1", Request{Success: true, BatchSize: 1}))
	h := newHarness(reg)
	h.models.Store([]ModelState{{Model: "add", Version: "1", State: StateReady}})
	h.ready.Store(true)
	h.start()
	defer h.stop()

	h.ticks <- time.Now()
	first := h.next(t)
	if first.subject != "inference.metrics.engines.engine-1" {
		t.Fatalf("subject %s", first.subject)
	}
	doc := decodeDoc(t, first.body)
	if doc.Schema != 1 || !doc.Live || !doc.Ready || !doc.NATSConnected || doc.ServerID != "engine-1" {
		t.Fatalf("document %+v", doc)
	}
	if len(doc.Models) != 1 || doc.Models[0].Success != 1 || doc.Models[0].Model != "add" {
		t.Fatalf("models %+v", doc.Models)
	}
	h.refuseExtra(t)

	h.notes <- []ModelState{
		{Model: "add", Version: "1", State: StateReady},
		{Model: "other", Version: "2", State: StateUnavailable, Reason: "missing"},
	}
	second := h.next(t)
	doc = decodeDoc(t, second.body)
	if len(doc.Models) != 2 || doc.Models[0].Model != "add" || doc.Models[1].Model != "other" || doc.Models[1].Ready != 0 {
		t.Fatalf("state change %+v", doc.Models)
	}
	if h.modelCalls.Load() != 1 {
		t.Fatalf("interval reads %d", h.modelCalls.Load())
	}
	h.refuseExtra(t)

	h.ticks <- time.Now()
	third := h.next(t)
	doc = decodeDoc(t, third.body)
	if len(doc.Models) != 1 || doc.Models[0].Model != "add" || doc.Models[0].Success != 1 {
		t.Fatalf("unchanged tick %+v", doc.Models)
	}
	if h.modelCalls.Load() != 2 {
		t.Fatalf("interval reads %d", h.modelCalls.Load())
	}
}

func TestPublisherDropsWhileDisconnected(t *testing.T) {
	reg := New()
	h := newHarness(reg)
	h.connected.Store(false)
	h.models.Store([]ModelState{{Model: "add", Version: "1", State: StateReady}})
	h.start()
	defer h.stop()

	h.ticks <- time.Now()
	waitGauge(t, reg, "add", "1", 1)
	if h.sent.Load() != 0 {
		t.Fatalf("published %d documents while down", h.sent.Load())
	}
	_, samples := parseBody(t, scrape(t, reg))
	requireValue(t, samples, nameReady, labels("add", "1"), 1)

	h.notes <- []ModelState{{Model: "add", Version: "1", State: StateUnavailable, Reason: "missing"}}
	waitGauge(t, reg, "add", "1", 0)
	if h.sent.Load() != 0 {
		t.Fatalf("published %d documents after the state change", h.sent.Load())
	}

	reg.Record(ran("add", "1", Request{Success: true, BatchSize: 1}))
	h.connected.Store(true)
	h.ready.Store(false)
	h.ticks <- time.Now()
	doc := decodeDoc(t, h.next(t).body)
	if !doc.NATSConnected || doc.Ready || len(doc.Models) != 1 || doc.Models[0].Success != 1 || doc.Models[0].Ready != 1 {
		t.Fatalf("document after reconnect %+v models %+v", doc, doc.Models)
	}
}

func TestPublisherDropsAFailedPublish(t *testing.T) {
	reg := New()
	h := newHarness(reg)
	h.fail.Store(true)
	h.models.Store([]ModelState{{Model: "add", Version: "1", State: StateReady}})
	h.start()
	defer h.stop()

	reg.Record(ran("add", "1", Request{Success: true, BatchSize: 1}))
	h.ticks <- time.Now()
	waitGauge(t, reg, "add", "1", 1)
	if h.sent.Load() != 0 {
		t.Fatalf("failed publish was delivered %d times", h.sent.Load())
	}

	reg.Record(ran("add", "1", Request{Success: true, BatchSize: 1}))
	h.fail.Store(false)
	h.ticks <- time.Now()
	doc := decodeDoc(t, h.next(t).body)
	if len(doc.Models) != 1 || doc.Models[0].Success != 2 {
		t.Fatalf("fresh document %+v", doc.Models)
	}
	h.refuseExtra(t)
}

func TestPublisherReadyFollowsThePredicate(t *testing.T) {
	reg := New()
	h := newHarness(reg)
	h.models.Store([]ModelState{
		{Model: "add", Version: "1", State: StateReady},
		{Model: "other", Version: "1", State: StateUnavailable, Reason: "missing"},
	})
	h.ready.Store(true)
	h.start()
	defer h.stop()

	h.ticks <- time.Now()
	msg := h.next(t)
	doc := decodeDoc(t, msg.body)
	if !doc.Ready || len(doc.Models) != 2 {
		t.Fatalf("ready document %+v", doc)
	}
	if doc.Models[0].Model != "add" || doc.Models[0].Ready != 1 || doc.Models[1].Ready != 0 || doc.Models[1].State != string(StateUnavailable) {
		t.Fatalf("models %+v", doc.Models)
	}
	assertNoSnapshotFields(t, msg.body)
}

type sentMessage struct {
	subject string
	body    []byte
}

type harness struct {
	reg        *Registry
	ticks      chan time.Time
	notes      chan []ModelState
	out        chan sentMessage
	connected  atomic.Bool
	ready      atomic.Bool
	fail       atomic.Bool
	sent       atomic.Int32
	modelCalls atomic.Int32
	models     atomic.Value
	cancel     context.CancelFunc
}

func newHarness(reg *Registry) *harness {
	h := &harness{
		reg:   reg,
		ticks: make(chan time.Time, 4),
		notes: make(chan []ModelState, 4),
		out:   make(chan sentMessage, 4),
	}
	h.connected.Store(true)
	h.models.Store([]ModelState{})
	return h
}

func (h *harness) start() {
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	go NewPublisher(PublisherConfig{
		Registry: h.reg,
		ServerID: "engine-1",
		Interval: time.Hour,
		Live:     func() bool { return true },
		Ready:    func() bool { return h.ready.Load() },
		Connected: func() bool {
			return h.connected.Load()
		},
		Publish: func(subject string, body []byte) error {
			if h.fail.Load() {
				return errors.New("publish failed")
			}
			copied := append([]byte(nil), body...)
			h.sent.Add(1)
			h.out <- sentMessage{subject: subject, body: copied}
			return nil
		},
		Models: func() []ModelState {
			h.modelCalls.Add(1)
			models, ok := h.models.Load().([]ModelState)
			if !ok {
				return []ModelState{}
			}
			return append([]ModelState(nil), models...)
		},
		Notes: h.notes,
		Ticks: h.ticks,
	}).Run(ctx)
}

func (h *harness) stop() {
	h.cancel()
}

func (h *harness) next(t *testing.T) sentMessage {
	t.Helper()
	select {
	case msg := <-h.out:
		return msg
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for a snapshot")
		return sentMessage{}
	}
}

func (h *harness) refuseExtra(t *testing.T) {
	t.Helper()
	select {
	case msg := <-h.out:
		t.Fatalf("extra snapshot %s %s", msg.subject, msg.body)
	case <-time.After(30 * time.Millisecond):
	}
}

func decodeDoc(t *testing.T, body []byte) snapshotDoc {
	t.Helper()
	var doc snapshotDoc
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	return doc
}

func waitGauge(t *testing.T, reg *Registry, model, version string, want float64) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		_, samples := parseBody(t, scrape(t, reg))
		got, ok := findSample(t, samples, nameReady, labels(model, version))
		if ok && got.value == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("gauge %s/%s did not become %v", model, version, want)
}
