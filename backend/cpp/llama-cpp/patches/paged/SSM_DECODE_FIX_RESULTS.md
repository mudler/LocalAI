# SSM decode fix - qwen35 gated-DeltaNet in-place recurrent-state write-back (patch 0018)

Follow-up to `A2_CUDAGRAPH_DECODE.md`. That analysis located the real decode lever
on the Qwen3.6 hybrid-SSM models (arch `qwen35`, 48 gated-DeltaNet linear-attn
layers : 16 full-attn layers) and ruled out the FP4 GEMM, CUDA graphs, the host
loop, and attention. The corrected per-kernel + per-memcpy decode decomposition
attributed ~67% of decode GPU time to SSM-state plumbing:

    gated_delta_net 23.4% | get_rows state-gather 21.9% | D2D state-copy 18.9% (= ~67%)
    FP4 matmul ~28% | full attention 0.4%

Root cause: per SSM layer per step the fused `gated_delta_net` op wrote its new
recurrent state into graph scratch, then a **separate `ggml_cpy` persisted the
full ~225 MB state into the recurrent-state cache** (1584 D2D ops, 356 GB, 18.9%
of decode over the profile window). vLLM's `fused_recurrent_gated_delta_rule`
keeps the state in place (no copy).

## STEP 1 (this patch): kill the per-layer D2D state copy-back

`ggml_gated_delta_net_inplace` (new builder, `src[6] = state_dst`) makes the op
write its final recurrent state **directly into the active sequences' contiguous
cache slot** (at `kv_head`), eliminating the copy-back. The op output then carries
only the attention scores. SSM arithmetic is unchanged - only the destination
pointer of the final-state write moved.

- `ggml/include/ggml.h`, `ggml/src/ggml.c`: new `ggml_gated_delta_net_inplace` op
  builder. `dst` retains the same `[attn | state]` layout so the attention-output
  view is identical; the state region is left unused.
- `ggml/src/ggml-cuda/gated_delta_net.cu`: kernel/launch/op-handler thread an
  optional `state_dst`; final-state (`!keep_rs`) write targets it when present.
- `ggml/src/ggml-cpu/ops.cpp`: K==1 path operates in place on the `state_dst`
  cache view (kept CPU-correct for non-CUDA runs / CI).
- `src/models/delta-net-base.cpp`: `build_recurrent_attn` uses the in-place op on
  the fused decode/prefill path and drops the `ggml_cpy`. The rollback path
  (`n_rs_seq > 0`) is unchanged. The get_rows state gather is unchanged (STEP 2).

### Correctness gate

- **Bit-identical**: greedy (`--temp 0 --seed 1`) `llama-completion` output on
  `q36-27b-nvfp4` is byte-for-byte identical between the copy-back baseline and the
  in-place build (`diff` -> IDENTICAL).
- **Coherent**: dense + MoE multi-paragraph greedy generations are on-topic and
  correct (Rayleigh scattering; Roman Empire 27 BCE / Actium 31 BCE; primes;
  additive vs subtractive color).
- Gated to the `qwen35` / gated-DeltaNet fused path; rollback and all non-SSM
  archs untouched (they never construct the in-place op).

### Measured decode_agg (`S_TG t/s`, npp 128, ntg 128, -fa on, paged on, fusion off)

Dense `q36-27b-nvfp4`:

| npl | baseline | in-place | delta   | % of vLLM (391 @128) |
|-----|----------|----------|---------|----------------------|
| 32  | 113.74   | 136.39   | +19.9%  | -                    |
| 128 | 146.23   | 180.53   | +23.5%  | 37.4% -> 46.2%       |

The npl-128 result lands on the predicted copy-removal ceiling (~180 t/s).

MoE `q36-35b-a3b-nvfp4`:

| npl | baseline | in-place | delta   |
|-----|----------|----------|---------|
| 32  | 246.79   | 279.41   | +13.2%  |
| 128 | 313.36   | 372.62   | +18.9%  |

### nsys confirmation (npp 128, ntg 24, npl 128, fusion off, eager)

The D2D state-copy bucket collapsed:

| bucket            | before              | after                |
|-------------------|---------------------|----------------------|
| MEMCPY D2D        | 18.9% / 356 GB / 1584 ops | 0.23% / 2.93 GB / 734 ops |

The ~225 MB/copy recurrent-state copy-back is gone (122x fewer D2D bytes); the
residual D2D is the small conv-state copies. With it removed, the remaining decode
buckets are `gated_delta_net` 26.0%, FP4 matmul ~37.5%, and `get_rows` state
gather 18.8%.

## STEP 2 (not in this patch): fuse the get_rows state gather

The state gather is now the largest single non-GEMM bucket (18.8%). It is a pure
materialization: `build_rs` calls `ggml_get_rows(cache, s_copy_main)` to copy each
sequence's previous state into a contiguous scratch tensor before the op reads it.
`ggml_ssm_scan` already avoids this by taking the `ids` tensor (`src[6]`) and
reading the per-seq state directly from the full cache. The same fusion applies
here: give `ggml_gated_delta_net` an `ids` source, read `curr_state` from
`cache + ids[seq]*D` in the kernel, and pass the full cache via the `build_rs`
`get_state_rows` lambda (mirroring `mamba-base.cpp`). Predicted ceiling with both
steps: ~247 t/s (~63% of vLLM dense @128), GEMM untouched.

## Verdict on the path to parity

STEP 1 removes ~half of the SSM plumbing overhead and is the dominant, lowest-risk
lever; it is bit-exact and shipped here. STEP 2 (gather fusion) has a proven ggml
precedent (`ssm_scan` `ids`) and is the clear next move. The residual gap to vLLM
after both SSM steps is the FP4 GEMM (~37% of decode), which is a separate kernel
track. No paged/graph/block-table change can move decode on this model (full
attention is 0.4% of decode).
