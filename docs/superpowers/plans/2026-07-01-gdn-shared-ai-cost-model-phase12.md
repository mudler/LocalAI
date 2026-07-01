# GDN Shared-A/Ai Cost Model Phase 12 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Decide whether a shared-A/Ai C32 GDN design is worth implementing on GB10 before touching llama.cpp source.

**Architecture:** Phase 12 is analysis-first and docs-only unless the cost model proves a credible win. It extracts model dimensions, computes dynamic-smem and global-scratch pressure, estimates traffic saved versus traffic added, and writes a go/no-go decision for a possible Phase 13 global-scratch prototype.

**Tech Stack:** llama.cpp CUDA GDN kernel geometry, vLLM/FLA chunked GDN references, DGX GB10 benchmark artifacts, LocalAI parity docs.

---

## Guardrails

- Do not edit llama.cpp source in this phase.
- Do not generate a LocalAI patch file in this phase.
- Treat Phase 10 and Phase 11 as rejected; do not reopen C32 slab or QS-early.
- Use actual model metadata where available; if a dimension is inferred, mark it
  as inferred.
- The output is a go/no-go decision, not an implementation patch.

## Task 1: Gather Current Evidence

**Files:**
- Read: `/home/mudler/_git/llama.cpp/ggml/src/ggml-cuda/gated_delta_net.cu`
- Read: `/home/mudler/_git/vllm/vllm/model_executor/layers/fla/ops/chunk.py`
- Read: `/home/mudler/_git/vllm/vllm/model_executor/layers/fla/ops/solve_tril.py`
- Read: `/home/mudler/_git/vllm/vllm/model_executor/layers/fla/ops/wy_fast.py`
- Read: `backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md`
- Artifact: `/home/mudler/bench/phase12_gdn_shared_ai_cost_model/`

- [x] **Step 1: Check tree state**

Run:

```bash
git -C /home/mudler/_git/llama.cpp status --short
git -C /home/mudler/_git/LocalAI/.claude/worktrees/feat+paged-attention status --short
```

Expected:

- llama.cpp fork is clean.
- LocalAI worktree only has this Phase 12 docs work and untracked `.claude/`.

- [x] **Step 2: Create artifact directory**

Run:

```bash
ssh dgx.casa 'mkdir -p /home/mudler/bench/phase12_gdn_shared_ai_cost_model'
```

Expected: command exits 0.

- [x] **Step 3: Record reference function map**

Record these llama.cpp insertion points in the result doc:

```text
/home/mudler/_git/llama.cpp/ggml/src/ggml-cuda/gated_delta_net.cu
  gated_delta_net_chunked_cuda
  launch_gdn_chunked
  launch_gated_delta_net
  ggml_cuda_op_gated_delta_net
```

Record these vLLM reference functions:

```text
/home/mudler/_git/vllm/vllm/model_executor/layers/fla/ops/chunk.py
  chunk_gated_delta_rule_fwd
/home/mudler/_git/vllm/vllm/model_executor/layers/fla/ops/solve_tril.py
  solve_tril
  solve_tril_16x16_kernel
  merge_16x16_to_32x32_inverse_kernel
  merge_16x16_to_64x64_inverse_kernel
/home/mudler/_git/vllm/vllm/model_executor/layers/fla/ops/wy_fast.py
  recompute_w_u_fwd
```

Result: recorded in
`backend/cpp/llama-cpp-localai-paged/docs/GDN_SHARED_AI_COST_MODEL.md`.

## Task 2: Extract Model Dimensions

**Files:**
- Artifact: `/home/mudler/bench/phase12_gdn_shared_ai_cost_model/model_metadata.txt`

- [x] **Step 1: Extract GGUF metadata**

Run on DGX:

```bash
ssh dgx.casa 'cd /home/mudler/llama-phase6-source/build-cuda/bin
{
  echo "=== MoE ==="
  ./llama-show-info -m /home/mudler/bench/q36-35b-a3b-nvfp4.gguf 2>/dev/null || ./llama-cli --show-info -m /home/mudler/bench/q36-35b-a3b-nvfp4.gguf -n 0 2>/dev/null || true
  echo "=== Dense ==="
  ./llama-show-info -m /home/mudler/bench/q36-27b-nvfp4.gguf 2>/dev/null || ./llama-cli --show-info -m /home/mudler/bench/q36-27b-nvfp4.gguf -n 0 2>/dev/null || true
} > /home/mudler/bench/phase12_gdn_shared_ai_cost_model/model_metadata.txt'
```

Expected: metadata file contains head count, layer count, and head dimension
or enough tensor metadata to infer them.

Result:

- Metadata artifact:
  `/home/mudler/bench/phase12_gdn_shared_ai_cost_model/model_metadata.txt`.
- `llama-show-info` was not present in the DGX build, so a minimal read-only
  GGUF metadata parser was used.

- [x] **Step 2: Summarize GDN dimensions**

Write a short table in the result doc:

```text
Model | GDN layers | H | S_v | benchmark npl | npp | chunks at BT=32 | chunks at BT=64
```

Use benchmark shapes:

- `npl=32`
- `npp=512,2048`
- `S_v=128`

If H cannot be read directly from metadata, infer it from source/model docs and
mark the row as inferred.

Result:

| Model | GDN layers | H | S_v | benchmark npl | npp | chunks at BT=32 | chunks at BT=64 |
|-------|------------|---|-----|---------------|-----|-----------------|-----------------|
| MoE | 30 inferred | 32 inferred | 128 | 32 | 512 | 16 | 8 |
| MoE | 30 inferred | 32 inferred | 128 | 32 | 2048 | 64 | 32 |
| Dense | 48 inferred | 48 inferred | 128 | 32 | 512 | 16 | 8 |
| Dense | 48 inferred | 48 inferred | 128 | 32 | 2048 | 64 | 32 |

`H = ssm.inner_size / ssm.state_size`.

## Task 3: Compute Smem and Scratch Costs

**Files:**
- Create: `backend/cpp/llama-cpp-localai-paged/docs/GDN_SHARED_AI_COST_MODEL.md`

- [x] **Step 1: Record dynamic-smem formulas**

Use:

```text
C16 full-width current M5:
  floats = S_v*S_v + 2*C*S_v + S_v*C + C*C + 3*C + 2*C*C

C32 full-width:
  floats = S_v*S_v + 2*C*S_v + S_v*C + C*C + 3*C + 2*C*C

C32 slab64 with U staging:
  floats = S_v*64 + 2*C*S_v + 64*C + C*C + 3*C + 2*C*C + 64*C
```

Expected values for `S_v=128`:

```text
C16 full-width:  93,376 B / 91.19 KiB
C32 full-width: 127,360 B / 124.38 KiB
C32 slab64:      94,592 B / 92.38 KiB
```

- [x] **Step 2: Record Ai scratch formulas**

Use:

```text
Ai scratch bytes = npl * H * ceil(npp / BT) * BT * BT * sizeof(dtype)
```

Compute for:

- `BT=32`, f32 and f16/bf16 Ai.
- `BT=64`, f32 and f16/bf16 Ai.
- `npp=512` and `npp=2048`.

- [x] **Step 3: Estimate extra global traffic**

For a two-slab C32 design, estimate:

```text
Ai write once = npl * H * nchunks * BT * BT * sizeof(Ai)
Ai read per slab = 2 * Ai write once
total Ai traffic = 3 * Ai write once
```

Record the estimate in MiB for every benchmark shape.

- [x] **Step 4: Estimate work saved**

Record that shared Ai saves duplicated A/T construction per second slab:

```text
saved per chunk/head = one KK/QK-derived A/T solve/apply setup currently duplicated by C32 slab
not saved = KS, QS, U, P*U, state update, state traffic
```

Do not claim a speedup from this estimate alone. The result doc must say whether
the saved work is large enough to justify the scratch traffic and kernel
boundary risk.

Result: recorded in
`backend/cpp/llama-cpp-localai-paged/docs/GDN_SHARED_AI_COST_MODEL.md`.
The f32 `BT=32` scratch path costs 256 MiB (MoE) and 384 MiB (dense) at
`npp=2048,npl=32`, with 768 MiB and 1.125 GiB of Ai traffic respectively.

## Task 4: Go/No-Go Decision

**Files:**
- Modify: `backend/cpp/llama-cpp-localai-paged/docs/GDN_SHARED_AI_COST_MODEL.md`
- Modify: `backend/cpp/llama-cpp-localai-paged/docs/VLLM_PARITY_LEVER_MAP.md`
- Modify: `backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md`

- [x] **Step 1: Write the decision**

Use one of these exact decisions:

```text
GO: Phase 13 should implement a default-off global-Ai scratch prototype.
```

or:

```text
NO-GO: shared-A/Ai scratch is not credible on GB10; stop GDN kernel work here.
```

The decision must cite the scratch size and Ai traffic estimates.

Decision:

```text
GO: Phase 13 should implement a default-off global-Ai scratch prototype.
```

Rationale: the scratch/traffic cost is high enough to require strict gates, but
not high enough to reject without a default-off prototype.

- [x] **Step 2: If GO, write Phase 13 scope**

If GO, create:

```text
docs/superpowers/specs/2026-07-01-gdn-global-ai-prototype-design.md
docs/superpowers/plans/2026-07-01-gdn-global-ai-prototype-phase13.md
```

The Phase 13 plan must include:

- default-off env selector,
- scratch allocation strategy,
- op gate,
- canonical MoE/dense md5 gates,
- same-session A/B,
- rejection path.

Result:

- `docs/superpowers/specs/2026-07-01-gdn-global-ai-prototype-design.md`.
- `docs/superpowers/plans/2026-07-01-gdn-global-ai-prototype-phase13.md`.

- [x] **Step 3: If NO-GO, update final records**

If NO-GO, update:

- `VLLM_PARITY_FINAL.md`
- `PARITY_HANDOFF.md`

Record that GDN kernel work on GB10 is exhausted by evidence, not assumption.

Result: not applicable because Phase 12 is GO. The final/handoff records are
not changed to close GDN work.

## Task 5: Verification and Commit

**Files:**
- Modify/create the files from Task 4.

- [x] **Step 1: Verify docs**

Run:

```bash
git diff --check
git status --short
```

Expected:

- no whitespace errors,
- only intended docs are modified plus untracked `.claude/`.

Result:

- `git diff --check` exited 0.
- `/home/mudler/_git/llama.cpp` was clean.
- DGX metadata artifact existed and contained MoE/dense GGUF metadata.

- [x] **Step 2: Commit docs**

For GO:

```bash
git add backend/cpp/llama-cpp-localai-paged/docs/GDN_SHARED_AI_COST_MODEL.md \
  backend/cpp/llama-cpp-localai-paged/docs/VLLM_PARITY_LEVER_MAP.md \
  backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md
git add -f docs/superpowers/specs/2026-07-01-gdn-global-ai-prototype-design.md \
  docs/superpowers/plans/2026-07-01-gdn-global-ai-prototype-phase13.md \
  docs/superpowers/plans/2026-07-01-gdn-shared-ai-cost-model-phase12.md
git commit -m "docs(paged): scope GDN shared-Ai prototype" \
  -m "Assisted-by: Codex:gpt-5"
```

For NO-GO:

```bash
git add backend/cpp/llama-cpp-localai-paged/docs/GDN_SHARED_AI_COST_MODEL.md \
  backend/cpp/llama-cpp-localai-paged/docs/VLLM_PARITY_LEVER_MAP.md \
  backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md \
  backend/cpp/llama-cpp-localai-paged/docs/VLLM_PARITY_FINAL.md \
  backend/cpp/llama-cpp-localai-paged/docs/PARITY_HANDOFF.md
git add -f docs/superpowers/plans/2026-07-01-gdn-shared-ai-cost-model-phase12.md
git commit -m "docs(paged): close GDN shared-Ai cost model" \
  -m "Assisted-by: Codex:gpt-5"
```
