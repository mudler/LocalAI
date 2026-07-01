# Quant Trace Phase65 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Attribute the remaining activation-quant and FP4 prefill quantization bucket with a default-off llama.cpp diagnostic patch, without changing inferencing by default.

**Architecture:** Add bounded stderr tracing at the CUDA call sites that launch activation quantization for MMQ and native large-M FP4 prefill. The trace records route, tensor names, tensor shapes, dedup/gather status, and padded K/M dimensions so Phase65 can decide whether a real source optimization is funded.

**Tech Stack:** llama.cpp CUDA backend, LocalAI parity docs, DGX GB10 benchmark host, canonical md5 and `test-backend-ops` gates.

---

## Guardrails

- Do not change default inferencing behavior. `LLAMA_QUANT_TRACE` unset or `0` must only add inert helper code.
- Keep the source patch small and incremental. Prefer local helper functions in existing CUDA files over new cross-file abstractions.
- Gate every source change with:
  - MoE paged md5: `8cb0ce23777bf55f92f63d0292c756b0`
  - dense md5: `5951a5b4d624ce891e22ab5fca9bc439`
  - `test-backend-ops` `MUL_MAT` all passed
  - `test-backend-ops` `MUL_MAT_ID` all passed
- Do not regenerate LocalAI patch files in this phase unless explicitly approved.
- Do not push without explicit approval.

## Files

- Modify: `/home/mudler/_git/llama.cpp/ggml/src/ggml-cuda/mmq.cu`
- Modify: `/home/mudler/_git/llama.cpp/ggml/src/ggml-cuda/fp4-gemm.cu`
- Create: `/home/mudler/_git/LocalAI/.claude/worktrees/feat+paged-attention/docs/superpowers/plans/2026-07-01-quant-trace-phase65.md`
- Modify after DGX run: `/home/mudler/_git/LocalAI/.claude/worktrees/feat+paged-attention/backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md`
- Modify after DGX run: `/home/mudler/_git/LocalAI/.claude/worktrees/feat+paged-attention/backend/cpp/llama-cpp-localai-paged/docs/PARITY_HANDOFF.md`
- Modify after DGX run: `/home/mudler/_git/LocalAI/.claude/worktrees/feat+paged-attention/backend/cpp/llama-cpp-localai-paged/docs/VLLM_PARITY_LEVER_MAP.md`

---

### Task 1: Add MMQ Quant Trace

- [x] **Step 1: Add default-off trace helpers to `mmq.cu`**

Add local helpers near the top of `ggml/src/ggml-cuda/mmq.cu`:

```c++
static inline int ggml_cuda_quant_trace_limit();
static inline const char * ggml_cuda_quant_trace_tensor_name(const ggml_tensor * t);
static inline void ggml_cuda_quant_trace(
    const char * route, const ggml_tensor * src0, const ggml_tensor * src1,
    const ggml_tensor * ids, const ggml_tensor * dst, int native_fp4,
    int dedup, int gathered, int64_t ne10, int64_t ne10_padded,
    int64_t rows, int64_t ne12, int64_t n_expert_used);
```

The helper reads `LLAMA_QUANT_TRACE`, uses a static atomic counter, and prints one line per trace:

```text
[LLAMA_QUANT_TRACE] route=... src0=... src0_type=... src1=... dst=... ids=... native_fp4=... dedup=... gathered=... K=... Kpad=... rows=... ne12=... experts=...
```

- [x] **Step 2: Trace dense MMQ quantization**

Before the dense `quantize_mmq_fp4_cuda` or `quantize_mmq_q8_1_cuda` call, emit:

```c++
ggml_cuda_quant_trace("mmq_dense", src0, src1, ids, dst, use_native_fp4 ? 1 : 0,
    0, 0, ne10, ne10_padded, ne11, ne12, 0);
```

- [x] **Step 3: Trace MoE MMQ quantization paths**

In the `ids` path:

```c++
ggml_cuda_quant_trace("mmq_moe_dedup_unique", src0, src1, ids, dst, use_native_fp4 ? 1 : 0,
    1, 0, ne10, ne10_padded, ne12, ne12, n_expert_used);
ggml_cuda_quant_trace("mmq_moe_gather", src0, src1, ids, dst, use_native_fp4 ? 1 : 0,
    1, 1, ne10, ne10_padded, ne11_flat, ne12, n_expert_used);
ggml_cuda_quant_trace("mmq_moe_flat", src0, src1, ids, dst, use_native_fp4 ? 1 : 0,
    0, 0, ne10, ne10_padded, ne11_flat, ne12, n_expert_used);
```

Only emit the route that is actually launched.

- [x] **Step 4: Run local syntax checks**

Run:

```bash
git -C /home/mudler/_git/llama.cpp diff --check
```

Expected: exit `0`.

---

### Task 2: Add Native FP4 Prefill Quant Trace

- [x] **Step 1: Add local helpers to `fp4-gemm.cu`**

Add a small `LLAMA_QUANT_TRACE` helper near `ggml_cuda_fp4_prefill_m()` that prints route `fp4_prefill_act_split` with `src0`, `src1`, `dst`, `K`, `M`, `Mpad`, and `Kb`.

- [x] **Step 2: Emit before `fp4_quantize_act_split`**

In `ggml_cuda_mul_mat_fp4_large_m`, emit the trace immediately before the activation split launch:

```c++
ggml_cuda_fp4_quant_trace("fp4_prefill_act_split", src0, src1, dst, K, M, Mpad, Kb);
```

- [x] **Step 3: Run local syntax checks**

Run:

```bash
git -C /home/mudler/_git/llama.cpp diff --check
```

Expected: exit `0`.

---

### Task 3: DGX Build and Gates

- [x] **Step 1: Confirm DGX is idle**

Run:

```bash
ssh dgx.casa 'cat /tmp/localai-gb10.lock 2>/dev/null || true; docker ps --format "{{.Names}}" | wc -l; (pgrep -af "[l]ocal-ai-worker" || true) | wc -l; nvidia-smi --query-compute-apps=pid,process_name,used_gpu_memory --format=csv,noheader | wc -l'
```

Expected: lock `FREE*`, docker `0`, worker `0`, compute apps `0`.

- [x] **Step 2: Acquire the lock**

Run:

```bash
ssh dgx.casa 'printf "codex-phase65-quant-trace %s\n" "$(date +%s)" > /tmp/localai-gb10.lock; cat /tmp/localai-gb10.lock'
```

- [x] **Step 3: Apply patch and build on DGX**

Run the existing phase-source mirror flow for `/home/mudler/llama-phase6-source`, then:

```bash
ssh dgx.casa 'cd /home/mudler/llama-phase6-source && cmake --build build-cuda --target llama-completion llama-batched-bench test-backend-ops -j $(nproc)'
```

Expected: exit `0`.

- [x] **Step 4: Run inference and op gates**

Run the canonical MoE and dense md5 commands plus:

```bash
./test-backend-ops test -o MUL_MAT
./test-backend-ops test -o MUL_MAT_ID
```

Expected:

```text
MoE md5 8cb0ce23777bf55f92f63d0292c756b0
dense md5 5951a5b4d624ce891e22ab5fca9bc439
MUL_MAT all passed
MUL_MAT_ID all passed
```

Result artifact: `/home/mudler/bench/phase65_quant_trace/20260701_143729`.

Observed:

```text
MoE md5 8cb0ce23777bf55f92f63d0292c756b0
dense md5 5951a5b4d624ce891e22ab5fca9bc439
MUL_MAT 1146/1146
MUL_MAT_ID 806/806
```

---

### Task 4: Trace and Decide

- [x] **Step 1: Run bounded quant trace**

Run MoE prefill with graphs disabled for log readability:

```bash
LLAMA_KV_PAGED=1 LLAMA_MOE_FORCE_GRAPHS=1 GGML_NO_BACKTRACE=1 GGML_CUDA_DISABLE_GRAPHS=1 LLAMA_QUANT_TRACE=12000 \
  ./llama-batched-bench -m /home/mudler/bench/q36-35b-a3b-nvfp4.gguf \
  -c 131072 -b 2048 -ub 512 -ngl 99 -fa on -npp 512 -ntg 4 -npl 32
```

- [x] **Step 2: Summarize trace routes**

Expected summary keys:

```text
mmq_dense
mmq_moe_dedup_unique
mmq_moe_gather
mmq_moe_flat
fp4_prefill_act_split
```

Observed default-path route counts:

| route | lines |
|-------|------:|
| `mmq_dense` | `4444` |
| `mmq_moe_dedup_unique` | `2960` |
| `mmq_moe_gather` | `2960` |
| `mmq_moe_flat` | `1480` |

Dominant `npp=512` shapes:

| count | route | source family | K | rows | ne12 |
|------:|-------|---------------|---:|-----:|-----:|
| `2560` | `mmq_moe_dedup_unique` | gate/up experts | `2048` | `512` | `512` |
| `2560` | `mmq_moe_gather` | gate/up experts | `2048` | `4096` | `512` |
| `2560` | `mmq_dense` | shared expert gate/up | `2048` | `512` | `1` |
| `1280` | `mmq_moe_flat` | down experts | `512` | `4096` | `512` |
| `1280` | `mmq_dense` | shared expert down | `512` | `512` | `1` |

`fp4_prefill_act_split` did not appear in the default trace because the native
large-M FP4 prefill route remains opt-in.

- [x] **Step 3: Source decision**

Fund a Phase66 source optimization only if one route is repeated, named, and material enough to plausibly remove at least `8%` of llama.cpp prefill time or at least `15 us/tok` cross-engine gap. Otherwise close Phase65 as attribution-only.

Decision: keep Phase65 as instrumentation plus attribution. Do not implement a
quantization optimization directly from route counts. Phase66 should first time
`quantize_mmq_nvfp4` versus `gather_mmq_fp4` with nsys/NVTX, because the trace
shows a repeated MoE gate/up dedup-and-gather chain but does not prove whether
the gather is the material part or just a cheap consequence of the existing
dedup optimization.

- [x] **Step 4: Release DGX lock**

Run:

```bash
ssh dgx.casa 'printf "FREE released-by-codex-phase65-quant-trace %s\n" "$(date +%s)" > /tmp/localai-gb10.lock'
```

---

### Task 5: Commit and Record

- [x] **Step 1: Commit llama.cpp source patch**

Commit only after build and gates pass:

```bash
git -C /home/mudler/_git/llama.cpp add ggml/src/ggml-cuda/mmq.cu ggml/src/ggml-cuda/fp4-gemm.cu
git -C /home/mudler/_git/llama.cpp commit -m "feat(cuda): trace activation quant routes" -m "Assisted-by: Codex:gpt-5"
```

Result:

- Local fork: `afc2c7030 feat(cuda): trace activation quant routes`
- DGX mirror: `7863194bd feat(cuda): trace activation quant routes`

- [x] **Step 2: Record LocalAI docs**

Update parity docs with artifact path, gate values, route distribution, and Phase66 decision.

- [x] **Step 3: Commit LocalAI docs**

```bash
git add -f docs/superpowers/plans/2026-07-01-quant-trace-phase65.md
git add backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md \
        backend/cpp/llama-cpp-localai-paged/docs/PARITY_HANDOFF.md \
        backend/cpp/llama-cpp-localai-paged/docs/VLLM_PARITY_LEVER_MAP.md
git commit -m "docs(paged): record quant trace phase" \
  -m "Assisted-by: Codex:gpt-5"
```
