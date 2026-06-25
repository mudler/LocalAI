# Bit-exact vs vLLM, and the f32-preserving-parity hunt (Qwen3.5 gated-DeltaNet)

Label: crossengine-bitexact (READ-ONLY, no GPU). Adversarial source+numerics study.
Model: q36-27b-nvfp4 (dense, `Qwen3_5ForConditionalGeneration`) / q36-35b-a3b-nvfp4
(MoE, `Qwen3_5MoeForConditionalGeneration`). Engines: llama dev `~/llama-paged-dev`,
vLLM 0.23.0 `~/vllm-bench`. Decode B=128, enforce-eager / graphs-off, GB10 (~273 GB/s).

> **CORRECTION NOTICE (supersedes the earlier draft of this file).** A prior pass concluded
> "vLLM's GDN state cache is bf16, so the 2x recurrence-DRAM gap is f32(llama)-vs-bf16(vLLM)
> width" (old B2/B3). **That is wrong.** It read `gated_delta_net_state_dtype(..., mamba_ssm_cache_dtype="auto")`
> as auto->model-dtype=bf16, but it did **not** trace the Qwen3.5-specific config override that
> reassigns `mamba_ssm_cache_dtype` from `"auto"` to `"float32"` *before* the state dtype is
> resolved. **vLLM stores this model's gated-DeltaNet temporal state in float32**, the same width
> as llama. Proof chain in Part B. Everything in Part C is re-derived from the corrected dtype.
>
> **INDEPENDENT RE-VERIFICATION (this pass, live DGX source).** Two separate sub-agents reached
> *opposite* dtype readings (one f32, two bf16). The contradiction was resolved by reading every
> link of the chain directly, not by majority vote. All eight links confirm **float32 temporal
> state**: `config.json text_config.mamba_ssm_dtype = "float32"` (both served models);
> `config/cache.py:129` default `mamba_ssm_cache_dtype = "auto"`; the bench scripts
> (`h2h_dense_vllm.sh`, `h2h_moe_serve_vllm.sh`, `serve_nvfp4.sh`) pass **only**
> `--enforce-eager --gpu-memory-utilization 0.85 --max-model-len 4096` (no `--mamba-ssm-cache-dtype`,
> no `--dtype`); `config/vllm.py:847 __post_init__` -> `:856 try_verify_and_update_config()` (runs at
> finalize, before any state-dtype resolution); `MODELS_CONFIG_MAP` (`models/config.py:622-623`) maps
> both `Qwen3_5ForConditionalGeneration` and `Qwen3_5MoeForConditionalGeneration` ->
> `Qwen3_5ForConditionalGenerationConfig`; its override body (`config.py:546-549`)
> `if mamba_ssm_cache_dtype=="auto": cache_config.mamba_ssm_cache_dtype = mamba_ssm_dtype` **fires**
> (value "float32"); `mamba_utils.py:91-94` then takes the `!= "auto"` branch ->
> `temporal = STR_DTYPE_TO_TORCH_DTYPE["float32"] = torch.float32` (conv stays bf16);
> `qwen_gdn_linear_attn.py:1101` `_, state_dtype = self.get_state_dtype()` takes the **temporal** (2nd)
> tuple element and allocates the cache (`:1136`) at f32; `ssm_state = self_kv_cache[1]` (`:1316/1596/1664`).
> The two bf16 sub-agent readings are **refuted** - they stopped at the `cache.py` default "auto" and
> never traced the `__post_init__` override. **Numeric corroboration:** at the measured vLLM duration
> 3.62 ms/call, bf16 (402 MB) would imply 111 GB/s = 41% peak (implausibly low for a tuned BW-bound
> Triton kernel); f32 (805 MB) implies 222 GB/s = 81% peak (the expected regime). f32 is the only
> reading consistent with both source *and* the measured time.

## Headline (two answers)

1. **Bit-exact-vs-vLLM (identical logits / probabilities) is IMPOSSIBLE - for this model and for any
   two distinct engines.** B4 = CONFIRMED. The sharpest proof is the GDN recurrence itself: the two
   kernels evaluate an *algebraically reassociated* expression (`g.Sigma` vs `Sigma.g`) on *different
   reduction trees*, so they diverge **even if both ran pure f32 with identical inputs**. On top of
   that the FP4 GEMM uses different operand precision (8-bit vs 4/16-bit activations) and different
   accumulation - a >>ULP divergence in every projection and the LM head.

2. **bf16 SSM state is NOT the only way to reach vLLM decode throughput, and an f32-preserving lever
   was missed.** vLLM reaches its throughput **with an f32 GDN state** (proven). Both engines move the
   same ~805 MB f32/recurrence-call; the ~10% per-call gap is a bandwidth-**efficiency** gap on equal
   bytes (llama ~74% of peak, vLLM ~81%), i.e. an occupancy/grid/coalescing lever that is **bit-exact
   vs llama's own f32**. bf16 state is an *optional over-clock* (goes AHEAD of vLLM on the recurrence),
   not a parity requirement. B2/B3 (as "bf16 width is the lever") = REFUTED.

---

# The five questions, answered (synthesis)

**Q1. Can llama be BIT-EXACT with vLLM? NO.** Two *binding* (>>ULP) divergence sources make
bit-identical logits impossible on their own: **(A1)** the FP4 GEMM - llama MMQ quantizes the
activation to **q8_1 (8-bit)** while vLLM runs cutlass **w4a4 (4-bit acts)** or marlin **w4a16
(16-bit acts)**; different operand precision + accumulation order -> ~1e-2 relative error in *every*
projection and the LM head; **(A2)** the GDN recurrence - llama computes `g*(Sigma round(S*k))`
(scalar decay *after* the reduction) while vLLM computes `Sigma round(round(g*h)*k)` (decay rounded
into each element *before* the reduction): an IEEE-754 reassociation on *different reduction trees*
(warp butterfly vs Triton `tl.sum`) that diverges **even with identical pure-f32 state and inputs**.
A dozen further ops (L2/RMSNorm, MRoPE, gate `exp`, flash-attn softmax) add close-but-not-equal
rounding. Cross-engine bit-exactness is impossible *in general* (FP non-associativity across distinct
GEMM/recurrence/norm kernel stacks); the determinism literature only buys run-to-run determinism
*within* one engine. **Weaker form reachable:** greedy **top-1 token agreement** is the right gate
(top-1 / KL / PPL-delta, never md5). It is probabilistic (flips at low-margin steps), **compounds**
with length (once one token differs the SSM/KV states fork), and is *weaker here* than a
same-precision run because of the A8-vs-A4 GEMM gap.

**Q2. Is bf16 SSM state the only path to vLLM decode throughput? NO - an f32-preserving lever exists
and bf16 is not even required for parity.** vLLM carries the **same f32 temporal state** (proven +
re-verified), so the recurrence gap is **bandwidth EFFICIENCY on equal f32 bytes** (llama 74% vs vLLM
81% of GB10 peak), ~10% per call, *not* a 2x width gap. The lever: **retune `gated_delta_net_cuda`
74% -> ~81%** - it launches 196608 tiny one-column blocks (butterfly-reduce per token); fold toward
fewer/larger `BV x BK` tiles + vectorized `f32x4` loads + better row coalescing, **keeping the
per-column reduction order -> BIT-EXACT vs llama's own f32** (md5-gateable). **Cost vs bf16:** zero
precision risk and bit-exact, but it can only **match** vLLM's recurrence BW (81%), never beat it;
worth ~+5% (~335 -> ~351 tok/s, ~90% of vLLM), and it caps below 100% unless stacked with the other
bit-exact levers (conv fusion 0021, activation fold, oproj MMQ 0020). The adversarial sweep of every
other f32 avenue (lossless sub-f32, delta/low-rank/sparse, recompute+checkpoint, 2nd-stream/overlap,
chunked recurrence) **FAILS** to beat it; recompute is bit-exact but only **ties** the irreducible
one-full-state-READ floor and is now moot (vLLM also writes f32, so you match its achieved BW, you
don't need to eliminate the write). bf16 remains the **only** lever that goes *ahead* of vLLM on the
recurrence (~440 tok/s) - an **over-clock**, not a requirement.

**Q3. Does bf16 state MATCH vLLM's precision or DEGRADE below it? It DEGRADES below vLLM.** (This
corrects the `precision-ground-truth` sub-agent's "matching, not degrading" claim, which rested on
the refuted bf16 reading.) vLLM keeps the **temporal/recurrent** state in **f32**; only its small
**conv** state is bf16 (llama keeps conv f32, so llama is *more* precise there). So bf16 **temporal**
state in llama (~8 mantissa bits) sits **below vLLM's f32 temporal** (~24 bits) - it is a deliberate
precision-for-speed trade, KL/PPL-gated vs llama's own f32 *and* a step under vLLM's recurrent-state
precision. A genuine "match vLLM's envelope" change would be f32 temporal (as today) + bf16 conv -
which costs llama precision only on a tiny stream and buys almost no BW.

**Q4. What can "parity" mean here? Throughput at equal precision + a distributional quality bar -
never identical bits.** Bit-identical logits are impossible cross-engine, so "parity" = **(a)**
throughput (tok/s in the harness) at **(b)** a quality bar measured by **top-1 greedy agreement,
KL(llama||vLLM)/step, and PPL-delta**, never md5. Both engines already run the recurrence math in f32
registers; at **equal** precision (llama f32 temporal == vLLM f32 temporal) the *only* open variable
is throughput, and that gap is closable **bit-exactly** (Q2). If llama adopts bf16 temporal, "parity"
must be restated as "throughput >= vLLM at KL/PPL within gate vs llama's own f32" and reported as the
precision-for-speed trade it is.

**Q5. Did the prior analysis get B1-B4 right? B1 mostly; B2/B3 REFUTED; B4 CONFIRMED. Overturn the
"bf16 is required" framing - keep the bit-exact levers.**
- **B1 TRUE** (single-pass f32, load-once/store-once, 74% peak) - but its sub-claim "more efficient
  than vLLM (41%)" is **REFUTED** (41% was the bf16 artifact; vLLM is ~81%, *more* efficient).
- **B2 REFUTED** - not a f32-vs-bf16 width gap; equal f32 bytes both sides, ~10% efficiency gap.
- **B3 REFUTED** as written - vLLM reaches its throughput **with f32 state**; a bit-exact f32
  occupancy retune reaches vLLM's recurrence BW. bf16 is optional.
- **B4 CONFIRMED** - impossible, on two independent grounds (structural A1+A2; general FP
  non-associativity across distinct kernel stacks).
- **Plan disposition:** do **not** overturn the conv-fusion (0021) bit-exact lever - keep it.
  **Re-prioritize the bit-exact f32 occupancy/coalescing retune of `gated_delta_net_cuda` as the
  parity path.** Treat bf16 temporal state as an explicitly-gated **over-clock for going beyond
  vLLM**, reported as a precision-for-speed trade (below vLLM's f32 recurrent precision), NOT as a
  parity-matching change.

---

# PART A - Divergence inventory (per source: bit-identical vs close)

Per decode layer the two engines run *different kernels* for: FP4 GEMMs (proj + LM head), depthwise
conv+SiLU, q/k L2-norm, the GDN recurrence, gated RMSNorm; and on the hybrid's full-attention layers:
RMSNorm q/k-norm, MRoPE, flash attention, a sigmoid gate.

## A1. NVFP4 dequant + FP4 GEMM -- NOT bit-identical (diverges >> ULP)

- **llama**: MMQ (`mmq.cuh` `block_fp4_mmq`, nvfp4 block=16, 4x ue4m3 sub-scales). Host path
  (`ggml-cuda.cu` ~1955-2014) **quantizes the activation (src1) to q8_1** (`block_q8_1_mmq`, **8-bit**,
  block 32) and accumulates over K in the MMQ tile (DP4A / Blackwell FP4-MMA); tile order set by
  `mmq_y`/`mmq_x` + the warp-MMA fragment layout.
- **vLLM**: `compressed_tensors_w4a4_nvfp4` -> cutlass FP4 GEMM on Blackwell (**4-bit** activations,
  w4a4, per-group act-quant, e4m3 block scale x global FP8 tensor scale) or marlin fp4 fallback
  (**16-bit** activations, w4a16, dequant->bf16 then bf16 GEMM). `apply_weights` -> `self.kernel`.
- **Verdict: not close.** (a) *Operand precision differs*: llama 8-bit acts vs vLLM 4-bit (cutlass) or
  16-bit (marlin) - per-GEMM outputs differ at ~1e-2 relative, not ULP. (b) Scale-application order
  differs. (c) Accumulation tiling/order differs (MMQ fragment vs cutlass/marlin). This is the largest
  divergence and is present in every projection + the LM head, so logits differ materially on its own.

## A2. gated-DeltaNet recurrence -- NOT bit-identical, AND provably so even in pure f32

Both single-pass over an **f32** state (Part B). llama: `gated_delta_net.cu`
`gated_delta_net_cuda<128,KDA=false>`; vLLM: `fused_recurrent.py`
`fused_recurrent_gated_delta_rule_packed_decode_kernel`. Scalar-gate (GDA) path, `g.ne0==1`.
With S[k][v] (llama, transposed) == h[v][k] (vLLM):

```
llama:  kv[v] = Sigma_k S_old[k][v]*k[k]      # OLD state; g applied AFTER the sum
        delta = (v[v] - g*kv[v])*beta;  S_new = g*S_old + k(x)delta;  o[v]=Sigma_k S_new[k][v]*q[k]
vLLM:   h' = g*h_old                          # decay rounded into EVERY element first
        kv[v]=Sigma_k h'[v][k]*k[k]=Sigma_k round(g*h_old)*k;  b_v=(v[v]-kv[v])*beta
        h_new = h' + b_v(x)k;  o[v]=Sigma_k h_new[v][k]*q[k]
```

Algebraically identical (g scalar). **Numerically not**, for two structural reasons that survive even
with identical f32 state, identical inputs, and identical reduction tree:
- **Reassociation:** llama forms `g*(Sigma round(S*k))` (scalar multiply *after* the reduction);
  vLLM forms `Sigma round(round(g*h)*k)` (decay rounded into each element *before* the reduction).
  Distributing a multiply across a sum is exact in R, not in IEEE-754. This is not a precision knob.
- **Different reduction trees:** llama `warp_reduce_sum<32>` (4 sequential per-lane FMAs + 5-step
  butterfly) vs vLLM `tl.sum(...,1)` (Triton tree over the 128-wide BK axis).
**Verdict: not bit-identical; cannot be made so without rewriting one kernel to the other's op order.**

## A3. Depthwise conv1d (width 4) + SiLU -- NOT bit-identical
llama `ggml_ssm_conv` (ascending-j f32 FMA) + `ggml_silu`, conv state cached **f32**. vLLM
`causal_conv1d_update` (Triton) + SiLU, conv state cached **bf16** (`conv_state_dtype = bf16`; only the
*temporal* SSM state is forced f32 - Part B). Different kernel + different conv-state width + FMA order.
(Patch 0021 fuses llama's chain bit-exactly vs *llama's own* f32 path, not vs vLLM.)

## A4. q/k L2-norm + RMSNorm/RMSNormGated -- NOT bit-identical (close, ~1e-6)
L2-norm: llama standalone `ggml_l2_norm` (f32 tree) vs vLLM `l2norm_fwd`/in-kernel fold
(`USE_QK_L2NORM_IN_KERNEL`). RMSNorm: llama `ggml_rms_norm` vs vLLM `vllm_c` fused kernel (run log:
`rms_norm=['vllm_c','native']`); gated out-norm `build_norm_gated`=RMS*SiLU(z) vs `RMSNormGated`.
Different variance reduction tree / eps placement / fusion boundary.

## A5. MRoPE + gate scalar pipeline -- NOT bit-identical (close)
MRoPE: `ggml_rope_multi` (ggml sin/cos) vs vLLM rotary cos/sin cache (different theta eval + apply
order). Gate: vLLM computes `-exp(A_log)*softplus(a+dt)` then `exp` **in-kernel**; llama computes
`softplus(alpha+ssm_dt)*ssm_a` as split graph ops with `ssm_a` baking `-exp(A_log)` at GGUF-convert
time (rounded once), writes/reloads the intermediate, `expf` in-kernel. Same algebra, different
rounding points + convert-time vs runtime `exp(A_log)`.

## A6. Flash attention (full-attn layers) -- NOT bit-identical (close)
llama `ggml_flash_attn_ext` -> `fattn-mma-f16`/`fattn-vec` (online softmax, F16/F32 PV accum per
`GGML_PREC`) vs vLLM FlashInfer/FA2. Different tiling => different running max/sum order => different
rounding.

## A7. SiLU/sigmoid primitives + fusion -- equivalent IF inputs matched (they never do)
Both ultimately use the same hardware `expf`/`__nv_expf`; the primitives could match given identical
inputs, but every upstream value has diverged, and vLLM fuses act+quant / norm+quant differently than
llama's separate ops (run log `fuse_act_quant=True`), moving the rounding points.

### Inventory summary

| Source | bit-identical? | divergence size |
|---|---|---|
| FP4 GEMM (proj/LM head): MMQ q8_1(A8) vs cutlass w4a4(A4)/marlin w4a16 | **NO** | **>>ULP (~1e-2)** |
| GDN recurrence: hand-CUDA warp-reduce vs Triton tl.sum | **NO (provable even in f32)** | reassoc + tree |
| conv1d+SiLU: f32 conv-state vs bf16 conv-state | NO | dtype + order |
| L2-norm / RMSNorm | NO | ~1e-6 (tree) |
| MRoPE | NO | ~ULP-1e-6 |
| gate softplus/exp | NO | rounding points |
| flash attention | NO | softmax tiling |
| silu/sigmoid primitive | identical IFF inputs equal | inputs never equal |

Any single NO makes the logits differ. A1 and A2 differ by far more than ULP -> the logit vectors are
not close-to-equal at the bit level; they agree only to a few significant digits.

---

# PART B - The decisive f32-state correction (proof from source)

The byte-gate inferred vLLM's GDN temporal state is **bf16** (402 MB/call, 41% peak) and built the
"bf16-width is the lever" case on it. The byte count was *inferred from the dtype*; ncu byte counters
were blocked, so only the **duration** (3.62 ms/call) was measured. The dtype inference is falsified:

1. `config.json`: `architectures=["Qwen3_5ForConditionalGeneration"]`, `text_config.dtype=bfloat16`,
   and **`text_config.mamba_ssm_dtype = "float32"`**.
2. `models/config.py:590 MODELS_CONFIG_MAP` maps `"Qwen3_5ForConditionalGeneration"` (line 622) and
   `"Qwen3_5MoeForConditionalGeneration"` (623) to `Qwen3_5ForConditionalGenerationConfig`.
3. `Qwen3_5ForConditionalGenerationConfig.verify_and_update_config` (config.py:536-562):
   `mamba_ssm_dtype = getattr(hf_text_config,"mamba_ssm_dtype")` (="float32"); if
   `cache_config.mamba_ssm_cache_dtype == "auto"` (the default) it executes
   **`cache_config.mamba_ssm_cache_dtype = mamba_ssm_dtype`** -> sets it to **"float32"**.
4. This override runs at config finalization: `config/vllm.py:856` -> `try_verify_and_update_config()`
   (vllm.py:1880-1900) looks up the arch in `MODELS_CONFIG_MAP` and calls `verify_and_update_config`.
   It runs **before** any layer/model state-dtype resolution.
5. The bench left it default: `h2h_dense_vllm.sh` = `vllm serve .../q36-27b-nvfp4-vllm --enforce-eager
   --gpu-memory-utilization 0.85 --max-model-len 4096` (no `--mamba-ssm-cache-dtype`; `dl-logs/vllm_dense.log`
   non-default args confirm none). So the override fires and the value is "float32".
6. State dtype resolution reads the **already-overridden** value:
   - `gdn/base.py:53-57` `get_state_dtype()` -> `gated_delta_net_state_dtype(model_dtype=bf16,
     cache_config.mamba_cache_dtype="auto", cache_config.mamba_ssm_cache_dtype="float32")`.
   - `qwen3_5.py:678 get_mamba_state_dtype_from_config` likewise passes
     `vllm_config.cache_config.mamba_ssm_cache_dtype` (= "float32", post-override) - **not** a raw "auto".
   - `mamba_utils.py _mamba_state_dtype`: conv_state = `get_kv_cache_torch_dtype("auto", bf16)` = **bf16**;
     temporal_state, since `mamba_ssm_cache_dtype != "auto"`, = `STR_DTYPE_TO_TORCH_DTYPE["float32"]`
     = **torch.float32** (key verified: `torch_utils.py:33 "float32": torch.float32`).
7. `qwen_gdn_linear_attn.py:1101` `_, state_dtype = self.get_state_dtype()` takes the **second** tuple
   element (temporal) = **float32**, allocates the cache `dtype=state_dtype`. The packed_decode kernel
   round-trips f32: `b_h = tl.load(p_h0).to(f32)` ... `tl.store(p_ht, b_h.to(p_ht.dtype.element_ty))`
   with `p_ht.dtype == initial_state.dtype == float32`.

**=> vLLM's gated-DeltaNet temporal (recurrent) state cache for this model is float32, identical width
to llama's f32 state.** The earlier "bf16" reading hardcoded the third arg as `"auto"` and missed the
override at step 3-4. Only the small *conv* state is bf16 in vLLM (f32 in llama: divergence A3, tiny
byte stream).

## Re-derived efficiency table (measured duration + PROVEN f32 byte volume)

| kernel | state dtype (PROVEN) | bytes R+W/call | duration/call | eff. BW | % of 273 peak |
|---|---|---|---|---|---|
| llama `gated_delta_net_cuda` | f32 | 805 MB | 3.98 ms | 202 GB/s | **74%** |
| vLLM `..._packed_decode` | **f32 (not bf16)** | **805 MB (not 402)** | 3.62 ms | **222 GB/s** | **~81%** |

- **B1 (single-pass f32 byte floor): TRUE** (load-once/store-once `s_shard`, coalesced). *Sub-claim
  "more BW-efficient than vLLM (41%)" REFUTED* - 41% was the bf16 artifact; at the correct f32 byte
  count vLLM is at ~81%, i.e. **more** efficient than llama.
- **B2 ("the gap is f32-vs-bf16 width"): REFUTED.** Equal f32 bytes both sides; the ~10% per-call gap
  is bandwidth **efficiency** on equal bytes, not width.
- **B3 ("vLLM throughput REQUIRES bf16 state"): REFUTED.** vLLM reaches it *with f32 state*.

---

# PART C - The f32-preserving lever, and where recompute/bf16 land

Since vLLM hits ~81% on the **same f32 byte volume** llama runs at ~74%, the missed lever is **raising
llama's `gated_delta_net_cuda` achieved BW 74% -> ~81%**, bit-exact, NOT dtype width:
- llama grid `(H=48, n_seqs=128, ceil(S_v/4)=32) = 196608` blocks/128 thr, each warp owns ONE state
  column + warp-reduces over 128 rows. vLLM grid `(NV=4, B*HV=6144) = 24576` programs (num_warps=1),
  each owns a BV=32 x BK=128 tile. llama's far-finer blocking (8x more blocks, one column of work each,
  a butterfly reduce/token) is the likely ~7-point deficit. Retune toward fewer/larger blocks (more
  columns/block, vectorized f32x4 loads, better row coalescing) - changes thread/tile mapping + load
  width only, **keeps the per-column reduction order -> bit-exact vs llama's own f32**.
- Upper bound: 74%->81% on ~50% of the step ~= +17 ms/step (384 -> ~367), ~+5% -> ~351 tok/s (~90% of
  vLLM 391), stacking with the landed bit-exact levers (oproj MMQ 0020 @86%, conv fusion 0021).

**Other f32-preserving avenues (adversarial sweep) - none beats the simple bf16 over-clock, but the
occupancy tune above is the real bit-exact win:**
- *Lossless sub-f32 state:* generic float compression is data-dependent (1.1-1.5x, never a guaranteed
  2x) and breaks the 128-consecutive-f32 coalescing a BW-bound kernel depends on. The state is dense,
  full-rank, non-symmetric (sum of `k(x)delta`, k!=delta) -> no low-rank/half-storage. FAILS.
- *Recompute (checkpoint every N + rank-1 replay):* eliminates the per-step WRITE; the per-step full
  dense f32 READ (the `S^T k` / `S^T q` matvecs need every element; the checkpoint is itself a full
  read) is irreducible. Optimal N~=11 -> ~473 MB/step (0.587x), realistically ~0.65-0.75x after
  replay/latency overhead. A genuine bit-exact path but it only reaches - never beats - the read floor,
  at large kernel/graph complexity. **Note: this was over-weighted before because vLLM was assumed
  bf16; now that vLLM is f32 too and runs at 81%, you do NOT need to cut the write to match vLLM - you
  need to match vLLM's achieved BW on the same f32 bytes.** Recompute is dominated.
- *2nd stream / overlap / pipelining:* DRAM BW (273) is one shared resource; the whole decode step is
  uniformly BW-bound (state traffic + ~13.5 GB/step dense NVFP4 weight traffic both hit 273), so
  overlapping two BW-bound phases sums to ~0. FAILS.
- *Equivalent recurrence with less decode traffic:* chunked gated-delta-rule is a prefill lever (C=1 at
  decode); attention/materialization-free form is O(t) over the prefix. FAILS.

**bf16 SSM state is therefore an OPTIONAL over-clock**, the only lever that goes *ahead* of vLLM on the
recurrence (halve 805 -> ~440 tok/s) - but it takes llama below both its own f32 and vLLM's f32
precision, so it must be **KL/PPL-gated vs llama's own f32**, never md5. f32-only parity-class
throughput is plausible from the SUM of bit-exact levers (recurrence occupancy + conv fusion + oproj
MMQ + activation fold); none require bf16.

---

# PART D - Verdict on B4 + the meaningful weaker form

## Bit-exact-vs-vLLM: IMPOSSIBLE (B4 CONFIRMED) - two independent grounds

1. **Structural (this model):** A1 (FP4 GEMM operand precision + accumulation) and A2 (recurrence
   `g.Sigma` vs `Sigma.g` + different reduction trees) make per-layer outputs differ by >>ULP, so logits
   cannot be bit-identical. A2 shows it is not a precision knob: the kernels evaluate a *reassociated
   expression*, differing **even given identical f32 state and inputs**.
2. **General (any two engines):** IEEE-754 add/mul are non-associative; two engines that tile, reduce,
   fuse, and quantize differently cannot produce bit-identical results for a non-trivial transformer.
   Field determinism work (batch-invariant / fixed-reduction kernels, "defeating nondeterminism in LLM
   inference") delivers **run-to-run determinism WITHIN one engine**; it does **not** and cannot deliver
   **cross-engine** bit-exactness (that needs identical kernel+tiling+reduction-order+dtype for *every*
   op). Cross-engine bit-exactness is essentially never achieved in practice. Bit-exactness is only a
   meaningful gate **within** an engine (how llama patches 0018-0021 are validated by md5).

## Greedy-token match (argmax robustness) - the right weaker form, but probabilistic
Because logits differ mostly in low-order bits (A4-A7) plus a few-significant-digit GEMM/recurrence gap
(A1-A2), the **argmax** frequently coincides whenever the top-1/top-2 logit margin exceeds the
cross-engine noise. This is the only meaningful cross-engine "equivalence"; gate on **top-1 agreement /
KL / PPL-delta**, never md5. Caveats: not guaranteed per-token (low-margin steps can flip); it
**compounds** - once one greedy token differs the sequences fork and the KV/SSM states diverge, so
agreement degrades with length (high on short continuations, drift on long ones); and the FP4 A4-vs-A8
gap (A1) makes the per-step divergence *larger* here than a same-precision bf16-vs-bf16 comparison,
weakening greedy agreement for this model specifically.

**Bottom line:** target near-vLLM via KL/PPL/top-1-agreement, not bit-exactness. Reserve bit-exact
gating for intra-llama validation (the f32 recurrence-occupancy lever and the conv fusion qualify;
bf16 state does not and must be KL/PPL-gated vs llama's own f32).

Assisted-by: Claude:opus-4.8 [Claude Code]
