#!/bin/bash
set -e

CURDIR=$(dirname "$(realpath $0)")

echo "Running vibevoice-cpp backend tests..."

# Required env-vars (set automatically when missing):
#   VIBEVOICE_MODEL_DIR : directory containing the gguf bundle.
#   VIBEVOICE_BINARY    : path to the built backend (default ./vibevoice-cpp)
#
# Tests skip when the model bundle is absent and the auto-download
# fails (e.g. no network on the runner) so local devs without HF access
# still get green compile output.

cd "$CURDIR"

if [ -z "$VIBEVOICE_MODEL_DIR" ]; then
    export VIBEVOICE_MODEL_DIR="./vibevoice-models"

    if [ ! -d "$VIBEVOICE_MODEL_DIR" ]; then
        echo "Creating vibevoice-models directory for tests..."
        mkdir -p "$VIBEVOICE_MODEL_DIR"

        REPO_ID="mudler/vibevoice.cpp-models"
        echo "Repository: ${REPO_ID}"

        # Q4_K instead of Q8_0 for the ASR model: smaller download
        # (10 GB vs 14 GB), fits on ubuntu-latest's free disk after the
        # runner image is loaded. The unit/closed-loop test only needs
        # decode quality, not Q8_0 precision.
        FILES=(
            "vibevoice-realtime-0.5B-q8_0.gguf"
            "vibevoice-asr-q4_k.gguf"
            "tokenizer.gguf"
            "voice-en-Carter_man.gguf"
        )

        BASE_URL="https://huggingface.co/${REPO_ID}/resolve/main"

        download_ok=1
        for file in "${FILES[@]}"; do
            dest="${VIBEVOICE_MODEL_DIR}/${file}"
            if [ -f "${dest}" ]; then
                echo "  [skip] ${file} (already exists)"
            else
                echo "  [download] ${file}..."
                if ! curl -fL -o "${dest}" "${BASE_URL}/${file}" --progress-bar; then
                    echo "  [warn] failed to download ${file} - network or HF unavailable"
                    rm -f "${dest}"
                    download_ok=0
                    break
                fi
                echo "  [done] ${file}"
            fi
        done

        if [ "$download_ok" != "1" ]; then
            echo "vibevoice-cpp: model bundle unavailable - tests will skip model-dependent cases."
            unset VIBEVOICE_MODEL_DIR
        fi
    fi
fi

# Ensure the per-variant .so the binary will dlopen actually exists -
# without one, every test will hit a Dlopen panic during server start.
if [ ! -f "${CURDIR}/libgovibevoicecpp-fallback.so" ]; then
    echo "vibevoice-cpp: libgovibevoicecpp-fallback.so missing - run \`make\` first."
    exit 1
fi

go test -v -timeout 900s .

echo "All vibevoice-cpp tests passed."
