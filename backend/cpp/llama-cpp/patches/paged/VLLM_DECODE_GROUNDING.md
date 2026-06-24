# vLLM 0.23.0 eager-decode grounding: where the ~2.4x decode gap to llama.cpp comes from

Source-reading + grounding only (no GPU, no benchmarking, no llama code changes). This
decomposes vLLM 0.23.0's per-decode-step work in `enforce_eager` mode and attributes the
measured ~2.4x decode-throughput gap on GB10 (DGX Spark, sm_121) to its parts, so the
throughput thread can decide what llama.cpp would actually need (CUDA-graphed decode vs new
kernels) before anyone touches a kernel.

Hardware: NVIDIA GB10 / DGX Spark, sm_121 (CC 1210 = `GGML_CUDA_CC_DGX_SPARK`), unified
LPDDR5x ~273 GB/s. vLLM install read: `/home/mudler/vllm-bench/lib/python3.12/site-packages/vllm/`
(on `dgx.casa`, read-only). Evidence: engine logs `~/bench/h2h_dense_vllm.log`,
`~/bench/h2h_moe_vllm.log`; nsys decode trace `~/bench/decode_study/srv_decode2.sqlite`
(reproduced here via `cat2.py`); committed `QWEN36_NVFP4_BENCH.md`, `DECODE_GAP_STUDY.md`,
`CONTINUOUS_BATCH_SCHEDULER_SCOPE.md`.

## TL;DR (the evidence-based answer)

At batch ~128, ~1024 ctx, NVFP4, `enforce_eager` (no CUDA graphs on either side), vLLM decodes
~2.4x faster than llama.cpp. Decomposed:

1. **The gap is dominantly a KERNEL-efficiency gap, not a host-overhead gap.** The strongest
   single datum: during steady llama decode the GPU is **~94.6% busy** (nvidia-smi, real run) /
   85.5% in the nsys window (`DECODE_GAP_STUDY.md`; nsys adds gaps). A GPU that is already ~95%
   busy has at most ~5% exposed host bubble, so a CUDA graph (which only removes host/launch
   overhead) can recover at most that bubble. **CUDA-graphing llama's decode is therefore a
   minority lever: on the order of ~5-15% of the step, i.e. roughly ~10-20% of the 2.4x.** The
   remaining ~80-90% is the GPU spending its busy time in kernels that are simply slower per unit
   work than vLLM's.

2. **vLLM's eager decode step is cheap on the host by construction**, so its host time is small
   to begin with and hides behind the async CUDA stream: persistent pre-allocated input buffers
   updated with vectorized numpy (no per-token Python), attention metadata built once per step and
   shared across all layers, no GPU->CPU sync in the hot path, and a fixed small kernel-launch
   sequence per layer (2 ops per Linear, 2 grouped Marlin launches for *all* MoE experts).
   `async_scheduling` was **off** in this run (absent from both engine logs; default resolves to
   the synchronous `Scheduler`, `config/scheduler.py:168-176`), so vLLM achieved the 2.4x with
   *synchronous* per-step scheduling. The host advantage is structural, not pipelining.

3. **Where vLLM's kernels win:** (a) attention reads paged KV **in-kernel** via a block table in
   one batched `flash_attn_varlen_func` launch, with **no gather/copy** (vLLM never pays llama's
   paged `get_rows` + `cpy` tax, which is ~36% of llama's *paged* step); (b) the dense NVFP4 GEMM
   is a **native FP4-MMA cutlass** kernel with the activation-quant **fused** into the preceding
   RMSNorm/SiLU (no standalone `quantize_mmq` requant pass); (c) the MoE experts are **one grouped
   Marlin kernel per projection for all experts** (W4A16, in-kernel dequant); (d) on these Qwen3.6
   models a fraction of layers are **GDN linear-attention** whose decode is an **O(1)-in-context
   recurrent state update**, not an O(ctx) KV read.

4. **Sampling is not the gap** on either side: vLLM samples all ~128 sequences with a handful of
   batched on-GPU kernels (FlashInfer), greedy and a heavy sampler chain cost the same; this
   mirrors llama's own finding (`DECODE_GAP_STUDY.md`: greedy 1343 ms == 5-sampler 1346 ms).

## The measured gap (apples-to-apples, both eager)

From `QWEN36_NVFP4_BENCH.md` (matched NVFP4 weights, one GB10 box, vLLM 0.23.0
`--enforce-eager`, llama patch 0015 + budget-256), decode aggregate tok/s at npl128:

| model | llama (best) | vLLM | ratio | per-step (128 tok) llama -> vLLM |
|-------|-------------:|-----:|------:|----------------------------------|
| DENSE Qwen3.6-27B | 161.2 | 390.7 | **2.42x** | ~795 ms -> ~328 ms |
| MoE Qwen3.6-35B-A3B | 333.5 | 811.1 | **2.43x** | ~384 ms -> ~158 ms |

Both models converge to ~41% of vLLM at npl128 after llama's prefill-starvation is removed
(patch 0013), and at npl8 the kernels are at parity (dense 99%, MoE 84%). So the residual ~2.4x
is a steady-state decode property at high batch, not a prefill or scheduler artifact (the
scheduler was separately proven not to be the lever: a clean all-128-decoding run still tops out
at 157-161 dense / 333 MoE - `CONTINUOUS_BATCH_SCHEDULER_SCOPE.md`).

## Confirmed configuration (both sides eager, no CUDA graphs)

vLLM, both models (engine logs):
- `enforce_eager=True`, `CompilationMode.NONE`, `cudagraph_mode=<CUDAGraphMode.NONE>`:
  `"Enforce eager set, disabling torch.compile and CUDAGraphs ... -cc.mode=none
  -cc.cudagraph_mode=none"`, `"Cudagraph is disabled under eager mode"`. So no torch.compile, no
  inductor, no graph capture: the model runs as pure eager dispatch of custom ops.
- Attention: `"Using FLASH_ATTN attention backend out of ['FLASH_ATTN','FLASHINFER','TRITON_ATTN',
  'FLEX_ATTENTION']"`, `"Using FlashAttention version 2"`.
- Dense weight GEMM: `"Using FlashInferCutlassNvFp4LinearKernel for NVFP4 GEMM"` (native W4A4
  cutlass FP4-MMA), `"Enabled custom fusions: norm_quant, act_quant"`, FlashInfer autotuned the
  `fp4_gemm` (16 configs) at startup.
- MoE weight GEMM: `"Using 'MARLIN' NvFp4 MoE backend out of ['FLASHINFER_TRTLLM',...,'MARLIN',
  'EMULATION']"` with `"Your GPU does not have native support for FP4 computation ... Weight-only
  FP4 compression will be used leveraging the Marlin kernel"` (so MoE experts = W4A16 weight-only
  Marlin: in-kernel dequant + bf16 MMA), plus `"FlashInferFP8ScaledMM"` for the FP8 attention
  linears.
- Both models are **hybrid GDN**: `"Using Triton/FLA GDN prefill kernel"` and `"Setting attention
  block size to 784/1056 tokens to ensure attention page size >= mamba page size"` (dense 784, MoE
  1056). A decode-time `fused_recurrent_gated_delta_rule_packed_decode_kernel` is JIT-compiled.
- Sampling: `"Using FlashInfer for top-p & top-k sampling."`
- `async_scheduling` not present in either log -> synchronous `Scheduler`.

llama side (the brief's premise, corroborated by `CONTINUOUS_BATCH_SCHEDULER_SCOPE.md` review):
`-fa on`, paged KV, eager (no engaged CUDA graphs at batched decode). The `DECODE_GAP_STUDY.md`
nsys run explicitly set `GGML_CUDA_DISABLE_GRAPHS=1` to match.

## Decomposition of vLLM's eager decode step

All file paths below are under
`/home/mudler/vllm-bench/lib/python3.12/site-packages/vllm/`. The driver is
`v1/worker/gpu_model_runner.py::execute_model` (line 4005): host preprocess under
`synchronize_input_prep()`, then `_model_forward` under `set_forward_context`, then `compute_logits`;
sampling is a separate `sample_tokens` (line 4357). Under eager, `_determine_batch_execution_and_padding`
(line 3768) dispatches `CUDAGraphMode.NONE`, and `_model_forward` (line 3718) just calls
`self.model(...)` directly: no capture, no replay, same code every step.

### (a) Attention - one batched in-kernel paged-decode launch + O(1) GDN layers

- **Full-attention layers (FA2):** `v1/attention/backends/flash_attn.py`. `FlashAttentionImpl.forward`
  (667-848) issues **one** `flash_attn_varlen_func` (796-818) over all ~128 decode tokens, passing
  `key_cache`/`value_cache` (the raw paged block pools, **not gathered**), `cu_seqlens_q`,
  `seqused_k`, and **`block_table=attn_metadata.block_table`**. The kernel walks the block table to
  fetch each sequence's KV pages directly. In-kernel paged read confirmed: there is **no gather/copy**
  in the Python layer; the only KV write is `reshape_and_cache_flash` (a scatter of the new token via
  `slot_mapping`). FA2 disables vLLM's AOT host scheduler (`aot_schedule = (fa_version==3)` is False,
  333), so `schedule()` returns `None` (445-469): the per-step metadata `build()` (388-575) is **pure
  reference/scalar assembly**, no Python loop over the 128 sequences, no host scheduling, no sync.
- **Built once per step, reused across layers:** `supports_update_block_table=True` (300); the first
  full-attn layer calls `build()`, every later layer reuses it via `update_block_table()` (577-586,
  a `copy.copy`). So `build()` runs **once per decode step** for the whole KV group, not per layer.
- **GDN linear-attention layers (the hybrid half):** `model_executor/layers/mamba/gdn/
  qwen_gdn_linear_attn.py`, kernels in `model_executor/layers/fla/ops/fused_recurrent.py`. Pure decode
  takes `_forward_core_decode_non_spec` (1644-1696): two state-update kernels only -
  `causal_conv1d_update` + `fused_recurrent_gated_delta_rule_packed_decode` (Triton kernel 255-336,
  grid `(NV, B*HV)` = one batched launch over all 128 rows). Each program updates a **fixed-size
  [K,V] recurrent state** (`b_h *= exp(g); b_h += (beta*(v - h.k)) outer k; o = h.q`) - **no loop over
  the 1024 past tokens, no KV read.** This is **O(1) in context length**, while FA2 streams ~ctx KV
  per head per row. On these Qwen3.6 models the GDN layers make a chunk of the decode cost flat in
  ctx, a structural cheapness llama only gets if its GGUF implements GDN the same way (see caveat).

### (b) Weight GEMM - native FP4-MMA (dense) / grouped Marlin (MoE), M-batched, fused quant

- **Dense NVFP4 linear:** `model_executor/layers/quantization/modelopt.py::ModelOptNvFp4LinearMethod.apply`
  (1226-1232) -> `model_executor/kernels/linear/nvfp4/flashinfer.py::apply_weights` (56-89): exactly
  two GPU ops - `scaled_fp4_quant` (activation -> packed FP4 + blockscale) then
  `flashinfer_scaled_fp4_mm` (the autotuned `fp4_gemm`, a **native W4A4 cutlass FP4-MMA** whose
  **dequant is fused into the MMA epilogue** via the precomputed `alpha = in_gscale*w_gscale`). The
  activation-quant is itself folded away: `compilation/passes/fusion/rms_quant_fusion.py:98`
  (`norm_quant`: RMSNorm -> `scaled_fp4_quant` fused) and `act_quant_fusion.py:40,128`
  (`act_quant`: SiLU+mul -> FP4 fused). **There is no standalone full-tensor requantize pass** like
  llama's `quantize_mmq`, and the weight is never dequantized to a temp buffer.
- **MoE experts (Marlin W4A16):** `model_executor/layers/fused_moe/experts/marlin_moe.py`.
  `fused_marlin_moe` (227) does **one** `moe_align_block_size` token-sort then `_fused_marlin_moe`
  (59) issues **exactly two grouped kernels** - `moe_wna16_marlin_gemm` for gate_up (137) and for
  down (194) - **each a single launch covering ALL experts** (it walks `expert_ids`/`sorted_token_ids`
  internally; no Python loop over experts), with a `silu_and_mul` between and a `moe_sum` reduce
  after. W4A16 means weights are dequantized in-kernel and activations stay bf16 (never requantized).
- **Decode-M batching (the key throughput property):** the dense GEMM reshapes activations to (M, K)
  with M = total decode tokens (~128) and reads each FP4 weight **once for all 128 tokens**; the MoE
  grouped GEMM reads each routed expert's weight **once** for the ~M*topk/E tokens routed to it. At
  M~128 with FP4 weights these are weight-read / memory-bound (correct: the GB10 LPDDR5x ~273 GB/s
  is the floor), but the bytes are amortized over the whole batch. This is the ideal case and it is
  the same regime llama is in - so the GEMM gap is kernel efficiency (fused quant + native FP4 MMA),
  not a batching defect.
- **Host cost per layer (eager):** each `Linear.apply()` dispatches at most 2 `torch.ops` kernels; a
  dense layer's GEMM+norm/act portion is ~7-11 launches, a MoE expert block is ~5-6 launches **for all
  experts combined** (expert count does not multiply launches). Fixed, small, no per-tile/per-expert
  Python.

### (c) Sampling - fully batched on-GPU, negligible

`v1/sample/sampler.py::Sampler.forward` (72) operates on the whole `[num_seqs, vocab]` logits
tensor: batched `argmax` (greedy, 240) or temperature `div_` + one FlashInfer
`top_k_top_p_sampling_from_logits` (`v1/sample/ops/topk_topp_sampler.py:493`) + `torch.where`
(296-301). **No per-sequence Python loop** in the hot path. Per-seq params live as pre-staged GPU
tensors `temperature/top_p/top_k[num_seqs]` (`v1/worker/gpu_input_batch.py:184-205`), copied once via
non-blocking H2D and rebuilt only on batch change (`refresh_metadata`, 815-829). Greedy and the full
chain are the same batched-op class. Sampled-token D2H is async (CUDA-event gated, 243-313);
detokenization runs on CPU in the async output processor (`v1/engine/output_processor.py`). Sampling
is a negligible tail and does not stall the GPU loop - exactly as on the llama side.

### (d) Host / Python per-step loop - cheap by construction, hidden behind the async stream

`execute_model` host prep, all incremental on persistent buffers (`_prepare_inputs`, 1872+):
- `block_table.commit_block_table` started **first** to overlap its copy with following CPU work
  (1890); each step appends only newly-allocated block ids (`append_row`), usually <=1 at decode.
- positions / token gather are **vectorized numpy + a single `torch.index_select`** into the
  pre-allocated `input_ids.cpu` (1928-1939); `query_start_loc`/`seq_lens` set by slice ops
  (1979-1990). `slot_mapping` is one Triton kernel (`v1/worker/block_table.py`). **No per-token, no
  per-request Python loop** in the steady decode path.
- `CommonAttentionMetadata` assembled once (2287-2305), then the attention builder runs once per KV
  group (see (a)).
- The forward runs under `set_forward_context(...)` with `cudagraph_runtime_mode=NONE`; `_model_forward`
  is a direct `self.model(...)`.
- **No GPU->CPU sync in the hot path:** the sampled-token copy is `non_blocking` + event-gated;
  `execute_model` returns after launching the forward, and the cheap host prep for the next step
  overlaps the GPU executing the current step on the async CUDA stream (CUDA launches are
  non-blocking). `async_scheduling` was off, so this overlap is just ordinary CUDA async, not
  pipelined scheduling - yet it is enough because the host work is so small.

What llama-server's per-step C++ loop pays that vLLM does not (host side, graph-addressable):
ggml rebuilds/reallocates the compute graph each decode step and dispatches ~1k kernel launches from
the loop on the weak Grace ARM cores (`CONTINUOUS_BATCH_SCHEDULER_SCOPE.md` review). vLLM's persistent
buffers + build-once-reuse metadata + fixed launch sequence are exactly the things that keep its eager
step host-cheap; llama could borrow these (persistent device KV/block metadata, build the ggml graph
once and reuse it, zero per-step host sync) to shrink the bubble **without** a full CUDA graph.

## The llama side, for the split (nsys, reproduced)

`~/bench/decode_study/cat2.py` over `srv_decode2.sqlite` (Qwen3-32B dense, pure full-attention, 64
layers, batch 32, 1024 ctx, paged, eager), reproduced now:

```
window_span_s 24.960  sum_kernel_s 21.348  gpu_busy_pct 85.5
ATTENTION (flash_attn_ext_f16) 10.177 s  47.7%
kv_copy_cast (cpy_*)            3.903 s  18.3%
embed_gather_rows (get/set)    3.803 s  17.8%   <- the PAGED gather tax
GEMM_weight (mul_mat)          3.173 s  14.9%
GEMM_act_quant (quantize_mmq)  0.172 s   0.8%
rmsnorm/silu/rope/add          ~0.12 s   ~0.6%
```

So on llama's paged decode step: ~84% is KV/attention (attention 47.7% + KV copy 18.3% + paged
gather 17.8%), ~16% is weight GEMM, and the host loop is **hidden** (GPU 85-94% busy; greedy ==
heavy-sampler step time). Mapping each bucket to vLLM:

| llama bucket (paged) | nsys % | vLLM equivalent | vLLM avoids it? |
|----------------------|------:|-----------------|-----------------|
| paged KV gather (`get_rows`) | 17.8% | block table read **in-kernel** | **Yes, entirely** (no such op) |
| KV copy/cast (`cpy_*`) | 18.3% | KV written once into block pool, read in place | Mostly |
| decode attention (`flash_attn_ext_f16`) | 47.7% | FA2 paged-decode varlen (+ O(1) GDN layers) | Same op, faster kernel; GDN is cheaper still |
| weight GEMM + act quant | 15.7% | fused native-FP4 / grouped Marlin, no separate requant | Faster + removes the requant kernel |
| host serving loop / sampling | ~0 (hidden) | cheap persistent-buffer prep, batched GPU sampling | Both hidden; vLLM also cheap |

Note: the nsys decomposition is on **Qwen3-32B (pure attention)**; the 2.4x throughput numbers are on
**Qwen3.6 hybrid GDN** models. The bucket *shares* differ between the two (GDN shifts work off
attention), but the lesson - llama's step is GPU-bound on attention + the paged gather + FP4 GEMM,
with the host hidden - transfers.

## The split of the 2.4x: kernel vs host (graph-addressable)

Anchored on the measured **~94.6% GPU busy** during steady llama decode (nvidia-smi,
`DECODE_GAP_STUDY.md`):

- **Host / CUDA-graph-addressable: the minority, ~5-15% of the llama step (=> ~10-20% of the 2.4x).**
  A GPU that is ~95% busy exposes at most ~5% host idle; a CUDA graph (capture-once, replay) removes
  per-step launch latency + ggml graph rebuild/realloc and can tighten inter-kernel gaps, plausibly
  recovering ~5-15% of the step in the best case. On llama's ~795 ms dense step that is ~40-120 ms of
  the ~467 ms gap. **A CUDA graph cannot close a 2.4x gap**, because the gap is mostly the GPU's busy
  time, not idle. (The fraction shrinks further at batch 128 vs the nsys batch 32: the per-step launch
  count is fixed while per-kernel work grows, so host overhead is a smaller share at higher batch.)
- **Kernel efficiency: the majority, ~80-90% of the 2.4x.** The GPU's busy time goes into kernels that
  are slower per unit work than vLLM's, decomposed:
  - **the paged gather regression (~36% of llama's *paged* step; `get_rows`+`cpy`)** - vLLM never pays
    it because it reads paged KV in-kernel. This is the single biggest discrete, llama-specific,
    addressable chunk, but removing it only restores llama's own *stock* path; stock is still ~2x off
    vLLM (`DECODE_GAP_STUDY.md`).
  - **long-context decode-attention** (the largest residual; attention is ~48% of the step and grows
    with ctx) - llama's `flash_attn_ext_f16` decode is slower than vLLM's FA2 paged-decode on sm_121,
    and slower still than the O(1) GDN layers on these models.
  - **the FP4 weight GEMM floor** (~15-30%) - vLLM fuses the activation-quant into the norm/SiLU and
    uses native FP4-MMA / grouped Marlin; llama runs `mul_mat_q` + a separate `quantize_mmq` requant.

## Ranked list: what llama would need to close the 2.4x, and how much each buys

1. **Do not pay the paged gather at decode. [largest discrete, llama-addressable; ~36% of the paged
   step]** Either disable paged KV for decode-latency workloads, or read paged blocks **in-kernel via
   a block table** like vLLM (no `get_rows`/`cpy`). This is a kernel change (a real in-kernel
   paged-decode read), not a graph change. Caveat: it only brings the paged path back to llama-stock;
   stock is still ~2x off vLLM, so this is necessary but not sufficient.
2. **Faster long-context decode-attention kernel. [biggest residual; partly structural]** A proper
   flash-decoding / split-K-over-KV, GQA-grouped, in-kernel-paged decode kernel for sm_121 (this also
   subsumes lever 1). Deep CUDA work, gated by kernel maturity on Blackwell-class parts. This is where
   the context-scaling gap lives and where most of the 2.4x is.
3. **Fused FP4 weight GEMM. [bounded; ~15-30%]** Fold the activation-quant into the preceding norm/SiLU
   (vLLM's `norm_quant`/`act_quant`) and into the GEMM epilogue; use native FP4-MMA where the part
   supports it. Removes the separate `quantize_mmq` pass. Bounded below by weight-read bandwidth
   (~19 GB/step over 273 GB/s).
4. **CUDA-graph the steady-state pure-decode step. [smallest, cheapest; ~10-20% of the gap]** Capture
   the all-128-decoding step once and replay (it is already fixed-shape at steady decode - the
   scheduler does not need to change to enable this, per `CONTINUOUS_BATCH_SCHEDULER_SCOPE.md` P3).
   Recovers the ~5% GPU-idle bubble + ggml per-step graph rebuild/realloc + launch latency on the weak
   Grace cores. A real, independent, low-risk win, but bounded by the ~95%-busy measurement: it does
   **not** close the kernel gap. Cheaper host-side half-measures that need no graph: persistent device
   KV/block metadata, build the ggml graph once and reuse it, and remove any per-step host sync (mirror
   vLLM's persistent-buffer + build-once-reuse + non-blocking-D2H pattern).
5. **Verify llama's GDN/linear-attention decode path. [architectural, model-specific]** On these
   Qwen3.6 hybrids vLLM runs the linear-attention layers as an O(1)-in-ctx recurrent state update. If
   llama's GGUF runs those layers as full attention (O(ctx)) rather than a recurrent state, that is a
   per-layer decode cost vLLM structurally avoids on exactly these models - check before attributing
   the whole residual to the full-attention kernel.

## Honest bottom line

The ~2.4x eager decode gap is **dominantly a kernel-efficiency gap (~80-90%), not a host-overhead
gap.** The decisive evidence is that llama's GPU is already ~94.6% busy during steady decode, so the
CUDA-graph-addressable host slice is a minority (~10-20% of the gap), recoverable but bounded. The
bulk of vLLM's advantage is concrete kernel work: an in-kernel paged-decode read that eliminates
llama's gather/copy tax (~36% of the paged step), a faster long-context decode-attention kernel, a
fused native-FP4 GEMM, and (on these specific models) O(1)-in-ctx GDN linear-attention layers. vLLM's
host loop is cheap by construction (persistent buffers, build-once-reuse metadata, no hot-path sync,
fixed small launch sequence) and it achieved the 2.4x with *synchronous* scheduling and *no* CUDA
graphs - so the host is not where vLLM's lead comes from, and a CUDA graph is the cheapest but
smallest of llama's available levers, not the silver bullet. The throughput effort should be scoped
as kernel work (in-kernel paged-decode read + flash-decoding attention + fused FP4 GEMM) with a
CUDA-graphed steady-state decode as a separate, bounded, lower-risk add-on.

## Key source citations (on dgx.casa, read-only)

- Eager driver / host loop: `v1/worker/gpu_model_runner.py` execute_model 4005, _model_forward 3718,
  _prepare_inputs 1872, _determine_batch_execution_and_padding 3768, sample_tokens 4357,
  synchronize_input_prep 3704; `v1/worker/block_table.py`; `v1/worker/gpu_input_batch.py:184-205`.
- Attention: `v1/attention/backends/flash_attn.py` (forward 667-848, varlen call 796-818, builder
  388-575, update_block_table 577-586); `model_executor/layers/mamba/gdn/qwen_gdn_linear_attn.py`
  (decode 1644-1696); `model_executor/layers/fla/ops/fused_recurrent.py` (kernel 255-336).
- GEMM: `model_executor/kernels/linear/nvfp4/flashinfer.py:56-89`;
  `model_executor/layers/quantization/modelopt.py` (NvFp4 LinearMethod 1103-1232, MoE 1381-1666);
  `model_executor/layers/fused_moe/experts/marlin_moe.py` (59-225, 227-360, 732-895);
  `compilation/passes/fusion/rms_quant_fusion.py:98`, `act_quant_fusion.py:40,128`.
- Sampling: `v1/sample/sampler.py:72-302`; `v1/sample/ops/topk_topp_sampler.py:55,460-497`;
  `v1/sample/metadata.py`; `v1/engine/output_processor.py`.
- Config: `config/scheduler.py:146,168-176` (async_scheduling default -> sync Scheduler).
- Evidence: `~/bench/h2h_dense_vllm.log`, `~/bench/h2h_moe_vllm.log`, `~/bench/decode_study/cat2.py`
  over `srv_decode2.sqlite`; this worktree `QWEN36_NVFP4_BENCH.md`, `DECODE_GAP_STUDY.md`,
  `CONTINUOUS_BATCH_SCHEDULER_SCOPE.md`.
</content>
</invoke>
