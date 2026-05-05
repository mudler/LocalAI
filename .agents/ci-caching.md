# CI Build Caching

Container builds — both the root LocalAI image (`Dockerfile`) and the per-backend images (`backend/Dockerfile.*`) — share a registry-backed BuildKit cache. This file explains how that cache is laid out, what invalidates it, and how to bypass it.

## Cache layout

- **Cache registry**: `quay.io/go-skynet/ci-cache`
- **One tag per matrix entry**, derived from the existing `tag-suffix`:
  - Backend builds (`backend_build.yml`): `cache<tag-suffix>`
    - e.g. `cache-gpu-nvidia-cuda-12-llama-cpp`, `cache-cpu-vllm`, `cache-nvidia-l4t-cuda-13-arm64-vllm`
  - Root image builds (`image_build.yml`): `cache-localai<tag-suffix>`
    - e.g. `cache-localai-gpu-nvidia-cuda-12`, `cache-localai-gpu-vulkan`
- Each tag stores a multi-arch BuildKit cache manifest (`mode=max`), so every intermediate stage is re-usable, not just the final image.

## Read/write semantics

| Trigger | `cache-from` | `cache-to` |
|---|---|---|
| `push` to `master` / tag | yes | yes (`mode=max,ignore-error=true`) |
| `pull_request` | yes | **no** |

PR builds read master's warm cache but never write — this prevents PRs from polluting the shared cache with their experimental state. After merge, the master build for that matrix entry refreshes the cache.

`ignore-error=true` on the write side means a transient quay push failure does not fail the build; the next master push retries.

## Self-warming, no separate populator

There is no cron job that pre-warms the cache. The production builds *are* the populator. The first master build of a given matrix entry pays the cold cost; subsequent same-entry master builds reuse everything that hasn't changed (apt installs, gRPC compile in `Dockerfile.{llama-cpp,ik-llama-cpp,turboquant}`, Python wheel installs, etc.).

Historically there was a `generate_grpc_cache.yaml` cron that targeted a `grpc` stage in the root Dockerfile. That stage was removed in July 2025 and the cron silently failed every night for 9 months without writing anything. It was deleted along with the registry-cache rollout.

## The `DEPS_REFRESH` cache-buster (Python backends)

Every Python backend goes through the shared `backend/Dockerfile.python`, which ends with:

```dockerfile
ARG DEPS_REFRESH=initial
RUN cd /${BACKEND} && PORTABLE_PYTHON=true make
```

Most Python backends ship `requirements*.txt` files that **do not pin every transitive dep** (`torch`, `transformers`, `vllm`, `diffusers`, etc. are listed without a `==` pin, or with `>=` lower bounds only). With a warm BuildKit cache, the `make` layer hashes only on Dockerfile instructions + COPYed source — not on what `pip install` resolves at runtime. So a warm cache would ship the *first* version of `vllm` ever cached and never pick up upstream releases.

`DEPS_REFRESH` defends against that:

- `backend_build.yml` computes `date -u +%Y-W%V` (ISO week, e.g. `2026-W17`) before each build and passes it as a build-arg.
- The `RUN ... make` layer's BuildKit hash now includes that string, so the layer invalidates **at most once per week**, automatically picking up newer wheels.
- Within a week, builds stay warm.

This applies only to `Dockerfile.python` because:
- Go (`Dockerfile.golang`) pins versions in `go.mod` / `go.sum`.
- Rust (`Dockerfile.rust`) pins via `Cargo.lock`.
- C++ backends (`Dockerfile.{llama-cpp,ik-llama-cpp,turboquant}`) clone gRPC at a pinned tag (`v1.65.0`) and llama.cpp at a pinned commit; their inputs don't drift between rebuilds.

### Adjusting the cadence

If you need a faster refresh (e.g. while debugging an upstream flake), bump the format to daily (`+%Y-%m-%d`) or hourly (`+%Y-%m-%d-%H`). If you need a one-shot rebuild for a specific backend without changing the schedule, append a marker to the tag-suffix in the matrix or temporarily delete that backend's cache tag in quay.

## Manually evicting cache

To force a fully cold build for one backend or the whole image:

```bash
# Delete a single tag (requires quay credentials with admin on the repo)
curl -X DELETE \
  -H "Authorization: Bearer ${QUAY_TOKEN}" \
  https://quay.io/api/v1/repository/go-skynet/ci-cache/tag/cache-gpu-nvidia-cuda-12-vllm

# List all tags
curl -s -H "Authorization: Bearer ${QUAY_TOKEN}" \
  "https://quay.io/api/v1/repository/go-skynet/ci-cache/tag/?limit=100" | jq '.tags[].name'
```

Eviction is rarely needed in normal operation — `DEPS_REFRESH` handles weekly drift, source changes invalidate naturally, and `mode=max` keeps the cache scoped per matrix entry so a stale tag never bleeds into a different build.

## What the cache **does not** cover

- The "Free Disk Space" / "Release space from worker" steps run on every job — these reclaim ~6 GB on `ubuntu-latest` runners. They are runner-state cleanup, not Docker, and BuildKit caches don't apply.
- Intermediate artifacts of `Build and push (PR)` are not pushed anywhere — PRs only build for verification.
- Darwin builds (see below) — macOS runners have no Docker daemon, so the registry-backed BuildKit cache cannot apply.

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

For ccache, the workflow exports `CMAKE_ARGS=… -DCMAKE_C_COMPILER_LAUNCHER=ccache -DCMAKE_CXX_COMPILER_LAUNCHER=ccache` via `$GITHUB_ENV` before running `make build-darwin-go-backend`. The Makefile in `backend/cpp/llama-cpp/` already forwards `CMAKE_ARGS` through to each variant build (`fallback`, `grpc`, `rpc-server`), so no script changes are needed. The three variants share most TUs, so ccache dedupes object files across them.

### Cache budget on Darwin

GitHub Actions caches are limited to 10 GB per repo. Steady-state worst case: ~800 MB Go cache + ~2 GB brew Cellar + up to 2 GB ccache + ~1.5 GB × 5 python backends. If the cap is hit, prefer collapsing the per-backend Python keys into a shared `pyenv-darwin-shared-<week>` key (accepts more cross-backend churn for a smaller footprint) before reducing other caches.

## Layered base images (`localai-base`)

The registry-backed BuildKit cache deduplicates **within** a matrix entry's
cache tag, but each matrix entry has its own tag — so the same `apt-get`,
GPU SDK install, and language toolchain bootstrap runs into N different
cache tags across the backend matrix. The `localai-base` images factor that
shared work out of the per-backend builds.

### How it fits together

```
.github/backend-matrix.yaml          # raw matrix data (linux + darwin)
   │
   ▼
backend.yml / backend_pr.yml
  ├── derive-bases / generate-matrix
  │     scripts/changed-backends.js
  │       reads .github/backend-matrix.yaml
  │       (PR mode also reads changed files)
  │       emits:
  │         - matrix         (annotated with base-image-prebuilt)
  │         - matrix-darwin
  │         - bases-matrix   (deduplicated by tag-stem)
  │
  ├── build-bases  (matrix: bases-matrix)
  │     uses base_images.yml
  │       FROM .docker/bases/Dockerfile.<lang>
  │       pushes quay.io/go-skynet/localai-base:<stem>[-pr<N>]
  │
  └── backend-jobs  (matrix: matrix; needs build-bases)
        uses backend_build.yml
          FROM ${BASE_IMAGE_PREBUILT}
            i.e. quay.io/go-skynet/localai-base:<stem>[-pr<N>]
          only the backend source COPY + `make` remain.
```

The base image is **always** built before backends consume it, in the same
workflow run. There is no cross-workflow dependency, no chicken-and-egg
on first push, and no manual matrix to keep in sync — adding a backend
matrix entry is just an edit to `.github/backend-matrix.yaml`.

### Tag scheme

`<stem>` is computed by `tagStem()` in `scripts/changed-backends.js` from
the (lang, build-type, ubuntu, cuda, base-image) tuple. Arch is
intentionally NOT in the stem — bases are built multi-arch when any
consumer needs multi-arch, and single-arch otherwise (the `platforms`
field on each base entry is the union of its consumers' platforms).

| Build-type | Stem template |
|---|---|
| `''` (CPU) | `<lang>-cpu-<ubuntu>[-<base-image-slug>]` |
| `cublas` / `l4t` | `<lang>-<build-type>-<ubuntu>-cuda<major>.<minor>[-<base-image-slug>]` |
| anything else (vulkan, hipblas, intel, sycl_*) | `<lang>-<build-type>-<ubuntu>[-<base-image-slug>]` |

The base-image slug is empty for the default `ubuntu:24.04` and a short
parseable suffix otherwise (`jetpack-r36.4.0`, `rocm-7.2.1`,
`oneapi-2025.3.2`, etc.).

| Event | Pushed tag |
|---|---|
| `push` (master/tag) | `:<stem>` |
| `pull_request` | `:<stem>-pr<PR_NUMBER>` |

The cache for the base build itself lives at
`quay.io/go-skynet/ci-cache:base-<stem>` (`mode=max,ignore-error=true`),
parallel to the per-matrix-entry caches.

The script also runs a collision check across consumers of each stem: if
two consumers map to the same stem but disagree on `base-image` or
`skip-drivers` (and skip-drivers is meaningful for that build-type), the
script fails loudly. Resolve by encoding the differing input in
`tagStem()` rather than letting the dedup silently pick a winner.

### PR testability

PRs run the same pipeline as master: derive bases → build bases (tagged
`-pr<N>`) → run filtered backend matrix consuming those `-pr<N>` tags.
End-to-end validation always lives within the PR.

For PRs that only change `.docker/bases/Dockerfile.<lang>` (no backend
source touched), `changed-backends.js` adds one canary backend matrix
entry per (lang × build-type × arch × cuda × ubuntu) tuple to the filtered
matrix so each base flavour gets exercised.

### Existing language tiers

| Tier (lang) | Recipe | Consumer Dockerfile(s) | Distinct stems |
|---|---|---|---|
| `python` | `.docker/bases/Dockerfile.python` | `backend/Dockerfile.python` | 9 |
| `golang` | `.docker/bases/Dockerfile.golang` | `backend/Dockerfile.golang` | 8 |
| `cpp` | `.docker/bases/Dockerfile.cpp` (apt + GPU + protoc + cmake + GRPC) | `backend/Dockerfile.{llama-cpp,ik-llama-cpp,turboquant}` | 8 |
| `rust` | `.docker/bases/Dockerfile.rust` | `backend/Dockerfile.rust` | 1 |

The C++ trio share a single `cpp` base because they only differ in their
per-backend `make` targets. `langOf()` in `scripts/changed-backends.js`
remaps `Dockerfile.{llama-cpp,ik-llama-cpp,turboquant}` → `cpp` so dedup
works across the trio. If a future C++ consumer needs a *different* base
(e.g. without GRPC, or with a different protoc version), give it its own
`Dockerfile.<newlang>` recipe and remove it from the cpp remap.

### Adding a new (accel × arch × cuda × lang) flavour

Just add the matrix entry to `.github/backend-matrix.yaml` for the new
flavour. The bases matrix and the per-entry `base-image-prebuilt` are
derived automatically by `scripts/changed-backends.js`. Nothing else to
change.

### Adding a new language tier

1. Create `.docker/bases/Dockerfile.<lang>` mirroring an existing tier
   (apt + accel install + lang-specific toolchain).
2. Slim `backend/Dockerfile.<lang>` to `FROM ${BASE_IMAGE_PREBUILT}` plus
   the per-backend source COPY + build (no inline accel install).
3. Add the new recipe to `baseTriggerFiles` in
   `scripts/changed-backends.js` so PRs touching it fan out to canaries.
4. Add `<lang>: (item) => item.dockerfile.endsWith("<lang>")` to
   `langTriggerSelector` in the same file.
5. Add a `LOCAL_BASE_<LANG>_TAG`, a `docker-build-<lang>-base` target,
   and a clause in `local-base-tag` / `local-base-target` in `Makefile`.

The `langsWithBase` set in `scripts/changed-backends.js` is auto-detected
from the `.docker/bases/` directory at script startup, so step 1 alone is
enough for the script to start emitting bases (and annotating matrix
entries with `base-image-prebuilt`) for that lang. Steps 3–5 plug it
into the canary fan-out and the local-build path.

### Why not just rely on `mode=max` cache?

`mode=max` deduplicates at the layer level, but each matrix entry has its
own cache tag (`cache<tag-suffix>`). A change that invalidates the GPU SDK
layer in one backend does not invalidate it in any other; each entry pays
the full cost on its next rebuild. The shared base image is built once per
(accel × arch × cuda × lang), then pulled by every backend that consumes
it — that's the actual cross-matrix dedup.

### Local builds

All `backend/Dockerfile.{python,golang,cpp,rust}` consumers require
`BASE_IMAGE_PREBUILT` (no inline fallback). The Makefile wires the right
`docker-build-<lang>-base` as a prerequisite for each backend's
`docker-build-<backend>` target, so:

```bash
# Build any backend; the matching base is built first if needed.
make docker-build-vllm BUILD_TYPE=cublas CUDA_MAJOR_VERSION=12 CUDA_MINOR_VERSION=8
make docker-build-llama-cpp BUILD_TYPE=cublas CUDA_MAJOR_VERSION=13 CUDA_MINOR_VERSION=0
make docker-build-rerankers   # golang
make docker-build-kokoros     # rust
```

Or build a base directly: `make docker-build-{python,golang,cpp,rust}-base
BUILD_TYPE=...`. Or pull a pre-built one from quay if it exists for your
target tuple.

## Touching the cache pipeline

When changing `image_build.yml`, `backend_build.yml`, or any of the `backend/Dockerfile.*` files:

1. **Don't drop `DEPS_REFRESH=...` from the build-args** without a replacement strategy (lockfiles, pinned requirements). Otherwise master will silently freeze on whichever versions were cached at the time.
2. **Keep `tag-suffix` unique per matrix entry** — it's the cache namespace. Two matrix entries sharing a tag-suffix would clobber each other's cache.
3. **Keep `cache-to` gated on `github.event_name != 'pull_request'`** — PRs must not write.
4. **Keep `ignore-error=true` on `cache-to`** — quay registry hiccups must not fail builds.
5. **`tagStem()` in `scripts/changed-backends.js` is the single source of truth for base image tags.** The matrix entries are annotated with `base-image-prebuilt` in the same script run; backend-jobs reads the value as-is. There's no parallel YAML expression to keep in sync. Adding a new dimension to the stem (e.g. a slug for a new base-image variant) is a script change only.
