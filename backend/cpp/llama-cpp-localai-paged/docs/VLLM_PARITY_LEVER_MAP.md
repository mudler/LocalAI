# vLLM Parity Lever Map

> Auto-generated from the parity-exploration workflow. Working artifact (the multi-week path to vLLM parity on prefill + decode, Qwen3.6 NVFP4 / GB10).

## 1. Prefill gap re-audit

I have walked the full prefill forward pass against the committed numbers (final_benchmark.csv, PREFILL_GEMM_SCOPE/RESULTS, the 0042 dense nsys profile, the qwen35moe/delta-net graph source). Here is the re-audit.

---

# PREFILL gap re-audit - Qwen3.6 NVFP4 on GB10

## Grounding (what the gap actually is)

From `docs/final_benchmark.csv`, prefill (S_PP, t/s; patched vs vLLM):
- **Dense 27B**: ~922 vs ~1929-2182 → patched is **44-48%** of vLLM.
- **MoE 35B-A3B** (the decision model): ~1510-2177 vs ~5186-6223 → patched is **29-41%** of vLLM. In us/tok at npl64: llama ~471, vLLM ~169 → **gap ~302 us/tok**.

The GEMM scope's bucket (~232 us/tok llama vs ~68 vLLM) = a **164 us/tok** GEMM difference = **~51-54% of the gap**, and GEMM is **~49% of the llama prefill wall** (232/471). GDN is cited at **~17% of the gap** (vLLM chunked scan ~2.5x cheaper). So GEMM+GDN ≈ **~68% of the gap** by the existing framing - leaving ~30% that the two levers' headline numbers do not name. This audit walks every op to place that residual.

Important structural facts confirmed from source (`models/qwen35moe.cpp`, `delta-net-base.cpp`, `llama-graph.cpp`):
- MoE = 40 layers (interval-4 → **30 GDN + 10 full-attention**), 256 experts top-8, **plus a dense shared expert on every layer**. Dense = 64 layers (48 GDN + 16 attn).
- **Default prefill GDN is NOT a single kernel.** `fused_gdn_ch`/patch-0031 is default-OFF, so prefill runs `build_delta_net_chunking` - a long graph of `ggml_mul`/`mul_mat`/`solve_tri`/`cumsum`/`tri`/`exp` + many `ggml_cont`/`transpose`/`pad`/`repeat` layout copies + a host-side per-chunk loop. The GDN lever (tensor-core fused kernel) is scoped to replace this **entire** decomposition, so the "11% k_bin_bcast op_mul gating muls" the 0042 patch calls "a separate lever" are in fact **inside the GDN bucket** (a fused GDN kernel subsumes them).

## Prefill op-share table (MoE decision model; % of the patched/llama prefill wall)

Estimates triangulated from the committed numbers (232/68 GEMM, 11%/5% from the 0042 dense nsys, the gap arithmetic), not a fresh nsys run.

| Op (prefill) | ~% of llama wall | vLLM faster? why | Covered by GEMM lever | Covered by GDN lever |
|---|---:|---|:---:|:---:|
| Token embed (`get_rows`) | <1% | tie | - | - |
| **NVFP4 weight GEMMs** total | **~49%** | **Yes** - vLLM W4A16-Marlin/cutlass large-M tiles + async pipeline vs MMQ small-tile / new FP4-MMA at 57.7% of peak | **YES** | - |
| ┝ routed-expert grouped GEMM (gate_up+down, `mul_mat_id`) | ~28% | yes (biggest single bucket) | yes | - |
| ┝ shared-expert dense GEMMs (all tokens, ×40) | ~9% | yes | yes | - |
| ┝ GDN in/out projections (wqkv, wqkv_gate, ssm_out) | ~7% | yes | yes | - |
| ┝ attention QKV/O projections (×10) | ~5% | yes | yes | - |
| **GDN chunked decomposition** (30 layers) | **~22%** | **Yes** - vLLM chunked scan ~2.5x cheaper (tensor-core intra-chunk vs llama's f32 graph ops + layout copies + host loop) | - | **YES** |
| ┝ gating/decay muls (`k_bin_bcast op_mul`) | ~11%* | yes | - | yes (fused kernel absorbs) |
| ┝ small f32 mul_mats + `solve_tri` + cumsum/tri/exp | ~7% | yes | - | yes |
| ┝ layout `cont`/`transpose`/`pad`/`repeat` copies | ~4% | yes | - | yes |
| **FlashAttention prefill** (QK^T·softmax·PV, 10 layers) | **~3-6%**† | maybe - L²-growing; bounded at npp=128, larger at serving context | **NO** | **NO** |
| **MoE router + combine/scatter** | **~5-8%** | **Yes** - vLLM fuses gather/weight/scatter into the grouped-GEMM epilogue | **NO** | **NO** |
| ┝ `argsort_top_k`(256→8) + softmax + weight-norm | ~2-3% | yes | no | no |
| ┝ combine: 7× fp32 `add` + weight `mul` (×40) | tested flat in Phase 7 | yes | no | no |
| **Activation quantization** (W4A4 e4m3 pass per GEMM) | **~3-6%** | **Yes - structurally**: vLLM W4A16-Marlin on GB10 has **no** activation-quant step | **NO**‡ | partial |
| Norm + residual tail (attn/post/q/k/ssm/l2/out + adds) | ~4% | small (0042 fused the main one) | - | - |
| RoPE + sigmoid/silu gates + scale | ~2-3% | small | - | - |
| LM head (last-token only in prefill) | <1% | tie | - | - |

\* 0042 dense profile; in MoE the relative share is a bit lower (MoE FFN is heavier). † grows quadratically - under-weighted at the benchmark's npp=128; re-measure at real serving lengths. ‡ the quant pass feeds the GEMM but is a *separate kernel*, not inside the GEMM-lever's mul_mat bucket.

## Verdict: GEMM + GDN are the two dominant buckets but NOT the whole gap

They cover ~71% of the prefill wall and the bulk of the gap. Three contributors are **materially uncovered** by either lever:

### Newly-identified lever 1 - MoE router + combine/scatter (the strongest miss on the decision model)
llama runs the expert routing and recombination as **separate memory-bound ggml ops**: `argsort_top_k` over 256 experts, softmax/normalize, then a fan-in of **7 fp32 `ggml_add` + a weight `ggml_mul`** per MoE layer (`llama-graph.cpp` ~1797-1824), every one of 40 layers. vLLM's fused-MoE (and Marlin grouped) path folds the gather, the router-weight multiply, and the scatter-accumulate into the **GEMM epilogue/prologue** - so this is overhead vLLM essentially does not pay. Est. ~5-8% of the MoE prefill wall, entirely outside GEMM (the `mul_mat_id` is covered; the surrounding argsort/adds/mul are not) and outside GDN.

Phase 7 challenged the smallest version of this lever: a CUDA-only post-down
weighted-combine fusion that removed the separate router-weight `mul` plus
rank-order add fan-in while preserving md5. It passed `MOE_WEIGHTED_COMBINE`
`7/7`, `MUL_MAT_ID` `806/806`, and canonical MoE/dense md5 gates; Nsight proved
the fused kernel launched (`110` `k_moe_weighted_combine` calls). Serving A/B was
flat (`decode_agg_tps 417.5 disabled -> 417.0 fused`), so the fan-in-only patch
was rejected. The remaining plausible lever is a larger fused-MoE
prologue/epilogue that also removes gather/scatter or moves work into the GEMM
kernel, not another standalone fan-in fusion.

Phase 8 scopes that remaining lever as profile-gated ragged serving dispatch:
first measure llama.cpp and vLLM at `n=128`, `ptok=128`, `gen=64` and bucket
`mm_ids_helper`, activation quant/gather, grouped MMQ, and scatter/writeback. Do
not implement a fused routed-expert `MUL_MAT_ID` dispatch path unless those rows
are material in live serving and not dominated by GDN or FA.

### Newly-identified lever 2 - the W4A4 activation-quant pass (a vLLM-asymmetry, not just a kernel-speed gap)
Every NVFP4 GEMM (MMQ today, and the new 0034 FP4-MMA) **quantizes activations to e4m3 (amax/6 + code search) before the matmul** - a distinct, M-proportional kernel. vLLM on **sm_121 falls back to W4A16-Marlin** (the TENSORCORE_GDN_SCOPE confirms this: no tcgen05/cutlass-FP4 on GB10), i.e. **f16 activations, zero activation-quant**. So this pass (~3-6% of prefill) is a structural cost vLLM avoids, and it explains part of why even a peak FP4-MMA GEMM will not fully reach vLLM's prefill. The README's "act-quant FLAT" and "W4A16 rejected" verdicts are **decode/BW-bound findings**; in compute-bound prefill the trade is different and unaudited. **Lever: measure this quant bucket as its own nsys row; consider fusing the activation-quant into the GEMM prologue (cp.async + in-register quant) so it is not a separate global-memory pass.**

### Flag 3 - FlashAttention prefill (context-dependent, currently under-measured)
The 10-16 full-attention layers' QK^T·softmax·PV is a separate kernel covered by neither lever. It is small at the benchmark's npp=128 but **grows as L²**; at the long contexts the decode-serving work targets it can become a real bucket. The whole prefill ground-truth (232/68) was taken at one ubatch size - **re-profile FA share at the real serving prefill lengths** before assuming it is negligible.

### Confirmed inside the existing levers (not new)
- The 0042 "11% gating muls" and all the GDN small-matmuls/`solve_tri`/cumsum/layout-conts are **inside the GDN bucket** - the tensor-core GDN kernel subsumes them; they are only "live and uncovered" *today* because patch 0031 is default-off and losing at C=16.
- Shared-expert dense GEMMs, GDN/attention projections = **GEMM lever** (the FP4-MMA 0034 path already routes them).

## Bottom line
Two prefill levers (GEMM, GDN) are correctly the top-2 and own ~the gap's majority, but they are **not** the whole gap. The op-walk surfaces **MoE router+combine/scatter** and the **W4A4 activation-quant pass** as genuine, currently-untracked prefill contributors on the MoE decision model (~8-14% combined), plus **FA prefill** as a context-dependent risk the npp=128 bench hides. Per the methodology, step 0 is an nsys prefill-only window that explicitly breaks out `argsort/add(combine)`, `quantize_mmq_nvfp4`, and `flash_attn` as separate rows to size these three before funding a kernel.

Phase63 executed that step-0 discipline after the W4A16 direct-A and MTP
rejections. It stayed profile-first and inference-gated: pre/post canonical md5
and backend-op gates wrapped same-shape llama.cpp/vLLM prefill profiles at
`npp/PT=512` and `2048`. Result: FA is not a source lever on GB10 right now.
llama.cpp FA was `0.71%` at `npp=512` and `1.18%` at `npp=2048`; the
`npp=2048` cross-engine FA delta was about `1.7 us/tok`. The paged
FlashAttention mask/block-table cleanup remains a correctness/test gap worth
keeping in mind, but Phase63 rejects it as a parity patch.

Phase64 then attributed the remaining `layout-copy` bucket with default-off
`LLAMA_LAYOUT_TRACE=<n>` in fork commit `fa944bb5f`. The trace showed the
layout bucket is a mix of GDN conv-state materialization, MoE top-k fan-in
gathers, and paged-attention mask/KV reshape/copy paths. It did not expose a
single low-conflict projection/layout shortcut; use the Phase64 names before
funding any Phase65 source work.

Phase65 attributed the activation-quant bucket with default-off
`LLAMA_QUANT_TRACE=<n>` in fork commit `afc2c7030`. The default MoE prefill path
emitted `mmq_dense 4444`, `mmq_moe_dedup_unique 2960`, `mmq_moe_gather 2960`,
and `mmq_moe_flat 1480` trace lines at `npp=512`. The named paths are MoE
gate/up expert quant dedup plus gather, MoE down expert flat quantization, and
shared-expert dense quantization. Do not optimize from counts alone; Phase66
should time `quantize_mmq_nvfp4` versus `gather_mmq_fp4` with nsys/NVTX first.

Phase66 ran that timing pass. At MoE `npp=512`, total GPU kernel time was
`7108388986 ns`; `quantize_mmq_nvfp4` was `317205504 ns` (`4.46%`),
`gather_mmq_fp4` was `45374880 ns` (`0.64%`), combined `5.10%`. Reject a
gather/quant shortcut on GB10 for now: the gather is not material and the
combined route is below the `8%` source-funding threshold.

Phase67 tested the `bf16-proj` conversion half directly. Fork commit
`ea0875d14` adds default-off `LLAMA_BF16_CUBLAS_F32_OUT=1`, letting BF16 cuBLAS
write F32 output instead of writing BF16 then launching a BF16-to-F32 conversion.
It passed MoE/dense md5 and `MUL_MAT 1146/1146`; MoE prefill improved
`2347.41 -> 2402.34` at `npp=512` and `2440.18 -> 2456.54` at `npp=2048`.
Keep it default-off until dense and serving A/B decide whether it is worth a
default policy change.

Phase68 ran that dense and serving A/B without changing source. Dense prefill
was positive but tiny (`973.13 -> 975.52` at `npp=512`, `1019.88 -> 1021.39` at
`npp=2048`). A small MoE serving window at `N=128`, prompt `128`, generation
`128` also moved in the right direction: aggregate `409.8 -> 415.0`,
decode aggregate `615.3 -> 627.2`, mean TTFT `8574.7 -> 8085.9 ms`, wall
`39.978 -> 39.480 s`. Decision: keep `LLAMA_BF16_CUBLAS_F32_OUT=1` default-off
but worth carrying as an opt-in shortcut candidate. Do not default it on until
the fork commit is mirrored into the LocalAI patch series and a broader serving
snapshot passes pre/post md5 and op gates.

Phase70 ran that broader serving snapshot. Gates stayed green, but the broader
window rejected default-on: at `N=8`, opt-in aggregate and decode fell to
`0.8896x` and `0.8998x` of default, and mean TTFT worsened to `1.1247x`.
At `N=32` and `N=128`, opt-in slightly widened the vLLM decode gap
(`0.6864x` vs `0.6882x`, and `0.6839x` vs `0.6921x`). Keep
`LLAMA_BF16_CUBLAS_F32_OUT=1` default-off only and move to another lever.

Phase71 revalidated the current shipped GDN tensor-core default before adding
more GDN source work. Artifact:
`/home/mudler/bench/phase71_gdn_tc_revalidation/20260701_153425`. Canonical
MoE/dense md5 gates matched for default, sequential-disabled, serial-chunked,
and forced M5 modes; `GATED_DELTA_NET` passed `46/46` for each mode, and
default passed `MUL_MAT 1146/1146` plus `MUL_MAT_ID 806/806`. Current default
beat sequential-disabled by `+5.24%`/`+2.61%` S_PP at `npp=512/2048`, beat
serial-chunked by `+29.43%`/`+42.54%`, and forced `GDN_TC=4 GDN_CHUNK_MIN=64`
was within noise of default (`+0.42%`/`-0.10%`). Decision: keep shipped M5 and
do not reopen smaller GDN C32/QS/global-Ai32/kernel-reorder work on GB10.

Relevant files: `/home/mudler/_git/LocalAI/.claude/worktrees/feat+paged-attention/backend/cpp/llama-cpp-localai-paged/docs/{PREFILL_GEMM_SCOPE.md,PREFILL_GEMM_RESULTS.md,TENSORCORE_GDN_SCOPE.md,final_benchmark.csv}`, `/home/mudler/_git/LocalAI/.claude/worktrees/feat+paged-attention/backend/cpp/llama-cpp-localai-paged/patches/paged/0042-feat-paged-fused-residual-add-RMS-norm-weight-multip.patch`, and the graph source `/home/mudler/_git/LocalAI/backend/cpp/llama-cpp-paged-dev/src/models/{qwen35moe.cpp,delta-net-base.cpp}` + `/home/mudler/_git/LocalAI/backend/cpp/llama-cpp-paged-dev/src/llama-graph.cpp` (build_moe_ffn ~1500-1834, build_attn ~2136-2189).

## 2. Decode-serving compute hypotheses (ranked)

RANKED DECODE-SERVING GPU-COMPUTE HYPOTHESES (paged llama.cpp vs vLLM, MoE Qwen3.6-35B-A3B-NVFP4 on GB10)

Grounding facts that constrain the ranking:
- The gap is empirically MoE-specific: dense static is parity-to-ahead, MoE static is 89-93% of vLLM, but MoE *burst* serving is ~66% (n=128: paged 4.53 vs vLLM 6.87 tok/s/seq). So whatever degrades is on a path that hurts MoE far more than dense.
- It is GPU-compute-bound, NOT host/reuse-bound: padded-shape lever rejected, baseline reuse 0% statistically equal to S1+S3 reuse 72% on aggregate tok/s, hostproc only 4-8% of wall. So the host loop (0040/0041/S2) is closed; the residual lives in per-step kernel time.
- The decode KERNELS tie vLLM at a fixed WIDE lockstep shape (static batched-bench). The serving loss is therefore about how a RAGGED/NARROW/fluctuating live batch (varying decoder count D, ragged KV lengths, ragged token->expert assignment) feeds those same kernels, vs how gracefully vLLM's kernels degrade at the same concurrency. This is exactly the Phase-0 "re-scope" branch in DECODE_SERVING_SCOPE.md ("serving runs a worse effective batch shape into the kernels").

Decisive measurement that arbitrates all of these (run first): nsys a clean steady-state serving window (serve_bench staggered ~128 clients through llama-server, LLAMA_KV_PAGED=1 + LLAMA_MOE_FORCE_GRAPHS=1, -fa on -ngl 99) AND the same nsys on vLLM at the same concurrency (both-engine rule). Decompose per-step GPU-kernel-time into buckets {MoE-expert-GEMM (MUL_MAT_ID), full-attn FA, GDN recurrence, bf16 projections, activation-quant, sampling/logits} and compare serving-narrow vs static-wide vs vLLM. The bucket whose per-useful-token time grows MOST going static->serving (relative to vLLM's same bucket) is the gap. Avoid the known window artifact; measure a steady span. Reference doc: backend/cpp/llama-cpp-localai-paged/docs/DECODE_SERVING_SCOPE.md.

---

H1 (TOP) - MoE expert GEMM collapses to per-expert GEMV at ragged/narrow serving width, plus risk of the host-sync sorted per-expert fallback.
- Mechanism: top-8 of 256 experts. Tokens/expert ~= D*8/256. Static npl128 -> ~4 tok/expert; serving burst-tail D->8 -> ~0.25 tok/expert, so most active experts get 0-1 tokens. The grouped MMQ id-GEMM's per-expert M collapses to 1 -> pure GEMV that reads the full FP4 expert weight (memory-bound, weight bytes unamortized) and re-loads per-expert scales. This is the "256 tiny-expert weight bandwidth" README s5 names as the residual. Separately, patch 0025 only keeps CUDA graphs on for the should_use_mmq grouped path; any serving step where MUL_MAT_ID ne[2]>8 (mmvq_mmid_max) AND should_use_mmq returns false falls to the per-expert host-loop fallback that cudaStreamSynchronizes per expert (the [TAG_MUL_MAT_ID_CUDA_GRAPHS] disable) - catastrophic, and serving's varying per-step shapes can trip it unevenly.
- Why slower than vLLM: vLLM runs ONE fused MoE GEMM with sorted_token_ids/expert_ids computed on-GPU (fused_moe / Marlin-MoE), a single persistent launch that keeps the grouped GEMM dense and amortizes launch + scale loads; it degrades gracefully at small M. llama issues a grouped MMQ that, at ragged narrow width, is many near-empty expert tiles each re-reading scales, and can drop to a host-synced loop.
- nsys metric to confirm: (a) MUL_MAT_ID kernel-time as % of per-step GPU wall, static-wide vs serving-narrow vs vLLM; (b) the tokens-per-expert (M) distribution per step - look for M->1 GEMV collapse and achieved FLOP/s vs M; (c) count cudaStreamSynchronize / per-expert cudaMemcpy *between* MUL_MAT_ID launches per step (host-sync fallback firing); (d) vLLM single fused-MoE kernel duration at same concurrency.
- Candidate fix: a fused grouped-NVFP4 MoE decode GEMM with on-GPU token sorting (device-computed sorted token offsets + expert ids) so all active experts share one persistent launch and scales amortize - i.e. port vLLM's fused-MoE dispatch shape onto the FP4-MMA MMQ id-path; as a floor, extend 0025 to GUARANTEE the grouped should_use_mmq path for every serving shape so the host-sync loop never fires. Bit-exact-gateable (graph-replay/grouped path re-issues identical kernels).

H2 - Paged full-attention decode kernel: ragged-KV load imbalance, no tensor cores, indirect block-table reads.
- Mechanism: the 16 full-attn layers run the paged block-table FA decode, pinned by the 0010/0011 dispatch guard to vec/tile and NEVER the mma/wmma tensor-core FA (a present block table routes only to vec/tile; tile loads half2, F16 cache only). Static bench: all sequences one KV length -> balanced. Serving: KV lengths are ragged (each request at a different position), so per-sequence attention work is imbalanced across the grid and the step waits on the longest-context tail; there is no KV-dimension split. Every K/V access is an indirect physical-cell load via the block table (gather-like), less coalesced than a contiguous read.
- Why slower than vLLM: vLLM PagedAttention v2 uses a split-K / partitioned reduction designed for ragged long contexts (flash-decoding style) that balances work and lifts occupancy on the tail, and keeps the contiguous-within-block layout. llama's vec/tile paged read has no KV split and leaves tensor cores idle on the full-attn layers.
- nsys metric to confirm: FA-decode (vec/tile) kernel duration vs KV-length VARIANCE across the live batch (does it scale with max-KV/tail rather than mean-KV?); tensor-core-active-% during FA layers (expect ~0); achieved memory-BW of the FA kernel under ragged KV; vLLM paged-attn kernel time + util at same concurrency.
- Candidate fix: a KV-split (flash-decoding / split-K) paged FA decode so long sequences are partitioned across blocks for balance + occupancy; longer term a tensor-core paged FA for the full-attn layers (mma.sync down-translation, same approach as the GDN tensor-core scope). At minimum a per-sequence work-balanced launch.

H3 - GDN/SSM recurrence decode kernel under-occupied at narrow/variable serving width.
- Mechanism: patch 0022 tuned the recurrence (NUM_WARPS=16, COLS_PER_WARP=8, grid.z = S_v/(NW*CPW)) for the WIDE B=128 lockstep batch; its DRAM-latency coverage / MLP needs ~128 independent sequence-states in flight, and it is bandwidth-bound (re-streams the 128x128 f32 state per sequence per step at 84.6% of peak BW *at B=128*). In serving D fluctuates and collapses in the burst tail; at low D the kernel is grid-starved (few independent states), achieved-BW falls below the tuned point and per-token state traffic rises - the same grid-starvation failure mode the chunked-prefill kernel hit at low n_seqs. Plus the serial-SSM host loop (README s2d/s5 structural floor) is amortized over fewer tokens.
- Why slower than vLLM: vLLM's fused_recurrent_gated_delta_rule + its scheduler keep the recurrence fed at small batch; llama's fixed B=128-tuned launch params under-saturate when D is small.
- nsys metric to confirm: gated_delta_net kernel achieved-BW (GB/s) and occupancy as a function of live D in serving vs the static 84.6%@B128 baseline; recurrence kernel time/token vs D; grid occupancy at the burst tail.
- Candidate fix: width-adaptive recurrence launch params - auto-select NUM_WARPS/COLS_PER_WARP (already env GDN_NW/GDN_CPW) by live D so the grid stays saturated at narrow width; bit-exact-safe (0022's column assignment is provably independent of visit order). Longer term the chunked/register-resident state scan cuts state traffic.

H4 - Continuous-batch ragged-shape overhead: every kernel sized to the batch union/max; bf16 projections become GEMV at narrow D (umbrella + the "bf16-projection bandwidth" half of README's stated residual).
- Mechanism: ragged positions/lengths/expert-assignments mean each per-step kernel is launched for the max/union over the live batch, so useful-token efficiency < lockstep. This is the shared root of H1-H3 but is worth isolating because it also covers the q/k/v/gate/o projections (deliberately kept bf16, per README s5) which at narrow D become GEMV-like memory-bound weight reads - the "bf16-projection bandwidth" residual vLLM also pays but amortizes over a steadier batch.
- Why slower than vLLM: vLLM's scheduler holds a steadier/denser decode batch (padded bucketed decode + chunked-prefill interleave) so its projection/attn GEMMs run at higher effective M; llama's batch width fluctuates more.
- nsys metric to confirm: GPU-busy% in a steady serving window vs static (expect lower in serving) and (sum useful-token FLOPs)/(kernel-time) serving vs static; bf16 projection GEMM achieved FLOP/s vs M (GEMV collapse at small D).
- Candidate fix: largely subsumed by fixing H1-H3 at the kernel level. Note: holding D high via admission was effectively probed by the padded-shape lever and REJECTED for throughput (the completion-driven shrink is itself a per-survivor win); so do NOT re-pursue width-padding - the payoff is in the per-kernel fixes.

H5 - Per-step sampling + logits handling across D independent sequences (low, cheap to exclude).
- Mechanism: each live sequence has its own sampler chain run after logits land; at narrow D this fixed per-step cost (+ any D2H logits copy) is amortized over fewer tokens. vLLM batches sampling on-GPU across the whole decode batch.
- nsys metric to confirm: sampling/logits-copy time as % of per-step wall serving vs static; D2H logits cudaMemcpy size+time; count of per-sequence sampler launches.
- Candidate fix: single on-GPU batched sampler over [D, vocab], no per-sequence D2H. Likely small on the greedy/temp0 path; profile mainly to exclude.

H6 - NVFP4 activation (re)quantize fixed per-step cost at small M (lowest; likely a wash vs vLLM, no bit-exact lever).
- Mechanism: W4A4 quantizes activations to FP4 each step; 0023 de-dups the per-expert re-quant but the base quantize + scale compute is a per-row pass amortized over fewer tokens at narrow D. README lever-3 records the act-quant tax is removable only by W4A16 (rejected) or a kernel rewrite.
- nsys metric to confirm: quantize-kernel time as % of step vs M, serving vs static, vs vLLM (which also FP4-quantizes activations -> expected wash).
- Candidate fix: none bit-exact; include only so the profile can rule it out.

---

Priority for the profiling workflow: profile the MoE model (the gap model) first; bucket per-step GPU-kernel-time and A/B serving-narrow vs static-wide vs vLLM. Expect the ranking to be dominated by H1 (MoE expert GEMM at collapsed M + any host-sync fallback) and H2 (ragged-KV FA on vec/tile), with H3 (recurrence occupancy at narrow D) third; H4 is the shared root, H5/H6 are exclusion checks. The single most decisive metric is the per-step time share of MUL_MAT_ID vs FA vs gated_delta_net in serving-narrow relative to vLLM's equivalents at the same concurrency.

## 3. vLLM pipeline deep-dive

I have what I need. Synthesizing the vLLM pipeline against our patch series.

---

# vLLM vs `llama-cpp-localai-paged`: kernel + scheduler deep-dive (Qwen3-style hybrid SSM+attn MoE, NVFP4, consumer Blackwell sm_121)

## The single most important grounding fact (changes how every GEMM row reads)

On **consumer Blackwell (sm_120/sm_121: DGX Spark/GB10, RTX 5090, RTX PRO 6000)** the native CUTLASS NVFP4 grouped-GEMM path is **broken** (TMA-WS grouped-GEMM init failure, CUTLASS #3096) and there is **no `tcgen05`/TMEM**. So vLLM on *this exact hardware* does **not** run a native FP4-MMA grouped GEMM - it **falls back to the Marlin BF16 kernel that dequantizes FP4->BF16 in-register**, capped at bf16-tensor-core peak (~half FP4 peak). Native FP4 (W4A4/tcgen05) and the best FlashInfer/TRT-LLM kernels are gated to **data-center Blackwell sm_100a**. This means several "vLLM advantages" assumed for B200 do **not** hold on GB10, and our native FP4-MMA path (the just-verified 103 TFLOP/s = 57.7% of FP4 peak GEMM) is potentially *ahead of* vLLM's Marlin-bf16 fallback on this part - the opposite of the usual framing.

## Comparison table

| # | Component | vLLM (this model class, sm_121 reality) | Ours (`llama-cpp-localai-paged`) | Regime | Verdict / gap |
|---|---|---|---|---|---|
| 1 | **Dense weight GEMM - decode** (M≤128, BW-bound) | Marlin FP4→bf16 in-register dequant (W4A4 broken→fallback); reads 4-bit weights | Native FP4-MMA MMQ (FP4 wt × Q8_1 int8 act), M≤128 tile | decode | **Parity** - both at FP4 weight-BW floor. Ours ~96-97% of vLLM, ahead at low concurrency |
| 2 | **Dense weight GEMM - prefill** (large-M, compute-bound) | Marlin grouped/dense, async cp.async pipeline, big tiles, ~bf16 peak | MMQ small-tile, 1 CTA/SM. **New native FP4-MMA large-M kernel @103 TFLOP/s being integrated** (beats cuBLAS-bf16, bit-exact) | prefill | dequant→bf16-cuBLAS lever (0033) was **rejected** (MMQ beat it 29-49%); the native FP4-MMA kernel is the real fix and could **beat** vLLM's bf16-Marlin here |
| 3 | **MoE expert GEMM - decode** | Marlin FP4→bf16 grouped, indirect addressing | Grouped MMQ (`mul_mat_id`), sorted expert layout, native FP4-MMA | decode | **Parity** - both BW-floor. Recurrence/GEMM are *our wins*; residual = bf16-projection BW + host loop |
| 4 | **MoE expert GEMM - prefill** | Marlin grouped GEMM, fused, big tiles | MMQ small-tile grouped (1 CTA/SM) | prefill | **GAP (#1 prefill bottleneck per docs).** Native FP4-MMA grouped kernel is the planned fix; today MMQ is small-tile-bound |
| 5 | **MoE routing / gather / scatter / epilogue** | Triton persistent fused-MoE: indirect token addressing, **fused gate+up + SwiGLU epilogue**, once-quantize, scatter+weighted-combine fused | Sorted per-expert layout; **NVFP4 act-quant de-dup (0023)** mirrors once-quantize; SwiGLU is **separate ops** (no fused epilogue) | both | Partial parity. **No fused gate+up+SwiGLU epilogue** (extra IO passes); fan-in-only weighted-combine fusion was Phase 7 tested-flat |
| 6 | **GDN / linear-attn - decode** | FLA Triton `fused_recurrent_gated_delta_rule` + `fused_sigmoid_gating_delta_rule_update` (sequential, per-step state) | Fused sequential recurrence: in-place state write-back (0018), fused state gather (0019), o_proj MMVQ→MMQ (0020), occupancy retune (0022), conv-tap gather fusion (0028) | decode | **Parity-to-win** - recurrence runs at **102.6% of vLLM bandwidth**, 84.6% of GB10 peak BW. Our strongest area |
| 7 | **GDN / linear-attn - prefill** | FLA `chunk_gated_delta_rule`: intra-chunk products on **tensor cores** (UT-transform), ~2.5× cheaper | Tuned **sequential** scan (default); chunked parallel-scan (0031) is **opt-in + ~22% slower** (serial f32 reductions, no TC, C=16 forced by 99KB smem) | prefill | **GAP (#2 prefill bottleneck).** No tensor-core chunked GDN. Scoped (TENSORCORE_GDN_SCOPE, mma.sync only); **Gram products de-risked at 6.7-9.3× over sequential**, kernel not yet built |
| 8 | **Causal conv1d (short conv)** | FLA `causal_conv1d_fn`/`_update` Triton | `ggml_ssm_conv_update_inplace` (0021): 5-op chain → 1 op, in-place ring | both | Parity |
| 9 | **Full-attention - decode** (16 of 64 layers) | FlashInfer / TRT-LLM paged decode (tensor-core, cascade wrapper, FP8-KV capable) | llama.cpp FA `ggml_flash_attn_ext` with **block-table paged read** (src[5]); routed to **vec/tile** kernels | decode | Parity at decode width (vec/tile is right for small batch) |
| 10 | **Full-attention - prefill** (large-M) | FlashInfer/TRT-LLM tensor-core prefill FA | **Forced to vec/tile** (block-table only grafted into vec/tile; mma/wmma FA ignores it, dispatch-guarded off) | prefill | **GAP (secondary).** Paged prefill full-attn gets **no tensor-core FA**. Docs rank it below MoE-GEMM/GDN, so not the dominant prefill term |
| 11 | **Paged KV manager (full-attn)** | vLLM block manager + hybrid KV cache manager (co-sizes attn/linear blocks to equal physical bytes, anti-fragmentation) + auto prefix caching | `PagedKVManager` (FreeBlockQueue/BlockPool/COW), cross-request prefix sharing, burst-reclaim (0024) | both | **Parity** on the attn side; we lack vLLM's *unified* hybrid co-sizing (we manage SSM state separately - see #12) |
| 12 | **Hybrid SSM-state cache mgmt** | Unified hybrid manager pages linear-attn state alongside attn KV | SSM recurrent + conv state in fixed per-seq slots, updated **in-place** (not paged; O(1)/seq) | both | Different approach, not a perf gap (recurrent state doesn't need paging); we lack unified fragmentation accounting |
| 13 | **Sampler** | **GPU FlashInfer sorting-free sampler** (Dual-Pivot rejection sampling, single kernel, no logits sort, ~0 overhead); RejectionSampler for spec-decode | llama.cpp **host-side** sampler chain (CPU partial-sort for top-k/p) | serving | **GAP - NO EQUIVALENT.** Host sampler + D2H logits adds to the per-step host loop at high concurrency (greedy md5 bench hides it) |
| 14 | **Scheduler / continuous batching / chunked prefill** | V1: mixed prefill+decode step, **chunked prefill default-on**, decode-prioritized `max_num_batched_tokens` budget, auto-chunk | `update_slots()` unified step, **decode-first dynamic budget** (0016, `max(n_ubatch,T−D)`), prefill budget (0013), prefix-share (0008) | serving | **Parity** - we match the chunked-prefill + decode-first token-budget design |
| 15 | **CUDA graphs - decode** | **FULL cudagraph**: padded/bucketed decode shapes → 1 persistent captured graph per bucket → steady decode = single `cudaGraphLaunch`, zero host rebuild | S1+S3 (0040/0041) graph **reuse** keyed on bucketed block-table dims + decode-shape-stable scheduling → serving reuse 0%→**72.2%** | serving | **Partial.** We reuse, not full-capture. **Padded/fixed-slot decode (→~100% like vLLM) was built + GPU-tested + REJECTED** - serving decode here is GPU-compute-bound, so dummy-row compute > reuse recovered |
| 16 | **CUDA graphs - prefill** | PIECEWISE cudagraph (default FULL_AND_PIECEWISE) | ggml graph rebuild per prefill step (paged data-ptr churn) | prefill | Gap, low value (prefill is compute-bound; launch overhead amortized over large M) |
| 17 | **Speculative decoding / MTP** | **MTP head + EAGLE-style spec-decode** supported for this model class (Qwen3-Next ships an MTP module) | Opt-in `draft-mtp` path exists and passes rollback safety, but current serving is rejected on GB10 | decode | **GAP - IMPLEMENTED BUT NOT A WIN.** Phase 15/19/62 showed high acceptance with severe serving regression from target-verify/output-row graph cost. Do not enable MTP or tune `n_max` blindly. |
| 18 | **KV-cache dtype** | FP8 KV cache + FP8 attention (halves KV BW) | F16 paged KV | both | Minor gap; partly offset by our overall 1.5-3× lower memory (NVFP4 weights). FP8-KV would cut KV BW further |

## Gaps where we have NO equivalent (ranked by value)

1. **Speculative decoding via the MTP head (#17).** Qwen3-Next/3.6 ships a Multi-Token-Prediction module; vLLM exploits it for spec-decode. Phase 9 proved the current fork is no longer at "nothing": Qwen3.5/3.6 `draft-mtp` code exists, the DGX MoE GGUF contains `nextn` tensors, and a short opt-in smoke passes after disabling backend draft sampling for MTP. Phase 14 passed rollback safety, but Phase 15, Phase 19, and Phase 62 rejected current serving MTP on GB10 because high acceptance did not overcome target-verify/output-row graph cost. Do not enable MTP by default.

2. **Tensor-core chunked GDN prefill (#7).** vLLM's FLA `chunk_gated_delta_rule` pushes intra-chunk Gram products through tensor cores (~2.5× cheaper prefill). Our 0031 chunked kernel is opt-in and 22% *slower* (serial f32 reductions). Scoped (mma.sync-only on sm_121, no wgmma/tcgen05), Gram products de-risked at 6.7-9.3×, kernel not built. One of the two named prefill bottlenecks.

3. **Large-M native FP4-MMA grouped MoE GEMM (#4).** The #1 prefill bottleneck. vLLM uses Marlin-bf16 grouped (capped at bf16 peak on sm_121); our MMQ is small-tile/1-CTA-bound. The new native FP4-MMA GEMM (103 TFLOP/s, beats cuBLAS-bf16) is the integration that closes this - and because vLLM is bf16-Marlin here, a working native FP4 grouped kernel could *exceed* vLLM on this exact hardware.

4. **GPU fused sorting-free sampler (#13).** vLLM samples on-device (FlashInfer Dual-Pivot rejection, no logits sort); llama.cpp samples on host. Adds to the serving host loop at 128-way concurrency for top-k/p workloads. No GPU-sampler equivalent in the series.

5. **Fused MoE SwiGLU epilogue (#5).** vLLM fuses gate+up+SwiGLU into the grouped-GEMM epilogue (fewer IO passes). We have the act-quant de-dup (0023) but run SwiGLU as separate ops. Prefill-relevant, decode-minor.

6. **Tensor-core FA for the paged prefill full-attn path (#10).** Paged forces vec/tile (mma FA ignores the block table). Secondary - docs rank it below #2/#3 in the prefill budget.

7. **FP8 KV cache / FP8 attention (#18).** Minor; partly offset by our NVFP4 memory lead.

## Where we are at or ahead of vLLM (not gaps)

- **GDN decode recurrence (#6):** 102.6% of vLLM bandwidth - our fusion series (0018-0022, 0028) is the strongest area.
- **Decode weight GEMMs dense+MoE (#1, #3):** at the FP4 weight-BW floor = parity; dense ahead at low concurrency. The residual MoE serving gap (~66% at n=128 burst) is a **GPU-compute** gap (vLLM's MoE decode kernel+scheduler ~1.3× on aggregate), **not** a host-loop gap that a graph-reuse/padding lever can close (padded-shape lever proved this, rejected 2026-06-28).
- **Memory:** 1.5-3× lower than vLLM (NVFP4-resident, no persistent bf16 dequant copies).
- **Scheduler design (#14):** chunked-prefill + decode-first budget matches vLLM's V1 model.

## Net assessment

Our **decode kernels are at parity-to-ahead** (GDN recurrence, both FP4 GEMMs at BW floor) - confirmed in the kernel regime. The two real, *named-in-docs* **prefill** gaps (MoE grouped GEMM #4, tensor-core chunked GDN #7) are being actively closed with the native FP4-MMA GEMM + the de-risked tensor-core Gram products; on consumer Blackwell specifically these can match-or-beat vLLM because vLLM is itself on a **bf16-Marlin fallback**, not native FP4. The remaining structural gap with no equivalent in the series is the **GPU fused sampler** (serving host-loop, secondary). MTP is no longer absent, but current GB10 serving MTP is rejected until a target-verify/output-row graph-cost design exists. The serving-decode residual is GPU-compute-bound (not host/graph-reuse), so vLLM's edge there is its faster MoE decode kernel + scheduler, not something a host-side lever recovers.

---

Files read (all absolute): `/home/mudler/_git/LocalAI/.claude/worktrees/feat+paged-attention/backend/cpp/llama-cpp-localai-paged/README.md`, `docs/DECODE_SERVING_SCOPE.md`, `docs/PREFILL_GEMM_SCOPE.md`, `docs/PREFILL_GEMM_RESULTS.md`, `docs/TENSORCORE_GDN_SCOPE.md` (same dir).

Sources:
- [vLLM Now Supports Qwen3-Next (FLA Triton kernels, hybrid KV manager, MTP)](https://blog.vllm.ai/2025/09/11/qwen3-next.html)
- [CUTLASS #3096 - SM120 NVFP4 MoE grouped GEMM broken, FlashInfer/Marlin fallback](https://github.com/NVIDIA/cutlass/issues/3096)
- [vLLM Quantization Kernels (NVFP4 W4A16/W4A4, Marlin, Machete)](https://deepwiki.com/bytedance-iaas/vllm/11.4-quantization-kernels)
- [SM120 NVFP4 MoE perf report - Marlin bf16 fallback on consumer Blackwell](https://discuss.vllm.ai/t/sm120-rtx-pro-6000-nvfp4-moe-performance-report-qwen3-5-397b/2536)
- [vLLM Attention Backends - FlashInfer/TRT-LLM default on Blackwell](https://docs.vllm.ai/en/latest/design/attention_backends/)
- [vLLM FLA fused_recurrent_gated_delta_rule](https://docs.vllm.ai/en/latest/api/vllm/model_executor/layers/fla/ops/fused_recurrent/)
- [vLLM Fused MoE Kernel Features](https://docs.vllm.ai/en/latest/design/moe_kernel_features/)
- [vLLM scheduling - chunked prefill, decode-first budget, FULL_AND_PIECEWISE cudagraph](https://docs.vllm.ai/en/stable/configuration/optimization/)
- [FlashInfer sorting-free GPU sampling (Dual-Pivot rejection)](https://flashinfer.ai/2025/03/10/sampling.html)
- [vLLM #11394 - FlashInfer sampling kernel in V1](https://github.com/vllm-project/vllm/pull/11394)
- [vLLM #42960 - batch-invariant GDN_ATTN for Qwen3-Next/Qwen3.6](https://github.com/vllm-project/vllm/issues/42960)

## 4. Novel levers

I've grounded myself in the four scope docs, the README patch table + benchmarks (final_benchmark.csv), the methodology doc, and the 0034 FP4-MMA / 0042 fused-residual patch headers. Verified state: prefill is the biggest gap (dense ~920 vs vLLM ~2000 t/s ≈ 44-46%; MoE ~2177 vs ~5300-6223 ≈ 35-41%); decode kernel at parity; serving decode ~65% and measured GPU-compute-bound (host/graph-reuse + padded-shape proved neutral-or-worse). Already-explored/rejected: dequant→bf16 cuBLAS (0033, rejected), bf16-tau (dropped), NVFP4 projections (KL-fail), W4A16-Marlin (rejected), graph coverage (flat), act-quant fusion on decode (flat), padded-shape decode (rejected). Below are levers that go beyond those.

---

# Candidate-lever brainstorm: closing the vLLM gap (paged Qwen3.6 NVFP4, GB10 sm_121a)

Organized by where the verified gap actually is. For each: mechanism / expected gain / gate (bit-exact vs KL) / risk / effort-reward. "Profile-gated" = run Phase-0 nsys before building, per the methodology.

## A. PREFILL (the largest gap, 35-46% of vLLM) — highest reward bucket

### A1. Graph-safe ragged grouped FP4-MMA MoE kernel (remove the per-expert host-sync loop)
- **Mechanism:** 0034 lands the native FP4-MMA dense kernel but routes MoE prefill through the *per-expert host-sync loop* (a `cudaStreamSynchronize` per expert per layer — e.g. dozens-to-hundreds of syncs/layer). Replace it with ONE ragged/grouped FP4-MMA launch over the existing `expert_bounds`/`ids_dst` sorted layout (variable M per expert, single kernel). This is the follow-up 0034 itself flags.
- **Gain:** HIGH. MoE expert GEMM is named the #1 prefill cost; this both removes the serial host syncs and unlocks kernel overlap + graph capture. The single biggest remaining prefill lever after 0034.
- **Gate:** bit-exact by construction (same FP4 math, same K-order as the per-expert path) → greedy md5.
- **Risk:** medium-high (ragged tiling + boundary handling, graph-safety).
- **Effort/reward: HIGH effort / HIGH reward.** The flagged 0034 follow-up; rank #1 for prefill.

### A2. Multi-stream expert dispatch (cheap stepping-stone to A1)
- **Mechanism:** before writing the full ragged kernel, run the independent per-expert FP4-MMA GEMMs on N CUDA streams instead of the serial host-sync loop, overlapping their LPDDR5x weight reads + tensor-core work.
- **Gain:** medium (partial overlap; recovers some of the serial-sync stall without the kernel rewrite).
- **Gate:** bit-exact (same kernel, reordered launches) → greedy md5.
- **Risk:** medium (stream/event mgmt, not graph-safe — prefill isn't graph-replayed so OK).
- **Effort/reward: LOW-MED effort / MED reward.** Bank this before A1.

### A3. Fuse MoE router → token-gather/scatter → GEMM (permutation fusion)
- **Mechanism:** vLLM/SGLang fuse routing→permute→grouped-GEMM→unpermute. Here the activation gather (into the sorted-expert layout) and the scatter-back are separate memory passes. Read activations through `ids_dst` in the GEMM prologue and write through the inverse permutation in the epilogue → removes two full activation memory passes per MoE layer.
- **Gain:** medium for prefill (large activation tensor); smaller for decode (0019/0028 already fuse the decode gather).
- **Gate:** bit-exact (index indirection only, same values) → greedy md5.
- **Risk:** medium (epilogue indexing correctness).
- **Effort/reward: MED / MED.** Pairs naturally with A1's kernel.

### A4. Fused MoE FFN (up_proj → SiLU → down_proj, intermediate register/shared-resident)
- **Mechanism:** keep the per-expert intermediate activation in shared/registers across up→act→down instead of round-tripping it to global. For large-M prefill the intermediate is big → a real BW save; also helps decode.
- **Gain:** medium-high (removes one full intermediate read+write per expert per layer).
- **Gate:** bit-exact if SiLU + accumulation order preserved → greedy md5 (else KL-gate).
- **Risk:** HIGH (fused FP4 FFN kernel is complex; register pressure on sm_121a).
- **Effort/reward: HIGH / MED-HIGH.** Strong but expensive; sequence after A1.
- **Phase 7 shortcut rejected:** fusing only SWIGLU into the NVFP4
  down-input quantization while reusing grouped-MMQ passed the focused op gate
  (`MOE_SWIGLU_DOWN 7/7`) but changed paged-MoE md5 under opt-in
  (`07db32c2...` vs canonical `8cb0ce23...`) and was flat in serving A/B
  (`decode_agg_tps 657.1 → 667.4`, `decode_perseq_tps 3.92 → 3.88`).
  Do not retry that partial fusion without a KL gate and a stronger profile
  bucket. A real A4 remains a different, larger register/shared-resident FFN
  kernel.

### A5. Activation-quant fusion into the 0042 residual/RMSNorm epilogue (prefill)
- **Mechanism:** the README's "act-quant fusion FLAT" verdict was *decode-only*. For prefill the W4A4 activation-quantize pass is a bigger tensor. 0042 already fuses residual-add+RMSNorm+mul; extend its epilogue to emit the FP4-quantized activation the next GEMM consumes, removing a dedicated act-quant read+write.
- **Gain:** low-medium for prefill.
- **Gate:** bit-exact (same `quantize_mmq_nvfp4` math, just fused) → greedy md5.
- **Risk:** medium (epilogue + the FP4 codepath coupling).
- **Effort/reward: MED / LOW-MED.** Cheap-ish add-on once 0034/A1 are in.

### A6. Stream-K / split-K for the FP4 prefill GEMM (SM occupancy on few-SM GB10)
- **Mechanism:** GB10 has relatively few SMs. For layers whose output grid (⌈M/128⌉×⌈N/128⌉) is smaller than the SM count, SMs idle. Stream-K splits the K dimension across CTAs with a reduction, keeping all SMs busy.
- **Gain:** medium for small-output-grid layers (profile-gated — only if 0034's grid under-fills the GPU).
- **Gate:** bit-exact if the f32-accumulate reduction order is fixed/deterministic; otherwise KL-gate.
- **Risk:** medium (reduction correctness, workspace).
- **Effort/reward: MED / MED.** Complements 0034; profile first.

### A7. Prefill CUDA-graph capture (follow-on to A1)
- **Mechanism:** with fixed prefill chunk size (0013/0016 budgets already exist) and A1 removing the host-sync MoE loop, the whole prefill chunk becomes graph-capturable.
- **Gain:** LOW marginal — prefill kernels are large so launch overhead is amortized; the value is mostly *enabling* it (which A1 already does). Record as low-reward, not a standalone lever.
- **Gate:** bit-exact.
- **Effort/reward: LOW / LOW.** Note, don't prioritize.

## B. DECODE-SERVING (~65% of vLLM aggregate, measured GPU-compute-bound)

### B1. Speculative decoding, greedy = bit-exact (SSM-state rollback is the crux) ⭐ novel
- **Mechanism:** draft γ tokens (small draft model, or prompt-lookup/n-gram for zero extra weights), verify in one target forward. At **temp=0 the accepted tokens are argmax-identical to non-spec → the greedy md5 gate PASSES by construction** (lossless). This is the rare throughput-multiplier that's bit-exact-compatible. Especially powerful at low concurrency where paged is farthest below vLLM (n=8 burst: paged 28 vs vLLM 45) and the GPU is underutilized.
- **The non-obvious crux:** hybrid-SSM rollback. KV rollback under paged is easy (truncate blocks). But the gated-DeltaNet recurrent state is updated **in-place** (patch 0018), so a rejected draft requires restoring the 128×128 f32 state per layer to the last accepted position — snapshot-before-speculate (memory+BW cost) or recompute. This SSM-state checkpoint/restore is the real engineering risk and is why naive llama.cpp spec-decode plumbing won't transfer.
- **Gain:** HIGH (2-3x at favorable acceptance/low concurrency).
- **Gate:** **bit-exact for greedy** (md5 holds); distribution-preserving (KL-gate) for temp>0.
- **Risk:** HIGH (SSM snapshot/rollback, draft integration with paged KV + recurrent state, acceptance tuning).
- **Effort/reward: HIGH / HIGH.** Biggest novel decode lever; start with zero-draft prompt-lookup to de-risk the rollback plumbing before adding a draft model.

### B2. FP8 / quantized paged KV cache
- **Mechanism:** decode is BW-bound; quantizing the paged KV (llama.cpp already has q8_0/q4_0 `--cache-type-k/v`) halves the KV-gather BW and **doubles effective KV capacity → higher max concurrency**. Wire the existing quantized-KV FA-vec path through the paged block-table read (0009/0010). Matches a vLLM feature (fp8 KV).
- **Gain:** medium-high for long-context / high-concurrency decode.
- **Gate:** KL-gate (KV quant changes attention numerics; watch long-context recall), per the `8cb0ce23` precedent.
- **Risk:** medium (paged FA-read FP8 path; precision on long context).
- **Effort/reward: MED / MED-HIGH.**

### B3. Coalesced paged-KV block layout for the in-kernel decode gather
- **Mechanism:** decode is at the LPDDR5x floor, so *effective* BW depends on coalescing. vLLM lays K as `[blocks, kv_heads, head_size/x, block_size, x]` precisely to coalesce the FA read. Re-lay-out the paged blocks so 0009/0010's in-kernel gather issues fully-coalesced vectorized loads matching the FA kernel's access pattern.
- **Gain:** medium (profile-gated: measure the FA-read achieved-BW / sector efficiency first).
- **Gate:** bit-exact (pure memory layout, identical values) → greedy md5.
- **Risk:** medium (touches paged KV manager + FA read).
- **Effort/reward: MED / MED.** Profile before building.

### B4. Megakernel / persistent decode (single-launch fused decode step)
- **Mechanism:** fuse the per-layer decode ops into one persistent kernel that loops layers internally (à la Mirage/MPK persistent megakernel), eliminating inter-op launch overhead, inter-op global round-trips, and the host loop for the decode step; keep the recurrent state resident across the step.
- **Gain:** potentially high for the GPU-compute-bound serving regime (kills launch/scheduling bubbles vLLM avoids). Honest caveat: at 27-35B the activations don't fit SMEM across layers, so the win is mostly launch-overhead + scheduling, less data-residency.
- **Gate:** in principle bit-exact (same ops/order) but extremely hard to guarantee → realistically KL-gate.
- **Risk:** VERY HIGH (essentially re-implements the decode forward as one kernel).
- **Effort/reward: VERY HIGH / HIGH.** The swing-for-the-fences lever; only after cheaper decode levers are exhausted.

### B5. Pipeline sampling off the decode critical path
- **Mechanism:** the doc names the "serial-SSM host loop / sampling can't start until logits land" as a floor. S2 (double-buffer set_inputs) was dropped because set_inputs is cheap — but the *sampling stall* between steps is different. Overlap step N's sampling + step N+1's input build with the GPU launch, so the GPU never idles waiting on host sampling.
- **Gain:** medium (recovers the inter-step sampling bubble; this is the precise residual S2 didn't target).
- **Gate:** bit-exact (host reordering only) → greedy md5.
- **Risk:** medium (ordering correctness vs the recurrent in-place state).
- **Effort/reward: MED / MED.**

### B6. Co-batch chunked prefill INTO decode steps (vLLM-style GPU saturation — flips S3) ⭐ reframe
- **Mechanism:** S3 deliberately keeps prefill *out* of decode steps (for graph reuse). But the later measurement proved serving decode is **GPU-compute-bound, not host-bound** — which *removes S3's rationale*. vLLM does the opposite: mixes small prefill chunks into decode steps to fill otherwise-idle GPU at low decode width. Test co-batching a sized prefill chunk with decode to use spare SMs.
- **Gain:** medium at low-to-mid decode width (better GPU utilization).
- **Gate:** bit-exact (same math, scheduling only) → greedy md5.
- **Risk:** low-medium (it partially contradicts S3 — A/B them; the GPU-compute-bound finding says S3's reuse benefit is ~nil here, so co-batching likely wins).
- **Effort/reward: LOW-MED / MED.** Cheap A/B with high information value (directly tests the regime conclusion).

### B7. Adaptive-width bucketed decode graph (doc-sanctioned revisit)
- **Mechanism:** the rejected padded-shape lever used fixed pad-to-`--parallel`; the doc explicitly leaves the door open for *adaptive* width (round up to next small bucket 8/16/32/64).
- **Gain:** LOW on GB10 — the same doc measured serving decode GPU-compute-bound, so graph reuse buys ~nothing here. Record as: revisit ONLY if the host loop is re-confirmed dominant on other hardware.
- **Gate:** bit-exact.
- **Effort/reward: MED / LOW (on GB10).** Note, don't build for GB10.

## C. CROSS-CUTTING / aggregate-throughput reframes

### C1. Exploit the 1.5-3x memory advantage for higher max concurrency ⭐ reframe
- **Mechanism:** the benchmark stops at npl=128 where both engines fit. With 1.5-3x lower memory (and synergistic with B2 FP8-KV), the paged backend can serve npl=256+ in the same VRAM where vLLM OOMs. Per-stream tok/s gap is irrelevant if paged sustains 2x the concurrent streams per GPU — aggregate tok/s/GPU can match or beat vLLM.
- **Gain:** HIGH for aggregate throughput-per-GPU at the memory ceiling (a legitimate, honestly-labeled "different operating point," not a per-stream parity claim).
- **Gate:** bit-exact (no numeric change) → greedy md5.
- **Risk:** low (scheduler/admission tuning to actually pack the streams).
- **Effort/reward: LOW / HIGH.** Cheapest high-reward lever — measure aggregate at max-concurrency, pair with B2.

---

## Ranked summary (effort vs reward)

| # | Lever | Regime | Gate | Effort | Reward |
|---|-------|--------|------|--------|--------|
| C1 | Higher max-concurrency via memory advantage (+B2) | aggregate | bit-exact | LOW | **HIGH** |
| A1 | Graph-safe ragged grouped FP4-MMA MoE kernel | prefill | bit-exact | HIGH | **HIGH** |
| B1 | Speculative decode (greedy=bit-exact; SSM rollback crux) | decode | bit-exact (greedy) | HIGH | **HIGH** |
| A2 | Multi-stream expert dispatch (→A1) | prefill | bit-exact | LOW-MED | MED |
| B6 | Co-batch chunked-prefill into decode (flips S3) | serving | bit-exact | LOW-MED | MED |
| B2 | FP8/quantized paged KV cache | decode | KL-gate | MED | MED-HIGH |
| A3 | MoE router+gather+GEMM permutation fusion | prefill | bit-exact | MED | MED |
| B3 | Coalesced paged-KV layout for decode gather | decode | bit-exact | MED | MED |
| B5 | Pipeline sampling off decode critical path | serving | bit-exact | MED | MED |
| A4 | Fused MoE FFN (up+SiLU+down resident) | prefill+decode | bit-exact | HIGH | MED-HIGH |
| A6 | Stream-K/split-K FP4 prefill GEMM | prefill | bit-exact/KL | MED | MED |
| A5 | Act-quant fusion into 0042 epilogue (prefill) | prefill | bit-exact | MED | LOW-MED |
| B4 | Megakernel/persistent decode | decode | KL-gate | VERY HIGH | HIGH |
| A7 | Prefill CUDA-graph capture (→ enabled by A1) | prefill | bit-exact | LOW | LOW |
| B7 | Adaptive-width bucketed decode graph | serving | bit-exact | MED | LOW (GB10) |

**Suggested attack order:** (1) **C1** — near-free aggregate win exploiting the memory advantage, immediately defensible. (2) **A2→A1** — the prefill MoE GEMM is the biggest single gap and 0034 already flags A1. (3) **B6** — cheap A/B that directly tests/exploits the "serving is GPU-compute-bound" conclusion. (4) **B1** — the highest-ceiling decode lever, but gate the SSM-state rollback plumbing first via zero-draft prompt-lookup. (5) **B2/B3/B5** as the BW + bubble cleanup. (6) **A4 / B4** as the high-effort structural swings only if the cheaper levers leave a funded gap.

**Two highest-value non-obvious insights:** (a) speculative decoding is *bit-exact under greedy* (md5 passes by construction) — the only throughput-multiplier compatible with the sacred gate — but its hybrid-SSM in-place-state rollback (patch 0018) is the unsolved crux. (b) the measured "serving decode is GPU-compute-bound" finding **invalidates S3's keep-prefill-out rationale** and argues for the *opposite* (B6 co-batching, vLLM-style), plus reframes the win toward aggregate-per-GPU concurrency (C1) rather than per-stream parity.

Relevant files: `/home/mudler/_git/LocalAI/.claude/worktrees/feat+paged-attention/backend/cpp/llama-cpp-localai-paged/docs/{DECODE_SERVING_SCOPE,PREFILL_GEMM_SCOPE,PREFILL_GEMM_RESULTS,TENSORCORE_GDN_SCOPE}.md`, `.../README.md` (s4 benchmarks, s5 rejected levers), `.../docs/final_benchmark.csv`, `.../patches/paged/0034-feat-paged-native-NVFP4-W4A4-FP4-MMA-large-M-prefill.patch` (A1 is its flagged follow-up), `.../patches/paged/0042-feat-paged-fused-residual-add-RMS-norm-weight-multip.patch` (A5 extends it).

## 5. Synthesized prioritized lever map

# Prioritized Lever Map - vLLM Parity, Qwen3.6 NVFP4 on GB10 (sm_121a)

## Bottom line (where the gap actually is)
- **Prefill is the largest absolute gap**: dense ~44-48% of vLLM, MoE (decision model) ~29-41%. Two buckets own ~71% of the wall (NVFP4 GEMM ~49%, chunked GDN ~22%); the op-walk surfaces **three uncovered residuals** (MoE router/combine, prefill act-quant, FA-at-length).
- **Decode kernels are at parity-to-ahead** (GDN recurrence 102.6% of vLLM BW; both FP4 GEMMs at the BW floor). **Decode-*serving* is the still-open gap** (~66% at n=128 burst), is **MoE-specific** and **GPU-compute-bound** (host-loop/graph-reuse/padded-shape all proved neutral-or-worse, so they are closed).
- The two structural levers vLLM has that the series has **no equivalent for**: **MTP speculative decode** and **GPU fused sampler**. On *this* hardware vLLM is itself on a **bf16-Marlin FP4 fallback** (no tcgen05/CUTLASS-grouped), so a working native FP4 path can **match-or-beat** it, not just chase it.

## Single highest-leverage NEXT action for the still-open decode-serving gap
**Run the both-engine steady-state serving nsys window FIRST (it is the gate before any decode kernel is funded).** Stagger ~128 clients through `llama-server` (`LLAMA_KV_PAGED=1 LLAMA_MOE_FORCE_GRAPHS=1 -fa -ngl 99`) and the identical concurrency on vLLM; bucket per-step GPU-kernel time into `{MUL_MAT_ID, FA-vec/tile, gated_delta_net, bf16-projections, act-quant, sampling}` and compare **serving-narrow vs static-wide vs vLLM**. The decisive single metric: the per-useful-token time share of `MUL_MAT_ID` vs `FA` vs `gated_delta_net` in serving relative to vLLM. **Primary hypothesis to confirm/refute: H1** - MoE grouped GEMM collapsing to per-expert GEMV at ragged width, **and** count `cudaStreamSynchronize` *between* `MUL_MAT_ID` launches to catch the per-expert host-sync fallback firing. This one A/B arbitrates D2 vs D3 vs D4 (all HIGH-effort) at once, and the methodology forbids building a kernel before it. **Bank D1 (grouped-path guarantee) immediately as near-free insurance against the host-sync cliff regardless of outcome.**

## Master ranked lever table (pursue list)

| # | Lever | Gap | Gain → parity | Effort | Risk | Gate | Dependency / sequence | Status |
|---|-------|-----|--------------|--------|------|------|----------------------|--------|
| 0 | **Phase-0 serving nsys (both-engine bucket A/B)** | decode | enabling - sizes/arbitrates H1-H4 | LOW | low | n/a | none - **do first** | NOT DONE |
| 1 | **X1 (C1) Exploit 1.5-3× memory → serve npl=256+ where vLLM OOMs** | aggregate | **HIGH** (different operating point: aggregate tok/s/GPU) | LOW | low | BE | pairs w/ D6; admission tuning | NOT STARTED |
| 2 | **P1 Native FP4-MMA large-M dense GEMM (patch 0034)** | prefill | **HIGH** - GEMM ~49% of wall; can *beat* vLLM bf16-Marlin | HIGH | med | BE (md5) | foundation for P2/P8 | **IN PROGRESS (0034 scaffold landed)** |
| 3 | **D1 Guarantee grouped MMQ path - never host-sync per-expert fallback (extend 0025)** | decode | **HIGH if firing** (removes catastrophic cliff) | LOW | low | BE | gated by #0; bank regardless | NOT STARTED |
| 4 | **P3 Multi-stream expert dispatch (→P2)** | prefill | MED (partial overlap of serial syncs) | LOW-MED | med | BE | stepping-stone, bank before P2 | NOT STARTED |
| 5 | **P2 (A1) Graph-safe ragged grouped FP4-MMA MoE GEMM** | prefill | **HIGH** - the #1 prefill bucket (~28% of wall) | HIGH | med-high | BE (md5) | after P1/P3; **shares kernel arch w/ D2** | **FLAGGED 0034 follow-up** |
| 6 | **D10 (B6) Co-batch chunked-prefill into decode (flips S3)** | serving | MED (fills idle SMs at low D) | LOW-MED | low-med | BE | cheap A/B; tests "GPU-compute-bound" conclusion | NOT STARTED |
| 7 | **P4 Tensor-core chunked GDN prefill kernel (rewrite 0031)** | prefill | **HIGH** - #2 prefill bucket (~22% of wall, ~17% of gap) | HIGH | med-high | BE→KL | Gram products de-risked 6.7-9.3× | **DESIGN SCOPED, kernel NOT built** |
| 8 | **D2 (H1) Fused grouped-NVFP4 MoE decode GEMM + on-GPU token sort** | decode | **HIGH** - top decode hypothesis (MoE-specific) | HIGH | high | BE | gated by #0; **co-develop kernel w/ P2** | NOT STARTED |
| 9 | **D5 (B1) Speculative decode via MTP head** | decode | **HIGH** (2-3× at low/mid concurrency) | HIGH | high | BE (greedy) / KL (temp>0) | crux=SSM in-place state rollback (0018); de-risk w/ zero-draft prompt-lookup | NOT STARTED |
| 10 | **D6 (B2) FP8 / quantized paged KV cache** | decode | MED-HIGH (halves KV BW; doubles capacity → enables X1) | MED | med | KL (8cb0ce23 precedent) | wire quantized-KV FA-vec through paged read (0009/0010) | NOT STARTED |
| 11 | **D3 (H2) KV-split / flash-decoding paged FA decode** | decode | MED-HIGH (ragged-KV balance + occupancy) | MED-HIGH | med | BE→KL | gated by #0 (build only if FA bucket grows) | NOT STARTED |
| 12 | **P5 (A3+PREFILL-L1) Fused MoE router+gather+scatter+combine** | prefill | MED (~5-8% MoE wall, uncovered by P2/P4) | MED | med | BE (fp32 reorder; 8cb0ce23) | pairs w/ P2 kernel | NOT STARTED |
| 13 | **D4 (H3) Width-adaptive GDN recurrence launch params** | decode | MED (saturate grid at narrow D) | LOW-MED | low | BE (0022 col-independence) | env GDN_NW/GDN_CPW already exists | NOT STARTED |
| 14 | **D7 (B3) Coalesced paged-KV block layout for decode gather** | decode | MED (effective BW / sector efficiency) | MED | med | BE | profile-gated (#0 FA-read BW) | NOT STARTED |
| 15 | **P6 (A4) Fused MoE FFN (up→SiLU→down resident)** | prefill+decode | MED-HIGH (removes intermediate round-trip) | HIGH | high | BE→KL | after P2 | NOT STARTED |
| 16 | **D9 (B5) Pipeline host sampling off decode critical path** | serving | MED (recovers inter-step sampling bubble) | MED | med | BE | ordering vs in-place recurrent state | NOT STARTED |
| 17 | **D8 (H5/#13) GPU fused sorting-free sampler** | serving | MED (small on greedy; matters at 128-way top-k/p) | MED | med | BE-ish | alt to D9; profile to size | NOT STARTED |
| 18 | **P8 (A6) Stream-K / split-K FP4 prefill GEMM** | prefill | MED (small-output-grid layers on few-SM GB10) | MED | med | BE if det. else KL | profile-gated; complements P1 | NOT STARTED |
| 19 | **P7 (A5/PREFILL-L2) Act-quant fusion into 0042 epilogue (prefill)** | prefill | LOW-MED (~3-6% prefill; vLLM avoids it entirely) | MED | med | BE (md5) | extends landed 0042; after P1 | NOT STARTED |
| 20 | **P9 (#10/flag-3) Tensor-core paged prefill FA** | prefill | LOW-MED, **context-dependent (grows L²)** | MED-HIGH | med | BE→KL | re-profile FA share at real serving lengths first | NOT STARTED |
| 21 | **D11 (B4) Megakernel / persistent decode** | decode | HIGH (kills launch/scheduling bubbles) | VERY HIGH | very high | KL | last resort, only if funded gap remains | NOT STARTED |

Gate key: BE = bit-exact (greedy md5); KL = KL-divergence gate; BE→KL = bit-exact preferred, KL fallback.

## Drop / closed (do NOT pursue)

| Lever | Why dropped |
|-------|-------------|
| Padded / fixed-slot decode (pad-to-`--parallel`) | Built, GPU-tested, **REJECTED** - serving decode is GPU-compute-bound; dummy-row compute > reuse recovered |
| B7 Adaptive-width bucketed decode graph | LOW value on GB10 (same GPU-compute-bound finding); revisit only if host-loop re-confirmed dominant on other HW |
| dequant→bf16 cuBLAS prefill (0033) | **REJECTED** - MMQ beat it 29-49%; superseded by native FP4-MMA (P1) |
| W4A16-Marlin / NVFP4 projections (bf16→FP4) | **REJECTED** - KL-fail; vLLM keeps SAME bf16 projections, no advantage to chase |
| bf16-tau | Dropped |
| Act-quant fusion on **decode** (lever-3) | **FLAT** - decode is BW-bound; the prefill variant (P7) is the live one |
| S2 double-buffer set_inputs | Dropped - set_inputs is cheap (host loop closed by 0040/0041) |
| H6 NVFP4 act-quant decode tax | No bit-exact lever; **exclusion check only** (expected wash vs vLLM, which also FP4-quantizes) |
| P10 (A7) Prefill CUDA-graph capture | LOW/LOW - prefill launch overhead amortized over large M; merely *enabled* by P2, not a standalone item |
| H4 ragged-shape umbrella | Not a lever - it is the shared *root* of H1-H3; fixed by D2/D3/D4 at the kernel level |
| H5 (as exclusion) / H6 | profile-only rule-outs, not builds (D8 is the actual sampler lever) |

## Critical-path sequence (two parallel tracks per the multi-agent GPU methodology)

**Decode-serving track (gated):** #0 serving nsys → bank #3 (D1) → branch on the dominant bucket: if MUL_MAT_ID-GEMV → #8 (D2); if FA → #11 (D3); if recurrence → #13 (D4). In parallel, cheap A/Bs #6 (D10) and #1 (X1). Highest-ceiling greenfield #9 (D5) once SSM-rollback de-risked via zero-draft prompt-lookup. BW cleanup #10 (D6, synergistic with X1).

**Prefill track (already moving):** #2 (P1, in progress) → #4 (P3) → #5 (P2) - and **co-develop the P2 ragged-grouped kernel with the D2 decode kernel** (one fused-MoE dispatch that degrades gracefully across M = vLLM's single fused_moe shape). In parallel #7 (P4, design ready). Then the residual-coverage adds #12 (P5), #15 (P6), #19 (P7). Profile-gated #18 (P8), #20 (P9).

**Two highest non-obvious insights to act on:** (a) the P2 prefill kernel and the D2 decode kernel are the **same kernel** (on-GPU token sort + single persistent grouped FP4-MMA launch) at different M - fund them as one effort. (b) the "serving decode is GPU-compute-bound" finding **invalidates S3's keep-prefill-out rationale** - #6 (D10 co-batching, vLLM-style) and #1 (X1 aggregate concurrency) are the cheap wins that follow from it, and are higher-reward-per-effort than any further host-side or graph-reuse work.

### Phase 9 MTP update

Phase 9 adds a narrow MTP smoke gate instead of production enablement:

- DGX asset check confirmed `qwen35moe.nextn_predict_layers` and
  `blk.40.nextn.*` tensors in `/home/mudler/bench/q36-35b-a3b-nvfp4.gguf`.
- Default `draft-mtp` initially ran but emitted backend-sampler errors because
  MTP verification batches can request more than one output row per sequence.
- Patch `0054-fix-speculative-disable-backend-sampling-for-MTP-drafts.patch`
  disables backend draft sampling inside `draft-mtp`.
- After the patch, the default `draft-mtp` smoke exits cleanly with
  `n_drafted=5`, `n_accept=4`, and `80.000%` acceptance.
- Canonical inference md5 gates stayed stable:
  MoE `8cb0ce23777bf55f92f63d0292c756b0`, dense
  `5951a5b4d624ce891e22ab5fca9bc439`.

MTP remains opt-in and, after Phase 15, rejected as a current GB10 serving
throughput lever. It does not supersede the GDN/paged-serving conclusions unless
a future graph/batch-shape fix changes the serving result.

### Phase 14 MTP rollback update

Phase 14 closes the safety gap left open by Phase 9, but still does not claim a
throughput/parity win:

- `test-recurrent-state-rollback` passed on the actual MoE GGUF and logged
  `recurrent rollback checkpoint restored successfully`.
- MTP stderr showed bounded recurrent rollback support:
  `the context supports bounded partial sequence removal`.
- A partial-rejection run produced `n_drafted=39`, `n_accept=20`,
  `accept=51.282%` with no backend sampler multi-output error.
- Canonical inference gates stayed green after the MTP work:
  MoE `8cb0ce23777bf55f92f63d0292c756b0`, dense
  `5951a5b4d624ce891e22ab5fca9bc439`, and `MUL_MAT_ID` `806/806`.

The greedy-equivalence gate uses normalized raw-output prefix comparison rather
than exact transcript md5 because `llama-speculative-simple` emits accepted
token groups and can produce a longer completion than `llama-completion -no-cnv`
for the same `-n`. Across `n=8,16,24,32,48`, no first differing token was found.

Phase 15 completed that serving/API benchmark and rejected current MTP serving.

### Phase 15 MTP serving update

Phase 15 ran the direct `llama-server` serving A/B that Phase 14 enabled. It
rejects current MTP serving as a parity lever on GB10:

| arm | n | decode agg t/s | decode per-seq t/s | TTFT mean ms |
|---|---:|---:|---:|---:|
| baseline | 8 | 247.8 | 30.70 | 1181.1 |
| MTP | 8 | 109.8 | 14.26 | 1691.5 |
| baseline | 32 | 406.0 | 12.02 | 2762.2 |
| MTP | 32 | 111.7 | 3.61 | 4545.6 |
| baseline | 128 | 662.4 | 4.31 | 7747.2 |
| MTP | 128 | 138.5 | 0.97 | 20385.7 |

Artifact: `/home/mudler/bench/phase15_mtp_serving/20260701_042005`.

MTP did draft and accept tokens (`#gen tokens = 17293`, `#acc tokens = 15493`),
so this is not a no-draft false negative. The likely culprit is graph/batch
shape disruption: baseline logs show heavy graph reuse (`graphs reused = 361`
in the high-concurrency tail), while MTP logs show `graphs reused = 1` and much
higher per-slot eval time. Pre/post canonical inference gates stayed green:
MoE `8cb0ce23777bf55f92f63d0292c756b0`, dense
`5951a5b4d624ce891e22ab5fca9bc439`, and `MUL_MAT_ID` `806/806`.

Do not keep tuning MTP draft length blindly. A follow-up must first profile
speculative verification batch shapes and CUDA graph reuse with
`nsys --cuda-graph-trace=node`.

### Phase 16 MTP graph-reuse profile

Phase 16 ran that profile on a smaller direct serving shape (`n=8`, `ptok=64`,
`gen=64`) with `nsys --cuda-graph-trace=node`.

Artifact: `/home/mudler/bench/phase16_mtp_graph_profile/20260701_043016`.

Result:

- baseline: `decode_agg_tps=230.5`, `graphs reused = 62`,
- MTP: `decode_agg_tps=97.7`, `graphs reused = 1`,
- MTP drafted (`#gen tokens = 460`, `#acc tokens = 346`),
- `nsys stats` showed materially more GPU kernel time in MTP (~`5.89 s`) than
  baseline (~`2.59 s`).

This supports the root-cause hypothesis: current MTP serving disrupts the paged
decode graph-reuse path and increases GPU work. If MTP is reopened, start at
`tools/server/server-context.cpp` speculative verification batch construction
and graph-reuse keys, not draft-length tuning.

### Phase 17 MTP graph-shape feasibility

Phase 17 inspected the source path before any patch. Verdict: no small additive
graph-reuse shortcut is evident.

Key mechanics:

- normal decode appends one `output=true` row per generating slot;
- MTP verification appends `K + 1` `output=true` rows per speculative slot,
  where `K = spec_draft.size()`;
- total shape is `sum(non_spec * 1) + sum(spec * (1 + K_i)) + prompt rows`;
- `n_tokens`, `n_seq_tokens`, `n_outputs`, KQ mask rows, position length, and
  output-id count are hard graph/input dimensions;
- paged-attention block-table bucketing does not stabilize those verification
  token/output dimensions.

Rejected shortcut: fake padding rows. They would be real target decode rows with
KV, position, logits, MTP nextn embedding, sampling-index, and rollback effects,
and they resemble the already rejected fixed-slot dummy-compute experiment.

Only plausible next step: an instrumentation-only patch around
`server_slot::handle_last_sampled_token()` to count verification shape buckets.
Only after that should an opt-in scheduling experiment group/defer MTP
verification by `1 + spec_draft.size()`. Keep it default-off and kill it if TTFT
or throughput regresses, graph reuse does not recover, or the md5/op gates drift.

### Phase 18 MTP shape trace

Phase 18 added that instrumentation-only patch as 0055. Set
`LLAMA_SPEC_SHAPE_TRACE=1` to log normal decode rows and MTP verification
`K + 1` row/output shapes from `server_slot::handle_last_sampled_token()`.
It is default-off and does not change scheduling, graph keys, logits, KV state,
acceptance, or rollback behavior.

Red/green result:

- before patch, `LLAMA_SPEC_SHAPE_TRACE=1` emitted no `spec shape:` lines;
- after patch, a tiny MTP request emitted `kind=verify` shapes with `rows=4`
  and `rows=3`;
- with the env var unset, the patched server emitted no `spec shape:` lines.

Canonical post-patch inference gates stayed green:

- MoE `8cb0ce23777bf55f92f63d0292c756b0`;
- dense `5951a5b4d624ce891e22ab5fca9bc439`;
- `MUL_MAT_ID` `806/806`.

Artifacts:

- `/home/mudler/bench/phase18_mtp_shape_trace_green`
- `/home/mudler/bench/phase18_mtp_shape_trace_green/gate_after`

Follow-up scope: before any source behavior change, run a trace-only real
serving entropy measurement. Only if repeatable draft-length buckets appear
should an opt-in group/defer-by-draft-length scheduler be built; kill it on
TTFT/throughput regression, graph-reuse failure, md5/op drift, or MTP
rollback/prefix gate failure.

### Phase 19 MTP serving shape entropy

Phase 19 ran the trace-only serving measurement. Artifact:
`/home/mudler/bench/phase19_mtp_shape_entropy/20260701_045534`.

Pre/post canonical gates passed: MoE `8cb0ce23777bf55f92f63d0292c756b0`,
dense `5951a5b4d624ce891e22ab5fca9bc439`, and `MUL_MAT_ID` `806/806`.

MTP serving stayed slower:

| n | baseline decode_agg | MTP decode_agg | MTP / baseline | baseline TTFT ms | MTP TTFT ms |
|---|---------------------|----------------|----------------|------------------|-------------|
| 8 | 245.0 | 95.7 | 39.1% | 1147.2 | 1633.4 |
| 32 | 409.2 | 110.0 | 26.9% | 2710.0 | 4471.5 |
| 128 | 697.2 | 154.0 | 22.1% | 7601.5 | 20310.4 |

The shape trace rejects the small scheduler shortcut:

- per-slot draft length is already stable: `draft=3` is 96.2-96.9% of verify
  slots across n8/n32/n128;
- full in-flight steps already mostly use all-`draft=3` vectors;
- remaining aggregate shape churn is active-slot/tail churn plus MTP's real
  `K + 1` output-row expansion;
- group/defer-by-draft would not remove the dominant row expansion and would
  risk more TTFT loss.

Decision: do not build a Phase 20 group/defer scheduler on current evidence.
Future MTP work would need a deeper target-verify graph/state design, not
another small server scheduling shortcut.

### Phase 62 MTP verify-cost result

Phase 62 is recorded in
`docs/superpowers/plans/2026-07-01-mtp-verify-cost-phase62.md`. Artifact:
`/home/mudler/bench/phase62_mtp_verify_cost/20260701_134125`.

Pre/post default inference gates stayed green: MoE md5
`8cb0ce23777bf55f92f63d0292c756b0`, dense md5
`5951a5b4d624ce891e22ab5fca9bc439`, and `MUL_MAT_ID 806/806`.

| n | baseline decode_agg | MTP decode_agg | MTP / baseline | baseline TTFT ms | MTP TTFT ms |
|---|---------------------|----------------|----------------|------------------|-------------|
| 8 | 248.5 | 104.4 | 42.0% | 1150.4 | 1682.9 |
| 32 | 411.8 | 112.8 | 27.4% | 2607.9 | 4444.7 |
| 128 | 696.5 | 148.1 | 21.3% | 7425.2 | 20155.8 |

Final MTP stats: `7372/9340 = 0.789` accepted tokens, mean acceptance length
`3.33`, per-position acceptance `(0.877, 0.767, 0.691)`, and
`graphs reused = 1`. Shape trace again showed `draft=3` / `rows=4` dominance
at `95.6%`.

Decision: reject another MTP implementation phase for now. Phase62 kept default
inference green with md5/op gates, but MTP remains rejected unless a later design
removes target-verify/output-row graph cost. Do not tune `n_max` blindly.

### Phase 20 current-stack serving snapshot

Phase 20 refreshed the MoE serving baseline using the current clean DGX mirror
(`~/llama-phase6-source`, `f2521ab12`) and the same-session vLLM server. Artifact:
`/home/mudler/bench/phase20_current_snapshot/20260701_050621`.

Pre/post canonical gates passed: MoE `8cb0ce23777bf55f92f63d0292c756b0`,
dense `5951a5b4d624ce891e22ab5fca9bc439`, and `MUL_MAT_ID` `806/806`.

| n | paged decode_agg | vLLM decode_agg | paged/vLLM decode | paged agg | vLLM agg | paged/vLLM agg |
|---|------------------|-----------------|-------------------|-----------|----------|----------------|
| 8 | 220.8 | 290.5 | 76.0% | 164.8 | 245.5 | 67.1% |
| 32 | 411.1 | 594.7 | 69.1% | 252.1 | 456.0 | 55.3% |
| 128 | 670.0 | 1022.7 | 65.5% | 322.4 | 662.4 | 48.7% |

TTFT/prefill remains the largest user-visible gap:

| n | paged TTFT ms | vLLM TTFT ms | paged/vLLM TTFT | paged prefill_tps | vLLM prefill_tps |
|---|---------------|--------------|------------------|--------------------|------------------|
| 8 | 783.6 | 271.8 | 2.88x | 1669.9 | 4371.5 |
| 32 | 2630.6 | 783.8 | 3.36x | 1712.8 | 5358.3 |
| 128 | 7678.7 | 2465.7 | 3.11x | 1660.4 | 5242.9 |

Decision: the latest stack is still below vLLM serving parity on GB10. The next
credible parity path is not another MTP/scheduler shortcut; it is either the
documented datacenter-Blackwell rerun or a larger fused-kernel project outside
the low-conflict GB10 patch stack.

### Phase 21 current-stack harness

Phase 21 added `paged-current-serving-snapshot.sh` so Phase 20 can be repeated
without the stale DGX `combined_definitive.sh` assumptions. The script defaults
to `~/llama-phase6-source`, enforces docker/`local-ai-worker`/GPU-idle preflight,
uses the owner-file lock, runs pre/post md5/op gates, runs paged and vLLM in the
same session, and emits ratio rows in `summary.tsv`.

Verification:

- local `bash -n` and `--help` passed;
- DGX `DRY_RUN=1` passed and wrote
  `/home/mudler/bench/phase21_harness_dryrun/20260701_051757`.

Use this harness for future current-stack GB10 snapshots before making parity
claims.

### Phase 24 snapshot hardware report

Phase 24 extended `paged-current-serving-snapshot.sh` to write `hardware.txt`
after preflight and before any server launch, including in `DRY_RUN=1`. The
report records `nvidia-smi -L`, GPU name, driver, memory, compute capability
when available, `hardware_class`, and a parity note for that class.

DGX dry run passed and wrote
`/home/mudler/bench/phase24_hardware_report_dryrun/20260701_052741`. It
classified the current DGX as `hardware_class=gb10_or_workstation_blackwell`
with `GPU 0: NVIDIA GB10`, driver `580.159.03`, and compute capability `12.1`.

Use `hardware.txt` when comparing future snapshots. GB10/workstation Blackwell
results do not establish datacenter-Blackwell parity.

### Phase 25 snapshot gate summary

Phase 25 extended `paged-current-serving-snapshot.sh` to write
`gate_summary.tsv` after the post gate in full runs. It also added
`--summarize-gates ART` for auditing existing artifacts without launching
servers.

The Phase 20 artifact was backfilled at
`/home/mudler/bench/phase20_current_snapshot/20260701_050621/gate_summary.tsv`.
It records pre/post MoE md5 `8cb0ce23777bf55f92f63d0292c756b0`, dense md5
`5951a5b4d624ce891e22ab5fca9bc439`, and `MUL_MAT_ID` `806/806` as `ok`.

Use `hardware.txt` plus `gate_summary.tsv` as the quick audit surface before
accepting any new parity snapshot.

### Phase 26 audited current-stack snapshot

Phase 26 ran the full current-stack paged-vs-vLLM MoE serving snapshot with the
Phase 24/25 audit files enabled:
`/home/mudler/bench/phase26_audited_snapshot/20260701_053650`.

The artifact records `hardware_class=gb10_or_workstation_blackwell` on GPU
`NVIDIA GB10` with driver `580.159.03` and compute capability `12.1`.
`gate_summary.tsv` reports every pre/post gate as `ok`: MoE md5
`8cb0ce23777bf55f92f63d0292c756b0`, dense md5
`5951a5b4d624ce891e22ab5fca9bc439`, and `MUL_MAT_ID` `806/806`.

Audited MoE serving result (`PTOK=128`, `GEN=64`):

| n | paged decode_agg | vLLM decode_agg | paged/vLLM decode | paged agg | vLLM agg | paged/vLLM agg |
|---|------------------|-----------------|-------------------|-----------|----------|----------------|
| 8 | 230.8 | 283.2 | 81.5% | 170.6 | 241.6 | 70.6% |
| 32 | 420.0 | 609.0 | 69.0% | 254.6 | 466.7 | 54.6% |
| 128 | 673.4 | 1025.0 | 65.7% | 324.0 | 656.5 | 49.4% |

Decision: the latest audited clean-stack run still does not reach vLLM serving
parity on GB10. Treat Phase 26 as the current benchmark baseline before funding
new kernel work, and keep md5/op gates as the first check when changing the
patch stack.

### Phase 27 graph-node-traced current-stack profile

Phase 27 re-profiled the current clean llama.cpp n128 serving path with
`--cuda-graph-trace=node`, using the same source (`f2521ab12`) and GB10 host.
Artifact: `/home/mudler/bench/phase27_graph_node_serving/20260701_055519`.

The profile run itself reported `decode_agg_tps=675.5`, close to Phase 26's
n128 paged `673.4`, so the trace is representative for bucket direction. Pre
gates passed, and the post-gate retry passed after Nsight teardown finished:
MoE md5 `8cb0ce23777bf55f92f63d0292c756b0`, dense md5
`5951a5b4d624ce891e22ab5fca9bc439`, and `MUL_MAT_ID` `806/806`.

Graph-node-traced macro buckets:

| bucket | time ms | share |
|--------|---------|-------|
| GDN | 6706.33 | 33.47% |
| MoE/FFN-GEMM | 5871.92 | 29.31% |
| bf16-proj | 2725.07 | 13.60% |
| layout-copy | 1309.99 | 6.54% |
| act-quant | 697.75 | 3.48% |
| MoE-dispatch | 275.99 | 1.38% |
| FA | 271.03 | 1.35% |

Fine rows keep the same decision shape as Phase 8: `gdn_core` is `29.59%`,
`mmq_nvfp4` is `28.44%`, while `mm_ids` is `0.61%`, `gather_mmq` is `0.37%`,
and `argsort_topk` is `0.40%`. Do not reopen metadata/helper-only MoE dispatch
work on GB10. Any credible source patch must directly reduce GDN, grouped-MMQ,
or projection work and still pass the md5/op gates.

### Phase 28 NVFP4 MMQ occupancy A/B

Phase 28 challenged the small grouped-MMQ build knobs before funding structural
kernel work. Artifact:
`/home/mudler/bench/phase28_mmq_occupancy/20260701_040450`.

`GGML_CUDA_FP4_MINBLOCKS=2` built and passed the canonical safety gates before
and after serving: MoE md5 `8cb0ce23777bf55f92f63d0292c756b0`, dense md5
`5951a5b4d624ce891e22ab5fca9bc439`, and `MUL_MAT_ID` `806/806`. Same-session
n128 serving A/B rejected it on throughput: baseline `705.1` decode_agg_tps vs
`689.9` with `MINBLOCKS=2` (`0.9784x`). `GGML_CUDA_FP4_MMQ_Y=64` does not
compile against the current NVFP4 writeback invariant
`nwarps*tile_C::I == mmq_y`, so the row-tile knob is not a valid low-conflict
shortcut.

Decision: do not promote the occupancy knobs and do not add a LocalAI patch.
The grouped-MMQ bucket still requires structural kernel work; launch-bounds and
row-tile build tweaks are closed on GB10.

### Phase 29 default-off MoE MMQ shape trace

Patch `0056` adds `LLAMA_MOE_MMQ_SHAPE_TRACE=<n>` as bounded, default-off
instrumentation at the grouped-MMQ host selector. Artifact:
`/home/mudler/bench/phase29_mmq_shape_trace/20260701_042428`. Fork commit:
`20a99518a feat(cuda): trace moe mmq batch shapes`.

The helper was added test-first (`test-cuda-mmq-shape-trace` failed on the
missing header before implementation, then passed locally and under the DGX CUDA
build). Default-off and trace-enabled gates both passed: MoE md5
`8cb0ce23777bf55f92f63d0292c756b0`, dense md5
`5951a5b4d624ce891e22ab5fca9bc439`, and `MUL_MAT_ID` `806/806`. The
trace-enabled gate with `LLAMA_MOE_MMQ_SHAPE_TRACE=4` emitted exactly four
shape lines.

Use this only to size the next grouped-MMQ structural kernel. It intentionally
does not perform device readback of `expert_bounds`, so it records selector
inputs and estimated density rather than exact per-expert histograms.

### Phase 30 live serving MMQ shape distribution

Phase 30 ran n128 serving with `LLAMA_MOE_MMQ_SHAPE_TRACE=4096` on the patched
DGX mirror (`826c97a05`). Artifact:
`/home/mudler/bench/phase30_mmq_shape_serving/20260701_043300`.

The first 4096 grouped-MMQ calls split into 1200 decode-like calls
(`ncols_max <= 128`) and 2896 prefill-like calls. Decode-like calls used
densities `1-4` and selected only `mmq_x_best` `32/40/48/64`
(`64`: 480, `32`: 360, `40`: 240, `48`: 120). Prefill-like calls were mostly
density `16` and selected `mmq_x_best=128` for 1816 calls. Every traced call had
`stream_k=1`.

Kernel implication: the next grouped-MMQ structural experiment should target
small-M decode tiles (`ncols_max` 26-111, density 1-4) separately from prefill.
The current stream-k/fixup path is part of the measured shape and cannot be
ignored by a replacement kernel.

### Phase 31 live serving MMQ launch distribution

Phase 31 added patch `0057`, extending `LLAMA_MOE_MMQ_SHAPE_TRACE=<n>` with
`[LLAMA_MOE_MMQ_LAUNCH]` lines emitted from `launch_mul_mat_q` after the actual
stream-k launch policy is known. Artifact:
`/home/mudler/bench/phase31_mmq_launch_trace/20260701_064424`.

The default-off, trace-enabled, and post-serving gates all stayed bit-exact:
MoE `8cb0ce23777bf55f92f63d0292c756b0`, dense
`5951a5b4d624ce891e22ab5fca9bc439`, and `MUL_MAT_ID` `806/806`.

Live n128 serving with `LLAMA_MOE_MMQ_SHAPE_TRACE=4096` produced:

| bucket | launch lines | `fixup=1` | `stream_k_blocks == ntiles_dst` | tile efficiency |
|--------|--------------|-----------|----------------------------------|-----------------|
| decode-like (`ncols_max <= 128`) | 4800 | 0 | 4800 | 96-99 |
| prefill-like (`ncols_max > 128`) | 4920 | 0 | 4920 | 99-100 |

Lever implication: a no-fixup/no-stream-k shortcut is rejected for the measured
n128 serving workload. The launch code is already choosing conventional
stream-k tiling with no fixup; the remaining gap is the small-M grouped-MMQ
kernel shape itself, not launch/fixup overhead.

### Phase 32 small-M MMQ candidate classifier

Phase 32 added patch `0058`, a default-off
`LLAMA_MOE_MMQ_SMALL_M_TRACE=<n>` classifier for decode-like low-density MoE
grouped-MMQ calls. Artifact:
`/home/mudler/bench/phase32_small_m_classifier/20260701_070127`.

The default-off, trace-enabled, and post-serving gates all stayed bit-exact:
MoE `8cb0ce23777bf55f92f63d0292c756b0`, dense
`5951a5b4d624ce891e22ab5fca9bc439`, and `MUL_MAT_ID` `806/806`.

Live n128 serving with `LLAMA_MOE_MMQ_SMALL_M_TRACE=4096` found 4096 candidate
calls:

| metric | notable values |
|--------|----------------|
| `mmq_x_best` | `64`: 1800, `48`: 1096, `40`: 360, `32`: 360, `16`: 360, `24`: 120 |
| density | `4`: 1440, `3`: 1336, `1`: 840, `2`: 480 |

Lever implication: Phase 33 should A/B a default-off small-M tile policy, first
forcing candidate calls to `mmq_x=16` and only then trying `8` if it compiles
and keeps the NVFP4 tile invariants. This matches the vLLM/Marlin lesson that
low-density routed expert rows want smaller M blocks, without porting Marlin,
Triton, TMA, tcgen05, or layout repack machinery.

### Phase 33 small-M tile policy rejection

Phase 33 added patch `0059`, default-off `LLAMA_MOE_SMALL_M_TILE=<n>`, and
tested the obvious vLLM-like shortcut on the Phase 32 candidate population.
Artifact: `/home/mudler/bench/phase33_small_m_tile_policy/20260701_071136`.

Default-off, tile16, tile8, and post-serving gates were all bit-exact: MoE
`8cb0ce23777bf55f92f63d0292c756b0`, dense
`5951a5b4d624ce891e22ab5fca9bc439`, and `MUL_MAT_ID` `806/806`.

Same-session n128 serving:

| mode | decode_agg_tps | ratio |
|------|----------------|-------|
| baseline | 672.1 | 1.000x |
| `LLAMA_MOE_SMALL_M_TILE=16` | 640.3 | 0.953x |
| `LLAMA_MOE_SMALL_M_TILE=8` | 583.2 | 0.868x |

Lever implication: smaller `mmq_x` alone is rejected for n128 serving. The
remaining grouped-MMQ gap is not solved by emulating Marlin's small `block_size_m`
with the current MMQ kernel; a future attempt must alter the kernel's internal
work partitioning or move to a different bottleneck.

### Phase 34 MMID route trace

Phase 34 added patch `0060`, default-off `LLAMA_MOE_MMID_ROUTE_TRACE=<n>`, to
classify the live `MUL_MAT_ID` dispatch route without changing the route. Artifact:
`/home/mudler/bench/phase34_mmid_route_trace/20260701_072737`.

Default-off, trace-enabled, and post-serving gates were all bit-exact: MoE
`8cb0ce23777bf55f92f63d0292c756b0`, dense
`5951a5b4d624ce891e22ab5fca9bc439`, and `MUL_MAT_ID` `806/806`.

Live n128 serving with `LLAMA_MOE_MMID_ROUTE_TRACE=4096` produced:

| route | count | host sync |
|-------|-------|-----------|
| grouped `mmq` | 2776 | 0 |
| `mmvq` | 1320 | 0 |
| `mmf` | 0 | 0 |
| fallback | 0 | 0 |

Top route shapes were `mmq ne2=12` (1096), `mmq ne2=18` (480), and
`mmvq ne2=8` (360). Lever implication: the old D1 concern that current n128
serving might fall into the per-expert host-sync fallback is refuted for this
stack. The remaining MoE route issue is grouped-MMQ small-M efficiency, not
fallback dispatch avoidance.

### Phase 35 regular MUL_MAT route trace

Phase 35 added patch `0061`, default-off `LLAMA_MUL_MAT_ROUTE_TRACE=<n>`, to
classify regular `MUL_MAT` routes for the `bf16-proj` serving bucket. Artifact:
`/home/mudler/bench/phase35_mul_mat_route_trace/20260701_074359`.

Default-off, trace-enabled, and post-serving gates were all bit-exact: MoE
`8cb0ce23777bf55f92f63d0292c756b0`, dense
`5951a5b4d624ce891e22ab5fca9bc439`, `MUL_MAT` `1146/1146`, and `MUL_MAT_ID`
`806/806`.

Live n128 serving with `LLAMA_MUL_MAT_ROUTE_TRACE=8192` produced:

| route | count |
|-------|-------|
| `mat_f` | 2888 |
| `op_cublas` | 2292 |
| `mmq` | 1328 |
| `vec_q` | 1214 |
| `vec_f` | 470 |

The trace was BF16-heavy (`type=30`: 3965 calls), mostly `mat_f=2485` and
`op_cublas=1330`. Top BF16 shapes were `mat_f ne1=12` (775),
`op_cublas ne1=18` (760), and `mat_f ne1=8` (570); `ne12=ne13=1` throughout the
top shapes, so batched cuBLAS is not the measured target.

Lever implication: the next projection phase should add cuBLAS/MMF subroute
detail or test a narrow BF16 route policy for the generic `op_cublas` shapes.
Do not spend time on batched cuBLAS for this n128 serving slice. If MTP is enabled
in a future serving configuration, first isolate `mtp_eh_proj` / shared-head
projection with `llama-debug --tensor-filter 'mtp_|h_nextn|nextn|ffn_|attn_'`
before optimizing ordinary decoder projections.

### Phase 36 cuBLAS subroute trace

Phase 36 added patch `0062`, default-off `LLAMA_CUBLAS_ROUTE_TRACE=<n>`, to
classify the generic cuBLAS `MUL_MAT` subroute without changing branch behavior.
Artifact: `/home/mudler/bench/phase36_cublas_route_trace/20260701_081228`.

Default-off, trace-enabled, and post-serving gates were all bit-exact: MoE
`8cb0ce23777bf55f92f63d0292c756b0`, dense
`5951a5b4d624ce891e22ab5fca9bc439`, `MUL_MAT` `1146/1146`, and `MUL_MAT_ID`
`806/806`.

Live n128 serving with `LLAMA_CUBLAS_ROUTE_TRACE=8192` produced:

| cuBLAS route | count |
|--------------|------:|
| `bf16_tc` | 5681 |
| `sgemm` | 2511 |

Top SGEMM shapes were `type=0 row_diff=256/1 src1_ncols=510 ne00=2048
ne10=2048`. Lever implication: the measured `op_cublas` bucket is BF16
tensor-core plus F32 SGEMM, not NVFP4 cuBLAS and not batched cuBLAS. The next
projection phase should explain whether the F32 SGEMM shapes are expected glue
tensors or a missed BF16 route, with md5/op gates before any route policy A/B.

### Phase 37 cuBLAS tensor-name trace

Phase 37 added patch `0063`, extending `LLAMA_CUBLAS_ROUTE_TRACE=<n>` with
`src0`, `src1`, and `dst` names. Artifact:
`/home/mudler/bench/phase37_cublas_name_trace/20260701_083227`.

Default-off, trace-enabled, and post-serving gates stayed bit-exact: MoE
`8cb0ce23777bf55f92f63d0292c756b0`, dense
`5951a5b4d624ce891e22ab5fca9bc439`, `MUL_MAT` `1146/1146`, and `MUL_MAT_ID`
`806/806`.

Live n128 serving with trace cap 4096 found `bf16_tc=2884`, `sgemm=1212`.
The `sgemm type=0` entries are the MoE gate logits and shared-expert gate
projections: `blk.N.ffn_gate_inp.weight -> ffn_moe_logits-N` and
`blk.N.ffn_gate_inp_shexp.weight -> shared_expert_gate-N`. Attention and SSM
projections in the sample are already `bf16_tc`.

Lever implication: do not blindly force the `sgemm` bucket to BF16. First inspect
why `ffn_gate_inp*` loads as F32 and whether a dtype or graph-route change is
precision-safe. If attempted, use md5/op gates plus KL validation.

### Phase 38 gate projection policy

Phase 38 re-ran the current Phase37 build safety gate before changing policy:
artifact `/home/mudler/bench/phase38_gate_baseline/20260701_084410`, MoE
`8cb0ce23777bf55f92f63d0292c756b0`, dense
`5951a5b4d624ce891e22ab5fca9bc439`, `MUL_MAT` `1146/1146`, and `MUL_MAT_ID`
`806/806`.

Source check: llama.cpp's Qwen35MoE graph uses `ffn_gate_inp.weight` for
`ffn_moe_logits` and `ffn_gate_inp_shexp.weight` for `shared_expert_gate`. vLLM
Qwen3-Next also constructs those gates with `quant_config=None`; the relevant
vLLM idea is not reduced precision, but concatenating router and shared-expert
gate weights in the fused-MoE runner when shared-expert fusion is active.

Lever implication: keep `ffn_gate_inp*` as inference-critical F32 policy. A
future low-conflict experiment may test a default-off fused F32 gate projection
that computes both logits in one matmul and splits the output, but it must pass
MoE/dense md5 and `MUL_MAT`/`MUL_MAT_ID` gates before benchmarking. If md5
changes, run the KL gate first and reject on any KL regression.

### Phase 39 gate fusion feasibility

Phase 39 rejected the tempting low-conflict implementation of the Phase38 idea:
do not build a graph-time `ggml_concat()` of `ffn_gate_inp.weight` and
`ffn_gate_inp_shexp.weight` just to issue one combined gate matmul. Phase37
proved the named `sgemm` bucket is the two gate projections, but Phase27's
graph-node serving profile already has `concat_layout=459.84ms` (`2.29%`,
`2250` instances) in a `20.0372s` kernel window. Adding another concat path for
weights would likely trade one small SGEMM shortcut for more layout-copy work.

The follow-up remains valid only in the persistent-weight form: create a
load-time F32 combined gate tensor, run one matmul, and view/split the output
into `ffn_moe_logits` and `shared_expert_gate`. That is a model-loader/weight
layout feature, not a graph shortcut. It must stay default-off until MoE/dense
md5, `MUL_MAT`, `MUL_MAT_ID`, and KL-if-md5-changes gates pass.

### Phase 40 max-concurrency C1 check

Phase 40 tested whether paged KV's memory advantage creates a higher-concurrency
GB10 serving point that closes the vLLM gap. Artifact:
`/home/mudler/bench/phase40_max_concurrency/20260701_090012`. The run used
`PARALLEL=256`, `CTX=262144`, `PTOK=128`, `GEN=64`, `NPL="128 192 256"`, and
`OPS=MUL_MAT,MUL_MAT_ID`.

Pre/post gates stayed green: MoE `8cb0ce23777bf55f92f63d0292c756b0`, dense
`5951a5b4d624ce891e22ab5fca9bc439`, `MUL_MAT` `1146/1146`, and `MUL_MAT_ID`
`806/806`.

Result:

| n | paged decode / vLLM | paged per-seq / vLLM | paged agg / vLLM | paged TTFT / vLLM |
|---|---------------------|----------------------|------------------|-------------------|
| 128 | `0.6630` | `0.5908` | `0.4986` | `3.1682` |
| 192 | `0.5737` | `0.5123` | `0.4562` | `3.0216` |
| 256 | `0.6354` | `0.5359` | `0.4721` | `2.9401` |

Decision: C1 does not close GB10 parity at `PTOK=128`, `GEN=64`, and `n<=256`.
Paged safely serves `n=256`, but vLLM also fits and remains faster. Do not use
the memory-footprint advantage as a parity claim at this tested point; any
future C1 retry must push beyond it and keep md5 plus `MUL_MAT`/`MUL_MAT_ID`
gates.

### Phase 41 low-concurrency serving check

Phase 41 measured the low-concurrency serving regime where any remaining
host/scheduler gap should be most visible. Artifact:
`/home/mudler/bench/phase41_low_concurrency/20260701_091437`. The run used
`PARALLEL=32`, `CTX=32768`, `PTOK=128`, `GEN=64`, `NPL="1 8 32"`, and
`OPS=MUL_MAT,MUL_MAT_ID`.

Pre/post gates stayed green: MoE `8cb0ce23777bf55f92f63d0292c756b0`, dense
`5951a5b4d624ce891e22ab5fca9bc439`, `MUL_MAT` `1146/1146`, and `MUL_MAT_ID`
`806/806`.

Result:

| n | paged decode / vLLM | paged per-seq / vLLM | paged agg / vLLM | paged TTFT / vLLM |
|---|---------------------|----------------------|------------------|-------------------|
| 1 | `0.7493` | `0.7501` | `0.7496` | `1.3830` |
| 8 | `0.7518` | `0.7398` | `0.6334` | `3.1425` |
| 32 | `0.6649` | `0.6397` | `0.5282` | `3.4014` |

Decision: low-concurrency remains a gap, but Phase41 does not reopen
D1/full-step graph capture. Patch `0043` already ships grouped-MMQ full-step
decode graph capture default-on, Phase34 found `host_sync=0/4096`, and S3 is
intentionally default-off because it hurts TTFT/end-to-end throughput. Treat
D1 as closed on the current GB10 path unless a fresh route trace proves a
host-sync fallback or graph-disable condition has returned. TTFT evidence keeps
prefill GDN/MoE work in scope for serving quality.

### Phase 42 target reconciliation

Phase 42 challenged the current target list with three read-only subagent
reviews:

- D1/full-step graph capture: closed on current GB10 path. `0040` S1 is
  default-on graph reuse, `0041` S3 is opt-in only, and `0043` D1 is default-on
  grouped-MMQ full-step CUDA graph capture.
- GDN prefill: the shipped GB10 wins are `0046`/`0047`; later C32 slab,
  QS-early, and Global-Ai32 variants were correctness-clean but slower. Do not
  add another low-conflict GDN reorder on GB10.
- W4A16 / prefill GEMM: `0033`/`0034`/`0035` remain default-off; `0048`-`0050`
  improved forced W4A16 only marginally and still did not beat default MMQ. Do
  not add another small W4A16 body/metadata tweak.

Phase 43 then checked that candidate against the actual Qwen35MoE model-loader
path and rejected it as a small shortcut. `ffn_gate_inp.weight` and
`ffn_gate_inp_shexp.weight` are separate GGUF tensors consumed by separate graph
matmuls; `create_tensor(...)` only materializes tensors from GGUF metadata, and
`create_tensor_as_view(...)` can view existing tensors but cannot create a new
persistent concatenated derived weight. A correct load-time combined gate would
need a general derived-weight allocation/materialization path across mmap,
offload, split buffers, and MTP blocks. Do not implement a Qwen-only loader hack,
and do not fall back to graph-time `ggml_concat()`.

The resulting GB10 state after Phase43: no remaining low-conflict shortcut patch
is justified by the current evidence. Future work needs either a larger funded
kernel/loader design or a hardware-pivot benchmark, with the canonical
MoE/dense md5, `MUL_MAT`, `MUL_MAT_ID`, and KL-if-md5-changes gates.

### Phase 44 hardware-pivot harness readiness

Phase 44 makes `paged-current-serving-snapshot.sh` usable for hardware-pivot
comparisons without editing the script for each vLLM deployment shape. It adds
environment overrides for `VLLM_GPU_MEMORY_UTILIZATION`, `VLLM_MAX_MODEL_LEN`,
`VLLM_MAX_NUM_SEQS`, `VLLM_TENSOR_PARALLEL_SIZE`, and whitespace-split
`VLLM_EXTRA_ARGS`, then prints the resolved values in `DRY_RUN=1` output.

This is deliberately a harness-only phase. It does not change inference code,
does not regenerate the llama.cpp patch series, and does not produce a new
throughput result. Its purpose is to keep the audited methodology portable:
future non-GB10 snapshots can carry the same `hardware.txt`, pre/post md5,
`MUL_MAT`/`MUL_MAT_ID`, and KL-if-md5-changes gates while using hardware-specific
vLLM serving limits.

### Phase 45 inference gate guard

Phase 45 ran the canonical paged inference safety gate after the Phase44 harness
change. Artifact:
`/home/mudler/bench/phase45_inference_gate_guard/20260701_094320`.

Results stayed green on the DGX phase36 build: MoE md5
`8cb0ce23777bf55f92f63d0292c756b0`, dense md5
`5951a5b4d624ce891e22ab5fca9bc439`, `MUL_MAT` `1146/1146`, and
`MUL_MAT_ID` `806/806`. This confirms the current build still satisfies the
inference-safety gates before any later hardware-pivot or larger kernel work.

### Phase 46 served-model-name harness readiness

Phase 46 removes the hardcoded `q36` served model name from
`paged-current-serving-snapshot.sh`. The new `SERVED_MODEL_NAME` environment
variable defaults to `q36` and is used consistently for vLLM
`--served-model-name`, the vLLM `/v1/models` readiness check, and h2h `--model`
requests on both arms.

DGX dry-run artifact:
`/home/mudler/bench/phase46_served_model_name_dryrun/20260701_094849`.
Preflight was clean and the dry run printed
`SERVED_MODEL_NAME=dense-q36` before any server launch. This is another
harness-only portability step for dense or hardware-pivot snapshots; it does not
change inference code or produce a new throughput result.

### Phase 47 dense serving snapshot attempt

Phase 47 attempted a dense audited serving snapshot with
`MODEL=$HOME/bench/q36-27b-nvfp4.gguf`,
`VLLM_MODEL=$HOME/bench/q36-27b-nvfp4-vllm`, and
`SERVED_MODEL_NAME=dense-q36`. Dry-run artifact:
`/home/mudler/bench/phase47_dense_serving_dryrun/20260701_095141`.

The full attempt at
`/home/mudler/bench/phase47_dense_serving/20260701_095151` is incomplete and is
not a parity result. Pre-gates passed and the paged dense arm completed through
`n=128`, but vLLM dense startup exceeded the old fixed readiness budget before
any vLLM result JSONs were produced. Use this artifact only as the root-cause
input for Phase48.

### Phase 48 serving harness readiness hardening

Phase 48 fixes the harness issue exposed by Phase47. It adds
`LLAMA_READY_ATTEMPTS` and `VLLM_READY_ATTEMPTS`, bounds each readiness probe
with `curl --max-time 2`, and replaces direct server waits with bounded cleanup
that escalates from `SIGTERM` to `SIGKILL`.

DGX dry-run artifact:
`/home/mudler/bench/phase48_readiness_harness_dryrun/20260701_100533`. The dry
run printed `VLLM_READY_ATTEMPTS=700` with clean preflight. Retry dense serving
snapshots with this hardening before interpreting dense paged-vs-vLLM ratios.

### Phase 47 dense serving snapshot retry

After Phase48, the dense snapshot completed at
`/home/mudler/bench/phase47_dense_serving_retry/20260701_100811` with pre/post
gates green: MoE `8cb0ce23777bf55f92f63d0292c756b0`, dense
`5951a5b4d624ce891e22ab5fca9bc439`, `MUL_MAT` `1146/1146`, and `MUL_MAT_ID`
`806/806`.

Dense paged-vs-vLLM ratios:

| n | paged decode / vLLM | paged per-seq / vLLM | paged agg / vLLM | paged TTFT / vLLM |
|---|---------------------|----------------------|------------------|-------------------|
| 1 | `1.3434` | `1.3488` | `1.3021` | `1.8746` |
| 8 | `1.1560` | `1.1493` | `0.9142` | `4.0467` |
| 32 | `0.9036` | `0.8382` | `0.6168` | `3.6450` |
| 128 | `0.7912` | `0.6436` | `0.5071` | `3.2011` |

Decision: dense low-N decode remains a real paged strength, but dense serving
still does not close GB10 parity because TTFT and high-concurrency aggregate
throughput remain substantially behind vLLM.

### Phase 50 dense true decode profile

Phase50 profiles dense `npl=128`, `npp=128` decode with graph nodes expanded and
uses the difference method (`ntg=64 - ntg=16`) instead of the Phase47 h2h
serving window. Artifact:
`/home/mudler/bench/phase50_dense_true_decode/20260701_103120`.

Pre/post inference gates stayed green on the profiled `build-cuda` binary set:
MoE `8cb0ce23777bf55f92f63d0292c756b0`, dense
`5951a5b4d624ce891e22ab5fca9bc439`, `MUL_MAT` `1146/1146`, and
`MUL_MAT_ID` `806/806`. A `build-phase36` pre-gate also passed, but
`build-phase36` did not contain `llama-batched-bench`, so `build-cuda` is the
profiled/gated build for this phase.

Results:

| engine | ntg16 wall s | ntg64 wall s | delta tokens | delta wall s | true decode t/s |
|--------|--------------|--------------|--------------|--------------|-----------------|
| paged | `5.754` | `21.768` | `6144` | `16.014` | `383.66` |
| vLLM | `13.041` | `27.165` | `6144` | `14.124` | `435.00` |
| ratio | | | | | `0.8820` |

Decision: Phase47's dense high-N serving loss is not just a kernel-speed gap.
True dense decode is still behind vLLM by about `12%`, but the Phase47 h2h
decode ratio at `n=128` was `0.7912` and aggregate serving was only `0.5071`.
The remaining difference points at scheduler/admission, prefill overlap, and
TTFT accounting. Next implementation target should be an opt-in
batch-composition/admission trace in `server_context::pre_decode()` before any
new GDN/GEMM shortcut.

### Phase 51 serving admission trace

Phase51 adds that trace in the llama.cpp fork. Fork commit:
`c6cb8460e feat(server): trace serving admission batches`.

The change is default-off behind `LLAMA_SERVING_TRACE=1` and does not change
inference decisions. It records aggregate scheduler-shape counters from
`server_context_impl::pre_decode()`: decode tokens, prompt tokens admitted,
waiting prompt slots, started/continued prompt slots, decode-only steps,
`n_batch`, `n_ubatch`, `prefill_budget_step`, and `prefill_cap_per_slot`.

Verification:

- Red test first: `test-server-admission-trace` failed before
  `server-admission-trace.h` existed.
- Local fork: unit test and `llama-server` build passed.
- DGX artifact:
  `/home/mudler/bench/phase51_serving_admission_trace/20260701_110130`
- DGX patched `build-cuda` CTest passed.
- DGX patched `build-cuda` inference gates stayed green: MoE
  `8cb0ce23777bf55f92f63d0292c756b0`, dense
  `5951a5b4d624ce891e22ab5fca9bc439`, `MUL_MAT` `1146/1146`, and
  `MUL_MAT_ID` `806/806`.

Mirror status: pending explicit approval to push the fork branch, then
regenerate the LocalAI patch series from the pushed fork commit.

### Phase 52 dense admission trace

Phase52 used the Phase51 trace on DGX to measure dense `n=128`, `ptok=128`,
`gen=64` llama-server admission. Artifact:
`/home/mudler/bench/phase52_dense_admission_trace/20260701_111017`.

The traced build was bracketed by canonical gates, all green before and after:
MoE `8cb0ce23777bf55f92f63d0292c756b0`, dense
`5951a5b4d624ce891e22ab5fca9bc439`, `MUL_MAT` `1146/1146`, and
`MUL_MAT_ID` `806/806`.

Clean trace:

| h2h wall s | decode agg t/s | TTFT mean ms | steps | decode-only steps | decode tokens | prompt tokens | max waiting prompt slots |
|------------|-----------------|--------------|-------|-------------------|---------------|---------------|--------------------------|
| `58.921` | `360.5` | `23171.5` | `76` | `0` | `8064` | `22785` | `35` |

Decision: the default scheduler never emitted pure decode steps for this
high-N dense run. Prompt tokens matched h2h exactly, and prompt admission used
the stock path (`prefill_budget_step=0`, `prefill_cap_per_slot=0`). This
supports the Phase50 conclusion that the remaining high-N serving gap is
scheduler/admission and TTFT shaped. Next lever should be a default-off
admission-policy A/B or per-step histogram trace, not immediate kernel work.

### Phase 53 admission budget sweep

Phase53 tested the already-existing default-off budget knobs:
`LLAMA_MAX_BATCH_TOKENS=1536/1024` with `LLAMA_PREFILL_CAP=512`, using the same
dense `n=128`, `ptok=128`, `gen=64` traced serving shape. Artifact:
`/home/mudler/bench/phase53_dense_admission_budget_sweep/20260701_111915`.

Pre/post md5 and op gates stayed green: MoE
`8cb0ce23777bf55f92f63d0292c756b0`, dense
`5951a5b4d624ce891e22ab5fca9bc439`, `MUL_MAT` `1146/1146`, and
`MUL_MAT_ID` `806/806`.

| variant | agg t/s | decode agg t/s | prefill t/s | TTFT mean ms | wall s | max waiting prompt slots |
|---------|---------|-----------------|-------------|--------------|--------|--------------------------|
| default Phase52 | `139.0` | `360.5` | `629.5` | `23171.5` | `58.921` | `35` |
| `T=1536 cap=512` | `134.4` | `376.7` | `607.0` | `22263.7` | `60.968` | `26` |
| `T=1024 cap=512` | `130.0` | `392.4` | `565.2` | `23234.3` | `63.003` | `16` |

Decision: simple budget shrinkage is rejected as a parity lever. It improves
the h2h decode-agg metric by starving/slimming prompt admission, but aggregate
throughput and prefill throughput fall, and TTFT does not materially improve.
Next scheduler work should collect per-step histograms or test a targeted
first-token admission policy.

### Phase 54 admission histogram trace

Phase54 extended the Phase51 default-off trace with prompt-token,
decode-token, and waiting-slot histograms. Fork stack:
`c6cb8460e feat(server): trace serving admission batches` and
`bd7b2e952 feat(server): add admission trace histograms`.

Artifact:
`/home/mudler/bench/phase54_admission_hist_trace/20260701_113201`.

Pre/post md5 and op gates stayed green on the temporary DGX patch stack:
MoE `8cb0ce23777bf55f92f63d0292c756b0`, dense
`5951a5b4d624ce891e22ab5fca9bc439`, `MUL_MAT` `1146/1146`, and
`MUL_MAT_ID` `806/806`.

The Phase52-aligned dense run used `n=128`, `ptok=168`, `gen=64`, producing
`prompt_tok_total=22913`, `agg_tps=138.1`, `decode_agg_tps=360.2`,
`prefill_tps=626.7`, `ttft_mean_ms=23393.2`, and `wall_s=59.303`.

Trace:

```text
steps=76 decode_only_steps=0 decode_tokens=8064 prompt_tokens=22913 waiting_prompt_slots=267 max_waiting_prompt_slots=34 prompt_hist=0:63,1-64:1,513+:12 decode_hist=0:3,1-63:10,64-127:10,128-255:53 waiting_hist=0:63,1-7:1,8-15:2,16-31:9,32-63:1
```

Decision: the scheduler does not spend every step over-admitting prompt work.
Most steps have no waiting prompts and no prompt tokens, while prompt admission
is concentrated into a small number of large chunks. This rejects global
budget-shrinkage as the next path and points to a targeted first-token
admission or prompt-front-loading A/B, gated by the same md5 and backend-op
checks.

### Phase 55 TTFT prefill-first scheduler A/B

Phase55 implemented the targeted first-token admission A/B proposed by
Phase54. It is default-off behind `LLAMA_TTFT_PREFILL_FIRST=1`; while any prompt
is still waiting for first-token admission, it defers token 2+ decode rows from
already-started streams. This does not lower `LLAMA_MAX_BATCH_TOKENS` and does
not change default scheduling.

Fork commit:
`8a97629a4 feat(server): add TTFT prefill-first scheduler mode`.

Artifact:
`/home/mudler/bench/phase55_ttft_prefill_first/20260701_114929`.

Pre/post/after-A-B md5 and op gates stayed green on the temporary DGX patch
stack: MoE `8cb0ce23777bf55f92f63d0292c756b0`, dense
`5951a5b4d624ce891e22ab5fca9bc439`, `MUL_MAT` `1146/1146`, and `MUL_MAT_ID`
`806/806`.

Dense `n=128`, `ptok=168`, `gen=64` A/B:

| variant | agg t/s | decode agg t/s | prefill t/s | TTFT mean ms | TTFT max ms | wall s |
|---------|---------|-----------------|-------------|--------------|-------------|--------|
| default | `138.2` | `361.3` | `626.0` | `23231.9` | `36599.5` | `59.272` |
| `LLAMA_TTFT_PREFILL_FIRST=1` | `142.9` | `336.9` | `694.2` | `21520.8` | `33008.2` | `57.323` |

Trace comparison:

- Default: `ttft_deferred_decode_slots=0`,
  `prompt_hist=0:63,1-64:1,513+:12`,
  `decode_hist=0:3,1-63:10,64-127:10,128-255:53`.
- Opt-in: `ttft_deferred_decode_slots=660`,
  `prompt_hist=0:63,1-64:1,257-512:1,513+:11`,
  `decode_hist=0:13,128-255:63`.

Decision: keep the policy as a promising default-off scheduler A/B. It improved
dense aggregate throughput by `+3.4%`, mean TTFT by `-7.4%`, max TTFT by
`-9.8%`, and wall time by `-3.3%`. The h2h decode-agg drop is expected because
the policy shifts early compute from token 2+ decode to first-token prompt
admission. Before any default-on discussion, test MoE serving and at least one
additional concurrency point.

### Phase 56 TTFT prefill-first validation

Phase56 made no code changes. It reapplied the Phase55 stack temporarily on DGX
and tested the opt-in policy on MoE `n=128` and dense `n=32`. Artifact:
`/home/mudler/bench/phase56_ttft_prefill_first_validation/20260701_115852`.

Pre/post md5 and op gates stayed green: MoE
`8cb0ce23777bf55f92f63d0292c756b0`, dense
`5951a5b4d624ce891e22ab5fca9bc439`, `MUL_MAT` `1146/1146`, and `MUL_MAT_ID`
`806/806`.

MoE `n=128`, `ptok=128`, `gen=64`:

| variant | agg t/s | decode agg t/s | prefill t/s | TTFT mean ms | TTFT max ms | wall s |
|---------|---------|-----------------|-------------|--------------|-------------|--------|
| default | `341.1` | `651.2` | `1555.9` | `7168.1` | `11435.5` | `24.015` |
| `LLAMA_TTFT_PREFILL_FIRST=1` | `339.9` | `623.8` | `1622.7` | `7615.3` | `10964.4` | `24.098` |

Dense `n=32`, `ptok=168`, `gen=64`:

| variant | agg t/s | decode agg t/s | prefill t/s | TTFT mean ms | TTFT max ms | wall s |
|---------|---------|-----------------|-------------|--------------|-------------|--------|
| default | `104.3` | `197.1` | `617.2` | `7687.7` | `9234.4` | `19.627` |
| `LLAMA_TTFT_PREFILL_FIRST=1` | `106.7` | `193.5` | `662.1` | `7284.3` | `8609.1` | `19.194` |

Decision: keep `LLAMA_TTFT_PREFILL_FIRST=1` opt-in only. It helps dense
serving at `n=128` and `n=32`, but MoE `n=128` regresses mean TTFT by `+6.2%`
and aggregate throughput by `-0.4%`. Do not promote it as a broad default.
Future scheduler work should either narrow the policy to dense/non-MoE shapes or
make the defer condition more selective for MoE.

### Phase 57 capped TTFT defer sweep

Phase57 added `LLAMA_TTFT_PREFILL_FIRST_MAX_DEFER` as an optional per-step cap
on the Phase55 policy. Unset or `0` keeps the Phase55 unlimited behavior.
Artifact: `/home/mudler/bench/phase57_ttft_cap_sweep/20260701_120830`.

Pre/post md5 and op gates stayed green: MoE
`8cb0ce23777bf55f92f63d0292c756b0`, dense
`5951a5b4d624ce891e22ab5fca9bc439`, `MUL_MAT` `1146/1146`, and `MUL_MAT_ID`
`806/806`.

MoE `n=128`, `ptok=128`, `gen=64`:

| variant | agg t/s | decode agg t/s | prefill t/s | TTFT mean ms | TTFT max ms | wall s |
|---------|---------|-----------------|-------------|--------------|-------------|--------|
| default | `337.1` | `652.0` | `1516.1` | `7425.5` | `11735.7` | `24.299` |
| cap16 | `330.2` | `611.5` | `1559.6` | `7589.4` | `11407.9` | `24.802` |
| cap32 | `335.3` | `624.6` | `1572.4` | `6994.0` | `11315.5` | `24.429` |
| cap64 | `327.1` | `589.6` | `1596.9` | `7533.2` | `11141.5` | `25.025` |

Dense `n=128`, `ptok=168`, `gen=64`:

| variant | agg t/s | decode agg t/s | prefill t/s | TTFT mean ms | TTFT max ms | wall s |
|---------|---------|-----------------|-------------|--------------|-------------|--------|
| default | `141.4` | `360.6` | `650.8` | `22423.5` | `35209.6` | `57.925` |
| cap32 | `139.7` | `340.1` | `663.1` | `20346.5` | `34556.0` | `58.645` |
| cap64 | `136.3` | `333.4` | `645.2` | `22461.1` | `35511.7` | `60.081` |

Decision: reject capped defer as a parity lever. cap32 is the only interesting
MoE point, but it trades lower mean TTFT for lower aggregate throughput and
higher wall time. Dense caps also lose aggregate. Keep the cap as an opt-in A/B
knob only.

### Phase 58 waiting-threshold TTFT defer

Phase58 added `LLAMA_TTFT_PREFILL_FIRST_MIN_WAITING`, so TTFT prefill-first
only activates when the number of prompt-waiting slots is at or above a
threshold. Artifact:
`/home/mudler/bench/phase58_ttft_waiting_sweep/20260701_122052`.

Pre/post md5 and op gates stayed green: MoE
`8cb0ce23777bf55f92f63d0292c756b0`, dense
`5951a5b4d624ce891e22ab5fca9bc439`, `MUL_MAT` `1146/1146`, and `MUL_MAT_ID`
`806/806`.

MoE `n=128`, `ptok=128`, `gen=64`:

| variant | agg t/s | decode agg t/s | prefill t/s | TTFT mean ms | TTFT max ms | wall s |
|---------|---------|-----------------|-------------|--------------|-------------|--------|
| default | `339.0` | `648.4` | `1542.9` | `7743.1` | `11532.5` | `24.167` |
| min24 | `339.9` | `619.3` | `1637.0` | `7326.6` | `10868.8` | `24.095` |
| min32 | `341.9` | `635.0` | `1609.6` | `7420.1` | `11054.6` | `23.950` |
| min32+cap32 | `331.2` | `631.8` | `1512.1` | `7829.2` | `11767.1` | `24.733` |

Dense `n=128`, `ptok=168`, `gen=64`:

| variant | agg t/s | decode agg t/s | prefill t/s | TTFT mean ms | TTFT max ms | wall s |
|---------|---------|-----------------|-------------|--------------|-------------|--------|
| default | `140.3` | `362.7` | `639.8` | `21407.3` | `35811.6` | `58.399` |
| min24 | `140.4` | `347.6` | `658.7` | `22078.2` | `34783.3` | `58.353` |
| min32 | `139.7` | `350.2` | `650.1` | `21221.5` | `35246.3` | `58.642` |

Decision: `LLAMA_TTFT_PREFILL_FIRST_MIN_WAITING=32` is the best selective
scheduler A/B so far: MoE `n=128` improved aggregate, TTFT, and wall in the same
window, while dense `n=128` was roughly neutral but slightly worse on aggregate
and wall. Keep it opt-in until repeated and compared against matching vLLM h2h.

### Phase 59 MoE min32 repeat and vLLM H2H

Phase59 repeated the Phase58 MoE min32 point, then ran matching vLLM serving.
Artifact:
`/home/mudler/bench/phase59_moe_min32_repeat_vllm/20260701_123147`.

Pre/post llama md5 and op gates stayed green: MoE
`8cb0ce23777bf55f92f63d0292c756b0`, dense
`5951a5b4d624ce891e22ab5fca9bc439`, `MUL_MAT` `1146/1146`, and `MUL_MAT_ID`
`806/806`.

MoE `n=128`, `ptok=128`, `gen=64`:

| engine / variant | agg t/s | decode agg t/s | prefill t/s | TTFT mean ms | TTFT max ms | wall s |
|------------------|---------|-----------------|-------------|--------------|-------------|--------|
| llama default | `336.6` | `646.7` | `1525.1` | `7798.5` | `11666.8` | `24.334` |
| llama min32 | `336.9` | `632.0` | `1567.1` | `7167.8` | `11353.4` | `24.316` |
| vLLM | `601.3` | `938.8` | `3648.7` | `2968.1` | `4871.6` | `13.563` |

Decision: min32 repeated as a real llama.cpp scheduler QoS improvement
(`-8.1%` mean TTFT with flat aggregate and wall), but it is not a vLLM parity
lever. Llama min32 is still `0.560x` vLLM aggregate, `0.430x` vLLM prefill,
`0.673x` vLLM decode aggregate, and `2.415x` slower on mean TTFT. Keep the
scheduler knob opt-in and return parity work to the prefill / MoE compute gap.

Phase72 broadened that min32 result to the Phase70 serving shape. Artifact:
`/home/mudler/bench/phase72_ttft_min32_serving/20260701_160730`. Gates stayed
green, but min32 regressed every tested concurrency: aggregate ratios
`0.9302`/`0.9414`/`0.9699`, decode ratios `0.9442`/`0.9570`/`0.9775`, and TTFT
ratios `1.0379`/`1.0977`/`1.0300` at `n=8/32/128`. Keep min32 opt-in only and
do not default it on GB10.

### Phase 60 current W4A16 prefill profile

Phase60 re-profiled the current W4A16 grouped MoE prefill path after the
Phase1-5 W4A16 work. Artifact:
`/home/mudler/bench/phase60_w4a16_current_profile/20260701_104915`.

Pre/post md5 and op gates stayed green: MoE
`8cb0ce23777bf55f92f63d0292c756b0`, dense
`5951a5b4d624ce891e22ab5fca9bc439`, `MUL_MAT` `1146/1146`, and `MUL_MAT_ID`
`806/806`.

MoE prefill A/B (`npl=32`, `ntg=4`) still rejects W4A16 as an incremental
parity path:

| path | npp512 S_PP | npp2048 S_PP |
|------|-------------|--------------|
| default FP4-MMQ | `2327.69` | `2423.20` |
| forced W4A16 | `1451.00` | `1482.76` |

At `npp=512`, default MMQ spends `2.712s` (`39.2%`) in its main
`mul_mat_q<nvfp4,128>` bucket. Forced W4A16 spends `4.142s` (`42.5%`) in
`w4a16_grouped_kernel<32,128,1,4,2>`, plus `1.094s` (`11.2%`) in
`k_get_rows_float<float,float>` sorted activation gathers and `0.517s` (`5.3%`)
in `w4a16_cast_act_f32_bf16`.

Decision: do not add another W4A16 micro-patch. Cast elimination alone cannot
close a `37-39%` S_PP loss, and the dominant loss is the grouped kernel body
plus sorted activation movement. Future W4A16 parity work must be a larger
design that changes those structures, not another metadata/body shortcut.

### Phase 61 W4A16 direct activation kill-gate

Phase61 implemented the larger direct-activation experiment behind
`LLAMA_W4A16_DIRECT_A=1`, consuming original `src1` and `ids_to_sorted` directly
instead of materializing `src1_sorted` and then casting it to bf16. The correct
source addressing matched `get_rows_cuda`: `ids_to_sorted` is a flat source-row
index addressed with `nb11`. The initial token/slot decode failed `b=1` op
tests; the flat-row fix passed forced direct-A `MUL_MAT_ID` `806/806`.

Artifacts:

- default gates: `/home/mudler/bench/phase61_direct_default_gates/20260701_132057`
- A/B: `/home/mudler/bench/phase61_direct_ab/20260701_132237`

Gates:

- default MoE md5 `8cb0ce23777bf55f92f63d0292c756b0`
- default dense md5 `5951a5b4d624ce891e22ab5fca9bc439`
- `MUL_MAT` `1146/1146`
- `MUL_MAT_ID` `806/806`
- forced W4A16 and direct-A MoE transcripts both
  `07db32c2bcb78d17a43ed18bc22705cd`

MoE prefill A/B (`npl=32`, `ntg=4`):

| path | npp512 S_PP | npp2048 S_PP |
|------|-------------|--------------|
| default FP4-MMQ | `2325.45` | `2423.18` |
| forced W4A16 | `1471.05` | `1502.46` |
| forced W4A16 direct-A | `1566.30` | `1605.82` |

Decision: reject. Direct-A improved forced W4A16 by only `+6.5%` / `+6.9%` and
remained `0.67x` / `0.66x` of default FP4-MMQ, below the `+12%` and `0.75x`
keep gates. The direct kernel diff was saved to
`/tmp/phase61-w4a16-direct-a-rejected.diff` and not committed. W4A16 body
tuning is no longer the next GB10 parity lever.

Relevant files (all absolute): `/home/mudler/_git/LocalAI/.claude/worktrees/feat+paged-attention/backend/cpp/llama-cpp-localai-paged/docs/{DECODE_SERVING_SCOPE.md,PREFILL_GEMM_SCOPE.md,PREFILL_GEMM_RESULTS.md,TENSORCORE_GDN_SCOPE.md,final_benchmark.csv}`, `.../README.md`, `.../patches/paged/0034-feat-paged-native-NVFP4-W4A4-FP4-MMA-large-M-prefill.patch` (P1/P2), `.../patches/paged/0042-feat-paged-fused-residual-add-RMS-norm-weight-multip.patch` (P7), `.../patches/paged/0031` (P4), `0025` (D1), `0018/0022` (D4/D5), `0009/0010` (D3/D6/D7); graph source `/home/mudler/_git/LocalAI/backend/cpp/llama-cpp-paged-dev/src/{models/qwen35moe.cpp,models/delta-net-base.cpp,llama-graph.cpp}`.

### Phase 10 GDN C32 slab update

Phase 10 tested the tempting low-conflict shortcut for #101: keep the current
M5 tensor-core GDN form, raise the chunk to `C=32`, and split the value
dimension into two `dv_tile=64` slabs to stay within shared memory.

Result:

- The shortcut cannot be a launcher-only change. C32 requires staging
  `U=T*RHS` because the existing M5 apply path relies on one 16-row tile being
  held in registers before overwriting `Ud`.
- A default-off `GDN_C32_SLAB=1` candidate was built and md5-gated.
- The first candidate exposed a dense-only transcript failure on tail chunks;
  root cause was copying uninitialized staged rows for `t >= Cc` back into
  `Ud`. Zeroing those rows restored both canonical md5 gates:
  MoE `8cb0ce23777bf55f92f63d0292c756b0`, dense
  `5951a5b4d624ce891e22ab5fca9bc439`.
- Performance regressed after correctness was fixed:
  MoE 2048 S_PP `2430.32 -> 2054.86`; dense 2048 S_PP `1019.25 -> 903.73`.

Decision:

- **REJECT** the two-slab C32 M5 variant.
- Do not add it to the LocalAI patch stack.
- The likely blocker is duplicated A/T recomputation per value slab; future GDN
  work must share that work across slabs or move to a different FLA-style
  chunked design rather than repeating this env-gated shortcut.

Artifacts:

- `/home/mudler/bench/phase10_gdn_c32_slab/gates/`
- `/home/mudler/bench/phase10_gdn_c32_slab/ab/`
- `/home/mudler/bench/phase10_gdn_c32_slab/rejected/c32_slab_tailfix_rejected.diff`

### Phase 11 GDN M5 QS-early update

Phase 11 tested the smallest possible C=16 follow-up after the C32 slab
rejection: move the `QS = Qc * S0` state-boundary product earlier in the M5
chunk loop behind `GDN_M5_QS_EARLY=1`.

Result:

- The candidate built on DGX and stayed md5-exact:
  MoE `8cb0ce23777bf55f92f63d0292c756b0`, dense
  `5951a5b4d624ce891e22ab5fca9bc439`.
- It regressed S_PP slightly in both families:
  MoE 2048 `2441.54 -> 2420.26`, dense 2048 `1021.06 -> 1015.77`.

Decision:

- **REJECT** QS-early.
- Do not add it to the LocalAI patch stack.
- A scheduling-only move that still performs the same QS MMA does not close the
  GDN gap. The next GDN scope should be a real shared-A/Ai blocked-solve or
  global-scratch design, not another local reorder.

Artifacts:

- `/home/mudler/bench/phase11_gdn_m5_state_boundary/gates/`
- `/home/mudler/bench/phase11_gdn_m5_state_boundary/ab/`
- `/home/mudler/bench/phase11_gdn_m5_state_boundary/rejected/qs_early_rejected.diff`

### Phase 12 GDN shared-A/Ai cost-model update

Phase 12 scoped the next non-shortcut GDN path: compute f32 Ai once per
`(sequence, head, chunk)` and reuse it across two `dv_tile=64` value slabs.

Cost model:

- C16 full-width M5 uses `93,376 B` dynamic smem.
- C32 full-width would need `127,360 B`, which does not fit GB10.
- C32 slab64 fits at `94,592 B`, but Phase 10 showed it loses when A/T is
  recomputed per slab.
- For `BT=32`, f32 Ai scratch at `npp=2048,npl=32` is:
  - MoE H=32: `256 MiB`, with `768 MiB` total Ai write/read traffic.
  - Dense H=48: `384 MiB`, with `1152 MiB` total Ai write/read traffic.

Decision:

- **GO** to a default-off Phase 13 prototype, not a shipped patch.
- Scope: `GDN_GLOBAL_AI32=1`, `BT=32`, f32 Ai, two `dv_tile=64` slabs.
- Reject if same-session A/B is flat/slower. If rejected, stop GDN kernel work
  on GB10 rather than iterating into f16 Ai or more local reorders.

Docs:

- `backend/cpp/llama-cpp-localai-paged/docs/GDN_SHARED_AI_COST_MODEL.md`
- `docs/superpowers/specs/2026-07-01-gdn-global-ai-prototype-design.md`
- `docs/superpowers/plans/2026-07-01-gdn-global-ai-prototype-phase13.md`

### Phase 13 GDN Global-Ai32 update

Phase 13 implemented the Phase 12 prototype behind `GDN_GLOBAL_AI32=1`:
precompute f32 Ai once per chunk/head, then consume it from two C32
`dv_tile=64` value slabs.

Result:

- Correctness passed:
  MoE `8cb0ce23777bf55f92f63d0292c756b0`, dense
  `5951a5b4d624ce891e22ab5fca9bc439`.
- Performance regressed:
  - MoE 2048 S_PP `2425.10 -> 2097.76`.
  - Dense 2048 S_PP `1016.14 -> 918.19`.

Decision:

- **REJECT** Global-Ai32.
- Do not add `0055`.
- Stop GDN kernel work on GB10. The shortcut space is exhausted by Phase 10,
  Phase 11, and Phase 13 evidence; further GDN parity work needs a different
  hardware regime or a larger FLA/CuteDSL-class implementation outside this
  low-conflict LocalAI patch stack.

Artifacts:

- `/home/mudler/bench/phase13_gdn_global_ai32/gates/`
- `/home/mudler/bench/phase13_gdn_global_ai32/ab/`
- `/home/mudler/bench/phase13_gdn_global_ai32/rejected/global_ai32_rejected.diff`

### Phase 8 ragged MoE dispatch closure

The remaining Phase 8 source shortcut was closed without production CUDA edits.
The live ragged serving profile showed helper metadata buckets too small to clear
the `+5%` serving A/B gate (`mm_ids=0.66%`, `gather_mmq=0.42%`). Patch `0023`
already handles the broadcast-activation NVFP4 path by quantizing unique tokens
once and gathering FP4 blocks, so a metadata-only `LLAMA_MOE_FUSED_DISPATCH`
hook would add conflict surface without attacking the dominant buckets.

Safety rerun:

- `MUL_MAT_ID_RAGGED_MOE`: `6/6` on CUDA0.
- Full `MUL_MAT_ID`: `806/806` on CUDA0.
- MoE transcript md5: `8cb0ce23777bf55f92f63d0292c756b0`.
- Dense transcript md5: `5951a5b4d624ce891e22ab5fca9bc439`.

Decision:

- Keep test patch `0053`.
- Do not add a Phase 8 production patch unless it directly reduces
  `mmq_nvfp4` or activation movement without D2H id readback, new
  synchronizations, or md5 drift.

---

# PROFILE-VALIDATED PATH (both-engine nsys, adversarially verified Sun Jun 28 11:55:12 PM UTC 2026)

## Prefill gap decomposition (paged 396 vs vLLM 197 us/tok)
All 4 runs ran on DGX (GB10) via ssh dgx.casa; GPU lock held+released, GPU restored idle. Model = decision MoE Qwen3.6-35B-A3B-NVFP4 (paged GGUF vs q36-35b-a3b-nvfp4-vllm). Buckets = % of GPU-kernel wall (nsys cuda_gpu_kern_sum), and per-prefill-token us.

PAGED MoE PREFILL (npp512 ntg4 npl32, LLAMA_KV_PAGED=1 +LLAMA_MOE_FORCE_GRAPHS=1): S_PP=2417.8 t/s; kernel 6.485s/16384 tok = 395.9 us/tok. MoE-expert-GEMM(MMQ nvfp4) 26.5% | GDN 24.2% (gdn_core 17.2, gdn_gather 3.3, gdn_conv 2.7, l2norm 1.0) | layout-copy 9.8 (convert_dtype 6.3, concat 2.9) | ew-mul 8.7 | bf16-proj 8.6 | act-quant(quantize_mmq_nvfp4) 4.7 | ew-add 4.6 | silu/sigmoid-gate 4.3 | norms 3.6 | MoE-DISPATCH(argsort 0.4+mm_ids 1.1+gather_mmq 0.7) 2.2 | get_rows 1.0 | FA 0.6 | softmax 0.05 | scatter 0.06.

vLLM MoE PREFILL (32x512, 5 reps): S_PP=4925.8 t/s; kernel 16.138s/81920 tok = 197.0 us/tok. SURPRISE: on sm_121 vLLM runs experts as Marlin W4A16 (FP4->bf16 dequant + bf16 GEMM), NOT fused-FP4 cutlass; projections are FP8 (sm89_xmma_e4m3). ew-glue(torch elementwise) 31.7% | MoE-expert-GEMM(Marlin) 24.6 | GDN(FLA chunk_* + causal_conv) 18.5 | bf16/fp8-proj 10.4 | reduce(cumsum/softmax) 5.2 | gate 2.3 | act-quant(scaled_fp8) 1.7 | layernorm 1.7 | MoE-DISPATCH(gather/align/count_sort/argsort) 1.4 | FA 1.1.

Per-token gap decomposition (paged-vLLM, of 198.9 us/tok total): GDN +59.2 (~30%), MoE-GEMM +56.5 (~28%), ew/layout/glue net +21.4 (~11%), act-quant +15.2 (~8%), bf16-proj +13.7 (~7%), gate +12.4 (~6%), norms +11.1 (~6%), dispatch +5.9 (~3%).

## Decode picture (host-bound, not kernel/graph-reuse)
3 decode profiles. KEY: paged decode KERNELS are 5.4x more GPU-efficient than vLLM's, but paged static decode is HOST-BOUND (GPU ~16% busy); vLLM is GPU-bound (99% busy) on a slow recurrent GDN. They tie at static-wide-128 (paged 782 vs vLLM ~819 t/s pure decode) via opposite regimes.

PAGED DECODE-SERVING (staggered 128 clients, llama-server, steady 22s window, 83.5% GPU-busy): MoE/FFN-GEMM 40.7% (mmq 34.2 + gemv_moe 4.6 + gemv 1.4) | bf16-proj 22.8 (mul_mat_f 11.1 + nvjet 9.1 + cutlass 2.5) | GDN 21.2 (gdn_core 19.9) | act-quant 2.8 | layout 2.1 | get_rows 2.0 | ew-mul 2.0 | FA 1.6 | norms 1.2 | MoE-DISPATCH 1.1 | scatter 0.2 | softmax 0.1.

PAGED STATIC npl=128 lockstep (PP128+TG256, ~16% GPU-busy, HOST-BOUND): kernel 7.83s/49152 tok=159 us/tok, S_TG=782 t/s. MoE-GEMM 37.5 | GDN 21.6 | layout 9.6 | bf16-proj 9.2 | ew-mul 5.5 | act-quant 4.1 | ew-add 3.4 | norms 2.5 | dispatch 1.8 | FA 0.55. cudaStreamSynchronize=43.4s (84% of API/87% of wall) vs 7.83s GPU kernel => GPU idle ~84%.

PAGED STATIC npl=1 (batch-1): kernel 0.20s, MEMOPS 0.44s (68% of kern+mem), cudaStreamSync 66.7% => latency/BW-bound, GPU ~4% busy.

vLLM 128-wide offline (PT128 GEN256, 99% GPU-busy): kernel 42.56s/49152 tok=866 us/tok. GDN 45.2% (fused_recurrent_gated_delta decode 42.8!) | MoE-GEMM(Marlin) 36.2 | bf16/fp8-proj 6.6 | ew-glue 6.3 | FA 2.1 | reduce 1.4 | dispatch 0.7.

Per-token decode (paged static-128 | vLLM | ratio): MoE-GEMM 59.7|313.5 paged 5.3x faster; GDN 34.3|391.7 paged 11.4x faster; bf16-proj 14.7|57.2; total 159|866 paged 5.4x less GPU.

H1 verdict (false): the stated mechanism - 'MUL_MAT_ID per-useful-token time growing static->serving from grouped-GEMV collapse' - is REFUTED at the kernel level. The grouped path engages correctly: at width-1 the MoE expert path is GEMV (mul_mat_vec_q), and at width>=~16 it switches to grouped MMQ (mul_mat_q nvfp4) - npl=128 is 37% MMQ/~0 GEMV, serving is 34% MMQ + 6% gemv_moe. It does NOT collapse to per-token GEMV. What IS confirmed (the real H1 mechanism) is HOST-SIDE SERIALIZATION: cudaStreamSynchronize dominates the static-decode wall - npl=1 66.7% of API time (~89% of wall), npl=128 84.3% of API time (43.4s sync vs 7.83s GPU kernel => GPU ~84% idle); the serving window logged 40,902 cudaStreamSynchronize. The grouped MMQ also runs at ragged small-M tiles (mmq_x = 16/24/32/40/48/64/80/96) because tokens-per-expert is tiny -> low tensor-core utilization (small-M MMQ, not a GEMV collapse). Mechanistically the device->host sync to read MoE routing before launching per-expert GEMMs is the serializer (task D1/#104 'no host-sync MoE path').

THE BIG DECODE PICTURE (most important finding): paged and vLLM have OPPOSITE decode profiles. Paged decode kernels are 5.4x more GPU-efficient (159 vs 866 us/tok) but paged static decode is host-bound (GPU ~16% busy, serial SSM+sampling+MoE-dispatch host loop); vLLM is GPU-bound (99% busy) on a recurrent GDN kernel that is 11x slower per token, but it saturates the GPU via CUDA graphs. They tie at static-wide-128 (782 vs ~819 t/s). At SERVING the paged GPU rises to 83.5% busy because overlapping request streams hide the host stalls - so the serving lever for paged is NOT faster decode kernels (they're already fast/idle) but (a) removing host serialization / graphing the whole step incl MoE dispatch, and (b) chunked-prefill: paged's 2x-slower prefill steals serving cycles during continuous batching (the gen-80-128 serving config was ~55% prefill work; the nsys'd run2 gen-256-512 ~25%). vLLM bf16/fp8 projections are a bigger paged decode bucket than expected (22.8% serving) because batch-1/small-batch bf16 proj uses mul_mat_f (11.1%) + nvjet (9.1%).

Methodology/scope: profiled with nsys --trace=cuda + cuda_gpu_kern_sum; no NVTX in either engine so buckets are by kernel-name regex (bucketer at dgx:/home/mudler/bench/bucket2.py; reports at dgx:/home/mudler/bench/profgap/). Shared elementwise (k_bin_bcast add/mul, torch elementwise) straddle resid/MoE-fanin/GDN-glue and are bucketed by dominant use with that caveat; vLLM's torch_ew (31.7% prefill) is GDN-glue+MoE-combine+resid and is genuinely ambiguous. The dense Qwen3.6-27B-NVFP4 was NOT separately profiled (time budget; the MoE decision-model contains both MoE experts AND the same GDN/attention stack, fully answering A/B/C); GDN findings generalize to dense. vLLM decode here is offline 128-wide (continuous-batched), not staggered-server, so the cross-engine serving ratio is taken from prior h2h benches (~55-80% of vLLM at npl 64-128), not a fresh staggered vLLM run. Cross-engine 'gap' numbers are GPU-kernel-time per token (apples for GPU-bound prefill; for decode the host-bound vs GPU-bound asymmetry means wall-throughput parity hides a 5.4x GPU-efficiency paged advantage).

## Decision
### moe_prefill_lever
BETTER GROUPED GEMM KERNEL (D2/#105), NOT P5 dispatch fusion. The profile settles this empirically: explicit MoE dispatch (argsort+softmax+get_rows+set_rows+mm_ids+gather_mmq) is only 8.6 us/tok (~2-3% of the paged prefill wall; +5.9 us/tok = ~3% of the gap). P5 is REJECTED as a standalone lever - and the premise it rests on ("vLLM fuses dispatch into the GEMM epilogue") is FALSE on GB10: vLLM runs Marlin W4A16 with its OWN separate dispatch kernels (count_and_sort_expert_tokens/moe_align/vectorized_gather/moe_sum, 2.7 us/tok). Dispatch is cheap in both engines; epilogue-fusing it buys ~3% at most.

The real lever is the grouped GEMM: paged grouped-MMQ MUL_MAT_ID is 105 us/tok vs vLLM Marlin 48.5 us/tok = 2.16x slower, ~28% of the prefill gap (+56.5 us/tok). It does NOT collapse to GEMV - the grouped path engages correctly; it loses because ragged small-M-per-expert tiles (mmq_x 16-96) under-utilize tensor cores.

Is it winnable given MMQ already beat our native kernel? YES in principle, but ONLY via a kernel approach we have NOT yet tried correctly. Both prior attempts failed for identifiable reasons: 0033 did dequant as a SEPARATE global-memory pass then cuBLAS (lost to fused FP4 MMQ 29-49%); 0034 native FP4-MMA W4A4 PoC did NOT hold in-backend. vLLM proves the winning shape on THIS EXACT silicon (sm_121, Marlin bf16 fallback - no native FP4) is IN-REGISTER FP4->bf16 dequant feeding bf16 mma.sync with cp.async pipelining + large/grouped tiles, and W4A16 means ZERO activation-quant. That second point is load-bearing: act-quant (quantize_mmq_nvfp4) is +15.2 us/tok = ~8% of the gap that vLLM STRUCTURALLY does not pay because it is W4A16. So a Marlin-style W4A16 grouped MoE-prefill GEMM is a combined ~36% prefill lever (GEMM 28% + act-quant 8%), and it is a DIFFERENT kernel from both rejects (not a separate-pass dequant, not native FP4-MMA). The README's "W4A16 rejected" verdict was DECODE-only (BW-bound, wash); prefill is compute-bound and the act-quant pass is M-proportional, so W4A16 for prefill is unaudited and the most promising structural fix. GATE: must beat MMQ in a SEPARATELY-BUILT in-backend A/B at the real ragged-small-M MoE-prefill shapes (NOT a standalone PoC - the exact lesson from rejecting native FP4-MMA); bit-exact via KL-gate for the bf16-dequant reduction-order change (paged-MoE 8cb0ce23 precedent).

### gdn_build_go
True

### gdn_rationale
GO on #101, with a Phase-1 in-backend kill-gate. The profile makes the regime check the scope doc demanded (TENSORCORE_GDN_SCOPE Phase 0) pass cleanly: (1) GDN is the #1 SINGLE contributor to the prefill gap at +59.2 us/tok (~30% of the gap), edging out MoE-GEMM (+56.5). (2) The cost is MATH-predominant, not layout/host: gdn_core (the hand-written FP32 chunked-scan, NOT tensor-core) is 17.2% of the wall; GDN-attributable layout (gdn_gather 3.3 + head-concat 2.9 + a convert_dtype slice) is only ~6-7% (~1/4). So tensor cores attack the dominant 3/4, and the 1/4 layout folds into the same fused kernel. (3) The headroom is MEASURED on identical silicon: vLLM's FLA chunked GDN runs the SAME math at 36.5 us/tok vs paged 95.7 = 2.62x, confirming the scope's "mma absorbs the O(C^2) intra-chunk flops so the Cx state-BW cut becomes a net win" mechanism. (4) Bonus dual payoff: it also chips the decode serial-SSM residual and, via continuous batching, the serving-decode lever (prefill steals ~25-55% of serving cycles).

CONDITION (empirical guard, not PoC-optimism): 0031's chunking math was correct yet came back 22% SLOWER in-backend, and we JUST rejected native FP4-MMA because its standalone PoC win did not hold in-backend. So GO funds Phase 1 ONLY (two Gram products on mma.cuh tf32 tiles at fixed C=16/1-block-SM); it must move S_PP in a SEPARATELY-BUILT in-backend A/B vs the sequential scan. If Phase 1 is flat, the occupancy/register wall is the blocker, not the reductions - NO-GO the multi-week Phase 2/3 build. Precision gate is the KL-gate (tf32 default, 3xtf32 ladder), greedy md5 stability, plus the adversarial g in [-20,-1e-4] decay op case; ship opt-in default-off until a separately-built A/B beats sequential.

### top_decode_lever
D1/#104 - the no-host-sync MoE decode path + full-step CUDA-graph capture (graph the WHOLE decode step INCLUDING MoE dispatch), targeting the device->host MoE-routing readback. Ranked decisively by the profile, NOT by raw GPU-bucket size: the dominant decode cost is not a GPU kernel at all - it is cudaStreamSynchronize, 84% of the static-decode wall (43.4s sync vs 7.83s GPU kernel; npl=1 66.7%, npl=128 84.3% of API time; 40,902 syncs in the serving window). Root cause = the device->host sync to read MoE routing before launching per-expert GEMMs. Paged decode KERNELS are already 5.4x more GPU-efficient than vLLM's and the GPU sits 84% idle in static decode, so D1 is the only decode lever that attacks the actual bottleneck.

D2/D3/D4 for DECODE are all REJECTED by the methodology's "a faster kernel off the critical path benches flat" rule: D2 fused MoE decode GEMM - paged MoE-GEMM is already 5.3x faster/token than vLLM (59.7 vs 313.5 us/tok); making it faster just adds idle. D3 FA-split - FA is 1.6% of decode-serving wall / 0.55% static (H2 refuted; the hybrid is mostly GDN with few full-attn layers); not a lever. D4 GDN-width-adaptive - paged GDN decode is already 11.4x faster/token than vLLM (34 vs 392); H3 confirmed (flat across width, no amortization) but the recurrence is NOT the bottleneck, host serialization is - an occupancy retune yields ~nothing until the host loop is gone.

Honest scope on D1's payoff: at HIGH-concurrency serving the paged GPU is already 83.5% busy because overlapping request streams hide the host stalls, so D1's win concentrates at LOW-concurrency / latency / batch-1 (GPU 4-16% busy), where it is large. The complementary serving-throughput lever is FIXING PREFILL (GDN #101 + MoE GEMM D2/#105): paged's 2x-slower prefill steals serving cycles under continuous batching (~25-55% of the serving step is prefill work) - so the prefill levers ARE also serving-decode levers. GATE: separately-built in-backend A/B (compiled-in, so a runtime flag does NOT isolate it) showing higher static/low-concurrency decode t/s with no high-concurrency-serving regression; bit-exact greedy md5 (graph replay re-issues identical kernels).

### next_3_levers

Post-Phase71 supersession: this ranked list is historical. `0047` already
ships the M5 tensor-core GDN path default-on under paged KV, Phase71
revalidated it against sequential-disabled and serial-chunked baselines, and
Phase10/11/13 rejected the smaller follow-up GDN reorders. Phase41/43 closed
D1 on the current GB10 path unless a fresh route trace proves a host-sync
fallback returned. Phase60/61/66 rejected another small W4A16/direct-A or
quant/gather pass. Treat the list below as pre-Phase60 planning context, not an
active queue.

Ranked, each with its pass-gate:

1) #101 TENSOR-CORE mma CHUNKED GDN PREFILL KERNEL (prefill, GO). #1 prefill-gap contributor (+59 us/tok, ~30%), ~3/4 math (tensor cores help) with 2.62x measured headroom on identical silicon, 1/4 layout folds in; also helps serving decode. GATE: Phase-0 regime already satisfied by this profile; Phase-1 two-Gram-product PoC must move S_PP in a SEPARATELY-BUILT in-backend A/B vs sequential (flat => NO-GO the multi-week build); then KL-gate (tf32/3xtf32) + greedy md5 + adversarial-decay op test; ship opt-in default-off until A/B beats sequential.

2) D1/#104 NO-HOST-SYNC MoE DECODE PATH + FULL-STEP CUDA-GRAPH CAPTURE (decode). Attacks the cudaStreamSynchronize that is 84% of the static-decode wall (the MoE-routing device->host readback). Lowest effort, bit-exact, highest-confidence decode win (concentrated at low-concurrency/latency). GATE: separately-built in-backend A/B (not a runtime-flag toggle) - higher static/low-concurrency decode t/s, no high-concurrency-serving regression; bit-exact greedy md5.

3) D2/#105 MARLIN-STYLE W4A16 GROUPED MoE PREFILL GEMM (prefill). In-register FP4->bf16 dequant + bf16 mma.sync, cp.async, large grouped tiles - captures the 28% MoE-GEMM gap AND the 8% act-quant gap (W4A16 has no activation-quant), = ~36% combined; this is exactly what vLLM does on sm_121. Ranked #3 because of HIGH risk: two prior in-backend GEMM attempts failed (0033 separate-pass dequant, 0034 native FP4-MMA PoC didn't hold). GATE: must beat MMQ in a SEPARATELY-BUILT in-backend A/B at ragged-small-M MoE-prefill shapes (NOT a standalone PoC); bit-exact via KL-gate (bf16-dequant reduction order).

Explicitly REJECTED/deprioritized (record so they aren't re-run): P5 dispatch fusion (~3%, and the "vLLM fuses dispatch" premise is false on GB10); D2-for-decode, D3 FA-split, D4 GDN-width-adaptive (their kernels are already 5-11x faster than vLLM and GPU-idle -> bench flat); padded/fixed-slot decode (already tested+rejected, commit b028c81e).

### notes
Empirical discipline applied throughout (per the just-rejected native FP4-MMA): every funded lever is gated on a SEPARATELY-BUILT in-backend A/B, never a standalone PoC - 0031 (chunking math correct, -22% in-backend) and 0034 (PoC win, didn't hold) are the two cautionary precedents. Two compiled-in levers (#101, D1) cannot be isolated by a runtime flag, so they need build-vs-build A/B (methodology hard rule).

Two profile surprises that reshape the directions: (a) vLLM on sm_121 is NOT native FP4 - it runs Marlin W4A16 (FP4->bf16 in-register dequant + bf16 GEMM) for experts and FP8 projections. So the winnable MoE-prefill GEMM is a W4A16-Marlin-style kernel (which also erases our 8% act-quant tax), not another native-FP4 attempt. (b) Decode is a regime asymmetry, not a kernel gap: paged decode kernels are 5.4x more GPU-efficient than vLLM's but paged static decode is HOST-BOUND (GPU 84% idle on cudaStreamSynchronize); vLLM is GPU-bound at 99% on a recurrence 11x slower/token. They tie at static-wide-128. Hence "make decode kernels faster" is the wrong instinct (benches flat); "remove host serialization / graph the full step" (D1) and "fix prefill so it stops stealing serving cycles" (#101, D2) are the decode-serving levers.

Cross-cutting: the prefill levers (#101 GDN, D2 MoE GEMM) double as serving-decode levers because continuous batching interleaves ~25-55% prefill work into the serving step. GDN edges MoE-GEMM as the top prefill pick (bigger gap, cleaner math mechanism, 2.6x proven headroom, lower in-backend risk, dual payoff).

All numbers from the both-engine nsys profile (cuda_gpu_kern_sum buckets, bucketer dgx:/home/mudler/bench/bucket2.py, reports dgx:/home/mudler/bench/profgap/); caveats: no NVTX (kernel-name regex buckets); shared elementwise straddles resid/MoE-fanin/GDN-glue; vLLM decode is offline 128-wide, not staggered-server. Relevant repo paths (absolute): /home/mudler/_git/LocalAI/.claude/worktrees/feat+paged-attention/backend/cpp/llama-cpp-localai-paged/docs/{TENSORCORE_GDN_SCOPE.md,TENSORCORE_GDN_BUILD_PLAN.md,VLLM_PARITY_LEVER_MAP.md,PREFILL_GEMM_SCOPE.md,PREFILL_GEMM_RESULTS.md,DECODE_SERVING_SCOPE.md,PAGED_BITEXACT_NOTE.md,final_benchmark.csv}; patches dir .../patches/paged/ (existing 0031 chunked-GDN serial, 0033 dequant->cuBLAS rejected, 0034 native FP4-MMA, 0040/0041 S1/S3 decode-graph, 0042 fused residual+RMSNorm); methodology /home/mudler/_git/LocalAI/.claude/worktrees/feat+paged-attention/.agents/vllm-parity-methodology.md.
