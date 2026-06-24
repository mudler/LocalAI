# CI Build Caching

Container builds — both the root LocalAI image (`Dockerfile`) and the per-backend images (`backend/Dockerfile.*`) — share a registry-backed BuildKit cache plus a layered set of prebuilt base images. This file explains how the cache is laid out, what invalidates it, and how to bypass it.

## Workflow surfaces

| Workflow | Purpose | Triggers |
|---|---|---|
| `.github/workflows/backend.yml` | Backend container images on master | `push` to master + tags, weekly Sunday cron, `workflow_dispatch` |
| `.github/workflows/backend_pr.yml` | Backend container images on PRs | `pull_request` |
| `.github/workflows/backend_build.yml` | Reusable: builds one backend (one arch) by digest | `workflow_call` from above |
| `.github/workflows/backend_merge.yml` | Reusable: assembles per-arch digests into a multi-arch manifest list | `workflow_call` |
| `.github/workflows/backend_build_darwin.yml` | Reusable: macOS-native backend builds | `workflow_call` |
| `.github/workflows/image.yml` / `image-pr.yml` | Root LocalAI image (push / PR) | push / PR |
| `.github/workflows/image_build.yml` / `image_merge.yml` | Reusable: per-arch root-image build + merge | `workflow_call` |
| `.github/workflows/base-images.yml` | Builds the prebuilt `base-grpc-*` builder bases | Saturdays 05:00 UTC cron, `workflow_dispatch`, master push touching `Dockerfile.base-grpc-builder`, `.docker/install-base-deps.sh`, `.docker/apt-mirror.sh`, or this workflow |

The matrix that drives `backend.yml` / `backend_pr.yml` lives in **`.github/backend-matrix.yml`** (data-only YAML, not embedded in the workflow). `scripts/changed-backends.js` parses it, applies path-filter logic against the PR diff (PR events) or the GitHub Compare API (push events), and emits the filtered matrix plus a `merge-matrix` for backends with multiple per-arch entries.

## Cache layout

- **Cache registry**: `quay.io/go-skynet/ci-cache`
- **One tag per matrix entry per arch**, derived from `tag-suffix` and `platform-tag`:
  - Backend builds (`backend_build.yml`): `cache<tag-suffix>-<platform-tag>`
    - e.g. `cache-cpu-faster-whisper-amd64`, `cache-cpu-faster-whisper-arm64`, `cache-gpu-nvidia-cuda-13-llama-cpp-amd64`
  - Root image builds (`image_build.yml`): `cache-localai<tag-suffix>-<platform-tag>` (with a `-core` placeholder when `tag-suffix` is empty, so `cache-localai-core-amd64` for the core image)
  - Pre-built base images (`base-images.yml`): `cache-base-grpc-<variant>` (one per `(BUILD_TYPE, arch)` permutation)
- Each tag stores a multi-arch BuildKit cache manifest (`mode=max`), so every intermediate stage is re-usable, not just the final image.

The per-arch suffix exists because amd64 and arm64 builds produce different intermediate content; sharing one cache key would thrash on every cross-arch rebuild.

## Read/write semantics

| Trigger | `cache-from` | `cache-to` |
|---|---|---|
| `push` to `master` / tag / cron / dispatch | yes | yes (`mode=max,ignore-error=true`) |
| `pull_request` | yes | **no** |

PR builds read master's warm cache but never write — this prevents PRs from polluting the shared cache with their experimental state. After merge, the master build for that matrix entry refreshes the cache.

`ignore-error=true` on the write side means a transient quay push failure does not fail the build; the next master push retries.

## Pre-built base images (`base-grpc-*`)

The C++ backend Dockerfiles (`Dockerfile.{llama-cpp,ik-llama-cpp,turboquant}`) compile gRPC from source. On a cold build that's ~25–35 min before any LocalAI source compiles. To skip that on CI, `.github/workflows/base-images.yml` builds and pushes a set of pre-prepped builder bases:

| Tag | Contents |
|---|---|
| `base-grpc-amd64` / `base-grpc-arm64` | Ubuntu 24.04 + apt build deps + protoc + cmake + gRPC at `/opt/grpc` |
| `base-grpc-cuda-12-amd64` | the above + CUDA 12.8 toolkit |
| `base-grpc-cuda-13-amd64` | the above + CUDA 13.0 toolkit (Ubuntu 22.04 base) |
| `base-grpc-cuda-13-arm64` | the above + CUDA 13.0 sbsa toolkit (Ubuntu 24.04 base) |
| `base-grpc-l4t-cuda-12-arm64` | JetPack r36.4.0 base (CUDA preinstalled, `SKIP_DRIVERS=true`) + gRPC |
| `base-grpc-rocm-amd64` | rocm/dev-ubuntu-24.04:7.2.1 base + hipblas/hipblaslt/rocblas + gRPC |
| `base-grpc-vulkan-amd64` / `base-grpc-vulkan-arm64` | Ubuntu 24.04 + Vulkan SDK 1.4.335 + gRPC |
| `base-grpc-intel-amd64` | intel/oneapi-basekit:2025.3.2 base + gRPC |

**Single source of truth**: the install logic for all 10 variants lives in `.docker/install-base-deps.sh`. Both `Dockerfile.base-grpc-builder` AND each variant Dockerfile's `builder-fromsource` stage bind-mount and execute the same script — so the prebuilt CI base and the local from-source path are bit-equivalent by construction.

### How variant Dockerfiles consume the base

`Dockerfile.{llama-cpp,ik-llama-cpp,turboquant}` are multi-target. Three stages plus a final aliasing stage:

- `builder-fromsource` — `FROM ${BASE_IMAGE}` then runs `install-base-deps.sh` and the per-backend compile script. Used when `BUILDER_TARGET=builder-fromsource` (the default; local `make backends/<name>`).
- `builder-prebuilt` — `FROM ${BUILDER_BASE_IMAGE}` (one of the prebuilt `base-grpc-*` tags) and runs only the per-backend compile script. Used when `BUILDER_TARGET=builder-prebuilt` (CI when the matrix entry sets `builder-base-image`).
- `FROM ${BUILDER_TARGET} AS builder` — alias resolves the ARG-selected stage to a fixed name (BuildKit doesn't allow ARG expansion in `COPY --from=`).
- `FROM scratch` + `COPY --from=builder ...package/. ./` — emits the final scratch image with just the package contents.

BuildKit prunes the unreferenced builder stage, so each build only runs the path it needs. `backend_build.yml` derives `BUILDER_TARGET=builder-prebuilt` automatically when the matrix entry has a non-empty `builder-base-image`; otherwise it defaults to `builder-fromsource`.

The matrix `(build-type, platforms)` → `builder-base-image` mapping for llama-cpp / ik-llama-cpp / turboquant entries:

| `build-type` | `platforms` | tag |
|---|---|---|
| `''` | `linux/amd64` | `base-grpc-amd64` |
| `''` | `linux/arm64` | `base-grpc-arm64` |
| `cublas` cuda 12 | `linux/amd64` | `base-grpc-cuda-12-amd64` |
| `cublas` cuda 13 | `linux/amd64` | `base-grpc-cuda-13-amd64` |
| `cublas` cuda 13 | `linux/arm64` | `base-grpc-cuda-13-arm64` |
| `cublas` cuda 12 + JetPack base | `linux/arm64` | `base-grpc-l4t-cuda-12-arm64` |
| `hipblas` | `linux/amd64` | `base-grpc-rocm-amd64` |
| `vulkan` | `linux/amd64` | `base-grpc-vulkan-amd64` |
| `vulkan` | `linux/arm64` | `base-grpc-vulkan-arm64` |
| `sycl_*` | `linux/amd64` | `base-grpc-intel-amd64` |

### Bootstrap order when adding a new variant

If you add a new entry to `base-images.yml`'s matrix, the new tag does not exist on quay until the workflow runs. To consume it from a variant entry safely, dispatch the base-images workflow on the branch first:

```bash
gh workflow run base-images.yml --ref <feature-branch>
```

Wait for the new variant to push, then merge the consumer change. Otherwise the consumer's CI fails with "image not found."

## Per-arch native builds + manifest merge

Multi-arch backends (and the core LocalAI image) build natively per arch instead of running both arches under QEMU emulation on a single x86 runner. The pattern:

- The matrix has TWO entries per multi-arch backend, sharing the same `tag-suffix` but distinct `platforms` + `platform-tag` + `runs-on`. Example: `-cpu-faster-whisper` has one amd64 entry on `ubuntu-latest` and one arm64 entry on `ubuntu-24.04-arm`.
- Each per-arch build pushes by **canonical digest only** (no tags) via `outputs: type=image,push-by-digest=true,name-canonical=true,push=true`. The digest is uploaded as an artifact named `digests<tag-suffix>-<platform-tag>` (or `digests-localai<...>` for root-image builds).
- `scripts/changed-backends.js` detects shared `tag-suffix` and emits a `merge-matrix` output. `backend.yml` / `backend_pr.yml` have a `backend-merge-jobs` job that consumes it and calls `backend_merge.yml`.
- `backend_merge.yml` downloads all matching digest artifacts and runs `docker buildx imagetools create` to publish the final tagged manifest list pointing at both per-arch digests. Same `docker/metadata-action` config as the original monolithic build, so consumers see no tag-shape change.
- `image_merge.yml` is the equivalent for the root LocalAI image (`-core` placeholder when `tag-suffix` is empty so the artifact-name glob doesn't over-match across `core` and `gpu-vulkan`).

**`provenance: false` is required on multi-registry digest pushes**: with the default `mode=max` provenance attestation, BuildKit bundles a per-registry attestation manifest into each registry's manifest list, making the resulting list digest diverge across registries. `steps.build.outputs.digest` only matches one of them and the merge step's `imagetools create <reg>@sha256:<digest>` lookup fails on the other. Setting `provenance: false` keeps the digest content-only and identical across registries.

## Path filter on master push

Both `backend.yml` (push) and `backend_pr.yml` (PR) generate their matrix dynamically through `scripts/changed-backends.js`:

- **PR events**: paginated `pulls/{n}/files` API → filter the matrix to entries whose `dockerfile` path prefix matches the PR diff.
- **Push events**: GitHub Compare API (`/repos/{owner}/{repo}/compare/{before}...{after}`) → same path-filter logic. Falls back to "run everything" on first-branch push (`event.before` zero), API truncation (≥300 changed files), missing API token, or any thrown error.
- **Tag pushes**: `FORCE_ALL=true` is set from the workflow side (`startsWith(github.ref, 'refs/tags/')`) — releases rebuild every backend regardless of diff.
- **Schedule / `workflow_dispatch`**: no `event.before`, falls through to "run everything" automatically.

### Shared build inputs

The per-backend prefix match only sees files under a backend's own directory, so a change to shared build infrastructure would rebuild *nothing* — an empty matrix, every job green, and the change reaching no image. That silently un-shipped PR #10946 (a partial-cuDNN packaging fix in `scripts/build/package-gpu-libs.sh`), which merged 1h48m after the weekly cron and so sat unbuilt for a week.

`SHARED_BUILD_INPUTS` in `scripts/lib/backend-filter.mjs` closes that hole. Each rule maps a shared path to the narrowest set of matrix entries it can honestly invalidate, since a full matrix is 417 Linux + 56 Darwin builds:

| Changed path | Rebuilds |
|---|---|
| `backend/backend.proto` | nothing if the edit is additive-only, otherwise everything (see below) |
| `backend/Dockerfile.<x>` | the Linux entries whose `dockerfile:` names it |
| `backend/python/common/` | Python, Linux + Darwin |
| `scripts/build/package-gpu-libs.sh` | every Linux entry (Python, Go and C++ all run it) |
| `scripts/build/<lang>-darwin.sh` | the Darwin entries that build target routes to |
| `.github/workflows/backend_build[_darwin].yml` | everything on that OS |
| anything else under `scripts/build/` (except `*_test.sh`) | everything — conservative default for unclassified packaging inputs |

Deliberately excluded: `backend/index.yaml` (gallery metadata, never enters an image), `.github/backend-matrix.yml` (adding a backend would rebuild all of them), `backend/Dockerfile.base-grpc-builder` (owned by `base-images.yml`), and the root `Makefile` (touched in ~11% of commits, and its backend-relevant edits arrive alongside the backend directory anyway). `make test-ci-scripts` pins all of this.

#### `backend/backend.proto` is content-filtered, not path-filtered

Every language consumes the proto, so a path rule for it can only ever say "rebuild all 473 images". It changes in ~1.3% of commits, and that was enough to make it the single largest CI cost driver in the repo: on 2026-07-29 four runs totalling 935 queued jobs traced to nothing but a proto edit, one of which (#11158) was a six-line diff adding `bool cache_prompt = 8;`.

An additive proto edit cannot change how a backend that never references the new symbol behaves, so `filterMatrix()` suppresses the rule for one. `changed-backends.js` fetches `backend/backend.proto` at the base revision (same contents-API pattern as `.github/backend-matrix.yml`) and hands both texts to `protoChangeIsAdditive()`, which compares them structurally rather than textually:

- **Additive, rebuilds nothing**: a new field with an unused number, a new message, a new enum value, a new RPC. Comment, whitespace and ordering changes also land here.
- **Breaking, rebuilds everything**: a removed, renumbered, retyped or renamed field, a dropped RPC, a changed `option` or `package`. So does an unresolvable base revision, matching the run-all posture used for a truncated diff.

Checked against every proto commit in the preceding six months, all nine resolvable ones classify as additive. Note the tradeoff this accepts: generated stubs do change for an additive edit, so image bytes would differ on a rebuild even though behavior does not. That is the same standard already applied when the filter declines to rebuild on unrelated `pkg/` changes, and the weekly cron remains the backstop.

The Sunday 06:00 UTC cron on `backend.yml` exists specifically because path filtering can leave Python backends frozen on stale wheels. `DEPS_REFRESH` (below) only fires when the build actually runs, so an untouched Python backend would never re-resolve its unpinned deps. The weekly cron is the safety net.

## Content-blind PRs skip the workflows that cannot see them

`backend_pr.yml` and `test-extra.yml` filter themselves (matrix generation and a `detect-changes` job), so a gallery-only or docs-only PR costs them about one job each. The Go and image workflows had no filter of any kind, so a one-line `gallery/index.yaml` edit queued 20 jobs, and a docs-only PR queued the same.

This is worth more than it looks. Measured over the week to 2026-07-30, **97% of CI wall-clock is queueing, 3% is execution** (median queue ~5h against a 4-20min median job). Cutting job count is therefore the only lever that shortens feedback time; making individual jobs faster moves 3%.

The volume is real: 13 gallery-only PRs merged that week with 10 open at once, and 78 of the 137 PRs opened were bot-generated.

`paths-ignore` on the PR trigger of `image-pr.yml` (7 jobs), `build-test.yaml` (3), `lint.yml` (2) and `tests-e2e.yml` (1) drops 13 of those 20. The excluded set:

| Path | Why no image or Go build can see it |
|---|---|
| `gallery/**` | Model-gallery metadata, parsed at runtime, never copied into an image |
| `docs/**`, `examples/**`, `**/*.md` | Never enter an image or a binary. `lint.yml` already excluded these before gallery was added |

### `backend/{cpp,go,python}/**` on `image-pr.yml` and `build-test.yaml` only

Version-pin bumps dominate PR volume: 48 `update/*` PRs in the week to 2026-07-30, from 16 pins, each a two-line diff. Most edit nothing but one `backend/*/<name>/Makefile`.

Neither of those two workflows can observe such a change. `make build` is `go build ./cmd/local-ai`, GoReleaser builds the same plus `./cmd/launcher`, and the core image's final stage ships only `entrypoint.sh`, `healthcheck.sh` and that binary. The per-backend trees are copied into the builder but nothing in them reaches the output.

What still triggers a full run, because none of it lives under those prefixes:

- `backend/backend.proto` — feeds `protogen-go`, so it does change the binary.
- `go.mod` / `go.sum` — the `go mod tidy` before-hook.
- `backend/Dockerfile.*` and anything else directly under `backend/`.

Deliberately **not** applied to:

| Workflow | Why it must keep seeing `backend/**` |
|---|---|
| `test.yml` | `TEST_PATHS` explicitly includes `./backend/go/cloud-proxy/...`, `./backend/go/local-store/...` and `./backend/go/valkey-store/...` |
| `lint.yml` | `.golangci.yml` carries `backend/`-scoped rules, so golangci-lint covers that tree |
| `tests-e2e.yml` | The e2e suite drives real backends over gRPC |
| `backend_pr.yml` | This is the workflow whose entire job is to rebuild the changed backend |

What still runs, and why it has to:

| Workflow | Why it keeps running |
|---|---|
| `test.yml` (`tests`) | `core/gallery/variants_lint_test.go` reads the real `gallery/index.yaml` and asserts the index invariants (no duplicate entry names, no build claimed by two parents). This is the only schema-level check the gallery has. |
| `yaml-check.yml` (`Yamllint`) | Lints `gallery/` for syntax. |
| `backend_pr.yml`, `test-extra.yml` | Already self-filtering; they stop after the detect step. |

Two properties this relies on:

- `paths-ignore` skips a run only when **every** changed file matches, so a PR touching the gallery *and* Go code still runs everything. That is what makes the exclusion safe rather than a hole.
- `master` carries no branch protection and no rulesets, so a skipped workflow reports no status and nothing waits on it. If required status checks are ever introduced, these four entries must be excluded from the required set or PRs will hang on "Expected — Waiting for status to be reported".

### `image.yml` on master push is gated too, by a job rather than a path filter

The same reasoning applies to master pushes, and the volume is larger there: on 2026-07-30, **12 of the 23 queued `image.yml` runs** were commits like "add 1 new model to gallery" or a docs fix, each rebuilding all 18 container images.

`image.yml` now has a `changes` job that decides once whether the push can affect any image; the other 11 jobs carry `needs: changes` plus an `if:` on its output. Verified against the shipped `Dockerfile`: the final stage copies only `entrypoint.sh`, `healthcheck.sh` and the `local-ai` binary, there is no `go:embed` of `gallery/` or `docs/`, and the gallery is fetched at runtime from `https://index.localai.io/models` (a caching mirror of `gallery/index.yaml` on master, with `github:mudler/LocalAI/gallery/index.yaml@master` as the fallback mirror). A gallery-only commit therefore produces byte-identical images, and the gallery change reaches users over the network immediately whether or not an image is rebuilt.

Two properties to preserve if you touch it:

- **It is a job gate, not `paths-ignore`.** `paths-ignore` on `push` also applies to tag pushes, and a tag created on an existing commit carries an empty commits list, which would silently skip the release image build. The gate short-circuits to "build" for `refs/tags/*`, and for any push whose base commit is missing, zero, or unresolvable.
- **The merge jobs must name the gate explicitly.** They use `if: ${{ !cancelled() && ... }}`, and `!cancelled()` is true when a dependency is *skipped*, so without the extra condition they would run and try to merge manifest lists for images that were never built.

## The `DEPS_REFRESH` cache-buster (Python backends)

Every Python backend goes through the shared `backend/Dockerfile.python`, which ends with:

```dockerfile
ARG DEPS_REFRESH=initial
RUN cd /${BACKEND} && PORTABLE_PYTHON=true make
```

Most Python backends ship `requirements*.txt` files that **do not pin every transitive dep** (`torch`, `transformers`, `vllm`, `diffusers`, etc. are listed without a `==` pin, or with `>=` lower bounds only). With a warm BuildKit cache, the `make` layer hashes only on Dockerfile instructions + COPYed source — not on what `pip install` resolves at runtime. So a warm cache would ship the *first* version of `vllm` ever cached and never pick up upstream releases.

`DEPS_REFRESH` defends against that:

- `backend_build.yml` computes `date -u +%Y-W%V` (ISO week, e.g. `2026-W19`) before each build and passes it as a build-arg.
- The `RUN ... make` layer's BuildKit hash now includes that string, so the layer invalidates **at most once per week**, automatically picking up newer wheels.
- Within a week, builds stay warm.

This applies only to `Dockerfile.python` because:
- Go (`Dockerfile.golang`) pins versions in `go.mod` / `go.sum`.
- Rust (`Dockerfile.rust`) pins via `Cargo.lock`.
- C++ backends pin gRPC (`v1.65.0`) and llama.cpp at a specific commit; their inputs don't drift between rebuilds.

### Adjusting the cadence

Bump the format to daily (`+%Y-%m-%d`) or hourly (`+%Y-%m-%d-%H`) for faster refreshes. For one-shot rebuilds without changing the schedule, append a marker to the tag-suffix in the matrix or temporarily delete that backend's cache tag in quay.

## ccache for C++ backend builds

`Dockerfile.{llama-cpp,ik-llama-cpp,turboquant}` declare a BuildKit cache mount on `/root/.ccache`:

```dockerfile
RUN --mount=type=cache,target=/root/.ccache,id=<backend>-ccache-${TARGETARCH}-${BUILD_TYPE},sharing=locked \
    bash /usr/local/sbin/compile.sh
```

The compile script exports `CMAKE_C/CXX/CUDA_COMPILER_LAUNCHER=ccache` so CMake threads ccache through gcc/g++/nvcc. Cache scope is per `(TARGETARCH, BUILD_TYPE)` so e.g. cublas-12 doesn't share with cublas-13 (their CUDA headers differ; cross-pollination would just be cache misses anyway).

### ⚠️ This ccache does nothing in CI today

This section previously claimed that `cache-to: type=registry,mode=max` "exports the cache mount data into the registry cache, so subsequent builds restore it". **That is not true.** BuildKit does not export the contents of a `--mount=type=cache` to a registry cache export. A cache mount lives in the builder's local state, and every CI job gets a fresh runner with a fresh builder, so `/root/.ccache` starts empty on every single build.

Measured on 2026-07-30 from the `ccache -s` output the compile script already prints (it runs `ccache -z` first, so the numbers are per-build):

| Job | Commit touched | Build time | ccache |
|---|---|---|---|
| 89766266951 (llama-cpp, cublas 13) | `backend/go/magpie-tts-cpp/Makefile` only | 6369s | **0 / 889 hits**, and 0 / 1778 |
| 89766267281 (llama-cpp, hipblas) | same commit | 8160s | **0 / 537 hits** |
| 90210828110 (llama-cpp, cublas 12.8) | `LLAMA_VERSION` bump | 5673s | **0 / 813 hits** |

The first two are the decisive control: commit `90355cd44` changed exactly one file, `backend/go/magpie-tts-cpp/Makefile`, nowhere near llama.cpp. The engine source was byte-identical to the previous build, which is precisely the case this section says ccache should serve, and the hit rate was still **0.00%**. A cache that was being restored but merely matching poorly would show partial hits; 0-of-N is the signature of an empty cache.

So the paragraph above about `LLAMA_VERSION` bumps reusing previous `.o` files describes an intended design that is not in effect. `Dockerfile.{llama-cpp,ik-llama-cpp,turboquant,bonsai,ds4,privacy-filter}` pay the ccache wrapper overhead and get nothing back. Multi-hour C++ rebuilds are recompiling identical translation units from scratch.

**Do not "fix" this by adding cache mounts to more Dockerfiles.** Wiring the same mount into `Dockerfile.golang` (215 of the 434 matrix entries) was measured locally at 18% faster on a rebuild after a source edit, with a 71.5% ccache hit rate — but only because the local test reused one builder across both builds. In CI it would be a no-op for exactly the reason above.

Making this actually work needs the cache to live outside the builder. The options, none of them free:

- **ccache `remote_storage`** (ccache ≥ 4.4, HTTP or Redis backend) or **sccache** with an S3/GCS/Redis backend. Genuinely works across runners; needs a cache service to point at. quay.io is a registry, not a blob store, so the existing infra does not cover it.
- **Round-trip the cache dir through `actions/cache` on the runner**: restore it, pass it in, and export it back out via a build stage output. No external infra, but clunky, and the repo already sits at GitHub's 10 GB cache ceiling while the llama-cpp ccache alone is capped at 5 GB.

Until one of those lands, treat C++ backend builds as always-cold and spend the effort on not running them instead (path filtering, see above).

## Composite actions

Two composite actions handle runner-side prep:

- **`.github/actions/free-disk-space/action.yml`** — wraps `jlumbroso/free-disk-space@main` plus an explicit apt purge of dotnet/android/ghc/mono/etc. Reclaims ~6–10 GB on `ubuntu-latest`. No-op on self-hosted runners. Used by `backend_build.yml`, `image_build.yml` and `base-images.yml` — the jobs that actually build images. Deliberately **not** used by `test.yml`, which runs no buildx step.
- **`.github/actions/setup-build-disk/action.yml`** — relocates Docker's data-root to `/mnt` on hosted X64 runners. GHA hosted `ubuntu-latest` ships ~75 GB of unused space at `/mnt`; combined with the free-disk-space cleanup this gives ~100 GB working space — enough for ROCm dev image + vLLM torch install + flash-attn intermediate layers. No-op on self-hosted and on non-X64 hosted runners. Used by `backend_build.yml`, `image_build.yml`, `base-images.yml`.

Both actions run before any docker buildx step.

## Concurrency

All `backend.yml` / `image.yml` / `test.yml` / etc. workflows use:

```yaml
concurrency:
  group: ci-<workflow>-${{ github.event.pull_request.number || github.sha }}-${{ github.repository }}
  cancel-in-progress: ${{ github.event_name == 'pull_request' }}
```

- **PR events** group by PR number → newer pushes to the same PR cancel old runs (intended).
- **Push events** group by `github.sha` → each master commit gets its own run; rapid-fire merges don't cancel each other (this was a real issue prior — two master pushes 11 seconds apart would cancel the first's CI).

## Self-warming, no separate populator

There is no cron job that pre-warms the BuildKit cache for individual backends. The production builds *are* the populators. The first master build of a given matrix entry pays the cold cost; subsequent same-entry master builds reuse everything that hasn't changed (apt installs, gRPC compile in the variant `builder-fromsource` stage or skipped entirely when consuming `base-grpc-*`, Python wheel installs, etc.). The base-images workflow's weekly cron is the closest thing to a populator and only refreshes the prebuilt builder bases.

## Manually evicting cache

To force a fully cold build for one backend or the whole image:

```bash
# Delete a single tag (requires quay credentials with admin on the repo)
curl -X DELETE \
  -H "Authorization: Bearer ${QUAY_TOKEN}" \
  https://quay.io/api/v1/repository/go-skynet/ci-cache/tag/cache-gpu-nvidia-cuda-12-vllm-amd64

# List all tags
curl -s -H "Authorization: Bearer ${QUAY_TOKEN}" \
  "https://quay.io/api/v1/repository/go-skynet/ci-cache/tag/?limit=100" | jq '.tags[].name'
```

Eviction is rarely needed in normal operation — `DEPS_REFRESH` handles weekly drift, source changes invalidate naturally, and `mode=max` keeps the cache scoped per matrix entry per arch so a stale tag never bleeds into a different build.

## What the cache does **not** cover

- The `free-disk-space` and `setup-build-disk` composite actions run on every job — these reclaim runner-state, not Docker layers, so BuildKit caches don't apply. `test.yml` deliberately does **not** use `free-disk-space`: it runs no buildx step, and the multi-GB fixture downloads that once justified it left `make test` in the test-suite reorg.
- Intermediate artifacts of `Build (PR)` are not pushed anywhere — PRs only build for verification.
- Darwin builds (see below) — macOS runners have no Docker daemon, so the registry-backed BuildKit cache cannot apply.

### The Linux Go workflows set `cache: false` on purpose

`test.yml`, `lint.yml`, `tests-e2e.yml` and friends pass `cache: false` to `actions/setup-go@v5`, unlike the darwin jobs. This looks like an oversight and is not.

Measured over the week to 2026-07-30, the `Set up Go` step has a **median of 11 seconds** on these runners. There is essentially nothing to win: the module download is not where the time goes. The expensive steps are compilation and test execution (`Test (with coverage gate)` at ~18.6min, `Test Backend E2E` at ~14.5min), and Go's build cache would have to survive across runners to touch those.

Enabling it also has a real cost. GitHub caps Actions cache at **10 GB per repo and the repo already sits at that ceiling** (31 entries), so every `setup-go` entry written by a branch with a distinct `go.sum` (222-375 MB on Linux, up to 1.4 GB on macOS) evicts something else. See the darwin cache budget below.

Before re-enabling this, measure `Set up Go` again and confirm it has actually become slow. If room is needed in the 10 GB budget, the cheapest evictions are the `docker.io--tonistiigi--binfmt` entries (~30 MB each, trivially re-fetched).

## Darwin native caches

`backend_build_darwin.yml` runs natively on `macOS-14` GitHub-hosted runners — there is no Docker, no BuildKit, no cross-job registry cache. Instead, the reusable workflow uses `actions/cache@v4` for four native caches that mirror the spirit of the Linux cache (warm by default, weekly refresh for unpinned Python deps, PRs read-only).

| Cache | Path(s) | Key | Scope |
|---|---|---|---|
| Go modules + build | `~/go/pkg/mod`, `~/Library/Caches/go-build` | `go.sum` (managed by `actions/setup-go@v5` `cache: true`) | All darwin jobs |
| Homebrew | `~/Library/Caches/Homebrew/downloads`, selected `/opt/homebrew/Cellar/*` | hash of `backend_build_darwin.yml` | All darwin jobs |
| ccache (llama.cpp CMake) | `~/Library/Caches/ccache` | pinned `LLAMA_VERSION` from `backend/cpp/llama-cpp/Makefile` | `inputs.backend == 'llama-cpp'` only |
| Python wheels (uv + pip) | `~/Library/Caches/pip`, `~/Library/Caches/uv` | `inputs.backend` + ISO week (`+%Y-W%V`) + hash of that backend's `requirements*.txt` | `inputs.lang == 'python'` only |

Read/write semantics match the BuildKit cache: `actions/cache/restore` runs every time, `actions/cache/save` is gated on `github.event_name != 'pull_request'`. PRs read master's warm cache but never write back.

The Python wheel cache uses the same ISO-week cache-buster as the Linux `DEPS_REFRESH` build-arg — same problem (unpinned `torch`/`mlx`/`diffusers`/`transformers` resolve to fresh wheels weekly), same ~one-cold-rebuild-per-week solution.

The brew Cellar cache requires `HOMEBREW_NO_AUTO_UPDATE=1` and `HOMEBREW_NO_INSTALL_CLEANUP=1` (set as job-level env). Without those, `brew install` would mutate the very directories that were just restored, defeating the cache.

**Force-link after cache restore**: `actions/cache` restores `/opt/homebrew/Cellar/*` but NOT the `/opt/homebrew/bin/*` symlinks. After a cache hit, `brew install` sees the Cellar entries and decides "already installed" without re-running its link step, leaving the formulas off PATH. The Dependencies step explicitly runs `brew link --overwrite` for every cached formula afterwards to ensure the symlinks exist.

For ccache, the workflow exports `CMAKE_ARGS=… -DCMAKE_C_COMPILER_LAUNCHER=ccache -DCMAKE_CXX_COMPILER_LAUNCHER=ccache` via `$GITHUB_ENV` before running `make build-darwin-go-backend`. The Makefile in `backend/cpp/llama-cpp/` already forwards `CMAKE_ARGS` through to each variant build (`fallback`, `grpc`, `rpc-server`), so no script changes are needed. The three variants share most TUs, so ccache dedupes object files across them.

`backend_build_darwin.yml` also has a llama-cpp-specific build-step branch that runs `make backends/llama-cpp-darwin` (the bespoke script that compiles three CMake variants and bundles dylibs via `otool`), distinct from the generic `make build-darwin-${lang}-backend` path. This was consolidated from a previously-bespoke top-level `llama-cpp-darwin` job in `backend.yml` so llama-cpp on Darwin honors the same path filter as the other 34 Darwin backends.

### Cache budget on Darwin

GitHub Actions caches are limited to 10 GB per repo. Steady-state worst case: ~800 MB Go cache + ~2 GB brew Cellar + up to 2 GB ccache + ~1.5 GB × 5 python backends. If the cap is hit, prefer collapsing the per-backend Python keys into a shared `pyenv-darwin-shared-<week>` key (accepts more cross-backend churn for a smaller footprint) before reducing other caches.

## Self-hosted runners

`.github/backend-matrix.yml` has zero references to `arc-runner-set` or `bigger-runner` — all backends run on GHA free-tier hosted runners (`ubuntu-latest` for amd64, `ubuntu-24.04-arm` for arm64 native, `macos-14` for Darwin). The migration off self-hosted relied on the per-arch native split (no QEMU emulation) plus `setup-build-disk`'s `/mnt` relocation (~100 GB working space, enough for ROCm dev image + vLLM/torch installs).

One residual self-hosted reference remains in `test-extra.yml` (`tests-vibevoice-cpp-grpc-transcription` uses `bigger-runner` for the 30s JFK-decode timeout headroom). That's a separate concern.

### Small always-on jobs routed to `arc-runner-set`

The hosted pool is shared across the whole *account*, not per repo, so a burst in one repo starves the others. On 2026-07-31 it went to **zero scheduled jobs for 35 consecutive minutes** with 39 jobs queued, while `arc-runner-set` completed 12 jobs without interruption over the same window. Actions was healthy globally at the time (other public repos were scheduling normally), so this is an account-level throttle, not an outage.

`gh-pages.yml` (`build` + `deploy`) is therefore routed to `arc-runner-set` when `github.repository == 'mudler/LocalAI'`. It needs no fork-safety clause because it only triggers on push-to-master and `workflow_dispatch`, so it never executes pull-request code. The repository guard keeps forks (which have no such runner label) from queueing forever. It fetches its own toolchains via `setup-go` / `actions-hugo` and uses no `sudo`/`apt`.

#### What the `arc-runner-set` image actually contains

Measured 2026-07-31 on run `30637392862` by a preflight step, not assumed:

| present | **absent** |
|---|---|
| `git`, `curl`, `unzip`, `tar`, `ldd`, `python3` | **`make`**, **`gcc`** |

That is why `lint.yml` is **not** on the self-hosted pool. Both of its jobs were routed there and both failed in one second: `golangci-lint` needs `make` (for `make protogen-go`, itself needing `curl`+`unzip` to fetch protoc, and for `make lint`), and `build-scripts` additionally needs a C toolchain because the packaging-script tests compile a throwaway binary and inspect it with `ldd`. Both jobs are back on `ubuntu-latest`.

The preflight steps were deliberately left in place. They cost about a second on the hosted pool and mean that whenever the runner image gains `make` + `gcc`, re-routing is one `runs-on:` line per job and any remaining gap reports itself by name rather than as an opaque mid-build failure.

Note for any future re-route: `lint.yml` also triggers on `pull_request`, and a fork PR runs untrusted contributor code. That must never reach a persistent self-hosted runner, so any re-route has to stay push-only, e.g. `${{ (github.event_name == 'push' && github.repository == 'mudler/LocalAI') && 'arc-runner-set' || 'ubuntu-latest' }}`.

## Touching the cache pipeline

When changing `image_build.yml`, `backend_build.yml`, any of the `backend/Dockerfile.*` files, `Dockerfile.base-grpc-builder`, `.docker/install-base-deps.sh`, `.docker/<backend>-compile.sh`, or `scripts/changed-backends.js`:

1. **Don't drop `DEPS_REFRESH=...` from the build-args** without a replacement strategy (lockfiles, pinned requirements). Otherwise master will silently freeze on whichever versions were cached at the time.
2. **Keep `(tag-suffix, platform-tag)` unique per matrix entry** — together they're the cache namespace. Two matrix entries sharing a key would clobber each other's cache.
3. **Keep `cache-to` gated on `github.event_name != 'pull_request'`** — PRs must not write.
4. **Keep `ignore-error=true` on `cache-to`** — quay registry hiccups must not fail builds.
5. **Keep `provenance: false` on push-by-digest steps** — multi-registry digest divergence is the Bug We Already Fixed; reintroducing provenance attestation re-breaks the merge.
6. **`install-base-deps.sh` is the single source of truth for base contents.** Both `Dockerfile.base-grpc-builder` (CI) and the variant Dockerfiles' `builder-fromsource` (local) bind-mount and execute it. If you add a package to one path, add it to the script — don't fork the logic into a Dockerfile RUN.
7. **After adding a `base-images.yml` matrix variant, run the workflow on your branch before merging consumer changes** that depend on the new tag — otherwise the consumer's CI fails "image not found."
