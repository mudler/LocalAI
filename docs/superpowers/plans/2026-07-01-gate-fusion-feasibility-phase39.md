# Gate Fusion Feasibility Phase39 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** decide whether to implement a quick fused F32 router/shared-expert gate projection after Phase38.

**Architecture:** Phase39 is evidence-first and source-conservative. It compares the Phase37 tensor-name trace, the Phase27 graph-node serving profile, and llama.cpp graph/model-loader capabilities. It rejects graph-time weight concatenation because it would add layout-copy work in a bucket that is already measurable, and scopes the only acceptable follow-up as a persistent/load-time combined-weight design with md5/op/KL gates.

**Tech Stack:** LocalAI paged llama.cpp backend, llama.cpp CUDA fork, DGX GB10, Nsight Systems, vLLM Qwen3-Next fused-MoE source comparison.

---

### Task 1: Inspect current graph/model support

**Files:**
- Read: `/home/mudler/_git/llama.cpp/ggml/include/ggml.h`
- Read: `/home/mudler/_git/llama.cpp/src/models/qwen35moe.cpp`
- Read: `/home/mudler/_git/llama.cpp/src/llama-graph.cpp`
- Read: `/home/mudler/_git/vllm/vllm/model_executor/layers/fused_moe/runner/moe_runner.py`

- [x] **Step 1: Confirm llama.cpp gate tensors**

`qwen35moe.cpp` creates:

```cpp
layer.ffn_gate_inp       = create_tensor(..., { n_embd, n_expert }, flags);
layer.ffn_gate_inp_shexp = create_tensor(..., { n_embd }, flags);
```

and computes:

```cpp
build_moe_ffn(cur, model.layers[il].ffn_gate_inp, ...)
build_lora_mm(model.layers[il].ffn_gate_inp_shexp, cur)
```

- [x] **Step 2: Confirm ggml graph-time concat is available but not free**

`ggml.h` exposes `ggml_concat()` and `ggml_view_*()`, so a graph-time fused
gate is syntactically possible. It would require building a temporary combined
weight in the compute graph unless the model loader creates a persistent
combined tensor.

- [x] **Step 3: Confirm vLLM's relevant idea**

vLLM's fused-MoE runner concatenates router and shared-expert gate weights into
`_combined_gate_weight`. The useful design pattern is persistent F32 combined
gate weight, not BF16/NVFP4 routing.

### Task 2: Reuse existing serving evidence

**Files:**
- Artifact: `dgx.casa:/home/mudler/bench/phase37_cublas_name_trace/20260701_083227`
- Artifact: `dgx.casa:/home/mudler/bench/phase27_graph_node_serving/20260701_055519`
- Artifact: `dgx.casa:/home/mudler/bench/phase39_gate_sgemm_profile/phase27_reanalysis`

- [x] **Step 1: Read Phase37 route-name evidence**

Observed:

```text
2884 route=bf16_tc
1212 route=sgemm
16 route=sgemm type=0 src0=blk.N.ffn_gate_inp.weight src1=attn_post_norm-N dst=ffn_moe_logits-N
16 route=sgemm type=0 src0=blk.N.ffn_gate_inp_shexp.weight src1=attn_post_norm-N dst=shared_expert_gate-N
```

- [x] **Step 2: Re-analyze Phase27 graph-node serving profile**

Run:

```bash
ssh dgx.casa 'set -euo pipefail; ART=/home/mudler/bench/phase39_gate_sgemm_profile/phase27_reanalysis; SRC=/home/mudler/bench/phase27_graph_node_serving/20260701_055519/llama_graph_node.nsys-rep; mkdir -p "$ART"; nsys stats --report cuda_gpu_kern_sum,cuda_api_sum --format csv --output "$ART/phase27" "$SRC"'
```

Observed serving kernel buckets:

```text
TOTAL kernel time: 20.0372 s
cublas_bf16_gemm       1892.81ms   9.45%
cutlass_bf16_gemm       684.01ms   3.41%
concat_layout           459.84ms   2.29%
```

Top raw kernel evidence includes:

```text
concat_non_cont         459.84ms   2.3%  2250 instances
```

### Task 3: Decision

**Files:**
- Modify: `backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md`
- Modify: `backend/cpp/llama-cpp-localai-paged/docs/VLLM_PARITY_LEVER_MAP.md`
- Modify: `backend/cpp/llama-cpp-localai-paged/docs/PARITY_HANDOFF.md`

- [x] **Step 1: Reject graph-time fused gate via `ggml_concat`**

Do not implement a quick graph-time combined gate that concatenates
`ffn_gate_inp` and `ffn_gate_inp_shexp` inside the compute graph. It risks
adding work to the existing `concat_layout` bucket (`459.84ms`, `2.29%`) before
removing enough SGEMM overhead, and it would be a high-conflict graph/model edit
without clear upside.

- [x] **Step 2: Preserve the only acceptable follow-up shape**

The only follow-up worth scoping is a persistent/load-time F32 combined gate
weight:

```text
combined_gate_weight = concat_rows(ffn_gate_inp.weight,
                                   ffn_gate_inp_shexp.weight)
```

Requirements:

- default-off until gates pass;
- no BF16/NVFP4 conversion for gate weights;
- no graph-time weight concat;
- split combined output into `ffn_moe_logits` and `shared_expert_gate` views;
- MoE/dense md5 must match before serving benchmarks;
- `MUL_MAT` and `MUL_MAT_ID` must pass;
- if md5 changes, run KL first and reject on KL regression.

### Task 4: Verify and commit docs

**Files:**
- Modify: `docs/superpowers/plans/2026-07-01-gate-fusion-feasibility-phase39.md`
- Modify: `backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md`
- Modify: `backend/cpp/llama-cpp-localai-paged/docs/VLLM_PARITY_LEVER_MAP.md`
- Modify: `backend/cpp/llama-cpp-localai-paged/docs/PARITY_HANDOFF.md`

- [x] **Step 1: Check docs diff**

Run:

```bash
git diff -- docs/superpowers/plans/2026-07-01-gate-fusion-feasibility-phase39.md \
  backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md \
  backend/cpp/llama-cpp-localai-paged/docs/VLLM_PARITY_LEVER_MAP.md \
  backend/cpp/llama-cpp-localai-paged/docs/PARITY_HANDOFF.md
```

Expected: only Phase39 documentation changes.

- [x] **Step 2: Commit**

Run:

```bash
git add -f docs/superpowers/plans/2026-07-01-gate-fusion-feasibility-phase39.md
git add backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md \
  backend/cpp/llama-cpp-localai-paged/docs/VLLM_PARITY_LEVER_MAP.md \
  backend/cpp/llama-cpp-localai-paged/docs/PARITY_HANDOFF.md
git commit -m "docs(paged): reject graph-time gate fusion shortcut" \
  -m "Assisted-by: Codex:gpt-5"
```
