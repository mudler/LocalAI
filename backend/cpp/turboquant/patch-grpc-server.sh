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

# Insert the three TURBO entries right after the first `    GGML_TYPE_Q5_1,`
# line (the kv_cache_types[] allow-list). Using awk because the builder image
# does not ship python3, and GNU sed's multi-line `a\` quoting is awkward.
awk '
    /^    GGML_TYPE_Q5_1,$/ && !done {
        print
        print "    // turboquant fork extras — added by patch-grpc-server.sh"
        print "    GGML_TYPE_TURBO2_0,"
        print "    GGML_TYPE_TURBO3_0,"
        print "    GGML_TYPE_TURBO4_0,"
        done = 1
        next
    }
    { print }
    END {
        if (!done) {
            print "patch-grpc-server.sh: anchor `    GGML_TYPE_Q5_1,` not found" > "/dev/stderr"
            exit 1
        }
    }
' "$SRC" > "$SRC.tmp"
mv "$SRC.tmp" "$SRC"

echo "==> patched OK"
