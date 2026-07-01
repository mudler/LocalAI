# GDN Shared-A/Ai Cost Model

Phase 12 decides whether the next GDN prefill attempt should implement a
shared-A/Ai global-scratch prototype or stop GDN kernel work on GB10.

## Reference Points

llama.cpp:

- `/home/mudler/_git/llama.cpp/ggml/src/ggml-cuda/gated_delta_net.cu`
  - `gated_delta_net_chunked_cuda`
  - `launch_gdn_chunked`
  - `launch_gated_delta_net`
  - `ggml_cuda_op_gated_delta_net`

vLLM/FLA:

- `/home/mudler/_git/vllm/vllm/model_executor/layers/fla/ops/chunk.py`
  - `chunk_gated_delta_rule_fwd`
- `/home/mudler/_git/vllm/vllm/model_executor/layers/fla/ops/solve_tril.py`
  - `solve_tril`
  - `solve_tril_16x16_kernel`
  - `merge_16x16_to_32x32_inverse_kernel`
  - `merge_16x16_to_64x64_inverse_kernel`
- `/home/mudler/_git/vllm/vllm/model_executor/layers/fla/ops/wy_fast.py`
  - `recompute_w_u_fwd`

## Metadata

DGX metadata artifact:

- `/home/mudler/bench/phase12_gdn_shared_ai_cost_model/model_metadata.txt`

GGUF metadata:

| Model | Arch | Blocks | Full-attn interval | GDN layers | SSM inner | SSM state | GDN heads |
|-------|------|--------|--------------------|------------|-----------|-----------|-----------|
| MoE | `qwen35moe` | 41 | 4 | 30 inferred | 4096 | 128 | 32 inferred |
| Dense | `qwen35` | 64 | 4 | 48 inferred | 6144 | 128 | 48 inferred |

Notes:

- `GDN heads = ssm.inner_size / ssm.state_size`.
- MoE has one `nextn` layer; the serving/prefill stack uses the 40 normal
  layers, with 30 GDN layers at interval 4.
- Dense has 64 layers, 48 GDN layers at interval 4.

## Dynamic Shared Memory

Formula:

```text
C16 full-width current M5:
  floats = S_v*S_v + 2*C*S_v + S_v*C + C*C + 3*C + 2*C*C

C32 full-width:
  floats = S_v*S_v + 2*C*S_v + S_v*C + C*C + 3*C + 2*C*C

C32 slab64 with U staging:
  floats = S_v*64 + 2*C*S_v + 64*C + C*C + 3*C + 2*C*C + 64*C
```

For `S_v=128`:

| Shape | Bytes | KiB | Fits GB10 dynamic smem? |
|-------|-------|-----|-------------------------|
| C16 full-width | 93,376 | 91.19 | yes |
| C32 full-width | 127,360 | 124.38 | no |
| C32 slab64 + U staging | 94,592 | 92.38 | yes |

Implication:

- C32 full-width cannot be a single current-style CTA on GB10.
- C32 only fits by splitting value columns or by changing state residency.
- Splitting value columns must share A/Ai or it repeats the Phase 10 failure.

## Ai Scratch Size

Formula:

```text
Ai scratch bytes = npl * H * ceil(npp / BT) * BT * BT * sizeof(dtype)
```

Benchmark shape: `npl=32`, `S_v=128`.

| Model | H | npp | BT | Ai dtype | Chunks | Ai scratch MiB | 3x Ai traffic MiB |
|-------|---|-----|----|----------|--------|----------------|-------------------|
| MoE | 32 | 512 | 32 | f32 | 16 | 64.0 | 192.0 |
| MoE | 32 | 512 | 32 | f16 | 16 | 32.0 | 96.0 |
| MoE | 32 | 512 | 64 | f32 | 8 | 128.0 | 384.0 |
| MoE | 32 | 512 | 64 | f16 | 8 | 64.0 | 192.0 |
| MoE | 32 | 2048 | 32 | f32 | 64 | 256.0 | 768.0 |
| MoE | 32 | 2048 | 32 | f16 | 64 | 128.0 | 384.0 |
| MoE | 32 | 2048 | 64 | f32 | 32 | 512.0 | 1536.0 |
| MoE | 32 | 2048 | 64 | f16 | 32 | 256.0 | 768.0 |
| Dense | 48 | 512 | 32 | f32 | 16 | 96.0 | 288.0 |
| Dense | 48 | 512 | 32 | f16 | 16 | 48.0 | 144.0 |
| Dense | 48 | 512 | 64 | f32 | 8 | 192.0 | 576.0 |
| Dense | 48 | 512 | 64 | f16 | 8 | 96.0 | 288.0 |
| Dense | 48 | 2048 | 32 | f32 | 64 | 384.0 | 1152.0 |
| Dense | 48 | 2048 | 32 | f16 | 64 | 192.0 | 576.0 |
| Dense | 48 | 2048 | 64 | f32 | 32 | 768.0 | 2304.0 |
| Dense | 48 | 2048 | 64 | f16 | 32 | 384.0 | 1152.0 |

`3x Ai traffic` means one Ai write plus two Ai reads for two value slabs.

## Interpretation

The f32 `BT=32` scratch path is large but plausible:

- Peak scratch is 256 MiB for MoE and 384 MiB for dense at `npp=2048,npl=32`.
- Ai traffic is 768 MiB for MoE and 1.125 GiB for dense per GDN layer call.
- This is not free on LPDDR5x, but it is not automatically worse than
  recomputing A/Ai in every value slab.

The f16/BF16 Ai path halves traffic but should not be first because Phase 10 and
Phase 11 showed correctness must be established before performance. The first
prototype should store Ai in f32, stay default-off, and use md5/KL gates before
trying a lossy Ai dtype.

## Decision

GO: Phase 13 should implement a default-off global-Ai scratch prototype.

Rationale:

- The only remaining C32 path that addresses Phase 10's failure is sharing A/Ai
  across value slabs.
- `BT=32` f32 scratch has acceptable peak memory for the existing GB10
  benchmark shapes.
- The implementation can be default-off and rejected cleanly if global scratch
  traffic or extra launch boundaries dominate.

Phase 13 constraints:

- Prototype only `BT=32`, f32 Ai, two `dv_tile=64` value slabs.
- Keep decode out via `GDN_CHUNK_MIN > 1`.
- Gate with `GATED_DELTA_NET`, canonical MoE/dense md5, and same-session A/B.
- If md5 changes, run KL before benchmarking.
- If the prototype is flat or slower, reject it and stop GDN kernel work on
  GB10; do not iterate into f16 Ai until f32 proves the schedule can win.

## Phase 13 Result

Phase 13 implemented the f32 Global-Ai32 prototype and rejected it.

Correctness:

- MoE md5: `8cb0ce23777bf55f92f63d0292c756b0`.
- Dense md5: `5951a5b4d624ce891e22ab5fca9bc439`.

Performance:

| Model | Mode | PP | S_PP t/s |
|-------|------|----|----------|
| MoE | M5 base | 2048 | 2425.10 |
| MoE | Global Ai32 | 2048 | 2097.76 |
| Dense | M5 base | 2048 | 1016.14 |
| Dense | Global Ai32 | 2048 | 918.19 |

Artifacts:

- `/home/mudler/bench/phase13_gdn_global_ai32/gates/`
- `/home/mudler/bench/phase13_gdn_global_ai32/ab/`
- `/home/mudler/bench/phase13_gdn_global_ai32/rejected/global_ai32_rejected.diff`

Final decision:

- Reject Global-Ai32.
- Stop GDN kernel work on GB10. The remaining vLLM GDN advantage is not
  reachable through the low-conflict C16/C32 patch shapes tested here.
