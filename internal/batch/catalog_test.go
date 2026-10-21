package batch

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"testing"
)

func TestNames(t *testing.T) {
	t.Parallel()
	got := Names([]Directory{
		{Name: "zeta", State: StateLoading, Reason: "model is loading", Versions: []ModelVersion{{Version: "1"}}},
		{Name: "plain", State: StateUnavailable, Reason: "no ONNX model in directory"},
		{Name: "alpha", State: StateUnavailable, Reason: "session could not be opened", Versions: []ModelVersion{{Version: "1"}}},
		{Name: "zeta", State: StateLoading, Versions: []ModelVersion{{Version: "2"}}},
	})
	want := []string{"alpha", "zeta"}
	if !slices.Equal(got, want) {
		t.Fatalf("names %v", got)
	}
	if Names(nil) == nil {
		t.Fatal("nil snapshot produced a nil list")
	}
	if len(Names(nil)) != 0 || len(Names([]Directory{})) != 0 {
		t.Fatal("empty snapshot is not an empty list")
	}
}

func TestVersionNames(t *testing.T) {
	t.Parallel()
	got := VersionNames([]ModelVersion{{Version: "10"}, {Version: "2"}, {Version: "1"}})
	want := []string{"1", "2", "10"}
	if !slices.Equal(got, want) {
		t.Fatalf("versions %v", got)
	}
	if VersionNames(nil) == nil {
		t.Fatal("nil versions")
	}
}

func TestIndex(t *testing.T) {
	t.Parallel()
	directories := []Directory{
		{
			Name:   "zeta",
			State:  StateLoading,
			Reason: "model is loading",
			Versions: []ModelVersion{
				{Version: "10"},
				{Version: "2"},
			},
		},
		{Name: "plain", State: StateUnavailable, Reason: "no ONNX model in directory"},
		{
			Name:     "add",
			State:    StateReady,
			Reason:   "should be cleared",
			Versions: []ModelVersion{{Version: "1"}},
		},
	}
	rows := Index(directories, false)
	if len(rows) != 4 {
		t.Fatalf("rows %+v", rows)
	}
	want := []IndexEntry{
		{Name: "add", Version: "1", State: StateReady, Reason: ""},
		{Name: "plain", State: StateUnavailable, Reason: "no ONNX model in directory"},
		{Name: "zeta", Version: "2", State: StateLoading, Reason: "model is loading"},
		{Name: "zeta", Version: "10", State: StateLoading, Reason: "model is loading"},
	}
	for i := range want {
		if rows[i] != want[i] {
			t.Fatalf("row %d %+v", i, rows[i])
		}
	}
	body, err := json.Marshal(rows[1])
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatal(err)
	}
	if _, ok := fields["version"]; ok {
		t.Fatalf("version present in %s", body)
	}
	if fields["reason"] != "no ONNX model in directory" {
		t.Fatalf("reason %v", fields["reason"])
	}
	readyBody, err := json.Marshal(rows[0])
	if err != nil {
		t.Fatal(err)
	}
	var readyFields map[string]any
	if err := json.Unmarshal(readyBody, &readyFields); err != nil {
		t.Fatal(err)
	}
	reason, ok := readyFields["reason"].(string)
	if !ok || reason != "" {
		t.Fatalf("ready reason %v body %s", readyFields["reason"], readyBody)
	}

	ready := Index(directories, true)
	if len(ready) != 1 || ready[0].Name != "add" || ready[0].State != StateReady || ready[0].Reason != "" {
		t.Fatalf("ready rows %+v", ready)
	}
	if Index(nil, false) == nil || len(Index(nil, true)) != 0 {
		t.Fatal("empty index")
	}
}

func TestSelect(t *testing.T) {
	t.Parallel()
	directories := []Directory{
		{
			Name:  "add",
			State: StateReady,
			Versions: []ModelVersion{
				{Version: "1", Signature: Signature{Inputs: []TensorSpec{{Name: "a", Datatype: TypeFP32, Shape: []int64{2}}}}},
			},
		},
		{
			Name:  "packed",
			State: StateReady,
			Versions: []ModelVersion{
				{Version: "2", Signature: Signature{Inputs: []TensorSpec{{Name: "second"}}}},
				{Version: "1", Signature: Signature{Inputs: []TensorSpec{{Name: "first"}}}},
			},
		},
		{Name: "zeta", State: StateLoading, Reason: "model is loading", Versions: []ModelVersion{{Version: "1"}}},
		{Name: "alpha", State: StateUnavailable, Reason: "session could not be opened", Versions: []ModelVersion{{Version: "1"}}},
		{Name: "plain", State: StateUnavailable, Reason: "no ONNX model in directory"},
	}

	_, version, err := Select(directories, "add", "")
	if err != nil || version.Version != "1" || version.Signature.Inputs[0].Name != "a" {
		t.Fatalf("flat %+v err %v", version, err)
	}
	_, version, err = Select(directories, "add", "1")
	if err != nil || version.Signature.Inputs[0].Name != "a" {
		t.Fatalf("version 1 %+v err %v", version, err)
	}
	_, version, err = Select(directories, "packed", "")
	if err != nil || version.Version != "2" || version.Signature.Inputs[0].Name != "second" {
		t.Fatalf("chosen %+v err %v", version, err)
	}
	_, version, err = Select(directories, "packed", "1")
	if err != nil || version.Signature.Inputs[0].Name != "first" {
		t.Fatalf("packed 1 %+v err %v", version, err)
	}

	assertSelect(t, directories, "add", "2", KindNotFound, http.StatusNotFound, `version "2" was not found`)
	assertSelect(t, directories, "add", "latest", KindNotFound, http.StatusNotFound, `version "latest" was not found`)
	assertSelect(t, directories, "add", "01", KindNotFound, http.StatusNotFound, `version "01" was not found`)
	assertSelect(t, directories, "missing", "", KindNotFound, http.StatusNotFound, `model "missing" was not found`)
	assertSelect(t, directories, "zeta", "", KindLoading, http.StatusServiceUnavailable, "model is loading")
	assertSelect(t, directories, "zeta", "1", KindLoading, http.StatusServiceUnavailable, "model is loading")
	assertSelect(t, directories, "zeta", "2", KindNotFound, http.StatusNotFound, `version "2" was not found`)
	assertSelect(t, directories, "alpha", "", KindUnavailable, http.StatusBadRequest, "session could not be opened")
	assertSelect(t, directories, "alpha", "1", KindUnavailable, http.StatusBadRequest, "session could not be opened")
	assertSelect(t, directories, "plain", "", KindUnavailable, http.StatusBadRequest, "no ONNX model in directory")
	assertSelect(t, directories, "plain", "1", KindNotFound, http.StatusNotFound, `version "1" was not found`)
}

func assertSelect(t *testing.T, directories []Directory, name, version string, kind Kind, status int, reason string) {
	t.Helper()
	_, _, err := Select(directories, name, version)
	var got *Error
	if !errors.As(err, &got) {
		t.Fatalf("%s %s: %v", name, version, err)
	}
	if got.Kind != kind || got.Reason != reason {
		t.Fatalf("%s %s kind %d reason %q", name, version, got.Kind, got.Reason)
	}
	result := Status(err)
	if result.HTTP != status || result.Message != reason {
		t.Fatalf("%s %s status %d message %q", name, version, result.HTTP, result.Message)
	}
}
