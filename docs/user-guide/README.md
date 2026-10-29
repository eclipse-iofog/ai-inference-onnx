# User guide

Read these pages in order. The committed `add` model is the first model you call. Later pages use the same calls on a model you place yourself.

| Step | Page | What you can do when you finish |
|---|---|---|
| 1 | [What the engine is](concepts.md) | Name the process, the three transports, and the three model states |
| 2 | [Run the container](docker-run.md) | Start the image and read a model signature before you infer |
| 3 | [Choose the ONNX file](choose-onnx-file.md) | Place a directory the engine will serve |
| 4 | [Write config.pbtxt](config-pbtxt.md) | Select the file, the versions, and the batch cap |
| 5 | [How a request is scheduled](schedule.md) | Predict a batch, a 503, and a 504 |
| 6 | [Call infer on REST](infer-rest.md) | Send a JSON tensor and decide whether to retry |
| 7 | [Send a binary tensor](binary-tensor.md) | Choose a datatype and return large outputs as bytes |
| 8 | [Add a knowledge item](knowledge.md) | Hold a model until a named directory exists |
| 9 | [Read a reload](reload.md) | Tell a file change from a load that did not happen |
| 10 | [Call infer on gRPC](infer-grpc.md) | Make the same call on h2c |
| 11 | [Choose authentication and TLS](auth-tls.md) | Pick `disabled`, `static`, or `edgelet`, and present a certificate |
| 12 | [Choose an image and pass a device](devices.md) | Keep uid `10001`, add host group ids, and mount the cache |
| 13 | [Bind a model from Edgelet](edgelet-bind.md) | Deploy the engine and the dashboard as one application |
| 14 | [Call infer on NATS](infer-nats.md) | Publish an inline request, then a rendezvous upload |
| 15 | [Read the dashboard](dashboard.md) | Read rate, latency, and queue depth for the last 15 minutes |
| 16 | [Operate the engine](operate.md) | Trace a model that stays `UNAVAILABLE` from the log to the index |

The lookup pages are in the [reference](../reference/).
