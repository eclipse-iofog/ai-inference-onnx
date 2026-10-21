package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	certKindServer = "server"
	certKindClient = "client"
	certKindCA     = "ca"
	notCertificate = "not-a-certificate-9f3c1a7e5b24"
	notAuthority   = "not-a-client-authority-6b0d2e8a41c7"
)

func TestTLSDisabledStaysPlain(t *testing.T) {
	t.Parallel()
	port := freePort(t)
	missing := filepath.Join(t.TempDir(), "missing")
	proc := startProcess(t, processEnv(t, port, map[string]string{
		"TLS_ENABLED": "false",
		"TLS_CERT":    missing,
		"TLS_KEY":     missing + ".key",
		"TLS_CA":      missing + ".ca",
	}))
	t.Cleanup(proc.kill)
	waitHealth(t, port)
	if err := tlsHandshake(port, nil, nil); err == nil {
		t.Fatal("plain HTTP presented a certificate")
	}
}

func TestTLSServesTheCertificate(t *testing.T) {
	t.Parallel()
	port := freePort(t)
	serverCert := mintCert(t, nil, certKindServer)
	certPath, keyPath := writePair(t, serverCert)
	proc := startProcess(t, processEnv(t, port, map[string]string{
		"TLS_ENABLED": "true",
		"TLS_CERT":    certPath,
		"TLS_KEY":     keyPath,
	}))
	t.Cleanup(proc.kill)
	peer := waitHTTPS(t, port, serverCert, nil)
	if !bytes.Equal(peer, serverCert.cert.Raw) {
		t.Fatal("peer certificate mismatch")
	}
	if status, body, err := getErr(port, "/health"); err == nil && status == http.StatusOK && len(body) == 0 {
		t.Fatalf("plain HTTP served health: %d %s", status, body)
	}
	out := proc.stdout.String() + proc.stderr.String()
	assertNoKey(t, out, serverCert)
	if strings.TrimSpace(proc.stderr.String()) != "" {
		t.Fatalf("stderr %s", proc.stderr.String())
	}
}

func TestTLSRequiresClientCertificate(t *testing.T) {
	t.Parallel()
	port := freePort(t)
	serverCert := mintCert(t, nil, certKindServer)
	ca := mintCert(t, nil, certKindCA)
	otherCA := mintCert(t, nil, certKindCA)
	client := mintCert(t, &ca, certKindClient)
	other := mintCert(t, &otherCA, certKindClient)
	certPath, keyPath := writePair(t, serverCert)
	caPath := writePEM(t, "ca.pem", ca.certPEM)
	proc := startProcess(t, processEnv(t, port, map[string]string{
		"TLS_ENABLED": "true",
		"TLS_CERT":    certPath,
		"TLS_KEY":     keyPath,
		"TLS_CA":      caPath,
	}))
	t.Cleanup(proc.kill)
	peer := waitHTTPS(t, port, serverCert, &client)
	if !bytes.Equal(peer, serverCert.cert.Raw) {
		t.Fatal("peer certificate mismatch")
	}
	if _, status, body, err := httpsGet(port, serverCert.cert, nil, "/health"); err == nil && status == http.StatusOK {
		t.Fatalf("accepted a client with no certificate: %d %s", status, body)
	}
	if _, status, body, err := httpsGet(port, serverCert.cert, &other, "/health"); err == nil && status == http.StatusOK {
		t.Fatalf("accepted a certificate from another authority: %d %s", status, body)
	}
	out := proc.stdout.String() + proc.stderr.String()
	assertNoKey(t, out, serverCert, ca, otherCA, client, other)
}

func TestListenerRequiresClientCertificate(t *testing.T) {
	t.Parallel()
	serverCert := mintCert(t, nil, certKindServer)
	ca := mintCert(t, nil, certKindCA)
	client := mintCert(t, &ca, certKindClient)
	certPath, keyPath := writePair(t, serverCert)
	caPath := writePEM(t, "ca.pem", ca.certPEM)
	tlsConfig, err := loadListenerTLS(config{
		TLSEnabled: true,
		TLSCert:    certPath,
		TLSKey:     keyPath,
		TLSCA:      caPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if tlsConfig.ClientAuth != tls.RequireAndVerifyClientCert || tlsConfig.ClientCAs == nil {
		t.Fatalf("client auth %v", tlsConfig.ClientAuth)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	httpServer := &http.Server{
		Handler:           http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }),
		ReadHeaderTimeout: time.Second,
		ErrorLog:          httpErrorLog(newLogger(io.Discard, "error")),
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- serve(httpServer, ln, tlsConfig)
	}()
	port := ln.Addr().(*net.TCPAddr).Port
	if _, status, _, err := httpsGet(port, serverCert.cert, nil, "/health"); err == nil && status == http.StatusOK {
		t.Fatal("in-process listener accepted a client with no certificate")
	}
	if _, status, _, err := httpsGet(port, serverCert.cert, &client, "/health"); err != nil || status != http.StatusOK {
		t.Fatalf("client certificate: %v status %d", err, status)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-errCh; err != nil {
		t.Fatal(err)
	}
}

func TestTLSSettingsExit(t *testing.T) {
	t.Parallel()
	t.Run("missing file", func(t *testing.T) {
		t.Parallel()
		port := freePort(t)
		missing := filepath.Join(t.TempDir(), "missing.pem")
		proc := requireExit(t, processEnv(t, port, map[string]string{
			"TLS_ENABLED": "true",
			"TLS_CERT":    missing,
			"TLS_KEY":     missing + ".key",
		}), port)
		assertStartupError(t, proc)
	})

	t.Run("key only", func(t *testing.T) {
		t.Parallel()
		port := freePort(t)
		serverCert := mintCert(t, nil, certKindServer)
		_, keyPath := writePair(t, serverCert)
		proc := requireExit(t, processEnv(t, port, map[string]string{
			"TLS_ENABLED": "true",
			"TLS_KEY":     keyPath,
		}), port)
		assertStartupError(t, proc)
		assertNoKey(t, proc.output(), serverCert)
	})

	t.Run("unreadable certificate", func(t *testing.T) {
		t.Parallel()
		port := freePort(t)
		serverCert := mintCert(t, nil, certKindServer)
		certPath, keyPath := writePair(t, serverCert)
		if err := os.Chmod(certPath, 0o000); err != nil {
			t.Fatal(err)
		}
		proc := requireExit(t, processEnv(t, port, map[string]string{
			"TLS_ENABLED": "true",
			"TLS_CERT":    certPath,
			"TLS_KEY":     keyPath,
		}), port)
		assertStartupError(t, proc)
		assertNoKey(t, proc.output(), serverCert)
	})

	t.Run("not a certificate", func(t *testing.T) {
		t.Parallel()
		port := freePort(t)
		certPath, keyPath := writeTextPair(t, notCertificate)
		proc := requireExit(t, processEnv(t, port, map[string]string{
			"TLS_ENABLED": "true",
			"TLS_CERT":    certPath,
			"TLS_KEY":     keyPath,
		}), port)
		assertStartupError(t, proc)
		if strings.Contains(proc.output(), notCertificate) {
			t.Fatal("log contains the file")
		}
	})

	t.Run("client authority is not a certificate", func(t *testing.T) {
		t.Parallel()
		port := freePort(t)
		serverCert := mintCert(t, nil, certKindServer)
		certPath, keyPath := writePair(t, serverCert)
		caPath := writePEM(t, "ca.pem", []byte(notAuthority))
		proc := requireExit(t, processEnv(t, port, map[string]string{
			"TLS_ENABLED": "true",
			"TLS_CERT":    certPath,
			"TLS_KEY":     keyPath,
			"TLS_CA":      caPath,
		}), port)
		assertStartupError(t, proc)
		out := proc.output()
		assertNoKey(t, out, serverCert)
		if strings.Contains(out, notAuthority) {
			t.Fatal("log contains the file")
		}
	})
}

type minted struct {
	certPEM []byte
	keyPEM  []byte
	cert    *x509.Certificate
	key     *ecdsa.PrivateKey
}

func mintCert(t *testing.T, parent *minted, kind string) minted {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "dashboard-" + kind},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		BasicConstraintsValid: true,
	}
	switch kind {
	case certKindCA:
		tmpl.IsCA = true
		tmpl.KeyUsage = x509.KeyUsageCertSign
	case certKindServer:
		tmpl.KeyUsage = x509.KeyUsageDigitalSignature
		tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
		tmpl.IPAddresses = []net.IP{net.ParseIP("127.0.0.1")}
	case certKindClient:
		tmpl.KeyUsage = x509.KeyUsageDigitalSignature
		tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	default:
		t.Fatalf("certificate kind %s", kind)
	}
	parentCert := tmpl
	var parentKey any = key
	if parent != nil {
		parentCert = parent.cert
		parentKey = parent.key
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parentCert, &key.PublicKey, parentKey)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if certPEM == nil || keyPEM == nil {
		t.Fatal("encode certificate")
	}
	return minted{certPEM: certPEM, keyPEM: keyPEM, cert: cert, key: key}
}

func writePair(t *testing.T, pair minted) (string, string) {
	t.Helper()
	return writePEM(t, "cert.pem", pair.certPEM), writePEM(t, "key.pem", pair.keyPEM)
}

func writeTextPair(t *testing.T, text string) (string, string) {
	t.Helper()
	body := []byte(text)
	return writePEM(t, "cert.pem", body), writePEM(t, "key.pem", body)
}

func writePEM(t *testing.T, name string, body []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func assertStartupError(t *testing.T, proc *running) {
	t.Helper()
	lines := decodeLog(t, proc.stdout.String())
	if len(lines) == 0 || !strings.EqualFold(lines[0].Level, "error") {
		t.Fatalf("configuration log %+v", lines)
	}
	if strings.Contains(proc.stdout.String(), "listener is up") {
		t.Fatalf("listener started\n%s", proc.stdout.String())
	}
	if strings.TrimSpace(proc.stderr.String()) != "" {
		t.Fatalf("stderr %s", proc.stderr.String())
	}
}

func assertNoKey(t *testing.T, out string, pairs ...minted) {
	t.Helper()
	for _, pair := range pairs {
		for _, line := range strings.Split(string(pair.keyPEM), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "-----") {
				continue
			}
			if strings.Contains(out, line) {
				t.Fatal("log contains a private key")
			}
		}
	}
}

func waitHTTPS(t *testing.T, port int, serverCert minted, client *minted) []byte {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var last error
	for time.Now().Before(deadline) {
		peer, status, body, err := httpsGet(port, serverCert.cert, client, "/health")
		if err == nil && status == http.StatusOK && len(body) == 0 {
			return peer
		}
		last = err
		if err == nil {
			last = errStatus(status, body)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("https: %v", last)
	return nil
}

func httpsGet(port int, serverCert *x509.Certificate, client *minted, path string) ([]byte, int, []byte, error) {
	conn, err := tls.Dial("tcp", hostPort(port), clientTLS(serverCert, client))
	if err != nil {
		return nil, 0, nil, err
	}
	peer := conn.ConnectionState().PeerCertificates
	var raw []byte
	if len(peer) > 0 {
		raw = peer[0].Raw
	}
	if err := conn.Close(); err != nil {
		return raw, 0, nil, err
	}
	httpClient := &http.Client{
		Timeout: 2 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig:   clientTLS(serverCert, client),
			DisableKeepAlives: true,
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+hostPort(port)+path, nil)
	if err != nil {
		return raw, 0, nil, err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return raw, 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return raw, 0, nil, err
	}
	return raw, resp.StatusCode, body, nil
}

func tlsHandshake(port int, serverCert *x509.Certificate, client *minted) error {
	cfg := clientTLS(serverCert, client)
	if serverCert == nil {
		cfg = &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: true}
	}
	conn, err := tls.Dial("tcp", hostPort(port), cfg)
	if err != nil {
		return err
	}
	return conn.Close()
}

func clientTLS(serverCert *x509.Certificate, client *minted) *tls.Config {
	pool := x509.NewCertPool()
	if serverCert != nil {
		pool.AddCert(serverCert)
	}
	cfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
		RootCAs:    pool,
		ServerName: "127.0.0.1",
	}
	if client != nil {
		cfg.Certificates = []tls.Certificate{{
			Certificate: [][]byte{client.cert.Raw},
			PrivateKey:  client.key,
		}}
	}
	return cfg
}

func errStatus(status int, body []byte) error {
	return &statusError{status: status, body: body}
}

type statusError struct {
	status int
	body   []byte
}

func (e *statusError) Error() string {
	return strings.TrimSpace(string(e.body))
}
