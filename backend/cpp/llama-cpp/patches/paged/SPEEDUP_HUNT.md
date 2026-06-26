# SPEEDUP_HUNT.md - the post-0023 vLLM decode close/beat hunt

Accumulator for the four-lever speedup hunt on the clean pin-synced base (llama.cpp
9d5d882d, bit-exact md5 == 0023 baseline). Levers (current-brief labels):
A = hybrid per-head SSM precision, B = MoE grouped-GEMM, C = structural dense residual
(lm_head + graph/launch), D = f16 glue.

---

## D - f16 GLUE: confirm lower-priority (label: D-f16-confirm, READ-ONLY no GPU)

Re-read `F16_DENSE_RESIDUAL_PROBE.md` (the lever-D doc) plus `BF16_SSM_STATE_RESULTS.md`
(lever A's parent work) and `OTHER_PATHS_INVESTIGATION.md` (the B/lm_head + graph
analysis). Verdict: **D is correctly deprioritized. Dominated by both A and B. Build
later behind an opt-in flag only if the last ~4% dense is ever chased; do NOT build now.**

### The numbers that pin D below A and B

- D's reachable mass is TINY. The dense decode gap to vLLM is ~27 ms/step (llama 332.8 ms
  vs vLLM 305.7 ms @npl128). 83.2% of the step (recurrence 49.3% + FP4 GEMM 27.4% + FP4
  act-quant/fixup 6.4%) is ALREADY precision-matched f32/W4A4 on both engines - f16 cannot
  touch it. The f16-able glue is only **8.4% of the step** (Budget A = 28.74 ms: norms +
  elementwise + activations + flash_attn + rope + copies).
- f16 does not zero the glue, it halves the bytes of the memory-bound part. Realistic
  recovery from the probe: ~11 ms (glue only) to ~16 ms (+ the uncertain nvjet GEMM) =
  **40-60% of the 27 ms residual**. That moves dense parity 91.8% -> ~95-96%, NOT a close.
- The single largest f16-able line (flash_attn 11.9 ms) is the LEAST recoverable (KV is
  ALREADY f16, the KQ/softmax accumulate stays forced f32 = vLLM does the same). The cleanly
  recoverable band is just the norms+elementwise+activations (~16.7 ms -> ~8.4 ms saved).

### Dominated by A (parity-and-beyond) and B (the bigger gap) - confirmed

- **A dominates on the same dense axis.** A targets the recurrence, which is 49.3% of the
  dense step - i.e. ~6x the mass D can touch. The bf16-SSM measurement already proved the
  recurrence kernel halves (-49%/call) and clean dense bf16 hit ~490 t/s = **125% of vLLM**
  (`BF16_SSM_STATE_RESULTS.md` sec 2). A's hybrid per-head variant keeps the long-memory
  heads f32 to pass the KL gate that plain bf16 failed (drift FAIL ~10% argmax flips @>=1024
  ctx) while banking most of that +25-31%. So A is the parity-AND-BEYOND lever on dense;
  D's ceiling is ~96% parity. A wins outright.
- **B is the bigger gap.** MoE sits at ~82% (726 vs 882) vs dense ~92%; the MoE-specific
  kernel (mul_mat_q<NVFP4,M-tile=64> grouped GEMM, 26.9% of MoE decode = ~43.5 ms/step) and
  the W4A4 act-quant tax are real MoE deltas. D is a DENSE-only lever (the MoE step is
  recurrence + FP4-GEMM + bf16-projection dominated; the f16 glue band is even smaller
  there) - it does nothing for the larger MoE gap. B addresses where the bench is worst.
- **C overlaps and out-prioritizes D's residual.** The probe's own conclusion: the
  remaining ~3-4% after f16 is structural (non-FP4 cublas/nvjet GEMM efficiency +
  graph/launch scheduling), and those help the BIT-EXACT default too, unlike D which is
  opt-in non-bit-exact. C's graph/launch work is the better long-term dense target.

### Is there a cheap subset of D worth folding into a later build?

**No cheap subset that pays.** The probe maps D to three escalating options:

- A flag: does not exist and cannot exist - the F32 stream is STRUCTURAL
  (`ggml_mul_mat` hardcodes an F32 result, so the residual stream snaps back to F32 after
  every projection; rms_norm/l2_norm/silu/add/mul/flash_attn/ssm_conv all emit F32).
- **Option 1 (the "cheap" one: per-op f16 on ops that already have f16 paths - silu/sigmoid/
  softplus/add/mul/rope): NET NEAR-ZERO OR NEGATIVE.** Because the residual stream stays F32,
  each op must be wrapped cast(F16)->op->cast(F32) = 2 extra `cpy` ops. At decode these ops
  are tiny and memory-bound, so the cast traffic ~= the op traffic and the win is eaten unless
  the cast is FUSED into producer/consumer. Crucially Option 1 CANNOT reach the norms - the
  largest glue item. So the only "cheap" subset is the one that does not actually help.
- Option 2 (the real lever): carry the residual stream in F16 across the layer, which needs
  NEW F16 template instantiations in norm.cu (rms_norm / l2_norm / fused rms+mul / rms+mul+add,
  today hard-`GGML_ASSERT(type==F32)`) keeping the f32 reduction, an f16 projection-output
  path, plus graph-dtype plumbing in qwen35.cpp/llama-graph.cpp. Multi-file, recovers ~11 ms,
  and is **non-bit-exact** (same gate-failing category as the shelved bf16-SSM state). Not cheap.

There is no fold-in-for-free subset: the only no-new-kernel piece (Option 1) is net-zero, and
the only piece that captures real mass (Option 2 norm.cu f16 kernels) is a multi-file build.

### THE D PRIORITY CALL

D is correctly deprioritized, below A, B, and C:
- **Reachable mass:** D 8.4% of the dense step vs A's 49.3% recurrence; D is dense-only and
  does nothing for the bigger MoE (B) gap.
- **Ceiling:** D tops out ~95-96% dense parity; A is already parity-AND-BEYOND (125% clean,
  hybrid keeps most of it inside the KL gate).
- **Bit-exactness:** D is opt-in NON-bit-exact (same bucket as shelved bf16-SSM and the
  NVFP4-head); it cannot improve the shipped f32 bit-exact default, whereas C's structural
  graph/launch work does help the default.

### RECOMMENDATION: build LATER (opt-in only), not now; no cheap subset to fold in

Do NOT build the f16 glue path now. Ship the 95%-bit-exact f32 plateau (patches 0018-0023)
as the default. If the last ~4% dense is ever chased, the ONLY worthwhile piece is Option 2's
norm.cu f16 kernels + f16 residual stream (recovers the norm/elementwise band, ~11 ms); gate
it behind an explicit opt-in flag and validate it against the SAME KL threshold that failed
plain bf16-SSM before shipping. Skip Option 1 entirely (cast overhead eats the win). Prefer
the structural ~3-4% (non-FP4 cublas GEMM efficiency + graph/launch scheduling, lever C) over
D, because that helps the bit-exact default too. D stays the lowest-priority of the four levers.

Assisted-by: Claude:opus-4.8 [Claude Code]

---

## A - HYBRID PER-HEAD f32/bf16 SSM STATE (label: A-hybrid-design, READ-ONLY no GPU)

Goal: capture most of the whole-bf16 SSM-state win (recurrence -49%/call; dense ~490 t/s = 125% of
vLLM; MoE +25%) WITHOUT the KL failure (whole-bf16 MeanKLD 0.05-0.17, Same-top-p ~90%, ~10% argmax
flips @>=1024 ctx). Keep f32 on the long-memory heads (where bf16 rounding does NOT contract and the
KL error concentrates); bf16 only the fast-decaying heads. Stays at-or-above vLLM precision (vLLM
keeps ALL temporal state f32) while landing ABOVE vLLM throughput.

### Why the error concentrates in long-memory heads (the physics)
qwen35/qwen35moe take the NON-KDA path: per (head h, token t) the decay is ONE scalar
(gated_delta_net.cu `g_val = expf(g[h,t])`, `S <- g_val*S + k(x)delta`). The gate (qwen35.cpp):
`g[h,t] = ssm_a[h] * softplus(alpha[h,t] + ssm_dt[h])`, with `ssm_a[h] = -exp(A_log[h]) <= 0` =>
decay = exp(g) in (0,1]. Two STATIC per-head weights set the timescale: ssm_a[h] (tensor
SSM_A_NOSCAN, [n_v_heads]) = decay-rate SCALE (|ssm_a| small => structurally long-memory); ssm_dt[h]
(SSM_DT "bias", [n_v_heads]) = softplus operating point. bf16 carry-error per step is contracting,
bounded ~`eps*tau_h`, eps~2^-8~3.9e-3, head memory length `tau_h ~ 1/(|ssm_a[h]|*softplus(ssm_dt[h]))`
tokens. Error scales LINEARLY with tau_h => long-memory heads blow up the KL (matches the measured
plateau-but-large failure). Keep those f32.

### Classification: per-head STATIC, at model load (NOT per-token)
g is per-token but the long-vs-fast PROPERTY is per-head static (dominated by ssm_a/ssm_dt). A cache
row's dtype must be stable across the sequence => a per-token threshold is impossible; classify ONCE
at load into a per-(layer,head) dtype mask.
- TIER 1 (default, zero-cost, deterministic): pure-weights. `tau_h = 1/(|ssm_a[il][h]|*
  softplus(ssm_dt[il][h]))`; keep f32 if tau_h > T_thresh, else bf16. T_thresh is THE knob (start
  32-64; sweep on GateBench). eps*tau_h => a single T_thresh sets a uniform per-head error ceiling.
- TIER 2 (optional): short calibration pass measures per-head time-mean of actual exp(g[h,t]); write
  mask to a model-hash sidecar (paid once). Use only if Tier 1 lands just above the gate.
cparam `ssm_hybrid_tau_thresh` / `--ssm-bf16-tau`: inf => all-f32 (today's bit-exact default); 0 =>
all-bf16 (the shelved mode); the hybrid band is in between.

### Mixed-dtype cache layout: two homogeneous partitions per slot (packed)
Split persisted s_l ([S_v,S_v,H,slots] f32, n_embd_s=S_v*S_v*H) into TWO dtype-homogeneous sub-caches
sized by head COUNT (this is what saves bytes): `s_l_f32 [S_v*S_v*n_f32, slots]` f32 +
`s_l_bf16 [S_v*S_v*n_bf16, slots]` bf16. Static map `head_slot[h]={is_bf16, local_idx}`. q/k/v/g/beta
KEEP natural head order (no activation permute). Block h_idx -> head_slot -> base + local_idx*S_v*S_v.
Recurrence R+W bytes scale by `f_bytes = (n_f32 + n_bf16/2)/H = 1 - 0.5*(n_bf16/H)`. In-place/ids
identity stays race-free (each head writes its own partition; read==write slot, registers before
store). (Cheaper coarse fallback = per-LAYER dtype, near-zero layout code, but long-memory heads span
most layers => too coarse; per-head is the right granularity.)

### Kernel: single launch, runtime per-head branch (on top of BF16_SSM_STATE.diff)
Reuse the existing bf16 plumbing (gdn_state_t alias, __bfloat162float load / __float2bfloat16 store,
gather template, dtype-detect dispatcher). Hybrid change: pass BOTH bases (`const float* s_f32_base`,
`const nv_bfloat16* s_bf16_base`, + the two state_dst views) + device `head_slot[]`; branch load/store
on `head_slot[h_idx].is_bf16` (UNIFORM per block => no warp divergence). Recurrence math byte-for-byte
untouched (f32 registers). keep_rs_t snapshots stay f32 (op-output scratch). gdn_gather_nonident
becomes per-head dtype-aware (still disjoint-scratch race-free). ONE op call + ONE launch.

### KL-gate plan + estimated pass / f32 fraction / speedup
KLD contribution ~ (eps*tau_h)^2 => dominated by the top-tau heads; removing the top ~25-40% by tau
cuts MeanKLD 1-2 orders. Honest estimate: ~30-40% f32 PASSES Same-top-p>=99.5% and brings MeanKLD to
1e-3..1e-2; strict <1e-3 may need ~40-50% f32. Find the exact fraction by sweeping T_thresh on the
EXISTING GateBench harness (noise floor -> 256-tok gate -> drift sweep 256/1024/2048/4096, both
models). Hybrid is STRICTLY safer than vLLM (vLLM = all-f32 temporal; we f32 exactly the unsafe
heads). Long-memory heads are the minority (~20-40%) => design band f in [0.30, 0.50].
Speedup (dense, bandwidth-bound recurrence, graphs-off): f32 3.38 ms/call, whole-bf16 1.73 (-49%);
hybrid ~ f_bytes*3.38 => f=0.30 -> 2.20 ms (-35%, ~70% of bf16 win); f=0.50 -> 2.54 ms (-25%, ~50%).
Throughput (dense f32 ~371-384=95% vLLM; whole-bf16 ~490=125%; vLLM ref 419): f=0.30 -> ~454 t/s
(~108% vLLM, gate-likely); f=0.50 -> ~430 t/s (~103% vLLM, most robust). MoE: smaller absolute
recurrence (31 GDN layers, H_v=32) + MUL_MAT_ID-bound step (lever B) => hybrid keeps the +13-25%
recurrence share KL-passing but does not alone close the MoE GEMM gap. Joint gate: nsys per-call bytes
down AND KL<1e-3 both models.

### Scope on top of BF16_SSM_STATE.diff
Reuse verbatim: gdn_state_t alias, templated load/store, gather template, dispatcher dtype-detect,
type_s/type_r cparams, CPU mirror, back-compat row convert, bf16 fill, test-backend-ops bf16 cases.
NEW: (1) classifier ~80-150 LOC (host fn over ssm_a/ssm_dt -> head_is_bf16[layer][head] + counts +
T_thresh cparam/CLI; optional Tier-2 calib+sidecar). (2) split cache layout ~150-250 LOC (BIGGEST:
llama-memory-recurrent.cpp alloc s_l_f32+s_l_bf16 by per-layer counts; build_rs builds two views +
passes head_slot; n_embd_s split). (3) kernel ~120-200 LOC (two bases + device map, runtime per-head
branch at load/in-place-store/gather/dispatch; math untouched; STATE_BF16 template stays as the
all-bf16 case). (4) ids/in-place per-head (state_dst two partition views; per-head gather; identity
unchanged). (5) CPU mirror per-head branch. (6) test-backend-ops MIXED-dtype-state case (decode +
multi-token prefill + keep_rs_t = the R2 corruption net). (7) gate: sweep T_thresh for min-f32 passing
KL<1e-3 + Same-top-p>=99.5% + drift both models; nsys per-call confirms f_bytes; md5 that T_thresh=inf
reproduces the f32 baseline (bit-exact opt-out preserved).

Net: principled path ABOVE vLLM throughput (dense ~430-454 vs vLLM 419) at-or-above vLLM precision,
KL-gated. Biggest new item = the split-tensor cache layout; classifier + kernel bounded; gate is a
threshold sweep on the existing harness.

Assisted-by: Claude:opus-4.8 [Claude Code]

---

## B - MoE GROUPED-GEMM + RE-GRAPH (label: B-moe-profile-design, THE GPU AGENT)

GPU-measured on DGX GB10 (sm_121), dev tree `~/llama-paged-dev` HEAD `2ee65c2` (patch 0024; the
decode kernels are byte-identical to 0023/f7409c2 - 0024 is the serving-only burst-reclaim).
`build-cuda`, model `q36-35b-a3b-nvfp4`, `llama-batched-bench -fa on -npp 128 -ntg 128`,
`LLAMA_KV_PAGED=1`. `decode_agg = S_TG t/s`. Batched-bench is the clean-kernel measure (no server
scheduler overhead), so its npl128 = ~743 t/s sits ABOVE the server final_benchmark 726 t/s; the
re-graph % gain below transfers to both paths (same kernels, same graph-disable).

### 1. MoE decode decomposition @npl128 - RE-CONFIRMED on the current HEAD

Fresh nsys `--cuda-graph-trace=node`, decode-isolated steady window, % of summed kernel GPU-time
(reproduces the 0023 profile in `OTHER_PATHS_INVESTIGATION.md` A.2/D within noise; window is
95.4% kernels-only busy / 96.8% with memcpy = GPU-compute-bound):

```
 42.3%  gated_delta_net_cuda            REC  (shared w/ dense; ALREADY tuned past vLLM, 0018-0022: 84.6% vs 82.4% peak BW)
~29.5%  mul_mat_q<NVFP4>                MoE FP4 GEMM = grouped M-tile=64 (~27%, biggest MoE-specific) + router M-tile=128 (~2.3%)
~10.5%  nvjet_sm121 + cutlass (bf16)    attn/gdn bf16 projections + the BF16 lm_head (path B)
  3.1%  k_get_rows_float                REC state gather
  2.7%  k_bin_bcast                     expert-combine + routing-weight scale + glue
  2.1%  ssm_conv_update_f32             REC
  2.0%  quantize_mmq_nvfp4              W4A4 activation-quant tax (3.25 ms/step; vLLM-W4A16 avoids it)
  1.8%  convert_unary bf16<->f32        glue around the bf16 projections
  1.4%  MEMCPY-DtoD                     (SSM state copy fused away by 0018-0019; now small)
  0.5%  mul_mat_q_stream_k_fixup | 0.32% mm_ids_helper | 0.19% argsort | 0.14% gather_mmq_fp4 (0023 dedup) | 0.3% flash_attn
```

Bucketed: **Recurrence/SSM ~48% (shared, tuned past vLLM, NOT a MoE lever)**; **MoE FP4 GEMM+routing
~33%**; **bf16 projections ~10.5%**; act-quant tax ~2%; attention ~0.3%.

### 2. RE-GRAPH the MoE decode step - TESTED + MEASURED (the headline finding)

**Un-graphed status CONFIRMED, and the disable is purely conservative.** NVFP4 on sm_121 has
`get_mmvq_mmid_max_batch_turing_plus(NVFP4)=8` (`mmvq.cu:139-148`). At MoE decode `ne[2]=npl > 8`,
so every MUL_MAT_ID node trips the disable in `ggml_cuda_graph_check_compability`
(`ggml-cuda.cu:3278`: `node->ne[2] > mmvq_mmid_max => use_cuda_graph=false` for the WHOLE step).
BUT the path actually taken at `ne[2]>8` on Blackwell NVFP4 is `ggml_cuda_should_use_mmq()==true`
(`ggml-cuda.cu:2664`) -> the **grouped stream-k `mul_mat_q` id-branch**, launched on one stream with
**NO host sync** (verified: zero `cudaStreamSynchronize`/`Memcpy` in `mmq.cu`/`mmid.cu`). The stream
sync the disable guards against lives ONLY in the per-expert host-loop fallback, which is never
reached when `should_use_mmq` is true. So graphs are SAFE for the grouped path; the disable is a
conservative over-guard (upstream TODO + ggml-org/llama.cpp#18958).

**The lever (env-gated, bit-exact, built+measured here).** Relax the disable when the node takes
the grouped MMQ path. Patch (one function, one TU, 9 s incremental build):

```c
// ggml-cuda.cu  ggml_cuda_graph_check_compability(), [TAG_MUL_MAT_ID_CUDA_GRAPHS]
bool mmid_needs_sync = !ggml_is_quantized(node->src[0]->type) || node->ne[2] > mmvq_mmid_max;
if (mmid_needs_sync && ggml_is_quantized(node->src[0]->type) &&
    getenv("LLAMA_MOE_FORCE_GRAPHS") != nullptr &&
    ggml_cuda_should_use_mmq(node->src[0]->type, cc, node->src[1]->ne[2], node->src[0]->ne[2])) {
    mmid_needs_sync = false;   // grouped stream-k id-path is sync-free => graph-safe
}
if (mmid_needs_sync) { use_cuda_graph = false; ... }
```

**Measured A/B (2 reps each, rock-solid; OFF=stock graphs-disabled, ON=LLAMA_MOE_FORCE_GRAPHS=1):**

| npl | OFF decode_agg | ON decode_agg | gain | OFF %vLLM | ON %vLLM |
|----:|---------------:|--------------:|-----:|----------:|---------:|
|   8 | 226.0 | 226.4 | +0.2% (noise) | 88% | 88% |  *(ne2=8<=mmid_max: MMVQ path already graphs, FORCE inert)*
|  32 | 433.8 | 452.7 | **+4.4%** | 86.6% | **90.4%** |
|  64 | 589.0 | 605.9 | **+2.9%** | 85.9% | **88.3%** |
| 128 | 743.1 | 757.1 | **+1.9%** | 84.2% | **85.8%** |

(vLLM ref 256.5 / 500.8 / 686.1 / 882.2.) The win is largest at small batch (more host-launch
overhead relative to kernel work) and shrinks as kernels dominate at npl128 - exactly the ~1.7%
within-step launch-idle the prior agent measured at 98.3% GPU-busy. This REFINES the prior "graphs
won't help npl128" verdict: it DOES help (+1.9%, above noise), and helps npl32-64 substantially
(+3-4%). **Bit-exact by construction** (graph replay re-issues the identical kernel sequence with
identical args; FORCE only flips `use_cuda_graph`; the shipped f32 dense path already runs graphed).
**Bit-exact gate - both PASS (measured):** `test-backend-ops -o MUL_MAT_ID -b CUDA0` = **806/806,
CUDA0 OK** (the grouped FP4 kernel is untouched - the edit is host-only graph-compat logic); and a
**parallel-greedy np16** run (ne2=16>8, i.e. the grouped MMQ path under graphs ON vs eager OFF) gives
**byte-identical generated content ON==OFF** (md5 `04c4761...` both, 16/16 completions, diff empty).
**SHIP CANDIDATE -> patch 0025** (default-off env now; safe to flip to `should_use_mmq`-gated
default-ON since it is a pure, gated, bit-exact win).

### 3. Grouped-GEMM occupancy headroom - EXHAUSTED on this model (cheap levers), one structural lever left

- The FP4-MMA `mul_mat_q<NVFP4>` is **register-bound to 1 CTA/SM** (`__launch_bounds__(256,1)`,
  ~255 regs/thread = ~12.5% thread occupancy). Grouped grids: ~2048 and ~8192 64-wide tiles.
- **M-tile (col-tile) axis NEUTRAL** (runtime `LLAMA_MOE_DECODE_TILE`, npl128): TILE32 742.4 /
  TILE64 744.2 / TILE96 747.1 - all within 0.6%. Re-confirms patch 0015: this 256-tiny-expert model
  is **bandwidth/SSM-bound, not col-tile-occupancy-bound**, so the M-tile lever has nothing to bite.
- **Cheap occupancy lever already measured (patch 0017):** compile-time `GGML_CUDA_FP4_MINBLOCKS=2`
  on MoE @npl128 = **+0.4% (noise)**, and nsys showed it makes the dense FP4 GEMM **+8.7% SLOWER**
  (register-cap spills, occupancy did not usefully rise). So the cheap register-cap lever is spent.
- **Only untested grouped-GEMM lever = the structural `mmq_y`-down (nwarps=4 warp-remap)** - the
  0017-deferred P2. `mmq_y` tiles N (weight rows), not M, so shrinking it does NOT re-read weights
  (BW-neutral) and raises resident CTAs. Bit-exact (warp/fragment remap, same FP4-MMA math), but a
  real kernel change (the `nwarps x tile_C::I == mmq_y` static_assert coupling), and predicted
  BOUNDED on this BW-bound model. Not a cheap toggle; do only if the re-graph + M1 banks are
  insufficient.

### 4. W4A16 option (skip the act-quant, vLLM's Marlin choice) - NOT recommended

vLLM on GB10 runs **MARLIN W4A16** MoE (engine-log confirmed: "Your GPU does not have native FP4 ...
Marlin kernel"): bf16 activations NEVER quantized, FP4 weights dequant-in-kernel to bf16, **bf16
MMA**, under a full CUDA graph. It does this because CUTLASS's native-FP4 grouped GEMM is broken on
consumer sm_121 (whitelists only sm_100/103 datacenter Blackwell). llama instead runs **native
Blackwell FP4-MMA W4A4** grouped stream-k - a HIGHER arithmetic tier (GB10 FP4 = 2x INT8/BF16 rate).
The W4A4 act-quant tax llama pays (`quantize_mmq_nvfp4`) is **only ~2.0% of MoE decode** (3.25 ms/step
after the 0023 up/gate dedup). Adopting W4A16 to erase it would: (a) be **NOT bit-exact** (bf16 acts
!= FP4 acts -> different logits); (b) **descend to BF16-class MMA** (concede GB10's 2x FP4 rate - the
grouped GEMM, ~27% of the step, would run at HALF the MMA rate); (c) re-enter the **W4A16 occupancy
wall** (the prior GB10 W4A16 effort plateaued ~9 TFLOP/178 t/s). The BW saving is a sliver (acts are
tiny vs the ~weight read at M~4/expert), so it trades a bit-exact 2% for a non-bit-exact, slower,
occupancy-hostile path. **Reject.** The act-quant tax is better attacked bit-exactly via the down_proj
quantize retune (M1).

### 5. RANKED MoE levers (expected gain, bit-exactness, tractability)

1. **RE-GRAPH the MoE decode (this patch, -> 0025): MEASURED +4.4% npl32 / +2.9% npl64 / +1.9% npl128.**
   Bit-exact, tiny (one function, one TU), low-risk, built+measured. **The clear #1.** Helps the
   server path AND small-npl most (where llama was weakest: npl32 86.6%->90.4% of vLLM).
2. **down_proj act-quant retune (M1): bit-exact, bounded (act-quant is ~2%).** Cheap bank-shot;
   retune `quantize_mmq_nvfp4` block/grid (byte-identical output, like 0023's gather). Low single-%.
3. **Grouped-GEMM `mmq_y`-down warp-remap: bit-exact, BW-neutral, the 0017-deferred P2.** Speculative,
   predicted bounded on this BW-bound model; real kernel work. Only if 1+2 insufficient.
4. **M-tile / MINBLOCKS occupancy: EXHAUSTED** (measured neutral-to-negative). Do not pursue.
5. **W4A16: REJECT** (non-bit-exact, slower BF16 arithmetic, occupancy wall). Not even a clean opt-in.

**Net:** the bit-exact MoE-GEMM-region headroom from 1+2(+3) is ~3-6% at npl128 (MoE ~84% -> ~88-90%
of vLLM) and ~4-5% at npl32-64. Full MoE parity is NOT reachable from the GEMM/launch track alone:
the remaining gap is the grouped GEMM (~27%, FP4-MMA at the LPDDR5x BW floor - hardest regime, vLLM
ships purpose-built Marlin-NvFp4) + the bf16 projections (~10.5%). The recurrence (~48%) is already
PAST vLLM. The single highest-ROI, ship-now item is the re-graph patch (0025).

Assisted-by: Claude:opus-4.8 [Claude Code]
