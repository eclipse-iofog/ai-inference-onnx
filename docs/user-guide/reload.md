# Read a reload

The platform replaces files on disk. The engine does not accept a load or unload call. Replacing the chosen ONNX file, its `.onnx.data` sibling, the `config.pbtxt`, or the `knowledge` name file schedules a load of that model.

## What you observe

1. The model becomes `LOADING`. `GET /v2/models/<name>` and infer for that name return 503 with `model is loading`.
2. The batch already inside the execution slot finishes on the old session. That caller receives its result.
3. The old session closes.
4. The new file loads. `READY` returns when the session opens. A file that cannot load becomes `UNAVAILABLE`, and infer is 400 with the load reason.

A request that was waiting in the queue is not run on the new session. Those callers receive 503 and retry. Another model that is `READY` keeps server ready true. `GET /v2/health/ready` stays 200 while some other model can serve. When the reloading model is the only model, server ready is false until it is `READY` again.

`UNAVAILABLE` is not 503. Infer for a model in that state is 400. Retrying does not help until the index `reason` is clear.

## When the load starts

Two watches run together.

The filesystem watch collapses changes for one model into one load. After the last change for that model, the process waits 100 ms, then loads. Changes that arrive while that load runs are loaded once more, after another quiet period.

Every 30 seconds the process compares identity. Identity is the inode, the size, and the modification time of each selected ONNX file and of its `.onnx.data` sibling when that path is considered. The bytes are not hashed. A replaced file with a new inode or a new size or a new modification time is a new model. A rewrite that keeps all three is the same model, and the session stays open.

The `knowledge` name file is part of identity. The presence of each named directory is part of identity. Rewriting the name file with the same names, while those directories stay, is the same model. Removing a named directory makes the model not ready. Editing a file inside the knowledge directory changes neither identity nor the session.

A directory that appears or disappears under the models root is picked up by the same watches. A name that contains `*` or `>` is rejected and does not become a model.

## What to call while it happens

Poll `GET /v2/models/<name>/ready`. 503 means still loading. 200 means the new session can serve. 400 means the new file is `UNAVAILABLE`. Then `POST /v2/repository/index` with `{}` and read `reason` before you change the client.

The next page is [Call infer on gRPC](infer-grpc.md).
