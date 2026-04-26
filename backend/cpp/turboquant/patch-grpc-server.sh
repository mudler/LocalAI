#!/bin/bash
# Patch the shared backend/cpp/llama-cpp/grpc-server.cpp *copy* used by the
# turboquant build to account for two gaps between upstream and the fork:
#
#   1. Augment the kv_cache_types[] allow-list so `LoadModel` accepts the
#      fork-specific `turbo2` / `turbo3` / `turbo4` cache types.
#   2. Replace `get_media_marker()` (added upstream in ggml-org/llama.cpp#21962,
#      server-side random per-instance marker) with the legacy "<__media__>"
#      literal. The fork branched before that PR, so server-common.cpp has no
#      get_media_marker symbol. The fork's mtmd_default_marker() still returns
#      "<__media__>", and Go-side tooling falls back to that sentinel when the
#      backend does not expose media_marker, so substituting the literal keeps
#      behavior identical on the turboquant path.
#
# We patch the *copy* sitting in turboquant-<flavor>-build/, never the original
# under backend/cpp/llama-cpp/, so the stock llama-cpp build keeps compiling
# against vanilla upstream.
#
# Idempotent: skips each insertion if its marker is already present (so re-runs
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

    echo "==> KV allow-list patch OK"
fi

if grep -q 'get_media_marker()' "$SRC"; then
    echo "==> patching $SRC to replace get_media_marker() with legacy \"<__media__>\" literal"
    # Only one call site today (ModelMetadata), but replace all occurrences to
    # stay robust if upstream adds more. Use a temp file to avoid relying on
    # sed -i portability (the builder image uses GNU sed, but keeping this
    # consistent with the awk block above).
    sed 's/get_media_marker()/"<__media__>"/g' "$SRC" > "$SRC.tmp"
    mv "$SRC.tmp" "$SRC"
    echo "==> get_media_marker() substitution OK"
else
    echo "==> $SRC has no get_media_marker() call, skipping media-marker patch"
fi

echo "==> all patches applied"
