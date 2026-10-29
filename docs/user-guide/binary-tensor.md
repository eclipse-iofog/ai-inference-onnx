# Send a binary tensor

JSON is the right body for a short `FP32` tensor. Bytes are the right body for an image, a long integer, `FP16`, `BF16`, or any output you do not want expanded as JSON numbers. The lookup table is [Tensors](../reference/tensors.md).

## Which datatype can travel as JSON

| Datatype | JSON input | JSON output |
|---|---|---|
| `BOOL` | `true` or `false` | `true` or `false` |
| `UINT8`, `UINT16`, `UINT32` | Integers in range | Integers |
| `INT8`, `INT16`, `INT32` | Integers in range | Integers |
| `INT64`, `UINT64` | Integers that JSON can carry exactly, within ±2^53 | Binary only |
| `FP32`, `FP64` | JSON numbers | JSON numbers |
| `FP16`, `BF16` | Binary only | Binary only |
| `BYTES` | JSON strings | JSON strings, or binary when you ask |

A number that does not fit the declared type is 400. An `INT64` or `UINT64` outside the exact JSON range is 400 with the text `integer is not exact in JSON; send it as binary data`. Asking for an `INT64`, `UINT64`, `FP16`, or `BF16` output as JSON is 400 with the text that the type is returned only as binary data.

Binary layout is little-endian, row-major, with no stride. `BOOL` is one byte, `0` or `1`. One element is 1, 2, 4, or 8 bytes for the fixed-size types. `FP32` is 4 bytes, so shape `[2, 3]` is 24 bytes.

`BYTES` in JSON is an array of strings. `BYTES` in binary is a 4-byte little-endian length, then that many bytes, repeated once per element.

## Put the input bytes after the JSON

`binary_data_size` marks an input whose bytes follow the JSON, in input order. `Inference-Header-Content-Length` is the JSON length. `Content-Length` is the whole body. An input has either `data` or `binary_data_size`. Both, or neither, is 400. The declared size must equal the size the shape requires.

The `add` model takes float32 inputs `A` and `B`, each of shape `[2, 3]`. Each input is 24 bytes. Six ones, then six twos. The compact JSON below is 176 bytes, so `Inference-Header-Content-Length` is `176` for this exact text. The output is still `C` of threes.

```python
import json
import struct
import urllib.request

header = json.dumps(
    {
        "inputs": [
            {
                "name": "A",
                "datatype": "FP32",
                "shape": [2, 3],
                "parameters": {"binary_data_size": 24},
            },
            {
                "name": "B",
                "datatype": "FP32",
                "shape": [2, 3],
                "parameters": {"binary_data_size": 24},
            },
        ]
    },
    separators=(",", ":"),
).encode()
body = header + struct.pack("<12f", *([1.0] * 6 + [2.0] * 6))
request = urllib.request.Request(
    "http://127.0.0.1:8000/v2/models/add/infer",
    data=body,
    method="POST",
    headers={"Inference-Header-Content-Length": str(len(header))},
)
print(urllib.request.urlopen(request).read().decode())
```

A header length of 0 is a raw-binary body: tensor bytes only, no JSON. That form is accepted only when the model has one input and at most one variable dimension. The `add` model has two inputs, so a header length of 0 is 400. Use the header form above for every model with more than one input.

## Ask for binary outputs

Set `binary_data_output` to `true` to return every output as bytes. Set `binary_data` on one entry in `outputs` to override that output. The output flag wins.

```json
{
  "parameters": {"binary_data_output": true},
  "inputs": [
    {"name": "A", "datatype": "FP32", "shape": [2, 3], "data": [1, 1, 1, 1, 1, 1]},
    {"name": "B", "datatype": "FP32", "shape": [2, 3], "data": [2, 2, 2, 2, 2, 2]}
  ]
}
```

The response status is 200. `Content-Type` is `application/octet-stream`. `Inference-Header-Content-Length` is the JSON length. The JSON output omits `data` and sets `parameters.binary_data_size` to the byte count. The bytes follow the JSON, in output order. Read that many bytes, then the next output.

`INT64`, `UINT64`, `FP16`, and `BF16` are returned this way even when the request does not set the flag. The response header is present whenever any output is binary.

gRPC sends the same input bytes in `raw_input_contents`, and the same output bytes in `raw_output_contents`. That call is in [Call infer on gRPC](infer-grpc.md). A suffix that does not fit one NATS payload uses rendezvous, in [Call infer on NATS](infer-nats.md).

The next page is [Add a knowledge item](knowledge.md).
