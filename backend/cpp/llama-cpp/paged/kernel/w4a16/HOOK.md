# W4A16 seam — how to apply to a llama.cpp / ggml-cuda checkout

Two source files + two one-line edits to `ggml/src/ggml-cuda/ggml-cuda.cu`. The build picks up the
new `.cu` via the existing `file(GLOB)` after a `cmake -S . -B build` reconfigure (no CMakeLists edit).

## Files (copy into `ggml/src/ggml-cuda/`)
- `marlin-w4a16.cuh`
- `marlin-w4a16.cu`

## Edit `ggml/src/ggml-cuda/ggml-cuda.cu`

1. **Include** — after the existing `#include "ggml-cuda/fp4-grouped-moe.cuh"` (sibling-header style):
   ```cpp
   #include "ggml-cuda/marlin-w4a16.cuh"
   ```

2. **Dispatch hook** — immediately before the dense dispatch chain, i.e. before
   `if (!split && use_mul_mat_vec_f) {` in `ggml_cuda_mul_mat(...)` (after `const int cc = ...`):
   ```cpp
   if (!split && ggml_cuda_w4a16_mul_mat(ctx, src0, src1, dst)) { return; }
   ```

## Verify (P1 acceptance — met)
- `cmake --build build --target test-backend-ops llama-bench` → builds clean.
- `test-backend-ops test -o MUL_MAT -b CUDA0` → **1103/1103** (byte-identical default).
- `llama-bench` dense Q4 pp512 → unchanged (~718, MMQ).
- `GGML_CUDA_W4A16=1 llama-bench` → unchanged + stderr `[w4a16] ... P1 seam - using MMQ` (seam reached,
  gating passes on sm_121, falls back).

The kernel body (P2 correctness → P3 Marlin pipeline) replaces the `TODO(P2/P3)` block in `marlin-w4a16.cu`
and returns `true` once parity holds.
