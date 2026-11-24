#!/usr/bin/env bash
set -euo pipefail

CURDIR=$(dirname "$(realpath "$0")")
BACKEND_DIR="$CURDIR/../../backend/go/stablediffusion-ggml"

build_commands=$(make -Bn -C "$BACKEND_DIR" \
  BUILD_TYPE=metal \
  libgosd-fallback.so)

for expected_flag in \
  -DSD_METAL=ON \
  -DGGML_METAL=ON \
  -DGGML_METAL_EMBED_LIBRARY=ON; do
  if ! grep -Fq -- "$expected_flag" <<<"$build_commands"; then
    echo "FAIL: Darwin Metal build omitted $expected_flag"
    exit 1
  fi
done

echo "PASS: stablediffusion-ggml embeds its Darwin Metal library"
