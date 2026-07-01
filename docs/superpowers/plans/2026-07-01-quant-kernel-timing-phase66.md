# Quant Kernel Timing Phase66 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Time the Phase65 activation-quant candidate kernels directly and decide whether a source optimization is funded.

**Architecture:** Use the already-gated Phase65 llama.cpp binary on DGX and collect an Nsight Systems CUDA kernel summary for the same MoE `npp=512`, `ntg=4`, `npl=32` prefill shape. Compare `quantize_mmq_nvfp4` and `gather_mmq_fp4` against total GPU kernel time.

**Tech Stack:** llama.cpp CUDA backend, Nsight Systems 2025.3.2, DGX GB10 benchmark host, LocalAI parity docs.

---

## Files

- Create: `/home/mudler/_git/LocalAI/.claude/worktrees/feat+paged-attention/docs/superpowers/plans/2026-07-01-quant-kernel-timing-phase66.md`
- Modify: `/home/mudler/_git/LocalAI/.claude/worktrees/feat+paged-attention/backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md`
- Modify: `/home/mudler/_git/LocalAI/.claude/worktrees/feat+paged-attention/backend/cpp/llama-cpp-localai-paged/docs/PARITY_HANDOFF.md`
- Modify: `/home/mudler/_git/LocalAI/.claude/worktrees/feat+paged-attention/backend/cpp/llama-cpp-localai-paged/docs/VLLM_PARITY_LEVER_MAP.md`

---

### Task 1: DGX Profile

- [x] **Step 1: Confirm DGX is idle**

Observed before profiling: lock `FREE`, Docker `0`, `local-ai-worker` `0`,
compute apps `0`.

- [x] **Step 2: Acquire lock**

Observed lock owner: `codex-phase66-quant-kernel-timing 1782909776`.

- [x] **Step 3: Run Nsight Systems profile**

Artifact: `/home/mudler/bench/phase66_quant_kernel_timing/20260701_144256`.

Command shape:

```bash
LLAMA_KV_PAGED=1 LLAMA_MOE_FORCE_GRAPHS=1 GGML_NO_BACKTRACE=1 GGML_CUDA_DISABLE_GRAPHS=1 \
  nsys profile --trace=cuda,nvtx --cuda-graph-trace=node --force-overwrite=true \
  --sample=none --cpuctxsw=none \
  -o "$ART/quant_npp512" \
  ./llama-batched-bench -m /home/mudler/bench/q36-35b-a3b-nvfp4.gguf \
  -c 131072 -b 2048 -ub 512 -ngl 99 -fa on -npp 512 -ntg 4 -npl 32
```

- [x] **Step 4: Generate CUDA kernel summary**

Generated:

```text
/home/mudler/bench/phase66_quant_kernel_timing/20260701_144256/quant_npp512_kern_sum_cuda_gpu_kern_sum.csv
```

---

### Task 2: Decide

- [x] **Step 1: Extract candidate kernel timing**

Observed total GPU kernel time: `7108388986 ns`.

| kernel | time | instances | share |
|--------|-----:|----------:|------:|
| `quantize_mmq_nvfp4` | `317205504 ns` | `8884` | `4.46%` |
| `gather_mmq_fp4` | `45374880 ns` | `2960` | `0.64%` |
| combined | `362580384 ns` | - | `5.10%` |

- [x] **Step 2: Source decision**

Reject a Phase66 gather/quant source optimization. `gather_mmq_fp4` is not
material, and `quantize_mmq_nvfp4 + gather_mmq_fp4` is below the `8%` source
funding threshold for this profiled shape. A W4A16/no-activation-quant rewrite
has already been rejected in earlier phases, so do not reopen it from this data.

- [x] **Step 3: Release lock**

Observed release state:

```text
FREE released-by-codex-phase66-quant-kernel-timing 1782909826
docker=0
local_ai_worker=0
compute_apps=0
```

---

### Task 3: Commit and Record

- [x] **Step 1: Record LocalAI docs**

This plan and parity docs record the Phase66 no-go decision.

- [x] **Step 2: Commit LocalAI docs**

Expected commit:

```bash
git add -f docs/superpowers/plans/2026-07-01-quant-kernel-timing-phase66.md
git add backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md \
        backend/cpp/llama-cpp-localai-paged/docs/PARITY_HANDOFF.md \
        backend/cpp/llama-cpp-localai-paged/docs/VLLM_PARITY_LEVER_MAP.md
git commit -m "docs(paged): record quant kernel timing phase" \
  -m "Assisted-by: Codex:gpt-5"
```
