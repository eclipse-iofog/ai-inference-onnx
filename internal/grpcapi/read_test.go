package grpcapi

import (
	"slices"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/eclipse-iofog/ai-inference-onnx/api/openinference/v2"
	"github.com/eclipse-iofog/ai-inference-onnx/internal/auth"
	"github.com/eclipse-iofog/ai-inference-onnx/internal/batch"
	"github.com/eclipse-iofog/ai-inference-onnx/internal/config"
)

func TestServerMetadata(t *testing.T) {
	t.Parallel()
	srv := New(disabledChecker(t), func() bool { return false })
	srv.SetIdentity("engine-1", "9.9.9")
	srv.RegisterReads(directoryCatalog{})
	client := dialServer(t, srv)
	resp, err := client.ServerMetadata(callCtx(t), &openinference.ServerMetadataRequest{})
	if status.Code(err) != codes.OK {
		t.Fatalf("metadata: %v", err)
	}
	if resp.GetName() != "engine-1" || resp.GetVersion() != "9.9.9" {
		t.Fatalf("metadata name %q version %q", resp.GetName(), resp.GetVersion())
	}
	want := []string{
		"binary_tensor_data", "model_repository", "parameters", "schedule_policy",
		"execution_providers:", "ort_version:",
	}
	if !slices.Equal(resp.GetExtensions(), want) {
		t.Fatalf("extensions %v", resp.GetExtensions())
	}
	if slices.Contains(resp.GetExtensions(), "model_repository(unload_dependents)") {
		t.Fatalf("extensions %v", resp.GetExtensions())
	}
}

func TestServerMetadataReportsRuntime(t *testing.T) {
	t.Parallel()
	srv := New(disabledChecker(t), func() bool { return false })
	srv.SetIdentity("engine-1", "9.9.9")
	srv.SetRuntime([]string{"TensorrtExecutionProvider", "CUDAExecutionProvider"}, "1.31.0")
	srv.RegisterReads(directoryCatalog{})
	client := dialServer(t, srv)
	resp, err := client.ServerMetadata(callCtx(t), &openinference.ServerMetadataRequest{})
	if status.Code(err) != codes.OK {
		t.Fatalf("metadata: %v", err)
	}
	want := []string{
		"binary_tensor_data", "model_repository", "parameters", "schedule_policy",
		"execution_providers:TensorrtExecutionProvider,CUDAExecutionProvider",
		"ort_version:1.31.0",
	}
	if !slices.Equal(resp.GetExtensions(), want) {
		t.Fatalf("extensions %v", resp.GetExtensions())
	}
}

func TestModelMetadata(t *testing.T) {
	t.Parallel()
	client := startReads(t, disabledChecker(t), sampleDirectories())
	for _, version := range []string{"", "1"} {
		t.Run(versionLabel(version), func(t *testing.T) {
			t.Parallel()
			resp := metadataAt(t, client, "add", version)
			if resp.GetName() != "add" || resp.GetPlatform() != "onnxruntime_onnx" {
				t.Fatalf("metadata %+v", resp)
			}
			if !slices.Equal(resp.GetVersions(), []string{"1"}) {
				t.Fatalf("versions %v", resp.GetVersions())
			}
			if len(resp.GetInputs()) != 1 || resp.GetInputs()[0].GetName() != "a" || resp.GetInputs()[0].GetDatatype() != "FP32" {
				t.Fatalf("inputs %+v", resp.GetInputs())
			}
			if !slices.Equal(resp.GetInputs()[0].GetShape(), []int64{-1, 3}) {
				t.Fatalf("shape %v", resp.GetInputs()[0].GetShape())
			}
			if len(resp.GetOutputs()) != 1 || resp.GetOutputs()[0].GetName() != "sum" || !slices.Equal(resp.GetOutputs()[0].GetShape(), []int64{3}) {
				t.Fatalf("outputs %+v", resp.GetOutputs())
			}
		})
	}

	resp := metadataAt(t, client, "packed", "")
	if !slices.Equal(resp.GetVersions(), []string{"1", "2"}) || resp.GetInputs()[0].GetName() != "second" {
		t.Fatalf("chosen %+v", resp)
	}
	resp = metadataAt(t, client, "packed", "1")
	if !slices.Equal(resp.GetVersions(), []string{"1", "2"}) || resp.GetInputs()[0].GetName() != "first" {
		t.Fatalf("version 1 %+v", resp)
	}
	resp = metadataAt(t, client, "my.detector", "")
	if resp.GetName() != "my.detector" || resp.GetInputs()[0].GetName() != "image" {
		t.Fatalf("dotted %+v", resp)
	}

	assertModelStatus(t, client, "add", "2", codes.NotFound, `version "2" was not found`)
	assertModelStatus(t, client, "add", "latest", codes.NotFound, `version "latest" was not found`)
	assertModelStatus(t, client, "add", "01", codes.NotFound, `version "01" was not found`)
	assertModelStatus(t, client, "missing", "", codes.NotFound, `model "missing" was not found`)
	assertModelStatus(t, client, "zeta", "", codes.Unavailable, "model is loading")
	assertModelStatus(t, client, "zeta", "1", codes.Unavailable, "model is loading")
	assertModelStatus(t, client, "alpha", "", codes.FailedPrecondition, "session could not be opened")
	assertModelStatus(t, client, "alpha", "1", codes.FailedPrecondition, "session could not be opened")
	assertModelStatus(t, client, "plain", "", codes.FailedPrecondition, "no ONNX model in directory")
	assertModelStatus(t, client, "plain", "1", codes.NotFound, `version "1" was not found`)
}

func TestModelReady(t *testing.T) {
	t.Parallel()
	client := startReads(t, disabledChecker(t), sampleDirectories())
	for _, call := range []struct {
		name    string
		version string
	}{
		{name: "add"},
		{name: "add", version: "1"},
		{name: "packed"},
		{name: "my.detector", version: "1"},
	} {
		resp, err := client.ModelReady(callCtx(t), &openinference.ModelReadyRequest{Name: call.name, Version: call.version})
		if status.Code(err) != codes.OK {
			t.Fatalf("%s %s: %v", call.name, call.version, err)
		}
		if !resp.GetReady() {
			t.Fatalf("%s %s ready is false", call.name, call.version)
		}
	}

	assertReadyStatus(t, client, "zeta", "", codes.Unavailable, "model is loading")
	assertReadyStatus(t, client, "zeta", "1", codes.Unavailable, "model is loading")
	assertReadyStatus(t, client, "alpha", "", codes.FailedPrecondition, "session could not be opened")
	assertReadyStatus(t, client, "missing", "", codes.NotFound, `model "missing" was not found`)
	assertReadyStatus(t, client, "add", "2", codes.NotFound, `version "2" was not found`)
	assertReadyStatus(t, client, "add", "latest", codes.NotFound, `version "latest" was not found`)
	assertReadyStatus(t, client, "add", "01", codes.NotFound, `version "01" was not found`)
	assertReadyStatus(t, client, "zeta", "2", codes.NotFound, `version "2" was not found`)
}

func TestRepositoryIndex(t *testing.T) {
	t.Parallel()
	dirs := sampleDirectories()
	client := startReads(t, disabledChecker(t), dirs)

	got := indexAt(t, client, "", false)
	if !slices.Equal(got, batch.Index(dirs, false)) {
		t.Fatalf("index %+v", got)
	}
	requireIndexRow(t, got, "plain", "", batch.StateUnavailable, "no ONNX model in directory")
	requireIndexRow(t, got, "add", "1", batch.StateReady, "")
	requireIndexRow(t, got, "zeta", "1", batch.StateLoading, "model is loading")

	ready := indexAt(t, client, "", true)
	if !slices.Equal(ready, batch.Index(dirs, true)) {
		t.Fatalf("ready index %+v", ready)
	}
	if len(ready) != 4 {
		t.Fatalf("ready rows %d", len(ready))
	}
	for _, row := range ready {
		if row.State != batch.StateReady || row.Reason != "" {
			t.Fatalf("ready row %+v", row)
		}
	}
	for _, name := range []string{"plain", "alpha", "zeta"} {
		if indexHas(ready, name) {
			t.Fatalf("ready kept %s", name)
		}
	}

	_, err := client.RepositoryIndex(callCtx(t), &openinference.RepositoryIndexRequest{RepositoryName: "other", Ready: true})
	assertStatus(t, err, codes.InvalidArgument, "repository name must be empty")
	_, err = client.RepositoryIndex(callCtx(t), &openinference.RepositoryIndexRequest{RepositoryName: " "})
	assertStatus(t, err, codes.InvalidArgument, "repository name must be empty")

	empty := startReads(t, disabledChecker(t), nil)
	if rows := indexAt(t, empty, "", false); len(rows) != 0 {
		t.Fatalf("empty index %+v", rows)
	}
}

func TestLoadAndUnload(t *testing.T) {
	t.Parallel()
	requests := []struct {
		name string
		load bool
		req  repositoryChange
	}{
		{name: "load missing", load: true, req: repositoryChange{model: "missing"}},
		{name: "unload missing", req: repositoryChange{model: "missing"}},
		{name: "load named repository", load: true, req: repositoryChange{repository: "other", model: "missing"}},
		{name: "unload named repository", req: repositoryChange{repository: "other", model: "add"}},
	}

	for _, call := range requests {
		t.Run("disabled "+call.name, func(t *testing.T) {
			t.Parallel()
			err := changeRepository(t, startFailingReads(t, disabledChecker(t)), "", call.load, call.req)
			assertStatus(t, err, codes.Unimplemented, "loading and unloading models is not supported")
		})
		t.Run("static "+call.name, func(t *testing.T) {
			t.Parallel()
			checker := mustChecker(t, config.Config{AuthMode: config.AuthStatic, AuthStaticToken: staticToken}, "")
			err := changeRepository(t, startFailingReads(t, checker), "Bearer "+staticToken, call.load, call.req)
			assertStatus(t, err, codes.Unimplemented, "loading and unloading models is not supported", staticToken)
		})
	}

	edgelet := edgeletChecker(t, accessRule([]string{"models/add"}, []string{"get"}))
	edgeletClient := startFailingReads(t, edgelet.checker)
	for _, call := range requests {
		err := changeRepository(t, edgeletClient, "Bearer "+edgelet.token, call.load, call.req)
		assertStatus(t, err, codes.PermissionDenied, "forbidden", edgelet.token, tokenIssuer, claimSentinel)
		if status.Code(err) == codes.Unimplemented {
			t.Fatal("load reached the handler")
		}
	}
}

func TestModelReadyRequiresCredential(t *testing.T) {
	t.Parallel()
	checker := mustChecker(t, config.Config{AuthMode: config.AuthStatic, AuthStaticToken: staticToken}, "")
	client := start(t, checker, func() bool { return true })

	_, err := client.ModelReady(callCtx(t), &openinference.ModelReadyRequest{Name: "add", Version: "1"})
	assertStatus(t, err, codes.Unauthenticated, "unauthenticated", staticToken)

	_, err = client.ServerLive(callCtx(t), &openinference.ServerLiveRequest{})
	if status.Code(err) != codes.OK {
		t.Fatalf("live: %v", err)
	}
	ready, err := client.ServerReady(callCtx(t), &openinference.ServerReadyRequest{})
	if status.Code(err) != codes.OK || !ready.GetReady() {
		t.Fatalf("ready %v err %v", ready.GetReady(), err)
	}

	_, err = client.ModelInfer(withAuth(callCtx(t), "Bearer "+staticToken), &openinference.ModelInferRequest{ModelName: "add"})
	assertStatus(t, err, codes.NotFound, `model "add" was not found`, staticToken)
}

type directoryCatalog struct {
	directories []batch.Directory
}

func (c directoryCatalog) Models() []batch.Directory { return c.directories }

type failCatalog struct{}

func (failCatalog) Models() []batch.Directory {
	panic("model directories were read")
}

func startReads(t *testing.T, checker *auth.Checker, directories []batch.Directory) openinference.GRPCInferenceServiceClient {
	t.Helper()
	srv := New(checker, func() bool { return false })
	srv.RegisterReads(directoryCatalog{directories: directories})
	return dialServer(t, srv)
}

func startFailingReads(t *testing.T, checker *auth.Checker) openinference.GRPCInferenceServiceClient {
	t.Helper()
	srv := New(checker, func() bool { return false })
	srv.RegisterReads(failCatalog{})
	return dialServer(t, srv)
}

func disabledChecker(t *testing.T) *auth.Checker {
	t.Helper()
	return mustChecker(t, config.Config{AuthMode: config.AuthDisabled}, "")
}

func sampleDirectories() []batch.Directory {
	return []batch.Directory{
		{
			Name:   "zeta",
			State:  batch.StateLoading,
			Reason: "model is loading",
			Versions: []batch.ModelVersion{{
				Version: "1",
				Signature: batch.Signature{
					Inputs:  []batch.TensorSpec{{Name: "pending", Datatype: batch.TypeFP32, Shape: []int64{1}}},
					Outputs: []batch.TensorSpec{{Name: "out", Datatype: batch.TypeFP32, Shape: []int64{1}}},
				},
			}},
		},
		{
			Name:   "alpha",
			State:  batch.StateUnavailable,
			Reason: "session could not be opened",
			Versions: []batch.ModelVersion{{
				Version: "1",
			}},
		},
		{
			Name:   "plain",
			State:  batch.StateUnavailable,
			Reason: "no ONNX model in directory",
		},
		{
			Name:  "add",
			State: batch.StateReady,
			Versions: []batch.ModelVersion{{
				Version: "1",
				Signature: batch.Signature{
					Inputs:  []batch.TensorSpec{{Name: "a", Datatype: batch.TypeFP32, Shape: []int64{-5, 3}}},
					Outputs: []batch.TensorSpec{{Name: "sum", Datatype: batch.TypeFP32, Shape: []int64{3}}},
				},
			}},
		},
		{
			Name:  "packed",
			State: batch.StateReady,
			Versions: []batch.ModelVersion{
				{
					Version: "2",
					Signature: batch.Signature{
						Inputs:  []batch.TensorSpec{{Name: "second", Datatype: batch.TypeFP32, Shape: []int64{1}}},
						Outputs: []batch.TensorSpec{{Name: "y", Datatype: batch.TypeInt32, Shape: []int64{1}}},
					},
				},
				{
					Version: "1",
					Signature: batch.Signature{
						Inputs:  []batch.TensorSpec{{Name: "first", Datatype: batch.TypeFP32, Shape: []int64{2}}},
						Outputs: []batch.TensorSpec{{Name: "z", Datatype: batch.TypeFP32, Shape: []int64{2}}},
					},
				},
			},
		},
		{
			Name:  "my.detector",
			State: batch.StateReady,
			Versions: []batch.ModelVersion{{
				Version: "1",
				Signature: batch.Signature{
					Inputs:  []batch.TensorSpec{{Name: "image", Datatype: batch.TypeUint8, Shape: []int64{1}}},
					Outputs: []batch.TensorSpec{{Name: "label", Datatype: batch.TypeInt64, Shape: []int64{1}}},
				},
			}},
		},
	}
}

func metadataAt(t *testing.T, client openinference.GRPCInferenceServiceClient, name, version string) *openinference.ModelMetadataResponse {
	t.Helper()
	resp, err := client.ModelMetadata(callCtx(t), &openinference.ModelMetadataRequest{Name: name, Version: version})
	if status.Code(err) != codes.OK {
		t.Fatalf("%s %s: %v", name, version, err)
	}
	return resp
}

func indexAt(t *testing.T, client openinference.GRPCInferenceServiceClient, repository string, ready bool) []batch.IndexEntry {
	t.Helper()
	resp, err := client.RepositoryIndex(callCtx(t), &openinference.RepositoryIndexRequest{
		RepositoryName: repository,
		Ready:          ready,
	})
	if status.Code(err) != codes.OK {
		t.Fatalf("index: %v", err)
	}
	rows := make([]batch.IndexEntry, 0, len(resp.GetModels()))
	for _, model := range resp.GetModels() {
		rows = append(rows, batch.IndexEntry{
			Name:    model.GetName(),
			Version: model.GetVersion(),
			State:   model.GetState(),
			Reason:  model.GetReason(),
		})
	}
	return rows
}

func assertModelStatus(t *testing.T, client openinference.GRPCInferenceServiceClient, name, version string, code codes.Code, message string) {
	t.Helper()
	_, err := client.ModelMetadata(callCtx(t), &openinference.ModelMetadataRequest{Name: name, Version: version})
	assertStatus(t, err, code, message)
}

func assertReadyStatus(t *testing.T, client openinference.GRPCInferenceServiceClient, name, version string, code codes.Code, message string) {
	t.Helper()
	resp, err := client.ModelReady(callCtx(t), &openinference.ModelReadyRequest{Name: name, Version: version})
	assertStatus(t, err, code, message)
	if resp.GetReady() {
		t.Fatal("ready is true")
	}
}

type repositoryChange struct {
	repository string
	model      string
}

func changeRepository(t *testing.T, client openinference.GRPCInferenceServiceClient, authorization string, load bool, change repositoryChange) error {
	t.Helper()
	ctx := callCtx(t)
	if authorization != "" {
		ctx = withAuth(ctx, authorization)
	}
	if load {
		_, err := client.RepositoryModelLoad(ctx, &openinference.RepositoryModelLoadRequest{
			RepositoryName: change.repository,
			ModelName:      change.model,
		})
		return err
	}
	_, err := client.RepositoryModelUnload(ctx, &openinference.RepositoryModelUnloadRequest{
		RepositoryName: change.repository,
		ModelName:      change.model,
	})
	return err
}

func requireIndexRow(t *testing.T, rows []batch.IndexEntry, name, version, state, reason string) {
	t.Helper()
	for _, row := range rows {
		if row.Name == name && row.Version == version {
			if row.State != state || row.Reason != reason {
				t.Fatalf("row %+v", row)
			}
			return
		}
	}
	t.Fatalf("missing %s %s in %+v", name, version, rows)
}

func indexHas(rows []batch.IndexEntry, name string) bool {
	for _, row := range rows {
		if row.Name == name {
			return true
		}
	}
	return false
}

func versionLabel(version string) string {
	if version == "" {
		return "default"
	}
	return version
}
