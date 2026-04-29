#!/bin/bash
set -x

backend_dir=$(dirname $0)

if [ -d $backend_dir/common ]; then
    source $backend_dir/common/libbackend.sh
else
    source $backend_dir/../common/libbackend.sh
fi

# Multi-node DP follower mode: when the first arg is `serve`, exec into
# vllm's own CLI instead of LocalAI's backend.py gRPC server. The
# follower speaks ZMQ directly to the head node's vllm ranks — there
# is no LocalAI gRPC on the follower side. Reaches this path via
# `local-ai p2p-worker vllm`.
if [ "${1:-}" = "serve" ]; then
    ensureVenv
    if [ "x${PORTABLE_PYTHON}" == "xtrue" ] || [ -x "$(_portable_python)" ]; then
        _makeVenvPortable --update-pyvenv-cfg
    fi
    if [ -d "${EDIR}/lib" ]; then
        export LD_LIBRARY_PATH="${EDIR}/lib:${LD_LIBRARY_PATH:-}"
    fi
    exec "${EDIR}/venv/bin/vllm" "$@"
fi

startBackend $@
