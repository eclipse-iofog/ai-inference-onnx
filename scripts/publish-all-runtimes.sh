#!/usr/bin/env bash
# Publish every runtime variant. Stop at the first failed build or index.
# docker buildx create --name runtime --driver docker-container --use
#  chmod +x scripts/publish-all-runtimes.sh
# caffeinate -dimsu scripts/publish-all-runtimes.sh

set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"
log="${root}/.runtime-publish/publish.log"
mkdir -p "${root}/.runtime-publish"
exec > >(tee -a "$log") 2>&1

step() {
	printf '\n%s %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*"
	"$@"
}

step make runtime-arch VARIANT=cpu ARCH=amd64
step make runtime-arch VARIANT=cpu ARCH=arm64
step make runtime-arch VARIANT=cpu ARCH=riscv64
step make runtime-index VARIANT=cpu

step make runtime-arch VARIANT=cuda12 ARCH=amd64
step make runtime-arch VARIANT=cuda12 ARCH=arm64
step make runtime-index VARIANT=cuda12

step make runtime-arch VARIANT=cuda13 ARCH=amd64
step make runtime-arch VARIANT=cuda13 ARCH=arm64
step make runtime-index VARIANT=cuda13

step make runtime-arch VARIANT=tensorrt12 ARCH=amd64
step make runtime-index VARIANT=tensorrt12

step make runtime-arch VARIANT=tensorrt13 ARCH=amd64
step make runtime-arch VARIANT=tensorrt13 ARCH=arm64
step make runtime-index VARIANT=tensorrt13

step make runtime-arch VARIANT=qnn ARCH=arm64
step make runtime-index VARIANT=qnn

step make runtime-arch VARIANT=openvino ARCH=amd64
step make runtime-index VARIANT=openvino

step make runtime-arch VARIANT=migraphx ARCH=amd64
step make runtime-index VARIANT=migraphx

printf '\n%s all runtimes published\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"