# Patch 0028: qwen35 recurrent-state gather fusion (Lever 1, bit-exact)

The MoE-gap groundtruth (`MOE_GAP_VS_VLLM.md`) found `k_get_rows_float` to be the single biggest
kernel vLLM has no equivalent of (~5.2 ms/step MoE decode; also present in dense): vLLM updates its
gated-DeltaNet recurrent state in-place inside the fused decode kernel, while llama ran a separate
`ggml_get_rows` gather. Patch 0019 fused the SSM recurrent-state gather; patch 0021 fused the conv
compute/write-back but KEPT a `build_rs` gather for the conv taps ("tiny; not one of the eliminated
buckets"). This patch closes that residual.

## Which gather was still firing (nsys-located, DGX GB10 sm_121)

Profiled MoE `q36-35b-a3b-nvfp4` at batch-128 decode (`llama-batched-bench -npp128 -ntg24 -npl128
-fa on`, `LLAMA_KV_PAGED=1 LLAMA_MOE_FORCE_GRAPHS=1`). The decode-window `k_get_rows_float<float,float>`
distribution was bimodal: a BIG cluster of **~720 instances (= 30 GDN layers x 24 decode steps) at
~115 us each** plus small embedding/router gathers.

The big gather's geometry (`grid=(ne10=128, block_num_y=96, 1)`) decodes to **128 rows (= n_seqs
active sequences) of ne00 = 24576 floats**. With the model's real dims (`d_conv=4, d_inner=4096,
n_group=16, d_state=128`):
- `n_embd_r = (d_conv-1) * (d_inner + 2*n_group*d_state) = 3 * 8192 = 24576` -> `block_num_y=96` EXACT match.
- `n_embd_s = d_state * d_inner = 524288` (the SSM state, gridY 2048 - already fused by 0019).

So the residual `k_get_rows` is the **conv-state tap gather** in `build_conv_state_fused`
(`src/models/delta-net-base.cpp`), which called the plain 4-arg `build_rs` -> `ggml_get_rows` of the
24576-float conv-state row x 128 sequences, once per GDN layer per decode step (~3.4 ms/step here,
~5.2 ms/step at steady ntg=128). The SSM-state gather is already fused, so this conv gather is the
last `k_get_rows` in the GDN decode path.

## What changed (mirror of the 0019 SSM gather fusion; bit-exact by construction)

New op `ggml_ssm_conv_update_inplace_ids` (reuses `GGML_OP_SSM_CONV`, discriminated by a non-null
`src[4]` = ids). Instead of a pre-gathered tap scratch, it takes the FULL conv-state cache (`src[0]`)
plus the per-sequence `ids` (= the recurrent-state `s_copy`, `src[4]`; `op_params[1]=rs_head`) and
reads each active sequence's prior K-1 taps directly from `cache[ids[s]]` in the kernel. This removes
the separate `k_get_rows` launch.

Race-free, exactly mirroring 0019:
- **Identity** sequences (`ids[s] == rs_head + s`, the whole AR-decode path) read the taps in place
  from the `conv_state_dst` write slot. The kernel loads the full conv window into registers before
  it writes the 1-token-shifted ring back, so read==write slot is race-free per (channel, seq) thread.
- **Non-identity** sequences (reorder / `rs_zero` remap at a prefill->decode boundary) are gathered
  into a disjoint scratch by a small `ssm_conv_gather_nonident_kernel` first (no-op at steady decode),
  so the update kernel never reads a slot another block writes.

The read VALUES are unchanged (identity in-place taps == the gathered taps == `cache[ids[s]]`); only
the read PATH changes from a `ggml_get_rows` materialization to an indexed in-kernel read. The conv
math, ascending-tap FMA order, silu and the ring write-back are byte-identical to 0021.

Files:
- `ggml/include/ggml.h`, `ggml/src/ggml.c`: `ggml_ssm_conv_update_inplace_ids` builder
  (src[0]=full cache [K-1,channels,n_cells], src[1]=conv_kernel, src[2]=x_cur, src[3]=conv_state_dst,
  src[4]=ids; op_params[0]=fuse_silu, op_params[1]=rs_head).
- `ggml/src/ggml-cuda/ssm-conv.cu`: `ssm_conv_gather_nonident_kernel` + `ssm_conv_update_ids_f32`
  kernel + `ggml_cuda_op_ssm_conv_update_ids` + a `src[4]`-discriminated branch in `ggml_cuda_op_ssm_conv`.
- `ggml/src/ggml-cpu/ops.cpp`: `ggml_compute_forward_ssm_conv_update_ids_f32` (window copied to a
  local before the possibly-aliasing write) + dispatch branch.
- `src/models/delta-net-base.cpp`: `build_conv_state_fused` now feeds the FULL cache + ids through the
  `build_rs` `get_state_rows` lambda (the rs_zero clear + extra-states copy still run around it),
  exactly like the 0019 recurrent-attn fusion. The `qwen35` / `qwen35moe` / `qwen3next` callers are
  unchanged (they already route the single-token decode path here).
- `tests/test-backend-ops.cpp`: `test_ssm_conv_update_ids` (16 cases) - ids = a shuffled permutation
  with `rs_head=0`, so each case exercises BOTH the identity in-place read and the non-identity cache
  read; validates the conv+silu output vs the CPU reference.

## GATE: test-backend-ops (CUDA0 vs CPU, 2/2 backends)

- SSM_CONV_UPDATE_IDS: OK (NEW; d_conv 3/4 x channels 256/3328 x n_seqs 1/4/32/128)
- SSM_CONV_UPDATE: OK (0021 path intact)
- SSM_CONV: OK
- GATED_DELTA_NET: OK
- GET_ROWS: OK

## GATE: greedy bit-exactness (--temp 0 --seed 1 -n 48, -fa on) - BOTH models BYTE-IDENTICAL

| model              | baseline md5                     | 0028 md5                         | result          |
|--------------------|----------------------------------|----------------------------------|-----------------|
| q36-27b-nvfp4      | 5951a5b4d624ce891e22ab5fca9bc439 | 5951a5b4d624ce891e22ab5fca9bc439 | BYTE-IDENTICAL  |
| q36-35b-a3b-nvfp4  | 07db32c2bcb78d17a43ed18bc22705cd | 07db32c2bcb78d17a43ed18bc22705cd | BYTE-IDENTICAL  |

(Built on the `paged` branch f32-default = 0026 hybrid default is f32; the baseline was re-confirmed
on the same build before the edit.)

## nsys proof - the gather is eliminated (MoE decode, npp128 ntg24 npl128, same window)

| kernel                              | before        | after                         |
|-------------------------------------|---------------|-------------------------------|
| `k_get_rows_float<float,float>` cnt | 10174         | 9454 (720 fewer = 30 GDN x 24)|
| `k_get_rows_float<float,float>` sum | 186.3 ms      | 102.8 ms (-83.5 ms)           |
| conv update kernel                  | `ssm_conv_update_f32` 720 | `ssm_conv_update_ids_f32` 720 |
| `ssm_conv_gather_nonident_kernel`   | -             | 720 x ~1.1 us = 0.8 ms (no-op at decode) |

The 720 big ~115 us conv gathers are gone; the only added work is a ~1.1 us no-op gather kernel per
layer-step (all sequences identity during steady AR decode). This matches 0019's "no-op at decode,
median ~1.2 us" non-identity gather.

## Preliminary throughput (post-fusion, single point; rigorous A/B is the bench phase)

- MoE `q36-35b-a3b-nvfp4` npl128 (`LLAMA_KV_PAGED=1 LLAMA_MOE_FORCE_GRAPHS=1`): **783.9 t/s**, step
  163.3 ms/step (MOE_GAP @0025 was 752.3 t/s / 169.8 ms/step => -6.5 ms/step in this stack).
- dense `q36-27b-nvfp4` npl128: **377.3 t/s** (~96% of vLLM 391; includes 0022/0026 base gains).
- npl128 ran clean (EXIT=0) on both - the non-identity boundary path does not crash.

## Verdict

Bit-exact (both md5 gates byte-identical, all test-backend-ops pass), the residual `k_get_rows` conv
gather is eliminated (nsys-confirmed), and decode throughput improves. Helps BOTH dense and MoE (the
shared GDN conv path). This closes the last `k_get_rows` in the GDN decode path (after 0019 SSM-state
+ 0021 conv compute). Additive and risk-free; ready for the rigorous same-session A/B bench.

Assisted-by: Claude:opus-4.8 [Claude Code]

## Rigorous same-session A/B bench (label gather-bench, DGX GB10 sm_121)

Independently re-validated on a fresh GPU session. `build-cuda-base` = pre-lever-1 (0026, 33e7c65;
NO `ssm_conv_update_ids` symbol) vs `build-cuda` = lever-1 (this commit; WITH it). Same env, back-to-back.

### Gate re-confirm (greedy --temp 0 --seed 1 -n 48, -fa on) - base == lever1 == recorded baseline

| model             | base (0026)                      | lever1 (0028)                    | recorded baseline                |
|-------------------|----------------------------------|----------------------------------|----------------------------------|
| q36-27b-nvfp4     | 5951a5b4d624ce891e22ab5fca9bc439 | 5951a5b4d624ce891e22ab5fca9bc439 | 5951a5b4d624ce891e22ab5fca9bc439 |
| q36-35b-a3b-nvfp4 | 07db32c2bcb78d17a43ed18bc22705cd | 07db32c2bcb78d17a43ed18bc22705cd | 07db32c2bcb78d17a43ed18bc22705cd |

test-backend-ops (CUDA0): SSM_CONV_UPDATE_IDS 16/16, SSM_CONV_UPDATE 16/16, SSM_CONV 45/45,
GATED_DELTA_NET 84/84, GET_ROWS 47/47 - all OK.

### decode_agg (S_TG t/s) before/after, npp128 ntg128 -npl 32,128 -c 33000

dense q36-27b-nvfp4 (LLAMA_KV_PAGED=1):

| npl | base S_TG | lever1 S_TG | delta  | vs vLLM 391    |
|-----|-----------|-------------|--------|----------------|
| 32  | 208.56    | 209.39      | +0.40% | -              |
| 128 | 369.95    | 377.83      | +2.13% | 94.6% -> 96.6% |

MoE q36-35b-a3b-nvfp4 (LLAMA_KV_PAGED=1 LLAMA_MOE_FORCE_GRAPHS=1):

| npl | base S_TG | lever1 S_TG | delta  | vs vLLM 901    |
|-----|-----------|-------------|--------|----------------|
| 32  | 456.85    | 459.56      | +0.59% | -              |
| 128 | 763.47    | 777.95      | +1.90% | 84.7% -> 86.3% |

Step time npl128: dense 346.0 -> 338.8 ms/batch-step, MoE 167.7 -> 164.5 ms/step (-3.13 ms/step).

### nsys (MoE decode, npp128 ntg64 npl128, same env) - k_get_rows eliminated

| kernel                          | base (0026)            | lever1 (0028)                                |
|---------------------------------|------------------------|----------------------------------------------|
| k_get_rows_float<float,float>   | 17334 inst / 358.37 ms | 15414 inst / 133.52 ms                       |
| delta                           |                        | -1920 inst (= 30 GDN x 64 steps), -224.85 ms |
| ssm_conv_update(_ids)_f32       | 219.71 ms (update)     | 225.75 ms (update_ids, +6 ms)                |
| ssm_conv_gather_nonident_kernel | -                      | 1920 x ~1.13 us = 2.17 ms (no-op, all ident) |

The 1920 big ~114 us conv-tap gathers are gone; only the ~1.13 us no-op gather kernel + ~6 ms folded
into the update kernel are added. Net GDN get_rows saving ~216 ms / 64 steps = ~3.4 ms/step, matching
the -3.13 ms/step throughput delta at npl128.

### Verdict (gather-bench)

Bit-exact (gate re-confirmed, both md5 byte-identical to baseline), the residual k_get_rows conv
gather is independently nsys-confirmed eliminated (-1920 inst, -224.85 ms over 64 steps), and decode
throughput lifts BOTH models in the same session: dense npl128 +2.13% (94.6 -> 96.6% of vLLM),
MoE npl128 +1.90% (84.7 -> 86.3% of vLLM). Ship it.
