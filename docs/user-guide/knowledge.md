# Add a knowledge item

Knowledge is a directory the model waits for. The engine does not read the files inside it, does not assume a `data/` subdirectory, and does not treat the item as a model or a session. Use it when a model must not become `READY` until a label set, a vocabulary directory, or another artifact is mounted beside it.

Put a file named `knowledge` at the top of the model directory, beside `config.pbtxt`. One name per line. The file is UTF-8.

```text
# line labels shipped with the model
wafer-labels
```

Blank lines and lines whose first non-space character is `#` are ignored. A name is one path segment: no `/`, no `\`, not `.` or `..`, and no `*` or `>`. Duplicate names count once.

The model stays `UNAVAILABLE` until `{KNOWLEDGE_PATH}/wafer-labels/` exists. The index reason is `knowledge "wafer-labels" is missing`. The default knowledge root is `/knowledge`. On an Edgelet catalog the platform bind is read-only at that path. [Bind a model from Edgelet](edgelet-bind.md) shows the bind.

A missing file, or a file that names nothing, means the model names no knowledge. A directory named `knowledge`, a file that cannot be read, or a file that is not valid UTF-8 makes the model `UNAVAILABLE` and the reason says so.

Removing a named directory makes the model not ready. Infer then fails until the directory is back. Editing a file inside the directory does not reload the session and does not change readiness. Replacing the ONNX file does reload the session. [Read a reload](reload.md) is the difference.

Confirm the directory with the repository index. `state` stays off `READY` while `wafer-labels` is absent, and the reason names the missing item. After the directory appears, read `GET /v2/models/<name>/ready` and expect 200.

The next page is [Read a reload](reload.md).
