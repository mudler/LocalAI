# MOE_GAP_VS_VLLM.md - ground-truth both-engine MoE decode decomposition (where vLLM's ~15% lives)

THE GPU AGENT (label `moe-gap-groundtruth`), DGX GB10 (sm_121). First **side-by-side, both-engine,
per-kernel ms/step** decomposition of the MoE decode gap. All prior B work decomposed llama ONLY; this
profiles vLLM's decode step too and computes the per-bucket `llama - vLLM` delta to pinpoint the gap.

Model `q36-35b-a3b-nvfp4` (40 layers: 30 GDN linear-attn + 10 full-attn, 256 experts top-8, vocab 248320).
Both engines profiled at **batch 128 decode** with `nsys --cuda-graph-trace=node`, steady-decode window,
per-step normalized by GDN-kernel-count / 30 (cross-checked vs flash/reshape_cache counts and throughput).

- **llama**: `build-cuda` tip `2f4f5ab` (patch 0025), `llama-batched-bench -npp 128 -ntg 128 -npl 128
  -c 32768 -fa on`, `LLAMA_KV_PAGED=1 LLAMA_MOE_FORCE_GRAPHS=1` (the re-graph ON = the 752 t/s ship point).
  Measured **S_TG = 752.3 t/s** => **step = 169.8 ms**, GPU-busy 97.5% (idle 2.5% = 4.2 ms/step).
- **vLLM 0.23.0**: `q36-35b-a3b-nvfp4-vllm`, **CUDA graphs ON** (`cudagraph_mode=FULL_AND_PIECEWISE`,
  the 882-reference config, NOT enforce_eager), MARLIN NvFp4 MoE, 128 seqs x 128-tok prompt x 128 gen.
  Measured **step = 142.0 ms** (= 901 t/s-equiv), GPU-busy 99.7% (idle 0.3% = 0.4 ms/step).
- Gap reproduced: **169.8 - 142.0 = 27.8 ms/step** (llama 83.6% of vLLM here; matches the ~85% server number).

## THE HEADLINE: the MoE grouped GEMM is NOT vLLM's advantage - it is a llama WIN

Grouped MoE-expert GEMM, isolated by per-call duration (LONG calls = the per-expert grouped GEMM):

| grouped MoE-expert GEMM | ms/step | what |
|-------------------------|--------:|------|
| **llama** `mul_mat_q<NVFP4,M-tile=64>` (+stream-k fixup + gather) | **48.3** | native Blackwell FP4-MMA **W4A4** |
| **vLLM** `marlin_moe_wna16::Marlin` | **50.0** | **W4A16** (FP4 weights dequant-in-kernel -> bf16 MMA) |

**llama's native-FP4 grouped GEMM is ~1.7 ms/step FASTER than vLLM's Marlin W4A16 at the ragged
tiny-M (~4 rows/expert) decode shape** (pure GEMM core 47.3 vs 50.0). Both read the same ~4-bit weight
bytes and are bandwidth-bound, so they tie to within a few %, and llama's 2x-rate FP4-MMA edges it.
**=> Marlin is NOT faster here; a Marlin-style W4A16 MoE GEMM in llama would make the MoE GEMM SLOWER.**
This directly answers the brief's load-bearing question #1/#2 and extends the prior `w4a16-marlin` DENSE
conclusion ("the win was NVFP4-dense-quant, not the Marlin kernel") to MoE: **the MoE GEMM kernel is not
the lever; llama already beats Marlin there.**

## Side-by-side per-step decomposition (ms/step, kernel-time attribution)

| bucket | llama ms | vLLM ms | Δ llama-vLLM | note |
|--------|---------:|--------:|-------------:|------|
| **Recurrence / SSM**           | **79.3** | **72.7** | **+6.6** | core kernel is a llama WIN (70.0 vs 71.1); the gap is llama's state-gather/conv plumbing |
| **MoE-expert grouped GEMM**    | 48.3 | 50.0 | **-1.7** | **llama FASTER** (native FP4-MMA W4A4 vs Marlin W4A16) |
| **Dense projections (+glue)**  | **20.3** | **13.8** | **+6.5** | llama runs GDN/attn projections in BF16 cublas; vLLM runs them as compact NVFP4-Marlin; +2.9 ms is llama's bf16<->f32 `convert_unary` glue vLLM never pays |
| **Norms / glue / memcpy**      | 9.6 | 6.0 | +3.6 | llama `k_bin_bcast` (expert-combine+residual) 4.3 + memcpy 2.4 heavier |
| **Act-quant (W4A4 tax)**       | 3.3 | 0.0 | **+3.3** | `quantize_mmq_nvfp4`; vLLM W4A16 keeps acts bf16 => structurally ZERO |
| **Router / align**             | 2.4 | 0.5 | +1.9 | llama computes router via a full FP4 GEMM (1.6) + argsort/scatter; vLLM fuses topk/align |
| **Attention (full-attn)**      | 2.8 | 2.6 | +0.2 | parity |
| kernel-time subtotal           | 166.1 | 145.7 | +20.4 | |
| **GPU idle (host bubble)**     | 4.2 | 0.4 | **+3.8** | graph coverage: llama partially-graphed (0025) vs vLLM FULL_AND_PIECEWISE |
| cross-stream overlap (union<sumdur) | ~0.8 | ~4.0 | ~-3.2 (vLLM overlaps more) | vLLM runs more kernels concurrently |
| **STEP TOTAL (wall)**          | **169.8** | **142.0** | **+27.8** | |

### Per-engine top kernels (ms/step)

```
llama (752 t/s, step 169.8 ms, 97.5% busy)        vLLM (901-equiv, step 142.0 ms, 99.7% busy)
 70.0  gated_delta_net_cuda          REC core      71.1  fused_recurrent_gated_delta   REC core
 47.3  mul_mat_q grouped MoE (M=64)  MoE GEMM       50.0  marlin_moe_wna16::Marlin      MoE GEMM
  8.2  nvjet 192x136 (bf16 proj)     PROJ            4.0  nvjet 128x72 (bf16 proj)      PROJ
  5.2  k_get_rows_float  REC-GATHER  REC <-- vLLM    2.8  marlin dense (lm_head NVFP4)  PROJ
  4.5  cutlass::Kernel2 (bf16 GEMM)  PROJ           has   2.7  nvjet 128x64 (bf16 proj)  PROJ
  4.3  k_bin_bcast (combine+resid)   GLUE           no    2.5  flash_fwd_splitkv         ATTN
  4.1  nvjet 128x64 (bf16 proj)      PROJ           equiv 2.0  marlin dense small (NVFP4) PROJ
  3.4  ssm_conv_update_f32           REC            of    1.6  causal_conv1d_update      REC
  3.3  quantize_mmq_nvfp4  W4A4 TAX   ACTQ <-- vLLM  these 1.4  std::enable_if (glue)     GLUE
  2.9  convert_unary bf16<->f32      PROJ-GLUE <--   two   1.2  reduce_kernel             GLUE
  2.8  flash_attn_tile               ATTN           (5.2+  1.0  cutlass::device (fp8 lin) PROJ
  2.4  MEMCPY-Device (SSM state)     GLUE           2.9 =  0.8  nvjet 32x64               PROJ
  1.6  mul_mat_q router (M=128)      ROUTER          8 ms  0.4  act_and_mul (SwiGLU)      GLUE
  1.5  rms_norm_f32                  GLUE           pure   0.2  topkGating / moe_align    ROUTE
  ...                                               llama  0.1  reshape_and_cache_flash   ATTN
                                                     tax)
```

## WHERE THE 27.8 ms ACTUALLY IS (ranked) - and it is NOT the Marlin GEMM

1. **Dense projections + bf16<->f32 glue: +6.5 ms.** llama keeps the GDN/attn linear projections (and
   the lm_head) in **BF16** (cublas `nvjet`/`cutlass`, full-precision weight reads) and pays a 2.9 ms
   `convert_unary` bf16<->f32 tax around them; vLLM runs the same projections as **compact NVFP4-Marlin
   W4A16** (4-bit weight read, ~4x less BW) and stays bf16 end-to-end (no convert). This is the
   **`NVFP4-dense-quant` lever the prior `w4a16-marlin` project already identified - applied to the
   still-bf16 projections**, not the MoE GEMM.
2. **Recurrence state-gather/conv plumbing: +6.6 ms.** The recurrence CORE kernel is a **llama win**
   (gated_delta_net 70.0 vs vLLM fused_recurrent 71.1, confirming "past vLLM on BW efficiency"). The gap
   is entirely the surrounding plumbing: **`k_get_rows_float` 5.2 ms (the recurrent-state gather)** +
   `ssm_conv_update` 3.4 vs vLLM's single `causal_conv1d_update` 1.6. vLLM has **no gather** - its
   recurrent state is updated in-place inside the fused decode kernel. `k_get_rows` is the single biggest
   llama-specific kernel vLLM has no equivalent of.
3. **Graph coverage + stream overlap: ~+7.0 ms combined** (idle +3.8, cross-stream overlap ~+3.2). vLLM
   FULL_AND_PIECEWISE is 99.7% busy with more concurrent kernels; llama (partially graphed post-0025) is
   97.5% busy with thinner overlap.
4. **W4A4 act-quant tax: +3.3 ms.** `quantize_mmq_nvfp4`; vLLM's W4A16 choice makes this structurally 0.
   Fusing the quant into the preceding op (as vLLM fuses act_quant into RMSNorm/SiLU) would erase it.
5. **Router GEMM + norms/glue: +5.4 ms.** llama computes router logits via a full FP4 GEMM (1.6) and has
   heavier `k_bin_bcast` combine/residual + memcpy; vLLM fuses routing into tiny topk/align kernels.

## THE SINGLE BIGGEST vLLM-MoE ADVANTAGE

**Not the Marlin GEMM.** It is a near-tie between two ~6.5 ms buckets, both bf16-precision-related:
- **Dense projections (+6.5 ms)** - vLLM runs the GDN/attn projections + lm_head as NVFP4-Marlin while
  llama runs them BF16 + a 2.9 ms convert tax. Single biggest *bucket* delta.
- **Recurrent-state gather (+5.2 ms, kernel `k_get_rows_float`)** - the single biggest *kernel* vLLM
  avoids entirely (in-place fused state vs llama's separate gather). Plus +1.8 ms more REC plumbing.

The MoE grouped GEMM (the brief's hypothesis) is a **-1.7 ms llama win**, so it is explicitly ruled out.

## ANSWERS TO THE BRIEF

1. **WHERE is vLLM's 15%?** Spread across bf16-projection BW (+6.5) + recurrence state-gather plumbing
   (+6.6) + graph/overlap (+7.0) + act-quant tax (+3.3) + router/glue (+5.4). **NOT the MoE GEMM.**
2. **Is Marlin faster at tiny-M decode?** **No.** llama native FP4-MMA W4A4 = 47.3 ms vs Marlin W4A16 =
   50.0 ms. Marlin is ~5% slower here; both are at the LPDDR5x BW floor.
3. **Should llama implement a Marlin-style W4A16 MoE GEMM?** **No** - it would slow the MoE GEMM and is
   not where the gap lives. The `w4a16-marlin` DENSE verdict ("NVFP4-dense-quant, not the Marlin kernel")
   carries to MoE. The real, ordered levers are: **(a) NVFP4-quantize the still-bf16 GDN/attn projections
   + lm_head** (close ~+6.5, the largest, bit-changing but the same class of move vLLM makes); **(b) fuse
   away the recurrent-state gather `k_get_rows`** (~+5, bit-exact, the biggest single-kernel win);
   **(c) fuller CUDA-graph coverage + stream overlap** (~+7, bit-exact); **(d) fuse the W4A4 act-quant
   into the preceding op** (+3.3, bit-exact). None of these is a new MoE GEMM.

Assisted-by: Claude:opus-4.8 [Claude Code]
