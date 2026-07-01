# BF16 F32 Output Broader Serving Phase70 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** decide whether `LLAMA_BF16_CUBLAS_F32_OUT=1` has enough broader serving evidence to move beyond default-off opt-in status.

**Architecture:** Do not change source. Reuse the Phase67 DGX mirror and binary, bracket the benchmark with canonical inference gates, then run same-window llama.cpp default, llama.cpp opt-in, and vLLM serving arms across multiple concurrencies.

**Tech Stack:** llama.cpp CUDA backend, DGX GB10, `llama-server`, vLLM 0.23.0, `h2h_cli3.py`, LocalAI parity docs.

---

## Guardrails

- Do not change llama.cpp source in Phase70.
- Do not regenerate LocalAI generated patches.
- Do not push any repository.
- Confirm Docker `0`, `local-ai-worker` `0`, and GPU compute apps `0` before taking the DGX lock.
- Bracket serving with md5/op gates so inferencing safety is explicit.
- Keep `LLAMA_BF16_CUBLAS_F32_OUT=1` default-off unless broad serving is consistently flat-to-positive with gates green.

## Files

- Create: `/home/mudler/_git/LocalAI/.claude/worktrees/feat+paged-attention/docs/superpowers/plans/2026-07-01-bf16-f32-output-broader-serving-phase70.md`
- Modify after DGX run: `/home/mudler/_git/LocalAI/.claude/worktrees/feat+paged-attention/backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md`
- Modify after DGX run: `/home/mudler/_git/LocalAI/.claude/worktrees/feat+paged-attention/backend/cpp/llama-cpp-localai-paged/docs/PARITY_HANDOFF.md`
- Modify after DGX run: `/home/mudler/_git/LocalAI/.claude/worktrees/feat+paged-attention/backend/cpp/llama-cpp-localai-paged/docs/VLLM_PARITY_LEVER_MAP.md`

---

### Task 1: DGX Preflight And Gates

- [x] **Step 1: Confirm DGX idle**

Run:

```bash
ssh dgx.casa 'set -e; cat /tmp/localai-gb10.lock 2>/dev/null || true; docker ps -q | wc -l; (pgrep -af "[l]ocal-ai-worker" || true) | wc -l; nvidia-smi --query-compute-apps=pid --format=csv,noheader | sed "/^$/d" | wc -l'
```

Expected:

```text
FREE...
0
0
0
```

- [x] **Step 2: Run pre gates**

Run canonical gates with default env and opt-in completion env:

```bash
ssh dgx.casa 'ART=$HOME/bench/phase70_bf16_broader_serving/<ts>/gate_pre_default OPS=MUL_MAT,MUL_MAT_ID ~/paged-inference-gates.sh'
ssh dgx.casa 'ART=$HOME/bench/phase70_bf16_broader_serving/<ts>/gate_pre_optin OPS=MUL_MAT EXTRA_ENV="LLAMA_BF16_CUBLAS_F32_OUT=1" ~/paged-inference-gates.sh'
```

Expected:

- MoE md5 `8cb0ce23777bf55f92f63d0292c756b0`
- dense md5 `5951a5b4d624ce891e22ab5fca9bc439`
- op gates green.

Result:

- Artifact: `/home/mudler/bench/phase70_bf16_broader_serving/20260701_151500`
- Default pre gates: MoE/dense md5 matched, `MUL_MAT 1146/1146`,
  `MUL_MAT_ID 806/806`.
- Opt-in pre gates: MoE/dense md5 matched, `MUL_MAT 1146/1146`.

### Task 2: Same-Window Serving Snapshot

- [x] **Step 1: Acquire lock**

Use both active lock conventions:

```bash
ssh dgx.casa 'mkdir -p ~/gpu_bench_lock; echo "codex-phase70-bf16-broader-serving $(date +%s)" > ~/gpu_bench_lock/owner; printf "codex-phase70-bf16-broader-serving %s\n" "$(date +%s)" > /tmp/localai-gb10.lock'
```

- [x] **Step 2: Run three serving arms**

Run:

- llama.cpp default
- llama.cpp with `LLAMA_BF16_CUBLAS_F32_OUT=1`
- vLLM

Shape:

```text
model=MoE q36-35b-a3b-nvfp4
NPL=8 32 128
PTOK=128
GEN=64
PARALLEL=128
CTX=131072
```

- [x] **Step 3: Release lock**

Run:

```bash
ssh dgx.casa 'echo "FREE released-by-codex-phase70-bf16-broader-serving $(date +%s)" > ~/gpu_bench_lock/owner; printf "FREE released-by-codex-phase70-bf16-broader-serving %s\n" "$(date +%s)" > /tmp/localai-gb10.lock'
```

### Task 3: Post Gates And Decision

- [x] **Step 1: Run post gates**

Repeat default and opt-in gates after serving.

- [x] **Step 2: Summarize metrics**

Capture for each `N`:

- default vs opt-in aggregate throughput
- default vs opt-in decode aggregate throughput
- default vs opt-in TTFT
- opt-in vs vLLM decode and aggregate ratios

- [x] **Step 3: Decision**

Keep default-off if any concurrency materially regresses or if the result is mixed. Consider default-on only if all concurrencies are flat-to-positive, post gates are green, and the opt-in does not widen the vLLM parity gap.

Result summary:

| n | default agg | opt-in agg | opt/default agg | default decode | opt-in decode | opt/default decode |
|---:|------------:|-----------:|----------------:|---------------:|--------------:|-------------------:|
| `8` | `178.5` | `158.8` | `0.8896` | `242.6` | `218.3` | `0.8998` |
| `32` | `250.1` | `247.9` | `0.9912` | `418.7` | `417.6` | `0.9974` |
| `128` | `322.5` | `324.8` | `1.0071` | `706.2` | `697.9` | `0.9882` |

Decision: reject default-on. The opt-in materially regressed low-concurrency
serving and slightly widened the vLLM decode gap at `n=32` and `n=128`, despite
green gates.

### Task 4: Record And Commit

- [x] **Step 1: Update docs**

Record artifact path, gates, serving table, ratio table, and decision.

- [x] **Step 2: Commit docs**

```bash
git add -f docs/superpowers/plans/2026-07-01-bf16-f32-output-broader-serving-phase70.md
git add backend/cpp/llama-cpp-localai-paged/docs/BENCHMARK.md \
        backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md \
        backend/cpp/llama-cpp-localai-paged/docs/PARITY_HANDOFF.md \
        backend/cpp/llama-cpp-localai-paged/docs/VLLM_PARITY_LEVER_MAP.md
git commit -m "docs(paged): record BF16 F32 output broader serving phase" \
  -m "Assisted-by: Codex:gpt-5"
```
