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

## Section: optimization-generality (patches 0013/0016 + 0017-0029)

Classifies each optimization as arch-GENERAL (ship everywhere, helps any arch),
GB10-TUNED (needs per-arch retuning of the magnitude/constants), or
Blackwell-PRECISION-specific (only meaningful where FP4-MMA exists). Read from the
patch commit bodies + the diffs they touch; bit-exactness verdicts are the
patches' own md5/test-backend-ops gates.

Arch axis used: NVFP4 FP4-MMA needs `BLACKWELL_MMA_AVAILABLE` (sm_120/121 consumer
+ sm_100 datacenter); Hopper sm_90 / Ada sm_89 / Ampere sm_80-86 have none;
Metal/CPU/AMD/Intel have no NVFP4-MMA. Datacenter Blackwell sm_100 has FP4-MMA but
HBM3e (~8 TB/s) so it is COMPUTE-bound, not bandwidth-bound: every GB10
"bandwidth-bound" verdict inverts there. The FP4-MMA kernel itself is UPSTREAM
ggml-cuda gated by `BLACKWELL_MMA_AVAILABLE`; none of these patches add it - they
reshape/route/dedup around it (0017/0020/0023/0025) or are precision-agnostic.

### A. ARCH-GENERAL (ship everywhere; pure win or provably neutral)

Graph-shape, host-side, or gather/copy-elimination changes. No FP4, no
bandwidth-floor assumption. Bit-exact. Help or are neutral on any arch that runs
the code path.

- 0013 decoupled prefill-token budget - pure `update_slots()` scheduler policy,
  zero libllama/ggml change, orthogonal to LLAMA_KV_PAGED, default-off
  byte-identical. Latency/fairness lever (flattens decode-ITL spike from a
  co-batched long prefill). No arch assumption.
- 0016 dynamic decode-first prefill budget - supersedes 0013; still pure
  `update_slots()` policy, default-off byte-identical, T==n_batch degenerate case
  == stock. Arch-neutral, identical paged on/off.
- 0024 paged-pool burst-reclaim - host-side block accounting + defrag + slot
  release; never touches KV values or compute. Gated behind LLAMA_KV_PAGED. Fixes
  a real fragmentation/throughput-collapse bug on long-lived servers.
  Arch-independent host bookkeeping.
- 0029 block-table within-step host cache - memcpy-reuse of the host block table
  across full-attention layers in one step; bit-exact, LLAMA_PAGED_NO_BT_CACHE=1
  off. Helps host-bound decode (dense +2.7% on GB10), neutral when compute-bound
  (MoE flat). The faster the GPU (e.g. sm_100), the MORE host-bound decode is, so
  the BIGGER this win elsewhere.
- 0028 recurrent-state (conv-tap) gather fusion - eliminates a k_get_rows by
  reading cache[ids[s]] in-kernel; bit-identical. Deleting a gather vLLM has no
  equivalent of is a win on ANY arch running the GDN path; not FP4, not
  bandwidth-floor specific.
- 0018 in-place SSM-state write-back + 0019 fused SSM-state gather + 0021
  conv-state in-place fusion - remove a D2D state copy-back (0018), a state
  get_rows (0019), and the 4-op conv chain + ring-state copy (0021), mirroring
  vLLM's in-place recurrent update. Arithmetic byte-identical; what is removed is
  plumbing, so wins on ANY arch running the gated-DeltaNet recurrence.
- Paged KV core (0001-0012) - paged KV manager, on-demand alloc, prefix caching,
  in-kernel paged read. No precision or bandwidth-floor assumption; the most
  portable part of the work, helps capacity/serving anywhere it compiles.

NOTE: 0018/0019/0021/0028 + the base GDN op have CUDA + CPU kernels ONLY (every
gate is "CUDA0 vs CPU"). General within {CUDA, HIP/ROCm (hipified ggml-cuda), CPU};
NOT covered on Metal/SYCL/Vulkan - see SAFETY #1.

### B. GENERAL-IN-DIRECTION but the MAGNITUDE was measured on the GB10 floor

Correct + beneficial everywhere, but the specific %/constants are GB10-bound.

- 0020 o_proj GDN MMVQ->MMQ reshape - collapses the GDN output to 2D so the
  ssm_out matmul sees src1->ne[1]=128 and routes to MMQ (M=128 GEMM that amortizes
  the weight read across 128 tokens) instead of MMVQ (built for batch<=8 with the
  128 sequences stuck in ne[2]). Zero-cost view change, bit-identical, gated to the
  gated-DeltaNet path. UNIVERSAL: MMVQ (mul_mat_vec_q) is structurally a batch<=8
  GEMV and cannot amortize the weight read at a real M=128; MMQ does, on ALL CUDA
  archs (dp4a pre-tensor-core still amortizes) and on HIP. So MMQ > MMVQ at M=128
  is NOT GB10-specific - pure win wherever MMQ exists. RE-TUNE: the +31.7%
  magnitude is on the GB10 BW floor; smaller % on sm_100 but still correct.
  REGRESSION RISK: only at a genuinely tiny real M (single-stream decode n_seqs<=8)
  could forcing MMQ be slower than MMVQ - see SAFETY #2. On Metal/Vulkan/SYCL the
  MMVQ/MMQ split differs; the reshape is harmless (a view) but yields no benefit.
- 0023 MoE NVFP4 activation-quantize de-dup - for broadcast up/gate proj (ne11==1)
  quantize the unique token activations once and gather the identical FP4 blocks
  instead of re-quantizing per expert; bit-exact, ..._DEDUP=0 off.
  DIRECTION-GENERAL (de-duplicating identical work is always good) but
  NVFP4-block-layout specific (uint4 copy of block_fp4_mmq) and only matters where
  activation-quant is a measurable decode bucket - on a compute-bound arch the
  saved quant time may be off the critical path (even on GB10 the MoE TG win is
  only +1.7%).

### C. GB10-TUNED (constants are GB10 winners; re-sweep per arch)

- 0022 GDN recurrence occupancy/coalescing retune - column-folding template params
  NUM_WARPS/COLS_PER_WARP, default (16,8), env-selectable GDN_NW/GDN_CPW. The
  reduction/FMA order is byte-identical (md5-gateable); only the warp/block->column
  assignment changes to raise memory-level parallelism on a BANDWIDTH-BOUND kernel.
  (16,8) is explicitly "the measured GB10 winner". Textbook per-arch tune: optimal
  values depend on DRAM latency / L2 / SM count / occupancy. SAFE everywhere
  (bit-exact, env-overridable, no forbidden float4 load) but unlikely optimal off
  GB10; on a compute-bound arch (sm_100) the kernel may not even be the
  bottleneck. Needs a per-arch GDN_NW/CPW sweep.
- 0017 FP4 dense-GEMM decode tile tune - shipped as a P0 bit-exact gate + DEFAULT-
  OFF occupancy levers (GGML_CUDA_FP4_MMQ_Y / ..._MINBLOCKS / ..._DENSE_MMQ_X).
  Honest GB10 verdict was a KILL-GATE: every cheap occupancy probe REGRESSED on
  sm_121 (the M=128 tile is already weight-read optimal). Nothing on by default =>
  byte-identical to stock everywhere. On a DIFFERENT FP4 arch (sm_100) the
  kill-gate could flip; the levers are in place and inert, ready to re-sweep.

### D. Blackwell-PRECISION-specific (only meaningful where FP4-MMA exists)

- 0025 MoE NVFP4 MoE-decode re-graph - keeps CUDA graphs on for the grouped
  stream-k mul_mat_q id-path; env-gated LLAMA_MOE_FORCE_GRAPHS, default-off
  byte-identical. The CUDA-graph mechanism is general, but the specific guard
  condition (mmvq_mmid_max==8 for NVFP4 on sm_121) and the "graphs are safe here"
  reasoning are tied to the NVFP4 grouped path on Blackwell. On a non-FP4 arch the
  node would not take that branch -> inert.
- 0026 hybrid per-head SSM-state precision (bf16 SSM/conv cache) - adds
  --cache-type-ssm/-conv + --ssm-bf16-tau (per-head f32-vs-bf16 by memory length).
  Default f32 = bit-exact. PRECISION/bandwidth lever: bf16 halves the dominant GDN
  decode byte stream, which only pays off on a BANDWIDTH-bound arch (GB10). On
  sm_100 HBM3e it buys little. Value is bandwidth-floor specific; correctness is
  precision-specific (opt-in, default-safe).
- NVFP4 GGUFs + the 6 gallery -paged rows - inherently Blackwell-precision-specific
  for the FAST path: NVFP4 weights only get FP4-MMA on sm_120/121/100. Elsewhere
  they run-via-dequant (correct, slow) per the gallery-targeting section above.

### Per-arch expected story

- Consumer Blackwell sm_120/121 (GB10 / dGPU): the validated target. dGPU sm_120
  (GDDR7 ~1 TB/s) is less BW-starved than GB10 LPDDR5x 273 GB/s, so the
  bandwidth-floor wins (0018/0019/0022/0026) shrink in % while the host-pipeline +
  graph wins (0029/0025) and the MMQ reshape (0020) hold.
- Datacenter Blackwell sm_100 (HBM3e ~8 TB/s): FP4-MMA WORKS so NVFP4 stays fast
  (precision bucket + 0025 carry over), BUT the BW floor is GONE -> compute-bound.
  Re-tune: 0022 GDN_NW/CPW sweep; 0017 kill-gate may flip (levers ready). The
  bandwidth-motivated wins (0018/0019, 0026 bf16-state) shrink toward neutral; the
  host-pipeline/graph/MMQ-reshape general wins (0029/0025/0020) still help. Net:
  works, faster GPU, needs a re-tune pass, do NOT assume the GB10 constants.
- Hopper sm_90 / Ada sm_89 / Ampere sm_80-86: NO FP4-MMA. NVFP4 GGUFs + the FP4
  levers (0017/0023/0025) are out of scope -> use a DIFFERENT quant (Q4_K/AWQ/GPTQ
  etc). BUT the precision-agnostic infra still helps: paged KV core, scheduler
  (0013/0016), burst-reclaim (0024), block-table cache (0029), SSM/conv
  plumbing-removal (0018/0019/0021/0028); 0020 still routes the o_proj
  MMVQ->MMQ in whatever quant it uses. Story: "no FP4 -> another quant, but paged +
  SSM + scheduler infra is a pure win".
- Metal / CPU / AMD-ROCm / Intel-SYCL / Vulkan (all built by the matrix): no
  NVFP4-MMA; paged KV + scheduler infra is the portable value. CPU has reference
  kernels for every fused op (the bit-exact gate is CUDA0-vs-CPU). ROCm/HIP reuses
  ggml-cuda (hipify) so it inherits the fused-op kernels. Metal/SYCL/Vulkan do NOT
  get the new fused-op kernels (SAFETY #1).

### SAFETY / regression risks

1. Fused GDN/conv ops are CUDA + CPU only; emission is NOT backend-gated.
   0018/0019/0021/0028 add new ops (ggml_gated_delta_net_inplace[_ids],
   ggml_ssm_conv_update_inplace[_ids]) implemented for CUDA + CPU only. They are
   emitted by the graph builder whenever cparams.fused_gdn_ar/fused_gdn_ch is set
   (constructor sets fused_gdn_ch=true, auto_fgdn=true), with NO check that the
   active compute backend is CUDA/HIP. Fine on CUDA/HIP/CPU. On Metal/SYCL/Vulkan
   two failure modes: (a) the new GATED_DELTA_NET op has no kernel -> loud
   supports_op/assert (but the base GDN op may already be CUDA/CPU-only upstream,
   so a qwen35 model likely cannot run there regardless); (b) the fused conv
   variant REUSES GGML_OP_SSM_CONV discriminated by a non-null src[3]/src[4] - a
   backend that supports plain SSM_CONV but ignores the discriminator would compute
   the WRONG plain conv -> SILENT corruption. That is the one genuine
   silent-correctness risk. RECOMMENDATION: gate fused-op emission on the compute
   backend being CUDA/HIP (or add a supports_op guard rejecting the discriminated
   SSM_CONV where the fused handling is absent).
2. 0020 MMVQ->MMQ at tiny real M. MMQ is right at decode M=128 (the gallery
   batched-serving regime); it would be wrong only at a genuine M<=8 (single-stream
   decode, n_seqs=1). Bit-identical either way - only a potential perf regression
   at tiny batch on non-GB10 archs (never the measured GB10 case). Worth confirming
   the reshape still picks the better kernel at n_seqs=1 elsewhere.
3. 0022 default (16,8) off GB10: safe (bit-exact, env-overridable) but non-optimal;
   do not ship it as the default for sm_100/Hopper/Ada without a GDN_NW/CPW sweep.
   No correctness risk.
4. Gallery rows do not state a GPU-arch requirement (covered in the
   gallery-targeting section): add a Blackwell (sm_120/121/100) recommended note.

### One-line verdict

The PORTABLE core (paged KV 0001-0012, scheduler 0013/0016, burst-reclaim 0024,
block-table cache 0029, the SSM/conv plumbing-removal 0018/0019/0021/0028, the
o_proj MMQ reshape 0020) is arch-general and ships everywhere it compiles -
bit-exact, mostly default-safe, pure win or neutral. The FP4/NVFP4 levers
(0017/0023/0025, the GGUFs, the gallery) are Blackwell-precision-specific. The
occupancy/precision tunes (0017 levers, 0022, 0026) are GB10-bandwidth-floor-tuned
and need a per-arch re-sweep (especially on sm_100 where the BW floor is gone and
the regime flips to compute-bound). The single real SAFETY gap: the new fused
GDN/conv ops are CUDA+CPU-only with backend-ungated emission, so a Vulkan/SYCL/Metal
paged build of a gated-DeltaNet model could assert (GDN op) or silently miscompute
(discriminated SSM_CONV) - it should be compute-backend-gated.

Assisted-by: Claude:opus-4.8 [Claude Code]
