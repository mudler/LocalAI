#!/bin/bash
set -e

CURDIR=$(dirname "$(realpath $0)")
cd "$CURDIR"

echo "Running magpie-tts-cpp backend tests..."

# Auto-download the q8_0 GGUF only when MAGPIETTS_MODEL is not set.
if [ -z "$MAGPIETTS_MODEL" ]; then
    MODEL_DIR="./magpie-tts-models"
    mkdir -p "$MODEL_DIR"
    REPO_ID="mudler/magpie-tts.cpp-gguf"
    BASE_URL="https://huggingface.co/${REPO_ID}/resolve/main"
    FILE="magpie-tts-multilingual-357m-q8_0.gguf"
    dest="${MODEL_DIR}/${FILE}"
    if [ -f "${dest}" ]; then
        echo "  [skip] ${FILE}"
    else
        echo "  [download] ${FILE}..."
        curl -L -o "${dest}" "${BASE_URL}/${FILE}" --progress-bar
    fi
    export MAGPIETTS_MODEL="${dest}"
fi

go test -v -timeout 1200s .

echo "All magpie-tts-cpp tests passed."
