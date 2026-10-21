# Call infer on NATS

The body is the REST body. The add model takes float32 inputs `A` and `B`, each of shape `[2, 3]`, and returns float32 `C`. Ones plus twos are threes. The reply carries `model_version` `"1"`.

```bash
nats req inference.v2.models.add.infer \
  '{"inputs":[{"name":"A","datatype":"FP32","shape":[2,3],"data":[1,1,1,1,1,1]},{"name":"B","datatype":"FP32","shape":[2,3],"data":[2,2,2,2,2,2]}]}'
```

`nats req` prints the first reply. That message has header `Inference-Status: 200` and the REST response. The subjects, inline transfer, and rendezvous are on the [NATS](../reference/nats.md) page.

This call is allowed by the NATS account and user rules. 401 and 403 are the token results on REST and gRPC, described in [authentication](../reference/auth.md). A body the model cannot run, including `sequence_id` or a model that is `UNAVAILABLE`, is `Inference-Status` 400. An unknown model or version is 404. A model that is `LOADING`, or a full queue, is 503.
