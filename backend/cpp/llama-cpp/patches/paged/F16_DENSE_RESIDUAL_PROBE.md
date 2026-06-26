# F16/BF16 Glue Probe - the dense decode residual to vLLM

Question: dense decode parity sits at llama 384.6 vs vLLM 418.8 t/s @ npl128 = 91.8%.
The 49% SSM recurrence (f32 BOTH engines) and the 27% NVFP4 GEMM (W4A4 BOTH) are
precision-matched. The residual ~8% may be partly that llama runs the NON-recurrence
GLUE (attention, norms, activations, elementwise, residual stream) in F32 while vLLM
runs the model in BF16. This probe settles, empirically on q36-27b-nvfp4 @npl128, how
much of that residual is realistically f16/bf16-closable.

Model: Qwen3.5-27B NVFP4 (dense). 64 layers = 16 attention + 48 gated-DeltaNet
(SSM) recurrent. Build b104-f7409c2 (patch 0023), verified git-clean and coherent.
The bf16 SSM work was never applied to the tree (only saved as a diff backup);
ggml-cuda needed no recompile on rebuild, so the binary is bit-identical to clean 0023.

## (1) Current KV / state dtype  (SETTLED)

From the `-v` init log:

- ATTENTION KV cache (16 of 64 layers):
  `K (f16): 1280 MiB, V (f16): 1280 MiB`  =>  **DEFAULT IS ALREADY F16.**
- RECURRENT cache (48 gated-DeltaNet layers):
  `R (f32): 180 MiB` (conv state), `S (f32): 4608 MiB` (SSM state)  =>  **f32.**

Consequence: the attention KV is ALREADY at vLLM's 16-bit bit-width. `--cache-type f16`
is a literal no-op; the cheap KV lever is spent. The f32 lives in (a) the recurrent
SSM/conv state (matched to vLLM, the bf16 version is shelved for failing the f32 KL
gate) and (b) the intermediate-activation glue (norms, residual stream, attention
compute, activations) - that glue is where llama still pays f32 vs vLLM bf16.

## (2) Decode kernel budget  (nsys --cuda-graph-trace=node, npl128, 39 steady steps)

step span 342.0 ms ; sum-of-kernels 338.8 ms ; **kern/span 99.0%** - the decode is
GPU-bound, kernels back-to-back, nsys overhead negligible. The measured bench step
(128 tok / 373.5 t/s = 342.8 ms) equals the nsys span, so the %-of-step figures below
ARE wall-time fractions.

OUT of scope - already precision-matched (83.2% of the step):

| kernel | ms/step | % |
|---|---:|---:|
| gated_delta_net (SSM recurrence, f32 BOTH) | 167.1 | 49.3 |
| mul_mat_q NVFP4 (W4A4 GEMM, BOTH)          |  93.0 | 27.4 |
| quantize_mmq_nvfp4 (FP4 act-quant)         |  17.6 |  5.2 |
| mul_mat_q stream_k fixup (FP4 reduction)   |   4.1 |  1.2 |

F16-ABLE GLUE - f32 in llama, bf16 in vLLM:

Budget A (clean compute glue, decoupled from the f32 state):

| kernel | ms/step |
|---|---:|
| flash_attn_ext            | 11.94 |
| unary_gated_op (silu)     |  5.16 |
| k_bin_bcast (mul)         |  4.72 |
| rms_norm                  |  3.58 |
| k_bin_bcast (add, residual)|  1.67 |
| l2_norm                   |  0.65 |
| cpy_scalar                |  0.37 |
| rope                      |  0.26 |
| sigmoid                   |  0.22 |
| softplus                  |  0.09 |
| flash_attn fixups         |  0.08 |
| **Budget A total**        | **28.74 ms = 8.4% of step** |

Budget B (+ the non-FP4 cublas GEMM): + nvjet 12.17 ms => **40.91 ms = 12.0%**.

Recurrence-coupled data movement (NOT bit-safe f16-able - needs the f32 state to go
bf16, which is the shelved work that fails the f32 KL gate):
ssm_conv 8.37 + k_get_rows_float 6.98 + k_set_rows 0.66 + gdn_gather 0.06 = 16.08 ms = 4.7%.

## (3) Cache-type A/B  (decode_agg S_TG t/s, dense)

| npl | DEFAULT | F16-explicit | Q8_0 |
|---:|---:|---:|---:|
|  32 | 209.05 | 208.75 | 208.63 |
| 128 | 373.46 | 373.56 | 374.71 |

- F16-explicit == DEFAULT (0.03% delta) => proves the default KV is already f16; the
  flag is a no-op.
- Q8_0 (8-bit, half the f16 KV bytes) is within noise at every npl => the attention KV
  bandwidth is NOT a decode bottleneck (it is 16/64 layers; flash_attn is 3.5% of the
  step). The KV-cache dtype is not a decode lever for this model.
- Coherence (48-tok greedy, "The capital of France is"): default and q8_0 both fully
  coherent; q8_0 only causes minor greedy-path divergence, no quality break. But since
  q8_0 buys zero speed and is not bit-exact, it is pointless here.

## Read: how much of the ~8% dense residual is f16-closable

The gap is ~27 ms/step (llama 332.8 ms vs vLLM 305.7 ms at npl128).

f16 does not zero the glue, it speeds it up. Realistic recovery:
- Memory-bound glue (norms + elementwise + activations + copies + rope = 16.7 ms):
  f16 halves the bytes => ~50% => ~8.4 ms.
- flash_attn_ext (12.0 ms): KV is ALREADY f16 and the accumulation must stay f32
  (vLLM also f32-accumulates), so only the Q/projection side helps => ~25% => ~3.0 ms.
- Budget A realistic recovery ~= **11.4 ms**.
- nvjet non-FP4 GEMM (12.2 ms): bf16 tensor cores vs f32 ~= ~40-50% => ~5 ms, but
  uncertain (may already run TF32) => +nvjet recovery ~= **16 ms**.

So f16/bf16 glue realistically recovers **~11 ms (glue only) to ~16 ms (+GEMM) of the
~27 ms gap = roughly 40-60% of the dense residual.** That moves parity 91.8% ->
~95-96%, NOT a full close. The remaining ~3-4% is structural: cublas GEMM efficiency
on the non-FP4 paths, graph/launch scheduling vs vLLM, and the irreducible f32
accumulation in attention and the recurrence.

Caveats for a build decision:
1. The single largest f16-able line (flash_attn 11.9 ms) is the LEAST recoverable
   (KV already f16, accumulate stays f32). The cleanly recoverable mass is the
   norms+elementwise+activations (~16.7 ms).
2. The recurrence-coupled 4.7% (ssm_conv + state gather) is only f16-able by taking the
   SSM/conv state to bf16 = the already-built, already-shelved work that fails the f32
   KL gate. It is OUT of a bit-safe f16 build.
3. f16 glue is NON-bit-exact (same category as the shelved bf16 SSM state). It would be
   an OPT-IN fast path, not the bit-exact default. Realistic ceiling ~95-96% parity for
   a meaningful (norms/elementwise/activations + optionally nvjet) f16 conversion, at
   the cost of leaving the 95%-bit-exact f32 plateau.

Assisted-by: Claude:opus-4.8 [Claude Code]
