#!/bin/sh
# Compatibility links for the ROCm install of a BUILD_TYPE=hipblas build.
#
# Used by the main Dockerfile, backend/Dockerfile.python, backend/Dockerfile.golang,
# backend/Dockerfile.audio-cpp and .docker/install-base-deps.sh, each via
#   RUN --mount=type=bind,source=.docker/rocm-compat-links.sh,target=/usr/local/sbin/rocm-compat-links ...
# Tested by scripts/build/rocm-compat-links_test.sh (make test-build-scripts).
#
# ROCm 7.x installs into /opt/rocm-<version>/ with the compiler in llvm/ and
# a lib/llvm -> ../llvm link, and /opt/rocm points at that directory.
# ROCm 10.x (TheRock packages from stable.repo.amd.com, as in the
# rocm/dev-ubuntu-24.04:10.x images) installs into /opt/rocm/core-<major.minor>/
# instead. The compiler lives in lib/llvm/ there, and /opt/rocm only gets
# bin, lib, include, libexec and share links. Two things the build relies on
# therefore disappear:
#
#   1. $ROCM_ROOT/llvm/bin/clang++, which the hipblas branch of every ggml
#      backend Makefile uses as CXX (backend/cpp/llama-cpp/Makefile and
#      siblings). We add $ROCM_ROOT/llvm -> $ROCM_ROOT/lib/llvm when it is
#      missing, so the Makefiles work unchanged on both layouts.
#   2. /opt/rocm-*/lib/llvm/lib/libomp.so, which the Dockerfiles linked to
#      /usr/lib/libomp.so with a glob. On 10.x the glob matches nothing and
#      `ln -s` silently creates a dangling link. We resolve the path through
#      $ROCM_ROOT/lib/llvm first, which exists on both layouts.
#
# Safe to run more than once, and a no-op when ROCm is not installed (for
# example BUILD_TYPE=hipblas with SKIP_DRIVERS=true on a plain Ubuntu base).
#
# Inputs (env, for tests; the defaults are the real locations):
#   ROCM_ROOT     ROCm root, default /opt/rocm
#   OMP_LINK_DIR  directory that receives libomp.so, default /usr/lib

set -eu

ROCM_ROOT="${ROCM_ROOT:-/opt/rocm}"
OMP_LINK_DIR="${OMP_LINK_DIR:-/usr/lib}"

# 1. $ROCM_ROOT/llvm
if [ ! -e "$ROCM_ROOT/llvm" ] && [ ! -L "$ROCM_ROOT/llvm" ] && [ -d "$ROCM_ROOT/lib/llvm/bin" ]; then
    ln -s "$ROCM_ROOT/lib/llvm" "$ROCM_ROOT/llvm"
    echo "rocm-compat-links: $ROCM_ROOT/llvm -> $ROCM_ROOT/lib/llvm"
fi

# 2. libomp.so
if [ -e "$OMP_LINK_DIR/libomp.so" ] || [ -L "$OMP_LINK_DIR/libomp.so" ]; then
    echo "rocm-compat-links: $OMP_LINK_DIR/libomp.so already present, left as is"
else
    omp=""
    for candidate in "$ROCM_ROOT/lib/llvm/lib/libomp.so" "$ROCM_ROOT/llvm/lib/libomp.so" "$ROCM_ROOT"-*/lib/llvm/lib/libomp.so; do
        if [ -e "$candidate" ]; then
            omp="$candidate"
            break
        fi
    done
    if [ -n "$omp" ]; then
        mkdir -p "$OMP_LINK_DIR"
        ln -s "$omp" "$OMP_LINK_DIR/libomp.so"
        echo "rocm-compat-links: $OMP_LINK_DIR/libomp.so -> $omp"
    else
        echo "rocm-compat-links: WARNING: no libomp.so found under $ROCM_ROOT, $OMP_LINK_DIR/libomp.so not created" >&2
    fi
fi
