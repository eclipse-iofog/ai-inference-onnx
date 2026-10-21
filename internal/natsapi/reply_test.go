package natsapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
)

type recordingPublisher struct {
	limit int64
	sent  []*nats.Msg
}

func (p *recordingPublisher) MaxPayload() int64 { return p.limit }

func (p *recordingPublisher) PublishMsg(m *nats.Msg) error {
	p.sent = append(p.sent, m)
	return nil
}

func TestWriteReplySlicesFitThePayloadLimit(t *testing.T) {
	header := nats.Header{HeaderContentType: []string{contentJSON}}
	first := headerBytes(nats.Header{HeaderContentType: []string{contentJSON}, HeaderStatus: []string{"200"}})
	body := bytes.Repeat([]byte("x"), 100)
	for _, limit := range []int64{int64(first), int64(first) + 1, int64(first) + 30, 1 << 20} {
		pub := &recordingPublisher{limit: limit}
		if err := writeReply(pub, "inbox", 200, header, body); err != nil {
			t.Fatalf("limit %d: %v", limit, err)
		}
		last := pub.sent[len(pub.sent)-1]
		if last.Header != nil || len(last.Data) != 0 {
			t.Fatalf("limit %d: last message is not the terminator: %+v", limit, last)
		}
		if pub.sent[0].Header.Get(HeaderStatus) != "200" || pub.sent[0].Header.Get(HeaderContentType) != contentJSON {
			t.Fatalf("limit %d: first headers %v", limit, pub.sent[0].Header)
		}
		var joined []byte
		for i, m := range pub.sent[:len(pub.sent)-1] {
			if framedSize(m) > limit {
				t.Fatalf("limit %d: message %d is %d bytes with headers %v", limit, i, len(m.Data), m.Header)
			}
			if i > 0 && (m.Header != nil || len(m.Data) == 0) {
				t.Fatalf("limit %d: later slice %d headers %v, %d bytes", limit, i, m.Header, len(m.Data))
			}
			joined = append(joined, m.Data...)
		}
		if !bytes.Equal(joined, body) {
			t.Fatalf("limit %d: joined %q", limit, joined)
		}
	}
	if header.Get(HeaderStatus) != "" {
		t.Fatal("the caller's headers were changed")
	}
}

func TestWriteReplyEmptyBodyIsHeadersThenTerminator(t *testing.T) {
	pub := &recordingPublisher{limit: 1 << 20}
	if err := writeReply(pub, "inbox", 400, nil, nil); err != nil {
		t.Fatal(err)
	}
	if len(pub.sent) != 2 {
		t.Fatalf("%d messages", len(pub.sent))
	}
	if pub.sent[0].Header.Get(HeaderStatus) != "400" || len(pub.sent[0].Data) != 0 {
		t.Fatalf("first %+v", pub.sent[0])
	}
	if pub.sent[1].Header != nil || len(pub.sent[1].Data) != 0 {
		t.Fatalf("terminator %+v", pub.sent[1])
	}
}

func TestWriteReplyRefusesHeadersAboveTheLimit(t *testing.T) {
	for _, limit := range []int64{0, 8} {
		pub := &recordingPublisher{limit: limit}
		if err := writeReply(pub, "inbox", 200, nil, []byte("x")); err == nil {
			t.Fatalf("limit %d: no error", limit)
		}
		if len(pub.sent) != 0 {
			t.Fatalf("limit %d: %d messages sent", limit, len(pub.sent))
		}
	}
}

func framedSize(m *nats.Msg) int64 {
	if len(m.Header) == 0 {
		return int64(len(m.Data))
	}
	return int64(len(m.Data) + headerBytes(m.Header))
}

func TestSubscriptionsUseTheQueueGroup(t *testing.T) {
	url := startServer(t, 0)
	subs := serve(t, url, Config{})
	var subjects []string
	for _, sub := range subs {
		if sub.Queue != QueueGroup {
			t.Fatalf("%s queue %q", sub.Subject, sub.Queue)
		}
		subjects = append(subjects, sub.Subject)
	}
	want := []string{
		"inference.v2.health.live",
		"inference.v2.health.ready",
		"inference.v2.server.metadata",
		"inference.v2.models",
		"inference.v2.models.>",
		"inference.v2.repository.index",
	}
	if !slices.Equal(subjects, want) || QueueGroup != "inference" {
		t.Fatalf("subjects %v queue %q", subjects, QueueGroup)
	}
}

func TestOneQueueMemberAnswersEachRequest(t *testing.T) {
	url := startServer(t, 0)
	serve(t, url, Config{})
	serve(t, url, Config{})
	client := connect(t, url)

	for _, subject := range []string{"inference.v2.health.live", "inference.v2.models.missing.ready"} {
		inbox := client.NewInbox()
		sub, err := client.SubscribeSync(inbox)
		if err != nil {
			t.Fatal(err)
		}
		got := readStream(t, sub, func() error {
			return client.PublishRequest(subject, inbox, nil)
		})
		if len(got.slices) != 1 {
			t.Fatalf("%s: %d data slices", subject, len(got.slices))
		}
		if extra, err := sub.NextMsg(300 * time.Millisecond); !errors.Is(err, nats.ErrTimeout) {
			t.Fatalf("%s: a second member answered: %+v %v", subject, extra, err)
		}
		_ = sub.Unsubscribe()
	}
}

func TestLiveIsStatusThenTerminator(t *testing.T) {
	url := startServer(t, 0)
	serve(t, url, Config{})
	client := connect(t, url)

	got := request(t, client, "inference.v2.health.live", nil, nil)
	wantStatus(t, got, 200)
	if len(got.slices) != 1 || len(got.body) != 0 {
		t.Fatalf("live slices %d body %q", len(got.slices), got.body)
	}
	if len(got.header) == 0 {
		t.Fatal("a headerless empty message was taken as the live reply")
	}
}

func TestReadyFollowsTheReadyFunction(t *testing.T) {
	cases := []struct {
		name   string
		ready  func() bool
		status int
	}{
		{name: "no ready model", ready: func() bool { return false }, status: 400},
		{name: "no ready function", status: 400},
		{name: "a ready model", ready: func() bool { return true }, status: 200},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			url := startServer(t, 0)
			serve(t, url, Config{Ready: c.ready})
			got := request(t, connect(t, url), "inference.v2.health.ready", nil, nil)
			wantStatus(t, got, c.status)
			if len(got.slices) != 1 || len(got.body) != 0 {
				t.Fatalf("ready slices %d body %q", len(got.slices), got.body)
			}
		})
	}
}

func TestSmallPayloadLimitSplitsServerMetadata(t *testing.T) {
	const limit = 96
	url := startServer(t, limit)
	serve(t, url, Config{})
	client := connect(t, url)
	if client.MaxPayload() != limit {
		t.Fatalf("server payload limit %d", client.MaxPayload())
	}

	got := request(t, client, "inference.v2.server.metadata", nil, nil)
	wantStatus(t, got, 200)
	if len(got.slices) < 2 {
		t.Fatalf("%d data slices", len(got.slices))
	}
	if got.header.Get(HeaderContentType) != contentJSON {
		t.Fatalf("first slice headers %v", got.header)
	}
	for i, slice := range got.slices[1:] {
		if slice.Header != nil || len(slice.Data) == 0 {
			t.Fatalf("slice %d headers %v, %d bytes", i+1, slice.Header, len(slice.Data))
		}
	}
	var doc serverMetadata
	if err := json.Unmarshal(got.body, &doc); err != nil {
		t.Fatalf("joined body %q: %v", got.body, err)
	}
	if doc.Name != "inference" || doc.Version != "dev" || !slices.Contains(doc.Extensions, "binary_tensor_data") {
		t.Fatalf("server metadata %+v", doc)
	}
}

func TestReadsMatchTheRESTBodies(t *testing.T) {
	url := startServer(t, 0)
	catalog := append(readyAdd(), fakeCatalogLoading("detector")...)
	serve(t, url, Config{Catalog: catalog})
	client := connect(t, url)

	got := request(t, client, "inference.v2.models", nil, nil)
	wantStatus(t, got, 200)
	if string(got.body) != `{"models":["add","detector"]}` {
		t.Fatalf("model list %s", got.body)
	}

	got = request(t, client, "inference.v2.models.add.metadata", nil, nil)
	wantStatus(t, got, 200)
	var meta modelMetadata
	if err := json.Unmarshal(got.body, &meta); err != nil {
		t.Fatal(err)
	}
	if meta.Name != "add" || !slices.Equal(meta.Versions, []string{"1"}) || len(meta.Inputs) != 2 || meta.Platform != modelPlatform {
		t.Fatalf("metadata %+v", meta)
	}
	wantStatus(t, request(t, client, "inference.v2.models.add.versions.1.metadata", nil, nil), 200)
	wantStatus(t, request(t, client, "inference.v2.models.add.versions.2.metadata", nil, nil), 404)

	got = request(t, client, "inference.v2.models.add.ready", nil, nil)
	wantStatus(t, got, 200)
	if len(got.body) != 0 {
		t.Fatalf("model ready body %q", got.body)
	}
	got = request(t, client, "inference.v2.models.detector.versions.1.ready", nil, nil)
	wantStatus(t, got, 503)

	got = request(t, client, "inference.v2.repository.index", nil, []byte(`{"ready":true}`))
	wantStatus(t, got, 200)
	if string(got.body) != `[{"name":"add","version":"1","state":"READY","reason":""}]` {
		t.Fatalf("ready index %s", got.body)
	}
	got = request(t, client, "inference.v2.repository.index", nil, []byte(`{}`))
	wantStatus(t, got, 200)
	if !bytes.Contains(got.body, []byte(`"state":"LOADING"`)) {
		t.Fatalf("index %s", got.body)
	}
	got = request(t, client, "inference.v2.repository.index", nil, nil)
	wantStatus(t, got, 400)
	got.errorText(t)
}

func TestSubjectsOutsideTheTable(t *testing.T) {
	url := startServer(t, 0)
	runner := &fakeRunner{}
	subs := serve(t, url, Config{Catalog: readyAdd(), Runner: runner, MaxPayloadBytes: 1 << 20})
	client := connect(t, url)

	for _, subject := range []string{"inference.v2.models.add.load", "inference.v2.models.add.unload", "inference.v2.models.add.versions.1.load"} {
		got := request(t, client, subject, nil, addJSONBody("x"))
		wantStatus(t, got, 404)
		got.errorText(t)
	}
	got := request(t, client, "inference.v2.models.ad*d.infer", nil, addJSONBody("x"))
	wantStatus(t, got, 400)
	got.errorText(t)
	got = request(t, client, "inference.v2.models.add.versions.1>.infer", nil, addJSONBody("x"))
	wantStatus(t, got, 400)
	if runner.calls.Load() != 0 {
		t.Fatalf("the runner was called %d times", runner.calls.Load())
	}

	noResponders(t, client, "inference.v2.repository.models.add.load")
	noResponders(t, client, "inference.v2.repository.models.add.unload")
	noResponders(t, client, "inference.xfer.abc")

	if err := client.Publish("inference.xfer.abc", []byte("slice")); err != nil {
		t.Fatal(err)
	}
	if err := client.Flush(); err != nil {
		t.Fatal(err)
	}
	wantStatus(t, request(t, client, "inference.v2.health.live", nil, nil), 200)
	for _, sub := range subs {
		if sub.Subject != "inference.v2.models.>" {
			continue
		}
		delivered, err := sub.Delivered()
		if err != nil {
			t.Fatal(err)
		}
		// The load, unload, and wildcard requests above.
		if delivered != 5 {
			t.Fatalf("models wildcard delivered %d messages", delivered)
		}
	}
}
