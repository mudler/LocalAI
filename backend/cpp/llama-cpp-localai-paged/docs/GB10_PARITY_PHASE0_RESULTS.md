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
