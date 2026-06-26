# Lever 1 (residual recurrent-state gather fusion) - PROGRESS / gather-bench DONE

STATUS: COMPLETE. Bit-exact, both gates green, rigorous same-session A/B bench done, committed both trees.

## What
Fused the residual conv-state tap k_get_rows (build_conv_state_fused) in-place into the SSM_CONV
update via ggml_ssm_conv_update_inplace_ids (src[4]=ids discriminator). Mirrors 0019 (SSM-state) +
0018 (in-place). Eliminates the last k_get_rows in the GDN decode path. Bit-exact by construction
(read path gather -> indexed in-kernel read; values + reduction order unchanged).

## Gates (lever1 build = build-cuda, base = build-cuda-base = 0026)
- md5 greedy --temp 0 --seed 1 -n 48: dense 5951a5b4d624ce891e22ab5fca9bc439 == baseline;
  MoE 07db32c2bcb78d17a43ed18bc22705cd == baseline; base == lever1 (byte-identical).
- test-backend-ops CUDA0: SSM_CONV_UPDATE_IDS 16/16, SSM_CONV_UPDATE 16/16, SSM_CONV 45/45,
  GATED_DELTA_NET 84/84, GET_ROWS 47/47 - all OK.

## Bench (S_TG t/s, npp128 ntg128 npl 32/128)
- dense npl128 369.95 -> 377.83 (+2.13%, 94.6 -> 96.6% of vLLM 391); npl32 208.56 -> 209.39.
- MoE   npl128 763.47 -> 777.95 (+1.90%, 84.7 -> 86.3% of vLLM 901); npl32 456.85 -> 459.56.
- nsys MoE decode: k_get_rows_float 17334 -> 15414 inst (-1920 = 30 GDN x 64 steps), 358.37 -> 133.52 ms;
  step 167.7 -> 164.5 ms (-3.13 ms/step). gather eliminated, replaced by no-op nonident kernel.

## Artifacts
- Patch: patches/paged/0028-qwen35-recurrent-state-gather-fusion.patch (LocalAI worktree)
- Docs: LEVER1_GATHER_RESULTS.md (full bench tables)
- DGX bench outs: ab_{dense,moe}_{base,lever1}.out, nab_{base,lever1}.kern.csv, md5{d,m}_{base,lever1}.txt

## gather-bench landed (worktree)

Rigorous same-session A/B (DGX GB10) validated patch 0028 bit-exact and lifting both models;
results folded into LEVER1_GATHER_RESULTS.md and the regenerated 0028 patch. The bench files
first landed in this worktree via concurrent merge c1f1d1e8e (origin/master sweep); this commit
re-anchors them with sign-off attribution. DGX llama tree dedicated commit: fafe878 (code
byte-identical to 944636c; docs-only amend). Both trees committed, not pushed.
