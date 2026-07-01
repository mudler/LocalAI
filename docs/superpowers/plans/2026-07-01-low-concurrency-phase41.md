# Low Concurrency Phase41 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Quantify the low-concurrency GB10 serving gap after Phase40 rejected the max-concurrency C1 shortcut.

**Architecture:** Reuse the same current-stack serving harness and canonical pre/post inference gates, changing only the concurrency list and llama-server parallel/context sizing.

**Tech Stack:** Bash harness, DGX GB10, llama.cpp `llama-server`, vLLM OpenAI-compatible server, h2h client, `paged-inference-gates.sh`.

---

### Task 1: Define Low-Concurrency Snapshot

**Files:**
- Read: `backend/cpp/llama-cpp-localai-paged/paged-current-serving-snapshot.sh`

- [x] **Step 1: Select the run shape**

Use:

```bash
NPL="1 8 32"
PARALLEL=32
CTX=32768
PTOK=128
GEN=64
OPS=MUL_MAT,MUL_MAT_ID
```

- [x] **Step 2: Validate on DGX dry-run**

Run:

```bash
ssh dgx.casa 'set -euo pipefail; ART=$HOME/bench/phase41_low_concurrency_dryrun/$(date +%Y%m%d_%H%M%S); SRC=$HOME/llama-phase6-source BUILD_DIR=$HOME/llama-phase6-source/build-phase36 BIN=$HOME/llama-phase6-source/build-phase36/bin ART=$ART NPL="1 8 32" PARALLEL=32 CTX=32768 PTOK=128 GEN=64 DRY_RUN=1 OPS=MUL_MAT,MUL_MAT_ID bash -s' < backend/cpp/llama-cpp-localai-paged/paged-current-serving-snapshot.sh
```

Observed artifact: `/home/mudler/bench/phase41_low_concurrency_dryrun/20260701_091429`.

Expected evidence:

```text
docker=0
local_ai_worker=0
compute=0
would build: cmake --build /home/mudler/llama-phase6-source/build-phase36 --target llama-server llama-completion test-backend-ops -j8
would run paged NPL=[1 8 32] PTOK=128 GEN=64
would run vLLM NPL=[1 8 32] PTOK=128 GEN=64
```

### Task 2: Run Low-Concurrency Snapshot With Gates

**Files:**
- Artifact: `dgx:~/bench/phase41_low_concurrency/20260701_091437`

- [x] **Step 1: Run the snapshot**

Run:

```bash
ssh dgx.casa 'set -euo pipefail; ART=$HOME/bench/phase41_low_concurrency/$(date +%Y%m%d_%H%M%S); SRC=$HOME/llama-phase6-source BUILD_DIR=$HOME/llama-phase6-source/build-phase36 BIN=$HOME/llama-phase6-source/build-phase36/bin ART=$ART NPL="1 8 32" PARALLEL=32 CTX=32768 PTOK=128 GEN=64 OPS=MUL_MAT,MUL_MAT_ID bash -s' < backend/cpp/llama-cpp-localai-paged/paged-current-serving-snapshot.sh
```

- [x] **Step 2: Confirm pre/post gates**

Observed:

```text
pre moe_md5 ok 8cb0ce23777bf55f92f63d0292c756b0
pre dense_md5 ok 5951a5b4d624ce891e22ab5fca9bc439
pre op_MUL_MAT ok 1146/1146
pre op_MUL_MAT_ID ok 806/806
post moe_md5 ok 8cb0ce23777bf55f92f63d0292c756b0
post dense_md5 ok 5951a5b4d624ce891e22ab5fca9bc439
post op_MUL_MAT ok 1146/1146
post op_MUL_MAT_ID ok 806/806
```

- [x] **Step 3: Record serving result**

Observed:

```text
arm     n   agg_tps  decode_agg_tps  decode_perseq_tps  prefill_tps  ttft_mean_ms
paged   1   50.6     56.5            55.61              1221.5       131.8
paged   8   159.5    222.9           26.72              1438.8       835.9
paged   32  240.1    393.9           11.15              1615.7       2784.4
vllm    1   67.5     75.4            74.14              1720.4       95.3
vllm    8   251.8    296.5           36.12              4558.8       266.0
vllm    32  454.6    592.4           17.43              5376.5       818.6
```

- [x] **Step 4: Record decision**

Decision: Phase41 confirms D1 remains relevant for low-concurrency/latency work, but the measured current-stack gap is around `0.75x` vLLM at `n=1/8` and `0.665x` at `n=32`, not an immediate parity bridge. TTFT remains the larger user-visible gap.

### Task 3: Update Handoff Docs

**Files:**
- Modify: `backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md`
- Modify: `backend/cpp/llama-cpp-localai-paged/docs/VLLM_PARITY_LEVER_MAP.md`
- Modify: `backend/cpp/llama-cpp-localai-paged/docs/PARITY_HANDOFF.md`
- Modify: `docs/superpowers/plans/2026-07-01-low-concurrency-phase41.md`

- [x] **Step 1: Add Phase41 sections**

Record artifact paths, preflight, gate evidence, serving table, and the D1/TTFT implication in all three handoff documents.

- [x] **Step 2: Verify docs**

Run:

```bash
git diff --check
```

- [x] **Step 3: Commit**

Run:

```bash
git add backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md \
  backend/cpp/llama-cpp-localai-paged/docs/VLLM_PARITY_LEVER_MAP.md \
  backend/cpp/llama-cpp-localai-paged/docs/PARITY_HANDOFF.md
git add -f docs/superpowers/plans/2026-07-01-low-concurrency-phase41.md
git commit -m "docs(paged): record low-concurrency serving check"
```
