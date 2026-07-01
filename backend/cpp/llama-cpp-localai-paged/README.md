# LocalAI paged-attention llama.cpp patch series

This backend vendors the patch series (in `patches/paged/`) that turns stock
llama.cpp into LocalAI's paged-attention variant (`llama-cpp-localai-paged`). The
patches are applied on top of a pinned upstream llama.cpp at build time; nothing
here is a fork - it is a source-only `*.patch` stack plus this canonical doc.

> One-file rule: this README is the canonical reference for the patch series. The
> only other docs are operational, kept in `docs/`, and linked below:
> - [`PAGED_BITEXACT_NOTE.md`](docs/PAGED_BITEXACT_NOTE.md) - the per-path bit-exactness gate (the canonical paged-MoE md5 reference).
> - [`LOCALAI_LLAMACPP_BACKEND_PLAN.md`](docs/LOCALAI_LLAMACPP_BACKEND_PLAN.md) - the design-of-record for shipping this as its own backend + the NVFP4 gallery items.
> - [`VLLM_PARITY_FINAL.md`](docs/VLLM_PARITY_FINAL.md) - the definitive, closed record of the GB10 vLLM-parity investigation: full benchmark, every lever + verdict, the structural floors, and the parity verdict (summarized in section 9 below). Read this before reopening any parity work.

---

## 1. What it is

`llama-cpp-localai-paged` is the LocalAI paged-attention llama.cpp backend: a
vendored patch series over upstream llama.cpp that adds

- a **paged KV cache** (vLLM-style block manager: on-demand fixed-size blocks,
  free pool, ref-counted blocks) with a **block-table flash-attention** read so
  the attention kernels index physical cells instead of a contiguous buffer;
- **cross-request prefix sharing** - concurrent requests that share a long
  prefix physically reuse one committed copy of the prefix blocks and prefill
  only their divergent suffix;
- a **decode-first prefill scheduler** - a dynamic per-step prefill-token budget
  decoupled from `n_batch`, so a long prefill never freezes co-batched decode;
- **GB10 / Blackwell NVFP4 decode optimizations** for the Qwen3.6 hybrid
  gated-DeltaNet (SSM) models, where the recurrent-state plumbing - not the FP4
  GEMM - dominates the decode step.

It is **pinned to llama.cpp `0ed235ea2c17a19fc8238668653946721ed136fd`** (kept == the stock `llama-cpp` backend's
pin) and advanced only by a manual, bit-exact-gated pin-sync process (see
section 7, "Pin + maintenance policy"), decoupled from the nightly auto-bumper. The pin must stay aligned with the stock pin because
`grpc-server.cpp` is shared; an earlier bump to `c299a92c` was bit-exact but broke
the grpc-server link and was reverted to the then-current stock pin.

The build gate is `LLAMA_PAGED` (default on in this tree); the paged engine is
enabled per-model at runtime via the gallery `options:` knobs (`paged_kv:true`,
`max_batch_tokens:`, `kv_unified:false`, ...). Against unpatched llama.cpp the
runtime hooks are inert, so a single `grpc-server.cpp` is shared between the
clean and the paged build.

---

## 2. Architecture

The decode step on these models breaks into three cost centers; the patch series
attacks each one.

**Paged KV manager + block-table flash-attn.** A host-side `PagedKVManager`
(`FreeBlockQueue` / `BlockPool` / chained-hash content cache) hands out
fixed-size KV blocks on demand and reclaims them per-sequence (ref-counted, with
copy-on-write for shared prefixes). The attention path reads through a **block
table** - an `I32 [n_view, n_stream]` position-ordered physical-cell index passed
as `src[5]` of `ggml_flash_attn_ext` - so the CUDA fattn vec/tile kernels and the
CPU reference map logical KV index `j` to physical cell `block_table[seq*ne11+j]`
and read K/V in place. Token-position ordering keeps the flash-attn online-softmax
reduction order identical to stock. A null block table is the stock contiguous
read, byte-identical.

**The gated-DeltaNet (GDN / SSM) decode path.** The Qwen3.6 hybrid models are 48
gated-DeltaNet (linear-attention / SSM) layers + 16 full-attention layers. On
GB10 the recurrent-state plumbing, not the weight GEMM, is the dominant decode
cost. The series fuses that plumbing to mirror vLLM's
`fused_recurrent_gated_delta_rule`: the recurrent state is read from and written
to its cache slot in place (no copy-back, no `get_rows` materialization), the
conv state is updated in place, the output projection is reshaped to route to the
tensor-core MMQ GEMM, and the recurrence kernel is occupancy-retuned - all
bit-exact (md5-gateable) against the f32 baseline.

**NVFP4 native FP4-MMA on Blackwell.** The NVFP4 dense/expert weight GEMM uses
Blackwell's native FP4-MMA. The series removes a redundant activation-requantize
in the MoE broadcast projections (bit-exact byte copy of identical blocks) and
keeps CUDA graphs on for the grouped-MMQ MoE decode step. These are the only
NVFP4-specific optimizations; on non-Blackwell hardware the FP4 path falls back
to dequant.

**The prefill/decode scheduler.** `update_slots()` already emits one unified
mixed prefill+decode batch per step. The scheduler patches change only the *count*
of prefill tokens admitted per step: decode tokens are claimed first
(decode-first), then a dynamic budget `max(n_ubatch, T - D)` (where `D` is the
live decode load and `T` is `LLAMA_MAX_BATCH_TOKENS`) admits prefill, auto-
shrinking as decode load rises. Pure scheduler policy, byte-identical when off,
orthogonal to the paged allocator.

---

## 3. Patch series (0001-0062)

Source-only patches, with intentional numbering gaps (e.g. 0005, 0027). The
decode-serving graph-reuse levers are 0040-0041. "Bit-exact" = greedy md5 /
`test-backend-ops` byte-identical to the relevant baseline; the gate methodology
is in section 5.

### Paged-KV core (0001-0012)

| # | What it does | Bit-exact |
|---|---|---|
| 0001 | Vendor the host-side paged KV block manager (`FreeBlockQueue`, `BlockPool`, `PagedKVManager`, chained-hash prefix cache). Pure C++17, nothing uses it yet. | n/a (no behavior) |
| 0002 | Place each sequence at permuted, non-contiguous block positions in `find_slot` (proves attention is invariant to physical KV placement). | yes (token-identical) |
| 0003 | Gather K/V/mask down to each stream's non-empty cells before `build_attn_mha`, position-sorted so the FA reduction order matches stock. | yes |
| 0004 | Drive paged placement through the vendored manager: blocks popped on demand, returned on seq end. Core kv-cache struct untouched. | yes (stock path byte-identical) |
| 0006 | Host-side cross-request prefix caching: hash prefix blocks, reuse matching physical blocks (ref-count++), COW-privatise before a divergent write. | yes (default off) |
| 0007 | Wire the prefix cache into the engine so a new sequence physically shares cached prefix blocks and skips recomputing the shared prefix. | yes (verified byte-identical) |
| 0008 | Wire cross-request prefix share into the llama-server continuous-batch loop so concurrent shared-prefix requests prefill only the suffix (36x fewer prefill tokens at K=32). | within CUDA batch-shape non-determinism band |
| 0009 | Replace the per-step gather with an **in-kernel paged read** (block table as `src[5]`); the K/V `get_rows` is gone. Decode step at batch32 691->696ms (was 1279ms gathered). | yes on CPU/batch1; GPU batch>1 within vec-vs-mma band |
| 0010 | Graft the block-table read into the tile kernel; add a dispatch guard so a present block table routes ONLY to vec/tile (never the mma/wmma kernels that ignore it). | yes (CPU byte-identical; vec route) |
| 0011 | Route the GQA-grouped F16 decode to the **tile kernel** (native head-group reuse) by default; vec for everything else. Paged decode to within 1.8% of stock. | vs stock-mma: different-kernel rounding; bit-exact vs vec |
| 0012 | Defensive `GGML_ASSERT(n_view % 64 == 0)` so a future pad/tile change can't silently reintroduce a past-end KV leak on the tile route. | yes (additive assert) |

### Decode-first scheduler (0013, 0016)

| # | What it does | Bit-exact |
|---|---|---|
| 0013 | `LLAMA_PREFILL_BUDGET`: a static per-step prefill-token budget decoupled from `n_batch` (vLLM `--max-num-batched-tokens` analogue). Flattens the decode ITL spike a long prefill inflicts (8.5x smaller worst freeze). | yes (off/short = byte-identical; == `-b` chunking) |
| 0016 | Supersede 0013 with a **dynamic decode-first** budget: `max(n_ubatch, T-D)`, auto-shrinking as decode load `D` rises. Policy-only inside `update_slots()`, zero libllama changes. | yes (default-off byte-identical) |

(0014/0015 are the MoE token-tile levers: 0014 adds `LLAMA_MOE_MMQ_X` (opt-in
high-batch decode micro-opt, +4.8% on Qwen3-Coder-30B), 0015 makes it a
default-on, density-aware auto-select that is prefill-safe by construction. Both
bit-exact. 0017 is the dense FP4-GEMM occupancy-tune track: bit-exact gate green,
but every cheap occupancy lever regressed on GB10, so nothing is enabled - it
ships as the parity gate + default-off instrumentation only.)

### Decode-serving graph reuse (0040, 0041)

These two close the **continuous-serving** decode gap (distinct from the static
batched-bench decode kernel, which is already at vLLM parity - see
[`docs/DECODE_SERVING_SCOPE.md`](docs/DECODE_SERVING_SCOPE.md)). In serving the
host rebuilt the ggml graph on **every** decode step (layer-A graph reuse was 0%),
so the GPU idled while the host rebuilt - the host-bound -39% the static bench
hides.

| # | What it does | Bit-exact |
|---|---|---|
| 0040 | **S1 paged decode-graph reuse** - the paged decode inputs (`input_block_table` / `input_gather_idxs`) never overrode `can_reuse` (defaults to false), so any graph carrying a paged input could never be reused. Add a correct `can_reuse` keyed on the (256-bucketed) block-table dims + a live-mctx refresh from the owning attn input. `LLAMA_PAGED_NO_GRAPH_REUSE=1` forces the pre-S1 path. | yes (md5 byte-identical reuse on/off; dense `5951a5b4`, paged-MoE `8cb0ce23`) |
| 0041 | **S3 decode-shape-stable scheduling** - keep co-batched prefill OUT of decode steps so the pure-decode batch shape stays reuse-stable (S1 makes a pure-decode step reusable; S3 makes the scheduler emit them). Pure `update_slots()` policy on top of 0016; prefill admitted on a bounded cadence (`LLAMA_PAGED_PREFILL_PERIOD`, default 8). **Default OFF** (opt-in via `LLAMA_PAGED_DECODE_STABLE=1`): a measured end-to-end A/B proved default-on is a serving mistake - deferring prefill admission on the period-8 cadence gives **2.5x worse TTFT** (60s vs 24s at N=256) and **20-29% lower end-to-end throughput**, with no end-to-end win at any concurrency; its apparent `decode_agg` gain was a metric artifact (faster per-step decode bought by starving prefill). Default prefers prompt prefill admission for good TTFT; opt in only for decode-dominated, low-arrival traffic where TTFT is not a concern. | yes (byte-identical on/off; per-stream independent in serving) |

Measured (GB10, MoE Qwen3.6-35B-A3B-NVFP4, 128-client staggered streaming load):
graph reuse **0% -> 72.2%**, host window `hostproc` **15.98 -> 6.31 ms/step**,
decode **4.05 -> 5.52 tok/s/seq median (4.24 -> 5.96 mean, at vLLM's ~5.9
sustained)**. S1 is necessary but **not** sufficient alone (13.8% reuse - prefill
co-batching churns the shape nearly every step); S3 is the multiplier of that
per-step decode metric. **But those are per-step decode numbers, not an end-to-end
serving win**: a later end-to-end A/B showed S3-default-on regresses real serving
(2.5x worse TTFT, 20-29% lower end-to-end throughput, no win at any concurrency),
because the period-8 cadence defers prefill admission. So **only S1 (0040) ships
default-on; S3 (0041) now defaults OFF and is opt-in** (`LLAMA_PAGED_DECODE_STABLE=1`,
for decode-dominated low-arrival traffic). The static batched-bench A/B isolates the S1
mechanism: paged decode reuse 0% -> 95.5% (throughput flat there, since the static
regime is GPU-bound). **S2 (double-buffer `set_inputs`) was dropped**: the Phase-0
profile put `set_inputs` at ~0.05 ms/step (the cost is the rebuild, not the input
copy), so it has nothing to recover. The remaining ~28% serving rebuilds are
request-boundary D/seq-set churn + the prefill-cadence steps. A **padded/fixed-slot
decode shape** to capture them was then implemented and GPU-tested (2026-06-28) and
**REJECTED** - it is bit-exact/inert but regresses serving throughput at every
concurrency, because this serving decode is GPU-compute-bound (baseline reuse 0% ~=
S1+S3 reuse 72% on aggregate tok/s), so the dummy-row compute it adds costs more
than the reuse it recovers. Full record + numbers in `docs/DECODE_SERVING_SCOPE.md`
("Padded-shape lever - rejected").

### Prefill fusions (0042, 0044)

CUDA-family graph fusions of the pre-norm residual chain and the gated-DeltaNet
output norm: separate `rms_norm` / `mul` / `add` / `silu` launches collapse into
one kernel so the intermediate never round-trips to HBM. Bit-exact (the fused
kernel reproduces the unfused FP order; float multiply is commutative). Each is
env-gated default-ON (`LLAMA_FUSE_*=0` for a clean single-build A/B that reverts
to the byte- and kernel-identical unfused path).

| # | What it does | Bit-exact / effect |
|---|---|---|
| 0042 | **Fused residual-add + RMS norm + weight multiply** (`rms_norm_pre_add_mul_f32`) - the pre-norm residual `h = x + sub_out; n = rms_norm(h) * w` ran as a `k_bin_bcast` ADD feeding the fused rms_norm+mul; the residual ADD has a second consumer (the skip add) so it can't pass the single-use `ggml_can_fuse`. Recognized via `ggml_can_fuse_subgraph` (ADD + final MUL both outputs), folded into one launch that publishes `h` and emits `scale * h * w`. Gate `LLAMA_FUSE_ADD_RMSNORM`. | yes (dense `5951a5b4`, MoE `8cb0ce23`); dense S_PP +0.5% |
| 0044 | **Fused gated RMSNorm + SiLU gate multiply** (`rms_norm_gate_mul_f32`) - the gated-DeltaNet output norm `(rms_norm(x) * w) * silu(z)` (qwen35 / qwen35moe `build_norm_gated`) ran as rms_norm_mul + silu_mul, two launches with the normalized intermediate crossing HBM. The gate z-projection (a MUL_MAT) is scheduled between the weight MUL and the SILU, so the chain is not naturally consecutive; `build_norm_gated` emits the gate multiply as `mul(silu(z), normalized)` (commutative, bit-exact) so the graph lays out the consecutive subgraph `{ SILU, RMS_NORM, MUL, MUL }` that `ggml_cuda_can_fuse` folds into one `scale * x * w * silu(z)` launch. Gate `LLAMA_FUSE_GATE_RMSNORM`. Profile (dense npp512): 672 (rms_norm_mul + silu_mul) -> 336 fused launches. | yes (dense `5951a5b4`, MoE `8cb0ce23`, paged + non-paged; `test-backend-ops` 12979/12979); S_PP dense +1.1% (~+10 us/tok), MoE +0.9% |

### SSM (gated-DeltaNet) decode levers (0018-0022, 0028)

These are the dominant decode levers on the Qwen3.6 hybrid models. All bit-exact.

| # | What it does | Effect (dense q36-27b / MoE q36-35b-a3b @npl128) |
|---|---|---|
| 0018 | **In-place SSM state write-back** - the recurrence writes its final state directly into the cache slot, removing the ~225MB/copy D2D memcpy (18.9% of decode time). | dense +23.5% / MoE +18.9% |
| 0019 | **Fused recurrent-state gather** - the op reads each sequence's prior state directly from `cache[ids[seq]]` (no `get_rows` materialization); race-free in-place + ids read. | dense +37.8% / MoE +35.3% |
| 0020 | **o_proj MMVQ->MMQ reshape** - collapse the GDN output to 2D so the output projection routes to the M=128 tensor-core MMQ GEMM (was a batch<=8 MMVQ GEMV). The single biggest decode-parity lever. | dense +31.7% (->85.9% of vLLM) / MoE +23.3% |
| 0021 | **Conv-state in-place fusion** - one `ggml_ssm_conv_update_inplace` op replaces the 4-op conv chain (transpose+concat+conv+silu+ring-cpy), writing the shifted ring state in place. | dense +3.2% / MoE +3.5% |
| 0022 | **GDN recurrence occupancy/coalescing retune** - column-folding (NUM_WARPS/COLS_PER_WARP) raises memory-level parallelism on the bandwidth-bound B=128 recurrence kernel; per-column f32 FMA order unchanged. 73.4%->84.6% of GB10 peak BW. | dense +11.1% / MoE +8.3% |
| 0028 | **Recurrent conv-tap gather fusion** - the last `k_get_rows` in the GDN decode path (the conv-state tap gather) becomes an indexed in-kernel read. | dense ~377 t/s / MoE ~784 t/s |

### MoE NVFP4 quant (0023, 0025, 0043)

| # | What it does | Bit-exact |
|---|---|---|
| 0023 | **NVFP4 activation-quantize de-dup** - the broadcast up/gate projections re-quantize the same token activation once per expert; quantize the unique token activations once and byte-copy them into the expert-gathered layout. The only NVFP4-specific patch. | yes (byte-identical) |
| 0025 | **MoE decode re-graph** - keep CUDA graphs on for the grouped-MMQ MoE decode step (the upstream guard disables graphs conservatively; the grouped path has no host sync). Was env-gated `LLAMA_MOE_FORCE_GRAPHS`; now ON by default via 0043. | yes (graph replay re-issues identical kernels) |
| 0043 | **MoE decode graph default-on (D1)** - flip 0025 to ON by default: capture/replay the full-step decode CUDA graph (incl. the grouped-MMQ MoE dispatch) instead of re-issuing every kernel each step. Guard is `should_use_mmq()` (FALSE for the large-M NVFP4 prefill of 0034, so prefill keeps graphs disabled - its per-expert host-loop genuinely syncs). `LLAMA_MOE_NO_FORCE_GRAPHS=1` forces the conservative pre-0025 disable for A/B. D1 profiling: the per-expert host-loop (the only device->host MoE-routing readback) is never hit on the NVFP4 grouped path (sync count identical graphs on/off); steady decode is ~99% GPU-busy, so the cost removed is per-step host kernel RE-ISSUE, not a sync. | yes (md5 byte-identical default/off/forced; paged-MoE `8cb0ce23`, dense `5951a5b4`) |

### Pool reclaim, block-table cache, backend gate

| # | What it does | Bit-exact |
|---|---|---|
| 0024 | **Paged-pool burst-reclaim** - truncate trailing blocks on partial-tail `seq_rm`, defrag the free queue when idle, release blocks on slot completion. Fixes the long-server burst-degradation bug (post-burst prefill collapse 488->44 t/s, restored to 532). Host-side accounting only. | yes |
| 0029 | **Block-table within-step host cache** - the block table is fixed for the whole step; cache it on first build and memcpy it for the other full-attention layers (get_block_table -87%/-91%). | yes, per path (paged-MoE ref `8cb0ce23`) |
| 0030 | **Fused-op backend gate** - the fused GDN / discriminated SSM_CONV ops are CUDA-family + CPU only; force them off on any non-CUDA compute backend so a Vulkan/SYCL/Metal build can't silently run the wrong plain-conv kernel. | yes on CUDA (byte-identical pre-0030); safety gate elsewhere |
| 0031 | **Chunked parallel-scan GDN prefill kernel** (upstream TODO) - FLA-style chunked gated-delta-rule for prefill (non-KDA / f32 / final-state): intra-chunk delta rule solved in parallel (UT-transform + forward subst), inter-chunk recurrence over n_tokens/C steps. The scalar-serial form (`GDN_TC=0`) was bit-exact-benign but not faster than the tuned sequential scan at the GB10-forced C=16 (see section 5); **superseded for paged by the tensor-core M5 path of 0047**. | NEW per-path (`test-backend-ops` 91/91, <=1e-7 NMSE vs CPU ref) |
| 0047 | **GDN M5 tensor-core chunked-scan prefill, f32-only re-port, default-ON under paged KV** - the f32/tf32 tensor-core forms of 0031's scan (KK/QK Gram = M2, KS/QS state-boundary 3xtf32 = M3, P*U output = M4, full form-T solve + state-update mma = M5), single build, runtime-selected by `GDN_TC`. Ships **M5 default-on when `LLAMA_KV_PAGED` is set** (`GDN_TC=5` + `GDN_CHUNK_MIN=64`, both env-overridable; OFF/`INT_MAX` when not paged). `GDN_CHUNK_MIN` is the per-call engage threshold and stays > 1 so decode (1 tok/call) keeps the sequential recurrence (at 1 it swallows decode and drops S_TG ~25%); 64 tuned from a {1,32,64,128,256} sweep. The bf16/hybrid dev-tree machinery (STATE_BF16/HYBRID, the dropped 0026 ssm_bf16_tau) and the bf16 CONFIG-C (M8) plus register-resident M6/M7 variants are NOT part of this f32-only series. MoE prefill S_PP +3.5% @npp512 (3x A/B), +17.7% @npp2048; decode S_TG unchanged. | NEW per-path, benign (`test-backend-ops` GATED_DELTA_NET 46/46 default AND force-M5, incl. multi-chunk/tail-chunk/multi-seq; greedy md5 default-on == M5-forced == canonical on the gate prompt: paged-MoE `8cb0ce23`, dense `5951a5b4`; long MoE prompt = one benign greedy flip vs sequential, dense byte-identical) |
| 0046 | **GDN prefill geometry gated by scan length** - patch 0022's `(NUM_WARPS=16, COLS_PER_WARP=8)` column-fold of the GDN sequential-recurrence dispatch (`case 128`) is a decode win but was applied UNCONDITIONALLY, so it also hit dense prefill (~-6% vs stock): on a long sequential scan the launch `grid.z` collapses from `S_v/4 = 32` to `S_v/(16*8) = 1` and the SMs starve (profiled: `gated_delta_net` +54% GPU time = the whole dense-prefill regression). Gate the geometry by per-call scan length: long scans (prefill, `n_tokens >= GDN_PREFILL_NTOK`, default 256) take stock's high-grid.z `(4,1)` geometry; short scans (decode) keep the `(16,8)` retune. Recovers dense prefill +7.2% back to stock parity, keeps the decode win. `GDN_PREFILL_NTOK` tunes the crossover; an explicit `GDN_NW`/`GDN_CPW` sweep still overrides (gate yields when either is set), so the one-build %peak A/B harness is unchanged. | yes (patch 0022 proved every `{NW,CPW}` variant byte-identical, so switching geometry by scan length cannot move the md5) |

### Speculative / MTP investigation (0054, 0055)

| # | What it does | Bit-exact / effect |
|---|---|---|
| 0054 | **Disable backend sampling for MTP drafts** - forces server MTP draft generation through the target-side sampler acceptance path instead of letting the draft backend sample independently. This was required for the Phase 14 rollback/prefix safety gate. | yes for canonical non-MTP gates; Phase 14 MTP normalized greedy-prefix gate passed |
| 0055 | **Trace speculative batch shapes** - adds default-off `LLAMA_SPEC_SHAPE_TRACE=1` server logs around `server_slot::handle_last_sampled_token()`, reporting normal decode rows and MTP verification `K + 1` rows (`draft`, `outputs`, `spec_i_first`, `spec_i_last`). This is instrumentation only for Phase 18 shape-entropy measurement before any scheduler experiment. | yes (env unset is silent; DGX gates after patch: MoE `8cb0ce23`, dense `5951a5b4`, `MUL_MAT_ID` `806/806`) |
| 0056 | **Trace MoE MMQ batch shapes** - adds default-off `LLAMA_MOE_MMQ_SHAPE_TRACE=<n>` logs from the grouped-MMQ host selector, reporting routed assignment count, estimated active experts, density, selected `mmq_x`, `mmq_y`, and stream-k. This is evidence-only instrumentation for sizing structural grouped-MMQ work after Phase 28 rejected launch-bounds/row-tile knobs. | yes (env unset and trace-enabled gates both green: MoE `8cb0ce23`, dense `5951a5b4`, `MUL_MAT_ID` `806/806`; trace cap verified with 4 lines) |
| 0057 | **Trace MoE MMQ launch shapes** - extends `LLAMA_MOE_MMQ_SHAPE_TRACE=<n>` with bounded `[LLAMA_MOE_MMQ_LAUNCH]` lines from `launch_mul_mat_q`, recording actual `ntiles_dst`, `stream_k_blocks`, tile efficiency, `fixup`, `ntx/nty/ntzw`, and compiled `mmq_x/mmq_y`. This is evidence-only instrumentation to distinguish real stream-k/fixup overhead from small-M kernel-shape cost. | yes (default-off, trace-enabled, and post-serving gates green: MoE `8cb0ce23`, dense `5951a5b4`, `MUL_MAT_ID` `806/806`; Phase 31 n128 trace showed decode and prefill `fixup=0`, `stream_k_blocks == ntiles_dst`) |
| 0058 | **Trace MoE small-M MMQ candidates** - adds `LLAMA_MOE_MMQ_SMALL_M_TRACE=<n>` and a host-only classifier for decode-like low-density grouped-MMQ shapes (`ncols_max <= 128`, density `<=4`, `mmq_x_best <=64`). It only counts candidate calls for the next structural tile-policy A/B; no numeric branch is added. | yes (default-off, trace-enabled, and post-serving gates green: MoE `8cb0ce23`, dense `5951a5b4`, `MUL_MAT_ID` `806/806`; Phase 32 n128 trace found 4096 candidates, mostly `mmq_x_best=64/48`) |
| 0059 | **Gate MoE small-M MMQ tile policy** - adds default-off `LLAMA_MOE_SMALL_M_TILE=<n>` to cap only classified small-M MoE grouped-MMQ calls. This was used to A/B vLLM-like smaller M blocks without changing default inference. | yes (default-off, tile16, tile8, and post-serving gates green: MoE `8cb0ce23`, dense `5951a5b4`, `MUL_MAT_ID` `806/806`; Phase 33 rejected tile16 and tile8 as slower) |
| 0060 | **Trace MoE MMID dispatch routes** - adds default-off `LLAMA_MOE_MMID_ROUTE_TRACE=<n>` around `MUL_MAT_ID` dispatch, classifying each call as `mmvq`, `mmvf`, grouped `mmq`, `mmf`, or host-sync `fallback`. This is evidence-only instrumentation to resolve whether serving hits the per-expert host-sync fallback. | yes (default-off, trace-enabled, and post-serving gates green: MoE `8cb0ce23`, dense `5951a5b4`, `MUL_MAT_ID` `806/806`; Phase 34 n128 trace found `mmq=2776`, `mmvq=1320`, `host_sync=0/4096`) |
| 0061 | **Trace regular MUL_MAT dispatch routes** - adds default-off `LLAMA_MUL_MAT_ROUTE_TRACE=<n>` around regular `MUL_MAT`, classifying projection-heavy calls as `vec_f`, `mat_f`, `vec_q`, `mmq`, `batched_cublas`, `op_*`, `fp4_prefill`, or `fwht`. This is evidence-only instrumentation for the `bf16-proj` serving bucket. | yes (default-off, trace-enabled, and post-serving gates green: MoE `8cb0ce23`, dense `5951a5b4`, `MUL_MAT` `1146/1146`, `MUL_MAT_ID` `806/806`; Phase 35 n128 trace found BF16 routes `mat_f=2485`, `op_cublas=1330`) |
| 0062 | **Trace cuBLAS subroutes** - adds default-off `LLAMA_CUBLAS_ROUTE_TRACE=<n>` around the generic cuBLAS `MUL_MAT` path, classifying calls as `nvfp4_bf16_tc`, `bf16_tc`, `f16_tc_32f`, `f16_tc_16f`, or `sgemm`. This is evidence-only instrumentation for the Phase 35 `op_cublas` bucket. | yes (default-off, trace-enabled, and post-serving gates green: MoE `8cb0ce23`, dense `5951a5b4`, `MUL_MAT` `1146/1146`, `MUL_MAT_ID` `806/806`; Phase 36 n128 trace found `bf16_tc=5681`, `sgemm=2511`) |

> **Dropped: patch 0026 (hybrid per-head bf16 SSM state, `ssm_bf16_tau`).** Once
> the decode fusions (0028 recurrent-state gather-fusion + 0029 block-table cache)
> landed, the bf16-SSM lever bought nothing: a clean re-measurement forcing **all**
> gated-DeltaNet heads to bf16 (`tau=100000`) gives **flat** decode (780.6 vs
> 780.0 t/s) - the mode engages but adds zero throughput because it is subsumed by
> the fusions. It was a precision trade (not bit-exact) plus extra bug surface and
> CUDA template-instantiation compile cost with no benefit, so it was removed. See
> section 5 ("rejected / flat levers") for the full record.

---

## 4. Benchmarks

Hardware: **GB10 / DGX Spark** (CUDA 13, sm_121). Models: dense
**Qwen3.6-27B-NVFP4** and MoE **Qwen3.6-35B-A3B-NVFP4**. Metric: `decode_agg`
S_TG (t/s) from `llama-batched-bench`, `-fa on -ngl 99`, `npp 128 / ntg 128`,
swept over serving width `npl` in {8, 32, 64, 128}. Plots:
[`qwen36_decode_overview.png`](docs/qwen36_decode_overview.png) (both models),
[`qwen36_dense_decode_vs_npl.png`](docs/qwen36_dense_decode_vs_npl.png),
[`qwen36_moe_decode_vs_npl.png`](docs/qwen36_moe_decode_vs_npl.png); raw data
[`final_benchmark.csv`](docs/final_benchmark.csv).

![NVFP4 decode throughput vs concurrency on GB10: llama.cpp standard vs vLLM vs LocalAI's llama.cpp patches](docs/qwen36_decode_overview.png)

> The plot above also shows a third "bf16-tau" llama curve. That was the opt-in
> `ssm_bf16_tau` lever (patch 0026), since **dropped** - a clean re-measurement
> showed it flat once the decode fusions landed (see section 5). The numbers below
> use only **stock** vs **patched** vs **vLLM**.

> **What was re-measured (2026-06-27).** The two llama columns - **stock** and
> **patched** - were re-measured this session on one consistent
> `llama-batched-bench` harness. The **vLLM** column is the **prior-session
> reference** (kept as-is, *not* re-run this session). Per-run peak
> VRAM was *not* re-captured: the GB10's unified Grace-Blackwell LPDDR5x reports
> `[N/A]` to `nvidia-smi --query-gpu=memory.used` and the bench does not print it
> (the memory-advantage note below is the prior-session finding).

### (a) + (b) Patched vs stock vs vLLM

The **stock** column is a separate, unpatched llama.cpp built at this backend's
**exact pin (`9d5d882d`)**; the **patched** column is
the paged binary, env/flag-toggled (`LLAMA_KV_PAGED=1`, plus
`LLAMA_MOE_FORCE_GRAPHS=1` for MoE). Both
run on the **same harness**, so "x over stock" is an apples-to-apples measure of
the patch series. (Note: the patch series' dominant SSM decode fusions are
compiled in, not env-gated - toggling `LLAMA_KV_PAGED` alone on the *patched*
binary does **not** reproduce stock; only the separately-built unpatched
`9d5d882d` binary does.) The **vLLM** column is a **different harness** (vLLM
server + client continuous batching) and a **prior-session reference**, so the
cross-engine "% of vLLM" is **indicative, not apples-to-apples**.

**Dense Qwen3.6-27B-NVFP4** (decode t/s):

| npl | stock | patched | vLLM (prior) | patched x over stock |
|----:|------:|--------:|-------------:|---------------------:|
| 8   |  68.3 |   85.3 |         70.4 | 1.25x |
| 32  | 119.9 |  211.9 |        211.8 | 1.77x |
| 64  | 142.8 |  305.2 |        309.1 | 2.14x |
| 128 | 155.1 |  382.1 |        418.8 | 2.46x |

Dense **patched** is parity-to-ahead of vLLM (121 / 100 / 99 / 91% of vLLM across
the widths).

**MoE Qwen3.6-35B-A3B-NVFP4** (decode t/s):

| npl | stock | patched | vLLM (prior) | patched x over stock |
|----:|------:|--------:|-------------:|---------------------:|
| 8   | 186.7 |  230.3 |        256.5 | 1.23x |
| 32  | 267.4 |  466.4 |        500.8 | 1.74x |
| 64  | 320.5 |  622.4 |        686.1 | 1.94x |
| 128 | 347.2 |  784.3 |        882.2 | 2.26x |

MoE **patched** is 90 / 93 / 91 / 89% of vLLM.

**Caveat on the vLLM column.** It is a **different harness** and a
**prior-session** measurement (not re-run this session), so the cross-engine "% of
vLLM" is **indicative, not apples-to-apples**. Memory (prior session): llama uses
**1.5-3x lower** memory than vLLM.

**Takeaway.** Re-measured this session, the patch series gives up to **2.46x
(dense) / 2.26x (MoE)** over true-stock `9d5d882d` on the same harness (close to,
slightly below, the prior 2.59x / 2.33x - llama was re-measured, vLLM kept).
Dense is parity-to-ahead of vLLM; MoE **patched** sits at ~89-93% of the
prior-session vLLM. The residual MoE gap is structural (see section 5).

### (c) Apple Silicon (M4, 16GB Metal) - does the patchset help here?

Short answer: **no - the wins are CUDA/Blackwell-specific.** Two facts first: the
24GB NVFP4 GGUF doesn't fit a 16GB M4 (SSD paging), and on Metal `supports_op`
**excludes NVFP4** from `MUL_MAT`/`MUL_MAT_ID`/`GET_ROWS` (FP4 matmuls fall back to
CPU - no Apple FP4-MMA). So NVFP4 Qwen3.6 is not a Mac fit; a Metal-native Q4_K is.

Measured **stock vs patched** (same pin `c299a92c`, both built `-DGGML_METAL=ON`;
the 28-patch series **compiles clean on Metal** - the CUDA code is `#if`-guarded),
on **Qwen3-8B Q4_K_M** (a dense GQA model that fits 16GB and exercises the *live*
Metal features; no Qwen3.6 hybrid GGUF fits 16GB, and the GDN fusions gate off on
Metal anyway), `llama-bench` pp512/tg128 t/s:

| config | pp512 | tg128 |
|---|---:|---:|
| stock | 226.7 | 20.4 |
| patched, paged **off** | 226.7 | 20.3 (= stock) |
| patched, paged **on** | 222.6 | 19.8 (~0.97x) |

Concurrency (`batched-bench`) scales identically to stock (S_TG ~20 -> ~137 at
npl32, from llama.cpp's existing batching). **Verdict: neutral-to-slightly-negative
on Metal.** Patched-paged-off equals stock; turning paged on is ~0-3% slower
decode / ~2-8% slower prefill, because the in-kernel block-table flash-attn read
that *recovers* the gather cost is CUDA-only (`fattn-*.cuh`) - on Metal the paged
path falls back to a host-side gather, pure overhead over stock's contiguous read.
Everything Blackwell-specific (NVFP4, GDN fusions via 0030, occupancy) is inert.
So **on Apple Silicon, prefer the stock `llama-cpp` backend.**

**Vulkan / SYCL** (source analysis): the gated-DeltaNet and SSM_CONV ops DO have
upstream kernels on Vulkan and SYCL (as on Metal), so the Qwen3.6 hybrids RUN on
all three via the non-fused path. The patchset's fusions are gated off there
(0030), so the outcome is the same neutral-to-slightly-negative as Metal - not
"won't run". This backend therefore ships **CUDA-only** (where the fusions are
live + verified); non-CUDA users should use the stock `llama-cpp` backend. See
[`UPSTREAM_LAYER2_SCOPE.md`](docs/UPSTREAM_LAYER2_SCOPE.md) for what native non-CUDA
fused kernels would take.

---

## 5. Dev notes - what we learned

**Bit-exact methodology.** Every bit-exact patch is gated two ways: (1) a greedy
md5 gate - `llama-completion -m MODEL -ngl 99 -fa on -p "The capital of France
is" -n 48 --temp 0 --seed 1 | md5sum`, paged paths prefixed with
`LLAMA_KV_PAGED=1` (+ `LLAMA_MOE_FORCE_GRAPHS=1` for paged MoE), on the default
chat-template path; and (2) `test-backend-ops` (CUDA0 vs CPU oracle) for every
touched op (`SSM_CONV*`, `GATED_DELTA_NET`, `MUL_MAT`, `MUL_MAT_ID`).
For DGX work, `paged-inference-gates.sh` runs the canonical MoE/dense transcript
md5 checks and selected `test-backend-ops` filters, and refuses to start while
docker, `local-ai-worker`, GPU compute processes, or a non-free GPU lock are
present.

For direct `llama-server` MTP serving A/B work, use
`paged-mtp-serving-bench.sh`. It runs the same pre/post inference gates, compares
baseline vs `--spec-type draft-mtp`, and captures the h2h client summaries plus
MTP acceptance lines. Phase 15 rejected current MTP serving on GB10 despite
passing safety gates; do not enable it by default.

**The gate is per-path** (see [`PAGED_BITEXACT_NOTE.md`](docs/PAGED_BITEXACT_NOTE.md)).
Dense is bit-exact across paged/non-paged (`5951a5b4`). The **paged MoE** md5
(`8cb0ce23`) does **not** byte-match the **non-paged MoE** md5 (`07db32c2`); this
is a benign FP-accumulation-order difference of the paged attention reduction,
**KL-validated** against the f16 reference: KLD(paged||f16) 0.13600 <=
KLD(nonpaged||f16) 0.13660, PPL within +/-0.29, ~zero probability bias - two
equivalent FP-reorderings of the same quantized model, not a regression. Future
paged-MoE regressions therefore compare to `8cb0ce23`, not `07db32c2`.

**MoE-parity conclusion** (the residual gap is structural). The two heaviest MoE
decode kernels - the GDN-SSM recurrence and the NVFP4-expert GEMM - are llama
**wins** after this series (the recurrence runs at 102.6% of vLLM's bandwidth;
the GEMM ties vLLM at the LPDDR5x BW floor). The residual gap is **bf16-projection
bandwidth + the host scheduling loop**, both at the LPDDR5x floor - not a kernel
llama is losing. The MoE GEMM kernel is *not* where the gap lives.

**Rejected / flat levers** (recorded so they are not re-tried):

- **Lever 2 - graph/stream coverage: FLAT.** Bit-exact graph coverage was
  exhausted by 0025; more graph/stream overlap is a no-op or small regression on
  this model.
- **D1 premise "static decode is host-sync-bound on the MoE-routing readback":
  REFUTED.** The hypothesis was that the dominant decode cost is the device->host
  readback of MoE routing before launching the per-expert GEMMs (mul_mat_id's
  per-expert host-loop fallback). Profiling (GB10, q36-35b-a3b-nvfp4, batched-bench
  npl128) shows the opposite: on NVFP4 the grouped stream-k MMQ id-path is what
  runs (routing stays device-side), so the host-loop fallback is **never hit** -
  `cudaStreamSynchronize` count is *identical* with CUDA graphs on vs off (1457
  either way; only the kernel-launch count changes, ~100k vs ~229k). Steady-decode
  GPU-busy is **~99%** (1% idle), i.e. static decode is GPU-bound, not idle waiting
  on a sync. The one actionable residual the profile surfaced - per-step host
  kernel **re-issue** when the step is not graph-captured - shipped as 0043
  (default-on full-step decode graph), worth +2.6% (npl128) to +5-13% (npl32). The
  larger continuous-serving host cost is the graph **rebuild** (0040/0041), and the
  irreducible floor is the per-step logits-D2H-before-sampling serial point - none
  of which is the MoE-routing readback.
- **Lever 3 - act-quant fusion: FLAT.** The W4A4 act-quant tax is removable only
  by W4A16 (a precision change, rejected) or a structural kernel rewrite; no
  further bit-exact lever clears it. 0023 already banks the de-dup.
- **Lever 4 - NVFP4 the bf16 GDN/attn projections: REJECTED (KL-gate fail).**
  Quantizing the projections to NVFP4 costs ~+6% PPL; vLLM deliberately keeps the
  same bf16 projections. No-ship.
- **W4A16-Marlin MoE GEMM: REJECTED.** It would be a precision upgrade nobody
  needs bought with a ~5% slower kernel; both kernels are already at the BW floor.
  (The "the win was NVFP4-dense-quant, not the Marlin kernel" dense verdict
  carries over to MoE.)
- **Chunked parallel-scan GDN prefill (patch 0031): the scalar-serial form was
  FLAT-to-SLOWER at C=16 - the tensor-core M5 form (patch 0047) is the win,
  now DEFAULT-ON under paged KV.** 0031 implements the upstream "faster pre-fill"
  TODO - the FLA-style chunked gated-delta-rule (intra-chunk delta rule solved in
  parallel via the UT-transform + forward substitution, inter-chunk recurrence
  over n_tokens/C steps), math validated equivalent (numpy f32 NMSE ~1e-13;
  `test-backend-ops` within the 1e-7 NMSE gate, a NEW per-path result). **But
  GB10's 99KB dynamic-smem opt-in forces C=16** (the 128x128 f32 state alone is
  64KB of the all-shared layout); the scalar-serial scan (`GDN_TC=0`) was then
  pinned to 1 block/SM with serial per-thread dk-reductions and measured **~761
  t/s chunked vs ~971 t/s sequential (~22% slower)**, grid-starved at low n_seqs.
  The lesson held: **at this head dim the win needs tensor cores, not just
  chunking.** Patch 0047 builds those tensor-core forms (KK/QK Gram = M2, KS/QS
  state-boundary 3xtf32 = M3, P*U output = M4, full form-T solve + state-update
  mma = M5, all `GDN_TC`-selected in one build) and ships **M5** as the default
  when `LLAMA_KV_PAGED` is set. It is an f32/tf32-only re-port: the bf16/hybrid
  dev-tree machinery (from the dropped 0026 ssm_bf16_tau) and the bf16 CONFIG-C
  (M8) plus register-resident M6/M7 variants are NOT part of this series. M5 is the
  variant that beats the (already 84.7%-of-peak) sequential scan while staying on
  the bit-exact gate: MoE prefill S_PP **+3.5% @npp512 (3x interleaved A/B), +17.7%
  @npp2048**; decode S_TG unchanged (the tuned `GDN_CHUNK_MIN=64` engage threshold
  is > 1, so the 1-tok decode steps never enter the chunked path - at
  `GDN_CHUNK_MIN=1` the chunked path swallows decode and collapses S_TG ~25%, the
  reason the threshold is the lever). Bit-exactness is per-path benign:
  `test-backend-ops` GATED_DELTA_NET is **94/94** vs CPU with M5 forced (incl.
  multi-chunk n_tokens up to 256); the greedy md5 default-on == M5-forced ==
  canonical on the short gate prompt (paged-MoE `8cb0ce23`, dense `5951a5b4`); on
  a long MoE prompt (where the default fires M5 at >=64 tokens) M5 and the
  sequential path agree word-for-word until **one** benign greedy token-flip
  ("the User:" vs "the User's Request:"), the dense model not flipping at all -
  the textbook reduction-order flip greedy amplifies, NMSE-validated. The chunk
  geometry stays env-selectable (`GDN_TC`/`GDN_CHUNK_C`/`GDN_DV_TILE`) for further
  tuning; M5 is the shipped default because it wins without losing the canonical gate.
- **GDN occupancy retune (patch 0022) was a decode win but an UNCONDITIONAL
  dense-prefill regression - now gated by scan length (patch 0046).** Patch
  0022's `(NUM_WARPS=16, COLS_PER_WARP=8)` column-fold of the GDN
  sequential-recurrence dispatch (`case 128`) raises per-warp memory-level
  parallelism on the short, wide DECODE scans (small `n_tokens`, large
  `n_seqs`) - the measured +11.1% dense decode win. Applied unconditionally it
  also hit the dense PREFILL path, where the scan is long and narrow: the launch
  `grid.z` collapses from `S_v/4 = 32` to `S_v/(16*8) = 1`, the SMs starve, and
  profiling attributed the whole ~-6% dense-prefill regression vs stock to
  `gated_delta_net` (+54% GPU time at the (16,8) geometry). Patch 0046 gates the
  geometry by per-call scan length: long scans (prefill,
  `n_tokens >= GDN_PREFILL_NTOK`, default 256) take stock's high-grid.z `(4,1)`
  geometry; short scans (decode) keep the `(16,8)` retune. That recovers dense
  prefill +7.2% back to stock parity while keeping the decode win, and it is
  bit-exact: patch 0022 already proved every selectable `{NUM_WARPS,
  COLS_PER_WARP}` variant is byte-identical (the sweep cannot change the md5), so
  switching geometry by scan length cannot move the greedy output. The explicit
  `GDN_NW`/`GDN_CPW` one-build %peak sweep still overrides (the gate yields when
  either is set), so the A/B harness is unchanged.

**Opt-in bf16-SSM fast mode - DROPPED (was patch 0026, `ssm_bf16_tau`).** The
design premise - that bf16 KL error concentrates in long-memory heads and can be
removed by keeping them f32 - was already shaky: the error scales with the bf16
head *count* and saturates (~0.06 MeanKLD / ~91% same-top-p) far below any useful
byte saving. The lever was then **removed entirely** once the decode fusions
(0028 recurrent-state gather-fusion + 0029 block-table cache) landed: a clean
re-measurement that forced **all** gated-DeltaNet heads to bf16 (`tau=100000`,
the most aggressive setting) gave **flat** decode throughput - **780.6 vs 780.0
t/s**. The mode engages but buys **zero** speed; the earlier "+12%" was subsumed
by the fusions. So bf16-tau was a precision trade (not bit-exact) plus extra bug
surface and CUDA template-instantiation compile cost with **no** offsetting
benefit, and patch 0026 was dropped from the series. Lesson recorded so it is not
re-tried: do not reintroduce a per-head SSM-precision lever - the bandwidth it
targeted is already recovered by the gather-fusion + block-table cache.

---

## 6. Architecture and quant generality

(From the arch-generality and quant-generality audits.)

- **15 of 16 optimizations are quant-AGNOSTIC.** Only **0023** (NVFP4
  activation-quantize de-dup) is NVFP4-specific. The SSM/paged/MMQ optimizations
  help **any quant** of these models (the GDN recurrence, conv, gather and
  o_proj-MMQ levers operate on the f32 recurrent state and the routing layout,
  not on the weight dtype).
- **Arch-safe to build everywhere.** NVFP4 use is Blackwell-gated and falls back
  to dequant on other hardware; the GB10-tuned occupancy params (0022) are
  perf-only and env-selectable (`GDN_NW` / `GDN_CPW`), so they never change
  correctness on other GPUs. Patch 0030 makes the fused-op emission CUDA-family +
  CPU only, so a non-CUDA paged build routes to the safe upstream non-fused path.

- **What generalizes beyond this backend (upstream candidates).** The *speedups*
  are CUDA/Blackwell-specific (which is why Metal/Vulkan don't benefit - section
  4c), but several *findings and ops* are portable and worth upstreaming:
  - The headline is hardware-independent: on hybrid gated-DeltaNet models, decode
    is bottlenecked by the recurrent-state **plumbing** (memcpy + gathers, ~67% of
    the step), not the weight GEMM. The fusions for it (in-place state 0018, gather
    0019/0028, conv 0021) are bit-exact and already have CPU reference kernels, so
    they would speed up Qwen3.6 / Qwen3-Next / any hybrid-SSM decode on **every**
    backend once the ggml ops gain the respective (Metal/Vulkan) kernels - the
    highest-value upstream contribution.
  - The o_proj GEMV->MMQ reshape (0020) is a model-graph fix (batch the projection
    to hit the GEMM path) - arch-agnostic in principle, trivial to upstream.
  - The paged KV + cross-request prefix sharing + decode-first scheduler align with
    llama.cpp's own in-progress KV / chunked-prefill work and could inform it.
  - The per-path bit-exact md5 gate + the weekly upstream-drift canary is a reusable
    maintenance pattern for any vendored-patch backend.

---

## 7. Pin + maintenance policy

- **Canonical source = the fork branch `mudler/llama.cpp:localai-paged`.** The
  vendored `patches/paged/*.patch` files are now generated (one `git format-patch`
  per commit) from that branch, which is the pin commit plus the paged patch
  commits in order, so there is no more hand-export drift between the dev tree and
  the shipped series.
- **Pinned to llama.cpp `0ed235ea2c17a19fc8238668653946721ed136fd`** (kept == the stock `llama-cpp` pin). The pin
  is advanced **only** by the manual pin-sync process (this section):
  rebase the source-only patch series onto the new tip, rebuild on GPU, pass the
  bit-exact gate on every path (dense + MoE, paged + non-paged) plus
  `test-backend-ops`, **and confirm the full grpc-server build links on CI**.
- **The pin must track the stock pin.** `grpc-server.cpp` is shared with the stock
  backend and tracks the stock pin, so a paged pin that diverges past an upstream
  server-API refactor breaks the grpc-server LINK even when the patches are
  bit-exact. A bump to `c299a92c` (23 commits ahead of stock) was greedy-md5
  bit-exact but failed to link (undefined `stream_*` server helpers introduced by
  the refactor), and was reverted to the then-current stock pin. The bit-exact gate alone does not
  catch this; only the full CI grpc-server build does.
- **Decoupled from the nightly auto-bumper.** There is deliberately **no**
  `bump_deps.yaml` entry for this backend - a naive `LLAMA_VERSION` bump could
  silently shift the tree out from under the patches.
- **Weekly canary.** [`.github/workflows/llama-cpp-paged-canary.yml`](../../../.github/workflows/llama-cpp-paged-canary.yml)
  (via [`.github/scripts/paged-canary-apply.sh`](../../../.github/scripts/paged-canary-apply.sh))
  tries the patch series against the latest upstream tip with the build's own
  strict `git apply`. **Red = upstream drifted past the series -> run a
  PIN_SYNC** (do not bump the pin blindly), following the policy in this section.

---

## 8. Models

> **Build coverage: CUDA-only.** This backend ships only the CUDA/cublas build
> targets (cuda-12, cuda-13, and the nvidia-l4t arm64 cuda-12/cuda-13 Jetson
> rows). There are no cpu / vulkan / sycl / hipblas / metal-darwin builds: the
> patchset's wins are CUDA/Blackwell-specific (section 4c), so off-CUDA the
> backend is neutral-to-negative and non-CUDA users should run the stock
> `llama-cpp` backend instead. The `backend/index.yaml` meta-backend resolves
> `default`/`nvidia` to a CUDA variant accordingly.

The benchmarked NVFP4 GGUFs are published and wired into the LocalAI gallery:

| Gallery entry | Weights (HuggingFace) | Notes |
|---|---|---|
| `qwen3.6-27b-nvfp4-paged` | [`mudler/Qwen3.6-27B-NVFP4-GGUF`](https://huggingface.co/mudler/Qwen3.6-27B-NVFP4-GGUF) | Dense, native Blackwell NVFP4 (FP4-MMA). |
| `qwen3.6-35b-a3b-nvfp4-paged` | [`mudler/Qwen3.6-35B-A3B-NVFP4-GGUF`](https://huggingface.co/mudler/Qwen3.6-35B-A3B-NVFP4-GGUF) | MoE (256 experts, top-8), `file_type MOSTLY_NVFP4`. |

Both gallery entries set `backend: llama-cpp-localai-paged` and the paged serving config
(`paged_kv:true`, `max_batch_tokens`, `kv_unified:false`, `parallel`,
`flash_attention:on`, `context_size`). They are bit-exact. The full
backend-split + gallery plan is in
[`LOCALAI_LLAMACPP_BACKEND_PLAN.md`](docs/LOCALAI_LLAMACPP_BACKEND_PLAN.md).

---

## 9. vLLM parity - final state (CLOSED)

> 2026-07-01 follow-up: the investigation was reopened for MTP safety,
> MTP-serving, graph-shape tracing, and a current-stack serving snapshot. Phases
> 14-20 are recorded in
> [`docs/GB10_PARITY_PHASE0_RESULTS.md`](docs/GB10_PARITY_PHASE0_RESULTS.md) and
> [`docs/PARITY_HANDOFF.md`](docs/PARITY_HANDOFF.md). They did not change the
> GB10 conclusion: MTP/scheduler shortcuts are rejected, and the latest clean
> stack remains below vLLM serving parity.

The multi-week GB10 (DGX Spark, sm_121) vLLM-parity investigation is **closed**.
The standing, never-re-litigate record - full benchmark, every lever and verdict,
the structural floors, the parity verdict - is
[`docs/VLLM_PARITY_FINAL.md`](docs/VLLM_PARITY_FINAL.md). Summary:

- **Where we are (GB10, Qwen3.6 NVFP4, vs vLLM 0.23.0).** Decode: dense is
  **ahead of vLLM at low concurrency (116.7% at N=8)** and both models are
  bandwidth-floored at **~56-68% of vLLM at high concurrency**. Prefill is
  **~36% (MoE) / ~43% (dense)** of vLLM. Memory: **1.5-3x lower** than vLLM
  (NVFP4-resident; vLLM's peak is a fixed ~109-112 GB 0.85-util reservation,
  paged grows with KV from ~50 GB). Output is bit-exact per-path
  (`5951a5b4` dense, `8cb0ce23` paged-MoE).
- **Why the residual is a hardware ceiling, not missing work.** Decode kernels
  are already **5.4x more GPU-efficient per token** than vLLM's; the gap is the
  **LPDDR5x ~273 GB/s** floor. The prefill GEMM is **FP4-MMQ-optimal** (every
  alternative - 0033 dequant->cuBLAS, 0034 native FP4-MMA, 0035/Marlin W4A16,
  offline-repack and vLLM-verbatim Marlin - was rejected; bf16 TC peak is ~half
  FP4 peak, and vLLM itself runs a bf16-Marlin fallback on sm_121). The GDN
  chunked scan is at the tractable tensor-core win (**M5 tf32**, patch 0047);
  its residual is the **O(C^2) intra-chunk solve + serial recurrence** (occupancy
  and dtype proven not the bound: BV -1%, bf16-C64 -18.75%). The serving host
  loop is **closed** (~0-1% of the wall; padded-decode built + rejected).
- **Shipped, bit-exact wins.** FP4-MMQ GEMM, M5 tensor-core GDN prefill (0047),
  fused residual+RMSNorm (0042), fused GatedRMSNorm+SiLU (0044), GDN-prefill
  geometry gate (0046), the SSM decode-fusion stack (0018-0022/0028, up to
  2.46x/2.26x over stock), decode-graph reuse (0040/0043), the memory advantage,
  and low-N decode lead.
- **The path to parity is different hardware.** Datacenter Blackwell (HBM,
  native tcgen05/CUTLASS FP4) lifts the bandwidth floor and **restores exactly
  the vLLM advantages that lose on GB10** (FLA blocked-solve GDN, Marlin/CUTLASS
  grouped FP4, HBM-tuned full-cudagraph decode). Re-run the methodology on new
  silicon; do not reopen the GB10 levers.

Latest current-stack MoE serving snapshot (`PTOK=128`, `GEN=64`, current clean
DGX mirror `f2521ab12`, artifact
`/home/mudler/bench/phase26_audited_snapshot/20260701_053650`). This run
includes `hardware.txt` and `gate_summary.tsv`; all pre/post gate rows are
`ok`:

| n | paged decode_agg | vLLM decode_agg | paged/vLLM decode | paged agg | vLLM agg | paged/vLLM agg |
|---|------------------|-----------------|-------------------|-----------|----------|----------------|
| 8 | 230.8 | 283.2 | 81.5% | 170.6 | 241.6 | 70.6% |
| 32 | 420.0 | 609.0 | 69.0% | 254.6 | 466.7 | 54.6% |
| 128 | 673.4 | 1025.0 | 65.7% | 324.0 | 656.5 | 49.4% |

Use `paged-current-serving-snapshot.sh` for future current-stack GB10 serving
snapshots. It targets the clean `~/llama-phase6-source` mirror, checks
docker/`local-ai-worker`/GPU-idle state, uses the owner-file lock, runs pre/post
inference gates, writes `hardware.txt`, emits `gate_summary.tsv`, and emits
paged/vLLM ratios.
`hardware.txt` records the GPU identity and hardware class so GB10/workstation
Blackwell evidence is not confused with a future datacenter-Blackwell rerun.
`gate_summary.tsv` records pre/post MoE md5, dense md5, and backend-op checks
so an artifact proves inferencing gates without reading full logs.
Do not use the stale DGX
`~/bench/combined_definitive.sh` without first porting it to the current mirror
and lock discipline.

Phase 28 challenged the remaining low-conflict NVFP4 grouped-MMQ occupancy
knobs on the same DGX mirror
(`/home/mudler/bench/phase28_mmq_occupancy/20260701_040450`). The only buildable
variant, `GGML_CUDA_FP4_MINBLOCKS=2`, was inference-safe before and after
serving (MoE `8cb0ce23`, dense `5951a5b4`, `MUL_MAT_ID 806/806`) but regressed
n128 decode serving (`705.1 -> 689.9` decode_agg_tps, `0.9784x`). The row-tile
knob `GGML_CUDA_FP4_MMQ_Y=64` failed the NVFP4 writeback compile-time
invariant. Do not promote these knobs; grouped-MMQ parity work now requires a
structural kernel change, not launch-bounds or row-tile tweaks.

Phase 29 added the default-off grouped-MMQ shape trace as patch `0056`
(`/home/mudler/bench/phase29_mmq_shape_trace/20260701_042428`). The helper was
added test-first (`test-cuda-mmq-shape-trace`), compiled under CUDA on DGX, and
kept inference stable with the trace disabled and enabled:
MoE `8cb0ce23`, dense `5951a5b4`, `MUL_MAT_ID 806/806`. Example trace line:
`[LLAMA_MOE_MMQ_SHAPE] type=40 moe=1 ncols_dst=104 nchannels_x=256 ncols_max=13 n_active_est=104 density=1 mmq_x_max=128 mmq_x_lim=64 mmq_x_best=16 mmq_y=128 stream_k=1`.

Phase 31 extended that trace as patch `0057`
(`/home/mudler/bench/phase31_mmq_launch_trace/20260701_064424`) with
`[LLAMA_MOE_MMQ_LAUNCH]` lines from `launch_mul_mat_q`. Default-off,
trace-enabled, and post-serving gates stayed stable: MoE `8cb0ce23`, dense
`5951a5b4`, `MUL_MAT_ID 806/806`. The n128 serving trace showed decode-like
`4800/4800` and prefill-like `4920/4920` launch lines with `fixup=0` and
`stream_k_blocks == ntiles_dst`, rejecting a no-fixup/no-stream-k shortcut for
this workload.

Phase 32 added the small-M classifier trace as patch `0058`
(`/home/mudler/bench/phase32_small_m_classifier/20260701_070127`). Default-off,
trace-enabled, and post-serving gates stayed stable: MoE `8cb0ce23`, dense
`5951a5b4`, `MUL_MAT_ID 806/806`. The n128 serving trace found 4096 small-M
candidate calls: `mmq_x_best=64` 1800, `48` 1096, `40` 360, `32` 360, `16`
360, `24` 120. This justifies Phase 33 as a default-off tile-policy A/B
(`mmq_x=16`, possibly `8`) rather than a broad kernel rewrite.

Phase 33 added default-off `LLAMA_MOE_SMALL_M_TILE=<n>` as patch `0059`
(`/home/mudler/bench/phase33_small_m_tile_policy/20260701_071136`). The knob is
md5/op safe, but both tested values were slower in same-session n128 serving:
baseline `672.1` decode_agg_tps, tile16 `640.3` (`0.953x`), tile8 `583.2`
(`0.868x`). Do not promote simple smaller `mmq_x` caps for this workload.

Phase 34 added default-off `LLAMA_MOE_MMID_ROUTE_TRACE=<n>` as patch `0060`
(`/home/mudler/bench/phase34_mmid_route_trace/20260701_072737`). Default-off,
trace-enabled, and post-serving gates stayed stable: MoE `8cb0ce23`, dense
`5951a5b4`, `MUL_MAT_ID 806/806`. Live n128 serving with trace cap 4096 produced
`mmq=2776`, `mmvq=1320`, and `host_sync=0/4096`; the top shapes were
`mmq ne2=12` (1096), `mmq ne2=18` (480), and `mmvq ne2=8` (360). This refutes
host-sync fallback as the current n128 `MUL_MAT_ID` problem; follow-up work should
target grouped-MMQ small-M kernel partitioning or another measured bucket.

Phase 35 added default-off `LLAMA_MUL_MAT_ROUTE_TRACE=<n>` as patch `0061`
(`/home/mudler/bench/phase35_mul_mat_route_trace/20260701_074359`). Default-off,
trace-enabled, and post-serving gates stayed stable: MoE `8cb0ce23`, dense
`5951a5b4`, `MUL_MAT 1146/1146`, `MUL_MAT_ID 806/806`. Live n128 serving with
trace cap 8192 produced route counts: `mat_f=2888`, `op_cublas=2292`,
`mmq=1328`, `vec_q=1214`, `vec_f=470`. BF16 (`type=30`) dominated the trace
with `mat_f=2485` and `op_cublas=1330`; top BF16 shapes were `mat_f ne1=12`
(775), `op_cublas ne1=18` (760), and `mat_f ne1=8` (570). Next projection work
should trace or optimize the BF16 `op_cublas`/`mat_f` split, not batched cuBLAS.
