# Small-M MoE MMQ Phase 32 Spec

## Problem

Phase 30 proved n128 serving feeds grouped-MMQ with small decode-like
per-expert shapes (`ncols_max <= 128`, density `1-4`, selected `mmq_x <= 64`).
Phase 31 proved the obvious launch-policy shortcut is not the issue: in live
n128 serving, all traced decode-like and prefill-like launch lines had
`fixup=0` and `stream_k_blocks == ntiles_dst`.

The remaining grouped-MMQ gap is therefore structural small-M kernel shape:
the kernel is already launched without fixup overhead, but the work inside each
expert tile still pays for padded, low-density token columns.

## Constraints

- Preserve default behavior unless an explicit experimental env/build knob is
  set.
- Keep the patch stack incremental: add helpers or alternate launch branches
  instead of rewriting existing MMQ templates.
- Prefer host-side selection shortcuts and small helper functions over broad
  template refactors, to reduce upstream conflict risk.
- Every source change must be gated by:
  - `test-cuda-mmq-shape-trace` or a new host/unit test for selector behavior.
  - DGX CUDA build of `llama-server`, `llama-completion`, `test-backend-ops`.
  - Default-off MoE md5 `8cb0ce23777bf55f92f63d0292c756b0`.
  - Default-off dense md5 `5951a5b4d624ce891e22ab5fca9bc439`.
  - `MUL_MAT_ID` `806/806`.
  - Trace/knob-enabled md5/op gate when the experiment is expected to be
    numerically identical.

## Rejected By Evidence

- No-fixup/no-stream-k shortcut: Phase 31 n128 serving had decode-like
  `4800/4800` and prefill-like `4920/4920` launch lines with `fixup=0` and
  `stream_k_blocks == ntiles_dst`.
- Build-time MMQ occupancy shortcuts: Phase 28 rejected `GGML_CUDA_FP4_MINBLOCKS=2`
  as slower and `GGML_CUDA_FP4_MMQ_Y=64` as compile-invalid for NVFP4 writeback.

## Candidate Directions

### A. Exact Expert Histogram Trace

Add a default-off diagnostic that records exact per-expert segment lengths after
`expert_bounds` is available. This requires care because device-to-host readback
can synchronize the stream and perturb serving; it should run only in a
standalone diagnostic path, never in normal serving gates.

Use this only if selector estimates are insufficient for designing the next
kernel.

### B. Decode-Only Alternative Small-M Kernel Hook

Add an opt-in branch for grouped MoE NVFP4 decode-like shapes:

- `args.expert_bounds != nullptr`
- `type == GGML_TYPE_NVFP4`
- `args.ncols_max <= 128`
- estimated density `<= 4`
- selected `mmq_x <= 64`

The first implementation should be a compile-time skeleton or dispatch counter,
not a numeric kernel, unless the exact implementation can be tested against
`MUL_MAT_ID` in isolation. The gate is a new `test-backend-ops` case covering
ragged MoE decode shapes before serving A/B.

### C. W4A16 / Marlin-Style Decode Probe

Re-use the existing W4A16 scaffolding only as a separately gated probe. Prior
decode W4A16 work was rejected as bandwidth-bound, while prefill remains the
higher-EV W4A16 target. Do not mix this with the small-M MMQ branch unless a
new in-backend A/B shows decode benefit.

## Recommended Phase 32 Deliverable

Do not jump straight to a large kernel. The next deliverable should be a small,
default-off dispatch classification patch:

1. Factor the Phase 30/31 decode-like predicate into a host helper.
2. Add a test proving the helper selects only small-M grouped MoE NVFP4 shapes
   and excludes prefill.
3. Add a bounded log/counter prefix such as `[LLAMA_MOE_MMQ_SMALL_M]` under the
   existing trace knob or a more specific `LLAMA_MOE_MMQ_SMALL_M_TRACE`.
4. Re-run n128 serving to verify the candidate branch population before any
   numeric kernel work.

This keeps the next patch additive, md5-safe, and low-conflict while giving a
hard count for the future structural branch.

## Subagent Findings Folded In

- llama.cpp path: `ggml_cuda_mul_mat_id` routes quantized MoE to grouped MMQ via
  `ggml_cuda_should_use_mmq`; `mmq_args` carries `expert_bounds`, `ids_dst`,
  `ncols_dst=ne12*n_expert_used`, `nchannels_x=ne02`, and `ncols_max=ne12`.
- The tile selector in `mul_mat_q_case` is the correct low-conflict hook:
  `LLAMA_MOE_MMQ_X`, `LLAMA_MOE_AUTO_TILE`, `LLAMA_MOE_DECODE_TILE`, and
  `LLAMA_MOE_DENSITY_MAX` already prove this branch can be changed host-side.
- vLLM's useful GB10-compatible idea is small expert `block_size_m` selection
  (`8/16` for low-density routed rows), not TMA/tcgen05/Triton/CUTLASS paths.
- Phase 32 should therefore add a default-off candidate classifier and trace,
  then use the measured candidate count to decide whether to A/B `mmq_x=8/16`.
