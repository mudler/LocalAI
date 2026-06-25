# Patch 0021: qwen35 decode conv-state in-place fusion (no-regret, bit-exact)

The no-regret conv-state cleanup from the GDN_RECURRENCE_BYTE_GATE design, point (3).
After the recurrence byte-gate (NO-BUILD: the GDN recurrence is already single-pass at
the f32 byte floor), the conv path was the only remaining bit-exact decode lever.

## What changed

A new fused op `ggml_ssm_conv_update_inplace` (reuses `GGML_OP_SSM_CONV`, discriminated by a
non-null `src[3]`) that, on the single-token decode path, replaces the four-op conv chain:

    qkv_mixed transpose -> ggml_concat (build width-K window)   [concat_cont 8.14 ms/step]
    -> ggml_ssm_conv (depthwise conv)                           [ssm_conv_f32 ~8.6 ms/step]
    -> ggml_silu                                                [folded into ssm_conv on CUDA]
    -> ggml_cpy of the shifted ring state into the conv cache   [cpy_scalar 5.76 ms/step]

with ONE kernel that, per (channel, sequence), assembles the width-K window in registers from
the K-1 cached taps + the current `qkv_mixed` token, computes the depthwise conv with the SAME
ascending-tap FMA order as `ssm_conv_f32` at i==0, folds silu, writes the conv output, and writes
the 1-token-shifted ring state back IN PLACE into the conv cache slot at kv_head (the exact slot
the baseline `ggml_cpy` wrote). Mirrors the 0018 in-place write-back + 0019 patterns. This is
vLLM's `causal_conv1d_update`.

Files:
- `ggml/include/ggml.h`, `ggml/src/ggml.c`: new builder `ggml_ssm_conv_update_inplace`
  (src[0]=conv_states [K-1,channels,n_seqs], src[1]=conv_kernel, src[2]=x_cur [channels,1,n_seqs],
  src[3]=conv_state_dst [(K-1)*channels,n_seqs] in-place ring; op_params[0]=fuse_silu).
- `ggml/src/ggml-cuda/ssm-conv.cu`: kernel `ssm_conv_update_f32<apply_silu,d_conv>` (one thread per
  (channel,seq)) + `ggml_cuda_op_ssm_conv_update` + a `src[3]`-discriminated branch at the top of
  `ggml_cuda_op_ssm_conv`.
- `ggml/src/ggml-cpu/ops.cpp`: `ggml_compute_forward_ssm_conv_update_f32` (threads split over
  channels) + branch in `ggml_compute_forward_ssm_conv`.
- `src/models/delta-net-base.cpp` + `models.h`: `build_conv_state_fused` (keeps the cheap build_rs
  conv-tap gather; fuses conv+silu+shifted write-back). Read source (gathered scratch) and write
  target (cache view) are disjoint buffers -> race-free by construction; no ids/identity logic needed.
- `src/models/qwen35.cpp`, `qwen35moe.cpp`, `qwen3next.cpp`: route the single-token decode path
  (`n_seq_tokens==1 && n_rs_seq==0 && fused_gdn_ar`) to `build_conv_state_fused`; prefill/chunked/
  rollback keep the existing concat+ssm_conv+silu+cpy chain.
- `tests/test-backend-ops.cpp`: `test_ssm_conv_update` (16 cases) comparing the fused conv output
  vs the CPU reference across backends.

## Gate: test-backend-ops (CUDA0 vs CPU reference)

- SSM_CONV: 45/45 OK (unchanged path intact)
- SSM_CONV_UPDATE: 16/16 OK (new op; d_conv 3/4 x channels 256/3328 x n_seqs 1/4/32/128)
- SSM_CONV_BIAS_SILU: 90/90 OK

## Gate: greedy bit-exactness (--temp 0 --seed 1 --ignore-eos -n 256, -no-cnv, -fa on)

Byte-identical to the clean Lever-1 (0019/0020) baseline, both models:

| model              | baseline md5                     | fused md5                        | result          |
|--------------------|----------------------------------|----------------------------------|-----------------|
| q36-27b-nvfp4      | 675cd52265f2b3d7695c8739946d55ea | 675cd52265f2b3d7695c8739946d55ea | BYTE-IDENTICAL  |
| q36-35b-a3b-nvfp4  | ac163882eb3812ef08d4c73e6d9a0abf | ac163882eb3812ef08d4c73e6d9a0abf | BYTE-IDENTICAL  |

## decode_agg S_TG (npp128 ntg128, -fa on, -c 33000), same-session before/after

Dense q36-27b-nvfp4:

| mode      | npl | baseline | fused  | delta   |
|-----------|-----|----------|--------|---------|
| CUDA-graph| 32  | 199.76   | 202.99 | +1.6%   |
| CUDA-graph| 128 | 336.35   | 347.14 | +3.2%   |
| eager     | 32  | 196.07   | 197.61 | +0.8%   |
| eager     | 128 | 333.62   | 342.97 | +2.8%   |

MoE q36-35b-a3b-nvfp4:

| mode      | npl | baseline | fused  | delta   |
|-----------|-----|----------|--------|---------|
| CUDA-graph| 32  | 421.72   | 432.39 | +2.5%   |
| CUDA-graph| 128 | 689.74   | 713.54 | +3.5%   |
| eager     | 32  | 421.05   | 432.46 | +2.7%   |
| eager     | 128 | 689.15   | 713.87 | +3.6%   |

Dense npl128 (production CUDA-graph) lands at 347.14 t/s, in the predicted 346-349 band, and at
**88.8% of vLLM 391** (up from 86.0%). The lift holds in BOTH graph and eager modes.

## Step time + nsys kernel delta

Per-step decode time (dense npl128, T_TG / ntg=128):
- baseline 48.711 s / 128 = 380.6 ms/step
- fused    47.197 s / 128 = 368.7 ms/step  -> **-11.9 ms/step** (matches the predicted +12-14 ms)
- MoE npl128: 185.6 -> 179.4 ms/step (-6.2 ms/step)

nsys eager decode (npp128 ntg24 npl128, 24 decode steps x 48 GDN layers), conv-path kernels:

| kernel              | baseline calls | fused calls | per-step (eager) |
|---------------------|----------------|-------------|------------------|
| concat_cont (decode)| 1152           | 0 (GONE)    | 7.95 -> 0 ms     |
| cpy_scalar (decode) | 1152 of 3648   | 0 (GONE)    | 4.29 -> 0 ms     |
| ssm_conv_f32 (decode)| 1152 of 2736  | 0 (prefill-only) | 8.65 -> 0 ms |
| ssm_conv_update     | 0              | 1152        | 0 -> 7.56 ms     |

Decode conv path eager GPU time: ~20.9 ms/step -> ~7.56 ms/step = ~13.3 ms/step saved. concat_cont
and the decode cpy_scalar are eliminated; ssm_conv at decode is replaced by the fused update kernel.
prefill keeps the original chain (concat_non_cont 1584, ssm_conv_f32 1584 unchanged).

## Verdict

Bit-exact, no regression, and lifts decode: dense 336.35 -> 347.14 t/s (+3.2%, 86.0 -> 88.8% of vLLM
391), MoE 689.74 -> 713.54 t/s (+3.5%) at npl128. Step -11.9 ms (dense). Additive and risk-free;
de-risks the in-place conv-cache plumbing the bf16-state lever (design (2)/(4)) also touches.

Assisted-by: Claude:opus-4.8 [Claude Code]
