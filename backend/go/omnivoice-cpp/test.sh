#!/bin/bash
set -e

CURDIR=$(dirname "$(realpath $0)")
cd "$CURDIR"

echo "Running omnivoice-cpp backend tests..."

if [ -z "$OMNIVOICE_MODEL" ]; then
    MODEL_DIR="./omnivoice-models"
    mkdir -p "$MODEL_DIR"
    REPO_ID="Serveurperso/OmniVoice-GGUF"
    BASE_URL="https://huggingface.co/${REPO_ID}/resolve/main"
    FILES=( "omnivoice-base-Q4_K_M.gguf" "omnivoice-tokenizer-Q4_K_M.gguf" )
    for file in "${FILES[@]}"; do
        dest="${MODEL_DIR}/${file}"
        if [ -f "${dest}" ]; then
            echo "  [skip] ${file}"
        else
            echo "  [download] ${file}..."
            curl -L -o "${dest}" "${BASE_URL}/${file}" --progress-bar
        fi
    done
    export OMNIVOICE_MODEL="${MODEL_DIR}/omnivoice-base-Q4_K_M.gguf"
    export OMNIVOICE_CODEC="${MODEL_DIR}/omnivoice-tokenizer-Q4_K_M.gguf"
fi

go test -v -timeout 1200s .

echo "All omnivoice-cpp e2e tests passed."
