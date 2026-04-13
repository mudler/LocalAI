# Adding a New Backend

When adding a new backend to LocalAI, you need to update several files to ensure the backend is properly built, tested, and registered. Here's a step-by-step guide based on the pattern used for adding backends like `moonshine`:

## 1. Create Backend Directory Structure

Create the backend directory under the appropriate location:
- **Python backends**: `backend/python/<backend-name>/`
- **Go backends**: `backend/go/<backend-name>/`
- **C++ backends**: `backend/cpp/<backend-name>/`

For Python backends, you'll typically need:
- `backend.py` - Main gRPC server implementation
- `Makefile` - Build configuration
- `install.sh` - Installation script for dependencies
- `protogen.sh` - Protocol buffer generation script
- `requirements.txt` - Python dependencies
- `run.sh` - Runtime script
- `test.py` / `test.sh` - Test files

## 2. Add Build Configurations to `.github/workflows/backend.yml`

Add build matrix entries for each platform/GPU type you want to support. Look at similar backends (e.g., `chatterbox`, `faster-whisper`) for reference.

**Placement in file:**
- CPU builds: Add after other CPU builds (e.g., after `cpu-chatterbox`)
- CUDA 12 builds: Add after other CUDA 12 builds (e.g., after `gpu-nvidia-cuda-12-chatterbox`)
- CUDA 13 builds: Add after other CUDA 13 builds (e.g., after `gpu-nvidia-cuda-13-chatterbox`)

**Additional build types you may need:**
- ROCm/HIP: Use `build-type: 'hipblas'` with `base-image: "rocm/dev-ubuntu-24.04:7.2.1"`
- Intel/SYCL: Use `build-type: 'intel'` or `build-type: 'sycl_f16'`/`sycl_f32` with `base-image: "intel/oneapi-basekit:2025.3.0-0-devel-ubuntu24.04"`
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

Add the backend to the `prepare-test-extra` target (around line 312) to prepare it for testing:

```makefile
prepare-test-extra: protogen-python
	...
	$(MAKE) -C backend/python/<backend-name>
```

**Step 4c: Add to `test-extra`**

Add the backend to the `test-extra` target (around line 319) to run its tests:

```makefile
test-extra: prepare-test-extra
	...
	$(MAKE) -C backend/python/<backend-name> test
```

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
