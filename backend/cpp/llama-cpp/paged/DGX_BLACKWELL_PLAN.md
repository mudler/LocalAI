# Closing the vLLM Gap on Blackwell (GB10 / DGX Spark) — Living Plan & Results

Target hardware: NVIDIA **GB10** (Grace-Blackwell, `sm_121a`, 119 GiB unified LPDDR5X), `dgx.casa`.
Model under test: **Qwen3-Coder-30B-A3B-Instruct** (MoE, 128 experts, top-8, ~3B active).
Engines: llama.cpp (CUDA, `~/llama.cpp-pr24423`, build `7a6ddc5`, `CMAKE_CUDA_ARCHITECTURES=121`) vs vLLM 0.23.0 (`~/vllm-bench`, torch 2.11.0+cu130).

> This is a working document. Each phase appends measured numbers, what was learned, and what's next.
> Methodology: `llama-bench` (single-stream pp/tg, built-in reps) and `llama-batched-bench` (`-npl` sweep,
> decode-phase aggregate `S_TG`, prefill aggregate `S_PP`); vLLM via `~/bench/vllm_conc.py` (decode-phase
> aggregate matched to `S_TG`). Same model/prompt/seed. Precision matched where possible.

---

## Baseline results (established)

### Single-stream (B=1), matched ~8-bit
| Engine / precision | prefill pp512 (t/s) | decode tg128 (t/s) |
|---|---|---|
| llama.cpp **Q8_0** | 2215 ± 15 | **54.8 / 62.2** * |
| llama.cpp **F16** | 700 ± 24 | 32.9 ± 0.05 |
| vLLM **FP8** | 9155 ± 308 | 52.45 ± 0.05 |

\* two sessions; ~55 right after worker-stop (clocks settling), ~62 steady state. Both ≥ vLLM → **single-stream parity holds**.

### Concurrency sweep (decode-phase aggregate `S_TG`, prefill aggregate)
| B | llama Q8 prefill | vLLM FP8 prefill | llama Q8 decode | vLLM FP8 decode |
|---|---|---|---|---|
| 1 | 1080 | 9644 | 60.1 | 48.0 |
| 8 | 2189 | 33373 | 160.8 | 312.4 |
| 32 | 2198 | 99398 | 357.1 | 1171 |
| 64 | 2194 | 151990 | 519.2 | 2064 |

llama F16 prefill also flat: B=1 452 → B=8 723 → B=32 778. **Prefill flat at both precisions = kernel-throughput ceiling.**

### Our paged patch (LLAMA_KV_PAGED) — concurrency effect: NONE
Same Q8 binary, paged branch confirmed firing (137 placements at B=8), throughput identical within noise:
| | B=1 | B=8 | B=32 |
|---|---|---|---|
| stock decode | 61.2 | 171.7 | 377.0 |
| paged decode | 62.7 | 170.8 | 376.8 |

Patch is placement-only correctness prototype; doesn't implement concurrency mechanics. Single-stream-neutral, concurrency-neutral.

---

## Root-cause diagnosis (nsys + code audit)

- **74.5% of GPU compute = `mul_mat_q`** (Q8_0 int8 MMQ GEMM, the MoE experts). Only cutlass kernel seen is `cutlass_80_tensorop` = **Ampere (sm_80)**, not Blackwell.
- ggml-cuda has **NO FP8 path** (no e4m3/e5m2 GEMM, no cuBLASLt FP8). Q8_0 runs the **Ampere-class int8 `mma.sync s8.s8.s32`** even on GB10 (`mma.cuh:924`, dispatched unconditionally `mmq.cu:307`).
- ggml-cuda **DOES** have a **native Blackwell FP4 path** (MXFP4 + NVFP4, `mma...kind::mxf4...e2m1`, `mma.cuh:1126`, gated `BLACKWELL_MMA_AVAILABLE`). Merged via #17906/#20644/#21074.
- **No fused MoE grouped GEMM**, no tcgen05/wgmma (warp-level `mma.sync` only).
- **Small per-expert GEMMs**: 512-tok ubatch → ~32 tok/expert (128 exp, top-8) → thin GEMMs, memory-bound, can't fill tensor-core tiles. vLLM processes 8192 tok/step → ~512 tok/expert → compute-bound + FP8.
- **The 45–69× gap is partly apples-to-oranges**: we compared llama Q8 (Ampere int8) vs vLLM FP8 (Blackwell). Upstream/NVIDIA benches put the *real* FP4-vs-FP8 prefill gap at **~25–50% long-context**, not 45–69×.

Key upstream refs: discussion #22042 (FP8 design: `ggml_mul_mat_ext` + scale tensors), #17906 (native MXFP4), #18250 (NVFP4-MoE closed not-planned).

---

## The levers (cheap → expensive) — execution log

### Lever 1 — NVFP4/MXFP4 model (use existing Blackwell FP4 path) + ubatch bump
Status: **IN PROGRESS** — single-stream done, concurrency next.
Quant: `llama-quantize F16 -> MXFP4_MOE` (type 38), 15.9 GiB / 4.47 BPW. (No NVFP4 in llama-quantize; MXFP4_MOE puts experts in MXFP4 = Blackwell FP4 MMA.)

Single-stream (llama-bench), MXFP4 vs Q8 vs vLLM-FP8:
| metric | llama Q8 | **llama MXFP4** | vLLM FP8 |
|---|---|---|---|
| prefill pp512 (ub512) | 2215 | **3061 ± 22** | 9155 |
| prefill pp2048 (ub512) | ~2200 | 3137 ± 7 | — |
| prefill pp2048 (**ub2048**) | — | **3441 ± 14** | — |
| decode tg128 | 62.2 | **86.4 ± 0.3** | 52.45 |

Findings:
- **MXFP4 decode 86.4 beats vLLM FP8 52.45 by 1.65×** (4-bit = less memory traffic; decode is memory-bound). llama wins decode outright.
- MXFP4 prefill +38% over Q8; **ub2048 lifts prefill +10%** (3137→3441). Single-stream prefill gap to vLLM: 4.1× (Q8) → **2.7× (MXFP4)**.
- Caveat: MXFP4 is 4-bit vs vLLM FP8 8-bit — not precision-matched. Fair match = vLLM NVFP4 (4-bit); pending.
Concurrency (decode-phase aggregate `S_TG`, ub2048), MXFP4 vs Q8 vs vLLM-FP8:
| B | Q8 dec | **MXFP4 dec** | vLLM dec | Q8 pp | **MXFP4 pp** | vLLM pp |
|---|---|---|---|---|---|---|
| 1 | 60.1 | **83.4** | 48.0 | 1080 | 1625 | 9644 |
| 8 | 160.8 | **267.4** | 312.4 | 2189 | 3634 | 33373 |
| 32 | 357.1 | **551.2** | 1171 | 2198 | 3651 | 99398 |
| 64 | 519.2 | **770.2** | 2064 | 2194 | 3648 | 151990 |

**Lever-1 verdict:** MXFP4 is a large, free win — decode +50–66% over Q8, prefill plateau +66% (2200→3650). MXFP4 decode **wins at B=1, near-parity at B=8** vs vLLM; only falls behind at high concurrency. **Prefill still plateaus (~3650)** — the MoE prefill GEMM doesn't scale with batch (no fused grouped GEMM; ubatch-limited). That plateau is the real remaining structural gap → Levers 2–3. Quality caveat unchanged (MXFP4 4-bit vs vLLM FP8 8-bit; quality not yet evaluated).

### Lever 2 — `n_ubatch` / `n_batch` tuning (standalone)
Status: **DONE + SHIPPED (auto-default implemented)**
MXFP4 pp4096 vs ubatch: ub512=2994, **ub2048=3316**, ub4096=2820(noisy), ub8192=3180.
**Verdict:** prefill saturates at ub=2048; larger ubatch gives nothing. The ~3300–3650 ceiling is the **MoE GEMM kernel**, not batch size. → No more free config wins; the rest is kernel work (Levers 3–5).
**Implemented:** `core/backend/hardware_defaults.go` — `EffectiveBatchSize` now defaults the physical batch
(n_batch→n_ubatch alias) to **2048 on Blackwell** (`xsysinfo.IsNVIDIABlackwell`, cc≥12 / sm_120/121) when the
config leaves `batch:` unset; explicit `batch:` always wins. Detection is a shared Go helper; placed at the
common ModelOptions builder so it covers the C++ llama.cpp backend too. Tests: `hardware_defaults_internal_test.go`.

### Lever 1b — Standard Q4 vs MXFP4 (what's actually MXFP4-specific)
**Q4_K_M** (17.3 GiB) vs **MXFP4** (15.9 GiB), ub2048:
| metric | Q4_K_M | MXFP4 | Q8 |
|---|---|---|---|
| decode tg128 | **93.5** | 86.4 | 62.2 |
| prefill pp512 | 2164 | **3061** | 2215 |
| prefill pp2048 | 2953 | **3441** | ~2200 |
**Verdict:** the **decode win is just "4-bit"** — plain Q4_K_M matches/beats MXFP4 on decode (both memory-bound).
MXFP4's *only* real edge is **prefill (+41% over Q4_K_M)** via Blackwell FP4 tensor cores. So for shipping,
**"4-bit quant + ubatch=2048" captures most of the win portably**; MXFP4 is a Blackwell-only prefill extra.

### Lever 3 — Fused FP4/FP8 MoE grouped GEMM (+ activation-quant fusion)
Status: **DESIGNED + PROFILED, not built** (multi-week kernel R&D). The single biggest remaining prefill win.

**Decisive measurements:**
- Prefill does NOT scale with bigger single prompts (attention O(N²) confounds): MXFP4 pp2048=3295, pp8192=1524,
  pp16384=2051. So the plateau is not a batch-size fix.
- Real gap is batched many-sequence prefill: B=32 llama 3651 vs vLLM 99398 = **27×**. llama.cpp MoE prefill runs
  at only **~22 effective TFLOP/s** on the GB10 — far below the GPU. Large headroom.
- **nsys (MXFP4 pp2048):** `mul_mat_q<type39>` (MoE FP4 GEMM) = **37.2%**, `quantize_mmq_mxfp4` (act-quant) = 8.0%,
  `mul_mat_q<type8>` (dense/attn, still Q8) = 10.1%, flash_attn = 8.8%. The native FP4 MMA *is* engaged — the
  inefficiency is the **per-expert thin-tile MMQ scheduler** + **un-fused activation quant**.

**Target (precise):** the ~45% in `mmq.cu`'s grouped MoE path (`ggml_cuda_mul_mat_q` + `ids`, `mmid.cu`). Replace
the per-expert thin-tile scheduler with a CUTLASS-style grouped GEMM (full tiles regardless of tokens/expert) and
fuse `quantize_mmq_mxfp4` into the permute/gather. Dense Q8 matmuls (10%) are the separate Lever-4 (FP8) target.
Problem (measured): the prefill ceiling is the MoE expert GEMM. Today `ggml_cuda_mul_mat_q` with `ids`
(`mmq.cu:127`) launches one grouped MMQ over a 3D grid (z = expert), but each expert's tile is thin
(~tokens/expert columns) so int8/FP4 tensor cores run underfilled; throughput is memory-bound on weight
streaming and flat vs batch.
Approach:
- Replace the per-expert thin-tile scheduler with a **CUTLASS-style grouped GEMM** that concatenates all
  experts' token-blocks into one problem with per-group offsets, so tiles are always full (m16n8k64 FP4 /
  m16n8k32 FP8) regardless of per-expert token count. Mirrors vLLM's `fused_moe` + cutlass grouped GEMM.
- **Fuse activation quantization into the permute/gather** (the `quantize_mmq_q8_1`/FP4 quantize currently a
  separate 3.3% kernel) so the routed activations are quantized as they're scattered into expert order.
- Files: new kernel under `ggml/src/ggml-cuda/` (e.g. `moe-grouped-gemm.cu`) + dispatch hook in
  `ggml_cuda_mul_mat_id` (`ggml-cuda.cu:2622`); reuse `mmid.cu` routing/`expert_bounds`.
- Effort: high (2–4 wks expert CUDA). Risk: numerics + sm_121 tile tuning. Expected payoff: the bulk of the
  prefill gap (vLLM's MoE prefill advantage is mostly this). Upstream: #18250 (NVFP4-MoE) was closed
  not-planned, so this would be a LocalAI patch or a fresh upstream proposal.

### Lever 4 — FP8 (e4m3) GEMM for dense layers
Status: **DESIGNED, not built** (blocked on a core ggml API change).
Problem: ggml-cuda has no FP8 matmul (only int8/FP4). vLLM runs qkv/o_proj/lm_head in FP8 on Blackwell
tensor cores. Our dense layers run int8-MMQ or f16-cuBLAS.
Approach (two options):
- (a) **cuBLASLt FP8**: route dense `mul_mat` through `cublasLtMatmul` with `CUDA_R_8F_E4M3` A/B and FP32
  compute + scale pointers. Lowest kernel effort; gets library-tuned Blackwell FP8 immediately. Needs the
  scale-tensor plumbing below.
- (b) **Hand-written sm_121 `mma.sync ...e4m3.e4m3.f32`** kernels in `mma.cuh`/`mmf.cu`. More control, more work.
- Prerequisite (both): the **`ggml_mul_mat_ext` / scale-tensor API** from upstream discussion #22042 —
  per-tensor FP8 scales don't fit the block-scaled quant struct; `MUL_MAT`/`MUL_MAT_ID` must accept optional
  scale tensors. This is a cross-cutting ggml change (graph + ops + all backends' fallbacks).
- Effort: high (API change is the hard part; cuBLASLt path is then moderate). Payoff: closes dense-layer
  prefill/compute gap; complements Lever 3. Note: for *this* MoE model the experts dominate, so Lever 3 > 4.

### Lever 5 — tcgen05 / wgmma-class kernels for large-prefill tiles
Status: **DESIGNED, not built** (very high effort; last increment).
Problem: ggml's tensor-core path is warp-level `mma.sync` only (no `wgmma`/`tcgen05`). Blackwell's
tensor-memory `tcgen05` MMA (what CUTLASS uses) extracts substantially more throughput at large prefill tiles.
Approach: introduce warpgroup/tcgen05 GEMM main-loops for the FP4/FP8 paths (effectively adopting CUTLASS
3.x collective mainloops for sm_120/121), used when tile size is large enough (prefill). Decode (thin) keeps
`mma.sync`.
- Effort: very high (CUTLASS-class engineering). Payoff: the final slice of large-prefill throughput; only
  worth it after Levers 3–4 land. Realistically: depend on/upstream CUTLASS kernels rather than hand-roll.

---

## Paged attention — complete implementation (after kernels are fair)
The placement prototype is insufficient (measured: zero concurrency benefit). A real implementation needs all
four gaps. CPU foundation already built & verified (`PagedKVManager` P0–P3, `README.md`); the in-model parts
are unbuilt. **Build order and concrete design:**

1. **On-demand block allocation from a shared pool** (capacity win — more concurrent seqs before OOM).
   - Replace `find_slot`'s ring-buffer (`llama-kv-cache.cpp:818`) with `PagedKVManager` block allocation; the
     KV tensor becomes a shared block pool `[n_embd, block_size*num_blocks]`, sequences draw blocks on demand
     (already prototyped on CPU: `paged_kv_manager.{h,cpp}`, `test_ggml_paged_rw.cpp`).
   - Win measured where it counts: max concurrent sequences before OOM (not yet benchmarked — needs this).
2. **Gather-read** so each seq attends only its own blocks (`get_k`/`get_v` `:1145/1165` → `ggml_get_rows`
   gather into scratch, then existing attention). Numerically proven on CPU (`test_ggml_paged_attn.cpp`,
   7.5e-08 vs reference). Needs `build_attn_paged` branch in `llama-graph.cpp` + Gate 0 in a real model.
3. **Continuous batching / scheduler** (no head-of-line blocking on mixed-length traffic). New scheduler in
   the server slot path; admit/evict at block granularity; the dimension where paging beats llama.cpp's
   current static batching. This is where the *real* concurrency win lives (vs our synthetic uniform test).
4. **Automatic prefix sharing** (block-hash dedup; `PagedKVManager::{compute_block_hashes,get_computed_blocks}`
   already implemented & tested). Cross-tenant shared system prompts reuse physical blocks.

Status: design in `2026-06-19-paged-attention-llamacpp-design.md`; CPU P0–P3 done; in-model #1–#4 unbuilt.
**Then** measure concurrency in paging's real scenarios — **memory-pressured (max seqs before OOM)** and
**mixed-length continuous batching** — on the MXFP4 (fair-quant) footing, not the uniform/over-provisioned
test that (correctly) showed no benefit.

> Reality check from this session's data: paged attention is a **capacity + scheduling** win, not a per-token
> speed win. On GB10 with 119 GB unified memory and uniform requests we are not memory-bound at B≤64, so the
> placement prototype showed nothing. Paging's value appears under memory pressure (many/long sequences) and
> bursty mixed-length traffic. The per-token throughput gap is a **kernel** problem (Levers 1–3), separate
> from paging.

---

## Implementation plan A — Lever 3: FP4 MoE GEMM to vLLM parity

Goal: lift batched MoE prefill from ~3.65k t/s (B=32) toward vLLM's ~99k. Root cause (profiled):
`mul_mat_q<MXFP4>` runs at ~22 effective TFLOP/s — warp-level `mma.sync`, not Blackwell tcgen05.
Cheap knobs are exhausted (ubatch saturates at 2048; `GGML_CUDA_FORCE_CUBLAS` is a no-op 3419↔3423;
tile width already full at mmq_x=128). So parity needs kernel work, done iteratively on the DGX
(`~/llama.cpp-pr24423`, editable + rebuildable; diffs captured as `patches/`).

Phases (each: hypothesis → edit `ggml/src/ggml-cuda/` → `cmake --build build --target llama-bench` →
`llama-bench` MXFP4 pp/concurrency → record):
1. **Cheap kernel tweaks (low confidence, fast).** nwarps (occupancy), `mmq_y` tile, stream-k on/off,
   FP4 load-tile path. Measure each. Likely small (<1.3x) — these don't change the warp-MMA ceiling.
   - **Result (nwarps):** DEAD END. `nwarps` is locked by `static_assert(nwarps*tile_C::I == mmq_y)`
     (mmq.cuh:3234) → nwarps=8 for mmq_y=128. Can't raise occupancy without co-scaling mmq_y to 256
     (nwarps=16), which blows Blackwell shared-memory limits. The MMQ constants are tightly coupled;
     it is not freely tunable. Confirms parity needs the kernel rewrite (phase 3), not knobs.
2. **Fuse activation quant** (`quantize_mmq_mxfp4`, 8%) into the permute/gather. Removes a kernel +
   a global round-trip. Tractable, ~1.1x.
   - **Result:** NOT AVAILABLE as a cheap patch. `quantize_mmq_fp4_cuda` (mmq.cu:200) *already* takes
     `ids_src1` — the gather is already fused into the quant. The only remaining fusion is quantize-on-load
     *inside* the GEMM hot loop (intricate, ~8% ceiling, risky). ORippler's #24481 fuses the decode (MMVQ)
     post-scale and intends a "BS>1" (prefill) follow-up — unwritten. Marginal; skip.

**Upstream survey (2026-06):** there is NO tcgen05/CUTLASS grouped-GEMM MoE kernel in ggml — not merged,
not in-flight, not a draft (Discussion #18369 is talk, no PR; #18250 closed not-planned). CUTLASS is not a
dependency (the profile's `cutlass_80_tensorop` is cuBLAS-internal). No fork has a portable MoE kernel
(croll83/llama.cpp-dgx is GatedDeltaNet-focused). Maintainer signal (woachk on #17906): "the path forward
is to wait for cuTile C++." So **nothing to cherry-pick; phase 3 is genuinely from-scratch.**
3. **The real lever — tcgen05 / CUTLASS FP4 grouped GEMM.** Replace the per-expert MMQ scheduler with a
   CUTLASS 3.x collective-mainloop grouped GEMM (sm_120a, `e2m1` block-scaled, tcgen05 tensor-memory MMA),
   one problem over all experts with per-group offsets, fused act-quant. This is what vLLM/FlashInfer use.
   Multi-week; the honest path to parity. Prefer **upstream ggml** (issue drafted) over a private patch.
4. **Full-model low precision.** Quantize dense layers (qkv/o_proj/lm_head, the 10% Q8) to FP4/FP8 too so
   the whole prefill runs on FP4 tensor cores, not int8-MMQ.
Exit per phase: measured t/s recorded here; stop a phase when it's a dead end (recorded as such).
Matching vLLM realistically requires phase 3; phases 1–2 are the warm-up + de-risking.

## Implementation plan B — Complete paged attention (the pivot)

CPU foundation done (P0–P3, `README.md`): vLLM-parity block manager + ggml write/gather + attention
numerics + placement Gate 0 (token-identical in-model). Remaining = make it deliver the multi-tenant wins.
Phases:
1. **On-demand shared-block pool** — replace `find_slot` ring buffer (`llama-kv-cache.cpp:818`) with
   `PagedKVManager` block allocation; KV tensor = `[n_embd, block_size*num_blocks]` shared pool. Win:
   fit more concurrent seqs before OOM. Test: max concurrent seqs at fixed budget vs contiguous.
2. **Gather-read** (`get_k/get_v` `:1145/1165` → `ggml_get_rows` into scratch) + `build_attn_paged` branch
   in `llama-graph.cpp`. Numerically proven on CPU (7.5e-08). Gate 0: token-identical multi-seq.
3. **Continuous batching / scheduler** — admit/evict at block granularity in the server slot path. The
   real concurrency win on mixed-length traffic (where the placement prototype showed nothing).
4. **Automatic prefix sharing** — block-hash dedup (`PagedKVManager::{compute_block_hashes,get_computed_blocks}`
   already implemented + tested). Cross-tenant shared system prompts reuse physical blocks.
Then benchmark in paging's real regimes — **memory-pressured** + **mixed-length continuous batching** — on
the MXFP4 (fair-quant) footing. Note: GB10's 119 GB unified memory means win-1 needs genuine pressure
(long/many seqs) to show; the win is capacity + scheduling, not per-token speed.

## Honest scope note
Levers 3–5 and the complete paged implementation are each substantial (weeks of expert CUDA/systems work). This doc tracks what is **measured** vs **designed** vs **not-yet-built**, and never claims a number that wasn't run on the box.
