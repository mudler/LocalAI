#!/bin/bash
# Augment the shared backend/cpp/llama-cpp/grpc-server.cpp allow-list of KV-cache
# types so the gRPC `LoadModel` call accepts the TurboQuant-specific
# `turbo2` / `turbo3` / `turbo4` cache types.
#
# We do this on the *copy* sitting in turboquant-<flavor>-build/, never on the
# original under backend/cpp/llama-cpp/, so the stock llama-cpp build keeps
# compiling against vanilla upstream which does not know about GGML_TYPE_TURBO*.
#
# Idempotent: skips the insertion if the marker is already present (so re-runs
# of the same build dir don't double-insert).

set -euo pipefail

if [[ $# -ne 1 ]]; then
    echo "usage: $0 <grpc-server.cpp>" >&2
    exit 2
fi

SRC=$1

if [[ ! -f "$SRC" ]]; then
    echo "grpc-server.cpp not found at $SRC" >&2
    exit 2
fi

if grep -q 'GGML_TYPE_TURBO2_0' "$SRC"; then
    echo "==> $SRC already has TurboQuant cache types, skipping"
    exit 0
fi

echo "==> patching $SRC to allow turbo2/turbo3/turbo4 KV-cache types"

# Insert the three TURBO entries right after the existing GGML_TYPE_Q5_1 line.
# Using a here-doc for readability; sed's `a\` is brittle with multi-line on macOS.
python3 - "$SRC" <<'PY'
import sys
path = sys.argv[1]
with open(path) as f:
    src = f.read()

needle = "    GGML_TYPE_Q5_1,\n"
addition = (
    "    GGML_TYPE_Q5_1,\n"
    "    // turboquant fork extras — accepted only when building the\n"
    "    // backend/cpp/turboquant variant (patched in by patch-grpc-server.sh)\n"
    "    GGML_TYPE_TURBO2_0,\n"
    "    GGML_TYPE_TURBO3_0,\n"
    "    GGML_TYPE_TURBO4_0,\n"
)

if needle not in src:
    sys.exit(f"could not find anchor '{needle.strip()}' in {path}")

# Replace only the first occurrence — there's exactly one kv_cache_types[] array.
with open(path, "w") as f:
    f.write(src.replace(needle, addition, 1))
PY

echo "==> patched OK"
