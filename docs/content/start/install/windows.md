---
title: "Install on Windows"
description: "Install and run LocalAI on Windows without WSL or Docker, with the native windows/amd64 llama.cpp backend."
weight: 85
aliases:
  - "/getting-started/windows/"
---

LocalAI runs natively on Windows. It uses pre-built backend images packaged as OCI tarballs, so you do not need WSL, Docker Desktop or MSYS2 to run it. The llama.cpp backend is the first backend with native Windows support. Other backends are not available on Windows yet.

To install and run LocalAI with a window and a tray icon instead of PowerShell, use the [desktop app]({{% relref "/start/install/desktop" %}}#install-on-windows). It downloads `local-ai.exe` for you, checks its checksum, starts it and updates it.

## Prerequisites

- Windows 10 or 11 on **amd64**. There is no Windows arm64 build.
- PowerShell. LocalAI starts the backend through `powershell.exe`, which ships with Windows.
- `ffmpeg`, if you use audio features. LocalAI does not bundle it. Install it with `winget install Gyan.FFmpeg` or `choco install ffmpeg`, or set `LOCALAI_FFMPEG_PATH` to the full path of `ffmpeg.exe`. If LocalAI cannot find it, the start log shows a warning.

The llama.cpp backend comes from the backend gallery as a native Windows image. LocalAI runs it as a normal process.

## Install

### Install script

The PowerShell script installs LocalAI for your user. It needs no administrator rights. It downloads the release file `local-ai-<version>-windows-amd64.exe`, checks its SHA-256 sum against the checksum file of the release, and puts it in `%LOCALAPPDATA%\LocalAI\bin`. It then adds that folder to your user `PATH`. It works in Windows PowerShell 5.1 and in PowerShell 7.

To read the script before you run it, download it first:

```powershell
irm https://localai.io/install.ps1 -OutFile install.ps1
Get-Content .\install.ps1
powershell -ExecutionPolicy Bypass -File .\install.ps1
```

The one-line form runs the script without a review step:

```powershell
irm https://localai.io/install.ps1 | iex
```

Run the script again to upgrade. It does nothing when the installed file already matches the release. A running `local-ai.exe` keeps the old version until you restart it.

The download-then-run form takes these parameters. The one-line form cannot take parameters, so set the environment variable in the same row before it.

| Parameter | Environment variable | Effect |
|---|---|---|
| `-Version v4.7.1` | `LOCALAI_VERSION` | Install a given release. The default is the latest release. |
| `-InstallDir C:\tools\localai` | `INSTALL_DIR` | Install into this folder. The default is `%LOCALAPPDATA%\LocalAI\bin`. |
| `-NoPath` | `LOCALAI_NO_PATH=1` | Do not change your `PATH`. |
| `-InstallBackend` | `LOCALAI_INSTALL_BACKEND=1` | Download the llama.cpp backend after the install, with `local-ai backends install llama-cpp`. Without this, LocalAI downloads it when a model first needs it. |
| `-Force` | `LOCALAI_FORCE=1` | Install again even when the same build is present. |
| `-Uninstall` | `LOCALAI_UNINSTALL=1` | Remove `local-ai.exe` and the `PATH` entry. Your models, backends and settings stay. |

Example with a pinned version and the backend, in one line:

```powershell
$env:LOCALAI_VERSION = 'v4.7.1'; $env:LOCALAI_INSTALL_BACKEND = '1'; irm https://localai.io/install.ps1 | iex
```

The script stops with a clear message on Windows arm64, because there is no arm64 build. It uses TLS 1.2 or later and stops when the checksum does not match.

### Manual install

Download `local-ai-<version>-windows-amd64.exe` from the [releases page](https://github.com/mudler/LocalAI/releases). The checksum file of the release is `LocalAI-<version>-checksums.txt`. Check the file with `Get-FileHash -Algorithm SHA256 .\local-ai-<version>-windows-amd64.exe`, put it in a folder of your choice and run it from PowerShell.

## First run

```powershell
# Download a model (for example, Llama 3.2 1B Instruct GGUF)
.\local-ai.exe model install huggingface://bartowski/Llama-3.2-1B-Instruct-GGUF:Q4_K_M

# Start LocalAI
.\local-ai.exe run
```

LocalAI picks the llama.cpp binary from the Windows backend image for you. You do not need to configure anything.

## What is not available on Windows yet

- The add-on runtime. Features that need an add-on do not work. The log can show "add-on runtime unavailable" warnings.
- The worker commands for backends without a Windows build: `local-ai worker mlx-distributed`, `p2p-mlx`, `vllm` and `ds4-distributed` stop at once with an error. See [Distributed mode and workers on Windows](#distributed-mode-and-workers-on-windows) for what works.
- Backends other than llama.cpp.
- The built-in bash tool of the chat. It needs a POSIX shell.

## Distributed mode and workers on Windows

A Windows host can be a node of a LocalAI cluster:

- `local-ai worker` (distributed mode with NATS) registers the host as a worker. The frontend can schedule llama.cpp models on it. A model that needs another backend fails to start on this node with the message "this backend has no Windows build".
- `local-ai worker llama-cpp-rpc` and `local-ai worker p2p-llama-cpp-rpc` run the llama.cpp RPC server. The RPC server is in the `rpc` variant of the Windows backend image, which the published image includes.
- The worker reports the GPU and its VRAM. For NVIDIA GPUs it uses `nvidia-smi`, which the NVIDIA driver installs. For other GPUs it reads the adapter list from DirectX (DXGI) and the memory in use from the "GPU Adapter Memory" performance counters (Windows 10 version 1709 or later). An integrated GPU reports the shared system memory it can use. If the counters are not available, the worker reports the free VRAM as equal to the total.
- The frontend and the workers can run on different operating systems. A path on a worker keeps the separator of the worker's operating system.

The default folders are the same as for `local-ai run`: `models`, `backends` and `data` in the current folder. Set `--models-path`, `--backends-path` and `LOCALAI_DATA_PATH` to use other folders. A folder under `%LOCALAPPDATA%` keeps the files private to your user.

### Stop a worker

Ctrl+C or Ctrl+Break stops the worker. The worker deregisters from the frontend and stops its backends. Each backend runs in a Windows job object, so it also stops when the worker process is killed. When a killed worker leaves a backend that was not in a job object, the next start of the worker stops it.

### Run a worker as a Windows service

LocalAI connects to the Windows service manager when it starts as a service. This applies to every command, for example `local-ai run` and `local-ai worker`. A stop or shutdown request then stops LocalAI like Ctrl+C. Create the service from an elevated PowerShell, with the full path to the executable and the worker flags:

```powershell
sc.exe create LocalAIWorker start= auto binPath= "C:\LocalAI\local-ai.exe worker --register-to http://frontend:8080 --nats-url nats://nats:4222 --models-path C:\LocalAI\models --backends-path C:\LocalAI\backends"
sc.exe start LocalAIWorker
```

Set the paths explicitly: a service starts in `C:\Windows\System32`, so the default folders relative to the current folder are not useful. The service runs as `LocalSystem` unless you set another account with `obj=`. Set environment variables such as `LOCALAI_REGISTRATION_TOKEN` and `LOCALAI_DATA_PATH` for the service in the registry key `HKLM\SYSTEM\CurrentControlSet\Services\LocalAIWorker`, value `Environment` (multi-string), or pass them as flags.

### Limits

- Only the llama.cpp backend has a Windows build, so a Windows worker serves llama.cpp models only.
- The per-process VRAM figures of the Nodes page are not available on Windows. The node totals are.
- Worker and distributed mode have had much less use on Windows than on Linux. Report problems on the issue tracker.

## Backend selection on Windows

The Windows backend image has these llama.cpp executables:

- `llama-cpp-cpu-all.exe`: the CPU build with all ggml CPU variants. The ggml registry loads the best variant for your CPU. LocalAI uses this build by default.
- `llama-cpp-fallback.exe`: a static build for any x86-64 CPU. LocalAI uses it only when `llama-cpp-cpu-all.exe` is not in the image.
- `llama-cpp-grpc.exe`: the RPC build. It is compiled without AVX, AVX2, AVX512, FMA, F16C and BMI2, so it is slower on the CPU. LocalAI uses it only when the `LLAMACPP_GRPC_SERVERS` environment variable is set.

Windows has no shell to run the `run.sh` stub. LocalAI starts `run.ps1` through `powershell.exe` instead. The script selects the binary with the rules above, adds the `lib` folder to `PATH` and forwards the arguments.

### GPU (Vulkan) support

The Windows backend image ships `ggml-vulkan.dll` alongside the CPU backends.
The single `llama-cpp-cpu-all.exe` build auto-detects a Vulkan device at
startup and offloads `gpu_layers` to it when a model requests them, falling
back to CPU inference on hosts with no Vulkan driver. No configuration is
required. The Vulkan loader (`vulkan-1.dll`) is bundled with the image and
loaded dynamically, so the backend never hard-depends on it.

## Environment variables

| Variable | Effect |
|----------|--------|
| `LLAMACPP_GRPC_SERVERS` | Comma-separated RPC server addresses. When set, `run.ps1` starts `llama-cpp-grpc.exe` and the backend connects to these servers. |
| `LOCALAI_FFMPEG_PATH` | Full path of the `ffmpeg.exe` program. When it is not set, LocalAI looks for `ffmpeg` on `PATH`. |

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

Windows Defender SmartScreen may warn about `local-ai.exe` and the backend
binaries. The server binary of a release is signed with Authenticode only when
the project has a signing certificate set up for that release, and the backend
binaries are not signed. A file that you download with the install script has
no web mark, so SmartScreen does not check it. A file that you download with a
browser does. Click **More info** and then **Run anyway** to proceed. Check the
SHA-256 sum against the checksum file of the release first. To see if a file is
signed, run `Get-AuthenticodeSignature .\local-ai.exe`.

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
configured. On a shared network, that exposes the API to anyone who can
reach the host. For local use, bind explicitly to the loopback interface
and set a key instead:

```powershell
./local-ai.exe --address 127.0.0.1:8080 --api-key your-key-here
```

When the firewall prompts on first run, allow local-ai.exe only if you
intend to listen on non-loopback interfaces (for example, to serve models
to other machines on your LAN); for a loopback-only setup, deny the
prompt.

## Private files on Windows

On Linux and macOS, LocalAI keeps secret files private with POSIX permission bits (mode `0600` for a file, `0700` for a folder). Windows has no permission bits. On Windows, LocalAI gives these files a protected access control list (ACL) that grants access to the current user only. This applies to:

- the sealing keys of the remotes and connectors stores,
- the HMAC secret for API key hashing,
- the TLS and MITM proxy private keys,
- the CLI chat configuration,
- every state file that LocalAI writes atomically with a private mode.

Before LocalAI reads a sealing key, it checks the ACL. The SYSTEM account and the Administrators group can stay in the list, because they can read every file on the machine. If another user or group has access, for example because the key was written by an older LocalAI version and has the ACL of its folder, LocalAI replaces the ACL and logs a warning. On Linux and macOS a key file with a loose mode is refused instead.

Known limits:

- Files that LocalAI does not mark as private keep the ACL of their folder. On a shared machine, keep the data folder in a place that other users cannot read. By default it is in your user profile.
- The add-on runtime is not enabled on Windows. Its gateway key and package checks do not use the Windows ACL yet.
- Windows has no executable bit. LocalAI treats a file as a program when its extension is `.exe`, `.com`, `.cmd`, `.bat` or `.ps1`.

## Other differences on Windows

- **Temporary folders.** Generated content, uploads and backend scratch files go to `%TEMP%\localai-<SID>`, where `<SID>` is the security identifier of your account. On Linux and macOS the folder name has the user id instead.
- **Symbolic links in backend images.** Windows lets a user make symbolic links only with administrator rights or in Developer Mode. Without that right, LocalAI copies the target of each link in a backend image in its place. The install takes more disk space but works the same.
- **Stopping the server.** Ctrl+C and Ctrl+Break stop LocalAI gracefully: it finishes the requests in flight and then exits. When you close the console window, or when you log off or shut down, Windows gives the process only about 5 seconds, so long requests can be cut.
- **Converted schedules.** Windows does not give LocalAI the name of its time zone. When old cron tasks are converted to chat schedules, they run in UTC and the report says so. To keep the local time, set the `TZ` environment variable to a zone name, for example `TZ=Europe/Rome`, before you start LocalAI.
- **Windows service.** LocalAI does not install itself as a Windows service. Run it from a console, from the Task Scheduler at logon, or with a service wrapper of your choice.

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
| `make backends/llama-cpp-windows-cuda` | CUDA build. Needs the NVIDIA CUDA toolkit and Visual Studio 2022+ (the C++ workload provides the MSVC host compiler nvcc requires); the script locates both automatically and explains what is missing. The llama.cpp tree stays MinGW-built; only `ggml-cuda.dll` comes from an MSVC configure and is loaded at runtime | `backend-images/llama-cpp-cuda.tar` |
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
mingw64 variant of `shaderc` (there is no ucrt64 build). It is a standalone
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
and, when run through the Makefile, installs it with
`./local-ai.exe backends install "ocifile://..."`.

### CI build

CI builds the Windows backend in the reusable
[`.github/workflows/backend_build_windows.yml`](https://github.com/mudler/LocalAI/blob/master/.github/workflows/backend_build_windows.yml)
workflow, dispatched from `.github/workflows/backend.yml` for the `includeWindows`
matrix entry. Only `llama-cpp` has a Windows build path today. A future entry
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
referrer bundle. The gallery does not enforce a `verification:` policy yet.
Strict mode stays off until a gallery covers every published variant, so
installs verify the signature, but a missing or invalid one degrades to a
warning rather than a refusal.
