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
HEAD `237ad9b96` (the tree already carries the MoE-region seam plus four HEAD commits
`237ad9b96` bf16 GDN state cache, `afc2c7030` trace act-quant routes, `ea0875d14` gate
BF16 cuBLAS F32 output, `7967ad47f` route W4A16 direct-A stub - the team has already
started scaffolding P1 and P3).

### P1: bf16-native execution pass (kill the f32 convert / act-quant boundary tax)

- **Goal:** delete the convert-in/convert-out on every op boundary and run
  norm/add/rope/silu at half the memory traffic, so the residual/activation stream is
  bf16-resident (as in vLLM) rather than f32-resident with bf16 only as an in-GEMM
  transient. Targets prefill bucket 3 (+36.6) + part of bucket 4 (norms +11.1, glue),
  and decode elementwise (57 us/tok, 5%).
- **Mechanism (Audit C Area 1, option A):** extend the existing fusion pass
  `ggml_cuda_try_fuse` (`ggml-cuda.cu:4661`, called per node in the capture loop at
  `:5444`) to recognize a residual-stream *segment* (norm -> proj-GEMM -> add -> norm)
  and execute it through bf16 variants that keep the intermediate in a bf16 pool buffer,
  converting to f32 only at the boundary a non-owned node reads. The GEMM already
  computes through bf16 tensor cores; the win is deleting the per-op converts, not the
  GEMM. `LLAMA_BF16_CUBLAS_F32_OUT` (`ea0875d14`) is plank 1 (GEMM writes f32 directly
  from bf16 compute, skips the round-trip pool alloc + convert). Reject option B
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

### P2: expert-major fused routed-FFN region executor (grow the merged MoE seam into the real thing)

- **Goal:** drive both MoE GEMMs expert-major so the gate_up output never lands in
  global memory, deleting the one intermediate still materialized today and the
  redundant per-GEMM sort. Targets prefill bucket 2 (+56.5, the ragged-tile tax) and the
  decode MoE fused-Marlin ~+11 ms residual.
- **Mechanism (Audit C Area 2):** the seam already exists. `moe-ffn.cu` +
  `ggml_cuda_moe_whole_pattern_detect_early` (`:4678`) matches the
  `gate_up (MUL_MAT_ID) -> VIEW -> SWIGLU -> down (MUL_MAT_ID)` chain and the hook
  returns the node-skip count so the graph advances past the region. But it is a
  *partial* executor: `ggml_cuda_moe_routed_ffn_poc` (`moe-ffn.cu:456`) still runs the
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

### P3: Marlin-class large-M GEMM retry, ON TOP of P1+P2 (the forensics-informed retry)

- **Goal:** land the W4A16 Marlin-shape GEMM (FP4->bf16 in-register dequant + bf16
  mma.sync + cp.async double-buffer + dequant-once weight reuse across 16-64 M-rows)
  that vLLM uses on sm_121, now that its two prereqs exist. Targets prefill bucket 2's
  residual to the bf16-peak ceiling and the ragged-tile TC collapse.
- **Mechanism (Audit C Area 4):** finish the `direct_a` W4A16 stub. `w4a16-gemm.cuh:58`
  + the `7967ad47f` stub define `ggml_cuda_mul_mat_id_w4a16_grouped_direct_a`, which
  takes `src1` f32 directly with an `ids_to_sorted` map, fusing the activation cast into
  the kernel and skipping both the host-side expert-sort and the separate act-quant pass
  (the +15 us/tok the FP4-MMQ path pays). The engage gate is
  `w4a16-policy.h:ggml_cuda_w4a16_direct_a_should_engage_params` (NVFP4 src0, f32
  src1/dst, Blackwell, `LLAMA_W4A16_PREFILL_M>0`, tokens > M, `k%64==0 && n%128==0`),
  unit-tested in `test-cuda-w4a16-policy.cpp`. Hooks already wired:
  `ggml-cuda.cu:3085,3171` (direct-A) and `:3093,3188` (grouped, `[paged patch 0035]`).
  Add a one-time host-side weight repack cache into Marlin's interleaved layout
  (fork-owned loader in `llama-model-loader.cpp`, off the per-step path).
- **Files:** finish the kernel in `w4a16-gemm.cu` (fork-owned, kernel largely exists,
  ~300 LOC to finish the stub), repack in `llama-model-loader.cpp`, hooks in
  `ggml-cuda.cu`.
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
- **Effort:** low-medium (kernel + policy exist; the lift is the P1/P2 predecessors).
- **Supersedes:** 0035 (-39%) and 0034 in-backend fail. **Missing prereqs now
  supplied:** P1 delivers bf16 activations to the GEMM without converts; P2 delivers the
  persistent region that owns the tiling across both GEMMs so the bf16 activation is
  read once (the prior loss was ggml MMQ re-quantizing the y-operand per weight-row-tile
  x stream-k split).
- **Upstream-clash / rebase-safety:** `w4a16-gemm.cu`/`w4a16-policy.h` fork-owned;
  can ride upstream multi-stream `GGML_CUDA_GRAPH_OPT` (already in-tree:
  `concurrent_event`/`stream_mapping`, `ggml-cuda.cu:5305-5318`) for the K-loop cp.async
  overlap instead of a private mechanism.

### P4: token-granular continuous-batching scheduler (server-side only)

- **Goal:** one per-step token budget mixing chunked prefill + all ready decodes, with
  per-seq chunked-prefill cursors, cheap recoverable preemption, and adaptive bucketed
  decode emission. On GB10 this is a **TTFT + architecture-enabler** lever, **not** a
  throughput lever (the prior host-loop-dead measurement is real and must be respected);
  its throughput payoff is on non-GB10 silicon where decode goes host-bound again.
- **Mechanism (Audit C Area 3, Audit B section 1):** extend the shipped continuous-batch
  P1 (patch 0016, `server-context.cpp:3122-3200`, the dynamic decode-first prefill
  budget `T = clamp(LLAMA_MAX_BATCH_TOKENS, n_ubatch, n_batch)`,
  `prefill_budget_step = max(n_ubatch, T - D)`) into: (1) chunked prefill as a
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
| 1 GDN scan | +59.2 | P5 (speculative) | ~0-10 |
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

- **P5 is likely a shared-hardware floor.** Phase74's standalone blocked-inverse ran at
  0.59x the direct solve, and the 99 KB smem cap forces C=16. Open question: does the
  full FLA pipeline *in-backend* (register-resident inter-chunk state, chunk loop
  in-kernel) behave differently from the standalone bench? If not, P5 recovers ~0 and is
  recorded as a confirmed floor. Rank it last-but-one and gate it hardest.
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
