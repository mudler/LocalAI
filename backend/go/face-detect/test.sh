#!/bin/bash
set -e

CURDIR=$(dirname "$(realpath "$0")")
cd "$CURDIR"

echo "Running face-detect backend tests..."

# The pure-Go parsing specs always run. The embed/detect/verify/analyze smoke
# specs run only when a model + image are provided via
# FACEDETECT_BACKEND_TEST_MODEL and FACEDETECT_BACKEND_TEST_IMAGE; otherwise they
# auto-skip.
LD_LIBRARY_PATH="$CURDIR:${LD_LIBRARY_PATH:-}" go test -v -timeout 1200s .

echo "face-detect tests completed."
