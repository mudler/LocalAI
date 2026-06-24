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

## STEP 2 (patch 0019): fuse the recurrent-state gather into the op

After Step 1 the largest non-GEMM decode bucket was the recurrent-state
`get_rows` gather (18.8% of decode GPU time): `build_rs` materialized each
sequence's prior state into a contiguous scratch via `ggml_get_rows` before the
gated-DeltaNet op read it. Step 2 eliminates that materialization, mirroring
`ggml_ssm_scan`'s `ids` source.

`ggml_gated_delta_net_inplace_ids` takes the FULL recurrent-state cache plus the
`s_copy` ids (`src[5]` = full cache `[S_v, S_v, H, n_rs_slots]`, `src[7]` = ids,
`op_param[1]` = `rs_head`) and reads each sequence's prior state directly from
`cache[ids[seq]]`. Combined with Step 1's in-place write the op now reads AND
writes the cache directly: no recurrent-state materialization at all. The
`build_recurrent_attn` fused path feeds the full cache and ids through the
`build_rs` `get_state_rows` lambda exactly like `mamba-base.cpp`, keeping the
`rs_zero` clear and the extra-states copy around the op.

### Race-free by construction (CUDA)

In-place write plus an ids read of the same cache is only safe when the read slot
equals the write slot. `s_copy(s) = cells[s + head].src0`, which is identity
(`rs_head + s`) for stable continuing sequences (the entire AR decode path) but
can remap on sequence reorder or `rs_zero` (e.g. multiple new sequences in one
prefill ubatch). The kernel handles both per (seq, head) block on device:

- identity sequences read `s0` in place from the destination slot `state_dst`
  (the kernel loads all of `s0` into registers before it writes the new state,
  so reading and writing the same slot is race-free) -- no materialization;
- non-identity sequences read from a disjoint scratch that a small
  `gdn_gather_nonident_kernel` copies from `cache[ids[seq]]` first, so the
  recurrence never reads a slot another block writes.

`ids` stays a device pointer (dereferenced only in the kernels; the input is
device-resident at op-execute time, so a host read segfaults). The CPU op
mirrors the same logic (host identity check + a serial gather in the dispatcher
for the non-identity case). The math is unchanged, so the result is bit-identical
to the `get_rows` path in every case.

Gated to the `qwen35` / `qwen35moe` fused decode/prefill path; `qwen3next`,
`kimi-linear`, the non-fused path and the rollback (`n_rs_seq > 0`) path are
untouched (they keep the materialized-state overload).

### Measured decode_agg (`S_TG` t/s, npp 128, ntg 128, -fa on, paged on, fusion off)

Dense `q36-27b-nvfp4`:

| npl | Step 1 (baseline) | Step 2   | delta   | % of vLLM (391 @128) |
|-----|-------------------|----------|---------|----------------------|
| 32  | 137.64            | 170.68   | +24.0%  | -                    |
| 128 | 186.25            | 256.57   | +37.8%  | 47.6% -> 65.6%       |

The npl-128 result (256.57 t/s) beats the predicted ~247 t/s Step-2 ceiling.

MoE `q36-35b-a3b-nvfp4`:

| npl | Step 1 (baseline) | Step 2   | delta   |
|-----|-------------------|----------|---------|
| 32  | 299.68            | 366.69   | +22.4%  |
| 128 | 409.30            | 553.63   | +35.3%  |

(Step-1 baselines re-measured in the same session; the brief's reference figures
were 136 / 180 dense and 279 / 373 MoE.)

### Bit-exact gate

Greedy (`--temp 0 --seed 1`) `llama-completion` output (256 tokens, paged on,
fusion off) vs the Step-1 build:

- dense `q36-27b-nvfp4`: model text byte-identical (md5 match);
- MoE `q36-35b-a3b-nvfp4`: byte-identical;
- Step-2 dense run1 == run2 (deterministic, no race).

### nsys confirmation (npp 128, ntg 24, npl 128, fusion off, eager)

The recurrent-state gather bucket collapsed:

| kernel                     | Step 1   | Step 2                                  |
|----------------------------|----------|-----------------------------------------|
| `k_get_rows_float`         | 18.8%    | 0.7% (residual: embeddings / conv-state)|
| `gdn_gather_nonident`      | -        | 1.7% (no-op at decode, median ~1.2 us)  |
| `gated_delta_net_cuda`     | 26.0%    | 22.5%                                    |
| FP4 GEMM family            | ~37.5%   | ~48% (now the dominant residual)        |

The SSM state gather is effectively eliminated. The residual decode gap to vLLM
is now the FP4 GEMM (~48% of decode), a separate kernel track.
