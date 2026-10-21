package main

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/eclipse-iofog/ai-inference-onnx/internal/batch"
)

// inferQueues keeps one queue for each model version.
// The queue uses the batch size and delay recorded for that version.
type inferQueues struct {
	mu           sync.Mutex
	items        map[batch.Key]*versionQueue
	capacity     int
	delayDefault time.Duration
	pool         batch.Pool
	logger       *slog.Logger
	clock        batch.Clock
	slot         func(name, version string) batch.Slot
	admit        func(name, version string) error
	recorder     batch.Recorder
	settings     func(name, version string) (maxBatch int, delay time.Duration, ok bool)
}

type versionQueue struct {
	batcher  *batch.Batcher
	maxBatch int
	delay    time.Duration
}

func (q *inferQueues) submit(ctx context.Context, name, version string, req *batch.Request) (*batch.Reply, error) {
	if q == nil {
		return nil, batch.NotFound(fmt.Sprintf("model %q was not found", name))
	}
	b, err := q.batcher(name, version)
	if err != nil {
		return nil, err
	}
	return b.Infer(ctx, req)
}

func (q *inferQueues) batcher(name, version string) (*batch.Batcher, error) {
	if q.admit != nil {
		if err := q.admit(name, version); err != nil {
			return nil, err
		}
	}
	maxBatch, delay, ok := q.lookup(name, version)
	if !ok {
		return nil, batch.NotFound(fmt.Sprintf("version %q was not found", version))
	}
	key := batch.Key{Model: name, Version: version}
	q.mu.Lock()
	defer q.mu.Unlock()
	if item := q.items[key]; item != nil && item.maxBatch == maxBatch && item.delay == delay {
		return item.batcher, nil
	}
	var slot batch.Slot
	if q.slot != nil {
		slot = q.slot(name, version)
	}
	var admit func() error
	if q.admit != nil {
		admit = func() error { return q.admit(name, version) }
	}
	created, err := batch.New(batch.Config{
		Key:          key,
		MaxBatchSize: maxBatch,
		Delay:        delay,
		Capacity:     q.capacity,
		Admit:        admit,
		Pool:         q.pool,
		Clock:        q.clock,
		Slot:         slot,
		Logger:       q.logger,
		Recorder:     q.recorder,
	})
	if err != nil {
		return nil, err
	}
	if q.items == nil {
		q.items = make(map[batch.Key]*versionQueue)
	}
	q.items[key] = &versionQueue{batcher: created, maxBatch: maxBatch, delay: delay}
	return created, nil
}

func (q *inferQueues) lookup(name, version string) (int, time.Duration, bool) {
	if q.settings == nil {
		return 0, 0, false
	}
	return q.settings(name, version)
}

// delayFromMicroseconds converts a recorded queue delay.
// A negative value stays negative so the queue reports it instead of waiting.
func delayFromMicroseconds(us int) time.Duration {
	if us < 0 {
		return time.Duration(us) * time.Microsecond
	}
	return unsignedDelay(uint64(us))
}

func unsignedDelay(us uint64) time.Duration {
	const maxMicros = uint64(math.MaxInt64 / int64(time.Microsecond))
	if us > maxMicros {
		return time.Duration(math.MaxInt64)
	}
	return time.Duration(us) * time.Microsecond
}
