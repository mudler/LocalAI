# Target Reconciliation Phase42 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reconcile the post-Phase41 target list so the next parity phase does not chase a closed D1/GDN/W4A16 premise.

**Architecture:** Use read-only parallel subagent analysis over D1 graph capture, GDN prefill, and W4A16/MoE prefill GEMM. Record the resulting target decision in the parity docs.

**Tech Stack:** LocalAI docs, llama.cpp patch mirrors, `/home/mudler/_git/llama.cpp` fork, Git.

---

### Task 1: Run Parallel Target Reviews

**Files:**
- Read: `backend/cpp/llama-cpp-localai-paged/patches/paged/0040-*.patch`
- Read: `backend/cpp/llama-cpp-localai-paged/patches/paged/0041-*.patch`
- Read: `backend/cpp/llama-cpp-localai-paged/patches/paged/0043-*.patch`
- Read: `backend/cpp/llama-cpp-localai-paged/patches/paged/0031-*.patch`
- Read: `backend/cpp/llama-cpp-localai-paged/patches/paged/0046-*.patch`
- Read: `backend/cpp/llama-cpp-localai-paged/patches/paged/0047-*.patch`
- Read: `backend/cpp/llama-cpp-localai-paged/patches/paged/0033-*.patch`
- Read: `backend/cpp/llama-cpp-localai-paged/patches/paged/0034-*.patch`
- Read: `backend/cpp/llama-cpp-localai-paged/patches/paged/0035-*.patch`
- Read: `backend/cpp/llama-cpp-localai-paged/patches/paged/0048-*.patch`
- Read: `backend/cpp/llama-cpp-localai-paged/patches/paged/0049-*.patch`
- Read: `backend/cpp/llama-cpp-localai-paged/patches/paged/0050-*.patch`

- [x] **Step 1: Review D1**

Ask a read-only explorer to reconcile whether D1/full-step graph capture is shipped or still open.

Observed:

```text
D1/full-step MoE decode CUDA graph capture is shipped and default-on.
The host-sync premise is closed/refuted for current GB10 NVFP4 grouped-MMQ decode.
```

- [x] **Step 2: Review GDN**

Ask a read-only explorer to inspect GDN tensor-core/chunking state.

Observed:

```text
0046/0047 are shipped GB10 wins.
0031 scalar chunking stayed opt-in/slower.
C32 slab, QS-early, and Global-Ai32 were correctness-clean but slower.
Do not add another GDN GB10 patch.
```

- [x] **Step 3: Review W4A16/GEMM**

Ask a read-only explorer to inspect the prefill GEMM / W4A16 state.

Observed:

```text
0033/0034/0035 are default-off.
0048/0049/0050 improve forced W4A16 only marginally.
Production defaults still use FP4-MMQ.
Do not add another small W4A16 body/metadata patch.
```

### Task 2: Record Phase42 Decision

**Files:**
- Modify: `backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md`
- Modify: `backend/cpp/llama-cpp-localai-paged/docs/VLLM_PARITY_LEVER_MAP.md`
- Modify: `backend/cpp/llama-cpp-localai-paged/docs/PARITY_HANDOFF.md`
- Modify: `docs/superpowers/plans/2026-07-01-low-concurrency-phase41.md`
- Create: `docs/superpowers/plans/2026-07-01-target-reconciliation-phase42.md`

- [x] **Step 1: Correct Phase41 D1 wording**

Change Phase41 from "D1 remains relevant" to "low-concurrency remains a gap, but D1 graph capture is already shipped/default-on and not reopened."

- [x] **Step 2: Add Phase42 decision**

Record:

```text
D1: closed on current GB10 path.
GDN: low-conflict GB10 work exhausted.
W4A16/GEMM: micro-patch track exhausted.
Next small GB10 source candidate: persistent/load-time F32 combined gate projection.
```

- [x] **Step 3: Verify and commit**

Run:

```bash
git diff --check
git status --short
```

Commit with:

```bash
git add backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md \
  backend/cpp/llama-cpp-localai-paged/docs/VLLM_PARITY_LEVER_MAP.md \
  backend/cpp/llama-cpp-localai-paged/docs/PARITY_HANDOFF.md \
  docs/superpowers/plans/2026-07-01-low-concurrency-phase41.md
git add -f docs/superpowers/plans/2026-07-01-target-reconciliation-phase42.md
git commit -m "docs(paged): reconcile next parity target" -m "Assisted-by: Codex:gpt-5"
```
