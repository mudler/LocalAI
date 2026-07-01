# Persistent Gate Fusion Phase43 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Determine whether the Phase42 persistent/load-time F32 combined gate projection can be implemented as a low-conflict GB10 shortcut.

**Architecture:** Inspect the Qwen35MoE tensor load and graph consumption paths, then decide whether to implement, reject, or rescope before source changes. This phase is a feasibility gate, not a production patch.

**Tech Stack:** llama.cpp model loader, Qwen35MoE graph builder, GGUF tensor metadata, LocalAI parity docs.

---

### Task 1: Inspect Gate Tensor Source Paths

**Files:**
- Read: `/home/mudler/_git/llama.cpp/src/models/qwen35moe.cpp`
- Read: `/home/mudler/_git/llama.cpp/src/llama-model-loader.cpp`
- Read: `/home/mudler/_git/llama.cpp/src/llama-model.cpp`
- Read: `/home/mudler/_git/llama.cpp/src/llama-model.h`

- [x] **Step 1: Locate tensor creation**

Observed in `src/models/qwen35moe.cpp`:

```cpp
layer.ffn_gate_inp = create_tensor(tn(LLM_TENSOR_FFN_GATE_INP, "weight", il), { n_embd, n_expert }, flags);
layer.ffn_gate_inp_shexp = create_tensor(tn(LLM_TENSOR_FFN_GATE_INP_SHEXP, "weight", il), { n_embd }, flags);
```

- [x] **Step 2: Locate tensor consumption**

Observed:

```cpp
build_moe_ffn(cur, model.layers[il].ffn_gate_inp, ...);
ggml_tensor * shared_gate = build_lora_mm(model.layers[il].ffn_gate_inp_shexp, cur);
```

- [x] **Step 3: Locate loader support for persistent derived tensors**

Observed:

```text
create_tensor(...) duplicates tensors from GGUF metadata.
create_tensor_as_view(...) can create views of existing GGUF tensors.
Backend buffers are allocated from loader contexts before load_all_data(...).
No existing helper creates a new persistent derived weight from two already-loaded tensors.
```

### Task 2: Make Feasibility Decision

**Files:**
- Modify: `backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md`
- Modify: `backend/cpp/llama-cpp-localai-paged/docs/VLLM_PARITY_LEVER_MAP.md`
- Modify: `backend/cpp/llama-cpp-localai-paged/docs/PARITY_HANDOFF.md`
- Create: `docs/superpowers/plans/2026-07-01-persistent-gate-fusion-phase43.md`

- [x] **Step 1: Reject graph-time fallback**

Decision:

```text
Do not use ggml_concat() at graph time; Phase39 already rejected it because concat_layout is measurable in serving.
```

- [x] **Step 2: Reject Qwen-only loader hack**

Decision:

```text
Do not read both tensors back to host, allocate an extra backend weight buffer, and patch layer pointers after load.
That would create high conflict surface across mmap, offload, split buffers, MTP blocks, and state lifetime.
```

- [x] **Step 3: Record no-go**

Decision:

```text
Persistent/load-time fused gate projection is not a small GB10 shortcut.
It requires either a GGUF-exported combined weight or a general derived-weight facility in llama.cpp.
```

### Task 3: Verify and Commit

**Files:**
- Modify: `docs/superpowers/plans/2026-07-01-persistent-gate-fusion-phase43.md`

- [x] **Step 1: Verify docs**

Run:

```bash
git diff --check
git status --short
```

- [x] **Step 2: Commit**

Run:

```bash
git add backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md \
  backend/cpp/llama-cpp-localai-paged/docs/VLLM_PARITY_LEVER_MAP.md \
  backend/cpp/llama-cpp-localai-paged/docs/PARITY_HANDOFF.md
git add -f docs/superpowers/plans/2026-07-01-persistent-gate-fusion-phase43.md
git commit -m "docs(paged): reject persistent gate fusion shortcut" -m "Assisted-by: Codex:gpt-5"
```
