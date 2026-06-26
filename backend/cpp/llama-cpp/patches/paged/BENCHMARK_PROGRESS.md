# FINAL apples-to-apples NVFP4 benchmark (GB10 / DGX Spark) - CLEAN env, containers stopped
# llama 0023 clean f7409c2 | LLAMA_KV_PAGED=1, LLAMA_MAX_BATCH_TOKENS=512 (decode-first QoS budget; beats stock 394s->142s TTFT@npl32), CUDA graphs ON, -c 131072 --parallel 128 -b 2048 -ub 512 -fa on
# vLLM 0.23.0 | CUDA graphs ON (no enforce-eager), util 0.85, max-model-len 4096, max-num-seqs 256, tp1
# client h2h_cli3.py: 512-tok UNIQUE-nonce prompt (fresh full prefill, defeats prefix caching), max_tokens=256, temp0, ignore_eos, stream+usage
# llama restarts server PER NPL (paged-pool degrades after high-npl bursts); vllm one server/combo + npl8 re-check. 1 measured pass/npl + ptok8 graph warmup. peak_gb engine = PEAK-PRE.
# started Fri Jun 26 04:43:38 AM CEST 2026 baseline=3.29 GB

[2026-06-26 04:43:38] [dense_llama] ==== START dense_llama (llama) baseline_mem=3.29 ====
[2026-06-26 04:43:38] [dense_llama] NPL=8 launching server PRE_GB=3.29
[2026-06-26 04:43:48] [dense_llama] NPL=8 ready LOADED_GB=47.06
[2026-06-26 04:43:55] [dense_llama] GATE=' |REASON:Here\'s a thinking process:\n\n1.  **Analyze User Input:** The user says "The capital of France is". This is a straightforward factual question with a clear answer.\n2.  **Identify Key Entity:** France (country)\n3.  **Identify Question Type:** Capit
[2026-06-26 04:44:30] [dense_llama] NPL=8 PASS=1 {"n": 8, "reqs": 8, "gen_total": 2040, "prompt_tok_total": 4195, "gen_per_req": 255.0, "agg_tps": 61.8, "decode_agg_tps": 82.5, "decode_perseq_tps": 9.57, "prefill_tps": 507.3, "ttft_mean_ms": 6038.1, "ttft_max_ms": 8270.0, "wall_s": 32.999}
[2026-06-26 04:44:30] [dense_llama] NPL=8 PEAK_GB=53.51
[2026-06-26 04:44:35] [dense_llama] NPL=8 server stopped mem=3.31
[2026-06-26 04:44:35] [dense_llama] NPL=32 launching server PRE_GB=3.31
[2026-06-26 04:44:40] [dense_llama] NPL=32 ready LOADED_GB=46.96
[2026-06-26 04:47:55] [dense_llama] NPL=32 PASS=1 {"n": 32, "reqs": 32, "gen_total": 8180, "prompt_tok_total": 16900, "gen_per_req": 255.6, "agg_tps": 43.2, "decode_agg_tps": 192.6, "decode_perseq_tps": 4.79, "prefill_tps": 115.0, "ttft_mean_ms": 133551.7, "ttft_max_ms": 147007.0, "wall_s": 189.49}
[2026-06-26 04:47:55] [dense_llama] NPL=32 PEAK_GB=69.63
[2026-06-26 04:48:01] [dense_llama] NPL=32 server stopped mem=3.32
[2026-06-26 04:48:01] [dense_llama] NPL=64 launching server PRE_GB=3.32
[2026-06-26 04:48:11] [dense_llama] NPL=64 ready LOADED_GB=46.97
[2026-06-26 04:55:10] [dense_llama] NPL=64 PASS=1 {"n": 64, "reqs": 64, "gen_total": 16382, "prompt_tok_total": 33828, "gen_per_req": 256.0, "agg_tps": 39.8, "decode_agg_tps": 277.8, "decode_perseq_tps": 3.09, "prefill_tps": 95.9, "ttft_mean_ms": 321618.8, "ttft_max_ms": 352633.6, "wall_s": 411.603}
[2026-06-26 04:55:10] [dense_llama] NPL=64 PEAK_GB=83.96
[2026-06-26 04:55:16] [dense_llama] NPL=64 server stopped mem=3.30
[2026-06-26 04:55:16] [dense_llama] NPL=128 launching server PRE_GB=3.30
[2026-06-26 04:55:21] [dense_llama] NPL=128 ready LOADED_GB=47.09
[2026-06-26 05:13:18] [dense_llama] NPL=128 PASS=1 {"n": 128, "reqs": 128, "gen_total": 32767, "prompt_tok_total": 67969, "gen_per_req": 256.0, "agg_tps": 30.9, "decode_agg_tps": 384.6, "decode_perseq_tps": 1.86, "prefill_tps": 69.7, "ttft_mean_ms": 902762.7, "ttft_max_ms": 975832.6, "wall_s": 1061.031}
[2026-06-26 05:13:18] [dense_llama] NPL=128 PEAK_GB=93.82
[2026-06-26 05:13:25] [dense_llama] NPL=128 server stopped mem=3.31
[2026-06-26 05:13:25] [dense_llama] ==== DONE dense_llama POST_GB=3.31 ====
[2026-06-26 05:13:25] [dense_vllm] ==== START dense_vllm (vllm) baseline_mem=3.31 ====
[2026-06-26 05:13:25] [dense_vllm] launching vllm PRE_GB=3.31
[2026-06-26 05:21:15] [dense_vllm] vllm ready LOADED_GB=110.48
[2026-06-26 05:21:27] [dense_vllm] GATE='Here\'s a thinking process:\n\n1.  **Analyze User Input:** The user says "The capital of France is"\n2.  **Identify Key Entity/Question:** The question is asking for the capital city of France.\n3.  **Retrieve Knowledge:** I know from general knowledge that t
[2026-06-26 05:21:59] [dense_vllm] NPL=8 PASS=1 {"n": 8, "reqs": 8, "gen_total": 1959, "prompt_tok_total": 4195, "gen_per_req": 244.9, "agg_tps": 65.6, "decode_agg_tps": 70.4, "decode_perseq_tps": 8.76, "prefill_tps": 2096.2, "ttft_mean_ms": 1861.1, "ttft_max_ms": 2000.6, "wall_s": 29.843}
[2026-06-26 05:21:59] [dense_vllm] NPL=8 PEAK_GB=110.92
[2026-06-26 05:22:47] [dense_vllm] NPL=32 PASS=1 {"n": 32, "reqs": 32, "gen_total": 8165, "prompt_tok_total": 16900, "gen_per_req": 255.2, "agg_tps": 176.3, "decode_agg_tps": 211.8, "decode_perseq_tps": 6.28, "prefill_tps": 2182.6, "ttft_mean_ms": 5353.2, "ttft_max_ms": 7741.4, "wall_s": 46.302}
[2026-06-26 05:22:47] [dense_vllm] NPL=32 PEAK_GB=110.87
[2026-06-26 05:23:59] [dense_vllm] NPL=64 PASS=1 {"n": 64, "reqs": 64, "gen_total": 16314, "prompt_tok_total": 33828, "gen_per_req": 254.9, "agg_tps": 236.5, "decode_agg_tps": 309.1, "decode_perseq_tps": 4.38, "prefill_tps": 2088.9, "ttft_mean_ms": 9512.4, "ttft_max_ms": 16191.0, "wall_s": 68.976}
[2026-06-26 05:23:59] [dense_vllm] NPL=64 PEAK_GB=110.88
[2026-06-26 05:25:57] [dense_vllm] NPL=128 PASS=1 {"n": 128, "reqs": 128, "gen_total": 32640, "prompt_tok_total": 67969, "gen_per_req": 255.0, "agg_tps": 288.4, "decode_agg_tps": 418.8, "decode_perseq_tps": 2.79, "prefill_tps": 1929.1, "ttft_mean_ms": 18449.5, "ttft_max_ms": 35227.7, "wall_s": 113.162}
[2026-06-26 05:25:57] [dense_vllm] NPL=128 PEAK_GB=110.95
[2026-06-26 05:26:27] [dense_vllm] RECHECK_NPL8 {"n": 8, "reqs": 8, "gen_total": 2044, "prompt_tok_total": 4187, "gen_per_req": 255.5, "agg_tps": 68.1, "decode_agg_tps": 73.4, "decode_perseq_tps": 9.07, "prefill_tps": 1921.9, "ttft_mean_ms": 1877.6, "ttft_max_ms": 2178.1, "wall_s": 30.018}
[2026-06-26 05:26:35] [dense_vllm] ==== DONE dense_vllm POST_GB=3.53 ====
[2026-06-26 05:26:35] [moe_llama] ==== START moe_llama (llama) baseline_mem=3.53 ====
[2026-06-26 05:26:35] [moe_llama] NPL=8 launching server PRE_GB=3.53
[2026-06-26 05:26:50] [moe_llama] NPL=8 ready LOADED_GB=36.42
[2026-06-26 05:26:52] [moe_llama] GATE=' |REASON:Here\'s a thinking process:\n\n1.  **Analyze User Input:**\n   - User says: "The capital of France is"\n   - This is a straightforward factual question, incomplete but clearly asking for the capital city of France.\n\n2.  **Identify Key Information:*
[2026-06-26 05:27:06] [moe_llama] NPL=8 PASS=1 {"n": 8, "reqs": 8, "gen_total": 2048, "prompt_tok_total": 4195, "gen_per_req": 256.0, "agg_tps": 156.8, "decode_agg_tps": 211.8, "decode_perseq_tps": 24.45, "prefill_tps": 1236.4, "ttft_mean_ms": 2477.1, "ttft_max_ms": 3392.9, "wall_s": 13.061}
[2026-06-26 05:27:06] [moe_llama] NPL=8 PEAK_GB=39.66
[2026-06-26 05:27:11] [moe_llama] NPL=8 server stopped mem=3.34
[2026-06-26 05:27:11] [moe_llama] NPL=32 launching server PRE_GB=3.34
[2026-06-26 05:27:16] [moe_llama] NPL=32 ready LOADED_GB=36.54
[2026-06-26 05:27:54] [moe_llama] NPL=32 PASS=1 {"n": 32, "reqs": 32, "gen_total": 8192, "prompt_tok_total": 16900, "gen_per_req": 256.0, "agg_tps": 235.6, "decode_agg_tps": 393.0, "decode_perseq_tps": 10.02, "prefill_tps": 1213.9, "ttft_mean_ms": 8225.2, "ttft_max_ms": 13921.9, "wall_s": 34.768}
[2026-06-26 05:27:54] [moe_llama] NPL=32 PEAK_GB=47.11
[2026-06-26 05:28:00] [moe_llama] NPL=32 server stopped mem=3.30
[2026-06-26 05:28:00] [moe_llama] NPL=64 launching server PRE_GB=3.30
[2026-06-26 05:28:05] [moe_llama] NPL=64 ready LOADED_GB=36.39
[2026-06-26 05:29:10] [moe_llama] NPL=64 PASS=1 {"n": 64, "reqs": 64, "gen_total": 16384, "prompt_tok_total": 33828, "gen_per_req": 256.0, "agg_tps": 271.0, "decode_agg_tps": 527.0, "decode_perseq_tps": 6.15, "prefill_tps": 1152.3, "ttft_mean_ms": 15849.5, "ttft_max_ms": 29356.9, "wall_s": 60.449}
[2026-06-26 05:29:10] [moe_llama] NPL=64 PEAK_GB=57.13
[2026-06-26 05:29:16] [moe_llama] NPL=64 server stopped mem=3.28
[2026-06-26 05:29:16] [moe_llama] NPL=128 launching server PRE_GB=3.28
[2026-06-26 05:29:21] [moe_llama] NPL=128 ready LOADED_GB=36.48
[2026-06-26 05:34:19] [moe_llama] NPL=128 PASS=1 {"n": 128, "reqs": 128, "gen_total": 32760, "prompt_tok_total": 67969, "gen_per_req": 255.9, "agg_tps": 112.7, "decode_agg_tps": 726.4, "decode_perseq_tps": 3.73, "prefill_tps": 276.8, "ttft_mean_ms": 213017.2, "ttft_max_ms": 245528.7, "wall_s": 290.634}
[2026-06-26 05:34:19] [moe_llama] NPL=128 PEAK_GB=61.51
[2026-06-26 05:34:25] [moe_llama] NPL=128 server stopped mem=3.28
[2026-06-26 05:34:25] [moe_llama] ==== DONE moe_llama POST_GB=3.28 ====
[2026-06-26 05:34:25] [moe_vllm] ==== START moe_vllm (vllm) baseline_mem=3.28 ====
[2026-06-26 05:34:25] [moe_vllm] launching vllm PRE_GB=3.28
[2026-06-26 05:39:35] [moe_vllm] vllm ready LOADED_GB=109.46
[2026-06-26 05:39:38] [moe_vllm] GATE='Here\'s a thinking process:\n\n1.  **Analyze User Input:**\n   - User says: "The capital of France is"\n   - This is a straightforward factual question, incomplete but clearly asking for the capital city of France.\n\n2.  **Identify Key Information:**\n   - C
[2026-06-26 05:39:47] [moe_vllm] NPL=8 PASS=1 {"n": 8, "reqs": 8, "gen_total": 1900, "prompt_tok_total": 4195, "gen_per_req": 237.5, "agg_tps": 231.2, "decode_agg_tps": 256.5, "decode_perseq_tps": 31.84, "prefill_tps": 5186.5, "ttft_mean_ms": 768.8, "ttft_max_ms": 808.2, "wall_s": 8.217}
[2026-06-26 05:39:47] [moe_vllm] NPL=8 PEAK_GB=109.62
[2026-06-26 05:40:07] [moe_vllm] NPL=32 PASS=1 {"n": 32, "reqs": 32, "gen_total": 7794, "prompt_tok_total": 16900, "gen_per_req": 243.6, "agg_tps": 426.4, "decode_agg_tps": 500.8, "decode_perseq_tps": 14.9, "prefill_tps": 6223.4, "ttft_mean_ms": 1830.4, "ttft_max_ms": 2714.2, "wall_s": 18.28}
[2026-06-26 05:40:07] [moe_vllm] NPL=32 PEAK_GB=109.63
[2026-06-26 05:40:37] [moe_vllm] NPL=64 PASS=1 {"n": 64, "reqs": 64, "gen_total": 15927, "prompt_tok_total": 33828, "gen_per_req": 248.9, "agg_tps": 550.7, "decode_agg_tps": 686.1, "decode_perseq_tps": 9.83, "prefill_tps": 5926.5, "ttft_mean_ms": 3224.4, "ttft_max_ms": 5704.9, "wall_s": 28.92}
[2026-06-26 05:40:37] [moe_vllm] NPL=64 PEAK_GB=109.63
[2026-06-26 05:41:27] [moe_vllm] NPL=128 PASS=1 {"n": 128, "reqs": 128, "gen_total": 31795, "prompt_tok_total": 67969, "gen_per_req": 248.4, "agg_tps": 650.7, "decode_agg_tps": 882.2, "decode_perseq_tps": 6.05, "prefill_tps": 5300.5, "ttft_mean_ms": 6487.7, "ttft_max_ms": 12817.8, "wall_s": 48.863}
[2026-06-26 05:41:27] [moe_vllm] NPL=128 PEAK_GB=109.64
[2026-06-26 05:41:36] [moe_vllm] RECHECK_NPL8 {"n": 8, "reqs": 8, "gen_total": 1702, "prompt_tok_total": 4187, "gen_per_req": 212.8, "agg_tps": 207.2, "decode_agg_tps": 226.4, "decode_perseq_tps": 28.06, "prefill_tps": 6021.3, "ttft_mean_ms": 642.7, "ttft_max_ms": 694.8, "wall_s": 8.213}
[2026-06-26 05:41:44] [moe_vllm] ==== DONE moe_vllm POST_GB=3.31 ====

==== ALL 16 ROWS COLLECTED (2 models x 2 engines x 4 npl) ====
decode_agg t/s (llama | vLLM | llama%vLLM):
 DENSE q36-27b-nvfp4:  npl8 82.5|70.4|117%  npl32 192.6|211.8|91%  npl64 277.8|309.1|90%  npl128 384.6|418.8|92%
 MoE   q36-35b-a3b:    npl8 211.8|256.5|83%  npl32 393.0|500.8|78%  npl64 527.0|686.1|77%  npl128 726.4|882.2|82%
peak_gb (llama on-demand grows | vLLM fixed ~107 pool):
 DENSE llama 53.5->93.8 ; vLLM ~110.9 flat
 MoE   llama 39.7->61.5 ; vLLM ~109.6 flat
Final CSV: final_benchmark.csv ; analysis: QWEN36_NVFP4_BENCH.md (FINAL section).
Cleanup: no leftover server/bench PIDs; GPU free (memnow 3.28 GB); local-ai + local-ai-worker
containers restarted (host returned). DONE.
