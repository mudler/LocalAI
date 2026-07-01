# Current Stack Serving Snapshot Phase 20 Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> superpowers:verification-before-completion before recording the phase result.
> Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** refresh the MoE paged-vs-vLLM serving baseline on the current clean
llama.cpp stack after the MTP investigation.

**Architecture:** benchmark only. Run the current DGX mirror
`~/llama-phase6-source` against vLLM in the same lock window with the same h2h
client, then run canonical pre/post inference gates. Do not change source.

**Tech Stack:** llama.cpp `llama-server`, vLLM `0.23.0`, DGX GB10,
`h2h_cli3.py`, LocalAI paged patch stack.

---

## Task 1: Run Current-Stack Snapshot

- [x] **Step 1: Confirm DGX is free**

  Preflight passed:

  - `docker=0`
  - `local_ai_worker=0`
  - `compute=0`

- [x] **Step 2: Build current mirror targets**

  Source:

  - `/home/mudler/llama-phase6-source`
  - HEAD: `f2521ab12 feat(server): trace speculative batch shapes`

  Build:

  ```bash
  cmake --build ~/llama-phase6-source/build-cuda \
    --target llama-server llama-completion test-backend-ops -j8
  ```

- [x] **Step 3: Run paged and vLLM serving arms**

  Artifact:

  - `/home/mudler/bench/phase20_current_snapshot/20260701_050621`

  Workload:

  - MoE Qwen3.6-35B-A3B-NVFP4
  - `NPL=8,32,128`
  - `PTOK=128`
  - `GEN=64`
  - h2h OpenAI completions client with fresh nonces

## Task 2: Verify Inference Gates

- [x] **Step 1: Pre-gate passed**

  Artifact:

  - `/home/mudler/bench/phase20_current_snapshot/20260701_050621/gate_pre`

  Result:

  - MoE md5: `8cb0ce23777bf55f92f63d0292c756b0`
  - Dense md5: `5951a5b4d624ce891e22ab5fca9bc439`
  - `MUL_MAT_ID`: `806/806`

- [x] **Step 2: Post-gate passed**

  Artifact:

  - `/home/mudler/bench/phase20_current_snapshot/20260701_050621/gate_post`

  Result:

  - MoE md5: `8cb0ce23777bf55f92f63d0292c756b0`
  - Dense md5: `5951a5b4d624ce891e22ab5fca9bc439`
  - `MUL_MAT_ID`: `806/806`

## Task 3: Snapshot Result

- [x] **Step 1: Compare serving throughput**

  | n | paged decode_agg | vLLM decode_agg | paged/vLLM decode | paged agg | vLLM agg | paged/vLLM agg |
  |---|------------------|-----------------|-------------------|-----------|----------|----------------|
  | 8 | 220.8 | 290.5 | 76.0% | 164.8 | 245.5 | 67.1% |
  | 32 | 411.1 | 594.7 | 69.1% | 252.1 | 456.0 | 55.3% |
  | 128 | 670.0 | 1022.7 | 65.5% | 322.4 | 662.4 | 48.7% |

- [x] **Step 2: Compare latency and prefill**

  | n | paged TTFT ms | vLLM TTFT ms | paged/vLLM TTFT | paged prefill_tps | vLLM prefill_tps |
  |---|---------------|--------------|------------------|--------------------|------------------|
  | 8 | 783.6 | 271.8 | 2.88x | 1669.9 | 4371.5 |
  | 32 | 2630.6 | 783.8 | 3.36x | 1712.8 | 5358.3 |
  | 128 | 7678.7 | 2465.7 | 3.11x | 1660.4 | 5242.9 |

  The current stack remains far from vLLM serving parity in e2e/TTFT because
  prefill is still much slower.

## Task 4: Decision

- [x] **Step 1: Keep GB10 shortcut closure**

  This snapshot confirms the Phase 19 direction:

  - MTP and scheduling shortcuts should stay closed.
  - Current paged serving is still below vLLM on MoE serving throughput.
  - The largest user-visible gap is prefill/TTFT, where vLLM is roughly 2.6-3.2x
    faster on this short serving snapshot.
  - The next credible parity path is not another small GB10 server shortcut; it
    is either a new-silicon rerun on datacenter Blackwell or a larger fused
    kernel project outside the low-conflict patch stack.

## Self-Review

- No source behavior changed.
- Pre/post inference gates passed.
- The result uses the current clean mirror, not the stale `llama-paged-dev`
  benchmark tree.
