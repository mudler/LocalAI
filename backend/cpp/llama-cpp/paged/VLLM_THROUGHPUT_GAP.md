# Where vLLM beats llama.cpp on a DGX Spark (GB10), and how to close it — keeping quality

The question: "vLLM is faster at the end — what do we improve, while keeping good quality?" Answer: the
gap is **three independent things**, and the biggest *per-user, quality-preserving* one is **speculative
decoding**, which llama.cpp already supports.

## Decomposition (measured + researched)

| vLLM advantage | helps single user? | llama.cpp answer | quality cost | status |
|---|---|---|---|---|
| **Per-user decode speed** | **yes** | **speculative decoding** (Qwen3 draft / EAGLE3) | **none** (target-verified, lossless) | mature in llama.cpp; **the main lever** |
| Prefill / TTFT | no (it's first-token latency) | tune FP4-MMA / Marlin W4A16 kernel | none | hard; `BLACKWELL_KERNEL_GAPS.md` |
| Aggregate throughput @ concurrency | no (per-user = 0) | continuous batching (paged engine) | none | also kernel-bound |

Key measured fact: **single-user decode is already at parity** (Qwen3-32B: llama 10.2 vs vLLM 11.7 t/s) —
both hit GB10's ~273 GB/s bandwidth wall (~15 t/s ceiling) **without** spec-dec. So vLLM's real per-user
speed edge is spec-dec, not architecture.

## Why spec-dec is THE lever here (and quality-safe)

- **Lossless:** the 32B target verifies every drafted token (accept/reject) — output distribution is
  identical to no-drafting. So you keep **Q4_K_M quality** (no lossy MXFP4 needed) *and* get speed.
- **GB10 is best-case for it:** decode is bandwidth-bound (one ~17 GB weight-read per token) with huge idle
  compute. Spec-dec verifies K drafted tokens in **one** weight-read → converts the loop to compute-bound,
  where GB10 has headroom. Realized speedup ≈ mean accepted length.
- **Measured (others, same model class):** llama.cpp Qwen2.5-32B dense + 0.5B draft = **2.9×** (13→38 t/s);
  vLLM EAGLE3 on Qwen3-32B = ~1.8–2.5× general, up to ~3× code/structured. **Competitive.**
- **Regime caveat:** spec-dec gives **~nothing for MoE-A3B** models (only ~3B active → not bandwidth-bound,
  nothing to amortize). It shines for **dense** 27–32B — the opposite regime. So this lever is *dense-model*
  specific.

## Qwen3-32B specifics

- **No native MTP head** (MTP is a Qwen3-*Next*/MoE feature). Options: a **same-family draft**
  (Qwen3-0.6B or **1.7B** — same tokenizer, llama.cpp vocab check passes) or an external **EAGLE3 head**
  (RedHatAI/AngelSlim Qwen3-32B-eagle3, accept length 2.15–2.49).
- Draft pick: **lean Qwen3-1.7B** (0.6B had ~60% lower acceptance in AWS's test; on a bandwidth-bound box the
  32B weight-read dwarfs the draft cost, so maximize acceptance). `--spec-draft-n-max 5–8`.

## Recommended LocalAI actions (quality-preserving, ranked)

1. **Make speculative decoding easy/recommended for dense ≥14B models on Blackwell** — a draft-model field in
   the model config (`-md` / `--spec-draft-*`), with a suggested Qwen3-1.7B draft for the Qwen3 family. This
   is the biggest per-user speed win, lossless, available **now** (no kernel). Gallery: ship target+draft pairs.
2. Kernel work (FP4-MMA tuning / Marlin W4A16) — improves **prefill/TTFT**, separate metric.
3. Continuous batching (paged engine) — **aggregate** concurrency only; per-user = 0.

## Honesty / status

The research conclusion is solid (sources below). **Our own empirical spec-dec run on the DGX is pending** —
the box rebooted mid-session and `llama-cli` now hangs at 0% GPU (while `llama-bench` works), plus the network
is dropping ssh mid-command. Drafts (Qwen3-0.6B/1.7B Q8) are downloaded and the spec-dec flags are confirmed;
re-run `llama-cli -m Qwen3-32B-Q4_K_M -md Qwen3-1.7B-Q8_0 -ngl 99 -ngld 99 --spec-draft-n-max 8` when the box
is stable to confirm the ~2× locally. The conclusion does not depend on it (it's measured-reproducible by
others on this exact model class), but we should bank our own number.

Sources: llama.cpp Discussion #10466 (Qwen2.5-32B+0.5B = 2.9×), #16578 (DGX Spark), DandinPower/llama.cpp_bench
(32B = 10.7 t/s, bandwidth-bound); vLLM MTP docs + Red Hat EAGLE3 article (lossless, up to 2.5×); AWS spec-dec
blog (Qwen3-32B+1.7B up to 3×, 0.6B ~60% lower accept); RedHatAI/AngelSlim Qwen3-32B-eagle3 heads.
