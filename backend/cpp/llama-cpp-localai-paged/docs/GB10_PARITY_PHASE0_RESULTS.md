# GB10 Parity Phase 0 Results

Status: in progress.

## Preflight

- DGX host: `promaxgb10-4ad8`
- Docker containers: `none`
- GPU compute apps: `none`
- GPU lock owner: `FREE released-by-claude-fp4norm-profile 1782828229`
- LocalAI worktree SHA: `d288a0300f36f7c126d62d997809bb03f297a3ac`
- Local llama.cpp fork SHA: `51168c5eee2e35348d9006f0b2fab3dc6e7c01cc`
- DGX artifact directory: `~/bench/reopen_phase0`

## Baseline Runs

Clean prefill baseline artifacts:

- MoE: `~/bench/reopen_phase0/paged_moe_prefill.txt`
- Dense: `~/bench/reopen_phase0/paged_dense_prefill.txt`

MoE paged prefill:

| PP | TG | B | N_KV | T_PP s | S_PP t/s | T_TG s | S_TG t/s | T s | S t/s |
|----|----|---|------|--------|----------|--------|----------|-----|-------|
| 512 | 4 | 32 | 16512 | 7.181 | 2281.66 | 0.355 | 360.57 | 7.536 | 2191.16 |
| 2048 | 4 | 32 | 65664 | 27.131 | 2415.53 | 0.328 | 390.84 | 27.459 | 2391.38 |

Dense paged prefill:

| PP | TG | B | N_KV | T_PP s | S_PP t/s | T_TG s | S_TG t/s | T s | S t/s |
|----|----|---|------|--------|----------|--------|----------|-----|-------|
| 512 | 4 | 32 | 16512 | 16.749 | 978.18 | 0.842 | 152.03 | 17.591 | 938.64 |
| 2048 | 4 | 32 | 65664 | 63.791 | 1027.35 | 0.687 | 186.29 | 64.479 | 1018.38 |

## Decode Difference-Method Reproduction

Paged llama.cpp artifacts:

- `~/bench/reopen_phase0/paged_decode_nsys/paged_moe_n256_ntg16.nsys-rep`
- `~/bench/reopen_phase0/paged_decode_nsys/paged_moe_n256_ntg16.bench.log`
- `~/bench/reopen_phase0/paged_decode_nsys/paged_moe_n256_ntg64.nsys-rep`
- `~/bench/reopen_phase0/paged_decode_nsys/paged_moe_n256_ntg64.bench.log`

Paged llama.cpp rows:

| PP | TG | B | N_KV | T_PP s | S_PP t/s | T_TG s | S_TG t/s | T s | S t/s |
|----|----|---|------|--------|----------|--------|----------|-----|-------|
| 128 | 16 | 256 | 36864 | 14.933 | 2194.39 | 4.502 | 909.80 | 19.435 | 1896.81 |
| 128 | 64 | 256 | 49152 | 14.949 | 2191.96 | 17.924 | 914.09 | 32.873 | 1495.21 |

Paged difference-method decode:

- Token delta: `256 * (64 - 16) = 12288`
- Wall delta: `17.924 - 4.502 = 13.422 s`
- Decode throughput: `915.51 t/s`

vLLM artifacts:

- `~/bench/reopen_phase0/vllm_decode_nsys/vllm_version.txt`
- `~/bench/reopen_phase0/vllm_decode_nsys/dec_npl256_ntg16.nsys-rep`
- `~/bench/reopen_phase0/vllm_decode_nsys/dec_npl256_ntg16.run.log`
- `~/bench/reopen_phase0/vllm_decode_nsys/dec_npl256_ntg16.kern.csv`
- `~/bench/reopen_phase0/vllm_decode_nsys/dec_npl256_ntg16.gpu_trace.csv`
- `~/bench/reopen_phase0/vllm_decode_nsys/dec_npl256_ntg64.nsys-rep`
- `~/bench/reopen_phase0/vllm_decode_nsys/dec_npl256_ntg64.run.log`
- `~/bench/reopen_phase0/vllm_decode_nsys/dec_npl256_ntg64.kern.csv`
- `~/bench/reopen_phase0/vllm_decode_nsys/dec_npl256_ntg64.gpu_trace.csv`

vLLM version: `0.23.0`

vLLM profiled rows:

| NSEQ | GEN | Generated tokens | Wall s | Logged tok/s |
|------|-----|------------------|--------|--------------|
| 256 | 16 | 4096 | 6.195 | 661.2 |
| 256 | 64 | 16384 | 17.607 | 930.5 |

vLLM difference-method decode:

- Token delta: `16384 - 4096 = 12288`
- Wall delta: `17.607 - 6.195 = 11.412 s`
- Decode throughput: `1076.76 t/s`

Clean reproduced paged/vLLM decode ratio: `85.0%`.

## W4A16 Kill-Gate Baseline

Artifacts:

- Default FP4-MMQ: `~/bench/reopen_phase0/w4a16_off.txt`
- Forced W4A16 with debug: `~/bench/reopen_phase0/w4a16_on_thr64.txt`
- Forced W4A16 without debug:
  `~/bench/reopen_phase0/w4a16_on_thr64_nodebug.txt`

Default FP4-MMQ:

| PP | TG | B | N_KV | T_PP s | S_PP t/s | T_TG s | S_TG t/s | T s | S t/s |
|----|----|---|------|--------|----------|--------|----------|-----|-------|
| 512 | 4 | 32 | 16512 | 7.105 | 2306.06 | 0.321 | 399.00 | 7.426 | 2223.68 |
| 2048 | 4 | 32 | 65664 | 27.047 | 2423.00 | 0.329 | 388.89 | 27.377 | 2398.55 |

Forced W4A16, `LLAMA_W4A16_PREFILL_M=64`, debug off:

| PP | TG | B | N_KV | T_PP s | S_PP t/s | T_TG s | S_TG t/s | T s | S t/s |
|----|----|---|------|--------|----------|--------|----------|-----|-------|
| 512 | 4 | 32 | 16512 | 12.517 | 1308.92 | 0.321 | 398.82 | 12.838 | 1286.17 |
| 2048 | 4 | 32 | 65664 | 49.165 | 1332.98 | 0.330 | 387.57 | 49.495 | 1326.67 |

Delta:

- `npp=512`: `-43.2%` S_PP versus default FP4-MMQ.
- `npp=2048`: `-45.0%` S_PP versus default FP4-MMQ.

Debug evidence:

- Forced W4A16 debug run emitted `19200` engagement lines.
- Observed `n_tiles` range: `139..282`.
- Observed `multi_tile_experts` range: `7..21`.

First implementation target:

- Option B: device-side or cached tile metadata.
- Rationale: `w4a16-gemm.cu` currently builds `h_tile_expert`,
  `h_tile_row0`, and `h_tile_rows` on the host, pool-allocates three device
  tile-map buffers, and issues three H2D `cudaMemcpyAsync` calls per grouped
  W4A16 launch. The debug run shows this path is repeatedly exercised across
  many small ragged tile maps. The first fork-first experiment should remove or
  amortize that host-built tile-map path before retuning MMA tile shapes.

## W4A16 Metadata Phase 1

Fork commit: `4b0cc1163cc42dc1c17892fd41ce5ab384ba3e17`
(`feat(paged): pack W4A16 grouped tile metadata`).

LocalAI patch mirror: `0048-feat-paged-pack-W4A16-grouped-tile-metadata.patch`.

Mirror invariant: applying the full LocalAI `patches/paged/*.patch` series to
base pin `0ed235ea2c17a19fc8238668653946721ed136fd` tree-matches fork HEAD
`4b0cc1163cc42dc1c17892fd41ce5ab384ba3e17`.

Artifacts:

- Diff: `~/bench/w4a16_phase1/packed_desc.diff`
- Build mtimes: `~/bench/w4a16_phase1/build_binary_mtimes.txt`
- MoE gate: `~/bench/w4a16_phase1/gate_moe.md5`
- Dense gate: `~/bench/w4a16_phase1/gate_dense.md5`
- Default FP4-MMQ: `~/bench/w4a16_phase1/w4a16_off.txt`
- Packed W4A16: `~/bench/w4a16_phase1/w4a16_on_thr64.txt`

Canonical gates:

- MoE greedy md5: `8cb0ce23777bf55f92f63d0292c756b0` (matched expected)
- Dense greedy md5: `5951a5b4d624ce891e22ab5fca9bc439` (matched expected)

Packed descriptor A/B:

| Path | PP | TG | B | N_KV | T_PP s | S_PP t/s | T_TG s | S_TG t/s | T s | S t/s |
|------|----|----|---|------|--------|----------|--------|----------|-----|-------|
| FP4-MMQ | 512 | 4 | 32 | 16512 | 7.114 | 2303.07 | 0.323 | 396.55 | 7.437 | 2220.32 |
| FP4-MMQ | 2048 | 4 | 32 | 65664 | 27.045 | 2423.23 | 0.331 | 387.14 | 27.376 | 2398.64 |
| W4A16 packed | 512 | 4 | 32 | 16512 | 12.468 | 1314.08 | 0.322 | 397.97 | 12.790 | 1291.04 |
| W4A16 packed | 2048 | 4 | 32 | 65664 | 48.930 | 1339.39 | 0.330 | 387.44 | 49.260 | 1333.00 |

Result:

- Packed descriptors improved forced W4A16 by `+0.39%` at `npp=512` and
  `+0.48%` at `npp=2048` versus the Phase 0 no-debug W4A16 baseline.
- W4A16 remains `-42.9%` at `npp=512` and `-44.7%` at `npp=2048` versus
  same-run default FP4-MMQ.
- Decision: keep patch `0048` as a small simplification, but pivot the next
  W4A16 iteration to the activation cast or MMA/dequant tile body.

## W4A16 Kernel Shape Phase 2

Profile-guided target:

- Phase 1 forced W4A16 profile at `npp=512`: `w4a16_grouped_kernel` dominated
  at `5231.667 ms` (`47.8%`) while `w4a16_cast_act_f32_bf16` was `517.195 ms`
  (`4.7%`).
- Phase 2 therefore targeted grouped-kernel tile shape/body before activation
  cast fusion.

Shape sweep artifacts:

- Build: `~/llama-w4a16-phase2`
- Benchmarks: `~/bench/w4a16_phase2/shape_*.txt`
- Winning profile: `~/bench/w4a16_phase2/profile/w4a16_bm32_npp512.*`

Shape A/B:

| Shape | 512 S_PP t/s | 2048 S_PP t/s | Decision |
|-------|--------------|---------------|----------|
| `base` / `64x128` | 1308.02 | 1339.46 | old baseline |
| `bn256` | 1286.99 | 1311.56 | rejected |
| `bm32` / `32x128` | 1442.99 | 1475.65 | selected |
| `bn64` | 1334.80 | 1362.55 | diagnostic only |
| `stages3` | 1271.01 | 1295.96 | rejected |
| `bn256x16` | 1084.66 | 1100.95 | rejected |

Only `bm32` and the old `base` selector are shipped in patch `0049`. The other
candidate shapes were benchmarked in the Phase 2 build and then deliberately
left out to keep the upstream conflict surface small.

Default-verification after selecting `bm32`:

| PP | TG | B | N_KV | T_PP s | S_PP t/s | T_TG s | S_TG t/s | T s | S t/s |
|----|----|---|------|--------|----------|--------|----------|-----|-------|
| 512 | 4 | 32 | 16512 | 11.360 | 1442.28 | 0.321 | 397.00 | 11.682 | 1413.43 |
| 2048 | 4 | 32 | 65664 | 44.529 | 1471.77 | 0.331 | 386.06 | 44.860 | 1463.75 |

Result:

- `bm32` improves forced W4A16 by about `+10.4%` at `npp=512` and `+10.2%`
  at `npp=2048` versus the old `64x128` shape in the same sweep.
- The profiled `bm32` grouped kernel dropped to `4107.355 ms` (`41.7%`) at
  `npp=512`, from Phase 1's `5231.667 ms` (`47.8%`).
- Canonical post-change gates matched: MoE
  `8cb0ce23777bf55f92f63d0292c756b0`, dense
  `5951a5b4d624ce891e22ab5fca9bc439`.
- Forced W4A16 shape gates matched each other: `LLAMA_W4A16_PREFILL_M=1`
  default `bm32` and `LLAMA_W4A16_SHAPE=base` both produced
  `07db32c2bcb78d17a43ed18bc22705cd` on the canonical gate prompt.
- Forced W4A16 `MUL_MAT_ID` op checks passed for both shapes:
  `test-backend-ops test -b CUDA0 -o MUL_MAT_ID -j 1` reported `806/806`
  for default `bm32` and `806/806` for `base`.
- Decision: make `bm32` the W4A16 default shape while keeping
  `LLAMA_W4A16_SHAPE=base` for old-shape A/B and leaving other candidates as
  diagnostics.

Mirror invariant after patch `0049`:

- Applying all 40 LocalAI `patches/paged/*.patch` files to base pin
  `0ed235ea2c17a19fc8238668653946721ed136fd` tree-matches fork HEAD
  `7dfa0e17548c5f04f83d2cc2a057b0a9941b599a`.
- Tree hash after patch application: `dabe225efbf20ec047b8309d1e1f19b34fc7c5c9`.

## W4A16 Scale Broadcast Phase 3

Goal: reduce duplicate FP4 scale conversion inside `w4a16_grouped_kernel` by
having one lane per 4-lane group convert the `ue4m3` scale and broadcast it with
`__shfl_sync`.

Artifacts:

- Build: `~/llama-w4a16-phase3`
- Logs: `~/bench/w4a16_phase3`

Gates:

- Canonical paged MoE md5: `8cb0ce23777bf55f92f63d0292c756b0`.
- Canonical dense md5: `5951a5b4d624ce891e22ab5fca9bc439`.
- Forced W4A16 `bm32` and old `base` shape md5s matched each other:
  `07db32c2bcb78d17a43ed18bc22705cd`.
- Forced W4A16 `MUL_MAT_ID`: `806/806` on CUDA0.

Performance:

| Shape | 512 S_PP t/s | 2048 S_PP t/s | Decision |
|-------|--------------|---------------|----------|
| Phase 2 `bm32` | 1442.28 | 1471.77 | baseline |
| Phase 3 scale-broadcast `bm32` | 1392.46 | 1422.74 | rejected |
| Phase 2 `base` | 1310.13 | 1336.02 | baseline |
| Phase 3 scale-broadcast `base` | 1201.69 | 1221.25 | rejected |

Result:

- Rejected. No fork commit and no LocalAI patch `0050`.
- The local fork experiment was reverted.
- Do not retry this exact scale-broadcast approach; on GB10 the shuffle and/or
  scheduling cost exceeds the saved duplicate scale conversion.

## W4A16 Shared-Memory Padding Phase 4

Goal: reduce bank pressure in `w4a16_grouped_kernel` by padding the A operand
shared-memory row stride while preserving math order and launch shape.

Fork commit: `d9b9be0bee3d7239132bfca05d5b057ff4ee4cc3`
(`feat(paged): pad W4A16 A shared tile stride`).

LocalAI patch mirror: `0050-feat-paged-pad-W4A16-A-shared-tile-stride.patch`.

Artifacts:

- Build: `~/llama-w4a16-phase4`
- Logs: `~/bench/w4a16_phase4`

Gates:

- Canonical paged MoE md5: `8cb0ce23777bf55f92f63d0292c756b0`.
- Canonical dense md5: `5951a5b4d624ce891e22ab5fca9bc439`.
- Forced W4A16 `bm32` and old `base` shape md5s matched each other:
  `07db32c2bcb78d17a43ed18bc22705cd`.
- Forced W4A16 `MUL_MAT_ID`: `806/806` on CUDA0.

Performance:

| Shape | 512 S_PP t/s | 2048 S_PP t/s | Decision |
|-------|--------------|---------------|----------|
| Phase 2 `bm32` | 1442.28 | 1471.77 | baseline |
| Phase 4 A-pad `bm32` | 1466.62 | 1495.93 | selected |
| Phase 2 `base` | 1310.13 | 1336.02 | baseline |
| Phase 4 A-pad `base` | 1337.88 | 1364.98 | positive diagnostic |

Result:

- Kept. Default W4A16 `bm32` improves another `+1.7%` at `npp=512` and
  `+1.6%` at `npp=2048` versus Phase 2.
- Applying all 41 LocalAI `patches/paged/*.patch` files to base pin
  `0ed235ea2c17a19fc8238668653946721ed136fd` tree-matches fork HEAD
  `d9b9be0bee3d7239132bfca05d5b057ff4ee4cc3`.
- Tree hash after patch application: `8fcb151e0620fd0fc82b80c04318e5c34320b087`.

## W4A16 Wq Padding Phase 5

Goal: test whether padding the quantized-weight shared-memory row stride gives
another low-conflict W4A16 grouped-kernel body win after `0050`.

Artifacts:

- Build: `~/llama-w4a16-phase5`
- Logs: `~/bench/w4a16_phase5`

Gates:

- Canonical paged MoE md5: `8cb0ce23777bf55f92f63d0292c756b0`.
- Canonical dense md5: `5951a5b4d624ce891e22ab5fca9bc439`.
- Forced W4A16 `bm32` and old `base` shape md5s matched each other:
  `07db32c2bcb78d17a43ed18bc22705cd`.
- Forced W4A16 `MUL_MAT_ID`: `806/806` on CUDA0.

Performance:

| Shape | 512 S_PP t/s | 2048 S_PP t/s | Decision |
|-------|--------------|---------------|----------|
| Phase 4 A-pad `bm32` | 1466.62 | 1495.93 | baseline |
| Phase 5 Wq-pad `bm32` | 1472.36 | 1504.82 | rejected: below 1% gate |
| Phase 4 A-pad `base` | 1337.88 | 1364.98 | baseline |
| Phase 5 Wq-pad `base` | 1337.70 | 1368.48 | diagnostic |

Result:

- Rejected. No fork commit and no LocalAI patch was created for that experiment.
- The local fork experiment was reverted.
- Do not ship Wq padding alone; the measured `+0.4%` / `+0.6%` default-shape
  gain is below the maintenance threshold.

## Clean Build

First clean build attempt:

- PID: `625392`
- Source checkout: `~/llama-paged-reopen-clean`
- Result: failed during CMake configure.
- Root cause: `nvcc` was not discoverable on PATH. CUDA headers were found under
  `/usr/local/cuda/targets/sbsa-linux/include`, and the compiler exists at
  `/usr/local/cuda-13.0/bin/nvcc`.
- Retry plan: rebuild the clean checkout with
  `CUDACXX=/usr/local/cuda-13.0/bin/nvcc`.

Second clean build attempt:

- PID: `631100`
- Source checkout: `~/llama-paged-reopen-clean`
- Source status: `## HEAD (no branch)`
- Build HEAD: `51168c5eee2e35348d9006f0b2fab3dc6e7c01cc`
- CUDA compiler: `/usr/local/cuda-13.0/bin/nvcc`
- Result: succeeded.
- Binary mtimes:
  - `build-cuda/bin/llama-server 2026-06-30 22:14:34.091312112 +0200`
  - `build-cuda/bin/llama-batched-bench 2026-06-30 22:14:35.156287566 +0200`
  - `build-cuda/bin/llama-completion 2026-06-30 22:14:37.095750242 +0200`
  - `build-cuda/bin/test-backend-ops 2026-06-30 22:14:47.360078186 +0200`

## Canonical Gates

- MoE greedy md5: `8cb0ce23777bf55f92f63d0292c756b0` (matched expected)
- Dense greedy md5: `5951a5b4d624ce891e22ab5fca9bc439` (matched expected)
- Artifacts:
  - `~/bench/reopen_phase0/gate_moe.txt`
  - `~/bench/reopen_phase0/gate_moe.md5`
  - `~/bench/reopen_phase0/gate_dense.txt`
  - `~/bench/reopen_phase0/gate_dense.md5`

## Source Provenance

- Local llama.cpp fork: `/home/mudler/_git/llama.cpp`
- Branch: `localai-paged`
- Working tree: clean after fork commit `d9b9be0bee3d7239132bfca05d5b057ff4ee4cc3`
- Phase 0 HEAD: `51168c5eee2e35348d9006f0b2fab3dc6e7c01cc`
- Current HEAD: `cd56cf037379b084d6bb0ed47db8b785c828be86`
- Base pin: `0ed235ea2c17a19fc8238668653946721ed136fd`
- Merge-base with base pin: `0ed235ea2c17a19fc8238668653946721ed136fd`
- LocalAI patch count: `38` at Phase 0; current mirror count is `42` after
  patch `0051`.
- LocalAI patch mirror: applies cleanly to the base pin and tree-matches fork
  HEAD.
- Tree hash after patch application: `623b7cb008a929455ca3d9deae35494c02622fef`

## Existing Artifact Gap Review

Read-only DGX artifact inspection was performed after confirming the machine was
idle: `docker ps` returned no running containers,
`nvidia-smi --query-compute-apps` returned no compute-app rows, and
`~/gpu_bench_lock/owner` read
`FREE released-by-claude-fp4norm-profile 1782828229`.

Existing paged llama.cpp decode and prefill numbers are supported by
`/home/mudler/bench/COMBINED_DEFINITIVE.txt`: MoE paged prefill lines 13-18,
MoE paged serving decode lines 23-26, dense paged prefill lines 43-48, and
dense paged serving decode lines 53-56. Supporting comparison artifacts are
`/home/mudler/bench/STOCK3WAY.txt`, `/home/mudler/bench/PREFILL_KNOB.txt`,
`/home/mudler/bench/DEFINITIVE_S3ab.txt`, and the adjacent raw logs.

No self-contained vLLM `1078 t/s` GPU-steady `ntg16`/`ntg64`
difference-method artifact was found. The available vLLM evidence is
serving-run output in `/home/mudler/bench/COMBINED_DEFINITIVE.txt` plus
nsys/run artifacts under `/home/mudler/bench/profgap/` and
`/home/mudler/bench/postssm_decomp/`; these do not form a packaged
`ntg16`/`ntg64` difference-method report.

W4A16/Marlin evidence exists in `/home/mudler/bench/vllm_prefix.log`,
`/home/mudler/bench/profgap/vllm_moe_decode.run.log`, and
`/home/mudler/bench/marlin_gate/kl_marlin.log`.
`/home/mudler/llama-paged-dev/LEVER3_ACTQUANT_FUSION_RESULTS.md` records the
parity conclusion: W4A16/Marlin is a precision-change lever, not a bit-exact
llama.cpp parity lever.

GDN M5/M8 evidence exists in `/home/mudler/bench/COMBINED_DEFINITIVE.txt`
(`GDN CONFIG C (M8)` and production defaults noting GDN M5),
`/home/mudler/llama-paged-dev/LEVER1_GATHER_RESULTS.md`, and
`/home/mudler/llama-paged-dev/CONV_STATE_FUSION_RESULTS.md`.

S3 evidence exists in `/home/mudler/bench/DEFINITIVE_S3ab.txt`; that A/B shows
S3-on was worse unless paired with `LLAMA_PAGED_PREFILL_PERIOD=1`, matching
`/home/mudler/bench/COMBINED_DEFINITIVE.txt` where S3 is recorded as off by
default. No separate self-contained adaptive-scheduling proof artifact was
found beyond the S3 and prefill-knob artifacts.

## Open Items

## Phase 6 Serving nsys Classifier

Exact fork head `d9b9be0bee3d7239132bfca05d5b057ff4ee4cc3` was mirrored to
`/home/mudler/llama-phase6-source` on DGX and rebuilt with CUDA Release,
`CMAKE_CUDA_COMPILER=/usr/local/cuda-13.0/bin/nvcc`, and
`CMAKE_CUDA_ARCHITECTURES=121`.

Pre-profile gates passed:

- MoE greedy md5: `8cb0ce23777bf55f92f63d0292c756b0`.
- Dense greedy md5: `5951a5b4d624ce891e22ab5fca9bc439`.

Serving nsys artifacts:

- llama.cpp:
  `/home/mudler/bench/phase6_serving_nsys/llama_server_n128/`.
- vLLM:
  `/home/mudler/bench/phase6_serving_nsys/vllm_server_n128/`.

Same h2h shape (`n=128`, `ptok=128`, `gen=128`) under nsys:

| Engine | decode tok/s/seq | decode agg tok/s | prefill tok/s |
|--------|------------------|------------------|---------------|
| llama.cpp | 4.05 | 591.0 | 1567.4 |
| vLLM | 6.95 | 961.1 | 5073.6 |

llama.cpp bucket highlights:

- `gated_delta_net_cuda`: 33.7% GPU kernel time, 10.21s.
- NVFP4 `mul_mat_q`: 24.3% + 5.5% for the largest grouped variants, 9.04s
  combined.
- `quantize_mmq_nvfp4`: 2.7%, 0.81s.
- `flash_attn_tile`: 1.3%, 0.38s.
- CUDA API: `cudaStreamSynchronize` 76.5% API time, 23.66s over 106585 calls;
  8028 synchronizes followed `cudaMemcpyAsync` and summed 21.41s.

vLLM bucket highlights:

- `fused_recurrent_gated_delta_rule_packed_decode_kernel`: 16.6%, 8.95s.
- `marlin_moe_wna16::Marlin`: 11.9% plus smaller Marlin-MoE variants.
- `flash_fwd_splitkv_kernel`: visible split-K FA decode rows at 0.6% + 0.1%.
- The vLLM delayed profile still contains startup/module-load API noise; prefer
  h2h and GPU kernel buckets over API percentages for vLLM.

Rejected Phase 6 sampler experiment:

- Patch idea: in backend distribution sampling, skip the random uniform upload
  when prior backend filters already collapsed candidates to one token
  (`temperature=0` path).
- Gates passed:
  - MoE md5 `8cb0ce23777bf55f92f63d0292c756b0`.
  - Dense md5 `5951a5b4d624ce891e22ab5fca9bc439`.
  - `MUL_MAT_ID`: `806/806` on CUDA0.
- Serving A/B did not clear the performance gate: no-nsys reps were `4.19` and
  `3.55` tok/s/seq. The fork patch was reverted; no commit and no LocalAI patch
  were created.

Next measured target:

- H3 is elevated above another W4A16/kernel-shape pass: llama.cpp spends 33.7%
  of GPU time in GDN decode versus vLLM's 16.6%, and vLLM remains 1.63x faster
  on aggregate decode for the same serving shape. Use existing `GDN_NW` and
  `GDN_CPW` controls to grid-search live-width-adaptive GDN launch parameters
  before changing source.

## Phase 6 GDN Narrow-Serving Env Grid

Artifact: `/home/mudler/bench/phase6_serving_nsys/gdn_grid/`.

Clean binaries were rebuilt after reverting the rejected sampler experiment.
Grid shape was `n=128`, `ptok=128`, `gen=64` to keep each isolated server run
bounded.

| Setting | decode tok/s/seq | decode agg tok/s | Decision |
|---------|------------------|------------------|----------|
| default | 3.91 | 647.9 | baseline |
| `GDN_NW=4 GDN_CPW=1` | 3.80 | 628.9 | reject |
| `GDN_NW=8 GDN_CPW=2` | 3.94 | 624.5 | reject |
| `GDN_NW=8 GDN_CPW=4` | 3.91 | 647.6 | reject |
| `GDN_NW=8 GDN_CPW=8` | 4.00 | 636.9 | no material win |
| `GDN_NW=16 GDN_CPW=4` | 3.85 | 637.5 | reject |
| `GDN_NW=16 GDN_CPW=8` | 3.96 | 652.0 | no material win |

Result:

- Rejected as an env-only lever. Existing GDN geometry variants are too close in
  this serving gate to justify a source change.
- Next focus moves back to the largest differentiating kernel bucket:
  llama.cpp's NVFP4 grouped `mul_mat_q` bucket (~30% GPU time) versus vLLM's
  Marlin-MoE bucket.

## Phase 6 MoE MMQ Tile Env Grid

Artifact: `/home/mudler/bench/phase6_serving_nsys/mmq_grid/`.

Shape: `n=128`, `ptok=128`, `gen=64`.

| Setting | decode tok/s/seq | decode agg tok/s | Decision |
|---------|------------------|------------------|----------|
| default | 3.90 | 645.3 | baseline |
| `LLAMA_MOE_AUTO_TILE=0` | 3.90 | 655.3 | tied/no material win |
| `LLAMA_MOE_DECODE_TILE=32` | 3.82 | 635.9 | reject |
| `LLAMA_MOE_DECODE_TILE=48` | 3.81 | 637.3 | reject |
| `LLAMA_MOE_DECODE_TILE=96` | 3.84 | 642.8 | reject |
| `LLAMA_MOE_DECODE_TILE=128` | 3.84 | 640.6 | reject |
| `LLAMA_MOE_MMQ_X=32` | 3.76 | 642.0 | reject; prefill worsened |

Result:

- Rejected as an env-only lever. Existing grouped-MMQ tile and auto-selector
  knobs do not materially close the serving gap.
- A source patch that only retunes the current tile selector is not justified.
  The next useful MoE lever would need a structural change closer to vLLM's
  Marlin-MoE/fused-MoE shape, or the work should move to the synchronous
  serving input/sampler path with a measurable non-greedy workload.

## Open Items

- No current env-only lever clears the serving performance gate. Scope the next
  source candidate against either structural MoE decode fusion or async serving
  input/sampler uploads, with a workload that proves the target bucket matters.
- Phase 7 must keep the canonical MoE and dense md5 gates as the first
  inference-safety check before any performance result is accepted.

## Phase 7 Source-Candidate Test Gate

Fork commit `cd56cf037379b084d6bb0ed47db8b785c828be86` added patch
`0051-test-paged-cover-MoE-swiglu-down-chain.patch`. This is a test-only patch;
it does not change the production inference path.

Fresh DGX gates from `/home/mudler/bench/phase7_source_scope/`:

- MoE greedy md5: `8cb0ce23777bf55f92f63d0292c756b0`.
- Dense greedy md5: `5951a5b4d624ce891e22ab5fca9bc439`.
- Baseline `MUL_MAT_ID`: `806/806`.
- New `MOE_SWIGLU_DOWN`: `7/7`.

The new gate covers the merged MoE gate_up -> SWIGLU -> down-projection graph
shape needed before attempting a batched NVFP4 down-input quantization fusion.

## Phase 7 SWIGLU-Down Fusion Candidate Rejected

Attempted candidate: fuse `GGML_OP_GLU(SWIGLU)` into the NVFP4 activation
quantization feeding the MoE down-projection `MUL_MAT_ID`, while keeping the
existing grouped-MMQ kernel. The patch was kept behind
`GGML_CUDA_FUSE_SWIGLU_DOWN_MMQ=1` during validation.

DGX artifacts:

- `/home/mudler/bench/phase7_source_scope/test_backend_ops_moe_swiglu_down_optin.txt`
- `/home/mudler/bench/phase7_source_scope/test_backend_ops_mul_mat_id_after_optin.txt`
- `/home/mudler/bench/phase7_source_scope/default_gates_after_optin/`
- `/home/mudler/bench/phase7_source_scope/optin_gates/`
- `/home/mudler/bench/phase7_source_scope/serving_ab/`

Correctness and inference gates:

- Forced fusion `MOE_SWIGLU_DOWN`: `7/7`.
- Broad default `MUL_MAT_ID`: `806/806`.
- Default md5 after opt-in gating stayed canonical:
  - MoE `8cb0ce23777bf55f92f63d0292c756b0`.
  - Dense `5951a5b4d624ce891e22ab5fca9bc439`.
- Opt-in fusion md5:
  - MoE `07db32c2bcb78d17a43ed18bc22705cd`.
  - Dense `5951a5b4d624ce891e22ab5fca9bc439`.

Serving A/B (`n=128`, `ptok=128`, `gen=64`, `/v1/completions`, `--no-cache`):

| path | decode tok/s/seq | decode agg tok/s | prefill tok/s | verdict |
|------|------------------|------------------|---------------|---------|
| default | 3.92 | 657.1 | 1456.0 | baseline |
| `GGML_CUDA_FUSE_SWIGLU_DOWN_MMQ=1` | 3.88 | 667.4 | 1462.9 | reject; md5 drift and flat A/B |

Result:

- Rejected as a production patch. The opt-in path changes the paged-MoE md5
  into the non-paged namespace and does not materially improve serving.
- Root-cause note for future attempts: the first fused-op gate failed because
  the fused quantizer used compact GLU-output strides to read split `gate`/`up`
  views. Split views stride over the merged gate/up tensor; using source-view
  strides fixed the op gate but not the end-to-end md5 drift.

## Phase 7 Weighted-Combine Test Gate

Fork commit `3ef7eb9e4d` added patch
`0052-test-paged-cover-MoE-weighted-combine-chain.patch`. This is a test-only
patch; it does not change the production inference path.

The new `MOE_WEIGHTED_COMBINE` whole-graph gate covers:

`down MUL_MAT_ID -> router-weight ggml_mul -> rank-ordered expert views/adds`.

DGX artifact:

- `/home/mudler/bench/phase7_source_scope/test_backend_ops_moe_weighted_combine_green.txt`

DGX result:

- `test-backend-ops test -b CUDA0 -o MOE_WEIGHTED_COMBINE -j 1`: `7/7`.

This gate is the correctness target for the next candidate: a deterministic
post-down MoE weighted-combine fusion that preserves current f32 product and
rank-order add semantics while avoiding the rejected SWIGLU/FP4-quantization
shortcut.

## Phase 7 Weighted-Combine Fusion Candidate Rejected

Attempted candidate: fuse the post-down MoE router-weight multiply and
rank-ordered add fan-in:

`ffn_moe_down -> ggml_mul(experts, weights) -> VIEW ranks -> ADD fan-in`.

The candidate was fork-first, default-on during validation, and had a rollback
env switch: `LLAMA_MOE_NO_WEIGHTED_COMBINE_FUSION=1`.

DGX artifacts:

- `/home/mudler/bench/phase7_source_scope/test_backend_ops_moe_weighted_combine_orderfix.txt`
- `/home/mudler/bench/phase7_source_scope/test_backend_ops_mul_mat_id_weighted_combine_orderfix.txt`
- `/home/mudler/bench/phase7_source_scope/weighted_combine_orderfix_gates_chat/`
- `/home/mudler/bench/phase7_source_scope/weighted_combine_orderfix_nsys_completion/`
- `/home/mudler/bench/phase7_source_scope/weighted_combine_orderfix_serving_ab/`
- Rejected diff:
  `/home/mudler/bench/phase7_source_scope/rejected-phase7-moe-weighted-combine-fusion.diff`

Correctness and inference gates:

- `MOE_WEIGHTED_COMBINE`: `7/7`.
- Broad `MUL_MAT_ID`: `806/806`.
- Canonical transcript md5:
  - MoE `8cb0ce23777bf55f92f63d0292c756b0`.
  - Dense `5951a5b4d624ce891e22ab5fca9bc439`.

Nsight proof:

- Disabled run: no `k_moe_weighted_combine` kernels.
- Fused run: `110` `k_moe_weighted_combine` launches.

Serving A/B (`n=128`, `ptok=128`, `gen=64`, `/v1/completions`):

| path | decode tok/s/seq | decode agg tok/s | prefill tok/s | verdict |
|------|------------------|------------------|---------------|---------|
| `LLAMA_MOE_NO_WEIGHTED_COMBINE_FUSION=1` | 2.63 | 417.5 | 1345.2 | baseline |
| fused default | 2.63 | 417.0 | 1346.9 | reject; kernel fires but A/B is flat |

Result:

- Rejected as a production patch. The patch is md5-safe and the kernel fires,
  but it does not improve the bounded serving workload. Keep patch `0052` as a
  useful regression gate; do not retry this exact fan-in-only fusion unless a
  fresh profile shows the weighted/add fan-in as a material bucket.

## Phase 8 Ragged MoE Dispatch Scope

Plan: `docs/superpowers/plans/2026-07-01-serving-ragged-moe-phase8.md`.

The next candidate is profile-gated before source work:

- Target a fused routed-expert `MUL_MAT_ID` dispatch path for ragged serving
  decode, not another post-down fan-in fusion.
- First decompose live llama.cpp and vLLM MoE serving at `n=128`, `ptok=128`,
  `gen=64` with Nsight and `/home/mudler/bench/bucket.py`.
- Promote only if `mm_ids_helper`, activation quant/gather, grouped MMQ, or
  related MoE dispatch rows are material and not hidden by GDN or FA.
- Keep the backend-sampling/logit-bias upload cache as a non-default follow-up;
  it requires `--backend-sampling` and request `backend_sampling: true` with
  non-empty `logit_bias` or `ignore_eos`.

Required promotion gates remain:

- MoE md5 `8cb0ce23777bf55f92f63d0292c756b0`.
- Dense md5 `5951a5b4d624ce891e22ab5fca9bc439`.
- `MUL_MAT_ID`: `806/806` on CUDA0.
- Any fused dispatch prototype must start default-off behind
  `LLAMA_MOE_FUSED_DISPATCH=1`.

Profile-gate result:

- Clean llama.cpp artifact:
  `/home/mudler/bench/phase8_ragged_moe_dispatch/llama_n128_clean/`.
- vLLM artifact:
  `/home/mudler/bench/phase8_ragged_moe_dispatch/vllm_n128/`.
- A stale first llama profile under `llama_n128/` is intentionally ignored
  because the binary still contained the rejected weighted-combine kernel before
  the clean-source rebuild.

Throughput:

| Engine | decode tok/s/seq | decode agg tok/s | prefill tok/s |
|--------|------------------|------------------|---------------|
| llama.cpp | 2.70 | 412.1 | 1368.3 |
| vLLM | 7.02 | 1036.6 | 5277.7 |

llama.cpp bucket highlights from the clean profile:

- GDN: `4680.27 ms`, `38.12%`.
- `mmq_nvfp4`: `2745.11 ms`, `22.36%`.
- `act_quant`: `441.42 ms`, `3.60%`.
- MoE dispatch: `183.67 ms`, `1.50%`.
- `ew_add` fan-in: `280.15 ms`, `2.28%`.

Decision:

- Promote to a test-only ragged `MUL_MAT_ID` gate before production source.
- Do not implement fused dispatch yet. Standalone `mm_ids`/`gather_mmq` helper
  time is small; a source patch must reduce the larger grouped-MMQ/activation
  movement bucket and still beat the `+5%` serving A/B gate.

## Phase 8 Ragged MoE Dispatch Test Gate

Fork commit `e21732fc4` added patch
`0053-test-paged-cover-ragged-MoE-dispatch.patch`. This is a test-only patch;
it does not change the production inference path.

The new `MUL_MAT_ID_RAGGED_MOE` gate covers:

- one small F32 wiring case,
- NVFP4 with `n_mats=256`, `n_used=8`, `m=768`, `k=2048`,
  `n in {1, 8, 33, 128, 257}`,
- deterministic unique top-k ids skewed toward hot experts, including expert
  `255`, leaving many experts empty.

DGX artifact:

- `/home/mudler/bench/phase8_ragged_moe_dispatch/test_backend_ops_mul_mat_id_ragged_moe_fixed.txt`

DGX result:

- `test-backend-ops test -b CUDA0 -o MUL_MAT_ID_RAGGED_MOE -j 1`: `6/6`.

Debug note:

- The first version of the gate failed because the deterministic IDs produced
  duplicate expert IDs within token 0. That is not a valid top-k routing shape
  and caused a CPU/CUDA mismatch followed by a CUDA fault. The committed gate
  preserves unique expert IDs per token while keeping cross-token load skew.

Production-source decision:

- Do not start a Phase 8 production CUDA patch yet.
- Code inspection found that the existing native-FP4 MoE path already de-dups
  broadcast activation quantization when `ne11 == 1`, then gathers FP4 blocks
  before grouped MMQ.
- The measured helper rows are small (`mm_ids=0.66%`, `gather_mmq=0.42%`).
  A metadata-only fused-dispatch hook would not plausibly clear the `+5%`
  serving A/B gate.
- A future source candidate must reduce `mmq_nvfp4` (`22.36%`) or `act_quant`
  (`3.60%`) directly, without D2H id readback, new stream synchronizations, or
  md5 drift.

## Phase 9 MTP Draft Smoke Gate

Phase 9 challenged the older "MTP absent" assumption. The current fork has
Qwen3.5/3.6 `draft-mtp` support and the DGX MoE GGUF contains MTP metadata and
tensors:

- `qwen35moe.nextn_predict_layers`
- `blk.40.nextn.eh_proj.weight`
- `blk.40.nextn.shared_head_norm.weight`
- `blk.40.nextn.enorm.weight`
- `blk.40.nextn.hnorm.weight`

Smoke artifacts:

- Failing default pre-patch:
  `/home/mudler/bench/phase9_mtp_smoke/mtp_smoke.err`.
- Passing explicit CPU-sampled draft:
  `/home/mudler/bench/phase9_mtp_smoke/mtp_smoke_no_backend_sampling.err`.
- Passing default after patch:
  `/home/mudler/bench/phase9_mtp_smoke/mtp_smoke_default_after_patch.err`.

Finding:

- `draft-mtp` runs with the current model when backend draft sampling is off.
- The default path previously emitted:
  `backend sampling requires at most one output token per sequence (seq_id 0 had 2)`.
- Patch `0054-fix-speculative-disable-backend-sampling-for-MTP-drafts.patch`
  disables backend draft sampling inside the MTP implementation until the
  backend sampler supports multi-output verification batches.

DGX smoke after patch:

- `rc=0`.
- Warning emitted:
  `backend draft sampling is disabled for MTP`.
- `n_drafted=5`, `n_accept=4`, acceptance `80.000%`.
- Output tail:
  `The capital of France is Paris, a city renowned for its rich history`.

Normal inference gates after patch:

- MoE md5: `8cb0ce23777bf55f92f63d0292c756b0`.
- Dense md5: `5951a5b4d624ce891e22ab5fca9bc439`.

Decision:

- Keep Phase 9 as an opt-in speculative smoke/fix only.
- Do not enable MTP by default in LocalAI or llama-server.
- Do not benchmark MTP as a parity win until a serving/API phase adds rollback
  gates for hybrid SSM/KV state and measures target verification throughput.

## Phase 14 MTP Rollback and Inference-Safety Gate

Phase 14 tested the missing safety question from Phase 9: whether MTP
speculative rejection can run against the actual Qwen3.6 MoE GGUF without
corrupting paged KV or recurrent GDN state.

Artifacts:

- `/home/mudler/bench/phase14_mtp_rollback/recurrent_rollback.err`
- `/home/mudler/bench/phase14_mtp_rollback/mtp_greedy_equiv.err`
- `/home/mudler/bench/phase14_mtp_rollback/completion_nocnv_n{8,16,24,32,48}.out`
- `/home/mudler/bench/phase14_mtp_rollback/mtp_n{8,16,24,48}.out`
- `/home/mudler/bench/paged_inference_gates/20260701_041117`

Safety evidence:

- `test-recurrent-state-rollback` on
  `/home/mudler/bench/q36-35b-a3b-nvfp4.gguf` exited `0` and logged
  `recurrent rollback checkpoint restored successfully`.
- MTP stderr logged bounded recurrent rollback support:
  `the context supports bounded partial sequence removal`.
- MTP partial rejection occurred at `temp=0`:
  `n_drafted=39`, `n_accept=20`, `accept=51.282%`.
- The backend sampler multi-output error stayed absent; the expected
  `backend draft sampling is disabled for MTP` warning was present.
- Raw greedy text was prefix-equivalent after normalization for
  `n=8,16,24,32,48`; no first differing token was found. Exact transcript md5
  is not used for this cross-frontend gate because `llama-speculative-simple`
  emits accepted token groups and can overrun `llama-completion -no-cnv` for
  the same `-n`.

Normal inference gates after Phase 14:

- MoE md5: `8cb0ce23777bf55f92f63d0292c756b0`.
- Dense md5: `5951a5b4d624ce891e22ab5fca9bc439`.
- `MUL_MAT_ID`: `806/806`, `Backend CUDA0: OK`.

Decision:

- MTP rollback safety is green enough to scope a Phase 15 serving/API
  throughput gate.
- Do not enable MTP by default.
- Do not count MTP as a GB10 speed-parity win until serving results show useful
  target-verification throughput under the canonical inference gates.

## Phase 15 MTP Serving Throughput Gate

Phase 15 measured the direct `llama-server` serving path after Phase 14 proved
rollback safety. The test compared two same-shape arms:

- baseline: no speculative decoding,
- MTP: `--spec-type draft-mtp --spec-draft-n-max 3
  --no-spec-draft-backend-sampling`.

Artifact:

- `/home/mudler/bench/phase15_mtp_serving/20260701_042005`

Harness:

- `backend/cpp/llama-cpp-localai-paged/paged-mtp-serving-bench.sh`
- `NPL="8 32 128" PTOK=128 GEN=128 CTX=131072 PARALLEL=128`
- client: `/home/mudler/bench/h2h_cli3.py` against `/v1/completions`

Result:

| arm | n | agg t/s | decode agg t/s | decode per-seq t/s | TTFT mean ms | wall s |
|---|---:|---:|---:|---:|---:|---:|
| baseline | 8 | 192.5 | 247.8 | 30.70 | 1181.1 | 5.318 |
| MTP | 8 | 92.9 | 109.8 | 14.26 | 1691.5 | 11.017 |
| baseline | 32 | 305.4 | 406.0 | 12.02 | 2762.2 | 13.412 |
| MTP | 32 | 95.8 | 111.7 | 3.61 | 4545.6 | 42.727 |
| baseline | 128 | 429.5 | 662.4 | 4.31 | 7747.2 | 38.144 |
| MTP | 128 | 100.3 | 138.5 | 0.97 | 20385.7 | 163.289 |

MTP did actually run:

- server initialized `draft-mtp` with bounded partial sequence removal,
- response/server timings included draft counters,
- server log tail included `#gen tokens = 17293`, `#acc tokens = 15493`.

Normal inference gates before and after the A/B:

- MoE md5: `8cb0ce23777bf55f92f63d0292c756b0`.
- Dense md5: `5951a5b4d624ce891e22ab5fca9bc439`.
- `MUL_MAT_ID`: `806/806`, `Backend CUDA0: OK`.

Decision:

- Reject current `llama-server` MTP as a GB10 serving parity lever.
- Do not enable MTP by default in LocalAI or llama-server.
- Do not tune `spec-draft-n-max` blindly. The regression is large enough that
  the next MTP phase, if any, must start with graph/batch-shape profiling.

Likely root cause:

- Baseline serving preserved heavy graph reuse (`graphs reused = 361` in the
  `n=128` tail).
- MTP serving showed `graphs reused = 1` and high per-slot eval time at high
  concurrency.
- The working hypothesis is that MTP verification/draft batch shape churn
  defeats the paged decode graph-reuse wins, so extra verification dominates
  despite high draft acceptance.

## Phase 16 MTP Graph-Reuse Profile

Phase 16 profiled the Phase 15 hypothesis with
`nsys --cuda-graph-trace=node` on a smaller direct serving shape:

- server: `-c 32768 -b 2048 -ub 512 --parallel 32`,
- client: `h2h_cli3.py -n 8 --ptok 64 --gen 64`,
- arms: baseline vs `--spec-type draft-mtp --spec-draft-n-max 3`.

Artifact:

- `/home/mudler/bench/phase16_mtp_graph_profile/20260701_043016`

Result:

| arm | decode agg t/s | decode per-seq t/s | wall s | graph reuse |
|---|---:|---:|---:|---:|
| baseline | 230.5 | 28.07 | 3.523 | `graphs reused = 62` |
| MTP | 97.7 | 12.83 | 7.049 | `graphs reused = 1` |

MTP drafted and accepted tokens:

- `draft acceptance = 0.81481 (44 accepted / 54 generated)`,
- `#gen tokens = 460`, `#acc tokens = 346`.

Nsight kernel summaries also show materially more GPU work in the MTP run:
roughly `5.89 s` top-level GPU kernel time versus `2.59 s` for the baseline
small profile.

Decision:

- Phase 16 supports the Phase 15 root-cause hypothesis: current MTP serving
  defeats the paged decode graph-reuse advantage and increases GPU work.
- A future source phase must start at speculative verification batch shapes and
  graph-reuse keys, not at MTP draft-length tuning.

## Phase 10 GDN C32 Slab Baseline and Source Check

Phase 10 starts a separate GDN prefill path; it does not reopen the rejected
decode `GDN_NW/GDN_CPW` grid.

Current M5 baseline artifacts:

- `/home/mudler/bench/phase10_gdn_c32_slab/m5_baseline/paged_moe_prefill.txt`
- `/home/mudler/bench/phase10_gdn_c32_slab/m5_baseline/paged_dense_prefill.txt`
- `/home/mudler/bench/phase10_gdn_c32_slab/m5_baseline/summary_rows.txt`
- `/home/mudler/bench/phase10_gdn_c32_slab/m5_baseline/provenance.txt`

Current M5 baseline:

| Model | PP | TG | B | S_PP t/s | S_TG t/s | S t/s |
|-------|----|----|---|----------|----------|-------|
| MoE | 512 | 4 | 32 | 2314.18 | 359.16 | 2220.48 |
| MoE | 2048 | 4 | 32 | 2439.95 | 389.43 | 2415.16 |
| Dense | 512 | 4 | 32 | 978.97 | 143.56 | 936.71 |
| Dense | 2048 | 4 | 32 | 1023.61 | 184.09 | 1014.59 |

Source check:

- A C32 M5 candidate cannot be implemented as a launcher-only shortcut.
- The current M5 form-T apply path stores one 16-row tile of `U=T*RHS` in
  registers, syncs, then overwrites `Ud`. That is safe for `C=16`.
- For `C=32`, a naive two-row-tile loop would overwrite RHS rows before all
  output rows are computed, and the current apply call only covers rowbase `0`.
- A correct C32 slab candidate must add a separate staging strategy for all
  `C*DV_TILE` U values, then run focused `GATED_DELTA_NET` op gates before any
  S_PP comparison.

Decision:

- A default-off C32 slab candidate was implemented and rejected by the
  performance gate.
- The candidate was correctness-clean only after fixing a tail-chunk staging
  bug: rows `t >= Cc` in the staged `U=T*RHS` copy-back must be zeroed before
  state/output math. Before that fix, the dense gate produced a degenerate
  transcript even though the focused op gate passed.
- After the tail fix, both default and forced-C32 modes matched the canonical
  md5 gates exactly:
  - MoE: `8cb0ce23777bf55f92f63d0292c756b0`.
  - Dense: `5951a5b4d624ce891e22ab5fca9bc439`.
- KL was not needed because md5 stayed stable after the tail fix.

Correctness artifacts:

- `/home/mudler/bench/phase10_gdn_c32_slab/gates/gated_delta_net_default_after_tailfix.txt`
- `/home/mudler/bench/phase10_gdn_c32_slab/gates/gated_delta_net_c32_slab_after_tailfix.txt`
- `/home/mudler/bench/phase10_gdn_c32_slab/gates/gate_moe_default_after_tailfix.md5`
- `/home/mudler/bench/phase10_gdn_c32_slab/gates/gate_dense_default_after_tailfix.md5`
- `/home/mudler/bench/phase10_gdn_c32_slab/gates/gate_moe_c32_after_tailfix.md5`
- `/home/mudler/bench/phase10_gdn_c32_slab/gates/gate_dense_c32_after_tailfix.md5`

Performance A/B artifacts:

- `/home/mudler/bench/phase10_gdn_c32_slab/ab/moe_base.txt`
- `/home/mudler/bench/phase10_gdn_c32_slab/ab/moe_c32.txt`
- `/home/mudler/bench/phase10_gdn_c32_slab/ab/dense_base.txt`
- `/home/mudler/bench/phase10_gdn_c32_slab/ab/dense_c32.txt`

Performance A/B:

| Model | Mode | PP | TG | B | S_PP t/s | S_TG t/s | S t/s |
|-------|------|----|----|---|----------|----------|-------|
| MoE | M5 base | 512 | 4 | 32 | 2323.48 | 397.57 | 2239.39 |
| MoE | C32 slab | 512 | 4 | 32 | 2069.12 | 357.43 | 1995.06 |
| MoE | M5 base | 2048 | 4 | 32 | 2430.32 | 388.29 | 2405.66 |
| MoE | C32 slab | 2048 | 4 | 32 | 2054.86 | 388.01 | 2037.79 |
| Dense | M5 base | 512 | 4 | 32 | 975.10 | 140.53 | 932.19 |
| Dense | C32 slab | 512 | 4 | 32 | 866.29 | 144.03 | 833.87 |
| Dense | M5 base | 2048 | 4 | 32 | 1019.25 | 183.25 | 1010.26 |
| Dense | C32 slab | 2048 | 4 | 32 | 903.73 | 183.47 | 896.86 |

Rejected diff:

- `/home/mudler/bench/phase10_gdn_c32_slab/rejected/c32_slab_tailfix_rejected.diff`

Conclusion:

- Do not ship Phase 10 C32 slab as implemented.
- C32 slab is not a maintainable shortcut toward parity because duplicated
  A/T recomputation per value slab outweighs the intended state-traffic
  reduction.
- A future GDN prefill attempt should either share the `A/T` work across value
  slabs or switch to a different FLA-style chunk design; it should not repeat
  this env-gated two-slab M5 variant.

## Phase 11 GDN M5 QS-Early Rejection

Phase 11 tested a smaller C=16 M5 scheduling shortcut instead of reopening C32:
move the `QS = Qc * S0` state-boundary tensor-core pass earlier and keep it
default-off behind `GDN_M5_QS_EARLY=1`.

Correctness artifacts:

- `/home/mudler/bench/phase11_gdn_m5_state_boundary/gates/gated_delta_net_default.txt`
- `/home/mudler/bench/phase11_gdn_m5_state_boundary/gates/gated_delta_net_qs_early.txt`
- `/home/mudler/bench/phase11_gdn_m5_state_boundary/gates/gate_moe_default.md5`
- `/home/mudler/bench/phase11_gdn_m5_state_boundary/gates/gate_dense_default.md5`
- `/home/mudler/bench/phase11_gdn_m5_state_boundary/gates/gate_moe_qs_early.md5`
- `/home/mudler/bench/phase11_gdn_m5_state_boundary/gates/gate_dense_qs_early.md5`

Correctness result:

- Default and QS-early paths matched canonical md5 exactly:
  - MoE `8cb0ce23777bf55f92f63d0292c756b0`.
  - Dense `5951a5b4d624ce891e22ab5fca9bc439`.
- KL was not needed.

Performance artifacts:

- `/home/mudler/bench/phase11_gdn_m5_state_boundary/ab/moe_base.txt`
- `/home/mudler/bench/phase11_gdn_m5_state_boundary/ab/moe_qs_early.txt`
- `/home/mudler/bench/phase11_gdn_m5_state_boundary/ab/dense_base.txt`
- `/home/mudler/bench/phase11_gdn_m5_state_boundary/ab/dense_qs_early.txt`

Performance A/B:

| Model | Mode | PP | TG | B | S_PP t/s | S_TG t/s | S t/s |
|-------|------|----|----|---|----------|----------|-------|
| MoE | M5 base | 512 | 4 | 32 | 2325.67 | 355.60 | 2229.90 |
| MoE | QS-early | 512 | 4 | 32 | 2315.77 | 353.27 | 2220.16 |
| MoE | M5 base | 2048 | 4 | 32 | 2441.54 | 390.53 | 2416.80 |
| MoE | QS-early | 2048 | 4 | 32 | 2420.26 | 389.89 | 2395.94 |
| Dense | M5 base | 512 | 4 | 32 | 975.15 | 142.71 | 932.97 |
| Dense | QS-early | 512 | 4 | 32 | 968.23 | 144.24 | 927.17 |
| Dense | M5 base | 2048 | 4 | 32 | 1021.06 | 183.34 | 1012.04 |
| Dense | QS-early | 2048 | 4 | 32 | 1015.77 | 183.73 | 1006.88 |

Rejected diff:

- `/home/mudler/bench/phase11_gdn_m5_state_boundary/rejected/qs_early_rejected.diff`

Conclusion:

- Do not ship Phase 11 QS-early as implemented.
- Merely moving the QS state-boundary product earlier is not enough; it remains
  an extra MMA pass and does not reduce the M5 critical path.
- The next GDN attempt should skip local scheduling-only changes and scope a
  true shared-A/Ai blocked-solve or global-scratch design, with an explicit
  scratch/synchronization cost model before coding.

## Phase 12 GDN Shared-A/Ai Cost Model

Phase 12 evaluated whether a real shared-A/Ai design is credible enough to
prototype after the C32 slab and QS-early shortcut rejections.

Cost-model doc:

- `backend/cpp/llama-cpp-localai-paged/docs/GDN_SHARED_AI_COST_MODEL.md`

Metadata artifact:

- `/home/mudler/bench/phase12_gdn_shared_ai_cost_model/model_metadata.txt`

Model dimensions:

| Model | GDN layers | H | S_v | Metadata basis |
|-------|------------|---|-----|----------------|
| MoE | 30 inferred | 32 inferred | 128 | `ssm.inner_size=4096`, `ssm.state_size=128` |
| Dense | 48 inferred | 48 inferred | 128 | `ssm.inner_size=6144`, `ssm.state_size=128` |

Dynamic-smem result for `S_v=128`:

| Shape | Bytes | KiB | Fits GB10 dynamic smem? |
|-------|-------|-----|-------------------------|
| C16 full-width | 93,376 | 91.19 | yes |
| C32 full-width | 127,360 | 124.38 | no |
| C32 slab64 + U staging | 94,592 | 92.38 | yes |

Ai scratch result at `npp=2048,npl=32,BT=32,f32`:

| Model | Ai scratch MiB | 3x Ai traffic MiB |
|-------|----------------|-------------------|
| MoE | 256.0 | 768.0 |
| Dense | 384.0 | 1152.0 |

Decision:

- GO for a default-off Phase 13 global-Ai32 prototype.
- Constraints: `BT=32`, f32 Ai, two `dv_tile=64` slabs, `GDN_GLOBAL_AI32=1`.
- The prototype must be rejected if it is flat or slower; do not iterate into
  f16/BF16 Ai unless f32 proves the schedule can win.

## Phase 13 GDN Global-Ai32 Prototype Rejection

Phase 13 implemented the Phase 12 design in the llama.cpp fork as a default-off
prototype behind `GDN_GLOBAL_AI32=1`.

Implementation summary:

- Added a f32 Ai precompute kernel.
- Added C32, `dv_tile=64` slab consumption through the chunked GDN path.
- Allocated Ai scratch from the ggml CUDA pool only for supported calls.
- Kept the default C16 M5 path unchanged.

Correctness artifacts:

- `/home/mudler/bench/phase13_gdn_global_ai32/gates/gated_delta_net_default.txt`
- `/home/mudler/bench/phase13_gdn_global_ai32/gates/gated_delta_net_global_ai32.txt`
- `/home/mudler/bench/phase13_gdn_global_ai32/gates/gate_moe_default.md5`
- `/home/mudler/bench/phase13_gdn_global_ai32/gates/gate_dense_default.md5`
- `/home/mudler/bench/phase13_gdn_global_ai32/gates/gate_moe_global_ai32.md5`
- `/home/mudler/bench/phase13_gdn_global_ai32/gates/gate_dense_global_ai32.md5`

Correctness result:

- Default and Global-Ai32 paths matched canonical md5 exactly:
  - MoE `8cb0ce23777bf55f92f63d0292c756b0`.
  - Dense `5951a5b4d624ce891e22ab5fca9bc439`.
- KL was not needed.

Performance artifacts:

- `/home/mudler/bench/phase13_gdn_global_ai32/ab/moe_base.txt`
- `/home/mudler/bench/phase13_gdn_global_ai32/ab/moe_global_ai32.txt`
- `/home/mudler/bench/phase13_gdn_global_ai32/ab/dense_base.txt`
- `/home/mudler/bench/phase13_gdn_global_ai32/ab/dense_global_ai32.txt`

Performance A/B:

| Model | Mode | PP | TG | B | S_PP t/s | S_TG t/s | S t/s |
|-------|------|----|----|---|----------|----------|-------|
| MoE | M5 base | 512 | 4 | 32 | 2325.86 | 396.05 | 2241.21 |
| MoE | Global Ai32 | 512 | 4 | 32 | 2106.50 | 398.55 | 2038.78 |
| MoE | M5 base | 2048 | 4 | 32 | 2425.10 | 389.63 | 2400.66 |
| MoE | Global Ai32 | 2048 | 4 | 32 | 2097.76 | 388.40 | 2079.92 |
| Dense | M5 base | 512 | 4 | 32 | 970.62 | 149.89 | 931.10 |
| Dense | Global Ai32 | 512 | 4 | 32 | 876.51 | 149.29 | 844.62 |
| Dense | M5 base | 2048 | 4 | 32 | 1016.14 | 182.16 | 1007.15 |
| Dense | Global Ai32 | 2048 | 4 | 32 | 918.19 | 183.00 | 911.05 |

Rejected diff:

- `/home/mudler/bench/phase13_gdn_global_ai32/rejected/global_ai32_rejected.diff`

Conclusion:

- Do not ship Phase 13 Global-Ai32 as implemented.
- The global scratch split is correctness-safe but slower than shipped C16 M5.
- Per the Phase 12/13 decision rule, stop GDN kernel work on GB10. The remaining
  vLLM GDN advantage requires a fuller FLA-style blocked solve or hardware
  assumptions that do not fit this GB10 patch stack without a regression.

## Phase 8 Ragged MoE Dispatch Safety Rerun

Phase 8 had already closed the live ragged MoE helper path by profile:
`mm_ids=0.66%`, `gather_mmq=0.42%`, while `mmq_nvfp4=22.36%` and
`act_quant=3.60%`. The only source patch kept from the phase is the test gate
(`0053-test-paged-cover-ragged-MoE-dispatch.patch`); the metadata-only
`LLAMA_MOE_FUSED_DISPATCH` shortcut is rejected.

Rerun artifacts:

- `/home/mudler/bench/phase8_ragged_moe_dispatch/ragged_gate_rerun_20260701_035529.txt`
- `/home/mudler/bench/phase8_ragged_moe_dispatch/safety_rerun_20260701_035549/`

Safety result:

- `MUL_MAT_ID_RAGGED_MOE`: `6/6` on CUDA0.
- Full `MUL_MAT_ID`: `806/806` on CUDA0.
- MoE transcript md5: `8cb0ce23777bf55f92f63d0292c756b0`.
- Dense transcript md5: `5951a5b4d624ce891e22ab5fca9bc439`.

Conclusion:

- The inferencing gates remain canonical on the unchanged production path.
- Do not add a metadata/helper-only fused-dispatch hook. A future Phase 8
  production candidate must reduce `mmq_nvfp4` or activation movement directly,
  stay free of D2H id readback and new stream synchronizations, and then pass
  the same md5/op gates before any serving A/B is considered.

## Phase 18 MTP Shape Trace

Phase 18 implemented the Phase 17 instrumentation-only recommendation as
patch `0055-feat-server-trace-speculative-batch-shapes.patch`.

Implementation summary:

- Added default-off `LLAMA_SPEC_SHAPE_TRACE=1` logging in
  `server_slot::handle_last_sampled_token()`.
- Normal decode logs one row/output per slot.
- MTP verification logs `K + 1` rows/outputs per speculative slot, including
  draft length and `slot.spec_i_batch` range.
- No scheduler, graph-key, KV, logits, acceptance, or rollback behavior changed.

Red/green trace artifacts:

- Red check before patch: `/home/mudler/bench/phase18_mtp_shape_trace_red`
- Green check after patch: `/home/mudler/bench/phase18_mtp_shape_trace_green`

Green trace sample:

```text
spec shape: kind=verify batch_before=0 rows=4 outputs=4 draft=3 spec_i_first=0 spec_i_last=3 pos0=5 slot_tokens=5
spec shape: kind=verify batch_before=0 rows=4 outputs=4 draft=3 spec_i_first=0 spec_i_last=3 pos0=6 slot_tokens=6
spec shape: kind=verify batch_before=0 rows=3 outputs=3 draft=2 spec_i_first=0 spec_i_last=2 pos0=9 slot_tokens=9
```

Disabled-env check:

- `LLAMA_SPEC_SHAPE_TRACE` unset emitted no `spec shape:` lines.

Inference gate artifact:

- `/home/mudler/bench/phase18_mtp_shape_trace_green/gate_after`

Safety result:

- MoE transcript md5: `8cb0ce23777bf55f92f63d0292c756b0`.
- Dense transcript md5: `5951a5b4d624ce891e22ab5fca9bc439`.
- Full `MUL_MAT_ID`: `806/806` on CUDA0.

Conclusion:

- Patch 0055 is safe instrumentation and does not break inferencing on the
  canonical gated paths.
- The trace confirms per-step MTP verification shape variation even in a tiny
  request (`rows=4` and `rows=3`).
- A follow-up scheduler experiment is not yet justified. First use this trace
  under real serving load to measure draft-length bucket entropy.

## Phase 19 MTP Serving Shape Entropy

Phase 19 ran Phase 18's shape trace under the direct serving harness with
`LLAMA_SPEC_SHAPE_TRACE=1`, `NPL="8 32 128"`, `GEN=64`, and `PTOK=128`.

Artifact:

- `/home/mudler/bench/phase19_mtp_shape_entropy/20260701_045534`

Pre/post gate result:

- Pre-gate and post-gate both passed.
- MoE transcript md5: `8cb0ce23777bf55f92f63d0292c756b0`.
- Dense transcript md5: `5951a5b4d624ce891e22ab5fca9bc439`.
- Full `MUL_MAT_ID`: `806/806` on CUDA0.

Serving A/B:

| n | baseline decode_agg | MTP decode_agg | MTP / baseline | baseline TTFT ms | MTP TTFT ms |
|---|---------------------|----------------|----------------|------------------|-------------|
| 8 | 245.0 | 95.7 | 39.1% | 1147.2 | 1633.4 |
| 32 | 409.2 | 110.0 | 26.9% | 2710.0 | 4471.5 |
| 128 | 697.2 | 154.0 | 22.1% | 7601.5 | 20310.4 |

Shape entropy summaries:

- `shape_entropy_summary.tsv`
- `step_shape_summary.tsv`

Per-slot draft distribution:

| window | verify slots | draft counts | top draft share | unique `batch_before` |
|--------|--------------|--------------|-----------------|-----------------------|
| n8 | 162 | `{1: 4, 2: 2, 3: 156}` | 96.3% | 15 |
| n32 | 610 | `{1: 8, 2: 11, 3: 591}` | 96.9% | 96 |
| n128 | 2353 | `{1: 40, 2: 49, 3: 2264}` | 96.2% | 479 |

Per-step aggregate shape:

| window | steps | unique total rows | top full-shape rows |
|--------|-------|-------------------|---------------------|
| n8 | 26 | 12 | `32` rows for 14 steps |
| n32 | 32 | 20 | `128` rows for 13 steps |
| n128 | 37 | 34 | `512` rows for 4 steps |

Decision:

- Do not implement the Phase 20 group/defer-by-draft scheduler shortcut on this
  evidence.
- Draft length is already stable (`draft=3` is >96% of verify slots), yet MTP
  still regresses decode throughput hard and worsens TTFT.
- The residual shape churn is dominated by active-slot/tail churn and the MTP
  `K + 1` verification-row expansion, not mixed draft lengths.
- Any future MTP parity work needs a deeper target-verify graph/state design,
  not a small server scheduling shortcut.

## Phase 20 Current-Stack Serving Snapshot

Phase 20 refreshed the MoE paged-vs-vLLM serving baseline on the current clean
DGX mirror after the MTP investigation.

Artifact:

- `/home/mudler/bench/phase20_current_snapshot/20260701_050621`

Current source:

- `/home/mudler/llama-phase6-source`
- `f2521ab12 feat(server): trace speculative batch shapes`

Pre/post gate result:

- Pre-gate and post-gate both passed.
- MoE transcript md5: `8cb0ce23777bf55f92f63d0292c756b0`.
- Dense transcript md5: `5951a5b4d624ce891e22ab5fca9bc439`.
- Full `MUL_MAT_ID`: `806/806` on CUDA0.

Serving snapshot:

| n | paged decode_agg | vLLM decode_agg | paged/vLLM decode | paged agg | vLLM agg | paged/vLLM agg |
|---|------------------|-----------------|-------------------|-----------|----------|----------------|
| 8 | 220.8 | 290.5 | 76.0% | 164.8 | 245.5 | 67.1% |
| 32 | 411.1 | 594.7 | 69.1% | 252.1 | 456.0 | 55.3% |
| 128 | 670.0 | 1022.7 | 65.5% | 322.4 | 662.4 | 48.7% |

Latency/prefill snapshot:

| n | paged TTFT ms | vLLM TTFT ms | paged/vLLM TTFT | paged prefill_tps | vLLM prefill_tps |
|---|---------------|--------------|------------------|--------------------|------------------|
| 8 | 783.6 | 271.8 | 2.88x | 1669.9 | 4371.5 |
| 32 | 2630.6 | 783.8 | 3.36x | 1712.8 | 5358.3 |
| 128 | 7678.7 | 2465.7 | 3.11x | 1660.4 | 5242.9 |

Decision:

- The latest clean stack is still not at vLLM serving parity on GB10.
- The user-visible gap is dominated by prefill/TTFT and e2e serving throughput,
  not by a now-open MTP or scheduler shortcut.
- Keep MTP scheduler work closed. The next credible parity path is either a
  datacenter-Blackwell rerun or a larger fused-kernel project outside the
  low-conflict GB10 patch stack.

## Phase 21 Current-Stack Serving Harness

Phase 21 made the Phase 20 current-stack serving snapshot repeatable from the
LocalAI backend tree.

New script:

- `backend/cpp/llama-cpp-localai-paged/paged-current-serving-snapshot.sh`

Purpose:

- targets the clean `~/llama-phase6-source` mirror by default;
- rejects busy docker, `local-ai-worker`, GPU compute, or owned GPU-lock state;
- builds the current llama.cpp targets;
- runs pre/post `paged-inference-gates.sh`;
- runs paged and vLLM serving arms with the same h2h client;
- writes paged/vLLM ratio summaries.

Verification:

- local `bash -n` passed;
- local `--help` passed;
- DGX `DRY_RUN=1` validated required paths and preflight without launching
  servers.

Dry-run artifact:

- `/home/mudler/bench/phase21_harness_dryrun/20260701_051757`

Decision:

- Use `paged-current-serving-snapshot.sh` for future current-stack GB10 serving
  snapshots.
- Do not use stale DGX `~/bench/combined_definitive.sh` without porting it to
  `~/llama-phase6-source` and the owner-file lock discipline.

## Phase 22 Patch-Series Mirror Invariant

Phase 22 verified that the LocalAI on-disk paged patch series still reconstructs
the canonical llama.cpp fork tree after patch `0055`.

Method:

- Create a fresh worktree at Makefile pin
  `0ed235ea2c17a19fc8238668653946721ed136fd`.
- Apply every `backend/cpp/llama-cpp-localai-paged/patches/paged/0*.patch` with
  strict `git apply`, matching the LocalAI build path.
- Stage the result and compare `git write-tree` with the fork branch HEAD tree.

Result:

```text
base=0ed235ea2c17a19fc8238668653946721ed136fd
applied_tree=5bdbf8ea3d750fe6fa1f85175fd6357d36222edb
fork_tree=5bdbf8ea3d750fe6fa1f85175fd6357d36222edb
```

Decision:

- The patch series is drift-free against fork branch `localai-paged` at
  `fb9402661 feat(server): trace speculative batch shapes`.

## Phase 24 Snapshot Hardware Report

Phase 24 made the current-stack serving harness record hardware identity before
any server starts. This keeps GB10/workstation Blackwell evidence separate from
future datacenter-Blackwell reruns.

Script change:

- `backend/cpp/llama-cpp-localai-paged/paged-current-serving-snapshot.sh` now
  writes `hardware.txt` after preflight and before the `DRY_RUN=1` exit.

Recorded fields:

- `nvidia-smi -L`;
- `nvidia-smi --query-gpu=name,driver_version,memory.total,compute_cap`, with
  fallback to name/driver/memory if `compute_cap` is unavailable;
- `gpu_name`;
- `hardware_class`;
- parity note for that hardware class.

Verification:

- local `bash -n` passed;
- local `--help` passed;
- DGX `DRY_RUN=1` validated preflight and wrote `hardware.txt` without launching
  servers.

Dry-run artifact:

- `/home/mudler/bench/phase24_hardware_report_dryrun/20260701_052741`

DGX hardware result:

```text
GPU 0: NVIDIA GB10
driver=580.159.03
compute_cap=12.1
hardware_class=gb10_or_workstation_blackwell
```

Decision:

- Future snapshot artifacts are self-describing enough to prevent accidental
  GB10-to-datacenter generalization.
- The Phase 20 GB10 closure still applies to `gb10_or_workstation_blackwell`;
  datacenter Blackwell needs a fresh run of the same methodology.

## Phase 25 Snapshot Gate Summary

Phase 25 made current-stack serving artifacts self-auditing for the inference
gates that protect the paged path.

Script change:

- `backend/cpp/llama-cpp-localai-paged/paged-current-serving-snapshot.sh` now
  writes `gate_summary.tsv` after the post gate in a full run.
- The script also supports `--summarize-gates ART` to generate the same summary
  from existing `gate_pre/` and `gate_post/` artifacts without launching
  servers.

Recorded rows:

- pre/post MoE transcript md5 versus
  `8cb0ce23777bf55f92f63d0292c756b0`;
- pre/post dense transcript md5 versus
  `5951a5b4d624ce891e22ab5fca9bc439`;
- pre/post backend op rows, currently `MUL_MAT_ID`, with the parsed passed/total
  count.

Verification:

- Red check: Phase 20 initially had gate artifacts but no `gate_summary.tsv`.
- local `bash -n` passed;
- local `--help` passed;
- DGX `--summarize-gates` against Phase 20 wrote six green rows;
- DGX `DRY_RUN=1` validated the normal path still preflights and writes
  `hardware.txt` without launching servers or writing a gate summary before
  gates exist.

Artifacts:

- Backfilled summary:
  `/home/mudler/bench/phase20_current_snapshot/20260701_050621/gate_summary.tsv`
- Dry run:
  `/home/mudler/bench/phase25_gate_summary_dryrun/20260701_053353`

Backfilled Phase 20 gate summary:

```text
pre  moe_md5     ok  8cb0ce23777bf55f92f63d0292c756b0
pre  dense_md5   ok  5951a5b4d624ce891e22ab5fca9bc439
pre  op_MUL_MAT_ID   ok  806/806
post moe_md5     ok  8cb0ce23777bf55f92f63d0292c756b0
post dense_md5   ok  5951a5b4d624ce891e22ab5fca9bc439
post op_MUL_MAT_ID   ok  806/806
```

Decision:

- Future full serving snapshots carry compact proof that inference md5/op gates
  stayed green before and after the paged-vs-vLLM run.
- Treat `gate_summary.tsv` plus `hardware.txt` as the quick audit surface before
  accepting a parity snapshot.

## Phase 26 Audited Current-Stack Serving Snapshot

Phase 26 ran a full current-stack paged-vs-vLLM MoE serving snapshot with the
Phase 24/25 audit files enabled.

Artifact:

- `/home/mudler/bench/phase26_audited_snapshot/20260701_053650`

Current source:

- `/home/mudler/llama-phase6-source`
- `f2521ab12 feat(server): trace speculative batch shapes`

Hardware report:

- `hardware_class=gb10_or_workstation_blackwell`
- `GPU 0: NVIDIA GB10`
- driver `580.159.03`
- compute capability `12.1`

Pre/post gate summary:

| phase | check | status | actual |
|-------|-------|--------|--------|
| pre | MoE md5 | ok | `8cb0ce23777bf55f92f63d0292c756b0` |
| pre | dense md5 | ok | `5951a5b4d624ce891e22ab5fca9bc439` |
| pre | `MUL_MAT_ID` | ok | `806/806` |
| post | MoE md5 | ok | `8cb0ce23777bf55f92f63d0292c756b0` |
| post | dense md5 | ok | `5951a5b4d624ce891e22ab5fca9bc439` |
| post | `MUL_MAT_ID` | ok | `806/806` |

Serving snapshot:

| n | paged decode_agg | vLLM decode_agg | paged/vLLM decode | paged agg | vLLM agg | paged/vLLM agg |
|---|------------------|-----------------|-------------------|-----------|----------|----------------|
| 8 | 230.8 | 283.2 | 81.5% | 170.6 | 241.6 | 70.6% |
| 32 | 420.0 | 609.0 | 69.0% | 254.6 | 466.7 | 54.6% |
| 128 | 673.4 | 1025.0 | 65.7% | 324.0 | 656.5 | 49.4% |

Latency/prefill snapshot:

| n | paged TTFT ms | vLLM TTFT ms | paged/vLLM TTFT | paged prefill_tps | vLLM prefill_tps |
|---|---------------|--------------|------------------|--------------------|------------------|
| 8 | 778.6 | 271.1 | 2.87x | 1679.9 | 4485.6 |
| 32 | 2607.4 | 749.4 | 3.48x | 1698.8 | 5427.8 |
| 128 | 7569.6 | 2534.3 | 2.99x | 1668.7 | 5122.0 |

vLLM startup notes:

- vLLM selected the expected GB10 backend mix: FlashInfer FP8 projection
  kernels, Triton/FLA GDN prefill, FlashAttention, and MARLIN NVFP4 MoE.
- Startup was long because the server loaded three checkpoint shards, loaded
  cached torch-compile graphs, ran FlashInfer fp8 GEMM autotuning, and captured
  CUDA graphs before the API became ready.

Decision:

- The audited current stack still is not at vLLM serving parity on GB10.
- The Phase 20 conclusion is reproduced with stronger audit artifacts:
  `hardware.txt`, `gate_summary.tsv`, pre/post full gates, and same-session
  paged/vLLM ratios.
- Current paged/vLLM decode ratios remain about `81.5%` at n8, `69.0%` at n32,
  and `65.7%` at n128; e2e aggregate ratios remain about `70.6%`, `54.6%`,
  and `49.4%`.

## Phase 27 Graph-Node-Traced Current-Stack Serving Profile

Phase 27 re-profiled the current clean llama.cpp serving path with CUDA graph
node tracing enabled. This checks the Phase 8 bucket picture against the decode
profiling rule: serving/decode profiles must use `--cuda-graph-trace=node`.

Artifact:

- `/home/mudler/bench/phase27_graph_node_serving/20260701_055519`

Source and hardware:

- `/home/mudler/llama-phase6-source`
- `f2521ab12 feat(server): trace speculative batch shapes`
- `GPU 0: NVIDIA GB10`, driver `580.159.03`, compute capability `12.1`
- Nsight Systems `2025.3.2.474-253236389321v0`

Safety gates:

| phase | check | status | actual |
|-------|-------|--------|--------|
| pre | MoE md5 | ok | `8cb0ce23777bf55f92f63d0292c756b0` |
| pre | dense md5 | ok | `5951a5b4d624ce891e22ab5fca9bc439` |
| pre | `MUL_MAT_ID` | ok | `806/806` |
| post retry | MoE md5 | ok | `8cb0ce23777bf55f92f63d0292c756b0` |
| post retry | dense md5 | ok | `5951a5b4d624ce891e22ab5fca9bc439` |
| post retry | `MUL_MAT_ID` | ok | `806/806` |

The first immediate post-gate attempt raced with Nsight teardown and rejected
the run because it detected one compute process even though `nvidia-smi` already
printed no running processes. The post-gate retry started from `docker=0`,
`local_ai_worker=0`, `compute=0`, and a `FREE` owner file.

Serving sample (`n=128`, `PTOK=128`, `GEN=64`):

| agg_tps | decode_agg_tps | decode_perseq_tps | prefill_tps | TTFT mean ms |
|---------|----------------|--------------------|-------------|--------------|
| 319.9 | 675.5 | 3.9 | 1671.1 | 8363.4 |

This matches Phase 26's n128 paged decode rate (`673.4` decode_agg_tps) closely
enough to treat the profile as representative for bucket direction.

Graph-node-traced kernel buckets:

| macro bucket | time ms | share |
|--------------|---------|-------|
| GDN | 6706.33 | 33.47% |
| MoE/FFN-GEMM | 5871.92 | 29.31% |
| bf16-proj | 2725.07 | 13.60% |
| layout-copy | 1309.99 | 6.54% |
| ew-mul(weight/norm/GDN) | 724.29 | 3.61% |
| act-quant | 697.75 | 3.48% |
| norms/residual | 405.29 | 2.02% |
| ew-add(resid/MoE-fanin) | 361.81 | 1.81% |
| MoE-dispatch | 275.99 | 1.38% |
| FA | 271.03 | 1.35% |

Fine buckets:

- `gdn_core`: `5929.85 ms` (`29.59%`)
- `mmq_nvfp4`: `5697.79 ms` (`28.44%`)
- `cublas_bf16_gemm`: `1892.81 ms` (`9.45%`)
- `act_quant`: `697.75 ms` (`3.48%`)
- `mm_ids`: `121.99 ms` (`0.61%`)
- `gather_mmq`: `73.88 ms` (`0.37%`)
- `argsort_topk`: `80.11 ms` (`0.40%`)

Decision:

- The graph-node-traced current-stack profile confirms the Phase 8 source
  shortcut decision. Metadata/helper work is still too small: `mm_ids`,
  `gather_mmq`, and `argsort_topk` together are about `1.38%`.
- A credible GB10 source patch would have to reduce `gdn_core` or
  `mmq_nvfp4`/bf16 projection work directly. The low-conflict helper-dispatch
  path still should not be reopened.
- The serving profile does not change the Phase 26 parity verdict: n128 paged
  decode remains about `675 tok/s`, far below vLLM's same-session `1025 tok/s`.

## Phase 28 NVFP4 MMQ Occupancy Build-Knob A/B

Phase 28 tested the remaining small, additive grouped-MMQ occupancy knobs
already present in the llama.cpp fork. This was a build-vs-build A/B only; no
source change was promoted.

Artifact:

- `/home/mudler/bench/phase28_mmq_occupancy/20260701_040450`

Source and hardware:

- `/home/mudler/llama-phase6-source`
- `f2521ab12 feat(server): trace speculative batch shapes`
- `GPU 0: NVIDIA GB10`, driver `580.159.03`, compute capability `12.1`

Build/gate results:

| variant | build result | MoE md5 | dense md5 | `MUL_MAT_ID` |
|---------|--------------|---------|-----------|--------------|
| baseline | existing `build-cuda` | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `806/806` |
| `GGML_CUDA_FP4_MINBLOCKS=2` | built | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `806/806` |
| `GGML_CUDA_FP4_MMQ_Y=64` | compile-time reject | n/a | n/a | n/a |

`GGML_CUDA_FP4_MMQ_Y=64` fails the NVFP4 writeback invariant:
`static_assert(nwarps*tile_C::I == mmq_y)`. That also rejects combined
`MMQ_Y=64+MINBLOCKS=2` as a source of evidence. `MMQ_Y=96` is not a valid
low-conflict shortcut for the same row-tile specialization reason, so it was
not promoted to a serving A/B.

Same-session n128 serving A/B (`PTOK=128`, `GEN=64`, two reps per arm):

| arm | reps | agg_tps | decode_agg_tps | decode_perseq_tps | prefill_tps | TTFT mean ms |
|-----|------|---------|----------------|--------------------|-------------|--------------|
| baseline | 2 | 328.8 | 705.1 | 3.970 | 1607.4 | 7868.8 |
| `MINBLOCKS=2` | 2 | 326.4 | 689.9 | 3.905 | 1644.9 | 7778.1 |
| ratio | 2 | 0.9927 | 0.9784 | 0.9836 | 1.0233 | 0.9885 |

Post-serving variant gate remained green:

| phase | check | status | actual |
|-------|-------|--------|--------|
| post serving | MoE md5 | ok | `8cb0ce23777bf55f92f63d0292c756b0` |
| post serving | dense md5 | ok | `5951a5b4d624ce891e22ab5fca9bc439` |
| post serving | `MUL_MAT_ID` | ok | `806/806` |

Decision:

- `GGML_CUDA_FP4_MINBLOCKS=2` is inference-safe but does not clear the serving
  A/B gate; it regressed n128 decode aggregate by about `2.2%`.
- `GGML_CUDA_FP4_MMQ_Y` is not a valid additive shortcut without deeper NVFP4
  writeback retile work.
- Do not promote either knob or add a LocalAI patch. The grouped-MMQ bucket
  still needs a structural kernel change, not a launch-bounds/row-tile tweak.

## Phase 29 Default-Off MoE MMQ Shape Trace

Phase 29 added evidence-only instrumentation for the structural grouped-MMQ
path that remains after Phase 28. The trace is default-off and lives at the
host-side grouped-MMQ selector so it does not read `expert_bounds` back from the
device or add a synchronization.

Patch and artifact:

- Fork commit: `20a99518a feat(cuda): trace moe mmq batch shapes`
- LocalAI patch: `0056-feat-cuda-trace-moe-mmq-batch-shapes.patch`
- Artifact: `/home/mudler/bench/phase29_mmq_shape_trace/20260701_042428`

TDD/build checks:

| check | result |
|-------|--------|
| RED | `test-cuda-mmq-shape-trace` first failed on missing `ggml-cuda/mmq-shape-trace.h` |
| local GREEN | `cmake --build build --target test-cuda-mmq-shape-trace -j 4 && ./build/bin/test-cuda-mmq-shape-trace` |
| DGX CUDA build | `cmake --build build-cuda --target llama-completion test-backend-ops test-cuda-mmq-shape-trace` |

Safety gates:

| gate | MoE md5 | dense md5 | `MUL_MAT_ID` | trace lines |
|------|---------|-----------|--------------|-------------|
| default-off | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `806/806` | `0` |
| `LLAMA_MOE_MMQ_SHAPE_TRACE=4` | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `806/806` | `4` |

Example trace line:

```text
[LLAMA_MOE_MMQ_SHAPE] type=40 moe=1 ncols_dst=104 nchannels_x=256 ncols_max=13 n_active_est=104 density=1 mmq_x_max=128 mmq_x_lim=64 mmq_x_best=16 mmq_y=128 stream_k=1
```

Decision:

- This is not a speed patch and should not be counted as parity progress by
  itself.
- It gives a bounded, md5-safe way to collect live serving grouped-MMQ shape
  evidence before designing the next structural kernel.

## Phase 30 Live MoE MMQ Shape Distribution

Phase 30 used patch `0056` under the n128 h2h serving workload to collect the
first 4096 grouped-MMQ selector shapes. This is a measurement-only phase.

Artifact:

- `/home/mudler/bench/phase30_mmq_shape_serving/20260701_043300`

Run:

- Source: `dgx:~/llama-phase6-source`, commit `826c97a05`
- Env: `LLAMA_KV_PAGED=1 LLAMA_MOE_FORCE_GRAPHS=1 LLAMA_MOE_MMQ_SHAPE_TRACE=4096`
- Workload: h2h `n=128`, `PTOK=128`, `GEN=64`
- Throughput while tracing: `decode_agg_tps=645.8`, `agg_tps=313.3`,
  `prefill_tps=1597.9`, `TTFT mean=8192.3 ms`

Trace summary:

| bucket | total traced calls | dominant `mmq_x_best` | density range | `ncols_max` range |
|--------|--------------------|-----------------------|---------------|-------------------|
| decode-like (`ncols_max <= 128`) | 1200 | `64` (480), `32` (360), `40` (240), `48` (120) | 1-4 | 26-111 |
| prefill-like (`ncols_max > 128`) | 2896 | `128` (1816), `64` (720), `112` (240), `48` (120) | 5-16 | 132-512 |

Overall first-4096 distribution:

| metric | notable values |
|--------|----------------|
| `mmq_x_best` | `128`: 1816, `64`: 1200, `32`: 360, `40`: 240, `48`: 240, `112`: 240 |
| `density` | `16`: 1680, `2`: 480, `1`: 360, `6`: 360, `4`: 240, `5`: 240 |
| `stream_k` | `1`: 4096 |

Post-run gates:

| check | status | actual |
|-------|--------|--------|
| MoE md5 | ok | `8cb0ce23777bf55f92f63d0292c756b0` |
| dense md5 | ok | `5951a5b4d624ce891e22ab5fca9bc439` |
| `MUL_MAT_ID` | ok | `806/806` |

Decision:

- Decode serving really is feeding grouped-MMQ small-M tiles: in this trace,
  decode-like calls stay at density `1-4` and `mmq_x_best <= 64`.
- Prefill-like calls mostly select `mmq_x_best=128` and density `16`, so a
  decode-only structural kernel should not be generalized to prefill without a
  separate A/B.
- Every traced call used stream-k, so a replacement kernel must account for the
  current stream-k/fixup behavior rather than only conventional tiling.

## Phase 31 Live MoE MMQ Launch Shape Distribution

Phase 31 added patch `0057`, a default-off launch trace paired with the Phase 29
selector trace. It records the actual launch policy after `ntiles_dst`,
`tiles_efficiency_percent`, `stream_k_blocks`, and `fixup_needed` are known.

Artifact:

- `/home/mudler/bench/phase31_mmq_launch_trace/20260701_064424`

Run:

- Fork commit: `/home/mudler/_git/llama.cpp` `c78e537b5`
- DGX mirror commit: `dgx:~/llama-phase6-source` `8b75905e9`
- Env: `LLAMA_KV_PAGED=1 LLAMA_MOE_FORCE_GRAPHS=1 LLAMA_MOE_MMQ_SHAPE_TRACE=4096`
- Workload: h2h `n=128`, `PTOK=128`, `GEN=64`
- Throughput while tracing: `decode_agg_tps=691.0`, `agg_tps=337.0`,
  `prefill_tps=1500.4`, `TTFT mean=7671.0 ms`

Launch summary:

| bucket | launch lines | `fixup=1` | `stream_k_blocks == ntiles_dst` | tile efficiency | `ncols_max` range |
|--------|--------------|-----------|----------------------------------|-----------------|-------------------|
| decode-like (`ncols_max <= 128`) | 4800 | 0 | 4800 | 96-99 | 12-128 |
| prefill-like (`ncols_max > 128`) | 4920 | 0 | 4920 | 99-100 | 129-510 |

Gates:

| check | status | actual |
|-------|--------|--------|
| default-off MoE md5 | ok | `8cb0ce23777bf55f92f63d0292c756b0` |
| default-off dense md5 | ok | `5951a5b4d624ce891e22ab5fca9bc439` |
| trace-enabled MoE md5 | ok | `8cb0ce23777bf55f92f63d0292c756b0` |
| trace-enabled dense md5 | ok | `5951a5b4d624ce891e22ab5fca9bc439` |
| post-serving MoE md5 | ok | `8cb0ce23777bf55f92f63d0292c756b0` |
| post-serving dense md5 | ok | `5951a5b4d624ce891e22ab5fca9bc439` |
| `MUL_MAT_ID` | ok | `806/806` in all three gate runs |

Decision:

- Do not pursue a no-fixup/no-stream-k shortcut for n128 serving: the measured
  launch path already uses `stream_k_blocks == ntiles_dst` and never runs fixup.
- The remaining grouped-MMQ work is structural small-M kernel work, not launch
  overhead. A follow-up should target the decode-like `mmq_x <= 64`, low-density
  kernel shape directly and keep the prefill `mmq_x=128` path separate.

## Phase 32 Small-M MoE MMQ Candidate Classifier

Phase 32 added patch `0058`, a default-off small-M candidate trace. It does not
change tile selection or launch behavior; it only logs
`[LLAMA_MOE_MMQ_SMALL_M]` lines when the grouped-MMQ selector has produced a
decode-like low-density MoE shape.

Artifact:

- `/home/mudler/bench/phase32_small_m_classifier/20260701_070127`

Run:

- Fork commit: `/home/mudler/_git/llama.cpp` `2a9964d29`
- DGX mirror commit: `dgx:~/llama-phase6-source` `024f494d0`
- Env: `LLAMA_KV_PAGED=1 LLAMA_MOE_FORCE_GRAPHS=1 LLAMA_MOE_MMQ_SMALL_M_TRACE=4096`
- Workload: h2h `n=128`, `PTOK=128`, `GEN=64`
- Throughput while tracing: `decode_agg_tps=689.0`, `agg_tps=343.9`,
  `prefill_tps=1566.5`, `TTFT mean=7849.0 ms`

Candidate summary:

| metric | notable values |
|--------|----------------|
| total candidates | 4096 |
| `mmq_x_best` | `64`: 1800, `48`: 1096, `40`: 360, `32`: 360, `16`: 360, `24`: 120 |
| density | `4`: 1440, `3`: 1336, `1`: 840, `2`: 480 |
| `ncols_max` | `84`: 600, `128`: 360, `70`: 240, `12`: 240, `97`: 240, `126`: 240 |

Gates:

| check | status | actual |
|-------|--------|--------|
| default-off MoE md5 | ok | `8cb0ce23777bf55f92f63d0292c756b0` |
| default-off dense md5 | ok | `5951a5b4d624ce891e22ab5fca9bc439` |
| trace-enabled MoE md5 | ok | `8cb0ce23777bf55f92f63d0292c756b0` |
| trace-enabled dense md5 | ok | `5951a5b4d624ce891e22ab5fca9bc439` |
| post-serving MoE md5 | ok | `8cb0ce23777bf55f92f63d0292c756b0` |
| post-serving dense md5 | ok | `5951a5b4d624ce891e22ab5fca9bc439` |
| `MUL_MAT_ID` | ok | `806/806` in all three gate runs |

Decision:

- There is enough live candidate coverage to justify a default-off tile-policy
  A/B in Phase 33.
- Start with a small-M MoE-only `mmq_x=16` cap, and consider `8` only if it
  compiles and preserves the existing NVFP4 tile invariants.

## Phase 33 Small-M MoE MMQ Tile Policy A/B

Phase 33 added patch `0059`, default-off `LLAMA_MOE_SMALL_M_TILE=<n>`, to cap
only the Phase 32 classified small-M MoE grouped-MMQ calls. This tested whether
a vLLM-like smaller M block could improve n128 decode without rewriting the
kernel.

Artifact:

- `/home/mudler/bench/phase33_small_m_tile_policy/20260701_071136`

Gates:

| mode | MoE md5 | dense md5 | `MUL_MAT_ID` |
|------|---------|-----------|--------------|
| default-off | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `806/806` |
| `LLAMA_MOE_SMALL_M_TILE=16` | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `806/806` |
| `LLAMA_MOE_SMALL_M_TILE=8` | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `806/806` |
| post-serving | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `806/806` |

Same-session n128 serving:

| mode | decode_agg_tps | agg_tps | prefill_tps | ratio vs baseline |
|------|----------------|---------|-------------|-------------------|
| baseline | 672.1 | 339.5 | 1511.4 | 1.000x |
| `LLAMA_MOE_SMALL_M_TILE=16` | 640.3 | 328.9 | 1522.2 | 0.953x |
| `LLAMA_MOE_SMALL_M_TILE=8` | 583.2 | 307.4 | 1442.6 | 0.868x |

Decision:

- Reject simple smaller `mmq_x` caps for classified n128 small-M calls. They are
  inference-safe but slower.
- A future grouped-MMQ kernel must change the work shape more deeply than the
  host-side tile cap, or pivot to a different bucket.

## Phase 34 MoE MMID Dispatch Route Trace

Phase 34 added patch `0060`, a default-off `LLAMA_MOE_MMID_ROUTE_TRACE=<n>`
diagnostic around `MUL_MAT_ID` dispatch. It does not alter routing; it logs the
existing route decision as `mmvq`, `mmvf`, grouped `mmq`, `mmf`, or host-sync
`fallback`.

Artifact:

- `/home/mudler/bench/phase34_mmid_route_trace/20260701_072737`

Run:

- Fork commit: `/home/mudler/_git/llama.cpp` `6c332094c`
- DGX mirror commit: `dgx:~/llama-phase6-source` `34a256d14`
- Env: `LLAMA_KV_PAGED=1 LLAMA_MOE_FORCE_GRAPHS=1 LLAMA_MOE_MMID_ROUTE_TRACE=4096`
- Workload: staggered n128 `llama-server`, `GEN=64`

Route summary:

| metric | value |
|--------|-------|
| traced `MUL_MAT_ID` calls | 4096 |
| grouped MMQ | 2776 |
| MMVQ | 1320 |
| host-sync fallback | 0 |
| top shapes | `mmq ne2=12`: 1096, `mmq ne2=18`: 480, `mmvq ne2=8`: 360 |

Gates:

| check | status | actual |
|-------|--------|--------|
| default-off MoE md5 | ok | `8cb0ce23777bf55f92f63d0292c756b0` |
| default-off dense md5 | ok | `5951a5b4d624ce891e22ab5fca9bc439` |
| trace-enabled MoE md5 | ok | `8cb0ce23777bf55f92f63d0292c756b0` |
| trace-enabled dense md5 | ok | `5951a5b4d624ce891e22ab5fca9bc439` |
| post-serving MoE md5 | ok | `8cb0ce23777bf55f92f63d0292c756b0` |
| post-serving dense md5 | ok | `5951a5b4d624ce891e22ab5fca9bc439` |
| `MUL_MAT_ID` | ok | `806/806` in all three gate runs |

Decision:

- The current n128 serving path is not hitting the host-sync fallback in traced
  `MUL_MAT_ID` calls. The route is graph-safe MMVQ for very small widths and
  grouped MMQ above that.
- Do not scope the next parity phase around avoiding fallback dispatch. Scope it
  around grouped-MMQ small-M kernel partitioning or another measured bucket.

## Phase 35 Regular MUL_MAT Route Trace

Phase 35 added patch `0061`, a default-off `LLAMA_MUL_MAT_ROUTE_TRACE=<n>`
diagnostic around regular `MUL_MAT` dispatch. It does not alter routing; it logs
the existing route decision for projection-heavy calls.

Artifact:

- `/home/mudler/bench/phase35_mul_mat_route_trace/20260701_074359`

Run:

- Fork commit: `/home/mudler/_git/llama.cpp` `486c28c63`
- DGX mirror commit: `dgx:~/llama-phase6-source` `18f7ad005`
- Env: `LLAMA_KV_PAGED=1 LLAMA_MOE_FORCE_GRAPHS=1 LLAMA_MUL_MAT_ROUTE_TRACE=8192`
- Workload: staggered n128 `llama-server`, `GEN=64`

Route summary:

| route | count |
|-------|-------|
| `mat_f` | 2888 |
| `op_cublas` | 2292 |
| `mmq` | 1328 |
| `vec_q` | 1214 |
| `vec_f` | 470 |

Type summary:

| type | meaning | count |
|------|---------|-------|
| 30 | BF16 | 3965 |
| 40 | NVFP4 | 2542 |
| 0 | F32 | 1685 |

Top BF16 route/shape counts:

| route | shape | count |
|-------|-------|-------|
| `mat_f` | `ne1=12 ne11=12 ne12=1 ne13=1` | 775 |
| `op_cublas` | `ne1=18 ne11=18 ne12=1 ne13=1` | 760 |
| `mat_f` | `ne1=8 ne11=8 ne12=1 ne13=1` | 570 |
| `op_cublas` | `ne1=36 ne11=36 ne12=1 ne13=1` | 380 |
| `mat_f` | `ne1=2 ne11=2 ne12=1 ne13=1` | 380 |

Gates:

| check | status | actual |
|-------|--------|--------|
| default-off MoE md5 | ok | `8cb0ce23777bf55f92f63d0292c756b0` |
| default-off dense md5 | ok | `5951a5b4d624ce891e22ab5fca9bc439` |
| trace-enabled MoE md5 | ok | `8cb0ce23777bf55f92f63d0292c756b0` |
| trace-enabled dense md5 | ok | `5951a5b4d624ce891e22ab5fca9bc439` |
| post-serving MoE md5 | ok | `8cb0ce23777bf55f92f63d0292c756b0` |
| post-serving dense md5 | ok | `5951a5b4d624ce891e22ab5fca9bc439` |
| `MUL_MAT` | ok | `1146/1146` in all three gate runs |
| `MUL_MAT_ID` | ok | `806/806` in all three gate runs |

Decision:

- The first 8192 regular `MUL_MAT` calls in n128 serving are dominated by BF16
  direct `mat_f` and generic `op_cublas`, not batched cuBLAS.
- Next projection work should either add a cuBLAS/MMF subroute trace or test a
  bounded BF16 route policy for the `op_cublas` shapes. Do not chase batched
  cuBLAS for this measured serving slice.

## Phase 36 cuBLAS Subroute Trace

Phase 36 added patch `0062`, a default-off `LLAMA_CUBLAS_ROUTE_TRACE=<n>`
diagnostic around the generic cuBLAS `MUL_MAT` path. It does not alter branch
behavior; it classifies existing calls as `nvfp4_bf16_tc`, `bf16_tc`,
`f16_tc_32f`, `f16_tc_16f`, or `sgemm`.

Artifact:

- `/home/mudler/bench/phase36_cublas_route_trace/20260701_081228`

Run:

- Fork commit: `/home/mudler/_git/llama.cpp` `38c4ef2e4`
- DGX mirror commit: `dgx:~/llama-phase6-source` `e0224393a`
- Env: `LLAMA_KV_PAGED=1 LLAMA_MOE_FORCE_GRAPHS=1 LLAMA_CUBLAS_ROUTE_TRACE=8192`
- Workload: staggered n128 `llama-server` diagnostic trace

Route summary:

| route | count |
|-------|------:|
| `bf16_tc` | 5681 |
| `sgemm` | 2511 |

Top shapes:

| route | shape | count |
|-------|-------|------:|
| `bf16_tc` | `type=30 row_diff=32 src1_ncols=510 ne00=2048 ne10=2048` | 360 |
| `bf16_tc` | `type=30 row_diff=8192 src1_ncols=510 ne00=2048 ne10=2048` | 240 |
| `bf16_tc` | `type=30 row_diff=2048 src1_ncols=510 ne00=4096 ne10=4096` | 240 |
| `sgemm` | `type=0 row_diff=256 src1_ncols=510 ne00=2048 ne10=2048` | 240 |
| `sgemm` | `type=0 row_diff=1 src1_ncols=510 ne00=2048 ne10=2048` | 240 |

Gates:

| check | status | actual |
|-------|--------|--------|
| default-off MoE md5 | ok | `8cb0ce23777bf55f92f63d0292c756b0` |
| default-off dense md5 | ok | `5951a5b4d624ce891e22ab5fca9bc439` |
| trace-enabled MoE md5 | ok | `8cb0ce23777bf55f92f63d0292c756b0` |
| trace-enabled dense md5 | ok | `5951a5b4d624ce891e22ab5fca9bc439` |
| post-serving MoE md5 | ok | `8cb0ce23777bf55f92f63d0292c756b0` |
| post-serving dense md5 | ok | `5951a5b4d624ce891e22ab5fca9bc439` |
| `MUL_MAT` | ok | `1146/1146` default, trace, post-serving |
| `MUL_MAT_ID` | ok | `806/806` default, trace, post-serving |

Decision:

- Phase 35's generic `op_cublas` bucket is BF16 tensor-core plus F32 SGEMM in
  this serving slice. It is not NVFP4 cuBLAS and not batched cuBLAS.
- The next projection phase should identify whether the `type=0` SGEMM shapes
  are expected glue tensors or a missed BF16 route. Do not change routing until
  a separately gated policy proves md5/op safety.

## Phase 37 cuBLAS Tensor-Name Trace

Phase 37 added patch `0063`, extending the default-off
`LLAMA_CUBLAS_ROUTE_TRACE=<n>` diagnostic with `src0`, `src1`, and `dst` tensor
names. It is instrumentation only.

Artifact:

- `/home/mudler/bench/phase37_cublas_name_trace/20260701_083227`

Run:

- Fork commit: `/home/mudler/_git/llama.cpp` `2d590d770`
- DGX mirror commit: `dgx:~/llama-phase6-source` `2cbb61969`
- Env: `LLAMA_KV_PAGED=1 LLAMA_MOE_FORCE_GRAPHS=1 LLAMA_CUBLAS_ROUTE_TRACE=4096`
- Workload: staggered n128 `llama-server` diagnostic trace

Route summary:

| route | count |
|-------|------:|
| `bf16_tc` | 2884 |
| `sgemm` | 1212 |

Named bucket summary:

| route | tensor pattern |
|-------|----------------|
| `bf16_tc` | `blk.N.attn_gate.weight -> z-N` |
| `bf16_tc` | `blk.N.ssm_out.weight -> linear_attn_out-N` |
| `sgemm` | `blk.N.ffn_gate_inp.weight -> ffn_moe_logits-N` |
| `sgemm` | `blk.N.ffn_gate_inp_shexp.weight -> shared_expert_gate-N` |

Gates:

| check | status | actual |
|-------|--------|--------|
| default-off MoE md5 | ok | `8cb0ce23777bf55f92f63d0292c756b0` |
| default-off dense md5 | ok | `5951a5b4d624ce891e22ab5fca9bc439` |
| trace-enabled MoE md5 | ok | `8cb0ce23777bf55f92f63d0292c756b0` |
| trace-enabled dense md5 | ok | `5951a5b4d624ce891e22ab5fca9bc439` |
| post-serving MoE md5 | ok | `8cb0ce23777bf55f92f63d0292c756b0` |
| post-serving dense md5 | ok | `5951a5b4d624ce891e22ab5fca9bc439` |
| `MUL_MAT` | ok | `1146/1146` default, trace, post-serving |
| `MUL_MAT_ID` | ok | `806/806` default, trace, post-serving |

Decision:

- The Phase 36 F32 SGEMM bucket is mainly MoE gate logits and shared-expert gate
  projections, not an anonymous missed dense projection route.
- Do not blindly force these calls to BF16. First inspect the model-load tensor
  types for `ffn_gate_inp*`; if changing weight dtype or graph routing is
  considered, require md5/op gates and KL validation.

## Phase 38 Gate Projection Policy

Phase 38 is a safety and scope checkpoint before any `ffn_gate_inp*` route
change. It makes the reusable inference gate stricter by default and records why
the Phase 37 SGEMM bucket should not be treated as a missed BF16 route.

Artifact:

- `/home/mudler/bench/phase38_gate_baseline/20260701_084410`

Preflight:

| check | actual |
|-------|--------|
| GPU | `NVIDIA GB10, 580.159.03` |
| docker containers | `0` |
| `local-ai-worker` containers | `0` |
| GPU compute apps | `0` |
| GPU lock owner | `FREE phase33-small-m-tile-policy-done 1782883234` |

Fresh baseline gates against the current Phase37 build:

| check | status | actual |
|-------|--------|--------|
| MoE md5 | ok | `8cb0ce23777bf55f92f63d0292c756b0` |
| dense md5 | ok | `5951a5b4d624ce891e22ab5fca9bc439` |
| `MUL_MAT` | ok | `1146/1146` |
| `MUL_MAT_ID` | ok | `806/806` |

Source comparison:

- `qwen35moe.cpp` creates `ffn_gate_inp.weight` as `[n_embd, n_expert]` and
  `ffn_gate_inp_shexp.weight` as `[n_embd]`.
- `llama-graph.cpp` computes router logits with `build_lora_mm(gate_inp, cur)`
  and labels the result `ffn_moe_logits`.
- vLLM Qwen3-Next constructs both gates as `ReplicatedLinear(...,
  quant_config=None)`, and its fused-MoE runner can concatenate router and
  shared-expert gate weights for one fused-gate forward path.

Decision:

- The `sgemm` bucket is router/shared-expert gate math kept unquantized by both
  engines. It is expected F32 policy, not an accidental cuBLAS fallback.
- Do not force BF16 or NVFP4 for `ffn_gate_inp*`.
- A future optimization can test a default-off fused gate projection that
  preserves F32 math and split semantics. Gate it with MoE/dense md5,
  `MUL_MAT`, `MUL_MAT_ID`, and KL validation if either md5 changes before any
  serving benchmark.

## Phase 39 Gate Fusion Feasibility

Phase 39 checked whether the Phase38 follow-up should be a quick graph-time
fused gate projection.

Artifacts:

- `/home/mudler/bench/phase37_cublas_name_trace/20260701_083227`
- `/home/mudler/bench/phase27_graph_node_serving/20260701_055519`
- `/home/mudler/bench/phase39_gate_sgemm_profile/phase27_reanalysis`

Evidence:

| source | result |
|--------|--------|
| Phase37 route trace | `sgemm=1212`, with per-layer `ffn_gate_inp.weight -> ffn_moe_logits` and `ffn_gate_inp_shexp.weight -> shared_expert_gate` entries |
| Phase27 serving profile | total kernel time `20.0372s` |
| Phase27 serving profile | `concat_layout=459.84ms` (`2.29%`, `2250` instances) |
| Phase27 serving profile | `cublas_bf16_gemm=1892.81ms` (`9.45%`) and `cutlass_bf16_gemm=684.01ms` (`3.41%`) |

Decision:

- Reject the quick graph-time fused gate shortcut based on `ggml_concat()` of
  the two gate weights. `concat_layout` is already a measurable serving bucket,
  so adding graph-time weight concatenation risks moving work into an existing
  bottleneck before removing enough SGEMM overhead.
- The only acceptable future fused-gate design is a persistent/load-time F32
  combined gate weight, split by output views after one matmul. It must be
  default-off, keep gate weights in F32, avoid graph-time weight concat, and
  pass MoE/dense md5 plus `MUL_MAT`/`MUL_MAT_ID` gates before any serving
  benchmark. If md5 changes, run KL first and reject on KL regression.

## Phase 40 Max-Concurrency C1 Check

Phase 40 tested the remaining C1 hypothesis from the lever map: use paged KV's
lower memory footprint to run a higher-concurrency serving point where vLLM
falls behind or fails to fit.

Artifacts:

- `/home/mudler/bench/phase40_max_concurrency_dryrun/20260701_090002`
- `/home/mudler/bench/phase40_max_concurrency/20260701_090012`

Preflight:

| check | actual |
|-------|--------|
| GPU | `NVIDIA GB10, 580.159.03` |
| docker containers | `0` |
| `local-ai-worker` containers | `0` |
| GPU compute apps | `0` |
| GPU lock owner | `FREE phase39-gate-sgemm-profile-done 1782888737` |

Harness change:

- `paged-current-serving-snapshot.sh` now accepts `BUILD_DIR` and defaults
  `BIN` from that same directory. This keeps the benchmark build step and runtime
  binaries pointed at the same CMake tree.
- Phase 40 used `BUILD_DIR=$HOME/llama-phase6-source/build-phase36`,
  `BIN=$HOME/llama-phase6-source/build-phase36/bin`,
  `OPS=MUL_MAT,MUL_MAT_ID`, `PARALLEL=256`, `CTX=262144`, `PTOK=128`,
  `GEN=64`, `NPL="128 192 256"`.

Pre/post inference gates:

| phase | check | status | actual |
|-------|-------|--------|--------|
| pre | MoE md5 | ok | `8cb0ce23777bf55f92f63d0292c756b0` |
| pre | dense md5 | ok | `5951a5b4d624ce891e22ab5fca9bc439` |
| pre | `MUL_MAT` | ok | `1146/1146` |
| pre | `MUL_MAT_ID` | ok | `806/806` |
| post | MoE md5 | ok | `8cb0ce23777bf55f92f63d0292c756b0` |
| post | dense md5 | ok | `5951a5b4d624ce891e22ab5fca9bc439` |
| post | `MUL_MAT` | ok | `1146/1146` |
| post | `MUL_MAT_ID` | ok | `806/806` |

Serving result:

| arm | n | agg t/s | decode agg t/s | decode per-seq t/s | prefill t/s | TTFT mean ms |
|-----|---|---------|----------------|--------------------|-------------|--------------|
| paged | 128 | `326.3` | `671.8` | `3.97` | `1695.2` | `8182.3` |
| paged | 192 | `318.3` | `679.9` | `2.50` | `1605.2` | `11151.6` |
| paged | 256 | `337.1` | `829.9` | `2.09` | `1525.7` | `15065.7` |
| vLLM | 128 | `654.4` | `1013.3` | `6.72` | `5206.0` | `2582.6` |
| vLLM | 192 | `697.7` | `1185.2` | `4.88` | `4787.1` | `3690.6` |
| vLLM | 256 | `714.1` | `1306.1` | `3.90` | `4471.0` | `5124.2` |

Ratios:

| n | paged decode / vLLM | paged per-seq / vLLM | paged agg / vLLM | paged TTFT / vLLM |
|---|---------------------|----------------------|------------------|-------------------|
| 128 | `0.6630` | `0.5908` | `0.4986` | `3.1682` |
| 192 | `0.5737` | `0.5123` | `0.4562` | `3.0216` |
| 256 | `0.6354` | `0.5359` | `0.4721` | `2.9401` |

Decision:

- C1 does not close GB10 parity for this workload. Paged safely serves `n=256`
  with canonical md5/op gates green before and after the run, but vLLM also
  fits and remains materially faster.
- Do not claim a GB10 parity win from higher max concurrency at
  `PTOK=128`, `GEN=64`, `n<=256`.
- The next GB10 work should stay on the profile-validated root causes:
  prefill GDN, prefill MoE GEMM, and low-concurrency/full-step graph capture.
  Any future C1 rerun must push beyond this tested point and keep the same
  md5 plus `MUL_MAT`/`MUL_MAT_ID` gates.

## Phase 41 Low-Concurrency Serving Check

Phase 41 measured the opposite serving regime after Phase40 rejected the tested
max-concurrency shortcut: low concurrency and latency-sensitive decode. This is
the regime where any remaining host/scheduler gap should be most visible.

Artifacts:

- `/home/mudler/bench/phase41_low_concurrency_dryrun/20260701_091429`
- `/home/mudler/bench/phase41_low_concurrency/20260701_091437`

Preflight:

| check | actual |
|-------|--------|
| GPU | `NVIDIA GB10, 580.159.03` |
| docker containers | `0` |
| `local-ai-worker` containers | `0` |
| GPU compute apps | `0` |
| GPU lock owner | `FREE released-by-codex-current-serving-snapshot 1782889704` |

Run shape:

- `BUILD_DIR=$HOME/llama-phase6-source/build-phase36`
- `BIN=$HOME/llama-phase6-source/build-phase36/bin`
- `OPS=MUL_MAT,MUL_MAT_ID`
- `PARALLEL=32`, `CTX=32768`, `PTOK=128`, `GEN=64`, `NPL="1 8 32"`

Pre/post inference gates:

| phase | check | status | actual |
|-------|-------|--------|--------|
| pre | MoE md5 | ok | `8cb0ce23777bf55f92f63d0292c756b0` |
| pre | dense md5 | ok | `5951a5b4d624ce891e22ab5fca9bc439` |
| pre | `MUL_MAT` | ok | `1146/1146` |
| pre | `MUL_MAT_ID` | ok | `806/806` |
| post | MoE md5 | ok | `8cb0ce23777bf55f92f63d0292c756b0` |
| post | dense md5 | ok | `5951a5b4d624ce891e22ab5fca9bc439` |
| post | `MUL_MAT` | ok | `1146/1146` |
| post | `MUL_MAT_ID` | ok | `806/806` |

Serving result:

| arm | n | agg t/s | decode agg t/s | decode per-seq t/s | prefill t/s | TTFT mean ms |
|-----|---|---------|----------------|--------------------|-------------|--------------|
| paged | 1 | `50.6` | `56.5` | `55.61` | `1221.5` | `131.8` |
| paged | 8 | `159.5` | `222.9` | `26.72` | `1438.8` | `835.9` |
| paged | 32 | `240.1` | `393.9` | `11.15` | `1615.7` | `2784.4` |
| vLLM | 1 | `67.5` | `75.4` | `74.14` | `1720.4` | `95.3` |
| vLLM | 8 | `251.8` | `296.5` | `36.12` | `4558.8` | `266.0` |
| vLLM | 32 | `454.6` | `592.4` | `17.43` | `5376.5` | `818.6` |

Ratios:

| n | paged decode / vLLM | paged per-seq / vLLM | paged agg / vLLM | paged TTFT / vLLM |
|---|---------------------|----------------------|------------------|-------------------|
| 1 | `0.7493` | `0.7501` | `0.7496` | `1.3830` |
| 8 | `0.7518` | `0.7398` | `0.6334` | `3.1425` |
| 32 | `0.6649` | `0.6397` | `0.5282` | `3.4014` |

Decision:

- The low-concurrency gap is real, but Phase41 does not reopen D1/full-step graph
  capture. Patch `0043` already ships that behavior default-on, and Phase34
  route tracing found `host_sync=0/4096` for the current n128 serving path.
  Paged is about `0.75x` vLLM decode at `n=1/8` and `0.665x` at `n=32`.
- TTFT is the bigger user-visible low-concurrency gap, especially by `n=8/32`;
  prefill GDN and MoE GEMM work therefore still matters even in a decode-focused
  serving discussion.
- Do not fund another D1 graph-capture patch on GB10 unless a fresh route trace
  first proves a host-sync fallback or graph-disable condition has returned. The
  next implementation target should be a measured non-D1 bucket, gated by the
  same md5 plus `MUL_MAT`/`MUL_MAT_ID` checks.

## Phase 42 D1/GDN/GEMM Target Reconciliation

Phase 42 challenged the Phase41 wording against the patch stack and read-only
subagent analysis. It resolves the next-target decision before any source work.

Evidence:

| track | evidence | decision |
|-------|----------|----------|
| D1/full-step graph capture | Patch `0043` is default-on for grouped MMQ decode and opt-out via `LLAMA_MOE_NO_FORCE_GRAPHS=1`; Phase34 route trace found `host_sync=0/4096`; `VLLM_PARITY_FINAL.md` marks D1 shipped and the host-sync premise refuted | closed on current GB10 path |
| S3 decode-shape-stable scheduling | Patch `0041` is shipped default-off after end-to-end A/B showed worse TTFT and lower throughput despite better per-step decode metrics | keep opt-in only |
| GDN prefill | Patches `0046`/`0047` are the shipped GB10 GDN wins; C32 slab, QS-early, and Global-Ai32 were md5-clean but slower | do not add another low-conflict GB10 GDN reorder |
| W4A16 / prefill GEMM | Patches `0033`/`0034`/`0035` are default-off; `0048`-`0050` improved forced W4A16 only marginally and did not beat default MMQ | do not add another small W4A16 body/metadata tweak |

Next target:

- The only small incremental candidate left from the current evidence is the
  persistent/load-time F32 combined gate projection scoped in Phase38/39:
  combine `ffn_gate_inp.weight` and `ffn_gate_inp_shexp.weight` once, run one
  F32 gate matmul, and split/view the output. Do not use graph-time
  `ggml_concat()`.
- It must be default-off, fork-first, and validated with MoE/dense md5,
  `MUL_MAT`, `MUL_MAT_ID`, and KL if either md5 changes before any serving
  benchmark.

## Phase 43 Persistent Gate Fusion Feasibility

Phase 43 checked whether the Phase42 "small source candidate" can really be
implemented as a low-conflict persistent/load-time combined gate tensor.

Source facts:

| path | finding |
|------|---------|
| `src/models/qwen35moe.cpp` | `ffn_gate_inp.weight` is loaded as `[n_embd, n_expert]`; `ffn_gate_inp_shexp.weight` is loaded separately as `[n_embd]` |
| `src/models/qwen35moe.cpp` | the routed gate is consumed inside `build_moe_ffn(...)`; the shared-expert gate is consumed later as a separate `build_lora_mm(ffn_gate_inp_shexp, cur)` |
| `src/llama-model-loader.cpp` | `create_tensor(...)` duplicates tensors from GGUF metadata and allocates backend buffers before `load_all_data(...)`; it has `create_tensor_as_view(...)` for views of existing GGUF tensors, not for new persistent derived tensors |
| `src/llama-model.cpp` | backend buffers are allocated from loader contexts before tensor data is loaded; adding a new persistent derived weight requires a new derived-weight allocation/materialization path, not a local Qwen graph change |

Decision:

- Reject persistent/load-time fused gate projection as a "small" GB10 shortcut.
  It is only low-conflict if the combined weight already exists in the GGUF, or
  if llama.cpp gains a general derived-weight facility. Neither is true in the
  current fork.
- Do not fall back to graph-time `ggml_concat()`; Phase39 already rejected that
  because `concat_layout` is measurable in serving.
- Do not implement a Qwen-only loader hack that reads both tensors back to host,
  allocates an extra backend weight buffer, and patches layer pointers after
  load. That is high conflict surface for a gate-only SGEMM bucket and would need
  new lifetime/state-management tests across mmap, offload, split buffers, and
  MTP blocks.
- The remaining GB10 parity work is no longer a shortcut patch. It is either a
  larger funded kernel/loader effort with its own design, or a hardware pivot
  benchmark. Any future implementation still needs the canonical MoE/dense md5,
  `MUL_MAT`, `MUL_MAT_ID`, and KL-if-md5-changes gates before benchmarking.

## Phase 44 Hardware-Pivot Harness Readiness

Phase 44 prepares the audited current-stack serving snapshot for hardware-pivot
runs without editing the harness between hosts. This is a harness-only change:
it does not modify llama.cpp inference code, patch-series source, md5 gates, op
gates, or any benchmark result.

New vLLM serving overrides:

| variable | default | vLLM flag |
|----------|---------|-----------|
| `VLLM_GPU_MEMORY_UTILIZATION` | `0.85` | `--gpu-memory-utilization` |
| `VLLM_MAX_MODEL_LEN` | `4096` | `--max-model-len` |
| `VLLM_MAX_NUM_SEQS` | `256` | `--max-num-seqs` |
| `VLLM_TENSOR_PARALLEL_SIZE` | `1` | `--tensor-parallel-size` |
| `VLLM_EXTRA_ARGS` | empty | whitespace-split args appended to `vllm serve` |

Verification scope:

- Red help-text check first proved `VLLM_MAX_NUM_SEQS` was absent from
  `paged-current-serving-snapshot.sh --help`.
- Red DGX dry-run check first proved the harness did not print
  `VLLM_MAX_NUM_SEQS=512` when the override was supplied.
- Green checks after the patch included `bash -n`, help-text grep, and DGX
  `DRY_RUN=1` preflight with the override values printed before any server
  starts. Artifact:
  `/home/mudler/bench/phase44_hardware_pivot_harness_dryrun/20260701_094038`.

Decision:

- Use the same audited harness for a future datacenter-Blackwell or other
  non-GB10 parity snapshot by overriding vLLM limits in the environment instead
  of editing the script.
- This does not reopen GB10 shortcut work and does not claim parity. A real
  hardware-pivot benchmark still needs the normal preflight, `hardware.txt`,
  pre/post MoE/dense md5 gates, `MUL_MAT`/`MUL_MAT_ID` checks, and
  KL-if-md5-changes before interpreting throughput.

## Phase 45 Inference Gate Guard

Phase 45 answers the inference-safety question after the harness-only Phase44
change by running the canonical paged inference gates on DGX. This is a
gate-only phase: it does not benchmark serving throughput and does not change
inference code.

Artifact:

- `/home/mudler/bench/phase45_inference_gate_guard/20260701_094320`

Preflight:

- Docker containers: `0`
- `local-ai-worker` containers: `0`
- GPU compute apps: `0`
- GPU lock owner: `FREE released-by-codex-current-serving-snapshot 1782890417`

Gate command:

```bash
BIN=$HOME/llama-phase6-source/build-phase36/bin \
ART=$HOME/bench/phase45_inference_gate_guard/20260701_094320 \
OPS=MUL_MAT,MUL_MAT_ID \
~/paged-inference-gates.sh
```

Results:

| check | result |
|-------|--------|
| MoE paged md5 | `8cb0ce23777bf55f92f63d0292c756b0` |
| Dense paged md5 | `5951a5b4d624ce891e22ab5fca9bc439` |
| `MUL_MAT` backend op | `1146/1146`, `Backend CUDA0: OK` |
| `MUL_MAT_ID` backend op | `806/806`, `Backend CUDA0: OK` |

Decision:

- Current DGX phase36 build still passes the canonical inference md5/op gates.
- Phase44 did not touch inference code; Phase45 provides the post-change guard
  artifact for future handoff and comparison.

## Phase 46 Served-Model-Name Harness Readiness

Phase 46 removes the remaining hardcoded `q36` model name from the audited
serving snapshot harness. This is a harness-only hardware-pivot readiness
change: it does not change llama.cpp inference code, patch-series source, md5
gates, op gates, or any throughput result.

New override:

| variable | default | used for |
|----------|---------|----------|
| `SERVED_MODEL_NAME` | `q36` | vLLM `--served-model-name`, vLLM readiness check, and h2h `--model` requests for both paged and vLLM arms |

Verification:

- Red help-text check first proved `SERVED_MODEL_NAME` was absent from
  `paged-current-serving-snapshot.sh --help`.
- Red DGX dry-run check first proved the harness did not print
  `SERVED_MODEL_NAME=dense-q36` when supplied.
- Green checks after the patch included `bash -n`, help-text grep, a source grep
  proving no hardcoded `q36` serve/request names remain in the harness, and DGX
  `DRY_RUN=1` preflight with the override value printed before any server
  starts. Artifact:
  `/home/mudler/bench/phase46_served_model_name_dryrun/20260701_094849`.

Decision:

- Future dense, MoE, or hardware-pivot snapshots can keep the same audited
  harness while setting model paths and the served OpenAI model name from the
  environment.
- This does not claim a new parity result. Full runs still require the normal
  preflight, `hardware.txt`, pre/post md5 gates, `MUL_MAT`/`MUL_MAT_ID`, and
  KL-if-md5-changes gates before interpreting throughput.

## Phase 47 Dense Serving Snapshot Attempt

Phase 47 attempted to use the Phase46 model-name override for a dense
paged-vs-vLLM serving snapshot. The first full attempt is incomplete and must
not be used as a dense parity result.

Artifacts:

- Dry-run: `/home/mudler/bench/phase47_dense_serving_dryrun/20260701_095141`
- Incomplete full attempt:
  `/home/mudler/bench/phase47_dense_serving/20260701_095151`

Run shape:

- `MODEL=$HOME/bench/q36-27b-nvfp4.gguf`
- `VLLM_MODEL=$HOME/bench/q36-27b-nvfp4-vllm`
- `SERVED_MODEL_NAME=dense-q36`
- `NPL="1 8 32 128"`, `PARALLEL=128`, `CTX=131072`, `PTOK=128`, `GEN=64`
- `OPS=MUL_MAT,MUL_MAT_ID`

Completed before failure:

- Preflight was clean: docker `0`, `local-ai-worker` `0`, GPU compute `0`.
- Pre-gates were green: MoE md5 `8cb0ce23777bf55f92f63d0292c756b0`, dense
  md5 `5951a5b4d624ce891e22ab5fca9bc439`, `MUL_MAT` `1146/1146`,
  `MUL_MAT_ID` `806/806`.
- Paged dense arm completed through `n=128`:

| n | paged decode agg t/s | paged per-seq t/s | paged agg t/s | paged TTFT ms |
|---|----------------------|-------------------|----------------|---------------|
| 1 | `13.3` | `13.14` | `12.5` | `312.3` |
| 8 | `85.5` | `10.35` | `62.5` | `2068.5` |
| 32 | `198.1` | `5.44` | `105.1` | `7608.5` |
| 128 | `361.8` | `1.89` | `143.0` | `20501.7` |

Failure/root cause:

- vLLM dense startup exceeded the old fixed `240` one-second readiness budget.
  The server log showed weight loading alone took about `199.43s`, followed by
  compile, autotune, CUDA graph capture, and multimodal warmup before the server
  began listening.
- `vllm/models.json` is empty and `models.json.err` contains an initial
  connection failure, so no vLLM result JSONs were produced.
- Cleanup then waited on the vLLM server PID after `SIGTERM`; manual cleanup was
  required. DGX was returned to idle with owner
  `FREE released-by-codex-phase47-cleanup 1782892962`.

Decision:

- Treat this artifact as a harness failure investigation, not a benchmark.
- Retry Phase47 only after the Phase48 readiness/cleanup hardening is present.

## Phase 47 Dense Serving Snapshot Retry

After Phase48 hardening, Phase47 was retried and completed successfully.

Artifact:

- `/home/mudler/bench/phase47_dense_serving_retry/20260701_100811`

Run shape:

- `MODEL=$HOME/bench/q36-27b-nvfp4.gguf`
- `VLLM_MODEL=$HOME/bench/q36-27b-nvfp4-vllm`
- `SERVED_MODEL_NAME=dense-q36`
- `NPL="1 8 32 128"`, `PARALLEL=128`, `CTX=131072`, `PTOK=128`, `GEN=64`
- `OPS=MUL_MAT,MUL_MAT_ID`, `VLLM_READY_ATTEMPTS=700`

Pre/post gates:

| phase | MoE md5 | dense md5 | `MUL_MAT` | `MUL_MAT_ID` |
|-------|---------|-----------|-----------|--------------|
| pre | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `1146/1146` | `806/806` |
| post | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `1146/1146` | `806/806` |

Results:

| arm | n | agg t/s | decode agg t/s | decode per-seq t/s | prefill t/s | TTFT ms |
|-----|---|---------|-----------------|---------------------|-------------|---------|
| paged | 1 | `12.5` | `13.3` | `13.11` | `515.1` | `312.5` |
| vLLM | 1 | `9.6` | `9.9` | `9.72` | `983.6` | `166.7` |
| paged | 8 | `61.8` | `85.2` | `10.39` | `579.5` | `2201.4` |
| vLLM | 8 | `67.6` | `73.7` | `9.04` | `2147.7` | `544.0` |
| paged | 32 | `105.9` | `198.7` | `5.44` | `595.8` | `7442.7` |
| vLLM | 32 | `171.7` | `219.9` | `6.49` | `2094.4` | `2041.9` |
| paged | 128 | `139.6` | `360.8` | `1.86` | `608.1` | `21177.2` |
| vLLM | 128 | `275.3` | `456.0` | `2.89` | `1889.6` | `6615.7` |

Ratios:

| n | paged decode / vLLM | paged per-seq / vLLM | paged agg / vLLM | paged TTFT / vLLM |
|---|---------------------|----------------------|------------------|-------------------|
| 1 | `1.3434` | `1.3488` | `1.3021` | `1.8746` |
| 8 | `1.1560` | `1.1493` | `0.9142` | `4.0467` |
| 32 | `0.9036` | `0.8382` | `0.6168` | `3.6450` |
| 128 | `0.7912` | `0.6436` | `0.5071` | `3.2011` |

Decision:

- Dense decode is ahead of vLLM at low concurrency (`n=1/8`) but falls behind
  at `n=32/128`; this mirrors the broader conclusion that low-N decode can be
  strong while prefill/TTFT and higher-concurrency serving remain gaps.
- Dense TTFT remains much worse than vLLM at all tested concurrency points, so
  dense serving does not change the GB10 conclusion or reopen closed shortcut
  work.

## Phase 48 Serving Harness Readiness Hardening

Phase 48 fixes the harness behavior exposed by the failed dense snapshot
attempt. It is a harness reliability change, not an inference change.

Changes:

- Add `LLAMA_READY_ATTEMPTS` (default `240`) and `VLLM_READY_ATTEMPTS` (default
  `600`) so slow vLLM model load/compile paths can be pre-budgeted.
- Bound each HTTP readiness probe with `curl --max-time 2` so a single probe
  cannot hang the readiness loop.
- Replace direct `kill` plus unbounded `wait` with `stop_server_pid`, which
  sends `SIGTERM`, waits up to 30 seconds, then sends `SIGKILL` before `wait`.
- Use the bounded cleanup helper for normal paged teardown, normal vLLM
  teardown, and error-path `release_lock`.

Verification:

- Red checks first proved `VLLM_READY_ATTEMPTS`, bounded curl, and hard-kill
  cleanup were absent.
- Green checks after the patch included `bash -n`, help-text grep, grep for
  `curl --max-time 2 -fsS "$url"`, grep for `kill -9 "$SERVER_PID"`, and a DGX
  dense dry-run with `VLLM_READY_ATTEMPTS=700`.
- DGX dry-run artifact:
  `/home/mudler/bench/phase48_readiness_harness_dryrun/20260701_100533`.

## Phase 49 vLLM Env Hygiene

Phase 49 cleans up benchmark log noise observed during the Phase47 retry. vLLM
warned about harness-owned environment variables such as `VLLM_READY_ATTEMPTS`
and `VLLM_MODEL` because they were inherited by the `vllm serve` process.

Change:

- Wrap `vllm serve` with `env -u` for harness-owned variables:
  `VLLM_MODEL`, `VLLM_BIN`, `VLLM_READY_ATTEMPTS`,
  `VLLM_GPU_MEMORY_UTILIZATION`, `VLLM_MAX_MODEL_LEN`, `VLLM_MAX_NUM_SEQS`,
  `VLLM_TENSOR_PARALLEL_SIZE`, and `VLLM_EXTRA_ARGS`.
- Keep intentional vLLM runtime variables such as `VLLM_LOGGING_LEVEL`.

Verification:

- Red grep first proved the scrub was absent.
- Green checks after the patch included `bash -n`, grep for `-u VLLM_MODEL`,
  and a DGX dense dry-run with `VLLM_READY_ATTEMPTS=700`.
- DGX dry-run artifact:
  `/home/mudler/bench/phase49_vllm_env_hygiene_dryrun/20260701_102138`.
