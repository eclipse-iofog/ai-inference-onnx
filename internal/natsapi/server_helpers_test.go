package natsapi

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"

	"github.com/eclipse-iofog/ai-inference-onnx/internal/batch"
)

const replyWait = 5 * time.Second

// startServer runs an embedded NATS server on a random local port.
// maxPayload zero keeps the server default.
func startServer(t *testing.T, maxPayload int32) string {
	t.Helper()
	srv, err := server.NewServer(&server.Options{
		Host:       "127.0.0.1",
		Port:       server.RANDOM_PORT,
		NoLog:      true,
		NoSigs:     true,
		MaxPayload: maxPayload,
	})
	if err != nil {
		t.Fatal(err)
	}
	srv.Start()
	t.Cleanup(srv.Shutdown)
	if !srv.ReadyForConnections(replyWait) {
		t.Fatal("embedded NATS server did not start")
	}
	return srv.ClientURL()
}

func connect(t *testing.T, url string) *nats.Conn {
	t.Helper()
	nc, err := nats.Connect(url, nats.NoReconnect())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	return nc
}

// serve subscribes a handler for cfg on its own connection.
func serve(t *testing.T, url string, cfg Config) []*nats.Subscription {
	t.Helper()
	nc := connect(t, url)
	h := NewHandler(cfg)
	subs, err := Subscribe(nc, h)
	if err != nil {
		t.Fatal(err)
	}
	if err = nc.Flush(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, sub := range subs {
			_ = sub.Unsubscribe()
		}
		h.Wait()
	})
	return subs
}

// streamReply is a reply stream as a client reads it.
type streamReply struct {
	status int
	header nats.Header
	slices []*nats.Msg
	body   []byte
}

// request publishes one request and reads the reply stream through its terminator.
// It fails the test when the stream breaks the framing: the first message must carry the status,
// later data slices carry no headers and are not empty, and the terminator is empty with no headers.
func request(t *testing.T, nc *nats.Conn, subject string, header nats.Header, body []byte) streamReply {
	t.Helper()
	inbox := nc.NewInbox()
	sub, err := nc.SubscribeSync(inbox)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sub.Unsubscribe() }()
	got := readStream(t, sub, func() error {
		return nc.PublishMsg(&nats.Msg{Subject: subject, Reply: inbox, Header: header, Data: body})
	})
	return got
}

func readStream(t *testing.T, sub *nats.Subscription, send func() error) streamReply {
	t.Helper()
	if err := send(); err != nil {
		t.Fatal(err)
	}
	var got streamReply
	for i := 0; ; i++ {
		msg, err := sub.NextMsg(replyWait)
		if err != nil {
			t.Fatalf("reply message %d: %v", i, err)
		}
		if i == 0 {
			raw := msg.Header.Get(HeaderStatus)
			if raw == "" {
				t.Fatalf("first reply message has no %s: headers %v, %d bytes", HeaderStatus, msg.Header, len(msg.Data))
			}
			got.status, err = strconv.Atoi(raw)
			if err != nil {
				t.Fatalf("status %q: %v", raw, err)
			}
			got.header = msg.Header
			got.slices = append(got.slices, msg)
			got.body = append(got.body, msg.Data...)
			continue
		}
		if len(msg.Header) == 0 && len(msg.Data) == 0 {
			return got
		}
		if len(msg.Header) != 0 {
			t.Fatalf("reply message %d has headers %v", i, msg.Header)
		}
		got.slices = append(got.slices, msg)
		got.body = append(got.body, msg.Data...)
	}
}

func (r streamReply) errorText(t *testing.T) string {
	t.Helper()
	var doc struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(r.body, &doc); err != nil || doc.Error == "" {
		t.Fatalf("status %d body %q is not an error document", r.status, r.body)
	}
	return doc.Error
}

func wantStatus(t *testing.T, got streamReply, status int) {
	t.Helper()
	if got.status != status {
		t.Fatalf("status %d, want %d, body %q", got.status, status, got.body)
	}
}

// addSignature is the committed add model: A plus B is C, all FP32.
func addSignature() batch.Signature {
	return batch.Signature{
		Inputs: []batch.TensorSpec{
			{Name: "A", Datatype: batch.TypeFP32, Shape: []int64{2, 3}},
			{Name: "B", Datatype: batch.TypeFP32, Shape: []int64{2, 3}},
		},
		Outputs: []batch.TensorSpec{
			{Name: "C", Datatype: batch.TypeFP32, Shape: []int64{2, 3}},
		},
	}
}

type fakeCatalog []batch.Directory

func (c fakeCatalog) Models() []batch.Directory { return c }

func readyAdd() fakeCatalog {
	return fakeCatalog{{
		Name:     "add",
		State:    batch.StateReady,
		Versions: []batch.ModelVersion{{Version: "1", Signature: addSignature()}},
	}}
}

// fakeRunner adds its two inputs, or returns err without running.
type fakeRunner struct {
	calls atomic.Int32
	err   error
}

func (r *fakeRunner) Run(ctx context.Context, name, version string, req *batch.Request) (*batch.Reply, error) {
	r.calls.Add(1)
	if r.err != nil {
		return nil, r.err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(req.Inputs) != 2 {
		return nil, fmt.Errorf("%d inputs", len(req.Inputs))
	}
	a, b := req.Inputs[0].Data, req.Inputs[1].Data
	sum := make([]byte, len(a))
	for i := 0; i+4 <= len(a); i += 4 {
		x := math.Float32frombits(binary.LittleEndian.Uint32(a[i:]))
		y := math.Float32frombits(binary.LittleEndian.Uint32(b[i:]))
		binary.LittleEndian.PutUint32(sum[i:], math.Float32bits(x+y))
	}
	return &batch.Reply{Outputs: []batch.Tensor{{Name: "C", Shape: req.Inputs[0].Shape, Data: sum}}}, nil
}

func fp32Bytes(vals ...float32) []byte {
	out := make([]byte, 4*len(vals))
	for i, v := range vals {
		binary.LittleEndian.PutUint32(out[4*i:], math.Float32bits(v))
	}
	return out
}

func addJSONBody(id string) []byte {
	return fmt.Appendf(nil, `{"id":%q,"inputs":[`+
		`{"name":"A","shape":[2,3],"datatype":"FP32","data":[1,1,1,1,1,1]},`+
		`{"name":"B","shape":[2,3],"datatype":"FP32","data":[2,2,2,2,2,2]}]}`, id)
}

// addBinaryBody is a binary-tensor request and its Inference-Header-Content-Length.
func addBinaryBody(id string) ([]byte, nats.Header) {
	doc := fmt.Sprintf(`{"id":%q,"inputs":[`+
		`{"name":"A","shape":[2,3],"datatype":"FP32","parameters":{"binary_data_size":24}},`+
		`{"name":"B","shape":[2,3],"datatype":"FP32","parameters":{"binary_data_size":24}}],`+
		`"outputs":[{"name":"C","parameters":{"binary_data":true}}]}`, id)
	body := append([]byte(doc), fp32Bytes(1, 1, 1, 1, 1, 1)...)
	body = append(body, fp32Bytes(2, 2, 2, 2, 2, 2)...)
	return body, nats.Header{HeaderContentLength: []string{strconv.Itoa(len(doc))}}
}

type inferDocument struct {
	ModelName    string           `json:"model_name"`
	ModelVersion string           `json:"model_version"`
	ID           string           `json:"id"`
	Outputs      []outputDocument `json:"outputs"`
}

type outputDocument struct {
	Name     string    `json:"name"`
	Datatype string    `json:"datatype"`
	Shape    []int64   `json:"shape"`
	Data     []float32 `json:"data"`
}

// assertThrees checks a successful add reply in JSON or binary form.
func assertThrees(t *testing.T, got streamReply, id string, binaryReply bool) {
	t.Helper()
	wantStatus(t, got, 200)
	doc := got.body
	var values []float32
	if binaryReply {
		if got.header.Get(HeaderContentType) != contentBinary {
			t.Fatalf("content type %q", got.header.Get(HeaderContentType))
		}
		n, err := strconv.Atoi(got.header.Get(HeaderContentLength))
		if err != nil || n <= 0 || n > len(got.body) {
			t.Fatalf("%s %q for a %d-byte body", HeaderContentLength, got.header.Get(HeaderContentLength), len(got.body))
		}
		doc = got.body[:n]
		suffix := got.body[n:]
		for i := 0; i+4 <= len(suffix); i += 4 {
			values = append(values, math.Float32frombits(binary.LittleEndian.Uint32(suffix[i:])))
		}
	} else if got.header.Get(HeaderContentType) != contentJSON {
		t.Fatalf("content type %q", got.header.Get(HeaderContentType))
	}
	var parsed inferDocument
	if err := json.Unmarshal(doc, &parsed); err != nil {
		t.Fatalf("infer document %q: %v", doc, err)
	}
	if parsed.ModelName != "add" || parsed.ModelVersion != "1" || parsed.ID != id {
		t.Fatalf("model %q version %q id %q", parsed.ModelName, parsed.ModelVersion, parsed.ID)
	}
	if len(parsed.Outputs) != 1 || parsed.Outputs[0].Name != "C" || parsed.Outputs[0].Datatype != "FP32" {
		t.Fatalf("outputs %+v", parsed.Outputs)
	}
	if shape := parsed.Outputs[0].Shape; len(shape) != 2 || shape[0] != 2 || shape[1] != 3 {
		t.Fatalf("shape %v", shape)
	}
	if !binaryReply {
		values = parsed.Outputs[0].Data
	}
	if len(values) != 6 {
		t.Fatalf("values %v", values)
	}
	for i, v := range values {
		if v != 3 {
			t.Fatalf("value %d is %v", i, v)
		}
	}
}

// syncBuffer is a log destination that tests read while handlers write.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func debugLogger(w io.Writer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func noResponders(t *testing.T, nc *nats.Conn, subject string) {
	t.Helper()
	_, err := nc.Request(subject, nil, replyWait)
	if !errors.Is(err, nats.ErrNoResponders) {
		t.Fatalf("request %s: %v, want no responders", subject, err)
	}
}
