# Phase48 Serving Harness Readiness Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the audited serving snapshot harness robust to slow vLLM dense startup and non-exiting server processes.

**Architecture:** Keep the fix local to `paged-current-serving-snapshot.sh`: add separate llama/vLLM readiness budgets, bound each HTTP probe with `curl --max-time`, and replace unbounded server cleanup waits with a short graceful wait followed by `SIGKILL`.

**Tech Stack:** Bash harness, DGX dry-run, LocalAI parity docs.

---

### Task 1: Prove the robustness controls are absent

**Files:**
- Test: `backend/cpp/llama-cpp-localai-paged/paged-current-serving-snapshot.sh`

- [x] **Step 1: Run readiness-budget red check**

```bash
backend/cpp/llama-cpp-localai-paged/paged-current-serving-snapshot.sh --help | grep -F 'VLLM_READY_ATTEMPTS'
```

Expected: exit `1`.

- [x] **Step 2: Run bounded-curl red check**

```bash
grep -F 'curl --max-time' backend/cpp/llama-cpp-localai-paged/paged-current-serving-snapshot.sh
```

Expected: exit `1`.

- [x] **Step 3: Run cleanup hard-kill red check**

```bash
grep -F 'kill -9 "$SERVER_PID"' backend/cpp/llama-cpp-localai-paged/paged-current-serving-snapshot.sh
```

Expected: exit `1`.

### Task 2: Patch readiness and cleanup

**Files:**
- Modify: `backend/cpp/llama-cpp-localai-paged/paged-current-serving-snapshot.sh`

- [x] **Step 1: Add documented environment variables**

Add:

```bash
  LLAMA_READY_ATTEMPTS llama-server readiness attempts, one per second (default: 240)
  VLLM_READY_ATTEMPTS  vLLM readiness attempts, one per second (default: 600)
```

- [x] **Step 2: Add defaults**

```bash
LLAMA_READY_ATTEMPTS=${LLAMA_READY_ATTEMPTS:-240}
VLLM_READY_ATTEMPTS=${VLLM_READY_ATTEMPTS:-600}
```

- [x] **Step 3: Bound HTTP probes**

Change `wait_http()` to accept an attempts argument and run:

```bash
curl --max-time 2 -fsS "$url" > "$health" 2>"$health.err"
```

- [x] **Step 4: Use per-server readiness budgets**

Call `wait_http` with `$LLAMA_READY_ATTEMPTS` for llama-server and `$VLLM_READY_ATTEMPTS` for vLLM.

- [x] **Step 5: Add bounded process cleanup**

Create `stop_server_pid()` that sends `SIGTERM`, waits up to 30 seconds, sends `SIGKILL` if needed, and only then calls `wait`.

### Task 3: Verify the harness fix

**Files:**
- Test: `backend/cpp/llama-cpp-localai-paged/paged-current-serving-snapshot.sh`

- [x] **Step 1: Shell syntax check**

```bash
bash -n backend/cpp/llama-cpp-localai-paged/paged-current-serving-snapshot.sh
```

Expected: exit `0`.

- [x] **Step 2: Help-text green check**

```bash
backend/cpp/llama-cpp-localai-paged/paged-current-serving-snapshot.sh --help | grep -F 'VLLM_READY_ATTEMPTS'
```

Expected: exit `0`.

- [x] **Step 3: Bounded-curl green check**

```bash
grep -F 'curl --max-time 2 -fsS "$url"' backend/cpp/llama-cpp-localai-paged/paged-current-serving-snapshot.sh
```

Expected: exit `0`.

- [x] **Step 4: Cleanup hard-kill green check**

```bash
grep -F 'kill -9 "$SERVER_PID"' backend/cpp/llama-cpp-localai-paged/paged-current-serving-snapshot.sh
```

Expected: exit `0`.

- [x] **Step 5: DGX dry-run with long vLLM readiness budget**

```bash
ssh dgx.casa 'set -euo pipefail; ART=$HOME/bench/phase48_readiness_harness_dryrun/$(date +%Y%m%d_%H%M%S); SRC=$HOME/llama-phase6-source BUILD_DIR=$HOME/llama-phase6-source/build-phase36 BIN=$HOME/llama-phase6-source/build-phase36/bin MODEL=$HOME/bench/q36-27b-nvfp4.gguf VLLM_MODEL=$HOME/bench/q36-27b-nvfp4-vllm SERVED_MODEL_NAME=dense-q36 ART=$ART NPL="1" PARALLEL=1 CTX=4096 PTOK=16 GEN=4 DRY_RUN=1 VLLM_READY_ATTEMPTS=700 OPS=MUL_MAT,MUL_MAT_ID bash -s' < backend/cpp/llama-cpp-localai-paged/paged-current-serving-snapshot.sh
```

Expected: exit `0`, clean preflight, and dry-run output includes `VLLM_READY_ATTEMPTS=700`.

### Task 4: Record Phase48 and failed Phase47 attempt

**Files:**
- Modify: `backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md`
- Modify: `backend/cpp/llama-cpp-localai-paged/docs/VLLM_PARITY_LEVER_MAP.md`
- Modify: `backend/cpp/llama-cpp-localai-paged/docs/PARITY_HANDOFF.md`
- Modify: `docs/superpowers/plans/2026-07-01-dense-serving-snapshot-phase47.md`
- Modify: `docs/superpowers/plans/2026-07-01-serving-harness-readiness-phase48.md`

- [x] **Step 1: Record Phase47 as failed/incomplete**

Record the partial artifact and the root cause: vLLM dense startup exceeded the old 240-attempt readiness budget, and cleanup could hang waiting on the server PID.

- [x] **Step 2: Record Phase48 fix**

Record the new readiness variables, bounded curl probe, bounded cleanup, and dry-run artifact.

### Task 5: Commit

**Files:**
- Commit the Phase48 harness, docs, and plan changes.

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
git add -f docs/superpowers/plans/2026-07-01-dense-serving-snapshot-phase47.md \
           docs/superpowers/plans/2026-07-01-serving-harness-readiness-phase48.md
git commit -m "fix(paged): harden serving snapshot readiness" -m "Assisted-by: Codex:gpt-5"
```
