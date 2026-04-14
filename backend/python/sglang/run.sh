#!/bin/bash

backend_dir=$(dirname $(realpath $0))

if [ -d $backend_dir/common ]; then
    source $backend_dir/common/libbackend.sh
else
    source $backend_dir/../common/libbackend.sh
fi

# sglang's CPU kernel references LLVM OpenMP (__kmpc_*) symbols that are
# not declared in its NEEDED list — they get resolved through LD_PRELOAD
# of libiomp5.so in sglang's own docker/xeon.Dockerfile. Do the same here.
# Harmless on GPU builds where libiomp5.so is absent.
if [ -f "${backend_dir}/lib/libiomp5.so" ]; then
    if [ -n "${LD_PRELOAD:-}" ]; then
        export LD_PRELOAD="${backend_dir}/lib/libiomp5.so:${LD_PRELOAD}"
    else
        export LD_PRELOAD="${backend_dir}/lib/libiomp5.so"
    fi
fi

# sglang CPU engine requires this env var to switch to the CPU backend.
# No-op on GPU builds. See docker/xeon.Dockerfile in sglang upstream.
if [ -f "${backend_dir}/lib/libiomp5.so" ]; then
    export SGLANG_USE_CPU_ENGINE=1
fi

startBackend $@
