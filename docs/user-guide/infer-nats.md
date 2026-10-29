# Call infer on NATS

NATS is how a caller on another node runs a model. An Edgelet JWT verifies only on the node that signed it, so REST and gRPC from another node fail authentication. NATS account and user rules authenticate the connection. The engine does not check the service-account JWT on this path.

NATS is off unless both `NATS_SERVER_URL` and `NATS_CREDS_PATH` are set. The catalog sets `natsAccess: true` and the platform injects both. A missing value does not stop REST or gRPC. A failed dial leaves the process up, and the client retries.

The engine queue-subscribes with queue group `inference` on every request subject, including health. The client publishes a request and reads the reply on its own inbox. The client does not set the queue group. Without the queue group, one request would be delivered to every replica. Each process batches only the requests that land on it.

The subjects, the failure table, and the byte layout are on the [NATS](../reference/nats.md) page. Read that table before you ship a client. This page is the sequence.

## Subjects

Subjects mirror the REST path. `/` becomes `.`. The token `versions` keeps a dotted model name unambiguous.

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

`inference.v2.models.wafer-defect.infer` and `inference.v2.models.wafer-defect.versions.1.infer` are the same flat model. `inference.v2.models.my.detector.versions.2.infer` is model `my.detector`, version `2`.

The caller's user needs publish on the infer subjects and on `inference.xfer.>`. The engine user from [Bind a model from Edgelet](edgelet-bind.md) needs subscribe on those, and publish on `inference.metrics.engines.>`.

## Inline, when the body fits

Use inline when the whole body fits in the NATS server `max_payload`. The body is the REST body. The `add` model takes float32 inputs `A` and `B`, each of shape `[2, 3]`. Ones plus twos are threes.

```bash
nats req inference.v2.models.add.infer \
  '{"inputs":[{"name":"A","datatype":"FP32","shape":[2,3],"data":[1,1,1,1,1,1]},{"name":"B","datatype":"FP32","shape":[2,3],"data":[2,2,2,2,2,2]}]}'
```

`nats req` prints the first reply. That message has header `Inference-Status: 200`, `Content-Type: application/json`, and the REST response, including `model_version` `"1"`. Every reply is a short stream, including health and metadata. One empty message with no headers ends the stream, and it is the only empty message.

`Inference-Status` uses the HTTP numbers from [Call infer on REST](infer-rest.md). 401 and 403 are the token results on REST and gRPC. They do not apply here. A body the model cannot run, including `sequence_id` or a model that is `UNAVAILABLE`, is 400. An unknown model or version is 404. A model that is `LOADING`, or a full queue, is 503.

## Rendezvous, when the body does not fit

Publish the JSON header to the infer subject. Omit `data`. Name each input with `binary_data_size`. Set `Inference-Transfer: rendezvous` and `Inference-Suffix-Length` to the byte count of the suffix. The queue group applies only to this first message, so the byte stream stays on the replica that accepted the header.

The engine replies with an empty body, `Inference-Status: 200`, and `Inference-Accept: inference.xfer.<id>`. `<id>` is 32 hex characters. Publish the raw little-endian bytes to that subject with header `Inference-Id` set to those 32 characters, not the full subject. The reply to the last slice is the infer result.

The `add` inputs are 24 bytes of ones and 24 bytes of twos, so the suffix is 48 bytes. They fit inline. The same bytes are used here so the suffix is short enough to read. The ceiling is 32 MiB unless `MAX_PAYLOAD_BYTES` sets another limit. A declared length over that ceiling is 400 and nothing is allocated.

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

A suffix larger than `max_payload` is several slices. Each slice is its own request, with the same `Inference-Id`, and each slice stays under `max_payload`. Append order is the byte order of the suffix. The reply to every slice except the last is an empty `200`. Wait for that reply before sending the next slice. The last slice's reply is the infer result.

The engine waits 5 seconds for the first slice, and 5 seconds again after each intermediate slice. A slice that does not arrive in that time aborts the upload. These are also 400: an empty slice, a slice past the declared length, a missing or repeated `Inference-Id`, a second slice before the previous reply, and a slice after the suffix is complete. The full table is on the [NATS](../reference/nats.md) page.

A suffix length of `0` is not an upload. The header message is handled as inline JSON, and the reply has no `Inference-Accept`.

The next page is [Read the dashboard](dashboard.md).
