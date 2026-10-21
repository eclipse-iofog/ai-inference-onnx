package natsapi

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nkeys"
)

const (
	retryMessage       = "nats connection attempt failed, retrying"
	credsMessage       = "nats credentials could not be read, retrying"
	caMessage          = "nats CA file could not be read, retrying"
	connectedMessage   = "nats connected"
	disconnectMessage  = "nats disconnected"
	listenerUpMessage  = "nats listener is up"
	shortReconnectWait = 5 * time.Millisecond
)

// runServer starts an embedded NATS server with opts and stops it when the test ends.
func runServer(t *testing.T, opts *server.Options) *server.Server {
	t.Helper()
	opts.NoLog = true
	opts.NoSigs = true
	srv, err := server.NewServer(opts)
	if err != nil {
		t.Fatal(err)
	}
	srv.Start()
	t.Cleanup(srv.Shutdown)
	if !srv.ReadyForConnections(replyWait) {
		t.Fatal("embedded NATS server did not start")
	}
	return srv
}

func plainServerOptions(port int) *server.Options {
	return &server.Options{Host: "127.0.0.1", Port: port}
}

func serverPort(t *testing.T, srv *server.Server) int {
	t.Helper()
	addr, ok := srv.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("server address %T", srv.Addr())
	}
	return addr.Port
}

// refusedAddress is a local address whose listener was opened and closed, so a dial is refused.
func refusedAddress(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	return addr
}

// identity is an account signing key and the operator that signed the account.
type identity struct {
	operator    nkeys.KeyPair
	account     nkeys.KeyPair
	accountJWT  string
	operatorJWT string
}

func newIdentity(t *testing.T) identity {
	t.Helper()
	operator, err := nkeys.CreateOperator()
	if err != nil {
		t.Fatal(err)
	}
	operatorPub, err := operator.PublicKey()
	if err != nil {
		t.Fatal(err)
	}
	operatorJWT, err := jwt.NewOperatorClaims(operatorPub).Encode(operator)
	if err != nil {
		t.Fatal(err)
	}
	account, err := nkeys.CreateAccount()
	if err != nil {
		t.Fatal(err)
	}
	accountPub, err := account.PublicKey()
	if err != nil {
		t.Fatal(err)
	}
	accountJWT, err := jwt.NewAccountClaims(accountPub).Encode(operator)
	if err != nil {
		t.Fatal(err)
	}
	return identity{operator: operator, account: account, accountJWT: accountJWT, operatorJWT: operatorJWT}
}

// credentials mints a user of the account and returns the contents of its credentials file,
// the user JWT, and the user seed.
func (id identity) credentials(t *testing.T) (contents []byte, userJWT, seed string) {
	t.Helper()
	user, err := nkeys.CreateUser()
	if err != nil {
		t.Fatal(err)
	}
	userPub, err := user.PublicKey()
	if err != nil {
		t.Fatal(err)
	}
	userJWT, err = jwt.NewUserClaims(userPub).Encode(id.account)
	if err != nil {
		t.Fatal(err)
	}
	rawSeed, err := user.Seed()
	if err != nil {
		t.Fatal(err)
	}
	contents, err = jwt.FormatUserConfig(userJWT, rawSeed)
	if err != nil {
		t.Fatal(err)
	}
	return contents, userJWT, string(rawSeed)
}

// trust makes opts accept users of the account and nobody else.
func (id identity) trust(t *testing.T, opts *server.Options) {
	t.Helper()
	claims, err := jwt.DecodeOperatorClaims(id.operatorJWT)
	if err != nil {
		t.Fatal(err)
	}
	accountPub, err := id.account.PublicKey()
	if err != nil {
		t.Fatal(err)
	}
	resolver := &server.MemAccResolver{}
	if err := resolver.Store(accountPub, id.accountJWT); err != nil {
		t.Fatal(err)
	}
	opts.TrustedOperators = []*jwt.OperatorClaims{claims}
	opts.AccountResolver = resolver
}

func writeFile(t *testing.T, path string, contents []byte) {
	t.Helper()
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatal(err)
	}
}

// writeCredentials writes a fresh user of a throwaway account to path and returns the secret parts.
func writeCredentials(t *testing.T, path string) (userJWT, seed string) {
	t.Helper()
	contents, userJWT, seed := newIdentity(t).credentials(t)
	writeFile(t, path, contents)
	return userJWT, seed
}

// tlsFiles is a CA in PEM and a server certificate for 127.0.0.1 that the CA signed.
type tlsFiles struct {
	caPEM  []byte
	server tls.Certificate
}

func newTLSFiles(t *testing.T) tlsFiles {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test nats ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	leafTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	return tlsFiles{
		caPEM:  pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}),
		server: tls.Certificate{Certificate: [][]byte{leafDER}, PrivateKey: leafKey},
	}
}

func (f tlsFiles) serverOptions() *server.Options {
	return &server.Options{
		Host:      "127.0.0.1",
		Port:      server.RANDOM_PORT,
		TLS:       true,
		TLSVerify: false,
		TLSConfig: &tls.Config{Certificates: []tls.Certificate{f.server}, MinVersion: tls.VersionTLS12},
	}
}

type logLine struct {
	Level string `json:"level"`
	Msg   string `json:"msg"`
	Error string `json:"error"`
}

func logLines(t *testing.T, buf *syncBuffer) []logLine {
	t.Helper()
	var lines []logLine
	for _, raw := range strings.Split(buf.String(), "\n") {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		var line logLine
		if err := json.Unmarshal([]byte(raw), &line); err != nil {
			t.Fatalf("log line %q: %v", raw, err)
		}
		lines = append(lines, line)
	}
	return lines
}

// logged returns the lines with this level and message.
func logged(t *testing.T, buf *syncBuffer, level, msg string) []logLine {
	t.Helper()
	var out []logLine
	for _, line := range logLines(t, buf) {
		if line.Level == level && line.Msg == msg {
			out = append(out, line)
		}
	}
	return out
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// assertLogOmits fails when the log contains any of the secrets.
func assertLogOmits(t *testing.T, buf *syncBuffer, secrets ...string) {
	t.Helper()
	out := buf.String()
	for _, secret := range secrets {
		if secret != "" && strings.Contains(out, secret) {
			t.Fatalf("log contains a secret:\n%s", out)
		}
	}
}

// listen starts a listener with a short pause between attempts and returns it with its log.
func listen(t *testing.T, cfg ListenerConfig) (*Listener, *syncBuffer) {
	t.Helper()
	logs := &syncBuffer{}
	if cfg.Handler == nil {
		cfg.Handler = NewHandler(Config{})
	}
	cfg.Logger = debugLogger(logs)
	if cfg.reconnectWait == 0 {
		cfg.reconnectWait = shortReconnectWait
		cfg.reconnectJitter = time.Millisecond
	}
	l, err := Dial(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(l.Close)
	return l, logs
}

func credsPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "user.creds")
}
