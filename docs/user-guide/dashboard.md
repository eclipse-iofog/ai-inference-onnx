# Read the dashboard

Open port 8080. The three views are Nodes, Models, and Engine. The dashboard does not run inference.

Each page reloads every 10 seconds. That matches the default snapshot interval. The engine view shows the time of the newest snapshot.

A rate or a latency needs two snapshots. Until the next snapshot arrives, that value is `unknown`. Latency is the time added since the previous snapshot, divided by the successes added since that snapshot. It is not the average since the process started. Durations are milliseconds. When the fastest and slowest lines differ by ten times or more, those graphs use a log scale. An interval with no new success leaves a gap on the latency graph. The graphs keep the last 15 minutes.

Nodes is one card per node. Each engine shows whether it is live and ready, whether NATS is connected, the request rate, the error percentage, the slowest model latency, and the queue depth.

Models is one row per model name. Engines that serve that name are grouped on the row. Latency is the sum of the duration added on those engines divided by the sum of the successes added.

Engine is one microservice. The graphs are request rate, request latency, queue latency, compute latency, and queue depth. One line is one model version. Success and failure are separate rate lines, and a failure reason is drawn when that reason increased. A model link above the graphs limits the lines to that model. Below the graphs, each model version shows queue depth, success and failure, average batch size, and model state. The provider image is unknown.

An engine is stale at 30 seconds and drops off the live view.

If either NATS variable is missing, every view shows `NATS is not configured.` A closed port shows `NATS is not connected.`

`GET /health` is 200. That means the process can answer. An infer path is 404.

The dashboard has its own `LOG_LEVEL`, `TLS_ENABLED`, `TLS_CERT`, `TLS_KEY`, and `TLS_CA`. It does not read the engine config file. Those settings, and the image name, are in [configuration](../reference/configuration.md) and [images](../reference/images.md).
