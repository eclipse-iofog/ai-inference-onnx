# Choose the ONNX file

The directory name is the model name. The [repository](../reference/repository.md) page shows the directory trees. These are the four outcomes.

A single `.onnx` file at the top of the directory is the model. The version is `"1"`. The file does not have to be named `model.onnx`. `wafer-defect.onnx` in a directory named `wafer-defect` is that model.

`model.onnx` at the top of the directory is the file that serves, including when another `.onnx` file sits beside it. The version is still `"1"`. When several top-level `.onnx` files are present and none is named `model.onnx`, the model stays `UNAVAILABLE` until `default_model_filename` names one. Files in nested directories do not count. That field is in [config.pbtxt](config-pbtxt.md).

Numeric version directories are digits only, with no leading zero. When those directories contain an ONNX file, they are the versions. With no config the policy is latest of 1, so the highest version is the one that serves. An `.onnx` file beside those directories is ignored.

A directory with no ONNX file is listed by `POST /v2/repository/index` as `UNAVAILABLE` with reason `no ONNX model in directory`. `GET /v2/models` does not list it. It does not clear server ready while another model is `READY`.
