# Call infer on REST

The add model takes float32 inputs `A` and `B`, each of shape `[2, 3]`, and returns float32 `C`. Ones plus twos are threes. The served version is `"1"`.

```bash
curl -sS -X POST "http://127.0.0.1:8000/v2/models/add/infer" \
  -H "Content-Type: application/json" \
  -d '{"inputs":[{"name":"A","datatype":"FP32","shape":[2,3],"data":[1,1,1,1,1,1]},{"name":"B","datatype":"FP32","shape":[2,3],"data":[2,2,2,2,2,2]}]}'
```

The calls and permissions are on the [REST](../reference/rest.md) page. In `static` or `edgelet` mode, add `Authorization: Bearer` with the credential from [authentication](../reference/auth.md).

A missing or invalid token is 401. A valid token whose rule does not allow the call is 403. A number that does not fit the declared type, a `sequence_id`, `sequence_start`, or `sequence_end` parameter, or a model that is `UNAVAILABLE`, is 400. An unknown model or version is 404. A model that is `LOADING`, or a full queue, is 503.

The error body is `{"error":"..."}`.
