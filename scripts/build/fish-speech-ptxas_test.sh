#!/bin/bash
set -euo pipefail

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

REPO_ROOT=$(dirname "$(dirname "$(dirname "$(realpath "$0")")")")
BACKEND_DIR="$WORK/fish-speech"
mkdir -p "$BACKEND_DIR/common" "$WORK/cuda/bin"
cp "$REPO_ROOT/backend/python/fish-speech/run.sh" "$BACKEND_DIR/run.sh"

cat > "$BACKEND_DIR/common/libbackend.sh" <<'LIBBACKEND'
startBackend() {
    printf '%s\n' "${TRITON_PTXAS_PATH:-}"
}
LIBBACKEND

fail() {
    echo "FAIL: $*"
    exit 1
}

touch "$WORK/cuda/bin/ptxas"
chmod +x "$WORK/cuda/bin/ptxas"

got=$(CUDA_HOME="$WORK/cuda" bash "$BACKEND_DIR/run.sh")
[ "$got" = "$WORK/cuda/bin/ptxas" ] || \
    fail "expected toolkit ptxas, got '$got'"

got=$(CUDA_HOME="$WORK/cuda" TRITON_PTXAS_PATH=/custom/ptxas \
    bash "$BACKEND_DIR/run.sh")
[ "$got" = "/custom/ptxas" ] || \
    fail "explicit TRITON_PTXAS_PATH was overwritten with '$got'"

chmod -x "$WORK/cuda/bin/ptxas"
got=$(CUDA_HOME="$WORK/cuda" bash "$BACKEND_DIR/run.sh")
[ -z "$got" ] || fail "non-executable ptxas was selected as '$got'"

echo "PASS: fish-speech selects a usable toolkit ptxas"
