# MTP Shape Trace Phase 18 Plan

> **For agentic workers:** REQUIRED SUB-SKILLS: Use
> superpowers:test-driven-development before source edits and
> superpowers:verification-before-completion before commit. Steps use checkbox
> (`- [ ]`) syntax for tracking.

**Goal:** add a default-off, inference-safe trace for speculative/MTP server
batch shape entropy before considering any scheduler experiment.

**Architecture:** keep this as a server-only instrumentation patch in
`server_slot::handle_last_sampled_token()`. Do not change speculative
acceptance, rollback, logits, KV writes, graph-reuse keys, or scheduling.

**Tech Stack:** llama.cpp `tools/server/server-context.cpp`, LocalAI paged
patch stack, DGX GB10 validation.

---

## Task 1: Red Check

- [x] **Step 1: Prove the trace does not already exist**

  Ran a direct MTP `llama-server` request on DGX with
  `LLAMA_SPEC_SHAPE_TRACE=1` before the source patch.

  Result:

  - no `spec shape:` lines were emitted,
  - artifact: `/home/mudler/bench/phase18_mtp_shape_trace_red`.

## Task 2: Instrumentation Patch

- [x] **Step 1: Add an env-gated trace**

  Added `LLAMA_SPEC_SHAPE_TRACE=1` logging in
  `server_slot::handle_last_sampled_token()`:

  - normal decode rows: `kind=decode`, `rows=1`, `outputs=1`, `draft=0`,
  - speculative verification rows: `kind=verify`, `rows=K+1`,
    `outputs=K+1`, `draft=K`, `spec_i_first`, `spec_i_last`.

  The env var is default-off and does not alter batch contents.

- [x] **Step 2: Keep the patch incremental**

  Local fork commit:

  - `fb9402661 feat(server): trace speculative batch shapes`

  LocalAI patch:

  - `0055-feat-server-trace-speculative-batch-shapes.patch`

## Task 3: Green Checks

- [x] **Step 1: Build and validate trace behavior on DGX**

  DGX mirror commit:

  - `f2521ab12 feat(server): trace speculative batch shapes`

  Build:

  - `cmake --build build-cuda --target llama-server -j$(nproc)`

  Trace-enabled result:

  ```text
  spec shape: kind=verify batch_before=0 rows=4 outputs=4 draft=3 spec_i_first=0 spec_i_last=3 pos0=5 slot_tokens=5
  spec shape: kind=verify batch_before=0 rows=4 outputs=4 draft=3 spec_i_first=0 spec_i_last=3 pos0=6 slot_tokens=6
  spec shape: kind=verify batch_before=0 rows=3 outputs=3 draft=2 spec_i_first=0 spec_i_last=2 pos0=9 slot_tokens=9
  ```

  Trace-disabled result:

  ```text
  trace disabled: no spec shape lines
  ```

  Artifact:

  - `/home/mudler/bench/phase18_mtp_shape_trace_green`

- [x] **Step 2: Run canonical inference gates**

  Artifact:

  - `/home/mudler/bench/phase18_mtp_shape_trace_green/gate_after`

  Result:

  - MoE md5: `8cb0ce23777bf55f92f63d0292c756b0`
  - Dense md5: `5951a5b4d624ce891e22ab5fca9bc439`
  - `MUL_MAT_ID`: `806/806`

## Task 4: Follow-Up Boundary

- [x] **Step 1: Scope Phase 19**

  Use the trace to measure shape entropy under real serving load before any
  behavior change. A Phase 19 scheduler experiment is allowed only if the trace
  shows repeatable draft-length buckets worth grouping. It must be opt-in,
  default-off, and killed by TTFT/throughput regression, md5/op drift, or MTP
  rollback/prefix failure.

## Self-Review

- No default behavior changed.
- The trace is read-only with respect to batch contents and slot state.
- The post-patch canonical md5/op gates passed, so this instrumentation did not
  break inferencing on the gated paths.
