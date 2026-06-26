# bf16 SSM-state cache - BUILD + DE-RISK RESULTS

Label: bf16-build-derisk (the GPU build agent). Lands on top of patch 0023 (HEAD f7409c2) on the DGX
dev tree `~/llama-paged-dev` (branch `paged`). Status: **DE-RISK GATE PASSED, READY FOR THE C.2 KL
GATE (GateBench).** Work is built into `build-cuda` and saved as `~/llama-paged-dev/BF16_SSM_STATE.diff`
(uncommitted on the dev tree; the 0024 ship/shelve decision is gated on GateBench's KL results).

## DECISION applied (user override of the plan): f32 DEFAULT + bf16 OPT-IN
The plan defaulted bf16; the user wants f32 to stay the bit-exact DEFAULT and bf16 to be opt-IN via
`--cache-type-ssm bf16`. So `type_s` default = `GGML_TYPE_F32`, `type_r` default = `GGML_TYPE_F32`
(conv stays f32 always, per plan C.0). Only the persisted RECURRENT (temporal) state narrows to bf16
when opted in; recurrence math stays f32 (load->f32, compute f32, store->cache dtype). The opt-in is
non-invasive: with no flag the output is byte-identical to 0023.

## Files changed (15; full diff = ~/llama-paged-dev/BF16_SSM_STATE.diff, 1003 lines)

STEP 1 - dtype-generic kernel + op (the de-risk core):
- `ggml/src/ggml.c` - 3 GDN builder `state`/`state_dst` asserts F32 -> {F32,BF16}; `state_dst->nb[0]`
  `sizeof(float)` -> `ggml_type_size(state_dst->type)`. Also relaxed the `ggml_fill` builder assert to
  allow BF16 (needed by the rs_zero clear; see below).
- `ggml/src/ggml-cuda/gated_delta_net.cu` - `gdn_state_t<STATE_BF16>` alias (`nv_bfloat16`/`float`);
  recurrence kernel + gather kernel + both launchers + the dispatcher templated on `STATE_BF16`.
  LOAD `__bfloat162float`, STORE `__float2bfloat16`; the gather scratch is allocated at the CACHE
  dtype so `read_state` is a single uniform dtype (no mixed-dtype read path - eliminates the plan-R2
  landmine). The keep_rs snapshot + the non-in-place final write stay f32 (op output scratch); the
  bf16 store happens ONLY on the in-place cache path. `supports_op` already returned `true`
  unconditionally for GATED_DELTA_NET, so no change there.
- `ggml/src/ggml-cpu/ops.cpp` - byte-based prior-state read base + `read_bf16` load conversion
  (`GGML_BF16_TO_FP32`); bf16 in-place convert-store after the per-(head,seq) token loop
  (`GGML_FP32_TO_BF16`); bf16-widening non-identity gather; relaxed `nb[]` asserts to
  `ggml_type_size`. Added a `ggml_compute_forward_fill_bf16` + dispatch case.
- `ggml/src/ggml-cuda/fill.cu` - BF16 case in the fill kernel switch.
- `ggml/src/ggml-cpu/ggml-cpu.c` - GDN work-size adds the extra `S_v*S_v` f32 buffer when the cache is
  bf16 in-place (mirror of `need_work` in ops.cpp).
- `tests/test-backend-ops.cpp` - `state_type` field on `test_gated_delta_net`; 16 bf16-state cases
  (head_size 64 + 128 x {decode, multi-token prefill 33/64/100, keep_rs_t K=4} x kda 0/1, n_seqs 1/2).

STEP 2/3/4 - cparams opt-in wiring (f32 DEFAULT):
- `include/llama.h` - `type_r`/`type_s` in `llama_context_params` (adjacent to type_k/type_v).
- `src/llama-context.cpp` - default-params `type_r = type_s = GGML_TYPE_F32`; `params_mem` passes them.
- `src/llama-memory.h` - `type_r`/`type_s` in `llama_memory_params`.
- `src/llama-model.cpp` - the 3 hardcoded `GGML_TYPE_F32` recurrent ctor pairs (recurrent /
  hybrid_iswa / hybrid = the qwen35/qwen35moe path) now pass `params.type_r` / `params.type_s`.
- `src/llama-memory-recurrent.cpp` - back-compat: `state_read_data` converts f32<->bf16 on type
  mismatch (helper `recurrent_read_convert_rows` via the public `ggml_bf16_to_fp32_row` /
  `ggml_fp32_to_bf16_row`) instead of failing, for both r and s; lets an f32-saved session restore
  into a bf16 cache and vice versa.
- `src/llama-graph.cpp` - `build_rs` rs_zero clear: f32 keeps the exact `ggml_scale_inplace(.,0)` op
  (bit-exactness); bf16 (and any non-f32) state uses `ggml_fill_inplace(.,0)` (CUDA scale is f32-only;
  this was the one extra state-touching op the plan's "one op family" claim missed). get_rows + cpy
  on the extra-states path already support bf16, so no change needed there.
- `common/common.h` / `common/common.cpp` / `common/arg.cpp` - `cache_type_ssm` / `cache_type_conv`
  (default F32) + `--cache-type-ssm`/`-ctssm` and `--cache-type-conv`/`-ctconv` CLI (reusing the
  existing `kv_cache_type_from_str`, which already maps `f32`/`bf16`).

## DE-RISK GATE - ALL PASS

1. **Build clean** (build-cuda, CUDA arch 121): EXIT=0 for ggml/ggml-cuda/ggml-cpu/llama/llama-common
   and the binaries (test-backend-ops, llama-completion, llama-cli, llama-perplexity, llama-batched-bench).
2. **test-backend-ops -o GATED_DELTA_NET = 52/52 PASS** (CUDA backend vs CPU reference). Includes all
   16 new bf16-state cases (CUDA bf16 vs CPU bf16) covering decode (n_tokens==1), multi-token
   prefill/chunk (33/64/100), and keep_rs_t (K=4), with kda on/off and head_size 64 + 128 (production
   S_v). The bf16 op test is the deterministic R2 de-risk for the load/compute/store contract.
3. **f32-default md5 == 0023 baseline (opt-in is non-invasive):**
   - dense  q36-27b-nvfp4: `5951a5b4d624ce891e22ab5fca9bc439` == 0023  (no flag AND `--cache-type-ssm f32`)
   - MoE    q36-35b-a3b-nvfp4: `07db32c2bcb78d17a43ed18bc22705cd` == 0023
   Command: `llama-completion -ngl 99 -fa on -p "The capital of France is" -n 48 --temp 0 --seed 1`.
4. **bf16 opt-in coherence + engaged (dense, `--cache-type-ssm bf16`):** no crash; coherent + on-topic.
   - 48-tok greedy ("The capital of France is"): "**Paris**." - byte-identical to f32 (md5 5951a5b4...),
     i.e. Same-top-p = 100% over that short sample (the g<1 decay bounds the per-step rounding so the
     argmax trajectory is unchanged at short length).
   - 256-tok greedy ("Explain how a transformer LM generates text, step by step"): fluent, well-structured
     step-by-step explanation, and the bf16 md5 (`fc82b4cd44f8ec999c3b6843eb3f8c61`) **DIVERGES** from
     f32 (`554cc667a2e62a47c34a999a127ac7e5`) - definitive proof that bf16 is genuinely ENGAGED (not a
     silent f32 fallback) and behaves as expected (non-bit-exact, coherent). The per-token divergence
     is exactly what the C.2 teacher-forced KL gate quantifies.
   - Independent proof bf16 is allocated: BEFORE the build_rs fill fix, decode aborted in
     `ggml_cuda_op_scale` on the recurrent-state tensor - an f32 cache would never have reached that
     bf16-only failure, so the opt-in demonstrably allocates bf16. Wiring is also directly traceable:
     `--cache-type-ssm bf16` -> cache_type_ssm -> cparams.type_s -> params_mem.type_s -> the
     llama_memory_hybrid recurrent `s_l` alloc.

CONFIRM: ready for the C.2 KL-divergence + PPL-delta + long-context drift gate (GateBench).

## A landmine fixed beyond the plan (record for the gate/ship agents)
The plan B.1 asserted `s_l` reaches compute through ONLY the gated-DeltaNet op. It also flows through
`build_rs`: (a) the rs_zero restart-slot clear used `ggml_scale_inplace(.,0)`, and `ggml_cuda_op_scale`
hard-asserts f32 -> the first bf16 decode aborted in scale. Fixed by routing the bf16 clear through
`ggml_fill` (with a new bf16 fill path). (b) the extra-states `ggml_get_rows` + `ggml_cpy` already
support bf16 (verified) - no change. This is exactly the kind of non-decode state path the de-risk
was meant to surface; it is now covered end-to-end (the bf16 coherence run exercises rs_zero on the
fresh-sequence prompt).

## NOT done in this phase (next agents)
- STEP 5 LocalAI gRPC/YAML (`CacheTypeSSM`/`CacheTypeConv` proto + grpc-server + model_config +
  options + meta registry) - needed to force f32/bf16 from a gallery YAML; not on the de-risk gate.
- STEP 6 capability fallback (device-match probe to demote bf16->f32 before alloc on a device lacking
  the bf16 GDN/fill path, e.g. CPU-offloaded GDN). The CPU reference DOES implement bf16 (load/store/
  gather/fill) so a CPU fallback is numerically correct today, but the probe is the clean guard.
- The C.2 KL/PPL/long-context gate + the C.3 nsys per-call bench - GateBench (GPU gate agent, runs
  sequentially after this build phase; binaries are pre-built in build-cuda).

Assisted-by: Claude:opus-4.8 [Claude Code]

---

# C.2/C.3 ACCEPTANCE GATE + PARITY BENCH RESULTS (label bf16-gate-bench)

Status: **GATE FAILS -> NO-SHIP. KEEP SHELVED. patch 0024 NOT created; nothing committed.**
All runs on `dgx.casa` build-cuda binaries, wikitext-2-raw test, `-ngl 99 -fa on --seed 1`.
Corpus: `~/bench/klgate/wikitext-2-raw/wiki.test.raw` (symlink to `~/wikitext-2-raw`, ~280k tokens).

## 1. KL acceptance gate

### Noise floor (f32-vs-f32, c256 chunks32) - the non-determinism floor
| model | Mean KLD | Max KLD | Same-top-p | ln(PPL(Q)/PPL(base)) |
|---|---|---|---|---|
| dense q27 | -1.3e-5 | 1e-6 | 100.000% | +0.001256 |
| MoE q35   | ~0 (-3e-7) | 5.9e-5 | 100.000% | +0.000607 |

### Headline 256-token gate (bf16-vs-f32, c256 chunks32) - PASSES, but vacuously
bf16 c256 is **byte-identical to the floor** for both models (Mean KLD -1.3e-5 dense / ~0 MoE,
Same-top-p 100%, identical PPL). Reason: a single 256-token window is processed in ONE ubatch
(ub512 > 256), so the recurrent state is written to the bf16 cache only ONCE at the chunk end and is
NEVER read back to produce that window's logits. The 256-token gate therefore does NOT exercise the
bf16 round-trip at all - it is blind to the actual cost.

### Long-context drift sweep (bf16-vs-f32, chunks8) - FAILS HARD for BOTH models
| model | ctx | Mean KLD | Same-top-p | Max KLD | 99.9% KLD |
|---|---|---|---|---|---|
| dense | 256  | -1.3e-5 | 100.000% | 1e-6 | 0 |
| dense | 1024 | 0.0647 | 91.54% | 20.17 | 7.69 |
| dense | 2048 | 0.1739 | 90.65% | 24.89 | 18.18 |
| dense | 4096 | 0.1258 | 90.40% | 26.03 | 17.22 |
| MoE   | 256  | ~0      | 100.000% | 5.6e-5 | 4.9e-5 |
| MoE   | 1024 | 0.0472 | 90.04% | 5.13 | 0.95 |
| MoE   | 2048 | 0.0442 | 90.84% | 1.85 | 1.11 |
| MoE   | 4096 | 0.0422 | 89.97% | 2.76 | 0.83 |

Gate thresholds: Mean KLD < 1e-3; Same-top-p >= 99.5%; |ln(PPL ratio)| < 0.005;
drift MeanKLD(4096) <= 1.5x MeanKLD(256) AND Same-top-p(4096) >= 99.0%.
Result: 256-tok PASS (vacuous); **drift FAIL by ~50-170x on Mean KLD and ~9 pts on Same-top-p**
(top-p ~90% = roughly 1 token in 10 flips its argmax at >=1024 ctx). FAIL for both dense and MoE.

### Discrimination (is it a bug or genuine bf16?) - dense c1024 chunks8
- **f32 long-context floor c1024**: Mean KLD -1.2e-5, Same-top-p 100% -> the bf16 divergence is REAL
  signal, not a long-context measurement artifact.
- **bf16 KLD is invariant to ubatch-boundary count** (= the cross-ubatch state read-back frequency):
  ub1024 (0 internal boundaries) 0.0642 / 91.19%; ub512 (1) 0.0647 / 91.54%; ub256 (3) 0.0639 /
  91.17%; ub64 (15) 0.0682 / 90.97%. Flat. -> The error is INTRINSIC to bf16 over the long
  recurrence INSIDE a single op call, NOT a chunked-prefill/keep_rs/gather handoff bug (R2 ruled out;
  test-backend-ops 52/52 already passed). The error PLATEAUS with context (contraction), i.e. it is
  bounded but LARGE: the gated-DeltaNet has long-memory heads (exp(g) ~ 1), so the g<1 decay does NOT
  tightly contract the per-step bf16 rounding the way the plan's A.3 optimistically assumed.

Note: this is exactly vLLM's own precision (vLLM's GDN temporal cache is bf16). vLLM users never see
this delta because vLLM has no f32 reference; our gate exposes the full bf16-vs-f32 gap because our
f32 path is a HIGHER bar than vLLM.

## 2. Parity bench - the perf lever IS real

### nsys recurrence per-call (graphs OFF, npp4 ntg32 npl128) - gated_delta_net_cuda Avg
| model | f32 ms/call | bf16 ms/call | delta |
|---|---|---|---|
| dense q27 | 3.381 | 1.726 | **-49.0%** |
| MoE q35   | 2.245 | 1.153 | **-48.6%** |

The predicted 3.49 -> 2-3 ms/call lever LANDED (even beat it). Total GPU time dropped too (dense
~12.05 -> ~9.05 s graphs-off). bf16 halving the persisted SSM-state bytes halves the dominant decode
kernel, exactly as designed.

### End-to-end decode throughput (S_TG aggregate, npp128 ntg128, graphs ON unless noted)
| model | npl | f32 t/s | bf16 t/s | note |
|---|---|---|---|---|
| dense | 32  | 212 | 239 | +12.8% |
| dense | 128 | 371-376 (stable) | 287 / 336 / 487 / 498 (BIMODAL) | clean ~490 = +31%; bad runs from a CUDA-graph instability on the dense path |
| dense | 128 | 371.67 (graphsOFF) | 486.68 (graphsOFF) | clean +31% |
| MoE   | 32  | 449 | 509 | +13.4% |
| MoE   | 128 | 767 | 958 | +24.9% (clean, nsys-corroborated) |

% of vLLM (391 t/s dense reference): f32 default = 95-96% (bit-exact, higher precision than vLLM);
bf16 clean ~490 = **125%** (but unstable on dense + fails the numeric gate). MoE bf16 +25% is clean.

## 3. DECISION: NO-SHIP / KEEP SHELVED
- The KL gate **fails** the long-context drift criterion for both models: bf16 SSM state changes
  ~10% of tokens at >=1024 ctx vs our f32 (Same-top-p ~90%, Mean KLD 0.04-0.17). It is therefore NOT
  a quality-neutral opt-in and cannot honor the project's "f32 bit-exact default" promise.
- Per the task rule (gate FAIL -> do not ship as usable): **patch 0024 was NOT created and nothing was
  committed** (DGX tree stays uncommitted; backup `~/llama-paged-dev/BF16_SSM_STATE.diff`).
- The perf lever is genuinely real (recurrence halves; dense ~490 t/s = 125% of vLLM when clean; MoE
  +25%) and bf16 == vLLM's own precision, so it remains a valid FUTURE option - but only if shipped as
  an explicitly-labeled "vLLM-precision-class, NON-bit-exact" mode (never quality-neutral), AND the
  dense CUDA-graph throughput instability (bimodal 287..498) is fixed first.
- Recommendation: keep the shipped default as f32 bit-exact (95% of vLLM at higher precision). Shelve
  bf16. Optional follow-up lever if precision must be cut: bf16 only on the SHORT-memory heads (those
  with exp(g) well below 1), keeping long-memory heads f32 - a mixed-precision state that could pass
  the gate while still cutting bytes; not implemented/measured here.

Assisted-by: Claude:opus-4.8 [Claude Code]
