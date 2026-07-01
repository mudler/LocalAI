# Gate Projection Policy Phase38 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** decide whether the Phase37 `ffn_gate_inp*` SGEMM bucket is a safe vLLM-parity lever without breaking inference.

**Architecture:** Treat router logits and shared-expert gate projections as inference-critical F32 policy until proven otherwise. Phase38 is analysis-first: record the source/vLLM comparison, strengthen the default inference gate, and only allow later route changes behind md5/op gates plus KL if byte output changes.

**Tech Stack:** LocalAI paged llama.cpp backend, llama.cpp CUDA fork, DGX GB10, vLLM Qwen3-Next fused-MoE code, `paged-inference-gates.sh`.

---

### Task 1: Establish a fresh inference baseline

**Files:**
- Read: `backend/cpp/llama-cpp-localai-paged/paged-inference-gates.sh`
- Artifact: `dgx.casa:/home/mudler/bench/phase38_gate_baseline/20260701_084410`

- [x] **Step 1: Verify DGX is idle**

Run:

```bash
ssh dgx.casa 'set -euo pipefail; echo owner=$(cat ~/gpu_bench_lock/owner 2>/dev/null || true); echo docker=$(docker ps -q | wc -l); echo local_ai_worker=$(docker ps --format "{{.Names}}" | grep -c local-ai-worker || true); echo compute=$(nvidia-smi --query-compute-apps=pid --format=csv,noheader | sed "/^$/d" | wc -l); nvidia-smi --query-gpu=name,driver_version --format=csv,noheader'
```

Observed:

```text
owner=FREE phase33-small-m-tile-policy-done 1782883234
docker=0
local_ai_worker=0
compute=0
NVIDIA GB10, 580.159.03
```

- [x] **Step 2: Run canonical md5 and op gates**

Run:

```bash
ssh dgx.casa 'set -euo pipefail; ART=$HOME/bench/phase38_gate_baseline/$(date +%Y%m%d_%H%M%S); mkdir -p "$ART"; BIN=$HOME/llama-phase6-source/build-phase36/bin ART="$ART" OPS=MUL_MAT,MUL_MAT_ID $HOME/paged-inference-gates.sh | tee "$ART/gate.log"'
```

Observed:

```text
moe md5 OK: 8cb0ce23777bf55f92f63d0292c756b0
dense md5 OK: 5951a5b4d624ce891e22ab5fca9bc439
1146/1146 tests passed
Backend CUDA0: OK
806/806 tests passed
Backend CUDA0: OK
paged inference gates OK
artifacts: /home/mudler/bench/phase38_gate_baseline/20260701_084410
```

### Task 2: Strengthen the reusable inference gate

**Files:**
- Modify: `backend/cpp/llama-cpp-localai-paged/paged-inference-gates.sh`

- [x] **Step 1: Make both matmul op gates default**

Change:

```bash
OPS=${OPS:-MUL_MAT_ID}
```

to:

```bash
OPS=${OPS:-MUL_MAT,MUL_MAT_ID}
```

Also update `--help` text so the default is visible.

- [x] **Step 2: Verify shell syntax and help output**

Run:

```bash
bash -n backend/cpp/llama-cpp-localai-paged/paged-inference-gates.sh
backend/cpp/llama-cpp-localai-paged/paged-inference-gates.sh --help | grep 'default: MUL_MAT,MUL_MAT_ID'
```

Expected: exit 0 and the updated default line is printed.

### Task 3: Record the Phase37 to Phase38 policy decision

**Files:**
- Read: `/home/mudler/_git/llama.cpp/src/models/qwen35moe.cpp`
- Read: `/home/mudler/_git/llama.cpp/src/llama-graph.cpp`
- Read: `/home/mudler/_git/vllm/vllm/model_executor/models/qwen3_next.py`
- Read: `/home/mudler/_git/vllm/vllm/model_executor/layers/fused_moe/runner/moe_runner.py`
- Modify: `backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md`
- Modify: `backend/cpp/llama-cpp-localai-paged/docs/VLLM_PARITY_LEVER_MAP.md`
- Modify: `backend/cpp/llama-cpp-localai-paged/docs/PARITY_HANDOFF.md`

- [x] **Step 1: Source inspection result**

`qwen35moe.cpp` creates `ffn_gate_inp.weight` as `[n_embd, n_expert]` and `ffn_gate_inp_shexp.weight` as `[n_embd]`. The graph uses:

```cpp
build_moe_ffn(cur, model.layers[il].ffn_gate_inp, ...)
build_lora_mm(model.layers[il].ffn_gate_inp_shexp, cur)
```

`llama-graph.cpp` computes router logits through `build_lora_mm(gate_inp, cur)` and labels the result `ffn_moe_logits`.

- [x] **Step 2: vLLM comparison result**

`qwen3_next.py` constructs both gates as `ReplicatedLinear(..., quant_config=None)`. `moe_runner.py` can concatenate `gate.weight` and `shared_expert_gate.weight` into `_combined_gate_weight` for fused shared-expert routing.

- [x] **Step 3: Decision**

The SGEMM bucket is not an accidental slow path. It is router/shared-expert gate math kept unquantized by both llama.cpp and vLLM. Do not force BF16 or NVFP4 for `ffn_gate_inp*`. The safe follow-up lever is a default-off fused gate projection experiment that preserves F32 math and split semantics, or a diagnostic proof that the two current SGEMMs are too small to matter.

- [ ] **Step 4: Gate any later fused-gate experiment**

Before benchmarking any code change:

```bash
BIN=$HOME/llama-phase6-source/build-phase36/bin \
ART=$HOME/bench/phase38_gate_fused_candidate \
OPS=MUL_MAT,MUL_MAT_ID \
$HOME/paged-inference-gates.sh
```

If either md5 differs, stop and run the KL gate before serving benchmarks. If either op gate fails, reject the candidate.

### Task 4: Commit the docs and gate-script update

**Files:**
- Modify: `backend/cpp/llama-cpp-localai-paged/paged-inference-gates.sh`
- Modify: `docs/superpowers/plans/2026-07-01-gate-projection-policy-phase38.md`
- Modify: `backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md`
- Modify: `backend/cpp/llama-cpp-localai-paged/docs/VLLM_PARITY_LEVER_MAP.md`
- Modify: `backend/cpp/llama-cpp-localai-paged/docs/PARITY_HANDOFF.md`

- [x] **Step 1: Run local syntax checks**

Run:

```bash
bash -n backend/cpp/llama-cpp-localai-paged/paged-inference-gates.sh
```

Expected: exit 0.

- [x] **Step 2: Commit**

Run:

```bash
git add backend/cpp/llama-cpp-localai-paged/paged-inference-gates.sh \
  docs/superpowers/plans/2026-07-01-gate-projection-policy-phase38.md \
  backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md \
  backend/cpp/llama-cpp-localai-paged/docs/VLLM_PARITY_LEVER_MAP.md \
  backend/cpp/llama-cpp-localai-paged/docs/PARITY_HANDOFF.md
git commit -m "docs(paged): scope gate projection policy" \
  -m "Assisted-by: Codex:gpt-5"
```
