# Phase52 Dense Admission Trace Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Use the Phase51 `LLAMA_SERVING_TRACE=1` fork patch to capture dense `n=128` llama-server admission counters and determine whether high-N serving loss is scheduler/admission-driven.

**Architecture:** Temporarily apply the Phase51 fork patch to the clean DGX mirror, build the patched server, bracket the traced serving run with canonical md5/op gates, run one dense `n=128`, `ptok=128`, `gen=64` h2h workload, parse the aggregate trace, then revert the DGX mirror.

**Tech Stack:** DGX GB10, `~/llama-phase6-source/build-cuda`, `h2h_cli3.py`, `paged-inference-gates.sh`, LocalAI parity docs.

---

### Task 1: Prepare patched DGX build

**Files:**
- DGX artifact: `/home/mudler/bench/phase52_dense_admission_trace/20260701_111017`

- [x] **Step 1: Check DGX preflight**

Observed before applying the patch: docker `0`, `local-ai-worker` `0`,
compute `0`, owner `FREE released-by-codex-phase50-dense-true-decode
1782895927`.

- [x] **Step 2: Apply Phase51 patch and build**

Applied `/tmp/phase51-serving-admission-trace.patch` to
`~/llama-phase6-source`. Built `llama-server`, `llama-completion`, and
`test-backend-ops` in `build-cuda`.

### Task 2: Gate before trace

- [x] **Step 1: Run canonical pre-trace inference gate**

Observed:

- MoE md5 `8cb0ce23777bf55f92f63d0292c756b0`
- dense md5 `5951a5b4d624ce891e22ab5fca9bc439`
- `MUL_MAT` `1146/1146`
- `MUL_MAT_ID` `806/806`

### Task 3: Run dense admission trace

- [x] **Step 1: Run warm trace**

First trace included warmup and was kept only as a secondary artifact:
`paged/`. Because `started_prompt_slots=136`, it combined warmup `n=8` and the
target `n=128` request.

- [x] **Step 2: Run clean `n=128` trace**

Clean artifact: `paged_clean/`.

H2H row:

```json
{"n": 128, "reqs": 128, "gen_total": 8192, "prompt_tok_total": 22785, "gen_per_req": 64.0, "agg_tps": 139.0, "decode_agg_tps": 360.5, "decode_perseq_tps": 1.93, "prefill_tps": 629.5, "ttft_mean_ms": 23171.5, "ttft_max_ms": 36195.3, "wall_s": 58.921}
```

Trace row:

```text
serving admission trace: steps=76 decode_only_steps=0 decode_tokens=8064 prompt_tokens=22785 waiting_prompt_slots=267 max_waiting_prompt_slots=35 started_prompt_slots=128 continued_prompt_slots=139 last_n_batch=2048 last_n_ubatch=512 last_prefill_budget_step=0 last_prefill_cap_per_slot=0
```

Parsed summary: `phase52_summary.json`.

### Task 4: Gate after trace and clean DGX

- [x] **Step 1: Run canonical post-trace inference gate**

Observed:

- MoE md5 `8cb0ce23777bf55f92f63d0292c756b0`
- dense md5 `5951a5b4d624ce891e22ab5fca9bc439`
- `MUL_MAT` `1146/1146`
- `MUL_MAT_ID` `806/806`

- [x] **Step 2: Revert temporary DGX patch**

Reverted `/tmp/phase51-serving-admission-trace.patch` from
`~/llama-phase6-source`. Final DGX state: docker `0`, `local-ai-worker` `0`,
compute `0`, owner `FREE released-by-codex-phase52-dense-admission-trace-clean
1782897309`.

### Task 5: Record decision

- [x] **Step 1: Update parity docs**

Record Phase52 artifact and interpretation:

- Prompt tokens admitted by the server trace exactly match h2h
  `prompt_tok_total`, so the trace maps to the target request.
- `decode_only_steps=0`, so the default scheduler never emits pure decode steps
  for this dense high-N serving shape.
- Prompt admission happens in `76` scheduler steps, averaging `299.8` prompt
  tokens and `106.11` decode tokens per step, with up to `35` waiting prompt
  slots.
- `prefill_budget_step=0` and `prefill_cap_per_slot=0` confirm stock
  n-batch-only prompt admission was used.
- Next candidate should be an A/B of a small, default-off admission policy or a
  trace extension with per-step histograms, not another immediate kernel rewrite.

- [x] **Step 2: Commit LocalAI docs**

Commit this plan and parity doc updates with `Assisted-by: Codex:gpt-5`.
