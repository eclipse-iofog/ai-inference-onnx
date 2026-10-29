# Run the container

This page starts the CPU image, then reads health, server metadata, the model list, and one signature. Infer comes after you have seen the signature. The image tag and the device variants are in [Images](../reference/images.md).

## Start it

`AUTH_MODE` is required. A missing value exits the process. `disabled` performs no check. Use it for this local run. REST is 8000, gRPC is 8001, and metrics are 8002. Mount a model directory read-only at `/models`.

The committed `add` model is float32 inputs `A` and `B`, each of shape `[2, 3]`, and float32 output `C`. Put it at `models/add/model.onnx` if you are using the file from this repository. Any other directory name becomes the model name.

```bash
docker run --rm --read-only --tmpfs /tmp \
  -e AUTH_MODE=disabled \
  -p 8000:8000 \
  -p 8001:8001 \
  -p 8002:8002 \
  -v "$PWD/models:/models:ro" \
  -v engine-cache:/var/cache/inference \
  ghcr.io/eclipse-iofog/ai-inference-onnx:0.1.0-ort1.31.0-cpu
```

`podman run` takes the same image and the same variables. `--read-only` makes the root filesystem read-only. `--tmpfs /tmp` gives the process a writable `/tmp`. The named volume `engine-cache` is mounted read-write at `/var/cache/inference`. The CPU image can serve `add` without that volume. CUDA writes its cache under `/var/cache/inference/cuda`, and TensorRT writes its engine cache and timing cache under `/var/cache/inference/tensorrt`. Mount the volume before you run those images. The volume must be writable by uid `10001`. A root-owned empty volume leaves the process unable to create those directories. [Choose an image and pass a device](devices.md) is the full container shape, including host group ids.

The process is `10001:10001`. It needs no capability, and it listens above 1024. Leave the user and the group at those ids when you add a device later.

Wait until the log line `model` reports the model `READY`. A failure is a `warn` line with `reason`.

## Read live and ready

```bash
curl -sS -D - http://127.0.0.1:8000/v2/health/live -o /dev/null
curl -sS -D - http://127.0.0.1:8000/v2/health/ready -o /dev/null
```

Both are open, with no token, in every authentication mode. Live is 200 when the process can answer, including when no model has loaded. Ready is 200 when at least one ONNX model is `READY`. Ready is not 200 while the only model is still `LOADING`.

## Read the server

```bash
curl -sS http://127.0.0.1:8000/v2
```

`name` is the server id for this process. A local run generates a new id unless `ENGINE_SERVER_ID` or `EDGELET_MICROSERVICE_UID` is set. `version` is the engine version linked into the image. The extensions name the protocol features, then the execution providers and the ONNX Runtime version:

```json
{
  "name": "<server id>",
  "version": "0.1.0",
  "extensions": [
    "binary_tensor_data",
    "model_repository",
    "parameters",
    "schedule_policy",
    "execution_providers:CPUExecutionProvider",
    "ort_version:1.31.0"
  ]
}
```

A CPU image reports `CPUExecutionProvider`. A TensorRT image reports `TensorrtExecutionProvider,CUDAExecutionProvider`, in that order. An empty value after either colon means the library did not load. Server ready stays false in that case. The `runtime` log line records the same chain.

## List models, then read one signature

```bash
curl -sS http://127.0.0.1:8000/v2/models
curl -sS http://127.0.0.1:8000/v2/models/add
curl -sS -D - http://127.0.0.1:8000/v2/models/add/ready -o /dev/null
```

`GET /v2/models` returns the names this engine can run:

```json
{"models":["add"]}
```

A directory with no ONNX file is absent from this list. `GET /v2/models/add` returns the signature the ONNX file declared:

```json
{
  "name": "add",
  "versions": ["1"],
  "platform": "onnxruntime_onnx",
  "inputs": [
    {"name": "A", "datatype": "FP32", "shape": [2, 3]},
    {"name": "B", "datatype": "FP32", "shape": [2, 3]}
  ],
  "outputs": [
    {"name": "C", "datatype": "FP32", "shape": [2, 3]}
  ]
}
```

Every later infer call uses these names, datatypes, and shapes. A `-1` in a shape is a variable dimension. You choose the length in the request. The `add` model has fixed lengths, so the request shape is `[2, 3]` and no other shape.

`GET /v2/models/add/ready` is 200 with an empty body when version `"1"` can serve. A model that is `LOADING` is 503 with `{"error":"model is loading"}`. A model that is `UNAVAILABLE` is 400, and the error text is the load reason.

Omit `/versions/1` and the server chooses the served version. For this flat model that version is `"1"`. `latest` and `v1` are 404. A version is a decimal integer with no leading zero.

## See every directory, including the ones that cannot run

```bash
curl -sS -X POST http://127.0.0.1:8000/v2/repository/index \
  -H "Content-Type: application/json" \
  -d '{}'
```

The response is a JSON array. Each row has `name`, `version` when a version was selected, `state`, and `reason`. `reason` is empty when `state` is `READY`. `{"ready": true}` keeps only rows that can serve. An empty body is 400. `GET /v2/models` hides a directory that selected no version. The index shows it, which is how you read `no ONNX model in directory` and the other load reasons.

The next page is [Choose the ONNX file](choose-onnx-file.md). The first infer is [Call infer on REST](infer-rest.md), after the model file and the scheduler.
