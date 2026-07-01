# MTP Graph-Reuse Profile Phase 16 Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> superpowers:systematic-debugging before proposing source changes. Steps use
> checkbox (`- [ ]`) syntax for tracking.

**Goal:** validate the Phase 15 hypothesis that current MTP serving regresses
because speculative verification disrupts paged CUDA graph reuse and increases
GPU work.

**Architecture:** capture a small direct `llama-server` baseline/MTP pair under
`nsys --cuda-graph-trace=node`, using the same request shape except for
`--spec-type draft-mtp`. Do not patch code in this phase.

**Tech Stack:** llama.cpp `llama-server`, Nsight Systems, DGX GB10,
`h2h_cli3.py`.

---

## Task 1: Preflight

- [x] **Step 1: Confirm DGX is free**

  Result:

  ```text
  docker=0
  local_ai_worker=0
  compute=0
  FREE released-by-codex-phase15-mtp-serving-bench 1782872749
  ```

- [x] **Step 2: Confirm profiler is available**

  Result:

  ```text
  /usr/local/bin/nsys
  ```

## Task 2: Capture Baseline and MTP Profiles

- [x] **Step 1: Run baseline profile**

  Command shape:

  ```bash
  nsys profile --force-overwrite=true --cuda-graph-trace=node \
    --trace=cuda,nvtx,osrt --output="$ART/baseline/profile" \
    ./llama-server -m "$MODEL" -ngl 99 -fa on -c 32768 -b 2048 -ub 512 \
      --parallel 32 --host 127.0.0.1 --port 8098 --no-webui
  ```

  Client:

  ```bash
  python3 ~/bench/h2h_cli3.py --url http://127.0.0.1:8098/v1/completions \
    --model m -n 8 --ptok 64 --gen 64 --no-cache
  ```

- [x] **Step 2: Run MTP profile**

  Same as baseline plus:

  ```text
  --spec-type draft-mtp --spec-draft-n-max 3 --no-spec-draft-backend-sampling
  ```

- [x] **Step 3: Save artifacts**

  Artifact root:

  - `/home/mudler/bench/phase16_mtp_graph_profile/20260701_043016`

  Files:

  - `baseline/profile.nsys-rep`
  - `baseline/profile.sqlite`
  - `baseline/nsys_stats.txt`
  - `baseline/client.json`
  - `baseline/key_lines.txt`
  - `mtp/profile.nsys-rep`
  - `mtp/profile.sqlite`
  - `mtp/nsys_stats.txt`
  - `mtp/client.json`
  - `mtp/key_lines.txt`

## Task 3: Compare Evidence

- [x] **Step 1: Compare client throughput**

  Result:

  ```text
  baseline n=8: decode_agg_tps=230.5, decode_perseq_tps=28.07, wall_s=3.523
  MTP      n=8: decode_agg_tps= 97.7, decode_perseq_tps=12.83, wall_s=7.049
  ```

- [x] **Step 2: Compare graph reuse**

  Result:

  ```text
  baseline: graphs reused = 62
  MTP:      graphs reused = 1
  ```

- [x] **Step 3: Confirm MTP actually drafted**

  Result:

  ```text
  common_speculative_impl_draft_mtp: - n_max=3, n_min=0, p_min=0.00
  draft acceptance = 0.81481 (44 accepted / 54 generated)
  statistics draft-mtp: #gen tokens = 460, #acc tokens = 346
  ```

- [x] **Step 4: Compare GPU work**

  `nsys stats` kernel summaries show materially more GPU work for the MTP run:

  - baseline top kernel summary total is about `2.59 s` of GPU kernel time,
  - MTP top kernel summary total is about `5.89 s` of GPU kernel time.

  This supports the graph/batch-shape hypothesis and rules out a purely
  host-side or no-draft explanation.

## Task 4: Disposition

- [x] **Step 1: Record root-cause hypothesis as supported**

  Phase 16 supports the Phase 15 root cause: current MTP serving loses the
  existing paged decode graph-reuse advantage and does substantially more GPU
  work, so it is not a viable GB10 parity lever as implemented.

- [x] **Step 2: Scope the only plausible code follow-up**

  Do not tune MTP draft parameters first. A source phase would need to inspect
  `tools/server/server-context.cpp` speculative batch construction and
  `llama-graph` reuse keys to answer:

  - whether verification batches can be bucketed/reused like pure decode,
  - whether MTP draft/verify rows force graph rebuilds by changing output rows
    per sequence,
  - whether target verification can be separated from normal decode graph reuse
    without breaking rollback or greedy equivalence.

  If those answers are negative, leave MTP default-off and closed for GB10.

## Self-Review

- No source patch was made.
- The profile used `--cuda-graph-trace=node`.
- The result narrows the next work to graph/batch-shape mechanics.
