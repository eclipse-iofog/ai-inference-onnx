# REST

REST listens on port 8000. With [TLS](configuration.md) off, which is the default, the listener is plain HTTP. When TLS is on, it presents the certificate named in the configuration. Health stays open in every [authentication](auth.md) mode. Every other call on this port uses the mode you set.

The error body is `{"error":"..."}`. It does not echo token claims.

## Calls

| Call | Permission |
|---|---|
| `GET /v2/health/live`, `GET /v2/health/ready` | Open |
| `GET /v2` | `server` `get` |
| `GET /v2/models` | `models` `get`. Body `{"models":["name", ...]}` |
| `GET /v2/models/{name}`, `GET /v2/models/{name}/ready` | `models/<name>` `get` |
| `POST /v2/models/{name}/infer` | `models/<name>/infer` `create` |
| `POST /v2/repository/index` | `repository/index` `get` |
| `POST /v2/repository/models/{name}/load`, `POST /v2/repository/models/{name}/unload` | Not performed. HTTP 501 |

Model metadata, model ready, and infer also accept `/versions/{version}`. Omit the version and the server chooses. A version is a decimal integer with no leading zero. `latest` and `v1` are 404. Which file that version serves is described in [the repository](repository.md).

`GET /v2/models` lists names this engine can run. `POST /v2/repository/index` lists every child directory. Each row has `name`, `version`, `state`, and `reason`. The body `{"ready": true}` keeps only models that can serve. An empty body is 400. The call is a POST because the filter is in the body. The permission is `get`.

States are `READY`, `LOADING`, and `UNAVAILABLE`. `reason` is set when the state is not `READY`. There is no `UNLOADING` state.

Server metadata `name` is the [server id](configuration.md). `version` is the engine version linked into the image, or `dev` when the binary was built without one. The extensions are `binary_tensor_data`, `model_repository`, `parameters`, and `schedule_policy`, then `execution_providers:` and `ort_version:`. `execution_providers:` lists the providers this process appended, in that order, separated by commas. A CPU image is `execution_providers:CPUExecutionProvider`. A TensorRT image is `execution_providers:TensorrtExecutionProvider,CUDAExecutionProvider`. `ort_version:` is the ONNX Runtime version of the loaded library, for example `ort_version:1.31.0`. Both values are empty after the colon when that library did not load. gRPC and NATS return the same extensions. It does not advertise unloading dependents. Load and unload are not available, because the platform places and replaces the files.

## Infer

The committed add model takes float32 inputs `A` and `B`, each of shape `[2, 3]`, and returns float32 `C`. Ones plus twos are threes. The served version is `"1"`.

```bash
curl -sS -X POST "http://127.0.0.1:8000/v2/models/add/infer" \
  -H "Content-Type: application/json" \
  -d '{"inputs":[{"name":"A","datatype":"FP32","shape":[2,3],"data":[1,1,1,1,1,1]},{"name":"B","datatype":"FP32","shape":[2,3],"data":[2,2,2,2,2,2]}]}'
```

In `static` or `edgelet` mode, add `Authorization: Bearer` with the credential from [authentication](auth.md). A missing or invalid token is 401. A valid token whose rule does not allow the call is 403.

The response carries `model_version` `"1"` and output `C`:

```json
{"model_name":"add","model_version":"1","id":"","outputs":[{"name":"C","datatype":"FP32","shape":[2,3],"data":[3,3,3,3,3,3]}]}
```

`id` is echoed when the request sets it. The client sends the full tensor, including the leading dimension. JSON numbers are the natural JSON type. `BOOL` is `true` or `false`. A number that does not fit the declared type is 400. `FP16` and `BF16` are raw little-endian bytes, not JSON numbers.

`binary_data_size` marks an input whose bytes follow the JSON, in input order. `Inference-Header-Content-Length` is the JSON length. `Content-Length` is the whole body. A header length of 0 is a raw-binary body. That form is accepted only when the model has one input and at most one variable dimension, so the add model rejects it with 400.

`sequence_id`, `sequence_start`, and `sequence_end` are 400. There is no sequence batcher, and ignoring those keys would run a sequence request as a stateless one. `timeout` is an integer in microseconds. `0` means the queue does not expire the request. A non-zero timeout that elapses while the request is still waiting is 504. Any other parameter key is accepted and ignored, and it is not echoed.

A model that is `LOADING`, or a full queue, is 503. An unknown model or version is 404. A model that is `UNAVAILABLE` is 400.

## Load

`POST /v2/repository/models/add/load` and `POST /v2/repository/models/add/unload` answer HTTP 501:

```json
{"error":"loading and unloading models is not supported"}
```

The same status is returned for a name that is not in the repository. The engine does not look the name up.
