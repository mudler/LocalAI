# Max Concurrency Phase40 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Test whether the paged llama.cpp GB10 memory advantage produces a higher-concurrency serving operating point that closes or beats vLLM.

**Architecture:** Use the existing same-session serving snapshot harness with pre/post inference gates. Add only a harness-level `BUILD_DIR` override so the benchmark builds and runs the same selected CMake tree.

**Tech Stack:** Bash harness, DGX GB10, llama.cpp `llama-server`, vLLM OpenAI-compatible server, h2h client, `paged-inference-gates.sh`.

---

### Task 1: Make The Snapshot Harness Build The Selected Tree

**Files:**
- Modify: `backend/cpp/llama-cpp-localai-paged/paged-current-serving-snapshot.sh`

- [x] **Step 1: Write the failing check**

Run:

```bash
backend/cpp/llama-cpp-localai-paged/paged-current-serving-snapshot.sh --help | grep -F 'BUILD_DIR'
```

Expected before the change: exit `1`.

- [x] **Step 2: Add `BUILD_DIR`**

Change the harness so:

```bash
BUILD_DIR=${BUILD_DIR:-"$SRC/build-cuda"}
BIN=${BIN:-"$BUILD_DIR/bin"}
```

and build with:

```bash
cmake --build "$BUILD_DIR" --target llama-server llama-completion test-backend-ops -j 8
```

- [x] **Step 3: Verify locally**

Run:

```bash
bash -n backend/cpp/llama-cpp-localai-paged/paged-current-serving-snapshot.sh
backend/cpp/llama-cpp-localai-paged/paged-current-serving-snapshot.sh --help | grep -F 'BUILD_DIR    llama.cpp CMake build dir'
```

Expected: both exit `0`.

- [x] **Step 4: Verify on DGX dry-run**

Run:

```bash
ssh dgx.casa 'set -euo pipefail; ART=$HOME/bench/phase40_max_concurrency_dryrun/$(date +%Y%m%d_%H%M%S); SRC=$HOME/llama-phase6-source BUILD_DIR=$HOME/llama-phase6-source/build-phase36 BIN=$HOME/llama-phase6-source/build-phase36/bin ART=$ART NPL="128 192 256" PARALLEL=256 CTX=262144 PTOK=128 GEN=64 DRY_RUN=1 OPS=MUL_MAT,MUL_MAT_ID bash -s' < backend/cpp/llama-cpp-localai-paged/paged-current-serving-snapshot.sh
```

Observed artifact: `/home/mudler/bench/phase40_max_concurrency_dryrun/20260701_090002`.

Expected evidence:

```text
docker=0
local_ai_worker=0
compute=0
would build: cmake --build /home/mudler/llama-phase6-source/build-phase36 --target llama-server llama-completion test-backend-ops -j8
```

### Task 2: Run Max-Concurrency Snapshot With Correctness Gates

**Files:**
- Read: `backend/cpp/llama-cpp-localai-paged/paged-current-serving-snapshot.sh`
- Artifact: `dgx:~/bench/phase40_max_concurrency/20260701_090012`

- [x] **Step 1: Run the gated snapshot**

Run:

```bash
ssh dgx.casa 'set -euo pipefail; ART=$HOME/bench/phase40_max_concurrency/$(date +%Y%m%d_%H%M%S); SRC=$HOME/llama-phase6-source BUILD_DIR=$HOME/llama-phase6-source/build-phase36 BIN=$HOME/llama-phase6-source/build-phase36/bin ART=$ART NPL="128 192 256" PARALLEL=256 CTX=262144 PTOK=128 GEN=64 OPS=MUL_MAT,MUL_MAT_ID bash -s' < backend/cpp/llama-cpp-localai-paged/paged-current-serving-snapshot.sh
```

- [x] **Step 2: Confirm pre/post inference gates**

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
arm     n    agg_tps  decode_agg_tps  decode_perseq_tps  prefill_tps  ttft_mean_ms
paged   128  326.3    671.8           3.97               1695.2       8182.3
paged   192  318.3    679.9           2.50               1605.2       11151.6
paged   256  337.1    829.9           2.09               1525.7       15065.7
vllm    128  654.4    1013.3          6.72               5206.0       2582.6
vllm    192  697.7    1185.2          4.88               4787.1       3690.6
vllm    256  714.1    1306.1          3.90               4471.0       5124.2
```

- [x] **Step 4: Record decision**

Decision: C1 does not close GB10 parity for the tested `PTOK=128`, `GEN=64`, `NPL=128/192/256` workload. Paged runs safely at `n=256`, but vLLM also fits and remains faster (`paged_decode_over_vllm=0.6354`, `paged_agg_over_vllm=0.4721`).

### Task 3: Update Handoff Docs

**Files:**
- Modify: `backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md`
- Modify: `backend/cpp/llama-cpp-localai-paged/docs/VLLM_PARITY_LEVER_MAP.md`
- Modify: `backend/cpp/llama-cpp-localai-paged/docs/PARITY_HANDOFF.md`
- Modify: `docs/superpowers/plans/2026-07-01-max-concurrency-phase40.md`

- [x] **Step 1: Add Phase40 sections**

Record artifact paths, gate evidence, throughput table, and C1 decision in all three handoff documents.

- [x] **Step 2: Verify docs and script**

Run:

```bash
bash -n backend/cpp/llama-cpp-localai-paged/paged-current-serving-snapshot.sh
git diff --check
```

- [x] **Step 3: Commit**

Run:

```bash
git add backend/cpp/llama-cpp-localai-paged/paged-current-serving-snapshot.sh \
  backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md \
  backend/cpp/llama-cpp-localai-paged/docs/VLLM_PARITY_LEVER_MAP.md \
  backend/cpp/llama-cpp-localai-paged/docs/PARITY_HANDOFF.md \
  docs/superpowers/plans/2026-07-01-max-concurrency-phase40.md
git commit -m "docs(paged): record max-concurrency parity check"
```
