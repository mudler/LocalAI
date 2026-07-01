# Phase45 Inference Gate Guard Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Prove the current DGX build still passes the canonical paged inference md5 and backend-op gates after the harness-only Phase44 change.

**Architecture:** Run the existing DGX `~/paged-inference-gates.sh` script against `~/llama-phase6-source/build-phase36/bin` with both `MUL_MAT` and `MUL_MAT_ID` op filters. Record the artifact in the parity docs; do not change llama.cpp inference source.

**Tech Stack:** DGX ssh, Bash gate harness, LocalAI parity documentation.

---

### Task 1: Confirm DGX gate preflight

**Files:**
- Test only: DGX runtime state.

- [x] **Step 1: Check docker, LocalAI worker, GPU compute, and lock owner**

```bash
ssh dgx.casa 'set -euo pipefail; docker_count=$(docker ps -q | wc -l); local_ai=$(docker ps --format "{{.Names}}" | grep -c local-ai-worker || true); compute=$(nvidia-smi --query-compute-apps=pid --format=csv,noheader | sed "/^$/d" | wc -l); owner=FREE-no-lock-file; if [ -f "$HOME/gpu_bench_lock/owner" ]; then owner=$(cat "$HOME/gpu_bench_lock/owner"); fi; printf "docker=%s\nlocal_ai_worker=%s\ncompute=%s\nowner=%s\n" "$docker_count" "$local_ai" "$compute" "$owner"'
```

Expected: `docker=0`, `local_ai_worker=0`, `compute=0`, and owner starts with `FREE`.

### Task 2: Run canonical inference gates

**Files:**
- Test only: `~/paged-inference-gates.sh` on DGX.

- [x] **Step 1: Run md5 and backend-op gates**

```bash
ssh dgx.casa 'set -euo pipefail; ART=$HOME/bench/phase45_inference_gate_guard/$(date +%Y%m%d_%H%M%S); BIN=$HOME/llama-phase6-source/build-phase36/bin ART=$ART OPS=MUL_MAT,MUL_MAT_ID ~/paged-inference-gates.sh'
```

Expected:

```text
moe md5 OK: 8cb0ce23777bf55f92f63d0292c756b0
dense md5 OK: 5951a5b4d624ce891e22ab5fca9bc439
1146/1146 tests passed
Backend CUDA0: OK
806/806 tests passed
Backend CUDA0: OK
paged inference gates OK
```

### Task 3: Record Phase45

**Files:**
- Modify: `backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md`
- Modify: `backend/cpp/llama-cpp-localai-paged/docs/VLLM_PARITY_LEVER_MAP.md`
- Modify: `backend/cpp/llama-cpp-localai-paged/docs/PARITY_HANDOFF.md`
- Modify: `docs/superpowers/plans/2026-07-01-inference-gate-guard-phase45.md`

- [x] **Step 1: Append gate artifact and verdict**

Record the exact artifact directory and the md5/op results.

- [x] **Step 2: Mark this plan complete**

Only mark the remaining steps complete after the gate and docs update are done.

### Task 4: Commit

**Files:**
- Commit the Phase45 docs and plan.

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
git add -f docs/superpowers/plans/2026-07-01-inference-gate-guard-phase45.md
git commit -m "docs(paged): record inference gate guard" -m "Assisted-by: Codex:gpt-5"
```
