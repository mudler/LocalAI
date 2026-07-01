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

- The investigation is **CLOSED**. Parity is **not reachable on GB10** silicon; the residual is a hardware ceiling, not engineering debt.
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
- **DCO sign-off required:** every commit ends with `Signed-off-by: Ettore Di Giacinto <mudler@localai.io>`.
- **AI attribution via `Assisted-by:` trailer:** `Assisted-by: Claude:opus-4.8 [Claude Code]`.
- **NEVER add `Co-Authored-By:` (AI) trailers** and never add an AI `Signed-off-by`.
- **No em-dashes** anywhere in output (use `-`, `:`, parentheses, or rephrase).
- **Ask before every `git push`.** Prior approval does not carry over.

### 2.5 Fork-first is MANDATORY (the fork is canonical)
- The **canonical source of truth is the fork branch `mudler/llama.cpp:localai-paged`** (= pin commit + paged patch commits in order). It is canonical for ALL paged-backend kernel/patch work. The shipped `patches/paged/*.patch` series is a **derivative**: the fork is the source.
- **Always update the fork FIRST, in this exact order:** (1) commit the change on the `localai-paged` branch and **push it**, then (2) regenerate the LocalAI series (`backend/cpp/llama-cpp-localai-paged/patches/paged/`) from the fork via `git format-patch` (one patch per fork commit, source-only, never touching a `*.md`/dev-doc), so the series stays a **1:1, drift-free mirror** of the branch. No hand-export.
- **NEVER edit the LocalAI `patches/paged/*.patch` files directly**, and **NEVER add a patch to the series with no corresponding fork-branch commit.** They are generated output, not source.
- The fork branch is also **where the build and the per-path bit-exact md5 gate actually run**, so it is the **only** place a change is truly validated. A patch that lives only in the LocalAI series has never been built or gated.
- **Mirror invariant (verify by tree hash):** applying the full on-disk series on the pin must reproduce the fork branch tree byte-for-byte. The series has **intentional gaps** (missing 0005, 0026, 0027, 0032, 0036-0039, 0045), so the patch count is not the max number; what must hold is the tree-hash equality, not the count. (Concretely: fork HEAD `d9b9be0be` is mirrored by worktree patch `0050-feat-paged-pad-W4A16-A-shared-tile-stride.patch`; W4A16 grouped tile shape is worktree patch `0049`, packed metadata is `0048`, and the f32-only M5 tensor-core scan is worktree patch `0047`.)

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

**The full automated both-engine harness is `dgx:~/bench/combined_definitive.sh`** (acquires lock, waits for GPU-idle up to 2h, runs MoE then dense for both engines, writes `COMBINED_DEFINITIVE.txt` + `.done`, traps cleanup to kill servers and release lock on exit). This is the reference harness; clone its discipline for any new run.

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

Phase 22 re-verified the patch-series mirror invariant after patch `0055`:
applying every LocalAI `patches/paged/0*.patch` with strict `git apply` on top of
Makefile pin `0ed235ea2c17a19fc8238668653946721ed136fd` produced tree
`5bdbf8ea3d750fe6fa1f85175fd6357d36222edb`, exactly matching fork branch
`localai-paged` HEAD `fb9402661 feat(server): trace speculative batch shapes`.

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
The investigation is already CLOSED in the docs. Concrete first steps:
1. Commit the untracked `patches/paged/0044-feat-paged-fused-gated-RMSNorm-SiLU-gate-mul.patch` into the worktree (it is on the fork as `51168c5ee` and on disk, but shows `??` here).
2. Reconcile the **pin discrepancy** (section 7): the Makefile builds with `0ed235ea`, but README section 7 prose and `VLLM_PARITY_FINAL.md` still say `9d5d882d`. Update the prose to the Makefile value (trust the Makefile when building).
3. Re-run the bit-exact gate on a clean tree to confirm `8cb0ce23` (paged-MoE) / `5951a5b4` (dense) before any release; resolve the `0921716...` open item in section 7.

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
- `dgx:~/llama-paged-fork`, remote `fork git@github.com:mudler/llama.cpp.git`, branch **`localai-paged`**, last clean local canonical HEAD `d9b9be0bee3d7239132bfca05d5b057ff4ee4cc3` ("pad W4A16 A shared tile stride", patch `0050`). The DGX checkout itself may still be dirty and must not be treated as canonical.
- `dgx:~/llama-paged-dev` (experimental dev/build tree), branch **`paged`**, HEAD `a7d439e8ce6990eb09721223c975da4e49d8d136` ("GDN CONFIG C (M8) - bf16 Kc/Qc"). **Dirty** + many untracked profiling artifacts. This tree's `build-cuda/bin/` produced the benchmarked binaries; `COMBINED_DEFINITIVE` recorded `GIT_HEAD=a7d439e` (the M8 bf16 dev config), NOT the fork HEAD. The dev tree carries bf16/hybrid M6/M7/M8 machinery deliberately EXCLUDED from the shipped f32-only series.

### LocalAI worktree
- Path: `/home/mudler/_git/LocalAI/.claude/worktrees/feat+paged-attention`, branch `worktree-feat+paged-attention` (199 ahead, 25 behind origin/master; the ahead count grows with each new commit).
- Backend dir: `backend/cpp/llama-cpp-localai-paged/` (`Makefile` thin wrapper, `package.sh`, `run.sh`, `README.md` ~44 KB canonical, `docs/`, `patches/paged/`).
- `docs/`: `VLLM_PARITY_FINAL.md` (authoritative record), `VLLM_PARITY_LEVER_MAP.md` (working brainstorm, profile-validated section), `DECODE_SERVING_SCOPE.md`, `PREFILL_GEMM_SCOPE.md`, `PREFILL_GEMM_RESULTS.md`, `TENSORCORE_GDN_SCOPE.md`, `TENSORCORE_GDN_BUILD_PLAN.md`, `ACCELERATOR_PORTING_SCOPE.md`, `UPSTREAM_LAYER2_SCOPE.md`, `LOCALAI_LLAMACPP_BACKEND_PLAN.md`, `PAGED_BITEXACT_NOTE.md`, `PATCH_MAINTENANCE.md`, `final_benchmark.csv`, `paged-burst-bench.cpp`, `paged-reclaim-unit.cpp`, 3 PNGs, and this `PARITY_HANDOFF.md`.
- `patches/paged/`: **41** `.patch` files spanning 0001-0050 with intentional gaps (missing 0005, 0026 [dropped ssm_bf16_tau], 0027, 0032, 0036-0039, 0045). Core paged-KV 0001-0012; decode-first scheduler 0013/0016; serving graph reuse 0040/0041; prefill fusions 0042/0044; SSM/GDN decode 0018-0022/0028; MoE NVFP4 quant 0023/0025/0043; FP4-MMA/Marlin scaffolds 0033/0034/0035 (default-off); GDN tensor-core prefill 0031 -> 0046 (geometry gate) -> 0047 (f32-only M5, default-on under paged KV); W4A16 packed metadata is 0048; W4A16 grouped-kernel shape tuning is 0049 and selects `bm32` by default; W4A16 A shared-tile padding is 0050.

### Bench artifacts (DGX)
- `~/bench/COMBINED_DEFINITIVE.txt` (+ `.log`, `.done`, `combined_definitive.sh`, `combined_definitive.out`) - the definitive same-session both-engine run.
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
2. **Both DGX checkouts are dirty** (`gated_delta_net.cu` modified in each), and the current clean local fork HEAD (`d9b9be0be`, patch 0050) differs from the dev-tree HEAD (`a7d439e`, M8 bf16) that actually produced the `COMBINED_DEFINITIVE` numbers.
3. **Worktree patch 0044 is now tracked here.** LocalAI commit `2033086f6` added `patches/paged/0044-feat-paged-fused-gated-RMSNorm-SiLU-gate-mul.patch`; the only current untracked path in this worktree is `.claude/`.
4. **`sm_121a` is not in the worktree build files** - it lives only in the DGX experimental build scripts (`gdn_cc.sh`, `gdn_bv_build.sh`, `paged-build.sh`); mainline uses arch `121`. **UNVERIFIED** whether the shipped CI Dockerfile build path injects `121a` for the FP4-MMA kernels (`Dockerfile.llama-cpp-localai-paged` does not hardcode a CUDA arch).
5. **The `0921716...` paged-MoE md5 open item.** `COMBINED_DEFINITIVE.txt` records `PAGED_GATE_MD5=0921716cd0582b5d15af8c362b811d00` for MoE, but a full doc/patch/`git log -S` grep of the worktree found **no** occurrence of `0921716...` in any committed source; the committed canonical paged-MoE gate is `8cb0ce23`. Treat this as **unreconciled**: the documented, KL-validated paged-MoE gate remains `8cb0ce23`, and any paged-MoE divergence (including `0921716`) must be KL-validated against the f16 reference before being accepted as benign, never on assertion alone. The `0921716` value is **UNVERIFIED** as a sanctioned gate; do not adopt it as canonical without re-running the KL gate. The **dense** run is symmetric: `COMBINED_DEFINITIVE.txt` records `PAGED_GATE_MD5=ecfe924dee6c5622c149f419ff2a6481` for dense, which likewise differs from the canonical dense gate `5951a5b4`. Both CDEF `PAGED_GATE_MD5` values come from the `combined_definitive.sh` harness's own gate command, NOT the canonical bit-exact gate command in section 3.3, which is why they diverge from the committed `8cb0ce23` / `5951a5b4`; neither is a sanctioned gate and both must be KL-validated before being treated as benign.

---

*Status: investigation CLOSED. This handoff is procedure; `VLLM_PARITY_FINAL.md` is the record. The path to parity is datacenter Blackwell, not GB10 kernels.*
