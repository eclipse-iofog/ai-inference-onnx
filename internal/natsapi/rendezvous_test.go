package natsapi

import (
	"bytes"
	"context"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"

	"github.com/eclipse-iofog/ai-inference-onnx/internal/batch"
)

var acceptPattern = regexp.MustCompile(`^inference\.xfer\.[0-9a-f]{32}$`)

// manualClock fires a timer only when the test advances past its deadline.
// The call numbered holdAt waits on hold after signaling held, so a test can act while a slice is being taken.
type manualClock struct {
	mu     sync.Mutex
	now    time.Duration
	timers []manualTimer
	calls  int
	holdAt int
	held   chan struct{}
	hold   chan struct{}
}

type manualTimer struct {
	at time.Duration
	ch chan time.Time
}

func newManualClock() *manualClock {
	return &manualClock{held: make(chan struct{}, 1), hold: make(chan struct{})}
}

func (c *manualClock) After(d time.Duration) <-chan time.Time {
	ch := make(chan time.Time, 1)
	c.mu.Lock()
	c.calls++
	call := c.calls
	c.timers = append(c.timers, manualTimer{at: c.now + d, ch: ch})
	holdAt := c.holdAt
	c.mu.Unlock()
	if call == holdAt {
		c.held <- struct{}{}
		<-c.hold
	}
	return ch
}

func (c *manualClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now += d
	kept := c.timers[:0]
	for _, tm := range c.timers {
		if tm.at <= c.now {
			tm.ch <- time.Time{}
			continue
		}
		kept = append(kept, tm)
	}
	c.timers = kept
}

// countingPool records every buffer handed out and taken back. A nil inner pool allocates.
type countingPool struct {
	inner batch.Pool

	gets atomic.Int32
	puts atomic.Int32

	mu       sync.Mutex
	got      [][]byte
	returned [][]byte
	contents [][]byte
}

func (p *countingPool) Get(n int) ([]byte, error) {
	p.gets.Add(1)
	b := make([]byte, n)
	if p.inner != nil {
		var err error
		if b, err = p.inner.Get(n); err != nil {
			return nil, err
		}
	}
	p.mu.Lock()
	p.got = append(p.got, b)
	p.mu.Unlock()
	return b, nil
}

func (p *countingPool) Put(b []byte) {
	p.mu.Lock()
	p.returned = append(p.returned, b)
	p.contents = append(p.contents, append([]byte(nil), b...))
	p.mu.Unlock()
	p.puts.Add(1)
	if p.inner != nil {
		p.inner.Put(b)
	}
}

// waitReturned waits until the pool has taken back n buffers.
func (p *countingPool) waitReturned(t *testing.T, n int32) {
	t.Helper()
	deadline := time.Now().Add(replyWait)
	for p.puts.Load() < n {
		if time.Now().After(deadline) {
			t.Fatalf("%d buffers returned, want %d", p.puts.Load(), n)
		}
		time.Sleep(time.Millisecond)
	}
	if got := p.puts.Load(); got != n || p.gets.Load() != n {
		t.Fatalf("pool gets %d puts %d, want %d each", p.gets.Load(), got, n)
	}
}

// assertSuffixInPoolBuffer checks that the one buffer the pool handed out came back holding doc and suffix.
func (p *countingPool) assertSuffixInPoolBuffer(t *testing.T, doc, suffix []byte) {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.got) != 1 || len(p.returned) != 1 {
		t.Fatalf("pool handed out %d and took back %d buffers", len(p.got), len(p.returned))
	}
	if len(p.returned[0]) == 0 || &p.returned[0][0] != &p.got[0][0] {
		t.Fatal("the buffer taken back is not the one the pool handed out")
	}
	want := append(append([]byte(nil), doc...), suffix...)
	if !bytes.Equal(p.contents[0], want) {
		t.Fatalf("pool buffer holds %d bytes that are not the header and suffix", len(p.contents[0]))
	}
}

func startEmbedded(t *testing.T) *server.Server {
	t.Helper()
	srv, err := server.NewServer(&server.Options{
		Host:   "127.0.0.1",
		Port:   server.RANDOM_PORT,
		NoLog:  true,
		NoSigs: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	srv.Start()
	t.Cleanup(srv.Shutdown)
	if !srv.ReadyForConnections(replyWait) {
		t.Fatal("embedded NATS server did not start")
	}
	return srv
}

// serveUntilCleanup is serve with a context that is canceled before the handler waits, so a test that fails mid-upload still ends.
func serveUntilCleanup(t *testing.T, url string, cfg Config) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cfg.Context = ctx
	serve(t, url, cfg)
	t.Cleanup(cancel)
}

func rendezvousHeader(length ...string) nats.Header {
	return nats.Header{HeaderTransfer: []string{transferRendezvous}, HeaderSuffixLength: length}
}

// splitBinary is the add model's binary request as its JSON header and its suffix.
func splitBinary(t *testing.T, id string) (doc, suffix []byte) {
	t.Helper()
	body, header := addBinaryBody(id)
	n, err := strconv.Atoi(header.Get(HeaderContentLength))
	if err != nil {
		t.Fatal(err)
	}
	return body[:n], body[n:]
}

// accepted checks the reply to a rendezvous header and returns the upload subject and id.
func accepted(t *testing.T, got streamReply) (subject, id string) {
	t.Helper()
	wantStatus(t, got, 200)
	if len(got.body) != 0 {
		t.Fatalf("accept body %q", got.body)
	}
	subject = got.header.Get(HeaderAccept)
	if !acceptPattern.MatchString(subject) {
		t.Fatalf("%s %q", HeaderAccept, subject)
	}
	if strings.HasPrefix(subject, "inference.v2.models.") {
		t.Fatalf("upload subject %q is under the model subjects", subject)
	}
	return subject, strings.TrimPrefix(subject, uploadPrefix)
}

func sendSlice(t *testing.T, nc *nats.Conn, subject, id string, data []byte) streamReply {
	t.Helper()
	return request(t, nc, subject, nats.Header{HeaderID: []string{id}}, data)
}

func wantAcked(t *testing.T, got streamReply) {
	t.Helper()
	wantStatus(t, got, 200)
	if len(got.body) != 0 || got.header.Get(HeaderAccept) != "" {
		t.Fatalf("slice acknowledgment has body %q and headers %v", got.body, got.header)
	}
}

func wantRefused(t *testing.T, got streamReply, text string) {
	t.Helper()
	wantStatus(t, got, 400)
	if msg := got.errorText(t); !strings.Contains(msg, text) {
		t.Fatalf("error %q, want %q", msg, text)
	}
}

func uploadSubscriptions(t *testing.T, srv *server.Server) []server.SubDetail {
	t.Helper()
	subsz, err := srv.Subsz(&server.SubszOptions{Subscriptions: true})
	if err != nil {
		t.Fatal(err)
	}
	var out []server.SubDetail
	for _, sub := range subsz.Subs {
		if strings.HasPrefix(sub.Subject, uploadPrefix) {
			out = append(out, sub)
		}
	}
	return out
}

// watchModels counts every message the model wildcard delivers, the way a replica's model subscription sees them.
func watchModels(t *testing.T, nc *nats.Conn) func() []string {
	t.Helper()
	var mu sync.Mutex
	var subjects []string
	sub, err := nc.Subscribe("inference.v2.models.>", func(m *nats.Msg) {
		mu.Lock()
		subjects = append(subjects, m.Subject)
		mu.Unlock()
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = nc.Flush(); err != nil {
		t.Fatal(err)
	}
	return func() []string {
		if err := nc.Flush(); err != nil {
			t.Fatal(err)
		}
		_ = sub.Unsubscribe()
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), subjects...)
	}
}

func TestRendezvousRunsTheJoinedBody(t *testing.T) {
	srv := startEmbedded(t)
	url := srv.ClientURL()
	runner := &fakeRunner{}
	pool := &countingPool{}
	clock := newManualClock()
	serveUntilCleanup(t, url, Config{Catalog: readyAdd(), Runner: runner, MaxPayloadBytes: 1 << 20, Buffers: pool, After: clock.After})
	client := connect(t, url)
	seen := watchModels(t, connect(t, url))

	doc, suffix := splitBinary(t, "rendezvous-id")
	header := rendezvousHeader(strconv.Itoa(len(suffix)))
	header.Set(HeaderContentLength, strconv.Itoa(len(doc)))
	subject, id := accepted(t, request(t, client, "inference.v2.models.add.infer", header, doc))

	subs := uploadSubscriptions(t, srv)
	if len(subs) != 1 || subs[0].Subject != subject || subs[0].Queue != "" {
		t.Fatalf("upload subscriptions %+v, want %s with no queue group", subs, subject)
	}
	if pool.gets.Load() != 1 || runner.calls.Load() != 0 {
		t.Fatalf("after the header: pool gets %d, runner calls %d", pool.gets.Load(), runner.calls.Load())
	}

	wantAcked(t, sendSlice(t, client, subject, id, suffix[:20]))
	assertThrees(t, sendSlice(t, client, subject, id, suffix[20:]), "rendezvous-id", true)

	if runner.calls.Load() != 1 {
		t.Fatalf("runner called %d times", runner.calls.Load())
	}
	pool.waitReturned(t, 1)
	pool.assertSuffixInPoolBuffer(t, doc, suffix)
	noResponders(t, client, subject)

	got := seen()
	if len(got) != 1 || got[0] != "inference.v2.models.add.infer" {
		t.Fatalf("the model wildcard saw %v, want only the header", got)
	}
}

func TestRendezvousOfZeroLengthIsJSON(t *testing.T) {
	srv := startEmbedded(t)
	url := srv.ClientURL()
	runner := &fakeRunner{}
	pool := &countingPool{}
	serveUntilCleanup(t, url, Config{Catalog: readyAdd(), Runner: runner, MaxPayloadBytes: 1 << 20, Buffers: pool, After: newManualClock().After})

	got := request(t, connect(t, url), "inference.v2.models.add.infer", rendezvousHeader("0"), addJSONBody("zero"))
	assertThrees(t, got, "zero", false)
	if got.header.Get(HeaderAccept) != "" {
		t.Fatalf("a zero-length rendezvous was accepted on %q", got.header.Get(HeaderAccept))
	}
	if pool.gets.Load() != 0 || runner.calls.Load() != 1 {
		t.Fatalf("pool gets %d, runner calls %d", pool.gets.Load(), runner.calls.Load())
	}
	if subs := uploadSubscriptions(t, srv); len(subs) != 0 {
		t.Fatalf("upload subscriptions %+v", subs)
	}
}

func TestRendezvousHeaderRefusals(t *testing.T) {
	const ceiling = 4096
	srv := startEmbedded(t)
	url := srv.ClientURL()
	runner := &fakeRunner{}
	pool := &countingPool{}
	serveUntilCleanup(t, url, Config{Catalog: readyAdd(), Runner: runner, MaxPayloadBytes: ceiling, Buffers: pool, After: newManualClock().After})
	client := connect(t, url)
	doc, _ := splitBinary(t, "x")

	cases := []struct {
		name    string
		subject string
		header  nats.Header
		status  int
		text    string
	}{
		{name: "above the ceiling", header: rendezvousHeader(strconv.Itoa(ceiling + 1)), status: 400, text: payloadTooLarge},
		{name: "header and suffix above the ceiling", header: rendezvousHeader(strconv.Itoa(ceiling)), status: 400, text: payloadTooLarge},
		{name: "far above the ceiling", header: rendezvousHeader("9223372036854775807"), status: 400, text: payloadTooLarge},
		{name: "missing length", header: nats.Header{HeaderTransfer: []string{transferRendezvous}}, status: 400, text: suffixLengthInvalid},
		{name: "empty length", header: rendezvousHeader(""), status: 400, text: suffixLengthInvalid},
		{name: "negative length", header: rendezvousHeader("-1"), status: 400, text: suffixLengthInvalid},
		{name: "fractional length", header: rendezvousHeader("1.5"), status: 400, text: suffixLengthInvalid},
		{name: "signed length", header: rendezvousHeader("+48"), status: 400, text: suffixLengthInvalid},
		{name: "word length", header: rendezvousHeader("forty"), status: 400, text: suffixLengthInvalid},
		{name: "overflowing length", header: rendezvousHeader("99999999999999999999"), status: 400, text: suffixLengthInvalid},
		{name: "two lengths", header: rendezvousHeader("48", "48"), status: 400, text: suffixLengthInvalid},
		{
			name:   "header length that is not the header",
			header: nats.Header{HeaderTransfer: []string{transferRendezvous}, HeaderSuffixLength: []string{"48"}, HeaderContentLength: []string{"3"}},
			status: 400,
			text:   headerLengthMismatch,
		},
		{name: "unknown model", subject: "inference.v2.models.missing.infer", header: rendezvousHeader("48"), status: 404},
		{name: "unknown version", subject: "inference.v2.models.add.versions.2.infer", header: rendezvousHeader("48"), status: 404},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			subject := c.subject
			if subject == "" {
				subject = "inference.v2.models.add.infer"
			}
			got := request(t, client, subject, c.header, doc)
			wantStatus(t, got, c.status)
			if got.header.Get(HeaderAccept) != "" {
				t.Fatalf("a refusal carries %s %q", HeaderAccept, got.header.Get(HeaderAccept))
			}
			if text := got.errorText(t); c.text != "" && !strings.Contains(text, c.text) {
				t.Fatalf("error %q, want %q", text, c.text)
			}
		})
	}
	if pool.gets.Load() != 0 || runner.calls.Load() != 0 {
		t.Fatalf("pool gets %d, runner calls %d", pool.gets.Load(), runner.calls.Load())
	}
	if subs := uploadSubscriptions(t, srv); len(subs) != 0 {
		t.Fatalf("upload subscriptions %+v", subs)
	}
}

// uploadCase is one accepted upload on its own server and replica.
type uploadCase struct {
	srv     *server.Server
	engine  *nats.Conn
	client  *nats.Conn
	runner  *fakeRunner
	pool    *countingPool
	clock   *manualClock
	cancel  context.CancelFunc
	subject string
	id      string
	suffix  []byte
}

// startUpload accepts the add model's binary header on a fresh replica whose connection the test holds.
func startUpload(t *testing.T, clock *manualClock) *uploadCase {
	t.Helper()
	srv := startEmbedded(t)
	url := srv.ClientURL()
	u := &uploadCase{srv: srv, engine: connect(t, url), client: connect(t, url), runner: &fakeRunner{}, pool: &countingPool{}, clock: clock}
	ctx, cancel := context.WithCancel(context.Background())
	u.cancel = cancel
	h := NewHandler(Config{Catalog: readyAdd(), Runner: u.runner, MaxPayloadBytes: 1 << 20, Buffers: u.pool, After: clock.After, Context: ctx})
	subs, err := Subscribe(u.engine, h)
	if err != nil {
		t.Fatal(err)
	}
	if err = u.engine.Flush(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		for _, sub := range subs {
			_ = sub.Unsubscribe()
		}
		h.Wait()
	})
	doc, suffix := splitBinary(t, "aborted")
	u.suffix = suffix
	u.subject, u.id = accepted(t, request(t, u.client, "inference.v2.models.add.infer", rendezvousHeader(strconv.Itoa(len(suffix))), doc))
	return u
}

// wantAbandoned checks that the upload gave its buffer back, never ran, and no longer has a subject.
func (u *uploadCase) wantAbandoned(t *testing.T) {
	t.Helper()
	u.pool.waitReturned(t, 1)
	if u.runner.calls.Load() != 0 {
		t.Fatalf("runner called %d times", u.runner.calls.Load())
	}
	deadline := time.Now().Add(replyWait)
	for len(uploadSubscriptions(t, u.srv)) != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("upload subscriptions %+v", uploadSubscriptions(t, u.srv))
		}
		time.Sleep(time.Millisecond)
	}
}

func TestRendezvousAbandonsAStalledUpload(t *testing.T) {
	t.Run("no slice after the header", func(t *testing.T) {
		u := startUpload(t, newManualClock())
		u.clock.Advance(uploadStall)
		u.wantAbandoned(t)
		noResponders(t, u.client, u.subject)
	})
	t.Run("missing last slice", func(t *testing.T) {
		u := startUpload(t, newManualClock())
		u.clock.Advance(uploadStall - time.Millisecond)
		wantAcked(t, sendSlice(t, u.client, u.subject, u.id, u.suffix[:20]))
		u.clock.Advance(uploadStall - time.Millisecond)
		wantAcked(t, sendSlice(t, u.client, u.subject, u.id, u.suffix[20:30]))
		if u.pool.puts.Load() != 0 {
			t.Fatal("an upload whose slices arrived in time was abandoned")
		}
		u.clock.Advance(uploadStall)
		u.wantAbandoned(t)
		noResponders(t, u.client, u.subject)
	})
}

func TestRendezvousRefusesABrokenSlice(t *testing.T) {
	cases := []struct {
		name   string
		header func(u *uploadCase) nats.Header
		data   func(u *uploadCase) []byte
		text   string
	}{
		{
			name:   "extra slice past the declared length",
			header: func(u *uploadCase) nats.Header { return nats.Header{HeaderID: []string{u.id}} },
			data:   func(u *uploadCase) []byte { return append(append([]byte(nil), u.suffix...), 0) },
			text:   sliceTooLong,
		},
		{
			name:   "mismatched id",
			header: func(*uploadCase) nats.Header { return nats.Header{HeaderID: []string{strings.Repeat("0", 32)}} },
			data:   func(u *uploadCase) []byte { return u.suffix[:20] },
			text:   sliceIDInvalid,
		},
		{
			name:   "missing id",
			header: func(*uploadCase) nats.Header { return nil },
			data:   func(u *uploadCase) []byte { return u.suffix[:20] },
			text:   sliceIDInvalid,
		},
		{
			name:   "two ids",
			header: func(u *uploadCase) nats.Header { return nats.Header{HeaderID: []string{u.id, u.id}} },
			data:   func(u *uploadCase) []byte { return u.suffix[:20] },
			text:   sliceIDInvalid,
		},
		{
			name:   "empty slice",
			header: func(u *uploadCase) nats.Header { return nats.Header{HeaderID: []string{u.id}} },
			data:   func(*uploadCase) []byte { return nil },
			text:   sliceEmpty,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			u := startUpload(t, newManualClock())
			wantRefused(t, request(t, u.client, u.subject, c.header(u), c.data(u)), c.text)
			u.wantAbandoned(t)
			noResponders(t, u.client, u.subject)
		})
	}
}

func TestRendezvousRefusesAnExtraSliceAfterAnOverrun(t *testing.T) {
	u := startUpload(t, newManualClock())
	wantAcked(t, sendSlice(t, u.client, u.subject, u.id, u.suffix[:40]))
	wantRefused(t, sendSlice(t, u.client, u.subject, u.id, u.suffix[:20]), sliceTooLong)
	u.wantAbandoned(t)
}

func TestRendezvousRefusesASecondSliceInFlight(t *testing.T) {
	clock := newManualClock()
	// The first timer is armed for the header. The second is armed while the first slice is being taken.
	clock.holdAt = 2
	u := startUpload(t, clock)

	inbox := u.client.NewInbox()
	first, err := u.client.SubscribeSync(inbox)
	if err != nil {
		t.Fatal(err)
	}
	if err = u.client.PublishMsg(&nats.Msg{Subject: u.subject, Reply: inbox, Header: nats.Header{HeaderID: []string{u.id}}, Data: u.suffix[:20]}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-clock.held:
	case <-time.After(replyWait):
		t.Fatal("the first slice was not taken")
	}
	wantRefused(t, sendSlice(t, u.client, u.subject, u.id, u.suffix[20:]), sliceOverlap)
	close(clock.hold)
	wantRefused(t, readStream(t, first, func() error { return nil }), sliceOverlap)
	u.wantAbandoned(t)
}

func TestRendezvousAbandonsAnUploadWhenTheConnectionCloses(t *testing.T) {
	u := startUpload(t, newManualClock())
	wantAcked(t, sendSlice(t, u.client, u.subject, u.id, u.suffix[:20]))
	u.engine.Close()
	u.wantAbandoned(t)
}

func TestRendezvousAbandonsAnUploadAtShutdown(t *testing.T) {
	u := startUpload(t, newManualClock())
	wantAcked(t, sendSlice(t, u.client, u.subject, u.id, u.suffix[:20]))
	u.cancel()
	u.wantAbandoned(t)
	noResponders(t, u.client, u.subject)
}

func TestRendezvousAbandonmentIsLoggedAtWarn(t *testing.T) {
	logs := &syncBuffer{}
	srv := startEmbedded(t)
	url := srv.ClientURL()
	pool := &countingPool{}
	clock := newManualClock()
	serveUntilCleanup(t, url, Config{Catalog: readyAdd(), Runner: &fakeRunner{}, MaxPayloadBytes: 1 << 20, Buffers: pool, After: clock.After, Logger: debugLogger(logs)})
	doc, suffix := splitBinary(t, "logged")
	accepted(t, request(t, connect(t, url), "inference.v2.models.add.infer", rendezvousHeader(strconv.Itoa(len(suffix))), doc))
	clock.Advance(uploadStall)
	pool.waitReturned(t, 1)

	out := logs.String()
	if !strings.Contains(out, `"level":"WARN"`) || !strings.Contains(out, uploadStalled) || !strings.Contains(out, `"status":400`) {
		t.Fatalf("log:\n%s", out)
	}
}
