# gRPC

gRPC listens on port 8001. With [TLS](configuration.md) off, which is the default, the listener is h2c. When TLS is on, it presents the same certificate as REST. The service is `inference.GRPCInferenceService`.

Health stays open in every [authentication](auth.md) mode: `ServerLive` and `ServerReady`. Every other RPC uses the mode you set. Send `Authorization: Bearer` in metadata, the same value REST takes in the header. A missing or invalid token is `UNAUTHENTICATED`. A valid token whose rule does not allow the call is `PERMISSION_DENIED`.

The calls match [REST](rest.md). An empty `model_version` means the same model as a REST URL with no `/versions/` segment.

| RPC | REST |
|---|---|
| `ServerLive`, `ServerReady` | `GET /v2/health/live`, `GET /v2/health/ready` |
| `ServerMetadata` | `GET /v2` |
| `ModelMetadata`, `ModelReady` | Model metadata and model ready |
| `ModelInfer` | Infer |
| `RepositoryIndex` | `POST /v2/repository/index` |
| `RepositoryModelLoad`, `RepositoryModelUnload` | Load and unload |

`RepositoryIndex` returns `name`, `version`, `state`, and `reason`. `ready: true` keeps only models that can serve.

## Infer

The add model takes float32 inputs `A` and `B`, each of shape `[2, 3]`, and returns float32 `C`. Ones plus twos are threes. The response field `model_version` is `"1"`.

The server does not publish a reflection service. Point the client at the protocol file in this repository.

```bash
grpcurl -plaintext \
  -proto api/openinference/v2/open_inference_grpc.proto \
  -d '{"modelName":"add","modelVersion":"1","inputs":[{"name":"A","datatype":"FP32","shape":[2,3],"contents":{"fp32Contents":[1,1,1,1,1,1]}},{"name":"B","datatype":"FP32","shape":[2,3],"contents":{"fp32Contents":[2,2,2,2,2,2]}}]}' \
  127.0.0.1:8001 inference.GRPCInferenceService/ModelInfer
```

`grpcurl` prints the protobuf JSON names. `modelVersion` is `model_version`, and `fp32Contents` is the float32 contents. The output tensor is `C`, shape `[2, 3]`, six threes.

Omit `modelVersion` and the server chooses the served version. For this flat model that version is still `"1"`.

`raw_input_contents` carries each input as little-endian bytes, in input order, instead of `contents`. `FP16` and `BF16` travel that way only. A JSON number for either type is rejected.

`sequence_id`, `sequence_start`, and `sequence_end` are `INVALID_ARGUMENT`, the same rejection as on REST. A timeout that elapses while the request is still waiting is `DEADLINE_EXCEEDED`. A model that is `LOADING`, or a full queue, is `UNAVAILABLE`. An unknown model or version is `NOT_FOUND`.

## Unload

`RepositoryModelUnload` returns `UNIMPLEMENTED`. `RepositoryModelLoad` returns the same code. The status text is `loading and unloading models is not supported`. The engine does not look the model name up.
