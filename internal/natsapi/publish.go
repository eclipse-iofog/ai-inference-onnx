package natsapi

import (
	"errors"
	"time"

	"github.com/nats-io/nats.go"
)

// publishFlush is how long a snapshot publish waits for the server to accept it.
const publishFlush = 5 * time.Second

// errNotConnected is returned when a publish is attempted while the leaf is down.
var errNotConnected = errors.New("nats is not connected")

// Publish sends body on subject using the connection this listener already opened.
// The message has no reply subject. A nil listener or a down leaf returns an error and sends nothing.
func (l *Listener) Publish(subject string, body []byte) error {
	if l == nil || !l.Connected() {
		return errNotConnected
	}
	msg := &nats.Msg{Subject: subject, Data: body}
	if err := l.conn.PublishMsg(msg); err != nil {
		return err
	}
	return l.conn.FlushTimeout(publishFlush)
}
