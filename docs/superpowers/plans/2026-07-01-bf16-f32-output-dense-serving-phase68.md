# BF16 F32 Output Dense Serving Phase68 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Decide whether `LLAMA_BF16_CUBLAS_F32_OUT=1` has enough dense and serving value to consider a default policy change.

**Architecture:** Reuse the Phase67 source patch and DGX build. Run dense prefill A/B first because it is fast and directly targets BF16 projections. Run serving A/B only if dense or MoE evidence supports a broader default-on question.

**Tech Stack:** llama.cpp CUDA backend, DGX GB10, `llama-batched-bench`, optional LocalAI serving snapshot harness, LocalAI parity docs.

---

## Guardrails

- Do not change source in Phase68.
- Do not make `LLAMA_BF16_CUBLAS_F32_OUT=1` default-on from MoE prefill alone.
- Keep DGX lock discipline: lock free, Docker `0`, `local-ai-worker` `0`, compute apps `0`.
- Keep existing md5/op gate evidence from Phase67 as the correctness basis for this exact source commit.
- Record no-go results as explicitly as wins.

## Files

- Create: `/home/mudler/_git/LocalAI/.claude/worktrees/feat+paged-attention/docs/superpowers/plans/2026-07-01-bf16-f32-output-dense-serving-phase68.md`
- Modify after DGX run: `/home/mudler/_git/LocalAI/.claude/worktrees/feat+paged-attention/backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md`
- Modify after DGX run: `/home/mudler/_git/LocalAI/.claude/worktrees/feat+paged-attention/backend/cpp/llama-cpp-localai-paged/docs/PARITY_HANDOFF.md`
- Modify after DGX run: `/home/mudler/_git/LocalAI/.claude/worktrees/feat+paged-attention/backend/cpp/llama-cpp-localai-paged/docs/VLLM_PARITY_LEVER_MAP.md`

---

### Task 1: Dense Prefill A/B

- [x] **Step 1: Confirm DGX idle and acquire lock**

Run:

```bash
ssh dgx.casa 'cat /tmp/localai-gb10.lock 2>/dev/null || true; docker ps --format "{{.Names}}" | wc -l; (pgrep -af "[l]ocal-ai-worker" || true) | wc -l; nvidia-smi --query-compute-apps=pid,process_name,used_gpu_memory --format=csv,noheader | wc -l'
ssh dgx.casa 'printf "codex-phase68-bf16-dense-serving %s\n" "$(date +%s)" > /tmp/localai-gb10.lock'
```

- [x] **Step 2: Run dense prefill default and opt-in**

Run:

```bash
./llama-batched-bench -m /home/mudler/bench/q36-27b-nvfp4.gguf \
  -c 131072 -b 2048 -ub 512 -ngl 99 -fa on -npp 512,2048 -ntg 4 -npl 32
```

with and without `LLAMA_BF16_CUBLAS_F32_OUT=1`.

- [x] **Step 3: Dense decision**

Dense improved slightly in the same window and did not regress:

| npp | default S_PP | opt-in S_PP | change |
|-----|-------------:|------------:|-------:|
| `512` | `973.13` | `975.52` | `+0.25%` |
| `2048` | `1019.88` | `1021.39` | `+0.15%` |

Decision: run a small MoE serving A/B because Phase67 MoE prefill was positive
and dense did not regress. The dense win is too small to justify default-on by
itself.

---

### Task 2: Serving A/B If Funded

- [x] **Step 1: Run a small same-window serving A/B**

Use the current clean source tree and the existing h2h client or snapshot harness.
Compare default versus:

```bash
LLAMA_BF16_CUBLAS_F32_OUT=1
```

At minimum capture MoE `N=128`, prompt `128`, generation `128` aggregate,
decode aggregate, mean TTFT, wall time, and md5 gate summary.

- [x] **Step 2: Serving decision**

Keep default-off unless serving improves or is flat without dense regression.
Do not default-on from prefill-only evidence.

Serving artifact:

- `/home/mudler/bench/phase68_bf16_dense_serving/20260701_145710/serving_ab_20260701_150249`

MoE serving A/B, `N=128`, prompt `128`, generation `128`, `--parallel 128`:

| metric | default | opt-in | change |
|--------|--------:|-------:|-------:|
| `agg_tps` | `409.8` | `415.0` | `+1.27%` |
| `decode_agg_tps` | `615.3` | `627.2` | `+1.93%` |
| `decode_perseq_tps` | `4.15` | `4.16` | `+0.24%` |
| `prefill_tps` | `1630.2` | `1648.0` | `+1.09%` |
| `ttft_mean_ms` | `8574.7` | `8085.9` | `-5.70%` |
| `wall_s` | `39.978` | `39.480` | `-1.25%` |

Decision: keep `LLAMA_BF16_CUBLAS_F32_OUT=1` default-off but promoted as a
safe opt-in shortcut candidate. It now has Phase67 MoE md5/op gates, Phase67
dense md5/op gates, a tiny positive dense prefill result, and a positive small
MoE serving A/B. Do not make it default-on until it is patch-series mirrored and
retested in a broader serving snapshot.

---

### Task 3: Record and Commit

- [x] **Step 1: Release DGX lock**

Run:

```bash
ssh dgx.casa 'printf "FREE released-by-codex-phase68-bf16-dense-serving %s\n" "$(date +%s)" > /tmp/localai-gb10.lock'
```

- [x] **Step 2: Record docs**

Record artifact path, dense A/B, serving A/B if run, and decision.

- [x] **Step 3: Commit LocalAI docs**

```bash
git add -f docs/superpowers/plans/2026-07-01-bf16-f32-output-dense-serving-phase68.md
git add backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md \
        backend/cpp/llama-cpp-localai-paged/docs/PARITY_HANDOFF.md \
        backend/cpp/llama-cpp-localai-paged/docs/VLLM_PARITY_LEVER_MAP.md
git commit -m "docs(paged): record BF16 F32 output dense serving phase" \
  -m "Assisted-by: Codex:gpt-5"
```
