# BF16 cuBLAS F32 Output Phase67 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Test whether BF16 projection GEMMs can write F32 output directly and remove the BF16-to-F32 conversion kernel without breaking inference.

**Architecture:** Add a default-off `LLAMA_BF16_CUBLAS_F32_OUT=1` branch in the CUDA BF16 cuBLAS path. The default path remains byte-identical. The opt-in path is accepted only if canonical md5/op gates pass and the measured GPU kernel-time reduction is material.

**Tech Stack:** llama.cpp CUDA backend, cuBLAS `cublasGemmEx`, DGX GB10, LocalAI parity docs, canonical md5 and backend-op gates.

---

## Guardrails

- Default behavior must remain unchanged when `LLAMA_BF16_CUBLAS_F32_OUT` is unset.
- The source patch must be small and local to the BF16 cuBLAS path.
- The opt-in path is rejected unless it passes:
  - MoE paged md5 `8cb0ce23777bf55f92f63d0292c756b0`
  - dense md5 `5951a5b4d624ce891e22ab5fca9bc439`
  - `test-backend-ops` `MUL_MAT`
- If the opt-in path changes md5, do not benchmark it as a parity shortcut unless a KL plan is explicitly created later.
- Do not regenerate LocalAI patch files in this phase.
- Do not push without explicit approval.

## Files

- Modify: `/home/mudler/_git/llama.cpp/ggml/src/ggml-cuda/ggml-cuda.cu`
- Create: `/home/mudler/_git/LocalAI/.claude/worktrees/feat+paged-attention/docs/superpowers/plans/2026-07-01-bf16-cublas-f32-output-phase67.md`
- Modify after DGX run: `/home/mudler/_git/LocalAI/.claude/worktrees/feat+paged-attention/backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md`
- Modify after DGX run: `/home/mudler/_git/LocalAI/.claude/worktrees/feat+paged-attention/backend/cpp/llama-cpp-localai-paged/docs/PARITY_HANDOFF.md`
- Modify after DGX run: `/home/mudler/_git/LocalAI/.claude/worktrees/feat+paged-attention/backend/cpp/llama-cpp-localai-paged/docs/VLLM_PARITY_LEVER_MAP.md`

---

### Task 1: Add Default-Off BF16 F32 Output Branch

- [x] **Step 1: Add env helper**

Add near the cuBLAS route helpers in `ggml-cuda.cu`:

```c++
static inline bool ggml_cuda_bf16_cublas_f32_out_enabled() {
    static const bool value = []() {
        const char * s = getenv("LLAMA_BF16_CUBLAS_F32_OUT");
        return s != nullptr && atoi(s) != 0;
    }();

    return value;
}
```

- [x] **Step 2: Branch BF16 cuBLAS output**

In the `src0->type == GGML_TYPE_BF16` cuBLAS branch, keep the current BF16
temporary path as the default. When the env is enabled, call `cublasGemmEx` with
the existing BF16 inputs and `dst_dd_i` as `CUDA_R_32F`, then skip the
`to_fp32_cuda` conversion:

```c++
if (ggml_cuda_bf16_cublas_f32_out_enabled()) {
    CUBLAS_CHECK(cublasGemmEx(ctx.cublas_handle(id), CUBLAS_OP_T, CUBLAS_OP_N,
            row_diff, src1_ncols, ne10,
            &alpha_f32,  src0_ptr, CUDA_R_16BF, ne00,
                         src1_ptr, CUDA_R_16BF, ne10,
            &beta_f32,   dst_dd_i, CUDA_R_32F,  ldc,
            CUBLAS_COMPUTE_32F,
            CUBLAS_GEMM_DEFAULT_TENSOR_OP));
} else {
    // existing BF16 temp plus BF16-to-F32 conversion
}
```

- [x] **Step 3: Local diff check**

Run:

```bash
git -C /home/mudler/_git/llama.cpp diff --check
```

Expected: exit `0`.

---

### Task 2: DGX Build and Default Gates

- [x] **Step 1: Confirm DGX is idle**

Run:

```bash
ssh dgx.casa 'cat /tmp/localai-gb10.lock 2>/dev/null || true; docker ps --format "{{.Names}}" | wc -l; (pgrep -af "[l]ocal-ai-worker" || true) | wc -l; nvidia-smi --query-compute-apps=pid,process_name,used_gpu_memory --format=csv,noheader | wc -l'
```

Expected: lock `FREE*`, Docker `0`, worker `0`, compute apps `0`.

- [x] **Step 2: Acquire lock**

Run:

```bash
ssh dgx.casa 'printf "codex-phase67-bf16-f32-out %s\n" "$(date +%s)" > /tmp/localai-gb10.lock'
```

- [x] **Step 3: Apply patch and build**

Apply the patch to `/home/mudler/llama-phase6-source`, then run:

```bash
ssh dgx.casa 'cd /home/mudler/llama-phase6-source && cmake --build build-cuda --target llama-completion llama-batched-bench test-backend-ops -j $(nproc)'
```

Expected: exit `0`.

- [x] **Step 4: Run default gates**

Run canonical MoE and dense md5 plus:

```bash
./test-backend-ops test -o MUL_MAT
```

Expected default path:

```text
MoE md5 8cb0ce23777bf55f92f63d0292c756b0
dense md5 5951a5b4d624ce891e22ab5fca9bc439
MUL_MAT 1146/1146
```

Observed artifact: `/home/mudler/bench/phase67_bf16_f32_out/20260701_144909`.

```text
default MoE md5 8cb0ce23777bf55f92f63d0292c756b0
default dense md5 5951a5b4d624ce891e22ab5fca9bc439
default MUL_MAT 1146/1146
```

---

### Task 3: Opt-In Correctness Gate

- [x] **Step 1: Run opt-in md5 gates**

Run the same MoE and dense commands with:

```bash
LLAMA_BF16_CUBLAS_F32_OUT=1
```

Expected: exact same md5s. If either md5 differs, reject the source shortcut.

Observed:

```text
opt-in MoE md5 8cb0ce23777bf55f92f63d0292c756b0
opt-in dense md5 5951a5b4d624ce891e22ab5fca9bc439
```

- [x] **Step 2: Run opt-in backend-op gate**

Run:

```bash
LLAMA_BF16_CUBLAS_F32_OUT=1 ./test-backend-ops test -o MUL_MAT
```

Expected: `1146/1146`.

Observed: `1146/1146`.

---

### Task 4: Benchmark if Correct

- [x] **Step 1: Run same-shape prefill A/B**

Only if Task 3 passes, run baseline and opt-in:

```bash
./llama-batched-bench -m /home/mudler/bench/q36-35b-a3b-nvfp4.gguf \
  -c 131072 -b 2048 -ub 512 -ngl 99 -fa on -npp 512,2048 -ntg 4 -npl 32
```

with and without `LLAMA_BF16_CUBLAS_F32_OUT=1`.

Observed MoE A/B:

| npp | default S_PP | opt-in S_PP | change |
|-----|-------------:|------------:|-------:|
| `512` | `2347.41` | `2402.34` | `+2.34%` |
| `2048` | `2440.18` | `2456.54` | `+0.67%` |

- [x] **Step 2: Profile opt-in if A/B improves**

Use nsys kernel summary to verify the BF16-to-F32 conversion rows shrink.

Observed opt-in `npp=512` profile:

| row | value |
|-----|------:|
| total GPU kernel time | `7020867757 ns` |
| `convert_unary<__nv_bfloat16, float>` | `0 ns`, `0` instances |
| `convert_unary<float, __nv_bfloat16>` | `159651026 ns`, `6840` instances, `2.27%` |

- [x] **Step 3: Source decision**

Keep the patch only if opt-in gates pass and it produces a material speedup.
Otherwise revert the source patch locally and record the rejection.

Decision: keep as a default-off opt-in path. It is correctness-clean and removes
the profiled BF16-to-F32 conversion row for this shape, but the speedup is small
and needs dense plus serving A/B before any default-on decision.

---

### Task 5: Commit and Record

- [x] **Step 1: Commit source only if accepted as default-off diagnostic or opt-in**

```bash
git -C /home/mudler/_git/llama.cpp add ggml/src/ggml-cuda/ggml-cuda.cu
git -C /home/mudler/_git/llama.cpp commit -m "feat(cuda): gate BF16 cuBLAS F32 output" -m "Assisted-by: Codex:gpt-5"
```

Result:

- Local fork: `ea0875d14 feat(cuda): gate BF16 cuBLAS F32 output`
- DGX mirror: `14fd69f1e feat(cuda): gate BF16 cuBLAS F32 output`

- [x] **Step 2: Record LocalAI docs**

Record artifact path, gates, A/B result, and decision.

- [x] **Step 3: Commit LocalAI docs**

```bash
git add -f docs/superpowers/plans/2026-07-01-bf16-cublas-f32-output-phase67.md
git add backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md \
        backend/cpp/llama-cpp-localai-paged/docs/PARITY_HANDOFF.md \
        backend/cpp/llama-cpp-localai-paged/docs/VLLM_PARITY_LEVER_MAP.md
git commit -m "docs(paged): record BF16 cuBLAS F32 output phase" \
  -m "Assisted-by: Codex:gpt-5"
```
