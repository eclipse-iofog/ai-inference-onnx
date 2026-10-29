# Choose the ONNX file

The directory name is the model name. These are the four layouts. The same rules, with the directory trees, are in [Repository](../reference/repository.md).

## One file at the top

A single `.onnx` file at the top of the directory is the model. The version is `"1"`. The file does not have to be named `model.onnx`.

```text
/models/wafer-defect/wafer-defect.onnx
```

`wafer-defect.onnx` in a directory named `wafer-defect` is the model `wafer-defect`, version `"1"`.

## `model.onnx` wins

```text
/models/wafer-defect/model.onnx
/models/wafer-defect/wafer-defect.onnx
```

`model.onnx` at the top of the directory is the file that serves, including when another `.onnx` file sits beside it. The version is still `"1"`.

When several top-level `.onnx` files are present and none is named `model.onnx`, the model stays `UNAVAILABLE`. The index reason is `several ONNX files and no model.onnx`. Set `default_model_filename` to one of those names. Files in nested directories do not count. That field is in [Write config.pbtxt](config-pbtxt.md).

## Numeric version directories

```text
/models/wafer-defect/1/model.onnx
/models/wafer-defect/2/model.onnx
```

A version directory name is digits only, with no leading zero. When those directories contain an ONNX file, they are the versions. With no config the policy is latest of 1, so version 2 serves and version 1 stays on disk. An `.onnx` file beside those directories is ignored.

Inside a version directory, `model.onnx` wins the same way. Call `/v2/models/wafer-defect/versions/2` for that version. Omit the version and the server chooses the highest selected version.

## No ONNX file

```text
/models/tiny-gpt2/config.json
/models/tiny-gpt2/vocab.json
```

`POST /v2/repository/index` lists this directory `UNAVAILABLE` with reason `no ONNX model in directory`. `GET /v2/models` does not list it. It does not clear server ready while another model is `READY`.

## What else is loaded with the file

A sibling `.onnx.data` file is external data and is loaded with the model. Replacing that sibling is a reload, the same as replacing the `.onnx` file. `vocab.json` and `config.json` stay on disk. The engine does not open them to tokenize or to build a request. The caller builds the tensors the signature names.

A name that contains `*` or `>` is rejected, because those characters are wildcards on NATS. Names may contain dots. `my.detector` is one model name. The NATS subject uses the token `versions` so that name stays unambiguous. That subject is in [Call infer on NATS](infer-nats.md).

Read `GET /v2/models/<name>` after the file is in place. The inputs and outputs in that document are the only names a request may use.

The next page is [Write config.pbtxt](config-pbtxt.md).
