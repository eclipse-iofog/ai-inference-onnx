package natsapi

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
)

func TestDialRequiresHandler(t *testing.T) {
	_, err := Dial(ListenerConfig{URL: "nats://127.0.0.1:1", CredsPath: credsPath(t)})
	if err == nil {
		t.Fatal("a listener without a handler started")
	}
}

func TestNilListenerIsNotConnected(t *testing.T) {
	var l *Listener
	if l.Connected() {
		t.Fatal("a nil listener reports connected")
	}
	l.Close()
}

func TestListenerUsesClientRetryDefaults(t *testing.T) {
	l, err := Dial(ListenerConfig{
		URL:       "nats://" + refusedAddress(t),
		CredsPath: credsPath(t),
		Handler:   NewHandler(Config{}),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(l.Close)
	opts := l.conn.Opts
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
	if l.Connected() {
		t.Fatal("connected to a refused address")
	}
}

func TestListenerKeepsRetryingARefusedDial(t *testing.T) {
	l, logs := listen(t, ListenerConfig{
		URL:       "nats://" + refusedAddress(t),
		CredsPath: credsPath(t),
	})
	wantAttempts := nats.DefaultMaxReconnect + 10
	waitFor(t, fmt.Sprintf("%d failed attempts", wantAttempts), func() bool {
		return len(logged(t, logs, "WARN", retryMessage)) >= wantAttempts
	})
	if l.Connected() || l.conn.IsClosed() {
		t.Fatalf("connected %t closed %t after %d attempts", l.Connected(), l.conn.IsClosed(), wantAttempts)
	}
	if got := logged(t, logs, "INFO", connectedMessage); len(got) != 0 {
		t.Fatalf("connected lines %+v", got)
	}
	if got := logged(t, logs, "INFO", listenerUpMessage); len(got) != 1 {
		t.Fatalf("listener lines %+v", got)
	}
}

func TestListenerRereadsAMissingCredentialsFile(t *testing.T) {
	srv := runServer(t, plainServerOptions(server.RANDOM_PORT))
	path := credsPath(t)
	l, logs := listen(t, ListenerConfig{URL: srv.ClientURL(), CredsPath: path})

	missing := func() bool {
		for _, line := range logged(t, logs, "WARN", credsMessage) {
			if !strings.Contains(line.Error, "no such file") {
				return false
			}
		}
		return len(logged(t, logs, "WARN", credsMessage)) >= 3
	}
	waitFor(t, "three missing-file attempts", missing)
	if l.Connected() || l.conn.IsClosed() {
		t.Fatalf("connected %t closed %t without credentials", l.Connected(), l.conn.IsClosed())
	}

	const garbage = "not-a-credentials-file-5e7b"
	writeFile(t, path, []byte(garbage))
	waitFor(t, "an attempt that read the replaced file", func() bool {
		for _, line := range logged(t, logs, "WARN", credsMessage) {
			if !strings.Contains(line.Error, "no such file") {
				return true
			}
		}
		return false
	})
	if l.Connected() {
		t.Fatal("connected with an unreadable credentials file")
	}

	userJWT, seed := writeCredentials(t, path)
	waitFor(t, "connected", l.Connected)
	waitFor(t, "connected log", func() bool { return len(logged(t, logs, "INFO", connectedMessage)) == 1 })
	wantStatus(t, request(t, connect(t, srv.ClientURL()), "inference.v2.health.live", nil, nil), 200)
	assertLogOmits(t, logs, garbage, userJWT, seed, "BEGIN NATS")
}

func TestListenerReadsRotatedCredentials(t *testing.T) {
	trusted := newIdentity(t)
	opts := plainServerOptions(server.RANDOM_PORT)
	trusted.trust(t, opts)
	srv := runServer(t, opts)

	path := credsPath(t)
	first, firstJWT, firstSeed := trusted.credentials(t)
	writeFile(t, path, first)
	l, logs := listen(t, ListenerConfig{URL: srv.ClientURL(), CredsPath: path})
	waitFor(t, "connected with trusted credentials", l.Connected)

	untrusted, untrustedJWT, untrustedSeed := newIdentity(t).credentials(t)
	writeFile(t, path, untrusted)
	if err := l.conn.ForceReconnect(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "two rejected attempts", func() bool {
		var rejected int
		for _, line := range logged(t, logs, "WARN", retryMessage) {
			if strings.Contains(strings.ToLower(line.Error), "authorization") {
				rejected++
			}
		}
		return rejected >= 2
	})
	if l.Connected() || l.conn.IsClosed() {
		t.Fatalf("connected %t closed %t with rejected credentials", l.Connected(), l.conn.IsClosed())
	}
	if got := logged(t, logs, "INFO", disconnectMessage); len(got) == 0 {
		t.Fatal("disconnect was not logged at info")
	}

	rotated, rotatedJWT, rotatedSeed := trusted.credentials(t)
	writeFile(t, path, rotated)
	waitFor(t, "connected with rotated credentials", l.Connected)

	client := trustedClient(t, srv.ClientURL(), trusted)
	wantStatus(t, request(t, client, "inference.v2.health.live", nil, nil), 200)
	assertLogOmits(t, logs, firstJWT, firstSeed, untrustedJWT, untrustedSeed, rotatedJWT, rotatedSeed, "BEGIN NATS")
}

func trustedClient(t *testing.T, url string, id identity) *nats.Conn {
	t.Helper()
	path := credsPath(t)
	contents, _, _ := id.credentials(t)
	writeFile(t, path, contents)
	nc, err := nats.Connect(url, nats.UserCredentials(path), nats.NoReconnect())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	return nc
}

func TestListenerPlainURLDoesNotReadCAFile(t *testing.T) {
	srv := runServer(t, plainServerOptions(server.RANDOM_PORT))
	path := credsPath(t)
	writeCredentials(t, path)
	missingCA := filepath.Join(t.TempDir(), "missing-ca.pem")
	l, logs := listen(t, ListenerConfig{URL: srv.ClientURL(), CredsPath: path, CAFile: missingCA})
	waitFor(t, "connected", l.Connected)
	if got := logged(t, logs, "WARN", caMessage); len(got) != 0 {
		t.Fatalf("nats:// read the CA file: %+v", got)
	}
	if l.conn.Opts.RootCAsCB != nil || l.conn.Opts.Secure {
		t.Fatal("nats:// was configured for TLS")
	}
	wantStatus(t, request(t, connect(t, srv.ClientURL()), "inference.v2.health.live", nil, nil), 200)
}

func TestListenerTLSTrustsCAFile(t *testing.T) {
	files := newTLSFiles(t)
	srv := runServer(t, files.serverOptions())
	url := fmt.Sprintf("tls://127.0.0.1:%d", serverPort(t, srv))
	caPath := filepath.Join(t.TempDir(), "ca.pem")
	writeFile(t, caPath, files.caPEM)
	path := credsPath(t)
	userJWT, seed := writeCredentials(t, path)

	l, logs := listen(t, ListenerConfig{URL: url, CredsPath: path, CAFile: caPath})
	waitFor(t, "connected over TLS", l.Connected)
	if state, err := l.conn.TLSConnectionState(); err != nil || !state.HandshakeComplete {
		t.Fatalf("connection is not TLS: %v", err)
	}

	client, err := nats.Connect(url, nats.RootCAs(caPath), nats.NoReconnect())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	wantStatus(t, request(t, client, "inference.v2.health.live", nil, nil), 200)
	assertLogOmits(t, logs, string(files.caPEM), "BEGIN CERTIFICATE", userJWT, seed)
}

func TestListenerTLSRetriesAMissingCAFile(t *testing.T) {
	files := newTLSFiles(t)
	srv := runServer(t, files.serverOptions())
	url := fmt.Sprintf("tls://127.0.0.1:%d", serverPort(t, srv))
	caPath := filepath.Join(t.TempDir(), "ca.pem")
	path := credsPath(t)
	writeCredentials(t, path)

	l, logs := listen(t, ListenerConfig{URL: url, CredsPath: path, CAFile: caPath})
	waitFor(t, "three missing-CA attempts", func() bool { return len(logged(t, logs, "WARN", caMessage)) >= 3 })
	if l.Connected() || l.conn.IsClosed() {
		t.Fatalf("connected %t closed %t without a CA file", l.Connected(), l.conn.IsClosed())
	}

	writeFile(t, caPath, files.caPEM)
	waitFor(t, "connected once the CA file appeared", l.Connected)
	waitFor(t, "connected log", func() bool { return len(logged(t, logs, "INFO", connectedMessage)) == 1 })
	assertLogOmits(t, logs, string(files.caPEM), "BEGIN CERTIFICATE")
}

func TestListenerTLSWithoutCAFileUsesSystemTrust(t *testing.T) {
	files := newTLSFiles(t)
	srv := runServer(t, files.serverOptions())
	url := fmt.Sprintf("tls://127.0.0.1:%d", serverPort(t, srv))
	path := credsPath(t)
	writeCredentials(t, path)

	l, logs := listen(t, ListenerConfig{URL: url, CredsPath: path})
	waitFor(t, "an attempt the system trust store refused", func() bool {
		for _, line := range logged(t, logs, "WARN", retryMessage) {
			if strings.Contains(line.Error, "certificate") {
				return true
			}
		}
		return false
	})
	if l.Connected() || l.conn.Opts.RootCAsCB != nil {
		t.Fatalf("connected %t to a server the system does not trust", l.Connected())
	}
}

func TestListenerRestoresQueueGroupAfterServerRestart(t *testing.T) {
	srv := runServer(t, plainServerOptions(server.RANDOM_PORT))
	port := serverPort(t, srv)
	url := srv.ClientURL()
	path := credsPath(t)
	writeCredentials(t, path)

	logs := &syncBuffer{}
	members := make([]*Listener, 2)
	for i := range members {
		l, err := Dial(ListenerConfig{
			URL:             url,
			CredsPath:       path,
			Handler:         NewHandler(Config{}),
			Logger:          debugLogger(logs),
			reconnectWait:   shortReconnectWait,
			reconnectJitter: time.Millisecond,
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(l.Close)
		members[i] = l
	}
	bothConnected := func() bool { return members[0].Connected() && members[1].Connected() }
	waitFor(t, "both members connected", bothConnected)
	assertOneReply(t, url, "inference.v2.health.live", 200)

	srv.Shutdown()
	srv.WaitForShutdown()
	waitFor(t, "both members disconnected", func() bool { return !members[0].Connected() && !members[1].Connected() })
	waitFor(t, "two disconnect logs", func() bool { return len(logged(t, logs, "INFO", disconnectMessage)) == 2 })

	runServer(t, plainServerOptions(port))
	waitFor(t, "both members reconnected", bothConnected)
	waitFor(t, "four connected logs", func() bool { return len(logged(t, logs, "INFO", connectedMessage)) == 4 })
	assertOneReply(t, url, "inference.v2.health.live", 200)
	assertOneReply(t, url, "inference.v2.models.add.infer", 404)
}

// assertOneReply sends one request and checks that exactly one reply stream comes back.
func assertOneReply(t *testing.T, url, subject string, status int) {
	t.Helper()
	nc := connect(t, url)
	inbox := nc.NewInbox()
	sub, err := nc.SubscribeSync(inbox)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sub.Unsubscribe() }()
	got := readStream(t, sub, func() error {
		return nc.PublishMsg(&nats.Msg{Subject: subject, Reply: inbox, Data: addJSONBody("restored")})
	})
	wantStatus(t, got, status)
	extra, err := sub.NextMsg(300 * time.Millisecond)
	if !errors.Is(err, nats.ErrTimeout) {
		t.Fatalf("a second reply arrived on %s: %v %v", subject, extra, err)
	}
}

func TestRedactURLDropsUserInfo(t *testing.T) {
	got := redactURL("nats://user:secret@leaf:4222, tls://token-5a1c@other:4443")
	if strings.Contains(got, "secret") || strings.Contains(got, "token-5a1c") || strings.Contains(got, "user") {
		t.Fatalf("redacted %q", got)
	}
	if !strings.Contains(got, "leaf:4222") || !strings.Contains(got, "other:4443") {
		t.Fatalf("redacted %q", got)
	}
}

func TestSecureURLFollowsScheme(t *testing.T) {
	for raw, want := range map[string]bool{
		"nats://leaf:4222":                  false,
		"tls://leaf:4222":                   true,
		"TLS://leaf:4222":                   true,
		"nats://a:4222, tls://b:4222":       true,
		"leaf:4222":                         false,
		"nats://leaf:4222,nats://other:422": false,
	} {
		if got := secureURL(raw); got != want {
			t.Errorf("%q secure %t, want %t", raw, got, want)
		}
	}
}
