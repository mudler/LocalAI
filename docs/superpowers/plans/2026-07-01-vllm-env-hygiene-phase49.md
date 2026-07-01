# Phase49 vLLM Env Hygiene Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Keep vLLM benchmark logs clean by preventing harness-only `VLLM_*` variables from being inherited by the vLLM server process.

**Architecture:** Add an `env -u ...` wrapper around the `vllm serve` command in `paged-current-serving-snapshot.sh`. Only unset harness-owned variables (`VLLM_MODEL`, `VLLM_BIN`, `VLLM_READY_ATTEMPTS`, `VLLM_GPU_MEMORY_UTILIZATION`, `VLLM_MAX_MODEL_LEN`, `VLLM_MAX_NUM_SEQS`, `VLLM_TENSOR_PARALLEL_SIZE`, `VLLM_EXTRA_ARGS`) and keep intentional vLLM runtime variables like `VLLM_LOGGING_LEVEL`.

**Tech Stack:** Bash serving harness, LocalAI parity docs.

---

### Task 1: Prove env scrubbing is absent

**Files:**
- Test: `backend/cpp/llama-cpp-localai-paged/paged-current-serving-snapshot.sh`

- [x] **Step 1: Run red grep**

```bash
grep -F 'env -u VLLM_MODEL' backend/cpp/llama-cpp-localai-paged/paged-current-serving-snapshot.sh
```

Expected: exit `1`.

### Task 2: Add vLLM child env scrub

**Files:**
- Modify: `backend/cpp/llama-cpp-localai-paged/paged-current-serving-snapshot.sh`

- [x] **Step 1: Wrap the vLLM command**

Change:

```bash
nohup "$VLLM_BIN" serve "$VLLM_MODEL" \
```

to:

```bash
nohup env \
  -u VLLM_MODEL -u VLLM_BIN -u VLLM_READY_ATTEMPTS \
  -u VLLM_GPU_MEMORY_UTILIZATION -u VLLM_MAX_MODEL_LEN -u VLLM_MAX_NUM_SEQS \
  -u VLLM_TENSOR_PARALLEL_SIZE -u VLLM_EXTRA_ARGS \
  "$VLLM_BIN" serve "$VLLM_MODEL" \
```

### Task 3: Verify

**Files:**
- Test: `backend/cpp/llama-cpp-localai-paged/paged-current-serving-snapshot.sh`

- [x] **Step 1: Shell syntax check**

```bash
bash -n backend/cpp/llama-cpp-localai-paged/paged-current-serving-snapshot.sh
```

Expected: exit `0`.

- [x] **Step 2: Green grep**

```bash
grep -F -- '-u VLLM_MODEL' backend/cpp/llama-cpp-localai-paged/paged-current-serving-snapshot.sh
```

Expected: exit `0`.

- [x] **Step 3: DGX dry-run still passes**

```bash
ssh dgx.casa 'set -euo pipefail; ART=$HOME/bench/phase49_vllm_env_hygiene_dryrun/$(date +%Y%m%d_%H%M%S); SRC=$HOME/llama-phase6-source BUILD_DIR=$HOME/llama-phase6-source/build-phase36 BIN=$HOME/llama-phase6-source/build-phase36/bin MODEL=$HOME/bench/q36-27b-nvfp4.gguf VLLM_MODEL=$HOME/bench/q36-27b-nvfp4-vllm SERVED_MODEL_NAME=dense-q36 ART=$ART NPL="1" PARALLEL=1 CTX=4096 PTOK=16 GEN=4 DRY_RUN=1 VLLM_READY_ATTEMPTS=700 OPS=MUL_MAT,MUL_MAT_ID bash -s' < backend/cpp/llama-cpp-localai-paged/paged-current-serving-snapshot.sh
```

Expected: exit `0`, clean preflight, and dry-run output still prints `VLLM_READY_ATTEMPTS=700`.

### Task 4: Record and commit

**Files:**
- Modify: `backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md`
- Modify: `backend/cpp/llama-cpp-localai-paged/docs/PARITY_HANDOFF.md`
- Modify: `docs/superpowers/plans/2026-07-01-vllm-env-hygiene-phase49.md`

- [x] **Step 1: Record Phase49**

Record the dry-run artifact and state that this is log hygiene only.

- [x] **Step 2: Final checks and commit**

```bash
git diff --check
git add backend/cpp/llama-cpp-localai-paged/paged-current-serving-snapshot.sh \
        backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md \
        backend/cpp/llama-cpp-localai-paged/docs/PARITY_HANDOFF.md
git add -f docs/superpowers/plans/2026-07-01-vllm-env-hygiene-phase49.md
git commit -m "fix(paged): scrub harness vars for vllm serve" -m "Assisted-by: Codex:gpt-5"
```
