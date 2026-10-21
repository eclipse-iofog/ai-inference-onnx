# Contributing

Thank you for contributing to the ONNX inference engine (`github.com/eclipse-iofog/ai-inference-onnx`). The engine module lives at the repository root. The dashboard is a second module under `dashboard/`.

## Branch workflow

1. Fork [github.com/eclipse-iofog/ai-inference-onnx](https://github.com/eclipse-iofog/ai-inference-onnx).
2. Clone your fork and create a feature branch from **`develop`**.
3. Make focused changes, with tests where the behavior is not obvious.
4. Run the gates below before opening a pull request.
5. Open the pull request against **`develop`**, with a description of the change and how you tested it.

## Required gates

Run these from the repository root:

```bash
make fmt
make lint
make security-code
make vulncheck
make test
```

`make quality` runs lint, vet, `security-code`, `vulncheck`, and `test` on the engine module, then on the dashboard module. Run `make fmt` as well. `make quality` does not format.

On Darwin, `make quality-linux` is the Linux parity path. It runs the Linux vet, lint, vulnerability scan, and gosec checks for both modules inside Docker. `make quality-linux-arm64` and `make quality-linux-amd64` select the architecture.

## Code style

- Format with `make fmt`.
- Run `make lint` before submitting. The lint budget is zero issues.
- Comment exported APIs and non-obvious logic. Write comments in product terms.
- Keep the engine under `cmd/inference` and `internal/`. Keep dashboard code in the dashboard module.

## Testing

- `make test` is the race-detector suite that does not load a session. It runs in both modules.
- A test that loads a session needs `ORT_LIB_PATH` set to the ONNX Runtime shared library file.
- Add or extend tests for new behavior. Prefer table-driven tests next to the code they cover.

## Pull requests

Update the operator pages under `docs/` when operator-visible behavior changes. Do not paste the reference into the pull request description. The required gates above must pass locally or in CI.

## Security

Report a vulnerability as described in [SECURITY.md](SECURITY.md). Do not open a public GitHub issue for an exploitable finding.

## Questions

Open a GitHub issue for bugs and feature discussion, or contact the maintainers listed in [MAINTAINERS.md](MAINTAINERS.md).
