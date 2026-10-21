package natsapi

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/nats-io/nats.go"
)

const (
	// HeaderStatus carries the decimal HTTP status on the first slice of a reply.
	HeaderStatus = "Inference-Status"
	// HeaderContentType is the media type of the reply body, with the same meaning as HTTP.
	HeaderContentType = "Content-Type"
	// HeaderContentLength is the byte length of the JSON document at the start of a binary body.
	HeaderContentLength = "Inference-Header-Content-Length"
	// HeaderTransfer names how a request body arrives. Absent means the body is in the request message.
	HeaderTransfer = "Inference-Transfer"

	contentJSON   = "application/json"
	contentBinary = "application/octet-stream"

	// headerFraming is the fixed part of an encoded NATS header block: the version line and the closing blank line.
	headerFraming = len("NATS/1.0\r\n") + len("\r\n")
	// headerFieldFraming is the colon, space, carriage return, and line feed around one header field.
	headerFieldFraming = 4
)

var errPayloadLimit = errors.New("server payload limit is not positive")

// publisher sends reply messages. A NATS connection satisfies it.
type publisher interface {
	PublishMsg(m *nats.Msg) error
	MaxPayload() int64
}

// writeReply sends one reply stream to subject.
// The first slice carries status as HeaderStatus, the given headers, and the start of body.
// Each later slice has no headers and a non-empty payload. One empty message with no headers ends the stream.
// Every message fits the connected server's payload limit. An empty body is a first slice with headers and no payload.
func writeReply(conn publisher, subject string, status int, header nats.Header, body []byte) error {
	limit := conn.MaxPayload()
	if limit < 1 {
		return errPayloadLimit
	}
	first := make(nats.Header, len(header)+1)
	for key, values := range header {
		first[key] = append([]string(nil), values...)
	}
	first.Set(HeaderStatus, strconv.Itoa(status))
	room := limit - int64(headerBytes(first))
	if room < 0 {
		return fmt.Errorf("reply headers are larger than the server payload limit of %d bytes", limit)
	}
	n := int(min(room, int64(len(body))))
	if err := conn.PublishMsg(&nats.Msg{Subject: subject, Header: first, Data: body[:n]}); err != nil {
		return fmt.Errorf("send reply: %w", err)
	}
	rest := body[n:]
	for len(rest) > 0 {
		n = int(min(limit, int64(len(rest))))
		if err := conn.PublishMsg(&nats.Msg{Subject: subject, Data: rest[:n]}); err != nil {
			return fmt.Errorf("send reply: %w", err)
		}
		rest = rest[n:]
	}
	if err := conn.PublishMsg(&nats.Msg{Subject: subject}); err != nil {
		return fmt.Errorf("end reply: %w", err)
	}
	return nil
}

// headerBytes is the largest size the client can encode header as.
func headerBytes(header nats.Header) int {
	size := headerFraming
	for key, values := range header {
		for _, value := range values {
			size += len(key) + len(value) + headerFieldFraming
		}
	}
	return size
}
