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

# Intel XPU: torch==2.11.0+xpu lives on the PyTorch XPU index, transitive
# deps on PyPI — unsafe-best-match lets uv mix both. vllm-xpu-kernels only
# ships a python3.12 wheel per upstream docs, so bump the portable Python
# before installRequirements (matches the l4t13 pattern below).
# https://github.com/vllm-project/vllm/blob/main/docs/getting_started/installation/gpu.xpu.inc.md
if [ "x${BUILD_PROFILE}" == "xintel" ]; then
    PYTHON_VERSION="3.12"
    PYTHON_PATCH="11"
    EXTRA_PIP_INSTALL_FLAGS+=" --index-strategy=unsafe-best-match"
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
# accordingly. JetPack 6 keeps cp310 + USE_PIP=true.
#
# l4t13 uses pyproject.toml (see the elif branch below) to pin only the
# L4T-specific wheels to the jetson-ai-lab index via [tool.uv.sources].
# That keeps PyPI as the resolution path for transitive deps like
# anthropic/openai/propcache, which the L4T mirror's proxy 503s on.
if [ "x${BUILD_PROFILE}" == "xl4t12" ]; then
    USE_PIP=true
fi
if [ "x${BUILD_PROFILE}" == "xl4t13" ]; then
    PYTHON_VERSION="3.12"
    PYTHON_PATCH="12"
    PY_STANDALONE_TAG="20251120"
fi

# Intel XPU has no upstream-published vllm wheels, so we always build vllm
# from source against torch-xpu and replace the default triton with
# triton-xpu (matching torch 2.11). Mirrors the upstream procedure:
# https://github.com/vllm-project/vllm/blob/main/docs/getting_started/installation/gpu.xpu.inc.md
if [ "x${BUILD_TYPE}" == "xintel" ]; then
    # Hide requirements-intel-after.txt so installRequirements doesn't
    # try `pip install vllm` (would either fail or grab a non-XPU wheel).
    _intel_after="${backend_dir}/requirements-intel-after.txt"
    _intel_after_bak=""
    if [ -f "${_intel_after}" ]; then
        _intel_after_bak="${_intel_after}.xpu.bak"
        mv "${_intel_after}" "${_intel_after_bak}"
    fi
    installRequirements
    if [ -n "${_intel_after_bak}" ]; then
        mv "${_intel_after_bak}" "${_intel_after}"
    fi

    # vllm's CMake build needs the Intel oneAPI dpcpp/sycl compiler — the
    # base image (intel/oneapi-basekit) has it but the env isn't sourced.
    if [ -f /opt/intel/oneapi/setvars.sh ]; then
        set +u
        source /opt/intel/oneapi/setvars.sh --force
        set -u
    fi

    _vllm_src=$(mktemp -d)
    trap 'rm -rf "${_vllm_src}"' EXIT
    git clone --depth 1 https://github.com/vllm-project/vllm "${_vllm_src}/vllm"
    pushd "${_vllm_src}/vllm"
        # Install vllm's own runtime deps (torch-xpu, vllm_xpu_kernels,
        # pydantic, fastapi, …) from upstream's requirements/xpu.txt — the
        # canonical source of truth. Avoids re-pinning everything ourselves.
        uv pip install ${EXTRA_PIP_INSTALL_FLAGS:-} -r requirements/xpu.txt
        # Stock triton (NVIDIA-only) may have come in transitively; replace
        # with triton-xpu==3.7.0 which matches torch 2.11.
        uv pip uninstall triton triton-xpu 2>/dev/null || true
        uv pip install ${EXTRA_PIP_INSTALL_FLAGS:-} \
            --extra-index-url https://download.pytorch.org/whl/xpu \
            triton-xpu==3.7.0
        export CMAKE_PREFIX_PATH="$(python -c 'import site; print(site.getsitepackages()[0])'):${CMAKE_PREFIX_PATH:-}"
        VLLM_TARGET_DEVICE=xpu uv pip install ${EXTRA_PIP_INSTALL_FLAGS:-} --no-deps .
    popd
# L4T arm64 (JetPack 7): drive the install through pyproject.toml so that
# [tool.uv.sources] can pin torch/vllm/flash-attn/torchvision/torchaudio
# to the jetson-ai-lab index, while everything else (transitive deps and
# PyPI-resolvable packages like transformers) comes from PyPI. Bypasses
# installRequirements because uv pip install -r requirements.txt does not
# honor sources — see backend/python/vllm/pyproject.toml for the rationale.
elif [ "x${BUILD_PROFILE}" == "xl4t13" ]; then
    ensureVenv
    if [ "x${PORTABLE_PYTHON}" == "xtrue" ]; then
        export C_INCLUDE_PATH="${C_INCLUDE_PATH:-}:$(_portable_dir)/include/python${PYTHON_VERSION}"
    fi
    pushd "${backend_dir}"
        # Build deps first (matches installRequirements' requirements-install.txt
        # pass — fastsafetensors and friends need pybind11 in the venv before
        # their sdists can build under --no-build-isolation).
        uv pip install ${EXTRA_PIP_INSTALL_FLAGS:-} -r requirements-install.txt
        uv pip install ${EXTRA_PIP_INSTALL_FLAGS:-} --requirement pyproject.toml
    popd
    runProtogen
# FROM_SOURCE=true on a CPU build skips the prebuilt vllm wheel in
# requirements-cpu-after.txt and compiles vllm locally against the host's
# actual CPU. Not used by default because it takes ~30-40 minutes, but
# kept here for hosts where the prebuilt wheel SIGILLs (CPU without the
# required SIMD baseline, e.g. AVX-512 VNNI/BF16). Default CI uses a
# bigger-runner with compatible hardware instead.
elif [ "x${BUILD_TYPE}" == "x" ] && [ "x${FROM_SOURCE:-}" == "xtrue" ]; then
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
