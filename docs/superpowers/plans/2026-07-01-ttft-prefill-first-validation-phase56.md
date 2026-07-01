# Phase56 TTFT Prefill-First Validation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Validate the Phase55 default-off `LLAMA_TTFT_PREFILL_FIRST=1` scheduler A/B beyond dense `n=128` before any default-on discussion.

**Architecture:** Do not change code. Temporarily apply the already-local Phase51+Phase54+Phase55 fork stack to the clean DGX mirror, reuse the gated `build-cuda` path, bracket runs with md5/op gates, then compare default vs opt-in on MoE `n=128` and dense lower-concurrency `n=32`.

**Tech Stack:** DGX GB10, llama.cpp `build-cuda`, `LLAMA_SERVING_TRACE=1`, `LLAMA_TTFT_PREFILL_FIRST=1`, `h2h_cli.py`, `paged-inference-gates.sh`.

---

### Task 1: Prepare DGX Stack

- [x] **Step 1: Preflight**

Require: Docker `0`, `local-ai-worker` `0`, GPU compute apps `0`, lock `FREE*`,
and clean `~/llama-phase6-source`.

Observed: docker `0`, `local-ai-worker` `0`, compute `0`, lock
`FREE released-by-codex-phase55-ttft 1782899730`, DGX mirror clean at
`2cbb61969443cf52aa1aa58eb9f5a8d7c20a7780`.

- [x] **Step 2: Apply stack and build**

Apply `/tmp/phase55-ttft-prefill-first-stack.patch` or regenerate the same stack
from `/home/mudler/_git/llama.cpp`. Reconfigure CMake if needed, then build
`llama-server`, `llama-cli`, and `test-backend-ops`.

Observed: stack applied, CMake reconfigured, and requested targets built.

### Task 2: Gate Before Validation

- [x] **Step 1: Run canonical pre-validation gate**

Expected:

- MoE md5 `8cb0ce23777bf55f92f63d0292c756b0`
- dense md5 `5951a5b4d624ce891e22ab5fca9bc439`
- `MUL_MAT` `1146/1146`
- `MUL_MAT_ID` `806/806`

Observed: all expected pre-validation gates matched.

### Task 3: Run A/B Matrix

- [x] **Step 1: Run MoE `n=128` default and opt-in**

Model: `~/bench/q36-35b-a3b-nvfp4.gguf`.
Shape: `--parallel 128`, `-c 131072`, `-b 2048`, `-ub 512`, `n=128`,
`ptok=128`, `gen=64`.

Default:

```json
{"n": 128, "reqs": 128, "gen_total": 8191, "prompt_tok_total": 17793, "gen_per_req": 64.0, "agg_tps": 341.1, "decode_agg_tps": 651.2, "decode_perseq_tps": 3.93, "prefill_tps": 1555.9, "ttft_mean_ms": 7168.1, "ttft_max_ms": 11435.5, "wall_s": 24.015}
```

`LLAMA_TTFT_PREFILL_FIRST=1`:

```json
{"n": 128, "reqs": 128, "gen_total": 8192, "prompt_tok_total": 17793, "gen_per_req": 64.0, "agg_tps": 339.9, "decode_agg_tps": 623.8, "decode_perseq_tps": 3.92, "prefill_tps": 1622.7, "ttft_mean_ms": 7615.3, "ttft_max_ms": 10964.4, "wall_s": 24.098}
```

- [x] **Step 2: Run dense `n=32` default and opt-in**

Model: `~/bench/q36-27b-nvfp4.gguf`.
Shape: `--parallel 128`, `-c 131072`, `-b 2048`, `-ub 512`, `n=32`,
`ptok=168`, `gen=64`.

Default:

```json
{"n": 32, "reqs": 32, "gen_total": 2048, "prompt_tok_total": 5700, "gen_per_req": 64.0, "agg_tps": 104.3, "decode_agg_tps": 197.1, "decode_perseq_tps": 5.42, "prefill_tps": 617.2, "ttft_mean_ms": 7687.7, "ttft_max_ms": 9234.4, "wall_s": 19.627}
```

`LLAMA_TTFT_PREFILL_FIRST=1`:

```json
{"n": 32, "reqs": 32, "gen_total": 2048, "prompt_tok_total": 5700, "gen_per_req": 64.0, "agg_tps": 106.7, "decode_agg_tps": 193.5, "decode_perseq_tps": 5.37, "prefill_tps": 662.1, "ttft_mean_ms": 7284.3, "ttft_max_ms": 8609.1, "wall_s": 19.194}
```

### Task 4: Gate After Validation and Clean DGX

- [x] **Step 1: Run canonical post-validation gate**

Expected md5/op values match Task 2.

Observed: all expected post-validation gates matched.

- [x] **Step 2: Revert temporary DGX stack**

Reverse the patch, remove untracked files introduced by the stack, release the
lock, and verify no compute apps remain.

Observed: stack reverted, introduced files removed, lock released as
`FREE released-by-codex-phase56-validation 1782900217`, and no compute apps
were reported.

### Task 5: Record Decision

- [x] **Step 1: Update parity docs**

Record the artifact, all A/B rows, trace counters, gates, and whether the policy
remains promising, is rejected, or needs narrower gating.

Decision: keep the policy opt-in only. Dense `n=32` improved aggregate and TTFT,
but MoE `n=128` slightly regressed aggregate and mean TTFT, so the policy is not
safe as a broad default.

- [x] **Step 2: Commit LocalAI docs**

Use:

```text
docs(paged): validate TTFT prefill-first A/B

Assisted-by: Codex:gpt-5
```
