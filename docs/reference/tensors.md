# Tensors

The wire datatypes and the binary layout. The worked requests are in [Send a binary tensor](../user-guide/binary-tensor.md).

Binary layout is little-endian, row-major, with no stride. `BOOL` is one byte, `0` or `1`. `BYTES` is a 4-byte little-endian length, then that many bytes, repeated once per element. A `BYTES` element longer than 2^32−1 is rejected.

| Datatype | Element size | JSON input | JSON output |
|---|---|---|---|
| `BOOL` | 1 | `true` or `false` | `true` or `false` |
| `UINT8` | 1 | Integer in range | Integer |
| `UINT16` | 2 | Integer in range | Integer |
| `UINT32` | 4 | Integer in range | Integer |
| `UINT64` | 8 | Integer JSON can carry exactly, at most 2^53 | Binary only |
| `INT8` | 1 | Integer in range | Integer |
| `INT16` | 2 | Integer in range | Integer |
| `INT32` | 4 | Integer in range | Integer |
| `INT64` | 8 | Integer JSON can carry exactly, within ±2^53 | Binary only |
| `FP16` | 2 | Binary only | Binary only |
| `FP32` | 4 | JSON number | JSON number |
| `FP64` | 8 | JSON number | JSON number |
| `BF16` | 2 | Binary only | Binary only |
| `BYTES` | Length-prefixed | JSON strings | JSON strings, or binary when requested |

A number that does not fit the declared type is 400. An `INT64` or `UINT64` outside the exact JSON range is 400, `integer is not exact in JSON; send it as binary data`. gRPC `int64Contents` and `uint64Contents` carry the full integer.

`data` may be a flat array or nested arrays that match `shape`. The element count equals the product of the shape. An input has `data` or `parameters.binary_data_size`, not both and not neither.

`binary_data_size` is the byte count of that input in the suffix, in input order. `Inference-Header-Content-Length` is the JSON length. A header length of 0 is a raw body: tensor bytes only. That form is accepted only when the model has one input and at most one variable dimension. A `BYTES` raw body needs shape `[1]` after the batch dimension.

`binary_data_output: true` on the request returns every output as bytes, unless an output sets `binary_data`. The output flag wins. A binary response uses `Content-Type: application/octet-stream` and sets `Inference-Header-Content-Length` to the JSON length. Each binary output omits `data` and sets `parameters.binary_data_size`. The bytes follow the JSON, in output order.

`INT64`, `UINT64`, `FP16`, and `BF16` outputs are binary even when the request does not set the flag. Asking for those types as JSON is 400. The text says the type is returned only as binary data.

Any parameter key other than `timeout`, `priority`, `binary_data_output`, `binary_data_size`, and `binary_data` is ignored and is not echoed. `sequence_id`, `sequence_start`, and `sequence_end` are 400. `priority` does not reorder the queue. `timeout` is in [Scheduling](scheduling.md).
