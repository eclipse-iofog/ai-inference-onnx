//go:build cgo

package main

import (
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/eclipse-iofog/ai-inference-onnx/api/openinference/v2"
	"github.com/eclipse-iofog/ai-inference-onnx/internal/auth"
	"github.com/eclipse-iofog/ai-inference-onnx/internal/config"
	"github.com/eclipse-iofog/ai-inference-onnx/internal/grpcapi"
)

func TestGRPCInferAddModel(t *testing.T) {
	t.Parallel()
	requireRuntime(t)

	models := filepath.Join(t.TempDir(), "models")
	knowledge := filepath.Join(t.TempDir(), "knowledge")
	copyAddModel(t, models)
	if err := os.MkdirAll(knowledge, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		AuthMode:        config.AuthDisabled,
		ModelsPath:      models,
		KnowledgePath:   knowledge,
		MaxPayloadBytes: 32 << 20,
		QueueCapacity:   8,
		IntraOpThreads:  1,
		InterOpThreads:  1,
		LogLevel:        "error",
	}
	rt := startModels(cfg, newLogger(io.Discard, "error"), nil)
	t.Cleanup(func() {
		if rt.watcher != nil {
			_ = rt.watcher.Close()
		}
		if rt.repo != nil {
			_ = rt.repo.Close()
		}
	})
	if !rt.ready() {
		t.Fatal("add model did not become ready")
	}
	checker, err := auth.New(cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	// Cleartext. The test dials with insecure credentials.
	srv := grpcapi.New(checker, rt.ready)
	srv.RegisterReads(rt)
	srv.RegisterInfer(grpcapi.InferConfig{
		Catalog:         rt,
		Runner:          rt,
		MaxPayloadBytes: cfg.MaxPayloadBytes,
	})
	client := dialBufconn(t, srv)

	for _, version := range []string{"", "1"} {
		resp := grpcAdd(t, client, version, "add-values", false)
		assertGRPCThrees(t, resp, "add-values")
		resp = grpcAdd(t, client, version, "add-raw", true)
		assertGRPCThrees(t, resp, "add-raw")
	}
	for _, version := range []string{"2", "latest", "01"} {
		_, err = client.ModelInfer(context.Background(), grpcAddRequest(version, "x", false))
		if status.Code(err) != codes.NotFound {
			t.Fatalf("version %q: %v", version, err)
		}
	}
	_, err = client.ModelInfer(context.Background(), &openinference.ModelInferRequest{ModelName: "missing"})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("missing: %v", err)
	}
	_, err = client.RepositoryModelLoad(context.Background(), &openinference.RepositoryModelLoadRequest{ModelName: "add"})
	if status.Code(err) != codes.Unimplemented {
		t.Fatalf("load: %v", err)
	}
	_, err = client.RepositoryModelUnload(context.Background(), &openinference.RepositoryModelUnloadRequest{ModelName: "add"})
	if status.Code(err) != codes.Unimplemented {
		t.Fatalf("unload: %v", err)
	}
}

func assertGRPCAddInfer(t *testing.T, port int) {
	t.Helper()
	conn, err := grpc.NewClient(hostPort(port), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial grpc: %v", err)
	}
	defer func() {
		if err := conn.Close(); err != nil {
			t.Errorf("close grpc: %v", err)
		}
	}()
	client := openinference.NewGRPCInferenceServiceClient(conn)
	for _, version := range []string{"", "1"} {
		resp := grpcAdd(t, client, version, "add-values", false)
		assertGRPCThrees(t, resp, "add-values")
		resp = grpcAdd(t, client, version, "add-raw", true)
		assertGRPCThrees(t, resp, "add-raw")
	}
	_, err = client.ModelInfer(context.Background(), grpcAddRequest("2", "x", false))
	if status.Code(err) != codes.NotFound {
		t.Fatalf("version 2: %v", err)
	}
}

func dialBufconn(t *testing.T, srv *grpcapi.Server) openinference.GRPCInferenceServiceClient {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	go func() {
		_ = srv.Serve(lis)
	}()
	t.Cleanup(srv.Stop)
	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Errorf("close client: %v", err)
		}
	})
	return openinference.NewGRPCInferenceServiceClient(conn)
}

func grpcAdd(t *testing.T, client openinference.GRPCInferenceServiceClient, version, id string, raw bool) *openinference.ModelInferResponse {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resp, err := client.ModelInfer(ctx, grpcAddRequest(version, id, raw))
	if err != nil {
		t.Fatalf("version %q raw %v: %v", version, raw, err)
	}
	return resp
}

func grpcAddRequest(version, id string, raw bool) *openinference.ModelInferRequest {
	req := &openinference.ModelInferRequest{
		ModelName:    "add",
		ModelVersion: version,
		Id:           id,
		Inputs: []*openinference.ModelInferRequest_InferInputTensor{
			{Name: "A", Datatype: "FP32", Shape: []int64{2, 3}},
			{Name: "B", Datatype: "FP32", Shape: []int64{2, 3}},
		},
	}
	if raw {
		req.RawInputContents = [][]byte{float32LE(1, 1, 1, 1, 1, 1), float32LE(2, 2, 2, 2, 2, 2)}
		return req
	}
	req.Inputs[0].Contents = &openinference.InferTensorContents{Fp32Contents: []float32{1, 1, 1, 1, 1, 1}}
	req.Inputs[1].Contents = &openinference.InferTensorContents{Fp32Contents: []float32{2, 2, 2, 2, 2, 2}}
	return req
}

func assertGRPCThrees(t *testing.T, resp *openinference.ModelInferResponse, id string) {
	t.Helper()
	if resp.GetModelName() != "add" || resp.GetModelVersion() != "1" || resp.GetId() != id {
		t.Fatalf("response name %q version %q id %q", resp.GetModelName(), resp.GetModelVersion(), resp.GetId())
	}
	if len(resp.GetOutputs()) != 1 || resp.GetOutputs()[0].GetName() != "C" || resp.GetOutputs()[0].GetDatatype() != "FP32" {
		t.Fatalf("outputs %+v", resp.GetOutputs())
	}
	if len(resp.GetOutputs()[0].GetShape()) != 2 || resp.GetOutputs()[0].GetShape()[0] != 2 || resp.GetOutputs()[0].GetShape()[1] != 3 {
		t.Fatalf("shape %v", resp.GetOutputs()[0].GetShape())
	}
	got := resp.GetOutputs()[0].GetContents().GetFp32Contents()
	if len(got) != 6 {
		t.Fatalf("values %v raw %d", got, len(resp.GetRawOutputContents()))
	}
	for i, value := range got {
		if value != 3 {
			t.Fatalf("output[%d]=%v", i, value)
		}
	}
}
