# Configuration

The engine reads `/etc/inference/config.yaml`. `CONFIG_PATH` selects another path. A missing file is normal. Environment variables are a complete configuration. The environment overrides the file, and the file overrides the built-in default.

A missing `AUTH_MODE` exits the process. A log level other than `debug`, `info`, `warn`, or `error` exits the process. The comparison ignores case.

Listeners bind all interfaces. REST listens on 8000, gRPC on 8001, and metrics on 8002. The dashboard listens on 8080.

| YAML key | Environment variable | Default |
|---|---|---|
| `modelsPath` | `MODELS_PATH` | `/models` |
| `knowledgePath` | `KNOWLEDGE_PATH` | `/knowledge` |
| `httpPort` | `HTTP_PORT` | `8000` |
| `grpcPort` | `GRPC_PORT` | `8001` |
| `metricsPort` | `METRICS_PORT` | `8002` |
| `authMode` | `AUTH_MODE` | required |
| `authStaticToken` | `AUTH_STATIC_TOKEN` | required when `authMode` is `static` |
| `natsServerUrl` | `NATS_SERVER_URL` | unset, and NATS stays off |
| `natsCredsPath` | `NATS_CREDS_PATH` | unset, and NATS stays off |
| `natsCAFile` | `NATS_CA_FILE` | unset. An empty value counts as unset |
| `tlsEnabled` | `TLS_ENABLED` | `false` |
| `tlsCert` | `TLS_CERT` | unset. Required when TLS is enabled |
| `tlsKey` | `TLS_KEY` | unset. Required when TLS is enabled |
| `tlsCA` | `TLS_CA` | unset. Set only when clients must present a certificate |
| `logLevel` | `LOG_LEVEL` | `info`. An empty value counts as unset |
| `intraOpThreads` | `INTRA_OP_THREADS` | `1` |
| `interOpThreads` | `INTER_OP_THREADS` | `1` |
| `maxPayloadBytes` | `MAX_PAYLOAD_BYTES` | 32 MiB |
| `queueCapacity` | `QUEUE_CAPACITY` | `8` |
| `batchDelayMicroseconds` | `BATCH_DELAY_US` | `0` |
| `metricsInterval` | `METRICS_INTERVAL` | `10s` |
| `serverId` | `ENGINE_SERVER_ID` | see below |

NATS stays off unless both `natsServerUrl` and `natsCredsPath` are set. When a model file omits its own batch delay, `batchDelayMicroseconds` is the delay. `metricsInterval` is a duration, and the default publishes a snapshot every 10 seconds.

## TLS

`tlsEnabled` defaults to false, so REST is plain HTTP and gRPC is h2c. When `tlsEnabled` is true, REST and gRPC present `tlsCert` and `tlsKey`. Both paths are required, and both files must be readable at startup. A missing file, or only one of the pair, exits the process. `tlsCA`, when set, must be readable, and both listeners then require a client certificate that chains to that CA. When `tlsCA` is unset, clients do not present a certificate. When `tlsEnabled` is false, the three paths are not read.

`TLS_ENABLED` accepts `true` and `false`, ignoring case. Any other value exits the process. The metrics port stays plain HTTP when TLS is enabled. A TLS terminator in front of the container remains valid while `tlsEnabled` is false.

## NATS CA

`natsCAFile` is unset by default. An empty value counts as unset. `nats://` does not read it. `tls://` uses it as the CA the engine trusts. When the path is unset, `tls://` uses the system trust store. A missing CA file on `tls://` retries and does not exit. This file is the CA for the NATS client. It is separate from the certificate REST and gRPC present.

## Logging

One JSON logger writes to stdout. `logLevel` is `debug`, `info`, `warn`, or `error`.

| Level | What it records |
|---|---|
| `info` | A listener is up, a model became `READY`, a reload started or finished, and NATS connected or disconnected. |
| `warn` | A retry, a rejected caller, or a model that became `UNAVAILABLE`. |
| `error` | A step the process attempted and could not finish. |
| `debug` | Per-request timing, without the tensor body. |

The log never contains the static token, a bearer token, a JWT, the creds file, a private key, or tensor values. The NATS CA file and the TLS files are paths in the configuration. Their contents are not logged.

## Server id

`serverId` is one token. It must not contain `.`, `*`, or `>`. An empty value counts as unset. A value that contains one of those characters exits the process.

Resolution order:

1. `EDGELET_MICROSERVICE_UID`, when the platform set it.
2. `ENGINE_SERVER_ID`, or the YAML key `serverId`, when the platform variable is absent. The environment overrides the file.
3. A new id for that process, when neither is set.

## Dashboard

The dashboard process has its own `LOG_LEVEL`, `TLS_ENABLED`, `TLS_CERT`, `TLS_KEY`, and `TLS_CA`, with the same defaults: `info`, TLS off, and the three paths unset. It listens on 8080, and `PORT` selects another port. It does not read the engine config file. TLS off on the engine does not change the dashboard.

## Model file

An optional `config.pbtxt` in the model directory may set `default_model_filename`, `max_batch_size`, `version_policy`, and `dynamic_batching.max_queue_delay_microseconds`, as described in [the model file guide](../user-guide/config-pbtxt.md).
