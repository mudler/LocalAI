# MTP Verify Cost Phase 62 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** decide whether MTP can still be a GB10 parity lever by separating draft acceptance from target-verify graph cost, while proving default inference remains unchanged.

**Architecture:** use the existing llama.cpp MTP implementation and existing server-side speculative telemetry first. Do not change inference behavior in this phase. Run default greedy-md5 and backend-op gates before and after each DGX serving sweep, then compare baseline, current MTP, and bounded MTP configurations on throughput, graph reuse, acceptance rate, mean acceptance length, per-position acceptance, and output-row expansion.

**Tech Stack:** llama.cpp `llama-server`, LocalAI paged harnesses, DGX GB10, `h2h_cli3.py`, `paged-inference-gates.sh`, existing `LLAMA_SPEC_SHAPE_TRACE=1` instrumentation.

---

## Files

- Modify: `docs/superpowers/plans/2026-07-01-mtp-verify-cost-phase62.md`
- Modify: `backend/cpp/llama-cpp-localai-paged/docs/PARITY_HANDOFF.md`
- Modify: `backend/cpp/llama-cpp-localai-paged/docs/VLLM_PARITY_LEVER_MAP.md`
- Modify: `backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md`
- Possible modify after this phase, only if Task 4 approves it: `/home/mudler/_git/llama.cpp/tools/server/server-context.cpp`
- Possible test after this phase, only if Task 4 approves source changes: `/home/mudler/_git/llama.cpp/tests/`

## Existing Evidence To Respect

- Phase 15 proved MTP drafts are accepted but serving throughput regresses:

  ```text
  statistics        draft-mtp: #gen tokens = 17293, #acc tokens = 15493
  n=128 baseline decode_agg_tps 662.4
  n=128 mtp decode_agg_tps      138.5
  ```

- Phase 19 proved draft length entropy is not the obvious culprit:

  ```text
  n128 draft counts: {1: 40, 2: 49, 3: 2264}
  top draft share: 96.2%
  unique total rows: 34
  ```

- Current llama.cpp already prints:

  ```text
  draft acceptance = ... mean acceptance length = ... acceptance rate per position = (...)
  statistics        draft-mtp: ... #gen tokens = ... #acc tokens = ... #mean acc len = ... #acc rate/pos = (...)
  graphs reused = ...
  ```

  Therefore this phase must not waste time adding duplicate acceptance counters unless the sweep shows a missing attribution that blocks the decision.

## Task 1: Safety Preflight And Baseline Gates

- [x] **Step 1: Confirm DGX is idle**

  Run on `dgx.casa`:

  ```bash
  docker_count=$(docker ps -q | wc -l)
  local_ai=$(docker ps --format '{{.Names}}' | grep -c local-ai-worker || true)
  compute=$(nvidia-smi --query-compute-apps=pid --format=csv,noheader | sed '/^$/d' | wc -l)
  owner=FREE-no-lock-file
  test -f "$HOME/gpu_bench_lock/owner" && owner=$(cat "$HOME/gpu_bench_lock/owner")
  printf 'docker=%s\nlocal_ai_worker=%s\ncompute=%s\nowner=%s\n' "$docker_count" "$local_ai" "$compute" "$owner"
  ```

  Expected:

  ```text
  docker=0
  local_ai_worker=0
  compute=0
  owner=FREE...
  ```

  Observed on 2026-07-01:

  ```text
  docker=0
  local_ai_worker=0
  compute=0
  owner=FREE released-by-codex-phase53-budget-sweep 1782897825
  ```

- [x] **Step 2: Run default inference gates before any MTP sweep**

  Run on `dgx.casa`:

  ```bash
  ART_ROOT="$HOME/bench/phase62_mtp_verify_cost/$(date +%Y%m%d_%H%M%S)"
  mkdir -p "$ART_ROOT"
  ART="$ART_ROOT/gate_pre" "$HOME/paged-inference-gates.sh" > "$ART_ROOT/gate_pre.log" 2>&1
  cat "$ART_ROOT/gate_pre.log"
  printf '%s\n' "$ART_ROOT" > "$HOME/bench/phase62_mtp_verify_cost/latest_artifact.txt"
  ```

  Expected:

  ```text
  moe md5 OK: 8cb0ce23777bf55f92f63d0292c756b0
  dense md5 OK: 5951a5b4d624ce891e22ab5fca9bc439
    806/806 tests passed
    Backend CUDA0: OK
  paged inference gates OK
  ```

  Artifact:

  ```text
  /home/mudler/bench/phase62_mtp_verify_cost/20260701_134125/gate_pre
  ```

  Observed:

  ```text
  moe md5 OK: 8cb0ce23777bf55f92f63d0292c756b0
  dense md5 OK: 5951a5b4d624ce891e22ab5fca9bc439
    806/806 tests passed
    Backend CUDA0: OK
  paged inference gates OK
  ```

- [x] **Step 3: Record the artifact path in this plan**

  Add the concrete artifact path and the gate output summary under `Task 1` after the run finishes.

## Task 2: Run A Small MTP Verify-Cost Sweep

- [x] **Step 1: Let the serving harness acquire the GPU lock**

  Do not write `~/gpu_bench_lock/owner` manually before calling
  `paged-mtp-serving-bench.sh`. That harness runs its own preflight, takes the
  owner-file lock after its pre-gate, and releases it before its post-gate.
  Confirm the lock is still free:

  ```bash
  owner=FREE-no-lock-file
  test -f "$HOME/gpu_bench_lock/owner" && owner=$(cat "$HOME/gpu_bench_lock/owner")
  printf 'owner=%s\n' "$owner"
  ```

  Observed before harness start:

  ```text
  owner=FREE released-by-codex-phase53-budget-sweep 1782897825
  ```

- [x] **Step 2: Run baseline and MTP arms with shape trace**

  Use the existing harness first so Phase 62 is comparable to Phases 15 and 19:

  ```bash
  ART="$(cat "$HOME/bench/phase62_mtp_verify_cost/latest_artifact.txt")"
  LLAMA_SPEC_SHAPE_TRACE=1 \
    ART="$ART" \
    NPL="8 32 128" \
    GEN=64 \
    PTOK=128 \
    SRC="$HOME/llama-phase6-source" \
    "$HOME/paged-mtp-serving-bench.sh"
  ```

  Expected files:

  ```text
  $ART/summary.tsv
  $ART/baseline/server.log
  $ART/mtp/server.log
  $ART/mtp/spec_lines.txt
  $ART/gate_pre.log
  $ART/gate_post.log
  ```

  Artifact:

  ```text
  /home/mudler/bench/phase62_mtp_verify_cost/20260701_134125
  ```

  Summary:

  ```text
  arm       n    decode_agg_tps  ttft_mean_ms  wall_s
  baseline 8    248.5           1150.4        3.214
  mtp      8    104.4           1682.9        6.591
  baseline 32   411.8           2607.9        8.116
  mtp      32   112.8           4444.7        24.078
  baseline 128  696.5           7425.2        24.570
  mtp      128  148.1           20155.8       99.787
  ```

- [x] **Step 3: Confirm the GPU lock was released**

  Run after the harness exits:

  ```bash
  pkill -f 'llama-server.*8097' || true
  owner=FREE-no-lock-file
  test -f "$HOME/gpu_bench_lock/owner" && owner=$(cat "$HOME/gpu_bench_lock/owner")
  printf 'owner=%s\n' "$owner"
  ```

  Expected: `owner=FREE...`.

  Observed:

  ```text
  docker=0
  local_ai_worker=0
  compute=0
  owner=FREE released-by-codex-phase15-mtp-serving-bench 1782906420
  ```

- [x] **Step 4: Confirm the post-gate remained green**

  Inspect:

  ```bash
  ART="$(cat "$HOME/bench/phase62_mtp_verify_cost/latest_artifact.txt")"
  cat "$ART/gate_post.log"
  ```

  Expected:

  ```text
  moe md5 OK: 8cb0ce23777bf55f92f63d0292c756b0
  dense md5 OK: 5951a5b4d624ce891e22ab5fca9bc439
    806/806 tests passed
    Backend CUDA0: OK
  paged inference gates OK
  ```

  Observed:

  ```text
  moe md5 OK: 8cb0ce23777bf55f92f63d0292c756b0
  dense md5 OK: 5951a5b4d624ce891e22ab5fca9bc439
    806/806 tests passed
    Backend CUDA0: OK
  paged inference gates OK
  ```

## Task 3: Parse Acceptance, Graph Reuse, And Output-Row Cost

- [x] **Step 1: Extract throughput**

  Run on `dgx.casa`:

  ```bash
  ART="$(cat "$HOME/bench/phase62_mtp_verify_cost/latest_artifact.txt")"
  cat "$ART/summary.tsv"
  ```

  Record `decode_agg_tps`, `ttft_mean_ms`, and `wall_s` for each arm and concurrency.

  MTP decode ratios:

  ```text
  n=8    104.4 / 248.5 = 0.420
  n=32   112.8 / 411.8 = 0.274
  n=128  148.1 / 696.5 = 0.213
  ```

- [x] **Step 2: Extract MTP acceptance and graph reuse**

  Run on `dgx.casa`:

  ```bash
  ART="$(cat "$HOME/bench/phase62_mtp_verify_cost/latest_artifact.txt")"
  grep -E 'draft acceptance|statistics[[:space:]]+draft-mtp|graphs reused' "$ART/mtp/server.log" > "$ART/mtp/phase62_mtp_stats.txt" || true
  cat "$ART/mtp/phase62_mtp_stats.txt"
  ```

  Record:

  ```text
  draft_n generated
  draft_n accepted
  draft acceptance ratio
  mean acceptance length
  acceptance rate per position
  graphs reused
  ```

  Final cumulative MTP statistics:

  ```text
  #gen tokens = 9340
  #acc tokens = 7372
  acceptance = 0.78929
  #mean acc len = 3.33
  #acc rate/pos = (0.877, 0.767, 0.691)
  graphs reused = 1
  ```

- [x] **Step 3: Parse shape-trace row expansion**

  Run on `dgx.casa`:

  ```bash
  ART="$(cat "$HOME/bench/phase62_mtp_verify_cost/latest_artifact.txt")"
  python3 - "$ART/mtp/server.log" > "$ART/phase62_shape_rows.tsv" <<'PY'
  import re
  import sys
  from collections import Counter
  path = sys.argv[1]
  total_rows = Counter()
  draft_rows = Counter()
  for line in open(path, errors="ignore"):
      if "spec shape:" not in line:
          continue
      m = re.search(r"batch_after=(\d+)", line)
      if m:
          total_rows[int(m.group(1))] += 1
      m = re.search(r"draft=(\d+)", line)
      if m:
          draft_rows[int(m.group(1))] += 1
  print("kind\tvalue\tcount")
  for value, count in sorted(total_rows.items()):
      print(f"batch_after\t{value}\t{count}")
  for value, count in sorted(draft_rows.items()):
      print(f"draft\t{value}\t{count}")
  PY
  cat "$ART/phase62_shape_rows.tsv"
  ```

  The initial parser used the wrong uppercase marker. The real marker is:

  ```bash
  grep -m 20 'spec shape:' "$ART/mtp/server.log"
  ```

  Final shape summary:

  ```text
  rows total 3212; rows=4: 3070 (95.6%)
  draft total 3212; draft=3: 3070 (95.6%)
  batch_after total 3212; unique values 495
  ```

## Task 4: Decide Whether A Source Patch Is Justified

- [x] **Step 1: Apply the MTP keep/reject rule**

  Keep MTP as a candidate only if all of these are true:

  ```text
  acceptance ratio >= 0.70
  mean acceptance length >= 2.0
  MTP decode_agg_tps >= 0.75 * baseline decode_agg_tps for at least n=8 or n=32
  post-gate md5 and MUL_MAT_ID remain green
  ```

  Reject another MTP implementation phase for now if acceptance is high but throughput remains below `0.75x` baseline, because that means verification/output-row cost still dominates.

  Decision: reject another MTP implementation phase for now. Acceptance is high
  (`0.789`, mean acceptance length `3.33`) but throughput is only `0.420x`,
  `0.274x`, and `0.213x` baseline decode at `n=8`, `n=32`, and `n=128`.

- [x] **Step 2: If rejected, mark the reason in docs**

  Update:

  - `backend/cpp/llama-cpp-localai-paged/docs/PARITY_HANDOFF.md`
  - `backend/cpp/llama-cpp-localai-paged/docs/VLLM_PARITY_LEVER_MAP.md`
  - `backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md`

  Required wording:

  ```text
  Phase62 kept default inference green with md5/op gates, but MTP remains rejected unless a later design removes target-verify/output-row graph cost. Do not tune n_max blindly.
  ```

- [x] **Step 3: If kept, scope Phase 63 as a TDD source patch**

  The keep rule did not pass, so no Phase 63 MTP source patch is scoped.

## Task 5: Commit Documentation

- [ ] **Step 1: Verify docs are clean**

  Run locally:

  ```bash
  git diff --check
  ```

  Expected: no output.

- [ ] **Step 2: Commit the phase docs**

  Run locally:

  ```bash
  git add docs/superpowers/plans/2026-07-01-mtp-verify-cost-phase62.md \
    backend/cpp/llama-cpp-localai-paged/docs/PARITY_HANDOFF.md \
    backend/cpp/llama-cpp-localai-paged/docs/VLLM_PARITY_LEVER_MAP.md \
    backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md
  git commit -m "docs(paged): scope MTP verify-cost phase" \
    -m "Assisted-by: Codex:gpt-5"
  ```

## Self-Review

- [x] No source behavior changes are planned before measurement.
- [x] The phase explicitly gates default inference with MoE md5, dense md5, and backend op checks before and after.
- [x] The plan uses current code reality: acceptance and per-position stats already exist.
- [x] The go/no-go rule prevents blind MTP `n_max` tuning after Phase 15 and Phase 19.
