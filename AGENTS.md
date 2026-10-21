# ai-inference-onnx

This repository is the ONNX inference engine (`github.com/eclipse-iofog/ai-inference-onnx`). One process loads ONNX models from a model repository and serves them with the Open Inference Protocol. The dashboard is a second Go module in the same repository.

## Module layout

```
cmd/inference/                 engine main
internal/config
internal/auth
internal/discover
internal/session
internal/batch
internal/httpapi
internal/grpcapi
internal/natsapi
internal/metrics
internal/ort                   thin wrapper over the official Go bindings
api/openinference/v2/          protocol buffers, generated during the build
testdata/models/               one small ONNX file, committed
dashboard/                     second module, its own command and internal packages
build/                         engine and dashboard Dockerfiles
build/onnxruntime/             runtime Dockerfiles and the recorded runtime digests
scripts/                       pinned ONNX Runtime build, digest checker, image test, quality-linux, vulncheck
docs/                          operator docs, published
Makefile                       local and CI quality gate
```

## Tests

`make test` covers both modules without loading a session. The engine module runs `CGO_ENABLED=0 go test -count=1 ./...`, then `CGO_ENABLED=1 go test -race -count=1` on the packages with no cgo files. The dashboard module runs `CGO_ENABLED=1 go test -race -count=1 ./...`.

`make quality` runs lint, vet, security-code, vulncheck, and `make test`.

`ORT_LIB_PATH` is required when a test loads a session. Set it to the shared library file. `make test` does not load a session and does not need that variable.

## Dashboard

The dashboard module does not link ONNX Runtime.

## Comments

Comments stay in product terms.
