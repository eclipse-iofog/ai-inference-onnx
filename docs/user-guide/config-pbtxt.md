# Write config.pbtxt

An optional `config.pbtxt` sits in the model directory, beside the ONNX file or beside the version directories. It does not sit inside a version directory. With no file, the ONNX file is chosen as in [Choose the ONNX file](choose-onnx-file.md), a batchable model has batch cap 1, and the version policy is latest of 1. The signature still comes from the `.onnx` file. Input and output blocks in this file are unused, and the model still loads.

This engine reads four fields.

## Name the file

`default_model_filename` names the ONNX file when several `.onnx` files sit at the top of the directory and none is `model.onnx`.

```text
default_model_filename: "wafer-defect.onnx"
```

A name that does not match a top-level file leaves the model `UNAVAILABLE`. The reason names the file that was not found.

## Choose versions

`version_policy` selects every version, the latest of N, or a specific list. The directories are the numeric version directories from [Choose the ONNX file](choose-onnx-file.md).

Latest of 2:

```text
version_policy: { latest: { num_versions: 2 } }
```

`num_versions` is at least 1. A smaller value leaves the model `UNAVAILABLE` with reason `num_versions must be at least 1`.

Every version:

```text
version_policy: { all: {} }
```

A specific list:

```text
version_policy: { specific: { versions: [1, 3] } }
```

A list that matches no directory leaves the model `UNAVAILABLE` with reason `version policy selected no version`. Metadata for the model lists every version the policy kept. The first one is the version that serves when the URL omits `/versions/`.

## Set the batch cap

`max_batch_size` replaces the batch default.

```text
max_batch_size: 8
```

The default for a batchable model is 1. `0` runs each tensor as sent and does not stack requests. A model is batchable only when every input and every output has a dynamic leading dimension. The `add` model, with fixed shape `[2, 3]`, is not batchable, so this field does not stack its requests. [How a request is scheduled](schedule.md) is the rule, the queue, and the delay.

## Set this model's queue delay

`dynamic_batching.max_queue_delay_microseconds` is that model's batch delay, in microseconds. A delay of 0 runs a lone request immediately.

```text
dynamic_batching {
  max_queue_delay_microseconds: 5000
}
```

When the block is absent, the server delay applies. That setting is `BATCH_DELAY_US`, and the default is `0`. Preferred batch sizes, priority levels, and preserve-ordering stay unused. `priority` on a request is accepted and does not reorder the queue.

## A file you can copy

This file names one ONNX file, keeps the latest of 1, and sets the batch cap to 1:

```text
default_model_filename: "wafer-defect.onnx"
max_batch_size: 1
version_policy: { latest: { num_versions: 1 } }
```

## What leaves the model UNAVAILABLE

`name`, when set, must match the directory name. A name that does not match makes that model `UNAVAILABLE`.

`platform`, when set, is `onnxruntime_onnx`. The backend string, when set, is `onnxruntime`. Any other platform or backend makes that model `UNAVAILABLE`.

`sequence_batching`, `ensemble_scheduling`, or `response_cache` makes that model `UNAVAILABLE`. The reason names the field. A file that cannot be parsed makes that model `UNAVAILABLE` with reason `config.pbtxt could not be parsed`.

`instance_group`, input and output blocks, warmup samples, and TensorRT optimization are unused, and the model still loads.

After the file is in place, read `POST /v2/repository/index` and confirm `state` is `READY`. Then read the signature again. The next page is [How a request is scheduled](schedule.md).
