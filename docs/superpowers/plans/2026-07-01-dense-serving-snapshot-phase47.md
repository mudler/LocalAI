# Phase47 Dense Serving Snapshot Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Use the newly parameterized harness to collect an audited dense paged-vs-vLLM serving snapshot, without changing inference code.

**Architecture:** Run `paged-current-serving-snapshot.sh` against the dense GGUF and dense vLLM model with `SERVED_MODEL_NAME=dense-q36`. Keep the standard pre/post paged inference gates and `MUL_MAT,MUL_MAT_ID` op checks.

**Tech Stack:** Bash serving harness, DGX, LocalAI parity docs.

---

### Task 1: Dry-run dense snapshot inputs

**Files:**
- Test: `backend/cpp/llama-cpp-localai-paged/paged-current-serving-snapshot.sh`

- [x] **Step 1: Run DGX dry-run**

```bash
ssh dgx.casa 'set -euo pipefail; ART=$HOME/bench/phase47_dense_serving_dryrun/$(date +%Y%m%d_%H%M%S); SRC=$HOME/llama-phase6-source BUILD_DIR=$HOME/llama-phase6-source/build-phase36 BIN=$HOME/llama-phase6-source/build-phase36/bin MODEL=$HOME/bench/q36-27b-nvfp4.gguf VLLM_MODEL=$HOME/bench/q36-27b-nvfp4-vllm SERVED_MODEL_NAME=dense-q36 ART=$ART NPL="1" PARALLEL=1 CTX=4096 PTOK=16 GEN=4 DRY_RUN=1 OPS=MUL_MAT,MUL_MAT_ID bash -s' < backend/cpp/llama-cpp-localai-paged/paged-current-serving-snapshot.sh
```

Expected: exit `0`, docker/local-ai-worker/GPU compute all zero, dense model paths validated, and `SERVED_MODEL_NAME=dense-q36` printed.

### Task 2: Run audited dense serving snapshot

**Files:**
- Test: `backend/cpp/llama-cpp-localai-paged/paged-current-serving-snapshot.sh`

- [x] **Step 1: Run full dense snapshot after Phase48 hardening**

```bash
ssh dgx.casa 'set -euo pipefail; ART=$HOME/bench/phase47_dense_serving/$(date +%Y%m%d_%H%M%S); SRC=$HOME/llama-phase6-source BUILD_DIR=$HOME/llama-phase6-source/build-phase36 BIN=$HOME/llama-phase6-source/build-phase36/bin MODEL=$HOME/bench/q36-27b-nvfp4.gguf VLLM_MODEL=$HOME/bench/q36-27b-nvfp4-vllm SERVED_MODEL_NAME=dense-q36 ART=$ART NPL="1 8 32 128" PARALLEL=128 CTX=131072 PTOK=128 GEN=64 OPS=MUL_MAT,MUL_MAT_ID bash -s' < backend/cpp/llama-cpp-localai-paged/paged-current-serving-snapshot.sh
```

Expected: full run exits `0`, pre/post gates are green, and `summary.tsv` contains paged-vs-vLLM ratios for `n=1/8/32/128`.

First attempt status: incomplete at
`/home/mudler/bench/phase47_dense_serving/20260701_095151`. Pre-gates and the
paged arm completed, but vLLM startup exceeded the old fixed readiness budget
and produced no vLLM result JSONs. Retry only after Phase48 readiness hardening.

Retry status: completed at
`/home/mudler/bench/phase47_dense_serving_retry/20260701_100811` after Phase48
with `VLLM_READY_ATTEMPTS=700`.

### Task 3: Record dense snapshot result

**Files:**
- Modify: `backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md`
- Modify: `backend/cpp/llama-cpp-localai-paged/docs/VLLM_PARITY_LEVER_MAP.md`
- Modify: `backend/cpp/llama-cpp-localai-paged/docs/PARITY_HANDOFF.md`
- Modify: `docs/superpowers/plans/2026-07-01-dense-serving-snapshot-phase47.md`

- [x] **Step 1: Summarize artifact outputs**

Record the dry-run artifact, full snapshot artifact, pre/post md5/op gate status, and the ratio rows from `summary.tsv`.

- [x] **Step 2: Mark completed plan items**

Mark this plan's checkboxes complete only after the corresponding command or docs update has happened.

### Task 4: Commit

**Files:**
- Commit Phase47 docs and plan changes.

- [x] **Step 1: Run final checks**

```bash
git diff --check
git status --short
```

Expected: no whitespace errors; only intended docs/plan changes plus the pre-existing untracked `.claude/`.

- [x] **Step 2: Commit**

```bash
git add backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md \
        backend/cpp/llama-cpp-localai-paged/docs/VLLM_PARITY_LEVER_MAP.md \
        backend/cpp/llama-cpp-localai-paged/docs/PARITY_HANDOFF.md
git add -f docs/superpowers/plans/2026-07-01-dense-serving-snapshot-phase47.md
git commit -m "docs(paged): record dense serving snapshot" -m "Assisted-by: Codex:gpt-5"
```
