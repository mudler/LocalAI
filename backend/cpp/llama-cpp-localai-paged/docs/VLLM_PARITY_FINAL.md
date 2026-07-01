# vLLM Parity - Final State (Qwen3.6 NVFP4 on GB10)

> 2026-06-30 update: this document records the earlier final-state verdict. The
> investigation has since been reopened; see `GB10_PARITY_REOPEN_SPEC.md`,
> `GB10_PARITY_PHASE0_RESULTS.md`, and the active `docs/superpowers/plans/`
> Phase 6/Phase 7 files for the current measured state and follow-up scope.

> **Status: CLOSED.** This is the standing record of the exhaustive GB10 (DGX
> Spark, sm_121) parity investigation for `llama-cpp-localai-paged` against vLLM
> on the Qwen3.6 hybrid gated-DeltaNet NVFP4 models. It exists so the
> investigation is **never re-litigated**: every lever attempted, its verdict,
> its key number, and the structural floors that bound the result are recorded
> below with the artifact each number came from. The one-line conclusion:
> **prefill is genuinely capped at 36-43% of vLLM (FP4-MMQ optimality + GDN
> O(C^2) intra-chunk complexity; prefill is not CUDA-graph-replayed, so these are
> real floors, not profiling artifacts); decode-serving is near-parity at ~86% of
> vLLM's true GPU-steady decode (the long-standing ~56% headline was a
> measurement / operating-point artifact, corrected below), with the residual
> ~14% being vLLM's mature fused-Marlin + Triton-elementwise kernels that are not
> cheaply replicable on GB10.**

Companion docs (design/rationale, not re-summarized here): the patch-series
[`README.md`](../README.md) (section 5 dev-notes), `VLLM_PARITY_LEVER_MAP.md`,
`PREFILL_GEMM_SCOPE.md`, `PREFILL_GEMM_RESULTS.md`, `DECODE_SERVING_SCOPE.md`,
`TENSORCORE_GDN_SCOPE.md`, `TENSORCORE_GDN_BUILD_PLAN.md`, `PAGED_BITEXACT_NOTE.md`.

Source key (every number below cites one of these):
- **CDEF** = the definitive same-session both-engine run `dgx:~/bench/COMBINED_DEFINITIVE.txt` (2026-06-29, GIT_HEAD `a7d439e`, h2h_cli3 OpenAI `/v1/completions`, fresh-nonce prompts, ignore_eos, ptok128 gen128; paged `LLAMA_KV_PAGED=1 LLAMA_MOE_FORCE_GRAPHS=1`, GDN M5 on, S1 on, S3 off; vLLM 0.23.0 gpu-util 0.85 max-model-len 4096 max-num-seqs 256 tp1).
- **README** = the static `llama-batched-bench` table in [`README.md`](../README.md) section 4 (npp128/ntg128; patched vs stock-`9d5d882d` vs vLLM-prior).
- **PGR** = `PREFILL_GEMM_RESULTS.md`. **LMAP** = `VLLM_PARITY_LEVER_MAP.md` (profile-validated section). **DSS** = `DECODE_SERVING_SCOPE.md`. **MG** = `dgx:~/bench/marlin_gate/`. **GDNAB** = `dgx:~/bench/gdn_p1_ab/`. **0034/0035** = patch headers in `patches/paged/`.
- **HNP** = the clean, uncontended, **graph-node-traced** both-engine high-N decode profile (2026-06-30): `dgx:~/highN_prof2/*.nsys-rep` (paged, npl=256) + `dgx:~/highN_vllm/*.nsys-rep` (vLLM), captured with `nsys --cuda-graph-trace=node` and decomposed by the **difference method** (per-token cost = ntg=64 profile minus ntg=16 profile). **This supersedes every earlier decode decomposition** (LMAP included): those were taken without `--cuda-graph-trace=node`, which collapses each graph replay into one opaque launch and made the per-kernel decode attribution an artifact (see 2c).
- "estimated" marks any figure not pinned to one of the above.

---

## 1. The benchmark (paged vs vLLM vs stock)

Two models: the MoE **Qwen3.6-35B-A3B-NVFP4** (decision model, 256 experts top-8,
30 GDN + 10 full-attn layers + a dense shared expert per layer) and the dense
**Qwen3.6-27B-NVFP4** (48 GDN + 16 full-attn). All numbers GB10 / CUDA 13 /
sm_121. The current backend pin is `0ed235ea2c17a19fc8238668653946721ed136fd`;
the CDEF benchmark artifact itself records the dev-tree commit that produced
those binaries.

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
(116.7% at N=8)**. The high-N percentages here (perseq ~56%, decode_agg ~61% at
N=256) are **server-window** numbers and **understate true engine parity**: they
divide the paged serving rate by vLLM's *prefill-overlap-inflated* server rate.
The corrected, graph-node-traced decomposition (section 2c, **HNP**) shows paged
decode at **~86% of vLLM's true GPU-steady decode**, with the remaining
server-window gap being an S3-recoverable serving graph-reuse overhead (2d). The
earlier "this is just the bandwidth floor / vLLM pays equally" reading was a
**profiling artifact** and is corrected in 2c.

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
| BV block-occupancy A/B (tf32) | raise blocks/SM to test if occupancy is the bound | **REJECTED** (occupancy is NOT the bound; latency is wave-hidden) | two arms statistically equal: **1844 vs 1814 S_PP (-1.04%, within noise)** | GDNAB armA/armB |
| bf16-C64 | bf16 Gram at the larger C=64 chunk | **REJECTED** | **-18.75%** - the O(C^2) intra-chunk triangular-solve + serial recurrence dominates, so growing C hurts | recorded verdict / GDN build-plan |
| Phase 10 C32 slab M5 | C=32 with two `dv_tile=64` slabs, default-off `GDN_C32_SLAB=1` | **REJECTED** | md5-clean after tail-row zeroing, but S_PP regressed: MoE 2048 **2430.32 -> 2054.86**, dense 2048 **1019.25 -> 903.73** | phase10 gates/ab |
| Phase 11 QS-early M5 | move `QS = Qc * S0` earlier, default-off `GDN_M5_QS_EARLY=1` | **REJECTED** | md5-clean, but S_PP regressed slightly: MoE 2048 **2441.54 -> 2420.26**, dense 2048 **1021.06 -> 1015.77** | phase11 gates/ab |
| Phase 12 shared-A/Ai cost model | f32 Ai scratch shared across two C32 value slabs | **GO to one prototype** | BT32 f32 scratch at npp2048,npl32: MoE 256 MiB / 768 MiB Ai traffic; dense 384 MiB / 1152 MiB Ai traffic | phase12 cost model |
| Phase 13 Global-Ai32 | precompute f32 Ai once, consume from two C32 `dv_tile=64` slabs | **REJECTED** | md5-clean, but S_PP regressed: MoE 2048 **2425.10 -> 2097.76**, dense 2048 **1016.14 -> 918.19** | phase13 gates/ab |

**Why the bottleneck is not occupancy/dtype:** the cost is the **O(C^2)
intra-chunk triangular solve + the serial inter-chunk recurrence dependency**, not
grid occupancy (BV: -1.04%, latency is wave-hidden) and not Gram dtype (bf16-C64:
-18.75%). GB10's 99 KB
dynamic-smem cap forces **C=16** (the 128x128 f32 state alone is 64 KB of the
all-shared layout), and at this head dim the only win is tensor cores on the
intra-chunk products, not chunking or wider chunks. M5 tf32 at C=16 is exactly
that and is the shipped winner; it does not fully close the 2.62x because vLLM's
mature FLA blocked-solve is a more complete tensor-core implementation.

Post-record caveat closed: Phase 13 tested the one permitted
`GDN_GLOBAL_AI32=1` prototype. It was correctness-clean but slower, so GDN kernel
work on GB10 should stop rather than moving to f16 Ai or additional local
reorders.

### 2c. DECODE / serving (verdict: near-parity at ~86% of vLLM's true GPU-steady decode; the earlier "BW-floored / vLLM pays equally" was a profiling artifact)

**Methodology correction - why every earlier decode decomposition was wrong.**
Decode runs as a **replayed CUDA graph**. `nsys` *without* `--cuda-graph-trace=node`
collapses each graph replay into a **single opaque launch**, so the per-kernel
attribution in every prior decode profile (the "paged 159 us/tok, GPU ~16% busy,
host-bound, 5.4x more GPU-efficient per token" picture, and the conclusion that the
high-N gap was a pure bandwidth floor vLLM pays equally) was an **artifact of graph
collapse, not real per-token cost**. The correct method, used for the numbers below
(**HNP**, clean uncontended node, 2026-06-30), is `nsys --cuda-graph-trace=node`
plus the **difference method**: per-token cost = the ntg=64 profile minus the
ntg=16 profile, isolating per-token-linear work from fixed per-step overhead. Under
this method **paged decode at npl=256 is 99% GPU-busy (GPU-idle only 1.4%), NOT
host-bound** - the opposite of the collapsed-graph reading. This supersedes the
LMAP decode decomposition.

**The real per-token decomposition (paged, npl=256, HNP)** - GPU-steady ~1082
us/tok (924 t/s):

| Bucket | us/tok | % of decode | Note |
|---|---:|---:|---|
| GDN recurrent scan | 553 | **51%** | **LINEAR in batch** - the dominant cost; shared BW floor (below) |
| NVFP4 expert GEMM | 254 | 23% | amortizes with batch |
| bf16 projections | 73 | 7% | |
| elementwise | 57 | 5% | |
| SSM conv | 31 | 3% | |
| rest | small | - | |
| GPU-idle | - | **1.4%** | not host-bound |

**The gap reconciled (the numbers must sum).** The headline N=256 figures (perseq
~56%, decode_agg ~61%, section 1b) were paged-**server** **718** over vLLM-**server**
**1177**. But the vLLM server number is **inflated ~8 pts**: vLLM's true GPU-steady
decode is **1078 t/s**, and its chunked-prefill overlap inflates the
server-measured decode window. The reconciled chain:

| Measurement | t/s | % of vLLM-server (1177) |
|---|---:|---:|
| vLLM server (CDEF) | 1177 | 100% |
| vLLM **true GPU-steady** decode | 1078 | 92% |
| llama **GPU-steady** decode | 924 | 78.5% (**= 86% of vLLM's true 1078**) |
| llama server (CDEF) | 718 | ~60.7% (61%) |

So **vs vLLM's true GPU-steady decode, paged is ~86%, not ~56%.** The ~56% headline
conflated two distinct things: vLLM's prefill-overlap-inflated server window, and
the paged serving graph-reuse overhead. The **~17 pt** drop from llama GPU-steady
(78.5%) to llama server (60.7%) is exactly that **serving graph-reuse overhead**,
which is **S3-recoverable** (2d).

**GDN is a shared BW floor where paged is ahead.** The GDN recurrent scan moves
**~32 GB/step of f32 recurrent-state traffic**; paged runs it at **83% of the
273 GB/s LPDDR5x peak vs vLLM's 79%**. Both engines' high-N sublinearity (only
**1.17-1.18x throughput for a 2x batch**) comes from this **shared** floor - it is
not a paged-specific loss, and paged is the faster of the two on it.

**The residual ~14 pt GPU-steady gap is real but not cheaply closable.** vLLM's
GPU-steady 1078 vs paged 924 decomposes into two buckets: the **MoE expert path
(~+11 ms)** - vLLM's fused Marlin persistent-tiling vs ggml's separate act-quant +
MMQ - and **elementwise (~+10 ms)** - vLLM fuses it into one Triton kernel. Both
fusions were attempted and rejected (table below). Closing the residual needs
vLLM's mature Marlin tiling (our own ggml Marlin port already lost **-19.6%**) plus
multi-stream overlap (hard inside a single-stream CUDA graph): **low-EV,
multi-week, GB10-uncertain**.

**Decode / fusion levers (verdicts).**

| Lever | What | Verdict | Key number | Source |
|---|---|---|---|---|
| act-quant folded into ggml MMQ | erase the act-quant pass by quantizing the y-operand inside the MoE expert MMQ kernel (vLLM's fused-Marlin single-pass shape) | **REJECTED** (regression) | **-79.4%**: ggml MMQ re-quantizes the y-operand **once per weight-row-tile x stream-k split**, with no tensor cores for the inline quant - structural, ggml MMQ lacks vLLM's persistent single-pass tiling | HNP / recorded verdict |
| norm + quant + silu fusion | fold the elementwise path into one launch (vLLM's Triton kernel) | **REJECTED** (architecturally infeasible) | `ggml_cuda_can_fuse` cannot express it: FP4 quant is a **mul_mat-internal prologue, not a cgraph node**; the norm is already fused (0042/0044); silu is separated from the norm by **2 GEMMs + the router** | recorded verdict |
| Q8_0 / FP8 projection | quantize the bf16 GDN/attn projections (premise: vLLM uses FP8 here) | **REJECTED** (regime error, not premise error) | vLLM **does** use FP8 projections (confirmed from `hf_quant_config.json` `MIXED_PRECISION`), but at N=128/256 projections are only **~12% of the decode stream**, so this closes **<=6%, not the gap** | HNP / hf_quant_config.json |
| NVFP4 the bf16 GDN/attn projections | drop projections to NVFP4 (more aggressive than FP8) | **REJECTED** | **KL-fail, ~+6% PPL**; vLLM keeps the SAME bf16/FP8 projections, never NVFP4 | LMAP |
| W4A16-Marlin MoE decode | Marlin grouped expert GEMM on the decode path | **REJECTED** | BW-floored wash, **~5% slower** kernel | LMAP |
| bf16-tau per-head SSM (0026) | per-head bf16 tau on the SSM decode | **DROPPED** | flat **780.6 vs 780.0 t/s** once the fusion patches landed | README s5 |
| D3 FA-split / D4 GDN-width-adaptive | the older "off critical path" decode levers | **SUPERSEDED reasoning** | originally rejected via the now-debunked "5.4x faster / host-bound" reading; under HNP the GDN scan **is** the critical path (51%), but it is the shared BW floor where paged already leads (83% vs 79%), so neither is a win | HNP |

**Dense decode is AHEAD at low N (116.7% @ N=8, CDEF)** because the GPU is
underutilized there and the paged path's per-token efficiency wins; this is the one
operating point where paged is unambiguously faster than vLLM.

### 2d. SERVING / engine (verdict: host loop and scheduler closed; spec-decode orthogonal)

| Lever | What | Verdict | Key number | Source |
|---|---|---|---|---|
| **0040 / S1** paged decode-graph reuse | correct `can_reuse` keyed on bucketed block-table dims | **SHIPPED (default-on)** | serving graph reuse **0% -> 72.2%** (with S3); static **0% -> 95.5%** | README, DSS |
| **0041 / S3** decode-shape-stable scheduling (`LLAMA_PAGED_DECODE_STABLE`) | keep prefill out of decode steps for reuse-stable shapes | **SHIPPED default-OFF** (opt-in throughput-max knob) | recovers the **~17 pt serving graph-reuse overhead** (llama server 60.7% -> toward GPU-steady 78.5%, 2c) at a TTFT cost; default-on regressed real serving: **2.5x worse TTFT** (60s vs 24s @N=256), **20-29% lower** end-to-end throughput, hence opt-in | README, DSS, HNP |
| **0043 / D1** full-step MoE decode CUDA graph | graph the whole decode step incl. grouped-MMQ MoE dispatch | **SHIPPED (default-on)** | +2.6% (npl128) to +5-13% (npl32); the D1 premise "host-sync on MoE-routing readback" was **REFUTED** (sync count identical graphs on/off; 99% GPU-busy static) | README s5 |
| S2 double-buffer set_inputs | overlap host input build with GPU | **DROPPED** | `set_inputs` is **~0.05 ms/step** - nothing to recover (the rebuild was the cost) | DSS |
| whole-step graph / host loop | the host scheduling loop as the serving residual | **CLOSED (~0-1%)** | baseline reuse 0% (agg 757.6) **statistically equal** to S1+S3 reuse 72% (agg 763.3); `hostproc` only ~4-8% of the per-step wall = **measured dead** | DSS |
| padded / fixed-slot decode | pad decode width to `--parallel` for ~100% reuse | **REJECTED (built, GPU-tested)** | inert (md5 bit-exact) but **regresses at every concurrency**; N=8 burst 28.16 -> 6.05 tok/s/seq (~4.6x slower); serving decode is **GPU-compute-bound**, dummy-row compute > reuse recovered | DSS |
| speculative decode (MTP) | draft + verify; greedy is bit-exact | **REJECTED for current GB10 serving** | Phase 14 passed safety, but Phase 15 direct serving A/B regressed at every tested concurrency (n128 decode agg 662.4 -> 138.5 tok/s) despite high acceptance; likely breaks paged decode graph reuse (`graphs reused` 361 -> 1). Not a parity lever unless a future graph/batch-shape fix changes this result | LMAP |

The serving regime was the one place the static-bench parity did not carry over
(paged ~3.7 vs vLLM ~5.9 tok/s/seq, -39%, DSS). S1 made the decode step reusable
and the host loop was driven to ~0-1% of the wall. The graph-node-traced HNP
profile (2c) then resolves the remaining serving gap into two parts: the **~17 pt
serving graph-reuse overhead** (S3-recoverable via this knob) and the **~14 pt
GPU-steady kernel gap** vs vLLM's true 1078 t/s (vLLM's fused-Marlin MoE + Triton
elementwise, 2c). Both are real; neither is the "pure LPDDR5x floor, vLLM pays
equally" story the collapsed-graph profile implied.

---

## 3. Structural floors (not closable on GB10)

These are the hardware/algorithm ceilings the investigation hit. They are why
parity is unreachable on this part, and they are the levers' "why" in one place.

1. **LPDDR5x bandwidth (~273 GB/s) bounds the GDN recurrent scan - a *shared*
   floor where paged leads.** The GDN scan is the dominant decode bucket (553
   us/tok, 51%, LINEAR in batch; HNP) and moves ~32 GB/step of f32 recurrent
   state; paged runs it at **83% of the 273 GB/s peak vs vLLM's 79%**, and both
   engines' high-N sublinearity (1.17-1.18x for a 2x batch) is this same floor.
   This is **not** the explanation for the high-N server-window gap: the
   graph-node-traced HNP profile (2c) shows paged decode **99% GPU-busy at ~86% of
   vLLM's true GPU-steady decode**, with the server-window ~56% being a
   prefill-overlap measurement artifact (~8 pt) plus an S3-recoverable graph-reuse
   overhead (~17 pt), not a bandwidth floor vLLM pays equally. The residual ~14 pt
   GPU-steady gap is kernel maturity (point 4 below + 2c), not bandwidth. On
   datacenter HBM (B200: ~8 TB/s) this GDN floor lifts ~30x.

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

4. **vLLM's mature fused kernels (FLA blocked-solve, fused-Marlin MoE, Triton
   elementwise) are tuned for HBM.** They are the source of both the prefill cap
   and the residual ~14 pt decode GPU-steady gap (2c): the fused-Marlin
   persistent-tiling MoE path (~+11 ms) and the single-kernel Triton elementwise
   (~+10 ms). The matching ggml fusions were rejected as infeasible or regressive
   (2c): folding act-quant into MMQ regressed -79.4% (no single-pass tiling), and
   norm+quant+silu cannot be expressed via `ggml_cuda_can_fuse`. The FLA chunked
   GDN, Marlin grouped GEMM, and FULL/PIECEWISE cudagraphs all assume datacenter
   bandwidth and TC ratios; they are real wins on B200, which is why closing the
   residual is a different-hardware question (mature kernels + multi-stream
   overlap), not a missing single-lever optimization.

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

**Verdict (revised): PREFILL is genuinely capped on GB10; DECODE-SERVING is near
vLLM parity (~86% of its true GPU-steady decode), with the long-standing ~56%
headline now identified as a measurement / operating-point artifact.** Prefill
sits at **36% (MoE) / 43% (dense)** of vLLM and is a real floor (FP4-MMQ optimality
+ GDN O(C^2) intra-chunk complexity; prefill is **not** CUDA-graph-replayed, so
unlike decode these numbers are not profiling artifacts). The GDN chunked scan is
at its tractable tensor-core win (M5) and the prefill GEMM bucket is FP4-MMQ-optimal
(every alternative rejected; vLLM is itself on a bf16-Marlin fallback here). For
decode, the graph-node-traced HNP profile corrects the record: paged decode is
**99% GPU-busy at ~86% of vLLM's true GPU-steady decode (924 vs 1078 t/s)**; the
~56% server-window figure was vLLM's prefill-overlap inflation (~8 pt) plus the
S3-recoverable serving graph-reuse overhead (~17 pt). The residual **~14 pt**
GPU-steady gap is vLLM's mature fused-Marlin MoE (~+11 ms) and Triton elementwise
(~+10 ms) kernels; the matching ggml fusions were rejected (act-quant-into-MMQ
-79.4%, norm+quant+silu infeasible), and closing the residual needs mature Marlin
tiling (our port lost -19.6%) plus multi-stream overlap - low-EV, multi-week,
GB10-uncertain, not a free bit-exact lever.

**The honest framing:** on GB10 the paged backend is **at or ahead of vLLM at low
concurrency (dense 117% @N=8), uses 1.5-3x less memory, and is bit-exact**, runs
high-N decode at **~86% of vLLM's true GPU-steady decode** (the ~56% server-window
number is a measurement artifact, 2c), and sits at **~36% (MoE) / ~43% (dense) of
vLLM prefill**. The prefill residual is a real FP4-MMQ + GDN-O(C^2) floor; the
~14 pt decode residual is vLLM's mature fused kernels, not engineering debt and not
a cheap lever.

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
