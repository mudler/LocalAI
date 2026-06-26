# A - HYBRID PER-HEAD f32/bf16 SSM STATE - BUILD + DE-RISK RESULTS

Label: A-build (the GPU build agent). Lands as patch 0026 on top of 0025 (DGX HEAD 2f4f5ab),
incorporating the bf16-SSM-state plumbing (`BF16_SSM_STATE.diff`) as the base. Built into
`~/llama-paged-dev/build-cuda` (sm_121); committed on the DGX `paged` branch (657e008) and as
`patches/paged/0026-qwen35-hybrid-perhead-ssm-state.patch` + this doc in the worktree.

## DE-RISK GATE - both required gates PASS

### Gate 1: test-backend-ops MIXED GATED_DELTA_NET (CUDA mixed vs CPU mixed)
`./bin/test-backend-ops -o GATED_DELTA_NET -b CUDA0` = **84/84 PASS, CUDA0 OK**. This includes the
**32 new hybrid mixed-dtype cases** (`test_gated_delta_net_hybrid`): head_count {4,8} x head_size
{64,128} x {single-token decode, multi-token prefill 33/64/100, keep_rs_t K=4} x kda {0,1}, with an
interleaved head_slot map (even heads f32, odd heads bf16) so both partition branches are exercised
across blocks. CUDA mixed vs CPU mixed agree. (Plus the pre-existing 52 f32 + bf16 cases still pass.)

### Gate 2: T_thresh=inf (default, all-f32) greedy md5 == 0023 baseline - BOTH MODELS
`llama-completion -ngl 99 -fa on -p "The capital of France is" -n 48 --temp 0 --seed 1`, NO
`--ssm-bf16-tau` flag (default 0.0 => every head f32 => no split => the existing single-cache path):
- dense q36-27b-nvfp4: `5951a5b4d624ce891e22ab5fca9bc439` == 0023 baseline.
- MoE   q36-35b-a3b-nvfp4: `07db32c2bcb78d17a43ed18bc22705cd` == 0023 baseline.
Re-verified byte-identical AFTER the full build with every plumbing edit in place. **The bit-exact
opt-out is preserved.**

## KNOB SEMANTICS (brief endpoint wording corrected)
`ssm_hybrid_tau_thresh` / `--ssm-bf16-tau` T: a gated-DeltaNet head is kept **f32 iff tau_h > T**,
else bf16. `tau_h = 1/(|ssm_a[il][h]| * softplus(ssm_dt[il][h]))` tokens (ssm_a = SSM_A_NOSCAN =
-exp(A_log), verified qwen35.cpp:376; ssm_dt = SSM_DT bias). This is the brief's operative rule + the
"start 32-64" guidance + the physics (long-memory/large-tau heads stay f32; fast/small-tau heads ->
bf16). Endpoints:
- **T = 0.0 (DEFAULT) => every tau>0 -> ALL F32 (bit-exact opt-out; the gate runs here).**
- **T -> +inf => ALL BF16 (shelved mode).**
- sweep T in {16,32,64,128} bf16's progressively more (longer-memory) heads = more speed.

NOTE: the brief's "inf=>all-f32, 0=>all-bf16" sentence is INVERTED relative to the rule it states
("keep f32 if tau>T") and to "start 32-64" + the physics. The physically-correct rule is implemented;
the bit-exact all-f32 mode is the DEFAULT (T=0), which is exactly what the de-risk gate exercises.

## What was built (all components, validated correct)
1. **Classifier** (llama-memory-recurrent ctor, host, model-load): reads ssm_a/ssm_dt per GDN layer,
   computes tau_h, sets head_is_bf16. VALIDATED on dense q27 (H_v=48, S_v=128): real per-head tau
   spread min~0.2-0.5 / max~800-26000 tokens; at T=32 the split is ~13-31 f32 / 17-35 bf16 per layer.
   Guarded against the device-memory-fitting pre-pass (weights not yet allocated => data==NULL =>
   fall back to single f32 cache, a conservative/larger memory estimate; real load classifies).
2. **Split cache** (llama-memory-recurrent): per split GDN layer, s_l[il] holds the f32 partition
   [S_v*S_v*n_f32, n_rows] and s_l_bf16[il] the bf16 partition [S_v*S_v*n_bf16, n_rows] + an I32[H]
   head_slot map (local_idx>=0 f32, -(local_idx+1)<0 bf16), uploaded after buffer alloc. ctx metadata
   budget bumped 2->4 tensors/layer (r, s_f32, s_bf16, head_slot). VALIDATED: cache layout correct
   (f32/bf16 partitions 2MB apart, non-overlapping; sizes match counts).
3. **Kernel** (gated_delta_net.cu): ONE kernel templated +HYBRID; per-block (h_idx) branch on
   head_slot picks the partition + local index (uniform within a block => no warp divergence). The
   homogeneous (HYBRID=false) instantiations are byte-identical to before (if constexpr elides the
   hybrid blocks). Two builders: ggml_gated_delta_net_hybrid (output-append, for the test) and
   ggml_gated_delta_net_inplace_ids_hybrid (decode). Backend detects hybrid = src[9]!=null; gathers
   both partitions for non-identity seqs; derives the bf16 in-place dst from src[8]+rs_head.
4. **CPU mirror** (ops.cpp): per-head partition read for the output-append form (the test path).
5. **Plumbing**: cparam ssm_hybrid_tau_thresh threaded llama_context_params -> cparams ->
   llama_memory_params -> recurrent/hybrid/iswa ctors; common_params + CLI --ssm-bf16-tau (default 0).
6. **test-backend-ops**: the 32 mixed cases above.

## KNOWN OPEN ISSUE - hybrid-ON decode is incoherent (opt-in only; does NOT affect the default)
With `--ssm-bf16-tau` > 0 (any split, even tau=1 = a handful of bf16 heads), the model generates
incoherent text ("<think> the the the > EOF"). The bit-exact all-f32 default is UNAFFECTED (gate 2).

Diagnosis (everything reachable by inspection was verified correct):
- The op-level MIXED test PASSES, but it only covers the **output-append** form (state read from the
  s0 input partitions, write to the f32 op output). The model decode uses the **ids in-place** form:
  read from the in-place cache partition (identity), write the new state in place per partition. That
  cross-step state path is NOT exercised by a single-op test (the in-place state write is a side
  effect, not the compared op output), so it is the only un-netted surface - and that is where the bug
  lives.
- Confirmed correct at runtime: the classifier (real tau split), the split cache layout (partitions
  2MB apart, sizes match), and the exact kernel parameters (H=48, S_v=128, n_f32+n_bf16=H, head_slot
  values, ids/state_dst/state_bf16 pointers all sane). The hybrid op IS built and dispatched (not a
  homogeneous fallback). Garbage persists with CUDA graphs disabled, so it is not a graph-capture
  issue. The recurrence math is shared with the (passing) output-append path.
- The bug is therefore in the ids in-place cross-step state handling (identity-d read and/or in-place
  partition store, and/or the bf16 partition rs_zero/extra-states mirroring in delta-net-base) - a
  state-corruption that cascades. It needs a multi-step reproduction (the single-op harness cannot
  catch a cross-step in-place bug; the homogeneous in-place ids op itself has no op test - it was only
  ever validated by model md5).

## NOT ready for the GateSweep yet
The de-risk gates (mixed op test + bit-exact default) BOTH PASS, but the hybrid-ON path must be made
coherent before the T_thresh KL/throughput sweep can produce meaningful numbers. Recommended next
step: build a minimal 2-step in-place reproduction (CPU ids-in-place hybrid mirror + a decode-loop
harness, or a kernel-side state dump comparing hybrid vs homogeneous for an all-f32-disguised split)
to localize identity-d-read vs in-place-store vs the bf16 clear/extra mirror.

Assisted-by: Claude:opus-4.8 [Claude Code]
