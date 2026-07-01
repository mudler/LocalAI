# MTP Draft Smoke Phase 9 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Prove the current Qwen3.6 MoE GGUF can exercise llama.cpp `draft-mtp` without breaking normal inference, then keep only the smallest fix needed for the smoke path.

**Architecture:** Phase 9 is an opt-in speculative-decoding gate, not a default serving feature. The production patch only disables backend draft sampling for MTP because the current backend sampler rejects verification batches with multiple output rows per sequence; target verification and normal greedy inference remain unchanged.

**Tech Stack:** llama.cpp common speculative runtime, Qwen3.6 MoE NVFP4 GGUF, DGX GB10 CUDA build, canonical LocalAI md5 gates.

---

## Guardrails

- Keep the patch incremental and additive in the llama.cpp fork.
- Do not enable MTP by default in LocalAI or llama-server.
- Do not enable backend draft sampling for MTP until it supports multi-output verification batches.
- Treat canonical md5 gates as mandatory after any runtime change:
  - MoE: `8cb0ce23777bf55f92f63d0292c756b0`.
  - Dense: `5951a5b4d624ce891e22ab5fca9bc439`.
- Record every DGX artifact under `/home/mudler/bench/phase9_mtp_smoke/`.

## Task 1: Verify MTP Assets

**Files:**
- Read-only: `/home/mudler/bench/q36-35b-a3b-nvfp4.gguf`

- [x] **Step 1: Check DGX is free**

Run:

```bash
ssh dgx.casa 'set -e
echo docker=$(docker ps -q | wc -l)
echo local_ai_worker=$(docker ps --format "{{.Names}}" | grep -c local-ai-worker || true)
echo compute=$(nvidia-smi --query-compute-apps=pid --format=csv,noheader | sed "/^$/d" | wc -l)
if [ -f ~/gpu_bench_lock/owner ]; then cat ~/gpu_bench_lock/owner; else echo FREE-no-lock-file; fi'
```

Result:

```text
docker=0
local_ai_worker=0
compute=0
FREE released-by-codex-phase6-mmq-grid 1782860601
```

- [x] **Step 2: Confirm nextn tensors exist**

Run:

```bash
ssh dgx.casa 'strings /home/mudler/bench/q36-35b-a3b-nvfp4.gguf | grep -i -E "nextn|mtp" | head -n 80 || true'
```

Result includes:

```text
qwen35moe.nextn_predict_layers
blk.40.nextn.eh_proj.weight
blk.40.nextn.shared_head_norm.weight
blk.40.nextn.enorm.weight
blk.40.nextn.hnorm.weight
```

## Task 2: Reproduce the Runtime Failure

**Files:**
- Read-only: `/home/mudler/llama-phase6-source/build-cuda/bin/llama-speculative-simple`
- Artifact: `/home/mudler/bench/phase9_mtp_smoke/mtp_smoke.out`
- Artifact: `/home/mudler/bench/phase9_mtp_smoke/mtp_smoke.err`

- [x] **Step 1: Build the narrow speculative target**

Run:

```bash
ssh dgx.casa 'cd /home/mudler/llama-phase6-source/build-cuda && cmake --build . --target llama-speculative-simple -j 8'
```

Result: `Built target llama-speculative-simple`.

- [x] **Step 2: Run default `draft-mtp` smoke before patch**

Run:

```bash
ssh dgx.casa 'cd /home/mudler/llama-phase6-source
ART=$HOME/bench/phase9_mtp_smoke
mkdir -p "$ART"
timeout 180s env LLAMA_KV_PAGED=1 LLAMA_MOE_FORCE_GRAPHS=1 GDN_CHUNK_MIN=64 GDN_TC=5 GGML_NO_BACKTRACE=1 \
  ./build-cuda/bin/llama-speculative-simple \
  -m /home/mudler/bench/q36-35b-a3b-nvfp4.gguf \
  --spec-type draft-mtp --spec-draft-model /home/mudler/bench/q36-35b-a3b-nvfp4.gguf --spec-draft-ngl 99 --spec-draft-n-max 1 \
  -ngl 99 -fa on -c 4096 -n 8 --temp 0 --seed 1 -p "The capital of France is" \
  > "$ART/mtp_smoke.out" 2> "$ART/mtp_smoke.err"'
```

Result: process exits `0` but stderr contains repeated backend sampler failures:

```text
decode: backend sampling requires at most one output token per sequence (seq_id 0 had 2)
```

- [x] **Step 3: Prove the expected behavior with backend sampling disabled**

Run the same command with `--no-spec-draft-backend-sampling`.

Artifact:

- `/home/mudler/bench/phase9_mtp_smoke/mtp_smoke_no_backend_sampling.out`
- `/home/mudler/bench/phase9_mtp_smoke/mtp_smoke_no_backend_sampling.err`

Result:

```text
n_drafted = 5
n_accept  = 4
accept    = 80.000%
```

Output tail:

```text
The capital of France is Paris, a city renowned for its rich history
```

## Task 3: Disable Backend Sampling for MTP

**Files:**
- Modify: `/home/mudler/_git/llama.cpp/common/speculative.cpp`
- Mirror: `/home/mudler/llama-phase6-source/common/speculative.cpp`

- [x] **Step 1: Add the guard in the fork**

Patch:

```cpp
if (this->params.backend_sampling) {
    LOG_WRN("%s: backend draft sampling is disabled for MTP; verification batches can request multiple output rows per sequence\n",
            __func__);
    this->params.backend_sampling = false;
}
```

- [x] **Step 2: Mirror to DGX and rebuild**

Run:

```bash
rsync -a /home/mudler/_git/llama.cpp/common/speculative.cpp dgx.casa:/home/mudler/llama-phase6-source/common/speculative.cpp
ssh dgx.casa 'cd /home/mudler/llama-phase6-source/build-cuda && cmake --build . --target llama-speculative-simple llama-completion -j 8'
```

Result: both targets built.

- [x] **Step 3: Re-run default `draft-mtp` smoke**

Artifact:

- `/home/mudler/bench/phase9_mtp_smoke/mtp_smoke_default_after_patch.out`
- `/home/mudler/bench/phase9_mtp_smoke/mtp_smoke_default_after_patch.err`

Result:

```text
rc=0
MTP_BACKEND_DISABLED_WARN
n_drafted = 5
n_accept  = 4
accept    = 80.000%
```

The backend sampler error is absent after the guard.

## Task 4: Normal Inference Gates

**Files:**
- Artifact: `/home/mudler/bench/phase9_mtp_smoke/gate_moe_after_patch.txt`
- Artifact: `/home/mudler/bench/phase9_mtp_smoke/gate_dense_after_patch.txt`

- [x] **Step 1: Run canonical MoE md5**

Run:

```bash
ssh dgx.casa 'cd /home/mudler/llama-phase6-source/build-cuda/bin
ART=$HOME/bench/phase9_mtp_smoke
L="LLAMA_KV_PAGED=1 LLAMA_MOE_FORCE_GRAPHS=1 GDN_CHUNK_MIN=64 GDN_TC=5 GGML_NO_BACKTRACE=1"
env $L ./llama-completion -m /home/mudler/bench/q36-35b-a3b-nvfp4.gguf -ngl 99 -fa on -c 4096 --temp 0 --seed 1 -n 48 -p "The capital of France is" </dev/null > "$ART/gate_moe_after_patch.txt"
md5sum "$ART/gate_moe_after_patch.txt"'
```

Result:

```text
8cb0ce23777bf55f92f63d0292c756b0
```

- [x] **Step 2: Run canonical dense md5**

Run the same command with `/home/mudler/bench/q36-27b-nvfp4.gguf`.

Result:

```text
5951a5b4d624ce891e22ab5fca9bc439
```

## Task 5: Mirror Patch Stack

**Files:**
- Create: `backend/cpp/llama-cpp-localai-paged/patches/paged/0054-fix-speculative-disable-backend-sampling-for-MTP-drafts.patch`

- [x] **Step 1: Commit in llama.cpp fork**

Fork commit:

```text
3eba64aff fix(speculative): disable backend sampling for MTP drafts
```

DGX mirror commit:

```text
3a714c6f9 fix(speculative): disable backend sampling for MTP drafts
```

- [x] **Step 2: Generate LocalAI patch**

Run:

```bash
git -C /home/mudler/_git/llama.cpp format-patch -1 --stdout > \
  /home/mudler/_git/LocalAI/.claude/worktrees/feat+paged-attention/backend/cpp/llama-cpp-localai-paged/patches/paged/0054-fix-speculative-disable-backend-sampling-for-MTP-drafts.patch
```

## Follow-up Scope

- MTP remains opt-in and smoke-gated only; do not promote it to default serving.
- A production MTP serving phase must add a server/API gate and a hybrid state rollback test before benchmark claims.
- The next GDN phase should be separate: C=32, `dv_tile=64`, M5-style chunked prefill slab prototype, compared against current M5 at `npp=512` and `npp=2048`.
