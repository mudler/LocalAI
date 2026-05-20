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

# cublas12 needs a cu128 torch index (see requirements-cublas12.txt) — without
# unsafe-best-match uv falls through to default PyPI's cu130 torch wheel and
# the resulting sgl-kernel can't load on our cu12 host libs.
if [ "x${BUILD_PROFILE}" == "xcublas12" ]; then
    EXTRA_PIP_INSTALL_FLAGS+=" --index-strategy=unsafe-best-match"
fi

# sglang 0.5.11 (Gemma 4 support) declares flash-attn-4 as a hard dep, but
# upstream only publishes pre-release wheels (4.0.0b*). uv rejects
# pre-releases by default — opt in for sglang specifically. Drop this once
# flash-attn-4 4.0 stable lands.
EXTRA_PIP_INSTALL_FLAGS+=" --prerelease=allow"

# JetPack 7 / L4T arm64 wheels are built for cp312 and shipped via
# pypi.jetson-ai-lab.io. Bump the venv Python so the prebuilt sglang
# wheel resolves cleanly. The actual install on l4t13 goes through
# pyproject.toml (see the elif branch below) so [tool.uv.sources] can
# pin only torch/torchvision/torchaudio/sglang to the jetson-ai-lab
# index — leaving PyPI as the path for transitive deps like
# markdown-it-py / anthropic / propcache that the L4T mirror's proxy
# 503s on. No --index-strategy flag here: the explicit index keeps the
# scoping clean.
if [ "x${BUILD_PROFILE}" == "xl4t13" ]; then
    PYTHON_VERSION="3.12"
    PYTHON_PATCH="12"
    PY_STANDALONE_TAG="20251120"
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
# L4T arm64 (JetPack 7): drive the install through pyproject.toml so that
# [tool.uv.sources] can pin torch/torchvision/torchaudio/sglang to the
# jetson-ai-lab index, while everything else (transitive deps and
# PyPI-resolvable packages like transformers / accelerate) comes from
# PyPI. Bypasses installRequirements because uv pip install -r
# requirements.txt does not honor sources — see
# backend/python/sglang/pyproject.toml for the rationale. Mirrors the
# equivalent path in backend/python/vllm/install.sh.
elif [ "x${BUILD_PROFILE}" == "xl4t13" ]; then
    ensureVenv
    if [ "x${PORTABLE_PYTHON}" == "xtrue" ]; then
        export C_INCLUDE_PATH="${C_INCLUDE_PATH:-}:$(_portable_dir)/include/python${PYTHON_VERSION}"
    fi
    pushd "${backend_dir}"
        # Build deps first (matches installRequirements' requirements-install.txt
        # pass — sglang/sgl-kernel sdists need packaging/setuptools-scm in the
        # venv before they can build under --no-build-isolation).
        uv pip install ${EXTRA_PIP_INSTALL_FLAGS:-} -r requirements-install.txt
        uv pip install ${EXTRA_PIP_INSTALL_FLAGS:-} --requirement pyproject.toml
    popd
    runProtogen
else
    installRequirements
fi
