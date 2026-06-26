# OTHER_PATHS_INVESTIGATION.md

Read-only investigation of the four post-0023 paths (A MoE grouped-GEMM, B lm_head GEMM,
C TTFT/paged-pool burst, D dense CUDA-graph). One section per agent. No GPU except the
moe-gpu-profile agent.

---

## A. MoE grouped-GEMM gap (label: moe-gemm-source, READ-ONLY, no GPU)

### The decisive finding: vLLM's MoE on GB10 is MARLIN W4A16, not a native-FP4 grouped GEMM

Engine-log ground truth (`VLLM_DECODE_GROUNDING.md`, from `~/bench/h2h_moe_vllm.log`):
`"Using 'MARLIN' NvFp4 MoE backend ... Your GPU does not have native support for FP4
computation ... Weight-only FP4 compression will be used leveraging the Marlin kernel"`.
vLLM does NOT take its native-FP4 cutlass/trtllm MoE path on sm_121 (it whitelists only
sm_100/103 datacenter Blackwell for FP4-MMA MoE). So on this box vLLM's MoE is:

- `moe_align_block_size` (BLOCK-PADDED token-sort; `num_tokens_post_padded`, sentinel pad rows),
- **2 grouped `moe_wna16_marlin_gemm` launches per MoE block** (gate_up, then SiLU+mul, then down),
  each ONE launch over ALL experts, `use_fp32_reduce=True`,
- **W4A16: activations stay bf16, NEVER quantized**; FP4 weights dequantized in-kernel to bf16,
  bf16 MMA,
- the whole decode step under a FULL CUDA graph.

llama's MUL_MAT_ID on GB10 (mmq.cu id-branch + mmid.cu + mmq.cuh stream-k) is:

- `mm_ids_helper` token-sort/scatter, **NO block padding** (exact segments, `expert_bounds`),
- **activation FP4 quantize** (`quantize_mmq_fp4`) of the expert-gathered rows = W4A4,
- **1 persistent stream-k `mul_mat_q<NVFP4>` launch per projection**, native Blackwell FP4-MMA
  (`block_fp4_mmq`), fp32 accumulate + `stream_k_fixup`,
- per-expert-density `mmq_x` (M-tile) select (patches 0014/0015, default tile 64 @ density<=8),
- NOT under a CUDA graph.

### So the "missing fused grouped GEMM" does not exist - llama already HAS it

llama's grouped FP4-MMA stream-k IS the same sorted-grouped-GEMM algorithm vLLM uses, and on
GB10 llama's MoE GEMM is at a HIGHER-precision/native-FP4 tier than vLLM's W4A16 Marlin. The
MoE decode gap (77-83% of vLLM vs dense 90-117%) is therefore NOT a grouped-GEMM-architecture
deficit. The MoE-specific EXTRA gap (the ~10-15pt that MoE is worse than dense) decomposes as:

1. **W4A4 activation-quantize tax (llama-only, the biggest MoE-specific discrete cost).**
   llama quantizes activations to FP4 for the MoE GEMM; vLLM (W4A16) keeps them bf16 and pays
   ZERO activation quantize. At MoE decode npl128 that is 1024 up/gate rows (patch 0023 dedup'd
   the broadcast ones to 128 unique + a coalesced block gather) PLUS 1024 down_proj rows
   (distinct per expert, CANNOT be dedup'd). nsys decode-isolated (`MOE_QUANT_DEDUP_RESULTS.md`):
   `quantize_mmq_nvfp4` is still **457 ms** of decode GPU-time after the 0023 up/gate dedup; the
   remaining bulk is the down_proj per-expert re-quantize. vLLM's W4A16 choice is actually SMART
   for MoE decode on a bandwidth-bound box: keeping activations bf16 adds negligible activation
   bandwidth at M~8/expert but ELIMINATES the entire quantize pass.

2. **Un-graphed extra MoE nodes' launch bubbles.** Per MoE layer llama runs mm_ids_helper +
   quantize + gather + 2 grouped GEMMs + SiLU/mul + down-quantize + moe_sum as separate
   host-launched ggml nodes, none under a CUDA graph; vLLM runs moe_align + 2 grouped launches
   under a full decode graph. This is the SAME launch-bubble root cause `CRITICALPATH_GAP_ANALYSIS.md`
   pins for the GDN region (57 ms/step dense = 100% bubble), amplified for MoE by the extra
   quantize/gather/scatter nodes - consistent with MoE being relatively worse than dense.

3. **Ragged tiny-M tile + `need_check` partial-tail MMA** in the grouped stream-k. Already
   addressed by 0014/0015 and measured **NEUTRAL** on q36-35b-a3b: that model is bandwidth/
   SSM-recurrence-bound, not col-tile-occupancy-bound (the `LLAMA_MOE_DECODE_TILE` sweep shows 64
   is the only non-negative width and it is within noise). So the M-tile lever has nothing to
   bite on for THIS model; it banks +4.8% only on col-tile-bound MoE (Qwen3-Coder-30B).

### Bit-exact llama MoE-GEMM levers (ranked)

- **M1 (bit-exact, modest): down_proj activation-quantize kernel retune.** The remaining ~457 ms
  is dominated by the down_proj per-expert FP4 re-quantize (`ne11==n_expert_used`, no dedup
  possible). The per-block quantize is a pure per-thread function of 16 consecutive inputs (the
  property 0023 exploited to make its gather bit-exact), so the launch GEOMETRY can be retuned
  (occupancy/coalescing, like 0022 did for the recurrence and like 0023's coalesced-uint4 gather
  fix) while the quantized bytes stay BYTE-IDENTICAL. Also worth checking whether the down gather
  (`ids_src1`) is redundant when the SwiGLU intermediate is already expert-contiguous. Scope:
  nsys the down-branch `quantize_mmq_fp4` on GB10, retune block/grid, gate on test-backend-ops
  MUL_MAT_ID exact + greedy md5 == 0023. Expected: low single-digit % at npl128 (bounded - it is
  a fraction of a fraction of the step), but it is the only clean quantize-axis lever left after
  0023 and it is strictly bit-exact.

- **M2 (bit-exact, the structurally-correct big one, SHARED with path D/A.2): CUDA-graph the MoE
  decode step.** Graph replay does not change numerics => bit-exact. The MoE-specific extra node
  count (quantize+gather+scatter+2 GEMM+silu+sum/layer, none graphed) makes the launch-bubble tax
  larger for MoE than dense, which is exactly why MoE sits at 77-83% while dense is 90-117%.
  Capturing the decode forward removes those bubbles. This is the same lever the GDN/A.2 work
  scoped; it helps MoE MORE than dense. Highest-leverage bit-exact MoE win, but it is a
  decode-graph-capture project, not a MoE-GEMM kernel edit.

- **M0 (already shipped): 0017 `GGML_CUDA_FP4_MINBLOCKS` (min-resident-CTAs register-cap) and
  0014/0015 (`mmq_x` density auto-tile) already cover the FP4-MMA occupancy + M-tile axes of the
  SHARED `mul_mat_q<NVFP4>` kernel.** 0017 is bit-exact (register allocation cannot change
  results) and was tuned on dense; a MoE-targeted min-blocks re-sweep (grouped per-expert M-tiles
  have different occupancy than the dense M=128 GEMM) is a cheap bit-exact follow-up, but
  MOE_DENSITY_AUTO_TILE already found this model is bandwidth-bound, so headroom is likely small.

### NOT recommended (explicitly out of scope)

- **W4A16 bf16-activation MoE GEMM (matching vLLM's Marlin choice).** This is the single biggest
  MoE-specific structural difference and would erase the activation-quantize tax entirely, but it
  (a) is NOT bit-exact (bf16 activations vs llama's FP4), and (b) is the W4A16 occupancy-wall
  dead-end the docs flag (only ~9 TFLOP/178 t/s on GB10). Do not pursue.

### Verdict / ranking of path A

Path A is NOT a missing-kernel opportunity - llama already runs the sorted-grouped-FP4-MMA GEMM,
at a higher native-FP4 tier than vLLM's GB10 W4A16 Marlin fallback. The MoE-specific extra gap is
(1) the W4A4 activation-quantize tax vLLM structurally avoids by choosing W4A16, and (2) the same
un-graphed launch-bubble tax as the GDN region, amplified by MoE's extra nodes. The only purely
bit-exact, MoE-GEMM-local lever left is M1 (down_proj quantize retune, modest). The real MoE
bit-exact win is M2 (CUDA-graph the decode step), which is the SAME lever as path A.2/D and helps
MoE more than dense - so A's best lever collapses into the decode-graph effort rather than
standing alone. Recommend ranking A's standalone kernel value BELOW the decode-graph (M2/D) and
the lm_head (B) levers; fold A into the decode-graph build, and keep M1 as a cheap bit-exact
bank-shot.

Assisted-by: Claude:opus-4.8 [Claude Code]

---

## B. lm_head GEMM (label: cublas-lmhead, READ-ONLY, no GPU)

### The decisive fact: lm_head is BF16, not NVFP4 - so it CANNOT take the FP4 MMQ path

`output.weight` (the LM head) in q36-35b-a3b-nvfp4 is **type 30 = GGML_TYPE_BF16, NOT quantized**
(verified in `DECODE_PARITY_EXPLORE.md:298`: "2425 MB = 2.37 GB, read in full each step", 16% of
weight traffic). This is by construction: the model was quantized with `--tensor-type attn/ffn=
nvfp4`, which converts the attn+ffn tensors to NVFP4 and **leaves `output.weight` (and `tok_embd`)
at base BF16** - the standard recipe, because the final projection is the most logit-sensitive
tensor. The NVFP4 sidecar scales (`output_s`, `output_in_s`) are only created when
`output->type == GGML_TYPE_NVFP4` (`llama-model.cpp:1459`), so for the BF16 head `model.output_s`
is null.

### Why it runs cublas/nvjet and not MMQ (exact routing trace)

Graph: `qwen35moe.cpp:244` `cur = build_lora_mm(model.output, cur, model.output_s)` ->
`llama-graph.cpp:1093` is just `ggml_mul_mat(ctx0, w, cur)` (the null `w_s` skips the scale `ggml_mul`).
Then `ggml_cuda_mul_mat` (`ggml-cuda.cu:2540`) decides the kernel:

- `use_mul_mat_q` / `use_mul_mat_vec_q` BOTH require `ggml_is_quantized(src0->type)`. BF16 is NOT
  quantized (`is_quantized=false` for F16/BF16/F32; NVFP4 IS `is_quantized=true`, `ggml.c:748`).
  => **both MMQ paths are ineligible for the BF16 head.** (If the head were NVFP4 it WOULD route to
  the tuned FP4 `mul_mat_q` - this is exactly the difference.)
- At decode npl128 the activation `src1->ne[1] = 128` columns: `use_mul_mat_vec_f` is gated off by
  the mmvf batch cap; `use_mul_mat_f` (the MMF bf16 tensor-core GEMM) is gated off by
  `ggml_cuda_should_use_mmf` for the wide `151936-row x 128-col` shape.
- `use_batched_cublas_bf16` is true, but the batched-cublas branch additionally requires
  `src1->ne[2]*src1->ne[3] > 1` (a 3D/4D multi-batch GEMM). The decode lm_head is 2D
  (`ne[2]*ne[3] == 1`) => **batched-cublas branch is skipped.**
- => falls through to the final `else`: `ggml_cuda_op_mul_mat_cublas`. With `src0` BF16 +
  bf16-MMA hardware it takes the BF16 branch (`ggml-cuda.cu:1663`): `cublasGemmEx(CUDA_R_16BF,
  CUDA_R_16BF -> CUBLAS_COMPUTE_32F, TENSOR_OP)`. **That cublasLt kernel is `nvjet_sm121`.**

Cost (both models): dense `nvjet` lm_head = **12.17 ms = 3.66% of the 332.8 ms dense step**
(`F16_DENSE_RESIDUAL_PROBE.md:65`); MoE = **11.91 ms = 3.1%** (`CRITICALPATH_GAP_ANALYSIS.md:398`).

### CRITICAL correction: the current head is NOT "f32-lm_head" - it is already BF16-rounded

The task brief calls the baseline "f32-lm_head"; it is not. The cublas BF16 branch **downcasts the
F32 activation to BF16**, does BF16xBF16 with F32 accumulate, **writes the result as BF16** (dst is
`CUDA_R_16BF`), then upcasts BF16->F32. So today's "bit-exact reference" logits are already
**BF16-precision**, not f32. Two consequences:
1. Any NVFP4/FP8 head swap is measured against a BF16 baseline, not f32 - the precision delta vs
   the *true* f32 head is partly already paid.
2. A *different* BF16 GEMM kernel that also F32-accumulates and BF16-rounds the output is
   **bit-identical for the vast majority of logits** (differs only at rare BF16 rounding ties) -
   this is what makes option (c) below "essentially bit-exact".

### The options, and which break bit-exactness

- **(a) NVFP4-quantize the head -> tuned FP4 MMQ. BIGGEST win, BREAKS bit-exactness.** Weight
  2.37 GB BF16 -> ~0.6 GB NVFP4 (0.5625 B/wt = 4x fewer bytes) AND it then hits the already-tuned
  `mul_mat_q<NVFP4>` (0017) instead of cublas. Memory-bound floor drops ~4x => save ~8-9 ms =
  ~2.5% of the dense step. But NVFP4 < BF16 precision => **different logit bits, can flip the greedy
  argmax** = NOT bit-exact; and it is **UNFAIR vs vLLM**, which keeps its LM head BF16
  (`DECODE_PARITY_EXPLORE.md:358`: "fp8 LM head ... only matters if vLLM also quantizes it"). This
  is the same opt-in, non-bit-exact bucket as the f16-glue probe (already concluded SKIP).
- **(b) FP8 / Q8_0 head.** Smaller error than NVFP4 but still != BF16 bits => still NOT bit-exact,
  and it is not even on the tuned FP4 MMQ path, so it buys less speed than (a). No reason to prefer.
- **(c) Keep BF16 weight, swap the kernel (custom skinny wide-vocab streaming GEMM, or a cublasLt
  algo heuristic tuned for the thin-M / huge-N memory-bound shape).** The ONLY essentially-bit-exact
  option (F32 accumulate + BF16 round = identical except rounding ties, per the correction above).

### Realistic lever + scope: there is NO good bit-exact lever here

Bandwidth math kills option (c): `nvjet` moves 2.37 GB in ~11.9-12.2 ms = **~195-199 GB/s = ~72% of
the GB10's 273 GB/s peak**. The lm_head GEMM is therefore **already one of the MOST
bandwidth-efficient kernels in the step** - the overall decode step runs at only 40% util /
110 GB/s (`DECODE_PARITY_EXPLORE.md`). The bit-exact ceiling is tiny: even a perfect
HBM-saturating kernel (199 -> 273 GB/s) takes 11.9 -> ~8.7 ms = **save ~3 ms = ~0.9% of the dense
step**, and beating cublas's own tuned nvjet on a pure weight-stream shape is NOT guaranteed (it may
already be near-optimal). High kernel-writing effort, uncertain sub-1% payoff. (`F16_DENSE_RESIDUAL_
PROBE.md:97` independently estimates a bf16-glue nvjet recovery of only ~5 ms and flags it
"uncertain - may already run TF32" - consistent with little headroom.)

The structural reason: the head must read the **entire 2.37 GB weight for just 128 output columns**
(inherently memory-bound), and **you cannot cut those weight bytes without changing the dtype** -
i.e. bit-exactness and the only real speedup (fewer weight bytes) are **mutually exclusive** here.

### Verdict / ranking of path B

The lm_head cublas/nvjet GEMM is a **dead end for a bit-exact win**: it is already ~72% of peak HBM
(the step's most efficient major kernel), so a bit-exact kernel swap caps at <1% with real risk and
no guarantee of beating cublas. The only large win - NVFP4-quantizing the head (~2.5%) - is
explicitly non-bit-exact AND unfair vs vLLM (which keeps BF16), so it lands in the same opt-in
non-bit-exact bucket as f16-glue that was already shelved. Rank B's bit-exact value **at the bottom**
of the four paths. The one worthwhile note for the team is the correction that the head is already
BF16 (not f32), which slightly narrows what "bit-exact" even protects here; if the project ever
opens a *non*-bit-exact opt-in track, NVFP4-head (option a) is a clean ~2.5% dense lever that rides
the existing tuned FP4 MMQ - but it must be gated as opt-in and excluded from any vLLM-parity claim.

Assisted-by: Claude:opus-4.8 [Claude Code]

---

## A.2 / D. GPU-measured MoE decode decomposition + dense-graph stability (label: moe-gpu-profile, THE GPU AGENT)

nsys `--cuda-graph-trace=node` on a steady MoE decode at npl128 (q36-35b-a3b-nvfp4, HEAD f7409c2,
clean 0023 build-cuda). The measurement was decode-isolated: the run has a prefill phase (16384 tok,
the big-GEMM region) followed by 64 steady decode steps; I segmented the timeline by GPU-idle gaps,
dropped the prefill window, and aggregated per-kernel time over the 64-step decode window only
(`moe_decode_npl128.{nsys-rep,trace.csv}` on the DGX; extractor `decfull.py`/`grid.py`).

### MoE decode window: 98.3% GPU-bound, ~165 ms/step. Per-kernel share of decode GPU-time:

```
 41.9%  gated_delta_net_cuda            REC (SHARED with dense, already tuned 0018-0022)
 26.9%  mul_mat_q<NVFP4, M-tile=64>     MOE expert grouped GEMM (MUL_MAT_ID) <-- biggest MoE-specific kernel
  7.7%  nvjet_sm121 (cublas bf16)       attn/gdn bf16 projections + the BF16 lm_head (path B)
  2.7%  cutlass_80 bf16 s16816 relu     bf16 GEMM (shared-expert / gate)
  2.7%  k_bin_bcast (mul/add)           expert-combine + routing-weight scale + glue
  2.6%  k_get_rows_float                REC recurrent-state gather
  2.4%  flash_attn_ext_f16              attention
  2.3%  mul_mat_q<NVFP4, M-tile=128>    router / non-grouped FP4 GEMM
  2.1%  ssm_conv(+update)               REC
  2.0%  quantize_mmq_nvfp4              MOE W4A4 activation-quantize tax (3.25 ms/step)
  1.8%  convert_unary bf16<->f32        glue around the bf16 projections
  1.5%  cpy_scalar                      glue
  0.9%  rms_norm
  0.5%  REC gating act | 0.5% streamk_fixup | 0.3% mm_ids_helper | 0.3% argsort |
  0.2%  l2norm | 0.2% set_rows | 0.1% gather_mmq_fp4 | <0.1% topk/softmax/reduce (routing)
```

Bucketed: **Recurrence (shared, tuned) ~= 47.5%** (gdn 41.9 + get_rows 2.6 + ssm_conv 2.1 + gating
0.5 + l2norm 0.2 + set_rows 0.2). **MoE FFN+routing block ~= 31%** (grouped GEMM 26.9 + activation
quant 2.0 + streamk 0.5 + mm_ids_helper/argsort/gather/softmax/topk/reduce ~1.3 + the expert-combine
share of bin_bcast). **cublas/cutlass bf16 projections ~= 10.4%** (nvjet 7.7 + cutlass 2.7).
Attention ~2.4%. The recurrence is the single biggest term but it is shared with dense and already
the subject of 0018-0022, so it is NOT a MoE lever.

### The biggest MoE-specific kernel (the lever): mul_mat_q<NVFP4, M-tile=64> grouped GEMM

26.9% of decode = ~43.5 ms/step, avg **373 us/call**, grids of **2048 and 8192** 64-wide tiles
(blk=32 = 1 warp/block). Compare the dense FFN GEMM in the same family at npl128: `mul_mat_q<NVFP4,
M-tile=128>` avg **31 us/call**, grid 48. The grouped per-expert GEMM is ~12x the per-call cost and
launches 100-200x more tiles because each of 128 experts is a separate tiny-M sub-GEMM (128 tokens x
top-k / 128 experts ~= a handful of rows per expert) padded into 64-wide tiles. This is exactly the
ragged-tiny-M / col-tile-occupancy axis section A's 0014/0015 `mmq_x` density auto-tile already
covers and measured NEUTRAL on this bandwidth-bound a3b model. MMQ FP4 is integer/FP4-exact
independent of tile geometry, so this kernel IS bit-exact to retune (occupancy/min-blocks/M-tile),
but the headroom on THIS model is small (it is bandwidth-bound, not tile-occupancy-bound).

### Confirmations / quantifications of section A (from live GPU, not source-reading):

1. **Un-graphed at npl128: CONFIRMED in source, but NOT the npl128 bottleneck.** NVFP4 on sm121
   (turing_plus path) has `mmvq_mmid_max = 8` (`mmvq.cu:145`); MoE decode batch ne[2]=128 > 8, so
   `[TAG_MUL_MAT_ID_CUDA_GRAPHS]` (`ggml-cuda.cu:3273`) disables CUDA graphs for the WHOLE step and
   the MMQ grouped path (not MMVQ) is taken. HOWEVER the measured decode window is **98.3% GPU-util
   with ~7.8 us inter-step host gaps** - at npl128 the kernels are large enough to fully hide the
   per-op launch latency, so the un-graphed launch-bubble tax is negligible HERE. The un-graphed
   penalty is a SMALL-npl problem; at npl128 the MoE gap is in-kernel (grouped GEMM + quantize),
   not host bubbles. This refines A's M2: graphing the decode step helps small-npl MoE much more
   than npl128 MoE.
2. **W4A4 activation-quantize tax: CONFIRMED present but only 2.0% at npl128.** `quantize_mmq_nvfp4`
   = 3.25 ms/step in the decode-isolated window (A's 457 ms figure is a whole-run/different-window
   total). Real, and vLLM-W4A16 avoids it, but it is a small-single-digit term, not dominant.
3. **lm_head/projection cublas (path B): CONFIRMED ~12.4 ms/step** of nvjet in MoE decode (matches
   B's 11.91 ms), but that 7.7% bundle is mostly per-layer attn/gdn bf16 projections, not just the
   one lm_head.

### D. Dense CUDA-graph stability: f32 dense is STABLE, the bimodality was a BF16-only artifact

Dense (q36-27b-nvfp4) has no MUL_MAT_ID, so it stays fully CUDA-graphed. Measured S_TG @npl128:

```
intra-process (1 load, 6x npl=128, npp8/ntg48, N_KV=7168): 376.2 376.2 375.7 375.1 375.3 374.9  (spread <0.4%)
inter-process (6 separate procs, fresh graph capture each):373.6 377.0 376.8 376.6 376.2 375.7  (spread ~0.9%)
committed heavy config (npl128 ntg128, N_KV=32768):        333.3 / 334.8 / 335.9                 (spread ~0.8%)
```

No bimodality in either replay (intra-process) or capture (inter-process). The custom graph state
machine (`ggml-cuda.cu:4484`: warmup_complete requires 2 property-stable calls; the one-time capture
cost lands in T_PP, not S_TG) absorbs capture into prefill, which is the only "hint" (the first
in-process measurement has a slightly higher T_PP and a marginally lower S_TG, fully bounded). The
287/336/487/498 bimodality in the brief was the shelved BF16 SSM-state path (BF16_SSM_STATE.diff,
never applied), not the shipped f32 path. There is NO graphs-off env in this fork (graph enable is
compile-time USE_CUDA_GRAPH + the warmup machine), so a graph-disable A/B would need a rebuild; given
the f32 path is already stable to <1%, path D is a non-issue and not worth the rebuild.

### Verdict (GPU agent)

- The MoE decode gap vs vLLM at npl128 is **in-kernel, not host-overhead**: 98.3% GPU-util rules
  out the un-graphed launch-bubble story AT npl128. The single biggest MoE-specific kernel is the
  `mul_mat_q<NVFP4, M-tile=64>` grouped GEMM (26.9%, 43.5 ms/step); it is bit-exact to retune but
  bandwidth-bound on this a3b model (A's auto-tile already measured neutral), so the standalone
  bit-exact MoE-GEMM lever is REAL but BOUNDED. The recurrence (47.5%) is shared and already tuned.
- **Path D (dense graph instability) is closed: the shipped f32 dense path is stable (<1%, no
  bimodality).** No latent fragility, no rebuild warranted.
- Net ranking from the GPU side agrees with A/B: the MoE-GEMM and lm_head levers are both bounded
  and partly non-bit-exact; the only structurally large bit-exact MoE win (A's M2, graph the decode
  step) pays off mostly at SMALL npl, not at the npl128 where the benchmark gap is reported.

Assisted-by: Claude:opus-4.8 [Claude Code]

---

## C. TTFT / paged-pool burst degradation (label: ttft-burst-rootcause, READ-ONLY, source + committed traces)

Files read: `paged/paged_kv_manager.{h,cpp}`; patches `0004` (on-demand alloc), `0007` (persistent
manager + ref-counted prefix), `0008` (server cross-request share), `0013`/`0016` (prefill budget);
docs `QWEN36_NVFP4_BENCH.md`, `BENCHMARK_PROGRESS.md`, `CHUNKED_PREFILL_PLAN.md`,
`CONTINUOUS_BATCH_SCHEDULER_SCOPE.md`, `P1_DYNAMIC_BUDGET_RESULTS.md`, `FUTURE_LEVERS.md`.

### Part 1 - the static decode-first budget: why a 128-way burst hits 903 s dense / 213 s MoE TTFT

How the budget schedules (patch 0016, `server-context.cpp::update_slots`): each step builds ONE
mixed batch. Phase 1 appends every GENERATING slot's single sampled token UNCONDITIONALLY (no budget
gate), so after Phase 1 `batch.n_tokens == D` (the live decode load). Phase 2 then fills prompt
tokens, bounded by three predicates: the hard `batch.n_tokens < n_batch` (2048) ceiling, a per-step
`prefill_budget_step`, and a per-slot `prefill_cap_per_slot`. **Decode is structurally claimed first
and never capped; only prefill is throttled.**

At the shipped config (`LLAMA_MAX_BATCH_TOKENS=512`, i.e. T=512=n_ubatch) the dynamic terms
degenerate to constants:
- `prefill_budget_step = max(n_ubatch, T - D) = max(512, 512-D) = 512` for all D in [0,128] - the
  floor binds, the `T-D` adaptivity NEVER bites (exactly the "structural note" in
  `P1_DYNAMIC_BUDGET_RESULTS.md`).
- `prefill_cap_per_slot = min(T, ceil(0.04*n_ctx)) = min(512, 5243) = 512`, clamped to 512.

So each step admits at most 512 prefill tokens TOTAL and up to 512 per single slot. Each benchmark
prompt is exactly 512 tokens and there is NO round-robin (0016 drains slots in index order):
**the first waiting slot consumes the entire 512-token step budget with its whole prompt; the 128
prompts prefill strictly SERIALLY, one prompt per step.** Slot k's first token appears after ~k
prefill steps and each step co-batches the accumulating decode load, so step time grows. Mean TTFT
~= (half the prompts) x step_time ~= **903 s dense** (each step reads the full 28B NVFP4 weights) /
**213 s MoE** (3B active = cheaper steps). Decode_agg stays high (384/726 t/s) because Phase 1 seats
every decode token every step. This is the **deliberate decode-first tradeoff**: T=512 was chosen
for decode throughput + memory; TTFT was the sacrificed axis. The 903 s is partly self-inflicted by
the floor budget + lack of fairness, not a kernel limit (dense `prefill_tps` collapses to ~70 t/s
under the throttle vs vLLM's flat ~1420).

The fix (chunked-interleave / fair dynamic budget = P2 of `CONTINUOUS_BATCH_SCHEDULER_SCOPE.md`,
NOT implemented), three pieces in `update_slots` Phase 2, zero libllama change:
1. Raise T toward `n_batch` (2048) so the per-step total budget is large; keep decode-first via the
   REAL `prefill_budget_step = T - D` (leftover auto-shrinks as D rises, so the step never inflates
   past T even at npl128).
2. A per-slot chunk cap MUCH smaller than the budget (the `long_prefill_token_threshold` analogue),
   e.g. 128-256 tokens, so one prompt cannot monopolize the step.
3. A round-robin start offset over PROCESSING_PROMPT slots so leftover budget spreads across MANY
   waiting prompts per step.

Net: instead of "one full 512-prompt per step" (serial, last prompt waits 128 steps), each step
admits small chunks from ~T/cap prompts at once, so all 128 advance in lockstep and TTFT collapses
from O(k*step) to O(constant) - the vLLM 6-18 s regime. 0016's per-slot-cap variable already exists
but is inert at the shipped config and lacks the round-robin spreader. Honest boundary (already in
the docs): this closes TTFT, it does NOT lift the ~161/333 decode ceiling (a separate lever).

### Part 2 - the burst-degradation BUG: later lower-npl prefill collapses 507 -> 65 t/s, decode fine, restart cures it

The signature - prefill-only collapse, decode untouched, persists in-process, a server restart fully
cures it (the benchmark's documented "restart per npl" workaround) - points to persistent paged-pool
host state never restored short of `clear()`/teardown. Two compounding mechanisms, both confirmable
from the patch source:

**(1) RECLAMATION GAP - blocks are returned ONLY on a FULL-range wipe.** `paged_alloc` returns a
sequence's blocks to the pool in exactly two places (patch 0004, kept in 0007): `clear()` ->
`release_all`, and `seq_rm(seq, p0, p1)` ONLY when `p0 == 0 && p1 == MAX`. But llama-server's normal
slot lifecycle issues PARTIAL truncations: slot reuse with a retained common/BOS prefix calls
`seq_rm(slot.id, n_past, -1)` with `n_past > 0` (patch 0008 itself calls
`common_context_seq_rm(ctx, slot.id, n_past, -1)`); context-shift / partial rewinds likewise. None
satisfy `p0 == 0`, so the release hook never fires: the kv-cache frees those CELLS but the manager
still believes the sequence owns those BLOCKS. The two desync and the manager's effective free pool
shrinks every time. Patch 0008's own comment is the smoking gun - it added the `n_past < 16` gate
because a mismatched full-prompt reservation vs suffix-only submission "never leaves stale blocks
(which otherwise fragment the paged pool ... and crashed the server under high fan-out)". 0008 only
closed that hole for the narrow `share()` path; the general partial-`seq_rm` path stays unhooked, so
over a high-fan-out burst leaked blocks accumulate and never return.

**(2) FRAGMENTATION / NO COMPACTION - the free queue is permuted by the burst and never rebuilt.**
Even for cleanly freed blocks, `BlockPool::free_blocks` just `prepend_n`/`append_n`s them in free
order; no compaction, no pristine reset. After a high-fan-out burst (many interleaved alloc/free
across many seqs in the unified pool, or reversed-order frees in a per-stream pool) the free queue is
a scrambled permutation of physical block ids. A subsequent LOW-npl prefill then `popleft`s
physically SCATTERED blocks, so its 512-token KV scatter-WRITE plus the in-kernel paged-attention
GATHER lose locality across the KV span -> prefill throughput collapses. Decode is a single-token
append per step with a gather amortized over tiny per-step work, so it barely notices - exactly the
observed "prefill collapses, decode robust". The scramble + leak persist for the process lifetime
(only `clear()`/restart rebuilds a contiguous free queue) - precisely why restart-per-npl restores
507 t/s. Contributing factor: slots used in the burst but not reassigned next run are never released
(release fires only on next-task divergence), so a low-npl run sees a reduced, fragmented pool and
falls back to the stock contiguous allocator more often (the `place()->false->res.idxs.clear()`
fallback in find_slot), scanning a littered cell array - another prefill-only slowdown.

Fix scope (all gated behind `LLAMA_KV_PAGED`, default-off byte-identical, no libllama API change):
- **Fix-1 (core, ~30-50 lines): close the reclamation gap.** Add
  `paged::PagedKVManager::truncate(seq, n_keep)` that frees the trailing blocks of a request beyond
  block index `ceil(n_keep/bs)` (ref-counted, mirroring vLLM's free of the truncated block suffix),
  expose `paged_alloc::truncate(cache, stream, seq, n_keep)`, and call it from
  `llama_kv_cache::seq_rm` for the `p1 == MAX && p0 > 0` case (ideally any `[p0,p1)`). Manager
  accounting then tracks the kv-cache exactly; the leak stops.
- **Fix-2 (small): defrag on empty.** When a stream's cells reach `get_used() == 0`, rebuild that
  manager's free queue to pristine contiguous order (or recreate the manager) so a reused pool
  starts unfragmented.
- **Fix-3 (small): release on slot completion.** Add a paged release at server `slot.release()` so
  finished-but-idle sequences return blocks promptly and a later low-npl run sees a full, compact
  pool.
- **Fix-4 (optional hardening): best-fit / contiguous-run preference** in `get_new_blocks` + a
  defrag pass before the find_slot stock-fallback fires.

Validation repro (GPU-bound, for a later profiling pass): npl64 burst then npl8 on ONE server;
assert npl8 `prefill_tps` within ~10% of a fresh-server npl8, and that `paged_alloc::num_free`
returns to the fresh value after the burst drains.

### Verdict / ranking of path C

Two distinct things: a **BUG** (Part 2) and a **tuning tradeoff** (Part 1). Rank the BUG first - it
is a true correctness/hygiene defect, not a tradeoff: a long-lived production server silently
degrades under ordinary mixed load and currently REQUIRES the "restart per npl" crutch, unacceptable
in real serving. Fix scope is small and localized to the paged-alloc unit + one `seq_rm` call site,
default-off byte-identical, with a crisp pass/fail repro. The chunked-interleave scheduler (Part 1)
is the bigger HEADLINE (the weakest benchmark number, 903 s/213 s burst TTFT vs vLLM 6-18 s) but a
larger effort with a deliberate TTFT-vs-decode-ITL tradeoff to navigate. The two are complementary:
the scheduler reduces how punishing each burst is; the bug fix ensures the pool survives the burst
so the NEXT request is not poisoned.

Assisted-by: Claude:opus-4.8 [Claude Code]

---

## SYNTHESIS - ranking and the first build target (label: orchestrator)

The brief framed two tracks: **BIT-EXACT** levers (help the shipped f32 parity DEFAULT, included in
the vLLM-parity claim) and **SERVING** levers (gated behind `LLAMA_KV_PAGED`, default-off
byte-identical, outside the parity claim). The decisive cross-cutting finding from all four agents:
**there is no compelling first build target on the bit-exact decode-default track** - A is bounded,
B is a sub-1% dead end, D is closed - **while the SERVING track has one clear, high-ROI, tractable,
low-risk, byte-identical-default first target: the paged-pool burst-degradation bug.**

### Per-path scorecard

| Path | Expected gain | Tractability | Bit-exactness | Net |
|------|---------------|--------------|---------------|-----|
| **A** MoE grouped-GEMM | Standalone kernel: **bounded, low single-digit %** at npl128 (model is bandwidth-bound; 0014/0015 M-tile auto-tile already NEUTRAL here). The big MoE win = M2 = graph-the-decode-step, which is SHARED with D and pays off mostly at SMALL npl, not the npl128 benchmark point. | M1 (down_proj quantize retune) cheap; M2 a decode-graph-capture project (large). | M1 strictly bit-exact (byte-identical quantized output); M2 bit-exact (replay). Helps the DEFAULT. | Real but **bounded**; no clean standalone kernel win. Keep M1 as a cheap bank-shot; fold M2 into a decode-graph effort. |
| **B** cublas lm_head (nvjet) | Bit-exact ceiling **<1%** (~3 ms; nvjet already ~72% of peak HBM, the step's most efficient major kernel). The only big win (NVFP4 head ~2.5%) is non-bit-exact AND unfair vs vLLM (which keeps BF16). | Custom skinny-GEMM = high effort, uncertain it beats cublas. | Bit-exact option caps <1%; the 2.5% option is a logits change (opt-in only). | **Dead end** for the default. Rank LAST. |
| **C** TTFT / paged-pool burst | **Part 2 bug:** restores prefill from collapsed 65 -> ~507 t/s after a burst (removes the "restart per npl" crutch). **Part 1 scheduler:** the headline - 903 s/213 s burst TTFT -> vLLM 6-18 s regime. | **Part 2: small + localized** (paged-alloc unit + 1 seq_rm call site). Part 1: larger (fairness + admission + tuning). | Both gated behind `LLAMA_KV_PAGED`, **default-off byte-identical**. SERVING track (doesn't touch the parity-default numerics). | **Highest ROI x tractability.** Part 2 is a true correctness defect with a crisp repro. |
| **D** dense CUDA-graph instability | **Zero** - f32 dense measured STABLE (<1% spread, no bimodality). The 287/336/487/498 bimodality was the SHELVED BF16 SSM path, not the shipped f32 path. | n/a (would need a rebuild for a graphs-off A/B). | n/a | **CLOSED.** Not worth any work. |

### Ranked order (ROI x tractability x bit-exactness)

1. **C-Part2 - paged-pool burst-degradation bug fix.** Small, localized, default-off byte-identical,
   crisp pass/fail repro, removes a real production-serving defect + the benchmark's restart crutch.
2. **C-Part1 - chunked-interleave / fair dynamic budget.** The public-facing TTFT headline closer,
   but a larger effort and a deliberate TTFT-vs-ITL tradeoff. Do it AFTER the bug fix (the scheduler
   reduces burst pain; the bug fix keeps the pool alive across bursts).
3. **A-M1 - down_proj activation-quantize kernel retune** (cheap bit-exact bank-shot for the default;
   bounded payoff on this bandwidth-bound model). Optionally folded with a future decode-graph build
   (A-M2 / the shared MoE+GDN decode-graph capture), which is the only structurally large bit-exact
   MoE lever but a big project that helps small-npl more than npl128.
4. **B - lm_head kernel swap.** Bit-exact ceiling <1% with real risk. Skip unless a non-bit-exact
   opt-in track opens (then NVFP4-head ~2.5% dense, gated, excluded from parity claims).
5. **D - dense graph instability.** Closed, no work.

### THE FIRST BUILD TARGET: paged-pool burst-degradation bug fix (C-Part2)

**Why this one:** it is the only candidate that is simultaneously (a) high ROI - fixes a real
correctness defect that forces the "restart per npl" crutch in long-lived serving, (b) tractable -
small and localized to the paged-alloc unit plus one `seq_rm` call site, (c) safe for the parity
claim - gated behind `LLAMA_KV_PAGED`, default-off byte-identical, and (d) verifiable with a crisp
pass/fail repro. Every bit-exact-default alternative is bounded (A), a dead end (B), or closed (D).

**Implementation plan (incremental, each step independently shippable):**
1. **Fix-1 (core):** add `paged::PagedKVManager::truncate(seq, n_keep)` that ref-count-frees the
   trailing blocks beyond block index `ceil(n_keep/bs)`; expose
   `paged_alloc::truncate(cache, stream, seq, n_keep)`; call it from `llama_kv_cache::seq_rm` for the
   `p1 == MAX && p0 > 0` case (ideally any `[p0,p1)`). Closes the reclamation gap so manager
   accounting tracks the kv-cache exactly.
2. **Fix-2:** defrag-on-empty - when a stream reaches `get_used() == 0`, rebuild its free queue to
   pristine contiguous order.
3. **Fix-3:** paged release at server `slot.release()` so finished-idle sequences return blocks
   promptly.
4. **Fix-4 (optional):** best-fit / contiguous-run preference in `get_new_blocks` + a defrag pass
   before the find_slot stock fallback.

**Confirming measurement (the explicit repro, GPU-bound):** on ONE long-lived server, run an npl64
burst, let it drain, then run npl8. PASS if (i) npl8 `prefill_tps` is within ~10% of a fresh-server
npl8 (vs the ~65 vs ~507 collapse today), and (ii) `paged_alloc::num_free` returns to the
fresh-start value after the burst drains (proves no leaked blocks). Decode t/s must be unchanged.

**Bit-exact gate it MUST pass:**
- With `LLAMA_KV_PAGED` unset, the build is byte-identical to HEAD f7409c2 (the fix lives entirely
  inside the paged path) - `test-backend-ops` + the greedy-decode md5 against the 0023 baseline are
  unchanged.
- With `LLAMA_KV_PAGED` set, the fix changes only block ACCOUNTING and PLACEMENT, never KV values or
  compute, so the greedy-decode md5 on a fixed prompt is identical before vs after the fix (and the
  post-burst run produces the same tokens as a fresh-server run).

**Paths NOT worth building now:** B (lm_head, sub-1% bit-exact ceiling, the only big win is a
non-bit-exact unfair-vs-vLLM logits change), and D (dense graph instability, measured stable -
closed). A's standalone kernel value is bounded; keep A-M1 as a cheap follow-up and fold A-M2 into a
later decode-graph project, but it is not the first target.

**First target: ship the paged-pool burst-degradation bug fix (C-Part2, Fix-1 + Fix-2 + Fix-3).**

Assisted-by: Claude:opus-4.8 [Claude Code]
