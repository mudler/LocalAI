# Patch Series Mirror Readiness Phase69 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** prove the LocalAI paged patch series can be extended from `0063` to the current local fork HEAD without conflicts, while respecting the no-push-without-approval rule.

**Architecture:** Do not edit generated patches yet. First verify the current on-disk series still matches the Phase37 fork tip, then export the missing commits into `/tmp`, apply current plus missing patches onto the pinned llama.cpp base, and compare that tree to the current local fork HEAD.

**Tech Stack:** Git worktrees, `git apply`, `git format-patch`, LocalAI paged patch stack, llama.cpp fork branch `localai-paged`.

---

## Guardrails

- Do not push `mudler/llama.cpp:localai-paged` without explicit user approval.
- Do not edit `backend/cpp/llama-cpp-localai-paged/patches/paged/*.patch` directly.
- Do not regenerate committed LocalAI patch files before the fork push step required by the repo policy.
- Use strict `git apply`, matching the LocalAI build path.
- Record drift as a first-class phase result.

## Files

- Create: `/home/mudler/_git/LocalAI/.claude/worktrees/feat+paged-attention/docs/superpowers/plans/2026-07-01-patch-series-mirror-readiness-phase69.md`
- Modify: `/home/mudler/_git/LocalAI/.claude/worktrees/feat+paged-attention/backend/cpp/llama-cpp-localai-paged/docs/PATCH_MAINTENANCE.md`
- Modify: `/home/mudler/_git/LocalAI/.claude/worktrees/feat+paged-attention/backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md`
- Modify: `/home/mudler/_git/LocalAI/.claude/worktrees/feat+paged-attention/backend/cpp/llama-cpp-localai-paged/docs/PARITY_HANDOFF.md`

---

### Task 1: Verify Current Mirror Baseline

- [x] **Step 1: Confirm current LocalAI state**

Result:

- LocalAI HEAD: `2b2b1f0b2 docs(paged): record BF16 F32 output dense serving phase`
- Untracked files: pre-existing `.claude/` scratch files only.
- Patch-series tail: `0063-feat-cuda-trace-cublas-tensor-names.patch`.

- [x] **Step 2: Compare current patch series against Phase37 fork tip**

Command shape:

```bash
BASE=$(awk -F '?=' '/^LLAMA_VERSION/ {print $2}' backend/cpp/llama-cpp-localai-paged/Makefile)
CHECK=/tmp/llama-paged-series-applycheck-phase69
git -C /home/mudler/_git/llama.cpp worktree add --detach "$CHECK" "$BASE"
for p in "$PWD"/backend/cpp/llama-cpp-localai-paged/patches/paged/0*.patch; do
  git -C "$CHECK" apply --verbose "$p"
done
git -C "$CHECK" add -A
git -C "$CHECK" write-tree
git -C /home/mudler/_git/llama.cpp rev-parse 2d590d770^{tree}
```

Result:

```text
base=0ed235ea2c17a19fc8238668653946721ed136fd
applied_tree=dedb1182910eafe9f6875588dc8285bfb544cce5
patch_tip_tree=dedb1182910eafe9f6875588dc8285bfb544cce5
fork_head_tree=fcf5720b659c5e1e2b487ccf3c8f7289bb12b9c4
match_patch_tip=yes
match_fork_head=no
patch_count=54
```

Decision: the committed LocalAI series remains correct for Phase37, but it is
intentionally behind the local fork HEAD.

### Task 2: Dry-Run Missing Patch Export

- [x] **Step 1: Inspect fork divergence**

Result:

```text
upstream=fork/localai-paged
ahead_of_upstream=26
ahead_of_patch_tip_2d590d770=10
fork_head=ea0875d14225a10d87a1d0e1b9b57b74c81d873e
fork_head_tree=fcf5720b659c5e1e2b487ccf3c8f7289bb12b9c4
```

- [x] **Step 2: Export missing commits to `/tmp` only**

Run:

```bash
OUT=/tmp/phase69_missing_patches
rm -rf "$OUT"
mkdir -p "$OUT"
git -C /home/mudler/_git/llama.cpp format-patch \
  --zero-commit --no-signature --start-number 64 \
  -o "$OUT" 2d590d770..HEAD
```

Result:

```text
0064-feat-server-trace-serving-admission-batches.patch
0065-feat-server-add-admission-trace-histograms.patch
0066-feat-server-add-TTFT-prefill-first-scheduler-mode.patch
0067-feat-server-cap-TTFT-prefill-first-decode-deferral.patch
0068-feat-server-gate-TTFT-defer-by-prompt-backlog.patch
0069-test-cuda-cover-W4A16-direct-activation-policy.patch
0070-feat-cuda-route-W4A16-direct-activation-stub.patch
0071-feat-cuda-trace-layout-tensor-names.patch
0072-feat-cuda-trace-activation-quant-routes.patch
0073-feat-cuda-gate-BF16-cuBLAS-F32-output.patch
```

- [x] **Step 3: Confirm source-only candidate paths**

The temp patches touch only llama.cpp source, tests, CMake, and server files:

```text
ggml/src/ggml-cuda/*
tests/*
tools/server/*
```

No markdown or LocalAI files are included in the generated candidate patches.

### Task 3: Prove Full Projected Mirror

- [x] **Step 1: Apply current plus temp patches to the pinned base**

Command shape:

```bash
BASE=$(awk -F '?=' '/^LLAMA_VERSION/ {print $2}' backend/cpp/llama-cpp-localai-paged/Makefile)
CHECK=/tmp/llama-paged-series-applycheck-phase69-full
git -C /home/mudler/_git/llama.cpp worktree add --detach "$CHECK" "$BASE"
for p in "$PWD"/backend/cpp/llama-cpp-localai-paged/patches/paged/0*.patch /tmp/phase69_missing_patches/*.patch; do
  git -C "$CHECK" apply --verbose "$p"
done
git -C "$CHECK" add -A
```

Result:

```text
base=0ed235ea2c17a19fc8238668653946721ed136fd
applied_plus_missing_tree=fcf5720b659c5e1e2b487ccf3c8f7289bb12b9c4
fork_head_tree=fcf5720b659c5e1e2b487ccf3c8f7289bb12b9c4
match_fork_head=yes
current_patch_count=54
missing_patch_count=10
projected_patch_count=64
```

Decision: after push approval, the LocalAI patch-series regeneration path is
known: add temp-export-equivalent patches `0064..0073`, then verify the same tree
hash. The BF16 F32 opt-in is projected as patch `0073`.

### Task 4: Record and Commit Documentation

- [x] **Step 1: Record phase result**

Update:

- `backend/cpp/llama-cpp-localai-paged/docs/PATCH_MAINTENANCE.md`
- `backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md`
- `backend/cpp/llama-cpp-localai-paged/docs/PARITY_HANDOFF.md`

- [x] **Step 2: Commit LocalAI docs**

Run:

```bash
git add -f docs/superpowers/plans/2026-07-01-patch-series-mirror-readiness-phase69.md
git add backend/cpp/llama-cpp-localai-paged/docs/PATCH_MAINTENANCE.md \
        backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md \
        backend/cpp/llama-cpp-localai-paged/docs/PARITY_HANDOFF.md
git commit -m "docs(paged): record patch mirror readiness phase" \
  -m "Assisted-by: Codex:gpt-5"
```
