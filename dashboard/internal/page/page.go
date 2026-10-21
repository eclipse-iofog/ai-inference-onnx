// Package page renders the nodes, models, and engine views.
// Colors are CSS variables in the page. Snapshot text is escaped.
package page

import (
	"fmt"
	"io"
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
	Live     string
	Ready    string
	NATS     string
}

type modelsPage struct {
	shellData
	Empty bool
	Rows  []modelRow
}

type modelRow struct {
	Name    string
	Engines []modelEngine
	Latency string
	Known   bool
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
	Node               string
	NodeUnknown        bool
	Application        string
	ApplicationUnknown bool
	Live               string
	Ready              string
	NATS               string
	Models             []modelBlock
	Samples            []sampleRow
}

type modelBlock struct {
	Name       string
	Version    string
	Pending    uint64
	Success    uint64
	Rejected   uint64
	Canceled   uint64
	Backend    uint64
	Other      uint64
	Failures   uint64
	Batch      string
	BatchKnown bool
	State      string
	StateClass string
	Reason     string
}

type sampleRow struct {
	At      string
	Latency string
	Known   bool
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
// One model name is one row. Latency is the newest snapshots' request duration
// divided by their success count, in microseconds, truncated toward zero.
func Models(w io.Writer, snapshots *store.Store, conn Connection) error {
	rows := snapshots.Models()
	page := modelsPage{
		shellData: shellData{
			Title:        "Models",
			ModelsActive: true,
			Notice:       noticeText(conn),
		},
		Empty: len(snapshots.Engines()) == 0,
		Rows:  make([]modelRow, 0, len(rows)),
	}
	for _, row := range rows {
		latency, known := formatLatency(row.Latency)
		item := modelRow{
			Name:    row.Name,
			Latency: latency,
			Known:   known,
			Engines: make([]modelEngine, 0, len(row.ServerIDs)),
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
	eng, status := snapshots.Lookup(serverID)
	page := enginePage{shellData: shellData{Notice: noticeText(conn)}}
	switch status {
	case store.StatusStale:
		page.Title = "This engine is stale."
		page.Kind = kindStale
	case store.StatusLive:
		page.Kind = kindLive
		page.Body = bodyFrom(eng)
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
	return engineLink{
		ServerID: eng.ServerID,
		Label:    label,
		Live:     yesNo(eng.Live),
		Ready:    yesNo(eng.Ready),
		NATS:     yesNo(eng.NATSConnected),
	}
}

func bodyFrom(eng store.Engine) engineBody {
	heading := eng.ServerID
	if eng.MicroserviceName != "" {
		heading = eng.MicroserviceName
	}
	node, nodeUnknown := knownText(eng.NodeUID)
	application, applicationUnknown := knownText(eng.ApplicationName)
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
		Models:             make([]modelBlock, 0, len(eng.Models)),
		Samples:            make([]sampleRow, 0, len(eng.Samples)),
	}
	for _, model := range eng.Models {
		batch, known := formatBatch(model.BatchSize())
		body.Models = append(body.Models, modelBlock{
			Name:       model.Name,
			Version:    model.Version,
			Pending:    model.Pending,
			Success:    model.Success,
			Rejected:   model.Failure.Rejected,
			Canceled:   model.Failure.Canceled,
			Backend:    model.Failure.Backend,
			Other:      model.Failure.Other,
			Failures:   model.Failure.Sum(),
			Batch:      batch,
			BatchKnown: known,
			State:      model.State,
			StateClass: stateClass(model.State),
			Reason:     model.Reason,
		})
	}
	for _, sample := range eng.Samples {
		latency, known := formatLatency(sample.Latency)
		body.Samples = append(body.Samples, sampleRow{
			At:      sample.At.UTC().Format(time.RFC3339),
			Latency: latency,
			Known:   known,
		})
	}
	return body
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

func formatLatency(ratio store.Ratio) (string, bool) {
	if !ratio.Known {
		return "unknown", false
	}
	return fmt.Sprintf("%d", ratio.Whole()), true
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
