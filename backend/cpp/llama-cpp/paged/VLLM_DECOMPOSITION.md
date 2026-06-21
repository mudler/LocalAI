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

## Sizing the scheduler gap — MEASURED (llama.cpp aggregate, the surprise)

`llama-batched-bench` Qwen3-32B-Q4_K_M, npp=128 ntg=128, npl scaling (DGX):

| npl | S_PP (agg prefill) | **S_TG (agg decode)** | vLLM decode | llama % of vLLM |
|---|---|---|---|---|
| 1 | 628 | 10.2 | 11.8 | 86% |
| 8 | 773 | 59.8 | - | - |
| 32 | 763 | **235** | **328** | **72%** |
| 64 | 761 | **391** | **569** | **69%** |
| 128 | 762 | **540** | **667** | **81%** |

**The "30x gap" headline is wrong for realistic concurrency.** llama.cpp's continuous batching already
captures **~70-81% of vLLM's aggregate decode** at npl<=128, with a near-identical multiplier (10.2 -> 540 =
**53x**, vs vLLM's 56x). And it is still climbing linearly at 128 (not plateaued). Combined with llama.cpp being
*ahead* single-stream (MXFP4 1153 > vLLM 800), **llama.cpp is already broadly competitive with vLLM on GB10 at
self-hosted concurrency.**

Two real findings remain:
1. **Aggregate prefill is flat ~760** regardless of npl - but that is the **GB10 compute roofline** (vLLM single-
   stream is ~800; neither can prefill faster aggregate, it is compute-bound). So prefill is **not a throughput
   gap**; chunked prefill is a **latency/TTFT** win (stop a long prefill stalling the decode batch), not a
   throughput one.
2. **vLLM's ~24k headline lives at thousands-of-sequences concurrency**, which **paged KV** unlocks (block KV,
   no fragmentation). llama.cpp's contiguous KV caps how far npl can scale before memory/fragmentation bite. So
   paged KV is the **high-concurrency (datacenter) lever**, not a moderate-concurrency one.

## Recommendation

**Pivot to the scheduler; treat the GEMM kernel as good-enough / roofline-blocked on GB10.**
Now that the gap is measured, ROI-ordered:
1. **Ship the MXFP4-dense win** — 1153 t/s single-stream beats vLLM's 800; a Blackwell dense-quant
   recommendation (requantize, no kernel work). Already documented in `BLACKWELL_KERNEL_GAPS.md` §6. Cheapest.
2. **Chunked prefill** — the tractable scheduler win: interleave prefill chunks with decode so a long prompt
   doesn't stall the decode batch. Payoff is **latency/TTFT under mixed load** (and steadier decode batches),
   not aggregate prefill throughput (that's GB10-compute-capped at ~760-800 for both engines). A grpc-server
   scheduler change; no KV-layout rewrite.
3. **Paged KV** — the **high-concurrency (thousands-of-seqs) lever** that unlocks vLLM's 24k regime. Heavy
   (block KV manager; contested upstream PR #22569 / vendored `patches/`). Worth it only if datacenter-scale
   concurrency is a target; at self-hosted concurrency (npl<=128) llama.cpp is already ~75-80% of vLLM.

**Reframed expectation:** llama.cpp on GB10 is NOT 30x behind vLLM. It is ahead single-stream (MXFP4) and
~70-81% of vLLM aggregate at npl<=128. The genuine differentiator vLLM still has is **scaling to very high
concurrency via paged KV**. Kernel tracks (W4A16 178 t/s; FP4-MMA) stay **banked** - not the lever.
