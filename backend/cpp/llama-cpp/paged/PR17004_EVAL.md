# PR #17004 (backend / GPU sampling) evaluation on DGX Spark (GB10, sm_121)

Date: 2026-06-21. Hardware: NVIDIA GB10 (GB10, sm_121), CUDA 13.0, cmake 3.28.
Model: `Qwen3-32B-Q4_K_M.gguf`. LocalAI pin: `LLAMA_VERSION=f3e182816421c648188b5eab269853bf1531d950` (2026-06-17).

## TL;DR (clean negative)

1. **PR #17004 is MERGED and is ALREADY present in our pinned llama.cpp `f3e1828`.** There is nothing to apply / cherry-pick / patch. The `-bs/--backend-sampling` CLI arg, the `llama_set_sampler` / `llama_get_sampled_*` API, and the GPU argsort/top-k/cumsum/softmax kernels are all in the pin.
2. **The prescribed benchmark cannot test the fix.** `llama-batched-bench` does ZERO sampling - it feeds random tokens (`std::rand() % n_vocab`). Its ~540 t/s plateau is therefore **not** sampling-bound, and enabling backend sampling does nothing to it. The valid tool is `llama-batched` (examples/batched), which the PR updated to drive per-sequence sampler chains and which actually exercises `-bs`.
3. **In a controlled real-sampling A/B (same `llama-batched` harness, CPU vs GPU sampler), GPU sampling gave only +25% at np=32, +3% at np=64, and CRASHED (`GGML_ASSERT(obj_new)`, graph-context alloc) at np=128 and np=256** - exactly the multi-user regime the investigation cares about.
4. **nsys at np=64: GPU kernel profile and GPU-busy time are essentially identical with and without the fix** (CPU 392.5 t/s / GPU 404.2 t/s; total GPU kernel+memop time ~4.05 s in both). Sampling kernels do not even appear among the top GPU contributors. GPU utilization did **not** rise.
5. **Conclusion: PR #17004, in the state shipped by our pin, does NOT break the ~540 plateau and does not move decode aggregate toward the ~2700 GPU-bound ceiling or past vLLM's 667.** It is modest at low parallelism and unusable (crash) at the high parallelism in question. The PR's own guidance ("recommended `--parallel 1`", "will take time to mature") matches what we measured.

## 1. What PR #17004 does + state

- Title: "sampling : add support for backend sampling". **State: MERGED** into `master` (PR head branch `gpu-sampling`). 44 files, +4133/-296.
- `libllama`: new `llama_context_params.samplers` / `n_samplers`, `llama_set_sampler`, `llama_get_sampled_*`, `llama_sampler_seq_config`, updated `llama_sampler_i`. Sampler chain can now run inside the compute graph on the backend (GPU) instead of on the CPU after `llama_decode`.
- CUDA: optimized/new `argsort`, `top-k`, `cumsum`, `softmax` kernels; CMake option `-DGGML_CUDA_CUB_3DOT2=ON` (builds a CCCL v3.2 prerelease for faster top-k).
- Tools: new `-bs, --backend-sampling` arg in `common/arg.cpp` (line 1921); server (`server-context.cpp`) per-slot wiring; `examples/batched/batched.cpp` updated.
- Supported backend samplers: `top-k`, `top-p`, `min-p`, `temp` (+ dist). **Limitations (from the PR): not compatible with grammar sampling; single output per sequence per batch; no save/load of sampling state; recommended only with `--parallel 1` and CUB_3DOT2.** Open follow-ups: #18547 (avoid graph reallocations), #18550 (skip inactive samplers in parallel decode).
- It DOES target the CPU-side per-sequence sampling stall we hypothesised - the mechanism is correct. Maturity is the problem.

Note: the GitHub API reports `mergedAt: 2026-01-04`, but the PR contains June 2026 upstream-merge commits and the feature is verified present in our 2026-06-17 pin, so treat the date field as a metadata quirk. What matters: the code is in `f3e1828`.

## 2/3. Apply + build

No apply needed (already in pin). Built from a clean `git worktree` at `f3e1828` (`~/llama-pr17004`), to avoid disturbing the existing diffusion build:

```
cmake -B build -DCMAKE_BUILD_TYPE=Release -DGGML_CUDA=ON \
  -DCMAKE_CUDA_ARCHITECTURES=121 -DLLAMA_MAX_SEQ=256 \
  -DGGML_CUDA_CUB_3DOT2=ON -DLLAMA_CURL=OFF
cmake --build build --target llama-batched llama-batched-bench -j20
```

**Build: SUCCESS** (CUB_3DOT2=ON FetchContent fetched and compiled despite flaky net; sm_121; LLAMA_MAX_SEQ=256). `-bs/--backend-sampling` confirmed present in `llama-batched --help`.

## 4. Decode aggregate: fix vs baseline vs vLLM

### 4a. `llama-batched-bench` (NO sampling - reconfirms the plateau, unaffected by the fix)
`-npp 16 -ntg 128 -npl 32,64,128,256 -c 40960 -b 2048 -ub 2048`

| npl | S_TG t/s |
|-----|----------|
| 32  | 241.8 |
| 64  | 395.1 |
| 128 | 542.6 |
| 256 | 567.2 |

Reproduces the ~540 plateau. Because this tool never samples, `-bs` is irrelevant here - the plateau is decode/host-overhead-bound, not sampling-bound.

### 4b. `llama-batched` real-sampling A/B (CPU sampler vs `-bs` GPU sampler, identical harness)
`-kvu -n 128 -np {32,64,128,256} -c 40960 --seed 1` (samplers: top-k 40 / top-p 0.95 / temp 0.8)

| np  | CPU sampling t/s | GPU `-bs` sampling t/s | delta |
|-----|------------------|------------------------|-------|
| 32  | 174.1 | 217.5 | +25% |
| 64  | 390.5 | 403.4 | +3.3% |
| 128 | 497.9 | **CRASH** `GGML_ASSERT(obj_new) ggml.c:1768` | - |
| 256 | 396.7 | **CRASH** `GGML_ASSERT(obj_new) ggml.c:1768` | - |

(`llama-batched` absolute t/s is lower than `batched-bench` because it does real sampling plus per-token detokenize/string/stream work; the A/B *within* this harness isolates the sampler cost.)

**Does the fix break the plateau? No.** GPU sampling helps only at low parallelism and the gain shrinks as np rises (+25% -> +3%), then the path crashes at np>=128 - i.e. it fails in exactly the multi-user regime where the plateau matters. It does not approach the ~2700 ceiling and does not pass vLLM's 667. The CPU-sampling curve itself peaks at np=128 (498) and *drops* at np=256 (397), confirming CPU sampling is a scaling wall - but PR #17004 as shipped does not lift it because the GPU path is unstable there.

## 5. GPU-utilization mechanism (nsys, np=64, the highest np where `-bs` survives)

`nsys profile -t cuda ... -n 96 -np 64`

| mode | decode t/s | total GPU kernel+memop time | top GPU contributors |
|------|-----------|------------------------------|----------------------|
| CPU sampling | 392.5 | ~4.07 s | mul_mat_q (55%+17%), flash_attn (5.7%), mul_mat_vec (2%) |
| GPU `-bs`    | 404.2 | ~4.04 s | identical set; sampling kernels not in top contributors |

GPU-busy time and the kernel mix are **essentially unchanged** between modes. The argsort/top-k/cumsum/softmax sampling kernels are negligible in the timeline; the only visible difference is H2D memcpy *instances* rising 1,495 -> 7,076 (pinned-memory sampler transfers) at ~unchanged total memcpy time. **GPU utilization did not rise.** This directly refutes the idea that, at this workload, the GPU idle is dominated by CPU sampler arithmetic - moving the sampler onto the GPU barely changed throughput (+3%) and did not raise GPU occupancy. The ~80% idle measured elsewhere is dominated by something other than the sampler math (host-side batch construction / synchronization / detokenize), which PR #17004 does not address.

(np=256 nsys "with fix" could not be captured: `-bs` aborts there. Fixing the crash needs the unmerged follow-ups #18547/#18550, not in our pin.)

## LocalAI adoption path

**The code arrives transparently with a version bump; enabling it is not transparent.**

- `backend/cpp/llama-cpp/prepare.sh` copies all of upstream `llama.cpp/tools/server/*` (including the #17004-modified `server-context.cpp` / `server-task.cpp` / `server-common.cpp`) into `tools/grpc-server/`, and `grpc-server.cpp` `#include`s them. So once `LLAMA_VERSION` points at a commit containing #17004 (our pin `f3e1828` already does), the backend-sampling machinery compiles into `grpc-server` automatically. **No vendored patch in `patches/` is required for the code.**
- The vendored `server-context.cpp` already does the per-slot wiring (around line 1615): `backend_sampling &= task.params.sampling.backend_sampling`, also disabled for speculative decode and for pre-sampling logits (`n_probs>0`), then `llama_set_sampler(ctx_tgt, slot.id, common_sampler_get(slot.smpl))`.
- **But it is OFF unless `task.params.sampling.backend_sampling == true`.** LocalAI's `grpc-server` builds `params` itself from the gRPC request and never sets this flag (and does not pass the upstream `--backend-sampling` CLI arg). So as-is, LocalAI compiles the feature but never uses it. **A small grpc-server change is needed**: read a LocalAI model option / env and set `params.sampling.backend_sampling = true` (global or per-request).
- For performant CUDA top-k, add `-DGGML_CUDA_CUB_3DOT2=ON` to the llama-cpp CUDA `CMAKE_ARGS` in the Makefile (optional; a non-CUB fallback exists).
- **Caveats that blunt the benefit for LocalAI specifically:** grammar-constrained requests (JSON-schema / tool calls - a large share of LocalAI traffic), `logprobs`/`n_probs>0`, and speculative decoding all fall back to CPU sampling by the gating above; and the GPU path crashes at np>=128 in this pin. So even after wiring the flag, the multi-user throughput case would not benefit (and would crash) until the follow-up PRs (#18547/#18550) land and stabilise high-parallelism backend sampling.

### Recommendation
Do **not** adopt PR #17004 as the multi-user throughput fix yet. It is already in the tree but is immature at the parallelism that matters (crashes at np>=128, modest gains below). The measured bottleneck at this workload is not the sampler arithmetic (nsys shows GPU-busy unchanged when sampling moves to GPU). Re-evaluate after #18547/#18550 merge into a future pin; revisit the host-side decode/batch-construction overhead as the more likely real lever.
