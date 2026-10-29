# Model repository

The engine loads every child directory of the models root. The directory name is the model name. A symlink to a directory is a model, and the link name is the model name. A directory whose name starts with `..` is not a model. A regular file in the models root is ignored. The knowledge root is a separate directory, and the engine does not scan it for ONNX files.

Names may contain dots. A name that contains `*` or `>` is rejected, because those characters are wildcards on NATS.

A URL or gRPC version is a decimal integer with no leading zero. `latest` and `v1` are 404. An empty gRPC `model_version` means the same model as a REST URL with no `/versions/` segment. For a flat artifact, `/v2/models/wafer-defect` and `/v2/models/wafer-defect/versions/1` are the same model, and any other version is 404. The infer response includes `model_version`. Metadata lists the versions the policy has made available.

## Flat artifact

```
/models/wafer-defect/wafer-defect.onnx
```

`wafer-defect.onnx` is one model named `wafer-defect`. The version is `"1"`. A version directory is not required, and the file does not have to be named `model.onnx`.

## `model.onnx` wins

```
/models/wafer-defect/model.onnx
/models/wafer-defect/wafer-defect.onnx
```

`model.onnx` at the top of the directory is the file that serves, including when another `.onnx` file sits beside it. The version is still `"1"`.

When several top-level `.onnx` files are present and none is named `model.onnx`, the model stays `UNAVAILABLE` until `default_model_filename` names one. Files in nested directories do not count. The field is described in [the model file guide](../user-guide/config-pbtxt.md).

## Numeric version directories

```
/models/wafer-defect/1/model.onnx
/models/wafer-defect/2/model.onnx
```

A version directory name is digits only, with no leading zero. Those directories are the versions when they contain an ONNX file. With no config the policy is latest of 1, so version 2 is the one that serves when version 1 and version 2 are both present. A `.onnx` file beside those directories is ignored.

Inside a version directory, `model.onnx` wins the same way. A directory named `model.onnx` that contains `model.onnx` plus other files is accepted there.

Config may select every version, the latest of N, or a specific list. The policy field is described in [the model file guide](../user-guide/config-pbtxt.md).

## No ONNX file

```
/models/tiny-gpt2/config.json
/models/tiny-gpt2/vocab.json
```

This directory has no ONNX file. `POST /v2/repository/index` lists it `UNAVAILABLE` with reason `no ONNX model in directory`. `GET /v2/models` does not list it. It does not clear server ready while another model is `READY`.

Server ready means at least one ONNX model is `READY`. When the only model is `LOADING` or `UNAVAILABLE`, server ready is false. Server live means the process can answer.

## Which file is chosen

1. If the directory contains numeric version subdirectories and those versions contain an ONNX file, those directories are the versions. The version policy then chooses which of them serve.
2. Otherwise the directory is a single version `"1"`. `model.onnx` at the top wins when it exists. Otherwise exactly one top-level `.onnx` file is the model.

A sibling `.onnx.data` file is external data and is loaded with the model. `vocab.json` and `config.json` beside an ONNX file stay on disk. The engine does not open them to run a session.

## Knowledge names

A model names knowledge with an optional text file called `knowledge` at the top of the model directory, beside `config.pbtxt`, not inside a version directory. The file is UTF-8. One name per line. Blank lines and lines whose first non-space character is `#` are ignored. A name is one path segment: no `/`, no `\`, not `.` or `..`, and no `*` or `>`. Duplicate names count once.

```
# line labels shipped with the model
wafer-labels
```

The model stays not ready until `{KNOWLEDGE_PATH}/{name}/` exists. The engine does not read the files inside that directory, does not assume a `data/` subdirectory, and does not treat the item as a model or a session. A missing file, or a file that names nothing, means the model names no knowledge. A directory named `knowledge`, a file that cannot be read, or a file that is not valid UTF-8 makes the model `UNAVAILABLE` and the reason says so.

Removing a named directory makes the model not ready. Editing a file inside the directory does not reload the session.

## Reload

Replacing the chosen ONNX file, its `.onnx.data` sibling, `config.pbtxt`, or the `knowledge` name file loads that model again. The model is `LOADING` until the new session is open. The in-flight batch finishes on the old session. A request that was waiting is not run on the new session.

The filesystem watch waits 100 ms after the last change for that model, then loads. Every 30 seconds the process compares identity: inode, size, and modification time. The bytes are not hashed. The presence of each named knowledge directory is part of identity. Rewriting the name file with the same names, while those directories stay, is the same model. The sequence is in [Read a reload](../user-guide/reload.md).

