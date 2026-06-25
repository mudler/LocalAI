# OCCUPANCY_RETUNE_RESULTS.md - CRUX SETTLED: vLLM recurrence state is FLOAT32 (805 MB/call)

Phase: vllm-f32-confirm (GPU agent). DGX GB10, peak DRAM BW = 273 GB/s.
Checkpoint: ~/bench/q36-27b-nvfp4-vllm (vLLM 0.23.0), ~/bench/q36-27b-nvfp4.gguf (llama HEAD 58426b5, conv-fusion 0021).
NOTE: ncu HW perf-counters are perm-blocked on this node (RmProfilingAdminOnly:1, no passwordless sudo, ERR_NVGPUCTRPERM).
Settled WITHOUT counters: (a) empirical tensor dtype at the kernel boundary, (b) nsys/CUPTI kernel timing (counter-free), (c) source+config chain.

## VERDICT: f32. The close-check is RIGHT. The byte-gate (402 MB/bf16) is WRONG. BUILD THE BIT-EXACT OCCUPANCY RETUNE.

vLLM carries the gated-DeltaNet TEMPORAL/recurrent state in FLOAT32 and moves 805.3 MB/call, NOT 402 MB bf16.
Both engines move the SAME ~805 MB f32 recurrent state per call. The gap is pure BANDWIDTH EFFICIENCY on equal f32 bytes.

## vLLM (kernel: fused_recurrent_gated_delta_rule_packed_decode)
- EMPIRICAL tensor at kernel boundary (initial_state = self.kv_cache[1], qwen_gdn_linear_attn.py:1316/1492):
    dtype=torch.float32  elem_bytes=4  shape=(1553, 48, 128, 128)  per-slot state = 786432 elems = 3.000 MiB (f32)
- MB/call (B=128, Read+Write) = 128 * 48*128*128 * 4 bytes * 2 = 805,306,368 B = 805.3 MB  (bf16 would be 402.7 MB)
- Runtime engine config: cache_config.mamba_ssm_cache_dtype = float32  (mamba_cache_dtype=auto/bf16 for conv)
- Source chain: config.json text_config.mamba_ssm_dtype=float32 -> Qwen3_5ForConditionalGenerationConfig.verify_and_update_config
    sets cache_config.mamba_ssm_cache_dtype="float32" -> MambaStateDtypeCalculator._mamba_state_dtype else-branch
    -> temporal_state_dtype = torch.float32 (conv state = bf16; temporal/SSM state = f32).
- Kernel timing (CUDA events, eager B=128, 432 steady-decode calls): median 3.578 ms/call, min 3.499, mean 3.593, p90 3.635
    BW @ median = 805.3MB / 3.578ms = 225.1 GB/s = 82.4% of 273 peak  (min 84.3%, p90 81.1%)

## llama (kernel: gated_delta_net_cuda<128, 0, 0>)
- Kernel signature: all operands const float* (q,k,v,g,beta,curr_state) + float* state_dst => recurrent state is f32. Source-confirmed.
- Identical state geometry (48 value-heads x 128 head_v x 128 head_k, B=128) => MB/call (R+W) = 805.3 MB f32 (same as vLLM).
- Fresh nsys (--cuda-graph-trace=node, build-cuda-base, -npp128 -ntg24 -npl128, q36-27b-nvfp4.gguf):
    gated_delta_net = 25.4% of GPU time (#2 kernel after nvfp4 mul_mat_q).
    Decode cluster isolated = exactly n=1152 calls (= 24 ntg x 48 GDN layers), B=128 steady state:
      median 4.0211 ms/call, mean 4.0315 => 200.3 GB/s = 73.4% of 273 peak.
    (Consistent with prior GAP_PROGRESS 4.08ms/~70% and context 3.98ms/202GB/s/74%.)

## THE GAP (equal f32 bytes, different efficiency)
  llama   805.3 MB / 4.021 ms = 200.3 GB/s = 73.4% peak
  vLLM    805.3 MB / 3.578 ms = 225.1 GB/s = 82.4% peak
  => vLLM is ~11% faster per recurrence call at IDENTICAL byte volume => ~9 pts more DRAM BW efficiency.
  Retune target: 73.4% -> ~82% peak, recurrence 4.02 -> ~3.58 ms/call, KEEPING exact per-column f32
  reduction/FMA order (md5-gateable bit-identical). bf16 plan stays SHELVED (optional over-clock only).

---

# retune-build (BUILD AGENT) — patch 0022 SHIPPED

vLLM verdict re-checked first: **f32, 805 MB/call** (the close-check is right, the byte-gate's 402 MB/bf16
is wrong). The bf16-state plan stays SHELVED. Built the bit-exact occupancy/coalescing retune.

## The change — bit-exact column folding (Lever A + B + D)

`ggml/src/ggml-cuda/gated_delta_net.cu` `gated_delta_net_cuda`: two new template params
`NUM_WARPS` (default 4) and `COLS_PER_WARP` (default 1) plus `MIN_BLOCKS`. Each warp now owns
`COLS_PER_WARP` columns of the 128x128 recurrent state instead of 1, looping the existing per-column
body over `col, col+NUM_WARPS, ...` inside a per-block column tile of `NUM_WARPS*COLS_PER_WARP` columns;
`grid.z = S_v / (NUM_WARPS*COLS_PER_WARP)`.

Why it is bit-exact: the S_v rows of every column stay sharded across the lanes by the SAME strided
mapping `i = r*warp_size + lane`, and every column's per-lane FMA accumulation and
`warp_reduce_sum<warp_size>` XOR-butterfly are byte-for-byte unchanged. Only the
`(warp,block)->column` assignment and the order a warp visits its columns differ, and a column's f32
value provably does not depend on either (columns are fully independent — column c reads only its own
S_v-float state slice plus the shared per-(token,head,seq) q/k/v/g/beta). The forbidden `float4`
state load (Lever E) — which would repartition a lane to 4 contiguous rows and change the reduction
grouping — was NOT done; this keeps the md5 invariant. Every global access stays identically coalesced
(32 consecutive lanes -> one 128B sector), so this is a latency-coverage / scheduling win (higher
per-warp memory-level parallelism: COLS_PER_WARP independent state-load bursts issued before any
reduction + the independent butterfly reductions interleave to hide each other's shfl latency), NOT a
coalescing change. The S_v=128 tile is env-selectable via `GDN_NW`/`GDN_CPW` for one-build re-tuning;
default is the measured GB10 winner **(NUM_WARPS=16, COLS_PER_WARP=8)**.

## %peak sweep — GB10, CUDA 13, sm_121 (nsys CUPTI timing; HW counters perm-blocked)

Metric: median of the 1152 (=ntg24 x 48 layers) B=128 decode calls, each moving 805.3 MB f32 (R+W),
isolated by the [2.5ms,6ms] band; %peak vs 273 GB/s. Baseline re-isolation reproduced the confirm
agent's 4.021 ms / 73.4% exactly (n=1152).

| NUM_WARPS x COLS_PER_WARP | ms/call | GB/s | %peak |
|---------------------------|---------|------|-------|
| base (0021)               | 4.021   | 200.3| 73.4  |
| 4 x 1 (control == base)   | 4.034   | 199.7| 73.1  |
| 4 x 2                     | 3.887   | 207.2| 75.9  |
| 4 x 4                     | 3.775   | 213.3| 78.1  |
| 8 x 1                     | 3.837   | 209.9| 76.9  |
| 8 x 2                     | 3.749   | 214.8| 78.7  |
| 8 x 4                     | 3.699   | 217.7| 79.9  |
| 8 x 8                     | 3.586   | 224.6| 82.3  |
| 16 x 2                    | 3.665   | 219.8| 80.5  |
| 16 x 4                    | 3.585   | 224.7| 82.3  |
| **16 x 8  (WINNER/default)** | **3.488** | **230.9** | **84.6** |
| 32 x 4                    | 3.489   | 230.8| 84.6  |

Plateau ~84.5% at the grid.z=1 tiles; (16,8) picked as default (512-thread block, no spill, no
1024-thread .minnctapersm warning). **84.6% > vLLM 82.4%.**

## Gates (both PASS, non-negotiable)

- **md5 BYTE-IDENTICAL to the 0021 baseline**, greedy `--temp 0 --seed 1 -n 48`, both models, winner
  (16,8 default) AND (4,1 control):
  - q36-27b-nvfp4 (dense): `5951a5b4d624ce891e22ab5fca9bc439` (baseline == winner == control)
  - q36-35b-a3b-nvfp4 (MoE): `07db32c2bcb78d17a43ed18bc22705cd` (baseline == winner == control)
- **test-backend-ops -o GATED_DELTA_NET: 36/36 PASS** (covers head_size=128, kda=0/1, prefill K>1).

## Decode throughput — base vs flag(16,8), llama-batched-bench -npp128 -ntg128 -fa on

| model | npl | base S_TG t/s | flag S_TG t/s | gain |
|-------|-----|---------------|---------------|------|
| dense 27b | 32  | 199.2 | 207.6 | +4.2% |
| dense 27b | 128 | 335.9 | 373.2 | +11.1% |
| MoE 35b-a3b | 32  | 420.6 | 440.0 | +4.6% |
| MoE 35b-a3b | 128 | 688.4 | 745.7 | +8.3% |

Prefill S_PP unchanged (dense ~930, MoE ~2185 t/s) — no regression. Stable across 3 samples.

## Parity vs vLLM (recurrence kernel)

Recurrence kernel BW: before 200.3 GB/s = 89.0% of vLLM's 225.1; **after 230.9 GB/s = 102.6% of vLLM**
(3.488 ms/call < vLLM 3.578 ms/call). The recurrence bandwidth gap that this workflow set out to close
is closed and slightly exceeded; the remaining decode-parity delta lives in the non-recurrence path
(matmul/attn), not in gated-DeltaNet.

Shipped: patch 0022, committed on the DGX dev tree and the LocalAI worktree. No push.
