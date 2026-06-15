#!/bin/bash
set -e

backend_dir=$(dirname $0)
if [ -d $backend_dir/common ]; then
    source $backend_dir/common/libbackend.sh
else
    source $backend_dir/../common/libbackend.sh
fi

EXTRA_PIP_INSTALL_FLAGS+=" --upgrade "
installRequirements

# Fetch convert_hf_to_gguf.py from llama.cpp
LLAMA_CPP_CONVERT_VERSION="${LLAMA_CPP_CONVERT_VERSION:-master}"
CONVERT_SCRIPT="${EDIR}/convert_hf_to_gguf.py"
if [ ! -f "${CONVERT_SCRIPT}" ]; then
    echo "Downloading convert_hf_to_gguf.py from llama.cpp (${LLAMA_CPP_CONVERT_VERSION})..."
    curl -L --fail --retry 3 \
        "https://raw.githubusercontent.com/ggml-org/llama.cpp/${LLAMA_CPP_CONVERT_VERSION}/convert_hf_to_gguf.py" \
        -o "${CONVERT_SCRIPT}" || echo "Warning: Failed to download convert_hf_to_gguf.py."
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

# Build llama-quantize from llama.cpp if not already present
QUANTIZE_BIN="${EDIR}/llama-quantize"
if [ ! -x "${QUANTIZE_BIN}" ] && ! command -v llama-quantize &>/dev/null; then
    if command -v cmake &>/dev/null; then
        echo "Building llama-quantize from llama.cpp (${LLAMA_CPP_CONVERT_VERSION})..."
        LLAMA_CPP_SRC="${EDIR}/llama.cpp"
        if [ ! -d "${LLAMA_CPP_SRC}" ]; then
            git clone --depth 1 --branch "${LLAMA_CPP_CONVERT_VERSION}" \
                https://github.com/ggml-org/llama.cpp.git "${LLAMA_CPP_SRC}" 2>/dev/null || \
            git clone --depth 1 https://github.com/ggml-org/llama.cpp.git "${LLAMA_CPP_SRC}"
        fi
        cmake -B "${LLAMA_CPP_SRC}/build" -S "${LLAMA_CPP_SRC}" -DGGML_NATIVE=OFF -DBUILD_SHARED_LIBS=OFF
        cmake --build "${LLAMA_CPP_SRC}/build" --target llama-quantize -j"$(nproc 2>/dev/null || echo 2)"
        cp "${LLAMA_CPP_SRC}/build/bin/llama-quantize" "${QUANTIZE_BIN}"
        chmod +x "${QUANTIZE_BIN}"
        echo "Built llama-quantize at ${QUANTIZE_BIN}"
    else
        echo "Warning: cmake not found — llama-quantize will not be available. Install cmake or provide llama-quantize on PATH."
    fi
fi
