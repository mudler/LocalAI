#!/bin/bash
# Real-weights realtime speech-to-speech e2e (manual run, never part of CI).
#
# Drives tests/e2e/realtime_s2s_test.go (the "Realtime S2S (real ...)" specs)
# against LFM3-Audio served by the llama-cpp backend, for each shape of the
# gallery entries: {any-to-any, chained pipeline} x {VAD, no VAD}. The any-to-any
# model takes the audio and answers with audio; the pipeline runs transcription,
# chat and speech synthesis as separate stages on the same model.
# The test client generates its input speech with a sherpa-onnx TTS and checks
# the reply audio with a separate sherpa-onnx ASR. The VAD is silero.
#
# Pre-requisites:
#   - `make build-mock-backend` and `make protogen-go` (the suite needs them)
#   - `make docker-build-llama-cpp` -> local-ai-backend:llama-cpp
#   - `make docker-build-sherpa-onnx` -> local-ai-backend:sherpa-onnx
#   Build the llama-cpp image for your hardware first (for example
#   BUILD_TYPE=cublas CUDA_MAJOR_VERSION=13 make docker-build-llama-cpp); a CPU
#   image works too, but a real turn then takes minutes.
# Environment overrides:
#   LFM3_VARIANT     q4 (default, lfm3-audio-2b) or q8 (lfm3-audio-2b-q8)
#   LFM3_GPU_LAYERS  Written as gpu_layers in the model config (default: unset)
#   LLAMA_IMAGE      Default local-ai-backend:llama-cpp
#   SHERPA_IMAGE     Default local-ai-backend:sherpa-onnx
#   WORK_DIR         Staging directory (default: a fresh temporary directory).
#   KEEP_WORK        Non-empty keeps WORK_DIR after the run; point WORK_DIR at it
#                    on the next run to skip the downloads.
#   LFM3_MATRIX      Shapes to run, kind:vad separated by spaces (default: all four:
#                    "any-to-any:vad any-to-any:novad pipeline:vad pipeline:novad").
#   GINKGO_FOCUS     Ginkgo focus regexp (default: "Realtime S2S \(real").
set -euo pipefail

ROOT=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
LLAMA_IMAGE=${LLAMA_IMAGE:-local-ai-backend:llama-cpp}
SHERPA_IMAGE=${SHERPA_IMAGE:-local-ai-backend:sherpa-onnx}
VARIANT=${LFM3_VARIANT:-q4}
VAD_MODEL=silero-vad-sherpa
ASR_MODEL=omnilingual-0.3b-ctc-q8-sherpa
TTS_MODEL=vits-ljs-sherpa

# Hugging Face revision and hashes match the gallery entries lfm3-audio-2b(-q8).
# The VAD and pipeline shapes mirror the -vad, -pipeline and -pipeline-vad entries.
HF=https://huggingface.co/LiquidAI/LFM3-Audio-2B-GGUF/resolve/fcfae9027c00a6f3f872c48a841cfeb77e2b5a1c
MMPROJ=mmproj-LFM3-Audio-2B-Q8_0.gguf
MMPROJ_SHA=26601e904245a85bd8f3cf320a629e897cfc01c1d09369e8b4fd951f13024d3e
case "$VARIANT" in
    q4) S2S_MODEL=lfm3-audio-2b
        GGUF=LFM3-Audio-2B-Q4_K_M.gguf
        GGUF_SHA=9a773289bd321ed879cd3a76545f6aca506e403851a45fb315420aee45625be2 ;;
    q8) S2S_MODEL=lfm3-audio-2b-q8
        GGUF=LFM3-Audio-2B-Q8_0.gguf
        GGUF_SHA=d4e36902ba8f0ee7b333596030a59d650f88ac76660b4d9ba5ff77c3c2258fc0 ;;
    *) echo "LFM3_VARIANT must be q4 or q8" >&2; exit 2 ;;
esac

WORK_DIR=${WORK_DIR:-$(mktemp -d -t localai-lfm3-realtime.XXXXXX)}
CIDS=()
cleanup() {
    for c in "${CIDS[@]}"; do docker rm -f "$c" >/dev/null 2>&1 || true; done
    [[ -z "${KEEP_WORK:-}" ]] && rm -rf "$WORK_DIR"
}
trap cleanup EXIT
echo "WORK_DIR=$WORK_DIR"

BACKENDS_DIR="$WORK_DIR/backends"
MODELS_DIR="$WORK_DIR/models"
mkdir -p "$BACKENDS_DIR" "$MODELS_DIR"

# 1. Extract each backend image rootfs (same approach as run-realtime-sherpa.sh).
extract_backend() {
    local name="$1" image="$2" dir="$BACKENDS_DIR/$1"
    if [[ -x "$dir/run.sh" ]]; then
        echo "cached backend: $dir"
        return
    fi
    mkdir -p "$dir"
    echo "Extracting $image into $dir ..."
    local cid
    cid=$(docker create --entrypoint=/run.sh "$image")
    CIDS+=("$cid")
    docker export "$cid" | tar -xC "$dir" --exclude='dev/*' --exclude='proc/*' --exclude='sys/*'
    docker rm -f "$cid" >/dev/null
    chmod +x "$dir/run.sh" 2>/dev/null || true
}
extract_backend llama-cpp "$LLAMA_IMAGE"
extract_backend sherpa-onnx "$SHERPA_IMAGE"

# 2. Download model files, verifying sha256.
download() {
    local dst="$1" url="$2" sha="$3"
    if [[ -f "$dst" && "$(sha256sum "$dst" | awk '{print $1}')" == "$sha" ]]; then
        echo "cached: $dst"
        return
    fi
    mkdir -p "$(dirname "$dst")"
    echo "downloading: $url -> $dst"
    curl -sSfL "$url" -o "$dst"
    local actual
    actual=$(sha256sum "$dst" | awk '{print $1}')
    if [[ "$actual" != "$sha" ]]; then
        echo "sha256 mismatch for $dst: got $actual, expected $sha" >&2
        exit 1
    fi
}

download "$MODELS_DIR/$GGUF" "$HF/$GGUF" "$GGUF_SHA"
download "$MODELS_DIR/$MMPROJ" "$HF/$MMPROJ" "$MMPROJ_SHA"

download "$MODELS_DIR/silero-vad/silero-vad.onnx" \
    "https://huggingface.co/onnx-community/silero-vad/resolve/main/onnx/model.onnx" \
    "a4a068cd6cf1ea8355b84327595838ca748ec29a25bc91fc82e6c299ccdc5808"
download "$MODELS_DIR/omnilingual-asr/model.int8.onnx" \
    "https://huggingface.co/csukuangfj/sherpa-onnx-omnilingual-asr-1600-languages-300M-ctc-int8-2025-11-12/resolve/main/model.int8.onnx" \
    "e7c4e54ee4c4c47829cc6667d5d00ed8ea7bef1dcfeef0fce766f77752a2726c"
download "$MODELS_DIR/omnilingual-asr/tokens.txt" \
    "https://huggingface.co/csukuangfj/sherpa-onnx-omnilingual-asr-1600-languages-300M-ctc-int8-2025-11-12/resolve/main/tokens.txt" \
    "a7a044c52cb29cbe8b0dc1953e92cefd4ca16b0ed968177b6beab21f9a7d0b31"
download "$MODELS_DIR/vits-ljs/vits-ljs.onnx" \
    "https://huggingface.co/csukuangfj/vits-ljs/resolve/main/vits-ljs.onnx" \
    "5bbd273797a9ecf8d94bd6ec02ad16cb41cbb85f055ad98d528ced3e44c9b31a"
download "$MODELS_DIR/vits-ljs/tokens.txt" \
    "https://huggingface.co/csukuangfj/vits-ljs/resolve/main/tokens.txt" \
    "5fee2c6b238d712287f2ecb08f34a8a8b413bcb7390862ef6fb6fd6f0f8d3a17"
download "$MODELS_DIR/vits-ljs/lexicon.txt" \
    "https://huggingface.co/csukuangfj/vits-ljs/resolve/main/lexicon.txt" \
    "bdccfc6da71c45c48e2e0056fcf0aab760577c5f959f6c1b5eb3e3e916fd5a0e"

# 3. Model configs. One config per shape, each mirroring the gallery overrides
# of the entry with the same name (the gallery names its VAD silero-vad-ggml;
# this script uses the sherpa silero model its extracted backend already has).
#   $S2S_MODEL                 any-to-any, no VAD  (lfm3-audio-2b[-q8])
#   $S2S_MODEL-vad             any-to-any, VAD
#   $S2S_MODEL-pipeline        chained pipeline on the same model, no VAD
#   $S2S_MODEL-pipeline-vad    chained pipeline, VAD
GPU_LINE=""
[[ -n "${LFM3_GPU_LAYERS:-}" ]] && GPU_LINE="gpu_layers: $LFM3_GPU_LAYERS"

write_model_config() {
    local name="$1" kind="$2" vad="$3" usecases pipeline
    if [[ "$kind" == "pipeline" ]]; then
        usecases="  - chat
  - transcript
  - tts"
        pipeline="pipeline:
  llm: $name
  transcription: $name
  tts: $name"
    else
        usecases="  - realtime_audio
  - chat
  - transcript
  - tts"
        pipeline="pipeline:
  any_to_any: true"
    fi
    [[ "$vad" == "1" ]] && pipeline="$pipeline
  vad: $VAD_MODEL"
    cat > "$MODELS_DIR/$name.yaml" <<YAML
name: $name
backend: llama-cpp
context_size: 8192
mmproj: $MMPROJ
$GPU_LINE
parameters:
  model: $GGUF
options:
  - parallel:2
template:
  use_tokenizer_template: true
tts:
  voice: female
known_usecases:
$usecases
$pipeline
YAML
}

write_model_config "$S2S_MODEL" any-to-any 0
write_model_config "$S2S_MODEL-vad" any-to-any 1
write_model_config "$S2S_MODEL-pipeline" pipeline 0
write_model_config "$S2S_MODEL-pipeline-vad" pipeline 1

cat > "$MODELS_DIR/$VAD_MODEL.yaml" <<YAML
name: $VAD_MODEL
backend: sherpa-onnx
type: vad
parameters:
  model: silero-vad/silero-vad.onnx
known_usecases:
  - vad
YAML

cat > "$MODELS_DIR/$ASR_MODEL.yaml" <<YAML
name: $ASR_MODEL
backend: sherpa-onnx
type: asr
parameters:
  model: omnilingual-asr/model.int8.onnx
options:
  - subtype=omnilingual
known_usecases:
  - transcript
YAML

cat > "$MODELS_DIR/$TTS_MODEL.yaml" <<YAML
name: $TTS_MODEL
backend: sherpa-onnx
parameters:
  model: vits-ljs/vits-ljs.onnx
known_usecases:
  - tts
YAML

# 4. Run the real variant of the specs, once per shape of the matrix. A shape is
# kind:vad, for example any-to-any:vad or pipeline:novad.
export REALTIME_S2S_TTS="$TTS_MODEL"
export REALTIME_S2S_ASR="$ASR_MODEL"
export REALTIME_MODELS_PATH="$MODELS_DIR"
export REALTIME_BACKENDS_PATH="$BACKENDS_DIR"

cd "$ROOT"
status=0
for shape in ${LFM3_MATRIX:-any-to-any:vad any-to-any:novad pipeline:vad pipeline:novad}; do
    kind=${shape%%:*}
    name=$S2S_MODEL
    [[ "$kind" == "pipeline" ]] && name="$name-pipeline"
    vad=0
    if [[ "$shape" == *:vad ]]; then vad=1; name="$name-vad"; fi
    [[ "$kind" == "any-to-any" || "$kind" == "pipeline" ]] || { echo "bad shape: $shape" >&2; exit 2; }
    echo "=== $shape -> model $name ==="
    REALTIME_S2S_MODEL="$name" REALTIME_S2S_KIND="$kind" REALTIME_S2S_VAD="$vad" \
    go test -v -count=1 -timeout 120m ./tests/e2e/ \
        -ginkgo.label-filter="Realtime && real-models" \
        -ginkgo.focus="${GINKGO_FOCUS:-Realtime S2S \(real}" \
        -ginkgo.v || status=1
done
exit $status
