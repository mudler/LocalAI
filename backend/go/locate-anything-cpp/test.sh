#!/bin/bash
set -e

CURDIR=$(dirname "$(realpath $0)")

echo "Running locate-anything-cpp backend tests..."

# Test model from the mudler/locate-anything.cpp-gguf HuggingFace repo. This is
# the q8_0 quantization of nvidia/LocateAnything-3B (~6.3 GB), so the download
# is the slow step. It is resumed with `curl -C -` and skipped entirely if the
# file is already present.
LOCATEANYTHING_MODEL_DIR="${LOCATEANYTHING_MODEL_DIR:-$CURDIR/test-models}"

LOCATEANYTHING_MODEL_FILE="${LOCATEANYTHING_MODEL_FILE:-locate-anything-q8_0.gguf}"
LOCATEANYTHING_MODEL_URL="${LOCATEANYTHING_MODEL_URL:-https://huggingface.co/mudler/locate-anything.cpp-gguf/resolve/main/locate-anything-q8_0.gguf}"

mkdir -p "$LOCATEANYTHING_MODEL_DIR"

if [ ! -f "$LOCATEANYTHING_MODEL_DIR/$LOCATEANYTHING_MODEL_FILE" ]; then
    echo "Downloading locate-anything q8_0 model (~6.3 GB, this is slow)..."
    # -C - resumes a partial download so an interrupted run doesn't restart from 0.
    curl -L -C - -o "$LOCATEANYTHING_MODEL_DIR/$LOCATEANYTHING_MODEL_FILE" "$LOCATEANYTHING_MODEL_URL" --progress-bar
fi

# Use a real COCO test image (people + cars) from the upstream rf-detr.cpp repo
# (~46 KB). Open-vocabulary detection needs real content to locate, so a
# synthetic image would trivially yield zero detections.
TEST_IMAGE_DIR="$CURDIR/test-data"
TEST_IMAGE_FILE="$TEST_IMAGE_DIR/test.jpg"
TEST_IMAGE_URL="${TEST_IMAGE_URL:-https://raw.githubusercontent.com/localai-org/rf-detr.cpp/main/tests/fixtures/ci/test_image.jpg}"

mkdir -p "$TEST_IMAGE_DIR"
if [ ! -f "$TEST_IMAGE_FILE" ]; then
    echo "Downloading COCO test image..."
    curl -L -o "$TEST_IMAGE_FILE" "$TEST_IMAGE_URL" --progress-bar
fi

echo "locate-anything-cpp test setup complete."
echo "  model:      $LOCATEANYTHING_MODEL_DIR/$LOCATEANYTHING_MODEL_FILE"
echo "  test image: $TEST_IMAGE_FILE"

# Run the Go smoke test: spawns the backend binary on a free port, calls
# LoadModel + Detect via gRPC against the downloaded GGUF + COCO image.
echo ""
echo "Running Go smoke test..."
cd "$CURDIR"
go test -v -timeout 30m ./...
