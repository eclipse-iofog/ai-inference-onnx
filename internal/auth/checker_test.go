package auth

import (
	"net/http"
	"path/filepath"
	"testing"

	"github.com/eclipse-iofog/ai-inference-onnx/internal/config"
)

// TestDisabledIgnoresStaticToken checks that disabled allows a protected call
// without reading the static token.
func TestDisabledIgnoresStaticToken(t *testing.T) {
	const token = "0123456789abcdef0123456789abcdef"
	checker, err := New(config.Config{
		AuthMode:        config.AuthDisabled,
		AuthStaticToken: token,
	}, filepath.Join(t.TempDir(), "missing.jwk"))
	if err != nil {
		t.Fatalf("checker: %v", err)
	}
	now := checkTime()
	decision, gotErr := checker.AuthorizeHTTP(http.MethodPost, "/v2/models/a/infer", "", now)
	assertDecision(t, decision, gotErr, Allow)

	sameLength := "ffffffffffffffffffffffffffffffff"
	decision, gotErr = checker.AuthorizeHTTP(http.MethodPost, "/v2/models/a/infer", bearer(sameLength), now)
	assertDecision(t, decision, gotErr, Allow)

	decision, gotErr = checker.AuthorizeHTTP(http.MethodPost, "/v2/repository/index", bearer("short"), now)
	assertDecision(t, decision, gotErr, Allow)
}

// TestStaticToken checks the bearer compare, including mismatch and the wrong length.
func TestStaticToken(t *testing.T) {
	const token = "0123456789abcdef0123456789abcdef"
	checker, err := New(config.Config{
		AuthMode:        config.AuthStatic,
		AuthStaticToken: token,
	}, "")
	if err != nil {
		t.Fatalf("checker: %v", err)
	}
	now := checkTime()
	allowed := []struct {
		method string
		path   string
	}{
		{method: http.MethodPost, path: "/v2/models/a/infer"},
		{method: http.MethodPost, path: "/v2/models/a/versions/2/infer"},
		{method: http.MethodPost, path: "/v2/repository/index"},
		{method: http.MethodGet, path: "/v2/not-a-call"},
	}
	for _, call := range allowed {
		decision, gotErr := checker.AuthorizeHTTP(call.method, call.path, bearer(token), now)
		assertDecision(t, decision, gotErr, Allow)
	}
	decision, gotErr := checker.AuthorizeHTTP(http.MethodPost, "/v2/models/a/infer", "bearer "+token, now)
	assertDecision(t, decision, gotErr, Allow)
	decision, gotErr = checker.AuthorizeGRPC("/inference.GRPCInferenceService/ModelInfer", "a", "", bearer(token), now)
	assertDecision(t, decision, gotErr, Allow)
	decision, gotErr = checker.AuthorizeGRPC("/inference.GRPCInferenceService/RepositoryIndex", "", "", bearer(token), now)
	assertDecision(t, decision, gotErr, Allow)

	rejections := []struct {
		name   string
		header string
	}{
		{name: "mismatch", header: bearer("0123456789abcdef0123456789abcdee")},
		{name: "wrong length", header: bearer("0123456789abcdef0123456789abcde")},
		{name: "missing", header: ""},
		{name: "jwt shaped", header: bearer("eyJhbGciOiJFZERTQSJ9.eyJzdWIiOiJ4In0.sig")},
	}
	for _, rejection := range rejections {
		t.Run(rejection.name, func(t *testing.T) {
			decision, gotErr := checker.AuthorizeHTTP(http.MethodPost, "/v2/models/a/infer", rejection.header, now)
			assertDecision(t, decision, gotErr, Unauthenticated)
			assertNoSecret(t, gotErr, token, rejection.header, "eyJzdWIiOiJ4In0")
		})
	}
}

// TestHealthAndMetricsStayOpen checks that live, ready, and metrics are not operations.
func TestHealthAndMetricsStayOpen(t *testing.T) {
	const token = "0123456789abcdef0123456789abcdef"
	public, _ := newSigningKey(t)
	path := keyFilePath(t)
	writePublicJWK(t, path, public)
	edgelet := newEdgelet(t, path)
	staticChecker, err := New(config.Config{AuthMode: config.AuthStatic, AuthStaticToken: token}, "")
	if err != nil {
		t.Fatalf("static checker: %v", err)
	}
	disabled, err := New(config.Config{AuthMode: config.AuthDisabled, AuthStaticToken: token}, "")
	if err != nil {
		t.Fatalf("disabled checker: %v", err)
	}
	now := checkTime()
	bad := bearer("not-a-credential")
	paths := []struct {
		method string
		path   string
	}{
		{method: http.MethodGet, path: "/v2/health/live"},
		{method: http.MethodGet, path: "/v2/health/ready"},
		{method: http.MethodPost, path: "/v2/health/live"},
		{method: http.MethodGet, path: "/v2/health/ready/"},
		{method: http.MethodGet, path: "/metrics"},
		{method: http.MethodPost, path: "/metrics"},
	}
	for _, checker := range []*Checker{disabled, staticChecker, edgelet} {
		for _, call := range paths {
			decision, gotErr := checker.AuthorizeHTTP(call.method, call.path, bad, now)
			assertDecision(t, decision, gotErr, Allow)
		}
		for _, method := range []string{"ServerLive", "ServerReady"} {
			decision, gotErr := checker.AuthorizeGRPC("/inference.GRPCInferenceService/"+method, "", "", bad, now)
			assertDecision(t, decision, gotErr, Allow)
		}
	}
}
