# Plan: ship the paged llama.cpp as its OWN backend + NVFP4 Qwen3.6 gallery items

Scoping deliverable only. NOTHING is changed by this document. It is grounded in the
actual repo structure (read 2026-06-26 in worktree feat+paged-attention), not assumptions.

================================================================================
0. GROUND TRUTH (what the repo actually does today)
================================================================================

The paged patchset is ALREADY integrated into the stock llama-cpp backend in this
worktree. Two mechanisms, both already present:

  (a) BUILD: backend/cpp/llama-cpp/Makefile has `LLAMA_PAGED?=on`. The `llama.cpp:`
      target git-applies patches/0*.patch (base series) then, when LLAMA_PAGED != off,
      patches/paged/0*.patch (the 0018-0023 paged series + the earlier 0001-0017).
      prepare.sh has a fallback `patch`-based apply guarded by a sentinel
      (llama.cpp/src/paged-kv-manager.cpp). So a stock `make backends/llama-cpp` TODAY
      already ships the paged engine compiled in.

  (b) RUNTIME GATING: backend/cpp/llama-cpp/grpc-server.cpp ALREADY carries the option
      hooks (lines ~752-842). They only call setenv() before context init:
        - option `kv_paged` / `paged_kv` / `paged_attention`  -> setenv LLAMA_KV_PAGED=1
        - option `kv_paged_debug` / `paged_kv_debug`          -> setenv LLAMA_KV_PAGED_DEBUG=1
        - option `max_prefill_tokens` / `mpt` / `prefill_budget` -> setenv LLAMA_PREFILL_BUDGET
        - option `max_batch_tokens` / `mbt`                   -> setenv LLAMA_MAX_BATCH_TOKENS
        - option `prefill_cap`                                -> setenv LLAMA_PREFILL_CAP
      Against UNPATCHED llama.cpp these setenv() calls are inert (nothing reads the env),
      so grpc-server.cpp is byte-safe to share between a clean build and a paged build.
      The paged engine itself lives entirely inside the patched llama.cpp lib
      (paged-kv-manager.cpp etc.), NOT in grpc-server.cpp.

Conclusion: "stock llama-cpp + paged patchset, runtime-gated" is the CURRENT state of
ONE backend. The task is to SPLIT that into two backends:
  - llama-cpp  = clean upstream llama.cpp (de-risked: a dep-bump can never break on a
                 paged hook), grpc-server.cpp keeps the dormant hooks.
  - <newname>  = stock grpc-server.cpp + paged patch series applied + paged on.

The turboquant backend is the EXACT precedent for "a llama.cpp variant that reuses the
backend/cpp/llama-cpp grpc-server sources via a thin wrapper Makefile + its own Dockerfile
+ its own matrix rows". Copy turboquant's shape, with two simplifications (see section 1).

CPU_ALL_VARIANTS reuse: backend/cpp/llama-cpp/Makefile already has `llama-cpp-cpu-all`
(one grpc-server + dlopen libggml-cpu-*.so via -DGGML_BACKEND_DL/-DGGML_CPU_ALL_VARIANTS,
SHARED_LIBS=ON make-var). turboquant mirrors it with `turboquant-cpu-all`. The new backend
gets the same single-build CPU target for free by reusing the same Makefile machinery.

--------------------------------------------------------------------------------
RECOMMENDED BACKEND NAME: `llama-cpp-paged`  (see section 4 for the full rationale)
--------------------------------------------------------------------------------
Everywhere below, NAME = llama-cpp-paged, DOCKERFILE = Dockerfile.llama-cpp-paged,
SRC DIR = backend/cpp/llama-cpp-paged/, MAKE VAR = BACKEND_LLAMA_CPP_PAGED.
DO NOT use the dotted working name `localai-llama.cpp`: a dot in Dockerfile.<suffix> and
in the tag-suffix is unprecedented (every sibling is hyphenated: llama-cpp, ik-llama-cpp,
turboquant, ds4) and complicates the changed-backends.js endsWith() suffix matching.

================================================================================
1. NEW BACKEND - file by file
================================================================================

--------------------------------------------------------------------------------
1.1 backend/cpp/llama-cpp/Makefile  (the ONE necessary touch to stock)
--------------------------------------------------------------------------------
Change exactly one default so the STOCK image ships clean against upstream:

    -LLAMA_PAGED?=on
    +LLAMA_PAGED?=off

Why: this is the entire point of the split - stock llama-cpp must build clean so an
upstream LLAMA_VERSION bump can never fail on a paged hook. The runtime hooks in
grpc-server.cpp stay (inert). The new backend forces LLAMA_PAGED=on explicitly (1.2), so
it does not depend on this default. NOTE this DOES change stock's shipped artifact (it
currently ships paged-compiled-in-but-gated); that is intended de-risking, call it out in
the PR. If the team prefers stock literally untouched, the alternative is to leave
`?=on` and accept that stock keeps carrying the patch series - but then "clean stock" is
not achieved. Recommendation: flip to off.

(No other change to backend/cpp/llama-cpp/ - grpc-server.cpp, CMakeLists.txt, prepare.sh,
patches/, patches/paged/ are all reused as-is by the new backend.)

--------------------------------------------------------------------------------
1.2 backend/cpp/llama-cpp-paged/Makefile  (NEW - thin wrapper, model on turboquant)
--------------------------------------------------------------------------------
Mirror backend/cpp/turboquant/Makefile, but SIMPLER (two things turboquant needs that we
do NOT):
  - turboquant overrides LLAMA_REPO/LLAMA_VERSION to a fork. We use the SAME upstream pin
    as stock (it lives in backend/cpp/llama-cpp/Makefile, already auto-bumped). So we do
    NOT set LLAMA_VERSION here -> no bump_deps.yaml entry needed (big simplification vs
    turboquant). We only force LLAMA_PAGED=on.
  - turboquant runs patch-grpc-server.sh (augments the KV-cache type allow-list) and
    apply-patches.sh (fork catch-up). We need NEITHER: grpc-server.cpp already has the
    paged hooks, and the paged patch series is applied by the copied llama-cpp Makefile's
    own `llama.cpp:` target when LLAMA_PAGED=on.

Shape (one flavor shown; replicate the turboquant flavor set: avx/avx2/avx512/fallback/
cpu-all/grpc/rpc-server):

    LLAMA_CPP_DIR := $(CURRENT_MAKEFILE_DIR)/../llama-cpp

    define paged-build   # $(1)=flavor $(2)=cmake flags $(3)=target
      rm -rf $(CURRENT_MAKEFILE_DIR)/../llama-cpp-paged-$(1)-build
      cp -rf $(LLAMA_CPP_DIR) $(CURRENT_MAKEFILE_DIR)/../llama-cpp-paged-$(1)-build
      $(MAKE) -C $(CURRENT_MAKEFILE_DIR)/../llama-cpp-paged-$(1)-build purge
      # clone upstream + apply base AND paged patch series (LLAMA_PAGED=on forces it)
      LLAMA_PAGED=on $(MAKE) -C $(CURRENT_MAKEFILE_DIR)/../llama-cpp-paged-$(1)-build llama.cpp
      CMAKE_ARGS="$(CMAKE_ARGS) $(2)" TARGET="$(3)" LLAMA_PAGED=on \
        $(MAKE) -C $(CURRENT_MAKEFILE_DIR)/../llama-cpp-paged-$(1)-build grpc-server
      cp -rfv $(CURRENT_MAKEFILE_DIR)/../llama-cpp-paged-$(1)-build/grpc-server llama-cpp-paged-$(1)
    endef

    llama-cpp-paged-cpu-all:
      # identical to turboquant-cpu-all: SHARED_LIBS=ON + GGML_BACKEND_DL + CPU_ALL_VARIANTS
      # + --target ggml; then collect ggml-shared-libs/ for package.sh to bundle.
      ... LLAMA_PAGED=on SHARED_LIBS=ON \
          EXTRA_CMAKE_ARGS="-DGGML_BACKEND_DL=ON -DGGML_CPU_ALL_VARIANTS=ON" \
          TARGET="--target grpc-server --target ggml" ...

    package: ; bash package.sh
    purge:   ; rm -rf $(CURRENT_MAKEFILE_DIR)/../llama-cpp-paged-*-build; rm -rf llama-cpp-paged-* package
    clean: purge

Binaries are named llama-cpp-paged-{cpu-all,fallback,grpc,rpc-server,...} so run.sh and
package.sh glob them.

--------------------------------------------------------------------------------
1.3 backend/cpp/llama-cpp-paged/run.sh  (NEW - copy turboquant/run.sh, rename binaries)
--------------------------------------------------------------------------------
s/turboquant/llama-cpp-paged/g. Prefers llama-cpp-paged-cpu-all if present, falls back to
llama-cpp-paged-fallback; llama-cpp-paged-grpc when LLAMACPP_GRPC_SERVERS set; Darwin
DYLD_LIBRARY_PATH branch; lib/ld.so launch. Keep verbatim otherwise.

--------------------------------------------------------------------------------
1.4 backend/cpp/llama-cpp-paged/package.sh  (NEW - copy turboquant/package.sh, rename)
--------------------------------------------------------------------------------
s/turboquant/llama-cpp-paged/g. Copies llama-cpp-paged-* into package/, bundles
ggml-shared-libs/*.so* into package/lib (the CPU_ALL_VARIANTS dlopen set), copies run.sh,
and the per-arch libc/ld.so set (unchanged).

--------------------------------------------------------------------------------
1.5 backend/Dockerfile.llama-cpp-paged  (NEW - copy Dockerfile.turboquant, swap paths)
--------------------------------------------------------------------------------
Identical 3-stage structure (builder-fromsource / builder-prebuilt / FROM scratch). Edits:
  - bind/run .docker/llama-cpp-paged-compile.sh (new, 1.6) instead of turboquant-compile.sh
  - ccache id: id=llama-cpp-paged-ccache-${TARGETARCH}-${BUILD_TYPE}
    (OPTIONAL OPTIMIZATION: set id=llama-cpp-ccache-${TARGETARCH}-${BUILD_TYPE} to SHARE
     stock llama-cpp's ccache - the paged TUs are mostly byte-identical to stock, so a warm
     stock cache would give the paged build near-free object reuse. Trade-off: a regression
     in one could surface as a cold miss in the other. Recommend sharing; revisit if noisy.)
  - both `make -BC /LocalAI/backend/cpp/llama-cpp-paged package`
  - final COPY --from=builder /LocalAI/backend/cpp/llama-cpp-paged/package/. ./

--------------------------------------------------------------------------------
1.6 .docker/llama-cpp-paged-compile.sh  (NEW - copy llama-cpp-compile.sh, swap make targets)
--------------------------------------------------------------------------------
Identical to .docker/llama-cpp-compile.sh except `cd .../llama-cpp-paged` and call
`make llama-cpp-paged-cpu-all` (BUILD_TYPE empty / CPU) or `make llama-cpp-paged-fallback`
(GPU), then `make llama-cpp-paged-grpc` + `make llama-cpp-paged-rpc-server`. Keep the
arm64 gcc-14 apt step (CPU_ALL_VARIANTS armv9.2 SME needs gcc-14). ccache export unchanged.

--------------------------------------------------------------------------------
1.7 Makefile (top-level) - 6 edits, mirror the turboquant lines
--------------------------------------------------------------------------------
  a) .NOTPARALLEL (line 2): append `backends/llama-cpp-paged`
  b) Backend def (after BACKEND_TURBOQUANT, line ~1172):
       # llama-cpp-paged = stock llama.cpp grpc-server + LocalAI paged-attention patch
       # series (LLAMA_PAGED=on). Reuses backend/cpp/llama-cpp sources via a thin wrapper.
       BACKEND_LLAMA_CPP_PAGED = llama-cpp-paged|llama-cpp-paged|.|false|false
     (lang field `llama-cpp-paged` -> Dockerfile.llama-cpp-paged, matching the
      llama-cpp / ik-llama-cpp / turboquant convention where lang==backend name.)
  c) generate-docker-build-target eval (after BACKEND_TURBOQUANT, line ~1273):
       $(eval $(call generate-docker-build-target,$(BACKEND_LLAMA_CPP_PAGED)))
  d) docker-build-backends (line ~1337): append docker-build-llama-cpp-paged
  e) test-extra-backend-llama-cpp-paged target (mirror test-extra-backend-turboquant,
     line ~673): BACKEND_IMAGE=local-ai-backend:llama-cpp-paged $(MAKE) test-extra-backend
  f) (optional) backends/llama-cpp-paged-darwin target if shipping metal (mirror
     backends/llama-cpp-darwin at line 1124; see 1.11).

--------------------------------------------------------------------------------
1.8 .github/backend-matrix.yml - add rows (mirror every llama-cpp row, swap names)
--------------------------------------------------------------------------------
For EACH variant you choose to ship (see phased recommendation in section 4), add a row
copied from the corresponding llama-cpp row with:
  - backend: "llama-cpp-paged"
  - dockerfile: "./backend/Dockerfile.llama-cpp-paged"
  - tag-suffix: swap `-llama-cpp` -> `-llama-cpp-paged`
    (e.g. -cpu-llama-cpp -> -cpu-llama-cpp-paged;
           -gpu-nvidia-cuda-12-llama-cpp -> -gpu-nvidia-cuda-12-llama-cpp-paged; etc.)
  - builder-base-image: UNCHANGED - reuse the same base-grpc-* tags as llama-cpp
    (this backend compiles the same gRPC + same toolchain; no new base-images.yml variant
     is needed, so NO base-images bootstrap step). This is the cheap-variant payoff.
  - CPU: TWO per-arch rows (amd64 ubuntu-latest + arm64 ubuntu-24.04-arm) sharing
    tag-suffix '-cpu-llama-cpp-paged' so changed-backends.js emits a merge-matrix entry and
    backend-merge-jobs assembles the manifest list. Same per-arch native + manifest-merge
    pattern as -cpu-llama-cpp.
  - Darwin (if shipping): add to includeDarwin:
      - backend: "llama-cpp-paged"
        tag-suffix: "-metal-darwin-arm64-llama-cpp-paged"
        lang: "go"
    (omit build-type, exactly like the llama-cpp darwin row at line 4908.)

  REMINDER: the CI path filter only builds a backend on a PR when a file under its dir
  changes. The PR that adds this backend touches backend/cpp/llama-cpp-paged/* so it self-
  triggers. But also add the cross-trigger in 1.9 so future edits to backend/cpp/llama-cpp/
  (the shared source) retrigger this backend too.

--------------------------------------------------------------------------------
1.9 scripts/changed-backends.js - two edits (mirror turboquant exactly)
--------------------------------------------------------------------------------
  a) inferBackendPath(): add BEFORE the generic `endsWith("llama-cpp")` branch (line 56),
     next to the turboquant branch (line 45):
       if (item.dockerfile.endsWith("llama-cpp-paged")) {
         // reuses backend/cpp/llama-cpp sources via a thin wrapper Makefile
         return `backend/cpp/llama-cpp-paged/`;
       }
     ORDER MATTERS: "Dockerfile.llama-cpp-paged".endsWith("llama-cpp") is false today, but
     keep the specific branch first regardless (defensive, and returns the right path).
  b) inferBackendPathDarwin(): add a case (next to the llama-cpp one at line 66):
       if (item.backend === "llama-cpp-paged") { return `backend/cpp/llama-cpp-paged/`; }
  c) Per-backend cross-trigger (line 274-278, mirror the turboquant block):
       if (backend === "llama-cpp-paged" && !changed) {
         changed = changedFiles.some(file => file.startsWith("backend/cpp/llama-cpp/"));
       }
  Verify: node -e "... e.dockerfile.endsWith('llama-cpp-paged') ..." per adding-backends.md.

--------------------------------------------------------------------------------
1.10 backend/index.yaml - meta + image entries (META-BACKEND - capabilities map, NO uri)
--------------------------------------------------------------------------------
GOTCHA (project_backend_meta_gotcha): a backend that ships per-platform images MUST be a
meta backend = an anchor with a `capabilities:` map and NO top-level `uri:`; the concrete
per-platform entries carry the uri. Copy the *llamacpp anchor (lines 3-31).

  Step a - meta anchor in `## metas` (after *turboquant, ~line 74):
    - &llamacpppaged
      name: "llama-cpp-paged"
      alias: "llama-cpp-paged"
      license: mit
      icon: <same as llama-cpp>
      description: |
        LocalAI's paged-attention llama.cpp: on-demand paged KV cache + decode-first
        prefill budget. Stock llama.cpp grpc-server + the LocalAI paged patch series.
        Tuned for NVFP4 dense/MoE on Blackwell/GB10. Reuses the llama-cpp gRPC server.
      urls: [ https://github.com/ggerganov/llama.cpp ]
      tags: [ text-to-text, LLM, CPU, GPU, CUDA, Metal, paged-attention, nvfp4 ]
      capabilities:
        default: "cpu-llama-cpp-paged"
        nvidia: "cuda12-llama-cpp-paged"
        nvidia-cuda-12: "cuda12-llama-cpp-paged"
        nvidia-cuda-13: "cuda13-llama-cpp-paged"
        nvidia-l4t: "nvidia-l4t-arm64-llama-cpp-paged"
        nvidia-l4t-cuda-12: "nvidia-l4t-arm64-llama-cpp-paged"
        nvidia-l4t-cuda-13: "cuda13-nvidia-l4t-arm64-llama-cpp-paged"
        metal: "metal-llama-cpp-paged"
        # add amd/intel/vulkan keys ONLY for variants you actually build (section 4)

  Step b - a `-development` meta (mirror llama-cpp-development, line 1611) with the same
    capabilities map pointing at the `*-development` image names.

  Step c - concrete image entries at end of file (mirror the llama-cpp block lines
    2106-2200), one latest + one development per variant, each as:
      - !!merge <<: *llamacpppaged
        name: "cpu-llama-cpp-paged"
        uri: "quay.io/go-skynet/local-ai-backends:latest-cpu-llama-cpp-paged"
        mirrors: [ localai/localai-backends:latest-cpu-llama-cpp-paged ]
      - !!merge <<: *llamacpppaged
        name: "cpu-llama-cpp-paged-development"
        uri: "quay.io/go-skynet/local-ai-backends:master-cpu-llama-cpp-paged"
        mirrors: [ localai/localai-backends:master-cpu-llama-cpp-paged ]
      ...repeat for cuda12 / cuda13 / l4t / metal etc.
  The `latest-` / `master-` uri prefix + tag-suffix MUST match the matrix tag-suffix exactly.

--------------------------------------------------------------------------------
1.11 Darwin (only if shipping metal; the NVFP4 target is CUDA, so metal is optional/phase 2)
--------------------------------------------------------------------------------
If metal is shipped, also:
  - scripts/build/llama-cpp-paged-darwin.sh (copy scripts/build/llama-cpp-darwin.sh; it
    drives the 3 CMake variants + otool dylib bundling). Ensure it forces LLAMA_PAGED=on.
  - Makefile `backends/llama-cpp-paged-darwin` target (mirror backends/llama-cpp-darwin).
  - backend_build_darwin.yml: add the llama-cpp-paged branch (mirror the llama-cpp-specific
    step that calls `make backends/llama-cpp-darwin`).
  - index.yaml metal-llama-cpp-paged / -development image entries (already in 1.10).
  - C++ proto gotcha already handled (reuses llama-cpp CMakeLists.txt with hw_grpc_proto
    linking protobuf/grpc++), so no Homebrew-include failure.

--------------------------------------------------------------------------------
1.12 Importer / /backends/known dropdown  (drop-in, NOT a new importer)
--------------------------------------------------------------------------------
This backend consumes GGUF exactly like llama-cpp -> extend the EXISTING importer, do not
add a new one (per adding-backends.md rule 2). Edit core/gallery/importers/llama-cpp.go:
  - AdditionalBackends() (line 37): append
      {Name: "llama-cpp-paged", Modality: "text",
       Description: "Paged-attention llama.cpp (on-demand paged KV + decode-first budget)"}
  - Import() backend allow-list (line 133): add "llama-cpp-paged" to the switch case so a
      preferences.backend == "llama-cpp-paged" is honored:
        case "ik-llama-cpp", "turboquant", "llama-cpp-paged": backend = b
  - core/gallery/importers/importers_test.go: add a table case asserting the preference
    override emits backend: llama-cpp-paged (Ginkgo/Gomega; reuse an existing public GGUF
    HF fixture). Run `go test ./core/gallery/importers/...`.

--------------------------------------------------------------------------------
1.13 Docs
--------------------------------------------------------------------------------
  - docs/content/features/backends.md: add llama-cpp-paged to the text-to-text/LLM list,
    one line noting paged KV + NVFP4 Blackwell tuning. (Not an in-house from-scratch engine
    -> it is a llama.cpp variant -> do NOT add to the README maintained-engines table.)

--------------------------------------------------------------------------------
1.14 Does grpc-server.cpp need the paged hooks?  YES - already present, reused unchanged.
--------------------------------------------------------------------------------
The hooks (kv_paged / max_batch_tokens / prefill_budget / prefill_cap) are already in the
SHARED backend/cpp/llama-cpp/grpc-server.cpp. The paged backend reuses that file verbatim
(via the Makefile copy). No patch-grpc-server.sh step is needed (unlike turboquant). The
hooks are what translate the gallery `options:` (1.10 section 2) into the LLAMA_KV_PAGED /
LLAMA_MAX_BATCH_TOKENS env that the paged llama.cpp lib reads.

================================================================================
2. GALLERY ITEMS - NVFP4 Qwen3.6 dense + MoE
================================================================================

Add two entries to gallery/index.yaml. Schema (verified against existing GGUF items and
the LocalAI config structs): backend selection via `overrides.backend`; runtime knobs via
either typed config fields (context_size/f16/flash_attention/gpu_layers/batch) or the
`options:` string list (key:value, parsed by grpc-server.cpp set_option).

--------------------------------------------------------------------------------
2.1 Benchmark llama-server flags -> LocalAI model-config mapping
--------------------------------------------------------------------------------
  -c 131072                  -> context_size: 131072            (LLMConfig.ContextSize, yaml context_size)
  -fa on                     -> flash_attention: "on"           (LLMConfig.FlashAttention, yaml flash_attention; string)
  -ngl 99                    -> gpu_layers: 99                  (LLMConfig.NGPULayers, yaml gpu_layers; or omit -> DefaultNGPULayers offloads all)
  -b 2048                    -> batch: 2048                     (schema.PredictionOptions.Batch, yaml batch)  [see caveat]
  --parallel 128             -> options: ["parallel:128"]       (grpc-server.cpp:629; alias n_parallel)
  LLAMA_KV_PAGED=1           -> options: ["paged_kv:true"]      (grpc-server.cpp:778)
  LLAMA_MAX_BATCH_TOKENS=512 -> options: ["max_batch_tokens:512"] (grpc-server.cpp:821; alias mbt)
  f16 KV                     -> f16: true                       (LLMConfig.F16, yaml f16)
  (recommended for paged)    -> options: ["kv_unified:false"]   (grpc-server.cpp:746 - the per-slot paged
                                  capacity/memory benefit only materializes with a per-sequence cache;
                                  the patch comment explicitly recommends pairing paged with kv_unified:false)

  CAVEAT (-ub 512): LocalAI sets params.n_ubatch = params.n_batch = request->nbatch()
  (grpc-server.cpp:528,532). There is NO separate config field for n_ubatch, so the
  benchmark's `-b 2048 -ub 512` split is NOT exactly reproducible. Options:
    (i)  set batch: 512 -> n_batch=n_ubatch=512 (matches -ub; the decode-first
         max_batch_tokens=512 budget is the dominant prefill lever anyway, and the
         benchmark states decode throughput is budget-independent), OR
    (ii) set batch: 2048 -> n_ubatch also 2048 (bigger physical batch, more KV scratch).
  RECOMMEND (i) batch: 512 for the shipped gallery config (closest to the measured run +
  lighter memory). Flag separately: a tiny grpc-server.cpp option `n_ubatch`/`ubatch` could
  be added later to honor -b/-ub independently (not required to ship).

--------------------------------------------------------------------------------
2.2 gallery/index.yaml entry - DENSE  q36-27b-nvfp4
--------------------------------------------------------------------------------
- name: "qwen3.6-27b-nvfp4-paged"
  url: "github:mudler/LocalAI/gallery/virtual.yaml@master"
  urls:
    - https://huggingface.co/<ORG>/Qwen3.6-27B-NVFP4-GGUF      # placeholder, section 3
  description: |
    Qwen3.6-27B dense, native Blackwell NVFP4 (FP4-MMA) GGUF. Configured for LocalAI's
    paged-attention llama.cpp backend: on-demand paged KV + decode-first prefill budget.
    Benchmarked on GB10/DGX Spark at 90-117% of vLLM dense decode at 1.5-3x lower memory.
  license: "apache-2.0"                                         # confirm vs Qwen license
  tags: [ llm, gguf, nvfp4, reasoning ]
  icon: https://user-images.githubusercontent.com/1991296/230134379-7181e485-c521-4d23-a0d6-f7b3b61ba524.png
  overrides:
    backend: llama-cpp-paged
    f16: true
    flash_attention: "on"
    context_size: 131072
    gpu_layers: 99
    batch: 512                       # see -ub caveat 2.1; matches the 512 ubatch floor
    known_usecases: [ chat ]
    options:
      - use_jinja:true
      - paged_kv:true                # LLAMA_KV_PAGED=1
      - max_batch_tokens:512         # LLAMA_MAX_BATCH_TOKENS=512 (decode-first QoS budget)
      - kv_unified:false             # enables the per-slot paged capacity/memory benefit
      - parallel:128                 # --parallel 128 serving slots
    parameters:
      model: llama-cpp/models/Qwen3.6-27B-NVFP4-GGUF/q36-27b-nvfp4.gguf
    template:
      use_tokenizer_template: true
  files:
    - filename: llama-cpp/models/Qwen3.6-27B-NVFP4-GGUF/q36-27b-nvfp4.gguf
      sha256: <FILL after publish>
      uri: https://huggingface.co/<ORG>/Qwen3.6-27B-NVFP4-GGUF/resolve/main/q36-27b-nvfp4.gguf

--------------------------------------------------------------------------------
2.3 gallery/index.yaml entry - MoE  q36-35b-a3b-nvfp4
--------------------------------------------------------------------------------
Same shape; the MoE is lighter on memory (~3B active). parallel:128 + budget 256 was the
MoE decode-throughput sweet spot in the sweep, but 512 is fine as a default; if optimizing
purely for saturated MoE decode use max_batch_tokens:256.
- name: "qwen3.6-35b-a3b-nvfp4-paged"
  urls: [ https://huggingface.co/<ORG>/Qwen3.6-35B-A3B-NVFP4-GGUF ]
  ...
  overrides:
    backend: llama-cpp-paged
    f16: true
    flash_attention: "on"
    context_size: 131072
    batch: 512
    options:
      - use_jinja:true
      - paged_kv:true
      - max_batch_tokens:512          # or 256 for max saturated MoE decode (sweep winner)
      - kv_unified:false
      - parallel:128
    parameters:
      model: llama-cpp/models/Qwen3.6-35B-A3B-NVFP4-GGUF/q36-35b-a3b-nvfp4.gguf
  files:
    - filename: llama-cpp/models/Qwen3.6-35B-A3B-NVFP4-GGUF/q36-35b-a3b-nvfp4.gguf
      sha256: <FILL after publish>
      uri: https://huggingface.co/<ORG>/Qwen3.6-35B-A3B-NVFP4-GGUF/resolve/main/q36-35b-a3b-nvfp4.gguf

Note: these are the BENCHMARK serving configs. For an interactive single-user default you
may want a second lighter gallery variant (context_size 16384, parallel 4, drop the budget)
- optional, not required to ship the benchmark reproduction.

================================================================================
3. GGUF PUBLISHING (so the gallery uri: resolves)
================================================================================

The two GGUFs already exist on the DGX dev box (final_benchmark.csv references
q36-27b-nvfp4.gguf and q36-35b-a3b-nvfp4.gguf; README.md "Models" + "Benchmarks"
document provenance: dense = native Blackwell FP4 unsloth W4A4 lineage; MoE = 241 NVFP4
tensors from nvidia modelopt weights). To publish:

  1. HF repos (suggest two, under the org that owns the gallery-referenced weights):
       <ORG>/Qwen3.6-27B-NVFP4-GGUF      (single q36-27b-nvfp4.gguf)
       <ORG>/Qwen3.6-35B-A3B-NVFP4-GGUF  (single q36-35b-a3b-nvfp4.gguf)
     ORG = localai-org (brand) or mudler (personal); pick per ownership of the conversions.
  2. Upload each .gguf; compute sha256 (sha256sum) and paste into the gallery `files:` sha256
     (LocalAI verifies it on download). Without sha256 the entry still works but loses the
     integrity check - fill it.
  3. Model card metadata: base_model Qwen/Qwen3.6-*, library_name gguf, quantization NVFP4,
     pipeline_tag text-generation, license (confirm Qwen3.6 license terms - apache-2.0 vs
     Qwen community license), a note that it REQUIRES the llama-cpp-paged backend (NVFP4 +
     paged), and the GB10 benchmark table (link README.md "Benchmarks" numbers).
  4. NVFP4 requires a llama.cpp new enough to read the NVFP4 GGUF type. Confirm the pinned
     LLAMA_VERSION in backend/cpp/llama-cpp/Makefile supports NVFP4 tensor types (the dev
     tree that produced the GGUFs did). If the current pin predates NVFP4 GGUF support, the
     backend pin must be bumped OR the paged patch series must carry the NVFP4 reader. THIS
     IS A GATING CHECK before the gallery items are usable - verify on a GPU box.
  5. Provenance/licensing: the dense conversion derives from unsloth; the MoE from nvidia
     modelopt weights. Ensure redistribution of the converted GGUFs is permitted and
     attribute upstream in the card.

================================================================================
4. OPEN DECISIONS / BLOCKERS / BUILD COST
================================================================================

BACKEND NAME - RECOMMEND `llama-cpp-paged`.
  - llama-cpp-paged (RECOMMENDED): descriptive (it IS the paged variant), hyphenated like
    every sibling (llama-cpp/ik-llama-cpp/turboquant/ds4), collision-free in the
    changed-backends.js endsWith() suffix scheme, self-documenting in the /backends/known
    importer dropdown. Reads correctly next to "turboquant" and "ik-llama-cpp".
  - localai-llama-cpp (branding alternative, ACCEPTABLE): keeps the LocalAI brand without a
    dot; hyphenated and safe. Use this if marketing wants "LocalAI's own llama.cpp" framing.
    Slightly less self-explanatory about WHAT differs (paged) in the dropdown.
  - localai-llama.cpp (the working name; NOT RECOMMENDED): the dot makes Dockerfile.localai-
    llama.cpp and tag-suffix -cpu-localai-llama.cpp the only dotted ones in the repo, and
    ".cpp" looks like a file extension to the suffix matcher. Avoid.

BLOCKERS / GATING CHECKS (cannot be closed read-only, no GPU here):
  1. NVFP4 GGUF read support in the pinned LLAMA_VERSION (section 3.4). Must verify on GPU.
     If unsupported, bump the pin (which also affects stock llama-cpp) or carry the reader.
  2. The two GGUFs are not yet on HF (section 3). Gallery uri + sha256 are placeholders
     until upload. Blocks gallery validation only, not the backend build.
  3. -ub vs -b split (section 2.1) is not exactly reproducible without a tiny grpc-server
     option; shipped config uses batch:512. Minor, not a blocker.
  4. Flipping stock LLAMA_PAGED?=off changes stock's shipped artifact (de-risking, intended)
     - get explicit sign-off since it alters a heavily-used backend's build.

PLATFORM SHIP MATRIX (RECOMMENDED PHASING - the variant is cheap because it reuses the same
base-grpc-* prebuilt bases and the same compile machinery, so each row is just CI minutes):
  Phase 1 (the benchmark target - GB10/Blackwell is CUDA):
    - cuda12 amd64, cuda13 amd64, cuda13 arm64 (sbsa), l4t-cuda-12 arm64  (NVFP4/paged win)
    - cpu-all amd64 + cpu-all arm64 (the single CPU_ALL_VARIANTS build; baseline coverage)
  Phase 2 (parity with stock llama-cpp coverage, only if demand):
    - metal-darwin-arm64 (1.11), vulkan amd64/arm64, rocm amd64, intel sycl f16/f32
  Defer rocm/sycl/vulkan/metal unless asked - the paged + NVFP4 story is GPU/CUDA-centric
  and these add CI cost without a clear consumer.

BUILD-COST ESTIMATE PER PLATFORM (with warm base-grpc-* base + ccache; the paged TUs are
~byte-identical to stock so a SHARED ccache id makes most objects free):
  - CPU_ALL_VARIANTS (per arch): ~15-30 min warm / ~35-50 min cold. arm64 adds a gcc-14
    apt step. Two arches + a merge job.
  - CUDA (per arch): ~25-45 min warm / ~45-75 min cold (nvcc dominates; ccache helps less
    across CUDA arch flag changes). amd64 cuda12 + cuda13, arm64 cuda13 + l4t = 4 jobs.
  - Metal/Darwin (if Phase 2): native macos-14 runner, ~20-35 min with the ccache cache.
  - No base-images.yml change and no bootstrap dispatch (reuses existing base-grpc-* tags),
    so the only new CI cost is the per-row build minutes above. PR builds read cache, don't
    write; first master build per row pays the cold cost once, then warm.

VERIFICATION (post-implementation, needs a GPU box - out of scope here):
  - `make backends/llama-cpp-paged` builds + installs locally (from-source path).
  - Confirm stock `make backends/llama-cpp` now builds clean (no paged-kv-manager.cpp in the
    checkout) - proves the split.
  - Load a published NVFP4 GGUF via the gallery entry, hit /v1/chat/completions, confirm the
    server log shows LLAMA_KV_PAGED engaged (LLAMA_KV_PAGED_DEBUG trace) and the configured
    max_batch_tokens/parallel took effect.
  - go test ./core/gallery/importers/... green (importer drop-in case).
  - node scripts/changed-backends.js dry-run: editing backend/cpp/llama-cpp/* retriggers
    llama-cpp-paged (cross-trigger), editing backend/cpp/llama-cpp-paged/* triggers it too.

================================================================================
END OF PLAN
================================================================================
