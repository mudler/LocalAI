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

## Section: gguf-gallery-targeting (NVFP4 portability + hardware gating)

### 1. NVFP4 GGUFs LOAD + RUN on non-Blackwell - runs-via-dequant, NOT FP4-MMA-required

The published GGUFs use `file_type` MOSTLY_NVFP4 / `GGML_TYPE_NVFP4` (type id 40).
This is a standard ggml block-quant type with FULL software dequant + matmul
coverage across every backend, NOT a Blackwell-only format. Verified against the
paged backend's pinned ggml source (pin 0a2677c6, same upstream as stock
llama-cpp):

- CPU (any arch, amd64 + arm64): full support, no special hardware.
  - `ggml/src/ggml-cpu/quants.c`: `quantize_row_nvfp4` (from_float) +
    `ggml_vec_dot_nvfp4_q8_0_generic` (the matmul dot product), dequant via the
    `kvalues_mxfp4` lookup table. Registered in the CPU type-traits table
    (`ggml-cpu.c` line 283: `[GGML_TYPE_NVFP4] = { .from_float=..., .vec_dot=... }`).
  - NVFP4 handled in all the CPU op switches (`ops.cpp` lines 674, 1125, 1255,
    4424, 4701, 4925, 5651). LOADS + RUNS correctly on a pure-CPU host, just slow.
- CUDA, NON-Blackwell (Pascal/Volta/Turing/Ampere sm_80-86 / Ada sm_89 /
  Hopper sm_90): RUNS correctly via the integer-quantized matmul paths, no
  FP4-MMA needed.
  - `convert.cu` registers `dequantize_row_nvfp4_cuda` as both the to_float and
    to_fp16 dequant kernel (lines 759, 814) - the generic dequant->GEMM path.
  - `mmvq.cu`: `vec_dot_nvfp4_q8_1` (DP4A integer dot, works on any GPU with
    dp4a, i.e. Pascal sm_61+). This is the decode (gemv) path.
  - `mmq.cuh`: NVFP4 has a `MMQ_DP4A_TXS_Q8_0_16` DP4A tile AND a separate
    `MMQ_MMA_TILE_X_K_NVFP4` tile explicitly commented "NVFP4 Generic" (line
    222), DISTINCT from `MMQ_MMA_TILE_X_K_FP4` "MXFP4 and NVFP4 Blackwell" (line
    221). So there are three tiers: DP4A (oldest), generic-MMA (Turing+), and
    Blackwell-native FP4-MMA.
  - The Blackwell path is a runtime FLAG, not a requirement:
    `mmq.cu` line 125 `const bool use_native_fp4 = blackwell_mma_available(cc)
    && (... NVFP4)`. When false (non-Blackwell), it falls through to the generic
    quantized kernel. Grep for any abort/unsupported on NVFP4+blackwell = NONE.
    No `GGML_ABORT`, no garbage - just the non-MMA kernel.
- Vulkan: has `dequant_nvfp4.comp` + NVFP4 in `ggml-vulkan.cpp` / dequant_funcs
  - LOADS + RUNS on Vulkan hosts (AMD/Intel/NVIDIA) via dequant.
- Metal: NVFP4 referenced only in `ggml-metal-device.m` (type registration /
  size), NO Metal NVFP4 compute kernel. On Apple Silicon NVFP4 tensors would
  fall back to the CPU backend op-by-op (correct but slow) IF a Metal build
  existed - which for THIS backend it does not (see build-targeting Section 3).

Bottom line: the NVFP4 GGUFs are PORTABLE. A Hopper/Ada/Ampere/CPU/Vulkan host
loads and runs them correctly (bit-faithful dequant), just WITHOUT the FP4-MMA
speedup. FP4-MMA is a Blackwell-only performance tier layered on top of a
fully-general software path, NOT a load/run gate. Off-Blackwell = runs-via-dequant,
correct-but-slow; never fail/garbage.

### 2. Gallery hardware-targeting GAP: nothing stops a non-Blackwell user

The 6 -paged entries declare NO machine-readable hardware targeting. The only
Blackwell signal is free prose in `description:` ("native Blackwell NVFP4
(FP4-MMA)", "Benchmarked on GB10 / DGX Spark") and a `nvfp4` string in `tags:`.

How LocalAI's gallery CAN express hardware gating (what exists):
- `tags:` are FREE-TEXT, search-only. `core/gallery/gallery.go` line 89 just does
  `strings.Contains(lower(join(tags)), term)` for the search box + line 128
  collects them for filter chips. There is NO tag that gates install or warns;
  the `nvfp4` tag is purely discoverability.
- The model `ModelConfig` struct (`core/gallery/models.go`) has only
  Description/Icon/License/URLs/Name/ConfigFile/Files/PromptTemplates. There is
  NO capabilities / requirements / hardware field at the MODEL level. (Signing
  `verification:` is the only structured gate, unrelated to hardware.)
- The `capabilities:` map (default/nvidia/intel/amd/metal/vulkan/...) is a
  BACKEND-level concept in `backend/index.yaml` (paged entry lines 100-111). It
  selects the backend IMAGE by detected accelerator FAMILY (nvidia vs amd vs
  metal vs cpu). Crucially it does NOT and CANNOT distinguish Blackwell sm_120/121
  from older NVIDIA - `nvidia: cuda12-llama-cpp-localai-paged` is served to ANY
  NVIDIA GPU. There is no sub-nvidia (microarch) gating mechanism in the gallery
  or the backend capability resolver.

So the gating gap is real: a non-Blackwell user browsing the gallery is offered
the NVFP4 entries with no machine-readable signal that they will run far below
the advertised "90-117% of vLLM" numbers (those numbers are GB10/LPDDR5x-bound
specific). It will install and run correctly, just slowly, and the bench claims
in the description will not hold.

### 3. How to express Blackwell-targeting (recommendation)

Given there is no microarch-gating primitive, the honest options are, in order:

a. DESCRIPTION + TAG (only thing available today, zero code): the entries already
   say "native Blackwell NVFP4 (FP4-MMA)" - tighten it to a leading one-line
   "Hardware: Blackwell (RTX 50-series / GB10 / B200) recommended; runs on other
   NVIDIA/CPU via NVFP4 dequant but WITHOUT the FP4-MMA speedup and below the
   quoted GB10 throughput." Add a `blackwell` tag alongside `nvfp4` for the
   filter chip. This is the existing convention (other entries use free prose +
   `nvidia` tag, e.g. line 2395; quant trade-offs are described in prose, e.g.
   the Gemma "Mobile-optimized" notes lines 1312/1366). No other gallery entry
   today encodes a GPU-microarch requirement, so prose is the de-facto standard.
b. If a structured signal is wanted, it would need a NEW field (e.g. a
   `recommended_hardware` / `requires` note surfaced by the React UI import
   dialog) - that is a feature, not a config tweak, and does not exist yet.
c. The `nvfp4` tag should at minimum be present on ALL six entries - the four
   Qwopus/Qwen-MTP entries at lines 819/854/890 have only `[llm, gguf]` tags and
   omit `nvfp4`, so they are not even discoverable/filterable as NVFP4, despite
   being NVFP4 GGUFs. Inconsistent tagging is a secondary gap.

Verdict (gallery-targeting): NVFP4 GGUFs are safe to ship broadly (they run
everywhere via dequant, never fail), so the risk is PERFORMANCE-EXPECTATION, not
correctness. LocalAI has no microarch gating primitive; the only lever is the
description + tags. Recommend a one-line Blackwell-recommended hardware note +
consistent `nvfp4`/`blackwell` tags on all six, and tempering the GB10 bench
claims with the "runs slower off-Blackwell" caveat.

Assisted-by: Claude:opus-4.8 [Claude Code]
