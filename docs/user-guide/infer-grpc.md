# Call infer on gRPC

gRPC listens on port 8001. With TLS off, the listener is h2c. The add model takes float32 inputs `A` and `B`, each of shape `[2, 3]`, and returns float32 `C`. Ones plus twos are threes. The response field `model_version` is `"1"`.

```bash
grpcurl -plaintext \
  -proto api/openinference/v2/open_inference_grpc.proto \
  -d '{"modelName":"add","modelVersion":"1","inputs":[{"name":"A","datatype":"FP32","shape":[2,3],"contents":{"fp32Contents":[1,1,1,1,1,1]}},{"name":"B","datatype":"FP32","shape":[2,3],"contents":{"fp32Contents":[2,2,2,2,2,2]}}]}' \
  127.0.0.1:8001 inference.GRPCInferenceService/ModelInfer
```

The RPCs are on the [gRPC](../reference/grpc.md) page. In `static` or `edgelet` mode, send `Authorization: Bearer` in metadata, the same credential [authentication](../reference/auth.md) describes for REST.

A missing or invalid token is `UNAUTHENTICATED`, the status REST reports as 401. A valid token whose rule does not allow the call is `PERMISSION_DENIED`, the status REST reports as 403. A request the model cannot run, including `sequence_id`, is `INVALID_ARGUMENT`, the status REST reports as 400. A model that is `UNAVAILABLE` is `FAILED_PRECONDITION`, which REST also reports as 400. An unknown model or version is `NOT_FOUND`, the status REST reports as 404. A model that is `LOADING`, or a full queue, is gRPC status `UNAVAILABLE`, the status REST reports as 503.
