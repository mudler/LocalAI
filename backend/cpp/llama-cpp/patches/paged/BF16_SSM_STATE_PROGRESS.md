# bf16 SSM state - build/de-risk progress

DECISION (user override of plan): f32 DEFAULT + bf16 OPT-IN. type_s default = GGML_TYPE_F32.
Conv state (type_r) stays F32. Recurrence math stays f32 (load->f32, store->cache dtype).

## STEP 1 (dtype-generic kernel + op) - DONE + DE-RISK GATE 1 PASSED
Files (DGX ~/llama-paged-dev):
- ggml/src/ggml.c: 3 GDN builder asserts F32 -> {F32,BF16}; state_dst nb[0] -> ggml_type_size.
- ggml/src/ggml-cuda/gated_delta_net.cu: gdn_state_t<STATE_BF16> alias; gather + recurrence kernel +
  launchers templated on STATE_BF16; __bfloat162float load / __float2bfloat16 store; gather scratch
  shares cache dtype (uniform read); dispatcher detects src_state->type, GDN_DISPATCH macro 8-way.
- ggml/src/ggml-cpu/ops.cpp: byte-based read base + read_bf16 load conversion; bf16 in-place
  convert-store after token loop; bf16 gather widening; relaxed asserts to ggml_type_size.
- ggml/src/ggml-cpu/ggml-cpu.c: work-size +S_v*S_v for bf16 in-place.
- tests/test-backend-ops.cpp: state_type field on test_gated_delta_net; 16 bf16 cases (hs 64+128 x
  decode/prefill/keep_rs x kda).
GATE 1: build clean (EXIT=0); test-backend-ops -o GATED_DELTA_NET = 52/52 OK (CUDA bf16 vs CPU bf16).

## STEP 2/3/4 (cparams opt-in wiring) - IN PROGRESS
f32 DEFAULT everywhere; --cache-type-ssm bf16 opts in.

## STEP 2/3/4 (cparams opt-in) - DONE
- llama.h/llama-context.cpp/llama-memory.h/llama-model.cpp: type_r/type_s plumbed, DEFAULT F32.
- common.h/common.cpp/arg.cpp: cache_type_ssm/conv (F32 default) + --cache-type-ssm/-conv CLI.
- llama-memory-recurrent.cpp: convert-on-mismatch f32<->bf16 (r and s) via ggml_*_row API.

## EXTRA FIX (plan B.1 miss): build_rs rs_zero clear used ggml_scale (f32-only) -> bf16 abort.
- llama-graph.cpp: f32 keeps ggml_scale_inplace (bit-exact); non-f32 uses ggml_fill_inplace.
- fill.cu + ops.cpp + ggml.c: added BF16 to ggml_fill. get_rows/cpy already bf16-capable.

## DE-RISK GATE - ALL PASS
- build clean EXIT=0 (test-backend-ops, llama-completion, llama-cli, llama-perplexity, llama-batched-bench).
- test-backend-ops -o GATED_DELTA_NET = 52/52 (16 bf16 cases: decode/prefill/keep_rs x kda x hs64/128).
- f32 default md5: dense 5951a5b4... MoE 07db32c2... == 0023 (non-invasive; also --cache-type-ssm f32 matches).
- bf16 opt-in: coherent "Paris", no crash; byte-identical to f32 on 48-tok sample (Same-top-p 100%).
- diff backup: ~/llama-paged-dev/BF16_SSM_STATE.diff (1003 lines, 15 files). NOT committed/pushed.
READY FOR C.2 KL GATE (GateBench).
