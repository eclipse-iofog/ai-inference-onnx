package page

import (
	"html/template"
	"strings"
)

const stylesheet = `:root {
  --page: #111827;
  --card: #ffffff;
  --card-border: #374151;
  --card-radius: 16px;
  --text: #f3f4f6;
  --muted: #9ca3af;
  --nav: #d1d5db;
  --nav-hover: #1f2937;
  --nav-active: #374151;
  --nav-active-text: #ffffff;
  --rule: #6b7280;
  --ready: #16A34A;
  --loading: #FBBF24;
  --unavailable: #DC2626;
  --stale: #7A3BFF;
  --unknown: #9CA3AF;
  --model-0: #38bdf8;
  --model-1: #fbbf24;
  --model-2: #a78bfa;
  --model-3: #fb923c;
  --model-4: #22d3ee;
  --model-5: #e879f9;
  --model-6: #94a3b8;
  --model-7: #f472b6;
  --rate-rejected: #fbbf24;
  --rate-canceled: #a78bfa;
  --rate-backend: #fb923c;
  --rate-other: #22d3ee;
}
body {
  margin: 0;
  background: var(--page);
  color: var(--text);
  font-family: system-ui, -apple-system, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif;
}
.shell {
  display: flex;
  align-items: stretch;
  min-height: 100vh;
}
.sidebar {
  width: 16rem;
  background: var(--page);
  border-right: 1px solid var(--rule);
  padding: 1.5rem 1rem;
  box-sizing: border-box;
}
.brand {
  margin: 0 0 1rem;
  padding-bottom: 1rem;
  border-bottom: 1px solid var(--rule);
  font-weight: 600;
}
a.nav {
  display: block;
  margin: 0 0 0.25rem;
  padding: 0.5rem 0.75rem;
  border-radius: 0.5rem;
  color: var(--nav);
  text-decoration: none;
}
a.nav:hover {
  background: var(--nav-hover);
}
a.nav.active {
  background: var(--nav-active);
  color: var(--nav-active-text);
  font-weight: 600;
}
main {
  flex: 1;
  min-width: 0;
  padding: 1.5rem 1.75rem 2.5rem;
}
main a {
  color: var(--nav);
}
h1 {
  margin: 0 0 0.35rem;
  font-size: 1.5rem;
  font-weight: 600;
}
h2 {
  margin: 0 0 0.75rem;
  font-size: 1rem;
  font-weight: 600;
}
.fresh, .section-note, .unit {
  margin: 0 0 1rem;
  color: var(--muted);
  font-size: 0.875rem;
}
.card {
  margin: 0 0 1rem;
  padding: 1rem;
  background: rgba(255, 255, 255, 0.05);
  border: 1px solid rgba(55, 65, 81, 0.5);
  border-radius: var(--card-radius);
}
.card.unknown,
.unknown {
  color: var(--unknown);
}
.card.unknown a {
  color: var(--unknown);
}
.stale {
  color: var(--stale);
}
.state-ready {
  color: var(--ready);
}
.state-loading {
  color: var(--loading);
}
.state-unavailable {
  color: var(--unavailable);
}
dt, th {
  color: var(--muted);
}
dl {
  display: grid;
  grid-template-columns: 10rem 1fr;
  gap: 0.25rem 1rem;
  margin: 0 0 1rem;
}
dt, dd {
  margin: 0;
}
table {
  width: 100%;
  border-collapse: collapse;
}
.table-wrap table {
  width: max-content;
  min-width: 100%;
}
th, td {
  padding: 0.45rem 0.75rem;
  border-bottom: 1px solid var(--rule);
  text-align: left;
  vertical-align: top;
}
th {
  font-size: 0.75rem;
  font-weight: 600;
}
.table-wrap {
  overflow-x: auto;
}
.stats {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(9.5rem, 1fr));
  gap: 0.75rem;
  margin: 0 0 1rem;
}
.stat {
  padding: 0.75rem 1rem;
  background: rgba(255, 255, 255, 0.05);
  border: 1px solid rgba(55, 65, 81, 0.5);
  border-radius: var(--card-radius);
}
.stat-label {
  margin: 0;
  color: var(--muted);
  font-size: 0.75rem;
  font-weight: 600;
}
.stat-value {
  margin: 0.35rem 0 0;
  font-size: 1.35rem;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
}
.charts {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 1rem;
  margin: 0 0 1rem;
}
.chart {
  margin: 0;
}
.versions {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 1rem;
  margin: 0 0 1rem;
}
.versions > .card {
  margin: 0;
}
.counts {
  display: flex;
  flex-wrap: wrap;
  gap: 0.35rem;
  margin: 0 0 0.75rem;
}
.counts .chip {
  margin: 0;
}
.filter {
  display: flex;
  flex-wrap: wrap;
  gap: 0.5rem;
  align-items: center;
  margin: 0 0 1rem;
}
a.chip {
  display: inline-block;
  margin: 0 0.25rem 0.25rem 0;
  padding: 0.15rem 0.5rem;
  border: 1px solid var(--rule);
  border-radius: 999px;
  color: var(--nav);
  font-size: 0.75rem;
  text-decoration: none;
}
a.chip.chip-on, span.chip.chip-on {
  color: var(--ready);
  border-color: var(--ready);
}
span.chip {
  display: inline-block;
  margin: 0 0.25rem 0.25rem 0;
  padding: 0.15rem 0.5rem;
  border: 1px solid var(--rule);
  border-radius: 999px;
  font-size: 0.75rem;
}
span.chip.chip-off {
  color: var(--unavailable);
  border-color: var(--unavailable);
}
.legend {
  display: flex;
  flex-wrap: wrap;
  gap: 0.75rem;
  margin: 0.75rem 0 0;
  padding: 0;
  list-style: none;
  font-size: 0.8125rem;
}
.plot {
  width: 100%;
  height: auto;
  display: block;
}
.plot .grid {
  stroke: var(--rule);
  stroke-width: 1;
}
.plot .axis {
  fill: var(--muted);
  font-size: 11px;
}
.plot .model-0 { stroke: var(--model-0); fill: none; stroke-width: 2; }
.plot .model-1 { stroke: var(--model-1); fill: none; stroke-width: 2; }
.plot .model-2 { stroke: var(--model-2); fill: none; stroke-width: 2; }
.plot .model-3 { stroke: var(--model-3); fill: none; stroke-width: 2; }
.plot .model-4 { stroke: var(--model-4); fill: none; stroke-width: 2; }
.plot .model-5 { stroke: var(--model-5); fill: none; stroke-width: 2; }
.plot .model-6 { stroke: var(--model-6); fill: none; stroke-width: 2; }
.plot .model-7 { stroke: var(--model-7); fill: none; stroke-width: 2; }
.plot circle.model-0 { fill: var(--model-0); stroke: none; }
.plot circle.model-1 { fill: var(--model-1); stroke: none; }
.plot circle.model-2 { fill: var(--model-2); stroke: none; }
.plot circle.model-3 { fill: var(--model-3); stroke: none; }
.plot circle.model-4 { fill: var(--model-4); stroke: none; }
.plot circle.model-5 { fill: var(--model-5); stroke: none; }
.plot circle.model-6 { fill: var(--model-6); stroke: none; }
.plot circle.model-7 { fill: var(--model-7); stroke: none; }
.legend .model-0 { color: var(--model-0); }
.legend .model-1 { color: var(--model-1); }
.legend .model-2 { color: var(--model-2); }
.legend .model-3 { color: var(--model-3); }
.legend .model-4 { color: var(--model-4); }
.legend .model-5 { color: var(--model-5); }
.legend .model-6 { color: var(--model-6); }
.legend .model-7 { color: var(--model-7); }
.plot .rate-success { stroke: var(--ready); fill: none; stroke-width: 2; }
.plot .rate-failure { stroke: var(--unavailable); fill: none; stroke-width: 2; }
.plot .rate-rejected { stroke: var(--rate-rejected); fill: none; stroke-width: 2; }
.plot .rate-canceled { stroke: var(--rate-canceled); fill: none; stroke-width: 2; }
.plot .rate-backend { stroke: var(--rate-backend); fill: none; stroke-width: 2; }
.plot .rate-other { stroke: var(--rate-other); fill: none; stroke-width: 2; }
.plot circle.rate-success { fill: var(--ready); stroke: none; }
.plot circle.rate-failure { fill: var(--unavailable); stroke: none; }
.plot circle.rate-rejected { fill: var(--rate-rejected); stroke: none; }
.plot circle.rate-canceled { fill: var(--rate-canceled); stroke: none; }
.plot circle.rate-backend { fill: var(--rate-backend); stroke: none; }
.plot circle.rate-other { fill: var(--rate-other); stroke: none; }
.legend .rate-success { color: var(--ready); }
.legend .rate-failure { color: var(--unavailable); }
.legend .rate-rejected { color: var(--rate-rejected); }
.legend .rate-canceled { color: var(--rate-canceled); }
.legend .rate-backend { color: var(--rate-backend); }
.legend .rate-other { color: var(--rate-other); }
@media (max-width: 1100px) {
  .versions { grid-template-columns: repeat(2, minmax(0, 1fr)); }
}
@media (max-width: 900px) {
  .shell { display: block; }
  .sidebar {
    display: flex;
    flex-wrap: wrap;
    gap: 0.5rem;
    align-items: center;
    width: auto;
    border-right: 0;
    border-bottom: 1px solid var(--rule);
  }
  .brand {
    margin: 0;
    padding: 0 0.5rem 0 0;
    border-bottom: 0;
  }
  a.nav { margin: 0; }
  dl { grid-template-columns: 1fr; }
  .charts { grid-template-columns: 1fr; }
}
@media (max-width: 700px) {
  .versions { grid-template-columns: 1fr; }
}
`

const markup = `
{{define "top"}}<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta http-equiv="refresh" content="10">
<title>{{.Title}}</title>
<style>
/*STYLE*/
</style>
</head>
<body>
<div class="shell">
<nav class="sidebar">
<p class="brand">Inference</p>
<a class="nav{{if .NodesActive}} active{{end}}" href="/nodes">Nodes</a>
<a class="nav{{if .ModelsActive}} active{{end}}" href="/models">Models</a>
</nav>
{{end}}

{{define "bottom"}}</div>
</body>
</html>
{{end}}

{{define "status"}}{{if .Notice}}<p>{{.Notice}}</p>{{end}}{{end}}

{{define "nodes"}}{{template "top" .}}<main>
<h1>Nodes</h1>
<p class="fresh">Refreshes every 10 seconds.</p>
<p class="section-note">Last 15 minutes.</p>
{{template "status" .}}
{{if .Empty}}<p>No engines.</p>{{end}}
{{range .Nodes}}<article class="card{{if .Unknown}} unknown{{end}}">
<h2>{{.Title}}</h2>
<div class="table-wrap"><table>
<thead><tr><th>Engine</th><th>Status</th><th>Requests/s</th><th>Error</th><th>Slowest request</th><th>Queue depth</th></tr></thead>
<tbody>
{{range .Engines}}<tr class="engine"><td><a href="/engines/{{.ServerID}}">{{.Label}}</a></td><td>{{range .Chips}}<span class="chip {{if .On}}chip-on{{else}}chip-off{{end}}">{{.Label}}</span> {{end}}</td><td>{{if .Requests.Known}}{{.Requests.Text}}{{else}}<span class="unknown">unknown</span>{{end}}</td><td{{if .Errors.Bad}} class="state-unavailable"{{end}}>{{if .Errors.Known}}{{.Errors.Text}}{{else}}<span class="unknown">unknown</span>{{end}}</td><td>{{if .Slowest.Known}}{{.Slowest.Text}}{{else}}<span class="unknown">unknown</span>{{end}}</td><td>{{.Pending}}</td></tr>
{{end}}</tbody>
</table></div>
</article>
{{end}}</main>
{{template "bottom" .}}{{end}}

{{define "models"}}{{template "top" .}}<main>
<h1>Models</h1>
<p class="fresh">Refreshes every 10 seconds.</p>
<p class="section-note">Last 15 minutes.</p>
{{template "status" .}}
{{if .Empty}}<p>No engines.</p>{{else}}<div class="table-wrap"><table>
<thead><tr><th>Model</th><th>State</th><th>Engines</th><th>Requests/s</th><th>Error</th><th>Request</th><th>Queue</th><th>Compute</th><th>Queue depth</th></tr></thead>
<tbody>
{{range .Rows}}<tr class="model"><td>{{.Name}}</td><td><span class="{{.Class}}">{{.State}}</span></td><td>{{range .Engines}}<a href="/engines/{{.ServerID}}">{{.ServerID}}</a> {{end}}</td><td>{{if .Requests.Known}}{{.Requests.Text}}{{else}}<span class="unknown">unknown</span>{{end}}</td><td{{if .Errors.Bad}} class="state-unavailable"{{end}}>{{if .Errors.Known}}{{.Errors.Text}}{{else}}<span class="unknown">unknown</span>{{end}}</td><td>{{if .Request.Known}}{{.Request.Text}}{{else}}<span class="unknown">unknown</span>{{end}}</td><td>{{if .Queue.Known}}{{.Queue.Text}}{{else}}<span class="unknown">unknown</span>{{end}}</td><td>{{if .Compute.Known}}{{.Compute.Text}}{{else}}<span class="unknown">unknown</span>{{end}}</td><td>{{.Pending}}</td></tr>
{{end}}</tbody>
</table></div>
{{end}}</main>
{{template "bottom" .}}{{end}}

{{define "engine"}}{{template "top" .}}<main>
<p class="fresh">Refreshes every 10 seconds.</p>
{{template "status" .}}
{{if eq .Kind "stale"}}<h1 class="stale">This engine is stale.</h1>
{{else if eq .Kind "unknown"}}<h1 class="unknown">This engine is unknown.</h1>
{{else}}<h1>{{.Body.Heading}}</h1>
{{if .Body.Fresh}}<p class="fresh">Last snapshot {{.Body.Fresh}}</p>{{end}}
<dl>
<dt>Server id</dt><dd>{{.Body.ServerID}}</dd>
<dt>Node</dt><dd{{if .Body.NodeUnknown}} class="unknown"{{end}}>{{.Body.Node}}</dd>
<dt>Application</dt><dd{{if .Body.ApplicationUnknown}} class="unknown"{{end}}>{{.Body.Application}}</dd>
<dt>Live</dt><dd><span class="chip {{if .Body.Live}}chip-on{{else}}chip-off{{end}}">Live</span></dd>
<dt>Ready</dt><dd><span class="chip {{if .Body.Ready}}chip-on{{else}}chip-off{{end}}">Ready</span></dd>
<dt>NATS connected</dt><dd><span class="chip {{if .Body.NATS}}chip-on{{else}}chip-off{{end}}">NATS</span></dd>
<dt>ONNX Runtime</dt><dd>{{if .Body.RuntimeKnown}}<span class="chip">{{.Body.Runtime}}</span>{{else}}<span class="chip unknown">unknown</span>{{end}}</dd>
<dt>Execution providers</dt><dd>{{if .Body.Providers}}{{range .Body.Providers}}<span class="chip">{{.}}</span> {{end}}{{else}}<span class="chip unknown">unknown</span>{{end}}</dd>
</dl>
<section class="stats">
{{range .Body.Stats}}<article class="stat"><p class="stat-label">{{.Label}}</p><p class="stat-value{{if not .Known}} unknown{{else if .Bad}} state-unavailable{{end}}">{{.Text}}</p></article>
{{end}}</section>
{{if .Body.Filters}}<p class="filter">{{range .Body.Filters}}<a class="chip{{if .Active}} chip-on{{end}}" href="{{.Href}}">{{.Label}}</a>{{end}}</p>{{end}}
<p class="section-note">Last 15 minutes.</p>
<div class="charts">
{{range .Body.Charts}}<section class="card chart">
<h2>{{.Title}}</h2>
<p class="unit">{{.Unit}}</p>
{{if .Empty}}<p class="unknown">{{.Empty}}</p>{{else}}{{.SVG}}
<ul class="legend">{{range .Legend}}<li class="{{.Class}}">{{.Label}}</li>{{end}}</ul>{{end}}
</section>
{{end}}</div>
<div class="versions">
{{range .Body.Models}}<section class="card version">
<h2>{{.Name}}</h2>
<p><span class="chip">version:{{.Version}}</span></p>
<p>Queue depth {{.Pending}}</p>
<p class="counts"><span class="chip{{if gt .Success 0}} chip-on{{end}}">Success {{.Success}}</span><span class="chip{{if gt .Rejected 0}} chip-off{{end}}">REJECTED {{.Rejected}}</span><span class="chip{{if gt .Canceled 0}} chip-off{{end}}">CANCELED {{.Canceled}}</span><span class="chip{{if gt .Backend 0}} chip-off{{end}}">BACKEND {{.Backend}}</span><span class="chip{{if gt .Other 0}} chip-off{{end}}">OTHER {{.Other}}</span><span class="chip{{if gt .Failures 0}} chip-off{{end}}">Failures {{.Failures}}</span></p>
<p{{if not .BatchKnown}} class="unknown"{{end}}>Average batch size {{.Batch}}</p>
<p>State <span class="{{.StateClass}}">{{.State}}</span></p>
{{if .Reason}}<p>Reason {{.Reason}}</p>{{end}}
<p{{if not .RateKnown}} class="unknown"{{end}}>Request rate {{.Rate}}</p>
<p{{if not .RequestKnown}} class="unknown"{{end}}>Request latency {{.Request}}</p>
<p{{if not .QueueKnown}} class="unknown"{{end}}>Queue latency {{.QueueLatency}}</p>
<p{{if not .ComputeKnown}} class="unknown"{{end}}>Compute latency {{.ComputeText}}</p>
</section>
{{end}}</div>
{{end}}</main>
{{template "bottom" .}}{{end}}
`

var views = template.Must(template.New("views").Parse(strings.Replace(markup, "/*STYLE*/", stylesheet, 1)))
