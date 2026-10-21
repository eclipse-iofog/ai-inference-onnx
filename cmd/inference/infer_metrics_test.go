//go:build cgo

package main

import (
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/eclipse-iofog/ai-inference-onnx/api/openinference/v2"
)

func TestInferMovesSharedCounters(t *testing.T) {
	t.Parallel()
	requireRuntime(t)

	srv := startNATSServer(t)
	path, _, _ := writeProcessCredentials(t)
	ports := pickPorts(t)
	models := filepath.Join(t.TempDir(), "models")
	knowledge := filepath.Join(t.TempDir(), "knowledge")
	copyAddModel(t, models)
	if err := os.MkdirAll(knowledge, 0o755); err != nil {
		t.Fatal(err)
	}
	proc := startCgoProcess(t, runtimeEnv(t, ports, map[string]string{
		"AUTH_MODE":       "disabled",
		"MODELS_PATH":     models,
		"KNOWLEDGE_PATH":  knowledge,
		"NATS_SERVER_URL": srv.ClientURL(),
		"NATS_CREDS_PATH": path,
	}))
	t.Cleanup(proc.kill)

	waitStatus(t, ports.http, "/v2/health/ready", http.StatusOK, 20*time.Second)
	waitLog(t, proc, "nats connected")

	status, hdr, body := postInfer(t, ports.http, "/v2/models/add/infer", []byte(addJSON("add-rest")), nil)
	assertThreesJSON(t, "/v2/models/add/infer", status, hdr, body, "add-rest")

	conn, err := grpc.NewClient(hostPort(ports.grpc), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Errorf("close grpc: %v", err)
		}
	})
	client := openinference.NewGRPCInferenceServiceClient(conn)
	assertGRPCThrees(t, grpcAdd(t, client, "", "add-grpc", false), "add-grpc")

	nc, err := nats.Connect(srv.ClientURL(), nats.NoReconnect())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	status, body = natsRequest(t, nc, "inference.v2.models.add.infer", []byte(addJSON("add-nats")))
	if status != http.StatusOK {
		t.Fatalf("nats infer %d %s", status, body)
	}
	doc := decodeInfer(t, body)
	if doc.ModelName != "add" || doc.ModelVersion != "1" || doc.ID != "add-nats" || len(doc.Outputs) != 1 {
		t.Fatalf("nats response %+v", doc)
	}
	if len(doc.Outputs[0].Data) != 6 {
		t.Fatalf("nats values %v", doc.Outputs[0].Data)
	}
	for i, value := range doc.Outputs[0].Data {
		if value != 3 {
			t.Fatalf("nats output[%d]=%v", i, value)
		}
	}

	text := metricsBody(t, ports.metrics)
	requireCounter(t, text, "inference_request_success", addVersion, 3)
	requireCounter(t, text, "inference_count", addVersion, 3)
	requireCounter(t, text, "inference_exec_count", addVersion, 3)

	status, _, body = postInfer(t, ports.http, "/v2/models/add/infer", []byte(`{"inputs":[{"name":"A","datatype":"INT32","shape":[2,3],"data":[1,1,1,1,1,1]},{"name":"B","datatype":"FP32","shape":[2,3],"data":[1,1,1,1,1,1]}]}`), nil)
	assertInferStatus(t, status, body, http.StatusBadRequest)
	text = metricsBody(t, ports.metrics)
	requireCounter(t, text, "inference_request_success", addVersion, 3)
	requireCounter(t, text, "inference_count", addVersion, 3)
	requireCounter(t, text, "inference_exec_count", addVersion, 3)
	requireCounter(t, text, "inference_request_failure", addOther, 1)

	status, _, body = postInfer(t, ports.http, "/v2/models/missing/infer", []byte(addJSON("x")), nil)
	assertInferStatus(t, status, body, http.StatusNotFound)
	again := metricsBody(t, ports.metrics)
	requireCounter(t, again, "inference_request_success", addVersion, 3)
	requireCounter(t, again, "inference_count", addVersion, 3)
	requireCounter(t, again, "inference_exec_count", addVersion, 3)
	requireCounter(t, again, "inference_request_failure", addOther, 1)
	if strings.Contains(again, `model="missing"`) {
		t.Fatalf("unknown model created a series\n%s", again)
	}
	stopProcess(t, proc)
}

var (
	addVersion = map[string]string{"model": "add", "version": "1"}
	addOther   = map[string]string{"model": "add", "version": "1", "reason": "OTHER"}
)

func metricsBody(t *testing.T, port int) string {
	t.Helper()
	status, body := get(t, port, "/metrics")
	if status != http.StatusOK {
		t.Fatalf("metrics %d %s", status, body)
	}
	return string(body)
}

func requireCounter(t *testing.T, body, name string, labels map[string]string, want float64) {
	t.Helper()
	for _, line := range strings.Split(body, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 2 || strings.HasPrefix(fields[0], "#") {
			continue
		}
		gotName, gotLabels := splitSample(fields[0])
		if gotName != name || !sameLabelSet(gotLabels, labels) {
			continue
		}
		got, err := strconv.ParseFloat(fields[1], 64)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got != want {
			t.Fatalf("%s = %v, want %v", name, got, want)
		}
		return
	}
	if want == 0 {
		return
	}
	t.Fatalf("%s %v missing\n%s", name, labels, body)
}

func splitSample(head string) (string, map[string]string) {
	name := head
	labels := map[string]string{}
	open := strings.IndexByte(head, '{')
	if open < 0 {
		return name, labels
	}
	name = head[:open]
	raw := strings.TrimSuffix(head[open+1:], "}")
	for _, pair := range strings.Split(raw, ",") {
		key, value, ok := strings.Cut(pair, "=")
		if !ok {
			continue
		}
		labels[key] = strings.Trim(value, `"`)
	}
	return name, labels
}

func sameLabelSet(got, want map[string]string) bool {
	if len(got) != len(want) {
		return false
	}
	for key, value := range want {
		if got[key] != value {
			return false
		}
	}
	return true
}
