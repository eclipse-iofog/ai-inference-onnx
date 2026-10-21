# Read a reload

When the chosen ONNX file changes, that model becomes `LOADING`. The in-flight batch finishes on the old session, and that caller receives its result. The old session is then closed, and only then is the new file loaded.

While that name is `LOADING`, infer for that name returns 503. A request that was waiting is not run on the new session. Callers retry.

Another model that is `READY` keeps server ready true. `GET /v2/health/ready` is that check. The status list is on the [REST](../reference/rest.md) page.

`UNAVAILABLE` is not 503. Infer for a model in that state is 400.
