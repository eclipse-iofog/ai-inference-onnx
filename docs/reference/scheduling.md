# Scheduling

Each selected model version has one queue and one execution slot. The slot runs one batch at a time. The queue holds the requests waiting for that slot. The batch inside the slot is not counted in the queue. Replicas do not share a queue. The worked explanation is in [How a request is scheduled](../user-guide/schedule.md).

| Setting | Default | Effect |
|---|---|---|
| `max_batch_size` in the model file | 1 when the model is batchable, otherwise the tensor is run as sent | Cap on the sum of leading lengths in one batch. `0` runs each request as sent |
| `BATCH_DELAY_US` | `0` | Server delay, in microseconds, when the model file omits its own |
| `dynamic_batching.max_queue_delay_microseconds` | The server delay | That model's delay. `0` runs a lone request immediately |
| `QUEUE_CAPACITY` | `8` | Waiting requests per version. The next request is 503, `request queue for this model is full` |
| `INTRA_OP_THREADS` | `1` | Threads inside one operator, passed to every session |
| `INTER_OP_THREADS` | `1` | Threads across operators in one run. This does not add a second slot |
| `MAX_PAYLOAD_BYTES` | 32 MiB | Largest infer body. A larger body is 400, `request body is larger than the payload limit`, and is not queued |
| Request `timeout` | `0` | Microseconds to wait in the queue. `0` does not expire the request. Elapsed is 504, `request timeout elapsed while waiting in the queue` |

A model is batchable when every input and every output has a dynamic leading dimension. Metadata shows that dimension as `-1`. The client sends a concrete leading length.

Two waiting requests share a batch when the model is batchable, the cap is greater than 0, the input names and datatypes match, every dimension after the leading dimension is equal, and the sum of the leading lengths fits in the cap. The server stacks them on axis 0 in arrival order.

A delay of 0 runs the formed batch immediately. A full batch runs immediately. A batch that cannot grow, because the next request does not fit, runs immediately. A delay above 0 waits from the oldest request in the batch until the batch is full or the delay elapses.

`priority` is accepted and does not change the order. Preferred batch sizes, priority levels, and preserve-ordering in `dynamic_batching` are unused.

A request inside the slot runs to completion. The timeout applies to the wait. A reload does not move a waiting request onto the new session. Those callers receive 503.

`inference_count` adds the batch size. `inference_exec_count` adds one per batch that ran. `inference_pending_request_count` is the number of requests waiting on the slot. The names are in [Metrics](metrics.md).
