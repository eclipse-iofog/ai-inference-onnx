package page

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/eclipse-iofog/ai-inference-onnx/dashboard/internal/store"
)

const (
	// chartWindow is the span of every graph.
	chartWindow = 15 * time.Minute
	// refreshSeconds is how often the page reloads. Snapshots arrive on that same cadence by default.
	refreshSeconds = 10
)

const (
	waitMsg    = "Waiting for the next snapshot."
	idleMsg    = "No completed requests in this window."
	missingMsg = "This model is not on the latest snapshot."
)

// reading is one number on a page.
// Known is false when the interval cannot be computed. Text is then "unknown".
type reading struct {
	Text  string
	Known bool
	Bad   bool
}

func unknownReading() reading {
	return reading{Text: "unknown"}
}

type versionStep struct {
	name     string
	version  string
	success  uint64
	fail     uint64
	rejected uint64
	canceled uint64
	backend  uint64
	other    uint64
	request  uint64
	queue    uint64
	compute  uint64
	pending  uint64
	ok       bool
	reset    bool
	added    bool
	gone     bool
}

type step struct {
	at         time.Time
	dt         time.Duration
	versions   []versionStep
	comparable int
	ok         bool
}

type chartPoint struct {
	at    time.Time
	value float64
	tip   string
}

type chartSeries struct {
	name  string
	spans [][]chartPoint
}

type chart struct {
	title  string
	unit   string
	empty  string
	wide   bool
	start  time.Time
	end    time.Time
	series []chartSeries
}

type folded struct {
	requests reading
	errors   reading
	request  reading
	queue    reading
	compute  reading
	slowest  reading
	pending  uint64
}

func msReading(us, n uint64) reading {
	if n == 0 {
		return unknownReading()
	}
	whole := us / n
	return reading{Text: fmt.Sprintf("%d.%03d ms", whole/1000, whole%1000), Known: true}
}

func rateReading(perSecond float64, known bool) reading {
	if !known || math.IsNaN(perSecond) || math.IsInf(perSecond, 0) || perSecond < 0 {
		return unknownReading()
	}
	if perSecond == 0 {
		return reading{Text: "0 /s", Known: true}
	}
	truncated := math.Trunc(perSecond*1000) / 1000
	return reading{Text: strconv.FormatFloat(truncated, 'f', 3, 64) + " /s", Known: true}
}

func percentReading(fail, total uint64) reading {
	if total == 0 {
		return unknownReading()
	}
	scaled, ok := mul(fail, 1000)
	if !ok {
		return unknownReading()
	}
	tenths := scaled / total
	return reading{Text: fmt.Sprintf("%d.%d%%", tenths/10, tenths%10), Known: true, Bad: fail > 0}
}

func mul(a, b uint64) (uint64, bool) {
	if a == 0 || b == 0 {
		return 0, true
	}
	if a > ^uint64(0)/b {
		return 0, false
	}
	return a * b, true
}

func add(a, b uint64) (uint64, bool) {
	s := a + b
	return s, s >= a
}

func foldEngines(engines []store.Engine, model string, pendingMax bool) folded {
	var (
		us, queueUs, computeUs uint64
		success, fail          uint64
		pending                uint64
		pendingSet             bool
		rate                   float64
		rateKnown              bool
		slowUs, slowN          uint64
		slowKnown              bool
		failed                 bool
	)
	for _, eng := range engines {
		st, ok := latestStep(eng.Samples, model)
		if ok {
			for _, vs := range st.versions {
				if !vs.ok {
					continue
				}
				var good bool
				success, good = add(success, vs.success)
				failed = failed || !good
				fail, good = add(fail, vs.fail)
				failed = failed || !good
				us, good = add(us, vs.request)
				failed = failed || !good
				queueUs, good = add(queueUs, vs.queue)
				failed = failed || !good
				computeUs, good = add(computeUs, vs.compute)
				failed = failed || !good
				if vs.success > 0 && slower(vs.request, vs.success, slowUs, slowN, slowKnown) {
					slowUs, slowN, slowKnown = vs.request, vs.success, true
				}
			}
			seconds := st.dt.Seconds()
			if seconds > 0 {
				rate += float64(stepCount(st)) / seconds
				rateKnown = true
			}
		}
		if n, known := pendingOf(eng.Samples, model); known {
			if pendingMax {
				if !pendingSet || n > pending {
					pending = n
				}
			} else {
				sum, good := add(pending, n)
				if !good {
					failed = true
				} else {
					pending = sum
				}
			}
			pendingSet = true
		}
	}
	out := folded{
		requests: rateReading(rate, rateKnown && !failed),
		errors:   percentReading(fail, success+fail),
		request:  msReading(us, success),
		queue:    msReading(queueUs, success),
		compute:  msReading(computeUs, success),
		slowest:  msReading(slowUs, slowN),
		pending:  pending,
	}
	if failed {
		out.requests = unknownReading()
		out.errors = unknownReading()
		out.request = unknownReading()
		out.queue = unknownReading()
		out.compute = unknownReading()
		out.slowest = unknownReading()
	}
	if !pendingSet {
		out.pending = 0
	}
	return out
}

func slower(us, n, bestUs, bestN uint64, bestKnown bool) bool {
	if n == 0 {
		return false
	}
	if !bestKnown || bestN == 0 {
		return true
	}
	return float64(us)/float64(n) > float64(bestUs)/float64(bestN)
}

func stepCount(st step) uint64 {
	var n uint64
	for _, vs := range st.versions {
		if vs.ok {
			n += vs.success + vs.fail
		}
	}
	return n
}

func pendingOf(samples []store.Sample, model string) (uint64, bool) {
	if len(samples) == 0 {
		return 0, false
	}
	last := samples[len(samples)-1]
	var sum uint64
	var seen bool
	for _, item := range last.Models {
		if model != "" && item.Name != model {
			continue
		}
		next, ok := add(sum, item.Pending)
		if !ok {
			return 0, false
		}
		sum = next
		seen = true
	}
	if model != "" && !seen {
		return 0, false
	}
	return sum, true
}

func latestStep(samples []store.Sample, model string) (step, bool) {
	if len(samples) < 2 {
		return step{}, false
	}
	st := stepBetween(samples[len(samples)-2], samples[len(samples)-1], model)
	return st, st.ok
}

func stepBetween(older, newer store.Sample, model string) step {
	if !newer.At.After(older.At) {
		return step{}
	}
	before := indexModels(older.Models, model)
	after := indexModels(newer.Models, model)
	keys := unionKeys(before, after)
	out := step{at: newer.At, dt: newer.At.Sub(older.At)}
	for _, key := range keys {
		n, hasN := after[key]
		o, hasO := before[key]
		vs := versionStep{}
		switch {
		case !hasN:
			vs.name, vs.version = o.Name, o.Version
			vs.gone = true
		case !hasO:
			vs.name, vs.version = n.Name, n.Version
			vs.pending = n.Pending
			vs.added = true
		default:
			vs.name, vs.version = n.Name, n.Version
			vs.pending = n.Pending
			d, ok := subtract(n, o)
			if !ok {
				vs.reset = true
				break
			}
			vs.ok = true
			vs.success = d.success
			vs.fail = d.fail
			vs.rejected = d.rejected
			vs.canceled = d.canceled
			vs.backend = d.backend
			vs.other = d.other
			vs.request = d.request
			vs.queue = d.queue
			vs.compute = d.compute
			out.comparable++
		}
		out.versions = append(out.versions, vs)
	}
	out.ok = out.comparable > 0
	return out
}

type delta struct {
	success, fail                      uint64
	rejected, canceled, backend, other uint64
	request, queue, compute            uint64
}

func subtract(n, o store.Model) (delta, bool) {
	if n.Success < o.Success || n.RequestDuration < o.RequestDuration || n.QueueDuration < o.QueueDuration || n.ComputeDuration < o.ComputeDuration {
		return delta{}, false
	}
	if n.Failure.Rejected < o.Failure.Rejected || n.Failure.Canceled < o.Failure.Canceled || n.Failure.Backend < o.Failure.Backend || n.Failure.Other < o.Failure.Other {
		return delta{}, false
	}
	return delta{
		success:  n.Success - o.Success,
		fail:     n.Failure.Sum() - o.Failure.Sum(),
		rejected: n.Failure.Rejected - o.Failure.Rejected,
		canceled: n.Failure.Canceled - o.Failure.Canceled,
		backend:  n.Failure.Backend - o.Failure.Backend,
		other:    n.Failure.Other - o.Failure.Other,
		request:  n.RequestDuration - o.RequestDuration,
		queue:    n.QueueDuration - o.QueueDuration,
		compute:  n.ComputeDuration - o.ComputeDuration,
	}, true
}

func indexModels(models []store.Model, model string) map[string]store.Model {
	out := make(map[string]store.Model, len(models))
	for _, item := range models {
		if model != "" && item.Name != model {
			continue
		}
		out[item.Name+"\x00"+item.Version] = item
	}
	return out
}

func unionKeys(a, b map[string]store.Model) []string {
	keys := make([]string, 0, len(a)+len(b))
	seen := make(map[string]bool, len(a)+len(b))
	for key := range a {
		seen[key] = true
		keys = append(keys, key)
	}
	for key := range b {
		if seen[key] {
			continue
		}
		keys = append(keys, key)
	}
	slices.SortFunc(keys, func(left, right string) int {
		ln, lv, _ := strings.Cut(left, "\x00")
		rn, rv, _ := strings.Cut(right, "\x00")
		if c := strings.Compare(ln, rn); c != 0 {
			return c
		}
		return compareVersion(lv, rv)
	})
	return keys
}

func compareVersion(a, b string) int {
	ai, aerr := strconv.ParseUint(a, 10, 64)
	bi, berr := strconv.ParseUint(b, 10, 64)
	if aerr == nil && berr == nil && ai != bi {
		if ai < bi {
			return -1
		}
		return 1
	}
	return strings.Compare(a, b)
}

func modelOnLatest(samples []store.Sample, model string) bool {
	if model == "" || len(samples) == 0 {
		return model == ""
	}
	last := samples[len(samples)-1]
	for _, item := range last.Models {
		if item.Name == model {
			return true
		}
	}
	return false
}

func worstState(engines []store.Engine, model string) string {
	state := ""
	for _, eng := range engines {
		if len(eng.Samples) == 0 {
			continue
		}
		last := eng.Samples[len(eng.Samples)-1]
		for _, item := range last.Models {
			if model != "" && item.Name != model {
				continue
			}
			state = worseState(state, item.State)
		}
	}
	return state
}

func worseState(current, next string) string {
	if stateRank(next) > stateRank(current) {
		return next
	}
	return current
}

func stateRank(state string) int {
	switch state {
	case store.StateUnavailable:
		return 3
	case store.StateLoading:
		return 2
	case store.StateReady:
		return 1
	default:
		return 0
	}
}

func buildCharts(samples []store.Sample, model string) []chart {
	charts := []chart{
		{title: "Request rate", unit: "per second"},
		{title: "Request latency", unit: "milliseconds"},
		{title: "Queue latency", unit: "milliseconds"},
		{title: "Compute latency", unit: "milliseconds"},
		{title: "Queue depth", unit: "requests", wide: true},
	}
	if model != "" && !modelOnLatest(samples, model) {
		for i := range charts {
			charts[i].empty = missingMsg
		}
		return charts
	}
	if len(samples) == 0 {
		for i := range charts {
			charts[i].empty = waitMsg
		}
		return charts
	}
	newest := samples[len(samples)-1].At
	start := newest.Add(-chartWindow)
	for i := range charts {
		charts[i].start = start
		charts[i].end = newest
	}
	rate, latency, queue, compute, depth := seriesOver(samples, model, start)
	charts[0].series = rate
	charts[1].series = latency
	charts[2].series = queue
	charts[3].series = compute
	charts[4].series = depth
	if len(rate) == 0 {
		charts[0].empty = waitMsg
	}
	charts[1].empty = latencyEmpty(samples, latency)
	charts[2].empty = latencyEmpty(samples, queue)
	charts[3].empty = latencyEmpty(samples, compute)
	if len(depth) == 0 {
		charts[4].empty = waitMsg
	}
	return charts
}

func latencyEmpty(samples []store.Sample, series []chartSeries) string {
	if len(series) > 0 {
		return ""
	}
	if len(samples) < 2 {
		return waitMsg
	}
	return idleMsg
}

func seriesOver(samples []store.Sample, model string, start time.Time) (rate, latency, queue, compute, depth []chartSeries) {
	success := &chartSeries{name: "Success"}
	failure := &chartSeries{name: "Failure"}
	reasons := []*chartSeries{
		{name: "REJECTED"},
		{name: "CANCELED"},
		{name: "BACKEND"},
		{name: "OTHER"},
	}
	latencyBy := map[string]*chartSeries{}
	queueBy := map[string]*chartSeries{}
	computeBy := map[string]*chartSeries{}
	depthBy := map[string]*chartSeries{}
	broken := map[string]bool{}
	var rateBroken bool
	var sawRate bool
	var prevDepth map[string]bool

	for i, sample := range samples {
		if !sample.At.Before(start) {
			cur := indexModels(sample.Models, model)
			for _, key := range unionKeys(cur, nil) {
				item := cur[key]
				label := versionLabel(item)
				addPoint(seriesFor(depthBy, key, label), !prevDepth[key], chartPoint{
					at:    sample.At,
					value: float64(item.Pending),
					tip:   label + " " + sample.At.UTC().Format(time.RFC3339) + " " + strconv.FormatUint(item.Pending, 10),
				})
			}
			prevDepth = make(map[string]bool, len(cur))
			for key := range cur {
				prevDepth[key] = true
			}
		} else {
			prevDepth = make(map[string]bool)
			for key := range indexModels(sample.Models, model) {
				prevDepth[key] = true
			}
		}
		if i == 0 {
			continue
		}
		newer := samples[i]
		if newer.At.Before(start) {
			continue
		}
		st := stepBetween(samples[i-1], newer, model)
		if !st.ok {
			rateBroken = true
			for key := range latencyBy {
				broken[key] = true
			}
			continue
		}
		var successN, failN, rejected, canceled, backend, other uint64
		for _, vs := range st.versions {
			if !vs.ok {
				continue
			}
			successN += vs.success
			failN += vs.fail
			rejected += vs.rejected
			canceled += vs.canceled
			backend += vs.backend
			other += vs.other
		}
		seconds := st.dt.Seconds()
		sawRate = true
		addPoint(success, rateBroken, ratePoint("Success", st.at, successN, seconds))
		addPoint(failure, rateBroken, ratePoint("Failure", st.at, failN, seconds))
		addPoint(reasons[0], rateBroken, ratePoint("REJECTED", st.at, rejected, seconds))
		addPoint(reasons[1], rateBroken, ratePoint("CANCELED", st.at, canceled, seconds))
		addPoint(reasons[2], rateBroken, ratePoint("BACKEND", st.at, backend, seconds))
		addPoint(reasons[3], rateBroken, ratePoint("OTHER", st.at, other, seconds))
		rateBroken = false
		seen := make(map[string]bool, len(st.versions))
		for _, vs := range st.versions {
			key := vs.name + "\x00" + vs.version
			label := versionLabel(store.Model{Name: vs.name, Version: vs.version})
			seen[key] = true
			if !vs.ok || vs.success == 0 {
				broken[key] = true
				continue
			}
			addPoint(seriesFor(latencyBy, key, label), broken[key], latencyPoint(label, st.at, vs.request, vs.success))
			addPoint(seriesFor(queueBy, key, label), broken[key], latencyPoint(label, st.at, vs.queue, vs.success))
			addPoint(seriesFor(computeBy, key, label), broken[key], latencyPoint(label, st.at, vs.compute, vs.success))
			broken[key] = false
		}
		for key := range latencyBy {
			if !seen[key] {
				broken[key] = true
			}
		}
	}
	if sawRate {
		rate = []chartSeries{*success, *failure}
		for _, series := range reasons {
			if seriesPeaks(*series) {
				rate = append(rate, *series)
			}
		}
	}
	return rate, ordered(latencyBy), ordered(queueBy), ordered(computeBy), ordered(depthBy)
}

func ratePoint(name string, at time.Time, count uint64, seconds float64) chartPoint {
	value := 0.0
	if seconds > 0 {
		value = float64(count) / seconds
	}
	return chartPoint{at: at, value: value, tip: name + " " + at.UTC().Format(time.RFC3339) + " " + rateReading(value, true).Text}
}

func latencyPoint(label string, at time.Time, us, success uint64) chartPoint {
	value := float64(us) / float64(success) / 1000
	return chartPoint{at: at, value: value, tip: label + " " + at.UTC().Format(time.RFC3339) + " " + msReading(us, success).Text}
}

func versionLabel(item store.Model) string {
	if item.Version == "" {
		return item.Name
	}
	return item.Name + " " + item.Version
}

func seriesFor(sets map[string]*chartSeries, key, label string) *chartSeries {
	series := sets[key]
	if series == nil {
		series = &chartSeries{name: label}
		sets[key] = series
	}
	return series
}

func addPoint(series *chartSeries, broken bool, point chartPoint) {
	if broken || len(series.spans) == 0 {
		series.spans = append(series.spans, []chartPoint{point})
		return
	}
	last := len(series.spans) - 1
	series.spans[last] = append(series.spans[last], point)
}

func seriesPeaks(series chartSeries) bool {
	for _, span := range series.spans {
		for _, point := range span {
			if point.value > 0 {
				return true
			}
		}
	}
	return false
}

func ordered(sets map[string]*chartSeries) []chartSeries {
	keys := make([]string, 0, len(sets))
	for key, series := range sets {
		if len(series.spans) == 0 {
			continue
		}
		keys = append(keys, key)
	}
	slices.SortFunc(keys, func(left, right string) int {
		ln, lv, _ := strings.Cut(left, "\x00")
		rn, rv, _ := strings.Cut(right, "\x00")
		if c := strings.Compare(ln, rn); c != 0 {
			return c
		}
		return compareVersion(lv, rv)
	})
	out := make([]chartSeries, 0, len(keys))
	for _, key := range keys {
		out = append(out, *sets[key])
	}
	return out
}
