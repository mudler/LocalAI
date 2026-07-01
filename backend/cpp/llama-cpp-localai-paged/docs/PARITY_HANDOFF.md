# PARITY_HANDOFF: how to pick up the GB10 vLLM-parity work

> 2026-06-30 update: this handoff is now historical procedure, not the active
> verdict. The GB10 investigation was reopened in `GB10_PARITY_REOPEN_SPEC.md`
> and `GB10_PARITY_PHASE0_RESULTS.md`, with Phase 6 serving-nsys evidence and
> the active follow-up plans under `docs/superpowers/plans/`. Use those files for
> the current state before relying on the older "closed" conclusion below.

Audience: an agent with **zero prior context** who has been told to "continue the GB10 vLLM-parity investigation" on the `llama-cpp-localai-paged` backend.

This file is the **operational how-to**. It is the companion to `VLLM_PARITY_FINAL.md`, which is the **why / authoritative record** ("never re-litigate"). If the two ever disagree on a *fact*, `VLLM_PARITY_FINAL.md` and the bench artifacts it cites win; this file wins on *procedure* (how to ssh, lock, build, bench, profile).

Read order for a cold start:
1. This file (TL;DR + hard gates + quickstart).
2. `VLLM_PARITY_FINAL.md` (the closed record, every number cites its artifact).
3. `.agents/vllm-parity-methodology.md` (the methodology: bit-exact gating, profile-don't-assume, both-engine ground truth).
4. The patch-series `README.md` (~44 KB, canonical backend doc) and `PAGED_BITEXACT_NOTE.md`.

---

## 1. TL;DR STATE

> 2026-07-01 active update: Phase50-59 reopened the dense and MoE serving
> scheduler question.
> True dense decode is much closer to vLLM (`383.66` vs `435.00` t/s, `88.2%`)
> than the Phase47 h2h aggregate suggested, while traced serving still shows
> no pure decode-only steps and high TTFT. Phase53 rejected static lower
> admission budgets; Phase54 histograms show prompt admission concentrated in a
> few large chunks (`prompt_hist=513+:12`) with mostly full-width decode
> (`decode_hist=128-255:53`). Phase55 implemented that targeted
> first-token A/B as `LLAMA_TTFT_PREFILL_FIRST=1`: on dense `n=128` it improved
> aggregate throughput `138.2 -> 142.9`, mean TTFT `23231.9 -> 21520.8 ms`, and
> wall `59.272 -> 57.323 s`, with md5/op gates green. Phase56 then showed the
> policy helps dense `n=32` but regresses MoE `n=128` mean TTFT
> `7168.1 -> 7615.3 ms` and aggregate `341.1 -> 339.9`; keep it opt-in only and
> do not default it broadly. Phase57 tried a per-step defer cap; cap32 improved
> MoE mean TTFT in one same-window sweep but still lost aggregate and wall, and
> dense caps lost aggregate. Phase58 added a prompt-backlog threshold; min32
> improved MoE `n=128` aggregate `339.0 -> 341.9`, mean TTFT
> `7743.1 -> 7420.1 ms`, and wall `24.167 -> 23.950 s` in the same window, while
> dense `n=128` was mixed. Phase59 repeated MoE min32: aggregate stayed flat
> (`336.6 -> 336.9`), mean TTFT improved (`7798.5 -> 7167.8 ms`), and wall stayed
> flat (`24.334 -> 24.316 s`) with md5/op gates green. Matching vLLM was still
> far ahead (`601.3` aggregate, `2968.1 ms` mean TTFT), so min32 is an opt-in
> llama.cpp QoS knob, not a parity-closing lever. The trace and scheduler commits
> are local and DGX-gated but not pushed, so the LocalAI patch series has not
> been regenerated.

- Historical verdict: the older investigation marked GB10 parity **CLOSED** and
  unreachable. Treat that as superseded where Phase50-54 provide newer dense
  serving evidence.
- **Prefill** is a genuine floor at **~36% (MoE) / ~43% (dense)** of vLLM. Prefill is **not** CUDA-graph-replayed, so these numbers are real, not measurement artifacts.
- **Decode** is **near-parity: ~86% of vLLM's TRUE GPU-steady decode** (924 vs 1078 t/s). The long-standing **~56% headline was a CUDA-graph measurement artifact** (nsys without `--cuda-graph-trace=node` collapses each graph replay into one opaque launch). Decode is also **ahead of vLLM at low concurrency** (dense 116.7% at N=8) and uses **1.5-3x less memory**, bit-exact per-path.
- The lever search was **exhaustive**: every attempt (prefill GEMM, GDN chunked scan, decode fusions, serving/scheduler) is recorded with its verdict and number so it is **not re-run**.
- **The path to parity is different hardware: datacenter Blackwell** (B200, HBM, native tcgen05 / CUTLASS FP4). Do NOT reopen GB10 kernels. Re-run the methodology on the new silicon, where vLLM's GB10-losing FLA/Marlin kernels invert.

---

## 2. THE HARD GATES YOU MUST NOT VIOLATE

These are non-negotiable. Violating any of them invalidates the result or the contribution.

### 2.1 The per-path greedy-md5 bit-exact gate (sacred)
The gate is **per-path**: paged vs non-paged attention legitimately produce different (equivalent) FP-reduction orders. Each path is gated against **its own** reference, validated benign by KL-divergence to the f16 reference. Canonical greedy md5s:

| Path | Model | Canonical md5 |
|---|---|---|
| non-paged | MoE q36-35b-a3b-nvfp4 | `07db32c2bcb78d17a43ed18bc22705cd` |
| **paged** | MoE q36-35b-a3b-nvfp4 | `8cb0ce23777bf55f92f63d0292c756b0` |
| non-paged | dense q36-27b-nvfp4 | `5951a5b4d624ce891e22ab5fca9bc439` |
| paged | dense q36-27b-nvfp4 | `5951a5b4d624ce891e22ab5fca9bc439` (bit-exact to non-paged) |

- **Compare paged-to-paged only.** Future paged-MoE regressions compare to `8cb0ce23`, NOT `07db32c2`.
- **Why paged-MoE differs (benign, KL-validated):** `llama-perplexity --kl-divergence` on the MoE GGUF (16 chunks, f16 base PPL 7.3734) shows non-paged-vs-f16 KLD 0.136597 and paged-vs-f16 KLD 0.136000, i.e. paged does NOT diverge from f16 ground truth more than non-paged does. Paged and non-paged are two equivalent FP-reorderings of the same 4-bit model. This holds on the 0028 baseline and with `LLAMA_MOE_FORCE_GRAPHS`/0029 on or off, so it is a property of the paged path, not any one lever.
- **Every bit-exact patch is gated two ways:** greedy md5 (per path) AND `test-backend-ops` vs the CPU oracle for every touched op.

### 2.2 The KL-gate for opt-in lossy paths
Any path that is NOT byte-identical (e.g. 0033 dequant-bf16, the 0034/0035 large-M FP paths, FP8-KV) ships **default-off** and is gated by a **KL-divergence band**: it requires `KLD(new||f16) <= KLD(FP4-MMQ||f16)` and PPL within the established band. Lossy levers never ship default-on.

### 2.3 In-backend A/B is the only proof (hard methodology rule)
A lever compiled into the binary is **NOT** isolated by a runtime flag alone. It needs a **separately-built in-backend A/B**. Precedents that burned this in: 0031 chunking math was correct yet -22% in-backend; 0034 had a standalone PoC win that did not hold in-backend.

### 2.4 Contribution / commit gates (LocalAI policy)
- **DCO sign-off is human-only:** do not add an AI `Signed-off-by` trailer.
- **AI attribution via `Assisted-by:` trailer:** `Assisted-by: Codex:gpt-5`.
- **NEVER add `Co-Authored-By:` (AI) trailers** and never add an AI `Signed-off-by`.
- **No em-dashes** anywhere in output (use `-`, `:`, parentheses, or rephrase).
- **Ask before every `git push`.** Prior approval does not carry over.

### 2.5 Fork-first is MANDATORY (the fork is canonical)
- The **canonical source of truth is the fork branch `mudler/llama.cpp:localai-paged`** (= pin commit + paged patch commits in order). It is canonical for ALL paged-backend kernel/patch work. The shipped `patches/paged/*.patch` series is a **derivative**: the fork is the source.
- **Always update the fork FIRST, in this exact order:** (1) commit the change on the `localai-paged` branch and **push it**, then (2) regenerate the LocalAI series (`backend/cpp/llama-cpp-localai-paged/patches/paged/`) from the fork via `git format-patch` (one patch per fork commit, source-only, never touching a `*.md`/dev-doc), so the series stays a **1:1, drift-free mirror** of the branch. No hand-export.
- **NEVER edit the LocalAI `patches/paged/*.patch` files directly**, and **NEVER add a patch to the series with no corresponding fork-branch commit.** They are generated output, not source.
- The fork branch is also **where the build and the per-path bit-exact md5 gate actually run**, so it is the **only** place a change is truly validated. A patch that lives only in the LocalAI series has never been built or gated.
- **Mirror invariant (verify by tree hash):** applying the full on-disk series on the pin must reproduce the fork branch tree byte-for-byte. The series has **intentional gaps** (missing 0005, 0026, 0027, 0032, 0036-0039, 0045), so the patch count is not the max number; what must hold is the tree-hash equality, not the count. Current verified state: fork HEAD `2d590d770` is mirrored by worktree patch `0063-feat-cuda-trace-cublas-tensor-names.patch`; applying all `54` patch files on `0ed235ea2c17a19fc8238668653946721ed136fd` produces tree `dedb1182910eafe9f6875588dc8285bfb544cce5`, exactly matching the fork.

### 2.6 Bench hygiene gates
- **NEVER set `LLAMA_MAX_BATCH_TOKENS` in benches** (the harness explicitly logs "NO LLAMA_MAX_BATCH_TOKENS").
- Do **not** set `GDN_TC`, `GDN_CHUNK_MIN`, or `LLAMA_PAGED_DECODE_STABLE` in parity benches. Production defaults are compiled in: **GDN M5 on (`GDN_TC=5`, `GDN_CHUNK_MIN=64`), S1 decode-graph on, S3 off.**
- **Decode profiling MUST use `nsys --cuda-graph-trace=node`** (see section 3.4). This is a gate, not a suggestion.

---

## 3. OPERATIONAL QUICKSTART (copy-pasteable)

### 3.0 Host
```
ssh dgx.casa        # resolves to hostname promaxgb10-4ad8; GPU = NVIDIA GB10 (unified LPDDR5x, ~273 GB/s, the bandwidth floor)
```
`nvidia-smi` reports memory as `[N/A]` (unified memory). CUDA 13 / sm_121.

### 3.1 GPU lock protocol (`~/gpu_bench_lock`) - TWO conventions, reconcile carefully
There are two conventions in flight:
- **Old harnesses** (`combined_definitive.sh`, `fuse_validate.sh`, `fuse_profile.sh`) treat it as an **empty mutex dir**: `mkdir ~/gpu_bench_lock` to acquire, `rmdir` to release.
- **Newer harnesses** (`fp4norm_profile.sh`) use an **owner-file convention**: `mkdir -p ~/gpu_bench_lock` then `echo "$ME $(date +%s)" > ~/gpu_bench_lock/owner`. They poll until `nvidia-smi --query-compute-apps=pid` count is 0 AND `owner` is `FREE*`/absent for 2 consecutive checks, and clear a stale `~/gpu_bench_lock/release` file. Release **writes** `FREE released-by-... $(date +%s)` to `owner` (it does NOT remove the dir).

Because the dir now permanently contains an `owner` file, **release with `rm -rf ~/gpu_bench_lock`, NOT `rmdir`** (rmdir fails on the non-empty dir). Recommended procedure for a future agent:
1. Read `~/gpu_bench_lock/owner`. `FREE*`/absent + 0 compute-apps means free.
2. Acquire via `mkdir -p ~/gpu_bench_lock` + write `owner`.
3. Release by writing `FREE ...` to `owner` (or `rm -rf ~/gpu_bench_lock`).

A separate 0-byte `~/bench/gpu.lock` is legacy/unrelated - ignore.

**Always gate on ALL THREE** before benching or building on DGX: `nvidia-smi --query-compute-apps=pid` count == 0, `owner` FREE, and `docker ps` shows no running containers. In particular, do not start work while a `local-ai-worker` container is running. Concurrent jobs share this GPU: an offline-repack Marlin workflow, an `~/.cache/autoresearch-quant/` quant pipeline (this is the `llama-imatrix` class of job), finetune trees, and LocalAI worker containers. The canonical harnesses poll for GPU-idle up to 2h.

### 3.2 Build (long; run detached + poll)
- **Mainline / canonical grpc-server + binaries: CUDA arch `121`** (`-DCMAKE_CUDA_ARCHITECTURES=121`). Runtime banner shows `ARCHS = 1210 | BLACKWELL_NATIVE_FP4 = 1`.
- **FP4-MMA / tensor-core experimental kernels: the accelerated `121a` gencode** (`arch=compute_121a,code=[compute_121a,sm_121a]`). The `a` suffix unlocks tcgen05 / native FP4-MMA intrinsics. `121a` lives ONLY in the DGX experimental build scripts (`~/gdn_cc.sh` standalone nvcc, `~/gdn_bv_build.sh` `-DCMAKE_CUDA_ARCHITECTURES=121a`, `~/paged-build.sh` `--build-arg CUDA_DOCKER_ARCH=121a`), not in the worktree build files. Supply it at build time via `CMAKE_CUDA_ARCHITECTURES` / `CUDA_DOCKER_ARCH`.
- **Long builds: run detached and poll for a marker.** Pattern: `nohup ... > build.log 2>&1 &` then poll for a `.DONE`/`.done` file. Do NOT block a foreground shell.

Built binaries live at `dgx:~/llama-paged-dev/build-cuda/bin/` (`llama-server`, `llama-batched-bench`, `llama-completion`; thin ~70 KB dynamic wrappers).

### 3.3 The standard bench env + commands
```
cd /home/mudler/llama-paged-dev/build-cuda/bin
L="LLAMA_KV_PAGED=1 LLAMA_MOE_FORCE_GRAPHS=1 GGML_NO_BACKTRACE=1"   # GGML_NO_BACKTRACE is log-hygiene, not a lever
MOE=/home/mudler/bench/q36-35b-a3b-nvfp4.gguf       # arch qwen35moe, ~22.2 GiB
DENSE=/home/mudler/bench/q36-27b-nvfp4.gguf         # arch qwen35,    ~17.5 GiB

# (1) Bit-exact / coherence gate. stdin MUST be /dev/null or it hangs in conv mode.
env $L ./llama-completion -m "$MOE" -ngl 99 -fa on -c 4096 --temp 0 --seed 1 -n 48 -no-cnv \
    -p "The capital of France is" </dev/null | md5sum
# The PAGED_BITEXACT_NOTE gate command uses the chat-template path (NO -no-cnv):
#   ./llama-completion -m MODEL -ngl 99 -fa on -p "The capital of France is" -n 48 --temp 0 --seed 1
# (compare to the canonical md5 for that model+path; paged-to-paged only)

# (2) PREFILL bench (S_PP from llama-batched-bench)
env $L ./llama-batched-bench -m "$MOE" -c 131072 -b 2048 -ub 512 -ngl 99 -fa on \
    -npp 512,2048 -ntg 4 -npl 32

# (3) SERVING bench: one --parallel 256 server, then drive with h2h_cli3.py
env $L nohup ./llama-server -m "$MOE" -c 262144 --parallel 256 -b 2048 -ub 512 \
    -ngl 99 -fa on --host 127.0.0.1 --port 8090 --no-webui >/home/mudler/bench/paged_server.log 2>&1 &
# poll http://127.0.0.1:8090/health for '"ok"', then:
python3 /home/mudler/bench/h2h_cli3.py   # OpenAI /v1/completions, ignore_eos, fresh-nonce, ptok128 gen128, NPL sweep 8/32/128/256
```
**vLLM side** (for both-engine parity): `~/vllm-bench/bin/vllm` (version **0.23.0**), served `gpu-util 0.85 max-model-len 4096 max-num-seqs 256 tp1`, models `~/bench/q36-35b-a3b-nvfp4-vllm/` and `~/bench/q36-27b-nvfp4-vllm/`.

**Current-stack serving snapshots use `backend/cpp/llama-cpp-localai-paged/paged-current-serving-snapshot.sh`.** It targets the clean `~/llama-phase6-source` mirror, checks docker/`local-ai-worker`/GPU-idle state, uses the owner-file lock, runs pre/post inference gates, then compares paged and vLLM with the same h2h client. The older `dgx:~/bench/combined_definitive.sh` is historical: do not reuse it without first porting away from stale `~/llama-paged-dev` paths and old lock assumptions.
The harness also writes `hardware.txt` before any server starts, including
`DRY_RUN=1`, so every new snapshot records the GPU model, driver, compute
capability when exposed by `nvidia-smi`, and a conservative hardware class.
Full runs also write `gate_summary.tsv` after the post gate, summarizing pre/post
MoE md5, dense md5, and backend-op checks; use
`paged-current-serving-snapshot.sh --summarize-gates ART` to backfill or audit an
existing snapshot without starting servers.

### 3.4 THE DECODE-PROFILING RULE (this trap caused 4 wrong analyses)
Decode runs as a **replayed CUDA graph**. `nsys` **without** `--cuda-graph-trace=node` collapses each graph replay into ONE opaque launch, so every per-kernel attribution becomes an artifact. This is exactly what made the old "paged 159 us/tok, GPU ~16% busy, host-bound, 5.4x more GPU-efficient" story wrong, and produced the wrong ~56% headline.

Mandatory method for any decode profile:
- Use **`nsys --cuda-graph-trace=node`**.
- Decompose with the **difference method**: per-token cost = (ntg=64 profile) - (ntg=16 profile).

Under the correct method, paged decode at npl=256 is **99% GPU-busy (1.4% idle), NOT host-bound** - the opposite of the collapsed-graph reading. The clean graph-node-traced profiles are at `~/highN_prof2/*.nsys-rep` (paged, npl=256) and `~/highN_vllm/*.nsys-rep` (vLLM), captured 2026-06-30. They **supersede every earlier decode decomposition.**

### 3.5 Models + artifacts (all on DGX)
GGUF (paged): `~/bench/q36-35b-a3b-nvfp4.gguf` (MoE, qwen35moe), `~/bench/q36-27b-nvfp4.gguf` (dense, qwen35). vLLM safetensors: `~/bench/q36-35b-a3b-nvfp4-vllm/` (has `hf_quant_config.json` confirming MIXED_PRECISION / FP8-proj), `~/bench/q36-27b-nvfp4-vllm/`.
Authoritative run: `~/bench/COMBINED_DEFINITIVE.txt` (+ `.log`, `.done`, `combined_definitive.sh`, per-engine `COMBINED_*_server.log`). A/B dirs: `~/bench/marlin_gate/`, `~/bench/gdn_p1_ab/`. NOTE: the `*_RESULTS*`/`*_MAP*` docs live only in the worktree `docs/`, not on the DGX.

---

## 4. THE COMPLETE LEVER MAP (do NOT re-run the rejected ones)

Verdicts and numbers are from `VLLM_PARITY_FINAL.md` + the cited artifacts. "BE" = greedy-md5 bit-exact; "KL-benign" = lossy path inside the KL band.

### 4.1 Prefill weight-GEMM track - WHOLE TRACK REJECTED (FP4-MMQ is optimal on GB10)
Decisive surprise: on sm_121 **vLLM itself does NOT run native FP4** - it runs **Marlin W4A16** (FP4 dequant->bf16 in-register + bf16 GEMM) for experts and FP8 projections, capped at ~half FP4 peak, because native CUTLASS NVFP4 grouped-GEMM is broken on consumer Blackwell (TMA-WS init failure, CUTLASS #3096; no tcgen05/TMEM). So MMQ's native FP4 is already structurally competitive here.

| Lever | What | Verdict | Key number |
|---|---|---|---|
| 0033 dequant->bf16 cuBLAS | route large-M NVFP4 dense GEMM to dequant->bf16 cuBLAS | REJECTED, ships default-off | dense S_PP -49%/-42%/-29% at M=512/1024/2048; BE + KL-better |
| dense-cuBLAS reroute (full) | same across dense+MoE prefill | REJECTED | -31% to -62% band |
| 0034 native FP4-MMA W4A4 | Blackwell `mxf4nvf4` OMMA large-M | REJECTED in-backend | PoC 103 TFLOP/s (57.7% FP4 peak, NMSE=0) but win did not hold in-backend |
| 0035 W4A16-Marlin grouped MoE | FP4->bf16 in-register + bf16 mma, zero act-quant tax | REJECTED (perf) | correct + KL-benign-and-better but **-39%** S_PP vs MMQ |
| 0045/0046 offline-repack / vLLM-verbatim Marlin | repack to Marlin layout; port vLLM kernel verbatim | REJECTED | verbatim correct but -39%; offline-repack same bf16-peak ceiling, no win |

Why it loses: bf16 TC peak on GB10 is ~half FP4 peak, so any dequant->bf16 kernel caps at ~half FP4-MMQ; the dequant write is an un-amortized weight-sized memory pass (~8x the FP4-read traffic). **The GEMM bucket is not winnable on GB10 with available kernels.**

### 4.2 Prefill GDN chunked-scan track - M5 tf32 C=16 is the SHIPPED winner
GDN is the #1 prefill-gap contributor (+59.2 us/tok, ~30%). vLLM's FLA `chunk_gated_delta_rule` runs the same math at 36.5 vs paged 95.7 us/tok = 2.62x via tensor-core intra-chunk Gram products.

| Lever | What | Verdict | Key number |
|---|---|---|---|
| 0031 scalar-serial chunked scan | FLA-style scalar/serial (`GDN_TC=0`) | superseded | correct but ~22% slower at forced C=16 |
| **0047 / M5 tf32 tensor-core scan** | tf32 `m16n8k8` mma form-T solve, f32-only | **SHIPPED default-on under paged** | MoE prefill +3.5% @npp512, +17.7% @npp2048; decode unchanged; BE-benign |
| bf16 CONFIG-C (M8) | bf16 Kc/Qc + 2 C*C scratch, C->64 | REJECTED (not in f32 series) | confirmed geometry then dropped |
| bf16-C16 | bf16 Gram at C=16 | REJECTED | no win; bf16 mantissa unsafe on state-coupled products |
| BV block-occupancy A/B (tf32) | raise blocks/SM | REJECTED (occupancy NOT the bound) | 1844 vs 1814 S_PP (-1.04%, within noise) |
| bf16-C64 | bf16 Gram at C=64 | REJECTED | -18.75%; O(C^2) intra-chunk + serial recurrence dominates |
| Phase 10 C32 slab M5 | C=32, two `dv_tile=64` slabs, default-off `GDN_C32_SLAB=1` | REJECTED | md5-clean after tail-row zeroing, but slower: MoE 2048 2430.32 -> 2054.86; dense 2048 1019.25 -> 903.73 |
| Phase 11 QS-early M5 | move `QS = Qc * S0` earlier, default-off `GDN_M5_QS_EARLY=1` | REJECTED | md5-clean, but slightly slower: MoE 2048 2441.54 -> 2420.26; dense 2048 1021.06 -> 1015.77 |
| Phase 12 shared-A/Ai cost model | f32 Ai scratch shared across two C32 value slabs | GO to one default-off prototype | BT32 f32 scratch at npp2048,npl32: MoE 256 MiB / 768 MiB Ai traffic; dense 384 MiB / 1152 MiB Ai traffic |
| Phase 13 Global-Ai32 | precompute f32 Ai once, consume from two C32 `dv_tile=64` slabs | REJECTED | md5-clean, but slower: MoE 2048 2425.10 -> 2097.76; dense 2048 1016.14 -> 918.19 |

Why not occupancy/dtype: the cost is the **O(C^2) intra-chunk triangular A-inverse solve + the strictly-serial inter-chunk recurrence**, with C forced to **16** by GB10's 99 KB dynamic-smem cap (the 128x128 f32 state alone is 64 KB). M5 captures the tractable TC part; it does not fully close 2.62x because vLLM's FLA blocked-solve is a more complete TC implementation.

Phase 13 closes the caveat: the default-off `GDN_GLOBAL_AI32=1` prototype was
correctness-clean but slower. Stop GDN kernel work on GB10 instead of iterating
into f16 Ai or more local reorders.

### 4.3 Decode / fusion levers - all REJECTED (near-parity already at ~86% true GPU-steady)
| Lever | What | Verdict | Key number |
|---|---|---|---|
| act-quant folded into ggml MMQ | inline y-quant in MoE expert MMQ | REJECTED | **-79.4%**; ggml MMQ re-quantizes y per weight-row-tile x stream-k split, no TC for inline quant |
| norm+quant+silu fusion | one launch (vLLM Triton kernel) | REJECTED (infeasible) | `ggml_cuda_can_fuse` cannot express it: FP4 quant is a mul_mat-internal prologue, silu separated from norm by 2 GEMMs + router |
| Q8_0 / FP8 projection | quantize bf16 GDN/attn projections | REJECTED (regime error) | vLLM DOES use FP8 proj, but at N>=128 proj is only ~12% of stream, closes <=6% |
| NVFP4 the projections | drop proj to NVFP4 | REJECTED | KL-fail, ~+6% PPL; vLLM keeps SAME bf16/FP8 proj, never NVFP4 |
| W4A16-Marlin MoE decode | Marlin grouped expert GEMM at decode | REJECTED | BW-floored wash, ~5% slower |
| bf16-tau per-head SSM (0026) | per-head bf16 tau on SSM decode | DROPPED | flat 780.6 vs 780.0 t/s; earlier "+12%" subsumed by 0028/0029 |
| D3 FA-split / D4 GDN-width-adaptive | older off-critical-path levers | SUPERSEDED reasoning | were rejected via the debunked "5.4x/host-bound" reading; under HNP the GDN scan IS critical path (51%) but is the shared BW floor where paged leads (83% vs 79%), so still not a win |

Dense decode is **AHEAD at low N (116.7% @ N=8)** - the one operating point where paged is unambiguously faster.

### 4.4 Serving / engine levers - host loop and scheduler CLOSED
| Lever | What | Verdict | Key number |
|---|---|---|---|
| **0040 / S1** paged decode-graph reuse | `can_reuse` keyed on bucketed block-table dims | SHIPPED default-on | serving reuse 0% -> 72.2% (with S3); static 0% -> 95.5% |
| **0041 / S3** decode-shape-stable scheduling (`LLAMA_PAGED_DECODE_STABLE`) | keep prefill out of decode steps | SHIPPED **default-OFF** (opt-in) | recovers the ~17 pt graph-reuse overhead at a TTFT cost; default-on regressed real serving (2.5x worse TTFT, 20-29% lower e2e throughput) |
| **0043 / D1** full-step MoE decode CUDA graph | graph whole decode step incl. grouped-MMQ MoE dispatch | SHIPPED default-on | +2.6% (npl128) to +5-13% (npl32); D1 premise "host-sync on MoE readback" REFUTED (sync count identical 1457 on/off) |
| S2 double-buffer set_inputs | overlap host input build with GPU | DROPPED | `set_inputs` ~0.05 ms/step, nothing to recover |
| whole-step graph / host loop | host loop as serving residual | CLOSED (~0-1%) | reuse 0% (757.6) == S1+S3 72% (763.3); hostproc only ~4-8% of step wall |
| padded / fixed-slot decode | pad decode width to `--parallel` for ~100% reuse | **REJECTED (built, GPU-tested, commit b028c81e)** | inert (BE) but regresses everywhere; N=8 burst 28.16->6.05 tok/s/seq; serving decode is GPU-compute-bound, dummy-row compute > reuse recovered |
| speculative decode (MTP) | draft + verify | **REJECTED for current GB10 serving** | Phase 14 safety passed, but Phase 15 serving A/B regressed hard: n128 decode agg 662.4 -> 138.5 tok/s; likely graph/batch-shape disruption (`graphs reused` 361 -> 1) |

### 4.5 SHIPPED WINS (all BE / KL-benign) - keep these, do not regress
- **FP4-MMQ MoE/dense GEMM** (native Blackwell FP4-MMA at the FP4 weight-BW floor; reason 4.1 stays default-off).
- **M5 tf32 tensor-core chunked GDN prefill (patch 0047)**, default-on under `LLAMA_KV_PAGED` (`GDN_TC=5` + `GDN_CHUNK_MIN=64`).
- **0042 fused residual-add + RMSNorm + weight-mul** (dense S_PP +0.5%, BE).
- **0044 fused GatedRMSNorm + SiLU gate-mul** (672 -> 336 launches @npp512; dense +1.1%, MoE +0.9%, test-backend-ops 12979/12979).
- **0046 GDN-prefill geometry gate** (gates 0022's decode retune by scan length; recovers +7.2% dense prefill, keeps the decode win, BE).
- **SSM decode-fusion stack (0018-0022, 0028)**: in-place state (+23.5%/+18.9%), fused gather (+37.8%/+35.3%), o_proj reshape (+31.7%/+23.3%), conv in-place (+3.2%/+3.5%), occupancy retune (+11.1%/+8.3%) = the **2.26x / 2.46x over stock** decode multiplier.
- **Serving host loop closed (0040 S1, 0043 D1).**
- **The memory advantage** (1.5-3x lower VRAM, NVFP4-resident, no persistent bf16 dequant copies).
- **Low-N decode lead** (dense 116.7% @ N=8). **Bit-exact output per-path** through the whole series.

### 4.6 REMAINING / unattempted levers + EV
- **Multi-week persistent-Marlin decode kernel** (vLLM's fused-Marlin MoE persistent-tiling + Triton elementwise): the only path to the residual ~14 pt GPU-steady decode gap. **Low-EV**: decode-only ~4-14%, our own ggml Marlin port already lost -19.6%, needs mature tiling + multi-stream overlap (hard inside a single-stream CUDA graph), GB10-uncertain, and **cannot lift the prefill floor**. Not a free bit-exact lever.
- **Datacenter-Blackwell pivot** (B200, ~8 TB/s HBM, native tcgen05/CUTLASS FP4, TMEM): lifts the LPDDR5x GDN bandwidth floor ~30x and restores exactly the vLLM advantages that lose on GB10. **This is the documented path to parity.** Re-run the methodology on the new silicon, do not reopen GB10 levers.

The `VLLM_PARITY_LEVER_MAP.md` "pursue list" (A1-A7/B1-B7/C1: graph-safe ragged grouped FP4-MMA MoE kernel, FP8 paged KV, MTP spec-decode, etc.) is the **earlier working brainstorm written before the final profiling**. `VLLM_PARITY_FINAL.md` is the authoritative supersession; treat those buckets as rejected / infeasible / different-hardware unless re-validated on new silicon.

Phase 14 re-validated the MTP bucket as safe, then Phase 15 rejected it as a
current GB10 serving-throughput lever. Do not enable it by default and do not
keep tuning draft length blindly. The only plausible follow-up is a graph-reuse
and speculative verification batch-shape profile with
`nsys --cuda-graph-trace=node`. Phase 16 ran that profile and supported the
root cause: small-shape baseline reused graphs (`graphs reused = 62`) while MTP
did not (`graphs reused = 1`) and did ~2.3x more GPU kernel work. The fixed
safety gates stayed green before and after the failed serving A/B: MoE md5
`8cb0ce23777bf55f92f63d0292c756b0`, dense md5
`5951a5b4d624ce891e22ab5fca9bc439`, and `MUL_MAT_ID` `806/806`.

Phase 17 source inspection found no tiny additive graph-reuse fix. MTP
verification rows are real target decode/output rows (`K + 1` per speculative
slot), so fake padding would touch KV, positions, logits, MTP nextn state, and
rollback semantics. If reopened, start with a server-only shape counter around
`server_slot::handle_last_sampled_token()`. Only then consider an opt-in
group/defer-by-draft-length scheduler experiment, with TTFT/throughput and
md5/op gates as kill criteria.

Phase 18 added the server-only shape trace as patch 0055. Set
`LLAMA_SPEC_SHAPE_TRACE=1` to log `kind=decode` rows and MTP `kind=verify`
`K + 1` row/output shapes from `server_slot::handle_last_sampled_token()`.
This is default-off instrumentation only. DGX green check after the patch saw
MTP verify shapes vary (`rows=4`, then `rows=3`) on a tiny request, while the
env-unset run emitted no `spec shape:` lines. Canonical post-patch gates passed:
MoE `8cb0ce23777bf55f92f63d0292c756b0`, dense
`5951a5b4d624ce891e22ab5fca9bc439`, and `MUL_MAT_ID` `806/806`.
Artifacts:
`/home/mudler/bench/phase18_mtp_shape_trace_green` and
`/home/mudler/bench/phase18_mtp_shape_trace_green/gate_after`.

Next MTP step, if any: trace real serving shape entropy first. Do not implement
a scheduler change until the trace shows repeatable draft-length buckets worth
grouping. Any scheduler experiment must be opt-in/default-off and killed by
TTFT/throughput regression, graph-reuse failure, md5/op drift, or MTP
rollback/prefix gate failure.

Phase 19 ran that trace-only serving measurement and rejected the scheduler
shortcut. Artifact:
`/home/mudler/bench/phase19_mtp_shape_entropy/20260701_045534`. Pre/post gates
passed with canonical MoE md5 `8cb0ce23777bf55f92f63d0292c756b0`, dense md5
`5951a5b4d624ce891e22ab5fca9bc439`, and `MUL_MAT_ID` `806/806`.

Serving result:

| n | baseline decode_agg | MTP decode_agg | MTP / baseline | baseline TTFT ms | MTP TTFT ms |
|---|---------------------|----------------|----------------|------------------|-------------|
| 8 | 245.0 | 95.7 | 39.1% | 1147.2 | 1633.4 |
| 32 | 409.2 | 110.0 | 26.9% | 2710.0 | 4471.5 |
| 128 | 697.2 | 154.0 | 22.1% | 7601.5 | 20310.4 |

Shape result: `draft=3` already accounts for 96.2-96.9% of verify slots, so
group/defer-by-draft has little to recover. Full in-flight steps already mostly
use all-`draft=3` vectors; the remaining churn is active-slot/tail churn plus
the real `K + 1` verification-row expansion. Do not build a Phase 20 scheduler
experiment on this evidence. Future MTP work would need a deeper target-verify
graph/state design, not another small server scheduling shortcut.

Phase 62 ran that gated verify-cost recheck. Artifact:
`/home/mudler/bench/phase62_mtp_verify_cost/20260701_134125`. Pre/post gates
passed with canonical MoE md5 `8cb0ce23777bf55f92f63d0292c756b0`, dense md5
`5951a5b4d624ce891e22ab5fca9bc439`, and `MUL_MAT_ID` `806/806`. MTP acceptance
was high (`7372/9340 = 0.789`, mean acceptance length `3.33`), but throughput
remained far below the keep threshold: `0.420x`, `0.274x`, and `0.213x`
baseline decode at n8/n32/n128. Shape trace again showed `draft=3` / `rows=4`
dominance (`95.6%`), with `graphs reused = 1`. Keep current MTP rejected unless
a later target-verify/output-row graph-cost design exists; do not tune
`spec-draft-n-max` blindly.

Phase 20 refreshed the current-stack MoE serving snapshot against vLLM using the
clean `~/llama-phase6-source` mirror (`f2521ab12`) rather than the stale
`llama-paged-dev` benchmark tree. Artifact:
`/home/mudler/bench/phase20_current_snapshot/20260701_050621`. Pre/post gates
passed with canonical MoE md5 `8cb0ce23777bf55f92f63d0292c756b0`, dense md5
`5951a5b4d624ce891e22ab5fca9bc439`, and `MUL_MAT_ID` `806/806`.

Current MoE serving snapshot (`PTOK=128`, `GEN=64`):

| n | paged decode_agg | vLLM decode_agg | paged/vLLM decode | paged agg | vLLM agg | paged/vLLM agg |
|---|------------------|-----------------|-------------------|-----------|----------|----------------|
| 8 | 220.8 | 290.5 | 76.0% | 164.8 | 245.5 | 67.1% |
| 32 | 411.1 | 594.7 | 69.1% | 252.1 | 456.0 | 55.3% |
| 128 | 670.0 | 1022.7 | 65.5% | 322.4 | 662.4 | 48.7% |

TTFT remains the clearest user-visible gap: paged is 2.88x/3.36x/3.11x slower
than vLLM at n8/n32/n128, and paged prefill_tps is roughly one-third of vLLM.
This keeps the GB10 shortcut closure intact: do not reopen MTP or small
scheduler work. The credible next parity path is a datacenter-Blackwell rerun or
a larger fused-kernel project outside this low-conflict patch stack.

Phase 21 added a reusable current-stack serving harness:
`backend/cpp/llama-cpp-localai-paged/paged-current-serving-snapshot.sh`.
It defaults to `~/llama-phase6-source`, validates docker/`local-ai-worker`/GPU
idle state, uses the owner-file lock, runs pre/post inference gates, compares
paged and vLLM with h2h, and writes ratio summaries. DGX dry run passed at
`/home/mudler/bench/phase21_harness_dryrun/20260701_051757`.

Use this harness for future current-stack GB10 snapshots. Do not reuse
`~/bench/combined_definitive.sh` unless it is first ported away from stale
`~/llama-paged-dev` paths and old lock assumptions.

Phase 31 re-verified the patch-series mirror invariant after patch `0057`:
applying every LocalAI `patches/paged/0*.patch` with strict `git apply` on top of
Makefile pin `0ed235ea2c17a19fc8238668653946721ed136fd` produced tree
`4eae628e4ba6f2defa14a19d19f7e4abef9a2647`, exactly matching fork branch
`localai-paged` HEAD `c78e537b5 feat(cuda): trace moe mmq launch shapes`.

Phase 24 extended `paged-current-serving-snapshot.sh` to write the snapshot
hardware report. DGX dry run passed at
`/home/mudler/bench/phase24_hardware_report_dryrun/20260701_052741`; it recorded
`GPU 0: NVIDIA GB10`, driver `580.159.03`, compute capability `12.1`, and
`hardware_class=gb10_or_workstation_blackwell`. This makes future parity
artifacts self-describing: GB10/workstation Blackwell results must not be used
as datacenter-Blackwell parity evidence.

Phase 25 extended the same harness to write `gate_summary.tsv`. The summary was
backfilled on the Phase 20 artifact at
`/home/mudler/bench/phase20_current_snapshot/20260701_050621/gate_summary.tsv`;
it records pre/post MoE md5 `8cb0ce23777bf55f92f63d0292c756b0`, dense md5
`5951a5b4d624ce891e22ab5fca9bc439`, and `MUL_MAT_ID` `806/806` as `ok`.

Phase 26 ran the full audited current-stack snapshot with `hardware.txt`,
pre/post gates, same-session paged and vLLM serving runs, `summary.tsv`, and
`gate_summary.tsv`. Artifact:
`/home/mudler/bench/phase26_audited_snapshot/20260701_053650`. Hardware was
recorded as `hardware_class=gb10_or_workstation_blackwell`, GPU `NVIDIA GB10`,
driver `580.159.03`, compute capability `12.1`. Every compact gate row was
`ok`: MoE md5 `8cb0ce23777bf55f92f63d0292c756b0`, dense md5
`5951a5b4d624ce891e22ab5fca9bc439`, and `MUL_MAT_ID` `806/806`, both before and
after the serving run.

Audited current MoE serving snapshot (`PTOK=128`, `GEN=64`):

| n | paged decode_agg | vLLM decode_agg | paged/vLLM decode | paged agg | vLLM agg | paged/vLLM agg |
|---|------------------|-----------------|-------------------|-----------|----------|----------------|
| 8 | 230.8 | 283.2 | 81.5% | 170.6 | 241.6 | 70.6% |
| 32 | 420.0 | 609.0 | 69.0% | 254.6 | 466.7 | 54.6% |
| 128 | 673.4 | 1025.0 | 65.7% | 324.0 | 656.5 | 49.4% |

Use Phase 26 as the current audit-grade GB10 snapshot. It keeps the Phase 20
verdict intact, but the artifact is more useful for future regressions because
it carries hardware classification and compact pre/post inference gates.

Phase 27 re-profiled the current clean llama.cpp n128 serving path with
`nsys --cuda-graph-trace=node`. Artifact:
`/home/mudler/bench/phase27_graph_node_serving/20260701_055519`. The run matched
Phase 26 throughput closely (`675.5` vs `673.4` decode_agg_tps) and kept gates
green before and after the profile (post retry): MoE md5
`8cb0ce23777bf55f92f63d0292c756b0`, dense md5
`5951a5b4d624ce891e22ab5fca9bc439`, `MUL_MAT_ID` `806/806`. The node-traced
buckets still put the work in `gdn_core` (`29.59%`) and `mmq_nvfp4` (`28.44%`);
helper dispatch remains too small (`mm_ids` `0.61%`, `gather_mmq` `0.37%`,
`argsort_topk` `0.40%`). Do not reopen metadata/helper-only MoE dispatch work on
GB10.

Phase 28 tested the remaining low-conflict NVFP4 grouped-MMQ occupancy knobs.
Artifact: `/home/mudler/bench/phase28_mmq_occupancy/20260701_040450`.
`GGML_CUDA_FP4_MINBLOCKS=2` passed md5/op gates before and after serving
(MoE `8cb0ce23777bf55f92f63d0292c756b0`, dense
`5951a5b4d624ce891e22ab5fca9bc439`, `MUL_MAT_ID` `806/806`) but regressed
n128 same-session decode serving (`705.1 -> 689.9` decode_agg_tps, `0.9784x`).
`GGML_CUDA_FP4_MMQ_Y=64` failed to compile because the NVFP4 writeback
specialization asserts `nwarps*tile_C::I == mmq_y`. Do not promote either knob;
future grouped-MMQ work must be structural kernel work.

Phase 29 added the default-off grouped-MMQ shape trace as patch `0056`.
Artifact: `/home/mudler/bench/phase29_mmq_shape_trace/20260701_042428`.
Fork commit: `20a99518a feat(cuda): trace moe mmq batch shapes`. The helper was
added test-first (`test-cuda-mmq-shape-trace`) and built under CUDA on DGX.
Default-off and `LLAMA_MOE_MMQ_SHAPE_TRACE=4` gates both passed: MoE
`8cb0ce23777bf55f92f63d0292c756b0`, dense
`5951a5b4d624ce891e22ab5fca9bc439`, `MUL_MAT_ID` `806/806`. The trace-enabled
gate emitted exactly four `[LLAMA_MOE_MMQ_SHAPE]` lines. This is evidence-only
instrumentation; it does not close the speed gap.

Phase 30 used patch `0056` for a live n128 serving shape trace. Artifact:
`/home/mudler/bench/phase30_mmq_shape_serving/20260701_043300`. The first 4096
grouped-MMQ calls split into 1200 decode-like calls (`ncols_max <= 128`) and
2896 prefill-like calls. Decode-like calls had density `1-4` and selected
`mmq_x_best` only in `{32,40,48,64}`; prefill-like calls were mostly density
`16` and selected `mmq_x_best=128`. All traced calls had `stream_k=1`. Post-run
gates stayed green: MoE `8cb0ce23777bf55f92f63d0292c756b0`, dense
`5951a5b4d624ce891e22ab5fca9bc439`, `MUL_MAT_ID` `806/806`.

Phase 31 added patch `0057` for default-off grouped-MMQ launch tracing.
Artifact: `/home/mudler/bench/phase31_mmq_launch_trace/20260701_064424`.
Fork commit: `c78e537b5 feat(cuda): trace moe mmq launch shapes`; DGX mirror
commit: `8b75905e9`. The trace adds `[LLAMA_MOE_MMQ_LAUNCH]` lines under
`LLAMA_MOE_MMQ_SHAPE_TRACE=<n>`, recording `ntiles_dst`, `stream_k_blocks`,
tile efficiency, `fixup`, `ntx/nty/ntzw`, and compiled `mmq_x/mmq_y`. Default
off, trace-enabled, and post-serving gates stayed green: MoE
`8cb0ce23777bf55f92f63d0292c756b0`, dense
`5951a5b4d624ce891e22ab5fca9bc439`, `MUL_MAT_ID` `806/806`. The n128 serving
trace showed decode-like `4800/4800` and prefill-like `4920/4920` launch lines
with `fixup=0` and `stream_k_blocks == ntiles_dst`. Do not pursue a
no-fixup/no-stream-k shortcut for this workload; the remaining grouped-MMQ work
is structural small-M kernel work.

Phase 32 added patch `0058` for default-off small-M grouped-MMQ candidate
tracing. Artifact: `/home/mudler/bench/phase32_small_m_classifier/20260701_070127`.
Fork commit: `2a9964d29 feat(cuda): trace moe small-m mmq candidates`; DGX
mirror commit: `024f494d0`. The trace adds `[LLAMA_MOE_MMQ_SMALL_M]` lines
under `LLAMA_MOE_MMQ_SMALL_M_TRACE=<n>` for decode-like low-density grouped-MMQ
MoE calls (`ncols_max <= 128`, density `<=4`, `mmq_x_best <=64`). Default-off,
trace-enabled, and post-serving gates stayed green: MoE
`8cb0ce23777bf55f92f63d0292c756b0`, dense
`5951a5b4d624ce891e22ab5fca9bc439`, `MUL_MAT_ID` `806/806`. The n128 serving
trace found 4096 candidate calls, mostly `mmq_x_best=64` (1800) and `48`
(1096). Phase 33 should A/B a default-off small-M tile policy starting at
`mmq_x=16`.

Phase 33 added patch `0059`, default-off `LLAMA_MOE_SMALL_M_TILE=<n>`, and
rejected the simple smaller-tile policy. Artifact:
`/home/mudler/bench/phase33_small_m_tile_policy/20260701_071136`. Fork commit:
`fbed2abaa feat(cuda): gate moe small-m mmq tile policy`; DGX mirror commit:
`dfd1eaea8`. Default-off, tile16, tile8, and post-serving gates stayed green:
MoE `8cb0ce23777bf55f92f63d0292c756b0`, dense
`5951a5b4d624ce891e22ab5fca9bc439`, `MUL_MAT_ID` `806/806`. Same-session n128
serving rejected both caps: baseline `672.1` decode_agg_tps, tile16 `640.3`
(`0.953x`), tile8 `583.2` (`0.868x`). Do not promote smaller `mmq_x` caps.

Phase 34 added patch `0060`, default-off `LLAMA_MOE_MMID_ROUTE_TRACE=<n>`, to
classify the live `MUL_MAT_ID` dispatch route without changing route behavior.
Artifact: `/home/mudler/bench/phase34_mmid_route_trace/20260701_072737`. Fork
commit: `6c332094c feat(cuda): trace moe mmid routes`; DGX mirror commit:
`34a256d14`. Default-off, trace-enabled, and post-serving gates stayed green:
MoE `8cb0ce23777bf55f92f63d0292c756b0`, dense
`5951a5b4d624ce891e22ab5fca9bc439`, `MUL_MAT_ID` `806/806`. Live n128 serving
with trace cap 4096 found `mmq=2776`, `mmvq=1320`, and `host_sync=0/4096`.
Treat the old current-stack host-sync-fallback concern as refuted for this
workload; the remaining MoE work is grouped-MMQ small-M efficiency or another
measured bucket.

Phase 35 added patch `0061`, default-off `LLAMA_MUL_MAT_ROUTE_TRACE=<n>`, to
classify regular `MUL_MAT` routes for the projection-heavy serving bucket.
Artifact: `/home/mudler/bench/phase35_mul_mat_route_trace/20260701_074359`.
Fork commit: `486c28c63 feat(cuda): trace mul mat routes`; DGX mirror commit:
`18f7ad005`. Default-off, trace-enabled, and post-serving gates stayed green:
MoE `8cb0ce23777bf55f92f63d0292c756b0`, dense
`5951a5b4d624ce891e22ab5fca9bc439`, `MUL_MAT` `1146/1146`, `MUL_MAT_ID`
`806/806`. Live n128 serving with trace cap 8192 found `mat_f=2888`,
`op_cublas=2292`, `mmq=1328`, `vec_q=1214`, `vec_f=470`; BF16 (`type=30`)
was split `mat_f=2485`, `op_cublas=1330`. Next projection work should target
BF16 `mat_f`/`op_cublas` subroute evidence or route policy, not batched cuBLAS.

Phase 36 added patch `0062`, default-off `LLAMA_CUBLAS_ROUTE_TRACE=<n>`, to
classify the generic cuBLAS `MUL_MAT` subroute without changing branch behavior.
Artifact: `/home/mudler/bench/phase36_cublas_route_trace/20260701_081228`.
Fork commit: `38c4ef2e4 feat(cuda): trace cublas routes`; DGX mirror commit:
`e0224393a`. Default-off, trace-enabled, and post-serving gates stayed green:
MoE `8cb0ce23777bf55f92f63d0292c756b0`, dense
`5951a5b4d624ce891e22ab5fca9bc439`, `MUL_MAT` `1146/1146`, `MUL_MAT_ID`
`806/806`. Live n128 serving with trace cap 8192 found `bf16_tc=5681` and
`sgemm=2511`. The next projection phase should explain whether the F32 SGEMM
shapes are expected glue tensors or a missed BF16 route; do not chase NVFP4
cuBLAS or batched cuBLAS for this measured bucket.

Phase 37 added patch `0063`, extending `LLAMA_CUBLAS_ROUTE_TRACE=<n>` with
`src0`, `src1`, and `dst` tensor names. Artifact:
`/home/mudler/bench/phase37_cublas_name_trace/20260701_083227`. Fork commit:
`2d590d770 feat(cuda): trace cublas tensor names`; DGX mirror commit:
`2cbb61969`. Default-off, trace-enabled, and post-serving gates stayed green:
MoE `8cb0ce23777bf55f92f63d0292c756b0`, dense
`5951a5b4d624ce891e22ab5fca9bc439`, `MUL_MAT` `1146/1146`, `MUL_MAT_ID`
`806/806`. Live n128 trace found `bf16_tc=2884`, `sgemm=1212`. The `sgemm`
bucket is `blk.N.ffn_gate_inp.weight -> ffn_moe_logits-N` and
`blk.N.ffn_gate_inp_shexp.weight -> shared_expert_gate-N`; do not force BF16
without first inspecting model-load tensor types and running KL validation.

Phase 38 is the current gate-projection policy checkpoint. Artifact:
`/home/mudler/bench/phase38_gate_baseline/20260701_084410`. Preflight showed
docker `0`, `local-ai-worker` `0`, compute apps `0`, and GB10 driver
`580.159.03`. Fresh baseline gates against the Phase37 build passed: MoE
`8cb0ce23777bf55f92f63d0292c756b0`, dense
`5951a5b4d624ce891e22ab5fca9bc439`, `MUL_MAT` `1146/1146`, `MUL_MAT_ID`
`806/806`. Source comparison found llama.cpp and vLLM both keep router and
shared-expert gate weights unquantized; vLLM's relevant idea is fused F32 gate
weight concatenation, not BF16/NVFP4 routing. Future fused-gate work must be
default-off, preserve F32 semantics, and pass md5/op gates before benchmarking;
if md5 changes, run KL first.

Phase 39 closes the naive fused-gate shortcut. Artifact:
`/home/mudler/bench/phase39_gate_sgemm_profile/phase27_reanalysis`. Re-analysis
of the Phase27 graph-node serving profile showed total kernel time `20.0372s`,
`concat_layout=459.84ms` (`2.29%`, `2250` instances), `cublas_bf16_gemm=1892.81ms`
(`9.45%`), and `cutlass_bf16_gemm=684.01ms` (`3.41%`). Do not implement
graph-time `ggml_concat()` of `ffn_gate_inp.weight` plus
`ffn_gate_inp_shexp.weight`; it risks increasing an existing layout-copy bucket.
The only future fused-gate design worth scoping is a persistent/load-time F32
combined gate weight with output views, default-off until MoE/dense md5,
`MUL_MAT`, `MUL_MAT_ID`, and KL-if-md5-changes gates pass.

Phase 40 closes the tested GB10 max-concurrency C1 shortcut. Artifact:
`/home/mudler/bench/phase40_max_concurrency/20260701_090012`. The snapshot ran
with `PARALLEL=256`, `CTX=262144`, `PTOK=128`, `GEN=64`, `NPL="128 192 256"`,
and `OPS=MUL_MAT,MUL_MAT_ID`. Pre/post gates stayed green: MoE
`8cb0ce23777bf55f92f63d0292c756b0`, dense
`5951a5b4d624ce891e22ab5fca9bc439`, `MUL_MAT` `1146/1146`, `MUL_MAT_ID`
`806/806`. Paged safely served `n=256`, but vLLM also fit and remained faster:
`paged_decode_over_vllm=0.6354`, `paged_agg_over_vllm=0.4721`,
`paged_ttft_over_vllm=2.9401`. Do not claim GB10 parity from higher max
concurrency at this prompt/gen length and `n<=256`; a future C1 retry must push
beyond this tested point and keep the same md5/op gates.

Phase 41 records the low-concurrency counterpart to the Phase40 high-concurrency
check. Artifact:
`/home/mudler/bench/phase41_low_concurrency/20260701_091437`. The snapshot ran
with `PARALLEL=32`, `CTX=32768`, `PTOK=128`, `GEN=64`, `NPL="1 8 32"`, and
`OPS=MUL_MAT,MUL_MAT_ID`. Pre/post gates stayed green: MoE
`8cb0ce23777bf55f92f63d0292c756b0`, dense
`5951a5b4d624ce891e22ab5fca9bc439`, `MUL_MAT` `1146/1146`, `MUL_MAT_ID`
`806/806`. Paged is about `0.75x` vLLM decode at `n=1/8` and `0.665x` at
`n=32`; TTFT is `1.38x`, `3.14x`, and `3.40x` vLLM respectively. Do not reopen
D1 from this result: `0043` already ships grouped-MMQ full-step graph capture
default-on, Phase34 found `host_sync=0/4096`, and S3 is default-off because it
regressed TTFT/end-to-end throughput.

Phase 42 reconciles the target list after parallel read-only review. D1 is
closed on the current GB10 path; GDN low-conflict work is exhausted after
`0046`/`0047` plus the rejected C32/QS-early/Global-Ai32 follow-ups; W4A16/GEMM
micro-tweaks are exhausted after `0033`-`0035` and `0048`-`0050`. It nominated
the Phase38/39 persistent/load-time F32 combined gate projection as the last
small GB10 source candidate.

Phase 43 rejects that gate-fusion candidate as a small shortcut after source
inspection. `ffn_gate_inp.weight` and `ffn_gate_inp_shexp.weight` are separate
GGUF tensors; the Qwen35MoE graph consumes them in separate matmuls; the loader
can create tensors from GGUF metadata or views of existing tensors, but not a
new persistent derived concatenated weight. A correct implementation would need
a general derived-weight allocation/materialization path across mmap, offload,
split buffers, and MTP blocks. Do not implement a Qwen-only loader hack, and do
not fall back to graph-time `ggml_concat()`. After Phase43 there is no remaining
low-conflict GB10 shortcut justified by current evidence; future work is either
a larger kernel/loader design or a hardware-pivot benchmark, still gated by
MoE/dense md5 plus `MUL_MAT`/`MUL_MAT_ID` and KL if md5 changes.

Phase 44 makes the current-stack serving snapshot harness ready for hardware
pivots by parameterizing the vLLM side instead of hardcoding the GB10 defaults.
`paged-current-serving-snapshot.sh` now accepts `VLLM_GPU_MEMORY_UTILIZATION`,
`VLLM_MAX_MODEL_LEN`, `VLLM_MAX_NUM_SEQS`, `VLLM_TENSOR_PARALLEL_SIZE`, and
whitespace-split `VLLM_EXTRA_ARGS`, and prints the resolved values during
`DRY_RUN=1`. This is not a new benchmark and does not change inference code or
gate behavior. Use it when the next parity run targets datacenter Blackwell or
another non-GB10 vLLM serving shape, while keeping `hardware.txt`, pre/post
MoE/dense md5, `MUL_MAT`/`MUL_MAT_ID`, and KL-if-md5-changes as mandatory gates.

Phase 45 records the immediate inference-safety guard after Phase44. Artifact:
`/home/mudler/bench/phase45_inference_gate_guard/20260701_094320`. The DGX
phase36 build passed MoE md5 `8cb0ce23777bf55f92f63d0292c756b0`, dense md5
`5951a5b4d624ce891e22ab5fca9bc439`, `MUL_MAT` `1146/1146`, and `MUL_MAT_ID`
`806/806`. Docker, `local-ai-worker`, and GPU compute preflight were all zero
before and after the run.

Phase 46 removes the last hardcoded `q36` served-model name from the audited
serving snapshot harness. Set `SERVED_MODEL_NAME` to drive vLLM
`--served-model-name`, the vLLM readiness check, and h2h `--model` on both
engines. DGX dry run:
`/home/mudler/bench/phase46_served_model_name_dryrun/20260701_094849`, with
`SERVED_MODEL_NAME=dense-q36` printed during `DRY_RUN=1`. This is harness-only
hardware-pivot readiness, not a throughput result.

Phase 47 attempted the first dense serving snapshot using the Phase46 override.
Dry-run artifact:
`/home/mudler/bench/phase47_dense_serving_dryrun/20260701_095141`; incomplete
full artifact: `/home/mudler/bench/phase47_dense_serving/20260701_095151`.
Pre-gates were green and the paged dense arm completed through `n=128`, but the
artifact is not a dense parity result because vLLM produced no result JSONs.
Root cause: dense vLLM startup exceeded the old fixed readiness budget, and the
cleanup path could wait indefinitely on the server PID after `SIGTERM`.

Phase 48 hardens the serving snapshot harness for that failure mode. It adds
`LLAMA_READY_ATTEMPTS` and `VLLM_READY_ATTEMPTS`, bounds HTTP readiness probes
with `curl --max-time 2`, and uses bounded server cleanup that escalates from
`SIGTERM` to `SIGKILL`. Dry-run artifact:
`/home/mudler/bench/phase48_readiness_harness_dryrun/20260701_100533`, with
`VLLM_READY_ATTEMPTS=700` printed and clean DGX preflight.

Phase 47 retry completed after Phase48. Artifact:
`/home/mudler/bench/phase47_dense_serving_retry/20260701_100811`. Pre/post
gates were green: MoE `8cb0ce23777bf55f92f63d0292c756b0`, dense
`5951a5b4d624ce891e22ab5fca9bc439`, `MUL_MAT` `1146/1146`, `MUL_MAT_ID`
`806/806`. Dense paged decode beats vLLM at low concurrency (`1.3434x` at `n=1`,
`1.1560x` at `n=8`) but falls behind at `n=32/128` (`0.9036x`, `0.7912x`), and
TTFT remains `1.87x` to `4.05x` vLLM. This does not change the GB10 conclusion.

Phase 49 removes vLLM log noise from harness-owned environment variables. The
`vllm serve` child now unsets `VLLM_MODEL`, `VLLM_BIN`,
`VLLM_READY_ATTEMPTS`, `VLLM_GPU_MEMORY_UTILIZATION`, `VLLM_MAX_MODEL_LEN`,
`VLLM_MAX_NUM_SEQS`, `VLLM_TENSOR_PARALLEL_SIZE`, and `VLLM_EXTRA_ARGS` while
preserving intentional vLLM runtime variables such as `VLLM_LOGGING_LEVEL`. Dry
run: `/home/mudler/bench/phase49_vllm_env_hygiene_dryrun/20260701_102138`.

Phase 50 resolves the dense high-N decode-accounting question with a graph-node
difference-method profile. Artifact:
`/home/mudler/bench/phase50_dense_true_decode/20260701_103120`. Pre/post
inference gates on the profiled `build-cuda` binary stayed green: MoE
`8cb0ce23777bf55f92f63d0292c756b0`, dense
`5951a5b4d624ce891e22ab5fca9bc439`, `MUL_MAT` `1146/1146`, and `MUL_MAT_ID`
`806/806`. Dense `npl=128`, `npp=128` true decode is `383.66 t/s` for paged and
`435.00 t/s` for vLLM, ratio `0.8820`. This means Phase47's `0.7912` h2h
decode ratio and `0.5071` aggregate ratio include scheduler/admission and
prefill-overlap/accounting effects beyond the real GPU-steady decode gap. Next
GB10 code work should instrument batch composition/admission in
`server_context::pre_decode()` before attempting another kernel shortcut.

Phase 51 implements that admission trace in the llama.cpp fork. Local fork
commit: `c6cb8460e feat(server): trace serving admission batches`. The trace is
default-off behind `LLAMA_SERVING_TRACE=1`, adds a small unit-tested accumulator,
and records aggregate `pre_decode()` scheduler shape: decode tokens, prompt
tokens admitted, waiting prompt slots, started/continued prompt slots,
decode-only steps, `n_batch`, `n_ubatch`, `prefill_budget_step`, and
`prefill_cap_per_slot`. DGX artifact:
`/home/mudler/bench/phase51_serving_admission_trace/20260701_110130`. The
patched `build-cuda` CTest passed and inference gates stayed green: MoE
`8cb0ce23777bf55f92f63d0292c756b0`, dense
`5951a5b4d624ce891e22ab5fca9bc439`, `MUL_MAT` `1146/1146`, `MUL_MAT_ID`
`806/806`. Push and LocalAI patch-series regeneration are still pending because
push requires explicit approval.

Phase 52 uses the Phase51 trace on DGX for dense `n=128`, `ptok=128`, `gen=64`.
Artifact: `/home/mudler/bench/phase52_dense_admission_trace/20260701_111017`.
Pre/post md5 and op gates stayed green. The clean traced h2h row was
`decode_agg_tps=360.5`, `prefill_tps=629.5`, `ttft_mean_ms=23171.5`, wall
`58.921s`. The admission trace reported `steps=76`, `decode_only_steps=0`,
`decode_tokens=8064`, `prompt_tokens=22785`, `max_waiting_prompt_slots=35`,
`started_prompt_slots=128`, `continued_prompt_slots=139`,
`prefill_budget_step=0`, and `prefill_cap_per_slot=0`. The prompt token count
matches h2h exactly, so this is the target request. The next GB10 lever should
be a default-off scheduler/admission A/B or a per-step histogram trace, not an
immediate GDN/GEMM rewrite.

Phase 53 tested the existing runtime admission-budget knobs instead of adding
new code. Artifact:
`/home/mudler/bench/phase53_dense_admission_budget_sweep/20260701_111915`.
Pre/post gates stayed green. Dense `n=128` results: default Phase52 `agg=139.0`,
`decode_agg=360.5`, `prefill=629.5`, `TTFT=23171.5ms`, wall `58.921s`;
`T=1536 cap=512` `agg=134.4`, `decode_agg=376.7`, `prefill=607.0`,
`TTFT=22263.7ms`, wall `60.968s`; `T=1024 cap=512` `agg=130.0`,
`decode_agg=392.4`, `prefill=565.2`, `TTFT=23234.3ms`, wall `63.003s`.
Decision: simple budget shrinkage is rejected. It raises h2h decode-agg while
lowering aggregate/prefill throughput, and it does not materially solve TTFT.
Next scheduler work should be per-step histograms or a targeted first-token
admission policy.

Phase 54 through Phase 59 tested that targeted scheduler path. The fork commits
are still local-only and default-off:

- `c6cb8460e feat(server): trace serving admission batches`
- `bd7b2e952 feat(server): add admission trace histograms`
- `8a97629a4 feat(server): add TTFT prefill-first scheduler mode`
- `3b6ab5fa8 feat(server): cap TTFT prefill-first decode deferral`
- `8759213e3 feat(server): gate TTFT defer by prompt backlog`

Phase59 is the current verdict. Artifact:
`/home/mudler/bench/phase59_moe_min32_repeat_vllm/20260701_123147`. Pre/post
llama gates stayed green: MoE `8cb0ce23777bf55f92f63d0292c756b0`, dense
`5951a5b4d624ce891e22ab5fca9bc439`, `MUL_MAT` `1146/1146`, `MUL_MAT_ID`
`806/806`. MoE `n=128`, `ptok=128`, `gen=64` repeated the Phase58 min32 signal:
llama default `agg=336.6`, `TTFT=7798.5ms`, wall `24.334s`; llama min32
`agg=336.9`, `TTFT=7167.8ms`, wall `24.316s`. Matching vLLM was still
`agg=601.3`, `TTFT=2968.1ms`, wall `13.563s`.

Decision: keep `LLAMA_TTFT_PREFILL_FIRST=1` and
`LLAMA_TTFT_PREFILL_FIRST_MIN_WAITING=32` as an opt-in llama.cpp latency/QoS
knob. It does not prove vLLM parity progress by itself. Do not default it until
more workload coverage exists, and do not regenerate LocalAI patches until the
fork commits are pushed with explicit approval.

Phase 60 re-profiled the current W4A16 grouped MoE prefill path to check whether
there was still a low-conflict W4A16 shortcut after Phase1-5. Artifact:
`/home/mudler/bench/phase60_w4a16_current_profile/20260701_104915`. Pre/post
gates stayed green: MoE `8cb0ce23777bf55f92f63d0292c756b0`, dense
`5951a5b4d624ce891e22ab5fca9bc439`, `MUL_MAT` `1146/1146`, `MUL_MAT_ID`
`806/806`. Default FP4-MMQ S_PP was `2327.69` at `npp=512` and `2423.20` at
`npp=2048`; forced W4A16 was `1451.00` and `1482.76`, only `0.623x` and
`0.612x` of default. The `npp=512` profile showed W4A16 still dominated by
`w4a16_grouped_kernel` (`4.142s`, `42.5%`) plus sorted activation gathers
(`1.094s`, `11.2%`), while the cast kernel was only `0.517s` (`5.3%`).

Decision: do not add another small W4A16 metadata/body/cast patch. Future W4A16
work needs a larger redesign that improves the grouped kernel body and removes
or fuses sorted activation movement. Near-term GB10 parity work should return to
broader prefill/GDN/MoE design or hardware-pivot benchmarking.

Phase61 is scoped as that larger W4A16 kill-gate, not as a committed code
change: `docs/superpowers/plans/2026-07-01-w4a16-direct-activation-phase61.md`.
It proposes a default-off `LLAMA_W4A16_DIRECT_A=1` experiment that consumes the
original activation tensor plus the existing `ids_to_sorted` map directly,
removing Phase60's sorted activation gather and separate cast kernels before any
grouped-kernel body rewrite. Keep it only if it improves forced W4A16 S_PP by at
least `+12%` and reaches at least `0.75x` default FP4-MMQ; otherwise reject and
do not continue W4A16 body tuning.

Phase61 result: rejected. The direct-A kernel passed correctness after matching
`get_rows_cuda` flat-row addressing (`MUL_MAT_ID` `806/806`; forced/direct-A
MoE transcript md5 both `07db32c2bcb78d17a43ed18bc22705cd`) and default gates
remained green (`8cb0ce23`, `5951a5b4`, `MUL_MAT` `1146/1146`, `MUL_MAT_ID`
`806/806`). But direct-A only improved forced W4A16 S_PP `1471.05 -> 1566.30`
at `npp=512` and `1502.46 -> 1605.82` at `npp=2048` (`+6.5%` / `+6.9%`), still
just `0.67x` / `0.66x` of default FP4-MMQ. The direct kernel diff was not
committed; only the safe policy/routing stub remains in the fork. Do not pursue
more W4A16 body tuning on GB10 as the next parity lever.

---

## 5. METHODOLOGY LESSONS (so you do not repeat the mistakes)

1. **Profile, don't assume. The analysts were wrong 4 times.** Every one was caught only by an in-backend A/B or a corrected profile:
   - **GDN-scalar grep** (assumed the scan was scalar/serial from reading source) - wrong, retired by the tensor-core port.
   - **dense-cuBLAS reroute** (assumed dequant->bf16 would win) - wrong, -31% to -62%.
   - **occupancy** (assumed blocks/SM was the GDN bound) - wrong, 1844 vs 1814 within noise.
   - **projection-regime** (assumed FP8/NVFP4 projections were a big lever) - wrong, projections are ~12% of the decode stream at high N.
   **In-backend A/B is the only truth.** A standalone PoC win (0034) is not a result.
2. **Per-kernel us/tok overstates end-to-end S_PP/S_TG.** A kernel that is X% faster in isolation does not move throughput X%; always confirm against the end-to-end batched-bench / serving number.
3. **The CUDA-graph-trace decode artifact (the big one).** Decode is a replayed graph; nsys without `--cuda-graph-trace=node` collapses it and lies. This single trap produced the wrong "host-bound / 159 us/tok / 56%" story across multiple analyses. Always graph-node-trace + difference method (section 3.4).
4. **Beware GPU contention skewing absolutes.** The box runs concurrent quant/repack/finetune jobs. Gate on idle GPU + free lock; prefer the same-session both-engine harness so both numbers move together.
5. **The vLLM server number is inflated ~8 pt vs its true GPU-steady.** vLLM's chunked-prefill-overlap inflates its own server-measured decode window (1177 server vs 1078 true GPU-steady). Compare GPU-steady to GPU-steady, or you will chase a phantom gap. The reconciliation chain that must sum: vLLM server 1177 (100%) -> vLLM true GPU-steady 1078 (92%) -> llama GPU-steady 924 (78.5% of 1177, = 86% of 1078) -> llama server 718 (60.7%, the S3-recoverable serving overhead).

---

## 6. THE THREE FORWARD DIRECTIONS

### (a) Close / ship the record (lowest effort, do this first)
The investigation is closed for GB10 shortcuts, and the closeout chores below
are now done:

- patch `0044` is tracked in the LocalAI series;
- the Makefile pin `0ed235ea2c17a19fc8238668653946721ed136fd` is the
  authoritative paged pin;
- Phase 20 re-ran the current-stack serving snapshot on the clean mirror;
- Phase 22 re-verified the patch-series mirror invariant after `0055`.

For future release checks, run `paged-inference-gates.sh` and
`paged-current-serving-snapshot.sh` from the LocalAI backend tree. The inference
gate now defaults to both `MUL_MAT` and `MUL_MAT_ID`; set `OPS=` only for a
focused diagnostic run.

### (b) Datacenter-Blackwell pivot (THE real parity path)
The thesis: every vLLM advantage that wins on GB10 is a kernel that is **broken or capped on consumer Blackwell** and **inverts on datacenter Blackwell** (B200): FLA blocked-solve GDN, Marlin/CUTLASS grouped FP4, HBM-tuned full-cudagraph decode, native tcgen05/TMEM. ~8 TB/s HBM lifts the LPDDR5x GDN bandwidth floor ~30x. Concrete first steps:
1. Acquire a B200 (or equivalent HBM tcgen05 part). Reproduce the **both-engine same-session harness** there (`combined_definitive.sh` discipline): build the stock and paged binaries, build vLLM 0.23.0+, run MoE + dense prefill + serving for both engines.
2. Re-measure the FP4 path: on B200, native CUTLASS NVFP4 grouped-GEMM should work (the CUTLASS #3096 / TMA-WS failure is consumer-Blackwell-specific). Confirm whether vLLM now runs **native FP4** instead of Marlin W4A16. If so, the 4.1 GEMM track must be re-evaluated from scratch (it was rejected on a GB10-specific ceiling).
3. Re-take the decode profile with `--cuda-graph-trace=node`; the GDN scan that floors at 273 GB/s on GB10 should no longer dominate at HBM bandwidth - re-derive the per-token decomposition before choosing any lever.

### (c) Multi-week persistent-Marlin decode kernel (decode-only, low-EV, CANNOT reach parity)
Only pursue if (a)+(b) are not options and someone explicitly wants the residual decode gap closed on GB10. It targets the ~14 pt GPU-steady decode gap (vLLM's fused-Marlin MoE persistent-tiling + single Triton elementwise). Concrete first steps:
1. Re-confirm the ceiling first: our own ggml Marlin port already lost -19.6% at decode (4.3), so the bar is "beat that and beat FP4-MMQ at the decode BW floor".
2. Prototype the persistent-tiling grouped-FP4 MoE kernel **standalone**, then prove it **in-backend** (a PoC win is not a result, per 0034). It must live inside a single-stream CUDA graph or bring its own multi-stream overlap.
3. Bound the upside honestly: this is **decode-only ~4-14%** and **does nothing for the prefill floor (36-43%)**, so it does not reach parity. Record the verdict either way.

---

## 7. KEY FILE / ARTIFACT INDEX

### Fork (canonical source of truth)
- Local canonical fork: `/home/mudler/_git/llama.cpp`, branch **`localai-paged`**, HEAD `2d590d770` ("trace cublas tensor names", patch `0063`).
- DGX current clean mirror/build tree: `dgx:~/llama-phase6-source`, HEAD `2cbb61969` with the Phase 37 cuBLAS tensor-name trace patch applied and committed; Phase 20/26/27 artifacts still record their historical source hashes.
- Historical DGX dev tree: `dgx:~/llama-paged-dev`, branch **`paged`**, HEAD `a7d439e8ce6990eb09721223c975da4e49d8d136` ("GDN CONFIG C (M8) - bf16 Kc/Qc"). It is an old experimental tree and must not be treated as canonical.

### LocalAI worktree
- Path: `/home/mudler/_git/LocalAI/.claude/worktrees/feat+paged-attention`, branch `worktree-feat+paged-attention` (currently 246 ahead, 31 behind `origin/master`; recompute before reporting).
- Backend dir: `backend/cpp/llama-cpp-localai-paged/` (`Makefile` thin wrapper, `package.sh`, `run.sh`, `README.md` ~44 KB canonical, `docs/`, `patches/paged/`).
- `docs/`: `VLLM_PARITY_FINAL.md` (authoritative record), `VLLM_PARITY_LEVER_MAP.md` (working brainstorm, profile-validated section), `DECODE_SERVING_SCOPE.md`, `PREFILL_GEMM_SCOPE.md`, `PREFILL_GEMM_RESULTS.md`, `TENSORCORE_GDN_SCOPE.md`, `TENSORCORE_GDN_BUILD_PLAN.md`, `ACCELERATOR_PORTING_SCOPE.md`, `UPSTREAM_LAYER2_SCOPE.md`, `LOCALAI_LLAMACPP_BACKEND_PLAN.md`, `PAGED_BITEXACT_NOTE.md`, `PATCH_MAINTENANCE.md`, `final_benchmark.csv`, `paged-burst-bench.cpp`, `paged-reclaim-unit.cpp`, 3 PNGs, and this `PARITY_HANDOFF.md`.
- `patches/paged/`: **54** `.patch` files spanning 0001-0063 with intentional gaps (missing 0005, 0026 [dropped ssm_bf16_tau], 0027, 0032, 0036-0039, 0045). Core paged-KV 0001-0012; decode-first scheduler 0013/0016; serving graph reuse 0040/0041; prefill fusions 0042/0044; SSM/GDN decode 0018-0022/0028; MoE NVFP4 quant 0023/0025/0043; FP4-MMA/Marlin scaffolds 0033/0034/0035 (default-off); GDN tensor-core prefill 0031 -> 0046 (geometry gate) -> 0047 (f32-only M5, default-on under paged KV); W4A16 packed metadata/shape/padding is 0048-0050; MoE safety tests are 0051-0053; MTP backend-sampling safety is 0054; speculative shape trace is 0055; MoE MMQ selector/launch/candidate/tile-policy/route instrumentation is 0056-0060; regular MUL_MAT route instrumentation is 0061; cuBLAS route instrumentation is 0062-0063.

### Bench artifacts (DGX)
- `~/bench/COMBINED_DEFINITIVE.txt` (+ `.log`, `.done`, `combined_definitive.sh`, `combined_definitive.out`) - historical same-session both-engine run.
- `~/bench/phase20_current_snapshot/20260701_050621` - current clean-stack paged-vs-vLLM MoE serving snapshot.
- `~/bench/phase21_harness_dryrun/20260701_051757` - current snapshot harness dry-run artifact.
- `~/bench/phase24_hardware_report_dryrun/20260701_052741` - current snapshot harness dry run proving `hardware.txt` captures the DGX as `hardware_class=gb10_or_workstation_blackwell`.
- `~/bench/phase25_gate_summary_dryrun/20260701_053353` - dry run after adding `gate_summary.tsv` support; normal dry-run still writes `hardware.txt` and does not emit a gate summary before gates exist.
- `~/bench/phase26_audited_snapshot/20260701_053650` - current audit-grade full paged-vs-vLLM MoE serving snapshot with `hardware.txt`, pre/post gates, `summary.tsv`, and `gate_summary.tsv`.
- `~/bench/phase27_graph_node_serving/20260701_055519` - current clean llama.cpp n128 serving profile captured with `--cuda-graph-trace=node`, pre/post retry gates green.
- `~/bench/phase28_mmq_occupancy/20260701_040450` - NVFP4 MMQ occupancy build-knob A/B; `MINBLOCKS=2` gate-safe but serving-regressed, `MMQ_Y=64` compile-rejected.
- `~/bench/phase29_mmq_shape_trace/20260701_042428` - default-off MoE MMQ shape trace patch `0056`; CUDA build plus default/trace md5 gates green.
- `~/bench/phase30_mmq_shape_serving/20260701_043300` - live n128 serving MMQ shape distribution from patch `0056`; post-run md5/op gates green.
- `~/bench/phase31_mmq_launch_trace/20260701_064424` - default-off MoE MMQ launch trace patch `0057`; default/trace/post-serving md5 gates green; n128 launch trace rejects stream-k/fixup shortcut (`fixup=0`, `stream_k_blocks == ntiles_dst`).
- `~/bench/phase32_small_m_classifier/20260701_070127` - default-off MoE MMQ small-M classifier patch `0058`; default/trace/post-serving md5 gates green; n128 trace found 4096 candidate calls.
- `~/bench/phase33_small_m_tile_policy/20260701_071136` - default-off MoE MMQ small-M tile policy patch `0059`; tile16/tile8 md5/op safe but both slower in n128 serving.
- `~/bench/phase34_mmid_route_trace/20260701_072737` - default-off MoE MMID route trace patch `0060`; default/trace/post-serving md5 gates green; n128 route trace found `mmq=2776`, `mmvq=1320`, `host_sync=0/4096`.
- `~/bench/phase35_mul_mat_route_trace/20260701_074359` - default-off regular MUL_MAT route trace patch `0061`; default/trace/post-serving md5 gates green; n128 route trace found BF16 `mat_f=2485`, `op_cublas=1330`.
- `~/bench/phase36_cublas_route_trace/20260701_081228` - default-off cuBLAS subroute trace patch `0062`; default/trace/post-serving md5 and op gates green; n128 route trace found `bf16_tc=5681`, `sgemm=2511`.
- `~/bench/phase37_cublas_name_trace/20260701_083227` - cuBLAS tensor-name trace patch `0063`; default/trace/post-serving md5 and op gates green; n128 trace identified `sgemm` as MoE gate logits and shared-expert gate projections.
- `~/bench/phase38_gate_baseline/20260701_084410` - current Phase37 build baseline before gate-projection policy work; docker/local-ai-worker/GPU idle preflight green; MoE/dense md5 green; `MUL_MAT` `1146/1146`; `MUL_MAT_ID` `806/806`.
- `~/bench/phase39_gate_sgemm_profile/20260701_085211` - short completion profile, diagnostic only because `-n 32` is not a canonical md5 gate; useful for confirming graph-time concat is a real kernel path.
- `~/bench/phase39_gate_sgemm_profile/phase27_reanalysis` - Phase27 serving profile re-analysis used to reject graph-time fused gate weight concat; `concat_layout=459.84ms` (`2.29%`) in the serving kernel window.
- `~/bench/phase40_max_concurrency/20260701_090012` - max-concurrency C1 check at `NPL=128/192/256`, `PTOK=128`, `GEN=64`, `PARALLEL=256`, `CTX=262144`; pre/post MoE/dense md5 and `MUL_MAT`/`MUL_MAT_ID` gates green, but vLLM also fit at `n=256` and stayed ahead (`paged_decode_over_vllm=0.6354`, `paged_agg_over_vllm=0.4721`).
- `~/bench/phase41_low_concurrency/20260701_091437` - low-concurrency serving check at `NPL=1/8/32`, `PTOK=128`, `GEN=64`, `PARALLEL=32`, `CTX=32768`; pre/post MoE/dense md5 and `MUL_MAT`/`MUL_MAT_ID` gates green; paged is `0.7493`, `0.7518`, and `0.6649` of vLLM decode at `n=1/8/32`, with TTFT still much worse by `n=8/32`; does not reopen D1.
- `~/bench/phase44_hardware_pivot_harness_dryrun/20260701_094038` - harness-only dry-run artifact proving the vLLM serving config overrides are printed and preflighted before any server starts.
- `~/bench/phase45_inference_gate_guard/20260701_094320` - post-Phase44 inference guard; MoE/dense md5 and `MUL_MAT`/`MUL_MAT_ID` backend-op gates green.
- `~/bench/phase46_served_model_name_dryrun/20260701_094849` - harness-only dry-run artifact proving `SERVED_MODEL_NAME` is printed and preflighted before any server starts.
- `~/bench/phase47_dense_serving_dryrun/20260701_095141` - dense serving dry-run with `SERVED_MODEL_NAME=dense-q36`.
- `~/bench/phase47_dense_serving/20260701_095151` - incomplete dense serving attempt; pre-gates and paged arm completed, vLLM did not produce result JSONs under the old readiness budget.
- `~/bench/phase48_readiness_harness_dryrun/20260701_100533` - harness dry-run proving configurable readiness budgets and clean preflight before retrying dense serving.
- `~/bench/phase47_dense_serving_retry/20260701_100811` - completed dense serving snapshot after Phase48; pre/post md5 and op gates green; paged low-N decode ahead, high-N aggregate and TTFT behind.
- `~/bench/phase49_vllm_env_hygiene_dryrun/20260701_102138` - harness dry-run after scrubbing harness-owned `VLLM_*` variables from the `vllm serve` child environment.
- `~/bench/phase50_dense_true_decode/20260701_103120` - dense graph-node difference-method profile at `npl=128`, `npp=128`; `build-cuda` pre/post md5 and op gates green; true decode paged `383.66 t/s`, vLLM `435.00 t/s`, ratio `0.8820`, pointing next at serving admission/scheduler tracing.
- `~/bench/phase51_serving_admission_trace/20260701_110130` - default-off `LLAMA_SERVING_TRACE=1` fork commit `c6cb8460e`; DGX patched `build-cuda` CTest and md5/op gates green; push and LocalAI patch-series mirror pending approval.
- `~/bench/phase52_dense_admission_trace/20260701_111017` - clean dense `n=128` admission trace; pre/post gates green; `decode_only_steps=0`, `prompt_tokens=22785`, `max_waiting_prompt_slots=35`; next lever is scheduler/admission A/B or per-step histogram trace.
- `~/bench/phase53_dense_admission_budget_sweep/20260701_111915` - runtime sweep of `LLAMA_MAX_BATCH_TOKENS=1536/1024` with `LLAMA_PREFILL_CAP=512`; pre/post gates green; simple budget shrinkage rejected because aggregate/prefill throughput regressed and TTFT did not materially improve.
- Per-engine logs `~/bench/COMBINED_{paged,vllm}_{MOE,DENSE}_server.log`; `~/bench/BENCHMARK_PROGRESS.md`.
- Graph-node-traced high-N profiles: `~/highN_prof2/*.nsys-rep` (paged npl=256), `~/highN_vllm/*.nsys-rep` (vLLM), 2026-06-30.
- A/B dirs: `~/bench/marlin_gate/`, `~/bench/gdn_p1_ab/`.

### Recent context commits
- `6edbb56b0` "docs(paged): definitive vLLM-parity final-state record (GB10, CLOSED)" - adds `VLLM_PARITY_FINAL.md`.
- `baf102524` "docs(paged): correct decode-serving record to ~86% GPU-steady parity (graph-node-traced)" - the ~56% -> ~86% correction.
- `bd100dd20` "fix(paged): repair the patch series, sync to the fork branch" - dropped dev-tree 0044/0045, added f32-only M5 as 0047.
- `b028c81ed` "docs(paged): record padded/fixed-slot decode shape as tested-and-rejected".

### Discrepancies to flag / resolve (carried verbatim from the gather, including UNVERIFIED labels)
1. **Pin prose reconciled in this worktree.** Makefile line 52 `LLAMA_VERSION?=0ed235ea2c17a19fc8238668653946721ed136fd` is authoritative and matches the local fork merge-base. Hard rule: the paged pin must equal the stock `llama-cpp` pin (shared `grpc-server.cpp`); a bump to `c299a92c` once broke the grpc-server link despite being bit-exact and was reverted. Trust the Makefile when building.
2. **Current fork/mirror are clean and verified.** Local fork HEAD is `2d590d770`, DGX clean mirror HEAD is `2cbb61969`, and Phase 37 should be treated as the current patch-series tip. The old `llama-paged-dev` tree is historical only.
3. **Worktree patch series is tracked through 0063.** The only expected unrelated untracked path in this worktree is `.claude/`.
4. **`sm_121a` is not in the worktree build files** - it lives only in the DGX experimental build scripts (`gdn_cc.sh`, `gdn_bv_build.sh`, `paged-build.sh`); mainline uses arch `121`. **UNVERIFIED** whether the shipped CI Dockerfile build path injects `121a` for the FP4-MMA kernels (`Dockerfile.llama-cpp-localai-paged` does not hardcode a CUDA arch).
5. **The `0921716...` paged-MoE md5 open item.** `COMBINED_DEFINITIVE.txt` records `PAGED_GATE_MD5=0921716cd0582b5d15af8c362b811d00` for MoE, but a full doc/patch/`git log -S` grep of the worktree found **no** occurrence of `0921716...` in any committed source; the committed canonical paged-MoE gate is `8cb0ce23`. Treat this as **unreconciled**: the documented, KL-validated paged-MoE gate remains `8cb0ce23`, and any paged-MoE divergence (including `0921716`) must be KL-validated against the f16 reference before being accepted as benign, never on assertion alone. The `0921716` value is **UNVERIFIED** as a sanctioned gate; do not adopt it as canonical without re-running the KL gate. The **dense** run is symmetric: `COMBINED_DEFINITIVE.txt` records `PAGED_GATE_MD5=ecfe924dee6c5622c149f419ff2a6481` for dense, which likewise differs from the canonical dense gate `5951a5b4`. Both CDEF `PAGED_GATE_MD5` values come from the `combined_definitive.sh` harness's own gate command, NOT the canonical bit-exact gate command in section 3.3, which is why they diverge from the committed `8cb0ce23` / `5951a5b4`; neither is a sanctioned gate and both must be KL-validated before being treated as benign.

---

## 8. PHASE63 RESULT: PREFILL BUCKET ATTRIBUTION

Phase63 is complete as a measurement-only no-go. The plan is
`docs/superpowers/plans/2026-07-01-prefill-bucket-attribution-phase63.md`; the
DGX artifact is `/home/mudler/bench/phase63_prefill_bucket/20260701_140127`.

Pre/post gates stayed green:

- MoE md5 `8cb0ce23777bf55f92f63d0292c756b0`;
- dense md5 `5951a5b4d624ce891e22ab5fca9bc439`;
- `MUL_MAT` `1146/1146`;
- `MUL_MAT_ID` `806/806`.

The candidate paged FlashAttention mask/block-table cleanup is rejected for now:
llama.cpp FA is only `0.71%` at `npp=512` and `1.18%` at `npp=2048`; the
`npp=2048` cross-engine FA delta is about `1.7 us/tok`, not the `15 us/tok`
needed to fund source work. No llama.cpp source files were modified.

*Status: Phase63 closed. `VLLM_PARITY_FINAL.md` remains the GB10 shortcut record;
the remaining measured buckets are still MoE/FFN GEMM, GDN, bf16 projections,
layout copies, and activation quantization.*

## 9. PHASE64 RESULT: LAYOUT TRACE

Phase64 added default-off layout attribution in the llama.cpp fork:
`fa944bb5f feat(cuda): trace layout tensor names`. The env gate is
`LLAMA_LAYOUT_TRACE=<n>`. It traces CUDA `GET_ROWS`, `CPY`, `CONT`, `DUP`, and
`CONCAT` runtime dispatch with tensor names, types, shapes, and contiguity flags.

DGX artifact: `/home/mudler/bench/phase64_layout_trace/20260701_142519`.
Patched build gates stayed green: MoE md5 `8cb0ce23`, dense md5 `5951a5b4`,
`MUL_MAT 1146/1146`, `MUL_MAT_ID 806/806`.

Trace result at MoE `npp=512`, `ntg=4`, `npl=32`:

- `get_rows`: `7268`
- `cpy`: `2008`
- `cont`: `1734`
- `concat`: `990`

The named layout sources are GDN conv-state gather/concat/update
(`cache_r_lN`, `conv_states_reshaped-N`, `qkv_mixed_transposed-N`,
`conv_input-N`, `conv_state_update-N`), MoE top-k fan-in gathers
(`ffn_moe_probs-N`, `ffn_moe_topk-N`, `ffn_moe_weights-N`), and paged-attention
mask/KV reshape/copy paths. This does not fund a clean layout optimization yet;
it gives Phase65 the exact names needed to either remove one repeated chain or
reject it with evidence.

## 10. PHASE65 RESULT: QUANT TRACE

Phase65 added default-off activation-quant route attribution in the llama.cpp
fork: `afc2c7030 feat(cuda): trace activation quant routes`. The env gate is
`LLAMA_QUANT_TRACE=<n>`. DGX mirror commit: `7863194bd`.

DGX artifact: `/home/mudler/bench/phase65_quant_trace/20260701_143729`.
Patched build gates stayed green: MoE md5 `8cb0ce23`, dense md5 `5951a5b4`,
`MUL_MAT 1146/1146`, `MUL_MAT_ID 806/806`.

Trace result at MoE `npp=512`, `ntg=4`, `npl=32`:

- `mmq_dense`: `4444`
- `mmq_moe_dedup_unique`: `2960`
- `mmq_moe_gather`: `2960`
- `mmq_moe_flat`: `1480`

The dominant default-path shapes are MoE gate/up expert activation quant
deduplication (`K=2048`, `rows=512`) followed by gather to expert-token rows
(`rows=4096`), shared-expert dense gate/up quantization (`K=2048`, `rows=512`),
MoE down expert flat quantization (`K=512`, `rows=4096`), and shared-expert down
quantization (`K=512`, `rows=512`). This confirms the activation-quant bucket is
concentrated in named MoE/shared-expert FFN paths, but it does not prove whether
`gather_mmq_fp4` is material or just a cheap cost of the existing dedup win.
Phase66 should time `quantize_mmq_nvfp4` versus `gather_mmq_fp4` with nsys/NVTX
before funding any behavior-changing source patch.

## 11. PHASE66 RESULT: QUANT KERNEL TIMING

Phase66 timed the Phase65 candidate kernels directly with Nsight Systems.
Artifact: `/home/mudler/bench/phase66_quant_kernel_timing/20260701_144256`.
Profile: `quant_npp512.nsys-rep`; summary:
`quant_npp512_kern_sum_cuda_gpu_kern_sum.csv`.

Shape: MoE `npp=512`, `ntg=4`, `npl=32`. Total GPU kernel time:
`7108388986 ns`.

| kernel | time | instances | share |
|--------|-----:|----------:|------:|
| `quantize_mmq_nvfp4` | `317205504 ns` | `8884` | `4.46%` |
| `gather_mmq_fp4` | `45374880 ns` | `2960` | `0.64%` |
| combined | `362580384 ns` | - | `5.10%` |

Decision: reject a Phase66 gather/quant source patch. The gather is too small
to target, and quantize plus gather is below the `8%` source-funding threshold.
Do not reopen W4A16/no-activation-quant from this evidence; that larger rewrite
was already rejected in earlier phases.

## 12. PHASE67 RESULT: BF16 CUBLAS F32 OUTPUT

Phase67 added a default-off BF16 projection shortcut in the llama.cpp fork:
`ea0875d14 feat(cuda): gate BF16 cuBLAS F32 output`. The env gate is
`LLAMA_BF16_CUBLAS_F32_OUT=1`. DGX mirror commit: `14fd69f1e`.

DGX artifact: `/home/mudler/bench/phase67_bf16_f32_out/20260701_144909`.
Default and opt-in gates stayed green: MoE md5 `8cb0ce23`, dense md5
`5951a5b4`, `MUL_MAT 1146/1146`.

Same-window MoE prefill A/B:

| npp | default S_PP | opt-in S_PP | change |
|-----|-------------:|------------:|-------:|
| `512` | `2347.41` | `2402.34` | `+2.34%` |
| `2048` | `2440.18` | `2456.54` | `+0.67%` |

The opt-in `npp=512` profile removed the BF16-to-F32 conversion row:
`convert_unary<__nv_bfloat16, float>` became `0 ns`, `0` instances. Keep this
as default-off for now. It is correctness-clean and measurable, but the win is
small and needs dense plus serving A/B before any default-on decision.

## 13. PHASE68 RESULT: BF16 F32 OUTPUT DENSE + SERVING A/B

Phase68 reused Phase67 source unchanged. Plan:
`docs/superpowers/plans/2026-07-01-bf16-f32-output-dense-serving-phase68.md`.
DGX artifact: `/home/mudler/bench/phase68_bf16_dense_serving/20260701_145710`;
serving A/B artifact:
`/home/mudler/bench/phase68_bf16_dense_serving/20260701_145710/serving_ab_20260701_150249`.

Correctness basis for the exact source commit remains Phase67: default and
`LLAMA_BF16_CUBLAS_F32_OUT=1` both produced MoE md5 `8cb0ce23`, dense md5
`5951a5b4`, and `MUL_MAT 1146/1146`.

Dense prefill stayed positive but tiny:

| npp | default S_PP | opt-in S_PP | change |
|-----|-------------:|------------:|-------:|
| `512` | `973.13` | `975.52` | `+0.25%` |
| `2048` | `1019.88` | `1021.39` | `+0.15%` |

MoE serving A/B at `N=128`, prompt `128`, generation `128`, `--parallel 128`:

| metric | default | opt-in | change |
|--------|--------:|-------:|-------:|
| `agg_tps` | `409.8` | `415.0` | `+1.27%` |
| `decode_agg_tps` | `615.3` | `627.2` | `+1.93%` |
| `prefill_tps` | `1630.2` | `1648.0` | `+1.09%` |
| `ttft_mean_ms` | `8574.7` | `8085.9` | `-5.70%` |
| `wall_s` | `39.978` | `39.480` | `-1.25%` |

Decision: carry the shortcut as a default-off opt-in candidate. It is no longer
just a prefill-only win, but Phase68 is not enough to default it on. Any future
default-on proposal must mirror the fork commit into the LocalAI patch series
and rerun a broader current serving snapshot with pre/post md5 and op gates.

## 14. PHASE69 RESULT: PATCH SERIES MIRROR READINESS

Phase69 checked the patch-series state without pushing and without editing
generated patch files. Plan:
`docs/superpowers/plans/2026-07-01-patch-series-mirror-readiness-phase69.md`.

Current committed LocalAI patches still match the Phase37 fork tip:

```text
base=0ed235ea2c17a19fc8238668653946721ed136fd
applied_tree=dedb1182910eafe9f6875588dc8285bfb544cce5
patch_tip_tree=dedb1182910eafe9f6875588dc8285bfb544cce5
fork_head_tree=fcf5720b659c5e1e2b487ccf3c8f7289bb12b9c4
match_patch_tip=yes
match_fork_head=no
patch_count=54
```

Dry-run export from `2d590d770..ea0875d14` produced ten additive source-only
patches, projected as `0064..0073`. Applying current `0001..0063` plus temp
`0064..0073` onto the pin exactly reconstructed current fork HEAD:

```text
applied_plus_missing_tree=fcf5720b659c5e1e2b487ccf3c8f7289bb12b9c4
fork_head_tree=fcf5720b659c5e1e2b487ccf3c8f7289bb12b9c4
match_fork_head=yes
current_patch_count=54
missing_patch_count=10
projected_patch_count=64
```

Projected patch tail:

- `0064` serving admission trace (`c6cb8460e`)
- `0065` admission histograms (`bd7b2e952`)
- `0066..0068` TTFT prefill-first scheduler knobs (`8a97629a4`,
  `3b6ab5fa8`, `8759213e3`)
- `0069..0070` W4A16 direct-activation policy/stub (`41be3da5b`,
  `7967ad47f`)
- `0071` layout trace (`fa944bb5f`)
- `0072` quant trace (`afc2c7030`)
- `0073` BF16 cuBLAS F32 output (`ea0875d14`)

Decision: mirror regeneration is technically ready but not executed. The local
fork is `26` commits ahead of `fork/localai-paged`, and the fork-first policy
requires pushing before regenerating the LocalAI series. Do not push without
explicit approval. After approval, push the fork, regenerate `0064..0073`, rerun
the same tree-hash check, and then run the broader serving gates before any
default-on BF16 policy change.

## 15. PHASE70 RESULT: BF16 F32 OUTPUT BROADER SERVING

Phase70 broadened the Phase68 serving evidence without source changes. Plan:
`docs/superpowers/plans/2026-07-01-bf16-f32-output-broader-serving-phase70.md`.
Benchmark ledger:
`backend/cpp/llama-cpp-localai-paged/docs/BENCHMARK.md`.
DGX artifact:
`/home/mudler/bench/phase70_bf16_broader_serving/20260701_151500`.

Gates stayed green. Default pre/post gates matched MoE md5 `8cb0ce23`, dense
md5 `5951a5b4`, `MUL_MAT 1146/1146`, and `MUL_MAT_ID 806/806`. Opt-in pre/post
gates matched MoE md5 `8cb0ce23`, dense md5 `5951a5b4`, and `MUL_MAT
1146/1146`.

Serving shape: MoE `NPL=8 32 128`, prompt `128`, generation `64`,
`PARALLEL=128`.

| n | opt/default agg | opt/default decode | opt/default TTFT | default decode/vLLM | opt decode/vLLM |
|---:|----------------:|-------------------:|-----------------:|--------------------:|----------------:|
| `8` | `0.8896` | `0.8998` | `1.1247` | `0.8100` | `0.7289` |
| `32` | `0.9912` | `0.9974` | `1.0320` | `0.6882` | `0.6864` |
| `128` | `1.0071` | `0.9882` | `0.9852` | `0.6921` | `0.6839` |

Decision: reject default-on for `LLAMA_BF16_CUBLAS_F32_OUT=1`. The shortcut is
correctness-clean, but it materially regressed low-concurrency serving and
slightly widened the vLLM decode gap at `n=32` and `n=128`. Keep it
default-off only and move the next parity effort to a different lever.

## 16. PHASE71 RESULT: GDN TENSOR-CORE REVALIDATION

Phase71 challenged the stale GDN planning docs before starting more source work.
Plan:
`docs/superpowers/plans/2026-07-01-gdn-tc-revalidation-phase71.md`.
Benchmark ledger:
`backend/cpp/llama-cpp-localai-paged/docs/BENCHMARK.md`.
DGX artifact:
`/home/mudler/bench/phase71_gdn_tc_revalidation/20260701_153425`.

Source under test stayed at DGX mirror commit
`14fd69f1e feat(cuda): gate BF16 cuBLAS F32 output`. No llama.cpp source was
changed.

Canonical gates matched for all four GDN modes: MoE md5 `8cb0ce23`, dense md5
`5951a5b4`, and `GATED_DELTA_NET 46/46`. Default also passed `MUL_MAT
1146/1146` and `MUL_MAT_ID 806/806`.

MoE prefill, `PP=512,2048`, `TG=4`, `B=32`, `CTX=131072`:

| arm | npp512 S_PP | npp2048 S_PP |
|-----|------------:|-------------:|
| default | `2313.57` | `2422.88` |
| sequential-disabled (`GDN_CHUNK_MIN=2147483647`) | `2198.28` | `2361.22` |
| serial-chunked (`GDN_TC=0 GDN_CHUNK_MIN=64`) | `1787.49` | `1699.77` |
| forced M5 (`GDN_TC=4 GDN_CHUNK_MIN=64`) | `2323.18` | `2420.52` |

Decision: keep shipped GDN M5 default behavior. It still beats
sequential-disabled by `+5.24%`/`+2.61%`, beats serial-chunked by
`+29.43%`/`+42.54%`, and forced M5 is within noise of the current default. Do
not reopen smaller GDN C32/QS/global-Ai32/kernel-reorder work on GB10.

Post-Phase71 do-not-reopen list for GB10:

- Smaller W4A16/MoE GEMM body, metadata, direct-activation, or quant/gather
  shortcuts.
- GDN C32 slab, QS-early, Global-Ai32, or another low-conflict M5 reorder.
- BF16 cuBLAS F32 output as a default-on policy.

The only GDN work that should be reconsidered is a larger FLA/CuteDSL-class
blocked-solve implementation or a hardware pivot where the GB10 constraints no
longer apply.

## 17. PHASE72 RESULT: TTFT MIN32 BROADER SERVING

Phase72 broadened the Phase59 min32 scheduler result to the same serving shape
used by Phase70. Plan:
`docs/superpowers/plans/2026-07-01-ttft-min32-serving-phase72.md`.
Benchmark ledger:
`backend/cpp/llama-cpp-localai-paged/docs/BENCHMARK.md`.
DGX artifact:
`/home/mudler/bench/phase72_ttft_min32_serving/20260701_160730`.

Source under test stayed at DGX mirror commit
`14fd69f1e feat(cuda): gate BF16 cuBLAS F32 output`. No llama.cpp source was
changed.

Gates stayed green. Pre default matched MoE md5 `8cb0ce23`, dense md5
`5951a5b4`, `MUL_MAT 1146/1146`, and `MUL_MAT_ID 806/806`. Pre/post min32 and
post default md5 gates also matched MoE `8cb0ce23` and dense `5951a5b4`.

Serving shape: MoE `NPL=8 32 128`, prompt `128`, generation `64`,
`PARALLEL=128`.

| n | min32/default agg | min32/default decode | min32/default TTFT | default decode/vLLM | min32 decode/vLLM |
|---:|------------------:|---------------------:|-------------------:|--------------------:|------------------:|
| `8` | `0.9302` | `0.9442` | `1.0379` | `0.7561` | `0.7140` |
| `32` | `0.9414` | `0.9570` | `1.0977` | `0.7158` | `0.6850` |
| `128` | `0.9699` | `0.9775` | `1.0300` | `0.6935` | `0.6779` |

Decision: keep `LLAMA_TTFT_PREFILL_FIRST=1` plus
`LLAMA_TTFT_PREFILL_FIRST_MIN_WAITING=32` opt-in only. It regressed aggregate,
decode, TTFT, and wall time at every tested concurrency in the broader shape,
and widened the vLLM decode gap. Do not default this scheduler policy on GB10.

## 18. PHASE73 RESULT: DATACENTER BLACKWELL RERUN READINESS

Phase73 is a no-new-benchmark decision/spec phase. Plan:
`docs/superpowers/plans/2026-07-01-datacenter-blackwell-rerun-readiness-phase73.md`.
Benchmark ledger:
`backend/cpp/llama-cpp-localai-paged/docs/BENCHMARK.md`.

No GPU benchmark was run and no llama.cpp source was changed. Source baseline
remains DGX mirror commit `14fd69f1e feat(cuda): gate BF16 cuBLAS F32 output`.

Decision:

- Do not start more GB10 grouped-MMQ/W4A16 source work. Phase61 direct-A was
  the last structurally distinct W4A16 shortcut and failed its keep gate; Phase66
  quantize plus gather was only `5.10%`, below the source-funding threshold.
- Do not start GDN backend source work until a standalone C=64 blocked-solve PoC
  proves timing and numerical viability. Phase71 kept M5 as shipped; the
  remaining GDN gap is a larger FLA/CuteDSL-class blocked-solve/register-state
  implementation, not another C32/QS/global-Ai/local reorder.
- The next parity evidence should come from datacenter Blackwell hardware with
  the existing same-session serving harness plus graph-node decode profiles.

B200 rerun checklist:

1. Build and verify the llama.cpp paged binary on B200 or equivalent
   datacenter Blackwell hardware with the correct CUDA architecture/settings.
2. Install and verify vLLM `0.23.0+` with the intended Blackwell backend stack.
3. Confirm both model forms exist: `q36-35b-a3b-nvfp4.gguf` and
   `q36-35b-a3b-nvfp4-vllm`.
4. Run `paged-current-serving-snapshot.sh` with `NPL="8 32 128"`, `PTOK=128`,
   `GEN=64`, `PARALLEL=128`, `CTX=131072`, and B200-specific
   `VLLM_GPU_MEMORY_UTILIZATION`, `VLLM_MAX_NUM_SEQS`, and
   `VLLM_TENSOR_PARALLEL_SIZE`.
5. Before interpreting the artifact, require `hardware.txt` to say
   `hardware_class=datacenter_blackwell`, `gate_summary.tsv` to be green,
   pre/post MoE md5 `8cb0ce23`, dense md5 `5951a5b4`, `MUL_MAT` and
   `MUL_MAT_ID` op gates green, and `summary.tsv` rows for both paged and vLLM.
6. Run decode/profile reruns with `nsys --cuda-graph-trace=node` and inspect
   whether vLLM is using native FP4/CUTLASS/FlashInfer rather than the GB10
   Marlin fallback.

Standalone GDN source-work gate:

```sh
nvcc -O3 -arch=sm_121a \
  ~/scratch_tc_gdn_poc/gdn_blocked_solve_bench.cu \
  -o ~/scratch_tc_gdn_poc/gdn_blocked_solve_bench

~/scratch_tc_gdn_poc/gdn_blocked_solve_bench \
  --c 64 --dk 128 --dv 128 \
  --iters 1000 \
  --precision tf32,offdiag3x,apply3x \
  --oracle f64 \
  --dump-json ~/bench/phase73_gdn_blocked_solve_poc.json
```

Do not touch `ggml/src/ggml-cuda/gated_delta_net.cu` for this larger path until
that standalone artifact shows a material timing win, non-catastrophic weak and
mixed decay error, plausible register/shared-memory fit, and records timing,
precision-rung error, and condition-number distribution.
