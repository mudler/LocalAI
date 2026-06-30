# Phase 6: Serving nsys Gap Classifier

**Status:** Completed. Phase 6 kept no source changes.

**Scope:** Measurement-first. Do not edit llama.cpp source in this phase unless
the serving profiles identify a small, bit-exact, fork-first patch candidate.
Every candidate must pass the md5 and op gates before it can be mirrored into
LocalAI patches.

**Goal:** Classify the remaining GB10 MoE serving gap against vLLM by profiling a
steady serving window for both engines, then pick exactly one next lever from
measured evidence.

## Safety Gates

- Canonical paged MoE greedy md5 must stay `8cb0ce23777bf55f92f63d0292c756b0`.
- Canonical dense greedy md5 must stay `5951a5b4d624ce891e22ab5fca9bc439`.
- If a patch touches W4A16, forced `bm32` and `base` md5 must both stay
  `07db32c2bcb78d17a43ed18bc22705cd`.
- If a patch touches `MUL_MAT_ID` routing or CUDA MoE kernels, run
  `test-backend-ops test -b CUDA0 -o MUL_MAT_ID -j 1` and require `806/806`.
- Patch promotion threshold: no semantic gate regression, no generated patch
  hand-editing, and at least one measured serving bucket improvement that explains
  a material share of the vLLM gap.
- Inference-safety rule: a candidate that changes CUDA routing, sampler inputs,
  graph construction, or MoE kernels is not kept unless the md5 gates are rerun
  from the clean candidate binary and still match the canonical values above.
  Performance-only evidence is insufficient.

## Checklist

- [x] Confirm DGX is idle before running GPU work.
  - Docker containers: `0`.
  - Compute PIDs: `0`.
  - Lock: `FREE released-by-claude-fp4norm-profile 1782828229`.
  - GPU util: `0%`.
- [x] Locate/reuse the existing llama.cpp and vLLM serving harnesses.
  - Both-engine h2h harness: `/home/mudler/bench/combined_definitive.sh`.
  - Current OpenAI completions load client: `/home/mudler/bench/h2h_cli3.py`.
  - Paged serving command shape: `llama-server -c 262144 --parallel 256 -b 2048
    -ub 512 -ngl 99 -fa on --port 8090 --no-webui` with
    `LLAMA_KV_PAGED=1 LLAMA_MOE_FORCE_GRAPHS=1 GGML_NO_BACKTRACE=1`.
  - vLLM serving command shape: `vllm serve
    /home/mudler/bench/q36-35b-a3b-nvfp4-vllm --served-model-name q36
    --gpu-memory-utilization 0.85 --max-model-len 4096 --max-num-seqs 256
    --port 8000 --tensor-parallel-size 1`.
  - Existing static high-N nsys harnesses:
    `/home/mudler/highN_nsys.sh`, `/home/mudler/vllm_moe_nsys.sh`, and
    `/home/mudler/vllm_moe_prof.py`.
- [x] Inspect `MUL_MAT_ID` fallback predicates before patching.
  - `LLAMA_MOE_FORCE_GRAPHS=1` is used by harnesses but is not an implemented
    hard-force predicate in the inspected CUDA path.
  - The host fallback still has stream synchronizations after device-to-host ids
    copy and after sorted-id upload.
  - Highest-risk condition to verify by nsys: NVFP4 Blackwell MoE with token
    count above `LLAMA_FP4_PREFILL_M` or `LLAMA_W4A16_PREFILL_M` can route away
    from grouped MMQ into the host fallback.
- [x] Build exact fork head on DGX for Phase 6 profiling.
  - Source mirror: `/home/mudler/llama-phase6-source`.
  - Fork head: `d9b9be0bee3d7239132bfca05d5b057ff4ee4cc3`.
  - Build config: CUDA Release, `CMAKE_CUDA_COMPILER=/usr/local/cuda-13.0/bin/nvcc`,
    `CMAKE_CUDA_ARCHITECTURES=121`.
  - Built targets: `llama-server`, `llama-batched-bench`, `llama-completion`,
    `test-backend-ops`.
- [x] Run canonical md5 gates before serving profiling.
  - MoE md5: `8cb0ce23777bf55f92f63d0292c756b0`.
  - Dense md5: `5951a5b4d624ce891e22ab5fca9bc439`.
  - Note: an older `-no-cnv` command shape produced different text hashes; the
    Phase 0 canonical command without `-no-cnv` matches the recorded gates.
- [x] Capture a llama-server steady serving nsys window with
  `--cuda-graph-trace=node`.
- [x] Capture a comparable vLLM steady serving nsys window with
  `--cuda-graph-trace=node`.
- [x] Reduce both profiles into kernel/API buckets:
  `MUL_MAT_ID`, FA decode, gated_delta_net, bf16 projections, activation-quant,
  sampling/logits, and CUDA API sync/memcpy.
- [x] Count `cudaStreamSynchronize` and host copies between `MUL_MAT_ID` launches
  to confirm or reject the host-sync fallback risk.
- [x] Compare serving-narrow vs static-wide vs vLLM and select one next lever:
  H1 MoE GEMM collapse/fallback, H2 paged FA ragged imbalance, H3 GDN narrow
  occupancy, H4 projection/ragged batch efficiency, H5 sampling/logits, or H6
  activation quant.
- [x] If a source change is justified, implement fork-first in
  `/home/mudler/_git/llama.cpp`, keep it stacked as one incremental commit, then
  mirror with `git format-patch` into LocalAI.
- [x] Run the safety gates and update this file plus
  `backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md`.

## Current Decision

W4A16 prefill was not the highest-leverage path for Phase 6. The accepted Phase 1-4
changes improved forced W4A16 from roughly `1314/1339` to `1466/1495` S_PP, but
default FP4-MMQ remains around `2303/2423`. The next evidence gate is serving
nsys, because the committed lever map says the residual gap is in real
continuous serving, not the static wide decode kernel regime.

## Expected Output

Artifacts should land under a dated `~/bench/phase6_serving_nsys/` directory on
DGX. For each engine, keep:

- server command and client command logs,
- nsys `.nsys-rep` files,
- exported `cuda_gpu_kern_sum`, `cuda_api_sum`, and any trace table needed to
  count syncs between MoE kernels,
- reduced CSV/markdown bucket summary,
- md5/op gate logs for any patch candidate.

## Results

Artifacts:

- llama.cpp serving nsys:
  `/home/mudler/bench/phase6_serving_nsys/llama_server_n128/`.
- vLLM serving nsys:
  `/home/mudler/bench/phase6_serving_nsys/vllm_server_n128/`.
- rejected sampler short-circuit gates:
  `/home/mudler/bench/phase6_serving_nsys/sampler_shortcircuit_gates/`.
- rejected sampler short-circuit serving A/B:
  `/home/mudler/bench/phase6_serving_nsys/sampler_shortcircuit_ab/`.

Serving result at 128 clients, `ptok=128`, `gen=128`:

| Engine | decode tok/s/seq | decode agg tok/s | prefill tok/s |
|--------|------------------|------------------|---------------|
| llama.cpp under nsys | 4.05 | 591.0 | 1567.4 |
| vLLM under nsys | 6.95 | 961.1 | 5073.6 |

llama.cpp nsys top buckets:

- `gated_delta_net_cuda`: 33.7% GPU kernel time, 10.21s.
- NVFP4 `mul_mat_q`: 24.3% + 5.5% for the largest grouped variants, 9.04s
  combined.
- `quantize_mmq_nvfp4`: 2.7%, 0.81s.
- `flash_attn_tile`: 1.3%, 0.38s.
- CUDA API: `cudaStreamSynchronize` 76.5% API time, 23.66s over 106585 calls.
  8028 synchronizes followed `cudaMemcpyAsync` and summed 21.41s.

vLLM nsys top buckets:

- `fused_recurrent_gated_delta_rule_packed_decode_kernel`: 16.6%, 8.95s.
- `marlin_moe_wna16::Marlin`: 11.9% plus smaller Marlin-MoE variants.
- `flash_fwd_splitkv_kernel`: 0.6% + 0.1% visible split-K FA decode rows.
- CUDA API has startup/module-load noise in the delayed profile, so use the
  kernel buckets and h2h result as the primary comparison.

Decision:

- The sync-heavy path is real, but source inspection shows the initial
  `MUL_MAT_ID` host-fallback hypothesis is incomplete for this run:
  `LLAMA_FP4_PREFILL_M` and `LLAMA_W4A16_PREFILL_M` were unset, so grouped MMQ
  should stay enabled. The largest sync signature instead matches thousands of
  small synchronous tensor uploads, especially backend sampler inputs.
- A fork-first sampler short-circuit experiment skipped backend distribution
  sampling when prior backend filters collapsed the candidate set to one token
  (`temperature=0` path). It passed gates:
  - MoE md5 `8cb0ce23777bf55f92f63d0292c756b0`.
  - Dense md5 `5951a5b4d624ce891e22ab5fca9bc439`.
  - `MUL_MAT_ID`: `806/806` on CUDA0.
- The sampler experiment was rejected: no-nsys serving reps were `4.19` and
  `3.55` tok/s/seq, not a material improvement over the known baseline band.
  The fork patch was reverted; no commit and no LocalAI patch were created.

Next lever:

- H3/H1 combined, but with H3 checked first: llama.cpp spends 33.7% of GPU time
  in GDN decode versus vLLM's 16.6%, and vLLM's aggregate decode remains 1.63x
  faster in the same serving shape.

## Follow-up: GDN Env Grid

Artifact: `/home/mudler/bench/phase6_serving_nsys/gdn_grid/`.

Shape: `n=128`, `ptok=128`, `gen=64`.

| Setting | decode tok/s/seq | decode agg tok/s | Decision |
|---------|------------------|------------------|----------|
| default | 3.91 | 647.9 | baseline |
| `GDN_NW=4 GDN_CPW=1` | 3.80 | 628.9 | reject |
| `GDN_NW=8 GDN_CPW=2` | 3.94 | 624.5 | reject |
| `GDN_NW=8 GDN_CPW=4` | 3.91 | 647.6 | reject |
| `GDN_NW=8 GDN_CPW=8` | 4.00 | 636.9 | no material win |
| `GDN_NW=16 GDN_CPW=4` | 3.85 | 637.5 | reject |
| `GDN_NW=16 GDN_CPW=8` | 3.96 | 652.0 | no material win |

Result: rejected as an env-only lever. Existing GDN geometry variants are too
close in the serving gate to justify a source change. Next focus is the largest
remaining differentiating bucket: llama.cpp NVFP4 grouped `mul_mat_q` versus
vLLM Marlin-MoE.

## Follow-up: MoE MMQ Tile Env Grid

Artifact: `/home/mudler/bench/phase6_serving_nsys/mmq_grid/`.

Shape: `n=128`, `ptok=128`, `gen=64`.

| Setting | decode tok/s/seq | decode agg tok/s | Decision |
|---------|------------------|------------------|----------|
| default | 3.90 | 645.3 | baseline |
| `LLAMA_MOE_AUTO_TILE=0` | 3.90 | 655.3 | tied/no material win |
| `LLAMA_MOE_DECODE_TILE=32` | 3.82 | 635.9 | reject |
| `LLAMA_MOE_DECODE_TILE=48` | 3.81 | 637.3 | reject |
| `LLAMA_MOE_DECODE_TILE=96` | 3.84 | 642.8 | reject |
| `LLAMA_MOE_DECODE_TILE=128` | 3.84 | 640.6 | reject |
| `LLAMA_MOE_MMQ_X=32` | 3.76 | 642.0 | reject; prefill worsened |

Result: rejected as an env-only lever. Existing grouped-MMQ tile knobs do not
materially close the serving gap, so a selector-only source patch is not
justified.

## Completion

Phase 6 completed as a classifier, not as a source patch phase:

- Accepted source patches before Phase 6 remained intact through fork head
  `d9b9be0bee3d7239132bfca05d5b057ff4ee4cc3`.
- The sampler short-circuit candidate passed inference gates but failed the
  serving performance gate, so it was reverted and not mirrored.
- GDN and grouped-MMQ env grids did not clear the material-improvement threshold.
- No LocalAI patch was generated for Phase 6. The next phase must start from a
  clean fork and keep the same md5/op gates before any source candidate is kept.
