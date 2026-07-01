# Snapshot Hardware Report Phase 24 Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> superpowers:verification-before-completion before recording the phase result.
> Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** make current-stack paged-vs-vLLM serving snapshots record the hardware
class so GB10/workstation Blackwell results are not confused with future
datacenter-Blackwell parity runs.

**Architecture:** extend the existing current serving snapshot harness with a
small pre-server hardware report. Keep it additive and outside llama.cpp source:
no patch-series change, no inference behavior change, and no GPU server launch
in dry-run mode.

**Tech Stack:** Bash, `nvidia-smi`, DGX GB10.

---

## Task 1: Red Check

- [x] **Step 1: Prove the previous dry-run artifact lacks hardware identity**

  Command:

  ```bash
  ssh dgx.casa 'test -e ~/bench/phase21_harness_dryrun/20260701_051757/hardware.txt'
  ```

  Result:

  - exited `1`, confirming the existing harness did not write a hardware report.

## Task 2: Add Hardware Report

- [x] **Step 1: Extend `paged-current-serving-snapshot.sh`**

  File:

  - `backend/cpp/llama-cpp-localai-paged/paged-current-serving-snapshot.sh`

  Behavior:

  - writes `$ART/hardware.txt` immediately after preflight;
  - records `nvidia-smi -L`;
  - records GPU name, driver, memory, and compute capability when available;
  - falls back if `compute_cap` is unavailable in `nvidia-smi`;
  - classifies hardware as `datacenter_blackwell`, `datacenter_other`,
    `gb10_or_workstation_blackwell`, or `unknown`;
  - writes a parity note for the detected hardware class;
  - runs in `DRY_RUN=1` before the script exits.

## Task 3: Verify

- [x] **Step 1: Local syntax/help checks**

  Commands:

  ```bash
  bash -n backend/cpp/llama-cpp-localai-paged/paged-current-serving-snapshot.sh
  backend/cpp/llama-cpp-localai-paged/paged-current-serving-snapshot.sh --help
  ```

  Result:

  - both passed.

- [x] **Step 2: DGX dry run**

  Command:

  ```bash
  DRY_RUN=1 ART=~/bench/phase24_hardware_report_dryrun/20260701_052741 \
    /tmp/paged-current-serving-snapshot.sh
  ```

  Result:

  - preflight verified `docker=0`, `local_ai_worker=0`, `compute=0`;
  - no paged or vLLM server launched;
  - `hardware.txt` was written.

  Artifact:

  - `/home/mudler/bench/phase24_hardware_report_dryrun/20260701_052741`

  Hardware report:

  ```text
  GPU 0: NVIDIA GB10
  driver=580.159.03
  compute_cap=12.1
  hardware_class=gb10_or_workstation_blackwell
  ```

## Task 4: Record Result

- [x] **Step 1: Update parity docs**

  Updated files:

  - `backend/cpp/llama-cpp-localai-paged/README.md`
  - `backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md`
  - `backend/cpp/llama-cpp-localai-paged/docs/PARITY_HANDOFF.md`
  - `backend/cpp/llama-cpp-localai-paged/docs/VLLM_PARITY_LEVER_MAP.md`

## Self-Review

- No llama.cpp source behavior changed.
- The harness remains dry-run safe.
- Future snapshot artifacts now carry enough hardware identity to separate GB10
  closure evidence from datacenter-Blackwell parity evidence.
