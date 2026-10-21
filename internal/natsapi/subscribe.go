package natsapi

import (
	"errors"
	"fmt"

	"github.com/nats-io/nats.go"
)

// QueueGroup is the queue group of every request subscription, so one replica answers each request.
const QueueGroup = "inference"

// Subjects are the request subscriptions. A model name may contain dots, so model calls use ">".
// That wildcard does not match the bare model list subject, which is subscribed on its own.
var Subjects = []string{
	"inference.v2.health.live",
	"inference.v2.health.ready",
	"inference.v2.server.metadata",
	"inference.v2.models",
	"inference.v2.models.>",
	"inference.v2.repository.index",
}

// Subscribe joins QueueGroup on every subject in Subjects and answers each request with h.
// When one subscription fails, the ones already made are removed and the error is returned.
func Subscribe(conn *nats.Conn, h *Handler) ([]*nats.Subscription, error) {
	subs := make([]*nats.Subscription, 0, len(Subjects))
	for _, subject := range Subjects {
		sub, err := conn.QueueSubscribe(subject, QueueGroup, func(msg *nats.Msg) {
			h.receive(conn, msg)
		})
		if err != nil {
			err = fmt.Errorf("subscribe %s: %w", subject, err)
			for _, made := range subs {
				err = errors.Join(err, made.Unsubscribe())
			}
			return nil, err
		}
		subs = append(subs, sub)
	}
	return subs, nil
}
