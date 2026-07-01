# Phase57 TTFT Prefill-First Cap Sweep Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Test whether a per-step cap on `LLAMA_TTFT_PREFILL_FIRST=1` avoids the MoE mean-TTFT regression seen in Phase56 while preserving dense gains.

**Architecture:** Add a small optional cap to the existing default-off Phase55 policy. Unset or zero cap keeps Phase55 unlimited behavior. Gate with focused unit tests, then temporarily apply the stack to DGX for md5/op gates and an A/B cap sweep.

**Tech Stack:** llama.cpp fork, `tools/server/server-admission-policy.h`, `tools/server/server-context.cpp`, DGX GB10, `h2h_cli.py`, `paged-inference-gates.sh`.

---

### Task 1: Add capped helper

- [x] **Step 1: Write red test**

Added test cases for:

- zero cap means unlimited
- below cap defers
- at cap stops deferring

Observed red failure: the helper accepted only three arguments.

- [x] **Step 2: Implement cap helper and env**

Added overload:

```cpp
server_admission_should_defer_decode_for_ttft(enabled, prompt_waiting, n_decoded, deferred_so_far, max_deferred)
```

Added `LLAMA_TTFT_PREFILL_FIRST_MAX_DEFER`. Unset or `0` keeps unlimited
Phase55 behavior.

- [x] **Step 3: Verify local**

Commands passed:

```bash
cmake --build build --target test-server-admission-policy test-server-admission-trace llama-server -j2
./build/bin/test-server-admission-policy
./build/bin/test-server-admission-trace
ctest --test-dir build -R 'test-server-admission-(policy|trace)' --output-on-failure
```

- [x] **Step 4: Commit fork patch**

Local fork commit:

```text
3b6ab5fa8 feat(server): cap TTFT prefill-first decode deferral
```

### Task 2: DGX gate and cap sweep

- [x] **Step 1: Preflight and build**

Preflight: docker `0`, `local-ai-worker` `0`, compute `0`, lock
`FREE released-by-codex-phase56-validation 1782900217`, clean mirror at
`2cbb61969443cf52aa1aa58eb9f5a8d7c20a7780`.

Applied `/tmp/phase57-ttft-cap-stack.patch`, built focused tests,
`llama-server`, `llama-cli`, and `test-backend-ops`. DGX focused CTests passed.

- [x] **Step 2: Run pre/post gates**

Artifact: `/home/mudler/bench/phase57_ttft_cap_sweep/20260701_120830`.

Pre and post gates matched:

- MoE md5 `8cb0ce23777bf55f92f63d0292c756b0`
- dense md5 `5951a5b4d624ce891e22ab5fca9bc439`
- `MUL_MAT` `1146/1146`
- `MUL_MAT_ID` `806/806`

- [x] **Step 3: Run MoE cap sweep**

MoE `n=128`, `ptok=128`, `gen=64`:

| variant | agg t/s | decode agg t/s | prefill t/s | TTFT mean ms | TTFT max ms | wall s | deferred |
|---------|---------|-----------------|-------------|--------------|-------------|--------|----------|
| default | `337.1` | `652.0` | `1516.1` | `7425.5` | `11735.7` | `24.299` | `0` |
| cap16 | `330.2` | `611.5` | `1559.6` | `7589.4` | `11407.9` | `24.802` | `111` |
| cap32 | `335.3` | `624.6` | `1572.4` | `6994.0` | `11315.5` | `24.429` | `236` |
| cap64 | `327.1` | `589.6` | `1596.9` | `7533.2` | `11141.5` | `25.025` | `339` |

- [x] **Step 4: Run dense cap sweep**

Dense `n=128`, `ptok=168`, `gen=64`:

| variant | agg t/s | decode agg t/s | prefill t/s | TTFT mean ms | TTFT max ms | wall s | deferred |
|---------|---------|-----------------|-------------|--------------|-------------|--------|----------|
| default | `141.4` | `360.6` | `650.8` | `22423.5` | `35209.6` | `57.925` | `0` |
| cap32 | `139.7` | `340.1` | `663.1` | `20346.5` | `34556.0` | `58.645` | `322` |
| cap64 | `136.3` | `333.4` | `645.2` | `22461.1` | `35511.7` | `60.081` | `490` |

- [x] **Step 5: Revert DGX stack**

Reverted the temporary patch stack, removed introduced files, and released the
lock as `FREE released-by-codex-phase57-cap 1782901003`.

### Task 3: Decision

- [x] **Step 1: Record outcome**

Decision: reject the cap as a parity lever. MoE cap32 improves mean TTFT versus
same-window default but still slightly loses aggregate and wall. Dense caps lose
aggregate versus the same-window default, and cap64 is broadly worse.
