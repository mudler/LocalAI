#!/bin/bash
# Drives tests/e2e/realtime_ws_test.go against a realtime pipeline where
# VAD, STT and TTS are served by the sherpa-onnx Docker backend, and the
# LLM slot stays mocked by the in-repo mock-backend. Pre-requisites:
#   - `make build-mock-backend` has produced tests/e2e/mock-backend/mock-backend
#   - `make docker-build-sherpa-onnx` has produced local-ai-backend:sherpa-onnx
#   - `make protogen-go` is up-to-date
# Environment overrides:
#   WORK_DIR   Where to stage the extracted backend + model files.
#              Defaults to a mktemp'd directory (cleaned on exit).
#   KEEP_WORK  Non-empty to preserve WORK_DIR after the test exits (useful for
#              debugging repeated runs — skips redownloads if files already present).
set -euo pipefail

ROOT=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
IMAGE=${BACKEND_IMAGE:-local-ai-backend:sherpa-onnx}
MODEL=${REALTIME_STT_MODEL:-omnilingual-0.3b-ctc-q8-sherpa}
VAD_MODEL=${REALTIME_VAD_MODEL:-silero-vad-sherpa}
TTS_MODEL=${REALTIME_TTS_MODEL:-vits-ljs-sherpa}

WORK_DIR=${WORK_DIR:-$(mktemp -d -t localai-sherpa-realtime.XXXXXX)}
if [[ -z "${KEEP_WORK:-}" ]]; then
    trap 'rm -rf "$WORK_DIR"' EXIT
fi
echo "WORK_DIR=$WORK_DIR"

BACKENDS_DIR="$WORK_DIR/backends"
MODELS_DIR="$WORK_DIR/models"
mkdir -p "$BACKENDS_DIR/sherpa-onnx" "$MODELS_DIR"

# 1. Extract the sherpa-onnx backend image rootfs. Mirrors the pattern in
# tests/e2e-backends/backend_test.go:extractImage — docker create + export
# avoids having to pull and parse layer tarballs.
if [[ ! -x "$BACKENDS_DIR/sherpa-onnx/run.sh" ]]; then
    echo "Extracting $IMAGE rootfs into $BACKENDS_DIR/sherpa-onnx ..."
    CID=$(docker create --entrypoint=/run.sh "$IMAGE")
    trap 'docker rm -f "$CID" >/dev/null 2>&1 || true; [[ -z "${KEEP_WORK:-}" ]] && rm -rf "$WORK_DIR"' EXIT
    docker export "$CID" | tar -xC "$BACKENDS_DIR/sherpa-onnx" \
        --exclude='dev/*' --exclude='proc/*' --exclude='sys/*'
    docker rm -f "$CID" >/dev/null
fi

# Make sure run.sh is executable (tar usually preserves this, but belt + braces).
chmod +x "$BACKENDS_DIR/sherpa-onnx/run.sh" \
         "$BACKENDS_DIR/sherpa-onnx/sherpa-onnx" 2>/dev/null || true

# 2. Download model files. URLs + sha256s match gallery/index.yaml entries.
download() {
    local dst="$1" url="$2" sha="$3"
    if [[ -f "$dst" ]]; then
        actual=$(sha256sum "$dst" | awk '{print $1}')
        if [[ "$actual" == "$sha" ]]; then
            echo "cached: $dst"
            return
        fi
    fi
    mkdir -p "$(dirname "$dst")"
    echo "downloading: $url -> $dst"
    curl -sSfL "$url" -o "$dst"
    actual=$(sha256sum "$dst" | awk '{print $1}')
    if [[ "$actual" != "$sha" ]]; then
        echo "sha256 mismatch for $dst: got $actual, expected $sha" >&2
        exit 1
    fi
}

# Silero VAD (single file)
download "$MODELS_DIR/silero-vad/silero-vad.onnx" \
    "https://huggingface.co/onnx-community/silero-vad/resolve/main/onnx/model.onnx" \
    "a4a068cd6cf1ea8355b84327595838ca748ec29a25bc91fc82e6c299ccdc5808"

# Omnilingual ASR (model + tokens)
download "$MODELS_DIR/omnilingual-asr/model.int8.onnx" \
    "https://huggingface.co/csukuangfj/sherpa-onnx-omnilingual-asr-1600-languages-300M-ctc-int8-2025-11-12/resolve/main/model.int8.onnx" \
    "e7c4e54ee4c4c47829cc6667d5d00ed8ea7bef1dcfeef0fce766f77752a2726c"
download "$MODELS_DIR/omnilingual-asr/tokens.txt" \
    "https://huggingface.co/csukuangfj/sherpa-onnx-omnilingual-asr-1600-languages-300M-ctc-int8-2025-11-12/resolve/main/tokens.txt" \
    "a7a044c52cb29cbe8b0dc1953e92cefd4ca16b0ed968177b6beab21f9a7d0b31"

# VITS-LJS TTS (model + tokens + lexicon)
download "$MODELS_DIR/vits-ljs/vits-ljs.onnx" \
    "https://huggingface.co/csukuangfj/vits-ljs/resolve/main/vits-ljs.onnx" \
    "5bbd273797a9ecf8d94bd6ec02ad16cb41cbb85f055ad98d528ced3e44c9b31a"
download "$MODELS_DIR/vits-ljs/tokens.txt" \
    "https://huggingface.co/csukuangfj/vits-ljs/resolve/main/tokens.txt" \
    "5fee2c6b238d712287f2ecb08f34a8a8b413bcb7390862ef6fb6fd6f0f8d3a17"
download "$MODELS_DIR/vits-ljs/lexicon.txt" \
    "https://huggingface.co/csukuangfj/vits-ljs/resolve/main/lexicon.txt" \
    "bdccfc6da71c45c48e2e0056fcf0aab760577c5f959f6c1b5eb3e3e916fd5a0e"

# 3. Write model config YAMLs matching the gallery entries' shape. These are
# what the realtime pipeline resolves via LoadModelConfigFileByName.
cat > "$MODELS_DIR/$VAD_MODEL.yaml" <<EOF
name: $VAD_MODEL
backend: sherpa-onnx
type: vad
parameters:
  model: silero-vad/silero-vad.onnx
known_usecases:
  - vad
EOF

cat > "$MODELS_DIR/$MODEL.yaml" <<EOF
name: $MODEL
backend: sherpa-onnx
type: asr
parameters:
  model: omnilingual-asr/model.int8.onnx
options:
  - subtype=omnilingual
known_usecases:
  - transcript
EOF

cat > "$MODELS_DIR/$TTS_MODEL.yaml" <<EOF
name: $TTS_MODEL
backend: sherpa-onnx
parameters:
  model: vits-ljs/vits-ljs.onnx
known_usecases:
  - tts
EOF

# 4. Run the Ginkgo spec. REALTIME_TEST_MODEL=realtime-test-pipeline triggers
# the e2e suite to auto-compose a pipeline YAML from the slot env vars.
export REALTIME_TEST_MODEL=realtime-test-pipeline
export REALTIME_VAD="$VAD_MODEL"
export REALTIME_STT="$MODEL"
export REALTIME_LLM=mock-llm
export REALTIME_TTS="$TTS_MODEL"
export REALTIME_MODELS_PATH="$MODELS_DIR"
export REALTIME_BACKENDS_PATH="$BACKENDS_DIR"

cd "$ROOT"
go test -v -timeout 30m ./tests/e2e/... \
    -ginkgo.focus="Manual audio commit"
