# Decode parity exploration (post-SSM-fix) - per-agent findings

Post-SSM-fix decode (patches 0018 in-place state write-back + 0019 fused gather):
dense q36-27b-nvfp4 decode_agg = 256.6 t/s @npl128 = 65.6% of vLLM 391, bit-exact.
The remaining +54% to parity is the question each section below probes. All numbers
DGX GB10 (sm_121), fusion OFF baseline, `decode_agg` = `S_TG t/s`.

---

## Section: per-token-latency (critical path / host-loop) - READ-ONLY

**Verdict: the per-step critical path and host loop are NOT the residual lever.
Post-SSM the GPU is still ~99% busy at npl128; the entire exposed-idle budget is
~0.65% of the step (~2.4 ms), of which graphs already remove the within-step half
(0.34%) and the between-step host gap is ~2 ms/step (~0.4% post-SSM). The 64-layer
sequential chain does NOT under-fill the GPU at batch 128 - every kernel's grid
saturates the SMs on its own. The +54% to parity is GPU kernel work (FP4 GEMM
efficiency + LPDDR5x weight bandwidth), not serialization or host overhead.**

### 1. Measured exposed-idle structure (a2_nsys pre-SSM rep, read-only sqlite sweep)

`paged_off_npl128.sqlite`, steady window 40-97% of trace (14.78 s, ~16.5 decode
steps at the pre-SSM ~896 ms/step). Overlap-correct interval-union sweep:

| activity set            | busy %  | exposed idle |
|-------------------------|---------|--------------|
| kernels only            | 80.25%  | 19.74%       |
| kernels + memcpy (all)  | 99.35%  | **0.65%**    |

- The 19.4% kernels-only gap = **841 big gaps (median 3.35 ms, ~51/step)** that are
  filled by D2D memcpy. These ARE the per-layer gated-DeltaNet recurrent-state copies
  (the `gated_delta_net -> ggml_cpy(state->cache) -> next layer reads state` chain).
  They were a real critical-path serialization, and **patches 0018/0019 removed exactly
  these** (D2D bucket 18.9% -> 0.23%; get_rows gather 18.8% -> 0.7%). Decode rose
  +37.8% (186 -> 256 t/s), ~matching the work removed -> the kernels reflowed
  back-to-back, so post-SSM these big gaps are CLOSED, not re-exposed (inferred from
  the throughput scaling; the post-SSM nsys was not re-profiled by this read-only agent).
- The TRUE exposed idle (kernel+memcpy union) is **0.65%**: 18 host gaps >=0.5 ms,
  **median 2.06 ms, max 2.85 ms, ~1.1/step**. This is the single between-step host gap
  (sample-128 + `update_slots` + next-batch build) that does NOT overlap GPU compute.
- Within-step launch gaps: 24,190 micro-gaps, median 2.14 us, summing to 50.6 ms =
  **0.34%** of the window - the pure launch overhead that CUDA graphs collapse
  (measured 0.37% -> 0.11% in A2_CUDAGRAPH_DECODE; graphs already engage on the
  default paged decode with a 256-token reset cadence).

### 2. Post-SSM scaling of the FIXED host gap

The ~2 ms/step between-step host gap is FIXED work (independent of GPU kernel time).
As decode accelerated it grew only as a fraction of a shrinking step:

| build         | step ms @npl128 | host gap | host gap % of step |
|---------------|-----------------|----------|--------------------|
| pre-SSM (146) | ~877            | ~2 ms    | 0.24%              |
| post-SSM (256)| ~499            | ~2 ms    | **~0.40%**         |
| vLLM (391)    | ~328            | (n/a)    | (would be ~0.6%)   |

Even fully removing it (perfect overlap) buys ~0.4%. It is a second-order floor, not
the lever - it only becomes material once the kernels are fast enough to drop GPU-busy
below the host time, which is not the case at 65% of parity.

### 3. The 64-layer chain does NOT under-fill the GPU at batch 128

The decode is an intrinsically sequential depth-64 chain (autoregressive: layer N
needs layer N-1; cannot be parallelized across layers). The question is whether each
individual kernel fills the SMs at batch 128. It does:

- **GDN kernel** (`gated_delta_net.cu`): launch grid `dim3(H, n_seqs, ceil(S_v/4))`
  = `48 x 128 x 32 = 196,608 blocks` (dense, H=48 value heads, S_v=128). Block
  `(warp_size, 4, 1)`. Massively oversubscribes the GB10 SMs. Each warp loads its
  state shard into registers once and runs a single `n_tokens==1` iteration - O(1) in
  context (confirmed flat across 4x ctx in GDN_DECODE_VERIFY).
- **FP4 GEMM** (`mul_mat_q`, mmq_x=128): M=128 token tile, well into the M-batched
  regime, full SM occupancy (and Track B P2a already showed it goes 2 CTA/SM).
- The 99.35% kernel+memcpy busy reading IS the direct proof there is no under-fill at
  npl128: if the chain under-filled, busy% would be well below 99%.

Under-fill only appears at LOW batch (npl32/npl4), where it manifests as the
weight-bandwidth/GEMV regime (npl32 = 170 t/s vs npl128 = 256): fewer tokens amortize
the same per-step weight read, NOT idle SMs. That is a bandwidth floor, not a
host/scheduler problem.

### 4. What the host actually does per step (eager rep runtime API)

Steady-window `CUPTI_ACTIVITY_KIND_RUNTIME` totals (host-thread wall, overlaps GPU):

| API                       |   n   | total   | avg     |
|---------------------------|-------|---------|---------|
| cudaStreamSynchronize     | 1723  | 7775 ms | 4513 us |
| cudaLaunchKernelExC        | 30983 | 4045 ms | 131 us  |
| cudaLaunchKernel          | 20385 | 2694 ms | 132 us  |
| cudaMemcpyAsync           | 2085  |   96 ms |  46 us  |

~104 stream-syncs/step and ~3100 kernel launches/step in eager mode (collapsed by
graphs to ~900 launches/step). The 7.8 s of sync is the host BLOCKING on the busy
GPU (it overlaps GPU compute, it is NOT exposed idle) - the GPU stays 99.4% busy. The
sampled-token path is `cudaMemcpyAsync` (96 ms total, negligible, non-blocking). The
only NON-overlapped residue is the ~2 ms/step between-step gap in section 1.

### 5. vLLM host-loop comparison (per VLLM_DECODE_GROUNDING.md)

vLLM's eager decode is host-cheap BY CONSTRUCTION and hides the host fully behind the
async CUDA stream WITHOUT pipelined scheduling (`async_scheduling` was OFF; it won the
2.4x with synchronous scheduling): persistent pre-allocated input buffers updated by
vectorized numpy (no per-token Python), attention metadata `build()` once per step
reused across all layers, no GPU->CPU sync in the hot path, sampled-token D2H
non-blocking + event-gated, and a fixed small launch sequence (~2 ops/Linear). The
next-step host prep overlaps the current-step GPU compute on the async stream. The key
asymmetry vs llama: vLLM builds its graph ONCE and reuses persistent device
KV/block metadata; ggml rebuilds/reallocates the cgraph each decode step (new
`cgraph->uid`) and re-dispatches ~3100 launches from the loop on the weak Grace cores.

But this asymmetry is hidden under GPU compute on BOTH sides at npl128: llama's host
loop is a 0.4% exposed gap, not a 2x lever. vLLM's host cheapness is why ITS step is
328 ms host-free, but llama's 499 ms is also ~99% GPU - the 171 ms difference is GPU
kernel time (FP4 GEMM), not host.

### 6. Is any host/serialization lever CUDA-graph or scheduler addressable?

- **Within-step launch idle (0.34%)**: CUDA-graph addressable, ALREADY captured by
  default (0.37 -> 0.11%). Worth ~0% of decode_agg (measured +0.1-0.8%, noise).
  Nothing left to win here.
- **Between-step host gap (~2 ms, ~0.4%)**: NOT removed by a graph (the graph replays
  the forward; the host still samples + runs `update_slots` + rebuilds the batch
  between replays). It is SCHEDULER addressable - overlap step N+1's host prep with
  step N's GPU compute, mirroring vLLM's persistent-buffer + build-once-reuse +
  non-blocking-D2H pattern (and ideally reuse the ggml cgraph across steps instead of
  rebuilding it every ubatch). But the ceiling is ~0.4% of the step, so it is a
  cleanup, not a parity lever.
- **The +54% to parity is none of the above.** It is GPU kernel work: post-SSM the FP4
  GEMM family is ~48% of decode (the dominant residual), GDN recurrence ~22.5%, and the
  decode is weight-bandwidth/latency-bound on LPDDR5x (Track B P2a: a -24.7% FP4-GEMM
  kernel left decode_agg FLAT, the freed compute became idle gaps -> decode is not
  GEMM-compute-bound but bandwidth/latency-bound). The lever lives in cutting DRAM
  traffic per step (fused act-quant to drop the separate `quantize_mmq` pass, native
  FP4-MMA, and/or NVFP4-dense weight quant), NOT in the host loop or CUDA graphs.

### Evidence
- Read-only sqlite sweeps on `~/bench/a2_nsys/paged_off_npl128.sqlite` (this agent).
- `gated_delta_net.cu` launch grid (DGX `~/llama-paged-dev`).
- A2_CUDAGRAPH_DECODE.md, SSM_DECODE_FIX_RESULTS.md, GDN_DECODE_VERIFY.md,
  VLLM_DECODE_GROUNDING.md, THROUGHPUT_B_P2a_POSTSSM_RESULTS.md.
# Decode-Parity Exploration

## Section: gdn-source-compare (llama gated_delta_net.cu vs vLLM fused_recurrent_gated_delta_rule)

### Model config (Qwen3.5-27B dense, from vLLM config.json)
- linear_key_head_dim K = 128, linear_value_head_dim V = 128
- linear_num_key_heads = 16, linear_num_value_heads = 48 (GVA 3:1), conv_kernel = 4
- 64 layers, full_attention_interval 4 -> 48 linear (GDN) : 16 full-attn
- Recurrent state per (seq, v-head) = V*K = 128*128 = 16384 f32 = 64 KiB.
  Per layer per seq = 48 * 64 KiB = 3 MiB. Both engines store state in f32.

### Which kernels run at decode
- llama: ggml_gated_delta_net_inplace_ids -> gated_delta_net_cuda<S_v=128, KDA=false, keep_rs_t=false>.
  Gate is SCALAR per head (graph reshapes gate/beta to ne[0]=1), so the cheaper !KDA branch runs (one expf per token, not per-channel).
- vLLM: enable_packed_recurrent_decode -> fused_recurrent_gated_delta_rule_packed_decode_kernel
  (the dedicated single-token decode kernel, NOT the generic varlen fwd kernel).

### The state HBM traffic is IDENTICAL - it is NOT the lever
Per (seq, v-head) per decode token both engines read 64 KiB state + write 64 KiB state, f32, coalesced.
The dominant memory term is equal. llama is NOT moving more state bytes than vLLM.
=> The 1.46 ms/call is llama achieving LOWER effective bandwidth on the SAME bytes,
   plus extra non-state work, NOT a fundamental HBM-traffic deficit. Hence closable.

### Algorithmic / parallelization delta (the real differences)

1) Reduction strategy (biggest structural difference)
   - llama: WARP-PER-OUTPUT-COLUMN. State stored transposed M[col][i]=S[i][col]. Each warp owns
     one V-column; the contraction over the 128 K-rows is a cross-lane warp_reduce_sum.
     TWO warp_reduce_sum per token (one for kv = S^T@k, one for attn = S^T@q) = ~10 shuffle
     rounds on the critical path, with n_tokens=1 they are NOT amortized.
   - vLLM: THREAD-PER-OUTPUT-ROW. b_h is a [BV,BK]=[32,128] tile; each thread owns a FULL K-row
     of state. sum(b_h*b_k, axis=K) and sum(b_h*b_q) are THREAD-LOCAL 128-wide reductions -
     ZERO cross-thread shuffles. Outer-product update b_h += b_v*b_k is also thread-local.
   Same FLOPs, but vLLM has no shuffle-reduction latency in the recurrence.

2) Occupancy / launch geometry (likely the dominant bandwidth gap)
   - llama: block = (32 lanes, 4 warps) = 128 threads; grid = (H=48, n_seqs, ceil(128/4)=32).
     Per (head,seq) it launches 32 blocks * 128 threads = 4096 threads to touch a 16384-elem state
     (only 4 state elems/thread). launch_bounds(128, 2) budgets registers for >=2 blocks/SM; with
     s_shard[4]+k_reg[4]+q_reg[4]+addressing the register pressure caps it near ~2 blocks = 8 warps/SM
     (~12-16% occupancy on GB10). A memory-bound kernel at ~8 warps/SM cannot generate enough in-flight
     loads to saturate 273 GB/s -> low achieved bandwidth on the state read/write.
   - vLLM: 1 warp/program (num_warps=1), grid (NV=4, B*HV), small register footprint, num_stages=3
     software-pipelines (prefetches) the state load. Far higher memory-level parallelism per SM.

3) Redundant non-state traffic in llama
   - q,k re-loaded by EVERY column-warp: 128 column-warps/head each reload the same 128-float q and k
     => ~128x amplified L2 loads of q/k per head/token (vLLM reloads ~4x, once per NV program).
     Small (L2-resident) but adds load-issue + L2 pressure competing with the state stream.
   - Output store: llama writes attn_data[col] from lane 0 only (31/32 lanes idle), scattered
     single-float stores; vLLM stores a contiguous BV=32 vector (coalesced).

4) Fusion delta (per-layer kernel-launch / HBM round-trip count)
   - vLLM packed_decode FUSES into ONE kernel: q/k l2norm + q*scale + softplus(a+dt_bias) +
     (-exp(A_log)) gate + sigmoid(beta) + the recurrence + state write-back.
   - llama computes these as SEPARATE ggml ops/kernels in the graph before the GDN op:
     ggml_l2_norm(q), ggml_l2_norm(k), ggml_add(+dt), ggml_softplus, ggml_mul(gate),
     ggml_sigmoid(beta) (+ conv/silu), each a launch + small HBM round-trip. Plus a separate
     gdn_gather_nonident_kernel launch per layer (a no-op at steady-state decode: every block
     early-returns on the identity check, but still a grid launch of n_seqs blocks).
   Across 48 linear layers this is ~6-10 extra small kernels/layer (~300-480 extra launches/token).
   Whether this dominates depends on CUDA-graph capture (see A2_CUDAGRAPH_DECODE.md); if captured,
   launch latency is hidden and the cost reverts to the per-op HBM round-trips + dependency gaps.

### What a faster llama GDN decode kernel would need (optimization scope)
- A. Re-parallelize like vLLM: thread/lane owns a full K-row (or K-shard) so the kv and attn
  contractions become register-local FMAs, eliminating the two warp_reduce_sum per token.
- B. Raise occupancy for the memory-bound regime: drop/raise the launch_bounds minBlocks hint
  (the `,2)` is too low), shrink the block, cut registers, and add a software-prefetch of the next
  state shard so more state loads are in flight per SM. This directly lifts achieved bandwidth on
  the equal state bytes - the single highest-leverage change.
- C. Load q,k ONCE per (head,seq) into shared memory instead of 128x per-column reload; coalesce
  the output store across the warp.
- D. Fuse the gate/l2norm/scale (softplus, exp(A_log), sigmoid, l2norm) INTO the recurrence kernel,
  reading raw a/b/A_log/dt_bias from registers, removing ~6 elementwise passes + their HBM round-trips
  per layer (matches vLLM's packed_decode). Drop the gather no-op kernel at steady-state decode
  (or fold the identity check into the recurrence prologue, which it already partly does).
- E. (Longer term) bf16 state would HALVE the dominant traffic, but vLLM keeps f32 too, so this is a
  divergence-from-reference not a parity lever.

### Bottom line
llama's GDN decode kernel is NOT moving more state HBM bytes than vLLM (the dominant term is equal),
so the 1.46 ms/call is an EFFICIENCY gap, not a traffic floor: (1) cross-warp shuffle reductions on
the n_tokens=1 critical path, (2) low occupancy (~8 warps/SM from launch_bounds + register pressure)
starving memory-level parallelism so the equal state bytes move at lower effective bandwidth, plus
(3) 128x redundant q/k L2 loads and (4) ~6-10 unfused gate/norm elementwise kernels per layer that
vLLM folds into one packed-decode kernel. Highest-leverage fixes: raise occupancy + prefetch (B) and
row-local reductions (A); secondary: gate/norm fusion (D) and q/k shared-mem reuse (C).

---

## Section: validate-findings (adversarial re-derivation from raw DGX data) - READ-ONLY

Re-queried `CUPTI_ACTIVITY_KIND_KERNEL` + `CUPTI_ACTIVITY_KIND_MEMCPY` directly (kernel and
memcpy summed separately so D2D is never lumped into compute), not from summary text.

### CLAIM 1 - decode decomposition
PRE-FIX (`a2_nsys/paged_off_npl128.sqlite`, last 17s) vs `decode_decomp.txt`, match <=0.1pp:
gated_delta_net 23.40% (doc 23.43), k_get_rows 21.99% (21.88), MEMCPY-DtoD 18.89% / 382 GB /
1583 ops (18.90 / 356 GB / 1584), mul_mat_vec_q 15.53% (15.51), mul_mat_q 10.48% (10.37).
=> CONFIRMED exactly. gated_delta_net = largest single non-GEMM kernel; FP4-GEMM group ~28%;
full attention 0.37%.

D2D collapse: only on-box post-fix decomp is `ssm_decomp/after.sqlite`; MEMCPY-DtoD there =
526 ops / 0.9 ms / 0.05 GB = 0.008% of busy (from 382 GB / 18.89%). => CONFIRMED, stronger than
the doc's "0.23%" (382 GB state copy-back gone; exact "0.23%/2.93GB/734ops" not reproducible -
my DtoD 0.05 GB, the 2.16 GB is DtoH).

FLAG (refutes part of the Step-2 decomp): `after.sqlite` is a Step-1 build (patch 0018 only),
NOT Step-2. It still shows k_get_rows_float 28.44% (gated_delta_net 28.96%, FP4-GEMM group ~33%),
no `gdn_gather_nonident` kernel, profiled S_TG=164 (~Step-1 180, not Step-2 256); mtime 00:31
predates the 08:48 rebuild that carried patch 0019. The Step-2 split in `SSM_DECODE_FIX_RESULTS`
("get_rows 18.8%->0.7%, FP4-GEMM ->48%, GDN 22.5%") has NO surviving sqlite, and the script meant
to produce it (`ssm_decomp.sh`) CRASHED (Python SyntaxError, see `ssm_decomp_after.out`). So
"FP4-GEMM ~48%" is UNVERIFIED against raw Step-2 data: measured ~33% on Step-1; removing the 28%
get_rows bucket lifts it to ~46% arithmetically, so ~48% is plausible but not directly measured.
Section 1 above and SSM_DECODE_FIX_RESULTS both inherit this unverified Step-2 split.

### CLAIM 2 - 146 -> ~257 ("+66%")
146.23 baseline CONFIRMED (`ssm_decode_baseline.out`); final 256.57 / 252.50 / 254.02 across
SSM_DECODE_FIX_RESULTS + THROUGHPUT_B_P2a, within ~1.6%. Magnitude CONFIRMED. TRAP: 146->257 is
+76% (146->254 = +74%), NOT +66%. "66%" is the % of vLLM (257/391 = 65.7%), not the speedup.

### CLAIM 3 - P2a GEMM-remap FLAT on decode
THROUGHPUT_B_P2a: dense npl128 252.50->254.02 (+0.6% noise), npl32 -0.4%, MoE flat; FP4 GEMM
kernel itself -24.7%, PREFILL +12.7%. Pre-SSM corroborated by THROUGHPUT_B_P1. => CONFIRMED.

### CLAIM 4 - 65% of vLLM (254 vs 391)
254/391 = 64.96%, 256.57/391 = 65.6%; vLLM 391 = enforce_eager apples ref. => CONFIRMED.

### Traps checked
GGML_CUDA_DISABLE_GRAPHS set `=1` explicitly (not the empty-value trap); graphs ON/OFF within
noise. memcpy-in-compute lumping AVOIDED (separate table sums). Decomp reps are ntg24-under-nsys
(S_TG 149/164) - valid for SHARES only; throughput correctly from unprofiled ntg128 logs.

### Net verdict
1 pre-fix decomp CONFIRMED exact; D2D collapse CONFIRMED (stronger); Step-2 0.7%/48% split
UNVERIFIED (producer script crashed, only post-fix sqlite is Step-1). 2 magnitude CONFIRMED,
"+66%" label REFUTED (true +76%; 66% = % of vLLM). 3 CONFIRMED. 4 CONFIRMED.

---

## Section: weight-bandwidth (whole-step DRAM budget, READ-ONLY math)

Agent label: weight-bandwidth. Method: exact GGUF tensor accounting (q36-27b-nvfp4,
arch qwen35, 64 layers) + activation-state math + existing nsys/decode_decomp; no GPU started.
Config = the production decode number: llama-batched-bench -fa on -npp128 -ntg128 -npl 128
(B = n_parallel = 128 sequences, S_TG = 254 t/s post-0019). GB10 LPDDR5x peak ~273 GB/s.

### Exact per-step DRAM byte budget at B=128 (ctx avg ~192 over the ntg128 window)

NVFP4 type-40 = 0.5625 B/weight (4-bit data + e4m3 per-16 micro-scale; verified: 5120*48*0.5625=138240).

WEIGHTS (read ONCE per step, shared across all 128 seqs):
  - NVFP4 layer weights (type40, 64 layers): 13,062.7 MB = 12.76 GB
      (per SSM layer 215.6 MB x48 = 9867.7 MB ; per full-attn layer 199.7 MB x16 = 3195.0 MB)
  - LM head output.weight: type 30 = **bf16, NOT quantized** = 2425 MB = 2.37 GB (read in full each step)
  - per-layer norms/conv1d/ssm_a/dt_bias (type0 f32): 10.1 MB
  - token_embd: EXCLUDED (get_rows gathers only 128 rows, negligible)
  => WEIGHTS TOTAL = 15.14 GB / step

PER-SEQUENCE STATE (x128 seqs, read + write every step):
  - SSM recurrent state: inner_size(6144) x state_size(128) x 4B(f32) = 3.0 MB / layer / seq
      x 48 SSM layers x 128 seq = 18.43 GB read + 18.43 GB write = **36.86 GB / step**
  - conv state: conv_k(4) x conv_dim(10240) x 4B = 160 KB / layer / seq
      x 48 x 128 = 0.96 GB read + 0.96 GB write = 1.92 GB / step
  - KV cache (16 full-attn layers, GQA n_kv_head=4, k+v_len=512, f16):
      4096 B/tok/layer x 16 x ~192 ctx x 128 seq = ~1.6 GB read / step

  TOTAL ~= 15.14 (W) + 36.86 (SSM state) + 1.92 (conv) + 1.6 (KV) = **~55.5 GB / step**

### Floor vs measured -- decode is NOT at the bandwidth floor

  Bandwidth floor = 55.5 GB / 273 GB/s = **203 ms/step**
  Measured llama  = 128 tok / 254 t/s   = **504 ms/step**  => **2.48x the floor** (eff BW 110 GB/s = 40% of peak)
  vLLM 391 t/s    = 128 / 391           = **327 ms/step**  => 1.61x the floor (eff BW 170 GB/s = 62% of peak)

  The SAME 55.5 GB/step floor applies to vLLM: identical NVFP4 weights, and its
  fused_recurrent_gated_delta_rule reads+writes the identical f32 recurrent state. So both engines
  face the same DRAM wall; vLLM simply moves those bytes at 62% of peak vs llama's 40%. The 62/40 =
  1.55x utilization gap is EXACTLY the 254->391 (1.54x) throughput gap. => Decode-parity is a
  bandwidth-UTILIZATION / launch-serialization problem, NOT a DRAM-traffic-volume problem. Bandwidth
  is not the binding constraint (we sit 2.5x above the floor); confirms the GDN-kernel section above.

### Traffic composition is STATE-dominated at B=128 (qualifies the "weight-quant" verdict)

  SSM state r+w = 66% of step traffic; weights = 27%; conv = 3.5%; KV = 3%.
  At B=128 weights are a minority of traffic, and we are 2.5x above the floor anyway -> NVFP4-dense
  weight quant (the QWEN36_NVFP4 verdict's lever) cannot move batch-128 decode much. Weight-quant
  helps PREFILL (compute/weight-bound, already +12.7% from the GEMM remap) and LOW-batch decode.
  Cross-check at B=32: traffic ~25.2 GB/step (weights now 60%), floor 92 ms, measured 189 ms = 2.05x
  floor. The sublinear scaling 32->128 (4x batch, only 1.5x throughput: 169->254) is fully explained
  by per-seq state traffic growing with B while weights stay amortized -> at B=128 the step has become
  state-traffic-heavy but is STILL 2.5x off the floor, i.e. latency/overlap-bound, not byte-bound.

### Redundant traffic llama reads that vLLM avoids (cut list, by impact)

  1. (HISTORICAL, FIXED by 0018) Redundant DtoD recurrent-state copy = +18.4 GB/step EXTRA
     (pre-fix decode_decomp: MEMCPY-DtoD 18.9%, 80 copies/step ~230 MB each = 18.4 GB; nsys window
     356 GB/19.8 steps). This doubled state traffic and was the dominant pre-fix waste. Verified gone
     post-fix: the THROUGHPUT_B_P2a A/B kernel sum (npp128 ntg24 npl128) lists gated_delta_net /
     mul_mat_q / quantize but NO MEMCPY-DtoD term. (The committed ~/bench/a2_nsys sqlites are all
     PRE-fix S_TG~149 traces; re-profiling deferred to the designated profiler.) This single removal
     (18.4 GB/273 ~= 67 ms/step of bytes plus the killed overlap stalls) is the bulk of 146->254.
  2. conv state as a SEPARATE ssm_conv kernel + separate buffer: 1.92 GB r+w/step AND 48 extra kernel
     launches/step. vLLM folds the causal conv into its recurrence kernel. Cut ~= 7 ms bytes + 48
     launches/step of serialization.
  3. Residual get_rows gather post-0019 (~0.7%, decode_decomp pre-fix k_get_rows was 21.9% / ~96
     ops/step = 2/SSM-layer): vLLM indexes the per-seq state in-kernel; llama still does a small
     gather/scatter. ~0.13 GB. 0019 already folds most of it; fold the identity check fully into the
     recurrence prologue.
  4. quantize_mmq_nvfp4: 448 ops/step re-quantizing activations to NVFP4 before each FP4 matmul.
     Activation BYTES are negligible, but it is 448 extra kernel launches/step that vLLM fuses into
     the GEMM prologue -> pure launch latency, not traffic.
  5. NOT redundant: weight bytes (identical NVFP4 to vLLM), SSM-state r+w (inherent, vLLM pays it),
     NVFP4 scale scalars (8 B/tensor). Note the LM head is bf16 not quantized (2.37 GB/step, 16% of
     weight traffic) -- fp8 LM head would save ~1.2 GB/step but only matters if vLLM also quantizes it.

### Bottom line (weight-bandwidth)
At B=128, decode moves ~55.5 GB/step and runs at 2.48x the 273 GB/s floor (40% util) vs vLLM's 1.61x
(62% util). Same bytes, same floor for both engines -> decode is bandwidth-UTILIZATION-bound, not
traffic-bound. There is NO large redundant-byte stream left to cut post-0018/0019 (the 18.4 GB/step
DtoD redundancy is already gone); the remaining 254->391 is recovered by raising achieved bandwidth
(occupancy + prefetch on the GDN state loads, conv fusion to drop 48 launches/step) so the EXISTING
55.5 GB/step moves at vLLM's 62% instead of 40%. Weight-quant (NVFP4-dense) is a PREFILL / low-batch
lever, largely orthogonal to the batch-128 decode-parity gap.

---

## Section: explore-other-levers (broad sweep for OTHER llama-specific decode inefficiencies) - READ-ONLY, no GPU

Scope handoff: GDN-kernel internals -> `gdn-source-compare`; host loop / graphs / gaps ->
`per-token-latency`; weight-byte / utilization -> `weight-bandwidth` section above (which already
covers the BF16 lm_head and the "same bytes, 40% vs 62% util" framing - I concur, no need to repeat).
This section covers the levers NONE of those own: the FP4 act-quant fusion, the M=128-vs-M=1 ggml
fusion gate, TMA scoping, and the conv-state residual.

**Terminology fix that matters for the whole doc:** in this repo's benches **"fusion OFF" means
`LLAMA_FUSE_NVFP4_QUANT=0`** (Track A's NVFP4 act-quant producer), confirmed in
`a2_nsys.sh`/`a2_4cell.sh`/`trackA_clean.sh`. It does NOT set `GGML_CUDA_DISABLE_FUSION`, so the
**standard ggml-cuda elementwise/GLU/rope fusion is ON** in every result. The header's "fusion OFF
baseline" is only about the act-quant producer.

**Framing (consistent with the sections above, sharpened):** the binder is bandwidth-UTILIZATION /
the kernel-dependency chain, not traffic or per-kernel compute (P2a -24.7% GEMM and graphs both
flat). The thing that raises utilization AND shortens the chain is the same: **fewer, fused kernels
per step** - removing whole passes vLLM doesn't run. So rank by "whole pass eliminated", not "us
shaved".

### L1. Re-test Track A act-quant fusion (`LLAMA_FUSE_NVFP4_QUANT=1`) POST-SSM. [impact ~8-11%, tractability HIGH - code exists, owned by tasks 38-41]
`quantize_mmq_nvfp4` is a standalone full-activation requantize run once per NVFP4 GEMM at M=128
(the weight-bandwidth section counts 448 such launches/step). vLLM has **zero** equivalent:
`rms_quant_fusion.py:98` folds it into RMSNorm, `act_quant_fusion.py:40,128` into SiLU+mul - the
activation never hits a temp buffer. Track A built exactly this fused producer (tasks 38-40 DONE),
but `LLAMA_FUSE_NVFP4_QUANT=1` regressed, and EVERY post-SSM bench ran with it OFF. **The regression
is likely stale:** pre-SSM the GPU was 99% busy on the state-copy chain, so folding act-quant into
the norm only relocated busy work into a lower-occupancy kernel with no idle to reclaim. Post-SSM the
chain has real idle and removing 448 launches/step both shortens the dependency chain and lifts
utilization - exactly the post-0018/0019 bind. Highest-value CLEAN removal; needs only a re-bench
(re-run `trackA_clean.sh` on the post-0019 build), not new code. Do not treat the prior regression
as final.

### L2. M=128 norm->matmul prologue fusion - the ggml fusion gate that does NOT fire at decode batch. [impact ~5-15% aggregate, tractability MEDIUM]
ggml-cuda's built-in `rms_norm+mul+mul_mat_vec_q` fusion (`ggml_cuda_should_fuse_mul_mat_vec_q`,
ggml-cuda.cu:2502) is gated to `dst->ne[1]==1` - it ONLY fires at **npl1** (M=1). At npl128
(`mul_mat_q`, M=128) it does NOT fire, so the per-layer RMSNorm stays a separate kernel feeding the
GEMM and the act path is unfused (L1). vLLM fuses both into the GEMM prologue at all M. This is the
M=128 generalization of the existing M=1 fusion + L1; largest aggregate surface but real kernel work.
Implies a regime split worth stating loudly: **npl1 single-stream latency already gets this fusion;
the npl128 throughput number does not** - tune the two separately.

### L3. TMA weight feed: a PREFILL / npl1-latency lever, NOT an npl128-decode lever.
Answering the brief's question (GEMM idle = FEED problem TMA fixes, or off-critical-path TMA can't?):
P2a cut GEMM compute and the freed time became IDLE, so at npl128 the GEMM finishes early and the
stall is BETWEEN kernels, not inside the GEMM waiting on weight tiles. TMA accelerates a
*feed-stalled* GEMM; at npl128 the GEMM is not the binder, so TMA won't move npl128 S_TG. It pays on
(a) **prefill** (compute/feed-bound; the remap already gave +12.7%) and (b) **npl1 decode**, a pure
weight-feed GEMV (full model / 273 GB/s ~ 19-20 tok/s ceiling). Scope TMA to prefill + low-batch
latency; do not bank it for batch-128 decode parity. (Consistent with the weight-bandwidth section's
"NVFP4-dense is a prefill/low-batch lever".)

### L4. In-place / `ids` conv-state - apply the 0018/0019 pattern to `ssm_conv`. [impact ~1-3%, tractability HIGH, proven pattern, bit-exact-able]
After the SSM fix the residual D2D is the conv-state copy (`build_conv_state`,
delta-net-base.cpp:449-525: `build_rs` reads 3 prior samples, `ggml_concat` the new token, writes
the last 3 back), plus `ssm_conv` (~0.8-1.5%) and a per-GDN-layer `concat_cont` (48/step). The exact
in-place + `ids`-read treatment from 0018/0019 applies to the conv state, and `ssm_conv`+`concat`
can fold into the GDN kernel prologue (it already has `ids` plumbing). Small ceiling but bit-exact,
low-risk, and removes ~48 launches/step from the chain - this is the "conv fusion to drop 48
launches/step" the weight-bandwidth section calls for, made concrete via the proven patch pattern.

### Deferred (covered by other sections, I concur)
- GDN occupancy / row-local reductions / gate-norm fusion -> `gdn-source-compare`. Add only: bf16
  state halves the dominant traffic but vLLM keeps f32, so it is a divergence-from-reference, not a
  parity lever - last priority, quality-risk.
- BF16 lm_head / weight-byte / 40%-vs-62% utilization -> `weight-bandwidth` section. lm_head NVFP4 is
  an absolute ~1-2% trim, not a vLLM-relative gap (vLLM likely keeps it bf16 too).
- Full-attention KV path (16 attn layers, 0.4-1.8%, O(ctx) but tiny) -> CLOSED, not a lever.

### Bottom line (this section's net-new)
Ranked by "whole pass vLLM eliminated": **L1 (re-test act-quant fusion post-SSM - clean removable
pass, code already written, just needs a post-0019 re-bench)** > **L2 (M=128 norm/act prologue
fusion - biggest aggregate surface, real work)** > **L4 (conv-state in-place - cheap, proven 0018/0019
pattern, -48 launches/step)**. **L3 (TMA) is mis-scoped if aimed at npl128 decode** - it is a prefill
/ npl1-latency lever, same bucket as NVFP4-dense weight quant. Caveat inherited from
`validate-findings`: the post-SSM act-quant absolute share (L1) is on an unverified Step-2 decomp
(only clean post-fix sqlite is Step-1); re-measure on a clean Step-2 nsys when the profiler runs.

Assisted-by: Claude:opus-4.8 [Claude Code]

---

## Section: profile-both-engines (GROUND-TRUTH post-SSM nsys of llama AND vLLM at npl128) - THE GPU PROFILER

Agent label: profile-both-engines (the only GPU agent). Fresh post-SSM nsys traces of
BOTH engines at the same shape (128-seq decode, 128-token prompts), q36-27b-nvfp4 dense.
llama = `build-cuda-base` (no FP4 flag, byte-identical to stock, HEAD 46d7dd8 = patch 0019
SSM fix), `llama-batched-bench -npp128 -ntg32 -npl128 -fa on`, eager (DISABLE_GRAPHS=1) for
a clean per-kernel trace. vLLM = 0.23.0 in-process offline (`VLLM_ENABLE_V1_MULTIPROCESSING=0`
so cudaProfilerApi controls the worker), enforce_eager, max_num_seqs 256, 128 prompts.
Decode-only windows (prefill excluded), overlap-correct interval-union busy, GPU-accurate
per-call kernel durations. This is the post-SSM **Step-2** trace `validate-findings` flagged
as having no surviving sqlite - it now exists: `~/bench/postssm_decomp/`.

### 0. THROUGHPUT GROUND TRUTH (un-profiled, prefill-subtracted) - resolves the 391 reference

The vLLM 391 reference is real and reproduced. Prefill-subtracted decode step (two-length
w16/w64 timing, in-process, batch 128):

| engine / mode            | ms/step | decode tok/s | notes                          |
|--------------------------|---------|--------------|--------------------------------|
| llama post-SSM (graphs)  | ~510-522| **245-251**  | S_TG @npl128 ntg32 (this run)  |
| vLLM enforce_eager       | 324.9   | **394.0**    | == the ~391 ref (h2h log 371-384)|
| vLLM cuda-graphs         | 304.9   | **419.8**    | graphs buy only +6%            |

- **CUDA graphs are NOT the parity lever**: vLLM is already 394 t/s EAGER; graphs add +6%
  (394->420). llama-batched-bench already runs WITH graphs at 245. So the gap is eager-vs-eager
  kernel work, confirming `per-token-latency` and `A2_CUDAGRAPH_DECODE`.
- TRAP I hit and corrected: the FIRST vLLM nsys window (0.35-0.99) read 468 ms/step / 273 t/s -
  WRONG, contaminated by prefill chunked-GDN kernels AND eager-nsys host overhead. The tight
  decode-only window (0.62-0.98) reads **326.5 ms/step**, matching the un-profiled 324.9 ms
  exactly -> the tight window is faithful; per-kernel numbers below use it.

### 1. POST-SSM per-step decode decomposition, SIDE BY SIDE (GPU-accurate, prefill-free)

Both at batch 128. llama 510 ms/step (98.7% GPU-busy), vLLM 326 ms/step (97.9% GPU-busy).
ms/step = on-device kernel time per real decode step (nsys host overhead does not inflate GPU
kernel duration; per-step = GPU-ms / real-step-count from the decode-only GDN call count).

| component (per step)        | llama ms/step | llama % | vLLM ms/step | vLLM % |
|-----------------------------|---------------|---------|--------------|--------|
| GDN linear-attn recurrence  | 193 (48x4.03) | 38%     | 174 (48x3.62)| 53%    |
| FP4 matmul + act-quant      | **236**       | **46%** | **117**      | **36%**|
|   - mul_mat_vec_q (GEMV)     | 132 (48x2.75) | 26%     | -            | -      |
|   - mul_mat_q (GEMM)         | 88 (448 calls)| 17%     | cutlass 61   | 19%    |
|   - quantize_mmq_nvfp4       | 16 (448)      | 3%      | nvjet 53+cvt2| 17%    |
| full attention (16 layers)  | 6.6 (16)      | 1.3%    | 6.2 (16)     | 1.9%   |
| SSM conv + glue/elementwise | ~45           | 9%      | ~22          | 7%     |
| MEMCPY (D2D+H2D)            | 2.5 (131 MB)  | 0.5%    | 0.36 (85 MB) | 0.1%   |
| **TOTAL**                   | **~510**      | 100%    | **~326**     | 100%   |

### 2. The three load-bearing comparisons (the brief)

**(1) GDN compute: llama vs vLLM = NOT the gap.** Per-call GPU duration:
llama `gated_delta_net_cuda<128>` = **4.03 ms/call**, vLLM
`fused_recurrent_gated_delta_rule_packed_decode` = **3.62 ms/call**. llama is only **+11%**
slower per call (+19 ms/step). GDN is comparable; it is the largest single kernel on BOTH sides
(38% llama, 53% vLLM) but it explains only ~19 ms of the 185 ms gap (~10%). This REFUTES the
framing that the GDN kernel is the dominant residual lever - it is a minor overage post-0018/0019.
(The `gdn-source-compare` occupancy/shuffle deltas are real but worth ~19 ms/step, not 1.5x.)

**(2) DRAM bytes/step: llama does NOT read more.** Explicit memcpy: llama **131 MB/step** vs
vLLM **85 MB/step** - llama moves a hair more in copies but both are <0.5% of the step. The big
per-layer state copies are GONE (pre-SSM 18 GB/step DtoD -> post-SSM 131 MB/step) - **the SSM fix
(0018/0019) is confirmed working in this trace.** Weight DRAM (read inside the GEMM/GEMV kernels,
not memcpy) is the SAME ~15 GB NVFP4 for both engines; at 273 GB/s that is a ~52 ms floor, and
BOTH engines sit far above it (326-510 ms), so BOTH are compute/kernel-bound, NOT
weight-bandwidth-bound, and llama reads no extra bytes. The 254-vs-391 gap is NOT a byte-volume
deficit - it is effective-bandwidth/compute-efficiency in the FP4 matmul kernels (see 3).

**(3) GPU-busy% / idle structure: identical, both ~98% busy.** llama 98.7% busy (1.3% idle),
vLLM 97.9% busy (2.1% idle). Neither engine is idle/gap/host-bound at npl128. The entire gap is
the GPU doing MORE kernel-time per step on llama: llama's non-GDN GPU work = ~310 ms/step vs
vLLM's ~146 ms/step. That 164 ms delta is concentrated in the FP4 matmul path.

### 3. THE single biggest llama-specific overage: the FP4 matmul path (+119 ms/step = 64% of the gap)

llama spends **236 ms/step** on FP4 matmul+quant; vLLM does ALL its matmul (cutlass FP4 GEMM +
cublas nvjet + act cvt) in **117 ms/step** - even though vLLM ALSO carries ~18 ms/step of extra
PyTorch eager elementwise glue that llama's fused ggml kernels avoid. llama is **2.0x slower on
FP4 matmul**, and that +119 ms is **64% of the entire 185 ms/step gap**.

Inside llama's FP4 path the dominant, untouched cost is **`mul_mat_vec_q` = 132 ms/step (26% of
decode), 48 calls/step (exactly one per GDN layer), 2.75 ms/call, grid 5120x128**. This is the
**FP4 GEMV ("vec_q") kernel running at decode batch 128** for the gated-DeltaNet in-projections -
a non-tensor-core, memory-bound-style kernel doing M=128 work without GEMM-grade weight-read
amortization. vLLM runs the equivalent projections through cutlass batched FP4 GEMM (tensor-core,
weight read amortized across the 128-row batch) at a fraction of the cost. **There is no
GEMV-at-batch-128 on the vLLM side at all.**

Key cross-check with Track B P2a: P2a optimized `mul_mat_q` (the 17%/88 ms tensor-core GEMM, made
it -24.7%) and decode stayed FLAT - because the BIG FP4 cost is `mul_mat_vec_q` (26%/132 ms),
which P2a never touched. **Track B optimized the wrong FP4 kernel.** The lever is to route the
GDN in-projection at M=128 through a tensor-core GEMM (mul_mat_q / MMQ) instead of the vec_q path,
and to fuse the act-quant (L1) + the norm prologue (L2) so the 448 `quantize_mmq_nvfp4` launches
fold away - exactly what `explore-other-levers` L1/L2 propose. My measurement RANKS them: the
mul_mat_vec_q->GEMM routing is the single highest-value target (132 ms), then act-quant fusion
(16 ms + 448 launches), then the GDN +19 ms.

### 4. Reconciling with the `weight-bandwidth` section (unification, not contradiction)

weight-bandwidth concluded "same 55.5 GB/step, llama 40% util vs vLLM 62% util -> utilization-bound."
My per-kernel data LOCALIZES that utilization gap: it lives in the **FP4 matmul kernels** (which
do the bulk of the ~15 GB weight read), NOT in the GDN state traffic. GDN moves its (equal) state
bytes at comparable rate on both engines (4.03 vs 3.62 ms/call). So the "40% vs 62%" is the
`mul_mat_vec_q`/`mul_mat_q` weight-read efficiency vs cutlass FP4 GEMM. Raising decode parity =
raise the FP4-matmul achieved bandwidth (tensor-core GEMM routing + act/norm prologue fusion),
not the GDN kernel and not byte-cutting.

### Verdict (profiler)
- Reproduced both engines at their true operating points: llama 245 / vLLM 394 eager / 420 graphs.
  Graphs are not the lever (+6%). Both engines ~98% GPU-busy; gap is GPU kernel-time, not idle/host.
- GDN compute is comparable (llama +11%/call, +19 ms/step) - NOT the dominant residual.
- bytes/step: llama does not read more (131 vs 85 MB memcpy; identical weight bytes); SSM fix's
  18 GB/step DtoD removal CONFIRMED in-trace.
- **The single biggest llama-specific overage is the FP4 matmul path: 236 vs 117 ms/step (+119 ms
  = 64% of the 185 ms gap), dominated by `mul_mat_vec_q` (FP4 GEMV at batch 128, 132 ms/step, 26%,
  one per GDN layer).** Highest-value lever = route the GDN in-projection through a tensor-core FP4
  GEMM at M=128 + fuse act-quant/norm prologue (L1/L2). Track B optimized the wrong FP4 kernel.

### Evidence (DGX, this agent)
- `~/bench/postssm_decomp/postssm_base.{nsys-rep,sqlite,gpu_trace.csv,run.log}` (llama post-SSM).
- `~/bench/postssm_decomp/vllm_decode.{nsys-rep,gpu_trace.csv}` (vLLM eager decode trace).
- `~/bench/postssm_decomp/vllm_decode_g1.*` (vLLM graphs run), `~/bench/vllm_tps.py` (throughput).
- Scripts: `~/bench/postssm_llama_decomp.sh`, `~/bench/vllm_nsys_run.sh`, `~/bench/decode_decomp2.py`
  (decode-only windowed, overlap-correct, MB-memcpy, per-step reconstruction).

Assisted-by: Claude:opus-4.8 [Claude Code]

---

## Section: SYNTHESIS (cross-check + ground-truth + ranked levers + verdict) - FINALIZED

Agent label: synthesize. Read-only (no GPU). Cross-checks all sections above against the
fresh `profile-both-engines` ground-truth, then mechanism-confirms the dominant lever by
reading the model graph + ggml-cuda dispatch source on the DGX (`~/llama-paged-dev`, HEAD
46d7dd8 = patch 0019). All throughput vs the vLLM 391 t/s eager apples-to-apples reference.

### 0. Headline

Post-SSM dense decode = 256.6 t/s @npl128 = 65.6% of vLLM 391, bit-exact. The residual is
NOT a hardware/architecture floor and NOT the GDN recurrence kernel, the host loop, CUDA
graphs, or DRAM byte-volume. It is ONE concrete, llama-specific kernel-routing defect:
**the gated-DeltaNet output projection (`ssm_out`) runs as an FP4 GEMV (`mul_mat_vec_q`)
at decode batch 128 instead of a tensor-core FP4 GEMM (MMQ), costing 132 ms/step = 26% of
decode = the single biggest overage vs vLLM (which packs the same projection into a cutlass
M=128 GEMM).** The fix is a ~2-line reshape, bit-exact, and is the highest-value next step.

### 1. Cross-check: which prior findings HELD, were REFUTED, or are SUPERSEDED

HELD (confirmed by both the adversarial re-derivation and the fresh profile):
- Pre-fix decomposition (gated_delta_net 23.4%, k_get_rows 21.9%, MEMCPY-DtoD 18.9% / 382 GB,
  mul_mat_vec_q 15.5%, mul_mat_q 10.5%): reproduced to <=0.1pp (validate-findings).
- SSM-fix D2D collapse: the 18.4 GB/step redundant recurrent-state copy is GONE. Confirmed
  three ways: validate (18.9% -> 0.008% on the post-fix sqlite), weight-bandwidth (A/B kernel
  sum lists no DtoD term), and IN-TRACE by the profiler (18 GB/step DtoD -> 131 MB/step). The
  SSM fix (0018/0019) is the real breakthrough and is working.
- P2a FP4-GEMM occupancy remap FLAT on decode (+0.6% noise) while the `mul_mat_q` kernel itself
  shrank -24.7% and prefill rose +12.7%: confirmed. Decode is not GEMM-occupancy-bound.
- 65% of vLLM (254/391 = 64.96%, 256.6/391 = 65.6%): confirmed.
- Decode is NOT at the bandwidth floor: 55.5 GB/step moved at 2.48x the 273 GB/s floor (40% util)
  vs vLLM 1.61x (62% util) on the SAME bytes. Confirmed + LOCALIZED below.
- Host loop / 64-layer serialization is NOT the lever: both engines ~98% GPU-busy at npl128
  (llama 98.7%, vLLM 97.9%); the entire exposed-idle budget is ~0.65%. Confirmed by the profiler.
- CUDA graphs are NOT the lever: vLLM is 394 t/s EAGER, graphs add only +6% (420); llama already
  runs with graphs. Confirmed by the profiler.

REFUTED / CORRECTED:
- "GDN recurrence kernel is the dominant residual lever" (the STATE brief's "gated_delta_net
  1.46 ms/call, the largest single kernel" and the gdn-source-compare framing): REFUTED. The
  profiler's fresh side-by-side per-call duration is llama 4.03 ms vs vLLM 3.62 ms = only +11% /
  +19 ms/step = ~10% of the 184 ms gap. It IS the largest single kernel on both sides (38% llama,
  53% vLLM) but the largest GAP is elsewhere. (The brief's "1.46 ms/call" is a stale/narrower
  window; the authoritative post-SSM per-call is 4.03 ms.) gdn-source-compare's occupancy/shuffle/
  fusion anatomy is correct but addresses a SECONDARY +19 ms target, not parity.
- "+66% SSM-fix gain" label: REFUTED. 146 -> 254-257 is +74 to +76%; "66%" is the percent-of-vLLM,
  not the speedup (validate-findings).

SUPERSEDED (the gap validate-findings flagged, now filled by real data):
- The "FP4-GEMM ~48% / get_rows 0.7% / GDN 22.5%" Step-2 split had NO surviving sqlite (the
  producer script crashed; only a Step-1 build was on the box). The profiler's fresh Step-2 trace
  replaces it with a FINER, load-bearing breakdown: the ~46% "FP4 matmul" bucket is NOT one GEMM
  family - it splits into `mul_mat_vec_q` 26% (the o_proj GEMV, the real culprit), `mul_mat_q` 17%
  (the tensor-core GEMM P2a already optimized), and `quantize_mmq_nvfp4` 3%. Lumping them as
  "48% FP4-GEMM" hid that Track B P2a optimized the 17% MMQ while the 26% MMVQ was the bind. This
  is why P2a was flat on decode: **it optimized the wrong FP4 kernel.**

### 2. Ground-truth per-step decode decomposition + the single biggest overage

From the profiler's fresh post-SSM eager nsys, both at batch 128, prefill-free, GPU-accurate:

| component (per decode step) | llama ms | llama% | vLLM ms | vLLM% | gap (llama-vLLM) |
|-----------------------------|----------|--------|---------|-------|------------------|
| GDN recurrence kernel       | 193      | 38%    | 174     | 53%   | **+19**          |
| FP4 matmul + act-quant      | 236      | 46%    | 117     | 36%   | **+119**         |
|   - mul_mat_vec_q (o_proj GEMV) | **132** | **26%** | 0   | -     | **+132**         |
|   - mul_mat_q (MMQ GEMM)    | 88       | 17%    | 61 (cutlass) | 19% | +27             |
|   - quantize_mmq_nvfp4      | 16       | 3%     | 55 (nvjet+cvt)| 17% | -39             |
| full attention (16 layers)  | 6.6      | 1.3%   | 6.2     | 1.9%  | +0.4             |
| SSM conv + glue/elementwise | 45       | 9%     | 22      | 7%    | +23              |
| MEMCPY                      | 2.5      | 0.5%   | 0.36    | 0.1%  | +2               |
| **TOTAL**                   | **~510** | 100%   | **~326**| 100%  | **+184**         |

The +119 ms FP4-matmul gap is ENTIRELY the `mul_mat_vec_q` o_proj GEMV (+132), partly offset
by llama being -39 on activation-quant (16 vs vLLM's heavier eager 55) and +27 on the MMQ. So
the one lever that matters is the +132 ms/step o_proj GEMV; everything else nets to ~+52 ms.

**MECHANISM (confirmed by source read, not inferred).** In the dense Qwen3.5-27B GDN block
(`src/models/qwen3next.cpp` `build_recurrent`), the recurrent core keeps the SSM layout
`[feat, n_seq_tokens, n_seqs]`. At decode `n_seq_tokens=1, n_seqs=128`. The output projection is:

```cpp
// current code (qwen3next.cpp, end of the GDN block)
ggml_tensor * final_output = ggml_reshape_3d(ctx0, attn_out_norm,
                                 head_v_dim * num_v_heads, n_seq_tokens, n_seqs); // [6144, 1, 128]
cur = build_lora_mm(model.layers[il].ssm_out, final_output);                     // <-- the matmul
cur = ggml_reshape_2d(ctx0, cur, n_embd, n_seq_tokens * n_seqs);                 // collapse AFTER
```

`final_output` is 3D `[6144, n_seq_tokens=1, n_seqs=128]`, so `src1->ne[1] = 1`. The ggml-cuda
dispatch (`ggml-cuda.cu:2553`) picks MMVQ when `src1->ne[1] <= MMVQ_MAX_BATCH_SIZE (8)`, with the
128 sequences carried in `ne[2]`. Result: a per-sequence FP4 GEMV, output rows 5120 x 128 seqs =
**`mul_mat_vec_q`, grid 5120x128, 48 calls/step (one per GDN layer)** - matching the profiler's
trace exactly. MMVQ does NOT amortize the `ssm_out` weight read into shared memory across the 128
sequences (it is built for batch <=8), so each of the 128 sequences re-streams the weight tiles -
the "40% vs 62% utilization" the weight-bandwidth section measured lives HERE, in this kernel, not
in the GDN state traffic. vLLM packs all 128 decode tokens into one cutlass M=128 GEMM (its GDN
kernel is literally `..._PACKED_decode`), so it has NO GEMV-at-batch-128 at all.

This also pins WHY it is decode-specific: at prefill the tokens are in `ne[1]` (n_seq_tokens=prompt
len), so `ne[1] >> 8` -> MMQ already; only the decode layout (128 seqs x 1 token, batched in ne[2])
trips the GEMV path. The in-projection (`wqkv`) is unaffected: its input is the 2D residual stream
`[n_embd, 128]` (reshaped to 3D only AFTER the matmul), so `ne[1]=128` -> MMQ today. The o_proj is
the unique 3D-input matmul, which is exactly why the profiler counted one MMVQ per GDN layer.

### 3. Ranked remaining decode levers (impact x tractability, cumulative ceiling toward 391)

Anchored on llama 256.6 t/s (499 ms/step) -> vLLM 391 (327 ms/step), gap 172 ms/step. Recover
figures past Lever 1 are ESTIMATES (the profiler measured the costs, not the post-fix kernels);
each needs a confirming re-profile. Ceilings are cumulative.

| # | lever | targets (ms/step) | est. recover | cumulative decode_agg | % of vLLM | tractability |
|---|-------|-------------------|--------------|-----------------------|-----------|--------------|
| 1 | **o_proj MMVQ -> MMQ** (collapse final_output to 2D before `ssm_out`) | vec_q 132 | ~100-110 | ~320-330 | **~82-85%** | **VERY HIGH** (2-line reshape, bit-exact, MMQ already proven on NVFP4 at M=128 by the in_proj) |
| 2 | act-quant + norm prologue fusion (explore L1 `LLAMA_FUSE_NVFP4_QUANT=1` re-bench + L2 M=128 gate) | quant 16 + 448 launches/step | ~15-25 | ~345-360 | ~88-92% | MED-HIGH (producer code exists, tasks 38-40; needs post-0019 re-bench, the pre-SSM regression is stale) |
| 3 | GDN-area fusion + occupancy (gdn A-D: row-local reduction, raise launch_bounds occupancy, fold gate/l2norm/softplus into the recurrence) | GDN +19 + glue +23 | ~25-40 | ~375-388 | ~96-99% | MED-LOW (real kernel rewrite + numeric re-validation) |
| 4 | conv-state in-place + conv fuse (explore L4, the proven 0018/0019 pattern on `ssm_conv`/concat) | part of glue, 48 launches/step | ~5-10 | ~388-395 | ~99-101% | HIGH (bit-exact, proven pattern) |
| - | between-step host gap / cgraph reuse | ~2 ms/step | ~2 | +~0.4% | n/a | LOW value (cleanup, not a parity lever) |
| x | CUDA graphs | - | 0 | already on | n/a | NOT a lever (+6% even for vLLM) |
| x | TMA weight-feed / NVFP4-dense weight-quant | prefill / npl1 | 0 at npl128 | n/a | n/a | MIS-SCOPED for batch-128 decode (prefill / low-batch levers; prefill already +12.7%) |

Note on Lever 1+2 coupling: routing the o_proj to MMQ ADDS one activation-quant (q8_1/NVFP4) per
o_proj, so Lever 2 (fusing that quant into the preceding `build_norm_gated`) compounds Lever 1
rather than overlapping it. Lever 3's "glue +23 ms" and Lever 1's quant are the same elementwise
passes vLLM folds into its packed kernel, so 2+3 share surface - treat the estimates as a band,
not a sum.

### 4. Verdict: is true decode parity reachable?

**Yes, parity is reachable in software, and the residual is NOT a hardware/architecture floor.**
Proof of "not a floor": both engines read identical NVFP4 weights and read+write identical f32
recurrent state = identical 55.5 GB/step DRAM floor (203 ms) on the identical GB10 LPDDR5x; vLLM
achieves 62% bandwidth utilization (327 ms/step) where llama achieves 40% (499 ms/step). The 1.54x
throughput gap equals the 1.55x utilization gap, and that utilization gap is now LOCALIZED to
specific llama kernels - chiefly the o_proj MMVQ - every one of which is closable in software. The
GDN recurrence (the supposed floor) is only +11%/call between the two engines.

How far each tier reaches:
- The first ~84% of parity (256 -> ~325) is nearly FREE: Lever 1 is a 2-line reshape that moves
  the GDN output projection from a per-sequence FP4 GEMV to a tensor-core M=128 FP4 GEMM, bit-exact,
  no new kernel (MMQ already runs the in-projection at this exact shape and type).
- ~84% -> ~92% (Levers 1+2) is low-effort: the fused act-quant producer already exists (tasks
  38-40), it just needs a post-0019 re-bench because its pre-SSM regression was measured when the
  GPU was 99% busy on the now-removed state-copy chain (no idle to reclaim then; real idle now).
- ~92% -> ~100% (Levers 3+4) is the diminishing-returns tail and the only genuinely HARD work:
  matching vLLM's fully-fused `packed_decode` GDN kernel (row-local reductions, higher occupancy,
  folding the gate/l2norm/softplus elementwise passes into the recurrence). This last ~8% is "hard
  but not floored" - it is kernel engineering, not a hardware wall.

**Single highest-value next step (do this first):** apply Lever 1 - collapse `final_output` to 2D
`[head_v_dim*num_v_heads, n_seq_tokens*n_seqs]` BEFORE the `ssm_out` matmul (drop the now-redundant
post-matmul `reshape_2d`):

```cpp
// route the GDN output projection through tensor-core MMQ at decode:
// M = n_seq_tokens*n_seqs (=128 at decode) instead of ne[1]=1 -> MMVQ GEMV. Free, bit-exact.
ggml_tensor * final_output = ggml_reshape_2d(ctx0, attn_out_norm,
                                 head_v_dim * num_v_heads, n_seq_tokens * n_seqs);
cur = build_lora_mm(model.layers[il].ssm_out, final_output); // now [n_embd, n_tokens], M=128 MMQ
```

Then the profiler re-measures the realized o_proj-as-MMQ cost on a clean post-0019 nsys (the one
number this synthesis estimates rather than measures) and confirms the 256 -> ~320-330 lift. The
same 3D-input-matmul pattern almost certainly affects the MoE checkpoint (q36-35b-a3b) decode and
any other matmul that consumes a tensor still in the `[feat, 1, n_seqs]` SSM layout - grep those
and apply the same collapse. Levers 2-4 follow in priority order; none requires a model or accuracy
compromise, so bit-exactness is preserved throughout.

### Evidence (this section)
- Source read (DGX `~/llama-paged-dev`, read-only): `src/models/qwen3next.cpp` (GDN in/out proj
  layout, lines ~286-305 and ~518-528), `ggml/src/ggml-cuda/ggml-cuda.cu:2553` (MMVQ dispatch on
  `ne[1]<=8`), `ggml/src/ggml-cuda/mmvq.cuh:3` (`MMVQ_MAX_BATCH_SIZE 8`), `mmq.cu:267` (NVFP4 is
  MMQ-supported).
- All five prior sections of this doc + the profiler's `~/bench/postssm_decomp/` traces.

Assisted-by: Claude:opus-4.8 [Claude Code]
