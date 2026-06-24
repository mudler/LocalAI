#!/bin/bash
set -euo pipefail

SCRIPT_DIR=$(dirname "$(realpath "$0")")
WORK_DIR=$(mktemp -d)
trap 'rm -rf "$WORK_DIR"' EXIT

mkdir -p "$WORK_DIR/runtime/common" "$WORK_DIR/runtime/fish-speech-src/fish_speech"
cp "$SCRIPT_DIR/run.sh" "$WORK_DIR/runtime/run.sh"

cat > "$WORK_DIR/runtime/fish-speech-src/fish_speech/inference_engine.py" <<'PY'
IMPORT_OK = True
PY

cat > "$WORK_DIR/runtime/common/libbackend.sh" <<'SH'
startBackend() {
    python3 -c 'from fish_speech.inference_engine import IMPORT_OK; assert IMPORT_OK'
}
SH

bash "$WORK_DIR/runtime/run.sh"

echo "PASS: runtime launcher imports relocated fish-speech source"
