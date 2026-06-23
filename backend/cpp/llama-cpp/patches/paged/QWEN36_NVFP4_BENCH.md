# Qwen3.6 NVFP4-vs-NVFP4: llama.cpp vs vLLM on GB10 (DGX Spark)

Apples-to-apples benchmark. Both engines run the **same NVFP4 weights** on the **same box**
(GB10, sm_121, LPDDR5x unified memory ~273 GB/s). The question is not "who wins the HW
lottery" but "at matched NVFP4, on one bandwidth-limited box, does our paged llama.cpp
(patch 0015, expert-density-aware MoE token-tile auto-select, default-on) sit at par with /
ahead of / behind vLLM?"

## Setup

- **Box**: GB10 / DGX Spark, sm_121, unified LPDDR5x (~273 GB/s). Memory figures are
  unified-memory used GB (`MemTotal-MemAvailable`), so they cover weights + KV + runtime.
- **llama.cpp**: dev tree `~/llama-paged-dev` branch `paged` HEAD `151343b` (patch 0015),
  `build-cuda` sm_121, `LLAMA_KV_PAGED=1`, `llama-server -c 131072 --parallel 128 -b 2048
  -ub 512 -ngl 99 -fa on`.
- **vLLM**: 0.23.0, `--enforce-eager --gpu-memory-utilization 0.85 --max-model-len 4096
  --max-num-seqs 256 -tp 1`.
- **Client**: identical async client (`h2h_cli.py`) for both engines. Per request:
  512-token unique prompt (unique leading tokens defeat cross-request prefix caching),
  `max_tokens=256`, `temperature=0`, `ignore_eos=True`, streaming with usage. Concurrency
  (npl) swept at 8 / 32 / 64 / 128.
- **Metrics** (localmaxxing.com schema): `decode_agg_tps` (aggregate decode tok/s across all
  live seqs), `decode_perseq_tps` (mean per-sequence decode), `prefill_tps`, `ttft_mean_ms`,
  `PEAK_GB` (unified-memory peak).

## The 4 models (NVFP4, matched weights)

| Model | llama.cpp GGUF | vLLM checkpoint | Match |
|-------|----------------|-----------------|-------|
| DENSE Qwen3.6-27B (28B dense) | `q36-27b-nvfp4.gguf` (native Blackwell FP4) | `q36-27b-nvfp4-vllm/` (unsloth TRUE W4A4) | clean W4A4 both sides |
| MoE Qwen3.6-35B-A3B (36B total, ~3B active) | `q36-35b-a3b-nvfp4.gguf` (241 NVFP4 tensors, nvidia weights) | `q36-35b-a3b-nvfp4-vllm/` (nvidia modelopt; vLLM picks Marlin NvFp4 MoE + FA2) | NVFP4 weight-only, identical nvidia weights |

---

## Results

### MoE Qwen3.6-35B-A3B (~3B active) - llama.cpp (paged, patch 0015)

| npl | decode agg tok/s | decode per-seq tok/s | prefill tok/s | TTFT mean ms | peak GB |
|----:|-----------------:|---------------------:|--------------:|-------------:|--------:|
| 8   | 170.2 | 20.27 | 2813.4 | 855.0   | 38.98 |
| 32  | 235.4 | 6.77  | 2004.5 | 4970.5  | 43.06 |
| 64  | 271.7 | 3.88  | 2388.7 | 7205.0  | 52.53 |
| 128 | 292.2 | 2.05  | 656.5  | 84799.7 | 61.42 |

Baseline (weights loaded, idle): 37.67 GB.

<!-- MoE vLLM, DENSE llama, DENSE vLLM tables appended by orchestrator phases below -->
