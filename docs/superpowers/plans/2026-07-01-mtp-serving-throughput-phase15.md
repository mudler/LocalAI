# MTP Serving Throughput Phase 15 Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> superpowers:subagent-driven-development or superpowers:executing-plans to
> implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for
> tracking.

**Goal:** measure whether Phase 14's safe MTP path improves real
`llama-server` serving throughput on GB10.

**Architecture:** use direct `llama-server` first, not LocalAI, so the benchmark
isolates llama.cpp serving behavior. Compare two same-shape arms: baseline with
no speculative decoding and MTP with `--spec-type draft-mtp`. Run canonical
inference gates before and after the A/B.

**Tech Stack:** llama.cpp `llama-server`, DGX GB10, `h2h_cli3.py`,
`paged-inference-gates.sh`.

---

## Files

- Create: `backend/cpp/llama-cpp-localai-paged/paged-mtp-serving-bench.sh`
- Modify: `backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md`
- Modify: `backend/cpp/llama-cpp-localai-paged/docs/VLLM_PARITY_LEVER_MAP.md`
- Modify: `backend/cpp/llama-cpp-localai-paged/docs/VLLM_PARITY_FINAL.md`
- Modify: `backend/cpp/llama-cpp-localai-paged/docs/PARITY_HANDOFF.md`

## Task 1: Confirm Server MTP Wiring

- [x] **Step 1: Dispatch independent codebase checks**

  Two explorer agents inspected:

  - llama.cpp server speculative/MTP wiring.
  - existing serving benchmark harnesses and safety-gate discipline.

- [x] **Step 2: Record startup-only control**

  Finding:

  - `llama-server` supports MTP when started with `--spec-type draft-mtp`.
  - HTTP request JSON cannot enable speculation per request because the
    speculative request fields in `tools/server/server-schema.cpp` are under
    `#if 0`.

- [x] **Step 3: Run a one-request server smoke**

  Artifact:

  - `/home/mudler/bench/phase15_mtp_serving_smoke`

  Evidence:

  ```text
  common_speculative_impl_draft_mtp: adding speculative implementation 'draft-mtp'
  common_context_can_seq_rm: the context supports bounded partial sequence removal
  timings.draft_n = 33
  timings.draft_n_accepted = 19
  ```

## Task 2: Add Repeatable DGX Runner

- [x] **Step 1: Create runner**

  Created:

  - `backend/cpp/llama-cpp-localai-paged/paged-mtp-serving-bench.sh`

  Responsibilities:

  - check docker, `local-ai-worker`, compute PIDs, and GPU lock owner,
  - run pre/post `paged-inference-gates.sh`,
  - run baseline and MTP `llama-server` arms,
  - drive `/v1/completions` with `/home/mudler/bench/h2h_cli3.py`,
  - capture server logs, client JSON, MTP acceptance lines, and a summary TSV.

- [x] **Step 2: Fix lock ordering**

  First attempt stopped before benchmarking because the runner acquired the GPU
  lock and then called `paged-inference-gates.sh`, whose own preflight correctly
  rejects a non-free lock owner.

  Fix: run the pre-gate before acquiring the benchmark lock and the post-gate
  after releasing it.

## Task 3: Run Serving A/B

- [x] **Step 1: Run canonical pre-gate**

  Artifact:

  - `/home/mudler/bench/phase15_mtp_serving/20260701_042005/gate_pre`

  Result:

  ```text
  moe md5 OK: 8cb0ce23777bf55f92f63d0292c756b0
  dense md5 OK: 5951a5b4d624ce891e22ab5fca9bc439
    806/806 tests passed
    Backend CUDA0: OK
  paged inference gates OK
  ```

- [x] **Step 2: Run baseline and MTP arms**

  Command shape:

  ```bash
  NPL="8 32 128" PTOK=128 GEN=128 CTX=131072 PARALLEL=128 \
    ~/paged-mtp-serving-bench.sh
  ```

  Artifact:

  - `/home/mudler/bench/phase15_mtp_serving/20260701_042005`

  Summary:

  ```text
  arm       n    agg_tps  decode_agg_tps  decode_perseq_tps  ttft_mean_ms  wall_s
  baseline 8    192.5    247.8           30.70              1181.1        5.318
  mtp      8     92.9    109.8           14.26              1691.5       11.017
  baseline 32   305.4    406.0           12.02              2762.2       13.412
  mtp      32    95.8    111.7            3.61              4545.6       42.727
  baseline 128  429.5    662.4            4.31              7747.2       38.144
  mtp      128  100.3    138.5            0.97             20385.7      163.289
  ```

- [x] **Step 3: Confirm MTP actually drafted**

  MTP server log showed:

  ```text
  common_speculative_impl_draft_mtp: - n_max=3, n_min=0, p_min=0.00
  statistics        draft-mtp: #gen tokens = 17293, #acc tokens = 15493
  ```

  Acceptance was high enough that this is not a no-draft false negative.

- [x] **Step 4: Run canonical post-gate**

  Artifact:

  - `/home/mudler/bench/phase15_mtp_serving/20260701_042005/gate_post`

  Result:

  ```text
  moe md5 OK: 8cb0ce23777bf55f92f63d0292c756b0
  dense md5 OK: 5951a5b4d624ce891e22ab5fca9bc439
    806/806 tests passed
    Backend CUDA0: OK
  paged inference gates OK
  ```

## Task 4: Disposition

- [x] **Step 1: Reject current MTP serving as a parity lever**

  Current `llama-server` MTP is slower at every tested concurrency:

  - `n=8`: decode aggregate `247.8 -> 109.8` tok/s.
  - `n=32`: decode aggregate `406.0 -> 111.7` tok/s.
  - `n=128`: decode aggregate `662.4 -> 138.5` tok/s.

- [x] **Step 2: Record likely root cause**

  Baseline logs show heavy graph reuse in the serving run (`graphs reused = 361`
  in the `n=128` tail). MTP logs show `graphs reused = 1` and per-slot eval
  around `900-1200 ms/token` at high concurrency. The working hypothesis is that
  MTP verification/draft batch shape churn defeats the paged decode graph-reuse
  wins, and the extra target verification work dominates despite high acceptance.

- [x] **Step 3: Scope follow-up**

  Do not continue by tuning `spec-draft-n-max` blindly. The next scoped phase,
  if pursued, must first inspect MTP serving graph reuse and batch shapes:

  - confirm whether speculative verification batches bypass the reusable
    pure-decode graph key,
  - measure with `nsys --cuda-graph-trace=node`,
  - test whether MTP can share the default decode graph path or must remain a
    non-parity feature on GB10.

## Self-Review

- No placeholders remain.
- Phase 15 does not enable MTP by default.
- Phase 15 keeps pre/post md5 and `test-backend-ops` gates.
- Result is a rejected serving-throughput lever, not a parity win.
