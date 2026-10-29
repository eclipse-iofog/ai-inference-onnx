# Read the dashboard

Open port 8080. The three views are Nodes, Models, and Engine. The dashboard does not run inference, and it does not read the engine config file. It subscribes to `inference.metrics.engines.>`. There is no queue group, so two dashboards both receive every snapshot.

If either NATS variable is missing, every view shows `NATS is not configured.` A closed port shows `NATS is not connected.` `GET /health` is 200 when the dashboard process can answer. An infer path is 404.

The engine publishes one JSON snapshot every 10 seconds, and again when a model changes state. The interval is `METRICS_INTERVAL`. Core NATS, no reply. A subscriber that is down drops the sample. The device does not store it. Each dashboard page reloads every 10 seconds, which matches that default. The engine view shows the time of the newest snapshot.

An engine is stale at 30 seconds and drops off the live view. A rate or a latency uses the last 15 minutes. One snapshot leaves that value `unknown`. Latency is the time added in that window, divided by the successes added in that window. It is the rate over the window, not the average since the process started. A window with no requests shows a request rate of `0 /s` and an error of `0.0%`. A window with no successes leaves latency `unknown`. Durations are milliseconds. When the fastest and slowest lines differ by ten times or more, those graphs use a log scale. An interval with no new success leaves a gap on the latency graph. The graphs keep the last 15 minutes. Success is green and failure is red. Model lines use other colors.

## Nodes

Nodes is one card per node. Each engine shows whether it is live and ready, whether NATS is connected, the request rate, the error percentage, the slowest model latency, and the queue depth. The rate, the error percentage, and the latency are the last 15 minutes. Queue depth is the current number of waiting requests.

Start here when several engines publish. A card that disappears was stale for 30 seconds: the process stopped publishing, or NATS stopped delivering. A card that is live and not ready has no `READY` model. The repository index on that engine has the reason.

## Models

Models is one row per model name. Engines that serve that name are grouped on the row. Latency is the sum of the duration added on those engines in the last 15 minutes, divided by the sum of the successes added.

Use this view to compare one model name across replicas. The replicas do not share a queue. A high queue depth on one card and an empty queue on another means the NATS queue group, or the clients, are landing requests on one process.

## Engine

Engine is one microservice. Live, Ready, and NATS connected are badges. ONNX Runtime and each execution provider are badges, in the order the process appended the providers. An empty version or an empty chain is unknown, which means that library did not load. The graphs are request rate, request latency, queue latency, compute latency, and queue depth. One line is one model version, labeled `name:version`. Success and failure are separate rate lines, and a failure reason is drawn when that reason increased. A model link above the graphs limits the lines to that model. Below the graphs, model cards sit side by side, at most four on a row. The model name is the card title and the version is a badge. Each card shows queue depth, success and failure badges, average batch size, and model state.

Read queue latency against compute latency when a model is slow. Queue latency is the wait for the one slot. Compute latency is the ONNX Runtime run. A deep queue with a short compute time means the cap, the delay, or the arrival rate is holding requests. [How a request is scheduled](schedule.md) is that queue. Average batch size on the card is `inference_count` divided by `inference_exec_count` for the samples in view.

The failure reasons drawn on the rate graph are `REJECTED` (the queue is full, or the request timed out while waiting), `CANCELED`, `BACKEND` (ONNX Runtime returned an error), and `OTHER`. An unknown model name does not create a series.

The dashboard has its own `LOG_LEVEL`, `TLS_ENABLED`, `TLS_CERT`, `TLS_KEY`, and `TLS_CA`. Those settings, and the image name, are in [Configuration](../reference/configuration.md) and [Images](../reference/images.md). The scrape names are in [Metrics](../reference/metrics.md).

The next page is [Operate the engine](operate.md).
