# Adding a New Backend

When adding a new backend to LocalAI, you need to update several files to ensure the backend is properly built, tested, and registered. Here's a step-by-step guide based on the pattern used for adding backends like `moonshine`:

## 1. Create Backend Directory Structure

Create the backend directory under the appropriate location:
- **Python backends**: `backend/python/<backend-name>/`
- **Go backends**: `backend/go/<backend-name>/`
- **C++ backends**: `backend/cpp/<backend-name>/`
- **Rust backends**: `backend/rust/<backend-name>/`

For Python backends, you'll typically need:
- `backend.py` - Main gRPC server implementation
- `Makefile` - Build configuration
- `install.sh` - Installation script for dependencies
- `protogen.sh` - Protocol buffer generation script
- `requirements.txt` - Python dependencies
- `run.sh` - Runtime script
- `test.py` / `test.sh` - Test files

For Rust backends, you'll typically need (see `backend/rust/kokoros/` as a reference):
- `Cargo.toml` - Crate manifest; depend on the upstream project as a submodule under `sources/`
- `build.rs` - Invokes `tonic_build` to generate gRPC stubs from `backend/backend.proto` (use the `BACKEND_PROTO_PATH` env var so the Makefile can inject the canonical copy)
- `src/` - The gRPC server implementation (implement `Backend` via `tonic`)
- `Makefile` - Copies `backend.proto` into the crate, runs `cargo build --release`, then `package.sh`
- `package.sh` - Uses `ldd` to bundle the binary's dynamic deps and `ld.so` into `package/lib/`
- `run.sh` - Sets `LD_LIBRARY_PATH`/`SSL_CERT_DIR` and execs the binary via the bundled `lib/ld.so`
- `sources/<UpstreamProject>/` - Git submodule with the upstream Rust crate

## 2. Add Build Configurations to `.github/backend-matrix.yml`

The build matrix is data-only YAML at `.github/backend-matrix.yml` (not inside `backend.yml` itself). `backend.yml` (master push) and `backend_pr.yml` (PR) load it via `scripts/changed-backends.js`, which also handles per-file path filtering so only touched backends rebuild on PRs and master pushes alike. Add build matrix entries to `.github/backend-matrix.yml` for each platform/GPU type you want to support. Look at similar backends for reference — `chatterbox`/`faster-whisper` for Python, `piper`/`silero-vad` for Go, `kokoros` for Rust.

**Without an entry here no image is ever built or pushed, and the gallery entry in `backend/index.yaml` will point at a tag that does not exist.** The `dockerfile:` field must point at `./backend/Dockerfile.<lang>` matching the language bucket from step 1 (e.g. `Dockerfile.python`, `Dockerfile.golang`, `Dockerfile.rust`). The `tag-suffix` must match the `uri:` in the corresponding `backend/index.yaml` image entry exactly.

**`scripts/changed-backends.js` registration — REQUIRED for any new dockerfile suffix.** This is the single most common omission, because it has no effect on the PR that adds the backend (when no prior path filter could catch it anyway) — it only breaks the *next* PR that touches your backend's directory, which then gets zero CI jobs and looks broken for unrelated reasons. Edit `scripts/changed-backends.js:inferBackendPath` and add a branch BEFORE the more-generic suffixes:

```js
if (item.dockerfile.endsWith("<your-dockerfile-suffix>")) {
    return `backend/cpp/<your-backend>/`;   // or backend/python|go|rust/...
}
```

The `endsWith()` test is against the matrix entry's `dockerfile:` value (e.g. `./backend/Dockerfile.ds4` → `endsWith("ds4")`). Specificity order matters here just like it does for importers: more-specific suffixes go BEFORE more-generic ones (e.g. `ds4` before `llama-cpp` even though both end with letters, because some upstream might one day call itself `super-ds4-llama-cpp`). Verify locally before pushing:

```bash
# Confirm your dockerfile suffix is unique enough
node -e "
const yaml = require('js-yaml'); const fs = require('fs');
const m = yaml.load(fs.readFileSync('.github/backend-matrix.yml','utf8'));
for (const e of m.include.filter(e => e.backend === '<your-backend>')) {
  console.log(e.dockerfile, '->', e.dockerfile.endsWith('<suffix>'));
}"
```

A quick way to find the right insertion point: `grep -n 'item.dockerfile.endsWith' scripts/changed-backends.js`.

**`bump_deps.yaml` registration — REQUIRED for any backend pinning an upstream commit.** If your backend's Makefile has a `*_VERSION?=<sha>` pin to a third-party repo, the daily auto-bump bot at `.github/workflows/bump_deps.yaml` won't notice it unless you register the backend in its matrix. The bot runs `.github/bump_deps.sh` which `grep`s for `^$VAR?=` in the Makefile you list — so the pin MUST live in the Makefile (not in a separate shell script). The bump for ds4 (#9761) had to walk this back because the original landed the pin in `prepare.sh`, which the bot can't see. Pattern (for `antirez/ds4`):

```yaml
# .github/workflows/bump_deps.yaml
matrix:
  include:
    - repository: "antirez/ds4"
      variable: "DS4_VERSION"
      branch: "main"
      file: "backend/cpp/ds4/Makefile"
```

And the corresponding Makefile shape (mirror `backend/cpp/llama-cpp/Makefile`):

```makefile
DS4_VERSION?=ae302c2fa18cc6d9aefc021d0f27ae03c9ad2fc0
DS4_REPO?=https://github.com/antirez/ds4
...
ds4:
	mkdir -p ds4
	cd ds4 && git init -q && \
	git remote add origin $(DS4_REPO) && \
	git fetch --depth 1 origin $(DS4_VERSION) && \
	git checkout FETCH_HEAD
```

If you have a `prepare.sh` doing the clone, delete it — the recipe belongs in the Makefile target so `make purge && make` works as a clean-and-rebuild and so the bump bot finds the pin.

**Placement in file:**
- CPU builds: Add after other CPU builds (e.g., after `cpu-chatterbox`)
- CUDA 12 builds: Add after other CUDA 12 builds (e.g., after `gpu-nvidia-cuda-12-chatterbox`)
- CUDA 13 builds: Add after other CUDA 13 builds (e.g., after `gpu-nvidia-cuda-13-chatterbox`)

**Additional build types you may need:**
- ROCm/HIP: Use `build-type: 'hipblas'` with `base-image: "rocm/dev-ubuntu-24.04:7.2.1"`
- Intel/SYCL: Use `build-type: 'intel'` or `build-type: 'sycl_f16'`/`sycl_f32` with `base-image: "intel/oneapi-basekit:2025.3.2-0-devel-ubuntu24.04"`
- L4T (ARM): Use `build-type: 'l4t'` with `platforms: 'linux/arm64'` and `runs-on: 'ubuntu-24.04-arm'`

**Per-arch native builds (`linux/amd64` + `linux/arm64`):**

Multi-arch backends are NOT a single matrix entry with `platforms: 'linux/amd64,linux/arm64'`. Instead, add **two** entries — one with `platforms: 'linux/amd64'` + `platform-tag: 'amd64'` + `runs-on: 'ubuntu-latest'`, one with `platforms: 'linux/arm64'` + `platform-tag: 'arm64'` + `runs-on: 'ubuntu-24.04-arm'` — both sharing the same `tag-suffix`. The script detects the shared `tag-suffix` and emits a `merge-matrix` entry, so `backend-merge-jobs` (in `backend.yml`/`backend_pr.yml`) automatically assembles the manifest list from per-arch digest artifacts. See `-cpu-faster-whisper` in `.github/backend-matrix.yml` for a reference shape.

**llama-cpp / ik-llama-cpp / turboquant variants only — `builder-base-image`:**

Entries whose `dockerfile` is `./backend/Dockerfile.{llama-cpp,ik-llama-cpp,turboquant}` must also set a `builder-base-image` field pointing at a prebuilt base from `quay.io/go-skynet/ci-cache:base-grpc-*` (CI builds these via `.github/workflows/base-images.yml`). The mapping is by `(build-type, platforms)` — see existing entries for the pattern. CI uses these prebuilt bases to skip the gRPC compile (~25–35 min cold). Local `make backends/<name>` ignores `builder-base-image` and uses the from-source path inside the Dockerfile, so you don't need quay access for local builds.

### Cover every OS the project supports (Linux **and** Darwin)

`.github/backend-matrix.yml` has two matrices, and they are the source of truth for which OS a backend ships on:

- `include:` — the **Linux** matrix (x86_64 + arm64; CPU and CUDA / ROCm / SYCL / Vulkan).
- `includeDarwin:` — the **macOS / Apple Silicon** matrix (arm64; Metal where the engine supports it, otherwise a native arm64 CPU build).

**A new backend must target every OS it can build for — do not ship Linux-only by default.** A backend that appears only under `include:` is silently unavailable on macOS even when its code would run there. Most C/C++/GGML engines build on Darwin out of the box (ggml defaults `GGML_METAL=ON` on Apple, so a plain build is Metal-enabled), and many Python backends do too (CPU / MPS wheels). If a backend genuinely cannot support an OS (e.g. CUDA-only, no CPU variant), state that in the PR description instead of omitting it silently.

Wiring a backend into `includeDarwin:` is more than the matrix entry:

1. **`includeDarwin:` entry** — `tag-suffix: "-metal-darwin-arm64-<backend>"`, `build-type: "metal"`, `lang: "go"` for go+ggml backends; omit `build-type` for the bespoke C++ ones (llama-cpp / ds4 / privacy-filter). Match an existing entry of the same shape.
2. **`backend/index.yaml`** — add `metal:` to the backend's `capabilities` map (main and `-development`) and concrete `metal-<backend>` / `metal-<backend>-development` image entries pointing at the `-metal-darwin-arm64-<backend>` images.
3. **C/C++ backends only** — add an `inferBackendPathDarwin` case in `scripts/changed-backends.js` returning `backend/cpp/<backend>/` (the generic fallthrough assumes `backend/<lang>/`, which is wrong for a C++ source tree driven with `lang: go`), and give `run.sh` a Darwin branch that exports `DYLD_LIBRARY_PATH` instead of `LD_LIBRARY_PATH`. If the build is bespoke (single `grpc-server` + dylib bundling), model it on `scripts/build/ds4-darwin.sh` and add a `backends/<backend>-darwin` make target plus a gated step in `.github/workflows/backend_build_darwin.yml`.
4. **C++ proto gotcha** — if the backend compiles the generated gRPC/protobuf in a separate CMake target (e.g. `hw_grpc_proto`), that target must link `protobuf::libprotobuf` + `gRPC::grpc++` so the Homebrew include dirs propagate; otherwise macOS fails with `google/protobuf/runtime_version.h not found` (Linux hides this because apt headers sit in `/usr/include`).

The CI path filter only builds a backend on a PR when a file under its directory changes, so a darwin-only YAML edit builds nothing — touch a file under `backend/<lang>/<backend>/` (a one-line comment is enough) in the same PR.

## 3. Add Backend Metadata to `backend/index.yaml`

**Step 3a: Add Meta Definition**

Add a YAML anchor definition in the `## metas` section (around line 2-300). Look for similar backends to use as a template such as `diffusers` or `chatterbox`

**Step 3b: Add Image Entries**

Add image entries at the end of the file, following the pattern of similar backends such as `diffusers` or `chatterbox`. Include both `latest` (production) and `master` (development) tags.

**Note on integrity:** OCI backends installed from a gallery whose `verification:` block is set are verified against a keyless-cosign policy before extraction; tarball/HTTP backends use the optional `sha256:` field. New backends do not need any extra YAML — the gallery-level `verification:` block covers every entry. See [.agents/backend-signing.md](backend-signing.md) for the producer-side CI step.

## 4. Update the Makefile

The Makefile needs to be updated in several places to support building and testing the new backend:

**Step 4a: Add to `.NOTPARALLEL`**

Add `backends/<backend-name>` to the `.NOTPARALLEL` line (around line 2) to prevent parallel execution conflicts:

```makefile
.NOTPARALLEL: ... backends/<backend-name>
```

**Step 4b: Add to `prepare-test-extra`**

Add the backend to the `prepare-test-extra` target to prepare it for testing. Use the path matching your language bucket (`backend/python/`, `backend/go/`, `backend/rust/`, …):

```makefile
prepare-test-extra: protogen-python
	...
	$(MAKE) -C backend/<lang>/<backend-name>
```

For Rust backends the target is usually the crate build target itself (e.g. `$(MAKE) -C backend/rust/<backend-name> <backend-name>-grpc`) so the binary is in place before `test` runs.

**Step 4c: Add to `test-extra`**

Add the backend to the `test-extra` target to run its tests — applies to Go and Rust backends too, not only Python:

```makefile
test-extra: prepare-test-extra
	...
	$(MAKE) -C backend/<lang>/<backend-name> test
```

Each backend's own `Makefile` should define a `test` target so this line works regardless of language. Integration tests that need large model downloads should be gated behind an env var (see `backend/rust/kokoros/`'s `KOKOROS_MODEL_PATH` pattern) so CI only runs unit tests.

**Step 4d: Add Backend Definition**

Add a backend definition variable in the backend definitions section (around line 428-457). The format depends on the backend type:

**For Python backends with root context** (like `faster-whisper`, `coqui`):
```makefile
BACKEND_<BACKEND_NAME> = <backend-name>|python|.|false|true
```

**For Python backends with `./backend` context** (like `chatterbox`, `moonshine`):
```makefile
BACKEND_<BACKEND_NAME> = <backend-name>|python|./backend|false|true
```

**For Go backends**:
```makefile
BACKEND_<BACKEND_NAME> = <backend-name>|golang|.|false|true
```

**For Rust backends**:
```makefile
BACKEND_<BACKEND_NAME> = <backend-name>|rust|.|false|true
```

The language field (`python`/`golang`/`rust`/…) must match a `backend/Dockerfile.<lang>` file.

**Step 4e: Generate Docker Build Target**

Add an eval call to generate the docker-build target (around line 480-501):

```makefile
$(eval $(call generate-docker-build-target,$(BACKEND_<BACKEND_NAME>)))
```

**Step 4f: Add to `docker-build-backends`**

Add `docker-build-<backend-name>` to the `docker-build-backends` target (around line 507):

```makefile
docker-build-backends: ... docker-build-<backend-name>
```

**Determining the Context:**

- If the backend is in `backend/python/<backend-name>/` and uses `./backend` as context in the workflow file, use `./backend` context
- If the backend is in `backend/python/<backend-name>/` but uses `.` as context in the workflow file, use `.` context
- Check similar backends to determine the correct context

## Engine preference for gallery model variants

A gallery entry can declare `variants`, alternative builds of the same weights,
and LocalAI picks one per host: it drops builds whose backend cannot run here or
that do not fit memory, then ranks the survivors by **engine preference first,
size second** (`SelectVariant` in `core/gallery/resolve_variant.go`).

Ask whether your backend should outrank another one on some hardware. If it
should, add it to `engineNamePreferenceRules` in `pkg/system/capabilities.go`,
best engine first for that capability:

```go
 	{Nvidia, []string{engineVLLM, engineSGLang, engineLlamaCpp}},
+	{Nvidia, []string{engineVLLM, engineSGLang, engineMyEngine, engineLlamaCpp}},
```

That is the ENGINE NAME table, matched against a gallery entry's `backend:`
value. The `backendBuildTagPreferenceRules` table next to it is a different
vocabulary: build tags (`cuda`, `rocm`, `metal`) matched against installed build
directory names for alias resolution. **Putting an engine name in the build tag
table, or a build tag in the engine table, matches nothing and does not error**:
every candidate scores equal and size alone decides, so the preference silently
stops existing. The block comment above both tables spells the contract out.

Leaving your backend out is a valid choice when no ordering can be justified for
it. It then ranks below every known engine and selection falls back to size,
which is the behaviour that predates preference.

## Documenting the backend (README + docs)

A backend is not "added" until it is discoverable. Update the user-facing docs:

- **`docs/content/features/backends.md`** - add the backend to the right
  category in the "LocalAI supports various types of backends" list (and add a
  new category if it introduces a new modality, e.g. sound classification).
- If the backend introduces a **new API surface** (a new endpoint or a realtime
  capability), document it under `docs/content/` where its area lives (audio,
  vision, etc.) and follow the api-endpoints checklist in
  [api-endpoints-and-auth.md](api-endpoints-and-auth.md).

**If the backend is a native C/C++/GGML engine created and maintained by the
LocalAI team** (a from-scratch port like `parakeet.cpp`, `ced.cpp`,
`vibevoice.cpp`, `rf-detr.cpp`, not a wrapper around a third-party runtime), it
ALSO belongs in the top-level **`README.md`** table under "native C/C++/GGML
engines ... developed and maintained by the LocalAI project itself". Add a row
linking the upstream engine repo with a one-line description. This is the
project's showcase of its own engines; a new in-house backend that is missing
from it is a documentation bug.

## 5. Verification Checklist

After adding a new backend, verify:

- [ ] Backend directory structure is complete with all necessary files
- [ ] Build configurations added to `.github/backend-matrix.yml` for all desired platforms (per-arch entries with `platform-tag` for multi-arch; `builder-base-image` for llama-cpp / ik-llama-cpp / turboquant)
- [ ] **OS coverage considered**: added to `includeDarwin:` (macOS/Apple Silicon) if the backend can build there — with the `backend/index.yaml` `metal:` capability + `metal-<backend>` image entries, a `run.sh` Darwin/DYLD branch and `inferBackendPathDarwin` case for C++ backends — or the PR explains why an OS is unsupported. Do not ship Linux-only by default.
- [ ] Meta definition added to `backend/index.yaml` in the `## metas` section
- [ ] Image entries added to `backend/index.yaml` for all build variants (latest + development)
- [ ] Tag suffixes match between workflow file and index.yaml
- [ ] Makefile updated with all 6 required changes (`.NOTPARALLEL`, `prepare-test-extra`, `test-extra`, backend definition, docker-build target eval, `docker-build-backends`)
- [ ] No YAML syntax errors (check with linter)
- [ ] No Makefile syntax errors (check with linter)
- [ ] Follows the same pattern as similar backends (e.g., if it's a transcription backend, follow `faster-whisper` pattern)
- [ ] **`Load` validates its input and refuses models it can't serve.** When a model config has no explicit `backend:`, the model loader greedily probes *every* installed backend with the model's name and binds to the first `Load` that succeeds — an accept-anything `Load` will capture arbitrary LLMs (issue #9287). Backends that load a real artefact get this for free (the load fails); backends with no artefact must gate on the name: `opus` accepts only its own name (or none), `local-store` requires the `store.NamespacePrefix` namespace marker sent by `core/backend/stores.go`.
- [ ] **Gallery variant ranking considered**: if this backend should be preferred over another on some hardware, it is listed in `engineNamePreferenceRules` (NOT `backendBuildTagPreferenceRules`) in `pkg/system/capabilities.go`. A missing entry silently ranks it last and lets size decide.
- [ ] Documented: added to the category list in `docs/content/features/backends.md` (and any new endpoint/realtime capability documented under `docs/content/`)
- [ ] If it is an in-house native C/C++/GGML engine, added to the maintained-engines table in the top-level `README.md`

## Bundling runtime shared libraries (`package.sh`)

The final `Dockerfile.python` stage is `FROM scratch` — there is no system `libc`, no `apt`, no fallback library path. Only files explicitly copied from the builder stage end up in the backend image. That means any runtime `dlopen` your backend (or its Python deps) needs **must** be packaged into `${BACKEND}/lib/`.

Pattern:

1. Make sure the library is installed in the builder stage of `backend/Dockerfile.python` (add it to the top-level `apt-get install`).
2. Drop a `package.sh` in your backend directory that copies the library — and its soname symlinks — into `$(dirname $0)/lib`. See `backend/python/vllm/package.sh` for a reference implementation that walks `/usr/lib/x86_64-linux-gnu`, `/usr/lib/aarch64-linux-gnu`, etc.
3. `Dockerfile.python` already runs `package.sh` automatically if it exists, after `package-gpu-libs.sh`.
4. `libbackend.sh` automatically prepends `${EDIR}/lib` to `LD_LIBRARY_PATH` at run time, so anything packaged this way is found by `dlopen`.

How to find missing libs: when a Python module silently fails to register torch ops or you see `AttributeError: '_OpNamespace' '...' object has no attribute '...'`, run the backend image's Python with `LD_DEBUG=libs` to see which `dlopen` failed. The filename in the error message (e.g. `libnuma.so.1`) is what you need to package.

To verify packaging works without trusting the host:

```bash
make docker-build-<backend>
CID=$(docker create --entrypoint=/run.sh local-ai-backend:<backend>)
docker cp $CID:/lib /tmp/check && docker rm $CID
ls /tmp/check    # expect the bundled .so files + symlinks
```

Then boot it inside a fresh `ubuntu:24.04` (which intentionally does *not* have the lib installed) to confirm it actually loads from the backend dir.

## Importer integration

When you add a new backend, you MUST also make it importable via the model import form (`/import-model`). The import form dropdown is sourced dynamically from `GET /backends/known` — it reads the importer registry at `core/gallery/importers/importers.go`, so the steps below are the ONLY way to make your backend show up.

Required steps:

1. **If your backend has unambiguous detection signals** (unique file extension, HF `pipeline_tag`, unique repo name pattern, unique artefact like `modules.json`):
   - Create an importer file at `core/gallery/importers/<backend>.go` following the Match/Import pattern in `llama-cpp.go`.
   - Register it in `importers.go:defaultImporters` in **specificity order** — more specific detectors must appear BEFORE more generic ones (e.g. `sentencetransformers` before `transformers`, `stablediffusion-ggml` before `llama-cpp`, `vllm-omni` before `vllm`). First match wins.
2. **If your backend is a drop-in replacement** (same artefacts as another backend, e.g. `ik-llama-cpp` and `turboquant` both consume GGUF the same way `llama-cpp` does):
   - Do NOT create a new importer. Extend the existing importer's `Import()` to swap the emitted `backend:` field when `preferences.backend` matches. See `llama-cpp.go` for the pattern.
3. **If your backend has no reliable auto-detect signal** (preference-only — e.g. `sglang`, `tinygrad`, `whisperx`):
   - Do NOT create an importer. Instead add the backend name to the curated pref-only slice in `core/http/endpoints/localai/backend.go` that feeds `/backends/known`. A single line addition.
4. **Always** add a table-driven test in `core/gallery/importers/importers_test.go` (Ginkgo/Gomega):
   - Use a real public HuggingFace repo URI as the test fixture (existing tests already hit the live HF API — follow that pattern).
   - Cover detection (auto-match without preferences), preference-override (explicit `backend:` in preferences wins), and — if the backend's modality has a common `pipeline_tag` but ambiguous artefacts — an ambiguity test asserting `errors.Is(err, importers.ErrAmbiguousImport)`.

Rules of thumb:

- When in doubt, lean pref-only. A wrong auto-detect is worse than a forced preference.
- Never silently emit a modality mismatch (e.g. emit `llama-cpp` for a TTS repo because `.gguf` is present). Return `ErrAmbiguousImport` instead.
- Registration order is the single most common source of bugs. Check by running `go test ./core/gallery/importers/...` — the existing suite will fail if you've shadowed a pre-existing detector.

## 6. Example: Adding a Python Backend

For reference, when `moonshine` was added:
- **Files created**: `backend/python/moonshine/{backend.py, Makefile, install.sh, protogen.sh, requirements.txt, run.sh, test.py, test.sh}`
- **Workflow entries**: 3 build configurations (CPU, CUDA 12, CUDA 13)
- **Index entries**: 1 meta definition + 6 image entries (cpu, cuda12, cuda13 x latest/development)
- **Makefile updates**:
  - Added to `.NOTPARALLEL` line
  - Added to `prepare-test-extra` and `test-extra` targets
  - Added `BACKEND_MOONSHINE = moonshine|python|./backend|false|true`
  - Added eval for docker-build target generation
  - Added `docker-build-moonshine` to `docker-build-backends`
