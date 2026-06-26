# A - HYBRID PER-HEAD f32/bf16 SSM STATE - BUILD + DE-RISK + GATE-SWEEP RESULTS

Label: A-build + A-gatesweep. Lands as patch 0026 on top of 0025 (DGX HEAD 2f4f5ab),
incorporating the bf16-SSM-state plumbing as the base. Built into `~/llama-paged-dev/build-cuda`
(sm_121); committed on the DGX `paged` branch (33e7c65, amended) and as
`patches/paged/0026-qwen35-hybrid-perhead-ssm-state.patch` + this doc in the worktree.

## VERDICT

The hybrid machinery is **CORRECT and complete** (both de-risk gates PASS; the carry is byte-exact;
the previously-open decode-incoherence bug is FIXED). The **ship gate FAILS**: no T_thresh reaches
`MeanKLD < 1e-3 AND Same-top-p >= 99.5%` for both models with any meaningful speedup. The design
premise - that the bf16 KL error concentrates in long-memory heads and is removed by keeping them
f32 at f32-fraction 0.30-0.50 - is **empirically refuted** on q36-27b and q36-35b-a3b-nvfp4: the KL
error scales with the bf16 head COUNT and saturates (~0.06 MeanKLD / ~91% same-top-p) far below any
useful byte-saving. The bf16 byte-saving (and the decode speedup it buys) is real but cannot meet the
strict KL bar. **Shipped default-off (f32, bit-exact opt-out); the hybrid is opt-in only.**

## THE FIX (was: hybrid-ON decode incoherent)

Root cause: `llama_memory_recurrent::clear(data=true)` zeroes the WHOLE recurrent backend buffer with
`ggml_backend_buffer_clear`, which includes the per-layer `head_slot` maps. Those maps were uploaded
only once in the constructor. llama.cpp calls `clear(true)` to reset state after the warm-up run (and
on context resets), so by the time real prefill/decode runs, every `head_slot[h] == 0`. The kernel
decodes `head_slot==0` as "f32 head, local index 0", so EVERY head reads/writes f32-partition slot 0:
the split collapses (the bf16 partition is never written, every head collides on one f32 slot) and the
output is garbage. Warm-up showed correct values precisely because it ran before the clear.

Fix: persist the host-side maps (`head_slot_host`) and re-upload them after every buffer clear via a
new `upload_head_slots()` (called both at construction and at the end of `clear(true)`). 22 lines in
`src/llama-memory-recurrent.cpp` + 7 in the header. After the fix:
- head_slot reads back correct in every forward (e.g. `0 1 -1 -2`), in both llama-completion and
  llama-perplexity;
- the bf16 partition is written (non-zero) every step;
- the cross-op state carry is **byte-exact**: at a continuation forward the op reads back EXACTLY what
  the prior op wrote, element-for-element, in BOTH partitions (f32 `[0]=0.00303 [1]=-0.00074
  [16384]=0.00054`, bf16 `[0]=-0.00023 [1]=0.00008 [16384]=0.00269` write == read), confirming there
  is no addressing/scramble/corruption bug. The only residual difference from f32 is the bf16 rounding
  of the bf16-partition heads.

## DE-RISK GATES - both PASS (re-verified on the final clean build)

1. **test-backend-ops GATED_DELTA_NET = 84/84 PASS, CUDA0 OK** (incl. the 32 mixed-dtype hybrid cases
   vs CPU: head_count {4,8} x head_size {64,128} x {decode, prefill 33/64/100, keep_rs_t K=4} x kda).
2. **T=0 (default, all-f32) greedy md5 == 0023 baseline, both models**, NO `--ssm-bf16-tau`:
   - dense q36-27b-nvfp4: `5951a5b4d624ce891e22ab5fca9bc439` == baseline
   - MoE   q36-35b-a3b-nvfp4: `07db32c2bcb78d17a43ed18bc22705cd` == baseline
   The bit-exact opt-out is preserved byte-for-byte.

## SHIP GATE - the KL/throughput sweep (FAILS)

KL harness = the bf16-work GateBench: `llama-perplexity --kl-divergence` on wikitext-2-raw,
`-ngl 99 -fa on --seed 1`, base = T=0 (f32). The clean carry config is single-sequence
`-b 1024 -ub 512 -c 1024 --chunks 8` (one cross-ubatch bf16 round-trip; f32-vs-f32 floor = 100.000%
same-top-p, MeanKLD ~ -1.2e-5). Gate: `MeanKLD < 1e-3 AND Same-top-p >= 99.5% AND bounded drift`.

### Dense q36-27b-nvfp4 (H_v=48), c1024 single-seq

| T_thresh | bf16 heads | f32-frac | f_bytes | MeanKLD  | Same-top-p |
|---------:|-----------:|--------:|--------:|---------:|-----------:|
| 0.25     | 14         | 0.964   | 0.982   | 0.00270  | 98.92%     |
| 0.5      | 48         | 0.963   | 0.982   | 0.01439  | 96.18%     |
| 1        | 118        | 0.935   | 0.968   | 0.06357  | 91.59%     |
| 8        | ~610       | 0.735   | 0.868   | 0.05669  | 91.59%     |
| 32       | ~1113      | 0.517   | 0.759   | 0.05724  | 90.97%     |
| 64       | ~1304      | 0.434   | 0.717   | 0.06183  | 91.85%     |
| 128      | ~1460      | 0.366   | 0.683   | 0.05980  | 91.56%     |

Monotonic below the knee (T<=1), then a flat plateau. Best meaningful point T=0.25 (only ~1.8% byte
saving) already FAILS both criteria (KLD 0.0027 > 1e-3; top-p 98.92% < 99.5%). To pass the gate the
bf16 count must be < ~14 heads (f_bytes > 0.98) => no speedup.

### MoE q36-35b-a3b-nvfp4 (H_v=32), c1024 single-seq

| T_thresh | bf16 heads | f32-frac | f_bytes | MeanKLD  | Same-top-p |
|---------:|-----------:|--------:|--------:|---------:|-----------:|
| 0.25     | 23         | 0.940   | 0.970   | 0.03907  | 91.61%     |
| 0.5      | 53         | 0.928   | 0.964   | 0.04620  | 90.31%     |
| 1        | 78         | 0.910   | 0.955   | 0.04425  | 89.82%     |
| 32       | 585        | 0.391   | 0.695   | 0.04552  | 90.09%     |

MoE has NO low-KL regime: even the minimal split (23 bf16 heads, ~3% byte saving) is already at the
~0.045 / ~91% plateau. Fails the gate everywhere by a wide margin.

### Why it fails (the refutation)

The carry is byte-exact, so this is genuine bf16 rounding of the recurrent state, not a bug. The
gated-DeltaNet logit is extremely sensitive to ANY perturbation of the temporal state: even rounding a
handful of small-magnitude heads to bf16 flips ~9% of hard-wikitext argmaxes, and adding more bf16
heads does not flip materially more (saturation - the flips concentrate in an inherently-marginal
token pool). This matches the prior whole-bf16 finding (MeanKLD 0.05-0.17, top-p ~90%, "bounded but
LARGE, plateaus with context"). The error is NOT concentrated by tau, so f32-ing the long-memory heads
(or, tested, the fast heads - inverted classifier gives the same plateau) does not recover the gate.

## THROUGHPUT - the byte-saving lever IS real (but KL-gated out)

`llama-batched-bench -fa on -npp 128 -ntg 128 -npl 128`, `LLAMA_KV_PAGED=1`, decode_agg = S_TG t/s:

| model | T=0 (f32) | T=128 (f_bytes ~0.68) | gain   |
|-------|----------:|----------------------:|-------:|
| dense | 529.0     | 594.4                 | +12.4% |
| MoE   | 1110.7    | 1238.1                | +11.5% |

So the split delivers the predicted recurrence-bandwidth win (~+12% decode at f_bytes ~0.68), but only
at T values whose KL is ~0.06 / ~91% top-p. There is no operating point with both a real speedup and a
passing KL.

## RECOMMENDATION

- Ship 0026 as-is: **default `ssm_hybrid_tau_thresh = 0.0` (f32, bit-exact)**; the hybrid is opt-in via
  `--ssm-bf16-tau` for callers who explicitly accept reduced precision for ~+12% decode. Do NOT put a
  hybrid T in the gallery/recommended config - it does not pass the KL bar.
- Lever A is closed as a KL-passing speedup: the GDN recurrent state does not tolerate bf16 on a
  head-subset basis. Speed beyond the f32 recurrence must come from elsewhere (the MoE FP4 GEMM /
  re-graph levers, or NVFP4-dense quant), not from bf16-ing the SSM state.
- If a product later accepts a looser bar (e.g. top-p >= 95%), dense T=0.5 (96.18%, f_bytes 0.982) is
  the only near-miss and buys ~2% - still not worth it; MoE has nothing.

Assisted-by: Claude:opus-4.8 [Claude Code]
