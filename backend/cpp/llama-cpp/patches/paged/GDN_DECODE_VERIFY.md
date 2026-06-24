# GDN decode verify: is llama.cpp's Gated-Delta-Net decode O(1) or an O(ctx) re-scan?

Verdict-first, then the evidence. This closes lever 5 of `VLLM_DECODE_GROUNDING.md` ("Verify
llama's GDN/linear-attention decode path"): on the Qwen3.6 hybrid models, is llama re-scanning the
context (O(ctx)) in the linear-attention layers, or keeping vLLM's O(1)-in-context recurrent state?

Method: GGUF-metadata + source reading on the `paged` dev tree (`~/llama-paged-dev`, build-cuda
sm_121) on `dgx.casa`, plus nsys CUDA-kernel decode traces on `~/bench/q36-27b-nvfp4.gguf`
(GB10 / DGX Spark, `GGML_CUDA_DISABLE_GRAPHS=1`, paged KV, `-fa on`). Models:
`~/bench/q36-27b-nvfp4.gguf` (dense, arch `qwen35`), `~/bench/q36-35b-a3b-nvfp4.gguf`
(MoE, arch `qwen35moe`).

## TL;DR verdict

**llama.cpp's GDN decode is EFFICIENT: it is O(1)-in-context, a single fused CUDA kernel that
reads + updates a fixed-size cached recurrent state, structurally identical to vLLM's
`fused_recurrent_gated_delta_rule`. It is NOT a re-scan, NOT a context-scaling blowup, and NOT a
major contributor to the ~2.4x eager-decode gap.** There is no GDN-specific bottleneck to fix, so
the cheap model-specific lever this probe was hunting for does not exist. The 2.4x is the general
kernel work (the FP4 weight GEMM, which dominates the step, plus the O(ctx) full-attention decode
kernel in the minority of full-attention layers), exactly as `VLLM_DECODE_GROUNDING.md` concluded.

The decisive datum: at matched batch (npl4), pure decode, 4x more context, the GDN kernel time is
**flat** while the full-attention kernel grows ~3.1x:

| kernel | ctx 1024 | ctx 4096 | ratio | meaning |
|--------|---------:|---------:|------:|---------|
| `gated_delta_net_cuda` (GDN linear-attn) | 10.3 us/launch | 8.0 us/launch | **~1.0x (flat)** | **O(1) in ctx** |
| `flash_attn_tile` (full-attn layers) | 27.1 us/launch | 85.0 us/launch | **3.1x** | O(ctx), as expected |
| total ms / decode step | 84.9 | 86.0 | 1.01x | GEMM-bound, ctx-independent |

Identical decode-step counts in both windows (~190 steps, ~9134 GDN launches), so this is a
per-step like-for-like comparison: the GDN layers do **not** get more expensive as context grows.

## 1. Architecture (confirmed from GGUF metadata + tensor names)

Both Qwen3.6 models are hybrid: a `full_attention_interval` of 4 means every 4th layer is standard
full attention and the other 3/4 are Gated-Delta-Net (GDN) linear attention with a recurrent state.

**Dense Qwen3.6-27B (`general.architecture = qwen35`):**
- `block_count = 64`, `full_attention_interval = 4` -> **16 full-attention layers + 48 GDN layers**.
- Full-attn: `head_count = 24`, `head_count_kv = 4` (GQA), `key_length = value_length = 256`,
  rope `freq_base = 1e7`, mrope sections `[11,11,10,0]`.
- GDN/SSM: `ssm.state_size = 128`, `ssm.conv_kernel = 4`, `ssm.group_count = 16`,
  `ssm.time_step_rank = 48`, `ssm.inner_size = 6144`. So the recurrent state per GDN layer is
  `[S_v=128, S_v=128, H_v=48]` per sequence (`H_v = inner_size/state_size = 6144/128 = 48` value
  heads), i.e. a 128x128 state matrix per head, ~3.1 MB (F32) per sequence per layer.

**MoE Qwen3.6-35B-A3B (`general.architecture = qwen35moe`):**
- `block_count = 41`, `full_attention_interval = 4` (~10 full-attn + ~31 GDN layers).
- `head_count = 16`, `head_count_kv = 2`, `key_length = value_length = 256`,
  `expert_count = 256`, `expert_used_count = 8`, `expert_feed_forward_length = 512`.
- Same SSM dims: `state_size = 128`, `conv_kernel = 4`, `group_count = 16`,
  `inner_size = 4096` -> `H_v = 32` value heads.

**Tensor names confirm the op split (27B, per-layer dump):**
- GDN layers (e.g. `blk.0.*`): `ssm_alpha`, `ssm_beta`, `ssm_conv1d`, `ssm_a`, `ssm_dt.bias`,
  `ssm_norm`, `ssm_out`, plus `attn_qkv` / `attn_gate` (the in/out projections of the linear-attn
  block). No `attn_k/v/output`, no per-head q/k norm.
- Full-attn layers (e.g. `blk.3.*`, every 4th): `attn_q`, `attn_k`, `attn_v`, `attn_output`,
  `attn_q_norm`, `attn_k_norm`. No `ssm_*`.

llama loads the GDN layers through the **recurrent memory** (`llama-memory-recurrent`), not the KV
cache: the conv state and the SSM state live in `conv_states_all` / `ssm_states_all` and are read
and written every step. Only the 16/10 full-attention layers use the (paged) KV cache. This is the
SSM-style recurrent path, not standard attention.

## 2. llama.cpp GDN decode implementation: O(1) recurrent-state update (code-proven)

Graph build (shared by both models): `src/models/delta-net-base.cpp`, dispatched from
`src/models/qwen35.cpp` and `src/models/qwen35moe.cpp` (the MoE class inherits
`llm_build_delta_net_base` and calls the same `build_recurrent_attn`, qwen35moe.cpp:472).

**Decode dispatch (`build_delta_net`, delta-net-base.cpp:425-447):** when `n_seq_tokens == 1`
(decode), it takes `build_delta_net_fused` if `cparams.fused_gdn_ar` (the default, see below), else
`build_delta_net_autoregressive`. Both are O(1):

- `build_delta_net_autoregressive` (delta-net-base.cpp:289-371) is the explicit rank-1 recurrence on
  the fixed-size state `s` shaped `[S_v, S_v, H_v, n_seqs]`: `s *= exp(g)` (decay),
  `sk = sum_rows(s * k)`, `d = (v - sk^T) * beta`, `s += k (x) d^T` (rank-1 update),
  `o = sum_rows(s * q)`. **No loop over past tokens, no KV read** - it touches only the state and
  the single new token's q/k/v/g/beta. `GGML_ASSERT(n_tokens == 1)`.
- `build_delta_net_fused` (delta-net-base.cpp:373-423) collapses the same recurrence into one op,
  `ggml_gated_delta_net(q, k, v, g, b, s, K=1)`.

**State is cached across steps, not rebuilt (`build_recurrent_attn`, delta-net-base.cpp:527-606):**
the input state `s` is read from `ssm_states_all` via `build_rs`, and the new state is copied back
with `ggml_cpy(new_state, view(ssm_states_all, ... kv_head ...))` (lines 555-558). The causal-conv
state is handled the same way in `build_conv_state` (449-525): the previous `conv_kernel-1 = 3`
samples are read from `conv_states_all`, the new token is appended, and the last 3 are written back.
So both pieces of GDN state persist in the recurrent cache exactly like a KV cache persists tokens -
this is the recurrent analogue, fixed size, independent of context length.

**Defaults (`src/llama-context.cpp:200-201`):** `cparams.fused_gdn_ar = true` and
`fused_gdn_ch = true`. They are only auto-disabled if the fused op cannot be scheduled on the same
device as the layer (`device_gdn != device_kv`, lines 540-595); on a single GB10 with `-ngl 99`
that does not happen, so the **fused single-kernel path is what runs**.

**The CUDA kernel (`ggml/src/ggml-cuda/gated_delta_net.cu`) is the crux, and it is unambiguously
O(1) in context:**
- Launch grid `dim3(H, n_seqs, ceil(S_v/4))` and block `(min(warp,S_v), 4, 1)` (lines 184-185):
  the grid spans heads x sequences x state-columns. **There is no context-length dimension and no
  context-length argument anywhere in the kernel signature** (q/k/v/g/beta are the new token(s)
  `[S_v, H, n_tokens, n_seqs]`; `curr_state` is the fixed `[S_v, S_v, H, n_seqs]`).
- Each warp loads its shard of the fixed-size state into registers **once** (lines 57-61), then
  loops `for (t = 0; t < n_tokens; t++)` (line 63). At decode `n_tokens == 1`, so it is a single
  iteration: read the one new token, do the rank-1 update
  `s_shard[r] = g * s_shard[r] + k[i] * delta_col` and the readout `attn = S^T q` (lines 84-141),
  then write the updated state back (lines 161-167). No second loop, no read of any past KV.
- Work per decode step is therefore proportional to `S_v * S_v * H * n_seqs` (the state size x
  batch) and **constant in context length**. This is precisely vLLM's
  `fused_recurrent_gated_delta_rule_packed_decode_kernel` (one batched launch updating a
  fixed-size `[K,V]` state) cited in the grounding doc.

A chunked GPU kernel for prefill is a TODO (delta-net-base.cpp:181 `//TODO: Add chunked kernel`);
the chunked CPU/graph path (`build_delta_net_chunking`) only runs for multi-token ubatches
(prefill), never at decode.

## 3. nsys decode profiling: GDN is a small share and does not scale with context

Qwen3.6-27B NVFP4, sm_121, `GGML_CUDA_DISABLE_GRAPHS=1`, paged KV, `-fa on`, `llama-server` driven
to steady decode by a looping completion client. Kernel time bucketed by name (full classifier and
sqlites under `~/bench/gdn_study/`).

**(a) Share at the headline batch (npl128, ctx 1024), GPU 92.7% busy:**

| bucket | % of busy | us/launch |
|--------|----------:|----------:|
| GEMM_weight (`mul_mat_q`/`mul_mat_vec_q`) | 59.2 | - |
| **GDN_recurrent (`gated_delta_net_cuda`)** | **8.9** | 369 |
| GEMM_act_quant (`quantize_mmq_nvfp4`) | 8.2 | - |
| elementwise / act_glu / norm / rope | ~13.5 | - |
| embed_gather (`get_rows`) | 2.9 | - |
| **ATTENTION_full (`flash_attn`, 16 layers)** | **1.8** | 107 |
| copy_cast (`cpy`) | 1.8 | - |
| **GDN_conv (`ssm_conv`)** | **1.5** | - |

The whole GDN path (recurrent 8.9% + conv 1.5%) is ~10% of the step; full attention is ~2%; the
**weight GEMM dominates at ~67% (59.2% GEMM + 8.2% act-quant requant)**. This is the dense model,
where the grounding predicted the GEMM would be the lever.

**(b) Share at low batch (npl32, ctx 1024), weight-bandwidth (GEMV) regime, GPU ~100%:**
GEMM_weight 88.7%, GDN_recurrent 0.8%, ATTENTION_full 0.7%, GDN_conv 0.3%. At low batch the
weight-read GEMV swamps everything and GDN is negligible; the GDN share tracks the batch, not the
context.

**(c) Context-scaling control (the decisive test): matched batch npl4, pure decode, ctx 1024 vs
4096.** Small batch -> fast prefill -> a clean pure-decode capture (verified: GEMM is the M=1
`mul_mat_vec_q` decode GEMV, and the client completed decode rounds inside the window). Identical
decode-step counts (~190 steps, gated_delta_net launched 9141 vs 9134 times), so per-launch time is
a true per-step comparison:

| kernel / bucket | ctx 1024 | ctx 4096 | ratio |
|-----------------|---------:|---------:|------:|
| `gated_delta_net_cuda` us/launch | 10.3 | **8.0** | **0.78x (flat)** |
| GDN_recurrent share | 0.6% | 0.4% | flat/down |
| `ssm_conv` (GDN_conv) us/launch | 5.2 | 5.2 | 1.00x |
| `flash_attn_tile` us/launch | 27.1 | **85.0** | **3.14x** |
| ATTENTION_full share | 0.6% | 1.8% | 3.0x up |
| total ms / decode step | 84.9 | 86.0 | 1.01x |

The GDN kernel time is flat (even a hair faster) across a 4x context increase, while the
full-attention kernel grows ~3x, exactly the O(1)-vs-O(ctx) signature. The total step time barely
moves because at this batch the (context-independent) FP4 weight GEMM is 88% of the step. This is
the empirical confirmation of the code analysis: **llama's GDN decode does not re-scan the context.**

(An earlier npl32 ctx4096 attempt was discarded: with 32 parallel slots each independently
prefilling ~4100 tokens, the nsys window caught prefill, not steady decode - the `mul_mat_q(M=128)`
+ `flash_attn_ext_f16(ctx4096)` signature gave it away. The npl4 runs above avoid this by keeping
prefill short.)

## 4. Verdict and fix scope

**Efficient, not a bottleneck.** llama.cpp runs the Qwen3.6 GDN/linear-attention layers as a fused,
single-CUDA-kernel, O(1)-in-context recurrent-state update, with the conv and SSM state cached in
the recurrent memory across decode steps. It is algorithmically the same as vLLM's O(1)
`fused_recurrent` decode. The probe's worst case (llama re-scanning context => GDN layers ballooning
with context and concurrency) is **falsified**: the GDN kernel is flat across 4x context, and the
op carries no context-length parameter at all.

**So the GDN path is not the cheap model-specific lever.** It is a small-to-moderate, context-flat
share of the step (~0.4-0.8% at low batch, ~10% including conv at batch 128), and removing it would
not dent the 2.4x. The gap is the general kernel work, confirming `VLLM_DECODE_GROUNDING.md`:
1. the **FP4 weight GEMM** is the dominant bucket (~59% GEMM + ~8% `quantize_mmq_nvfp4` requant that
   vLLM fuses away via native FP4-MMA / grouped Marlin); this is the biggest, hardest lever.
2. the **full-attention decode kernel** is the O(ctx) residual (the only thing that grows with
   context, ~3x per-launch over 4x ctx), in the minority of full-attention layers.

If anything on the GDN side is ever worth touching, it is a bounded micro-optimization, not a
complexity fix: the kernel is memory-bound on the F32 recurrent state (state read+write is
`S_v^2 * H * batch` = ~0.79 GB/step over 273 GB/s at batch 128, hence the ~8.9% share), and this
traffic is **intrinsic to the architecture - vLLM pays the identical state I/O**, so it is not a
llama-specific inefficiency. A future win could keep the recurrent state in bf16 or fuse the
`ssm_conv` + gated-norm into the delta-net kernel to shave that ~10%, but the ceiling is small and
it does not close the 2.4x. The throughput effort stays where the grounding put it: the FP4 GEMM
(fused act-quant + native FP4-MMA) and the full-attention decode kernel, with a CUDA-graphed
steady-state step as the bounded host-side add-on.

## Reproduce

- Metadata: `python3 gguf-py/gguf/scripts/gguf_dump.py --no-tensors ~/bench/q36-27b-nvfp4.gguf`.
- Code: `src/models/delta-net-base.cpp` (build_delta_net 425, autoregressive 289, fused 373,
  build_recurrent_attn 527, build_conv_state 449); `src/llama-context.cpp:200-201,540-595`
  (fused_gdn defaults/guard); `ggml/src/ggml-cuda/gated_delta_net.cu` (kernel 4-168, launch grid
  184-185, dispatch 226-312).
- Profiles: `~/bench/gdn_study/drv.sh <label> <P> <K> <ctx> <delay> <dur>` runs `llama-server` under
  nsys and drives `clientloop.py`; `catgdn.py <sqlite>` buckets kernels. Sqlites:
  `gdn_npl128_ctx1024`, `gdn_npl32_ctx1024`, `gdn_npl4_ctx1024`, `gdn_npl4_ctx4096`.
