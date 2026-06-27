# ARCH_GENERALITY_AUDIT - llama-cpp-localai-paged backend

Source/build/gallery audit (no GPU, no hardware). Maps how arch-general the
paged backend's BUILD targeting is, and whether non-Blackwell / Metal / CPU
hosts get a working build.

## Section: backend-build-matrix (build targeting)

### 1. CUDA arch list: NOT Blackwell-only - it is the FULL upstream ggml default

There is NO explicit CUDA arch list anywhere in the paged build path:

- `.docker/llama-cpp-localai-paged-compile.sh` only injects
  `-DCMAKE_CUDA_ARCHITECTURES=${CUDA_DOCKER_ARCH}` *when* `CUDA_DOCKER_ARCH` is
  non-empty (`if [[ -n "${CUDA_DOCKER_ARCH:-}" ]]`).
- NO `backend-matrix.yml` row for `llama-cpp-localai-paged` sets
  `CUDA_DOCKER_ARCH` (nor does any stock `llama-cpp` row). It is empty.
- `backend/cpp/llama-cpp/Makefile` (reused verbatim by the paged wrapper) sets
  only `-DGGML_CUDA=ON` (+ `-DGGML_NATIVE=OFF`). It never sets
  `CMAKE_CUDA_ARCHITECTURES` / `CUDA_DOCKER_ARCH`.

=> The compiled arch fan is whatever upstream llama.cpp / ggml-cuda picks by
default with `GGML_NATIVE=OFF` (the full multi-arch default, which includes
Blackwell sm_120 alongside the older archs ggml ships). This is BIT-IDENTICAL to
how the stock llama-cpp backend is targeted - the paged wrapper copies and reuses
the exact same Makefile + CMakeLists + prepare.sh, only forcing `LLAMA_PAGED=on`.

Consequence for NVFP4: the FP4-MMA kernel is compile-time gated *inside* the
ggml-cuda TU by `BLACKWELL_MMA_AVAILABLE` (sm_120/121 consumer, sm_100
datacenter). Because the build emits the full arch fan (not a Blackwell-only
list), the sm_120 NVFP4-MMA path is present for Blackwell AND the older archs get
their own kernels (NVFP4 runs the non-MMA fallback at runtime on
Ampere/Ada/Hopper). NOTHING in the build pins this to GB10/sm_121. The binary is
arch-portable; only the NVFP4 *speedup* is Blackwell-specific, by kernel gating,
not by build targeting.

### 2. Variants built: CUDA + ROCm + SYCL + Vulkan + CPU (NOT CUDA-only)

`backend-matrix.yml` `include:` (Linux) ships, for `llama-cpp-localai-paged`,
one row per stock-llama-cpp Linux row (10 rows, lines 4889-5046):

- cublas CUDA 12.8 (linux/amd64)
- cublas CUDA 13.0 (linux/amd64)
- cublas CUDA 13.0 arm64 l4t (jetson)
- cublas CUDA 12.0 arm64 l4t (jetson)
- hipblas / ROCm 7.2.1 (linux/amd64) - AMDGPU_TARGETS = gfx908..gfx1201
- sycl_f32 (Intel oneAPI)
- sycl_f16 (Intel oneAPI)
- vulkan (linux/amd64)
- vulkan (linux/arm64)
- CPU (linux/amd64) + CPU (linux/arm64), built via the ggml
  `CPU_ALL_VARIANTS` single-build (dlopen libggml-cpu-*.so by host CPU feature;
  arm64 uses gcc-14 for SME).

So it is NOT CUDA-only. Per image, `compile.sh` builds: the accelerator variant
(or CPU_ALL_VARIANTS when BUILD_TYPE empty) + grpc-server + rpc-server.

### 3. THE GAP vs stock llama-cpp: NO Metal / Darwin build

This is the single build-targeting divergence:

- stock `llama-cpp` HAS a Darwin row in `includeDarwin`
  (`-metal-darwin-arm64-llama-cpp`, line 5071) and a `metal:` capability key
  (`metal: "metal-llama-cpp"`, index.yaml line 25).
- `llama-cpp-localai-paged` has ZERO entries in `includeDarwin` (grep past line
  5048 = none) and NO `metal:` capability key in its meta-backend.
- There is NO `metal-*-llama-cpp-localai-paged` tag anywhere in
  backend-matrix.yml or backend/index.yaml.

`scripts/changed-backends.js` already anticipates a future darwin paged row
(lines 78-81 map `backend === "llama-cpp-localai-paged"` to the C++ source dir),
but no such matrix row exists, so it is currently dead/forward-looking code.

Everything else (CUDA arch fan, ROCm gfx list, SYCL, Vulkan, CPU) matches stock
llama-cpp exactly.

### 4. Does a non-Blackwell / Metal / CPU host get a working build of THIS backend?

Meta-backend capabilities map (index.yaml lines 101-111):
default(cpu), nvidia(cuda12), intel(sycl-f16), amd(rocm), vulkan, nvidia-l4t,
nvidia-cuda-13, nvidia-cuda-12, nvidia-l4t-cuda-12/13.  NO `metal:` key.

- Non-Blackwell NVIDIA (Ampere sm_80-86 / Ada sm_89 / Hopper sm_90 / datacenter
  Blackwell sm_100): selects the SAME cuda12 / cuda13 image. That image is
  compiled for the full arch fan, so it RUNS. NVFP4 falls back to the non-MMA
  path on pre-Blackwell; on sm_100 it gets FP4-MMA but is compute-bound (HBM3e),
  not the LPDDR5x-bound GB10 regime the patches were tuned for. WORKS, just
  without the GB10-specific bandwidth win.
- AMD / Intel / Vulkan / CPU (amd64 + arm64) Linux hosts: each has its own
  matching variant in the map + matrix. WORKS.
- Metal / macOS Apple Silicon: NO `metal:` key and NO darwin build. Capability
  resolution falls back to `default` = `cpu-llama-cpp-localai-paged`, which is a
  Linux (amd64/arm64) image, NOT a macOS-native build, so it will NOT run on
  macOS. And because this is a SEPARATE meta-backend, it does NOT fall through to
  the stock `llama-cpp` backend - a Mac user who explicitly selects
  llama-cpp-localai-paged gets a non-running selection and must manually pick the
  stock llama-cpp backend instead. DOES NOT WORK on Metal/macOS; no auto-fallback
  to stock.

## Verdict (build-targeting)

- Arch-general on Linux: YES. The build is NOT Blackwell-only; it targets the
  exact same full CUDA arch fan + the same ROCm/SYCL/Vulkan/CPU variant set as
  stock llama-cpp. Any Linux host that can run stock llama-cpp can run THIS
  backend; the NVFP4 speedup is the only Blackwell-gated piece, and that gating
  is inside the kernel, not in the build matrix.
- Single gap: NO Metal/Darwin variant and NO `metal:` capability key. macOS /
  Apple Silicon hosts have no working build of this backend and do not auto-fall
  to stock llama-cpp. To close the gap, add an `includeDarwin` row
  (`-metal-darwin-arm64-llama-cpp-localai-paged`, mirroring the stock llama-cpp
  darwin row + the C++ source build path that changed-backends.js already
  anticipates) and a `metal:` key to the paged meta-backend. (Note: NVFP4 has no
  Metal MMA path, so a Metal build would deliver paged-KV behaviour only, no
  NVFP4 acceleration - still a correctness/availability win over the current
  broken selection.)

Assisted-by: Claude:opus-4.8 [Claude Code]
