# Layout Trace Phase64 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Attribute the remaining llama.cpp `layout-copy` prefill bucket to concrete graph tensors without changing inference behavior.

**Architecture:** Add default-off CUDA layout tracing for `GET_ROWS`, `CPY`, `CONT`, `DUP`, and `CONCAT`, gated by `LLAMA_LAYOUT_TRACE=<n>`. Use the same md5/op gates before accepting the instrumentation, then run a bounded MoE prefill trace to decide whether the layout bucket exposes a low-conflict Phase65 source patch.

**Tech Stack:** llama.cpp CUDA backend, LocalAI paged parity docs, DGX `dgx.casa`, `llama-batched-bench`, canonical md5/op gates.

---

## Guardrails

- Trace must be silent when `LLAMA_LAYOUT_TRACE` is unset.
- Trace must not alter tensor data or route decisions.
- Do not regenerate LocalAI patch series in this phase.
- Canonical gates:
  - MoE md5 `8cb0ce23777bf55f92f63d0292c756b0`
  - dense md5 `5951a5b4d624ce891e22ab5fca9bc439`
  - `MUL_MAT` `1146/1146`
  - `MUL_MAT_ID` `806/806`

## Files

- Modify: `/home/mudler/_git/llama.cpp/ggml/src/ggml-cuda/ggml-cuda.cu`
- Create: `docs/superpowers/plans/2026-07-01-layout-trace-phase64.md`
- Modify: `backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md`
- Modify: `backend/cpp/llama-cpp-localai-paged/docs/PARITY_HANDOFF.md`
- Modify: `backend/cpp/llama-cpp-localai-paged/docs/VLLM_PARITY_LEVER_MAP.md`

---

### Task 1: Add Default-Off Layout Trace

- [x] **Step 1: Inspect measured layout rows**

Phase63 kernel names at `npp=2048`:

```text
convert_unary<bf16,float>: 721.23ms
convert_unary<float,bf16>: 634.91ms
concat_non_cont: 566.04ms
k_get_rows_float<float,float>: 307.52ms
cpy_scalar<float,float>: 107.05ms
```

- [x] **Step 2: Add trace helper in `ggml-cuda.cu`**

Implemented `LLAMA_LAYOUT_TRACE=<n>` with route, op, dst/src names, types,
shapes, and contiguity flags.

- [x] **Step 3: Wire trace calls to runtime dispatch**

Runtime cases traced:

- `GGML_OP_GET_ROWS`
- `GGML_OP_DUP`
- `GGML_OP_CPY`
- `GGML_OP_CONT`
- `GGML_OP_CONCAT`

- [x] **Step 4: Verify local diff**

Run:

```bash
git -C /home/mudler/_git/llama.cpp diff --check
```

Expected: no output.

Result: no output.

---

### Task 2: Build and Gate on DGX

- [x] **Step 1: Acquire DGX lock**

Result:

```text
docker=0 local_ai_worker=0 compute=0 lock=FREE released-by-codex-phase63-prefill-bucket 1782908317
codex-phase64-layout-trace 1782908645
```

- [x] **Step 2: Apply the patch to DGX clean build tree**

Result: applied to `/home/mudler/llama-phase6-source`; remote diff was
`ggml/src/ggml-cuda/ggml-cuda.cu | 51 +++++++++++++++++++++++++++++++++++++++++`.

- [x] **Step 3: Build CUDA targets**

Run:

```bash
ssh dgx.casa 'cd /home/mudler/llama-phase6-source && cmake --build build-cuda --target llama-completion llama-batched-bench test-backend-ops -j $(nproc)'
```

Result: build passed.

- [x] **Step 4: Run patched md5/op gates**

Artifact: `/home/mudler/bench/phase64_layout_trace/20260701_142519`

```text
patched	moe_md5	8cb0ce23777bf55f92f63d0292c756b0	8cb0ce23777bf55f92f63d0292c756b0	ok
patched	dense_md5	5951a5b4d624ce891e22ab5fca9bc439	5951a5b4d624ce891e22ab5fca9bc439	ok
patched	MUL_MAT	1146/1146	1146/1146	ok
patched	MUL_MAT_ID	806/806	806/806	ok
```

---

### Task 3: Run Bounded Layout Trace

- [x] **Step 1: Run MoE prefill trace**

Run:

```bash
LLAMA_KV_PAGED=1 LLAMA_MOE_FORCE_GRAPHS=1 GGML_NO_BACKTRACE=1 GGML_CUDA_DISABLE_GRAPHS=1 LLAMA_LAYOUT_TRACE=12000 \
  ./llama-batched-bench -m /home/mudler/bench/q36-35b-a3b-nvfp4.gguf \
  -c 131072 -b 2048 -ub 512 -ngl 99 -fa on -npp 512 -ntg 4 -npl 32
```

Result files:

- `/home/mudler/bench/phase64_layout_trace/20260701_142519/layout_trace_npp512.trace`
- `/home/mudler/bench/phase64_layout_trace/20260701_142519/layout_trace_summary2.txt`

- [x] **Step 2: Reduce trace**

Route distribution:

| route | lines |
|-------|------:|
| `get_rows` | `7268` |
| `cpy` | `2008` |
| `cont` | `1734` |
| `concat` | `990` |

Top type pairs:

| route/type | count |
|------------|------:|
| `get_rows f32 -> f32` | `6250` |
| `get_rows f16 -> f32` | `1018` |
| `concat f32 -> f32` | `990` |
| `cpy f32 -> f32 noncontig -> contig` | `990` |
| `cont f16 -> f16 noncontig -> contig` | `970` |
| `cont f32 -> f32 noncontig -> contig` | `688` |
| `cpy f32 -> f16 noncontig -> contig` | `660` |
| `cpy f32 -> f16 contig -> contig` | `358` |

Named sources:

- `concat conv_states_reshaped-N + qkv_mixed_transposed-N -> conv_input-N`
- `cpy conv_state_last-N -> conv_state_update-N`
- `get_rows cache_r_lN -> conv_states-N`
- `get_rows ffn_moe_probs-N -> ffn_moe_weights-N`
- `get_rows node_* with ffn_moe_topk-N` for expert fan-in weights
- attention mask/KV reshapes and f32-to-f16 copies for paged full-attention layers

---

### Task 4: Commit and Record

- [x] **Step 1: Commit fork instrumentation**

Result: `/home/mudler/_git/llama.cpp` commit
`fa944bb5f feat(cuda): trace layout tensor names`.

- [x] **Step 2: Record LocalAI docs**

Result: this plan and parity docs updated.

- [x] **Step 3: Commit LocalAI docs**

Result: this commit records the Phase64 LocalAI docs.

Command:

```bash
git add -f docs/superpowers/plans/2026-07-01-layout-trace-phase64.md
git add backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md \
        backend/cpp/llama-cpp-localai-paged/docs/PARITY_HANDOFF.md \
        backend/cpp/llama-cpp-localai-paged/docs/VLLM_PARITY_LEVER_MAP.md
git commit -m "docs(paged): record layout trace phase" \
  -m "Assisted-by: Codex:gpt-5"
```

---

## Decision

Phase64 keeps the instrumentation patch because it is default-off, low-conflict,
and md5/op gated. It does not yet fund a layout optimization: the trace points at
GDN conv-state materialization, MoE top-k fan-in gathers, and paged-attention
mask/KV reshapes, not a single clean projection/layout shortcut.
