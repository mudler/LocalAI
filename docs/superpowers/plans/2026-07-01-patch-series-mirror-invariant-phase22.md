# Patch Series Mirror Invariant Phase 22 Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> superpowers:verification-before-completion before recording the phase result.
> Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** prove the LocalAI `patches/paged/` series still reconstructs the
canonical llama.cpp fork tree after adding patch `0055`.

**Architecture:** use the same strict `git apply` method as the LocalAI
Makefile. Apply every on-disk paged patch to a fresh worktree at
`LLAMA_VERSION`, then compare the resulting tree hash with the fork branch HEAD.

**Tech Stack:** Git worktrees, `git apply`, LocalAI paged patch stack,
llama.cpp fork branch `localai-paged`.

---

## Task 1: Apply Patch Series

- [x] **Step 1: Read the pinned base**

  Source:

  - `backend/cpp/llama-cpp-localai-paged/Makefile`

  Result:

  - `LLAMA_VERSION=0ed235ea2c17a19fc8238668653946721ed136fd`

- [x] **Step 2: Apply all patches with strict `git apply`**

  Command shape:

  ```bash
  git -C /home/mudler/_git/llama.cpp worktree add --detach \
    /tmp/llama-paged-series-applycheck \
    0ed235ea2c17a19fc8238668653946721ed136fd

  for p in backend/cpp/llama-cpp-localai-paged/patches/paged/0*.patch; do
    git -C /tmp/llama-paged-series-applycheck apply --verbose "$PWD/$p"
  done
  ```

  Result:

  - every patch applied successfully with `git apply`.

## Task 2: Compare Tree Hash

- [x] **Step 1: Compare applied tree to fork HEAD**

  Result:

  ```text
  base=0ed235ea2c17a19fc8238668653946721ed136fd
  applied_tree=5bdbf8ea3d750fe6fa1f85175fd6357d36222edb
  fork_tree=5bdbf8ea3d750fe6fa1f85175fd6357d36222edb
  ```

  Canonical fork:

  - `/home/mudler/_git/llama.cpp`
  - branch `localai-paged`
  - HEAD `fb9402661 feat(server): trace speculative batch shapes`

## Decision

- [x] **Step 1: Mark mirror invariant green**

  The LocalAI `patches/paged/` series is a byte-for-byte source mirror of the
  canonical fork branch at `fb9402661`.

## Self-Review

- No source or benchmark behavior changed.
- The check used the Makefile's strict `git apply` method, not `git am`.
- The temporary worktree was removed after verification.
