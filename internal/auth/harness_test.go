package auth

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/eclipse-iofog/ai-inference-onnx/internal/config"
)

func checkTime() time.Time {
	return time.Unix(1_800_000_000, 0)
}

func assertDecision(t *testing.T, got Decision, err error, want Decision) {
	t.Helper()
	if got != want {
		t.Fatalf("decision %s, want %s", got, want)
	}
	switch want {
	case Allow:
		if err != nil {
			t.Fatalf("allow returned %v", err)
		}
	case Unauthenticated:
		if !errors.Is(err, ErrUnauthenticated) {
			t.Fatalf("error %v", err)
		}
	case Forbidden:
		if !errors.Is(err, ErrForbidden) {
			t.Fatalf("error %v", err)
		}
	default:
		t.Fatalf("unknown decision %s", want)
	}
}

func assertNoSecret(t *testing.T, err error, secrets ...string) {
	t.Helper()
	if err == nil {
		return
	}
	text := err.Error()
	for _, secret := range secrets {
		if secret != "" && strings.Contains(text, secret) {
			t.Fatal("error contains credential material")
		}
	}
}

func bearer(token string) string {
	return "Bearer " + token
}

func newSigningKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return public, private
}

func writePublicJWK(t *testing.T, path string, public ed25519.PublicKey) {
	t.Helper()
	writeJWKMap(t, path, map[string]any{
		"kty": "OKP",
		"crv": "Ed25519",
		"alg": "EdDSA",
		"x":   base64.RawURLEncoding.EncodeToString(public),
	})
}

func writeJWKMap(t *testing.T, path string, doc map[string]any) {
	t.Helper()
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("encode key: %v", err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("write key: %v", err)
	}
}

func newEdgelet(t *testing.T, path string) *Checker {
	t.Helper()
	checker, err := New(config.Config{AuthMode: config.AuthEdgelet}, path)
	if err != nil {
		t.Fatalf("checker: %v", err)
	}
	return checker
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

func accessRule(resources, verbs []string) map[string]any {
	return map[string]any{
		"resources": resources,
		"verbs":     verbs,
	}
}

func claimsFor(now time.Time, group string, rules ...map[string]any) jwt.MapClaims {
	if group == "" {
		group = apiGroup
	}
	listed := make([]any, len(rules))
	for i := range rules {
		listed[i] = rules[i]
	}
	return jwt.MapClaims{
		"iss":      edgeletIssuer,
		"aud":      []string{edgeletIssuer},
		"iat":      now.Add(-time.Minute).Unix(),
		"nbf":      now.Add(-time.Minute).Unix(),
		"exp":      now.Add(time.Hour).Unix(),
		"tokenUse": tokenUseServiceAccount,
		"edgelet.iofog.org": map[string]any{
			"rbac": map[string]any{
				"version": "v1",
				"rulesByGroup": map[string]any{
					group: listed,
				},
			},
		},
	}
}

func keyFilePath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "edgelet.jwk")
}
