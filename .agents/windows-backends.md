# Adding a Windows Backend Build Path

This guide covers adding a new backend (e.g. `vllm`, `sglang`, `tinygrad`) to the
native Windows build pipeline, following the pattern established by
`llama-cpp` (`scripts/build/llama-cpp-windows.sh` + `llama-cpp-windows.ps1`).

## Overview

Windows backends are **not** built with prebuilt Docker images like Linux/Darwin.
Instead:

1. The build runs on `windows-latest` under **MSYS2 UCRT64** (mingw-w64 toolchain).
2. gRPC is compiled from source into `backend/cpp/grpc/installed_packages`.
3. The backend is compiled with cmake/ninja using mingw-w64.
4. The result is packaged as an **OCI tar** (`--platform windows/amd64`).
5. LocalAI installs and runs it as a native process via `run.ps1`.
6. The published image is signed with keyless cosign.

The whole flow is driven by `make backends/<name>-windows`.

---

## Step-by-step checklist

### 1. Add the Makefile target

In `Makefile`, add your backend to `.NOTPARALLEL:` and add a target:

```make
.NOTPARALLEL: ... backends/<backend>-windows ...

backends/<backend>-windows:
ifeq ($(findstring NT,$(OS)),NT)
	powershell -NoProfile -ExecutionPolicy Bypass -File ./scripts/build/<backend>-windows.ps1
else
	bash ./scripts/build/<backend>-windows.sh
endif
	./$(BINARY_NAME) backends install "ocifile://$(shell cygpath -m $(abspath ./backend-images/<backend>.tar))"
```

### 2. Create `scripts/build/<backend>-windows.sh`

This bash script runs under MSYS2 UCRT64 and must:

- Re-exec under real MSYS2 bash if started from Git for Windows' bash
  (see `scripts/build/llama-cpp-windows.sh` lines 42-72).
- Build gRPC from source (pinned version) into `backend/cpp/grpc/installed_packages`
  — this is the layout the llama-cpp Makefile expects.
- Apply any mingw-w64 patches needed (c-ares, abseil, zlib, etc. — see the
  llama-cpp script for examples).
- Compile the backend variants with cmake/ninja.
- Bundle the mingw runtime DLLs.
- Package as an OCI tar via `local-ai util create-oci-image`.
- Honor a `JOBS` override for memory-limited hosts.

The script is driven by `make backends/<backend>-windows`, so `ROOT` should be
the repo root and `IMAGE_NAME` defaults to `localai/<backend>-windows`.

### 3. Create `scripts/build/<backend>-windows.ps1`

This PowerShell entry point is used when `make` runs natively on Windows
(OS=Windows_NT). It:

- Locates MSYS2 (env var `MSYS2_ROOT`, standard install dirs, or any PATH entry
  that is really an MSYS2 root).
- Installs MSYS2 + the toolchain via `winget` + `pacman` on first use —
  **only after asking for confirmation** unless `LOCALAI_AUTO_INSTALL_MSYS2=1`.
- Aborts with non-zero status if the user declines.
- Hands off to the `.sh` script under the real MSYS2 bash.

See `scripts/build/llama-cpp-windows.ps1` for the reference implementation.

### 4. Register in the CI matrix

Add an entry to `.github/backend-matrix.yml` under `includeWindows`:

```yaml
includeWindows:
  - backend: "llama-cpp"
    tag-suffix: "-windows-amd64-llama-cpp"
    lang: "go"
  - backend: "<your-backend>"
    tag-suffix: "-windows-amd64-<your-backend>"
    lang: "go"   # or "python" for Python backends
```

The `.github/workflows/backend_build_windows.yml` reusable workflow handles
MSYS2 setup, ccache, build, artifact upload, and cosign signing.

Also register in `scripts/lib/backend-filter.mjs`:
- Add to `WINDOWS_BESPOKE_BUILDERS` set if your backend needs the bespoke
  `scripts/build/<backend>-windows.sh` path (currently only `llama-cpp`).

### 5. Gallery entries

Add entries to `gallery/backends/index.yaml`:

- A `latest-*` release entry with `os: windows`, `arch: amd64`, pinned digest.
- A `*-development` mutable entry.
- Add `windows: windows-<backend>` to the meta-backend's `capabilities` map.

**Important:** Digests must be filled in by running `backendcatalog --repin`
after the images are published. Never edit digests by hand.

### 6. Capability preference rules

In `pkg/system/capabilities.go`:

- `backendBuildTagPreferenceRules`: add
  `{windows, []string{backendTokenWindows, backendTokenCPU}}` so Windows hosts
  prefer native builds over Linux CPU builds that cannot run.
- `engineNamePreferenceRules`: add
  `{windows, []string{engine<backend>}}` so the gallery picks the native build
  on Windows hosts.

### 7. Launcher (`run.ps1`)

Create `backend/cpp/<backend>/run.ps1` — a PowerShell launcher mirroring
`run.sh`. It:

- Picks the best binary variant (cpu-all, grpc, fallback).
- Sets `PATH` to include bundled DLLs.
- Forwards arguments and exit code.

`pkg/model/process_launcher_windows.go` already handles invoking
`powershell.exe -NoProfile -ExecutionPolicy Bypass -File run.ps1` on Windows.

### 8. Process tree cleanup

`pkg/model/process_tree_windows.go` already exists — it uses Windows Job
Objects (`JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`) so backend trees are reaped
when `local-ai.exe` exits. No per-backend work needed.

---

## Testing

- **Unit tests**: `pkg/model/process_launcher_windows_test.go` and
  `pkg/model/process_launcher_other_test.go` cover `resolveLauncher`.
- **E2E smoke**: `tests/e2e/windows` builds and boots `local-ai.exe` with the
  mock-backend. For real backend testing, add a Windows e2e job that builds
  the backend image, installs it, loads a model, and asserts a chat completion.

---

## Known gotchas

- **MSYS2 vs Git for Windows bash**: The build script must re-exec under real
  MSYS2 bash (`/c/msys64/usr/bin/bash.exe`), not Git for Windows' bash, which
  cannot resolve `/ucrt64/bin`.
- **cmake must be the native UCRT64 build**: MSYS2's `/usr/bin/cmake` reports
  `UNIX=TRUE`, causing gRPC to link against nonexistent `-ldl` / `-lrt`.
- **zlib conflicts**: System MSYS2 zlib conflicts with gRPC's bundled static
  zlib; back up/restore `/ucrt64/lib/libz.dll.a`.
- **PROCESSOR_ARCHITECTURE**: MSYS2 doesn't export it; set to `AMD64` so ggml
  doesn't treat the machine as `UNKNOWN`.
- **PATH ordering**: Append `/mingw64/bin` (for glslc), never prepend it —
  mingw libstdc++ would shadow the UCRT64 ABI runtime.

## CGO_ENABLED=0 for local-ai.exe

The Windows `local-ai.exe` server binary builds with **`CGO_ENABLED=0`** (see
`.github/workflows/tests-e2e.yml` and `Makefile`), while Windows backend builds
use full mingw gcc/CGO. This means CGO-dependent Go packages cannot be used in
the server binary itself. Contributors adding Go code to `cmd/local-ai` or
packages it imports should verify they compile with `CGO_ENABLED=0` on Windows.

