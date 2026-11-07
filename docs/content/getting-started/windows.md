---
title: Windows
weight: 8
description: >
  Install and run LocalAI on Windows without WSL or Docker. Native
  windows/amd64 backend support.
---

## Native Windows backends (no WSL / Docker required)

LocalAI can run entirely natively on Windows using pre-built backend images
packaged as OCI tarballs. The llama-cpp backend is the first backend with
native Windows support; other backends will follow.

## Prerequisites

- Windows 10/11, **amd64**
- PowerShell 7+ (recommended) or cmd.exe
- No WSL, no Docker Desktop, no MSYS2 needed at runtime

The `local-ai` launcher downloads the Windows backend image on first run and
executes it as a native process.

## Install

```powershell
# Download the Windows release asset `local-ai-<version>-windows-amd64.exe`
# from the releases page: https://github.com/mudler/LocalAI/releases

# Or via winget (when available):
# winget install mudler.localai
```

## First run

```powershell
# Download a model (for example, Llama 3.2 1B Instruct GGUF)
.\local-ai.exe model install huggingface://bartowski/Llama-3.2-1B-Instruct-GGUF:Q4_K_M

# Start LocalAI
.\local-ai.exe run
```

LocalAI selects the best llama-cpp binary shipped in the Windows backend image
automatically. No configuration is required.

## Backend selection on Windows

Windows backend images ship three llama-cpp executables:

- `llama-cpp-cpu-all.exe` — CPU-only build with all ggml CPU backends (preferred
  when present).
- `llama-cpp-grpc.exe` — gRPC-RPC build, selected automatically when the
  `LLAMACPP_GRPC_SERVERS` environment variable is set.
- `llama-cpp-fallback.exe` — static fallback used when neither of the above
  applies.

A small `run.ps1` launcher (Windows has no shell to run the `run.sh` stub
through, so `pkg/model/process.go` starts `run.ps1` via `powershell.exe`)
picks the right binary at startup.

### GPU (Vulkan) support

The Windows backend image ships `ggml-vulkan.dll` alongside the CPU backends.
The single `llama-cpp-cpu-all.exe` build auto-detects a Vulkan device at
startup and offloads `gpu_layers` to it when a model requests them, falling
back to CPU inference on hosts with no Vulkan driver. No configuration is
required — the Vulkan loader (`vulkan-1.dll`) is bundled with the image and
loaded dynamically, so the backend never hard-depends on it.

## Environment variables

| Variable | Effect |
|----------|--------|
| `LLAMACPP_GRPC_SERVERS` | Forces use of `llama-cpp-grpc.exe` when set. |

## Troubleshooting

### Missing Visual C++ runtime

If the backend fails to start with a `DLL not found` error, install the
[Visual C++ Redistributable](https://learn.microsoft.com/en-us/cpp/windows/latest-supported-vc-redist).

### CUDA backend aborts on load (ucrtbase.dll, 0xc0000409)

The CUDA backend DLL ships binaries for the standard GPU architecture set
(Turing sm_75 and newer, per the llama.cpp defaults). If the CUDA backend
process aborts during model load, the installed image was likely built with
a narrower architecture set than the host GPU - rebuild with the default
set (`make backends/llama-cpp-windows-cuda` does not pin architectures) and
check the GPU's compute capability with
`nvidia-smi --query-gpu=name,compute_cap --format=csv`.

### Antivirus / SmartScreen

Windows Defender SmartScreen may warn about the unsigned `local-ai.exe` and
backend binaries. Click **More info** → **Run anyway** to proceed. Future
releases will be signed.

### App chats fail with "The model's context window is too small for the tools"

The chat UI runs the model as an agent: it always sends the session tool
schemas (about 25 tools, 6–8k tokens) with every turn, before any
conversation text. A model configured with a small `context_size` (8192 and
below with the default tool set) cannot fit them, and every chat fails with
a `context_too_small` message while plain `/v1/chat/completions` calls still
work. Raise the context window in the model YAML (most current models
support far more than 8k):

```yaml
context_size: 16384
```

or reduce the tool surface (disconnect MCP servers, disable built-in tool
groups in the chat's settings).

### CLI commands fail while the server runs ("port 55421 is already in use")

CLI commands (`model install`, `backends install`, ...) bind the add-on
owner loopback port, which the running server already holds. Stop the server
before running CLI commands, or pass `--addons-enabled=false` to the CLI
command.

### Model download URIs

The `huggingface://` URI needs the explicit file for direct installs:

```powershell
.\local-ai.exe model install huggingface://<user>/<repo>/<model-file>.gguf
```

Gallery names (for example `local-ai.exe models install <gallery-model>`)
resolve through the model gallery index instead.

### Chats: "the built-in bash cannot run commands on this machine"

The chats' built-in bash needs a POSIX process group and shell, which
Windows does not provide, so the command tool is unavailable (the reasoning
and chat itself keep working). Commands in chats run when the add-on that
isolates them is installed.

### Add-on runtime warnings ("addon runtime unavailable")

The add-on supervisor does not run on Windows yet; while a chat page is open
the log may repeat `could not list the connector kinds` / `addon owner
unavailable` warnings every few seconds. Chats, models and the API work;
connectors that need an add-on runtime are the missing piece.

### Firewall

LocalAI binds `:8080` on **all interfaces** by default, with no API key
configured — on a shared network that exposes the API to anyone who can
reach the host. For local use, bind explicitly to the loopback interface
and set a key instead:

```powershell
./local-ai.exe --address 127.0.0.1:8080 --api-key your-key-here
```

When the firewall prompts on first run, allow local-ai.exe only if you
intend to listen on non-loopback interfaces (for example, to serve models
to other machines on your LAN); for a loopback-only setup, deny the
prompt.

## Building from source

The commands mirror the Linux/macOS flow: `make build` produces the
`local-ai.exe` app, and the backend is built and installed by its own target
(`make backends/llama-cpp-windows` builds the backend image and installs it
into the local backend store, like `backends/%` does on Linux).

```powershell
cd C:\source_path\LocalAI
make build                        # the local-ai.exe app
make backends/llama-cpp-windows   # the llama.cpp backend image + install
```

### Backend image targets

See `scripts/build/llama-cpp-windows.sh` (section "Variant selection") and the
Makefile targets below if you want to rebuild the Windows backend image
locally. The targets depend on `build` (the app) the same way the Linux
`backends/%` targets do, and they are re-runnable: an interrupted build can
simply be run again (the script re-arms its source clones, and the long gRPC
build tree is reused between runs).
The build requires an MSYS2 UCRT64 environment with mingw-w64 GCC,
CMake 4.x, Ninja, the Vulkan headers/loader and glslc (see below), and Go
(to build the `local-ai.exe` that assembles the OCI image tar; it must be
reachable via `GOROOT`, `GO_TOOLCHAIN_ROOT`, or `PATH`).

| Target | Builds | Output |
|--------|--------|--------|
| `make backends/llama-cpp-windows` | default image: one llama.cpp build with Vulkan support (CPU everywhere, GPU offload when a Vulkan driver is present) | `backend-images/llama-cpp.tar` |
| `make backends/llama-cpp-windows-all` | everything the CI publishes: `vulkan` + `rpc` + `fallback` | `backend-images/llama-cpp.tar` |
| `make backends/llama-cpp-windows-cpu` | CPU-only build (no Vulkan packages needed) | `backend-images/llama-cpp-cpu.tar` |
| `make backends/llama-cpp-windows-vulkan` | the Vulkan-capable build alone | `backend-images/llama-cpp-vulkan.tar` |
| `make backends/llama-cpp-windows-cuda` | CUDA build — needs the NVIDIA CUDA toolkit and Visual Studio 2022+ (the C++ workload provides the MSVC host compiler nvcc requires); the script locates both automatically and explains what is missing. The llama.cpp tree stays MinGW-built; only `ggml-cuda.dll` comes from an MSVC configure and is loaded at runtime | `backend-images/llama-cpp-cuda.tar` |
| `make backends/llama-cpp-windows-rpc` | gRPC-RPC server + `ggml-rpc-server` | `backend-images/llama-cpp-rpc.tar` |
| `make backends/llama-cpp-windows-fallback` | static lowest-common-denominator build | `backend-images/llama-cpp-fallback.tar` |

Arbitrary combinations (one of `cpu`/`vulkan`/`cuda` plus any of `rpc`,
`fallback`) can be selected directly with
`LLAMA_CPP_WINDOWS_VARIANTS="vulkan rpc" make backends/llama-cpp-windows-vulkan`.


### Setting up MSYS2 UCRT64

The Makefile target installs MSYS2 and the toolchain automatically (after
confirmation) when they are missing, so the manual steps below are only needed
if you prefer to install by hand:

1. Download and install MSYS2 from https://www.msys2.org/
2. Open the **MSYS2 UCRT64** terminal (not plain MSYS2)
3. Install the build toolchain:

```bash
pacman -S --needed --noconfirm base-devel git cmake ninja mingw-w64-ucrt-x86_64-gcc mingw-w64-ucrt-x86_64-cmake mingw-w64-ucrt-x86_64-vulkan-headers mingw-w64-ucrt-x86_64-vulkan-loader mingw-w64-ucrt-x86_64-spirv-headers mingw-w64-x86_64-shaderc
```

The Vulkan packages are required: `GGML_VULKAN=ON` makes CMake's `FindVulkan`
treat `glslc` as a REQUIRED component, and ggml-vulkan runs
`find_package(SPIRV-Headers CONFIG REQUIRED)`. MSYS2 ships glslc only in the
mingw64 variant of `shaderc` (there is no ucrt64 build) — it is a standalone
tool, so the build script adds `/mingw64/bin` to `PATH` when no glslc is
already available (e.g. from a Vulkan SDK installed via the registry). The
script also installs `mingw-w64-ucrt-x86_64-spirv-headers` itself if its cmake
config is missing, so the docs' package list is only a head start.

4. Run the build from a plain Windows shell (PowerShell, `cmd`, or Git bash):

```powershell
cd C:\source_path\LocalAI
make backends/llama-cpp-windows
```

   On a native Windows host the Makefile hands the build to
   `scripts/build/llama-cpp-windows.ps1`, which runs the sh script under the
   real MSYS2 UCRT64 bash. If MSYS2 (or its mingw-w64 UCRT64 toolchain) is not
   installed, the dispatcher asks for confirmation before downloading and
   installing it via `winget`/`pacman`; declining stops the build. No need to
   open the UCRT64 terminal yourself.

   Inside the MSYS2 UCRT64 terminal the script runs directly:

```bash
cd /source_path/LocalAI
bash scripts/build/llama-cpp-windows.sh
```

The script packages the result as an OCI tarball (`backend-images/llama-cpp.tar`)
and — when run through the Makefile — installs it with
`./local-ai.exe backends install "ocifile://..."`.

### CI build

CI builds the Windows backend in the reusable
[`.github/workflows/backend_build_windows.yml`](https://github.com/mudler/LocalAI/blob/master/.github/workflows/backend_build_windows.yml)
workflow, dispatched from `.github/workflows/backend.yml` for the `includeWindows`
matrix entry. Only `llama-cpp` has a Windows build path today — a future entry
dispatched without one fails loudly rather than upload an empty tarball.

The pipeline:

1. **Build job** (`windows-latest`): sets up MSYS2 UCRT64 with the same
   package set as the local instructions above, caches ccache keyed on the
   pinned `LLAMA_VERSION`, then runs `make backends/llama-cpp-windows-all` in an
   MSYS2 shell (the Go toolchain from `actions/setup-go` is handed in via
   `GO_TOOLCHAIN_ROOT`, since the MSYS2 shell resets `PATH`). The produced
   `backend-images/llama-cpp.tar` is uploaded as a workflow artifact.
2. **Publish job** (`ubuntu-latest`, skipped on PRs): downloads the tarball,
   logs into DockerHub and quay.io with `crane`, tags it with the standard
   branch/semver/sha metadata (the `-windows-amd64-llama-cpp` tag suffix from
   the matrix),
   and pushes the OCI image to both registries.

The gallery entry points at the published image, so `local-ai backends install`
fetches the exact artifact CI produced.

The published image is signed keyless with Sigstore/cosign, the same way the
platform images that go through the `backend_merge.yml` manifest merge are:
the publish job resolves the pushed digest and signs it with an OCI 1.1
referrer bundle. The gallery does not enforce a `verification:` policy yet —
`backend/index.yaml` still ships without those blocks populated, and strict
mode stays off until a gallery covers every published variant — so installs
verify the signature, but a missing or invalid one degrades to a warning
rather than a refusal.
