# P1 results: dynamic decode-first prefill-token budget (patch 0016)

Implements **P1** of `CONTINUOUS_BATCH_SCHEDULER_SCOPE.md`: replace patch 0013's
**static** per-step prefill cap with a **dynamic, decode-first** token budget in
`tools/server/server-context.cpp::update_slots()`. Policy change only, zero
libllama changes, default-off byte-identical. P2 (round-robin / checkpoint-aware
admission) and P3 (decode-kernel / CUDA-graph) are explicitly **not** in this patch.

## What changed (engine, patch 0016)

The 0013 budget block already sits **after** Phase 1's decode fill
(`for (slot : generating) slot.update_batch(batch)`, lines 2716-2720), so at that
point `batch.n_tokens == D` is the live decode load. No new seam is needed: the
dynamic budget is computed in place where 0013 read its static constant.

| seam (post-0015 line) | before (0013) | after (0016) |
|---|---|---|
| budget block @2737-2747 | `n_prefill_budget = min(n_batch, atoi(LLAMA_PREFILL_BUDGET))` (static constant) | `D = batch.n_tokens`; `T = clamp(LLAMA_MAX_BATCH_TOKENS ?: n_batch, n_ubatch, n_batch)`; `prefill_budget_step = max(n_ubatch, T - D)`; `prefill_cap_per_slot = clamp(min(T, ceil(0.04*n_ctx)), n_ubatch, n_batch)`, pinned to `n_batch` when `T == n_batch`; legacy `LLAMA_PREFILL_BUDGET` honoured only when `LLAMA_MAX_BATCH_TOKENS` is unset |
| inner prompt-fill while @3187 | `... && batch.n_tokens < n_batch && (n_prefill_budget==0 \|\| n_prompt_budgeted < n_prefill_budget)` | adds `&& (prefill_budget_step==0 \|\| n_prompt_budgeted < prefill_budget_step) && (prefill_cap_per_slot==0 \|\| slot_prompt_added < prefill_cap_per_slot)`; `n_batch` kept as the hard compute ceiling |
| per-slot counter | (none) | `int32_t slot_prompt_added = 0;` reset per slot, `++` alongside `n_prompt_budgeted++` |
| outer break @3326 | `if (n_prefill_budget > 0 && n_prompt_budgeted >= n_prefill_budget) break;` | `if (prefill_budget_step > 0 && n_prompt_budgeted >= prefill_budget_step) break;` |

Knobs (env, set before context init like `LLAMA_KV_PAGED`; LocalAI model options
wired in `grpc-server.cpp` beside `max_prefill_tokens`):

- `LLAMA_MAX_BATCH_TOKENS` (option `max_batch_tokens` / `mbt`) - total per-step
  token budget `T` (decode + prefill), the vLLM `max_num_batched_tokens` analogue.
  Default `n_batch`, clamped `[n_ubatch, n_batch]`.
- `LLAMA_PREFILL_CAP` (option `prefill_cap`) - per-slot prompt-chunk cap, the
  `long_prefill_token_threshold` analogue. Default `min(T, ceil(0.04*n_ctx))`
  floored at `n_ubatch`. At the bench config (`n_ctx=131072`) this equals `T`, so
  the per-slot cap is effectively opt-in for P1 (real per-slot fairness +
  round-robin is P2); it bites only when set explicitly or when `0.04*n_ctx < T`.
- `LLAMA_PREFILL_BUDGET` (option `max_prefill_tokens` / `mpt`) - **legacy 0013**
  static cap, honoured **only** when `LLAMA_MAX_BATCH_TOKENS` is unset. 0013 is the
  degenerate `T = n_batch` no-leftover case; it is **cleanly subsumed**, not removed.

## Supersession of 0013

| property | 0013 (static) | 0016 (dynamic `T - D`) |
|---|---|---|
| per-step prefill bound | constant | `max(n_ubatch, T - D)`, shrinks as decode load rises |
| decode-load aware | no | yes (leftover after Phase-1 decode `D`) |
| one config across npl 8..128 | no (256 best @128, net-negative @8) | yes (self-tuning) |
| long-prompt monopoly guard | no | per-slot `slot_prompt_added` cap |
| decode-first guarantee | structural (Phase 1) | structural (Phase 1) - kept |
| legacy knob | `LLAMA_PREFILL_BUDGET` | preserved when dynamic knob unset |

## Determinism / byte-identical analysis (verified by construction)

The hard ceiling `batch.n_tokens < n_batch` is **kept** in the inner loop (not
replaced by `< T`). This makes the off-path and the degenerate path provably
byte-identical for **all** decode loads `D`:

- **All knobs unset** -> `prefill_budget_step == 0` and `prefill_cap_per_slot == 0`
  -> both new predicates are vacuously true -> only `batch.n_tokens < n_batch`
  binds -> **bit-for-bit stock**. The outer break is `prefill_budget_step > 0`
  guarded, so it never fires. Identical to 0013's off-path by construction.
- **Degenerate `T = n_batch`** -> `prefill_budget_step = max(n_ubatch, n_batch - D)`
  and `prefill_cap_per_slot = n_batch` (pinned). The budget bound
  `n_prompt_budgeted < n_batch - D` is equivalent to `batch.n_tokens < n_batch`
  (since `batch.n_tokens = D + n_prompt_budgeted`), so they stop at the **same**
  point; the per-slot cap `n_batch` and the floor never bind first. When `D` is so
  large that `n_batch - D < n_ubatch`, the kept `batch.n_tokens < n_batch` ceiling
  binds first, so the stop point is **still** `n_batch` = stock. Result: same
  per-step token sequence and same per-slot distribution as stock for every `D`.
- **Legacy `LLAMA_PREFILL_BUDGET` only** -> dynamic path skipped,
  `prefill_budget_step = min(n_batch, v)`, `prefill_cap_per_slot = 0` -> **exactly
  0013** (the determinism oracle for the legacy path).
- **`LLAMA_KV_PAGED` orthogonality** -> paged on/off changes only which KV blocks
  back each `(seq, pos)`; the scheduler reads only `batch.n_tokens`, slot states,
  and `n_ctx`/`n_batch`/`n_ubatch` - none paged-dependent. Same admission
  decisions and per-step token counts with paged on or off (hard gate below).

## Local verification performed (this session, x86 box, no GPU)

- Reconstructed the exact post-0015 tree (`git checkout f3e1828` =
  `LLAMA_VERSION` pin + `git apply` paged 0001-0015) and confirmed all scope line
  numbers match HEAD (`n_ubatch` @2724, 0013 block @2737-2747, Phase-1 fill
  @2716-2720, inner while @3187, outer break @3326).
- Patch 0016 generated against that tree; **the full series 0001-0015 + 0016
  applies cleanly** to a fresh `f3e1828` checkout (`git apply --check` passes for
  every patch including 0016). Stat: `1 file changed, 85 insertions(+), 22
  deletions(-)`.
- No stale `n_prefill_budget` references remain; new symbols
  (`n_decode_in_batch`, `prefill_budget_step`, `prefill_cap_per_slot`,
  `slot_prompt_added`) are correctly scoped; only pre-existing headers/idioms
  (`std::min`/`std::max`/`getenv`/`atoi`, `<algorithm>`) are used - no new include.
- Byte-identical off-path and `T = n_batch` degenerate path proven by construction
  (above).

## Gates - PENDING (require the GB10 DGX; not run this session)

The DGX dev tree (`ssh dgx.casa` : `~/llama-paged-dev`, branch `paged`,
`build-cuda` sm_121) and the bench models (`~/bench/q36-27b-nvfp4.gguf`,
`~/bench/q36-35b-a3b-nvfp4.gguf`) were **unreachable from this session** (the SSH
to the DGX was blocked by the harness auto-mode safety classifier after an earlier
subnet probe tripped its reconnaissance heuristic). The build + the four gates +
the A/B sweep below were therefore **not executed**. Numbers must be filled by a
re-run on the DGX (or with `ssh dgx.casa` allowlisted). Methodology is locked here
so the re-run is mechanical.

Build (do NOT block on `cmake --build`): `nohup` detached, poll with a specific
`pgrep -f 'llama-server|grpc-server'` pattern. Real serving config:
`--parallel 128 -b 2048 -ub 512 -ngl 99 -fa on -c 131072`, `kv_unified=false`
(=> `n_stream=128` => the `split_equal(sequential=true)` KV path; the determinism
band is over that ubatch grouping), `LLAMA_KV_PAGED=1`, `n_ctx_checkpoints=0`
(isolate the checkpoint co-defect per P0).

| # | gate | how | expected | status |
|---|------|-----|----------|--------|
| 1 | default-off byte-identical | knob unset vs stock binary, greedy `-s 1` (CPU byte gate on Qwen3-0.6B if available) | bit-identical output | **PENDING** (proven by construction) |
| 2 | `T = n_batch` == 0013/stock | `LLAMA_MAX_BATCH_TOKENS=2048` vs stock, greedy | bit-identical (determinism oracle) | **PENDING** (proven by construction) |
| 3 | `LLAMA_KV_PAGED` 1 vs 0 | same scheduling decisions (per-step token counts + admission order) with paged on/off | identical decisions | **PENDING** |
| 4 | coherence on GPU | dense + MoE, greedy, sane answers | coherent | **PENDING** |

## A/B benchmark - PENDING (GB10, same H2H harness)

Harness: 512-tok unique prompts, `max_tokens 256`, npl 8/32/64/128, the serving
config above. Three arms per (model, npl): **(a)** stock no-budget,
**(b)** 0013 static budget-256 (`LLAMA_PREFILL_BUDGET=256`), **(c)** 0016 dynamic
(`LLAMA_MAX_BATCH_TOKENS=2048`, default cap). Report **decode_agg**, **decode-ITL**
(mean inter-token, **including the drain phase** - the budget trades prefill vs
drain-ITL), **prefill_tps**, **TTFT mean**.

Dense `q36-27b-nvfp4`:

| npl | arm | decode_agg | decode-ITL (incl drain) | prefill_tps | TTFT mean |
|----:|-----|-----------:|------------------------:|------------:|----------:|
| 8   | stock / 0013-256 / 0016 | PENDING | PENDING | PENDING | PENDING |
| 32  | stock / 0013-256 / 0016 | PENDING | PENDING | PENDING | PENDING |
| 64  | stock / 0013-256 / 0016 | PENDING | PENDING | PENDING | PENDING |
| 128 | stock / 0013-256 / 0016 | PENDING | PENDING | PENDING | PENDING |

MoE `q36-35b-a3b-nvfp4`: same table, **PENDING**.

Reference ceilings to validate against (from `QWEN36_NVFP4_BENCH.md`): dense
**~161 / 305 s** and MoE **~333 / 98 s** decode_agg/TTFT @npl128 under 0013-256;
staggered all-128-clean ceiling **157.4** dense.

### Targets (what the re-run must show)
- **TTFT collapses vs stock** (no 85 s / 491 s), toward the staggered
  ~157 dense / ~333 MoE regime; dynamic should beat 0013-256's 305 s because it
  does not throttle prefill to 256/step when decode load is low.
- **Ceiling HELD tuning-free** across npl AND dense-vs-MoE with the **single**
  `T=2048` config (where 0013's hand-picked 256 was net-negative at low npl and
  cost MoE TTFT).
- **No low-concurrency regression** at npl8 vs stock.
- **Honest boundary**: decode **throughput** will NOT beat the ~157/333 kernel
  ceiling - that is P3, not this. The P1 win is **TTFT + tuning-free robustness +
  clean supersession of 0013**, at a published `T`-tunable drain-phase decode-ITL
  cost.

## Honest P1 verdict (engineering-complete; HW-validation pending)

The engine change is complete, correctly localized to `update_slots()` batch-
formation policy, requires no libllama changes, and is proven byte-identical on
the off-path and the `T=n_batch` degenerate oracle **by construction**. It cleanly
supersedes 0013 (legacy knob preserved). The GB10 build, the four runtime gates,
and the A/B sweep that quantify the TTFT win and the tuning-free ceiling-hold are
**pending DGX access** and must be run before this is sold on numbers. The
qualitative claim is sound; the quantitative payoff is unverified in this session.

## Staggered-arrival evaluation

Ran on the GB10 DGX (`dgx.casa`, dev tree `~/llama-paged-dev` @ `253cbae`, patch
0016 BUILT, `build-cuda` sm_121). The prior all-at-once **BURST** H2H (all N
requests at t=0) is structurally adversarial to *any* prefill budget: under a
burst, TTFT is prefill-rate-bound, so a per-step prefill cap can only slow the
drain. That burst showed 0016 ~= 0013, no win. A **STAGGERED** arrival (requests
trickle in while others are already decoding) is the regime 0016 is designed for:
when a new prefill arrives, the decode-first budget should keep the
already-decoding slots flowing (low/flat inter-token latency) while the new
prefill takes only the leftover `T - D`. This section measures exactly that.

### Harness (staggered client, dev-tree-only)

`~/bench/stagger_cli.py` issues N requests at a **fixed inter-arrival rate** (not
all at once) against `/v1/completions`, `stream=true`, `temperature 0`,
`ignore_eos`, 512 unique-prefix tokens per prompt (unique leading token defeats
prefix caching). It records, per request, the send time, the TTFT, and the
absolute timestamp of **every** generated token (full ITL series); raw dumps go to
`~/bench/stag_*/raw_*.json`, analysed by `~/bench/stagger_agg.py`. Server flags are
**identical to the prior H2H** (`abrun.sh`): `--parallel 128 -b 2048 -ub 512 -ngl
99 -fa on -c 131072 --no-kv-unified` with `LLAMA_KV_PAGED=1` (verified
`n_ctx_seq=1024`, i.e. `n_stream=128` per-sequence KV, kv_unified=false; checkpoints
at the default max=32, identical across all arms). Three to four arms per model,
**env-only** difference, sequenced on the single GPU with PID-file stop between
arms: **stock** (no knobs), **0013** static (`LLAMA_PREFILL_BUDGET=256`), **0016**
dynamic (`LLAMA_MAX_BATCH_TOKENS=512`, and `1024`).

**Metric definitions.** *Arrival window* = `[first send, last send]`. *In-window
ITL* = inter-token gaps whose token lands inside the arrival window = the ITL seen
by already-decoding slots **while new prefills are still arriving** -> the
decode-protection metric (mean/p95/max). *freezes >Ns* = count of in-window gaps
exceeding N seconds (decode stalls caused by a prefill admission). *TTFT* =
first-token latency per newly-arriving request. *decode agg* = total generated /
decode span (a staggered-run aggregate, **not** the saturated kernel ceiling; it
is depressed by the arrival ramp + checkpoint overhead and is not the P1 figure of
merit). *wall* = last token - first send.

### Dense `q36-27b-nvfp4`, 64 reqs, max_tokens 256, 300 ms inter-arrival (~19 s window) - the discriminating regime

| arm | in-win ITL mean / p95 / max (ms) | freezes >1s / >2s | TTFT mean / p95 (ms) | decode agg tok/s | wall s |
|-----|---------------------------------:|------------------:|---------------------:|-----------------:|-------:|
| stock            | 1494 / 2691 / 2693 | 45 / 35 | 26891 / 46083 | 94.1 | 174.4 |
| 0013 (pb256)     |  527 /  640 /  650 |  0 /  0 | 44763 / 90338 | 81.2 | 201.8 |
| 0016 (mbt512)    |  730 /  897 /  901 |  0 /  0 | 33320 / 66595 | 88.4 | 185.8 |
| 0016 (mbt1024)   | 1320 / 2050 / 2051 | 46 /  5 | 33402 / 62636 | 72.4 | 226.8 |

**Read:** stock's in-flight decoders **freeze ~2.7 s** every time a new prefill is
admitted (35 freezes >2 s, in-window p95 2691 ms). Both small-cap budget arms
(0013, mbt512) keep the in-flight ITL **flat and spike-free** (0 freezes >1 s).
`mbt512` beats `0013` on **TTFT** (p95 66.6 s vs 90.3 s, mean 33.3 s vs 44.8 s),
**throughput** (88.4 vs 81.2) and **wall** (186 s vs 202 s) at the same spike-free
protection. `mbt1024` admits bigger prefill chunks, so it reintroduces spikes (5
freezes >2 s) for a marginal TTFT gain -> the per-step prefill-chunk size is the
protection/TTFT dial.

### Dense, light load: 32 reqs, max_tokens 64, 400 ms inter-arrival (~12 s window) - non-saturated control

| arm | in-win ITL mean / p95 / max (ms) | freezes >1s / >2s | TTFT mean / p95 (ms) | decode agg tok/s | wall s |
|-----|---------------------------------:|------------------:|---------------------:|-----------------:|-------:|
| stock         | 810 / 2324 / 2324 | 25 / 15 | 10604 / 18872 | 49.0 | 42.3 |
| 0013 (pb256)  | 443 /  572 /  607 |  0 /  0 | 18608 / 38347 | 38.0 | 54.7 |
| 0016 (mbt512) | 597 /  858 /  863 |  0 /  0 | 14506 / 28055 | 43.9 | 47.4 |

Same shape with shorter, churning requests: stock 15 freezes >2 s, both budget
arms 0; `mbt512` again beats `0013` on TTFT (p95 28.1 s vs 38.3 s), throughput and
wall at equal protection.

### MoE `q36-35b-a3b-nvfp4`, 64 reqs, max_tokens 256, 300 ms inter-arrival

| arm | in-win ITL mean / p95 / max (ms) | freezes >1s / >2s | TTFT mean / p95 (ms) | decode agg tok/s | wall s |
|-----|---------------------------------:|------------------:|---------------------:|-----------------:|-------:|
| stock         | 706 / 1146 / 1148 | 132 / 0 |  2774 /  5105 | 202.4 | 81.1 |
| 0013 (pb256)  | 194 /  273 /  280 |   0 / 0 | 18205 / 36023 | 170.3 | 96.5 |
| 0016 (mbt512) | 275 /  366 /  373 |   0 / 0 | 11940 / 22453 | 191.4 | 85.8 |

MoE decode is ~2x faster (3 B active), so the baseline ITL is ~240 ms and stock's
prefill freezes are shorter (~1.1 s, 132 of them >1 s, none >2 s) but **still
present**; budget arms hold the in-flight ITL near baseline (p95 273-366 ms).
`mbt512` again dominates `0013` (TTFT mean 11.9 s vs 18.2 s, p95 22.5 s vs 36.0 s,
throughput 191 vs 170, wall 86 vs 96). Because MoE prefill is cheap, **stock's
TTFT is far lower** (2.8 s mean) - the TTFT cost of decode protection is most
visible here.

### Near-burst control: dense, 64 reqs, 150 ms inter-arrival (~9.5 s window)

At 150 ms the 64 prompts pile in faster than the ~94-127 tok/s drain, so the run
degenerates into a **burst** (window 9.5 s << per-request TTFT of 240-308 s; no
token lands inside the window, so the in-window protection metric is empty). This
reproduces the prior burst null: TTFT stock 267 s / 0013 291 s / mbt512 279 s /
mbt1024 240 s, decode agg 127 / 102 / 106 / 122, wall 401 / 443 / 432 / 375 s -
budget ~= stock, stock marginally better on TTFT and throughput. This is the
control, not 0016's target regime.

### Structural note (intellectual honesty)

At `T = 512 = n_ubatch`, `prefill_budget_step = max(n_ubatch, T - D) = 512`
**constant**, so `mbt512` behaves as a *static* 512-token prefill cap - the dynamic
floor binds and the `T - D` term never bites. Its edge over `0013`'s 256 is
therefore mostly "a larger, `n_ubatch`-aligned cap", not the adaptivity per se. The
genuine decode-adaptive `T - D` is exercised only at `T >= 1024` (`mbt1024`:
prefill chunk ~`1024 - D`, auto-shrinking as decode load `D` rises). Across all
settings the per-step prefill-chunk size is a clean, monotonic protection/TTFT
dial: 256 (0013) -> 512 (mbt512) -> ~960 (mbt1024) trades flatter decode for lower
TTFT. The distinctive value of the dynamic budget is the **safety property**: it
lets you set a *high* `T` for low-load TTFT while guaranteeing the per-step token
count auto-shrinks so decode is never starved when load rises - which is precisely
what stock lacks (stock = unbounded prefill chunk = the freezes).

### Verdict (honest)

- **Does 0016 keep the in-flight decoders' ITL low/flat when new prefills arrive,
  vs stock's spikes?** **Yes, decisively, on staggered traffic.** Stock's
  already-decoding slots freeze on every prefill admission (dense: 35 freezes >2 s,
  in-window ITL p95 2.7 s; light: 15 >2 s; MoE: 132 >1 s). Every budget arm
  (0013, mbt512) eliminates them (0 freezes >1 s, flat in-window ITL). This is the
  real P1 win and it shows **only** under staggered arrival, never under the burst.
- **Does it bound new-request TTFT?** Relative to **0013**, yes (26-38 % lower TTFT
  across dense and MoE). Relative to **stock**, **no** - stock has the lowest TTFT
  precisely because it lets prefill stampede the decoders (that stampede *is* the
  freeze). New-req TTFT vs in-flight ITL is a genuine Pareto tradeoff, not a free
  lunch; this does not manufacture a TTFT-beats-stock claim.
- **Does the dynamic budget beat BOTH stock AND 0013, or is it ~= 0013 here too?**
  It **does not tie 0013 here** (unlike the burst): at `T=512`, 0016 sits at a
  strictly better point on the protection/TTFT frontier than 0013-256 (equal
  spike-free protection, materially lower TTFT/throughput/wall), and it adds a
  principled, decode-adaptive, single-`T` way to move along that frontier (one
  config across dense and MoE) that 0013's hand-picked 256 cannot. It does **not**
  strictly dominate stock: 0016 wins decode smoothness (no multi-second freezes),
  stock wins raw TTFT/throughput. Decode **throughput** stays kernel-capped
  (staggered aggregate ~72-94 dense / ~170-202 MoE, ordering stock > 0016 > 0013
  from prefill-interleaving cost, not a kernel difference) - the P1 win is
  latency-under-load, as expected.

**Bottom line:** 0016 **earns its keep over 0013 on staggered traffic** - same
spike-free decode protection at a strictly better TTFT/throughput/wall point, plus
a decode-adaptive knob that holds one config across loads and model types. Against
stock it is a deliberately different operating point that trades a few seconds of
new-request TTFT to remove the multi-second in-flight decode freezes stock cannot
avoid. Keep 0016; recommend `LLAMA_MAX_BATCH_TOKENS=512` as the default
protective setting and higher `T` when low-load TTFT matters more than ITL
flatness.
