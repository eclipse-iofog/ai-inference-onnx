package grpcapi

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/eclipse-iofog/ai-inference-onnx/api/openinference/v2"
	"github.com/eclipse-iofog/ai-inference-onnx/internal/auth"
	"github.com/eclipse-iofog/ai-inference-onnx/internal/config"
	"github.com/eclipse-iofog/ai-inference-onnx/internal/httpapi"
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
	got := ListenAddr(8001)
	if got != ":8001" {
		t.Fatalf("ListenAddr = %q, want :8001", got)
	}
	host, port, err := net.SplitHostPort(got)
	if err != nil {
		t.Fatalf("SplitHostPort: %v", err)
	}
	if host != "" || port != "8001" {
		t.Fatalf("host %q port %q", host, port)
	}
	if strings.Contains(got, "127.0.0.1") || strings.Contains(got, "localhost") || strings.Contains(got, "::1") {
		t.Fatalf("ListenAddr = %q", got)
	}
}

func TestServerReadyFollowsFunc(t *testing.T) {
	t.Parallel()
	checker := mustChecker(t, config.Config{AuthMode: config.AuthDisabled}, "")
	cases := []struct {
		name  string
		ready func() bool
		want  bool
	}{
		{name: "false", ready: func() bool { return false }, want: false},
		{name: "true", ready: func() bool { return true }, want: true},
		{name: "nil", ready: nil, want: false},
		{name: "empty", ready: func() bool { return httpapi.AnyReady() }, want: false},
		{name: "loading", ready: func() bool { return httpapi.AnyReady(httpapi.StateLoading) }, want: false},
		{name: "unavailable", ready: func() bool { return httpapi.AnyReady(httpapi.StateUnavailable) }, want: false},
		{
			name:  "loading and unavailable",
			ready: func() bool { return httpapi.AnyReady(httpapi.StateLoading, httpapi.StateUnavailable) },
			want:  false,
		},
		{name: "ready", ready: func() bool { return httpapi.AnyReady(httpapi.StateReady) }, want: true},
		{
			name:  "ready and unavailable",
			ready: func() bool { return httpapi.AnyReady(httpapi.StateReady, httpapi.StateUnavailable) },
			want:  true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			client := start(t, checker, tc.ready)
			ctx := callCtx(t)
			readyResp, err := client.ServerReady(ctx, &openinference.ServerReadyRequest{})
			if status.Code(err) != codes.OK {
				t.Fatalf("ready code %s: %v", status.Code(err), err)
			}
			if readyResp.GetReady() != tc.want {
				t.Fatalf("ready %v, want %v", readyResp.GetReady(), tc.want)
			}
			liveResp, err := client.ServerLive(ctx, &openinference.ServerLiveRequest{})
			if status.Code(err) != codes.OK {
				t.Fatalf("live code %s: %v", status.Code(err), err)
			}
			if !liveResp.GetLive() {
				t.Fatal("live is false")
			}
		})
	}
}

func TestHealthIgnoresMissingMetadata(t *testing.T) {
	t.Parallel()
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
			notReady := start(t, mode.checker, func() bool {
				return httpapi.AnyReady(httpapi.StateLoading, httpapi.StateUnavailable)
			})
			assertReady(t, notReady, "", false)
			assertLive(t, notReady, "")

			ready := start(t, mode.checker, func() bool {
				return httpapi.AnyReady(httpapi.StateReady, httpapi.StateUnavailable)
			})
			assertReady(t, ready, "", true)
			assertLive(t, ready, "")

			assertReady(t, notReady, "Bearer "+badStaticToken, false)
			assertLive(t, ready, "Bearer "+badStaticToken)
		})
	}
}

func TestModelInferAuth(t *testing.T) {
	t.Parallel()
	req := &openinference.ModelInferRequest{ModelName: "demo", ModelVersion: "1"}

	t.Run("static missing token", func(t *testing.T) {
		t.Parallel()
		checker := mustChecker(t, config.Config{AuthMode: config.AuthStatic, AuthStaticToken: staticToken}, "")
		client := start(t, checker, func() bool { return true })
		_, err := client.ModelInfer(callCtx(t), req)
		assertStatus(t, err, codes.Unauthenticated, "unauthenticated", staticToken)
	})

	t.Run("static wrong token", func(t *testing.T) {
		t.Parallel()
		checker := mustChecker(t, config.Config{AuthMode: config.AuthStatic, AuthStaticToken: staticToken}, "")
		client := start(t, checker, func() bool { return true })
		_, err := client.ModelInfer(withAuth(callCtx(t), "Bearer "+badStaticToken), req)
		assertStatus(t, err, codes.Unauthenticated, "unauthenticated", staticToken, badStaticToken)
	})

	t.Run("static matching token", func(t *testing.T) {
		t.Parallel()
		checker := mustChecker(t, config.Config{AuthMode: config.AuthStatic, AuthStaticToken: staticToken}, "")
		client := start(t, checker, func() bool { return true })
		_, err := client.ModelInfer(withAuth(callCtx(t), "Bearer "+staticToken), req)
		assertStatus(t, err, codes.NotFound, `model "demo" was not found`, staticToken)
	})

	t.Run("edgelet missing token", func(t *testing.T) {
		t.Parallel()
		edgelet := edgeletChecker(t, accessRule([]string{"models/demo"}, []string{"get"}))
		client := start(t, edgelet.checker, func() bool { return true })
		_, err := client.ModelInfer(callCtx(t), req)
		assertStatus(t, err, codes.Unauthenticated, "unauthenticated", edgelet.token, tokenIssuer, claimSentinel)
	})

	t.Run("edgelet rule does not allow infer", func(t *testing.T) {
		t.Parallel()
		edgelet := edgeletChecker(t, accessRule([]string{"models/demo"}, []string{"get"}))
		client := start(t, edgelet.checker, func() bool { return true })
		_, err := client.ModelInfer(withAuth(callCtx(t), "Bearer "+edgelet.token), req)
		assertStatus(t, err, codes.PermissionDenied, "forbidden", edgelet.token, tokenIssuer, claimSentinel)
	})
}

func start(t *testing.T, checker *auth.Checker, ready func() bool) openinference.GRPCInferenceServiceClient {
	t.Helper()
	return dialServer(t, New(checker, ready))
}

func dialServer(t *testing.T, srv *Server) openinference.GRPCInferenceServiceClient {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	go func() {
		_ = srv.Serve(lis)
	}()
	t.Cleanup(srv.Stop)

	// Dial with insecure credentials. The server does not terminate TLS.
	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Errorf("close client: %v", err)
		}
	})
	return openinference.NewGRPCInferenceServiceClient(conn)
}

func callCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func withAuth(ctx context.Context, authorization string) context.Context {
	return metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", authorization))
}

func assertLive(t *testing.T, client openinference.GRPCInferenceServiceClient, authorization string) {
	t.Helper()
	resp, err := client.ServerLive(callContext(t, authorization), &openinference.ServerLiveRequest{})
	if status.Code(err) != codes.OK {
		t.Fatalf("live code %s: %v", status.Code(err), err)
	}
	if !resp.GetLive() {
		t.Fatal("live is false")
	}
}

func assertReady(t *testing.T, client openinference.GRPCInferenceServiceClient, authorization string, want bool) {
	t.Helper()
	resp, err := client.ServerReady(callContext(t, authorization), &openinference.ServerReadyRequest{})
	if status.Code(err) != codes.OK {
		t.Fatalf("ready code %s: %v", status.Code(err), err)
	}
	if resp.GetReady() != want {
		t.Fatalf("ready %v, want %v", resp.GetReady(), want)
	}
}

func callContext(t *testing.T, authorization string) context.Context {
	t.Helper()
	ctx := callCtx(t)
	if authorization == "" {
		return ctx
	}
	return withAuth(ctx, authorization)
}

func assertStatus(t *testing.T, err error, code codes.Code, message string, secrets ...string) {
	t.Helper()
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("error %v", err)
	}
	if st.Code() != code {
		t.Fatalf("code %s, want %s", st.Code(), code)
	}
	if message != "" && st.Message() != message {
		t.Fatalf("message %q", st.Message())
	}
	text := st.Message() + "\n" + err.Error()
	for _, secret := range secrets {
		if secret != "" && strings.Contains(text, secret) {
			t.Fatal("status contains credential material")
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
}

func edgeletChecker(t *testing.T, rule map[string]any) signedEdgelet {
	t.Helper()
	public, private := signingKey(t)
	path := filepath.Join(t.TempDir(), "edgelet.jwk")
	writePublicJWK(t, path, public)
	now := time.Now()
	var rules []map[string]any
	if rule != nil {
		rules = []map[string]any{rule}
	}
	return signedEdgelet{
		checker: mustChecker(t, config.Config{AuthMode: config.AuthEdgelet}, path),
		token:   signClaims(t, private, edgeletClaims(now, rules...)),
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
	client := dialServer(t, srv)

	if _, err := client.ServerLive(callCtx(t), &openinference.ServerLiveRequest{}); err != nil {
		t.Fatal(err)
	}
	_, err := client.ModelInfer(withAuth(callCtx(t), "Bearer "+badStaticToken), &openinference.ModelInferRequest{ModelName: "demo"})
	assertStatus(t, err, codes.Unauthenticated, "unauthenticated", staticToken, badStaticToken)

	out := buf.String()
	if strings.Contains(out, staticToken) || strings.Contains(out, badStaticToken) || strings.Contains(out, "Bearer") {
		t.Fatalf("log contains a credential:\n%s", out)
	}
	if strings.Count(out, `"msg":"auth"`) != 1 || !strings.Contains(out, `"decision":"unauthenticated"`) || !strings.Contains(out, `"model":"demo"`) || !strings.Contains(out, `"transport":"grpc"`) {
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
