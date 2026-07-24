#!/bin/bash
# Mark a copied gRPC server as targeting a llama.cpp fork that does not carry
# LocalAI's slot-based Score patches. The RPC remains present in the shared
# protobuf service, but responds with UNIMPLEMENTED instead of referencing
# server task types and common_params fields absent from those forks.

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

if grep -q '^#define LOCALAI_LLAMA_CPP_NO_SCORE_TASK' "$SRC"; then
    echo "==> $SRC already disables the LocalAI score task, skipping"
    exit 0
fi

awk '
    !done && /^#include/ {
        print "#define LOCALAI_LLAMA_CPP_NO_SCORE_TASK 1"
        print "// ^ injected by disable-score-task.sh for an unpatched llama.cpp fork"
        print ""
        done = 1
    }
    { print }
    END {
        if (!done) {
            print "disable-score-task.sh: no #include anchor found" > "/dev/stderr"
            exit 1
        }
    }
' "$SRC" > "$SRC.tmp"
mv "$SRC.tmp" "$SRC"

echo "==> LocalAI score task disabled in $SRC"
