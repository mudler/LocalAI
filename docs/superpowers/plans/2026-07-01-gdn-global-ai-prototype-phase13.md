# GDN Global-Ai Prototype Phase 13 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement and test a default-off C32 GDN prefill prototype that computes f32 Ai once per chunk/head and reuses it across two value slabs.

**Architecture:** The prototype adds one Ai precompute kernel plus one Ai-consuming chunked kernel in `gated_delta_net.cu`. Scratch is allocated from the existing ggml CUDA pool in `ggml_cuda_op_gated_delta_net`, scoped to the op, and only used when `GDN_GLOBAL_AI32=1`.

**Tech Stack:** llama.cpp CUDA, ggml CUDA pool allocator, GB10 DGX benchmark harness, Qwen3.6 NVFP4 GGUF gates.

---

## Guardrails

- Default path remains current C16 M5.
- Candidate engages only with `GDN_GLOBAL_AI32=1`.
- Prototype only supports `S_v=128`, `C=32`, `DV_TILE=64`, f32 Ai.
- Keep `GDN_CHUNK_MIN > 1`; decode must never use this path.
- Do not add f16/BF16 Ai until f32 Ai wins.
- Do not generate a LocalAI patch unless the fork implementation passes gates
  and improves S_PP.

## Task 1: Preflight

**Files:**
- Read: `/home/mudler/_git/llama.cpp/ggml/src/ggml-cuda/gated_delta_net.cu`
- Artifact: `/home/mudler/bench/phase13_gdn_global_ai32/`

- [ ] **Step 1: Check DGX is free**

Run:

```bash
ssh dgx.casa 'set -e
echo docker=$(docker ps -q | wc -l)
echo local_ai_worker=$(docker ps --format "{{.Names}}" | grep -c local-ai-worker || true)
echo compute=$(nvidia-smi --query-compute-apps=pid --format=csv,noheader | sed "/^$/d" | wc -l)
if [ -f ~/gpu_bench_lock/owner ]; then cat ~/gpu_bench_lock/owner; else echo FREE-no-lock-file; fi'
```

Expected:

```text
docker=0
local_ai_worker=0
compute=0
FREE...
```

- [ ] **Step 2: Record provenance**

Run:

```bash
git -C /home/mudler/_git/llama.cpp status --short
git -C /home/mudler/_git/llama.cpp rev-parse HEAD
ssh dgx.casa 'cd /home/mudler/llama-phase6-source && git status --short && git rev-parse HEAD'
```

Expected: both llama.cpp trees are clean.

- [ ] **Step 3: Create artifacts**

Run:

```bash
ssh dgx.casa 'mkdir -p /home/mudler/bench/phase13_gdn_global_ai32/{gates,ab,rejected}'
```

Expected: command exits 0.

## Task 2: Add Ai Scratch Plumbing

**Files:**
- Modify: `/home/mudler/_git/llama.cpp/ggml/src/ggml-cuda/gated_delta_net.cu`

- [ ] **Step 1: Add env selector in `ggml_cuda_op_gated_delta_net`**

Add after `keep_rs` is computed:

```cpp
static const bool gdn_global_ai32 = []{
    const char * e = getenv("GDN_GLOBAL_AI32");
    return e && atoi(e) != 0;
}();
```

- [ ] **Step 2: Allocate Ai scratch only for supported calls**

Add:

```cpp
float * ai32_d = nullptr;
int64_t ai32_chunks = 0;
ggml_cuda_pool_alloc<float> ai32_scratch(ctx.pool());
if (gdn_global_ai32 && !kda && !keep_rs && S_v == 128 && n_tokens > 1) {
    ai32_chunks = (n_tokens + 31) / 32;
    ai32_d = ai32_scratch.alloc((size_t) n_seqs * H * ai32_chunks * 32 * 32);
}
```

Pass `ai32_d` and `ai32_chunks` into the non-KDA/non-keep launch call only.
Other launch calls pass `nullptr, 0`.

- [ ] **Step 3: Extend `launch_gated_delta_net` signature**

Change the signature to include:

```cpp
float * ai32_d, int64_t ai32_chunks,
```

before `float scale`. Thread these through all four call sites.

## Task 3: Add Ai Precompute Kernel

**Files:**
- Modify: `/home/mudler/_git/llama.cpp/ggml/src/ggml-cuda/gated_delta_net.cu`

- [ ] **Step 1: Add `gdn_ai32_cuda`**

Add a kernel near `gated_delta_net_chunked_cuda`:

```cpp
template <int S_v, int C>
__global__ void gdn_ai32_cuda(
        const float * __restrict__ k,
        const float * __restrict__ g,
        const float * __restrict__ beta,
        float * __restrict__ ai,
        int64_t H, int64_t n_tokens, int64_t n_seqs,
        int64_t sq1, int64_t sq2, int64_t sq3,
        int64_t sb1, int64_t sb2, int64_t sb3,
        uint3 neqk1_magic, uint3 rq3_magic) {
    // CTA: blockIdx.x=head, blockIdx.y=seq, blockIdx.z=chunk.
    // Shared: Kc[C*S_v], A[C*C], csh[C], gam[C], bet[C], KKsh[C*C].
    // Compute Kc, prefix csh/gam, KK, A, then exact f32 inverse into ai.
}
```

The inverse algorithm must match the existing M5 f32 inverse:

```cpp
if (j < C) {
    if (j < Cc) {
        float x[C];
        for (int r = 0; r < C; r++) x[r] = 0.0f;
        x[j] = 1.0f;
        for (int r = j + 1; r < Cc; r++) {
            float acc = 0.0f;
            for (int m = j; m < r; m++) acc += A[r * C + m] * x[m];
            x[r] = -acc;
        }
        for (int r = 0; r < C; r++) ai[ai_base + r * C + j] = x[r];
    } else {
        for (int r = 0; r < C; r++) ai[ai_base + r * C + j] = 0.0f;
    }
}
```

Use fixed stride `C` in scratch, zeroing out-of-range tail rows/columns.

- [ ] **Step 2: Add launcher**

Add:

```cpp
template <int S_v, int C>
static void launch_gdn_ai32(..., float * ai32_d, int64_t ai32_chunks, cudaStream_t stream)
```

Launch grid:

```cpp
dim3 grid_dims(H, n_seqs, ai32_chunks);
dim3 block_dims(S_v, 1, 1);
```

Dynamic smem:

```cpp
((size_t) C * S_v + (size_t) C * C + (size_t) 3 * C + (size_t) C * C) * sizeof(float)
```

## Task 4: Add Ai-Consuming C32 Slab Kernel

**Files:**
- Modify: `/home/mudler/_git/llama.cpp/ggml/src/ggml-cuda/gated_delta_net.cu`

- [ ] **Step 1: Add `gated_delta_net_chunked_ai32_cuda`**

Add a separate kernel rather than overloading the shipped M5 body:

```cpp
template <int S_v, int C, int DV_TILE>
__global__ void gated_delta_net_chunked_ai32_cuda(
        const float * __restrict__ q,
        const float * __restrict__ k,
        const float * __restrict__ v,
        const float * __restrict__ g,
        const float * __restrict__ beta,
        const float * __restrict__ curr_state,
        float * __restrict__ dst,
        const float * __restrict__ ai,
        int64_t H, int64_t n_tokens, int64_t n_seqs,
        int64_t sq1, int64_t sq2, int64_t sq3,
        int64_t sv1, int64_t sv2, int64_t sv3,
        int64_t sb1, int64_t sb2, int64_t sb3,
        uint3 neqk1_magic, uint3 rq3_magic,
        float scale, float * __restrict__ state_dst,
        const int32_t * __restrict__ ids, int rs_head) {
    // CTA: blockIdx.x=head, blockIdx.y=seq, blockIdx.z=value slab.
    // C=32, DV_TILE=64.
    // Load the full source state stride S_v*S_v but own only columns [slab*DV_TILE, +DV_TILE).
    // For every chunk, load Kc/Qc/csh/gam/bet, build RHS, load Ai, apply U = Ai*RHS,
    // build P from QK, compute O, update owned state columns, write owned state columns.
}
```

Use the Phase 10 tail-row fix:

```cpp
Ud[j * C + t] = (t < Cc) ? staged_value : 0.0f;
```

and use full state stride for reads/writes:

```cpp
(int64_t) seq * H * S_v * S_v + (int64_t) h_idx * S_v * S_v
```

- [ ] **Step 2: Add launcher**

Add:

```cpp
template <int S_v, int C, int DV_TILE>
static void launch_gdn_chunked_ai32(..., const float * ai32_d, int64_t ai32_chunks, ...)
```

Launch grid:

```cpp
dim3 grid_dims(H, n_seqs, S_v / DV_TILE);
dim3 block_dims(DV_TILE, 1, 1);
```

The smem formula must stay under the C32 slab Phase 10 budget:

```cpp
((size_t) S_v * DV_TILE + (size_t) 2 * C * S_v + (size_t) DV_TILE * C
 + (size_t) C * C + (size_t) 3 * C + (size_t) C * C
 + (size_t) DV_TILE * C) * sizeof(float)
```

## Task 5: Route Candidate

**Files:**
- Modify: `/home/mudler/_git/llama.cpp/ggml/src/ggml-cuda/gated_delta_net.cu`

- [ ] **Step 1: Add route in `launch_gated_delta_net`**

Before the existing `GDN_CHUNKED_LAUNCH` switch:

```cpp
if (ai32_d != nullptr && ai32_chunks > 0 && S_v == 128 && n_tokens >= gdn_chunk_min) {
    launch_gdn_ai32<128, 32>(...);
    launch_gdn_chunked_ai32<128, 32, 64>(...);
    return;
}
```

The route must require `!KDA && !keep_rs_t` via the existing template branch and
must not trigger for decode-sized calls.

- [ ] **Step 2: Keep default path unchanged**

Run:

```bash
git diff -- ggml/src/ggml-cuda/gated_delta_net.cu
```

Check that default `GDN_TC=5` still launches `launch_gdn_chunked<128, 16, 4>`.

## Task 6: Build and Correctness Gates

**Files:**
- Artifact: `/home/mudler/bench/phase13_gdn_global_ai32/gates/`

- [ ] **Step 1: Mirror and build**

Run:

```bash
rsync -a /home/mudler/_git/llama.cpp/ggml/src/ggml-cuda/gated_delta_net.cu \
  dgx.casa:/home/mudler/llama-phase6-source/ggml/src/ggml-cuda/gated_delta_net.cu
ssh dgx.casa 'cd /home/mudler/llama-phase6-source/build-cuda && cmake --build . --target test-backend-ops llama-completion llama-batched-bench -j 8'
```

Expected: build exits 0.

- [ ] **Step 2: Run op gates**

Run:

```bash
ssh dgx.casa 'cd /home/mudler/llama-phase6-source/build-cuda/bin
ART=$HOME/bench/phase13_gdn_global_ai32/gates
./test-backend-ops test -b CUDA0 -o GATED_DELTA_NET -j 1 > "$ART/gated_delta_net_default.txt" 2>&1
GDN_GLOBAL_AI32=1 GDN_TC=5 GDN_CHUNK_MIN=2 ./test-backend-ops test -b CUDA0 -o GATED_DELTA_NET -j 1 > "$ART/gated_delta_net_global_ai32.txt" 2>&1'
```

Expected: both logs show CUDA0 OK for all cases.

- [ ] **Step 3: Run canonical md5 gates**

Run default and candidate MoE/dense completion gates. Expected:

```text
MoE   8cb0ce23777bf55f92f63d0292c756b0
Dense 5951a5b4d624ce891e22ab5fca9bc439
```

If candidate md5 differs, run the KL gate before benchmarking.

## Task 7: Performance A/B

**Files:**
- Artifact: `/home/mudler/bench/phase13_gdn_global_ai32/ab/`

- [ ] **Step 1: Run same-session A/B**

Run MoE and dense:

```bash
LBASE="LLAMA_KV_PAGED=1 LLAMA_MOE_FORCE_GRAPHS=1 GDN_TC=5 GDN_CHUNK_MIN=64 GGML_NO_BACKTRACE=1"
LCAND="LLAMA_KV_PAGED=1 LLAMA_MOE_FORCE_GRAPHS=1 GDN_TC=5 GDN_CHUNK_MIN=64 GDN_GLOBAL_AI32=1 GGML_NO_BACKTRACE=1"
```

Use:

```bash
./llama-batched-bench -c 131072 -b 2048 -ub 512 -ngl 99 -fa on -npp 512,2048 -ntg 4 -npl 32
```

Expected: candidate improves S_PP without dense regression.

- [ ] **Step 2: Decide**

Accept only if:

- op gate passes,
- md5 is canonical or KL-benign,
- MoE S_PP improves,
- dense S_PP does not regress outside noise.

Reject if flat or slower.

## Task 8: Mirror or Reject

**Files:**
- Create if accepted: `backend/cpp/llama-cpp-localai-paged/patches/paged/0055-...patch`
- Modify: `backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md`
- Modify: `backend/cpp/llama-cpp-localai-paged/docs/VLLM_PARITY_LEVER_MAP.md`
- Modify: `backend/cpp/llama-cpp-localai-paged/docs/VLLM_PARITY_FINAL.md`
- Modify: `backend/cpp/llama-cpp-localai-paged/docs/PARITY_HANDOFF.md`

- [ ] **Step 1: If accepted, commit fork patch and generate LocalAI patch**

Run:

```bash
git -C /home/mudler/_git/llama.cpp add ggml/src/ggml-cuda/gated_delta_net.cu
git -C /home/mudler/_git/llama.cpp commit -m "feat(cuda): add GDN global Ai32 prefill prototype"
git -C /home/mudler/_git/llama.cpp format-patch -1 HEAD --stdout \
  > backend/cpp/llama-cpp-localai-paged/patches/paged/0055-feat-cuda-add-GDN-global-Ai32-prefill-prototype.patch
```

- [ ] **Step 2: If rejected, save diff and restore**

Run:

```bash
git -C /home/mudler/_git/llama.cpp diff -- ggml/src/ggml-cuda/gated_delta_net.cu \
  > /home/mudler/bench/phase13_gdn_global_ai32/rejected/global_ai32_rejected.diff
git -C /home/mudler/_git/llama.cpp checkout -- ggml/src/ggml-cuda/gated_delta_net.cu
ssh dgx.casa 'cd /home/mudler/llama-phase6-source && git checkout -- ggml/src/ggml-cuda/gated_delta_net.cu'
```

- [ ] **Step 3: Commit LocalAI docs**

Commit accepted patch/docs or rejected docs with:

```bash
git commit -m "docs(paged): record GDN global Ai32 result" \
  -m "Assisted-by: Codex:gpt-5"
```
