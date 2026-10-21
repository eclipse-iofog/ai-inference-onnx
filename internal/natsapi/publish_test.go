package natsapi

import (
	"errors"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
)

func TestPublishUsesTheOpenConnection(t *testing.T) {
	srv := runServer(t, plainServerOptions(server.RANDOM_PORT))
	path := credsPath(t)
	writeCredentials(t, path)
	l, _ := listen(t, ListenerConfig{URL: srv.ClientURL(), CredsPath: path})
	waitFor(t, "connected", l.Connected)
	if got := srv.NumClients(); got != 1 {
		t.Fatalf("clients %d", got)
	}

	const subject = "inference.metrics.engines.engine-1"
	body := []byte(`{"schema":1}`)
	if err := l.Publish(subject, body); err != nil {
		t.Fatal(err)
	}
	if got := srv.NumClients(); got != 1 {
		t.Fatalf("publish opened another connection: %d clients", got)
	}
	if srv.JetStreamEnabled() {
		t.Fatal("jetstream is enabled")
	}
}

func TestPublishReachesEverySubscriber(t *testing.T) {
	srv := runServer(t, plainServerOptions(server.RANDOM_PORT))
	path := credsPath(t)
	writeCredentials(t, path)
	l, _ := listen(t, ListenerConfig{URL: srv.ClientURL(), CredsPath: path})
	waitFor(t, "connected", l.Connected)

	url := srv.ClientURL()
	first := connectClient(t, url)
	second := connectClient(t, url)
	const subject = "inference.metrics.engines.>"
	sub1 := subscribe(t, first, subject)
	sub2 := subscribe(t, second, subject)
	if err := first.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := second.Flush(); err != nil {
		t.Fatal(err)
	}
	before := srv.NumClients()

	body := []byte(`{"schema":1,"models":[]}`)
	if err := l.Publish("inference.metrics.engines.engine-1", body); err != nil {
		t.Fatal(err)
	}
	msg1 := nextMsg(t, sub1)
	msg2 := nextMsg(t, sub2)
	if msg1.Reply != "" || msg2.Reply != "" {
		t.Fatalf("reply %q %q", msg1.Reply, msg2.Reply)
	}
	if string(msg1.Data) != string(body) || string(msg2.Data) != string(body) {
		t.Fatalf("bodies %s %s", msg1.Data, msg2.Data)
	}
	if msg1.Subject != "inference.metrics.engines.engine-1" || msg2.Subject != msg1.Subject {
		t.Fatalf("subjects %s %s", msg1.Subject, msg2.Subject)
	}
	if got := srv.NumClients(); got != before {
		t.Fatalf("clients %d, was %d", got, before)
	}

	late := subscribe(t, first, subject)
	if err := first.Flush(); err != nil {
		t.Fatal(err)
	}
	if _, err := late.NextMsg(200 * time.Millisecond); !errors.Is(err, nats.ErrTimeout) {
		t.Fatalf("late subscriber received a sample: %v", err)
	}
	if _, err := first.Request("$JS.API.INFO", nil, 200*time.Millisecond); !errors.Is(err, nats.ErrNoResponders) && !errors.Is(err, nats.ErrTimeout) {
		t.Fatalf("jetstream info: %v", err)
	}
}

func TestPublishWhileDownSendsNothing(t *testing.T) {
	var listener *Listener
	if err := listener.Publish("inference.metrics.engines.engine-1", []byte(`{"schema":1}`)); !errors.Is(err, errNotConnected) {
		t.Fatalf("nil listener: %v", err)
	}
	l, _ := listen(t, ListenerConfig{URL: "nats://" + refusedAddress(t), CredsPath: credsPath(t)})
	if err := l.Publish("inference.metrics.engines.engine-1", []byte(`{"schema":1}`)); !errors.Is(err, errNotConnected) {
		t.Fatalf("refused listener: %v", err)
	}
}

func connectClient(t *testing.T, url string) *nats.Conn {
	t.Helper()
	nc, err := nats.Connect(url, nats.Name("snapshot-subscriber"), nats.NoReconnect())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	return nc
}

func subscribe(t *testing.T, nc *nats.Conn, subject string) *nats.Subscription {
	t.Helper()
	sub, err := nc.SubscribeSync(subject)
	if err != nil {
		t.Fatal(err)
	}
	return sub
}

func nextMsg(t *testing.T, sub *nats.Subscription) *nats.Msg {
	t.Helper()
	msg, err := sub.NextMsg(time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return msg
}
