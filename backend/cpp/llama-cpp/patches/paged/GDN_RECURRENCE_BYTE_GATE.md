# GDN recurrence byte gate + fused single-pass kernel design

Label: llama-fused-recurrence-design (READ-ONLY, no GPU). Source-and-math design only;
the byte-ratio measurement itself is produced by the `ncu-byte-gate` agent.

## TL;DR (the correction the workflow was set up to settle)

**The recurrence kernel is ALREADY single-pass on the f32 state.** `gated_delta_net_cuda<128>`
(after patches 0018 in-place write + 0019 fused gather) loads the whole `s0` column into registers
ONCE (`s_shard[rows_per_lane]`), runs the entire token loop in registers, and writes the new state
back ONCE - directly into the persistent cache slot (0018) or scratch. For decode `n_tokens==1`,
`keep_rs_t==false`: one register load, one register store, no re-read of state from DRAM.

The byte-gate's working hypothesis - "un-fused l2norm/gate/decay/recurrence/state-writeback/gather
each touching the f32 state, so a fused pass halves DRAM bytes" - is **false for the state**. Only
the recurrence kernel touches the 3 MB/seq state. The surrounding ops (`l2_norm`, `silu`, `sigmoid`,
the `gate` exp/softplus, `ssm_conv`, `concat`, `cpy`) all operate on the **small activations**
(q/k/v/g/beta), which are 100-800x smaller than the state. There is no 2x state re-streaming to
recover; the recurrence kernel is byte-minimal on state by construction.

Therefore a fused single-pass kernel **cannot move the dominant 196 ms recurrence** - that cost is
f32-state read+write bandwidth, already a single pass. The two real levers are decoupled:

1. **Fold the surrounding activation ops into the kernel** (MEDIUM effort): recovers the small
   per-op buckets (`ssm_conv` 1.5% + `silu`/`sigmoid` 1.4% + 2x `l2_norm` + `concat` 2.1% + conv
   `cpy` 2.0%, ~6-8% of the step) plus per-op launch overhead. Bit-exact. Ceiling ~93-96% of vLLM.
2. **bf16 state cache** (HIGH effort, NON-bit-exact): halves the dominant byte stream. The only
   large lever on the 196 ms. Target KL < 1e-3 by keeping f32 register accumulation, storing only
   the persisted cache in bf16.

Which of (1)/(2) is worth building hinges on the `ncu-byte-gate` byte ratio (below).

## Byte arithmetic (dense q36-27b-nvfp4, decode, npl128, S_v=128, H_v=48, batch=128)

State per (seq, GDN layer) = S_v^2 * H_v = 128*128*48 = 786,432 f32 = **3.0 MiB**.

Per kernel call (one GDN layer, full 128-seq batch), single pass:
- state read  = 786,432 * 128 * 4 = 402.65 MB
- state write = 402.65 MB
- **state R+W = 805.3 MB/call** (768 MiB)
- activations (q,k 1 MB each; v 3 MB; attn-out 3 MB; g,beta tiny) ~= 8 MB/call = **<1%**.

Measured 4.08 ms/call (node-level trace) -> effective **197.4 GB/s**.
GB10 / DGX Spark LPDDR5X peak ~= **273 GB/s** -> **~72% of peak.**

48 GDN layers/step -> 38.7 GB of state traffic/step -> 196 ms = 51.6% of the 383.48 ms step. v=8MB
activation traffic is noise; state is 99% of the recurrence bytes.

### What this means for the open question
- The recurrence is single-pass, coalesced (transposed layout: lane reads `state[col*S_v + i]`,
  consecutive lanes -> consecutive `i`), running at ~72% of peak BW. It is NOT at the 85% hardware
  floor, but it is NOT re-streaming state either. The 72->85% headroom (~30 ms, bit-exact) is an
  occupancy/coalescing tune, NOT a fusion win.
- vLLM `fused_recurrent_gated_delta_rule` does the SAME single-pass recurrence. If vLLM's recurrent
  state cache is bf16 (model dtype) while llama's is f32, vLLM moves HALF the bytes on the dominant
  stream - that alone is ~98 ms, i.e. essentially the whole residual decode gap. **This is the
  single most decision-relevant number for the `ncu-byte-gate` agent to confirm: the dtype/bytes of
  vLLM's GDN state cache vs llama's f32, plus llama's measured achieved-BW % on the recurrence
  kernel.** If vLLM is bf16-state -> build (2). If vLLM is also f32-state and at ~85% -> llama is
  at the floor, only (1) + coalescing remain and bit-exact parity tops out ~95%.

## The fused single-pass kernel design

Two deliverables, layered. Build (1) first (bit-exact, de-risks the graph), gate (2) on the byte
verdict.

### (1) `ggml_gated_delta_net_decode_fused` - fold the activation ops into the kernel

Folds the pre-recurrence activation ops and the post-recurrence gated RMSNorm into the existing
single-pass recurrence kernel, so q/k/v/g are produced and consumed in registers/shared and never
make a separate DRAM round-trip, and the per-op launches collapse to one.

Current decode op chain in `build_layer_attn_linear` (qwen35.cpp 386-461), per GDN layer:

```
wqkv GEMM -> qkv_mixed                                  (keep: GEMM, separate)
wqkv_gate GEMM -> z                                     (keep: GEMM, separate)
ssm_beta GEMM -> beta -> sigmoid                        [FOLD beta sigmoid]
ssm_alpha GEMM -> alpha -> +ssm_dt -> softplus -> *ssm_a (gate) [FOLD softplus/mul -> per-head g]
build_conv_state: reshape, transpose qkv, CONCAT, cpy   [concat/cpy -> conv-state plumbing, see note]
ggml_ssm_conv(conv_input, conv_kernel)                  [FOLD depthwise conv, K=4]
ggml_silu(conv_output)                                  [FOLD silu]
views q_conv/k_conv/v_conv
ggml_l2_norm(q_conv); ggml_l2_norm(k_conv)              [FOLD 2x l2norm]
[repeat_4d skipped on fused path]
ggml_gated_delta_net_inplace_ids(...)                   <-- THE recurrence kernel (196 ms)
build_norm_gated(output, ssm_norm, z): RMSNorm + silu(z) + mul  [FOLD post gated-RMSNorm]
ssm_out GEMM                                            (keep: GEMM, separate)
```

Fold list (what moves INTO the kernel):
- `beta` sigmoid: scalar per (head,seq); apply in-kernel when reading beta.
- `gate` g = softplus(alpha+dt)*a (GDA, g->ne0==1): scalar per (head,seq); compute/exp in-kernel.
  The kernel already does `expf(*g_t)` (non-KDA path, line 85) - so feed RAW `alpha`+`dt` and the
  `a` scale and do softplus+mul+exp in-kernel; removes the `add`/`softplus`/`mul` launches.
- `ssm_conv` (depthwise causal conv1d, kernel width 4) + `silu`: per channel a length-4 dot of the
  conv state with `ssm_conv1d` then silu. This is the prologue: each warp/thread, before loading
  state, computes its q/k/v channel by reading 3 cached conv-state taps + the current qkv_mixed
  token, dotting the 4-wide kernel, applying silu. The conv state (conv_kernel-1=3 taps x conv_dim)
  is tiny and already cached; fold its read here and its 1-token shift write into the epilogue
  (replaces the `concat`+`cpy` conv-state update).
- `l2_norm` of q and k: a warp reduction over S_v of the per-head q/k vector. The recurrence kernel
  already does warp reductions over S_v (the kv/attn dot products) - the l2norm reuses the same
  warp-reduce primitive on q_reg/k_reg right after they are loaded, before the recurrence math.
- Post: `build_norm_gated` = RMSNorm(output, ssm_norm) * silu(z). The kernel already holds the
  attn output `attn_col` per (head,seq,col) in registers at the end; fold an S_v warp-reduce RMS,
  multiply by `ssm_norm` weight and by `silu(z)` (z read once), and write the final gated output -
  removing the `rms_norm`+`silu`+`mul` launches and one activation round-trip.

State traffic UNCHANGED (still one read + one write). Activation traffic for conv/silu/l2norm/norm
collapses into the kernel's register/shared path; ~6 separate launches become 0. Expected recovery:
the ~6-8% surrounding-op buckets + launch overhead. **Bit-exact** if the numeric ordering is held
(see Numeric notes). Conservative ceiling ~365-375 tok/s dense (~93-96% of vLLM 391).

Data flow (per (h_idx=head, sequence=seq) block, decode n_tokens=1, S_v=128, num_warps=4):
1. PDL sync.
2. Prologue (per channel/lane): read 3 conv-state taps + current `qkv_mixed[t]` for this channel,
   dot with `ssm_conv1d[0..3]`, add conv bias if any, `silu`. Produces this lane's q/k/v element.
3. l2norm q,k: warp-reduce sum(q^2), sum(k^2) over the S_v dim; scale q_reg,k_reg by rsqrt(.+eps).
4. Load `s0` column into `s_shard` (UNCHANGED single read).
5. Recurrence (UNCHANGED math: g-decay, kv = S^T k, delta = (v - g*kv)*beta, S = g*S + k(x)delta,
   attn = S^T q * scale).
6. Write `s_shard` back to cache slot ONCE (UNCHANGED single write). Write the 1-token-shifted conv
   state back to the conv cache (replaces concat+cpy).
7. Epilogue gated-RMSNorm: warp-reduce sum(attn^2) over S_v -> RMS; multiply by `ssm_norm[col]` and
   by `silu(z[col])` (z loaded once); write final output element. ssm_out GEMM stays separate.

Inputs added to the op: `ssm_conv1d` weight, `ssm_norm` weight, `z`, conv-state cache view, raw
`alpha`/`dt`/`a`, eps. This is a wider op signature (src[8..]) - acceptable; gate it behind a new
`cparams.fused_gdn_decode` resolved exactly like `auto_fgdn` (graph_reserve + device-match probe,
llama-context.cpp 518-595) so it silently falls back to the current op chain if any device lacks it.

### (2) bf16 recurrent-state cache - the dominant-term lever (NON-bit-exact)

Only build if `ncu-byte-gate` shows vLLM moves fewer state bytes (bf16) OR llama's f32 recurrence is
already >=85% of peak (then f32 is at the floor and bf16 is the only way down).

- Store `ssm_states_all` (the recurrent-state cache) as bf16. Halves the dominant 805 MB/call -> at
  the same ~197 GB/s -> ~2.04 ms/call -> ~98 ms/step saved (196 -> ~98). Dense projected
  335 -> ~440+ tok/s (>= vLLM 391) if BW-bound holds; smaller dtype usually achieves a HIGHER % of
  peak, so likely better.
- Kernel change: read state -> convert bf16->f32 into `s_shard` (registers stay f32); all recurrence
  arithmetic in f32 (UNCHANGED); on write, convert f32->bf16. Accumulation precision is preserved
  within a step; only the PERSISTED state is rounded to bf16 each step.
- Numerics: the recurrent state decays geometrically (g<1), so per-step bf16 rounding does not
  accumulate unboundedly, but it is NOT bit-exact. Validate KL < 1e-3 vs the f32-state build over a
  256-token greedy run; if KL fails, fall back to f32 state (keep it a cparams toggle). This is the
  ONLY path to bit-near parity-or-better on the dominant term; bit-EXACT parity on the 196 ms is
  unreachable because the f32 state bytes are irreducible (single pass already).

## Numeric / bit-exactness notes (for fold (1))
- l2norm/RMS use f32 warp-reduce accumulation (matches `ggml_l2_norm`/`ggml_rms_norm` f32 sum).
  Order of summation across lanes differs from the standalone op's sequential sum -> floating
  reassociation. To stay bit-exact, replicate the standalone op's reduction order, OR accept a
  tiny reassociation delta and gate on KL<1e-3 (the workflow's near-bit-exact target). Recommend:
  ship fold (1) behind the cparams probe and assert greedy md5 match vs the current chain (0019
  already established the harness: dense text md5, MoE byte-identical).
- Recurrence math, scale, g-exp order, beta apply: keep EXACTLY as in `gated_delta_net_cuda` /
  `ops.cpp` reference (lines 84-141 .cu, 10685-10730 ops.cpp). Do not reorder the
  v - g*kv -> *beta -> S update -> S^T q sequence.
- conv: depthwise dot of width-4 kernel in f32, then silu - identical to `ggml_ssm_conv`+`ggml_silu`
  if done in the same order.
- gate softplus: `softplus(x)=log1p(exp(x))`; match ggml's `ggml_softplus` (has the >20 fast path)
  to stay bit-exact.

## Implementation scope
- (1) `.cu`: extend `gated_delta_net_cuda` with a decode-fused template specialization (or a new
  kernel) that does conv+silu prologue, q/k l2norm, recurrence, conv-state shift write, gated-RMSNorm
  epilogue. Add `ggml_cuda_op` dispatch. CPU mirror in `ops.cpp` for parity/CI.
- (1) `ggml.h`/`ggml.c`: new builder `ggml_gated_delta_net_decode_fused` (extra src: ssm_conv1d,
  ssm_norm, z, conv-cache view, alpha/dt/a, eps + op_params for eps).
- (1) graph edits: `delta-net-base.cpp build_recurrent_attn` (add the decode-fused branch alongside
  the existing fused/ids branch); `qwen35.cpp` + `qwen35moe.cpp` `build_layer_attn_linear` (route
  the pre/post ops into the op when `cparams.fused_gdn_decode`); leave `qwen3next.cpp`,
  `kimi-linear.cpp`, the non-fused and rollback (n_rs_seq>0) paths unchanged.
- (1) `llama-context.cpp`: `auto_fgdn`-style device-match probe to enable/disable the decode-fused
  op (silent fallback). `cparams.h`/`cparams.fused_gdn_decode`.
- (2) bf16 state: cache dtype change in the recurrent-memory allocation + the kernel load/store
  convert + a `cparams` toggle + KL gate. Touches `gated_delta_net.cu` load/store, the inplace/ids
  builders' state asserts, and the recurrent cache type.

## Risk register
- (1) is MEDIUM effort, bit-exact-targetable, but bounded upside (~6-8% + launches; ceiling ~95% of
  vLLM). Worth it only if the workflow wants >90% and accepts no bf16.
- (2) is the only large lever on the dominant 196 ms but is NON-bit-exact (KL-gated). If vLLM is
  f32-state, (2) takes llama BELOW vLLM's precision, not toward parity - a product call, not a perf
  call.
- The widened op signature (many srcs) raises maintenance cost and the device-match probe matters
  (CPU offload of a GDN layer must fall back cleanly).
- Do NOT expect a fused recurrence to cut the 196 ms: it is already one read + one write of f32
  state. Re-confirm with the `ncu-byte-gate` achieved-BW number before committing HIGH effort.

---

# MEASUREMENT + VERDICT (label ncu-byte-gate, THE GPU agent) - GATE SETTLED

The design above predicted the answer; this is the decisive measurement that confirms it.

## VERDICT: NO-BUILD the fused single-pass recurrence. BUILD bf16 SSM state (design's lever (2)).

Deciding number: **llama re-stream factor = ~1.0x** (mathematically capped at <=1.33x; >=1.5x is
physically impossible). llama's recurrence kernel is ALREADY single-pass, coalesced, and at
**74% of GB10 peak BW** - MORE bandwidth-efficient than vLLM's fused triton kernel (41% of peak).
The whole 2x DRAM gap vs vLLM is **f32 (llama) vs bf16 (vLLM) state-cache width**, not re-streaming.

## ncu HW counters were BLOCKED; timing + geometry gave the byte ratio anyway
- `ncu dram__bytes` and `nsys --gpu-metrics-devices` both return `ERR_NVGPUCTRPERM`
  (`NVreg_RestrictProfilingToAdminUsers` restricted, root-only; no passwordless sudo on dgx.casa).
  DRAM byte counters are unobtainable on this box.
- Decisive fallback (no perf counters): CUPTI kernel TIMING (allowed) + EXACT byte geometry from
  the kernel source. bytes_moved <= peak_BW x duration gives a HARD CAP on the re-stream factor;
  comparing implied effective BW between llama and vLLM (same model, same B, both eager) settles it.

## Measured (clean nsys CUDA timing; build-cuda-base df1cc97 Lever-1; both B=128, both graphs/eager-OFF)
llama: `llama-batched-bench -npp 8 -ntg 12 -npl 128 -ub 2048`, GGML_CUDA_DISABLE_GRAPHS=1.
vLLM:  postssm_decomp/vllm_decode.sqlite, NSEQ=128, enforce_eager=True (apples-to-apples).

| kernel | state dtype | bytes R+W/call | duration/call (steady) | eff. BW | % of 273 peak | re-stream |
|---|---|---|---|---|---|---|
| llama gated_delta_net_cuda          | f32  | 805.3 MB | **3.98 ms** (min 3.90 max 4.33, grid 48x128x32) | 202 GB/s | **74%** | ~1.0x |
| vLLM fused_recurrent...packed_decode | bf16 | 402.6 MB | **3.62 ms** (min 3.53 max 3.96, grid 4x6144x1)  | 111 GB/s | **41%** | ~1.0x |

- llama recurrence/step = 3.98 x 48 = **191 ms** (50% of 384 ms step; matches STATE 196 ms).
- vLLM recurrence/step  = 3.62 x 48 = **174 ms**. Per-call gap llama-vs-vLLM is only +10%, NOT 2.8x.
  The old "1.47 ms near-vLLM" was prefill-contaminated; clean decode is 3.98 ms (confirms STATE).
- Both kernels verified SINGLE-PASS in source (llama: s_shard load-once/store-once, 128 consecutive
  f32/warp = coalesced; vLLM packed_decode: `b_h += load(p_h0).to(f32)` once, `store(p_ht, b_h.to(bf16))`
  once). vLLM cache dtype = state_dtype = model_dtype = bf16 (`_mamba_state_dtype` default "auto" ->
  model dtype; config.json dtype=bfloat16). Geometry identical (H=48, k/v head_dim 128, S_v 128).

## Why re-stream ~1.0x (the gate number)
Most bytes a 3.98 ms call could move at 273 GB/s peak = 1.087 GB = **1.33x the 816 MB minimal**.
1.5x/2x re-stream would need >peak BW -> impossible. Source proves single-pass+coalesced -> 1.0x end:
~816 MB at 202 GB/s = 74% peak. A fused single-pass rewrite recovers ~0 state bytes => NO-BUILD.

## The lever: bf16 SSM state (design (2)) - confirmed, large, parity-to-ahead
2x recurrence bytes vs vLLM = 100% f32-vs-bf16 cache. llama's kernel is the more efficient one
(74% vs 41% peak), so bf16 state (cache + load/store bf16, f32 register compute, exactly as vLLM):
- 805.3 -> ~413 MB => at 74% peak ~2.0 ms/call => 191 -> ~96 ms/step, save ~95 ms => step ~289 ms
  (~443 tok/s, AHEAD of vLLM 327). Conservative (50% peak on smaller footprint): ~3.0 ms/call =>
  save ~45 ms => step ~339 ms = vLLM parity. Range = parity-to-ahead.
- NON-bit-exact vs llama's f32 reference, but EQUAL precision to vLLM (which is bf16). Gate on
  PPL/KL vs the f32 build, not md5. "Bit-exact parity with vLLM" was never on the table - vLLM is bf16.

## Conv-path (no-regret conv-fusion lever sizing), llama steady decode, per call x48
concat_cont 169.6 us (8.14 ms/step) + cpy_scalar 120.1 us (5.76) + ssm_conv_f32 115.9 us (5.56)
= ~19.5 ms/step (~5%). Conv STATE ~12.6 MB (tiny) -> this is LAUNCH/small-kernel overhead, not bytes
-> a FUSION lever (design (1)), secondary to bf16 state. l2_norm 6.8 us, gdn_gather 1.21 us (no-op,
identity seqs -> confirms gather does NOT re-stream state at steady decode).

## One-line answer
llama: 805 MB/call, 74% peak, re-stream ~1.0x (<=1.33x). vLLM: 402 MB/call (bf16), 41% peak.
conv-path: ~12.6 MB (launch-bound ~19.5 ms/step, not byte-bound).
=> NO-BUILD fused recurrence (already single-pass, more efficient than vLLM); BUILD bf16 state
(halves the dominant 805 MB, ~45-95 ms/step, parity-to-ahead). Deciding number: re-stream ~1.0x.

Assisted-by: Claude:opus-4.8 [Claude Code]
