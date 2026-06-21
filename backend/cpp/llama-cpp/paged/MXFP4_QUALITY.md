# MXFP4-dense vs Q4_K_M: quality check (Blackwell recommendation gate)

Question: MXFP4-dense is ~1.58x faster concurrent prefill than Q4_K on GB10 (routes onto the FP4-MMA
kernel). Is its quality acceptable enough to recommend on Blackwell? **Answer: NO - it is a large quality
regression. Do not recommend MXFP4 for dense weights.**

## Measured (wikitext-2-raw test, --chunks 50, -c 512)

**Fair comparison - Qwen3-4B, all three quantized from the SAME BF16 source (clean, no double-quant):**

| quant | PPL | vs BF16 |
|---|---|---|
| BF16 (baseline) | 13.32 | - |
| **Q4_K_M** | **13.66** | **+2.6% (near-lossless)** |
| **MXFP4** (attn+ffn, MXFP4_MOE) | **17.42** | **+30.8%** |

**MXFP4 is ~27% worse PPL than Q4_K**, even quantized cleanly from BF16.

Cross-check - Qwen3-32B (existing models; the MXFP4 there is double-quant Q4_K->MXFP4, an unfair lower bound):
Q4_K_M 7.39 vs MXFP4 8.46 (+14.6%). Same direction; the clean 4B number is the fair one.

## Why

`MXFP4_MOE` is a 4-bit float format designed for MoE expert tensors (gpt-oss et al.), with a coarse per-block
scale. Q4_K uses 6-bit superblock scales + per-sub-block mins - materially better for dense attention/FFN
weights. Forcing MXFP4 onto dense layers to reach the FP4 kernel trades ~1.58x prefill for a large accuracy
loss. The FP4-MMA speed path is real, but the only weights it accepts (MXFP4/NVFP4) are lossy for dense.

## Verdict

**Do NOT ship a Blackwell "use MXFP4 for dense" recommendation.** The ~1.58x prefill (and ~1.2x decode) is not
worth ~27% perplexity. Q4_K_M stays the right dense default on Blackwell (near-lossless; its ~764 t/s prefill
ceiling is the int8-MMQ kernel limit, not the quant). MXFP4/FP4 remains correct only where the model is trained
for it (MoE / gpt-oss-style). A finer FP4 format (NVFP4) might narrow the gap but is unproven for dense here and
is a separate investigation.
