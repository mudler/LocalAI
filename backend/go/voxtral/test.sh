#!/bin/bash
set -e

CURDIR=$(dirname "$(realpath $0)")

echo "Running voxtral backend tests..."

# The test requires:
#   - VOXTRAL_MODEL_DIR: path to directory containing consolidated.safetensors + tekken.json
#   - VOXTRAL_BINARY: path to the voxtral binary (defaults to ./voxtral)
#
# Tests that require the model will be skipped if VOXTRAL_MODEL_DIR is not set.

cd "$CURDIR"
export VOXTRAL_MODEL_DIR="${VOXTRAL_MODEL_DIR:-./voxtral-model}"

if [ ! -d "$VOXTRAL_MODEL_DIR" ]; then
    echo "Creating voxtral-model directory for tests..."
    mkdir -p "$VOXTRAL_MODEL_DIR"
    MODEL_ID="mistralai/Voxtral-Mini-4B-Realtime-2602"
    echo "Model: ${MODEL_ID}"
    echo ""

    # Files to download
    FILES=(
        "consolidated.safetensors"
        "params.json"
        "tekken.json"
    )

    BASE_URL="https://huggingface.co/${MODEL_ID}/resolve/main"

    for file in "${FILES[@]}"; do
        dest="${VOXTRAL_MODEL_DIR}/${file}"
        if [ -f "${dest}" ]; then
            echo "  [skip] ${file} (already exists)"
        else
            echo "  [download] ${file}..."
            curl -L -o "${dest}" "${BASE_URL}/${file}" --progress-bar
            echo "  [done] ${file}"
        fi
    done
fi

# Run Go tests
go test -v -timeout 300s ./...

echo "All voxtral tests passed."
