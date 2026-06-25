# Critical-Path Gap Analysis - GDN decode region

## vllm-gdn-compare (READ-ONLY, no GPU) - vLLM decode GDN kernel inventory vs llama

### Source ground truth
- Local checkout `/home/mudler/_git/vllm` and the DGX's benchmarked venv
  `/home/mudler/vllm-bench/lib/python3.12/site-packages/vllm` are STRUCTURALLY
  IDENTICAL (same file `qwen_gdn_linear_attn.py`, byte-for-byte same line numbers
  1287/1344/1457/1644/1684). So the analysis below is faithful to what was actually
  benchmarked on the GB10. Both are a recent dev build (`__version__ = "dev"`), same
  era as the "0.23.0" reference; the GDN path is the refactored
  `vllm/model_executor/layers/mamba/gdn/qwen_gdn_linear_attn.py`.

### The headline: vLLM runs the entire GDN region at decode as 2 Triton kernels + 3 GEMMs, ALL fused
Per Qwen3.5 gated-DeltaNet (linear-attn) layer, vLLM decode launches:

| # | Kernel | What is folded in |
|---|--------|-------------------|
| 1 | `in_proj_qkvz` GEMM | (quantized matmul - shared with llama) |
| 2 | `in_proj_ba` GEMM | (quantized matmul - shared with llama) |
| 3 | `_causal_conv1d_update_kernel` (causal_conv1d.py:1193) | conv1d **+ silu activation fused in** (the `activation` arg) |
| 4 | `fused_recurrent_gated_delta_rule_packed_decode_kernel` (fused_recurrent.py:256-336) | **l2norm(q), l2norm(k), scale, softplus gate, A_log decay exp(g), sigmoid(beta), the delta-rule recurrence (b_h*=exp(g); delta update), the output b_o=sum(b_h*b_q), AND the SSM state write-back** - all in one kernel |
| 5 | `RMSNormGated` (gated rms_norm) | **output gate silu/sigmoid * z fused into the rms_norm**; the comment notes the norm+quant is further fusable by the compilation pass (`fuse_norm_quant`) |
| 6 | `out_proj` GEMM | (quantized matmul - shared with llama) |

So the GDN-region "glue" elementwise op count in vLLM is effectively ZERO separate
launches. Everything llama runs as standalone ggml nodes - conv-silu, gate
sigmoid, softplus, l2norm, scale, decay mul, residual add, gather - is absorbed
into kernels #3, #4, and #5.

Verified kernel bodies:
- `fused_recurrent_gated_delta_rule_packed_decode_kernel` lines 313-336:
  `b_q/sqrt(sum(b_q^2)+eps)`, `b_k/sqrt(...)` (l2norm), `b_q*scale`,
  `softplus_x=where(x<=thr, log(1+exp(x)), x)`, `g_val=-exp(A_log)*softplus_x`,
  `beta_val=sigmoid(b)`, `b_h*=exp(g_val)`, `b_v-=sum(b_h*b_k)`, `b_v*=beta_val`,
  `b_h+=b_v*b_k`, `b_o=sum(b_h*b_q)`, `tl.store(p_o,...)`, `tl.store(p_ht,...)`.
  ONE kernel = recurrence + ALL gating + l2norm + state writeback.
- The non-packed variant `fused_sigmoid_gating_delta_rule_update_kernel`
  (fused_sigmoid_gating.py:24-179) is the same fusion (used for the spec-decode /
  mixed-batch path); both fold gate+l2norm+recurrence+writeback into one launch.
- Decode dispatch: `_forward_core` (line 1286-1298) routes pure non-spec decode to
  `_forward_core_decode_non_spec` (line 1644), which calls exactly
  `causal_conv1d_update` (#3) then `fused_recurrent_gated_delta_rule_packed_decode`
  (#4). `_output_projection` (line 851) does `self.norm(core_attn_out, z)` (#5,
  gated rmsnorm) then `out_proj` (#6).

### vLLM ALSO captures decode in a FULL CUDA graph - the launch bubbles are gone entirely
`vllm/v1/attention/backends/gdn_attn.py`:
- `_cudagraph_support = AttentionCGSupport.UNIFORM_BATCH` (line 82)
- `use_full_cuda_graph = cudagraph_mode.has_full_cudagraphs()` (line 113)
- `build_for_cudagraph_capture` (line 509): "only decode is supported for full
  cudagraphs with Mamba" / "GDN only supports decode-only full CUDAGraph capture".

So at decode vLLM captures the WHOLE forward (all 48 layers: GDN linear-attn layers
+ the 1-in-4 full-attn layers + projections + conv + recurrence + gated rmsnorm)
into a single replayed CUDA graph. Per-kernel host launch latency and the
data-dependent inter-op gaps are eliminated at replay time. Even the 2 Triton
kernels per GDN layer incur no host-side launch bubble during graph replay.

### Why this is the 62%-vs-40% explanation (not GEMM throughput)
- llama runs the GDN region as ~7-9 separate ggml nodes per layer at decode
  (`ssm_conv`, `gated_delta_net` recurrence, `gdn_gather`, `k_bin_bcast` mul,
  `silu`, `sigmoid`, `l2_norm`, `op_add`, `concat`), each a host-launched kernel,
  serially data-dependent (conv -> gate -> recurrence -> gather), with the gating
  elementwise wedged between recurrence steps. Each launch + the dependency stall
  is a bubble ON the critical path. x48 layers x ~8 ops = ~384 launch bubbles/step.
- vLLM has 2 fused Triton kernels per GDN layer AND wraps them in a CUDA graph, so
  the GDN-region inter-op bubble count at decode is ~0. The recurrence kernel
  itself is already near-parity in llama (gated_delta_net 1.47 ms/call vs vLLM).
  The gap is the surrounding launch/sync overhead, which is exactly the 60% idle
  measured (llama ~40% busy vs vLLM 62%).
- This matches why P2a and Lever 2 were FLAT: they shrink GPU-busy kernels that are
  already overlapped with the 42% mul_mat_q GEMM. The real wall-clock lever is the
  SERIAL GDN gating chain's launch bubbles, which vLLM removed by (a) fusion into
  the recurrence kernel and (b) CUDA-graph capture.

### What llama would need to match vLLM (two independent wins, either helps)
1. **Op fusion (Lever 3).** Collapse the GDN per-layer gating chain into the
   recurrence kernel: fold conv-silu, q/k l2norm, scale, softplus+A_log gate,
   sigmoid-beta, the exp-decay mul, the residual add, and the SSM-state write-back
   INTO the `gated_delta_net` CUDA kernel (and fuse the output gate silu*z into the
   final rms_norm). Target: from ~8 GDN nodes/layer down to ~2 (conv-fused +
   recurrence-fused), mirroring vLLM's `fused_recurrent_gated_delta_rule_packed_decode`.
   The conv silu fold and the l2norm/scale/gate fold are the high-value pieces -
   they are pure elementwise prologues sitting ON the serial chain between conv and
   recurrence.
2. **CUDA-graph the decode step.** Even without fusion, capturing the decode forward
   in a CUDA graph removes the per-node host launch latency for all ~384 nodes/step.
   (Prior A.2 work flagged ggml-cuda graph capture as the orthogonal lever; the
   measured GDN structure here is exactly why it should move the wall.) vLLM gets
   BOTH; llama gets neither today.

### Bottom line for the gap-analysis agent
The candidate explanation is confirmed at the source level: vLLM's GDN decode region
is 2 fused Triton kernels under a full CUDA graph vs llama's ~8 separate
host-launched, serially-dependent ggml nodes. That launch/bubble delta - not GEMM
compute - is the 62%-vs-40% busy gap. A timeline gap analysis on the existing nsys
trace should show idle GPU between the GDN sub-ops (conv -> gate -> recurrence ->
gather) per layer; if it does, Lever 3 (gating-into-recurrence fusion) and/or
decode CUDA-graph capture are the levers that will move the wall, unlike P2a/Lever 2.

---

## roofline-decode (READ-ONLY, no GPU) - decode-step roofline + bubble budget + parity target

Goal: bound the q36-27b-nvfp4 decode step by the bandwidth floor and the compute floor,
compare to measured llama 384 ms/step vs vLLM 327 ms/step, size the unexplained "bubble
budget", and state the step-time target for parity. Cross-checks vllm-gdn-compare above.

### Inputs (measured / GGUF metadata, no new GPU work)
- DGX GB10 (sm_121): LPDDR5x **273 GB/s**, dense NVFP4 MMA peak ~**500 TFLOP/s** (sparse ~1 PFLOP/s).
  Both numbers are shared identically by llama and vLLM (same HW, same weights).
- q36-27b-nvfp4 GGUF (arch qwen35): block_count **64** (full_attention_interval 4 ->
  **16 full-attn + 48 GDN** layers), d_model 5120, FFN 17408, attn 24 heads / 4 kv-heads,
  head_dim 256, ssm conv_kernel 4 / state_size 128 / group_count 16 / inner_size 6144.
  Weight file = **18.804 GB** (NVFP4 + FP8 block-scales + f16 norms/embd), fully GPU-resident.
- Measured llama decode (dense_base.out, -fa -npp128 -ntg128 -npl128, B=128, 128 TG steps):
  T_TG 49.154 s / 128 = **384.0 ms/step** (S_TG 333.3 tok/s; matches STATE "~381 ms").
- vLLM dense ref **391 tok/s @128** => 128/391 = **327.4 ms/step**.

### 1. Bandwidth floor (bytes that MUST cross LPDDR5x per step / 273 GB/s)
| term | bytes/step | basis |
|------|-----------|-------|
| Weights (batched, read ONCE/step, reused across all 128 seqs) | ~18.4 GB | file 18.804 minus ~0.4 GB sparse input-embd lookup; lm_head fully read |
| SSM state R+W (48 GDN layers x 128 seqs) | ~19 GB (bracket 10-38) | ~1.5 MB/layer/seq R+W, kernel-grounded: gated_delta_net 1.47 ms/call -> ~400 MB/call @273 GB/s; theoretical d_inner*d_state f32 doubles it |
| KV cache read (16 attn layers, avg 192 ctx, 128 seqs, f16) | ~1.6 GB | 64 KiB/tok over 16 layers; max-ctx 256 -> 2.1 GB |
| Activation/quantize/gate intermediates R+W | ~3 GB | quantize_mmq_nvfp4 + k_bin_bcast + silu + rms tensors @ batch 128 |
| **TOTAL** | **~42 GB/step** | bracket 32-61 GB |

**Bandwidth floor = 42/273 = ~154 ms/step** (central; bracket ~117-224 ms).
Weight-only HARD sub-floor (unavoidable, both engines pay it): 18.4/273 = **67 ms/step**.

KEY: even at batch 128 the FP4 GEMM is STILL memory-bound, not MMA-bound. AI = 2*128/0.53 B
= ~483 FLOP/byte << GB10 ridge 500e12/273e9 = 1832 FLOP/byte. The 42% `mul_mat_q<NVFP4,m=128>`
GPU time is weight-DRAM streaming, not tensor cores -> first-principles reason P2a (-26% MMA
occupancy) and Lever-2 were FLAT on decode.

### 2. Compute floor (FLOPs / ~500 TFLOP/s dense FP4)
| term | FLOPs/step | floor |
|------|-----------|-------|
| FP4 GEMM (all dense matmuls): 2 * ~26e9 params * 128 seqs | 6.66 TFLOP | / 500e12 = **13.3 ms** (6.7 ms @ sparse 1 PFLOP) |
| GDN recurrence (state update + read-out, 48 layers x 128 seqs) | ~0.04 TFLOP | < 0.1 ms (state-bound, not FLOP-bound) |
| **TOTAL** | ~6.7 TFLOP | **~13 ms/step (~4% of the step)** |

### 3. Verdict / bubble budget / parity target
```
                    compute floor   bandwidth floor    MEASURED step   x above bw-floor
GB10 dense-FP4      ~13 ms          ~154 ms (117-224)
vLLM dense @128                                        327 ms          ~2.1x (1.5-2.8x)
llama dense @128                                       384 ms          ~2.5x (1.7-3.3x)
```
- **Binding floor = bandwidth (~130-155 ms), NOT compute (~13 ms).** Compute floor is ~25x
  below the wall -> FP4-MMA throughput is irrelevant; matches P2a/Lever-2 flatness exactly.
- **Both engines run ~2-2.8x ABOVE the bandwidth floor.** vLLM itself reaches only ~40-47%
  LPDDR5x efficiency -> even the reference is LATENCY/occupancy bound, not byte-bound.
  Confirms prior "decode is 2.5x above its bandwidth floor" work.
- **Bubble budget** (wall - bandwidth floor, central 154): vLLM ~**173 ms**, llama ~**230 ms**.
  = kernel-launch latency + occupancy gaps + serial data-dependency stalls.
- **The llama-vs-vLLM gap = 384 - 327 = 57 ms/step (14.8% of the step) is 100% BUBBLE.**
  Both engines share IDENTICAL bandwidth AND compute floors (same 18.8 GB NVFP4 weights, same
  SSM state, same KV, same GB10 273 GB/s + 500 TFLOP). Bytes and FLOPs are byte-for-byte equal,
  so the entire 57 ms differential lives in critical-path bubble - NOT bandwidth, NOT compute.

**Parity target: 327 ms/step (391 tok/s @128). llama must shave 57 ms/step = 14.8% off 384 ms.**
Neither floor can move (both already shared with vLLM), so the 57 ms can ONLY come from
collapsing critical-path bubbles -> structurally-correct case for Lever 3 (fuse the serial GDN
gating chain) and/or decode CUDA-graph capture, exactly the two wins vllm-gdn-compare found vLLM
already has. P2a/Lever-2 were flat because they freed OVERLAPPED GPU-busy time BELOW the floor.

### Cross-check / sizing for the gap-analysis (timeline) agent
- GPU-busy sum from nsysab_new (ntg24 window, /24 steps): FP4 GEMM ~243 + gated_delta_net ~76 +
  GDN glue (k_bin_bcast mul ~49, silu ~34, concat ~19, gdn_gather ~21, ssm_conv ~12, l2_norm ~6,
  op_add ~10) ~152 + quantize ~62 = **~555 ms GPU-busy vs 384 ms wall** -> sum >> wall by ~1.45x,
  so heavy overlap is real and GPU-busy% buckets ARE misleading. Do NOT sum kernel times; the
  wall is the critical path.
- Concrete budget: if the inter-kernel IDLE gaps + non-overlapped launch latency along the serial
  GDN chain (ssm_conv -> gated_delta_net -> gating elementwise -> gdn_gather, x48 layers x N steps)
  sum to **>= 57 ms/step**, Lever 3 is justified AND sized. If those critical-path gaps total
  < 57 ms, parity is NOT reachable via GDN-gate fusion alone and the gap is elsewhere (GDN core
  kernel slower than vLLM fused_recurrent, or scheduler/H2D).
- Structural corroboration (agrees with vllm-gdn-compare): vLLM runs the GDN region as 2 fused
  Triton kernels under a full CUDA graph; llama splits it into ssm_conv + gated_delta_net +
  gdn_gather + ~6 serially data-dependent elementwise gate kernels. ~384 host-launched nodes/step
  on a chain that cannot overlap is precisely the mechanism that produces llama's extra ~57 ms.

Floors are engine-independent lower bounds; the timeline agent owns proving the 57 ms is
recoverable on the critical path. Roofline says: target 327 ms, shave 57 ms, and it can ONLY
come from bubble (not bytes, not FLOPs).

Assisted-by: Claude:opus-4.8 [Claude Code]

## lever3-design (READ-ONLY, no GPU) - concrete fusion of the serial GDN gate chain into the recurrence kernel

### What actually feeds/consumes the recurrence kernel today (qwen35 decode, fused_gdn_ar)
Traced in `src/models/qwen35.cpp::build_layer_attn_linear` ->
`src/models/delta-net-base.cpp::build_recurrent_attn` (fused !keep branch) ->
`ggml/src/ggml-cuda/gated_delta_net.cu`. The model is GDA (g->ne[0]==1, scalar
gate per head; kda=false in the kernel). S_v = ssm_d_state = 128, so the kernel
runs the `<128>` template: warp_size==S_v==128, num_warps=4, rows_per_lane==1,
grid (H, n_seqs, S_v/4=32 z-tiles). Each warp owns one output column `col`; the
128 lanes hold the full head-vector (one element per lane).

Serial pre-GDN gate chain (each a standalone host-launched ggml node, all on the
critical path between the in-proj GEMMs and the recurrence):
1. `beta = ggml_sigmoid(ssm_beta @ cur)`            -> kernel reads `beta_val = *beta_t`
2. `alpha = ssm_alpha @ cur`
3. `ggml_add(alpha, ssm_dt)`  (k_bin_bcast op_add)
4. `ggml_softplus(...)`        (unary_op<softplus>, 1248 inst)
5. `ggml_mul(softplus, ssm_a)` (k_bin_bcast op_mul; ssm_a = -exp(A_log), baked)  -> g; kernel does `expf(g_t)`
6. `ssm_conv` then `ggml_silu` (conv path; may already hit the upstream SSM_CONV+SILU fuse) -> v_conv, and the q/k slices
7. `ggml_l2_norm(q_conv)`, `ggml_l2_norm(k_conv)` (l2_norm_f32<32>, 2496 inst = 1248x2) -> kernel reads q_reg/k_reg

Post-GDN gate (consumes kernel output):
8. `build_norm_gated(output, ssm_norm, z)` = rms_norm(output)*ssm_norm (RMS_NORM+MUL fused) then `silu(z)*.` (unary_gated_op<silu>, the 5.9% bucket)

### The fusion: fold steps 1,3,4,5,7 INTO gated_delta_net_cuda (a "fused-gate" mode)
These five are exactly the per-(head) scalar gates (sigmoid beta; softplus+dt+ssm_a
-> g) and the per-head-vector L2 norms of q/k - and the kernel ALREADY loads every
operand it needs:
- It reads `beta_val` (scalar) -> pass RAW beta, do `beta_val = 1.f/(1.f+expf(-raw))` in-kernel. Removes node 1.
- It reads `g_t` (scalar, GDA) and does `expf(g_t)` -> pass RAW alpha + per-head `ssm_dt[h]` + per-head `ssm_a[h]`, compute `g = ssm_a[h]*op_softplus(alpha + ssm_dt[h])` in-kernel, keep the existing `expf(g)`. `op_softplus(x) = (x>20)?x:logf(1+expf(x))` (copy `ggml_compute_softplus_f32` verbatim). Removes nodes 3,4,5.
- It loads the full q/k head-vector into `q_reg[r]`/`k_reg[r]` (one element per lane at S_v==128). L2-normalize in registers: `float qss = warp_reduce_sum<128>(q_reg[0]*q_reg[0]); q_reg[0] *= rsqrtf(qss + eps* ... )` matching the l2_norm formula, same for k. Each warp redundantly recomputes the (identical) norm for its column - cheap, no shared mem, no extra launch. Removes nodes 7 (x2). `eps` (= f_norm_rms_eps) passed as a kernel float param.

That collapses the pre-GDN serial chain to just: in-proj GEMMs -> build_conv_state(concat) -> ssm_conv(+silu) -> [single fused gated_delta_net kernel]. 5 gate kernels removed per SSM layer per decode step.

### Why the OUTPUT gate (step 8) is NOT folded into this kernel
The output gated-rmsnorm reduces over the full head_v_dim (S_v=128) per (head,seq).
In this kernel those 128 elements are produced by 128 DIFFERENT (warp x z-tile)
blocks (4 warps x 32 z-tiles), so an in-kernel head-wide reduction would need a
grid-global sync - not feasible without a grid redesign. Leave step 8 as the
existing RMS_NORM+MUL + unary_gated<silu> fusion (already 2 launches, not in scope).
The conv-silu (step 6) is a convolution, structurally separate; rely on the
existing upstream SSM_CONV(+ADD)+SILU fuse rather than pulling it into the
recurrence kernel.

### Implementation scope
- `ggml/include/ggml.h`: new builder `ggml_gated_delta_net_inplace_ids_fused_gate(ctx, q_raw, k_raw, v, alpha_raw, beta_raw, cache4d, state_dst, ids, ssm_a, ssm_dt, rs_head, eps)` (or an op-param flag GDN_FUSE_GATE on the existing builder + 2 extra srcs). src budget: current op uses src[0..7]; add ssm_a -> src[8], ssm_dt -> src[9]. GGML_MAX_SRC==10, so it fits EXACTLY (zero headroom - note for review).
- `ggml/src/ggml.c`: builder + a new op-param i32 flag (e.g. params[2]=fuse_gate) + f32 param for eps; assert shapes (ssm_a/ssm_dt are [num_v_heads]).
- `ggml/src/ggml-cuda/gated_delta_net.cu`: in `gated_delta_net_cuda`, gate the in-kernel sigmoid/softplus-gate/l2norm behind a `bool FUSE_GATE` template param (4th template bool, keeps the non-fused path byte-identical and avoids register bloat when off). Read ssm_a[h_idx], ssm_dt[h_idx]; compute g per head; sigmoid raw beta; warp-reduce q_reg/k_reg sumsq -> rsqrtf scale. Plumb the 2 new src pointers + eps through `launch_gated_delta_net` and `ggml_cuda_op_gated_delta_net` (read src[8],src[9], op_param eps/flag). The `gdn_gather_nonident` path is unaffected (it gathers state, not q/k/g/beta).
- `ggml/src/ggml-cpu/ops.cpp`: mirror in `ggml_compute_forward_gated_delta_net_one_chunk` (host sigmoid/softplus/l2norm before the per-token math) for CPU parity / test-backend-ops.
- `src/models/delta-net-base.cpp::build_recurrent_attn` (the fused !keep + ids branch, and the inplace non-ids branch): call the fused-gate builder, pass raw alpha/beta/q/k + ssm_a + ssm_dt + eps.
- `src/models/qwen35.cpp` / `qwen35moe.cpp` / `qwen3next.cpp` `build_layer_attn_linear`: when the fuse flag is on, DROP `ggml_sigmoid(beta)`, `ggml_add(alpha,dt)`, `ggml_softplus`, `ggml_mul(.,ssm_a)`, and the two `ggml_l2_norm` nodes; hand the raw tensors + `model.layers[il].ssm_a`, `ssm_dt` to build_recurrent_attn. The conv-silu and z/output-gate path are unchanged.
- Guard the whole thing behind `cparams.fused_gdn_gate` / env `LLAMA_FUSE_GDN_GATE` (default OFF) so it A/Bs against the clean Lever-1 build exactly like P2a/Lever-2, and only the recurrent (GDA) qwen35 family path is touched.

### Numeric considerations / bit-exactness
- sigmoid(beta), softplus(alpha+dt), and the `g = ssm_a*softplus` mul/add are pointwise fp32 with the SAME formula/order as the standalone ggml ops -> these can be **bit-exact** (no reduction). softplus must copy `(x>20)?x:logf(1+expf(x))` exactly.
- q/k l2norm is the ONE op with a reduction: the standalone `l2_norm_f32<32>` does its own warp/block reduction; the in-kernel `warp_reduce_sum<128>` tree may differ in the last ULP, and the eps placement (`x*rsqrt(sumsq+eps)` vs `x/max(sqrt(sumsq),eps)`) must match the ggml l2_norm exactly. Expect **near-bit-exact, not guaranteed byte-identical** greedy output. So unlike Levers 1/2, gate this on a **PPL/KL tolerance** (KL logit delta < ~1e-3, PPL delta within noise) rather than md5 identity. If byte-identity is required, exclude l2norm from the fold (keep nodes 7) and fuse only sigmoid/softplus/gate - but that drops the value to ~0.3% and is probably not worth it.

### Estimated kernels-removed-per-layer and the honest ceiling
- Removed per SSM decode layer-step: sigmoid(beta) + add(dt) + softplus + mul(ssm_a) + l2norm(q) + l2norm(k) = **6 host-launched kernels -> 0**, collapsing 7 nodes (incl. recurrence) to 1. Across 48 SSM layers = **~288 launches/step removed** (matches the instance deltas: l2_norm 2496, softplus 1248, sigmoid 1248, plus the alpha-add/ssm_a-mul share of op_add/op_mul).
- GPU-BUSY ceiling of the removed ops is small: l2_norm 1.0% + softplus ~0% + sigmoid 0.3% + (dt add + ssm_a mul share of op_add 1.7% / op_mul 8.5%). The point of Lever 3 is NOT the freed busy-time (P2a/Lever-2 proved freeing overlapped busy-time is flat) - it is removing ~288 LAUNCH BUBBLES/step that sit on the serial conv->gate->recurrence dependency where the GPU is otherwise idle. The win is wall-clock only if those specific bubbles are on the critical path.

### RISK (must be settled before building)
1. **Same trap as P2a/Lever-2 if the bubbles overlap.** If the scheduler already
   overlaps these pre-GDN gate kernels with an adjacent layer's 42% mul_mat_q GEMM,
   Lever 3 is FLAT. **Precondition: the timeline gap analysis must show idle GPU
   between ssm_conv -> (sigmoid/softplus/l2norm) -> gated_delta_net per layer** at
   batch=128 BEFORE building. If the trace shows the gate ops back-to-back with no
   gap (overlapped), do NOT build op-fusion; go to lever (2) below.
2. **The bigger bubbles may be elsewhere on the chain.** The large buckets are op_mul
   8.5% and unary_gated<silu> 5.9% - much of which is the POST-GDN output gate and
   FFN, which this fusion does NOT touch. If the gap analysis pins the dominant idle
   to the post-GDN region or to inter-layer launch latency generally, the
   higher-leverage Lever 3 is **decode CUDA-graph capture** (removes host launch
   latency for ALL ~384 nodes/step at once, exactly what vLLM does), not per-op
   fusion. CUDA-graph is the strictly larger hammer here; op-fusion only helps the
   pre-GDN slice. Recommend measuring the per-sub-op gap first and preferring the
   CUDA-graph lever if the bubbles are spread across the step rather than concentrated
   in the pre-GDN gate slice.
3. **src-slot exhaustion** (src[8],src[9] use the last 2 of GGML_MAX_SRC=10) - any
   later op needing more srcs on this node has zero headroom; flag for review.

## cudagraph-coverage (READ-ONLY, no GPU) - does the CUDA graph cover the GDN serial chain at B=128?

### Verdict: YES, the graph covers GDN at batch=128 (dense model). No GDN op forces graph-disable or per-step re-instantiation.

Source: `ggml/src/ggml-cuda/ggml-cuda.cu` (graph state machine), `gated_delta_net.cu`
(fused op), `src/models/delta-net-base.cpp` (graph build), `src/llama-memory-recurrent.cpp`
(recurrent head), all on dev tree `~/llama-paged-dev` (HEAD df1cc97, Lever-1). Cross-checked
against the committed A2_CUDAGRAPH_DECODE.md + DECODE_PARITY_EXPLORE.md measurements.

### How graph-disable / re-instantiation are decided (this fork's state machine)
- `ggml_cuda_graph_check_compability` (ggml-cuda.cu:3251) disables the graph for ONLY two
  reasons: (a) a split-buffer src, (b) `GGML_OP_MUL_MAT_ID` with non-quantized weights OR
  `node->ne[2] > get_mmvq_mmid_max(...)` [TAG_MUL_MAT_ID_CUDA_GRAPHS]. GATED_DELTA_NET,
  SSM_CONV, SSM_SCAN, GET_ROWS, CONCAT, the gating elementwise ops are NOT in the disable
  list. So no GDN op forces graph-disable.
- `ggml_cuda_graph_update_required` (3297) memcmps, per node, the full `ggml_tensor` struct
  (incl. `op_params` and `data`) + each src's `data` ptr / `ne` / `nb`. ANY delta -> the
  warmup state machine (ggml_backend_cuda_graph_compute:4464) resets `warmup_complete` and the
  WHOLE graph (one key = `cgraph->nodes[0]`) runs eager that step until stable again. Buffer
  CONTENTS are NOT compared - a contents-only change (e.g. ids values) is graph-safe.

### Why the GDN region's properties are STABLE across steady decode steps
The fused decode path is `ggml_gated_delta_net_inplace_ids` (delta-net-base.cpp:558-560):
```
state_dst = ggml_view_2d(ctx, ssm_states_all, n_embd_s, n_seqs, nb1,
                         kv_head * n_embd_s * elsize);   // offset = kv_head
ggml_gated_delta_net_inplace_ids(..., cache4d, state_dst, ids, /*rs_head=*/(int)kv_head);
```
Both the `state_dst` view byte-offset and the `rs_head` op_param (read back as
`ggml_get_op_params_i32(dst,1)` in gated_delta_net.cu:330) derive from
`kv_head = mctx_cur->get_head()`. In `llama_memory_recurrent::find_slot`
(llama-memory-recurrent.cpp:610-689) the n_seqs used cells are SWAPPED into the contiguous
range `[min .. min+n_seqs-1]` and `head = min`. The recurrent cache does NOT grow per token
(one state cell per sequence, unlike the KV cache). For a steady 128-seq continuous batch the
same sequences own the same tails every step, so `min`/`head` are constant (=0) -> state_dst
offset constant, rs_head op_param constant. The GDN inputs (q,k,v,g,b, cache4d, ids) are
fixed-shape (n_seqs=128, n_rs slots), so ne/nb are stable, and ggml-alloc hands out the same
compute-buffer offsets each same-topology ubatch -> data ptrs stable. The `ids` (s_copy)
tensor's CONTENTS change per step but its address/ne/nb do not -> graph-safe.

### The fused GDN op is capture-safe (no host-sync, no capture-time cudaMalloc)
`gated_delta_net.cu`: the op launches `gdn_gather_nonident_kernel` + `gated_delta_net_cuda`
on `ctx.stream()` with NO `cudaStreamSynchronize` / host `cudaMemcpy` / `cudaMalloc`. The
gather scratch is `ggml_cuda_pool_alloc` (VMM pool, served from reserved memory after warmup,
no real cudaMalloc during capture). `gdn_gather_nonident` early-returns for identity sequences
(`ids[s]==rs_head+s`), which is the steady-decode case, so its 3.7% is a launched-but-mostly-
noop kernel - still captured into the graph like any other. Capture succeeds (the build runs,
graphs engage), confirming none of these break stream capture.

### The only re-instantiation is NOT GDN-driven
A2 already measured the re-warm cadence: the graph re-instantiates every ~256 tokens because
the FULL-ATTENTION block-table input `idx` has `ne[0]=GGML_PAD(n_kv,256)` (and kq_mask in
lockstep) - those step at 256-token boundaries (paged-attn.cpp:199-213). ~97% of decode steps
replay the captured graph. This is a full-attention-layer input, not a GDN op. (The unpadded
`LLAMA_KV_PAGED_GATHER` fallback grows `ne[0]` every step and runs pure-eager, but that is not
the default decode path and is not the GDN/SSM path.)

### Reconciliation with the "~40% util / 60% idle bubbles" premise (it is refuted for GDN)
The committed nsys sweeps (A2_CUDAGRAPH_DECODE.md, DECODE_PARITY_EXPLORE.md) show the steady
decode is ~99.4-99.5% GPU-BUSY with graphs ON (measured with `--cuda-graph-trace=node`; a
graphs-ON trace WITHOUT that flag under-counts GPU rows and falsely reports idle - Trap #2).
Total exposed idle is ~0.65% of the step; the within-step launch fraction graphs remove is
0.34% (0.37%->0.11%) and is ALREADY collapsed - the GDN sub-op launch gaps are inside the
captured region. The "40% utilization" in the STATE is BANDWIDTH-roofline util, not idle SMs:
decode moves ~55.5 GB/step at 2.48x the 273 GB/s floor, SSM state r+w = 66% of step bytes. The
GDN recurrence is memory-bandwidth-bound at low occupancy (~12-16%), not launch-gap-bound. So
"60% idle bubbles on the serial GDN chain" is not supported by the traces; the gap to vLLM is
SSM-state memory traffic, consistent with P2a/Lever-2 being flat (freeing GPU-busy time, not
wall-clock).

### Graph-safe lever for GDN: none new
- GDN is already graph-covered; there is no "make the GDN ops graph-safe" lever to build - they
  are already safe and captured.
- The only genuinely graph-NON-covered idle is the BETWEEN-step host gap (~2 ms/step, ~0.4%):
  ggml rebuilds/reallocs the cgraph each step with a new `cgraph->uid`, so the uid fast-path in
  ggml_cuda_graph_update_required never fires and the host re-dispatches ~3100 launches on the
  Grace cores between graph launches (vLLM builds its graph once + persistent device metadata).
  A persistent/reused cgraph across decode steps would let the uid fast-path fire and shrink the
  host gap - but at 0.4% of the step it is second-order to the SSM bandwidth floor.
- CAVEAT (MoE, qwen35moe): MUL_MAT_ID at B=128 can trip [TAG_MUL_MAT_ID_CUDA_GRAPHS]
  (`ne[2] > mmvq_mmid_max`), disabling the WHOLE MoE-decode graph (GDN included) into eager.
  That is a MUL_MAT_ID disable, not a GDN break, and does not touch the dense 335 tok/s headline;
  worth a separate confirm for the MoE model.

## decode-timeline-gap (GPU, label gap-analysis) - the decisive fresh node-level measurement

This is the new GPU run the analysis was waiting on. It arbitrates between the
roofline/vllm-gdn-compare theory ("57 ms = 100% bubble, Lever 3 closes it") and the
cudagraph-coverage source verdict ("~99.4% busy, bandwidth-bound, bubbles refuted").
The measurement confirms the latter and refutes the former, with per-kernel numbers.

### Capture (the trap the prior `--trace=cuda` fell into is now avoided)
`nsys profile --trace=cuda --cuda-graph-trace=node` on build-cuda-base (clean
Lever-1, HEAD df1cc97, git-clean mmq.cuh), q36-27b-nvfp4 dense, `-fa on -npp 128
-ntg 24 -npl 128 -c 33000`. Artifacts on DGX: `~/llama-paged-dev/nsysgap.{nsys-rep,
sqlite}`. The decode step is a single CUDA graph (graphId=11, 23 replays = steps
2-24; graphId=1 x8 = prefill). Plain `--trace=cuda` recorded each step as ONE opaque
~380 ms block, so the widely-cited `nsysab_new.kern.txt` breakdown (mul_mat_q 42%,
gated_delta_net 13%) is PREFILL + the single eager capture step, NOT decode. With
node-level trace the graph expands: 168201 kernels = 91499 graph-internal + 76702
eager prefill. **All graph kernels on stream 14 (single stream) -> strictly serial,
no overlap, so any inter-kernel gap is pure GPU idle.**

### One steady decode step (window between decode launches 22413.26 / 22796.74 ms, width 383.48 ms)
Exactly 48 `gated_delta_net` + 16 `flash_attn` = one clean step (48 GDN + 16 attn).
2965 kernels.

| classification | ms/step | % of step |
|---|---|---|
| (a) inter-kernel LAUNCH gaps + (b) SERIAL-DEPENDENCY stalls (LAG sum, single stream) | **0.225** | **0.06%** |
| (c) within-kernel time (GPU running) | 380.4 | 99.94% |

Zero gaps > 5 us. Largest single gap 2.40 us. 1260 sub-1us gaps + 1700 back-to-back.
**The decode step is 99.94% GPU-busy. There are no bubbles.** This independently
confirms cudagraph-coverage's ~99.4% and **refutes** roofline-decode's "57 ms = 100%
bubble" and vllm-gdn-compare's "~384 launch bubbles/step on the critical path".
nvidia-smi's "40% util" = low SM/compute efficiency WITHIN kernels (c) (memory-latency-
bound, ~12-16% achieved occupancy), not wall-clock idle.

### Real decode kernel mix (% of the 380.4 ms step) - corrects the prefill-contaminated kern_sum
| kernel | n/step | ms | % | grid CTAs | waves/48SM |
|---|---|---|---|---|---|
| gated_delta_net_cuda | 48 | **196.37** | **51.6** | 48x128x32 = 196608 | 4096 |
| mul_mat_q (FP4 in/out/qkv/o proj) | 496 | 92.90 | 24.4 | 136 | 1.5 |
| quantize_mmq_nvfp4 | 496 | 17.13 | 4.5 | 483 | 10 |
| nvjet GEMM (lm_head) | 1 | 11.91 | 3.1 | 1944 | 40 |
| flash_attn_ext_f16 (16 attn layers) | 16 | 11.67 | 3.1 | 48 | 1.0 |
| concat_cont (conv-state) | 48 | 8.01 | 2.1 | 20480 | 427 |
| cpy_scalar | 64 | 7.62 | 2.0 | 49152 | 1024 |
| k_get_rows_float | 49 | 7.08 | 1.9 | 15098 | 315 |
| k_bin_bcast (gate mul + add) | 720 | 6.59 | 1.7 | 3169 | 66 |
| ssm_conv_f32 | 48 | 5.64 | 1.5 | 10240 | 213 |
| unary_gated (silu/sigmoid) | 128 | 5.36 | 1.4 | 5888 | 123 |
| mul_mat_q_stream_k_fixup | 304 | 3.94 | 1.0 | 192 | 4 |
| rms_norm_f32 | 209 | 3.52 | 0.9 | 1764 | 37 |
| l2_norm_f32 | 96 | 0.64 | 0.2 | | |
| gdn_gather_nonident | 48 | **0.061** | 0.016 | | |

- `gated_delta_net` is **51.6% of the step**, the single dominant term. The
  previously-cited "1.47 ms/call near-vLLM" was the EAGER average over 1248 calls
  (range 0.046-4.42 ms = prefill warmups + capture); true steady decode is
  **4.08-4.11 ms/call** (gridY=128 = the 128 seqs). 2.8x higher than believed.
- It launches 196608 CTAs / 4096 waves = NOT occupancy-starved; the cost is
  bandwidth-bound state traffic (~384 MB read + ~384 MB write per layer for the
  48-head x 128-seq x [state 128 x head_v 128] recurrent state, ~190 GB/s effective).
- The Lever-3 narrow target (gating glue) = k_bin_bcast 6.59 + silu/sigmoid 5.36 +
  l2_norm 0.64 + softplus 0.13 = **12.76 ms = 3.35%** of the step. `gdn_gather` is
  **0.06 ms** (negligible - it early-returns on identity ids as predicted).

### The three answers (with numbers)
1. **Bubbles on the serial GDN critical path?** NO. 0.225 ms idle/step = 0.06%,
   zero gaps > 5 us. CUDA graphs eliminated launch overhead; serial dependencies do
   not produce idle (each kernel starts < 1 us after the previous). The premise is
   refuted by direct measurement.
2. **Would Lever 3 (fuse the gating chain) shrink the step or overlap away?** It
   shrinks it, but only by its hard ceiling **12.76 ms = 3.35%** (380 -> 367 ms, 336
   -> ~348 tok/s, 86% -> 89% of vLLM). It does NOT close the 14% / 53-57 ms gap.
   IMPORTANT mechanism correction: the step is single-stream and 99.94% busy, so
   there is NO overlap to absorb freed time (the lever3-design RISK #1 "same trap as
   P2a if overlapped" does NOT apply - nothing overlaps). So removing those kernels'
   GPU-time DOES cut wall-clock - but the win is removing their HBM byte traffic, NOT
   launch bubbles (there are none). And the value is the measured ~12.76 ms, not the
   "~288 launch bubbles" framing (those launches cost ~0 inside the graph). This also
   explains P2a/Lever-2 flatness correctly: NOT "overlapped busy-time" (no overlap),
   but P2a tuned the prefill large-M GEMM (decode GEMMs are 136-CTA tail-bound, untouched)
   and Lever-2 relocated mandatory quantize work into the GEMM prologue (net zero).
3. **Do CUDA graphs cover the GDN region at B=128?** YES, fully. Whole step = one
   graph, 23 replays, ~0.2 ms host gap between steps. `gdn_gather_nonident` and the
   in-place state ops are graph-internal nodes (graphNodeId != 0); no fragmentation.
   Confirms cudagraph-coverage. Note: lever #2 from vllm-gdn-compare ("CUDA-graph the
   decode step") is ALREADY IN EFFECT in this build and did not close the gap - so it
   is spent, not pending.

### Verdict against roofline-decode's own sizing test
roofline-decode stated: "if critical-path gaps total < 57 ms, parity is NOT reachable
via GDN-gate fusion alone and the gap is elsewhere (GDN core kernel slower than vLLM
fused_recurrent)." **Measured gaps = 0.225 ms << 57 ms.** Therefore, by that test, the
53-57 ms / 14% gap is NOT bubble and NOT closable by gating fusion. It lives in
**kernel GPU-time**, dominated by the `gated_delta_net` recurrence (51.6%, bandwidth-
bound) and secondarily the FP4 GEMM + quantize stack (29%). The "57 ms = 100% bubble"
roofline conclusion was an inference from the prefill-contaminated GPU-busy sum
(~555 ms vs 384 ms "implies overlap"); the node-level decode-only measurement shows
per-step GPU-busy = wall (no overlap), so that inference does not hold.

### Recommendation (resized)
- The real lever is the `gated_delta_net` recurrence kernel itself (196 ms, 51.6%):
  match vLLM's `fused_recurrent_gated_delta_rule_packed_decode` (vllm-gdn-compare
  kernel #4) which folds l2norm + gate + decay + recurrence + state-writeback into a
  SINGLE pass over the state, reducing HBM round-trips of the state. The win is byte
  reduction in a memory-bound single-stream step, not bubble removal.
- The lever3-design fusion is still worth doing as a component of that (it removes
  ~12.76 ms = 3.35% of real byte traffic, and unlike its own RISK section feared, it
  will NOT be flat because there is no overlap), but on its own it is a ~3% lever, not
  the gap-closer. Build it folded into a single-pass recurrence kernel, not as an
  isolated gate fold.
- Next decisive measurement (future GPU-agent run): profile vLLM's decode step at
  npl128 with the same node-level method and compare per-region GPU-time (GDN
  recurrence vs GEMM vs attention) to localize exactly where vLLM spends its 53-57 ms
  less. Both engines move near-identical bytes only if vLLM's fused recurrence does
  not re-stream state; the per-kernel A/B will show whether the gap is the recurrence
  pass or the GEMM/quantize stack.

Assisted-by: Claude:opus-4.8 [Claude Code]

---

## SYNTHESIS (final) - the validated decode-parity picture, ranked plan, and verdict

Reconciles all six investigation sections above plus the three adversarial verdicts
(Verify A/B/C). One sentence: **the "~60% idle" never existed; the decode step is
99.94% GPU-busy single-stream, so the 14% gap to vLLM is kernel GPU-time, dominated by
the bandwidth-bound `gated_delta_net` recurrence (51.6%), and the only gap-closing levers
are byte-reduction inside that kernel - NOT launch-bubble removal.**

### 1. The proven critical-path decomposition of the decode step

Decisive node-level trace (`nsys --cuda-graph-trace=node`, clean Lever-1 build df1cc97,
q36-27b-nvfp4 dense, npl128, GB10/48SM/sm_121, commit a7238525, nsysgap.sqlite). One
steady step = single replayed CUDA graph (graphId=11, 23 replays), all 2965 kernels on
ONE stream (stream 14, strictly serial -> every inter-kernel gap is pure idle). Window
383.48 ms.

BUBBLE CLASSIFICATION (the "where is the ~60% idle" answer - it is NOT idle):

| bucket | ms/step | % step | note |
|---|---|---|---|
| (a) inter-kernel launch bubbles | ~0 | ~0 | graph replay collapses host launch latency |
| (b) serial-dependency stalls (GDN chain) | included in 0.225 | 0.06 | each kernel starts < 1 us after prev; zero gaps > 5 us, max 2.40 us |
| (a)+(b) total exposed idle (LAG sum) | **0.225** | **0.06%** | 1700 kernels back-to-back |
| (d) between-step HOST gap (cgraph rebuild, new uid) | ~0.2 | ~0.05 | the ONLY graph-non-covered idle; ~0.4% in older eager-tail traces |
| (c) within-kernel GPU-busy | **380.4** | **99.94%** | this is the whole step |

The nvidia-smi "40%" is within-kernel SM/bandwidth efficiency (~12-16% achieved
occupancy on memory-latency-bound kernels), NOT wall-clock idle.

KERNEL GPU-TIME DECOMPOSITION of the 380.4 ms busy step (this is where the gap lives):

| kernel | ms | % step | regime |
|---|---|---|---|
| `gated_delta_net_cuda<128>` (48x, 4.08 ms/call) | **196.37** | **51.6** | bandwidth-bound f32 recurrent-state R+W (~384 MB R + 384 MB W/layer) |
| `mul_mat_q` FP4 GEMM (496x) | 92.90 | 24.4 | memory-bound weight stream, 136-CTA tail-bound at decode |
| `quantize_mmq_nvfp4` (496x) | 17.13 | 4.5 | mandatory act-quant (Lever-2 only relocated it) |
| `nvjet` lm_head GEMM | 11.91 | 3.1 | |
| `flash_attn_ext_f16` (16 attn layers) | 11.67 | 3.1 | |
| `concat_cont` (conv-state splice) | 8.01 | 2.1 | Lever-1 target |
| `cpy_scalar` (conv-state writeback + dup) | 7.62 | 2.0 | Lever-1 target (the conv-state share) |
| `k_get_rows_float` | 7.08 | 1.9 | |
| `k_bin_bcast` (gate mul + add) | 6.59 | 1.7 | Lever-3 gate-fold target (partial - rest is residual adds) |
| `ssm_conv_f32` | 5.64 | 1.5 | folds into Lever-1 |
| `unary_gated` (silu/sigmoid) | 5.36 | 1.4 | mostly FFN + output-gate (Lever 3 does NOT touch) |
| `mul_mat_q_stream_k_fixup` | 3.94 | 1.0 | |
| `rms_norm_f32` | 3.52 | 0.9 | |
| `l2_norm_f32` | 0.64 | 0.2 | Lever-3 gate-fold target |
| `gdn_gather_nonident` | 0.061 | 0.016 | negligible (early-returns on identity ids) |

GDN region (recurrence + conv + concat + cpy + gather + l2norm) >= 210 ms = 55%+ of the step.
The widely-cited "gated_delta_net 13%, 1.47 ms/call near-vLLM" from nsysab_new.kern.txt was
PREFILL + the single eager capture step contaminating the average over 1248 calls (range
0.046-4.42 ms); true steady decode is 4.08 ms/call, 2.8x higher, 51.6% of the step.

### 2. Claims A / B / C: which HOLD, which are REFUTED, and the residual uncertainty

**CLAIM A** ("the ~60% decode GPU-idle is inter-op launch bubbles ON the serial GDN
chain"): **REFUTED.** Measured idle = 0.225 ms = 0.06%, not the ~53-57 ms the claim
requires (two-plus orders of magnitude short). Zero gaps > 5 us; CUDA-graph replay
already collapsed launch latency; serial data-dependency does NOT equal idle when the
graph dispatches nodes back-to-back. The "40%" was a misread of within-kernel SM
efficiency; the "555 ms busy-sum > 384 ms wall implies overlap" was a prefill-contaminated
`--trace=cuda` artifact (each step recorded as one opaque ~380 ms block).

**CLAIM B** ("Lever 3 - gate fusion - moves the wall, unlike P2a/Lever-2, by removing
serial launch bubbles"): **REFUTED on mechanism.** (i) There are no bubbles to remove
(0.06%). (ii) The contrast is fictional: the step is single-stream with ZERO overlap
anywhere, so P2a/Lever-2 were NOT flat because they "optimized overlapped work" - P2a
tuned the prefill large-M GEMM (decode GEMMs are a different 136-CTA tail regime) and
Lever-2 merely relocated mandatory quantize work into the GEMM prologue (net zero).
(iii) Where the claim is trivially true (any kernel removal cuts wall in a 99.94%-busy
single-stream step), the slice Lever 3 actually fuses ceilings at **12.76 ms = 3.35%**
(k_bin_bcast 6.59 + silu/sigmoid 5.36 + l2_norm 0.64 + softplus 0.13 - and even that
over-counts, since silu is mostly untouched FFN/output-gate). So the wall DOES move, but
only ~3% (380 -> ~367 ms, 86% -> ~89% of vLLM), and NOT for the claimed reason. Lever 3
is a component, not the gap-closer.

**CLAIM C** ("the residual gap is software-closable LATENCY, not a GB10 hardware floor"):
**REFUTED as worded** (no latency, no idle to close - same data as A). The "not a hardware
floor" half is **UNSETTLED, not proven.** vLLM hits 327 ms on the same silicon, so it is
not an absolute hard floor - but whether the dominant 51.6% `gated_delta_net` term is
software-closable in BIT-EXACT form turns on one unmeasured quantity (below).

RESIDUAL UNCERTAINTY (the single open question that decides everything):
- **The DRAM byte-traffic ratio of llama's recurrence vs vLLM's.** Every section above
  ESTIMATED the GDN state bytes (~190 GB/s effective, ~70% of 273 GB/s peak); none MEASURED
  it. If llama's `gated_delta_net_cuda<128>` moves ~2x the minimal (s0-read + s1-write)
  bytes because the un-fused gate/l2norm/writeback/gather ops re-stream state through HBM,
  then the 51.6% is software-closable by a single-pass fused recurrence (Claim C spirit
  HOLDS). If llama already moves ~minimal bytes at > 85% of peak and vLLM moves the same,
  the recurrence is at the GB10 LPDDR5x floor for this state size -> the gap is a
  hardware/architecture floor and is NOT closable in bit-exact form (Claim C REFUTED on
  both halves). This is the one measurement that converts the verdict from "refuted as
  worded" to a definitive yes/no.
- **The MoE model (qwen35moe) is untested.** At B=128 MUL_MAT_ID can trip
  [TAG_MUL_MAT_ID_CUDA_GRAPHS] (`ne[2] > mmvq_mmid_max`) and disable the WHOLE MoE-decode
  graph into eager, where the ~3100 per-step launches re-dispatch serially on the Grace
  cores and inter-op bubbles WOULD reappear. For MoE only, Claim A could partially hold.
  The dense 335 tok/s headline is fully settled.

### 3. Ranked implementation plan for the remaining ~14% (57 ms/step, 384 -> 327)

Every win must come from kernel GPU-time (bytes), because bubbles = 0 and both engines
share identical bandwidth/compute floors. Ranked by expected recovery.

| # | Lever | ms/step recovered | -> % of vLLM | bit-exact | tractability | gate |
|---|---|---|---|---|---|---|
| **1** | **Single-pass fused GDN recurrence** (fold l2norm+gate+decay+recurrence+state-writeback+gather into ONE pass over state, mirroring vLLM `fused_recurrent_gated_delta_rule_packed_decode`) - cuts state HBM round-trips | **0 to ~40** (= the byte-delta; UNKNOWN until ncu) | 86% -> up to ~98% | near (l2norm reduction; KL < ~1e-3) | HIGH (kernel rewrite) | **ncu byte-ratio test FIRST** |
| 2 | **Conv-state concat -> ssm_conv fusion** (Lever 1): pass conv-state + new token as separate srcs, update conv state in place (vLLM `causal_conv1d_update`); removes concat_cont + the conv-state cpy | **~8-12** (concat 8.01 + cpy share of 7.62) | +2-3% | YES | MEDIUM | no-regret, build regardless |
| 3 | **Gate-chain fold** (Lever 3 as designed): sigmoid-beta + softplus+dt+ssm_a gate + q/k l2norm into the recurrence kernel | **~12.76 ceiling** (3.35%) - but SUBSUMED by #1 | +3% | near (l2norm) | MEDIUM | build as a COMPONENT of #1, not standalone |
| 4 | **bf16 recurrent + conv state** (Lever 5): halve the 196 ms recurrence + conv traffic; keep f32 in-register accumulation | **~70-90** (if floor-bound) | could reach/exceed parity | NO (parity-tolerance decision; must match vLLM stored dtype) | HIGH (rewrite + parity validation) | the ONLY lever that moves the floor kernel; separate precision track |
| 5 | gdn_gather skip-launch at steady decode | ~0.06 | ~0 | YES | trivial | not worth it (micro) |
| 6 | GDN occupancy split | 0 | 0 | - | - | NOT a lever: 196608 CTAs / 4096 waves, already saturated, bandwidth-bound |
| 7 | quantize_mmq attack (Lever 2) | 0 | 0 | - | - | SPENT - relocated mandatory work, proven flat |
| 8 | decode CUDA-graph capture | 0 | 0 | - | - | SPENT - ALREADY in effect (graphId=11), did not close gap |
| 9 | persistent cgraph (uid fast-path) | ~0.2 (0.05-0.4%) | ~0 | YES | MEDIUM | second-order to the SSM floor |

Levers 1, 3, and the gather of #5 are the SAME kernel rewrite: build them together as a
single-pass recurrence. Levers 6/7/8 are dead (at-floor or already-shipped). Lever 4 is a
distinct, bit-exactness-breaking precision track.

### 4. The honest verdict and the single highest-value next step

**Is true (bit-exact) decode parity reachable?** UNCERTAIN, and it hinges entirely on the
unmeasured byte ratio:
- If llama's recurrence re-streams state (~2x bytes from un-fused ops): YES - a single-pass
  fused recurrence (Lever 1) plus conv fusion (Lever 2) plausibly recover ~20-40 ms, taking
  llama to ~345-365 ms = ~90-95% of vLLM, near-bit-exact (gate on KL tolerance).
- If llama is already at the GB10 bandwidth floor for f32 state: NO in bit-exact form - the
  57 ms is a hardware floor, and only bf16 state (Lever 4, non-bit-exact) closes it.

Either way, the gating-fold-alone path tops out at ~89% of vLLM, so the project should NOT
ship the isolated gate fold as "the parity lever."

**SINGLE highest-value next IMPLEMENTATION step:** build the **single-pass fused GDN
recurrence kernel** (Lever 1 = fold gate + l2norm + state-writeback + gather into one pass
over the recurrent state) - BUT gate the build on one cheap measurement first, because it
is a HIGH-effort kernel rewrite that is worthless if the recurrence is already byte-minimal.

**The measurement that confirms it before over-investing (one short GPU run, gap-analysis
agent only):** `ncu` on `gated_delta_net_cuda<128>` at B=128 vs vLLM's
`fused_recurrent_gated_delta_rule_packed_decode_kernel` for identical layer dims, two
counters:
- `dram__bytes.sum` (actual DRAM bytes/call)
- `dram__throughput.avg.pct_of_peak_sustained_elapsed` (achieved % of 273 GB/s)

Decision rule:
- llama moves ~2x minimal bytes OR vLLM moves materially fewer for the same math -> redundant
  un-fused state round-trips -> BUILD the single-pass fused recurrence; predicted recovery
  scales with the byte delta (up to ~40 ms). This is the gap-closer.
- llama already moves ~minimal bytes at > 85% of peak and vLLM moves the same -> the
  recurrence is at the GB10 hardware floor -> do NOT build the fusion for throughput (only
  the ~3% gate-fold ceiling remains); the sole remaining lever is bf16 state (Lever 4,
  accept non-bit-exact), and bit-exact parity is NOT reachable.

**No-regret parallel work** (build regardless of the ncu outcome, bit-exact, medium effort):
the conv-state concat -> ssm_conv in-place fusion (Lever 2, ~8-12 ms = +2-3% toward parity),
which removes concat_cont (8.01 ms) and the conv-state writeback cpy off a bandwidth-bound,
single-stream step where their full GPU-time is wall-clock.

Assisted-by: Claude:opus-4.8 [Claude Code]
