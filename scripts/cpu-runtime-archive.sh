#!/usr/bin/env bash
# Copy the CPU runtime out of the signed image and pack one archive per architecture.
# This does not compile ONNX Runtime.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

print_names() {
	printf '%s\n' \
		"onnxruntime-1.31.0-linux-amd64.tar.gz" \
		"onnxruntime-1.31.0-linux-arm64.tar.gz" \
		"onnxruntime-1.31.0-linux-riscv64.tar.gz"
}

if [ "${1:-}" = "--print-names" ]; then
	if [ "$#" -ne 1 ]; then
		echo "usage: cpu-runtime-archive.sh --print-names" >&2
		exit 2
	fi
	print_names
	exit 0
fi

if [ "$#" -gt 1 ]; then
	echo "usage: cpu-runtime-archive.sh [output-dir]" >&2
	exit 2
fi

out="${1:-dist}"
case "$out" in
/*) ;;
*) out="$(pwd)/${out}" ;;
esac

trim() {
	local value="$1"
	value="${value#"${value%%[![:space:]]*}"}"
	value="${value%"${value##*[![:space:]]}"}"
	printf '%s' "$value"
}

lock="${root}/build/onnxruntime/runtime-images.txt"
if [ ! -f "$lock" ]; then
	echo "runtime-images.txt is missing" >&2
	exit 1
fi

ref=""
while IFS= read -r raw || [ -n "$raw" ]; do
	line="$(trim "$raw")"
	case "$line" in
	"" | \#*) continue ;;
	esac
	variant="${line%% *}"
	rest="$(trim "${line#"$variant"}")"
	if [ "$variant" != "cpu" ]; then
		continue
	fi
	if [ -n "$ref" ]; then
		echo "runtime-images.txt records cpu more than once" >&2
		exit 1
	fi
	ref="$rest"
done <"$lock"

if [ -z "$ref" ]; then
	echo "cpu runtime is not recorded in runtime-images.txt" >&2
	exit 1
fi

if ! printf '%s\n' "$ref" | grep -Eq '^1\.31\.0-cpu@sha256:[0-9a-f]{64}$'; then
	echo "cpu line in runtime-images.txt is malformed" >&2
	exit 1
fi

registry="${IMAGE_REGISTRY:-ghcr.io/eclipse-iofog}"
registry="${registry%/}"
image="${registry}/ai-inference-onnx-runtime:${ref}"

repo="${GITHUB_REPOSITORY:-}"
if [ -z "$repo" ]; then
	origin="$(git -C "$root" remote get-url origin 2>/dev/null || true)"
	repo="${origin%.git}"
	repo="${repo#https://github.com/}"
	repo="${repo#ssh://git@github.com/}"
	repo="${repo#git@github.com:}"
fi
case "$repo" in
*/*) ;;
*)
	echo "repository is unknown" >&2
	exit 1
	;;
esac

branch="${DEFAULT_BRANCH:-}"
if [ -z "$branch" ]; then
	branch="$(git -C "$root" symbolic-ref --short refs/remotes/origin/HEAD 2>/dev/null || true)"
	branch="${branch#origin/}"
fi
if [ -z "$branch" ]; then
	echo "default branch is unknown" >&2
	exit 1
fi

identity="https://github.com/${repo}/.github/workflows/onnxruntime.yml@refs/heads/${branch}"
cosign verify \
	--certificate-identity "$identity" \
	--certificate-oidc-issuer https://token.actions.githubusercontent.com \
	"$image"

tmp="$(mktemp -d)"
cleanup() {
	rm -rf "$tmp"
}
trap cleanup EXIT

context="${tmp}/context"
mkdir -p "$context"
printf 'FROM %s\n' "$image" >"${context}/Dockerfile"
packed="${tmp}/archives"
mkdir -p "$packed"

copy_platform() {
	local platform="$1"
	local archive="$2"
	local dest="${tmp}/root-${platform//\//-}"
	mkdir -p "$dest"
	docker buildx build \
		--platform "$platform" \
		--output "type=local,dest=${dest}" \
		--provenance=false \
		"$context"
	local part
	for part in lib include licenses VERSION COMMIT; do
		if [ ! -e "${dest}/${part}" ]; then
			echo "runtime image is missing ${part} for ${platform}" >&2
			exit 1
		fi
	done
	tar -C "$dest" -czf "${packed}/${archive}" lib include licenses VERSION COMMIT
}

copy_platform linux/amd64 onnxruntime-1.31.0-linux-amd64.tar.gz
copy_platform linux/arm64 onnxruntime-1.31.0-linux-arm64.tar.gz
copy_platform linux/riscv64 onnxruntime-1.31.0-linux-riscv64.tar.gz

mkdir -p "$out"
cp "${packed}/onnxruntime-1.31.0-linux-amd64.tar.gz" \
	"${packed}/onnxruntime-1.31.0-linux-arm64.tar.gz" \
	"${packed}/onnxruntime-1.31.0-linux-riscv64.tar.gz" \
	"$out/"
