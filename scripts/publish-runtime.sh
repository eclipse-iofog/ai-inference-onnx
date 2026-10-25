#!/usr/bin/env bash
# Publish one ONNX Runtime variant.
# arch builds one architecture and pushes it by digest.
# index tags those digests and copies the same index to the fork registry.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source_image="ghcr.io/eclipse-iofog/ai-inference-onnx-runtime"
mirror_image="ghcr.io/datasance/ai-inference-onnx-runtime"
runtime_version="1.31.0"

usage() {
	echo "usage: publish-runtime.sh arch <variant> <arch>" >&2
	echo "       publish-runtime.sh index <variant>" >&2
}

arches_for() {
	case "$1" in
	cpu) printf '%s\n' amd64 arm64 riscv64 ;;
	cuda12 | cuda13 | tensorrt13) printf '%s\n' amd64 arm64 ;;
	tensorrt12 | openvino | migraphx) printf '%s\n' amd64 ;;
	qnn) printf '%s\n' arm64 ;;
	*) return 1 ;;
	esac
}

platform_for() {
	case "$1" in
	amd64) printf '%s\n' linux/amd64 ;;
	arm64) printf '%s\n' linux/arm64 ;;
	riscv64) printf '%s\n' linux/riscv64 ;;
	*) return 1 ;;
	esac
}

digest_dir() {
	printf '%s\n' "${root}/.runtime-publish/${1}"
}

require_variant() {
	local variant="$1"
	if ! arches_for "$variant" >/dev/null; then
		echo "unknown variant ${variant}" >&2
		usage
		exit 2
	fi
}

read_build_digest() {
	local meta="$1"
	python3 - "$meta" <<'PY'
import json
import sys

path = sys.argv[1]
with open(path, encoding="utf-8") as handle:
    data = json.load(handle)
digest = data.get("containerimage.digest", "")
if not isinstance(digest, str) or len(digest) != 71 or not digest.startswith("sha256:"):
    sys.exit("runtime digest is missing")
sys.stdout.write(digest)
PY
}

read_index_digest() {
	local manifest="$1"
	python3 - "$manifest" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    data = json.load(handle)
found = ""
for key in ("Digest", "digest"):
    value = data.get(key)
    if isinstance(value, str) and value.startswith("sha256:"):
        found = value
        break
if not found:
    body = data.get("Manifest") or data.get("manifest") or {}
    if isinstance(body, dict):
        value = body.get("digest") or body.get("Digest") or ""
        if isinstance(value, str):
            found = value.strip('"')
if len(found) != 71 or not found.startswith("sha256:"):
    sys.exit("manifest digest is missing")
sys.stdout.write(found)
PY
}

publish_arch() {
	local variant="$1"
	local arch="$2"
	local allowed=0 candidate platform file dir meta digest
	require_variant "$variant"
	if ! platform="$(platform_for "$arch")"; then
		echo "unknown architecture ${arch}" >&2
		usage
		exit 2
	fi
	while IFS= read -r candidate; do
		if [ "$candidate" = "$arch" ]; then
			allowed=1
		fi
	done < <(arches_for "$variant")
	if [ "$allowed" -ne 1 ]; then
		echo "${variant} has no ${arch} runtime" >&2
		exit 2
	fi
	file="${root}/build/onnxruntime/Dockerfile.${variant}"
	if [ ! -f "$file" ]; then
		echo "runtime dockerfile is missing" >&2
		exit 1
	fi
	if ! command -v docker >/dev/null 2>&1; then
		echo "docker is not installed" >&2
		exit 1
	fi
	if ! command -v python3 >/dev/null 2>&1; then
		echo "python3 is not installed" >&2
		exit 1
	fi
	dir="$(digest_dir "$variant")"
	mkdir -p "$dir"
	meta="${dir}/.${arch}.metadata.json"
	rm -f "$meta"
	docker buildx build \
		--file "$file" \
		--target runtime \
		--platform "$platform" \
		--sbom=true \
		--provenance=false \
		--output "type=image,name=${source_image},push-by-digest=true,name-canonical=true,push=true" \
		--metadata-file "$meta" \
		"$root"
	digest="$(read_build_digest "$meta")"
	rm -f "$meta"
	printf '%s\n' "$digest" >"${dir}/${arch}"
	write_runtime_sbom "$variant" "$arch" "$platform" "$digest" "$dir"
	printf 'pushed %s %s %s\n' "$variant" "$arch" "$digest" >&2
}

write_runtime_sbom() {
	local variant="$1" arch="$2" platform="$3" digest="$4" dir="$5"
	local stage
	if ! command -v go >/dev/null 2>&1; then
		echo "go is not installed" >&2
		exit 1
	fi
	chmod +x "${root}/scripts/export-image-root.sh"
	stage="$(mktemp -d)"
	trap "rm -rf $(printf '%q' "$stage")" EXIT
	"${root}/scripts/export-image-root.sh" "${source_image}@${digest}" "$platform" "$stage"
	(
		cd "${root}/licensegen"
		go run . -root .. -release \
			-subject runtime \
			-variant "$variant" \
			-arch "$arch" \
			-version "$runtime_version" \
			-stage "$stage" \
			-out "$dir"
	)
	rm -rf "$stage"
}

publish_index() {
	local variant="$1"
	local dir arch digest tag manifest index copied
	local -a refs=()
	require_variant "$variant"
	if ! command -v docker >/dev/null 2>&1; then
		echo "docker is not installed" >&2
		exit 1
	fi
	if ! command -v python3 >/dev/null 2>&1; then
		echo "python3 is not installed" >&2
		exit 1
	fi
	dir="$(digest_dir "$variant")"
	while IFS= read -r arch; do
		if [ ! -f "${dir}/${arch}" ]; then
			echo "${variant} ${arch} has not been pushed" >&2
			exit 1
		fi
		digest="$(tr -d '[:space:]' <"${dir}/${arch}")"
		case "$digest" in
		sha256:*) ;;
		*)
			echo "${variant} ${arch} digest is malformed" >&2
			exit 1
			;;
		esac
		refs+=("${source_image}@${digest}")
	done < <(arches_for "$variant")
	if ! command -v crane >/dev/null 2>&1; then
		echo "crane is not installed" >&2
		exit 1
	fi
	tag="${source_image}:${runtime_version}-${variant}"
	docker buildx imagetools create -t "$tag" "${refs[@]}"
	manifest="$(mktemp)"
	docker buildx imagetools inspect --format '{{json .}}' "$tag" >"$manifest"
	index="$(read_index_digest "$manifest")"
	rm -f "$manifest"
	crane copy "${source_image}@${index}" "${mirror_image}:${runtime_version}-${variant}"
	copied="$(crane digest "${mirror_image}:${runtime_version}-${variant}")"
	copied="$(printf '%s' "$copied" | tr -d '[:space:]')"
	if [ "$copied" != "$index" ]; then
		echo "fork registry digest does not match" >&2
		exit 1
	fi
	printf '%s\n' "$index" >"${dir}/index"
	printf '%s %s-%s@%s\n' "$variant" "$runtime_version" "$variant" "$index"
}

command="${1:-}"
case "$command" in
arch)
	if [ "$#" -ne 3 ] || [ -z "${2:-}" ] || [ -z "${3:-}" ]; then
		usage
		exit 2
	fi
	publish_arch "$2" "$3"
	;;
index)
	if [ "$#" -ne 2 ] || [ -z "${2:-}" ]; then
		usage
		exit 2
	fi
	publish_index "$2"
	;;
*)
	usage
	exit 2
	;;
esac
