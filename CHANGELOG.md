# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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
