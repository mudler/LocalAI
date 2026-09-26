#!/bin/bash
set -x

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

# CPU profile: torch._inductor's ISA-probe (run at vllm engine
# startup, even with enforce_eager=True) shells out to g++. The
# LocalAI runtime image and the FROM-scratch backend image both
# omit a compiler; package.sh bundles one into ${EDIR}/toolchain
# along with wrapper scripts at toolchain/usr/bin that already pass
# --sysroot and -B. So all run.sh has to do is put the wrapper on
# PATH and expose the toolchain's shared libs (libisl, libmpc, libbfd,
# ...) to ld.so. No-op for other profiles -- the dir doesn't exist.
if [ -d "${EDIR}/toolchain/usr/bin" ]; then
    export PATH="${EDIR}/toolchain/usr/bin:${PATH}"
    _libpath="${EDIR}/toolchain/usr/lib/x86_64-linux-gnu"
    export LD_LIBRARY_PATH="${_libpath}${LD_LIBRARY_PATH:+:${LD_LIBRARY_PATH}}"
fi

# ROCm (hipblas) profile: Triton JIT-compiles its launcher/util C modules at first
# inference (e.g. the top-k/top-p sampling kernel), and torch._inductor's ISA probe
# shells out to a C compiler too. The LocalAI runtime image ships hipcc but not
# cc/gcc, and the FROM-scratch backend omits them as well -- so Triton dies with
# "Failed to find C compiler". rocm-sdk-devel (installed for the gfx1151 build)
# already bundles amdclang, so just put it on PATH and expose it as CC/CXX. Mirrors
# the CPU-toolchain block above; no-op for other profiles (the dir doesn't exist).
_rocm_sdk_bin="$(ls -d ${EDIR}/venv/lib/python*/site-packages/_rocm_sdk_devel/bin 2>/dev/null | head -1)"
if [ -n "${_rocm_sdk_bin}" ] && [ -x "${_rocm_sdk_bin}/amdclang" ]; then
    export PATH="${_rocm_sdk_bin}:${PATH}"
    export CC="${CC:-${_rocm_sdk_bin}/amdclang}"
    export CXX="${CXX:-${_rocm_sdk_bin}/amdclang++}"
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
    # Run the vllm console script through the venv python rather than
    # exec-ing it directly. uv bakes an absolute shebang at install time
    # (e.g. `#!/vllm/venv/bin/python3` from the build image) which
    # doesn't exist once the backend is relocated to BackendsPath, and
    # _makeVenvPortable's shebang rewriter only matches paths that
    # already point at ${EDIR}. Invoking python with the script as an
    # argument bypasses the shebang entirely.
    exec "${EDIR}/venv/bin/python" "${EDIR}/venv/bin/vllm" "$@"
fi

startBackend $@
