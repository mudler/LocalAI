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

## Section: patch-arch-safety (build-break / miscompile classification, 0018-0029)

This section is the narrow safety read: for EACH patch, does it (a) compile and
behave correctly on every build target, (b) compile only under
BLACKWELL_MMA_AVAILABLE with a fallback elsewhere, or (c) RISK a build break /
miscompile / crash on a non-Blackwell arch. Class letters here are
build-safety classes, distinct from the perf-generality buckets above. Note 0027
does not exist (numbering gap). The dispositive build facts: the backend is built
for CUDA 12/13, L4T arm64, ROCm/hipblas, SYCL f32/f16, CPU (amd64+arm64), Vulkan -
and NOT for darwin/Metal (no includeDarwin row), and the CUDA build emits the full
multi-arch fan (CUDA_DOCKER_ARCH unset; Dockerfile documents e.g. `75;86;89;120`),
so every .cu TU MUST already compile for non-Blackwell SASS.

Method: grepped every added line in 0017-0029 for arch-specific tokens
(BLACKWELL/__CUDA_ARCH__/sm_NNN/cp.async/ldmatrix/mma./asm volatile/cc gates).
The ONLY hits are in 0017 (all correctly `#if`-gated) and free-text comments. No
SSM/conv/GDN kernel in the series uses a Blackwell intrinsic or a hardcoded
sm_12x launch geometry.

| patch | class | build-safety note |
|-------|-------|-------------------|
| 0017 fp4-gemm-decode-tile-tune | (b) GATED | only Blackwell-specific patch; NVFP4 mmq_y/min-blocks levers behind `#if defined(BLACKWELL_MMA_AVAILABLE)` + `blackwell_mma_available(cc)` + `type==GGML_TYPE_NVFP4`, ALL default-off => default build byte-identical to stock on every arch. `get_mmq_y_device<type>()` templating has a default arg keeping stock behaviour for non-NVFP4. Builds on sm_80-90 (body stripped). |
| 0018 ssm-decode-inplace-state | (a) general | plain in-place GDN state write-back, no intrinsics; CPU mirror in ggml-cpu/ops.cpp. |
| 0019 ssm-decode-fused-gather | (a) general | `gdn_gather_nonident_kernel` = plain `<<<n_seqs,256>>>`; CPU mirror added. |
| 0020 gdn-oproj-mmq-reshape | (a) general | host-side reshape_2d in qwen35*/qwen3next.cpp, no device code. |
| 0021 conv-state-inplace-fusion | (a) general | new op reuses GGML_OP_SSM_CONV (4th src discriminator), no new enum => no ggml-cpu.c switch needed; `ssm_conv_update_f32` plain portable CUDA (threads=128, templated d_conv); CPU mirror + test case. |
| 0022 gdn-recurrence-occupancy-retune | (a) general | template NUM_WARPS/COLS_PER_WARP/MIN_BLOCKS; new default (16,8) = 512 thr/block, MIN_BLOCKS=2, within the 1024 limit on sm_70..120 and AMD; bit-exact for any (NW,CPW). NOT Blackwell-gated and NOT a break - just a GB10-tuned default applied everywhere (see risk 3 below). |
| 0023 moe-nvfp4-quant-dedup | (a) general | `gather_mmq_fp4` = plain uint4 byte-copy kernel; reached ONLY inside the pre-existing `if (use_native_fp4)` branch (Blackwell-only at runtime) and uses `block_fp4_mmq`, a type that already compiles for the full arch fan pre-0023. Adds no new arch surface. |
| 0024 paged-pool-burst-reclaim | (a) general | pure host C++. |
| 0025 moe-nvfp4-decode-regraph | (a) general | host-side ggml-cuda.cu graph-guard relaxation, env-gated `LLAMA_MOE_FORCE_GRAPHS` default-off => byte-identical; predicate is runtime cc-generic. |
| 0026 hybrid-perhead-ssm-state | (a) general | mostly host plumbing; GDN kernel = same portable column-folded code; fill.cu instantiates `fill_kernel<nv_bfloat16>` (bf16 STORAGE-only, fine on all targeted arches; bf16-compute SSM plan is SHELVED so STATE_T stays f32 on the hot path). LOW-RISK verify item: confirm no bf16-arithmetic GDN instantiation reaches sm_75 if sm_75 ships. |
| 0028 recurrent-state-gather-fusion | (a) general | new op reuses GGML_OP_SSM_CONV (ids src + rs_head); `ssm_conv_gather_nonident_kernel` plain portable CUDA; CPU mirror + test cases. |
| 0029 blocktable-within-step-cache | (a) general | pure host C++ + host-timing instrumentation. |

### Specific lines that carry the only conditional/risk surface

- 0017 the ONLY correctly-gated arch surface:
  - `get_mmq_y_host`: `if (GGML_CUDA_FP4_MMQ_Y != 128 && type == GGML_TYPE_NVFP4 && blackwell_mma_available(cc))`
  - `get_mmq_y_device<type>()` / `mmq_get_min_blocks_device<type>()`: bodies inside `#if defined(BLACKWELL_MMA_AVAILABLE)`.
  All default to the stock value, so a default build is byte-identical everywhere.
- 0023 the gather kernel default-on (GGML_CUDA_MOE_QUANT_DEDUP=1) but the call site
  is `if (moe_quant_dedup && ne11 == 1)` strictly inside `if (use_native_fp4)`; on
  non-Blackwell `use_native_fp4` is false so the dedup never executes.
- 0022 the GB10-tuned launch geometry is `GDN_DEFAULT_NW 16` / `GDN_DEFAULT_CPW 8`
  (=> 512 threads, MIN_BLOCKS=2). This is the closest thing to a "hardcoded for
  GB10" launch config, but it is a correct, within-limits, bit-exact default for
  ANY arch, runtime-overridable via GDN_NW/GDN_CPW. Not a break.

### THE ONE silent-correctness risk (cross-ref SAFETY #1 above)

0021/0028 (and 0018/0019 for the GDN op) implement their new ops for CUDA + CPU
ONLY, and the fused conv variants REUSE GGML_OP_SSM_CONV discriminated by a
non-null src[3]/src[4]. Emission is NOT gated on the active compute backend. A
backend that supports plain SSM_CONV but ignores the discriminator would run the
WRONG plain conv => SILENT corruption (not a build break). In practice the model
that emits these (qwen35 hybrid) also needs the fork-custom GDN op, which is
CUDA/CPU-only, so on Vulkan/SYCL the GDN node asserts/falls back FIRST and the
model cannot run there regardless; and Metal is not a build target. So the risk is
latent rather than live, but it should still be closed by gating fused-op emission
on a CUDA/HIP compute backend (or a supports_op guard rejecting the discriminated
SSM_CONV where fused handling is absent). This is the single item that could ever
miscompute silently; everything else is either build-safe or loud.

### Build-safety verdict per target (would it COMPILE / RUN)

- CUDA sm_80 / 86 / 89 / 90 (Ampere/Ada/Hopper): BUILDS (0017 Blackwell code
  `#if`-stripped + default-off; all other device code portable CUDA). qwen35 hybrid
  models RUN (GDN + ssm_conv_update + gather have non-Blackwell kernels). NVFP4
  GGUFs run via the stock non-FP4-MMA dequant/DP4A path; the FP4 levers are inert,
  not broken. No patch in 0018-0029 breaks this build.
- CUDA sm_100 (datacenter Blackwell, HBM3e): BUILDS + every lever active
  (BLACKWELL_MMA_AVAILABLE defined). Bit-exact. GB10-tuned launch defaults are
  correct but tuned for the LPDDR5x BW floor; on HBM3e the regime is compute-bound,
  so safe-but-not-necessarily-optimal (re-sweep 0022/0017 levers). No build/correctness risk.
- Metal: NOT a build target (no darwin row), so missing Metal kernels for the new
  SSM_CONV/GDN ops cannot break a build or a run here. (The GDN op has no Metal
  kernel regardless.)
- CPU (amd64 + arm64): BUILDS + RUNS - every new op ships a CPU mirror under the
  reused enums; host patches are portable C++.
- ROCm/HIP, Intel SYCL, Vulkan: BUILD ok. The .cu additions hipify cleanly (no
  Blackwell intrinsic outside the `#if`; 0022's 512-thread launch within AMD limits).
  SYCL/Vulkan are separate backends that don't compile the .cu files and lack the
  GDN op, so qwen35 hybrid models fall back/assert there rather than run; classic
  (non-qwen35) models are unaffected because SSM_CONV semantics only change when the
  qwen35 graph emits the discriminator src. The latent silent-SSM_CONV risk above
  applies only if a backend both supports SSM_CONV and ignores the discriminator.

Verdict: of 0018-0029, none would break a non-Blackwell CUDA build, the CPU build,
or the ROCm/SYCL/Vulkan builds; 0017 is the only Blackwell-gated patch and is
default-off and `#if`-guarded. The sole non-build hazard is the latent
discriminated-SSM_CONV silent-miscompute on a hypothetical Vulkan/SYCL/Metal GDN
run, which should be closed by compute-backend-gating the fused-op emission.

Assisted-by: Claude:opus-4.8 [Claude Code]
