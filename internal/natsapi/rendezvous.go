package natsapi

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/eclipse-iofog/ai-inference-onnx/internal/batch"
)

const (
	// HeaderSuffixLength is the exact byte length of the binary suffix that follows a rendezvous header.
	HeaderSuffixLength = "Inference-Suffix-Length"
	// HeaderAccept is the subject the slices of an accepted upload are sent to.
	HeaderAccept = "Inference-Accept"
	// HeaderID names the upload a slice belongs to.
	HeaderID = "Inference-Id"

	transferRendezvous = "rendezvous"

	// uploadPrefix is outside the model subjects, so the queue group never takes a slice from the replica that accepted the header.
	uploadPrefix = "inference.xfer."
	// uploadStall is how long an upload waits for its next slice before it is abandoned.
	uploadStall = 5 * time.Second

	suffixLengthInvalid  = "Inference-Suffix-Length must be one non-negative decimal integer"
	headerLengthMismatch = "Inference-Header-Content-Length must be the length of the header message"
	uploadUnsupported    = "connection cannot receive an upload"
	sliceIDInvalid       = "Inference-Id does not name this upload"
	sliceEmpty           = "upload slice is empty"
	sliceTooLong         = "upload slice passes Inference-Suffix-Length"
	sliceOverlap         = "upload slice arrived before the previous slice was answered"
	uploadFinished       = "upload is already finished"
	uploadStalled        = "upload stalled before its last slice"
	uploadClosed         = "connection closed during the upload"
	uploadCanceled       = "server is shutting down"
)

// subscriber opens the subject an upload's slices arrive on and reports when the connection drops.
// A NATS connection satisfies it.
type subscriber interface {
	Subscribe(subject string, cb nats.MsgHandler) (*nats.Subscription, error)
	StatusChanged(statuses ...nats.Status) chan nats.Status
	RemoveStatusListener(ch chan nats.Status)
}

// rendezvous accepts a JSON header whose binary suffix follows as ordered slices on a subject only this replica reads.
// The header and the suffix are joined in one pooled buffer and decoded as one HTTP binary-tensor request.
// A declared length of zero is a JSON request in the header message.
func rendezvous(h *Handler, conn publisher, msg *nats.Msg, sub Subject, started time.Time) {
	length, err := suffixLength(msg.Header)
	if err != nil {
		h.refuseInfer(conn, msg, sub, started, err)
		return
	}
	if length == 0 {
		h.inline(conn, msg, sub, started)
		return
	}
	limit := max(h.cfg.MaxPayloadBytes, 0)
	if length > limit || len(msg.Data) > limit-length {
		h.refuseInfer(conn, msg, sub, started, &batch.InvalidError{Reason: payloadTooLarge})
		return
	}
	dir, chosen, err := batch.Select(h.directories(), sub.Model, sub.Version)
	if err != nil {
		h.refuseInfer(conn, msg, sub, started, err)
		return
	}
	headerLength, err := inferenceHeaderLength(msg.Header)
	if err == nil && headerLength != batch.NoHeaderLength && headerLength != int64(len(msg.Data)) {
		err = &batch.InvalidError{Reason: headerLengthMismatch}
	}
	if err != nil {
		h.refuseInfer(conn, msg, sub, started, err)
		return
	}
	subs, ok := conn.(subscriber)
	if !ok {
		h.refuseInfer(conn, msg, sub, started, errors.New(uploadUnsupported))
		return
	}
	id, err := uploadID()
	if err != nil {
		h.refuseInfer(conn, msg, sub, started, err)
		return
	}
	body, err := h.buffers().Get(len(msg.Data) + length)
	if err != nil {
		h.refuseInfer(conn, msg, sub, started, fmt.Errorf("reserve upload buffer: %w", err))
		return
	}
	u := &upload{
		h:       h,
		conn:    conn,
		id:      id,
		dir:     dir,
		chosen:  chosen,
		model:   sub.Model,
		started: started,
		body:    body,
		header:  int64(len(msg.Data)),
		filled:  copy(body, msg.Data),
		slices:  make(chan *nats.Msg, 1),
	}
	// Watching before subscribing means a drop after the subscribe is always seen.
	lost := subs.StatusChanged(nats.DISCONNECTED, nats.CLOSED)
	defer subs.RemoveStatusListener(lost)
	u.sub, err = subs.Subscribe(uploadPrefix+id, u.receive)
	if err != nil {
		h.buffers().Put(body)
		h.refuseInfer(conn, msg, sub, started, fmt.Errorf("open upload subject: %w", err))
		return
	}
	u.wait(msg, lost)
}

// upload is one accepted rendezvous. Its slices are appended in arrival order, one at a time.
type upload struct {
	h       *Handler
	conn    publisher
	id      string
	dir     batch.Directory
	chosen  batch.ModelVersion
	model   string
	started time.Time
	sub     *nats.Subscription

	// body is the JSON header followed by room for the suffix. It belongs to the waiting goroutine.
	body   []byte
	header int64
	filled int

	// slices hands one slice at a time from the subscription to the waiting goroutine.
	slices chan *nats.Msg

	mu sync.Mutex
	// busy is set from the arrival of a slice until the waiting goroutine has taken it and is about to answer it.
	busy bool
	// failed records that a slice arrived while another was busy.
	failed bool
	// ended is set once the upload is finishing or abandoned. Later slices are refused.
	ended bool
}

// wait answers the header with the upload subject, then takes slices until the upload finishes or is abandoned.
// A dropped connection abandons the upload, because a slice sent while it was down is lost.
func (u *upload) wait(accept *nats.Msg, lost <-chan nats.Status) {
	timer := u.h.after()(uploadStall)
	u.h.send(u.conn, accept, http.StatusOK, nats.Header{HeaderAccept: []string{uploadPrefix + u.id}}, nil)
	for {
		select {
		case m := <-u.slices:
			next, done := u.take(m)
			if done {
				return
			}
			timer = next
		case <-timer:
			u.abort(nil, uploadStalled)
			return
		case <-lost:
			u.abort(nil, uploadClosed)
			return
		case <-u.h.cfg.Context.Done():
			u.abort(nil, uploadCanceled)
			return
		}
	}
}

// receive runs on the subscription. A slice that arrives while another is busy, or after the upload ended, is refused.
func (u *upload) receive(m *nats.Msg) {
	if m.Reply == "" {
		return
	}
	u.mu.Lock()
	switch {
	case u.ended:
		u.mu.Unlock()
		u.h.writeError(u.conn, m, &batch.InvalidError{Reason: uploadFinished})
	case u.busy:
		u.failed = true
		u.mu.Unlock()
		u.h.writeError(u.conn, m, &batch.InvalidError{Reason: sliceOverlap})
	default:
		u.busy = true
		u.slices <- m
		u.mu.Unlock()
	}
}

// take appends one slice. It returns the stall timer for the next slice, or done when the upload has ended.
func (u *upload) take(m *nats.Msg) (next <-chan time.Time, done bool) {
	if ids := m.Header.Values(HeaderID); len(ids) != 1 || ids[0] != u.id {
		u.abort(m, sliceIDInvalid)
		return nil, true
	}
	if len(m.Data) == 0 {
		u.abort(m, sliceEmpty)
		return nil, true
	}
	if len(m.Data) > len(u.body)-u.filled {
		u.abort(m, sliceTooLong)
		return nil, true
	}
	u.filled += copy(u.body[u.filled:], m.Data)
	if u.filled < len(u.body) {
		next = u.h.after()(uploadStall)
		if !u.release() {
			u.abort(m, sliceOverlap)
			return nil, true
		}
		u.h.writeEmpty(u.conn, m, http.StatusOK)
		return next, false
	}
	if !u.end() {
		u.abort(m, sliceOverlap)
		return nil, true
	}
	u.finish(m)
	return nil, true
}

// release lets the next slice in. It reports false when a slice overlapped the one just taken.
func (u *upload) release() bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.busy = false
	return !u.failed
}

// end refuses later slices. It reports false when a slice overlapped the last one.
func (u *upload) end() bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.failed {
		return false
	}
	u.ended = true
	return true
}

// finish runs the joined body and answers the last slice with the infer result.
func (u *upload) finish(last *nats.Msg) {
	_ = u.sub.Unsubscribe()
	defer u.h.buffers().Put(u.body)
	encoded, id, err := u.h.execute(u.dir, u.chosen, u.body, u.header)
	u.h.logInfer(u.model, u.chosen.Version, id, u.started, err)
	if err != nil {
		u.h.writeError(u.conn, last, err)
		return
	}
	u.h.writeInfer(u.conn, last, encoded)
}

// abort abandons the upload, returns its buffer, and answers the current slice and any slice still waiting with 400.
func (u *upload) abort(current *nats.Msg, reason string) {
	u.mu.Lock()
	u.ended = true
	waiting := []*nats.Msg{current}
	select {
	case m := <-u.slices:
		waiting = append(waiting, m)
	default:
	}
	u.mu.Unlock()

	_ = u.sub.Unsubscribe()
	u.h.buffers().Put(u.body)
	err := &batch.InvalidError{Reason: reason}
	u.h.logInfer(u.model, u.chosen.Version, "", u.started, err)
	for _, m := range waiting {
		if m != nil {
			u.h.writeError(u.conn, m, err)
		}
	}
}

// suffixLength reads Inference-Suffix-Length. It must be present once and be decimal digits only.
func suffixLength(header nats.Header) (int, error) {
	invalid := &batch.InvalidError{Reason: suffixLengthInvalid}
	values := header.Values(HeaderSuffixLength)
	if len(values) != 1 || values[0] == "" {
		return 0, invalid
	}
	for _, c := range []byte(values[0]) {
		if c < '0' || c > '9' {
			return 0, invalid
		}
	}
	n, err := strconv.Atoi(values[0])
	if err != nil {
		return 0, invalid
	}
	return n, nil
}

// uploadID is 32 hex characters from 16 random bytes, so it is one subject token.
func uploadID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("upload id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

func (h *Handler) buffers() batch.Pool {
	if h.cfg.Buffers == nil {
		return heapBuffers{}
	}
	return h.cfg.Buffers
}

func (h *Handler) after() func(time.Duration) <-chan time.Time {
	if h.cfg.After == nil {
		return time.After
	}
	return h.cfg.After
}

// heapBuffers allocates each upload body and lets the collector reclaim it.
type heapBuffers struct{}

func (heapBuffers) Get(n int) ([]byte, error) { return make([]byte, n), nil }

func (heapBuffers) Put([]byte) {}
