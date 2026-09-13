#!/bin/bash
set -e

PYTHON_VERSION="3.12"
PYTHON_PATCH="12"
PY_STANDALONE_TAG="20251120"

backend_dir=$(dirname "$0")
if [ -d "$backend_dir/common" ]; then
    source "$backend_dir/common/libbackend.sh"
else
    source "$backend_dir/../common/libbackend.sh"
fi

if [ "x${BUILD_PROFILE}" == "xl4t12" ]; then
    USE_PIP=true
fi

# Omni and vLLM share internal APIs. Keep matched releases instead of resolving
# either project from main/latest. CUDA 13 needs the newer aarch64 wheels.
VLLM_VERSION=0.14.0
# vllm-omni v0.14.0
OMNI_REVISION=ed89c8b0436999e9210f11363f4eb512330a9dfa
engine_flags=()
if [ "x${BUILD_TYPE}" == "xhipblas" ]; then
    engine_flags=(--extra-index-url https://wheels.vllm.ai/rocm/0.14.0/rocm700)
elif [ "x${BUILD_PROFILE}" == "xcublas13" ] || [ "x${BUILD_PROFILE}" == "xl4t13" ]; then
    VLLM_VERSION=0.20.0
    # vllm-omni v0.20.0 still accepts the stage_configs_path model option.
    OMNI_REVISION=4a24a517abc7769b1399ded594558a3fe8269872
elif [ "x${BUILD_TYPE}" != "xcublas" ] && [ "x${BUILD_TYPE}" != "x" ]; then
    echo "Unsupported build type: ${BUILD_TYPE}" >&2
    exit 1
fi

installRequirements

if [ "x${USE_PIP}" == "xtrue" ]; then
    installer=(pip install)
else
    installer=(uv pip install)
    if [ "x${BUILD_TYPE}" != "xhipblas" ]; then
        engine_flags+=(--torch-backend=auto)
    fi
fi
"${installer[@]}" "vllm==${VLLM_VERSION}" "${engine_flags[@]}"

# Use a fresh checkout so a previous build cannot leave a stale or modified
# source tree. The installed wheel must survive removal of this directory and
# relocation of the packaged backend's virtual environment.
omni_source=$(mktemp -d)
trap 'rm -rf "$omni_source"' EXIT
(
    cd "$omni_source"
    git init
    git fetch --depth 1 https://github.com/vllm-project/vllm-omni.git "$OMNI_REVISION"
    git checkout --detach FETCH_HEAD
    # Preserve release metadata for setuptools-scm in the shallow checkout.
    git tag "v${VLLM_VERSION}"

    # FA3 publishes no aarch64 wheel. These releases keep this optional CUDA
    # kernel dependency in different files; neither needs it to import on ARM.
    if [ "$(uname -m)" = "aarch64" ]; then
        if [ -f requirements/cuda.txt ]; then
            sed -i '/^fa3-fwd[[:space:]]*==/d' requirements/cuda.txt
        fi
        sed -i '/^[[:space:]]*"fa3-fwd==/d' pyproject.toml
    fi

    # v0.14 only declares generic stage configs as package data. Preserve the
    # platform YAML files too, or ROCm silently uses generic batching defaults.
    printf '\nrecursive-include vllm_omni *.yaml\n' >> MANIFEST.in

    # Retain the engine pin while resolving Omni's dependencies too.
    "${installer[@]}" ${EXTRA_PIP_INSTALL_FLAGS:-} . "vllm==${VLLM_VERSION}" "${engine_flags[@]}"

    # Check wheel resources without importing GPU-dependent modules. Verify
    # every source YAML, so future upstream packaging changes fail the build.
    python -I - <<'PY'
from importlib.metadata import distribution
from pathlib import Path

dist = distribution("vllm-omni")
installed = {str(path) for path in dist.files or []}
resources = list(Path("vllm_omni").rglob("*.yaml"))
if not resources:
    raise RuntimeError("vllm-omni source contains no YAML resources")
for source in resources:
    target = Path(dist.locate_file(str(source)))
    if (str(source) not in installed or not target.is_file()
            or target.read_bytes() != source.read_bytes()):
        raise RuntimeError(f"vllm-omni wheel omits or changes {source}")
PY
)

# Omni can change the protobuf runtime after the initial stub generation.
runProtogen
