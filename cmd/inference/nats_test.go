package main

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"
)

const natsReplyWait = 5 * time.Second

func TestNATSPartialSettingsDoNotDial(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		nats func(url, creds string) map[string]string
	}{
		{name: "server url only", nats: func(url, _ string) map[string]string {
			return map[string]string{"NATS_SERVER_URL": url}
		}},
		{name: "credentials only", nats: func(_, creds string) map[string]string {
			return map[string]string{"NATS_CREDS_PATH": creds}
		}},
		{name: "empty credentials", nats: func(url, _ string) map[string]string {
			return map[string]string{"NATS_SERVER_URL": url, "NATS_CREDS_PATH": "  "}
		}},
		{name: "empty server url", nats: func(_, creds string) map[string]string {
			return map[string]string{"NATS_SERVER_URL": "", "NATS_CREDS_PATH": creds}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			addr, accepted := countingListener(t)
			path, _, _ := writeProcessCredentials(t)
			env := tc.nats("nats://"+addr, path)
			env["AUTH_MODE"] = "disabled"
			ports := pickPorts(t)
			proc := startProcess(t, processEnv(t, ports, env))
			t.Cleanup(proc.kill)

			waitLive(t, ports.http)
			assertGET(t, ports.http, "/v2/health/ready", http.StatusBadRequest)
			assertGRPCHealth(t, ports.grpc, false)
			stdout := waitLog(t, proc, "grpc listener is up")
			time.Sleep(300 * time.Millisecond)
			if n := accepted.Load(); n != 0 {
				t.Fatalf("the process dialed %d times", n)
			}
			for _, line := range decodeLog(t, stdout+proc.stdout.String()) {
				if strings.Contains(strings.ToLower(line.Msg), "nats") {
					t.Fatalf("nats log line %+v", line)
				}
			}
		})
	}
}

func TestNATSRefusedDialKeepsServing(t *testing.T) {
	t.Parallel()
	const token = "nats-refused-token-3f8a1c90d2b7e645"
	path, userJWT, seed := writeProcessCredentials(t)
	ports := pickPorts(t)
	proc := startProcess(t, processEnv(t, ports, map[string]string{
		"AUTH_MODE":         "static",
		"AUTH_STATIC_TOKEN": token,
		"NATS_SERVER_URL":   "nats://" + refusedAddress(t),
		"NATS_CREDS_PATH":   path,
	}))
	t.Cleanup(proc.kill)

	waitLive(t, ports.http)
	stdout := waitLog(t, proc, "nats connection attempt failed, retrying")
	assertGET(t, ports.http, "/v2/health/live", http.StatusOK)
	assertGET(t, ports.http, "/v2/health/ready", http.StatusBadRequest)
	assertGRPCHealth(t, ports.grpc, false)
	assertMetricsOpen(t, ports.metrics)

	var up, retries int
	for _, line := range decodeLog(t, stdout) {
		switch line.Msg {
		case "nats listener is up":
			if line.Level != "INFO" {
				t.Fatalf("listener line %+v", line)
			}
			up++
		case "nats connection attempt failed, retrying":
			if line.Level != "WARN" {
				t.Fatalf("retry line %+v", line)
			}
			retries++
		case "nats connected":
			t.Fatalf("connected to a refused address: %+v", line)
		}
	}
	if up != 1 || retries == 0 {
		t.Fatalf("listener lines %d retry lines %d:\n%s", up, retries, stdout)
	}
	select {
	case <-proc.waited:
		t.Fatalf("process exited\n%s", proc.output())
	default:
	}
	out := proc.output()
	for _, secret := range []string{token, userJWT, seed, "BEGIN NATS"} {
		if strings.Contains(out, secret) {
			t.Fatalf("log contains a secret:\n%s", out)
		}
	}

	if err := proc.signal(syscall.SIGTERM); err != nil {
		t.Fatalf("signal: %v", err)
	}
	if err := proc.waitTimeout(t, 5*time.Second); err != nil {
		t.Fatalf("exit after signal: %v\n%s", err, proc.output())
	}
}

func TestNATSServesEmptyModelSet(t *testing.T) {
	t.Parallel()
	srv := startNATSServer(t)
	url := srv.ClientURL()
	path, _, _ := writeProcessCredentials(t)
	ports := pickPorts(t)
	proc := startProcess(t, processEnv(t, ports, map[string]string{
		"AUTH_MODE":        "disabled",
		"NATS_SERVER_URL":  url,
		"NATS_CREDS_PATH":  path,
		"ENGINE_SERVER_ID": "engine-1",
		"METRICS_INTERVAL": "100ms",
	}))
	t.Cleanup(proc.kill)
	waitLive(t, ports.http)
	waitLog(t, proc, "nats connected")
	waitClients(t, srv, 1)

	nc, err := nats.Connect(url, nats.NoReconnect())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	if got := srv.NumClients(); got != 2 {
		t.Fatalf("clients %d", got)
	}
	snapshots, err := nc.SubscribeSync("inference.metrics.engines.>")
	if err != nil {
		t.Fatal(err)
	}
	if err := nc.Flush(); err != nil {
		t.Fatal(err)
	}

	if status, body := natsRequest(t, nc, "inference.v2.health.live", nil); status != http.StatusOK || len(body) != 0 {
		t.Fatalf("nats live %d %q", status, body)
	}
	if status, body := natsRequest(t, nc, "inference.v2.health.ready", nil); status != http.StatusBadRequest || len(body) != 0 {
		t.Fatalf("nats ready %d %q", status, body)
	}
	status, body := natsRequest(t, nc, "inference.v2.models.add.infer", []byte(`{"inputs":[]}`))
	if status != http.StatusNotFound || string(body) != `{"error":"model \"add\" was not found"}` {
		t.Fatalf("nats infer %d %s", status, body)
	}
	if _, err := nc.Request("inference.v2.repository.models.add.load", nil, time.Second); !errors.Is(err, nats.ErrNoResponders) {
		t.Fatalf("nats load: %v", err)
	}

	assertGET(t, ports.http, "/v2/health/live", http.StatusOK)
	status, body = post(t, ports.http, "/v2/repository/models/add/load", "")
	if status != http.StatusNotImplemented {
		t.Fatalf("load %d %s", status, body)
	}
	assertMetricsOpen(t, ports.metrics)
	msg, err := snapshots.NextMsg(2 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if got := srv.NumClients(); got != 2 {
		t.Fatalf("publish opened another connection: %d clients", got)
	}
	assertProcessSnapshot(t, msg, ports, false)
	if srv.JetStreamEnabled() {
		t.Fatal("jetstream is enabled")
	}
	status, body = get(t, ports.metrics, "/metrics")
	if status != http.StatusOK || strings.Contains(string(body), "inference_request_success{") {
		t.Fatalf("scrape %d %s", status, body)
	}
}

func TestSnapshotCarriesPlatformNames(t *testing.T) {
	t.Parallel()
	srv := startNATSServer(t)
	url := srv.ClientURL()
	path, _, _ := writeProcessCredentials(t)
	ports := pickPorts(t)
	proc := startProcess(t, processEnv(t, ports, map[string]string{
		"AUTH_MODE":                 "disabled",
		"NATS_SERVER_URL":           url,
		"NATS_CREDS_PATH":           path,
		"ENGINE_SERVER_ID":          "engine-1",
		"METRICS_INTERVAL":          "100ms",
		"EDGELET_MICROSERVICE_NAME": "vision",
		"EDGELET_NODE_UID":          "node-1",
		"EDGELET_APPLICATION_NAME":  "factory",
	}))
	t.Cleanup(proc.kill)
	waitLive(t, ports.http)
	waitLog(t, proc, "nats connected")
	waitClients(t, srv, 1)

	nc, err := nats.Connect(url, nats.NoReconnect())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	snapshots, err := nc.SubscribeSync("inference.metrics.engines.>")
	if err != nil {
		t.Fatal(err)
	}
	if err := nc.Flush(); err != nil {
		t.Fatal(err)
	}
	msg, err := snapshots.NextMsg(2 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if got := srv.NumClients(); got != 2 {
		t.Fatalf("publish opened another connection: %d clients", got)
	}
	assertProcessSnapshot(t, msg, ports, true)
}

// countingListener accepts connections on a local port and counts them.
func countingListener(t *testing.T) (string, *atomic.Int32) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	var accepted atomic.Int32
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			accepted.Add(1)
			_ = conn.Close()
		}
	}()
	return ln.Addr().String(), &accepted
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

func startNATSServer(t *testing.T) *server.Server {
	t.Helper()
	srv, err := server.NewServer(&server.Options{Host: "127.0.0.1", Port: server.RANDOM_PORT, NoLog: true, NoSigs: true})
	if err != nil {
		t.Fatal(err)
	}
	srv.Start()
	t.Cleanup(srv.Shutdown)
	if !srv.ReadyForConnections(natsReplyWait) {
		t.Fatal("embedded NATS server did not start")
	}
	return srv
}

func waitClients(t *testing.T, srv *server.Server, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		got := srv.NumClients()
		if got > want {
			t.Fatalf("clients %d", got)
		}
		if got == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("clients %d", srv.NumClients())
}

func assertProcessSnapshot(t *testing.T, msg *nats.Msg, ports listenPorts, withNames bool) {
	t.Helper()
	if msg.Subject != "inference.metrics.engines.engine-1" {
		t.Fatalf("subject %s", msg.Subject)
	}
	if msg.Reply != "" {
		t.Fatalf("reply %q", msg.Reply)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(msg.Data, &raw); err != nil {
		t.Fatalf("decode %s: %v", msg.Data, err)
	}
	if string(raw["schema"]) != "1" {
		t.Fatalf("schema %s", raw["schema"])
	}
	if string(raw["server_id"]) != `"engine-1"` || string(raw["models"]) != "[]" {
		t.Fatalf("identity %s models %s", raw["server_id"], raw["models"])
	}
	if string(raw["live"]) != "true" || string(raw["nats_connected"]) != "true" {
		t.Fatalf("live %s nats %s", raw["live"], raw["nats_connected"])
	}
	live, _ := get(t, ports.http, "/v2/health/live")
	ready, _ := get(t, ports.http, "/v2/health/ready")
	wantReady := "false"
	if ready == http.StatusOK {
		wantReady = "true"
	}
	if live != http.StatusOK || string(raw["ready"]) != wantReady {
		t.Fatalf("http live %d ready %d snapshot ready %s", live, ready, raw["ready"])
	}
	for _, key := range []string{"inputs", "outputs", "process_resident_memory_bytes"} {
		if _, ok := raw[key]; ok {
			t.Fatalf("field %s is present", key)
		}
	}
	names := map[string]string{
		"microservice_name": `"vision"`,
		"node_uid":          `"node-1"`,
		"application_name":  `"factory"`,
	}
	for key, want := range names {
		got, ok := raw[key]
		if withNames {
			if !ok || string(got) != want {
				t.Fatalf("%s %s", key, got)
			}
			continue
		}
		if ok {
			t.Fatalf("unset %s is present", key)
		}
	}
}

// writeProcessCredentials mints a user credentials file in a temp directory and returns its path, JWT, and seed.
func writeProcessCredentials(t *testing.T) (path, userJWT, seed string) {
	t.Helper()
	account, err := nkeys.CreateAccount()
	if err != nil {
		t.Fatal(err)
	}
	user, err := nkeys.CreateUser()
	if err != nil {
		t.Fatal(err)
	}
	userPub, err := user.PublicKey()
	if err != nil {
		t.Fatal(err)
	}
	userJWT, err = jwt.NewUserClaims(userPub).Encode(account)
	if err != nil {
		t.Fatal(err)
	}
	rawSeed, err := user.Seed()
	if err != nil {
		t.Fatal(err)
	}
	contents, err := jwt.FormatUserConfig(userJWT, rawSeed)
	if err != nil {
		t.Fatal(err)
	}
	path = filepath.Join(t.TempDir(), "user.creds")
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	return path, userJWT, string(rawSeed)
}

// natsRequest sends one request and joins the reply stream up to its terminator.
// The status is on the first message. The terminator is the only empty message with no headers.
func natsRequest(t *testing.T, nc *nats.Conn, subject string, body []byte) (int, []byte) {
	t.Helper()
	inbox := nc.NewInbox()
	sub, err := nc.SubscribeSync(inbox)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sub.Unsubscribe() }()
	if err := nc.PublishMsg(&nats.Msg{Subject: subject, Reply: inbox, Data: body}); err != nil {
		t.Fatal(err)
	}
	status := -1
	var joined []byte
	for i := 0; ; i++ {
		msg, err := sub.NextMsg(natsReplyWait)
		if err != nil {
			t.Fatalf("%s reply %d: %v", subject, i, err)
		}
		if i == 0 {
			status, err = strconv.Atoi(msg.Header.Get("Inference-Status"))
			if err != nil {
				t.Fatalf("%s status: %v", subject, err)
			}
		} else if len(msg.Header) == 0 && len(msg.Data) == 0 {
			return status, joined
		}
		joined = append(joined, msg.Data...)
	}
}
