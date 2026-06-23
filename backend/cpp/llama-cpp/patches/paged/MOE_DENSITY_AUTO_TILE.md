# Patch 0015 findings: expert-density-aware MoE token-tile auto-select

The durable follow-up to patch 0014 (`MOE_TOKEN_TILE_CAP.md`): replace the blunt,
opt-in `LLAMA_MOE_MMQ_X` global cap with a host-side, **default-on** density-aware
`mmq_x` auto-select in `mul_mat_q_case`. Companion to
`0015-paged-expert-density-aware-moe-token-tile-auto-select.patch`. Dev tree
`~/llama-paged-dev` (branch `paged`), `build-cuda` sm_121.

Primary model: **Qwen3.6-35B-A3B NVFP4** (`~/bench/q36-35b-a3b-nvfp4.gguf`),
**256 experts, top-8**, expert FFN 512, GDN linear attention (SSM inner 4096),
41 layers. This is a different beast from 0014's Qwen3-Coder-30B-A3B (128 experts,
larger expert FFN, standard attention).

## What it does (vs 0014)

`mul_mat_q_case` picks the token-tile width `mmq_x` to cover `ncols_max` (= `ne12`,
the per-expert column upper bound = token count) in one column-tile, i.e. stock
**maximizes** the tile (128 on Blackwell). Applied per expert at MoE decode, where
per-expert density is tiny, that 128-wide tile is mostly padding.

Patch 0014 capped `mmq_x` globally on the ids path via `LLAMA_MOE_MMQ_X` (decode
**and** prefill), which cost ~1.3% prefill. Patch 0015 instead estimates the
per-expert density host-side, from args the ids path already passes:

```
ne_get_rows = ncols_dst   = ne12 * n_expert_used        (token-expert assignments)
n_experts   = nchannels_x = ne02
density     = ceil(ne_get_rows / min(ne_get_rows, n_experts))   (tokens/expert)
```

and caps to the small tile (default 64) **only when `density <= density_max`**, so
the high-density prefill ubatch keeps the big 128 tile. Prefill-safe by construction.
No new kernel: the selection only lowers the loop's upper bound to an
already-compiled, granularity- and shared-memory-validated `mmq_x`.

## The threshold matters: `density_max = 8`, not `tile/4 = 16`

The cap must fire for decode but not for a prefill ubatch. Each has per-expert
density `n_tokens * n_used / n_experts`. At the standard `n_ubatch=512`, `n_used=8`:

```
                       128 experts   256 experts
prefill ubatch (512)        32            16
decode npl128 (128)          8             4
```

`tile/4 = 16` (0014's first auto-select draft default) **equals the 256-expert
prefill density** and caps prefill: measured -2.0% to -2.9% S_PP on q36-35b-a3b.
`density_max = 8` sits strictly between decode and prefill for every `n_experts` in
`[128, 511]`, so it caps decode and leaves prefill on the big tile. This single
default change is what makes the patch prefill-safe on the 256-expert model.

## Measurements (default-on vs stock, median of 5 reps)

`llama-batched-bench`, q36-35b-a3b-nvfp4.gguf, `-fa on -npp 128 -ntg 128`, GB10
sm_121. STOCK = `LLAMA_MOE_AUTO_TILE=0` (exact stock selection); 0015 = default.

```
  npl   S_TG stock  S_TG 0015   dTG%     S_PP stock  S_PP 0015   dPP%
    8      183.59     183.18  -0.22%        1489.2     1500.1  +0.73%
   32      264.02     263.44  -0.22%        2034.5     2033.5  -0.05%
   64      311.76     310.41  -0.43%        2028.3     2027.6  -0.03%
  128      336.10     337.32  +0.36%        2025.0     2027.7  +0.13%
```

Raw npl128 reps: S_TG 0015 `[337.3, 336.9, 336.4, 338.9, 338.1]` vs stock
`[336.2, 336.1, 335.9, 336.9, 335.8]` (distributions overlap); S_PP 0015
`[2028.6, 2023.0, 2024.9, 2028.0, 2027.7]` vs stock `[2024.9, 2025.0, 2023.2,
2029.4, 2029.0]`.

### Honest read: neutral on this model

On q36-35b-a3b the decode delta is **within run-to-run noise** (npl128 +0.36%,
npl<=64 slightly negative) and prefill is **neutral** (within +/-0.7%, well inside
the 1% target). The `+5%` decode target from the localmaxxing reference does **not**
materialize here. q36-35b-a3b decode is bound by the GDN/SSM recurrence and
256-tiny-expert weight bandwidth, not the MoE col-tile occupancy, so the col-tile
lever has nothing to bite on.

### npl128 decode tile sweep confirms 64 is the only useful width

`density_max=8` fixed, varying `LLAMA_MOE_DECODE_TILE`, S_TG @ npl128 vs stock:

```
  TILE8   TILE16  TILE32  TILE64  TILE96
 -6.31%   -3.18%  -0.17%  +0.70%  -0.76%
```

Smaller tiles are **worse**, not better: more column-tiles per expert = more
grid/scheduling overhead, and the FP4-MMA has a minimum efficient width. So matching
the tile to the literal density (4) is counterproductive; 64 is the sweet spot,
same as 0014.

## Why ship it default-on anyway

1. **Removes 0014's prefill cost by construction.** The cap is density-gated, not
   global, so prefill keeps its 128 tile (S_PP neutral above).
2. **Banks the col-tile-bound gain for free.** At npl128 the auto-select picks
   `tile=64` for a 128-expert model (decode density 8 <= 8), i.e. exactly 0014's
   `cap64`, so it reproduces 0014's **+4.8% @npl128 on Qwen3-Coder-30B** without the
   -1.3% prefill cost. (That model was unavailable to re-bench here; the tile choice
   is identical by construction.)
3. **Prefill-safe and decode-neutral on the SSM model**, so it is harmless where it
   does not help.
4. **Correctness-gated** by the P0 harness (below).

## Conservative by design (known limitation)

A pure-density gate cannot separate two cases with the **same** per-expert density:
Qwen3-Coder npl256 decode (density 16) and the 256-expert prefill ubatch (density
16) are identical to the estimator. `density_max=8` therefore **forgoes 0014's
+2.3% @npl256** on the 128-expert model to keep 256-expert prefill safe. Recovering
it needs an `ne12`-aware (absolute token count) gate in addition to density; scoped
as future work, not implemented.

## Knobs

- `LLAMA_MOE_AUTO_TILE=0` : disable the auto-select, exact stock `mmq_x` selection.
- `LLAMA_MOE_MMQ_X=<n>` (patch 0014) : **kept** as a manual override; when > 0 it
  forces the old blunt global cap and bypasses the auto-select (explicit A/B knob).
- `LLAMA_MOE_DECODE_TILE=<n>` : the small tile (default 64).
- `LLAMA_MOE_DENSITY_MAX=<n>` : the density ceiling (default 8).

## P0 correctness gate

`tests/test-backend-ops` `test_mul_mat_id` is extended with a ragged small-M
NVFP4/MXFP4 MoE decode-density block: 128 experts, top-8, m=768, k=2048, n in
`{16,33,64,128,130,200,256,512}` spanning the cap boundary (n>=130 keeps the 128
tile at `density_max=8`, n<=128 takes tile 64) and ragged token counts (experts with
0/1/2 tokens, n not a multiple of the tile). All 16 shapes pass the CUDA-vs-CPU
oracle on GB10 both default-on and with `LLAMA_MOE_AUTO_TILE=0`; full `MUL_MAT_ID`
suite 2/2 backends OK. Off the ids path nothing changes (non-MoE `mul_mat`
byte-identical to stock).

## Verdict

- Correct, prefill-safe, default-on density-aware tile select; the durable design
  0014's own doc scoped. Supersedes 0014's global cap as the default path; the
  `LLAMA_MOE_MMQ_X` knob is retained as a manual override.
- **Net effect on q36-35b-a3b NVFP4: neutral** (decode within noise, prefill neutral)
  because the model is SSM/bandwidth-bound, not col-tile-bound. The lever's real win
  lives on col-tile-bound MoE (Qwen3-Coder-30B, +4.8% @npl128), banked here at zero
  prefill cost.
