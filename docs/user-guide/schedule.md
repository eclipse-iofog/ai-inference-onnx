# How a request is scheduled

Each selected model version has one queue and one execution slot. The slot runs one batch at a time. The queue holds the requests that are waiting for that slot. The batch inside the slot is not counted in the queue.

The defaults are queue capacity 8, batch delay 0, intra-op threads 1, and inter-op threads 1. The payload ceiling is 32 MiB. The keys are in [Configuration](../reference/configuration.md). The same rules as a lookup are in [Scheduling](../reference/scheduling.md).

## When two requests share a batch

A model is batchable when every input and every output has a dynamic leading dimension. Metadata shows that dimension as `-1`. The client still sends a concrete leading length. That length is the row count of the request.

The batch cap is `max_batch_size` from [Write config.pbtxt](config-pbtxt.md). When the model file omits it, the cap is 1. `0`, and any model that is not batchable, runs each request as sent. The `add` model has fixed shapes, so each call runs alone.

Two waiting requests share a batch when all of the following hold:

- The model is batchable and the cap is greater than 0.
- The input names and datatypes match.
- Every dimension after the leading dimension is equal.
- The sum of the leading lengths fits in the cap.

The server stacks them on axis 0, in arrival order, runs the stack once, and splits the outputs back by row. A request that does not fit the request at the head of the queue waits for the next batch.

## When the batch runs

A delay of 0 runs the formed batch immediately. A full batch runs immediately. A batch that cannot grow, because the next request does not fit, runs immediately.

A delay above 0 waits, measured from the oldest request in the batch, until the batch is full or the delay elapses. The server delay is `BATCH_DELAY_US`. A model file that sets `dynamic_batching.max_queue_delay_microseconds` uses that value instead.

`priority` is accepted and does not change this order.

## What the caller sees

| Situation | HTTP | What you do |
|---|---|---|
| The queue already holds `QUEUE_CAPACITY` requests | 503 | Retry |
| `timeout` microseconds elapse while the request is still waiting | 504 | Retry, or raise the timeout |
| The model is `LOADING` | 503 | Retry after the reload finishes |
| The model is `UNAVAILABLE`, or the tensor does not match the signature | 400 | Change the request or the model file. Retrying the same body fails again |
| The model name or version is unknown | 404 | Fix the name or the version |

`timeout` is a request parameter, an integer in microseconds. `0`, and an omitted timeout, mean the queue does not expire the request. A request that has entered the slot runs to completion. The timeout applies to the wait, not to the ONNX Runtime run.

A reload does not move a waiting request onto the new session. Those callers receive 503 and retry. The batch that was already inside the slot finishes on the old session.

## Threads and the payload

`INTRA_OP_THREADS` is the thread count inside one operator. `INTER_OP_THREADS` is the thread count across operators in one run. Both default to 1, and both are passed to every session. Raise them when a CPU image should use more cores. They do not add a second slot. One version still runs one batch at a time.

`MAX_PAYLOAD_BYTES` is the largest infer body, default 32 MiB. A larger body is 400 and is not queued. The same ceiling bounds a NATS suffix. A tensor that fits the signature and exceeds the ceiling has to shrink, or the operator raises the limit and restarts.

## What the metrics count

One success is one request that returned a result. A batch counts as one execution. `inference_count` adds the batch size, which is the sum of the leading lengths. `inference_exec_count` adds one for each batch that ran. Average batch size is `inference_count` divided by `inference_exec_count`. `inference_pending_request_count` is the number of requests waiting on the slot. The names are in [Metrics](../reference/metrics.md). The graphs are in [Read the dashboard](dashboard.md).

The next page is [Call infer on REST](infer-rest.md).
