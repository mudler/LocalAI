# Prefill Bucket Attribution Phase63 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Re-profile current llama.cpp and vLLM MoE prefill on GB10 with inference gates before/after, then fund only a localized paged FlashAttention mask/block-table cleanup if the profile proves the bucket is material.

**Architecture:** Phase63 is measurement-first. It brackets all DGX work with canonical md5 and backend-op gates, captures same-shape Nsight Systems prefill profiles for llama.cpp and vLLM, reduces kernel rows into named buckets, and records a go/no-go decision before touching llama.cpp source. If the FA/mask bucket is too small, the phase closes as a documented rejection.

**Tech Stack:** LocalAI paged docs, llama.cpp CUDA backend, Nsight Systems, DGX `dgx.casa`, `/home/mudler/bench/bucket.py`, `llama-batched-bench`, vLLM offline profiling harness.

---

## Guardrails

- Do not edit llama.cpp source until Task 4 has a positive go decision.
- Do not regenerate the LocalAI patch series in this phase.
- Do not accept any md5 drift as benign without a separate KL decision.
- Canonical gates:
  - MoE md5: `8cb0ce23777bf55f92f63d0292c756b0`
  - dense md5: `5951a5b4d624ce891e22ab5fca9bc439`
  - `MUL_MAT`: `1146/1146`
  - `MUL_MAT_ID`: `806/806`
- DGX preflight must show `docker=0`, `local_ai_worker=0`, `compute=0`, and a free lock before starting a run.

## Files

- Create: `docs/superpowers/plans/2026-07-01-prefill-bucket-attribution-phase63.md`
- Modify: `backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md`
- Modify: `backend/cpp/llama-cpp-localai-paged/docs/PARITY_HANDOFF.md`
- Modify: `backend/cpp/llama-cpp-localai-paged/docs/VLLM_PARITY_LEVER_MAP.md`
- Read-only unless Task 4 is positive: `/home/mudler/_git/llama.cpp/src/paged-attn.cpp`
- Read-only unless Task 4 is positive: `/home/mudler/_git/llama.cpp/ggml/src/ggml-cuda/fattn-vec.cuh`
- Read-only unless Task 4 is positive: `/home/mudler/_git/llama.cpp/ggml/src/ggml-cuda/fattn-tile.cuh`
- Read-only unless Task 4 is positive: `/home/mudler/_git/llama.cpp/ggml/src/ggml-cuda/fattn.cu`
- Test if Task 4 is positive: `/home/mudler/_git/llama.cpp/tests/test-backend-ops.cpp`

---

### Task 1: Acquire DGX and Run Pre-Gates

- [x] **Step 1: Verify DGX is idle and acquire the phase lock**

Run:

```bash
ssh dgx.casa 'set -euo pipefail
docker_count=$(docker ps --format "{{.Names}}" | wc -l)
worker_count=$(pgrep -af "[l]ocal-ai-worker" | wc -l)
compute_count=$(nvidia-smi --query-compute-apps=pid --format=csv,noheader | sed "/^$/d" | wc -l)
lock_state=FREE
if [ -f /tmp/localai-gb10.lock ]; then lock_state=$(cat /tmp/localai-gb10.lock); fi
printf "docker=%s local_ai_worker=%s compute=%s lock=%s\n" "$docker_count" "$worker_count" "$compute_count" "$lock_state"
test "$docker_count" = 0
test "$worker_count" = 0
test "$compute_count" = 0
case "$lock_state" in FREE*|FREE-no-lock) : ;; *) exit 3 ;; esac
printf "codex-phase63-prefill-bucket %s\n" "$(date +%s)" > /tmp/localai-gb10.lock'
```

Expected: one line containing `docker=0 local_ai_worker=0 compute=0 lock=FREE...`, exit code `0`, and `/tmp/localai-gb10.lock` owned by `codex-phase63-prefill-bucket`.

Result: initial preflight showed `docker=0`, `compute=0`, and no real
`local-ai-worker` process. The first direct gate retry exposed a shell issue:
with `set -euo pipefail`, an empty `pgrep` pipeline exits before printing, so the
execution command uses `(pgrep -af '[l]ocal-ai-worker' || true) | wc -l`.

- [x] **Step 2: Run canonical pre-gate**

Run:

```bash
ssh dgx.casa 'set -euo pipefail
cd /home/mudler/_git/LocalAI/.claude/worktrees/feat+paged-attention/backend/cpp/llama-cpp-localai-paged
ART=/home/mudler/bench/phase63_prefill_bucket/$(date +%Y%m%d_%H%M%S)
mkdir -p "$ART"
echo "$ART" > /tmp/phase63_artifact_dir
./scripts/paged-inference-gates.sh /home/mudler/llama-phase6-source/build-cuda/bin "$ART/pre_gate" | tee "$ART/pre_gate.log"'
```

Expected:

```text
moe md5 OK: 8cb0ce23777bf55f92f63d0292c756b0
dense md5 OK: 5951a5b4d624ce891e22ab5fca9bc439
  1146/1146 tests passed
  806/806 tests passed
paged inference gates OK
```

Result:

```text
docker=0 local_ai_worker=0 compute=0 lock=FREE-no-lock
pre	moe_md5	8cb0ce23777bf55f92f63d0292c756b0	8cb0ce23777bf55f92f63d0292c756b0	ok
pre	dense_md5	5951a5b4d624ce891e22ab5fca9bc439	5951a5b4d624ce891e22ab5fca9bc439	ok
pre	MUL_MAT	1146/1146	1146/1146	ok
pre	MUL_MAT_ID	806/806	806/806	ok
paged inference gates OK
```

Artifact: `/home/mudler/bench/phase63_prefill_bucket/20260701_140127`.

---

### Task 2: Capture Current llama.cpp Prefill Profiles

- [x] **Step 1: Run `npp=512` and `npp=2048` llama.cpp prefill profiles**

Run:

```bash
ssh dgx.casa 'set -euo pipefail
ART=$(cat /tmp/phase63_artifact_dir)
BIN=/home/mudler/llama-phase6-source/build-cuda/bin/llama-batched-bench
MODEL=/home/mudler/bench/q36-35b-a3b-nvfp4.gguf
for npp in 512 2048; do
  REP="$ART/llama_moe_prefill_npp${npp}"
  rm -f "$REP.nsys-rep" "$REP.sqlite" "$REP.log" "$REP.buckets.txt"
  env LLAMA_KV_PAGED=1 LLAMA_MOE_FORCE_GRAPHS=1 GGML_NO_BACKTRACE=1 GGML_CUDA_DISABLE_GRAPHS=1 \
    nsys profile --trace=cuda --sample=none --cpuctxsw=none --force-overwrite true -o "$REP" \
    "$BIN" -m "$MODEL" -c 131072 -b 2048 -ub 512 -ngl 99 -fa on \
      -npp "$npp" -ntg 4 -npl 32 > "$REP.log" 2>&1
  nsys stats --report cuda_gpu_kern_sum --format csv --force-export true -o "$REP.kern" "$REP.nsys-rep" >/dev/null
  python3 /home/mudler/bench/bucket.py "$REP.nsys-rep" "phase63_llama_npp${npp}" > "$REP.buckets.txt"
  grep -E "main:|pp|tg|llama_print_timings|error|failed|CUDA" "$REP.log" | tail -40 > "$REP.summary.txt" || true
done'
```

Expected:

- `llama_moe_prefill_npp512.nsys-rep`, `.kern_cuda_gpu_kern_sum.csv`, `.buckets.txt`, `.log`.
- `llama_moe_prefill_npp2048.nsys-rep`, `.kern_cuda_gpu_kern_sum.csv`, `.buckets.txt`, `.log`.
- Logs contain no `error`, `failed`, or CUDA runtime failure.

Result: both profiles completed under
`/home/mudler/bench/phase63_prefill_bucket/20260701_140127`.

- [x] **Step 2: Extract llama bucket rows for the decision table**

Run:

```bash
ssh dgx.casa 'set -euo pipefail
ART=$(cat /tmp/phase63_artifact_dir)
for f in "$ART"/llama_moe_prefill_npp*.buckets.txt; do
  echo "==== $f ===="
  sed -n "/--- MACRO buckets ---/,/--- FINE buckets ---/p" "$f"
  sed -n "/--- FINE buckets ---/,/--- top UNCLASSIFIED ---/p" "$f" | \
    egrep "mmq_nvfp4|act_quant|gdn_core|fa|argsort|mm_ids|gather_mmq|get_rows|copy_layout|concat_layout|convert_dtype" || true
done | tee "$ART/llama_bucket_extract.txt"'
```

Expected: extract includes rows for `MoE/FFN-GEMM`, `GDN`, `act-quant`, and `FA`; FA may be small.

Result:

| npp | MoE/FFN-GEMM | GDN | bf16-proj | layout-copy | act-quant | MoE-dispatch | gather | FA |
|-----|--------------|-----|-----------|-------------|-----------|--------------|--------|----|
| 512 | `40.48%` | `18.00%` | `10.19%` | `7.82%` | `4.47%` | `1.94%` | `1.26%` | `0.71%` |
| 2048 | `41.06%` | `16.15%` | `9.97%` | `7.96%` | `4.61%` | `2.12%` | `1.36%` | `1.18%` |

The FA bucket is below the Phase63 reject threshold before any source work.

---

### Task 3: Capture vLLM Same-Shape Prefill Profiles

- [x] **Step 1: Run vLLM `PT=512` and `PT=2048` prefill profiles**

Run:

```bash
ssh dgx.casa 'set -euo pipefail
ART=$(cat /tmp/phase63_artifact_dir)
export PATH=$HOME/vllm-bench/bin:$PATH
export HF_HUB_OFFLINE=1
for pt in 512 2048; do
  REP="$ART/vllm_moe_prefill_pt${pt}"
  rm -f "$REP.nsys-rep" "$REP.sqlite" "$REP.log" "$REP.buckets.txt"
  env NSEQ=32 PT="$pt" GEN=1 NREP=3 \
    nsys profile --capture-range=cudaProfilerApi --capture-range-end=stop \
      --trace=cuda --sample=none --cpuctxsw=none --force-overwrite true -o "$REP" \
      $HOME/vllm-bench/bin/python /home/mudler/bench/vllm_prefill_prof.py > "$REP.log" 2>&1
  nsys stats --report cuda_gpu_kern_sum --format csv --force-export true -o "$REP.kern" "$REP.nsys-rep" >/dev/null
  python3 /home/mudler/bench/bucket.py "$REP.nsys-rep" "phase63_vllm_pt${pt}" > "$REP.buckets.txt"
  grep -E "TIMING|PROFILED|Error|Traceback|RuntimeError|CUDA" "$REP.log" | tail -40 > "$REP.summary.txt" || true
done'
```

Expected:

- `vllm_moe_prefill_pt512.nsys-rep`, `.kern_cuda_gpu_kern_sum.csv`, `.buckets.txt`, `.log`.
- `vllm_moe_prefill_pt2048.nsys-rep`, `.kern_cuda_gpu_kern_sum.csv`, `.buckets.txt`, `.log`.
- Logs contain `TIMING ... S_PP=...`, `PROFILED PREFILL START`, and `PROFILED END`.

Result: both vLLM profiles completed under
`/home/mudler/bench/phase63_prefill_bucket/20260701_140127`.
Timing:

| PT | S_PP |
|----|------|
| 512 | `5315.6 tok/s` |
| 2048 | `5384.4 tok/s` |

- [x] **Step 2: Extract vLLM bucket rows for the decision table**

Run:

```bash
ssh dgx.casa 'set -euo pipefail
ART=$(cat /tmp/phase63_artifact_dir)
for f in "$ART"/vllm_moe_prefill_pt*.buckets.txt; do
  echo "==== $f ===="
  sed -n "/--- MACRO buckets ---/,/--- FINE buckets ---/p" "$f"
  sed -n "/--- FINE buckets ---/,/--- top UNCLASSIFIED ---/p" "$f" | \
    egrep "vllm_fa|fla_gdn|vllm_dispatch|vllm_fp4_gemm|torch_ew|rmsnorm|triton|scaled|quant" || true
done | tee "$ART/vllm_bucket_extract.txt"'
```

Expected: extract includes vLLM rows for `MoE/FFN-GEMM`, `GDN`, `FA`, and dispatch/glue.

Result:

| PT | ew(misc) | GDN | FA | bf16-proj | MoE-dispatch | top `other` rows |
|----|----------|-----|----|-----------|--------------|------------------|
| 512 | `32.97%` | `18.34%` | `0.73%` | `3.41%` | `1.37%` | Marlin MoE `1940.99ms`, FP8 projection `565.74ms` |
| 2048 | `33.48%` | `18.00%` | `1.75%` | `1.06%` | `0.49%` | Marlin MoE `7745.84ms`, FP8 projection `3047.75ms` |

---

### Task 4: Decide Whether a Source Patch Is Funded

- [x] **Step 1: Apply the Phase63 decision gate**

Use these rules:

- Continue to a source patch only if llama.cpp FA or paged-mask-related work is at least `8%` of prefill GPU kernel time at `npp>=2048`, or it accounts for at least `15 us/tok` versus vLLM at the same shape.
- Reject source work if FA is below `5%` of llama.cpp prefill kernel time at `npp=2048`.
- Reject source work if the profile again points primarily at already-rejected GDN, W4A16, MTP, small-M MMQ, or gate-projection buckets.
- If continuing, keep the source target limited to physical mask/block-table indexing for paged FlashAttention and an explicit `FLASH_ATTN_EXT` block-table backend-op test.

Expected: write a short decision paragraph into `GB10_PARITY_PHASE0_RESULTS.md`.

Result: reject source work for Phase63. llama.cpp FA was `0.71%` at `npp=512`
and `1.18%` at `npp=2048`, below the `<5%` source-work reject threshold. At
`npp=2048`, llama FA was `320.66ms` over `65536` prompt tokens, about
`4.9 us/tok`; vLLM FA was `618.02ms` over `196608` prompt tokens, about
`3.1 us/tok`. The approximate FA delta is only `1.7 us/tok`, below the
`15 us/tok` source-funding gate.

- [x] **Step 2: If the source gate is negative, skip directly to Task 6**

Expected: no source files modified.

Result: no llama.cpp source files were modified.

---

### Task 5: Optional Source Patch Only If Task 4 Is Positive

Skipped: Task 4 rejected source work.

- [ ] **Step 1: Add the missing block-table FlashAttention backend-op case first**

Modify `/home/mudler/_git/llama.cpp/tests/test-backend-ops.cpp` so `FLASH_ATTN_EXT` has a paged/block-table mask case that fails before any mask-indexing implementation.

Run:

```bash
ssh dgx.casa 'set -euo pipefail
cd /home/mudler/llama-phase6-source
cmake --build build-cuda --target test-backend-ops -j $(nproc)
./build-cuda/bin/test-backend-ops test -b CUDA0 -o FLASH_ATTN_EXT -j 1'
```

Expected before implementation: the new block-table case fails or is skipped with an explicit unsupported path that proves the gap.

- [ ] **Step 2: Implement physical mask indexing behind the existing block-table dispatch**

Modify only the narrow paged-FA files:

- `/home/mudler/_git/llama.cpp/src/paged-attn.cpp`
- `/home/mudler/_git/llama.cpp/ggml/src/ggml-cuda/fattn-vec.cuh`
- `/home/mudler/_git/llama.cpp/ggml/src/ggml-cuda/fattn-tile.cuh`
- `/home/mudler/_git/llama.cpp/ggml/src/ggml-cuda/fattn.cu`

The implementation must remove mask compaction only when a block table is present and the CUDA kernel is using the physical-mask path. Non-paged attention must keep the existing mask layout.

- [ ] **Step 3: Run correctness and inference gates**

Run:

```bash
ssh dgx.casa 'set -euo pipefail
cd /home/mudler/llama-phase6-source
cmake --build build-cuda --target llama-completion llama-batched-bench test-backend-ops -j $(nproc)
ART=$(cat /tmp/phase63_artifact_dir)
./build-cuda/bin/test-backend-ops test -b CUDA0 -o FLASH_ATTN_EXT -j 1 | tee "$ART/flash_attn_ext_post.log"
cd /home/mudler/_git/LocalAI/.claude/worktrees/feat+paged-attention/backend/cpp/llama-cpp-localai-paged
./scripts/paged-inference-gates.sh /home/mudler/llama-phase6-source/build-cuda/bin "$ART/post_patch_gate" | tee "$ART/post_patch_gate.log"'
```

Expected: `FLASH_ATTN_EXT` passes, canonical md5s match, `MUL_MAT` is `1146/1146`, and `MUL_MAT_ID` is `806/806`.

- [ ] **Step 4: Run the A/B performance gate**

Run baseline and patched builds with:

```bash
env LLAMA_KV_PAGED=1 LLAMA_MOE_FORCE_GRAPHS=1 ./llama-batched-bench \
  -m /home/mudler/bench/q36-35b-a3b-nvfp4.gguf -c 131072 -b 2048 -ub 512 -ngl 99 -fa on \
  -npp 128 -ntg 128 -npl 128,256
```

Keep only if the patch improves decode `S_TG` by at least `1.0%` at `npl=128` or `npl=256`, or reduces graph-node-traced decode wall by at least `0.5 ms/step`, with no md5/op drift.

---

### Task 6: Post-Gate, Release DGX, and Record Result

- [x] **Step 1: Run canonical post-gate**

Run:

```bash
ssh dgx.casa 'set -euo pipefail
ART=$(cat /tmp/phase63_artifact_dir)
cd /home/mudler/_git/LocalAI/.claude/worktrees/feat+paged-attention/backend/cpp/llama-cpp-localai-paged
./scripts/paged-inference-gates.sh /home/mudler/llama-phase6-source/build-cuda/bin "$ART/post_gate" | tee "$ART/post_gate.log"'
```

Expected:

```text
moe md5 OK: 8cb0ce23777bf55f92f63d0292c756b0
dense md5 OK: 5951a5b4d624ce891e22ab5fca9bc439
  1146/1146 tests passed
  806/806 tests passed
paged inference gates OK
```

Result:

```text
post	moe_md5	8cb0ce23777bf55f92f63d0292c756b0	8cb0ce23777bf55f92f63d0292c756b0	ok
post	dense_md5	5951a5b4d624ce891e22ab5fca9bc439	5951a5b4d624ce891e22ab5fca9bc439	ok
post	MUL_MAT	1146/1146	1146/1146	ok
post	MUL_MAT_ID	806/806	806/806	ok
post paged inference gates OK
```

- [x] **Step 2: Release DGX lock**

Run:

```bash
ssh dgx.casa 'printf "FREE released-by-codex-phase63-prefill-bucket %s\n" "$(date +%s)" > /tmp/localai-gb10.lock'
```

Expected: `/tmp/localai-gb10.lock` starts with `FREE released-by-codex-phase63-prefill-bucket`.

Result: `/tmp/localai-gb10.lock` is
`FREE released-by-codex-phase63-prefill-bucket 1782908317`; Docker count `0`,
worker count `0`, and no compute-app rows.

- [x] **Step 3: Update LocalAI docs**

Modify:

- `backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md`
- `backend/cpp/llama-cpp-localai-paged/docs/PARITY_HANDOFF.md`
- `backend/cpp/llama-cpp-localai-paged/docs/VLLM_PARITY_LEVER_MAP.md`

Record:

- artifact directory,
- pre/post gate md5s and op counts,
- llama and vLLM bucket table,
- Task 4 decision,
- source patch commit if any, or explicit source-work rejection.

Result: completed in this commit.

- [x] **Step 4: Commit LocalAI tracking docs**

Run:

```bash
cd /home/mudler/_git/LocalAI/.claude/worktrees/feat+paged-attention
git add -f docs/superpowers/plans/2026-07-01-prefill-bucket-attribution-phase63.md
git add backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md \
        backend/cpp/llama-cpp-localai-paged/docs/PARITY_HANDOFF.md \
        backend/cpp/llama-cpp-localai-paged/docs/VLLM_PARITY_LEVER_MAP.md
git commit -m "docs(paged): record prefill bucket attribution phase" \
  -m "Assisted-by: Codex:gpt-5"
```

Expected: commit succeeds without bypassing hooks.

Result: committed as `6fc2cfb54 docs(paged): record prefill bucket attribution
phase`, then amended to mark this final checklist item complete.

---

## Self-Review

- Spec coverage: The plan directly covers the user's inferencing-safety request with pre/post md5 and op gates, uses DGX only after idle preflight, scopes Phase63 as measurement before source work, and limits any source follow-up to a localized FA/mask candidate.
- Placeholder scan: No `TBD`, `TODO`, or undefined test command remains.
- Type/path consistency: Artifact path, gate command, model paths, and binary paths are consistent across tasks.
