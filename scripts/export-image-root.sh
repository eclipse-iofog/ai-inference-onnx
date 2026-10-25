#!/usr/bin/env bash
# Copy an image root filesystem out without running the image.
set -euo pipefail

if [ "$#" -ne 3 ]; then
	echo "usage: export-image-root.sh <image> <platform> <dest>" >&2
	exit 2
fi

image="$1"
platform="$2"
dest="$3"
case "$platform" in
linux/amd64 | linux/arm64 | linux/riscv64) ;;
*)
	echo "unknown platform ${platform}" >&2
	exit 2
	;;
esac
if [ -z "$image" ]; then
	echo "image is empty" >&2
	exit 2
fi

mkdir -p "$dest"
tmp="$(mktemp -d)"
cleanup() {
	rm -rf "$tmp"
}
trap cleanup EXIT
printf 'FROM %s\n' "$image" >"${tmp}/Dockerfile"
docker buildx build \
	--platform "$platform" \
	--output "type=local,dest=${dest}" \
	--provenance=false \
	--sbom=false \
	"$tmp"
