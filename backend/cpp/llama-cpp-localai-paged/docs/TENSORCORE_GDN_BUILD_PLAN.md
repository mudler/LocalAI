# Tensor-Core GDN Build Plan

> Auto-generated from the GDN build-design workflow. Build-ready spec for the full tensor-core chunked-scan kernel (2nd prefill lever).

## 1. Remaining intra-chunk products -> mma mapping

I have everything needed: the exact chunked-scan math from patch 0031, the sm_121a constraints from the scope doc, and the concrete ggml tf32 fragment (`mma.sync.aligned.m16n8k8.row.col.f32.tf32.tf32.f32`, `tile<16,8,float> D, tile<16,8,float> A, tile<8,8,float> B`) at `mma.cuh:976-984`. Here is the design.

---

# Tensor-core mapping of the REMAINING intra-chunk GDN products (patch 0031 steps 3-7)

## 0. Building block + what the PoC already covered

**Grounding.** Math: `backend/cpp/llama-cpp-localai-paged/patches/paged/0031-paged-chunked-gdn-prefill-scan-kernel.patch` (steps reproduced inline below). Scope/constraints: `backend/cpp/llama-cpp-localai-paged/docs/TENSORCORE_GDN_SCOPE.md`. Fragment API: `ggml/src/ggml-cuda/mma.cuh:976-984` (the only f32-accumulate tf32 overload on sm_121a).

The single warp-level primitive on sm_121a is **`m16n8k8` tf32 / f32-accumulate**:
- `A` fragment = `tile<16,8,float>` (M=16, K=8; 4 floats/thread, `Axi[0..3]`)
- `B` fragment = `tile<8,8,float>` (K=8, N=8, `.col` operand; 2 floats/thread, `Bxi[0..1]`)
- `D` accumulator = `tile<16,8,float>` (M=16, N=8; 4 floats/thread)
- A GEMM `[M×K]·[K×N]` tiles to `ceil(M/16) × ceil(N/8) × ceil(K/8)` mma calls, f32-accumulating over the K-subtiles.
- bf16 alternative `m16n8k16` (`mma.cuh:1064`, K=16/mma, 7-bit mantissa) exists but is **only** admissible for the tf32-safe Gram class — never the state/decay-coupled class.
- 3xtf32 ladder = split each f32 operand into 3 tf32 limbs, run 3 limb-products per K-subtile (hi·hi, hi·lo, lo·hi), accumulate high→low. ~3x the mma count, ~f32 accuracy.

**PoC covered products 1 + 2** (the two `C×C` Gram products, both tf32-safe, NMSE ~3e-9): `KK[t,t']=k_t·k_t'` → `A`, and `QK[t,t']=q_t·k_t'` → `P`. Both are `(C×dk)·(dk×C)`, M=C N=C K=dk=128, decay+beta applied in f32 after. They already share the `Kc^T` B-fragments.

The remaining families are **steps 3,4,5,6,7**. Notation: `C` = chunk (default 64; PoC 16), `dk=dv=128`, per `(head,seq)` block. Tile counts below are for **C=64**.

---

## 1. Per-product mma mapping table (the deliverable)

| # | Product (0031 step) | Result = matmul | M | N | K | mma tiles `(M/16)·(N/8)·(K/8)` @C=64 | Accumulation order | Precision class | Shares staged operand with |
|---|---|---|---|---|---|---|---|---|---|
| 1 | `KK→A` (PoC) | `Kc · Kcᵀ` | C | C | dk=128 | 4·8·16 = **512** (~½ tri) | over 16 k-subtiles | **tf32-safe** (proven) | `Kcᵀ` B-frag ↔ P2; `Kc` LHS ↔ P3 |
| 2 | `QK→P` (PoC) | `Qc · Kcᵀ` | C | C | dk=128 | 4·8·16 = **512** (~½ tri) | over 16 k-subtiles | **tf32-safe** (proven) | `Kcᵀ` B-frag ↔ P1; `Qc` LHS ↔ P4 |
| 3 | `KS = S0ᵀk_t` | `Kc · S0` | C | dv=128 | dk=128 | 4·16·16 = **1024** | 16 k-subtiles, limbs hi→lo | **3xtf32 / f32** (state-boundary, feeds solve) | `S0` B-frag ↔ P4; `Kc` LHS ↔ P1 |
| 4 | `QS = S0ᵀq_t` | `Qc · S0` | C | dv=128 | dk=128 | 4·16·16 = **1024** | 16 k-subtiles, limbs hi→lo | **3xtf32 → demote-first** (×γ_t≤1 attenuated) | `S0` B-frag ↔ P3; `Qc` LHS ↔ P2 |
| 5 | `O += P·U` | `P · U` | C | dv=128 | C=64 | 4·16·8 = **512** (~½ tri over K) | C/8 k-subtiles, triangular | **tf32-safe** (P decay-masked & bounded in f32 first) | `P`(=Amat) ↔ P2; `U` B-frag ↔ P6 |
| 6 | `S_C += Kᵀ(D·U)` | `Kcᵀ · DU` | dk=128 | dv=128 | C=64 | 8·16·8 = **1024** | scale state by γ_last (f32) **first**, then C/8 k-subtiles, limbs hi→lo | **3xtf32 / f32** (THE cross-chunk carry, compounds over n_tok/C) | `U` B-frag ↔ P5; `Kc` (transposed) ↔ P1/3 |
| 7 | `U = A⁻¹·RHS` off-diag coupling `A_ij·U_j` | `A_ij · U_j` | b=16 | dv=128 | b=16 | 1·16·2 = **32**/pair → **~192** (6 pairs) +~128 diag | forward sweep i=0..C/b; off-diag subtractions before diagonal solve | **tf32-safe off-diag + f32 in-register `16×16` diagonal** | `A`(=Amat) ↔ P1; `U` blocks ↔ P5/P6 |

3xtf32 inflation if the ladder is taken: P3 1024→**3072**, P4→**3072**, P6 1024→**3072**.

---

## 2. Per-product detail (the 5 remaining families)

### Product 3 - `KS = S0ᵀ k_t` (RHS state-boundary term)
0031: `ks = Σ_i Sd[j·dk+i]·Kc[t·dk+i]`; feeds `RHS[t][j] = β_t(v_t[j] − γ_t·ks)`.
- **As a GEMM:** `KS[t][j] = Σ_i Kc[t][i]·S0[i][j]` ⇒ `KS = Kc[C×dk] · S0[dk×dv]`. **M=C, N=dv=128, K=dk=128.** Contraction over the state-row index `i`.
- **Schedule:** `Kc` is the LHS (M-major over `t`, K over `i`) — already staged for P1. `S0` is the B operand, K-major over `i`, N over `j`. The patch's `Sd[j·dk+i]` layout (i contiguous for fixed j) **is already a K-major B layout** → `ldmatrix`-friendly as `tile<8,8>` B fragments. Accumulate 16 k-subtiles into f32 D.
- **Precision: 3xtf32/f32.** This is a state-boundary product: `S0` carries the full sequence history (wide dynamic range), and the result is *differenced* against `v_t` then fed into the solve, so error here propagates through `U` into both `O` and `S_C`. Default to the 3xtf32 ladder; A/B a plain-tf32 demote only after P4.

### Product 4 - `QS = S0ᵀ q_t` (γ cross-chunk `O` term)
0031: `qs = Σ_i Sd[j·dk+i]·Qc[t·dk+i]`; `o = γ_t·qs + Σ P·U`.
- **As a GEMM:** `QS = Qc[C×dk] · S0[dk×dv]`. **M=C, N=dv=128, K=dk=128** — identical shape to P3.
- **Schedule:** identical to P3 but LHS=`Qc` (shared with P2). **Fuse with P3 on the shared `S0` B-fragments:** stage `S0` once as B, run `Kc·S0` then `Qc·S0` back-to-back — `S0` is the heavy operand (128×128) and is loaded once for both.
- **Precision: 3xtf32 but the demote-first candidate.** The term is scaled by `γ_t ≤ 1` in f32 after the mma, so when the chunk has decayed (`γ_t→0`) the absolute error is attenuated. Least sensitive of the three state-boundary products; it is the first to try at plain tf32 in the precision A/B.

### Product 5 - `O += P · U` (attention-weighted output)
0031: `o += Amat[t·Cc+tp]·Ud[j·C+tp]` for `tp≤t`.
- **As a GEMM:** `O[C×dv] += P[C×C] · U[C×dv]`. **M=C, N=dv=128, K=C=64.** Contraction over the chunk index `t'`.
- **Schedule:** `P` (=Amat scratch from P2, with `d(t',t)` applied in f32) is LHS (M over t, K over t'); `U` (solved, in `Ud`) is the B operand, K-major over t'. `P` is **lower-triangular** ⇒ for M-tile `m` only K-subtiles `≤ m` are non-zero → ~½ the mma. Accumulate `C/8` k-subtiles. Add the `γ_t·QS` term (P4) into the same f32 D accumulator before write-out.
- **Precision: tf32-safe.** `P = d·QK` with `d≤1` is formed and bounded **in f32 first** (strong-decay rows already underflowed to ~0), so down-casting the bounded `P` to tf32 for this mma is benign. The decay is never inside the accumulation — it is pre-baked in f32, preserving the bounded de-gating invariant.

### Product 6 - `S_C += Kᵀ(D·U)` (the state update)
0031: `s = γ_last·Sd[j·dk+i] + Σ_t d(t,last)·Kc[t·dk+i]·Ud[j·C+t]`.
- **As a GEMM:** let `DU[t][j] = d(t,last)·U[t][j]` (D=diag applied in f32). `S_C[i][j] += Σ_t Kc[t][i]·DU[t][j]` ⇒ `S_C[dk×dv] += Kcᵀ[dk×C] · DU[C×dv]`. **M=dk=128, N=dv=128, K=C=64.** Contraction over the chunk index `t`.
- **Schedule:** the accumulator D fragments **are the register-resident state** that persists across the chunk loop. Order is strict: (i) scale the state fragments by `γ_last` in f32 in-register, **then** (ii) mma-accumulate `Kcᵀ·DU` over `C/8` k-subtiles into them. LHS = `Kc` read **transposed** (i as M-row, t as K) — a different fragment view of the same `Kc` smem buffer (use the `ldmatrix` transpose / J-major tile view). B = `DU` = `U` scaled by `d(t,last)` in f32, K-major over t — **same `U` B-layout as P5**.
- **Precision: 3xtf32 / f32 — the strongest ladder candidate.** This is the only product whose error *compounds across all `n_tokens/C` chunk steps*; it defines the state trajectory. Keep at 3xtf32 longest; this is the last product to ever consider demoting, and the place where a full-f32 accumulate (3xtf32) is most justified even if everything else passes plain tf32.

### Product 7 - the A-inverse (blocked forward substitution, FLA UT-transform)
0031 does a serial per-thread fwd-subst. Tensor-core form (block `b=16` = one mma M-tile, `C/b=4` blocks at C=64):
- For block `i`: `U_i = Ainv_ii·(RHS_i − Σ_{j<i} A_ij·U_j)`.
- **The A-inverse-adjacent matmul = the off-diagonal coupling `A_ij·U_j`:** **M=b=16, N=dv=128, K=b=16** ⇒ `1·16·2 = 32` mma/pair; 6 lower pairs at C=64 → **192** mma. Optional materialized-`Ainv_ii` apply is the same shape (~128 more).
- **Schedule:** forward sweep `i=0..3`; for each `i` accumulate all `j<i` couplings into a `b×dv` register tile (subtract from `RHS_i`), then apply the `b×b` diagonal inverse. `A`=Amat (from P1, β·d applied in f32) is the LHS; `U_j` blocks are read from `Ud` and updated in place as the sweep advances.
- **Precision: split.** Off-diagonal coupling = **tf32-safe** (`A_ij`=β·d·kk is bounded, `d≤1`; well-conditioned for the stable de-gating). The `16×16` **diagonal block inverse stays f32/in-register** (Neumann series on the b-nilpotent, ≤b−1 terms, or a short serial solve) — exact, sensitive, but tiny. This is exactly the scope's recommended structure.

---

## 3. Staged-operand sharing graph (load amortization)

Five smem/register operands, and which products read them — the fusion that makes the added flops nearly free:

- **`Kc` (C×dk)** — the most-shared buffer. M-major-over-t LHS: P1, P3. K-major-over-i B (`Kcᵀ`): P1, P2. Transposed (i-major, contract t): P6. ⇒ stage once per chunk, feeds 1,2,3,6.
- **`Qc` (C×dk)** — LHS for P2 and P4.
- **`S0` B-fragments (dk×dv, register-resident state)** — P3 and P4. **Stage once as B, run KS then QS** (heaviest operand, amortized 2×).
- **`Amat` (C×C)** — P1 writes `A` → P7 reads `A` → P2 overwrites with `P` → P5 reads `P`. One buffer, lifecycle-reused (as 0031 already does).
- **`Ud` (C×dv)** — P7 writes `U` → P5 reads `U` (B, contract t) → P6 reads `U` scaled to `DU` (B, contract t). **P5 and P6 share the identical `U` B-layout** (both contract the chunk dim) → fully shared B-fragments.

Three concrete fusions worth coding as fused passes:
1. **P1+P2** share `Kcᵀ` B (PoC already does this).
2. **P3+P4** share `S0` B (stage the 128×128 state-as-B once).
3. **P5+P6** share `U` as B (both K=C contractions); compute `P·U` and `Kcᵀ·DU` from one `U` staging, P6 accumulating straight into the persistent state fragments.

---

## 4. tf32-safe vs 3xtf32 ladder - summary + recommended A/B order

**Plain-tf32-safe (well-conditioned, bounded, intra-chunk; bf16 `m16n8k16` is even an option if more throughput is needed):**
- P1 `KK`, P2 `QK` (PoC-proven), P5 `P·U` (P bounded/f32-pre-masked), P7 off-diagonal coupling.

**3xtf32 / f32 ladder (state-boundary, cross-chunk carry, error compounds):**
- P6 `Kᵀ(D·U)` — keep at 3xtf32 longest (compounds over every chunk).
- P3 `KS` — feeds the solve; 3xtf32 by default.
- P4 `QS` — 3xtf32 by default but γ_t-attenuated → **first to demote** to plain tf32 in the precision A/B.
- P7 `16×16` diagonal block inverse — stays **f32/in-register** (not a tensor-core op).

**Recommended precision A/B ladder (drives the KL-gate from `PAGED_BITEXACT_NOTE.md`):** start P3/P4/P6 at 3xtf32 and P1/P2/P5/P7-offdiag at plain tf32. If the KL-gate has margin, demote in order **P4 → P3**, holding **P6 at 3xtf32**. If even all-3xtf32 misses the KL-gate, the residual is the `16×16` diagonal solve precision, not the mma — that already stays f32.

---

## 5. Two honest implementation gotchas (not in the scope doc, surface in the mapping)

1. **Accumulator→B relayout of the state at each chunk boundary.** The register-resident state lives as P6's **D/accumulator** fragments (`tile<16,8>`), but P3/P4 need it as a **B operand** (`tile<8,8>`, K-major over `i`). These fragment layouts differ, so at chunk entry the state must be re-laid-out accumulator→B. Cheapest correct path: bounce the 128×128 state through a transient smem tile (write D fragments, `ldmatrix` back as B fragments) once per chunk — `n_tokens/C` times total, negligible vs the `C`x state-BW saved, but it means the "freed 64KB" needs a *transient* state-shaped smem tile at boundaries (not resident across the intra-chunk work). With dv-slabbing it's per-slab (`dk×dv_tile`).

2. **`Kc` needs two fragment views.** P1/P2/P3 read `Kc` contracting over `i` (dk); P6 reads `Kc` contracting over `t` (transposed, i as M-row). One smem buffer, but P6 must use the `ldmatrix` transpose / J-major `tile` view — budget for the transposed load, don't assume one staging serves both.

Ordering within a chunk is already correct in 0031 and must be preserved by the tensor-core version: P3,P4 read **pre-update** `S0` → P7 solve → P5 → **P6 overwrites** `S0`→`S_C`. Single accumulator, read-first/write-last, no state double-buffer needed.

## 2. A-inverse solve (form-T then apply, FLA UT transform)

I have everything I need: the chunked GDN math (patch 0031), the scope doc's recommendation, the ggml tf32 `mma.sync` API (`mma(tile<16,8,float>&D, tile<16,8,float>&A, tile<8,8,float>&B)` = m16n8k8), and the proven Gram PoC layout (`g=lane>>2, t=lane&3`; tf32 NMSE ~3e-9). Here is the design.

---

# A-inverse solve on `mma.sync` tensor cores (C=64, sm_121a) - design

Notation: `C=64`, head dim `dk=dv=128`, block size `b=16` (= one `m16n8k8` m-tile), `n_b=C/b=4`. `A = I + N`, `N = tril(beta_t·d(t',t)·(k_t·k_t'), -1)` strictly-lower (nilpotent, `N^C=0`); `RHS[t][j] = beta_t(v_t[j] - gamma_t(S0^T k_t)[j])` is `C×dv`; we want `U = A^{-1}·RHS`.

## 0. Core decision: form `T=A^{-1}` explicitly, then one wide apply (not direct back-subst)

Two routes were on the table. **Form `T = A^{-1}` in the `C×C` domain (FLA "UT transform"), then `U = T·RHS` as a single tf32 GEMM** - rather than blocked forward-substitution applied directly to the `C×dv` RHS. Reasons, all decisive on this part:

1. **Confines the only triangular dependency to the cheap `C×C` domain.** The expensive `dv=128`-wide work (`U=T·RHS`) becomes a dependency-free dense GEMM. The serial part is just the tiny `T`-formation. This is the single most important move for "don't serialize the warps."
2. **Fewer serial passes vs `dv`.** Inverting the `16×16` diagonal block once = a 16-column solve. Direct-solving against `RHS` re-solves against all `dv=128` columns per block. Form-`T`-once + reuse via mma is far cheaper in serial work.
3. **dv-slab reuse (the occupancy lever).** `T` depends only on `K`, not on `dv`. Form once, reuse for every `dv`-slab's `T·RHS_slab` apply. (Improvement over the scope's conservative "recompute per slab": when single-block, `T` lives in 16KB shared and is broadcast; only when dv-slabbing across separate blocks for occupancy do we recompute - which is cheap anyway, ~12% of the apply's mma count.)
4. **Isolates the error amplifier.** All recursion (the part that "amplifies error") lives in the small `T`-formation where 3xtf32 is nearly free; the bulk apply is a single well-conditioned GEMM.

This still **is** the scope's "blocked forward substitution: in-register diagonal solves + mma off-diagonal coupling" - just organized to produce `T` explicitly so the wide apply is dependency-free.

## 1. Solve algorithm

Block-partition `A` into a `4×4` lower-triangular grid of `16×16` blocks. `A_ii = I_b + N_ii` (unit-lower-tri, `N_ii` strictly-lower nilpotent); `A_ij` (i>j) full `16×16`. `T=A^{-1}` is block-lower-tri with:

```
T_ii = A_ii^{-1}                                    (diagonal block inverse)
T_ij = -A_ii^{-1} · ( Σ_{m=j}^{i-1} A_im · T_mj )    for i > j     (block fwd subst)
```

Then `U = T·RHS`, with `U_i = Σ_{j≤i} T_ij·RHS_j`.

**Phase D - diagonal inverses (4 blocks, fully parallel).** Each `A_ii` is `16×16` unit-lower-tri. Invert **exactly in f32** via shared-memory column-parallel forward substitution: stage `A_ii` to shared; thread `c` (c=0..15) solves `A_ii x = e_c` (`x_c=1`, `x_r = -Σ_{m=c}^{r-1} A_ii[r][m]·x_m`), writes column `c` of `T_ii`. 16 columns in parallel, ≤16 serial MACs each, all 4 blocks on 4 warps simultaneously. **No tensor cores here, and no reduced precision** - this is where the strongest coupling lives (see §4).

**Phase O - off-diagonal, mma.** For each i>j: accumulate `P_ij = Σ_m A_im·T_mj` (δ block-products), then `T_ij = -T_ii·P_ij`. All on `mma.sync` (`16×16×16` = `2 n-tiles × 2 k-steps` = 4 m16n8k8 per block-product).

**Apply.** `U = T·RHS`: warp `w` owns output rows `16w..16w+15`, sweeps all `dv=128` (16 n-tiles) × `C=64` (8 k-steps) = 128 m16n8k8/warp. This is the bulk and is embarrassingly parallel.

`A`, `P` (the QK term), `RHS`, and `T` are all assembled from tf32 Gram mma's (`KK`,`QK`,`KS`,`QS` - the PoC-proven step-1/2 plus step-3/4) with **all decay/`gamma`/`beta` applied in f32 outside the mma** (preserves bounded de-gating).

## 2. Tile schedule - keeping the triangular dependency off the warps

Block = 128 threads = 4 warps; **"warp == 16-row m-tile" throughout** (same mapping as the PoC's C=64 kernel, `rowbase = warp*16`, `g=lane>>2`, `t=lane&3`). Three layered mechanisms keep the warps busy despite the triangular dependency:

**(a) Wavefront (anti-diagonal) parallelism in `T`-formation.** The 6 off-diagonal blocks have a critical path of only `n_b-1=3`, not 6. Group by distance `δ=i-j`:

| Wave | Blocks (δ) | count | depends on | mapped to |
|---|---|---|---|---|
| D | (0,0)(1,1)(2,2)(3,3) | 4 | - | 4 warps ‖ |
| 1 | (1,0)(2,1)(3,2) | 3 | D | 3 warps ‖ |
| 2 | (2,0)(3,1) | 2 | D,1 | 2 warps ‖ |
| 3 | (3,0) | 1 | D,1,2 | 1 warp |

Within each wave all blocks are independent → one block per warp, no intra-wave serialization. Critical path = 4 dependency levels. Total `T`-formation mma: ~10 accumulation block-products + 6 inverse-applies = ~16 block-products × 4 = **~64 m16n8k8**, vs the apply's **512** (128/warp × 4) - so `T`-formation is ~12% of apply width and carries the only dependency.

**(b) Confinement.** Because we form `T` then apply, the dependency-laden work is the ~64-mma `C×C` formation; the 512-mma `dv`-wide apply has zero triangular dependency. The serial chain never touches the throughput-critical width.

**(c) Latency hiding via RHS overlap.** `T` depends only on `K` (→ `A` ← KK Gram). `RHS` depends on `V` and `S0^T k` (KS Gram, `dv`-wide, the expensive RHS term) and is **independent of the solve**. Schedule the wavefront `T`-formation (cheap, short critical path) concurrently with the `dv`-wide KS/QS Grams that build `RHS` and the `O` cross-term. The Phase-D shared scalar inverse (~16 shared round-trips × 4 warps) hides entirely under the KS Gram (thousands of cycles). By the time `T` is ready, `RHS` is staged and the apply fires immediately.

**Shared/register budget (C=64, state register-resident per the scope):**

| Buffer | bytes | note |
|---|---|---|
| `Kc`,`Qc` (bf16/tf32-staged) | 16KB+16KB | Gram operands |
| `A`→`T` scratch (`C×C` f32, in place) | 16KB | `A` consumed into `T`; reuses scope's A/P slot |
| `RHS`/`U` (`C×dv`) | 16-32KB | bf16 for the P·U and KᵀU mma's |
| diag-inverse scratch | ~1KB | `16×16` per warp, transient |
| gates `cs/gam/beta` | <1KB | f32 |
| state `S` | 0 (registers) | frees the 64KB that forced 0031's C=16 |

Total ~65-80KB, under the 99KB opt-in - the solve adds **no** net shared pressure (T overwrites A; diag scratch is transient). Per-thread diag-inverse needs ~16 regs (one column of `x`), released before the apply - does not compound the already-heavy state-accumulator register budget.

## 3. Precision risk assessment

**Error model.** `‖ΔU‖/‖U‖ ≲ κ(A)·(‖ΔA‖/‖A‖ + ‖ΔRHS‖/‖RHS‖) + ‖Δ_apply‖/‖U‖`. The inverse is the amplifier; `κ(A)` is data-dependent. For DeltaNet, keys are L2-normalized so `|k_t·k_t'|≤1` ⇒ `|N[t][t']|≤beta_t≤1`; in the decaying regime `‖N‖<1` and `κ` is modest, but in the weak-decay/aligned-keys corner `κ` grows and the `δ=3` column path (`T_30`) compounds 3 multiplies. tf32 input rounding is ~`2⁻¹¹`≈`5e-4` relative (f32-accumulate; PoC measured Gram NMSE ~`3e-9`). 3xtf32 (3-limb split, the CUTLASS fp32-emulation trick) buys ~f32 (~`1e-7`) at ~3× that step's mma cost.

**Where the strong coupling actually sits (the key structural fact):** the *inverse* `T_ii` is computed f32-exact, **but the dominant near-diagonal mixing is applied in the tf32 apply GEMM** (`U_i ⊃ T_ii·RHS_i`), and block-boundary adjacent pairs (e.g. tokens 15↔16) live in the `δ=1` off-diagonal `T_10`. So "f32 protects the strong coupling" is only true for the inverse *computation*; its *application* is tf32 unless promoted. This drives the ladder.

**Precision config + 3xtf32 ladder (mandatory vs optional):**

| Step | Default | Mandatory? | 3xtf32 cost |
|---|---|---|---|
| Diagonal inverse `T_ii` | **f32 (shared scalar)** | **Mandatory-and-free** (it's already f32) | n/a |
| Off-diag coupling `A_im·T_mj`, `T_ii·P_ij` | **3xtf32 (default-on)** | Effectively mandatory; ~3× of ~64 tiny mma = **negligible** | free insurance |
| KK/QK Gram → A,P | tf32 | optional (rung 1) | 3× of C×C Grams (cheap) |
| Apply `U=T·RHS` | tf32 | optional (rungs 2-4) | up to 3× the bulk |
| KS/QS Gram → RHS, O | tf32 | optional (rung 5) | vLLM keeps these bf16 (L4-rejected precedent) |

Decays/`gamma`/`beta` **always f32, outside the mma** - invariant, not a rung.

**Ladder ordering if the default config misses the KL-gate (cheapest → most expensive):**
1. KK Gram (feeds `A`) → 3xtf32 [cheap, C×C].
2. Apply **block-diagonal terms only** `T_ii·RHS_i` → 3xtf32 [≈+0.8× apply; protects within-window strong coupling - mixed-precision-by-distance].
3. + apply `δ=1` off-diagonal terms → 3xtf32 [covers block-boundary adjacent pairs].
4. Full apply → 3xtf32 [≈+2× apply; expensive escape hatch].
5. KS/QS Gram → 3xtf32.
6. Fall back to direct blocked back-substitution against RHS in 3xtf32 (the alternative route, slightly more accurate than form-`T`-then-multiply at the cost of the parallelism), else keep 0031's serial path.

**Adversarial `g∈[-20,-1e-4]`:** strong decay ⇒ `d=exp(big-negative)→0` ⇒ off-diagonal `N→0` ⇒ `A≈I`, `T≈I`, apply≈identity, tf32 error vanishes; bounded de-gating (f32) guarantees underflow-to-zero, never inf. Weak decay (`g→0`) ⇒ `d≈1`, `A` well-conditioned, tf32's 8-bit exponent (vs f16's 5) holds the `gamma` dynamic range. The dangerous middle is the only KL-empirical risk - re-run this op case explicitly per the scope.

**KL impact / gating.** Same gate as the backend's new-FP-path precedents: NMSE is expected to *fail* at reduced precision (this is a new path on a new path) - **the binding gate is KL** (`KLD(tc‖f16) ≤ KLD(seq‖f16)` + PPL band) plus greedy-md5 stability (md5 will not match 0031's serial path - per-path, validated benign). Expectation: the **default config (f32 diagonal + 3xtf32 off-diagonal-coupling + tf32 everything-else)** clears the KL-gate, because (i) the dominant apply matches the PoC Gram's `~3e-9`/tf32-input grade and (ii) the recursion-amplified `C×C` work is f32-grade for free. The expensive apply-3xtf32 rungs are reserved escapes. Worst case all-3xtf32 ≈ 3× the mma cost - still an order of magnitude under 0031's serial-f32 reductions and still net-positive given the `~C×` state-BW cut.

## 4. Integration + validation

- Build on `ggml/src/ggml-cuda/mma.cuh`: the tf32 path is `mma(tile<16,8,float>&D, tile<16,8,float>&A, tile<8,8,float>&B)` → `mma.sync.aligned.m16n8k8.row.col.f32.tf32.tf32.f32` (line ~1089), gated by `AMPERE_MMA_AVAILABLE` (sm_121-correct). tf32 operands stage to shared and load via `load_generic` (or the PoC's `cvt.rna.tf32.f32` register packing); `ldmatrix` is `.b16`-only so it is **not** usable for tf32 fragments - use `load_generic`. 3xtf32 = 3 limb-split tf32 mma's + f32 recombine, reusing the same tile type.
- Slots into patch 0031's `gated_delta_net_chunked_cuda`: replace the serial `KK`/`QK` Grams (lines 183-198, 222-235) with the PoC tiles; replace the serial forward-subst (lines 211-218) with this Phase-D + Phase-O `T`-formation; replace the serial `O`/`S_C` reductions (steps 5/6) with the apply + `KᵀU` mma. `C=16`→`C=64`, state register-resident.
- Validation step before the full kernel: extend `~/scratch_tc_gdn_poc/gdn_gram_bench.cu` (DGX) to microbench Phase-D+O `T`-formation and the `T·RHS` apply in isolation, with the f64 host oracle measuring `κ(A)` and the tf32-vs-3xtf32 NMSE per rung - mirrors the Gram PoC that proved 6.7×→9.3×. This tells you, cheaply, whether the default precision config will clear the KL-gate before committing to the multi-week kernel (Phase 2 of the scope's plan).

**Net:** the solve adds zero shared pressure, ~12% mma overhead over the apply, a 4-level (not 64-step) critical path fully hidden under the RHS Grams, an f32-exact inverse, and a cheap-by-default 3xtf32 placement aimed exactly at the recursion-amplified term - so the A-inverse is **not** the bottleneck and **not** the precision wall; the residual risk is the data-dependent `κ(A)` in the weak-decay corner, arbitrated by the KL-gate with the apply-3xtf32 rungs as the escape.

Files referenced (absolute): `/home/mudler/_git/LocalAI/.claude/worktrees/feat+paged-attention/backend/cpp/llama-cpp-localai-paged/docs/TENSORCORE_GDN_SCOPE.md`; `/home/mudler/_git/LocalAI/.claude/worktrees/feat+paged-attention/backend/cpp/llama-cpp-localai-paged/patches/paged/0031-paged-chunked-gdn-prefill-scan-kernel.patch`; ggml tf32 mma at `ggml/src/ggml-cuda/mma.cuh` (line ~1089); DGX PoC `~/scratch_tc_gdn_poc/gdn_gram_bench.cu`.

## 3. Register-resident state + C=64 budget

I have everything I need: the scope doc, the full 0031 kernel (smem layout, the 7 product families, the chunk-loop sequencing), and the sibling FP4-MMA GEMM kernel's concrete patterns (PAD=4 u32 rows, STAGES=2 cp.async, WM=4/WN=2 8-warp grid, ldmatrix, register-resident accumulators). Here is the concrete data-layout design.

---

# Register-resident state + C=64 design (tensor-core chunked GDN, sm_121a / mma.sync)

## 0. Block/warp configuration (the frame everything hangs on)

0031 used `block_dims(128,1,1)` = 128 threads, "thread j owns v-column j" - a **column-parallel scalar** model. The tensor-core kernel must abandon that and adopt a **warp-tiled** model (same as the sibling GEMM kernel):

- **256 threads = 8 warps**, arranged as a **WARPS_M x WARPS_N = 4 x 2** warp grid (the GEMM kernel's proven `WM=4, WN=2`).
- Threads no longer own columns; warp `(wm,wn)` owns a rectangular sub-tile of each matrix and drives `mma.sync` on it.
- Precision: **tf32 m16n8k8** for the S-coupled / decay-coupled products, **bf16 m16n8k16** allowed only for the well-conditioned intra-chunk Gram terms (KK, QK). f32 accumulate throughout. Decays/`gamma`/`beta` stay f32, applied outside the mma (preserve bounded de-gating).

This 4x2 warp grid is the denominator for every ownership calc below.

---

## 1. The one hard problem: S is an *accumulator* for step 6 but an *operand* for steps 3/4

This is the crux the scope hand-waves ("read S as the stationary operand; step 6 accumulates into it"). The register fragment layouts are **not** interchangeable:

| Use | Role | mma shape | S indexing | Fragment layout |
|---|---|---|---|---|
| Step 6 `S += Kᵀ(D·U)` | **accumulator (D/C)** | m=dk, n=dv, k=C | `S[i][j]`, m=i, n=j | `tile<16,8,float>` acc grid |
| Step 3 `KS = K·S` | **B operand** | m=C, n=dv, k=dk | `S[i][j]`, k=i, n=j | `tile<8,8,float>` B frag |
| Step 4 `QS = Q·S` | **B operand** | m=C, n=dv, k=dk | same as step 3 | `tile<8,8,float>` B frag |

An accumulator fragment's thread→element map differs from a B-operand fragment's, so **you cannot feed the persistent S registers directly into the step-3/4 mma.** A bridge is mandatory. The design decision:

> **S lives register-resident in the step-6 ACCUMULATOR layout** (it is written every chunk; that is the hot path). Steps 3/4 reach it via a **once-per-chunk restage to a small smem tile, re-read with `ldmatrix`** as B-operand fragments.

The restage cost is paid `n_tokens/C` times (not per token) - it is *inside* the BW saving the whole lever buys. And critically, the restage smem **time-multiplexes onto the Uc/Amat region**: at chunk entry (when KS/QS are needed) U and A for this chunk are not yet computed, so their buffers are free to hold the S restage. **Net additional persistent smem for the state: 0KB** - the scope's "0KB shared state" holds, with this scheduling caveat made explicit.

KS and QS read the **same** pre-update S0, so restage once → do both → then overwrite with U.

---

## 2. Register allocation map (per thread, 256-thread block)

State `S` is `dk x dv = 128 x 128` f32 = 16384 elems. Distributed over 256 threads = **64 f32/thread** at full dv.

| Register class | Lifetime | Full dv=128 | dv-slab=64 | dv-slab=32 | Layout / ownership |
|---|---|---|---|---|---|
| **Persistent S accumulator** | whole chunk loop | **64 regs** | **32 regs** | **16 regs** | warp `(wm,wn)` owns dk-rows `[wm·32, +32)` x dv-cols `[wn·(dv/2), +dv/2)`; = 2 m-tiles x (dv/2/8) n-tiles of `tile<16,8,float>`, 4 f32 each |
| Transient A-operand frag | per product | 4 regs/tile | same | same | `tile<16,8,float>` (tf32 packs k8) reused across KK/QK/KS/QS/O/Supd |
| Transient B-operand frag | per product | 2 regs/tile | same | same | `tile<8,8,float>` |
| Transient product accumulator (KK/QK/KS/QS/O) | per product, then spilled to smem | ≤8 tiles·4 = 32 regs | ≤16 | ≤8 | these outputs go to smem; acc is transient, reused |
| A⁻¹ diagonal-block solve (16x16, in-registers) | step 7 only | ~8-12 regs | same | same | one `b=16` unit-lower-tri block per warp-row, scalar Neumann/`<b-1` terms |
| loop/index/gate scalars | always | ~12 regs | same | same | c0, Cc, cs/gam/beta locals |

**Per-thread totals (256 threads):**
- Full dv: 64 (S) + ~50 (transients, non-overlapping with S) + ~12 ≈ **~130 regs/thread** → fits **1 block/SM** (256 regs/thread budget at 65536/SM÷256). 2 blocks/SM (128 regs/thread cap) would spill - hence dv-slab for occupancy.
- dv-slab 64: 32 (S) + ~50 + 12 ≈ **~94 regs/thread** → fits **2 blocks/SM** (128-reg cap). ✓
- dv-slab 32: 16 (S) → ~78 regs/thread → headroom; grid x4.

Persistent-state register pressure is the occupancy gate; everything else is transient and reused across the 7 products.

---

## 3. Shared-memory allocation map (PAD-padded, conflict-free)

Apply the GEMM kernel's lesson verbatim: **PAD = 4 (in the row's element width)** so a 128-wide row (a multiple of the 32 banks → 8-way conflict for the 8-row `ldmatrix`) becomes stride `132`, and the 8 rows of an `ldmatrix.m8n8` land in 8 distinct banks: `(r·132 + c) mod 32 = (4r + c) mod 32`, distinct for `r=0..7`. ✓

| Buffer | Logical shape | Row stride (padded) | Element | Notes |
|---|---|---|---|---|
| `Kc` (chunk K) | `[C][dk]` | `dk + 8` bf16 (= +4 u32) | bf16 | A-operand for KK/QK; A/Bᵀ for KS; transposed-A for Supd. bf16 default |
| `Qc` (chunk Q) | `[C][dk]` | `dk + 8` bf16 | bf16 | A-operand for QK/QS/O |
| `Uc` (solved U) | `[dv][C]` | `C + 4` f32 | f32 | f32 for the triangular solve accuracy; B-operand (down-cast tf32) for O & Supd |
| `Amat` (A then P) | `[C][C]` | `C + 4` f32 | f32 | KK→A→solve, then reused for QK→P; decays applied in f32 here |
| `gates` cs/gam/beta | `[3·C]` | (1-D, no pad) | f32 | prefix-sum + `expf`, f32 always |
| **S-restage tile** | `[dk][dv_strip]` | `dv_strip + 4` f32 | f32 | **overlays Uc∪Amat** at chunk entry; not additive at peak |
| `cp.async` stage dup | (STAGES=2 on Kc/Qc) | as Kc/Qc | bf16 | Phase-3 latency hiding only |

PAD widths: f32 tiles +4 elems; bf16 tiles +8 elems (= +4 u32, identical bank offset as the GEMM kernel). `Uc` and `Amat` are padded on the C dimension (their `ldmatrix` access dimension).

---

## 4. C=64 shared budget table (under the 99KB opt-in)

Byte math with PAD included (`KB = bytes/1024`):

| Buffer | CONFIG A — **default**: C=64, dv=128, K/Q **bf16** | CONFIG B: C=64, dv=128, K/Q **tf32** | CONFIG C — **2 blk/SM**: C=32, dv-slab=64, K/Q bf16 |
|---|---|---|---|
| `Kc` | 64·136·2 = **17.0KB** | 64·132·4 = 33.0KB | 32·136·2 = 8.5KB |
| `Qc` | **17.0KB** | 33.0KB | 8.5KB |
| `Uc` (f32) | 128·68·4 = **34.0KB** | 34.0KB | 64·36·4 = 9.0KB |
| `Amat` (f32) | 64·68·4 = **17.0KB** | 17.0KB | 32·36·4 = 4.5KB |
| gates | **0.75KB** | 0.75KB | 0.4KB |
| S-restage | overlay (0 net) | overlay (0 net) | overlay (0 net) |
| **Per-block total** | **≈ 85.8KB** ✅ < 99 | **≈ 117.8KB** ❌ | **≈ 30.9KB** |
| Blocks/SM (≈100KB/SM) | **1** | n/a | **2** (61.8KB) ✅ |

Read-out:
- **CONFIG A is the recommended default**: C=64 (4x the 0031 chunk), full dv, fits at ~86KB with margin, 1 block/SM. Peak is the O/Supd phase (all of Kc+Qc+Uc+Amat live).
- **CONFIG B (tf32 K/Q) is budget-hostile** (117KB) - tf32 K/Q tiles don't shrink with dv-slab (they're `C x dk`), so even dv-slab=64 lands ~101KB. **Conclusion: stage K/Q as bf16; reserve tf32/3xtf32 for the S-coupled and decay-coupled terms** (which arrive via the small streamed S-restage and the f32 gate scaling), exactly per the scope's "bf16 only for well-conditioned Gram terms."
- **CONFIG C is the 2-block/SM lever**: C=32 + dv-slab=64 → 31KB/block, two resident blocks under the ~100KB/SM total, and the grid grows to `H x n_seqs x 2`.

---

## 5. dv-slab strategy (the 2nd block/SM + grid-starvation fix)

Split the `dv=128` value dimension into `n_slabs` blocks; each block computes a `dv_tile`-wide vertical strip of O and of the state.

- **Grid**: `dim3(H, n_seqs, n_slabs)` (was `(H, n_seqs)`). `n_slabs ∈ {1,2,4}` for `dv_tile ∈ {128,64,32}`. This **multiplies the grid by `n_slabs`**, directly attacking 0031's low-`n_seqs` grid starvation.
- **What is dv-independent and must be recomputed per slab**: `A` (KK Gram), the `A⁻¹` solve, the gate prefix - all depend only on K and the gates, *not* dv. Each slab recomputes them. Cheap once they are on tensor cores (the whole point); this is the FLA per-slab pattern.
- **What is dv-sliced**: the S accumulator (`128 x dv_tile`), `Uc` (`dv_tile x C`), KS/QS/O outputs, step-6 update. Halving/quartering dv halves/quarters both the **S register footprint** (64→32→16 regs/thread, §2) and the dv-scaled smem (`Uc`, restage).
- **Restage budget bonus**: at `dv_tile=64` the per-block S is `128 x 64` = 32KB, so the once-per-chunk restage fits the Uc∪Amat overlay window in a single pass (no strip loop). At full dv=128 the restage is done as **2 dv-strips of 32KB** reusing the same overlay (or 16 k-strips of 8x128 if registers are tighter than smem).

`b`-block forward substitution (step 7) is independent of dv too, so the in-register `16x16` diagonal solves are computed once and the off-diagonal mma coupling `Uᵢ -= Aᵢⱼ Uⱼ` runs per slab as a `(16x16)·(16 x dv_tile)` mma.

---

## 6. Bank-conflict-free layout - the GEMM PAD lesson applied

Concretely, per the sibling kernel's `ARS = KBLK·SAW + PAD` with `PAD=4` (which gave +19%):

- Every smem matrix read by `ldmatrix` (or its tf32 equivalent in `ggml/src/ggml-cuda/mma.cuh`) is stored with **row stride = logical_width + PAD**, PAD chosen so `stride mod 32 ≠ 0`: f32 width-128 → 132 (`132 mod 32 = 4`); bf16 width-128 (packed 64 u32) → 68 u32 (`68 mod 32 = 4`).
- This guarantees the 8 rows an `m8n8` `ldmatrix` touches map to 8 distinct banks for any fixed column → no replays on the operand loads, which are the kernel's inner-loop smem traffic.
- `cp.async` (CONFIG, Phase 3): `STAGES=2` double-buffer on `Kc`/`Qc` only (the GEMM kernel found multistage saturates BW past depth 2). 16B `cp.async.cg` copies into the padded rows; `commit_group`/`wait_group` Ampere-style (no TMA on sm_121). The pad keeps the staged writes coalesced and the mma reads conflict-free simultaneously.

---

## 7. Summary of the allocation decisions

| Decision | Value |
|---|---|
| Threads / warp grid | 256 / 4x2 (WM=4, WN=2) |
| **State residency** | register-resident in **step-6 accumulator layout** (`tile<16,8,float>` grid), 64/32/16 f32-regs/thread at dv 128/64/32 |
| **Accumulator↔operand bridge** | once-per-chunk `ldmatrix` restage of S to a small smem tile that **overlays Uc∪Amat** (0 net persistent smem); KS+QS share one restage |
| K/Q precision | **bf16** staged (tf32 K/Q breaks the 99KB budget); tf32/f32 reserved for S-coupled + decay-coupled terms |
| Uc / Amat | f32, padded on C (+4) |
| **PAD** | +4 f32 (+8 bf16 = +4 u32) row stride → `ldmatrix` 8-row conflict-free (GEMM-proven) |
| **C=64 default budget** | **≈86KB**, 1 block/SM (CONFIG A) ✅ |
| 2 blocks/SM | C=32 + dv-slab=64 → ≈31KB/block, grid x2 (CONFIG C) |
| dv-slab | grid `(H, n_seqs, n_slabs)`; A/A⁻¹/gates recomputed per slab; S/Uc/O dv-sliced |
| cp.async | STAGES=2 on Kc/Qc (Phase 3 only) |

One honest caveat surfaced beyond the scope doc: the scope's "~64-80KB / 0KB shared state" budget only holds with **bf16 K/Q staging** and the **overlay restage**; tf32 K/Q (CONFIG B) does not fit even with dv-slab, which is why bf16-Gram + tf32-only-for-S/decay is the forced (and scope-consistent) precision split.

Files referenced: `/home/mudler/_git/LocalAI/.claude/worktrees/feat+paged-attention/backend/cpp/llama-cpp-localai-paged/docs/TENSORCORE_GDN_SCOPE.md`, `/home/mudler/_git/LocalAI/.claude/worktrees/feat+paged-attention/backend/cpp/llama-cpp-localai-paged/patches/paged/0031-paged-chunked-gdn-prefill-scan-kernel.patch`, and the sibling GEMM patterns in `/home/mudler/_git/LocalAI/.claude/worktrees/feat+paged-attention/backend/cpp/llama-cpp-localai-paged/patches/paged/0034-feat-paged-native-NVFP4-W4A4-FP4-MMA-large-M-prefill.patch`.

## 4. Occupancy + launch config

I have everything I need. Key facts confirmed: tf32 tile is `mma.sync.aligned.m16n8k8.f32.tf32.tf32.f32` (mma.cuh:1089, accumulator `tile<16,8,float>` = 4 f32/thread/tile); grid.x = `H` = `n_v_heads` = `ssm_dt_rank` (llama-model.cpp:504; Qwen3-Next family = 32 GDN value heads); the sequential kernel already uses a 3D grid `(H, n_seqs, ceil(S_v/num_warps))` (gated_delta_net.cu:184) — the chunked 0031 collapsed that z-axis to 1, which is exactly what starves the grid.

Here is the occupancy + launch design.

---

# Occupancy + launch design — tensor-core chunked GDN prefill (sm_121a)

## 0. The two independent caps 0031 hit (must relieve BOTH for ≥2 blocks/SM)

0031's -22% is not one wall, it's two stacked walls, and they are relieved by *different* levers:

| Cap | 0031 value | Binding resource | Lever |
|---|---|---|---|
| **Shared-memory cap** | 89 KB (64 KB all-shared state) | 100 KB/SM, 99 KB dyn opt-in | state→registers **+ smaller C** |
| **Register cap** | n/a (was scalar) | 65536 regs/SM | **dv-slab** the register-resident state |
| **Grid cap** | `(H, n_seqs, 1)` = 32·n_seqs blocks | 48 SMs | **dv-slab multiplies grid** by n_slabs |

sm_120/121-class per-SM limits used throughout: **1536 threads/SM, 65536 32-bit regs/SM, 100 KB shared/SM (99 KB dynamic opt-in), 255 regs/thread, ≤24-32 blocks/SM (hw, never the binding limit here).** The binding limits are **shared and registers.**

Critical correction to the scope-doc budget table: it assumes **bf16** K/Q staging (2 B). The precision default is **tf32**, which is a 32-bit container in shared — tf32 K/Q would *double* Kc/Qc and blow C=64 past 99 KB. So the occupancy config **stages K/Q as bf16** (the well-conditioned KK/QK Gram products per the scope's "bf16 only for Gram terms"), keeps gates/decays/beta/the solve in f32. This is a real precision↔occupancy coupling, flagged in §5.

## 1. Grid mapping — three parallel axes, the chunk axis is serial

The inter-chunk recurrence carries state `S` across chunks, so **the chunk axis cannot be a grid axis** (it's the sequential dependency — that's the whole algorithm). The only legitimate grid axes that don't break the recurrence are:

```
dim3 grid(H, n_seqs, n_slabs);   // H = n_v_heads = 32 (ssm_dt_rank)
                                  // n_slabs = dv / dv_tile  (the new lever)
```

- `blockIdx.x = head` (0..31), `blockIdx.y = seq`, `blockIdx.z = dv-slab`.
- A block owns v-columns `[z·dv_tile, (z+1)·dv_tile)`, walks the chunk loop serially, and keeps **only its `dk × dv_tile` state slab** register-resident.
- This reuses the **same 3D grid shape the sequential kernel already has** (gated_delta_net.cu:184 uses z for S_v-splitting); the chunked kernel repurposes z from S_v-split to dv-slab. The dispatcher change is minimal.

**Saturation math (the core of the task).** Target ≥2 blocks/SM on 48 SMs ⇒ **≥96 concurrent blocks**. With H=32:

| n_seqs | dv_tile=128 (n_slabs=1) | dv_tile=64 (2) | dv_tile=32 (4) |
|---|---|---|---|
| 1 | 32 (starved, 0031) | 64 (48/48 SMs busy, 67% warp-occ) | **128 (100%)** |
| 2 | 64 | **128 (100%)** | 256 |
| 4 | 128 | 256 | 512 |

So **dv-slabbing is simultaneously the register-relief lever and the grid-multiplier** — it's the single most important move. Rejected grid alternatives: split-K over dk (needs cross-block atomic reduction + fights the state carry); batching heads/seqs per block (reduces grid, wrong direction).

## 2. Block dim / warp count — 8 warps / 256 threads

```
constexpr int WARPS = 8;
dim3 block(32 * WARPS, 1, 1);    // 256 threads
```

Why 8 warps:
- **Clean mma tile partition at C=32:** KK/QK output is `C×C = 32×32` = (32/16)·(32/8) = **8 m16n8 tiles → exactly 1 tile/warp**, dk=128 = 16 k8-steps. Steps 3/4 (KS/QS) and 5 (P·U) → 2 tiles/warp. Step 6 state update `dk×dv_tile`=128×64 → 64 tiles → **8 tiles/warp** (these are the persistent register-resident accumulators).
- **Register dilution:** the register-resident state accumulator is spread across all 256 threads (see §3) — more warps = fewer state-regs/thread.
- **Threads are not the cap:** 256 threads ⇒ up to 6 blocks/SM by the 1536 thread limit, so registers/shared decide.

Fallback if register-capped (§5): **12 warps (384 threads)** dilutes the state accumulator further (dv_tile=64: 32→21 state-regs/thread) at the cost of thinner per-warp tiles and ≤4 blocks/SM by threads.

## 3. Register-resident state ↔ dv-slab ↔ occupancy interaction

The state slab is held as **tf32 mma accumulator fragments** (`tile<16,8,float>`, 4 f32/thread/tile) persisting across the chunk loop. Per-thread state-register cost = `dk·dv_tile / 256`:

| dv_tile | state f32/block | state regs/thread (256 thr) | + working (est.) | regs/thread | regs/block | reg-allowed blocks/SM |
|---|---|---|---|---|---|---|
| 128 (no slab) | 16384 | 64 | ~50 | ~114 | ~29 K | 2 (tight) |
| 64 | 8192 | 32 | ~50 | ~82 | ~21 K | 3 |
| 32 | 4096 | 16 | ~50 | ~66 | ~17 K | 3 |

So on registers alone, dv_tile≤64 admits ≥2 blocks/SM. **Shared memory is then the binding cap**, and it's governed by **C**, not dv_tile (Kc/Qc/A all scale with C, only U scales with dv_tile):

| Config | Kc+Qc (bf16) | A/P (f32) | U (f32) | single | +cp.async dbl-buf K/Q | blocks/SM (shared) |
|---|---|---|---|---|---|---|
| C=64, dv_tile=128 | 32 KB | 16 KB | 32 KB | 80 KB | 112 KB ✗(no room!) | **1** |
| C=64, dv_tile=64 | 32 KB | 16 KB | 16 KB | 64 KB | 96 KB ✓ | **1** |
| **C=32, dv_tile=64** | 16 KB | 4 KB | 8 KB | **28 KB** | **44 KB ✓** | **2** |
| C=32, dv_tile=32 | 16 KB | 4 KB | 4 KB | 24 KB | 40 KB ✓ | **2** |

**Finding the scope doc missed:** C=64-no-slab is shared-saturated at 80 KB — there is **no room for cp.async double-buffering**, so the 1-block/SM kernel would have *no latency hiding* and likely still lose. C=64 needs dv_tile≤64 *just to make room for cp.async*, and is still 1 block/SM. **Genuine 2 blocks/SM requires C=32** (to drop Kc/Qc/A under the 49.5 KB/block budget).

## 4. cp.async double-buffering (depth 2, no TMA)

At 1 block/SM (C=64 path) cp.async is the *only* latency-hiding mechanism, so it's mandatory, not optional. Plain Ampere `cp.async` (`cp.async.commit_group` / `cp.async.wait_group`) — **no `cp.async.bulk`/TMA on sm_121.** Stage the **next chunk's Kc, Qc** (and Vc if the KL-gate doesn't force V from global) into a second shared buffer while the current chunk's mma runs. Depth **2 only** — the sibling GEMM kernel proved multistage saturates BW past depth 2. The double-buffer cost is already in the "+cp.async" column above (44 KB at C=32 keeps 2 blocks/SM).

## 5. Launch config (concrete) + honest occupancy estimate

**Recommended default (batched-prefill serving regime, n_seqs≥2):**
```
C = 32 ;  dv_tile = 64 ;  n_slabs = 2 ;  WARPS = 8
grid  = dim3(H=32, n_seqs, 2)
block = dim3(256, 1, 1)
smem  = 44 KB  (Kc/Qc bf16 ×2 dbl-buf + A/P f32 + U f32)   // cudaFuncSetAttribute return CHECKED (0031 precedent)
→ 2 blocks/SM. n_seqs≥2 ⇒ ≥128 blocks ⇒ 48/48 SMs at full 2-block occupancy (100%), 1.33 waves.
  A/Gram/solve recomputed 2× across slabs (state-update per slab is 2× the A work ⇒ ~25% redundant-flop overhead).
```

**Single-stream prefill (n_seqs=1) saturator:**
```
C = 32 ;  dv_tile = 32 ;  n_slabs = 4 ;  WARPS = 8
grid = dim3(32, 1, 4) = 128 blocks ⇒ 2 blocks/SM on all 48 SMs (100%) even at n_seqs=1.
Cost: A recomputed 4×, and at dv_tile=32 the A bucket ≈ the per-slab state bucket ⇒ ~1.5-2× total-flop overhead.
```

**BW-max alternative (1 block/SM, bench against the above):**
```
C = 64 ;  dv_tile = 64 ;  n_slabs = 2 ;  WARPS = 8 ;  smem = 96 KB (dbl-buf, fits 99 KB)
→ 1 block/SM, but 4× state-BW cut (vs 2× at C=32) + grid ×2. n_seqs=1 ⇒ 64 blocks ⇒ 48/48 SMs busy (67% warp-occ).
```

**Occupancy summary:**

| Config | blocks/SM | regs/thread | smem/block | SM util @ n_seqs=1 | SM util @ n_seqs≥2 | state-BW cut | redundant-A |
|---|---|---|---|---|---|---|---|
| 0031 | 1 | scalar | 89 KB | 32/48 busy (starved) | 1024 blk, no overlap | 1× (C=16) | none |
| C=32 dv64 (default) | **2** | ~82 | 44 KB | 48 busy, 67% occ | **100%** | 2× | 2× (~25%) |
| C=32 dv32 (1-seq) | **2** | ~66 | 40 KB | **100%** | 100% | 2× | 4× (~1.5-2×) |
| C=64 dv64 (BW-max) | 1 | ~114 | 96 KB | 48 busy, 67% occ | 100%, multi-wave | **4×** | 2× |

The C=32 (occupancy) vs C=64 (BW) choice is the empirical fork the scope doc defers to Phase-3 bench: 2 blocks/SM at half the BW saving, vs 1 block/SM at full BW saving + cp.async. **Wire both behind the existing `GDN_CHUNK_MIN` gate plus a `GDN_CHUNK_C` / `GDN_DV_TILE` selector and A/B them; do not assume.**

## 6. Residual risk — register pressure likely caps it at 1 block/SM (honest)

The ≥2-blocks/SM result rests on the **~50 working-regs/thread estimate**, which is optimistic:

- **The blocked-forward-subst A⁻¹ (step 7) is the swing factor.** The in-register 16×16 unit-lower-triangular diagonal inverse + the off-diagonal mma coupling + mma operand fragments + the **accumulator→operand fragment transpose** for reusing the register-resident S as a step-3/4 operand (a `movmatrix`/shared round-trip, since S lives in C-fragment layout but steps 3/4 need it as an A/B operand) can push working regs to **80-120**. At 256 threads, regs/thread > 128 ⇒ > 32 K regs/block ⇒ **silently drops to 1 block/SM** regardless of the 44 KB shared headroom. The scope doc names this exactly: "blocked-forward-subst register pressure trades against state-register pressure; both compete for the same budget."
- **Mitigation ladder, in order:** (i) 12 warps to dilute (dv_tile=64: state 32→21 regs/thread); (ii) `__launch_bounds__(256, 2)` to force the compiler under 128 regs/thread (risks spills to local → BW back); (iii) smaller dv_tile (more grid, more redundant A). If all fail, accept **1 block/SM and lean on cp.async double-buffering + the 4× BW cut + mma throughput** — which is still very likely a win over 0031's serial-f32/-22%, just not the 2-block target.
- **Grid-starvation at n_seqs=1 is structural** (H=32 < 96): only dv_tile=32 fully saturates a single stream, and it pays ~1.5-2× redundant-A flops. This is an inherent floor of the chunked formulation on a 32-head layer; batched serving (n_seqs≥2) is where the design cleanly saturates.
- **Precision↔occupancy coupling:** the 2-block budget assumes **bf16 K/Q** staging. If the KL-gate demands tf32 for the KS/QS S0-products (decay-coupled), that needs a second 32-bit K/Q copy or 3×tf32 — both inflate shared/registers and can knock C=32 back toward 1 block/SM. The occupancy win is contingent on bf16 Gram clearing the gate.

**Bottom line:** 2 blocks/SM and full 48-SM saturation are *reachable* — via **C=32 + dv-slab (64 for serving, 32 for single-stream) + 8 warps + depth-2 cp.async** — on the **shared-memory** budget. Whether the **register** budget also permits it is the one genuinely open risk, and the A⁻¹ solve's footprint is what decides it; the realistic outcome remains the scope doc's honest **1-2 blocks/SM**, with the win carried by mma + cp.async + the C× BW cut rather than by high occupancy.

Files: design lives in `backend/cpp/llama-cpp-localai-paged/docs/TENSORCORE_GDN_SCOPE.md` (§3 occupancy plan — this analysis sharpens it); kernel/dispatcher to modify `backend/cpp/llama-cpp-paged-dev/ggml/src/ggml-cuda/gated_delta_net.cu` (grid at :184, chunked launch added by patch `patches/paged/0031-paged-chunked-gdn-prefill-scan-kernel.patch`); tf32 tile at `ggml/src/ggml-cuda/mma.cuh:1089`; H source `src/llama-model.cpp:504`.

## 5. Synthesized build plan + milestones + gate

All anchors verified. 0031's kernel body, the 7-step structure, the `GDN_CHUNK_MIN`/`GDN_CHUNK_OFF` gating at the `if constexpr (!KDA && !keep_rs_t)` site, the `launch_gdn_chunked<128,16>` template, the smem formula, and the test-backend-ops shapes are all confirmed. The scope doc's KL gate, 3xtf32 ladder, risk register, and Phase 0-3 plan are confirmed. Here is the build-ready synthesis.

---

# BUILD-READY PLAN: tensor-core chunked-GDN prefill kernel (sm_121a)

Anchors (absolute): kernel + launch + gate live in `ggml/src/ggml-cuda/gated_delta_net.cu` (grid at :184), added by `/home/mudler/_git/LocalAI/.claude/worktrees/feat+paged-attention/backend/cpp/llama-cpp-localai-paged/patches/paged/0031-paged-chunked-gdn-prefill-scan-kernel.patch`. tf32 tile `mma(tile<16,8,float>&D, tile<16,8,float>&A, tile<8,8,float>&B)` = `mma.sync.aligned.m16n8k8.row.col.f32.tf32.tf32.f32` in `ggml/src/ggml-cuda/mma.cuh` (m16n8k8 overload ~976-984, dispatch ~1089), gated by `AMPERE_MMA_AVAILABLE`. PAD/cp.async patterns from `patches/paged/0034-feat-paged-native-NVFP4-W4A4-FP4-MMA-large-M-prefill.patch`. Gate/precedent docs: `docs/TENSORCORE_GDN_SCOPE.md`, `docs/PAGED_BITEXACT_NOTE.md`, `README.md` s5. Microbench: `~/scratch_tc_gdn_poc/gdn_gram_bench.cu` (DGX). Last patch in series is 0042 → this work is patches 0043+.

The new kernel is `gated_delta_net_chunked_tc_cuda<S_v, C, DV_TILE>`, a sibling to 0031's `gated_delta_net_chunked_cuda`. Symbols below reuse 0031's smem names (`Sd, Kc, Qc, Ud, Amat, csh, gam, bet`).

---

## (1) Phase-by-phase kernel structure

Block = **256 threads / 8 warps** in a **4×2 (WM×WN)** warp grid. State `S` (`dk×dv_tile`) is **register-resident in the step-6 accumulator layout** (`tile<16,8,float>` grid). Grid = `dim3(H, n_seqs, n_slabs)`, `blockIdx.z` = dv-slab. Chunk axis is the serial recurrence (NOT a grid axis). Invariant preserved from 0031: read pre-update `S0` (P3/P4) → solve → output (P5) → **overwrite S last** (P6). Single accumulator, no state double-buffer.

Per chunk `c0` (the loop body):

**Phase A - chunk load + gate prefix (f32, cooperative).** Load `Kc[C][dk]`, `Qc[C][dk]` **as bf16** (tf32 K/Q blows the 99KB budget - see §5 of the state design), load `V` chunk. Compute `csh = cumsum(g)` (≤0), `gam = exp(csh)` (≤1), `bet` - all f32, identical to 0031 lines (the `j==0` prefix scan, kept scalar; it is <1KB and hides under the Grams). cp.async depth-2 prefetch of the *next* chunk's `Kc/Qc` starts here.

**Phase B - state restage (accumulator→B bridge).** The crux. `S0` lives as P6's D/accumulator fragments but P3/P4 need it as a **B operand** (`tile<8,8>`, K-major over `i`). Bounce the `dk×dv_tile` state through a transient smem tile that **overlays the `Ud∪Amat` region** (free at chunk entry - U/A not yet computed) → `load_generic` back as B fragments (NOT `ldmatrix`: it is `.b16`-only, unusable for tf32; use `load_generic`). Paid `n_tokens/C` times, **0KB net persistent smem**. KS and QS share this one restage.

**Phase C - Gram + state-boundary products (the matmuls that read pre-update S0).**
- **P1 `KK→A`** = `Kc·Kcᵀ`, M=C N=C K=dk, lower-tri (~½ tiles). **tf32-safe** (PoC-proven NMSE ~3e-9). Apply `A = I + tril(βₜ·d(t',t)·KK, -1)` in **f32** after the mma.
- **P3 `KS`** = `Kc·S0`, M=C N=dv K=dk. **3xtf32** (state-boundary, feeds the solve). Output → `Ud` region (becomes RHS).
- **P4 `QS`** = `Qc·S0`, M=C N=dv K=dk, **fused with P3 on the shared S0 B-fragments**. **3xtf32** (γ-attenuated → first demote candidate). Seed the **O accumulator fragments register-resident with `γₜ·QS`** immediately (avoids parking QS in smem through to Phase F). Restage overlay is now free; `Ud`/`Amat` reclaim it.

**Phase D - A-inverse (form T = A⁻¹ explicitly, then wide apply).**
- **Phase-D inverses:** 4 diagonal `16×16` unit-lower-tri blocks, **f32 in shared-memory column-parallel forward substitution** (thread `c` solves `A_ii x = e_c`). No tensor cores, no reduced precision (this is the strong-coupling amplifier). 4 blocks on 4 warps in parallel, hides entirely under the Phase-C/RHS Grams.
- **Phase-O off-diagonal:** wavefront (anti-diagonal) schedule, critical path `n_b-1=3` not 6. For each i>j: `P_ij = Σ_m A_im·T_mj` then `T_ij = -T_ii·P_ij`, on `m16n8k8`. **3xtf32 default-on** (~64 tiny mma total, negligible). `T` overwrites the `A` scratch in place.

**Phase E - RHS + apply.** `RHS = βₜ(vₜ - γₜ·KS)` in **f32** (uses P3 result + V) → `Ud`. **`U = T·RHS`** as one dependency-free wide **tf32** GEMM, M=C N=dv K=C (the bulk, 128 mma/warp at full dv), in place → `Ud`.

**Phase F - intra-chunk output.**
- **P2 `QK→P`** = `Qc·Kcᵀ`, reuse `Amat` (now free after T consumed). **tf32-safe**. Apply `P = d(t',t)·QK` in **f32** (bounded, decay pre-baked - preserves the bounded de-gating invariant).
- **P5 `O += P·U`**, M=C N=dv K=C, P lower-tri (~½ tiles). **tf32-safe** (P f32-bounded first). Accumulate into the O fragments already seeded with `γₜ·QS`. Write `O*scale` to `dst`.

**Phase G - state carry (overwrites S0 last).** `DU = d(t,last)·U` in f32. **Scale the persistent S accumulator fragments by `γ_last` in f32 in-register first**, then **P6 `S_C += Kcᵀ·DU`** = `Kcᵀ·DU`, M=dk N=dv K=C, **3xtf32 (the strongest ladder candidate - compounds over every chunk)**, accumulated straight into the persistent registers. `Kc` is read **transposed** here (second fragment view, `load_generic` transpose). No restage-out: S stays resident for the next chunk.

After the loop: final-state write-back (M-layout), identical to 0031's tail.

Buffer lifecycle (single `Amat`, single `Ud`, as 0031): `Amat`: A(P1) → T(Phase-D/O, in place) → consumed by apply → P(P2) → consumed by P5. `Ud`: KS(P3) → RHS(Phase-E) → U(apply, in place) → read by P5 (B) and P6 (B, scaled to DU). Restage tile overlays `Ud∪Amat` only at chunk entry (Phase B), before either is written.

---

## (2) Build sequence - incremental, each independently GPU-verifiable vs 0031

Each milestone is a **separate patch** stacked on 0031, **green on `test-backend-ops GATED_DELTA_NET` + greedy-md5 stable before the next is started**. Reference for every step = the `test_gated_delta_net` op's f64/CPU oracle (already in-tree) and 0031's serial-chunked output. **No milestone integrates on top of an unverified one.**

| M | Scope | Patch | GPU verification gate (vs 0031 / op oracle) |
|---|---|---|---|
| **M0** | Re-confirm regime, NO code (scope Phase 0) | - | Profile 0031 (`GDN_CHUNK_MIN` low): confirm GDN prefill bucket dominates + grid-starved at low n_seqs. If not, kill the lever now. |
| **M1** | **DGX microbench (NO kernel yet)** - extend `gdn_gram_bench.cu` with KS/QS/PU/KᵀU microkernels + Phase-D/O T-formation + T·RHS apply, each with f64 host oracle measuring **κ(A)** and tf32-vs-3xtf32 NMSE per rung, incl. adversarial `g∈[-20,-1e-4]` | - | **The cheap go/no-go before multi-week kernel work.** Pass = default precision config (f32 diag + 3xtf32 off-diag + tf32 bulk) reaches ~PoC `3e-9`-grade on benign data and survives the κ(A) weak-decay corner within the ladder. Mirrors the PoC that proved 6.7×→9.3×. |
| **M2** | In-kernel: replace **only** step-1/2 serial Grams (KK/QK) with tensor-core tiles. **C=16, scalar everything else, same occupancy** (scope Phase 1 / PoC integration) | 0043 | test-backend-ops 128-shapes green via KL gate (NMSE if it passes); greedy-md5 stable. |
| **M3** | Add **P3/P4 (KS/QS)** tensor-core (3xtf32) + S restage bridge. Still C=16, scalar solve + scalar O/state | 0044 | Same gate. Isolates the accumulator→B bridge correctness. |
| **M4** | **A-inverse** Phase-D (f32 diag) + Phase-O (3xtf32 off-diag), form T; replace 0031's serial fwd-subst. Still C=16 | 0045 | Same gate + the adversarial-decay op case (this is the amplifier). |
| **M5** | **Apply `U=T·RHS`** + **P5 `P·U`** tensor-core. Still C=16 | 0046 | Same gate. |
| **M6** | **P6 `Kᵀ(D·U)`** tensor-core + **register-resident state** (step-6 accumulator layout) + accumulator→B restage in steady state. State leaves smem here | 0047 | Same gate. Frees the 64KB that forced C=16. |
| **M7** | **Flip C=16→C=64, full dv (CONFIG A ~86KB, 1 blk/SM)**, 8-warp 4×2 grid, PAD=4 smem | 0048 | Gate + **first A/B bench vs sequential** (S_PP at n_seqs≥2). |
| **M8** | **Occupancy:** C=32 + dv-slab grid `(H,n_seqs,n_slabs)` (CONFIG C, 2 blk/SM) + cp.async depth-2; selectors `GDN_CHUNK_C`/`GDN_DV_TILE` | 0049 | Gate + A/B bench across {C=32/dv64, C=32/dv32, C=64/dv64-BW-max}; pick winner per regime. |

---

## (3) Bit-exact / KL gate plan

**md5 is per-path and will NOT match** 0031-serial or the sequential recurrence (different FP reduction order). This is the established `-paged` precedent (`PAGED_BITEXACT_NOTE.md`): per-path md5, validated benign. So:

- **Binding gate = KL** (not strict NMSE): `KLD(tensorcore ‖ f16) ≤ KLD(sequential ‖ f16)` plus a PPL band, on the README s5 harness. NMSE is *expected to fail* at reduced precision (new path on a new path); NMSE-pass is a bonus, KL-pass is the bar.
- **Stability gate:** greedy-md5 **stable across runs** (deterministic), not equal to the serial path.
- **Adversarial op case mandatory:** `g∈[-20,-1e-4]` (the dangerous middle-decay regime where κ(A) grows); strong-decay underflows to 0 (safe), weak-decay is well-conditioned (tf32's 8-bit exponent holds γ range), the middle is the only empirical risk.

**Precision default config (the bet that clears the gate):** f32 diagonal inverse (mandatory, already f32) · **3xtf32 off-diagonal coupling** (default-on, negligible ~64-mma cost) · **tf32** Grams + apply · **bf16** K/Q staging (well-conditioned KK/QK only) · decays/γ/β **always f32 outside the mma** (invariant, not a rung). Hold **P6 state carry at 3xtf32 longest** (it compounds over every chunk).

**3xtf32 ladder (cheapest→dearest) if default misses the gate:** (1) KK Gram→3xtf32; (2) apply **block-diagonal `T_ii·RHS_i`**→3xtf32 (within-window strong coupling, mixed-precision-by-distance); (3) +**δ=1 off-diagonal** apply→3xtf32 (block-boundary adjacent pairs e.g. tokens 15↔16); (4) **full apply**→3xtf32 (≈+2× apply, expensive escape); (5) KS/QS→3xtf32; (6) fall back to direct blocked back-substitution in 3xtf32, else keep 0031's serial path. **Demote order if the gate has margin:** P4→P3, holding P6 at 3xtf32. If even all-3xtf32 misses, the residual is the f32 diagonal solve (already f32) → not fixable by more mma precision → fall to (6). Record the final rung in `PAGED_BITEXACT_NOTE.md` + README s5.

---

## (4) Slot into 0031's existing framework (opt-in, default-OFF)

Same dispatch site - the `if constexpr (!KDA && !keep_rs_t)` block inside `launch_gated_delta_net` (0031 patch, after `init_fastdiv_values`). Extend, don't replace:

- Keep `GDN_CHUNK_MIN` (token threshold, default `INT_MAX` = off) and `GDN_CHUNK_OFF` (kill switch).
- Add **`GDN_CHUNK_TC`** selector: `0` = 0031 serial-solve chunked (fallback, retained), `1` = tensor-core. Add **`GDN_CHUNK_C` ∈ {16,32,64}** and **`GDN_DV_TILE` ∈ {32,64,128}** for A/B; defaults `C=32, DV_TILE=64` (CONFIG C) for serving, `DV_TILE=32` saturator for n_seqs=1.
- New launcher `launch_gdn_chunked_tc<128, C, DV_TILE>` mirrors `launch_gdn_chunked`: `cudaFuncSetAttribute(...MaxDynamicSharedMemorySize...)` **return-checked** (0031 precedent), `grid = dim3(H, n_seqs, n_slabs)`, `block = dim3(256,1,1)`. Per-slab the kernel recomputes A/A⁻¹/gates (dv-independent), dv-slices S/Ud/O.
- **Default OFF** (`gdn_chunk_min=INT_MAX`) exactly as 0031 ships. Flip the default to on **only when** the M8 A/B shows an S_PP win over the tuned sequential recurrence at the serving regime (n_seqs≥2) **and** the KL gate + adversarial op case hold - recorded in README s5 (dev notes / rejected-flat levers) and `PAGED_BITEXACT_NOTE.md`. Until then it ships like 0031: opt-in, regression-free default.
- Extend the test-backend-ops block 0031 added (the `S_v==128` shapes at lines after :9398) so the tc path is exercised at C=64 and C=32 in CI.
- New per-path md5 acknowledged in the dispatch comment (tc-md5 ≠ serial-chunked-md5 ≠ sequential-md5; all benign, KL-validated).

---

## (5) Top 3 risks that could make it NOT beat sequential + kill-criteria

**Risk 1 - Register pressure forces 1 block/SM (the swing factor).** The ~50 working-regs/thread estimate is optimistic; the A⁻¹ blocked solve (in-register `16×16` diag inverse), the accumulator→B restage transpose, and the O+state transient accumulators can push working regs to 80-120. At 256 threads, >128 regs/thread → >32K regs/block → **silently 1 block/SM regardless of the 44KB shared headroom**, and local-memory spills push BW back. *Mitigation ladder:* (i) 12 warps (dilute state 32→21 regs/thread); (ii) `__launch_bounds__(256,2)`; (iii) smaller `DV_TILE`. **Kill criterion:** if after the full ladder the M8 occupancy build still spills to local OR stays 1 block/SM, **and** the CONFIG-A BW-max 1-block path (C=64, dv64, 96KB, cp.async, 4× state-BW cut) **also** fails to beat sequential S_PP at n_seqs≥2 in the A/B bench → the occupancy lever is dead; keep 0031 serial-chunked behind `GDN_CHUNK_TC=0`, record rejected in README s5.

**Risk 2 - Precision: tf32 (and even all-3xtf32) misses the KL gate in the weak-decay/aligned-keys κ(A) corner.** The inverse amplifies error; κ(A) is data-dependent and grows where keys align and decay is weak. **Detected cheaply at M1** (microbench measures κ(A) + per-rung NMSE on the adversarial case *before* the kernel exists). **Kill criterion:** if at M1 the **top of the ladder (all-3xtf32 + f32 diagonal)** cannot reach f32-grade on `g∈[-20,-1e-4]`, OR at M4+ `KLD(tc‖f16) ≤ KLD(seq‖f16)` fails on that op case at the top rung → the tensor-core solve is not numerically viable as a default; fall to ladder rung (6) (direct back-subst 3xtf32); if that also misses, abandon the tc solve and keep 0031 serial. **Fail-fast:** M1 gates this before any multi-week kernel commitment.

**Risk 3 - Grid starvation at n_seqs=1 is structural (H=32 < the ~96 blocks needed for 2 blk/SM × 48 SM).** Only `DV_TILE=32` (4 slabs) fully saturates a single stream, and it pays ~1.5-2× redundant-A flops (A/A⁻¹/gates recomputed per slab) plus the per-chunk restage. **Kill criterion:** if the M8 bench shows single-stream (n_seqs=1) S_PP is slower than sequential even at full saturation (dv32×4) due to redundant-A + restage overhead, **and** the batched regime (n_seqs≥2) gain also fails to materialize → the lever only helps a regime the target workload doesn't hit → keep default-OFF, ship as opt-in experiment only, record. (If n_seqs≥2 *does* win, ship enabled for the serving regime and gate single-stream back to sequential via `GDN_CHUNK_MIN` + an n_seqs check - a partial, honest win.)

**Overarching kill gate:** the disposition is the bench, not the theory. The kernel flips to default-on only when it beats the tuned sequential recurrence at the serving regime AND clears the KL + adversarial gates. Any milestone that regresses test-backend-ops or md5-stability halts the stack until fixed; M1 and M0 are the cheap fail-fast exits before the expensive kernel work.