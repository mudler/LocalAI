# W4A16 Direct-Activation Phase61 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build and gate a default-off W4A16 grouped MoE prefill experiment that removes the measured sorted activation gather and separate f32-to-bf16 cast overhead from Phase60.

**Architecture:** Keep the existing default path unchanged. Add a second W4A16 grouped kernel mode behind `LLAMA_W4A16_DIRECT_A=1` that consumes the original `src1` activation tensor and the existing `ids_to_sorted` map directly, converting f32 activations to bf16 while loading A into shared memory. This tests the measured Phase60 hypothesis before any larger grouped-kernel body rewrite.

**Tech Stack:** llama.cpp fork (`/home/mudler/_git/llama.cpp`), ggml CUDA, CMake, `test-backend-ops`, DGX GB10 gates, LocalAI docs.

---

## Evidence

Phase60 artifact:

- `/home/mudler/bench/phase60_w4a16_current_profile/20260701_104915`

At MoE `npp=512`, forced W4A16 spent:

- `4.142s` in `w4a16_grouped_kernel<32,128,1,4,2>`
- `1.094s` in `k_get_rows_float<float,float>` sorted activation gathers
- `0.517s` in `w4a16_cast_act_f32_bf16`

Default FP4-MMQ spent:

- `2.712s` in `mul_mat_q<nvfp4,128>`
- `0.314s` in `quantize_mmq_nvfp4`

The direct-activation experiment can at most remove the `1.611s` gather+cast tax before kernel-body effects. It is a kill-gate experiment: if forced W4A16 remains far behind default MMQ, stop this branch and do not tune the W4A16 body again.

## Files

- Modify: `/home/mudler/_git/llama.cpp/ggml/src/ggml-cuda/w4a16-gemm.cuh`
  - Add declarations for a direct-activation engagement helper and direct kernel launcher.
- Modify: `/home/mudler/_git/llama.cpp/ggml/src/ggml-cuda/w4a16-gemm.cu`
  - Add `LLAMA_W4A16_DIRECT_A` parsing.
  - Add a pure helper for direct-mode route tests.
  - Add a direct-activation kernel variant that uses `ids_to_sorted` and original `src1` strides.
- Modify: `/home/mudler/_git/llama.cpp/ggml/src/ggml-cuda/ggml-cuda.cu`
  - Pass `ids_to_sorted`, original `src1` data pointer, and source strides to the direct launcher.
  - Skip `get_rows_cuda` and `w4a16_cast_act_f32_bf16` only in direct mode.
- Modify: `/home/mudler/_git/llama.cpp/tests/CMakeLists.txt`
  - Register a small policy unit test.
- Create: `/home/mudler/_git/llama.cpp/tests/test-cuda-w4a16-policy.cpp`
  - Unit-test direct-mode engagement logic without requiring CUDA execution.
- Create after DGX run: `/home/mudler/_git/LocalAI/.claude/worktrees/feat+paged-attention/docs/superpowers/plans/2026-07-01-w4a16-direct-activation-phase61-result.md`
  - Record gates, A/B, decision, and whether to keep or revert the fork patch.

## Task 1: Add Red Policy Tests

**Files:**

- Create: `/home/mudler/_git/llama.cpp/tests/test-cuda-w4a16-policy.cpp`
- Modify: `/home/mudler/_git/llama.cpp/tests/CMakeLists.txt`

- [ ] **Step 1: Add the failing policy test file**

Create `/home/mudler/_git/llama.cpp/tests/test-cuda-w4a16-policy.cpp`:

```cpp
#include "ggml-cuda/w4a16-gemm.cuh"

#include <cassert>
#include <cstdio>

static void test_direct_a_requires_master_w4a16() {
    const bool ok = ggml_cuda_w4a16_direct_a_should_engage_params(
        GGML_TYPE_NVFP4, GGML_TYPE_F32, GGML_TYPE_F32,
        /*blackwell=*/true,
        /*w4a16_prefill_m=*/0,
        /*direct_a=*/true,
        /*tokens=*/512,
        /*k=*/2048,
        /*n=*/512);
    assert(!ok);
}

static void test_direct_a_requires_direct_flag() {
    const bool ok = ggml_cuda_w4a16_direct_a_should_engage_params(
        GGML_TYPE_NVFP4, GGML_TYPE_F32, GGML_TYPE_F32,
        /*blackwell=*/true,
        /*w4a16_prefill_m=*/1,
        /*direct_a=*/false,
        /*tokens=*/512,
        /*k=*/2048,
        /*n=*/512);
    assert(!ok);
}

static void test_direct_a_engages_for_large_nvfp4_moe_prefill_shape() {
    const bool ok = ggml_cuda_w4a16_direct_a_should_engage_params(
        GGML_TYPE_NVFP4, GGML_TYPE_F32, GGML_TYPE_F32,
        /*blackwell=*/true,
        /*w4a16_prefill_m=*/1,
        /*direct_a=*/true,
        /*tokens=*/512,
        /*k=*/2048,
        /*n=*/512);
    assert(ok);
}

static void test_direct_a_rejects_decode_sized_shape() {
    const bool ok = ggml_cuda_w4a16_direct_a_should_engage_params(
        GGML_TYPE_NVFP4, GGML_TYPE_F32, GGML_TYPE_F32,
        /*blackwell=*/true,
        /*w4a16_prefill_m=*/128,
        /*direct_a=*/true,
        /*tokens=*/128,
        /*k=*/2048,
        /*n=*/512);
    assert(!ok);
}

int main() {
    test_direct_a_requires_master_w4a16();
    test_direct_a_requires_direct_flag();
    test_direct_a_engages_for_large_nvfp4_moe_prefill_shape();
    test_direct_a_rejects_decode_sized_shape();
    std::puts("test-cuda-w4a16-policy: OK");
    return 0;
}
```

- [ ] **Step 2: Register the test**

Add to `/home/mudler/_git/llama.cpp/tests/CMakeLists.txt` near the other small C++ tests:

```cmake
if (GGML_CUDA)
    llama_test_executable(test-cuda-w4a16-policy test-cuda-w4a16-policy.cpp)
    target_link_libraries(test-cuda-w4a16-policy PRIVATE ggml)
endif()
```

- [ ] **Step 3: Verify RED**

Run:

```bash
cd /home/mudler/_git/llama.cpp
cmake --build build --target test-cuda-w4a16-policy -j2
```

Expected: build fails because `ggml_cuda_w4a16_direct_a_should_engage_params` is not declared.

## Task 2: Add Direct-A Policy Helper

**Files:**

- Modify: `/home/mudler/_git/llama.cpp/ggml/src/ggml-cuda/w4a16-gemm.cuh`
- Modify: `/home/mudler/_git/llama.cpp/ggml/src/ggml-cuda/w4a16-gemm.cu`

- [ ] **Step 1: Declare the helper**

Add to `w4a16-gemm.cuh`:

```cpp
bool ggml_cuda_w4a16_direct_a_enabled();

bool ggml_cuda_w4a16_direct_a_should_engage_params(
    ggml_type src0_type,
    ggml_type src1_type,
    ggml_type dst_type,
    bool blackwell,
    int64_t w4a16_prefill_m,
    bool direct_a,
    int64_t tokens,
    int64_t k,
    int64_t n);
```

- [ ] **Step 2: Implement the helper**

Add to `w4a16-gemm.cu` near `ggml_cuda_w4a16_prefill_enabled()`:

```cpp
bool ggml_cuda_w4a16_direct_a_enabled() {
    static const bool enabled = [] {
        const char * e = getenv("LLAMA_W4A16_DIRECT_A");
        return e != nullptr && atoi(e) != 0;
    }();
    return enabled;
}

bool ggml_cuda_w4a16_direct_a_should_engage_params(
        ggml_type src0_type,
        ggml_type src1_type,
        ggml_type dst_type,
        bool blackwell,
        int64_t w4a16_prefill_m,
        bool direct_a,
        int64_t tokens,
        int64_t k,
        int64_t n) {
    if (!direct_a || w4a16_prefill_m <= 0) {
        return false;
    }
    if (src0_type != GGML_TYPE_NVFP4 || src1_type != GGML_TYPE_F32 || dst_type != GGML_TYPE_F32) {
        return false;
    }
    if (!blackwell || tokens <= w4a16_prefill_m) {
        return false;
    }
    return k % 64 == 0 && n % 128 == 0;
}
```

- [ ] **Step 3: Verify GREEN for policy test**

Run:

```bash
cd /home/mudler/_git/llama.cpp
cmake --build build --target test-cuda-w4a16-policy -j2
./build/bin/test-cuda-w4a16-policy
```

Expected:

```text
test-cuda-w4a16-policy: OK
```

## Task 3: Add Direct-A Kernel Launcher Skeleton

**Files:**

- Modify: `/home/mudler/_git/llama.cpp/ggml/src/ggml-cuda/w4a16-gemm.cuh`
- Modify: `/home/mudler/_git/llama.cpp/ggml/src/ggml-cuda/w4a16-gemm.cu`

- [ ] **Step 1: Declare the direct launcher**

Add to `w4a16-gemm.cuh`:

```cpp
void ggml_cuda_mul_mat_id_w4a16_grouped_direct_a(
    ggml_backend_cuda_context & ctx,
    const ggml_tensor * src0,
    const float * src1,
    const int32_t * ids_to_sorted,
    float * dst_sorted,
    const int * tokens_per_expert,
    int64_t n_experts,
    int64_t n_expert_used,
    int64_t k,
    int64_t n,
    size_t src1_nb1,
    size_t src1_nb2,
    cudaStream_t stream);
```

- [ ] **Step 2: Add a stub that preserves behavior**

Add to `w4a16-gemm.cu` after `ggml_cuda_mul_mat_id_w4a16_grouped()`:

```cpp
void ggml_cuda_mul_mat_id_w4a16_grouped_direct_a(
        ggml_backend_cuda_context & ctx,
        const ggml_tensor * src0,
        const float * src1,
        const int32_t * ids_to_sorted,
        float * dst_sorted,
        const int * tokens_per_expert,
        int64_t n_experts,
        int64_t n_expert_used,
        int64_t k,
        int64_t n,
        size_t src1_nb1,
        size_t src1_nb2,
        cudaStream_t stream) {
    GGML_UNUSED(ctx);
    GGML_UNUSED(src0);
    GGML_UNUSED(src1);
    GGML_UNUSED(ids_to_sorted);
    GGML_UNUSED(dst_sorted);
    GGML_UNUSED(tokens_per_expert);
    GGML_UNUSED(n_experts);
    GGML_UNUSED(n_expert_used);
    GGML_UNUSED(k);
    GGML_UNUSED(n);
    GGML_UNUSED(src1_nb1);
    GGML_UNUSED(src1_nb2);
    GGML_UNUSED(stream);
    GGML_ABORT("LLAMA_W4A16_DIRECT_A selected before direct-A kernel implementation");
}
```

- [ ] **Step 3: Verify build still passes**

Run:

```bash
cd /home/mudler/_git/llama.cpp
cmake --build build --target test-cuda-w4a16-policy llama-batched-bench -j2
./build/bin/test-cuda-w4a16-policy
```

Expected: test passes and `llama-batched-bench` builds.

## Task 4: Route Direct-A Mode Without Touching Default Path

**Files:**

- Modify: `/home/mudler/_git/llama.cpp/ggml/src/ggml-cuda/ggml-cuda.cu`

- [ ] **Step 1: Add direct-mode branch**

In `ggml_cuda_mul_mat_id`, after `ids_to_sorted` and `ids_from_sorted` are prepared, replace the W4A16 branch with this structure:

```cpp
    const bool use_w4a16_direct_a = ggml_cuda_w4a16_direct_a_should_engage_params(
        src0->type, src1->type, dst->type,
        blackwell_mma_available(cc),
        ggml_cuda_w4a16_prefill_m(),
        ggml_cuda_w4a16_direct_a_enabled(),
        ne12,
        ne10,
        ne0);

    if (use_w4a16_direct_a) {
        ggml_cuda_mul_mat_id_w4a16_grouped_direct_a(ctx, src0,
            (const float *) src1->data, ids_to_sorted, (float *) dst_sorted.ptr,
            tokens_per_expert.data(), ne02, n_expert_used, ne10, ne0,
            nb11, nb12, stream);
    } else {
        get_rows_cuda(src1->data, src1->type, ids_to_sorted, src1_sorted.ptr, type_src1_sorted,
            ne10, nb11, nb12, nb13,
            ne_get_rows, 1, 1, sizeof(int32_t), ne_get_rows*sizeof(int32_t), ne_get_rows*sizeof(int32_t),
            ne10*ts_src1_sorted, ne_get_rows*ne10*ts_src1_sorted, ne_get_rows*ne10*ts_src1_sorted, stream);
        CUDA_CHECK(cudaGetLastError());

        if (ggml_cuda_w4a16_moe_grouped_should_engage(src0, src1, dst, cc)) {
            ggml_cuda_mul_mat_id_w4a16_grouped(ctx, src0,
                (const float *) src1_sorted.ptr, (float *) dst_sorted.ptr,
                tokens_per_expert.data(), ne02, ne10, ne0, stream);
        } else {
            // existing per-expert loop remains here unchanged
        }
    }
```

Do not leave two `get_rows_cuda` calls in the direct path.

- [ ] **Step 2: Verify default path**

Run:

```bash
cd /home/mudler/_git/llama.cpp
cmake --build build --target test-cuda-w4a16-policy llama-batched-bench -j2
./build/bin/test-cuda-w4a16-policy
```

Expected: build and policy test pass. Do not run `LLAMA_W4A16_DIRECT_A=1` yet; the stub must abort if selected.

## Task 5: Implement Direct-A Kernel

**Files:**

- Modify: `/home/mudler/_git/llama.cpp/ggml/src/ggml-cuda/w4a16-gemm.cu`

- [ ] **Step 1: Add the direct kernel variant**

Copy `w4a16_grouped_kernel` into a new template named `w4a16_grouped_direct_a_kernel`. Change only the A-load section:

```cpp
const int32_t src_row = ids_to_sorted[row0 + r];
const int64_t token = src_row / n_expert_used;
const int64_t slot  = src_row - token * n_expert_used;
const char * src_base = ((const char *) src1) + token * src1_nb2 + slot * src1_nb1;
const float * src = (const float *) (src_base + (int64_t) kt * BK * sizeof(float) + c * 8 * sizeof(float));
```

Load eight f32 values, convert to bf16, and store into `sA[st]`. This replaces the old `cp.async` A load because the source conversion is no longer a raw copy:

```cpp
nv_bfloat16 tmp[8];
#pragma unroll
for (int q = 0; q < 8; ++q) {
    tmp[q] = __float2bfloat16(src[q]);
}
uint4 packed = *reinterpret_cast<const uint4 *>(tmp);
*reinterpret_cast<uint4 *>(((char *) sA[st]) + (r*ASTR + c*4)*sizeof(uint32_t)) = packed;
```

Keep W `cp.async` unchanged.

- [ ] **Step 2: Wire the direct launcher to the new kernel**

Replace the stub body with a launcher that mirrors `ggml_cuda_mul_mat_id_w4a16_grouped_impl`, but:

- does not allocate `Abf`;
- does not call `w4a16_cast_act_f32_bf16`;
- passes `src1`, `ids_to_sorted`, `n_expert_used`, `src1_nb1`, and `src1_nb2` into the direct kernel.

- [ ] **Step 3: Build**

Run:

```bash
cd /home/mudler/_git/llama.cpp
cmake --build build --target test-cuda-w4a16-policy test-backend-ops llama-batched-bench llama-completion -j2
./build/bin/test-cuda-w4a16-policy
```

Expected: build succeeds and policy test passes.

## Task 6: Local CUDA Correctness Gate

**Files:** none.

- [ ] **Step 1: Run forced W4A16 direct-A op gate**

Run on a CUDA host:

```bash
cd /home/mudler/_git/llama.cpp
LLAMA_W4A16_PREFILL_M=1 LLAMA_W4A16_DIRECT_A=1 ./build/bin/test-backend-ops test -b CUDA0 -o MUL_MAT_ID -j 1
```

Expected: `806/806 tests passed`.

- [ ] **Step 2: Run default op gate**

Run:

```bash
cd /home/mudler/_git/llama.cpp
./build/bin/test-backend-ops test -b CUDA0 -o MUL_MAT_ID -j 1
```

Expected: `806/806 tests passed`.

## Task 7: DGX Inference and Performance Gate

**Files:** none.

- [ ] **Step 1: Preflight DGX**

Run:

```bash
ssh dgx.casa 'echo docker=$(docker ps -q | wc -l); echo compute=$(nvidia-smi --query-compute-apps=pid --format=csv,noheader | sed "/^$/d" | wc -l); cat /tmp/localai-gpu.lock 2>/dev/null || true; pgrep -af "[l]ocal-ai-worker|[v]llm|[l]lama-server" || true'
```

Expected: Docker `0`, compute `0`, lock `FREE*`, and no worker/server process.

- [ ] **Step 2: Apply patch to clean DGX mirror and build**

Use the fork diff for this one patch only, apply it to `~/llama-phase6-source`, and build `build-cuda`. Do not leave the DGX mirror dirty after the phase.

- [ ] **Step 3: Run pre gates**

Run the canonical MoE/dense md5 and `MUL_MAT`/`MUL_MAT_ID` gates:

```bash
LLAMA_KV_PAGED=1 LLAMA_MOE_FORCE_GRAPHS=1 GGML_NO_BACKTRACE=1 ./llama-completion -m /home/mudler/bench/q36-35b-a3b-nvfp4.gguf -ngl 99 -fa on -p "The capital of France is" -n 48 --temp 0 --seed 1 </dev/null | md5sum
LLAMA_KV_PAGED=1 LLAMA_MOE_FORCE_GRAPHS=1 GGML_NO_BACKTRACE=1 ./llama-completion -m /home/mudler/bench/q36-27b-nvfp4.gguf -ngl 99 -fa on -p "The capital of France is" -n 48 --temp 0 --seed 1 </dev/null | md5sum
./test-backend-ops test -b CUDA0 -o MUL_MAT -j 1
./test-backend-ops test -b CUDA0 -o MUL_MAT_ID -j 1
```

Expected:

- MoE md5 `8cb0ce23777bf55f92f63d0292c756b0`
- dense md5 `5951a5b4d624ce891e22ab5fca9bc439`
- `MUL_MAT` `1146/1146`
- `MUL_MAT_ID` `806/806`

- [ ] **Step 4: Run W4A16 A/B**

Run:

```bash
BASE="LLAMA_KV_PAGED=1 LLAMA_MOE_FORCE_GRAPHS=1 GGML_NO_BACKTRACE=1"
env $BASE ./llama-batched-bench -m /home/mudler/bench/q36-35b-a3b-nvfp4.gguf -c 131072 -b 2048 -ub 512 -ngl 99 -fa on -npp 512,2048 -ntg 4 -npl 32
env $BASE LLAMA_W4A16_PREFILL_M=1 ./llama-batched-bench -m /home/mudler/bench/q36-35b-a3b-nvfp4.gguf -c 131072 -b 2048 -ub 512 -ngl 99 -fa on -npp 512,2048 -ntg 4 -npl 32
env $BASE LLAMA_W4A16_PREFILL_M=1 LLAMA_W4A16_DIRECT_A=1 ./llama-batched-bench -m /home/mudler/bench/q36-35b-a3b-nvfp4.gguf -c 131072 -b 2048 -ub 512 -ngl 99 -fa on -npp 512,2048 -ntg 4 -npl 32
```

Expected decision gate:

- Keep the patch only if direct-A improves forced W4A16 S_PP by at least `+12%` at either `npp=512` or `npp=2048` without regressing the other by more than `1%`.
- Continue deeper W4A16 body work only if direct-A reaches at least `0.75x` default FP4-MMQ S_PP.
- Otherwise revert the code patch and record Phase61 as rejected.

- [ ] **Step 5: Run post gates and cleanup**

Run the same md5/op gates as Step 3, revert the temporary DGX patch, confirm `git status --short` is clean, and release `/tmp/localai-gpu.lock` as `FREE phase61-cleanup ...`.

## Task 8: Commit or Revert

**Files:**

- If kept: commit fork code first.
- Always: update LocalAI docs with the Phase61 result.

- [ ] **Step 1: If performance gate passes, commit fork code**

Run:

```bash
cd /home/mudler/_git/llama.cpp
git status --short
git add ggml/src/ggml-cuda/w4a16-gemm.cuh ggml/src/ggml-cuda/w4a16-gemm.cu ggml/src/ggml-cuda/ggml-cuda.cu tests/CMakeLists.txt tests/test-cuda-w4a16-policy.cpp
git commit -m "feat(cuda): add W4A16 direct activation prefill path" -m "Assisted-by: Codex:gpt-5"
```

- [ ] **Step 2: If performance gate fails, revert fork code**

Run:

```bash
cd /home/mudler/_git/llama.cpp
git diff > /tmp/phase61-w4a16-direct-a-rejected.diff
git restore ggml/src/ggml-cuda/w4a16-gemm.cuh ggml/src/ggml-cuda/w4a16-gemm.cu ggml/src/ggml-cuda/ggml-cuda.cu tests/CMakeLists.txt
rm -f tests/test-cuda-w4a16-policy.cpp
git status --short
```

- [ ] **Step 3: Update LocalAI docs**

Create `docs/superpowers/plans/2026-07-01-w4a16-direct-activation-phase61-result.md` with the artifact path, gate table, A/B table, and keep/reject decision. Update:

- `backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md`
- `backend/cpp/llama-cpp-localai-paged/docs/VLLM_PARITY_LEVER_MAP.md`
- `backend/cpp/llama-cpp-localai-paged/docs/PARITY_HANDOFF.md`

- [ ] **Step 4: Commit LocalAI docs**

Run:

```bash
cd /home/mudler/_git/LocalAI/.claude/worktrees/feat+paged-attention
git add backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md backend/cpp/llama-cpp-localai-paged/docs/VLLM_PARITY_LEVER_MAP.md backend/cpp/llama-cpp-localai-paged/docs/PARITY_HANDOFF.md
git add -f docs/superpowers/plans/2026-07-01-w4a16-direct-activation-phase61-result.md
git commit -m "docs(paged): record W4A16 direct activation phase" -m "Assisted-by: Codex:gpt-5"
```

## Self-Review

- Spec coverage: The plan addresses Phase60's measured W4A16 sorted-gather and cast overhead before any grouped-kernel-body rewrite.
- Placeholder scan: No `TBD`, `TODO`, or unspecified test commands remain.
- Type consistency: Helper names use the `ggml_cuda_w4a16_direct_a_*` prefix consistently across declaration, test, implementation, and route branch.
