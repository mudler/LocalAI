# Layer-2 upstream scope: native fused-GDN kernels for Metal / Vulkan / SYCL

Source-only analysis (no GPU, no build) of what it would take to give the
gated-DeltaNet (GDN / SSM) decode fusions native kernels on the non-CUDA compute
backends, so the patch-series decode win extends past CUDA-family hardware.

In our changeset (patches 0018-0030) these fusions ship with CUDA native kernels
+ CPU reference kernels ONLY; patch 0030 force-gates them OFF on Metal / Vulkan /
SYCL (a CPU-fallback fused op would regress via the device round-trip, and a
backend that ran the plain op on the discriminated node would silently
miscompute). "Layer 2" is the upstream work that adds the missing native kernels.

This doc was written against the ggml backend trees in
`backend/cpp/llama-cpp-paged-dev` (upstream base #24732, one commit OLDER than the
series pin `c299a92c` #25045, with only the two paged-KV patches applied - neither
touches GDN/SSM). So every "kernel already exists" statement below is a
conservative lower bound: the pin has at least these kernels.

--------------------------------------------------------------------------------
## 0. Headline finding (correct a stale assumption first)

The series README (section 4c) says "the gated-DeltaNet op has no Vulkan kernel
upstream, so the Qwen3.6 hybrid models assert / fall back and don't run there."
**That is now stale.** All three backends already carry the BASE compute ops:

| op                     | Metal                              | Vulkan                                   | SYCL                            |
|------------------------|------------------------------------|------------------------------------------|---------------------------------|
| GGML_OP_GATED_DELTA_NET| `kernel_gated_delta_net_impl` (f32, NSG 1/2/4) | `gated_delta_net.comp` (d16/32/64/128 x kda, shmem/cluster/nocluster variants) | `gated_delta_net.cpp` (`launch_gated_delta_net<KDA,keep_rs>`) |
| GGML_OP_SSM_CONV       | `kernel_ssm_conv_f32_f32` (+ `_4`, + batched) | `ssm_conv.comp` (+ APPLY_BIAS, APPLY_SILU specialization consts) | `ssm_conv.cpp` (`kernel_ssm_conv`) |
| GGML_OP_SSM_SCAN       | yes                                | `ssm_scan.comp` (mamba2)                 | `ssm_scan.cpp` (mamba2)         |

Verified: Vulkan `gated_delta_net.comp` was last touched at the upstream base
commit (#24732), not by any LocalAI patch. So the GDN COMPUTE op is present on
Metal, Vulkan AND SYCL. The Qwen3.6 hybrids therefore DO run on all three today
(via the upstream non-fused path that 0030 routes to). The Layer-2 value-add is
the decode SPEEDUP from the fusions, NOT enabling the model to run at all.

Consequence: the GDN-compute op being "partly there" is true on every backend,
not just Metal. What is still missing per backend is only the FUSION plumbing
(in-place write-back target, the ids gather read, and the conv-update kernel) -
a materially smaller scope than "port GDN from scratch."

--------------------------------------------------------------------------------
## 1. Per-op semantics (the four fusions to port)

All four reuse an existing GGML_OP enum with extra `src[]` slots as a
discriminator; none adds a new enum value. f32 throughout. The arithmetic core
is IDENTICAL to the upstream non-fused op; only the read source and/or the write
target are redirected. That single fact drives the whole bit-exactness story
(section 3).

### OP A - `ggml_gated_delta_net_inplace` (patch 0018)
- Enum `GGML_OP_GATED_DELTA_NET`, discriminated by a non-null `src[6]` =
  `state_dst` (a contiguous `[S_v*S_v*H, n_seqs]` view into the recurrent-state
  cache at `kv_head`). K == 1 only.
- Semantics: run the standard GDN recurrence, but write the FINAL recurrent state
  directly into `state_dst` instead of appending it to the op output. The op
  output then carries only the attention scores. Removes the per-layer per-step
  ~full-state D2D copy-back (the 0018 win).
- Race (in-place read == write): each (seq, head) block owns a disjoint cache
  slot. The kernel loads the whole prior state `s0` into per-thread registers
  (`s_shard` on CUDA, `ls[NSG]` on Metal, the column shard on Vulkan/SYCL)
  BEFORE the ring write, so reading and writing the same slot is safe.

### OP B - `ggml_gated_delta_net_inplace_ids` (patch 0019)
- Adds `src[5]` = FULL state cache `[S_v,S_v,H,n_rs_slots]`, `src[7]` = `ids`
  (I32, per-seq source slot == the recurrent-state `s_copy`), `op_param[1]` =
  `rs_head` (destination base slot). Still has the OP-A `src[6]` in-place target.
- Semantics: read each sequence's prior state directly from `cache[ids[seq]]`
  (mirrors `ggml_ssm_scan`'s ids source), eliminating the `ggml_get_rows`
  materialization. Combined with OP A the op now reads AND writes the cache in
  place.
- Race: identity sequences (`ids[s] == rs_head + s`, the steady AR-decode case)
  read s0 in place from the destination slot (safe via the register snapshot
  above). Non-identity sequences (reorder / rs_zero remap) are first copied by a
  TINY separate gather kernel (`gdn_gather_nonident`, one block/seq) into a
  DISJOINT scratch that the recurrence then reads, so the recurrence never reads
  a slot another block is writing. Value-preserving memcpy -> bit-identical to
  the get_rows path.

### OP C - `ggml_ssm_conv_update_inplace` (patch 0021)
- Enum `GGML_OP_SSM_CONV`, discriminated by a non-null `src[3]` =
  `conv_state_dst` (`[(K-1)*channels, n_seqs]` in-place ring view).
  `src[0]` = conv_states `[K-1, channels, n_seqs]`, `src[1]` = conv_kernel
  `[K, channels]`, `src[2]` = x_cur `[channels, 1, n_seqs]`. `op_param[0]` =
  fuse_silu.
- Semantics (decode, n_seq_tokens == 1): per (channel, sequence) assemble the
  width-K conv window in registers from the K-1 cached taps + the current token,
  compute the depthwise conv with the SAME ascending-tap FMA order as plain
  `ssm_conv` (`tap0*w0 + ... + xc*w_{K-1}`, then `+0.0f` to match plain conv's
  `sumf += b` with b==0), optionally fold SiLU, write the conv output
  `[channels,1,n_seqs]`, and write the 1-token-shifted ring state back in place.
  Replaces the 4-op decode conv chain (transpose + concat + conv + silu + ring
  cpy).
- Race: read source (gathered taps) and write target (cache view) are disjoint
  buffers -> race-free by construction, no ids/identity logic.

### OP D - `ggml_ssm_conv_update_inplace_ids` (patch 0028)
- Same enum, discriminated by a non-null `src[4]` = `ids`; `src[0]` becomes the
  FULL conv cache `[K-1, channels, n_cells]`; `op_param[1]` = rs_head.
- Semantics: gather-free conv-update - read each sequence's prior taps from
  `cache[ids[s]]` in-kernel (no get_rows). Identity reads in place from
  `conv_state_dst`; non-identity gathered into a disjoint scratch first by a tiny
  `ssm_conv_gather_nonident` kernel. The window is copied to a local array
  BEFORE the (possibly aliasing) ring write so the identity read==write slot is
  correct. Bit-identical to get_rows + OP C.

### Net new kernels vs reuse, per op
- OP A: NOT a new compute kernel - a write-target redirection of the EXISTING
  GDN kernel + 1 buffer binding + a supports_op/op-handler branch.
- OP B: the GDN kernel gains a per-seq read-base select (identity vs scratch) +
  1 ids binding + rs_head param + 1 tiny gather kernel.
- OP C: a GENUINELY NEW kernel on each backend. The existing `ssm_conv` computes
  a windowed reduction over a PRE-concatenated input; it does not assemble the
  window from cached taps + the current token, fold silu, or write the shifted
  ring state. This is the largest net-new piece.
- OP D: the OP-C kernel gains the read-base select + 1 ids binding + rs_head + 1
  tiny conv gather kernel.

The `ggml.h` / `ggml.c` builders, the CPU reference kernels, the model-graph
emission (`delta-net-base.cpp`, qwen35*), and the `test-backend-ops` cases are
SHARED and already done by patches 0018/0019/0021/0028. The only NEW per-backend
work is the kernel(s) + the backend wiring.

--------------------------------------------------------------------------------
## 2. Per-backend: authoring model, effort, gotchas, wiring

### 2.1 Metal (MSL)

Authoring model: `.metal` MSL source (`ggml-metal.metal`), function-constant
specialization (e.g. `FC_GATED_DELTA_NET`), kernels templated on `NSG`; host
glue split across `ggml-metal-ops.cpp` (`ggml_metal_op_*` encode), the pipeline
lookup in `ggml-metal-device.cpp`/`.m`, the kargs struct in `ggml-metal-impl.h`,
and `supports_op` in `ggml-metal-device.m`. Threadgroup model; Apple GPU
simdgroup width is a FIXED 32, `simd_sum` for the per-column reduce.

Effort: MEDIUM. ~350-500 LOC. The GDN and plain-ssm_conv kernels already exist
and are ergonomic to extend. OP A is a write-base redirect of the existing
`kernel_gated_delta_net_impl` (its tail already does
`dst_state = dst + attn_size + state_out_base; dst_state[is] = ls[j]` after
loading `ls[]` into registers - just point `dst_state` at the `state_dst` buffer
and add the binding). OP C is the one net-new MSL kernel (Metal has NO bias/silu
ssm_conv variant today - only plain + `_4` + batched - so the silu-fold and ring
write are both new). Host glue spans 3-4 files.

Gotchas:
- In-place race: the existing kernel ALREADY snapshots the state column into
  `ls[NSG]` registers before writing, so OP A/B are safe with no barrier; OP C/D
  must mirror the `float window[K]` local-copy-before-write that CPU/CUDA use.
- Discriminated SSM_CONV: `supports_op` for `GGML_OP_SSM_CONV` currently returns
  `has_simdgroup_reduction` with NO check of `src[3]`/`src[4]`; GDN returns
  `has_simdgroup_reduction && src[2]->ne[0] % 32 == 0` with NO check of
  `src[6]`/`src[7]`. Both must be tightened (accept the discriminated variant
  only once the kernel exists) AND `ggml_metal_op_ssm_conv` /
  `ggml_metal_op_gated_delta_net` must branch on the extra src to pick the kernel.
- Bit-exactness: fixed 32-wide simdgroup makes this the SIMPLEST of the three -
  the fused variant only redirects addresses, so it is bit-identical to Metal's
  own non-fused path by construction (the conv per-channel FMA needs the exact
  ascending order + the `+0.0f`).
- The kargs struct grows by the `state_dst` / `ids` / `rs_head` fields; a new
  pipeline name (or a function-constant branch) distinguishes the variants.

### 2.2 Vulkan (GLSL .comp -> SPIR-V)

Authoring model: GLSL `.comp` in `vulkan-shaders/`, compiled at build time by
`vulkan-shaders-gen` into embedded SPIR-V byte arrays (`gated_delta_net_f32_data`
etc.); pipeline creation in `ggml-vulkan.cpp` declares the binding count +
push-constant size; a push-constant struct per op; host dispatch `ggml_vk_*`
binds subbuffers; `supports_op` in the device support function. Subgroup size
VARIES by vendor (NVIDIA 32, AMD 64, Intel 8/16/32).

Effort: HARDEST. ~450-650 LOC + the most build/host glue. Same kernel logic as
Metal/SYCL, but every new shader or variant requires: the shaders-gen regen, a
new `ggml_vk_create_pipeline` registration with an explicit binding count and
push-constant size, a new/extended push-constant struct (add `rs_head`), and
GROWING the descriptor binding set from the current 7 (`src[0..5]` + dst) to 8-9
(`state_dst`, `ids`). The GDN host dispatch hardcodes a 6-src bind loop and the
pipeline is created with `"main", 7, ...` - both must change.

Gotchas:
- Subgroup variance interacts with the EXISTING variant matrix: the GDN comp
  already ships shmem / cluster / nocluster variants keyed on subgroup size and
  relies on `S_V % COLS_PER_WG == 0`. The OP-A/B read/write redirect must be
  applied across ALL of those variants, and re-validated per vendor.
- In-place race: GLSL must read the full column shard into local registers before
  the ring write (same pattern); confirm the SPIR-V memory model is not relied on
  for cross-invocation ordering (it is not - blocks are disjoint per (seq,head)).
  OP C/D need the explicit window-to-local copy.
- Discriminated SSM_CONV: `supports_op` returns `op->src[0]->type == F32` with NO
  discriminator check; GDN loops `src[0..5]` F32 with NO `src[6]`/`src[7]` check.
  Both must be tightened. This is the backend where the 0030 hazard is most
  concrete (a present plain-conv kernel + a permissive supports_op = silent
  miscompute) - Vulkan is the exact case 0030 was written for.
- conv-update is per-channel (one invocation per channel) so it is
  subgroup-AGNOSTIC; only the GDN recurrence carries the subgroup-width burden.
- Vulkan's `ssm_conv.comp` ALREADY has APPLY_SILU + APPLY_BIAS specialization
  constants, so the silu-fold half of OP C is partly precedented here (unlike
  Metal); the ring write-back + tap-window assembly are still new.

### 2.3 SYCL (single-source DPC++)

Authoring model: plain C++ `.cpp`/`.hpp` per op (`gated_delta_net.cpp`,
`ssm_conv.cpp`); a SYCL `queue.parallel_for` over an `nd_range` with
`reqd_sub_group_size(WARP_SIZE)`; sub-group reductions (`warp_reduce_sum`);
`supports_op` in `ggml-sycl.cpp`. NO separate shader-compile step (single
source).

Effort: EASIEST to author. ~250-350 LOC. The SYCL op handlers + kernels are
near-VERBATIM mirrors of the CUDA ones (`launch_gated_delta_net<KDA,keep_rs>`,
`s_shard`, `curr_state`, `state = dst + attn_score_elems`, `warp_reduce_sum`) -
a dpct/SYCLomatic-style port. The CUDA diffs in 0018/0019/0021/0028 would port
almost line-for-line: add the `state_dst` param, the `ids`/`rs_head` params, the
read-base select, the two tiny gather kernels, and the new conv-update kernel.
No pipeline/push-constant/binding bookkeeping.

Gotchas:
- In-place race: the `s_shard[]` / window arrays are per-work-item private, so
  the register-snapshot-before-write pattern carries over directly. Safe.
- Discriminated SSM_CONV: `supports_op` checks `src[0]`/`src[1]` F32 with NO
  discriminator check; GDN returns a BARE `true` (the MOST permissive, so the
  hazard is worst here). Both must be tightened, and `ggml_sycl_op_ssm_conv` /
  `ggml_sycl_op_gated_delta_net` must branch on the extra src.
- Bit-exactness: `WARP_SIZE` is compile-fixed (Intel sub-group 8/16/32), same
  situation as CUDA; the fused variant matches SYCL's own non-fused path by
  construction. conv-update is per-channel -> subgroup-agnostic.

### 2.4 Common wiring (all three) + the 0030 emission-gate change

Per backend, four wiring touch-points beyond the kernel body:
1. `supports_op`: tighten the `GGML_OP_SSM_CONV` and `GGML_OP_GATED_DELTA_NET`
   entries so the discriminated/extra-src node is reported supported ONLY when
   the new kernel handles it (and rejected otherwise, instead of today's
   silently-true-for-the-plain-kernel).
2. op handler: branch on `src[3]`/`src[4]` (conv) and `src[6]`/`src[7]` (GDN) to
   dispatch the fused kernel.
3. pipeline/kernel registration (Vulkan: + push-constant struct + descriptor
   bindings; Metal: + kargs fields + pipeline name; SYCL: just the new functions).
4. The patch-0030 gate in `src/llama-context.cpp`.

The 0030 change today is a hard allow-list: any non-CPU compute backend whose reg
name is not `"CUDA"`/`"ROCm"`/`"MUSA"` forces `fused_gdn_ar = fused_gdn_ch =
auto_fgdn = false`. As each backend gains kernels this must become capability-
driven, in one of two ways:
- minimal: add the backend's reg name (e.g. `"Metal"`) to the allow-list once its
  kernels + tightened supports_op ship; OR
- clean (recommended upstream form): DELETE the name allow-list and make
  `supports_op` authoritative - have the `auto_fgdn` resolution probe
  `ggml_backend_dev_supports_op` on a representative node that carries the
  discriminated `src[]` slots. Then routing falls out of the normal scheduler
  fallback and no backend name is ever hard-coded. This also fixes 0030's stated
  weakness that the upstream `auto_fgdn` check only inspects GATED_DELTA_NET
  nodes and covered the discriminated SSM_CONV only incidentally.

--------------------------------------------------------------------------------
## 3. Bit-exactness per backend (the md5 gate question)

Feasible on ALL THREE, and not actually constraining, because of how the gate is
scoped:

- The series md5 gate is a CUDA-vs-CPU comparison; each GPU backend ALREADY has
  its own f32 reduction order (Metal `simd_sum`, Vulkan subgroup reduce, SYCL
  `warp_reduce_sum`) that differs from CUDA's and from CPU's. There is no
  cross-backend md5 and none is expected.
- The relevant per-backend invariant is: the FUSED variant must equal that
  backend's OWN non-fused path. The fusions change only the read source
  (gather -> indexed read; the gather is a value-preserving memcpy) and the write
  target (appended output -> in-place cache slot). They do NOT touch the
  per-column FMA/reduce order. So the fused op is bit-identical to the
  non-fused op on the same backend BY CONSTRUCTION.
- Two arithmetic details each port MUST preserve exactly: (a) the conv
  ascending-tap order plus the `+0.0f` that matches plain `ssm_conv`'s
  `sumf += b` with b==0; (b) the existing GDN per-column subgroup reduce (do not
  re-order it). Get those right and `test-backend-ops` (backendX-vs-CPU, already
  registered for SSM_CONV / SSM_CONV_UPDATE / SSM_CONV_UPDATE_IDS /
  GATED_DELTA_NET) is the per-backend gate.

--------------------------------------------------------------------------------
## 4. Upstream path and ranked recommendation

### Ops-first, then one PR per backend (NOT one big PR)

Recommended sequence:

1. PR #1 - OPS (already essentially done, upstreamable as-is): the `ggml.h`/
   `ggml.c` builders, the CPU reference kernels, the CUDA kernels, the
   `test-backend-ops` cases, and the capability-driven gate (the clean
   `supports_op`-authoritative version of 0030). This is independently mergeable
   and mirrors how llama.cpp lands new ops (CPU + CUDA first; GDN itself landed
   that way).
2. PR #2 - Metal kernels + wiring.
3. PR #3 - SYCL kernels + wiring.
4. PR #4 - Vulkan kernels + wiring.

Do NOT bundle the backends: each needs its own hardware to validate
`test-backend-ops`, reviewers are backend-specialized, and a regression in one
must not block the others.

### Value x effort ranking (which backend first)

| backend | user base / value          | author effort | bit-exact difficulty | net rank |
|---------|----------------------------|---------------|----------------------|----------|
| Metal   | HIGH (Apple Silicon = largest non-CUDA LocalAI base; unified memory makes the no-copy / no-gather plumbing wins map directly) | MEDIUM | LOWEST (fixed 32 simdgroup) | **1st** |
| SYCL    | LOW-MED (Intel GPU)        | LOWEST (near-verbatim CUDA mirror) | LOW   | **2nd** |
| Vulkan  | HIGHEST breadth (AMD + Intel + cross-vendor) | HIGHEST (shaders-gen + variant matrix + subgroup variance + descriptor growth) | MEDIUM (per-vendor subgroup validation) | **3rd** |

Recommendation: **Metal first.** It banks the biggest user-facing decode win at
medium effort, the base GDN + conv kernels already exist, and Apple's fixed
simdgroup width makes bit-exactness the simplest. **SYCL second** as a cheap,
nearly mechanical follow-on (the port is a line-for-line CUDA mirror, so it is
low-cost insurance even though the Intel-GPU audience is smaller). **Vulkan last**
as the high-effort / high-breadth capstone - it reaches the widest hardware
(AMD + Intel + anything with a Vulkan driver), but the shader-gen pipeline, the
existing variant matrix, the subgroup-width variance, and the per-vendor
validation burden make it the right capstone once the pattern is proven on
Metal + SYCL.

A reasonable cheaper variant: ship Metal + SYCL together right after the ops PR
(both are register-snapshot ports with no shader-gen step) and treat Vulkan as a
separate later effort.

--------------------------------------------------------------------------------
## 5. Summary

- GDN-compute and plain SSM_CONV kernels ALREADY EXIST on Metal, Vulkan and SYCL
  (the README's "no Vulkan kernel" line is stale). The Qwen3.6 hybrids run on all
  three today via the non-fused path; Layer-2 is about the decode SPEEDUP.
- Per backend the NEW work is: redirect the GDN state write (OP A) + add the ids
  read (OP B) to the existing GDN kernel, write ONE new conv-update kernel
  (OP C) + its ids variant (OP D), add two tiny gather kernels, and tighten
  supports_op + the op-handler branch + (Vulkan) the pipeline/push-constant/
  descriptor wiring. The builders, CPU refs, model graph and tests are shared and
  already done.
- Bit-exactness is feasible everywhere and per-backend by construction (the
  fusions redirect addresses, not the f32 reduction order); `test-backend-ops`
  (backendX-vs-CPU) is the gate.
- Sequence: ops-first PR (incl. the capability-driven replacement for 0030's
  name allow-list), then Metal, then SYCL, then Vulkan.
