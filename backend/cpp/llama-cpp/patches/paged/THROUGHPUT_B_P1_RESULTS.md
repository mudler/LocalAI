# Track B P0 + P1 results: the FP4-MMA decode-GEMM occupancy tune (GB10, sm_121)

Measured on the DGX (GB10 / DGX Spark, sm_121, `~/llama-paged-dev`, branch `paged`). Implements
`FP4_GEMM_SCOPE_B.md` P0 (baseline + bit-exact gate) and P1 (the cheap host/occupancy tile tune).
Dev-tree commit: **089f78d** (`feat(paged): FP4 decode GEMM track-B P0 gate + default-off occupancy
instrumentation`). Patch artifact: `0017-fp4-gemm-decode-tile-tune.patch`.

**Headline verdict: the P1 occupancy kill-gate TRIPPED.** None of the cheap host/occupancy levers
lift dense or MoE decode_agg on GB10; every dense probe regresses and the nsys evidence shows the
FP4 GEMM kernel gets *slower* under register-capping. Nothing is enabled by default (the levers are
compile-time/env gated and the default build is byte-identical to stock). The one untested lever is
the structural `mmq_y`-down, which is **not** a host switch: it is coupled to `nwarps` by the
`nwarps*tile_C::I == mmq_y` static_assert, so it requires an `nwarps=4` warp-remap (P2 kernel work).

All benches: `llama-batched-bench -fa on -c 32768 -ngl 99 -npp 128 -ntg 128 -npl 32,128`.
`decode_agg = S_TG` (aggregate decode tok/s). 3 reps dense, 2 reps MoE; medians below.

## P0 baseline (mmq_y=128, minblocks=1 — stock)

### Bit-exact parity gate (CPU oracle vs CUDA, deterministic)
- `test-backend-ops -o MUL_MAT  -b CUDA0`: **1115/1115** (1103 stock + 12 new NVFP4/MXFP4 dense
  decode-shape cases), NVFP4 0 fail.
- `test-backend-ops -o MUL_MAT_ID -b CUDA0`: **805/805**, NVFP4 0 fail.
- New P0 cases exercise the weight-row (`mmq_y`) tiling boundary: `type_a ∈ {NVFP4, MXFP4}`,
  `m ∈ {2048 (exact at mmq_y 64/128), 1600 (ragged vs 128), 2050 (ragged vs both 64 & 128 →
  need_check last row-tile)}`, `n ∈ {32, 128}` (decode M), `k = 2048`. They make the oracle cover
  the `mmq_y`/min-blocks changes and stay bit-exact with every lever on.

### Decode throughput (decode_agg = S_TG)
| model | npl32 | npl128 |
|---|---:|---:|
| DENSE q36-27b-nvfp4 | 117.3 | **149.5** |
| MoE q36-35b-a3b-nvfp4 (stock mmq_x=128/expert) | 262.6 | **336.3** |

(For reference the scope §6 cites dense 161 / MoE 333 from a server harness; this is the cleaner
batched-bench A/B baseline. The relative P0→P1 deltas below are what the kill-gate turns on.)

### nsys FP4 GEMM efficiency (dense, `-npp 64 -ntg 48 -npl 128`)
The decode FP4 weight GEMM kernel = `mul_mat_q<NVFP4(40), mmq_x=128, need_check=0>`:
- **33.2 %** of GPU kernel time, total **2.782 s** / 4576 inst, **avg 608 µs/launch**.
- Plus `quantize_mmq_nvfp4` 9.1 % (the act-quant bucket — track A's target), `mul_mat_q<…,16,…>`
  5.8 % (prefill ubatch tiling), stream-k fixups ~0.5 %.

This is the locked baseline; P1 must lower the GEMM kernel time (raise FP4-eff) to pass.

## P1 — the cheap occupancy levers (all default-off, byte-identical when off)

Three bit-exact, gated levers were added (`mmq.cuh`):
- `GGML_CUDA_FP4_MMQ_Y` (default 128): type-aware `get_mmq_y_host/device` plumbing for an NVFP4
  weight-row tile override. **Inert** — see "the mmq_y wall" below.
- `GGML_CUDA_FP4_MINBLOCKS` (default 1): NVFP4-only `__launch_bounds__` min-resident-CTAs lever
  (register-caps the FP4-MMA kernel so >1 CTA co-resides). The bounded occupancy probe.
- `GGML_CUDA_FP4_DENSE_MMQ_X` (env, default off): dense col-tile re-read occupancy diagnostic
  (the §4.1 A/B: does eating a 2× weight re-read at a smaller `mmq_x` buy net occupancy?).

P1 parity: with `MINBLOCKS=2` the gate stays **MUL_MAT 1115/1115, MUL_MAT_ID 805/805, NVFP4 0
fail** — register allocation is result-neutral, so bit-exactness holds.

### DENSE decode_agg @ npl128 — every occupancy probe REGRESSES
| config | npl32 | npl128 | Δ vs P0 @npl128 |
|---|---:|---:|---:|
| P0 stock (mmq_y=128, minblocks=1) | 117.3 | **149.5** | — |
| MINBLOCKS=2 (2 resident CTAs via reg-cap) | 115.7 | 147.9 | **−1.1 %** |
| DENSE_MMQ_X=64 (2 col-tiles, 2× weight re-read) | 115.3 | 144.3 | **−3.5 %** |
| DENSE_MMQ_X=32 (4 col-tiles, 4× weight re-read) | 115.4 | 141.7 | **−5.2 %** |

### MoE decode_agg @ npl128 — mmq_x-down regresses; min-blocks neutral
| config | npl32 | npl128 | Δ vs stock @npl128 |
|---|---:|---:|---:|
| stock (mmq_x=128/expert) | 262.6 | **336.3** | — |
| TILE32 | 262.1 | 336.0 | −0.1 % |
| TILE16 | 261.1 | 324.0 | **−3.7 %** |
| TILE8 | 260.8 | 316.6 | **−5.9 %** |
| MINBLOCKS=2 | 260.0 | 337.7 | +0.4 % (noise) |

The MoE result reproduces patch 0015 exactly: q36-35b-a3b (256 tiny experts, GDN linear attention)
decode is GDN/bandwidth-bound, **not** col-tile-occupancy-bound, so tightening `mmq_x` below 64
(the brief's "8–16 ideal") monotonically *loses*. 64 ≈ 32 ≈ stock is the floor.

### nsys kill-gate evidence (the decisive datum)
`mul_mat_q<NVFP4,128,0>` under MINBLOCKS=2: **2.782 s → 3.025 s**, avg **608 µs → 661 µs
(+8.7 % SLOWER)**. The FP4-MMA kernel needs >128 regs/thread; forcing 2 CTAs/SM register-caps it,
which **spills to local memory**, so the GEMM does *more* work per launch — occupancy did not
usefully rise, it inverted. FP4-eff went **down**, not up. Kill-gate tripped, with hard evidence.

## Why P1 can't lift it (and why mmq_y-down is P2, not P1)

The two orthogonal occupancy probes both regress: register-capping (minblocks↑) spills, and
col-tile-shrinking (mmq_x↓) re-reads the 18 GB weight set. This says the **dense M=128 tile is
already weight-read / one-read-optimal at mmq_x=128** — it is not occupancy-starved in a way the
cheap levers can fix. This contradicts the scope's central "self-inflicted occupancy, recover it by
raising resident CTAs" hypothesis *for the cheap levers*.

The only lever that raises resident CTAs **without** spilling and **without** extra weight reads is
the structural `mmq_y`-down (smaller weight-row tile → smaller shared + smaller accumulator → more
CTAs, weights still read once). But `mmq_y` is **rigidly** `nwarps * tile_C::I = 8 * 16 = 128`
(the `mmq.cuh:3258` static_assert; `tile_C::I=16` is the fixed `m16n8k64` MMA shape). So
`mmq_y=64` requires **`nwarps=4`** — a warp-remap, not a host switch. That remap threads `nwarps`
through ~13 NVFP4-reachable sites including the **shared** `vec_dot_fp4_fp4_mma` (used by both NVFP4
and MXFP4) and the loader/kernel nwarps lockstep, with real risk of a silent shared-mem/thread-block
mismatch. It was scoped but **deferred to P2** (the scope's own phase table also places `mmq_y`-down
at P2, after the P1 host-only knobs). The `get_mmq_y` host/device plumbing is committed and inert so
P2 only has to add the `nwarps` half.

## Honest verdict vs the scope targets

- **DENSE:** P1 (host knobs + min-blocks + re-read diagnostic) does **not** move decode_agg toward
  the 391 target — it slightly *regresses* (149.5 → 147.9, 38 % of vLLM). The scope's P1 row
  (~177, "honest: small") was optimistic; on GB10 the cheap levers are net-negative. The remaining
  upside lives entirely in the P2 `mmq_y`-down (nwarps=4) kernel remap **plus** track A. Whether
  that clears the floor is now an *open, unproven* question — the cheap-lever evidence here leans
  *against* large occupancy upside (the tile already looks one-read-optimal), so the P2 ceiling is
  plausibly lower than the scope's 316–328.
- **MoE:** the mmq_x-down lever (the brief's MoE P1) is a **confirmed dead-end on this model**
  (regresses; GDN/BW-bound, reproduces patch 0015). min-blocks is neutral. No host-level MoE win.

**Kill-gate: TRIPPED on both arms.** Per the brief this is *not* forced into a default-on change.
Committed: the P0 bit-exact gate + the default-off instrumentation + this honest record. Not pushed.

## Reproduce
```
# default (byte-identical stock): build-cuda as-is -> MUL_MAT 1115/1115, MUL_MAT_ID 805/805
# occupancy probe:  cmake build with -DGGML_CUDA_FP4_MINBLOCKS=2  (or flip the macro default)
# dense re-read A/B: GGML_CUDA_FP4_DENSE_MMQ_X=64 ./llama-batched-bench -m q36-27b-nvfp4.gguf ...
# nsys: nsys profile --trace cuda ... ; nsys stats --report cuda_gpu_kern_sum  (watch mul_mat_q<40,128,0>)
```
