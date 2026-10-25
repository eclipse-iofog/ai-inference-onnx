# ai-inference-onnx

[![CI](https://github.com/eclipse-iofog/ai-inference-onnx/actions/workflows/ci.yml/badge.svg)](https://github.com/eclipse-iofog/ai-inference-onnx/actions/workflows/ci.yml)
[![Release](https://github.com/eclipse-iofog/ai-inference-onnx/actions/workflows/release.yml/badge.svg)](https://github.com/eclipse-iofog/ai-inference-onnx/actions/workflows/release.yml)
[![govulncheck](https://github.com/eclipse-iofog/ai-inference-onnx/actions/workflows/govulncheck.yml/badge.svg)](https://github.com/eclipse-iofog/ai-inference-onnx/actions/workflows/govulncheck.yml)
[![OpenSSF Scorecard](https://api.securityscorecards.dev/projects/github.com/eclipse-iofog/ai-inference-onnx/badge)](https://securityscorecards.dev/viewer/?uri=github.com/eclipse-iofog/ai-inference-onnx)
[![Go](https://img.shields.io/badge/Go-1.26.10-blue.svg)](https://go.dev/)
[![License](https://img.shields.io/badge/License-EPL--2.0-blue.svg)](LICENSE)

[![Engine (Eclipse)](https://img.shields.io/badge/ghcr.io-eclipse--iofog%2Fai--inference--onnx-2496ED?style=flat&logo=docker&logoColor=white)](https://ghcr.io/eclipse-iofog/ai-inference-onnx)
[![Engine (Datasance)](https://img.shields.io/badge/ghcr.io-datasance%2Fai--inference--onnx-2496ED?style=flat&logo=docker&logoColor=white)](https://ghcr.io/datasance/ai-inference-onnx)
[![Dashboard (Eclipse)](https://img.shields.io/badge/ghcr.io-eclipse--iofog%2Fai--inference--dashboard-2496ED?style=flat&logo=docker&logoColor=white)](https://ghcr.io/eclipse-iofog/ai-inference-dashboard)
[![Dashboard (Datasance)](https://img.shields.io/badge/ghcr.io-datasance%2Fai--inference--dashboard-2496ED?style=flat&logo=docker&logoColor=white)](https://ghcr.io/datasance/ai-inference-dashboard)

[![cpu](https://img.shields.io/badge/cpu-amd64%20%7C%20arm64%20%7C%20riscv64-2ea44f?style=flat)](docs/reference/images.md)
[![cuda12](https://img.shields.io/badge/cuda12-amd64%20%7C%20arm64-2ea44f?style=flat)](docs/reference/images.md)
[![cuda13](https://img.shields.io/badge/cuda13-amd64%20%7C%20arm64-2ea44f?style=flat)](docs/reference/images.md)
[![tensorrt12](https://img.shields.io/badge/tensorrt12-amd64-2ea44f?style=flat)](docs/reference/images.md)
[![qnn](https://img.shields.io/badge/qnn-arm64-2ea44f?style=flat)](docs/reference/images.md)
[![tensorrt13](https://img.shields.io/badge/tensorrt13-amd64%20%7C%20arm64-2ea44f?style=flat)](docs/reference/images.md)
[![openvino](https://img.shields.io/badge/openvino-amd64-2ea44f?style=flat)](docs/reference/images.md)
[![migraphx](https://img.shields.io/badge/migraphx-amd64-2ea44f?style=flat)](docs/reference/images.md)

[![Runtime cpu](https://img.shields.io/badge/runtime%20cpu-1.31.0--cpu-2496ED?style=flat&logo=docker&logoColor=white)](https://ghcr.io/eclipse-iofog/ai-inference-onnx-runtime)
[![Runtime cuda12](https://img.shields.io/badge/runtime%20cuda12-1.31.0--cuda12-2496ED?style=flat&logo=docker&logoColor=white)](https://ghcr.io/eclipse-iofog/ai-inference-onnx-runtime)
[![Runtime cuda13](https://img.shields.io/badge/runtime%20cuda13-1.31.0--cuda13-2496ED?style=flat&logo=docker&logoColor=white)](https://ghcr.io/eclipse-iofog/ai-inference-onnx-runtime)
[![Runtime tensorrt12](https://img.shields.io/badge/runtime%20tensorrt12-1.31.0--tensorrt12-2496ED?style=flat&logo=docker&logoColor=white)](https://ghcr.io/eclipse-iofog/ai-inference-onnx-runtime)
[![Runtime qnn](https://img.shields.io/badge/runtime%20qnn-1.31.0--qnn-2496ED?style=flat&logo=docker&logoColor=white)](https://ghcr.io/eclipse-iofog/ai-inference-onnx-runtime)
[![Runtime tensorrt13](https://img.shields.io/badge/runtime%20tensorrt13-1.31.0--tensorrt13-2496ED?style=flat&logo=docker&logoColor=white)](https://ghcr.io/eclipse-iofog/ai-inference-onnx-runtime)
[![Runtime openvino](https://img.shields.io/badge/runtime%20openvino-1.31.0--openvino-2496ED?style=flat&logo=docker&logoColor=white)](https://ghcr.io/eclipse-iofog/ai-inference-onnx-runtime)
[![Runtime migraphx](https://img.shields.io/badge/runtime%20migraphx-1.31.0--migraphx-2496ED?style=flat&logo=docker&logoColor=white)](https://ghcr.io/eclipse-iofog/ai-inference-onnx-runtime)

**Upstream:** [eclipse-iofog/ai-inference-onnx](https://github.com/eclipse-iofog/ai-inference-onnx) · **Datasance distribution:** [datasance/ai-inference-onnx](https://github.com/datasance/ai-inference-onnx)

**ONNX inference server for an edge node.** One process loads a model repository and serves it with the [KServe Open Inference Protocol](https://github.com/kserve/open-inference-protocol) on REST, gRPC, and NATS. A second process, the dashboard, reads metrics and does not run inference. The engine does not register itself with the Controller. The same engine image runs under `docker run`, `podman run`, and an Edgelet catalog.

Language is Go 1.26.10. License is [EPL-2.0](LICENSE). Operator reference and task book: [docs/](docs/).

## What you deploy

| Image | Eclipse | Datasance | Who pulls it |
|---|---|---|---|
| Engine | `ghcr.io/eclipse-iofog/ai-inference-onnx` | `ghcr.io/datasance/ai-inference-onnx` | The node |
| Dashboard | `ghcr.io/eclipse-iofog/ai-inference-dashboard` | `ghcr.io/datasance/ai-inference-dashboard` | The node |
| ONNX Runtime | `ghcr.io/eclipse-iofog/ai-inference-onnx-runtime` | `ghcr.io/datasance/ai-inference-onnx-runtime` | The engine image build |

A tag on one GitHub remote publishes only that remote's names. The tag shape is the same on both.

There is no `latest` tag. Pin the release you deploy.

| Git tag | Published tag |
|---|---|
| `v0.1.0` | Engine `0.1.0-ort1.31.0-<variant>` |
| `dashboard/v0.1.0` | Dashboard `0.1.0` |

The `v` is stripped. The engine tag carries the ONNX Runtime version, `ort1.31.0`, and the execution-provider variant. The architecture is a manifest entry, not part of the tag. `0.1.0-ort1.31.0-cpu` is the manifest for amd64, arm64, and riscv64. The catalog default is `cpu`. The dashboard tag has no ONNX Runtime token. Dashboard manifests are amd64 and arm64.

## Run it

REST is 8000, gRPC is 8001, metrics are 8002. `AUTH_MODE` is required. Mount the model directory read-only at `/models`. The process is `10001:10001`, needs no capability, and listens above 1024.

```bash
docker run --read-only --tmpfs /tmp \
  -e AUTH_MODE=disabled \
  -p 8000:8000 -p 8001:8001 -p 8002:8002 \
  -v "$PWD/models:/models:ro" \
  ghcr.io/eclipse-iofog/ai-inference-onnx:0.1.0-ort1.31.0-cpu
```

`podman run` takes the same image and the same variables. A missing `AUTH_MODE` exits the process. `disabled` is for a local run and for CI. An Edgelet catalog sets `edgelet`. Writable paths are `/var/cache/inference` and `/home/inference`. Device access for GPU, NPU, and QNN images is in [Images](docs/reference/images.md).

A model is one child directory of `/models` that resolves to an ONNX file. The directory name is the model name.

```text
/models/add/model.onnx
```

```bash
curl -sS -X POST "http://127.0.0.1:8000/v2/models/add/infer" \
  -H "Content-Type: application/json" \
  -d '{"inputs":[{"name":"A","datatype":"FP32","shape":[2,3],"data":[1,1,1,1,1,1]},{"name":"B","datatype":"FP32","shape":[2,3],"data":[2,2,2,2,2,2]}]}'
```

The served version is `"1"`. Output `C` is six threes. Health is open: `GET /v2/health/live` and `GET /v2/health/ready`.

## Open Inference Protocol

The wire contract is the KServe Open Inference Protocol, version 2. The gRPC service is `inference.GRPCInferenceService`. The protocol file in this repository is [`api/openinference/v2/open_inference_grpc.proto`](api/openinference/v2/open_inference_grpc.proto), Copyright 2023 The KServe Authors, Apache-2.0. The server does not publish a reflection service. Point a gRPC client at that file.

| Transport | Listener | Default |
|---|---|---|
| REST | 8000 | Plain HTTP |
| gRPC | 8001 | h2c |
| Metrics | 8002 | Plain HTTP, including when REST and gRPC use TLS |

With TLS off, which is the default, REST is HTTP and gRPC is h2c. `tlsEnabled` presents one certificate on both. `tlsCA` requires a client certificate. The metrics port stays plain HTTP.

Server metadata advertises the extensions `binary_tensor_data`, `model_repository`, `parameters`, and `schedule_policy`.

| Call | REST | gRPC |
|---|---|---|
| Server live and ready | `GET /v2/health/live`, `GET /v2/health/ready` | `ServerLive`, `ServerReady` |
| Server metadata | `GET /v2` | `ServerMetadata` |
| List models | `GET /v2/models` | Model index via repository index |
| Model metadata and ready | `GET /v2/models/{name}`, `.../ready` | `ModelMetadata`, `ModelReady` |
| Infer | `POST /v2/models/{name}/infer` | `ModelInfer` |
| Repository index | `POST /v2/repository/index` | `RepositoryIndex` |
| Load and unload | HTTP 501 | `RepositoryModelLoad`, `RepositoryModelUnload` are not performed |

The platform places and replaces files, so load and unload answer `{"error":"loading and unloading models is not supported"}`. A version in the URL is a decimal integer with no leading zero. `latest` and `v1` are 404. Omit the version and the server chooses. States are `READY`, `LOADING`, and `UNAVAILABLE`. Server ready means at least one ONNX model is `READY`. Server live means the process can answer.

`sequence_id`, `sequence_start`, and `sequence_end` are rejected. A binary tensor follows the JSON when an input sets `binary_data_size`. Full call lists: [REST](docs/reference/rest.md), [gRPC](docs/reference/grpc.md).

## Authentication

`authMode` is required. The environment variable is `AUTH_MODE`. If neither the environment nor `/etc/inference/config.yaml` sets it, the process exits. The mode applies to REST and gRPC. It does not apply to NATS.

These stay open in every mode:

- `GET /v2/health/live` and `GET /v2/health/ready`
- gRPC `ServerLive` and `ServerReady`
- `GET /metrics` on port 8002

| Mode | Where | Credential |
|---|---|---|
| `disabled` | `docker run`, `podman run`, CI | None |
| `static` | A shared bearer, every protected call | `Authorization: Bearer` equal to `authStaticToken` |
| `edgelet` | A catalog on a node | Edgelet service-account JWT |

`static` compares the bearer in constant time. The token is at least 32 characters, is not a JWT, has no per-model rules, and is never logged. A missing or short token exits the process. Changing it requires a restart. Presenting it allows every protected call. gRPC sends the same value in metadata.

`edgelet` verifies an Edgelet service-account JWT. The engine watches `/var/run/secrets/edgelet.iofog.org/serviceaccount/edgelet.jwk` and swaps the public key in memory when the file changes. The key is a public Ed25519 JWK (`kty` `OKP`, `crv` `Ed25519`, `alg` `EdDSA`). A missing or unreadable key at startup exits the process. After startup, a missing key fails new REST and gRPC calls until a valid key is loaded again.

A token passes when all of the following hold:

1. The signature matches the current `edgelet.jwk`, algorithm EdDSA.
2. `tokenUse` is `serviceaccount`, and `iat`, `nbf`, `exp`, and `iss` are valid. The issuer is `https://edgelet.default.svc.bridge.local`.
3. `rulesByGroup` allows the operation under the group `inference.iofog.org/v2`.

The `aud` claim is the Edgelet bridge, `https://edgelet.default.svc.bridge.local`. The engine accepts that audience. The signature shows the token was minted by this node. The API group shows the caller may call inference. A missing or invalid token is 401. A valid token whose rule does not allow the call is 403. gRPC uses `UNAUTHENTICATED` and `PERMISSION_DENIED`. The body is `{"error":"..."}` and does not echo claims.

Each node has its own signing key, so a token from another node fails verification. Revocation is the token `exp` window. The engine does not call Edgelet on the infer path. Callers on another node use NATS.

`GET` maps to the verb `get`. `POST` maps to `create`. Infer is `models/<name>/infer` with verb `create`. A role may grant one model, or `models/*` and `models/*/infer` for every model in the container. The full permission table is in [Authentication](docs/reference/auth.md).

## NATS

NATS is off unless both `natsServerUrl` and `natsCredsPath` are set. The platform injects them when a catalog sets `natsAccess: true`. A missing value does not stop REST or gRPC. A failed dial leaves the process up, and the client retries.

NATS does not check the service-account JWT. Account and user rules on the NATS server authenticate the connection. That is why a caller on another node uses NATS: the JWT is valid only on the node that signed it.

The engine queue-subscribes with queue group `inference` on every request subject, including health. The client publishes a request and reads the reply on its own inbox. The client does not set the queue group. Without it, one request would be delivered to every replica. Each process batches only the requests that land on it.

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
| `POST /v2/repository/index` | `inference.v2.repository.index` |

`inference.v2.models.wafer-defect.infer` and `inference.v2.models.wafer-defect.versions.1.infer` are the same flat model. A name that contains `*` or `>` is rejected.

The caller's user needs publish on the infer subjects and on `inference.xfer.>`. The engine user needs subscribe on `inference.v2.>` and `inference.xfer.>`, and publish on `inference.metrics.engines.>`. The dashboard user only subscribes to `inference.metrics.engines.>`. A catalog example is in [Catalog](docs/reference/catalog.md).

Two transfers:

- **Inline.** The body is the REST JSON. Use it when the body fits in the NATS server `max_payload`. The reply has header `Inference-Status` and the REST JSON. One empty message with no headers ends the stream.
- **Rendezvous.** Use it when the body does not fit. The client publishes the JSON header to the infer subject with `Inference-Transfer: rendezvous` and `Inference-Suffix-Length`. Tensor bytes are omitted from the JSON and named with `binary_data_size`. The engine replies with an empty body and `Inference-Accept: inference.xfer.<id>`. The client then publishes the raw little-endian bytes to that subject. The queue group applies only to the header, so the byte stream stays on the replica that accepted it. The ceiling is 32 MiB unless `maxPayloadBytes` sets another limit. A suffix larger than `max_payload` is several slices, one in flight at a time. The engine waits 5 seconds for each slice.

`sequence_id` is rejected on NATS with the same status as on REST. Worked requests, including the slice rules, are in [NATS](docs/reference/nats.md).

## Images and execution providers

ONNX Runtime is 1.31.0 in every engine image. The image tag selects the provider. There is no configuration key for it. After the library loads, the engine uses the first provider that library reports, in this order: TensorRT, CUDA, MIGraphX, OpenVINO, QNN, then CPU. It logs the chain once at start. If that provider cannot start, the model is `UNAVAILABLE` and the engine does not retry on CPU. An unsupported node may still run on CPU after the provider has started. That is graph partitioning.

Every variant above has its runtime digest recorded in [`build/onnxruntime/runtime-images.txt`](build/onnxruntime/runtime-images.txt). A release builds all of them.

| Variant | Architectures in the manifest | Accelerator SDK | Runtime recorded |
|---|---|---|---|
| `cpu` | amd64, arm64, riscv64 | None | Yes |
| `cuda12` | amd64, arm64 (SBSA) | CUDA 12.8.1, cuDNN 9.8.0.87 | Yes |
| `cuda13` | amd64, arm64 (SBSA and JetPack 7) | CUDA 13.0.2, cuDNN 9.14.0.64 | Yes |
| `qnn` | arm64 | QNN 2.51.0.260929, HTP backend | Yes |
| `tensorrt12` | amd64 | TensorRT 10.14.1.48, CUDA 12.9.1, cuDNN 9.10.2.21 | Yes |
| `tensorrt13` | amd64, arm64 (SBSA and JetPack 7) | amd64: TensorRT 10.14.1.48. arm64: TensorRT 10.15.1.29 | Yes |
| `openvino` | amd64 | OpenVINO 2026.2.1, CPU device | Yes |
| `migraphx` | amd64 | MIGraphX on ROCm 7.1 | Yes |

`cuda12` on arm64 is NVIDIA server aarch64 (SBSA: Grace, GH200, or an Arm server with a discrete GPU). `cuda13` and `tensorrt13` on arm64 cover SBSA and JetPack 7. A JetPack 6 image is not published. There is no arm64 `tensorrt12`.

`cpu` and `openvino` need no device. CUDA and TensorRT need the NVIDIA container toolkit (`--gpus` or CDI). `migraphx` needs `/dev/kfd` and `/dev/dri`, plus the numeric ids of groups `video` and `render` on that host. `qnn` needs the device nodes and the group id the board vendor assigns. Group ids differ between hosts, so the operator passes the number. TensorRT keeps its engine cache under `/var/cache/inference/tensorrt`. That cache survives a restart when `/var/cache/inference` is a volume.

A UBI engine image includes a shell, so an exec session can start. The riscv64 image does not. `/licenses` in every image holds the license and the third-party notices.

### ONNX Runtime images

`ghcr.io/eclipse-iofog/ai-inference-onnx-runtime` and `ghcr.io/datasance/ai-inference-onnx-runtime` are build inputs. A node does not pull them. The tag is `1.31.0-<variant>`, for example `1.31.0-cpu`. Both registries serve the same digest.

A maintainer publishes one architecture, then the multi-arch index:

```bash
make runtime-arch VARIANT=cpu ARCH=amd64
make runtime-arch VARIANT=cpu ARCH=arm64
make runtime-arch VARIANT=cpu ARCH=riscv64
make runtime-index VARIANT=cpu
```

The digest is then recorded in `build/onnxruntime/runtime-images.txt`. The ONNX Runtime workflow signs that recorded digest. It does not build the image. The engine release copies the signed runtime into the engine image. Accelerator runtimes ship inside the engine image. The CPU runtime is also published as three archives, one per architecture: `onnxruntime-1.31.0-linux-amd64.tar.gz`, `onnxruntime-1.31.0-linux-arm64.tar.gz`, and `onnxruntime-1.31.0-linux-riscv64.tar.gz`. A release verifies the CPU runtime image before it copies the files out.

`SBOM.md` at the repository root is the source inventory. A release attaches a CycloneDX file and an SPDX file for each engine image and for each CPU runtime archive. `make sbom` regenerates the source documents.

Tag, device, and license detail: [Images](docs/reference/images.md).

## Dashboard and metrics

The dashboard listens on 8080. It does not run inference and it does not read the engine config file. It subscribes to `inference.metrics.engines.>` and shows Nodes, Models, and Engine. An engine is stale at 30 seconds and drops off the live view. `GET /health` on the dashboard means the process can answer.

`GET /metrics` on the engine publishes the `inference_` prefix for Prometheus. The same numbers are published on NATS. The scrape is open. Model names on that port are operational metadata. NVML gauges are published when NVML loads, which is the CUDA and TensorRT images. Names: [Metrics](docs/reference/metrics.md). The views: [Dashboard](docs/user-guide/dashboard.md).

## Configuration

The engine reads `/etc/inference/config.yaml`. `CONFIG_PATH` selects another path. A missing file is normal. Environment variables are a complete configuration. The environment overrides the file.

| YAML key | Environment variable | Default |
|---|---|---|
| `modelsPath` | `MODELS_PATH` | `/models` |
| `authMode` | `AUTH_MODE` | required |
| `authStaticToken` | `AUTH_STATIC_TOKEN` | required when `authMode` is `static` |
| `natsServerUrl` | `NATS_SERVER_URL` | unset, NATS stays off |
| `natsCredsPath` | `NATS_CREDS_PATH` | unset, NATS stays off |
| `httpPort` | `HTTP_PORT` | `8000` |
| `grpcPort` | `GRPC_PORT` | `8001` |
| `metricsPort` | `METRICS_PORT` | `8002` |
| `logLevel` | `LOG_LEVEL` | `info` |

One JSON logger writes to stdout. The log never contains the static token, a bearer token, a JWT, the creds file, a private key, or tensor values. Every key: [Configuration](docs/reference/configuration.md).

## Documentation

| | |
|---|---|
| [docs/reference/](docs/reference/) | Repository, configuration, authentication, REST, gRPC, NATS, metrics, images, catalog |
| [docs/user-guide/](docs/user-guide/) | Run the container, bind a model, call infer, send a binary tensor, read a reload |
| [CONTRIBUTING.md](CONTRIBUTING.md) | How to contribute |
| [SECURITY.md](SECURITY.md) | How to report a vulnerability |
| [CHANGELOG.md](CHANGELOG.md) | Release notes |

## Project layout

```text
cmd/inference/            engine main
dashboard/                second module; does not link ONNX Runtime
internal/                 config, auth, repository, session, batch, HTTP, gRPC, NATS, metrics
internal/ort/             wrapper over the ONNX Runtime Go bindings
api/openinference/v2/     Open Inference Protocol buffers
build/                    engine and dashboard Dockerfiles
build/onnxruntime/        runtime Dockerfiles and recorded runtime digests
testdata/models/          one small ONNX file, committed
docs/                     operator reference and task book
```

`make test` covers both modules and does not load a session. `make quality` runs lint, vet, security-code, vulncheck, and `make test`. A test that loads a session needs `ORT_LIB_PATH` set to the shared library file.
