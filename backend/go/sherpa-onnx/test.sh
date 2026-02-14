#!/bin/bash
set -e

CURDIR=$(dirname "$(realpath $0)")

echo "Running sherpa-onnx backend tests..."

cd "$CURDIR"

# TTS model
export SHERPA_ONNX_MODEL_DIR="${SHERPA_ONNX_MODEL_DIR:-./vits-ljs}"

if [ ! -d "$SHERPA_ONNX_MODEL_DIR" ]; then
    echo "Downloading vits-ljs test model..."
    curl -L https://github.com/k2-fsa/sherpa-onnx/releases/download/tts-models/vits-ljs.tar.bz2 \
      -o vits-ljs.tar.bz2 --progress-bar
    tar xf vits-ljs.tar.bz2
    rm vits-ljs.tar.bz2
fi

# ASR model (whisper tiny.en int8 — ~120MB)
export SHERPA_ONNX_ASR_MODEL_DIR="${SHERPA_ONNX_ASR_MODEL_DIR:-./sherpa-onnx-whisper-tiny.en}"

if [ ! -d "$SHERPA_ONNX_ASR_MODEL_DIR" ]; then
    echo "Downloading whisper tiny.en ASR model..."
    curl -L https://github.com/k2-fsa/sherpa-onnx/releases/download/asr-models/sherpa-onnx-whisper-tiny.en.tar.bz2 \
      -o sherpa-onnx-whisper-tiny.en.tar.bz2 --progress-bar
    tar xf sherpa-onnx-whisper-tiny.en.tar.bz2
    rm sherpa-onnx-whisper-tiny.en.tar.bz2
fi

# Run Go tests, filtering out upstream sources
PACKAGES=$(go list ./... | grep -v /sources/)
go test -v -timeout 300s $PACKAGES

echo "All sherpa-onnx tests passed."
