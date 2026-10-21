# Metrics

`GET /metrics` listens on port 8002. The path is open in every authentication mode. The listener stays plain HTTP when [TLS](configuration.md) is enabled on REST and gRPC. A scrape does not increment a counter. Any other path on this port is 404.

The published prefix is `inference_`. It is not `nv_inference_`.

Prometheus labels are `model`, `version`, and `reason`. `reason` is only on `inference_request_failure`. The server id is not a label. A scrape distinguishes targets with `instance`.

## Names

One success is one request that returned a result. A batch counts as one request. `inference_count` adds the batch size. `inference_exec_count` adds one for each batch that actually ran. Average batch size is `inference_count` divided by `inference_exec_count`.

| Name | Type | Meaning |
|---|---|---|
| `inference_request_success` | counter | Requests that returned a result |
| `inference_request_failure` | counter | Failures, labeled with `reason` |
| `inference_count` | counter | Sum of batch sizes |
| `inference_exec_count` | counter | Batches actually executed |
| `inference_pending_request_count` | gauge | Requests waiting on the one in-flight batch |
| `inference_request_duration_us` | counter | Cumulative end-to-end time, in microseconds |
| `inference_queue_duration_us` | counter | Cumulative time waiting to run, in microseconds |
| `inference_compute_infer_duration_us` | counter | Cumulative ONNX Runtime run time, in microseconds |
| `inference_model_ready` | gauge | `1` when that version is `READY`, otherwise `0` |

The three duration series are counters of microseconds. They are not histograms and not summaries.

`reason` is one of four labels:

| `reason` | When |
|---|---|
| `REJECTED` | The queue is full, or the request timed out while waiting |
| `CANCELED` | The caller canceled |
| `BACKEND` | ONNX Runtime returned an error |
| `OTHER` | Any other failure of a selected version |

An unknown model name does not create a series. The Go process series stay. They describe this container. `nv_gpu_power_usage`, `nv_gpu_utilization`, and `nv_gpu_memory_*` are published only when NVML loads, which is the CUDA and TensorRT images.

These KServe histogram names are not published: `request_preprocess_seconds`, `request_predict_seconds`, `request_postprocess_seconds`, and `request_explain_seconds`. This engine has no preprocess or explain step.

## Snapshot

Every 10 seconds, and again when a model changes state, the engine publishes one JSON snapshot on `inference.metrics.engines.<serverId>`. The interval is `metricsInterval`. Core NATS, no reply. A subscriber that is down drops the sample. The device does not store it.

The dashboard subscribes to `inference.metrics.engines.`. There is no queue group, so two dashboards both receive every snapshot. The document uses schema `1` and the counter names above. It carries server live and ready, each model's state and reason, and `nats_connected`. It does not carry tensors. How `serverId` is chosen is described in [configuration](configuration.md).
