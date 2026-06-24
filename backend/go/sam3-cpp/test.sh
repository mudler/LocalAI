#!/bin/bash
set -e

CURDIR=$(dirname "$(realpath $0)")

echo "Running sam3-cpp backend tests..."

# The test requires a SAM model in GGML format.
# Uses EdgeTAM Q4_0 (~15MB) for fast CI testing.
SAM3_MODEL_DIR="${SAM3_MODEL_DIR:-$CURDIR/test-models}"
SAM3_MODEL_FILE="${SAM3_MODEL_FILE:-edgetam_q4_0.ggml}"
SAM3_MODEL_URL="${SAM3_MODEL_URL:-https://huggingface.co/PABannier/sam3.cpp/resolve/main/edgetam_q4_0.ggml}"

# Download model if not present
if [ ! -f "$SAM3_MODEL_DIR/$SAM3_MODEL_FILE" ]; then
    echo "Downloading EdgeTAM Q4_0 model for testing..."
    mkdir -p "$SAM3_MODEL_DIR"
    curl -L -o "$SAM3_MODEL_DIR/$SAM3_MODEL_FILE" "$SAM3_MODEL_URL" --progress-bar
    echo "Model downloaded."
fi

# Create a test image (4x4 red pixel PNG) using base64
# This is a minimal valid PNG for testing the pipeline
TEST_IMAGE_DIR="$CURDIR/test-data"
mkdir -p "$TEST_IMAGE_DIR"

# Generate a simple test image using Python if available, otherwise use a pre-encoded one
if command -v python3 &> /dev/null; then
    python3 -c "
import struct, zlib, base64
def create_png(width, height, r, g, b):
    raw = b''
    for y in range(height):
        raw += b'\x00'  # filter byte
        for x in range(width):
            raw += bytes([r, g, b])
    def chunk(ctype, data):
        c = ctype + data
        return struct.pack('>I', len(data)) + c + struct.pack('>I', zlib.crc32(c) & 0xffffffff)
    ihdr = struct.pack('>IIBBBBB', width, height, 8, 2, 0, 0, 0)
    return b'\x89PNG\r\n\x1a\n' + chunk(b'IHDR', ihdr) + chunk(b'IDAT', zlib.compress(raw)) + chunk(b'IEND', b'')
with open('$TEST_IMAGE_DIR/test.png', 'wb') as f:
    f.write(create_png(64, 64, 255, 0, 0))
"
    echo "Test image created."
fi

echo "sam3-cpp test setup complete."
echo "Model: $SAM3_MODEL_DIR/$SAM3_MODEL_FILE"
echo "Note: Full integration tests run via the LocalAI test-extra target."
