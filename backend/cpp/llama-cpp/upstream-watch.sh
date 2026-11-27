#!/bin/bash
# SPDX-License-Identifier: MIT
#
# Checks a llama.cpp checkout for the upstream changes that make parts of
# patches/0002-paged-serving.patch redundant (see "Upstream watch" in
# PATCHES.md). Prints one line per item; "LANDED" means: read PATCHES.md and
# re-benchmark before the bump merges. Exit status is 0 either way.
#
# usage: upstream-watch.sh [llama.cpp dir]   (default: ./llama.cpp)
set -u
D=${1:-llama.cpp}
if [ ! -f "$D/common/common.h" ]; then
    echo "no llama.cpp checkout at $D" >&2
    exit 1
fi

probe() { # name, then a grep -E pattern and the files to search
    local name=$1 pattern=$2
    shift 2
    if grep -qE "$pattern" "$@" 2>/dev/null; then
        echo "LANDED   $name"
    else
        echo "absent   $name"
    fi
}

probe "#30970 --slot-attach (shared prefix; H2, victim choice)" 'slot_attach' "$D/common/common.h"
probe "#32358 shared prefix across slots (H2, victim choice)" 'copy_prefix_to|shares_cells' "$D"/tools/server/*.cpp "$D"/tools/server/*.h
probe "#10718 chunked prefill (H4, prefill_budget)" 'n_chunk_pp|prefill_budget|max_prefill' "$D/common/arg.cpp"
probe "#32219 request priority (drop patches/0003)" 'int priority' "$D/tools/server/server-task.h"
probe "#32520 quantized-KV kv_rows on CUDA (docs, q8_0 arm)" 'kv_rows' "$D"/ggml/src/ggml-cuda/fattn-vec*
if grep -q "try to terminate only the largest active slot" "$D/tools/server/server-context.cpp" 2>/dev/null; then
    echo "absent   upstream preemption (H5, H6): the decode() TODO is still there"
else
    echo "LANDED   upstream preemption (H5, H6)? the decode() TODO is gone, check server-context.cpp"
fi
exit 0
