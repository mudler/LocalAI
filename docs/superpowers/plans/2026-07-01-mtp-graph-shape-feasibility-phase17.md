# MTP Graph-Shape Feasibility Phase 17 Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> superpowers:systematic-debugging before proposing source changes. Steps use
> checkbox (`- [ ]`) syntax for tracking.

**Goal:** decide whether Phase 16's MTP graph-reuse loss has a small,
maintainable source fix.

**Architecture:** use read-only code inspection first. Split the problem into
server speculative batch construction and graph-reuse keying. Do not patch until
the shape mechanics are clear.

**Tech Stack:** llama.cpp `tools/server`, `src/llama-graph.*`,
`ggml-cuda` graph reuse, LocalAI paged docs.

---

## Task 1: Parallel Read-Only Inspection

- [x] **Step 1: Inspect server speculative batch construction**

  Finding:

  - Normal decode appends one `output=true` row per generating slot.
  - Speculative/MTP verification appends `K + 1` `output=true` rows per slot,
    where `K = spec_draft.size()`.
  - `slot.spec_i_batch` stores the absolute logical row indices for those
    verification rows.
  - Total batch shape becomes:

    ```text
    sum(non_spec_slots * 1) + sum(spec_slots * (1 + K_i)) + prompt rows
    ```

  Key source areas:

  - `/home/mudler/_git/llama.cpp/tools/server/server-context.cpp`
    around `server_slot::handle_last_sampled_token()`.
  - `/home/mudler/_git/llama.cpp/tools/server/server-context.cpp`
    around the `slot.handle_last_sampled_token(batch)` call site.
  - `/home/mudler/_git/llama.cpp/tools/server/server-context.cpp`
    `post_decode()` speculative index validation.

- [x] **Step 2: Inspect graph-reuse blockers**

  Finding:

  - MTP changes hard graph dimensions:
    `n_tokens`, `n_seq_tokens`, `n_outputs`, KQ mask shape, position length, and
    output-id count.
  - `llm_graph_params::allow_reuse` rejects changes in these dimensions.
  - Paged attention bucketing stabilizes block-table view dimensions only; it
    does not stabilize verification token/output rows.
  - CUDA graph reuse still requires copied node/source properties (`ne`, `nb`,
    pointers, node count) to match.

## Task 2: Feasibility Verdict

- [x] **Step 1: Reject dummy-row padding as a shortcut**

  Padding fake verification rows is not low-risk:

  - rows are real target decode rows,
  - rows have real output logits,
  - rows feed MTP nextn embedding/state extraction,
  - fake rows would mutate KV, positions, sampling indices, and rollback shape.

  This also resembles the previously rejected fixed-slot decode experiment,
  where dummy compute cost exceeded graph-reuse recovery.

- [x] **Step 2: Identify the only small safe hook**

  A read-only shape counter around `server_slot::handle_last_sampled_token()` is
  low-conflict and can expose:

  - normal vs speculative rows,
  - draft length `K`,
  - output rows per sequence,
  - `slot.spec_i_batch` range.

  This is useful instrumentation, not a performance fix.

- [x] **Step 3: Identify the only plausible behavior experiment**

  The least invasive performance experiment is server-side scheduling, not graph
  padding:

  - group or defer speculative verification slots by `1 + spec_draft.size()`,
  - try to make verification windows repeat shape buckets,
  - keep it opt-in and default-off,
  - gate with Phase 14 rollback, Phase 15 serving A/B, and pre/post inference
    md5/op checks.

  This changes serving scheduling and may regress TTFT or reduce concurrency, so
  it needs an explicit kill gate.

## Task 3: Phase 18 Scope If Pursued

- [x] **Step 1: Write the source-scope boundary**

  Phase 18 should be split into two incremental patches if it is attempted:

  1. instrumentation-only: log or count verification shape buckets under a
     disabled-by-default env var, no scheduling change,
  2. opt-in scheduler experiment: group/defer MTP verification by draft length.

- [x] **Step 2: Define stop criteria**

  Stop and reject the source path if:

  - shape counters show high entropy across draft lengths and active slots,
  - grouping reduces graph churn but loses more throughput/TTFT than it recovers,
  - pre/post md5 or `MUL_MAT_ID` gates drift,
  - MTP rollback or normalized greedy-prefix gates fail.

## Self-Review

- No source patch was made in this phase.
- The feasibility conclusion is narrower than "optimize MTP": instrument first,
  then only consider an opt-in scheduler experiment.
- No default behavior changes are proposed without a separate implementation
  phase and gates.
