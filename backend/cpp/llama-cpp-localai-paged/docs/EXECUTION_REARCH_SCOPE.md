# EXECUTION_REARCH_SCOPE: porting vLLM's execution architecture into the paged fork (additive program)

Status: scope, not a result. This document reopens the GB10 vLLM-parity work on a
new thesis and lays out a phased, additive, falsifiable program. It supersedes the
per-lever "hardware floor" framing of [`VLLM_PARITY_FINAL.md`](VLLM_PARITY_FINAL.md)
*where that framing was wrong*, and keeps it *where it was right*. Read
[`VLLM_PARITY_FINAL.md`](VLLM_PARITY_FINAL.md),
[`VLLM_PARITY_LEVER_MAP.md`](VLLM_PARITY_LEVER_MAP.md),
[`PARITY_HANDOFF.md`](PARITY_HANDOFF.md) and
[`PREFILL_GEMM_RESULTS.md`](PREFILL_GEMM_RESULTS.md) before acting on anything here.

Target model + hardware are unchanged: Qwen3.6 NVFP4 (dense 27B + MoE 35B-A3B hybrid
GDN-SSM) on GB10 / DGX Spark (sm_121a, mma.sync only, LPDDR5x ~273 GB/s). Reference
engine is vLLM v1 on the same GB10.

---

## 1. Reframing: the 2-3x is software architecture, not silicon

The prior two campaigns (June, then a 141-phase reopened one) A/B'd every single kernel
and every single execution-model boundary in isolation and rejected them, and concluded
"hardware floor". That conclusion is a **per-lever** verdict and it conflated two
different kinds of floor. On the *same silicon* vLLM is 2-3x faster at prefill and
serving; a same-silicon multiple is by definition a software-architecture delta, not a
hardware limit. The correct reframe:

**Truly shared-hardware floors (bind vLLM too; not engineering debt, do not re-litigate):**

1. **The high-N GDN recurrent-scan bandwidth plateau.** The scan moves ~32 GB/step of
   f32 recurrent state, is 51% of decode and LINEAR in batch; both engines show the
   same sublinearity (1.17-1.18x throughput for a 2x batch). Paged runs it at **83% of
   the 273 GB/s LPDDR5x peak vs vLLM's 79%** - on this one floor paged already **leads**.
   Lifts ~30x on B200 HBM, not on GB10.
2. **bf16 tensor-core peak = ~half FP4 peak on sm_121**, with no tcgen05 / CUTLASS
   grouped-FP4 on consumer Blackwell (CUTLASS #3096). This is why vLLM itself runs a
   bf16-Marlin fallback here and why native FP4-MMQ is optimal; it caps any
   dequant-to-bf16 alternative for **both** engines.
3. **The GDN O(C^2) intra-chunk triangular solve under the 99 KB smem cap forcing C=16.**
   Occupancy is not the bound (block-vote A/B: -1.04%); dtype is not the bound
   (bf16-C64: -18.75%; explicit blocked-inverse: 0.59x of direct solve, Phase74). Joint
   algorithm-plus-hardware ceiling.

**ggml-architecture-conditional floors (the real "same-silicon 2-3x"; this program's target):**

1. **The per-cgraph-node materialize-everything executor.** Root cause of the -79.4%
   act-quant-into-MMQ failure, the inexpressible norm+quant+silu fusion, the
   +21.4 us/tok convert/glue tax, and all six MoE-transplant regressions. vLLM's
   persistent kernels + Triton fusions + expert-major pipeline never create these
   intermediates. Unclosable one-boundary-at-a-time; must be a complete fused rewrite.
2. **The prefill grouped-GEMM tiling quality** (+56.5 us/tok). ggml grouped-MMQ shatters
   into ragged small-M-per-expert tiles; vLLM's aggregated expert-major grouped GEMM
   keeps tensor cores full at the *same* bf16-peak ceiling. Ceiling is hardware; the
   tiling maturity gap to it is software.
3. **The ~17 pt serving graph-reuse overhead.** vLLM's padded/bucketed decode shapes +
   piecewise CUDA graphs keep the GPU fed; ggml rebuilds/re-captures on batch-shape
   churn. Largely closed by S1/D1; residual is S3-recoverable, bit-exact-safe.
4. **The ~8 pt vLLM server-number inflation** is pure measurement (chunked-prefill
   overlap inflating vLLM's own server window), not a floor at all.

**Goal of this program:** port vLLM's **execution architecture** (token-budget scheduler,
persistent-buffer full-graph execution, expert-major single-launch MoE, persistent-CTA
weight-reuse GEMM, chunked blocked-solve GDN, bf16-resident activation stream) into the
fork **additively** (new files, narrow additive hooks, default-off env gates), and let
the existing CUDA-only kernels slot in underneath. The failed ports failed not because
their kernels are GB10-hostile (mostly they are portable) but because each was dropped
one boundary at a time into an executor that materializes every intermediate to LPDDR5x,
so each partial port paid the temp-traffic cost without the persistent-kernel benefit.

---

## 2. Why vLLM is faster on GB10 (ranked attribution + port forensics)

All numbers are tagged. Source keys: **CDEF** = `dgx:~/bench/COMBINED_DEFINITIVE.txt`
(same-session both-engine, GIT_HEAD a7d439e). **LMAP** =
[`VLLM_PARITY_LEVER_MAP.md`](VLLM_PARITY_LEVER_MAP.md) profile-validated section
(both-engine nsys). **HNP** = graph-node-traced decode profile
(`--cuda-graph-trace=node`; `dgx:~/highN_prof2/`, `~/highN_vllm/`). **PGR** =
[`PREFILL_GEMM_RESULTS.md`](PREFILL_GEMM_RESULTS.md). **VPF** =
[`VLLM_PARITY_FINAL.md`](VLLM_PARITY_FINAL.md). **PH** =
[`PARITY_HANDOFF.md`](PARITY_HANDOFF.md).

### 2a. Prefill (paged 395.9 vs vLLM 197.0 us/tok; gap 198.9; MoE 35B-A3B decision model)

Prefill is NOT CUDA-graph-replayed, so these buckets are real per-token costs.

| Rank | Bucket | Delta us/tok | % gap | Mechanism (paged vs vLLM) |
|---|---|---:|---:|---|
| 1 | GDN prefill scan | +59.2 | 30% | hand f32 chunked scan `gdn_core` 95.7 vs vLLM FLA `chunk_gated_delta_rule` 36.5 = **2.62x**; O(C^2) intra-chunk solve + serial cross-chunk carry, C forced to 16 by 99 KB smem |
| 2 | GEMM pipeline | +56.5 | 28% | grouped-MMQ (FP4 wt x Q8_1 int8 act) 105 vs Marlin W4A16 (FP4->bf16 in-register + bf16 mma) 48.5 = **2.16x**; loses on ragged small-M-per-expert tiles under-utilizing TC, NOT a GEMV collapse |
| 3 | activation-dtype boundary tax | +21.4 glue + 15.2 act-quant = **+36.6** | 19% | `convert_dtype` 6.3% + `concat` 2.9% of wall are pure dtype/layout glue vLLM's bf16 stream never materializes; plus act-quant vLLM structurally does not pay (W4A16 = bf16 activations, zero act-quant) |
| 4 | projections + norms + gate | bf16-proj +13.7, gate +12.4, norms +11.1 = **+37.2** | 19% | paged runs these as separate memory-bound ggml ops; vLLM keeps FP8 projections and fuses norm/gate into Triton kernels |
| 5 | scheduler / MoE dispatch | +5.9 | 3% | explicit argsort+mm_ids+gather_mmq 8.6 vs 2.7; both cheap. vLLM runs its own count_and_sort/moe_align, does NOT fuse dispatch into the GEMM epilogue on GB10 |

Sum of deltas = 195.4 ~ 198.9 (rounding): **the buckets close the measured gap.**
The executor-model tax is not a separate row; it is the *cause* of buckets 2, 3, 4.
Prefill S_PP ratios (CDEF, batched B=32): MoE **36.0% / 35.6%** of vLLM at PP=512/2048;
dense **42.2% / 42.8%**.

**Note on the retired 232/68 claim.** `PREFILL_GEMM_SCOPE.md` flagged the "GEMM bucket
232 vs 68 us/tok" numbers as uncommitted early ground-truth needing re-confirmation.
The both-engine nsys re-confirmation revised them to **105 vs 48.5** (2.16x), and
reassigned the missing ~127 to the paged GDN scan (95.7 us/tok) and act-quant
(19 us/tok). **GDN scan, not GEMM, is the #1 prefill contributor.** Any reasoning that
still cites 232/68 or "GEMM is ~51% of the gap" is stale.

### 2b. Serving / decode (the ~56% headline reconciled to ~86%)

The old "paged decode 159 us/tok, GPU ~16% busy, host-bound" was a **measurement
artifact**: `nsys` without `--cuda-graph-trace=node` collapses each replayed decode
graph into one opaque launch. Re-profiled correctly (HNP), paged decode at npl=256 is
**99% GPU-busy (idle 1.4%), not host-bound**.

Real decode decomposition (paged npl=256, HNP; GPU-steady 1082 us/tok = 924 t/s):

| Bucket | us/tok | % decode | Note |
|---|---:|---:|---|
| GDN recurrent scan | 553 | 51% | LINEAR in batch; shared BW floor where **paged LEADS** (83% vs 79%) |
| NVFP4 expert GEMM | 254 | 23% | amortizes with batch; paged competitive |
| bf16 projections | 73 | 7% | vLLM uses FP8 here |
| elementwise | 57 | 5% | vLLM fuses into one Triton kernel |
| SSM conv | 31 | 3% | |
| GPU-idle | - | 1.4% | not host-bound |

Reconciliation chain (must sum):

| Measurement | t/s | % of vLLM-server |
|---|---:|---:|
| vLLM server (CDEF) | 1177 | 100% |
| vLLM **true GPU-steady** | 1078 | 92% (~8 pt = vLLM chunked-prefill-overlap window inflation) |
| llama **GPU-steady** | 924 | 78.5% (**= 86% of vLLM's true 1078**) |
| llama server (CDEF) | 718 | 60.7% (~17 pt = serving graph-reuse overhead, S3-recoverable) |

Serving gap = **~8 pt measurement + ~17 pt scheduler/graph-reuse (recoverable) +
~14 pt GPU-steady kernel residual**. The 14 pt residual = MoE fused-Marlin
persistent-tiling (~+11 ms) + Triton elementwise fusion (~+10 ms). Decode CDEF ratios:
MoE perseq **70.0/65.2/59.4/55.6%** at N=8/32/128/256; **dense 116.7% at N=8** (paged
ahead) falling to 62.1% at N=256.

### 2c. Single-stream tie vs batched 2.4-2.8x divergence: which property is load-bearing

At single-stream / small-M both engines are weight-bandwidth-bound and the GEMM inner
loop is the same order of work, so they tie (corroborated in kind by the committed
"tie at static-wide-128", paged 782 vs vLLM ~819 t/s). When batched to B=32 x PP=512
the workload becomes **compute-bound** and three M-invisible properties dominate:

1. **Tensor-core utilization on aggregated large-M work.** vLLM's expert-major grouped
   GEMM keeps TC full; grouped-MMQ shatters top-8-of-256 into ~4 tok/expert ragged
   tiles (the +56.5 us/tok bucket, batched-only).
2. **The GDN chunked scan only exists at batched prefill** (decode uses the recurrent
   path); its O(C^2) intra-chunk solve is the +59.2 us/tok #1 bucket, no single-stream
   analogue.
3. **act-quant + convert/glue are M-proportional** (+36.6 combined), negligible at M=1.

**Load-bearing property = tensor-core utilization on aggregated large-M work
(grouped-GEMM tiling quality + the GDN tensor-core solve), i.e. compute-kernel maturity,
not scheduling.** Dispatch is only +5.9 us/tok / 3% of the batched gap. This challenges
the older "dense AND MoE both converge to ~41% ⇒ scheduler-localized" interpretation:
the convergence reflects a **shared per-token compute structure** (dense and MoE share
the GDN + projection + norm stack; MoE just adds the expert GEMM), and the definitive
decomposition attributes ~97% of the batched-prefill gap to GPU compute kernels, ~3% to
dispatch.

### 2d. Port forensics: kernel-intrinsic-on-GB10 vs ggml-integration-tax

| Lever | Verdict | Why (integration tax vs kernel-intrinsic) |
|---|---|---|
| **0033** dequant-to-bf16 cuBLAS (dense large-M) | REJECTED -49/-42/-29% at M=512/1024/2048 (PGR) | BOTH: a separate global-memory dequant pass (~8x the FP4-MMQ read traffic, un-amortized), AND bf16 peak = ~half FP4 peak on sm_121 (real ceiling). GB10-hostile as a bf16-dequant approach. Bit-exact, KL-better; correctness never the issue |
| **0034** native FP4-MMA W4A4 | REJECTED in-backend despite winning PoC | PoC: 103 TFLOP/s = 57.7% FP4 peak, NMSE=0, beat cuBLAS-bf16 (kernel portable-in-principle, could *exceed* vLLM). Integration tax dominated: surrounded by act-quant + f32 converts + per-node launch. **Portable-with-prereqs** (fuse act-quant into GEMM prologue, remove f32 converts, live in the CUDA graph) |
| **0035** W4A16-Marlin grouped MoE | REJECTED -39% S_PP, correct + KL-better (KLD 0.131 < MMQ 0.137) | vLLM's *exact* sm_121 shape. Lost because the ggml drop-in still sat in ggml's materialize-every-node grouped-`mul_mat_id` harness at ragged small-M. **Portable-with-prereqs = the whole persistent expert-major executor, not the Marlin inner kernel.** Decode Marlin port lost -19.6% for the same reason |
| **Six one-boundary MoE transplants** (Phase113/114/122/123/125/127) | ALL REJECTED (flat or regress) | Phase124 profile: `mmq_nvfp4` 30.17% + `gdn_core` 29.25%, `act_quant` only 3.35%. Each transplant either attacked a boundary too small (122/123 flat) or added a sorted/padded temporary whose LPDDR5x traffic exceeded the boundary it removed (113/114/125/127 regress). **Portable-with-prereqs, and the prereq is all-or-nothing:** the win exists only as a complete fused persistent expert-major kernel |
| **bf16-C64 GDN** | REJECTED -18.75% | Kept our O(C^2) form-T solve and grew C to 64: makes the O(C^2) solve + serial recurrence worse; C=32 full-width needs 127 KB > 99 KB smem. Separately, Phase74 tested vLLM's blocked `solve_tril` standalone (C=64, tf32): explicit inverse-plus-apply ran at **0.59x** the direct solve (1.7x slower), smem at 98304/99 KB. Blocked-inverse validated **GB10-hostile** on this silicon. Shipped winner = M5 tf32 C=16 (+3.5% npp512, +17.7% npp2048) |

---

## 3. The phased additive program

Ordered by (expected recovery x confidence) / effort. Each phase names the ggml/fork
seam (Audit C), the files, the default-off env gate, the correctness gate (per-path md5
if math-preserving, KL band if dtype-changing), a **falsifiable P0 kill-gate** with a
numeric go/no-go, the expected-recovery arithmetic grounded in section 2, effort, the
prior rejected lever it supersedes with the **missing prereq** that made the prior
rejection not apply, and upstream-clash / rebase-safety.

The phases are **ordered and dependent**: P3 requires P1+P2 landed. That dependency is
precisely why the isolated 0034/0035 A/Bs failed - each was tested without its two
predecessors.

Fork seams referenced below are against local `mudler/llama.cpp:localai-paged`
HEAD `1edddc8fe` (patch series 0001-0052; all file:line references below are against
that tree). The tree carries the MoE-region seam (patch 0052, `moe-ffn.cu` + the
whole-pattern matcher) and the grouped W4A16 Marlin prefill path (patch 0035). It does
**not** carry any P1/P3 scaffolding: the four experiment commits an earlier campaign
prototyped - `237ad9b96` bf16 GDN state cache, `afc2c7030` act-quant-route trace,
`ea0875d14` `LLAMA_BF16_CUBLAS_F32_OUT`, `7967ad47f` W4A16 direct-A stub - were
**trimmed** from the series by the immediately-preceding commit (`b529cc5420`, sync to
fork `1edddc8fe`) and no longer exist in the tree; they survive only as recorded
experiments in [`PARITY_HANDOFF.md`](PARITY_HANDOFF.md). P1's bf16-cuBLAS plank and P3's
direct-A stub therefore must be **re-introduced**, not "finished". The team has not
started P2/P4/P5/P6.

### P1: bf16-native execution pass (kill the f32 convert / act-quant boundary tax)

- **Goal:** delete the convert-in/convert-out on every op boundary and run
  norm/add/rope/silu at half the memory traffic, so the residual/activation stream is
  bf16-resident (as in vLLM) rather than f32-resident with bf16 only as an in-GEMM
  transient. Targets prefill bucket 3 (+36.6) + part of bucket 4 (norms +11.1, glue),
  and decode elementwise (57 us/tok, 5%).
- **Mechanism (Audit C Area 1, option A):** extend the existing fusion pass
  `ggml_cuda_try_fuse` (`ggml-cuda.cu:4232`, called per node in the capture loop at
  `:4908`) to recognize a residual-stream *segment* (norm -> proj-GEMM -> add -> norm)
  and execute it through bf16 variants that keep the intermediate in a bf16 pool buffer,
  converting to f32 only at the boundary a non-owned node reads. The GEMM already
  computes through bf16 tensor cores; the win is deleting the per-op converts, not the
  GEMM. Plank 1 is to re-introduce `LLAMA_BF16_CUBLAS_F32_OUT` (prototyped in the
  trimmed `ea0875d14`, now absent from the tree - see section 3): GEMM writes f32
  directly from bf16 compute, skipping the round-trip pool alloc + convert. Reject option B
  (bf16 tensor types at graph build in `llama-model.cpp`/`llama-graph.cpp`): it edits
  the most rebase-sensitive shared files and forces a hard cut with no per-segment
  opt-in; hold it for a datacenter-Blackwell reopen.
- **Files:** new `norm-bf16.cu` (rms_norm + the two 0042/0044 fused norms, templated on
  IO dtype), bf16 case in `binbcast.cu` (residual add), bf16 instantiation in `rope.cu`,
  bf16 `UNARY+MUL` SiLU-gate; the segment-detect rewrite as ONE additive clause in
  `ggml_cuda_try_fuse`. GDN glue + attention io already bf16 (`gated_delta_net.cu`,
  fattn). ~400-600 LOC.
- **Env gate:** `LLAMA_BF16_STREAM=1` (default off).
- **Correctness gate:** **KL band** (bf16 intermediates change accumulation; the
  bit-exact md5 gate cannot hold and must not be forced). vLLM itself runs bf16 here so
  the reference precision is the same. KL-benign category per
  [`PAGED_BITEXACT_NOTE.md`](PAGED_BITEXACT_NOTE.md).
- **P0 kill-gate:** wire `LLAMA_BF16_STREAM` for ONE residual segment
  (norm -> proj -> add) only; A/B the MoE-decision-model prefill wall at PP=512 with
  `--cuda-graph-trace=node`. **GO** if the convert/glue share (`convert_dtype` 6.3% +
  `concat` 2.9%) drops by >50% of its share AND KL vs the f32 reference stays in band
  (same-top-p >= 84%, KLD delta < 0.01). **NO-GO** if net prefill regresses beyond
  noise (> max(2%, 3 sigma) of control medians) - which would mean the segment-boundary
  converts eat the win.
- **Expected recovery:** conservative ~30 of the +36.6 bucket-3 tax + ~15 of bucket-4
  (norms/glue) + the decode elementwise 57 us/tok fused. Prefill: ~45 us/tok.
- **Effort:** medium (templated re-instantiations + one rewrite clause).
- **Supersedes:** the -79.4% act-quant-into-MMQ fold and the +21.4 convert tax.
  **Missing prereq now supplied:** those failed because the activation reached the GEMM
  as f32 and every op boundary re-converted; a bf16-resident segment removes the
  boundary entirely rather than folding the quant into an MMQ that has no TC for the
  inline quant.
- **Upstream-clash / rebase-safety:** new `.cu` files are rebase-inert; the only shared
  edit is one additive clause in `ggml-cuda.cu` (8 patches + upstream fusion churn -
  the hottest surface, keep growth to the single clause). Do **not** add ggml tensor
  types (avoids `ggml.h`, 5 patches). Rides upstream fusion machinery (`ggml_can_fuse`,
  discussion #17621) by adding new clauses, not editing upstream's.

#### P1 RESULT (landed 2026-07-02, `LLAMA_BF16_STREAM`, default-off)

The bf16-resident residual-segment executor landed as three fork commits on
`mudler/llama.cpp:localai-paged` (new HEAD `653bb2f3d`, tree `6cf1523047`, base
`1edddc8fe`): `1271488fc` (segment executor + `norm-bf16.{cu,cuh}` + the
re-introduced `LLAMA_BF16_CUBLAS_F32_OUT` plank), `91373e1b9` (bf16 residual-add
+ rope op-variants), `653bb2f3d` (test sentinel). LocalAI series regenerated
additively as `0053-0055` (46 patches total); kill-gate at pin `0ed235ea`: all
patches apply and stage tree `6cf1523047` byte-for-byte == fork HEAD tree.

- **Mechanism as-shipped (Option A, as scoped).** One additive clause in
  `ggml_cuda_try_fuse` detects a residual-stream norm-producer (plain
  `{RMS_NORM,MUL}` attn/GDN input norm, or the 0044 `{SILU,RMS_NORM,MUL,MUL}`
  ssm_out gated-output norm) whose f32-output consumers are ALL large-M (M>=128)
  cuBLAS-bf16 projections, runs the norm into a bf16 pool buffer via
  `norm-bf16.cu` (bit-faithful to the f32 kernels up to the `__float2bfloat16`
  store), executes the owned span inline through a bf16 view, then skips it. A
  strict all-consumers-are-ours guard keeps the f32 norm un-materialised and
  bails to the stock f32 path on small-M / decode / MMQ / native-FP4 /
  multi-consumer. The `LLAMA_BF16_CUBLAS_F32_OUT` plank lets owned projections
  write f32 directly from bf16 compute (F32_OUT else-branch byte-identical to the
  original cuBLAS path). No upstream fuse clause edited; exactly 6 files, cmake
  untouched (`.cu` globbed).
- **KEY REFRAME (why a first guard engaged 0).** q36 GDN/attention projections
  (attn_qkv/gate, ssm_alpha/beta/out) are **BF16 weights, NOT NVFP4**; only the
  MoE experts (`ffn_*_exps`) are NVFP4. The convert tax therefore lives at the
  BF16 cuBLAS projection boundary (`op_mul_mat_cublas` src0==BF16 converts f32
  src1->bf16), not on the FP4-MMQ path (which pays act_quant, not convert). The
  dense model quantizes its attn/GDN projections to NVFP4, so it **engages
  nothing** and stays bit-identical. **bf16-stream is a MoE-model prefill lever.**
- **P0 kill-gate (`~/bench/p1_bf16_stream/killgate_20260702_135544`): GO.** One
  segment (960 gate_norm->ssm_out engagements/prefill). `convert_unary<float,bf16>`
  fell 6840->5880 = exactly -960 (163.19->130.73 ms, -19.9%; share 2.27%->1.83%)
  = 100% within-owned-segment drop (the kill-gate's stated criterion), no
  boundary convert added. KL: control and bf16 arms **byte-identical** (KLD
  0.136563 both, same-top-p 83.725% both) => KLD delta 0.000 < 0.01. Prefill S_PP
  +0.53% (2323.24 vs 2310.94 t/s), inside the 3-sigma noise gate. Default md5
  GREEN both models. (The total convert bucket only moved 4.83%->4.40% because
  the minimal segment owns 1 of ~5 BF16 cuBLAS GEMMs per GDN layer; the >50% GO
  is the within-segment 100%.)
- **P1 full build-out: 2240 segments/prefill** (2.33x P0's 960) = 960
  gate_norm->ssm_out (0044, single-consumer) + 1280 multi-consumer plain
  rms_norm -> {attn q/k/v, GDN in_proj} BF16 projections. Prefill A/B (5 iters,
  clean, captured before external contention): MoE @512 B=32 **+1.99%**
  (2361.67 vs 2315.52 t/s; all 5 bf16 samples above all 5 ctrl; reproduced +1.89%),
  @2048 B=8 +0.95%; dense @512 -0.09% / @2048 -0.10% (no-op). Recovered ~8.44
  us/tok @512 (wall 431.87->423.43), ~4.02 @2048. Both MoE deltas sit at the
  max(2%, 3-sigma) floor => classified neutral, but consistent and reproducible
  positive shifts; no prefill regression => not a NO-GO. Decode S_TG neutral
  (M<128 bails).
- **KL gate GREEN (both models).** MoE bf16 KLD 0.136042 vs control 0.136563 =>
  delta **-0.00052** (bf16 slightly better: F32_OUT keeps the full f32 GEMM
  result instead of the old bf16 round-trip), inside the +0.01 band; same-top-p
  84.461% vs 83.725% (>= 84% baseline). Dense: 0 engagements => bit-identical
  (KLD delta 0, same-top-p 100%).
- **All correctness gates GREEN.** Default md5 canonical both models
  (MoE `8cb0ce23`, dense `5951a5b4`); env-on md5 canonical both (small-M bails);
  `test-backend-ops` MUL_MAT 1146/1146, MUL_MAT_ID 806/806, GATED_DELTA_NET
  46/46, MOE_SWIGLU_DOWN 7/7, MUL_MAT_ID_RAGGED_MOE 6/6, BF16_STREAM_SEGMENT 4/4
  (default AND opt-in). Files: binbcast.cu +10, ggml-cuda.cu +297, norm-bf16.cu
  +483, norm-bf16.cuh +37, rope.cu +31, test-backend-ops.cpp +79.
- **Honest magnitude / what remains.** The +1.9-2.0% @512 win is real,
  reproducible, KL-benign (in fact KL-improving), and safe, but modest:
  bf16-stream targets only prefill bucket 3 (the ~4.8%-of-wall convert/glue tax)
  and owns the projection-boundary portion of it (~40% end-to-end), not the
  GDN-scan (bucket 1) or GEMM-tiling (bucket 2) buckets. Read the "expected
  recovery: ~45 us/tok" line above as an upper bound on the whole bucket-3+4
  region; this landing captures the bucket-3 projection boundary only. The next
  P1 increment on the table = extend the multi-consumer executor to own the
  bf16->f32 dst direction plus the remaining attn_norm-fed projection src1
  converts (~4 more converts/layer). Deferred (blocked only by an external
  imatrix job contending the GPU, not a failed gate): the nsys graph-node bucket
  table, decode S_TG @npl128, and the Phase130 serving A/B need a clean idle GB10
  re-run; the scope deems throughput-neutral serving acceptable on GB10.

### P2: expert-major fused routed-FFN region executor (grow the merged MoE seam into the real thing)

- **Goal:** drive both MoE GEMMs expert-major so the gate_up output never lands in
  global memory, deleting the one intermediate still materialized today and the
  redundant per-GEMM sort. Targets prefill bucket 2 (+56.5, the ragged-tile tax) and the
  decode MoE fused-Marlin ~+11 ms residual.
- **Mechanism (Audit C Area 2):** the seam already exists. `moe-ffn.cu` +
  `ggml_cuda_moe_whole_pattern_detect_early` (`:4157`) matches the
  `gate_up (MUL_MAT_ID) -> VIEW -> SWIGLU -> down (MUL_MAT_ID)` chain and the hook
  returns the node-skip count so the graph advances past the region. But it is a
  *partial* executor: `ggml_cuda_moe_routed_ffn_poc` (`moe-ffn.cu:275`) still runs the
  first GEMM as the stock node and **materializes its full `[2*n_ff, n_expert_used,
  n_tokens]` intermediate**, only then fusing SwiGLU+quant (into the finalize epilogue
  it also folds the weighted combine). A true region executor routes once, keeps the
  token-sort/`ids_meta` resident, feeds each expert's gate+up tile straight into the
  fused SwiGLU+quant into the down GEMM, and emits one unpermuted+combined result.
- **Files:** new ~400-600 LOC fused two-GEMM expert-major loop in `moe-ffn.cu`
  (fork-owned), ~30 LOC hook change in `ggml-cuda.cu`. mmq.cu touched (5 patches).
- **Env gate:** new default-off env (e.g. `LLAMA_MOE_REGION_EXECUTOR=1`).
- **Correctness gate:** **KL band** (expert-major fusion changes FP accumulation order;
  the finalize path is already recorded KL-benign, paged-MoE md5 `8cb0ce23`).
- **P0 kill-gate:** implement the expert-major region for ONE projection pair (remove
  the materialized gate_up); A/B `MOE_SWIGLU_DOWN` + `MUL_MAT_ID_RAGGED_MOE` at
  n=128 and n=257. **GO** if the n=257 (batched large-M) rows improve > 5% over the
  grouped-MMQ control with the KL gate green. **NO-GO** if flat/regress like the six
  prior transplants (that is the null hypothesis this phase must beat; a single removed
  boundary is not enough, the whole region must be owned).
- **Expected recovery:** conservative ~40 of the +56.5 bucket-2 prefill tax (approaches
  the bf16-peak ceiling with full TC utilization) + the ~11 ms decode MoE residual.
- **Effort:** high (single-kernel fused rewrite; the load-bearing lift of the program).
- **Supersedes:** all six one-boundary MoE transplants (113/114/122/123/125/127).
  **Missing prereq now supplied:** those paid the sorted/padded temp-traffic cost
  without the persistent-kernel payoff because they ported one boundary into a
  materialize-every-node cgraph; the win exists **only** as the complete fused region
  that never materializes the intermediates.
- **Upstream-clash / rebase-safety:** the kernel is fork-owned in `moe-ffn.cu`
  (rebase-inert); the hook is one narrow block in `ggml-cuda.cu`. Must keep the strict
  view/consumer guard (region ownership is safe-by-construction but narrow: bail to
  node-at-a-time if any other node reads `gate_up`/`glu`). **Open q for q36:** confirm
  the dense shared-expert-per-layer does not alias the routed `gate_up` view before
  widening ownership. CUDA-graph capture: all region kernels run inside the capture
  loop; keep every pool alloc shape-stable across replays (keyed on n_tokens/n_experts,
  never on data-dependent routing counts) or it forces re-capture.

#### P2 RESULT (NO-GO, recorded 2026-07-02, `LLAMA_MOE_REGION_EXECUTOR`, default-off)

The layout-only expert-major region executor was implemented, correctness-proven
on the synthetic sentinel, and A/B'd against the grouped-MMQ control at the P0
kill-gate. **Verdict: NO-GO on two independent signals; nothing built beyond P0,
nothing landed.** The topic branch `p2-moe-region` is retained on the DGX fork for
forensics at `2d87564ddfa26f6c275dad0e1f0e3d8d5413e337` (base `localai-paged`
`653bb2f3d`, NOT pushed); the fork `localai-paged` HEAD is **untouched at
`653bb2f3d`** and the LocalAI series stays at 46 patches (`0001-0055`). This
records P2-at-this-granularity as a confirmed floor.

- **(1) Primary GO metric FLAT (the kill-gate's stated criterion).** The kill-gate
  required the n=257 (batched large-M) `MOE_SWIGLU_DOWN` rows to improve **> 5%**
  over the grouped-MMQ control. Measured (region arm vs grouped-MMQ control, 5x
  medians): control **1021.61 us**, region **1022.15 us** => **-0.05%**
  (marginally slower). n=128: 804.87 vs 807.63 = -0.34%. `MUL_MAT_ID_RAGGED_MOE`
  (lone MUL_MAT_ID, region never engages there): n=257 +0.48%, n=128 +0.28% (pure
  noise, confirms no perturbation of the standalone grouped MMQ). All four deltas
  sit inside the 5-sample spread => sentinel flat. **This reproduces the six prior
  one-boundary MoE transplants (phases 113/114/122/123/125/127) - the null
  hypothesis the scope said P2 had to beat.** A compact expert-major layout + a
  single route-sort, with both GEMMs still ragged grouped-MMQ, does not move the
  sentinel; the ragged-tile tiling (the actual +56.5 bucket-2 tax) is *unchanged*
  by a layout swap. Closing bucket 2 needs P3's Marlin persistent-CTA aggregation,
  not a P2 layout change.
  - *Methodology caveat on the sentinel (reported as-is, it is the requested
    metric):* `test-backend-ops` `eval_perf` duplicates only the down/out node
    ~n_runs (~1000) times per timed iteration, so the single region invocation is
    ~1/n_runs of the signal => the perf sentinel is structurally under-sensitive to
    the region change. The flat verdict is corroborated by signal (2). (The n=257
    `MOE_SWIGLU_DOWN` case was added to both `make_test_cases_eval` and
    `make_test_cases_perf`; the eval list already had n=128.)
- **(2) DECISIVE STRUCTURAL BLOCKER: the seam does not match q36's decision
  graph.** `q36-35b-a3b-nvfp4.gguf` ships **separate** `ffn_gate_exps` +
  `ffn_up_exps` (+ per-tensor `.scale`/`.input_scale`), **NOT** a merged
  `ffn_gate_up_exps` (verified by GGUF tensor-name scan). `llama-graph.cpp`
  `build_moe_ffn` therefore takes the separate-gate/up branch =>
  `ffn_moe_gate_scaled` + `ffn_moe_up_scaled` + `ggml_swiglu_split`. The
  whole-pattern matcher `ggml_cuda_moe_whole_pattern_detect_early` requires the
  merged `gate_up(MUL_MAT_ID) -> VIEW -> VIEW -> SWIGLU -> down` shape, which is
  **absent** on q36. Result: `LLAMA_MOE_WHOLE_PATTERN_EARLY_TRACE` fires **0x** on
  q36 (prefill AND decode); the region executor engages 0x; the pre-existing
  POC/fused-quant (`LLAMA_MOE_ROUTED_FFN_POC=1 +FUSED_QUANT=1`) also engages 0x.
  The region only engages on the synthetic merged-shape test sentinel (7
  engagements/pass, `MOE_SWIGLU_DOWN` 8/8 nmse-correct). **Even a positive sentinel
  could not have translated to q36 without first extending the matcher + POC to the
  separate/scaled/swiglu-split shape.**
- **KL gate: in-band but VACUOUS.** control KLD 0.136563 / same-top-p 83.725%;
  region KLD 0.136563 / same-top-p 83.725% => delta **0.000000**, byte-identical.
  In-band (delta < 0.01, top-p >= 84 baseline) but only because the region engages
  0x on q36 - it is not a KL-neutrality claim for the executor (that is the separate
  8/8 NVFP4 nmse sentinel).
- **S_PP @512 (npp512 ntg4 npl32, 5x):** control 2320.62 t/s (stdev 0.23%), region
  2316.70 t/s (stdev 0.24%) => -0.17% (flat; region == control at 0 engagement;
  code-present, no regression). **Capture stability:** region S_PP stdev 0.24%
  across 5 iters = no CUDA-graph re-capture thrash (pool allocs keyed on
  n_tokens/n_experts held shape-stable).
- **All correctness gates GREEN, both arms** (default AND
  `LLAMA_MOE_REGION_EXECUTOR=1`): `test-backend-ops` MUL_MAT 1146/1146, MUL_MAT_ID
  806/806, GATED_DELTA_NET 46/46, MOE_SWIGLU_DOWN 8/8, MUL_MAT_ID_RAGGED_MOE 6/6,
  BF16_STREAM_SEGMENT 4/4. Default md5 canonical both models (MoE `8cb0ce23`, dense
  `5951a5b4`); env-on also canonical (greedy prompt is small-M => region bails).
  Region correctness where it *does* engage is proven by the 8/8 NVFP4 nmse match
  incl. n=257 (ne_get_rows=2056).
- **Implementation (correct, committed on `p2-moe-region`, NOT pushed, ~407 LOC / 6
  files).** `moe-ffn.cu` `ggml_cuda_moe_region_executor`: one route-sort (ids_meta,
  cur framing); gate_up grouped NVFP4 MMQ writes a **compact expert-major buffer**
  via iota `ids_dst` (the token-order `[2*n_ff, n_used, n_tokens]` intermediate
  never materialised); new `moe_swiglu_nvfp4_quant_compact_kernel` reads the compact
  buffer by route-slot (no ids_src1 gather); down MMQ unpermutes to token order.
  Strict all-consumers guard `ggml_cuda_moe_region_consumers_ok` bails if any node
  outside the 5-node region reads gate_up/views/glu (covers shared-expert aliasing).
  `LLAMA_MOE_REGION_TRACE`.
- **Honest delta vs expectation.** The scope's P2 line targeted ~40 of the +56.5
  bucket-2 prefill tax + the ~11 ms decode MoE residual. **Delivered: 0** (region
  flat on its sentinel and 0-engagement on the decision model). The compact
  expert-major layout is the wrong lever at this granularity: it swaps *where* the
  intermediate lives without changing the ragged-tile GEMM tiling that owns the
  cost.
- **Prerequisite handoff (gates P2 AND P3).** Before ANY MoE-region lever can
  engage on q36, the seam - the whole-pattern matcher, the POC/fused-quant, AND the
  region executor - must first be **rebuilt for q36's separate
  `ffn_gate_exps`/`ffn_up_exps` + per-tensor `.scale` + `ggml_swiglu_split` FFN
  shape**. The current seam only matches a merged shape q36 does not emit. The
  correct next action is a re-scope of the seam to the separate/scaled shape as the
  gating prerequisite, then re-evaluate whether a *fused two-GEMM* region (not a
  layout swap) beats the sentinel - the scope's own null hypothesis holds that the
  win exists only as the complete fused kernel that never materialises the
  intermediates.
- **Artifacts (DGX `~/bench/p2_moe_region/`):** `focused_20260702_172644/` (perf
  sentinels 5x, correctness OFF+ON, md5, S_PP@512 5x, KL) + `RESULTS.txt`;
  `killgate_20260702_171826/` (engagement proof: `engage_moe.log`=0,
  `engage_dense.log`=0); `build_20260702_145928/` (build logs). Environment:
  `LLAMA_MAX_BATCH_TOKENS` unset, sm_121a, `nsys --cuda-graph-trace=node`, GPU lock
  held.

### P3: Marlin-class large-M GEMM retry, ON TOP of P1+P2 (the forensics-informed retry)

- **Goal:** land the W4A16 Marlin-shape GEMM (FP4->bf16 in-register dequant + bf16
  mma.sync + cp.async double-buffer + dequant-once weight reuse across 16-64 M-rows)
  that vLLM uses on sm_121, now that its two prereqs exist. Targets prefill bucket 2's
  residual to the bf16-peak ceiling and the ragged-tile TC collapse.
- **Mechanism (Audit C Area 4):** add a `direct_a` W4A16 path. What exists in the tree
  is the **grouped** W4A16 Marlin path (patch 0035: `w4a16-gemm.cu`/`w4a16-gemm.cuh`,
  engaged by `ggml_cuda_w4a16_moe_grouped_should_engage` at the hook `ggml-cuda.cu:2797`
  [`paged patch 0035`], gated by `LLAMA_W4A16_PREFILL_M>0`). What it lacks is a direct-A
  variant that takes `src1` f32 directly with an `ids_to_sorted` map, fusing the
  activation cast into the kernel and skipping both the host-side expert-sort and the
  separate act-quant pass (the +15 us/tok the FP4-MMQ path pays). An earlier campaign
  prototyped exactly this as the trimmed `7967ad47f`
  (`ggml_cuda_mul_mat_id_w4a16_grouped_direct_a`, a `w4a16-policy.h` engage gate
  `ggml_cuda_w4a16_direct_a_should_engage_params`: NVFP4 src0, f32 src1/dst, Blackwell,
  `LLAMA_W4A16_PREFILL_M>0`, tokens > M, `k%64==0 && n%128==0`, unit-tested in
  `test-cuda-w4a16-policy.cpp`), but that stub, its policy header, and its test were
  **trimmed** (see section 3) and are **not** in the tree - they must be re-created on
  top of the grouped path, with a new direct-A hook alongside the grouped one. Add a
  one-time host-side weight repack cache into Marlin's interleaved layout (fork-owned
  loader in `llama-model-loader.cpp`, off the per-step path).
- **Files:** the grouped Marlin kernel exists (`w4a16-gemm.cu`, fork-owned); the
  direct-A variant (~300 LOC) + its policy header + unit test must be re-added, repack in
  `llama-model-loader.cpp`, a new direct-A hook in `ggml-cuda.cu`.
- **Env gate:** `LLAMA_W4A16_DIRECT_A=1` + `LLAMA_W4A16_PREFILL_M>0` (default off).
- **Correctness gate:** **KL band** (bf16 dequant path; already characterized
  KL-benign-and-better, KLD 0.131 < MMQ 0.137).
- **P0 kill-gate:** with P1 (convert-free bf16 activations) and P2 (persistent region
  owning the tiling) landed, engage direct-A and A/B S_PP vs grouped-MMQ at
  M=512/1024/2048. **GO** if S_PP >= grouped-MMQ + 5% at M >= 1024 AND KLD <= 0.137.
  **NO-GO** if it reproduces the prior -39% / -19.6% - which would mean the prereqs are
  still insufficient and the executor still materializes around the kernel.
- **Expected recovery:** the remainder of bucket 2 not captured by P2, up to the
  bf16-peak ceiling. Combined P2+P3 target ~40-50 of the +56.5.
- **Effort:** medium (the grouped Marlin kernel exists as a starting point, but the
  direct-A variant + policy + test were trimmed and must be re-created; the larger lift
  is still the P1/P2 predecessors).
- **Supersedes:** 0035 (-39%) and 0034 in-backend fail. **Missing prereqs now
  supplied:** P1 delivers bf16 activations to the GEMM without converts; P2 delivers the
  persistent region that owns the tiling across both GEMMs so the bf16 activation is
  read once (the prior loss was ggml MMQ re-quantizing the y-operand per weight-row-tile
  x stream-k split).
- **Upstream-clash / rebase-safety:** `w4a16-gemm.cu`/`.cuh` fork-owned (the re-added
  `w4a16-policy.h` will be too); can ride the in-tree multi-stream `concurrent_event`
  machinery (`ggml-cuda.cu:4769`, `try_launch_concurrent_event` over
  `stream_ctx.concurrent_events`) for the K-loop cp.async overlap instead of a private
  mechanism.

### P4: token-granular continuous-batching scheduler (server-side only)

- **Goal:** one per-step token budget mixing chunked prefill + all ready decodes, with
  per-seq chunked-prefill cursors, cheap recoverable preemption, and adaptive bucketed
  decode emission. On GB10 this is a **TTFT + architecture-enabler** lever, **not** a
  throughput lever (the prior host-loop-dead measurement is real and must be respected);
  its throughput payoff is on non-GB10 silicon where decode goes host-bound again.
- **Mechanism (Audit C Area 3, Audit B section 1):** extend the shipped continuous-batch
  P1 (patch 0016, `server-context.cpp:3083-3135`, the dynamic decode-first prefill
  budget: `LLAMA_MAX_BATCH_TOKENS` read at `:3105`, `prefill_budget_step =
  max(n_ubatch, T - n_decode_in_batch)` at `:3113`) into: (1) chunked prefill as a
  first-class per-sequence cursor (each waiting prompt contributes
  `min(remaining_prompt, per_slot_cap)` tokens per step and resumes next step);
  (2) a `SLOT_STATE_PREEMPTED` state + release-KV-keep-prompt-tokens-re-admit transition
  (the paged KV manager already supports on-demand block alloc + burst-reclaim, patch
  0024; defrag in `paged-alloc.cpp`); (3) adaptive bucketed decode widths matched to
  live load (never fixed pad-to-parallel: `DECODE_SERVING_SCOPE.md` proved padding
  net-negative on GB10 since decode is GPU-compute-bound). Zero ggml; llama-server owns
  batch formation.
- **Files:** `server-context.cpp` (5 patches), `paged-alloc.cpp` + `paged-kv-manager.cpp`
  (3 each), new pure helpers in an `server-admission-policy.h`-style unit-tested header.
  ~600-1000 LOC.
- **Env gate:** new default-off env (e.g. `LLAMA_CONTINUOUS_BATCH_V2=1`).
- **Correctness gate:** **md5 bit-exact** (per-seq logits depend only on that seq's
  tokens + its own paged KV; the S3 note already establishes this). This is the one
  phase that stays on the sacred md5 gate rather than KL.
- **P0 kill-gate:** implement the per-seq chunked-prefill cursor + adaptive bucketing;
  A/B TTFT and serving-aggregate at concurrency 8/32/128 server-side. **GO** if TTFT
  under load drops > 20% with the md5 gate green AND serving-aggregate not regressed.
  Throughput-neutral on GB10 is acceptable (the gate is TTFT, per prior evidence).
  **NO-GO** if TTFT is flat or md5 breaks.
- **Expected recovery:** part of the ~17 pt serving graph-reuse overhead on GB10
  (conservative ~10 pt combined with S3), plus the TTFT axis (the `2377 -> 13533 ms`
  TTFT scaling is scheduler-shaped; vLLM's ~3.4x better TTFT is the target). It is also
  the **enabling substrate** for P2/P3 (a persistent per-seq scheduling context is the
  prereq the Marlin retry's persistent tiling wants).
- **Effort:** high (largest new server-side piece, but mechanical and bit-exact-safe).
- **Supersedes:** nothing was rejected here; but it explicitly does **not** re-litigate
  the S3 fixed-padding result (net-negative on GB10). **Value framing:** TTFT + fairness
  + non-GB10 throughput + enabler; the GB10 throughput claim is deferred by design.
- **Upstream-clash / rebase-safety:** safest area. `tools/server/server-context.cpp` is
  a fork-owned tool, not ggml core; upstream churns it less and conflicts are mechanical.

#### P4 RESULT (NO-GO at the P0 perf kill-gate, recorded 2026-07-02, `LLAMA_CONTINUOUS_BATCH_V2`, default-off)

The CBv2 P0 kill-gate subset (per-seq chunked-prefill cursors + adaptive decode
bucketing) was **implemented and correctness-proven green**, but the P0 kill-gate's
stated GO criterion - a **> 20% TTFT-under-load drop** with md5 green and
serving-aggregate not regressed - was **NOT demonstrated**, so per the phased
contract `go=false` was the kill-gate default, **nothing was built beyond P0**
(no `SLOT_STATE_PREEMPTED`, no aging/starvation-freedom), and **nothing landed.**
The topic branch `p4-cbv2` is retained on the DGX fork at
`ebb649335fe7686524a3630ee2fdffce44be6d52` (base `localai-paged` `653bb2f3d`, NOT
pushed); the fork `localai-paged` HEAD is **untouched at `653bb2f3d`** and the
LocalAI series stays at 46 patches (`0001-0055`). **This is the scope-anticipated
outcome:** the P4 section frames CBv2 on GB10 as a TTFT + fairness + architecture-
enabler lever, **not** a throughput lever (decode is GPU-compute-bound; the
host-loop-dead measurement is real), so a NO-GO on the TTFT perf gate is the
expected result and any throughput payoff lives on non-GB10 silicon (out of scope).

- **FINAL MEASURED VERDICT (the A/B completed autonomously after the forced report;
  full 60/60 raws, 5 reps per arm per shape;
  `dgx:~/bench/p4_cbv2/perf_20260702_194359/RESULTS.md`): NO-GO CONFIRMED BY
  MEASUREMENT, and stronger than flat: CBv2-at-this-granularity REGRESSES.**
  TTFT-GO shapes: NONE. Measured deltas (candidate vs control medians; "clears" =
  beyond max(2%, 3 sigma)):
  - staggered N=32: TTFT p50 **+33.6% WORSE** (4559.3 -> 6091.3 ms, clears), mean
    +31.4% worse (clears), p95 +14.3% worse (clears); agg/decode -3.3/-3.4%
    (inside a very noisy ~21% gate).
  - staggered N=128: TTFT p50 +15.5% / mean +17.9% / p95 +12.1% worse (all clear);
    **aggregate -6.9% and decode-agg -6.9% REGRESSED beyond noise** (0.4% sd).
  - burst N=128: TTFT p50 +13.5% / mean +10.5% worse (clear); agg -3.9% (clears).
  - staggered N=8 and burst N=8: neutral. burst N=32: decode-agg +36.3% (barely
    clears a 35.2% noise gate; high-variance shape; the one positive signal:
    fair-share keeps decodes flowing through a prefill wave).
- **WHY (analysis, recorded so it is not re-litigated):** fair-share chunked
  prefill is processor-sharing; for a near-uniform prompt population it delays
  every prompt's prefill completion versus run-to-completion admission
  (round-robin maximizes mean completion time for identical jobs), so TTFT rises
  by construction, and at N=128 the extra interleave overhead also costs
  throughput. The premise that the TTFT scaling curve was "scheduler-shaped" is
  hereby PARTIALLY REFUTED for GB10: the shipped decode-first budget (patch 0016)
  already captures the schedulable win, and vLLM's TTFT advantage on this hardware
  is dominated by its 2.6-2.8x prefill compute (buckets 1-2), not batch formation.
  TTFT parity therefore routes through P3/P5 (prefill compute), not the scheduler.
  Chunked-prefill fair-share may still pay on mixed long/short-prompt workloads
  and on non-GB10 (host-bound) silicon; both are out of scope here.
- **CORRECTNESS GATES ALL GREEN (DGX GB10, arch sm_121a), the substantive P0
  result.** Behind `LLAMA_CONTINUOUS_BATCH_V2=1` (default OFF, byte-identical off):
  - **(a) canonical md5 GREEN both models, default-off AND cbv2-on:** paged-MoE
    `8cb0ce23777bf55f92f63d0292c756b0`, dense `5951a5b4d624ce891e22ab5fca9bc439`.
  - **(c) `test-backend-ops` GREEN (zero-ggml side-effect proof):** MUL_MAT
    1146/1146, MUL_MAT_ID 806/806, GATED_DELTA_NET 46/46.
  - **(c) CURSOR INTERLEAVE PROVEN** (`LLAMA_CBV2_TRACE`, staggered N=20): steps
    carry decode AND prefill tokens in the SAME batch with per-slot cursors
    advancing across steps, not slot-exclusive. Verbatim step=6: `n_decode_toks=5
    n_prefill_toks=1535 n_seqs=20` with 15 partial cursors; slot s112 advances
    144/523 -> 281 -> 418 -> 519 over steps 6-9 while decode runs; adaptive
    fair-share cap tracks live load (410@5waiting, 171@12, 137@15, 291@7, 508@4);
    `dbucket==n_decode` confirms **no fixed pad-to-parallel** (per
    `DECODE_SERVING_SCOPE.md` net-negative-on-GB10).
  - **(b) SERVER DETERMINISM = CBv2 is NEUTRAL / correctness-preserving.** The
    literal exact-reproducibility gate is unsatisfiable by ANY scheduler here: the
    paged CONCURRENT greedy path is inherently non-deterministic run-to-run in the
    BASELINE too (the control default scheduler diverges from itself), a pre-existing
    benign near-tied-argmax / co-batch FP-reduction-order property
    (`PAGED_BITEXACT_NOTE`), on both dense and MoE. The discriminating test - does
    CBv2 diverge from control MORE than control diverges from itself - **PASSES**:
    across 8 configs {dense,moe} x {degenerate,natural} x {gen8,gen64}, per-request
    cross-arm divergence tracks the within-arm run-to-run baseline to +/-1-3 of 32
    (small-count noise; e.g. MoE-natural gen64 base 31/32 worst-cross 31/32;
    dense-degenerate base 14 cross 12-17). Single-sequence greedy is fully
    deterministic (the md5 gate above).
- **Implementation (kill-gate subset only; correct, committed on `p4-cbv2`, NOT
  pushed; server-side only, ZERO `ggml/` files, ~68 LOC in `server-context.cpp` +
  a new unit-tested header).** (1) Per-seq chunked-prefill cursors with a
  **load-adaptive fair-share cap** = `ceil(prefill_leftover / n_waiting)` floored at
  `LLAMA_CBV2_CHUNK_MIN` (default 128, deliberately NOT `n_ubatch` so a 512-token
  prompt actually chunks under load); CBv2 activates the shipped 0016 decode-first
  budget by default (`T=n_batch`, no `LLAMA_MAX_BATCH_TOKENS` needed) and replaces
  0016's fixed cap with this fair-share cap; cursor = `slot.prompt.n_tokens()`
  advancing across steps. (2) Adaptive decode bucket policy (`LLAMA_CBV2_DECODE_PAD`
  default 0 => `bucket==n_decode`, no padding; policy computed+traced only, never
  fed to batch formation, so bit-exact-safe; row-emission for host-bound silicon is
  the deferred [Build phase]). Pure math lives in the NEW unit-tested header
  `tools/server/server-admission-policy.h` (namespace `cbv2`) +
  `server-admission-policy-test.cpp` (host-side unit tests ALL PASS local + DGX);
  `server-context.cpp` is the thin integration; step trace under `LLAMA_CBV2_TRACE=1`.
- **Honest delta vs expectation.** Kill-gate GO required TTFT-under-load to drop
  `> 20%`; **delivered: not demonstrated** (perf A/B force-terminated control-only).
  The correctness substrate (bit-exact md5, proven decode+prefill co-batching with
  per-seq cursors, determinism-neutrality) is real and is the enabler the scope
  values, but the perf axis that gates the phase was never measured to GO.
- **WHAT WOULD CHANGE THE VERDICT (re-score path).** Read the finalized DGX
  `~/bench/p4_cbv2/perf_20260702_194359/RESULTS.md` once the CANDIDATE arm completes
  (the perf driver `p4_agg.py` auto-writes medians+stdev deltas with the
  `> 20%`-TTFT-drop GO logic baked in). **IF** it shows a genuine `> 20%`
  staggered-TTFT drop clearing `max(2%, 3*stdev)` with md5 green and aggregate not
  regressed, re-score `go=true` and trigger the **full P4 build-out**:
  `SLOT_STATE_PREEMPTED` + release-KV-keep-prompt-tokens re-admit (reusing the paged
  burst-reclaim patch 0024 + `paged-alloc.cpp` defrag), aging/starvation-freedom with
  a constructed starvation test, preemption-transition + aging unit tests, and a
  forced-preemption byte-identical-resume determinism gate. **ELSE** (the
  scope-expected case) this NO-GO stands and P4 is deferred as a GB10 TTFT/fairness/
  enabler lever whose throughput payoff is non-GB10.
- **Series-numbering flag (for whoever lands a future GO).** The P0 code comments
  label `[paged 0056]` per the pinned fork's next slot (46 patches), but the LocalAI
  worktree README is already ahead at `0056-0061` (the MoE MMQ trace series) -
  reconcile the actual series number on landing (likely `0062`).
- **Artifacts (DGX `~/bench/p4_cbv2/`):** `build_20260702_192141/` (build.log);
  `gates_20260702_192632/` (SUMMARY.txt: md5 x4, test-backend-ops, cbv2_trace.txt,
  determinism tsvs); `det2_20260702_193123/` + `det3_20260702_193649/` +
  `det4_20260702_194040/` (determinism diff-matrix: degenerate / natural / gen8);
  `perf_20260702_194359/` (raw_*.json + auto-written RESULTS.md). Environment:
  `LLAMA_KV_PAGED=1 LLAMA_MOE_FORCE_GRAPHS=1`, `LLAMA_MAX_BATCH_TOKENS` unset,
  sm_121a, GPU lock held. Code on `p4-cbv2` `ebb649335`:
  `tools/server/server-admission-policy.h`, `server-admission-policy-test.cpp`,
  `server-context.cpp` (+68).

### P5: FLA-faithful GDN prefill scan (blocked solve_tril port; the algorithm never actually tested in-backend)

- **Goal:** replace the hand f32 chunked scan (`gdn_core`, 95.7 us/tok, 2.62x vLLM) with
  vLLM's FLA six-kernel chunk-64 pipeline whose triangular solve is **blocked into
  tensor-core matmuls**. Targets prefill bucket 1 (+59.2, 30% of the gap) - the largest
  single bucket.
- **Mechanism (Audit B section 6):** port the FLA `chunk_gated_delta_rule_fwd` pipeline:
  (1) `chunk_local_cumsum`, (2) `chunk_scaled_dot_kkt` (fp32 A), (3) **`solve_tril`
  blocked inverse** (`merge_16x16_to_64x64_inverse`: invert 16x16 diagonal blocks with a
  ~14-iteration register-resident loop, fill off-diagonal blocks with block-inverse
  identity via `tl.dot` tensor-core matmuls, dropping the serial dependency length from
  ~64 to ~14), (4) `recompute_w_u` (tl.dot), (5) `chunk_gated_delta_rule_fwd_h`
  inter-chunk recurrence (register-resident fp32 state, chunk loop *inside* the kernel,
  heads/dim-blocks parallel across the grid), (6) `chunk_fwd_o`. fp32 accumulate,
  bf16 streamed operands.
- **Files:** new `gdn-blocked-solve.cu` / additions to `gated_delta_net.cu` (6 patches).
- **Env gate:** new default-off env (e.g. `LLAMA_GDN_FLA_CHUNK=1`).
- **Correctness gate:** **KL band** (fp32-accumulate but different algorithm order).
- **P0 kill-gate (gated hardest):** port the six-kernel pipeline and A/B `gdn_core`
  prefill at npp512 and npp2048. **GO ONLY IF** the in-pipeline blocked solve_tril beats
  the current f32 chunked scan by > 10% at npp2048 AND fits under the 99 KB smem cap AND
  the KL band holds. **NO-GO** if it reproduces Phase74's standalone 0.59x (explicit
  inverse slower than direct solve) - which is the **expected null** given the prior
  standalone evidence, so this phase must clear the highest bar.
- **Expected recovery:** speculative. This bucket is partly a **shared-hardware floor**
  (99 KB smem forces C=16; Phase74 found the blocked inverse GB10-hostile). Conservative
  expected recovery is **small (~0-10 of the +59.2)**: the difference from Phase74 is
  that P5 tests the *whole FLA pipeline in-backend* (register-resident state, chunk loop
  in-kernel), which was never actually run in-backend - the prior bf16-C64 lever kept
  our O(C^2) form-T solve, and the blocked solve was only ever benched standalone. If
  the in-pipeline register-resident form behaves differently from the standalone bench,
  upside is up to 59 us/tok (the single largest lever); if not, P5 is confirmed a
  shared-hardware floor and recorded as such.
- **Effort:** high, high-risk.
- **Supersedes:** bf16-C64 (-18.75%) and the Phase74 standalone blocked-solve (0.59x).
  **Missing prereq / difference:** neither prior test ran the full FLA chunk pipeline
  in-backend with the register-resident inter-chunk scan; P5 does. This is the one lever
  with a prior standalone negative, so it is ranked after the high-confidence phases and
  its kill-gate is the strictest.
- **Upstream-clash / rebase-safety:** `gated_delta_net.cu` is a high-churn fork file
  (6 patches) and upstream may add its own GDN paths; keep the new pipeline in a
  separate `.cu` and gate the dispatch narrowly.

#### P5 RESULT (NO-GO at the P0 perf kill-gate, recorded 2026-07-02, `LLAMA_GDN_FLA_CHUNK`, default-off) - the GDN prefill bucket is now a CONFIRMED SHARED-HARDWARE FLOOR

The full six-kernel vLLM-FLA `chunk_gated_delta_rule_fwd` pipeline was **ported to
CUDA tf32 mma, per-kernel validated against a host fp64 reference, integrated behind
`LLAMA_GDN_FLA_CHUNK=1` (default-off), and A/B'd in-backend** against the shipped M5
f32 chunked scan. It **lost decisively** and by the wrong sign, so `go=false` was the
kill-gate default, **nothing was built beyond P0, and nothing landed.** This is the
**scope-anticipated "expected null"** (the P5 section framed this as the program's
strictest kill-gate given Phase74's standalone blocked-inverse 0.59x), but the phase
delivered the one thing the prior evidence lacked: **the whole FLA pipeline run
in-backend with the register/smem-resident inter-chunk state and the chunk loop
in-kernel** - the exact form that "was never actually tested in-backend." It was tested
here, and the result **settles the GDN prefill bucket (bucket 1, +59.2, the single
largest prefill lever) as a shared-hardware / memory-bandwidth floor on GB10.**

- **PERF GO GATE FAILED DECISIVELY (the decisive result).** GO required the in-pipeline
  blocked `solve_tril` to beat the M5 f32 chunked scan by **> 10% at npp2048**.
  Measured (nsys `--cuda-graph-trace=node`, MoE `q36-35b-a3b-nvfp4`, per distinct token
  over the 30 GDN layers): **npp2048 M5 56.31 vs FLA 119.46 us/tok = FLA 2.12x SLOWER**
  (`gdn_delta_pct_2048 = -112.1`); **npp512 M5 51.23 vs FLA 117.35 = 2.29x slower**.
  End-to-end **S_PP regressed MoE -13.33% @npp2048 / -13.12% @npp512** (3-rep medians;
  clears `max(2%, 3 sigma)` by a wide margin, and it is the wrong sign, so there is no
  3-sigma question). The shipped M5 remains `gdn_core` at **56.31 us/tok = 64.82% of
  vLLM's FLA chunk-64 36.5 us/tok on this GB10**; the rejected FLA port was only **30.55%
  of vLLM** (36.5/119.46) - a regression, not a recovery. This reproduces Phase74's
  standalone blocked-inverse 0.59x and extends bf16-C64 (-18.75%), now **confirmed
  in-backend** with the register-resident state + in-kernel chunk loop.
- **WHERE THE TIME WENT (the novel, valuable decomposition - the reason this NO-GO
  matters beyond a rejection).** Per-kernel nsys share of the FLA bucket: the **blocked
  `solve_tril` is only ~2.8% (55.6 ms)** - the algorithm the whole phase was about is
  *cheap*. The bucket is dominated by **`chunk_gated_delta_rule_fwd_h` 46.2% (903 ms) +
  `chunk_fwd_o` 31.5% (617 ms)**: the inter-chunk state-recurrence GEMMs plus the
  **per-chunk h-state materialization to global LPDDR5x** that FLA's split-kernel
  structure forces (`fwd_h` writes `h_pre` per chunk, `fwd_o` re-reads it). The fused M5
  single kernel keeps the 128x128 state **resident in smem and never materializes
  per-chunk h**, so it is **2.1x faster on GB10's low-bandwidth memory.** So the novel
  finding vs all prior evidence: **the blocked solve itself is not the floor - the floor
  is the state-GEMM + h-materialization region, which the FLA structure makes WORSE than
  M5, not better.** This is exactly the "materialize-everything tax" the scope warns of.
  The binding silicon property is **memory bandwidth** (per-chunk h round-trips to
  LPDDR5x), compounded by the **99 KB smem cap** that forces the FLA split (`fwd_h` and
  `fwd_o` cannot co-reside), not the mma shapes or wave count.
- **SMEM GATE PASSES (all six kernels under the 99 KB opt-in cap at C=64;
  `cudaOccupancyMaxActiveBlocksPerMultiprocessor`):** `k_kkt` 48 KB / 2 blk, `k_solve`
  38 KB / 2 blk, `k_wu` 48 KB / 2 blk, `k_fwdh` 80 KB / 1 blk, `k_fwdo` 96 KB / 1 blk -
  **max 96 KB < 99 KB.** The kernels fit; they are simply bandwidth-floored above M5.
- **KL BAND GREEN / IN-BAND (model numerics sound):** FLA `KLD 0.137028` vs control
  `0.136563` = **delta +0.000465 < 0.01**; same-top-p **84.61% vs 83.73%** control
  (>= 84% baseline; FLA marginally better). Per-kernel bring-up validation vs host fp64
  on synthetic shapes: **o NMSE 2.2e-7, final-state 1.2e-7** (done BEFORE integration,
  per the "do not debug six kernels blind" rule).
- **DEFAULT PATH UNTOUCHED (canonical md5 GREEN with the code present):** paged-MoE
  `8cb0ce23777bf55f92f63d0292c756b0`, dense `5951a5b4d624ce891e22ab5fca9bc439`, both
  **default-off AND `LLAMA_GDN_FLA_CHUNK`-on** (the small-M greedy path bails to M5).
  `test-backend-ops GATED_DELTA_NET` **DEFAULT 46/46 OK.** Decode untouched
  (`GDN_CHUNK_MIN` untouched; decode stays on the sequential recurrence).
- **`test-backend-ops` env-on = 43-44/46 (`gdn_op_tests_env_on_green=false`; explicit
  tolerance judgment).** The FLA-engaged `head_size=128, n_seq_tokens>=64` cases
  marginally exceed the test's `1e-7` threshold (**ERR 1.03-1.06e-7**, fluctuating
  across the boundary run-to-run) because this port uses **plain tf32** where the shipped
  M5 uses **3xtf32 (CUTLASS fp32-emulation)** for the decay-coupled compounding state
  products; M5-chunked (`LLAMA_KV_PAGED=1`, no FLA) passes the SAME cases at `< 1e-7`.
  Judgment: a marginal tf32-vs-3xtf32 accuracy gap, **benign at the model level (KL
  green)**; tightening the port to 3xtf32 would only add mma count and **deepen** the
  perf NO-GO, so it was not pursued.
- **Engagement PROVEN:** `LLAMA_GDN_FLA_TRACE` fired `[gdn-fla] engage H=32 n_seqs=N
  n_tokens=128 NT=2` in `batched-bench`; nsys shows all six `gdn_fla::` kernels executing
  under `LLAMA_GDN_FLA_CHUNK=1` and none under default. Protocols honored: GPU lock held
  throughout and released; `LLAMA_MAX_BATCH_TOKENS` unset; sm_121a; nsys
  `--cuda-graph-trace=node`; 3+ iter S_PP medians; no external contention.
- **Provenance.** WIP on the DGX fork topic branch `p5-fla-gdn` at
  `2d64c37f08ad323038a44a89ab32189527c6ba29` (base `localai-paged` `653bb2f3d`, **NOT
  pushed, NOT landed**): new `ggml/src/ggml-cuda/gdn-blocked-solve.cu` + narrow dispatch
  in `gated_delta_net.cu` / `gated_delta_net.cuh`. Fork `localai-paged` HEAD **untouched
  at `653bb2f3d`**; the LocalAI series **stays at 46 patches (`0001-0055`)**; topic
  branches `p1-bf16-stream` / `p2-moe-region` / `p4-cbv2` left intact. Artifacts on the
  DGX `~/bench/p5_fla_gdn/`: `killgate_20260702_204225/` (RESULTS.md, spp_control.txt,
  spp_fla.txt, `nsys_{ctrl,fla}{2048,512}.{nsys-rep,kern.csv}`, GATES.txt,
  `kl_moe_{ctrl,fla}.log`, occupancy.txt, gdn-blocked-solve.cu, p5_fla_test.cu) and
  `standalone_20260702_203434/` (RESULTS.txt + p5_fla_test.cu, p5_m5_time.cu,
  m5_kernel_body.cuh).
- **Honest delta vs the +59.2 expectation.** The scope's conservative expected recovery
  was **~0-10 of the +59.2, "likely a shared-hardware floor."** Delivered: **0 recovery,
  a -63 us/tok regression on the FLA arm**; the floor is **confirmed**. The shipped M5
  fused smem-resident chunked scan (56.31 us/tok) is the winner and is **at or near the
  GB10 memory-bandwidth floor for this op.** This closes the last speculative prefill
  lever in the program. What binds is silicon (LPDDR5x bandwidth on the per-chunk h
  round-trip + the 99 KB smem cap forcing the split), not the algorithm; it lifts only on
  datacenter Blackwell (HBM + larger smem + TMEM), consistent with section 4's framing.

### P6: FP8 KV cache + smaller dtype/bandwidth items

- **Goal:** halve decode-time KV cache traffic (K/V stored fp8-e4m3 with a scale) and
  pick up remaining small dtype/bandwidth wins (FP8 projections where accuracy allows,
  matching vLLM's bf16-proj +13.7 bucket).
- **Mechanism (Audit B section 3):** fp8-e4m3 KV with per-tensor (or per-head) scales,
  loaded/calibrated (not dynamic-per-step); optional FP8 projections at the linear
  boundary keeping the residual stream bf16.
- **Files:** KV cache dtype path in `llama-kv-cache.cpp` (7 patches) + `paged-attn.cpp`
  (5 patches); FP8 proj in the fork GEMM files.
- **Env gate:** new default-off env (e.g. `LLAMA_KV_FP8=1`).
- **Correctness gate:** **KL band** (fp8 KV changes attention numerics; nearly free in
  accuracy per vLLM). Precision is **per-path**: validate paged vs non-paged separately.
- **P0 kill-gate:** enable fp8 KV; A/B decode t/s + KLD at N >= 128. **GO** if decode
  t/s + >3% with KLD in band. **NO-GO** if KLD out of band or throughput flat.
- **Expected recovery:** decode bandwidth on the KV read; part of bucket-4 bf16-proj
  (+13.7 prefill) via FP8 projections.
- **Effort:** medium.
- **Supersedes:** nothing rejected; additive bandwidth item.
- **Upstream-clash / rebase-safety:** `llama-kv-cache.cpp` is high-churn (7 patches);
  keep the fp8 path additive and gate the dtype selection narrowly.

---

## 4. Program-level arithmetic (if all phases land)

Conservative, showing the math. Baselines from section 2.

**Prefill (MoE decision model, paged 395.9 us/tok, vLLM 197.0, gap 198.9):**

| Bucket | delta | phase | conservative recovery |
|---|---:|---|---:|
| 3 dtype boundary tax | +36.6 | P1 | ~30 |
| 4 norms/glue (part) | +37.2 | P1 (norms) + P6 (FP8 proj) | ~18 |
| 2 GEMM tiling | +56.5 | P2 + P3 | ~40 |
| 1 GDN scan | +59.2 | P5 (NO-GO, CONFIRMED FLOOR) | 0 |
| 5 dispatch | +5.9 | P2/P4 | ~3 |

Recovered ~91-101 us/tok of 198.9. New paged wall ~295-305 us/tok. **Prefill S_PP goes
from 36% to ~55-65% of vLLM** (throughput ratio 197/300 ~= 66% best case, ~55%
conservative). Roughly a doubling. **What remains unreachable:** the GDN-scan 2.62x
residual (bucket 1: shared-hardware floor of 99 KB smem forcing C=16 + the GB10-hostile
blocked inverse) and the bf16-vs-FP4 peak ratio ceiling on the GEMM (FP4-MMQ already
optimal). Full 100% prefill parity requires datacenter Blackwell (tcgen05 + HBM + TMEM).

**Serving aggregate (llama server 718 t/s = 60.7% of vLLM server 1177; vLLM true
GPU-steady 1078):**

- ~8 pt is vLLM measurement inflation (not ours to recover; it means the honest target
  is 1078, not 1177).
- ~17 pt scheduler/graph-reuse: P4 + S3 recover ~10 pt on GB10 (host-loop is
  GB10-compute-bound, so P4's throughput payoff here is bounded; the rest is TTFT).
- ~14 pt GPU-steady kernel residual: P2+P3 (MoE fused-Marlin ~11 ms) + P1 (Triton
  elementwise ~10 ms) recover ~10-12 pt.

llama server goes ~60.7% -> **~80-83% of vLLM server** (~87-90% of vLLM's true
GPU-steady). Decode GPU-steady is already 86% of true; P1+P2+P3 close most of the 14 pt
residual to **~95%+ of vLLM's true GPU-steady**, with low-N dense already leading
(116.7% at N=8).

**TTFT:** P4 (continuous batching + chunked prefill co-batching decode) plus the prefill
gains (P1/P2/P3) target the 3.4x TTFT gap. Conservative: TTFT gap closes from ~3.4x to
~1.5-2x under load. It is bounded below by prefill throughput, which the program roughly
doubles.

**What stays unreachable and why:** (1) the GDN recurrent-scan bandwidth plateau (shared
hardware, and paged already leads); (2) the C=16-forcing 99 KB smem cap on the GDN solve
(joint algorithm+hardware); (3) the bf16 = half-FP4 tensor-core peak on sm_121. These are
the genuine floors; they lift only on datacenter Blackwell, not on GB10. The program's
honest ceiling on GB10 is roughly **prefill ~55-65%, serving-agg ~80%, decode-GPU-steady
~95%, TTFT within ~2x** of vLLM - a large closure of the current 2-3x, not 100% parity.

---

## 5. Execution rules (non-negotiable)

1. **Fork-first, always.** `mudler/llama.cpp:localai-paged` is canonical. Commit+push the
   fork branch FIRST, THEN regenerate the LocalAI patch series via `git format-patch`
   (1:1 tree-hash mirror). Never edit the series directly or add a patch with no fork
   commit (drift caused the build-broken 0044/0045). See
   [`PATCH_MAINTENANCE.md`](PATCH_MAINTENANCE.md).
2. **Per-path correctness gate.** Math-preserving change -> **per-path greedy md5**
   (canonical MoE `8cb0ce23777bf55f92f63d0292c756b0`, dense
   `5951a5b4d624ce891e22ab5fca9bc439`; paged md5 != non-paged md5 by design).
   Dtype/algorithm-changing change -> **KL band** (same-top-p >= the recorded baseline,
   KLD not worse than the current path; see [`PAGED_BITEXACT_NOTE.md`](PAGED_BITEXACT_NOTE.md)).
   Never force the md5 gate on a bf16/fp8 path.
3. **Noise-floor promotion rule.** Keep a lever only if its **median** improvement
   exceeds **max(2%, 3 sigma)** over the control medians. Flat-within-noise is a reject.
4. **Decode profiling MUST use `--cuda-graph-trace=node`.** Without it, nsys collapses
   each replayed decode graph into one opaque launch and reports a false "host-bound
   ~16% GPU busy" artifact (this is the mislabel that produced the retired ~56% headline;
   the true number is ~86%).
5. **One lever per A/B.** A standalone PoC win is **not** a result; gate on a
   separately-built in-backend A/B with only that lever changed. 0034 won as a PoC
   (57.7% FP4 peak, NMSE=0) and lost in-backend; that is the rule's origin.
6. **Record every rejected lever** in [`PARITY_HANDOFF.md`](PARITY_HANDOFF.md) with the
   DGX artifact path, the numeric result, and the mechanism verdict (integration tax vs
   kernel-intrinsic vs shared-hardware floor). The rejected-lever log is load-bearing:
   it is what prevents re-litigating a floor.

---

## 6. Risks and open questions

- **P5 is a shared-hardware floor - RESOLVED / CONFIRMED (2026-07-02, see the P5 RESULT
  above).** Phase74's standalone blocked-inverse ran at 0.59x the direct solve. The open
  question was whether the full FLA pipeline *in-backend* (register-resident inter-chunk
  state, chunk loop in-kernel) behaves differently from the standalone bench. **Answer:
  no - it is 2.12x SLOWER than M5 at npp2048 (119.46 vs 56.31 us/tok), S_PP -13.3%.** The
  per-kernel decomposition showed the blocked solve is only 2.8% of the bucket; the floor
  is the state-GEMM + per-chunk h-materialization to LPDDR5x that FLA's split-kernel
  structure forces (and the 99 KB smem cap that forces that split). P5 recovers 0 and is a
  **confirmed shared-hardware / memory-bandwidth floor.**
- **P1 segment-boundary converts.** Option A keeps f32 at segment edges; if the q36
  residual stream has many short segments, the boundary converts could eat the win.
  Open: how many bf16 segments survive across a q36 layer, and does the shared-expert
  path fork the stream?
- **P2/P3 all-or-nothing + aliasing.** The region executor must never materialize
  gate_up; if the q36 dense shared-expert-per-layer aliases the routed `gate_up` view,
  region ownership breaks and must fall back to node-at-a-time. Confirm the topology
  before widening ownership.
- **CUDA-graph capture safety.** Region-executor pool allocs must be shape-stable across
  replays (keyed on n_tokens/n_experts, never on data-dependent routing counts) or they
  force re-capture and negate the graph-reuse win. Dovetails with S1 (patch 0040).
- **Rebase risk concentration.** `ggml-cuda.cu` (8 patches), `mmq.cu` (5), `ggml.c`/`.h`
  (5 each), `llama-kv-cache.cpp` (7), `gated_delta_net.cu` (6) are exactly the files
  upstream churns for fusion/MoE. Mitigation is the series discipline: new `.cu` files,
  narrow additive `ggml_can_fuse` clauses, no new ggml tensor types, re-baseline md5 on
  every pin bump (weekly canary).
- **P4 is throughput-neutral on GB10.** Its measured value there is TTFT + fairness +
  enabling P2/P3; the throughput payoff is on non-GB10 silicon. Risk: over-investing in
  P4 as a GB10 throughput lever. Scope it as the enabler it is.
- **Datacenter-Blackwell dependency.** The program targets ~55-80% closure on GB10, not
  100%. The residual floors (GDN scan BW, C=16 smem cap, bf16=half-FP4 peak) lift only on
  tcgen05 + HBM + TMEM silicon. Do not promise GB10 parity.
- **Upstream may solve pieces for us.** PR #11867 (overlap graph build with processing)
  serves P4 on non-GB10; `GGML_CUDA_GRAPH_OPT` streams serve P3; PR #16016 (deterministic
  MoE mul_mat_id) could shift our recorded md5s (keep the per-path gate, re-baseline on
  pin bump). Align, do not duplicate.
