# Paged bit-exactness gate - per path (canonical references)

## TL;DR

The greedy decode of the **paged** path does not byte-match the **non-paged**
path for the MoE model. This is a **benign FP-accumulation-order difference of
the paged attention reduction**, KL-validated against the f16 reference. It is
**not a bug**. The bit-exactness gate is therefore **per path**:

| path | model | canonical md5 |
|------|-------|---------------|
| non-paged | MoE q36-35b-a3b-nvfp4   | `07db32c2bcb78d17a43ed18bc22705cd` |
| paged     | MoE q36-35b-a3b-nvfp4   | `8cb0ce23777bf55f92f63d0292c756b0` |
| non-paged | dense q36-27b-nvfp4     | `5951a5b4d624ce891e22ab5fca9bc439` |
| paged     | dense q36-27b-nvfp4     | `5951a5b4d624ce891e22ab5fca9bc439` (bit-exact to non-paged) |

Gate command (chat-template / conversation path):
```
llama-completion -m MODEL -ngl 99 -fa on -p "The capital of France is" \
                 -n 48 --temp 0 --seed 1
# paged: prefix with  LLAMA_KV_PAGED=1 LLAMA_MOE_FORCE_GRAPHS=1
```
Note: use the default chat-template path (do **not** pass `-no-cnv`; raw
completion lands in a different md5 namespace).

**Future paged-MoE regressions compare to the PAGED reference `8cb0ce23`, not to
the non-paged `07db32c2`.** Dense is bit-exact across paths, so dense uses the
single reference `5951a5b4`.

## Why dense is bit-exact but MoE is not

Dense paged decode reproduces the non-paged reduction order exactly, so dense
greedy md5 is identical across paths. The MoE path runs additional kernels (the
NVFP4 MoE GEMM + expert routing) whose multi-kernel accumulation order differs
between the paged and non-paged attention layouts. Over a long greedy decode this
flips a small number of near-tied argmaxes, changing the byte stream. The same
divergence is present on the 0028 baseline, with `LLAMA_MOE_FORCE_GRAPHS` on or
off, and with the patch-0029 block-table cache on or off - it is a property of
the paged attention path, not of any one lever.

## KL evidence that the paged path is sound (the load-bearing check)

`llama-perplexity --kl-divergence` on `q36-35b-a3b-nvfp4.gguf`, 16 chunks,
`-c 512 -ngl 99 --seed 1`, base logits from the f16 reference
(`darwin_36b_opus/f16.gguf`, PPL 7.3734):

| comparison | PPL(Q) | KL divergence | Same top p | Cor |
|------------|-------:|--------------:|-----------:|----:|
| f16 reference | 7.3734 | - | - | - |
| **non-paged** vs f16 | 7.3896 | 0.136597 +/- 0.003157 | 84.314% | 97.68% |
| **paged** vs f16     | 7.4009 | 0.136000 +/- 0.003285 | 84.828% | 97.58% |
| paged vs non-paged (direct) | 7.4009 (base 7.3818) | 0.050011 +/- 0.001653 | 89.044% | 99.04% |

Direct paged-vs-non-paged: Mean Delta-p = 0.079% (no bias), RMS Delta-p = 6.187%.

### Verdict: BENIGN

- **Paged does not diverge from the f16 ground truth more than non-paged does.**
  KLD(paged||f16) = 0.13600 <= KLD(nonpaged||f16) = 0.13660, and PPL(paged) =
  7.4009 ~ PPL(nonpaged) = 7.3896 (difference 0.011, far inside the +/- 0.29
  error bars). A real paged-MoE correctness bug would push paged measurably
  *further* from f16; it does not (it is marginally closer).
- **Paged and non-paged cluster together.** They agree with each other (KLD 0.050,
  89.0% same-top-p) more than either agrees with f16 (KLD ~0.137, ~84% same-top-p),
  with essentially zero probability bias. That is the signature of two equivalent
  FP-reorderings of the same quantized model, both equally approximating the f16
  ground truth - not a quality regression.
- The direct same-top-p of 89.0% is below a naive ">99%" heuristic, but that
  heuristic is calibrated for higher-precision models. In a 4-bit (NVFP4) model
  logit near-ties are abundant, so a different-but-equivalent reduction order
  flips ~11% of argmaxes with no quality cost (proven by the equal KLD-to-f16 and
  zero Delta-p bias).

Therefore the canonical gate is per path, and `8cb0ce23` is the validated paged
reference for the MoE deployment path.
