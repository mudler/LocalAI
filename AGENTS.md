# Build and testing

Building and testing the project depends on the components involved and the platform where development is taking place. Due to the amount of context required it's usually best not to try building or testing the project unless the user requests it. If you must build the project then inspect the Makefile in the project root and the Makefiles of any backends that are effected by changes you are making. In addition the workflows in .github/workflows can be used as a reference when it is unclear how to build or test a component. The primary Makefile contains targets for building inside or outside Docker, if the user has not previously specified a preference then ask which they would like to use.

## Building a specified backend

Let's say the user wants to build a particular backend for a given platform. For example let's say they want to build coqui for ROCM/hipblas

- The Makefile has targets like `docker-build-coqui` created with `generate-docker-build-target` at the time of writing. Recently added backends may require a new target.
- At a minimum we need to set the BUILD_TYPE, BASE_IMAGE build-args
  - Use .github/workflows/backend.yml as a reference it lists the needed args in the `include` job strategy matrix
  - l4t and cublas also requires the CUDA major and minor version
- You can pretty print a command like `DOCKER_MAKEFLAGS=-j$(nproc --ignore=1) BUILD_TYPE=hipblas BASE_IMAGE=rocm/dev-ubuntu-24.04:6.4.4 make docker-build-coqui`
- Unless the user specifies that they want you to run the command, then just print it because not all agent frontends handle long running jobs well and the output may overflow your context
- The user may say they want to build AMD or ROCM instead of hipblas, or Intel instead of SYCL or NVIDIA insted of l4t or cublas. Ask for confirmation if there is ambiguity.
- Sometimes the user may need extra parameters to be added to `docker build` (e.g. `--platform` for cross-platform builds or `--progress` to view the full logs), in which case you can generate the `docker build` command directly.

## Adding a New Backend

When adding a new backend to LocalAI, you need to update several files to ensure the backend is properly built, tested, and registered. Here's a step-by-step guide based on the pattern used for adding backends like `moonshine`:

### 1. Create Backend Directory Structure

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

### 2. Add Build Configurations to `.github/workflows/backend.yml`

Add build matrix entries for each platform/GPU type you want to support. Look at similar backends (e.g., `chatterbox`, `faster-whisper`) for reference.

**Placement in file:**
- CPU builds: Add after other CPU builds (e.g., after `cpu-chatterbox`)
- CUDA 12 builds: Add after other CUDA 12 builds (e.g., after `gpu-nvidia-cuda-12-chatterbox`)
- CUDA 13 builds: Add after other CUDA 13 builds (e.g., after `gpu-nvidia-cuda-13-chatterbox`)

**Additional build types you may need:**
- ROCm/HIP: Use `build-type: 'hipblas'` with `base-image: "rocm/dev-ubuntu-24.04:6.4.4"`
- Intel/SYCL: Use `build-type: 'intel'` or `build-type: 'sycl_f16'`/`sycl_f32` with `base-image: "intel/oneapi-basekit:2025.3.0-0-devel-ubuntu24.04"`
- L4T (ARM): Use `build-type: 'l4t'` with `platforms: 'linux/arm64'` and `runs-on: 'ubuntu-24.04-arm'`

### 3. Add Backend Metadata to `backend/index.yaml`

**Step 3a: Add Meta Definition**

Add a YAML anchor definition in the `## metas` section (around line 2-300). Look for similar backends to use as a template such as `diffusers` or `chatterbox`

**Step 3b: Add Image Entries**

Add image entries at the end of the file, following the pattern of similar backends such as `diffusers` or `chatterbox`. Include both `latest` (production) and `master` (development) tags.

### 4. Update the Makefile

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

### 5. Verification Checklist

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

### 6. Example: Adding a Python Backend

For reference, when `moonshine` was added:
- **Files created**: `backend/python/moonshine/{backend.py, Makefile, install.sh, protogen.sh, requirements.txt, run.sh, test.py, test.sh}`
- **Workflow entries**: 3 build configurations (CPU, CUDA 12, CUDA 13)
- **Index entries**: 1 meta definition + 6 image entries (cpu, cuda12, cuda13 × latest/development)
- **Makefile updates**: 
  - Added to `.NOTPARALLEL` line
  - Added to `prepare-test-extra` and `test-extra` targets
  - Added `BACKEND_MOONSHINE = moonshine|python|./backend|false|true`
  - Added eval for docker-build target generation
  - Added `docker-build-moonshine` to `docker-build-backends`

# Coding style

- The project has the following .editorconfig

```
root = true

[*]
indent_style = space
indent_size = 2
end_of_line = lf
charset = utf-8
trim_trailing_whitespace = true
insert_final_newline = true

[*.go]
indent_style = tab

[Makefile]
indent_style = tab

[*.proto]
indent_size = 2

[*.py]
indent_size = 4

[*.js]
indent_size = 2

[*.yaml]
indent_size = 2

[*.md]
trim_trailing_whitespace = false
```

- Use comments sparingly to explain why code does something, not what it does. Comments are there to add context that would be difficult to deduce from reading the code.
- Prefer modern Go e.g. use `any` not `interface{}`

# Logging

Use `github.com/mudler/xlog` for logging which has the same API as slog.

# llama.cpp Backend

The llama.cpp backend (`backend/cpp/llama-cpp/grpc-server.cpp`) is a gRPC adaptation of the upstream HTTP server (`llama.cpp/tools/server/server.cpp`). It uses the same underlying server infrastructure from `llama.cpp/tools/server/server-context.cpp`.

## Building and Testing

- Test llama.cpp backend compilation: `make backends/llama-cpp`
- The backend is built as part of the main build process
- Check `backend/cpp/llama-cpp/Makefile` for build configuration

## Architecture

- **grpc-server.cpp**: gRPC server implementation, adapts HTTP server patterns to gRPC
- Uses shared server infrastructure: `server-context.cpp`, `server-task.cpp`, `server-queue.cpp`, `server-common.cpp`
- The gRPC server mirrors the HTTP server's functionality but uses gRPC instead of HTTP

## Common Issues When Updating llama.cpp

When fixing compilation errors after upstream changes:
1. Check how `server.cpp` (HTTP server) handles the same change
2. Look for new public APIs or getter methods
3. Store copies of needed data instead of accessing private members
4. Update function calls to match new signatures
5. Test with `make backends/llama-cpp`

## Key Differences from HTTP Server

- gRPC uses `BackendServiceImpl` class with gRPC service methods
- HTTP server uses `server_routes` with HTTP handlers
- Both use the same `server_context` and task queue infrastructure
- gRPC methods: `LoadModel`, `Predict`, `PredictStream`, `Embedding`, `Rerank`, `TokenizeString`, `GetMetrics`, `Health`

## Tool Call Parsing Maintenance

When working on JSON/XML tool call parsing functionality, always check llama.cpp for reference implementation and updates:

### Checking for XML Parsing Changes

1. **Review XML Format Definitions**: Check `llama.cpp/common/chat-parser-xml-toolcall.h` for `xml_tool_call_format` struct changes
2. **Review Parsing Logic**: Check `llama.cpp/common/chat-parser-xml-toolcall.cpp` for parsing algorithm updates
3. **Review Format Presets**: Check `llama.cpp/common/chat-parser.cpp` for new XML format presets (search for `xml_tool_call_format form`)
4. **Review Model Lists**: Check `llama.cpp/common/chat.h` for `COMMON_CHAT_FORMAT_*` enum values that use XML parsing:
   - `COMMON_CHAT_FORMAT_GLM_4_5`
   - `COMMON_CHAT_FORMAT_MINIMAX_M2`
   - `COMMON_CHAT_FORMAT_KIMI_K2`
   - `COMMON_CHAT_FORMAT_QWEN3_CODER_XML`
   - `COMMON_CHAT_FORMAT_APRIEL_1_5`
   - `COMMON_CHAT_FORMAT_XIAOMI_MIMO`
   - Any new formats added

### Model Configuration Options

Always check `llama.cpp` for new model configuration options that should be supported in LocalAI:

1. **Check Server Context**: Review `llama.cpp/tools/server/server-context.cpp` for new parameters
2. **Check Chat Params**: Review `llama.cpp/common/chat.h` for `common_chat_params` struct changes
3. **Check Server Options**: Review `llama.cpp/tools/server/server.cpp` for command-line argument changes
4. **Examples of options to check**:
   - `ctx_shift` - Context shifting support
   - `parallel_tool_calls` - Parallel tool calling
   - `reasoning_format` - Reasoning format options
   - Any new flags or parameters

### Implementation Guidelines

1. **Feature Parity**: Always aim for feature parity with llama.cpp's implementation
2. **Test Coverage**: Add tests for new features matching llama.cpp's behavior
3. **Documentation**: Update relevant documentation when adding new formats or options
4. **Backward Compatibility**: Ensure changes don't break existing functionality

### Files to Monitor

- `llama.cpp/common/chat-parser-xml-toolcall.h` - Format definitions
- `llama.cpp/common/chat-parser-xml-toolcall.cpp` - Parsing logic
- `llama.cpp/common/chat-parser.cpp` - Format presets and model-specific handlers
- `llama.cpp/common/chat.h` - Format enums and parameter structures
- `llama.cpp/tools/server/server-context.cpp` - Server configuration options
