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
- Working tree: clean
- HEAD: `51168c5eee2e35348d9006f0b2fab3dc6e7c01cc`
- Base pin: `0ed235ea2c17a19fc8238668653946721ed136fd`
- Merge-base with base pin: `0ed235ea2c17a19fc8238668653946721ed136fd`
- LocalAI patch count: `38`
- LocalAI patch mirror: applies cleanly to the base pin and tree-matches fork
  HEAD.
- Tree hash after patch application: `a73d759350277532a14e853e1fe78f08bbb74ce8`

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

- Reproduce paged prefill and decode baselines.
- Find or recreate vLLM graph-node-traced difference-method decode artifacts.
