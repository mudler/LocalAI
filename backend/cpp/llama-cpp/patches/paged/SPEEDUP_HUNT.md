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

---

## C - STRUCTURAL DENSE RESIDUAL: lm_head + scheduling (label: C-structural-design, READ-ONLY no GPU)

Source-confirmed on DGX `~/llama-paged-dev` @ HEAD `2ee65c2` plus committed traces
(`CRITICALPATH_GAP_ANALYSIS.md`, `A2_CUDAGRAPH_DECODE.md`, `F16_DENSE_RESIDUAL_PROBE.md`,
`OTHER_PATHS_INVESTIGATION.md` sec B). Numbers are dense q36-27b-nvfp4 @npl128: step ~333 ms
(384 t/s), gap to vLLM (419 t/s = 305 ms) is ~27-28 ms/step. **Verdict: lever C is a near
dead-end for a bit-exact dense win; rank it LAST of A/B/C/D for the bit-exact default.**

### How the lm_head is stored, and why it routes to cublas/nvjet (not the tuned FP4 MMQ)

`output.weight` is **GGML_TYPE_BF16** (NOT quantized): the `--tensor-type attn/ffn=nvfp4`
recipe converts only attn+ffn, leaving the logit-sensitive final projection (and tok_embd)
at base BF16. Confirmed: `llama-model.cpp:1460` creates the NVFP4 scale `output_s` ONLY
`if (output->type == GGML_TYPE_NVFP4)`, so for the BF16 head `model.output_s` is null, and
`build_lora_mm` (`llama-graph.cpp:1087`) collapses to a plain `ggml_mul_mat`. In
`ggml_cuda_mul_mat` dispatch (`ggml-cuda.cu:2599-2629`): `use_mul_mat_q`/`use_mul_mat_vec_q`
both require `ggml_is_quantized(src0)` (BF16 fails => the tuned FP4 path is INELIGIBLE);
MMF is gated off for the wide `vocab x 128` shape; `use_batched_cublas_bf16` is true but the
batched branch additionally needs `src1->ne[2]*ne[3] > 1` (the 2D decode lm_head fails it).
Falls through to `ggml_cuda_op_mul_mat_cublas` BF16 branch (`:1662`): downcast F32 act ->
BF16, `cublasGemmEx(16BF x 16BF -> COMPUTE_32F)` = **nvjet_sm121**, output rounded BF16 ->
upcast F32. Shape M=vocab(151936) x N=128 x K=5120: a tall-skinny output GEMM reading the
ENTIRE BF16 head weight for 128 columns = inherently **memory-bound**. On the dense model
this is the ONLY non-FP4 cublas GEMM in decode. Cost: nvjet = 11.91 ms = 3.1-3.6% of step.

**CRITICAL CORRECTION the team must carry:** the baseline is NOT "f32 lm_head". The cublas
BF16 branch downcasts the activation F32->BF16 AND rounds the output to BF16. Today's
"bit-exact reference" logits are ALREADY BF16-precision on both input and output. So
"bit-exact" for lever C only protects BF16-rounded logits, which is exactly why option (c)
is "essentially bit-exact" and why any meaningful lm_head speedup requires changing the dtype.

### lm_head bit-exact lever + gain - bandwidth math kills it

nvjet moves the full BF16 head weight in 11.9-12.2 ms = ~195-199 GB/s = ~72% of GB10's
273 GB/s peak: it is ALREADY one of the most bandwidth-efficient kernels in the step (the
overall decode step runs at only ~40% util / ~110 GB/s). The bit-exact ceiling is the
remaining bandwidth headroom only:
- **(c) keep BF16 weight, swap the kernel** (custom skinny wide-vocab streaming GEMM, or a
  hand-picked cublasLt algo/workspace heuristic for the thin-N/huge-M shape). The ONLY
  essentially-bit-exact option. Perfect HBM saturation 199 -> 273 GB/s = 11.9 -> ~8.7 ms =
  **save ~3 ms = ~0.9-1.0% of step = ~11% of the 27 ms gap.** REALISTIC gain: 0 to 3 ms,
  leaning toward 0 - cublasLt already selected nvjet as its best algo, so beating it on a
  pure weight-stream is not guaranteed, and it is high kernel-writing effort. (F16 probe
  independently estimates the same nvjet recovery as "~5 ms, uncertain - may already run TF32".)

Structural reason it is near-zero: the head must read the entire BF16 weight for 128 columns;
you CANNOT cut those weight bytes without changing the dtype. Bit-exactness and the only real
speedup (fewer weight bytes) are mutually exclusive here.

### lm_head NON-bit-exact options (excluded from any vLLM-parity claim)

- **(a) NVFP4-quantize the head -> tuned FP4 MMQ.** Biggest win, BREAKS bit-exactness.
  Weight ~4x fewer bytes (BF16 ~1.5-2.4 GB -> NVFP4 ~0.4-0.6 GB) AND rides the already-tuned
  `mul_mat_q<NVFP4>` (patch 0017): memory floor drops ~4x = **save ~8-9 ms = ~2.5% of step**.
  BUT NVFP4 < BF16 precision => different logit bits, can flip greedy argmax, AND it is
  **UNFAIR vs vLLM** (which keeps its LM head BF16). Same opt-in non-bit-exact bucket as the
  shelved bf16-SSM / f16-glue; exclude from parity claims.
- (b) FP8 / Q8_0 head: smaller error than NVFP4 but still != BF16 bits AND not on the tuned
  FP4 MMQ path, so it buys less speed than (a). No reason to prefer.
- (existing knob) `GGML_CUDA_FORCE_CUBLAS_COMPUTE_16F` (`ggml-cuda.cu:1610`): 16-bit accumulate
  on this exact GEMM, faster but NON-bit-exact (16F vs 32F accumulate). Non-bit-exact track only.

### Scheduling / launch bit-exact lever + gain - ~0.05%

The decode step is GPU-bound at 99.94% (node-level trace, single stream, graphId replayed).
CUDA graphs ALREADY collapse within-step launch latency: exposed idle = 0.225 ms/step = 0.06%,
zero gaps > 5 us, graph ON vs OFF = +0.13% @npl128 (noise). Graphs are NOT a pending dense
lever - they are already in effect. The ONLY graph-non-covered overhead is the BETWEEN-step
host gap: ggml rebuilds the cgraph each step with a NEW `cgraph->uid`, so the uid fast-path in
`ggml_cuda_graph_update_required` never fires and the host re-dispatches ~3100 launches between
graph launches. MEASURED exposed cost: ~0.2 ms/step = ~0.05% (most of the ~2 ms host loop
overlaps GPU compute). **Bit-exact lever:** make the cgraph PERSISTENT/reused across decode
steps so the uid fast-path fires (replay-only => bit-exact). GAIN ~0.2 ms/step = ~0.05%, medium
effort (touches ggml graph lifetime), second-order. No other per-step host overhead is exposed
(the host loop is HIDDEN under GPU compute until the kernels get fast enough to drop GPU-busy
below host time).

### Quantified realistic bit-exact total for lever C

lm_head kernel swap 0 to ~3 ms (upper ~0.9%, realistically ~0) + persistent cgraph ~0.2 ms
(~0.05%) = **combined bit-exact ceiling ~3.2 ms = ~0.95% of the 333 ms step = ~12% of the
27 ms gap.** Moves dense parity 91.8% -> at most ~92.7%, realistically <0.5% net (<1.5 ms).
The "~3-4%" in the brief is the lm_head's TOTAL cost, NOT what is bit-exactly recoverable: only
the bandwidth headroom (~3 ms) and host gap (~0.2 ms) are recoverable; the other ~9 ms is the
irreducible BF16 weight stream BOTH engines pay (vLLM keeps a BF16 head too). **Rank C LAST for
the bit-exact default.** Its one durable note for the team: the lm_head logits are ALREADY
BF16-rounded (not f32), which both narrows what option (c) must preserve and is exactly why the
only meaningful lm_head speedup requires a dtype change (= non-bit-exact + unfair vs vLLM).

Source (DGX @2ee65c2): `llama-model.cpp:1460`, `llama-graph.cpp:1087`, `qwen35.cpp:222` /
`qwen35moe.cpp:246`, `ggml-cuda.cu:2599-2629` / `:1662-1690` / `:1610`.

Assisted-by: Claude:opus-4.8 [Claude Code]

---

# RANK + PLAN - the final synthesis (build order, A handoff, B/C/D queue)

This is the decision section: all four levers measured/designed, ranked by gain x tractability
x gate, the concrete A build plan, and the ordered B/C/D queue with each one's trigger. Base:
clean pin-synced llama.cpp 9d5d882d, bit-exact md5 == 0023. Dense gap to vLLM ~27 ms/step (384
vs 419 t/s @npl128); MoE ~82% (726 vs 882). Recurrence already PAST vLLM (84.6% vs 82.4% peak BW).

## (1) Per-lever scorecard: gain (dense + MoE), tractability, gate

| Lever | Dense decode gain | MoE decode gain | Tractability | Quality gate | Bit-exact? |
|-------|-------------------|-----------------|--------------|--------------|------------|
| **B re-graph (patch 0025)** | ~0 (dense already graphed) | **MEASURED +4.4% npl32 / +2.9% npl64 / +1.9% npl128** (MoE 84%->86% .. 90% of vLLM) | **VERY HIGH - already built+measured**, 1 fn / 1 TU / 9 s build | md5 byte-identical: **PASSED** (MUL_MAT_ID 806/806 + parallel-greedy md5 identical) | YES |
| **A hybrid per-head SSM** | **+25% to +35%/call recurrence -> ~430-454 t/s = 103-108% of vLLM** (ABOVE vLLM) | keeps the +13-25% recurrence share KL-passing; does NOT alone close the MoE GEMM floor | MEDIUM-HIGH - builds on `BF16_SSM_STATE.diff`; biggest new piece = split-dtype cache layout (~150-250 LOC) | **KL<1e-3 + Same-top-p>=99.5% + drift sweep 256/1024/2048/4096 both models**; md5 that T_thresh=inf == f32 baseline | f32 default YES; hybrid is at-or-above vLLM precision, KL-gated |
| **B M1 down_proj retune** | ~0 | bit-exact, bounded (act-quant is ~2% of MoE step) - low single-% | HIGH - block/grid retune of `quantize_mmq_nvfp4`, byte-identical output | md5 byte-identical | YES |
| **B mmq_y-down warp-remap** | small (shared FP4 GEMM) | bit-exact, BW-neutral, predicted BOUNDED on this BW-bound model | LOW-MEDIUM - real kernel change (nwarps x tile_C coupling) | test-backend-ops MUL_MAT_ID + md5 | YES |
| **C lm_head kernel swap** | 0 to ~3 ms (~0.9%, realistically ~0; uncertain it beats nvjet) | ~0 | LOW payoff - high kernel-writing effort, not guaranteed to beat cublasLt | md5 (BF16-rounded logits) | YES (essentially) |
| **C persistent cgraph** | ~0.2 ms (~0.05%) | ~0 (B's re-graph already covers MoE host gap) | MEDIUM - touches ggml graph lifetime, for 0.05% | replay-only = bit-exact, md5 | YES |
| **D f16 glue (Option 2)** | ~11-16 ms = 40-60% of residual -> 91.8% -> ~95-96% (NOT a close) | ~0 (dense-only lever) | LOW-MEDIUM - new norm.cu f16 kernels, multi-file | **NON-bit-exact, must pass the SAME KL<1e-3 that plain bf16-SSM FAILED** | NO - opt-in only |

Notes that decide the ranking:
- **B's re-graph helps ONLY MoE** (dense decode is already graphed; the disable is the MoE
  MUL_MAT_ID `ne[2]>8` over-guard). It is the single highest-ROI item because it is already
  built, measured, and gated - zero remaining build risk, just a default flip.
- **A is the only lever that moves dense ABOVE vLLM** (103-108%) and it does it at-or-above
  vLLM precision (vLLM keeps ALL temporal state f32; A keeps f32 on exactly the unsafe heads).
  It reaches the largest mass (recurrence = 49.3% dense / ~48% MoE = ~6x what D can touch).
- **C and D are dead-or-tiny for the bit-exact default.** C's bit-exact ceiling is <1% with
  real risk; D is non-bit-exact, dense-only, and tops out at ~96% parity (not a close).

## (2) Ranked build order (gain x tractability x gate) - A confirmed as the build lead

1. **B re-graph (patch 0025) - LAND NOW.** Already built + measured + both gates PASSED. The
   only remaining decision is flipping the default from env-gated (`LLAMA_MOE_FORCE_GRAPHS`) to
   `should_use_mmq`-gated default-ON. Zero new build, measured +1.9-4.4% MoE, bit-exact. This
   is not a "build" so much as a "ship"; it precedes A because it is free and de-risked.
2. **A hybrid per-head SSM - THE BUILD LEAD (user-greenlit, CONFIRMED by evidence).** The only
   lever that takes dense ABOVE vLLM and the only principled fix for the bf16-SSM KL failure.
   Largest reachable mass, bounded build on an existing diff, KL-gated. Build plan in (3).
3. **B M1 down_proj act-quant retune** - cheap bit-exact bank-shot, run after A while the GPU
   is warm. Bounded (~2% act-quant tax), byte-identical-output retune.
4. **B mmq_y-down warp-remap** - only if 1+2+3 leave MoE short of target; real kernel work,
   predicted bounded on this BW-bound model.
5. **C persistent cgraph** - a bit-exact ~0.05% micro-win for the default; build only if a
   broad graph-lifetime refactor is happening anyway (not worth a standalone effort).
6. **C lm_head BF16 kernel swap** - near-zero, uncertain, high effort. Effectively shelved.
7. **D f16 glue (Option 2 norm.cu kernels)** - LAST, opt-in only, non-bit-exact, dense-only,
   gated by the same KL threshold bf16-SSM failed. Build only if the last ~4% dense is chased
   AFTER A lands and is shown insufficient. Skip Option 1 entirely (cast overhead eats the win).

**Why A over B as the lead, despite B's re-graph being measured:** B's re-graph is already
DONE - it is a ship, not a build. For the NEW build effort, A is correctly the lead: it is the
only lever with a path ABOVE vLLM on dense, it attacks the largest mass (recurrence, shared by
both models), and it converts the already-proven whole-bf16 win (490 t/s = 125% vLLM, but KL
FAIL) into a KL-passing form. B's remaining items (M1, mmq_y) are bounded single-% bank-shots
that cannot reach parity on their own (the residual MoE gap is the FP4 grouped GEMM at the
LPDDR5x BW floor + bf16 projections, both structural). So: ship 0025, then build A, then bank B.

## (3) CONCRETE A BUILD PLAN (hand to the build agent)

**Objective:** a per-head mixed-dtype SSM state cache - f32 on long-memory heads, bf16 on
fast-decaying heads - that captures 50-70% of the whole-bf16 recurrence win (-25% to -35%/call)
while PASSING KL<1e-3. Builds directly on the existing `BF16_SSM_STATE.diff` (untracked backup
on DGX `~/llama-paged-dev`). Target dense ~430-454 t/s (103-108% of vLLM 419), MoE +13-25%
recurrence share KL-passing. f32 default stays bit-exact (md5 == 0023 baseline).

**Reuse VERBATIM from BF16_SSM_STATE.diff** (do NOT rewrite): `gdn_state_t<STATE_BF16>` alias,
templated `__bfloat162float` load / `__float2bfloat16` store, the gather template, the dtype-
detect dispatcher, `type_s`/`type_r` cparam wiring, the CPU mirror, the back-compat row convert,
the bf16 fill path, and the test-backend-ops bf16 cases.

**NEW work items (in build order):**

1. **Head classifier (~80-150 LOC, do first, no GPU).** Host function over `ssm_a` (tensor
   `SSM_A_NOSCAN`, `[n_v_heads]`, = `-exp(A_log)`) and `ssm_dt` (tensor `SSM_DT`, `[n_v_heads]`):
   for each (layer il, head h) compute `tau_h = 1 / (|ssm_a[il][h]| * softplus(ssm_dt[il][h]))`;
   set `head_is_bf16[il][h] = (tau_h <= T_thresh)`. Emit per-layer `n_f32`/`n_bf16` counts +
   the `head_slot[il][h] = {is_bf16, local_idx}` map. Add cparam `ssm_hybrid_tau_thresh` / CLI
   `--ssm-bf16-tau` (inf => all-f32 bit-exact default; 0 => all-bf16; hybrid band in between).
   Runs in microseconds at load, no data, no GPU. (Optional Tier-2: a short calibration pass
   measuring per-head time-mean of actual `exp(g[h,t])` -> model-hash sidecar; only if Tier 1
   lands just above the gate.)
2. **Split-dtype cache layout (~150-250 LOC - THE BIGGEST piece).** In
   `llama-memory-recurrent.cpp`: replace the single `s_l` ([S_v,S_v,H,slots] f32) with two
   dtype-homogeneous sub-caches sized by per-layer head COUNT (this is what saves the bytes):
   `s_l_f32 [S_v*S_v*n_f32, slots]` f32 + `s_l_bf16 [S_v*S_v*n_bf16, slots]` bf16. In
   `build_rs` (`delta-net-base.cpp`): build the two views + pass the `head_slot` map; split the
   `n_embd_s` accessors. q/k/v/g/beta KEEP natural head order (no activation permute - they come
   from the projection GEMMs). Coarser per-LAYER fallback is REJECTED (long-memory heads span
   most layers => too coarse; per-head is the right granularity).
3. **Recurrence kernel: single launch, runtime per-head branch (~120-200 LOC).** Pass BOTH
   bases (`const float* s_f32_base`, `const nv_bfloat16* s_bf16_base`) + the two `state_dst`
   partition views + the device `head_slot[]` map. Branch on `head_slot[h_idx].is_bf16` at the
   load site, the in-place store site, the gather, and the dispatcher. The branch is UNIFORM
   within a block (all threads share `h_idx` = `blockIdx.x`) => **NO warp divergence**. The
   recurrence math (the ~140-260 region) stays byte-for-byte f32-register, untouched. `keep_rs_t`
   snapshots stay f32 (op-output scratch). The `STATE_BF16` template stays as the all-bf16
   special case.
4. **ids / in-place per-head.** `state_dst` becomes two partition views; `gdn_gather_nonident`
   becomes per-head dtype-aware (copies each head's `S_v*S_v` block from the right partition of
   `cache[ids[s]]`; still disjoint-scratch race-free). Each head writes its own partition slot
   (read==write slot, loaded to registers before store) => the identity / in-place property is
   preserved.
5. **CPU mirror (ops.cpp)** per-head dtype branch for CI / CPU-offload parity.
6. **test-backend-ops: a MIXED-dtype-state GATED_DELTA_NET case** (some heads f32, some bf16)
   vs the CPU ref, covering decode + multi-token prefill + `keep_rs_t` (this is the R2
   silent-corruption net - do NOT skip it).
7. **Gate (GPU, GateBench harness, already built).** Sweep `T_thresh` to find the MINIMUM f32
   fraction that passes: noise floor first, then the 256-tok KL gate, then the long-context
   drift sweep 256/1024/2048/4096, BOTH models (dense q36-27b + MoE q36-35b-a3b). Pass bar =
   **KL<1e-3 AND Same-top-p>=99.5% AND drift bounded**. nsys per-call confirms `f_bytes` =
   `(n_f32 + n_bf16/2)/H` dropped. md5 that `T_thresh=inf` reproduces the f32 baseline (the
   bit-exact opt-out MUST be preserved).

**Expected result (from the physics + the whole-bf16 measurement):** KLD contribution per head
~ `(eps*tau_h)^2` (eps~2^-8~3.9e-3) is dominated by the top-tau heads, so removing the top
~25-40% by tau cuts MeanKLD by 1-2 orders. Design band **f32 fraction f in [0.30, 0.50]**:
- f=0.30 (n_bf16/H=0.70): `f_bytes`=0.65 -> ~2.20 ms/call (-35%), captures ~70% of the bf16
  win -> dense **~454 t/s = ~108% of vLLM** (gate-likely, MeanKLD ~1e-3..1e-2).
- f=0.50: `f_bytes`=0.75 -> ~2.54 ms/call (-25%), captures ~50% -> dense **~430 t/s = ~103% of
  vLLM** (most robust pass; strict KL<1e-3 may need this fraction).

The exact f is found by the T_thresh sweep. **MoE:** A keeps the +13-25% recurrence share
KL-passing but does NOT by itself close the MoE GEMM gap (that is B). Joint ship gate = nsys
per-call bytes down AND KL<1e-3 for BOTH models; neither alone ships. Hybrid is STRICTLY safer
than vLLM (we keep f32 exactly where bf16 is unsafe; vLLM keeps all-f32 everywhere).

## (4) Ordered B / C / D queue with build triggers

- **B-1 re-graph default flip (patch 0025): trigger = NOW / immediate.** Already built, measured
  (+1.9-4.4% MoE), both gates PASSED. Flip env-gated -> `should_use_mmq`-gated default-ON. No
  dependency on A. Ship first.
- **B-2 down_proj act-quant retune (M1): trigger = after A's kernel work lands** (reuse the warm
  GPU window). Bit-exact block/grid retune of `quantize_mmq_nvfp4`, byte-identical output.
  Bounded ~1% (act-quant is ~2% of the MoE step). Run it; it is cheap.
- **B-3 mmq_y-down warp-remap: trigger = ONLY if B-1 + B-2 + A leave MoE below the target.**
  Real kernel change, BW-neutral, predicted bounded on this BW-bound model. Speculative; gate by
  test-backend-ops MUL_MAT_ID + md5.
- **C-1 persistent cgraph: trigger = ONLY if a broader ggml graph-lifetime refactor is already
  in flight.** Standalone it is ~0.05%, not worth the graph-lifetime touch. Bit-exact (replay).
- **C-2 lm_head BF16 kernel swap: trigger = effectively NEVER for the default** (0 to ~3 ms,
  uncertain it beats nvjet, high effort). Documented; not queued.
- **D Option 2 f16-glue norm.cu kernels: trigger = ONLY if dense parity is still wanted AFTER A
  lands AND A is shown insufficient, AND an opt-in non-bit-exact mode is acceptable.** Multi-file,
  recovers ~11 ms (norm/elementwise band), gated by the SAME KL<1e-3 that plain bf16-SSM failed.
  Skip Option 1 (net-zero cast overhead). Lowest priority of all.

**Bottom line:** ship 0025 now (free, measured MoE +1.9-4.4%), then build A (the only path
ABOVE vLLM on dense, KL-gated, ~430-454 t/s = 103-108% of vLLM), then bank B-2/B-3 on MoE. C is
last for the bit-exact default (<1%, dead-end); D is opt-in-only and dense-only, behind the KL
gate, only if the last ~4% is ever chased. The recurrence is already PAST vLLM; A converts that
proven win into a KL-passing form, and the MoE GEMM floor (the structural residual) is the one
piece no bit-exact lever fully closes - vLLM ships purpose-built Marlin-NvFp4 there.

Assisted-by: Claude:opus-4.8 [Claude Code]
