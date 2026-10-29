# What the engine is

One process loads ONNX models from a directory and serves them with the KServe Open Inference Protocol, version 2. A second process, the dashboard, reads metrics. The dashboard does not run a model. The engine does not register itself with the Controller. The platform places the files. The engine watches them.

## What you connect to

| Transport | Port | Default |
|---|---|---|
| REST | 8000 | Plain HTTP |
| gRPC | 8001 | h2c, the HTTP/2 listener with TLS off |
| Metrics | 8002 | Plain HTTP, including when REST and gRPC use TLS |
| Dashboard | 8080 | A separate container |

NATS is off until both a server URL and a credentials file are set. Callers on the same node use REST or gRPC. Callers on another node use NATS, because an Edgelet token is valid only on the node that signed it. That split is in [Choose authentication and TLS](auth-tls.md) and [Call infer on NATS](infer-nats.md).

## A model is a directory

The models root defaults to `/models`. Every child directory is a model. The directory name is the model name. A symlink to a directory is a model, and the link name is the name. A regular file in the models root is ignored.

The engine reads the signature from the `.onnx` file: each input and output has a name, a datatype, and a shape. A dimension of `-1` in metadata is a variable dimension. You send a concrete length. `vocab.json` and `config.json` beside the ONNX file stay on disk. The engine does not open them to run a session.

`POST /v2/repository/models/{name}/load` and the matching unload call answer HTTP 501. The gRPC calls return `UNIMPLEMENTED`. The status text is `loading and unloading models is not supported`. Replacing the file on disk is how a model changes. [Read a reload](reload.md) is that sequence.

## Three states

| State | Meaning | Infer |
|---|---|---|
| `READY` | A session is open and the model can run | The call runs |
| `LOADING` | A session is opening, including during a reload | 503. Retry |
| `UNAVAILABLE` | The directory is known and cannot run | 400. The repository index carries the `reason` |

There is no `UNLOADING` state.

Server live means the process can answer. `GET /v2/health/live` is that check. It stays open in every authentication mode. Server ready means at least one ONNX model is `READY`. `GET /v2/health/ready` is that check. A directory with no ONNX file is `UNAVAILABLE` and does not clear server ready while another model is `READY`. When the only model is `LOADING` or `UNAVAILABLE`, server ready is false.

## Knowledge

A model may name knowledge items in a text file called `knowledge`. The model stays not ready until each named directory exists under the knowledge root, `/knowledge` by default. The engine does not read the files inside the item. [Add a knowledge item](knowledge.md) is the file format.

## What a request is

An infer call names the model, and optionally the version, and sends one tensor per model input. The client sends the full shape, including the leading dimension. The server runs one batch at a time for each model version. Requests that can share a batch wait on one queue. [How a request is scheduled](schedule.md) is that queue.

`sequence_id`, `sequence_start`, and `sequence_end` are rejected. There is no sequence batcher. Running those requests as stateless calls would return the wrong result.

## Where this path goes

[Run the container](docker-run.md) starts the CPU image and reads the `add` model's signature before the first infer.
