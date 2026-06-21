# What makes vLLM fast on GB10 — kernel vs scheduler (code-grounded, measured)

Decisive analysis (vLLM v0.23.0, torch 2.11+cu130, sm_121, model `RedHatAI/Qwen3-32B-NVFP4A16`, source at tag
`v0.23.0`). **Answer: it's the scheduler, not the kernel.** This closes the kernel track and opens the
scheduler track.

## The decomposition (measured on the DGX, prefix-cache OFF, unique prompts)

| | vLLM W4A16 Marlin | llama.cpp | verdict |
|---|---|---|---|
| **single-stream prefill** | ~800 t/s (~52 TFLOPS) | 718 MMQ / **1153 MXFP4** | **tied; llama.cpp MXFP4 wins** |
| decode batch-1 | 11.8 t/s | ~similar | bandwidth-bound (≈190/273 GB/s); no kernel helps |
| **aggregate decode** | 328 (N32) / 569 (N64) / **667 (N128)** | the gap | **~56× multiplier = scheduler** |

vLLM's single-stream Marlin is **not** at the roofline — it's in the same ~4×-under regime as MMQ. The 24k
headline is entirely the aggregate decode multiplier.

## The kernel vLLM actually runs on sm_121 (W4A16, forced)

Dispatch (vLLM v0.23.0): `compressed_tensors.py:704` (NVFP4 + no input-quant → `W4A4Fp4(use_a16=True)`) →
`compressed_tensors_w4a4_nvfp4.py:28` → `kernels/linear/__init__.py:894` (`if use_a16: force_kernel =
MarlinNvFp4LinearKernel`, **unconditional, no cc gate**) → `nvfp4/marlin.py` → `marlin_utils_fp4.py:182`
`ops.marlin_gemm(b_q_type=float4_e2m1f)`, activations FP16/BF16. csrc: `csrc/quantization/marlin/marlin.cu`
+ `marlin_template.h` + `marlin.cuh`.

Techniques = **exactly the playbook we proved loses on GB10**: XOR shared swizzle (`marlin_template.h:722
^ (row%8)`), 4-stage cp.async pipeline (`marlin.cu:396 stages=4`, `cp_async_wait<stages-2>`), ldmatrix+mma,
FP16/BF16 acts. Native FP4 (`FlashInferB12xNvFp4LinearKernel`) needs `Sm120BlockScaledDenseGemm` cubins absent
on GB10 → W4A4 hangs → forced W4A16 Marlin fallback. **Nothing to port; vLLM's kernel is occupancy-blocked too.**

## The scheduler (the real multiplier) — what llama.cpp lacks

- **Paged KV cache** (`vllm/v1/core/kv_cache_manager.py`, `block_pool.py`): block KV, no fragmentation → very
  high concurrent batch. **llama.cpp: NO** (contiguous per-slot KV → fragmentation caps real concurrency).
- **Chunked prefill** (`config/scheduler.py:84 enable_chunked_prefill=True`, default ON): interleaves prefill
  chunks with decode so decode batches stay full. **llama.cpp: NO** (a long prefill stalls the decode batch).
- **Continuous batching** (`v1/core/sched/scheduler.py`): per-step admit/evict. **llama.cpp: YES** (`n_parallel`,
  rudimentary — we enabled VRAM-scaled slots in #10411).

## Recommendation

**Pivot to the scheduler; treat the GEMM kernel as good-enough / roofline-blocked on GB10.**
1. **Ship the MXFP4-dense win now** — 1153 t/s single-stream beats vLLM's 800; a Blackwell dense-quant
   recommendation (requantize, no kernel work). Already documented in `BLACKWELL_KERNEL_GAPS.md` §6.
2. **Size the gap first:** measure llama.cpp aggregate decode at `n_parallel` = 32/64/128 vs vLLM's 328/569/667.
   This tells us how much of the 56× the existing continuous batching already captures, and how much paged KV +
   chunked prefill would add.
3. **Then the two missing scheduler features**, in ROI order from the measurement: **chunked prefill** (keep
   decode batches saturated, avoid prefill stalls) and **paged KV** (sustain large concurrent batches without
   fragmentation — the contested upstream PR #22569 / the vendored patches in `patches/`).

Kernel tracks (W4A16 P3b at 178 t/s; FP4-MMA tuning) are **banked, not resumed** — they cannot move the
throughput needle on GB10 because the bottleneck is not the GEMM.
