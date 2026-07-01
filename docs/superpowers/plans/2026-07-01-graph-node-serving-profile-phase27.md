# Graph Node Serving Profile Phase 27 Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Re-profile the current clean llama.cpp serving path with CUDA graph
node tracing so source decisions are based on the required decode profiling
method.

**Architecture:** This is a profile-only phase. It does not edit llama.cpp
source. It runs md5/op gates before and after a graph-node-traced n128 serving
profile, then records whether the bucket decomposition changes the Phase 8
helper-dispatch decision.

**Tech Stack:** DGX GB10, llama.cpp CUDA backend, Nsight Systems
`--cuda-graph-trace=node`, `paged-inference-gates.sh`, LocalAI parity docs.

---

## Checklist

- [x] **Step 1: Confirm the profiling gap**
  - Phase 8 used an ordinary Nsight serving profile.
  - Current handoff requires `--cuda-graph-trace=node` for decode/serving
    profiles because CUDA graph replay can collapse kernel attribution.

- [x] **Step 2: Check DGX preflight before gates**
  - `docker=0`
  - `local_ai_worker=0`
  - `compute=0`
  - GPU owner file was `FREE`.

- [x] **Step 3: Run pre-profile inference gates**
  - Artifact: `/home/mudler/bench/phase27_graph_node_serving/20260701_055519/gate_pre`
  - MoE md5: `8cb0ce23777bf55f92f63d0292c756b0`
  - Dense md5: `5951a5b4d624ce891e22ab5fca9bc439`
  - `MUL_MAT_ID`: `806/806`

- [x] **Step 4: Fix Nsight session-control syntax**
  - A first attempt failed because `nsys launch` on Nsight Systems
    `2025.3.2.474-253236389321v0` rejects `--cpuctxsw`.
  - A smoke test showed the correct split:
    `nsys launch --trace=cuda --cuda-graph-trace=node ...` and
    `nsys start --sample=none --cpuctxsw=none -o OUT`.
  - Do not put `--trace`, `--cuda-graph-trace`, or `--cpuctxsw` all on both
    commands for this Nsight version.

- [x] **Step 5: Run graph-node-traced n128 serving profile**
  - Artifact: `/home/mudler/bench/phase27_graph_node_serving/20260701_055519`
  - Source: `f2521ab12 feat(server): trace speculative batch shapes`
  - Hardware: `GPU 0: NVIDIA GB10`, driver `580.159.03`, compute `12.1`
  - Serving shape: `n=128`, `PTOK=128`, `GEN=64`
  - Client result: `decode_agg_tps=675.5`, `agg_tps=319.9`,
    `prefill_tps=1671.1`, `TTFT mean=8363.4 ms`

- [x] **Step 6: Run post-profile inference gates**
  - The immediate post-gate raced with Nsight teardown and reported one compute
    process even though `nvidia-smi` printed no running processes.
  - Retried after idle preflight:
    `/home/mudler/bench/phase27_graph_node_serving/20260701_055519/gate_post_retry`
  - Retry MoE md5: `8cb0ce23777bf55f92f63d0292c756b0`
  - Retry dense md5: `5951a5b4d624ce891e22ab5fca9bc439`
  - Retry `MUL_MAT_ID`: `806/806`

- [x] **Step 7: Bucket the graph-node trace**
  - `buckets.txt` was generated from
    `llama_graph_node.nsys-rep`.
  - Macro buckets:
    - GDN: `6706.33 ms` (`33.47%`)
    - MoE/FFN-GEMM: `5871.92 ms` (`29.31%`)
    - bf16-proj: `2725.07 ms` (`13.60%`)
    - layout-copy: `1309.99 ms` (`6.54%`)
    - act-quant: `697.75 ms` (`3.48%`)
    - MoE-dispatch: `275.99 ms` (`1.38%`)
    - FA: `271.03 ms` (`1.35%`)

- [x] **Step 8: Record decision**
  - Fine rows confirm the Phase 8 source shortcut rejection:
    `gdn_core=29.59%`, `mmq_nvfp4=28.44%`, `mm_ids=0.61%`,
    `gather_mmq=0.37%`, `argsort_topk=0.40%`.
  - Do not reopen metadata/helper-only MoE dispatch work on GB10.
  - A credible patch must directly reduce GDN, grouped-MMQ, or projection work
    while preserving md5/op gates.

## Result

Phase 27 strengthens the profile basis for the current GB10 conclusion. It does
not find a new low-conflict source shortcut. The profile is representative of
Phase 26 n128 serving throughput and keeps the inference gates green after a
post-teardown retry.
