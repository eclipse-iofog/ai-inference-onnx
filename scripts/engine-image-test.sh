#!/usr/bin/env bash
# Call the committed model inside the CPU engine image over REST, gRPC, and NATS.
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root"

# The container uses the library path baked into the image.
unset ORT_LIB_PATH

exec go run ./scripts/engineimage
