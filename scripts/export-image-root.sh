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

# The published digest is an image index. Its provenance attestation is not a
# root filesystem, and exporting that index tries to create a buildinfo directory.
platform_manifest() {
	local image="$1" platform="$2" raw selected
	if ! command -v python3 >/dev/null 2>&1; then
		echo "python3 is not installed" >&2
		exit 1
	fi
	raw="$(docker buildx imagetools inspect --raw "$image")"
	selected="$(
		printf '%s' "$raw" | python3 - "$image" "$platform" <<'PY'
import json
import sys

image, platform = sys.argv[1], sys.argv[2]
os_name, arch = platform.split("/", 1)
data = json.load(sys.stdin)
manifests = data.get("manifests")
if not isinstance(manifests, list):
    sys.stdout.write(image)
    raise SystemExit(0)
selected = ""
for item in manifests:
    if not isinstance(item, dict):
        continue
    plat = item.get("platform") or {}
    if plat.get("os") != os_name or plat.get("architecture") != arch:
        continue
    annotations = item.get("annotations") or {}
    if annotations.get("vnd.docker.reference.type") == "attestation-manifest":
        continue
    digest = item.get("digest") or ""
    if digest.startswith("sha256:"):
        selected = digest
        break
if not selected:
    sys.exit(f"image has no {platform} manifest")
sys.stdout.write(image.split("@", 1)[0] + "@" + selected)
PY
	)"
	printf '%s\n' "$selected"
}

mkdir -p "$dest"
tmp="$(mktemp -d)"
cleanup() {
	rm -rf "$tmp"
}
trap cleanup EXIT
image="$(platform_manifest "$image" "$platform")"
printf 'FROM %s\n' "$image" >"${tmp}/Dockerfile"
docker buildx build \
	--platform "$platform" \
	--output "type=local,dest=${dest}" \
	--provenance=false \
	--sbom=false \
	"$tmp"
