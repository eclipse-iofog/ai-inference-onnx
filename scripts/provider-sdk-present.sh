#!/bin/bash
# Report whether a runtime variant has the inputs its build needs.
# The decision reads the environment only.

set -euo pipefail

if [[ $# -ne 1 ]]; then
  echo "usage: provider-sdk-present.sh <variant>" >&2
  exit 2
fi

variant="$1"

case "$variant" in
  cpu)
    printf '%s\n' present
    ;;
  cuda12 | cuda13 | tensorrt13)
    if [[ -n "${ORT_LARGE_RUNNER_AMD64:-}" && -n "${ORT_LARGE_RUNNER_ARM64:-}" ]]; then
      printf '%s\n' present
    else
      printf '%s\n' "not built"
    fi
    ;;
  tensorrt12 | openvino | migraphx)
    if [[ -n "${ORT_LARGE_RUNNER_AMD64:-}" ]]; then
      printf '%s\n' present
    else
      printf '%s\n' "not built"
    fi
    ;;
  qnn)
    if [[ -n "${ORT_LARGE_RUNNER_ARM64:-}" && -n "${QNN_SDK_URL:-}" && -n "${QNN_SDK_SHA256:-}" ]]; then
      printf '%s\n' present
    else
      printf '%s\n' "not built"
    fi
    ;;
  *)
    echo "unknown variant ${variant}" >&2
    exit 2
    ;;
esac
