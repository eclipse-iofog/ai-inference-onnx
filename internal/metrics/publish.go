package metrics

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
)

// errNoRegistry is returned when a snapshot is requested without a registry.
var errNoRegistry = errors.New("metrics registry is nil")

// PublisherConfig is how one process publishes the snapshot on an interval and after a model state is stored.
type PublisherConfig struct {
	// Registry is the process registry. The snapshot reads it.
	Registry *Registry
	// Interval is how often a snapshot is published. Tests leave this unused and send on Ticks.
	Interval time.Duration
	// ServerID is the resolved id. It is not resolved again.
	ServerID string
	// MicroserviceName is included only when it is set.
	MicroserviceName string
	// NodeUID is included only when it is set.
	NodeUID string
	// ApplicationName is included only when it is set.
	ApplicationName string
	// ExecutionProviders is the provider chain copied into every snapshot, in append order.
	// Empty means the runtime library did not load.
	ExecutionProviders []string
	// ORTVersion is the ONNX Runtime version copied into every snapshot.
	// Empty means the runtime library did not load.
	ORTVersion string
	// Live reports server live.
	Live func() bool
	// Ready reports server ready.
	Ready func() bool
	// Connected reports whether the leaf is up. A false bit publishes nothing.
	Connected func() bool
	// Publish sends one document on the connection that is already open.
	// An error is dropped. The document is not kept.
	Publish func(subject string, body []byte) error
	// Logger records a failed publish. A nil logger records nothing.
	// Further failures stay quiet until a publish succeeds or one interval has passed.
	Logger *slog.Logger
	// Models returns a copy of the current model list for an interval tick.
	Models func() []ModelState
	// Notes carries a copy of the model list after a state is stored.
	Notes <-chan []ModelState
	// Ticks drives an interval in tests. When it is set, Interval is not used.
	Ticks <-chan time.Time
}

// Publisher publishes one snapshot on each interval and again when a model state is stored.
type Publisher struct {
	reg                *Registry
	interval           time.Duration
	serverID           string
	microserviceName   string
	nodeUID            string
	applicationName    string
	executionProviders []string
	ortVersion         string
	live               func() bool
	ready              func() bool
	connected          func() bool
	publish            func(subject string, body []byte) error
	models             func() []ModelState
	notes              <-chan []ModelState
	ticks              <-chan time.Time
	logger             *slog.Logger

	failMu   sync.Mutex
	lastFail time.Time
}

// NewPublisher returns a publisher. Run sends snapshots until the context is canceled.
func NewPublisher(cfg PublisherConfig) *Publisher {
	return &Publisher{
		reg:                cfg.Registry,
		interval:           cfg.Interval,
		serverID:           cfg.ServerID,
		microserviceName:   cfg.MicroserviceName,
		nodeUID:            cfg.NodeUID,
		applicationName:    cfg.ApplicationName,
		executionProviders: append([]string(nil), cfg.ExecutionProviders...),
		ortVersion:         cfg.ORTVersion,
		live:               cfg.Live,
		ready:              cfg.Ready,
		connected:          cfg.Connected,
		publish:            cfg.Publish,
		models:             cfg.Models,
		notes:              cfg.Notes,
		ticks:              cfg.Ticks,
		logger:             cfg.Logger,
	}
}

// Run publishes on each tick and on each stored model list until ctx is canceled.
func (p *Publisher) Run(ctx context.Context) {
	if p == nil || ctx == nil {
		return
	}
	tick := p.ticks
	if tick == nil && p.interval > 0 {
		ticker := time.NewTicker(p.interval)
		defer ticker.Stop()
		tick = ticker.C
	}
	notes := p.notes
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick:
			p.publishTick()
		case models, ok := <-notes:
			if !ok {
				notes = nil
				continue
			}
			p.publishModels(models)
		}
	}
}

func (p *Publisher) publishTick() {
	var models []ModelState
	if p.models != nil {
		models = p.models()
	}
	p.publishModels(models)
}

// publishModels records the model list, then publishes only while the leaf is connected.
// The document is built for that attempt and is not kept when the leaf is down or the publish fails.
func (p *Publisher) publishModels(models []ModelState) {
	if p == nil || p.reg == nil {
		return
	}
	p.reg.Observe(models)
	if p.connected == nil || !p.connected() || p.publish == nil {
		return
	}
	subject := Subject(p.serverID)
	body, err := p.reg.Snapshot(p.meta())
	if err != nil {
		p.notePublish(subject, err)
		return
	}
	// A failed publish is dropped. The next trigger builds a new document.
	if err = p.publish(subject, body); err != nil {
		p.notePublish(subject, err)
		return
	}
	p.notePublish(subject, nil)
}

// notePublish records a failed publish, then stays quiet until the next success or one interval has passed.
// A nil error clears that quiet period. A disconnected leaf does not call this.
func (p *Publisher) notePublish(subject string, err error) {
	if p == nil {
		return
	}
	p.failMu.Lock()
	if err == nil {
		p.lastFail = time.Time{}
		p.failMu.Unlock()
		return
	}
	now := time.Now()
	if !p.lastFail.IsZero() && now.Sub(p.lastFail) < p.interval {
		p.failMu.Unlock()
		return
	}
	p.lastFail = now
	logger := p.logger
	p.failMu.Unlock()
	if logger == nil {
		return
	}
	logger.Warn("metrics", "subject", subject, "error", err)
}

func (p *Publisher) meta() SnapshotMeta {
	meta := SnapshotMeta{
		ServerID:         p.serverID,
		MicroserviceName: p.microserviceName,
		NodeUID:          p.nodeUID,
		ApplicationName:  p.applicationName,
		NATSConnected:    true,
	}
	if p.live != nil {
		meta.Live = p.live()
	}
	if p.ready != nil {
		meta.Ready = p.ready()
	}
	meta.ExecutionProviders = p.executionProviders
	meta.ORTVersion = p.ortVersion
	return meta
}
