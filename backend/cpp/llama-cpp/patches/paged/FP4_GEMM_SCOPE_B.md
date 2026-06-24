# Track B: the FP4-MMA weight-GEMM for GB10 decode parity with vLLM — build-ready scope + honest go/no-go

Scope only (build-ready plan + honest verdict). **Not implemented in this workflow.** Track B is the
residual-kernel track after track A (fuse the standalone `quantize_mmq_fp4` activation-requant, the
8.2% decode bucket — tasks 38-41, the fused `rms_norm+mul+nvfp4-quant` producer + prequantized-MMQ
consumer) is handled separately. Track B owns the **weight GEMM**, the ~59% bucket.

**The load-bearing question, restated:** at the decode batch shape (M≈128 tokens fused into one
ubatch, NVFP4 weights), is the weight GEMM **compute-bound** (FP4-MMA throughput is the lever →
parity reachable with a better kernel) or **bandwidth-bound** (273 GB/s weight-read is a hard floor →
parity capped)? And given the GB10 occupancy history, can a better FP4-MMA decode GEMM actually reach
vLLM's **391 (dense) / 811 (MoE)** decode-agg tok/s @npl128, or only partway?

Hardware: NVIDIA GB10 / DGX Spark, sm_121 (CC 1210 = `GGML_CUDA_CC_DGX_SPARK`), unified LPDDR5x.
Dev tree `~/llama-paged-dev` (branch `paged`, build-cuda sm_121). All numbers are reasoned from the
committed nsys decomposition + measured GB10 specs + a source read of the FP4-MMA kernel; **no new GPU
benchmarks were run** (track A is on the box).

## 0. Grounded inputs (measured, committed)

| quantity | value | source |
|---|---|---|
| LPDDR5x bandwidth (spec) | **273 GB/s** | `BLACKWELL_KERNEL_GAPS.md`, `VLLM_DECODE_GROUNDING.md` |
| LPDDR5x bandwidth (achieved, batch-1 weight read) | **~216 GB/s** (19 GB / ~88 ms irreducible) | prior batch-1 study |
| FP4 (NVFP4/MXFP4) dense peak | **~427–500 TFLOP/s** (2× BF16; GB10 is 1:1:2 BF16:INT8:FP4) | `BLACKWELL_KERNEL_GAPS.md` §2 |
| BF16 / INT8 peak | ~213 TFLOP/s / ~215 TOPS (INT8 == BF16 on GB10) | same §2 |
| Demonstrated GB10 FP4-MMA efficiency | **~17%** of FP4 peak at prefill M=512 (MXFP4 dense 1153 t/s); **~3% dense / ~35%-of-BW MoE at decode** | `BLACKWELL_KERNEL_GAPS.md` §6, `GDN_DECODE_VERIFY.md` |
| Dense Qwen3.6-27B NVFP4 weights | **18.8 GB** file; ~18 GB matmul tensors | `du` on DGX |
| MoE Qwen3.6-35B-A3B NVFP4 weights | **23.85 GB** file; ~22 GB read/step @npl128 (~98% experts hit) | `du` on DGX |
| Decode step decomposition (dense npl128, nsys, GPU 92.7% busy) | GEMM_weight **59.2%**, act_quant 8.2%, GDN 10.4%, full-attn 1.8%, elementwise/norm/rope 13.5%, embed 2.9%, copy 1.8% | `GDN_DECODE_VERIFY.md` §3a |
| Measured per-step @npl128 | dense **~795 ms** (llama) → **~328 ms** (vLLM); MoE **~384 ms** → **~158 ms** | `VLLM_DECODE_GROUNDING.md` |
| Aggregate decode @npl128 (the parity scoreboard) | dense **161** (llama) vs **391** (vLLM); MoE **333** vs **811** | `QWEN36_NVFP4_BENCH.md` |

`decode_agg = npl / step_s = 128 / step_s`. Crossover formula throughout:
`M* = b · peak / (2 · BW)`, `b` = bytes per weight element. Below `M*` bandwidth-bound, above it
compute-bound.

---

## 1. The kernel-approach decision: TUNE the existing FP4-MMA `mul_mat_q`, do NOT write a cutlass kernel

This is the first thing track B must settle, and the evidence settles it decisively.

| option | verdict | why |
|---|---|---|
| **(A) Tune the existing `mul_mat_q<NVFP4>` FP4-MMA path** | **CHOSEN — the tractable spine** | The kernel already exists, is **bit-exact** (`test-backend-ops MUL_MAT` 1103/1103), is genuine **W4A4** (below), and already **beats vLLM at batch-1 prefill** (MXFP4 1153 t/s vs vLLM's 800 W4A16 — vLLM has no FP4 cubins on sm_121). The deficit is **decode-shape scheduling**, not the math op. Host-side selection + a bounded occupancy tune respects the GB10 lessons and is build-ready against known files/lines. |
| **(B) New cutlass-style SM120 FP4 collective** | **REJECTED** | Repeats the **proven GB10 dead-end**: the from-scratch W4A16 BF16 GEMM hit only ~9–15 TFLOP/s (¼ of MMQ) and was **STOPPED** (`W4A16_MARLIN_KERNEL_PLAN.md`) because deep `cp.async` + XOR-swizzle **collapse GB10 occupancy**. Worse, **CUTLASS's own SM120 grouped block-scaled FP4 GEMM is broken on consumer Blackwell** (garbage/init-fail — CUTLASS #3096/#2800) — it is the exact reason vLLM falls back to **BF16 Marlin** for its MoE on sm_121. "Port cutlass" is not even a working option for the MoE arm. |
| **(C) Marlin-style W4A16 (FP4→BF16 dequant + BF16 HMMA)** | **REJECTED for the win, noted for context** | This is what **vLLM's MoE actually runs** on sm_121 (W4A16, BF16 activations, dequant-in-mainloop). On GB10 **INT8 == BF16 == ½ FP4 rate**, so a BF16-HMMA path concedes the 2× FP4 advantage llama already has. We do not want to *descend* to vLLM's slower arithmetic class; we want to keep the FP4-MMA class and schedule it better. |

**Decision: track B = tune `mul_mat_q<NVFP4>` (dense, `mmq.cu`/`mmq.cuh`) + the grouped `mul_mat_q`
id-branch (MoE, `mmid.cu` + the same `mmq.cuh`).** No new kernel, no rewrite, no descent to BF16.
The win is kernel *engineering around an FP4-MMA llama already possesses*, so there is **no
hardware-instruction wall** — but it is gated by whether MMQ's occupancy-bound design can be pushed
to the bandwidth floor at the thin decode M-tile.

### What "the existing path" actually is (source-read, DGX `ggml/src/ggml-cuda/`)

Decode runs **one `mul_mat_q` per weight, M=128** (all 128 slots' single tokens fused into one
ubatch — confirmed `mul_mat_q(M=128)` in `GDN_DECODE_VERIFY.md`, not 128× M=1). The NVFP4 path:
`mmq.cu` `use_native_fp4` gate (L125) → `quantize_mmq_fp4_cuda` act-quant (L138 dense / L200 id;
**track A's fuse target**) → `mul_mat_q` → `vec_dot_fp4_fp4_mma` (`mmq.cuh:997`) →
`mma_block_scaled_fp4` (`mma.cuh:1126`).

**Confirmed W4A4 (this corrects an earlier "A is 8-bit-class" framing):** `block_fp4_mmq`
(`mmq.cuh:53`) is `uint32_t d4[4]` (four `ue4m3` block scales) + `int8_t qs[4*32]` = **256 FP4 (e2m1)
values packed 2-per-byte**. `quantize_mmq_fp4_cuda` (`quantize.cu:422`) emits FP4 via
`ggml_cuda_float_to_fp4_e2m1`. The MMA is
`mma.sync.aligned.kind::mxf4nvf4.block_scale.scale_vec::4X.m16n8k64.row.col.f32.e2m1.e2m1.f32.ue4m3`
(`mma.cuh:1145`) — **both operands e2m1, ue4m3 block scales**. So llama's dense FP4-MMA path is
already the *same arithmetic class as vLLM's cutlass W4A4 dense*. The `sizeof(block_fp4_mmq) ==
sizeof(block_q8_1_mmq)` static_assert is a shared-tile-footprint convention, **not** an 8-bit
activation. **Consequence: there is no "make activations 4-bit" work to do and no activation-traffic
halving to win — that is already banked. The entire dense deficit is scheduling/occupancy.**

Geometry (`vec_dot_fp4_fp4_mma`): `MMQ_NWARPS=8`, `iter_k=MMQ_ITER_K_FP4=512`, tiles
`tile_A<16,8,int>` (weights, 16 N-rows × 64 FP4-in-K), `tile_B<8,8,int>` (acts, 8 M-cols × 64
FP4-in-K), `tile_C<16,8,float>` (16 N-rows × 8 M-cols), `nfrags = MMQ_TILE_NE_K/tile_A::J`. The M loop
is `for (j0=0; j0<mmq_x; j0 += ntx*tile_C::J)` — M tiled in steps of `tile_C::J=8`.

---

## 2. The roofline — answering the load-bearing question

**Answer: BANDWIDTH-bound on the hardware roofline, but COMPUTE-bound in practice by the kernel's own
under-occupancy. The 273 GB/s is NOT the wall at the parity target.**

### 2a. DENSE Qwen3.6-27B, M=128

`b = 18e9/27e9 = 0.667 B/param`; FLOPs/step `= 2·128·27e9 = 6.91 TFLOP`.

- **Weight-read floor** (18 GB read ONCE for all 128 tokens): @273 GB/s = **65.9 ms → 1,942 tok/s**;
  @216 GB/s = 83 ms → 1,542 tok/s.
- **Crossover** at FP4 peak: `M* = 0.667·500e12/(2·273e9) = 611`. **M=128 ≪ 611 → an ideal FP4 GEMM
  at decode is BANDWIDTH-bound.** At the kernel's *achieved* ~3% efficiency the effective peak
  collapses and drags M* to ≈30, putting the *current* kernel in self-inflicted compute-bound
  territory.
- **Where llama sits:** GEMM = 59.2% × 795 ms = **471 ms = 14.7 TFLOP/s = 2.9% of FP4 peak = 7.1×
  slower than the 66 ms weight-read floor.** Not a bandwidth wall — a kernel running deep in
  compute-bound territory at single-digit efficiency.
- **Where vLLM sits:** step 328 ms ≈ llama's GEMM bucket (471 ms) alone. The **entire 2.42× gap is
  the GEMM.**

### 2b. MoE Qwen3.6-35B-A3B, M=128

@npl128, 128 tok × top-8 / 256 experts ⇒ ~98% experts read ⇒ ~22 GB/step (the full weight set), per-
expert M ≈ **4 tokens**.

- **Weight-read floor:** 22/273 = **80.6 ms → 1,588 tok/s** (@216: 102 ms → 1,255).
- **Compute floor:** only ~3B active params ⇒ 0.77 TFLOP ⇒ 1.5 ms @peak — **trivial. MoE decode is
  purely bandwidth/occupancy-bound, never compute-bound.** The hard part is saturating 273 GB/s while
  feeding ragged M≈4 tiles.
- **Where llama sits:** GEMM = 59% × 384 = **227 ms = 97 GB/s = 35% of peak BW** (occupancy/tile-fill
  loss, not compute).
- **Where vLLM sits:** step 158 ms ≈ grouped Marlin-NvFp4 at the ~80 ms floor + ~78 ms non-GEMM —
  already pushing the MoE BW floor.

**Both weight-read floors (dense ~1,940, MoE ~1,590 tok/s) sit 4–6× ABOVE vLLM's 391/811. Bandwidth
is not the wall; the GB10 FP4-MMA occupancy efficiency is.**

---

## 3. The code-level inefficiencies, and the M-tile asymmetry that drives the whole plan

The selection is `mul_mat_q_case` (`mmq.cuh:4108`): it loops `mmq_x = 8..mmq_x_max(=128) step 8` and
keeps the `mmq_x` that **minimizes `ntiles_x = ceil(ncols_max/mmq_x)`**, stopping at `ntiles_x==1`.
`mmq_y` (the weight-row tile) is pinned at **128** by `get_mmq_y_host` (L143). This produces the
single most important structural fact for track B:

> **`mmq_x` tiles M (tokens / output columns) — shrinking it RE-READS the weights `ntiles_x` times.
> `mmq_y` tiles N (weight rows / output rows) — shrinking it does NOT re-read weights (each weight row
> lives in exactly one row-tile); it only lowers shared footprint and raises occupancy.** The two
> regimes pick opposite knobs:

| | dense decode (M=128, no `expert_bounds`) | MoE decode (per-expert M≈4) |
|---|---|---|
| selection picks | `mmq_x=128` → `ntiles_x=1` → **weights read ONCE** (the one-read optimum) | `mmq_x=128` applied **per expert** → tile ~3% filled |
| shrink `mmq_x`? | **NO — re-reads 18 GB ×`ntiles_x`**, fatal in the BW-bound regime | **YES, FREE** — 1 col-tile/expert regardless, no re-read → strictly occupancy-positive |
| FP4-MMA M-frag fill | **full** (128/`tile_C::J`=16 frag-groups, all live) → no fragment waste | **wasted** (~1 of 8/16 frag-groups live, rest masked tails) |
| BW-neutral occupancy lever | **`mmq_y`↓** (more resident CTAs, weights still read once) — kernel-structure change | **`mmq_x`↓** (toward density ≈8) — host-side template switch |
| dominant loss | **occupancy** at the heavy 128×128 tile (exposed weight-load latency) | **tile-fill** (dense-tuned M-tile applied to ragged M≈4) |

This asymmetry is the spine of the plan: **MoE's lever is host-only `mmq_x`↓ (already landed as patch
0015 auto-cap→64; ideal ≈8–16); dense's lever is `mmq_y`↓ + occupancy, a bounded kernel change.**

The five inefficiencies, ranked:

1. **Separate activation-quant pass (track A's bucket, 8.2%).** `quantize_mmq_fp4_cuda` writes the
   whole activation tensor to `block_fp4_mmq` in a standalone kernel; vLLM fuses `scaled_fp4_quant`
   into the preceding RMSNorm/SiLU epilogue. **Handoff (track A → B):** B must consume A's prequantized
   `block_fp4_mmq` y-tile in place of calling `quantize_mmq_fp4_cuda`, so the fusion saves the
   activation round-trip, not just the launch (see §4.4).

2. **No weight-load software pipeline → exposed latency at thin M (the #1 dense kernel lever).**
   `load_tiles_nvfp4_nvfp4` (`mmq.cuh:946`) does plain global→shared stores → `__syncthreads` →
   `vec_dot_fp4_fp4_mma` (`load_ldmatrix` of A + MMA): a **load→sync→compute→repeat** cadence with **no
   `cp.async` double-buffering** overlapping the next k-block weight load with the current MMA. At
   M=128 the per-tile MMA work is small, so serialized weight-load latency dominates → 2.9% (dense) /
   35%-of-BW (MoE). **Caveat (the GB10 wall):** a *deep* pipeline + XOR-swizzle collapses GB10
   occupancy (`W4A16_MARLIN_KERNEL_PLAN.md`). The fix is **occupancy-first** (raise resident CTAs to
   hide latency via CTA-parallelism), **shallow 2-stage prefetch second**, never Marlin's 4-stage.

3. **`mmq_x` maximized for dense = occupancy-heavy, but pinned by the one-read constraint.** At dense
   decode the 128×128 tile (8 warps, large shared) is low-occupancy on the occupancy-dominated GB10 —
   but you cannot shrink `mmq_x` without doubling the 18 GB weight read. So the dense occupancy fix is
   **`mmq_y`↓** (BW-neutral), not `mmq_x`↓.

4. **MoE per-expert M-tile waste (the structural MoE gap).** The 128-wide (or patch-0015 64-wide)
   tile is applied per expert at density ≈4, so the accumulator is ~3–6% filled and ~1 `tile_C` frag-
   group is live, the rest masked `need_check` tails. Ideal `mmq_x` ≈ tokens/expert ≈ 8 (= `tile_C::J`).
   At ≤1 col-tile/expert this costs **no** extra weight read → strictly occupancy-positive. (This is
   the MoE arm of inefficiency 3; scoped in `MOE_GROUPED_GEMM_SCOPE.md`.)

5. **`iter_k=512` (FP4) couples to occupancy.** The FP4 main loop stages 512 K-elements/iter → larger
   shared footprint → adverse in the occupancy-bound regime. A P2 tuning knob.

**Ruled out (do not chase):** redundant weight reads on the *current* selection (none — dense
`ntiles_x=1`, MoE ≤1 col-tile/expert); stream-K fixup (it *helps* fill the small GB10 grid at thin M);
raw FP4-MMA peak rate (already beats Q4-MMQ and is BW-bound at batch 1 — latency-hiding binds first).

---

## 4. The specific build-ready changes

All against DGX `~/llama-paged-dev/ggml/src/ggml-cuda/`. Every change is gated and defaults to exact
stock behavior until proven.

### 4.1 Dense M-tile / occupancy (the make-or-break)

- **Keep `mmq_x=128` at dense decode** (the one-weight-read optimum; do **not** shrink it — that
  re-reads 18 GB). Lock this as an invariant in P0.
- **Make `mmq_y` decode-selectable** (`get_mmq_y_host`/`get_mmq_y_device`, L143/L157). Today pinned
  128; try **64** (and 96) at decode. `mmq_y` is coupled to `nwarps × tile_C::I` via the MMQ
  static_assert, so this is a **warp/fragment remap** (bounded kernel change), not a pure host switch:
  fewer N-frags per warp or fewer warps → smaller per-CTA shared → **more resident CTAs → latency
  hidden by CTA-parallelism**, with **weights still read once** (BW-neutral). This is the primary
  dense occupancy lever and respects every GB10 rule.
- **Host-only knobs first (P1, zero kernel):** the `mmq_get_granularity_host` choice (L274 — sets
  `rows_per_warp=2·granularity`, `ntx`), and the stream-k-vs-xy-tiling threshold (`launch_mul_mat_q`
  ~L3954, `tiles_efficiency_percent` L4001). Plus one **empirical A/B**: does eating a 2× weight
  re-read at `mmq_x=64` buy enough occupancy to net positive? (Diagnostic: if yes, occupancy is badly
  broken and P2 `mmq_y`↓ has large upside; if no, the tile is already BW-saturated and P2's ceiling is
  lower.) All behind `GGML_CUDA_FP4_MMQ_Y` / `GGML_CUDA_FP4_GRAN` / `GGML_CUDA_FP4_FORCE_STREAMK`.

### 4.2 FP4-MMA fragment usage

- Fragments stay `tile_A<16,8,int>` / `tile_B<8,8,int>` / `tile_C<16,8,float>` — these match the
  `m16n8k64` block-scaled FP4 MMA and must not change (they are the instruction shape). At dense M=128
  all 16 `tile_C::J`-groups are live → **no dense fragment work needed**. The lever is *how many of
  these tiles are resident per SM* (occupancy), set by `mmq_y`/`nwarps`/granularity, not the fragment
  shape.
- MoE: shrink `mmq_x` toward `tile_C::J`=8 so the live frag-group count matches density (§4.3).

### 4.3 MoE M-tile (`MOE_GROUPED_GEMM_SCOPE.md`, partly landed)

- **Patch 0015 already auto-caps `mmq_x`→64 at decode** via per-expert density in `mul_mat_q_case`
  (the `expert_bounds != nullptr` block, L4118-4165; env `LLAMA_MOE_DECODE_TILE`,
  `LLAMA_MOE_DENSITY_MAX`). Tighten the decode tile toward **8–16** (= density) and sweep.
- **Optional [2]: block-padded `mm_ids_helper`** (`mmid.cu`) — pad each expert segment to a multiple
  of the tile, removing `need_check` masked tails and tightening the stream-k schedule. Medium risk
  (scatter + write-back masking); behind `LLAMA_MOE_BLOCK_ALIGN`.

### 4.4 Scale handling + the act-quant fusion handoff (the track A → B ABI contract)

- **Weight scales** (`ue4m3`, one per 16 weights) load in `load_tiles_nvfp4_nvfp4` into `x_sc`
  (`x_u32 + 64 + kbx`), consumed as `scaleA` in `vec_dot_fp4_fp4_mma` and passed as the block-scale
  operand to `mma_block_scaled_fp4`. **No change** — already a first-class MMA scale operand.
- **Activation scales** (`ue4m3`) live in the `block_fp4_mmq` y-tile `d4[4]`, consumed as `scaleB`.
- **The handoff contract:** track B must hold the **`block_fp4_mmq` y-tile layout invariant**
  (`uint32_t d4[4]` ue4m3 scales + `int8_t qs[128]` = 256 packed FP4, `mmq.cuh:53`). Track A's fused
  `rms_norm+mul+nvfp4-quant` producer (task 39) writes exactly this struct; track B's "prequantized
  MMQ consumer" (task 40) makes `mul_mat_q` accept a prebuilt `src1_q8_1` buffer and **skip the
  `quantize_mmq_fp4_cuda` call** (`mmq.cu:138`/`200`). The numerics must be **bit-identical** to the
  unfused path (same `e2m1` rounding, same `ue4m3` block scale per 16) so the parity gate stays green
  with the fusion on or off. B owns the consumer seam; A owns the producer kernel; the `block_fp4_mmq`
  struct is the frozen interface between them.

### 4.5 GB10-fit rules (binding constraints on every kernel change)

- **Small shared mem + high occupancy.** Do **not** add deep `cp.async` stages or XOR-swizzle shared
  layouts — they are exactly what collapsed W4A16 on GB10 (`W4A16_MARLIN_KERNEL_PLAN.md`: a 16 KB
  XOR-swizzle dropped q4_K from 6.63→2.84 TFLOPS).
- **Preserve the skew-pad** (`MMQ_MMA_TILE_X_K_FP4 = 2·MMQ_TILE_NE_K + 8 + 4`, the `% 8 == 4`
  padding, `mmq.cuh:221/233`) — conflict-free `ldmatrix` at ~zero shared cost.
- **Stay on the FP4-MMA path** (`block_fp4_mmq` / `mma_block_scaled_fp4`) — the only path at GB10's
  FP4 = 2× INT8/BF16 rate. Never descend to BF16/INT8 (1:1 on GB10).
- **Occupancy beats a conflict-free-but-wide layout.** Buy latency-hiding with *more resident CTAs*
  (smaller `mmq_y`, smaller shared), not a deeper pipeline.
- Tuning is **empirical** — `nsys` (throughput) is available, **`ncu` is not** on the DGX (no driver
  perms). Sweep configs, measure decode_agg, bracket thermals (same-session cold A/B only).

---

## 5. Correctness / parity gate (every phase)

- **Primary, bit-exact:** `test-backend-ops test -o MUL_MAT -b CUDA0` and
  `test-backend-ops test -o MUL_MAT_ID -b CUDA0` must stay **1103/1103** with the flag set **and**
  unset, and **byte-identical** when unset. The CPU reference is the deterministic oracle; the op test
  is exact (the GB10 greedy-decode non-determinism band applies only to end-to-end, never to the op
  test).
- **Add decode-shape cases if absent:** `type_a ∈ {NVFP4, MXFP4}`, `type_b = F32`, dense **n=128** at
  the real FFN K/N; for `_ID`, `n_mats=128, n_expert_used=8, n_tokens ∈ {8,32,64,128}` **plus ragged
  small-M** (experts with 0/1/2 tokens, `n_tokens` not a multiple of `mmq_x`) — exactly where `mmq_x`/
  `mmq_y` changes and block-pad masking can leak.
- **Fusion-handoff parity (P3):** with track A's fused producer on, the prequantized-consumer path
  must produce dst **identical** to the unfused `quantize_mmq_fp4_cuda` path (same `e2m1`/`ue4m3`
  rounding).
- **End-to-end:** `llama-batched-bench -fa on -npp 512 -ntg 256 -npl 128` on `q36-27b-nvfp4.gguf`
  (dense) and `q36-35b-a3b-nvfp4.gguf` (MoE); confirm decode_agg climbs per §6 and output stays within
  the documented CUDA batch-shape non-determinism band vs the CPU oracle. All scripts **dev-tree-only**.

---

## 6. Phased plan, with expected decode_agg at each phase

Per-step model used (ms @npl128): **dense 795** = GEMM 471 + act 65 + GDN 83 + attn 14 + rest 162;
**MoE 384** = GEMM 227 + act 31 + GDN 38 + attn 8 + rest 81. `decode_agg = 128 / step_s`.

### DENSE (parity target 391)

| phase | work | GEMM ms | step ms | **decode_agg** | **% of vLLM 391** | risk |
|---|---|---:|---:|---:|---:|---|
| **P0** harness | Lock baseline: 1103/1103, decode n=128 perf, nsys window, the 471 ms / 2.9% eff datum. Pin `mmq_x=128` one-read invariant. | 471 | 795 | **161** | 41% | low |
| **P1** host-only tile/grid + re-read A/B | granularity + stream-k threshold sweep; the `mmq_x=64` re-read-vs-occupancy diagnostic. **Honest: small** — `mmq_x` is pinned, so this mostly de-risks P2. | ~400 | ~724 | **~177** | ~45% | low |
| **P2** `mmq_y`↓ + occupancy/shallow-prefetch | The make-or-break: raise resident CTAs (`mmq_y` 128→64, granularity, shallow 2-stage weight prefetch, skew-pad), push GEMM toward the **66–81 ms BW floor (17–21% FP4 eff)**. **KILL-GATE: if eff plateaus <15% (GEMM >110 ms) → dense parity OFF, report partial.** | **66–81** | 390–405 | **316–328** | **81–84%** | **med-high** |
| **P3** co-land track A | Consume A's prequantized `block_fp4_mmq` y-tile; the 65 ms act bucket folds away. | 66–81 | **325–340** | **376–394** | **96–101%** | low |

Dense climb: **161 → ~177 → 316–328 → 376–394** tok/s = **41% → 45% → 81–84% → 96–101% of vLLM 391.**
Robust to the 273-vs-216 GB/s uncertainty (@216 GB/s P3 → ~359 tok/s = 92%). **Parity within error,
contingent on P2 clearing the kill-gate and on A landing.**

### MoE (parity target 811)

| phase | work | GEMM ms | step ms | **decode_agg** | **% of vLLM 811** | risk |
|---|---|---:|---:|---:|---:|---|
| **P0** harness | Lock 1103/1103 + the monotonic `85→1771` batched-bench curve + 227 ms / 35%-BW datum. | 227 | 384 | **333** | 41% | low |
| **P1/P4** MoE `mmq_x`↓ (patch 0015 → tighten to 8–16) | Free per-expert tile shrink (no re-read); reclaim the 3–6% fill waste, raise occupancy. | ~140 | ~297 | **~431** | ~53% | low |
| **P2** block-pad align + occupancy | Remove `need_check` tails, tighten stream-k; push toward the 80 ms floor. | ~100 | ~257 | **~498** | ~61% | med |
| **P3** co-land track A | act bucket (31 ms) folds away; GEMM at the ~80 ms floor. | 80 | **207** | **618** | **76% — CEILING** | low |

MoE climb: **333 → ~431 → ~498 → 618** tok/s = **41% → 53% → 61% → 76% of vLLM 811.** **The 76% is the
hard ceiling from the GEMM track:** even a *perfect* weight-read-floor grouped GEMM leaves llama's
non-GEMM (GDN 38 + attn 8 + rest 81 = 127 ms) at **1.6× vLLM's whole ~78 ms non-GEMM**, so the step
cannot drop below ~207 ms. The remaining ~49 ms to vLLM's 158 ms step is elementwise + host-loop
(GDN state I/O is intrinsic and vLLM pays it identically — `GDN_DECODE_VERIFY.md`), **outside track B.**

### Explicitly NOT in scope (and why)

- A from-scratch W4A16 / CUTLASS SM120 collective — repeats the STOPPED occupancy dead-end and
  CUTLASS's grouped FP4 is broken on sm_121.
- Deep multi-stage `cp.async` / XOR-swizzle — proven to collapse GB10 occupancy.
- "Make activations 4-bit" — already W4A4; no work, no win there.
- The non-GEMM MoE residual (elementwise, host CUDA-graph, GDN bf16 state) — needed for MoE parity but
  **separate tracks**; B owns the GEMM only.

---

## 7. The honest ceiling — does B reach TRUE PARITY?

- **DENSE: TRUE PARITY is PLAUSIBLY REACHABLE, conditional, no margin.** The entire 2.42× gap is the
  GEMM bucket; its ideal floor (66 ms) is 7× below the current 471 ms and is **bandwidth-bound, not
  hardware-capped**. **B (GEMM → BW floor) + A (act-fuse) lands 376–394 tok/s = 90–103% of vLLM 391.**
  The catch: it needs **~17–21% FP4-MMA efficiency at decode M=128**, and GB10 has only demonstrated
  ~17% — and that at the *easier* prefill M=512 tile. It is a **reach, not a lock**, gated by the P2
  occupancy kill-gate and contingent on track A. **GO (conditional).**

- **MoE: full parity is NOT reachable from track B.** Realistic ceiling **~76% of vLLM (618 vs 811)**
  even with a perfect weight-read-floor grouped GEMM, because (1) the MoE floor is the hardest
  grouped-GEMM regime (M≈4/expert, vLLM ships purpose-built Marlin-NvFp4) and (2) ~24% of the step is
  non-GEMM outside this track. Worth doing (333 → ~618, a 1.85× and a real win), but it **cannot
  deliver 811 alone.** **PARTIAL / NO-GO for parity-from-B.**

- **The 273 GB/s is not the ceiling — the GB10 FP4-MMA occupancy efficiency is.** Decode M=128 is a
  *different* regime from the dead W4A16 path: bandwidth/occupancy-bound (saturate LPDDR5x at a thin
  M-tile via resident CTAs), not compute-throughput-bound (pack MMAs). The existing path is already at
  the BW floor at batch 1 (88 ms), so the work is **keeping it bandwidth-bound as M grows to 128**
  (occupancy via `mmq_y`↓ + shallow prefetch), a **tune of a working path**, not the greenfield
  rewrite. The binding risk is whether that occupancy can be bought without tripping the GB10 wall —
  which is exactly what the P2 kill-gate measures.

**Bottom line for the "TRUE PARITY" ask:** GB10 **can** plausibly deliver **dense** decode parity with
vLLM via a tuned FP4-MMA decode GEMM **+ track A**, at the top of the demonstrated efficiency envelope
with no margin. GB10 **cannot** deliver **MoE** decode parity from the GEMM track alone (ceiling ~76%);
MoE parity is a B-plus-non-GEMM program. **Verdict: GO for dense (conditional, B+A, kill-gated),
PARTIAL for MoE.**

---

## 8. One-paragraph summary

The decode GEMM at M=128 is **bandwidth-bound on paper** (crossover M*≈611 ≫ 128) with weight-read
floors 4–6× above vLLM, so **273 GB/s is not the wall** — but llama's FP4-MMA kernel runs at ~3% of
FP4 peak, in **self-inflicted compute-bound territory** (471 ms vs a 66 ms floor). The path is already
**W4A4** and already **beats vLLM at batch-1 prefill**, so the fix is **tuning the existing
`mul_mat_q<NVFP4>`**, not a cutlass rewrite (a proven GB10 dead-end, and broken on sm_121 anyway). The
M-tile asymmetry sets the levers: **dense** is pinned at `mmq_x=128` (one weight read) so its occupancy
win is **`mmq_y`↓ + shallow prefetch** (BW-neutral), while **MoE**'s win is the free per-expert
**`mmq_x`↓** (patch 0015). **Track B (GEMM → BW floor) + track A (fuse act-quant)** plausibly reaches
**90–103% of vLLM dense (391)** — TRUE PARITY on the table for dense, but only at the **top of the
demonstrated GB10 FP4-efficiency envelope (~17–21%)**, with **no margin**, gated by the P2 occupancy
kill-gate. **MoE parity is not reachable from the GEMM alone** (ceiling ~76% of 811), because its floor
sits in the hardest grouped-GEMM regime and ~24% of its step is non-GEMM. **Verdict: GO for dense
(conditional, B+A), PARTIAL for MoE.**
