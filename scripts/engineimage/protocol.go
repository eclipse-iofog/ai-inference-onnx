package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/eclipse-iofog/ai-inference-onnx/api/openinference/v2"
)

var inferenceNames = []string{
	"inference_request_success",
	"inference_request_failure",
	"inference_count",
	"inference_exec_count",
	"inference_pending_request_count",
	"inference_request_duration_us",
	"inference_queue_duration_us",
	"inference_compute_infer_duration_us",
	"inference_model_ready",
}

var absentGPUNames = []string{
	"nv_gpu_power_usage",
	"nv_gpu_utilization",
	"nv_gpu_memory",
}

type inferDoc struct {
	ModelName    string     `json:"model_name"`
	ModelVersion string     `json:"model_version"`
	ID           string     `json:"id"`
	Outputs      []inferOut `json:"outputs"`
	Error        string     `json:"error"`
}

type inferOut struct {
	Name     string    `json:"name"`
	Datatype string    `json:"datatype"`
	Shape    []int64   `json:"shape"`
	Data     []float64 `json:"data"`
}

type indexRow struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	State   string `json:"state"`
	Reason  string `json:"reason"`
}

func addJSON(id string) string {
	return `{"id":"` + id + `","inputs":[` +
		`{"name":"A","datatype":"FP32","shape":[2,3],"data":[1,1,1,1,1,1]},` +
		`{"name":"B","datatype":"FP32","shape":[2,3],"data":[2,2,2,2,2,2]}]}`
}

func addBinary(id string) (body []byte, headerLen int) {
	header := `{"id":"` + id + `","parameters":{"binary_data_output":true},"inputs":[` +
		`{"name":"A","datatype":"FP32","shape":[2,3],"parameters":{"binary_data_size":24}},` +
		`{"name":"B","datatype":"FP32","shape":[2,3],"parameters":{"binary_data_size":24}}]}`
	body = append([]byte(header), float32LE(1, 1, 1, 1, 1, 1)...)
	body = append(body, float32LE(2, 2, 2, 2, 2, 2)...)
	return body, len(header)
}

func float32LE(vals ...float32) []byte {
	buf := make([]byte, 4*len(vals))
	for i, value := range vals {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(value))
	}
	return buf
}

func postInfer(url string, body []byte, headers map[string]string) (int, []byte, http.Header, error) {
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return 0, nil, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, resp.Header, err
	}
	return resp.StatusCode, payload, resp.Header, nil
}

func getURL(url string) (int, []byte, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return resp.StatusCode, body, err
}

func waitHTTP(url string, status int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var last error
	for time.Now().Before(deadline) {
		got, body, err := getURL(url)
		if err == nil && got == status {
			return nil
		}
		if err != nil {
			last = err
		} else {
			last = fmt.Errorf("status %d body %q", got, trim(body))
		}
		time.Sleep(50 * time.Millisecond)
	}
	if last == nil {
		last = errors.New("timed out")
	}
	return last
}

func assertThreesJSON(status int, hdr http.Header, body []byte, id string) error {
	if status != http.StatusOK {
		return fmt.Errorf("status %d body %s", status, trim(body))
	}
	if hdr.Get("Content-Type") != "application/json" {
		return fmt.Errorf("content type %q", hdr.Get("Content-Type"))
	}
	var doc inferDoc
	if err := json.Unmarshal(body, &doc); err != nil {
		return err
	}
	return threesDoc(doc, id, true)
}

func assertThreesBinary(status int, hdr http.Header, body []byte, id string) error {
	if status != http.StatusOK {
		return fmt.Errorf("status %d body %s", status, trim(body))
	}
	if hdr.Get("Content-Type") != "application/octet-stream" {
		return fmt.Errorf("content type %q", hdr.Get("Content-Type"))
	}
	n, err := strconv.Atoi(hdr.Get("Inference-Header-Content-Length"))
	if err != nil || n <= 0 || n >= len(body) {
		return fmt.Errorf("binary header %q body %d", hdr.Get("Inference-Header-Content-Length"), len(body))
	}
	var doc inferDoc
	if err := json.Unmarshal(body[:n], &doc); err != nil {
		return err
	}
	if err := threesDoc(doc, id, false); err != nil {
		return err
	}
	suffix := body[n:]
	if len(suffix) != 24 {
		return fmt.Errorf("binary suffix %d", len(suffix))
	}
	for i := 0; i < 6; i++ {
		value := math.Float32frombits(binary.LittleEndian.Uint32(suffix[i*4:]))
		if value != 3 {
			return fmt.Errorf("binary output[%d]=%v", i, value)
		}
	}
	return nil
}

func threesDoc(doc inferDoc, id string, withData bool) error {
	if doc.ModelName != "add" || doc.ModelVersion != "1" || doc.ID != id {
		return fmt.Errorf("response name %q version %q id %q", doc.ModelName, doc.ModelVersion, doc.ID)
	}
	if len(doc.Outputs) != 1 || doc.Outputs[0].Name != "C" || doc.Outputs[0].Datatype != "FP32" {
		return fmt.Errorf("outputs %+v", doc.Outputs)
	}
	shape := doc.Outputs[0].Shape
	if len(shape) != 2 || shape[0] != 2 || shape[1] != 3 {
		return fmt.Errorf("shape %v", shape)
	}
	if !withData {
		return nil
	}
	if len(doc.Outputs[0].Data) != 6 {
		return fmt.Errorf("data %v", doc.Outputs[0].Data)
	}
	for i, value := range doc.Outputs[0].Data {
		if value != 3 {
			return fmt.Errorf("output[%d]=%v", i, value)
		}
	}
	return nil
}

func errorText(body []byte) (string, error) {
	var doc inferDoc
	if err := json.Unmarshal(body, &doc); err != nil {
		return "", fmt.Errorf("body %s: %w", trim(body), err)
	}
	if doc.Error == "" {
		return "", fmt.Errorf("body %s", trim(body))
	}
	return doc.Error, nil
}

func restThrees(base, path, id string) error {
	status, body, hdr, err := postInfer(base+path, []byte(addJSON(id)), nil)
	if err != nil {
		return err
	}
	return assertThreesJSON(status, hdr, body, id)
}

func restBinary(base, id string) error {
	body, n := addBinary(id)
	status, got, hdr, err := postInfer(base+"/v2/models/add/infer", body, map[string]string{
		"Content-Type":                    "application/octet-stream",
		"Inference-Header-Content-Length": strconv.Itoa(n),
	})
	if err != nil {
		return err
	}
	return assertThreesBinary(status, hdr, got, id)
}

func grpcThrees(port int, version, id string) error {
	conn, err := grpc.NewClient(
		net.JoinHostPort("127.0.0.1", strconv.Itoa(port)),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return err
	}
	defer conn.Close()
	client := openinference.NewGRPCInferenceServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	resp, err := client.ModelInfer(ctx, &openinference.ModelInferRequest{
		ModelName:    "add",
		ModelVersion: version,
		Id:           id,
		Inputs: []*openinference.ModelInferRequest_InferInputTensor{
			{
				Name:     "A",
				Datatype: "FP32",
				Shape:    []int64{2, 3},
				Contents: &openinference.InferTensorContents{Fp32Contents: []float32{1, 1, 1, 1, 1, 1}},
			},
			{
				Name:     "B",
				Datatype: "FP32",
				Shape:    []int64{2, 3},
				Contents: &openinference.InferTensorContents{Fp32Contents: []float32{2, 2, 2, 2, 2, 2}},
			},
		},
	})
	if err != nil {
		return err
	}
	if resp.GetModelName() != "add" || resp.GetModelVersion() != "1" || resp.GetId() != id {
		return fmt.Errorf("grpc name %q version %q id %q", resp.GetModelName(), resp.GetModelVersion(), resp.GetId())
	}
	outs := resp.GetOutputs()
	if len(outs) != 1 || outs[0].GetName() != "C" || outs[0].GetDatatype() != "FP32" {
		return fmt.Errorf("grpc outputs %+v", outs)
	}
	if len(outs[0].GetShape()) != 2 || outs[0].GetShape()[0] != 2 || outs[0].GetShape()[1] != 3 {
		return fmt.Errorf("grpc shape %v", outs[0].GetShape())
	}
	values := outs[0].GetContents().GetFp32Contents()
	if len(values) != 6 {
		return fmt.Errorf("grpc values %v", values)
	}
	for i, value := range values {
		if value != 3 {
			return fmt.Errorf("grpc output[%d]=%v", i, value)
		}
	}
	return nil
}

func natsThrees(url, subject, id string) error {
	nc, err := nats.Connect(url, nats.NoReconnect(), nats.Timeout(5*time.Second))
	if err != nil {
		return err
	}
	defer nc.Close()
	status, hdr, body, err := natsRequest(nc, subject, nil, []byte(addJSON(id)))
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("nats %s status %d body %s", subject, status, trim(body))
	}
	if hdr.Get("Content-Type") != "application/json" {
		return fmt.Errorf("nats content type %q", hdr.Get("Content-Type"))
	}
	var doc inferDoc
	if err := json.Unmarshal(body, &doc); err != nil {
		return err
	}
	return threesDoc(doc, id, true)
}

func natsRequest(nc *nats.Conn, subject string, header nats.Header, body []byte) (int, nats.Header, []byte, error) {
	inbox := nc.NewInbox()
	sub, err := nc.SubscribeSync(inbox)
	if err != nil {
		return 0, nil, nil, err
	}
	defer func() { _ = sub.Unsubscribe() }()
	if err := nc.PublishMsg(&nats.Msg{Subject: subject, Reply: inbox, Header: header, Data: body}); err != nil {
		return 0, nil, nil, err
	}
	var joined []byte
	var statusHdr nats.Header
	status := -1
	for i := 0; ; i++ {
		msg, err := sub.NextMsg(10 * time.Second)
		if err != nil {
			return 0, nil, nil, fmt.Errorf("%s reply %d: %w", subject, i, err)
		}
		if i == 0 {
			status, err = strconv.Atoi(msg.Header.Get("Inference-Status"))
			if err != nil {
				return 0, nil, nil, fmt.Errorf("%s status: %w", subject, err)
			}
			statusHdr = msg.Header
		} else if len(msg.Header) == 0 && len(msg.Data) == 0 {
			return status, statusHdr, joined, nil
		}
		joined = append(joined, msg.Data...)
	}
}

func repositoryIndex(base string) ([]indexRow, error) {
	status, body, _, err := postInfer(base+"/v2/repository/index", []byte(`{}`), nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("index status %d body %s", status, trim(body))
	}
	var rows []indexRow
	if err := json.Unmarshal(body, &rows); err != nil {
		return nil, fmt.Errorf("index %s: %w", trim(body), err)
	}
	return rows, nil
}

func indexState(base, name string) (indexRow, error) {
	rows, err := repositoryIndex(base)
	if err != nil {
		return indexRow{}, err
	}
	for _, row := range rows {
		if row.Name == name {
			return row, nil
		}
	}
	return indexRow{}, fmt.Errorf("index has no %s", name)
}

func trim(body []byte) string {
	text := strings.TrimSpace(string(body))
	if len(text) > 300 {
		return text[:300]
	}
	return text
}
