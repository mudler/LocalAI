# bf16 SSM-state cache: BUILD PLAN (PART C synthesis - hand this to the build agent)

Status: READ-ONLY design. Lands ON TOP of patch 0021 (conv-state in-place fusion, building
concurrently on the GPU). DEFAULT = bf16 SSM recurrent state, f32 opt-out. This PART C is the
executive build brief: ordered edits, acceptance gate, bench targets, semantics/back-compat/risk
register, and the de-risk-first item. PART A (cparams wiring), PART B (kernel/op plumbing) and the
Appendix (upstream precedent + numeric safety) below are the detailed reference each step points into.

The decision (settled by GDN_RECURRENCE_BYTE_GATE.md): the gated-DeltaNet recurrence is the dominant
decode kernel (51.6% of the step, 805 MB f32 state R+W/call at 74% of GB10 peak BW) and is ALREADY
single-pass (measured re-stream ~1.0x, hard-capped <=1.33x). The whole ~2x DRAM gap vs vLLM is purely
f32(llama) vs bf16(vLLM) state-cache WIDTH, not extra passes. Narrowing the persisted SSM state to
bf16 (load->f32, recurrence math in f32 UNCHANGED, store->bf16) halves the dominant term and reaches
vLLM parity-to-ahead. vLLM's own GDN state cache is bf16, so this is a fair equal-precision change.

## C.0 Synthesis decisions that OVERRIDE the per-part text

1. v1 ships `type_s` = BF16 (SSM recurrent state, the 805 MB lever) and KEEPS `type_r` = F32 (conv
   state). Reason: `ggml_concat` at prefill (`build_conv_state`, delta-net-base.cpp:472) requires
   same-type operands; a bf16 conv cache breaks the f32 `qkv_mixed` concat. Conv state is ~12.6 MB
   (launch-bound, ~0 ms byte benefit), so keeping it f32 costs nothing. This OVERRIDES PART A §3a/§3b,
   which set BOTH defaults to BF16: in v1 set the `type_r` / `cache_type_conv` DEFAULT to
   `GGML_TYPE_F32`. `type_r`=bf16 is a v2 follow-up (needs an f32 staging view before the prefill
   concat - PART B §B.6).
2. Keep ALL transient/scratch tensors f32: the GDN op OUTPUT scratch (ggml.c:6327), the 0019 gather
   scratch, and the keep_rs_t prefill snapshot. ONLY the PERSISTED cache rows narrow to bf16 (the
   src[5] read view and the src[6] in-place write view).
3. The gate REPLACES the bit-exact md5 gate for the bf16 default: bf16 is intentionally non-bit-exact
   vs llama f32 (it is equal precision to vLLM's bf16). The 0018/0019 md5 gate STILL applies to (a)
   patch 0021's conv fusion and (b) verifying the f32 opt-out path is byte-identical to the pre-bf16
   f32 baseline.

## C.1 Ordered file-by-file edit list (build order, on top of 0021)

Order is dependency- and de-risk-driven: prove the kernel dtype-correct in ISOLATION before flipping
any default. Section refs point into PART A / PART B below.

STEP 1 - kernel + op made dtype-generic (the load/store conversion), validated standalone:
- 1a `ggml/src/ggml.c` - relax the F32-only state asserts to {F32,BF16} in the 3 GDN builders:
  `ggml_gated_delta_net` (~6308), `_inplace` (~6370), `_inplace_ids` (~6430), on `state` and
  `src_state_dst`. KEEP the op OUTPUT scratch F32 (6327). [PART B §B.2]
- 1b `ggml/src/ggml-cuda/ggml-cuda.cu` - `supports_op` `GGML_OP_GATED_DELTA_NET` (~3096): permit a
  BF16 `src[5]`/`src[6]`. [PART B §B.3]
- 1c `ggml/src/ggml-cuda/gated_delta_net.cu` - template kernel+gather+launch on `bool STATE_BF16`;
  `#include <cuda_bf16.h>`. LOAD `__bfloat162float` (~102), STORE `__float2bfloat16` (~207), GATHER
  bf16->f32 scratch (~20). Cast `src_state`/`src_state_dst` pointers to `nv_bfloat16` on bf16; relax
  dispatcher asserts (309-311) `sizeof(float)` -> `ggml_type_size(type)`. Keep gather scratch +
  keep_rs_t snapshot f32. ALL recurrence math (106-200) UNCHANGED in f32 registers. [PART B §B.4,§B.8]
- 1d `ggml/src/ggml-cpu/ops.cpp` - matching bf16 load/store branch in the GDN reference (10726/10744/
  10891 load via `GGML_BF16_TO_FP32`, 10758-10762 store via `GGML_FP32_TO_BF16`); relax `nb[]` asserts
  to `ggml_type_size(type)`. [PART B §B.5]
- 1e `tests/test-backend-ops.cpp` - add a BF16-state `GATED_DELTA_NET` case covering BOTH `n_tokens==1`
  decode AND a multi-token (prefill/chunk) + `keep_rs_t==true` path, CUDA bf16 vs CPU bf16 reference.
  THIS IS THE DE-RISK GATE for Step 1 (see C.5). Build + pass before Step 2.

STEP 2 - cparams selection wiring (llama.cpp core):
- 2a `include/llama.h` (after :366) - add `enum ggml_type type_s;` and `type_r;` adjacent to
  `type_k`/`type_v`, marked `[EXPERIMENTAL]`. [PART A §3a]
- 2b `src/llama-context.cpp:3468` (`llama_context_default_params`) - add `/*.type_s =*/ GGML_TYPE_BF16,`
  and `/*.type_r =*/ GGML_TYPE_F32,`. THIS IS THE DEFAULT CHANGE (type_r stays F32 per C.0). [PART A §3a]
- 2c `src/llama-memory.h:19` (`struct llama_memory_params`) - add `ggml_type type_r;` and `type_s;`.
  [PART A §3a]
- 2d `src/llama-context.cpp:325` (`params_mem` init) - pass `params.type_r` / `params.type_s`. [PART A §3a]
- 2e `src/llama-model.cpp` - replace the 3 hardcoded `GGML_TYPE_F32` pairs (2056-57 recurrent, 2098-99
  hybrid_iswa, 2117-18 hybrid = the qwen35/qwen35moe path) with `params.type_r` / `params.type_s`.
  [PART A §2/§3a]

STEP 3 - back-compat for saved recurrent state (REQUIRED, the default flips):
- 3a `src/llama-memory-recurrent.cpp` `state_read_data` - on `s_type_i_ref != live type` with both in
  {F32,BF16}, CONVERT row-by-row during load instead of returning false (same for `r`). Bump the
  recurrent state-file version. [PART A §5, option A]

STEP 4 - CLI / llama-server surface (needed by the gate harness):
- 4a `common/common.h:566` region - `cache_type_ssm = GGML_TYPE_BF16;` and
  `cache_type_conv = GGML_TYPE_F32;` (conv default F32 per C.0). [PART A §3b]
- 4b `common/common.cpp:1589` region - `cparams.type_s = params.cache_type_ssm;` and
  `cparams.type_r = params.cache_type_conv;`. [PART A §3b]
- 4c `common/arg.cpp` (after :2074) - add `--cache-type-ssm`/`-ctssm` and `--cache-type-conv`/`-ctconv`
  via the existing `kv_cache_type_from_str` (arg.cpp:402); confirm `bf16` -> `GGML_TYPE_BF16`. The C.2
  harness depends on `--cache-type-ssm {f32,bf16}`. [PART A §3b]

STEP 5 - LocalAI gRPC / YAML (force f32 from model config):
- 5a `backend/backend.proto` - `string CacheTypeSSM` / `CacheTypeConv` (next free tags after 64);
  regen proto. [PART A §3c]
- 5b `backend/cpp/llama-cpp/grpc-server.cpp:504` region - `params.cache_type_ssm =
  kv_cache_type_from_str(request->cachetypessm());` + conv. [PART A §3c]
- 5c `core/config/model_config.go:935` - `CacheTypeSSM`/`CacheTypeConv` yaml fields. [PART A §3c]
- 5d `core/backend/options.go:247` - map into the request. [PART A §3c]
- 5e `core/config/meta/registry.go` + `build_test.go` - register `cache_type_ssm`/`cache_type_conv`
  as static fields (gate). [PART A §3c]

STEP 6 - capability fallback (heterogeneous / CPU-offload safety):
- 6a `src/llama-context.cpp:518-595` - an `auto_fgdn`-style device-match probe: if a participating
  device lacks the bf16 GDN load/store specialization (CPU-offloaded GDN layer, non-GB10 backend),
  demote `type_s` to F32 BEFORE alloc and log once. [PART A §4]

## C.2 Acceptance gate (REPLACES the bit-exact md5 gate)

bf16 is intentionally non-bit-exact, so the 0018/0019 md5 byte-equality gate does NOT apply to the
bf16 default. The gate is teacher-forced KL-divergence + PPL-delta + greedy coherence + a
long-context drift sweep, vs the SAME model run f32. All commands on `dgx.casa` (DO NOT run during
this design - GPU busy). Binaries `~/llama-paged-dev/build*/bin`; models `~/bench/q36-27b-nvfp4.gguf`
(dense) and `~/bench/q36-35b-a3b-nvfp4.gguf` (MoE); scratch `~/bench/klgate`.

Why teacher-forced (not self-greedy): a self-greedy decode lets each precision pick its own argmax,
so after the first divergence the contexts differ and per-token logits are no longer comparable (you
measure trajectory divergence, not numeric error). `llama-perplexity --kl-divergence` feeds both
precisions the IDENTICAL token stream and compares output distributions position-by-position; the
greedy trajectory is validated SEPARATELY by the Same-top-p metric + a coherence read.

Corpus (one-time): wikitext-2 raw test (~280k tokens) into `~/bench/klgate`. KL mode needs
>= 2*n_ctx tokens; any fixed >=8k-token UTF-8 file works as long as base AND test share it.

256-token headline gate (per model; shown for dense):
```
M=~/bench/q36-27b-nvfp4.gguf; F=~/bench/klgate/wikitext-2-raw/wiki.test.raw; D=~/bench/klgate
COMMON="-m $M -f $F -c 256 -b 256 -ngl 99 -fa on --seed 1 --chunks 32"
# (a) f32 BASE: reference logits + f32 PPL
llama-perplexity $COMMON --cache-type-ssm f32  --kl-divergence-base $D/q27.f32.c256.kld | tee $D/q27.f32.c256.base.log
# (b) bf16 TEST: KL(bf16||f32) + bf16 PPL + Same-top-p
llama-perplexity $COMMON --cache-type-ssm bf16 --kl-divergence --kl-divergence-base $D/q27.f32.c256.kld | tee $D/q27.bf16.c256.kl.log
```
Noise floor (run FIRST, mandatory - GPU reductions are not bit-deterministic, so KLD has a non-zero
floor; bf16 is judged against BOTH the absolute threshold AND this floor):
```
llama-perplexity $COMMON --cache-type-ssm f32 --kl-divergence --kl-divergence-base $D/q27.f32.c256.kld | tee $D/q27.f32f32.floor.log
```
Record `Mean KLD_floor` and `Same-top-p_floor` (expect KLD ~1e-6..1e-5, top-p ~100%).

Coherence spot-check (greedy trajectory, reuses the 0018/0019 `--temp 0 --seed 1` convention):
```
P="Explain how a transformer language model generates text, step by step."
for T in f32 bf16; do llama-cli -m $M -ngl 99 -fa on --temp 0 --seed 1 -n 256 -p "$P" --cache-type-ssm $T 2>/dev/null > $D/q27.greedy.$T.txt; done
diff $D/q27.greedy.f32.txt $D/q27.greedy.bf16.txt && echo "GREEDY BYTE-IDENTICAL"
```
Long-context drift sweep (verifies the g<1 decay bound: bf16 state-rounding error must stay FLAT, not
accumulate, as context grows - the GDN state spans the whole window):
```
for C in 256 1024 2048 4096; do
  CMN="-m $M -f $F -c $C -b $C -ngl 99 -fa on --seed 1 --chunks 8"
  llama-perplexity $CMN --cache-type-ssm f32  --kl-divergence-base $D/q27.f32.c$C.kld >/dev/null
  llama-perplexity $CMN --cache-type-ssm bf16 --kl-divergence --kl-divergence-base $D/q27.f32.c$C.kld | tee $D/q27.bf16.c$C.kl.log
done
```
f32 opt-out verification (the safety valve must actually select f32 and reproduce the committed f32
greedy md5 from 0018/0019 - the bf16 default must NOT change the f32-path output):
```
llama-cli -m $M -ngl 99 -fa on --temp 0 --seed 1 -n 256 -p "$P" --cache-type-ssm f32 2>/dev/null | md5sum  # == 0018/0019 f32 baseline md5
```
Repeat the WHOLE gate verbatim for the MoE model (`M=~/bench/q36-35b-a3b-nvfp4.gguf`).

PASS/FAIL (bf16 ships as DEFAULT only if ALL rows pass for BOTH dense and MoE):

| metric | source | PASS threshold |
|---|---|---|
| Mean KLD | 256-gate (b) | **< 1e-3 nats** (hard, the brief) |
| Mean KLD vs floor | (b) vs floor | <= ~5x `Mean KLD_floor` (bounded signal, not pure noise) |
| Same top p | (b) | **>= 99.5%** (100% => greedy byte-identical to f32) |
| PPL-delta `ln(PPL_bf16/PPL_f32)` | (a)+(b) | **abs < 0.005** (PPL within +-0.5%) |
| Max / 99.9% KLD | (b) | report; flag if Max > 0.05 (tail outliers) |
| Coherence | greedy | fluent + on-topic; byte-identical if Same-top-p=100% |
| Long-context drift | sweep | MeanKLD(4096) <= 1.5x MeanKLD(256) AND Same-top-p(4096) >= 99.0% |

If any row fails for a model: keep THAT model on f32 (gallery YAML `cache_type_ssm: f32`) while the
global default stays bf16; the cparams f32 fallback is the safety valve. MoE has fewer GDN layers
(31 vs 48) and smaller per-head state (H_v=32 vs 48), so expected KLD <= dense; same thresholds.
Same-top-p is the bridge to the old md5 harness: at 100% the bf16 greedy output is byte-identical to
f32 and the 0018/0019 md5 gate would still pass - the strongest possible non-bit-exact result.

## C.3 Bench targets + nsys confirmation

Dense q36-27b-nvfp4 (48 GDN layers, S_v=128, H_v=48), npl128, GB10/sm_121, graphs-OFF
apples-to-apples (the measured baseline):
- Recurrence per call: 3.98 ms (f32, 805 MB R+W, 74% peak) -> **~2.0-3.0 ms** (bf16, ~413 MB R+W).
  2.0 ms = 74% peak retained; 3.0 ms = conservative 50% peak on the smaller footprint.
- Recurrence per step: 191 ms -> ~96-143 ms (save ~48-95 ms).
- Step time: 384 ms -> **289-339 ms**.
- Decode throughput: ~335 -> **360-443 tok/s** = parity-to-ahead of vLLM (327 ms / 391 tok/s).

MoE q36-35b-a3b-nvfp4 (31 GDN layers, H_v=32): state per (seq,layer) = 128*128*32*4 = 2.0 MiB f32 ->
per-call R+W ~537 MB f32 -> ~268 MB bf16. Fewer layers + smaller state => smaller ABSOLUTE recurrence
savings, and MoE decode is more GEMM-bound (the `MUL_MAT_ID` expert path), so the bf16-state win is a
smaller FRACTION of the MoE step. Target: a measurable per-call halving of the GDN recurrence time
with the C.2 KL gate passing; no absolute MoE step target is asserted here (the MoE step is
MUL_MAT_ID-dominated, a separate lever from this one).

nsys confirmation (the measurement that proves the lever landed):
```
GGML_CUDA_DISABLE_GRAPHS=1 nsys profile -o ssmbf16 --force-overwrite true \
  llama-batched-bench -m $M -npp 8 -ntg 12 -npl 128 -ub 2048
nsys stats --report cuda_gpu_kern_sum ssmbf16.nsys-rep | grep -i gated_delta_net
```
Confirm: `gated_delta_net_cuda` mean duration/call drops 3.98 -> 2.0-3.0 ms; step time + tok/s land in
the 289-339 ms / 360-443 tok/s band; the f32 opt-out reproduces the 3.98 ms f32 call. The gate is the
JOINT condition: per-call speed in band AND KL<1e-3 - neither alone ships bf16.

## C.4 Default / opt-out semantics, back-compat, risk register

Semantics:
- DEFAULT `type_s` = `GGML_TYPE_BF16` (SSM recurrent state). `type_r` = `GGML_TYPE_F32` in v1 (conv
  state; bf16 is v2). This is the INVERSE of KV (KV is opt-IN to compression at F16 default; SSM is
  opt-OUT to f32).
- Opt-out: `--cache-type-ssm f32` (CLI) or `cache_type_ssm: f32` (LocalAI YAML) -> bit-exact f32
  recurrence. Per-model opt-out lives in gallery YAML if a model fails the gate; the global default
  stays bf16.
- Silent capability fallback: the C.1 STEP 6 device-match probe demotes `type_s` to F32 before alloc
  on devices lacking the bf16 GDN specialization (CPU offload / non-GB10) and logs once.

Back-compat (the ONE real breakage): `llama-memory-recurrent.cpp` serializes the per-layer state
dtype and HARD-matches on restore (mismatch -> `"mismatched s type"` -> returns false). The f32->bf16
default flip makes OLD f32-saved sessions fail to restore against a bf16 build. Fix = STEP 3a: convert
row-by-row on mismatch (both in {F32,BF16}) + bump the recurrent state-file version. KV never hit this
because `type_k`/`type_v` were EXPERIMENTAL and never default-changed; the SSM default FLIP is what
forces the convert/version work.

Risk register:
- **R1 numeric drift (KL gate fails).** Likelihood LOW: g<1 geometric decay contracts per-step bf16
  rounding to a bounded series (~`eps/(1-exp(g_mean))`), f32 registers confine rounding to one
  per-step cache write, and vLLM ships this exact config in production. Mitigation: C.2 gate +
  per-model f32 opt-out + global f32 fallback.
- **R2 prefill / keep_rs_t / gather state path (the silent-corruption landmine).** The conversion
  points are documented for DECODE; the SAME kernel also runs the chunked prefill path, the keep_rs_t
  snapshot (writes to f32 scratch while the cache is bf16), and the 0019 gather (reads bf16 cache ->
  f32 scratch). A dtype mistake on any of these corrupts the state at the prefill->decode handoff and
  surfaces ONLY as long-context drift, which a decode-only 256-token gate can mask. Mitigation: STEP
  1e test-backend-ops MUST cover the multi-token prefill + keep_rs_t==true path, not just decode; the
  C.2 long-context sweep is the second net. (This is C.5, the single biggest risk.)
- **R3 MoE MUL_MAT_ID path.** The GDN recurrence op is IDENTICAL for dense and MoE; the MoE expert
  GEMM (`MUL_MAT_ID`) does NOT touch the SSM state, so bf16-state is orthogonal to the expert path.
  Residual risk: `qwen35moe` `build_recurrent_attn` must route the same bf16 state view (it shares
  delta-net-base.cpp). Mitigation: run the full C.2 gate on the MoE model; the test-backend-ops case
  is arch-agnostic.
- **R4 conv-state coupling with patch 0021.** Flipping `type_r` to bf16 breaks `ggml_concat` at
  prefill (different types). Mitigation: v1 keeps `type_r`=F32 (C.0); `type_r`=bf16 deferred to v2
  with an f32 staging view (PART B §B.6).
- **R5 back-compat restore failure.** Mitigation: STEP 3a convert + version bump (above).

## C.5 Single biggest risk + how the build agent de-risks it FIRST

Single biggest risk: **R2 - silent state corruption on the NON-decode state paths** (chunked prefill,
the keep_rs_t snapshot, the 0019 gather). The 805 MB measurement and every conversion-point in the
cheat-sheet describe the STEADY decode path (`n_tokens==1`, `!keep_rs_t`). But the bf16 cache is ALSO
read/written by the multi-token prefill path and the prefill/rollback snapshot (which targets f32
scratch while the cache is bf16). A dtype bug there does not crash and barely moves the 256-token
decode md5; it corrupts the recurrent state at the prefill->decode boundary and shows up ONLY as
long-context drift - exactly the failure a quick gate misses.

De-risk FIRST (before ANY default flip or wiring): implement STEP 1 (kernel + op dtype-generic) and
STEP 1e (test-backend-ops) ONLY, then prove the kernel is dtype-correct in ISOLATION by forcing a
bf16 state allocation behind a temporary debug flag and running test-backend-ops with a case that
exercises (a) single-token decode, (b) a multi-token prefill chunk, and (c) `keep_rs_t==true`,
comparing CUDA bf16 against the CPU bf16 reference AND against the f32 path within tolerance. Only
after that case is GREEN does the build agent proceed to STEP 2 (flip the default) and the C.2
model-level gate. This decouples kernel dtype-correctness from the cparams wiring, so a Step-1 bug is
caught by a deterministic op test in minutes instead of as a fuzzy long-context regression after the
full stack is wired.

---

# bf16 SSM state cache — cparams wiring (DEFAULT bf16 + f32 opt-out)

Label: cparams-default-fallback (READ-ONLY design). Mirrors the KV-cache `type_k`/`type_v`
precision plumbing exactly. Designed against HEAD-after-patch-0021 (conv-state in-place fusion).

This is lever (2) of GDN_RECURRENCE_BYTE_GATE.md: the recurrent SSM state cache is the dominant
decode byte stream (805 MB R+W/call, 51.6% of step, single-pass f32 = at the BW floor). The whole
~2x DRAM gap vs vLLM is f32(llama) vs bf16(vLLM) state width. Storing the persisted state in bf16
(load→f32, recurrence math in f32 UNCHANGED, store→bf16) halves the dominant term. vLLM's GDN state
cache is bf16, so bf16-default is the fair equal-precision comparison → make it the DEFAULT.

---

## 1. The KV-cache template we mirror (exact chain for type_k / type_v)

```
CLI   common/arg.cpp:2052     -ctk/--cache-type-k TYPE → params.cache_type_k
                              (common_params, common/common.h:566, default GGML_TYPE_F16)
  ↓
glue  common/common.cpp:1589  cparams.type_k = params.cache_type_k   (cparams = llama_context_params)
  ↓
API   include/llama.h:365     llama_context_params.type_k  // [EXPERIMENTAL]
      llama-context.cpp:3468  default in llama_context_default_params() = GGML_TYPE_F16
  ↓
mem   llama-context.cpp:326   llama_memory_params params_mem.type_k = params.type_k
      llama-memory.h:19       struct llama_memory_params { ggml_type type_k; type_v; ... }
  ↓
alloc llama-model.cpp:2030    create_memory(params_mem, cparams) → KV cache uses params.type_k
```

Key facts:
- `type_k`/`type_v` are NOT stored in `struct llama_cparams` (src/llama-cparams.h). They ride in
  `llama_context_params` → `llama_memory_params` and are consumed directly at cache-alloc time.
  We mirror that: NO new `llama_cparams` field is needed.
- KV default is opt-IN to compression (F16 default, pass `-ctk q8_0` to shrink). SSM is the INVERSE:
  bf16 DEFAULT, pass an explicit `f32` to opt out / restore bit-exactness.

## 2. Where the SSM state type is currently hardcoded (the targets)

The recurrent cache constructor already accepts the types — only the model hardcodes F32:

- `src/llama-memory-recurrent.cpp:22-23` ctor params `ggml_type type_r, type_s`
  - `r_l` (line 100, `n_embd_r`) = short conv state  → `type_r` (TINY: conv_width-1 taps × conv_dim)
  - `s_l` (line 101, `n_embd_s`) = SSM recurrent state → `type_s` (THE 805 MB/call dominant)
- `src/llama-memory-hybrid.h:32-33` ctor params `type_r, type_s` (qwen35 / qwen35moe path)
- Hardcoded `GGML_TYPE_F32` call sites in `src/llama-model.cpp::create_memory`:
  - 2056-2057  `llama_memory_recurrent(...)`            (pure recurrent arches)
  - 2098-2099  `llama_memory_hybrid_iswa(...)`          recurrent_type_r / recurrent_type_s
  - 2117-2118  `llama_memory_hybrid(...)`               recurrent_type_k / recurrent_type_v (mislabeled; they are r/s)

Note: `qwen35` / `qwen35moe` are HYBRID (filter_attn/filter_recr, no SWA) → they take the
`llama_memory_hybrid` branch (2108-2118). That is the call site that matters for the parity push.

## 3. New plumbing (parallel chain `type_s` / `type_r`)

### 3a. Public API + cparams glue (llama.cpp side)

| File | Change |
|------|--------|
| `include/llama.h` (after :366) | Add `enum ggml_type type_s; // data type for recurrent SSM state cache [EXPERIMENTAL]` and `enum ggml_type type_r; // data type for recurrent conv state cache [EXPERIMENTAL]`. Place adjacent to `type_k`/`type_v`. |
| `src/llama-context.cpp:3468` (default params) | Add `/*.type_s =*/ GGML_TYPE_BF16,` and `/*.type_r =*/ GGML_TYPE_BF16,`. **This is the DEFAULT change.** |
| `src/llama-memory.h:19` (`struct llama_memory_params`) | Add `ggml_type type_r;` and `ggml_type type_s;` next to `type_k`/`type_v`. |
| `src/llama-context.cpp:325` (`params_mem` init) | Add `/*.type_r =*/ params.type_r,` and `/*.type_s =*/ params.type_s,`. |
| `src/llama-model.cpp` 2056-57 / 2098-99 / 2117-18 | Replace the 3 hardcoded `GGML_TYPE_F32` pairs with `params.type_r` / `params.type_s`. |

### 3b. CLI / llama-server (common side)

| File | Change |
|------|--------|
| `common/common.h:566` region | Add `ggml_type cache_type_ssm = GGML_TYPE_BF16;` and `ggml_type cache_type_conv = GGML_TYPE_BF16;` (mirror `cache_type_k/v`; note the DEFAULT is BF16, not F16). |
| `common/common.cpp:1589` region | Add `cparams.type_s = params.cache_type_ssm;` and `cparams.type_r = params.cache_type_conv;`. |
| `common/arg.cpp` (after :2074) | Add `--cache-type-ssm TYPE` (`-ctssm`) → `params.cache_type_ssm = kv_cache_type_from_str(value)`, and `--cache-type-conv TYPE` (`-ctconv`). Reuse the existing `kv_cache_type_from_str` (arg.cpp:402). Help text: "recurrent SSM state cache type (default bf16; pass f32 for bit-exact recurrence)". |

`kv_cache_type_from_str` already accepts `f32`/`bf16`/`f16` — no change needed; just confirm `bf16`
maps to `GGML_TYPE_BF16` (add the case if absent).

### 3c. LocalAI gRPC backend (so users can force f32 from model YAML)

Mirror `CacheTypeKey` exactly:

| File | Change |
|------|--------|
| `backend/backend.proto:419` region | Add `string CacheTypeSSM = NN;` and `string CacheTypeConv = NN;` (next free field tags). Regenerate proto. |
| `backend/cpp/llama-cpp/grpc-server.cpp:504` region | `if (!request->cachetypessm().empty()) params.cache_type_ssm = kv_cache_type_from_str(request->cachetypessm());` and the conv equivalent. (grpc-server already has its own `kv_cache_type_from_str`; ensure it knows `bf16`.) |
| `core/config/model_config.go:935` region | Add `CacheTypeSSM string yaml:"cache_type_ssm,omitempty"` and `CacheTypeConv string yaml:"cache_type_conv,omitempty"`. |
| `core/backend/options.go:247` region | Add `CacheTypeSSM: c.CacheTypeSSM,` and `CacheTypeConv: c.CacheTypeConv,` to the request build. |
| `core/config/meta/registry.go:161` + `core/config/meta/build_test.go:140` | Register `cache_type_ssm` / `cache_type_conv` as static fields (the `staticFields` slice + registry map) so the meta-config gate passes. |

LocalAI semantics: leaving `cache_type_ssm` UNSET in YAML → empty gRPC string → backend keeps its
BF16 default. Setting `cache_type_ssm: f32` → forces the f32 opt-out (bit-exact recurrence).

## 4. Default / fallback semantics

- **DEFAULT = `GGML_TYPE_BF16`** for both SSM state (`type_s`) and conv state (`type_r`).
  - SSM state (`type_s`) is the lever: f32→bf16 halves 805→413 MB/call → ~3.98→~2.0-3.0 ms/call.
  - Conv state (`type_r`) is negligible bytes; default it bf16 too for consistency, but it can stay
    f32 with zero perf cost if patch-0021's in-place conv path assumes f32 — see §6.
- **Opt-out = `GGML_TYPE_F32`** via `--cache-type-ssm f32` (CLI) or `cache_type_ssm: f32` (LocalAI YAML).
  Restores bit-exact recurrence; use when the KL gate (<1e-3 / PPL-delta over 256-tok greedy) fails
  for a given model, or for deterministic regression baselines.
- **Silent capability fallback**: gate the bf16 path behind a device-match probe modeled on
  `auto_fgdn` (llama-context.cpp:518-595). If the GDN recurrence kernel's bf16 load/store
  specialization is unavailable on a participating device (e.g. a CPU-offloaded GDN layer with no
  bf16 op, or a non-GB10 backend), fall back to `GGML_TYPE_F32` for `type_s` BEFORE cache alloc and
  log it once. This keeps "bf16 default" from breaking heterogeneous/CPU setups.
- The kernel contract is unchanged-math: load bf16→f32 into `s_shard` (registers stay f32), all
  recurrence arithmetic in f32, store f32→bf16. Only the persisted cache is rounded per step;
  geometric decay (g<1) bounds the rounding (does not accumulate unboundedly).

## 5. Back-compat (the one real breakage — saved sessions / state files)

`src/llama-memory-recurrent.cpp` SERIALIZES the per-layer state tensor dtype and does a HARD match
on restore:
- write: `state_write_data` writes `s_type_i = (int32_t)s_l[il]->type` (line ~900) and the r type.
- read: `state_read_data` reads `s_type_i_ref`, compares to current `s_l[il]->type`, and on
  mismatch logs `"mismatched s type (%d != %d, layer %d)"` and **returns false** (restore FAILS).
  Same for `r` type.

Consequence of the default flip f32→bf16:
- Sessions SAVED by an old f32-default build will FAIL to RESTORE against a new bf16-default build
  (and vice versa), because the serialized `s_type_i_ref` (F32) ≠ the new cache type (BF16).

Required handling (pick one, recommend A):
- **A (convert on mismatch, recommended)**: in `state_read_data`, when `s_type_i_ref != current`
  and both ∈ {F32, BF16}, convert row-by-row during load (`ggml_fp32_to_bf16` / `bf16→fp32`) instead
  of returning false. Same for `r`. Bump the recurrent state-file version so older readers reject
  cleanly. This makes old f32 sessions loadable into bf16 caches and round-trips safely.
- **B (pin precision to the saved file)**: if a session is being restored, read `s_type_i_ref`
  first and set `type_s`/`type_r` from it, overriding the default for that context. Keeps restore
  working but silently disables the bf16 win for resumed sessions.
- **C (document-only)**: keep the hard match; document that bf16-default invalidates cross-version
  saved recurrent states. Lowest effort, worst UX. Not recommended given parity is the goal.

KV-cache parallel: `type_k`/`type_v` were always EXPERIMENTAL and non-default-changing, so the KV
path never had to solve this. The SSM default-FLIP is what forces the convert/version work — call it
out as the single most load-bearing back-compat item.

## 6. Coupling notes / sequencing

- Land ON TOP of patch 0021 (conv-state in-place fusion). If 0021's fused conv write assumes an f32
  conv-state tensor, either (a) extend it to the cache tensor's dtype, or (b) keep `type_r` = F32 by
  default and make ONLY `type_s` bf16 (conv bytes are negligible, so this loses nothing perf-wise and
  de-risks 0021). Decision: ship `type_s`=BF16 first; make `type_r`=BF16 a follow-up gated on 0021's
  conv path being dtype-generic.
- Kernel side (separate patch, not this wiring): `ggml/src/ggml-cuda/gated_delta_net.cu` currently
  takes `const float * curr_state` / `float * state_dst` and does `s_shard[r] = read_state[i]`
  (line 102) — hardcoded f32. The bf16 build needs the dispatch to read `s0->type` and route a
  bf16 load/store specialization; the gather kernel `gdn_gather_nonident_kernel` (line 7, `const
  float * cache`) likewise needs a bf16 variant. The cparams wiring here only selects the cache
  dtype; the kernel patch consumes it. Patches 0018 (in-place) / 0019 (gather) state asserts must be
  relaxed from f32-only to {f32,bf16}.
- CPU mirror `ggml-cpu/ops.cpp` GDN path needs the same bf16 load/store for CI parity / fallback.

## 7. Validation gate

- KL < 1e-3 and PPL-delta within tolerance vs the f32-state build over a 256-token greedy run, per
  model (dense q36-27b-nvfp4, MoE q36-35b-a3b-nvfp4). If a model fails, that model sets
  `cache_type_ssm: f32` in its gallery YAML (per-model opt-out) — the global default stays bf16.
- Add a `test-backend-ops` case for the GDN recurrence with bf16 state (mirror the 0021 harness:
  dense text md5 + MoE byte check) to lock the load→f32→store→bf16 contract.

---

# Appendix - label `upstream-bf16-precedent` (READ-ONLY research)

Precedent + numeric-safety justification for the §1-7 wiring above. Sources: paged dev tree
(`dgx.casa:~/llama-paged-dev`, branch `paged`) and the vLLM checkout
(`~/vllm-bench/.../site-packages/vllm`).

## A.1 Upstream llama.cpp: recurrent-cache f32 is HARDCODED (no f16/bf16 path), not a documented numeric guard

The asymmetry to override: the attention KV cache type is user-tunable; the recurrent state cache is not.

- KV cache: `llama_context_params.type_k/type_v` default `GGML_TYPE_F16`
  (`src/llama-context.cpp:3468-3469`), `[EXPERIMENTAL]` in `include/llama.h:365-366`, plumbed from
  user params (`attn_type_k = params.type_k`).
- Recurrent/SSM cache: `llama_memory_recurrent(... type_r, type_s ...)` and the hybrid wrappers take
  the recurrent types as ctor args, but EVERY call site in `src/llama-model.cpp` passes the literal
  `GGML_TYPE_F32` (2056-2057 pure-recurrent; 2098-2099 hybrid-iswa `recurrent_type_r/s`;
  2117-2118 hybrid `recurrent_type_k/v`). No cparams field feeds these - compile-time constants.
  So mamba/mamba2/rwkv/falcon-h1/nemotron-h/qwen3.5 ALL get f32 recurrent + conv state unconditionally.
- Alloc: `r = ggml_new_tensor_2d(ctx, type_r, ...)`, `s = ggml_new_tensor_2d(ctx, type_s, ...)`
  (`src/llama-memory-recurrent.cpp:100-101`). No f16 branch anywhere.

Is f32 a deliberate numeric constraint? Structural, not documented:
- `ggml_ssm_conv` / `ggml_ssm_conv_update_inplace` HARD-ASSERT f32 on conv state/kernel/x_cur/dst
  plus `nb[0]==sizeof(float)` (`ggml/src/ggml.c:5581-5584,5589,5597`). Conv path is f32-locked at the
  builder.
- `ggml_ssm_scan` does NOT assert input state `s` dtype, but hardcodes its OUTPUT as
  `GGML_TYPE_F32` (`ggml/src/ggml.c:5662`); scan kernels read `s` as `float *`.
- `ggml/src/ggml-cuda/gated_delta_net.cu` takes `const float * curr_state`, `float * state`,
  `float * state_dst`; the per-(seq,head) shard `float s_shard[rows_per_lane]` is loaded/stored as raw
  float (34-102). Same in `ggml-cpu/ops.cpp`.
- NO code comment anywhere justifies "f32 for precision". The constraint is that the ops were written
  float-only. => recurrent-cache-f32 is a hardcoded implementation default to override deliberately:
  the 3 literal `GGML_TYPE_F32` call-site pairs (gate behind `type_s`/`type_r` per §3), the
  gated_delta_net.cu load/store convert, and KEEP conv f32 unless its asserts are extended (conv bytes
  are negligible - only the temporal `type_s` state needs bf16).

## A.2 vLLM: GDN temporal state cache is bf16 BY DEFAULT, fp32-accumulated in-kernel (the exact design)

- Dtype: `qwen3_next.py:780-787` -> `MambaStateDtypeCalculator.gated_delta_net_state_dtype` ->
  `_mamba_state_dtype` (`mamba_utils.py:84-96`):
  `conv_state_dtype = get_kv_cache_torch_dtype(mamba_cache_dtype, model_dtype)`;
  `if mamba_ssm_cache_dtype == "auto": temporal_state_dtype = conv_state_dtype`.
  With both knobs default `"auto"`, `get_kv_cache_torch_dtype("auto", model_dtype)` returns
  `model_dtype` (`torch_utils.py:293-297`) = bf16 for Qwen3-Next => BOTH conv and temporal state are
  bf16 by default. Explicit opt-out: `--mamba-ssm-cache-dtype float32` (mirror of our f32 fallback).
- In-kernel numerics (decode), `fla/ops/fused_recurrent.py`:
  `b_h = tl.load(p_h0).to(tl.float32)` (303) load bf16->fp32; q/k/v/g/beta `.to(tl.float32)` (309-318);
  recurrence in fp32 `b_h*=exp(g); b_v-=sum(b_h*b_k); b_v*=beta; b_h+=b_v*b_k; b_o=sum(b_h*b_q)`
  (327-331); `tl.store(p_ht, b_h.to(p_ht.dtype.element_ty))` (337) store fp32->bf16. Prefill chunk path
  identical (`b_h=tl.zeros(...,tl.float32)`, `+= load().to(fp32)`, 102/120).
  => byte-for-byte the proposed llama lever: load bf16->f32, math in f32 (UNCHANGED order, matches
  gated_delta_net.cu's v-g*kv -> *beta -> S-update -> S^T q), store f32->bf16; only the persisted cache
  crosses the bf16 boundary, once per step.
- vLLM numeric guards: NONE beyond fp32 accumulation - no per-step renorm, no clamp, no Kahan. Optional
  `use_qk_l2norm_in_kernel` normalizes q,k (keeps k unit-norm) but does not touch the state.
- KDA nuance: `kda_state_dtype` returns `(state_dtype, torch.float32)` - Kimi Delta Attention keeps a
  fp32 secondary component. qwen3.5 is `gated_delta_net` (fully-bf16 temporal state), but this shows
  vLLM judged a fp32 component necessary for one delta variant -> reinforces keeping the f32 toggle.

Verdict: vLLM's own GDN state cache is bf16, so bf16-state in llama is a FAIR equal-precision target,
not a regression vs the competitor. bf16 brings llama TO vLLM's precision.

## A.3 Numeric-safety assessment for bf16 gated-DeltaNet state

Update: `S <- S*diag(exp(g)) + beta * k (x) (v - S k)`, with
`g = -exp(A_log)*softplus(a+dt_bias) <= 0` so `exp(g) in (0,1]` (strict geometric decay) and
`beta = sigmoid(.) in (0,1)`.

- Decay bounds error accumulation. bf16 = 8 mantissa bits -> per-element rel rounding
  `eps ~= 2^-8 ~= 3.9e-3`. An error injected at step t is multiplied by `exp(g)<1` every later step ->
  carry-error is a CONTRACTING geometric series bounded by ~`eps/(1-exp(g_mean))`, a small constant
  multiple of one step's eps, NOT linear/unbounded. The recurrence is a contraction map - no
  divergence. (The "per-step renorm" framing is not a literal renorm op in either codebase; the bound
  IS the `g<1` contraction + `beta in (0,1)` + unit-norm k from the l2norm capping `||k (x) delta||`.)
- fp32 register accumulation is the minimal-error placement: load bf16->f32, do `S k`, `v-g*kv`,
  `*beta`, the outer-product accumulate and `S^T q` ALL in fp32 (UNCHANGED math), store f32->bf16 once.
  Identical to vLLM, which ships this as the Qwen3-Next default with no reported quality regression -
  the strongest empirical safety evidence.
- Dominant risk is small KL/PPL drift, not instability. Gate KL<1e-3 + PPL-delta over 256-tok greedy
  vs the f32 build; fall back to f32 via the §3c toggle if it fails. Keep conv state f32 (ssm_conv* is
  f32-locked, conv bytes negligible) - no reason to risk it.

Bottom line: (1) upstream recurrent-cache f32 is a hardcoded implementation default (conv asserts f32;
scan/gdn kernels float-only; no numeric-rationale comments) - override via §3's `type_s`/`type_r`
plumbing, bf16-default + f32 opt-out, touching only the temporal state. (2) vLLM's GDN temporal state
is bf16 by default (auto->model_dtype), fp32-accumulated, with `--mamba-ssm-cache-dtype float32`
opt-out - a fair equal-precision target. (3) bf16 GDN state is numerically safe: g<1 decay contracts
rounding to a bounded geometric series, fp32 registers confine bf16 rounding to one per-step cache
write, and vLLM ships this exact config in production. KL<1e-3 / PPL gate + f32 fallback is the right
safety net.

---

# PART B - label `bf16-kernel-plumbing` (the kernel/op edits §6 defers)

Part A wires the cache DTYPE selection (cparams -> memory_params -> `s_l`/`r_l` alloc). Part B is the
consuming half: every kernel/op that reads or writes those caches, and the exact
load->f32->compute(f32, UNCHANGED)->store->bf16 conversion points. Traced against HEAD-after-0021 on
`dgx.casa:~/llama-paged-dev` (branch `paged`).

## B.1 Complete set of state-cache READERS/WRITERS (one op family only)
`s_l` (ssm_states_all) reaches compute through exactly ONE op family - the gated-DeltaNet recurrence -
via a strided VIEW from `build_rs` (graph base) that carries the cache dtype. The cache-touching srcs:
- `src[5]` `src_state` - the s0 read view (the cache, or the 0019 gather scratch).
- `src[6]` `src_state_dst` - the 0018 in-place write-back target (a view INTO the cache).
- `src[7]` `ids` - I32 seq map for the 0019 gather (no dtype concern).
No other op reads `s_l`. `build_rs` only re-strides (dtype rides through); the 0019
`gdn_gather_nonident_kernel` is the only other reader. So bf16 awareness localizes to: the 3 ggml.c
builders (asserts), cuda `supports_op`, `gated_delta_net.cu`, and the CPU mirror in `ops.cpp`.

## B.2 ggml.c builder asserts (relax F32-only -> {F32,BF16})
File `ggml/src/ggml.c`:
- `ggml_gated_delta_net` (6287): line 6308 `GGML_ASSERT(state->type == GGML_TYPE_F32)` ->
  `... == GGML_TYPE_F32 || ... == GGML_TYPE_BF16`.
- `ggml_gated_delta_net_inplace` (6349): same `state` assert (~6366-6370) + any `src_state_dst`
  type assert -> allow BF16.
- `ggml_gated_delta_net_inplace_ids` (6417): same `state` + `src_state_dst` relax.
- KEEP the op OUTPUT scratch f32: line 6327 `ggml_new_tensor(ctx, GGML_TYPE_F32, 4, ne)` stays. The
  `[attn_scores | new_states]` output is a TRANSIENT graph tensor; the bf16 persisted write goes
  through `src_state_dst`/`state` (in-place). The non-in-place fallback `cpy`s scratch->cache and
  `ggml_cpy` already type-converts f32->bf16.

## B.3 CUDA supports_op
`ggml/src/ggml-cuda/ggml-cuda.cu`, `supports_op` case `GGML_OP_GATED_DELTA_NET` (3096): allow a BF16
`src[5]`/`src[6]` (add BF16 to the permitted state-src types).

## B.4 CUDA recurrence kernel `ggml/src/ggml-cuda/gated_delta_net.cu`
Template the kernel + gather + launch on the CACHE-pointer dtype (`bool STATE_BF16`); keep f32 valid so
the f32 opt-out is the SAME kernel. Include `<cuda_bf16.h>`; convert with `__bfloat162float` /
`__float2bfloat16`. ALL recurrence math (lines 106-200) stays in f32 registers, byte-for-byte UNCHANGED.
- Signatures: line 39 `const float * curr_state` -> `const STATE_T * curr_state`; line 57
  `float * state_dst` -> `STATE_T * state_dst`; `read_state` (85-88) -> `const STATE_T * read_state`.
- LOAD (s0 -> f32 regs), lines 100-103:
  `if constexpr (STATE_BF16) s_shard[r]=__bfloat162float(read_state[i]); else s_shard[r]=read_state[i];`
  `s_shard` stays `float`.
- STORE-BACK (f32 regs -> bf16 cache):
  - non-keep final write (203-208): `state[col*S_v+i] = STATE_BF16 ? __float2bfloat16(s_shard[r]) : s_shard[r];`
  - keep_rs_t snapshot (191-200) targets `dst + attn_score_elems` = the f32 OUTPUT scratch (kept f32
    per B.2); this is the prefill/rollback path (n_rs_seq>0), decode is `!keep_rs_t`. KEEP it f32.
    Only the CACHE pointers (`curr_state` src[5], `state_dst` src[6]) are STATE_T.
- 0019 gather `gdn_gather_nonident_kernel` (7-30): `const float * cache` -> `const STATE_T * cache`;
  `dst[i] = STATE_BF16 ? __bfloat162float(src[i]) : src[i];`. Keep `scratch` OUTPUT f32 (pool alloc
  326-333 stays `ggml_cuda_pool_alloc<float>`) so the non-identity read path feeds f32; the identity
  in-place path reads bf16 directly. `read_state`'s dtype follows the branch that selected it.
- Dispatcher (270-353):
  - casts 299/323 `(const float *)src_state->data`, 312 `(float *)src_state_dst->data` ->
    `(const nv_bfloat16 *)` / `(nv_bfloat16 *)` when `type == GGML_TYPE_BF16`; branch launch on type.
  - asserts 309-311: `src_state_dst->type == GGML_TYPE_F32` -> allow BF16; `nb[0] == sizeof(float)` ->
    `== ggml_type_size(type)`; `nb[1] == S_v*S_v*H*sizeof(float)` -> `... * ggml_type_size(type)`.
  - q/k/v/g/beta strides (348-353) are ACTIVATION (f32) strides - UNCHANGED. Kernel indexes state by
    ELEMENT (`col*S_v+i`), so the typed pointer halves the byte stride implicitly.
  - `launch_gated_delta_net` (212-) + S_v switch (230-260): thread `STATE_BF16` into the
    `gated_delta_net_cuda<S_v, KDA, keep_rs_t, STATE_BF16>` instantiations.

## B.5 CPU reference `ggml/src/ggml-cpu/ops.cpp` (parity / CI / CPU-offload fallback)
`ggml_compute_forward_gated_delta_net_one_chunk` (10662) + `_f32` (10847), dispatch (10915):
- LOAD: 10726 `const float * state_in_base = (const float *)src_state->data`, the rs_head/gather read
  10744-10745, and 10891 `const float * cache = (const float *)src_state->data` -> when
  `src_state->type == GGML_TYPE_BF16`, read `GGML_BF16_TO_FP32(((const ggml_bf16_t*)..)[..])`.
- STORE: 10758-10762 `inplace_state_base = (float *)src_state_dst->data` -> store
  `((ggml_bf16_t*)inplace_state_base)[..] = GGML_FP32_TO_BF16(s_shard)`; relax asserts `nb[0]`/`nb[1]`
  to `ggml_type_size(type)`. Keep ONE impl, branch load/store on `src_state->type`.

## B.6 Conv state (`r_l`) -> bf16 : DEFER (optional, low-value, prefill snag)
Conv state ~12.6 MB total, LAUNCH-bound (0021 removed concat/cpy); bf16 saves ~0 ms, adds complexity:
- DECODE (0021 fused) `ggml_ssm_conv_update_inplace` (ggml.c:5566) asserts 5581-5584
  `conv_states/conv_state_dst->type == F32`; CUDA `ssm_conv_update_f32` (ssm-conv.cu:131) + CPU
  `ggml_compute_forward_ssm_conv_update_f32` (ops.cpp:9471) read/write f32. To bf16: relax the 2
  asserts, template tap LOAD (`__bfloat162float`) + ring write-back STORE (`__float2bfloat16`), cast
  `conv_states`/`conv_state_dst` ptrs in both dispatchers.
- PREFILL (non-fused) `build_conv_state` (delta-net-base.cpp:449-524): `conv_states=build_rs(...)`
  (bf16 view) then `ggml_concat(conv_states, qkv_mixed, 0)` (472). **`ggml_concat` requires same type**
  - qkv_mixed is f32 -> bf16 conv cache BREAKS the prefill concat (needs an f32 staging view of the
  taps first; the ring write-back `ggml_cpy` at 496/520 already converts; concat is the blocker).
RECOMMENDATION: keep `type_r` = F32 in v1 (matches Part A §6). Ship `type_s`=BF16 first; `type_r`=BF16
is a follow-up that adds the f32 staging view.

## B.7 Confirm UNTOUCHED: full-attn KV-cache (16 layers) + FP4 weights
- KV-cache: the `llama_kv_cache` half of `llama_memory_hybrid`, alloc with `params.type_k/type_v`
  (llama-model.cpp 2030-2031 / 2089-2090 / 2108-2109). Part A changes ONLY the recurrent half's
  `type_s`; `attn_type_k`/`attn_type_v` untouched. Paged-KV gather (0003-0011), flash-attn,
  `type_k()/type_v()` accessors (kv-cache.h 161-162/381-382) unaffected.
- FP4 weights (nvfp4 dense + MoE): model weights, separate from runtime state caches; recurrence/conv
  kernels read STATE not weights. FP4 GEMM (0017/0020) untouched.
- Activations (q/k/v/g/beta, attn-out, z) stay f32 (<1% of bytes). Only persisted `s_l` rows narrow.

## B.8 Conversion-point cheat-sheet (the ONLY numeric-precision boundaries)
1. CUDA load   `gated_delta_net.cu` ~102: `s_shard[r] = __bfloat162float(read_state[i])`.
2. CUDA store  ~207: `state[col*S_v+i] = __float2bfloat16(s_shard[r])`.
3. CUDA gather ~20: `dst[i] = __bfloat162float(src[i])` (bf16 cache -> f32 scratch).
4. CPU load    `ops.cpp` ~10726/10744/10891: `GGML_BF16_TO_FP32(((ggml_bf16_t*)src_state->data)[..])`.
5. CPU store   ~10762: `((ggml_bf16_t*)inplace_state_base)[..] = GGML_FP32_TO_BF16(s_shard)`.
Everything between (1)/(4) and (2)/(5) is f32-register math, identical to today's f32 kernel. Only the
persisted cache rounds to bf16 once per step; g<1 geometric decay bounds the rounding.

## B.9 File-by-file edit table (Part B)
| File | Edit |
|---|---|
| `ggml/src/ggml.c` | relax `state`/`src_state_dst` F32 asserts -> allow BF16 in the 3 GDN builders (6308, ~6370, ~6430); keep output scratch F32 (6327) |
| `ggml/src/ggml-cuda/ggml-cuda.cu` | `supports_op` GATED_DELTA_NET (3096): allow BF16 state src |
| `ggml/src/ggml-cuda/gated_delta_net.cu` | template kernel+gather+launch on STATE_BF16; `__bfloat162float` load / `__float2bfloat16` store; cast src_state/src_state_dst ptrs; relax dispatcher asserts (309-311) to `ggml_type_size(type)`; keep gather scratch + keep_rs snapshot f32 |
| `ggml/src/ggml-cpu/ops.cpp` | bf16 load/store branch in GDN ref (10726/10744/10758-10762/10891); relax asserts |
| `tests/test-backend-ops.cpp` | add BF16-state GATED_DELTA_NET case (CUDA bf16 vs CPU bf16) |
| (deferred) conv: `ggml.c:5581-84`, `ssm-conv.cu:131`, `ops.cpp:9471`, `delta-net-base.cpp:472` | v2 only - f32 staging before prefill concat |

Assisted-by: Claude:opus-4.8 [Claude Code]
