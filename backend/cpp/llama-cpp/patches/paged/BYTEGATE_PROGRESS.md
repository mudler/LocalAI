# GDN Recurrence Byte-Gate - Progress (agent: ncu-byte-gate)

## Hard blocker on direct DRAM counters
- ncu HW perf counters: ERR_NVGPUCTRPERM (NVreg_RestrictProfilingToAdminUsers=restricted, root-only).
- nsys --gpu-metrics-devices: same ERR_NVGPUCTRPERM.
- No passwordless sudo on dgx.casa. DRAM byte counters UNOBTAINABLE without root.
- FALLBACK (decisive, no perfcounters needed): CUPTI kernel TIMING (allowed) + exact byte
  geometry from kernel source => implied effective BW + a hard mathematical cap on re-stream factor.

## Byte geometry (exact, from gated_delta_net.cu + GGUF)
- Qwen3.5 dense q36-27b-nvfp4: 48 GDN layers, H=48 v-heads, S_v=128 (square state 128x128/head).
- State per (seq,head) = 128*128 f32 = 64 KiB. Per seq = 48*64KiB = 3.0 MiB.
- Kernel is SINGLE-PASS by construction: loads s_shard[] ONCE into regs, recurrence in-register,
  writes state ONCE (read_state coalesced 128 consecutive f32/warp; writeback coalesced).
  l2norm/sigmoid/softplus/gate act on small q/k/g/beta (NOT the 805MB state); gather no-ops at
  steady decode (identity seqs). => NO multi-pass state re-streaming exists to fuse away.
- Minimal bytes/call (B=128): state R+W = 128*48*16384*4*2 = 805.3 MB; +q/k/v/out ~10 MB = ~816 MB.
- Floor time @273 GB/s = 816MB/273 = 2.99 ms/call.

## Measured (clean nsys CUDA timing, graphs OFF, npp8 ntg12 npl128, build-cuda-base df1cc97)
- llama gated_delta_net_cuda steady decode: 480 calls, grid(48,128,32), avg 3.98 ms/call
  (min 3.90, max 4.33; very tight => bandwidth-bound). 48 layers => 191 ms/step (50% of 384 ms).
- Implied effective BW @1.0x bytes = 816MB/3.98ms = 205 GB/s = 75% of 273 peak.
- HARD CAP: max bytes movable in 3.98ms @273 peak = 1.087 GB = 1.33x minimal.
  => re-stream factor in [1.0x, 1.33x]. 2x re-streaming PHYSICALLY IMPOSSIBLE.
  Source proves single-pass+coalesced => ~1.0x, kernel at ~75% peak.

## Conv-path (same trace, steady-decode region kernels, per-call):
- ssm_conv_f32: 672 calls whole-trace avg 135.9us (incl prefill); decode-region TBD
- concat_cont: 576 calls avg 169.6us ; concat_non_cont 96 calls (prefill big)
- cpy_scalar: 896 calls avg 123.7us ; gdn_gather_nonident 672 calls avg 153.9us (mostly no-op)

## vLLM (apples-to-apples: NSEQ=128, enforce_eager=True; postssm_decomp/vllm_decode.sqlite)
- vLLM state dtype = model_dtype = BF16 (_mamba_state_dtype default "auto"; config dtype=bfloat16).
  Geometry identical to llama (H=48, k/v head_dim 128, S_v 128).
- vLLM fused_recurrent_gated_delta_rule_packed_decode steady: 3.62 ms/call (grid 4x6144x1),
  bf16 state R+W = 402.6 MB => 111 GB/s = 41% peak. SINGLE-PASS (load p_h0 once -> f32 regs ->
  store bf16 once).
- llama 3.98 ms/call, f32 805.3 MB => 202 GB/s = 74% peak. llama kernel is MORE BW-efficient.

## Conv-path (llama steady decode, per call x48 layers)
- concat_cont 169.6us (8.14 ms/step) + cpy_scalar 120.1us (5.76) + ssm_conv_f32 115.9us (5.56)
  = ~19.5 ms/step. Conv state ~12.6 MB (tiny) => LAUNCH-bound, not byte-bound => fusion lever (~5%).
- l2_norm 6.8us, gdn_gather 1.21us (no-op identity seqs => gather does NOT re-stream state).

## FINAL VERDICT (DONE)
- llama re-stream factor ~1.0x (hard cap <=1.33x; >=1.5x physically impossible @273 peak).
- NO-BUILD fused single-pass recurrence: already single-pass, coalesced, 74% peak (> vLLM 41%);
  gate ops touch tiny q/k/g/beta, not the 805MB state => recovers ~0 state bytes.
- BUILD bf16 SSM state (design lever (2)): the 2x gap vs vLLM is 100% f32-vs-bf16 cache width.
  805->413 MB => ~45-95 ms/step => step 384 -> 289-339 ms = parity-to-ahead of vLLM 327.
  Non-bit-exact vs llama f32 but equal to vLLM's own bf16 precision.
- Findings written: GDN_RECURRENCE_BYTE_GATE.md (MEASUREMENT + VERDICT section appended).
