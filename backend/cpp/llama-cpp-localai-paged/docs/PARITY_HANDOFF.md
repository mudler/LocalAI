# PARITY_HANDOFF: how to pick up the GB10 vLLM-parity work

> 2026-07-02 forward direction: the active plan is now
> [`EXECUTION_REARCH_SCOPE.md`](EXECUTION_REARCH_SCOPE.md), which reframes the
> per-lever "hardware floor" verdict as *ggml-execution-architecture-conditional*
> (same-silicon 2-3x is software) and scopes an additive, phased (P1 bf16-native
> stream, P2 expert-major fused MoE region, P3 Marlin large-M retry on top of
> P1+P2, P4 token-budget scheduler, P5 blocked-solve GDN, P6 fp8 KV) program with
> a falsifiable P0 kill-gate per phase. The port-forensics finding is that the
> failed single-kernel/single-boundary A/Bs below failed on *integration tax*
> (dropped into a materialize-every-node executor), not because the kernels are
> GB10-hostile; the reject log below is the evidence that grounds those verdicts.
> Read the scope doc first for what to build next.
>
> 2026-06-30 update: this handoff is now historical procedure, not the active
> verdict. The GB10 investigation was reopened in `GB10_PARITY_REOPEN_SPEC.md`
> and `GB10_PARITY_PHASE0_RESULTS.md`, with Phase 6 serving-nsys evidence and
> the active follow-up plans under `docs/superpowers/plans/`. Use those files for
> the current state before relying on the older "closed" conclusion below.
>
> 2026-07-01 Phase112 update: keep the new default-off
> `LLAMA_W4A16_DIRECT_A=1` direct activation staging hook, especially combined
> with Phase110 `LLAMA_MOE_GPU_SORT=1`. Artifact:
> `/home/mudler/bench/phase112_w4a16_direct_a/20260701_231749_direct_a`.
> Selected gates passed `13/13` for W4A16+GPU-sort, direct-A, and
> direct-A+GPU-sort. Direct-A+GPU-sort improved the 257-token W4A16 fallback
> rows versus W4A16+GPU-sort control (`MOE_SWIGLU_DOWN 1551.08 -> 1477.74 us`,
> `MUL_MAT_ID_RAGGED_MOE 2278.50 -> 2166.22 us`) but was neutral/slightly
> slower on 128-token rows. Canonical README md5 gates are green: MoE
> `8cb0ce23777bf55f92f63d0292c756b0`, dense
> `5951a5b4d624ce891e22ab5fca9bc439`; compact supported op gates are green
> (`SSM_CONV 45/45`, `SSM_CONV_SPLIT 6/6`, `GET_ROWS 49/49`,
> `GATED_DELTA_NET 48/48`, `MUL_MAT 1146/1146`, `MUL_MAT_ID 806/806`).
> This is still default-off structural groundwork, not parity: W4A16 fallback
> remains slower than the default grouped-MMQ path. Use the patch-series README
> md5 command as canonical; the handoff `-no-cnv -c 4096` snippet produced
> stable but non-canonical md5s for both candidate and control.
>
> 2026-07-01 Phase113 update: reject the combined direct-A GPU-tile descriptor
> attempt. Artifact:
> `/home/mudler/bench/phase113_w4a16_direct_a_gpu_tiles/20260701_233345_no_readback`.
> The candidate (`LLAMA_W4A16_GPU_TILES=1` on top of Phase112 direct-A+GPU-sort)
> avoided the `n_tiles` readback by launching over zero-initialized `max_tiles`
> and returning early on `rows <= 0`. Selected correctness passed `13/13`, but
> perf failed the keep gate: `MOE_SWIGLU_DOWN n=257` was flat
> (`1478.16 -> 1476.36 us`) and `MUL_MAT_ID_RAGGED_MOE n=257` regressed
> (`2148.44 -> 2214.23 us`). The source was reverted and post-revert
> Phase112 direct-A+GPU-sort selected gates passed `13/13`. Next W4A16/MoE work
> should not revisit compact GPU tile descriptors; use vLLM-style padded routing
> metadata (`sorted_token_ids`, expert ids per M block, padded row count) if
> continuing this line.
>
> 2026-07-01 Phase114 update: reject the naive padded routing implementation.
> It implemented the vLLM-style metadata contract with separate padded source
> ids and destination ids for llama.cpp, plus an expert-id W4A16 consumer mode
> and a direct scatter that skipped compact `get_rows_cuda`. Correctness passed
> (`13/13`) but perf failed: after a fix using `num_tokens_post_pad` early
> returns, `MOE_SWIGLU_DOWN n=257` regressed `1477.88 -> 1726.27 us` and
> `MUL_MAT_ID_RAGGED_MOE n=257` regressed `2163.35 -> 2650.93 us`. Artifacts:
> `/home/mudler/bench/phase114_w4a16_padded_routing/20260701_234634_padded_meta`
> and
> `/home/mudler/bench/phase114_w4a16_padded_routing/20260701_235003_padded_meta_fix1`.
> Source was reverted; post-revert Phase112 direct-A+GPU-sort selected gate
> passed `13/13`. Padded metadata is not enough by itself on GB10 because sparse
> expert occupancy makes padded activation/output traffic too expensive.
>
> 2026-07-02 Phase115 update: reject another small-M/tile-policy shortcut.
> Phase115 re-tested the existing default-off `LLAMA_MOE_SMALL_M_TILE=16/32/64`
> knob on the newer Phase108 whole-graph MoE sentinels. Artifact:
> `/home/mudler/bench/phase115_moe_small_m_sentinel/20260702_020258`.
> Control and all three tile caps passed selected correctness (`13/13` each),
> but no candidate met the promotion rule. The 257-token ragged down row
> regressed for every cap (`1452.30 us` control vs `1455.02`, `1458.71`, and
> `1456.88 us`). Do not add name-based down special cases or another MMQ
> tile-policy patch. The next credible target is a true fused routed-MoE kernel
> or a graph-level fusion that removes materialized activation/output traffic.
>
> 2026-07-02 Phase116 update: reject the standalone graph-level
> SwiGLU-to-MMQ-activation-quant fusion. The default-off candidate
> `LLAMA_MOE_SWIGLU_DOWN_FUSED_QUANT=1` detected the plain
> `GLU -> down MUL_MAT_ID` pattern and computed `silu(gate) * up` directly into
> the grouped-MMQ NVFP4 activation buffer. Artifact:
> `/home/mudler/bench/phase116_moe_swiglu_down_fused_quant/20260702_022611`.
> Correctness passed (`13/13`) and the fix1 route emitted the fused marker
> (`6` hits), but perf was not useful: `MOE_SWIGLU_DOWN n=257` was flat
> (`1024.90 -> 1024.69 us`), `n=128` regressed (`806.33 -> 808.79 us`), and the
> ragged sentinel drifted slower. Source was reverted and post-revert selected
> gate passed `13/13`. Do not retry this narrow fused-quant route; the next
> fused-MoE attempt must remove a larger boundary, such as route-once metadata
> shared by both expert GEMMs plus fused GEMM1/activation/GEMM2 or
> weighted-combine/scatter.
>
> 2026-07-02 Phase117 update: keep the default-off MoE boundary trace as
> diagnostic instrumentation only. Artifact:
> `/home/mudler/bench/phase117_moe_route_once_boundary/20260702_024140`.
> The trace decomposes `MOE_SWIGLU_DOWN` into route-sort, activation
> quantization, grouped-MMQ launch, GLU, and graph-pattern records under
> `LLAMA_MOE_BOUNDARY_TRACE=1`; optional timing is gated by
> `LLAMA_MOE_BOUNDARY_TIMING=1`. Inline CUDA event timing initially aborted
> under CUDA graph capture, so the guarded trace emits `us=-1` while capturing
> and only produces real event timings with `GGML_CUDA_DISABLE_GRAPHS=1`.
> Post-guard selected gates passed (`13/13`), trace mode passed (`7/7`), and
> canonical gates passed: MoE md5 `8cb0ce23`, dense md5 `5951a5b4`,
> `MUL_MAT 1146/1146`, `MUL_MAT_ID 806/806`. The timing attribution does not
> fund another local route-sort, tile, GLU, or activation-quant shortcut. The
> next MoE source phase should own a larger pipeline boundary: shared
> route-once metadata across gate_up/down and/or whole-pattern
> GEMM1->activation->GEMM2 execution.
>
> 2026-07-02 Phase118 update: reject standalone route-metadata caching.
> Artifact:
> `/home/mudler/bench/phase118_moe_route_cache/20260702_030549`. The
> default-off candidate `LLAMA_MOE_ROUTE_CACHE=1` stored ids-derived grouped-MMQ
> route metadata in context-owned buffers and reused it within a graph
> evaluation. It was correctness-clean (`13/13` default, opt-in, and
> post-reject) and the trace showed reuse (`23` hits, `3` misses on
> `MOE_SWIGLU_DOWN n=128`), but perf was too small: `MOE_SWIGLU_DOWN n=257`
> improved only `1017.711 -> 1011.915 us` (`+0.57%`) and `n=128` regressed
> `799.360 -> 803.738 us` (`-0.55%`). Runtime cache source was reverted; only a
> local `ggml_cuda_mmq_ids_meta` helper refactor remains as low-conflict
> groundwork. Do not retry metadata-cache-only work. The next attempt must own
> more of the vLLM-style pipeline: GEMM1->activation->GEMM2 and/or
> scatter/combine, not just skipping one `mm_ids_helper` launch.
>
> 2026-07-02 Phase119 update: keep the default-off whole-pattern contract trace
> after fix1. Initial artifact:
> `/home/mudler/bench/phase119_moe_whole_pattern_contract/20260702_034729`;
> fix1 artifact:
> `/home/mudler/bench/phase119_moe_whole_pattern_contract/20260702_035126_fix1`.
> The initial trace proved coverage but missed the overhead rule on
> `MOE_SWIGLU_DOWN n=257` (`1015.070 -> 1028.937 us`, `-1.35%`). Fix1 moved
> detector work off the default path unless `LLAMA_MOE_WHOLE_PATTERN_TRACE` or
> the existing boundary trace is enabled. Fix1 gates are green: selected
> `MOE_SWIGLU_DOWN,MUL_MAT_ID_RAGGED_MOE` `13/13`, trace `MOE_SWIGLU_DOWN`
> `7/7`, canonical MoE md5 `8cb0ce23`, dense md5 `5951a5b4`,
> `MUL_MAT 1146/1146`, and `MUL_MAT_ID 806/806`. Trace overhead is now within
> rule (`MOE_SWIGLU_DOWN n=128` `805.400 -> 805.584 us`, `-0.02%`;
> `n=257` `1019.715 -> 1021.836 us`, `-0.21%`) and emits supported NVFP4
> markers for both `n_tokens=128` and `257`. This is diagnostic scaffolding,
> not a runtime optimization. The next executor attempt should match at the
> earlier `gate_up MUL_MAT_ID` node and skip through `VIEW, VIEW, GLU, down
> MUL_MAT_ID`; the current `GLU -> down` hook is validation-only because GEMM1
> has already executed.
>
> 2026-07-02 Phase120 update: keep the default-off early whole-pattern matcher
> after fix2. Initial artifact:
> `/home/mudler/bench/phase120_moe_early_whole_pattern/20260702_040153`;
> fix2 artifact:
> `/home/mudler/bench/phase120_moe_early_whole_pattern/20260702_040725_fix2`.
> The initial/fix1 versions proved `skip_ready=4` but emitted noisy unsupported
> markers from unrelated `MUL_MAT_ID` candidates. Fix2 emits only the actual
> early pattern and is clean: selected `MOE_SWIGLU_DOWN,MUL_MAT_ID_RAGGED_MOE`
> `13/13`, early trace `MOE_SWIGLU_DOWN` `7/7`, canonical MoE md5
> `8cb0ce23`, dense md5 `5951a5b4`, `MUL_MAT 1146/1146`, and
> `MUL_MAT_ID 806/806`. It emits exactly six supported early markers for the
> perf sentinels, covering `n_tokens=128` and `257`, with `skip_ready=4`,
> `ids_match=1`, and `swiglu=1`. Trace overhead is within rule
> (`MOE_SWIGLU_DOWN n=128` `803.937 -> 808.978 us`, `-0.62%`;
> `n=257` `1020.412 -> 1026.073 us`, `-0.55%`). The next source phase can now
> implement a guarded executor at this early matcher. First prove safe
> ownership/skip accounting for the five-node sequence, then move route-plan
> reuse and activation/down execution into the helper.
>
> 2026-07-02 Phase121 update: keep the default-off executor proof after fix1.
> Initial artifact:
> `/home/mudler/bench/phase121_moe_whole_pattern_exec_proof/20260702_041543`;
> fix1 artifact:
> `/home/mudler/bench/phase121_moe_whole_pattern_exec_proof/20260702_041739_fix1`.
> The initial run passed correctness but emitted zero exec markers because the
> exec branch was accidentally nested under the early-trace env condition.
> Fix1 makes `LLAMA_MOE_WHOLE_PATTERN_EXEC=1` engage independently. Gates are
> green: selected `MOE_SWIGLU_DOWN,MUL_MAT_ID_RAGGED_MOE` `13/13`, exec
> `MOE_SWIGLU_DOWN` `7/7`, canonical MoE md5 `8cb0ce23`, dense md5
> `5951a5b4`, `MUL_MAT 1146/1146`, and `MUL_MAT_ID 806/806`. Exec perf emits
> six `skip=4` markers covering `n_tokens=128` and `257`, and target perf is
> neutral (`MOE_SWIGLU_DOWN n=128` `807.772 -> 806.051 us`, `+0.21%`;
> `n=257` `1021.115 -> 1020.839 us`, `+0.03%`). This proves ownership and skip
> accounting only; it is not a fused-MoE speedup. The next source phase should
> replace one internal boundary inside this helper, preferably route-plan reuse
> or activation in route-slot order, with the same md5/op gates.
>
> 2026-07-02 Phase122 update: reject route-only metadata reuse inside the
> Phase121 executor. Artifact:
> `/home/mudler/bench/phase122_moe_shared_route_meta/20260702_043212`.
> The candidate exposed `ggml_cuda_mmq_ids_meta` as a public MMQ helper and
> used `LLAMA_MOE_WHOLE_PATTERN_SHARED_ROUTE=1` to build route metadata once
> for both `gate_up` and `down`. Correctness passed (`13/13` selected and
> `7/7` shared-route), but perf missed the keep gate:
> `MOE_SWIGLU_DOWN n=128` regressed `808.190 -> 811.836 us` and `n=257`
> regressed `1020.850 -> 1051.666 us` versus the Phase121 executor. Source was
> reverted, including the public metadata API and shared-route env. Post-reject
> gates on the reverted tree passed (`13/13` selected and `7/7` Phase121 exec)
> with six retained exec markers. Do not retry route-only metadata reuse. The
> next MoE executor scope should target activation/down data layout, direct
> activation-to-down input, or a larger GEMM1->activation->GEMM2 fused boundary.
>
> 2026-07-02 Phase123 update: reject standalone fused-down activation
> quantization inside the Phase121 executor. Artifact:
> `/home/mudler/bench/phase123_moe_executor_fused_down_input/20260702_025811`;
> red check:
> `/home/mudler/bench/phase123_moe_executor_fused_down_input/red_20260702_025031`.
> The candidate used `LLAMA_MOE_WHOLE_PATTERN_FUSED_DOWN=1` to run `gate_up`,
> compute `silu(gate) * up` directly into the sorted NVFP4 down MMQ activation
> buffer, and launch the existing down MMQ kernel. Correctness passed
> (`13/13` selected, `7/7` fused-down, six fused markers), but perf was flat:
> versus Phase121 exec, `MOE_SWIGLU_DOWN n=128` was
> `811.153 -> 810.618 us` and `n=257` was `1023.090 -> 1023.657 us`.
> Source was reverted, including the fused-down env, MMQ helper, and NVFP4
> fused quant kernel. Post-reject gates passed (`13/13` selected, `7/7`
> Phase121 exec, six exec markers). Do not retry a single-boundary
> SwiGLU-to-down-quant shortcut; if continuing MoE source work, scope a full
> expert-major packed pipeline that owns `GEMM1->activation->GEMM2`, or pivot to
> another measured bottleneck.
>
> 2026-07-02 Phase124 update: current-stack graph-node serving was refreshed
> after the Phase122/123 rejections. Artifact:
> `/home/mudler/bench/phase124_current_moe_profile/20260702_031205`.
> Pre/post gates are green: MoE md5 `8cb0ce23`, dense md5 `5951a5b4`,
> `MUL_MAT 1146/1146`, `MUL_MAT_ID 806/806`. At `N=128`, prompt `128`,
> generation `64`, serving under graph-node profiling was
> `agg_tps 206.2`, `decode_agg_tps 320.3`, `prefill_tps 1536.4`, wall
> `39.738s`. Fine buckets are now `mmq_nvfp4 6074.78 ms` (`30.17%`) and
> `gdn_core 5888.31 ms` (`29.25%`), with `act_quant` only `674.88 ms`
> (`3.35%`). This explains why single-boundary activation/quant attempts were
> flat. The next source work must reduce one of the two dominant buckets:
> either a full expert-major MoE pipeline for `mmq_nvfp4`, or a default-off GDN
> decode/core experiment for `gdn_core`. Do not spend more GB10 time on
> route-only metadata reuse, fused-down quantization, or MoE tile-policy knobs
> unless a new profile makes those buckets material.
>
> 2026-07-02 Phase125 scoping update: two independent code explorers and a
> local GDN audit challenged the Phase124 fork in the road. The chosen next
> source attempt is the MoE side, but only as a first maintainable slice:
> implement a default-off MMQ sorted-output primitive behind
> `LLAMA_MOE_EXPERT_MAJOR_SORTED_OUT=1`, immediately unsort as a proof, and
> measure `MOE_SWIGLU_DOWN` before attempting the full
> `gate_up -> SWIGLU -> down` expert-major executor. Rationale: vLLM's portable
> advantage is keeping activations expert-major across both GEMMs and
> unpermuting once; Phase122/123 failed because they only touched route metadata
> or one activation boundary. Do not copy CUTLASS/FlashInfer pointer-array, TMA,
> or FP4 scale-swizzle internals. A small GDN patch is not funded by current
> evidence because previous decode/core micro-attempts already rejected the
> obvious geometry/store/broadcast/conv-state shortcuts. Plan:
> `docs/superpowers/plans/2026-07-02-moe-expert-major-sorted-output-phase125.md`.
>
> 2026-07-02 Phase125 result: reject the MMQ sorted-output plus immediate
> unsort proof. Artifact:
> `/home/mudler/bench/phase125_moe_expert_major_sorted_output/20260702_033931`;
> post-reject:
> `/home/mudler/bench/phase125_moe_expert_major_sorted_output/post_reject_20260702_034232`.
> The candidate was default-off and correctness-clean (`13/13` default
> selected, `7/7` opt-in `MOE_SWIGLU_DOWN`, 12 sorted markers), but perf failed
> decisively: versus Phase121 exec, `MOE_SWIGLU_DOWN n=128` regressed
> `805.16 -> 888.76 us` and `n=257` regressed `1023.83 -> 1192.05 us`.
> Source was reverted. Post-reject gates are green: selected `13/13`, Phase121
> exec `7/7` with six markers, MoE md5 `8cb0ce23`, dense md5 `5951a5b4`,
> `MUL_MAT 1146/1146`, and `MUL_MAT_ID 806/806`. Do not retry a path that adds
> a sorted-output temporary and immediately unsorts. A future expert-major MoE
> attempt must keep sorted activations through the down GEMM and unpermute only
> once after the full FFN, or pivot to a larger GDN recurrence design.

> 2026-07-02 Phase126 result: keep the grouped-MMQ presorted helper scaffold.
> The patch only touches `mmq.cu`/`mmq.cuh`, refactors the current MoE id path
> behind explicit `ids_src1`/`ids_dst`/`expert_bounds` metadata, and exposes a
> `src1_sorted` entry point for the future whole-MoE executor. Fixed artifact:
> `/home/mudler/bench/phase126_mmq_presorted_helper/fix1_20260702_040858`.
> Gates were green: selected `13/13`, MoE md5
> `8cb0ce23777bf55f92f63d0292c756b0`, dense md5
> `5951a5b4d624ce891e22ab5fca9bc439`, `MUL_MAT 1146/1146`,
> `MUL_MAT_ID 806/806`. Focused perf was neutral:
> `MOE_SWIGLU_DOWN n=128 805.99 us`, `MUL_MAT_ID_RAGGED_MOE n=128
> 1243.85 us`, `MOE_SWIGLU_DOWN n=257 1018.74 us`,
> `MUL_MAT_ID_RAGGED_MOE n=257 1452.84 us`. This is not a parity win by
> itself; it is the dependency for Phase127 to keep `gate_up -> SWIGLU -> down`
> in expert-major order and unpermute only once after the full FFN.

> 2026-07-02 Phase127 result: reject and revert the whole-MoE expert-major
> executor built on the Phase126 helper. Red:
> `/home/mudler/bench/phase127_moe_whole_expert_major/red_20260702_042125`
> passed by fallback with zero markers. Candidate green:
> `/home/mudler/bench/phase127_moe_whole_expert_major/green2_20260702_042916`
> passed default selected `13/13` and opt-in `MOE_SWIGLU_DOWN 7/7`, emitting
> six `LLAMA_MOE_WHOLE_EXPERT_MAJOR` markers after fixing the down-weight shape
> interpretation (`down_w` is `[n_ff, n_embd, experts]`). Perf artifact:
> `/home/mudler/bench/phase127_moe_whole_expert_major/perf_20260702_043104`.
> It failed the keep rule: `MOE_SWIGLU_DOWN n=128` regressed
> `802.57 -> 812.14 us`; `n=257` regressed `1023.25 -> 1039.36 us`;
> ragged standalone was essentially flat. Source was reverted. Post-reject:
> `/home/mudler/bench/phase127_moe_whole_expert_major/post_reject_20260702_043318`
> passed selected `13/13`, MoE md5 `8cb0ce23`, dense md5 `5951a5b4`,
> `MUL_MAT 1146/1146`, and `MUL_MAT_ID 806/806`. Do not retry the same
> fake-tensor whole-executor shape; the next MoE attempt must remove more
> temporary traffic or become a real fused grouped MMQ/SWIGLU/down path. A
> separate alternative is the previously scoped Qwen3Next BF16 GDN S-cache
> experiment, but that needs non-md5 numerical gates.

> 2026-07-02 Phase128 result: reject/revert the Qwen3Next BF16 GDN S-cache
> selector probe for the current target. Artifact:
> `/home/mudler/bench/phase128_qwen3next_gdn_bf16_s_cache/default_20260702_043939`
> built and passed default gates (`GATED_DELTA_NET 48/48`, canonical MoE md5
> `8cb0ce23`, dense md5 `5951a5b4`, `MUL_MAT`, `MUL_MAT_ID`). Verbose smoke
> artifact:
> `/home/mudler/bench/phase128_qwen3next_gdn_bf16_s_cache/smoke3_20260702_044434`
> showed the active decision model is `qwen35moe`, not Qwen3Next, and S cache
> remained `f32` under `LLAMA_QWEN3NEXT_GDN_S_CACHE_TYPE=bf16`. No true
> Qwen3Next GGUF was found on DGX. The relevant Qwen35/Qwen35MoE BF16 S-cache
> lever was already Phase81/82: it cut `gdn_core` but changed MoE md5 and
> missed the full f16-reference KL acceptance band. Do not retry this exact
> lever unless the quality gate is explicitly re-scoped or a real Qwen3Next
> model artifact is available.

> 2026-07-02 Phase129 result: reject/revert the Qwen35/Qwen35MoE grouped Q/K
> broadcast probe for fused GDN. Plan:
> `docs/superpowers/plans/2026-07-02-qwen35-gdn-qk-grouped-bcast-phase129.md`.
> The candidate added a default-off `LLAMA_QWEN35_GDN_QK_BCAST=1` branch in
> `src/models/qwen35.cpp` and `src/models/qwen35moe.cpp`, reusing the existing
> Qwen3Next `ggml_gated_delta_net_set_bcast()` path. Default gates were green:
> `/home/mudler/bench/phase129_qwen35_gdn_qk_bcast/default_20260702_065445`
> passed MoE md5 `8cb0ce23`, dense md5 `5951a5b4`, `GATED_DELTA_NET 46/46`,
> `MUL_MAT 1146/1146`, and `MUL_MAT_ID 806/806`. A standalone opt-in gate
> artifact at `optin_20260702_065604` was invalid because
> `paged-inference-gates.sh` only passes completion env through `EXTRA_ENV`.
> The valid opt-in pre-gate from
> `/home/mudler/bench/phase129_qwen35_gdn_qk_bcast/decode_optin_20260702_070149/gate_pre`
> changed MoE md5 to `b773e2f032aa0e992626d486b321808e`, so profiling was
> stopped and the source was reverted. Post-reject:
> `/home/mudler/bench/phase129_qwen35_gdn_qk_bcast/post_reject_20260702_070258`
> passed canonical MoE/dense md5, `GATED_DELTA_NET 46/46`, `MUL_MAT 1146/1146`,
> and `MUL_MAT_ID 806/806`; rebuilt `libllama.so` has zero
> `LLAMA_QWEN35_GDN_QK_BCAST` strings. Do not retry this Qwen3Next
> grouped-broadcast port for Qwen35/Qwen35MoE under the current bit-exact md5
> rule.

> 2026-07-02 Phase130 result: current-stack graph-node serving profile refresh,
> measurement-only. Artifact:
> `/home/mudler/bench/phase130_current_stack_profile/20260702_070949`. Shape:
> MoE `q36-35b-a3b-nvfp4`, `N=128`, prompt `128`, generation `64`,
> `PARALLEL=128`, `CTX=131072`. Pre/post gates passed canonical MoE md5
> `8cb0ce23`, dense md5 `5951a5b4`, `MUL_MAT 1146/1146`, and
> `MUL_MAT_ID 806/806`. Serving metrics: `agg_tps 208.0`,
> `decode_agg_tps 326.9`, `prefill_tps 1519.6`, `TTFT mean 8170.6 ms`, wall
> `39.38 s`, total kernel time `20.1559 s`. The profile confirms the live
> bottleneck remains split between `mmq_nvfp4 6009.52 ms` (`29.82%`) and
> `gdn_core 5891.40 ms` (`29.23%`). FA/mask cleanup is not funded:
> `get_rows 280.62 ms` (`1.39%`) and `fa 257.38 ms` (`1.28%`). The next source
> attempt must target a larger MoE/FFN-GEMM executor/kernel or a materially
> different GDN recurrent-state/packed-decode design, not another paged-mask,
> route-only, activation-only, grouped-broadcast, BF16-cache, or launch-geometry
> shortcut.

> 2026-07-02 Phase131 result: source-selection challenge, no source changes.
> Plan:
> `docs/superpowers/plans/2026-07-02-fused-routed-ffn-phase131.md`. Two
> read-only explorers challenged the Phase130 fork. MoE/FFN-GEMM source work is
> not funded unless it becomes a real fused routed-FFN kernel/executor; another
> route-only, activation-only, W4A16, tile-policy, sorted-output, or fake
> executor patch is expected to repeat Phases 110-127. GDN source work is not
> funded unless it materially reduces f32 recurrent-state traffic without
> BF16/quality drift; launch geometry, gather/identity, producer/store fusion,
> BF16 S-cache, and grouped Q/K broadcast have already failed or changed md5s.
> The next active line is to audit vLLM's fused MoE design and llama.cpp's
> current whole-pattern executor hook for a default-off fused routed-FFN PoC.
> If that audit does not produce a concrete low-conflict hook, require a
> standalone CUDA PoC before touching llama.cpp source.
>
> 2026-07-02 Phase132 result: keep the new default-off routed-FFN PoC scaffold.
> Plan:
> `docs/superpowers/plans/2026-07-02-routed-ffn-poc-phase132.md`. Artifact:
> `/home/mudler/bench/phase132_routed_ffn_poc/20260702_072725`. Source adds
> `ggml/src/ggml-cuda/moe-ffn.cu/.cuh` and a narrow hook in
> `ggml/src/ggml-cuda/ggml-cuda.cu` behind `LLAMA_MOE_ROUTED_FFN_POC=1`.
> The helper currently executes the baseline `gate_up -> SWIGLU -> down`
> sequence through the existing whole-pattern hook, so it is a scaffold, not a
> parity speedup. Initial incremental build failed until CMake was reconfigured
> to pick up the new globbed CUDA source; after `cmake -S . -B build`, build
> passed. Selected default and opt-in gates passed
> `MOE_SWIGLU_DOWN,MUL_MAT_ID_RAGGED_MOE 13/13`; opt-in emitted six exec
> markers and `libggml-cuda.so` contains one `LLAMA_MOE_ROUTED_FFN_POC` string.
> Default and opt-in canonical gates passed MoE md5 `8cb0ce23`, dense md5
> `5951a5b4`, `GATED_DELTA_NET 48/48`, `MUL_MAT 1146/1146`, and
> `MUL_MAT_ID 806/806`. Focused perf was neutral (`808.32 -> 804.87 us` at
> n=128, `1023.36 -> 1022.71 us` at n=257). Next phase may replace the helper
> internals with a real fused routed-FFN slice; do not claim Phase132 itself as
> a speedup.
>
> 2026-07-02 Phase133 result: keep only as a default-off structural base, not a
> speedup. Plan:
> `docs/superpowers/plans/2026-07-02-routed-ffn-sorted-down-phase133.md`.
> Artifact:
> `/home/mudler/bench/phase133_routed_ffn_sorted_down/20260702_074651`.
> Source exposes `ggml_cuda_mmq_ids_meta`, adds raw
> `ggml_cuda_mul_mat_q_moe_sorted_f32(...)`, and adds
> `LLAMA_MOE_ROUTED_FFN_SORTED_DOWN=1` on top of
> `LLAMA_MOE_ROUTED_FFN_POC=1`. The path executes baseline `gate_up` and
> `SWIGLU`, gathers the SWIGLU output into compact expert-sorted F32 rows, then
> calls raw MMQ down without fake tensors. Selected default, Phase132, and
> Phase133 gates passed `13/13`; Phase133 trace proved six
> `mmq_moe_sorted_raw` launches. Default and Phase133 canonical gates passed
> MoE md5 `8cb0ce23`, dense md5 `5951a5b4`, `GATED_DELTA_NET 48/48`,
> `MUL_MAT 1146/1146`, and `MUL_MAT_ID 806/806`. Perf was not a win:
> default `807.37/1020.76 us`, Phase132 `808.21/1018.87 us`, Phase133
> `808.85/1026.87 us` for `n=128/257`. Next phase must fuse SWIGLU-to-sorted
> or SWIGLU-to-quant to remove this added gather/quant boundary; do not promote
> sorted-down as-is.
>
> 2026-07-02 Phase134 result: keep only as default-off fused-SWIGLU structural
> base, not a speedup. Plan:
> `docs/superpowers/plans/2026-07-02-routed-ffn-fused-swiglu-phase134.md`.
> Artifact:
> `/home/mudler/bench/phase134_routed_ffn_fused_swiglu/20260702_075828`.
> Source adds `LLAMA_MOE_ROUTED_FFN_FUSED_SWIGLU=1` on top of
> `LLAMA_MOE_ROUTED_FFN_POC=1`, passes `gate/up` views into the routed-FFN
> helper, computes `silu(gate) * up` directly into expert-sorted F32 rows, and
> calls the raw sorted-F32 down MMQ helper. The fused flag now implies the
> sorted-down path; `LLAMA_MOE_ROUTED_FFN_SORTED_DOWN=1` is not required.
> Selected opt-in gates passed `13/13`; trace proved six `mmq_moe_sorted_raw`
> launches; canonical opt-in gates passed MoE md5 `8cb0ce23`, dense md5
> `5951a5b4`, `GATED_DELTA_NET 48/48`, `MUL_MAT 1146/1146`, and
> `MUL_MAT_ID 806/806`. Perf is mixed: default `804.92/1026.02 us`, Phase132
> `808.00/1028.43 us`, Phase133 `808.07/1029.02 us`, Phase134
> `810.61/1025.68 us` for `n=128/257`. It recovers n=257 but regresses n=128;
> next work must fuse SWIGLU directly into down-MMQ quant or remove another
> launch/buffer before this becomes a parity lever.
>
> 2026-07-02 Phase135 result: keep as current best default-off routed-FFN base,
> but not parity. Plan:
> `docs/superpowers/plans/2026-07-02-routed-ffn-fused-quant-phase135.md`.
> Focused artifact:
> `/home/mudler/bench/phase135_routed_ffn_fused_quant/20260702_081723`.
> Serving artifact:
> `/home/mudler/bench/phase135_routed_ffn_fused_quant_serving/20260702_082102`.
> Source adds `LLAMA_MOE_ROUTED_FFN_FUSED_QUANT=1` on top of
> `LLAMA_MOE_ROUTED_FFN_POC=1`, computes `silu(gate) * up` directly into the
> NVFP4 MMQ activation layout, and launches raw down MMQ via
> `ggml_cuda_mul_mat_q_moe_quantized(...)`. Focused selected gates passed
> `13/13`; trace proved six `mmq_moe_quantized_raw` launches and zero
> `mmq_moe_sorted_raw` launches; canonical focused gates passed MoE md5
> `8cb0ce23`, dense md5 `5951a5b4`, `GATED_DELTA_NET 48/48`,
> `MUL_MAT 1146/1146`, and `MUL_MAT_ID 806/806`. Focused perf:
> default `805.92/1031.06 us`, Phase134 `807.65/1027.51 us`, Phase135
> `807.92/1024.97 us` for `n=128/257`. Serving at the Phase130 shape passed
> pre/post gates and improved decode aggregate t/s `326.9 -> 332.7`, while
> `mmq_nvfp4` dropped `6009.52 -> 5915.24 ms`; aggregate stayed `208.0`, prefill
> worsened `1519.6 -> 1475.1`, and total kernel time rose slightly
> `20.1559 -> 20.2498 s`. Next work should target remaining MoE overhead after
> fused quant (`mmq_fixup`, route/writeback, weighted combine), not another F32
> intermediate.
>
> 2026-07-02 Phase136 result: reject and revert the separate post-down
> weighted-combine fuse. Plan:
> `docs/superpowers/plans/2026-07-02-routed-ffn-combine-phase136.md`.
> Focused artifact:
> `/home/mudler/bench/phase136_routed_ffn_combine/20260702_083727`.
> Serving artifact:
> `/home/mudler/bench/phase136_routed_ffn_combine_serving/20260702_085749`.
> The candidate added `LLAMA_MOE_ROUTED_FFN_COMBINE=1` on top of Phase135 and
> skipped the post-down `MUL(weights) -> VIEW* -> ADD*` tail with a separate
> F32 weighted-combine kernel. It was correctness-clean: expanded selected
> gates passed `20/20`, trace proved six combine markers plus six
> `mmq_moe_quantized_raw` launches and zero sorted launches, canonical gates
> passed MoE md5 `8cb0ce23`, dense md5 `5951a5b4`, `GATED_DELTA_NET 46/46`,
> `MUL_MAT 1146/1146`, and `MUL_MAT_ID 806/806`. Focused full-tail perf
> improved (`MOE_SWIGLU_COMBINE n=257` `428.53 -> 401.81 us` versus Phase135),
> but serving regressed versus Phase135: aggregate/decode t/s
> `208.0/332.7 -> 206.5/323.2`. Source and the sentinel test were reverted;
> post-reject Phase135 selected gates passed `13/13`. Do not retry a standalone
> post-MMQ combine launch as the next parity lever; any combine/finalize work
> needs a larger serving-visible fused writeback/finalize design.
>
> 2026-07-02 Phase137 result: reject the GDN launch-geometry retune with no
> source changes. Plan:
> `docs/superpowers/plans/2026-07-02-gdn-geometry-sweep-phase137.md`.
> Focused artifact:
> `/home/mudler/bench/phase137_gdn_geometry_sweep/20260702_091441`.
> Serving artifact:
> `/home/mudler/bench/phase137_gdn_geometry_serving/20260702_091740`.
> The env-only sweep tested existing `GDN_NW`/`GDN_CPW` knobs. The best focused
> candidate, `GDN_NW=4 GDN_CPW=1`, improved 1-token GDN rows
> (`hc=32,hs=128,kda=0` `6.793748 -> 4.713682 us`, KDA
> `7.790557 -> 5.194275 us`, grouped broadcast `5.967364 -> 3.407998 us`),
> but real serving regressed versus Phase135 despite clean pre/post gates:
> MoE md5 `8cb0ce23`, dense md5 `5951a5b4`, `MUL_MAT 1146/1146`, and
> `MUL_MAT_ID 806/806`. Aggregate/decode t/s moved
> `208.0/332.7 -> 206.2/324.9`, total kernel time rose
> `20.2498 -> 20.7530 s`, and `gdn_core` worsened
> `5926.55 -> 6466.27 ms`. Do not promote or source-code a GDN geometry retune
> for this target. The next scoped source line is default-off MoE
> finalize/writeback inside the existing down-MMQ path, not a standalone
> post-MMQ combine launch.
>
> 2026-07-02 Phase138 attempt 1 update: keep the default-off finalize trace and
> full-tail sentinel scaffold; no runtime speedup claim yet. Plan:
> `docs/superpowers/plans/2026-07-02-moe-down-mmq-finalize-phase138.md`.
> Artifacts:
> `/home/mudler/bench/phase138_moe_down_mmq_finalize_trace/20260702_092943`
> (`MOE_SWIGLU_DOWN` trace-only),
> `/home/mudler/bench/phase138_moe_down_mmq_finalize_trace/20260702_093617_full_tail`
> (new full-tail sentinel), and
> `/home/mudler/bench/phase138_moe_down_mmq_finalize_trace/20260702_093731_canonical`
> (canonical gates). The old `MOE_SWIGLU_DOWN` sentinel emitted six early
> routed-FFN records but no weighted tail. The new `MOE_SWIGLU_FINALIZE`
> sentinel passed default and Phase135-opt-in correctness (`7/7` each) and
> emitted six supported tail records with `tail_nodes=16`, `views=8`, and
> `adds=7`. Canonical patched-Phase93 gates passed MoE md5 `8cb0ce23`, dense
> md5 `5951a5b4`, `MUL_MAT 1146/1146`, and `MUL_MAT_ID 806/806`. Next work may
> implement default-off down-MMQ finalize/writeback against this sentinel first;
> keep serving promotion gated by Phase135 decode/aggregate/kernel-time
> thresholds.
>
> 2026-07-02 Phase138 attempt 2 update: keep the default-off down-MMQ
> finalize/writeback candidate as a narrow positive, but do not promote it or
> call parity. Plan:
> `docs/superpowers/plans/2026-07-02-moe-down-mmq-finalize-phase138.md`.
> Focused artifact:
> `/home/mudler/bench/phase138_moe_down_mmq_finalize/20260702_095927_focused`;
> canonical gates:
> `/home/mudler/bench/phase138_moe_down_mmq_finalize/20260702_100202_canonical`;
> serving:
> `/home/mudler/bench/phase138_moe_down_mmq_finalize_serving/20260702_100330`.
> The candidate adds `LLAMA_MOE_ROUTED_FFN_FINALIZE_POC=1` on top of Phase135,
> zeroes the final output, atomically accumulates `down_sum * router_weight`
> from the down-MMQ path, and skips the strict weighted tail only after the
> finalize helper is selected. Focused `MOE_SWIGLU_FINALIZE` correctness passed
> for default, Phase135, and Phase138 (`7/7` each); canonical and serving
> pre/post gates passed MoE md5 `8cb0ce23`, dense md5 `5951a5b4`,
> `MUL_MAT 1146/1146`, and `MUL_MAT_ID 806/806`. Serving versus Phase135 moved
> aggregate/decode t/s `208.0/332.7 -> 209.3/333.5`, total kernel time
> `20.2498 -> 20.0489 s`, and `mmq_nvfp4 5915.24 -> 5802.87 ms`; however
> `ew_add` remains visible at `374.09 ms`, so this is only an incremental
> default-off improvement. Next work should reduce the remaining fan-in/writeback
> path more deeply or return to the dominant `gdn_core`/`mmq_nvfp4` buckets.
>
> 2026-07-02 Phase139 result: serving noise-floor repeat rejects treating the
> Phase138 one-off serving gain as source-funding evidence. Spec:
> `docs/superpowers/specs/2026-07-02-serving-noise-floor-phase139-design.md`.
> Plan:
> `docs/superpowers/plans/2026-07-02-serving-noise-floor-phase139.md`.
> Artifact:
> `/home/mudler/bench/phase139_serving_noise_floor/20260702_081901`.
> Seven identical current-binary Phase138 serving/profile runs all passed
> pre/post gates: MoE md5 `8cb0ce23`, dense md5 `5951a5b4`,
> `MUL_MAT 1146/1146`, and `MUL_MAT_ID 806/806`. The runtime variance was much
> larger than Phase138's one-off delta: aggregate throughput median
> `208.5 t/s`, stdev `2.8022`, CV `1.349%`, range `203.4..212.3`; wall CV
> `1.347%`; `mmq_nvfp4` CV `3.351%`. Keep Phase138 default-off as
> correctness-clean and focused-positive, but do not stack another
> finalize/MMQ micro-patch from serving evidence alone. Future serving claims
> need repeated A/B medians and must exceed `max(2.0%, 3 * same-binary stdev)`.
> The next source phase should pivot to a larger measured bucket, with GDN
> packed decode/prep now more defensible than another MoE finalize shortcut.
>
> 2026-07-02 Phase140 result: reject an immediate in-GDN Q/K
> L2-normalization patch. Spec:
> `docs/superpowers/specs/2026-07-02-gdn-decode-prep-trace-phase140-design.md`.
> Plan:
> `docs/superpowers/plans/2026-07-02-gdn-decode-prep-trace-phase140.md`.
> Artifact:
> `/home/mudler/bench/phase140_gdn_decode_prep_trace/20260702_085348`.
> The current Phase138 opt-in serving/profile shape passed pre/post gates:
> MoE md5 `8cb0ce23`, dense md5 `5951a5b4`, `MUL_MAT 1146/1146`, and
> `MUL_MAT_ID 806/806`. Serving/profile reported aggregate/decode throughput
> `207.3/328.9 t/s`, wall `39.501 s`, total kernel `20.2002 s`, `GDN
> 6673.66 ms`, `gdn_core 5890.44 ms`, and `gdn_l2norm 100.30 ms`. The focused
> SQLite summary had `l2_norm_f32 100.3024 ms` versus
> `gated_delta_net_cuda 5804.7074 ms`. This is above the absolute
> three-sigma floor from Phase139 (`53.433 ms`) but below the planned `3%` of
> GDN-core materiality threshold at about `1.7%`, so prep-only L2 fusion is not
> source-funded. Next GDN work should be recurrence-level, packed-state, or
> datacenter-Blackwell-specific, not another prep micro-fusion.
>
> 2026-07-02 Phase141 result: decode-only GDN source claims must normalize by
> launch count or tightly control the capture window. Spec:
> `docs/superpowers/specs/2026-07-02-gdn-decode-noise-floor-phase141-design.md`.
> Plan:
> `docs/superpowers/plans/2026-07-02-gdn-decode-noise-floor-phase141.md`.
> Artifact:
> `/home/mudler/bench/phase141_gdn_decode_noise_floor/20260702_090428`.
> Five identical current-binary decode-only captures all passed pre/post gates:
> MoE md5 `8cb0ce23`, dense md5 `5951a5b4`, `MUL_MAT 1146/1146`, and
> `MUL_MAT_ID 806/806`. Raw `gdn_core_ms` median/stdev/CV was
> `1415.500/30.641/2.146%`, range `1410.300..1482.140 ms`, but launch counts
> drifted (`597`, `598`, `600`, `630`). Normalized `gdn_core_ms_per_launch`
> was stable: median/stdev/CV `2.359167/0.005399/0.229%`. Future GDN A/B
> source claims need repeated medians and must beat either `6.49%` raw
> `gdn_core` reduction or `2.0%` launch-normalized reduction. The small
> default-off source follow-up now worth testing is scalar gate/beta hoisting
> inside `gated_delta_net_cuda`; vLLM-style packed decode recurrence remains a
> larger redesign.

Audience: an agent with **zero prior context** who has been told to "continue the GB10 vLLM-parity investigation" on the `llama-cpp-localai-paged` backend.

This file is the **operational how-to**. It is the companion to `VLLM_PARITY_FINAL.md`, which is the **why / authoritative record** ("never re-litigate"). If the two ever disagree on a *fact*, `VLLM_PARITY_FINAL.md` and the bench artifacts it cites win; this file wins on *procedure* (how to ssh, lock, build, bench, profile).

Read order for a cold start:
1. This file (TL;DR + hard gates + quickstart).
2. `VLLM_PARITY_FINAL.md` (the closed record, every number cites its artifact).
3. `.agents/vllm-parity-methodology.md` (the methodology: bit-exact gating, profile-don't-assume, both-engine ground truth).
4. The patch-series `README.md` (~44 KB, canonical backend doc) and `PAGED_BITEXACT_NOTE.md`.

---

## 1. TL;DR STATE

> 2026-07-01 Phase104-108 update: the current carried source line is still the
> Phase93 Qwen3Next grouped Q/K broadcast plus the Phase101/102 default-off
> cleanup candidates. Phase104/106 same-session serving showed the stack is
> md5/op clean but still far from vLLM: at `N=128`, paged/vLLM was about
> `0.66` on decode and `0.50-0.51` on aggregate; at `N=192/256`, vLLM remained
> faster and TTFT stayed about `3x` lower. Phase105 refreshed the grouped-MMQ
> trace and found no new host-side tile-policy lever. Phase107 proved the MoE
> structural correctness gates exist (`MOE_SWIGLU_DOWN 7/7`,
> `MOE_WEIGHTED_COMBINE 7/7`, `MUL_MAT_ID_RAGGED_MOE 6/6`) but also proved
> `test-backend-ops perf` did not time those custom whole-graph cases. Phase108
> fixed that measurement gap in `tests/test-backend-ops.cpp`: perf mode now
> includes those MoE cases at `n_tokens=128,257`, and CSV output includes
> `time_us`, `flops`, `memory_kb`, and `n_runs`. The Phase108 artifact is
> `/home/mudler/bench/phase108_moe_perf_csv/20260701_221559`; md5s and compact
> op gates are green. Use Phase108 rows as the baseline for any fused routed-MoE
> implementation. Current ranking: `MUL_MAT_ID_RAGGED_MOE` is `1239-1446 us/run`,
> `MOE_SWIGLU_DOWN` is `802-1020 us/run`, and `MOE_WEIGHTED_COMBINE` is only
> `28-68 us/run`, so do not spend the next patch on weighted-combine fusion
> alone.
> Phase109 then tested existing env-gated routes on the Phase108 rows:
> `LLAMA_W4A16_PREFILL_M=128`, `LLAMA_FP4_PREFILL_M=128`,
> `LLAMA_MOE_DENSITY_MAX=9`, and `LLAMA_MOE_MMQ_X=64`
> (`/home/mudler/bench/phase109_existing_moe_prefill_ab/20260701_222559`).
> All selected correctness gates passed (`13/13` per env), but W4A16 and FP4
> large-M regressed the 257-token rows badly, and density/tile retuning was
> noise-level on `MUL_MAT_ID_RAGGED_MOE` while not helping `MOE_SWIGLU_DOWN`.
> Do not spend another phase on MMQ tile-policy shortcuts. The next credible
> implementation is structural: port the vLLM-style idea of GPU-side
> token/expert routing metadata (`sorted_token_ids`, expert offsets/bounds,
> inverse permutation) into llama.cpp's `mul_mat_id` host-sync fallback/grouped
> W4A16 path, while leaving the graph-safe grouped-MMQ path untouched.
> Phase110 implemented the first slice of that structural path as default-off
> `LLAMA_MOE_GPU_SORT=1` in `ggml_cuda_mul_mat_id`, reusing the existing
> `mm_ids_helper` GPU sort for fallback metadata. The initial branch failed
> `3/13` selected opt-in rows because `mm_ids_helper` returns sorted-to-original
> `ids_dst`, while fallback `get_rows_cuda()` needs original-to-sorted
> `ids_from_sorted`; adding a tiny inverse-permutation kernel fixed correctness.
> Accepted artifact:
> `/home/mudler/bench/phase110_gpu_moe_sort/20260701_224446_fix1`. Gates are
> green: canonical MoE md5 `8cb0ce23777bf55f92f63d0292c756b0`, dense md5
> `5951a5b4d624ce891e22ab5fca9bc439`, and supported compact ops
> `SSM_CONV 45/45`, `SSM_CONV_SPLIT 6/6`, `GET_ROWS 49/49`,
> `GATED_DELTA_NET 48/48`, `MUL_MAT 1146/1146`, `MUL_MAT_ID 806/806` for both
> default and `LLAMA_W4A16_PREFILL_M=128 LLAMA_MOE_GPU_SORT=1`. Perf decision:
> keep as a default-off structural base only. It improves W4A16 fallback
> 257-token rows by `7.2%` (`MOE_SWIGLU_DOWN`) and `7.9%`
> (`MUL_MAT_ID_RAGGED_MOE`), but the opt-in fallback is still about `1.5x`
> slower than default grouped-MMQ. Phase111 must remove another fallback
> bottleneck, such as the remaining `expert_bounds` host copy / host tile
> descriptor build, before this line can matter for parity.
> Phase111 tested that narrow follow-up as default-off `LLAMA_W4A16_GPU_TILES=1`:
> W4A16 tile descriptors were built on GPU from `expert_bounds_dev` with an
> atomic tile counter. It was correctness-clean after fixing a pointer mutability
> compile error and a CUDA pool LIFO allocation bug, but clean perf was
> flat-to-negative (`MUL_MAT_ID_RAGGED_MOE n=257` regressed about `2.0%` versus
> Phase110 GPU-sort). Artifact:
> `/home/mudler/bench/phase111_w4a16_gpu_tiles/20260701_230400_fix1`. The
> Phase111 source was reverted, and post-revert W4A16+GPU-sort selected gates
> passed `13/13`. Do not reopen a standalone GPU tile descriptor cleanup; the
> next W4A16 attempt must remove a larger boundary, such as direct activation
> consumption plus GPU descriptors together, or bypass the host-sync fallback
> path entirely.
>
> 2026-07-01 active update: Phase50-59 reopened the dense and MoE serving
> scheduler question.
> True dense decode is much closer to vLLM (`383.66` vs `435.00` t/s, `88.2%`)
> than the Phase47 h2h aggregate suggested, while traced serving still shows
> no pure decode-only steps and high TTFT. Phase53 rejected static lower
> admission budgets; Phase54 histograms show prompt admission concentrated in a
> few large chunks (`prompt_hist=513+:12`) with mostly full-width decode
> (`decode_hist=128-255:53`). Phase55 implemented that targeted
> first-token A/B as `LLAMA_TTFT_PREFILL_FIRST=1`: on dense `n=128` it improved
> aggregate throughput `138.2 -> 142.9`, mean TTFT `23231.9 -> 21520.8 ms`, and
> wall `59.272 -> 57.323 s`, with md5/op gates green. Phase56 then showed the
> policy helps dense `n=32` but regresses MoE `n=128` mean TTFT
> `7168.1 -> 7615.3 ms` and aggregate `341.1 -> 339.9`; keep it opt-in only and
> do not default it broadly. Phase57 tried a per-step defer cap; cap32 improved
> MoE mean TTFT in one same-window sweep but still lost aggregate and wall, and
> dense caps lost aggregate. Phase58 added a prompt-backlog threshold; min32
> improved MoE `n=128` aggregate `339.0 -> 341.9`, mean TTFT
> `7743.1 -> 7420.1 ms`, and wall `24.167 -> 23.950 s` in the same window, while
> dense `n=128` was mixed. Phase59 repeated MoE min32: aggregate stayed flat
> (`336.6 -> 336.9`), mean TTFT improved (`7798.5 -> 7167.8 ms`), and wall stayed
> flat (`24.334 -> 24.316 s`) with md5/op gates green. Matching vLLM was still
> far ahead (`601.3` aggregate, `2968.1 ms` mean TTFT), so min32 is an opt-in
> llama.cpp QoS knob, not a parity-closing lever. The trace and scheduler commits
> are local and DGX-gated but not pushed, so the LocalAI patch series has not
> been regenerated.
>
> 2026-07-01 Phase81-85 update: the next viable GDN lever is no longer launch
> shape or gather removal. A default-off Qwen35/Qwen35MoE BF16 persistent
> recurrent S-cache experiment (`LLAMA_QWEN35_GDN_S_CACHE_TYPE=bf16`) cut
> same-source decode-only `gdn_core` from `1399.30 ms / 599 launches`
> (`2.34 ms/launch`) to `863.57 ms / 720 launches` (`1.20 ms/launch`). Default
> F32 md5 gates and op gates stayed green, and BF16 dense md5 stayed canonical,
> but BF16 MoE md5 changed to `07db32c2bcb78d17a43ed18bc22705cd`. A quick
> MoE KL smoke vs the same-source F32 base showed KLD `0.055499 +/- 0.001705`,
> same-top-p `88.361%`, and PPL ratio `1.010356`. Phase82 then ran the full MoE
> f16-reference gate at
> `/home/mudler/bench/phase82_bf16_s_cache_f16_kl/20260701_183016`: same-source
> F32 measured KLD `0.136563 +/- 0.003242`, while BF16 S-cache measured
> `0.137162 +/- 0.003456` against the documented paged acceptance reference
> `0.136000 +/- 0.003285`. Reject promotion and do not run serving A/B for this
> candidate under the current hard KL rule. Phase83 then tested a bit-exact
> KDA `expf(g)` register-cache shortcut in the GDN CUDA core. Md5 and op gates
> stayed green, but same-window decode-only `gdn_core` moved
> `1399.46 -> 1405.62 ms`, so reject that micro-optimization too. Phase84
> reduced in-place GDN op outputs to attention-only tensors and moved the CPU
> ids fallback scratch to workspace; md5/op gates stayed green and startup free
> CUDA memory improved `117472 -> 117855 MiB`, but same-window decode-only
> `gdn_core` moved `1399.72 -> 1407.38 ms`. Treat Phase84 as a possible
> memory-footprint cleanup only, not a speed parity lever. Phase85 added a
> graph-reuse-safe identity-contiguous recurrent-state fast path: it calls
> `ggml_gated_delta_net_inplace` on a direct state view when `s_copy_main` is
> identity, otherwise keeps the ids path. Md5/op gates stayed green, the
> `gdn_gather` fine bucket disappeared, GDN macro launches dropped
> `3600 -> 2980`, and same-window `gdn_core` moved `1412.33 -> 1400.34 ms`.
> Carry Phase85 only as a small cleanup candidate. Phase86 audited the producer
> fusion idea against the Phase85 node-traced profile before coding it: the
> whole `act/GDN-gate(shared)` macro is only `13.57 ms` of `3.6622 s`, beta
> sigmoid is `2.73 ms`, and CUDA already fuses `UNARY + MUL` for softplus,
> sigmoid, and SILU. Reject producer-only fusion as too small. Phase87 then
> exposed an env-gated `GDN_NW=4 GDN_CPW=8` decode geometry probe to test a
> vLLM-like `BV=32` tile shape. It was md5/op green, but same-source
> decode-only `gdn_core` regressed `1390.56 -> 1417.13 ms`, so the source line
> was reverted. Phase88 tried a first default-off `GDN_DECODE_PACK2=1` packed
> decode CTA kernel. It built and CUDA op tests stayed green, but canonical md5
> failed for both MoE (`320b5ed...` vs `8cb0ce...`) and dense (`6a65e9...` vs
> `5951a5...`), with visible output corruption, so it was reverted without
> profiling. Phase89 tried to add that focused guardrail through
> `test_gated_delta_net_inplace_ids`, but selecting that test class directly
> already fails the pre-existing BF16 cases on CUDA, so the naive test addition
> was also reverted. Phase90 fixed the fixture root cause for identity ids by
> mirroring `state` into `state_dst` during initialization and added F32
> `S_v=128`, `n_seqs=2` cases that return `concat(out,state_dst)`, so the
> backend comparator now checks both attention output and the side-effect state
> write. DGX CUDA selected-op gate is green (`4/4`). Use this Phase90 guardrail
> before any new packed-decode kernel, then still run canonical md5/op gates.
> Phase91 retried the default-off `GDN_DECODE_PACK2=1` CTA sequence-packing
> kernel under that guardrail. The first `n_seqs=2` guardrail passed but MoE md5
> failed for the single-sequence completion gate, exposing an uncovered odd/single
> sequence PDL hazard. Moving inactive lanes past `ggml_cuda_pdl_sync()` and
> adding `n_seqs=1,3` guardrail cases made the candidate md5/op clean
> (`GATED_DELTA_NET 46/46`, `MUL_MAT 1146/1146`, `MUL_MAT_ID 806/806`), but
> decode-only `gdn_core` regressed to `1425.44 ms`, so the runtime patch was
> reverted. Keep the expanded guardrail; do not retry CTA-level sequence packing
> unless it also reduces per-sequence GDN work. ids gather, producer overhead,
> simple geometry changes, and ungated packed kernels are not acceptable parity
> paths. Phase92 tried the next smallest scalar one-token recurrence
> micro-optimization: a default-off `GDN_SCALAR_DECODE_STORE_FUSED=1` CUDA path
> that stores final state inside the scalar update loop and skips the final
> post-token register-store loop. It passed local CPU guardrail, DGX CUDA
> guardrail, canonical md5s, `GATED_DELTA_NET 46/46`, `MUL_MAT 1146/1146`, and
> `MUL_MAT_ID 806/806`, but decode-only `gdn_core` regressed further to
> `1529.72 ms` (`/home/mudler/bench/phase92_gdn_scalar_store_fused/20260701_204718/decode_profile`),
> so the runtime patch was reverted. Do not retry store-fusing without evidence
> that the final state store loop is independently dominant. The next credible
> scoped ideas from the vLLM audit are the larger packed decode contract and the
> Qwen3Next GQA-repeat removal, each as a separate guarded phase. Phase93
> implemented the Qwen3Next GQA-repeat removal as an explicit grouped Q/K
> broadcast mode on `GGML_OP_GATED_DELTA_NET` (`op_params[2]`), preserving the
> existing modulo/tiled broadcast for Qwen35 while allowing Qwen3Next to map
> `qk_head = value_head / (H_v / H_k)` and skip materializing repeated q/k heads
> when the GDN op path is active. Local CPU `GATED_DELTA_NET` passed `48/48`,
> local CPU in-place ids passed `6/6`, DGX CUDA `GATED_DELTA_NET` passed `48/48`,
> DGX CUDA in-place ids passed `6/6`, canonical md5/op gates passed
> (`GATED_DELTA_NET 48/48`, `MUL_MAT 1146/1146`, `MUL_MAT_ID 806/806`), and
> decode-only `gdn_core` improved to `1333.48 ms`
> (`/home/mudler/bench/phase93_qwen3next_gqa_bcast/20260701_211019/decode_profile`).
> Carry Phase93 as the current positive candidate. Phase94 then retested
> decode geometry on top of Phase93 with env-only `GDN_NW=8 GDN_CPW=8`. It
> stayed md5/op clean (`GATED_DELTA_NET 48/48`, `MUL_MAT 1146/1146`,
> `MUL_MAT_ID 806/806`) but decode-only `gdn_core` regressed to `1440.79 ms`
> (`/home/mudler/bench/phase94_gdn_geometry_phase93/20260701_211855/decode_profile_8x8`),
> so reject 8x8 and keep Phase93's default 16x8 geometry. Phase93 trace evidence
> also shows remaining producer-side GDN work is small (`l2_norm_f32 8.65 ms`,
> GDN gate/sigmoid about `12.75 ms`, remaining repeat `5.34 ms`), so the next
> useful lead should target recurrence work or a larger packed decode contract,
> not another small producer-only fusion. Phase95 tested a default-off
> `GDN_WARP_SCALAR_GATE=1` CUDA decode specialization on top of Phase93: lane 0
> computed the scalar non-KDA gate and broadcast it within the warp for the
> one-token `S_v=128`, default `16x8` path. Local CPU guardrails passed
> (`GATED_DELTA_NET 48/48`, in-place ids `6/6`), DGX CUDA guardrails passed
> (`GATED_DELTA_NET 48/48`, in-place ids `6/6`), canonical md5/op gates passed
> (`GATED_DELTA_NET 48/48`, `MUL_MAT 1146/1146`, `MUL_MAT_ID 806/806`), but
> decode-only `gdn_core` regressed to `1402.40 ms`
> (`/home/mudler/bench/phase95_gdn_warp_scalar_gate/20260701_213311/decode_profile`).
> The runtime patch was reverted. Do not retry scalar-gate warp broadcast unless
> a future profile shows SFU pressure, rather than recurrent state traffic or
> reductions, dominating the GDN core. Phase96 then tested the narrow
> conv-state identity fast path suggested by the trace audit: when
> `s_copy_main` was identity, `build_conv_state_fused` viewed the active
> conv-cache slots directly and called `ggml_ssm_conv_update_inplace` instead of
> the ids variant. Local CPU `SSM_CONV` passed `45/45`; DGX CUDA `SSM_CONV`
> passed `45/45`; canonical gates passed (`SSM_CONV 45/45`,
> `GATED_DELTA_NET 48/48`, `MUL_MAT 1146/1146`, `MUL_MAT_ID 806/806`, md5s
> canonical). Decode-only profile regressed to total kernel `3.6723 s`,
> `gdn_core 1406.57 ms`, and `gdn_conv 70.42 ms`
> (`/home/mudler/bench/phase96_conv_identity_fastpath/20260701_214141/decode_profile`).
> The runtime model-graph patch was reverted. Do not retry the conv identity
> branch as a speed lever unless a same-window trace proves the ids variant is
> independently dominant. Phase97 then measured the carried Phase93 stack in an
> end-to-end `n=128`, `PTOK=128`, `GEN=64`, `PARALLEL=128` serving snapshot
> against vLLM. Pre/post canonical gates stayed green. Paged Phase93 measured
> `agg_tps 329.6`, `decode_agg_tps 669.8`, `prefill_tps 1734.5`,
> `ttft_mean_ms 7415.4`, `wall_s 24.851`; vLLM measured `agg_tps 664.8`,
> `decode_agg_tps 1029.4`, `prefill_tps 5271.8`, `ttft_mean_ms 2519.5`,
> `wall_s 11.929`
> (`/home/mudler/bench/phase97_phase93_serving_snapshot/20260701_214648`).
> Phase93 therefore remains a decode-profile positive candidate, but it does not
> close serving parity (`paged_decode_over_vllm=0.6507`). The next useful phase
> needs a larger serving-impact lever; isolated GDN/conv micro-optimizations
> have now repeatedly failed to move live serving enough. Phase98 profiled that
> carried Phase93 serving window with graph-node CUDA tracing. Pre/post gates
> stayed green. Total kernel time was `20.0411 s`; macro buckets were GDN
> `6679.96 ms` (`33.33%`), MoE/FFN-GEMM `6034.52 ms` (`30.11%`),
> bf16/fp8-proj `2766.06 ms` (`13.80%`), and layout-copy `1257.60 ms`
> (`6.28%`). Fine buckets were led by `gdn_core 5892.99 ms` (`29.40%`) and
> `mmq_nvfp4 5809.55 ms` (`28.99%`), followed by `convert_dtype 663.45 ms`,
> `gdn_conv 457.11 ms`, and `concat_layout 430.25 ms`
> (`/home/mudler/bench/phase98_phase93_serving_profile/20260701_215715`).
> This re-ranks the next work: do not spend more time on scalar GDN, conv
> identity, or gather-only shortcuts. Either attribute and remove a proven
> material layout-copy node, or pursue a larger GDN-core/MMQ serving lever with a
> standalone PoC gate. Phase99 then used the existing default-off
> `LLAMA_LAYOUT_TRACE` hook on the same Phase93 serving profile shape
> (`N=128`, `PTOK=128`, `GEN=64`, `PARALLEL=128`). Trace-enabled gates stayed
> green (`GATED_DELTA_NET 48/48`, `MUL_MAT 1146/1146`, `MUL_MAT_ID 806/806`,
> canonical MoE/dense md5s). Serving remained comparable (`total kernel
> 20.2408 s`, `layout-copy 1269.35 ms`). The trace attributed
> `concat_layout 440.01 ms` almost entirely to
> `conv_input-* = concat(conv_states_reshaped-*, qkv_mixed_transposed-*)` before
> `SSM_CONV`; `copy_layout 119.16 ms` includes `conv_state_update-*` writeback.
> The larger `convert_dtype 662.34 ms` bucket is mostly unnamed F32-to-F16 `CPY`
> rows and needs stronger attribution before coding. Decision: Phase99 is
> measurement-only; do not retry the Phase96-style conv-state identity branch.
> The only conv-side patch worth funding is a larger two-source `SSM_CONV`
> contract that reads `(conv_states, qkv_mixed)` as a logical concat, or else
> extend trace attribution for the unnamed `convert_dtype` bucket first
> (`/home/mudler/bench/phase99_layout_trace/20260701_200835/serving_profile`).
> Phase100 extended that trace with `dst_view`, `src0_view`, and `src1_view`
> names. The trace-only patch built locally and on DGX, and trace-enabled gates
> stayed green (`GATED_DELTA_NET 48/48`, `MUL_MAT 1146/1146`,
> `MUL_MAT_ID 806/806`, canonical MoE/dense md5s). Serving stayed comparable
> (`total kernel 20.3464 s`, `convert_dtype 661.73 ms`, `concat_layout
> 438.15 ms`). The new fields identify a concrete `convert_dtype` source:
> `GET_ROWS` reads F16 `cache_k_l*` / `cache_v_l*` into F32 `node_*`, then
> `CPY` downcasts views such as `src0_view=node_358` / `node_365` to F16
> attention-shaped tensors. This repeats across attention layers
> (`cache_k_l3/v_l3`, `cache_k_l7/v_l7`, `cache_k_l11/v_l11`, ...). Some F32->F16
> rows remain unnamed, so the next runtime phase should be a narrow K/V cache
> get_rows dtype A/B, not a broad layout rewrite
> (`/home/mudler/bench/phase100_layout_view_trace/20260701_201800/serving_profile`).
> Phase101 implemented that narrow A/B as default-off
> `LLAMA_PAGED_KV_GET_ROWS_F16=1`: add `ggml_get_rows_type`, support CPU F16
> source -> F16 destination row copy, and use typed F16 `GET_ROWS` only for
> paged K/V gather when the cache tensor is F16. Local and DGX builds completed;
> CUDA `GET_ROWS` passed `49/49` including the new F16-output cases; default and
> opt-in md5/op gates stayed green (`GET_ROWS 49/49`, `GATED_DELTA_NET 48/48`,
> `MUL_MAT 1146/1146`, `MUL_MAT_ID 806/806`, canonical MoE/dense md5s).
> Serving profile under opt-in measured `total kernel 20.1989 s`, `agg_tps
> 206.4`, `decode_agg_tps 328.0`, and `ttft_mean_ms 8211.1`. It reduced
> `copy_layout 116.25 -> 80.32 ms` and macro `layout-copy 1262.58 -> 1220.30 ms`
> versus Phase100, but `convert_dtype` stayed flat (`661.73 -> 661.35 ms`) and
> serving throughput did not improve. Carry Phase101 only as a small default-off
> cleanup candidate pending repeat A/B; do not promote it as a parity lever
> (`/home/mudler/bench/phase101_kv_get_rows_f16/20260701_203930/serving_profile`).
> Phase102 then implemented the funded two-source `SSM_CONV` contract as
> default-off `LLAMA_SSM_CONV_SPLIT=1`: `ggml_ssm_conv_split(ctx, conv_states,
> x_cur, conv_kernel)` reuses `GGML_OP_SSM_CONV`, reads
> `[K-1,channels,n_seqs]` cached taps plus native `[channels,n_tokens,n_seqs]`
> qkv tokens as a logical concat, and is wired into Qwen3Next/Qwen35/Qwen35MoE
> only for multi-token, non-rollback batches with `n_seq_tokens >= K-1`. The
> initial semantic test exposed a harness issue (`split-base` has an exactly
> zero CPU reference, so normalized MSE reported `ERR=inf`); direct split
> CUDA-vs-CPU passed `6/6`, and the final test keeps `split-base` with absolute
> max error. Local and DGX builds passed; default, standalone opt-in, and
> serving pre/post gates stayed green (`SSM_CONV 45/45`, `SSM_CONV_SPLIT 6/6`,
> `GET_ROWS 49/49`, `GATED_DELTA_NET 48/48`, `MUL_MAT 1146/1146`,
> `MUL_MAT_ID 806/806`, canonical MoE/dense md5s). Opt-in serving measured
> `total kernel 19.5482 s`, `agg_tps 206.1`, `decode_agg_tps 320.0`,
> `prefill_tps 1538.0`, and `ttft_mean_ms 7928.4`. It removed the traced concat
> materialization (`concat_layout 433.13 -> 4.59 ms` versus Phase101 and
> `layout-copy 1220.30 -> 826.87 ms`), but live serving throughput still did not
> improve. Carry Phase102 as a default-off cleanup/follow-up base only; do not
> promote it as parity-closing without a repeat A/B or an additional state-update
> fusion. The remaining high-value targets are still `gdn_core`, `mmq_nvfp4`, or
> a larger serving scheduler/packed-decode contract
> (`/home/mudler/bench/phase102_ssm_conv_split/20260701_210907/serving_profile`).
> Phase103 measured Phase101+Phase102 together, with no new source changes:
> `LLAMA_SSM_CONV_SPLIT=1 LLAMA_PAGED_KV_GET_ROWS_F16=1`. Standalone and
> serving pre/post gates stayed green (`SSM_CONV 45/45`, `SSM_CONV_SPLIT 6/6`,
> `GET_ROWS 49/49`, `GATED_DELTA_NET 48/48`, `MUL_MAT 1146/1146`,
> `MUL_MAT_ID 806/806`, canonical MoE/dense md5s). Combined serving improved
> over Phase102 (`agg_tps 206.1 -> 212.3`, `decode_agg_tps 320.0 -> 331.5`,
> `prefill_tps 1538.0 -> 1569.1`, `wall_s 39.743 -> 38.575`) and reduced
> `layout-copy 826.87 -> 798.52 ms`; it also preserved most of the split
> SSM_CONV concat removal and recovered the F16 K/V `copy_layout` reduction
> (`copy_layout 112.53 -> 78.22 ms`). This proves the two cleanup candidates are
> compatible, but not parity-closing: `gdn_core 5930.47 ms` and `mmq_nvfp4
> 6001.77 ms` still dominate. Carry the combined env as the cleanup comparison
> baseline; do not rerun isolated layout cleanup unless it changes a larger
> serving contract
> (`/home/mudler/bench/phase103_combined_layout_cleanups/20260701_211821/serving_profile`).
> Phase104 then measured that combined cleanup stack in the normal same-session
> serving harness against vLLM at `N=128`, `PTOK=128`, `GEN=64`,
> `PARALLEL=128`. Pre/post gates stayed green with the same expanded op set and
> canonical md5s. Paged combined measured `agg_tps 338.6`,
> `decode_agg_tps 675.8`, `prefill_tps 1813.0`, `ttft_mean_ms 7121.6`, and
> `wall_s 24.196`; vLLM measured `agg_tps 661.1`, `decode_agg_tps 1028.0`,
> `prefill_tps 5208.7`, `ttft_mean_ms 2572.3`, and `wall_s 11.980`. This is a
> small serving improvement over Phase97 (`agg_tps +2.73%`, `prefill_tps
> +4.53%`, `TTFT -3.96%`), but still not parity: `paged_decode_over_vllm=0.6574`
> and `paged_agg_over_vllm=0.5122`. Carry the combined cleanup stack as the best
> current comparison baseline. The next useful phase must attack a larger
> serving-impact contract or the dominant GDN/MMQ buckets, not more isolated
> layout-copy cleanup
> (`/home/mudler/bench/phase104_combined_serving_snapshot/20260701_212551`).
> Phase105 refreshed grouped-MMQ evidence on that current stack without source
> changes. `MUL_MAT_ID_RAGGED_MOE` stayed green both default and trace-enabled
> (`6/6`), full `MUL_MAT_ID` stayed green (`806/806`), and the live serving
> retry returned a non-empty response while recording `120` shape and launch
> lines. The live sample was prefill-like (`ncols_max=317`, density `10`,
> `mmq_x_best=112`, `stream_k=1`) with no small-M lines; all launches had
> `fixup=0`, `stream_k_blocks == ntiles_dst`, and efficiency `100`. This
> confirms the current cleanup stack did not open a new cheap MMQ shortcut.
> Do not add another host-side MMQ tile policy; only revisit MMQ for a
> genuinely structural kernel or serving-contract change
> (`/home/mudler/bench/phase105_mmq_current_shape/20260701_214129_serving_retry`).
> Phase106 tested the remaining low-conflict C1 operating-point hypothesis on
> the current stack: same-session `N=128/192/256` with `PARALLEL=256`,
> `VLLM_MAX_NUM_SEQS=256`, and the combined cleanup env. Pre/post gates stayed
> green with canonical md5s and the expanded op set. vLLM completed all legs and
> stayed ahead: at `N=256`, paged measured `agg_tps 338.4`,
> `decode_agg_tps 824.6`, `ttft_mean_ms 14933.5`, while vLLM measured
> `agg_tps 723.8`, `decode_agg_tps 1320.4`, `ttft_mean_ms 4999.0`. Reject C1
> for the current GB10 stack. The next source phase should be structural
> persistent-batch/fused-MoE/GDN work, not another scheduler shortcut
> (`/home/mudler/bench/phase106_max_concurrency_current_stack/20260701_214907`).
> Phase107 established the fused-MoE structural guardrail surface before coding:
> `MOE_SWIGLU_DOWN 7/7`, `MOE_WEIGHTED_COMBINE 7/7`, and
> `MUL_MAT_ID_RAGGED_MOE 6/6` passed on CUDA0. However,
> `test-backend-ops perf` did not provide usable timing rows for these custom
> whole-graph cases; the broad `MUL_MAT_ID` perf CSV reported support metadata
> only. The next source patch should be measurement-only: add a narrow MoE
> fusion timing harness with explicit GPU synchronization and CSV timing before
> funding any fused routed-MoE kernel
> (`/home/mudler/bench/phase107_moe_fusion_guardrail/20260701_220227`).

- Historical verdict: the older investigation marked GB10 parity **CLOSED** and
  unreachable. Treat that as superseded where Phase50-54 provide newer dense
  serving evidence.
- **Prefill** is a genuine floor at **~36% (MoE) / ~43% (dense)** of vLLM. Prefill is **not** CUDA-graph-replayed, so these numbers are real, not measurement artifacts.
- **Decode** is **near-parity: ~86% of vLLM's TRUE GPU-steady decode** (924 vs 1078 t/s). The long-standing **~56% headline was a CUDA-graph measurement artifact** (nsys without `--cuda-graph-trace=node` collapses each graph replay into one opaque launch). Decode is also **ahead of vLLM at low concurrency** (dense 116.7% at N=8) and uses **1.5-3x less memory**, bit-exact per-path.
- The lever search was **exhaustive**: every attempt (prefill GEMM, GDN chunked scan, decode fusions, serving/scheduler) is recorded with its verdict and number so it is **not re-run**.
- **The path to parity is different hardware: datacenter Blackwell** (B200, HBM, native tcgen05 / CUTLASS FP4). Do NOT reopen GB10 kernels. Re-run the methodology on the new silicon, where vLLM's GB10-losing FLA/Marlin kernels invert.

---

## 2. THE HARD GATES YOU MUST NOT VIOLATE

These are non-negotiable. Violating any of them invalidates the result or the contribution.

### 2.1 The per-path greedy-md5 bit-exact gate (sacred)
The gate is **per-path**: paged vs non-paged attention legitimately produce different (equivalent) FP-reduction orders. Each path is gated against **its own** reference, validated benign by KL-divergence to the f16 reference. Canonical greedy md5s:

| Path | Model | Canonical md5 |
|---|---|---|
| non-paged | MoE q36-35b-a3b-nvfp4 | `07db32c2bcb78d17a43ed18bc22705cd` |
| **paged** | MoE q36-35b-a3b-nvfp4 | `8cb0ce23777bf55f92f63d0292c756b0` |
| non-paged | dense q36-27b-nvfp4 | `5951a5b4d624ce891e22ab5fca9bc439` |
| paged | dense q36-27b-nvfp4 | `5951a5b4d624ce891e22ab5fca9bc439` (bit-exact to non-paged) |

- **Compare paged-to-paged only.** Future paged-MoE regressions compare to `8cb0ce23`, NOT `07db32c2`.
- **Why paged-MoE differs (benign, KL-validated):** `llama-perplexity --kl-divergence` on the MoE GGUF (16 chunks, f16 base PPL 7.3734) shows non-paged-vs-f16 KLD 0.136597 and paged-vs-f16 KLD 0.136000, i.e. paged does NOT diverge from f16 ground truth more than non-paged does. Paged and non-paged are two equivalent FP-reorderings of the same 4-bit model. This holds on the 0028 baseline and with `LLAMA_MOE_FORCE_GRAPHS`/0029 on or off, so it is a property of the paged path, not any one lever.
- **Every bit-exact patch is gated two ways:** greedy md5 (per path) AND `test-backend-ops` vs the CPU oracle for every touched op.

### 2.2 The KL-gate for opt-in lossy paths
Any path that is NOT byte-identical (e.g. 0033 dequant-bf16, the 0034/0035 large-M FP paths, FP8-KV) ships **default-off** and is gated by a **KL-divergence band**: it requires `KLD(new||f16) <= KLD(FP4-MMQ||f16)` and PPL within the established band. Lossy levers never ship default-on.

### 2.3 In-backend A/B is the only proof (hard methodology rule)
A lever compiled into the binary is **NOT** isolated by a runtime flag alone. It needs a **separately-built in-backend A/B**. Precedents that burned this in: 0031 chunking math was correct yet -22% in-backend; 0034 had a standalone PoC win that did not hold in-backend.

### 2.4 Contribution / commit gates (LocalAI policy)
- **DCO sign-off is human-only:** do not add an AI `Signed-off-by` trailer.
- **AI attribution via `Assisted-by:` trailer:** `Assisted-by: Codex:gpt-5`.
- **NEVER add `Co-Authored-By:` (AI) trailers** and never add an AI `Signed-off-by`.
- **No em-dashes** anywhere in output (use `-`, `:`, parentheses, or rephrase).
- **Ask before every `git push`.** Prior approval does not carry over.

### 2.5 Fork-first is MANDATORY (the fork is canonical)
- The **canonical source of truth is the fork branch `mudler/llama.cpp:localai-paged`** (= pin commit + paged patch commits in order). It is canonical for ALL paged-backend kernel/patch work. The shipped `patches/paged/*.patch` series is a **derivative**: the fork is the source.
- **Always update the fork FIRST, in this exact order:** (1) commit the change on the `localai-paged` branch and **push it**, then (2) regenerate the LocalAI series (`backend/cpp/llama-cpp-localai-paged/patches/paged/`) from the fork via `git format-patch` (one patch per fork commit, source-only, never touching a `*.md`/dev-doc), so the series stays a **1:1, drift-free mirror** of the branch. No hand-export.
- **NEVER edit the LocalAI `patches/paged/*.patch` files directly**, and **NEVER add a patch to the series with no corresponding fork-branch commit.** They are generated output, not source.
- The fork branch is also **where the build and the per-path bit-exact md5 gate actually run**, so it is the **only** place a change is truly validated. A patch that lives only in the LocalAI series has never been built or gated.
- **Mirror invariant (verify by tree hash):** applying the full on-disk series on the pin must reproduce the fork branch tree byte-for-byte. The series has **intentional gaps** (missing 0005, 0026, 0027, 0032, 0036-0039, 0045), so the patch count is not the max number; what must hold is the tree-hash equality, not the count. Current verified state: fork HEAD `2d590d770` is mirrored by worktree patch `0063-feat-cuda-trace-cublas-tensor-names.patch`; applying all `54` patch files on `0ed235ea2c17a19fc8238668653946721ed136fd` produces tree `dedb1182910eafe9f6875588dc8285bfb544cce5`, exactly matching the fork.

### 2.6 Bench hygiene gates
- **NEVER set `LLAMA_MAX_BATCH_TOKENS` in benches** (the harness explicitly logs "NO LLAMA_MAX_BATCH_TOKENS").
- Do **not** set `GDN_TC`, `GDN_CHUNK_MIN`, or `LLAMA_PAGED_DECODE_STABLE` in parity benches. Production defaults are compiled in: **GDN M5 on (`GDN_TC=5`, `GDN_CHUNK_MIN=64`), S1 decode-graph on, S3 off.**
- **Decode profiling MUST use `nsys --cuda-graph-trace=node`** (see section 3.4). This is a gate, not a suggestion.

---

## 3. OPERATIONAL QUICKSTART (copy-pasteable)

### 3.0 Host
```
ssh dgx.casa        # resolves to hostname promaxgb10-4ad8; GPU = NVIDIA GB10 (unified LPDDR5x, ~273 GB/s, the bandwidth floor)
```
`nvidia-smi` reports memory as `[N/A]` (unified memory). CUDA 13 / sm_121.

### 3.1 GPU lock protocol (`~/gpu_bench_lock`) - TWO conventions, reconcile carefully
There are two conventions in flight:
- **Old harnesses** (`combined_definitive.sh`, `fuse_validate.sh`, `fuse_profile.sh`) treat it as an **empty mutex dir**: `mkdir ~/gpu_bench_lock` to acquire, `rmdir` to release.
- **Newer harnesses** (`fp4norm_profile.sh`) use an **owner-file convention**: `mkdir -p ~/gpu_bench_lock` then `echo "$ME $(date +%s)" > ~/gpu_bench_lock/owner`. They poll until `nvidia-smi --query-compute-apps=pid` count is 0 AND `owner` is `FREE*`/absent for 2 consecutive checks, and clear a stale `~/gpu_bench_lock/release` file. Release **writes** `FREE released-by-... $(date +%s)` to `owner` (it does NOT remove the dir).

Because the dir now permanently contains an `owner` file, **release with `rm -rf ~/gpu_bench_lock`, NOT `rmdir`** (rmdir fails on the non-empty dir). Recommended procedure for a future agent:
1. Read `~/gpu_bench_lock/owner`. `FREE*`/absent + 0 compute-apps means free.
2. Acquire via `mkdir -p ~/gpu_bench_lock` + write `owner`.
3. Release by writing `FREE ...` to `owner` (or `rm -rf ~/gpu_bench_lock`).

A separate 0-byte `~/bench/gpu.lock` is legacy/unrelated - ignore.

**Always gate on ALL THREE** before benching or building on DGX: `nvidia-smi --query-compute-apps=pid` count == 0, `owner` FREE, and `docker ps` shows no running containers. In particular, do not start work while a `local-ai-worker` container is running. Concurrent jobs share this GPU: an offline-repack Marlin workflow, an `~/.cache/autoresearch-quant/` quant pipeline (this is the `llama-imatrix` class of job), finetune trees, and LocalAI worker containers. The canonical harnesses poll for GPU-idle up to 2h.

### 3.2 Build (long; run detached + poll)
- **Mainline / canonical grpc-server + binaries: CUDA arch `121`** (`-DCMAKE_CUDA_ARCHITECTURES=121`). Runtime banner shows `ARCHS = 1210 | BLACKWELL_NATIVE_FP4 = 1`.
- **FP4-MMA / tensor-core experimental kernels: the accelerated `121a` gencode** (`arch=compute_121a,code=[compute_121a,sm_121a]`). The `a` suffix unlocks tcgen05 / native FP4-MMA intrinsics. `121a` lives ONLY in the DGX experimental build scripts (`~/gdn_cc.sh` standalone nvcc, `~/gdn_bv_build.sh` `-DCMAKE_CUDA_ARCHITECTURES=121a`, `~/paged-build.sh` `--build-arg CUDA_DOCKER_ARCH=121a`), not in the worktree build files. Supply it at build time via `CMAKE_CUDA_ARCHITECTURES` / `CUDA_DOCKER_ARCH`.
- **Long builds: run detached and poll for a marker.** Pattern: `nohup ... > build.log 2>&1 &` then poll for a `.DONE`/`.done` file. Do NOT block a foreground shell.

Built binaries live at `dgx:~/llama-paged-dev/build-cuda/bin/` (`llama-server`, `llama-batched-bench`, `llama-completion`; thin ~70 KB dynamic wrappers).

### 3.3 The standard bench env + commands
```
cd /home/mudler/llama-paged-dev/build-cuda/bin
L="LLAMA_KV_PAGED=1 LLAMA_MOE_FORCE_GRAPHS=1 GGML_NO_BACKTRACE=1"   # GGML_NO_BACKTRACE is log-hygiene, not a lever
MOE=/home/mudler/bench/q36-35b-a3b-nvfp4.gguf       # arch qwen35moe, ~22.2 GiB
DENSE=/home/mudler/bench/q36-27b-nvfp4.gguf         # arch qwen35,    ~17.5 GiB

# (1) Bit-exact / coherence gate. stdin MUST be /dev/null or it hangs in conv mode.
env $L ./llama-completion -m "$MOE" -ngl 99 -fa on -c 4096 --temp 0 --seed 1 -n 48 -no-cnv \
    -p "The capital of France is" </dev/null | md5sum
# The PAGED_BITEXACT_NOTE gate command uses the chat-template path (NO -no-cnv):
#   ./llama-completion -m MODEL -ngl 99 -fa on -p "The capital of France is" -n 48 --temp 0 --seed 1
# (compare to the canonical md5 for that model+path; paged-to-paged only)

# (2) PREFILL bench (S_PP from llama-batched-bench)
env $L ./llama-batched-bench -m "$MOE" -c 131072 -b 2048 -ub 512 -ngl 99 -fa on \
    -npp 512,2048 -ntg 4 -npl 32

# (3) SERVING bench: one --parallel 256 server, then drive with h2h_cli3.py
env $L nohup ./llama-server -m "$MOE" -c 262144 --parallel 256 -b 2048 -ub 512 \
    -ngl 99 -fa on --host 127.0.0.1 --port 8090 --no-webui >/home/mudler/bench/paged_server.log 2>&1 &
# poll http://127.0.0.1:8090/health for '"ok"', then:
python3 /home/mudler/bench/h2h_cli3.py   # OpenAI /v1/completions, ignore_eos, fresh-nonce, ptok128 gen128, NPL sweep 8/32/128/256
```
**vLLM side** (for both-engine parity): `~/vllm-bench/bin/vllm` (version **0.23.0**), served `gpu-util 0.85 max-model-len 4096 max-num-seqs 256 tp1`, models `~/bench/q36-35b-a3b-nvfp4-vllm/` and `~/bench/q36-27b-nvfp4-vllm/`.

**Current-stack serving snapshots use `backend/cpp/llama-cpp-localai-paged/paged-current-serving-snapshot.sh`.** It targets the clean `~/llama-phase6-source` mirror, checks docker/`local-ai-worker`/GPU-idle state, uses the owner-file lock, runs pre/post inference gates, then compares paged and vLLM with the same h2h client. The older `dgx:~/bench/combined_definitive.sh` is historical: do not reuse it without first porting away from stale `~/llama-paged-dev` paths and old lock assumptions.
The harness also writes `hardware.txt` before any server starts, including
`DRY_RUN=1`, so every new snapshot records the GPU model, driver, compute
capability when exposed by `nvidia-smi`, and a conservative hardware class.
Full runs also write `gate_summary.tsv` after the post gate, summarizing pre/post
MoE md5, dense md5, and backend-op checks; use
`paged-current-serving-snapshot.sh --summarize-gates ART` to backfill or audit an
existing snapshot without starting servers.

### 3.4 THE DECODE-PROFILING RULE (this trap caused 4 wrong analyses)
Decode runs as a **replayed CUDA graph**. `nsys` **without** `--cuda-graph-trace=node` collapses each graph replay into ONE opaque launch, so every per-kernel attribution becomes an artifact. This is exactly what made the old "paged 159 us/tok, GPU ~16% busy, host-bound, 5.4x more GPU-efficient" story wrong, and produced the wrong ~56% headline.

Mandatory method for any decode profile:
- Use **`nsys --cuda-graph-trace=node`**.
- Decompose with the **difference method**: per-token cost = (ntg=64 profile) - (ntg=16 profile).

Under the correct method, paged decode at npl=256 is **99% GPU-busy (1.4% idle), NOT host-bound** - the opposite of the collapsed-graph reading. The clean graph-node-traced profiles are at `~/highN_prof2/*.nsys-rep` (paged, npl=256) and `~/highN_vllm/*.nsys-rep` (vLLM), captured 2026-06-30. They **supersede every earlier decode decomposition.**

### 3.5 Models + artifacts (all on DGX)
GGUF (paged): `~/bench/q36-35b-a3b-nvfp4.gguf` (MoE, qwen35moe), `~/bench/q36-27b-nvfp4.gguf` (dense, qwen35). vLLM safetensors: `~/bench/q36-35b-a3b-nvfp4-vllm/` (has `hf_quant_config.json` confirming MIXED_PRECISION / FP8-proj), `~/bench/q36-27b-nvfp4-vllm/`.
Authoritative run: `~/bench/COMBINED_DEFINITIVE.txt` (+ `.log`, `.done`, `combined_definitive.sh`, per-engine `COMBINED_*_server.log`). A/B dirs: `~/bench/marlin_gate/`, `~/bench/gdn_p1_ab/`. NOTE: the `*_RESULTS*`/`*_MAP*` docs live only in the worktree `docs/`, not on the DGX.

---

## 4. THE COMPLETE LEVER MAP (do NOT re-run the rejected ones)

Verdicts and numbers are from `VLLM_PARITY_FINAL.md` + the cited artifacts. "BE" = greedy-md5 bit-exact; "KL-benign" = lossy path inside the KL band.

### 4.1 Prefill weight-GEMM track - WHOLE TRACK REJECTED (FP4-MMQ is optimal on GB10)
Decisive surprise: on sm_121 **vLLM itself does NOT run native FP4** - it runs **Marlin W4A16** (FP4 dequant->bf16 in-register + bf16 GEMM) for experts and FP8 projections, capped at ~half FP4 peak, because native CUTLASS NVFP4 grouped-GEMM is broken on consumer Blackwell (TMA-WS init failure, CUTLASS #3096; no tcgen05/TMEM). So MMQ's native FP4 is already structurally competitive here.

| Lever | What | Verdict | Key number |
|---|---|---|---|
| 0033 dequant->bf16 cuBLAS | route large-M NVFP4 dense GEMM to dequant->bf16 cuBLAS | REJECTED, ships default-off | dense S_PP -49%/-42%/-29% at M=512/1024/2048; BE + KL-better |
| dense-cuBLAS reroute (full) | same across dense+MoE prefill | REJECTED | -31% to -62% band |
| 0034 native FP4-MMA W4A4 | Blackwell `mxf4nvf4` OMMA large-M | REJECTED in-backend | PoC 103 TFLOP/s (57.7% FP4 peak, NMSE=0) but win did not hold in-backend |
| 0035 W4A16-Marlin grouped MoE | FP4->bf16 in-register + bf16 mma, zero act-quant tax | REJECTED (perf) | correct + KL-benign-and-better but **-39%** S_PP vs MMQ |
| 0045/0046 offline-repack / vLLM-verbatim Marlin | repack to Marlin layout; port vLLM kernel verbatim | REJECTED | verbatim correct but -39%; offline-repack same bf16-peak ceiling, no win |

Why it loses: bf16 TC peak on GB10 is ~half FP4 peak, so any dequant->bf16 kernel caps at ~half FP4-MMQ; the dequant write is an un-amortized weight-sized memory pass (~8x the FP4-read traffic). **The GEMM bucket is not winnable on GB10 with available kernels.**

### 4.2 Prefill GDN chunked-scan track - M5 tf32 C=16 is the SHIPPED winner
GDN is the #1 prefill-gap contributor (+59.2 us/tok, ~30%). vLLM's FLA `chunk_gated_delta_rule` runs the same math at 36.5 vs paged 95.7 us/tok = 2.62x via tensor-core intra-chunk Gram products.

| Lever | What | Verdict | Key number |
|---|---|---|---|
| 0031 scalar-serial chunked scan | FLA-style scalar/serial (`GDN_TC=0`) | superseded | correct but ~22% slower at forced C=16 |
| **0047 / M5 tf32 tensor-core scan** | tf32 `m16n8k8` mma form-T solve, f32-only | **SHIPPED default-on under paged** | MoE prefill +3.5% @npp512, +17.7% @npp2048; decode unchanged; BE-benign |
| bf16 CONFIG-C (M8) | bf16 Kc/Qc + 2 C*C scratch, C->64 | REJECTED (not in f32 series) | confirmed geometry then dropped |
| bf16-C16 | bf16 Gram at C=16 | REJECTED | no win; bf16 mantissa unsafe on state-coupled products |
| BV block-occupancy A/B (tf32) | raise blocks/SM | REJECTED (occupancy NOT the bound) | 1844 vs 1814 S_PP (-1.04%, within noise) |
| bf16-C64 | bf16 Gram at C=64 | REJECTED | -18.75%; O(C^2) intra-chunk + serial recurrence dominates |
| Phase 10 C32 slab M5 | C=32, two `dv_tile=64` slabs, default-off `GDN_C32_SLAB=1` | REJECTED | md5-clean after tail-row zeroing, but slower: MoE 2048 2430.32 -> 2054.86; dense 2048 1019.25 -> 903.73 |
| Phase 11 QS-early M5 | move `QS = Qc * S0` earlier, default-off `GDN_M5_QS_EARLY=1` | REJECTED | md5-clean, but slightly slower: MoE 2048 2441.54 -> 2420.26; dense 2048 1021.06 -> 1015.77 |
| Phase 12 shared-A/Ai cost model | f32 Ai scratch shared across two C32 value slabs | GO to one default-off prototype | BT32 f32 scratch at npp2048,npl32: MoE 256 MiB / 768 MiB Ai traffic; dense 384 MiB / 1152 MiB Ai traffic |
| Phase 13 Global-Ai32 | precompute f32 Ai once, consume from two C32 `dv_tile=64` slabs | REJECTED | md5-clean, but slower: MoE 2048 2425.10 -> 2097.76; dense 2048 1016.14 -> 918.19 |

Why not occupancy/dtype: the cost is the **O(C^2) intra-chunk triangular A-inverse solve + the strictly-serial inter-chunk recurrence**, with C forced to **16** by GB10's 99 KB dynamic-smem cap (the 128x128 f32 state alone is 64 KB). M5 captures the tractable TC part; it does not fully close 2.62x because vLLM's FLA blocked-solve is a more complete TC implementation.

Phase 13 closes the caveat: the default-off `GDN_GLOBAL_AI32=1` prototype was
correctness-clean but slower. Stop GDN kernel work on GB10 instead of iterating
into f16 Ai or more local reorders.

### 4.3 Decode / fusion levers - all REJECTED (near-parity already at ~86% true GPU-steady)
| Lever | What | Verdict | Key number |
|---|---|---|---|
| act-quant folded into ggml MMQ | inline y-quant in MoE expert MMQ | REJECTED | **-79.4%**; ggml MMQ re-quantizes y per weight-row-tile x stream-k split, no TC for inline quant |
| norm+quant+silu fusion | one launch (vLLM Triton kernel) | REJECTED (infeasible) | `ggml_cuda_can_fuse` cannot express it: FP4 quant is a mul_mat-internal prologue, silu separated from norm by 2 GEMMs + router |
| Q8_0 / FP8 projection | quantize bf16 GDN/attn projections | REJECTED (regime error) | vLLM DOES use FP8 proj, but at N>=128 proj is only ~12% of stream, closes <=6% |
| NVFP4 the projections | drop proj to NVFP4 | REJECTED | KL-fail, ~+6% PPL; vLLM keeps SAME bf16/FP8 proj, never NVFP4 |
| W4A16-Marlin MoE decode | Marlin grouped expert GEMM at decode | REJECTED | BW-floored wash, ~5% slower |
| bf16-tau per-head SSM (0026) | per-head bf16 tau on SSM decode | DROPPED | flat 780.6 vs 780.0 t/s; earlier "+12%" subsumed by 0028/0029 |
| D3 FA-split / D4 GDN-width-adaptive | older off-critical-path levers | SUPERSEDED reasoning | were rejected via the debunked "5.4x/host-bound" reading; under HNP the GDN scan IS critical path (51%) but is the shared BW floor where paged leads (83% vs 79%), so still not a win |

Dense decode is **AHEAD at low N (116.7% @ N=8)** - the one operating point where paged is unambiguously faster.

### 4.4 Serving / engine levers - host loop and scheduler CLOSED
| Lever | What | Verdict | Key number |
|---|---|---|---|
| **0040 / S1** paged decode-graph reuse | `can_reuse` keyed on bucketed block-table dims | SHIPPED default-on | serving reuse 0% -> 72.2% (with S3); static 0% -> 95.5% |
| **0041 / S3** decode-shape-stable scheduling (`LLAMA_PAGED_DECODE_STABLE`) | keep prefill out of decode steps | SHIPPED **default-OFF** (opt-in) | recovers the ~17 pt graph-reuse overhead at a TTFT cost; default-on regressed real serving (2.5x worse TTFT, 20-29% lower e2e throughput) |
| **0043 / D1** full-step MoE decode CUDA graph | graph whole decode step incl. grouped-MMQ MoE dispatch | SHIPPED default-on | +2.6% (npl128) to +5-13% (npl32); D1 premise "host-sync on MoE readback" REFUTED (sync count identical 1457 on/off) |
| S2 double-buffer set_inputs | overlap host input build with GPU | DROPPED | `set_inputs` ~0.05 ms/step, nothing to recover |
| whole-step graph / host loop | host loop as serving residual | CLOSED (~0-1%) | reuse 0% (757.6) == S1+S3 72% (763.3); hostproc only ~4-8% of step wall |
| padded / fixed-slot decode | pad decode width to `--parallel` for ~100% reuse | **REJECTED (built, GPU-tested, commit b028c81e)** | inert (BE) but regresses everywhere; N=8 burst 28.16->6.05 tok/s/seq; serving decode is GPU-compute-bound, dummy-row compute > reuse recovered |
| speculative decode (MTP) | draft + verify | **REJECTED for current GB10 serving** | Phase 14 safety passed, but Phase 15 serving A/B regressed hard: n128 decode agg 662.4 -> 138.5 tok/s; likely graph/batch-shape disruption (`graphs reused` 361 -> 1) |

### 4.5 SHIPPED WINS (all BE / KL-benign) - keep these, do not regress
- **FP4-MMQ MoE/dense GEMM** (native Blackwell FP4-MMA at the FP4 weight-BW floor; reason 4.1 stays default-off).
- **M5 tf32 tensor-core chunked GDN prefill (patch 0047)**, default-on under `LLAMA_KV_PAGED` (`GDN_TC=5` + `GDN_CHUNK_MIN=64`).
- **0042 fused residual-add + RMSNorm + weight-mul** (dense S_PP +0.5%, BE).
- **0044 fused GatedRMSNorm + SiLU gate-mul** (672 -> 336 launches @npp512; dense +1.1%, MoE +0.9%, test-backend-ops 12979/12979).
- **0046 GDN-prefill geometry gate** (gates 0022's decode retune by scan length; recovers +7.2% dense prefill, keeps the decode win, BE).
- **SSM decode-fusion stack (0018-0022, 0028)**: in-place state (+23.5%/+18.9%), fused gather (+37.8%/+35.3%), o_proj reshape (+31.7%/+23.3%), conv in-place (+3.2%/+3.5%), occupancy retune (+11.1%/+8.3%) = the **2.26x / 2.46x over stock** decode multiplier.
- **Serving host loop closed (0040 S1, 0043 D1).**
- **The memory advantage** (1.5-3x lower VRAM, NVFP4-resident, no persistent bf16 dequant copies).
- **Low-N decode lead** (dense 116.7% @ N=8). **Bit-exact output per-path** through the whole series.

### 4.6 REMAINING / unattempted levers + EV
- **Multi-week persistent-Marlin decode kernel** (vLLM's fused-Marlin MoE persistent-tiling + Triton elementwise): the only path to the residual ~14 pt GPU-steady decode gap. **Low-EV**: decode-only ~4-14%, our own ggml Marlin port already lost -19.6%, needs mature tiling + multi-stream overlap (hard inside a single-stream CUDA graph), GB10-uncertain, and **cannot lift the prefill floor**. Not a free bit-exact lever.
- **Datacenter-Blackwell pivot** (B200, ~8 TB/s HBM, native tcgen05/CUTLASS FP4, TMEM): lifts the LPDDR5x GDN bandwidth floor ~30x and restores exactly the vLLM advantages that lose on GB10. **This is the documented path to parity.** Re-run the methodology on the new silicon, do not reopen GB10 levers.

The `VLLM_PARITY_LEVER_MAP.md` "pursue list" (A1-A7/B1-B7/C1: graph-safe ragged grouped FP4-MMA MoE kernel, FP8 paged KV, MTP spec-decode, etc.) is the **earlier working brainstorm written before the final profiling**. `VLLM_PARITY_FINAL.md` is the authoritative supersession; treat those buckets as rejected / infeasible / different-hardware unless re-validated on new silicon.

Phase 14 re-validated the MTP bucket as safe, then Phase 15 rejected it as a
current GB10 serving-throughput lever. Do not enable it by default and do not
keep tuning draft length blindly. The only plausible follow-up is a graph-reuse
and speculative verification batch-shape profile with
`nsys --cuda-graph-trace=node`. Phase 16 ran that profile and supported the
root cause: small-shape baseline reused graphs (`graphs reused = 62`) while MTP
did not (`graphs reused = 1`) and did ~2.3x more GPU kernel work. The fixed
safety gates stayed green before and after the failed serving A/B: MoE md5
`8cb0ce23777bf55f92f63d0292c756b0`, dense md5
`5951a5b4d624ce891e22ab5fca9bc439`, and `MUL_MAT_ID` `806/806`.

Phase 17 source inspection found no tiny additive graph-reuse fix. MTP
verification rows are real target decode/output rows (`K + 1` per speculative
slot), so fake padding would touch KV, positions, logits, MTP nextn state, and
rollback semantics. If reopened, start with a server-only shape counter around
`server_slot::handle_last_sampled_token()`. Only then consider an opt-in
group/defer-by-draft-length scheduler experiment, with TTFT/throughput and
md5/op gates as kill criteria.

Phase 18 added the server-only shape trace as patch 0055. Set
`LLAMA_SPEC_SHAPE_TRACE=1` to log `kind=decode` rows and MTP `kind=verify`
`K + 1` row/output shapes from `server_slot::handle_last_sampled_token()`.
This is default-off instrumentation only. DGX green check after the patch saw
MTP verify shapes vary (`rows=4`, then `rows=3`) on a tiny request, while the
env-unset run emitted no `spec shape:` lines. Canonical post-patch gates passed:
MoE `8cb0ce23777bf55f92f63d0292c756b0`, dense
`5951a5b4d624ce891e22ab5fca9bc439`, and `MUL_MAT_ID` `806/806`.
Artifacts:
`/home/mudler/bench/phase18_mtp_shape_trace_green` and
`/home/mudler/bench/phase18_mtp_shape_trace_green/gate_after`.

Next MTP step, if any: trace real serving shape entropy first. Do not implement
a scheduler change until the trace shows repeatable draft-length buckets worth
grouping. Any scheduler experiment must be opt-in/default-off and killed by
TTFT/throughput regression, graph-reuse failure, md5/op drift, or MTP
rollback/prefix gate failure.

Phase 19 ran that trace-only serving measurement and rejected the scheduler
shortcut. Artifact:
`/home/mudler/bench/phase19_mtp_shape_entropy/20260701_045534`. Pre/post gates
passed with canonical MoE md5 `8cb0ce23777bf55f92f63d0292c756b0`, dense md5
`5951a5b4d624ce891e22ab5fca9bc439`, and `MUL_MAT_ID` `806/806`.

Serving result:

| n | baseline decode_agg | MTP decode_agg | MTP / baseline | baseline TTFT ms | MTP TTFT ms |
|---|---------------------|----------------|----------------|------------------|-------------|
| 8 | 245.0 | 95.7 | 39.1% | 1147.2 | 1633.4 |
| 32 | 409.2 | 110.0 | 26.9% | 2710.0 | 4471.5 |
| 128 | 697.2 | 154.0 | 22.1% | 7601.5 | 20310.4 |

Shape result: `draft=3` already accounts for 96.2-96.9% of verify slots, so
group/defer-by-draft has little to recover. Full in-flight steps already mostly
use all-`draft=3` vectors; the remaining churn is active-slot/tail churn plus
the real `K + 1` verification-row expansion. Do not build a Phase 20 scheduler
experiment on this evidence. Future MTP work would need a deeper target-verify
graph/state design, not another small server scheduling shortcut.

Phase 62 ran that gated verify-cost recheck. Artifact:
`/home/mudler/bench/phase62_mtp_verify_cost/20260701_134125`. Pre/post gates
passed with canonical MoE md5 `8cb0ce23777bf55f92f63d0292c756b0`, dense md5
`5951a5b4d624ce891e22ab5fca9bc439`, and `MUL_MAT_ID` `806/806`. MTP acceptance
was high (`7372/9340 = 0.789`, mean acceptance length `3.33`), but throughput
remained far below the keep threshold: `0.420x`, `0.274x`, and `0.213x`
baseline decode at n8/n32/n128. Shape trace again showed `draft=3` / `rows=4`
dominance (`95.6%`), with `graphs reused = 1`. Keep current MTP rejected unless
a later target-verify/output-row graph-cost design exists; do not tune
`spec-draft-n-max` blindly.

Phase 20 refreshed the current-stack MoE serving snapshot against vLLM using the
clean `~/llama-phase6-source` mirror (`f2521ab12`) rather than the stale
`llama-paged-dev` benchmark tree. Artifact:
`/home/mudler/bench/phase20_current_snapshot/20260701_050621`. Pre/post gates
passed with canonical MoE md5 `8cb0ce23777bf55f92f63d0292c756b0`, dense md5
`5951a5b4d624ce891e22ab5fca9bc439`, and `MUL_MAT_ID` `806/806`.

Current MoE serving snapshot (`PTOK=128`, `GEN=64`):

| n | paged decode_agg | vLLM decode_agg | paged/vLLM decode | paged agg | vLLM agg | paged/vLLM agg |
|---|------------------|-----------------|-------------------|-----------|----------|----------------|
| 8 | 220.8 | 290.5 | 76.0% | 164.8 | 245.5 | 67.1% |
| 32 | 411.1 | 594.7 | 69.1% | 252.1 | 456.0 | 55.3% |
| 128 | 670.0 | 1022.7 | 65.5% | 322.4 | 662.4 | 48.7% |

TTFT remains the clearest user-visible gap: paged is 2.88x/3.36x/3.11x slower
than vLLM at n8/n32/n128, and paged prefill_tps is roughly one-third of vLLM.
This keeps the GB10 shortcut closure intact: do not reopen MTP or small
scheduler work. The credible next parity path is a datacenter-Blackwell rerun or
a larger fused-kernel project outside this low-conflict patch stack.

Phase 21 added a reusable current-stack serving harness:
`backend/cpp/llama-cpp-localai-paged/paged-current-serving-snapshot.sh`.
It defaults to `~/llama-phase6-source`, validates docker/`local-ai-worker`/GPU
idle state, uses the owner-file lock, runs pre/post inference gates, compares
paged and vLLM with h2h, and writes ratio summaries. DGX dry run passed at
`/home/mudler/bench/phase21_harness_dryrun/20260701_051757`.

Use this harness for future current-stack GB10 snapshots. Do not reuse
`~/bench/combined_definitive.sh` unless it is first ported away from stale
`~/llama-paged-dev` paths and old lock assumptions.

Phase 31 re-verified the patch-series mirror invariant after patch `0057`:
applying every LocalAI `patches/paged/0*.patch` with strict `git apply` on top of
Makefile pin `0ed235ea2c17a19fc8238668653946721ed136fd` produced tree
`4eae628e4ba6f2defa14a19d19f7e4abef9a2647`, exactly matching fork branch
`localai-paged` HEAD `c78e537b5 feat(cuda): trace moe mmq launch shapes`.

Phase 24 extended `paged-current-serving-snapshot.sh` to write the snapshot
hardware report. DGX dry run passed at
`/home/mudler/bench/phase24_hardware_report_dryrun/20260701_052741`; it recorded
`GPU 0: NVIDIA GB10`, driver `580.159.03`, compute capability `12.1`, and
`hardware_class=gb10_or_workstation_blackwell`. This makes future parity
artifacts self-describing: GB10/workstation Blackwell results must not be used
as datacenter-Blackwell parity evidence.

Phase 25 extended the same harness to write `gate_summary.tsv`. The summary was
backfilled on the Phase 20 artifact at
`/home/mudler/bench/phase20_current_snapshot/20260701_050621/gate_summary.tsv`;
it records pre/post MoE md5 `8cb0ce23777bf55f92f63d0292c756b0`, dense md5
`5951a5b4d624ce891e22ab5fca9bc439`, and `MUL_MAT_ID` `806/806` as `ok`.

Phase 26 ran the full audited current-stack snapshot with `hardware.txt`,
pre/post gates, same-session paged and vLLM serving runs, `summary.tsv`, and
`gate_summary.tsv`. Artifact:
`/home/mudler/bench/phase26_audited_snapshot/20260701_053650`. Hardware was
recorded as `hardware_class=gb10_or_workstation_blackwell`, GPU `NVIDIA GB10`,
driver `580.159.03`, compute capability `12.1`. Every compact gate row was
`ok`: MoE md5 `8cb0ce23777bf55f92f63d0292c756b0`, dense md5
`5951a5b4d624ce891e22ab5fca9bc439`, and `MUL_MAT_ID` `806/806`, both before and
after the serving run.

Audited current MoE serving snapshot (`PTOK=128`, `GEN=64`):

| n | paged decode_agg | vLLM decode_agg | paged/vLLM decode | paged agg | vLLM agg | paged/vLLM agg |
|---|------------------|-----------------|-------------------|-----------|----------|----------------|
| 8 | 230.8 | 283.2 | 81.5% | 170.6 | 241.6 | 70.6% |
| 32 | 420.0 | 609.0 | 69.0% | 254.6 | 466.7 | 54.6% |
| 128 | 673.4 | 1025.0 | 65.7% | 324.0 | 656.5 | 49.4% |

Use Phase 26 as the current audit-grade GB10 snapshot. It keeps the Phase 20
verdict intact, but the artifact is more useful for future regressions because
it carries hardware classification and compact pre/post inference gates.

Phase 27 re-profiled the current clean llama.cpp n128 serving path with
`nsys --cuda-graph-trace=node`. Artifact:
`/home/mudler/bench/phase27_graph_node_serving/20260701_055519`. The run matched
Phase 26 throughput closely (`675.5` vs `673.4` decode_agg_tps) and kept gates
green before and after the profile (post retry): MoE md5
`8cb0ce23777bf55f92f63d0292c756b0`, dense md5
`5951a5b4d624ce891e22ab5fca9bc439`, `MUL_MAT_ID` `806/806`. The node-traced
buckets still put the work in `gdn_core` (`29.59%`) and `mmq_nvfp4` (`28.44%`);
helper dispatch remains too small (`mm_ids` `0.61%`, `gather_mmq` `0.37%`,
`argsort_topk` `0.40%`). Do not reopen metadata/helper-only MoE dispatch work on
GB10.

Phase 28 tested the remaining low-conflict NVFP4 grouped-MMQ occupancy knobs.
Artifact: `/home/mudler/bench/phase28_mmq_occupancy/20260701_040450`.
`GGML_CUDA_FP4_MINBLOCKS=2` passed md5/op gates before and after serving
(MoE `8cb0ce23777bf55f92f63d0292c756b0`, dense
`5951a5b4d624ce891e22ab5fca9bc439`, `MUL_MAT_ID` `806/806`) but regressed
n128 same-session decode serving (`705.1 -> 689.9` decode_agg_tps, `0.9784x`).
`GGML_CUDA_FP4_MMQ_Y=64` failed to compile because the NVFP4 writeback
specialization asserts `nwarps*tile_C::I == mmq_y`. Do not promote either knob;
future grouped-MMQ work must be structural kernel work.

Phase 29 added the default-off grouped-MMQ shape trace as patch `0056`.
Artifact: `/home/mudler/bench/phase29_mmq_shape_trace/20260701_042428`.
Fork commit: `20a99518a feat(cuda): trace moe mmq batch shapes`. The helper was
added test-first (`test-cuda-mmq-shape-trace`) and built under CUDA on DGX.
Default-off and `LLAMA_MOE_MMQ_SHAPE_TRACE=4` gates both passed: MoE
`8cb0ce23777bf55f92f63d0292c756b0`, dense
`5951a5b4d624ce891e22ab5fca9bc439`, `MUL_MAT_ID` `806/806`. The trace-enabled
gate emitted exactly four `[LLAMA_MOE_MMQ_SHAPE]` lines. This is evidence-only
instrumentation; it does not close the speed gap.

Phase 30 used patch `0056` for a live n128 serving shape trace. Artifact:
`/home/mudler/bench/phase30_mmq_shape_serving/20260701_043300`. The first 4096
grouped-MMQ calls split into 1200 decode-like calls (`ncols_max <= 128`) and
2896 prefill-like calls. Decode-like calls had density `1-4` and selected
`mmq_x_best` only in `{32,40,48,64}`; prefill-like calls were mostly density
`16` and selected `mmq_x_best=128`. All traced calls had `stream_k=1`. Post-run
gates stayed green: MoE `8cb0ce23777bf55f92f63d0292c756b0`, dense
`5951a5b4d624ce891e22ab5fca9bc439`, `MUL_MAT_ID` `806/806`.

Phase 31 added patch `0057` for default-off grouped-MMQ launch tracing.
Artifact: `/home/mudler/bench/phase31_mmq_launch_trace/20260701_064424`.
Fork commit: `c78e537b5 feat(cuda): trace moe mmq launch shapes`; DGX mirror
commit: `8b75905e9`. The trace adds `[LLAMA_MOE_MMQ_LAUNCH]` lines under
`LLAMA_MOE_MMQ_SHAPE_TRACE=<n>`, recording `ntiles_dst`, `stream_k_blocks`,
tile efficiency, `fixup`, `ntx/nty/ntzw`, and compiled `mmq_x/mmq_y`. Default
off, trace-enabled, and post-serving gates stayed green: MoE
`8cb0ce23777bf55f92f63d0292c756b0`, dense
`5951a5b4d624ce891e22ab5fca9bc439`, `MUL_MAT_ID` `806/806`. The n128 serving
trace showed decode-like `4800/4800` and prefill-like `4920/4920` launch lines
with `fixup=0` and `stream_k_blocks == ntiles_dst`. Do not pursue a
no-fixup/no-stream-k shortcut for this workload; the remaining grouped-MMQ work
is structural small-M kernel work.

Phase 32 added patch `0058` for default-off small-M grouped-MMQ candidate
tracing. Artifact: `/home/mudler/bench/phase32_small_m_classifier/20260701_070127`.
Fork commit: `2a9964d29 feat(cuda): trace moe small-m mmq candidates`; DGX
mirror commit: `024f494d0`. The trace adds `[LLAMA_MOE_MMQ_SMALL_M]` lines
under `LLAMA_MOE_MMQ_SMALL_M_TRACE=<n>` for decode-like low-density grouped-MMQ
MoE calls (`ncols_max <= 128`, density `<=4`, `mmq_x_best <=64`). Default-off,
trace-enabled, and post-serving gates stayed green: MoE
`8cb0ce23777bf55f92f63d0292c756b0`, dense
`5951a5b4d624ce891e22ab5fca9bc439`, `MUL_MAT_ID` `806/806`. The n128 serving
trace found 4096 candidate calls, mostly `mmq_x_best=64` (1800) and `48`
(1096). Phase 33 should A/B a default-off small-M tile policy starting at
`mmq_x=16`.

Phase 33 added patch `0059`, default-off `LLAMA_MOE_SMALL_M_TILE=<n>`, and
rejected the simple smaller-tile policy. Artifact:
`/home/mudler/bench/phase33_small_m_tile_policy/20260701_071136`. Fork commit:
`fbed2abaa feat(cuda): gate moe small-m mmq tile policy`; DGX mirror commit:
`dfd1eaea8`. Default-off, tile16, tile8, and post-serving gates stayed green:
MoE `8cb0ce23777bf55f92f63d0292c756b0`, dense
`5951a5b4d624ce891e22ab5fca9bc439`, `MUL_MAT_ID` `806/806`. Same-session n128
serving rejected both caps: baseline `672.1` decode_agg_tps, tile16 `640.3`
(`0.953x`), tile8 `583.2` (`0.868x`). Do not promote smaller `mmq_x` caps.

Phase 34 added patch `0060`, default-off `LLAMA_MOE_MMID_ROUTE_TRACE=<n>`, to
classify the live `MUL_MAT_ID` dispatch route without changing route behavior.
Artifact: `/home/mudler/bench/phase34_mmid_route_trace/20260701_072737`. Fork
commit: `6c332094c feat(cuda): trace moe mmid routes`; DGX mirror commit:
`34a256d14`. Default-off, trace-enabled, and post-serving gates stayed green:
MoE `8cb0ce23777bf55f92f63d0292c756b0`, dense
`5951a5b4d624ce891e22ab5fca9bc439`, `MUL_MAT_ID` `806/806`. Live n128 serving
with trace cap 4096 found `mmq=2776`, `mmvq=1320`, and `host_sync=0/4096`.
Treat the old current-stack host-sync-fallback concern as refuted for this
workload; the remaining MoE work is grouped-MMQ small-M efficiency or another
measured bucket.

Phase 35 added patch `0061`, default-off `LLAMA_MUL_MAT_ROUTE_TRACE=<n>`, to
classify regular `MUL_MAT` routes for the projection-heavy serving bucket.
Artifact: `/home/mudler/bench/phase35_mul_mat_route_trace/20260701_074359`.
Fork commit: `486c28c63 feat(cuda): trace mul mat routes`; DGX mirror commit:
`18f7ad005`. Default-off, trace-enabled, and post-serving gates stayed green:
MoE `8cb0ce23777bf55f92f63d0292c756b0`, dense
`5951a5b4d624ce891e22ab5fca9bc439`, `MUL_MAT` `1146/1146`, `MUL_MAT_ID`
`806/806`. Live n128 serving with trace cap 8192 found `mat_f=2888`,
`op_cublas=2292`, `mmq=1328`, `vec_q=1214`, `vec_f=470`; BF16 (`type=30`)
was split `mat_f=2485`, `op_cublas=1330`. Next projection work should target
BF16 `mat_f`/`op_cublas` subroute evidence or route policy, not batched cuBLAS.

Phase 36 added patch `0062`, default-off `LLAMA_CUBLAS_ROUTE_TRACE=<n>`, to
classify the generic cuBLAS `MUL_MAT` subroute without changing branch behavior.
Artifact: `/home/mudler/bench/phase36_cublas_route_trace/20260701_081228`.
Fork commit: `38c4ef2e4 feat(cuda): trace cublas routes`; DGX mirror commit:
`e0224393a`. Default-off, trace-enabled, and post-serving gates stayed green:
MoE `8cb0ce23777bf55f92f63d0292c756b0`, dense
`5951a5b4d624ce891e22ab5fca9bc439`, `MUL_MAT` `1146/1146`, `MUL_MAT_ID`
`806/806`. Live n128 serving with trace cap 8192 found `bf16_tc=5681` and
`sgemm=2511`. The next projection phase should explain whether the F32 SGEMM
shapes are expected glue tensors or a missed BF16 route; do not chase NVFP4
cuBLAS or batched cuBLAS for this measured bucket.

Phase 37 added patch `0063`, extending `LLAMA_CUBLAS_ROUTE_TRACE=<n>` with
`src0`, `src1`, and `dst` tensor names. Artifact:
`/home/mudler/bench/phase37_cublas_name_trace/20260701_083227`. Fork commit:
`2d590d770 feat(cuda): trace cublas tensor names`; DGX mirror commit:
`2cbb61969`. Default-off, trace-enabled, and post-serving gates stayed green:
MoE `8cb0ce23777bf55f92f63d0292c756b0`, dense
`5951a5b4d624ce891e22ab5fca9bc439`, `MUL_MAT` `1146/1146`, `MUL_MAT_ID`
`806/806`. Live n128 trace found `bf16_tc=2884`, `sgemm=1212`. The `sgemm`
bucket is `blk.N.ffn_gate_inp.weight -> ffn_moe_logits-N` and
`blk.N.ffn_gate_inp_shexp.weight -> shared_expert_gate-N`; do not force BF16
without first inspecting model-load tensor types and running KL validation.

Phase 38 is the current gate-projection policy checkpoint. Artifact:
`/home/mudler/bench/phase38_gate_baseline/20260701_084410`. Preflight showed
docker `0`, `local-ai-worker` `0`, compute apps `0`, and GB10 driver
`580.159.03`. Fresh baseline gates against the Phase37 build passed: MoE
`8cb0ce23777bf55f92f63d0292c756b0`, dense
`5951a5b4d624ce891e22ab5fca9bc439`, `MUL_MAT` `1146/1146`, `MUL_MAT_ID`
`806/806`. Source comparison found llama.cpp and vLLM both keep router and
shared-expert gate weights unquantized; vLLM's relevant idea is fused F32 gate
weight concatenation, not BF16/NVFP4 routing. Future fused-gate work must be
default-off, preserve F32 semantics, and pass md5/op gates before benchmarking;
if md5 changes, run KL first.

Phase 39 closes the naive fused-gate shortcut. Artifact:
`/home/mudler/bench/phase39_gate_sgemm_profile/phase27_reanalysis`. Re-analysis
of the Phase27 graph-node serving profile showed total kernel time `20.0372s`,
`concat_layout=459.84ms` (`2.29%`, `2250` instances), `cublas_bf16_gemm=1892.81ms`
(`9.45%`), and `cutlass_bf16_gemm=684.01ms` (`3.41%`). Do not implement
graph-time `ggml_concat()` of `ffn_gate_inp.weight` plus
`ffn_gate_inp_shexp.weight`; it risks increasing an existing layout-copy bucket.
The only future fused-gate design worth scoping is a persistent/load-time F32
combined gate weight with output views, default-off until MoE/dense md5,
`MUL_MAT`, `MUL_MAT_ID`, and KL-if-md5-changes gates pass.

Phase 40 closes the tested GB10 max-concurrency C1 shortcut. Artifact:
`/home/mudler/bench/phase40_max_concurrency/20260701_090012`. The snapshot ran
with `PARALLEL=256`, `CTX=262144`, `PTOK=128`, `GEN=64`, `NPL="128 192 256"`,
and `OPS=MUL_MAT,MUL_MAT_ID`. Pre/post gates stayed green: MoE
`8cb0ce23777bf55f92f63d0292c756b0`, dense
`5951a5b4d624ce891e22ab5fca9bc439`, `MUL_MAT` `1146/1146`, `MUL_MAT_ID`
`806/806`. Paged safely served `n=256`, but vLLM also fit and remained faster:
`paged_decode_over_vllm=0.6354`, `paged_agg_over_vllm=0.4721`,
`paged_ttft_over_vllm=2.9401`. Do not claim GB10 parity from higher max
concurrency at this prompt/gen length and `n<=256`; a future C1 retry must push
beyond this tested point and keep the same md5/op gates.

Phase 41 records the low-concurrency counterpart to the Phase40 high-concurrency
check. Artifact:
`/home/mudler/bench/phase41_low_concurrency/20260701_091437`. The snapshot ran
with `PARALLEL=32`, `CTX=32768`, `PTOK=128`, `GEN=64`, `NPL="1 8 32"`, and
`OPS=MUL_MAT,MUL_MAT_ID`. Pre/post gates stayed green: MoE
`8cb0ce23777bf55f92f63d0292c756b0`, dense
`5951a5b4d624ce891e22ab5fca9bc439`, `MUL_MAT` `1146/1146`, `MUL_MAT_ID`
`806/806`. Paged is about `0.75x` vLLM decode at `n=1/8` and `0.665x` at
`n=32`; TTFT is `1.38x`, `3.14x`, and `3.40x` vLLM respectively. Do not reopen
D1 from this result: `0043` already ships grouped-MMQ full-step graph capture
default-on, Phase34 found `host_sync=0/4096`, and S3 is default-off because it
regressed TTFT/end-to-end throughput.

Phase 42 reconciles the target list after parallel read-only review. D1 is
closed on the current GB10 path; GDN low-conflict work is exhausted after
`0046`/`0047` plus the rejected C32/QS-early/Global-Ai32 follow-ups; W4A16/GEMM
micro-tweaks are exhausted after `0033`-`0035` and `0048`-`0050`. It nominated
the Phase38/39 persistent/load-time F32 combined gate projection as the last
small GB10 source candidate.

Phase 43 rejects that gate-fusion candidate as a small shortcut after source
inspection. `ffn_gate_inp.weight` and `ffn_gate_inp_shexp.weight` are separate
GGUF tensors; the Qwen35MoE graph consumes them in separate matmuls; the loader
can create tensors from GGUF metadata or views of existing tensors, but not a
new persistent derived concatenated weight. A correct implementation would need
a general derived-weight allocation/materialization path across mmap, offload,
split buffers, and MTP blocks. Do not implement a Qwen-only loader hack, and do
not fall back to graph-time `ggml_concat()`. After Phase43 there is no remaining
low-conflict GB10 shortcut justified by current evidence; future work is either
a larger kernel/loader design or a hardware-pivot benchmark, still gated by
MoE/dense md5 plus `MUL_MAT`/`MUL_MAT_ID` and KL if md5 changes.

Phase 44 makes the current-stack serving snapshot harness ready for hardware
pivots by parameterizing the vLLM side instead of hardcoding the GB10 defaults.
`paged-current-serving-snapshot.sh` now accepts `VLLM_GPU_MEMORY_UTILIZATION`,
`VLLM_MAX_MODEL_LEN`, `VLLM_MAX_NUM_SEQS`, `VLLM_TENSOR_PARALLEL_SIZE`, and
whitespace-split `VLLM_EXTRA_ARGS`, and prints the resolved values during
`DRY_RUN=1`. This is not a new benchmark and does not change inference code or
gate behavior. Use it when the next parity run targets datacenter Blackwell or
another non-GB10 vLLM serving shape, while keeping `hardware.txt`, pre/post
MoE/dense md5, `MUL_MAT`/`MUL_MAT_ID`, and KL-if-md5-changes as mandatory gates.

Phase 45 records the immediate inference-safety guard after Phase44. Artifact:
`/home/mudler/bench/phase45_inference_gate_guard/20260701_094320`. The DGX
phase36 build passed MoE md5 `8cb0ce23777bf55f92f63d0292c756b0`, dense md5
`5951a5b4d624ce891e22ab5fca9bc439`, `MUL_MAT` `1146/1146`, and `MUL_MAT_ID`
`806/806`. Docker, `local-ai-worker`, and GPU compute preflight were all zero
before and after the run.

Phase 46 removes the last hardcoded `q36` served-model name from the audited
serving snapshot harness. Set `SERVED_MODEL_NAME` to drive vLLM
`--served-model-name`, the vLLM readiness check, and h2h `--model` on both
engines. DGX dry run:
`/home/mudler/bench/phase46_served_model_name_dryrun/20260701_094849`, with
`SERVED_MODEL_NAME=dense-q36` printed during `DRY_RUN=1`. This is harness-only
hardware-pivot readiness, not a throughput result.

Phase 47 attempted the first dense serving snapshot using the Phase46 override.
Dry-run artifact:
`/home/mudler/bench/phase47_dense_serving_dryrun/20260701_095141`; incomplete
full artifact: `/home/mudler/bench/phase47_dense_serving/20260701_095151`.
Pre-gates were green and the paged dense arm completed through `n=128`, but the
artifact is not a dense parity result because vLLM produced no result JSONs.
Root cause: dense vLLM startup exceeded the old fixed readiness budget, and the
cleanup path could wait indefinitely on the server PID after `SIGTERM`.

Phase 48 hardens the serving snapshot harness for that failure mode. It adds
`LLAMA_READY_ATTEMPTS` and `VLLM_READY_ATTEMPTS`, bounds HTTP readiness probes
with `curl --max-time 2`, and uses bounded server cleanup that escalates from
`SIGTERM` to `SIGKILL`. Dry-run artifact:
`/home/mudler/bench/phase48_readiness_harness_dryrun/20260701_100533`, with
`VLLM_READY_ATTEMPTS=700` printed and clean DGX preflight.

Phase 47 retry completed after Phase48. Artifact:
`/home/mudler/bench/phase47_dense_serving_retry/20260701_100811`. Pre/post
gates were green: MoE `8cb0ce23777bf55f92f63d0292c756b0`, dense
`5951a5b4d624ce891e22ab5fca9bc439`, `MUL_MAT` `1146/1146`, `MUL_MAT_ID`
`806/806`. Dense paged decode beats vLLM at low concurrency (`1.3434x` at `n=1`,
`1.1560x` at `n=8`) but falls behind at `n=32/128` (`0.9036x`, `0.7912x`), and
TTFT remains `1.87x` to `4.05x` vLLM. This does not change the GB10 conclusion.

Phase 49 removes vLLM log noise from harness-owned environment variables. The
`vllm serve` child now unsets `VLLM_MODEL`, `VLLM_BIN`,
`VLLM_READY_ATTEMPTS`, `VLLM_GPU_MEMORY_UTILIZATION`, `VLLM_MAX_MODEL_LEN`,
`VLLM_MAX_NUM_SEQS`, `VLLM_TENSOR_PARALLEL_SIZE`, and `VLLM_EXTRA_ARGS` while
preserving intentional vLLM runtime variables such as `VLLM_LOGGING_LEVEL`. Dry
run: `/home/mudler/bench/phase49_vllm_env_hygiene_dryrun/20260701_102138`.

Phase 50 resolves the dense high-N decode-accounting question with a graph-node
difference-method profile. Artifact:
`/home/mudler/bench/phase50_dense_true_decode/20260701_103120`. Pre/post
inference gates on the profiled `build-cuda` binary stayed green: MoE
`8cb0ce23777bf55f92f63d0292c756b0`, dense
`5951a5b4d624ce891e22ab5fca9bc439`, `MUL_MAT` `1146/1146`, and `MUL_MAT_ID`
`806/806`. Dense `npl=128`, `npp=128` true decode is `383.66 t/s` for paged and
`435.00 t/s` for vLLM, ratio `0.8820`. This means Phase47's `0.7912` h2h
decode ratio and `0.5071` aggregate ratio include scheduler/admission and
prefill-overlap/accounting effects beyond the real GPU-steady decode gap. Next
GB10 code work should instrument batch composition/admission in
`server_context::pre_decode()` before attempting another kernel shortcut.

Phase 51 implements that admission trace in the llama.cpp fork. Local fork
commit: `c6cb8460e feat(server): trace serving admission batches`. The trace is
default-off behind `LLAMA_SERVING_TRACE=1`, adds a small unit-tested accumulator,
and records aggregate `pre_decode()` scheduler shape: decode tokens, prompt
tokens admitted, waiting prompt slots, started/continued prompt slots,
decode-only steps, `n_batch`, `n_ubatch`, `prefill_budget_step`, and
`prefill_cap_per_slot`. DGX artifact:
`/home/mudler/bench/phase51_serving_admission_trace/20260701_110130`. The
patched `build-cuda` CTest passed and inference gates stayed green: MoE
`8cb0ce23777bf55f92f63d0292c756b0`, dense
`5951a5b4d624ce891e22ab5fca9bc439`, `MUL_MAT` `1146/1146`, `MUL_MAT_ID`
`806/806`. Push and LocalAI patch-series regeneration are still pending because
push requires explicit approval.

Phase 52 uses the Phase51 trace on DGX for dense `n=128`, `ptok=128`, `gen=64`.
Artifact: `/home/mudler/bench/phase52_dense_admission_trace/20260701_111017`.
Pre/post md5 and op gates stayed green. The clean traced h2h row was
`decode_agg_tps=360.5`, `prefill_tps=629.5`, `ttft_mean_ms=23171.5`, wall
`58.921s`. The admission trace reported `steps=76`, `decode_only_steps=0`,
`decode_tokens=8064`, `prompt_tokens=22785`, `max_waiting_prompt_slots=35`,
`started_prompt_slots=128`, `continued_prompt_slots=139`,
`prefill_budget_step=0`, and `prefill_cap_per_slot=0`. The prompt token count
matches h2h exactly, so this is the target request. The next GB10 lever should
be a default-off scheduler/admission A/B or a per-step histogram trace, not an
immediate GDN/GEMM rewrite.

Phase 53 tested the existing runtime admission-budget knobs instead of adding
new code. Artifact:
`/home/mudler/bench/phase53_dense_admission_budget_sweep/20260701_111915`.
Pre/post gates stayed green. Dense `n=128` results: default Phase52 `agg=139.0`,
`decode_agg=360.5`, `prefill=629.5`, `TTFT=23171.5ms`, wall `58.921s`;
`T=1536 cap=512` `agg=134.4`, `decode_agg=376.7`, `prefill=607.0`,
`TTFT=22263.7ms`, wall `60.968s`; `T=1024 cap=512` `agg=130.0`,
`decode_agg=392.4`, `prefill=565.2`, `TTFT=23234.3ms`, wall `63.003s`.
Decision: simple budget shrinkage is rejected. It raises h2h decode-agg while
lowering aggregate/prefill throughput, and it does not materially solve TTFT.
Next scheduler work should be per-step histograms or a targeted first-token
admission policy.

Phase 54 through Phase 59 tested that targeted scheduler path. The fork commits
are still local-only and default-off:

- `c6cb8460e feat(server): trace serving admission batches`
- `bd7b2e952 feat(server): add admission trace histograms`
- `8a97629a4 feat(server): add TTFT prefill-first scheduler mode`
- `3b6ab5fa8 feat(server): cap TTFT prefill-first decode deferral`
- `8759213e3 feat(server): gate TTFT defer by prompt backlog`

Phase59 is the current verdict. Artifact:
`/home/mudler/bench/phase59_moe_min32_repeat_vllm/20260701_123147`. Pre/post
llama gates stayed green: MoE `8cb0ce23777bf55f92f63d0292c756b0`, dense
`5951a5b4d624ce891e22ab5fca9bc439`, `MUL_MAT` `1146/1146`, `MUL_MAT_ID`
`806/806`. MoE `n=128`, `ptok=128`, `gen=64` repeated the Phase58 min32 signal:
llama default `agg=336.6`, `TTFT=7798.5ms`, wall `24.334s`; llama min32
`agg=336.9`, `TTFT=7167.8ms`, wall `24.316s`. Matching vLLM was still
`agg=601.3`, `TTFT=2968.1ms`, wall `13.563s`.

Decision: keep `LLAMA_TTFT_PREFILL_FIRST=1` and
`LLAMA_TTFT_PREFILL_FIRST_MIN_WAITING=32` as an opt-in llama.cpp latency/QoS
knob. It does not prove vLLM parity progress by itself. Do not default it until
more workload coverage exists, and do not regenerate LocalAI patches until the
fork commits are pushed with explicit approval.

Phase 60 re-profiled the current W4A16 grouped MoE prefill path to check whether
there was still a low-conflict W4A16 shortcut after Phase1-5. Artifact:
`/home/mudler/bench/phase60_w4a16_current_profile/20260701_104915`. Pre/post
gates stayed green: MoE `8cb0ce23777bf55f92f63d0292c756b0`, dense
`5951a5b4d624ce891e22ab5fca9bc439`, `MUL_MAT` `1146/1146`, `MUL_MAT_ID`
`806/806`. Default FP4-MMQ S_PP was `2327.69` at `npp=512` and `2423.20` at
`npp=2048`; forced W4A16 was `1451.00` and `1482.76`, only `0.623x` and
`0.612x` of default. The `npp=512` profile showed W4A16 still dominated by
`w4a16_grouped_kernel` (`4.142s`, `42.5%`) plus sorted activation gathers
(`1.094s`, `11.2%`), while the cast kernel was only `0.517s` (`5.3%`).

Decision: do not add another small W4A16 metadata/body/cast patch. Future W4A16
work needs a larger redesign that improves the grouped kernel body and removes
or fuses sorted activation movement. Near-term GB10 parity work should return to
broader prefill/GDN/MoE design or hardware-pivot benchmarking.

Phase61 is scoped as that larger W4A16 kill-gate, not as a committed code
change: `docs/superpowers/plans/2026-07-01-w4a16-direct-activation-phase61.md`.
It proposes a default-off `LLAMA_W4A16_DIRECT_A=1` experiment that consumes the
original activation tensor plus the existing `ids_to_sorted` map directly,
removing Phase60's sorted activation gather and separate cast kernels before any
grouped-kernel body rewrite. Keep it only if it improves forced W4A16 S_PP by at
least `+12%` and reaches at least `0.75x` default FP4-MMQ; otherwise reject and
do not continue W4A16 body tuning.

Phase61 result: rejected. The direct-A kernel passed correctness after matching
`get_rows_cuda` flat-row addressing (`MUL_MAT_ID` `806/806`; forced/direct-A
MoE transcript md5 both `07db32c2bcb78d17a43ed18bc22705cd`) and default gates
remained green (`8cb0ce23`, `5951a5b4`, `MUL_MAT` `1146/1146`, `MUL_MAT_ID`
`806/806`). But direct-A only improved forced W4A16 S_PP `1471.05 -> 1566.30`
at `npp=512` and `1502.46 -> 1605.82` at `npp=2048` (`+6.5%` / `+6.9%`), still
just `0.67x` / `0.66x` of default FP4-MMQ. The direct kernel diff was not
committed; only the safe policy/routing stub remains in the fork. Do not pursue
more W4A16 body tuning on GB10 as the next parity lever.

---

## 5. METHODOLOGY LESSONS (so you do not repeat the mistakes)

1. **Profile, don't assume. The analysts were wrong 4 times.** Every one was caught only by an in-backend A/B or a corrected profile:
   - **GDN-scalar grep** (assumed the scan was scalar/serial from reading source) - wrong, retired by the tensor-core port.
   - **dense-cuBLAS reroute** (assumed dequant->bf16 would win) - wrong, -31% to -62%.
   - **occupancy** (assumed blocks/SM was the GDN bound) - wrong, 1844 vs 1814 within noise.
   - **projection-regime** (assumed FP8/NVFP4 projections were a big lever) - wrong, projections are ~12% of the decode stream at high N.
   **In-backend A/B is the only truth.** A standalone PoC win (0034) is not a result.
2. **Per-kernel us/tok overstates end-to-end S_PP/S_TG.** A kernel that is X% faster in isolation does not move throughput X%; always confirm against the end-to-end batched-bench / serving number.
3. **The CUDA-graph-trace decode artifact (the big one).** Decode is a replayed graph; nsys without `--cuda-graph-trace=node` collapses it and lies. This single trap produced the wrong "host-bound / 159 us/tok / 56%" story across multiple analyses. Always graph-node-trace + difference method (section 3.4).
4. **Beware GPU contention skewing absolutes.** The box runs concurrent quant/repack/finetune jobs. Gate on idle GPU + free lock; prefer the same-session both-engine harness so both numbers move together.
5. **The vLLM server number is inflated ~8 pt vs its true GPU-steady.** vLLM's chunked-prefill-overlap inflates its own server-measured decode window (1177 server vs 1078 true GPU-steady). Compare GPU-steady to GPU-steady, or you will chase a phantom gap. The reconciliation chain that must sum: vLLM server 1177 (100%) -> vLLM true GPU-steady 1078 (92%) -> llama GPU-steady 924 (78.5% of 1177, = 86% of 1078) -> llama server 718 (60.7%, the S3-recoverable serving overhead).

---

## 6. THE THREE FORWARD DIRECTIONS

### (a) Close / ship the record (lowest effort, do this first)
The investigation is closed for GB10 shortcuts, and the closeout chores below
are now done:

- patch `0044` is tracked in the LocalAI series;
- the Makefile pin `0ed235ea2c17a19fc8238668653946721ed136fd` is the
  authoritative paged pin;
- Phase 20 re-ran the current-stack serving snapshot on the clean mirror;
- Phase 22 re-verified the patch-series mirror invariant after `0055`.

For future release checks, run `paged-inference-gates.sh` and
`paged-current-serving-snapshot.sh` from the LocalAI backend tree. The inference
gate now defaults to both `MUL_MAT` and `MUL_MAT_ID`; set `OPS=` only for a
focused diagnostic run.

### (b) Datacenter-Blackwell pivot (THE real parity path)
The thesis: every vLLM advantage that wins on GB10 is a kernel that is **broken or capped on consumer Blackwell** and **inverts on datacenter Blackwell** (B200): FLA blocked-solve GDN, Marlin/CUTLASS grouped FP4, HBM-tuned full-cudagraph decode, native tcgen05/TMEM. ~8 TB/s HBM lifts the LPDDR5x GDN bandwidth floor ~30x. Concrete first steps:
1. Acquire a B200 (or equivalent HBM tcgen05 part). Reproduce the **both-engine same-session harness** there (`combined_definitive.sh` discipline): build the stock and paged binaries, build vLLM 0.23.0+, run MoE + dense prefill + serving for both engines.
2. Re-measure the FP4 path: on B200, native CUTLASS NVFP4 grouped-GEMM should work (the CUTLASS #3096 / TMA-WS failure is consumer-Blackwell-specific). Confirm whether vLLM now runs **native FP4** instead of Marlin W4A16. If so, the 4.1 GEMM track must be re-evaluated from scratch (it was rejected on a GB10-specific ceiling).
3. Re-take the decode profile with `--cuda-graph-trace=node`; the GDN scan that floors at 273 GB/s on GB10 should no longer dominate at HBM bandwidth - re-derive the per-token decomposition before choosing any lever.

### (c) Multi-week persistent-Marlin decode kernel (decode-only, low-EV, CANNOT reach parity)
Only pursue if (a)+(b) are not options and someone explicitly wants the residual decode gap closed on GB10. It targets the ~14 pt GPU-steady decode gap (vLLM's fused-Marlin MoE persistent-tiling + single Triton elementwise). Concrete first steps:
1. Re-confirm the ceiling first: our own ggml Marlin port already lost -19.6% at decode (4.3), so the bar is "beat that and beat FP4-MMQ at the decode BW floor".
2. Prototype the persistent-tiling grouped-FP4 MoE kernel **standalone**, then prove it **in-backend** (a PoC win is not a result, per 0034). It must live inside a single-stream CUDA graph or bring its own multi-stream overlap.
3. Bound the upside honestly: this is **decode-only ~4-14%** and **does nothing for the prefill floor (36-43%)**, so it does not reach parity. Record the verdict either way.

---

## 7. KEY FILE / ARTIFACT INDEX

### Fork (canonical source of truth)
- Local canonical fork: `/home/mudler/_git/llama.cpp`, branch **`localai-paged`**, HEAD `2d590d770` ("trace cublas tensor names", patch `0063`).
- DGX current clean mirror/build tree: `dgx:~/llama-phase6-source`, HEAD `2cbb61969` with the Phase 37 cuBLAS tensor-name trace patch applied and committed; Phase 20/26/27 artifacts still record their historical source hashes.
- Historical DGX dev tree: `dgx:~/llama-paged-dev`, branch **`paged`**, HEAD `a7d439e8ce6990eb09721223c975da4e49d8d136` ("GDN CONFIG C (M8) - bf16 Kc/Qc"). It is an old experimental tree and must not be treated as canonical.

### LocalAI worktree
- Path: `/home/mudler/_git/LocalAI/.claude/worktrees/feat+paged-attention`, branch `worktree-feat+paged-attention` (currently 246 ahead, 31 behind `origin/master`; recompute before reporting).
- Backend dir: `backend/cpp/llama-cpp-localai-paged/` (`Makefile` thin wrapper, `package.sh`, `run.sh`, `README.md` ~44 KB canonical, `docs/`, `patches/paged/`).
- `docs/`: `VLLM_PARITY_FINAL.md` (authoritative record), `VLLM_PARITY_LEVER_MAP.md` (working brainstorm, profile-validated section), `DECODE_SERVING_SCOPE.md`, `PREFILL_GEMM_SCOPE.md`, `PREFILL_GEMM_RESULTS.md`, `TENSORCORE_GDN_SCOPE.md`, `TENSORCORE_GDN_BUILD_PLAN.md`, `ACCELERATOR_PORTING_SCOPE.md`, `UPSTREAM_LAYER2_SCOPE.md`, `LOCALAI_LLAMACPP_BACKEND_PLAN.md`, `PAGED_BITEXACT_NOTE.md`, `PATCH_MAINTENANCE.md`, `final_benchmark.csv`, `paged-burst-bench.cpp`, `paged-reclaim-unit.cpp`, 3 PNGs, and this `PARITY_HANDOFF.md`.
- `patches/paged/`: **54** `.patch` files spanning 0001-0063 with intentional gaps (missing 0005, 0026 [dropped ssm_bf16_tau], 0027, 0032, 0036-0039, 0045). Core paged-KV 0001-0012; decode-first scheduler 0013/0016; serving graph reuse 0040/0041; prefill fusions 0042/0044; SSM/GDN decode 0018-0022/0028; MoE NVFP4 quant 0023/0025/0043; FP4-MMA/Marlin scaffolds 0033/0034/0035 (default-off); GDN tensor-core prefill 0031 -> 0046 (geometry gate) -> 0047 (f32-only M5, default-on under paged KV); W4A16 packed metadata/shape/padding is 0048-0050; MoE safety tests are 0051-0053; MTP backend-sampling safety is 0054; speculative shape trace is 0055; MoE MMQ selector/launch/candidate/tile-policy/route instrumentation is 0056-0060; regular MUL_MAT route instrumentation is 0061; cuBLAS route instrumentation is 0062-0063.

### Bench artifacts (DGX)
- `~/bench/COMBINED_DEFINITIVE.txt` (+ `.log`, `.done`, `combined_definitive.sh`, `combined_definitive.out`) - historical same-session both-engine run.
- `~/bench/phase20_current_snapshot/20260701_050621` - current clean-stack paged-vs-vLLM MoE serving snapshot.
- `~/bench/phase21_harness_dryrun/20260701_051757` - current snapshot harness dry-run artifact.
- `~/bench/phase24_hardware_report_dryrun/20260701_052741` - current snapshot harness dry run proving `hardware.txt` captures the DGX as `hardware_class=gb10_or_workstation_blackwell`.
- `~/bench/phase25_gate_summary_dryrun/20260701_053353` - dry run after adding `gate_summary.tsv` support; normal dry-run still writes `hardware.txt` and does not emit a gate summary before gates exist.
- `~/bench/phase26_audited_snapshot/20260701_053650` - current audit-grade full paged-vs-vLLM MoE serving snapshot with `hardware.txt`, pre/post gates, `summary.tsv`, and `gate_summary.tsv`.
- `~/bench/phase27_graph_node_serving/20260701_055519` - current clean llama.cpp n128 serving profile captured with `--cuda-graph-trace=node`, pre/post retry gates green.
- `~/bench/phase28_mmq_occupancy/20260701_040450` - NVFP4 MMQ occupancy build-knob A/B; `MINBLOCKS=2` gate-safe but serving-regressed, `MMQ_Y=64` compile-rejected.
- `~/bench/phase29_mmq_shape_trace/20260701_042428` - default-off MoE MMQ shape trace patch `0056`; CUDA build plus default/trace md5 gates green.
- `~/bench/phase30_mmq_shape_serving/20260701_043300` - live n128 serving MMQ shape distribution from patch `0056`; post-run md5/op gates green.
- `~/bench/phase31_mmq_launch_trace/20260701_064424` - default-off MoE MMQ launch trace patch `0057`; default/trace/post-serving md5 gates green; n128 launch trace rejects stream-k/fixup shortcut (`fixup=0`, `stream_k_blocks == ntiles_dst`).
- `~/bench/phase32_small_m_classifier/20260701_070127` - default-off MoE MMQ small-M classifier patch `0058`; default/trace/post-serving md5 gates green; n128 trace found 4096 candidate calls.
- `~/bench/phase33_small_m_tile_policy/20260701_071136` - default-off MoE MMQ small-M tile policy patch `0059`; tile16/tile8 md5/op safe but both slower in n128 serving.
- `~/bench/phase34_mmid_route_trace/20260701_072737` - default-off MoE MMID route trace patch `0060`; default/trace/post-serving md5 gates green; n128 route trace found `mmq=2776`, `mmvq=1320`, `host_sync=0/4096`.
- `~/bench/phase35_mul_mat_route_trace/20260701_074359` - default-off regular MUL_MAT route trace patch `0061`; default/trace/post-serving md5 gates green; n128 route trace found BF16 `mat_f=2485`, `op_cublas=1330`.
- `~/bench/phase36_cublas_route_trace/20260701_081228` - default-off cuBLAS subroute trace patch `0062`; default/trace/post-serving md5 and op gates green; n128 route trace found `bf16_tc=5681`, `sgemm=2511`.
- `~/bench/phase37_cublas_name_trace/20260701_083227` - cuBLAS tensor-name trace patch `0063`; default/trace/post-serving md5 and op gates green; n128 trace identified `sgemm` as MoE gate logits and shared-expert gate projections.
- `~/bench/phase38_gate_baseline/20260701_084410` - current Phase37 build baseline before gate-projection policy work; docker/local-ai-worker/GPU idle preflight green; MoE/dense md5 green; `MUL_MAT` `1146/1146`; `MUL_MAT_ID` `806/806`.
- `~/bench/phase39_gate_sgemm_profile/20260701_085211` - short completion profile, diagnostic only because `-n 32` is not a canonical md5 gate; useful for confirming graph-time concat is a real kernel path.
- `~/bench/phase39_gate_sgemm_profile/phase27_reanalysis` - Phase27 serving profile re-analysis used to reject graph-time fused gate weight concat; `concat_layout=459.84ms` (`2.29%`) in the serving kernel window.
- `~/bench/phase40_max_concurrency/20260701_090012` - max-concurrency C1 check at `NPL=128/192/256`, `PTOK=128`, `GEN=64`, `PARALLEL=256`, `CTX=262144`; pre/post MoE/dense md5 and `MUL_MAT`/`MUL_MAT_ID` gates green, but vLLM also fit at `n=256` and stayed ahead (`paged_decode_over_vllm=0.6354`, `paged_agg_over_vllm=0.4721`).
- `~/bench/phase41_low_concurrency/20260701_091437` - low-concurrency serving check at `NPL=1/8/32`, `PTOK=128`, `GEN=64`, `PARALLEL=32`, `CTX=32768`; pre/post MoE/dense md5 and `MUL_MAT`/`MUL_MAT_ID` gates green; paged is `0.7493`, `0.7518`, and `0.6649` of vLLM decode at `n=1/8/32`, with TTFT still much worse by `n=8/32`; does not reopen D1.
- `~/bench/phase44_hardware_pivot_harness_dryrun/20260701_094038` - harness-only dry-run artifact proving the vLLM serving config overrides are printed and preflighted before any server starts.
- `~/bench/phase45_inference_gate_guard/20260701_094320` - post-Phase44 inference guard; MoE/dense md5 and `MUL_MAT`/`MUL_MAT_ID` backend-op gates green.
- `~/bench/phase46_served_model_name_dryrun/20260701_094849` - harness-only dry-run artifact proving `SERVED_MODEL_NAME` is printed and preflighted before any server starts.
- `~/bench/phase47_dense_serving_dryrun/20260701_095141` - dense serving dry-run with `SERVED_MODEL_NAME=dense-q36`.
- `~/bench/phase47_dense_serving/20260701_095151` - incomplete dense serving attempt; pre-gates and paged arm completed, vLLM did not produce result JSONs under the old readiness budget.
- `~/bench/phase48_readiness_harness_dryrun/20260701_100533` - harness dry-run proving configurable readiness budgets and clean preflight before retrying dense serving.
- `~/bench/phase47_dense_serving_retry/20260701_100811` - completed dense serving snapshot after Phase48; pre/post md5 and op gates green; paged low-N decode ahead, high-N aggregate and TTFT behind.
- `~/bench/phase49_vllm_env_hygiene_dryrun/20260701_102138` - harness dry-run after scrubbing harness-owned `VLLM_*` variables from the `vllm serve` child environment.
- `~/bench/phase50_dense_true_decode/20260701_103120` - dense graph-node difference-method profile at `npl=128`, `npp=128`; `build-cuda` pre/post md5 and op gates green; true decode paged `383.66 t/s`, vLLM `435.00 t/s`, ratio `0.8820`, pointing next at serving admission/scheduler tracing.
- `~/bench/phase51_serving_admission_trace/20260701_110130` - default-off `LLAMA_SERVING_TRACE=1` fork commit `c6cb8460e`; DGX patched `build-cuda` CTest and md5/op gates green; push and LocalAI patch-series mirror pending approval.
- `~/bench/phase52_dense_admission_trace/20260701_111017` - clean dense `n=128` admission trace; pre/post gates green; `decode_only_steps=0`, `prompt_tokens=22785`, `max_waiting_prompt_slots=35`; next lever is scheduler/admission A/B or per-step histogram trace.
- `~/bench/phase53_dense_admission_budget_sweep/20260701_111915` - runtime sweep of `LLAMA_MAX_BATCH_TOKENS=1536/1024` with `LLAMA_PREFILL_CAP=512`; pre/post gates green; simple budget shrinkage rejected because aggregate/prefill throughput regressed and TTFT did not materially improve.
- Per-engine logs `~/bench/COMBINED_{paged,vllm}_{MOE,DENSE}_server.log`; `~/bench/BENCHMARK_PROGRESS.md`.
- Graph-node-traced high-N profiles: `~/highN_prof2/*.nsys-rep` (paged npl=256), `~/highN_vllm/*.nsys-rep` (vLLM), 2026-06-30.
- A/B dirs: `~/bench/marlin_gate/`, `~/bench/gdn_p1_ab/`.

### Recent context commits
- `6edbb56b0` "docs(paged): definitive vLLM-parity final-state record (GB10, CLOSED)" - adds `VLLM_PARITY_FINAL.md`.
- `baf102524` "docs(paged): correct decode-serving record to ~86% GPU-steady parity (graph-node-traced)" - the ~56% -> ~86% correction.
- `bd100dd20` "fix(paged): repair the patch series, sync to the fork branch" - dropped dev-tree 0044/0045, added f32-only M5 as 0047.
- `b028c81ed` "docs(paged): record padded/fixed-slot decode shape as tested-and-rejected".

### Discrepancies to flag / resolve (carried verbatim from the gather, including UNVERIFIED labels)
1. **Pin prose reconciled in this worktree.** Makefile line 52 `LLAMA_VERSION?=0ed235ea2c17a19fc8238668653946721ed136fd` is authoritative and matches the local fork merge-base. Hard rule: the paged pin must equal the stock `llama-cpp` pin (shared `grpc-server.cpp`); a bump to `c299a92c` once broke the grpc-server link despite being bit-exact and was reverted. Trust the Makefile when building.
2. **Current fork/mirror are clean and verified.** Local fork HEAD is `2d590d770`, DGX clean mirror HEAD is `2cbb61969`, and Phase 37 should be treated as the current patch-series tip. The old `llama-paged-dev` tree is historical only.
3. **Worktree patch series is tracked through 0063.** The only expected unrelated untracked path in this worktree is `.claude/`.
4. **`sm_121a` is not in the worktree build files** - it lives only in the DGX experimental build scripts (`gdn_cc.sh`, `gdn_bv_build.sh`, `paged-build.sh`); mainline uses arch `121`. **UNVERIFIED** whether the shipped CI Dockerfile build path injects `121a` for the FP4-MMA kernels (`Dockerfile.llama-cpp-localai-paged` does not hardcode a CUDA arch).
5. **The `0921716...` paged-MoE md5 open item.** `COMBINED_DEFINITIVE.txt` records `PAGED_GATE_MD5=0921716cd0582b5d15af8c362b811d00` for MoE, but a full doc/patch/`git log -S` grep of the worktree found **no** occurrence of `0921716...` in any committed source; the committed canonical paged-MoE gate is `8cb0ce23`. Treat this as **unreconciled**: the documented, KL-validated paged-MoE gate remains `8cb0ce23`, and any paged-MoE divergence (including `0921716`) must be KL-validated against the f16 reference before being accepted as benign, never on assertion alone. The `0921716` value is **UNVERIFIED** as a sanctioned gate; do not adopt it as canonical without re-running the KL gate. The **dense** run is symmetric: `COMBINED_DEFINITIVE.txt` records `PAGED_GATE_MD5=ecfe924dee6c5622c149f419ff2a6481` for dense, which likewise differs from the canonical dense gate `5951a5b4`. Both CDEF `PAGED_GATE_MD5` values come from the `combined_definitive.sh` harness's own gate command, NOT the canonical bit-exact gate command in section 3.3, which is why they diverge from the committed `8cb0ce23` / `5951a5b4`; neither is a sanctioned gate and both must be KL-validated before being treated as benign.

---

## 8. PHASE63 RESULT: PREFILL BUCKET ATTRIBUTION

Phase63 is complete as a measurement-only no-go. The plan is
`docs/superpowers/plans/2026-07-01-prefill-bucket-attribution-phase63.md`; the
DGX artifact is `/home/mudler/bench/phase63_prefill_bucket/20260701_140127`.

Pre/post gates stayed green:

- MoE md5 `8cb0ce23777bf55f92f63d0292c756b0`;
- dense md5 `5951a5b4d624ce891e22ab5fca9bc439`;
- `MUL_MAT` `1146/1146`;
- `MUL_MAT_ID` `806/806`.

The candidate paged FlashAttention mask/block-table cleanup is rejected for now:
llama.cpp FA is only `0.71%` at `npp=512` and `1.18%` at `npp=2048`; the
`npp=2048` cross-engine FA delta is about `1.7 us/tok`, not the `15 us/tok`
needed to fund source work. No llama.cpp source files were modified.

*Status: Phase63 closed. `VLLM_PARITY_FINAL.md` remains the GB10 shortcut record;
the remaining measured buckets are still MoE/FFN GEMM, GDN, bf16 projections,
layout copies, and activation quantization.*

## 9. PHASE64 RESULT: LAYOUT TRACE

Phase64 added default-off layout attribution in the llama.cpp fork:
`fa944bb5f feat(cuda): trace layout tensor names`. The env gate is
`LLAMA_LAYOUT_TRACE=<n>`. It traces CUDA `GET_ROWS`, `CPY`, `CONT`, `DUP`, and
`CONCAT` runtime dispatch with tensor names, types, shapes, and contiguity flags.

DGX artifact: `/home/mudler/bench/phase64_layout_trace/20260701_142519`.
Patched build gates stayed green: MoE md5 `8cb0ce23`, dense md5 `5951a5b4`,
`MUL_MAT 1146/1146`, `MUL_MAT_ID 806/806`.

Trace result at MoE `npp=512`, `ntg=4`, `npl=32`:

- `get_rows`: `7268`
- `cpy`: `2008`
- `cont`: `1734`
- `concat`: `990`

The named layout sources are GDN conv-state gather/concat/update
(`cache_r_lN`, `conv_states_reshaped-N`, `qkv_mixed_transposed-N`,
`conv_input-N`, `conv_state_update-N`), MoE top-k fan-in gathers
(`ffn_moe_probs-N`, `ffn_moe_topk-N`, `ffn_moe_weights-N`), and paged-attention
mask/KV reshape/copy paths. This does not fund a clean layout optimization yet;
it gives Phase65 the exact names needed to either remove one repeated chain or
reject it with evidence.

## 10. PHASE65 RESULT: QUANT TRACE

Phase65 added default-off activation-quant route attribution in the llama.cpp
fork: `afc2c7030 feat(cuda): trace activation quant routes`. The env gate is
`LLAMA_QUANT_TRACE=<n>`. DGX mirror commit: `7863194bd`.

DGX artifact: `/home/mudler/bench/phase65_quant_trace/20260701_143729`.
Patched build gates stayed green: MoE md5 `8cb0ce23`, dense md5 `5951a5b4`,
`MUL_MAT 1146/1146`, `MUL_MAT_ID 806/806`.

Trace result at MoE `npp=512`, `ntg=4`, `npl=32`:

- `mmq_dense`: `4444`
- `mmq_moe_dedup_unique`: `2960`
- `mmq_moe_gather`: `2960`
- `mmq_moe_flat`: `1480`

The dominant default-path shapes are MoE gate/up expert activation quant
deduplication (`K=2048`, `rows=512`) followed by gather to expert-token rows
(`rows=4096`), shared-expert dense gate/up quantization (`K=2048`, `rows=512`),
MoE down expert flat quantization (`K=512`, `rows=4096`), and shared-expert down
quantization (`K=512`, `rows=512`). This confirms the activation-quant bucket is
concentrated in named MoE/shared-expert FFN paths, but it does not prove whether
`gather_mmq_fp4` is material or just a cheap cost of the existing dedup win.
Phase66 should time `quantize_mmq_nvfp4` versus `gather_mmq_fp4` with nsys/NVTX
before funding any behavior-changing source patch.

## 11. PHASE66 RESULT: QUANT KERNEL TIMING

Phase66 timed the Phase65 candidate kernels directly with Nsight Systems.
Artifact: `/home/mudler/bench/phase66_quant_kernel_timing/20260701_144256`.
Profile: `quant_npp512.nsys-rep`; summary:
`quant_npp512_kern_sum_cuda_gpu_kern_sum.csv`.

Shape: MoE `npp=512`, `ntg=4`, `npl=32`. Total GPU kernel time:
`7108388986 ns`.

| kernel | time | instances | share |
|--------|-----:|----------:|------:|
| `quantize_mmq_nvfp4` | `317205504 ns` | `8884` | `4.46%` |
| `gather_mmq_fp4` | `45374880 ns` | `2960` | `0.64%` |
| combined | `362580384 ns` | - | `5.10%` |

Decision: reject a Phase66 gather/quant source patch. The gather is too small
to target, and quantize plus gather is below the `8%` source-funding threshold.
Do not reopen W4A16/no-activation-quant from this evidence; that larger rewrite
was already rejected in earlier phases.

## 12. PHASE67 RESULT: BF16 CUBLAS F32 OUTPUT

Phase67 added a default-off BF16 projection shortcut in the llama.cpp fork:
`ea0875d14 feat(cuda): gate BF16 cuBLAS F32 output`. The env gate is
`LLAMA_BF16_CUBLAS_F32_OUT=1`. DGX mirror commit: `14fd69f1e`.

DGX artifact: `/home/mudler/bench/phase67_bf16_f32_out/20260701_144909`.
Default and opt-in gates stayed green: MoE md5 `8cb0ce23`, dense md5
`5951a5b4`, `MUL_MAT 1146/1146`.

Same-window MoE prefill A/B:

| npp | default S_PP | opt-in S_PP | change |
|-----|-------------:|------------:|-------:|
| `512` | `2347.41` | `2402.34` | `+2.34%` |
| `2048` | `2440.18` | `2456.54` | `+0.67%` |

The opt-in `npp=512` profile removed the BF16-to-F32 conversion row:
`convert_unary<__nv_bfloat16, float>` became `0 ns`, `0` instances. Keep this
as default-off for now. It is correctness-clean and measurable, but the win is
small and needs dense plus serving A/B before any default-on decision.

## 13. PHASE68 RESULT: BF16 F32 OUTPUT DENSE + SERVING A/B

Phase68 reused Phase67 source unchanged. Plan:
`docs/superpowers/plans/2026-07-01-bf16-f32-output-dense-serving-phase68.md`.
DGX artifact: `/home/mudler/bench/phase68_bf16_dense_serving/20260701_145710`;
serving A/B artifact:
`/home/mudler/bench/phase68_bf16_dense_serving/20260701_145710/serving_ab_20260701_150249`.

Correctness basis for the exact source commit remains Phase67: default and
`LLAMA_BF16_CUBLAS_F32_OUT=1` both produced MoE md5 `8cb0ce23`, dense md5
`5951a5b4`, and `MUL_MAT 1146/1146`.

Dense prefill stayed positive but tiny:

| npp | default S_PP | opt-in S_PP | change |
|-----|-------------:|------------:|-------:|
| `512` | `973.13` | `975.52` | `+0.25%` |
| `2048` | `1019.88` | `1021.39` | `+0.15%` |

MoE serving A/B at `N=128`, prompt `128`, generation `128`, `--parallel 128`:

| metric | default | opt-in | change |
|--------|--------:|-------:|-------:|
| `agg_tps` | `409.8` | `415.0` | `+1.27%` |
| `decode_agg_tps` | `615.3` | `627.2` | `+1.93%` |
| `prefill_tps` | `1630.2` | `1648.0` | `+1.09%` |
| `ttft_mean_ms` | `8574.7` | `8085.9` | `-5.70%` |
| `wall_s` | `39.978` | `39.480` | `-1.25%` |

Decision: carry the shortcut as a default-off opt-in candidate. It is no longer
just a prefill-only win, but Phase68 is not enough to default it on. Any future
default-on proposal must mirror the fork commit into the LocalAI patch series
and rerun a broader current serving snapshot with pre/post md5 and op gates.

## 14. PHASE69 RESULT: PATCH SERIES MIRROR READINESS

Phase69 checked the patch-series state without pushing and without editing
generated patch files. Plan:
`docs/superpowers/plans/2026-07-01-patch-series-mirror-readiness-phase69.md`.

Current committed LocalAI patches still match the Phase37 fork tip:

```text
base=0ed235ea2c17a19fc8238668653946721ed136fd
applied_tree=dedb1182910eafe9f6875588dc8285bfb544cce5
patch_tip_tree=dedb1182910eafe9f6875588dc8285bfb544cce5
fork_head_tree=fcf5720b659c5e1e2b487ccf3c8f7289bb12b9c4
match_patch_tip=yes
match_fork_head=no
patch_count=54
```

Dry-run export from `2d590d770..ea0875d14` produced ten additive source-only
patches, projected as `0064..0073`. Applying current `0001..0063` plus temp
`0064..0073` onto the pin exactly reconstructed current fork HEAD:

```text
applied_plus_missing_tree=fcf5720b659c5e1e2b487ccf3c8f7289bb12b9c4
fork_head_tree=fcf5720b659c5e1e2b487ccf3c8f7289bb12b9c4
match_fork_head=yes
current_patch_count=54
missing_patch_count=10
projected_patch_count=64
```

Projected patch tail:

- `0064` serving admission trace (`c6cb8460e`)
- `0065` admission histograms (`bd7b2e952`)
- `0066..0068` TTFT prefill-first scheduler knobs (`8a97629a4`,
  `3b6ab5fa8`, `8759213e3`)
- `0069..0070` W4A16 direct-activation policy/stub (`41be3da5b`,
  `7967ad47f`)
- `0071` layout trace (`fa944bb5f`)
- `0072` quant trace (`afc2c7030`)
- `0073` BF16 cuBLAS F32 output (`ea0875d14`)

Decision: mirror regeneration is technically ready but not executed. The local
fork is `26` commits ahead of `fork/localai-paged`, and the fork-first policy
requires pushing before regenerating the LocalAI series. Do not push without
explicit approval. After approval, push the fork, regenerate `0064..0073`, rerun
the same tree-hash check, and then run the broader serving gates before any
default-on BF16 policy change.

## 15. PHASE70 RESULT: BF16 F32 OUTPUT BROADER SERVING

Phase70 broadened the Phase68 serving evidence without source changes. Plan:
`docs/superpowers/plans/2026-07-01-bf16-f32-output-broader-serving-phase70.md`.
Benchmark ledger:
`backend/cpp/llama-cpp-localai-paged/docs/BENCHMARK.md`.
DGX artifact:
`/home/mudler/bench/phase70_bf16_broader_serving/20260701_151500`.

Gates stayed green. Default pre/post gates matched MoE md5 `8cb0ce23`, dense
md5 `5951a5b4`, `MUL_MAT 1146/1146`, and `MUL_MAT_ID 806/806`. Opt-in pre/post
gates matched MoE md5 `8cb0ce23`, dense md5 `5951a5b4`, and `MUL_MAT
1146/1146`.

Serving shape: MoE `NPL=8 32 128`, prompt `128`, generation `64`,
`PARALLEL=128`.

| n | opt/default agg | opt/default decode | opt/default TTFT | default decode/vLLM | opt decode/vLLM |
|---:|----------------:|-------------------:|-----------------:|--------------------:|----------------:|
| `8` | `0.8896` | `0.8998` | `1.1247` | `0.8100` | `0.7289` |
| `32` | `0.9912` | `0.9974` | `1.0320` | `0.6882` | `0.6864` |
| `128` | `1.0071` | `0.9882` | `0.9852` | `0.6921` | `0.6839` |

Decision: reject default-on for `LLAMA_BF16_CUBLAS_F32_OUT=1`. The shortcut is
correctness-clean, but it materially regressed low-concurrency serving and
slightly widened the vLLM decode gap at `n=32` and `n=128`. Keep it
default-off only and move the next parity effort to a different lever.

## 16. PHASE71 RESULT: GDN TENSOR-CORE REVALIDATION

Phase71 challenged the stale GDN planning docs before starting more source work.
Plan:
`docs/superpowers/plans/2026-07-01-gdn-tc-revalidation-phase71.md`.
Benchmark ledger:
`backend/cpp/llama-cpp-localai-paged/docs/BENCHMARK.md`.
DGX artifact:
`/home/mudler/bench/phase71_gdn_tc_revalidation/20260701_153425`.

Source under test stayed at DGX mirror commit
`14fd69f1e feat(cuda): gate BF16 cuBLAS F32 output`. No llama.cpp source was
changed.

Canonical gates matched for all four GDN modes: MoE md5 `8cb0ce23`, dense md5
`5951a5b4`, and `GATED_DELTA_NET 46/46`. Default also passed `MUL_MAT
1146/1146` and `MUL_MAT_ID 806/806`.

MoE prefill, `PP=512,2048`, `TG=4`, `B=32`, `CTX=131072`:

| arm | npp512 S_PP | npp2048 S_PP |
|-----|------------:|-------------:|
| default | `2313.57` | `2422.88` |
| sequential-disabled (`GDN_CHUNK_MIN=2147483647`) | `2198.28` | `2361.22` |
| serial-chunked (`GDN_TC=0 GDN_CHUNK_MIN=64`) | `1787.49` | `1699.77` |
| forced M5 (`GDN_TC=4 GDN_CHUNK_MIN=64`) | `2323.18` | `2420.52` |

Decision: keep shipped GDN M5 default behavior. It still beats
sequential-disabled by `+5.24%`/`+2.61%`, beats serial-chunked by
`+29.43%`/`+42.54%`, and forced M5 is within noise of the current default. Do
not reopen smaller GDN C32/QS/global-Ai32/kernel-reorder work on GB10.

Post-Phase71 do-not-reopen list for GB10:

- Smaller W4A16/MoE GEMM body, metadata, direct-activation, or quant/gather
  shortcuts.
- GDN C32 slab, QS-early, Global-Ai32, or another low-conflict M5 reorder.
- BF16 cuBLAS F32 output as a default-on policy.

The only GDN work that should be reconsidered is a larger FLA/CuteDSL-class
blocked-solve implementation or a hardware pivot where the GB10 constraints no
longer apply.

## 17. PHASE72 RESULT: TTFT MIN32 BROADER SERVING

Phase72 broadened the Phase59 min32 scheduler result to the same serving shape
used by Phase70. Plan:
`docs/superpowers/plans/2026-07-01-ttft-min32-serving-phase72.md`.
Benchmark ledger:
`backend/cpp/llama-cpp-localai-paged/docs/BENCHMARK.md`.
DGX artifact:
`/home/mudler/bench/phase72_ttft_min32_serving/20260701_160730`.

Source under test stayed at DGX mirror commit
`14fd69f1e feat(cuda): gate BF16 cuBLAS F32 output`. No llama.cpp source was
changed.

Gates stayed green. Pre default matched MoE md5 `8cb0ce23`, dense md5
`5951a5b4`, `MUL_MAT 1146/1146`, and `MUL_MAT_ID 806/806`. Pre/post min32 and
post default md5 gates also matched MoE `8cb0ce23` and dense `5951a5b4`.

Serving shape: MoE `NPL=8 32 128`, prompt `128`, generation `64`,
`PARALLEL=128`.

| n | min32/default agg | min32/default decode | min32/default TTFT | default decode/vLLM | min32 decode/vLLM |
|---:|------------------:|---------------------:|-------------------:|--------------------:|------------------:|
| `8` | `0.9302` | `0.9442` | `1.0379` | `0.7561` | `0.7140` |
| `32` | `0.9414` | `0.9570` | `1.0977` | `0.7158` | `0.6850` |
| `128` | `0.9699` | `0.9775` | `1.0300` | `0.6935` | `0.6779` |

Decision: keep `LLAMA_TTFT_PREFILL_FIRST=1` plus
`LLAMA_TTFT_PREFILL_FIRST_MIN_WAITING=32` opt-in only. It regressed aggregate,
decode, TTFT, and wall time at every tested concurrency in the broader shape,
and widened the vLLM decode gap. Do not default this scheduler policy on GB10.

## 18. PHASE73 RESULT: DATACENTER BLACKWELL RERUN READINESS

Phase73 is a no-new-benchmark decision/spec phase. Plan:
`docs/superpowers/plans/2026-07-01-datacenter-blackwell-rerun-readiness-phase73.md`.
Benchmark ledger:
`backend/cpp/llama-cpp-localai-paged/docs/BENCHMARK.md`.

No GPU benchmark was run and no llama.cpp source was changed. Source baseline
remains DGX mirror commit `14fd69f1e feat(cuda): gate BF16 cuBLAS F32 output`.

Decision:

- Do not start more GB10 grouped-MMQ/W4A16 source work. Phase61 direct-A was
  the last structurally distinct W4A16 shortcut and failed its keep gate; Phase66
  quantize plus gather was only `5.10%`, below the source-funding threshold.
- Do not start GDN backend source work until a standalone C=64 blocked-solve PoC
  proves timing and numerical viability. Phase71 kept M5 as shipped; the
  remaining GDN gap is a larger FLA/CuteDSL-class blocked-solve/register-state
  implementation, not another C32/QS/global-Ai/local reorder.
- The next parity evidence should come from datacenter Blackwell hardware with
  the existing same-session serving harness plus graph-node decode profiles.

B200 rerun checklist:

1. Build and verify the llama.cpp paged binary on B200 or equivalent
   datacenter Blackwell hardware with the correct CUDA architecture/settings.
2. Install and verify vLLM `0.23.0+` with the intended Blackwell backend stack.
3. Confirm both model forms exist: `q36-35b-a3b-nvfp4.gguf` and
   `q36-35b-a3b-nvfp4-vllm`.
4. Run `paged-current-serving-snapshot.sh` with `NPL="8 32 128"`, `PTOK=128`,
   `GEN=64`, `PARALLEL=128`, `CTX=131072`, and B200-specific
   `VLLM_GPU_MEMORY_UTILIZATION`, `VLLM_MAX_NUM_SEQS`, and
   `VLLM_TENSOR_PARALLEL_SIZE`.
5. Before interpreting the artifact, require `hardware.txt` to say
   `hardware_class=datacenter_blackwell`, `gate_summary.tsv` to be green,
   pre/post MoE md5 `8cb0ce23`, dense md5 `5951a5b4`, `MUL_MAT` and
   `MUL_MAT_ID` op gates green, and `summary.tsv` rows for both paged and vLLM.
6. Run decode/profile reruns with `nsys --cuda-graph-trace=node` and inspect
   whether vLLM is using native FP4/CUTLASS/FlashInfer rather than the GB10
   Marlin fallback.

Phase74 standalone GDN source-work gate result:

```sh
nvcc -O3 -arch=sm_121a \
  ~/scratch_tc_gdn_poc/gdn_blocked_solve_bench.cu \
  -o ~/scratch_tc_gdn_poc/gdn_blocked_solve_bench

~/scratch_tc_gdn_poc/gdn_blocked_solve_bench \
  --c 64 --dk 128 --dv 128 \
  --iters 1000 \
  --precision tf32,offdiag3x,apply3x \
  --oracle f64 \
  --dump-json ~/bench/phase74_gdn_blocked_solve_poc/20260701_143711/phase74_gdn_blocked_solve_poc.json
```

Artifact:
`/home/mudler/bench/phase74_gdn_blocked_solve_poc/20260701_143711`.

The standalone C=64 shared-memory explicit inverse-plus-apply scaffold did not
fund backend source work:

- weak decay: direct solve/apply `3.263936 ms`; inverse-plus-apply
  `5.493515 ms`; inverse/direct speed `0.5941x`; inverse NMSE `2.755e-15`;
- mixed decay: direct solve/apply `3.275959 ms`; inverse-plus-apply
  `5.527584 ms`; inverse/direct speed `0.5927x`; inverse NMSE `7.541e-16`;
- shared memory was already near the GB10 cap: direct `81920` bytes,
  inverse-plus-apply `98304` bytes, with `99 KB` opt-in available.

Decision: do not touch `ggml/src/ggml-cuda/gated_delta_net.cu` for this C=64
inverse scaffold on GB10. A future GDN source-work gate must be a substantially
different tensor-core blocked-solve/register-state design that shows a material
timing win before backend changes.

Phase75 follow-up audit:

- llama.cpp already ships the M5 tensor-core GDN path default-on under paged KV:
  `KK/QK`, `KS/QS`, `P*U`, explicit `T=A^-1`, `U=T*RHS`, and
  `Kc^T*DU` state carry are covered in the current `C=16` GB10 path.
- vLLM has a distinct one-token recurrent decode path that updates state
  directly and a packed decode path that avoids Q/K/V materialization copies,
  but this is not source-funded in llama.cpp without a fresh profile: prior
  parity evidence showed llama.cpp GDN decode already faster than vLLM and
  decode serving dominated by host/MoE synchronization.
- vLLM's CuTeDSL GDN prefill path is useful reference material for datacenter
  Blackwell, but depends on SM10x/CUDA-13 features such as TMA/tcgen05/CUTLASS
  DSL and should not be treated as a portable GB10 patch base until the local
  toolchain proves support.

Phase76 current-stack GB10 graph-node profile:

- Artifact:
  `/home/mudler/bench/phase76_current_moe_profile/20260701_145116`.
- Shape: MoE `q36-35b-a3b-nvfp4`, `n=128`, `PTOK=128`, `GEN=64`,
  `PARALLEL=128`, `CTX=131072`, production defaults.
- Pre/post gates were green: MoE md5 `8cb0ce23777bf55f92f63d0292c756b0`, dense
  md5 `5951a5b4d624ce891e22ab5fca9bc439`, `MUL_MAT 1146/1146`,
  `MUL_MAT_ID 806/806`.
- Serving under graph-node profiling: aggregate `204.1 t/s`, decode aggregate
  `320.7 t/s`, prefill `1490.1 t/s`, TTFT mean `8365.1 ms`, wall `40.146 s`.
- Bucket result: GDN was the largest macro bucket, `6669.16 ms` (`32.88%`),
  ahead of MoE/FFN-GEMM `6264.88 ms` (`30.88%`) and BF16 projections
  `2772.38 ms` (`13.67%`). `gdn_core` alone was `5876.94 ms` (`28.97%`).

This supersedes the Phase75 "datacenter only unless fresh profile" wording:
Phase76 is that fresh profile. It does **not** justify an immediate backend
patch because it is llama-only and graph-node tracing depresses absolute
throughput, but it does fund one narrow GB10 follow-up before waiting for B200:
prove whether vLLM's direct recurrent/packed decode idea can reduce the current
`gdn_core` bucket.

Current next gate:

1. Keep the B200/B100/GB200 Phase72 same-session rerun as the hardware-pivot
   gate when datacenter Blackwell is available.
2. In parallel on GB10, run a Phase77 GDN decode proof with pre/post md5 and op
   gates. Accept only if it materially reduces the Phase76 `gdn_core` bucket and
   does not regress serving throughput or canonical output md5.
3. Do not merge or default-on any `gated_delta_net.cu` change from this evidence
   alone; Phase76 is a profile gate, not a source patch gate.

Phase77 decode-only profile result:

- Artifact:
  `/home/mudler/bench/phase77_moe_decode_only_profile/20260701_150134`.
- Shape: MoE `q36-35b-a3b-nvfp4`, `N=128`, long-running `/completion`
  requests, `N_PREDICT=2048`, capture after active decode.
- Capture window: active slots `128`; median decoded depth `67` at start and
  `89` mid-capture.
- Pre/post gates were green: MoE md5 `8cb0ce23777bf55f92f63d0292c756b0`, dense
  md5 `5951a5b4d624ce891e22ab5fca9bc439`, `MUL_MAT 1146/1146`,
  `MUL_MAT_ID 806/806`.
- Bucket result: GDN `1489.71 ms` (`41.20%`) and MoE/FFN-GEMM `1400.77 ms`
  (`38.74%`). Fine bucket `gdn_core` was `1408.33 ms` (`38.95%`), slightly
  larger than `mmq_nvfp4` at `1383.50 ms` (`38.26%`).

Phase77 supersedes the Phase75 "no GB10 GDN source work" stance for decode
only. Do **not** reopen the failed C=64 prefill inverse scaffold. The funded
GB10 source path is now a narrow, default-off GDN decode A/B or standalone PoC
based on vLLM's direct recurrent/packed decode structure. The next patch must
prove a material reduction in the Phase77 `gdn_core` bucket, keep canonical md5
and op gates green, and avoid serving/decode throughput regression under the
same decode-only capture shape before it can be considered for merge or default.

Phase78 launch-shape sweep:

- Baseline: Phase77 default launch shape (`GDN_NW=16 GDN_CPW=8`) had
  `gdn_core 1408.33 ms` (`38.95%`) in the decode-only window.
- `GDN_NW=8 GDN_CPW=8` artifact:
  `/home/mudler/bench/phase78_gdn_launch_sweep/nw8_cpw8_20260701_150654`.
  Gates were green, but `gdn_core` worsened to `1443.55 ms` (`39.68%`).
- `GDN_NW=16 GDN_CPW=4` artifact:
  `/home/mudler/bench/phase78_gdn_launch_sweep/nw16_cpw4_20260701_150954`.
  Rejected before profiling: `MUL_MAT_ID` failed `805/806`.

Decision: keep default `GDN_NW=16 GDN_CPW=8`. Do not retry existing
`GDN_NW`/`GDN_CPW` launch-shape retunes unless a new profile gives a specific
reason. The next GB10 source-funded work must be structural, default-off, and
measured against the Phase77 decode-only `gdn_core` bucket.

Phase79 BV32 decode source A/B:

- Artifact root:
  `/home/mudler/bench/phase79_gdn_decode_bv32_ab/20260701_152530`.
- Candidate source tree:
  `/home/mudler/llama-phase79-gdn-source`.
- Candidate patch: one-file default-off CUDA decode-only kernel in
  `ggml/src/ggml-cuda/gated_delta_net.cu`, enabled by `GDN_DECODE_BV32=1`.
  Scope was `S_v=128`, one-token decode, scalar gate, final-state write-back.
- Candidate build completed for `llama-completion`, `llama-batched-bench`,
  `test-backend-ops`, and `llama-server`.
- Safety gates were green for the candidate default and opt-in paths:
  MoE md5 `8cb0ce23`, dense md5 `5951a5b4`, `MUL_MAT 1146/1146`,
  `MUL_MAT_ID 806/806`.
- A/B baseline post-gate initially failed `MUL_MAT 1145/1146` on a q4_1 case
  after profiling, but immediate retry in `A_baseline/gate_post_retry` was
  green; treat the first failure as a gate hiccup, not as accepted evidence.

Result:

| arm | env | GDN ms | `gdn_core` ms | `gdn_core` launches | `gdn_core`/launch |
|-----|-----|-------:|--------------:|--------------------:|------------------:|
| baseline | none | `1493.14` | `1411.46` | `600` | `2.352 ms` |
| BV32 | `GDN_DECODE_BV32=1` | `1502.89` | `1426.17` | `570` | `2.502 ms` |

Decision: reject the BV32 decode topology. It passed md5/op gates but worsened
normalized `gdn_core` by about `6.4%` per launch and increased the GDN macro
bucket. Do not carry this source patch forward. The next GDN source hypothesis
should target recurrent-state precision/traffic or another structural delta
from vLLM; reduced-precision recurrent state is promising but invasive and needs
a separate scope.

Phase80 identity-ids shortcut source A/B:

- Artifact root:
  `/home/mudler/bench/phase80_gdn_identity_ids_ab/20260701_153927`.
- Candidate source tree:
  `/home/mudler/llama-phase80-gdn-identity-source`.
- Candidate patch: one-file default-off shortcut in
  `ggml/src/ggml-cuda/gated_delta_net.cu`, enabled by
  `GDN_ASSUME_IDENTITY_IDS=1`. It skips the GDN scratch gather for one-token
  final-state decode by assuming `ids[s] == rs_head+s` and reading from
  `state_dst` directly.
- Candidate build completed for `llama-completion`, `llama-batched-bench`,
  `test-backend-ops`, and `llama-server`.
- Baseline and candidate pre/post gates were green: MoE md5 `8cb0ce23`, dense
  md5 `5951a5b4`, `MUL_MAT 1146/1146`, `MUL_MAT_ID 806/806`.

Result:

| arm | env | GDN ms | `gdn_core` ms | `gdn_gather` ms | GDN macro launches |
|-----|-----|-------:|--------------:|----------------:|------------------:|
| baseline | none | `1493.57` | `1411.65` | `0.79` | `3600` |
| identity shortcut | `GDN_ASSUME_IDENTITY_IDS=1` | `1489.96` | `1409.28` | not present | `3000` |

Decision: reject carry-forward/default. The shortcut safely removes the tiny
`gdn_gather` bucket in this shape, but `gdn_core` is unchanged and the identity
assumption is too narrow for a sub-millisecond capture-level win. Do not spend
more parity time on gather-only GDN shortcuts unless a future profile makes
gather material. The next serious GDN scope remains recurrent-state
precision/traffic.

## Series trim (phases 110-140 review, 2026-07-02)

The campaign's on-disk patches `0048-0063` were added without matching fork
commits (a fork-first policy violation). After a keep/drop review of the
phase 110-140 work, the series was trimmed to a single kept line plus the
gate harness, and re-mirrored to the fork:

- KEEP - test sentinels (the MoE gate harness): `MOE_SWIGLU_DOWN`,
  `MOE_SWIGLU_COMBINE`, `MUL_MAT_ID_RAGGED_MOE` (old `0051-0053`).
- KEEP - the MTP-draft correctness fix (old `0054`): forces target-side
  sampler acceptance for MTP drafts (backend draft sampling can request
  multiple output rows per sequence); the backend ships `-mtp` gallery models.
- KEEP - the Phase135 routed-FFN fused-quant line: whole-pattern MoE matcher +
  routed-FFN executor hook (Phase120/121), the routed-FFN PoC scaffold
  `moe-ffn.{cu,cuh}` (Phase132), and the fused SwiGLU-to-NVFP4-quant + raw down
  MMQ (`ggml_cuda_mul_mat_q_moe_quantized` + local `ggml_cuda_mmq_ids_meta`
  refactor, Phase135). All default-off, md5-clean opt-in, six
  `mmq_moe_quantized_raw` markers with zero sorted launches on the sentinel.

- DROP - W4A16 grouped-tile pack/tune/pad (old `0048-0050`): dead line, W4A16
  is ~1.5x slower than grouped-MMQ.
- DROP - speculative/trace/cublas-route/mmid-route/mul-mat-route traces + the
  rejected small-M tile-policy knob (old `0055-0063`).
- DROP - all other campaign keep-markers not needed by Phase135: GPU-sort
  (Phase110), W4A16-direct-A (Phase112), boundary trace/timing (Phase117),
  Phase133 sorted-F32 down, Phase134 fused-SWIGLU-only, Phase138
  finalize/weighted-combine. The final fork tree carries zero of these markers.

Fork branch `mudler/llama.cpp:localai-paged` re-mirrored on top of
`51168c5ee` (LocalAI series `0001-0047`):

- `fd920cf8a` test(paged): cover MoE swiglu down chain
- `a85c1e098` test(paged): cover MoE weighted combine chain
- `2fed6aacf` test(paged): cover ragged MoE dispatch
- `f1d976f06` fix(speculative): disable backend sampling for MTP drafts
- `1edddc8fe` feat(paged): whole-pattern MoE matcher + routed-FFN fused
  NVFP4-quant down MMQ

New fork HEAD `1edddc8fe`, tree `097c862c`. The rejected/neutral levers of
the 110-140 campaign are recorded above and in the per-phase bench artifacts.
