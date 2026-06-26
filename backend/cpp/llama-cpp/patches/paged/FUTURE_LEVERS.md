# Decode-Parity: Parked Levers (future exploration)

**Context.** The bit-exact decode-parity effort shipped patches **0018-0023**: dense decode
38% -> **95% of vLLM** @npl128 on GB10 / DGX Spark (LPDDR5x ~273 GB/s), every patch
**byte-identical to llama's own f32 output** (md5-gated). The gated-DeltaNet recurrence (the
dominant ~50% kernel) now runs at **84.6% of peak BW = past vLLM's 82.4%**, at the DRAM floor.
bf16 SSM state was fully built and **shelved** (real +25-31% lever but fails the f32 KL gate).

The remaining non-recurrence kernels (FP4 GEMM, attention, lm_head) are at their bit-exact
floor: any knob changes a reduction order vs the f32 reference. So further *bit-exact* decode
gains are marginal; the levers below are the honest pick-up points, ranked by promise.

---

## 1. Hybrid-precision SSM state (the most promising)

The bf16 build (`BF16_SSM_STATE_RESULTS.md`) proved the throughput lever is large -
recurrence **-49%/call** (dense 3.38 -> 1.73 ms), dense decode ~**490 t/s = 125% of vLLM** (clean
runs), MoE @128 **+24.9%** - but bf16 fails the f32 KL gate (KLD 0.06-0.17 at >=1024 ctx,
~10% argmax flips). The discrimination showed the error is **intrinsic to bf16 over the
long-memory heads** (exp(g) ~ 1, where the per-step decay does not contract the rounding);
short/fast-decaying heads are fine.

**Lever:** a per-head (or per-channel) precision split - keep the long-memory heads (g near 1)
in f32, store the fast-decaying heads (g well below 1, where rounding contracts) in bf16. Could
capture most of the speedup while passing the KL gate. Needs a g-magnitude classifier at graph
build + a mixed-dtype recurrent-state cache. **HIGH promise, moderate effort.** The bf16 kernel
plumbing already exists (DGX `~/llama-paged-dev/BF16_SSM_STATE.diff`); this adds the per-head
dtype selection on top.

*Note (precision, corrected):* plain bf16 (no split) is a legitimate **opt-in for precision-tolerant
deployments**, but it is *below* vLLM's recurrent precision, NOT equal to it. vLLM keeps the
gated-DeltaNet **temporal state in f32** (proven three ways in `BITEXACT_VS_VLLM.md`; only its tiny
conv state is bf16, and llama keeps even that f32). So bf16 here trades *below-vLLM* precision for
*above-vLLM* throughput. We declined it as the default because both llama's f32 AND vLLM's f32 are a
higher bar - and at equal f32 precision llama's recurrence already beats vLLM (84.6% vs 82.4% peak BW),
so we do not need bf16 to match vLLM's recurrence.

## 2. Dense CUDA-graph instability

The bf16 dense decode was **bimodal** across runs (287 / 336 / 487 / 498 t/s) - a dense-path
CUDA-graph capture/replay instability (good runs hit ~490). The f32 dense path measured stable
(371-376) but the bimodality is a latent fragility worth root-causing; a robust graph capture on
the dense path could stabilize and possibly lift dense decode. **Moderate promise**, diagnostic.

## 3. Dense rms_norm -> fp4 producer-fold (~1.5-2.5%, parked as flat-risk)

The last bit-exact bucket (`RMSNORM_FP4_FOLD.md`). Folding the standalone `quantize_mmq_nvfp4`
into the rms_norm+mul producer at the FFN boundary (f32 output dead -> droppable) could recover
~1.5-2.5% dense. Parked because: the Lever-2 precedent measured **flat**, it has the worst
gain/plumbing ratio (3-op `{RMS_NORM,MUL,MUL_MAT(NVFP4)}` graph fusion + a pre-quantized-src1
GEMM path + scratch-pool / CUDA-graph-lifetime plumbing), and the gain risks being swallowed by
the ~0.3-0.5% bench noise floor. Revisit only with the inter-node graph-CSE plumbing built and
proven on a same-build flag toggle (decode_agg lift above noise AND md5 == 0023). **LOW promise.**

## 4. Datacenter Blackwell (sm_100)

This effort targeted **consumer** Blackwell sm_12x (sm_120 RTX 50-series, sm_121 GB10). Datacenter
Blackwell (B100/B200/GB200, sm_100 / cc 10.0) has HBM3e (much higher BW) and different MMA
characteristics - the LPDDR5x bandwidth floor that dominates GB10 decode does **not** apply, so the
whole calculus changes (likely compute-bound, not BW-bound; the recurrence would not be the binding
kernel). A separate investigation if datacenter Blackwell becomes a target.

## 5. Prefill / TTFT scheduler + paged-pool burst degradation (HIGH priority - the weakest benchmark number)

The final benchmark (`QWEN36_NVFP4_BENCH.md`) exposed TTFT as the clear weak spot vs vLLM. Two distinct
issues:
- **Static decode-first budget tradeoff:** the QoS budget (patches 0013/0016, `LLAMA_MAX_BATCH_TOKENS=512`)
  maximizes decode tok/s + memory but throttles burst-prefill, so under a synchronized 128-way burst TTFT
  climbs to **903 s dense / 213 s MoE @npl128** vs vLLM's chunked-prefill 6-18 s. A dynamic/adaptive budget
  (by concurrency + queue depth), or matching vLLM's chunked-prefill interleave, would rebalance.
- **Paged-pool burst-degradation BUG (concrete, found in the benchmark):** after a high-npl burst, a
  server's *subsequent lower-npl* prefill collapses (fresh npl8 = 507 t/s / 6 s TTFT; npl8 after an npl64
  burst = 65 t/s / 64 s). Decode stays robust; only prefill degrades -> root-cause the paged-pool state
  that persists across the burst.

**HIGH promise** for the serving experience: decode (dense 90-117%, MoE 77-83% of vLLM) and memory (1.5-3x
lower) are already strong; TTFT is the one number holding back a clean public win.

## 6. MoE-specific recurrence tuning

The occupancy retune (0022) was tuned on the dense path; it lifted MoE +8.3% as a side effect. The
MoE path (`MUL_MAT_ID` grouped GEMM + the shared GDN recurrence, expert routing changes the GEMM
shapes) may have MoE-specific occupancy headroom. Worth a MoE-targeted reprofile.

---

*All shelved per the host handover - experiments parked. Pick up from the linked result docs in this
directory.*
