# PREFILL_GEMM_RESULTS - option (a) dequant->bf16 cuBLAS, measured on GB10

Companion to `PREFILL_GEMM_SCOPE.md`. This records the GPU A/B for the #1
prefill lever (route large-M NVFP4 dense GEMMs off FP4-MMQ onto dequant->bf16
cuBLAS / nvjet). Shipped as patch `0033`, **default-off** because the measured
result is a regression on this hardware.

Hardware: NVIDIA GB10 (sm_121), CUDA 13.0. Backend pin `9d5d882d`.
Models: `q36-27b-nvfp4.gguf` (dense), `q36-35b-a3b-nvfp4.gguf` (MoE).
Binary: `build-cuda/bin/llama-batched-bench -fa on -ngl 99`, `LLAMA_KV_PAGED=1`.
A/B is a single build toggled by `LLAMA_FP4_PREFILL_M` (0 = MMQ baseline, >0 =
route prefill M>threshold to bf16 cuBLAS), so it isolates exactly this lever.

## 1. Bit-exact / numeric gate (PASS - divergence benign)

| Gate | Result |
|---|---|
| `test-backend-ops -o MUL_MAT` (default, threshold off) | 1146/1146 pass |
| `test-backend-ops -o MUL_MAT_ID` (default) | 806/806 pass (MoE untouched) |
| `test-backend-ops -o MUL_MAT`, path FORCED (`LLAMA_FP4_PREFILL_M=64`) | NVFP4 large-M cases (m=2048/1600/2050, n=128, k=2048) green CUDA-vs-CPU |
| greedy md5, short prefill (< threshold), lever vs base | identical: `5951a5b4d624ce891e22ab5fca9bc439` (== documented dense reference; decode byte-untouched) |
| greedy md5, long prefill (> threshold, exercises bf16 path), lever vs base | identical: `5f3967df5781445feeb25762abb9eae7` (the new FP path flips no greedy argmax) |

The new path (NVFP4->bf16 round, bf16 tensor cores, f32 accumulate) is a
different FP path from fused FP4xQ8_1 MMQ, but it is precision-neutral-to-better:
keeping activations in bf16 instead of Q8_1 is strictly more precise, and the
greedy output is byte-identical. This matches the scope's prediction
(KLD(dequant-bf16 || f16) <= KLD(FP4-MMQ || f16)).

## 2. Performance (REGRESSION - the lever loses on GB10)

S_PP (prefill tokens/s), q36-27b dense, A/B `LLAMA_FP4_PREFILL_M` off vs on:

| prefill ubatch M | npl | base S_PP (MMQ) | lever S_PP (bf16 cuBLAS) | delta |
|---|---|---|---|---|
| 512  | 32 | 958.99  | 486.65 | -49% |
| 1024 | 8  | 1013.65 | 587.27 | -42% |
| 2048 | 8  | 918.46  | 649.42 | -29% |

Default-off control (no env): S_PP 966.98 == base (within noise) -> the patch is
inert by default.

## 3. Why it loses (the scope premise was wrong for GB10)

The scope assumed FP4-MMQ is register-bound to ~3% of FP4 peak at large M, so a
vendor large-M kernel would win. **Measured, FP4-MMQ at M=512..2048 beats
dequant->bf16 cuBLAS by 29-49%.** Two compounding reasons:

1. **bf16 tensor-core peak is ~half FP4 peak on GB10.** Even a perfect bf16 GEMM
   caps at ~half the throughput the FP4-MMA path can reach.
2. **The dequant tax is an un-amortized memory pass.** Per prefill step the new
   path reads FP4 weights (~0.5 B/elt), writes bf16 (2 B/elt), then the GEMM
   reads bf16 (2 B/elt) = ~8x the weight byte traffic of the FP4-MMQ read
   (~0.5 B/elt). The dequant write is M-independent, so it only amortizes as M
   grows: the gap shrinks 49% -> 42% -> 29% from M=512 -> 2048 but never crosses
   even at M=2048 (above the default n_ubatch).

This is also consistent with the README decode finding that the dense path was
already ~96-97% of vLLM - the dense GEMM was never the bottleneck the way the
prefill ground-truth (measured on the MoE decision model) implied.

## 4. Status of the phases

- **Phase 1 (dense): REJECTED on GB10**, landed default-off as a validated,
  env-gated scaffold (mechanism + bit-exact gate reusable by option (b) and by
  non-GB10 hardware where bf16 may fare differently).
- **Phase 2 (MoE grouped large-M): NOT implemented.** It inherits the same
  bf16-peak < FP4-peak ceiling plus a per-expert dequant, so a grouped
  bf16-cuBLAS would regress for the same reason; the MoE id-path also has the
  graph-safety catch (a false `should_use_mmq` falls to the host-sync sorted
  loop, not CUDA-graph-safe). Not worth the multi-day grouped-cuBLAS + graph
  work on a path the dense A/B already shows loses.
- **The only route to a real prefill GEMM win is option (b)** - a native
  Blackwell FP4-MMA large-M kernel (multi-week), to greenlight only if the
  prefill regime is funded. The committed scaffold gives option (b) its
  M-threshold routing and its bit-exact gate for free.
