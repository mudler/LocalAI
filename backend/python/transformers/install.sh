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

# ROCm/gfx1151: the community whl/rocm7.0 torch wheel does not enumerate Strix
# Halo (device_count == 0). AMD ships a stable ROCm 7.14 build via its multi-arch
# index, selected per GPU with the torch[device-gfx<arch>] extra. Install it (plus
# its rocm/triton deps, which all live on that same index) in an isolated step:
# uv aborts on that index's 403-for-missing-package responses, so we must not let
# it look up PyPI-only packages (accelerate, transformers, ...) there. Everything
# else then resolves from PyPI in installRequirements.
if [ "x${BUILD_PROFILE}" == "xhipblas" ]; then
    # Arch from the build (AMDGPU_TARGETS); default gfx1151 -- the only arch we
    # could validate on real hardware. The torch[device-...] extra takes one arch.
    _gpu_arch="${AMDGPU_TARGETS:-gfx1151}"; _gpu_arch="${_gpu_arch%%;*}"; _gpu_arch="${_gpu_arch%% *}"
    ensureVenv
    uv pip install --index-url https://repo.amd.com/rocm/whl-multi-arch/ \
        "torch[device-${_gpu_arch}]==2.12.0+rocm7.14.0"
fi

installRequirements
