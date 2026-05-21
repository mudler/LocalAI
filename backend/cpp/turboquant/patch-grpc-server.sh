#!/bin/bash
# Patch the shared backend/cpp/llama-cpp/grpc-server.cpp *copy* used by the
# turboquant build:
#
#   1. Augment the kv_cache_types[] allow-list so `LoadModel` accepts the
#      fork-specific `turbo2` / `turbo3` / `turbo4` cache types.
#
# Historical context: this script used to also paper over API gaps between the
# fork and upstream (flat vs nested `common_params_speculative`, missing
# `get_media_marker()`, `ctx_server.impl->model` vs `model_tgt`, and a
# LOCALAI_LEGACY_LLAMA_CPP_SPEC compile gate). As of TURBOQUANT_VERSION
# 4c1c3ac0 the fork has rebased past ggml-org/llama.cpp#21962, #22397 and
# #22838, so the shared grpc-server.cpp compiles unmodified against the fork.
# Only the fork-specific KV-cache enum entries remain.
#
# We patch the *copy* sitting in turboquant-<flavor>-build/, never the original
# under backend/cpp/llama-cpp/, so the stock llama-cpp build stays compiling
# against vanilla upstream.
#
# Idempotent: skips the insertion if its marker is already present (so re-runs
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
    echo "==> $SRC already has TurboQuant cache types, skipping KV allow-list patch"
else
    echo "==> patching $SRC to allow turbo2/turbo3/turbo4 KV-cache types"

    # Insert the three TURBO entries right after the first `    GGML_TYPE_Q5_1,`
    # line (the kv_cache_types[] allow-list). Using awk because the builder image
    # does not ship python3, and GNU sed's multi-line `a\` quoting is awkward.
    awk '
        /^    GGML_TYPE_Q5_1,$/ && !done {
            print
            print "    // turboquant fork extras - added by patch-grpc-server.sh"
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

    echo "==> KV allow-list patch OK"
fi

echo "==> all patches applied"
