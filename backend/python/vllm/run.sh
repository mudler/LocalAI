#!/bin/bash

backend_dir=$(dirname $0)

# FlashInfer / PyTorch JIT-compile CUDA kernels at first model load (e.g.
# the NVFP4 GEMM kernel for Blackwell SM120). Each concurrent nvcc /
# cudafe++ peaks at multiple GiB during compilation; ninja's default
# (-j$(nproc)+2) OOM-kills on memory-tight hosts but underutilises
# 100-core / 1 TB boxes. Default MAX_JOBS to the smaller of the CPU count
# and an available-memory budget at ~4 GiB per job. User-set MAX_JOBS in
# the environment wins.
# https://github.com/vllm-project/vllm/issues/20079
if [ -z "${MAX_JOBS:-}" ]; then
    _ncpus=$(nproc 2>/dev/null || echo 1)
    _mem_avail_kb=$(awk '/^MemAvailable:/ {print $2; exit}' /proc/meminfo 2>/dev/null || echo 0)
    _mem_avail_gb=$(( _mem_avail_kb / 1024 / 1024 ))
    # Reserve ~4 GiB for the rest of the system; budget ~4 GiB per job.
    if [ "${_mem_avail_gb}" -gt 8 ]; then
        _mem_jobs=$(( (_mem_avail_gb - 4) / 4 ))
    else
        _mem_jobs=1
    fi
    [ "${_mem_jobs}" -lt 1 ] && _mem_jobs=1
    [ "${_mem_jobs}" -gt "${_ncpus}" ] && _mem_jobs=${_ncpus}
    export MAX_JOBS="${_mem_jobs}"
fi
export NVCC_THREADS="${NVCC_THREADS:-2}"

if [ -d $backend_dir/common ]; then
    source $backend_dir/common/libbackend.sh
else
    source $backend_dir/../common/libbackend.sh
fi

startBackend $@