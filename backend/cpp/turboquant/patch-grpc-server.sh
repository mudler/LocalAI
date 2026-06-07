#!/bin/bash
# Patch the shared backend/cpp/llama-cpp/grpc-server.cpp *copy* used by the
# turboquant build to account for the gaps between upstream and the fork:
#
#   1. Augment the kv_cache_types[] allow-list so `LoadModel` accepts the
#      fork-specific `turbo2` / `turbo3` / `turbo4` cache types.
#   2. Define LOCALAI_TURBOQUANT_NO_CHECKPOINT_MIN_STEP at the top of the file
#      so the grpc-server option parser skips the two references to
#      common_params::checkpoint_min_step (the default and the option handler).
#      That field does not exist in the fork yet; drop this once it does.
#
# The fork used to lag upstream on the whole common_params_speculative refactor
# (ggml-org/llama.cpp#22397/#22838/#22964), the model_tgt rename (#22838) and
# get_media_marker (#21962), which required a much larger compat shim here
# (flat-field sed renames + a coarse LOCALAI_LEGACY_LLAMA_CPP_SPEC define). The
# fork has since rebased past all of those, so the only remaining gap is
# checkpoint_min_step. If a future bump reintroduces a divergence, add a narrow
# guard in grpc-server.cpp keyed on a fork-specific macro and inject it here
# rather than resurrecting the coarse one.
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

# 2. Define LOCALAI_TURBOQUANT_NO_CHECKPOINT_MIN_STEP at the top of the file so
#    the grpc-server option parser skips the two references to
#    common_params::checkpoint_min_step (the default assignment and the option
#    handler). That field does not exist in the fork yet. Drop this block once
#    the fork rebases past the bump that added checkpoint_min_step.
if grep -q '^#define LOCALAI_TURBOQUANT_NO_CHECKPOINT_MIN_STEP' "$SRC"; then
    echo "==> $SRC already defines LOCALAI_TURBOQUANT_NO_CHECKPOINT_MIN_STEP, skipping"
else
    echo "==> patching $SRC to define LOCALAI_TURBOQUANT_NO_CHECKPOINT_MIN_STEP at the top"
    # Insert the define before the very first `#include` so it precedes the
    # checkpoint_min_step references.
    awk '
        !done && /^#include/ {
            print "#define LOCALAI_TURBOQUANT_NO_CHECKPOINT_MIN_STEP 1"
            print "// ^ injected by backend/cpp/turboquant/patch-grpc-server.sh"
            print ""
            done = 1
        }
        { print }
        END {
            if (!done) {
                print "patch-grpc-server.sh: no #include anchor found to insert LOCALAI_TURBOQUANT_NO_CHECKPOINT_MIN_STEP" > "/dev/stderr"
                exit 1
            }
        }
    ' "$SRC" > "$SRC.tmp"
    mv "$SRC.tmp" "$SRC"
    echo "==> LOCALAI_TURBOQUANT_NO_CHECKPOINT_MIN_STEP define OK"
fi

echo "==> all patches applied"
