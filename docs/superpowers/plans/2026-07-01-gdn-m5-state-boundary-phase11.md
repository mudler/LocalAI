# GDN M5 State-Boundary Phase 11 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Test a low-conflict C=16 M5 GDN prefill variant that moves/reuses the QS state-boundary product earlier without changing chunk size or decode routing.

**Architecture:** Phase 11 is fork-first and default-off. It keeps the shipped C=16 M5 path as the baseline, adds one explicit env-selected candidate in `gated_delta_net.cu`, and accepts it only if focused op gates, canonical md5 gates, and same-session GB10 A/B benchmarks all pass.

**Tech Stack:** llama.cpp CUDA GDN kernel, DGX GB10 CUDA build, Qwen3.6 NVFP4 MoE/dense GGUF, LocalAI paged patch stack.

---

## Guardrails

- Do not reintroduce C32 slab code.
- Do not change the default `GDN_TC=5` path until the candidate wins.
- Keep `GDN_CHUNK_MIN > 1`; decode must stay on the sequential recurrence.
- Prefer one env-gated shortcut over new helper files or global scratch.
- Gate every change with `GATED_DELTA_NET` and canonical MoE/dense md5 before
  any performance claim.

## Task 1: Preflight and Baseline

**Files:**
- Read-only: `/home/mudler/_git/llama.cpp/ggml/src/ggml-cuda/gated_delta_net.cu`
- Artifact: `/home/mudler/bench/phase11_gdn_m5_state_boundary/`

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

- [ ] **Step 2: Record source provenance**

Run:

```bash
ssh dgx.casa 'cd /home/mudler/llama-phase6-source && git status --short && git rev-parse HEAD'
git -C /home/mudler/_git/llama.cpp status --short
git -C /home/mudler/_git/llama.cpp rev-parse HEAD
```

Expected: clean llama.cpp fork and DGX mirror before source edits.

- [ ] **Step 3: Create artifact directory**

Run:

```bash
ssh dgx.casa 'mkdir -p /home/mudler/bench/phase11_gdn_m5_state_boundary/{gates,ab,rejected}'
```

Expected: command exits 0.

## Task 2: Add Default-Off Candidate

**Files:**
- Modify: `/home/mudler/_git/llama.cpp/ggml/src/ggml-cuda/gated_delta_net.cu`
- Mirror: `/home/mudler/llama-phase6-source/ggml/src/ggml-cuda/gated_delta_net.cu`

- [ ] **Step 1: Add an env selector**

Add a static env flag near the existing `gdn_tc` selector:

```cpp
static const bool gdn_m5_qs_early = []{
    const char * e = getenv("GDN_M5_QS_EARLY");
    return e && atoi(e) != 0;
}();
```

Route it only for `S_v == 128 && n_tokens >= gdn_chunk_min && gdn_tc >= 4`.

- [ ] **Step 2: Add a template boolean for the candidate**

Extend the chunked launch templates with a defaulted boolean, keeping existing
call sites source-compatible:

```cpp
template <int S_v, int C, int TC = 0, bool QS_EARLY = false>
__global__ void gated_delta_net_chunked_cuda(...)
```

and:

```cpp
template <int S_v, int C, int TC = 0, bool QS_EARLY = false>
static void launch_gdn_chunked(...)
```

Use the boolean only inside the M3/M5 code path. Existing launches must remain
`launch_gdn_chunked<128, 16, TC_>(...)`.

- [ ] **Step 3: Move QS deposition earlier for candidate only**

In `gated_delta_net_chunked_cuda`, after the KS/RHS section and before the
`solve A U = RHS` section, add a candidate-only QS pass:

```cpp
if constexpr (QS_EARLY && TC >= 2) {
    const int w    = threadIdx.x >> 5;
    const int lane = threadIdx.x & 31;
    const int lg   = lane >> 2;
    const int lt   = lane & 3;
    constexpr int NWARP = S_v / 32;
    constexpr int NT    = dv / 8;
    constexpr int NTPW  = (NT + NWARP - 1) / NWARP;
    #pragma unroll
    for (int mt = 0; mt < (C + 15) / 16; mt++) {
        const int rowbase = mt * 16;
        #pragma unroll
        for (int nn = 0; nn < NTPW; nn++) {
            const int nt = w * NTPW + nn;
            if (nt >= NT) break;
            const int colbase = nt * 8;
            float cc[4];
            gdn_gram_tile_mma_3x<dk>(cc, Qc, Sd, rowbase, colbase, lg, lt);
            const int tt[4] = {rowbase + lg, rowbase + lg, rowbase + lg + 8, rowbase + lg + 8};
            const int jj[4] = {colbase + 2*lt, colbase + 2*lt + 1, colbase + 2*lt, colbase + 2*lt + 1};
            #pragma unroll
            for (int l = 0; l < 4; l++) {
                const int t = tt[l], jc = jj[l];
                if (t < Cc && jc < dv) {
                    attn_base[(c0 + t) * S_v * H + jc] = gam[t] * cc[l];
                }
            }
        }
    }
    __syncthreads();
}
```

Then change the existing QS deposition block to:

```cpp
if constexpr (TC >= 2 && !QS_EARLY) {
    ...
}
```

This is intentionally conservative. It should not change math order for the
deposited QS values, only their scheduling relative to the solve/P build.

- [ ] **Step 4: Add a candidate launch arm**

In `launch_gated_delta_net`, when `gdn_m5_qs_early && gdn_tc >= 4`, call:

```cpp
launch_gdn_chunked<128, 16, 4, true>(...);
return;
```

Default M5 must continue to call `launch_gdn_chunked<128, 16, 4>(...)`.

- [ ] **Step 5: Mirror to DGX and build**

Run:

```bash
rsync -a /home/mudler/_git/llama.cpp/ggml/src/ggml-cuda/gated_delta_net.cu \
  dgx.casa:/home/mudler/llama-phase6-source/ggml/src/ggml-cuda/gated_delta_net.cu
ssh dgx.casa 'cd /home/mudler/llama-phase6-source/build-cuda && cmake --build . --target test-backend-ops llama-completion llama-batched-bench -j 8'
```

Expected: build exits 0.

## Task 3: Correctness Gates

**Files:**
- Artifact: `/home/mudler/bench/phase11_gdn_m5_state_boundary/gates/`

- [ ] **Step 1: Run focused op gates**

Run:

```bash
ssh dgx.casa 'cd /home/mudler/llama-phase6-source/build-cuda/bin
ART=$HOME/bench/phase11_gdn_m5_state_boundary/gates
./test-backend-ops test -b CUDA0 -o GATED_DELTA_NET -j 1 > "$ART/gated_delta_net_default.txt" 2>&1
GDN_M5_QS_EARLY=1 GDN_TC=5 GDN_CHUNK_MIN=2 ./test-backend-ops test -b CUDA0 -o GATED_DELTA_NET -j 1 > "$ART/gated_delta_net_qs_early.txt" 2>&1'
```

Expected: both logs show CUDA0 `OK` for all `GATED_DELTA_NET` cases.

- [ ] **Step 2: Run canonical md5 gates**

Run:

```bash
ssh dgx.casa 'cd /home/mudler/llama-phase6-source/build-cuda/bin
ART=$HOME/bench/phase11_gdn_m5_state_boundary/gates
LBASE="LLAMA_KV_PAGED=1 LLAMA_MOE_FORCE_GRAPHS=1 GDN_TC=5 GDN_CHUNK_MIN=64 GGML_NO_BACKTRACE=1"
LCAND="LLAMA_KV_PAGED=1 LLAMA_MOE_FORCE_GRAPHS=1 GDN_TC=5 GDN_CHUNK_MIN=2 GDN_M5_QS_EARLY=1 GGML_NO_BACKTRACE=1"
env $LBASE ./llama-completion -m /home/mudler/bench/q36-35b-a3b-nvfp4.gguf -ngl 99 -fa on -c 4096 --temp 0 --seed 1 -n 48 -p "The capital of France is" </dev/null > "$ART/gate_moe_default.txt" 2> "$ART/gate_moe_default.err"
md5sum "$ART/gate_moe_default.txt" | tee "$ART/gate_moe_default.md5"
env $LBASE ./llama-completion -m /home/mudler/bench/q36-27b-nvfp4.gguf -ngl 99 -fa on -c 4096 --temp 0 --seed 1 -n 48 -p "The capital of France is" </dev/null > "$ART/gate_dense_default.txt" 2> "$ART/gate_dense_default.err"
md5sum "$ART/gate_dense_default.txt" | tee "$ART/gate_dense_default.md5"
env $LCAND ./llama-completion -m /home/mudler/bench/q36-35b-a3b-nvfp4.gguf -ngl 99 -fa on -c 4096 --temp 0 --seed 1 -n 48 -p "The capital of France is" </dev/null > "$ART/gate_moe_qs_early.txt" 2> "$ART/gate_moe_qs_early.err"
md5sum "$ART/gate_moe_qs_early.txt" | tee "$ART/gate_moe_qs_early.md5"
env $LCAND ./llama-completion -m /home/mudler/bench/q36-27b-nvfp4.gguf -ngl 99 -fa on -c 4096 --temp 0 --seed 1 -n 48 -p "The capital of France is" </dev/null > "$ART/gate_dense_qs_early.txt" 2> "$ART/gate_dense_qs_early.err"
md5sum "$ART/gate_dense_qs_early.txt" | tee "$ART/gate_dense_qs_early.md5"'
```

Expected:

```text
MoE   8cb0ce23777bf55f92f63d0292c756b0
Dense 5951a5b4d624ce891e22ab5fca9bc439
```

- [ ] **Step 3: Stop if md5 changes**

If either candidate md5 differs, do not benchmark yet. Run the KL gate from
`backend/cpp/llama-cpp-localai-paged/docs/PAGED_BITEXACT_NOTE.md` and accept
only if KL is benign and the transcript is sane.

## Task 4: Performance A/B

**Files:**
- Artifact: `/home/mudler/bench/phase11_gdn_m5_state_boundary/ab/`

- [ ] **Step 1: Run same-session MoE and dense A/B**

Run:

```bash
ssh dgx.casa 'cd /home/mudler/llama-phase6-source/build-cuda/bin
ART=$HOME/bench/phase11_gdn_m5_state_boundary/ab
mkdir -p "$ART"
LBASE="LLAMA_KV_PAGED=1 LLAMA_MOE_FORCE_GRAPHS=1 GDN_TC=5 GDN_CHUNK_MIN=64 GGML_NO_BACKTRACE=1"
LCAND="LLAMA_KV_PAGED=1 LLAMA_MOE_FORCE_GRAPHS=1 GDN_TC=5 GDN_CHUNK_MIN=64 GDN_M5_QS_EARLY=1 GGML_NO_BACKTRACE=1"
env $LBASE ./llama-batched-bench -m /home/mudler/bench/q36-35b-a3b-nvfp4.gguf -c 131072 -b 2048 -ub 512 -ngl 99 -fa on -npp 512,2048 -ntg 4 -npl 32 > "$ART/moe_base.txt" 2>&1
env $LCAND ./llama-batched-bench -m /home/mudler/bench/q36-35b-a3b-nvfp4.gguf -c 131072 -b 2048 -ub 512 -ngl 99 -fa on -npp 512,2048 -ntg 4 -npl 32 > "$ART/moe_qs_early.txt" 2>&1
env $LBASE ./llama-batched-bench -m /home/mudler/bench/q36-27b-nvfp4.gguf -c 131072 -b 2048 -ub 512 -ngl 99 -fa on -npp 512,2048 -ntg 4 -npl 32 > "$ART/dense_base.txt" 2>&1
env $LCAND ./llama-batched-bench -m /home/mudler/bench/q36-27b-nvfp4.gguf -c 131072 -b 2048 -ub 512 -ngl 99 -fa on -npp 512,2048 -ntg 4 -npl 32 > "$ART/dense_qs_early.txt" 2>&1'
```

Expected: candidate improves S_PP for at least the target MoE prefill cases and
does not regress dense outside noise.

- [ ] **Step 2: Decide accept/reject**

Accept only if:

- op gates pass,
- md5 is canonical or KL-benign,
- MoE S_PP improves,
- dense S_PP does not regress,
- decode routing remains untouched by `GDN_CHUNK_MIN > 1`.

Reject if the candidate is flat/slower. Save:

```bash
git -C /home/mudler/_git/llama.cpp diff -- ggml/src/ggml-cuda/gated_delta_net.cu \
  > /home/mudler/bench/phase11_gdn_m5_state_boundary/rejected/qs_early_rejected.diff
```

Then restore fork and DGX mirror.

## Task 5: Mirror Accepted Patch or Record Rejection

**Files:**
- Create if accepted: `backend/cpp/llama-cpp-localai-paged/patches/paged/0055-...patch`
- Modify: `backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md`
- Modify: `backend/cpp/llama-cpp-localai-paged/docs/VLLM_PARITY_LEVER_MAP.md`
- Modify: `backend/cpp/llama-cpp-localai-paged/docs/PARITY_HANDOFF.md`
- Modify: `backend/cpp/llama-cpp-localai-paged/docs/VLLM_PARITY_FINAL.md`

- [ ] **Step 1: If accepted, commit fork patch**

Commit in `/home/mudler/_git/llama.cpp` only after gates pass:

```bash
git add ggml/src/ggml-cuda/gated_delta_net.cu
git commit -m "feat(cuda): add gated delta net M5 QS-early path"
```

- [ ] **Step 2: Generate LocalAI patch**

Run:

```bash
git -C /home/mudler/_git/llama.cpp format-patch -1 HEAD \
  --stdout > backend/cpp/llama-cpp-localai-paged/patches/paged/0055-feat-cuda-add-gated-delta-net-M5-QS-early-path.patch
```

Do not hand-edit the generated patch.

- [ ] **Step 3: Update docs and commit LocalAI**

Record artifacts, md5/KL results, A/B numbers, and the decision. Commit with:

```bash
git add backend/cpp/llama-cpp-localai-paged/patches/paged/0055-feat-cuda-add-gated-delta-net-M5-QS-early-path.patch \
  backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md \
  backend/cpp/llama-cpp-localai-paged/docs/VLLM_PARITY_LEVER_MAP.md \
  backend/cpp/llama-cpp-localai-paged/docs/PARITY_HANDOFF.md \
  backend/cpp/llama-cpp-localai-paged/docs/VLLM_PARITY_FINAL.md
git commit -m "feat(paged): add GDN M5 QS-early path" \
  -m "Assisted-by: Codex:gpt-5"
```

If rejected, commit docs only:

```bash
git add backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md \
  backend/cpp/llama-cpp-localai-paged/docs/VLLM_PARITY_LEVER_MAP.md \
  backend/cpp/llama-cpp-localai-paged/docs/PARITY_HANDOFF.md \
  backend/cpp/llama-cpp-localai-paged/docs/VLLM_PARITY_FINAL.md \
  docs/superpowers/plans/2026-07-01-gdn-m5-state-boundary-phase11.md
git commit -m "docs(paged): record GDN M5 QS-early result" \
  -m "Assisted-by: Codex:gpt-5"
```
