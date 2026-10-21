.PHONY: help fmt vet lint lint-fix install-lint security-code vulncheck test quality \
	quality-linux quality-linux-amd64 quality-linux-arm64

GOBIN ?= $(shell go env GOBIN)
ifeq ($(GOBIN),)
GOBIN := $(shell go env GOPATH)/bin
endif

export PATH := $(GOBIN):$(PATH)

# Pinned quality tools.
GOLANGCI_LINT_VERSION ?= v2.12.2
GOLANGCI_LINT         := $(GOBIN)/golangci-lint
GOVULNCHECK_VERSION   ?= v1.1.4
GOSEC_VERSION         ?= v2.29.0

.DEFAULT_GOAL := help

help: ## Show this help message
	@echo 'Usage: make [target]'
	@echo ''
	@echo 'Available targets:'
	@grep -E '^[a-zA-Z0-9_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-22s\033[0m %s\n", $$1, $$2}'

fmt: ## Format the engine module, then the dashboard module
	go fmt ./...
	cd dashboard && go fmt ./...

vet: ## Run go vet on the engine module, then the dashboard module
	go vet ./...
	cd dashboard && go vet ./...

# Download golangci-lint when the pinned binary is absent.
$(GOLANGCI_LINT):
	@echo "Installing golangci-lint $(GOLANGCI_LINT_VERSION) into $(GOBIN)..."
	@curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh \
		| sh -s -- -b $(GOBIN) $(GOLANGCI_LINT_VERSION)
	@echo "golangci-lint $(GOLANGCI_LINT_VERSION) installed"

install-lint: $(GOLANGCI_LINT) ## Install golangci-lint at the pinned version
	@$(GOLANGCI_LINT) version

lint: $(GOLANGCI_LINT) ## Run golangci-lint on the engine module, then the dashboard module
	$(GOLANGCI_LINT) run --config .golangci.yaml
	cd dashboard && $(GOLANGCI_LINT) run --config ../.golangci.yaml

lint-fix: $(GOLANGCI_LINT) ## Run golangci-lint and apply fixes on both modules
	$(GOLANGCI_LINT) run --config .golangci.yaml --fix
	cd dashboard && $(GOLANGCI_LINT) run --config ../.golangci.yaml --fix

security-code: ## Run gosec on the engine module, then the dashboard module
	@if ! command -v gosec >/dev/null 2>&1; then \
		echo "Installing gosec $(GOSEC_VERSION)..."; \
		go install github.com/securego/gosec/v2/cmd/gosec@$(GOSEC_VERSION); \
	fi
	gosec -exclude-dir=.cursor -exclude-dir=dashboard ./...
	cd dashboard && gosec ./...

vulncheck: ## Scan both modules, then verify module checksums
	@if ! command -v govulncheck >/dev/null 2>&1; then \
		echo "Installing govulncheck $(GOVULNCHECK_VERSION)..."; \
		go install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION); \
	fi
	chmod +x scripts/vulncheck.sh
	scripts/vulncheck.sh
	go mod verify
	cd dashboard && go mod verify

# The race detector on Linux needs cgo. These packages have no cgo files,
# so that run does not link ONNX Runtime.
ENGINE_RACE_PKGS := \
	./api/... \
	./internal/auth/... \
	./internal/batch/... \
	./internal/config/... \
	./internal/discover/... \
	./internal/grpcapi/... \
	./internal/httpapi/... \
	./internal/metrics/...

test: ## Tests that do not load a session, in both modules
	CGO_ENABLED=0 go test -count=1 ./...
	CGO_ENABLED=1 go test -race -count=1 $(ENGINE_RACE_PKGS)
	cd dashboard && CGO_ENABLED=1 go test -race -count=1 ./...

quality: lint vet security-code vulncheck test ## Lint, vet, gosec, vulnerability scan, and test

quality-linux-arm64: ## Run vet, lint, vulncheck, and gosec in Linux arm64
	chmod +x scripts/quality-linux.sh
	GOARCH=arm64 scripts/quality-linux.sh

quality-linux-amd64: ## Run vet, lint, vulncheck, and gosec in Linux amd64
	chmod +x scripts/quality-linux.sh
	GOARCH=amd64 scripts/quality-linux.sh

# On Apple Silicon this is the arm64 Linux parity path.
quality-linux: quality-linux-arm64 ## Linux parity path
