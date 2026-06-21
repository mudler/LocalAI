# NVFP4-dense on DGX Spark (GB10, sm_121): is it the quality-preserving FP4 win MXFP4 wasn't?

Test rig: DGX Spark GB10 (sm_121), `~/llama.cpp-pr24423/build` (PR #24423, FP4 MMA + NVFP4
kernel), wikitext-2-raw, clean BF16 source `q3-4b-bf16.gguf` (the same source used for the
established MXFP4 / Q4_K fair test). NVFP4 and all comparison quants were produced clean from
BF16, no imatrix.

## Verdict (short)

YES on all the load-bearing questions, with one honest caveat:

1. llama.cpp CAN produce an NVFP4 GGUF.
2. NVFP4 quality is Q4_K-class, NOT MXFP4-class: +7.4% PPL vs BF16 (MXFP4 was +30.8%). It is
   slightly behind Q4_K (+4.8% relative) but in the same ballpark, not on the quality cliff.
3. NVFP4 routes onto the FP4 MMA kernel and gets the FP4 prefill speedup: ~1.29x Q4_K on the
   4B, tracking MXFP4 to within 5% (MXFP4 hit 1.58x on the 32B; NVFP4 should track it there too).
4. Output is coherent.

Bottom line: NVFP4-dense IS the quality-preserving FP4 win MXFP4 wasn't. It delivers
essentially the full FP4 prefill speedup at roughly Q4_K quality, where MXFP4 paid a 27% quality
tax for the same speed. LocalAI can support/recommend NVFP4-dense on Blackwell for prefill-bound
workloads, with the caveat that it is marginally (~5%) behind Q4_K on perplexity; an imatrix-guided
NVFP4 quant would likely close most of that remaining gap.

## 1. Feasibility: can llama-quantize produce an NVFP4 GGUF? YES

- The type exists with a full quantize path, not just a kernel:
  - `GGML_TYPE_NVFP4 = 40` (`ggml.h`), `GGML_FTYPE_MOSTLY_NVFP4 = 26`
  - `quantize_nvfp4` / `quantize_row_nvfp4_ref` / `dequantize_row_nvfp4` registered in `ggml.c`
  - type_name is `"nvfp4"`, block `QK_NVFP4` (per-16 FP8/E4M3 block scale + global scale)
- NVFP4 is NOT a top-level `llama-quantize` ftype (no `NVFP4` entry in the allowed-types list,
  no reference in `tools/quantize/quantize.cpp` or `src/llama-quant.cpp`), BUT
  `--tensor-type name=nvfp4` resolves it: `parse_ggml_type` matches the arg against
  `ggml_type_name(...)`, which returns `"nvfp4"`. This is the exact same mechanism that produced
  MXFP4-dense.
- Recipe used (mirrors the MXFP4-dense GGUF byte-for-byte in structure: token_embd Q8_0, all
  norms F32, all 2D attn+ffn weights to FP4):

  ```
  llama-quantize --tensor-type "attn=nvfp4" --tensor-type "ffn=nvfp4" \
                 q3-4b-bf16.gguf q3-4b-nvfp4.gguf Q8_0
  ```

  Result: `q3-4b-nvfp4.gguf`, 2343.93 MiB, 4.89 BPW, ~5 s. (MXFP4-dense was 2350 MiB; same shape.)
  Every `blk.N.attn_*` and `blk.N.ffn_*` reported `converting to nvfp4`; token_embd Q8_0; norms F32.

The on-box `~/bench/q3-32b-nvfp4*` dirs are vLLM HF safetensors (already 4-bit), not GGUF, and
do not feed llama.cpp - confirmed and irrelevant.

## 2. Quality (decisive): NVFP4 is Q4_K-class, not MXFP4-class

`llama-perplexity -f wiki.test.raw --chunks 50 -c 512 -ngl 99`, all clean from the same BF16 4B:

| Quant   | PPL    | vs BF16  | vs Q4_K  |
|---------|--------|----------|----------|
| BF16    | 13.32  | -        | -        |
| Q4_K_M  | 13.66  | +2.6%    | -        |
| NVFP4   | 14.31  | +7.4%    | +4.8%    |
| MXFP4   | 17.42  | +30.8%   | +27.6%   |

(NVFP4 measured this run: Final PPL = 14.3097 +/- 0.4457.)

NVFP4 lands much closer to Q4_K (gap 0.65 PPL) than to MXFP4 (gap 3.11 PPL). MXFP4's finer
sibling delivers: the two-level scaling (per-16 FP8 block scale + global scale) recovers almost
all of the quality MXFP4's coarse per-32 E8M0 scale threw away. It is not quite Q4_K, but it is
firmly in the "acceptable 4-bit" regime, not the lossy one.

## 3. Speed: NVFP4 routes onto the FP4 MMA kernel

No clean BF16 32B was on the box (only the vLLM NVFP4 safetensors and the Q4_K/MXFP4 32B GGUFs),
so per the brief this is the 4B speed signal - a 3-way cold A/B on the SAME 4B model, 45 s
cooldowns between runs (`-npp 512 -ntg 128 -npl 8,32,64 -b 2048 -ub 2048 -ngl 99`):

Prefill S_PP (t/s):

| B   | Q4_K   | NVFP4  | MXFP4  | NVFP4 / Q4_K | NVFP4 / MXFP4 |
|-----|--------|--------|--------|--------------|---------------|
| 8   | 4862   | 6313   | 6602   | 1.30x        | 0.96x         |
| 32  | 5020   | 6497   | 6836   | 1.29x        | 0.95x         |
| 64  | 5031   | 6490   | 6831   | 1.29x        | 0.95x         |

- NVFP4 prefill is within ~5% of MXFP4 at every batch size -> both land on the same FP4 MMA
  kernel. NVFP4 does NOT fall back to a slow path.
- NVFP4 beats Q4_K's int8-MMQ prefill by ~1.29x on the 4B. The established 32B figures were
  Q4_K S_PP ~767 and MXFP4 ~1209 (1.58x); since NVFP4 tracks MXFP4 to within 5%, NVFP4 on the
  32B should likewise approach ~1.5x. (The 4B shows a smaller multiplier than the 32B because a
  smaller model spends proportionally less time in the matmul the FP4 kernel accelerates.)
- Token-gen (S_TG) is comparable across all three (memory-bound), as expected.

## 4. Coherence

`llama-simple` (llama-cli hangs - avoided), NVFP4 4B:
- "The capital of France is" -> "...Paris. ...Germany is in Berlin. ...Italy is in Rome.
  ...Spain is in Madrid. ...Netherlands is in Amsterdam." (all correct)
- "Q: What is 17 plus 25? A:" -> "42." (correct)

Coherent and factually accurate.

## Recommendation for LocalAI on Blackwell

Support and recommend NVFP4-dense as the FP4 prefill option on Blackwell (sm_120/121), produced
via `--tensor-type attn=nvfp4 --tensor-type ffn=nvfp4` over a BF16 source (token_embd Q8_0,
norms F32). It gives ~the full FP4 prefill speedup (FP4 MMA kernel, ~1.3x Q4_K on 4B and
expected ~1.5x on larger models) at roughly Q4_K quality (+7.4% PPL vs BF16). This is the win
MXFP4 failed to deliver: MXFP4 paid a +30.8% quality tax for the same speed and was rejected.

Caveats / follow-ups:
- NVFP4 is still ~4.8% behind Q4_K on PPL. For quality-first deployments where the prefill win
  does not matter, Q4_K_M remains the better pick.
- These NVFP4/Q4_K numbers are clean (no imatrix). An imatrix-guided NVFP4 quant is the obvious
  next step and would likely close most of the remaining gap to Q4_K - worth measuring before a
  blanket recommendation.
- A direct 32B NVFP4-vs-Q4_K speed run (needs a clean BF16 32B GGUF, not on the box) would
  confirm the projected ~1.5x; the 4B signal plus the MXFP4-tracking already make this very likely.
