# GDN C32 Slab Phase 10 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Test whether a C=32, `dv_tile=64` slabbed M5-style tensor-core GDN prefill path beats the current C=16 M5 kernel without changing decode.

**Architecture:** Phase 10 is a fork-first, profile-gated GDN prefill experiment. It does not revisit the rejected decode `GDN_NW/GDN_CPW` env grid. The candidate keeps the current tensor-core M5 math shape, slabs the value dimension into two `dv=64` blocks to fit dynamic shared memory, and initially recomputes A/T per slab to prove or reject the geometry before optimizing shared work.

**Tech Stack:** llama.cpp CUDA GDN kernel, GB10 sm_121 CUDA build, Qwen3.6 NVFP4 MoE/dense GGUF, canonical md5/KL gates.

---

## Guardrails

- Keep `GDN_CHUNK_MIN > 1`; decode must never route into the chunked prefill prototype.
- Compare against current M5 (`GDN_TC=5`, `GDN_CHUNK_MIN=64`), not against old sequential GDN.
- Build-vs-build A/B only; do not accept a standalone PoC win.
- Keep the candidate default-off behind an explicit env selector until it clears correctness and performance gates.
- Run canonical md5 gates after any source change:
  - MoE: `8cb0ce23777bf55f92f63d0292c756b0`.
  - Dense: `5951a5b4d624ce891e22ab5fca9bc439`.

## Task 1: Baseline Current M5

**Files:**
- Read-only: `/home/mudler/llama-phase6-source/ggml/src/ggml-cuda/gated_delta_net.cu`
- Artifact: `/home/mudler/bench/phase10_gdn_c32_slab/`

- [x] **Step 1: Check DGX is free**

Run the standard DGX preflight:

```bash
ssh dgx.casa 'set -e
echo docker=$(docker ps -q | wc -l)
echo local_ai_worker=$(docker ps --format "{{.Names}}" | grep -c local-ai-worker || true)
echo compute=$(nvidia-smi --query-compute-apps=pid --format=csv,noheader | sed "/^$/d" | wc -l)
if [ -f ~/gpu_bench_lock/owner ]; then cat ~/gpu_bench_lock/owner; else echo FREE-no-lock-file; fi'
```

Expected:

```text
docker=0
local_ai_worker=0
compute=0
FREE...
```

- [x] **Step 2: Record current source provenance**

Run:

```bash
ssh dgx.casa 'cd /home/mudler/llama-phase6-source && git status --short && git rev-parse HEAD'
```

Expected: clean or only the current phase commit.

- [x] **Step 3: Run current M5 prefill baseline**

Run MoE and dense prefill at `npp=512` and `npp=2048` with:

```bash
LLAMA_KV_PAGED=1 LLAMA_MOE_FORCE_GRAPHS=1 GDN_TC=5 GDN_CHUNK_MIN=64 GGML_NO_BACKTRACE=1
```

Record S_PP, kernel bucket summaries, and artifacts under:

```text
/home/mudler/bench/phase10_gdn_c32_slab/m5_baseline/
```

Result:

| Model | PP | TG | B | S_PP t/s | S_TG t/s | S t/s |
|-------|----|----|---|----------|----------|-------|
| MoE | 512 | 4 | 32 | 2314.18 | 359.16 | 2220.48 |
| MoE | 2048 | 4 | 32 | 2439.95 | 389.43 | 2415.16 |
| Dense | 512 | 4 | 32 | 978.97 | 143.56 | 936.71 |
| Dense | 2048 | 4 | 32 | 1023.61 | 184.09 | 1014.59 |

Artifacts:

- `/home/mudler/bench/phase10_gdn_c32_slab/m5_baseline/paged_moe_prefill.txt`
- `/home/mudler/bench/phase10_gdn_c32_slab/m5_baseline/paged_dense_prefill.txt`
- `/home/mudler/bench/phase10_gdn_c32_slab/m5_baseline/summary_rows.txt`
- `/home/mudler/bench/phase10_gdn_c32_slab/m5_baseline/provenance.txt`

## Task 2: Add Default-Off C32 Slab Candidate

**Files:**
- Modify: `/home/mudler/_git/llama.cpp/ggml/src/ggml-cuda/gated_delta_net.cu`
- Mirror: `/home/mudler/llama-phase6-source/ggml/src/ggml-cuda/gated_delta_net.cu`

### Source Inspection Result

- [x] **Step 0: Check whether C32 can reuse the current M5 body**

Result: no safe launcher-only shortcut exists for C32 M5.

The current M5 code path is structurally specialized to `C<=16` in the form-T
solve/apply stage:

- `gated_delta_net_chunked_cuda<S_v, C, TC>` stores the full `U=T*RHS`
  output in registers before overwriting `Ud`, avoiding read/write aliasing.
- For `C=16`, one `m16` row tile covers all chunk rows.
- For `C=32`, there are two row tiles. Writing the first tile to `Ud` before
  computing the second would corrupt the RHS reads for the second tile.
- The current code also calls the apply helper with rowbase `0` only in the M5
  solve path, so a naive `launch_gdn_chunked<128, 32, TC=4>` would be
  incomplete even if dynamic shared memory fit.

Implication:

- Do not add `GDN_C32_SLAB=1` by only changing launch dimensions.
- A correct C32 slab patch must first add a separate `U=T*RHS` staging strategy:
  either a slab-local temporary buffer for all `C*DV_TILE` U values, or a
  two-pass apply that preserves the original RHS until all row tiles are
  computed.
- Because the candidate changes the solve/apply mechanics, it requires a
  focused `GATED_DELTA_NET` op gate before any prefill A/B.

- [ ] **Step 1: Add an explicit env selector**

Use an env var such as:

```text
GDN_C32_SLAB=1
```

The default path must stay current M5.

- [ ] **Step 2: Introduce a C=32, dv_tile=64 launch**

Target shape:

```cpp
launch_gdn_chunked_slab<128, 32, 64, TC_>(...)
```

Initial prototype rules:

- one slab block per `(head, seq, dv_tile)`,
- two slab blocks cover `dv=128`,
- recompute `A/T` per slab for simplicity,
- no decode routing,
- no D2H synchronization.

- [ ] **Step 3: Build on DGX**

Run:

```bash
ssh dgx.casa 'cd /home/mudler/llama-phase6-source/build-cuda && cmake --build . --target llama-completion test-backend-ops -j 8'
```

Expected: build succeeds.

## Task 3: Correctness Gates

**Files:**
- Artifact: `/home/mudler/bench/phase10_gdn_c32_slab/gates/`

- [ ] **Step 1: Run `GATED_DELTA_NET` op gate**

Run default and forced C32 slab modes:

```bash
./test-backend-ops test -b CUDA0 -o GATED_DELTA_NET -j 1
GDN_C32_SLAB=1 ./test-backend-ops test -b CUDA0 -o GATED_DELTA_NET -j 1
```

Required coverage to inspect in logs:

- multi-chunk,
- tail chunk,
- multi-seq,
- GQA,
- permuted layout,
- adversarial decay.

- [ ] **Step 2: Run canonical md5 gates**

Run MoE and dense greedy gates with and without `GDN_C32_SLAB=1`.

Expected:

```text
MoE   8cb0ce23777bf55f92f63d0292c756b0
Dense 5951a5b4d624ce891e22ab5fca9bc439
```

- [ ] **Step 3: Run KL gate if md5 changes**

If the C32 slab path changes reduction order and therefore md5, stop and run the
existing KL procedure from `PAGED_BITEXACT_NOTE.md`. Keep the patch only if the
new path is KL-benign and no worse than current M5.

## Task 4: Performance A/B

**Files:**
- Artifact: `/home/mudler/bench/phase10_gdn_c32_slab/ab/`

- [ ] **Step 1: Run C32 slab prefill at `npp=512`**

Compare:

```text
baseline: GDN_TC=5 GDN_CHUNK_MIN=64
candidate: GDN_TC=5 GDN_CHUNK_MIN=64 GDN_C32_SLAB=1
```

Pass: candidate beats current M5 S_PP outside noise.

- [ ] **Step 2: Run C32 slab prefill at `npp=2048`**

Use the same A/B. Pass requires a net S_PP improvement or a clear GDN bucket
reduction without a larger regression elsewhere.

- [ ] **Step 3: Reject if duplicated A/T work cancels the state-traffic win**

If the candidate only shifts time between A/T recomputation and state traffic
without a net win, save the diff as a rejected artifact and update this plan.

## Task 5: Mirror or Reject

**Files:**
- Create if accepted: `backend/cpp/llama-cpp-localai-paged/patches/paged/0055-...patch`
- Modify: `backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md`

- [ ] **Step 1: Commit accepted fork patch**

Commit only after correctness and performance gates pass.

- [ ] **Step 2: Generate LocalAI patch**

Use `git format-patch`; do not hand-edit the generated patch.

- [ ] **Step 3: Update docs**

Record exact artifacts, md5/KL results, and performance decision.
