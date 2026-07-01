# Handoff Current-State Cleanup Phase 23 Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> superpowers:verification-before-completion before recording the phase result.
> Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** remove stale handoff coordinates that predate patches `0051-0055` and
the current clean DGX mirror.

**Architecture:** documentation-only cleanup. Keep the measured benchmark
verdict unchanged, but make the handoff point to current source, patch count,
artifact, harness, and contribution-policy facts.

**Tech Stack:** LocalAI paged docs, llama.cpp fork metadata, git status output.

---

## Task 1: Identify Stale Coordinates

- [x] **Step 1: Scan for old fork/patch references**

  Found stale references in `PARITY_HANDOFF.md`:

  - fork HEAD `d9b9be0be` and patch `0050`;
  - `41` patch files spanning `0001-0050`;
  - old `combined_definitive.sh` as the current reference harness;
  - stale ahead/behind count;
  - old AI attribution and sign-off wording.

## Task 2: Update Handoff

- [x] **Step 1: Update canonical source and mirror invariant**

  Current values:

  - local fork HEAD `fb9402661291e0488a3e2bf2f3948ebcd18e18c9`;
  - DGX clean mirror HEAD `f2521ab12`;
  - `46` patch files through `0055`;
  - verified tree `5bdbf8ea3d750fe6fa1f85175fd6357d36222edb`.

- [x] **Step 2: Update harness guidance**

  Current harness:

  - `backend/cpp/llama-cpp-localai-paged/paged-current-serving-snapshot.sh`

  Historical harness:

  - `dgx:~/bench/combined_definitive.sh` should not be reused without porting.

- [x] **Step 3: Update contribution policy text**

  Current policy:

  - no AI `Signed-off-by`;
  - no AI `Co-Authored-By`;
  - use `Assisted-by: Codex:gpt-5`.

## Task 3: Verification

- [x] **Step 1: Re-scan for targeted stale strings**

  Command searched for:

  - `d9b9be0be`
  - `41.*patch`
  - `0001-0050`
  - `199 ahead`
  - `25 behind`
  - `llama-paged-fork`
  - stale `combined_definitive.sh` reference-harness wording
  - old Claude attribution

  Result:

  - no targeted stale strings remain in `PARITY_HANDOFF.md`.

## Self-Review

- No source or benchmark behavior changed.
- The cleanup aligns the handoff with Phases 20-22.
- The parity verdict remains unchanged: current GB10 stack is still below vLLM
  serving parity; the next credible path is new hardware or a larger fused-kernel
  project.
