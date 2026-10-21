package auth

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"sync"
	"testing"

	"github.com/eclipse-iofog/ai-inference-onnx/internal/config"
)

// TestPublicJWKPath checks the in-container signing key path.
func TestPublicJWKPath(t *testing.T) {
	const production = "/var/run/secrets/edgelet.iofog.org/serviceaccount/edgelet.jwk"
	if PublicJWKPath != production {
		t.Fatalf("path %s", PublicJWKPath)
	}
	if resolveKeyPath("") != PublicJWKPath || resolveKeyPath("   ") != PublicJWKPath {
		t.Fatal("empty path")
	}
	if resolveKeyPath("/tmp/custom.jwk") != "/tmp/custom.jwk" {
		t.Fatal("custom path")
	}
}

// TestKeyStartupFails checks a missing, unreadable, or unusable key file.
func TestKeyStartupFails(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		_, err := New(config.Config{AuthMode: config.AuthEdgelet}, keyFilePath(t))
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("missing key: %v", err)
		}
	})

	t.Run("directory", func(t *testing.T) {
		_, err := New(config.Config{AuthMode: config.AuthEdgelet}, t.TempDir())
		if err == nil {
			t.Fatal("expected startup error")
		}
	})

	t.Run("unreadable", func(t *testing.T) {
		public, _ := newSigningKey(t)
		path := keyFilePath(t)
		writePublicJWK(t, path, public)
		if err := os.Chmod(path, 0); err != nil {
			t.Fatalf("chmod: %v", err)
		}
		t.Cleanup(func() {
			_ = os.Chmod(path, 0o644)
		})
		_, err := New(config.Config{AuthMode: config.AuthEdgelet}, path)
		if err == nil {
			t.Fatal("expected startup error")
		}
	})

	public, _ := newSigningKey(t)
	validX := base64.RawURLEncoding.EncodeToString(public)
	const marker = "private-key-material-marker"
	short := make([]byte, ed25519.PublicKeySize-1)
	if _, err := rand.Read(short); err != nil {
		t.Fatalf("short key: %v", err)
	}
	cases := []struct {
		name string
		body []byte
	}{
		{name: "not json", body: []byte("not-a-key " + marker)},
		{name: "private field", body: mustJSON(t, map[string]any{
			"kty": "OKP",
			"crv": "Ed25519",
			"alg": "EdDSA",
			"x":   validX,
			"d":   marker,
		})},
		{name: "wrong length", body: mustJSON(t, map[string]any{
			"kty": "OKP",
			"crv": "Ed25519",
			"alg": "EdDSA",
			"x":   base64.RawURLEncoding.EncodeToString(short),
		})},
		{name: "wrong curve", body: mustJSON(t, map[string]any{
			"kty": "OKP",
			"crv": "X25519",
			"alg": "EdDSA",
			"x":   validX,
		})},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := keyFilePath(t)
			if err := os.WriteFile(path, tc.body, 0o644); err != nil {
				t.Fatalf("write: %v", err)
			}
			_, err := New(config.Config{AuthMode: config.AuthEdgelet}, path)
			if err == nil {
				t.Fatal("expected startup error")
			}
			assertNoSecret(t, err, marker, validX)
		})
	}
}

// TestKeyReplacement swaps the signing key, then an invalid file makes the next call unauthenticated.
func TestKeyReplacement(t *testing.T) {
	publicA, privateA := newSigningKey(t)
	publicB, privateB := newSigningKey(t)
	path := keyFilePath(t)
	writePublicJWK(t, path, publicA)
	checker := newEdgelet(t, path)
	now := checkTime()
	tokenA := signClaims(t, privateA, claimsFor(now, apiGroup, accessRule([]string{"server"}, []string{"get"})))
	tokenB := signClaims(t, privateB, claimsFor(now, apiGroup, accessRule([]string{"server"}, []string{"get"})))

	decision, err := checker.AuthorizeHTTP(http.MethodGet, "/v2", bearer(tokenA), now)
	assertDecision(t, decision, err, Allow)

	writePublicJWK(t, path, publicB)
	decision, err = checker.AuthorizeHTTP(http.MethodGet, "/v2", bearer(tokenA), now)
	assertDecision(t, decision, err, Unauthenticated)
	assertNoSecret(t, err, tokenA)
	decision, err = checker.AuthorizeHTTP(http.MethodGet, "/v2", bearer(tokenB), now)
	assertDecision(t, decision, err, Allow)

	if writeErr := os.WriteFile(path, []byte(`{"d":"private-key-material-marker"}`), 0o644); writeErr != nil {
		t.Fatalf("write invalid key: %v", writeErr)
	}
	decision, err = checker.AuthorizeHTTP(http.MethodGet, "/v2", bearer(tokenB), now)
	assertDecision(t, decision, err, Unauthenticated)
	assertNoSecret(t, err, tokenB, "private-key-material-marker")

	if removeErr := os.Remove(path); removeErr != nil {
		t.Fatalf("remove key: %v", removeErr)
	}
	decision, err = checker.AuthorizeHTTP(http.MethodGet, "/v2", bearer(tokenB), now)
	assertDecision(t, decision, err, Unauthenticated)

	writePublicJWK(t, path, publicB)
	decision, err = checker.AuthorizeHTTP(http.MethodGet, "/v2", bearer(tokenB), now)
	assertDecision(t, decision, err, Allow)
	decision, err = checker.AuthorizeHTTP(http.MethodGet, "/v2", bearer(tokenA), now)
	assertDecision(t, decision, err, Unauthenticated)
}

// TestKeyUseIsConcurrent checks that a replacement does not race with a call.
func TestKeyUseIsConcurrent(t *testing.T) {
	public, private := newSigningKey(t)
	path := keyFilePath(t)
	writePublicJWK(t, path, public)
	checker := newEdgelet(t, path)
	now := checkTime()
	token := signClaims(t, private, claimsFor(now, apiGroup, accessRule([]string{"server"}, []string{"get"})))

	var wg sync.WaitGroup
	started := make(chan struct{})
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-started
			for range 20 {
				decision, err := checker.AuthorizeHTTP(http.MethodGet, "/v2", bearer(token), now)
				switch decision {
				case Allow:
					if err != nil {
						t.Error("allow error")
					}
				case Unauthenticated:
					if !errors.Is(err, ErrUnauthenticated) {
						t.Error("unauthenticated error")
					}
				default:
					t.Errorf("decision %s", decision)
				}
			}
		}()
	}
	close(started)
	if err := os.WriteFile(path, []byte("{"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	writePublicJWK(t, path, public)
	wg.Wait()
}

func mustJSON(t *testing.T, doc map[string]any) []byte {
	t.Helper()
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	return raw
}
