package metrics

import (
	"testing"
	"time"
)

func TestCountExamples(t *testing.T) {
	const model, version = "add", "1"
	one := func(size uint64) Request {
		return Request{Success: true, BatchSize: size}
	}
	separately := func(sizes ...uint64) []Execution {
		out := make([]Execution, 0, len(sizes))
		for _, size := range sizes {
			out = append(out, ran(model, version, one(size)))
		}
		return out
	}
	cases := []struct {
		name    string
		records []Execution
		success float64
		count   float64
		execs   float64
	}{
		{name: "single batch of 1", records: separately(1), success: 1, count: 1, execs: 1},
		{name: "single batch of 8", records: separately(8), success: 1, count: 8, execs: 1},
		{name: "batch 1 and batch 8 run separately", records: separately(1, 8), success: 2, count: 9, execs: 2},
		{name: "two batch-1 requests share a run", records: []Execution{ran(model, version, one(1), one(1))}, success: 2, count: 2, execs: 1},
		{name: "batch 1 and batch 8 share a run", records: []Execution{ran(model, version, one(1), one(8))}, success: 2, count: 9, execs: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg := New()
			for _, ex := range tc.records {
				reg.Record(ex)
			}
			_, samples := parseBody(t, scrape(t, reg))
			want := labels(model, version)
			requireValue(t, samples, nameSuccess, want, tc.success)
			requireValue(t, samples, nameCount, want, tc.count)
			requireValue(t, samples, nameExec, want, tc.execs)
		})
	}
}

func TestFailedRunDoesNotAddBatchSize(t *testing.T) {
	reg := New()
	reg.Record(Execution{
		Model: "add", Version: "1", Ran: true, Compute: 50 * time.Microsecond,
		Requests: []Request{
			{Success: false, Reason: ReasonBackend, BatchSize: 8, Elapsed: 80 * time.Microsecond, Queued: 10 * time.Microsecond},
			{Success: false, Reason: ReasonBackend, BatchSize: 1, Elapsed: 80 * time.Microsecond, Queued: 20 * time.Microsecond},
		},
	})
	_, samples := parseBody(t, scrape(t, reg))
	want := labels("add", "1")
	requireAbsent(t, samples, nameSuccess, want)
	requireAbsent(t, samples, nameCount, want)
	requireValue(t, samples, nameExec, want, 1)
	requireValue(t, samples, nameFailure, failureLabels("add", "1", ReasonBackend), 2)
	requireValue(t, samples, nameCompute, want, 100)
	requireValue(t, samples, nameElapsed, want, 160)
	requireValue(t, samples, nameWaiting, want, 30)
}

func TestRequestThatNeverRuns(t *testing.T) {
	reg := New()
	reg.Record(Execution{
		Model: "add", Version: "1", Ran: false,
		Requests: []Request{{
			Success:   false,
			Reason:    ReasonRejected,
			BatchSize: 8,
			Elapsed:   3 * time.Millisecond,
			Queued:    3 * time.Millisecond,
		}},
	})
	_, samples := parseBody(t, scrape(t, reg))
	want := labels("add", "1")
	requireAbsent(t, samples, nameSuccess, want)
	requireAbsent(t, samples, nameCount, want)
	requireAbsent(t, samples, nameExec, want)
	requireValue(t, samples, nameFailure, failureLabels("add", "1", ReasonRejected), 1)
	requireValue(t, samples, nameElapsed, want, 3000)
	requireValue(t, samples, nameWaiting, want, 3000)
	requireValue(t, samples, nameCompute, want, 0)
}

func TestCancelAndDeadlineDuringRun(t *testing.T) {
	cases := []struct {
		name   string
		reason Reason
	}{
		{name: "cancel during run", reason: ReasonCanceled},
		{name: "deadline during run", reason: ReasonOther},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg := New()
			reg.Record(Execution{
				Model: "add", Version: "1", Ran: true, Compute: time.Millisecond,
				Requests: []Request{{Success: false, Reason: tc.reason, BatchSize: 4}},
			})
			_, samples := parseBody(t, scrape(t, reg))
			want := labels("add", "1")
			requireValue(t, samples, nameExec, want, 1)
			requireAbsent(t, samples, nameCount, want)
			requireAbsent(t, samples, nameSuccess, want)
			requireValue(t, samples, nameFailure, failureLabels("add", "1", tc.reason), 1)
			requireValue(t, samples, nameCompute, want, 1000)
		})
	}
}

func TestDurations(t *testing.T) {
	reg := New()
	reg.Record(Execution{
		Model: "add", Version: "1", Ran: true, Compute: 4 * time.Millisecond,
		Requests: []Request{
			{Success: true, BatchSize: 1, Elapsed: 5 * time.Millisecond, Queued: time.Millisecond},
			{Success: true, BatchSize: 8, Elapsed: 5 * time.Millisecond, Queued: 2 * time.Millisecond},
		},
	})
	body := scrape(t, reg)
	_, samples := parseBody(t, body)
	want := labels("add", "1")
	requireValue(t, samples, nameSuccess, want, 2)
	requireValue(t, samples, nameElapsed, want, 10000)
	requireValue(t, samples, nameWaiting, want, 3000)
	requireValue(t, samples, nameCompute, want, 8000)
	assertNoForbiddenNames(t, body)
}
