#!/bin/bash
set -e

CURDIR=$(dirname "$(realpath "$0")")
cd "$CURDIR"

echo "Running voice-detect backend tests..."

# The pure-Go parsing specs always run. The embed/verify/analyze smoke specs run
# only when a model + WAV are provided via VOICEDETECT_BACKEND_TEST_MODEL and
# VOICEDETECT_BACKEND_TEST_WAV; otherwise they auto-skip.
LD_LIBRARY_PATH="$CURDIR:${LD_LIBRARY_PATH:-}" go test -v -timeout 1200s .

echo "voice-detect tests completed."
