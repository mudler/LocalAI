# Making llama.cpp/LocalAI a viable vLLM alternative — phased plan

Goal: close the practical gap to vLLM for both single-user *speed* and multi-user *throughput*, while keeping
quality (no lossy quant). Grounded in measured benchmarks + research (`BENCHMARKS.md`, `BLACKWELL_KERNEL_GAPS.md`,
`VLLM_THROUGHPUT_GAP.md`). The gap is NOT one thing — each phase targets a distinct, independent lever.

## Where vLLM actually leads (measured, GB10 / Qwen3-32B)

- **Single-user decode:** ~parity (10.2 vs 11.7) — bandwidth-bound. vLLM's edge is **spec-dec** (lossless).
- **Multi-user decode:** gap grows to ~2.2× at B=64 (kernel + scheduler).
- **Prefill aggregate:** llama plateaus ~765, vLLM scales to 24k — **paged KV + chunked prefill + kernel**.
- Note: on GB10 vLLM's FP4 trump card is *broken* (falls back to Marlin); llama.cpp runs reliably — a real
  viability point. vLLM is structurally ahead mainly via **paged KV, chunked prefill, cross-request prefix cache**.

## Phases

### Phase 1 — Hardware-tuned config (PR #10411) — DONE
Folded into the hardware-defaults path (`core/config/hardware_defaults.go`):
- Blackwell physical batch (n_ubatch) = 2048.
- **VRAM-scaled `n_parallel` default** (>=32GiB→8, >=8→4, >=4→2): turns on concurrency + continuous batching,
  which the backend leaves OFF at its `n_parallel=1` default. Unified KV → slots share the budget (no extra
  KV memory). Single-host (local GPU) + distributed router (per node). Already-good defaults confirmed:
  flash-attn=auto, context=4096.

### Phase 2 — Paged / block KV cache  ← biggest structural multi-user lever
vLLM's PagedAttention lifts KV utilization ~20-38% → ~96%. llama.cpp's own A10G data (draft PR #22569):
contiguous OOMs at 26 seqs / 496 t/s → paged 247 seqs / 1256 t/s (**~9.5× concurrency, 2.5× aggregate**).
- Build on / complete **upstream draft PR #22569** (`-kvp`, block manager + paged-attn ggml op, FCFS scheduler)
  rather than the from-scratch series we prototyped (`paged/`). Our CPU-verified block manager + gather-read
  design informs the review/port; the upstream momentum is the place to land it.
- Phase 2b: cross-request prefix sharing (block-hash dedup) — our `PagedKVManager` already implements it.

### Phase 3 — Prefill amortization (chunked prefill + n_batch/n_ubatch split)
llama aggregate prefill plateaus because (a) one prompt saturates compute, (b) the per-forward GEMM M-dim is
capped at `n_ubatch`=512, (c) no scheduler chunked prefill (draft #10718 abandoned).
- Split logical `n_batch` from physical `n_ubatch` (LocalAI ties them today) so concurrent prefills batch into
  a larger logical batch while keeping ubatch at the Blackwell sweet spot (2048).
- Chunked prefill + prefill/decode co-batching in the server slot scheduler.

### Phase 4 — Batched-GEMM kernel tuning (the decode 2.2× + prefill height)
Per `BLACKWELL_KERNEL_GAPS.md`: dense int8-MMQ at ~21% of ceiling, MoE FP4-MMA at ~5%. Both untuned for
Blackwell. To MATCH: tune MMQ or a Marlin-style W4A16 BF16 GEMM (FP4 not required — GB10 is INT8==BF16). To
BEAT (2×): fix+tune the existing FP4-MMA on sm_121 (build-flag/`-O3`-miscompile, not greenfield).

### Phase 5 — Backend GPU sampling
CPU per-sequence sampling caps GPU util ~60% beyond n_parallel ~8-16 (upstream PR #17004). Track/adopt.

### Cross-cutting — Speculative decoding (single-user speed, quality-preserving)
Dense ≥14B: lossless ~1.8-3×. llama.cpp has `-md`/`--spec-draft-*`. Wire a draft-model field in the model
config + ship Qwen3 target+draft (1.7B) pairs in the gallery. NOT for MoE-A3B (nothing to amortize).

## Sequencing rationale
Phase 1 (config) ships now — biggest immediate multi-user win for zero kernel work (concurrency was OFF).
Phase 2 (paged KV) is the highest-leverage structural build and has upstream momentum. Phases 3-4 are deeper
(scheduler + kernel). Spec-dec is independent and can land any time for single-user speed.
