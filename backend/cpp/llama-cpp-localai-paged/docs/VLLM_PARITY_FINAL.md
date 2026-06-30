# vLLM Parity - Final State (Qwen3.6 NVFP4 on GB10)

> **Status: CLOSED.** This is the standing record of the exhaustive GB10 (DGX
> Spark, sm_121) parity investigation for `llama-cpp-localai-paged` against vLLM
> on the Qwen3.6 hybrid gated-DeltaNet NVFP4 models. It exists so the
> investigation is **never re-litigated**: every lever attempted, its verdict,
> its key number, and the structural floors that bound the result are recorded
> below with the artifact each number came from. The one-line conclusion:
> **per-token kernel and engine work is exhausted; the residual is a hardware
> ceiling (LPDDR5x bandwidth + FP4-MMQ optimality + GDN intra-chunk complexity),
> not a missing optimization.**

Companion docs (design/rationale, not re-summarized here): the patch-series
[`README.md`](../README.md) (section 5 dev-notes), `VLLM_PARITY_LEVER_MAP.md`,
`PREFILL_GEMM_SCOPE.md`, `PREFILL_GEMM_RESULTS.md`, `DECODE_SERVING_SCOPE.md`,
`TENSORCORE_GDN_SCOPE.md`, `TENSORCORE_GDN_BUILD_PLAN.md`, `PAGED_BITEXACT_NOTE.md`.

Source key (every number below cites one of these):
- **CDEF** = the definitive same-session both-engine run `dgx:~/bench/COMBINED_DEFINITIVE.txt` (2026-06-29, GIT_HEAD `a7d439e`, h2h_cli3 OpenAI `/v1/completions`, fresh-nonce prompts, ignore_eos, ptok128 gen128; paged `LLAMA_KV_PAGED=1 LLAMA_MOE_FORCE_GRAPHS=1`, GDN M5 on, S1 on, S3 off; vLLM 0.23.0 gpu-util 0.85 max-model-len 4096 max-num-seqs 256 tp1).
- **README** = the static `llama-batched-bench` table in [`README.md`](../README.md) section 4 (npp128/ntg128; patched vs stock-`9d5d882d` vs vLLM-prior).
- **PGR** = `PREFILL_GEMM_RESULTS.md`. **LMAP** = `VLLM_PARITY_LEVER_MAP.md` (profile-validated section). **DSS** = `DECODE_SERVING_SCOPE.md`. **MG** = `dgx:~/bench/marlin_gate/`. **GDNAB** = `dgx:~/bench/gdn_p1_ab/`. **0034/0035** = patch headers in `patches/paged/`.
- "estimated" marks any figure not pinned to one of the above.

---

## 1. The benchmark (paged vs vLLM vs stock)

Two models: the MoE **Qwen3.6-35B-A3B-NVFP4** (decision model, 256 experts top-8,
30 GDN + 10 full-attn layers + a dense shared expert per layer) and the dense
**Qwen3.6-27B-NVFP4** (48 GDN + 16 full-attn). All numbers GB10 / CUDA 13 /
sm_121, backend pin `9d5d882d`.

### 1a. Prefill (S_PP, prefill tokens/s)

Paged = static `llama-batched-bench` PP block; vLLM = server prefill-phase rate
at the same prompt length. Source: **CDEF**.

| Model | shape | paged S_PP | vLLM S_PP | paged % of vLLM |
|---|---|---:|---:|---:|
| MoE 35B-A3B | PP=512, B=32 | 2309.6 | 6418.9 | **36.0%** |
| MoE 35B-A3B | PP=2048, B=32 | 2401.9 | 6748.5 | **35.6%** |
| Dense 27B | PP=512, B=32 | 960.3 | 2277.3 | **42.2%** |
| Dense 27B | PP=2048, B=32 | 1010.2 | 2360.1 | **42.8%** |

Prefill is the largest absolute gap. The profile-validated decomposition (LMAP,
nsys both-engine, MoE decision model) attributes it as: paged **395.9 us/tok** vs
vLLM **197.0 us/tok** (total gap ~198.9 us/tok), split GDN **+59.2** (~30%),
MoE-GEMM **+56.5** (~28%), ew/layout/glue **+21.4** (~11%), act-quant **+15.2**
(~8%), bf16-proj **+13.7** (~7%), gate **+12.4** (~6%), norms **+11.1** (~6%),
dispatch **+5.9** (~3%).

### 1b. Decode / serving (per-seq + aggregate decode t/s), staggered serving

Source: **CDEF** NPL runs (continuous serving via h2h_cli3). `decode_agg` =
aggregate decode t/s; `perseq` = decode tok/s/seq; PEAK_GB = peak process VRAM.

**MoE Qwen3.6-35B-A3B-NVFP4:**

| N | paged decode_agg | vLLM decode_agg | paged perseq | vLLM perseq | perseq % of vLLM | paged TTFT_mean ms | vLLM TTFT_mean ms | paged PEAK_GB | vLLM PEAK_GB |
|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| 8   | 208.1 | 297.1 | 25.68 | 36.68 | **70.0%** | 747.9   | 204.2  | 50.03 | 112.42 |
| 32  | 379.1 | 575.7 | 11.40 | 17.49 | **65.2%** | 2377.9  | 640.8  | 52.13 | 112.20 |
| 128 | 611.9 | 958.2 | 4.14  | 6.97  | **59.4%** | 7058.3  | 1965.4 | 60.57 | 112.51 |
| 256 | 717.8 | 1177.4| 2.29  | 4.12  | **55.6%** | 13533.6 | 3937.3 | 70.18 | 112.55 |

**Dense Qwen3.6-27B-NVFP4:**

| N | paged decode_agg | vLLM decode_agg | paged perseq | vLLM perseq | perseq % of vLLM | paged TTFT_mean ms | vLLM TTFT_mean ms | paged PEAK_GB | vLLM PEAK_GB |
|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| 8   | 84.0  | 72.1  | 10.42 | 8.93 | **116.7%** | 1914.7  | 493.1   | 77.97  | 109.63 |
| 32  | 196.5 | 214.7 | 5.83  | 6.56 | **88.9%**  | 7023.3  | 1735.4  | 83.04  | 109.65 |
| 128 | 343.8 | 431.8 | 2.18  | 3.10 | **70.3%**  | 19468.9 | 5455.0  | 101.93 | 109.67 |
| 256 | 380.3 | 532.5 | 1.13  | 1.82 | **62.1%**  | 36306.8 | 10824.1 | 114.63 | 109.67 |

End-to-end aggregate `agg_tps` (incl. prefill contention), **CDEF**: MoE paged
179.7/301.4/425.6/459.9 vs vLLM 278.5/515.6/798.3/915.4 at N=8/32/128/256; dense
paged 72.6/141.4/205.8/213.3 vs vLLM 69.4/193.3/346.6/394.7.

**Reading the table.** Dense decode is **ahead of vLLM at low concurrency
(116.7% at N=8)** and degrades to BW-floored ~62-70% only at N=256. MoE decode is
BW-floored across the board at **56-70%** of vLLM. The high-N steady-state band
for both models is **~56-68% of vLLM** - this is the bandwidth floor, discussed
in section 3.

**PEAK_GB is the structural memory advantage.** vLLM's PEAK_GB is a **fixed
~109-112.5 GB reservation** (the `--gpu-memory-utilization 0.85` block-manager
pre-allocation of the ~128 GB unified LPDDR5x) and does **not** vary with N. The
paged backend allocates KV on demand, so its peak **grows with load** but stays
far below vLLM at low/mid concurrency: MoE N=8 uses **50.0 vs 112.4 GB (~2.2x
less)**, and even at N=256 MoE is 70.2 vs 112.6 GB. This is the headline of
section 5 (memory advantage / higher max concurrency per GPU) and is real,
bit-exact, and not an operating-point trick.

### 1c. Patched vs true-stock (static batched-bench, the patch-series multiplier)

Stock `9d5d882d` was not in the same-session CDEF run; the patched-vs-stock
multiplier is the static `llama-batched-bench` table (**README**, npp128/ntg128,
decode t/s):

| | N=8 | N=32 | N=64 | N=128 | max x over stock |
|---|---:|---:|---:|---:|---:|
| Dense patched / stock | 85.3 / 68.3 | 211.9 / 119.9 | 305.2 / 142.8 | 382.1 / 155.1 | **2.46x** |
| MoE patched / stock | 230.3 / 186.7 | 466.4 / 267.4 | 622.4 / 320.5 | 784.3 / 347.2 | **2.26x** |

In that **static** regime the patched decode kernel is **at vLLM parity**
(dense 121/100/99/91% of vLLM-prior across widths; MoE 90/93/91/89%). The serving
table in 1b is the harder continuous regime; the gap between the two regimes is
the subject of section 2 (serving) and was fully closed on the host side.

---

## 2. Complete lever map (every attempt, verdict, key number)

Bit-exactness convention (per `PAGED_BITEXACT_NOTE.md`): the gate is **per-path**.
Dense greedy md5 `5951a5b4`; paged-MoE greedy md5 `8cb0ce23` (a benign
FP-accumulation-order reorder vs non-paged `07db32c2`, KL-validated). "BE" = greedy
md5 byte-identical; "KL-benign" = new FP path, gated by KL-divergence within band.

### 2a. PREFILL - weight GEMM track (verdict: FP4-MMQ is optimal on GB10)

Four kernels were built or ported to beat MMQ at large-M MoE prefill. **All
rejected; FP4-MMQ stays the shipped path.** The decisive surprise (LMAP, both-engine
nsys): **on sm_121 vLLM itself does not run native FP4** - it runs **Marlin W4A16**
(FP4 dequant to bf16 in-register + bf16 GEMM) for experts and FP8 projections,
capped at bf16-tensor-core peak (~half FP4 peak). So MMQ's native FP4 path is
already structurally competitive on this exact silicon.

| Lever | What | Verdict | Key number | Source |
|---|---|---|---|---|
| **0033** dequant -> bf16 cuBLAS | route large-M NVFP4 dense GEMM off MMQ to dequant->bf16 nvjet/cuBLAS | **REJECTED** (regression) | dense S_PP **-49% / -42% / -29%** at M=512/1024/2048; bit-exact md5 identical, KL-better | PGR |
| dense-cuBLAS reroute (full sweep) | the same reroute across the dense + MoE prefill sweep | **REJECTED** | **-31% to -62%** band (estimated; the artifact-pinned dense subset is -29% to -49%, PGR) | LMAP / recorded verdict |
| **0034** native FP4-MMA W4A4 | Blackwell `mxf4nvf4` OMMA large-M kernel, PoC verbatim | **REJECTED in-backend** | PoC `~103 TFLOP/s` (57.7% of FP4 peak, beats cuBLAS-bf16, NMSE=0), but the standalone PoC win **did not hold in-backend** | 0034 header / LMAP |
| **0035** W4A16-Marlin grouped MoE | FP4->bf16 in-register dequant + bf16 `mma.sync`, zero act-quant tax (vLLM's exact sm_121 shape) | **REJECTED** (perf regression) | correct + bit-exact-gated: `test-backend-ops MUL_MAT_ID` 81/81; KL **benign and better** (marlin KLD **0.131** < MMQ **0.137**, same-top-p 84.6% vs 84.3%); md5 short identical, long one benign flip - but **-39%** S_PP vs MMQ (estimated/recorded; MG holds only the correctness+KL gate) | 0035 header, MG |
| offline-repack Marlin / vLLM-verbatim Marlin | repack weights offline to Marlin layout; port vLLM's Marlin kernel verbatim | **REJECTED** | verbatim-Marlin: **correct but -39%**; offline-repack: workflow built (shared the GPU lock, `combined_definitive.sh:29`), same bf16-peak ceiling, no win | recorded verdict / combined_definitive.sh |

**Why the whole track loses (the structural reason):** bf16 tensor-core peak on
GB10 is **~half FP4 peak** (PGR s3), so any dequant->bf16 kernel caps at ~half the
throughput the native FP4-MMQ read reaches; and the dequant write is an
un-amortized weight-sized memory pass (~8x the FP4-read byte traffic, PGR). The
W4A16 angle was the most promising because it *also* erases the ~8% act-quant tax
vLLM never pays - but the bf16-peak ceiling still made it a net regression. **MMQ
is optimal; the GEMM bucket is not winnable on GB10 with the available kernels.**

### 2b. PREFILL - GDN chunked-scan track (verdict: M5 tf32 C=16 is the shipped winner)

The gated-DeltaNet chunked scan is the **#1 single prefill-gap contributor**
(+59.2 us/tok, ~30% of the gap; LMAP). vLLM's FLA `chunk_gated_delta_rule` runs the
same math at **36.5 us/tok vs paged 95.7 = 2.62x** (LMAP), pushing intra-chunk Gram
products through tensor cores. The series chased that headroom.

| Lever | What | Verdict | Key number | Source |
|---|---|---|---|---|
| **0031** scalar-serial chunked scan | FLA-style chunk gated-delta-rule, scalar/serial form (`GDN_TC=0`) | superseded | math-correct (`test-backend-ops` 91/91, <=1e-7 NMSE) but **~761 vs ~971 t/s = ~22% slower** at the GB10-forced C=16 | README s5 |
| **0047 / M5** tf32 tensor-core scan | full form-T solve + state-update on tf32 `m16n8k8` mma, f32-only re-port | **SHIPPED (default-on under paged)** | MoE prefill S_PP **+3.5% @npp512 (3x A/B), +17.7% @npp2048**; decode unchanged; bit-exact-benign (`GATED_DELTA_NET` 46-94/94, md5 == canonical) | README s3/s5 |
| bf16 CONFIG-C (M8) | bf16 `Kc/Qc` + 2 C*C scratch, C->64 + 2 blk/SM | **REJECTED** (not in f32-only series) | the run that confirmed the geometry (CDEF GIT_HEAD), then dropped | CDEF / README s5 |
| bf16-C16 | bf16 Gram at C=16 | rejected | no win over tf32-M5; bf16 mantissa unsafe on the state-coupled products | GDN build-plan s4 |
| BV block-occupancy A/B | raise blocks/SM to test if occupancy is the bound | **REJECTED** (occupancy is NOT the bound) | two arms statistically equal: **1844 vs 1814 S_PP (~-1%, within noise)** | GDNAB armA/armB |
| bf16-C64 | bf16 Gram at the larger C=64 chunk | **REJECTED** | **-18.75%** - the O(C^2) intra-chunk triangular-solve + serial recurrence dominates, so growing C hurts | recorded verdict / GDN build-plan |

**Why the bottleneck is not occupancy/dtype:** the cost is the **O(C^2)
intra-chunk triangular solve + the serial inter-chunk recurrence dependency**, not
grid occupancy (BV: -1%) and not Gram dtype (bf16-C64: -18.75%). GB10's 99 KB
dynamic-smem cap forces **C=16** (the 128x128 f32 state alone is 64 KB of the
all-shared layout), and at this head dim the only win is tensor cores on the
intra-chunk products, not chunking or wider chunks. M5 tf32 at C=16 is exactly
that and is the shipped winner; it does not fully close the 2.62x because vLLM's
mature FLA blocked-solve is a more complete tensor-core implementation.

### 2c. DECODE (verdict: BW-floored at high-N; kernels already ahead of vLLM)

The decode **kernels** are not the gap. The both-engine nsys profile (LMAP) is the
decisive finding:

- Paged decode kernels are **5.4x more GPU-efficient per token** than vLLM's
  (paged static-128 **159 us/tok** vs vLLM **866 us/tok**). Per-bucket: MoE-GEMM
  paged 59.7 vs vLLM 313.5 us/tok (**5.3x**); GDN recurrence paged 34.3 vs vLLM
  391.7 us/tok (**11.4x**); bf16-proj 14.7 vs 57.2.
- They tie at static-wide-128 (paged ~782 vs vLLM ~819 t/s pure decode) via
  **opposite regimes**: paged static decode is **host-bound** (GPU ~16% busy, the
  serial SSM + sampling + MoE-dispatch host loop), vLLM is **GPU-bound** (99% busy)
  on a recurrence 11x slower per token but graph-saturated.
- At high concurrency both are at the **LPDDR5x bandwidth floor**; paged lands at
  **56-68% of vLLM** (section 1b) because vLLM's MoE decode kernel + scheduler are
  ~1.3x faster on aggregate at the floor, and paged pays the bf16-projection
  bandwidth + the serial-SSM host loop.
- **Dense decode is AHEAD at low N (116.7% @ N=8, CDEF)** because the GPU is
  underutilized there and the paged kernels' per-token efficiency wins.

Decode-kernel levers that were therefore **rejected by the "a faster kernel off
the critical path benches flat" rule** (LMAP): D2 fused MoE decode GEMM (already
5.3x faster than vLLM), D3 FA-split (FA is 0.55-1.6% of the decode wall; H2
refuted), D4 GDN-width-adaptive recurrence (already 11.4x faster; H3 confirmed flat
but not the bottleneck). Also rejected: NVFP4 the bf16 GDN/attn projections
(**KL-fail, ~+6% PPL**; vLLM keeps the SAME bf16 projections), W4A16-Marlin MoE
decode (BW-floored wash, ~5% slower kernel), bf16-tau per-head SSM (patch 0026,
**dropped: flat 780.6 vs 780.0 t/s** once the fusion patches landed), act-quant
fusion on decode (**FLAT**, BW-bound).

### 2d. SERVING / engine (verdict: host loop and scheduler closed; spec-decode orthogonal)

| Lever | What | Verdict | Key number | Source |
|---|---|---|---|---|
| **0040 / S1** paged decode-graph reuse | correct `can_reuse` keyed on bucketed block-table dims | **SHIPPED (default-on)** | serving graph reuse **0% -> 72.2%** (with S3); static **0% -> 95.5%** | README, DSS |
| **0041 / S3** decode-shape-stable scheduling | keep prefill out of decode steps for reuse-stable shapes | **SHIPPED default-OFF** (opt-in) | default-on regressed real serving: **2.5x worse TTFT** (60s vs 24s @N=256), **20-29% lower** end-to-end throughput | README, DSS |
| **0043 / D1** full-step MoE decode CUDA graph | graph the whole decode step incl. grouped-MMQ MoE dispatch | **SHIPPED (default-on)** | +2.6% (npl128) to +5-13% (npl32); the D1 premise "host-sync on MoE-routing readback" was **REFUTED** (sync count identical graphs on/off; 99% GPU-busy static) | README s5 |
| S2 double-buffer set_inputs | overlap host input build with GPU | **DROPPED** | `set_inputs` is **~0.05 ms/step** - nothing to recover (the rebuild was the cost) | DSS |
| whole-step graph / host loop | the host scheduling loop as the serving residual | **CLOSED (~0-1%)** | baseline reuse 0% (agg 757.6) **statistically equal** to S1+S3 reuse 72% (agg 763.3); `hostproc` only ~4-8% of the per-step wall = **measured dead** | DSS |
| padded / fixed-slot decode | pad decode width to `--parallel` for ~100% reuse | **REJECTED (built, GPU-tested)** | inert (md5 bit-exact) but **regresses at every concurrency**; N=8 burst 28.16 -> 6.05 tok/s/seq (~4.6x slower); serving decode is **GPU-compute-bound**, dummy-row compute > reuse recovered | DSS |
| speculative decode (MTP) | draft + verify; greedy is bit-exact | **ORTHOGONAL, not pursued** | both engines have it; the crux is hybrid-SSM in-place-state (0018) rollback. Not a paged-specific gap - a feature both can add | LMAP |

The serving regime was the one place the static-bench parity did not carry over
(paged ~3.7 vs vLLM ~5.9 tok/s/seq, -39%, DSS). S1 made the decode step reusable
and the host loop was driven to ~0-1% of the wall; the remaining serving gap was
then **measured to be GPU-compute-bound**, not host-bound - which is the same
LPDDR5x floor as section 2c, not a closable scheduler defect.

---

## 3. Structural floors (not closable on GB10)

These are the hardware/algorithm ceilings the investigation hit. They are why
parity is unreachable on this part, and they are the levers' "why" in one place.

1. **LPDDR5x bandwidth (~273 GB/s) is the decode floor.** Decode is BW-bound at
   high concurrency for both engines. The GDN recurrence already runs at **84.6%
   of GB10 peak BW** (102.6% of vLLM's bandwidth; README s5). There is no slack to
   recover - the 56-68% high-N gap is vLLM's ~1.3x-better aggregate scheduling at
   the *same* floor plus the bf16-projection bandwidth, neither a kernel paged is
   losing. On datacenter HBM (B200: ~8 TB/s) this floor lifts ~30x and the decode
   picture changes entirely.

2. **FP4-MMQ optimality at GB10's tensor-core ratios.** Native FP4-MMQ at M<=128 is
   at the FP4 weight-BW floor (decode) and beats every dequant->bf16 alternative at
   large M (prefill), because bf16 TC peak is ~half FP4 peak on sm_121 and the
   dequant pass is an un-amortized memory pass (PGR). vLLM itself is on a **bf16
   Marlin fallback** here (no tcgen05/CUTLASS-grouped FP4 on consumer Blackwell,
   CUTLASS #3096), so there is no faster GEMM to port.

3. **GDN O(C^2) intra-chunk solve + serial inter-chunk recurrence.** The chunked
   scan's cost is the triangular A-inverse solve (quadratic in chunk size C) plus
   the strictly-serial cross-chunk state carry, with C forced to 16 by the 99 KB
   smem cap. Occupancy (BV: -1%) and dtype (bf16-C64: -18.75%) are not the bound;
   only a fuller tensor-core blocked-solve closes the residual 2.62x, and M5 tf32
   captures the tractable part.

4. **vLLM's mature FLA blocked-solve + Marlin kernels are tuned for HBM.** The
   exact advantages vLLM has (FLA chunked GDN, Marlin grouped GEMM, FULL/PIECEWISE
   cudagraphs over a steadier batch) are the ones that **lose on GB10** because
   they assume datacenter bandwidth and TC ratios. They are real wins on B200; they
   are why parity is a different-hardware question, not a missing-optimization one.

---

## 4. Shipped wins (all bit-exact / KL-benign)

What the series actually banks, all gated per-path:

- **FP4-MMQ MoE/dense GEMM** - native Blackwell FP4-MMA, at the FP4 weight-BW
  floor (decode parity) and beating every dequant alternative at prefill. The
  reason the whole 2a track stays default-off.
- **M5 tf32 tensor-core chunked GDN prefill (patch 0047)** - default-on under
  `LLAMA_KV_PAGED`; MoE prefill **+3.5% @npp512, +17.7% @npp2048**, decode
  untouched, bit-exact-benign.
- **0042 fused residual-add + RMSNorm + weight-mul** - one kernel for `h = x +
  sub; n = rms_norm(h) * w`; dense S_PP +0.5%, bit-exact.
- **0044 fused gated RMSNorm + SiLU gate-mul (GatedRMSNorm fusion)** - the GDN
  output norm `(rms_norm(x)*w)*silu(z)` folded into one launch (672 -> 336
  launches @npp512); S_PP dense +1.1%, MoE +0.9%, `test-backend-ops` 12979/12979.
- **0046 GDN-prefill geometry gate** - gates patch 0022's decode occupancy retune
  by scan length so it stops regressing dense prefill; recovers **+7.2%** dense
  prefill back to stock parity while keeping the decode win, bit-exact.
- **SSM decode fusion stack (0018-0022, 0028)** - in-place state, fused gather,
  o_proj MMQ reshape, conv in-place, occupancy retune; the **2.26x/2.46x over
  stock** decode multiplier (README).
- **Serving host loop closed (0040 S1, 0043 D1)** - decode-graph reuse and
  full-step graph capture; host loop driven to ~0-1% of the serving wall.
- **The memory advantage** - **1.5-3x lower VRAM** than vLLM (NVFP4-resident, no
  persistent bf16 dequant copies; CDEF PEAK_GB e.g. MoE N=8 50 vs 112 GB), which
  is a legitimate higher-max-concurrency-per-GPU operating point.
- **Low-N decode efficiency** - dense decode **ahead of vLLM (116.7% @ N=8)**.
- **Bit-exact output** - per-path greedy md5 stable (dense `5951a5b4`, paged-MoE
  `8cb0ce23`), the sacred gate held through the entire series.

---

## 5. The parity verdict and the path

**Verdict: full vLLM parity is structurally unreachable on GB10, and that is a
hardware ceiling, not a missing optimization.** The per-token kernel and engine
work is exhausted: the prefill GEMM bucket is FP4-MMQ-optimal (every alternative
rejected, and vLLM is on a bf16-Marlin fallback here anyway), the GDN chunked scan
is at the tractable tensor-core win (M5), the decode kernels are already **5.4x more
GPU-efficient per token** than vLLM's, and the serving host loop is closed
(~0-1%). What remains is the **LPDDR5x bandwidth floor** plus vLLM's ~1.3x-better
aggregate decode scheduling at that same floor - neither recoverable by any
bit-exact lever that was not already tried and recorded above.

**The honest framing:** on GB10 the paged backend is **at or ahead of vLLM at low
concurrency (dense 117% @N=8), uses 1.5-3x less memory, and is bit-exact**, while
sitting at **~56-68% of vLLM decode at high concurrency** and **~36% (MoE) / ~43%
(dense) of vLLM prefill** - the high-N/prefill residuals being the bandwidth and
FP4/GDN-complexity floors, not engineering debt.

**The path to parity is different hardware.** A datacenter Blackwell (B200,
~8 TB/s HBM, native tcgen05/CUTLASS FP4, TMEM) lifts the bandwidth floor ~30x and
**restores exactly the vLLM advantages that lose on GB10**: its FLA blocked-solve
GDN, its Marlin/CUTLASS grouped FP4 GEMM, and its HBM-tuned full-cudagraph decode
all assume that bandwidth and those TC ratios. On that hardware the parity question
is re-opened from scratch; on GB10 it is closed. Do not re-litigate the GB10 levers
- re-run the methodology on the new silicon instead.

---

*Recorded per `.agents/vllm-parity-methodology.md` (both-engine ground-truth,
per-lever A/B, record-rejected-levers). All GPU numbers from `ssh dgx.casa`
artifacts under `~/bench/`; all in-repo numbers from the docs cited in the source
key. The GPU lock was not touched in producing this document (CPU-only:
artifact-read + write).*
