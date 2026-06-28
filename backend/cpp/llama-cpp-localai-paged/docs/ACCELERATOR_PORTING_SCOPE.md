# Accelerator-porting scope: bringing the paged backend's portable benefits to Metal / SYCL / Vulkan (+ a ROCm note)

Source-only analysis (no GPU, no build) of which `llama-cpp-localai-paged` benefits
are portable off the CUDA family, and what each port costs per accelerator. This is
the umbrella doc; it BUILDS ON, and does not repeat,
[`UPSTREAM_LAYER2_SCOPE.md`](UPSTREAM_LAYER2_SCOPE.md) (the GDN/SSM fusion kernel
scope) - that doc remains the authoritative reference for benefit #1 below.

The backend ships **CUDA-only** today (README sections 4c, 8): off-CUDA the fusions
gate off (patch 0030) and NVFP4 falls back to dequant, so it is
neutral-to-slightly-negative there and non-CUDA users run the stock `llama-cpp`.
"Porting the benefits" is the upstream-contribution track that would make these
wins real on the other accelerators. Methodology for the work itself is in
[`.agents/vllm-parity-methodology.md`](../../../../.agents/vllm-parity-methodology.md).

We have **no Metal / SYCL / Vulkan / ROCm hardware here**, so every port is gated
by `test-backend-ops` (backendX-vs-CPU) **on the target hardware** - the same gate
discipline the existing layer-2 doc sets out.

--------------------------------------------------------------------------------
## 0. The four benefits and their portability class

| # | Benefit (patches) | Portable off CUDA? | Where scoped |
|---|---|---|---|
| 1 | **GDN/SSM decode fusions** (0018-0022, 0028) - in-place state write-back, fused recurrent-state gather, conv-state in-place fusion, o_proj MMQ reshape, occupancy retune | YES - per-backend KERNEL work | [`UPSTREAM_LAYER2_SCOPE.md`](UPSTREAM_LAYER2_SCOPE.md) (consolidated in section 1 here) |
| 2 | **Paged KV in-kernel block-table flash-attn read** (0009-0011) | YES - per-backend KERNEL work | **Section 2 here (the new analysis)** |
| 3 | **Decode-first prefill scheduler** (0013/0016) | YES - FREE, host-side, zero kernel work | Section 3 here |
| 4 | **NVFP4 FP4-MMA + its decode levers** (0017/0023/0025) | NO (Blackwell FP4-MMA) - out of scope; two analogues flagged | Section 4 here |

The two kernel-bearing tracks (#1 and #2) share an identical port SHAPE - they touch
the same decode kernel(s), the same `supports_op`, the same dispatch guard, and
sequence the same way (ops-first PR, then one PR per backend). They should be
**bundled into one per-backend PR**, not pursued as two separate efforts; section 5
sequences them together. Tracks #3 (free) and #4 (out of scope) are independent.

--------------------------------------------------------------------------------
## 1. Benefit #1 - GDN/SSM decode fusions (consolidated; full scope is the layer-2 doc)

Do not re-derive this here. [`UPSTREAM_LAYER2_SCOPE.md`](UPSTREAM_LAYER2_SCOPE.md)
already establishes, and this doc adopts wholesale:

- The base `GGML_OP_GATED_DELTA_NET` + `GGML_OP_SSM_CONV` + `GGML_OP_SSM_SCAN`
  kernels **already exist on Metal, Vulkan AND SYCL**, so the Qwen3.6 hybrids RUN
  on all three today via the upstream non-fused path. Layer-2 is the decode
  SPEEDUP, not "make it run." (NB: the README section 4c no longer carries the
  stale "no Vulkan kernel" line that the layer-2 doc section 0 was written to
  correct - that correction has since been folded into the README, so treat
  layer-2 section 0 as historical context, not a live correction.)
- The four fusion ops (A in-place state 0018, B fused state gather 0019, C
  conv-update in-place 0021, D conv-tap gather 0028) reuse the existing op enums
  with extra `src[]` discriminators; only OP C is a genuinely new kernel, the rest
  redirect the read source / write target of the EXISTING kernel. The builders,
  CPU reference kernels, model graph and `test-backend-ops` cases are SHARED and
  already done.
- Per-backend net-new work, effort and gotchas: **SYCL easiest** (near-verbatim
  CUDA mirror, ~250-350 LOC, no shader-gen), **Metal medium** (~350-500 LOC,
  fixed 32 simdgroup = simplest bit-exactness), **Vulkan hardest** (~450-650 LOC +
  shaders-gen + descriptor growth + per-vendor subgroup validation).
- Bit-exactness is per-backend BY CONSTRUCTION (the fusions redirect addresses, not
  the f32 reduce order); gated by `test-backend-ops` (backendX-vs-CPU).
- Upstream path: ops-first PR (incl. the capability-driven replacement for patch
  0030's backend-name allow-list), then one PR per backend.

The value/effort ranking from that doc (**Metal 1st, SYCL 2nd, Vulkan 3rd**) is
adopted unchanged here and, as section 5 shows, coincides with benefit #2's ranking
- which is why the two bundle cleanly per backend.

--------------------------------------------------------------------------------
## 2. Benefit #2 - paged KV in-kernel block-table flash-attn read (NEW scope)

### 2.0 What it is, and why it is the lever that makes paged KV non-negative off-CUDA

On CUDA, patches 0009-0011 replaced the per-step host-side K/V gather (patch 0003)
with an **in-kernel paged read**. `ggml_flash_attn_ext` gained an optional
`src[5]` = an I32 block table `[n_view, n_stream]` in token-POSITION order; the
fattn vec/tile kernel maps logical KV index `j` to physical cell
`block_table[seq*ne11 + j]` and reads `K0 + cell*nb11` / `V0 + cell*nb21` in place,
so the `get_rows` of K and V (the bulk of the gather) is gone. A null block table is
the stock contiguous read, byte-identical. Position ordering keeps the online-softmax
reduction order identical to stock, so it is bit-exact (CPU/batch1) by construction.

The crucial point for portability: **the entire host side is already
backend-agnostic.** The block-table fill (`llama_kv_cache::get_block_table`), the
K/V views, the mask compaction, the `input_block_table` graph input, and the
`ggml.c` / `ggml.h` builder (`ggml_flash_attn_ext_set_block_table`) all live in
`src/` and `ggml/...` shared code. The ONLY per-backend work is, in each backend's
flash-attn kernel: (a) thread one extra source through to the kernel, and (b) do the
indexed read at the K/V load sites. The CPU reference already does it (patch 0009,
`ops.cpp`).

Off-CUDA today the paged path falls back to the **host-side gather** (patch 0003),
which the README section 4c measured as neutral-to-slightly-negative on the M4
(~0-3% slower decode / ~2-8% slower prefill vs stock's contiguous read - pure
overhead, because the in-kernel read that *recovers* the gather cost is CUDA-only).
**Porting the block-table read is exactly what flips paged KV from
"neutral-to-negative" to "neutral-to-positive" off CUDA** - it removes the gather
overhead so paged KV's memory-management and prefix-sharing wins come for free
instead of at a decode tax. (The big decode multipliers on the hybrids are still the
benefit-#1 GDN fusions; this benefit is what makes the paged *allocator* pay its own
way off CUDA.)

### 2.1 The cross-cutting finding (applies to all three backends)

The indexed per-cell read only fits the **vec / scalar decode kernel**. Every
backend's FAST attention path - CUDA mma, Metal `simdgroup_load` MM, Vulkan
coopmat2, SYCL tile - loads K/V as **contiguous tiles** (8-cell `simdgroup_load`,
`coopMatLoadTensorNV` over a linear stride, shared-memory tile loads) that cannot
express an arbitrary per-cell gather without a staging pre-pass. This is exactly why
the CUDA port (0009-0010) wired ONLY the vec kernel and added a dispatch guard
(`if (dst->src[5]) force vec`).

So each port mirrors that: **route any FA op carrying a block table onto the vec /
scalar kernel; leave the fast MM path contiguous-only**, and keep the null-table
contiguous read on the fast path untouched. The decode shape (1 query token/stream)
naturally lands on or near the vec/scalar kernel on all three, so this is a small
routing change, not a rewrite of the fast path.

### 2.2 SYCL - EASIEST (near line-for-line CUDA mirror)

- **Exists today:** `ggml-sycl/fattn-vec.hpp` is a DPCT-style near-verbatim mirror
  of CUDA `fattn-vec.cuh`; the kernel signature ends in the same `nb11..nb33`
  cluster the CUDA patch appends `const int* block_table` to (fattn-vec.hpp:65-76).
  Args are passed by SYCL lambda value-capture - **no descriptor/binding/push-
  constant bookkeeping at all** (strictly easier than CUDA). `supports_op`
  (`fattn.cpp` -> `ggml_sycl_get_best_fattn_kernel`) needs no change to ACCEPT
  `src[5]`.
- **Port shape (value: medium / effort: LOW):** append `const int* block_table`
  to the kernel + `fattn_kernel_t` typedef + `lauch_kernel`/`launch_fattn`
  (sourcing `dst->src[5]->data`); 3 read-site substitutions (K at line 318, V at
  389 and 410): `K0 + block_table[seq*ne11 + k_VKQ_0 + i_KQ]*nb11`.
- **Two SYCL-specific gotchas:**
  1. **Pointer pre-advance.** The vec kernel advances `K`/`V` by `k_VKQ_0` OUTSIDE
     the inner read (fattn-vec.hpp:293-300), so `i_KQ`/`k` are tile-local. The port
     must keep an UN-advanced base `K0`/`V0`, drop the per-iteration `K +=`/`V +=`
     on the paged path, and reconstruct the absolute cell. Get this wrong and you
     read the wrong cells with NO compile error.
  2. **Dispatch guard is bigger than CUDA's.** f16-GQA decode routes to the TILE
     kernel, not vec (`fattn.cpp:198-208` fall-through). Add
     `if (dst->src[5]) return BEST_FATTN_KERNEL_VEC;` near the top of
     `ggml_sycl_get_best_fattn_kernel`. The shared `fattn_kernel_t` typedef means
     the tile kernel must gain a matching ignored `block_table` param (or split the
     typedef) - a trivial chore.
- **Bit-exact:** sub-group width (16) is fixed and the indexed read does not touch
  lane assignment, loop bounds, or the XOR-reduction stride - reduction order is
  invariant, so the paged vec path is byte-identical to SYCL's own contiguous vec
  path. Gate: `test-backend-ops` FLASH_ATTN_EXT (with a block-table case) on Intel
  GPU.

### 2.3 Metal - EASY-MEDIUM (decode already routes to the vec kernel)

- **Exists today:** decode (1 query token/stream, GQA) dispatches to
  `kernel_flash_attn_ext_vec` (`ggml-metal-ops.cpp` `..._use_vec`: `ne01 < 20`).
  Metal IS a true vec-equivalent (not a single unified FA kernel), and the vec
  kernel's quantized K/V branches ALREADY compute a per-cell base address
  (`k + ((ic + NE*cc + ty)*nb11)`, ggml-metal.metal:6934 / V at :7045) - so a
  per-cell indexed read is unambiguously admissible. `supports_op`
  (`ggml-metal-device.m` FLASH_ATTN_EXT) inspects no src count, so `src[5]` is
  accepted as-is.
- **Port shape (value: HIGH / effort: EASY-MEDIUM):** append a
  `device const char * block_table` param after `dst` (**buffer index 8** for vec)
  + a kargs field + a `has_block_table` function-constant; reuse the existing
  "bind dummy when null" idiom for a missing table; substitute the cell index with
  `block_table[seq*ne11 + cell]` at the K reads (lines 6919/6934) and V reads
  (7032/7045) - a localized rewrite of ~2 loops (the fast path must adopt the
  per-cell base form the quantized branch already uses).
- **Gotcha:** the **non-vec MM kernel is a HARD blocker** -
  `simdgroup_load(..., NS10, ...)` reads 8 physically-CONTIGUOUS KV cells as one
  matrix tile (lines 6160 / 6339-6363); an arbitrary gather can't be a single
  strided matrix load. Mitigate exactly as CUDA did: force any block-table op onto
  the vec kernel in `..._use_vec` (ggml-metal-ops.cpp:2517); leave the MM path
  contiguous-only. Also watch a NAME COLLISION: `kernel_flash_attn_ext_blk` is an
  existing mask-skip optimization, NOT a paged block table.
- **Bit-exact:** fixed 32-wide simdgroup + address-only redirect = byte-identical to
  Metal's own vec contiguous path. Gate: `test-backend-ops` on Apple Silicon.

### 2.4 Vulkan - MEDIUM (the fast NVIDIA decode path cannot do it)

- **Exists today:** three FA shaders - `flash_attn.comp` (scalar/vec),
  `flash_attn_cm1.comp` (coopmat1, stages K/V through shared memory),
  `flash_attn_cm2.comp` (coopmat2, the fast NVIDIA path). FA uses **7 descriptor
  bindings (0-6)**; `supports_op` (`ggml-vulkan.cpp` FLASH_ATTN_EXT) checks
  specific srcs only, no count check; but `src[5]` is **not even threaded today** -
  `ggml_vk_flash_attn` stops at `src[4]` (ggml-vulkan.cpp:14537), so wiring it
  through is part of the work.
- **Port shape (value: HIGHEST breadth / effort: MEDIUM):** add binding 7 in the
  shader(s), bump `7`->`8` in the three `ggml_vk_create_pipeline` calls (:3997,
  :4033, :4070) and the two dispatch subbuffer lists (passing a dummy when null),
  and wrap the indexed read in one `phys_kv()` helper applied at the ~4 K + 2 V
  load sites (flash_attn.comp; the logical index is the same `(j*Bc + ...)`
  expression at every site).
- **Two gotchas, one structural:**
  1. **Push constants are FULL.** `vk_flash_attn_push_constants` is exactly
     128 bytes with a `static_assert(... <= 128)` (the Vulkan guaranteed minimum) -
     **no room for a new field.** Signal "block-table enabled" via the existing
     `Flags` spec constant (flash_attn_base.glsl, `constant_id=10`, already
     bit-packed) - add a `BLOCK_TABLE_ENABLE` bit. The per-seq stride is already
     `p.KV`; the seq index is derivable in `init_indices()`.
  2. **coopmat2 (the fast NVIDIA GQA-decode path) is INCOMPATIBLE.** Its K/V load
     is a hardware `coopMatLoadTensorNV` over a LINEAR stride
     (flash_attn_cm2.comp:307-313/377-383); the decode callback only dequantizes,
     it cannot remap the physical address. The indexed read drops cleanly into
     **scalar** (which non-GQA decode already uses) and **cm1** (which stages
     through shmem - remap the staging loop), but **not cm2**. With a block table
     present, NVIDIA GQA decode falls back to scalar/cm1 (slower than cm2, still
     correct); the **null-table path keeps using cm2 unchanged**. AMD/Intel (no
     cm2) are fully covered by scalar/cm1.
- **Net positive?** Yes. Non-GQA decode already runs scalar (paged read ~free);
  AMD/Intel covered; only NVIDIA GQA decode trades cm2 for scalar/cm1 *when a table
  is supplied*, and paged KV's payoff is allocator/memory + prefix-sharing, not raw
  FA throughput, so the trade is contained and the fast contiguous path is
  untouched.
- **Bit-exact:** the read is a per-thread scalar load, subgroup-size agnostic
  (already abstracted via the `SubGroupSize` spec constant); position ordering keeps
  the reduction order identical, so byte-identical to the backend's own
  scalar/cm1 contiguous path. **Build burden is low** - these are EXISTING shader
  variants recompiling (no new `string_to_spv` shape), so no shaders-gen matrix
  growth. Gate: `test-backend-ops` per vendor (AMD + Intel + NVIDIA).

### 2.5 Benefit-#2 ranking and the shared dispatch/supports_op pattern

| backend | value | author effort | structural risk | rank |
|---|---|---|---|---|
| SYCL  | medium (Intel GPU) | **LOW** (line-for-line; no bindings) | low (pointer pre-advance; force-vec guard) | easiest |
| Metal | **HIGH** (largest non-CUDA base) | EASY-MEDIUM (decode = vec already) | medium (MM blocker -> force vec) | mid |
| Vulkan| **HIGHEST breadth** (AMD+Intel+NVIDIA) | MEDIUM (7->8 bindings; Flags bit) | medium (cm2 can't; full push-const) | hardest |

Common to all three (mirrors CUDA 0009-0010): (1) `supports_op` needs no change to
ACCEPT `src[5]`; (2) a **dispatch guard forces any block-table op onto the
vec/scalar kernel**; (3) the fast MM/coopmat2 path stays contiguous-only and the
null-table read on it is byte-identical to stock.

--------------------------------------------------------------------------------
## 3. Benefit #3 - decode-first prefill scheduler (FREE portable win, confirmed)

Patches 0013 (static `LLAMA_PREFILL_BUDGET`) and 0016 (dynamic decode-first
`max(n_ubatch, T-D)`) are **pure host-side scheduler policy inside `update_slots()`
with zero libllama / zero ggml-backend changes** (README sections 2, 3). They change
only the *count* of prefill tokens admitted per step; they touch no kernel, no
`supports_op`, no device code. They are therefore **already backend-portable with no
per-accelerator work** - they run identically on Metal, SYCL, Vulkan, ROCm, CPU.
Byte-identical when off (default-off / short prefill == upstream `-b` chunking).

This is the cheapest portable benefit: it needs no port at all, only the decision to
leave it enabled in the (currently CUDA-only) build, or to upstream the policy. The
only reason it is not "live everywhere" today is that the backend ships CUDA-only;
the code itself is accelerator-neutral. If the scheduler levers are upstreamed
independently of the kernels, they help any llama.cpp build on any accelerator at
once - the lowest-effort, broadest-reach contribution of the whole series.

--------------------------------------------------------------------------------
## 4. Benefit #4 - NVFP4 FP4-MMA (NOT portable) + two backend-agnostic analogues

The NVFP4 decode track is **Blackwell-specific and out of scope** for accelerator
porting: Metal, SYCL, Vulkan and ROCm/AMD lack native FP4-MMA (Metal `supports_op`
already excludes NVFP4 from `MUL_MAT`/`MUL_MAT_ID`/`GET_ROWS`; on non-Blackwell the
FP4 path dequants). Patch 0017 (dense FP4-GEMM occupancy tune) ships only as the
parity gate + default-off instrumentation even on CUDA, so there is nothing to port.

Two of the NVFP4 *decode levers*, however, have backend-agnostic analogues worth a
note (do not over-claim - these are observations, not scoped ports):

- **0023 (NVFP4 activation-quantize de-dup)** - the IDEA generalizes, the patch does
  not. The MoE broadcast up/gate projections re-quantize the same token activation
  once per expert; 0023 quantizes the unique activations once and byte-copies them
  into the expert-gathered layout. Any backend whose MoE path requantizes a shared
  activation per-expert (e.g. a Q8 activation-quant before an integer-dot MoE GEMM)
  could dedup the same way. It is NOT NVFP4-specific in PRINCIPLE - but it IS the
  one quant-specific patch in the series (README section 6), so a port is a
  per-backend MoE-quant investigation, not a lift-and-shift. Low priority.
- **0025 (MoE decode re-graph / `LLAMA_MOE_FORCE_GRAPHS`)** - keeping the graph/
  capture path on across the grouped-MMQ MoE decode step is a CUDA-graphs concept.
  Metal/Vulkan/SYCL have their own command-buffer/graph reuse machinery; the
  generalizable finding is "the grouped MoE decode step has no host sync, so it is
  safe to keep in a captured/replayed command buffer." Whether each backend's graph
  layer already covers this is a per-backend question. The methodology note (README
  dev notes: graph/stream coverage was a FLAT lever beyond 0025 on CUDA) is the
  more durable takeaway - do not expect a large graph-coverage win on any backend.

Neither analogue is on the critical path; both are recorded so the next person does
not mistake them for free ports.

--------------------------------------------------------------------------------
## 5. Combined sequencing and top recommendations

Benefits #1 (GDN fusions) and #2 (block-table FA read) share the port shape
(vec/scalar decode kernel + `supports_op`/dispatch guard + ops-first-then-per-backend
PR) and rank in the SAME order per backend. So sequence them TOGETHER, per backend,
behind one shared ops-first PR:

1. **PR #1 - OPS (largely done, upstreamable as-is):** the `ggml.h`/`ggml.c`
   builders, the CPU reference kernels, the CUDA kernels, the `test-backend-ops`
   cases (GDN fusions AND a FLASH_ATTN_EXT block-table case), and the
   **capability-driven gate** replacing patch 0030's backend-name allow-list (make
   `supports_op` + the dispatch guard authoritative, so routing falls out of the
   normal scheduler fallback and no backend name is hard-coded). Independently
   mergeable.
2. **PR #2 - Metal:** GDN fusion kernels (layer-2 doc) + block-table read into
   `kernel_flash_attn_ext_vec` + the force-vec routing guard. Gate on Apple Silicon.
3. **PR #3 - SYCL:** the near-verbatim CUDA mirror of both tracks + the force-vec
   guard. Gate on Intel GPU.
4. **PR #4 - Vulkan:** GDN fusion shaders + the scalar/cm1 block-table read (cm2
   stays contiguous, falls back when a table is present) + the `Flags` spec-constant
   bit + the 7->8 binding bump. Gate per vendor.

Do NOT bundle the backends into one PR (each needs its own hardware for
`test-backend-ops`; reviewers are backend-specialized; a regression in one must not
block the others).

### Top recommendations

1. **Metal first, both benefits together.** Largest non-CUDA LocalAI base; the
   decode shape already routes to the Metal vec kernel (block-table read is
   EASY-MEDIUM there) and the base GDN/conv kernels already exist (fusions are
   MEDIUM); fixed 32-wide simdgroup makes bit-exactness the simplest of the three.
   Highest value at moderate effort.
2. **SYCL second as the cheap mechanical follow-on.** Both tracks are near
   line-for-line CUDA mirrors with no binding/shader-gen bookkeeping, so it is
   low-cost insurance even though the Intel-GPU audience is smaller. Budget the
   effort on the two SYCL gotchas (pointer pre-advance; the force-vec guard since
   f16-GQA decode routes to tile), not on plumbing.
3. **Vulkan last as the high-breadth capstone.** Reaches AMD + Intel + NVIDIA, but
   carries the most host glue and the coopmat2 limitation (NVIDIA GQA decode trades
   the fast path for scalar/cm1 only when a table is present). Do it once the
   pattern is proven on Metal + SYCL.

A cheaper variant (from the layer-2 doc, reaffirmed): ship **Metal + SYCL together**
right after the ops PR and treat Vulkan as a separate later effort.

--------------------------------------------------------------------------------
## 6. ROCm note

ROCm is in the **CUDA family**, not a separate port: patch 0030's allow-list already
admits `"CUDA"/"ROCm"/"MUSA"`, and the CUDA kernels compile for HIP, so benefits #1
and #2 are largely already-built or near-free on ROCm rather than a from-scratch
accelerator port. Two caveats:

- **FP4-MMA (benefit #4) stays NVIDIA-Blackwell-only** - AMD has no native FP4-MMA,
  so the NVFP4 path dequants on ROCm exactly as elsewhere.
- **The block-table read's force-vec routing matters on AMD too.** The AMD fast FA
  path is the wmma/mma kernel (`fattn-wmma-f16`), which - like CUDA mma, Metal MM
  and Vulkan cm2 - ignores the block table; the CUDA dispatch guard already forces a
  block-table op onto the vec kernel, so ROCm inherits correct routing, but the
  perf trade (vec vs wmma for AMD GQA decode with a table present) should be
  measured on AMD hardware before claiming a win. The GDN fusions, being plain
  CUDA-C, port to HIP with the rest of the CUDA path.

Net: ROCm is a "validate, don't re-port" follow-up - confirm the HIP build picks up
the fusions + the force-vec block-table routing and gate it with `test-backend-ops`
on an AMD GPU. It is genuinely separate from, and lighter than, the Metal / SYCL /
Vulkan ports.

--------------------------------------------------------------------------------
## 7. Summary

- **Benefit #3 (decode-first scheduler) is free and already portable** - host-side
  policy, zero kernel work; it only needs to be left enabled / upstreamed.
- **Benefits #1 (GDN fusions) and #2 (block-table FA read) are the real ports** -
  both are vec/scalar-decode-kernel + `supports_op`/dispatch-guard changes, both
  rank Metal-then-SYCL-then-Vulkan, and they bundle into one per-backend PR behind a
  shared ops-first PR.
- **Benefit #2 is the lever that makes paged KV non-negative off CUDA** - it removes
  the host-gather overhead the README measured as neutral-to-slightly-negative on
  the Mac. Feasibility: SYCL EASY, Metal EASY-MEDIUM, Vulkan MEDIUM. The universal
  constraint is that only the vec/scalar kernel admits the indexed read; the fast
  MM/coopmat2 path is contiguous-only, so route block-table ops onto vec (as CUDA
  already does) and leave the fast path's null-table read byte-identical.
- **Benefit #4 (NVFP4 FP4-MMA) is out of scope** (Blackwell only); 0023's de-dup and
  0025's graph-coverage have backend-agnostic *ideas* but no lift-and-shift port.
- **ROCm rides the CUDA path** (validate, don't re-port); FP4-MMA stays Blackwell-only.
- Everything is bit-exact per-backend BY CONSTRUCTION (position-ordered table +
  address-only redirect = identical reduction order), gated by `test-backend-ops`
  (backendX-vs-CPU) **on the target hardware**, which we do not have here.
</content>
</invoke>
