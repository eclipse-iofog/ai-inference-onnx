package main

import (
	"bytes"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"

	"github.com/eclipse-iofog/ai-inference-onnx/dashboard/internal/page"
	"github.com/eclipse-iofog/ai-inference-onnx/dashboard/internal/store"
)

const (
	fixtureEngineA = `{
  "schema": 1,
  "server_id": "engine-a",
  "live": true,
  "ready": true,
  "nats_connected": true,
  "node_uid": "node-1",
  "microservice_name": "wafer-engine",
  "application_name": "line",
  "models": [
    {
      "model": "wafer-defect",
      "version": "1",
      "state": "READY",
      "reason": "",
      "inference_request_success": 2,
      "inference_request_failure": {"REJECTED": 0, "CANCELED": 0, "BACKEND": 0, "OTHER": 0},
      "inference_count": 2,
      "inference_exec_count": 2,
      "inference_pending_request_count": 0,
      "inference_request_duration_us": 100,
      "inference_queue_duration_us": 10,
      "inference_compute_infer_duration_us": 80,
      "inference_model_ready": 1
    }
  ]
}`

	fixtureEngineB = `{
  "schema": 1,
  "server_id": "engine-b",
  "live": true,
  "ready": true,
  "nats_connected": true,
  "node_uid": "node-2",
  "microservice_name": "wafer-engine-b",
  "application_name": "line",
  "models": [
    {
      "model": "wafer-defect",
      "version": "1",
      "state": "READY",
      "reason": "",
      "inference_request_success": 2,
      "inference_request_failure": {"REJECTED": 0, "CANCELED": 0, "BACKEND": 0, "OTHER": 1},
      "inference_count": 8,
      "inference_exec_count": 2,
      "inference_pending_request_count": 1,
      "inference_request_duration_us": 300,
      "inference_queue_duration_us": 0,
      "inference_compute_infer_duration_us": 0,
      "inference_model_ready": 1
    }
  ]
}`

	fixtureOther = `{
  "schema": 1,
  "server_id": "engine-other",
  "live": true,
  "ready": true,
  "nats_connected": true,
  "node_uid": "node-other",
  "microservice_name": "other-engine",
  "application_name": "other",
  "models": [
    {
      "model": "other-defect",
      "version": "1",
      "state": "READY",
      "reason": "",
      "inference_request_success": 1,
      "inference_request_failure": {"REJECTED": 0, "CANCELED": 0, "BACKEND": 0, "OTHER": 0},
      "inference_count": 1,
      "inference_exec_count": 1,
      "inference_pending_request_count": 0,
      "inference_request_duration_us": 50,
      "inference_queue_duration_us": 0,
      "inference_compute_infer_duration_us": 0,
      "inference_model_ready": 1
    }
  ]
}`
)

func TestSnapshotsStayOnTheDashboardAccount(t *testing.T) {
	t.Parallel()
	acc := startAccounts(t)
	reader := connectUser(t, acc.url, acc.readerPath, "reader")
	sub, err := reader.SubscribeSync(snapshotSubject)
	if err != nil {
		t.Fatal(err)
	}
	if err := reader.FlushTimeout(2 * time.Second); err != nil {
		t.Fatal(err)
	}
	other := connectUser(t, acc.url, acc.otherPath, "other")
	publish(t, other, "inference.metrics.engines.engine-other", fixtureOther)
	if msg, err := sub.NextMsg(300 * time.Millisecond); err == nil {
		t.Fatalf("other account was delivered: %s", msg.Data)
	}

	port := freePort(t)
	missingCA := filepath.Join(t.TempDir(), "missing-ca.pem")
	proc := startProcess(t, processEnv(t, port, map[string]string{
		"NATS_SERVER_URL": acc.url,
		"NATS_CREDS_PATH": acc.dashPath,
		"NATS_CA_FILE":    missingCA,
	}))
	t.Cleanup(proc.kill)
	waitHealth(t, port)
	info := waitDashboard(t, acc.srv)
	assertDashboardSubscription(t, info)

	pub := connectUser(t, acc.url, acc.pubPath, "publisher")
	publish(t, pub, "inference.metrics.engines.engine-a", fixtureEngineA)
	if msg := nextMsg(t, sub); !bytes.Contains(msg.Data, []byte(`"server_id": "engine-a"`)) {
		t.Fatalf("first delivery %s", msg.Data)
	}
	publish(t, pub, "inference.metrics.engines.engine-b", fixtureEngineB)
	if msg := nextMsg(t, sub); !bytes.Contains(msg.Data, []byte(`"server_id": "engine-b"`)) {
		t.Fatalf("second delivery %s", msg.Data)
	}

	models := waitPage(t, port, "/models", "engine-b")
	if strings.Count(models, `class="model"`) != 1 || strings.Count(models, "wafer-defect") != 1 {
		t.Fatalf("models row:\n%s", models)
	}
	if !strings.Contains(models, `href="/engines/engine-a"`) || !strings.Contains(models, `href="/engines/engine-b"`) || !strings.Contains(models, "unknown") {
		t.Fatalf("models page:\n%s", models)
	}
	if strings.Contains(models, "engine-other") || strings.Contains(models, "other-defect") || strings.Contains(models, "NATS is not") {
		t.Fatalf("models page:\n%s", models)
	}
	nodes := getBody(t, port, "/nodes")
	if !strings.Contains(nodes, "node-1") || !strings.Contains(nodes, "node-2") || strings.Contains(nodes, "node-other") {
		t.Fatalf("nodes page:\n%s", nodes)
	}
	engine := getBody(t, port, "/engines/engine-b")
	if !strings.Contains(engine, "wafer-engine-b") || !strings.Contains(engine, "Queue depth 1") {
		t.Fatalf("engine page:\n%s", engine)
	}

	info = waitDashboard(t, acc.srv)
	assertDashboardSubscription(t, info)
	if info.InMsgs != 0 {
		t.Fatalf("dashboard published %d messages", info.InMsgs)
	}
	out := proc.stdout.String() + proc.stderr.String()
	if strings.Contains(out, acc.dashJWT) || strings.Contains(out, acc.dashSeed) || strings.Contains(out, "BEGIN NKEY") {
		t.Fatal("log contains credentials")
	}
	if strings.Contains(out, "nats CA file could not be read") {
		t.Fatal("plaintext server read the CA file")
	}
	status, health := get(t, port, "/health")
	if status != 200 || len(health) != 0 {
		t.Fatalf("health %d %q", status, health)
	}
}

func TestSubscriptionReturnsAfterReconnect(t *testing.T) {
	t.Parallel()
	acc := startAccounts(t)
	port := freePort(t)
	proc := startProcess(t, processEnv(t, port, map[string]string{
		"NATS_SERVER_URL": acc.url,
		"NATS_CREDS_PATH": acc.dashPath,
	}))
	t.Cleanup(proc.kill)
	waitHealth(t, port)
	waitDashboard(t, acc.srv)

	pub := connectUser(t, acc.url, acc.pubPath, "publisher")
	publish(t, pub, "inference.metrics.engines.engine-a", fixtureEngineA)
	waitPage(t, port, "/models", "engine-a")

	natsPort := acc.stop(t)
	body := waitPage(t, port, "/models", "NATS is not connected.")
	if strings.Contains(body, "NATS is not configured.") {
		t.Fatalf("body %s", body)
	}
	status, health := get(t, port, "/health")
	if status != 200 || len(health) != 0 {
		t.Fatalf("health %d %q", status, health)
	}
	waitLog(t, proc, "nats disconnected")

	acc.start(t, natsPort)
	waitDashboard(t, acc.srv)
	pub = connectUser(t, acc.url, acc.pubPath, "publisher-again")
	publish(t, pub, "inference.metrics.engines.engine-b", fixtureEngineB)
	models := waitPage(t, port, "/models", "engine-b")
	if strings.Contains(models, "NATS is not connected.") || strings.Contains(models, "NATS is not configured.") {
		t.Fatalf("still down:\n%s", models)
	}
	if proc.exited() {
		t.Fatalf("exited\n%s", proc.output())
	}
}

func TestDashboardUserCannotPublishOrSubscribeElsewhere(t *testing.T) {
	t.Parallel()
	acc := startAccounts(t)

	denied := connectUser(t, acc.url, acc.dashPath, "denied-sub")
	sub, err := denied.SubscribeSync("inference.v2.>")
	if err != nil {
		t.Fatal(err)
	}
	_, err = sub.NextMsg(2 * time.Second)
	if !errors.Is(err, nats.ErrPermissionViolation) {
		t.Fatalf("subscribe inference.v2: %v", err)
	}

	publisher := connectUser(t, acc.url, acc.dashPath, "denied-pub")
	if err := publisher.Publish("inference.metrics.engines.probe", []byte("x")); err != nil {
		t.Fatal(err)
	}
	flushErr := publisher.FlushTimeout(2 * time.Second)
	if !errors.Is(publisher.LastError(), nats.ErrPermissionViolation) && !errors.Is(flushErr, nats.ErrPermissionViolation) {
		t.Fatalf("publish last %v flush %v", publisher.LastError(), flushErr)
	}

	allowed := connectUser(t, acc.url, acc.dashPath, "allowed")
	kept, err := allowed.SubscribeSync(snapshotSubject)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := kept.NextMsg(300 * time.Millisecond); err != nil && !errors.Is(err, nats.ErrTimeout) {
		t.Fatalf("subscribe %s: %v", snapshotSubject, err)
	}
}

func TestSubscriberUsesClientRetry(t *testing.T) {
	t.Parallel()
	var logs lockedBuf
	cfg := config{
		NATSServerURL: "nats://" + refusedAddress(t),
		NATSCredsPath: filepath.Join(t.TempDir(), "missing.creds"),
		NATSCAFile:    filepath.Join(t.TempDir(), "missing-ca.pem"),
	}
	sub, err := dialSubscriber(cfg, store.New(nil), newLogger(&logs, "info"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sub.Close)
	opts := sub.conn.Opts
	if !opts.RetryOnFailedConnect || opts.MaxReconnect != -1 || !opts.IgnoreAuthErrorAbort {
		t.Fatalf("retry %t max %d ignore auth abort %t", opts.RetryOnFailedConnect, opts.MaxReconnect, opts.IgnoreAuthErrorAbort)
	}
	if opts.ReconnectWait != nats.DefaultReconnectWait {
		t.Fatalf("wait %v", opts.ReconnectWait)
	}
	if opts.ReconnectJitter != nats.DefaultReconnectJitter || opts.ReconnectJitterTLS != nats.DefaultReconnectJitterTLS {
		t.Fatalf("jitter %v tls %v", opts.ReconnectJitter, opts.ReconnectJitterTLS)
	}
	if opts.CustomReconnectDelayCB != nil {
		t.Fatal("a custom delay replaces the client backoff")
	}
	if opts.RootCAsCB != nil || opts.Secure {
		t.Fatal("plaintext server was configured for TLS")
	}
	if sub.status() != page.ConnectionDown {
		t.Fatalf("status %v", sub.status())
	}

	tlsSub, err := dialSubscriber(config{
		NATSServerURL: "tls://" + refusedAddress(t),
		NATSCredsPath: cfg.NATSCredsPath,
		NATSCAFile:    cfg.NATSCAFile,
	}, store.New(nil), newLogger(&lockedBuf{}, "info"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(tlsSub.Close)
	if tlsSub.conn.Opts.RootCAsCB == nil || !tlsSub.conn.Opts.Secure {
		t.Fatal("tls server did not use the CA file")
	}
}

func TestMissingCAFileRetries(t *testing.T) {
	t.Parallel()
	leaf := mintCert(t, nil, certKindCA)
	serverCert := mintCert(t, &leaf, certKindServer)
	pair, err := tls.X509KeyPair(serverCert.certPEM, serverCert.keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	id := newIdentity(t)
	opts := id.trust(t, &server.Options{
		Host:      "127.0.0.1",
		Port:      server.RANDOM_PORT,
		TLS:       true,
		TLSVerify: false,
		TLSConfig: &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12},
	})
	srv := runServer(t, opts)
	creds := filepath.Join(t.TempDir(), "user.creds")
	contents, userJWT, seed := id.credentials(t, nil, nil, nil)
	writeFile(t, creds, contents)
	caPath := filepath.Join(t.TempDir(), "ca.pem")
	var logs lockedBuf
	sub, err := dialSubscriber(config{
		NATSServerURL: fmt.Sprintf("tls://127.0.0.1:%d", serverPort(t, srv)),
		NATSCredsPath: creds,
		NATSCAFile:    caPath,
	}, store.New(nil), newLogger(&logs, "info"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sub.Close)
	waitFor(t, "a missing CA retry", func() bool {
		return strings.Count(logs.String(), "nats CA file could not be read, retrying") >= 1
	})
	if sub.status() == page.ConnectionUp {
		t.Fatal("connected without a CA file")
	}
	writeFile(t, caPath, leaf.certPEM)
	waitFor(t, "connected once the CA file appeared", func() bool {
		return sub.status() == page.ConnectionUp
	})
	out := logs.String()
	if strings.Contains(out, string(leaf.certPEM)) || strings.Contains(out, userJWT) || strings.Contains(out, seed) {
		t.Fatal("log contains the CA or the credentials")
	}
}

func waitDashboard(t *testing.T, srv *server.Server) *server.ConnInfo {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		cz, err := srv.Connz(&server.ConnzOptions{Subscriptions: true, SubscriptionsDetail: true})
		if err != nil {
			t.Fatal(err)
		}
		for _, info := range cz.Conns {
			if info != nil && info.Name == clientName && len(info.SubsDetail) > 0 {
				return info
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("dashboard did not subscribe")
	return nil
}

func assertDashboardSubscription(t *testing.T, info *server.ConnInfo) {
	t.Helper()
	if info.Name != clientName {
		t.Fatalf("connection %s", info.Name)
	}
	if len(info.SubsDetail) != 1 {
		t.Fatalf("subscriptions %+v", info.SubsDetail)
	}
	sub := info.SubsDetail[0]
	if sub.Subject != snapshotSubject || sub.Queue != "" {
		t.Fatalf("subscription %+v", sub)
	}
	for _, detail := range info.SubsDetail {
		if strings.Contains(detail.Subject, "inference.v2") {
			t.Fatalf("subscribed to %s", detail.Subject)
		}
	}
	for _, subject := range info.Subs {
		if strings.Contains(subject, "inference.v2") || strings.Contains(subject, " ") {
			t.Fatalf("subscription list %s", subject)
		}
	}
}

func waitPage(t *testing.T, port int, path, text string) string {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		status, body, err := getErr(port, path)
		if err == nil && status == 200 {
			last = string(body)
			if strings.Contains(last, text) {
				return last
			}
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatalf("GET %s missing %q\n%s", path, text, last)
	return ""
}

func publish(t *testing.T, nc *nats.Conn, subject, body string) {
	t.Helper()
	if err := nc.Publish(subject, []byte(body)); err != nil {
		t.Fatal(err)
	}
	if err := nc.FlushTimeout(2 * time.Second); err != nil {
		t.Fatal(err)
	}
}

func nextMsg(t *testing.T, sub *nats.Subscription) *nats.Msg {
	t.Helper()
	msg, err := sub.NextMsg(3 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return msg
}

func connectUser(t *testing.T, url, creds, name string) *nats.Conn {
	t.Helper()
	nc, err := nats.Connect(url,
		nats.UserCredentials(creds),
		nats.Name(name),
		nats.PermissionErrOnSubscribe(true),
		nats.NoReconnect(),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	return nc
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func runPlainServer(t *testing.T) *server.Server {
	t.Helper()
	return runServer(t, &server.Options{Host: "127.0.0.1", Port: server.RANDOM_PORT})
}

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
	if !srv.ReadyForConnections(5 * time.Second) {
		t.Fatal("embedded NATS server did not start")
	}
	return srv
}

func serverPort(t *testing.T, srv *server.Server) int {
	t.Helper()
	addr, ok := srv.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("server address %T", srv.Addr())
	}
	return addr.Port
}

type identity struct {
	operator    nkeys.KeyPair
	operatorJWT string
	account     nkeys.KeyPair
	accountJWT  string
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
	return newAccount(t, operator, operatorJWT)
}

func newAccount(t *testing.T, operator nkeys.KeyPair, operatorJWT string) identity {
	t.Helper()
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
	return identity{operator: operator, operatorJWT: operatorJWT, account: account, accountJWT: accountJWT}
}

func (id identity) credentials(t *testing.T, pubAllow, pubDeny, subAllow []string) (contents []byte, userJWT, seed string) {
	t.Helper()
	user, err := nkeys.CreateUser()
	if err != nil {
		t.Fatal(err)
	}
	userPub, err := user.PublicKey()
	if err != nil {
		t.Fatal(err)
	}
	claims := jwt.NewUserClaims(userPub)
	claims.Pub.Allow.Add(pubAllow...)
	claims.Pub.Deny.Add(pubDeny...)
	claims.Sub.Allow.Add(subAllow...)
	userJWT, err = claims.Encode(id.account)
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

func (id identity) trust(t *testing.T, opts *server.Options) *server.Options {
	t.Helper()
	return trustAccounts(t, opts, id)
}

func trustAccounts(t *testing.T, opts *server.Options, ids ...identity) *server.Options {
	t.Helper()
	if len(ids) == 0 {
		t.Fatal("no accounts")
	}
	claims, err := jwt.DecodeOperatorClaims(ids[0].operatorJWT)
	if err != nil {
		t.Fatal(err)
	}
	resolver := &server.MemAccResolver{}
	for _, id := range ids {
		accountPub, err := id.account.PublicKey()
		if err != nil {
			t.Fatal(err)
		}
		if err := resolver.Store(accountPub, id.accountJWT); err != nil {
			t.Fatal(err)
		}
	}
	opts.TrustedOperators = []*jwt.OperatorClaims{claims}
	opts.AccountResolver = resolver
	return opts
}

type accounts struct {
	makeOpts   func(port int) *server.Options
	srv        *server.Server
	url        string
	dashPath   string
	dashJWT    string
	dashSeed   string
	readerPath string
	pubPath    string
	otherPath  string
}

func startAccounts(t *testing.T) *accounts {
	t.Helper()
	line := newIdentity(t)
	other := newAccount(t, line.operator, line.operatorJWT)
	acc := &accounts{}
	acc.makeOpts = func(port int) *server.Options {
		return trustAccounts(t, &server.Options{Host: "127.0.0.1", Port: port}, line, other)
	}
	acc.srv = runServer(t, acc.makeOpts(server.RANDOM_PORT))
	acc.url = acc.srv.ClientURL()
	dir := t.TempDir()
	acc.dashPath = filepath.Join(dir, "dashboard.creds")
	contents, userJWT, seed := line.credentials(t, nil, []string{">"}, []string{snapshotSubject})
	writeFile(t, acc.dashPath, contents)
	acc.dashJWT = userJWT
	acc.dashSeed = seed
	acc.readerPath = filepath.Join(dir, "reader.creds")
	contents, _, _ = line.credentials(t, nil, []string{">"}, []string{snapshotSubject})
	writeFile(t, acc.readerPath, contents)
	acc.pubPath = filepath.Join(dir, "publisher.creds")
	contents, _, _ = line.credentials(t, []string{snapshotSubject}, nil, []string{snapshotSubject})
	writeFile(t, acc.pubPath, contents)
	acc.otherPath = filepath.Join(dir, "other.creds")
	contents, _, _ = other.credentials(t, []string{snapshotSubject}, nil, nil)
	writeFile(t, acc.otherPath, contents)
	return acc
}

func (a *accounts) stop(t *testing.T) int {
	t.Helper()
	port := serverPort(t, a.srv)
	a.srv.Shutdown()
	a.srv.WaitForShutdown()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err == nil {
			if closeErr := ln.Close(); closeErr != nil {
				t.Fatal(closeErr)
			}
			return port
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("port %d stayed taken", port)
	return 0
}

func (a *accounts) start(t *testing.T, port int) {
	t.Helper()
	a.srv = runServer(t, a.makeOpts(port))
	if serverPort(t, a.srv) != port {
		t.Fatalf("port %d, want %d", serverPort(t, a.srv), port)
	}
	a.url = a.srv.ClientURL()
}

func writeFile(t *testing.T, path string, contents []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatal(err)
	}
}
