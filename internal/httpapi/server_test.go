package httpapi

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/eclipse-iofog/ai-inference-onnx/internal/auth"
	"github.com/eclipse-iofog/ai-inference-onnx/internal/config"
)

const (
	staticToken    = "0123456789abcdef0123456789abcdef"
	badStaticToken = "fedcba9876543210fedcba9876543210"
	tokenIssuer    = "https://edgelet.default.svc.bridge.local"
	claimSentinel  = "sentinel-claim-value"
	inferenceGroup = "inference.iofog.org/v2"
)

func TestListenAddr(t *testing.T) {
	t.Parallel()
	got := ListenAddr(8000)
	if got != ":8000" {
		t.Fatalf("ListenAddr = %q, want :8000", got)
	}
	host, port, err := net.SplitHostPort(got)
	if err != nil {
		t.Fatalf("SplitHostPort: %v", err)
	}
	if host != "" || port != "8000" {
		t.Fatalf("host %q port %q", host, port)
	}
	if strings.Contains(got, "127.0.0.1") || strings.Contains(got, "localhost") || strings.Contains(got, "::1") {
		t.Fatalf("ListenAddr = %q", got)
	}
}

func TestHealthLiveAndReady(t *testing.T) {
	t.Parallel()
	sets := [][]State{
		nil,
		{StateLoading},
		{StateUnavailable},
		{StateLoading, StateUnavailable},
		{StateReady},
		{StateReady, StateUnavailable},
		{StateReady, StateLoading},
		{StateLoading, StateUnavailable, StateReady},
	}
	checker := mustChecker(t, config.Config{AuthMode: config.AuthDisabled}, "")
	for _, states := range sets {
		states := states
		wantReady := http.StatusBadRequest
		if AnyReady(states...) {
			wantReady = http.StatusOK
		}
		t.Run(readyLabel(states), func(t *testing.T) {
			t.Parallel()
			current := append([]State(nil), states...)
			srv := New(checker, func() bool { return AnyReady(current...) })
			assertEmpty(t, call(t, srv, http.MethodGet, "/v2/health/ready", ""), wantReady)
			assertEmpty(t, call(t, srv, http.MethodGet, "/v2/health/live", ""), http.StatusOK)
		})
	}
}

func TestHealthIgnoresBadBearer(t *testing.T) {
	t.Parallel()
	bad := "Bearer " + badStaticToken
	modes := []struct {
		name    string
		checker *auth.Checker
	}{
		{
			name:    "disabled",
			checker: mustChecker(t, config.Config{AuthMode: config.AuthDisabled, AuthStaticToken: staticToken}, ""),
		},
		{
			name:    "static",
			checker: mustChecker(t, config.Config{AuthMode: config.AuthStatic, AuthStaticToken: staticToken}, ""),
		},
		{
			name:    "edgelet",
			checker: edgeletChecker(t, nil).checker,
		},
	}
	for _, mode := range modes {
		t.Run(mode.name, func(t *testing.T) {
			t.Parallel()
			notReady := New(mode.checker, func() bool { return AnyReady(StateLoading, StateUnavailable) })
			assertEmpty(t, call(t, notReady, http.MethodGet, "/v2/health/live", bad), http.StatusOK)
			assertEmpty(t, call(t, notReady, http.MethodGet, "/v2/health/ready", bad), http.StatusBadRequest)

			ready := New(mode.checker, func() bool { return AnyReady(StateReady, StateUnavailable) })
			assertEmpty(t, call(t, ready, http.MethodGet, "/v2/health/live", bad), http.StatusOK)
			assertEmpty(t, call(t, ready, http.MethodGet, "/v2/health/ready", bad), http.StatusOK)
		})
	}
}

func TestHealthWrongMethod(t *testing.T) {
	t.Parallel()
	checker := mustChecker(t, config.Config{AuthMode: config.AuthStatic, AuthStaticToken: staticToken}, "")
	srv := New(checker, func() bool { return AnyReady(StateReady) })
	methods := []string{http.MethodPost, http.MethodPut, http.MethodHead, http.MethodDelete}
	paths := []string{"/v2/health/live", "/v2/health/ready"}
	for _, method := range methods {
		for _, path := range paths {
			t.Run(method+" "+path, func(t *testing.T) {
				t.Parallel()
				rec := call(t, srv, method, path, "Bearer "+badStaticToken)
				assertError(t, rec, http.StatusMethodNotAllowed, badStaticToken, staticToken)
				if allow := rec.Header().Get("Allow"); allow != http.MethodGet {
					t.Fatalf("Allow = %q", allow)
				}
			})
		}
	}
	notReady := New(checker, func() bool { return AnyReady(StateLoading, StateUnavailable) })
	rec := call(t, notReady, http.MethodPost, "/v2/health/ready", "")
	assertError(t, rec, http.StatusMethodNotAllowed)
}

func TestProtectedPaths(t *testing.T) {
	t.Parallel()
	staticChecker := mustChecker(t, config.Config{AuthMode: config.AuthStatic, AuthStaticToken: staticToken}, "")
	staticSrv := New(staticChecker, func() bool { return true })

	t.Run("static missing token", func(t *testing.T) {
		t.Parallel()
		rec := call(t, staticSrv, http.MethodGet, "/v2/models", "")
		assertError(t, rec, http.StatusUnauthorized, staticToken)
	})
	t.Run("static bad token", func(t *testing.T) {
		t.Parallel()
		rec := call(t, staticSrv, http.MethodPost, "/v2/models/demo/infer", "Bearer "+badStaticToken)
		assertError(t, rec, http.StatusUnauthorized, staticToken, badStaticToken)
	})
	t.Run("static allowed without handler", func(t *testing.T) {
		t.Parallel()
		paths := []struct {
			method string
			path   string
		}{
			{method: http.MethodGet, path: "/v2"},
			{method: http.MethodGet, path: "/v2/models"},
			{method: http.MethodGet, path: "/v2/models/demo/ready"},
			{method: http.MethodGet, path: "/v2/models/demo/versions/1/ready"},
			{method: http.MethodPost, path: "/v2/models/demo/infer"},
			{method: http.MethodPost, path: "/v2/repository/index"},
		}
		for _, path := range paths {
			rec := call(t, staticSrv, path.method, path.path, "Bearer "+staticToken)
			assertError(t, rec, http.StatusNotFound, staticToken)
			if rec.Body.Len() == 0 {
				t.Fatal("missing handler returned an empty body")
			}
		}
	})

	t.Run("edgelet missing token", func(t *testing.T) {
		t.Parallel()
		edgelet := edgeletChecker(t, accessRule([]string{"repository/index"}, []string{"get"}))
		srv := New(edgelet.checker, func() bool { return AnyReady(StateReady) })
		rec := call(t, srv, http.MethodGet, "/v2/models", "")
		assertError(t, rec, http.StatusUnauthorized, tokenIssuer, claimSentinel)
	})
	t.Run("edgelet bad token", func(t *testing.T) {
		t.Parallel()
		edgelet := edgeletChecker(t, accessRule([]string{"repository/index"}, []string{"get"}))
		srv := New(edgelet.checker, func() bool { return AnyReady(StateReady) })
		rec := call(t, srv, http.MethodGet, "/v2", "Bearer "+edgelet.wrong)
		assertError(t, rec, http.StatusUnauthorized, edgelet.wrong, tokenIssuer, claimSentinel)
	})
	t.Run("edgelet rule absent", func(t *testing.T) {
		t.Parallel()
		edgelet := edgeletChecker(t, accessRule([]string{"repository/index"}, []string{"get"}))
		srv := New(edgelet.checker, func() bool { return AnyReady(StateReady) })
		rec := call(t, srv, http.MethodGet, "/v2/models", "Bearer "+edgelet.token)
		assertError(t, rec, http.StatusForbidden, edgelet.token, tokenIssuer, claimSentinel)
	})
	t.Run("edgelet allowed without handler", func(t *testing.T) {
		t.Parallel()
		allowed := edgeletChecker(t, accessRule([]string{"models", "models/demo"}, []string{"get"}))
		srv := New(allowed.checker, func() bool { return true })
		rec := call(t, srv, http.MethodGet, "/v2/models", "Bearer "+allowed.token)
		assertError(t, rec, http.StatusNotFound, allowed.token, tokenIssuer, claimSentinel)
		readyRec := call(t, srv, http.MethodGet, "/v2/models/demo/ready", "Bearer "+allowed.token)
		assertError(t, readyRec, http.StatusNotFound, allowed.token, tokenIssuer, claimSentinel)
	})

	disabled := mustChecker(t, config.Config{AuthMode: config.AuthDisabled}, "")
	disabledSrv := New(disabled, func() bool { return false })
	t.Run("disabled allowed without handler", func(t *testing.T) {
		t.Parallel()
		rec := call(t, disabledSrv, http.MethodGet, "/v2/models/demo/ready", "Bearer "+badStaticToken)
		assertError(t, rec, http.StatusNotFound, badStaticToken)
	})
}

func TestMetricsExempt(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name          string
		checker       *auth.Checker
		authorization string
		secrets       []string
	}{
		{
			name:    "disabled",
			checker: mustChecker(t, config.Config{AuthMode: config.AuthDisabled}, ""),
		},
		{
			name:          "static",
			checker:       mustChecker(t, config.Config{AuthMode: config.AuthStatic, AuthStaticToken: staticToken}, ""),
			authorization: "",
			secrets:       []string{staticToken},
		},
		{
			name:          "static bad bearer",
			checker:       mustChecker(t, config.Config{AuthMode: config.AuthStatic, AuthStaticToken: staticToken}, ""),
			authorization: "Bearer " + badStaticToken,
			secrets:       []string{staticToken, badStaticToken},
		},
		{
			name:          "edgelet",
			checker:       edgeletChecker(t, nil).checker,
			authorization: "",
			secrets:       []string{tokenIssuer, claimSentinel},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv := New(tc.checker, func() bool { return false })
			rec := call(t, srv, http.MethodGet, "/metrics", tc.authorization)
			if rec.Code == http.StatusUnauthorized {
				t.Fatal("metrics rejected a caller")
			}
			assertError(t, rec, http.StatusNotFound, tc.secrets...)
		})
	}
}

func readyLabel(states []State) string {
	if len(states) == 0 {
		return "empty"
	}
	parts := make([]string, len(states))
	for i, state := range states {
		parts[i] = string(state)
	}
	return strings.Join(parts, "+")
}

func call(t *testing.T, handler http.Handler, method, urlPath, authorization string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, urlPath, nil)
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func assertEmpty(t *testing.T, rec *httptest.ResponseRecorder, status int) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status %d, want %d, body %q", rec.Code, status, rec.Body.String())
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("body %q", rec.Body.String())
	}
}

func assertError(t *testing.T, rec *httptest.ResponseRecorder, status int, secrets ...string) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status %d, want %d, body %q", rec.Code, status, rec.Body.String())
	}
	var fields map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &fields); err != nil {
		t.Fatalf("body %q: %v", rec.Body.String(), err)
	}
	if len(fields) != 1 {
		t.Fatalf("body %q", rec.Body.String())
	}
	message, ok := fields["error"].(string)
	if !ok || message == "" {
		t.Fatalf("body %q", rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q", got)
	}
	text := rec.Body.String()
	for key, values := range rec.Header() {
		text += "\n" + key
		for _, value := range values {
			text += "\n" + value
		}
	}
	for _, secret := range secrets {
		if secret != "" && strings.Contains(text, secret) {
			t.Fatal("response contains credential material")
		}
	}
}

func mustChecker(t *testing.T, cfg config.Config, jwkPath string) *auth.Checker {
	t.Helper()
	checker, err := auth.New(cfg, jwkPath)
	if err != nil {
		t.Fatalf("checker: %v", err)
	}
	return checker
}

type signedEdgelet struct {
	checker *auth.Checker
	token   string
	wrong   string
}

func edgeletChecker(t *testing.T, rule map[string]any) signedEdgelet {
	t.Helper()
	public, private := signingKey(t)
	path := filepath.Join(t.TempDir(), "edgelet.jwk")
	writePublicJWK(t, path, public)
	_, other := signingKey(t)
	now := time.Now()
	var rules []map[string]any
	if rule != nil {
		rules = []map[string]any{rule}
	}
	return signedEdgelet{
		checker: mustChecker(t, config.Config{AuthMode: config.AuthEdgelet}, path),
		token:   signClaims(t, private, edgeletClaims(now, rules...)),
		wrong:   signClaims(t, other, edgeletClaims(now, rules...)),
	}
}

func accessRule(resources, verbs []string) map[string]any {
	return map[string]any{
		"resources": resources,
		"verbs":     verbs,
	}
}

func edgeletClaims(now time.Time, rules ...map[string]any) jwt.MapClaims {
	listed := make([]any, len(rules))
	for i := range rules {
		listed[i] = rules[i]
	}
	return jwt.MapClaims{
		"iss":      tokenIssuer,
		"sub":      claimSentinel,
		"aud":      []string{tokenIssuer},
		"iat":      now.Add(-time.Minute).Unix(),
		"nbf":      now.Add(-time.Minute).Unix(),
		"exp":      now.Add(time.Hour).Unix(),
		"tokenUse": "serviceaccount",
		"edgelet.iofog.org": map[string]any{
			"rbac": map[string]any{
				"version": "v1",
				"rulesByGroup": map[string]any{
					inferenceGroup: listed,
				},
			},
		},
	}
}

func signingKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return public, private
}

func writePublicJWK(t *testing.T, path string, public ed25519.PublicKey) {
	t.Helper()
	doc := map[string]string{
		"kty": "OKP",
		"crv": "Ed25519",
		"alg": "EdDSA",
		"x":   base64.RawURLEncoding.EncodeToString(public),
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("encode key: %v", err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
}

func TestAuthDenialIsLogged(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	checker := mustChecker(t, config.Config{AuthMode: config.AuthStatic, AuthStaticToken: staticToken}, "")
	srv := New(checker, func() bool { return true })
	srv.RegisterInfer(InferConfig{Logger: logger, MaxPayloadBytes: 1 << 20})

	assertEmpty(t, call(t, srv, http.MethodGet, "/v2/health/live", "Bearer "+badStaticToken), http.StatusOK)
	rec := call(t, srv, http.MethodPost, "/v2/models/demo/infer", "Bearer "+badStaticToken)
	assertError(t, rec, http.StatusUnauthorized, staticToken, badStaticToken)

	out := buf.String()
	if strings.Contains(out, staticToken) || strings.Contains(out, badStaticToken) || strings.Contains(out, "Bearer") {
		t.Fatalf("log contains a credential:\n%s", out)
	}
	if strings.Count(out, `"msg":"auth"`) != 1 || !strings.Contains(out, `"decision":"unauthenticated"`) || !strings.Contains(out, `"model":"demo"`) || !strings.Contains(out, `"transport":"http"`) {
		t.Fatalf("auth log %s", out)
	}
}

func signClaims(t *testing.T, key ed25519.PrivateKey, claims jwt.MapClaims) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	signed, err := token.SignedString(key)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return signed
}
