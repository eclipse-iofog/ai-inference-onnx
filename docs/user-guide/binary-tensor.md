# Send a binary tensor

`binary_data_size` marks an input whose bytes follow the JSON, in input order. `Inference-Header-Content-Length` is the JSON length. `Content-Length` is the whole body. The bytes are little-endian, with no stride.

The add model takes float32 inputs `A` and `B`, each of shape `[2, 3]`. Each input is 24 bytes. Six ones, then six twos. The compact JSON below is 176 bytes, so `Inference-Header-Content-Length` is `176` for this exact text. The output is still `C` of threes.

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

A header length of 0 is a raw-binary body: tensor bytes only. A header length of 0 on the add model is 400 because that model has two inputs. That form is accepted only when the model has one input and at most one variable dimension.

The REST calls are on the [REST](../reference/rest.md) page. gRPC sends the same bytes in `raw_input_contents`, described on the [gRPC](../reference/grpc.md) page. A suffix that does not fit one NATS payload uses rendezvous, described on the [NATS](../reference/nats.md) page.
