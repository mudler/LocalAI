# Phase53 Admission Budget Sweep Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Test whether existing default-off scheduler knobs (`LLAMA_MAX_BATCH_TOKENS`, `LLAMA_PREFILL_CAP`) improve dense `n=128` serving enough to pursue a scheduler policy patch.

**Architecture:** Temporarily apply the Phase51 trace patch to the clean DGX mirror, build the patched server, bracket the sweep with canonical md5/op gates, run dense `n=128`, `ptok=128`, `gen=64` variants, parse h2h plus admission trace rows, then revert the DGX mirror.

**Tech Stack:** DGX GB10, llama.cpp `build-cuda`, `LLAMA_SERVING_TRACE=1`, `h2h_cli3.py`, `paged-inference-gates.sh`.

---

### Task 1: Prepare patched DGX trace build

- [x] **Step 1: Check preflight**

Artifact: `/home/mudler/bench/phase53_dense_admission_budget_sweep/20260701_111915`.
Preflight: docker `0`, `local-ai-worker` `0`, compute `0`, owner
`FREE released-by-codex-phase52-dense-admission-trace-clean 1782897309`.

- [x] **Step 2: Apply Phase51 patch and build**

Applied `/tmp/phase51-serving-admission-trace.patch` to
`~/llama-phase6-source`. Built `llama-server`, `llama-completion`, and
`test-backend-ops` in `build-cuda`.

### Task 2: Gate before sweep

- [x] **Step 1: Run canonical pre-sweep gate**

Observed:

- MoE md5 `8cb0ce23777bf55f92f63d0292c756b0`
- dense md5 `5951a5b4d624ce891e22ab5fca9bc439`
- `MUL_MAT` `1146/1146`
- `MUL_MAT_ID` `806/806`

### Task 3: Run budget variants

- [x] **Step 1: Run `T=1536`, `cap=512`**

Environment: `LLAMA_MAX_BATCH_TOKENS=1536 LLAMA_PREFILL_CAP=512`.

Result:

```text
agg=134.4 decode_agg=376.7 perseq=1.82 prefill=607.0 ttft=22263.7 wall=60.968
steps=81 decode_only_steps=0 prompt_tokens=23809 max_waiting_prompt_slots=26 prefill_budget_step=1535 prefill_cap_per_slot=512
```

- [x] **Step 2: Run `T=1024`, `cap=512`**

Environment: `LLAMA_MAX_BATCH_TOKENS=1024 LLAMA_PREFILL_CAP=512`.

Result:

```text
agg=130.0 decode_agg=392.4 perseq=1.82 prefill=565.2 ttft=23234.3 wall=63.003
steps=89 decode_only_steps=0 prompt_tokens=23809 max_waiting_prompt_slots=16 prefill_budget_step=1021 prefill_cap_per_slot=512
```

### Task 4: Parse and decide

- [x] **Step 1: Write `summary.tsv`**

Summary:

| variant | agg t/s | decode agg t/s | prefill t/s | TTFT mean ms | wall s | steps | max waiting prompt slots |
|---------|---------|-----------------|-------------|--------------|--------|-------|--------------------------|
| default Phase52 | `139.0` | `360.5` | `629.5` | `23171.5` | `58.921` | `76` | `35` |
| `T=1536 cap=512` | `134.4` | `376.7` | `607.0` | `22263.7` | `60.968` | `81` | `26` |
| `T=1024 cap=512` | `130.0` | `392.4` | `565.2` | `23234.3` | `63.003` | `89` | `16` |

Decision: simple budget shrinkage trades aggregate/prefill throughput for a
higher h2h decode-agg metric and does not materially solve TTFT. Do not promote
these knobs as a parity lever. The next step should be either per-step histogram
tracing or a more targeted policy that improves first-token admission without
starving prefill throughput.

### Task 5: Gate after sweep and clean DGX

- [x] **Step 1: Run canonical post-sweep gate**

Observed:

- MoE md5 `8cb0ce23777bf55f92f63d0292c756b0`
- dense md5 `5951a5b4d624ce891e22ab5fca9bc439`
- `MUL_MAT` `1146/1146`
- `MUL_MAT_ID` `806/806`

- [x] **Step 2: Revert temporary DGX patch**

Reverted the Phase51 patch from `~/llama-phase6-source`. Final DGX state:
docker `0`, `local-ai-worker` `0`, compute `0`, owner
`FREE released-by-codex-phase53-budget-sweep 1782897825`.

- [x] **Step 3: Commit docs**

Commit this plan and parity doc updates with `Assisted-by: Codex:gpt-5`.
