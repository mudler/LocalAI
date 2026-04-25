#!/bin/bash
set -e

EXTRA_PIP_INSTALL_FLAGS="--no-build-isolation"

# Avoid overcommitting the CPU during builds that compile native code.
export NVCC_THREADS=2
export MAX_JOBS=1

backend_dir=$(dirname $0)

if [ -d $backend_dir/common ]; then
    source $backend_dir/common/libbackend.sh
else
    source $backend_dir/../common/libbackend.sh
fi

if [ "x${BUILD_PROFILE}" == "xintel" ]; then
    EXTRA_PIP_INSTALL_FLAGS+=" --upgrade --index-strategy=unsafe-first-match"
fi

if [ "x${BUILD_PROFILE}" == "xcpu" ]; then
    EXTRA_PIP_INSTALL_FLAGS+=" --index-strategy=unsafe-best-match"
fi

# JetPack 7 / L4T arm64 wheels are built for cp312 and shipped via
# pypi.jetson-ai-lab.io. Bump the venv Python so the prebuilt sglang
# wheel resolves cleanly. unsafe-best-match is required because the
# jetson-ai-lab index lists transitive deps (e.g. decord) at older
# versions only — without it uv refuses to fall through to PyPI for a
# compatible wheel and resolution fails.
if [ "x${BUILD_PROFILE}" == "xl4t13" ]; then
    PYTHON_VERSION="3.12"
    PYTHON_PATCH="12"
    PY_STANDALONE_TAG="20251120"
    EXTRA_PIP_INSTALL_FLAGS+=" --index-strategy=unsafe-best-match"
fi

# sglang's CPU path has no prebuilt wheel on PyPI — upstream publishes
# a separate pyproject_cpu.toml that must be swapped in before `pip install`.
# Reference: docker/xeon.Dockerfile in the sglang upstream repo.
#
# When BUILD_TYPE is empty (CPU profile) or FROM_SOURCE=true is forced,
# install torch/transformers/etc from requirements-cpu.txt, then clone
# sglang and install its python/ and sgl-kernel/ packages from source
# using the CPU pyproject.
if [ "x${BUILD_TYPE}" == "x" ] || [ "x${FROM_SOURCE:-}" == "xtrue" ]; then
    # sgl-kernel's CPU build links against libnuma and libtbb. Install
    # them here (Docker builder stage) before running the source build.
    # Harmless no-op on runs outside the docker build since installRequirements
    # below still needs them only if we reach the source build branch.
    if command -v apt-get >/dev/null 2>&1 && [ "$(id -u)" = "0" ]; then
        apt-get update
        DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
            libnuma-dev numactl libtbb-dev libgomp1 libomp-dev google-perftools \
            build-essential cmake ninja-build
    fi

    installRequirements

    # sgl-kernel's pyproject_cpu.toml uses scikit-build-core as its build
    # backend. With --no-build-isolation, that (and ninja/cmake) must be
    # present in the venv before we build from source.
    uv pip install --no-build-isolation "scikit-build-core>=0.10" ninja cmake

    # sgl-kernel's CPU shm.cpp uses __m512 AVX-512 intrinsics unconditionally.
    # csrc/cpu/CMakeLists.txt hard-codes add_compile_options(-march=native),
    # which on runners without AVX-512 in /proc/cpuinfo fails with
    # "__m512 return without 'avx512f' enabled changes the ABI".
    # CXXFLAGS alone is insufficient because CMake's add_compile_options()
    # appends -march=native *after* CXXFLAGS, overriding it.
    # We therefore patch the CMakeLists.txt to replace -march=native with
    # -march=sapphirerapids so the flag is consistent throughout the build.
    # The resulting binary still requires an AVX-512 capable CPU at runtime,
    # same constraint sglang upstream documents in docker/xeon.Dockerfile.

    _sgl_src=$(mktemp -d)
    trap 'rm -rf "${_sgl_src}"' EXIT
    git clone --depth 1 https://github.com/sgl-project/sglang "${_sgl_src}/sglang"

    # Patch -march=native → -march=sapphirerapids in the CPU kernel CMakeLists
    sed -i 's/-march=native/-march=sapphirerapids/g' \
        "${_sgl_src}/sglang/sgl-kernel/csrc/cpu/CMakeLists.txt"

    pushd "${_sgl_src}/sglang/sgl-kernel"
        if [ -f pyproject_cpu.toml ]; then
            cp pyproject_cpu.toml pyproject.toml
        fi
        uv pip install ${EXTRA_PIP_INSTALL_FLAGS:-} .
    popd

    pushd "${_sgl_src}/sglang/python"
        if [ -f pyproject_cpu.toml ]; then
            cp pyproject_cpu.toml pyproject.toml
        fi
        uv pip install ${EXTRA_PIP_INSTALL_FLAGS:-} .
    popd
else
    installRequirements
fi
