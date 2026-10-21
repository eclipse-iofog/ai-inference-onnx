package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/eclipse-iofog/ai-inference-onnx/internal/auth"
	"github.com/eclipse-iofog/ai-inference-onnx/internal/batch"
	"github.com/eclipse-iofog/ai-inference-onnx/internal/config"
)

func TestServerMetadata(t *testing.T) {
	t.Parallel()
	srv := readServer(t, disabledChecker(t), nil)
	rec := call(t, srv, http.MethodGet, "/v2", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("Content-Type %q", rec.Header().Get("Content-Type"))
	}
	var doc serverMetadata
	decodeJSON(t, rec.Body.Bytes(), &doc)
	if doc.Name != "inference" || doc.Version != "dev" {
		t.Fatalf("metadata %+v", doc)
	}
	want := []string{"binary_tensor_data", "model_repository", "parameters", "schedule_policy"}
	if !slices.Equal(doc.Extensions, want) {
		t.Fatalf("extensions %v", doc.Extensions)
	}
	if strings.Contains(rec.Body.String(), "unload_dependents") {
		t.Fatalf("body %s", rec.Body.String())
	}
}

func TestModelList(t *testing.T) {
	t.Parallel()
	srv := readServer(t, disabledChecker(t), sampleDirectories())
	rec := call(t, srv, http.MethodGet, "/v2/models", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	var doc modelList
	decodeJSON(t, rec.Body.Bytes(), &doc)
	want := []string{"add", "alpha", "my.detector", "packed", "zeta"}
	if !slices.Equal(doc.Models, want) {
		t.Fatalf("models %v", doc.Models)
	}
	if strings.Contains(rec.Body.String(), "plain") {
		t.Fatalf("body %s", rec.Body.String())
	}

	empty := readServer(t, disabledChecker(t), nil)
	rec = call(t, empty, http.MethodGet, "/v2/models", "")
	if rec.Body.String() != `{"models":[]}` {
		t.Fatalf("empty body %s", rec.Body.String())
	}
}

func TestModelMetadata(t *testing.T) {
	t.Parallel()
	srv := readServer(t, disabledChecker(t), sampleDirectories())
	for _, path := range []string{"/v2/models/add", "/v2/models/add/versions/1"} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			doc := metadataAt(t, srv, path)
			if doc.Name != "add" || doc.Platform != "onnxruntime_onnx" {
				t.Fatalf("metadata %+v", doc)
			}
			if !slices.Equal(doc.Versions, []string{"1"}) {
				t.Fatalf("versions %v", doc.Versions)
			}
			if len(doc.Inputs) != 1 || doc.Inputs[0].Name != "a" || doc.Inputs[0].Datatype != "FP32" {
				t.Fatalf("inputs %+v", doc.Inputs)
			}
			if !slices.Equal(doc.Inputs[0].Shape, []int64{-1, 3}) {
				t.Fatalf("shape %v", doc.Inputs[0].Shape)
			}
			if len(doc.Outputs) != 1 || doc.Outputs[0].Name != "sum" || !slices.Equal(doc.Outputs[0].Shape, []int64{3}) {
				t.Fatalf("outputs %+v", doc.Outputs)
			}
		})
	}

	doc := metadataAt(t, srv, "/v2/models/packed")
	if !slices.Equal(doc.Versions, []string{"1", "2"}) || doc.Inputs[0].Name != "second" {
		t.Fatalf("chosen %+v", doc)
	}
	doc = metadataAt(t, srv, "/v2/models/packed/versions/1")
	if !slices.Equal(doc.Versions, []string{"1", "2"}) || doc.Inputs[0].Name != "first" {
		t.Fatalf("version 1 %+v", doc)
	}
	doc = metadataAt(t, srv, "/v2/models/my.detector")
	if doc.Name != "my.detector" || doc.Inputs[0].Name != "image" {
		t.Fatalf("dotted %+v", doc)
	}

	assertRouteError(t, srv, http.MethodGet, "/v2/models/add/versions/2", http.StatusNotFound)
	assertRouteError(t, srv, http.MethodGet, "/v2/models/add/versions/latest", http.StatusNotFound)
	assertRouteError(t, srv, http.MethodGet, "/v2/models/add/versions/01", http.StatusNotFound)
	assertRouteError(t, srv, http.MethodGet, "/v2/models/missing", http.StatusNotFound)
	assertRouteError(t, srv, http.MethodGet, "/v2/models/zeta", http.StatusServiceUnavailable)
	assertRouteError(t, srv, http.MethodGet, "/v2/models/alpha", http.StatusBadRequest)
	assertRouteError(t, srv, http.MethodGet, "/v2/models/plain", http.StatusBadRequest)
	assertRouteError(t, srv, http.MethodGet, "/v2/models/plain/versions/1", http.StatusNotFound)

	loading := call(t, srv, http.MethodGet, "/v2/models/zeta", "")
	assertErrorText(t, loading, http.StatusServiceUnavailable, "model is loading")
	unavailable := call(t, srv, http.MethodGet, "/v2/models/alpha/versions/1", "")
	assertErrorText(t, unavailable, http.StatusBadRequest, "session could not be opened")
}

func TestModelReady(t *testing.T) {
	t.Parallel()
	srv := readServer(t, disabledChecker(t), sampleDirectories())
	for _, path := range []string{
		"/v2/models/add/ready",
		"/v2/models/add/versions/1/ready",
		"/v2/models/packed/ready",
		"/v2/models/my.detector/versions/1/ready",
	} {
		assertEmpty(t, call(t, srv, http.MethodGet, path, ""), http.StatusOK)
	}
	loading := call(t, srv, http.MethodGet, "/v2/models/zeta/ready", "")
	assertErrorText(t, loading, http.StatusServiceUnavailable, "model is loading")
	loadingVersion := call(t, srv, http.MethodGet, "/v2/models/zeta/versions/1/ready", "")
	assertErrorText(t, loadingVersion, http.StatusServiceUnavailable, "model is loading")
	unavailable := call(t, srv, http.MethodGet, "/v2/models/alpha/ready", "")
	assertErrorText(t, unavailable, http.StatusBadRequest, "session could not be opened")
	assertRouteError(t, srv, http.MethodGet, "/v2/models/missing/ready", http.StatusNotFound)
	assertRouteError(t, srv, http.MethodGet, "/v2/models/add/versions/2/ready", http.StatusNotFound)
	assertRouteError(t, srv, http.MethodGet, "/v2/models/add/versions/latest/ready", http.StatusNotFound)
	assertRouteError(t, srv, http.MethodGet, "/v2/models/add/versions/01/ready", http.StatusNotFound)
	assertRouteError(t, srv, http.MethodGet, "/v2/models/zeta/versions/2/ready", http.StatusNotFound)
}

func TestRepositoryIndex(t *testing.T) {
	t.Parallel()
	srv := readServer(t, disabledChecker(t), sampleDirectories())
	rows := indexAt(t, srv, `{}`)
	requireIndexRow(t, rows, "plain", "", batch.StateUnavailable, "no ONNX model in directory")
	requireIndexRow(t, rows, "add", "1", batch.StateReady, "")
	requireIndexRow(t, rows, "zeta", "1", batch.StateLoading, "model is loading")
	requireIndexRow(t, rows, "packed", "1", batch.StateReady, "")
	requireIndexRow(t, rows, "packed", "2", batch.StateReady, "")
	if rows[0].Name != "add" || rows[1].Name != "alpha" {
		t.Fatalf("order %+v", namesOf(rows))
	}
	packed := versionOrder(rows, "packed")
	if !slices.Equal(packed, []string{"1", "2"}) {
		t.Fatalf("packed versions %v", packed)
	}

	ready := indexAt(t, srv, `{"ready":true,"unload_dependents":true}`)
	for _, row := range ready {
		if row.State != batch.StateReady || row.Reason != "" {
			t.Fatalf("ready row %+v", row)
		}
	}
	if len(ready) != 4 {
		t.Fatalf("ready rows %+v", ready)
	}
	for _, name := range []string{"plain", "alpha", "zeta"} {
		if indexHas(ready, name) {
			t.Fatalf("ready kept %s %+v", name, ready)
		}
	}

	ignored := indexAt(t, srv, `{"ready":false,"name":"x"}`)
	if len(ignored) != len(rows) {
		t.Fatalf("ignored %d want %d", len(ignored), len(rows))
	}

	assertRouteError(t, srv, http.MethodPost, "/v2/repository/index", http.StatusBadRequest)
	assertIndexError(t, srv, "")
	assertIndexError(t, srv, "   ")
	assertIndexError(t, srv, "null")
	assertIndexError(t, srv, `{"ready":"true"}`)
	assertIndexError(t, srv, `{"ready":1}`)
	assertIndexError(t, srv, `{"ready":null}`)

	empty := readServer(t, disabledChecker(t), nil)
	rec := callBody(t, empty, http.MethodPost, "/v2/repository/index", "", `{}`)
	if rec.Code != http.StatusOK || rec.Body.String() != "[]" {
		t.Fatalf("empty index %d %s", rec.Code, rec.Body.String())
	}
}

func TestLoadAndUnload(t *testing.T) {
	t.Parallel()
	paths := []string{
		"/v2/repository/models/missing/load",
		"/v2/repository/models/missing/unload",
		"/v2/repository/models/add/load",
		"/v2/repository/models/add/unload",
	}
	disabled := readServer(t, disabledChecker(t), nil)
	replaceCatalog(disabled, failCatalog{})
	staticChecker := mustChecker(t, config.Config{AuthMode: config.AuthStatic, AuthStaticToken: staticToken}, "")
	staticSrv := readServer(t, staticChecker, nil)
	replaceCatalog(staticSrv, failCatalog{})

	for _, path := range paths {
		t.Run("disabled "+path, func(t *testing.T) {
			t.Parallel()
			assertErrorText(t, call(t, disabled, http.MethodPost, path, ""), http.StatusNotImplemented, "loading and unloading models is not supported")
		})
		t.Run("static "+path, func(t *testing.T) {
			t.Parallel()
			rec := call(t, staticSrv, http.MethodPost, path, "Bearer "+staticToken)
			assertErrorText(t, rec, http.StatusNotImplemented, "loading and unloading models is not supported")
			if strings.Contains(rec.Body.String(), staticToken) {
				t.Fatal("response contains the token")
			}
		})
	}

	edgelet := edgeletChecker(t, accessRule([]string{"models/add"}, []string{"get"}))
	edgeletSrv := readServer(t, edgelet.checker, nil)
	replaceCatalog(edgeletSrv, failCatalog{})
	for _, path := range paths {
		rec := call(t, edgeletSrv, http.MethodPost, path, "Bearer "+edgelet.token)
		assertError(t, rec, http.StatusForbidden, edgelet.token, tokenIssuer, claimSentinel)
		if rec.Code == http.StatusNotImplemented {
			t.Fatal("load reached the handler")
		}
	}
}

func TestReadAuthAndMethods(t *testing.T) {
	t.Parallel()
	staticChecker := mustChecker(t, config.Config{AuthMode: config.AuthStatic, AuthStaticToken: staticToken}, "")
	srv := readServer(t, staticChecker, sampleDirectories())

	rec := call(t, srv, http.MethodGet, "/v2", "")
	assertError(t, rec, http.StatusUnauthorized, staticToken)

	live := call(t, srv, http.MethodGet, "/v2/health/live", "Bearer "+badStaticToken)
	assertEmpty(t, live, http.StatusOK)

	disabled := readServer(t, disabledChecker(t), sampleDirectories())
	postMeta := call(t, disabled, http.MethodPost, "/v2", "")
	assertError(t, postMeta, http.StatusMethodNotAllowed)
	if postMeta.Header().Get("Allow") != http.MethodGet {
		t.Fatalf("Allow %q", postMeta.Header().Get("Allow"))
	}
	getIndex := call(t, disabled, http.MethodGet, "/v2/repository/index", "")
	assertError(t, getIndex, http.StatusMethodNotAllowed)
	if getIndex.Header().Get("Allow") != http.MethodPost {
		t.Fatalf("Allow %q", getIndex.Header().Get("Allow"))
	}
	assertRouteError(t, disabled, http.MethodPost, "/v2/models", http.StatusMethodNotAllowed)
	assertRouteError(t, disabled, http.MethodPut, "/v2/models/add/ready", http.StatusMethodNotAllowed)

	infer := call(t, disabled, http.MethodPost, "/v2/models/add/infer", "")
	assertError(t, infer, http.StatusNotFound)
	if infer.Body.String() != `{"error":"not found"}` {
		t.Fatalf("infer body %s", infer.Body.String())
	}
	versioned := call(t, disabled, http.MethodPost, "/v2/models/add/versions/1/infer", "")
	assertError(t, versioned, http.StatusNotFound)
	staticInfer := call(t, srv, http.MethodPost, "/v2/models/add/infer", "Bearer "+staticToken)
	assertError(t, staticInfer, http.StatusNotFound, staticToken)
}

type directoryCatalog struct {
	directories []batch.Directory
}

func (c directoryCatalog) Models() []batch.Directory { return c.directories }

type failCatalog struct{}

func (failCatalog) Models() []batch.Directory {
	panic("model directories were read")
}

func readServer(t *testing.T, checker *auth.Checker, directories []batch.Directory) *Server {
	t.Helper()
	srv := New(checker, func() bool { return AnyReady() })
	srv.RegisterReads(directoryCatalog{directories: directories})
	return srv
}

func replaceCatalog(srv *Server, catalog Catalog) {
	srv.catalog = catalog
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

func metadataAt(t *testing.T, handler http.Handler, path string) modelMetadata {
	t.Helper()
	rec := call(t, handler, http.MethodGet, path, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("%s status %d body %s", path, rec.Code, rec.Body.String())
	}
	var doc modelMetadata
	decodeJSON(t, rec.Body.Bytes(), &doc)
	return doc
}

func indexAt(t *testing.T, handler http.Handler, body string) []batch.IndexEntry {
	t.Helper()
	rec := callBody(t, handler, http.MethodPost, "/v2/repository/index", "", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("index %d body %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("Content-Type %q", rec.Header().Get("Content-Type"))
	}
	var rows []batch.IndexEntry
	decodeJSON(t, rec.Body.Bytes(), &rows)
	return rows
}

func assertIndexError(t *testing.T, handler http.Handler, body string) {
	t.Helper()
	rec := callBody(t, handler, http.MethodPost, "/v2/repository/index", "", body)
	assertError(t, rec, http.StatusBadRequest)
}

func assertRouteError(t *testing.T, handler http.Handler, method, path string, status int) {
	t.Helper()
	assertError(t, call(t, handler, method, path, ""), status)
}

func assertErrorText(t *testing.T, rec *httptest.ResponseRecorder, status int, message string) {
	t.Helper()
	assertError(t, rec, status)
	var doc errorDocument
	decodeJSON(t, rec.Body.Bytes(), &doc)
	if doc.Error != message {
		t.Fatalf("error %q", doc.Error)
	}
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

func namesOf(rows []batch.IndexEntry) []string {
	names := make([]string, len(rows))
	for i, row := range rows {
		names[i] = row.Name
	}
	return names
}

func versionOrder(rows []batch.IndexEntry, name string) []string {
	var versions []string
	for _, row := range rows {
		if row.Name == name {
			versions = append(versions, row.Version)
		}
	}
	return versions
}

func decodeJSON(t *testing.T, body []byte, dest any) {
	t.Helper()
	if err := json.Unmarshal(body, dest); err != nil {
		t.Fatalf("json %s: %v", body, err)
	}
}

func callBody(t *testing.T, handler http.Handler, method, urlPath, authorization, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, urlPath, strings.NewReader(body))
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}
