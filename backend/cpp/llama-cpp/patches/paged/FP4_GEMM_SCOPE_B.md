# Track B: the FP4-MMA weight-GEMM for GB10 decode parity with vLLM — roofline + go/no-go

Scope only (build-ready plan + honest verdict). **Not implemented in this workflow.** This is the
residual-kernel track after track A (fuse the standalone `quantize_mmq_nvfp4` activation-requant,
the 8.2% bucket) is handled separately. Track B asks the load-bearing question and answers it
quantitatively: at the decode batch shape (M≈128 tokens, NVFP4 weights), is the weight GEMM
**compute-bound** (FP4-MMA throughput is the lever → parity reachable with a better kernel) or
**bandwidth-bound** (273 GB/s weight-read is a hard floor → parity capped)? And given the prior
GB10 occupancy history, can a better FP4-MMA decode GEMM actually reach vLLM's 391 (dense) / 811
(MoE) tok/s, or only partway?

Hardware: NVIDIA GB10 / DGX Spark, sm_121 (CC 1210 = `GGML_CUDA_CC_DGX_SPARK`), unified LPDDR5x.
Dev tree `~/llama-paged-dev` (branch `paged`, build-cuda sm_121). All numbers are reasoned from the
committed nsys decomposition + measured GB10 specs; **no new GPU benchmarks were run** (track A is on
the box).

## 0. The grounded inputs (measured, committed)

| quantity | value | source |
|---|---|---|
| LPDDR5x bandwidth (spec) | **273 GB/s** | `BLACKWELL_KERNEL_GAPS.md`, `VLLM_DECODE_GROUNDING.md` |
| LPDDR5x bandwidth (achieved, batch-1) | **~216 GB/s** (19 GB / ~88 ms irreducible) | prior batch-1 weight-read study |
| FP4 (NVFP4/MXFP4) dense peak | **~427–500 TFLOP/s** (2× BF16; GB10 is 1:1:2 BF16:INT8:FP4) | `BLACKWELL_KERNEL_GAPS.md` §2 (measured) |
| BF16 peak | ~213 TFLOP/s | same |
| Demonstrated GB10 FP4-MMA efficiency | **~17%** of FP4 peak at prefill M=512 (MXFP4 dense 1153 t/s); ~3–7% at decode; ~5% MoE | `BLACKWELL_KERNEL_GAPS.md` §6, `GDN_DECODE_VERIFY.md` |
| Demonstrated GB10 INT8-MMQ efficiency | ~21% of BF16 peak | `BLACKWELL_KERNEL_GAPS.md` §3 |
| Dense Qwen3.6-27B NVFP4 weights | **18.8 GB** file (`q36-27b-nvfp4.gguf`); ~18 GB matmul tensors | `du` on DGX |
| MoE Qwen3.6-35B-A3B NVFP4 weights | **23.85 GB** file; ~22 GB read/step at npl128 (≈98% experts hit) | `du` on DGX |
| Decode step decomposition (dense npl128, nsys, GPU 92.7% busy) | GEMM_weight **59.2%**, act_quant 8.2%, GDN(recurrent+conv) 10.4%, full-attn 1.8%, elementwise/norm/rope 13.5%, embed 2.9%, copy 1.8% | `GDN_DECODE_VERIFY.md` §3a |
| Measured per-step times @npl128 | dense **~795 ms** (llama) → **~328 ms** (vLLM); MoE **~384 ms** → **~158 ms** | `VLLM_DECODE_GROUNDING.md` |
| Aggregate decode @npl128 | dense 161 (llama) vs **391** (vLLM); MoE 333 vs **811** | `QWEN36_NVFP4_BENCH.md` |

Crossover formula used throughout (per-GEMM and whole-model are identical):
`M* = b · peak / (2 · BW)` where `b` = bytes per weight element. Below `M*` the GEMM is
bandwidth-bound; above it, compute-bound.

---

## 1. DENSE Qwen3.6-27B — the roofline at decode M=128

`b = 18e9 B / 27e9 params = 0.667 B/param`. FLOPs/step `= 2·M·P = 2·128·27e9 = 6.91 TFLOP`.

**(a) Weight-read floor** (weights read ONCE for all 128 tokens):
- @273 GB/s: 18 GB / 273 = **65.9 ms/step → 1,942 tok/s ceiling**
- @216 GB/s (achieved): 18 / 216 = **83 ms/step → 1,542 tok/s**

**(b) Compute floor:**
- @FP4 peak 500 TF/s: 6.91 / 500 = **13.8 ms → 9,275 tok/s**
- @17% FP4 (85 TF/s, the demonstrated prefill ceiling): 81 ms → 1,580 tok/s
- @5% FP4 (25 TF/s, measured decode regime): 276 ms → 464 tok/s

**(c) Crossover:**
- At FP4 **peak**: `M* = 0.667·500e12 / (2·273e9) = 611`. **M=128 ≪ 611 → an ideal FP4 GEMM at decode is BANDWIDTH-BOUND.**
- At the kernel's **achieved** efficiency the effective peak collapses, dragging `M*` down: 17% → M*≈104; 5% → M*≈30. So **at its current ~3–7% efficiency the kernel is COMPUTE-BOUND at M=128** (limited by its own poor FP4-MMA throughput), even though the hardware says it should be bandwidth-bound.

**Where llama actually sits:** GEMM = 59.2% × 795 ms = **471 ms**. Achieved = 6.91e12 / 0.471 =
**14.7 TFLOP/s = 2.9% of FP4 peak**. That is **7.1× slower than the 66 ms weight-read floor** and
matches the ~3–7% decode-efficiency band. The 471 ms is not a hardware bandwidth wall — it is the
FP4-MMA kernel running deep in compute-bound territory at single-digit efficiency.

**Where vLLM sits:** step 328 ms → if its native-FP4 cutlass GEMM is at the ~66 ms BW floor, the
GEMM is only ~20% of vLLM's step; the rest (~262 ms) is GDN + full-attn + host. vLLM's **whole step
(328 ms) ≈ llama's GEMM bucket alone (471 ms)** minus a bit. The entire 2.42× gap is the GEMM.

**Dense parity arithmetic** (795 ms = GEMM 471 + act 65 + GDN 83 + attn 14 + rest 162):
- B alone (GEMM → 66 ms BW floor, requires ~21% FP4 eff): step 728→… = 66+65+83+14+162 = **390 ms → 328 tok/s = 84% of vLLM**.
- **B + A** (GEMM 66 ms floor **and** act-quant fused away): 66+83+14+162 = **325 ms → 394 tok/s = 101% of vLLM → PARITY/BEAT.**
- B+A at the softer 17% FP4 (GEMM 81 ms, the *demonstrated* prefill ceiling, not the 21% floor): 340 ms → **376 tok/s = 96% of vLLM.**

**Dense robust band: 90–103% of vLLM**, and it is insensitive to the 273-vs-216 GB/s uncertainty
(at 216 GB/s the floor is 83 ms → step 357 ms → 359 tok/s = 92%). The conclusion holds.

---

## 2. MoE Qwen3.6-35B-A3B — the roofline at decode M=128

At npl128, 128 tokens × top-8 over 256 experts ⇒ P(expert unused) = (1−8/256)^128 ≈ 1.7%, so
**~98% of experts are read** → ~22 GB/step (essentially the full weight set), the same
weight-read regime as dense. The grouped GEMM (`mmid.cu` / `mul_mat_q` id-branch) reads each
routed expert's weight **once** for the ~128·8/256 = **4 tokens/expert** on average.

**(a) Weight-read floor:**
- @273 GB/s: 22 / 273 = **80.6 ms → 1,588 tok/s**
- @216 GB/s: 102 ms → 1,255 tok/s

**(b) Compute floor:** only ~3B active params/token → FLOPs = 2·128·3e9 = 0.77 TFLOP → 1.5 ms @peak.
**Trivial.** MoE decode is **purely bandwidth/occupancy bound**, never compute-bound. The hard part
is that per-expert M ≈ 4: the grouped GEMM must saturate ~273 GB/s while feeding tiny ragged M-tiles
— the regime where ggml's dense-tuned `mmq_x=128` underfills (see `MOE_GROUPED_GEMM_SCOPE.md`).

**Where llama sits:** GEMM = 59% × 384 = **227 ms** → effective BW 22 GB / 0.227 s =
**97 GB/s = 35% of 273** (less compute-bound than dense, but only 1/3 of peak bandwidth — an
occupancy/tile-fill loss, exactly the `MOE_GROUPED_GEMM_SCOPE.md` M-tile finding).

**Where vLLM sits:** step 158 ms ≈ GEMM at the ~80 ms floor (grouped Marlin-NvFp4, 51% of its step)
+ ~78 ms non-GEMM. So vLLM is already pushing the MoE bandwidth floor.

**MoE parity arithmetic** (384 ms = GEMM 227 + act 31 + GDN 38 + attn 8 + rest 81):
- B + A, GEMM → 80 ms floor + act fused: 80+38+8+81 = **207 ms → 618 tok/s = 76% of vLLM.**
- This is the **ceiling from the GEMM track**: even with a *perfect* MoE weight-read-floor GEMM,
  llama's non-GEMM (GDN 38 + attn 8 + rest 81 = 127 ms) is **1.6× vLLM's whole non-GEMM (~78 ms)**,
  so the step cannot drop below ~207 ms. To reach vLLM's 158 ms needs the non-GEMM buckets too
  (GDN state I/O is intrinsic and vLLM pays it identically — `GDN_DECODE_VERIFY.md` — so the
  remaining ~49 ms is elementwise + host loop, **outside track B**).

**MoE band from B+A: ~60–76% of vLLM.** Full MoE parity is **not reachable from the GEMM alone.**

---

## 3. The load-bearing verdict

**Q: compute-bound or bandwidth-bound at M=128?**
At the **hardware** roofline the decode GEMM is **bandwidth-bound** (M=128 ≪ crossover 515–611).
At the **current kernel's** ~3–7% FP4 efficiency it is **compute-bound by its own inefficiency**
(effective M*≈30). The two weight-read floors — **dense ~1,940 tok/s, MoE ~1,590 tok/s** — both sit
**4–6× ABOVE vLLM's 391/811.** So **the 273 GB/s bandwidth is NOT the wall at the parity target.**
There is large bandwidth headroom; the gap is the FP4-MMA kernel achieving single-digit % of peak
where the roofline permits ~20%+ before bandwidth even binds.

**Q: can a better FP4-MMA GEMM reach vLLM — TRUE PARITY?**

- **DENSE: parity is PLAUSIBLY REACHABLE, but at the edge of the demonstrated envelope.** The entire
  2.42× gap is the GEMM bucket; its ideal floor (66 ms) is 7× below the current 471 ms and is
  bandwidth-bound, not hardware-capped. **B (GEMM → BW floor) + A (act-fuse) lands 376–394 tok/s ≈
  vLLM's 391 (90–103%).** The catch: hitting the floor needs **~21% FP4-MMA efficiency at decode
  M=128**, and GB10 has only ever demonstrated ~17% (and that at prefill M=512, a *larger, easier*
  tile). Decode M=128 is a smaller M than prefill, so the same kernel must hold efficiency at a
  thinner tile. This is a **reach, not a lock**: parity is on the table but with **no comfortable
  margin** and **contingent on track A landing too**.

- **MoE: full parity is NOT reachable from track B.** Realistic ceiling **~60–76% of vLLM** (618 vs
  811) even with a perfect weight-read-floor grouped GEMM, because (1) the MoE GEMM floor at M≈4/expert
  demands near-**full** BW saturation in the hardest grouped-GEMM regime, where llama is at 35% of peak
  BW and vLLM ships a purpose-built grouped Marlin-NvFp4, and (2) ~24% of the residual is non-GEMM
  (elementwise + host loop) outside track B. MoE parity needs B **plus** the non-GEMM tracks.

**Q: the GB10 occupancy wall — does it cap this?** Yes, it is the binding constraint, not bandwidth.
History (`W4A16_MARLIN_KERNEL_PLAN.md`, `BLACKWELL_KERNEL_GAPS.md`): the from-scratch W4A16 BF16 GEMM
hit only ~9–15 TFLOP/s (¼ of MMQ) because deep `cp.async` pipelines + XOR-swizzle **collapse GB10
occupancy**; skew-pad + small-shared + high-occupancy won. **Crucially, decode M=128 is a different
regime from that dead path:** it is bandwidth/occupancy-bound, not compute-throughput-bound, so the
lever is **saturating LPDDR5x at a thin M-tile via occupancy**, not packing MMAs. The existing
FP4-MMA path (`block_fp4_mmq` / `vec_dot_fp4_fp4_mma`) is **already at the BW floor at batch 1**
(88 ms irreducible) — so the kernel *can* saturate bandwidth at M=1; the work is keeping it
bandwidth-bound as M grows to 128 instead of degrading to compute-bound at 3% efficiency. That is a
**tune/fix of a working path**, not the dead greenfield W4A16 rewrite.

### Go / No-Go

- **DENSE — GO (conditional).** Build track B as a **decode-M-tile tune of the existing
  `mul_mat_q<NVFP4>` FP4-MMA kernel**, co-delivered with track A. Honest expectation: **90–103% of
  vLLM (parity within error), not a guaranteed beat.** Go condition: it is contingent on reaching
  ~17–21% FP4 efficiency at M=128 (top of the demonstrated GB10 envelope) — set a P2 kill-gate
  (below).
- **MoE — PARTIAL / NO-GO for parity-from-B.** Track B (the M-tile work already scoped in
  `MOE_GROUPED_GEMM_SCOPE.md`) buys MoE → ~60–76% of vLLM and is worth doing, but **cannot deliver
  MoE parity by itself**; do not promise 811. Full MoE parity requires B + the non-GEMM tracks
  (elementwise/host CUDA-graph, GDN state I/O bf16) and is a multi-track effort.

**Bottom line for the "TRUE PARITY" ask:** GB10 **can** plausibly deliver **dense** decode parity
with vLLM via a tuned FP4-MMA decode GEMM **+ track A**, at the edge of the demonstrated efficiency
envelope and with no margin. GB10 **cannot** deliver **MoE** decode parity from the GEMM track
alone (ceiling ~76%); MoE parity is a B-plus-non-GEMM program. The hardware (273 GB/s) is **not** the
ceiling — the GB10 FP4-MMA occupancy efficiency is, and it is a "reach" for dense and a "partial" for
MoE.

---

## 4. Build-ready plan (do NOT implement here)

The kernels already exist; track B is a **tune + fuse of the FP4-MMA `mul_mat_q` path at the decode
M-tile**, not a new kernel. This respects every GB10 occupancy lesson (small shared, high occupancy,
skew-pad, stay on `block_fp4_mmq`; never deep `cp.async` / XOR-swizzle).

### Files (DGX `~/llama-paged-dev/ggml/src/ggml-cuda/`)
- `mmq.cuh` — `block_fp4_mmq` (L53), `load_tiles_nvfp4_nvfp4` (L948), `vec_dot_fp4_fp4_mma` (L997),
  the stream-k `mul_mat_q` kernel + `mul_mat_q_case` / `launch_mul_mat_q` tile selection (~L3320–4055,
  all under `BLACKWELL_MMA_AVAILABLE`).
- `mmq.cu` — dense + id dispatch; `use_native_fp4` gate (L125), `quantize_mmq_fp4_cuda` act-quant
  (L138/L200 — **track A's fuse target**).
- `mmid.cu` — `mm_ids_helper` MoE token-sort (the MoE M-tile lever, scoped in `MOE_GROUPED_GEMM_SCOPE.md`).

### Phases (each ends with: `test-backend-ops -o MUL_MAT[/_ID] -b CUDA0` bit-exact + a decode bench)

| Phase | Work | Expected payoff | Risk |
|---|---|---|---|
| **P0** harness | Capture per-shape baseline at the **decode shape** (`test-backend-ops perf -o MUL_MAT`, type NVFP4, **n=128**, FFN K/N) + nsys decode window. Lock 1103/1103 parity + the 14.7 TFLOP/s baseline. Decode-M is the canonical target, not prefill n=512. | None (gate). | Low |
| **P1** decode M-tile selection (dense) | In `mul_mat_q_case`/`launch_mul_mat_q`, pick `mmq_x`/`mmq_y` from the **decode M=128** shape rather than the prefill-tuned config. M=128 with FP4 N-frag 8 wants a small, occupancy-friendly tile; the prefill `mmq_x=128` likely underfills SM occupancy at decode. Host-side template selection, **zero new kernel**, mirrors `MOE_GROUPED_GEMM_SCOPE.md` [1]. | Lift dense FP4 eff from ~3% toward 10–17%; no extra weight read (one col-tile). | Low |
| **P2** occupancy/pipeline tune | Sweep warps/tile/skew-pad on the FP4-MMA decode kernel to push toward the **66 ms BW floor (~21% FP4 eff)**. Honor GB10 rules: small shared, high occupancy, skew-pad +4, **no** deep cp.async / XOR-swizzle. **KILL-GATE:** if decode FP4 eff plateaus < ~15% (GEMM > ~110 ms) after the sweep, dense parity is off — stop and report partial. | The dense parity make-or-break. Target GEMM 471→66–81 ms. | **Med-high** (the occupancy wall is real; ncu unavailable on DGX → empirical sweep only) |
| **P3** co-land track A | Verify the fused act-quant (track A) composes with the tuned GEMM (the requant folds into the FP4 GEMM prologue, removing the 8.2% bucket). | Dense 376–394 tok/s = 90–103% vLLM. | Low (track A owns the fuse) |
| **P4** MoE M-tile | Land the `MOE_GROUPED_GEMM_SCOPE.md` expert-aware `mmq_x` ([1]) + block-pad align ([2]). | MoE → ~60–76% vLLM (not parity). | Med |

### Parity gate (every phase)
`GGML_CUDA_*` flag set and unset → `test-backend-ops test -o MUL_MAT -b CUDA0` = **1103/1103**,
byte-identical when unset. Add **decode-shape (n=128) + ragged small-M** cases if absent. End-to-end:
`llama-batched-bench -fa on -npp 512 -ntg 256 -npl 128` on `q36-27b-nvfp4.gguf`, confirm decode
agg climbs toward ~376–394 and stays bit-stable vs the CPU oracle (within the GB10 greedy-decode
non-determinism band). All bench/parity scripts **dev-tree-only**.

### Explicitly NOT in scope (and why)
- A from-scratch W4A16 / CUTLASS collective — the FP4-MMA path already exists and is BW-optimal at
  batch 1; rewriting repeats the W4A16 occupancy dead-end (`W4A16_MARLIN_KERNEL_PLAN.md`: STOPPED).
- Deep multi-stage `cp.async` / XOR-swizzle shared layouts — proven to collapse GB10 occupancy.
- The non-GEMM MoE residual (elementwise, host CUDA-graph, GDN bf16 state) — needed for MoE parity
  but **separate tracks**; track B owns the GEMM only.

---

## 5. Honest one-paragraph summary

The decode GEMM at M=128 is **bandwidth-bound on paper** (crossover M*≈611 ≫ 128) with weight-read
floors 4–6× above vLLM, so **273 GB/s is not the wall** — but llama's FP4-MMA kernel runs at ~3% of
FP4 peak, putting it in **self-inflicted compute-bound territory** (471 ms vs a 66 ms floor). Closing
that is the entire dense gap: **track B (tune the FP4-MMA decode M-tile to the BW floor) + track A
(fuse act-quant)** plausibly reaches **90–103% of vLLM dense (391)** — TRUE PARITY is on the table for
dense, but only at the **top of the demonstrated GB10 FP4-efficiency envelope (~17–21%)** and with
**no margin**, gated by the occupancy wall. **MoE parity is not reachable from the GEMM alone**
(ceiling ~60–76% of 811), because its floor sits in the hardest grouped-GEMM regime and ~24% of its
step is non-GEMM work outside this track. Verdict: **GO for dense (conditional, B+A), PARTIAL for MoE.**
</content>
</invoke>
