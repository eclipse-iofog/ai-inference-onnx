# Call infer on gRPC

gRPC listens on port 8001. With TLS off, the listener is h2c. The service is `inference.GRPCInferenceService`. The server does not publish a reflection service. Point the client at [`api/openinference/v2/open_inference_grpc.proto`](../../api/openinference/v2/open_inference_grpc.proto) in this repository.

The RPCs match the REST calls you already made. An empty `model_version` means the same model as a REST URL with no `/versions/` segment. Health stays open: `ServerLive` and `ServerReady`. Every other RPC uses the authentication mode from [Choose authentication and TLS](auth-tls.md). Send `Authorization: Bearer` in metadata, the same value REST takes in the header.

| RPC | REST |
|---|---|
| `ServerLive`, `ServerReady` | `GET /v2/health/live`, `GET /v2/health/ready` |
| `ServerMetadata` | `GET /v2` |
| `ModelMetadata`, `ModelReady` | Model metadata and model ready |
| `ModelInfer` | Infer |
| `RepositoryIndex` | `POST /v2/repository/index` |
| `RepositoryModelLoad`, `RepositoryModelUnload` | Load and unload, both `UNIMPLEMENTED` |

## Infer the add model

The `add` model takes float32 inputs `A` and `B`, each of shape `[2, 3]`, and returns float32 `C`. Ones plus twos are threes. The response field `model_version` is `"1"`.

```bash
grpcurl -plaintext \
  -proto api/openinference/v2/open_inference_grpc.proto \
  -d '{"modelName":"add","modelVersion":"1","inputs":[{"name":"A","datatype":"FP32","shape":[2,3],"contents":{"fp32Contents":[1,1,1,1,1,1]}},{"name":"B","datatype":"FP32","shape":[2,3],"contents":{"fp32Contents":[2,2,2,2,2,2]}}]}' \
  127.0.0.1:8001 inference.GRPCInferenceService/ModelInfer
```

`grpcurl` prints the protobuf JSON names. `modelVersion` is `model_version`. Omit `modelVersion` and the server chooses the served version. For this flat model that version is still `"1"`.

## Which contents field matches the datatype

| Datatype | `contents` field |
|---|---|
| `BOOL` | `boolContents` |
| `INT8`, `INT16`, `INT32` | `intContents` |
| `INT64` | `int64Contents` |
| `UINT8`, `UINT16`, `UINT32` | `uintContents` |
| `UINT64` | `uint64Contents` |
| `FP32` | `fp32Contents` |
| `FP64` | `fp64Contents` |
| `BYTES` | `bytesContents` |
| `FP16`, `BF16` | None. Send `raw_input_contents` |

`raw_input_contents` carries each input as little-endian bytes, in input order, instead of `contents`. One input uses one contents field or one raw slice, not both. `FP16` and `BF16` travel as raw bytes only. A JSON number for either type is rejected. `int64Contents` and `uint64Contents` carry the full integer. REST JSON accepts `INT64` and `UINT64` only inside ±2^53.

`raw_output_contents` carries an output the same way when that output is binary. `INT64`, `UINT64`, `FP16`, and `BF16` outputs are binary unless you are reading them from `raw_output_contents` already. The request flag is `binary_data_output`, and one output's `binary_data` overrides it, the same as on REST.

`sequence_id`, `sequence_start`, and `sequence_end` are `INVALID_ARGUMENT`.

## Status codes

The HTTP column is the status [Call infer on REST](infer-rest.md) returns for the same outcome.

| gRPC | HTTP | When |
|---|---|---|
| `OK` | 200 | The outputs are the result |
| `INVALID_ARGUMENT` | 400 | The tensor or the parameter cannot run, including a body over the payload limit |
| `FAILED_PRECONDITION` | 400 | The model is `UNAVAILABLE` |
| `UNAUTHENTICATED` | 401 | The token is missing or invalid |
| `PERMISSION_DENIED` | 403 | The token's rule does not allow the call |
| `NOT_FOUND` | 404 | The model or the version is unknown |
| `CANCELED` | 400 | The caller canceled |
| `DEADLINE_EXCEEDED` | 504 | The queue timeout elapsed, or the call deadline elapsed |
| `UNAVAILABLE` | 503 | The model is `LOADING`, or the queue is full |
| `UNIMPLEMENTED` | 501 | Load and unload |
| `INTERNAL` | 500 | The run did not finish |

`RepositoryModelLoad` and `RepositoryModelUnload` return `UNIMPLEMENTED` with the text `loading and unloading models is not supported`. The engine does not look the model name up.

The next page is [Choose authentication and TLS](auth-tls.md).
