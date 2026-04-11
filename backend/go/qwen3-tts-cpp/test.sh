#!/bin/bash
set -e

CURDIR=$(dirname "$(realpath $0)")

echo "Running qwen3-tts-cpp backend tests..."

# The test requires:
#   - QWEN3TTS_MODEL_DIR: path to directory containing GGUF model files
#   - QWEN3TTS_BINARY: path to the qwen3-tts-cpp binary (defaults to ./qwen3-tts-cpp)
#
# Tests that require the model will be skipped if QWEN3TTS_MODEL_DIR is not set
# or the directory does not contain the required model files.

cd "$CURDIR"

# Only auto-download models when QWEN3TTS_MODEL_DIR is not explicitly set
if [ -z "$QWEN3TTS_MODEL_DIR" ]; then
    export QWEN3TTS_MODEL_DIR="./qwen3-tts-models"

    if [ ! -d "$QWEN3TTS_MODEL_DIR" ]; then
        echo "Creating qwen3-tts-models directory for tests..."
        mkdir -p "$QWEN3TTS_MODEL_DIR"
        REPO_ID="endo5501/qwen3-tts.cpp"
        echo "Repository: ${REPO_ID}"
        echo ""

        # Files to download (smallest model for testing)
        FILES=(
            "qwen3-tts-0.6b-f16.gguf"
            "qwen3-tts-tokenizer-f16.gguf"
        )

        BASE_URL="https://huggingface.co/${REPO_ID}/resolve/main"

        for file in "${FILES[@]}"; do
            dest="${QWEN3TTS_MODEL_DIR}/${file}"
            if [ -f "${dest}" ]; then
                echo "  [skip] ${file} (already exists)"
            else
                echo "  [download] ${file}..."
                curl -L -o "${dest}" "${BASE_URL}/${file}" --progress-bar
                echo "  [done] ${file}"
            fi
        done
    fi
fi

# Run Go tests
go test -v -timeout 600s .

echo "All qwen3-tts-cpp tests passed."
