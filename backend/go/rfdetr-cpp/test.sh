#!/bin/bash
set -e

CURDIR=$(dirname "$(realpath $0)")

echo "Running rfdetr-cpp backend tests..."

# Test models from the mudler/rfdetr-cpp-* HuggingFace repos. Both the
# detection (nano-q8_0, ~36 MB) and segmentation (seg-nano-q8_0, ~40 MB)
# variants are downloaded so the Go smoke test exercises both code paths.
RFDETR_MODEL_DIR="${RFDETR_MODEL_DIR:-$CURDIR/test-models}"

RFDETR_DET_FILE="${RFDETR_DET_FILE:-rfdetr-nano-q8_0.gguf}"
RFDETR_DET_URL="${RFDETR_DET_URL:-https://huggingface.co/mudler/rfdetr-cpp-nano/resolve/main/rfdetr-nano-q8_0.gguf}"

RFDETR_SEG_FILE="${RFDETR_SEG_FILE:-rfdetr-seg-nano-q8_0.gguf}"
RFDETR_SEG_URL="${RFDETR_SEG_URL:-https://huggingface.co/mudler/rfdetr-cpp-seg-nano/resolve/main/rfdetr-seg-nano-q8_0.gguf}"

mkdir -p "$RFDETR_MODEL_DIR"

if [ ! -f "$RFDETR_MODEL_DIR/$RFDETR_DET_FILE" ]; then
    echo "Downloading rfdetr nano-q8_0 detection model..."
    curl -L -o "$RFDETR_MODEL_DIR/$RFDETR_DET_FILE" "$RFDETR_DET_URL" --progress-bar
fi

if [ ! -f "$RFDETR_MODEL_DIR/$RFDETR_SEG_FILE" ]; then
    echo "Downloading rfdetr seg-nano-q8_0 segmentation model..."
    curl -L -o "$RFDETR_MODEL_DIR/$RFDETR_SEG_FILE" "$RFDETR_SEG_URL" --progress-bar
fi

# Use a real COCO test image from the upstream rf-detr.cpp repo (~46 KB).
# A synthetic 64x64 red PNG was too synthetic to elicit detections from a
# real model — the smoke test would always trivially pass with zero
# detections.
TEST_IMAGE_DIR="$CURDIR/test-data"
TEST_IMAGE_FILE="$TEST_IMAGE_DIR/test.jpg"
TEST_IMAGE_URL="${TEST_IMAGE_URL:-https://raw.githubusercontent.com/mudler/rf-detr.cpp/main/tests/fixtures/ci/test_image.jpg}"

mkdir -p "$TEST_IMAGE_DIR"
if [ ! -f "$TEST_IMAGE_FILE" ]; then
    echo "Downloading COCO test image..."
    curl -L -o "$TEST_IMAGE_FILE" "$TEST_IMAGE_URL" --progress-bar
fi

echo "rfdetr-cpp test setup complete."
echo "  detection model:    $RFDETR_MODEL_DIR/$RFDETR_DET_FILE"
echo "  segmentation model: $RFDETR_MODEL_DIR/$RFDETR_SEG_FILE"
echo "  test image:         $TEST_IMAGE_FILE"

# Run the Go smoke test: spawns the backend binary on a free port, calls
# LoadModel + Detect via gRPC for both detection and segmentation models.
echo ""
echo "Running Go smoke test..."
cd "$CURDIR"
go test -v -timeout 5m ./...
