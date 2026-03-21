#!/bin/bash
set -e

backend_dir=$(dirname $0)
if [ -d $backend_dir/common ]; then
    source $backend_dir/common/libbackend.sh
else
    source $backend_dir/../common/libbackend.sh
fi

EXTRA_PIP_INSTALL_FLAGS+=" --upgrade --index-strategy=unsafe-first-match"
installRequirements

# Fetch convert_hf_to_gguf.py and gguf package from the same llama.cpp version
LLAMA_CPP_CONVERT_VERSION="${LLAMA_CPP_CONVERT_VERSION:-master}"
CONVERT_SCRIPT="${EDIR}/convert_hf_to_gguf.py"
if [ ! -f "${CONVERT_SCRIPT}" ]; then
    echo "Downloading convert_hf_to_gguf.py from llama.cpp (${LLAMA_CPP_CONVERT_VERSION})..."
    curl -L --fail --retry 3 \
        "https://raw.githubusercontent.com/ggml-org/llama.cpp/${LLAMA_CPP_CONVERT_VERSION}/convert_hf_to_gguf.py" \
        -o "${CONVERT_SCRIPT}" || echo "Warning: Failed to download convert_hf_to_gguf.py. GGUF export will not be available."
fi

# Install gguf package from the same llama.cpp commit to keep them in sync
GGUF_PIP_SPEC="gguf @ git+https://github.com/ggml-org/llama.cpp@${LLAMA_CPP_CONVERT_VERSION}#subdirectory=gguf-py"
echo "Installing gguf package from llama.cpp (${LLAMA_CPP_CONVERT_VERSION})..."
if [ "x${USE_PIP:-}" == "xtrue" ]; then
    pip install "${GGUF_PIP_SPEC}" || {
        echo "Warning: Failed to install gguf from llama.cpp commit, falling back to PyPI..."
        pip install "gguf>=0.16.0"
    }
else
    uv pip install "${GGUF_PIP_SPEC}" || {
        echo "Warning: Failed to install gguf from llama.cpp commit, falling back to PyPI..."
        uv pip install "gguf>=0.16.0"
    }
fi
