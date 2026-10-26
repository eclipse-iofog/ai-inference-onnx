# Read the dashboard

Open port 8080. The three views are Nodes, Models, and Engine. The dashboard does not run inference.

Each page reloads every 10 seconds. That matches the default snapshot interval. The engine view shows the time of the newest snapshot.

A rate or a latency uses the last 15 minutes. One snapshot leaves that value `unknown`. Latency is the time added in that window, divided by the successes added in that window. It is not the average since the process started. A window with no requests shows a request rate of `0 /s` and an error of `0.0%`. A window with no successes leaves latency `unknown`. Durations are milliseconds. When the fastest and slowest lines differ by ten times or more, those graphs use a log scale. An interval with no new success leaves a gap on the latency graph. The graphs keep the last 15 minutes. Success is green and failure is red. Model lines use other colors.

Nodes is one card per node. Each engine shows whether it is live and ready, whether NATS is connected, the request rate, the error percentage, the slowest model latency, and the queue depth. The rate, the error percentage, and the latency are the last 15 minutes. Queue depth is the current number of waiting requests.

Models is one row per model name. Engines that serve that name are grouped on the row. Latency is the sum of the duration added on those engines in the last 15 minutes, divided by the sum of the successes added.

Engine is one microservice. Live, Ready, and NATS connected are badges. ONNX Runtime and each execution provider are badges, in the order the process appended the providers. An empty version or an empty chain is unknown. The graphs are request rate, request latency, queue latency, compute latency, and queue depth. One line is one model version, labeled `name:version`. Success and failure are separate rate lines, and a failure reason is drawn when that reason increased. A model link above the graphs limits the lines to that model. Below the graphs, model cards sit side by side, at most four on a row. The model name is the card title and the version is a badge. Each card shows queue depth, success and failure badges, average batch size, and model state.

An engine is stale at 30 seconds and drops off the live view.

If either NATS variable is missing, every view shows `NATS is not configured.` A closed port shows `NATS is not connected.`

`GET /health` is 200. That means the process can answer. An infer path is 404.

The dashboard has its own `LOG_LEVEL`, `TLS_ENABLED`, `TLS_CERT`, `TLS_KEY`, and `TLS_CA`. It does not read the engine config file. Those settings, and the image name, are in [configuration](../reference/configuration.md) and [images](../reference/images.md).
