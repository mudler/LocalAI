# A-build: hybrid per-head f32/bf16 SSM state - BUILD PROGRESS

Label: A-build (GPU agent). Base: DGX `~/llama-paged-dev` branch `paged` HEAD 2f4f5ab (patch 0025),
plus `BF16_SSM_STATE.diff` applied as the bf16 plumbing base. Goal: per-head mixed-dtype SSM state
(f32 long-memory heads, bf16 fast heads); default `ssm_hybrid_tau_thresh=inf` (all-f32, bit-exact).

## Design recap (from SPEEDUP_HUNT.md A-hybrid-design)
- Classifier (host, model-load): tau_h = 1/(|ssm_a[il][h]| * softplus(ssm_dt[il][h])); f32 if tau_h>T.
  ssm_a = SSM_A_NOSCAN = -exp(A_log) (verified qwen35.cpp:376). ssm_dt = SSM_DT bias.
- Split cache: per GDN layer, s_l (f32, n_f32 heads) + s_l_bf16 (bf16, n_bf16 heads). head_slot map.
- Kernel: ONE kernel templated +HYBRID; per-block (h_idx) branch on head_slot (uniform, no divergence).
  Recurrence math byte-for-byte f32-register, untouched. Homogeneous (HYBRID=false) path bit-exact.
- Op: extra src[8]=state_bf16, src[9]=head_slot; backend detects hybrid = (src[9]!=null).
- CPU mirror: per-head partition read.
- test-backend-ops: MIXED case (some heads f32, some bf16) output-append, decode+prefill+keep_rs_t.

## DE-RISK GATE (must pass before sweep)
1. test-backend-ops GATED_DELTA_NET mixed PASS (CUDA mixed vs CPU mixed).
2. T_thresh=inf greedy md5 == 0023 baseline: dense 5951a5b4d624ce891e22ab5fca9bc439,
   MoE 07db32c2bcb78d17a43ed18bc22705cd.

## KNOB SEMANTICS (IMPORTANT - brief endpoint wording corrected)
Rule (brief verbatim + physics + "start 32-64" guidance all agree): a head is kept f32 iff
tau_h > T_thresh, else bf16. tau_h = 1/(|ssm_a|*softplus(ssm_dt)) in tokens. Long-memory (large tau)
heads stay f32 (bf16 rounding does not contract there -> KL); fast (small tau) heads -> bf16.
- ssm_hybrid_tau_thresh DEFAULT = 0.0  => every tau>0 -> ALL F32 (bit-exact opt-out; the gate runs here).
- ssm_hybrid_tau_thresh -> +inf        => ALL BF16 (shelved mode).
- sweep: raise T (16/32/64/128 tokens) to bf16 progressively more (longer-memory) heads = more speed.
NOTE: the brief's "inf=>all-f32, 0=>all-bf16" sentence is INVERTED vs the operative rule it states
("keep f32 if tau>T") and vs "start 32-64" + the physics. Correct endpoints: 0=all-f32, inf=all-bf16.
Implemented the physically-correct rule; default 0.0 = bit-exact all-f32.

## STATUS
- [x] ggml.h/ggml.c hybrid op builders
- [x] gated_delta_net.cu hybrid kernel + dispatch (one kernel, +HYBRID template, uniform per-block branch)
- [x] ops.cpp CPU hybrid read mirror (output-append; ids in-place is GPU-only, asserted)
- [x] test-backend-ops mixed case (32 cases: hc 4/8 x hs 64/128 x decode/prefill/keep_rs_t x kda)
- [x] de-risk gate 1: test-backend-ops GATED_DELTA_NET = 84/84 PASS (incl 32 hybrid mixed CUDA-vs-CPU)
- [x] cparam/CLI ssm_hybrid_tau_thresh plumbing (default 0.0; threaded context->cparams->memory->ctors)
- [x] memory-recurrent split cache + classifier (validated: real tau split, correct 2-partition layout)
- [x] delta-net-base hybrid op build (fused ids decode + bf16 rs_zero/extra mirror)
- [x] full build clean (sm_121; llama-completion/batched-bench/perplexity/test-backend-ops)
- [x] de-risk gate 2 (default/all-f32 md5 == 0023 both models, re-verified post-build)
- [x] hybrid-ON decode FIXED: the incoherence was head_slot being zeroed by clear(data=true) (whole-RS
      buffer clear) after warm-up, never re-uploaded => every head -> f32-local-0 => split collapse.
      Fix = persist head_slot_host + re-upload via upload_head_slots() after every buffer clear. Hybrid
      decode now coherent; cross-op carry verified BYTE-EXACT (write==read both partitions).
- [x] A-gatesweep DONE: KL sweep T in {0.25..128} both models, single-seq c1024 (clean carry), drift.
      SHIP GATE FAILS - no T passes MeanKLD<1e-3 AND top-p>=99.5% with meaningful speedup. Premise
      (error concentrates in long-memory heads) REFUTED: KL scales with bf16 COUNT and saturates
      ~0.06/~91% (MoE saturates at the minimal split). Carry byte-exact => genuine bf16 sensitivity,
      not a bug. Throughput lever real: dense +12.4% / MoE +11.5% decode @npl128 at T=128.
- [x] Shipped default-off (f32, bit-exact). De-risk gates re-verified on the clean build (84/84;
      md5 == baseline both models). See A_HYBRID_SSM_RESULTS.md for the full tables.

Committed: DGX paged 33e7c65 (amended); worktree patch 0026 + A_HYBRID_SSM_RESULTS.md + this doc.
