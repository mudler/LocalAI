# llama-cpp-localai-paged Backend (paged attention + Blackwell NVFP4 decode)

`llama-cpp-localai-paged` is LocalAI's **CUDA-only** paged-attention variant of the
llama.cpp backend. It targets high-concurrency decode for the Qwen3.6 hybrid
gated-DeltaNet (SSM) models on Blackwell (GB10 / DGX Spark). It reuses the stock
`llama-cpp` backend's sources and applies a vendored patch series on top at build
time. It is **not** a fork: a source-only `*.patch` stack plus one canonical doc.

**Canonical reference:** `backend/cpp/llama-cpp-localai-paged/README.md`
(architecture, the patch series 0001-0030, benchmarks, dev notes, generality,
pin/canary policy). Read it for any technical detail; this guide is the maintenance
how-to.

## Where things live

- `backend/cpp/llama-cpp-localai-paged/Makefile` - the thin wrapper. It copies the
  stock `backend/cpp/llama-cpp/` build infra into a build dir, clones llama.cpp at
  this backend's **own** pin (`LLAMA_VERSION`), applies the paged series via the
  `apply-paged-patches` define (strict `git apply`), then builds `grpc-server`.
- `backend/cpp/llama-cpp-localai-paged/patches/paged/` - the source-only `.patch`
  series (0001-0030), nothing else.
- `backend/cpp/llama-cpp-localai-paged/README.md` - the canonical doc. The
  operational docs (`PAGED_BITEXACT_NOTE.md`, `UPSTREAM_LAYER2_SCOPE.md`) and
  dev artifacts live in
  `backend/cpp/llama-cpp-localai-paged/docs/`.
- `backend/Dockerfile.llama-cpp-localai-paged`, `.docker/llama-cpp-localai-paged-compile.sh`
  - the CUDA build entry points.
- `backend/cpp/llama-cpp/` - the **stock** backend, pure upstream. It carries no
  paged patches.

## Invariants (do not break these)

- **Stock stays pure.** The paged patches live ONLY in this backend. Never add a
  `patches/paged/` dir or `LLAMA_PAGED` logic to `backend/cpp/llama-cpp/`.
- **CUDA-only.** Ship cublas/cuda targets only. Off-CUDA the fusions are gated off
  (patch 0030) and NVFP4 falls back to dequant, so the backend is neutral-to-
  slightly-negative there - non-CUDA users use the stock `llama-cpp`. Do not add
  cpu/vulkan/sycl/metal rows for this backend in `.github/backend-matrix.yml`.
  (Those builds also fail to link `grpc-server` on darwin/arm64 against upstream
  `stream_*` server symbols - another reason it is CUDA-only.)
- **Source-only patches.** A `.patch` may touch only llama.cpp source - never a
  dev doc or `*.md`. Strict `git apply` on a clean checkout must reach exit 0. (A
  stray `SSM_DECODE_FIX_RESULTS.md` hunk in patch 0019 once broke the CI build.)
- **Bit-exact by default.** Every shipped patch is byte-identical to the f32
  baseline. (The one opt-in precision trade, `ssm_bf16_tau` / patch 0026, was
  DROPPED: it went flat once the decode fusions landed - forcing all gated-DeltaNet
  heads to bf16 gave 780.6 vs 780.0 t/s, zero benefit - so the series is now
  bit-exact end to end. Do not reintroduce a per-head SSM-precision lever; see the
  rejected-levers note in the backend README section 5.)

## Fork-first workflow (MANDATORY)

The fork **`mudler/llama.cpp` branch `localai-paged`** is the CANONICAL source
of truth for ALL paged-backend kernel and patch work. The vendored
`patches/paged/*.patch` series is a **derivative**: the fork is the source, the
series is a generated mirror of it.

**Always update the fork FIRST, in this exact order:**

1. **Commit the change on the `localai-paged` branch and push it.** Every
   kernel or patch change lands as a fork commit first.
2. **Then regenerate the LocalAI series from the fork** via `git format-patch`
   (one patch per fork commit, source-only) into
   `backend/cpp/llama-cpp-localai-paged/patches/paged/`, so the series stays a
   **1:1, drift-free mirror** of the branch.

Hard rules, no exceptions:

- **NEVER edit the `patches/paged/*.patch` files directly.** They are generated
  output, not source.
- **NEVER add a patch to the series that has no corresponding fork-branch
  commit.** Every `.patch` must be the `git format-patch` of a real commit on
  `localai-paged`.
- The fork branch is **where the build and the per-path bit-exact md5 gate
  actually run**, so it is the **only** place a change is truly validated. A
  patch living only in the LocalAI series has never been built or gated.

Verify the mirror by tree hash: applying the full on-disk series on the pin
must reproduce the fork branch tree byte-for-byte. (The patch maintenance
detail is in `backend/cpp/llama-cpp-localai-paged/docs/PATCH_MAINTENANCE.md`;
the hard-gate is section 2.5 of `docs/PARITY_HANDOFF.md`.)

## Maintaining the pin against new llama.cpp

The pin (`LLAMA_VERSION` in the wrapper Makefile) is advanced ONLY by the manual
pin-sync. It is deliberately **excluded from the nightly auto-bumper**
(`bump_deps.yaml`): a naive bump would shift the tree out from under the patches
and break `git apply` at build time.

1. **The canary tells you when to sync.** `.github/workflows/llama-cpp-paged-canary.yml`
   runs weekly: it applies + builds the series against the latest upstream tip and
   goes **red** when upstream drifts past the patches. Canary red -> run a pin-sync.
2. **The pin-sync** (recorded in the README section 7 and git history): rebase the series onto the new
   tip (resolve conflicts; re-export **source-only** with a pathspec like
   `-- src/ ggml/ common/ include/ tools/ tests/ cmake/`), rebuild on a CUDA box,
   pass the bit-exact gate on **every** path + `test-backend-ops`, **and confirm
   the full grpc-server build/link is green on CI**, then bump `LLAMA_VERSION`.

**Hard constraint: keep the pin == the stock `llama-cpp` pin.** `grpc-server.cpp`
is shared with the stock backend and tracks the stock pin. A paged pin that
diverges PAST an upstream server-API refactor breaks the grpc-server LINK even
when the patches are byte-for-byte bit-exact - the bit-exact gate alone does NOT
catch it. The `c299a92c` bump did exactly this (patches applied + greedy-md5
bit-exact, but `grpc-server.cpp` failed to link with undefined `stream_*` server
helpers the refactor pulled into its headers), so it was reverted to `9d5d882d`.
A pin bump is shippable only once the full CI grpc-server build is green, which in
practice means moving in lockstep with the stock pin (or vendoring a
pin-matched grpc-server.cpp, which we deliberately do not, to keep stock pure).

## The bit-exact gate (run for every change)

- greedy md5: `llama-completion -m MODEL -ngl 99 -fa on -p "The capital of France is" -n 48 --temp 0 --seed 1 </dev/null | md5sum`,
  paged paths prefixed `LLAMA_KV_PAGED=1` (+ `LLAMA_MOE_FORCE_GRAPHS=1` for paged
  MoE). Must match the recorded baseline. Redirect stdin from `/dev/null` or
  `llama-completion` hangs in conversation mode.
- `test-backend-ops` (CUDA0 vs CPU oracle) for every touched op (`SSM_CONV*`,
  `GATED_DELTA_NET`, `MUL_MAT`, `MUL_MAT_ID`).
- **The gate is per-path.** The paged-MoE md5 differs from the non-paged md5 - a
  benign, KL-validated FP-accumulation-order difference (see `docs/PAGED_BITEXACT_NOTE.md`).
  Compare a paged-MoE change to the **paged** reference, not the non-paged one.

## Encapsulating your work

- When you change a kernel, follow the **Fork-first workflow** above: commit and
  push on the `localai-paged` branch first, then regenerate the `.patch`
  (source-only) from the fork so this worktree mirrors the branch byte-for-byte.
  Commit with sign-off.
- New optimization -> next patch number (gaps 0005/0027 are intentional). Update
  the README's patch table and dev notes - keep the README the single doc; do not
  scatter `*_RESULTS.md` files.
- Record rejected/flat levers in the README too (they stop the next person from
  re-running dead ends).

## Follow-ups (Metal / SYCL / Vulkan)

The decode fusions are implemented for **CUDA + CPU only**. The base
gated-DeltaNet + SSM_CONV ops already exist upstream on Metal, SYCL, and Vulkan,
so the models **run** there via the non-fused path - what is missing is the
fusion speedup. Porting it (strictly mirroring the CUDA kernels, since we have no
Metal/SYCL/Vulkan hardware to test on here) is scoped in `docs/UPSTREAM_LAYER2_SCOPE.md`
(recommended order: Metal, then SYCL, then Vulkan; ops-first upstream PR, then one
PR per backend, each gated by `test-backend-ops` on the target hardware). The
methodology for that work is in [.agents/vllm-parity-methodology.md](vllm-parity-methodology.md).
