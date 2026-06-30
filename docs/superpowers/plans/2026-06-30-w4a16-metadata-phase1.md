# W4A16 Metadata Phase 1 Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans. Keep checkboxes current while executing.

**Goal:** Test the first W4A16 kill-gate target selected by Phase 0: reduce host-built tile metadata overhead in the grouped W4A16 MoE prefill path.

**Scope:** Fork-first in `/home/mudler/_git/llama.cpp`; LocalAI patch series is regenerated only after the fork commit is validated.

## Task 1: Packed Tile Descriptor

**Files:**
- Modify fork-first: `/home/mudler/_git/llama.cpp/ggml/src/ggml-cuda/w4a16-gemm.cu`
- Modify fork-first if needed: `/home/mudler/_git/llama.cpp/ggml/src/ggml-cuda/w4a16-gemm.cuh`

- [x] Replace `h_tile_expert`, `h_tile_row0`, and `h_tile_rows` with one packed tile descriptor vector.
- [x] Replace three device pool allocations and three `cudaMemcpyAsync` calls with one descriptor allocation and one H2D copy.
- [x] Keep default-off behavior unchanged when `LLAMA_W4A16_PREFILL_M=0`.

## Task 2: Fork Build And Gates

- [x] Build the fork on DGX from a clean source snapshot.
- [x] Run canonical MoE and dense md5 gates.
- [x] Run W4A16 off/on prefill A/B at `npp=512,2048`.
- [x] Record whether packed descriptors improve, regress, or do not materially change W4A16.

Result: packed descriptors passed md5 gates and improved forced W4A16 by only
`+0.39%` at `npp=512` and `+0.48%` at `npp=2048`; W4A16 remains `-42.9%` and
`-44.7%` behind default FP4-MMQ respectively.

## Task 3: Next Decision

- [x] If W4A16 improves materially, continue metadata work toward device-side/cached descriptor generation.
- [x] If W4A16 does not improve materially, keep the patch only if it simplifies the path and choose the next target from the observed bottleneck.
- [x] Commit fork-first result, regenerate LocalAI patches, verify mirror invariant, and update LocalAI results docs.

Decision: keep the packed descriptor patch as a simplification, but do not spend
the next iteration on metadata alone. The remaining gap is dominated elsewhere;
next target should be the activation cast or MMA/dequant tile body.
