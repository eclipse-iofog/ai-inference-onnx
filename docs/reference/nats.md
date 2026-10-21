# NATS

NATS is off unless both `natsServerUrl` and `natsCredsPath` are set. The platform injects them. A missing value does not stop REST or gRPC. When the dial fails, the process stays up and retries.

NATS does not check the service-account JWT. [Authentication](auth.md) applies to REST and gRPC only. Account and user rules on the NATS server authenticate the connection.

The engine queue-subscribes with queue group `inference` on every request subject, including health. The client does not set that group. The client publishes a request and reads the reply on its own inbox. Without the queue group, one request would be delivered to every replica. Each process batches only the requests that land on it.

## Subjects

Subjects mirror the [REST](rest.md) path. `/` becomes `.`. The token `versions` keeps a dotted model name unambiguous.

| REST | Subject |
|---|---|
| `GET /v2/health/live` | `inference.v2.health.live` |
| `GET /v2/health/ready` | `inference.v2.health.ready` |
| `GET /v2` | `inference.v2.server.metadata` |
| `GET /v2/models` | `inference.v2.models` |
| `GET /v2/models/{name}` | `inference.v2.models.<name>.metadata` |
| `GET /v2/models/{name}/ready` | `inference.v2.models.<name>.ready` |
| `POST /v2/models/{name}/infer` | `inference.v2.models.<name>.infer` |
| `.../versions/{version}/...` | `inference.v2.models.<name>.versions.<version>.<operation>` |
| `POST /v2/repository/index` | `inference.v2.repository.index` |

`inference.v2.models.wafer-defect.infer` and `inference.v2.models.wafer-defect.versions.1.infer` are the same flat model. `inference.v2.models.my.detector.versions.2.infer` is model `my.detector`, version `2`. A name that contains `*` or `>` is rejected.

The caller's user needs publish on the infer subjects and on `inference.xfer.>`. The engine user needs subscribe on those, and publish on `inference.metrics.engines.>`.

## Inline infer

The body is the REST body. The add model takes float32 inputs `A` and `B`, each of shape `[2, 3]`. Ones plus twos are threes. The reply carries `model_version` `"1"`.

```bash
nats req inference.v2.models.add.infer \
  '{"inputs":[{"name":"A","datatype":"FP32","shape":[2,3],"data":[1,1,1,1,1,1]},{"name":"B","datatype":"FP32","shape":[2,3],"data":[2,2,2,2,2,2]}]}'
```

`nats req` prints the first reply. That message has header `Inference-Status: 200`, `Content-Type: application/json`, and the REST response:

```json
{"model_name":"add","model_version":"1","id":"","outputs":[{"name":"C","datatype":"FP32","shape":[2,3],"data":[3,3,3,3,3,3]}]}
```

Every reply is a short stream, including health and metadata. One empty message with no headers ends the stream, and it is the only empty message. The inbox is one caller to one replica and is not queue-grouped. `sequence_id`, `sequence_start`, and `sequence_end` are status 400, the same rejection as on REST.

## Rendezvous

Use inline when the whole body fits in the NATS server `max_payload`. When it does not, the JSON header and the raw tensor bytes travel as separate messages, and both messages stay on the replica that accepted the header. The queue group is only on the first message. The client does not set it.

The add model above fits inline. The same inputs are used here so the bytes are short enough to read. Ones are still input `A`, twos are still input `B`, and the output is still `C` of threes with `model_version` `"1"`.

### Header

Publish only the JSON to `inference.v2.models.add.infer`. Each input names `binary_data_size` and omits `data`. The sizes are in input order: 24 bytes for `A`, then 24 bytes for `B`, so the suffix is 48 bytes.

```bash
nats req inference.v2.models.add.infer \
  --header 'Inference-Transfer:rendezvous' \
  --header 'Inference-Suffix-Length:48' \
  '{"inputs":[{"name":"A","datatype":"FP32","shape":[2,3],"parameters":{"binary_data_size":24}},{"name":"B","datatype":"FP32","shape":[2,3],"parameters":{"binary_data_size":24}}]}'
```

`Inference-Transfer` must be the single value `rendezvous`. Any other value, or a repeated value, is 400. `Inference-Suffix-Length` must be present once and be decimal digits. That compact JSON is 176 bytes. Leave `Inference-Header-Content-Length` unset. If you set it, it must be `176` for this exact text. A different length is 400.

`nats req` prints the first reply and stops. The body is empty. The headers are `Inference-Status: 200` and `Inference-Accept: inference.xfer.<id>`. `<id>` is 32 hex characters. One empty message with no headers then ends that reply stream. The accept subject is outside `inference.v2.models.>`, so the queue group cannot take the byte stream.

A suffix length of `0` is not an upload. The header message is handled as inline JSON, and the reply has no `Inference-Accept`.

The ceiling is 32 MiB unless `maxPayloadBytes` sets another limit. A declared length over that ceiling, or a header whose own length plus the suffix would pass it, is 400 and nothing is allocated.

### Suffix

The 48 bytes are little-endian float32, with no stride. Six ones, then six twos:

```text
00 00 80 3f  00 00 80 3f  00 00 80 3f  00 00 80 3f  00 00 80 3f  00 00 80 3f
00 00 00 40  00 00 00 40  00 00 00 40  00 00 00 40  00 00 00 40  00 00 00 40
```

Each value begins with a zero byte, so a shell argument truncates the slice. Send it from a client that writes the raw bytes. This client sends the suffix as one slice. The caller's user needs publish on `inference.xfer.>`.

```python
import asyncio
import json
import struct

from nats.aio.client import Client as NATS

HEADER = json.dumps(
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
SUFFIX = struct.pack("<12f", *([1.0] * 6 + [2.0] * 6))


async def main():
    nc = NATS()
    await nc.connect("nats://127.0.0.1:4222")
    accepted = await nc.request(
        "inference.v2.models.add.infer",
        HEADER,
        headers={
            "Inference-Transfer": "rendezvous",
            "Inference-Suffix-Length": str(len(SUFFIX)),
        },
    )
    subject = accepted.headers["Inference-Accept"]
    upload_id = subject.removeprefix("inference.xfer.")
    result = await nc.request(
        subject,
        SUFFIX,
        headers={"Inference-Id": upload_id},
    )
    print(result.data.decode())
    await nc.close()


asyncio.run(main())
```

`Inference-Id` is the 32 hex characters, not the full subject, and it is set once. The reply to this only slice is the infer result, the same JSON as the inline call:

```json
{"model_name":"add","model_version":"1","id":"","outputs":[{"name":"C","datatype":"FP32","shape":[2,3],"data":[3,3,3,3,3,3]}]}
```

`Inference-Status` is `200` and `Content-Type` is `application/json`. One empty message with no headers ends that stream too.

A suffix larger than `max_payload` is several slices. Each slice is its own request, with the same `Inference-Id`, and each slice must stay under `max_payload`. Append order is the byte order of the suffix. For this model the first slice can be the 24 bytes of ones and the second the 24 bytes of twos. The reply to every slice except the last is an empty `200`. Wait for that reply before sending the next slice. The last slice's reply is the infer result.

The engine waits 5 seconds for the first slice, and 5 seconds again after each intermediate slice. A slice that does not arrive in that time aborts the upload. A dropped connection aborts it as well. These are also 400:

| What was sent | Why it fails |
|---|---|
| An empty slice | A slice must carry bytes |
| A slice that passes the declared length | The suffix was declared as 48 bytes |
| A missing, wrong, or repeated `Inference-Id` | The id has to name this upload, once |
| The next slice before the previous reply | One slice is in flight at a time |
| A slice after the suffix is complete | The upload is already finished |
