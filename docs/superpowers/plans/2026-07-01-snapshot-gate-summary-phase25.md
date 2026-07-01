# Snapshot Gate Summary Phase 25 Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> superpowers:verification-before-completion before recording the phase result.
> Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** make current-stack paged-vs-vLLM serving artifacts prove that
inference md5/op gates stayed green without requiring a full log read.

**Architecture:** extend the existing current serving snapshot harness with a
compact gate-summary writer. Keep it additive and outside llama.cpp source: no
patch-series change and no inference behavior change.

**Tech Stack:** Bash, Python stdlib, existing `paged-inference-gates.sh`
artifacts.

---

## Task 1: Red Check

- [x] **Step 1: Prove Phase 20 lacks compact gate proof**

  Command:

  ```bash
  ssh dgx.casa 'test -e ~/bench/phase20_current_snapshot/20260701_050621/gate_summary.tsv'
  ```

  Result:

  - exited `1` before the patch, while `gate_pre/`, `gate_post/`, and full gate
    logs existed.

## Task 2: Add Gate Summary

- [x] **Step 1: Extend `paged-current-serving-snapshot.sh`**

  File:

  - `backend/cpp/llama-cpp-localai-paged/paged-current-serving-snapshot.sh`

  Behavior:

  - writes `$ART/gate_summary.tsv` after the post gate in a full serving run;
  - records pre/post MoE md5, dense md5, and backend op status;
  - compares MoE against `8cb0ce23777bf55f92f63d0292c756b0`;
  - compares dense against `5951a5b4d624ce891e22ab5fca9bc439`;
  - parses op pass counts such as `806/806 tests passed`;
  - exits non-zero if an existing gate artifact is missing, mismatched, or not
    fully passing;
  - supports `--summarize-gates ART` to audit existing artifacts without running
    servers.

## Task 3: Verify

- [x] **Step 1: Local syntax/help checks**

  Commands:

  ```bash
  bash -n backend/cpp/llama-cpp-localai-paged/paged-current-serving-snapshot.sh
  backend/cpp/llama-cpp-localai-paged/paged-current-serving-snapshot.sh --help
  ```

  Result:

  - both passed.

- [x] **Step 2: Backfill Phase 20 gate summary**

  Command:

  ```bash
  /tmp/paged-current-serving-snapshot.sh \
    --summarize-gates ~/bench/phase20_current_snapshot/20260701_050621
  ```

  Result:

  - wrote `/home/mudler/bench/phase20_current_snapshot/20260701_050621/gate_summary.tsv`;
  - pre/post MoE md5 rows were `ok`;
  - pre/post dense md5 rows were `ok`;
  - pre/post `MUL_MAT_ID` rows were `ok` with `806/806`.

- [x] **Step 3: DGX dry run**

  Command:

  ```bash
  DRY_RUN=1 ART=~/bench/phase25_gate_summary_dryrun/20260701_053353 \
    /tmp/paged-current-serving-snapshot.sh
  ```

  Result:

  - preflight verified `docker=0`, `local_ai_worker=0`, `compute=0`;
  - `hardware.txt` was still written;
  - no paged or vLLM server launched;
  - no `gate_summary.tsv` was written before gates existed.

  Artifact:

  - `/home/mudler/bench/phase25_gate_summary_dryrun/20260701_053353`

## Task 4: Record Result

- [x] **Step 1: Update parity docs**

  Updated files:

  - `backend/cpp/llama-cpp-localai-paged/README.md`
  - `backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md`
  - `backend/cpp/llama-cpp-localai-paged/docs/PARITY_HANDOFF.md`
  - `backend/cpp/llama-cpp-localai-paged/docs/VLLM_PARITY_LEVER_MAP.md`

## Self-Review

- No llama.cpp source behavior changed.
- Future full snapshots now contain compact proof of pre/post md5 and op gates.
- The summary-only mode lets old artifacts be audited without consuming GPU
  benchmark time.
