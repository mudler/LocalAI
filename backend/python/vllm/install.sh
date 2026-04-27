#!/bin/bash
set -e

EXTRA_PIP_INSTALL_FLAGS="--no-build-isolation"

# Avoid to overcommit the CPU during build
# https://github.com/vllm-project/vllm/issues/20079
# https://docs.vllm.ai/en/v0.8.3/serving/env_vars.html
# https://docs.redhat.com/it/documentation/red_hat_ai_inference_server/3.0/html/vllm_server_arguments/environment_variables-server-arguments
export NVCC_THREADS=2
export MAX_JOBS=1

backend_dir=$(dirname $0)

if [ -d $backend_dir/common ]; then
    source $backend_dir/common/libbackend.sh
else
    source $backend_dir/../common/libbackend.sh
fi

# This is here because the Intel pip index is broken and returns 200 status codes for every package name, it just doesn't return any package links.
# This makes uv think that the package exists in the Intel pip index, and by default it stops looking at other pip indexes once it finds a match.
# We need uv to continue falling through to the pypi default index to find optimum[openvino] in the pypi index
# the --upgrade actually allows us to *downgrade* torch to the version provided in the Intel pip index
if [ "x${BUILD_PROFILE}" == "xintel" ]; then
    EXTRA_PIP_INSTALL_FLAGS+=" --upgrade --index-strategy=unsafe-first-match"
fi

# CPU builds need unsafe-best-match to pull torch==2.10.0+cpu from the
# pytorch test channel while still resolving transformers/vllm from pypi.
if [ "x${BUILD_PROFILE}" == "xcpu" ]; then
    EXTRA_PIP_INSTALL_FLAGS+=" --index-strategy=unsafe-best-match"
fi

# cublas13 pulls the vLLM wheel from a per-tag cu130 index (PyPI's vllm wheel
# is built against CUDA 12 and won't load on cu130). uv's default per-package
# first-match strategy would still pick the PyPI wheel, so allow it to consult
# every configured index when resolving.
if [ "x${BUILD_PROFILE}" == "xcublas13" ]; then
    EXTRA_PIP_INSTALL_FLAGS+=" --index-strategy=unsafe-best-match"
fi

# JetPack 7 / L4T arm64 wheels (torch, vllm, flash-attn) live on
# pypi.jetson-ai-lab.io and are built for cp312, so bump the venv Python
# accordingly. JetPack 6 keeps cp310 + USE_PIP=true. unsafe-best-match
# is required because the jetson-ai-lab index lists transitive deps at
# limited versions — without it uv pins to the first matching index and
# fails to resolve a compatible wheel from PyPI.
if [ "x${BUILD_PROFILE}" == "xl4t12" ]; then
    USE_PIP=true
fi
if [ "x${BUILD_PROFILE}" == "xl4t13" ]; then
    PYTHON_VERSION="3.12"
    PYTHON_PATCH="12"
    PY_STANDALONE_TAG="20251120"
    EXTRA_PIP_INSTALL_FLAGS+=" --index-strategy=unsafe-best-match"
fi

# FROM_SOURCE=true on a CPU build skips the prebuilt vllm wheel in
# requirements-cpu-after.txt and compiles vllm locally against the host's
# actual CPU. Not used by default because it takes ~30-40 minutes, but
# kept here for hosts where the prebuilt wheel SIGILLs (CPU without the
# required SIMD baseline, e.g. AVX-512 VNNI/BF16). Default CI uses a
# bigger-runner with compatible hardware instead.
if [ "x${BUILD_TYPE}" == "x" ] && [ "x${FROM_SOURCE:-}" == "xtrue" ]; then
    # Temporarily hide the prebuilt wheel so installRequirements doesn't
    # pull it — the rest of the requirements files (base deps, torch,
    # transformers) are still installed normally.
    _cpu_after="${backend_dir}/requirements-cpu-after.txt"
    _cpu_after_bak=""
    if [ -f "${_cpu_after}" ]; then
        _cpu_after_bak="${_cpu_after}.from-source.bak"
        mv "${_cpu_after}" "${_cpu_after_bak}"
    fi
    installRequirements
    if [ -n "${_cpu_after_bak}" ]; then
        mv "${_cpu_after_bak}" "${_cpu_after}"
    fi

    # Build vllm from source against the installed torch.
    # https://docs.vllm.ai/en/latest/getting_started/installation/cpu/
    _vllm_src=$(mktemp -d)
    trap 'rm -rf "${_vllm_src}"' EXIT
    git clone --depth 1 https://github.com/vllm-project/vllm "${_vllm_src}/vllm"
    pushd "${_vllm_src}/vllm"
        uv pip install ${EXTRA_PIP_INSTALL_FLAGS:-} wheel packaging ninja "setuptools>=49.4.0" numpy typing-extensions pillow setuptools-scm
        # Respect pre-installed torch version — skip vllm's own requirements-build.txt torch pin.
        VLLM_TARGET_DEVICE=cpu uv pip install ${EXTRA_PIP_INSTALL_FLAGS:-} --no-deps .
    popd
else
    installRequirements
fi
