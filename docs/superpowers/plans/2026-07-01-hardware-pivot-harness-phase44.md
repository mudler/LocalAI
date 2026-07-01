# Phase44 Hardware Pivot Harness Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the current-stack serving snapshot harness configurable enough to run the same audited paged-vs-vLLM methodology on hardware beyond the current GB10 defaults.

**Architecture:** Keep this as a harness-only change: add environment overrides for vLLM serving limits and print them in `DRY_RUN=1` output. Do not touch llama.cpp inference code, patch-series source, md5 gates, or op gates.

**Tech Stack:** Bash harness, DGX preflight over ssh, LocalAI parity documentation.

---

### Task 1: Prove the vLLM config knobs are absent

**Files:**
- Test: `backend/cpp/llama-cpp-localai-paged/paged-current-serving-snapshot.sh`

- [x] **Step 1: Run help-text red check**

```bash
backend/cpp/llama-cpp-localai-paged/paged-current-serving-snapshot.sh --help | grep -F 'VLLM_MAX_NUM_SEQS'
```

Expected: exit `1`, because the harness does not document the override yet.

- [x] **Step 2: Run DGX dry-run red check**

```bash
ssh dgx.casa 'set -euo pipefail; ART=$HOME/bench/phase44_hardware_pivot_harness_dryrun_red/$(date +%Y%m%d_%H%M%S); SRC=$HOME/llama-phase6-source BUILD_DIR=$HOME/llama-phase6-source/build-phase36 BIN=$HOME/llama-phase6-source/build-phase36/bin ART=$ART NPL="1" PARALLEL=1 CTX=4096 PTOK=16 GEN=4 DRY_RUN=1 VLLM_GPU_MEMORY_UTILIZATION=0.90 VLLM_MAX_MODEL_LEN=8192 VLLM_MAX_NUM_SEQS=512 VLLM_TENSOR_PARALLEL_SIZE=2 VLLM_EXTRA_ARGS="--disable-log-requests" OPS=MUL_MAT,MUL_MAT_ID bash -s' < backend/cpp/llama-cpp-localai-paged/paged-current-serving-snapshot.sh | grep -F 'VLLM_MAX_NUM_SEQS=512'
```

Expected: exit `1`, because `DRY_RUN=1` validates inputs but does not print the vLLM serving config yet.

### Task 2: Add vLLM serving overrides

**Files:**
- Modify: `backend/cpp/llama-cpp-localai-paged/paged-current-serving-snapshot.sh`

- [x] **Step 1: Document the environment variables in `usage()`**

Add these lines under `VLLM_BIN`:

```bash
  VLLM_GPU_MEMORY_UTILIZATION vLLM --gpu-memory-utilization (default: 0.85)
  VLLM_MAX_MODEL_LEN          vLLM --max-model-len (default: 4096)
  VLLM_MAX_NUM_SEQS           vLLM --max-num-seqs (default: 256)
  VLLM_TENSOR_PARALLEL_SIZE   vLLM --tensor-parallel-size (default: 1)
  VLLM_EXTRA_ARGS             whitespace-split extra args appended to vLLM serve (default: empty)
```

- [x] **Step 2: Add conservative defaults beside `VLLM_BIN`**

```bash
VLLM_GPU_MEMORY_UTILIZATION=${VLLM_GPU_MEMORY_UTILIZATION:-0.85}
VLLM_MAX_MODEL_LEN=${VLLM_MAX_MODEL_LEN:-4096}
VLLM_MAX_NUM_SEQS=${VLLM_MAX_NUM_SEQS:-256}
VLLM_TENSOR_PARALLEL_SIZE=${VLLM_TENSOR_PARALLEL_SIZE:-1}
VLLM_EXTRA_ARGS=${VLLM_EXTRA_ARGS:-}
```

- [x] **Step 3: Use the variables in `run_vllm()`**

Use an array for `VLLM_EXTRA_ARGS`:

```bash
  local extra_args=()
  if [[ -n "$VLLM_EXTRA_ARGS" ]]; then
    read -r -a extra_args <<< "$VLLM_EXTRA_ARGS"
  fi
```

Then replace the hardcoded vLLM flags with:

```bash
    --served-model-name q36 --gpu-memory-utilization "$VLLM_GPU_MEMORY_UTILIZATION" --max-model-len "$VLLM_MAX_MODEL_LEN" \
    --max-num-seqs "$VLLM_MAX_NUM_SEQS" --host 127.0.0.1 --port "$VLLM_PORT" --tensor-parallel-size "$VLLM_TENSOR_PARALLEL_SIZE" \
    "${extra_args[@]}" \
```

- [x] **Step 4: Print the vLLM config during `DRY_RUN=1`**

```bash
  log "vLLM config: VLLM_GPU_MEMORY_UTILIZATION=$VLLM_GPU_MEMORY_UTILIZATION VLLM_MAX_MODEL_LEN=$VLLM_MAX_MODEL_LEN VLLM_MAX_NUM_SEQS=$VLLM_MAX_NUM_SEQS VLLM_TENSOR_PARALLEL_SIZE=$VLLM_TENSOR_PARALLEL_SIZE VLLM_EXTRA_ARGS=[$VLLM_EXTRA_ARGS]"
```

### Task 3: Verify the harness

**Files:**
- Test: `backend/cpp/llama-cpp-localai-paged/paged-current-serving-snapshot.sh`

- [x] **Step 1: Shell syntax check**

```bash
bash -n backend/cpp/llama-cpp-localai-paged/paged-current-serving-snapshot.sh
```

Expected: exit `0`.

- [x] **Step 2: Help-text green check**

```bash
backend/cpp/llama-cpp-localai-paged/paged-current-serving-snapshot.sh --help | grep -F 'VLLM_MAX_NUM_SEQS'
```

Expected: exit `0`.

- [x] **Step 3: DGX dry-run green check**

```bash
ssh dgx.casa 'set -euo pipefail; ART=$HOME/bench/phase44_hardware_pivot_harness_dryrun/$(date +%Y%m%d_%H%M%S); SRC=$HOME/llama-phase6-source BUILD_DIR=$HOME/llama-phase6-source/build-phase36 BIN=$HOME/llama-phase6-source/build-phase36/bin ART=$ART NPL="1" PARALLEL=1 CTX=4096 PTOK=16 GEN=4 DRY_RUN=1 VLLM_GPU_MEMORY_UTILIZATION=0.90 VLLM_MAX_MODEL_LEN=8192 VLLM_MAX_NUM_SEQS=512 VLLM_TENSOR_PARALLEL_SIZE=2 VLLM_EXTRA_ARGS="--disable-log-requests" OPS=MUL_MAT,MUL_MAT_ID bash -s' < backend/cpp/llama-cpp-localai-paged/paged-current-serving-snapshot.sh
```

Expected: exit `0`, preflight shows docker/local-ai-worker/GPU compute idle, and output includes `VLLM_MAX_NUM_SEQS=512`.

### Task 4: Record Phase44 in docs

**Files:**
- Modify: `backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md`
- Modify: `backend/cpp/llama-cpp-localai-paged/docs/VLLM_PARITY_LEVER_MAP.md`
- Modify: `backend/cpp/llama-cpp-localai-paged/docs/PARITY_HANDOFF.md`
- Modify: `docs/superpowers/plans/2026-07-01-hardware-pivot-harness-phase44.md`

- [x] **Step 1: Append the Phase44 result**

Record that Phase44 is a harness-readiness change only. It does not claim a new performance result, does not run inference, and does not modify md5/op gate behavior.

- [x] **Step 2: Mark all plan tasks complete**

Change each remaining `- [ ]` entry in this file to `- [x]` only after the corresponding verification has been run.

### Task 5: Commit

**Files:**
- Commit all Phase44 script, docs, and plan changes.

- [x] **Step 1: Run final diff checks**

```bash
git diff --check
git status --short
```

Expected: no whitespace errors; only intended files changed plus the pre-existing untracked `.claude/`.

- [x] **Step 2: Commit**

```bash
git add backend/cpp/llama-cpp-localai-paged/paged-current-serving-snapshot.sh \
        backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md \
        backend/cpp/llama-cpp-localai-paged/docs/VLLM_PARITY_LEVER_MAP.md \
        backend/cpp/llama-cpp-localai-paged/docs/PARITY_HANDOFF.md
git add -f docs/superpowers/plans/2026-07-01-hardware-pivot-harness-phase44.md
git commit -m "feat(paged): parameterize vllm serving snapshot" -m "Assisted-by: Codex:gpt-5"
```
