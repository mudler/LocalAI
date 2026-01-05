# Build and testing

Building and testing the project depends on the components involved and the platform where development is taking place. Due to the amount of context required it's usually best not to try building or testing the project unless the user requests it. If you must build the project then inspect the Makefile in the project root and the Makefiles of any backends that are effected by changes you are making. In addition the workflows in .github/workflows can be used as a reference when it is unclear how to build or test a component. The primary Makefile contains targets for building inside or outside Docker, if the user has not previously specified a preference then ask which they would like to use.

## Building a specified backend

Let's say the user wants to build a particular backend for a given platform. For example let's say they want to build bark for ROCM/hipblas

- The Makefile has targets like `docker-build-bark` created with `generate-docker-build-target` at the time of writing. Recently added backends may require a new target.
- At a minimum we need to set the BUILD_TYPE, BASE_IMAGE build-args
  - Use .github/workflows/backend.yml as a reference it lists the needed args in the `include` job strategy matrix
  - l4t and cublas also requires the CUDA major and minor version
- You can pretty print a command like `DOCKER_MAKEFLAGS=-j$(nproc --ignore=1) BUILD_TYPE=hipblas BASE_IMAGE=rocm/dev-ubuntu-24.04:6.4.4 make docker-build-bark`
- Unless the user specifies that they want you to run the command, then just print it because not all agent frontends handle long running jobs well and the output may overflow your context
- The user may say they want to build AMD or ROCM instead of hipblas, or Intel instead of SYCL or NVIDIA insted of l4t or cublas. Ask for confirmation if there is ambiguity.
- Sometimes the user may need extra parameters to be added to `docker build` (e.g. `--platform` for cross-platform builds or `--progress` to view the full logs), in which case you can generate the `docker build` command directly.

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
