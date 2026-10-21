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
}
.sidebar {
  width: 16rem;
  background: var(--page);
  border-right: 1px solid var(--rule);
  padding: 1.5rem 1rem;
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
  padding: 1.5rem;
}
main a {
  color: var(--nav);
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
table {
  width: 100%;
  border-collapse: collapse;
}
th, td {
  padding: 0.25rem 0.75rem;
  border-bottom: 1px solid var(--rule);
  text-align: left;
}
`

const markup = `
{{define "top"}}<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
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
{{template "status" .}}
{{if .Empty}}<p>No engines.</p>{{end}}
{{range .Nodes}}<article class="card{{if .Unknown}} unknown{{end}}">
<h2>{{.Title}}</h2>
<ul>
{{range .Engines}}<li class="engine"><a href="/engines/{{.ServerID}}">{{.Label}}</a> Live {{.Live}} Ready {{.Ready}} NATS connected {{.NATS}}</li>
{{end}}</ul>
</article>
{{end}}</main>
{{template "bottom" .}}{{end}}

{{define "models"}}{{template "top" .}}<main>
<h1>Models</h1>
{{template "status" .}}
{{if .Empty}}<p>No engines.</p>{{else}}<table>
<thead><tr><th>Model</th><th>Engines</th><th>Latency (microseconds)</th></tr></thead>
<tbody>
{{range .Rows}}<tr class="model"><td>{{.Name}}</td><td>{{range .Engines}}<a href="/engines/{{.ServerID}}">{{.ServerID}}</a> {{end}}</td><td{{if not .Known}} class="unknown"{{end}}>{{.Latency}}</td></tr>
{{end}}</tbody>
</table>
{{end}}</main>
{{template "bottom" .}}{{end}}

{{define "engine"}}{{template "top" .}}<main>
{{template "status" .}}
{{if eq .Kind "stale"}}<h1 class="stale">This engine is stale.</h1>
{{else if eq .Kind "unknown"}}<h1 class="unknown">This engine is unknown.</h1>
{{else}}<h1>{{.Body.Heading}}</h1>
<dl>
<dt>Server id</dt><dd>{{.Body.ServerID}}</dd>
<dt>Node</dt><dd{{if .Body.NodeUnknown}} class="unknown"{{end}}>{{.Body.Node}}</dd>
<dt>Application</dt><dd{{if .Body.ApplicationUnknown}} class="unknown"{{end}}>{{.Body.Application}}</dd>
<dt>Live</dt><dd>{{.Body.Live}}</dd>
<dt>Ready</dt><dd>{{.Body.Ready}}</dd>
<dt>NATS connected</dt><dd>{{.Body.NATS}}</dd>
</dl>
<p>Provider image <span class="unknown">unknown</span></p>
{{range .Body.Models}}<section class="card version">
<h2>{{.Name}} {{.Version}}</h2>
<p>Queue depth {{.Pending}}</p>
<p>Success {{.Success}}</p>
<ul>
<li>REJECTED {{.Rejected}}</li>
<li>CANCELED {{.Canceled}}</li>
<li>BACKEND {{.Backend}}</li>
<li>OTHER {{.Other}}</li>
</ul>
<p>Failures {{.Failures}}</p>
<p{{if not .BatchKnown}} class="unknown"{{end}}>Average batch size {{.Batch}}</p>
<p>State <span class="{{.StateClass}}">{{.State}}</span></p>
{{if .Reason}}<p>Reason {{.Reason}}</p>{{end}}
</section>
{{end}}
<table class="samples">
<caption>Samples</caption>
<thead><tr><th>Receipt</th><th>Latency (microseconds)</th></tr></thead>
<tbody>
{{range .Body.Samples}}<tr class="sample"><td>{{.At}}</td><td{{if not .Known}} class="unknown"{{end}}>{{.Latency}}</td></tr>
{{end}}</tbody>
</table>
{{end}}</main>
{{template "bottom" .}}{{end}}
`

var views = template.Must(template.New("views").Parse(strings.Replace(markup, "/*STYLE*/", stylesheet, 1)))
