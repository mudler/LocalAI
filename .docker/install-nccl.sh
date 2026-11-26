#!/usr/bin/env bash
# Installs NCCL (runtime library and headers) in the llama-cpp CUDA builders.
#
# ggml's CUDA backend links NCCL when CMake finds it (GGML_CUDA_NCCL defaults to
# ON) and then uses it for the cross-GPU reductions of `split_mode: tensor`.
# Without it, tensor split falls back to ggml's own all-reduce, which upstream
# documents as slower. NCCL is not part of the CUDA toolkit packages that
# install-base-deps.sh installs, so the llama-cpp image adds it here.
#
# This is a separate script, not a section of install-base-deps.sh, on purpose:
#   - the other builders that share install-base-deps.sh (ik-llama-cpp,
#     turboquant, bonsai, ...) would find NCCL too and each grow by the size of
#     the library;
#   - the prebuilt CI bases (base-grpc-*) do not have to be rebuilt.
# Both builder stages of backend/Dockerfile.llama-cpp run it before the source
# copy, so BuildKit caches the layer.
#
# Inputs (env): BUILD_TYPE, TARGETARCH, CUDA_MAJOR_VERSION, CUDA_MINOR_VERSION,
#   NCCL_VERSION (optional: an exact apt version, e.g. 2.26.2-1+cuda12.8).
#
# Sourcing the script defines the functions only, for scripts/build/install-nccl_test.sh.

# nccl_wanted BUILD_TYPE TARGETARCH CUDA_MAJOR_VERSION
# Succeeds when the image should ship NCCL.
#   - cublas on amd64: CUDA 12 and CUDA 13 images.
#   - cublas on arm64 with CUDA 13: the SBSA image (Grace servers, DGX Spark).
#   - not arm64 with CUDA 12: that image is built on JetPack for Jetson boards,
#     which have one integrated GPU, and NVIDIA ships no NCCL for them.
#   - not l4t, hipblas, vulkan, sycl or CPU builds.
nccl_wanted() {
    local build_type="$1" arch="$2" cuda_major="$3"
    [ "$build_type" = "cublas" ] || return 1
    case "$arch" in
        amd64) return 0 ;;
        arm64) [ "${cuda_major:-0}" -ge 13 ] 2>/dev/null && return 0 ;;
    esac
    return 1
}

# nccl_pick_version SUFFIX
# Reads `apt-cache madison libnccl2` output on stdin and prints the newest
# version built for exactly this CUDA release (SUFFIX is e.g. "+cuda12.8").
# The exact match keeps the driver requirement of NCCL the same as the one of
# the CUDA libraries the image already bundles.
nccl_pick_version() {
    local suffix="$1"
    awk -F'|' -v s="$suffix" '{
        v = $2; gsub(/^[ \t]+|[ \t]+$/, "", v)
        if (length(v) > length(s) && substr(v, length(v) - length(s) + 1) == s) print v
    }' | sort -V | tail -n 1
}

nccl_apt() {
    if [ -x /usr/local/sbin/apt-mirror ]; then
        sh /usr/local/sbin/apt-mirror apt-get "$@"
    else
        apt-get "$@"
    fi
}

install_nccl() {
    if ! nccl_wanted "${BUILD_TYPE:-}" "${TARGETARCH:-}" "${CUDA_MAJOR_VERSION:-}"; then
        echo "NCCL: not used for BUILD_TYPE=${BUILD_TYPE:-} TARGETARCH=${TARGETARCH:-} CUDA_MAJOR_VERSION=${CUDA_MAJOR_VERSION:-}"
        return 0
    fi

    : "${CUDA_MAJOR_VERSION:?CUDA_MAJOR_VERSION is required for a cublas build}"
    : "${CUDA_MINOR_VERSION:?CUDA_MINOR_VERSION is required for a cublas build}"

    nccl_apt update
    local version="${NCCL_VERSION:-}"
    if [ -z "$version" ]; then
        version=$(apt-cache madison libnccl2 | nccl_pick_version "+cuda${CUDA_MAJOR_VERSION}.${CUDA_MINOR_VERSION}")
    fi
    if [ -z "$version" ]; then
        # The CUDA apt repository is set up by install-base-deps.sh. A missing
        # package means that repository changed, so stop instead of shipping an
        # image without NCCL.
        echo "NCCL: no libnccl2 package for CUDA ${CUDA_MAJOR_VERSION}.${CUDA_MINOR_VERSION} in the configured apt sources" >&2
        return 1
    fi

    echo "NCCL: installing libnccl2 and libnccl-dev ${version}"
    nccl_apt install -y --no-install-recommends "libnccl2=${version}" "libnccl-dev=${version}"
    apt-get clean
    rm -rf /var/lib/apt/lists/*
}

if [ "${BASH_SOURCE[0]}" = "$0" ]; then
    set -euo pipefail
    install_nccl
fi
