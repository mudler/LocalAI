# Current Serving Harness Phase 21 Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> superpowers:verification-before-completion before recording the phase result.
> Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** make the Phase 20 current-stack paged-vs-vLLM serving snapshot
repeatable from the LocalAI backend tree.

**Architecture:** add a standalone shell harness beside the existing paged
inference gate and MTP serving harness. The script targets the clean
`~/llama-phase6-source` mirror, uses the owner-file GPU lock, runs pre/post
inference gates, compares paged and vLLM in one session, and writes ratio
summaries.

**Tech Stack:** Bash, llama.cpp `llama-server`, vLLM, `h2h_cli3.py`, DGX GB10.

---

## Task 1: Red Check

- [x] **Step 1: Prove no reusable current-stack harness exists**

  Command:

  ```bash
  test -e backend/cpp/llama-cpp-localai-paged/paged-current-serving-snapshot.sh
  ```

  Result:

  - exited `1` before the patch, as expected.

## Task 2: Add Harness

- [x] **Step 1: Create script**

  File:

  - `backend/cpp/llama-cpp-localai-paged/paged-current-serving-snapshot.sh`

  Features:

  - defaults to `~/llama-phase6-source`, not stale `~/llama-paged-dev`;
  - checks docker, `local-ai-worker`, GPU compute processes, and owner-file lock;
  - builds `llama-server`, `llama-completion`, and `test-backend-ops`;
  - runs pre/post `paged-inference-gates.sh`;
  - runs paged and vLLM serving arms with the same h2h client;
  - writes `summary.tsv` with paged/vLLM ratios;
  - supports `DRY_RUN=1` for path/preflight validation without servers.

## Task 3: Verify Harness

- [x] **Step 1: Local syntax/help checks**

  Commands:

  ```bash
  test -x backend/cpp/llama-cpp-localai-paged/paged-current-serving-snapshot.sh
  bash -n backend/cpp/llama-cpp-localai-paged/paged-current-serving-snapshot.sh
  backend/cpp/llama-cpp-localai-paged/paged-current-serving-snapshot.sh --help
  ```

  Result:

  - all passed.

- [x] **Step 2: DGX dry run**

  Command:

  ```bash
  DRY_RUN=1 ART=~/bench/phase21_harness_dryrun/20260701_051757 \
    /tmp/paged-current-serving-snapshot.sh
  ```

  Result:

  - verified `docker=0`, `local_ai_worker=0`, `compute=0`;
  - verified owner file was free;
  - found current source `f2521ab12`;
  - validated required paths and printed the build/paged/vLLM commands without
    launching servers.

  Artifact:

  - `/home/mudler/bench/phase21_harness_dryrun/20260701_051757`

## Task 4: Future Use

- [x] **Step 1: Prefer this harness for current snapshots**

  Use this script for future current-stack GB10 parity snapshots:

  ```bash
  backend/cpp/llama-cpp-localai-paged/paged-current-serving-snapshot.sh
  ```

  Do not use the stale DGX `~/bench/combined_definitive.sh` without first
  porting it to the clean mirror and owner-file lock discipline.

## Self-Review

- No llama.cpp source behavior changed.
- The harness is repeatable and defaults to the current clean mirror.
- The dry run covered path validation and DGX preflight without consuming GPU
  benchmark time.
