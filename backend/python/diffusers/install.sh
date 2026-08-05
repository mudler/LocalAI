#!/bin/bash
set -e

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

if [ "x${BUILD_PROFILE}" == "xl4t12" ]; then
    USE_PIP=true
fi

# Use python 3.12 for l4t
if [ "x${BUILD_PROFILE}" == "xl4t13" ]; then
  PYTHON_VERSION="3.12"
  PYTHON_PATCH="12"
  PY_STANDALONE_TAG="20251120"
fi

# ROCm/gfx1151: the community whl/rocm7.0 torch wheels do not enumerate Strix
# Halo (device_count == 0). AMD ships stable ROCm 7.14 builds via its multi-arch
# index, selected per GPU with the torch[device-gfx<arch>] extra. Install torch +
# torchvision (the same pinned versions, from that index) in an isolated step:
# uv aborts on that index's 403-for-missing-package responses, so we must not let
# it resolve PyPI-only packages (diffusers, transformers, ...) there. Everything
# else then resolves from PyPI in installRequirements.
if [ "x${BUILD_PROFILE}" == "xhipblas" ]; then
    # Arch from the build (AMDGPU_TARGETS); default gfx1151 -- the only arch we
    # could validate on real hardware. The torch[device-...] extra takes one arch.
    _gpu_arch="${AMDGPU_TARGETS:-gfx1151}"; _gpu_arch="${_gpu_arch%%;*}"; _gpu_arch="${_gpu_arch%% *}"
    ensureVenv
    uv pip install --index-url https://repo.amd.com/rocm/whl-multi-arch/ \
        "torch[device-${_gpu_arch}]==2.10.0+rocm7.14.0" \
        "torchvision==0.25.0+rocm7.14.0"
fi

installRequirements
