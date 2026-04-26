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

## 2. Add Build Configurations to `.github/workflows/backend.yml`

Add build matrix entries for each platform/GPU type you want to support. Look at similar backends for reference — `chatterbox`/`faster-whisper` for Python, `piper`/`silero-vad` for Go, `kokoros` for Rust.

**Without an entry here no image is ever built or pushed, and the gallery entry in `backend/index.yaml` will point at a tag that does not exist.** The `dockerfile:` field must point at `./backend/Dockerfile.<lang>` matching the language bucket from step 1 (e.g. `Dockerfile.python`, `Dockerfile.golang`, `Dockerfile.rust`). The `tag-suffix` must match the `uri:` in the corresponding `backend/index.yaml` image entry exactly.

If you add a new language bucket, `scripts/changed-backends.js` also needs a branch in `inferBackendPath` so PR change-detection routes file edits correctly.

**Placement in file:**
- CPU builds: Add after other CPU builds (e.g., after `cpu-chatterbox`)
- CUDA 12 builds: Add after other CUDA 12 builds (e.g., after `gpu-nvidia-cuda-12-chatterbox`)
- CUDA 13 builds: Add after other CUDA 13 builds (e.g., after `gpu-nvidia-cuda-13-chatterbox`)

**Additional build types you may need:**
- ROCm/HIP: Use `build-type: 'hipblas'` with `base-image: "rocm/dev-ubuntu-24.04:7.2.1"`
- Intel/SYCL: Use `build-type: 'intel'` or `build-type: 'sycl_f16'`/`sycl_f32` with `base-image: "intel/oneapi-basekit:2025.3.2-0-devel-ubuntu24.04"`
- L4T (ARM): Use `build-type: 'l4t'` with `platforms: 'linux/arm64'` and `runs-on: 'ubuntu-24.04-arm'`

## 3. Add Backend Metadata to `backend/index.yaml`

**Step 3a: Add Meta Definition**

Add a YAML anchor definition in the `## metas` section (around line 2-300). Look for similar backends to use as a template such as `diffusers` or `chatterbox`

**Step 3b: Add Image Entries**

Add image entries at the end of the file, following the pattern of similar backends such as `diffusers` or `chatterbox`. Include both `latest` (production) and `master` (development) tags.

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

## 5. Verification Checklist

After adding a new backend, verify:

- [ ] Backend directory structure is complete with all necessary files
- [ ] Build configurations added to `.github/workflows/backend.yml` for all desired platforms
- [ ] Meta definition added to `backend/index.yaml` in the `## metas` section
- [ ] Image entries added to `backend/index.yaml` for all build variants (latest + development)
- [ ] Tag suffixes match between workflow file and index.yaml
- [ ] Makefile updated with all 6 required changes (`.NOTPARALLEL`, `prepare-test-extra`, `test-extra`, backend definition, docker-build target eval, `docker-build-backends`)
- [ ] No YAML syntax errors (check with linter)
- [ ] No Makefile syntax errors (check with linter)
- [ ] Follows the same pattern as similar backends (e.g., if it's a transcription backend, follow `faster-whisper` pattern)

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
