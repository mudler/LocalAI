# LEVER 4 - NVFP4 the bf16 MoE GDN/attn projections: KL-GATE FAIL, no-ship

GPU agent (L4-gatebench), DGX GB10 (sm_121, BLACKWELL_NATIVE_FP4=1). Build at 0028 (HEAD fafe878,
branch `paged`). Lever 4 hypothesis (from `MOE_GAP_VS_VLLM.md` + the lever-4 scope): the MoE GGUF's
GDN/attn projections (in_proj_qkvz=attn_qkv, in_proj_ba=ssm_alpha/ssm_beta, out_proj=ssm_out,
attn_gate, full-attn attn_q/k/v/output) are left in BF16 by nvidia modelopt while the dense
q36-27b-nvfp4 (unsloth) already ships them NVFP4. The scope called this a "quant-provenance accident"
and proposed re-quantizing them to NVFP4 to recover the ~20.3->13.8ms projection-GEMM bucket.

**Verdict: KL-GATE FAIL on every axis, for both variants. STOP, do NOT ship. No 0029 GGUF, no
gallery entry, no bench, no nsys** (per spec: KL fails first -> report, do not bench/ship). The bf16
projections are a **deliberate precision choice, not an accident** - re-quantizing them costs ~6% PPL.

## Gate setup (all bit-changing -> KLD gate per spec)

- Reference (the "f32" of the gate): `~/work/darwin_36b_opus/f16.gguf` - the full-precision f16 GGUF
  of the same Qwen3.6-35B-A3B model (qwen35moe, 41 blocks, vocab 248320, embd 2048). Verified it
  matches the NVFP4 baseline shape; its own PPL = 7.376 self-consistent with the KLD base.
- KL base: `llama-perplexity --kl-divergence-base` over `wiki.test.raw`, c512, 16 chunks (8192 tok),
  -ngl 99, seed 1. Base file `~/bench/l4gate/klbase_moe.dat` (2.0 GB). f16 PPL(base) = 7.3734.
- Candidates scored with `--kl-divergence` against that base, identical c512/16-chunks/seed.
- Current "bf16-projection GGUF" baseline = `~/bench/q36-35b-a3b-nvfp4.gguf` (the shipping NVFP4:
  experts NVFP4, GDN/attn projections BF16). It is the reference for the PPL-delta and argmax gates.

## Measurements (16 chunks, c512, 8192 tokens, wiki.test.raw)

| model | PPL(Q) | PPL delta vs baseline | Mean KLD-to-f16 | Same-top-p (argmax agree vs f16) | RMS dp |
|-------|--------|-----------------------|-----------------|----------------------------------|--------|
| baseline NVFP4 (proj BF16, shipping) | 7.3896 | - (reference) | 0.1366 | 84.31% | 9.20% |
| **projq FULL** (190 proj -> NVFP4, incl. in_proj_ba) | 7.8705 | **+6.51%** | 0.1638 | 81.72% | 10.47% |
| **projq CONS** (130 proj -> NVFP4, in_proj_ba kept BF16) | 7.8440 | **+6.15%** | 0.1716 | 82.16% | 10.82% |

Baseline vs f16: PPL ratio 1.0022 (+0.22%), i.e. the shipping NVFP4 is already near-f16 - because
modelopt put the quant-sensitive GDN/attn projections in BF16 and only the experts (designed for FP4)
in NVFP4. projq pushes the projections to NVFP4 and PPL ratio jumps to 1.067 (FULL) / 1.064 (CONS).

## Gate verdict (all three conditions FAIL)

1. **PPL delta < ~1% vs the bf16-projection GGUF -> FAIL.** FULL +6.51%, CONS +6.15%. Off by ~6x.
2. **KLD-to-f32 < 0.06 -> FAIL.** The shipping baseline NVFP4 itself sits at 0.137 mean KLD vs f16
   (per-token KLD is naturally high at 248K vocab), and projq raises it to 0.164 (FULL) / 0.172 (CONS).
   Whatever the intended reference granularity, projq is strictly worse than the baseline, not < 0.06.
3. **Zero greedy-argmax flips -> FAIL.** Per-token top-1 agreement vs f16 drops from 84.31% (baseline)
   to 81.72% (FULL) / 82.16% (CONS): the requant flips the argmax on ~2.2-2.6% MORE tokens than the
   shipping model. (A direct `llama-cli --temp 0 -n 48` greedy diff was attempted but the paged
   llama-cli build segfaults at teardown on ALL models incl. baseline - not projq-specific - so the
   8192-token Same-top-p above is the argmax measure used; it is strictly stronger than a 48-tok probe.)

CONSERVATIVE (keeping the most quant-sensitive in_proj_ba=ssm_alpha/ssm_beta in BF16) recovered almost
nothing: 7.844 vs 7.871. The damage is in the BULK attn/GDN projections (attn_qkv, ssm_out, attn_gate,
attn_q/k/v/output), not the tiny in_proj_ba. An attn_gate-excluded third variant would, at best, shave
a fraction of a percent off a 6% miss - not worth a GPU pass. lm_head was already NVFP4 in the baseline
(and in vLLM's checkpoint), so it is not a variable here and was never the issue.

## Why the premise was wrong (root cause of the failure)

The scope assumed vLLM runs these projections in NVFP4. It does not. vLLM runs the **nvidia modelopt
checkpoint** (`~/bench/q36-35b-a3b-nvfp4-vllm`), which is the SAME provenance that left these exact
projections in BF16. So:

- The baseline GGUF's bf16 projections **match vLLM** already. They are not a llama-vs-vLLM gap.
- modelopt left in_proj_qkvz/in_proj_ba/out_proj/attn_q/k/v/output in BF16 **because they are
  quant-sensitive in this hybrid gated-DeltaNet + attention model** - the gate confirms this empirically
  at ~6% PPL. The dense q36-27b-nvfp4 (unsloth) tolerating NVFP4 projections does not transfer: it is a
  different (non-MoE, different-provenance) model and a different sensitivity profile.
- Re-quantizing them is therefore not "matching vLLM" - it is going BEYOND vLLM's precision and paying
  for it in quality. The ~20.3ms projection-GEMM bucket is the price of running these projections in
  high precision; vLLM pays the same precision cost (its nvjet/cutlass bf16 GEMMs), so the bucket is NOT
  the lever it looked like. The L4 speed win is real but only purchasable with a 6% PPL regression -
  rejected by the gate.

## Disposition / artifacts

- Both projq GGUFs exist on DGX but are **dead** (do not publish): `~/bench/q36-35b-a3b-nvfp4-projq.gguf`
  (FULL, md5 1bd32114..., sha256 88b7e812...), `~/bench/q36-35b-a3b-nvfp4-projq-cons.gguf` (CONS, md5
  6847ebe3..., sha256 ca035111...). The L4-requant pin files (`~/bench/pins_projq_{full,cons}.txt`) and
  `/tmp/gen_pins.py` remain if a future, kernel-side (not precision-side) approach is ever revisited.
- Gate logs: `~/bench/l4gate/` - `f16base.log`, `kld.{baseline,projqFULL,projqCONS}.log`,
  `klbase_moe.dat`.
- No code change, no patch, no commit to the DGX `llama-paged-dev` tree. No `-paged` gallery entry.
- MoE remains at 86.3% of vLLM @ npl128; this lever does not move it within the quality budget.

Assisted-by: Claude:opus-4.8 [Claude Code]
