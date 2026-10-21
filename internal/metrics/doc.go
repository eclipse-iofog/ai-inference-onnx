// Package metrics collects inference counters and writes them as Prometheus text and one JSON snapshot.
// One registry feeds both. Collecting metrics does not open a port and does not dial.
package metrics
