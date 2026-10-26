// Package page renders the nodes, models, and engine views.
// Colors are CSS variables in the page. Snapshot text is escaped.
// The engine view graphs the last 15 minutes. Each page reloads every 10 seconds.
package page

import (
	"fmt"
	"io"
	"net/url"
	"time"

	"github.com/eclipse-iofog/ai-inference-onnx/dashboard/internal/store"
)

const (
	// ConnectionUp means snapshots can be read.
	ConnectionUp Connection = iota
	// ConnectionNotConfigured means the server URL or the credentials path is unset.
	ConnectionNotConfigured
	// ConnectionDown means the subscription is not receiving snapshots.
	ConnectionDown
)

const (
	kindLive    = "live"
	kindStale   = "stale"
	kindUnknown = "unknown"
)

// Connection is whether the dashboard is receiving snapshots.
type Connection int

type shellData struct {
	Title        string
	NodesActive  bool
	ModelsActive bool
	Notice       string
}

type nodesPage struct {
	shellData
	Empty bool
	Nodes []nodeCard
}

type nodeCard struct {
	Title   string
	Unknown bool
	Engines []engineLink
}

type engineLink struct {
	ServerID string
	Label    string
	Chips    []chip
	Requests reading
	Errors   reading
	Slowest  reading
	Pending  string
}

type chip struct {
	Label string
	On    bool
}

type modelsPage struct {
	shellData
	Empty bool
	Rows  []modelRow
}

type modelRow struct {
	Name     string
	Engines  []modelEngine
	State    string
	Class    string
	Requests reading
	Errors   reading
	Request  reading
	Queue    reading
	Compute  reading
	Pending  string
}

type modelEngine struct {
	ServerID string
}

type enginePage struct {
	shellData
	Kind string
	Body engineBody
}

type engineBody struct {
	Heading            string
	ServerID           string
	Fresh              string
	Node               string
	NodeUnknown        bool
	Application        string
	ApplicationUnknown bool
	Live               string
	Ready              string
	NATS               string
	Stats              []statView
	Filters            []filterChip
	Charts             []chartView
	Models             []modelBlock
}

type statView struct {
	Label string
	Text  string
	Known bool
	Bad   bool
}

type filterChip struct {
	Href   string
	Label  string
	Active bool
}

type modelBlock struct {
	Name         string
	Version      string
	Pending      uint64
	Success      uint64
	Rejected     uint64
	Canceled     uint64
	Backend      uint64
	Other        uint64
	Failures     uint64
	Batch        string
	BatchKnown   bool
	State        string
	StateClass   string
	Reason       string
	Rate         string
	RateKnown    bool
	Request      string
	RequestKnown bool
	QueueLatency string
	QueueKnown   bool
	ComputeText  string
	ComputeKnown bool
}

// Nodes writes the nodes view.
// One card lists the live engines on that node.
func Nodes(w io.Writer, snapshots *store.Store, conn Connection) error {
	groups := snapshots.Nodes()
	page := nodesPage{
		shellData: shellData{
			Title:       "Nodes",
			NodesActive: true,
			Notice:      noticeText(conn),
		},
		Empty: len(groups) == 0,
		Nodes: make([]nodeCard, 0, len(groups)),
	}
	for _, group := range groups {
		card := nodeCard{Engines: make([]engineLink, 0, len(group.Engines))}
		if group.NodeUID == "" {
			card.Title = "Node unknown"
			card.Unknown = true
		} else {
			card.Title = group.NodeUID
		}
		for _, eng := range group.Engines {
			card.Engines = append(card.Engines, linkFrom(eng))
		}
		page.Nodes = append(page.Nodes, card)
	}
	return write(w, "nodes", page)
}

// Models writes the models view.
// One model name is one row. Rate and latency use the newest interval on each engine.
func Models(w io.Writer, snapshots *store.Store, conn Connection) error {
	rows := snapshots.Models()
	engines := snapshots.Engines()
	byID := make(map[string]store.Engine, len(engines))
	for _, eng := range engines {
		byID[eng.ServerID] = eng
	}
	page := modelsPage{
		shellData: shellData{
			Title:        "Models",
			ModelsActive: true,
			Notice:       noticeText(conn),
		},
		Empty: len(engines) == 0,
		Rows:  make([]modelRow, 0, len(rows)),
	}
	for _, row := range rows {
		group := make([]store.Engine, 0, len(row.ServerIDs))
		for _, id := range row.ServerIDs {
			group = append(group, byID[id])
		}
		folded := foldEngines(group, row.Name, false)
		state := worstState(group, row.Name)
		item := modelRow{
			Name:     row.Name,
			State:    state,
			Class:    stateClass(state),
			Requests: folded.requests,
			Errors:   folded.errors,
			Request:  folded.request,
			Queue:    folded.queue,
			Compute:  folded.compute,
			Pending:  fmt.Sprintf("%d", folded.pending),
			Engines:  make([]modelEngine, 0, len(row.ServerIDs)),
		}
		if state == "" {
			item.State = "unknown"
			item.Class = "unknown"
		}
		for _, id := range row.ServerIDs {
			item.Engines = append(item.Engines, modelEngine{ServerID: id})
		}
		page.Rows = append(page.Rows, item)
	}
	return write(w, "models", page)
}

// Engine writes the engine view for serverID.
// The title is the microservice name when it is set, otherwise the server id.
// A stale id says the engine is stale. An id that was never kept says the engine is unknown.
func Engine(w io.Writer, snapshots *store.Store, serverID string, conn Connection) error {
	return EngineModel(w, snapshots, serverID, "", conn)
}

// EngineModel writes the engine view for serverID.
// model limits the graphs and the model cards to that model name.
// An empty model name selects every model on the engine.
func EngineModel(w io.Writer, snapshots *store.Store, serverID, model string, conn Connection) error {
	eng, status := snapshots.Lookup(serverID)
	page := enginePage{shellData: shellData{Notice: noticeText(conn)}}
	switch status {
	case store.StatusStale:
		page.Title = "This engine is stale."
		page.Kind = kindStale
	case store.StatusLive:
		page.Kind = kindLive
		page.Body = bodyFrom(eng, model)
		page.Title = page.Body.Heading
	default:
		page.Title = "This engine is unknown."
		page.Kind = kindUnknown
	}
	return write(w, "engine", page)
}

func noticeText(conn Connection) string {
	switch conn {
	case ConnectionNotConfigured:
		return "NATS is not configured."
	case ConnectionDown:
		return "NATS is not connected."
	default:
		return ""
	}
}

func linkFrom(eng store.Engine) engineLink {
	label := eng.ServerID
	if eng.MicroserviceName != "" {
		label = eng.MicroserviceName
	}
	folded := foldEngines([]store.Engine{eng}, "", true)
	return engineLink{
		ServerID: eng.ServerID,
		Label:    label,
		Chips: []chip{
			{Label: "Live", On: eng.Live},
			{Label: "Ready", On: eng.Ready},
			{Label: "NATS", On: eng.NATSConnected},
		},
		Requests: folded.requests,
		Errors:   folded.errors,
		Slowest:  folded.slowest,
		Pending:  fmt.Sprintf("%d", folded.pending),
	}
}

func bodyFrom(eng store.Engine, model string) engineBody {
	heading := eng.ServerID
	if eng.MicroserviceName != "" {
		heading = eng.MicroserviceName
	}
	node, nodeUnknown := knownText(eng.NodeUID)
	application, applicationUnknown := knownText(eng.ApplicationName)
	folded := foldEngines([]store.Engine{eng}, model, false)
	body := engineBody{
		Heading:            heading,
		ServerID:           eng.ServerID,
		Node:               node,
		NodeUnknown:        nodeUnknown,
		Application:        application,
		ApplicationUnknown: applicationUnknown,
		Live:               yesNo(eng.Live),
		Ready:              yesNo(eng.Ready),
		NATS:               yesNo(eng.NATSConnected),
		Stats: []statView{
			statFrom("Requests/s", folded.requests),
			statFrom("Error", folded.errors),
			statFrom("Request latency", folded.request),
			statFrom("Queue latency", folded.queue),
			statFrom("Compute latency", folded.compute),
			{Label: "Queue depth", Text: fmt.Sprintf("%d", folded.pending), Known: true},
		},
		Filters: filtersFrom(eng, model),
		Charts:  presentCharts(buildCharts(eng.Samples, model)),
		Models:  make([]modelBlock, 0, len(eng.Models)),
	}
	if len(eng.Samples) > 0 {
		body.Fresh = eng.Samples[len(eng.Samples)-1].At.UTC().Format(time.RFC3339)
	}
	for _, item := range eng.Models {
		if model != "" && item.Name != model {
			continue
		}
		batch, known := formatBatch(item.BatchSize())
		rate, request, queue, compute := versionReading(eng.Samples, item.Name, item.Version)
		body.Models = append(body.Models, modelBlock{
			Name:         item.Name,
			Version:      item.Version,
			Pending:      item.Pending,
			Success:      item.Success,
			Rejected:     item.Failure.Rejected,
			Canceled:     item.Failure.Canceled,
			Backend:      item.Failure.Backend,
			Other:        item.Failure.Other,
			Failures:     item.Failure.Sum(),
			Batch:        batch,
			BatchKnown:   known,
			State:        item.State,
			StateClass:   stateClass(item.State),
			Reason:       item.Reason,
			Rate:         rate.Text,
			RateKnown:    rate.Known,
			Request:      request.Text,
			RequestKnown: request.Known,
			QueueLatency: queue.Text,
			QueueKnown:   queue.Known,
			ComputeText:  compute.Text,
			ComputeKnown: compute.Known,
		})
	}
	return body
}

func versionReading(samples []store.Sample, name, version string) (reading, reading, reading, reading) {
	unknown := unknownReading()
	st, ok := latestStep(samples, name)
	if !ok {
		return unknown, unknown, unknown, unknown
	}
	for _, vs := range st.versions {
		if vs.name != name || vs.version != version || !vs.ok {
			continue
		}
		seconds := st.dt.Seconds()
		return rateReading(float64(vs.success+vs.fail)/seconds, seconds > 0),
			msReading(vs.request, vs.success),
			msReading(vs.queue, vs.success),
			msReading(vs.compute, vs.success)
	}
	return unknown, unknown, unknown, unknown
}

func statFrom(label string, item reading) statView {
	return statView{Label: label, Text: item.Text, Known: item.Known, Bad: item.Bad}
}

func filtersFrom(eng store.Engine, selected string) []filterChip {
	seen := make(map[string]bool)
	names := make([]string, 0, len(eng.Models))
	for _, item := range eng.Models {
		if seen[item.Name] {
			continue
		}
		seen[item.Name] = true
		names = append(names, item.Name)
	}
	if len(names) == 0 {
		return nil
	}
	chips := make([]filterChip, 0, len(names)+1)
	chips = append(chips, filterChip{Href: enginePath(eng.ServerID, ""), Label: "All models", Active: selected == ""})
	for _, name := range names {
		chips = append(chips, filterChip{Href: enginePath(eng.ServerID, name), Label: name, Active: name == selected})
	}
	return chips
}

func enginePath(serverID, model string) string {
	path := "/engines/" + url.PathEscape(serverID)
	if model == "" {
		return path
	}
	return path + "?model=" + url.QueryEscape(model)
}

func knownText(value string) (string, bool) {
	if value == "" {
		return "unknown", true
	}
	return value, false
}

func yesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

func stateClass(state string) string {
	switch state {
	case store.StateReady:
		return "state-ready"
	case store.StateLoading:
		return "state-loading"
	case store.StateUnavailable:
		return "state-unavailable"
	default:
		return "unknown"
	}
}

func formatBatch(ratio store.Ratio) (string, bool) {
	if !ratio.Known || ratio.Divisor == 0 {
		return "unknown", false
	}
	whole := ratio.Numerator / ratio.Divisor
	frac := (ratio.Numerator % ratio.Divisor) * 100 / ratio.Divisor
	return fmt.Sprintf("%d.%02d", whole, frac), true
}

func write(w io.Writer, name string, data any) error {
	if w == nil {
		return fmt.Errorf("render %s: nil writer", name)
	}
	if err := views.ExecuteTemplate(w, name, data); err != nil {
		return fmt.Errorf("render %s: %w", name, err)
	}
	return nil
}
