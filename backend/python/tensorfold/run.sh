#!/bin/bash

backend_dir=$(dirname $0)

# TensorFold compiles its CUDA kernels on first load with torch's extension
# builder. Keep the build cache next to the backend so a restart does not
# recompile, and never let it spawn more nvcc jobs than memory allows
# (each peaks at several GiB).
export TORCH_EXTENSIONS_DIR="${TORCH_EXTENSIONS_DIR:-${backend_dir}/torch_extensions}"
export TENSORFOLD_NO_UPDATE_CHECK=1
if [ -z "${MAX_JOBS:-}" ]; then
    _ncpus=$(nproc 2>/dev/null || echo 1)
    _mem_avail_kb=$(awk '/^MemAvailable:/ {print $2; exit}' /proc/meminfo 2>/dev/null || echo 0)
    _mem_avail_gb=$(( _mem_avail_kb / 1024 / 1024 ))
    if [ "${_mem_avail_gb}" -gt 8 ]; then
        _mem_jobs=$(( (_mem_avail_gb - 4) / 4 ))
    else
        _mem_jobs=1
    fi
    [ "${_mem_jobs}" -lt 1 ] && _mem_jobs=1
    [ "${_mem_jobs}" -gt "${_ncpus}" ] && _mem_jobs=${_ncpus}
    export MAX_JOBS="${_mem_jobs}"
fi

if [ -d $backend_dir/common ]; then
    source $backend_dir/common/libbackend.sh
else
    source $backend_dir/../common/libbackend.sh
fi

startBackend $@
