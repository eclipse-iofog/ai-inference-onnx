# Call infer on REST

REST listens on port 8000. This page uses the signature you read in [Run the container](docker-run.md). The `add` model takes float32 inputs `A` and `B`, each of shape `[2, 3]`, and returns float32 `C`. Ones plus twos are threes. The served version is `"1"`.

The call list is on the [REST](../reference/rest.md) page. Datatypes and binary bodies are on [Send a binary tensor](binary-tensor.md).

## Send the request

```bash
curl -sS -X POST "http://127.0.0.1:8000/v2/models/add/infer" \
  -H "Content-Type: application/json" \
  -d '{"id":"req-1","inputs":[{"name":"A","datatype":"FP32","shape":[2,3],"data":[1,1,1,1,1,1]},{"name":"B","datatype":"FP32","shape":[2,3],"data":[2,2,2,2,2,2]}]}'
```

The response:

```json
{"model_name":"add","model_version":"1","id":"req-1","outputs":[{"name":"C","datatype":"FP32","shape":[2,3],"data":[3,3,3,3,3,3]}]}
```

`id` is echoed when the request sets it. An omitted id comes back as `""`. The client sends the full tensor, including the leading dimension. `data` may be a flat array or nested arrays that match `shape`. The element count must equal the product of the shape. JSON numbers are the natural JSON type. `BOOL` is `true` or `false`.

Omit `/versions/1` and the server chooses. For this flat model the choice is still `"1"`. `POST /v2/models/add/versions/1/infer` selects that version explicitly. `latest` and `v1` are 404.

In `static` or `edgelet` mode, add `Authorization: Bearer` with the credential from [Choose authentication and TLS](auth-tls.md). This local run is `disabled`, so the header is absent.

## Parameters the server honors

| Parameter | Where | Meaning |
|---|---|---|
| `timeout` | Request parameters | Microseconds to wait in the queue. `0` does not expire the request. A timeout that elapses while the request is still waiting is 504 |
| `priority` | Request parameters | Accepted. The queue order does not change |
| `binary_data_output` | Request parameters | `true` returns every output as bytes after the JSON, unless one output sets its own flag |
| `binary_data_size` | One input | The input's bytes follow the JSON. [Send a binary tensor](binary-tensor.md) |
| `binary_data` | One requested output | `true` or `false` for that output. It wins over `binary_data_output` |

Any other parameter key is accepted and ignored, and it is not echoed. `sequence_id`, `sequence_start`, and `sequence_end` are 400 on the request, on an input, or on an output.

Omit `outputs` and the server returns every model output, in model order. Name an output to ask for that one. A name the model does not have is 400. A duplicate name is 400.

## Decide whether to retry

The error body is `{"error":"..."}`. It does not echo token claims.

| Status | Meaning | Retry the same body |
|---|---|---|
| 200 | The outputs are the result | The call finished |
| 400 | The tensor, the parameter, the payload, a canceled call, or an `UNAVAILABLE` model | No, until the body or the model changes. A payload larger than `MAX_PAYLOAD_BYTES` says `request body is larger than the payload limit`. An `UNAVAILABLE` model returns its load reason |
| 401 | The token is missing or invalid | No, until the credential is fixed |
| 403 | The token is valid and its rule does not allow this call | No, until the rule allows `models/<name>/infer` |
| 404 | The model or the version is unknown | No |
| 500 | The run did not finish. The text starts with `model run failed` | Read the text. The same tensor fails again when the runtime rejected it |
| 503 | The model is `LOADING` (`model is loading`), or the queue is full (`request queue for this model is full`) | Yes |
| 504 | The queue timeout elapsed (`request timeout elapsed while waiting in the queue`), or the call deadline elapsed | Yes, or send a larger `timeout` |

gRPC reports the same outcomes with status codes. The map is in [Call infer on gRPC](infer-grpc.md). NATS reports them in the `Inference-Status` header.

A 400 for an `UNAVAILABLE` model is a model problem, not a bad tensor. `POST /v2/repository/index` with `{}` shows `reason`. Fix the file, the `config.pbtxt`, the knowledge directory, or the execution provider. Calling infer again with the same body returns 400 until that reason clears.

The next page is [Send a binary tensor](binary-tensor.md).
