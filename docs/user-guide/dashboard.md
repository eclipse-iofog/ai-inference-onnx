# Read the dashboard

Open port 8080. The three views are Nodes, Models, and Engine. The dashboard does not run inference.

Nodes is one card per node. Each card lists the engines on that node, whether each is live and ready, and whether NATS is connected.

Models is one row per model name. Engines that serve that name are grouped on the row. Latency across those engines is the sum of the duration counters divided by the sum of the success counters.

Engine is one microservice. It shows per-model queue depth, success and failure, average batch size, model state, and the provider image.

An engine is stale at 30 seconds and drops off the live view.

If either NATS variable is missing, every view shows `NATS is not configured.` A closed port shows `NATS is not connected.`

`GET /health` is 200. That means the process can answer. An infer path is 404.

The dashboard has its own `LOG_LEVEL`, `TLS_ENABLED`, `TLS_CERT`, `TLS_KEY`, and `TLS_CA`. It does not read the engine config file. Those settings, and the image name, are in [configuration](../reference/configuration.md) and [images](../reference/images.md).
