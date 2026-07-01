# MTP Serving Shape Entropy Phase 19 Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> superpowers:verification-before-completion before recording the phase result.
> Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** use Phase 18's `LLAMA_SPEC_SHAPE_TRACE=1` instrumentation under real
serving load to decide whether a group/defer-by-draft-length scheduler
experiment is justified.

**Architecture:** trace-only benchmark. Do not change llama.cpp source or
scheduling policy. Run the existing MTP serving A/B with pre/post canonical
inference gates.

**Tech Stack:** `paged-mtp-serving-bench.sh`, llama.cpp `llama-server`, DGX
GB10, LocalAI paged patch stack.

---

## Task 1: Run Trace-Only Serving A/B

- [x] **Step 1: Confirm DGX is free**

  Preflight passed:

  - `docker=0`
  - `local_ai_worker=0`
  - `compute=0`

- [x] **Step 2: Run serving harness with shape trace**

  Command shape:

  ```bash
  LLAMA_SPEC_SHAPE_TRACE=1 \
    ART=~/bench/phase19_mtp_shape_entropy/20260701_045534 \
    NPL="8 32 128" GEN=64 PTOK=128 \
    /tmp/paged-mtp-serving-bench.sh
  ```

  Artifact:

  - `/home/mudler/bench/phase19_mtp_shape_entropy/20260701_045534`

## Task 2: Verify Inference Gates

- [x] **Step 1: Pre-gate passed**

  Artifact:

  - `/home/mudler/bench/phase19_mtp_shape_entropy/20260701_045534/gate_pre`

  Result:

  - MoE md5: `8cb0ce23777bf55f92f63d0292c756b0`
  - Dense md5: `5951a5b4d624ce891e22ab5fca9bc439`
  - `MUL_MAT_ID`: `806/806`

- [x] **Step 2: Post-gate passed**

  Artifact:

  - `/home/mudler/bench/phase19_mtp_shape_entropy/20260701_045534/gate_post`

  Result:

  - MoE md5: `8cb0ce23777bf55f92f63d0292c756b0`
  - Dense md5: `5951a5b4d624ce891e22ab5fca9bc439`
  - `MUL_MAT_ID`: `806/806`

## Task 3: Analyze Serving Result

- [x] **Step 1: Compare baseline vs MTP serving throughput**

  | n | baseline decode_agg | MTP decode_agg | MTP / baseline | baseline TTFT ms | MTP TTFT ms |
  |---|---------------------|----------------|----------------|------------------|-------------|
  | 8 | 245.0 | 95.7 | 39.1% | 1147.2 | 1633.4 |
  | 32 | 409.2 | 110.0 | 26.9% | 2710.0 | 4471.5 |
  | 128 | 697.2 | 154.0 | 22.1% | 7601.5 | 20310.4 |

  MTP remained materially slower at every concurrency.

- [x] **Step 2: Parse per-slot draft entropy**

  Artifact:

  - `/home/mudler/bench/phase19_mtp_shape_entropy/20260701_045534/shape_entropy_summary.tsv`

  Result:

  | window | verify slots | draft counts | top draft share | unique `batch_before` |
  |--------|--------------|--------------|-----------------|-----------------------|
  | n8 | 162 | `{1: 4, 2: 2, 3: 156}` | 96.3% | 15 |
  | n32 | 610 | `{1: 8, 2: 11, 3: 591}` | 96.9% | 96 |
  | n128 | 2353 | `{1: 40, 2: 49, 3: 2264}` | 96.2% | 479 |

  Draft length is already overwhelmingly `3`. Grouping by draft length has
  little to recover.

- [x] **Step 3: Parse per-step aggregate shapes**

  Artifact:

  - `/home/mudler/bench/phase19_mtp_shape_entropy/20260701_045534/step_shape_summary.tsv`

  Result:

  | window | steps | unique total rows | top full-shape rows |
  |--------|-------|-------------------|---------------------|
  | n8 | 26 | 12 | `32` rows for 14 steps |
  | n32 | 32 | 20 | `128` rows for 13 steps |
  | n128 | 37 | 34 | `512` rows for 4 steps |

  Full in-flight steps already consist mostly of all-`draft=3` vectors. The
  remaining shape churn is active-slot/tail churn plus the speculative `K + 1`
  output-row expansion itself, not a draft-length scheduling problem.

## Task 4: Decision

- [x] **Step 1: Reject Phase 20 scheduler experiment for now**

  Do not build the group/defer-by-draft-length scheduler experiment on this
  evidence:

  - draft length is already stable (`draft=3` >96% of verify slots),
  - MTP still regresses decode throughput to 22-39% of baseline,
  - TTFT gets worse at every concurrency,
  - per-step shape variation is dominated by active-slot/tail churn and row
    expansion, not mixed draft lengths.

  The next useful MTP work would need a deeper target-verify graph/state design,
  not a small server scheduling shortcut.

## Self-Review

- No source behavior changed in this phase.
- Pre/post md5 and op gates passed.
- The phase result moves the plan by rejecting the scheduler follow-up rather
  than leaving it as an attractive but unsupported idea.
