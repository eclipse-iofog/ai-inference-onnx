package openinference

import (
	"slices"
	"testing"
)

func TestOpenInferenceService(t *testing.T) {
	t.Parallel()
	desc := GRPCInferenceService_ServiceDesc
	if desc.ServiceName != "inference.GRPCInferenceService" {
		t.Fatalf("service %q", desc.ServiceName)
	}
	if len(desc.Streams) != 0 {
		t.Fatalf("streams %d", len(desc.Streams))
	}
	got := make([]string, len(desc.Methods))
	for i, method := range desc.Methods {
		got[i] = method.MethodName
	}
	want := []string{
		"ServerLive",
		"ServerReady",
		"ModelReady",
		"ServerMetadata",
		"ModelMetadata",
		"ModelInfer",
		"RepositoryIndex",
		"RepositoryModelLoad",
		"RepositoryModelUnload",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("methods %v", got)
	}
}
