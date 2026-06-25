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
