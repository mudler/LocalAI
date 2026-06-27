# QUANT_GENERALITY - are the paged decode opts NVFP4-specific or quant-agnostic?

Source-verified classification of the paged decode optimizations (patches 0013-0029)
as either QUANT-AGNOSTIC (operate on the gated-DeltaNet f32/bf16 recurrent state, the
paged serving host path, or the matmul ROUTING - independent of the model's weight
quantization, so they help a Q4_K / Q8_0 / bf16 Qwen3.6 as much as an NVFP4 one) or
NVFP4-SPECIFIC (only fire for / only help GGML_TYPE_NVFP4 weights on a Blackwell GPU).

READ-ONLY, NO GPU. Every classification below is taken from the patch body source,
not from the prose claims. Hardware referenced for the empirical plan only.

---

## 1. THE GROUND TRUTH GATE: what makes anything NVFP4-specific

There is exactly ONE runtime gate in the whole ggml-cuda matmul stack that means
"NVFP4 on Blackwell":

    mmq.cu:  const bool use_native_fp4 = blackwell_mma_available(cc)
                                         && (src0->type == GGML_TYPE_NVFP4 ...);

(confirmed in ARCH_GENERALITY_AUDIT.md section gguf-targeting-1 and in patch 0023's
own diff context). A patch is NVFP4-specific iff the code it changes lives INSIDE a
`use_native_fp4` / `type == GGML_TYPE_NVFP4` / `blackwell_mma_available(cc)` branch.
Everything else - the gated-DeltaNet recurrence, the conv update, the SSM/conv state
caches, the MMQ-vs-MMVQ dispatch, the CUDA-graph guard, the host scheduler and paged
pool - is dtype-independent.

The recurrent state is the decisive fact: in this hybrid model the gated-DeltaNet
temporal state, the conv ring state, q/k/v/g/beta and the SSM scratch are ALL
GGML_TYPE_F32 (asserted explicitly in every new op builder: see 0018 ggml.c
`GGML_ASSERT(state->type == GGML_TYPE_F32)`, 0019 same, 0021/0028 conv asserts
`conv_states->type == GGML_TYPE_F32`). The weight quantization type never enters the
recurrence or conv kernels. So any patch that only touches those is quant-agnostic by
construction.

---

## 2. PER-PATCH CLASSIFICATION (with source evidence)

| patch | what it changes | classification | source evidence |
|-------|-----------------|----------------|-----------------|
| 0013 | static per-step prefill-token budget (LLAMA_PREFILL_BUDGET) | QUANT-AGNOSTIC | tools/server/server-context.cpp only; a host scheduler loop bound on prompt-token COUNT; no dtype anywhere; default-off byte-identical |
| 0014 | manual MoE token-tile (mmq_x) cap | QUANT-AGNOSTIC | mmq.cuh `mul_mat_q_case`; cap applies on `args.expert_bounds != nullptr` (the MUL_MAT_ID grouped path) for ANY templated `<type>`; no NVFP4 branch |
| 0015 | density-aware MoE token-tile auto-select | QUANT-AGNOSTIC | mmq.cuh; gate is `expert_bounds != nullptr` + per-expert density only, NEVER on src0 type. PROVEN on a non-NVFP4 model: the measured +4.8% win was Qwen3-Coder-30B (128 larger experts), test gate covers MXFP4 AND NVFP4 |
| 0016 | dynamic decode-first prefill budget (supersedes 0013) | QUANT-AGNOSTIC | update_slots() policy only; "identical decisions paged on or off", zero libllama/dtype touch; default-off |
| 0017 | FP4 GEMM decode mmq_y / minblocks tile tune | NVFP4-SPECIFIC, but DEFAULT-OFF / INERT | mmq.cuh `get_mmq_y_host`: fires only `type == GGML_TYPE_NVFP4 && blackwell_mma_available(cc)`. BUT the patch is a recorded NO-BUILD: every occupancy probe REGRESSED (kill-gate tripped), so nothing is enabled by default. Default build is byte-identical to stock; it changes no behavior |
| 0018 | in-place SSM recurrent-state write-back | QUANT-AGNOSTIC | gated_delta_net.cu + ggml.c; operates on the f32 recurrent state cache (`state->type == GGML_TYPE_F32`); removes a D2D f32 state copy. Weights never read by this op |
| 0019 | fused recurrent-state gather (ids read, no get_rows) | QUANT-AGNOSTIC | reads the f32 state cache via ids; builder asserts F32 on q/k/v/g/beta/state/state_dst; mirrors ggml_ssm_scan. No weight dtype involved |
| 0020 | gated-DeltaNet o_proj MMVQ->MMQ reshape | QUANT-AGNOSTIC (routing) | qwen35.cpp/qwen35moe.cpp/qwen3next.cpp: a 2D-vs-3D RESHAPE of the f32 activation so `src1->ne[1]=128` routes to MMQ instead of batch-1 MMVQ. The MMVQ(ne[1]<=8)-vs-MMQ dispatch is a generic ggml-cuda decision present for EVERY quantized type. See section 3 |
| 0021 | in-place conv-state fusion (conv+silu+ring write) | QUANT-AGNOSTIC | ssm-conv.cu + ggml.c new op asserts `conv_states/conv_kernel/x_cur/conv_state_dst == GGML_TYPE_F32`; pure f32 conv-state work |
| 0022 | gated_delta_net_cuda occupancy/coalescing retune | QUANT-AGNOSTIC | gated_delta_net.cu kernel: q/k/v/g/beta/state are all f32; the COLS_PER_WARP/NUM_WARPS fold is a scheduling change on the f32 recurrence. Never touches a weight tensor |
| 0023 | MoE NVFP4 activation-quantize de-dup | NVFP4-SPECIFIC | mmq.cu: the `gather_mmq_fp4` de-dup is INSIDE `if (use_native_fp4) { ... }`. Gathers `block_fp4_mmq`. The non-FP4 path (`quantize_mmq_q8_1_cuda`) is untouched. Confirmed NVFP4-only |
| 0024 | paged-pool burst reclaim (truncate/defrag/release) | QUANT-AGNOSTIC | paged-alloc / paged-kv-manager / llama-kv-cache host accounting; "never KV values or compute, no ggml op touched"; gated behind LLAMA_KV_PAGED |
| 0025 | MoE-decode CUDA-graph re-graph (graph-safe id path) | QUANT-AGNOSTIC (corrects hypothesis) | ggml-cuda.cu: relaxes the MUL_MAT_ID graph guard when `ggml_is_quantized(src0) && ggml_cuda_should_use_mmq(...)`. Gated on the GENERIC quantized-MMQ grouped path, NOT on NVFP4. See section 4 |
| 0026 | hybrid per-head f32/bf16 SSM state (--cache-type-ssm / tau) | QUANT-AGNOSTIC, default-off (and precision-changing) | common/arg.cpp + cparams type_s/type_r + tau; changes the RECURRENT-STATE cache dtype (f32 default, bf16 opt-in). Independent of the weight quant; default tau=0 keeps bit-exact f32 |
| 0028 | residual conv-tap gather fusion (ids read) | QUANT-AGNOSTIC | ssm-conv.cu new SSM_CONV_UPDATE_IDS op reads the f32 conv cache via ids; eliminates the last k_get_rows in the GDN decode path. f32 throughout |
| 0029 | block-table within-step host cache | QUANT-AGNOSTIC | llama-kv-cache.cpp / paged-attn.cpp: memcpy-reuse of an int32 block table across full-attn layers of a step; pure host pipeline, bit-exact |

(There is no patch 0027.)

### Summary count
- QUANT-AGNOSTIC (helps any weight quant): 0013, 0014, 0015, 0016, 0018, 0019, 0020,
  0021, 0022, 0024, 0025, 0026, 0028, 0029 - 14 of 16 landed patches.
- NVFP4-SPECIFIC: 0023 (the only landed NVFP4-only optimization) + 0017 (NVFP4-only but
  default-off / inert, no measured win).

---

## 3. 0020 IN DETAIL - MMQ-over-MMVQ at batched decode is a win for ANY quantized type

The hypothesis is CONFIRMED. 0020 is not an FP4 trick:

- The gated-DeltaNet op left its output in 3D SSM layout `[value_dim, n_seq_tokens=1,
  n_seqs=128]`, so the ssm_out matmul saw `src1->ne[1] = 1` with the 128 sequences
  stuck in `ne[2]`.
- ggml-cuda dispatches `ne[1] <= 8` to MMVQ (the batch<=8 GEMV) and larger to MMQ
  (the tensor-core GEMM). This `ne[1]`-threshold dispatch is type-INDEPENDENT: it is
  the same routing for Q4_K, Q8_0, Q6_K, MXFP4, NVFP4 - every k-/legacy-quant has BOTH
  an MMVQ (mmvq.cu vec_dot) AND an MMQ (mmq.cuh) path.
- The fix is a `ggml_reshape_2d` to `[value_dim, n_seq_tokens*n_seqs] = [6144, 128]` so
  `src1->ne[1] = 128` routes to the M=128 MMQ GEMM that amortizes the ssm_out weight
  read across all 128 sequences. Same contiguous data, bit-identical.

Why it generalizes: at batched decode (npl 32-128) the weight read of ssm_out is the
cost, and MMVQ at the degenerate batch-1 shape re-reads / fails to amortize the weight
for whatever dtype the weight is. MMQ at M=128 reads each weight tile once for all 128
tokens. That amortization is a pure bandwidth win that exists for every quantized
weight type, not just NVFP4. A Q4_K or Q8_0 Qwen3.6 has the exact same 3D-SSM-output ->
batch-1-MMVQ pathology and gets the same MMQ amortization from the reshape. (The patch
already routes the in-projection through MMQ; only the output was stuck in 3D.)

The same logic underwrites 0014/0015 (the MoE `mmq_x` token-tile is a generic grouped-
MMQ knob; the win was measured on a non-NVFP4 Qwen3-Coder-30B) and 0025 (section 4).

---

## 4. 0025 CORRECTS THE HYPOTHESIS - it is quant-agnostic, not NVFP4-specific

The hypothesis listed "the act-quant / quantize_mmq_nvfp4 portions of 0025" as
NVFP4-specific. That is a patch-number mismatch. The ACTUAL patch 0025
(0025-qwen35moe-nvfp4-moe-decode-regraph.patch) does NOT contain any act-quant /
quantize_mmq_nvfp4 code. Its entire diff is one hunk in ggml-cuda.cu:

    bool mmid_needs_sync = !ggml_is_quantized(src0->type) || node->ne[2] > mmvq_mmid_max;
    if (mmid_needs_sync && ggml_is_quantized(src0->type) &&
        getenv("LLAMA_MOE_FORCE_GRAPHS") &&
        ggml_cuda_should_use_mmq(src0->type, cc, src1->ne[2], src0->ne[2])) {
        mmid_needs_sync = false;   // keep CUDA graphs on for the grouped-MMQ id path
    }

The relax condition is `ggml_is_quantized(src0->type) && ggml_cuda_should_use_mmq(...)`
- the GENERIC quantized grouped-MMQ id-path, NOT NVFP4. `should_use_mmq()` returns true
for Q4_K / Q8_0 / etc. at large enough batch just as for NVFP4. So a Q4_K or Q8_0 MoE
Qwen3.6 whose MUL_MAT_ID takes the grouped MMQ path also keeps CUDA graphs across the
MoE decode step under LLAMA_MOE_FORCE_GRAPHS. 0025 is quant-agnostic.

LEVER2_GRAPH_COVERAGE_RESULTS.md confirms this is the role of 0025 ("0025's
[TAG_MUL_MAT_ID_CUDA_GRAPHS] env-gate keeps the grouped MMQ id-path graph-safe").

Where the hypothesis's "act-quant / quantize_mmq_nvfp4" actually lives: that is
LEVER 3 (LEVER3_ACTQUANT_FUSION_RESULTS.md - fuse W4A4 act-quant into RMSNorm/SiLU),
which is genuinely NVFP4-specific, BUT it was a measurement STOP and NEVER LANDED (no
patch 0030, no commit). Likewise LEVER 4 (NVFP4 the still-bf16 GDN/attn projections,
LEVER4_PROJNVFP4_RESULTS.md) is NVFP4-specific but FAILED its KL gate (~6% PPL) and was
NOT shipped. So the only NVFP4-specific code that actually landed is 0023 (+ inert 0017).

### Net correction to the hypothesis
- 0018/0019, 0021, 0022, 0028, 0026, 0013/0016, 0029, 0020: CONFIRMED quant-agnostic.
- 0023: CONFIRMED NVFP4-specific.
- 0025: WRONG in the hypothesis -> it is QUANT-AGNOSTIC (CUDA-graph guard on the generic
  quantized grouped-MMQ path). The NVFP4-specific "act-quant" work the hypothesis was
  thinking of is LEVER 3, which is unshipped (STOP), not patch 0025.
- Bonus: 0014/0015 (not in the hypothesis) are quant-agnostic, and 0017 is
  NVFP4-specific but default-off/inert.

---

## 5. RELATIVE-IMPACT BY WEIGHT-QUANT SIZE

Decode is bandwidth-bound on the weight read. The quant-agnostic opts target work whose
absolute cost is FIXED in the weight quant: the f32 recurrence, the f32 conv state, the
host pipeline. The weight-read buckets (MoE expert GEMM + dense projections) scale
~linearly with bits-per-weight. So the quant-agnostic opts deliver the same ABSOLUTE
millisecond saving at every quant, but the RELATIVE % shrinks as the weight grows.

Anchor: the measured MoE q36-35b-a3b NVFP4 decode step (MOE_GAP_VS_VLLM.md, step =
169.8 ms, GPU-busy 97.5%), split into quant-agnostic vs weight-quant-scaling buckets:

| bucket | ms/step @ NVFP4 | scales with weight bits? | which opts touch it |
|--------|-----------------|--------------------------|---------------------|
| Recurrence core (gated_delta_net) | 70.0 | NO (f32 state) | 0022 |
| Recurrent-state + conv gather/plumbing (k_get_rows 5.2 + ssm_conv 3.4) | ~8.6 | NO (f32) | 0018/0019/0021/0028 |
| Host bubble (sample+batch+block-table) | 4.2 | NO (host) | 0013/0016/0024/0029 |
| Router / norms / glue | ~5.4 | mostly NO | 0014/0015 partial |
| MoE expert GEMM | 47.3 | YES (4-bit now) | (weight read) |
| Dense GDN/attn projections + convert glue | 20.3 | YES | (weight read) |
| W4A4 act-quant tax (quantize_mmq_nvfp4) | 3.3 | (FP4 only) | 0023 |

Quant-agnostic, weight-size-fixed total: ~70.0 + 8.6 + 4.2 + 5.4 = ~88 ms (~52% of the
NVFP4 step). Weight-read buckets: 47.3 + 20.3 = ~67.6 ms (~40%).

Model the weight-read buckets as scaling with bytes-per-weight relative to NVFP4 (4-bit
= 1x): Q8_0 ~ 2x, bf16 ~ 4x. Hold the ~88 ms fixed (the recurrence f32 byte stream and
host time do not change with the weight quant), and recompute the recurrence/host
fraction of the step:

| weight quant | weight-read buckets (ms, est.) | fixed quant-agnostic (ms) | step (ms, est.) | recurrence+host % of step |
|--------------|--------------------------------|---------------------------|-----------------|---------------------------|
| NVFP4 (4-bit) | ~68  (1x) | ~88 | ~159 (+act-quant ~3) | ~52% (measured ~50%) |
| Q8_0 (8-bit)  | ~136 (2x) | ~88 | ~224 | ~39% |
| bf16 (16-bit) | ~272 (4x) | ~88 | ~360 | ~24% |

Reading this:
- The quant-agnostic SSM/serving opts deliver the SAME ~ms savings at Q8/bf16 as at
  NVFP4 (they remove fixed f32/host work). The headline % speedups quoted in the patch
  bodies (e.g. 0019 dense npl128 +37.8%, 0020 +31.7%, 0022 +11.1%) are the LARGEST at
  NVFP4 precisely because the fixed recurrence is the biggest fraction of the smallest
  (4-bit weight) step. The same absolute removal is a smaller % of a Q8 step and a much
  smaller % of a bf16 step, because the weight-read denominator grows.
- This MATCHES the brief's decomposition framing (recurrence ~40-50%, GEMM ~26-28% at
  NVFP4): at NVFP4 the recurrence dominates, so the recurrence-targeting opts are where
  the win is; as the weight quant grows the GEMM dominates and the recurrence opts
  matter relatively less (but never zero, and never negative).
- Corollary: the ONE NVFP4-specific landed lever, 0023, only addresses the ~3.3 ms FP4
  act-quant tax (and only the broadcast up/gate share of it) - the smallest bucket and
  its measured win is +1.7%. The big bit-exact wins are all quant-agnostic.

So the optimization set is overwhelmingly general: a Q4_K / Q8_0 / bf16 Qwen3.6 gets the
full recurrence + conv + serving + MMQ-routing benefit; only the small FP4 act-quant
de-dup (0023) does nothing for it (and the inert 0017 was never enabled).

---

## 6. EMPIRICAL CONFIRMATION PLAN (specify only - DO NOT run; the GPU is busy)

Goal: prove on hardware that the quant-agnostic opts FIRE and LIFT a non-NVFP4 Qwen3.6,
isolating them from the one NVFP4-specific lever.

### 6.1 Hardware
GB10 / DGX Spark (sm_121), when free. The DGX has live deployments; this plan is
read-only until then. (Any Blackwell or non-Blackwell CUDA host also works to prove
quant-GENERALITY - the recurrence/serving opts are not Blackwell-gated; only the NVFP4
FP4-MMA tier is. Running on a non-Blackwell card would ALSO demonstrate the opts help
where there is no use_native_fp4 path at all - a strong second proof.)

### 6.2 Build the non-NVFP4 control GGUF first (prerequisite)
The same Qwen3.6 architecture, re-quantized so the weights are NOT NVFP4 but the
gated-DeltaNet/conv recurrence is still f32:

  - Source: the existing q36-27b (dense) and/or q36-35b-a3b (MoE) f16/bf16 GGUF already
    on the DGX (~/work/darwin_36b_opus/f16.gguf is the MoE f16 used as the LEVER4 KL
    base; an equivalent dense f16 exists).
  - Produce: `llama-quantize f16.gguf q36-27b-Q4_K_M.gguf Q4_K_M` (primary control) and
    optionally `... Q8_0` and keep the f16/bf16 as the 16-bit control. Q4_K_M is the
    cleanest contrast: 4-bit like NVFP4 but a totally different (k-quant, non-FP4-MMA)
    weight path, so any shared win is provably from the f32 recurrence / routing, not
    from FP4.
  - Note: this requantize is free (no retrain) and must be done before any A/B.

### 6.3 Bit-exact gate per path (same method as the patch bodies)
For the bit-EXACT quant-agnostic opts (0018/0019/0020/0021/0022/0028/0029 and the
host 0013/0016/0024 default-off), the gate is: greedy `llama-completion --temp 0
--seed 1 --ignore-eos -n 256`, md5 of the output, patches-ON == patches-OFF on the
Q4_K_M control. Per path:
  - non-paged Q4_K vs paged Q4_K (expect the same benign paged-reduction FP-order
    delta noted in PAGED_BITEXACT_NOTE.md / 0029, gate with KLD/PPL not md5 across the
    paged boundary, md5-exact within a fixed paged/non-paged setting).
  - patches-on vs patches-off (see toggles 6.4) on the Q4_K control: byte-identical md5.
  - 0026 (bf16 SSM state) is precision-CHANGING -> gate with KLD-to-f16 + PPL, not md5,
    exactly like LEVER4 did; default tau=0 stays md5-exact.
  - test-backend-ops on the build: GATED_DELTA_NET, SSM_CONV, SSM_CONV_UPDATE,
    SSM_CONV_UPDATE_IDS, MUL_MAT, MUL_MAT_ID, GET_ROWS all green (these op tests are
    dtype-parametrized and already include non-FP4 types).

### 6.4 The clean A/B (decode_agg, llama-batched-bench)
Two arms, SAME Q4_K_M control GGUF, `-fa on -npp 128 -ntg 128 -npl 32,128 -c 33000`,
report S_TG (decode aggregate), median of 5 reps:

  - Arm A (patches-OFF baseline): the cleanest is two builds - the pre-0018 paged commit
    (the SSM opts not yet present) vs HEAD. If a rebuild is not wanted, approximate
    OFF on the single HEAD binary by setting every disabling toggle at once:
      fused GDN off (cparams.fused_gdn_ar/ch path disabled - the "fusion off" mode the
      patch docs A/B against), `GDN_NW=4 GDN_CPW=1` (0022 pre-retune), `LLAMA_MOE_AUTO_TILE=0`
      (0015), no `LLAMA_MOE_FORCE_GRAPHS` (0025 off), `LLAMA_PAGED_NO_BT_CACHE=1` (0029),
      `LLAMA_PAGED_NO_RECLAIM=1` (0024), `LLAMA_PREFILL_BUDGET`/`LLAMA_MAX_BATCH_TOKENS`
      unset (0013/0016), tau=0 / ctssm f32 (0026). The two-build form is preferred for a
      publishable number; the env form is a fast same-binary sanity A/B.
  - Arm B (patches-ON default): stock defaults (fusion on, 16x8, auto-tile on,
    FORCE_GRAPHS on for the MoE graph arm, bt-cache on, reclaim on).

### 6.5 What result confirms quant-generality
  1. The quant-agnostic opts FIRE on Q4_K: nsys on Arm B (Q4_K) shows the same kernel
     deltas the NVFP4 runs showed - `k_get_rows_float` bucket collapses (0019/0028),
     `concat_cont` + decode `cpy_scalar` gone and `ssm_conv_update` present (0021), the
     o_proj `mul_mat_vec_q m=1` bucket gone and absorbed into `mul_mat_q m=128`
     (0020 - now a Q4_K MMQ kernel, proving the routing win is not FP4-bound),
     `get_block_table` host time down ~90% (0029).
  2. The opts LIFT the non-NVFP4 model: Arm B S_TG > Arm A S_TG on the Q4_K control at
     npl 32 and 128, with the recurrence/routing opts contributing the bulk (expect a
     smaller % than the NVFP4 runs per section 5, but clearly positive and of the same
     absolute ms order).
  3. The NVFP4-specific lever does NOTHING on Q4_K: toggling 0023
     (`GGML_CUDA_MOE_QUANT_DEDUP=0` vs default) shows ZERO delta on the Q4_K MoE control
     (it never enters the `use_native_fp4` branch) - the negative control that isolates
     the one NVFP4-only optimization from the general ones.

A clean pass = Arm B beats Arm A on Q4_K with the SSM/conv/routing/host kernel deltas
present and 0023 inert. That proves the decode wins are quant-general; NVFP4 is just the
weight quant where they show the largest PERCENTAGE because its weight read is smallest.

---

## 7. ONE-LINE VERDICT

14 of the 16 landed paged decode patches (0013-0029) are quant-agnostic: they act on the
f32 gated-DeltaNet/conv recurrent state, the paged serving host path, or the generic
MMQ-vs-MMVQ / CUDA-graph routing, none of which read the weight tensor's quant type. Only
0023 is genuinely NVFP4-specific (and 0017 is NVFP4-only but default-off/inert). The
hypothesis was right except for 0025, which is quant-agnostic (a generic
`ggml_is_quantized && should_use_mmq` CUDA-graph guard); the NVFP4-specific "act-quant"
work it was conflated with is LEVER 3, which never shipped. The opts deliver fixed
absolute ms savings at any weight quant; the % is largest at NVFP4 only because its
4-bit weight read makes the fixed recurrence the biggest slice of the step.

Assisted-by: Claude:opus-4.8 [Claude Code]
