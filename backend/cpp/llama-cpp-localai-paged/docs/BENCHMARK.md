# llama.cpp vLLM Parity Benchmark Ledger

This file tracks each parity attempt from Phase70 onward, plus the immediate
context needed to interpret the current record. Append every new attempt here
with artifact path, gates, benchmark rows, and decision.

## Current Status

- Goal: reach vLLM speed parity in llama.cpp on GB10.
- Current decision model: MoE `q36-35b-a3b-nvfp4`.
- Canonical paged MoE md5: `8cb0ce23777bf55f92f63d0292c756b0`.
- Canonical dense md5: `5951a5b4d624ce891e22ab5fca9bc439`.
- Current tested source: DGX mirror
  `/home/mudler/llama-phase93-qwen3next-gqa-bcast`, local guardrail stack plus
  Qwen3Next grouped Q/K broadcast for fused GDN.
- Latest attempt: Phase141 GDN decode-only noise-floor repeat.
- Latest decision: recurrence-level GDN source A/B must normalize by launch
  count or control the decode capture window tightly. Phase141 ran five
  identical current-binary decode-only captures with pre/post gates green. Raw
  `gdn_core_ms` had median `1415.500`, stdev `30.641`, CV `2.146%`, and range
  `1410.300..1482.140 ms`, mostly because capture windows recorded `597`,
  `598`, `600`, or `630` `gdn_core` launches. Normalized
  `gdn_core_ms_per_launch` was much steadier: median `2.359167`, stdev
  `0.005399`, CV `0.229%`, range `2.352603..2.366917 ms`. A future
  recurrence-level source patch must beat `max(2.0%, 3 * same-binary stdev)`
  on repeated A/B medians, using per-launch GDN core when launch counts drift;
  for Phase141 that means at least `6.49%` raw `gdn_core` reduction or `2.0%`
  launch-normalized reduction. Phase140 still rejects prep-only L2 fusion. The
  most defensible small source follow-up is a default-off scalar gate/beta
  hoist inside `gated_delta_net_cuda`; the vLLM-style packed decode recurrence
  remains a larger redesign, not a shortcut.
  Phase137 was rejected with no source changes: `GDN_NW=4 GDN_CPW=1` improved
  isolated 1-token GDN rows but regressed real serving versus Phase135
  (`208.0/332.7 -> 206.2/324.9` aggregate/decode t/s, `gdn_core`
  `5926.55 -> 6466.27 ms`). Phase135 remains the current best default-off
  routed-FFN base without Phase138 finalize, but not parity. Phase135 adds
  `LLAMA_MOE_ROUTED_FFN_FUSED_QUANT=1` on top of
  `LLAMA_MOE_ROUTED_FFN_POC=1`: it computes `silu(gate) * up` directly into
  the NVFP4 MMQ activation layout and launches raw down MMQ, skipping both the
  sorted F32 buffer and the separate activation-quant kernel. Focused gates and
  canonical opt-in gates passed; trace proved six `mmq_moe_quantized_raw`
  launches and zero `mmq_moe_sorted_raw` launches. Focused perf was mixed but
  better at the larger sentinel: default `805.92/1031.06 us`, Phase135
  `807.92/1024.97 us` for `n=128/257`. The same opt-in serving profile at the
  Phase130 shape passed pre/post gates and improved decode aggregate t/s
  `326.9 -> 332.7`, while `mmq_nvfp4` dropped `6009.52 -> 5915.24 ms`; total
  kernel time still rose slightly (`20.1559 -> 20.2498 s`) because GDN and
  projection buckets moved up. Next work should either make this path
  default-off-clean enough for broader serving comparisons, or attack the
  remaining MoE launch/writeback overhead (`mmq_fixup`, route metadata, and
  direct weighted combine) rather than another F32 intermediate. Phase134 is
  kept as a default-off fused-SWIGLU structural base,
  not as a promoted speedup. Phase134 adds
  `LLAMA_MOE_ROUTED_FFN_FUSED_SWIGLU=1` on top of
  `LLAMA_MOE_ROUTED_FFN_POC=1`: it executes `gate_up`, computes
  `silu(gate) * up` directly into expert-sorted F32 rows, then calls the raw
  MMQ down helper. Selected opt-in gates passed `13/13`; trace proved six raw
  sorted launches; canonical opt-in gates passed MoE/dense md5,
  `GATED_DELTA_NET 48/48`, `MUL_MAT 1146/1146`, and `MUL_MAT_ID 806/806`.
  Focused perf was mixed: default `804.92/1026.02 us`, Phase134
  `810.61/1025.68 us` for `n=128/257`. It removes the Phase133 standalone
  `glu -> get_rows` boundary and recovers n=257, but the extra fused-SWIGLU
  kernel is still slower at n=128. Next work should fuse SWIGLU directly into
  the down-MMQ quant buffer, or otherwise remove one more launch/buffer.
  Phase133 remains only as a default-off structural base for the
  next fused routed-FFN slice, not as a speedup. Phase133 adds
  `LLAMA_MOE_ROUTED_FFN_SORTED_DOWN=1` on top of
  `LLAMA_MOE_ROUTED_FFN_POC=1`: it keeps baseline `gate_up` and `SWIGLU`,
  gathers the computed SWIGLU output into expert-sorted compact F32 rows, and
  calls a raw MMQ down helper without constructing fake tensors. Default and
  opt-in canonical gates passed with canonical MoE/dense md5s,
  `GATED_DELTA_NET 48/48`, `MUL_MAT 1146/1146`, and `MUL_MAT_ID 806/806`;
  selected default/Phase132/Phase133 gates passed `13/13`, and trace proved
  six `mmq_moe_sorted_raw` launches. Focused perf was not a win:
  default `807.37/1020.76 us`, Phase132 `808.21/1018.87 us`, Phase133
  `808.85/1026.87 us` for `n=128/257`. The next phase must fuse
  SWIGLU-to-sorted or SWIGLU-to-quant to remove the added gather/quant boundary;
  do not promote sorted-down as-is. Phase132 remains the cleaner default-off
  scaffold if Phase133 needs to be bypassed. Phase131 challenged the Phase130 fork with two read-only
  source explorers. Both rejected another cheap source patch: MoE/FFN-GEMM work
  should not continue unless it funds a real fused routed-FFN kernel/executor,
  and GDN work should not continue unless it materially changes the f32
  recurrent-state traffic without BF16/quality drift. The next active line is
  therefore a default-off fused routed-FFN PoC scoped from vLLM's real fused MoE
  design and llama.cpp's current `gate_up -> SWIGLU -> down` executor hook.
  Phase131 is a no-source decision/architecture attempt, not a speedup claim.
  Keep carrying the Phase93 Qwen3Next GQA-repeat removal
  candidate as a decode-profile positive, but it does not close serving parity.
  Phase130 refreshed the current-stack graph-node serving profile after the
  Phase129 rejection. Pre/post gates stayed green and the profile confirms the
  live serving bottleneck remains split between `mmq_nvfp4` (`6009.52 ms`,
  `29.82%`) and `gdn_core` (`5891.40 ms`, `29.23%`), with FA only `1.28%` and
  get-rows only `1.39%`. This rejects the paged-mask/F16 get-rows idea as the
  next source patch and keeps the next credible work on either a larger
  MoE/FFN-GEMM executor/kernel or a larger GDN recurrence redesign. Phase129
  tested a default-off Qwen35/Qwen35MoE grouped Q/K broadcast probe for
  fused GDN, reusing the existing Qwen3Next op-param path. The default path was
  md5/op clean, but the valid opt-in gate changed the MoE greedy md5 to
  `b773e2f032aa0e992626d486b321808e`, so the source was rejected and reverted.
  Do not port Qwen3Next grouped-broadcast semantics to Qwen35/Qwen35MoE under
  the current bit-exact rule. Phase128 scoped the Qwen3Next BF16 GDN S-cache
  idea and rejected/reverted the
  source probe for the current target: the active `q36-35b-a3b-nvfp4.gguf`
  model loads as `qwen35moe`, no true Qwen3Next GGUF was found on DGX, and the
  existing Qwen35/Qwen35MoE BF16 S-cache lever was already rejected by the
  Phase82 f16-reference KL gate. Phase127 tested the first whole-MoE
  expert-major executor using the Phase126 helper; it passed selected
  correctness and emitted expert-major markers, but was rejected and reverted
  because focused perf regressed `MOE_SWIGLU_DOWN` at both n=128 and n=257.
  Phase126 remains the kept scaffold.
  Phase104 measured the combined cleanup stack in the normal same-session
  serving harness against vLLM at `N=128`. It is md5/op clean and modestly
  improves paged serving versus Phase97 (`agg_tps 329.6 -> 338.6`,
  `prefill_tps 1734.5 -> 1813.0`, `TTFT 7415.4 -> 7121.6 ms`), but it is not
  parity-closing: paged/vLLM is `0.6574` on decode and `0.5122` on aggregate.
  Phase105 refreshed the current-stack grouped-MMQ evidence: ragged MoE and
  full `MUL_MAT_ID` gates still pass, serving launch traces still have
  `fixup=0` and `stream_k_blocks == ntiles_dst`, and the simple live request
  landed in density-10 prefill-like shapes (`mmq_x_best=112`) rather than a new
  small-M decode opportunity. Phase106 then tested the C1 high-concurrency
  operating-point hypothesis at `N=128/192/256`; vLLM completed all legs and
  stayed ahead, so C1 is rejected for the current GB10 stack. Do not add another
  MMQ micro-policy patch or scheduler shortcut. Phase107 established the
  existing fused-MoE correctness guardrails and found that `test-backend-ops
  perf` did not emit timing rows for these custom whole-graph cases. Phase108
  added the missing measurement-only harness by exposing the existing MoE
  whole-graph cases to perf mode and expanding CSV output to include timing
  fields. Use these timings to rank fused routed-MoE work; do not start a fused
  kernel without improving one of these rows and preserving md5/op gates.
  Phase109 tested the existing default-off W4A16 and FP4 large-M MoE routes,
  plus the cheapest grouped-MMQ density/tile-policy knobs, on the Phase108 rows.
  All selected op gates passed, but none of the env-only routes is a useful
  parity lever: W4A16 and FP4 large-M are much slower at `n_tokens=257`, while
  `LLAMA_MOE_DENSITY_MAX=9` / `LLAMA_MOE_MMQ_X=64` are noise-level on
  `MUL_MAT_ID_RAGGED_MOE` and do not help `MOE_SWIGLU_DOWN`. The next credible
  implementation target is GPU-side routed-MoE metadata construction for the
  host-sync fallback/grouped path, taking the vLLM `moe_align_block_size` /
  permute-unpermute design as the reference, not importing vLLM wholesale.
  Phase110 implemented that first default-off CUDA metadata branch behind
  `LLAMA_MOE_GPU_SORT=1`, reusing `mm_ids_helper` and adding a tiny inverse
  permutation kernel for the fallback `get_rows` contract. The initial branch
  failed `3/13` selected opt-in rows because `mm_ids_helper`'s `ids_dst` is
  sorted-to-original while fallback `get_rows` needs original-to-sorted; the
  inversion fix made default, W4A16, and W4A16+GPU-sort selected gates `13/13`,
  and canonical md5/op gates stayed green. Keep Phase110 as a default-off
  structural base only: it improves W4A16 fallback 257-token rows by `7-8%`,
  but remains `~1.5x` slower than default grouped-MMQ, so it is not a parity
  win by itself.
  Phase111 then tried to remove the remaining W4A16 fallback host descriptor
  construction by building `w4a16_tile_desc` on GPU from `expert_bounds_dev`.
  The first compile needed a pointer mutability fix, then the first runtime
  attempt hit a CUDA pool LIFO assertion because the outer expert-bounds
  allocation was freed after an inner later allocation. After fixing that,
  selected gates passed for the new `LLAMA_W4A16_GPU_TILES=1` path, but clean
  perf was flat-to-negative versus Phase110 (`MUL_MAT_ID_RAGGED_MOE n=257`
  regressed about `2.0%`). The Phase111 source was reverted; post-revert
  W4A16+GPU-sort selected gates passed `13/13`. Do not carry a GPU tile
  descriptor path unless it is part of a larger direct-A or graph-safe W4A16
  redesign that removes more than one host-sync/launch bottleneck.
  Phase112 implemented the existing default-off `LLAMA_W4A16_DIRECT_A=1` hook
  for W4A16 grouped MoE, staging bf16 activations directly from original `src1`
  through `ids_to_sorted` instead of materializing a sorted f32 buffer and then
  casting it. Selected gates passed for W4A16+GPU-sort, direct-A alone, and
  direct-A+GPU-sort (`13/13` each). The useful arm is direct-A+GPU-sort:
  `MUL_MAT_ID_RAGGED_MOE n=257` improved `2278.50 -> 2166.22 us` (`+4.93%`)
  and `MOE_SWIGLU_DOWN n=257` improved `1551.08 -> 1477.74 us` (`+4.73%`)
  versus Phase112's W4A16+GPU-sort control, while the 128-token rows were
  neutral/slightly negative. Canonical README md5 gates are green
  (`8cb0ce23`, `5951a5b4`) and compact op gates are green on the supported
  rows. Keep Phase112 default-off as the next structural base; do not make it
  default-on because W4A16 fallback remains slower than the default grouped-MMQ
  path.
  Phase113 tried the combined follow-up:
  `LLAMA_W4A16_DIRECT_A=1 LLAMA_MOE_GPU_SORT=1 LLAMA_W4A16_GPU_TILES=1`.
  It built W4A16 tile descriptors from GPU expert bounds and launched over a
  zero-initialized `max_tiles` grid to avoid even the one-int tile-count
  readback. Selected correctness stayed green (`13/13`), but perf did not meet
  the keep threshold: `MOE_SWIGLU_DOWN n=257` was effectively flat
  (`1478.16 -> 1476.36 us`) and `MUL_MAT_ID_RAGGED_MOE n=257` regressed
  (`2148.44 -> 2214.23 us`). The Phase113 source was reverted; post-revert
  Phase112 direct-A+GPU-sort selected gates passed `13/13`.
  Phase114 then implemented the vLLM-style padded routing contract behind
  `LLAMA_W4A16_PADDED_META=1`: separate padded source ids, padded destination
  ids, expert ids per M block, a padded W4A16 expert-id consumer mode, and a
  direct scatter that skipped the old compact `get_rows_cuda` restore. It was
  correctness-clean (`13/13`) but failed the performance gate. Initial artifact:
  `/home/mudler/bench/phase114_w4a16_padded_routing/20260701_234634_padded_meta`;
  fix1 artifact:
  `/home/mudler/bench/phase114_w4a16_padded_routing/20260701_235003_padded_meta_fix1`.
  Fix1 added `num_tokens_post_pad` early returns for padded gather/scatter, but
  257-token rows still regressed (`MOE_SWIGLU_DOWN 1477.88 -> 1726.27 us`,
  `MUL_MAT_ID_RAGGED_MOE 2163.35 -> 2650.93 us`). The source was reverted and
  post-revert Phase112 direct-A+GPU-sort selected gates passed `13/13`.
  Phase115 then re-tested the existing default-off MoE small-M MMQ tile knob on
  the current Phase108 whole-graph sentinels rather than adding another patch.
  Artifact:
  `/home/mudler/bench/phase115_moe_small_m_sentinel/20260702_020258`.
  Control and `LLAMA_MOE_SMALL_M_TILE=16/32/64` all passed the selected
  `MOE_SWIGLU_DOWN,MUL_MAT_ID_RAGGED_MOE` correctness gate (`13/13` each), but
  none met the promotion rule. The best 128-token rows were tiny/noise-level
  wins, while every capped env regressed the 257-token ragged row
  (`1452.30 us` control vs `1455.02`, `1458.71`, `1456.88 us`). Reject
  small-M row shaping as a parity lever; the next phase should scope a true
  fused routed-MoE kernel or a graph-level fusion target that removes materialized
  activation/output traffic.
  Phase116 implemented that graph-level probe as a default-off CUDA-only
  detector for the plain `GLU -> down MUL_MAT_ID` pattern:
  `LLAMA_MOE_SWIGLU_DOWN_FUSED_QUANT=1`. The candidate computed
  `silu(gate) * up` directly into the existing grouped-MMQ NVFP4 activation
  buffer, leaving the MMQ kernel and graph API unchanged. Artifact:
  `/home/mudler/bench/phase116_moe_swiglu_down_fused_quant/20260702_022611`.
  Correctness passed (`13/13`) and the fix1 route emitted the fused trace marker
  (`6` hits), but perf failed the promotion gate: `MOE_SWIGLU_DOWN n=257` was
  flat (`1024.90 -> 1024.69 us`), `n=128` regressed (`806.33 -> 808.79 us`),
  and the non-fused ragged sentinel drifted slower. Source was reverted and the
  post-revert selected gate passed `13/13`. Do not retry a standalone fused
  SwiGLU-to-MMQ-activation-quant path; the next fused-MoE attempt must remove a
  larger boundary than one activation materialization.
  Phase117 added default-off boundary tracing/timing around the route-sort,
  activation quantization, grouped-MMQ launch, GLU, and whole-graph pattern
  detector. Artifact:
  `/home/mudler/bench/phase117_moe_route_once_boundary/20260702_024140`.
  The first timing run proved inline CUDA events are incompatible with CUDA
  graph capture (`cudaEventSynchronize` on a capturing stream), so the trace was
  guarded to emit `us=-1` during capture and real timings only with
  `GGML_CUDA_DISABLE_GRAPHS=1`. Post-guard selected gates passed (`13/13`),
  trace mode passed (`7/7`), and canonical gates passed: MoE md5 `8cb0ce23`,
  dense md5 `5951a5b4`, `MUL_MAT 1146/1146`, `MUL_MAT_ID 806/806`.
  No new runtime optimization is promoted from Phase117. The timing attribution
  rejects another small route-sort or standalone GLU/quant shortcut; the next
  funded MoE source phase needs a larger pipeline boundary: shared route
  metadata across gate_up/down and/or an executor that owns
  GEMM1->activation->GEMM2 rather than another local micro-fusion.
  Phase118 tested a default-off route metadata cache/reuse prototype. Artifact:
  `/home/mudler/bench/phase118_moe_route_cache/20260702_030549`.
  The first preflight command falsely detected `local-ai-worker` because the
  check matched its own shell text; the corrected `pgrep -x local-ai-worker`
  preflight was clean. The cache candidate (`LLAMA_MOE_ROUTE_CACHE=1`) was
  correctness-clean and did hit (`23` hits, `3` misses on the trace row), but
  did not meet the keep rule: `MOE_SWIGLU_DOWN n=257` improved only
  `1017.711 -> 1011.915 us` (`+0.57%`) and `n=128` regressed
  `799.360 -> 803.738 us` (`-0.55%`). Runtime cache source was reverted; the
  post-reject selected gate passed `13/13`. Keep only the local ids metadata
  helper refactor if final checks remain clean. This closes route-cache as a
  standalone parity lever; next MoE work needs a larger executor boundary than
  skipping one metadata build.
  Phase119 added a default-off whole-pattern contract trace for
  `gate_up MUL_MAT_ID -> views -> SWIGLU -> down MUL_MAT_ID`. Initial artifact:
  `/home/mudler/bench/phase119_moe_whole_pattern_contract/20260702_034729`;
  fix1 artifact:
  `/home/mudler/bench/phase119_moe_whole_pattern_contract/20260702_035126_fix1`.
  The initial trace proved coverage but exceeded the trace-overhead rule on
  `MOE_SWIGLU_DOWN n=257` (`1015.070 -> 1028.937 us`, `-1.35%`). Fix1 moved
  detector work fully off the default path unless a trace env is enabled. It is
  correctness-clean (`13/13` selected, `7/7` trace), canonical md5/op clean
  (MoE `8cb0ce23`, dense `5951a5b4`, `MUL_MAT 1146/1146`,
  `MUL_MAT_ID 806/806`), and trace overhead is within rule:
  `MOE_SWIGLU_DOWN n=128` `805.400 -> 805.584 us` (`-0.02%`) and `n=257`
  `1019.715 -> 1021.836 us` (`-0.21%`). Keep Phase119 as default-off
  diagnostic/contract scaffolding only. The next source phase is allowed to
  implement a guarded executor, but the executor must match at the earlier
  `gate_up MUL_MAT_ID` node so it can own `GEMM1->activation->GEMM2` and skip
  the remaining nodes; the current GLU hook is validation-only because GEMM1
  has already executed.
  Phase120 added that earlier default-off matcher/trace at the
  `gate_up MUL_MAT_ID` node. Initial artifact:
  `/home/mudler/bench/phase120_moe_early_whole_pattern/20260702_040153`;
  fix2 artifact:
  `/home/mudler/bench/phase120_moe_early_whole_pattern/20260702_040725_fix2`.
  The initial/fix1 traces proved `skip_ready=4` but emitted noisy unsupported
  candidates from unrelated `MUL_MAT_ID` rows; fix2 gates output on the actual
  `gate/up` view pair only. Fix2 is correctness-clean (`13/13` selected,
  `7/7` early trace), canonical md5/op clean (MoE `8cb0ce23`, dense
  `5951a5b4`, `MUL_MAT 1146/1146`, `MUL_MAT_ID 806/806`), and early trace
  overhead stays within rule: `MOE_SWIGLU_DOWN n=128` `803.937 -> 808.978 us`
  (`-0.62%`) and `n=257` `1020.412 -> 1026.073 us` (`-0.55%`). Keep Phase120
  as the executor entry-point scaffold. The next source phase should add a
  default-off executor that starts from this early matcher, first proving safe
  ownership/skip accounting, then moving route-plan reuse and fused activation
  into that helper.
  Phase121 added that default-off executor proof behind
  `LLAMA_MOE_WHOLE_PATTERN_EXEC=1`. Initial artifact:
  `/home/mudler/bench/phase121_moe_whole_pattern_exec_proof/20260702_041543`;
  fix1 artifact:
  `/home/mudler/bench/phase121_moe_whole_pattern_exec_proof/20260702_041739_fix1`.
  The initial run passed gates but emitted zero exec markers because the exec
  path was incorrectly nested under the early-trace env. Fix1 made exec
  detection depend on either exec or trace env. It is correctness-clean
  (`13/13` selected, `7/7` exec), canonical md5/op clean (MoE `8cb0ce23`,
  dense `5951a5b4`, `MUL_MAT 1146/1146`, `MUL_MAT_ID 806/806`), and emits
  `skip=4` markers for the six supported MoE rows. Perf is neutral for the
  target sentinel: `MOE_SWIGLU_DOWN n=128` `807.772 -> 806.051 us` (`+0.21%`)
  and `n=257` `1021.115 -> 1020.839 us` (`+0.03%`). Keep Phase121 as the
  executor ownership/skip-accounting proof only. The next real optimization
  phase should replace one internal boundary inside this helper, starting with
  route-plan reuse or activation-in-route-order, while preserving this md5/op
  contract.
  Phase122 tested route-plan reuse inside the Phase121 executor by exposing
  `ggml_cuda_mmq_ids_meta` and passing one built route to both `gate_up` and
  `down` MMQ calls behind `LLAMA_MOE_WHOLE_PATTERN_SHARED_ROUTE=1`. Artifact:
  `/home/mudler/bench/phase122_moe_shared_route_meta/20260702_043212`.
  Correctness was clean (`13/13` selected, `7/7` shared-route), but the target
  `MOE_SWIGLU_DOWN n=257` row regressed versus the Phase121 executor
  (`1020.850 -> 1051.666 us`, `-3.02%`) and `n=128` also missed the keep
  threshold (`808.190 -> 811.836 us`, `-0.45%`). The source was reverted,
  including the public MMQ metadata API. Post-reject gates on the reverted tree
  passed (`13/13` selected, `7/7` executor) with six retained Phase121 exec
  markers. Do not retry route-only metadata reuse; the next MoE executor phase
  should attack activation/down data layout, direct activation-to-down input,
  or a larger fused GEMM1->activation->GEMM2 boundary.
  Phase123 tested that direct activation-to-down input boundary inside the
  Phase121 executor. Artifact:
  `/home/mudler/bench/phase123_moe_executor_fused_down_input/20260702_025811`.
  The candidate added an NVFP4-only fused `silu(gate) * up -> down MMQ
  activation buffer` path behind
  `LLAMA_MOE_WHOLE_PATTERN_FUSED_DOWN=1`. Correctness passed (`13/13`
  selected, `7/7` fused-down, six fused markers), but perf was flat and missed
  the keep rule: versus Phase121 exec, `MOE_SWIGLU_DOWN n=128` was
  `811.153 -> 810.618 us` (`+0.07%`) and `n=257` was
  `1023.090 -> 1023.657 us` (`-0.06%`). Source was reverted; post-reject
  selected and Phase121 exec gates passed (`13/13`, `7/7`, six exec markers).
  Do not retry standalone fused-down quantization. The next MoE source attempt
  must either own the full expert-major packed pipeline
  `GEMM1->activation->GEMM2` or pivot to another measured bottleneck.
  Phase124 refreshed the current-stack graph-node serving profile after the
  Phase122/123 rejections. Artifact:
  `/home/mudler/bench/phase124_current_moe_profile/20260702_031205`.
  Pre/post gates were green (MoE md5 `8cb0ce23`, dense md5 `5951a5b4`,
  `MUL_MAT 1146/1146`, `MUL_MAT_ID 806/806`). Serving under graph-node
  profiling at `N=128`, prompt `128`, generation `64` was
  `agg_tps 206.2`, `decode_agg_tps 320.3`, `prefill_tps 1536.4`, wall
  `39.738s`. The fine buckets explain the Phase122/123 failures:
  `mmq_nvfp4` is now the largest fine bucket (`6074.78 ms`, `30.17%`) and
  `gdn_core` remains essentially tied (`5888.31 ms`, `29.25%`), while
  `act_quant` is only `674.88 ms` (`3.35%`). Next work should target either a
  full expert-major MoE pipeline that materially reduces `mmq_nvfp4` or a GDN
  source experiment that materially reduces `gdn_core`; one-boundary
  activation/route shortcuts are no longer funded. Phase125 scoping used two
  independent code explorers plus a local GDN audit. The challenged conclusion
  is that another GDN micro-patch is not funded: prior geometry/store/broadcast
  and conv-state attempts already exhausted the small safe space, while a
  useful GDN change would be a larger recurrence redesign. The next source
  attempt should therefore test the first maintainable slice of a vLLM-style
  expert-major MoE pipeline: a default-off MMQ sorted-output primitive that
  still uses expert bounds but writes sorted rows, then immediately unsorts as
  a proof. Only if that primitive is correctness clean and materially improves
  `MOE_SWIGLU_DOWN` should the following phase proceed to a full
  `gate_up -> SWIGLU -> down` expert-major executor.

### Phase141: GDN Decode-Only Noise Floor

- Date: 2026-07-02.
- Spec:
  `docs/superpowers/specs/2026-07-02-gdn-decode-noise-floor-phase141-design.md`.
- Plan:
  `docs/superpowers/plans/2026-07-02-gdn-decode-noise-floor-phase141.md`.
- Result type: measurement-only; no llama.cpp source changes.
- Artifact:
  `/home/mudler/bench/phase141_gdn_decode_noise_floor/20260702_090428`.
- Summary files:
  - `/home/mudler/bench/phase141_gdn_decode_noise_floor/20260702_090428/summary.tsv`
  - `/home/mudler/bench/phase141_gdn_decode_noise_floor/20260702_090428/runs.tsv`

Setup:

- Current patched Phase93 binary:
  `/home/mudler/llama-phase93-qwen3next-gqa-bcast/build/bin`.
- Env:
  `LLAMA_MOE_ROUTED_FFN_POC=1`,
  `LLAMA_MOE_ROUTED_FFN_FUSED_QUANT=1`,
  `LLAMA_MOE_ROUTED_FFN_FINALIZE_POC=1`.
- Harness:
  `/home/mudler/bench/phase77_moe_decode_only_profile.sh`.
- Shape:
  `N=128 N_PREDICT=2048 DEPTH_TARGET=64 CAPTURE_SECONDS=4 CTX=131072 PARALLEL=128 BATCH=2048 UBATCH=512`.

Gates:

- All five runs passed pre/post canonical gates:
  MoE md5 `8cb0ce23777bf55f92f63d0292c756b0`, dense md5
  `5951a5b4d624ce891e22ab5fca9bc439`, `MUL_MAT 1146/1146`, and
  `MUL_MAT_ID 806/806`.

Run summary:

| run | total kernel s | GDN ms | GDN launches | `gdn_core` ms | `gdn_core` launches | `gdn_core` ms/launch | `mmq_nvfp4` ms | `mmq_nvfp4` launches |
|-----|---------------:|-------:|-------------:|--------------:|--------------------:|---------------------:|---------------:|---------------------:|
| 1 | `3.553400` | `1500.210000` | `3000` | `1420.150000` | `600` | `2.366917` | `1315.460000` | `4816` |
| 2 | `3.708300` | `1492.230000` | `2994` | `1410.300000` | `598` | `2.358361` | `1470.550000` | `4801` |
| 3 | `3.678100` | `1566.780000` | `3150` | `1482.140000` | `630` | `2.352603` | `1336.250000` | `5061` |
| 4 | `3.698400` | `1495.970000` | `3000` | `1415.500000` | `600` | `2.359167` | `1458.510000` | `4820` |
| 5 | `3.620900` | `1490.630000` | `2985` | `1410.870000` | `597` | `2.363266` | `1389.990000` | `4784` |

Variance summary:

| metric | median | mean | stdev | CV | min | max |
|--------|-------:|-----:|------:|---:|----:|----:|
| `total_kernel_s` | `3.678100` | `3.651820` | `0.064600` | `1.769%` | `3.553400` | `3.708300` |
| `gdn_ms` | `1495.970000` | `1509.164000` | `32.419626` | `2.148%` | `1490.630000` | `1566.780000` |
| `gdn_core_ms` | `1415.500000` | `1427.792000` | `30.641160` | `2.146%` | `1410.300000` | `1482.140000` |
| `mmq_nvfp4_ms` | `1389.990000` | `1394.152000` | `69.894566` | `5.013%` | `1315.460000` | `1470.550000` |
| `gdn_core_ms_per_launch` | `2.359167` | `2.360063` | `0.005399` | `0.229%` | `2.352603` | `2.366917` |

Decision:

- Raw decode-only `gdn_core` is not a reliable keep/reject metric by itself
  unless capture launch counts are fixed; run 3 recorded `630` core launches
  while the other runs recorded `597..600`.
- For future GDN source A/B, require repeated medians and either:
  - raw `gdn_core` reduction above `max(2.0%, 3 * 30.641160 / 1415.500000) =
    6.49%`, or
  - launch-normalized `gdn_core_ms_per_launch` reduction above `2.0%`
    (`3 * 0.005399 / 2.359167 = 0.69%`, so the explicit floor dominates).
- This supports a very small default-off scalar gate/beta hoist probe if it can
  be kept bit-exact and measured per launch. It does not support large packed
  decode recurrence source work yet; that should wait for a broader spec.

### Phase140: GDN Decode Prep Trace

- Date: 2026-07-02.
- Spec:
  `docs/superpowers/specs/2026-07-02-gdn-decode-prep-trace-phase140-design.md`.
- Plan:
  `docs/superpowers/plans/2026-07-02-gdn-decode-prep-trace-phase140.md`.
- Result type: measurement-only; no llama.cpp source changes.
- Artifact:
  `/home/mudler/bench/phase140_gdn_decode_prep_trace/20260702_085348`.
- Summary file:
  `/home/mudler/bench/phase140_gdn_decode_prep_trace/20260702_085348/gdn_prep_kernel_summary.tsv`.

Setup:

- Current patched Phase93 binary:
  `/home/mudler/llama-phase93-qwen3next-gqa-bcast/build/bin`.
- Env:
  `LLAMA_MOE_ROUTED_FFN_POC=1`,
  `LLAMA_MOE_ROUTED_FFN_FUSED_QUANT=1`,
  `LLAMA_MOE_ROUTED_FFN_FINALIZE_POC=1`,
  plus route/layout trace envs.
- Shape:
  `N=128 PTOK=128 GEN=64 CTX=131072 PARALLEL=128 BATCH=2048 UBATCH=512`.

Gates:

| gate | MoE md5 | dense md5 | `MUL_MAT` | `MUL_MAT_ID` |
|------|---------|-----------|-----------|--------------|
| pre | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `1146/1146` | `806/806` |
| post | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `1146/1146` | `806/806` |

Serving/profile result:

| metric | value |
|--------|------:|
| `agg_tps` | `207.3` |
| `decode_agg_tps` | `328.9` |
| `decode_perseq_tps` | `2.11` |
| `prefill_tps` | `1490.6` |
| `ttft_mean_ms` | `8325.9` |
| `ttft_max_ms` | `14593.3` |
| `wall_s` | `39.501` |
| total kernel time | `20.2002 s` |

Key buckets:

| bucket | ms |
|--------|---:|
| `GDN` | `6673.66` |
| `gdn_core` | `5890.44` |
| `MoE/FFN-GEMM` | `6144.19` |
| `mmq_nvfp4` | `5918.31` |
| `gdn_conv` | `454.99` |
| `gdn_gather` | `227.92` |
| `gdn_l2norm` | `100.30` |
| `gdn_sigmoid` | `22.68` |

Focused kernel summary:

| kernel | count | ms | avg us |
|--------|------:|---:|-------:|
| `gated_delta_net_cuda` | `4650` | `5804.7074` | `1248.3242` |
| `k_bin_bcast` | `89426` | `1155.3901` | `12.9201` |
| `convert_unary` | `52060` | `659.7529` | `12.6729` |
| `concat_non_cont` | `2130` | `441.9353` | `207.4814` |
| `ssm_conv_update_ids_f32` | `2610` | `227.8964` | `87.3166` |
| `mul_mat_f` | `3670` | `227.7857` | `62.0669` |
| `ssm_conv_long_token_f32` | `1110` | `190.6664` | `171.7715` |
| `unary_gated_op_kernel` | `14340` | `184.3254` | `12.8539` |
| `rms_norm_gate_mul_f32` | `4740` | `170.0508` | `35.8757` |
| `rms_norm_f32` | `9798` | `114.3863` | `11.6745` |
| `rms_norm_pre_add_mul_f32` | `6160` | `108.2927` | `17.5800` |
| `cpy_scalar` | `5130` | `106.8951` | `20.8373` |
| `l2_norm_f32` | `9480` | `100.3024` | `10.5804` |
| `gated_delta_net_chunked_cuda` | `90` | `85.7367` | `952.6300` |

Decision:

- Reject an immediate in-GDN Q/K L2-normalization source patch for this shape.
- `l2_norm_f32` is above the absolute Phase139 noise floor
  (`3 * 17.8110 ms = 53.433 ms`) but only about `1.7%` of `gdn_core`, below
  the phase's `3%` materiality rule.
- Do not spend another phase on prep-only GDN micro-fusion unless a future
  profile shows prep kernels above the materiality gate.
- Next GDN work should be recurrence-level, packed-state, or datacenter
  Blackwell-specific, and still default-off with md5/op gates.

### Phase139: Serving Noise-Floor Repeat

- Date: 2026-07-02.
- Spec:
  `docs/superpowers/specs/2026-07-02-serving-noise-floor-phase139-design.md`.
- Plan:
  `docs/superpowers/plans/2026-07-02-serving-noise-floor-phase139.md`.
- Result type: measurement-only; no llama.cpp source changes.
- Artifact:
  `/home/mudler/bench/phase139_serving_noise_floor/20260702_081901`.
- Summary files:
  - `/home/mudler/bench/phase139_serving_noise_floor/20260702_081901/summary.tsv`
  - `/home/mudler/bench/phase139_serving_noise_floor/20260702_081901/runs.tsv`

Setup:

- Current patched Phase93 binary:
  `/home/mudler/llama-phase93-qwen3next-gqa-bcast/build/bin`.
- Env:
  `LLAMA_MOE_ROUTED_FFN_POC=1`,
  `LLAMA_MOE_ROUTED_FFN_FUSED_QUANT=1`,
  `LLAMA_MOE_ROUTED_FFN_FINALIZE_POC=1`.
- Shape:
  `N=128 PTOK=128 GEN=64 CTX=131072 PARALLEL=128 BATCH=2048 UBATCH=512`.
- Harness:
  `/home/mudler/bench/phase76_current_moe_profile.sh`.

Gates:

- All seven runs passed pre/post canonical gates:
  MoE md5 `8cb0ce23777bf55f92f63d0292c756b0`, dense md5
  `5951a5b4d624ce891e22ab5fca9bc439`, `MUL_MAT 1146/1146`, and
  `MUL_MAT_ID 806/806`.

Run summary:

| run | agg t/s | decode agg t/s | wall s | kernel s | MoE ms | mmq_nvfp4 ms | gdn_core ms | mmq_fixup ms | ew_add ms |
|-----|--------:|---------------:|-------:|---------:|-------:|-------------:|------------:|-------------:|----------:|
| 1 | `212.3` | `333.6` | `38.586` | `19.5196` | `5642.07` | `5464.17` | `5877.57` | `104.64` | `371.81` |
| 2 | `208.6` | `330.1` | `39.272` | `19.8779` | `5927.18` | `5719.41` | `5886.67` | `104.49` | `353.07` |
| 3 | `206.8` | `327.2` | `39.606` | `20.0228` | `5983.97` | `5756.85` | `5906.11` | `105.76` | `369.31` |
| 4 | `208.5` | `331.4` | `39.284` | `19.8543` | `5921.30` | `5702.74` | `5911.82` | `104.31` | `371.32` |
| 5 | `208.8` | `335.6` | `39.240` | `20.0571` | `5950.46` | `5720.96` | `5913.65` | `104.53` | `371.59` |
| 6 | `203.4` | `319.7` | `40.277` | `20.3933` | `6285.32` | `6049.05` | `5914.11` | `104.98` | `379.23` |
| 7 | `205.7` | `320.4` | `39.818` | `20.1422` | `6173.88` | `5978.03` | `5929.75` | `106.28` | `355.59` |

Variance summary:

| metric | median | mean | stdev | CV | min | max |
|--------|-------:|-----:|------:|---:|----:|----:|
| `agg_tps` | `208.5000` | `207.7286` | `2.8022` | `1.349%` | `203.4000` | `212.3000` |
| `decode_agg_tps` | `330.1000` | `328.2857` | `6.2157` | `1.893%` | `319.7000` | `335.6000` |
| `wall_s` | `39.2840` | `39.4404` | `0.5312` | `1.347%` | `38.5860` | `40.2770` |
| `kernel_s` | `20.0228` | `19.9810` | `0.2717` | `1.360%` | `19.5196` | `20.3933` |
| `moe_ms` | `5950.4600` | `5983.4543` | `204.9581` | `3.425%` | `5642.0700` | `6285.3200` |
| `mmq_nvfp4_ms` | `5720.9600` | `5770.1729` | `193.3642` | `3.351%` | `5464.1700` | `6049.0500` |
| `gdn_ms` | `6695.0800` | `6690.3629` | `17.4585` | `0.261%` | `6656.7100` | `6705.9100` |
| `gdn_core_ms` | `5911.8200` | `5905.6686` | `17.8110` | `0.302%` | `5877.5700` | `5929.7500` |
| `mmq_fixup_ms` | `104.6400` | `104.9986` | `0.7420` | `0.707%` | `104.3100` | `106.2800` |
| `ew_add_ms` | `371.3200` | `367.4171` | `9.4938` | `2.584%` | `353.0700` | `379.2300` |

Decision:

- Phase138 remains md5/op clean and focused-positive, but its one-off serving
  gain (`+0.63%` aggregate, `+0.24%` decode) is inside same-binary noise.
- Do not use Phase138's single serving run as evidence to stack another
  finalize/MMQ micro-patch.
- Future serving claims need repeated A/B medians and must exceed
  `max(2.0%, 3 * same-binary stdev)` on aggregate throughput. With this
  Phase139 stdev, that is materially higher than the Phase138 one-off delta.
- Bucket attribution also needs repeated evidence: the same binary had
  `mmq_nvfp4` CV `3.351%`, so a small MMQ movement is not enough. GDN was much
  steadier (`gdn_core` CV `0.302%`), making a measured GDN-side source attempt
  the more defensible next phase.

### Phase138 Attempt 2: Down-MMQ Finalize Writeback

- Date: 2026-07-02.
- Plan:
  `docs/superpowers/plans/2026-07-02-moe-down-mmq-finalize-phase138.md`.
- Result type: kept source candidate, default-off; narrow serving-positive
  result, not parity and not default-on.
- Focused artifact:
  `/home/mudler/bench/phase138_moe_down_mmq_finalize/20260702_095927_focused`.
- Canonical gate artifact:
  `/home/mudler/bench/phase138_moe_down_mmq_finalize/20260702_100202_canonical`.
- Serving/profile artifact:
  `/home/mudler/bench/phase138_moe_down_mmq_finalize_serving/20260702_100330`.
- Source files changed:
  - `ggml/src/ggml-cuda/ggml-cuda.cu`
  - `ggml/src/ggml-cuda/mmq.cu`
  - `ggml/src/ggml-cuda/mmq.cuh`
  - `ggml/src/ggml-cuda/moe-ffn.cu`
  - `ggml/src/ggml-cuda/moe-ffn.cuh`
  - `tests/test-backend-ops.cpp`

Implementation:

- Added default-off `LLAMA_MOE_ROUTED_FFN_FINALIZE_POC=1`, requiring both
  `LLAMA_MOE_ROUTED_FFN_POC=1` and
  `LLAMA_MOE_ROUTED_FFN_FUSED_QUANT=1`.
- Added a finalize helper that zeroes the final output, sends router weights
  and the final output pointer into the grouped down-MMQ path, and skips the
  strict weighted tail only after the helper is selected.
- Added optional finalize metadata to MMQ and stream-k/fixup writeback. The
  finalize branch uses the routed destination id to derive `(token, slot)` and
  atomically accumulates `sum * weight` into the final token row.
- Left all existing non-finalize MMQ call sites disabled-by-default.

Focused gates and trace:

| route | result |
|-------|--------|
| `MOE_SWIGLU_FINALIZE` default | `7/7` |
| `MOE_SWIGLU_FINALIZE` Phase135 opt-in | `7/7` |
| `MOE_SWIGLU_FINALIZE` Phase138 finalize opt-in | `7/7` |
| Phase138 exec trace | `6` records, `FINALIZE_EXEC skip=20 tail_nodes=16` |

Canonical gates on patched Phase93 binary:

| route | MoE md5 | dense md5 | `MUL_MAT` | `MUL_MAT_ID` |
|-------|---------|-----------|-----------|--------------|
| Phase138 via `EXTRA_ENV` | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `1146/1146` | `806/806` |

Focused perf:

| row | default | Phase135 | Phase138 finalize |
|-----|--------:|---------:|------------------:|
| `MOE_SWIGLU_FINALIZE nvfp4 n_tokens=128` | `198.021937 us` | `197.301518 us` | `187.134493 us` |
| `MOE_SWIGLU_FINALIZE nvfp4 n_tokens=257` | `429.235219 us` | `428.697087 us` | `384.673195 us` |

Serving comparison:

| metric | Phase135 opt-in | Phase138 finalize opt-in |
|--------|----------------:|--------------------------:|
| aggregate t/s | `208.0` | `209.3` |
| decode aggregate t/s | `332.7` | `333.5` |
| decode per-seq t/s | `2.12` | `2.13` |
| prefill t/s | `1475.1` | `1492.8` |
| TTFT mean | `8468.1 ms` | `8382.5 ms` |
| wall | `39.375 s` | `39.144 s` |
| total kernel time | `20.2498 s` | `20.0489 s` |

Serving buckets:

| bucket | Phase135 opt-in | Phase138 finalize opt-in |
|--------|----------------:|--------------------------:|
| `gdn_core` | `5926.55 ms` | `5914.04 ms` |
| `mmq_nvfp4` | `5915.24 ms` | `5802.87 ms` |
| `ew_mul` | `727.04 ms` | `723.65 ms` |
| `act_quant` | `677.59 ms` | `678.17 ms` |
| `get_rows` | `283.62 ms` | `283.80 ms` |
| `mmq_fixup` | `104.81 ms` | `106.06 ms` |
| `ew_add` | not listed in Phase135 top rows | `374.09 ms` |

Serving pre/post gates:

| phase | MoE md5 | dense md5 | `MUL_MAT` | `MUL_MAT_ID` |
|-------|---------|-----------|-----------|--------------|
| pre | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `1146/1146` | `806/806` |
| post | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `1146/1146` | `806/806` |

Decision:

- Keep Phase138 default-off. It passes md5/op gates and beats Phase135 on the
  configured keep thresholds: aggregate/decode throughput, total kernel time,
  and `mmq_nvfp4`.
- Do not promote/default-on. The serving delta is small and the weighted
  fan-in still appears as `ew_add 374.09 ms`, so this is not a complete tail
  removal and not parity.
- Next work should either reduce the remaining fan-in/writeback path more
  deeply, or pivot back to the two dominant buckets: `gdn_core` and
  `mmq_nvfp4`.

### Phase138 Attempt 1: MoE Finalize Trace And Full-Tail Sentinel

- Date: 2026-07-02.
- Plan:
  `docs/superpowers/plans/2026-07-02-moe-down-mmq-finalize-phase138.md`.
- Result type: kept trace/test scaffold, default-off; no runtime speedup claim.
- Trace-only `MOE_SWIGLU_DOWN` artifact:
  `/home/mudler/bench/phase138_moe_down_mmq_finalize_trace/20260702_092943`.
- Traced canonical gate artifact using the old default gate binary, superseded:
  `/home/mudler/bench/phase138_moe_down_mmq_finalize_trace/20260702_093003_gate`.
- Traced canonical gate artifact using patched Phase93 binary:
  `/home/mudler/bench/phase138_moe_down_mmq_finalize_trace/20260702_093141_gate_phase93`.
- Traced early-pattern gate artifact using patched Phase93 binary:
  `/home/mudler/bench/phase138_moe_down_mmq_finalize_trace/20260702_093243_gate_phase93_early`.
- Full-tail sentinel artifact:
  `/home/mudler/bench/phase138_moe_down_mmq_finalize_trace/20260702_093617_full_tail`.
- Canonical gate artifact:
  `/home/mudler/bench/phase138_moe_down_mmq_finalize_trace/20260702_093731_canonical`.
- Source files changed:
  - `ggml/src/ggml-cuda/ggml-cuda.cu`
  - `tests/test-backend-ops.cpp`

Implementation:

- Added default-off `LLAMA_MOE_ROUTED_FFN_FINALIZE_TRACE`.
- Added a trace-only strict tail scanner for
  `down -> MUL(weights) -> VIEW/ADD rank reduction`.
- Added `MOE_SWIGLU_FINALIZE`, a whole-graph backend-op sentinel that composes
  the existing `gate_up -> SWIGLU -> down` graph with the existing
  router-weighted rank-add tail.
- No production finalize/writeback kernel was added in this attempt.

Focused gates:

| route | result |
|-------|--------|
| `MOE_SWIGLU_DOWN` + Phase135 opt-in + finalize trace | `6` early records, `0` supported tail records |
| `MOE_SWIGLU_FINALIZE` default | `7/7` |
| `MOE_SWIGLU_FINALIZE` + Phase135 opt-in + finalize trace | `7/7`, `6` supported tail records |

Representative finalize trace row:

| field | value |
|-------|-------|
| `supported` | `1` |
| `tail_nodes` | `16` |
| `views` | `8` |
| `adds` | `7` |
| `down_ne` | `2048x8x128` on the 128-token row |
| `weights_ne` | `1x8x128` |
| `weights_nb` | `4,4,32` |
| `final_ne` | `2048x128x1` |
| `final_nb` | `4,8192,1048576` |

Canonical gates on patched Phase93 binary:

| MoE md5 | dense md5 | `MUL_MAT` | `MUL_MAT_ID` |
|---------|-----------|-----------|--------------|
| `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `1146/1146` | `806/806` |

Decision:

- Keep the trace/test scaffold as Phase138 groundwork.
- Proceed next to the default-off down-MMQ finalize/writeback implementation,
  but only against `MOE_SWIGLU_FINALIZE` first.
- Do not claim a speedup from this attempt; it only proves graph availability
  and preserves md5/op gates.

### Phase136: Routed-FFN Post-Down Weighted Combine

- Date: 2026-07-02.
- Plan:
  `docs/superpowers/plans/2026-07-02-routed-ffn-combine-phase136.md`.
- Result type: rejected source probe; source and sentinel test reverted.
- Focused artifact:
  `/home/mudler/bench/phase136_routed_ffn_combine/20260702_083727`.
- Serving/profile artifact:
  `/home/mudler/bench/phase136_routed_ffn_combine_serving/20260702_085749`.
- Source files tested and reverted:
  - `ggml/src/ggml-cuda/moe-ffn.cuh`
  - `ggml/src/ggml-cuda/moe-ffn.cu`
  - `ggml/src/ggml-cuda/ggml-cuda.cu`
  - `tests/test-backend-ops.cpp`

Implementation tested:

- Added `LLAMA_MOE_ROUTED_FFN_COMBINE=1` on top of Phase135.
- Extended the early routed-FFN graph hook to skip the post-down
  `MUL(weights) -> VIEW* -> ADD*` tail.
- Added a separate F32 weighted-combine kernel that preserved expert-rank
  accumulation order.
- Added a temporary full-tail `MOE_SWIGLU_COMBINE` sentinel for focused
  correctness/perf.

Focused gates:

| route | result |
|-------|--------|
| default selected + full-tail sentinel | `MOE_SWIGLU_DOWN,MOE_SWIGLU_COMBINE,MUL_MAT_ID_RAGGED_MOE 20/20` |
| Phase135 selected + full-tail sentinel | `20/20` |
| Phase136 selected + full-tail sentinel | `20/20` |
| Phase136 trace | `6` combine markers, `6` `mmq_moe_quantized_raw`, `0` `mmq_moe_sorted_raw` |
| post-reject Phase135 selected | `MOE_SWIGLU_DOWN,MUL_MAT_ID_RAGGED_MOE 13/13` |

Canonical focused gates:

| route | MoE md5 | dense md5 | `GATED_DELTA_NET` | `MUL_MAT` | `MUL_MAT_ID` |
|-------|---------|-----------|-------------------|-----------|--------------|
| Phase136 via `EXTRA_ENV` | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `46/46` | `1146/1146` | `806/806` |

Focused perf:

| row | default | Phase135 | Phase136 |
|-----|--------:|---------:|---------:|
| `MOE_SWIGLU_DOWN n_tokens=128` | `803.97 us` | `805.77 us` | `806.75 us` |
| `MOE_SWIGLU_DOWN n_tokens=257` | `1020.15 us` | `1016.53 us` | `1017.11 us` |
| `MOE_SWIGLU_COMBINE n_tokens=128` | `197.98 us` | `197.74 us` | `191.04 us` |
| `MOE_SWIGLU_COMBINE n_tokens=257` | `429.22 us` | `428.53 us` | `401.81 us` |

Serving/profile gate:

| phase | MoE md5 | dense md5 | `MUL_MAT` | `MUL_MAT_ID` |
|-------|---------|-----------|-----------|--------------|
| pre | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `1146/1146` | `806/806` |
| post | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `1146/1146` | `806/806` |

Serving metrics at Phase130 shape:

| metric | Phase135 opt-in | Phase136 opt-in |
|--------|----------------:|----------------:|
| aggregate t/s | `208.0` | `206.5` |
| decode aggregate t/s | `332.7` | `323.2` |
| decode per-seq t/s | `2.12` | `2.07` |
| prefill t/s | `1475.1` | `1519.5` |
| TTFT mean ms | `8468.1` | `8080.6` |
| wall s | `39.375` | `39.668` |
| total kernel time | `20.2498 s` | `19.9778 s` |

Serving fine buckets:

| bucket | Phase135 opt-in | Phase136 opt-in |
|--------|----------------:|----------------:|
| `mmq_nvfp4` | `5915.24 ms` | `5885.05 ms` |
| `gdn_core` | `5926.55 ms` | `5912.65 ms` |
| `cublas_bf16_gemm` | `1782.58 ms` | `1728.15 ms` |
| `cutlass_bf16_gemm` | `756.98 ms` | `767.94 ms` |
| `ew_mul` | `727.04 ms` | `712.97 ms` |
| `ew_add` | not listed in Phase135 top rows | `374.70 ms` |
| `act_quant` | `677.59 ms` | `677.60 ms` |
| `get_rows` | `283.62 ms` | `278.31 ms` |
| `mmq_fixup` | `104.81 ms` | `103.73 ms` |

Decision:

- Reject and revert Phase136. The focused synthetic full-tail row improved, but
  serving aggregate and decode throughput regressed versus Phase135.
- Keep Phase135 as the current default-off routed-FFN source base.
- Do not retry a separate post-MMQ weighted-combine launch next. A future
  combine/finalize attempt needs to remove a larger serving-visible boundary,
  likely by integrating finalize/writeback with the down projection or by
  changing graph scheduling enough to reduce launches without hurting decode.

### Phase137: GDN Geometry Sweep

- Date: 2026-07-02.
- Plan:
  `docs/superpowers/plans/2026-07-02-gdn-geometry-sweep-phase137.md`.
- Result type: rejected env-only serving probe; no source changes.
- Focused artifact:
  `/home/mudler/bench/phase137_gdn_geometry_sweep/20260702_091441`.
- Serving/profile artifact:
  `/home/mudler/bench/phase137_gdn_geometry_serving/20260702_091740`.

Implementation tested:

- No source edits.
- Swept existing `GDN_NW`/`GDN_CPW` runtime knobs:
  default `(16,8)`, `(8,8)`, `(16,4)`, `(8,4)`, and `(4,1)`.
- Ran serving only for the best focused candidate:
  `LLAMA_MOE_ROUTED_FFN_POC=1 LLAMA_MOE_ROUTED_FFN_FUSED_QUANT=1
  GDN_NW=4 GDN_CPW=1`.

Focused GDN perf:

| row | default | `8x8` | `16x4` | `8x4` | `4x1` |
|-----|--------:|------:|-------:|------:|------:|
| `hc=32,hs=128,nt=1,kda=0` | `6.793748 us` | `6.992506 us` | `6.161572 us` | `5.501046 us` | `4.713682 us` |
| `hc=32,hs=128,nt=1,kda=1` | `7.790557 us` | `7.639035 us` | `6.553847 us` | `5.772280 us` | `5.194275 us` |
| `hc=4,hs=128,nt=1,nseq=2,vrep=2,bcast=1` | `5.967364 us` | `4.721621 us` | `3.759859 us` | `3.747508 us` | `3.407998 us` |
| `hc=32,hs=128,nt=64,kda=0` | `153.718880 us` | `152.660797 us` | `119.964294 us` | `94.862477 us` | `125.016141 us` |
| `hc=32,hs=128,nt=256,kda=0` | `491.066095 us` | `678.143207 us` | `495.650551 us` | `454.202876 us` | `489.942166 us` |
| `hc=32,hs=128,nt=512,kda=0` | `1033.510463 us` | `2081.115639 us` | `1197.792952 us` | `1143.683921 us` | `1025.449339 us` |
| `hc=32,hs=128,nt=1024,kda=0` | `2060.529106 us` | `4382.363825 us` | `2403.995842 us` | `2310.580042 us` | `2060.707900 us` |
| `hc=4,hs=128,nt=64,kda=0` | `151.409035 us` | `142.777045 us` | `82.000488 us` | `78.839499 us` | `26.777607 us` |
| `hc=4,hs=128,nt=256,kda=0` | `102.606410 us` | `564.485714 us` | `311.945543 us` | `301.296947 us` | `102.232357 us` |
| `hc=4,hs=128,nt=512,kda=0` | `198.996831 us` | `1127.205870 us` | `620.111479 us` | `600.911809 us` | `198.595701 us` |
| `hc=4,hs=128,nt=1024,kda=0` | `396.210102 us` | `2249.487113 us` | `1240.201770 us` | `1200.476178 us` | `395.850039 us` |

Serving/profile gate:

| phase | MoE md5 | dense md5 | `MUL_MAT` | `MUL_MAT_ID` |
|-------|---------|-----------|-----------|--------------|
| pre | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `1146/1146` | `806/806` |
| post | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `1146/1146` | `806/806` |

Serving metrics at Phase130 shape:

| metric | Phase135 opt-in | Phase137 `GDN_NW=4 GDN_CPW=1` |
|--------|----------------:|-------------------------------:|
| aggregate t/s | `208.0` | `206.2` |
| decode aggregate t/s | `332.7` | `324.9` |
| decode per-seq t/s | `2.12` | `2.08` |
| prefill t/s | `1475.1` | `1499.4` |
| TTFT mean ms | `8468.1` | `8209.4` |
| TTFT max ms | not recorded | `14511.2` |
| wall s | `39.375` | `39.719` |
| total kernel time | `20.2498 s` | `20.7530 s` |

Serving fine buckets:

| bucket | Phase135 opt-in | Phase137 `GDN_NW=4 GDN_CPW=1` |
|--------|----------------:|-------------------------------:|
| `gdn_core` | `5926.55 ms` | `6466.27 ms` |
| `mmq_nvfp4` | `5915.24 ms` | `5978.87 ms` |
| `cublas_bf16_gemm` | `1782.58 ms` | `1726.10 ms` |
| `cutlass_bf16_gemm` | `756.98 ms` | `745.00 ms` |
| `ew_mul` | `727.04 ms` | `711.72 ms` |
| `ew_add` | not listed in Phase135 top rows | `367.85 ms` |
| `act_quant` | `677.59 ms` | `681.32 ms` |
| `get_rows` | `283.62 ms` | `284.31 ms` |
| `mmq_fixup` | `104.81 ms` | `103.26 ms` |

Decision:

- Reject Phase137. The isolated 1-token GDN rows improved, but real serving
  decode, aggregate throughput, total kernel time, `gdn_core`, and `mmq_nvfp4`
  all regressed versus Phase135.
- Do not edit source for a GDN launch-geometry retune.
- Next scoped source line: a default-off MoE finalize/writeback integration in
  down-MMQ that removes the serving-visible `MUL(weights) -> VIEW* -> ADD*`
  tail without adding a standalone combine launch.

### Phase135: Routed-FFN Fused SWIGLU-to-NVFP4 Quant

- Date: 2026-07-02.
- Plan:
  `docs/superpowers/plans/2026-07-02-routed-ffn-fused-quant-phase135.md`.
- Result type: source structural base, default-off, serving-profile positive on
  decode but not parity-closing.
- Focused artifact:
  `/home/mudler/bench/phase135_routed_ffn_fused_quant/20260702_081723`.
- Serving/profile artifact:
  `/home/mudler/bench/phase135_routed_ffn_fused_quant_serving/20260702_082102`.
- Source files:
  - `ggml/src/ggml-cuda/mmq.cuh`
  - `ggml/src/ggml-cuda/mmq.cu`
  - `ggml/src/ggml-cuda/moe-ffn.cu`

Implementation:

- Added `LLAMA_MOE_ROUTED_FFN_FUSED_QUANT=1` on top of
  `LLAMA_MOE_ROUTED_FFN_POC=1`.
- Added `ggml_cuda_mul_mat_q_moe_quantized(...)`, a raw MMQ launcher that
  accepts a caller-owned quantized activation buffer.
- Added a Blackwell/NVFP4-only fused kernel that reads `gate/up` views, uses
  the existing ids metadata ordering, computes `silu(gate) * up`, and writes
  `block_fp4_mmq` activation layout directly.
- MXFP4 and unsupported shapes fall back to earlier paths.

Focused gates:

| route | result |
|-------|--------|
| Phase135 selected | `MOE_SWIGLU_DOWN,MUL_MAT_ID_RAGGED_MOE 13/13` |
| Phase135 trace | `6` `mmq_moe_quantized_raw` launches, `0` `mmq_moe_sorted_raw` launches |

Canonical focused gates:

| route | MoE md5 | dense md5 | `GATED_DELTA_NET` | `MUL_MAT` | `MUL_MAT_ID` |
|-------|---------|-----------|-------------------|-----------|--------------|
| Phase135 via `EXTRA_ENV` | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `48/48` | `1146/1146` | `806/806` |

Focused perf:

| row | default | Phase134 | Phase135 |
|-----|--------:|---------:|---------:|
| `MOE_SWIGLU_DOWN n_tokens=128` | `805.920354 us` | `807.650845 us` | `807.921963 us` |
| `MOE_SWIGLU_DOWN n_tokens=257` | `1031.064815 us` | `1027.513292 us` | `1024.971370 us` |

Serving/profile gate:

| phase | MoE md5 | dense md5 | `MUL_MAT` | `MUL_MAT_ID` |
|-------|---------|-----------|-----------|--------------|
| pre | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `1146/1146` | `806/806` |
| post | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `1146/1146` | `806/806` |

Serving metrics at Phase130 shape:

| metric | Phase130 default | Phase135 opt-in |
|--------|-----------------:|----------------:|
| aggregate t/s | `208.0` | `208.0` |
| decode aggregate t/s | `326.9` | `332.7` |
| decode per-seq t/s | `2.1` | `2.12` |
| prefill t/s | `1519.6` | `1475.1` |
| TTFT mean ms | `8170.6` | `8468.1` |
| wall s | `39.38` | `39.375` |
| total kernel time | `20.1559 s` | `20.2498 s` |

Serving fine buckets:

| bucket | Phase130 default | Phase135 opt-in |
|--------|-----------------:|----------------:|
| `mmq_nvfp4` | `6009.52 ms` | `5915.24 ms` |
| `gdn_core` | `5891.40 ms` | `5926.55 ms` |
| `cublas_bf16_gemm` | `1735.98 ms` | `1782.58 ms` |
| `cutlass_bf16_gemm` | `749.64 ms` | `756.98 ms` |
| `act_quant` | `675.67 ms` | `677.59 ms` |
| `get_rows` | `280.62 ms` | `283.62 ms` |
| `mmq_fixup` | not listed in Phase130 top rows | `104.81 ms` |

Decision:

- Keep Phase135 as the best current default-off routed-FFN base. It is
  canonical-clean and reduces the dominant `mmq_nvfp4` serving bucket.
- Do not promote it as parity: aggregate serving is unchanged, prefill/TTFT are
  worse, and total kernel time is slightly higher due to other buckets.
- Next work should target remaining MoE overhead after fused quant, especially
  `mmq_fixup`, route/writeback, and weighted-combine/scatter boundaries, or run
  a broader serving comparison to determine whether the decode improvement
  persists outside this graph-node profile.

### Phase134: Routed-FFN Fused SWIGLU-to-Sorted

- Date: 2026-07-02.
- Plan:
  `docs/superpowers/plans/2026-07-02-routed-ffn-fused-swiglu-phase134.md`.
- Result type: source structural base, default-off, mixed perf.
- Artifact:
  `/home/mudler/bench/phase134_routed_ffn_fused_swiglu/20260702_075828`.
- Source files:
  - `ggml/src/ggml-cuda/moe-ffn.cuh`
  - `ggml/src/ggml-cuda/moe-ffn.cu`
  - `ggml/src/ggml-cuda/ggml-cuda.cu`

Implementation:

- Added `LLAMA_MOE_ROUTED_FFN_FUSED_SWIGLU=1` on top of
  `LLAMA_MOE_ROUTED_FFN_POC=1`.
- Passes `gate` and `up` views into the Phase132 routed-FFN helper.
- Executes `gate_up`, builds ids metadata, launches a CUDA kernel to write
  `silu(gate) * up` directly into expert-sorted F32 rows, then calls Phase133's
  raw sorted-F32 down MMQ helper.
- The fused flag now implies the sorted-down machinery; it does not require
  `LLAMA_MOE_ROUTED_FFN_SORTED_DOWN=1`.

Selected and trace gates:

| route | result |
|-------|--------|
| Phase134 selected | `MOE_SWIGLU_DOWN,MUL_MAT_ID_RAGGED_MOE 13/13` |
| Phase134 trace | `MOE_SWIGLU_DOWN 7/7`, `6` `mmq_moe_sorted_raw` launches |

Canonical gates:

| route | MoE md5 | dense md5 | `GATED_DELTA_NET` | `MUL_MAT` | `MUL_MAT_ID` |
|-------|---------|-----------|-------------------|-----------|--------------|
| Phase134 via `EXTRA_ENV` | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `48/48` | `1146/1146` | `806/806` |

Focused perf sanity:

| row | default | Phase132 | Phase133 | Phase134 |
|-----|--------:|---------:|---------:|---------:|
| `MOE_SWIGLU_DOWN n_tokens=128` | `804.920354 us` | `807.999195 us` | `808.068383 us` | `810.614642 us` |
| `MOE_SWIGLU_DOWN n_tokens=257` | `1026.024540 us` | `1028.434560 us` | `1029.015432 us` | `1025.682004 us` |

Decision:

- Keep Phase134 only as default-off structural plumbing. It removes the
  standalone `glu -> get_rows` boundary and recovers the n=257 regression, but
  the extra fused-SWIGLU kernel is still slower at n=128.
- Do not promote `LLAMA_MOE_ROUTED_FFN_FUSED_SWIGLU=1` as a speedup.
- Next work must remove one more boundary, likely by fusing SWIGLU directly
  into the down-MMQ quant buffer rather than writing an intermediate sorted F32
  buffer.

### Phase133: Routed-FFN Sorted-Down Raw MMQ

- Date: 2026-07-02.
- Plan:
  `docs/superpowers/plans/2026-07-02-routed-ffn-sorted-down-phase133.md`.
- Result type: source structural base, default-off, not a speedup.
- Artifact:
  `/home/mudler/bench/phase133_routed_ffn_sorted_down/20260702_074651`.
- Source files:
  - `ggml/src/ggml-cuda/mmq.cuh`
  - `ggml/src/ggml-cuda/mmq.cu`
  - `ggml/src/ggml-cuda/moe-ffn.cu`

Implementation:

- Exposed `ggml_cuda_mmq_ids_meta` from `mmq.cuh` so the routed-FFN helper can
  reuse the existing GPU ids metadata (`ids_src1`, `ids_dst`, `expert_bounds`).
- Added `ggml_cuda_mul_mat_q_moe_sorted_f32(...)`, a raw sorted-F32 MMQ entry
  that accepts a compact F32 activation pointer plus `ids_dst` and
  `expert_bounds` directly.
- Added `LLAMA_MOE_ROUTED_FFN_SORTED_DOWN=1` on top of
  `LLAMA_MOE_ROUTED_FFN_POC=1`. The opt-in path executes baseline `gate_up` and
  `SWIGLU`, gathers `SWIGLU` output into compact expert-sorted F32 rows, then
  runs the raw MMQ down helper. It falls back to Phase132 if strict shape/type
  checks fail.

Selected op gates:

| route | result | marker |
|-------|--------|--------|
| default | `MOE_SWIGLU_DOWN,MUL_MAT_ID_RAGGED_MOE 13/13` | none |
| Phase132 `LLAMA_MOE_ROUTED_FFN_POC=1` | `13/13` | `6` whole-pattern exec markers |
| Phase133 `LLAMA_MOE_ROUTED_FFN_POC=1 LLAMA_MOE_ROUTED_FFN_SORTED_DOWN=1` | `13/13` | `6` whole-pattern exec markers |

Trace proof:

- `LLAMA_QUANT_TRACE=32` with Phase133 opt-in passed `MOE_SWIGLU_DOWN 7/7`.
- `grep -c mmq_moe_sorted_raw phase133_quant_trace.log` returned `6`, proving
  the raw sorted-down helper engaged for the NVFP4 rows.

Canonical gates:

| route | MoE md5 | dense md5 | `GATED_DELTA_NET` | `MUL_MAT` | `MUL_MAT_ID` |
|-------|---------|-----------|-------------------|-----------|--------------|
| default | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `48/48` | `1146/1146` | `806/806` |
| Phase133 via `EXTRA_ENV` | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `48/48` | `1146/1146` | `806/806` |

Focused perf sanity:

| row | default | Phase132 | Phase133 |
|-----|--------:|---------:|---------:|
| `MOE_SWIGLU_DOWN n_tokens=128` | `807.369268 us` | `808.213194 us` | `808.848753 us` |
| `MOE_SWIGLU_DOWN n_tokens=257` | `1020.762195 us` | `1018.870935 us` | `1026.874233 us` |

Decision:

- Keep Phase133 only as default-off structural plumbing. It is correctness-clean
  and proves the fake-tensor boundary can be replaced with a raw helper, but it
  adds a separate gather into sorted F32 rows and is not faster.
- Do not promote `LLAMA_MOE_ROUTED_FFN_SORTED_DOWN=1` as a runtime speedup.
- Next work must remove the new overhead by fusing SWIGLU directly into sorted
  rows or directly into the down-MMQ quant buffer. A standalone sorted-down
  gather is not a parity lever.

### Phase132: Default-Off Routed-FFN PoC Scaffold

- Date: 2026-07-02.
- Plan:
  `docs/superpowers/plans/2026-07-02-routed-ffn-poc-phase132.md`.
- Result type: source scaffold, default-off, no math change intended.
- Artifact:
  `/home/mudler/bench/phase132_routed_ffn_poc/20260702_072725`.
- Source files:
  - `ggml/src/ggml-cuda/moe-ffn.cuh`
  - `ggml/src/ggml-cuda/moe-ffn.cu`
  - `ggml/src/ggml-cuda/ggml-cuda.cu`

Build:

- First incremental build failed at link because the existing CMake build
  directory had not reconfigured its globbed CUDA source list, so the new
  `moe-ffn.cu` object was not compiled.
- Re-running `cmake -S . -B build` in the DGX mirror picked up `moe-ffn.cu`;
  `cmake --build build --target test-backend-ops -j"$(nproc)"` then passed.
- Symbol/string evidence:
  `strings build/bin/libggml-cuda.so | grep -c LLAMA_MOE_ROUTED_FFN_POC`
  returned `1`.

Selected op gates:

| route | result | trace |
|-------|--------|-------|
| default | `MOE_SWIGLU_DOWN,MUL_MAT_ID_RAGGED_MOE 13/13` | no opt-in markers |
| `LLAMA_MOE_ROUTED_FFN_POC=1` | `MOE_SWIGLU_DOWN,MUL_MAT_ID_RAGGED_MOE 13/13` | `6` `LLAMA_MOE_WHOLE_PATTERN_EXEC` markers |

Canonical gates:

| route | MoE md5 | dense md5 | `GATED_DELTA_NET` | `MUL_MAT` | `MUL_MAT_ID` |
|-------|---------|-----------|-------------------|-----------|--------------|
| default | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `48/48` | `1146/1146` | `806/806` |
| `LLAMA_MOE_ROUTED_FFN_POC=1` via `EXTRA_ENV` | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `48/48` | `1146/1146` | `806/806` |

Focused perf sanity:

| row | default | opt-in | delta |
|-----|--------:|-------:|------:|
| `MOE_SWIGLU_DOWN n_tokens=128` | `808.318584 us` | `804.868061 us` | `+0.43%` |
| `MOE_SWIGLU_DOWN n_tokens=257` | `1023.355828 us` | `1022.713701 us` | `+0.06%` |

Decision:

- Keep the Phase132 scaffold. It is correctness-clean and neutral, and it gives
  the next patch a low-conflict helper boundary for a real fused routed-FFN
  slice.
- Do not present Phase132 as a speedup. The helper currently executes the same
  baseline `gate_up`, `SWIGLU`, and `down` nodes; it only proves default-off
  ownership, capability gating, and reachability.
- Next source phase should replace one internal helper boundary with real work,
  preferably a routed-FFN packed workspace or direct sorted activation/down
  path that removes more traffic than Phase116/123.

### Phase131: Fused Routed-FFN Scoping Challenge

- Date: 2026-07-02.
- Plan:
  `docs/superpowers/plans/2026-07-02-fused-routed-ffn-phase131.md`.
- Result type: source-selection and design-gate phase; no source changes and no
  DGX benchmark artifact.
- Inputs:
  - Phase130 current-stack serving profile:
    `/home/mudler/bench/phase130_current_stack_profile/20260702_070949`.
  - MoE explorer: `019f2140-de84-7eb2-8ab5-0c7d7de336bd`.
  - GDN explorer: `019f2141-0af2-7480-bf66-4fd7e67716c5`.

Decision:

- Reject another incremental MoE/FFN-GEMM shortcut for Phase131. The current
  stack already includes default grouped FP4-MMQ, default-off W4A16 fallback
  routes, route metadata scaffolding, and whole-pattern executor ownership
  proof. Prior route-only, activation-only, tile-policy, W4A16, sorted-output,
  and fake-executor attempts either regressed or were noise-level.
- Reject another incremental GDN shortcut for Phase131. The remaining GDN bucket
  is dominated by the f32 recurrent-state scan; the safe space around launch
  geometry, gather/identity, producer fusion, store fusion, BF16 S-cache, and
  grouped Q/K broadcast has already been tested and rejected under canonical
  md5/KL gates.
- Continue only with a larger default-off fused routed-FFN PoC if the vLLM and
  llama.cpp audits identify a concrete low-conflict hook. Otherwise, require a
  standalone CUDA PoC before touching llama.cpp source.

Gates:

- No correctness or performance gates were run for this no-source decision
  phase.
- Any follow-up source phase must use the canonical MoE md5
  `8cb0ce23777bf55f92f63d0292c756b0`, dense md5
  `5951a5b4d624ce891e22ab5fca9bc439`, `GATED_DELTA_NET`, `MUL_MAT 1146/1146`,
  `MUL_MAT_ID 806/806`, and selected `MOE_SWIGLU_DOWN,MUL_MAT_ID_RAGGED_MOE`
  op gates before claiming a speedup.

### Phase130: Current-Stack Serving Profile Refresh

- Date: 2026-07-02.
- Plan:
  `docs/superpowers/plans/2026-07-02-current-stack-serving-profile-phase130.md`.
- Result type: measurement-only profile; no source changes.
- Artifact:
  `/home/mudler/bench/phase130_current_stack_profile/20260702_070949`.
- Shape: MoE `q36-35b-a3b-nvfp4`, `N=128`, prompt `128`, generation `64`,
  `PARALLEL=128`, `CTX=131072`, graph-node CUDA tracing.

Gates:

| phase | MoE md5 | dense md5 | `MUL_MAT` | `MUL_MAT_ID` |
|-------|---------|-----------|-----------|--------------|
| pre | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `1146/1146` | `806/806` |
| post | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `1146/1146` | `806/806` |

Serving metrics:

| metric | value |
|--------|------:|
| aggregate t/s | `208.0` |
| decode aggregate t/s | `326.9` |
| decode per-seq t/s | `2.1` |
| prefill t/s | `1519.6` |
| TTFT mean ms | `8170.6` |
| TTFT max ms | `14315.6` |
| wall s | `39.38` |
| total kernel time | `20.1559 s` |

Macro buckets:

| bucket | time | share |
|--------|-----:|------:|
| GDN | `6646.64 ms` | `32.98%` |
| MoE/FFN-GEMM | `6213.70 ms` | `30.83%` |
| bf16/fp8-proj | `2734.06 ms` | `13.56%` |
| layout-copy | `1260.74 ms` | `6.25%` |
| act-quant | `675.67 ms` | `3.35%` |
| gather | `280.62 ms` | `1.39%` |
| FA | `267.02 ms` | `1.32%` |

Fine buckets:

| bucket | time | share |
|--------|-----:|------:|
| `mmq_nvfp4` | `6009.52 ms` | `29.82%` |
| `gdn_core` | `5891.40 ms` | `29.23%` |
| `cublas_bf16_gemm` | `1735.98 ms` | `8.61%` |
| `cutlass_bf16_gemm` | `749.64 ms` | `3.72%` |
| `act_quant` | `675.67 ms` | `3.35%` |
| `convert_dtype` | `656.25 ms` | `3.26%` |
| `concat_layout` | `443.94 ms` | `2.20%` |
| `gdn_conv` | `443.80 ms` | `2.20%` |
| `get_rows` | `280.62 ms` | `1.39%` |
| `fa` | `257.38 ms` | `1.28%` |

Decision:

- The current serving profile remains a tied two-bucket problem:
  `mmq_nvfp4` and `gdn_core` are effectively equal and far larger than every
  candidate cleanup bucket.
- Do not spend the next source attempt on paged mask/F16 get-rows or FA cleanup:
  `get_rows` and FA are below `1.5%` each in this profile, matching the older
  Phase63 no-go.
- The next credible source attempt must either reduce the MoE/FFN-GEMM bucket
  with a larger executor/kernel than the rejected route/activation shortcuts, or
  reduce GDN with a materially different recurrent-state/packed-decode design
  rather than the rejected grouped-broadcast/BF16-cache/geometry/store shapes.

### Phase129: Qwen35 GDN Q/K Grouped Broadcast Probe

- Date: 2026-07-02.
- Plan:
  `docs/superpowers/plans/2026-07-02-qwen35-gdn-qk-grouped-bcast-phase129.md`.
- Result type: source attempted, rejected, and reverted.
- Default gate artifact:
  `/home/mudler/bench/phase129_qwen35_gdn_qk_bcast/default_20260702_065445`.
- Focused GDN perf artifact:
  `/home/mudler/bench/phase129_qwen35_gdn_qk_bcast/perf_20260702_065728`.
- Default decode-profile artifact:
  `/home/mudler/bench/phase129_qwen35_gdn_qk_bcast/decode_default_20260702_065847`.
- Valid opt-in reject artifact:
  `/home/mudler/bench/phase129_qwen35_gdn_qk_bcast/decode_optin_20260702_070149/gate_pre`.
- Post-reject artifact:
  `/home/mudler/bench/phase129_qwen35_gdn_qk_bcast/post_reject_20260702_070258`.
- Candidate env:
  `LLAMA_QWEN35_GDN_QK_BCAST=1`.

Candidate implementation:

- Added a default-off `qk_bcast_grouped` branch to `src/models/qwen35.cpp` and
  `src/models/qwen35moe.cpp`.
- When enabled, the branch skipped explicit Q/K repeat and called the
  state-taking `build_recurrent_attn(..., state, il, true)` overload so the
  existing `ggml_gated_delta_net_set_bcast()` op parameter could use grouped
  Q/K indexing.
- Default source behavior remained unchanged when the env was unset.

Evidence:

- Default canonical gates passed:
  - MoE md5 `8cb0ce23777bf55f92f63d0292c756b0`;
  - dense md5 `5951a5b4d624ce891e22ab5fca9bc439`;
  - `GATED_DELTA_NET 46/46`;
  - `MUL_MAT 1146/1146`;
  - `MUL_MAT_ID 806/806`.
- The first standalone opt-in gate artifact
  `/home/mudler/bench/phase129_qwen35_gdn_qk_bcast/optin_20260702_065604`
  was not valid evidence because `paged-inference-gates.sh` only injects model
  env through `EXTRA_ENV`.
- The valid opt-in gate from the decode harness used
  `PROFILE_ENV="LLAMA_QWEN35_GDN_QK_BCAST=1"` and failed before profiling:
  MoE md5 became `b773e2f032aa0e992626d486b321808e` instead of the canonical
  `8cb0ce23777bf55f92f63d0292c756b0`.
- Focused `test-backend-ops perf -o GATED_DELTA_NET` was effectively neutral
  because it exercises op fixtures, not the Qwen35 model-builder branch. The
  representative rows were:

| row | default us/run | opt-in us/run |
|-----|---------------:|--------------:|
| `head_count=32,head_size=128,n_seq_tokens=1024,qk_bcast_grouped=0` | `2064.48` | `2060.23` |
| `head_count=4,head_size=128,n_seq_tokens=256,qk_bcast_grouped=0` | `101.69` | `101.61` |
| `head_count=4,head_size=128,n_seq_tokens=64,v_repeat=2,qk_bcast_grouped=1` | `151.32` | `151.39` |

- Default decode-profile baseline, before the valid opt-in reject:

| metric | default |
|--------|--------:|
| total kernel time | `3.6916 s` |
| GDN macro | `1491.99 ms` (`40.42%`) |
| `gdn_core` | `1411.34 ms` (`38.23%`) |
| MoE/FFN-GEMM macro | `1475.96 ms` (`39.98%`) |
| `mmq_nvfp4` | `1458.54 ms` (`39.51%`) |

- Post-reject rebuild removed the env string from `libllama.so`
  (`strings ... | grep -c LLAMA_QWEN35_GDN_QK_BCAST == 0`) and post-reject
  gates passed: MoE md5 canonical, dense md5 canonical, `GATED_DELTA_NET 46/46`,
  `MUL_MAT 1146/1146`, `MUL_MAT_ID 806/806`.

Decision:

- Reject and revert Phase129 source. The candidate is not bit-exact for the
  current `qwen35moe` decision model.
- Do not retry the same Qwen3Next grouped Q/K broadcast port for Qwen35 or
  Qwen35MoE unless the quality rule is explicitly changed. The current
  bit-exact md5 gate rejects it before any perf profile is meaningful.

### Phase128: Qwen3Next GDN BF16 S-Cache Scope

- Date: 2026-07-02.
- Plan:
  `docs/superpowers/plans/2026-07-02-qwen3next-gdn-bf16-s-cache-phase128.md`.
- Result type: source probe rejected and reverted.
- Default gate artifact:
  `/home/mudler/bench/phase128_qwen3next_gdn_bf16_s_cache/default_20260702_043939`.
- Verbose smoke artifact:
  `/home/mudler/bench/phase128_qwen3next_gdn_bf16_s_cache/smoke3_20260702_044434`.

Candidate implementation:

- Temporarily generalized the Qwen35/Qwen35MoE GDN S-cache selector in
  `src/llama-model.cpp` to accept
  `LLAMA_QWEN3NEXT_GDN_S_CACHE_TYPE=bf16` for `LLM_ARCH_QWEN3NEXT`.
- Preserved the existing `LLAMA_QWEN35_GDN_S_CACHE_TYPE=bf16` behavior.
- Reverted the source probe after validation showed it does not apply to the
  current decision model and no true Qwen3Next artifact is available.

Evidence:

- Default `GATED_DELTA_NET` op gate passed `48/48`.
- Default canonical gates passed:
  - MoE md5 `8cb0ce23777bf55f92f63d0292c756b0`;
  - dense md5 `5951a5b4d624ce891e22ab5fca9bc439`;
  - `MUL_MAT` passed;
  - `MUL_MAT_ID` passed.
- Verbose smoke showed the active model metadata:
  `general.architecture = qwen35moe`, `print_info: arch = qwen35moe`.
- With `LLAMA_QWEN3NEXT_GDN_S_CACHE_TYPE=bf16`, recurrent cache logs still
  showed `S (f32): 60.00 MiB`, as expected for a `qwen35moe` model.
- DGX search found no true Qwen3Next GGUF under `/home/mudler/bench` or
  `/home/mudler`.

Decision:

- Reject and revert the Qwen3Next selector change for the current parity run.
- Do not retry the existing Qwen35/Qwen35MoE BF16 S-cache lever under the
  current rules: Phase81 showed it reduced `gdn_core`, but Phase82 rejected it
  because MoE md5 changed and the full f16-reference KL gate missed the hard
  acceptance band.
- A future BF16-S-cache attempt needs either a deliberately re-scoped quality
  gate or an actual Qwen3Next model artifact to validate.

### Phase127: Whole-MoE Expert-Major Executor

- Date: 2026-07-02.
- Plan:
  `docs/superpowers/plans/2026-07-02-moe-whole-expert-major-phase127.md`.
- Result type: source attempted, rejected, and reverted. Phase126 helper
  remains.
- Red artifact:
  `/home/mudler/bench/phase127_moe_whole_expert_major/red_20260702_042125`.
- Green artifact:
  `/home/mudler/bench/phase127_moe_whole_expert_major/green2_20260702_042916`.
- Perf artifact:
  `/home/mudler/bench/phase127_moe_whole_expert_major/perf_20260702_043104`.
- Post-reject artifact:
  `/home/mudler/bench/phase127_moe_whole_expert_major/post_reject_20260702_043318`.
- Candidate env:
  `LLAMA_MOE_WHOLE_EXPERT_MAJOR=1 LLAMA_MOE_WHOLE_EXPERT_MAJOR_TRACE=128`.

Candidate implementation:

- Added an opt-in executor at the existing early whole-pattern match.
- Built route metadata once with `ggml_cuda_launch_mm_ids_helper()`.
- Wrote `gate_up` to a sorted F32 temporary using identity `ids_dst`.
- Ran SWIGLU on a fake contiguous split-half `[2*n_ff, ne_get_rows]` tensor.
- Ran down MMQ from sorted activations through the Phase126
  `ggml_cuda_mul_mat_q_moe_with_ids(..., src1_sorted=true)` helper.
- Unpermuted once after down into the real graph destination.

Attempt notes:

- The red gate passed by fallback and emitted zero
  `LLAMA_MOE_WHOLE_EXPERT_MAJOR` markers.
- First green attempt aborted because the executor interpreted `down_w` as
  `[n_embd, n_ff, experts]`. Debug trace proved the correct shape is
  `[n_ff, n_embd, experts]`; the dimension fix made the selected green gate
  pass.

Gates:

| gate | result |
|------|--------|
| red `MOE_SWIGLU_DOWN` | `7/7`, zero expert-major markers |
| default selected `MOE_SWIGLU_DOWN,MUL_MAT_ID_RAGGED_MOE` | `13/13` |
| opt-in `MOE_SWIGLU_DOWN` | `7/7`, six expert-major markers |
| candidate canonical md5/op | skipped because perf rejected source |
| post-reject selected `MOE_SWIGLU_DOWN,MUL_MAT_ID_RAGGED_MOE` | `13/13` |
| post-reject MoE md5 | `8cb0ce23777bf55f92f63d0292c756b0` |
| post-reject dense md5 | `5951a5b4d624ce891e22ab5fca9bc439` |
| post-reject `MUL_MAT` | `1146/1146` |
| post-reject `MUL_MAT_ID` | `806/806` |

Focused perf:

| arm | `MOE_SWIGLU_DOWN n=128` | `MUL_MAT_ID_RAGGED_MOE n=128` | `MOE_SWIGLU_DOWN n=257` | `MUL_MAT_ID_RAGGED_MOE n=257` |
|-----|-------------------------:|--------------------------------:|-------------------------:|--------------------------------:|
| default | `802.57 us` | `1236.67 us` | `1023.25 us` | `1455.65 us` |
| expert-major opt-in | `812.14 us` | `1238.50 us` | `1039.36 us` | `1455.06 us` |

Decision:

- Reject and revert Phase127 source. The path passed correctness but missed the
  keep rule: `MOE_SWIGLU_DOWN n=128` regressed about `1.2%` and `n=257`
  regressed about `1.6%`; no row reached the required `>=3%` improvement.
- Do not retry the same fake-tensor whole-executor shape. It removes the early
  unsort boundary but adds enough temporary traffic and quant/layout work to
  lose on the focused rows. The next MoE attempt must reduce temporary traffic
  or move closer to a real fused grouped MMQ/SWIGLU/down path; otherwise pivot
  to the scoped GDN BF16 S-cache experiment with non-md5 numerical gates.

### Phase126: MMQ Presorted Helper Scaffold

- Date: 2026-07-02.
- Plan:
  `docs/superpowers/plans/2026-07-02-mmq-presorted-helper-phase126.md`.
- Result type: source scaffold kept; no default behavior change intended.
- Artifact:
  `/home/mudler/bench/phase126_mmq_presorted_helper/fix1_20260702_040858`.
- Source scope:
  - `ggml/src/ggml-cuda/mmq.cu`
  - `ggml/src/ggml-cuda/mmq.cuh`
- Candidate implementation:
  - refactored the current MoE `ggml_cuda_mul_mat_q()` id path into an
    internal helper that accepts prebuilt `ids_src1`, `ids_dst`, and
    `expert_bounds`;
  - added the public CUDA-internal wrapper
    `ggml_cuda_mul_mat_q_moe_with_ids(..., bool src1_sorted)`;
  - preserved current behavior by having the existing path build metadata and
    call the helper with `src1_sorted=false`;
  - added `src1_sorted=true` support for the future whole-MoE executor without
    wiring that executor in this phase.

Attempt notes:

- Initial Phase126 build/gate attempt compiled and selected gates passed, but
  local review found the helper had widened the default MMQ q-buffer stride from
  `n_expert_used` to `ne_get_rows`. The fix1 attempt restored the old stride
  for `src1_sorted=false`; that is the accepted artifact below.
- One canonical gate invocation failed because it was nested under an outer
  DGX lock while `paged-inference-gates.sh` owns the lock itself. The gate was
  rerun cleanly outside the outer lock.

Gates:

| gate | result |
|------|--------|
| build `test-backend-ops llama-completion` | passed |
| selected `MOE_SWIGLU_DOWN,MUL_MAT_ID_RAGGED_MOE` | `13/13` |
| MoE md5 | `8cb0ce23777bf55f92f63d0292c756b0` |
| dense md5 | `5951a5b4d624ce891e22ab5fca9bc439` |
| `MUL_MAT` | `1146/1146` |
| `MUL_MAT_ID` | `806/806` |

Focused perf:

| row | runs | us/run | TFLOPS |
|-----|-----:|-------:|-------:|
| `MOE_SWIGLU_DOWN n=128` | `1243` | `805.99` | `11.99` |
| `MUL_MAT_ID_RAGGED_MOE n=128` | `832` | `1243.85` | `2.59` |
| `MOE_SWIGLU_DOWN n=257` | `984` | `1018.74` | `19.05` |
| `MUL_MAT_ID_RAGGED_MOE n=257` | `704` | `1452.84` | `4.45` |

Decision:

- Keep the scaffold as Phase127 dependency. This phase is perf-neutral versus
  the Phase125 baseline/control band and preserves canonical md5/op gates.
- Do not claim parity progress from Phase126 alone. The useful next step is to
  use this helper inside the whole-pattern executor so `gate_up` output,
  SWIGLU, and `down` input stay in expert-major order, with one unpermute after
  the full FFN.

### Phase125: Expert-Major Sorted Output Scope

- Date: 2026-07-02.
- Plan:
  `docs/superpowers/plans/2026-07-02-moe-expert-major-sorted-output-phase125.md`.
- Result type: source implementation spec and scoped next attempt; no source
  change yet.
- Subagent findings:
  - llama.cpp audit: the full expert-major executor is credible but too large
    for a first patch. The first slice should add a sorted-output grouped MMQ
    mode so `expert_bounds` can be used without scattering through `ids_dst`.
  - vLLM audit: portable ideas are expert-major layout across both GEMMs,
    one permute/unpermute boundary, expert offsets for activation quant/scales,
    and whole-layer measurement. CUTLASS/FlashInfer pointer-array, TMA, and
    FP4 scale-swizzle contracts should not be copied into GGML/MMQ.
  - local GDN challenge: Phase124's `gdn_core` bucket is material, but prior
    small GDN attempts already rejected the obvious decode/core knobs. A new
    GDN win would need a larger recurrence redesign, not a Phase125 shortcut.
- Decision:
  - Phase125 source was tested and rejected. Do not carry
    `LLAMA_MOE_EXPERT_MAJOR_SORTED_OUT`, the `mmq_args` identity-destination
    flag, the MMQ sorted-output temporary, or the immediate unsort proof path.
  - The full expert-major `gate_up -> SWIGLU -> down` executor remains the
    right conceptual MoE target, but the first slice proved that sorted-output
    plus immediate unsort is too expensive to be a stepping stone by itself.
    Any follow-up must avoid adding an extra unsort boundary and must consume
    sorted activations directly in the down GEMM.
- Red/baseline attempt:
  - Red artifact:
    `/home/mudler/bench/phase125_moe_expert_major_sorted_output/red_valid_20260702_032918`.
  - Baseline artifact:
    `/home/mudler/bench/phase125_moe_expert_major_sorted_output/baseline_valid_20260702_032923`.
  - Red env:
    `LLAMA_MOE_EXPERT_MAJOR_SORTED_OUT=1 LLAMA_MOE_EXPERT_MAJOR_SORTED_TRACE=32`.
  - Red result: `test-backend-ops perf -o MOE_SWIGLU_DOWN` exited `0` and
    emitted `0` `LLAMA_MOE_EXPERT_MAJOR_SORTED` markers, as expected before
    implementation.
  - Baseline selected gate:
    `test-backend-ops test -o MOE_SWIGLU_DOWN,MUL_MAT_ID_RAGGED_MOE` passed
    `13/13`.

Baseline perf rows:

| row | runs | us/run | GFLOP/run | TFLOPS |
|-----|-----:|-------:|----------:|-------:|
| `MOE_SWIGLU_DOWN n=128` | `1243` | `809.70` | `9.66` | `11.93` |
| `MUL_MAT_ID_RAGGED_MOE n=128` | `832` | `1244.18` | `3.22` | `2.59` |
| `MOE_SWIGLU_DOWN n=257` | `984` | `1016.44` | `19.40` | `19.09` |
| `MUL_MAT_ID_RAGGED_MOE n=257` | `688` | `1453.65` | `6.47` | `4.45` |

Source attempt:

- Artifact:
  `/home/mudler/bench/phase125_moe_expert_major_sorted_output/20260702_033931`.
- Candidate env:
  `LLAMA_MOE_EXPERT_MAJOR_SORTED_OUT=1 LLAMA_MOE_EXPERT_MAJOR_SORTED_TRACE=32`.
- Candidate implementation:
  - added an internal `mmq_args` identity-destination flag;
  - wrote NVFP4 grouped MMQ output to a sorted temporary when the env was set;
  - inverted `ids_dst` on GPU and immediately used `get_rows_cuda` to restore
    the normal destination layout;
  - emitted bounded `LLAMA_MOE_EXPERT_MAJOR_SORTED` trace markers.
- Correctness:
  - default selected `MOE_SWIGLU_DOWN,MUL_MAT_ID_RAGGED_MOE`: `13/13`;
  - opt-in sorted `MOE_SWIGLU_DOWN`: `7/7`;
  - opt-in correctness markers: `12` (`gate_up` and `down` for six NVFP4
    rows).

Perf:

| arm | `MOE_SWIGLU_DOWN n=128` | `MUL_MAT_ID_RAGGED_MOE n=128` | `MOE_SWIGLU_DOWN n=257` | `MUL_MAT_ID_RAGGED_MOE n=257` |
|-----|-------------------------:|--------------------------------:|-------------------------:|--------------------------------:|
| control | `806.13 us` | `1250.99 us` | `1027.15 us` | `1457.69 us` |
| Phase121 exec | `805.16 us` | `1247.92 us` | `1023.83 us` | `1457.67 us` |
| sorted-output proof | `888.76 us` | `1283.17 us` | `1192.05 us` | `1528.27 us` |

Rejection:

- Reject and revert. The proof passed correctness, but it badly missed the keep
  rule: versus Phase121 exec, `MOE_SWIGLU_DOWN n=128` regressed by about
  `10.4%` and `n=257` regressed by about `16.4%`. The ragged standalone row
  also regressed.
- Post-reject artifact:
  `/home/mudler/bench/phase125_moe_expert_major_sorted_output/post_reject_20260702_034232`.
- Post-reject gates:
  - build: `0`;
  - selected `MOE_SWIGLU_DOWN,MUL_MAT_ID_RAGGED_MOE`: `13/13`;
  - retained Phase121 exec `MOE_SWIGLU_DOWN`: `7/7`, six exec markers;
  - MoE md5: `8cb0ce23777bf55f92f63d0292c756b0`;
  - dense md5: `5951a5b4d624ce891e22ab5fca9bc439`;
  - `MUL_MAT`: `1146/1146`;
  - `MUL_MAT_ID`: `806/806`.

### Phase124: Current MoE Serving Graph-Node Refresh

- Date: 2026-07-02.
- Artifact:
  `/home/mudler/bench/phase124_current_moe_profile/20260702_031205`.
- Result type: current-stack llama.cpp graph-node serving profile; no source
  change.
- Shape: MoE `q36-35b-a3b-nvfp4`, `N=128`, `PTOK=128`, `GEN=64`,
  `PARALLEL=128`, `CTX=131072`, `BATCH=2048`, `UBATCH=512`.
- Profiler: `nsys launch --cuda-graph-trace=node`, bucketed with
  `/home/mudler/bench/bucket2.py`.

Gates:

| phase | MoE md5 | dense md5 | `MUL_MAT` | `MUL_MAT_ID` |
|-------|---------|-----------|-----------|--------------|
| pre | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `1146/1146` | `806/806` |
| post | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `1146/1146` | `806/806` |

Serving result under graph-node profiling:

| n | agg_tps | decode_agg_tps | decode_perseq_tps | prefill_tps | ttft_mean_ms | wall_s |
|--:|--------:|---------------:|------------------:|------------:|-------------:|-------:|
| `128` | `206.2` | `320.3` | `2.11` | `1536.4` | `8826.7` | `39.738` |

Macro buckets:

| bucket | time ms | share | instances |
|--------|--------:|------:|----------:|
| GDN | `6665.04` | `33.10%` | `20790` |
| MoE/FFN-GEMM | `6246.97` | `31.03%` | `52484` |
| bf16/fp8-proj | `2687.28` | `13.35%` | `51960` |
| layout-copy | `1259.59` | `6.26%` | `79100` |
| ew-mul(weight/norm/GDN) | `728.03` | `3.62%` | `50422` |
| act-quant | `674.88` | `3.35%` | `36084` |
| FA | `264.14` | `1.31%` | `3530` |

Fine buckets:

| bucket | macro | time ms | share | instances |
|--------|-------|--------:|------:|----------:|
| `mmq_nvfp4` | MoE/FFN-GEMM | `6074.78` | `30.17%` | `33204` |
| `gdn_core` | GDN | `5888.31` | `29.25%` | `4500` |
| `cublas_bf16_gemm` | bf16/fp8-proj | `1722.37` | `8.55%` | `21970` |
| `cutlass_bf16_gemm` | bf16/fp8-proj | `766.57` | `3.81%` | `26380` |
| `ew_mul` | ew-mul(weight/norm/GDN) | `723.07` | `3.59%` | `46494` |
| `act_quant` | act-quant | `674.88` | `3.35%` | `36084` |
| `convert_dtype` | layout-copy | `660.48` | `3.28%` | `51300` |
| `gdn_conv` | GDN | `457.10` | `2.27%` | `6960` |
| `concat_layout` | layout-copy | `440.02` | `2.19%` | `2040` |

Decision:

- Phase124 confirms the current serving gap is still a two-bucket problem:
  `mmq_nvfp4` and `gdn_core` together account for about `59.4%` of kernel
  time.
- The `act_quant` bucket is only `3.35%`, explaining why Phase116/123
  fused-activation shortcuts did not move end-to-end rows.
- Do not fund more route-only, activation-only, or tile-policy MoE shortcuts.
  Next source work must either own the full expert-major MoE pipeline to reduce
  `mmq_nvfp4`, or attack `gdn_core` with a default-off GDN decode experiment
  measured against this Phase124/Phase77 bucket.

### Phase123: MoE Executor Fused Down Input

- Date: 2026-07-02.
- Plan:
  `docs/superpowers/plans/2026-07-02-moe-executor-fused-down-input-phase123.md`.
- Artifact:
  `/home/mudler/bench/phase123_moe_executor_fused_down_input/20260702_025811`.
- Red check artifact:
  `/home/mudler/bench/phase123_moe_executor_fused_down_input/red_20260702_025031`.
- Candidate env:
  `LLAMA_MOE_WHOLE_PATTERN_EXEC=1 LLAMA_MOE_WHOLE_PATTERN_FUSED_DOWN=1`.
- Source decision: reject and revert. Do not carry the
  `LLAMA_MOE_WHOLE_PATTERN_FUSED_DOWN` env, NVFP4 fused SwiGLU quant kernel,
  or `ggml_cuda_mul_mat_q_moe_swiglu_down()` helper.

Gates:

| gate | result | trace markers |
|------|--------|---------------|
| red check fused-down trace before implementation | `7/7` test rows | `0` fused-down markers |
| default selected `MOE_SWIGLU_DOWN,MUL_MAT_ID_RAGGED_MOE` | `13/13` | n/a |
| fused-down `MOE_SWIGLU_DOWN` | `7/7` | `6` fused-down markers |
| post-reject selected `MOE_SWIGLU_DOWN,MUL_MAT_ID_RAGGED_MOE` | `13/13` | n/a |
| post-reject Phase121 exec `MOE_SWIGLU_DOWN` | `7/7` | `6` exec markers |

Perf:

| arm | `MOE_SWIGLU_DOWN n=128` | `MUL_MAT_ID_RAGGED_MOE n=128` | `MOE_SWIGLU_DOWN n=257` | `MUL_MAT_ID_RAGGED_MOE n=257` |
|-----|-------------------------:|--------------------------------:|-------------------------:|--------------------------------:|
| control | `812.340097 us` | `1242.909856 us` | `1021.592480 us` | `1461.043605 us` |
| Phase121 exec | `811.152856 us` | `1248.876202 us` | `1023.089980 us` | `1455.405523 us` |
| fused-down | `810.617860 us` | `1250.528750 us` | `1023.657464 us` | `1459.239826 us` |

Decision:

- Reject the standalone fused-down activation quantization path. It passed
  correctness, but the target row was flat-to-negative and far below the `2%`
  keep rule.
- Keep Phase121 executor proof only. The next MoE attempt should not be another
  one-boundary activation materialization shortcut; it needs a full
  expert-major packed pipeline or a different measured bottleneck.

### Phase122: MoE Shared Route Metadata

- Date: 2026-07-02.
- Plan:
  `docs/superpowers/plans/2026-07-02-moe-shared-route-meta-phase122.md`.
- Artifact:
  `/home/mudler/bench/phase122_moe_shared_route_meta/20260702_043212`.
- Candidate env:
  `LLAMA_MOE_WHOLE_PATTERN_EXEC=1 LLAMA_MOE_WHOLE_PATTERN_SHARED_ROUTE=1`.
- Source decision: reject and revert. Do not carry the public
  `ggml_cuda_mmq_ids_meta` API, shared-route executor helper, or
  `LLAMA_MOE_WHOLE_PATTERN_SHARED_ROUTE` env.

Gates:

| gate | result | trace markers |
|------|--------|---------------|
| default selected `MOE_SWIGLU_DOWN,MUL_MAT_ID_RAGGED_MOE` | `13/13` | n/a |
| shared-route `MOE_SWIGLU_DOWN` | `7/7` | `6` shared-route markers |
| post-reject selected `MOE_SWIGLU_DOWN,MUL_MAT_ID_RAGGED_MOE` | `13/13` | n/a |
| post-reject Phase121 exec `MOE_SWIGLU_DOWN` | `7/7` | `6` exec markers |

Perf:

| arm | `MOE_SWIGLU_DOWN n=128` | `MUL_MAT_ID_RAGGED_MOE n=128` | `MOE_SWIGLU_DOWN n=257` | `MUL_MAT_ID_RAGGED_MOE n=257` |
|-----|-------------------------:|--------------------------------:|-------------------------:|--------------------------------:|
| control | `808.519710 us` | `1245.913462 us` | `1022.664622 us` | `1457.690407 us` |
| Phase121 exec | `808.189863 us` | `1250.302500 us` | `1020.849593 us` | `1461.318314 us` |
| shared-route | `811.836039 us` | `1246.143029 us` | `1051.665618 us` | `1449.548295 us` |

Decision:

- Reject the shared-route metadata API/path: it did not meet the keep rule and
  regressed the target `MOE_SWIGLU_DOWN n=257` row by about `3%` versus the
  Phase121 executor.
- Keep Phase121 executor proof only. Route-only reuse is closed as a parity
  lever; the next executor scope must remove a larger activation/down boundary.

### Phase121: MoE Whole-Pattern Exec Proof

- Date: 2026-07-02.
- Plan:
  `docs/superpowers/plans/2026-07-02-moe-whole-pattern-exec-proof-phase121.md`.
- Initial artifact:
  `/home/mudler/bench/phase121_moe_whole_pattern_exec_proof/20260702_041543`.
- Fix1 artifact:
  `/home/mudler/bench/phase121_moe_whole_pattern_exec_proof/20260702_041739_fix1`.
- Source decision: keep fix1 default-off executor proof; it proves ownership
  and skip accounting but does not yet fuse work.

Gates:

| run | result |
|-----|--------|
| fix1 selected default, `MOE_SWIGLU_DOWN,MUL_MAT_ID_RAGGED_MOE` | `13/13` |
| fix1 exec proof, `LLAMA_MOE_WHOLE_PATTERN_EXEC=1 MOE_SWIGLU_DOWN` | `7/7` |
| fix1 MoE md5 | `8cb0ce23777bf55f92f63d0292c756b0` |
| fix1 dense md5 | `5951a5b4d624ce891e22ab5fca9bc439` |
| fix1 `MUL_MAT` gate | `1146/1146` |
| fix1 `MUL_MAT_ID` gate | `806/806` |

Perf:

| row | control us | exec us | change |
|-----|-----------:|--------:|-------:|
| `MOE_SWIGLU_DOWN n_tokens=128` | `807.772325` | `806.051488` | `+0.21%` |
| `MOE_SWIGLU_DOWN n_tokens=257` | `1021.114837` | `1020.839431` | `+0.03%` |
| `MUL_MAT_ID_RAGGED_MOE n=128` | `1243.250000` | `1243.313702` | `-0.01%` |
| `MUL_MAT_ID_RAGGED_MOE n=257` | `1450.889205` | `1456.279070` | `-0.37%` |

Trace:

- Initial run passed correctness but emitted `0` exec markers because the exec
  branch was accidentally nested under the early trace env condition.
- Fix1 exec gate emitted `6` `skip=4` markers for the supported correctness
  rows.
- Fix1 exec perf emitted `6` `skip=4` markers covering `n_tokens=128` and
  `n_tokens=257`.

Decision:

- Keep the default-off executor proof.
- It changes no default behavior and proves that the early matcher can own
  `gate_up`, skip both views, execute `GLU` and `down`, and return `4`.
- Next phase should turn the proof helper into a useful executor by replacing
  one internal boundary at a time. The most defensible next slice is route-plan
  reuse inside the helper or activation in route-slot order, not another graph
  detector.

### Phase120: MoE Early Whole-Pattern Matcher

- Date: 2026-07-02.
- Plan:
  `docs/superpowers/plans/2026-07-02-moe-early-whole-pattern-phase120.md`.
- Initial artifact:
  `/home/mudler/bench/phase120_moe_early_whole_pattern/20260702_040153`.
- Fix1 artifact:
  `/home/mudler/bench/phase120_moe_early_whole_pattern/20260702_040515_fix1`.
- Fix2 artifact:
  `/home/mudler/bench/phase120_moe_early_whole_pattern/20260702_040725_fix2`.
- Source decision: keep fix2 default-off early matcher/trace; no execution is
  skipped yet.

Gates:

| run | result |
|-----|--------|
| fix2 selected default, `MOE_SWIGLU_DOWN,MUL_MAT_ID_RAGGED_MOE` | `13/13` |
| fix2 early trace, `LLAMA_MOE_WHOLE_PATTERN_EARLY_TRACE=16 MOE_SWIGLU_DOWN` | `7/7` |
| fix2 MoE md5 | `8cb0ce23777bf55f92f63d0292c756b0` |
| fix2 dense md5 | `5951a5b4d624ce891e22ab5fca9bc439` |
| fix2 `MUL_MAT` gate | `1146/1146` |
| fix2 `MUL_MAT_ID` gate | `806/806` |

Perf:

| row | control us | early trace us | change |
|-----|-----------:|---------------:|-------:|
| `MOE_SWIGLU_DOWN n_tokens=128` | `803.937002` | `808.978278` | `-0.62%` |
| `MOE_SWIGLU_DOWN n_tokens=257` | `1020.411585` | `1026.072597` | `-0.55%` |
| `MUL_MAT_ID_RAGGED_MOE n=128` | `1246.259615` | `1243.800481` | `+0.20%` |
| `MUL_MAT_ID_RAGGED_MOE n=257` | `1456.428779` | `1456.109012` | `+0.02%` |

Trace:

- Initial artifact emitted `96` early markers with only `6` supported rows;
  fix1 emitted `104` markers with only `6` supported rows.
- Fix2 emits exactly `6` early markers, all supported, covering
  `n_tokens=128` and `n_tokens=257`.
- The fix2 marker proves the executor entry contract before GEMM1 dispatch:
  `skip_ready=4`, `ids_match=1`, `swiglu=1`, `n_used=8`, `experts=128`,
  `n_embd=2048`, `n_ff=768`.

Decision:

- Keep the default-off early matcher/trace.
- This does not improve runtime by itself; it establishes the correct hook for
  the next executor attempt.
- Next phase should add a guarded executor at this matcher. First prove that it
  can own the five-node sequence and return `4` only after reproducing the
  existing outputs, then move useful work into the helper: route-plan reuse
  across both expert GEMMs, activation in route-slot order, and later direct
  weighted combine.

### Phase119: MoE Whole-Pattern Contract

- Date: 2026-07-02.
- Plan:
  `docs/superpowers/plans/2026-07-02-moe-whole-pattern-contract-phase119.md`.
- Initial artifact:
  `/home/mudler/bench/phase119_moe_whole_pattern_contract/20260702_034729`.
- Fix1 artifact:
  `/home/mudler/bench/phase119_moe_whole_pattern_contract/20260702_035126_fix1`.
- Source decision: keep default-off contract trace after fix1; no runtime
  executor yet.

Gates:

| run | result |
|-----|--------|
| fix1 selected default, `MOE_SWIGLU_DOWN,MUL_MAT_ID_RAGGED_MOE` | `13/13` |
| fix1 trace gate, `LLAMA_MOE_WHOLE_PATTERN_TRACE=16 MOE_SWIGLU_DOWN` | `7/7` |
| fix1 MoE md5 | `8cb0ce23777bf55f92f63d0292c756b0` |
| fix1 dense md5 | `5951a5b4d624ce891e22ab5fca9bc439` |
| fix1 `MUL_MAT` gate | `1146/1146` |
| fix1 `MUL_MAT_ID` gate | `806/806` |

Initial perf:

| row | control us | trace us | change |
|-----|-----------:|---------:|-------:|
| `MOE_SWIGLU_DOWN n_tokens=128` | `809.251810` | `811.777597` | `-0.31%` |
| `MOE_SWIGLU_DOWN n_tokens=257` | `1015.069697` | `1028.937243` | `-1.35%` |
| `MUL_MAT_ID_RAGGED_MOE n=128` | `1247.114183` | `1247.876202` | `-0.06%` |
| `MUL_MAT_ID_RAGGED_MOE n=257` | `1450.355114` | `1456.109012` | `-0.40%` |

Fix1 perf:

| row | control us | trace us | change |
|-----|-----------:|---------:|-------:|
| `MOE_SWIGLU_DOWN n_tokens=128` | `805.399839` | `805.584071` | `-0.02%` |
| `MOE_SWIGLU_DOWN n_tokens=257` | `1019.715447` | `1021.836382` | `-0.21%` |
| `MUL_MAT_ID_RAGGED_MOE n=128` | `1247.504808` | `1247.542067` | `-0.00%` |
| `MUL_MAT_ID_RAGGED_MOE n=257` | `1458.351744` | `1454.090116` | `+0.29%` |

Trace:

- Initial and fix1 trace perf emitted `6` whole-pattern markers.
- Fix1 covered supported NVFP4 contract rows at `n_tokens=128` and
  `n_tokens=257`: `view_pair=1`, `ids_match=1`, `swiglu=1`,
  `n_used=8`, `experts=128`, `n_embd=2048`, `n_ff=768`.
- The trace gate also covered smaller correctness shapes; the F32 row reports
  `supported=0` by design because the executor target is native FP4.

Decision:

- Keep the default-off trace/contract scaffold.
- This phase does not promote a runtime optimization.
- The next executor attempt should be matched from the earlier
  `gate_up MUL_MAT_ID` node, not from the current `GLU -> down` validation
  hook, so it can own route-plan reuse, GEMM1, activation, GEMM2, and later
  weighted combine.

### Phase118: MoE Route Cache

- Date: 2026-07-02.
- Plan:
  `docs/superpowers/plans/2026-07-02-moe-route-cache-phase118.md`.
- Artifact:
  `/home/mudler/bench/phase118_moe_route_cache/20260702_030549`.
- Source decision: reject and revert runtime cache; keep helper refactor only.

Preflight note:

- The initial `pgrep -af "[l]ocal-ai-worker"` preflight was a false positive
  because the remote shell contained the literal text `local-ai-worker busy`.
  Corrected follow-up used `pgrep -x local-ai-worker`; Docker, worker, and GPU
  compute-app checks were clean.

Gates:

| run | result |
|-----|--------|
| helper refactor selected gate | `13/13` |
| cache default selected gate | `13/13` |
| cache opt-in selected gate, `LLAMA_MOE_ROUTE_CACHE=1` | `13/13` |
| post-reject selected gate | `13/13` |

Perf:

| row | baseline us | cache us | change |
|-----|------------:|---------:|-------:|
| `MOE_SWIGLU_DOWN n_tokens=128` | `799.360447` | `803.738437` | `-0.55%` |
| `MOE_SWIGLU_DOWN n_tokens=257` | `1017.711382` | `1011.915152` | `+0.57%` |
| `MUL_MAT_ID_RAGGED_MOE n=128` | `1239.332933` | `1239.560096` | `-0.02%` |
| `MUL_MAT_ID_RAGGED_MOE n=257` | `1447.588068` | `1441.795455` | `+0.40%` |

Trace:

- `LLAMA_MOE_ROUTE_CACHE=1 LLAMA_MOE_ROUTE_CACHE_TRACE=128` on
  `MOE_SWIGLU_DOWN n_tokens=128`: `23` hits, `3` misses.

Decision:

- Reject and revert the runtime route cache. It proves reuse is possible, but
  the win is too small for the additional context-owned state and graph-capture
  lifetime surface.
- Keep only the local `ggml_cuda_mmq_ids_meta` helper refactor as low-conflict
  groundwork for a future whole-pattern executor.

### Phase117: MoE Route-Once Boundary Timing

- Date: 2026-07-02.
- Plan:
  `docs/superpowers/plans/2026-07-02-moe-route-once-boundary-phase117.md`.
- Artifact:
  `/home/mudler/bench/phase117_moe_route_once_boundary/20260702_024140`.
- Trace env:
  `LLAMA_MOE_BOUNDARY_TRACE=1`; optional timings with
  `LLAMA_MOE_BOUNDARY_TIMING=1`.
- Source decision: keep default-off diagnostic trace only; no runtime
  optimization promoted.

Gates:

| run | result |
|-----|--------|
| post-guard selected default, `MOE_SWIGLU_DOWN,MUL_MAT_ID_RAGGED_MOE` | `13/13` |
| post-guard trace/timing, `MOE_SWIGLU_DOWN` | `7/7`, `50` trace lines |
| canonical MoE md5 | `8cb0ce23777bf55f92f63d0292c756b0` |
| canonical dense md5 | `5951a5b4d624ce891e22ab5fca9bc439` |
| canonical `MUL_MAT` | `1146/1146` |
| canonical `MUL_MAT_ID` | `806/806` |

Perf / timing:

| row | perf us | boundary medians |
|-----|--------:|------------------|
| graph-enabled `MOE_SWIGLU_DOWN n=128`, trace+timing guarded | `806.271923` | capture emits `us=-1` after graph warmup |
| no-graph `MOE_SWIGLU_DOWN n=128` | `821.530713` | gate_up: sort `8.992`, quant `103.840`, mmq `1218.656`; down: sort `8.800`, quant `50.720`, mmq `632.768`; GLU `26.240` |
| no-graph `MOE_SWIGLU_DOWN n=257` | `1079.544086` | gate_up: sort `13.376`, quant `185.632`, mmq `1297.728`; down: sort `13.952`, quant `83.808`, mmq `672.096`; GLU `51.232` |
| no-graph `MUL_MAT_ID_RAGGED_MOE n=128` | `1255.156250` | sort `8.896`, quant `99.232`, mmq `1133.472` |
| no-graph `MUL_MAT_ID_RAGGED_MOE n=257` | `1531.667683` | sort `14.624`, quant `174.464`, mmq `1263.360` |

Notes:

- Inline CUDA events cannot be synchronized inside CUDA graph capture. The
  guard is required: graph-enabled timing no longer aborts, but captured
  sections report `us=-1`; use `GGML_CUDA_DISABLE_GRAPHS=1` only for boundary
  attribution.
- The route-sort bucket is small, and standalone GLU/down-quant is not enough
  after the Phase116 flat result. Do not fund another small sort/tile/quant
  shortcut from this evidence.
- Next source work should be a larger MoE pipeline: route-once metadata shared
  by both expert GEMMs and/or whole-pattern GEMM1->activation->GEMM2 ownership.

### Phase116: MoE SwiGLU Down Fused Quant

- Date: 2026-07-02.
- Plan:
  `docs/superpowers/plans/2026-07-02-moe-swiglu-down-fused-quant-phase116.md`.
- Artifact:
  `/home/mudler/bench/phase116_moe_swiglu_down_fused_quant/20260702_022611`.
- Env under test:
  `LLAMA_MOE_SWIGLU_DOWN_FUSED_QUANT=1`.
- Source decision: rejected and reverted.

Selected gates:

| run | selected gate | route marker |
|-----|---------------|--------------|
| control | `13/13` | n/a |
| initial candidate | `13/13` | absent |
| fix1 candidate | `13/13` | present, `6` hits |
| post-revert | `13/13` | n/a |

Perf:

| op | shape | control us | fused us | candidate change |
|----|-------|-----------:|---------:|-----------------:|
| `MOE_SWIGLU_DOWN` | `n_tokens=128` | `806.332261` | `808.791633` | `-0.30%` |
| `MUL_MAT_ID_RAGGED_MOE` | `n=128` | `1241.147837` | `1245.063702` | `-0.32%` |
| `MOE_SWIGLU_DOWN` | `n_tokens=257` | `1024.895706` | `1024.685072` | `+0.02%` |
| `MUL_MAT_ID_RAGGED_MOE` | `n=257` | `1454.116279` | `1455.965116` | `-0.13%` |

Decision:

- Reject and revert Phase116.
- The route is technically feasible without a new ggml op or MMQ kernel change,
  but fusing only `SWIGLU` into MMQ activation quantization is too small to move
  GB10 parity.
- Do not retry this exact standalone fused-quant path. The next credible fused
  routed-MoE phase needs route-once metadata shared by both expert GEMMs plus a
  larger fused GEMM1/activation/GEMM2 or weighted-combine/scatter boundary.

### Phase115: MoE Small-M Sentinel A/B

- Date: 2026-07-02.
- Plan:
  `docs/superpowers/plans/2026-07-01-moe-small-m-sentinel-phase115.md`.
- Artifact:
  `/home/mudler/bench/phase115_moe_small_m_sentinel/20260702_020258`.
- Env under test:
  `LLAMA_MOE_SMALL_M_TILE=16`, `LLAMA_MOE_SMALL_M_TILE=32`,
  `LLAMA_MOE_SMALL_M_TILE=64`.
- Source decision: no source change; reject as a parity lever.

Selected gates:

| env | selected gate |
|-----|---------------|
| control | `13/13` |
| `LLAMA_MOE_SMALL_M_TILE=16` | `13/13` |
| `LLAMA_MOE_SMALL_M_TILE=32` | `13/13` |
| `LLAMA_MOE_SMALL_M_TILE=64` | `13/13` |

Perf:

| env | `MOE_SWIGLU_DOWN` 128 us | `MUL_MAT_ID_RAGGED_MOE` 128 us | `MOE_SWIGLU_DOWN` 257 us | `MUL_MAT_ID_RAGGED_MOE` 257 us |
|-----|-------------------------:|-------------------------------:|-------------------------:|-------------------------------:|
| control | `809.814159` | `1247.719952` | `1021.508130` | `1452.301136` |
| `LLAMA_MOE_SMALL_M_TILE=16` | `804.780370` | `1241.008413` | `1020.710366` | `1455.017442` |
| `LLAMA_MOE_SMALL_M_TILE=32` | `809.751408` | `1242.140625` | `1021.155488` | `1458.712209` |
| `LLAMA_MOE_SMALL_M_TILE=64` | `807.938858` | `1247.765625` | `1021.431911` | `1456.875000` |

Decision:

- Reject small-M row shaping for the current stack.
- This confirms the older Phase33 serving-level rejection on the newer
  whole-graph sentinels: smaller MoE token tiles are correctness-safe, but the
  257-token ragged down path does not improve.
- Do not add a down-name special case or another tile-policy shortcut. Phase116
  should scope a fused routed-MoE kernel or graph-level fusion that avoids
  materializing intermediate activation/output traffic.

### Phase114: W4A16 Padded Routing

- Date: 2026-07-01.
- Plan:
  `docs/superpowers/plans/2026-07-01-w4a16-padded-routing-phase114.md`.
- Initial artifact:
  `/home/mudler/bench/phase114_w4a16_padded_routing/20260701_234634_padded_meta`.
- Fix1 artifact:
  `/home/mudler/bench/phase114_w4a16_padded_routing/20260701_235003_padded_meta_fix1`.
- Env under test:
  `LLAMA_W4A16_PREFILL_M=128 LLAMA_W4A16_DIRECT_A=1 LLAMA_MOE_GPU_SORT=1 LLAMA_W4A16_PADDED_META=1`.
- Source decision: rejected and reverted.

Selected gates:

| run | control | candidate |
|-----|---------|-----------|
| initial padded metadata | `13/13` | `13/13` |
| fix1 with `num_tokens_post_pad` early returns | `13/13` | `13/13` |
| post-revert Phase112 control | `13/13` | n/a |

Fix1 perf:

| op | shape | Phase112 control us | Phase114 fix1 us | candidate change |
|----|-------|--------------------:|-----------------:|-----------------:|
| `MOE_SWIGLU_DOWN` | `n_tokens=128` | `805.094932` | `804.176236` | `+0.11%` |
| `MUL_MAT_ID_RAGGED_MOE` | `n=128` | `1243.722356` | `1245.055288` | `-0.11%` |
| `MOE_SWIGLU_DOWN` | `n_tokens=257` | `1477.876106` | `1726.273196` | `-16.81%` |
| `MUL_MAT_ID_RAGGED_MOE` | `n=257` | `2163.346983` | `2650.932292` | `-22.54%` |

Decision:

- Reject and revert Phase114.
- The vLLM-style padded metadata contract is correctness-feasible in llama.cpp,
  but a naive padded consumer does too much padded gather/GEMM/scatter work for
  sparse expert occupancy on these GB10 test rows.
- Do not retry this exact padded-W4A16 route unless the kernel is changed to
  avoid padded activation/output traffic, or the work shifts to a true fused
  routed-MoE kernel where padding is part of the native tile scheduler.

### Phase113: W4A16 Direct-A GPU Tiles

- Date: 2026-07-01.
- Plan:
  `docs/superpowers/plans/2026-07-01-w4a16-direct-a-gpu-tiles-phase113.md`.
- Artifact:
  `/home/mudler/bench/phase113_w4a16_direct_a_gpu_tiles/20260701_233345_no_readback`.
- Env under test:
  `LLAMA_W4A16_PREFILL_M=128 LLAMA_W4A16_DIRECT_A=1 LLAMA_MOE_GPU_SORT=1 LLAMA_W4A16_GPU_TILES=1`.
- Source decision: rejected and reverted.

Selected gates:

| env | selected gate |
|-----|---------------|
| Phase112 control, `DIRECT_A=1 MOE_GPU_SORT=1` | `13/13` |
| Phase113 candidate, plus `W4A16_GPU_TILES=1` | `13/13` |
| post-revert Phase112 control | `13/13` |

Perf:

| op | shape | Phase112 control us | Phase113 candidate us | candidate change |
|----|-------|--------------------:|----------------------:|-----------------:|
| `MOE_SWIGLU_DOWN` | `n_tokens=128` | `808.130330` | `803.574960` | `+0.56%` |
| `MUL_MAT_ID_RAGGED_MOE` | `n=128` | `1242.206731` | `1239.567308` | `+0.21%` |
| `MOE_SWIGLU_DOWN` | `n_tokens=257` | `1478.156342` | `1476.355457` | `+0.12%` |
| `MUL_MAT_ID_RAGGED_MOE` | `n=257` | `2148.437500` | `2214.230603` | `-3.06%` |

Canonical gates:

- Skipped for the candidate because the perf gate failed.
- Post-revert selected gate passed `13/13`, restoring the accepted Phase112
  state on DGX.

Decision:

- Reject and revert Phase113.
- Do not spend more time on compact GPU tile descriptors for W4A16 unless the
  GEMM itself consumes a vLLM-style padded metadata contract directly.
- The next credible MoE phase should move toward padded aligned metadata
  (`sorted_token_ids`, expert-per-block ids, and padded row count) rather than
  compact descriptors plus a ragged tile map.

### Phase112: W4A16 Direct Activation Staging

- Date: 2026-07-01.
- Plan:
  `docs/superpowers/plans/2026-07-01-w4a16-direct-a-phase112.md`.
- Artifact:
  `/home/mudler/bench/phase112_w4a16_direct_a/20260701_231749_direct_a`.
- Env under test:
  `LLAMA_W4A16_PREFILL_M=128 LLAMA_W4A16_DIRECT_A=1 LLAMA_MOE_GPU_SORT=1`.
- Source decision: keep default-off.

Selected gates:

| env | selected gate |
|-----|---------------|
| `LLAMA_W4A16_PREFILL_M=128 LLAMA_MOE_GPU_SORT=1` | `13/13` |
| `LLAMA_W4A16_PREFILL_M=128 LLAMA_W4A16_DIRECT_A=1` | `13/13` |
| `LLAMA_W4A16_PREFILL_M=128 LLAMA_W4A16_DIRECT_A=1 LLAMA_MOE_GPU_SORT=1` | `13/13` |

Perf:

| op | shape | W4A16+GPU-sort us | direct-A us | direct-A+GPU-sort us | best change vs control |
|----|-------|------------------:|------------:|---------------------:|-----------------------:|
| `MOE_SWIGLU_DOWN` | `n_tokens=128` | `807.219630` | `805.847949` | `809.409493` | `-0.27%` |
| `MUL_MAT_ID_RAGGED_MOE` | `n=128` | `1242.664663` | `1245.671875` | `1247.674279` | `-0.40%` |
| `MOE_SWIGLU_DOWN` | `n_tokens=257` | `1551.081790` | `1576.045597` | `1477.738938` | `+4.73%` |
| `MUL_MAT_ID_RAGGED_MOE` | `n=257` | `2278.504464` | `2347.164352` | `2166.224138` | `+4.93%` |

Canonical gates for direct-A+GPU-sort:

| gate | result |
|------|--------|
| README MoE md5 | `8cb0ce23777bf55f92f63d0292c756b0` |
| README dense md5 | `5951a5b4d624ce891e22ab5fca9bc439` |
| `SSM_CONV` | `45/45` |
| `SSM_CONV_SPLIT` | `6/6` |
| `GET_ROWS` | `49/49` supported rows |
| `GATED_DELTA_NET` | `48/48` |
| `MUL_MAT` | `1146/1146` supported rows |
| `MUL_MAT_ID` | `806/806` |

Note: the older handoff snippet with `-no-cnv -c 4096` produced stable but
non-canonical md5s (`18a4e85031694388bab85e5f5b03effc` and
`0764361176d94719ab94f82da12eed65`) for both the direct-A candidate and the
W4A16+GPU-sort control. Treat that as a harness mismatch, not a sanctioned
gate. The patch-series README gate without `-no-cnv` and without explicit
`-c 4096` is the canonical md5 gate used above.

Decision:

- Carry Phase112 as default-off only.
- The improvement is real for the larger Phase108 MoE rows, but it only narrows
  the fallback path. W4A16 fallback is still not the default grouped-MMQ parity
  path.
- Next target: either remove another W4A16 fallback boundary that remains after
  direct-A, or shift to a fused routed-MoE kernel that avoids fallback entirely
  while preserving the same md5/op gates.

## Current Serving Record

Phase72 broader serving snapshot, MoE `PTOK=128`, `GEN=64`, `PARALLEL=128`.

Artifact:

- `/home/mudler/bench/phase72_ttft_min32_serving/20260701_160730`

| arm | n | agg_tps | decode_agg_tps | decode_perseq_tps | prefill_tps | ttft_mean_ms | wall_s |
|-----|--:|--------:|---------------:|------------------:|------------:|-------------:|-------:|
| llama default | `8` | `170.4` | `231.3` | `28.42` | `1693.4` | `786.4` | `3.004` |
| llama min32 | `8` | `158.5` | `218.4` | `26.27` | `1547.8` | `816.2` | `3.230` |
| vLLM | `8` | `260.0` | `305.9` | `37.32` | `4659.7` | `266.4` | `1.915` |
| llama default | `32` | `257.8` | `430.2` | `12.09` | `1720.4` | `2625.2` | `7.943` |
| llama min32 | `32` | `242.7` | `411.7` | `11.58` | `1617.4` | `2881.6` | `8.439` |
| vLLM | `32` | `463.6` | `601.0` | `17.60` | `5496.2` | `773.7` | `4.357` |
| llama default | `128` | `325.8` | `714.0` | `3.92` | `1628.8` | `7822.5` | `25.148` |
| llama min32 | `128` | `316.0` | `697.9` | `3.81` | `1606.0` | `8056.9` | `25.926` |
| vLLM | `128` | `666.4` | `1029.5` | `6.81` | `5292.5` | `2511.7` | `11.933` |

Ratios:

| n | min32/default agg | min32/default decode | min32/default TTFT | default decode/vLLM | min32 decode/vLLM |
|--:|------------------:|---------------------:|-------------------:|--------------------:|----------------:|
| `8` | `0.9302` | `0.9442` | `1.0379` | `0.7561` | `0.7140` |
| `32` | `0.9414` | `0.9570` | `1.0977` | `0.7158` | `0.6850` |
| `128` | `0.9699` | `0.9775` | `1.0300` | `0.6935` | `0.6779` |

Decision:

- Reject default-on for `LLAMA_TTFT_PREFILL_FIRST=1`
  `LLAMA_TTFT_PREFILL_FIRST_MIN_WAITING=32`.
- Keep min32 as opt-in only.
- The opt-in regressed aggregate, decode, TTFT, and wall time at every tested
  concurrency and widened the vLLM decode gap.

## Attempt Log

### Phase111: W4A16 GPU Tile Descriptor Probe

- Date: 2026-07-01.
- Plan:
  `docs/superpowers/plans/2026-07-01-w4a16-gpu-tile-descriptors-phase111.md`.
- Source: `/home/mudler/llama-phase93-qwen3next-gqa-bcast`.
- Local patch status: rejected and reverted.
  - Probe added default-off `LLAMA_W4A16_GPU_TILES=1`.
  - It built W4A16 tile descriptors on GPU from Phase110 `expert_bounds_dev`
    with an atomic tile counter, then copied back one `n_tiles` integer for the
    grouped W4A16 launch dimension.
  - The final source returned to the Phase110 `LLAMA_MOE_GPU_SORT=1` state.
- Failed build/runtime artifact:
  `/home/mudler/bench/phase111_w4a16_gpu_tiles/20260701_230216`.
- Measured artifact:
  `/home/mudler/bench/phase111_w4a16_gpu_tiles/20260701_230400_fix1`.

Failure/fix notes:

| attempt | result | cause |
|---------|--------|-------|
| initial DGX compile | failed | `expert_bounds_for_w4a16` was typed `const int32_t *` but `mm_ids_helper` writes expert bounds |
| first runtime artifact `20260701_230216` | aborted | CUDA pool LIFO assert: outer `expert_bounds_dev` was allocated after inner `ids_dst_dev` but freed later |
| fix1 artifact `20260701_230400_fix1` | selected gates passed | allocation order corrected; `LLAMA_W4A16_GPU_TILES=1` branch traced |
| post-revert gate | `13/13` | source restored to Phase110 behavior |

Selected gates:

| env | selected gate result |
|-----|----------------------|
| `LLAMA_W4A16_PREFILL_M=128 LLAMA_MOE_GPU_SORT=1` | `13/13` |
| `LLAMA_W4A16_PREFILL_M=128 LLAMA_MOE_GPU_SORT=1 LLAMA_W4A16_GPU_TILES=1` | `13/13` |
| post-revert `LLAMA_W4A16_PREFILL_M=128 LLAMA_MOE_GPU_SORT=1` | `13/13` |

Clean perf A/B:

| env | case | `n_tokens` | time_us | n_runs | vs Phase110 GPU-sort |
|-----|------|-----------:|--------:|-------:|---------------------:|
| `LLAMA_W4A16_PREFILL_M=128 LLAMA_MOE_GPU_SORT=1` | `MOE_SWIGLU_DOWN` | `128` | `807.037812` | `1243` | `1.000` |
| `LLAMA_W4A16_PREFILL_M=128 LLAMA_MOE_GPU_SORT=1` | `MOE_SWIGLU_DOWN` | `257` | `1531.958716` | `654` | `1.000` |
| `LLAMA_W4A16_PREFILL_M=128 LLAMA_MOE_GPU_SORT=1 LLAMA_W4A16_GPU_TILES=1` | `MOE_SWIGLU_DOWN` | `128` | `802.969697` | `1254` | `0.995` |
| `LLAMA_W4A16_PREFILL_M=128 LLAMA_MOE_GPU_SORT=1 LLAMA_W4A16_GPU_TILES=1` | `MOE_SWIGLU_DOWN` | `257` | `1538.542813` | `654` | `1.004` |
| `LLAMA_W4A16_PREFILL_M=128 LLAMA_MOE_GPU_SORT=1` | `MUL_MAT_ID_RAGGED_MOE` | `128` | `1244.568510` | `832` | `1.000` |
| `LLAMA_W4A16_PREFILL_M=128 LLAMA_MOE_GPU_SORT=1` | `MUL_MAT_ID_RAGGED_MOE` | `257` | `2250.435268` | `448` | `1.000` |
| `LLAMA_W4A16_PREFILL_M=128 LLAMA_MOE_GPU_SORT=1 LLAMA_W4A16_GPU_TILES=1` | `MUL_MAT_ID_RAGGED_MOE` | `128` | `1243.544471` | `832` | `0.999` |
| `LLAMA_W4A16_PREFILL_M=128 LLAMA_MOE_GPU_SORT=1 LLAMA_W4A16_GPU_TILES=1` | `MUL_MAT_ID_RAGGED_MOE` | `257` | `2295.743304` | `448` | `1.020` |

Trace facts:

- `MOE_SWIGLU_DOWN n=257` built `128` W4A16 tiles for `2056` rows.
- `MUL_MAT_ID_RAGGED_MOE n=257` built `288` W4A16 tiles for `2056` rows.
- The clean perf rerun omitted `LLAMA_W4A16_GPU_TILES_TRACE=1`; the earlier
  traced perf leg is preserved in the artifact but should not be used for timing.

Decision:

- Reject and revert Phase111 source. Moving only the W4A16 tile descriptor build
  to GPU is correctness-clean after fixes, but it does not improve the parity
  row and slightly regresses the most relevant 257-token ragged row.
- Do not spend another phase on a one-piece W4A16 host-metadata cleanup. The
  next W4A16 attempt must remove a larger boundary, such as direct activation
  consumption plus GPU descriptors in one path, or avoid the host-sync fallback
  path entirely.

### Phase110: GPU MoE Routing Metadata for Fallback/W4A16

- Date: 2026-07-01.
- Plan:
  `docs/superpowers/plans/2026-07-01-gpu-moe-routing-metadata-phase110.md`.
- Source: `/home/mudler/llama-phase93-qwen3next-gqa-bcast`.
- Local patch status: new default-off CUDA source change in
  `ggml/src/ggml-cuda/ggml-cuda.cu`.
  - Add `LLAMA_MOE_GPU_SORT=1` to route fallback `ggml_cuda_mul_mat_id`
    metadata construction through existing `ggml_cuda_launch_mm_ids_helper()`.
  - Add a local inverse-permutation kernel because `mm_ids_helper` returns
    sorted-to-original `ids_dst`, while fallback `get_rows_cuda()` needs
    original-to-sorted `ids_from_sorted`.
  - Leave graph-safe grouped-MMQ untouched.
- Failed first artifact:
  `/home/mudler/bench/phase110_gpu_moe_sort/20260701_224103`.
- Accepted artifact:
  `/home/mudler/bench/phase110_gpu_moe_sort/20260701_224446_fix1`.

Initial failure and fix:

| artifact | env | selected gate result | reason |
|----------|-----|----------------------|--------|
| `20260701_224103` | default | `13/13` | baseline clean |
| `20260701_224103` | `LLAMA_W4A16_PREFILL_M=128` | `13/13` | fallback baseline clean |
| `20260701_224103` | `LLAMA_W4A16_PREFILL_M=128 LLAMA_MOE_GPU_SORT=1` | `10/13` | wrong permutation direction for fallback `get_rows` |
| `20260701_224446_fix1` | default | `13/13` | accepted fix |
| `20260701_224446_fix1` | `LLAMA_W4A16_PREFILL_M=128` | `13/13` | accepted fix |
| `20260701_224446_fix1` | `LLAMA_W4A16_PREFILL_M=128 LLAMA_MOE_GPU_SORT=1` | `13/13` | accepted fix; trace showed branch execution |

Canonical gates:

| env | MoE md5 | dense md5 | `SSM_CONV` | `SSM_CONV_SPLIT` | `GET_ROWS` | `GATED_DELTA_NET` | `MUL_MAT` | `MUL_MAT_ID` |
|-----|---------|-----------|------------|------------------|------------|-------------------|-----------|--------------|
| default | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `45/45` | `6/6` | `49/49` | `48/48` | `1146/1146` | `806/806` |
| `LLAMA_W4A16_PREFILL_M=128 LLAMA_MOE_GPU_SORT=1` | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `45/45` | `6/6` | `49/49` | `48/48` | `1146/1146` | `806/806` |

Perf A/B:

| env | case | `n_tokens` | time_us | n_runs | vs W4A16 | vs default |
|-----|------|-----------:|--------:|-------:|---------:|-----------:|
| default | `MOE_SWIGLU_DOWN` | `128` | `806.724859` | `1243` | n/a | `1.000` |
| default | `MOE_SWIGLU_DOWN` | `257` | `1022.161585` | `984` | n/a | `1.000` |
| `LLAMA_W4A16_PREFILL_M=128` | `MOE_SWIGLU_DOWN` | `128` | `809.339501` | `1243` | `1.000` | `1.003` |
| `LLAMA_W4A16_PREFILL_M=128` | `MOE_SWIGLU_DOWN` | `257` | `1656.102310` | `606` | `1.000` | `1.620` |
| `LLAMA_W4A16_PREFILL_M=128 LLAMA_MOE_GPU_SORT=1` | `MOE_SWIGLU_DOWN` | `128` | `807.311344` | `1243` | `0.997` | `1.001` |
| `LLAMA_W4A16_PREFILL_M=128 LLAMA_MOE_GPU_SORT=1` | `MOE_SWIGLU_DOWN` | `257` | `1536.868502` | `654` | `0.928` | `1.504` |
| default | `MUL_MAT_ID_RAGGED_MOE` | `128` | `1242.343750` | `832` | n/a | `1.000` |
| default | `MUL_MAT_ID_RAGGED_MOE` | `257` | `1453.979651` | `688` | n/a | `1.000` |
| `LLAMA_W4A16_PREFILL_M=128` | `MUL_MAT_ID_RAGGED_MOE` | `128` | `1248.412260` | `832` | `1.000` | `1.005` |
| `LLAMA_W4A16_PREFILL_M=128` | `MUL_MAT_ID_RAGGED_MOE` | `257` | `2428.586538` | `416` | `1.000` | `1.670` |
| `LLAMA_W4A16_PREFILL_M=128 LLAMA_MOE_GPU_SORT=1` | `MUL_MAT_ID_RAGGED_MOE` | `128` | `1247.145433` | `832` | `0.999` | `1.004` |
| `LLAMA_W4A16_PREFILL_M=128 LLAMA_MOE_GPU_SORT=1` | `MUL_MAT_ID_RAGGED_MOE` | `257` | `2237.145089` | `448` | `0.921` | `1.539` |

Decision:

- Keep Phase110 as a default-off structural base. It is md5/op clean after the
  inverse-permutation fix and confirms vLLM-style GPU route metadata can replace
  the CPU id scan for the host-sync fallback path.
- Do not promote it as a speed parity lever by itself. The W4A16 fallback
  improves by `7.2%` on `MOE_SWIGLU_DOWN n=257` and `7.9%` on
  `MUL_MAT_ID_RAGGED_MOE n=257`, but still remains about `1.5x` slower than
  the default grouped-MMQ path.
- Phase111 should only build on this if it removes another fallback bottleneck:
  either the remaining `expert_bounds` host copy / host tile descriptor build,
  or a grouped W4A16 path that can consume GPU expert bounds directly.

### Phase109: Existing MoE Prefill and Tile-Policy A/B

- Date: 2026-07-01.
- Source: `/home/mudler/llama-phase93-qwen3next-gqa-bcast`.
- Local patch status: no new source changes. This was an env-only benchmark
  attempt using the Phase108 perf CSV harness.
- Artifact:
  `/home/mudler/bench/phase109_existing_moe_prefill_ab/20260701_222559`.

Perf A/B:

| env | case | `n_tokens` | time_us | n_runs | vs default |
|-----|------|-----------:|--------:|-------:|-----------:|
| default | `MOE_SWIGLU_DOWN` | `128` | `800.802233` | `1254` | `1.000` |
| default | `MOE_SWIGLU_DOWN` | `257` | `1008.593373` | `996` | `1.000` |
| `LLAMA_W4A16_PREFILL_M=128` | `MOE_SWIGLU_DOWN` | `128` | `805.747385` | `1243` | `1.006` |
| `LLAMA_W4A16_PREFILL_M=128` | `MOE_SWIGLU_DOWN` | `257` | `1646.679739` | `612` | `1.633` |
| `LLAMA_FP4_PREFILL_M=128` | `MOE_SWIGLU_DOWN` | `128` | `806.103781` | `1243` | `1.007` |
| `LLAMA_FP4_PREFILL_M=128` | `MOE_SWIGLU_DOWN` | `257` | `4070.191057` | `246` | `4.035` |
| `LLAMA_MOE_DENSITY_MAX=9` | `MOE_SWIGLU_DOWN` | `128` | `810.080451` | `1243` | `1.012` |
| `LLAMA_MOE_DENSITY_MAX=9` | `MOE_SWIGLU_DOWN` | `257` | `1024.869121` | `978` | `1.016` |
| `LLAMA_MOE_MMQ_X=64` | `MOE_SWIGLU_DOWN` | `128` | `806.358005` | `1243` | `1.007` |
| `LLAMA_MOE_MMQ_X=64` | `MOE_SWIGLU_DOWN` | `257` | `1008.191767` | `996` | `1.000` |
| default | `MUL_MAT_ID_RAGGED_MOE` | `128` | `1241.417067` | `832` | `1.000` |
| default | `MUL_MAT_ID_RAGGED_MOE` | `257` | `1445.333807` | `704` | `1.000` |
| `LLAMA_W4A16_PREFILL_M=128` | `MUL_MAT_ID_RAGGED_MOE` | `128` | `1242.049279` | `832` | `1.001` |
| `LLAMA_W4A16_PREFILL_M=128` | `MUL_MAT_ID_RAGGED_MOE` | `257` | `2518.852500` | `400` | `1.743` |
| `LLAMA_FP4_PREFILL_M=128` | `MUL_MAT_ID_RAGGED_MOE` | `128` | `1244.775240` | `832` | `1.003` |
| `LLAMA_FP4_PREFILL_M=128` | `MUL_MAT_ID_RAGGED_MOE` | `257` | `2898.838068` | `352` | `2.006` |
| `LLAMA_MOE_DENSITY_MAX=9` | `MUL_MAT_ID_RAGGED_MOE` | `128` | `1247.564904` | `832` | `1.005` |
| `LLAMA_MOE_DENSITY_MAX=9` | `MUL_MAT_ID_RAGGED_MOE` | `257` | `1438.245739` | `704` | `0.995` |
| `LLAMA_MOE_MMQ_X=64` | `MUL_MAT_ID_RAGGED_MOE` | `128` | `1246.139423` | `832` | `1.004` |
| `LLAMA_MOE_MMQ_X=64` | `MUL_MAT_ID_RAGGED_MOE` | `257` | `1434.058239` | `704` | `0.992` |

`MOE_WEIGHTED_COMBINE` spot rows:

| env | `n_tokens=128` | `n_tokens=257` |
|-----|---------------:|---------------:|
| default | `27.695333` | `67.423746` |
| `LLAMA_W4A16_PREFILL_M=128` | `27.502254` | `95.550477` |
| `LLAMA_FP4_PREFILL_M=128` | `27.687500` | `229.421474` |

Correctness gates:

| env | selected gate result |
|-----|----------------------|
| default | `13/13` |
| `LLAMA_W4A16_PREFILL_M=128` | `13/13` |
| `LLAMA_FP4_PREFILL_M=128` | `13/13` |
| `LLAMA_MOE_DENSITY_MAX=9` | `13/13` |
| `LLAMA_MOE_MMQ_X=64` | `13/13` |

Trace notes:

- The default/density route remained CUDA-graph-safe grouped MMQ:
  `route=mmq host_sync=0`.
- For the 257-token ragged row the traced launch uses
  `ncols_dst=2056`, `ncols_max=257`, `mmq_x=96`, `stream_k_blocks == ntiles_dst`,
  and `fixup=0`.
- For 128-token rows the current default already selects `mmq_x=64`; raising
  density or forcing 64 does not open a new path.

Decision:

- Reject existing W4A16 and FP4 large-M env routes for these Phase108 MoE
  sentinel rows. They are correctness-clean but slower, especially at
  `n_tokens=257`.
- Reject `LLAMA_MOE_DENSITY_MAX=9` and `LLAMA_MOE_MMQ_X=64` as parity levers.
  The best `MUL_MAT_ID_RAGGED_MOE` improvement is only `0.5-0.8%` and
  `MOE_SWIGLU_DOWN` is flat or worse.
- Do not spend Phase110 on another MMQ tile-policy shortcut.
- Next implementation should target the structural gap identified by the vLLM
  audit: build routed-MoE sorted token/expert metadata on GPU and remove the
  host ID readback/sync path from the grouped fallback/W4A16 path, while keeping
  the graph-safe MMQ path untouched.

### Phase108: MoE Whole-Graph Perf CSV Harness

- Date: 2026-07-01.
- Source: `/home/mudler/llama-phase93-qwen3next-gqa-bcast`.
- Local patch status: measurement-only source change in
  `tests/test-backend-ops.cpp`.
  - Add existing `MOE_SWIGLU_DOWN`, `MOE_WEIGHTED_COMBINE`, and
    `MUL_MAT_ID_RAGGED_MOE` whole-graph cases to `make_test_cases_perf()` for
    `n_tokens=128` and `257`.
  - Expand `--output csv` to use `test_result::get_fields()`, which includes
    `time_us`, `flops`, `bandwidth_gb_s`, `memory_kb`, and `n_runs`.
- Artifact:
  `/home/mudler/bench/phase108_moe_perf_csv/20260701_221559`.

RED condition from Phase107:

| command | Phase107 result |
|---------|-----------------|
| `test-backend-ops perf -b CUDA0 -o MOE_SWIGLU_DOWN --output csv` | zero rows |
| `test-backend-ops perf -b CUDA0 -o MOE_WEIGHTED_COMBINE --output csv` | zero rows |
| `test-backend-ops perf -b CUDA0 -o MUL_MAT_ID_RAGGED_MOE --output csv` | zero rows |

Perf rows after patch:

| case | params | time_us | n_runs | flops |
|------|--------|--------:|-------:|------:|
| `MOE_SWIGLU_DOWN` | `type_a=nvfp4,n_mats=128,n_used=8,n_ff=768,n_tokens=128,n_embd=2048` | `801.764753` | `1254` | `12053007297164.449219` |
| `MOE_SWIGLU_DOWN` | `type_a=nvfp4,n_mats=128,n_used=8,n_ff=768,n_tokens=257,n_embd=2048` | `1019.953252` | `984` | `19023274120980.359375` |
| `MOE_WEIGHTED_COMBINE` | `type_a=nvfp4,n_mats=128,n_used=8,n_ff=768,n_tokens=128,n_embd=2048` | `27.550055` | `36320` | `117074893979840.453125` |
| `MOE_WEIGHTED_COMBINE` | `type_a=nvfp4,n_mats=128,n_used=8,n_ff=768,n_tokens=257,n_embd=2048` | `67.593041` | `14800` | `95809244446043.828125` |
| `MUL_MAT_ID_RAGGED_MOE` | `type_a=nvfp4,n_mats=256,n_used=8,m=768,n=128,k=2048` | `1239.103365` | `832` | `2599642259062.170898` |
| `MUL_MAT_ID_RAGGED_MOE` | `type_a=nvfp4,n_mats=256,n_used=8,m=768,n=257,k=2048` | `1445.950284` | `704` | `4472917803025.495117` |

Safety gates:

| gate | result |
|------|--------|
| MoE md5 | `8cb0ce23777bf55f92f63d0292c756b0` |
| dense md5 | `5951a5b4d624ce891e22ab5fca9bc439` |
| `MOE_SWIGLU_DOWN` | `7/7` |
| `MOE_WEIGHTED_COMBINE` | `7/7` |
| `MUL_MAT_ID_RAGGED_MOE` | `6/6` |
| `SSM_CONV` | `45/45` |
| `SSM_CONV_SPLIT` | `6/6` |
| `GET_ROWS` | `49/49` |
| `GATED_DELTA_NET` | `48/48` |
| `MUL_MAT` | `1146/1146` |
| `MUL_MAT_ID` | `806/806` |

Notes:

- The first md5 attempt in `gates/` used `-no-cnv` and intentionally failed
  against the canonical chat-template hashes. The corrected historical gate is
  in `gates_chat/` and passed.
- CSV output is now a usable perf ledger for these cases; the schema includes
  timing columns instead of support metadata only.

Decision:

- Phase108 closes the Phase107 measurement gap; it is not a parity-improving
  runtime patch by itself.
- The dominant focused row is `MUL_MAT_ID_RAGGED_MOE` (`1239-1446 us/run`) and
  `MOE_SWIGLU_DOWN` (`802-1020 us/run`), not `MOE_WEIGHTED_COMBINE`
  (`28-68 us/run`).
- Next fused-MoE work should target the routed matmul/SWIGLU/down chain and
  must report deltas against these Phase108 rows plus the same md5/op gates.

### Phase107: Fused-MoE Structural Guardrail

- Date: 2026-07-01.
- Source: `/home/mudler/llama-phase93-qwen3next-gqa-bcast`.
- Local patch status: no new source changes. This was a correctness and
  measurement-surface attempt for the next structural fused routed-MoE path.
- Artifact:
  `/home/mudler/bench/phase107_moe_fusion_guardrail/20260701_220227`.

Correctness guardrails:

| guard | result |
|-------|--------|
| `MOE_SWIGLU_DOWN` | `7/7` |
| `MOE_WEIGHTED_COMBINE` | `7/7` |
| `MUL_MAT_ID_RAGGED_MOE` | `6/6` |

Perf-output check:

| command | result |
|---------|--------|
| `test-backend-ops perf -b CUDA0 -o MOE_SWIGLU_DOWN --output csv` | zero rows |
| `test-backend-ops perf -b CUDA0 -o MOE_WEIGHTED_COMBINE --output csv` | zero rows |
| `test-backend-ops perf -b CUDA0 -o MUL_MAT_ID_RAGGED_MOE --output csv` | zero rows |
| `test-backend-ops perf -b CUDA0 -o MUL_MAT_ID --output csv` | `116` support rows, `63` relevant rows, but no timing columns |

Decision:

- Existing correctness guardrails are sufficient to protect the three structural
  MoE surfaces before a future source change.
- Existing `test-backend-ops perf` output is not sufficient as a performance
  guard for these custom whole-graph cases because it emits support metadata,
  not timings.
- The next source patch should be measurement-only: a narrow MoE fusion timing
  harness that emits `case,iterations,total_ms,mean_ms` for the selected
  `MOE_SWIGLU_DOWN`, `MOE_WEIGHTED_COMBINE`, and `MUL_MAT_ID_RAGGED_MOE`
  shapes.
- Do not start fused routed-MoE kernel implementation until that timing harness
  proves which sub-surface is large enough to move Phase104/106 serving.

### Phase106: Max-Concurrency Current-Stack Serving

- Date: 2026-07-01.
- Source: `/home/mudler/llama-phase93-qwen3next-gqa-bcast`.
- Local patch status: no new source changes. This was a measurement-only
  serving-contract attempt on top of the carried Phase101/102 default-off
  cleanup candidates.
- Harness: streamed `paged-current-serving-snapshot.sh` with:
  - source-log workaround for the non-git DGX mirror,
  - paged env
    `LLAMA_SSM_CONV_SPLIT=1 LLAMA_PAGED_KV_GET_ROWS_F16=1`,
  - expanded gate ops:
    `SSM_CONV,SSM_CONV_SPLIT,GET_ROWS,GATED_DELTA_NET,MUL_MAT,MUL_MAT_ID`,
  - `NPL=128 192 256`, `PTOK=128`, `GEN=64`, `PARALLEL=256`,
    `CTX=131072`, `BATCH=2048`, `UBATCH=512`, `VLLM_MAX_NUM_SEQS=256`.
- Artifacts:
  - dry-run:
    `/home/mudler/bench/phase106_max_concurrency_current_stack/20260701_214839_dryrun`,
  - full sweep:
    `/home/mudler/bench/phase106_max_concurrency_current_stack/20260701_214907`.

Safety gates:

| phase | env | MoE md5 | dense md5 | `SSM_CONV` | `SSM_CONV_SPLIT` | `GET_ROWS` | `GATED_DELTA_NET` | `MUL_MAT` | `MUL_MAT_ID` |
|-------|-----|---------|-----------|------------|------------------|------------|-------------------|-----------|--------------|
| pre | split + F16 K/V rows | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `45/45` | `6/6` | `49/49` | `48/48` | `1146/1146` | `806/806` |
| post | split + F16 K/V rows | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `45/45` | `6/6` | `49/49` | `48/48` | `1146/1146` | `806/806` |

Serving snapshot:

| arm | n | agg_tps | decode_agg_tps | decode_perseq_tps | prefill_tps | ttft_mean_ms | wall_s |
|-----|--:|--------:|---------------:|------------------:|------------:|-------------:|-------:|
| paged combined | `128` | `331.8` | `678.9` | `3.90` | `1734.1` | `7392.5` | `24.689` |
| paged combined | `192` | `318.4` | `681.8` | `2.50` | `1602.4` | `11058.0` | `38.595` |
| paged combined | `256` | `338.4` | `824.6` | `2.10` | `1542.8` | `14933.5` | `48.410` |
| vLLM | `128` | `663.4` | `1029.8` | `6.78` | `5228.9` | `2514.6` | `11.970` |
| vLLM | `192` | `709.8` | `1202.4` | `4.98` | `4881.5` | `3674.8` | `16.769` |
| vLLM | `256` | `723.8` | `1320.4` | `3.94` | `4520.9` | `4999.0` | `21.931` |

Ratios:

| n | paged decode/vLLM | paged perseq/vLLM | paged agg/vLLM | paged TTFT/vLLM |
|--:|------------------:|------------------:|---------------:|----------------:|
| `128` | `0.6593` | `0.5752` | `0.5002` | `2.9398` |
| `192` | `0.5670` | `0.5020` | `0.4486` | `3.0091` |
| `256` | `0.6245` | `0.5330` | `0.4675` | `2.9873` |

Decision:

- Reject C1 as a GB10 parity lever for the current stack.
- llama.cpp completed `N=256`, but vLLM also completed `N=256` under the same
  harness cap and remained materially faster.
- Higher concurrency did not reveal an aggregate operating point where llama.cpp
  catches vLLM: paged aggregate stayed around `318-338 t/s`, while vLLM rose to
  `724 t/s`.
- TTFT widened with higher concurrency on llama.cpp (`7392.5 -> 14933.5 ms`)
  and stayed much lower on vLLM (`2514.6 -> 4999.0 ms`).
- The next phase should not be another scheduler or MMQ micro-policy. The
  remaining plausible source work is structural: persistent batch state, fused
  routed-MoE dispatch, or a larger GDN/packed-decode design with new guardrails.

### Phase105: Current-Stack MoE MMQ Shape Refresh

- Date: 2026-07-01.
- Source: `/home/mudler/llama-phase93-qwen3next-gqa-bcast`.
- Local patch status: no new source changes. This was a measurement-only
  attempt on top of the carried Phase101/102 default-off cleanup candidates.
- Env for trace legs:
  `LLAMA_SSM_CONV_SPLIT=1 LLAMA_PAGED_KV_GET_ROWS_F16=1`.
- Artifacts:
  - gates:
    `/home/mudler/bench/phase105_mmq_current_shape/20260701_213927`,
  - serving trace retry:
    `/home/mudler/bench/phase105_mmq_current_shape/20260701_214129_serving_retry`.

Safety gates:

| gate | env | result |
|------|-----|--------|
| `MUL_MAT_ID_RAGGED_MOE` | default | `6/6` |
| `MUL_MAT_ID_RAGGED_MOE` | split + F16 K/V rows + shape traces | `6/6` |
| `MUL_MAT_ID` | split + F16 K/V rows | `806/806` |

Trace refresh:

| source | shape lines | launch lines | small-M lines | shape summary | launch summary |
|--------|------------:|-------------:|--------------:|---------------|----------------|
| ragged gate | `3` | `3` | `2` | density `2/4/9`, `mmq_x_best 40/64/96` | `fixup=0`, `stream_k_blocks == ntiles_dst` |
| one live serving request | `120` | `120` | `0` | `ncols_max=317`, density `10`, `mmq_x_best=112`, `stream_k=1` | `fixup=0`, `stream_k_blocks == ntiles_dst` (`120/120`), efficiency `100` |

Notes:

- The first live-serving trace leg used the wrong model path and exited before
  loading the model. It is preserved in the gate artifact as a harness hiccup,
  not an inference failure.
- The serving retry used `~/bench/q36-35b-a3b-nvfp4.gguf`; the request returned
  a non-empty response (`3648` bytes), and the wrapper's nonzero exit was from
  `grep` under `pipefail` when there were zero `SMALL_M` lines.

Decision:

- The current Phase104 stack did not create a new cheap grouped-MMQ lever.
- The trace reconfirms that no-fixup/no-stream-k shortcuts are closed for this
  workload, and the live sampled shape is prefill-like rather than a new
  small-M decode class.
- Do not pursue another host-side MMQ tile policy. Any next MMQ work must be a
  structural kernel or serving-contract change with a clear path to reducing
  the dominant `mmq_nvfp4` bucket.
- Given prior GDN micro-kernel rejections, the next high-value phase should be
  a larger serving contract or a new structural design, not more isolated
  micro-knobs.

### Phase104: Combined Cleanup Normal Serving Snapshot vs vLLM

- Date: 2026-07-01.
- Source: `/home/mudler/llama-phase93-qwen3next-gqa-bcast`.
- Local patch status: no new source changes beyond the carried Phase101/102
  default-off runtime candidates.
- Harness: streamed `paged-current-serving-snapshot.sh` with:
  - source-log workaround for the non-git DGX mirror,
  - paged env
    `LLAMA_SSM_CONV_SPLIT=1 LLAMA_PAGED_KV_GET_ROWS_F16=1`,
  - expanded gate ops:
    `SSM_CONV,SSM_CONV_SPLIT,GET_ROWS,GATED_DELTA_NET,MUL_MAT,MUL_MAT_ID`,
  - `NPL=128`, `PTOK=128`, `GEN=64`, `PARALLEL=128`, `CTX=131072`,
    `BATCH=2048`, `UBATCH=512`.
- Artifact:
  `/home/mudler/bench/phase104_combined_serving_snapshot/20260701_212551`.

Safety gates:

| phase | env | MoE md5 | dense md5 | `SSM_CONV` | `SSM_CONV_SPLIT` | `GET_ROWS` | `GATED_DELTA_NET` | `MUL_MAT` | `MUL_MAT_ID` |
|-------|-----|---------|-----------|------------|------------------|------------|-------------------|-----------|--------------|
| pre | split + F16 K/V rows | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `45/45` | `6/6` | `49/49` | `48/48` | `1146/1146` | `806/806` |
| post | split + F16 K/V rows | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `45/45` | `6/6` | `49/49` | `48/48` | `1146/1146` | `806/806` |

Serving snapshot, MoE `PTOK=128`, `GEN=64`, `PARALLEL=128`, `N=128`:

| arm | n | agg_tps | decode_agg_tps | decode_perseq_tps | prefill_tps | ttft_mean_ms | wall_s |
|-----|--:|--------:|---------------:|------------------:|------------:|-------------:|-------:|
| paged combined | `128` | `338.6` | `675.8` | `3.93` | `1813.0` | `7121.6` | `24.196` |
| vLLM | `128` | `661.1` | `1028.0` | `6.80` | `5208.7` | `2572.3` | `11.980` |

Ratios:

| n | paged decode/vLLM | paged perseq/vLLM | paged agg/vLLM | paged TTFT/vLLM |
|--:|------------------:|------------------:|---------------:|----------------:|
| `128` | `0.6574` | `0.5779` | `0.5122` | `2.7686` |

Comparison to Phase97 Phase93-only normal serving:

| metric | Phase97 | Phase104 combined | change |
|--------|--------:|------------------:|-------:|
| `agg_tps` | `329.6` | `338.6` | `+2.73%` |
| `decode_agg_tps` | `669.8` | `675.8` | `+0.90%` |
| `prefill_tps` | `1734.5` | `1813.0` | `+4.53%` |
| `ttft_mean_ms` | `7415.4` | `7121.6` | `-3.96%` |
| `wall_s` | `24.851` | `24.196` | `-2.64%` |
| `paged_decode_over_vllm` | `0.6507` | `0.6574` | `+0.0067` |
| `paged_agg_over_vllm` | `0.4958` | `0.5122` | `+0.0164` |

Decision:

- The combined cleanup stack has a small real serving benefit outside `nsys`.
- It does not change the parity conclusion: vLLM is still about `1.52x` faster
  on decode aggregate and `1.95x` faster on aggregate throughput at this shape.
- Carry the combined cleanup env as the best current comparison baseline.
- Next source work should target the remaining high-impact gap, not another
  isolated layout cleanup. The current evidence points to larger serving
  contracts or the dominant GDN/MMQ buckets.

### Phase103: Combined Layout Cleanup Stack

- Date: 2026-07-01.
- Source: `/home/mudler/llama-phase93-qwen3next-gqa-bcast`.
- Local patch status: no new source changes beyond the Phase101 and Phase102
  default-off runtime candidates.
- Env:
  `LLAMA_SSM_CONV_SPLIT=1 LLAMA_PAGED_KV_GET_ROWS_F16=1`.
- Artifacts:
  - standalone combined gates:
    `/home/mudler/bench/phase103_combined_layout_cleanups/20260701_211632/gates_combined`,
  - combined serving profile:
    `/home/mudler/bench/phase103_combined_layout_cleanups/20260701_211821/serving_profile`.

Safety gates:

| gate | env | MoE md5 | dense md5 | `SSM_CONV` | `SSM_CONV_SPLIT` | `GET_ROWS` | `GATED_DELTA_NET` | `MUL_MAT` | `MUL_MAT_ID` |
|------|-----|---------|-----------|------------|------------------|------------|-------------------|-----------|--------------|
| standalone combined | split + F16 K/V rows | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `45/45` | `6/6` | `49/49` | `48/48` | `1146/1146` | `806/806` |
| serving pre combined | split + F16 K/V rows | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `45/45` | `6/6` | `49/49` | `48/48` | `1146/1146` | `806/806` |
| serving post combined | split + F16 K/V rows | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `45/45` | `6/6` | `49/49` | `48/48` | `1146/1146` | `806/806` |

Serving under combined graph-node profiling:

| metric | value |
|--------|------:|
| aggregate t/s | `212.3` |
| decode aggregate t/s | `331.5` |
| decode per-seq t/s | `2.13` |
| prefill t/s | `1569.1` |
| TTFT mean ms | `7858.5` |
| wall s | `38.575` |
| total kernel time | `19.5519 s` |

Fine bucket comparison:

| bucket | Phase101 opt-in | Phase102 opt-in | Phase103 combined | Phase103 vs Phase102 |
|--------|----------------:|----------------:|------------------:|---------------------:|
| `convert_dtype` | `661.35 ms` | `663.99 ms` | `662.36 ms` | `-1.63 ms` |
| `copy_layout` | `80.32 ms` | `112.53 ms` | `78.22 ms` | `-34.31 ms` |
| `concat_layout` | `433.13 ms` | `4.59 ms` | `12.51 ms` | `+7.92 ms` |
| `layout-copy` macro | `1220.30 ms` | `826.87 ms` | `798.52 ms` | `-28.35 ms` |
| `get_rows` | `277.67 ms` | `278.61 ms` | `278.61 ms` | `0.00 ms` |
| `gdn_conv` | `453.54 ms` | `383.90 ms` | `390.08 ms` | `+6.18 ms` |
| `gdn_core` | `5886.76 ms` | `5940.33 ms` | `5930.47 ms` | `-9.86 ms` |
| `mmq_nvfp4` | `6193.70 ms` | `5987.09 ms` | `6001.77 ms` | `+14.68 ms` |

Decision:

- Correctness-clean combined stack. The two cleanup candidates are compatible.
- The combination improves traced serving over Phase102 and recovers the
  Phase101 `copy_layout` reduction while preserving the Phase102 concat removal.
- It is still not a parity-closing lever. Dominant buckets remain
  `gdn_core 5930.47 ms` and `mmq_nvfp4 6001.77 ms`, far larger than the
  residual layout buckets.
- Carry Phase101+Phase102 as a combined default-off cleanup stack for future
  comparisons. Next source work should not spend more time on isolated
  layout-copy cleanup unless it also changes a serving-critical contract.

### Phase102: Split-Input `SSM_CONV` Prefill Path

- Date: 2026-07-01.
- Source: `/home/mudler/llama-phase93-qwen3next-gqa-bcast`.
- Local patch status: default-off runtime candidate:
  - adds `ggml_ssm_conv_split(ctx, conv_states, x_cur, conv_kernel)` while
    reusing `GGML_OP_SSM_CONV`,
  - adds CPU and CUDA split-input implementations plus `SSM_CONV_SPLIT` tests,
  - wires Qwen3Next/Qwen35/Qwen35MoE through
    `LLAMA_SSM_CONV_SPLIT=1` only for `n_seq_tokens > 1`,
    `n_seq_tokens >= K-1`, and `cparams.n_rs_seq == 0`,
  - keeps decode fused and rollback/short-prefill cases on the existing path.
- Local build: `cmake --build build --target test-backend-ops -j $(nproc)`.
- DGX build:
  `cmake --build /home/mudler/llama-phase93-qwen3next-gqa-bcast/build --target llama-server llama-completion test-backend-ops -j $(nproc)`.
- Debug note: the first split-minus-base test used the default normalized-MSE
  metric and failed with `ERR = inf` for `d_conv=4` because the CPU reference is
  exactly zero. A direct split CUDA-vs-CPU diagnostic passed `6/6`; the final
  semantic test keeps `split - base` and uses absolute max error.
- Artifacts:
  - default/opt-in standalone gates:
    `/home/mudler/bench/phase102_ssm_conv_split/20260701_210559`,
  - opt-in serving profile:
    `/home/mudler/bench/phase102_ssm_conv_split/20260701_210907/serving_profile`.

Safety gates:

| gate | env | MoE md5 | dense md5 | `SSM_CONV` | `SSM_CONV_SPLIT` | `GET_ROWS` | `GATED_DELTA_NET` | `MUL_MAT` | `MUL_MAT_ID` |
|------|-----|---------|-----------|------------|------------------|------------|-------------------|-----------|--------------|
| default | none | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `45/45` | `6/6` | `49/49` | `48/48` | `1146/1146` | `806/806` |
| standalone opt-in | `LLAMA_SSM_CONV_SPLIT=1` | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `45/45` | `6/6` | `49/49` | `48/48` | `1146/1146` | `806/806` |
| serving pre opt-in | `LLAMA_SSM_CONV_SPLIT=1` | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `45/45` | `6/6` | `49/49` | `48/48` | `1146/1146` | `806/806` |
| serving post opt-in | `LLAMA_SSM_CONV_SPLIT=1` | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `45/45` | `6/6` | `49/49` | `48/48` | `1146/1146` | `806/806` |

Serving under opt-in graph-node profiling:

| metric | value |
|--------|------:|
| aggregate t/s | `206.1` |
| decode aggregate t/s | `320.0` |
| decode per-seq t/s | `2.06` |
| prefill t/s | `1538.0` |
| TTFT mean ms | `7928.4` |
| wall s | `39.743` |
| total kernel time | `19.5482 s` |

Fine bucket comparison:

| bucket | Phase100 | Phase101 opt-in | Phase102 opt-in | Phase102 vs Phase101 |
|--------|---------:|----------------:|----------------:|---------------------:|
| `convert_dtype` | `661.73 ms` | `661.35 ms` | `663.99 ms` | `+2.64 ms` |
| `copy_layout` | `116.25 ms` | `80.32 ms` | `112.53 ms` | `+32.21 ms` |
| `concat_layout` | `438.15 ms` | `433.13 ms` | `4.59 ms` | `-428.54 ms` |
| `layout-copy` macro | `1262.58 ms` | `1220.30 ms` | `826.87 ms` | `-393.43 ms` |
| `get_rows` | `283.47 ms` | `277.67 ms` | `278.61 ms` | `+0.94 ms` |
| `gdn_conv` | `458.13 ms` | `453.54 ms` | `383.90 ms` | `-69.64 ms` |
| `gdn_core` | `5919.48 ms` | `5886.76 ms` | `5940.33 ms` | `+53.57 ms` |
| `mmq_nvfp4` | `6127.44 ms` | `6193.70 ms` | `5987.09 ms` | `-206.61 ms` |

Decision:

- Correctness-clean and structurally useful: the split op removes the large
  concat materialization from the eligible prefill/microbatch path.
- It does not improve live serving throughput in the profiled `N=128`,
  `PTOK=128`, `GEN=64`, `PARALLEL=128` window; aggregate and decode are below
  Phase100/101 traced profiles despite lower total kernel time.
- Carry as a default-off cleanup candidate pending repeat A/B or a follow-up
  that fuses the remaining state update/copy work. Do not promote as a parity
  lever by itself.
- Next higher-value work should target the still-dominant buckets:
  `gdn_core` and `mmq_nvfp4`, or a larger serving scheduler/packed-decode
  contract.

### Phase101: Paged K/V F16 `GET_ROWS` A/B

- Date: 2026-07-01.
- Source: `/home/mudler/llama-phase93-qwen3next-gqa-bcast`.
- Local patch status: default-off runtime candidate:
  - `ggml_get_rows_type(ctx, a, b, type)` helper added while preserving stock
    `ggml_get_rows` widening semantics,
  - CPU reference supports F16 source -> F16 output row copy,
  - CUDA already supports F16 `GET_ROWS` output through `get_rows_cuda`,
  - paged attention K/V gather calls typed F16 `GET_ROWS` only when
    `LLAMA_PAGED_KV_GET_ROWS_F16=1` and the K/V cache tensor is F16,
  - tests add F16-output `GET_ROWS` cases.
- Local build: `cmake --build build --target test-backend-ops -j $(nproc)`.
- DGX build:
  `cmake --build /home/mudler/llama-phase93-qwen3next-gqa-bcast/build --target llama-server llama-completion test-backend-ops -j $(nproc)`.
- Artifacts:
  - default gates:
    `/home/mudler/bench/phase101_kv_get_rows_f16/20260701_203621/gates_default`,
  - opt-in gates:
    `/home/mudler/bench/phase101_kv_get_rows_f16/20260701_203754/gates_optin`,
  - opt-in serving profile:
    `/home/mudler/bench/phase101_kv_get_rows_f16/20260701_203930/serving_profile`.

Safety gates:

| gate | env | MoE md5 | dense md5 | `GET_ROWS` | `GATED_DELTA_NET` | `MUL_MAT` | `MUL_MAT_ID` |
|------|-----|---------|-----------|------------|-------------------|-----------|--------------|
| default | none | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `49/49` | `48/48` | `1146/1146` | `806/806` |
| standalone opt-in | `LLAMA_PAGED_KV_GET_ROWS_F16=1` | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `49/49` | `48/48` | `1146/1146` | `806/806` |
| serving pre opt-in raw log | `LLAMA_PAGED_KV_GET_ROWS_F16=1` | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `49/49` | `48/48` | `1146/1146` | `806/806` |
| serving post opt-in raw log | `LLAMA_PAGED_KV_GET_ROWS_F16=1` | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `49/49` | `48/48` | `1146/1146` | `806/806` |

Serving under opt-in graph-node profiling:

| metric | value |
|--------|------:|
| aggregate t/s | `206.4` |
| decode aggregate t/s | `328.0` |
| decode per-seq t/s | `2.08` |
| prefill t/s | `1479.6` |
| TTFT mean ms | `8211.1` |
| wall s | `39.678` |
| total kernel time | `20.1989 s` |

Fine bucket comparison against Phase100:

| bucket | Phase100 | Phase101 opt-in | change |
|--------|---------:|----------------:|-------:|
| `convert_dtype` | `661.73 ms` | `661.35 ms` | `-0.38 ms` |
| `copy_layout` | `116.25 ms` | `80.32 ms` | `-35.93 ms` |
| `concat_layout` | `438.15 ms` | `433.13 ms` | `-5.02 ms` |
| `layout-copy` macro | `1262.58 ms` | `1220.30 ms` | `-42.28 ms` |
| `get_rows` | `283.47 ms` | `277.67 ms` | `-5.80 ms` |
| `gdn_core` | `5919.48 ms` | `5886.76 ms` | `-32.72 ms` |
| `mmq_nvfp4` | `6127.44 ms` | `6193.70 ms` | `+66.26 ms` |

Decision:

- Correctness-clean but not parity-closing.
- The hypothesis that K/V F16 typed gather would materially reduce
  `convert_dtype` is mostly false for this serving window; `convert_dtype`
  stayed flat.
- The patch does remove some `copy_layout` work and keeps md5/op gates green,
  so it can remain as a small default-off cleanup candidate, but it should not
  be promoted or treated as the main parity path without a repeat serving A/B.
- Next higher-value runtime work remains either the two-source `SSM_CONV`
  contract for `conv_input` or a larger GDN/MMQ serving lever.

### Phase100: Layout Trace View-Source Attribution

- Date: 2026-07-01.
- Source: `/home/mudler/llama-phase93-qwen3next-gqa-bcast`.
- Local patch status: trace-only source change in
  `ggml/src/ggml-cuda/ggml-cuda.cu`; `LLAMA_LAYOUT_TRACE` now prints
  `dst_view`, `src0_view`, and `src1_view`. Default execution is unchanged.
- Local build: `cmake --build build --target test-backend-ops -j $(nproc)`.
- DGX build:
  `cmake --build /home/mudler/llama-phase93-qwen3next-gqa-bcast/build --target llama-server llama-completion test-backend-ops -j $(nproc)`.
- Harness:
  - trace gate:
    `EXTRA_ENV=LLAMA_LAYOUT_TRACE=128 OPS=GATED_DELTA_NET,MUL_MAT,MUL_MAT_ID`,
  - serving profile: streamed `/home/mudler/bench/phase76_current_moe_profile.sh`
    with source logging fixed for the mirror, `GATED_DELTA_NET` gates, and
    `LLAMA_LAYOUT_TRACE=30000` on `llama-server`,
  - `N=128`, `PTOK=128`, `GEN=64`, `PARALLEL=128`, `CTX=131072`.
- Artifacts:
  - trace gate:
    `/home/mudler/bench/phase100_layout_view_trace/20260701_201635/trace_gates`,
  - serving profile:
    `/home/mudler/bench/phase100_layout_view_trace/20260701_201800/serving_profile`.

Safety gates:

| gate | MoE md5 | dense md5 | `GATED_DELTA_NET` | `MUL_MAT` | `MUL_MAT_ID` |
|------|---------|-----------|-------------------|-----------|--------------|
| trace-enabled standalone | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `48/48` | `1146/1146` | `806/806` |
| serving pre raw log | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `48/48` | `1146/1146` | `806/806` |
| serving post raw log | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `48/48` | `1146/1146` | `806/806` |

Serving under graph-node profiling plus view-source layout trace:

| metric | value |
|--------|------:|
| aggregate t/s | `207.0` |
| decode aggregate t/s | `327.9` |
| decode per-seq t/s | `2.10` |
| prefill t/s | `1490.9` |
| TTFT mean ms | `8302.7` |
| wall s | `39.578` |
| total kernel time | `20.3464 s` |

Fine buckets:

| bucket | time | share | launches |
|--------|-----:|------:|---------:|
| `mmq_nvfp4` | `6127.44 ms` | `30.12%` | `33682` |
| `gdn_core` | `5919.48 ms` | `29.09%` | `4680` |
| `convert_dtype` | `661.73 ms` | `3.25%` | `52060` |
| `gdn_conv` | `458.13 ms` | `2.25%` | `7230` |
| `concat_layout` | `438.15 ms` | `2.15%` | `2130` |
| `copy_layout` | `116.25 ms` | `0.57%` | `8090` |
| `ew_repeat` | `46.45 ms` | `0.23%` | `18720` |

View-source trace findings:

| finding | evidence |
|---------|----------|
| K/V cache reads feed F32->F16 converts | For attention layers, `GET_ROWS` outputs F32 `node_*` from F16 `cache_k_l*` / `cache_v_l*`, then a `CPY` downcasts a view of that node to F16. Examples: `node_358 <- cache_k_l3` and `node_365 <- cache_v_l3`, followed by `cpy` rows with `src0_view=node_358` / `node_365`, `src0_type=f32`, `src1_type=f16`, and shapes like `256x64x2x8`, `256x128x2x8`, `256x162x2x8`. |
| The pattern repeats across attention layers | The same pair pattern appears for `cache_k_l7/cache_v_l7` (`node_798/node_805`), `cache_k_l11/cache_v_l11` (`node_1238/node_1245`), and later attention layers. |
| Some converts remain anonymous | `959` F32->F16 `CPY` trace rows still had no tensor or view names; do not assume the K/V path accounts for the full `convert_dtype` bucket without a targeted A/B. |
| Phase99 conv attribution is confirmed | `concat` rows show `conv_input-*` from `conv_states_reshaped-*` and `qkv_mixed_transposed-*`; the new view fields map `qkv_mixed_transposed-*` back to layer-local `node_*` producers. |

Decision:

- Carry the trace-only Phase100 patch as default-off instrumentation.
- The next runtime source candidate should target the attention K/V cache gather
  dtype path: avoid `GET_ROWS` producing F32 only to downcast to F16 when the
  consumer wants F16. This is more directly connected to the `convert_dtype`
  bucket than a generic copy/layout tweak.
- Keep the two-source `SSM_CONV` contract as a separate later phase for
  `concat_layout`; do not mix it with the K/V dtype experiment.

### Phase99: Serving Layout Trace Attribution

- Date: 2026-07-01.
- Source: `/home/mudler/llama-phase93-qwen3next-gqa-bcast`.
- Local patch status: no source change; the default-off `LLAMA_LAYOUT_TRACE`
  hook was already present in the fork and DGX mirror.
- Harness:
  - trace gate:
    `EXTRA_ENV=LLAMA_LAYOUT_TRACE=128 OPS=GATED_DELTA_NET,MUL_MAT,MUL_MAT_ID`,
  - serving profile: streamed `/home/mudler/bench/phase76_current_moe_profile.sh`
    with measurement-only edits for source logging, `GATED_DELTA_NET` gates,
    and `LLAMA_LAYOUT_TRACE=30000` on `llama-server`,
  - `N=128`, `PTOK=128`, `GEN=64`, `PARALLEL=128`, `CTX=131072`.
- Artifacts:
  - trace gate:
    `/home/mudler/bench/phase99_layout_trace/20260701_200637/trace_gates`,
  - serving profile:
    `/home/mudler/bench/phase99_layout_trace/20260701_200835/serving_profile`.

Safety gates:

| gate | MoE md5 | dense md5 | `GATED_DELTA_NET` | `MUL_MAT` | `MUL_MAT_ID` |
|------|---------|-----------|-------------------|-----------|--------------|
| trace-enabled standalone | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `48/48` | `1146/1146` | `806/806` |
| serving pre raw log | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `48/48` | `1146/1146` | `806/806` |
| serving post raw log | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `48/48` | `1146/1146` | `806/806` |

Serving under graph-node profiling plus layout trace:

| metric | value |
|--------|------:|
| aggregate t/s | `208.2` |
| decode aggregate t/s | `332.9` |
| decode per-seq t/s | `2.12` |
| prefill t/s | `1476.8` |
| TTFT mean ms | `8466.3` |
| wall s | `39.341` |
| total kernel time | `20.2408 s` |

Macro buckets:

| bucket | time | share |
|--------|-----:|------:|
| GDN | `6709.45 ms` | `33.15%` |
| MoE/FFN-GEMM | `6158.11 ms` | `30.42%` |
| bf16/fp8-proj | `2786.81 ms` | `13.77%` |
| layout-copy | `1269.35 ms` | `6.27%` |
| ew-mul(weight/norm/GDN) | `729.08 ms` | `3.60%` |
| act-quant | `686.52 ms` | `3.39%` |
| FA | `268.04 ms` | `1.32%` |

Fine buckets:

| bucket | time | share | launches |
|--------|-----:|------:|---------:|
| `mmq_nvfp4` | `5936.34 ms` | `29.33%` | `34162` |
| `gdn_core` | `5920.40 ms` | `29.25%` | `4710` |
| `convert_dtype` | `662.34 ms` | `3.27%` | `52440` |
| `gdn_conv` | `457.47 ms` | `2.26%` | `7290` |
| `concat_layout` | `440.01 ms` | `2.17%` | `2130` |
| `copy_layout` | `119.16 ms` | `0.59%` | `8110` |
| `ew_repeat` | `47.83 ms` | `0.24%` | `18840` |

Layout trace summary:

| route | trace lines |
|-------|------------:|
| `get_rows` | `18779` |
| `cpy` | `4638` |
| `cont` | `4384` |
| `concat` | `2199` |

Top attribution:

| finding | evidence |
|---------|----------|
| `concat_layout` is conv input materialization | `conv_input-* = concat(conv_states_reshaped-*, qkv_mixed_transposed-*)`; top shapes include `45x8192x12x1 = 3x8192x12x1 + 42x8192x12x1` (`450` trace lines) and `49x8192x11x1 = 3x8192x11x1 + 46x8192x11x1` (`180` trace lines). |
| `copy_layout` includes conv state writeback | `conv_state_update-* = cpy(conv_state_last-*, conv_state_update-*)`; top grouped shapes include `24576x12x1x1 <- 3x8192x12x1` (`780` trace lines), `24576x11x1x1` (`420`), and `24576x13x1x1` (`270`). |
| `convert_dtype` needs stronger attribution | the trace sees many unnamed `CPY` rows with F32 source and F16 destination, e.g. `256x166x2x11`, `256x166x2x12`, and similar attention/KV-shaped tensors; names are not preserved by the current dispatch trace. |

Decision:

- Phase99 is a measurement-only phase; no runtime patch was carried or reverted.
- Do not spend more time on the Phase96-style conv-state identity shortcut.
  The serving hot layout path is the prefill/microbatch `conv_input` concat
  feeding `SSM_CONV`, not just decode update writeback.
- A conv-side source phase must be a larger two-source `SSM_CONV` contract that
  reads `(conv_states, qkv_mixed)` as a logical concatenation, or it is too small
  to fund. If not coding that, first extend trace attribution for the larger
  unnamed F32->F16 `convert_dtype` bucket.

### Phase98: Phase93 Serving Graph-Node Profile

- Date: 2026-07-01.
- Source: `/home/mudler/llama-phase93-qwen3next-gqa-bcast`.
- Local patch status: no source change; this measured the carried Phase93 stack
  after Phase95 and Phase96 reverts.
- Harness:
  - streamed `/home/mudler/bench/phase76_current_moe_profile.sh` with two
    measurement-only edits:
    - source logging does not call `git` because the DGX Phase93 mirror is a
      source copy without `.git`,
    - pre/post gate ops include `GATED_DELTA_NET,MUL_MAT,MUL_MAT_ID`,
  - `SRC=/home/mudler/llama-phase93-qwen3next-gqa-bcast`,
  - `BIN=/home/mudler/llama-phase93-qwen3next-gqa-bcast/build/bin`,
  - `N=128`, `PTOK=128`, `GEN=64`, `PARALLEL=128`, `CTX=131072`.
- Artifact:
  `/home/mudler/bench/phase98_phase93_serving_profile/20260701_215715`.

Safety gates:

| phase | MoE md5 | dense md5 | `GATED_DELTA_NET` | `MUL_MAT` | `MUL_MAT_ID` |
|-------|---------|-----------|-------------------|-----------|--------------|
| pre | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `48/48` | `1146/1146` | `806/806` |
| post | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `48/48` | `1146/1146` | `806/806` |

Serving under graph-node profiling, MoE `N=128`, `PTOK=128`, `GEN=64`,
`PARALLEL=128`:

| metric | value |
|--------|------:|
| aggregate t/s | `208.4` |
| decode aggregate t/s | `332.0` |
| decode per-seq t/s | `2.12` |
| prefill t/s | `1488.1` |
| TTFT mean ms | `8315.5` |
| wall s | `39.296` |
| total kernel time | `20.0411 s` |

Macro buckets:

| bucket | time | share |
|--------|-----:|------:|
| GDN | `6679.96 ms` | `33.33%` |
| MoE/FFN-GEMM | `6034.52 ms` | `30.11%` |
| bf16/fp8-proj | `2766.06 ms` | `13.80%` |
| layout-copy | `1257.60 ms` | `6.28%` |
| ew-mul(weight/norm/GDN) | `726.03 ms` | `3.62%` |
| act-quant | `686.69 ms` | `3.43%` |
| FA | `265.00 ms` | `1.32%` |

Fine buckets:

| bucket | time | share | launches |
|--------|-----:|------:|---------:|
| `gdn_core` | `5892.99 ms` | `29.40%` | `4680` |
| `mmq_nvfp4` | `5809.55 ms` | `28.99%` | `33442` |
| `cublas_bf16_gemm` | `1745.83 ms` | `8.71%` | `22200` |
| `cutlass_bf16_gemm` | `740.22 ms` | `3.69%` | `26190` |
| `ew_mul` | `720.94 ms` | `3.60%` | `48326` |
| `act_quant` | `686.69 ms` | `3.43%` | `37526` |
| `convert_dtype` | `663.45 ms` | `3.31%` | `51300` |
| `gdn_conv` | `457.11 ms` | `2.28%` | `7260` |
| `concat_layout` | `430.25 ms` | `2.15%` | `2100` |
| `get_rows` | `283.56 ms` | `1.41%` | `27978` |
| `gdn_gather` | `231.32 ms` | `1.15%` | `360` |
| `mm_ids` | `119.93 ms` | `0.60%` | `16680` |
| `gdn_l2norm` | `98.54 ms` | `0.49%` | `9360` |
| `gemv_moe_q` | `81.77 ms` | `0.41%` | `1560` |

Decision:

- Phase98 confirms the serving hot path is still a two-bucket problem:
  `gdn_core` and `mmq_nvfp4` together account for `58.39%` of kernel time.
- The repeated negative GDN micro-tries (Phase91, Phase92, Phase95, Phase96)
  argue against more scalar/launch/gather shortcuts. A credible GDN follow-up
  needs a larger recurrence design with a measured PoC, not another local tweak.
- `layout-copy` is now large enough (`6.28%`, led by `convert_dtype` and
  `concat_layout`) to deserve attribution before code changes, but it is not
  parity-closing by itself.
- Next phase should either:
  - attribute `convert_dtype`/`concat_layout` to exact graph nodes and remove a
    proven material copy, or
  - pursue a larger `gdn_core`/`mmq_nvfp4` serving lever with a strict PoC gate.

### Phase97: Phase93 Serving Snapshot, N=128

- Date: 2026-07-01.
- Source: `/home/mudler/llama-phase93-qwen3next-gqa-bcast`.
- Local patch status: no source change; this measured the carried Phase93 stack
  after Phase95 and Phase96 reverts.
- Harness:
  - streamed `paged-current-serving-snapshot.sh` with a one-line source-log
    workaround because the DGX Phase93 mirror is a source copy without `.git`,
  - `SRC=/home/mudler/llama-phase93-qwen3next-gqa-bcast`,
  - `BUILD_DIR=/home/mudler/llama-phase93-qwen3next-gqa-bcast/build`,
  - `BIN=/home/mudler/llama-phase93-qwen3next-gqa-bcast/build/bin`,
  - `NPL=128`, `PTOK=128`, `GEN=64`, `PARALLEL=128`, `CTX=131072`,
  - gate ops: `GATED_DELTA_NET,MUL_MAT,MUL_MAT_ID`.
- Artifact:
  `/home/mudler/bench/phase97_phase93_serving_snapshot/20260701_214648`.

Safety gates:

| phase | MoE md5 | dense md5 | `GATED_DELTA_NET` | `MUL_MAT` | `MUL_MAT_ID` |
|-------|---------|-----------|-------------------|-----------|--------------|
| pre | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `48/48` | `1146/1146` | `806/806` |
| post | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `48/48` | `1146/1146` | `806/806` |

Serving snapshot, MoE `PTOK=128`, `GEN=64`, `PARALLEL=128`, `N=128`:

| arm | n | agg_tps | decode_agg_tps | decode_perseq_tps | prefill_tps | ttft_mean_ms | wall_s |
|-----|--:|--------:|---------------:|------------------:|------------:|-------------:|-------:|
| paged Phase93 | `128` | `329.6` | `669.8` | `3.85` | `1734.5` | `7415.4` | `24.851` |
| vLLM | `128` | `664.8` | `1029.4` | `6.79` | `5271.8` | `2519.5` | `11.929` |

Ratios:

| n | paged decode/vLLM | paged perseq/vLLM | paged agg/vLLM | paged TTFT/vLLM |
|--:|------------------:|------------------:|---------------:|----------------:|
| `128` | `0.6507` | `0.5670` | `0.4958` | `2.9432` |

Decision:

- Phase93 remains a valid decode-profile improvement, but it is not
  serving-parity at `n=128`.
- The Phase97 paged aggregate is slightly above the Phase72 default snapshot
  (`329.6` vs `325.8`), and TTFT improves (`7415.4 ms` vs `7822.5 ms`), but
  decode aggregate is lower than Phase72 (`669.8` vs `714.0`) while vLLM stays
  essentially unchanged (`1029.4` vs `1029.5`).
- Treat Phase93 as worth carrying for source quality and decode-profile gain,
  but the next parity phase needs a larger serving-impact lever. More isolated
  GDN/conv micro-optimizations are unlikely to close the live serving gap.

### Phase96: Conv-State Identity Fast Path

- Date: 2026-07-01.
- Source: `/home/mudler/llama-phase93-qwen3next-gqa-bcast`.
- Local patch status: runtime model-graph change reverted after profiling;
  Phase93 is still the current carried source.
- Rationale:
  - The Phase93 decode profile showed `ssm_conv_update_ids_f32`/`gdn_conv`
    around the 66-72 ms range, larger than the cleanly attributable remaining
    GDN producer math.
  - The recurrent GDN path already uses a direct in-place op when
    `s_copy_main` is identity. This trial added the same shape of branch to
    `build_conv_state_fused`: when `inp->s_copy_main_identity` was true, it
    viewed the active conv-state cache slots directly and called
    `ggml_ssm_conv_update_inplace` instead of the ids variant.
  - The existing `build_rs` zero/extra-state maintenance stayed around the
    lambda, and the CUDA update kernel loads the conv window before writing the
    same slot, so the identity aliasing was expected to be safe.
- Gate and profile artifacts:
  - canonical gates:
    `/home/mudler/bench/phase96_conv_identity_fastpath/20260701_214023/canonical_gates`,
  - decode-only profile:
    `/home/mudler/bench/phase96_conv_identity_fastpath/20260701_214141/decode_profile`.

Safety gates:

| check | result |
|-------|--------|
| local build | `cmake --build build --target test-backend-ops -j $(nproc)` OK |
| local CPU `SSM_CONV` | `45/45` |
| DGX CUDA `SSM_CONV` | `45/45`, `Backend CUDA0: OK` |
| DGX CUDA `GATED_DELTA_NET_INPLACE_IDS` | `6/6`, `Backend CUDA0: OK` |
| canonical MoE md5 | `8cb0ce23777bf55f92f63d0292c756b0` |
| canonical dense md5 | `5951a5b4d624ce891e22ab5fca9bc439` |
| canonical `SSM_CONV` | `45/45`, `Backend CUDA0: OK` |
| canonical `GATED_DELTA_NET` | `48/48`, `Backend CUDA0: OK` |
| canonical `MUL_MAT` | `1146/1146`, `Backend CUDA0: OK` |
| canonical `MUL_MAT_ID` | `806/806`, `Backend CUDA0: OK` |
| profile pre/post md5/op gates | all OK |

Decode-only profile, MoE `N=128`, `N_PREDICT=2048`, capture after median
depth `74 -> 96`, default env:

| arm | total kernel s | GDN ms | `gdn_core` ms | `gdn_core` launches | `gdn_conv` ms | `mmq_nvfp4` ms |
|-----|---------------:|-------:|--------------:|--------------------:|--------------:|---------------:|
| Phase93 default | `3.5476` | `1409.19` | `1333.48` | `570` | about `66.40` to `72.26` | `1421.63` |
| Phase96 conv identity | `3.6723` | `1486.12` | `1406.57` | `600` | `70.42` | `1433.84` |

Decision:

- Reject the conv-state identity fast path. It is inference-safe, but it did
  not improve `gdn_conv` and worsened total kernel time and `gdn_core` versus
  Phase93.
- Revert the runtime model-graph change and keep Phase93 as the current carried
  candidate.
- Do not retry the conv identity branch as a speed lever unless a same-window
  trace shows the ids variant itself is materially slower than the direct
  variant independent of launch-count/capture variance.

### Phase95: GDN Warp Scalar-Gate Broadcast

- Date: 2026-07-01.
- Source: `/home/mudler/llama-phase93-qwen3next-gqa-bcast`.
- Local patch status: runtime CUDA change reverted after profiling; Phase93 is
  still the current carried source.
- Env:
  - `GDN_WARP_SCALAR_GATE=1`
- Rationale:
  - After Phase93, the remaining GDN producer buckets are small while
    `gdn_core` remains the largest target.
  - The scalar non-KDA decode path loads one scalar gate value per
    `(head, seq, token)`, but every lane computes `expf(*g_t)`. This
    default-off trial computed the scalar gate on lane 0 and broadcast it within
    the warp for the one-token `S_v=128`, non-KDA, default `16x8` decode path.
  - The recurrence order, reductions, state update, and stores were unchanged.
- Gate and profile artifacts:
  - canonical gates:
    `/home/mudler/bench/phase95_gdn_warp_scalar_gate/20260701_213150/canonical_gates`,
  - decode-only profile:
    `/home/mudler/bench/phase95_gdn_warp_scalar_gate/20260701_213311/decode_profile`.

Safety gates:

| check | result |
|-------|--------|
| local build | `cmake --build build --target test-backend-ops -j $(nproc)` OK |
| local CPU `GATED_DELTA_NET` | `48/48` |
| local CPU `GATED_DELTA_NET_INPLACE_IDS` | `6/6` |
| DGX CUDA `GATED_DELTA_NET`, `GDN_WARP_SCALAR_GATE=1` | `48/48`, `Backend CUDA0: OK` |
| DGX CUDA `GATED_DELTA_NET_INPLACE_IDS`, `GDN_WARP_SCALAR_GATE=1` | `6/6`, `Backend CUDA0: OK` |
| canonical MoE md5 | `8cb0ce23777bf55f92f63d0292c756b0` |
| canonical dense md5 | `5951a5b4d624ce891e22ab5fca9bc439` |
| canonical `GATED_DELTA_NET` | `48/48`, `Backend CUDA0: OK` |
| canonical `MUL_MAT` | `1146/1146`, `Backend CUDA0: OK` |
| canonical `MUL_MAT_ID` | `806/806`, `Backend CUDA0: OK` |
| profile pre/post md5/op gates | all OK |

Decode-only profile, MoE `N=128`, `N_PREDICT=2048`, capture after median
depth `65 -> 87`, `PROFILE_ENV=GDN_WARP_SCALAR_GATE=1`:

| arm | total kernel s | GDN ms | GDN % | `gdn_core` ms | `gdn_core` launches | `mmq_nvfp4` ms |
|-----|---------------:|-------:|------:|--------------:|--------------------:|---------------:|
| Phase93 default | `3.5476` | `1409.19` | `39.72%` | `1333.48` | `570` | `1421.63` |
| Phase95 warp scalar gate | `3.6317` | `1483.44` | `40.85%` | `1402.40` | `599` | `1402.88` |

Decision:

- Reject `GDN_WARP_SCALAR_GATE=1`. It is inference-safe, but worsens the target
  `gdn_core` bucket by `+68.92 ms` and total kernel time by `+84.1 ms` versus
  Phase93.
- Revert the runtime CUDA change and keep Phase93 as the current carried
  candidate.
- Do not retry scalar-gate warp broadcast unless a future profile shows SFU
  pressure, rather than recurrent state traffic/reductions, dominating the
  decode GDN core.

### Phase94: Phase93 GDN Geometry Reprobe, 8x8

- Date: 2026-07-01.
- Source: `/home/mudler/llama-phase93-qwen3next-gqa-bcast`.
- Local patch status: no source change; env-only geometry probe rejected.
- Env:
  - `GDN_NW=8`
  - `GDN_CPW=8`
- Rationale:
  - Phase93 changed the active GDN launch mix and dropped `gdn_core` to the
    current best `1333.48 ms`.
  - The 8x8 geometry keeps a single S_v=128 column tile (`grid.z=1`) like the
    default 16x8 path, but halves threads per block. This tested whether lower
    block occupancy pressure helped after grouped Q/K broadcast.
- Gate and profile artifacts:
  - canonical gates:
    `/home/mudler/bench/phase94_gdn_geometry_phase93/20260701_211730/canonical_gates_8x8`,
  - decode-only profile:
    `/home/mudler/bench/phase94_gdn_geometry_phase93/20260701_211855/decode_profile_8x8`.

Safety gates:

| check | result |
|-------|--------|
| DGX CUDA `GATED_DELTA_NET`, `GDN_NW=8 GDN_CPW=8` | `48/48`, `Backend CUDA0: OK` |
| canonical MoE md5 | `8cb0ce23777bf55f92f63d0292c756b0` |
| canonical dense md5 | `5951a5b4d624ce891e22ab5fca9bc439` |
| canonical `GATED_DELTA_NET` | `48/48`, `Backend CUDA0: OK` |
| canonical `MUL_MAT` | `1146/1146`, `Backend CUDA0: OK` |
| canonical `MUL_MAT_ID` | `806/806`, `Backend CUDA0: OK` |
| profile pre/post md5/op gates | all OK |

Decode-only profile, MoE `N=128`, `N_PREDICT=2048`, capture after median
depth `74 -> 96`, `PROFILE_ENV=GDN_NW=8 GDN_CPW=8`:

| arm | total kernel s | GDN ms | GDN % | `gdn_core` ms | `gdn_core` launches | `mmq_nvfp4` ms |
|-----|---------------:|-------:|------:|--------------:|--------------------:|---------------:|
| Phase93 default geometry | `3.5476` | `1409.19` | `39.72%` | `1333.48` | `570` | `1421.63` |
| Phase94 8x8 geometry | `3.6223` | `1522.02` | `42.02%` | `1440.79` | `600` | `1352.68` |

Decision:

- Reject `GDN_NW=8 GDN_CPW=8` for Phase93. It is inference-safe, but worsens
  the target `gdn_core` bucket by `+107.31 ms` and total kernel time by
  `+74.7 ms`.
- Keep the Phase93 default `16x8` geometry.
- The profile also shows remaining producer-side GDN work is small compared with
  recurrence core: `l2_norm_f32 8.65 ms`, GDN gate/sigmoid kernels about
  `12.75 ms`, and remaining repeat `5.34 ms` in the Phase93 default trace. The
  next candidate should target recurrence work or a larger packed decode
  contract, not another small producer-only fusion.

### Phase93: Qwen3Next Grouped Q/K Broadcast for Fused GDN

- Date: 2026-07-01.
- Source: `/home/mudler/llama-phase93-qwen3next-gqa-bcast`.
- Local patch status: carried as a positive candidate.
- Patch scope:
  - added `ggml_gated_delta_net_set_bcast(tensor, grouped)` using
    `op_params[2]`,
  - kept default GDN Q/K head mapping as the existing tiled/modulo behavior,
  - added grouped mapping for opt-in GDN calls:
    `qk_head = value_head / (H_v / H_k)`,
  - threaded the grouped flag through CPU GDN, CUDA sequential decode, and CUDA
    chunked prefill kernels,
  - changed Qwen3Next to skip the explicit q/k repeat only when the GDN op path
    can consume grouped broadcast,
  - added grouped broadcast backend-op coverage for one-token and prompt-sized
    `GATED_DELTA_NET`.
- Build artifact:
  `/home/mudler/llama-phase93-qwen3next-gqa-bcast/build`.
- Gate and profile artifacts:
  - canonical gates:
    `/home/mudler/bench/phase93_qwen3next_gqa_bcast/20260701_210857/canonical_gates`,
  - decode-only profile:
    `/home/mudler/bench/phase93_qwen3next_gqa_bcast/20260701_211019/decode_profile`.

Safety gates:

| check | result |
|-------|--------|
| local build | `cmake --build build --target test-backend-ops -j $(nproc)` OK |
| local CPU `GATED_DELTA_NET` | `48/48`, includes grouped AR and PP cases |
| local CPU `GATED_DELTA_NET_INPLACE_IDS` | `6/6` |
| DGX CUDA `GATED_DELTA_NET` | `48/48`, includes grouped AR and PP cases |
| DGX CUDA `GATED_DELTA_NET_INPLACE_IDS` | `6/6` |
| canonical MoE md5 | `8cb0ce23777bf55f92f63d0292c756b0` |
| canonical dense md5 | `5951a5b4d624ce891e22ab5fca9bc439` |
| canonical `GATED_DELTA_NET` | `48/48`, `Backend CUDA0: OK` |
| canonical `MUL_MAT` | `1146/1146`, `Backend CUDA0: OK` |
| canonical `MUL_MAT_ID` | `806/806`, `Backend CUDA0: OK` |
| profile pre/post md5/op gates | all OK |

Decode-only profile, MoE `N=128`, `N_PREDICT=2048`, capture after median
depth `73 -> 94`, default env:

| arm | total kernel s | GDN ms | GDN % | `gdn_core` ms | `gdn_core` launches | `mmq_nvfp4` ms |
|-----|---------------:|-------:|------:|--------------:|--------------------:|---------------:|
| Phase87 same-source default | `3.6310` | `1471.27` | `40.52%` | `1390.56` | `598` | `1416.46` |
| Phase91 pack2 PDL-fix | `3.5813` | `1505.91` | `42.05%` | `1425.44` | `598` | `1333.39` |
| Phase92 store-fused | `3.7419` | `1609.81` | `43.02%` | `1529.72` | `600` | `1383.82` |
| Phase93 Qwen3Next grouped broadcast | `3.5476` | `1409.19` | `39.72%` | `1333.48` | `570` | `1421.63` |

Decision:

- Carry Phase93. It is md5/op clean and improves the target `gdn_core` bucket by
  `-57.08 ms` vs Phase87 same-source default, `-91.86 ms` vs Phase85
  identity-state (`1400.34 ms`), and `-92.0 ms` vs the rejected Phase91 pack2
  trial.
- The win is consistent with the intended work reduction: Qwen3Next stops
  materializing repeated q/k heads for fused GDN and lets the op map value heads
  to grouped q/k heads directly.
- Next follow-up should profile/count node-level repeat/layout buckets around
  Qwen3Next GDN to confirm whether more vLLM-style packed decode producer work
  remains worth porting.

### Phase92: Scalar Decode Store-Fused GDN Trial

- Date: 2026-07-01.
- Source: `/home/mudler/llama-phase92-gdn-store-fused`, default-off CUDA
  experiment on top of the Phase90/91 guardrail stack.
- Local patch status: runtime CUDA changes reverted after profiling; guardrail
  stack remains.
- Patch scope:
  - added a `STORE_FUSED` CUDA kernel instantiation behind
    `GDN_SCALAR_DECODE_STORE_FUSED=1`,
  - gated it to S_v=128, scalar-gate, final-state, one-token, in-place decode
    with default geometry,
  - wrote `state_dst` inside the scalar update loop and skipped the final
    post-token register-store loop for that instantiation.
- Build artifact:
  `/home/mudler/llama-phase92-gdn-store-fused/build`.
- Guardrail and gate artifacts:
  - canonical gates:
    `/home/mudler/bench/phase92_gdn_scalar_store_fused/20260701_204550/canonical_gates`,
  - decode-only profile:
    `/home/mudler/bench/phase92_gdn_scalar_store_fused/20260701_204718/decode_profile`.

Safety gates:

| check | result |
|-------|--------|
| local build | `cmake --build build --target test-backend-ops -j $(nproc)` OK |
| local CPU guardrail | `GATED_DELTA_NET_INPLACE_IDS` `6/6`, `Backend CPU: OK` |
| DGX CUDA guardrail, `GDN_SCALAR_DECODE_STORE_FUSED=1` | `6/6`, `Backend CUDA0: OK` |
| canonical MoE md5 | `8cb0ce23777bf55f92f63d0292c756b0` |
| canonical dense md5 | `5951a5b4d624ce891e22ab5fca9bc439` |
| canonical `GATED_DELTA_NET` | `46/46`, `Backend CUDA0: OK` |
| canonical `MUL_MAT` | `1146/1146`, `Backend CUDA0: OK` |
| canonical `MUL_MAT_ID` | `806/806`, `Backend CUDA0: OK` |
| profile pre/post md5/op gates | all OK |

Decode-only profile, MoE `N=128`, `N_PREDICT=2048`, capture after median
depth `72 -> 94`, `PROFILE_ENV=GDN_SCALAR_DECODE_STORE_FUSED=1`:

| arm | total kernel s | GDN ms | GDN % | `gdn_core` ms | `gdn_core` launches | `mmq_nvfp4` ms |
|-----|---------------:|-------:|------:|--------------:|--------------------:|---------------:|
| Phase87 same-source default | `3.6310` | `1471.27` | `40.52%` | `1390.56` | `598` | `1416.46` |
| Phase91 pack2 PDL-fix | `3.5813` | `1505.91` | `42.05%` | `1425.44` | `598` | `1333.39` |
| Phase92 store-fused | `3.7419` | `1609.81` | `43.02%` | `1529.72` | `600` | `1383.82` |

Decision:

- Reject and revert the store-fused runtime patch. It is inference-safe under
  the current md5/op gates, but it worsens the target `gdn_core` bucket by
  `+139.16 ms` vs Phase87 same-source default and `+104.28 ms` vs the already
  rejected Phase91 pack2 trial.
- The extra in-loop global stores likely increase pressure/ordering cost enough
  to outweigh removing the final register pass. Do not retry this shape unless
  a profile shows the final store loop as independently dominant.
- Next higher-value direction from the vLLM code audit is not another
  recurrence micro-loop tweak; scope the larger packed decode contract or the
  Qwen3Next GQA-repeat removal as separate, guarded phases.

### Phase91: Default-off PACK=2 Decode Kernel, Guarded Retry

- Date: 2026-07-01.
- Source: `/home/mudler/llama-phase91-gdn-pack2-guarded-source`, default-off
  CUDA experiment on top of the Phase90 guardrail stack.
- Local patch status: runtime CUDA changes reverted after profiling; Phase90
  test guardrail remains.
- Patch scope:
  - reintroduced a `GDN_DECODE_PACK2=1` F32 scalar-gate, one-token,
    in-place decode kernel that packs two sequences into one CTA,
  - added a PDL-safety fix after the first canonical md5 failure: inactive
    odd/single sequence lanes now call `ggml_cuda_pdl_sync()` before returning,
  - extended the guardrail with F32 `n_seqs=1` and `n_seqs=3`
    output-plus-state cases.
- Build artifact:
  `/home/mudler/llama-phase91-gdn-pack2-guarded-source/build`.
- Guardrail artifacts:
  - initial `n_seqs=2` guardrail pass:
    `/home/mudler/bench/phase91_gdn_pack2_guarded/20260701_201943/guardrail`,
  - initial canonical md5 failure:
    `/home/mudler/bench/phase91_gdn_pack2_guarded/20260701_202024/canonical_gates`,
  - PDL-fix expanded guardrail pass:
    `/home/mudler/bench/phase91_gdn_pack2_guarded/20260701_202140/guardrail_pdl_fix`,
  - PDL-fix canonical gates with `GATED_DELTA_NET,MUL_MAT,MUL_MAT_ID`:
    `/home/mudler/bench/phase91_gdn_pack2_guarded/20260701_202154/canonical_gates_pdl_fix`,
  - decode-only profile:
    `/home/mudler/bench/phase91_gdn_pack2_guarded/20260701_202425/decode_profile_pdl_fix`.

Safety gates:

| check | result |
|-------|--------|
| initial Phase90 guardrail, `GDN_DECODE_PACK2=1` | `4/4`, `Backend CUDA0: OK` |
| initial canonical MoE md5 | failed: `b93724e88460d90379c5009df0e1f2b6` vs `8cb0ce23777bf55f92f63d0292c756b0` |
| expanded guardrail after PDL fix | `6/6`, covers F32 `n_seqs=1,2,3` output-plus-state |
| PDL-fix MoE md5 | `8cb0ce23777bf55f92f63d0292c756b0` |
| PDL-fix dense md5 | `5951a5b4d624ce891e22ab5fca9bc439` |
| PDL-fix `GATED_DELTA_NET` | `46/46`, `Backend CUDA0: OK` |
| PDL-fix `MUL_MAT` | `1146/1146`, `Backend CUDA0: OK` |
| PDL-fix `MUL_MAT_ID` | `806/806`, `Backend CUDA0: OK` |

Decode-only profile, MoE `N=128`, `N_PREDICT=2048`, capture after
median depth `66 -> 88`, `PROFILE_ENV=GDN_DECODE_PACK2=1`:

| arm | total kernel s | GDN ms | GDN % | `gdn_core` ms | `gdn_core` launches | `mmq_nvfp4` ms |
|-----|---------------:|-------:|------:|--------------:|--------------------:|---------------:|
| Phase87 same-source default | `3.6310` | `1471.27` | `40.52%` | `1390.56` | `598` | `1416.46` |
| Phase85 identity state | `3.6622` | `1480.21` | `40.42%` | `1400.34` | `596` | `1437.53` |
| Phase91 pack2 PDL-fix | `3.5813` | `1505.91` | `42.05%` | `1425.44` | `598` | `1333.39` |

Decision:

- Reject and revert the pack2 runtime patch. It is inference-safe after the PDL
  fix, but it worsens the target `gdn_core` bucket by `+34.88 ms` vs the
  Phase87 same-source default and `+25.10 ms` vs Phase85.
- Keep the expanded Phase90/91 `GATED_DELTA_NET_INPLACE_IDS` guardrail cases
  because they caught the missing odd/single sequence coverage.
- Do not retry CTA-level sequence packing without a different per-sequence work
  reduction; packing alone raises GDN's share of total kernel time.

### Phase90: In-place GDN Decode State Guardrail

- Date: 2026-07-01.
- Source: `/home/mudler/llama-phase90-gdn-inplace-ids-guardrail-source`,
  test-only experiment on top of the current Phase85 carry-forward stack.
- Local patch status: kept as a guardrail candidate in
  `tests/test-backend-ops.cpp`.
- Patch scope:
  - fixes the in-place ids fixture initialization by mirroring the identity
    source cache bytes into `state_dst` after random tensor initialization,
  - adds F32 serving-shape cases: `head_count=4`, `head_size=128`,
    `n_seqs=2`, scalar gate and KDA,
  - makes those F32 cases return `concat(flatten(out), flatten(state_dst))`,
    so the normal backend comparator validates both attention output and the
    recurrent-state side effect.
- Build artifact:
  `/home/mudler/llama-phase90-gdn-inplace-ids-guardrail-source/build`.
- Gate artifacts:
  - stale-source assertion:
    `/home/mudler/bench/phase90_gdn_inplace_ids_guardrail/20260701_200946/direct`,
  - output-only corrected pass:
    `/home/mudler/bench/phase90_gdn_inplace_ids_guardrail/20260701_201058/direct`,
  - output-plus-state corrected pass:
    `/home/mudler/bench/phase90_gdn_inplace_ids_guardrail/20260701_201257/direct`.

DGX verification:

| check | result |
|-------|--------|
| local build | `cmake --build build --target test-backend-ops -j $(nproc)` completed |
| local CPU selected op | `4/4`, including F32 `check_state=1` cases |
| DGX CUDA selected op, stale source | failed before comparison on BF16 `state_dst` F32-only assert |
| DGX CUDA selected op, corrected output-only source | `4/4`, `Backend CUDA0: OK` |
| DGX CUDA selected op, output plus state | `4/4`, `Backend CUDA0: OK` |

Decision:

- Keep this as the minimum guardrail for the next packed decode attempt. It
  covers the Phase88 target shape (`S_v=128`, one-token decode, two sequences)
  and observes the side-effect `state_dst` update for F32 scalar-gate and KDA
  cases.
- BF16 in-place ids cases remain output-only in this fixture; use canonical md5
  gates for full-model BF16 inference safety.
- Do not profile Phase90: it is a test harness/guardrail attempt, not a runtime
  performance candidate.

### Phase89: In-place GDN Decode Test Guardrail Attempt

- Date: 2026-07-01.
- Source: `/home/mudler/llama-phase89-gdn-decode-gate-source`, test-only
  experiment on top of the reverted Phase88 source.
- Local patch status: reverted after the targeted test filter failed.
- Patch scope:
  - temporarily added two `test_gated_delta_net_inplace_ids` cases in
    `tests/test-backend-ops.cpp`:
    - F32, `head_count=4`, `head_size=128`, `n_seqs=2`, scalar gate,
    - F32, `head_count=4`, `head_size=128`, `n_seqs=2`, KDA.
- Build artifact:
  `/home/mudler/llama-phase89-gdn-decode-gate-source/build-cuda`.
- Build logs:
  - `/home/mudler/llama-phase89-gdn-decode-gate-source/configure.phase89.log`
  - `/home/mudler/llama-phase89-gdn-decode-gate-source/build.phase89.log`
- Gate artifact:
  `/home/mudler/bench/phase89_gdn_decode_gate/20260701_175903/direct`.

DGX verification:

| check | result |
|-------|--------|
| local build | `cmake --build build --target test-backend-ops -j 8` completed |
| local run | local CPU backend skipped for this op set |
| CUDA `GATED_DELTA_NET` filter | `46/46`, `Backend CUDA0: OK` |
| CUDA `GATED_DELTA_NET_INPLACE_IDS` filter | failed `0/4`, including both newly added F32 cases and the two pre-existing BF16 cases |

Decision:

- Reject and revert the test-only change. The direct
  `GATED_DELTA_NET_INPLACE_IDS` filter is not currently a reliable green
  guardrail, because the existing BF16 cases fail when selected directly.
- Do not add more packed decode source until there is a focused harness for the
  serving decode shape that compares both attention output and the side-effect
  `state_dst` update against the existing sequential kernel.

### Phase88: Default-off PACK=2 Decode CTA Kernel

- Date: 2026-07-01.
- Source: `/home/mudler/llama-phase88-gdn-pack2-source`, one-file CUDA
  experiment on top of Phase85.
- Local patch status: reverted after md5 failure.
- Patch scope:
  - added `gated_delta_net_decode_pack2_cuda` in
    `ggml/src/ggml-cuda/gated_delta_net.cu`,
  - gated it behind `GDN_DECODE_PACK2=1`,
  - limited it to F32 state, scalar-gate, `S_v == 128`, `n_tokens == 1`,
    in-place decode, with no `GDN_NW/GDN_CPW` override,
  - attempted to preserve the existing `(16,8)` per-column math order while
    packing two independent sequences into one CTA.
- Build artifact:
  `/home/mudler/llama-phase88-gdn-pack2-source/build-cuda`.
- Build logs:
  - `/home/mudler/llama-phase88-gdn-pack2-source/configure.phase88.log`
  - `/home/mudler/llama-phase88-gdn-pack2-source/build.phase88.log`
- Gate artifact:
  `/home/mudler/bench/phase88_gdn_pack2_gates/20260701_175059/direct`.
- Profile artifact: none. Profiling was skipped because the md5 gate failed.

DGX gates with `GDN_DECODE_PACK2=1`:

| check | result |
|-------|--------|
| MoE md5 | failed, got `320b5ed679844cbfd6f18d85d7ae32b0`, expected `8cb0ce23777bf55f92f63d0292c756b0` |
| dense md5 | failed, got `6a65e9d9e47321ebce9e461c8abf036c`, expected `5951a5b4d624ce891e22ab5fca9bc439` |
| `GATED_DELTA_NET` | `Backend CUDA0: OK` |
| `MUL_MAT` | `Backend CUDA0: OK` |
| `MUL_MAT_ID` | `Backend CUDA0: OK` |

Observed output symptom:

- MoE output duplicated the opening `<think>` marker.
- Dense output degenerated into repeated `/` characters immediately after the
  opening `<think>` marker.

Decision:

- Reject and revert. The sacred greedy md5 gate failed, so no profile was run.
- The existing `test-backend-ops -o GATED_DELTA_NET` set did not catch this
  because it does not cover the exact serving decode shape that triggers the
  pack2 path. Before another packed decode attempt, add or script a focused
  `n_seq_tokens=1`, `n_seqs > 1`, in-place F32 state equivalence gate against
  the existing sequential kernel.
- Do not carry the pack2 kernel in the patch stack.

### Phase87: Decode Geometry Probe `(GDN_NW=4, GDN_CPW=8)`

- Date: 2026-07-01.
- Source: `/home/mudler/llama-phase87-gdn-4x8-source`, one-line CUDA
  dispatcher experiment on top of Phase85:
  expose `launch_gdn_variant<128, ..., NUM_WARPS=4, COLS_PER_WARP=8>` through
  the existing `GDN_NW/GDN_CPW` env sweep.
- Local patch status: reverted after profiling. The attempt was env-gated and
  never made default.
- Build artifact:
  `/home/mudler/llama-phase87-gdn-4x8-source/build-cuda`.
- Build logs:
  - `/home/mudler/llama-phase87-gdn-4x8-source/configure.phase87.log`
  - `/home/mudler/llama-phase87-gdn-4x8-source/build.phase87.log`
- Gate artifact:
  `/home/mudler/bench/phase87_gdn_4x8_gates/20260701_174014/direct`.
- Profile artifact:
  `/home/mudler/bench/phase87_gdn_4x8_profile/20260701_174310`.
- Result type: source geometry probe. The hypothesis was that a `4*8 = 32`
  column tile would be closer to vLLM's `BV=32` decode program shape while
  preserving the existing per-column reduction order.

DGX gates with `GDN_NW=4 GDN_CPW=8`:

| check | result |
|-------|--------|
| MoE md5 | `8cb0ce23777bf55f92f63d0292c756b0` |
| dense md5 | `5951a5b4d624ce891e22ab5fca9bc439` |
| `GATED_DELTA_NET` | `Backend CUDA0: OK` |
| `MUL_MAT` | `Backend CUDA0: OK` |
| `MUL_MAT_ID` | `Backend CUDA0: OK` |

Same-source decode-only profile:

| arm | source | env | active slots | depth start | depth mid | total kernel s | GDN ms | GDN share | `gdn_core` ms | `gdn_core` launches | `mmq_nvfp4` ms |
|-----|--------|-----|-------------:|------------:|----------:|---------------:|-------:|----------:|--------------:|--------------------:|---------------:|
| default geometry | `/home/mudler/llama-phase87-gdn-4x8-source` | default `(16,8)` | `128` | `74` | `96` | `3.6310` | `1471.27` | `40.52%` | `1390.56` | `598` | `1416.46` |
| Phase87 4x8 | `/home/mudler/llama-phase87-gdn-4x8-source` | `GDN_NW=4 GDN_CPW=8` | `128` | `71` | `92` | `3.5988` | `1493.66` | `41.50%` | `1417.13` | `569` | `1396.11` |

Decision:

- Reject. The target bucket regressed by `+26.57 ms` (`+1.91%`) despite lower
  total kernel time from unrelated `mmq_nvfp4` variance.
- Reverted the one-line dispatcher addition. Do not carry this in the patch
  stack.
- The subagent/code audit points to a different Phase88 shape: keep the current
  `(16,8)` per-column math order and pack two independent sequences per CTA, or
  implement a fuller vLLM-style packed decode kernel that fuses producer math
  and recurrence.

### Phase86: Producer-fusion Scope Audit

- Date: 2026-07-01.
- Source: no source patch. This is a profile-backed scope rejection using the
  Phase85 node-traced DGX artifact before spending code on a small-ceiling
  fusion.
- Input profile artifact:
  `/home/mudler/bench/phase85_gdn_identity_state_profile/20260701_171856`.
- Source audit:
  - `ggml/src/ggml-cuda/ggml-cuda.cu` already fuses
    `{ GGML_OP_UNARY, GGML_OP_MUL }` for `SILU`, `SIGMOID`, and `SOFTPLUS`,
    covering the expensive part of `alpha_softplus * ssm_a`.
  - Qwen35 and Qwen35MoE still compute beta sigmoid and the alpha bias/softplus
    producer as separate graph pieces, but those pieces are small in the
    decode-only trace.
  - vLLM's Triton producer fusion remains a useful design reference, but its
    isolated producer scope is not the main GB10 bottleneck in this llama.cpp
    profile.
- Gate artifact: not applicable, no binary changed.
- Result type: no-code benchmark/scope attempt. The benchmark record below is
  copied from the Phase85 candidate profile because Phase86 deliberately asks
  whether a source patch is worth writing.

Same-window profile evidence:

| bucket | time | share | launches | interpretation |
|--------|-----:|------:|---------:|----------------|
| total kernel time | `3.6622 s` | `100.00%` | - | Phase85 identity-state candidate capture |
| `GDN` macro | `1480.21 ms` | `40.42%` | `2980` | target family remains dominant |
| `gdn_core` | `1400.34 ms` | `38.24%` | `596` | real parity lever must reduce this bucket |
| `act/GDN-gate(shared)` macro | `13.57 ms` | `0.37%` | `3771` | entire producer/gate-side ceiling is tiny |
| `gated_act_silu_sigmoid` | `10.84 ms` | `0.30%` | `1786` | already includes fused unary-gated kernels |
| `gdn_sigmoid` | `2.73 ms` | `0.07%` | `1985` | beta sigmoid ceiling |
| `unary_op_kernel<&op_softplus>` | about `1.08 ms` | about `0.03%` | `596` | alpha softplus standalone signal from `nsys stats` |

Decision:

- Reject a narrow Phase86 producer-only implementation. Even deleting the whole
  `act/GDN-gate(shared)` macro would improve the captured total by only
  `0.37%`, and deleting only the still-unfused beta sigmoid would be about
  `0.07%`.
- Do not modify or gate source for this phase. It would add upstream conflict
  surface without meaningful parity upside.
- Phase87 should target a packed decode GDN kernel, inspired by vLLM's decode
  path, that reduces launches and memory traffic inside `gdn_core` itself while
  preserving the default F32 recurrent S-cache and md5/op gates.

### Phase85: Identity-contiguous GDN State Fast Path

- Date: 2026-07-01.
- Source: `/home/mudler/llama-phase85-gdn-identity-state-source`, local
  eight-file experiment on top of fork commit
  `237ad9b96 feat(cuda): add BF16 Qwen GDN state cache`.
- Local patch scope:
  - carry forward Phase84 attention-only in-place GDN output cleanup,
  - add a side-effect-free `llama_memory_recurrent_context::s_copy_main_is_identity`,
  - store that identity bit in `llm_graph_input_rs`,
  - include it in base and hybrid graph reuse checks,
  - call `ggml_gated_delta_net_inplace` on a direct state view when active
    recurrent rows are identity-contiguous, otherwise keep the ids path.
- Build artifact:
  `/home/mudler/llama-phase85-gdn-identity-state-source/build-cuda`.
- Build logs:
  - `/home/mudler/llama-phase85-gdn-identity-state-source/configure.phase85.log`
  - `/home/mudler/llama-phase85-gdn-identity-state-source/build.phase85.log`
- Gate artifact:
  `/home/mudler/bench/phase85_gdn_identity_state_gates/20260701_171733/direct`.
- Profile artifact:
  `/home/mudler/bench/phase85_gdn_identity_state_profile/20260701_171856`.
- Result type: source cleanup / small performance experiment. This reuses the
  existing F32 recurrent-state CUDA kernel and changes only the source-state
  view used for identity-contiguous decode windows. It avoids the ids scratch
  allocation and no-op `gdn_gather_nonident_kernel` launch in that graph shape.

Local verification:

| check | result |
|-------|--------|
| local build | `cmake --build build --target test-backend-ops llama-server -j 8` completed |
| local note | `llama-server` build used the UI archive fallback after local npm engine warning; target completed |

DGX gates:

| check | result |
|-------|--------|
| MoE md5 | `8cb0ce23777bf55f92f63d0292c756b0` |
| dense md5 | `5951a5b4d624ce891e22ab5fca9bc439` |
| `GATED_DELTA_NET` | `46/46`, `Backend CUDA0: OK` |
| `MUL_MAT` | `1146/1146`, `Backend CUDA0: OK` |
| `MUL_MAT_ID` | `806/806`, `Backend CUDA0: OK` |

Same-window decode-only profile:

| arm | source | active slots | depth start | depth mid | total kernel s | GDN ms | GDN share | `gdn_core` ms | `gdn_core` launches | `gdn_gather` ms | GDN macro launches | `mmq_nvfp4` ms |
|-----|--------|-------------:|------------:|----------:|---------------:|-------:|----------:|--------------:|--------------------:|----------------:|------------------:|---------------:|
| baseline F32 | `/home/mudler/llama-phase81-bf16-state-source` | `128` | `73` | `95` | `3.7081` | `1493.78` | `40.28%` | `1412.33` | `600` | `0.89` | `3600` | `1473.60` |
| Phase85 identity state | `/home/mudler/llama-phase85-gdn-identity-state-source` | `128` | `72` | `94` | `3.6622` | `1480.21` | `40.42%` | `1400.34` | `596` | not present | `2980` | `1437.53` |

Server log signal:

| arm | CUDA free memory at startup | graph reuse |
|-----|----------------------------:|------------:|
| baseline F32 | `116418 MiB` | `105/122 = 86.1%` |
| Phase85 identity state | `117857 MiB` | `105/123 = 85.4%` |

Decision:

- Carry forward only as a small cleanup candidate. The patch is md5/op green,
  removes the explicit `gdn_gather` bucket, and reduces GDN macro launches.
- Do not treat it as a parity-closing speed lever: direct removed work was only
  `0.89 ms` over the capture, and `gdn_core` improved by only `0.85%`
  (`1412.33 -> 1400.34 ms`) in a noisy same-window run.
- Keep the next speed-focused scope on either producer fusion
  (`alpha softplus * A`, beta sigmoid) or a larger packed decode kernel. The
  remaining GDN gap is not explained by ids gather overhead.

### Phase84: Attention-only Outputs for In-place GDN

- Date: 2026-07-01.
- Source: `/home/mudler/llama-phase84-attn-only-source`, local three-file
  experiment on top of fork commit
  `237ad9b96 feat(cuda): add BF16 Qwen GDN state cache`.
- Local patch files:
  - `ggml/src/ggml.c`
  - `ggml/src/ggml-cpu/ggml-cpu.c`
  - `ggml/src/ggml-cpu/ops.cpp`
- Build artifact: `/home/mudler/llama-phase84-attn-only-source/build-cuda`.
- Build logs:
  - `/home/mudler/llama-phase84-attn-only-source/configure.phase84.log`
  - `/home/mudler/llama-phase84-attn-only-source/build.phase84.log`
- Gate artifact:
  `/home/mudler/bench/phase84_attn_only_gates/20260701_165952/direct`.
- Profile artifact:
  `/home/mudler/bench/phase84_attn_only_profile/20260701_170131`.
- Result type: source cleanup / memory experiment. `ggml_gated_delta_net_inplace`
  and `ggml_gated_delta_net_inplace_ids` now allocate only the attention-score
  output tensor because final recurrent state is written as a side effect into
  `state_dst`. The CPU `inplace_ids` non-identity fallback was moved from the
  old unused output tail to explicit workspace so CPU/CUDA semantics remain
  aligned.

Local verification:

| check | result |
|-------|--------|
| local build | `cmake --build build --target test-backend-ops -j 8` completed |
| local GDN subset | no non-CPU backend locally, so CPU was skipped by `test-backend-ops` |

DGX gates:

| check | result |
|-------|--------|
| MoE md5 | `8cb0ce23777bf55f92f63d0292c756b0` |
| dense md5 | `5951a5b4d624ce891e22ab5fca9bc439` |
| `GATED_DELTA_NET` | `46/46`, `Backend CUDA0: OK` |
| `MUL_MAT` | `1146/1146`, `Backend CUDA0: OK` |
| `MUL_MAT_ID` | `806/806`, `Backend CUDA0: OK` |

Same-window decode-only profile:

| arm | source | active slots | depth start | depth mid | total kernel s | GDN ms | GDN share | `gdn_core` ms | `gdn_core` launches | `gdn_core`/launch | `mmq_nvfp4` ms |
|-----|--------|-------------:|------------:|----------:|---------------:|-------:|----------:|--------------:|--------------------:|------------------:|---------------:|
| baseline F32 | `/home/mudler/llama-phase81-bf16-state-source` | `128` | `74` | `96` | `3.6464` | `1481.59` | `40.63%` | `1399.72` | `599` | `2.337 ms` | `1418.47` |
| Phase84 attention-only | `/home/mudler/llama-phase84-attn-only-source` | `128` | `65` | `87` | `3.5814` | `1489.33` | `41.59%` | `1407.38` | `598` | `2.354 ms` | `1349.11` |

Server log memory signal:

| arm | CUDA free memory at startup | graph reuse |
|-----|----------------------------:|------------:|
| baseline F32 | `117472 MiB` | `107/124 = 86.3%` |
| Phase84 attention-only | `117855 MiB` | `98/115 = 85.2%` |

Decision:

- Do not count Phase84 as a speed parity win. The target GDN bucket moved
  `1399.72 -> 1407.38 ms` (`+0.55%`), and the lower total kernel time is again
  explained by unrelated `mmq_nvfp4` variance (`1418.47 -> 1349.11 ms`).
- Keep as a possible memory-footprint cleanup only if upstream maintainability
  is acceptable: gates are green and the server startup memory signal improved
  by about `383 MiB` in the same profile window.
- Do not regenerate the LocalAI patch series until a follow-up decides whether
  this memory-only cleanup belongs in the fork commit stack.

### Phase83: KDA GDN exp-cache Decode Shortcut

- Date: 2026-07-01.
- Source: `/home/mudler/llama-phase83-kda-gexp-source`, local one-file CUDA
  experiment on top of fork commit
  `237ad9b96 feat(cuda): add BF16 Qwen GDN state cache`.
- Build artifact: `/home/mudler/llama-phase83-kda-gexp-source/build-cuda`.
- Build log:
  `/home/mudler/llama-phase83-kda-gexp-source/build.phase83.log`.
- Gate artifact:
  `/home/mudler/bench/phase83_kda_gexp_gates/20260701_184237/direct_retry`.
- Profile artifact:
  `/home/mudler/bench/phase83_kda_gexp_profile/20260701_164731`.
- Result type: source micro-optimization. Cache the KDA per-row
  `expf(g_t[i])` value in a register once per token/thread in
  `ggml/src/ggml-cuda/gated_delta_net.cu`, then reuse it in both the KDA
  `kv` and S-update loops. This preserves the same recurrence storage,
  operation order at the algorithm level, and F32 state path.

Gate harness notes:

- First copied-harness attempt used a LocalAI worktree path that was not present
  on DGX and failed before running gates.
- Second harness attempt refused to run because this job already owned the GPU
  lock.
- First direct gate script had an `awk` quoting bug after producing partial
  output.
- Corrected direct retry completed and is the valid gate artifact.

Gates:

| check | result |
|-------|--------|
| MoE md5 | `8cb0ce23777bf55f92f63d0292c756b0` |
| dense md5 | `5951a5b4d624ce891e22ab5fca9bc439` |
| `GATED_DELTA_NET` | `46/46`, `Backend CUDA0: OK` |
| `MUL_MAT` | `1146/1146`, `Backend CUDA0: OK` |
| `MUL_MAT_ID` | `806/806`, `Backend CUDA0: OK` |

Same-window decode-only profile:

| arm | source | active slots | depth start | depth mid | total kernel s | GDN ms | GDN share | `gdn_core` ms | `gdn_core` launches | `gdn_core`/launch | `mmq_nvfp4` ms |
|-----|--------|-------------:|------------:|----------:|---------------:|-------:|----------:|--------------:|--------------------:|------------------:|---------------:|
| baseline F32 | `/home/mudler/llama-phase81-bf16-state-source` | `128` | `73` | `95` | `3.6487` | `1481.06` | `40.59%` | `1399.46` | `597` | `2.344 ms` | `1424.65` |
| Phase83 exp-cache | `/home/mudler/llama-phase83-kda-gexp-source` | `128` | `66` | `88` | `3.5501` | `1487.71` | `41.91%` | `1405.62` | `600` | `2.343 ms` | `1317.98` |

Decision:

- Reject carry-forward. The target GDN bucket was flat-to-slightly worse:
  `gdn_core` changed `1399.46 -> 1405.62 ms` (`+0.44%`), while per-launch cost
  stayed effectively unchanged (`2.344 -> 2.343 ms`).
- The lower total kernel time is not credited to the shortcut because the
  unrelated `mmq_nvfp4` bucket dropped by `106.67 ms` in the candidate sample.
- Do not regenerate LocalAI patch-series output for this experiment. Next GDN
  work should target a structural traffic or launch-shape change, not
  single-expression reuse inside the current core loop.

### Phase82: BF16 Persistent GDN S-Cache f16 KL Gate

- Date: 2026-07-01.
- Source: `/home/mudler/llama-phase81-bf16-state-source`, fork commit
  `237ad9b96 feat(cuda): add BF16 Qwen GDN state cache`.
- Build artifact: `/home/mudler/llama-phase81-bf16-state-source/build-cuda`.
- KL artifact:
  `/home/mudler/bench/phase82_bf16_s_cache_f16_kl/20260701_183016`.
- Result type: full MoE f16-reference KL gate for the Phase81 default-off
  BF16 persistent GDN S-cache candidate.
- Reference base: `/home/mudler/bench/l4gate/klbase_moe.dat`, generated from
  `/home/mudler/work/darwin_36b_opus/f16.gguf` at `-c 512 -b 2048 --chunks 16`
  with f16 PPL `7.3760 +/- 0.29100`.
- Acceptance reference from `PAGED_BITEXACT_NOTE.md`: paged FP4-MMQ vs f16
  KLD `0.136000 +/- 0.003285`, PPL `7.4009`; non-paged FP4-MMQ vs f16 KLD
  `0.136597 +/- 0.003157`.
- Run note: the script metadata hash lines hit an `awk` quoting issue, so
  `BASE_SHA256` and `MODEL_SHA256_HEAD` are blank in `meta.txt`; both KL passes
  completed and produced full logs. Treat the blank hashes as harness metadata
  noise, not a model-output failure.

Result:

| arm | env | KLD vs f16 | PPL(Q) | PPL ratio vs f16 | same-top-p | max KLD |
|-----|-----|-----------:|-------:|-----------------:|-----------:|--------:|
| same-source F32 | `LLAMA_KV_PAGED=1 LLAMA_MOE_FORCE_GRAPHS=1` | `0.136563 +/- 0.003242` | `7.418401 +/- 0.296694` | `1.006105 +/- 0.008899` | `83.725 +/- 0.578%` | `3.602697` |
| BF16 S-cache | `LLAMA_QWEN35_GDN_S_CACHE_TYPE=bf16` plus same env | `0.137162 +/- 0.003456` | `7.321044 +/- 0.290693` | `0.992902 +/- 0.008714` | `84.240 +/- 0.571%` | `5.973692` |

Decision:

- Reject promotion of the BF16 persistent GDN S-cache patch.
- Do not run serving A/B for this candidate under the current rules: the hard
  lossy-path gate requires `KLD(new||f16) <= KLD(FP4-MMQ||f16)`, and the BF16
  S-cache mean KLD is above both the documented paged reference (`0.136000`) and
  the same-source F32 measurement (`0.136563`).
- Keep the Phase81 source only as a local experimental branch unless the gate is
  deliberately re-scoped. The next source attempt should preserve F32 recurrent
  S-cache quality or reduce traffic without changing the MoE f16 KL band.

### Phase81: Qwen35 BF16 Persistent GDN S-Cache

- Date: 2026-07-01.
- Source: `/home/mudler/llama-phase81-bf16-state-source`, local fork patch in
  `/home/mudler/_git/llama.cpp` branch `localai-paged`.
- Build artifact: `/home/mudler/llama-phase81-bf16-state-source/build-cuda`.
- Gate artifact:
  `/home/mudler/bench/phase81_bf16_s_cache_gates/20260701_161350`.
- Profile artifacts:
  - default F32:
    `/home/mudler/bench/phase81_bf16_s_cache_profile/default_20260701_162117`
  - BF16 S-cache:
    `/home/mudler/bench/phase81_bf16_s_cache_profile/bf16_20260701_162028`
- KL smoke artifact:
  `/home/mudler/bench/phase81_bf16_s_cache_kl/20260701_162322`.
- Result type: source experiment. `LLAMA_QWEN35_GDN_S_CACHE_TYPE=bf16`
  stores Qwen35/Qwen35MoE persistent recurrent S cache in BF16 while keeping GDN
  recurrence math, q/k/v/g/beta, and output in F32. Default remains F32.

Implementation scope:

- Added BF16 state support for `ggml_gated_delta_net_inplace_ids` only.
- Added CPU/CUDA BF16 state load/store conversion at the persistent cache
  boundary.
- Added BF16 CPU/CUDA `SCALE` support because recurrent cache zeroing uses
  `ggml_scale_inplace(..., 0)` on the S cache.
- Added tests for BF16 `GATED_DELTA_NET_INPLACE_IDS` and BF16 in-place `SCALE`.

Local verification:

| check | result |
|-------|--------|
| RED test before implementation | `ggml_gated_delta_net_inplace_ids` rejected BF16 state at `state->type == GGML_TYPE_F32` |
| CPU `SCALE -p bf16` | `1/1` passed |
| CPU `GATED_DELTA_NET_INPLACE_IDS` | `2/2` passed |
| DGX CUDA build | completed for `llama-completion`, `llama-batched-bench`, `test-backend-ops`, `llama-server`, later `llama-perplexity` |

Gates:

| mode | MoE md5 | dense md5 | `MUL_MAT` | `MUL_MAT_ID` |
|------|---------|-----------|-----------|--------------|
| default F32 | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `1146/1146` | `806/806` |
| BF16 S-cache | `07db32c2bcb78d17a43ed18bc22705cd` | `5951a5b4d624ce891e22ab5fca9bc439` | `1146/1146` | `806/806` |

Profile:

| arm | env | active slots | depth start | depth mid | total kernel s | GDN ms | GDN share | `gdn_core` ms | `gdn_core` launches | `gdn_core`/launch | `mmq_nvfp4` ms |
|-----|-----|-------------:|------------:|----------:|---------------:|-------:|----------:|--------------:|--------------------:|------------------:|---------------:|
| default F32 | none | `128` | `65` | `87` | `3.6157` | `1480.44` | `40.94%` | `1399.30` | `599` | `2.336 ms` | `1394.28` |
| BF16 S-cache | `LLAMA_QWEN35_GDN_S_CACHE_TYPE=bf16` | `128` | `65` | `91` | `3.5244` | `961.61` | `27.28%` | `863.57` | `720` | `1.199 ms` | `1665.38` |

KL smoke against same-source F32 base:

| check | result |
|-------|--------|
| shape | MoE, `-c 256 -b 256 --chunks 32`, Wikitext-2 raw |
| F32 floor KLD vs F32 base | `0.000000 +/- 0.000000`, same-top-p `99.975%` |
| BF16 S-cache KLD vs F32 base | `0.055499 +/- 0.001705`, same-top-p `88.361%` |
| BF16 PPL ratio vs F32 base | `1.010356 +/- 0.005817` |

Decision:

- Carry forward as a default-off candidate and run Phase82 full gates.
- Do not make it default-on: MoE greedy md5 is not canonical, and the KL smoke is
  not the full f16-reference acceptance gate.
- Required Phase82 before patch-series promotion:
  full f16-reference KL gate for MoE and dense, same-source serving A/B against
  F32 default and vLLM, then regenerate LocalAI patches from the fork only if
  serving and KL both hold.

### Phase80: GDN Identity-Ids Shortcut Source A/B

- Date: 2026-07-01.
- Artifact root:
  `/home/mudler/bench/phase80_gdn_identity_ids_ab/20260701_153927`.
- Arms:
  - `A_baseline`: `/home/mudler/llama-phase6-source`, default source
    `14fd69f1e feat(cuda): gate BF16 cuBLAS F32 output`.
  - `B_identity`: `/home/mudler/llama-phase80-gdn-identity-source`, one-file
    default-off source patch in `ggml/src/ggml-cuda/gated_delta_net.cu`,
    enabled with `GDN_ASSUME_IDENTITY_IDS=1`.
- Result type: source A/B of an identity-ids shortcut that skips the
  non-identity scratch gather for one-token final-state decode and reads the
  in-place state cache directly.
- Shape: same as Phase77 decode-only graph-node profile.
- Build: candidate CUDA build completed for `llama-completion`,
  `llama-batched-bench`, `test-backend-ops`, and `llama-server`.

Gates:

| arm | phase | MoE md5 | dense md5 | `MUL_MAT` | `MUL_MAT_ID` |
|-----|-------|---------|-----------|-----------|--------------|
| `A_baseline` | pre | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `1146/1146` | `806/806` |
| `A_baseline` | post | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `1146/1146` | `806/806` |
| `B_identity` | pre | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `1146/1146` | `806/806` |
| `B_identity` | post | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `1146/1146` | `806/806` |

Capture:

| arm | active slots | depth start | depth mid | `gdn_core` launches |
|-----|-------------:|------------:|----------:|--------------------:|
| `A_baseline` | `128` | `74` | `96` | `600` |
| `B_identity` | `128` | `65` | `87` | `600` |

Result:

| arm | env | total kernel s | GDN ms | GDN share | `gdn_core` ms | `gdn_gather` ms | GDN macro launches |
|-----|-----|---------------:|-------:|----------:|--------------:|----------------:|------------------:|
| `A_baseline` | none | `3.7132` | `1493.57` | `40.22%` | `1411.65` | `0.79` | `3600` |
| `B_identity` | `GDN_ASSUME_IDENTITY_IDS=1` | `3.5685` | `1489.96` | `41.75%` | `1409.28` | not present | `3000` |

Decision:

- Reject carry-forward/default for `GDN_ASSUME_IDENTITY_IDS=1`.
- The shortcut did remove the `gdn_gather` fine bucket and kept all gates
  green, but the removed bucket was only `0.79 ms` over the capture and
  `gdn_core` was effectively unchanged.
- The identity assumption is too narrow/risky for the size of the measured win.
  Do not spend more parity time on gather-only GDN shortcuts unless a future
  profile shows gather becoming material.
- Keep the next real GDN source scope on recurrent-state precision/traffic.

### Phase79: GDN Decode BV32 Source A/B

- Date: 2026-07-01.
- Artifact root:
  `/home/mudler/bench/phase79_gdn_decode_bv32_ab/20260701_152530`.
- Arms:
  - `A_baseline`: `/home/mudler/llama-phase6-source`, default source
    `14fd69f1e feat(cuda): gate BF16 cuBLAS F32 output`.
  - `B_bv32`: `/home/mudler/llama-phase79-gdn-source`, one-file default-off
    source patch in `ggml/src/ggml-cuda/gated_delta_net.cu`, enabled with
    `GDN_DECODE_BV32=1`.
- Result type: source A/B of a decode-only `S_v=128`, `n_tokens=1`,
  scalar-gate smaller-V-tile kernel inspired by vLLM's packed decode topology.
- Shape: same as Phase77 decode-only graph-node profile.
- Build: candidate CUDA build completed for `llama-completion`,
  `llama-batched-bench`, `test-backend-ops`, and `llama-server`.

Gate detail:

- Candidate default gates before profiling were green: MoE md5
  `8cb0ce23777bf55f92f63d0292c756b0`, dense md5
  `5951a5b4d624ce891e22ab5fca9bc439`, `MUL_MAT 1146/1146`,
  `MUL_MAT_ID 806/806`.
- Candidate opt-in gates before the A/B were green with `GDN_DECODE_BV32=1`:
  same md5 values, `MUL_MAT 1146/1146`, `MUL_MAT_ID 806/806`.
- A/B baseline pre-gates were green. Baseline post-gate first run hit a
  transient `MUL_MAT 1145/1146` failure on
  `MUL_MAT(type_a=q4_1,type_b=f32,m=16,n=1,k=256,...)`; immediate retry at
  `A_baseline/gate_post_retry` was green for md5, `MUL_MAT 1146/1146`, and
  `MUL_MAT_ID 806/806`.
- `B_bv32` pre/post gates were green with `GDN_DECODE_BV32=1`.

Capture:

| arm | active slots | depth start | depth mid | `gdn_core` launches |
|-----|-------------:|------------:|----------:|--------------------:|
| `A_baseline` | `128` | `67` | `89` | `600` |
| `B_bv32` | `128` | `72` | `93` | `570` |

Result:

| arm | env | total kernel s | GDN ms | GDN share | `gdn_core` ms | `gdn_core`/launch | `mmq_nvfp4` ms |
|-----|-----|---------------:|-------:|----------:|--------------:|------------------:|---------------:|
| `A_baseline` | none | `3.6274` | `1493.14` | `41.16%` | `1411.46` | `2.352` | `1392.60` |
| `B_bv32` | `GDN_DECODE_BV32=1` | `3.5739` | `1502.89` | `42.05%` | `1426.17` | `2.502` | `1363.65` |

Decision:

- Reject the BV32 decode source patch.
- Although all safety gates passed, normalized `gdn_core` worsened by about
  `6.4%` per launch and the GDN macro bucket increased.
- Lower total kernel time in the candidate is not accepted as a win because the
  capture contains fewer graph-node launches (`570` vs `600` `gdn_core`), while
  the per-launch GDN core cost is worse.
- Do not retry smaller V-tile decode topology without a new profile-level
  reason. The next GDN source hypothesis should attack recurrent-state
  precision/traffic or another structural difference from vLLM.

### Phase78: GDN Decode Launch-Shape Sweep

- Date: 2026-07-01.
- Baseline artifact:
  `/home/mudler/bench/phase77_moe_decode_only_profile/20260701_150134`.
- Sweep artifacts:
  - `/home/mudler/bench/phase78_gdn_launch_sweep/nw8_cpw8_20260701_150654`
  - `/home/mudler/bench/phase78_gdn_launch_sweep/nw16_cpw4_20260701_150954`
- Source baseline: `14fd69f1e feat(cuda): gate BF16 cuBLAS F32 output`.
- Result type: env-gated launch-shape sweep only; no source change.
- Shape: same as Phase77 decode-only graph-node profile.

Result:

| arm | env | gate status | GDN ms | GDN share | `gdn_core` ms | `gdn_core` share | `mmq_nvfp4` ms |
|-----|-----|-------------|-------:|----------:|--------------:|-----------------:|---------------:|
| Phase77 default | none | pre/post green | `1489.71` | `41.20%` | `1408.33` | `38.95%` | `1383.50` |
| sweep `8x8` | `GDN_NW=8 GDN_CPW=8` | pre/post green | `1525.86` | `41.94%` | `1443.55` | `39.68%` | `1366.33` |
| sweep `16x4` | `GDN_NW=16 GDN_CPW=4` | rejected | not run | not run | not run | not run | not run |

Gate detail:

- `8x8`: pre/post MoE md5 `8cb0ce23777bf55f92f63d0292c756b0`, dense md5
  `5951a5b4d624ce891e22ab5fca9bc439`, `MUL_MAT 1146/1146`,
  `MUL_MAT_ID 806/806`.
- `16x4`: completion md5 and `MUL_MAT 1146/1146` passed, but
  `MUL_MAT_ID` failed `805/806`; rejected before profiling.

Decision:

- Keep the current default `GDN_NW=16 GDN_CPW=8`.
- Do not spend more GB10 time on launch-shape retunes without a new hypothesis.
- The funded source path remains a structural default-off GDN decode A/B/PoC
  that reduces the Phase77 `gdn_core` bucket, not another existing-env sweep.

### Phase77: MoE Decode-Only Graph-Node Profile

- Date: 2026-07-01.
- Artifact:
  `/home/mudler/bench/phase77_moe_decode_only_profile/20260701_150134`.
- Setup-hiccup artifact:
  `/home/mudler/bench/phase77_moe_decode_only_profile/20260701_145815`.
- Source baseline: `14fd69f1e feat(cuda): gate BF16 cuBLAS F32 output`.
- Result type: current-stack llama.cpp decode-only graph-node profile; no
  source change.
- Shape: MoE `q36-35b-a3b-nvfp4`, `N=128`, long-running `/completion`
  requests, `N_PREDICT=2048`, capture after active decode.
- Capture window: active slots `128`; median decoded depth `67` at start and
  `89` mid-capture; `CAPTURE_SECONDS=4`.
- Profiler: `nsys launch --cuda-graph-trace=node`, bucketed with
  `/home/mudler/bench/bucket2.py`.

Gates:

| phase | MoE md5 | dense md5 | `MUL_MAT` | `MUL_MAT_ID` |
|-------|---------|-----------|-----------|--------------|
| pre | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `1146/1146` | `806/806` |
| post | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `1146/1146` | `806/806` |

Macro buckets:

| bucket | time ms | share | instances |
|--------|--------:|------:|----------:|
| GDN | `1489.71` | `41.20%` | `3600` |
| MoE/FFN-GEMM | `1400.77` | `38.74%` | `7220` |
| bf16/fp8-proj | `352.90` | `9.76%` | `7400` |
| layout-copy | `69.85` | `1.93%` | `10400` |
| act-quant | `67.63` | `1.87%` | `4820` |
| FA | `36.74` | `1.02%` | `600` |

Fine buckets:

| bucket | macro | time ms | share | instances |
|--------|-------|--------:|------:|----------:|
| `gdn_core` | GDN | `1408.33` | `38.95%` | `600` |
| `mmq_nvfp4` | MoE/FFN-GEMM | `1383.50` | `38.26%` | `4820` |
| `gdn_conv` | GDN | `71.76` | `1.98%` | `1200` |
| `gdn_l2norm` | GDN | `8.81` | `0.24%` | `1200` |
| `gdn_gather` | GDN | `0.80` | `0.02%` | `600` |

Decision:

- Phase77 confirms Phase76's GDN bucket is not only prompt/prefill
  contamination. In an isolated decode window, `gdn_core` is the largest fine
  bucket and is slightly larger than `mmq_nvfp4`.
- This supersedes the Phase75 no-GB10-GDN-source stance. The source-funded path
  is no longer C=64 prefill inverse work; it is a narrow default-off GDN decode
  A/B or standalone PoC based on the direct recurrent/packed decode structure
  found in vLLM.
- Acceptance gate for the next source attempt:
  reduce the Phase77 `gdn_core` bucket materially, keep pre/post md5 and
  `MUL_MAT`/`MUL_MAT_ID` green, and show no serving/decode throughput
  regression under the same decode-only capture shape.

### Phase76: Current MoE Serving Graph-Node Profile

- Date: 2026-07-01.
- Artifact:
  `/home/mudler/bench/phase76_current_moe_profile/20260701_145116`.
- Setup-hiccup artifacts:
  `/home/mudler/bench/phase76_current_moe_profile/20260701_144754` and
  `/home/mudler/bench/phase76_current_moe_profile/20260701_144929`.
- Source baseline: `14fd69f1e feat(cuda): gate BF16 cuBLAS F32 output`.
- Result type: current-stack llama.cpp graph-node serving profile; no source
  change.
- Shape: MoE `q36-35b-a3b-nvfp4`, `n=128`, `PTOK=128`, `GEN=64`,
  `PARALLEL=128`, `CTX=131072`, production defaults.
- Profiler: `nsys launch --cuda-graph-trace=node`, bucketed with
  `/home/mudler/bench/bucket2.py`.

Gates:

| phase | MoE md5 | dense md5 | `MUL_MAT` | `MUL_MAT_ID` |
|-------|---------|-----------|-----------|--------------|
| pre | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `1146/1146` | `806/806` |
| post | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `1146/1146` | `806/806` |

Serving result under graph-node profiling:

| n | agg_tps | decode_agg_tps | decode_perseq_tps | prefill_tps | ttft_mean_ms | wall_s |
|--:|--------:|---------------:|------------------:|------------:|-------------:|-------:|
| `128` | `204.1` | `320.7` | `2.06` | `1490.1` | `8365.1` | `40.146` |

Macro buckets:

| bucket | time ms | share | instances |
|--------|--------:|------:|----------:|
| GDN | `6669.16` | `32.88%` | `25980` |
| MoE/FFN-GEMM | `6264.88` | `30.88%` | `54406` |
| bf16/fp8-proj | `2772.38` | `13.67%` | `53880` |
| layout-copy | `1265.44` | `6.24%` | `81280` |
| ew-mul(weight/norm/GDN) | `734.61` | `3.62%` | `52464` |
| act-quant | `678.95` | `3.35%` | `37526` |
| FA | `264.50` | `1.30%` | `3660` |

Fine buckets:

| bucket | macro | time ms | share | instances |
|--------|-------|--------:|------:|----------:|
| `gdn_core` | GDN | `5876.94` | `28.97%` | `4680` |
| `gdn_conv` | GDN | `454.03` | `2.24%` | `7260` |
| `gdn_gather` | GDN | `237.87` | `1.17%` | `4680` |
| `gdn_l2norm` | GDN | `100.32` | `0.49%` | `9360` |
| `mmq_nvfp4` | MoE/FFN-GEMM | `6055.03` | `29.85%` | `34162` |

Decision:

- Phase76 contradicts the Phase75 assumption that GDN decode is not on the
  current critical path. Under graph-node current serving, GDN is the largest
  GPU-kernel macro bucket and `gdn_core` alone is nearly `29%`.
- Do not patch `gated_delta_net.cu` yet. This profile is llama-only and
  graph-node tracing depresses absolute throughput, so it is a source-funding
  signal, not a source patch gate.
- Fund Phase77 as a narrow proof before backend edits:
  compare current `gdn_core` against a vLLM-style direct recurrent/packed decode
  PoC or an in-backend default-off A/B, with pre/post md5 and op gates, and
  require a material reduction in the Phase76 `gdn_core` bucket without
  regressing serving throughput or canonical md5.

### Phase75: Post-PoC GDN/VLLM Audit

- Date: 2026-07-01.
- Artifact: no new benchmark artifact.
- Source baseline: `14fd69f1e feat(cuda): gate BF16 cuBLAS F32 output`.
- Result type: subagent codebase audit and gate-setting only; no source change.
- Inputs: Phase74 artifact
  `/home/mudler/bench/phase74_gdn_blocked_solve_poc/20260701_143711`,
  llama.cpp GDN implementation, vLLM FLA/GDN implementation, and parity docs.

Findings:

- llama.cpp already has the M5 tensor-core GDN path default-on under paged KV.
  It includes `KK/QK` mma, `KS/QS` 3xtf32 mma, `P*U` mma, explicit
  `T=A^-1`, `U=T*RHS`, and state carry `Kc^T*DU`.
- The current backend path is fixed at `C=16` for GB10 shared-memory limits.
  The remaining C=64/register-state class is not a shortcut patch.
- Phase74 tested a C=64 shared-memory explicit inverse-plus-apply scaffold and
  failed its source-work gate: inverse/direct speed was `0.5941x` weak decay
  and `0.5927x` mixed decay.
- vLLM has a structurally different one-token recurrent decode kernel that
  updates state directly without chunk inverse, and a packed decode path that
  avoids Q/K/V materialization copies. This is not currently source-funded in
  llama.cpp because prior parity profiles showed llama.cpp GDN decode faster
  than vLLM and decode serving dominated by host/MoE synchronization.
- vLLM's CuTeDSL GDN prefill path uses SM10x/CUDA-13 Blackwell features
  including TMA/tcgen05/CUTLASS DSL. Treat it as datacenter-Blackwell reference
  evidence unless GB10 support is proven in the local toolchain.

Decision:

- Do not start GB10 GDN backend source work after Phase74/75.
- Do not start a packed/recurrent GDN decode PoC unless a fresh same-session
  profile shows GDN decode or Q/K/V materialization back on the critical path.
- Phase75 acceptance gate for the next real parity attempt is a datacenter
  Blackwell serving rerun with the Phase72 shape:
  `NPL=8 32 128`, `PTOK=128`, `GEN=64`, `PARALLEL=128`, production defaults.
- The rerun is valid only if `hardware.txt` records
  `hardware_class=datacenter_blackwell`, pre/post md5 gates are green
  (`8cb0ce23777bf55f92f63d0292c756b0`,
  `5951a5b4d624ce891e22ab5fca9bc439`), `MUL_MAT 1146/1146` and
  `MUL_MAT_ID 806/806` are green, and decode profiles include
  `nsys --cuda-graph-trace=node`.
- If datacenter Blackwell materially lifts llama/vLLM decode ratios above the
  GB10 Phase72 record (`0.7561`, `0.7158`, `0.6935`), continue parity work on
  that surface. If not, record the residual gap as engine/kernel architecture
  rather than GB10 memory bandwidth and keep GB10 GDN stopped.

### Phase74: GDN Blocked-Solve PoC Gate

- Date: 2026-07-01.
- Plan:
  `docs/superpowers/plans/2026-07-01-gdn-blocked-solve-poc-phase74.md`.
- Artifact:
  `/home/mudler/bench/phase74_gdn_blocked_solve_poc/20260701_143711`.
- Source baseline: `14fd69f1e feat(cuda): gate BF16 cuBLAS F32 output`.
- Result type: standalone CUDA microbenchmark only; no llama.cpp source change.
- Toolchain: CUDA `13.0.88`, `nvcc -O3 -arch=sm_121a`.
- Hardware: NVIDIA GB10, `cc=12.1`, `48` SMs, `99 KB` dynamic shared memory.
- Shape: `C=64`, `DK=128`, `DV=128`, `chunks=4096`, `iters=1000`.
- Shared memory: direct solve/apply `81920` bytes; inverse-plus-apply
  `98304` bytes.

Result:

| case | direct ms | inverse+apply ms | inverse/direct speed | direct NMSE | inverse NMSE | direct max abs | inverse max abs | max lower row sum |
|------|----------:|-----------------:|---------------------:|------------:|-------------:|---------------:|----------------:|------------------:|
| weak decay | `3.263936` | `5.493515` | `0.5941x` | `2.081e-14` | `2.755e-15` | `8.890e-07` | `2.415e-07` | `4.072` |
| mixed decay | `3.275959` | `5.527584` | `0.5927x` | `1.981e-14` | `7.541e-16` | `8.115e-07` | `7.888e-08` | `1.635` |

Decision:

- Reject this explicit inverse-plus-apply shape as a backend source candidate on
  GB10. It is numerically clean but materially slower than direct solve/apply.
- Do not touch `ggml/src/ggml-cuda/gated_delta_net.cu` for the larger C=64 path
  based on this attempt.
- A future GDN source-work gate would need a substantially different
  tensor-core blocked solve/register-state design, not this shared-memory
  inverse scaffold.

### Phase73: Datacenter Blackwell Rerun Readiness

- Date: 2026-07-01.
- Plan:
  `docs/superpowers/plans/2026-07-01-datacenter-blackwell-rerun-readiness-phase73.md`.
- Artifact: no new benchmark artifact.
- Source baseline: `14fd69f1e feat(cuda): gate BF16 cuBLAS F32 output`.
- Result type: harness/spec audit only.

Evidence:

- Phase72 is the current GB10 serving baseline. Default llama decode/vLLM
  ratios remain `0.7561`, `0.7158`, and `0.6935` at `n=8/32/128`.
- Grouped-MMQ/W4A16: Phase61 direct activation was the last structurally
  distinct W4A16 shortcut; it failed its keep gate and stayed far behind
  default FP4-MMQ. Phase66 quantize plus gather was only `5.10%`, below the
  source-funding threshold.
- GDN: Phase71 kept shipped M5 as default. The remaining GDN gap is a larger
  FLA/CuteDSL-class C=64 blocked-solve/register-state implementation, not
  another C32/QS/global-Ai/local reorder.
- Harness: `paged-current-serving-snapshot.sh` already records
  `hardware_class=datacenter_blackwell` for B200/B100/GB200, supports
  `DRY_RUN=1`, `SERVED_MODEL_NAME`, and vLLM deployment overrides.

Decision:

- Do not start more GB10 grouped-MMQ/W4A16 source work.
- Do not start GDN backend source work until a standalone C=64 blocked-solve
  PoC records timing, numerical error, and resource estimates.
- The next parity run should be on datacenter Blackwell hardware with the
  existing same-session serving harness plus graph-node decode profiles.
- No parity claim is made by this phase.

### Phase72: TTFT Min32 Broader Serving

- Date: 2026-07-01.
- Plan: `docs/superpowers/plans/2026-07-01-ttft-min32-serving-phase72.md`.
- Artifact:
  `/home/mudler/bench/phase72_ttft_min32_serving/20260701_160730`.
- Source: `14fd69f1e feat(cuda): gate BF16 cuBLAS F32 output`.
- Shape: MoE serving, `NPL=8 32 128`, prompt `128`, generation `64`,
  `PARALLEL=128`, `CTX=131072`.
- Env gate: `LLAMA_TTFT_PREFILL_FIRST=1`
  `LLAMA_TTFT_PREFILL_FIRST_MIN_WAITING=32`.

Gates:

| gate | MoE md5 | dense md5 | `MUL_MAT` | `MUL_MAT_ID` |
|------|---------|-----------|-----------|--------------|
| pre default | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `1146/1146` | `806/806` |
| pre min32 | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | not run | not run |
| post default | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | not run | not run |
| post min32 | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | not run | not run |

Result:

- Reject default-on for min32 in the broader serving shape.
- Keep the scheduler knob opt-in only.
- min32 regressed aggregate, decode, TTFT, and wall time for every tested
  concurrency.

### Phase71: GDN Tensor-Core Revalidation

- Date: 2026-07-01.
- Plan: `docs/superpowers/plans/2026-07-01-gdn-tc-revalidation-phase71.md`.
- Artifact:
  `/home/mudler/bench/phase71_gdn_tc_revalidation/20260701_153425`.
- Source: `14fd69f1e feat(cuda): gate BF16 cuBLAS F32 output`.
- Shape: MoE prefill, `PP=512,2048`, `TG=4`, `B=32`, `CTX=131072`.

Canonical gates:

| gate | env | MoE md5 | dense md5 | `GATED_DELTA_NET` | `MUL_MAT` | `MUL_MAT_ID` |
|------|-----|---------|-----------|-------------------|-----------|--------------|
| default | none | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `46/46` | `1146/1146` | `806/806` |
| sequential-disabled | `GDN_CHUNK_MIN=2147483647` | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `46/46` | not run | not run |
| serial-chunked | `GDN_TC=0 GDN_CHUNK_MIN=64` | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `46/46` | not run | not run |
| forced M5 | `GDN_TC=4 GDN_CHUNK_MIN=64` | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `46/46` | not run | not run |

MoE prefill:

| arm | npp | S_PP t/s | T_PP s | S_TG t/s | total S t/s |
|-----|----:|---------:|-------:|---------:|------------:|
| default | `512` | `2313.57` | `7.082` | `401.82` | `2231.28` |
| sequential-disabled | `512` | `2198.28` | `7.453` | `392.50` | `2122.58` |
| serial-chunked | `512` | `1787.49` | `9.166` | `396.23` | `1740.12` |
| forced M5 | `512` | `2323.18` | `7.052` | `393.62` | `2238.13` |
| default | `2048` | `2422.88` | `27.049` | `389.91` | `2398.50` |
| sequential-disabled | `2048` | `2361.22` | `27.755` | `386.08` | `2337.91` |
| serial-chunked | `2048` | `1699.77` | `38.556` | `389.48` | `1688.69` |
| forced M5 | `2048` | `2420.52` | `27.075` | `388.72` | `2396.11` |

Ratios:

| npp | default/sequential S_PP | default/serial S_PP | forced/default S_PP |
|-----|------------------------:|---------------------:|--------------------:|
| `512` | `1.0524` | `1.2943` | `1.0042` |
| `2048` | `1.0261` | `1.4254` | `0.9990` |

Decision:

- Keep shipped GDN M5 default behavior.
- Do not reopen smaller GDN C32/QS/global-Ai32/kernel-reorder work on GB10.
- The stale "two-Gram PoC before M5 exists" framing is superseded by the
  existing `0047` M5 implementation and this revalidation.

### Phase70: BF16 F32 Output Broader Serving

- Date: 2026-07-01.
- Plan: `docs/superpowers/plans/2026-07-01-bf16-f32-output-broader-serving-phase70.md`.
- Artifact: `/home/mudler/bench/phase70_bf16_broader_serving/20260701_151500`.
- Source: `14fd69f1e feat(cuda): gate BF16 cuBLAS F32 output`.
- Shape: MoE serving, `NPL=8 32 128`, prompt `128`, generation `64`,
  `PARALLEL=128`, `CTX=131072`.

Gates:

| gate | MoE md5 | dense md5 | `MUL_MAT` | `MUL_MAT_ID` |
|------|---------|-----------|-----------|--------------|
| pre default | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `1146/1146` | `806/806` |
| pre opt-in | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `1146/1146` | not run |
| post default | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `1146/1146` | `806/806` |
| post opt-in | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `1146/1146` | not run |

Result:

- Default-on rejected.
- Opt-in remains correctness-clean, but broad serving is mixed-to-negative.

### Phase69: Patch Series Mirror Readiness

- Date: 2026-07-01.
- Plan: `docs/superpowers/plans/2026-07-01-patch-series-mirror-readiness-phase69.md`.
- Artifact: local dry-run only.
- Result: current `0001..0063` series matched Phase37 tree
  `dedb1182910eafe9f6875588dc8285bfb544cce5`; projected `0064..0073`
  matched fork HEAD tree `fcf5720b659c5e1e2b487ccf3c8f7289bb12b9c4`.
- Decision: patch regeneration is technically ready but blocked on explicit
  push approval by policy.

### Phase68: BF16 F32 Output Dense Serving

- Date: 2026-07-01.
- Plan: `docs/superpowers/plans/2026-07-01-bf16-f32-output-dense-serving-phase68.md`.
- Artifact: `/home/mudler/bench/phase68_bf16_dense_serving/20260701_145710`.
- Serving artifact:
  `/home/mudler/bench/phase68_bf16_dense_serving/20260701_145710/serving_ab_20260701_150249`.

Dense prefill:

| npp | default S_PP | opt-in S_PP | change |
|-----|-------------:|------------:|-------:|
| `512` | `973.13` | `975.52` | `+0.25%` |
| `2048` | `1019.88` | `1021.39` | `+0.15%` |

MoE serving `N=128`, prompt `128`, generation `128`:

| metric | default | opt-in | change |
|--------|--------:|-------:|-------:|
| `agg_tps` | `409.8` | `415.0` | `+1.27%` |
| `decode_agg_tps` | `615.3` | `627.2` | `+1.93%` |
| `prefill_tps` | `1630.2` | `1648.0` | `+1.09%` |
| `ttft_mean_ms` | `8574.7` | `8085.9` | `-5.70%` |
| `wall_s` | `39.978` | `39.480` | `-1.25%` |

Decision:

- Carry as default-off opt-in candidate pending broader serving evidence.

### Phase67: BF16 cuBLAS F32 Output

- Date: 2026-07-01.
- Plan: `docs/superpowers/plans/2026-07-01-bf16-cublas-f32-output-phase67.md`.
- Artifact: `/home/mudler/bench/phase67_bf16_f32_out/20260701_144909`.
- Fork commit: `ea0875d14 feat(cuda): gate BF16 cuBLAS F32 output`.
- DGX mirror commit: `14fd69f1e`.
- Env gate: `LLAMA_BF16_CUBLAS_F32_OUT=1`.

Gates:

| mode | MoE md5 | dense md5 | `MUL_MAT` |
|------|---------|-----------|-----------|
| default | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `1146/1146` |
| opt-in | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `1146/1146` |

MoE prefill:

| npp | default S_PP | opt-in S_PP | change |
|-----|-------------:|------------:|-------:|
| `512` | `2347.41` | `2402.34` | `+2.34%` |
| `2048` | `2440.18` | `2456.54` | `+0.67%` |

Decision:

- Keep default-off pending dense and serving A/B.
