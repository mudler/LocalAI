# Phase 8 Ragged MoE Dispatch Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Decide whether GB10 serving parity should target a fused routed-expert `MUL_MAT_ID` dispatch path for ragged MoE decode, then implement only if profiling proves the bucket is material.

**Architecture:** Phase 8 is profile-gated. First decompose serving decode into routing compaction, activation quant/gather, grouped MMQ, scatter/fan-in, GDN, and FA buckets. Only if the `MUL_MAT_ID` routing/compaction/MMQ bucket expands materially in live ragged serving do we add a default-off fused-dispatch candidate in llama.cpp.

**Tech Stack:** llama.cpp CUDA backend, Nsight Systems, `/home/mudler/bench/bucket.py`, LocalAI paged patch mirror, GB10 DGX host `dgx.casa`.

---

## Context

Rejected Phase 7 shortcuts:

- SWIGLU-down NVFP4 quantization fusion: focused op gate passed, but opt-in
  paged-MoE md5 changed and serving A/B was flat.
- Post-down weighted-combine fan-in fusion: md5-safe and Nsight-proven to fire,
  but serving A/B was flat (`decode_agg_tps 417.5 -> 417.0`).

Deferred non-default work:

- Backend sampler logit-bias upload caching is real but only applies to
  `--backend-sampling` with request `backend_sampling: true` and non-empty
  `logit_bias` or `ignore_eos`. It is not a default greedy parity lever.

Selected Phase 8 candidate:

- Fused routed-expert `MUL_MAT_ID` dispatch for ragged serving decode.
- This is distinct from fan-in-only fusion because it attacks the earlier chain:
  `mm_ids_helper -> activation quant/gather -> grouped MMQ -> dst scatter`.

## File Map

- Read/profile only:
  - `/home/mudler/_git/llama.cpp/src/llama-graph.cpp`
  - `/home/mudler/_git/llama.cpp/ggml/src/ggml-cuda/topk-moe.cu`
  - `/home/mudler/_git/llama.cpp/ggml/src/ggml-cuda/mmid.cu`
  - `/home/mudler/_git/llama.cpp/ggml/src/ggml-cuda/mmq.cu`
  - `/home/mudler/_git/llama.cpp/ggml/src/ggml-cuda/mmq.cuh`
  - `/home/mudler/_git/llama.cpp/ggml/src/ggml-cuda/ggml-cuda.cu`
- If promoted to source:
  - Modify: `/home/mudler/_git/llama.cpp/ggml/src/ggml-cuda/mmid.cu`
  - Modify: `/home/mudler/_git/llama.cpp/ggml/src/ggml-cuda/mmq.cu`
  - Modify: `/home/mudler/_git/llama.cpp/ggml/src/ggml-cuda/mmq.cuh`
  - Modify: `/home/mudler/_git/llama.cpp/ggml/src/ggml-cuda/ggml-cuda.cu`
  - Test: `/home/mudler/_git/llama.cpp/tests/test-backend-ops.cpp`
- Tracking docs:
  - Modify:
    `/home/mudler/_git/LocalAI/.claude/worktrees/feat+paged-attention/docs/superpowers/plans/2026-07-01-serving-ragged-moe-phase8.md`
  - Modify:
    `/home/mudler/_git/LocalAI/.claude/worktrees/feat+paged-attention/backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md`
  - Modify:
    `/home/mudler/_git/LocalAI/.claude/worktrees/feat+paged-attention/backend/cpp/llama-cpp-localai-paged/docs/VLLM_PARITY_LEVER_MAP.md`

## Required Safety Gates

- Before DGX work:
  - `docker ps -q | wc -l` must be `0`.
  - no `local-ai-worker` container may be running.
  - `nvidia-smi --query-compute-apps=pid --format=csv,noheader` must be empty.
  - `~/gpu_bench_lock/owner` must be absent or start with `FREE`.
- Before keeping any source patch:
  - MoE transcript md5 must be `8cb0ce23777bf55f92f63d0292c756b0`.
  - Dense transcript md5 must be `5951a5b4d624ce891e22ab5fca9bc439`.
  - `test-backend-ops test -b CUDA0 -o MUL_MAT_ID -j 1` must report `806/806`.
  - If adding a specific ragged op test, it must include `n_expert=256`,
    `n_expert_used=8`, single-token decode, empty experts, ragged expert loads,
    and `ne2 > get_mmvq_mmid_max_batch(...)`.
  - CUDA graph replay must still work with `LLAMA_MOE_FORCE_GRAPHS=1`.
  - Source candidate must be default-off first, e.g.
    `LLAMA_MOE_FUSED_DISPATCH=1`.
  - No D2H id readback or new `cudaStreamSynchronize` may enter the decode path.

## Task 1: Profile-Gate Ragged MoE Dispatch

**Files:**
- Modify:
  `docs/superpowers/plans/2026-07-01-serving-ragged-moe-phase8.md`
- Modify:
  `backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md`

- [x] **Step 1: Record Phase 8 scope**

  Write this plan and commit it before source work.

- [x] **Step 2: Reconfirm DGX idle state**

  Run:

  ```bash
  ssh dgx.casa 'set -e
  echo docker=$(docker ps -q | wc -l)
  echo local_ai_worker=$(docker ps --format "{{.Names}}" | grep -c local-ai-worker || true)
  echo compute=$(nvidia-smi --query-compute-apps=pid --format=csv,noheader | sed "/^$/d" | wc -l)
  if [ -f ~/gpu_bench_lock/owner ]; then cat ~/gpu_bench_lock/owner; else echo FREE-no-lock-file; fi'
  ```

  Expected:

  ```text
  docker=0
  local_ai_worker=0
  compute=0
  FREE...
  ```

- [x] **Step 3: Run serving nsys for llama.cpp MoE**

  Run on DGX:

  ```bash
  ssh dgx.casa 'cat > /tmp/phase8_llama_nsys.sh <<'"'"'SH'"'"'
  #!/usr/bin/env bash
  set -euo pipefail
  ART=$HOME/bench/phase8_ragged_moe_dispatch/llama_n128
  BIN=$HOME/llama-phase6-source/build-cuda/bin
  MOE=/home/mudler/bench/q36-35b-a3b-nvfp4.gguf
  H2H=$HOME/bench/h2h_cli3.py
  mkdir -p "$ART"
  pkill -9 -f "[l]lama-server" 2>/dev/null || true
  cd "$BIN"
  env LLAMA_KV_PAGED=1 LLAMA_MOE_FORCE_GRAPHS=1 GDN_CHUNK_MIN=1 GDN_TC=5 GGML_NO_BACKTRACE=1 \
    nsys profile --trace=cuda --sample=none --cpuctxsw=none --force-overwrite=true \
    -o "$ART/llama_n128" \
    ./llama-server -m "$MOE" -c 262144 --parallel 256 -b 2048 -ub 512 -ngl 99 -fa on \
      --host 127.0.0.1 --port 8092 --no-webui > "$ART/server.log" 2>&1 &
  pid=$!
  for i in $(seq 1 360); do
    curl -s -m2 http://127.0.0.1:8092/health | grep -q ok && break
    kill -0 "$pid" 2>/dev/null || { tail -30 "$ART/server.log"; exit 1; }
    sleep 1
  done
  python3 "$H2H" --url http://127.0.0.1:8092/v1/completions --model q36 -n 8 --ptok 128 --gen 32 \
    > "$ART/warmup.json" 2> "$ART/warmup.err" || true
  python3 "$H2H" --url http://127.0.0.1:8092/v1/completions --model q36 -n 128 --ptok 128 --gen 64 \
    > "$ART/client_n128.json" 2> "$ART/client_n128.err"
  kill "$pid" 2>/dev/null || true
  for i in $(seq 1 60); do kill -0 "$pid" 2>/dev/null || break; sleep 1; done
  kill -9 "$pid" 2>/dev/null || true
  python3 $HOME/bench/bucket.py "$ART/llama_n128.nsys-rep" llama_phase8_n128 > "$ART/buckets.txt"
  SH
  bash /tmp/phase8_llama_nsys.sh'
  ```

  Expected:

  - `client_n128.json` contains `decode_agg_tps`, `decode_perseq_tps`, and
    `prefill_tps`.
  - `buckets.txt` has fine rows for `mm_ids`, `gather_mmq`, `act_quant`,
    `mmq_nvfp4`, `set_rows`, `ew_add`, `gdn_core`, and `fa`.

  Result:

  - Artifact: `/home/mudler/bench/phase8_ragged_moe_dispatch/llama_n128_clean/`.
  - Throughput: `decode_agg_tps=412.1`, `decode_perseq_tps=2.70`,
    `prefill_tps=1368.3`.
  - Clean rebuild was required before this run; the first `llama_n128/` profile
    still contained the rejected weighted-combine kernel in the binary and is
    not used for the decision.
  - Bucket highlights:
    - GDN: `4680.27 ms`, `38.12%`.
    - `mmq_nvfp4`: `2745.11 ms`, `22.36%`.
    - `act_quant`: `441.42 ms`, `3.60%`.
    - MoE dispatch: `183.67 ms`, `1.50%`.
    - `mm_ids`: `80.92 ms`, `0.66%`.
    - `gather_mmq`: `50.96 ms`, `0.42%`.
    - `ew_add`: `280.15 ms`, `2.28%`.

- [x] **Step 4: Run serving nsys for vLLM MoE**

  Run on DGX:

  ```bash
  ssh dgx.casa 'cat > /tmp/phase8_vllm_nsys.sh <<'"'"'SH'"'"'
  #!/usr/bin/env bash
  set -euo pipefail
  ART=$HOME/bench/phase8_ragged_moe_dispatch/vllm_n128
  MODEL=/home/mudler/bench/q36-35b-a3b-nvfp4-vllm
  H2H=$HOME/bench/h2h_cli3.py
  mkdir -p "$ART"
  pkill -9 -u "$(id -u)" -f "[v]llm serve" 2>/dev/null || true
  export PATH="$HOME/vllm-bench/bin:$PATH"
  export VLLM_LOGGING_LEVEL=INFO
  export HF_HUB_OFFLINE=1
  nsys profile --trace=cuda --sample=none --cpuctxsw=none --force-overwrite=true \
    -o "$ART/vllm_n128" \
    "$HOME/vllm-bench/bin/vllm" serve "$MODEL" --served-model-name q36 \
      --gpu-memory-utilization 0.85 --max-model-len 4096 --max-num-seqs 256 \
      --host 127.0.0.1 --port 8002 --tensor-parallel-size 1 > "$ART/server.log" 2>&1 &
  pid=$!
  for i in $(seq 1 420); do
    curl -s -m2 http://127.0.0.1:8002/v1/models | grep -q q36 && break
    kill -0 "$pid" 2>/dev/null || { tail -40 "$ART/server.log"; exit 1; }
    sleep 1
  done
  python3 "$H2H" --url http://127.0.0.1:8002/v1/completions --model q36 -n 8 --ptok 128 --gen 32 \
    > "$ART/warmup.json" 2> "$ART/warmup.err" || true
  python3 "$H2H" --url http://127.0.0.1:8002/v1/completions --model q36 -n 128 --ptok 128 --gen 64 \
    > "$ART/client_n128.json" 2> "$ART/client_n128.err"
  kill "$pid" 2>/dev/null || true
  for i in $(seq 1 80); do kill -0 "$pid" 2>/dev/null || break; sleep 1; done
  kill -9 "$pid" 2>/dev/null || true
  python3 $HOME/bench/bucket.py "$ART/vllm_n128.nsys-rep" vllm_phase8_n128 > "$ART/buckets.txt"
  SH
  bash /tmp/phase8_vllm_nsys.sh'
  ```

  Expected:

  - `client_n128.json` contains comparable throughput.
  - `buckets.txt` has vLLM rows for `vllm_dispatch`, `vllm_fp4_gemm`,
    `vllm_fa`, and `fla_gdn`.

  Result:

  - Artifact: `/home/mudler/bench/phase8_ragged_moe_dispatch/vllm_n128/`.
  - Throughput: `decode_agg_tps=1036.6`, `decode_perseq_tps=7.02`,
    `prefill_tps=5277.7`.
  - Nsight includes startup/autotune and `delayStreamKernel`, so the aggregate
    vLLM macro percentages are not directly comparable to llama.cpp. Direct
    kernel extraction still shows Marlin-MoE rows around `1.0 s` and
    `moe_align/topk/count` rows around `38.5 ms` in the full capture.

- [x] **Step 5: Decide promotion**

  Promote to source only if all are true:

  - llama.cpp `MoE-dispatch` plus `MoE/FFN-GEMM` fine rows are a materially
    larger share than expected from Phase 6 or worse than vLLM on the same
    serving shape.
  - `mm_ids`, `gather_mmq`, `act_quant`, or grouped `mmq_nvfp4` is a clear
    target, not hidden by GDN or FA.
  - Serving throughput gap is still visible in the same profile.

  Reject or defer if:

  - GDN remains the dominant gap.
  - FA prefill dominates the profiled window.
  - MoE dispatch is too small to beat a `+5%` serving A/B gate.

  Decision:

  - Promote to Task 2 test-gate work, not production source work yet.
  - Rationale: standalone `mm_ids` and `gather_mmq` are small, but the live
    ragged path around `mmq_nvfp4 + act_quant + MoE-dispatch + fan-in` is
    roughly `29.7%` of llama.cpp kernel time. vLLM throughput is still much
    higher on the same client shape. A production patch is only justified after
    a ragged `MUL_MAT_ID` test gate exists and after the source prototype can
    reduce the grouped-MMQ/activation movement bucket, not merely the helper
    kernels.
  - GDN remains the single largest bucket, so any Phase 8 source patch still
    must clear the `+5%` serving A/B gate before being kept.

- [x] **Step 6: Commit the profile decision**

  If promoted:

  ```bash
  git add docs/superpowers/plans/2026-07-01-serving-ragged-moe-phase8.md \
    backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md \
    backend/cpp/llama-cpp-localai-paged/docs/VLLM_PARITY_LEVER_MAP.md
  git commit -m "docs(paged): scope ragged MoE dispatch phase" \
    -m "Assisted-by: Codex:gpt-5"
  ```

  If rejected:

  ```bash
  git add docs/superpowers/plans/2026-07-01-serving-ragged-moe-phase8.md \
    backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md \
    backend/cpp/llama-cpp-localai-paged/docs/VLLM_PARITY_LEVER_MAP.md
  git commit -m "docs(paged): reject ragged MoE dispatch phase" \
    -m "Assisted-by: Codex:gpt-5"
  ```

  Result:

  - Committed the profile decision as `89ef3a402`
    (`docs(paged): record ragged MoE profile gate`).
  - The follow-up test gate landed as fork commit `e21732fc4` and LocalAI
    mirror commit `b009de0ee`.
  - The source shortcut rejection landed as `b862e2c56`
    (`docs(paged): stop ragged dispatch source shortcut`).

## Task 2: Add Ragged `MUL_MAT_ID` Test Gate If Promoted

**Files:**
- Modify: `/home/mudler/_git/llama.cpp/tests/test-backend-ops.cpp`
- Mirror patch under:
  `/home/mudler/_git/LocalAI/.claude/worktrees/feat+paged-attention/backend/cpp/llama-cpp-localai-paged/patches/paged/`

- [x] **Step 1: Add a test-only fork patch**

  Add a `MUL_MAT_ID_RAGGED_MOE` whole-graph test that exercises:

  - `type_a=nvfp4`
  - `n_mats=256`
  - `n_used=8`
  - `n_tokens in {1, 8, 33, 128, 257}`
  - explicitly empty experts and high skew into 1-row experts

  Result:

  - Fork commit: `e21732fc4` (`test(paged): cover ragged MoE dispatch`).
  - LocalAI patch:
    `0053-test-paged-cover-ragged-MoE-dispatch.patch`.
  - Coverage:
    - one small F32 wiring case,
    - NVFP4 with `n_mats=256`, `n_used=8`, `m=768`, `k=2048`,
      `n in {1, 8, 33, 128, 257}`.
    - deterministic unique top-k ids skewed toward hot experts, including
      expert `255`, with many empty experts.

- [x] **Step 2: Run red/green if the test exposes a missing path**

  Run:

  ```bash
  ./build-cuda/bin/test-backend-ops test -b CUDA0 -o MUL_MAT_ID_RAGGED_MOE -j 1
  ```

  Expected after adding only the test:

  - Existing path should pass. If it fails, stop and debug before production
    code.

  Result:

  - Initial test failed because the first deterministic ID pattern created
    duplicate expert IDs within the same token, which is not a valid top-k
    routing shape. The corrected gate preserves unique expert IDs per token.
  - DGX artifact:
    `/home/mudler/bench/phase8_ragged_moe_dispatch/test_backend_ops_mul_mat_id_ragged_moe_fixed.txt`.
  - Result: `MUL_MAT_ID_RAGGED_MOE` `6/6` on CUDA0.

- [x] **Step 3: Mirror the test patch**

  Generate with:

  ```bash
  git format-patch -1 --stdout > /tmp/0053-test-paged-cover-ragged-MoE-dispatch.patch
  ```

  Copy into LocalAI only after checking patch order.

  Result:

  - Patch `0053-test-paged-cover-ragged-MoE-dispatch.patch` added after
    `0052-test-paged-cover-MoE-weighted-combine-chain.patch`.

## Task 3: Default-Off Fused Dispatch Prototype If Promoted

**Files:**
- Modify: `/home/mudler/_git/llama.cpp/ggml/src/ggml-cuda/mmid.cu`
- Modify: `/home/mudler/_git/llama.cpp/ggml/src/ggml-cuda/mmq.cu`
- Modify: `/home/mudler/_git/llama.cpp/ggml/src/ggml-cuda/mmq.cuh`
- Modify: `/home/mudler/_git/llama.cpp/ggml/src/ggml-cuda/ggml-cuda.cu`

**Status:** Rejected before production CUDA edits. The profile and code
inspection do not justify a metadata/helper-only prototype.

Inspection result:

- `ggml_cuda_mul_mat_q()` already runs the ids path as
  `mm_ids_helper -> quantize/gather -> grouped MMQ`.
- For native FP4 MoE with broadcast activations (`ne11 == 1`), patch `0023`
  already quantizes unique tokens once and gathers FP4 blocks:
  `quantize_mmq_fp4_cuda(... ids=nullptr ...)` followed by
  `gather_mmq_fp4_cuda(...)`.
- The live serving profile shows `mm_ids` at `0.66%` and `gather_mmq` at
  `0.42%`, while `mmq_nvfp4` is `22.36%` and `act_quant` is `3.60%`.
- Therefore a safe Phase 8 production patch must change grouped-MMQ execution
  shape or activation movement. A default-off hook that only skips or repacks
  metadata is not expected to clear the `+5%` serving A/B gate.

Stop condition:

- Do not edit production CUDA for Phase 8 until there is a concrete design for
  reducing `mmq_nvfp4` or `act_quant` time without D2H id readback, new stream
  synchronizations, or md5 drift.

- [x] **Step 1: Add env-gated entry point**

  Decision: not implemented. Adding a default-off env hook without a concrete
  `mmq_nvfp4` or activation-movement reduction would add patch-stack conflict
  surface while preserving the same slow path.

  Add a default-off env gate:

  ```cpp
  static bool ggml_cuda_moe_fused_dispatch_enabled() {
      static const bool enabled = [] {
          const char * e = getenv("LLAMA_MOE_FUSED_DISPATCH");
          return e != nullptr && std::atoi(e) != 0;
      }();
      return enabled;
  }
  ```

  The default path must remain byte-identical and use the existing
  `ggml_cuda_mul_mat_id` implementation.

- [x] **Step 2: Add the smallest measurable fused metadata path**

  Decision: not implemented. The live profile puts the metadata helpers below
  the `+5%` serving A/B threshold (`mm_ids=0.66%`, `gather_mmq=0.42%`), and
  patch `0023` already avoids repeated activation quantization for the
  broadcast-activation NVFP4 MoE case.

  Start by replacing repeated host/device metadata setup only when all are true:

  - CUDA backend.
  - `src0->type == GGML_TYPE_NVFP4`.
  - `ids` are already device-resident.
  - decode-ish `src1->ne[1] <= 128`.
  - no D2H id readback.

  If this cannot be done without syncs, stop and reject the prototype.

- [x] **Step 3: Run gates**

  Rerun result from the unchanged production path:

  - Artifact:
    `/home/mudler/bench/phase8_ragged_moe_dispatch/safety_rerun_20260701_035549/`
  - MoE md5: `8cb0ce23777bf55f92f63d0292c756b0`.
  - Dense md5: `5951a5b4d624ce891e22ab5fca9bc439`.
  - Full `MUL_MAT_ID`: `806/806` on CUDA0.
  - Specific `MUL_MAT_ID_RAGGED_MOE`: `6/6` on CUDA0, rerun artifact
    `/home/mudler/bench/phase8_ragged_moe_dispatch/ragged_gate_rerun_20260701_035529.txt`.

  Run on DGX:

  ```bash
  ./test-backend-ops test -b CUDA0 -o MUL_MAT_ID -j 1
  ```

  Expected: `806/806`.

  Run transcript gates:

  ```bash
  env LLAMA_KV_PAGED=1 LLAMA_MOE_FORCE_GRAPHS=1 GDN_CHUNK_MIN=1 GDN_TC=5 GGML_NO_BACKTRACE=1 \
    ./llama-completion -m /home/mudler/bench/q36-35b-a3b-nvfp4.gguf -ngl 99 -fa on -c 4096 \
    --temp 0 --seed 1 -n 48 -p "The capital of France is" </dev/null | md5sum
  ```

  Expected MoE md5: `8cb0ce23777bf55f92f63d0292c756b0`.

- [x] **Step 4: Run serving A/B**

  Decision: not run because no production CUDA candidate was added. The existing
  profile already rejects metadata-only work: the helper buckets are too small,
  and a valid source candidate must attack `mmq_nvfp4` or `act_quant` directly
  before it earns a serving A/B run.

  Compare:

  - default env
  - `LLAMA_MOE_FUSED_DISPATCH=1`

  Same shape:

  - `n=128`
  - `ptok=128`
  - `gen=64`
  - `/v1/completions`

  Keep only if the fused path improves aggregate decode by at least `+5%` or
  produces a clear MoE dispatch/MMQ bucket reduction that predicts a larger
  serving shape win.

## Self-Review

- Spec coverage: profile gate, safety gates, source promotion, md5/op gates, and
  docs updates are covered.
- Placeholder scan: no task uses TBD/TODO/fill-in placeholders.
- Type consistency: candidate env name is consistently
  `LLAMA_MOE_FUSED_DISPATCH`; artifact root is consistently
  `/home/mudler/bench/phase8_ragged_moe_dispatch`.
