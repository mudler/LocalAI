#!/bin/bash
set -e

CURDIR=$(dirname "$(realpath $0)")
cd "$CURDIR"

echo "Running moss-tts-cpp backend tests..."

# Auto-download the Local v1.5 model + codec + tokenizer only when
# MOSSTTS_MODEL is not set.
if [ -z "$MOSSTTS_MODEL" ]; then
    MODEL_DIR="./moss-tts-models"
    mkdir -p "$MODEL_DIR"
    REPO_ID="mudler/MOSS-TTS-Local-Transformer-v1.5-GGUF"
    BASE_URL="https://huggingface.co/${REPO_ID}/resolve/main"
    FILES=( "moss-tts-local-v1_5-q8_0.gguf" "moss-audio-tokenizer-v2-f32.gguf" "moss-tokenizer-v1_5.gguf" )
    for file in "${FILES[@]}"; do
        dest="${MODEL_DIR}/${file}"
        if [ -f "${dest}" ]; then
            echo "  [skip] ${file}"
        else
            echo "  [download] ${file}..."
            curl -L -o "${dest}" "${BASE_URL}/${file}" --progress-bar
        fi
    done
    export MOSSTTS_MODEL="${MODEL_DIR}/moss-tts-local-v1_5-q8_0.gguf"
    export MOSSTTS_CODEC="${MODEL_DIR}/moss-audio-tokenizer-v2-f32.gguf"
    export MOSSTTS_TOKENIZER="${MODEL_DIR}/moss-tokenizer-v1_5.gguf"
fi

go test -v -timeout 1200s .

echo "All moss-tts-cpp tests passed."
