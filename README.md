# ai-inference-onnx

[![CI](https://github.com/eclipse-iofog/ai-inference-onnx/actions/workflows/ci.yml/badge.svg)](https://github.com/eclipse-iofog/ai-inference-onnx/actions/workflows/ci.yml)
[![govulncheck](https://github.com/eclipse-iofog/ai-inference-onnx/actions/workflows/govulncheck.yml/badge.svg)](https://github.com/eclipse-iofog/ai-inference-onnx/actions/workflows/govulncheck.yml)
[![OpenSSF Scorecard](https://api.securityscorecards.dev/projects/github.com/eclipse-iofog/ai-inference-onnx/badge)](https://securityscorecards.dev/viewer/?uri=github.com/eclipse-iofog/ai-inference-onnx)
[![Go](https://img.shields.io/badge/Go-1.26.10-blue.svg)](https://go.dev/)
[![License](https://img.shields.io/badge/License-EPL--2.0-blue.svg)](LICENSE)

One container is a model repository. Each child directory that resolves to an ONNX file is one Open Inference Protocol model. The same image runs under `docker run`, `podman run`, and an Edgelet catalog. The engine does not register itself with the Controller.

Language is Go 1.26.10. License is [EPL-2.0](LICENSE).

The engine image is [`ghcr.io/eclipse-iofog/ai-inference-onnx`](https://ghcr.io/eclipse-iofog/ai-inference-onnx) on `eclipse-iofog/ai-inference-onnx` and [`ghcr.io/datasance/ai-inference-onnx`](https://ghcr.io/datasance/ai-inference-onnx) on `datasance/ai-inference-onnx`. The dashboard image is [`ghcr.io/eclipse-iofog/ai-inference-dashboard`](https://ghcr.io/eclipse-iofog/ai-inference-dashboard) and [`ghcr.io/datasance/ai-inference-dashboard`](https://ghcr.io/datasance/ai-inference-dashboard).

A `v0.1.0` tag on a remote publishes `0.1.0-ort1.31.0-cpu` on that remote's engine name. The variant table is [docs/reference/images.md](docs/reference/images.md).

The operator reference and the task book are in [docs/](docs/).

Contributing is described in [CONTRIBUTING.md](CONTRIBUTING.md).

Security reports follow [SECURITY.md](SECURITY.md).
