# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [v0.1.0-rc.4]

### Added

- Server metadata extensions include the execution provider chain and the ONNX Runtime version, on REST, gRPC, and NATS. A CPU image reports `execution_providers:CPUExecutionProvider` and `ort_version:1.31.0`. The metrics snapshot carries the same facts as `execution_providers` and `ort_version`. An empty chain and an empty version mean the runtime library did not load.

### Changed

- Server metadata `name` is the server id. `version` is the engine version linked into the image, or `dev` when the binary was built without one.
- The engine writes one JSON event per line. After settings load, every line includes `service` and `server_id`. Infer on HTTP, gRPC, and NATS shares one record: transport, model, version, request id, status, and duration. The log also records startup settings, an unavailable model and its reason, each denied REST or gRPC call, and a metrics publish that failed. A successful infer stays at debug. Tokens, credentials, and tensor values stay out of the log.

## [dashboard/v0.1.0-rc.4]

### Changed

- Nodes, Models, and the engine summary use the last 15 minutes. A quiet latest snapshot no longer clears the rate, the error percentage, or the latency.
- The request-rate graph uses green for success and red for failure. Model lines use a separate palette and are labeled `name:version`.
- Queue depth is one cell in the graph grid. Model cards sit side by side, at most four on a row. The model name is the card title, and the version is a badge under it.
- Live, Ready, and NATS connected are badges. Success and each failure reason on a model card are badges.
- The engine view shows the ONNX Runtime version and the execution provider chain from the snapshot, each as a badge. An empty version or an empty chain is unknown.

## [dashboard/v0.1.0-rc.3]

### Added

- The engine view graphs request rate, request latency, queue latency, compute latency, and queue depth for the last 15 minutes. Each page reloads every 10 seconds.

### Changed

- Latency is the time added since the previous snapshot, divided by the successes added since that snapshot. Nodes and Models show the request rate, the error percentage, and that latency. A model link on the engine view limits the graphs to that model.

## [v0.1.0-rc.3]

### Added

- Software bill of materials. The repository root carries `SBOM.md`, `LICENSE-3RD-PARTY.md`, `sbom.cdx.json`, and `sbom.spdx.json` for the source tree. The engine image SBOM is the registry attestation on the pushed image. A release attaches a CycloneDX file and an SPDX file for each CPU runtime archive.

### Fixed

- `sbom.cdx.json` and `sbom.spdx.json` list required and optional components that have a version. Excluded modules stay in `SBOM.md` and `LICENSE-3RD-PARTY.md`. Generating those documents no longer appends unused module checksums to `go.sum`.
- UBI 9 builds no longer install the `curl` package. The base image already provides `curl-minimal`, and the two packages conflict. This covers the QNN, OpenVINO, and TensorRT engine and runtime images.
- A TensorRT archive extracts its top-level headers and libraries. The CUDA-style `targets` paths are not in that archive.
- The MIGraphX runtime image installs `migraphx` and `migraphx-devel` from the ROCm 7.1 image and compiles with GCC 13. The engine image installs the same packages when it stages the ROCm libraries.

## [v0.1.0-rc.2] - 2026-10-21

### Fixed

- A model directory that is a symlink is loaded. A directory whose name starts with `..` is not a model.

### Changed

- A UBI engine image includes a shell, so an exec session can start. The riscv64 image does not.

## [dashboard/v0.1.0-rc.2] - 2026-10-21

### Changed

- The dashboard image includes a shell, so an exec session can start.

## [v0.1.0-rc.1] - 2026-10-21

### Added

- Initial release. REST, gRPC, and NATS serve one model repository with the Open Inference Protocol.
- Dynamic batching groups requests for a model. A request can carry a binary tensor.
- Execution-provider images cover CPU, CUDA, TensorRT, QNN, OpenVINO, and MIGraphX. CPU is the catalog default.
- Metrics record success, failure, queue depth, batch size, and duration. Prometheus and NATS publish the same numbers.
- The dashboard reads those metrics. It does not run inference.
- Operator docs describe the repository, configuration, calls, metrics, images, and day-to-day tasks.

## [dashboard/v0.1.0-rc.1] - 2026-10-21

### Added

- The dashboard shows three views: Nodes, Models, and Engine.
- The dashboard image does not link ONNX Runtime and does not run inference.
