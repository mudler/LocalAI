#!/bin/bash
set -e

CURDIR=$(dirname "$(realpath $0)")

echo "Running acestep-cpp backend tests..."

# The test requires:
#   - ACESTEP_MODEL_DIR: path to directory containing GGUF model files
#   - ACESTEP_BINARY: path to the acestep-cpp binary (defaults to ./acestep-cpp)
#
# Tests that require the model will be skipped if ACESTEP_MODEL_DIR is not set
# or the directory does not contain the required model files.

cd "$CURDIR"

# Only auto-download models when ACESTEP_MODEL_DIR is not explicitly set
if [ -z "$ACESTEP_MODEL_DIR" ]; then
    export ACESTEP_MODEL_DIR="./acestep-models"

    if [ ! -d "$ACESTEP_MODEL_DIR" ]; then
        echo "Creating acestep-models directory for tests..."
        mkdir -p "$ACESTEP_MODEL_DIR"
        REPO_ID="Serveurperso/ACE-Step-1.5-GGUF"
        echo "Repository: ${REPO_ID}"
        echo ""

        # Files to download (smallest quantizations for testing)
        FILES=(
            "acestep-5Hz-lm-0.6B-Q8_0.gguf"
            "Qwen3-Embedding-0.6B-Q8_0.gguf"
            "acestep-v15-turbo-Q8_0.gguf"
            "vae-BF16.gguf"
        )

        BASE_URL="https://huggingface.co/${REPO_ID}/resolve/main"

        for file in "${FILES[@]}"; do
            dest="${ACESTEP_MODEL_DIR}/${file}"
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

echo "All acestep-cpp tests passed."
