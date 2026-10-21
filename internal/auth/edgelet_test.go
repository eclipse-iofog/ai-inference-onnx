package auth

import (
	"net/http"
	"testing"
	"time"
)

// TestEdgeletAllowsNamedCalls checks each protected REST and gRPC call,
// including a dotted model name and an empty gRPC model version.
func TestEdgeletAllowsNamedCalls(t *testing.T) {
	public, private := newSigningKey(t)
	path := keyFilePath(t)
	writePublicJWK(t, path, public)
	checker := newEdgelet(t, path)
	now := checkTime()
	signed := signClaims(t, private, claimsFor(now, apiGroup,
		accessRule([]string{"server", "models", "models/my.detector", "repository/index"}, []string{"get"}),
		accessRule([]string{"models/my.detector/infer"}, []string{"create"}),
	))
	header := bearer(signed)
	httpCalls := []struct {
		method string
		path   string
	}{
		{method: http.MethodGet, path: "/v2"},
		{method: http.MethodGet, path: "/v2/models"},
		{method: http.MethodGet, path: "/v2/models/my.detector"},
		{method: http.MethodGet, path: "/v2/models/my.detector/versions/2"},
		{method: http.MethodGet, path: "/v2/models/my.detector/ready"},
		{method: http.MethodGet, path: "/v2/models/my.detector/versions/2/ready"},
		{method: http.MethodPost, path: "/v2/models/my.detector/infer"},
		{method: http.MethodPost, path: "/v2/models/my.detector/versions/2/infer"},
		{method: http.MethodPost, path: "/v2/repository/index"},
	}
	for _, call := range httpCalls {
		decision, err := checker.AuthorizeHTTP(call.method, call.path, header, now)
		if decision != Allow || err != nil {
			t.Fatalf("%s %s: decision %s err %v", call.method, call.path, decision, err)
		}
	}
	grpcCalls := []struct {
		method  string
		model   string
		version string
	}{
		{method: "/inference.GRPCInferenceService/ServerMetadata"},
		{method: "/inference.GRPCInferenceService/ModelMetadata", model: "my.detector"},
		{method: "/inference.GRPCInferenceService/ModelMetadata", model: "my.detector", version: "2"},
		{method: "/inference.GRPCInferenceService/ModelReady", model: "my.detector", version: "2"},
		{method: "/inference.GRPCInferenceService/ModelInfer", model: "my.detector"},
		{method: "/inference.GRPCInferenceService/ModelInfer", model: "my.detector", version: "2"},
		{method: "RepositoryIndex"},
	}
	for _, call := range grpcCalls {
		decision, err := checker.AuthorizeGRPC(call.method, call.model, call.version, header, now)
		if decision != Allow || err != nil {
			t.Fatalf("%s %s %s: decision %s err %v", call.method, call.model, call.version, decision, err)
		}
	}
}

// TestEdgeletCheckOrder fails each check on its own.
// A signature failure stays unauthenticated when the rules would forbid the call.
func TestEdgeletCheckOrder(t *testing.T) {
	public, private := newSigningKey(t)
	path := keyFilePath(t)
	writePublicJWK(t, path, public)
	checker := newEdgelet(t, path)
	now := checkTime()
	allowing := func() map[string]any {
		return accessRule([]string{"server"}, []string{"get"})
	}

	t.Run("bridge audience", func(t *testing.T) {
		signed := signClaims(t, private, claimsFor(now, apiGroup, allowing()))
		decision, err := checker.AuthorizeHTTP(http.MethodGet, "/v2", bearer(signed), now)
		assertDecision(t, decision, err, Allow)
	})

	t.Run("bad signature", func(t *testing.T) {
		_, other := newSigningKey(t)
		signed := signClaims(t, other, claimsFor(now, apiGroup))
		decision, err := checker.AuthorizeHTTP(http.MethodGet, "/v2", bearer(signed), now)
		if decision == Forbidden {
			t.Fatal("signature failure must not be forbidden")
		}
		assertDecision(t, decision, err, Unauthenticated)
		assertNoSecret(t, err, signed, edgeletIssuer)
	})

	t.Run("missing header", func(t *testing.T) {
		decision, err := checker.AuthorizeHTTP(http.MethodGet, "/v2", "", now)
		assertDecision(t, decision, err, Unauthenticated)
	})

	t.Run("wrong token use", func(t *testing.T) {
		claims := claimsFor(now, apiGroup, allowing())
		claims["tokenUse"] = "edgeletapi"
		signed := signClaims(t, private, claims)
		decision, err := checker.AuthorizeHTTP(http.MethodGet, "/v2", bearer(signed), now)
		assertDecision(t, decision, err, Unauthenticated)
		assertNoSecret(t, err, signed, "edgeletapi", edgeletIssuer)
	})

	t.Run("expired", func(t *testing.T) {
		claims := claimsFor(now, apiGroup, allowing())
		claims["exp"] = now.Unix()
		signed := signClaims(t, private, claims)
		decision, err := checker.AuthorizeHTTP(http.MethodGet, "/v2", bearer(signed), now)
		assertDecision(t, decision, err, Unauthenticated)
		assertNoSecret(t, err, signed)
	})

	t.Run("not before", func(t *testing.T) {
		claims := claimsFor(now, apiGroup, allowing())
		claims["nbf"] = now.Add(time.Second).Unix()
		signed := signClaims(t, private, claims)
		decision, err := checker.AuthorizeHTTP(http.MethodGet, "/v2", bearer(signed), now)
		assertDecision(t, decision, err, Unauthenticated)
		assertNoSecret(t, err, signed)
	})

	t.Run("issued at", func(t *testing.T) {
		claims := claimsFor(now, apiGroup, allowing())
		claims["iat"] = now.Add(time.Second).Unix()
		signed := signClaims(t, private, claims)
		decision, err := checker.AuthorizeHTTP(http.MethodGet, "/v2", bearer(signed), now)
		assertDecision(t, decision, err, Unauthenticated)
		assertNoSecret(t, err, signed)
	})

	t.Run("issuer", func(t *testing.T) {
		claims := claimsFor(now, apiGroup, allowing())
		claims["iss"] = "https://other.example"
		signed := signClaims(t, private, claims)
		decision, err := checker.AuthorizeHTTP(http.MethodGet, "/v2", bearer(signed), now)
		assertDecision(t, decision, err, Unauthenticated)
		assertNoSecret(t, err, signed, "https://other.example")
	})

	t.Run("bounds", func(t *testing.T) {
		claims := claimsFor(now, apiGroup, allowing())
		claims["iat"] = now.Unix()
		claims["nbf"] = now.Unix()
		claims["exp"] = now.Add(time.Second).Unix()
		signed := signClaims(t, private, claims)
		decision, err := checker.AuthorizeHTTP(http.MethodGet, "/v2", bearer(signed), now)
		assertDecision(t, decision, err, Allow)
	})
}

// TestEdgeletRules checks wildcards, a dotted model name, and deny by default.
func TestEdgeletRules(t *testing.T) {
	public, private := newSigningKey(t)
	path := keyFilePath(t)
	writePublicJWK(t, path, public)
	checker := newEdgelet(t, path)
	now := checkTime()

	httpCases := []struct {
		name   string
		group  string
		rules  []map[string]any
		method string
		path   string
		want   Decision
	}{
		{
			name:   "wildcard metadata allows dotted name",
			rules:  []map[string]any{accessRule([]string{"models/*"}, []string{"get"})},
			method: http.MethodGet,
			path:   "/v2/models/my.detector",
			want:   Allow,
		},
		{
			name:   "wildcard metadata allows version",
			rules:  []map[string]any{accessRule([]string{"models/*"}, []string{"get"})},
			method: http.MethodGet,
			path:   "/v2/models/my.detector/versions/2",
			want:   Allow,
		},
		{
			name:   "wildcard metadata denies infer",
			rules:  []map[string]any{accessRule([]string{"models/*"}, []string{"get", "create"})},
			method: http.MethodPost,
			path:   "/v2/models/my.detector/infer",
			want:   Forbidden,
		},
		{
			name:   "wildcard metadata denies versioned infer",
			rules:  []map[string]any{accessRule([]string{"models/*"}, []string{"create"})},
			method: http.MethodPost,
			path:   "/v2/models/my.detector/versions/2/infer",
			want:   Forbidden,
		},
		{
			name:   "wildcard metadata denies the collection",
			rules:  []map[string]any{accessRule([]string{"models/*"}, []string{"get"})},
			method: http.MethodGet,
			path:   "/v2/models",
			want:   Forbidden,
		},
		{
			name:   "wildcard infer allows dotted name",
			rules:  []map[string]any{accessRule([]string{"models/*/infer"}, []string{"create"})},
			method: http.MethodPost,
			path:   "/v2/models/my.detector/infer",
			want:   Allow,
		},
		{
			name:   "wildcard infer allows version",
			rules:  []map[string]any{accessRule([]string{"models/*/infer"}, []string{"create"})},
			method: http.MethodPost,
			path:   "/v2/models/my.detector/versions/2/infer",
			want:   Allow,
		},
		{
			name:   "wildcard infer denies metadata",
			rules:  []map[string]any{accessRule([]string{"models/*/infer"}, []string{"get", "create"})},
			method: http.MethodGet,
			path:   "/v2/models/my.detector",
			want:   Forbidden,
		},
		{
			name:   "wildcard infer denies ready",
			rules:  []map[string]any{accessRule([]string{"models/*/infer"}, []string{"get"})},
			method: http.MethodGet,
			path:   "/v2/models/my.detector/ready",
			want:   Forbidden,
		},
		{
			name:   "dotted name allows metadata",
			rules:  []map[string]any{accessRule([]string{"models/my.detector"}, []string{"get"})},
			method: http.MethodGet,
			path:   "/v2/models/my.detector/versions/2",
			want:   Allow,
		},
		{
			name:   "dotted name denies another model",
			rules:  []map[string]any{accessRule([]string{"models/my.detector"}, []string{"get"})},
			method: http.MethodGet,
			path:   "/v2/models/other",
			want:   Forbidden,
		},
		{
			name:   "dotted name denies infer",
			rules:  []map[string]any{accessRule([]string{"models/my.detector"}, []string{"get", "create"})},
			method: http.MethodPost,
			path:   "/v2/models/my.detector/infer",
			want:   Forbidden,
		},
		{
			name: "create does not allow repository index",
			rules: []map[string]any{
				accessRule([]string{"repository/index"}, []string{"create"}),
				accessRule([]string{"models/a/infer"}, []string{"create"}),
			},
			method: http.MethodPost,
			path:   "/v2/repository/index",
			want:   Forbidden,
		},
		{
			name: "create allows infer",
			rules: []map[string]any{
				accessRule([]string{"repository/index"}, []string{"create"}),
				accessRule([]string{"models/a/infer"}, []string{"create"}),
			},
			method: http.MethodPost,
			path:   "/v2/models/a/infer",
			want:   Allow,
		},
		{
			name:   "repository index allows get",
			rules:  []map[string]any{accessRule([]string{"repository/index"}, []string{"get"})},
			method: http.MethodPost,
			path:   "/v2/repository/index",
			want:   Allow,
		},
		{
			name:   "get is not repository index",
			rules:  []map[string]any{accessRule([]string{"repository/index"}, []string{"get"})},
			method: http.MethodGet,
			path:   "/v2/repository/index",
			want:   Forbidden,
		},
		{
			name:   "wrong verb",
			rules:  []map[string]any{accessRule([]string{"server"}, []string{"create"})},
			method: http.MethodGet,
			path:   "/v2",
			want:   Forbidden,
		},
		{
			name:   "other api group",
			group:  "inference.datasance.com/v2",
			rules:  []map[string]any{accessRule([]string{"server"}, []string{"get"})},
			method: http.MethodGet,
			path:   "/v2",
			want:   Forbidden,
		},
		{
			name:   "no matching rule",
			rules:  []map[string]any{accessRule([]string{"models/other"}, []string{"get"})},
			method: http.MethodGet,
			path:   "/v2",
			want:   Forbidden,
		},
		{
			name:   "unnamed path",
			rules:  []map[string]any{accessRule([]string{"server"}, []string{"get"})},
			method: http.MethodGet,
			path:   "/v2/not-a-call",
			want:   Forbidden,
		},
	}

	for _, tc := range httpCases {
		t.Run(tc.name, func(t *testing.T) {
			signed := signClaims(t, private, claimsFor(now, tc.group, tc.rules...))
			decision, err := checker.AuthorizeHTTP(tc.method, tc.path, bearer(signed), now)
			assertDecision(t, decision, err, tc.want)
			assertNoSecret(t, err, signed, edgeletIssuer, "my.detector", "inference.datasance.com/v2")
		})
	}

	grpcCases := []struct {
		name    string
		rules   []map[string]any
		method  string
		model   string
		version string
		want    Decision
	}{
		{
			name:   "wildcard metadata allows grpc",
			rules:  []map[string]any{accessRule([]string{"models/*"}, []string{"get"})},
			method: "/inference.GRPCInferenceService/ModelMetadata",
			model:  "my.detector",
			want:   Allow,
		},
		{
			name:   "wildcard metadata denies grpc infer",
			rules:  []map[string]any{accessRule([]string{"models/*"}, []string{"create"})},
			method: "/inference.GRPCInferenceService/ModelInfer",
			model:  "my.detector",
			want:   Forbidden,
		},
		{
			name:    "wildcard infer allows empty version",
			rules:   []map[string]any{accessRule([]string{"models/*/infer"}, []string{"create"})},
			method:  "/inference.GRPCInferenceService/ModelInfer",
			model:   "my.detector",
			version: "",
			want:    Allow,
		},
		{
			name:    "wildcard infer allows set version",
			rules:   []map[string]any{accessRule([]string{"models/*/infer"}, []string{"create"})},
			method:  "/inference.GRPCInferenceService/ModelInfer",
			model:   "my.detector",
			version: "2",
			want:    Allow,
		},
		{
			name:   "wildcard infer denies grpc metadata",
			rules:  []map[string]any{accessRule([]string{"models/*/infer"}, []string{"get"})},
			method: "/inference.GRPCInferenceService/ModelMetadata",
			model:  "my.detector",
			want:   Forbidden,
		},
		{
			name:    "dotted name allows empty version",
			rules:   []map[string]any{accessRule([]string{"models/my.detector"}, []string{"get"})},
			method:  "/inference.GRPCInferenceService/ModelReady",
			model:   "my.detector",
			version: "",
			want:    Allow,
		},
		{
			name:   "create does not allow grpc repository index",
			rules:  []map[string]any{accessRule([]string{"repository/index"}, []string{"create"})},
			method: "/inference.GRPCInferenceService/RepositoryIndex",
			want:   Forbidden,
		},
		{
			name:   "unnamed grpc method",
			rules:  []map[string]any{accessRule([]string{"server"}, []string{"get"})},
			method: "/inference.GRPCInferenceService/RepositoryModelLoad",
			model:  "my.detector",
			want:   Forbidden,
		},
	}
	for _, tc := range grpcCases {
		t.Run(tc.name, func(t *testing.T) {
			signed := signClaims(t, private, claimsFor(now, apiGroup, tc.rules...))
			decision, err := checker.AuthorizeGRPC(tc.method, tc.model, tc.version, bearer(signed), now)
			assertDecision(t, decision, err, tc.want)
			assertNoSecret(t, err, signed, edgeletIssuer, "my.detector")
		})
	}

	t.Run("unnamed path with bad signature", func(t *testing.T) {
		_, other := newSigningKey(t)
		signed := signClaims(t, other, claimsFor(now, apiGroup))
		decision, err := checker.AuthorizeHTTP(http.MethodGet, "/v2/not-a-call", bearer(signed), now)
		if decision == Forbidden {
			t.Fatal("signature failure must not be forbidden")
		}
		assertDecision(t, decision, err, Unauthenticated)
		assertNoSecret(t, err, signed)
	})
}
