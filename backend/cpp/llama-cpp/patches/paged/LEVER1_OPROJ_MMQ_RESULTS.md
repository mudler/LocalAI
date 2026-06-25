# Lever 1: gated-DeltaNet output-projection MMQ reshape (patch 0020)

The single biggest decode-parity lever for the Qwen3.6 hybrid-SSM models
(arch qwen35: 48 gated-DeltaNet + 16 full-attention layers). A two-line,
bit-exact tensor reshape that re-routes the per-layer SSM output projection
from a batch-1 FP4 GEMV (MMVQ) to a batch-128 tensor-core GEMM (MMQ).

## The mechanism (profiled, both engines)

Post-SSM (patches 0018 + 0019) dense decode sat at 255 t/s = 65% of vLLM 391.
The largest llama-specific overage was the gated-DeltaNet OUTPUT projection
(ssm_out). The GDN op left its output in SSM layout and the graph reshaped it
to 3D `[value_dim, n_seq_tokens=1, n_seqs=128]` before the ssm_out matmul, so
`src1->ne[1] = 1`. That trips the ggml-cuda MMVQ dispatch (ne[1] <= 8) with the
128 sequences stuck in ne[2]; MMVQ is built for batch <= 8 and does NOT amortize
the ssm_out weight read across the 128 sequences. vLLM packs the same projection
into a single M=128 GEMM. The in-projection was already fine (2D input -> MMQ);
only the output projection was in 3D SSM layout.

## The fix

In the GDN output path of qwen35.cpp / qwen35moe.cpp / qwen3next.cpp, collapse
the final GDN output to 2D `[value_dim, n_seq_tokens * n_seqs]` (= [6144, 128] at
decode) BEFORE the ssm_out `ggml_mul_mat`, so `src1->ne[1] = 128` routes to the
MMQ M=128 GEMM. The result is then already 2D `[n_embd, n_seq_tokens * n_seqs]`,
so the redundant post-matmul reshape_2d is dropped. Same contiguous data, just a
2D vs 3D view => bit-identical. MMQ on NVFP4 at this exact M=128 shape was already
proven by the in-projection.

```
-    ggml_tensor * final_output = ggml_reshape_3d(ctx0, attn_out_norm, head_v_dim * num_v_heads, n_seq_tokens, n_seqs);
+    ggml_tensor * final_output = ggml_reshape_2d(ctx0, attn_out_norm, head_v_dim * num_v_heads, n_seq_tokens * n_seqs);
     ...
     cur = build_lora_mm(model.layers[il].ssm_out, final_output, model.layers[il].ssm_out_s);
-    cur = ggml_reshape_2d(ctx0, cur, n_embd, n_seq_tokens * n_seqs);
```

## Gates (all PASS)

- Bit-identical greedy (--temp 0 --seed 1, -n 200, llama-completion) vs the
  post-SSM baseline build:
  - dense q36-27b-nvfp4: md5 b90681a7728faadc44492b0bcd6181cc (IDENTICAL)
  - MoE   q36-35b-a3b-nvfp4: md5 f37c7ca1edd752e3bd82e99b4e8744b6 (IDENTICAL)
- test-backend-ops MUL_MAT: OK ; MUL_MAT_ID: OK
- Coherent dense + MoE output (greedy text inspected).

## decode_agg (llama-batched-bench, -fa on, -npp 128 -ntg 128 -npl 32,128 -c 33000)

S_TG t/s (decode aggregate):

| model            | npl | baseline | Lever 1 | delta   |
|------------------|-----|----------|---------|---------|
| dense q36-27b    |  32 |   170.52 |  200.00 | +17.3%  |
| dense q36-27b    | 128 |   254.92 |  335.80 | +31.7%  |
| MoE   q36-35b-a3b|  32 |   373.28 |  420.77 | +12.7%  |
| MoE   q36-35b-a3b| 128 |   560.66 |  691.24 | +23.3%  |

Dense @128: 335.80 t/s = 85.9% of vLLM 391 (target 82-85% HIT/exceeded;
up from 65% post-SSM).

## nsys (cuda_gpu_kern_sum, -npp 128 -ntg 24 -npl 128)

The o_proj FP4 batch-1 GEMV bucket is eliminated and the work moves to MMQ M=128:

| kernel                              | baseline           | Lever 1          |
|-------------------------------------|--------------------|------------------|
| mul_mat_vec_q<NVFP4, m=1> (o_proj)  | 132.8 ms / 48 inst | 0 ms / 0 inst    |
| mul_mat_q<NVFP4, m=128>             | 5463 ms / 8800 inst| 5827 ms /10000 inst|

The 132.8 ms o_proj GEMV bucket collapses to zero; mul_mat_q M=128 absorbs it
(+1200 instances, +363 ms over the window), and its per-call average DROPS
(620.8 us -> 582.7 us) because the added o_proj GEMMs are individually cheaper
than the average projection GEMM. Realized o_proj-as-MMQ marginal cost
~363.5 ms / 1200 = ~0.30 ms/call, versus the 2.77 ms/call (132.8 ms / 48) of the
old GEMV: the amortized weight read is the win.

Assisted-by: Claude:opus-4.8 [Claude Code]
