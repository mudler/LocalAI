# GB10 Parity Reopen Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reopen the GB10 vLLM-parity investigation with clean provenance, then execute gated W4A16, GDN, MoE fan-in, serving, and glue-fusion workstreams only when their entry criteria are met.

**Architecture:** The plan is phased. Phase 0 creates trustworthy baseline artifacts and command provenance; later phases are fork-first llama.cpp changes regenerated into the LocalAI patch stack. Every branch has a kill gate, and subagents are used only for independent file or artifact ownership.

**Tech Stack:** LocalAI docs and patch stack, `mudler/llama.cpp:localai-paged`, ggml CUDA kernels, vLLM 0.23.0 on DGX GB10, CUDA 13, Nsight Systems, LocalAI benchmark artifacts.

---

## File Structure

- Modify: `backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_REOPEN_SPEC.md`
  - Keep the high-level scope in sync when Phase 0 changes the evidence.
- Create: `backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md`
  - Record Phase 0 commands, preflight state, source SHAs, artifact paths, and baseline numbers.
- Modify later, fork-first: `/home/mudler/_git/llama.cpp/ggml/src/ggml-cuda/w4a16-gemm.cu`
  - W4A16 grouped MoE prefill kernel tuning.
- Modify later, fork-first: `/home/mudler/_git/llama.cpp/ggml/src/ggml-cuda/w4a16-gemm.cuh`
  - W4A16 API and tuning switches.
- Modify later, fork-first: `/home/mudler/_git/llama.cpp/ggml/src/ggml-cuda/ggml-cuda.cu`
  - `ggml_cuda_mul_mat_id` dispatch, MoE fan-in fusions, graph behavior.
- Modify later, fork-first: `/home/mudler/_git/llama.cpp/ggml/src/ggml-cuda/mmq.cu`
  - W4A16/FP4 prefill routing thresholds.
- Modify later, fork-first: `/home/mudler/_git/llama.cpp/ggml/src/ggml-cuda/gated_delta_net.cu`
  - GDN M5 follow-up variants.
- Modify later, fork-first: `/home/mudler/_git/llama.cpp/src/llama-graph.cpp`
  - MoE weighted fan-in graph shape if a fused op is pursued.
- Modify later: `backend/cpp/llama-cpp-localai-paged/patches/paged/*.patch`
  - Generated only from fork commits using `git format-patch`; never edited directly.

## Task 1: Phase 0 Preflight And Run Directory

**Files:**
- Create: `backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md`

- [x] **Step 1: Confirm the current worktree state**

Run:

```bash
git status --short --branch
git log --oneline --decorate --max-count=5
```

Expected:

```text
## worktree-feat+paged-attention...origin/worktree-feat+paged-attention [ahead 2]
?? .claude/
```

- [x] **Step 2: Run DGX preflight without starting workloads**

Run:

```bash
ssh dgx.casa 'set -e
echo "HOST=$(hostname)"
echo "--- docker ps ---"
docker ps --format "{{.ID}} {{.Names}} {{.Image}} {{.Status}}" || true
echo "--- compute apps ---"
nvidia-smi --query-compute-apps=pid,process_name,used_memory --format=csv,noheader || true
echo "--- gpu lock ---"
if [ -e ~/gpu_bench_lock/owner ]; then cat ~/gpu_bench_lock/owner; else echo NO_OWNER; fi
echo "--- source states ---"
git -C ~/llama-paged-fork status --short --branch 2>/dev/null || true
git -C ~/llama-paged-dev status --short --branch 2>/dev/null || true
'
```

Expected:

```text
docker ps has no running containers
compute apps has no rows
gpu lock is FREE or NO_OWNER
DGX source states are recorded, even if dirty
```

- [x] **Step 3: Create the Phase 0 artifact directory on DGX**

Run:

```bash
ssh dgx.casa 'set -e
mkdir -p ~/bench/reopen_phase0
date -u +%Y-%m-%dT%H:%M:%SZ > ~/bench/reopen_phase0/created_utc.txt
hostname > ~/bench/reopen_phase0/hostname.txt
docker ps --format "{{.ID}} {{.Names}} {{.Image}} {{.Status}}" > ~/bench/reopen_phase0/docker_ps.txt
nvidia-smi --query-compute-apps=pid,process_name,used_memory --format=csv,noheader > ~/bench/reopen_phase0/compute_apps.txt || true
if [ -e ~/gpu_bench_lock/owner ]; then cat ~/gpu_bench_lock/owner > ~/bench/reopen_phase0/gpu_lock_owner.txt; else echo NO_OWNER > ~/bench/reopen_phase0/gpu_lock_owner.txt; fi
'
```

Expected:

```text
~/bench/reopen_phase0 exists and contains created_utc.txt, hostname.txt, docker_ps.txt, compute_apps.txt, gpu_lock_owner.txt
```

- [x] **Step 4: Write the initial Phase 0 results document from captured values**

Run:

```bash
DGX_HOST=$(ssh dgx.casa 'cat ~/bench/reopen_phase0/hostname.txt')
DGX_DOCKER=$(ssh dgx.casa 'if [ -s ~/bench/reopen_phase0/docker_ps.txt ]; then tr "\n" "; " < ~/bench/reopen_phase0/docker_ps.txt; else echo "none"; fi')
DGX_COMPUTE=$(ssh dgx.casa 'if [ -s ~/bench/reopen_phase0/compute_apps.txt ]; then tr "\n" "; " < ~/bench/reopen_phase0/compute_apps.txt; else echo "none"; fi')
DGX_LOCK=$(ssh dgx.casa 'cat ~/bench/reopen_phase0/gpu_lock_owner.txt')
LOCALAI_SHA=$(git rev-parse HEAD)
LLAMA_SHA=$(git -C /home/mudler/_git/llama.cpp rev-parse HEAD)
cat > backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md <<EOF
# GB10 Parity Phase 0 Results

Status: in progress.

## Preflight

- DGX host: \`${DGX_HOST}\`
- Docker containers: \`${DGX_DOCKER}\`
- GPU compute apps: \`${DGX_COMPUTE}\`
- GPU lock owner: \`${DGX_LOCK}\`
- LocalAI worktree SHA: \`${LOCALAI_SHA}\`
- Local llama.cpp fork SHA: \`${LLAMA_SHA}\`
- DGX artifact directory: `~/bench/reopen_phase0`

## Baseline Runs

No baseline runs have been started yet.

## Open Items

- Capture clean source provenance.
- Reproduce paged prefill and decode baselines.
- Find or recreate vLLM graph-node-traced difference-method decode artifacts.
EOF
```

- [x] **Step 5: Commit Task 1**

Run:

```bash
git add backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md
git commit -m "docs(paged): start GB10 parity phase0 record" \
  -m "Create the Phase 0 results record for the parity reopen workflow, including preflight, provenance, and baseline sections." \
  -m "Assisted-by: Codex:gpt-5"
```

Expected:

```text
Commit succeeds with only GB10_PARITY_PHASE0_RESULTS.md staged.
```

## Task 2: Clean Source Provenance Runbook

**Files:**
- Modify: `backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md`

- [x] **Step 1: Record local source truth**

Run:

```bash
git -C /home/mudler/_git/llama.cpp status --short --branch
git -C /home/mudler/_git/llama.cpp rev-parse HEAD
git -C /home/mudler/_git/llama.cpp merge-base HEAD 0ed235ea2c17a19fc8238668653946721ed136fd
git -C /home/mudler/_git/llama.cpp log --oneline --decorate --max-count=5
```

Expected:

```text
branch localai-paged is clean
HEAD is 51168c5eee2e35348d9006f0b2fab3dc6e7c01cc
merge-base is 0ed235ea2c17a19fc8238668653946721ed136fd
```

- [x] **Step 2: Verify LocalAI patch mirror invariant**

Run:

```bash
rm -rf /tmp/localai-paged-apply-check
git clone --shared /home/mudler/_git/llama.cpp /tmp/localai-paged-apply-check
git -C /tmp/localai-paged-apply-check checkout -q 0ed235ea2c17a19fc8238668653946721ed136fd
for p in /home/mudler/_git/LocalAI/.claude/worktrees/feat+paged-attention/backend/cpp/llama-cpp-localai-paged/patches/paged/*.patch; do
  git -C /tmp/localai-paged-apply-check apply "$p"
done
git -C /tmp/localai-paged-apply-check add -A
git -C /tmp/localai-paged-apply-check diff --cached --quiet 51168c5eee2e35348d9006f0b2fab3dc6e7c01cc
```

Expected:

```text
Exit code 0.
```

- [x] **Step 3: Write clean source provenance into Phase 0 results**

Update `GB10_PARITY_PHASE0_RESULTS.md`:

```markdown
## Source Provenance

- Local llama.cpp fork: `/home/mudler/_git/llama.cpp`
- Branch: `localai-paged`
- HEAD: `51168c5eee2e35348d9006f0b2fab3dc6e7c01cc`
- Base pin: `0ed235ea2c17a19fc8238668653946721ed136fd`
- LocalAI patch mirror: applies cleanly and tree-matches fork HEAD.
```

- [x] **Step 4: Commit Task 2**

Run:

```bash
git add backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md
git commit -m "docs(paged): record phase0 source provenance" \
  -m "Record the clean llama.cpp fork source truth and LocalAI patch mirror verification for the GB10 parity reopen." \
  -m "Assisted-by: Codex:gpt-5"
```

Expected:

```text
Commit succeeds.
```

## Task 3: Existing Artifact Gap Report

**Files:**
- Modify: `backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md`

- [x] **Step 1: Extract the current artifact-backed numbers**

Run:

```bash
ssh dgx.casa 'set -e
{
  echo "=== COMBINED_DEFINITIVE key lines ==="
  grep -E "GIT_HEAD=|PAGED_BATCHED_BENCH|PAGED NPL=256|VLLM NPL=256|PAGED_GATE_MD5|VLLM PREFILL ptok" ~/bench/COMBINED_DEFINITIVE.txt || true
  echo "=== paged highN bench logs ==="
  grep -H "T_TG\\|S_TG\\|tokens" ~/highN_prof2/dec_npl256_ntg16.bench.log ~/highN_prof2/dec_npl256_ntg64.bench.log || true
  echo "=== vllm highN run logs ==="
  grep -H "12288\\|tokens\\|elapsed\\|tok/s\\|graph" ~/highN_vllm/vllm_moe_n256.run.log ~/highN_vllm/vllm_moe_n128.run.log || true
} > ~/bench/reopen_phase0/existing_artifact_extract.txt
cat ~/bench/reopen_phase0/existing_artifact_extract.txt
'
```

Expected:

```text
existing_artifact_extract.txt is created and shows CDEF, paged highN, and vLLM highN evidence.
```

- [x] **Step 2: Update Phase 0 results with artifact gaps**

Add:

```markdown
## Existing Artifact Gap Report

- CDEF prefill is mixed harness: paged `llama-batched-bench`, vLLM server/h2h.
- Paged high-N difference method has artifact support under `~/highN_prof2`.
- vLLM 1078 t/s true GPU-steady decode is not yet backed by a self-contained
  ntg16/ntg64 difference-method artifact in the inspected files.
- CDEF records a dev-tree `GIT_HEAD=a7d439e` while current shipped fork HEAD is
  `51168c5ee`; this must be separated from current production-source baselines.
```

- [x] **Step 3: Commit Task 3**

Run:

```bash
git add backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md
git commit -m "docs(paged): record phase0 artifact gaps" \
  -m "Record the existing benchmark artifact gaps that must be resolved before accepting the GB10 parity final-state claims." \
  -m "Assisted-by: Codex:gpt-5"
```

Expected:

```text
Commit succeeds.
```

## Task 4: Clean Build And Canonical Gates

**Files:**
- Modify: `backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md`

- [x] **Step 1: Re-run DGX preflight immediately before build**

Run:

```bash
ssh dgx.casa 'set -e
test -z "$(docker ps -q)"
test -z "$(nvidia-smi --query-compute-apps=pid --format=csv,noheader | grep . || true)"
if [ -e ~/gpu_bench_lock/owner ]; then grep -q "^FREE" ~/gpu_bench_lock/owner; fi
'
```

Expected:

```text
Exit code 0.
```

- [x] **Step 2: Start a detached clean build**

Run:

```bash
ssh dgx.casa 'set -e
mkdir -p ~/bench/reopen_phase0
cat > ~/bench/reopen_phase0/build_clean.sh <<'"'"'SH'"'"'
#!/usr/bin/env bash
set -euo pipefail
cd "$HOME"
rm -rf "$HOME/llama-paged-reopen-clean"
git clone git@github.com:mudler/llama.cpp.git "$HOME/llama-paged-reopen-clean"
cd "$HOME/llama-paged-reopen-clean"
git checkout 51168c5eee2e35348d9006f0b2fab3dc6e7c01cc
git status --short --branch > "$HOME/bench/reopen_phase0/build_source_status.txt"
cmake -S . -B build-cuda \
  -DGGML_CUDA=ON \
  -DCMAKE_CUDA_ARCHITECTURES=121 \
  -DCMAKE_BUILD_TYPE=Release \
  -DLLAMA_CURL=OFF
cmake --build build-cuda --target llama-server llama-batched-bench llama-completion test-backend-ops -j"$(nproc)"
git rev-parse HEAD > "$HOME/bench/reopen_phase0/build_git_head.txt"
stat -c "%n %y" build-cuda/bin/llama-server build-cuda/bin/llama-batched-bench build-cuda/bin/llama-completion build-cuda/bin/test-backend-ops > "$HOME/bench/reopen_phase0/build_binary_mtimes.txt"
touch "$HOME/bench/reopen_phase0/build_clean.done"
SH
chmod +x ~/bench/reopen_phase0/build_clean.sh
rm -f ~/bench/reopen_phase0/build_clean.done
nohup ~/bench/reopen_phase0/build_clean.sh > ~/bench/reopen_phase0/build_clean.log 2>&1 &
echo $! > ~/bench/reopen_phase0/build_clean.pid
'
```

Expected:

```text
Command returns quickly and writes build_clean.pid.
```

- [x] **Step 3: Poll build completion**

Note: first build attempt started as PID `625392` and failed during CMake
configure because `nvcc` was not on PATH. DGX has
`/usr/local/cuda-13.0/bin/nvcc`; retry uses explicit `CUDACXX`.

Retry build attempt started as PID `631100` and completed successfully.

Run:

```bash
ssh dgx.casa 'for i in $(seq 1 240); do
  if [ -f ~/bench/reopen_phase0/build_clean.done ]; then
    echo DONE
    tail -20 ~/bench/reopen_phase0/build_clean.log
    exit 0
  fi
  if ! kill -0 "$(cat ~/bench/reopen_phase0/build_clean.pid)" 2>/dev/null; then
    echo BUILD_EXITED_WITHOUT_DONE
    tail -80 ~/bench/reopen_phase0/build_clean.log
    exit 1
  fi
  sleep 30
done
echo BUILD_TIMEOUT
tail -80 ~/bench/reopen_phase0/build_clean.log
exit 2'
```

Expected:

```text
DONE
```

- [x] **Step 4: Run canonical md5 gates**

Run:

```bash
ssh dgx.casa 'set -e
cd ~/llama-paged-reopen-clean/build-cuda/bin
L="LLAMA_KV_PAGED=1 LLAMA_MOE_FORCE_GRAPHS=1 GGML_NO_BACKTRACE=1"
MOE=/home/mudler/bench/q36-35b-a3b-nvfp4.gguf
DENSE=/home/mudler/bench/q36-27b-nvfp4.gguf
env $L ./llama-completion -m "$MOE" -ngl 99 -fa on -c 4096 --temp 0 --seed 1 -n 48 -p "The capital of France is" </dev/null | tee ~/bench/reopen_phase0/gate_moe.txt | md5sum | tee ~/bench/reopen_phase0/gate_moe.md5
env $L ./llama-completion -m "$DENSE" -ngl 99 -fa on -c 4096 --temp 0 --seed 1 -n 48 -p "The capital of France is" </dev/null | tee ~/bench/reopen_phase0/gate_dense.txt | md5sum | tee ~/bench/reopen_phase0/gate_dense.md5
'
```

Expected:

```text
MoE md5 is 8cb0ce23777bf55f92f63d0292c756b0
Dense md5 is 5951a5b4d624ce891e22ab5fca9bc439
```

- [x] **Step 5: Update Phase 0 results and commit**

Add build SHA, binary mtimes, gate md5s, and whether they matched expectations.

Run:

```bash
git add backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md
git commit -m "docs(paged): record phase0 clean build gates" \
  -m "Record the clean DGX build provenance and canonical greedy md5 gates for the GB10 parity reopen." \
  -m "Assisted-by: Codex:gpt-5"
```

Expected:

```text
Commit succeeds if gates pass. If gates fail, do not commit success; record failure and stop Phase 0.
```

## Task 5: Clean Prefill Baseline

**Files:**
- Modify: `backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md`

- [x] **Step 1: Run paged prefill baseline**

Run:

```bash
ssh dgx.casa 'set -e
cd ~/llama-paged-reopen-clean/build-cuda/bin
L="LLAMA_KV_PAGED=1 LLAMA_MOE_FORCE_GRAPHS=1 GGML_NO_BACKTRACE=1"
MOE=/home/mudler/bench/q36-35b-a3b-nvfp4.gguf
DENSE=/home/mudler/bench/q36-27b-nvfp4.gguf
env $L ./llama-batched-bench -m "$MOE" -c 131072 -b 2048 -ub 512 -ngl 99 -fa on -npp 512,2048 -ntg 4 -npl 32 > ~/bench/reopen_phase0/paged_moe_prefill.txt 2>&1
env $L ./llama-batched-bench -m "$DENSE" -c 131072 -b 2048 -ub 512 -ngl 99 -fa on -npp 512,2048 -ntg 4 -npl 32 > ~/bench/reopen_phase0/paged_dense_prefill.txt 2>&1
grep -E "S_PP|^\\|" ~/bench/reopen_phase0/paged_moe_prefill.txt ~/bench/reopen_phase0/paged_dense_prefill.txt
'
```

Expected:

```text
Both files contain S_PP rows for 512 and 2048.
```

- [x] **Step 2: Update Phase 0 results and commit**

Record exact S_PP rows and artifact paths.

Run:

```bash
git add backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md
git commit -m "docs(paged): record phase0 prefill baseline" \
  -m "Record clean-source MoE and dense prefill baselines for the GB10 parity reopen." \
  -m "Assisted-by: Codex:gpt-5"
```

Expected:

```text
Commit succeeds.
```

## Task 6: Decode Difference-Method Repro

**Files:**
- Modify: `backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md`

- [ ] **Step 1: Dispatch a vLLM harness discovery subagent**

Prompt:

```text
Read-only task. On dgx.casa, inspect existing vLLM high-N profiling scripts/logs under ~/highN_vllm, ~/bench, and the installed vLLM package. Find the exact command sequence needed to produce a graph-node-traced ntg16/ntg64 difference-method decode artifact for vLLM comparable to paged highN_prof2. Do not run vLLM, nsys, servers, builds, or benchmarks. Return commands and artifact paths only.
```

Expected:

```text
Subagent returns a concrete vLLM command sequence or reports that no prior harness exists.
```

- [ ] **Step 2: Run paged graph-node-traced decode difference-method**

Run only after DGX preflight passes:

```bash
ssh dgx.casa 'set -e
test -z "$(docker ps -q)"
test -z "$(nvidia-smi --query-compute-apps=pid --format=csv,noheader | grep . || true)"
if [ -e ~/gpu_bench_lock/owner ]; then grep -q "^FREE" ~/gpu_bench_lock/owner; fi
mkdir -p ~/bench/reopen_phase0/paged_decode_nsys
cd ~/llama-paged-reopen-clean/build-cuda/bin
L="LLAMA_KV_PAGED=1 LLAMA_MOE_FORCE_GRAPHS=1 GGML_NO_BACKTRACE=1"
MOE=/home/mudler/bench/q36-35b-a3b-nvfp4.gguf
for NTG in 16 64; do
  env $L nsys profile --force-overwrite=true --cuda-graph-trace=node \
    -o ~/bench/reopen_phase0/paged_decode_nsys/paged_moe_n256_ntg${NTG} \
    ./llama-batched-bench -m "$MOE" -c 131072 -b 2048 -ub 512 -ngl 99 -fa on \
      -npp 128 -ntg "$NTG" -npl 256 \
    > ~/bench/reopen_phase0/paged_decode_nsys/paged_moe_n256_ntg${NTG}.bench.log 2>&1
done
'
```

Expected:

```text
Two `.nsys-rep` files and two `.bench.log` files exist.
```

- [ ] **Step 3: Run vLLM graph-node-traced decode difference-method**

Use the exact command sequence from Step 1. Required properties:

```text
nsys profile uses --cuda-graph-trace=node
N is 128 or 256
ntg 16 and ntg 64 artifacts are both captured
model is /home/mudler/bench/q36-35b-a3b-nvfp4-vllm
vLLM version is recorded as 0.23.0 or the actual installed value
```

Expected:

```text
Two vLLM graph-node-traced artifacts exist and can be reduced by the difference method.
```

- [ ] **Step 4: Update Phase 0 results and commit**

Record paged and vLLM tokens/s using:

```text
per-token-linear decode throughput = generated token delta / (ntg64 wall - ntg16 wall)
```

Run:

```bash
git add backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md
git commit -m "docs(paged): record phase0 decode repro" \
  -m "Record graph-node-traced paged and vLLM decode difference-method artifacts for the GB10 parity reopen." \
  -m "Assisted-by: Codex:gpt-5"
```

Expected:

```text
Commit succeeds only after both engines have comparable artifacts.
```

## Task 7: Phase 1 W4A16 Kill-Gate Plan

**Files:**
- Modify: `backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md`
- Later fork-first changes in `/home/mudler/_git/llama.cpp/ggml/src/ggml-cuda/w4a16-gemm.cu`

- [ ] **Step 1: Run current W4A16 forced baseline**

Run:

```bash
ssh dgx.casa 'set -e
cd ~/llama-paged-reopen-clean/build-cuda/bin
MOE=/home/mudler/bench/q36-35b-a3b-nvfp4.gguf
LBASE="LLAMA_KV_PAGED=1 LLAMA_MOE_FORCE_GRAPHS=1 GGML_NO_BACKTRACE=1"
env $LBASE ./llama-batched-bench -m "$MOE" -c 131072 -b 2048 -ub 512 -ngl 99 -fa on -npp 512,2048 -ntg 4 -npl 32 > ~/bench/reopen_phase0/w4a16_off.txt 2>&1
env $LBASE LLAMA_W4A16_PREFILL_M=64 LLAMA_W4A16_DEBUG=1 ./llama-batched-bench -m "$MOE" -c 131072 -b 2048 -ub 512 -ngl 99 -fa on -npp 512,2048 -ntg 4 -npl 32 > ~/bench/reopen_phase0/w4a16_on_thr64.txt 2>&1
grep -E "S_PP|^\\||W4A16" ~/bench/reopen_phase0/w4a16_off.txt ~/bench/reopen_phase0/w4a16_on_thr64.txt
'
```

Expected:

```text
Artifacts prove current clean W4A16 delta against FP4-MMQ.
```

- [ ] **Step 2: Decide first W4A16 implementation target**

Use nsys or debug logs to choose exactly one first target:

```text
Option A: fuse/remove f32->bf16 cast pre-pass
Option B: device-side tile metadata
Option C: 16-byte weight staging/shared-memory layout
Option D: tile-shape retune for ragged expert M
```

Expected:

```text
Only one implementation target is selected for the first fork commit.
```

- [ ] **Step 3: Stop before kernel edits if Phase 0 is incomplete**

Expected:

```text
No W4A16 code edit begins unless Tasks 1-6 are complete or explicitly waived by the maintainer.
```
