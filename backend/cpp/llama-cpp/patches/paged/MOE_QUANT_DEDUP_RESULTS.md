# MOE_QUANT_DEDUP_RESULTS.md - patch 0023 (qwen35moe NVFP4 activation-quantize de-dup)

Bit-exact MoE decode/prefill lever. Built + measured on DGX GB10 (sm_121a) on top of HEAD
8a3229f (patch 0022). Companion analysis: NONRECURRENCE_BITEXACT.md (section "nonrec-build").

## What

ggml `mul_mat_id` quantizes the EXPERT-GATHERED activation rows: it allocates
`ne11_flat = ne12 * n_expert_used` rows and quantizes each via `quantize_mmq_nvfp4(..., ids_src1)`.
For the broadcast up/gate projections the activation is the per-token hidden state, the SAME for
every expert that token routes to (`ne11 == 1`). So the stock path re-quantizes each token
`n_expert_used` times (4x for q36-35b-a3b).

`quantize_mmq_nvfp4` computes each `block_fp4_mmq` as a pure per-thread function of its 16
consecutive inputs (per-thread amax, the +/-2 ue4m3 search, the e2m1 packing - NO cross-thread
shfl/reduction). So the quantized block for a given token is byte-identical no matter which
expert slot it lands in.

## Lever

When `ne11 == 1` (broadcast up/gate):
1. Quantize the `ne12` UNIQUE token activations once into a compact buffer
   (`quantize_mmq_fp4_cuda(src1_d, nullptr, ..., ne12, 1, 1)`, row stride `s12`).
2. Gather the `block_fp4_mmq` rows into the expert-gathered layout keyed by `ids_src1`
   (`gather_mmq_fp4`): `block_fp4_mmq == 9 * uint4 == 144 B`, copied with a coalesced uint4
   kernel whose output is written fully contiguously (`gathered[t] = unique[ib_u*9 + w]`).

Pure byte copy of identical blocks => the gathered buffer is byte-for-byte identical to
re-quantizing each gathered row. The MMQ GEMM is UNTOUCHED. `down_proj`
(`ne11 == n_expert_used`, distinct per expert) keeps the stock re-quantize path.

The first gather draft (one thread copies one 144 B struct, scattered) was uncoalesced and cost
478 ms - it ate 84% of the quantize saving and decode stayed flat. The shipped coalesced-uint4
gather costs 32 ms.

## Measurements (q36-35b-a3b-nvfp4 dense=q36-27b-nvfp4, -fa on, -npp 128 -ntg 128)

nsys decode-isolated (`--cuda-graph-trace=node`, npp8 ntg128 npl128), per-run kernel sums:
| kernel                | dedup off | dedup on |
|-----------------------|-----------|----------|
| quantize_mmq_nvfp4    | 868 ms    | 457 ms   |
| gather_mmq_fp4        | -         | 32 ms    |
| net quantize path     | 868 ms    | 489 ms   |  (-379 ms decode GPU-time)
| gated_delta_net (50%) | unchanged | unchanged |
| mul_mat_q<NVFP4>      | unchanged | unchanged |

Decode S_TG (t/s), back-to-back same-build A/B (default-on vs GGML_CUDA_MOE_QUANT_DEDUP=0):
| model           | npl32 off->on    | npl128 off->on        |
|-----------------|------------------|-----------------------|
| MoE q36-35b-a3b | 440.3 -> 442.8 (+0.6%) | 745.2 -> 758.1 (+1.73%) |
| dense q36-27b   | 207.4 -> 206.9 (flat)  | 373.28 -> 373.24 (byte-flat) |

Prefill: MoE T_PP 7.69 -> 7.38 s (~ -4% time). Dense unaffected (no `mul_mat_id`).

## Bit-exact gate (greedy --temp 0 --seed 1 md5, byte-identical to 0022)

| model            | md5 (default on)                     | == 0022 |
|------------------|--------------------------------------|---------|
| q36-27b-nvfp4    | 5951a5b4d624ce891e22ab5fca9bc439     | yes (dense untouched) |
| q36-35b-a3b-nvfp4| 07db32c2bcb78d17a43ed18bc22705cd     | yes (on == off == 0022) |

test-backend-ops: MUL_MAT 1115/1115, MUL_MAT_ID 805/805 (default on).

## Knob

On by default. `GGML_CUDA_MOE_QUANT_DEDUP=0` restores the stock per-expert re-quantize path
(byte-identical output, used as the A/B baseline).

Commits: DGX dev tree f7409c2; worktree patch `0023-qwen35moe-nvfp4-quant-dedup.patch`.

Assisted-by: Claude:opus-4.8 [Claude Code]
