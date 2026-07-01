# Phase46 Served Model Name Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let the audited serving snapshot harness run MoE, dense, or hardware-pivot model variants without hardcoded `q36` model names.

**Architecture:** Add a single `SERVED_MODEL_NAME` environment variable to `paged-current-serving-snapshot.sh`, defaulting to `q36`. Use it consistently for vLLM `--served-model-name`, vLLM model readiness checks, and h2h `--model` requests on both engines. Print it in `DRY_RUN=1` output so hardware-pivot runs can be audited before launching servers.

**Tech Stack:** Bash serving harness, DGX dry-run preflight, LocalAI parity docs.

---

### Task 1: Prove the override is missing

**Files:**
- Test: `backend/cpp/llama-cpp-localai-paged/paged-current-serving-snapshot.sh`

- [x] **Step 1: Run help-text red check**

```bash
backend/cpp/llama-cpp-localai-paged/paged-current-serving-snapshot.sh --help | grep -F 'SERVED_MODEL_NAME'
```

Expected: exit `1`, because the harness does not document the model-name override yet.

- [x] **Step 2: Run DGX dry-run red check**

```bash
ssh dgx.casa 'set -euo pipefail; ART=$HOME/bench/phase46_served_model_name_dryrun_red/$(date +%Y%m%d_%H%M%S); SRC=$HOME/llama-phase6-source BUILD_DIR=$HOME/llama-phase6-source/build-phase36 BIN=$HOME/llama-phase6-source/build-phase36/bin ART=$ART NPL="1" PARALLEL=1 CTX=4096 PTOK=16 GEN=4 DRY_RUN=1 SERVED_MODEL_NAME=dense-q36 OPS=MUL_MAT,MUL_MAT_ID bash -s' < backend/cpp/llama-cpp-localai-paged/paged-current-serving-snapshot.sh | grep -F 'SERVED_MODEL_NAME=dense-q36'
```

Expected: exit `1`, because `DRY_RUN=1` does not print the served model name yet.

### Task 2: Add `SERVED_MODEL_NAME`

**Files:**
- Modify: `backend/cpp/llama-cpp-localai-paged/paged-current-serving-snapshot.sh`

- [x] **Step 1: Document the variable**

Add this line after `VLLM_MODEL`:

```bash
  SERVED_MODEL_NAME OpenAI model name used by llama-server, vLLM, and h2h (default: q36)
```

- [x] **Step 2: Add the default**

Add this assignment after `VLLM_MODEL`:

```bash
SERVED_MODEL_NAME=${SERVED_MODEL_NAME:-q36}
```

- [x] **Step 3: Replace hardcoded h2h model names**

Replace every h2h `--model q36` with:

```bash
--model "$SERVED_MODEL_NAME"
```

- [x] **Step 4: Replace hardcoded vLLM model name and readiness check**

Replace:

```bash
--served-model-name q36
wait_http "http://127.0.0.1:$VLLM_PORT/v1/models" "q36" ...
```

with:

```bash
--served-model-name "$SERVED_MODEL_NAME"
wait_http "http://127.0.0.1:$VLLM_PORT/v1/models" "$SERVED_MODEL_NAME" ...
```

- [x] **Step 5: Print it in dry-run output**

Add:

```bash
log "served model: SERVED_MODEL_NAME=$SERVED_MODEL_NAME"
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
backend/cpp/llama-cpp-localai-paged/paged-current-serving-snapshot.sh --help | grep -F 'SERVED_MODEL_NAME'
```

Expected: exit `0`.

- [x] **Step 3: DGX dry-run green check**

```bash
ssh dgx.casa 'set -euo pipefail; ART=$HOME/bench/phase46_served_model_name_dryrun/$(date +%Y%m%d_%H%M%S); SRC=$HOME/llama-phase6-source BUILD_DIR=$HOME/llama-phase6-source/build-phase36 BIN=$HOME/llama-phase6-source/build-phase36/bin ART=$ART NPL="1" PARALLEL=1 CTX=4096 PTOK=16 GEN=4 DRY_RUN=1 SERVED_MODEL_NAME=dense-q36 OPS=MUL_MAT,MUL_MAT_ID bash -s' < backend/cpp/llama-cpp-localai-paged/paged-current-serving-snapshot.sh
```

Expected: exit `0`, clean preflight, and output includes `SERVED_MODEL_NAME=dense-q36`.

### Task 4: Record Phase46

**Files:**
- Modify: `backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md`
- Modify: `backend/cpp/llama-cpp-localai-paged/docs/VLLM_PARITY_LEVER_MAP.md`
- Modify: `backend/cpp/llama-cpp-localai-paged/docs/PARITY_HANDOFF.md`
- Modify: `docs/superpowers/plans/2026-07-01-served-model-name-phase46.md`

- [x] **Step 1: Append the Phase46 result**

Record that this is harness-only hardware-pivot readiness and cite the DGX dry-run artifact.

- [x] **Step 2: Mark all completed plan items**

Mark this file's remaining task checkboxes complete only after the corresponding command or docs update has happened.

### Task 5: Commit

**Files:**
- Commit Phase46 script, docs, and plan changes.

- [x] **Step 1: Run final checks**

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
git add -f docs/superpowers/plans/2026-07-01-served-model-name-phase46.md
git commit -m "feat(paged): parameterize served model name" -m "Assisted-by: Codex:gpt-5"
```
