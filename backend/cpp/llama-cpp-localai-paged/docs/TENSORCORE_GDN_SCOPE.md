# TENSORCORE_GDN_SCOPE - tensor-core chunked gated-DeltaNet prefill (design only)

**Status: DESIGN + SCOPE ONLY. No kernel written, no GPU run, no PTX in this pass.**
This scopes the follow-up recorded by patch 0031 and README section 5: a
tensor-core (`mma`) chunked gated-DeltaNet (GDN) prefill kernel - the path that
would actually *beat* the tuned sequential scan and close the GDN prefill bucket
toward vLLM. vLLM's chunked GDN scan was measured ~2.5x cheaper in the prefill
ground-truth precisely because it pushes the intra-chunk products through
tensor-core matmuls; patch 0031 proved the chunking math but, with serial
per-thread reductions at the GB10-forced `C=16`, came out ~22% *slower* than the
sequential recurrence. This document scopes replacing those reductions with
`mma.sync` matmuls and lifting the occupancy ceiling.

> **Read patch 0031 + README section 5 first.** The bounded/stable de-gating form
> (pairwise decays `d <= 1`, `gamma <= 1`), the per-path bit-exact precedent, and
> the honest negative ("C=16 all-shared -> 1 block/SM -> serial reductions -> 22%
> slower, grid-starved at low n_seqs") are the starting point. This doc does not
> re-derive the algebra; it maps it onto tensor cores.

> **Regime note (the mechanism, read this).** The sequential scan is
> **bandwidth-bound**: it re-streams the entire `128x128` f32 state (64KB) once
> *per token*. README section 5 already records it runs at ~84.7% of GB10 peak BW
> (decode) and the recurrence is a llama *win* vs vLLM's BW. So a tensor-core
> kernel does **not** win by doing the same work faster - it wins by **changing
> the work**: chunking by `C` reads/writes the state `n_tokens/C` times instead of
> `n_tokens` (a ~`C`x cut in state traffic, the dominant prefill GDN cost), and the
> price is `O(C^2)` extra intra-chunk dot-products per chunk. The naive 0031 paid
> that price in serial f32 reductions, which cost *more* than the BW it saved -
> hence 22% slower. **Tensor cores make the added intra-chunk flops nearly free,
> so the BW saving becomes a net win.** That is exactly why vLLM's chunked scan is
> 2.5x cheaper. The whole lever rests on this trade; if a GPU re-profile shows
> prefill GDN is *not* state-BW-bound, stop and re-scope (step 0 below).

---

## 1. GB10 tensor-core reality (sm_121a) - confirmed, not assumed

GB10 / DGX Spark reports **compute capability 12.1 (sm_121)**, CUDA 13 (README
section "Hardware: GB10 / DGX Spark (CUDA 13, sm_121)"). sm_121a is **consumer
Blackwell** (the SM12x family, same tensor-core programming model as RTX 50 /
sm_120), **not** data-center Blackwell (sm_100a / GB200). This distinction is the
single most important input to the design and is confirmed from sources, not
assumed:

- **No `wgmma`.** Warp-group MMA is Hopper (sm_90a) only; targeting SM12x yields
  `ptxas error: Instruction 'wgmma.fence' not supported on .target 'sm_120'`.
  Do **not** design around Hopper-style warp-group MMA.
- **No `tcgen05` / no TMEM.** SM12x lacks the Tensor Memory hardware entirely, so
  the autonomous 5th-gen tensor-core path (`tcgen05.mma`, the sm_100a data-center
  instruction) is unavailable. This is the same wall that makes vLLM/CUTLASS fall
  back to Marlin and gate FP4 to sm_100a on GB10 (tracked in CUTLASS #2800/#2947,
  vLLM #43906). We cannot use it either.
- **What sm_121a DOES have: extended `mma.sync`.** The Ampere/Ada warp-level
  `mma.sync` family, extended with the Blackwell numeric formats (FP8/FP6/FP4).
  "Consumer Blackwell put new data types on top of the oldest programming model."
  For our operands (q/k/v/state are f32 in the op, see below) the usable tiles are
  the standard warp-level ones:
  - **bf16/f16 inputs, f32 accumulate:** `mma.sync.aligned.m16n8k16` (and
    `m16n8k8`). 7-bit (bf16) / 10-bit (f16) input mantissa.
  - **tf32 inputs, f32 accumulate:** `m16n8k8` / `m16n8k4`. 10-bit input mantissa
    - the **highest-precision tensor-core option** on this part, and the one this
    design defaults to (the GDN is decay-sensitive; see section 4).
  - FP8 (`m16n8k32`) / FP4 (`m16n8k64.kind::mxf4nvf4`, block-scaled) compile on
    sm_121a but are **out of scope** here - the GDN q/k/v/state are not 4/8-bit.
- **`cp.async` is available** (Ampere+), so global->shared double-buffering of the
  K/Q chunk tiles is on the table for the occupancy phase. There is **no TMA** on
  SM12x; staging is plain `cp.async`, not `cp.async.bulk`.

**Reuse, do not hand-roll PTX.** ggml already ships a warp-level MMA tile
abstraction at `ggml/src/ggml-cuda/mma.cuh` (the `tile<M,N,T>` fragments +
`mma()` used by the FlashAttention-mma and MMQ kernels), and it already routes
through `turing_mma_available(cc)` / `ampere_mma_available(cc)` - i.e. it is
sm_121-correct today. Build the GDN matmuls on that API (bf16/half/tf32 fragments,
f32 accumulators), not on raw `asm volatile("mma.sync...")`. This de-risks the
kernel and keeps it consistent with the backend's other tensor-core paths.

**Bottom line for the design:** the kernel is a **warp-synchronous `mma.sync`**
kernel (Ampere-class programming model with Blackwell silicon), *not* a
warp-group / TMA / tcgen05 kernel. Every "wgmma"/"tcgen05" idea from FLA's
sm_90/sm_100 kernels must be down-translated to `mma.sync` + `cp.async`. Patch
0031's and README's shorthand "mma/wgmma" should be read as **mma only** on this
part.

---

## 2. Mapping the chunked GDN matmuls onto `mma.sync`

The chunked gated-delta-rule (patch 0031 header) has six dot-product families.
Five are plain matmuls and map cleanly to `mma`; the sixth (the A-inverse) is a
unit-lower-triangular solve and is the one subtle case. Notation: `C` = chunk
length, `dk = dv = S_v = 128` (GDN head dim), per `(head, seq)` block.

| # | Product (0031 step) | Shape | mma form | Notes |
|---|---|---|---|---|
| 1 | `KK[t,t'] = k_t . k_t'` (for `A`) | `C x C` over `k=dk=128` | `(C x dk) x (dk x C)` | Gram matrix; only strict-lower triangle used. Decay `d(t',t)` + `beta_t` applied **after** mma in f32. |
| 2 | `QK[t,t'] = q_t . k_t'` (for `P`/`O`) | `C x C` over `k=dk` | `(C x dk) x (dk x C)` | Lower triangle (`t' <= t`); decay applied after in f32. |
| 3 | `KS[t,j] = (S0^T k_t)[j]` | `C x dv` over `k=dk` | `(C x dk) x (dk x dv)` | `S0` is the chunk-entry state (stationary operand). Feeds RHS of the solve. |
| 4 | `QS[t,j] = (S0^T q_t)[j]` | `C x dv` over `k=dk` | `(C x dk) x (dk x dv)` | The `gamma_t` cross-chunk term of `O`. |
| 5 | `O += P . U` | `C x dv` over `k=C` | `(C x C) x (C x dv)` | `P` (decay-masked `QK`) times the solved `U`. |
| 6 | `S_C += K^T (D .* U)` | `dk x dv` over `k=C` | `(dk x C) x (C x dv)` | The state update; `D` = `diag(d(t,last))` applied to `U` in f32 first. |
| 7 | `U = A^{-1} RHS` | `C x C` solve, `C x dv` RHS | blocked fwd-subst (see below) | The only non-GEMM. |

**Critical precision invariant (preserve the bounded de-gating).** Every decay
(`gamma_t`, `d(t',t) = exp(cs_t - cs_t')`) and every `beta_t` stays in **f32** and
is applied as an elementwise scale **before/after** the mma, never inside it. The
mma only ever multiplies the raw, unweighted dot-products (`k.k`, `q.k`,
`S0^T k`, `S0^T q`, `P.U`, `K^T U`). This keeps the strong-decay underflow-to-zero
behaviour (the adversarial `g in [-20, -1e-4]` op test) exactly as 0031 has it -
the numerically delicate part never touches reduced precision. This is the
discipline that makes a tf32/bf16 mma kernel safe for a decay-sensitive op.

### The A-inverse (step 7) - it CAN be tensor-core'd

`A = I + N`, `N = tril(beta_t d(t',t) k_t.k_t', -1)` is **strictly lower
triangular**, hence **nilpotent** (`N^C = 0`). Two routes, both better than 0031's
serial per-thread forward substitution:

- **Blocked forward substitution (RECOMMENDED, this is the FLA "UT transform").**
  Partition `C` into sub-blocks of `b` (e.g. `b = 16`, one mma `m`-tile). Invert
  each `b x b` diagonal block in registers (it is unit-lower-triangular `b x b`,
  cheap: a short serial solve or the finite Neumann series on a `b`-nilpotent,
  `<= b-1` terms), then propagate to the off-diagonal sub-blocks with **mma**
  (the inter-block coupling `U_i -= A_ij U_j` is exactly a `(b x b) x (b x dv)`
  matmul). For `C = 64, b = 16` that is 4 tiny in-register diagonal solves + a
  triangular sweep of mma updates - the bulk of the solve is on tensor cores, only
  the `16 x 16` diagonals stay scalar.
- **Neumann/Newton-Schulz inverse (fallback).** `A^{-1} = I - N + N^2 - ... ` is
  finite (`C` terms) but `O(C)` mma's of `C x C`; Newton-Schulz
  (`X <- X(2I - AX)`) converges in `~log2(C)` steps for the nilpotent part. Cheap
  in flops, but more numerically exposed than blocked subst for adversarial decays.
  Keep as a fallback if blocked subst's register pressure hurts occupancy.

Verdict: **blocked forward substitution** - it keeps the sensitive diagonal solve
exact-in-registers and tensor-core's only the well-conditioned off-diagonal
coupling. This is precisely the structure FLA/vLLM use, down-translated to `mma`.

### Tile/chunk design that fits the 99KB shared budget AND feeds the mma

The 0031 failure was a layout failure: the all-shared `128x128` f32 state (64KB)
crowded out everything and forced `C=16`. The fix is to get the state **out of the
bulk shared footprint**. Two complementary mechanisms:

1. **State register-resident across the chunk loop (the key move).** `S` only
   participates at chunk boundaries (steps 3,4 at entry; step 6 at exit). Keep it
   as **mma accumulator fragments distributed across the block's warps** (each
   warp owns a `dk x dv` sub-tile of `S`), persisting in registers across the
   sequential chunk loop. Steps 3/4 read `S` as the stationary mma operand; step 6
   accumulates into it. This **frees the entire 64KB** - shared then holds only the
   per-chunk K/Q/U/A tiles. (The chunked algorithm's whole point is that the heavy
   work is intra-chunk and state-free, so the state need not be in shared.)
2. **dv-slab tiling for occupancy (the secondary move).** If register pressure
   from a register-resident `128x128` state caps the kernel at 1 block/SM (likely
   - that is a lot of accumulator registers), split the `dv=128` value dimension
   into slabs (`dv_tile in {64, 32}`). Each warp-group owns a `128 x dv_tile`
   state slab. `A` and the solve depend only on `K` (not `dv`), so they are
   computed once and the `C x C` `A^{-1}` is **broadcast/recomputed** per slab
   (cheap once it is mma'd). This shrinks per-block register/shared pressure and is
   the lever for >1 block/SM.

**Shared budget at `C = 64` (state register-resident), staging K/Q as bf16/tf32:**

| Buffer | Elems | Bytes |
|---|---|---|
| `Kc` (chunk K) | `C x dk = 64x128` | 16KB (bf16) |
| `Qc` (chunk Q) | `C x dk` | 16KB (bf16) |
| `Uc` (solved U) | `C x dv = 64x128` | 32KB (f32 for the solve) / 16KB (bf16 for the P.U + K^T U mma) |
| `A`/`P` scratch | `C x C = 64x64` | 16KB (f32) |
| gates `cs/gam/beta` | `~3C` | <1KB |
| **state** | (registers) | **0KB shared** |
| **Total** | | **~64-80KB** (under the 99KB opt-in) |

So **`C = 64` fits the 99KB budget once the state is register-resident** - 4x the
0031 chunk, and a natural multiple of the `m16n8k*` tiles. For >1 block/SM, drop
to `C = 32` + bf16-staged U (`8 + 8 + 16 + 4 = 36KB`, two blocks fit under the
~49.5KB/block needed) and/or dv-slab the state. **Recommended default: `C = 64`,
tf32 mma, state register-resident** (maximize the BW-saving `C` first; chase the
second block/SM only if the bench says occupancy, not BW, is the residual).

---

## 3. Occupancy plan (break the 1 block/SM ceiling)

0031 is pinned to 1 block/SM by the 64KB shared state. The plan, in priority order:

1. **Free the 64KB: state register-resident** (section 2). This alone may not give
   2 blocks/SM (the register-distributed `128x128` f32 accumulator is heavy), but
   it is the precondition for everything and it lets `C` grow to 64 - which is the
   dominant win (`C`x less state BW). Even at 1 block/SM, `C=64` + mma should flip
   the sign vs 0031.
2. **dv-slab the state** (`dv_tile = 64` then `32`): halve/quarter the per-block
   accumulator-register and shared pressure to admit a 2nd resident block, at the
   cost of recomputing the `C x C` `A^{-1}` per slab (cheap on mma). This is the
   primary occupancy lever once (1) is in.
3. **`cp.async` double-buffer the K/Q chunk loads**: overlap the next chunk's
   global->shared staging with the current chunk's mma, hiding LPDDR5x latency that
   1-2 blocks/SM cannot. No TMA on sm_121, so plain `cp.async` (`commit_group` /
   `wait_group`), Ampere-style.
4. **Grid starvation at low `n_seqs`** (0031's other failure: grid is `H x n_seqs`,
   ~few hundred blocks): the larger `C` reduces per-block serial chunk steps, and
   dv-slabbing **multiplies the grid by the slab count** (`H x n_seqs x n_slabs`),
   directly mitigating the low-`n_seqs` starvation that hurt 0031.

Honest occupancy caveat: a register-resident `128x128` f32 state is a large
register commitment; the realistic outcome is **1-2 blocks/SM**, not high
occupancy. The design leans on **mma throughput + cp.async latency hiding + the
`C`x BW cut**, not on many resident blocks, to win. If profiling shows the kernel
register-capped at 1 block/SM *and* tensor-core-active-% still low, that is the
signal to dv-slab harder (smaller `dv_tile`) or accept the achieved win.

---

## 4. Bit-exactness + precision risk

This is a **NEW FP path on top of a NEW FP path**. 0031 is already not byte-equal
to the sequential recurrence (different reduction order; README s5 records it as a
benign per-path result). Adding tf32/bf16 mma is a *further* reduced-precision
step. Gate it exactly like the backend's other new-FP-path precedents
(`PAGED_BITEXACT_NOTE.md`, the paged-MoE `8cb0ce23`, the PREFILL_GEMM scope):

- **Greedy md5 stability** on the standard prompt (README s5 harness) - to catch
  *unexpected* divergence on the non-prefill paths (decode must stay on the tuned
  sequential kernel and byte-match its reference; this lever is prefill-only and
  opt-in, so the default path is untouched).
- **`test-backend-ops GATED_DELTA_NET`** at the 0031 prefill shapes (the
  `S_v=128` exact-multiple / tail / multi-seq / GQA / permuted cases), CUDA0 vs the
  CPU f32 oracle. **Honest expectation: bf16 mma will likely NOT clear the 1e-7
  NMSE gate; tf32 is borderline.** So the binding gate is the **KL-gate**, not
  strict NMSE: require `KLD(tensorcore || f16) <= KLD(sequential || f16)` and PPL
  within the established band, recorded in `PAGED_BITEXACT_NOTE.md`. tf32 (10-bit
  mantissa, f32 accumulate) is the precision default precisely to give the KL-gate
  the best chance.
- **Precision fallback ladder if tf32 fails the KL-gate:** (i) **3xtf32**
  emulation (split each f32 operand into 3 tf32 limbs, 3 mma's, recombine - the
  CUTLASS fp32-emulation trick; near-f32 accuracy at 3x the mma cost, still far
  cheaper than serial f32 loops and still a likely net win given the `C`x BW cut);
  (ii) keep the **decay-coupled and state-boundary products in 3xtf32/f32** while
  the well-conditioned intra-chunk Gram products use plain tf32 (mixed precision by
  sensitivity). Do **not** fall back to bf16 for the decay-sensitive terms.
- **Preserve the bounded de-gating (section 2):** decays/`gamma`/`beta` stay f32,
  applied outside the mma. Re-run the adversarial `g in [-20, -1e-4]` op case
  specifically; a tensor-core kernel that moved a decay inside the mma would be a
  silent precision regression even if the benign cases pass.

The likely-favourable framing (as in PREFILL_GEMM): keeping the heavy reductions
in f32-accumulate tensor cores is *more* precise than a naive f32 serial loop only
if the inputs stay full-width; here inputs are down-cast (tf32/bf16), so this is a
genuine precision *trade*, not a free win - hence the KL-gate is mandatory and the
3xtf32 ladder exists. Treat NMSE-gate-pass as a bonus, KL-gate-pass as the bar.

---

## 5. Honest effort + expected gain

**This is a multi-week GPU kernel project, not a routing change.** Unlike the
PREFILL_GEMM dense lever (a dispatch flip onto an existing vendor kernel), there is
no vendor chunked-GDN kernel to route to on sm_121 (CUTLASS/FLA gate the good
paths to sm_100a; that is the whole reason vLLM falls back to Marlin on GB10). We
must write the `mma` kernel ourselves. Realistic estimate: **4-8 weeks** of
focused kernel work, high risk, with non-trivial probability the occupancy/register
wall caps the win.

**Expected gain (mechanism-grounded, section 0/regime-note):** the lever attacks
the state-BW that dominates sequential GDN prefill by `~C`x (chunking) while
tensor cores absorb the `O(C^2)` intra-chunk flops. Fully realized, it targets
vLLM's ~2.5x-cheaper chunked GDN prefill bucket = the ~17% prefill lever the
ground-truth attributes to GDN. It should also help the serial-SSM portion of the
**decode** residual (README names the irreducible "serial-SSM host loop" as part
of the decode floor; a chunked state-update reduces the per-step state traffic
there too, though decode `n_tokens` is small so the prefill regime is where it
pays). **Honest ceiling:** sm_121 has no wgmma/tcgen05, so we cannot match a
hypothetical sm_100a FLA kernel's throughput - the `mma.sync` path is the Ampere-
class programming model on Blackwell silicon. But `mma` over serial f32 reductions
is an order-of-magnitude flop-rate jump, which is more than enough to flip 0031's
-22% into a win and recover most of the GDN prefill bucket. Do not promise full
parity with vLLM's sm_100-class kernels; promise "beats the sequential scan and
closes most of the GDN prefill gap."

**Risk register:**
- Register-resident `128x128` state may cap occupancy at 1 block/SM (section 3) -
  mitigated by dv-slabbing, but slabbing recomputes `A^{-1}` per slab.
- tf32 may miss the KL-gate -> 3xtf32 ladder (3x mma cost) -> thinner margin.
- The win is contingent on prefill GDN being state-BW-bound (regime note); a GPU
  re-profile that says otherwise kills the lever (step 0).
- Blocked-forward-subst register pressure trades against state-register pressure;
  both compete for the same budget on a 1-block/SM kernel.

---

## 6. Phased build plan

Smallest tensor-core proof-of-concept first, bit-exact/KL-gate + A/B bench at every
phase, per `.agents/vllm-parity-methodology.md` (one lever at a time, record
rejected/flat variants, ground-truth both engines).

### Phase 0 - re-confirm the regime on GPU (NO code)
nsys a **prefill-only** window (`llama-batched-bench -npp <large> -ntg 0/1`,
exclude graph capture) on q36-27b-nvfp4 + q36-35b-a3b, at the backend pin, with
`GDN_CHUNK_MIN` set so 0031 runs. Confirm (a) the GDN prefill bucket is
state-BW-bound (state memcpy/recurrence dominates, tensor-core-active-% low), and
(b) it is ~17% of the prefill step / ~2.5x vLLM's. **If prefill GDN is not
state-BW-bound, stop and re-scope** - the entire mechanism (section 0) fails.

### Phase 1 - PoC: tensor-core just TWO products, same occupancy
Keep 0031's `C=16` all-shared layout and 1 block/SM. Replace **only** the two
cleanest `C x C` Gram products - step 1 (`KK` for `A`) and step 2 (`QK` for `P`) -
with `ggml/src/ggml-cuda/mma.cuh` tf32 tiles (decays still applied in f32 after).
Leave the solve, the `S0` products, and the state update serial. This is the
minimal "do tensor cores help here at all" probe at fixed occupancy.
- Gate: greedy md5 stable; `test-backend-ops GATED_DELTA_NET` prefill shapes via
  the KL-gate (NMSE if it passes).
- Bench: `llama-batched-bench` S_PP, A/B vs sequential and vs 0031-serial, same
  harness. **If even this does not move S_PP, the head-dim/occupancy is the wall,
  not the reductions - learn it cheaply before the big build.**

### Phase 2 - full intra-chunk tensor-core + register-resident state + C=64
State register-resident (free the 64KB), `C=64`, tf32 mma for all of steps 1-6,
blocked-forward-subst `A^{-1}` (step 7) with mma off-diagonal coupling +
in-register `16x16` diagonal solves. Decays/gamma/beta stay f32 throughout.
- Gate: as Phase 1, plus the adversarial `g in [-20,-1e-4]` op case explicitly.
  If tf32 misses the KL-gate, climb the 3xtf32 ladder (section 4).
- Bench: S_PP A/B vs sequential, sweep prefill length and `npl`; record the
  `C in {32,64,128}` sweep and any rejected `C`.

### Phase 3 - occupancy + latency hiding
dv-slab the state (`dv_tile in {64,32}`) for a 2nd resident block and to multiply
the grid (fix low-`n_seqs` starvation); `cp.async` double-buffer the K/Q chunk
loads. Tune `C`, `dv_tile`, warp count per the bench.
- Gate: unchanged (the FP path does not change; this is scheduling).
- Bench: final S_PP vs sequential + indicative % of vLLM prefill; name the
  residual floor honestly (register-cap / sm_121-has-no-tcgen05).

### Disposition
Like 0031, ship **opt-in default-OFF first** (extend the existing `GDN_CHUNK_MIN`
gate, add a `GDN_CHUNK_TC` selector if the serial path is kept as fallback). Flip
the default only when a separately-built A/B proves S_PP beats the sequential scan
*and* the KL-gate holds, recorded in README section 5 + `PAGED_BITEXACT_NOTE.md`.
If a phase comes back flat-or-slower, record it as a rejected lever with the reason
(the most valuable output if it fails) and keep 0031's serial path as the shipped
prefill kernel.

---

## 7. Summary

| Aspect | Decision |
|---|---|
| Tensor-core ISA | **`mma.sync` only** (sm_121a: no wgmma, no tcgen05/TMEM - confirmed) |
| Building block | reuse `ggml/src/ggml-cuda/mma.cuh` tiles, not raw PTX |
| Precision default | **tf32** inputs / f32 accumulate; **3xtf32** ladder if KL-gate misses; bf16 only for well-conditioned Gram terms |
| Decay handling | gamma/d/beta stay **f32**, applied outside the mma (preserve bounded de-gating) |
| A-inverse | blocked forward substitution (FLA UT-transform): in-register diagonal solves + mma off-diagonal |
| Chunk size | **C=64** default (4x 0031), C=32 for 2 blocks/SM |
| State | **register-resident** (frees the 64KB that forced C=16); dv-slab for occupancy |
| Shared budget | ~64-80KB at C=64 state-register-resident (under the 99KB opt-in) |
| Mechanism / why it wins | chunking cuts state-BW by ~Cx; mma absorbs the O(C^2) intra-chunk flops the serial 0031 could not |
| Bit-exact | NEW per-path; **KL-gate** binding (NMSE likely fails at reduced precision), greedy md5 + adversarial-decay op case |
| Effort | **multi-week (4-8 wk), high risk**; no vendor kernel to route to on sm_121 |
| Expected gain | beats the sequential scan, closes most of the ~17% GDN prefill bucket toward vLLM's 2.5x; also helps the decode serial-SSM residual. NOT full sm_100-class parity. |
| Phasing | P0 re-profile -> P1 two-product PoC -> P2 full intra-chunk + C=64 + reg-state -> P3 occupancy/cp.async; opt-in default-OFF until A/B-proven |

Decode is untouched (this is prefill-only, opt-in); the stock `llama-cpp` backend
stays patch-free. This lever lives entirely in `llama-cpp-localai-paged`.
