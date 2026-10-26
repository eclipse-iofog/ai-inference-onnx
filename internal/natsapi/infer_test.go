package natsapi

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nats-io/nats.go"

	"github.com/eclipse-iofog/ai-inference-onnx/internal/auth"
	"github.com/eclipse-iofog/ai-inference-onnx/internal/batch"
	"github.com/eclipse-iofog/ai-inference-onnx/internal/config"
	"github.com/eclipse-iofog/ai-inference-onnx/internal/httpapi"
)

const staticToken = "nats-infer-static-token-0123456789abcdef"

func fakeCatalogLoading(name string) fakeCatalog {
	return fakeCatalog{{
		Name:     name,
		State:    batch.StateLoading,
		Reason:   "model is loading",
		Versions: []batch.ModelVersion{{Version: "1"}},
	}}
}

func TestInlineInferRunsTheDecodedRequest(t *testing.T) {
	url := startServer(t, 0)
	runner := &fakeRunner{}
	serve(t, url, Config{Catalog: readyAdd(), Runner: runner, MaxPayloadBytes: 1 << 20})
	client := connect(t, url)

	for _, subject := range []string{"inference.v2.models.add.infer", "inference.v2.models.add.versions.1.infer"} {
		assertThrees(t, request(t, client, subject, nil, addJSONBody("json-id")), "json-id", false)
		body, header := addBinaryBody("binary-id")
		assertThrees(t, request(t, client, subject, header, body), "binary-id", true)
	}
	if runner.calls.Load() != 4 {
		t.Fatalf("runner called %d times", runner.calls.Load())
	}
}

func TestInlineInferRefusals(t *testing.T) {
	const ceiling = 512
	unavailable := fakeCatalog{{
		Name:     "broken",
		State:    batch.StateUnavailable,
		Reason:   "model file could not be opened",
		Versions: []batch.ModelVersion{{Version: "1", Signature: addSignature()}},
	}}
	catalog := append(append(readyAdd(), fakeCatalogLoading("loading")...), unavailable...)
	oversized := append(addJSONBody("big"), bytes.Repeat([]byte(" "), ceiling)...)

	cases := []struct {
		name    string
		subject string
		header  nats.Header
		body    []byte
		status  int
		text    string
	}{
		{name: "bad tensor", subject: "inference.v2.models.add.infer", body: []byte(`{"inputs":[{"name":"A","shape":[2,3],"datatype":"FP32","data":[1]}]}`), status: 400},
		{name: "not json", subject: "inference.v2.models.add.infer", body: []byte(`nope`), status: 400},
		{name: "bad header length", subject: "inference.v2.models.add.infer", header: nats.Header{HeaderContentLength: []string{"-1"}}, body: addJSONBody("x"), status: 400, text: headerLengthInvalid},
		{name: "version 2", subject: "inference.v2.models.add.versions.2.infer", body: addJSONBody("x"), status: 404},
		{name: "version latest", subject: "inference.v2.models.add.versions.latest.infer", body: addJSONBody("x"), status: 404},
		{name: "unknown model", subject: "inference.v2.models.missing.infer", body: addJSONBody("x"), status: 404},
		{name: "above the payload limit", subject: "inference.v2.models.add.infer", body: oversized, status: 400, text: payloadTooLarge},
		{name: "loading", subject: "inference.v2.models.loading.infer", body: addJSONBody("x"), status: 503, text: "model is loading"},
		{name: "unavailable", subject: "inference.v2.models.broken.infer", body: addJSONBody("x"), status: 400, text: "model file could not be opened"},
		{name: "object store", subject: "inference.v2.models.add.infer", header: nats.Header{HeaderTransfer: []string{"object-store"}}, body: addJSONBody("x"), status: 400, text: `Inference-Transfer "object-store" is not supported`},
		{name: "rendezvous without a suffix length", subject: "inference.v2.models.add.infer", header: nats.Header{HeaderTransfer: []string{"rendezvous"}}, body: addJSONBody("x"), status: 400, text: suffixLengthInvalid},
		{name: "two transfers", subject: "inference.v2.models.add.infer", header: nats.Header{HeaderTransfer: []string{"a", "b"}}, body: addJSONBody("x"), status: 400, text: transferRepeated},
	}
	url := startServer(t, 0)
	runner := &fakeRunner{}
	serve(t, url, Config{Catalog: catalog, Runner: runner, MaxPayloadBytes: ceiling})
	client := connect(t, url)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := request(t, client, c.subject, c.header, c.body)
			wantStatus(t, got, c.status)
			if text := got.errorText(t); c.text != "" && !strings.Contains(text, c.text) {
				t.Fatalf("error %q, want %q", text, c.text)
			}
		})
	}
	if runner.calls.Load() != 0 {
		t.Fatalf("the runner was called %d times", runner.calls.Load())
	}
}

func TestInlineInferQueueOutcomes(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	cases := []struct {
		name   string
		runner *fakeRunner
		ctx    context.Context
		status int
		text   string
	}{
		{name: "queue full", runner: &fakeRunner{err: batch.ErrQueueFull}, status: 503, text: "request queue for this model is full"},
		{name: "queue timeout", runner: &fakeRunner{err: batch.ErrQueueTimeout}, status: 504},
		{name: "shutdown", runner: &fakeRunner{}, ctx: canceled, status: 400, text: "call was canceled"},
		{name: "call deadline", runner: &fakeRunner{err: context.DeadlineExceeded}, status: 504},
		{name: "runtime failure", runner: &fakeRunner{err: errors.New("session run failed")}, status: 500},
	}
	url := startServer(t, 0)
	serve(t, url, Config{Catalog: fakeCatalogLoading("add"), Runner: &fakeRunner{}, MaxPayloadBytes: 1 << 20})
	loading := request(t, connect(t, url), "inference.v2.models.add.infer", nil, addJSONBody("x"))
	wantStatus(t, loading, 503)
	loadingText := loading.errorText(t)

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			url := startServer(t, 0)
			serve(t, url, Config{Catalog: readyAdd(), Runner: c.runner, Context: c.ctx, MaxPayloadBytes: 1 << 20})
			got := request(t, connect(t, url), "inference.v2.models.add.infer", nil, addJSONBody("x"))
			wantStatus(t, got, c.status)
			text := got.errorText(t)
			if c.text != "" && text != c.text {
				t.Fatalf("error %q, want %q", text, c.text)
			}
			if c.status == 503 && text == loadingText {
				t.Fatalf("queue full and loading share the text %q", text)
			}
			if c.runner.calls.Load() != 1 {
				t.Fatalf("runner called %d times", c.runner.calls.Load())
			}
		})
	}
}

func TestEmptyModelSetIsNotFound(t *testing.T) {
	url := startServer(t, 0)
	serve(t, url, Config{MaxPayloadBytes: 1 << 20})
	client := connect(t, url)
	for _, subject := range []string{"inference.v2.models.add.infer", "inference.v2.models.add.versions.1.infer"} {
		got := request(t, client, subject, nil, addJSONBody("x"))
		wantStatus(t, got, 404)
		got.errorText(t)
	}
	got := request(t, client, "inference.v2.models", nil, nil)
	wantStatus(t, got, 200)
	if string(got.body) != `{"models":[]}` {
		t.Fatalf("model list %s", got.body)
	}
}

// The REST listener checks the static token. A NATS message is not checked, and its Authorization header is ignored.
func TestStaticModeDoesNotApplyToNATS(t *testing.T) {
	cfg := config.Config{AuthMode: config.AuthStatic, AuthStaticToken: staticToken}
	checker, err := auth.New(cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{}
	rest := httpapi.New(checker, func() bool { return true })
	rest.RegisterReads(readyAdd())
	rest.RegisterInfer(httpapi.InferConfig{Catalog: readyAdd(), Runner: runner, MaxPayloadBytes: 1 << 20})
	rec := httptest.NewRecorder()
	rest.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v2/models/add/infer", bytes.NewReader(addJSONBody("rest"))))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("REST infer without a token: %d", rec.Code)
	}

	logs := &syncBuffer{}
	url := startServer(t, 0)
	serve(t, url, Config{Catalog: readyAdd(), Runner: runner, MaxPayloadBytes: 1 << 20, Logger: debugLogger(logs)})
	client := connect(t, url)
	assertThrees(t, request(t, client, "inference.v2.models.add.infer", nil, addJSONBody("no-token")), "no-token", false)
	bearer := nats.Header{"Authorization": []string{"Bearer not-the-token"}}
	assertThrees(t, request(t, client, "inference.v2.models.add.infer", bearer, addJSONBody("wrong-token")), "wrong-token", false)
	bearer = nats.Header{"Authorization": []string{"Bearer " + staticToken}}
	assertThrees(t, request(t, client, "inference.v2.models.add.infer", bearer, addJSONBody("right-token")), "right-token", false)

	out := logs.String()
	if !strings.Contains(out, `"msg":"infer"`) || !strings.Contains(out, `"level":"DEBUG"`) || !strings.Contains(out, `"transport":"nats"`) {
		t.Fatalf("infer timing was not logged at debug:\n%s", out)
	}
	for _, secret := range []string{staticToken, "Bearer", "not-the-token", "[1,1,1"} {
		if strings.Contains(out, secret) {
			t.Fatalf("log contains %q:\n%s", secret, out)
		}
	}
}

func TestRejectedInferIsLoggedAtWarn(t *testing.T) {
	logs := &syncBuffer{}
	url := startServer(t, 0)
	serve(t, url, Config{Catalog: readyAdd(), Runner: &fakeRunner{err: batch.ErrQueueFull}, MaxPayloadBytes: 1 << 20, Logger: debugLogger(logs)})
	wantStatus(t, request(t, connect(t, url), "inference.v2.models.add.infer", nil, addJSONBody("x")), 503)
	out := logs.String()
	if !strings.Contains(out, `"level":"WARN"`) || !strings.Contains(out, `"msg":"infer"`) || !strings.Contains(out, `"status":503`) || !strings.Contains(out, `"error_kind":"queue_full"`) {
		t.Fatalf("log:\n%s", out)
	}
}

// The process serves this package over the NATS client without the runtime library.
func TestProcessImportsClientWithoutRuntime(t *testing.T) {
	cmd := exec.Command("go", "list", "-deps", "../../cmd/inference")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	for _, pkg := range []string{"github.com/nats-io/nats.go", "github.com/eclipse-iofog/ai-inference-onnx/internal/natsapi"} {
		if !strings.Contains(string(out), pkg+"\n") {
			t.Fatalf("the process does not import %s", pkg)
		}
	}
	if strings.Contains(string(out), "github.com/microsoft/onnxruntime/go") {
		t.Fatal("the process imports the runtime library without cgo")
	}
}

func TestPackageImportsNoListenerOrRuntime(t *testing.T) {
	cmd := exec.Command("go", "list", "-f", `{{join .Imports "\n"}}`, ".")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	for _, forbidden := range []string{
		"github.com/eclipse-iofog/ai-inference-onnx/internal/httpapi",
		"github.com/eclipse-iofog/ai-inference-onnx/internal/grpcapi",
		"github.com/eclipse-iofog/ai-inference-onnx/internal/ort",
		"github.com/eclipse-iofog/ai-inference-onnx/internal/auth",
		"github.com/microsoft/onnxruntime/go/onnxruntime",
		"github.com/nats-io/nats-server/v2/server",
		"net",
	} {
		for line := range strings.SplitSeq(string(out), "\n") {
			if line == forbidden {
				t.Fatalf("production files import %s", forbidden)
			}
		}
	}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(src, []byte("Listen(")) {
			t.Fatalf("%s opens a listener", file)
		}
	}
}
