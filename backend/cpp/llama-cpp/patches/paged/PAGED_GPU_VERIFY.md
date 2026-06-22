# Paged-KV GPU verification + full backend CUDA build

Verification run on a DGX Spark (NVIDIA GB10, compute capability 12.1 / sm_121),
CUDA 13.0, against pin `f3e182816421c648188b5eab269853bf1531d950`. Models:
`Qwen3-0.6B-Q8_0.gguf` (core gate) and `Qwen3-32B-Q4_K_M.gguf` (sanity).

All paged behaviour stays gated by `LLAMA_KV_PAGED` (env) / the `kv_paged`
server option; default-off is byte-identical to stock.

## Deliverable 1 - GPU-path correctness (all on GPU, `-ngl 99`)

CUDA build of the dev tree configured with
`-DGGML_CUDA=ON -DCMAKE_CUDA_ARCHITECTURES=121 -DCMAKE_BUILD_TYPE=Release`;
all paged drivers (`llama-simple`, `llama-paged-multiseq`,
`llama-paged-prefix`, `llama-paged-prefix-engine`) compiled clean under sm_121.

1. Core token-identical gate - PASS. `llama-simple` greedy, Qwen3-0.6B, `-ngl 99`:
   stock (env unset) vs `LLAMA_KV_PAGED=1` output is BYTE-IDENTICAL. The paged
   path is genuinely engaged: `LLAMA_KV_PAGED_DEBUG=1` shows the device gather
   firing (`[paged-attn] gather n_stream=1 ...`), per-token block placement
   (`[paged-alloc] ... grew`), and the stock run uses CUDA Graphs while the paged
   run takes the distinct gather path - yet output matches exactly.

2. Multi-stream - PASS. `llama-paged-multiseq -s 4 -ngl 99`, stock vs paged:
   all 4 concurrent sequences BYTE-IDENTICAL on GPU (n_seqs=4, CUDA0 compute
   buffer matches expectation). Same result reproduced on the CPU build.

   Prefix recompute-skip (`llama-paged-prefix-engine`, patch 0007) - MIXED, and
   this is a dev-scaffolding driver ("not shipped"); it was never built on CPU
   (absent from the CPU Gate-0 set), so there is no prior CPU pass to match.
   The driver hardcodes `n_gpu_layers = 0`; a reported test-harness-only env
   override (`PAGED_NGL`) was added to run it at `-ngl 99` (29/29 layers
   offloaded confirmed), then reverted. Results are IDENTICAL on CPU and GPU
   (so not a GPU issue):
   - PASS: measured recompute-skip (32 prefix tokens skipped, block-aligned),
     ref-count == 2 on shared block, ref drop 2->1 on free, only-private-blocks
     returned, block returned to pool.
   - FAIL: 2 of ~16 greedy-token-equality assertions. `boundary` case diverges
     from the from-scratch baseline at the 2nd generated token (`17971` vs
     `5671`) and then completely; `mid-block` "A re-shareable after free, output
     unchanged" also differs. Driver prints `GATE FAILED (failures=2)`.
   This is a divergence in the prefix recompute-skip path (0006/0007), NOT in the
   core gather gate, and not GPU-specific. Reported, not fixed (out of scope).

3. 32B GPU sanity - PASS. `LLAMA_KV_PAGED=1 llama-simple -ngl 99 -n 16` on
   Qwen3-32B-Q4_K_M (65/65 layers offloaded): coherent output
   ("The capital of France is Paris..."), no crash, no OOM.

## Deliverable 2 - full backend build with the paged patches

Built in a nested LocalAI tree on the DGX; gRPC v1.59.0 built from source
(LocalAI bundle; the system protobuf ships no CMake CONFIG) in ~26 min.

- (2a) `make llama.cpp LLAMA_PAGED=on` - PASS. All 6 paged patches
  (0001,0002,0003,0004,0006,0007) `git apply` cleanly to the pin (EXIT=0). The 8
  vendored paged sources land in `llama.cpp/src/` and are BYTE-IDENTICAL to the
  dev tree; `grpc-server.cpp` carries the `kv_paged`/`paged_attention` option
  (patch 0005); `llama-kv-cache.cpp` has the env-gated hooks.

- (2b) grpc-server under CUDA sm_121 - PASS (with the single-application caveat
  below). 89 MB ARM aarch64 executable, build ~139 s, linked against
  libcudart.so.13 / libcublas.so.13; binary contains the paged option strings
  and `paged_alloc`/`paged_attn`/gather symbols.

- (2c) `make llama.cpp LLAMA_PAGED=off` - PASS. "skipping paged-attention patch
  series", EXIT=0, NO `paged-*` sources in the checkout (clean escape hatch).

### Build-flow finding: paged patches are applied TWICE in the on-flow

A plain `make grpc-server LLAMA_PAGED=on` FAILS to compile. The paged series is
applied by BOTH the Makefile `llama.cpp` target (`git apply`) AND `prepare.sh`
(`patch -p1`). On the already-git-applied tree, `prepare.sh` hits "Reversed (or
previously applied) patch detected! Assume -R? [n]", declines, and re-applies the
pure-addition hunks a second time. `llama_kv_cache::get_n_gather` etc. end up
defined twice -> redefinition errors in `llama-kv-cache.cpp` (`.rej`/`.orig`
litter `src/`). Single application (one of the two appliers) compiles clean -
the 2b build above used a single git-apply with `prepare.sh` patching suppressed.
Reported only; the fix (drop one of the two application sites for
`patches/paged/`) is out of scope for this verification.

Assisted-by: Claude:opus-4.8 [Claude Code]
