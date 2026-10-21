#!/usr/bin/env bash
# Run vet, lint, vulncheck, and gosec for both modules inside Linux.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

GO_IMAGE="${GO_IMAGE:-golang:1.26.10-trixie@sha256:7202554ed358a37f41c44e4910ae29bbc5ef0715f289ac637218adbb0ca132dd}"
GOARCH="${GOARCH:-arm64}"
GOLANGCI_LINT_VERSION="${GOLANGCI_LINT_VERSION:-v2.12.2}"
GOVULNCHECK_VERSION="${GOVULNCHECK_VERSION:-v1.1.4}"
GOSEC_VERSION="${GOSEC_VERSION:-v2.29.0}"

echo "=== quality-linux (linux/${GOARCH}) ==="

docker run --rm --platform "linux/${GOARCH}" \
	-v "${ROOT}:/src" -w /src "${GO_IMAGE}" bash -euxo pipefail -c "
		go version
		go mod download
		(cd dashboard && go mod download)

		echo '=== go vet ==='
		go vet ./...
		(cd dashboard && go vet ./...)

		curl -sSfL https://github.com/golangci/golangci-lint/releases/download/${GOLANGCI_LINT_VERSION}/golangci-lint-${GOLANGCI_LINT_VERSION#v}-linux-${GOARCH}.tar.gz \
			| tar xz -C /tmp
		GOLANGCI=/tmp/golangci-lint-${GOLANGCI_LINT_VERSION#v}-linux-${GOARCH}/golangci-lint

		echo '=== golangci-lint ==='
		\$GOLANGCI run --config .golangci.yaml --timeout=10m0s
		(cd dashboard && \$GOLANGCI run --config ../.golangci.yaml --timeout=10m0s)

		go install golang.org/x/vuln/cmd/govulncheck@${GOVULNCHECK_VERSION}
		chmod +x scripts/vulncheck.sh
		scripts/vulncheck.sh
		go mod verify
		(cd dashboard && go mod verify)

		go install github.com/securego/gosec/v2/cmd/gosec@${GOSEC_VERSION}
		echo '=== gosec ==='
		gosec -exclude-dir=.cursor -exclude-dir=dashboard ./...
		(cd dashboard && gosec ./...)
	"

echo "quality-linux (linux/${GOARCH}): vet, lint, vulncheck, and gosec passed"
