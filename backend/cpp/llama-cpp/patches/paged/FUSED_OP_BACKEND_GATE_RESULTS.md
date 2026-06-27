# Patch 0030 - fused-op backend gate (audit RISKY-1 fix) - RESULTS

Closes the single latent silent-miscompute hazard from `ARCH_GENERALITY_AUDIT.md`
(RISKY-1): the fused GDN / discriminated-SSM_CONV decode ops are CUDA+CPU-only but
were emitted DEFAULT-ON with no backend guard.

## The hazard

- `cparams.fused_gdn_ar = fused_gdn_ch = auto_fgdn = true` are set unconditionally
  in the `llama_context` constructor (`src/llama-context.cpp`).
- Patches 0018/0019/0026 add `ggml_gated_delta_net_inplace[_ids][_hybrid]`
  (reuse `GGML_OP_GATED_DELTA_NET` with extra src slots).
- Patches 0021/0028 add `ggml_ssm_conv_update_inplace[_ids]` which **reuse
  `GGML_OP_SSM_CONV`, discriminated by a non-null `src[3]`/`src[4]`** (ring/ids).
- Both families have CUDA + CPU kernels only. No `supports_op` change was made for
  the discriminated variants.
- A backend that supports **plain** `SSM_CONV` but ignores the discriminator
  (Vulkan/SYCL/Metal) returns `supports_op==true` for the node; the scheduler
  assigns the discriminated conv to it; it runs the **wrong plain conv** =>
  SILENT corruption (not a crash).
- The upstream `auto_fgdn` resolution only inspects `GATED_DELTA_NET` nodes, so the
  discriminated-`SSM_CONV` safety was only **incidentally** covered (GDN-op and
  discriminated-conv happened to share backend coverage). It goes live the moment a
  non-CUDA paged build of a gated-DeltaNet model exists.

## The fix (emission gate, not supports_op)

Chosen route: **gate the emission on the active compute backend type.** The
`supports_op` route would require editing every other backend's per-device
`supports_op` (Vulkan/SYCL/Metal/...) to reject the discriminated `SSM_CONV` -
invasive, fragile, and not centrally exposed by the ggml backend interface. The
emission gate is self-contained in the fork's own code.

`src/llama-context.cpp`, in `llama_context::sched_reserve()`, immediately before
the existing `if (cparams.auto_fgdn)` resolution block: if any **non-CPU** compute
backend has a reg name other than `"CUDA"` / `"ROCm"` (HIP) / `"MUSA"` (the three
`GGML_CUDA_NAME` values - all the same hipified ggml-cuda TU that carries the
discriminated-op handling), force
`fused_gdn_ar = fused_gdn_ch = auto_fgdn = false`.

Every emission site keys off these flags:
`conv_decode_fused = (n_seq_tokens==1) && (n_rs_seq==0) && fused_gdn_ar`
(qwen35/qwen35moe/qwen3next + `build_conv_state_fused`) and
`fused = (n_seq_tokens==1) ? fused_gdn_ar : fused_gdn_ch` (delta-net-base). With
the flags false the graph takes the upstream non-fused branch: a **plain
`ggml_ssm_conv` (no discriminator) + `ggml_silu`**, which every backend handles
correctly.

## CUDA byte-identical invariant

On a CUDA backend the reg name is `"CUDA"`, so `fgdn_backend_ok` stays true, the
flags are left untouched, and the emitted decode graph is unchanged. The fix only
changes behavior on non-CUDA/non-CPU backends. CUDA decode graph is byte-identical
to pre-0030 **by construction** (no flag flips on CUDA), so the existing greedy
md5 gates are unaffected on the validated GB10 target.

## Verification

- COMPILE (GPU-free, done on a CPU box): reconstructed the exact source state
  (upstream pin `9d5d882d` + paged patches `0001-0029`, .md docs stripped) and
  applied 0030. CPU-only build (`-DGGML_CUDA=OFF`) of `llama` + `test-backend-ops`
  links `libllama.so` and the test binary with **0 errors**; the edited
  `llama-context.cpp` compiles clean (uses only the already-included `<cstring>`
  and the backend-reg API already used in this TU:
  `ggml_backend_dev_backend_reg` / `ggml_backend_reg_name` /
  `ggml_backend_dev_type`).
- 0030 applies cleanly on a fresh pin+0001-0029 tree via both `git apply --check`
  (Makefile path) and `patch -p1 -N` (prepare.sh path).
- test-backend-ops correctness is a **CUDA0-vs-CPU** comparison; a CPU-only run
  skips CPU-vs-CPU by design ("Skipping CPU backend"). The relevant test cases are
  registered and will be exercised by the DGX CUDA run:
  `test_ssm_conv` / `test_ssm_conv_update` (SSM_CONV_UPDATE) /
  `test_ssm_conv_update_ids` (SSM_CONV_UPDATE_IDS) /
  `test_gated_delta_net` (+ `_hybrid`).

## Pending on the DGX (GPU)

The CUDA-side confirmation could not be run from the CPU box (the DGX cloudflared
tunnel `jp-6.prem.io` was returning `websocket: bad handshake` for the whole
session - origin offline). To run on the DGX `~/llama-paged-dev` (branch `paged`)
once reachable, then commit 0030 there too:

```
test-backend-ops test -o SSM_CONV
test-backend-ops test -o SSM_CONV_UPDATE
test-backend-ops test -o SSM_CONV_UPDATE_IDS
test-backend-ops test -o GATED_DELTA_NET   # expect: 2/2 backends passed, OK
```

Greedy md5 (only if >40GB VRAM free; must equal the established baselines):
`q36-27b-nvfp4 == 5951a5b4d624ce891e22ab5fca9bc439`,
`q36-35b-a3b-nvfp4 == 07db32c2bcb78d17a43ed18bc22705cd`. Since 0030 does not flip
any flag on CUDA, the md5 is unchanged by code-path argument; the run is a
belt-and-suspenders confirmation, not a correctness dependency.

Assisted-by: Claude:opus-4.8 [Claude Code]
