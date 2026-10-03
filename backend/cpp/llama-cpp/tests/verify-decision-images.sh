#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
set -euo pipefail
root=$(git rev-parse --show-toplevel)
b="$root/backend/cpp/llama-cpp"
out="$b/llama.cpp/build-image-tests"
mkdir -p "$out"
${CXX:-g++} -std=c++17 -Wall -Wextra -I"$b" -I"$b/llama.cpp/vendor" "$b/tests/decision-images.cpp" -lz -ljpeg -o "$out/decision-images"
python3 "$b/tests/image-fixtures.py" "$out/fixtures.json"
"$out/decision-images" "$out/fixtures.json"
# Keep the native boundary in lockstep with canonical Go limits.
python3 - "$root" <<'PY'
import pathlib, re, sys
root=pathlib.Path(sys.argv[1])
go=(root/'core/systemone/images.go').read_text()
cpp=(root/'backend/cpp/llama-cpp/decision_images.h').read_text()
for g,c in [('MaxImages','max_images'),('MaxImageDecodedBytes','decoded_bytes'),('MaxImageEncodedBytes','encoded_bytes'),('MaxImageBodyBytes','body_bytes'),('MaxImageDimension','max_dimension'),('MaxImagePixels','max_pixels'),('MaxResponseBytes','text_bytes')]:
    gv=re.search(r'\b'+g+r'\s*=\s*([^\n]+)',go)[1]
    cv=re.search(r'\b'+c+r'\s*=\s*([^;]+)',cpp)[1]
    assert eval(gv)==eval(cv),(g,c)
print('Go/native limit parity PASS')
PY
