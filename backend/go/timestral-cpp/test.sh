#!/bin/bash
set -e

CURDIR=$(dirname "$(realpath "$0")")
cd "$CURDIR"

echo "Running timestral-cpp backend tests..."

# The pure-Go specs always run. The integration specs run when libtimestral.so
# is built in this directory (make libtimestral.so) or TIMESTRAL_LIBRARY names
# one. They use the 5M F32 model (22 MB) from the public GGUF repository. The
# download is checked against a fixed SHA-256, taken from the repository's
# SHA256SUMS file, so a changed or truncated file stops the run.
TIMESTRAL_MODEL_DIR="${TIMESTRAL_MODEL_DIR:-$CURDIR/test-models}"
TIMESTRAL_MODEL_FILE="timestral-5m-f32.gguf"
TIMESTRAL_MODEL_URL="${TIMESTRAL_MODEL_URL:-https://huggingface.co/mudler/timestral-cpp-gguf/resolve/main/5M/timestral-5m-f32.gguf}"
TIMESTRAL_MODEL_SHA256="c320276d8db95fb72fe5f4e043147e29142a1bf197c1b45daf6736780f6736ed"

if [ -n "${TIMESTRAL_LIBRARY:-}" ] || ls "$CURDIR"/libtimestral.so* >/dev/null 2>&1; then
	mkdir -p "$TIMESTRAL_MODEL_DIR"
	MODEL="$TIMESTRAL_MODEL_DIR/$TIMESTRAL_MODEL_FILE"
	if [ ! -f "$MODEL" ]; then
		echo "Downloading $TIMESTRAL_MODEL_FILE (22 MB)..."
		# -C - resumes a partial download.
		curl -fL -C - -o "$MODEL" "$TIMESTRAL_MODEL_URL" --progress-bar
	fi
	echo "$TIMESTRAL_MODEL_SHA256  $MODEL" | sha256sum -c -
	export TIMESTRAL_BACKEND_TEST_MODEL="$MODEL"
else
	echo "libtimestral.so not found: the integration specs will skip."
fi

LD_LIBRARY_PATH="$CURDIR:${LD_LIBRARY_PATH:-}" go test -v -count=1 -timeout 20m .

echo "timestral-cpp tests completed."
