# MXFP4-dense vs Q4_K_M quality check (Qwen3, GB10 / DGX Spark)

## Question

MXFP4-quantized **dense** Qwen3-32B is measurably faster on GB10 (Blackwell) than
Q4_K_M: ~1.58x concurrent prefill, ~1.2x decode, for free (just a requantize that
routes onto the FP4-MMA kernel). Before LocalAI recommends MXFP4-dense as a Blackwell
default, we must confirm its **quality is acceptable versus Q4_K** (Q4_K is normally the
stronger 4-bit format).

Critical caveat going in: the pre-existing `~/bench/q3-32b-mxfp4-dense.gguf` was built
with `--allow-requantize`, so it was suspected to be **double-quantized** (Q4_K_M ->
MXFP4), which would unfairly penalize MXFP4. The goal here was a *fair* answer.

## Verdict

**Do NOT recommend MXFP4-dense as a quality-equivalent replacement for Q4_K on
Blackwell.** A clean apples-to-apples test (same BF16 source, both 4-bit, no imatrix)
shows MXFP4-dense carries a **large** quality penalty that Q4_K does not:

- Q4_K_M costs **+2.6%** perplexity vs the BF16 baseline.
- MXFP4-dense costs **+30.8%** perplexity vs the BF16 baseline (i.e. **+27.5% worse
  than Q4_K**).

The double-quant suspicion was correct but it was **not** the main culprit: even a clean
MXFP4-from-BF16 is dramatically worse than Q4_K. The ~1.58x prefill / ~1.2x decode
speedup is real, but it is not free on quality. MXFP4-dense output is still coherent (not
gibberish), so it is usable where raw throughput dominates and a quality hit is
acceptable, but it must not be presented as a drop-in, quality-neutral Q4_K replacement.

## Evidence

### 1. Provenance of the existing 32B MXFP4 (it is double-quant)

`~/dense_mxfp4.sh` (mtime matches the `q3-32b-mxfp4-dense.gguf` mtime, Jun 20 09:47)
created it:

```
SRC=$HOME/bench/q3-32b-gguf/Qwen3-32B-Q4_K_M.gguf      # <-- source is Q4_K_M, not F16/BF16
OUT=$HOME/bench/q3-32b-mxfp4-dense.gguf
$QB --allow-requantize --tensor-type "attn=mxfp4" --tensor-type "ffn=mxfp4" \
    "$SRC" "$OUT" MXFP4_MOE
```

Confirmed **double-quantized** (Q4_K_M -> MXFP4). Any PPL measured on this file
overstates MXFP4's true penalty, so the 32B number below is a loose upper bound, not the
fair answer.

### 2. 32B quick read (wikitext-2-raw test, 50 chunks, ctx 512, ngl 99)

`llama-perplexity`, PR build `~/llama.cpp-pr24423/build` (sm_121):

| 32B model | PPL | vs Q4_K |
|---|---|---|
| Qwen3-32B-Q4_K_M | **7.3865** +/- 0.177 | - |
| q3-32b-mxfp4-dense (double-quant) | **8.4638** +/- 0.206 | +14.6% |

MXFP4 is much worse than Q4_K here, **and** it is double-quant, so the quick read is
unfair -> escalated to a clean small-model comparison.

### 3. Fair comparison: clean small dense model (Qwen3-4B BF16)

The MXFP4-vs-Q4_K delta is a *format* property and roughly model-size-independent, so a
small model gives a fast, clean answer. Downloaded `Qwen3-4B-BF16.gguf` (unsloth, ~7.7
GiB) and quantized it **from that same BF16 source** to both formats with the identical
recipe used for the 32B (no `--allow-requantize` needed, no imatrix on either side):

```
llama-quantize  q3-4b-bf16.gguf  q3-4b-q4km.gguf   Q4_K_M
llama-quantize --tensor-type attn=mxfp4 --tensor-type ffn=mxfp4 \
               q3-4b-bf16.gguf  q3-4b-mxfp4.gguf  MXFP4_MOE
```

Perplexity (wikitext-2-raw test, 50 chunks, ctx 512, ngl 99):

| Qwen3-4B | size | PPL | vs BF16 | vs Q4_K |
|---|---|---|---|---|
| BF16 (baseline) | 7672 MiB | **13.3188** +/- 0.416 | - | - |
| Q4_K_M | 2497 MiB | **13.6605** +/- 0.426 | **+2.57%** | - |
| MXFP4 (clean) | 2236 MiB (4.66 BPW) | **17.4183** +/- 0.561 | **+30.78%** | **+27.5%** |

This is the apples-to-apples quality answer: **clean MXFP4-from-BF16 is ~12x more lossy
than Q4_K relative to the BF16 baseline** (30.8% vs 2.6%). Notably the clean-4B MXFP4-vs-
Q4_K gap (+27.5%) is *wider* than the 32B double-quant gap (+14.6%), consistent with
smaller models being more quantization-sensitive - the double-quant did not invent the
problem, it is intrinsic to the format as quantized by `llama-quantize`.

### 4. Coherence spot-check (32B, llama-simple, n=60)

MXFP4-dense 32B is fully coherent, not degraded gibberish:

- "The capital of France is" -> MXFP4: "...Paris, is located near the Seine River..."
  (correct); Q4_K similar.
- "Q: What is 17 multiplied by 23? A:" -> MXFP4 reasons via the distributive property
  (sound); Q4_K answers 391 directly (correct).
- "def fibonacci(n):" -> both emit valid Python.

So the quality cost shows up as measurably higher perplexity (and would surface on harder
/ longer tasks), not as obviously broken text at short generation lengths.

## Why

`MXFP4_MOE` is a 4-bit float format (E2M1 values, shared E8M0 scale per block of 32,
round-to-nearest) designed for MoE expert tensors (gpt-oss et al.) with a coarse
per-block scale. Q4_K uses 6-bit superblock scales plus per-sub-block mins - materially
better for dense attention/FFN weights. Forcing MXFP4 onto dense layers to reach the FP4
kernel trades ~1.58x prefill for a large accuracy loss. The FP4-MMA speed path is real,
but the weights it accepts (MXFP4 here) are lossy for dense.

## Caveat, stated precisely

This measures **llama.cpp's `llama-quantize` MXFP4** (OCP MX FP4, RTN, **no imatrix**)
against **llama.cpp's Q4_K_M** (k-quant superblocks, also no imatrix here). It is a fair
format-vs-format comparison of exactly what LocalAI would ship if it routed a requantize
through this path. It does **not** claim FP4 is fundamentally unviable on Blackwell:

- An imatrix-aware MXFP4, or a better FP4 format with two-level scaling
  (**NVFP4** - there are already `q3-32b-nvfp4` / `q3-32b-nvfp4a16` dirs on the box),
  may close much of this gap and is the more promising Blackwell FP4 path to evaluate.
- The result is for Qwen3 dense; other families may differ in magnitude but the
  format-level disadvantage of plain MXFP4 RTN vs Q4_K is expected to hold.

## Recommendation

- **Do not** ship a blanket "use MXFP4-dense on Blackwell" recommendation as a Q4_K
  quality equivalent. The ~1.58x prefill / ~1.2x decode win comes with a real ~30% PPL
  inflation (vs ~2.6% for Q4_K). Q4_K_M stays the right dense default on Blackwell.
- If exposing MXFP4-dense at all, gate it as an explicit **throughput-over-quality**
  option with the perplexity caveat surfaced, not a default.
- MXFP4/FP4 remains correct where the model is trained for it (MoE / gpt-oss-style).
  Pursue **NVFP4** (and/or imatrix-aware FP4) as the quality-competitive Blackwell FP4
  format before making any FP4-dense recommendation.

## Reproduction (DGX Spark, GB10, build `~/llama.cpp-pr24423/build`, sm_121)

- Dataset: `~/wikitext-2-raw/wiki.test.raw` (wikitext-2-raw-v1 test).
- 32B: `~/ppl32b.sh` -> `~/ppl32b.out`; coherence `~/coh32b.sh` -> `~/coh32b.out`.
- Clean 4B: `~/fair4b.sh` -> `~/fair4b.out` (quantize + 3x perplexity).
- All runs `-ngl 99`, `--chunks 50`, `-c 512`. GB10 thermal-throttles but PPL is a
  correctness metric, so thermal state does not affect these numbers.
