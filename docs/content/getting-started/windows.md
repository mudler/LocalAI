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

### Antivirus / SmartScreen

Windows Defender SmartScreen may warn about the unsigned `local-ai.exe` and
backend binaries. Click **More info** → **Run anyway** to proceed. Future
releases will be signed.

### Firewall

LocalAI listens on `http://localhost:8080` by default. Allow it through the
firewall on first run when prompted.

## Building from source

See `scripts/build/llama-cpp-windows.sh` and the Makefile target
`backends/llama-cpp-windows` if you want to rebuild the Windows backend image
locally. The target is re-runnable: an interrupted build can simply be run
again (the script re-arms its source clones, and the long gRPC build tree is
reused between runs).
The build requires an MSYS2 UCRT64 environment with mingw-w64 GCC,
CMake 4.x, Ninja, the Vulkan headers/loader and glslc (see below), and Go
(to build the `local-ai.exe` that assembles the OCI image tar; it must be
reachable via `GOROOT`, `GO_TOOLCHAIN_ROOT`, or `PATH`).

### Setting up MSYS2 UCRT64

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

4. Run the build. From a plain Windows shell (PowerShell, `cmd`, or Git bash)
   the script detects that it is not under MSYS2 and re-executes itself under
   an MSYS2 UCRT64 login shell automatically — no need to open the UCRT64
   terminal yourself:

```powershell
cd C:\source_path\LocalAI
make backends/llama-cpp-windows
```

   Inside the MSYS2 UCRT64 terminal the script runs directly:

```bash
cd /source_path/LocalAI
bash scripts/build/llama-cpp-windows.sh
```

The script packages the result as an OCI tarball (`backend-images/llama-cpp.tar`)
and — when run through the Makefile — installs it with
`./local-ai.exe backends install "ocifile://..."`.

### GitHub Actions build

CI builds the Windows backend in the reusable
[`.github/workflows/backend_build_windows.yml`](https://github.com/mudler/LocalAI/blob/master/.github/workflows/backend_build_windows.yml)
workflow, dispatched from `.github/workflows/backend.yml` for the `includeWindows`
matrix entry. Only `llama-cpp` has a Windows build path today — a future entry
dispatched without one fails loudly rather than upload an empty tarball.

The pipeline:

1. **Build job** (`windows-latest`): sets up MSYS2 UCRT64 with the same
   package set as the local instructions above, caches ccache keyed on the
   pinned `LLAMA_VERSION`, then runs `make backends/llama-cpp-windows` in an
   MSYS2 shell (the Go toolchain from `actions/setup-go` is handed in via
   `GO_TOOLCHAIN_ROOT`, since the MSYS2 shell resets `PATH`). The produced
   `backend-images/llama-cpp.tar` is uploaded as a workflow artifact.
2. **Publish job** (`ubuntu-latest`, skipped on PRs): downloads the tarball,
   logs into DockerHub and quay.io with `crane`, tags it with the standard
   branch/semver/sha metadata (the `-windows-amd64-llama-cpp` tag suffix from
   the matrix),
   and pushes the OCI image to both registries.

The gallery entry (with its `verification:` block) points at the published
image, so `local-ai backends install` fetches the exact artifact CI produced.
