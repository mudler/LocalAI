# LEVER1_GATHER_PROGRESS.md - gather-build GPU agent checkpoint

Status: **DONE.** Residual k_get_rows fused in-place, bit-exact, both gates pass. Patch 0028.

## Lever
Fuse the residual `k_get_rows_float` in the GDN decode path (the biggest single kernel vLLM lacks,
~5.2 ms/step MoE per MOE_GAP_VS_VLLM.md). 0019 fused the SSM-state gather; 0021 fused the conv
compute but kept a `build_rs` gather for the conv taps. This patch closes that last gather.

## Located (nsys, DGX GB10, MoE q36-35b-a3b-nvfp4, npp128 ntg24 npl128)
The residual is the **conv-state tap gather** in `build_conv_state_fused`
(`src/models/delta-net-base.cpp`): the plain 4-arg `build_rs` -> `ggml_get_rows` of n_embd_r = 24576
floats (= (d_conv-1)*(d_inner + 2*n_group*d_state) = 3*8192) x 128 seqs, once per GDN layer per step.
Decode-window `k_get_rows_float<float,float>` had a BIG cluster of ~720 instances (30 GDN x 24) at
~115 us = ~3.4 ms/step (5.2 ms/step at steady ntg=128). grid (ne10=128, block_num_y=96) confirmed
ne00=24576 == n_embd_r (the SSM n_embd_s=524288 gather is already fused by 0019).

## Built (paged branch f32 default = 0026 hybrid default is f32)
New op `ggml_ssm_conv_update_inplace_ids` (src[4]=ids, op_params[1]=rs_head): reads each seq's prior
taps from cache[ids[s]] in-kernel (identity -> in place from conv_state_dst; non-identity -> disjoint
scratch via ssm_conv_gather_nonident_kernel). Mirrors 0019. Files: ggml.h, ggml.c, ssm-conv.cu,
ggml-cpu/ops.cpp, delta-net-base.cpp, tests/test-backend-ops.cpp. Build EXIT=0.

## GATE - PASS
- test-backend-ops (CUDA0 2/2): SSM_CONV_UPDATE_IDS OK (new), SSM_CONV_UPDATE OK, SSM_CONV OK,
  GATED_DELTA_NET OK, GET_ROWS OK.
- greedy md5 (-temp 0 -seed 1 -n 48) BYTE-IDENTICAL both models:
  dense 5951a5b4d624ce891e22ab5fca9bc439, MoE 07db32c2bcb78d17a43ed18bc22705cd (== baseline).
- nsys: k_get_rows<float,float> 10174 -> 9454 (720 fewer), 186.3 -> 102.8 ms; conv gathers replaced
  by 720 x ~1.1 us no-op gather. MoE npl128 783.9 t/s (step 163.3 ms vs 169.8 @0025), dense 377.3 t/s.

## Artifacts
- DGX: commit `944636c` on branch `paged`; LEVER1_GATHER_RESULTS.md in llama tree; nsys
  `/tmp/kgr_moe.nsys-rep` (before) + `/tmp/kgr_moe_after.nsys-rep` (after).
- LocalAI worktree: patches/paged/0028-qwen35-recurrent-state-gather-fusion.patch + LEVER1_GATHER_RESULTS.md.
- BOTH trees committed (-s). NOT pushed.

## Next
Ready for the rigorous same-session A/B decode bench (npl 32/128, dense + MoE, before/after on the
same 0026 base). The kernel-elimination and bit-exactness are proven; the bench quantifies the lift.

Assisted-by: Claude:opus-4.8 [Claude Code]
