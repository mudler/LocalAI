#!/bin/bash
set -e

CURDIR=$(dirname "$(realpath $0)")

echo "Running depth-anything-cpp backend tests..."

# Test model from the mudler/depth-anything.cpp-gguf HuggingFace repo. The small
# (vits) f32 GGUF is the lightest backbone (~131 MB), so it keeps the download
# cheap. It is resumed with `curl -C -` and skipped entirely if already present.
DEPTHANYTHING_MODEL_DIR="${DEPTHANYTHING_MODEL_DIR:-$CURDIR/test-models}"

DEPTHANYTHING_MODEL_FILE="${DEPTHANYTHING_MODEL_FILE:-depth-anything-small-f32.gguf}"
DEPTHANYTHING_MODEL_URL="${DEPTHANYTHING_MODEL_URL:-https://huggingface.co/mudler/depth-anything.cpp-gguf/resolve/main/depth-anything-small-f32.gguf}"

mkdir -p "$DEPTHANYTHING_MODEL_DIR"

if [ ! -f "$DEPTHANYTHING_MODEL_DIR/$DEPTHANYTHING_MODEL_FILE" ]; then
    echo "Downloading depth-anything small f32 model (~131 MB)..."
    # -C - resumes a partial download so an interrupted run doesn't restart from 0.
    curl -L -C - -o "$DEPTHANYTHING_MODEL_DIR/$DEPTHANYTHING_MODEL_FILE" "$DEPTHANYTHING_MODEL_URL" --progress-bar
fi

# Use a real photo (people + cars) from the upstream rf-detr.cpp repo (~46 KB).
# Depth estimation needs real content; a synthetic image would be degenerate.
TEST_IMAGE_DIR="$CURDIR/test-data"
TEST_IMAGE_FILE="$TEST_IMAGE_DIR/test.jpg"
TEST_IMAGE_URL="${TEST_IMAGE_URL:-https://raw.githubusercontent.com/localai-org/rf-detr.cpp/main/tests/fixtures/ci/test_image.jpg}"

mkdir -p "$TEST_IMAGE_DIR"
if [ ! -f "$TEST_IMAGE_FILE" ]; then
    echo "Downloading test image..."
    curl -L -o "$TEST_IMAGE_FILE" "$TEST_IMAGE_URL" --progress-bar
fi

echo "depth-anything-cpp test setup complete."
echo "  model:      $DEPTHANYTHING_MODEL_DIR/$DEPTHANYTHING_MODEL_FILE"
echo "  test image: $TEST_IMAGE_FILE"

# Run the Go smoke test: spawns the backend binary on a free port, calls
# LoadModel + Predict via gRPC against the downloaded GGUF + image.
echo ""
echo "Running Go smoke test..."
cd "$CURDIR"
go test -v -timeout 30m ./...
