#!/usr/bin/env bash
# Build the pinned ONNX Runtime shared library for one variant and architecture.
set -euo pipefail

ORT_VERSION=1.31.0
ORT_COMMIT=402979ac1bef89dcd55add41eaa7277c407599eb
ORT_REPO=https://github.com/microsoft/onnxruntime.git

# SDK versions the variant images install. The flags below point at those trees.
CUDA12_VERSION=12.8.1
CUDNN12_VERSION=9.8.0.87
CUDA13_VERSION=13.0.2
CUDNN13_VERSION=9.14.0.64
TENSORRT12_VERSION=10.14.1.48
TENSORRT12_CUDA_VERSION=12.9.1
TENSORRT12_CUDNN_VERSION=9.10.2.21
TENSORRT13_AMD64_VERSION=10.14.1.48
TENSORRT13_AMD64_CUDA_VERSION=13.0.2
TENSORRT13_AMD64_CUDNN_VERSION=9.14.0.64
TENSORRT13_ARM64_VERSION=10.15.1.29
TENSORRT13_ARM64_CUDA_VERSION=13.1.2
TENSORRT13_ARM64_CUDNN_VERSION=9.17.1.4
OPENVINO_VERSION=2026.2.1
QNN_VERSION=2.51.0.260929
ROCM_VERSION=7.1

# CUDA is /usr/local/cuda. These UBI images install cuDNN under /usr
# (headers in include, libraries in lib64), not inside the CUDA tree.
CUDA_HOME=/usr/local/cuda
CUDNN_HOME=/usr
QNN_HOME=/opt/qnn-${QNN_VERSION}
OPENVINO_DIR=/opt/intel/openvino_${OPENVINO_VERSION}/runtime/cmake
MIGRAPHX_HOME=/opt/rocm
# The CPU image installs the pinned riscv64-unknown-linux-gnu toolchain at the prefix it was built with.
RISCV_TOOLCHAIN_ROOT=/opt/riscv

PREFIX="${ORT_PREFIX:-/opt/onnxruntime}"
SRC="${ORT_SRC:-/tmp/onnxruntime-src}"
BUILD_DIR="${ORT_BUILD_DIR:-/tmp/onnxruntime-build}"
STAGE="${ORT_STAGE:-/tmp/onnxruntime-stage}"

usage() {
  echo "usage: build-onnxruntime.sh [--print-flags] <variant> <arch>" >&2
}

python_bin() {
  local candidate ver major minor
  for candidate in python3.12 python3.11 python3; do
    if command -v "$candidate" >/dev/null 2>&1; then
      ver="$("$candidate" -c 'import sys; print("%d.%d" % sys.version_info[:2])')"
      major="${ver%%.*}"
      minor="${ver#*.}"
      if [[ "$major" -gt 3 || ("$major" -eq 3 && "$minor" -ge 10) ]]; then
        echo "$candidate"
        return 0
      fi
    fi
  done
  echo "ONNX Runtime's build needs Python 3.10 or newer" >&2
  return 1
}

host_arch() {
  case "$(uname -m)" in
    x86_64 | amd64) echo amd64 ;;
    aarch64 | arm64) echo arm64 ;;
    riscv64) echo riscv64 ;;
    *) echo unknown ;;
  esac
}

matrix_ok() {
  case "$1:$2" in
    cpu:amd64 | cpu:arm64 | cpu:riscv64) return 0 ;;
    cuda12:amd64 | cuda12:arm64) return 0 ;;
    cuda13:amd64 | cuda13:arm64) return 0 ;;
    tensorrt12:amd64) return 0 ;;
    tensorrt13:amd64 | tensorrt13:arm64) return 0 ;;
    qnn:arm64) return 0 ;;
    openvino:amd64) return 0 ;;
    migraphx:amd64) return 0 ;;
    *) return 1 ;;
  esac
}

parallel_args() {
  local variant="$1"
  local cap="" mem_kb jobs cpus
  # CUDA and TensorRT compile one file at a time. Four of those files fit
  # on the build machine; more of them run it out of memory.
  case "$variant" in
    cuda12 | cuda13 | tensorrt12 | tensorrt13) cap=4 ;;
  esac
  if [[ -r /proc/meminfo ]]; then
    mem_kb="$(awk '/MemTotal/ {print $2}' /proc/meminfo)"
    cpus="$(getconf _NPROCESSORS_ONLN)"
    # A compile of this tree uses a few gigabytes per job.
    jobs=$((mem_kb / 3500000))
    if [[ "$jobs" -lt 1 ]]; then
      jobs=1
    fi
    if [[ "$jobs" -gt "$cpus" ]]; then
      jobs="$cpus"
    fi
    if [[ -n "$cap" && "$jobs" -gt "$cap" ]]; then
      jobs="$cap"
    fi
    printf '%s\n' --parallel "$jobs"
  elif [[ -n "$cap" ]]; then
    printf '%s\n' --parallel "$cap"
  else
    printf '%s\n' --parallel
  fi
}

tensorrt_home() {
  case "$1:$2" in
    tensorrt12:amd64) echo "/usr/local/tensorrt-${TENSORRT12_VERSION}" ;;
    tensorrt13:amd64) echo "/usr/local/tensorrt-${TENSORRT13_AMD64_VERSION}" ;;
    tensorrt13:arm64) echo "/usr/local/tensorrt-${TENSORRT13_ARM64_VERSION}" ;;
    *) return 1 ;;
  esac
}

# Fill FLAGS with the build.py arguments for this pair.
# src_dir is empty when only printing. A compile passes the cloned source tree
# so the toolchain file is an absolute path.
set_flags() {
  local variant="$1" arch="$2" src_dir="${3:-}"
  local riscv_toolchain cuda cudnn
  FLAGS=()
  if [[ -n "$src_dir" ]]; then
    riscv_toolchain="${src_dir}/cmake/riscv64.toolchain.cmake"
  else
    riscv_toolchain="cmake/riscv64.toolchain.cmake"
  fi

  FLAGS+=(
    --allow_running_as_root
    --config Release
    --update
    --build
    --build_shared_lib
    --skip_tests
    --compile_no_warning_as_error
    --cmake_extra_defines onnxruntime_BUILD_UNIT_TESTS=OFF CMAKE_INSTALL_LIBDIR=lib
  )
  while IFS= read -r arg; do
    FLAGS+=("$arg")
  done < <(parallel_args "$variant")

  case "$arch" in
    amd64 | riscv64) FLAGS+=(--no_kleidiai) ;;
    arm64) ;;
  esac

  case "$arch" in
    riscv64)
      FLAGS+=(
        --rv64
        --riscv_toolchain_root "$RISCV_TOOLCHAIN_ROOT"
        --cmake_extra_defines "CMAKE_TOOLCHAIN_FILE=${riscv_toolchain}"
      )
      ;;
  esac

  case "$variant" in
    cpu) ;;
    cuda12)
      FLAGS+=(--use_cuda --nvcc_threads 1 --cuda_version "$CUDA12_VERSION" --cuda_home "$CUDA_HOME" --cudnn_home "$CUDNN_HOME")
      ;;
    cuda13)
      FLAGS+=(--use_cuda --nvcc_threads 1 --cuda_version "$CUDA13_VERSION" --cuda_home "$CUDA_HOME" --cudnn_home "$CUDNN_HOME")
      ;;
    tensorrt12)
      FLAGS+=(
        --use_cuda --nvcc_threads 1 --cuda_version "$TENSORRT12_CUDA_VERSION" --cuda_home "$CUDA_HOME" --cudnn_home "$CUDNN_HOME"
        --use_tensorrt --tensorrt_home "$(tensorrt_home tensorrt12 amd64)"
      )
      ;;
    tensorrt13)
      if [[ "$arch" == arm64 ]]; then
        cuda="$TENSORRT13_ARM64_CUDA_VERSION"
      else
        cuda="$TENSORRT13_AMD64_CUDA_VERSION"
      fi
      FLAGS+=(
        --use_cuda --nvcc_threads 1 --cuda_version "$cuda" --cuda_home "$CUDA_HOME" --cudnn_home "$CUDNN_HOME"
        --use_tensorrt --tensorrt_home "$(tensorrt_home tensorrt13 "$arch")"
      )
      ;;
    qnn)
      FLAGS+=(--use_qnn --qnn_home "$QNN_HOME")
      ;;
    openvino)
      FLAGS+=(--use_openvino CPU --cmake_extra_defines "OpenVINO_DIR=${OPENVINO_DIR}")
      ;;
    migraphx)
      FLAGS+=(--use_migraphx --migraphx_home "$MIGRAPHX_HOME")
      ;;
  esac

  # The version is the cuDNN release in the image named above.
  case "$variant" in
    cuda12) cudnn="$CUDNN12_VERSION" ;;
    cuda13) cudnn="$CUDNN13_VERSION" ;;
    tensorrt12) cudnn="$TENSORRT12_CUDNN_VERSION" ;;
    tensorrt13)
      if [[ "$arch" == arm64 ]]; then
        cudnn="$TENSORRT13_ARM64_CUDNN_VERSION"
      else
        cudnn="$TENSORRT13_AMD64_CUDNN_VERSION"
      fi
      ;;
    *) cudnn="" ;;
  esac
  if [[ -n "$cudnn" && -z "$CUDNN_HOME" ]]; then
    echo "cuDNN ${cudnn} has no home" >&2
    exit 1
  fi
  if [[ "$variant" == migraphx && -z "$ROCM_VERSION" ]]; then
    echo "ROCm version is unset" >&2
    exit 1
  fi
}

enable_compiler() {
  if [[ -f /opt/rh/gcc-toolset-14/enable ]]; then
    set +u
    # shellcheck disable=SC1091
    source /opt/rh/gcc-toolset-14/enable
    set -u
  fi
}

prepare_cross_toolchain() {
  local arch="$1"
  if [[ "$arch" != riscv64 ]]; then
    return 0
  fi
  if [[ ! -x "${RISCV_TOOLCHAIN_ROOT}/bin/riscv64-unknown-linux-gnu-gcc" || ! -d "${RISCV_TOOLCHAIN_ROOT}/sysroot" ]]; then
    echo "riscv64 toolchain is not installed at ${RISCV_TOOLCHAIN_ROOT}" >&2
    exit 1
  fi
}

fetch_source() {
  if [[ ! -f "${SRC}/VERSION_NUMBER" ]]; then
    rm -rf "$SRC"
    git -c advice.detachedHead=false clone --depth 1 --branch "v${ORT_VERSION}" "$ORT_REPO" "$SRC"
  fi
  local got
  got="$(git -C "$SRC" rev-parse HEAD)"
  if [[ "$got" != "$ORT_COMMIT" ]]; then
    echo "ONNX Runtime ${ORT_VERSION} resolved to ${got}, expected ${ORT_COMMIT}" >&2
    exit 1
  fi
  local noted
  noted="$(tr -d '[:space:]' < "${SRC}/VERSION_NUMBER")"
  if [[ "$noted" != "$ORT_VERSION" ]]; then
    echo "source version is ${noted}, expected ${ORT_VERSION}" >&2
    exit 1
  fi
}

install_prefix() {
  cmake --install "${BUILD_DIR}/Release" --prefix "$STAGE"
  local libdir="$STAGE/lib"
  if [[ ! -d "$libdir" && -d "$STAGE/lib64" ]]; then
    libdir="$STAGE/lib64"
  fi
  mkdir -p "$PREFIX/lib" "$PREFIX/include" "$PREFIX/licenses"
  local copied=0 f base
  shopt -s nullglob
  for f in "$libdir"/*; do
    base="$(basename "$f")"
    case "$base" in
      *.so | *.so.*)
        cp -a "$f" "$PREFIX/lib/"
        copied=1
        ;;
    esac
  done
  shopt -u nullglob
  if [[ "$copied" -ne 1 ]]; then
    echo "install produced no shared libraries in ${libdir}" >&2
    exit 1
  fi
  cp -a "$STAGE/include/." "$PREFIX/include/"
  if [[ ! -f "$PREFIX/include/onnxruntime/onnxruntime_c_api.h" ]]; then
    echo "install produced no C API header" >&2
    exit 1
  fi
  cp -a "$SRC/LICENSE" "$PREFIX/licenses/LICENSE"
  cp -a "$SRC/ThirdPartyNotices.txt" "$PREFIX/licenses/ThirdPartyNotices.txt"
  printf '%s\n' "$ORT_COMMIT" > "$PREFIX/COMMIT"
  rm -rf "$SRC" "$BUILD_DIR" "$STAGE"
}

check_version() {
  local arch="$1" so="$PREFIX/lib/libonnxruntime.so.${ORT_VERSION}"
  if [[ ! -f "$so" ]]; then
    echo "missing ${so}" >&2
    exit 1
  fi
  if [[ "$(host_arch)" == "$arch" ]]; then
    cat > /tmp/ort-version.c <<'EOF'
#include <stdio.h>
#include <string.h>
#include "onnxruntime_c_api.h"
int main(void) {
  const char *v = OrtGetApiBase()->GetVersionString();
  if (v == NULL || strcmp(v, "1.31.0") != 0) {
    fprintf(stderr, "runtime version %s\n", v == NULL ? "(null)" : v);
    return 1;
  }
  fputs(v, stdout);
  return 0;
}
EOF
    gcc -O2 -I"$PREFIX/include/onnxruntime" /tmp/ort-version.c \
      -L"$PREFIX/lib" -lonnxruntime -Wl,-rpath,"$PREFIX/lib" -o /tmp/ort-version
    local got
    got="$(/tmp/ort-version)"
    if [[ "$got" != "$ORT_VERSION" ]]; then
      echo "runtime version ${got}" >&2
      exit 1
    fi
    rm -f /tmp/ort-version /tmp/ort-version.c
  else
    # The compiler stores the version literal as the tail of a longer string.
    # Read every line. With pipefail, grep -q stops early and strings
    # exits on SIGPIPE, which looks like a missing version.
    local version_re="${ORT_VERSION//./\\.}"
    if ! strings -a "$so" | grep -E "(^|[^0-9])${version_re}$" >/dev/null; then
      echo "runtime library does not contain version ${ORT_VERSION}" >&2
      exit 1
    fi
  fi
  printf '%s\n' "$ORT_VERSION" > "$PREFIX/VERSION"
}

check_abi() {
  local arch="$1" readelf objdump glibc_ceiling cxx_ceiling
  readelf=readelf
  objdump=objdump
  glibc_ceiling=2.34
  cxx_ceiling=3.4.29
  if [[ "$arch" == riscv64 ]]; then
    readelf="${RISCV_TOOLCHAIN_ROOT}/bin/riscv64-unknown-linux-gnu-readelf"
    objdump="${RISCV_TOOLCHAIN_ROOT}/bin/riscv64-unknown-linux-gnu-objdump"
    glibc_ceiling=2.39
    cxx_ceiling=3.4.31
  fi
  "$PYTHON" - "$PREFIX/lib" "$readelf" "$objdump" "$glibc_ceiling" "$cxx_ceiling" <<'PY'
import re
import subprocess
import sys
from pathlib import Path

root = Path(sys.argv[1])
readelf = sys.argv[2]
objdump = sys.argv[3]
glibc_ceiling = tuple(int(part) for part in sys.argv[4].split("."))
cxx_ceiling = tuple(int(part) for part in sys.argv[5].split("."))
glibc_re = re.compile(r"GLIBC_(\d+)\.(\d+)")
cxx_re = re.compile(r"GLIBCXX_(\d+)\.(\d+)\.(\d+)")
libs = [p for p in root.iterdir() if p.is_file() and ".so" in p.name]
if not libs:
    print(f"no shared libraries in {root}", file=sys.stderr)
    sys.exit(1)
failed = False
for lib in sorted(libs):
    dyn = subprocess.check_output([readelf, "-d", str(lib)], text=True, errors="replace")
    for line in dyn.splitlines():
        if ("RPATH" in line or "RUNPATH" in line) and "/opt/rh/" in line:
            print(f"{lib}: runtime path points at the compiler toolset: {line.strip()}", file=sys.stderr)
            failed = True
    out = subprocess.check_output([objdump, "-T", str(lib)], text=True, errors="replace")
    newer = []
    for line in out.splitlines():
        if "*UND*" not in line:
            continue
        for match in glibc_re.finditer(line):
            if (int(match.group(1)), int(match.group(2))) > glibc_ceiling:
                newer.append(match.group(0))
        for match in cxx_re.finditer(line):
            if tuple(int(part) for part in match.groups()) > cxx_ceiling:
                newer.append(match.group(0))
    if newer:
        names = ", ".join(sorted(set(newer)))
        print(
            f"{lib}: needs a symbol newer than glibc {sys.argv[4]} or GLIBCXX_{sys.argv[5]}: {names}",
            file=sys.stderr,
        )
        failed = True
sys.exit(1 if failed else 0)
PY
}

if [[ $# -lt 1 ]]; then
  usage
  exit 2
fi

print_only=0
if [[ "$1" == "--print-flags" ]]; then
  print_only=1
  shift
fi
if [[ $# -ne 2 ]]; then
  usage
  exit 2
fi

variant="$1"
arch="$2"
if ! matrix_ok "$variant" "$arch"; then
  echo "onnx runtime build: ${variant} is not built for ${arch}" >&2
  exit 1
fi

set_flags "$variant" "$arch"
if [[ "$print_only" -eq 1 ]]; then
  printf '%s\n' "${FLAGS[@]}"
  exit 0
fi

PYTHON="$(python_bin)"
enable_compiler
prepare_cross_toolchain "$arch"
fetch_source
set_flags "$variant" "$arch" "$SRC"
export MALLOC_ARENA_MAX="${MALLOC_ARENA_MAX:-2}"
"$PYTHON" "${SRC}/tools/ci_build/build.py" --build_dir "$BUILD_DIR" "${FLAGS[@]}"
install_prefix
check_version "$arch"
if [[ "$arch" == amd64 || "$arch" == arm64 || "$arch" == riscv64 ]]; then
  check_abi "$arch"
fi
