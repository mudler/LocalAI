# MOE_GAP_VS_VLLM.md - ground-truth both-engine MoE decode decomposition (where vLLM's ~15% lives)

> **READ THE FINAL SECTION FIRST ("RESIDUAL-ASSESS (FINAL)" at the bottom).** It concludes the hunt and
> CORRECTS one premise used throughout the body below: this doc assumes vLLM runs the GDN/attn projections
> as NVFP4-Marlin. It does NOT. vLLM runs the same nvidia-modelopt checkpoint that keeps them BF16, so the
> projection bucket is a matched-precision (bf16) gap, not a quant gap. Lever 4 (NVFP4 the projections) is
> REJECTED (+6% PPL, and not even a vLLM gap). The MoE is at its bit-exact ceiling (~86-88% of vLLM).

THE GPU AGENT (label `moe-gap-groundtruth`), DGX GB10 (sm_121). First **side-by-side, both-engine,
per-kernel ms/step** decomposition of the MoE decode gap. All prior B work decomposed llama ONLY; this
profiles vLLM's decode step too and computes the per-bucket `llama - vLLM` delta to pinpoint the gap.

Model `q36-35b-a3b-nvfp4` (40 layers: 30 GDN linear-attn + 10 full-attn, 256 experts top-8, vocab 248320).
Both engines profiled at **batch 128 decode** with `nsys --cuda-graph-trace=node`, steady-decode window,
per-step normalized by GDN-kernel-count / 30 (cross-checked vs flash/reshape_cache counts and throughput).

- **llama**: `build-cuda` tip `2f4f5ab` (patch 0025), `llama-batched-bench -npp 128 -ntg 128 -npl 128
  -c 32768 -fa on`, `LLAMA_KV_PAGED=1 LLAMA_MOE_FORCE_GRAPHS=1` (the re-graph ON = the 752 t/s ship point).
  Measured **S_TG = 752.3 t/s** => **step = 169.8 ms**, GPU-busy 97.5% (idle 2.5% = 4.2 ms/step).
- **vLLM 0.23.0**: `q36-35b-a3b-nvfp4-vllm`, **CUDA graphs ON** (`cudagraph_mode=FULL_AND_PIECEWISE`,
  the 882-reference config, NOT enforce_eager), MARLIN NvFp4 MoE, 128 seqs x 128-tok prompt x 128 gen.
  Measured **step = 142.0 ms** (= 901 t/s-equiv), GPU-busy 99.7% (idle 0.3% = 0.4 ms/step).
- Gap reproduced: **169.8 - 142.0 = 27.8 ms/step** (llama 83.6% of vLLM here; matches the ~85% server number).

## THE HEADLINE: the MoE grouped GEMM is NOT vLLM's advantage - it is a llama WIN

Grouped MoE-expert GEMM, isolated by per-call duration (LONG calls = the per-expert grouped GEMM):

| grouped MoE-expert GEMM | ms/step | what |
|-------------------------|--------:|------|
| **llama** `mul_mat_q<NVFP4,M-tile=64>` (+stream-k fixup + gather) | **48.3** | native Blackwell FP4-MMA **W4A4** |
| **vLLM** `marlin_moe_wna16::Marlin` | **50.0** | **W4A16** (FP4 weights dequant-in-kernel -> bf16 MMA) |

**llama's native-FP4 grouped GEMM is ~1.7 ms/step FASTER than vLLM's Marlin W4A16 at the ragged
tiny-M (~4 rows/expert) decode shape** (pure GEMM core 47.3 vs 50.0). Both read the same ~4-bit weight
bytes and are bandwidth-bound, so they tie to within a few %, and llama's 2x-rate FP4-MMA edges it.
**=> Marlin is NOT faster here; a Marlin-style W4A16 MoE GEMM in llama would make the MoE GEMM SLOWER.**
This directly answers the brief's load-bearing question #1/#2 and extends the prior `w4a16-marlin` DENSE
conclusion ("the win was NVFP4-dense-quant, not the Marlin kernel") to MoE: **the MoE GEMM kernel is not
the lever; llama already beats Marlin there.**

## Side-by-side per-step decomposition (ms/step, kernel-time attribution)

| bucket | llama ms | vLLM ms | Δ llama-vLLM | note |
|--------|---------:|--------:|-------------:|------|
| **Recurrence / SSM**           | **79.3** | **72.7** | **+6.6** | core kernel is a llama WIN (70.0 vs 71.1); the gap is llama's state-gather/conv plumbing |
| **MoE-expert grouped GEMM**    | 48.3 | 50.0 | **-1.7** | **llama FASTER** (native FP4-MMA W4A4 vs Marlin W4A16) |
| **Dense projections (+glue)**  | **20.3** | **13.8** | **+6.5** | llama runs GDN/attn projections in BF16 cublas; vLLM runs them as compact NVFP4-Marlin; +2.9 ms is llama's bf16<->f32 `convert_unary` glue vLLM never pays |
| **Norms / glue / memcpy**      | 9.6 | 6.0 | +3.6 | llama `k_bin_bcast` (expert-combine+residual) 4.3 + memcpy 2.4 heavier |
| **Act-quant (W4A4 tax)**       | 3.3 | 0.0 | **+3.3** | `quantize_mmq_nvfp4`; vLLM W4A16 keeps acts bf16 => structurally ZERO |
| **Router / align**             | 2.4 | 0.5 | +1.9 | llama computes router via a full FP4 GEMM (1.6) + argsort/scatter; vLLM fuses topk/align |
| **Attention (full-attn)**      | 2.8 | 2.6 | +0.2 | parity |
| kernel-time subtotal           | 166.1 | 145.7 | +20.4 | |
| **GPU idle (host bubble)**     | 4.2 | 0.4 | **+3.8** | graph coverage: llama partially-graphed (0025) vs vLLM FULL_AND_PIECEWISE |
| cross-stream overlap (union<sumdur) | ~0.8 | ~4.0 | ~-3.2 (vLLM overlaps more) | vLLM runs more kernels concurrently |
| **STEP TOTAL (wall)**          | **169.8** | **142.0** | **+27.8** | |

### Per-engine top kernels (ms/step)

```
llama (752 t/s, step 169.8 ms, 97.5% busy)        vLLM (901-equiv, step 142.0 ms, 99.7% busy)
 70.0  gated_delta_net_cuda          REC core      71.1  fused_recurrent_gated_delta   REC core
 47.3  mul_mat_q grouped MoE (M=64)  MoE GEMM       50.0  marlin_moe_wna16::Marlin      MoE GEMM
  8.2  nvjet 192x136 (bf16 proj)     PROJ            4.0  nvjet 128x72 (bf16 proj)      PROJ
  5.2  k_get_rows_float  REC-GATHER  REC <-- vLLM    2.8  marlin dense (lm_head NVFP4)  PROJ
  4.5  cutlass::Kernel2 (bf16 GEMM)  PROJ           has   2.7  nvjet 128x64 (bf16 proj)  PROJ
  4.3  k_bin_bcast (combine+resid)   GLUE           no    2.5  flash_fwd_splitkv         ATTN
  4.1  nvjet 128x64 (bf16 proj)      PROJ           equiv 2.0  marlin dense small (NVFP4) PROJ
  3.4  ssm_conv_update_f32           REC            of    1.6  causal_conv1d_update      REC
  3.3  quantize_mmq_nvfp4  W4A4 TAX   ACTQ <-- vLLM  these 1.4  std::enable_if (glue)     GLUE
  2.9  convert_unary bf16<->f32      PROJ-GLUE <--   two   1.2  reduce_kernel             GLUE
  2.8  flash_attn_tile               ATTN           (5.2+  1.0  cutlass::device (fp8 lin) PROJ
  2.4  MEMCPY-Device (SSM state)     GLUE           2.9 =  0.8  nvjet 32x64               PROJ
  1.6  mul_mat_q router (M=128)      ROUTER          8 ms  0.4  act_and_mul (SwiGLU)      GLUE
  1.5  rms_norm_f32                  GLUE           pure   0.2  topkGating / moe_align    ROUTE
  ...                                               llama  0.1  reshape_and_cache_flash   ATTN
                                                     tax)
```

## WHERE THE 27.8 ms ACTUALLY IS (ranked) - and it is NOT the Marlin GEMM

1. **Dense projections + bf16<->f32 glue: +6.5 ms.** llama keeps the GDN/attn linear projections (and
   the lm_head) in **BF16** (cublas `nvjet`/`cutlass`, full-precision weight reads) and pays a 2.9 ms
   `convert_unary` bf16<->f32 tax around them; vLLM runs the same projections as **compact NVFP4-Marlin
   W4A16** (4-bit weight read, ~4x less BW) and stays bf16 end-to-end (no convert). This is the
   **`NVFP4-dense-quant` lever the prior `w4a16-marlin` project already identified - applied to the
   still-bf16 projections**, not the MoE GEMM.
2. **Recurrence state-gather/conv plumbing: +6.6 ms.** The recurrence CORE kernel is a **llama win**
   (gated_delta_net 70.0 vs vLLM fused_recurrent 71.1, confirming "past vLLM on BW efficiency"). The gap
   is entirely the surrounding plumbing: **`k_get_rows_float` 5.2 ms (the recurrent-state gather)** +
   `ssm_conv_update` 3.4 vs vLLM's single `causal_conv1d_update` 1.6. vLLM has **no gather** - its
   recurrent state is updated in-place inside the fused decode kernel. `k_get_rows` is the single biggest
   llama-specific kernel vLLM has no equivalent of.
3. **Graph coverage + stream overlap: ~+7.0 ms combined** (idle +3.8, cross-stream overlap ~+3.2). vLLM
   FULL_AND_PIECEWISE is 99.7% busy with more concurrent kernels; llama (partially graphed post-0025) is
   97.5% busy with thinner overlap.
4. **W4A4 act-quant tax: +3.3 ms.** `quantize_mmq_nvfp4`; vLLM's W4A16 choice makes this structurally 0.
   Fusing the quant into the preceding op (as vLLM fuses act_quant into RMSNorm/SiLU) would erase it.
5. **Router GEMM + norms/glue: +5.4 ms.** llama computes router logits via a full FP4 GEMM (1.6) and has
   heavier `k_bin_bcast` combine/residual + memcpy; vLLM fuses routing into tiny topk/align kernels.

## THE SINGLE BIGGEST vLLM-MoE ADVANTAGE

**Not the Marlin GEMM.** It is a near-tie between two ~6.5 ms buckets, both bf16-precision-related:
- **Dense projections (+6.5 ms)** - vLLM runs the GDN/attn projections + lm_head as NVFP4-Marlin while
  llama runs them BF16 + a 2.9 ms convert tax. Single biggest *bucket* delta.
- **Recurrent-state gather (+5.2 ms, kernel `k_get_rows_float`)** - the single biggest *kernel* vLLM
  avoids entirely (in-place fused state vs llama's separate gather). Plus +1.8 ms more REC plumbing.

The MoE grouped GEMM (the brief's hypothesis) is a **-1.7 ms llama win**, so it is explicitly ruled out.

## ANSWERS TO THE BRIEF

1. **WHERE is vLLM's 15%?** Spread across bf16-projection BW (+6.5) + recurrence state-gather plumbing
   (+6.6) + graph/overlap (+7.0) + act-quant tax (+3.3) + router/glue (+5.4). **NOT the MoE GEMM.**
2. **Is Marlin faster at tiny-M decode?** **No.** llama native FP4-MMA W4A4 = 47.3 ms vs Marlin W4A16 =
   50.0 ms. Marlin is ~5% slower here; both are at the LPDDR5x BW floor.
3. **Should llama implement a Marlin-style W4A16 MoE GEMM?** **No** - it would slow the MoE GEMM and is
   not where the gap lives. The `w4a16-marlin` DENSE verdict ("NVFP4-dense-quant, not the Marlin kernel")
   carries to MoE. The real, ordered levers are: **(a) NVFP4-quantize the still-bf16 GDN/attn projections
   + lm_head** (close ~+6.5, the largest, bit-changing but the same class of move vLLM makes); **(b) fuse
   away the recurrent-state gather `k_get_rows`** (~+5, bit-exact, the biggest single-kernel win);
   **(c) fuller CUDA-graph coverage + stream overlap** (~+7, bit-exact); **(d) fuse the W4A4 act-quant
   into the preceding op** (+3.3, bit-exact). None of these is a new MoE GEMM.

---

# FINAL DECISION (cross-agent synthesis) - "can we do what vLLM does on MoE?"

Three agents converged on the same verdict from independent angles: `moe-gap-groundtruth`
(the measured both-engine nsys decomposition above), `vllm-marlin-study` (source-read of vLLM's
`moe_wna16_marlin_gemm` / `moe_align_block_size` / `prepare_nvfp4_moe_layer_for_marlin` on the DGX),
and `marlin-port-feasibility` (read-only assessment of the dense W4A16 scaffold + prior STOP). All
three agree, and the measurement is the arbiter. Below is the decision the user asked for.

## (1) WHERE the 15% lives - decisive

The gap is **27.8 ms/step (llama at 83.6% of vLLM)** and it is **NOT one kernel - it is a sum of small
deltas, and the MoE grouped GEMM is on llama's side of the ledger.** Ranked:

| rank | lever | Δ ms/step | bit-exact? | this is... |
|-----:|-------|----------:|:----------:|------------|
| 1 | Graph coverage + cross-stream overlap | ~+7.0 | **yes** | scheduler/runtime (idle +3.8, overlap +3.2) |
| 2 | Recurrence state-gather/conv plumbing (`k_get_rows_float` 5.2 + conv) | +6.6 | **yes** | llama-only kernels; vLLM updates state in-place |
| 3 | Dense GDN/attn projections + lm_head (bf16 vs NVFP4) + convert glue | +6.5 | **no** | the NVFP4-dense-quant lever, on the projections |
| 4 | Router GEMM + norms/combine/memcpy glue | +5.4 | mostly yes | llama router = full FP4 GEMM; vLLM fuses topk/align |
| 5 | W4A4 act-quant tax (`quantize_mmq_nvfp4`) | +3.3 | **yes** | vLLM's W4A16 makes this structurally 0 |
| - | **MoE-expert grouped GEMM** | **-1.7** | - | **llama WIN** - native FP4-MMA W4A4 47.3 vs Marlin W4A16 50.0 |

**The Marlin GEMM is explicitly ruled out as the source of the gap.** Both engines read the same ~22 GB
of ~4-bit expert weights once per step and are LPDDR5x-bandwidth-bound; on that weight stream they tie,
and llama's 2x-rate FP4-MMA edges Marlin's half-rate bf16 MMA. It is **not the projections-vs-Marlin
distinction in the experts, it is the projections in the DENSE path, the recurrence plumbing, and the
runtime/graph** that cost llama the 15%. Not distributed, not the expert GEMM, not routing alone.

## (2) Can llama MATCH it - and HOW

**Yes - to within a few percent, and NOT with a Marlin/W4A16 MoE GEMM.** The two biggest *compute*
kernels (the gated-DeltaNet SSM core 70.0 vs 71.1, and the MoE grouped GEMM 47.3 vs 50.0) are **already
llama wins.** The gap is overhead/scheduling/precision-of-the-other-tensors, all of which llama can
attack on its existing W4A4 FP4-MMA expert path. The four levers, in recommended build order:

| order | build | gain | bit-exact / gate | effort |
|------:|-------|-----:|------------------|--------|
| 1st | **Fuse away the recurrent-state gather `k_get_rows_float`** (update SSM state in-place in the GDN decode path, fold `ssm_conv_update`) | ~+5 ms (~3% of step) - biggest single-kernel win | **bit-exact** (no md5 rebaseline) | medium - CUDA, the GDN decode kernel |
| 2nd | **Fuller CUDA-graph coverage + stream overlap** (extend the 0025 re-graph to the remaining MoE/projection nodes, overlap independent streams) | ~+7 ms combined; 0025 already banked ~+1.9% | **bit-exact** | medium - scheduler, partly done |
| 3rd | **NVFP4-quantize the still-bf16 GDN/attn projections + lm_head** (the same move vLLM makes on its dense path; 4-bit weight read ~4x less BW, kills the 2.9 ms bf16<->f32 convert) | ~+6.5 ms - biggest *bucket* | **bit-changing** (re-baselines md5 gates; precision-UPGRADE, see below) | medium-high - new NVFP4 weight path for non-expert linears |
| 4th | **Fuse the W4A4 act-quant into the preceding RMSNorm/SiLU** (as vLLM fuses act-quant) | +3.3 ms | **bit-exact** | low-medium |

**Reach:** the three bit-exact levers (1+2+4 ~= +15.3 ms) alone close the gap to ~154.5 ms/step
=> ~830 t/s = **~94% of vLLM, with zero precision change and zero md5 rebaseline.** Adding the
NVFP4-projection lever (3, +6.5) reaches ~148 ms => ~865 t/s = **~96-97% of vLLM**, with the residual
being router/glue and the irreducible cross-stream-overlap that is structural to how ggml schedules
host-launched nodes vs vLLM's single fused graph. Because llama's two heaviest kernels are already
ahead, **parity-or-better is physically reachable** once the plumbing/overhead is removed; vLLM has no
arithmetic advantage on this hardware (its W4A16 is half-rate FP4 - it only wins on overhead and on the
dense-path weight-read BW).

## (3) The leading lever, in full - and the Marlin question, settled

**The user's specific hypothesis - "do what vLLM does = a Marlin-style W4A16 grouped MoE GEMM" - is
REJECTED, by measurement and by feasibility.**

- **It is not where the gap is.** The MoE GEMM is a **-1.7 ms llama win.** A W4A16 Marlin MoE GEMM would
  make that bucket SLOWER (half-rate bf16 MMA on the ~27% GEMM bucket), not faster.
- **Its entire intrinsic upside is the ~2% act-quant tax** (W4A16 has no activation quantize). That
  +2% ceiling is **smaller than the +1.9% the bit-exact 0025 re-graph already banked**, at vastly higher
  effort and with a precision change. And the act-quant tax is independently closeable bit-exactly by
  lever 4 (fuse it into the preceding op) without touching the GEMM.
- **The scaffold does not help.** `paged/kernel/w4a16/marlin-w4a16.cu` is dense-only, Q4_0/Q4_K, with no
  grouped/MUL_MAT_ID path and no NVFP4 dequant. A real MoE Marlin is effectively a from-scratch port of
  `moe_wna16_marlin_gemm` (per-expert M-tiles, block-padded `moe_align` token-sort, stream-K over ragged
  segments, NVFP4->bf16 in-kernel dequant). vLLM only reaches the BW floor via cutlass-SM120 TMA +
  warp-specialized pipelining; the GB10 occupancy-only route the dense scaffold tried **plateaued at
  ~9 TFLOPS / 178 t/s (~5x under MMQ)** and STOPPED at the occupancy wall (XOR-swizzle + deep cp.async
  collapse GB10 occupancy). Realistic outcome of an MoE port: **a net REGRESSION** on the 27% GEMM
  bucket. Multi-week, high-risk, DGX-only, no `ncu`, for a +2% ceiling. **Do not build it.**

**Why vLLM runs W4A16 at all:** not because it is better - because sm_121 (consumer Blackwell / GB10)
has no working cutlass FP4 MoE cubins (vLLM whitelists only sm_100/103 datacenter Blackwell for native
FP4 MoE; the engine literally warns it is falling back to "Weight-only FP4 ... Marlin kernel"). On GB10,
W4A16 is HALF the FP4-MMA rate. **llama's native W4A4 FP4-MMA is the higher hardware tier; matching vLLM
does NOT mean copying its W4A16 fallback.**

**Precision / gate (the brief's key nuance, assessed honestly):** the observation that W4A16 (bf16 acts)
is a strict activation-precision UPGRADE over W4A4 (FP4 acts), with better KL-to-f32, is **correct but
unmonetizable here.** (a) The current W4A4 MoE default is **already bit-exact to the f32 reference**
(test-backend-ops MUL_MAT_ID 806/806, greedy md5 stable on both models) - you get no quality credit for
being more precise than a default that already passes, and the precision-sensitive site is the
gated-DeltaNet SSM *state* (a different op, addressed by the separate 0026 bf16-SSM opt-in), not the MoE
GEMM. (b) W4A16 is **non-bit-exact vs the W4A4 default, so adopting it re-baselines every shipped md5
gate** - a real cost for a +2% throughput ceiling that is itself likely negative. So the precision angle
does not flip the verdict: it would be a precision upgrade nobody needs, bought with a slower,
occupancy-hostile, gate-rebaselining kernel. The one genuinely precision-positive AND throughput-positive
move that quantizes weights is **lever 3 (NVFP4 projections)** - and that is W4A16 on the DENSE linears
(where it cuts weight-read BW), not on the experts.

## (4) HONEST VERDICT + recommended build

**VERDICT: We can essentially match vLLM on MoE decode (~94% bit-exact, ~96-97% with the projection
quant, parity-or-better physically in reach), but NOT by doing "what vLLM does" in the sense the question
implies. A Marlin/W4A16 grouped MoE GEMM is the wrong lever - the MoE GEMM is already a llama win and a
W4A16 port would regress it. The 15% is bf16 dense-projection bandwidth + recurrence-gather plumbing +
graph/overlap overhead + a 2% act-quant tax + router glue. Every piece is closeable on llama's existing
native-FP4 expert path, mostly bit-exactly.**

**Recommended build (ship order, none of it a new MoE GEMM):**
1. **`k_get_rows` SSM-state-gather fusion** - bit-exact, ~+5 ms, biggest single-kernel win, no rebaseline. **Do first.**
2. **Extend CUDA-graph coverage + stream overlap** beyond 0025 - bit-exact, ~+7 ms combined, partly banked.
3. **Fuse the W4A4 act-quant into the preceding RMSNorm/SiLU** - bit-exact, +3.3 ms, erases the act-quant tax (the only thing W4A16 would have bought) without W4A16.
4. **NVFP4-quantize the bf16 GDN/attn projections + lm_head** - +6.5 ms (biggest bucket), bit-changing
   (re-gate md5; precision-UPGRADE, the same NVFP4-dense-quant move vLLM makes). Ship as default after
   re-gating, or as an opt-in if the md5 rebaseline is undesirable.

**Do NOT build:** the W4A16/Marlin grouped MoE GEMM (`paged/kernel/w4a16/` scaffold is dense-only and not
reusable). Neither default nor opt-in: +2% ceiling < the already-banked bit-exact +1.9%, likely a net
regression on the 27% GEMM bucket, multi-week high-risk, and it rebaselines every gate. The dense
`w4a16-marlin` STOP transfers to MoE, and MORE strongly (the tiny-M decode shape is purely BW-bound, so
the FP4-vs-bf16 tier is a wash that the weight-read floor erases - leaving only the half-rate downside).

Assisted-by: Claude:opus-4.8 [Claude Code]

---

# LEVER 4 (scope) - NVFP4-quantize the still-bf16 MoE GDN/attn projections (+lm_head), the +6.5 ms bucket

Label `L4-scope`, READ-ONLY (no GPU). This scopes lever 4 - the single biggest *bucket* in the table
above (**Dense projections +glue, +6.5 ms**) and the only remaining MoE lever with a real, measurable
gain after levers 2 and 3 both came back FLAT measurement-STOPs (no patch, no commit - see
`LEVER2_GRAPH_COVERAGE_RESULTS.md`, `LEVER3_ACTQUANT_FUSION_RESULTS.md`, `LEVERS_23_PROGRESS.md`). Lever 4
is **bit-changing** (re-gates md5; gate on KL-to-f32, not bit-exact md5). Below: the root cause, the
path, effort, the precision/KL story, the expected gain, and the default-vs-opt-in recommendation.

## Root cause: the MoE GGUF's projections are bf16 only because of its quant PROVENANCE

The "still-bf16 GDN/attn projections" are **MoE-specific, and they are an accident of how the MoE
checkpoint was quantized - not a llama limitation.** The two GGUFs have different quant lineages:

- **Dense `q36-27b-nvfp4` (unsloth, native-Blackwell FP4, 304 NVFP4 tensors):** the GDN/attn projections
  ARE already NVFP4. Proven directly - `DECODE_PARITY_EXPLORE.md:594` shows the dense `ssm_out`
  (GDN out-projection) running as an **FP4 GEMV/MMQ** (`mul_mat_vec_q`/`mul_mat_q<NVFP4>`), and the
  in_proj runs FP4 MMQ at M=128. This is exactly why the **dense decode is already at 96.6% of vLLM** -
  it has essentially no bf16-projection bucket left.
- **MoE `q36-35b-a3b-nvfp4` (nvidia modelopt, 241 NVFP4 tensors):** modelopt quantized the **256-expert
  FFN** tensors to NVFP4 (the 241 count is dominated by the packed grouped-expert tensors) but **left the
  GDN/attn linear projections in BF16** - `in_proj_qkvz`, `in_proj_ba`, the GDN `out_proj`/`ssm_out`, and
  the full-attn `attn_q/k/v/output`. Those are exactly the **bf16 nvjet/cutlass projection GEMMs** seen in
  the MoE decode top-kernel list (8.2 `nvjet 192x136` + 4.5 `cutlass::Kernel2` + 4.1 `nvjet 128x64`)
  plus the 2.9 ms `convert_unary` bf16<->f32 glue = the **20.3 ms projection bucket** vs vLLM's 13.8 ms
  (vLLM runs the same projections, and on this modelopt checkpoint even its lm_head, as NVFP4-Marlin -
  see its `2.8 marlin dense (lm_head NVFP4)` kernel).

**=> Lever 4 is overwhelmingly a MoE-GGUF move:** bring the MoE GGUF's GDN/attn projections to the SAME
NVFP4 the DENSE GGUF already ships and that vLLM already runs on the identical weights. It is not a new
capability - the dense GGUF is the existence proof that llama runs and ships these projections in NVFP4.

## (1) THE PATH + EFFORT

Two ways to get the projection weights into NVFP4:

- **PATH A - offline re-quantize to a NEW GGUF variant (RECOMMENDED, = exactly what vLLM does).** Re-run
  `llama-quantize` on the MoE source with the `--tensor-type` selector EXPANDED to also capture the
  GDN/attn projection tensor-name patterns that the modelopt checkpoint left bf16 (the GDN `in_proj_*` /
  `out_proj`/`ssm_out` and full-attn `attn_q/k/v/output` weights), producing e.g.
  `q36-35b-a3b-nvfp4-projq.gguf`. **ZERO kernel/runtime code:** NVFP4 weights already flow end-to-end -
  the loader auto-creates the per-tensor NVFP4 sidecar scales when `type == GGML_TYPE_NVFP4`
  (`llama-model.cpp:1459`), and the projection GEMMs then route to the already-tuned `mul_mat_q<NVFP4>`
  (patch 0017) instead of cublas/nvjet. The dense GGUF is the live proof this path works and gates clean.
  **Effort: LOW-MEDIUM** - the only "build" is the quantize recipe + a KL gate harness + a gallery/index
  entry + a RELEASE note. Risk items: (i) confirm the exact bf16 tensor list with a CPU `gguf_dump`
  (metadata-only, no GPU); (ii) NVFP4 needs the contraction dim divisible by the 16-elt block - any
  projection whose row dim is not a multiple of 16 stays bf16 (or needs padding), which is the most
  likely reason a given tensor was left bf16 and must be checked per-tensor; (iii) the lm_head decision
  (below).
- **PATH B - runtime quantize bf16->NVFP4 at load.** Convert the bf16 projection weights in-memory at
  model load (one-time ue4m3 per-block scale-search), GGUF unchanged. **Worse choice:** needs new
  load-time quant code (MEDIUM), and it *silently* changes the output of an existing GGUF for current
  users (an implicit, non-opt-in precision change) - strictly inferior to an explicit new artifact.
  Only attractive if shipping a new GGUF is somehow impossible; it is not.

## (2) PRECISION / KL story (honest)

Quantizing the projection WEIGHTS bf16 -> NVFP4 (e2m1 + per-16 ue4m3 scale) is a per-weight precision
**downgrade vs the current bf16** on those specific tensors (it adds ~4-bit weight-quant error), and -
because they route to the W4A4 MMQ path - it also FP4-quantizes those projections' activations. It is
NOT a precision upgrade over bf16; it is the **same W4A4/W4A16-class move vLLM already makes on these
same projections**, so at matched precision it is apples-to-apples with vLLM. Non-bit-exact => **re-gate
on KL-to-f32, not md5.**

**KL estimate: should PASS with margin.** Three independent reasons: (a) the dense GGUF ALREADY ships
these GDN/attn projections in NVFP4 and passes its greedy gate (`5951a5b4...`), so the move is
empirically proven shippable on this architecture; (b) the 256 experts already run W4A4 NVFP4 and pass
(test-backend-ops MUL_MAT_ID 806/806, greedy md5 stable) - the GDN/attn projections are the same class of
linear op and arguably less sensitive than the expert FFN; (c) this is a per-step, **non-accumulating**
weight-quant error - structurally unlike the bf16-GDN-*state* experiment (`BF16_SSM_STATE_RESULTS.md`)
that FAILED the KL gate (KLD 0.06-0.17, ~10% argmax flips) because that error *accumulated* through the
recurrence. Expect KLD-to-f32 well under that failed-state threshold and PPL delta sub-percent (cf. the
broader NVFP4-dense ~+4.8% PPL-vs-Q4_K figure is for full-model NVFP4; here only a minority of residual
projection tensors move). **The one genuinely risky tensor is lm_head** (logit-direct; `OTHER_PATHS_
INVESTIGATION.md` flags NVFP4-lm_head can flip the greedy argmax). For the MoE, quantizing lm_head is
*fair* (vLLM's modelopt checkpoint already runs lm_head NVFP4), so include it but gate it explicitly on
argmax-agreement; if it flips the greedy probe, keep lm_head bf16 and bank only the GDN/attn portion.
Recommended gate: **KLD-to-f32 < the bf16-state failure floor (~0.06) AND PPL delta < ~1% vs the current
bf16-projection GGUF AND zero greedy-argmax flips on the -n 48 probe.**

## (3) EXPECTED MoE GAIN

Closing the +6.5 ms projection bucket = bringing llama's 20.3 ms projection bucket down to vLLM's
~13.8 ms (NVFP4 cuts the projection weight-read ~4x - 2.37 GB-class bf16 -> ~0.56 B/wt - and the W4A4
MMQ path stays in the quantized domain, **erasing the 2.9 ms `convert_unary` bf16<->f32 glue**). llama's
native FP4-MMA is faster per-FLOP than vLLM's W4A16-Marlin and these projections are BW-bound, so llama
lands at parity-or-slightly-better, same as the expert GEMM (where W4A4 beat Marlin by 1.7 ms). 

- With **lm_head also NVFP4** (fair on this modelopt MoE, vLLM did it): full ~**+6.5 ms** =>
  step 169.8 -> ~163.3 ms => ~785 t/s.
- With **lm_head kept bf16** (conservative): ~**+4 to +5 ms** (the GDN/attn projections + the convert
  glue; lm_head's ~bf16 GEMM stays) => step 169.8 -> ~165-166 ms => ~768-775 t/s.

In MOE_GAP frame (vLLM 142.0 ms / 901 t/s-equiv): **MoE moves from 86.3% (post-lever-1 / 0028) toward
~89-91% of vLLM** (full bucket) or ~88% (lm_head bf16). This is the **largest single banked MoE gain
available** - lever 1 (gather) shipped, levers 2 and 3 banked nothing, and the MoE GEMM is already a
llama win - so after lever 4 the residual is just router/glue + the structural cross-stream-overlap and
the ~4.2 ms host bubble (reachable only via a paged-attn host-pipeline edit, not a quant or graph knob).

## (4) RECOMMENDATION: ship as a SEPARATE OPT-IN gallery GGUF variant (KL-gated), not a re-gated default

**Ship lever 4 as a distinct, opt-in gallery variant** (e.g. `q36-35b-a3b-nvfp4-projq` / `-w4a4full`),
**not** as a silent replacement of the default MoE GGUF. Rationale:

1. The current default MoE GGUF is **md5-bit-exact-gated** (`07db32c2...` shipped); making it default
   forces a permanent md5 rebaseline of every gate - the hard line this whole track has held (levers 2+3
   STOPPED rather than cross it). A new artifact sidesteps that for users who chose the f32-lineage GGUF.
2. Path A produces a **new GGUF anyway** (offline re-quant), so a separate gallery entry costs nothing
   extra and makes the throughput<->precision choice explicit and reversible.
3. The gain (~+4-6.5 ms, ~86% -> ~88-91% of vLLM) is real but modest - not worth forcing a precision
   change on default-path users.
4. **Promotion path:** because lever 4 only brings the MoE GGUF to the SAME NVFP4 the dense GGUF already
   ships *as its default* and that vLLM already runs, a clean KL gate (KLD << 0.06, PPL delta < ~0.5%,
   zero argmax flips) is a strong case to PROMOTE the variant to the default MoE GGUF in a later release.
   Ship opt-in first to preserve the bit-exact default and avoid a forced rebaseline; promote if the
   gate is clean and lm_head NVFP4 holds.

**Effort summary:** LOW-MEDIUM, dominated by the KL gate + gallery wiring, NOT code (zero new kernel; the
NVFP4 weight path - loader sidecar scales + tuned `mul_mat_q<NVFP4>` - is already in tree and proven by
the dense GGUF). Highest-ROI remaining MoE lever. **Do first among remaining MoE work**, ahead of any
non-bit-exact recurrence-plumbing or the rejected W4A16/Marlin GEMM.

Assisted-by: Claude:opus-4.8 [Claude Code]

> **SUPERSEDED:** the lever-4 scope above was optimistic and PRE-GATE. The L4 KL gate FAILED
> (+6.15-6.51% PPL, see `LEVER4_PROJNVFP4_RESULTS.md`) and the premise was wrong (vLLM keeps these
> projections BF16 too). Lever 4 is REJECTED - do NOT ship. See the FINAL section below.

---

# RESIDUAL-ASSESS (FINAL, concludes the hunt) - convert-glue + bf16-GEMM verdicts, the bit-exact MoE ceiling

Label `residual-assess`, DGX GB10 (sm_121). After lever 1 shipped (0028, MoE 86.3% of vLLM @npl128,
bit-exact), levers 2+3 flat, lever 4 REJECTED (KL-gate FAIL, AND vLLM keeps the same projections bf16),
and lever 5 flat for MoE (host-side, off the compute-bound critical path; dense gets +0.41%), this is the
final honest assessment of the two remaining sub-levers inside the 20.3-vs-13.8 ms projection bucket.
Both are **bit-CHANGING or at-the-BW-floor.** The hunt is DONE.

## CORRECTION that reframes the projection bucket

The body above assumed **vLLM runs the GDN/attn projections as NVFP4-Marlin.** FALSE (confirmed by the L4
gate). vLLM runs the **same nvidia-modelopt checkpoint** as the GGUF, which keeps `in_proj_qkvz`,
`in_proj_ba`, `out_proj`, `attn_gate`, and full-attn `attn_q/k/v/output` in **BF16**. llama and vLLM run
these projections at the **same precision (bf16).** The +6.5 ms projection-bucket delta is therefore NOT
a precision/quant gap - it is (a) llama's f32-residual-stream convert tax and (b) bf16-GEMM kernel /
round-trip efficiency, both at matched bf16 precision.

## (1) convert-glue verdict (3.24 ms/step measured): NOT bit-exact eliminable

Empirical split (`moe_dec` nsys, per-step over 43 decode steps):
- `convert_unary<float,bf16>` (input, f32 act -> bf16): **1.73 ms/step**, 186 calls/step
- `convert_unary<bf16,float>` (output, bf16 -> f32): **1.52 ms/step**, 186 calls/step (equal count = every
  bf16 projection round-trips)

Source root cause (`ggml/src/ggml-cuda/ggml-cuda.cu:1663-1690`, the `src0->type == BF16` cuBLAS path):
ggml converts f32 activations to bf16, runs `cublasGemmEx` bf16xbf16 with **CUBLAS_COMPUTE_32F** but
writes the result to a **bf16** buffer (`dst_bf16`, `CUDA_R_16BF`), then widens bf16 -> f32. The f32
accumulator is **rounded to bf16 and then widened back** - it drops ~15 mantissa bits, and that
bf16-rounded value feeds the f32 residual stream.

- The **output round-trip is load-bearing for the shipped numerics.** The fp16-fp32-compute path 40 lines
  down (`:1729`, `dst CUDA_R_32F`) proves cuBLAS CAN write the f32 accumulator directly - so the bf16
  output write+convert is a removable ggml inefficiency. BUT removing it (f32-direct output) changes the
  value from "bf16-rounded" to "full-f32" => greedy md5 (`07db32c2`) re-baselines. It is a **precision
  boundary (an upgrade), exactly like lever 4.** NOT bit-exact.
- The **input convert is intrinsic** to a bf16 GEMM (cuBLAS needs bf16 inputs; ggml's residual stream is
  f32). The only bit-exact move is to fuse the f32->bf16 cast into the producing op's epilogue (same RNE
  rounding, one fewer launch) - but that is per-site ggml graph surgery for a sub-1.7 ms launch ceiling,
  and it is **subsumed by the (rejected) lever-4 move**: NVFP4-quantizing the weights routes the
  projection to `mul_mat_q<NVFP4>` (W4A4) and deletes the entire bf16 cuBLAS path - input convert, GEMM,
  output convert - in one shot.
- vLLM pays ~0 here because it runs an **end-to-end bf16 residual stream** (no f32 intermediate). Matching
  that = converting llama's residual stream to bf16 = a global precision change, md5 rebaseline. Also not
  bit-exact.

**Verdict: bit-exact-eliminable = NO.** The f32<->bf16 round-trip is load-bearing for the current md5 (the
bf16-rounded output IS the shipped value). Every way to remove it (f32-direct GEMM output, bf16 residual
stream, or NVFP4 weights) is bit-changing. The one bit-exact sliver (fuse the input cast into the
producer) is ~1.7 ms ceiling, high per-site effort, and redundant with lever 4. (Aside: the f32-direct
GEMM output is a genuine upstreamable ggml win - faster AND more precise - but it rebaselines md5, so it
is off the bit-exact table for this hunt.)

## (2) bf16 projection GEMM verdict (17.27 ms/step measured): BW-bound at the floor, no kernel lever

Per-step bf16-projection GEMM (nvjet cuBLASLt + cutlass bf16, `moe_dec` nsys): **17.27 ms/step, 225
calls/step.** Roofline at the M=128 decode shape:
- Arithmetic intensity ~= 2*M FLOP / 2 bytes-per-weight = **M = 128 FLOP/byte** (the weight read
  dominates; activations/output negligible at M=128).
- GB10: LPDDR5x unified BW ~= **273 GB/s**; bf16 tensor-core peak >= ~250 TFLOPS => ridge point ~=
  250e12 / 273e9 ~= **>900 FLOP/byte.** 128 << 900 => **memory-bandwidth-bound by ~7x.**
- Achieved: 17.27 ms at 273 GB/s = **~4.7 GB of bf16 projection weights streamed per step** - i.e. the
  GEMM moves the weight bytes at ~full LPDDR5x bandwidth. **It is at the BW floor.**

The nvjet kernels are `tmaAB` (TMA-streamed on both operands) - the optimal Blackwell weight-streaming
access pattern; vLLM's cutlass does the same and reads the **same bf16 bytes.** A cutlass swap cannot beat
the byte floor. The only way faster is **fewer weight bytes = quantize** (lever 4, ~4x fewer bytes) -
bit-changing AND rejected on quality (+6% PPL) AND not even a vLLM-parity gap. The residual ~3.5 ms of the
llama-vs-vLLM GEMM-bucket delta traces to llama's extra `dst_bf16` write+read round-trip traffic (the
convert glue of verdict 1), not a worse GEMM kernel.

**Verdict: at the bandwidth floor; no bit-exact (nor even same-precision) kernel lever exists.** nvjet
already streams the weights near-optimally.

## (3) The bit-exact MoE ceiling, and the irreducible residual

| MoE lever | status | bit-exact? | MoE gain |
|-----------|--------|:----------:|----------|
| 1 - recurrent-state gather fusion (0028) | **SHIPPED** | yes | banked -> 86.3% of vLLM |
| 2 - graph coverage / overlap | flat | yes | ~0 |
| 3 - act-quant fusion | flat | yes | ~0 |
| 5 - block-table within-step cache | flat for MoE | yes | ~0 (host off compute-bound path; dense +0.41%) |
| 4 - NVFP4 projections | REJECTED | no | +6% PPL, not a vLLM gap |
| convert-glue elimination | this assess | **no** (precision boundary) | bit-changing only |
| bf16-GEMM kernel | this assess | **no** (BW floor) | none |

**Realistic bit-exact MoE ceiling = ~86-88% of vLLM @npl128. The shipped state (lever 1, 86.3%) is
essentially AT it.** Lever 5 adds nothing to MoE. No clean bit-exact MoE lever remains.

**The irreducible ~12-14% residual to vLLM is structural, not a missing optimization:**
1. **f32-residual-stream convert tax (~3.2 ms/step)** - ggml runs an f32 graph and casts per bf16
   projection; vLLM runs bf16 end-to-end. Removing it is a precision change.
2. **bf16-GEMM BW floor + round-trip traffic (~3.5 ms/step)** - both engines at the LPDDR5x byte floor on
   bf16 weights; the delta is the round-trip traffic (= item 1, bit-changing).
3. **Recurrence-plumbing remainder** - mostly banked by lever 1; the core SSM kernel is already a llama
   win.
4. **Between-replay host loop + graph/overlap bubble** - sampling needs logits between graph replays;
   irreducible at this batch shape.

## CONCLUSION: the MoE-parity hunt is DONE

The MoE is at its bit-exact ceiling. The two heaviest MoE compute kernels (the gated-DeltaNet SSM core and
the NVFP4 expert grouped GEMM) are **already llama wins**, so there is no arithmetic gap to close. The
remaining 12-14% is the f32-vs-bf16 graph-precision tax, the bf16-weight BW floor, and the irreducible
host loop - none of which is a clean bit-exact lever, and the one bit-changing option (quantize the
projections) is rejected on quality and is not even a vLLM-parity gap. **No one-more-lever for MoE.** The
only clean win left in the whole track is DENSE (+0.41% from lever 5), gated behind first resolving the
pre-existing paged-MoE baseline md5 drift (paged `8cb0ce23` vs canonical `07db32c2`) the L5 finish flagged.

Assisted-by: Claude:opus-4.8 [Claude Code]
