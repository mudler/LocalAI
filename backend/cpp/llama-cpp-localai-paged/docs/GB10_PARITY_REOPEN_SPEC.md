# GB10 vLLM Parity Reopen Spec

Status: scoped follow-up. This document intentionally challenges the current
`VLLM_PARITY_FINAL.md` conclusion that GB10 parity is closed. The final record is
still useful as a baseline, but the follow-up work must treat it as a hypothesis
to test, not as a proof of impossibility.

## Goal

Determine whether llama.cpp / ggml can close the remaining GB10 parity gap for
Qwen3.6 NVFP4 hybrid gated-DeltaNet models by porting or adapting concrete vLLM
implementation ideas, while preserving LocalAI's hard correctness gates.

Success means one of two outcomes:

1. A measured, source-backed path improves paged llama.cpp materially toward vLLM
   parity on GB10.
2. The remaining gap is rejected with clean provenance: clean source, clean DGX
   host state, artifact-pinned A/B results, and explicit correctness gates.

## Non-goals

- Do not accept a "closed" conclusion based only on existing docs.
- Do not run long builds or benchmarks without a recorded DGX preflight.
- Do not edit `patches/paged/*.patch` directly. Kernel changes land fork-first in
  `mudler/llama.cpp:localai-paged`, then the LocalAI patch series is regenerated.
- Do not treat a standalone PoC as a result. Every performance claim requires an
  in-backend A/B.
- Do not ship lossy paths default-on. Non-byte-identical paths require KL gates.

## Required Preflight

Before any DGX build, benchmark, or profile:

1. `docker ps` must show no running containers, especially no `local-ai-worker`.
2. `nvidia-smi --query-compute-apps=pid` must show zero compute apps.
3. `~/gpu_bench_lock/owner` must be absent or `FREE*`.
4. Record hostname, git SHA, dirty status, build arch, binary mtimes, model paths,
   benchmark command, and environment variables.

Use `~/_git/llama.cpp` as the local source of truth. DGX source trees are allowed
for builds and artifact inspection, but dirty DGX checkouts must not be treated as
canonical source.

## Evidence From Subagent Audit

Four read-only subagents audited the current state:

- llama.cpp / ggml source audit.
- vLLM source and installed package audit.
- LocalAI patch and docs audit.
- DGX artifact and profile audit.

Their shared conclusion: the final docs are a useful snapshot, but several
claims are broader than the available evidence.

Key findings:

- The strongest unresolved implementation target is W4A16 grouped MoE prefill.
  vLLM uses Marlin W4A16 on GB10, and llama.cpp already has a correct but untuned
  scaffold in `ggml/src/ggml-cuda/w4a16-gemm.cu`.
- The existing W4A16 rejection is a first-implementation failure, not a proof of
  impossibility. The patch header names fixable costs: f32 to bf16 cast pre-pass,
  host tile-map setup, small copies, scalar dequant, and ragged tile waste.
- The 924 t/s paged GPU-steady decode figure is artifact-backed, but the vLLM
  1078 t/s true GPU-steady figure was not found as a self-contained
  ntg16/ntg64 difference-method artifact. Reproduce before relying on the 86%
  claim.
- GDN M5 is real, but M5/M8 provenance is muddy because CDEF records a dirty
  dev-tree M8 commit while docs describe production M5 defaults.
- S3 fixed-period scheduling and fixed-slot padding were rejected, but adaptive
  scheduling remains unproven.

## Candidate Workstreams

### A. Provenance And Baseline Reproduction

Purpose: make later claims defensible.

Tasks:

- Build from clean `~/_git/llama.cpp` `localai-paged` source, or a clean DGX clone
  generated from that source.
- Re-run canonical md5 gates for paged MoE and dense:
  - paged MoE: `8cb0ce23777bf55f92f63d0292c756b0`
  - dense: `5951a5b4d624ce891e22ab5fca9bc439`
- Re-run a short prefill baseline for MoE and dense at `npp=512,2048`.
- Re-run graph-node-traced decode for paged and vLLM using the same
  difference-method shape: `ntg=16` and `ntg=64` at N=128 or N=256.

Gate:

- No implementation work starts until the baseline artifact names, source SHAs,
  and commands are recorded.

### B. W4A16 Grouped MoE Prefill Attack

Purpose: port the vLLM Marlin W4A16 advantage into ggml's in-backend MoE prefill
path.

Current hooks:

- `ggml/src/ggml-cuda/w4a16-gemm.cu`
- `ggml/src/ggml-cuda/w4a16-gemm.cuh`
- `ggml/src/ggml-cuda/ggml-cuda.cu` around `ggml_cuda_mul_mat_id`
- `ggml/src/ggml-cuda/mmq.cu` around `LLAMA_W4A16_PREFILL_M`

Known current costs:

- Separate f32 to bf16 activation cast pass.
- Host-built tile metadata and H2D copies.
- Scalar in-register FP4 to bf16 dequant.
- 4-byte weight staging.
- Ragged expert tile waste.
- Interaction with the generic token-sorting fallback.

Phased experiments:

1. Reconfirm current 0035 W4A16 performance with clean provenance.
2. Remove or fuse the f32 to bf16 activation cast pre-pass.
3. Move tile metadata generation device-side or cache it across repeated shapes.
4. Improve weight staging width and shared-memory layout.
5. Tune tile shapes for ragged per-expert M distribution.
6. Compare against FP4-MMQ and vLLM Marlin buckets with nsys.

Correctness gate:

- `test-backend-ops MUL_MAT_ID` forced W4A16.
- Greedy md5 for unaffected default-off path.
- KL gate for engaged W4A16 path: `KLD(W4A16||f16) <= KLD(FP4-MMQ||f16)`.
- Decode path unchanged when `LLAMA_W4A16_PREFILL_M=0`.

Benchmark gate:

- Beat default FP4-MMQ on MoE `S_PP` at `npp=512` and `npp=2048`.
- No material peak-memory increase.
- No decode regression in the default path.

### C. Native Ragged Grouped FP4-MMA Prefill

Purpose: test whether patch 0034's native FP4-MMA PoC failed due to integration,
not due to the core kernel idea.

Current hooks:

- `ggml/src/ggml-cuda/fp4-gemm.cu`
- `ggml/src/ggml-cuda/fp4-gemm.cuh`
- `LLAMA_FP4_PREFILL_M`

Experiment:

- Build a graph-safe ragged grouped FP4-MMA MoE prefill kernel that avoids the
  per-expert host-sync loop.

Correctness gate:

- Same KL and op gates as W4A16.
- Explicit proof that the per-expert host fallback is not on the hot path.

Benchmark gate:

- Beat current FP4-MMQ or lose decisively enough to close this branch.

### D. GDN Chunked Scan Follow-up

Purpose: compare vLLM's in-tree FLA-derived GDN path against the current M5
implementation without relying on muddy dev-tree artifacts.

Current hooks:

- `ggml/src/ggml-cuda/gated_delta_net.cu`
- `GDN_TC`
- `GDN_CHUNK_MIN`
- existing M5 tensor-core ladder

Phased experiments:

1. Clean A/B: current production M5 against sequential and against recorded M8
   dev-tree behavior.
2. C=32 and C=64 variants.
3. dv slab variants.
4. cp.async staging variants.
5. Register-state variant only if the lower-risk variants show headroom.

Correctness gate:

- `test-backend-ops GATED_DELTA_NET`, including multi-chunk, tail-chunk,
  multi-seq, and adversarial decay cases.
- Greedy md5 per path.
- KL gate for any non-byte-identical path.

Benchmark gate:

- Beat current M5, not just old sequential.
- Preserve decode behavior by keeping `GDN_CHUNK_MIN > 1`.

### E. MoE Weighted Fan-in Fusion

Purpose: remove generic graph-level MoE reduction overhead that vLLM avoids or
amortizes through fused MoE handling.

Current source:

- `src/llama-graph.cpp`, MoE down projection and expert reduction.
- `ggml/src/ggml-cuda/ggml-cuda.cu`, CUDA fusion and MoE support.

Experiment:

- Add a CUDA-specific fused path for `down_experts * weights -> sum expert_used`
  while preserving the current reduction order where required.

Correctness gate:

- Bit-exact for supported shapes, or KL-benign if reduction order changes.
- Handles all `n_expert_used` used by Qwen3.6 MoE.

Benchmark gate:

- Move MoE prefill or decode wall time by more than noise. If it is only a
  2-3% dispatch bucket, record and deprioritize.

### F. Adaptive Serving Scheduler

Purpose: keep S3's decode-window benefit without reproducing its TTFT collapse.

Current hooks:

- `tools/server/server-context.cpp`
- `LLAMA_PAGED_DECODE_STABLE`
- `LLAMA_PAGED_PREFILL_PERIOD`
- existing dynamic prefill budget patches.

Experiment:

- Replace fixed-period prefill deferral with adaptive admission based on live
  decode width, waiting prefill backlog, and TTFT budget.

Correctness gate:

- Serving output correctness unchanged.
- No starvation of prefill requests.

Benchmark gate:

- Improve aggregate throughput or decode throughput at N=128 or N=256 without
  the 2.5x TTFT regression from fixed S3.

### G. Projection And GDN Glue Fusion

Purpose: steal vLLM's `prepare_gdn_attention_core_inputs` idea where ggml still
pays small copy, cat, slice, or unpack kernels.

Current source:

- `src/models/qwen35.cpp`
- `src/models/qwen35moe.cpp`
- `ggml/src/ggml-cuda/ggml-cuda.cu`

Experiment:

- Fuse q/k/v/z unpacking, BA projection preparation, RMSNorm-gated output prep,
  and FP4/FP8 quant prep where the graph pattern is stable.

Correctness gate:

- Per-op tests for new fusion.
- Greedy md5 for model paths.

Benchmark gate:

- Only continue if nsys shows this bucket is material after MoE and GDN work.

## Subagent Plan

Use subagents for independent read, implementation, and review slices. Do not use
subagents to edit the same files in parallel.

Recommended roles by phase:

- Phase 0 source/provenance agent: owns command capture and source SHA checks.
- Phase 0 artifact agent: owns parsing existing and new benchmark artifacts.
- W4A16 kernel agent: owns `w4a16-gemm.*`.
- W4A16 integration agent: owns `ggml-cuda.cu` and `mmq.cu` dispatch plumbing.
- GDN kernel agent: owns `gated_delta_net.cu`.
- Scheduler agent: owns server scheduling files only.
- Reviewer agent: reviews gates, provenance, and whether measured claims match
  artifacts.

Subagent output requirements:

- File paths and functions inspected or changed.
- Exact commands run.
- Exact artifacts produced.
- Pass/fail result against the phase gate.
- Any uncertainty labeled explicitly.

## Phase Order

### Phase 0 - Reproduce And Correct The Record

Do first.

Deliverables:

- Clean source/build provenance.
- Short prefill baseline.
- Graph-node-traced decode difference-method for paged and vLLM.
- Updated docs if the 86% decode claim or CDEF provenance changes.

Exit criteria:

- Baseline is trustworthy enough to judge optimization deltas.

### Phase 1 - W4A16 MoE Prefill

Do second.

Deliverables:

- Reconfirmed current W4A16 baseline.
- At least one targeted W4A16 overhead removal.
- A/B against default FP4-MMQ.

Exit criteria:

- Either W4A16 beats FP4-MMQ and continues, or it is rejected with direct
  artifact-backed evidence.

### Phase 2 - GDN Follow-up

Do after Phase 1 unless Phase 0 proves decode/GDN is the larger immediate gap.

Deliverables:

- Clean M5 vs candidate geometry A/B.
- Correctness gates for all candidate variants.

Exit criteria:

- Keep the best variant or close the branch with measured evidence.

### Phase 3 - MoE Fan-in And Glue Fusions

Do after kernel work identifies remaining non-kernel buckets.

Deliverables:

- nsys-backed bucket selection.
- Fusion implementation only for material buckets.

Exit criteria:

- Keep only fusions that move end-to-end numbers beyond noise.

### Phase 4 - Adaptive Serving

Do after compute kernels are stable.

Deliverables:

- Adaptive scheduling policy.
- Serving A/B at N=8,32,128,256.

Exit criteria:

- Improve serving without TTFT collapse.

## Decision Rules

- Prefer measured in-backend results over source plausibility.
- Prefer small kill-gate experiments over multi-week rewrites.
- Continue a branch only if it beats the current shipped path, not an obsolete
  baseline.
- Document rejected branches with artifact paths so they are not rerun.
- Keep the fork branch canonical and regenerate LocalAI patches from it.

