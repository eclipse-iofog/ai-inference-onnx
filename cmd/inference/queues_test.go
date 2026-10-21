package main

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/eclipse-iofog/ai-inference-onnx/internal/batch"
)

func TestOneQueuePerVersion(t *testing.T) {
	t.Parallel()
	started := make(chan string, 2)
	release := make(chan struct{})
	queues := &inferQueues{
		capacity: 4,
		pool:     memPool{},
		slot: func(_, version string) batch.Slot {
			return func(context.Context, []batch.Tensor) ([]batch.Tensor, func(), error) {
				started <- version
				if version == "1" {
					<-release
				}
				return []batch.Tensor{{Name: "C", Shape: []int64{1}, Data: []byte{0, 0, 0, 0}}}, nil, nil
			}
		},
		settings: func(_, _ string) (int, time.Duration, bool) {
			return 0, 0, true
		},
	}
	var once sync.Once
	unlock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unlock)

	firstDone := make(chan error, 1)
	go func() {
		_, err := queues.submit(context.Background(), "add", "1", &batch.Request{})
		firstDone <- err
	}()
	select {
	case version := <-started:
		if version != "1" {
			t.Fatalf("first version %s", version)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("version 1 did not start")
	}

	secondDone := make(chan error, 1)
	go func() {
		_, err := queues.submit(context.Background(), "add", "2", &batch.Request{})
		secondDone <- err
	}()
	select {
	case err := <-secondDone:
		if err != nil {
			t.Fatalf("version 2: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("version 2 waited on version 1")
	}
	select {
	case version := <-started:
		if version != "2" {
			t.Fatalf("second version %s", version)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("version 2 did not start")
	}

	unlock()
	select {
	case err := <-firstDone:
		if err != nil {
			t.Fatalf("version 1: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("version 1 did not finish")
	}
}

type memPool struct{}

func (memPool) Get(n int) ([]byte, error) { return make([]byte, n), nil }

func (memPool) Put([]byte) {}
