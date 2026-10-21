package auth

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
)

// keyFile keeps the public signing key in memory and replaces it when the file changes.
// After a key has loaded, a missing, unreadable, or invalid replacement clears the key.
type keyFile struct {
	mu   sync.Mutex
	path string
	raw  []byte
	key  ed25519.PublicKey
}

func resolveKeyPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return PublicJWKPath
	}
	return path
}

func openKeyFile(path string) (*keyFile, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- signing key path is configured, not taken from a request
	if err != nil {
		return nil, fmt.Errorf("read signing key: %w", err)
	}
	parsed, err := parsePublicKey(data)
	if err != nil {
		return nil, err
	}
	return &keyFile{path: path, raw: data, key: parsed}, nil
}

func (k *keyFile) current() (ed25519.PublicKey, bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	data, err := os.ReadFile(k.path) // #nosec G304 -- signing key path is configured, not taken from a request
	if err != nil {
		k.raw = nil
		k.key = nil
		return nil, false
	}
	if bytes.Equal(data, k.raw) {
		if len(k.key) != ed25519.PublicKeySize {
			return nil, false
		}
		return copyKey(k.key), true
	}
	parsed, err := parsePublicKey(data)
	k.raw = append([]byte(nil), data...)
	if err != nil {
		k.key = nil
		return nil, false
	}
	k.key = parsed
	return copyKey(k.key), true
}

func parsePublicKey(data []byte) (ed25519.PublicKey, error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(data, &doc); err != nil || doc == nil {
		return nil, errors.New("signing key is not a usable public key")
	}
	if _, private := doc["d"]; private {
		return nil, errors.New("signing key is not a usable public key")
	}
	kty, ok := jsonString(doc, "kty")
	if !ok || kty != "OKP" {
		return nil, errors.New("signing key is not a usable public key")
	}
	crv, ok := jsonString(doc, "crv")
	if !ok || crv != "Ed25519" {
		return nil, errors.New("signing key is not a usable public key")
	}
	alg, ok := jsonString(doc, "alg")
	if !ok || alg != "EdDSA" {
		return nil, errors.New("signing key is not a usable public key")
	}
	x, ok := jsonString(doc, "x")
	if !ok {
		return nil, errors.New("signing key is not a usable public key")
	}
	raw, err := base64.RawURLEncoding.DecodeString(x)
	if err != nil || len(raw) != ed25519.PublicKeySize {
		return nil, errors.New("signing key is not a usable public key")
	}
	return ed25519.PublicKey(raw), nil
}

func jsonString(doc map[string]json.RawMessage, key string) (string, bool) {
	raw, ok := doc[key]
	if !ok {
		return "", false
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", false
	}
	return value, true
}

func copyKey(key ed25519.PublicKey) ed25519.PublicKey {
	out := make(ed25519.PublicKey, len(key))
	copy(out, key)
	return out
}
