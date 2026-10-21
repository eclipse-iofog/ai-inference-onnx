# Write config.pbtxt

An optional `config.pbtxt` sits in the model directory. With no file, the ONNX file is chosen as in [Choose the ONNX file](choose-onnx-file.md), the batch default for a batchable model is 1, and the version policy is latest of 1. The signature is read from the `.onnx` file.

This engine reads four fields.

`default_model_filename` names the ONNX file when several `.onnx` files sit at the top of the directory and none is `model.onnx`.

`max_batch_size` replaces the batch default. The batch default for a batchable model is 1. `0` means the tensor is run as sent.

`version_policy` selects every version, the latest of N, or a specific list.

`dynamic_batching.max_queue_delay_microseconds` is that model's batch delay. A delay of 0 runs a lone request immediately. Preferred batch sizes, priority levels, and preserve-ordering stay unused.

The common file names one ONNX file, keeps the latest of 1, and sets the batch size:

```
default_model_filename: "wafer-defect.onnx"
max_batch_size: 1
version_policy: { latest: { num_versions: 1 } }
```

A delay of 0 is the default when `dynamic_batching` is absent. When the block is absent, the server delay applies. That setting is in [configuration](../reference/configuration.md).

## What leaves the model UNAVAILABLE

`name`, when set, must match the directory name. A name that does not match the directory makes that model `UNAVAILABLE`.

`platform`, when set, is `onnxruntime_onnx`. The backend string, when set, is `onnxruntime`. Any other platform or backend makes that model `UNAVAILABLE`.

`sequence_batching`, `ensemble_scheduling`, or `response_cache` makes that model `UNAVAILABLE`. A file that cannot be parsed makes that model `UNAVAILABLE`.

`instance_group`, input and output blocks, warmup samples, and TensorRT optimization are unused, and the model still loads.
