#!/bin/bash
set -e

CURDIR=$(dirname "$(realpath $0)")
cd "$CURDIR"

echo "Running qwen3-tts-cpp backend tests..."

# Auto-download a small model pair only when QWEN3TTS_MODEL is not set.
if [ -z "$QWEN3TTS_MODEL" ]; then
    MODEL_DIR="./qwen3-tts-models"
    mkdir -p "$MODEL_DIR"
    REPO_ID="Serveurperso/Qwen3-TTS-GGUF"
    BASE_URL="https://huggingface.co/${REPO_ID}/resolve/main"
    FILES=( "qwen-talker-0.6b-base-Q4_K_M.gguf" "qwen-tokenizer-12hz-Q4_K_M.gguf" )
    for file in "${FILES[@]}"; do
        dest="${MODEL_DIR}/${file}"
        if [ -f "${dest}" ]; then
            echo "  [skip] ${file}"
        else
            echo "  [download] ${file}..."
            curl -L -o "${dest}" "${BASE_URL}/${file}" --progress-bar
        fi
    done
    export QWEN3TTS_MODEL="${MODEL_DIR}/qwen-talker-0.6b-base-Q4_K_M.gguf"
    export QWEN3TTS_CODEC="${MODEL_DIR}/qwen-tokenizer-12hz-Q4_K_M.gguf"
fi

go test -v -timeout 1200s .

echo "All qwen3-tts-cpp tests passed."
