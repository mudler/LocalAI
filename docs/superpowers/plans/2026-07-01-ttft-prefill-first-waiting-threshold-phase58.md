# Phase58 TTFT Prefill-First Waiting-Threshold Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Test whether activating TTFT prefill-first only during high prompt-backlog windows keeps the MoE benefit without the broad-defer regressions from Phase56-57.

**Architecture:** Add `LLAMA_TTFT_PREFILL_FIRST_MIN_WAITING` as a default-off refinement. Unset or zero keeps the existing Phase55/57 behavior. Gate with focused tests, then run DGX md5/op gates and same-window MoE/dense threshold sweeps.

**Tech Stack:** llama.cpp fork, `tools/server/server-admission-policy.h`, `tools/server/server-context.cpp`, DGX GB10, `h2h_cli.py`, `paged-inference-gates.sh`.

---

### Task 1: Add waiting-threshold helper

- [x] **Step 1: Write red test**

Added helper expectations:

- zero waiting threshold defers
- at waiting threshold defers
- below waiting threshold does not defer

Observed red failure: no helper overload accepted the waiting-slot threshold
signature.

- [x] **Step 2: Implement threshold helper and env**

Added `LLAMA_TTFT_PREFILL_FIRST_MIN_WAITING`. The scheduler now counts prompt
slots in `SLOT_STATE_STARTED` or `SLOT_STATE_PROCESSING_PROMPT` before collecting
decode rows and only defers if the waiting count is at or above the threshold.

- [x] **Step 3: Verify local**

Commands passed:

```bash
cmake --build build --target test-server-admission-policy test-server-admission-trace llama-server -j2
./build/bin/test-server-admission-policy
ctest --test-dir build -R 'test-server-admission-(policy|trace)' --output-on-failure
```

- [x] **Step 4: Commit fork patch**

Local fork commit:

```text
8759213e3 feat(server): gate TTFT defer by prompt backlog
```

### Task 2: DGX gate and threshold sweep

- [x] **Step 1: Preflight and build**

Preflight: docker `0`, `local-ai-worker` `0`, compute `0`, lock
`FREE released-by-codex-phase57-cap 1782901003`, clean mirror at
`2cbb61969443cf52aa1aa58eb9f5a8d7c20a7780`.

Applied `/tmp/phase58-ttft-waiting-stack.patch`, built focused tests,
`llama-server`, `llama-cli`, and `test-backend-ops`. DGX focused CTests passed.

- [x] **Step 2: Run pre/post gates**

Artifact: `/home/mudler/bench/phase58_ttft_waiting_sweep/20260701_122052`.

Pre and post gates matched:

- MoE md5 `8cb0ce23777bf55f92f63d0292c756b0`
- dense md5 `5951a5b4d624ce891e22ab5fca9bc439`
- `MUL_MAT` `1146/1146`
- `MUL_MAT_ID` `806/806`

- [x] **Step 3: Run MoE threshold sweep**

MoE `n=128`, `ptok=128`, `gen=64`:

| variant | agg t/s | decode agg t/s | prefill t/s | TTFT mean ms | TTFT max ms | wall s | deferred |
|---------|---------|-----------------|-------------|--------------|-------------|--------|----------|
| default | `339.0` | `648.4` | `1542.9` | `7743.1` | `11532.5` | `24.167` | `0` |
| min24 | `339.9` | `619.3` | `1637.0` | `7326.6` | `10868.8` | `24.095` | `323` |
| min32 | `341.9` | `635.0` | `1609.6` | `7420.1` | `11054.6` | `23.950` | `220` |
| min32+cap32 | `331.2` | `631.8` | `1512.1` | `7829.2` | `11767.1` | `24.733` | `140` |

- [x] **Step 4: Run dense threshold sweep**

Dense `n=128`, `ptok=168`, `gen=64`:

| variant | agg t/s | decode agg t/s | prefill t/s | TTFT mean ms | TTFT max ms | wall s | deferred |
|---------|---------|-----------------|-------------|--------------|-------------|--------|----------|
| default | `140.3` | `362.7` | `639.8` | `21407.3` | `35811.6` | `58.399` | `0` |
| min24 | `140.4` | `347.6` | `658.7` | `22078.2` | `34783.3` | `58.353` | `420` |
| min32 | `139.7` | `350.2` | `650.1` | `21221.5` | `35246.3` | `58.642` | `386` |

- [x] **Step 5: Revert DGX stack**

Reverted the temporary patch stack, removed introduced files, and released the
lock as `FREE released-by-codex-phase58-waiting 1782901748`.

### Task 3: Decision

- [x] **Step 1: Record outcome**

Decision: keep the threshold as the best selective TTFT-defer A/B so far, but
still opt-in. MoE min32 improved aggregate, mean/max TTFT, and wall in the same
window. Dense min32 was roughly neutral with a small TTFT gain but slight
aggregate/wall loss. Next step should repeat min32 and compare against vLLM h2h
before any default-on discussion.
