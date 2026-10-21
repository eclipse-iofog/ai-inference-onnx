package metrics

import (
	"bytes"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"google.golang.org/protobuf/proto"
)

const (
	nameSuccess = "inference_request_success"
	nameFailure = "inference_request_failure"
	nameCount   = "inference_count"
	nameExec    = "inference_exec_count"
	namePending = "inference_pending_request_count"
	nameElapsed = "inference_request_duration_us"
	nameWaiting = "inference_queue_duration_us"
	nameCompute = "inference_compute_infer_duration_us"
	nameReady   = "inference_model_ready"

	nameResident = "process_resident_memory_bytes"
	helpResident = "Resident memory size in bytes."

	expositionType = "text/plain; version=0.0.4; charset=utf-8"
)

type spec struct {
	name   string
	help   string
	kind   dto.MetricType
	labels []string
}

var (
	labelsModelVersion = []string{"model", "version"}
	labelsFailure      = []string{"model", "version", "reason"}

	inferenceSpecs = []spec{
		{name: nameSuccess, help: "Requests that returned a result.", kind: dto.MetricType_COUNTER, labels: labelsModelVersion},
		{name: nameFailure, help: "Failed requests.", kind: dto.MetricType_COUNTER, labels: labelsFailure},
		{name: nameCount, help: "Sum of batch sizes for requests that returned a result.", kind: dto.MetricType_COUNTER, labels: labelsModelVersion},
		{name: nameExec, help: "Batches that were executed.", kind: dto.MetricType_COUNTER, labels: labelsModelVersion},
		{name: namePending, help: "Requests waiting to run.", kind: dto.MetricType_GAUGE, labels: labelsModelVersion},
		{name: nameElapsed, help: "Cumulative end-to-end time in microseconds.", kind: dto.MetricType_COUNTER, labels: labelsModelVersion},
		{name: nameWaiting, help: "Cumulative time waiting to run, in microseconds.", kind: dto.MetricType_COUNTER, labels: labelsModelVersion},
		{name: nameCompute, help: "Cumulative run time in microseconds.", kind: dto.MetricType_COUNTER, labels: labelsModelVersion},
		{name: nameReady, help: "1 when the model version can run.", kind: dto.MetricType_GAUGE, labels: labelsModelVersion},
	}
)

// Registry holds the inference series and the process series.
// Record, SetPending, SetState, Observe, Remove, Snapshot, and ServeHTTP may run together.
type Registry struct {
	prom     *prometheus.Registry
	success  *prometheus.CounterVec
	failure  *prometheus.CounterVec
	count    *prometheus.CounterVec
	executed *prometheus.CounterVec
	pending  *prometheus.GaugeVec
	elapsed  *prometheus.CounterVec
	waiting  *prometheus.CounterVec
	compute  *prometheus.CounterVec
	ready    *prometheus.GaugeVec

	mu    sync.Mutex
	known map[versionKey]ModelState
}

type versionKey struct {
	model   string
	version string
}

// New returns a registry that names every inference series before any model is observed.
func New() *Registry {
	prom := prometheus.NewRegistry()
	r := &Registry{prom: prom}
	r.success = registerCounter(prom, findSpec(nameSuccess))
	r.failure = registerCounter(prom, findSpec(nameFailure))
	r.count = registerCounter(prom, findSpec(nameCount))
	r.executed = registerCounter(prom, findSpec(nameExec))
	r.pending = registerGauge(prom, findSpec(namePending))
	r.elapsed = registerCounter(prom, findSpec(nameElapsed))
	r.waiting = registerCounter(prom, findSpec(nameWaiting))
	r.compute = registerCounter(prom, findSpec(nameCompute))
	r.ready = registerGauge(prom, findSpec(nameReady))
	prom.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	return r
}

func findSpec(name string) spec {
	for _, s := range inferenceSpecs {
		if s.name == name {
			return s
		}
	}
	panic("unknown series " + name)
}

func registerCounter(prom *prometheus.Registry, s spec) *prometheus.CounterVec {
	c := prometheus.NewCounterVec(prometheus.CounterOpts{Name: s.name, Help: s.help}, s.labels)
	prom.MustRegister(c)
	return c
}

func registerGauge(prom *prometheus.Registry, s spec) *prometheus.GaugeVec {
	g := prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: s.name, Help: s.help}, s.labels)
	prom.MustRegister(g)
	return g
}

// SetPending sets how many requests are waiting to run for that version.
// The batch being executed is not included. Zero keeps the sample.
func (r *Registry) SetPending(model, version string, waiting uint64) {
	r.pending.WithLabelValues(model, version).Set(float64(waiting))
}

// SetState sets whether that version can run.
// Ready is 1. Loading and unavailable are 0. Counters already recorded stay.
func (r *Registry) SetState(model, version string, state State) {
	value := 0.0
	if state == StateReady {
		value = 1
	}
	r.ready.WithLabelValues(model, version).Set(value)
}

// Remove drops every sample for that version.
func (r *Registry) Remove(model, version string) {
	r.success.DeleteLabelValues(model, version)
	r.count.DeleteLabelValues(model, version)
	r.executed.DeleteLabelValues(model, version)
	r.pending.DeleteLabelValues(model, version)
	r.elapsed.DeleteLabelValues(model, version)
	r.waiting.DeleteLabelValues(model, version)
	r.compute.DeleteLabelValues(model, version)
	r.ready.DeleteLabelValues(model, version)
	for _, reason := range []Reason{ReasonRejected, ReasonCanceled, ReasonBackend, ReasonOther} {
		r.failure.DeleteLabelValues(model, version, string(reason))
	}
}

// ServeHTTP writes the current exposition text.
func (r *Registry) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	body, err := r.expose()
	if err != nil {
		http.Error(w, "metrics unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", expositionType)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func (r *Registry) expose() ([]byte, error) {
	families, err := r.prom.Gather()
	if err != nil && len(families) == 0 {
		return nil, fmt.Errorf("collect metrics: %w", err)
	}
	byName := make(map[string]*dto.MetricFamily, len(families)+len(inferenceSpecs)+1)
	names := make([]string, 0, len(families)+len(inferenceSpecs)+1)
	for _, family := range families {
		name := family.GetName()
		if name == "" {
			continue
		}
		byName[name] = family
		names = append(names, name)
	}
	if _, ok := byName[nameResident]; !ok {
		if rss, ok := residentBytes(); ok {
			byName[nameResident] = residentFamily(rss)
			names = append(names, nameResident)
		}
	}
	for _, s := range inferenceSpecs {
		if _, ok := byName[s.name]; ok {
			continue
		}
		names = append(names, s.name)
	}
	slices.Sort(names)

	var buf bytes.Buffer
	for _, name := range names {
		family, ok := byName[name]
		if ok {
			if _, writeErr := expfmt.MetricFamilyToText(&buf, family); writeErr != nil {
				return nil, fmt.Errorf("encode %s: %w", name, writeErr)
			}
			continue
		}
		s, found := lookupSpec(name)
		if !found {
			return nil, fmt.Errorf("missing description for %s", name)
		}
		writeFamilyHeader(&buf, s)
	}
	return buf.Bytes(), nil
}

func lookupSpec(name string) (spec, bool) {
	for _, s := range inferenceSpecs {
		if s.name == name {
			return s, true
		}
	}
	return spec{}, false
}

func writeFamilyHeader(buf *bytes.Buffer, s spec) {
	_, _ = buf.WriteString("# HELP ")
	_, _ = buf.WriteString(s.name)
	_ = buf.WriteByte(' ')
	_, _ = buf.WriteString(escapeHelp(s.help))
	_ = buf.WriteByte('\n')
	_, _ = buf.WriteString("# TYPE ")
	_, _ = buf.WriteString(s.name)
	_ = buf.WriteByte(' ')
	_, _ = buf.WriteString(typeName(s.kind))
	_ = buf.WriteByte('\n')
}

func typeName(kind dto.MetricType) string {
	if kind == dto.MetricType_GAUGE {
		return "gauge"
	}
	return "counter"
}

func escapeHelp(help string) string {
	return strings.NewReplacer(`\`, `\\`, "\n", `\n`).Replace(help)
}

func residentFamily(rss float64) *dto.MetricFamily {
	return &dto.MetricFamily{
		Name: proto.String(nameResident),
		Help: proto.String(helpResident),
		Type: dto.MetricType_GAUGE.Enum(),
		Metric: []*dto.Metric{{
			Gauge: &dto.Gauge{Value: proto.Float64(rss)},
		}},
	}
}
