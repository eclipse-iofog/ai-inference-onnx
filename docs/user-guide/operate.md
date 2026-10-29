# Operate the engine

Use this page when a call fails or a model never becomes `READY`. The log is one JSON object per line on stdout. After settings load, every line includes `service` (`inference`) and `server_id`. The message is the event name. The detail is in attributes. A failure is an `error` attribute. The level table is in [Configuration](../reference/configuration.md).

The log never contains the static token, a bearer token, a JWT, the creds file, a private key, or tensor values.

## Follow one failure

1. `GET /v2/health/live`. A failed connection means the process is down or the port is unpublished. Live 200 means the process can answer, including when every model is `UNAVAILABLE`.
2. `GET /v2/health/ready`. Ready 200 means at least one ONNX model is `READY`. Ready false with live true means no model can serve.
3. `POST /v2/repository/index` with `{}`. Read `state` and `reason` for the directory. `GET /v2/models` hides a directory that selected no version. The index shows it.
4. Read the log for that model. `info` message `model` is `READY` or a load starting. `warn` message `model` is `UNAVAILABLE` and includes `reason`.

Reasons you will meet, and what to change:

| `reason` | Change |
|---|---|
| `no ONNX model in directory` | Put an `.onnx` file where [Choose the ONNX file](choose-onnx-file.md) looks |
| `several ONNX files and no model.onnx` | Name one file with `default_model_filename`, or add `model.onnx` |
| `num_versions must be at least 1` | Set `version_policy.latest.num_versions` to at least 1 |
| `version policy selected no version` | Point `specific.versions` at directories that exist |
| `config.pbtxt could not be parsed` | Fix the file. The four fields this engine reads are in [Write config.pbtxt](config-pbtxt.md) |
| `name` does not match the directory | Make `name` match the directory, or remove the field |
| Platform or backend is not ONNX Runtime | Use `onnxruntime_onnx` and `onnxruntime`, or remove the fields |
| `sequence_batching`, `ensemble_scheduling`, or `response_cache` is not supported | Remove that block |
| `knowledge "<name>" is missing` | Create `{KNOWLEDGE_PATH}/<name>/`. The engine does not read the files inside it |
| The execution provider could not start | The `runtime` line names the chain. The engine does not fall back to CPU. Fix the device, the `supplementalGroups`, or the image tag |

A provider that started and then rejected a node may still run that node on CPU. That is graph partitioning. A provider that failed to start is `UNAVAILABLE` for the model.

## Read the log on purpose

| Level | Message | What it tells you |
|---|---|---|
| `info` | `start` | The safe settings: version, auth mode, ports, TLS, paths, queue, payload, threads, batch delay, metrics interval, whether NATS is enabled. The static token, the NATS URL, the credentials path, and the certificate paths are absent |
| `info` | `listener` | HTTP, gRPC, and metrics are bound |
| `info` | `runtime` | The ONNX Runtime version and the execution providers, in selection order |
| `info` | `model` | A model is `READY`, or a load started |
| `warn` | `model` | A model is `UNAVAILABLE`, with `reason` |
| `warn` | `auth` | One denied REST or gRPC call. `decision` is `unauthenticated` or `forbidden` |
| `warn` | `infer` | A caller was rejected. Attributes include `transport`, the model, the version, the request id, the HTTP status, and `error_kind` |
| `warn` | `cache` | The user cache directory could not be read. TensorRT cache options stay off |
| `warn` | `metrics` | A snapshot could not be published. Further failures stay quiet until a publish succeeds or one metrics interval has passed |
| `error` | `configuration`, `authentication`, `listen`, `runtime`, `models`, `watch`, `serve`, `stop`, `nats`, `infer` | A step the process attempted and could not finish |
| `debug` | `infer`, `batch` | A call that finished, and a batch that ran. `duration_us` is the call. `queue_wait_us` and `run_us` are the batch |

Set `LOG_LEVEL=debug` when you need the successful calls. Success stays at debug. An id longer than 256 bytes is cut.

NATS connected, disconnected, and closed are `info`. A retry is `warn`. The process stays up and retries. REST and gRPC keep serving.

## Probe, scrape, and shut down

Probe `GET /v2/health/live` for process liveness and `GET /v2/health/ready` for "at least one model can serve". Both are open. A command that runs `curl` inside the container works on the UBI image. The riscv64 image has no shell and no `curl`. Probe those URLs from outside the container on riscv64.

`GET /metrics` on port 8002 is open, stays plain HTTP when TLS is on, and does not increment a counter. The prefix is `inference_`. Prometheus labels are `model`, `version`, and `reason`. The server id is not a label. A scrape distinguishes targets with `instance`. The names and the four `reason` labels are in [Metrics](../reference/metrics.md). `nv_gpu_power_usage`, `nv_gpu_utilization`, and `nv_gpu_memory_*` are published only when NVML loads, which is the CUDA and TensorRT images.

On a signal the log says `stopping`, then `stopped` when shutdown finishes. The in-flight batch is the run already inside the slot. Callers waiting in the queue do not start a new run.

## Change a running engine

| Goal | What you change | Restart |
|---|---|---|
| Replace a model | The ONNX file or its `.onnx.data` sibling | No. [Read a reload](reload.md). Callers retry 503 |
| Hold a model for an artifact | The `knowledge` file and the named directory | No |
| Raise the queue, the threads, the payload, or the batch delay | `QUEUE_CAPACITY`, `INTRA_OP_THREADS`, `INTER_OP_THREADS`, `MAX_PAYLOAD_BYTES`, `BATCH_DELAY_US` | Yes |
| Turn TLS or `static` on, or change the static token | `TLS_*`, `AUTH_MODE`, `AUTH_STATIC_TOKEN` | Yes |
| Swap the Edgelet public key | The `edgelet.jwk` file | No. The process loads the new key |
| Keep a TensorRT engine across restart | The volume at `/var/cache/inference` | The volume has to be read-write and writable by uid `10001` |
| Add a GPU group | `supplementalGroups` with the host ids from `getent group render` | Yes, the container is recreated |
| Pin the release | The image tag `0.1.0-ort1.31.0-<variant>` | There is no `latest` tag |

`INTRA_OP_THREADS` and `INTER_OP_THREADS` default to 1. Raise them when a CPU image should use more cores. They do not add a second execution slot. `QUEUE_CAPACITY` defaults to 8 waiting requests per version. A full queue is 503 for the caller that did not fit. Replicas do not share a queue. The NATS queue group spreads requests across processes. Each process batches only what it receives.

You have now walked the path from an empty container to a node. The [reference](../reference/) is the lookup for the keys, the subjects, the metric names, and the image tags.
