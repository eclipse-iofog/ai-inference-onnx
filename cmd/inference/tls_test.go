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
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/eclipse-iofog/ai-inference-onnx/api/openinference/v2"
	"github.com/eclipse-iofog/ai-inference-onnx/internal/config"
)

const (
	tlsToken       = "tls-token-4e8b1c9a0d7f6235ab91c0e4"
	notCertificate = "not-a-certificate-9f3c1a7e5b24"
	notAuthority   = "not-a-client-authority-6b0d2e8a41c7"
	certKindServer = "server"
	certKindClient = "client"
	certKindCA     = "ca"
)

func TestLoadListenerTLS(t *testing.T) {
	t.Parallel()

	t.Run("disabled does not open paths", func(t *testing.T) {
		t.Parallel()
		missing := filepath.Join(t.TempDir(), "missing")
		got, err := loadListenerTLS(config.Config{
			TLSCert: missing,
			TLSKey:  missing + ".key",
			TLSCA:   missing + ".ca",
		})
		if err != nil || got != nil {
			t.Fatalf("config %v err %v", got, err)
		}
	})

	t.Run("certificate pair", func(t *testing.T) {
		t.Parallel()
		server := mintCert(t, nil, certKindServer)
		certPath, keyPath := writePair(t, server)
		got, err := loadListenerTLS(config.Config{
			TLSEnabled: true,
			TLSCert:    certPath,
			TLSKey:     keyPath,
		})
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if got.ClientAuth != tls.NoClientCert || got.ClientCAs != nil {
			t.Fatalf("client auth %v", got.ClientAuth)
		}
		if len(got.Certificates) != 1 || len(got.Certificates[0].Certificate) == 0 || !bytes.Equal(got.Certificates[0].Certificate[0], server.cert.Raw) {
			t.Fatal("loaded certificate mismatch")
		}
	})

	t.Run("client authority", func(t *testing.T) {
		t.Parallel()
		server := mintCert(t, nil, certKindServer)
		ca := mintCert(t, nil, certKindCA)
		certPath, keyPath := writePair(t, server)
		caPath := writePEM(t, "ca.pem", ca.certPEM)
		got, err := loadListenerTLS(config.Config{
			TLSEnabled: true,
			TLSCert:    certPath,
			TLSKey:     keyPath,
			TLSCA:      caPath,
		})
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if got.ClientAuth != tls.RequireAndVerifyClientCert || got.ClientCAs == nil {
			t.Fatalf("client auth %v", got.ClientAuth)
		}
	})

	t.Run("not a certificate pair", func(t *testing.T) {
		t.Parallel()
		certPath, keyPath := writeTextPair(t, notCertificate)
		_, err := loadListenerTLS(config.Config{
			TLSEnabled: true,
			TLSCert:    certPath,
			TLSKey:     keyPath,
		})
		if err == nil {
			t.Fatal("expected an error")
		}
		if strings.Contains(err.Error(), notCertificate) {
			t.Fatalf("error contains file contents: %v", err)
		}
	})

	t.Run("client authority is not a certificate", func(t *testing.T) {
		t.Parallel()
		server := mintCert(t, nil, certKindServer)
		certPath, keyPath := writePair(t, server)
		caPath := writePEM(t, "ca.pem", []byte(notAuthority))
		_, err := loadListenerTLS(config.Config{
			TLSEnabled: true,
			TLSCert:    certPath,
			TLSKey:     keyPath,
			TLSCA:      caPath,
		})
		if err == nil {
			t.Fatal("expected an error")
		}
		if strings.Contains(err.Error(), notAuthority) || strings.Contains(err.Error(), string(server.keyPEM)) {
			t.Fatalf("error contains file contents: %v", err)
		}
	})
}

func TestTLSDisabledServesPlain(t *testing.T) {
	ports := pickPorts(t)
	missing := filepath.Join(t.TempDir(), "missing")
	proc := startProcess(t, processEnv(t, ports, map[string]string{
		"AUTH_MODE":   "disabled",
		"TLS_ENABLED": "false",
		"TLS_CERT":    missing,
		"TLS_KEY":     missing + ".key",
		"TLS_CA":      missing + ".ca",
	}))
	t.Cleanup(proc.kill)

	waitLive(t, ports.http)
	assertGET(t, ports.http, "/v2/health/live", http.StatusOK)
	assertGRPCHealth(t, ports.grpc, false)
	assertNotTLS(t, ports.http)
	assertNotTLS(t, ports.grpc)
	assertMetricsOpen(t, ports.metrics)
}

func TestTLSServesOneCertificate(t *testing.T) {
	if len(tlsToken) < 32 {
		t.Fatalf("token length %d", len(tlsToken))
	}
	ports := pickPorts(t)
	server := mintCert(t, nil, certKindServer)
	certPath, keyPath := writePair(t, server)
	proc := startProcess(t, processEnv(t, ports, map[string]string{
		"AUTH_MODE":         "static",
		"AUTH_STATIC_TOKEN": tlsToken,
		"TLS_ENABLED":       "true",
		"TLS_CERT":          certPath,
		"TLS_KEY":           keyPath,
		"NATS_SERVER_URL":   "nats://" + refusedAddress(t),
		"NATS_CREDS_PATH":   filepath.Join(t.TempDir(), "missing.creds"),
	}))
	t.Cleanup(proc.kill)

	httpPeer := waitHTTPS(t, ports.http, server.cert, nil, true)
	stdout := waitLog(t, proc, "nats connection attempt failed, retrying")
	grpcPeer := grpcLive(t, ports.grpc, server.cert, nil, true)
	if !bytes.Equal(httpPeer, grpcPeer) || !bytes.Equal(httpPeer, server.cert.Raw) {
		t.Fatal("peer certificate mismatch")
	}
	assertPlainLiveFails(t, ports.http)
	assertInsecureGRPCFails(t, ports.grpc)
	assertMetricsOpen(t, ports.metrics)
	out := stdout + proc.stderr.String()
	assertNoKey(t, out, server)
	if strings.Contains(out, tlsToken) {
		t.Fatal("log contains the token")
	}
	if strings.TrimSpace(proc.stderr.String()) != "" {
		t.Fatalf("stderr %s", proc.stderr.String())
	}
}

func TestTLSRequiresClientCertificate(t *testing.T) {
	ports := pickPorts(t)
	server := mintCert(t, nil, certKindServer)
	ca := mintCert(t, nil, certKindCA)
	otherCA := mintCert(t, nil, certKindCA)
	client := mintCert(t, &ca, certKindClient)
	other := mintCert(t, &otherCA, certKindClient)
	certPath, keyPath := writePair(t, server)
	caPath := writePEM(t, "ca.pem", ca.certPEM)

	proc := startProcess(t, processEnv(t, ports, map[string]string{
		"AUTH_MODE":   "disabled",
		"TLS_ENABLED": "true",
		"TLS_CERT":    certPath,
		"TLS_KEY":     keyPath,
		"TLS_CA":      caPath,
	}))
	t.Cleanup(proc.kill)

	httpPeer := waitHTTPS(t, ports.http, server.cert, &client, false)
	grpcPeer := grpcLive(t, ports.grpc, server.cert, &client, false)
	if !bytes.Equal(httpPeer, grpcPeer) || !bytes.Equal(httpPeer, server.cert.Raw) {
		t.Fatal("peer certificate mismatch")
	}
	if _, _, _, err := httpsGetErr(ports.http, server.cert, nil, false, "/v2/health/live"); err == nil {
		t.Fatal("https without a client certificate succeeded")
	}
	if _, err := grpcLiveErr(ports.grpc, server.cert, nil, false); err == nil {
		t.Fatal("grpc without a client certificate succeeded")
	}
	if _, _, _, err := httpsGetErr(ports.http, server.cert, &other, false, "/v2/health/live"); err == nil {
		t.Fatal("https accepted a certificate from another authority")
	}
	if _, err := grpcLiveErr(ports.grpc, server.cert, &other, false); err == nil {
		t.Fatal("grpc accepted a certificate from another authority")
	}
	assertMetricsOpen(t, ports.metrics)
	out := proc.stdout.String() + proc.stderr.String()
	assertNoKey(t, out, server, ca, otherCA, client, other)
}

func TestTLSExitsBeforeListen(t *testing.T) {
	t.Run("missing file", func(t *testing.T) {
		ports := pickPorts(t)
		missing := filepath.Join(t.TempDir(), "missing.pem")
		proc := requireExitWithoutListen(t, processEnv(t, ports, map[string]string{
			"AUTH_MODE":   "disabled",
			"TLS_ENABLED": "true",
			"TLS_CERT":    missing,
			"TLS_KEY":     missing + ".key",
		}), ports)
		assertStartupError(t, proc)
	})

	t.Run("unreadable certificate", func(t *testing.T) {
		ports := pickPorts(t)
		server := mintCert(t, nil, certKindServer)
		certPath, keyPath := writePair(t, server)
		if err := os.Chmod(certPath, 0o000); err != nil {
			t.Fatalf("chmod: %v", err)
		}
		proc := requireExitWithoutListen(t, processEnv(t, ports, map[string]string{
			"AUTH_MODE":   "disabled",
			"TLS_ENABLED": "true",
			"TLS_CERT":    certPath,
			"TLS_KEY":     keyPath,
		}), ports)
		assertStartupError(t, proc)
		assertNoKey(t, proc.stdout.String()+proc.stderr.String(), server)
	})

	t.Run("not a certificate pair", func(t *testing.T) {
		ports := pickPorts(t)
		certPath, keyPath := writeTextPair(t, notCertificate)
		proc := requireExitWithoutListen(t, processEnv(t, ports, map[string]string{
			"AUTH_MODE":   "disabled",
			"TLS_ENABLED": "true",
			"TLS_CERT":    certPath,
			"TLS_KEY":     keyPath,
		}), ports)
		assertStartupError(t, proc)
		out := proc.stdout.String() + proc.stderr.String()
		if strings.Contains(out, notCertificate) {
			t.Fatal("log contains file contents")
		}
	})

	t.Run("client authority is not a certificate", func(t *testing.T) {
		ports := pickPorts(t)
		server := mintCert(t, nil, certKindServer)
		certPath, keyPath := writePair(t, server)
		caPath := writePEM(t, "ca.pem", []byte(notAuthority))
		proc := requireExitWithoutListen(t, processEnv(t, ports, map[string]string{
			"AUTH_MODE":   "disabled",
			"TLS_ENABLED": "true",
			"TLS_CERT":    certPath,
			"TLS_KEY":     keyPath,
			"TLS_CA":      caPath,
		}), ports)
		assertStartupError(t, proc)
		out := proc.stdout.String() + proc.stderr.String()
		assertNoKey(t, out, server)
		if strings.Contains(out, notAuthority) {
			t.Fatal("log contains file contents")
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
		t.Fatalf("generate key: %v", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		t.Fatalf("serial: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "inference-" + kind},
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
		t.Fatalf("create certificate: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
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
		t.Fatalf("write %s: %v", name, err)
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

func assertPlainLiveFails(t *testing.T, port int) {
	t.Helper()
	client := &http.Client{
		Timeout: 2 * time.Second,
		Transport: &http.Transport{
			Proxy:             func(*http.Request) (*url.URL, error) { return nil, nil },
			DisableKeepAlives: true,
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+hostPort(port)+"/v2/health/live", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	body, readErr := io.ReadAll(resp.Body)
	closeErr := resp.Body.Close()
	if readErr != nil {
		t.Fatalf("read: %v", readErr)
	}
	if closeErr != nil {
		t.Fatalf("close: %v", closeErr)
	}
	if resp.StatusCode == http.StatusOK && len(body) == 0 {
		t.Fatal("plain HTTP succeeded")
	}
}

func assertNotTLS(t *testing.T, port int) {
	t.Helper()
	conn, err := tls.Dial("tcp", hostPort(port), &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: true,
	})
	if err != nil {
		return
	}
	if closeErr := conn.Close(); closeErr != nil {
		t.Errorf("close: %v", closeErr)
	}
	t.Fatal("listener accepted TLS")
}

func waitHTTPS(t *testing.T, port int, server *x509.Certificate, client *minted, rejectRequest bool) []byte {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var last error
	for time.Now().Before(deadline) {
		status, peer, body, err := httpsGetErr(port, server, client, rejectRequest, "/v2/health/live")
		if err == nil && status == http.StatusOK && len(body) == 0 && bytes.Equal(peer, server.Raw) {
			return peer
		}
		if err != nil {
			last = err
		} else {
			last = errors.New("unexpected https response")
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("https live: %v", last)
	return nil
}

func httpsGetErr(port int, server *x509.Certificate, client *minted, rejectRequest bool, path string) (int, []byte, []byte, error) {
	cfg, err := clientConfig(server, client, rejectRequest)
	if err != nil {
		return 0, nil, nil, err
	}
	httpClient := &http.Client{
		Timeout: 2 * time.Second,
		Transport: &http.Transport{
			Proxy:             func(*http.Request) (*url.URL, error) { return nil, nil },
			TLSClientConfig:   cfg,
			DisableKeepAlives: true,
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+hostPort(port)+path, nil)
	if err != nil {
		return 0, nil, nil, err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	body, readErr := io.ReadAll(resp.Body)
	closeErr := resp.Body.Close()
	if readErr != nil {
		return 0, nil, nil, readErr
	}
	if closeErr != nil {
		return 0, nil, nil, closeErr
	}
	if resp.TLS == nil || len(resp.TLS.PeerCertificates) == 0 {
		return 0, nil, body, errors.New("missing peer certificate")
	}
	return resp.StatusCode, resp.TLS.PeerCertificates[0].Raw, body, nil
}

func grpcLive(t *testing.T, port int, server *x509.Certificate, client *minted, rejectRequest bool) []byte {
	t.Helper()
	peer, err := grpcLiveErr(port, server, client, rejectRequest)
	if err != nil {
		t.Fatalf("grpc live: %v", err)
	}
	if !bytes.Equal(peer, server.Raw) {
		t.Fatal("peer certificate mismatch")
	}
	return peer
}

func grpcLiveErr(port int, server *x509.Certificate, client *minted, rejectRequest bool) ([]byte, error) {
	cfg, err := clientConfig(server, client, rejectRequest)
	if err != nil {
		return nil, err
	}
	peerCh := make(chan []byte, 1)
	cfg.VerifyConnection = func(state tls.ConnectionState) error {
		if len(state.PeerCertificates) == 0 {
			return errors.New("missing peer certificate")
		}
		select {
		case peerCh <- append([]byte(nil), state.PeerCertificates[0].Raw...):
		default:
		}
		return nil
	}
	conn, err := grpc.NewClient(
		hostPort(port),
		grpc.WithTransportCredentials(credentials.NewTLS(cfg)),
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()
	rpc := openinference.NewGRPCInferenceServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	live, err := rpc.ServerLive(ctx, &openinference.ServerLiveRequest{})
	if err != nil {
		return nil, err
	}
	if !live.GetLive() {
		return nil, errors.New("server live is false")
	}
	select {
	case peer := <-peerCh:
		return peer, nil
	default:
		return nil, errors.New("missing peer certificate")
	}
}

func clientConfig(server *x509.Certificate, client *minted, rejectRequest bool) (*tls.Config, error) {
	pool := x509.NewCertPool()
	pool.AddCert(server)
	cfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
		RootCAs:    pool,
		ServerName: "127.0.0.1",
	}
	if client != nil {
		pair, err := tls.X509KeyPair(client.certPEM, client.keyPEM)
		if err != nil {
			return nil, err
		}
		cfg.Certificates = []tls.Certificate{pair}
	}
	if rejectRequest {
		cfg.GetClientCertificate = func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
			return nil, errors.New("server requested a client certificate")
		}
	}
	return cfg, nil
}

func assertInsecureGRPCFails(t *testing.T, port int) {
	t.Helper()
	conn, err := grpc.NewClient(
		hostPort(port),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial grpc: %v", err)
	}
	defer func() { _ = conn.Close() }()
	rpc := openinference.NewGRPCInferenceServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := rpc.ServerLive(ctx, &openinference.ServerLiveRequest{}); err == nil {
		t.Fatal("insecure grpc dial succeeded")
	}
}
