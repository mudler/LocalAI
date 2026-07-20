#!/bin/bash
set -e

EXTRA_PIP_INSTALL_FLAGS="--no-build-isolation"

# grpcio 1.82 bundles protoc 7.x, while vLLM's resolved protobuf runtime is
# still 6.33.x. Generate the backend stubs with the newest grpcio-tools release
# that emits protobuf 6.x gencode; the newer grpcio runtime can consume those
# stubs, but protobuf 6.x cannot import 7.x gencode.
export GRPCIO_TOOLS_VERSION="1.78.0"

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

# AMD ROCm: vLLM ships prebuilt ROCm wheels, but on a DEDICATED index
# (https://wheels.vllm.ai/rocm/), NOT PyPI, and ONLY for CPython 3.12. On any
# other Python the installer silently falls back to the CUDA-only PyPI wheel,
# which is unusable on an AMD GPU (import fails, so the backend never finds the
# vllm module). Force Python 3.12 before the venv is created (matches the
# intel/l4t13 cp312 bump); the hipblas branch below pulls vllm from the ROCm
# wheel index. unsafe-best-match lets uv consult that index and PyPI together.
# https://docs.vllm.ai/en/latest/getting_started/installation/gpu.html?device=rocm
if [ "x${BUILD_TYPE}" == "xhipblas" ]; then
    PYTHON_VERSION="3.12"
    PYTHON_PATCH="12"
    PY_STANDALONE_TAG="20251120"
    EXTRA_PIP_INSTALL_FLAGS+=" --index-strategy=unsafe-best-match"
fi

# cublas13 pulls the vLLM wheel from a per-tag cu130 index (PyPI's vllm wheel
# is built against CUDA 12 and won't load on cu130). uv's default per-package
# first-match strategy would still pick the PyPI wheel, so allow it to consult
# every configured index when resolving.
if [ "x${BUILD_PROFILE}" == "xcublas13" ]; then
    EXTRA_PIP_INSTALL_FLAGS+=" --index-strategy=unsafe-best-match"
fi

# Apple Silicon (Metal/MLX) via vllm-metal.
# vllm-metal (github.com/vllm-project/vllm-metal) brings vLLM to macOS on Apple
# Silicon: it registers through vLLM's platform-plugin entry point
# (metal -> vllm_metal:register), MetalPlatform activates, and the vLLM v1
# AsyncLLM engine runs on the GPU through MLX. LocalAI's backend.py is UNCHANGED
# on darwin — AsyncEngineArgs(...) -> AsyncLLMEngine.from_engine_args transparently
# resolves to the MLX engine (proven on a real M4 / macOS 26.5 against Qwen3-0.6B).
#
# vllm-metal REQUIRES Python 3.12, so force the portable CPython before the venv
# is created (ensureVenv reads PYTHON_VERSION/PYTHON_PATCH/PY_STANDALONE_TAG).
# The patch + standalone tag mirror the l4t13 cp312 pin — a known-good
# python-build-standalone release that also ships an aarch64-apple-darwin asset.
if [ "$(uname -s)" = "Darwin" ]; then
    PYTHON_VERSION="3.12"
    PYTHON_PATCH="12"
    PY_STANDALONE_TAG="20251120"
fi

# JetPack 7 / L4T arm64 vllm + torch wheels come straight from PyPI now
# (torch 2.11+ ships aarch64 + cu130 manylinux wheels and vllm 0.20+ ships
# an aarch64 wheel pinned to that torch). They're cp312-only, so bump the
# venv Python accordingly. JetPack 6 keeps cp310 + USE_PIP=true.
# https://pytorch.org/blog/vllm-and-pytorch-work-together-to-improve-the-developer-experience-on-aarch64/
if [ "x${BUILD_PROFILE}" == "xl4t12" ]; then
    USE_PIP=true
fi
if [ "x${BUILD_PROFILE}" == "xl4t13" ]; then
    PYTHON_VERSION="3.12"
    PYTHON_PATCH="12"
    PY_STANDALONE_TAG="20251120"
fi

# ===================== Apple Silicon (Metal/MLX) =====================
# Reproduce vllm-metal's upstream installer
# (curl -fsSL https://raw.githubusercontent.com/vllm-project/vllm-metal/main/install.sh)
# but INTO LocalAI's managed venv (ensureVenv) instead of a throwaway
# ~/.venv-vllm-metal, so the backend integrates with LocalAI's venv lifecycle
# (portable CPython, _makeVenvPortable relocation, runtime activation). The
# normal CUDA/CPU installRequirements is skipped on darwin — there is no
# macOS/arm64 vLLM wheel on PyPI; vLLM is built from source and the MLX engine
# is layered on by the vllm-metal wheel.
if [ "$(uname -s)" = "Darwin" ]; then
    # Create/activate the portable 3.12 venv. On darwin USE_PIP=true and
    # PORTABLE_PYTHON=true (set by scripts/build/python-darwin.sh), so this is a
    # `python -m venv` based, relocatable venv.
    ensureVenv

    # vllm-metal's installer drives everything through `uv`: building vLLM from
    # the CPU requirements needs `--index-strategy unsafe-best-match` (mixes the
    # pytorch CPU channel with PyPI), a flag plain pip does not have. The darwin
    # venv is pip-based, so bootstrap uv into it. uv honours $VIRTUAL_ENV (set by
    # libbackend's _activateVenv) and installs into THIS venv — same pattern the
    # intel branch below relies on.
    pip install uv

    # The ONLY darwin version pin -- AUTO-BUMPED by .github/bump_vllm_metal.sh,
    # which tracks vllm-project/vllm-metal releases (NOT vllm/vllm latest). Keep
    # it as a plain double-quoted assignment on its own line so the bumper's sed
    # can rewrite it. Darwin therefore follows vllm-metal and can lag the Linux
    # vllm pin (requirements-cublas13-after.txt, bumped independently against
    # vllm/vllm) until vllm-metal supports a newer vLLM.
    VLLM_METAL_VERSION="v0.3.0.dev20260717051959"

    # The coupled vLLM source version is whatever this vllm-metal release builds
    # against -- it declares it in its own installer as `vllm_v=`. Derive it from
    # the PINNED tag rather than hardcoding a second value that could drift. The
    # tag is immutable, so this stays reproducible across rebuilds.
    VLLM_VERSION=$(curl -fsSL "https://raw.githubusercontent.com/vllm-project/vllm-metal/${VLLM_METAL_VERSION}/install.sh" \
        | grep -oE 'vllm_v="[0-9]+\.[0-9]+\.[0-9]+"' | head -n1 | cut -d'"' -f2)
    if [ -z "${VLLM_VERSION}" ]; then
        echo "ERROR: could not derive the vLLM version from vllm-metal ${VLLM_METAL_VERSION}" >&2
        exit 1
    fi
    echo "vllm-metal ${VLLM_METAL_VERSION} builds against vLLM ${VLLM_VERSION}"

    _vllm_src=$(mktemp -d)
    trap 'rm -rf "${_vllm_src}"' EXIT
    pushd "${_vllm_src}"
        # 1) Build vLLM ${VLLM_VERSION} from the release source tarball against
        #    the CPU requirements. vllm-metal layers its MLX platform plugin on
        #    top of this exact build.
        curl -fsSL -o "vllm-${VLLM_VERSION}.tar.gz" \
            "https://github.com/vllm-project/vllm/releases/download/v${VLLM_VERSION}/vllm-${VLLM_VERSION}.tar.gz"
        tar -xzf "vllm-${VLLM_VERSION}.tar.gz"
        pushd "vllm-${VLLM_VERSION}"
            uv pip install -r requirements/cpu.txt --index-strategy unsafe-best-match
            # -Wno-parentheses: clang on macOS treats one of vLLM's C++ warnings
            # as an error without it (matches the upstream installer's CXXFLAGS).
            CXXFLAGS="-Wno-parentheses" uv pip install .
        popd
    popd

    # 2) Install the prebuilt vllm-metal wheel for the PINNED release. It pulls
    #    mlx / mlx-metal as deps and registers the `metal` platform plugin that
    #    backend.py resolves to at engine-init time. Build the release-asset URL
    #    deterministically (tag + the cp312/arm64 wheel name) rather than querying
    #    api.github.com, whose unauthenticated rate limit (60/hr per IP) 403s on
    #    shared CI runners. The wheel version is the tag without its leading 'v'.
    _metal_wheel="vllm_metal-${VLLM_METAL_VERSION#v}-cp312-cp312-macosx_11_0_arm64.whl"
    _metal_wheel_url="https://github.com/vllm-project/vllm-metal/releases/download/${VLLM_METAL_VERSION}/${_metal_wheel}"
    echo "Installing vllm-metal wheel: ${_metal_wheel_url}"
    uv pip install "${_metal_wheel_url}"

    # Generate the gRPC stubs (backend_pb2*). installRequirements normally does
    # this via runProtogen at the end; we skipped installRequirements on darwin,
    # so call it explicitly here.
    runProtogen

# Intel XPU has no upstream-published vllm wheels, so we always build vllm
# from source against torch-xpu and replace the default triton with
# triton-xpu (matching torch 2.11). Mirrors the upstream procedure:
# https://github.com/vllm-project/vllm/blob/main/docs/getting_started/installation/gpu.xpu.inc.md
elif [ "x${BUILD_TYPE}" == "xintel" ]; then
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
# AMD ROCm / gfx1151 (RDNA 3.5, Strix Halo): there is NO prebuilt vllm wheel for
# this GPU. wheels.vllm.ai/rocm ships only a gfx942/gfx950 (CDNA) build pinned to
# a rocm7.2.3 torch that cannot even enumerate Strix Halo (device_count == 0), and
# AMD's gfx1151 wheel index carries torch/triton but no vllm. So build vllm from
# source against AMD's ROCm 7.14 torch, targeting gfx1151 — mirrors the intel/cpu
# source-build branches above. This reproduces the exact stack AMD's own
# rocm/vllm:*_rdna_* image ships and runs on gfx1151 (torch 2.11.0+rocm7.14.0,
# vllm 0.23.1.dev1 @ commit 9ddef7117). The Python-3.12 pin is set above.
elif [ "x${BUILD_TYPE}" == "xhipblas" ]; then
    # vllm's HIP/C++ compile is large; MAX_JOBS=1 (set above for CUDA hosts) would
    # take hours. 8 jobs balances throughput vs the per-job RAM of the ROCm kernels.
    export MAX_JOBS=8

    # rocm_smi's CMake package (pulled by vllm's ROCm build) probes system libdrm
    # via pkg-config; these are OS-level build tools, not ROCm. No-op outside the
    # Docker builder stage. The ROCm toolchain itself comes entirely from pip below.
    if command -v apt-get >/dev/null 2>&1 && [ "$(id -u)" = "0" ]; then
        apt-get update
        DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
            pkg-config libdrm-dev
    fi

    # GPU arch to build for. AMDGPU_TARGETS comes from the build (Dockerfile.python
    # ARG/ENV); default to gfx1151 -- the only arch we could validate on real
    # hardware. AMD ships a per-GPU torch build for essentially every AMD arch on
    # the multi-arch index, selected via the torch[device-gfx<arch>] extra, which
    # takes a single arch, so use the first target for that selection while the
    # source build below targets the full AMDGPU_TARGETS list.
    _gpu_targets="${AMDGPU_TARGETS:-gfx1151}"
    _gpu_arch="${_gpu_targets%%;*}"; _gpu_arch="${_gpu_arch%% *}"

    ensureVenv
    # Self-contained ROCm 7.14 build -- reproduces AMD's own rocm/vllm rdna recipe.
    # From AMD's multi-arch index: the per-GPU torch (torch[device-gfx<arch>]) AND
    # the full devel SDK (rocm-sdk-devel: hipcc + the HIP CMake packages that the
    # runtime _rocm_sdk_core lacks). Everything lives in the venv, so the build
    # needs NO system ROCm at /opt/rocm and reproduces on any base image with pip.
    uv pip install --index-url https://repo.amd.com/rocm/whl-multi-arch/ \
        "torch[device-${_gpu_arch}]==2.11.0+rocm7.14.0" \
        "torchvision==0.26.0+rocm7.14.0" \
        "rocm-sdk-devel==7.14.0"
    installRequirements

    # Expand the devel tree and link the installed rocm-sdk-device-* wheels into
    # it (required before the SDK's hipcc/CMake are usable), then resolve every
    # path from the SDK's own CLI — no hardcoded location, no host dependency.
    rocm-sdk init
    ROCM_PATH="$(rocm-sdk path --root)"
    export ROCM_PATH ROCM_HOME="$ROCM_PATH"
    export CMAKE_PREFIX_PATH="$(rocm-sdk path --cmake)${CMAKE_PREFIX_PATH:+:$CMAKE_PREFIX_PATH}"
    export PATH="$(rocm-sdk path --bin):$PATH"

    # vLLM detects ROCm at runtime via `import amdsmi` (platforms/__init__.py:
    # amdsmi_init() + get_processor_handles()); without it vLLM falls back to
    # UnspecifiedPlatform and dies with "Device string must not be empty".
    # rocm-sdk-core ships amd_smi in-place under _rocm_sdk_core/share/amd_smi,
    # where it resolves its native libamd_smi.so RELATIVE to that directory.
    # A pip copy into site-packages breaks that relative lookup, and forcing the
    # lib via LD_LIBRARY_PATH shadows torch's own bundled ROCm runtime and drops
    # device_count to 0. So register the in-place package with a .pth entry --
    # `import amdsmi` then resolves where its libs do, no LD override needed.
    # (AMD's rocm/vllm image likewise imports amdsmi in-place from this path.)
    _sp="$(python -c 'import sysconfig; print(sysconfig.get_paths()["purelib"])')"
    printf '_rocm_sdk_core/share/amd_smi\n' > "${_sp}/amdsmi.pth"

    # Build vllm from source against this ROCm 7.14 torch. Pin to the release tag
    # closest to what AMD's rocm/vllm rdna image ships (0.23.1.dev1+g9ddef7117
    # sits between v0.23.0 and v0.23.1rc0).
    VLLM_REF="${VLLM_REF:-v0.23.0}"
    _vllm_src=$(mktemp -d)
    trap 'rm -rf "${_vllm_src}"' EXIT
    git clone --depth 1 --branch "${VLLM_REF}" \
        https://github.com/vllm-project/vllm "${_vllm_src}/vllm"
    pushd "${_vllm_src}/vllm"
        # Strip vllm's own torch/triton pins so it builds against our ROCm wheels.
        python use_existing_torch.py || true
        uv pip install ${EXTRA_PIP_INSTALL_FLAGS:-} -r requirements/rocm.txt
        # No GPU in the build container -> pin the target arch(es) explicitly.
        export PYTORCH_ROCM_ARCH="${_gpu_targets}" GPU_TARGETS="${_gpu_targets}" GPU_ARCHS="${_gpu_targets}"
        VLLM_TARGET_DEVICE=rocm uv pip install ${EXTRA_PIP_INSTALL_FLAGS:-} --no-deps .
    popd
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
