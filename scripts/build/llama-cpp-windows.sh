#!/bin/bash

# Builds the native windows/amd64 llama-cpp backend image on a windows-latest
# GitHub runner under MSYS2 (UCRT64). Driven from the top-level Makefile via:
#
#     make backends/llama-cpp-windows            # default single-variant image
#     make backends/llama-cpp-windows-all        # vulkan + rpc + fallback
#     make backends/llama-cpp-windows-{cpu,vulkan,cuda,rpc,fallback}
#
# The set of variants is controlled with LLAMA_CPP_WINDOWS_VARIANTS (see the
# "Variant selection" section below); the Makefile targets are thin wrappers
# around it.
#
# Unlike Linux (prebuilt base-grpc images) and Darwin (Homebrew gRPC), a
# Windows runner has neither, so gRPC is compiled from source into
# backend/cpp/grpc/installed_packages -- the exact layout the llama-cpp
# Makefile's BUILD_GRPC_FOR_BACKEND_LLAMA path expects, so the flags it would
# pass to cmake (absl_DIR / Protobuf_DIR / utf8_range_DIR / gRPC_DIR) resolve
# to real directories.
#
# This script drives cmake directly instead of the llama-cpp Makefile's
# llama-cpp-cpu-all / llama-cpp-grpc / llama-cpp-fallback targets: those copy
# binaries around as bare `grpc-server` paths, which MSYS2's cp cannot resolve
# to grpc-server.exe. The cmake flags below mirror exactly what those targets
# would pass (see backend/cpp/llama-cpp/Makefile).
#
# The build host also has no docker daemon, so the result is packaged as an OCI
# tar (--platform windows/amd64) that LocalAI's `backends install` extracts and
# runs as a native process -- see pkg/model/process.go (run.ps1) and
# backend/cpp/llama-cpp/run.ps1.

set -ex

# ---------------------------------------------------------------------------
# Auto-dispatch into MSYS2 (UCRT64).
#
# `make backends/llama-cpp-windows` runs this script through the recipe shell.
# GNU make for Windows falls back to cmd.exe unless it finds sh; the Makefile's
# OS-detection block (top of file) then points SHELL at Git for Windows' sh, so
# a local run normally starts under a MINGW64 bash. That bash cannot resolve
# /ucrt64/bin (the path maps to C:\Program Files\Git\ucrt64, which does not
# exist) and nothing below can build without it. Re-exec under the real MSYS2
# bash instead. A non-login invocation deliberately keeps the inherited working
# directory (the repo root) and the Windows PATH (Go, Git); `bash -l` would
# reset PATH and cd to $HOME. MSYSTEM is not a usable discriminator here (Git
# bash reports MINGW64 too) - the existence of /ucrt64/bin under MSYS2's own
# runtime is the discriminator.
if [ ! -d /ucrt64/bin ]; then
  if [ -x /c/msys64/usr/bin/bash.exe ]; then
    echo "==> not running under MSYS2; re-exec'ing under C:/msys64 ..." >&2
    SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
    REPO_ROOT=$(cd "$SCRIPT_DIR/../.." && pwd)
    # Run the script by name, not `exec bash "$0"`: a bare `bash` here would
    # resolve to Git for Windows' bash via the inherited PATH (re-dispatch loop);
    # the shebang's /bin/bash is an absolute MSYS2 path and lands on the real
    # MSYS2 bash, where the /ucrt64/bin check below takes the direct branch.
    #
    # Mirror msys2_shell.cmd -ucrt64 -use-full-path: a LOGIN bash with
    # MSYSTEM=UCRT64 + MSYS2_PATH_TYPE=inherit. This is the one configuration in
    # which MSYS2's own git works when launched from a foreign shell (a plain
    # non-login `bash.exe -c` inherits the Git-for-Windows mount table and its
    # https remote helper dies with "fatal: remote helper 'https' aborted
    # session"). `bash -l` resets PATH to MSYS2 defaults and drops `go` (it is
    # on the Windows PATH but GOROOT is unset), so re-add the bin dir that the
    # invoking shell resolved before the profile runs.
    GO_DIR=$(command -v go 2>/dev/null)
    [ -n "$GO_DIR" ] && GO_DIR=$(dirname "$GO_DIR")
    # The login profile rebuilds the environment from a whitelist and drops
    # Windows vars the MSYS2 runtime does not know (USERPROFILE etc.), so
    # carry the ones go/ccache need over from the invoking shell.
    exec env MSYSTEM=UCRT64 MSYS2_PATH_TYPE=inherit /c/msys64/usr/bin/bash.exe -l -c \
      "export PATH='$GO_DIR':\$PATH; export USERPROFILE='$USERPROFILE'; cd '$REPO_ROOT' && exec ./scripts/build/llama-cpp-windows.sh"
  else
    echo "ERROR: this script must run under MSYS2 UCRT64 (C:\\msys64)." >&2
    echo "       Install MSYS2 (winget install MSYS2.MSYS2) and retry 'make backends/llama-cpp-windows'." >&2
    exit 1
  fi
fi

ROOT=$(pwd)
IMAGE_NAME="${IMAGE_NAME:-localai/llama-cpp-windows}"
PLATFORMARCH="${PLATFORMARCH:-windows/amd64}"

# nproc exists on MSYS2; fall back like the llama-cpp Makefile does otherwise.
# Honor an explicit JOBS override (e.g. `JOBS=4 make backends/llama-cpp-windows`):
# on memory-limited Windows hosts -j$(nproc) can spike past available RAM and
# several parallel g++ processes get killed mid-compile with no error text
# (silent "Error 1" on unrelated .obj targets).
JOBS="${JOBS:-$(nproc 2>/dev/null || echo 1)}"
export JOBS

# ---------------------------------------------------------------------------
# Variant selection
#
# LLAMA_CPP_WINDOWS_VARIANTS picks what goes into the image (space-separated).
# Exactly one "main" llama.cpp build is allowed plus optional companions:
#
#   vulkan    main build with GGML_VULKAN=ON: one grpc-server plus the
#             dlopen-able libggml-cpu-*.dll set and libggml-vulkan.dll. Works
#             on every host; GPU offload happens when a Vulkan driver is
#             present. This is the default image.
#   cpu       the same build without Vulkan (no Vulkan packages needed).
#   cuda      main build with GGML_CUDA=ON; needs the NVIDIA CUDA toolkit
#             (nvcc) on PATH or pointed at by CUDA_PATH.
#   rpc       companion: gRPC-RPC server + ggml-rpc-server binary.
#   fallback  companion: static lowest-common-denominator grpc-server.
#
# "make backends/llama-cpp-windows" builds the default single-variant image;
# "make backends/llama-cpp-windows-all" builds vulkan + rpc + fallback (the
# full set the CI publishes); the per-variant targets build one variant each.
# ---------------------------------------------------------------------------
ALL_VARIANTS="vulkan rpc fallback"
VARIANTS="${LLAMA_CPP_WINDOWS_VARIANTS:-$ALL_VARIANTS}"

has_variant() {
  case " $VARIANTS " in
    *" $1 "*) return 0 ;;
    *) return 1 ;;
  esac
}

for _v in $VARIANTS; do
  case " cpu vulkan cuda rpc fallback " in
    *" $_v "*) ;;
    *)
      echo "ERROR: unknown llama-cpp windows variant '$_v'." >&2
      echo "       Allowed: cpu vulkan cuda rpc fallback (LLAMA_CPP_WINDOWS_VARIANTS=\"$VARIANTS\")." >&2
      exit 1
      ;;
  esac
done

# Count the main variants (cpu/vulkan/cuda are mutually exclusive: each
# configures the same llama.cpp source tree differently).
MAIN_VARIANT=""
_main_count=0
for _m in cpu vulkan cuda; do
  if has_variant "$_m"; then
    MAIN_VARIANT=$_m
    _main_count=$((_main_count + 1))
  fi
done
if [ "$_main_count" -gt 1 ]; then
  echo "ERROR: pick ONE of cpu / vulkan / cuda (got: $VARIANTS)." >&2
  echo "       rpc and fallback can be added as companions, e.g. LLAMA_CPP_WINDOWS_VARIANTS=\"vulkan rpc\"." >&2
  exit 1
fi
[ -n "$MAIN_VARIANT" ] || MAIN_VARIANT=vulkan

if [ "$MAIN_VARIANT" = "cuda" ]; then
  # Resolve the toolkit root: CUDA_PATH (set by the NVIDIA installer) or the
  # nvcc found above.
  _cuda_root="${CUDA_PATH:-}"
  if [ -z "$_cuda_root" ] && command -v nvcc >/dev/null 2>&1; then
    _cuda_root=$(dirname "$(dirname "$(cygpath -m "$(command -v nvcc)")")")
  fi
  if [ -n "$_cuda_root" ]; then
    export CUDA_PATH="$_cuda_root"
    # nvcc must resolve for the MSVC side-build's configure and for the CUDA
    # runtime DLL bundling below.
    export PATH="$(cygpath -u "$_cuda_root")/bin:$PATH"
  fi
  # nvcc on Windows drives the MSVC host compiler (cl.exe). cl is rarely on
  # PATH: locate it through vswhere (any Visual Studio / Build Tools edition
  # with the C++ workload) and hand it to both nvcc's PATH lookup and CMake.
  _cl_exe=""
  if command -v cl.exe >/dev/null 2>&1; then
    _cl_exe=$(command -v cl.exe)
  elif [ -x "/c/Program Files (x86)/Microsoft Visual Studio/Installer/vswhere.exe" ]; then
    _cl_exe=$("/c/Program Files (x86)/Microsoft Visual Studio/Installer/vswhere.exe" \
      -latest -products '*' -requires Microsoft.VisualStudio.Component.VC.Tools.x86.x64 \
      -find 'VC/Tools/MSVC/*/bin/Hostx64/x64/cl.exe' 2>/dev/null | head -1)
  fi
  if [ -z "$_cl_exe" ]; then
    echo "ERROR: the cuda variant needs the MSVC host compiler for nvcc ('cl.exe')." >&2
    echo "       Install Visual Studio 2022 (or newer) with the 'Desktop development" >&2
    echo "       with C++' workload and retry, or use the vulkan variant, which drives" >&2
    echo "       the same NVIDIA GPU through the Vulkan driver." >&2
    exit 1
  fi
  # cl.exe's directory goes first in PATH so nvcc's own lookup succeeds; the
  # -DCMAKE_CUDA_HOST_COMPILER hint is passed with the configure (cuda case
  # of the main-variant build below).
  export PATH="$(dirname "$(cygpath -u "$_cl_exe")"):$PATH"
fi

# Output tar: explicit OUTPUT_TAR wins; the canonical image (the default
# single-variant build and the full CI set) keeps the historical name; any
# other selection names itself after its variants.
OUTPUT_TAR="${OUTPUT_TAR:-./backend-images/llama-cpp.tar}"
if [ "$OUTPUT_TAR" = "./backend-images/llama-cpp.tar" ] && [ "$VARIANTS" != "vulkan" ] && [ "$VARIANTS" != "$ALL_VARIANTS" ]; then
  OUTPUT_TAR="./backend-images/llama-cpp-$(echo "$VARIANTS" | tr ' ' '-').tar"
fi
echo "==> Variants: $VARIANTS (main: $MAIN_VARIANT) -> $OUTPUT_TAR"


# Resolve cmake to the native UCRT64 build (/ucrt64/bin/cmake), not MSYS2's
# /usr/bin/cmake. The MSYS/Cygwin cmake reports UNIX=TRUE, so gRPC's
# `if(UNIX)` block in CMakeLists.txt appends ${CMAKE_DL_LIBS} m rt to every
# target's link line and every gRPC executable dies with "cannot find -ldl /
# -lrt" (no such libraries on Windows). The native cmake also sets MINGW
# (gRPC then defines -D_WIN32_WINNT=0x600 itself) and keeps plain exe names
# (the MSYS cmake emits protoc-24.3.0.exe). Everything else the script needs
# (make, git, perl, curl, unzip) lives in /usr/bin, so only cmake's resolution
# changes.
export PATH="/ucrt64/bin:$PATH"

# Handle zlib conflicts between system MSYS2 and gRPC's bundled static zlib
SYS_ZLIB_DLLA="/ucrt64/lib/libz.dll.a"
SYS_ZLIB_DLLA_BAK="/ucrt64/lib/libz.dll.a.bak"
backup_sys_zlib() {
  if [ -f "$SYS_ZLIB_DLLA" ] && [ ! -f "$SYS_ZLIB_DLLA_BAK" ]; then
    mv "$SYS_ZLIB_DLLA" "$SYS_ZLIB_DLLA_BAK"
    echo "==> Backed up system $SYS_ZLIB_DLLA"
  fi
}
restore_sys_zlib() {
  if [ -f "$SYS_ZLIB_DLLA_BAK" ] && [ ! -f "$SYS_ZLIB_DLLA" ]; then
    mv "$SYS_ZLIB_DLLA_BAK" "$SYS_ZLIB_DLLA"
    echo "==> Restored system $SYS_ZLIB_DLLA"
  fi
}

# The MSYS2 runtime does not export PROCESSOR_ARCHITECTURE, so cmake's Windows
# host detection yields an empty CMAKE_SYSTEM_PROCESSOR and ggml treats the
# machine as UNKNOWN (GGML_CPU_ALL_VARIANTS then refuses to configure). This
# backend is x86_64-only.
export PROCESSOR_ARCHITECTURE=AMD64

# go.exe resolves GOPATH (default %USERPROFILE%\go) and its build cache
# (%LocalAppData%\go-build) through Windows env vars the login profile drops;
# ccache needs a user-profile dir too. Fall back to the MSYS2 home for direct
# launches that forgot to set it.
export USERPROFILE="${USERPROFILE:-$(cygpath -w "$HOME")}"
export GOCACHE="${GOCACHE:-$(cygpath -w "$HOME")/go-build}"
mkdir -p "$GOCACHE"

# ggml-vulkan compiles its shaders at build time with glslc, and CMake's
# FindVulkan declares it a REQUIRED component. MSYS2 only ships glslc in the
# mingw64 variant of shaderc (there is no ucrt64 build); it is a standalone
# tool, so its prefix can sit next to the UCRT64 one. When a Vulkan SDK
# (VULKAN_SDK, found by FindVulkan through the registry on Windows) already
# provides glslc, leave PATH alone.
#
# The mingw64 dir must be APPENDED, never prepended: the UCRT64-built tools
# (protoc, gcc, ...) resolve their runtime DLLs through PATH, and a mingw64
# libstdc++/libgcc_s_seh placed ahead of /ucrt64/bin makes them load the wrong
# ABI runtime and die silently (protoc exits 127 mid-build).
if ! command -v glslc >/dev/null 2>&1 && [ -x /mingw64/bin/glslc.exe ]; then
  export PATH="$PATH:/mingw64/bin"
fi

# ggml-vulkan runs find_package(SPIRV-Headers CONFIG REQUIRED) at configure
# time; MSYS2 ships the config as mingw-w64-ucrt-x86_64-spirv-headers.
# Also ensure common image libraries (JPEG) needed by some components are installed.
if [ ! -f /ucrt64/share/cmake/SPIRV-Headers/SPIRV-HeadersConfig.cmake ] || \
   [ ! -f /ucrt64/lib/libjpeg.a ] && [ ! -f /ucrt64/lib/libjpeg.dll.a ]; then
  echo "==> Installing required MSYS2 UCRT64 dependencies (SPIRV-Headers, libjpeg-turbo, etc.)"
  pacman -S --noconfirm mingw-w64-ucrt-x86_64-spirv-headers mingw-w64-ucrt-x86_64-libjpeg-turbo mingw-w64-ucrt-x86_64-libpng mingw-w64-ucrt-x86_64-tiff 2>/dev/null || true
fi
# Ensure libjpeg is available even if condition logic was odd
if [ ! -f /ucrt64/lib/libjpeg.dll.a ] && [ ! -f /ucrt64/lib/libturbojpeg.dll.a ] && [ ! -f /ucrt64/lib/libjpeg.a ]; then
  pacman -S --noconfirm mingw-w64-ucrt-x86_64-libjpeg-turbo 2>/dev/null || true
fi

# actions/setup-go adds go to the runner PATH but only exports GOROOT for Go
# < 1.9; the msys2 shell resets PATH, so `go` is not reachable here. The
# workflow resolves the install dir (GO_TOOLCHAIN_ROOT) with the default shell
# and hands it over; GOROOT is the fallback for standalone runs. Re-add the
# bin dir (translated to an msys path) unless a go binary already resolves.
GO_BIN_DIR="${GO_TOOLCHAIN_ROOT:-$GOROOT}"
if ! command -v go >/dev/null 2>&1 && [ -n "$GO_BIN_DIR" ]; then
  GO_BIN_DIR="$GO_BIN_DIR/bin"
  case "$GO_BIN_DIR" in
    [A-Za-z]:*) GO_BIN_DIR=$(cygpath -u "$GO_BIN_DIR") ;;
  esac
  export PATH="$GO_BIN_DIR:$PATH"
fi

# ---------------------------------------------------------------------------
# gRPC from source (backend/cpp/grpc/installed_packages)
# ---------------------------------------------------------------------------
GRPC_DIR="$ROOT/backend/cpp/grpc"
# One pin for gRPC across platforms: the default is read from the Linux
# base-grpc-builder Dockerfile, so a bump there also moves this build. Set
# GRPC_VERSION in the environment to override it. The mingw-w64 patches below
# were checked against the bundled c-ares 1.19.1, zlib 1.3 and the
# boringssl-with-bazel and abseil sources of that release; re-check them when
# the pin moves.
GRPC_VERSION="${GRPC_VERSION:-$(sed -n 's/^ARG GRPC_VERSION=//p' "$ROOT/backend/Dockerfile.base-grpc-builder" | head -n 1)}"
if [ -z "$GRPC_VERSION" ]; then
  echo "ERROR: cannot read GRPC_VERSION from backend/Dockerfile.base-grpc-builder" >&2
  exit 1
fi
echo "==> gRPC ${GRPC_VERSION}"
mkdir -p "$GRPC_DIR/grpc_repo/grpc" "$GRPC_DIR/grpc_build"
cd "$GRPC_DIR/grpc_repo/grpc"
git init -q
# Re-runnable: a previous (possibly failed) run may already have wired origin.
git remote remove origin 2>/dev/null || true
git remote add origin https://github.com/grpc/grpc.git
git fetch origin "refs/tags/${GRPC_VERSION}:refs/tags/${GRPC_VERSION}" --depth 1
git checkout -q "${GRPC_VERSION}"
git submodule update --init --recursive --depth 1 --single-branch

# abseil's cctz win32 local-time-zone path (USE_WIN32_LOCAL_TIME_ZONE) uses
# WindowsCreateStringReference / WindowsDeleteString / WindowsGetStringRawBuffer
# from <winstring.h>, which the MSVC SDK pulls in transitively but mingw-w64's
# roapi.h/windows.globalization.h do not. Patch the one TU to include it
# explicitly; without this the whole gRPC build dies at time_zone_lookup.cc.
# (\R matches either LF or CRLF since git may have normalized line endings.)
perl -0pi -e 's/(#include <windows\.globalization\.h>\R#include <windows\.h>\R)(#endif)/$1#include <winstring.h>\n$2/' \
  third_party/abseil-cpp/absl/time/internal/cctz/src/time_zone_lookup.cc

# c-ares (bundled with gRPC) has a broken CMake build under mingw-w64
# UCRT64 + CMake 4.x. Its CMakeLists uses CHECK_INCLUDE_FILES to detect
# winsock2.h / ws2tcpip.h / windows.h on WIN32, but the probes run BEFORE
# CMAKE_REQUIRED_DEFINITIONS is set (so without WIN32_LEAN_AND_MEAN), causing
# windows.h to pull in winsock.h which conflicts with winsock2.h. When the
# checks fail, CMAKE_EXTRA_INCLUDE_FILES stays empty, so the downstream
# CARES_TYPE_EXISTS / CHECK_SYMBOL_EXISTS probes for SOCKET, struct
# sockaddr_in6, struct addrinfo, recv, send etc. all fail too. That leaves
# the generated ares_config.h without HAVE_RECV / HAVE_SEND /
# HAVE_STRUCT_SOCKADDR_IN6 / HAVE_STRUCT_ADDRINFO / RECV_TYPE_* / SEND_TYPE_*,
# which trips setup_once.h's compile-time Error guards and makes ares_ipv6.h
# redefine structs already in ws2tcpip.h.
#
# Pre-seeding the cache vars via -D didn't work: CMake 4.x's CHECK_INCLUDE_FILES
# still runs and overwrites them. Instead, patch the ares_config.h.cmake
# template to append Windows-specific forced defines at the end. This way,
# regardless of what CMake detection produces, the generated ares_config.h
# always has the correct Windows values.
cat >> third_party/cares/cares/src/lib/ares_config.h.cmake <<'CARES_WIN32_FIX'

/* ---- LocalAI mingw-w64 build fix ----
 * The CMake detection in this old c-ares (bundled with gRPC) doesn't
 * reliably detect Windows socket types and functions under mingw-w64's
 * UCRT64 headers + CMake 4.x. Force the known-correct values on _WIN32
 * so the build doesn't trip setup_once.h's Error guards or ares_ipv6.h's
 * struct redefinitions. Values mirror config-win32.h (the hand-crafted
 * config c-ares uses for non-CMake Windows builds). */
#ifdef _WIN32
#ifndef HAVE_WINDOWS_H
#define HAVE_WINDOWS_H 1
#endif
#ifndef HAVE_WINSOCK_H
#define HAVE_WINSOCK_H 1
#endif
#ifndef HAVE_WINSOCK2_H
#define HAVE_WINSOCK2_H 1
#endif
#ifndef HAVE_WS2TCPIP_H
#define HAVE_WS2TCPIP_H 1
#endif
#ifndef HAVE_ASSERT_H
#define HAVE_ASSERT_H 1
#endif
#ifndef HAVE_ERRNO_H
#define HAVE_ERRNO_H 1
#endif
#ifndef HAVE_GETOPT_H
#define HAVE_GETOPT_H 1
#endif
#ifndef HAVE_LIMITS_H
#define HAVE_LIMITS_H 1
#endif
#ifndef HAVE_PROCESS_H
#define HAVE_PROCESS_H 1
#endif
#ifndef HAVE_SIGNAL_H
#define HAVE_SIGNAL_H 1
#endif
#ifndef HAVE_TIME_H
#define HAVE_TIME_H 1
#endif
#ifndef HAVE_UNISTD_H
#define HAVE_UNISTD_H 1
#endif
#ifndef HAVE_STDBOOL_H
#define HAVE_STDBOOL_H 1
#endif
#ifndef HAVE_STDINT_H
#define HAVE_STDINT_H 1
#endif
#ifndef HAVE_STDLIB_H
#define HAVE_STDLIB_H 1
#endif
#ifndef HAVE_STRING_H
#define HAVE_STRING_H 1
#endif
#ifndef HAVE_SOCKLEN_T
#define HAVE_SOCKLEN_T 1
#endif
#ifndef HAVE_TYPE_SOCKET
#define HAVE_TYPE_SOCKET 1
#endif
#ifndef HAVE_BOOL_T
#define HAVE_BOOL_T 1
#endif
#ifndef HAVE_SSIZE_T
#define HAVE_SSIZE_T 1
#endif
#ifndef HAVE_LONGLONG
#define HAVE_LONGLONG 1
#endif
#ifndef HAVE_SIG_ATOMIC_T
#define HAVE_SIG_ATOMIC_T 1
#endif
#ifndef HAVE_STRUCT_ADDRINFO
#define HAVE_STRUCT_ADDRINFO 1
#endif
#ifndef HAVE_STRUCT_IN6_ADDR
#define HAVE_STRUCT_IN6_ADDR 1
#endif
#ifndef HAVE_STRUCT_SOCKADDR_IN6
#define HAVE_STRUCT_SOCKADDR_IN6 1
#endif
#ifndef HAVE_STRUCT_SOCKADDR_STORAGE
#define HAVE_STRUCT_SOCKADDR_STORAGE 1
#endif
#ifndef HAVE_STRUCT_TIMEVAL
#define HAVE_STRUCT_TIMEVAL 1
#endif
#ifndef HAVE_AF_INET6
#define HAVE_AF_INET6 1
#endif
#ifndef HAVE_PF_INET6
#define HAVE_PF_INET6 1
#endif
#ifndef HAVE_FIONBIO
#define HAVE_FIONBIO 1
#endif
#ifndef HAVE_CLOSESOCKET
#define HAVE_CLOSESOCKET 1
#endif
#ifndef HAVE_CONNECT
#define HAVE_CONNECT 1
#endif
#ifndef HAVE_FREEADDRINFO
#define HAVE_FREEADDRINFO 1
#endif
#ifndef HAVE_GETADDRINFO
#define HAVE_GETADDRINFO 1
#endif
#ifndef HAVE_GETHOSTBYADDR
#define HAVE_GETHOSTBYADDR 1
#endif
#ifndef HAVE_GETHOSTBYNAME
#define HAVE_GETHOSTBYNAME 1
#endif
#ifndef HAVE_GETHOSTNAME
#define HAVE_GETHOSTNAME 1
#endif
#ifndef HAVE_GETNAMEINFO
#define HAVE_GETNAMEINFO 1
#endif
#ifndef HAVE_GETTIMEOFDAY
#define HAVE_GETTIMEOFDAY 1
#endif
#ifndef HAVE_INET_NTOP
#define HAVE_INET_NTOP 1
#endif
#ifndef HAVE_INET_PTON
#define HAVE_INET_PTON 1
#endif
#ifndef HAVE_IOCTLSOCKET
#define HAVE_IOCTLSOCKET 1
#endif
/* NOTE: HAVE_IOCTL_FIONBIO is deliberately NOT defined. c-ares's
 * setsocknonblock() prefers HAVE_IOCTL_FIONBIO (POSIX ioctl(FIONBIO)) over
 * HAVE_IOCTLSOCKET_FIONBIO (Windows ioctlsocket(FIONBIO)), and the POSIX
 * branch calls ioctl() which does not exist on Windows. config-win32.h —
 * the hand-crafted config c-ares uses for non-CMake Windows builds — also
 * omits it, defining only HAVE_IOCTLSOCKET_FIONBIO. */
#ifndef HAVE_IOCTLSOCKET_FIONBIO
#define HAVE_IOCTLSOCKET_FIONBIO 1
#endif
#ifndef HAVE_RECV
#define HAVE_RECV 1
#endif
#ifndef HAVE_RECVFROM
#define HAVE_RECVFROM 1
#endif
#ifndef HAVE_SEND
#define HAVE_SEND 1
#endif
#ifndef HAVE_SETSOCKOPT
#define HAVE_SETSOCKOPT 1
#endif
#ifndef HAVE_SOCKET
#define HAVE_SOCKET 1
#endif
#ifndef HAVE_STRDUP
#define HAVE_STRDUP 1
#endif
#ifndef HAVE_STRICMP
#define HAVE_STRICMP 1
#endif
#ifndef HAVE_STRNICMP
#define HAVE_STRNICMP 1
#endif
#ifndef HAVE_GETENV
#define HAVE_GETENV 1
#endif
/* HAVE_IOCTL_FIONBIO is intentionally absent and HAVE_IOCTLSOCKET_FIONBIO
 * is already defined above; see the NOTE above the first
 * HAVE_IOCTLSOCKET_FIONBIO define. */
#ifndef HAVE_GETADDRINFO_THREADSAFE
#define HAVE_GETADDRINFO_THREADSAFE 1
#endif
#ifndef HAVE_SOCKADDR_IN6_SIN6_SCOPE_ID
#define HAVE_SOCKADDR_IN6_SIN6_SCOPE_ID 1
#endif
#ifndef RECV_TYPE_ARG1
#define RECV_TYPE_ARG1 SOCKET
#endif
#ifndef RECV_TYPE_ARG2
#define RECV_TYPE_ARG2 char *
#endif
#ifndef RECV_TYPE_ARG3
#define RECV_TYPE_ARG3 int
#endif
#ifndef RECV_TYPE_ARG4
#define RECV_TYPE_ARG4 int
#endif
#ifndef RECV_TYPE_RETV
#define RECV_TYPE_RETV int
#endif
#ifndef SEND_QUAL_ARG2
#define SEND_QUAL_ARG2
#endif
#ifndef SEND_TYPE_ARG1
#define SEND_TYPE_ARG1 SOCKET
#endif
#ifndef SEND_TYPE_ARG2
#define SEND_TYPE_ARG2 char *
#endif
#ifndef SEND_TYPE_ARG3
#define SEND_TYPE_ARG3 int
#endif
#ifndef SEND_TYPE_ARG4
#define SEND_TYPE_ARG4 int
#endif
#ifndef SEND_TYPE_RETV
#define SEND_TYPE_RETV int
#endif
#ifndef RECVFROM_TYPE_ARG1
#define RECVFROM_TYPE_ARG1 SOCKET
#endif
#ifndef RECVFROM_TYPE_ARG2
#define RECVFROM_TYPE_ARG2 char *
#endif
#ifndef RECVFROM_TYPE_ARG3
#define RECVFROM_TYPE_ARG3 int
#endif
#ifndef RECVFROM_TYPE_ARG4
#define RECVFROM_TYPE_ARG4 int
#endif
#ifndef RECVFROM_TYPE_ARG5
#define RECVFROM_TYPE_ARG5 "struct sockaddr *"
#endif
#ifndef RECVFROM_TYPE_ARG6
#define RECVFROM_TYPE_ARG6 "int *"
#endif
#ifndef RECVFROM_TYPE_RETV
#define RECVFROM_TYPE_RETV int
#endif
#ifndef RETSIGTYPE
#define RETSIGTYPE void
#endif
#ifndef CARES_TYPEOF_ARES_SOCKLEN_T
#define CARES_TYPEOF_ARES_SOCKLEN_T socklen_t
#endif
#ifndef CARES_TYPEOF_ARES_SSIZE_T
#define CARES_TYPEOF_ARES_SSIZE_T "long long"
#endif
#endif /* _WIN32 */
CARES_WIN32_FIX

# The c-ares CMake detection above fails every HAVE_* probe (SOCKLEN_T,
# TYPE_SOCKET, STRUCT_ADDRINFO, ...), so its CMakeLists never adds the Winsock
# library to the link line. Without it the static libcares.a references
# WSAStartup / select / ntohl / gethostname / ... which then fail to link with
# "undefined reference to __imp_*".
#
# The static lib target (c-ares_static) links CARES_DEPENDENT_LIBS
# (ws2_32/advapi32/iphlpapi) PUBLICly, so gRPC's own consumers are fine. But
# the diagnostic tools (ahost/adig/acountry) link ${PROJECT_NAME} = "c-ares"
# by name; with CARES_SHARED=OFF there is no "c-ares" target, so CMake treats
# it as a plain library name and resolves it to libcares.a WITHOUT the PUBLIC
# Winsock deps -- the tools then fail to link with "undefined reference to
# __imp_*". The backend image doesn't need these standalone diagnostic
# utilities (gRPC links libcares.a, not the tools), so disable them entirely
# via -DCARES_BUILD_TOOLS=OFF in the cmake configure below.

# boringssl's ssl_file.cc and ssl_x509.cc use X509_NAME as a C++ type, but
# Windows' wincrypt.h (pulled in transitively by <windows.h> in some
# mingw-w64 header chains) #defines X509_NAME as L"Name", a string literal.
# The macro turns `const X509_NAME *const *a` into `const L"Name" *const *a`
# and the whole file fails to compile. A per-file #undef doesn't work because
# wincrypt.h is re-included transitively through boringssl's own headers.
# Instead, define WIN32_LEAN_AND_MEAN globally via CMAKE_C/CXX_FLAGS so
# windows.h never pulls in wincrypt.h at all, eliminating the collision
# everywhere. This is the standard Windows idiom for avoiding wincrypt.h
# macro pollution (X509_NAME, X509_CERT, etc.) in code that uses OpenSSL/
# boringssl types.
#
# _WIN32_WINNT=0x600 must ride along on the same flags: abseil's
# win32_waiter.cc guards its Win32Waiter definitions on
# _WIN32_WINNT >= _WIN32_WINNT_VISTA, and win32_waiter.h is included before
# any header sets a default _WIN32_WINNT, so without an explicit define the
# gate stays off. per_thread_sem.cc pulls win32_waiter.h in late (after
# thread_identity.h has already defined the default), so it compiles with the
# gate ON and the synchronization archive ends up referencing Win32Waiter
# while win32_waiter.cc.o is empty -- protoc dies with "undefined reference
# to Win32Waiter::*" that no --start-group can fix, because the symbols are
# not in the archive at all. (gRPC's own CMakeLists only adds
# -D_WIN32_WINNT=0x600 under `if (MINGW)`, which the native cmake sets but
# the MSYS2/Cygwin cmake that CI resolves via PATH does not.)
#
# The flags are spelled out on each cmake configure below instead of through
# CMAKE_ARGS: ${CMAKE_ARGS} is word-split on spaces by bash, so a
# space-containing -D value cannot travel through it.
export CMAKE_ARGS="${CMAKE_ARGS:-}"

# boringssl's bssl CLI tool uses Winsock functions (socket, connect, select,
# WSAStartup, ...) directly in transport_common.cc, but its CMakeLists only
# links bssl with ssl crypto, omitting ws2_32. A global
# -DCMAKE_EXE_LINKER_FLAGS=-lws2_32 cannot fix this: CMake places those flags
# before the object files on the link line, so ld cannot use the library to
# satisfy the references that only appear later, and bssl still fails with
# "undefined reference to `__imp_select'". Patch the CMakeLists instead.
# gRPC adds the whole boringssl-with-bazel dir via add_subdirectory, so the
# bssl target lives in the ROOT CMakeLists.txt (its binary dir shows as
# third_party/boringssl-with-bazel/CMakeFiles/bssl.dir/), not src/CMakeLists.txt.
# Both files declare the same `target_link_libraries(bssl ssl crypto)` line, so
# rewrite it in place to add ws2_32. Editing the exact declaration (instead of
# appending a guarded block) cannot be skipped by target visibility or block
# evaluation order: when the line is read, ssl and crypto are already linked
# and the bssl target already exists.
for bssl_cmake in \
  third_party/boringssl-with-bazel/CMakeLists.txt \
  third_party/boringssl-with-bazel/src/CMakeLists.txt
do
  if [ -f "$bssl_cmake" ]; then
    sed -i 's/^target_link_libraries(bssl ssl crypto)/target_link_libraries(bssl ssl crypto ws2_32)/' "$bssl_cmake"
    grep -q 'target_link_libraries(bssl ssl crypto ws2_32)' "$bssl_cmake" || \
      { echo "ERROR: failed to patch bssl link in $bssl_cmake" >&2; exit 1; }
  fi
done

# zlib bundled with gRPC ships win32/zlib1.rc which the mingw-w64
# binutils 2.46+ windres rejects ("zlib1.rc:7: syntax error" — the VERSIONINFO
# resource format it emits is no longer accepted). The .rc is only used to
# embed Windows version metadata into the DLL; it is unnecessary for a static
# zlib linked into protobuf/gRPC. Overwriting the file with a minimal valid
# resource is format-independent (no need to know the pinned submodule's
# CMakeLists spelling, and the file is unconditionally named win32/zlib1.rc on
# WIN32) and produces no side effects for a static library.
cat > third_party/zlib/win32/zlib1.rc <<'ZLIB_RC_FIX'
1 VERSIONINFO
FILEVERSION 1,3,0,0
PRODUCTVERSION 1,3,0,0
BEGIN
END
ZLIB_RC_FIX

# The grpc Makefile's MSYS/MINGW branches already add OPENSSL_NO_ASM=ON. We
# still pass an explicit generator + compiler: a bare cmake on windows-latest
# would pick the Visual Studio generator, producing an MSVC gRPC that the
# mingw-built grpc-server cannot link against.
cd "$GRPC_DIR/grpc_build"
# The bundled c-ares (1.19.1) still declares cmake_minimum_required < 3.5,
# which cmake >= 4 removed support for. MSYS2 ships cmake 4.x, so configure
# with the minimum policy version c-ares' CMakeLists can still read.
# Static-library resolution on MinGW is order-sensitive: GNU ld scans each
# archive once and a definition that lives before the reference in the link
# line is never revisited. That is why protoc dies with "__imp_Sym*"
# (absl's symbolize needs dbghelp) unless dbghelp is reachable from a cyclic
# group: CMake places CMAKE_EXE_LINKER_FLAGS before the object files, so a
# bare -ldbghelp would be scanned before any reference to it exists and never
# revisited. The --start-group here is what makes it work - it extends to the
# end of the link line (ld closes it implicitly) and makes ld rescan all
# archives cyclically until no new member is pulled, so dbghelp resolves
# regardless of archive order. (The historical "Win32Waiter::*" failures are
# NOT a link-order problem; see the WIN32_LEAN_AND_MEAN/_WIN32_WINNT comment
# above - those symbols were missing from the archive entirely.) The same
# applies to shared links.
cmake -G "Unix Makefiles" \
  -DCMAKE_BUILD_TYPE=Release \
  -DCMAKE_C_COMPILER=gcc \
  -DCMAKE_CXX_COMPILER=g++ \
  -DCMAKE_C_FLAGS="-DWIN32_LEAN_AND_MEAN -D_WIN32_WINNT=0x600" \
  -DCMAKE_CXX_FLAGS="-DWIN32_LEAN_AND_MEAN -D_WIN32_WINNT=0x600" \
  -DCMAKE_POLICY_VERSION_MINIMUM=3.5 \
  -DCMAKE_EXE_LINKER_FLAGS="-Wl,--start-group -ldbghelp" \
  -DCMAKE_SHARED_LINKER_FLAGS="-Wl,--start-group -ldbghelp" \
  -DCMAKE_PREFIX_PATH="${INSTALLED_PACKAGES};${CMAKE_PREFIX_PATH:-}" \
  ${CMAKE_ARGS:-} \
  -DgRPC_INSTALL=ON \
  -DEXECUTABLE_OUTPUT_PATH=../installed_packages/grpc/bin \
  -DLIBRARY_OUTPUT_PATH=../installed_packages/grpc/lib \
  -DgRPC_BUILD_TESTS=OFF \
  -DCARES_BUILD_TOOLS=OFF \
  -DgRPC_BUILD_CSHARP_EXT=OFF \
  -DgRPC_BUILD_GRPC_CPP_PLUGIN=ON \
  -DgRPC_BUILD_GRPC_CSHARP_PLUGIN=OFF \
  -DgRPC_BUILD_GRPC_NODE_PLUGIN=OFF \
  -DgRPC_BUILD_GRPC_OBJECTIVE_C_PLUGIN=OFF \
  -DgRPC_BUILD_GRPC_PHP_PLUGIN=OFF \
  -DgRPC_BUILD_GRPC_PYTHON_PLUGIN=ON \
  -DgRPC_BUILD_GRPC_RUBY_PLUGIN=OFF \
  -Dprotobuf_WITH_ZLIB=ON \
  -DZLIB_ROOT=${INSTALLED_PACKAGES} \
  -DRE2_BUILD_TESTING=OFF \
  -DCMAKE_INSTALL_PREFIX=../installed_packages \
  -DOPENSSL_NO_ASM=ON \
  ../grpc_repo/grpc
# Diagnostics: print the bssl link line that CMake generated. bssl uses Winsock
# directly and dies with __imp_select/__imp_* undefined references ~30 minutes
# into the build if ws2_32 is not on its link line, so surface it now while the
# CI log still has the configure context. Verify ws2_32 actually landed.
echo "=== cmake flavor (must be the UCRT64 native build; MSYS2's /usr/bin/cmake
splits the Windows exe names and, unlike the native build, does not set MINGW) ==="
which cmake
cmake --version | head -1
echo "=== bssl link (expect ws2_32) ==="
if [ -f third_party/boringssl-with-bazel/CMakeFiles/bssl.dir/linkLibs.rsp ]; then
  cat third_party/boringssl-with-bazel/CMakeFiles/bssl.dir/linkLibs.rsp
elif [ -f third_party/boringssl-with-bazel/CMakeFiles/bssl.dir/build.make ]; then
  grep 'Linking CXX executable bssl' -A1 third_party/boringssl-with-bazel/CMakeFiles/bssl.dir/build.make || true
else
  echo "WARNING: bssl build files not generated"
fi
grep -rq 'ws2_32' third_party/boringssl-with-bazel/CMakeFiles/bssl.dir/ && \
  echo "OK: bssl link includes ws2_32" || echo "FAIL: ws2_32 missing from bssl link"
# Diagnostics: protoc's link rule must carry --start-group + dbghelp, otherwise
# the absl static archives (symbolize) fail ~40 minutes in with undefined
# references that CMake cannot reorder away.
echo "=== protoc link (expect --start-group and -ldbghelp) ==="
if [ -f third_party/protobuf/CMakeFiles/protoc.dir/build.make ]; then
  grep 'Linking CXX executable' -A1 third_party/protobuf/CMakeFiles/protoc.dir/build.make || true
else
  echo "WARNING: protoc build files not generated"
fi
grep -rq -- '--start-group' third_party/protobuf/CMakeFiles/protoc.dir/ && \
  echo "OK: protoc link has --start-group" || echo "FAIL: --start-group missing from protoc link"
cmake --build . -j "$JOBS"
# Diagnostics: the _WIN32_WINNT=0x600 flag above must have compiled
# win32_waiter.cc with its gate open; confirm the archive actually defines
# Win32Waiter (a successful protoc link already implies it, but surface the
# count for the log).
echo "=== absl synchronization archive (expect Win32Waiter T symbols) ==="
# LIBRARY_OUTPUT_PATH=../installed_packages/grpc/lib is relative to the top
# build dir, so the archive lands next to the build tree, not under
# third_party/abseil-cpp.
nm ../installed_packages/grpc/lib/libabsl_synchronization.a 2>/dev/null | \
  grep -c 'Win32Waiter' || echo "WARNING: no Win32Waiter symbols in libabsl_synchronization.a"
cmake --build . --target install

INSTALLED_PACKAGES="$GRPC_DIR/installed_packages"
ADDED_CMAKE_ARGS="-Dabsl_DIR=${INSTALLED_PACKAGES}/lib/cmake/absl \
  -DProtobuf_DIR=${INSTALLED_PACKAGES}/lib/cmake/protobuf \
  -Dutf8_range_DIR=${INSTALLED_PACKAGES}/lib/cmake/utf8_range \
  -DgRPC_DIR=${INSTALLED_PACKAGES}/lib/cmake/grpc \
  -DZLIB_ROOT=${INSTALLED_PACKAGES} \
  -DZLIB_INCLUDE_DIR=${INSTALLED_PACKAGES}/include \
  -DZLIB_LIBRARY=${INSTALLED_PACKAGES}/lib/libzlibstatic.a \
  -DZLIB_INCLUDE_DIRS=${INSTALLED_PACKAGES}/include \
  -DZLIB_LIBRARIES=${INSTALLED_PACKAGES}/lib/libzlibstatic.a \
  -DCMAKE_CXX_STANDARD_INCLUDE_DIRECTORIES=${INSTALLED_PACKAGES}/include"

# Restore system zlib for llama.cpp builds (to avoid conflicts) if it was backed up
restore_sys_zlib 2>/dev/null || true
# But we want to force static zlib for llama.cpp - backup again before building variants
backup_sys_zlib 2>/dev/null || true
# find_program(protoc / grpc_cpp_plugin) in the grpc-server CMakeLists searches
# PATH, so the freshly installed protoc + grpc_cpp_plugin must be reachable.
export PATH="${INSTALLED_PACKAGES}/bin:${PATH}"

# ---------------------------------------------------------------------------
# llama.cpp source at the pinned version, with the LocalAI grpc-server patch
# ---------------------------------------------------------------------------
cd "$ROOT/backend/cpp/llama-cpp"
LLAMA_VERSION=$(grep '^LLAMA_VERSION' Makefile | head -1 | cut -d= -f2 | cut -d'?' -f1 | tr -d ' ')
mkdir -p llama.cpp
cd llama.cpp
git init -q
# Re-runnable like the gRPC clone above; -B resets a stale `build` branch.
git remote remove origin 2>/dev/null || true
git remote add origin https://github.com/ggerganov/llama.cpp
# Shallow-fetch the pinned commit (allowAnySHA1InWant is on for public repos),
# then the pinned submodules shallowly. Equivalent content to `make llama.cpp`
# without downloading the full llama.cpp history on every build.
git fetch origin "$LLAMA_VERSION" --depth 1
# prepare.sh patches the source tree in place; a previous (re-run) build may
# have left the working tree dirty, which would make the checkout below fail.
# Restore the pristine tree BEFORE checking out (tracked edits, untracked
# .rej/.orig and the generated tools/grpc-server staging dir).
git reset -q --hard
git clean -qfd
mkdir -p tools/grpc-server
git checkout -q -B build "$LLAMA_VERSION"
git submodule update --init --recursive --depth 1 --single-branch
cd "$ROOT/backend/cpp/llama-cpp"
bash prepare.sh

# ---------------------------------------------------------------------------
# The three cmake variants (flags mirror backend/cpp/llama-cpp/Makefile)
# ---------------------------------------------------------------------------
build_variant() {
  local name="$1"
  local targets="$2"
  shift 2
  # ggml turns ccache on when it finds it, but on MSYS2 ccache wants
  # Windows-style USERPROFILE/LOCALAPPDATA the login profile drops - disable
  # it (the variant builds are from scratch anyway).
  rm -rf "llama.cpp/build-${name}"
  # Force zlib to use the gRPC-installed static one to avoid mixing with system DLL import lib
  export PKG_CONFIG_PATH="${INSTALLED_PACKAGES}/lib/pkgconfig:${PKG_CONFIG_PATH:-}"
  cmake -G "Unix Makefiles" \
    -S llama.cpp -B "llama.cpp/build-${name}" \
    -DCMAKE_BUILD_TYPE=Release \
    -DCMAKE_C_COMPILER=gcc \
    -DCMAKE_CXX_COMPILER=g++ \
    -DCMAKE_C_FLAGS="-DWIN32_LEAN_AND_MEAN -D_WIN32_WINNT=0xA00" \
    -DCMAKE_CXX_FLAGS="-DWIN32_LEAN_AND_MEAN -D_WIN32_WINNT=0xA00" \
    -DCMAKE_EXE_LINKER_FLAGS="-Wl,--start-group -ldbghelp" \
    -DCMAKE_PREFIX_PATH="${INSTALLED_PACKAGES};${CMAKE_PREFIX_PATH:-}" \
    ${CMAKE_ARGS:-} \
    -DGGML_NATIVE=OFF \
    -DLLAMA_OPENSSL=OFF \
    -DLLAMA_CURL=OFF \
    -DBUILD_SHARED_LIBS=OFF \
    -DGGML_CCACHE=OFF \
    "$@" \
    $ADDED_CMAKE_ARGS
  cmake --build "llama.cpp/build-${name}" --config Release -j "$JOBS" --target $targets
}

# Main build: one grpc-server plus, with BUILD_SHARED_LIBS=ON +
# GGML_BACKEND_DL=ON + GGML_CPU_ALL_VARIANTS=ON, the dlopen-able
# libggml-cpu-*.dll set. GGML_VULKAN=ON adds the dlopen-able
# libggml-vulkan.dll to the same image, so the single grpc-server
# auto-detects a Vulkan device at runtime (ggml-vulkan loads vulkan-1.dll
# dynamically - it is never in the import table) and falls back to CPU when
# the host has no Vulkan driver. The cuda main build is configured without
# CUDA flags here: ggml-cuda.dll comes from the MSVC side-build below and is
# loaded at runtime the same way. No separate launcher change is needed: one
# image works everywhere and GPU offload just works when a model requests
# gpu_layers.
MAIN_FLAGS="-DBUILD_SHARED_LIBS=ON -DGGML_BACKEND_DL=ON -DGGML_CPU_ALL_VARIANTS=ON"

# Build the CUDA backend DLL with the MSVC toolchain. nvcc on Windows only
# hosts on cl.exe, and a CMake tree whose C/CXX compilers are GNU cannot link
# MSVC-hosted CUDA objects (the CUDA language flips the link rules to MSVC
# style while GNU ld is in use - CMake refuses to generate). The image loads
# backends at runtime (GGML_BACKEND_DL), so only this one DLL comes from the
# MSVC side-build; the boundary to the MinGW-built ggml-base.dll is the
# extern "C" ggml API, and both sides link the same UCRT.
build_cuda_msvc_backend() {
  local build_dir="llama.cpp/build-cuda-msvc"
  rm -rf "$build_dir"
  # The Visual Studio generator is used deliberately: Ninja + cl would need a
  # vcvars environment (rc.exe, SDK dirs), while the VS generator locates the
  # whole toolset itself. The generator name carries the release year, mapped
  # from the installation's major version.
  local vs_gen=""
  local vs_major
  vs_major=$("/c/Program Files (x86)/Microsoft Visual Studio/Installer/vswhere.exe" \
    -latest -products '*' -requires Microsoft.VisualStudio.Component.VC.Tools.x86.x64 \
    -property installationVersion 2>/dev/null | head -1 | cut -d. -f1)
  case "$vs_major" in
    15) vs_gen="Visual Studio 15 2017" ;;
    16) vs_gen="Visual Studio 16 2019" ;;
    17) vs_gen="Visual Studio 17 2022" ;;
    18) vs_gen="Visual Studio 18 2026" ;;
  esac
  if [ -z "$vs_gen" ]; then
    echo "ERROR: could not map Visual Studio version '$vs_major' to a CMake generator name; extend build_cuda_msvc_backend in $0." >&2
    exit 1
  fi
  # CUDA architectures stay at ggml's default set (every GPU). CMAKE_ARGS
  # (the CI ccache launchers) and the gRPC hint dirs are deliberately not
  # forwarded: they belong to the mingw pass, and this configure builds no
  # gRPC consumer (LLAMA_BUILD_TOOLS=OFF).
  cmake -S llama.cpp -B "$build_dir" -G "$vs_gen" -A x64 \
    -DGGML_CUDA=ON \
    -DGGML_BACKEND_DL=ON \
    -DBUILD_SHARED_LIBS=ON \
    -DGGML_NATIVE=OFF \
    -DLLAMA_OPENSSL=OFF \
    -DLLAMA_CURL=OFF \
    -DGGML_CPU_ALL_VARIANTS=OFF \
    -DGGML_OPENMP=OFF \
    -DLLAMA_BUILD_TOOLS=OFF \
    -DGGML_BUILD_EXAMPLES=OFF \
    -DGGML_BUILD_TESTS=OFF \
    "-DCMAKE_CUDA_HOST_COMPILER=$(cygpath -m "$_cl_exe")"
  cmake --build "$build_dir" --config Release --target ggml-cuda -j "$JOBS"
}

case "$MAIN_VARIANT" in
  vulkan) build_variant "cpu-all" "grpc-server ggml" $MAIN_FLAGS -DGGML_VULKAN=ON ;;
  cpu)    build_variant "cpu-all" "grpc-server ggml" $MAIN_FLAGS ;;
  cuda)
    if [ -z "$_cuda_root" ] || [ -z "$_cl_exe" ]; then
      echo "ERROR: the CUDA toolkit root or the MSVC host compiler could not be resolved; set CUDA_PATH and install the Visual Studio C++ workload." >&2
      exit 1
    fi
    # The MinGW tree is built WITHOUT CUDA: nvcc-hosted CUDA objects cannot
    # be linked by the GNU build. ggml-cuda.dll comes from the MSVC
    # side-build above and is loaded at runtime by the backend registry.
    build_variant "cpu-all" "grpc-server ggml" $MAIN_FLAGS
    build_cuda_msvc_backend
    ;;
esac

# gRPC-RPC server + the ggml-rpc-server companion binary.
if has_variant rpc; then
  build_variant "rpc" "grpc-server ggml-rpc-server" \
    -DGGML_RPC=ON \
    -DGGML_AVX=off -DGGML_AVX2=off -DGGML_AVX512=off \
    -DGGML_FMA=off -DGGML_F16C=off -DGGML_BMI2=off
fi

# Static fallback, mirrors llama-cpp-fallback.
if has_variant fallback; then
  build_variant "fallback" "grpc-server" \
    -DGGML_AVX=off -DGGML_AVX2=off -DGGML_AVX512=off \
    -DGGML_FMA=off -DGGML_F16C=off -DGGML_BMI2=off
fi

# ---------------------------------------------------------------------------
# Stage the image contents
# ---------------------------------------------------------------------------
STAGE="$ROOT/build/windows"
rm -rf "$STAGE"
mkdir -p "$STAGE" "$ROOT/backend-images"

# The main build's grpc-server keeps the historical llama-cpp-cpu-all.exe name:
# run.ps1 selects it by that name. The rpc build below is used only when
# LLAMACPP_GRPC_SERVERS is set, as in run.sh.
cp llama.cpp/build-cpu-all/bin/grpc-server.exe "$STAGE/llama-cpp-cpu-all.exe"
# ggml's shared backends are loadable DLLs; they go next to the executable so
# the registry finds them without any PATH setup (mirrors run.sh LD_LIBRARY_PATH).
find llama.cpp/build-cpu-all/bin -maxdepth 1 -name '*.dll' -exec cp -v {} "$STAGE/" \;

if has_variant rpc; then
  cp llama.cpp/build-rpc/bin/grpc-server.exe "$STAGE/llama-cpp-grpc.exe"
  cp llama.cpp/build-rpc/bin/ggml-rpc-server.exe "$STAGE/llama-cpp-rpc-server.exe"
fi

if has_variant fallback; then
  cp llama.cpp/build-fallback/bin/grpc-server.exe "$STAGE/llama-cpp-fallback.exe"
fi

# The CUDA backend DLL is built by the MSVC side-build (see
# build_cuda_msvc_backend); the backend registry dlopens it at model load.
if [ "$MAIN_VARIANT" = "cuda" ]; then
  cp -v llama.cpp/build-cuda-msvc/bin/Release/ggml-cuda.dll "$STAGE/"
fi

cd "$ROOT"
# The launcher is the PowerShell script pkg/model/process.go starts on Windows
# (run.ps1, mirroring run.sh); run.sh below is the stub that keeps
# discovery/validation/upgrade uniform with the other platforms.
cp backend/cpp/llama-cpp/run.ps1 "$STAGE/run.ps1"
cp backend/cpp/llama-cpp/run.sh "$STAGE/run.sh"

# Bundle the runtime DLLs (mingw gcc runtime, openssl when gRPC links it
# dynamically) so the image runs on a host with no MSYS2. ggml's shared
# backends are already in $STAGE; scanning them too (not just the .exe files)
# catches the runtime deps of the loadable modules (ggml-vulkan.dll pulls in
# libgcc/libstdc++/libwinpthread) and lets the vulkan loader be bundled below.
for bin in "$STAGE"/*.exe "$STAGE"/*.dll; do
  objdump -p "$bin" | awk '/DLL Name:/ {print $3}' | while read -r dll; do
    if [ -f "/ucrt64/bin/$dll" ] && [ ! -e "$STAGE/$dll" ]; then
      cp -v "/ucrt64/bin/$dll" "$STAGE/"
    fi
  done
done

# ggml-vulkan loads vulkan-1.dll dynamically (volk-style LoadLibrary), so it is
# not in any import table and the scan above cannot see it. Bundle the loader
# from the MSYS2 vulkan-loader package anyway: on a host with no Vulkan driver
# the loader still initializes, ggml-vulkan registers zero devices, and CPU
# inference keeps working - but on a host whose driver does not drop its own
# copy into system32 the GPU would otherwise be invisible.
if [ "$MAIN_VARIANT" = "vulkan" ] && [ -f "/ucrt64/bin/vulkan-1.dll" ] && [ ! -e "$STAGE/vulkan-1.dll" ]; then
  cp -v "/ucrt64/bin/vulkan-1.dll" "$STAGE/"
fi

# The CUDA backend's runtime libraries (cudart64_*.dll, cublas64_*.dll, ...)
# come from the NVIDIA toolkit, not from /ucrt64/bin, so the import-table scan
# above cannot find them there. Bundle the transitive closure: scan the
# staged DLLs' imports, copy what resolves into the toolkit's bin dirs, and
# repeat until nothing new is copied (cublas64 pulls cublasLt64, which pulls
# nvJitLink, ...), so the image also runs on hosts that have the NVIDIA
# driver but no CUDA toolkit installed.
if [ "$MAIN_VARIANT" = "cuda" ]; then
  CUDA_BINS=()
  for _sub in bin/x64 bin; do
    _d="$(cygpath -u "$CUDA_PATH")/$_sub"
    if [ -d "$_d" ]; then
      CUDA_BINS+=("$_d")
    fi
  done
  if [ "${#CUDA_BINS[@]}" -gt 0 ]; then
    # cudart64_*.dll never appears in an import table (the runtime is pulled
    # dynamically inside the CUDA libraries, like vulkan-1.dll), so it is
    # copied unconditionally before the import-table closure walk.
    for _cb in "${CUDA_BINS[@]}"; do
      for _rt in "$_cb"/cudart64_*.dll; do
        if [ -f "$_rt" ] && [ ! -e "$STAGE/$(basename "$_rt")" ]; then
          cp -v "$_rt" "$STAGE/"
        fi
      done
    done
    _copied=1
    while [ "$_copied" -eq 1 ]; do
      _copied=0
      for bin in "$STAGE"/*.dll; do
        for dll in $(objdump -p "$bin" | awk '/DLL Name:/ {print $3}'); do
          if [ ! -e "$STAGE/$dll" ]; then
            for cb in "${CUDA_BINS[@]}"; do
              if [ -f "$cb/$dll" ]; then
                cp -v "$cb/$dll" "$STAGE/"
                _copied=1
                break
              fi
            done
          fi
        done
      done
    done
  else
    echo "WARNING: could not locate the CUDA toolkit bin dir; assuming a statically linked CUDA runtime." >&2
  fi
fi

echo "Bundled DLLs:"
ls -la "$STAGE"

# ---------------------------------------------------------------------------
# Package as an OCI image tar
# ---------------------------------------------------------------------------
# local-ai.exe is only the tool that assembles the OCI tar - the image carries
# $STAGE, not this binary. cmd/local-ai still needs the prereqs of the upstream
# `make build`: pkg/grpc/proto/*.pb.go (gitignored, absent in a fresh checkout;
# regenerate with the protoc built above + the pinned Go plugins, mirroring
# `make protogen-go`) and the embedded react-ui dist (stubbed like lint.yml;
# the real bundle would need node, and this binary never serves it).
GO_PLUGIN_DIR="$(go env GOPATH)/bin"
GO_PLUGIN_DIR_MSYS="$(cygpath -u "$GO_PLUGIN_DIR")"
if [ ! -x "$GO_PLUGIN_DIR_MSYS/protoc-gen-go" ]; then
  go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.34.2
fi
if [ ! -x "$GO_PLUGIN_DIR_MSYS/protoc-gen-go-grpc" ]; then
  go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@1958fcbe2ca8bd93af633f11e97d44e567e945af
fi
mkdir -p pkg/grpc/proto
"$GRPC_DIR/installed_packages/bin/protoc.exe" --experimental_allow_proto3_optional \
  -Ibackend/ \
  --go_out=pkg/grpc/proto/ --go_opt=paths=source_relative \
  --go-grpc_out=pkg/grpc/proto/ --go-grpc_opt=paths=source_relative \
  --plugin=protoc-gen-go="$GO_PLUGIN_DIR/protoc-gen-go.exe" \
  --plugin=protoc-gen-go-grpc="$GO_PLUGIN_DIR/protoc-gen-go-grpc.exe" \
  backend/backend.proto
mkdir -p pkg/motion/proto
"$GRPC_DIR/installed_packages/bin/protoc.exe" \
  -Ipkg/motion/proto \
  --go_out=pkg/motion/proto --go_opt=paths=source_relative \
  --plugin=protoc-gen-go="$GO_PLUGIN_DIR/protoc-gen-go.exe" \
  pkg/motion/proto/motion.proto
mkdir -p core/http/react-ui/dist
[ -f core/http/react-ui/dist/index.html ] || touch core/http/react-ui/dist/index.html

# The packaging tool is always local-ai.exe on Windows. Never invoke the
# bare "./local-ai": if a stale extensionless file from a cross-build sits in
# the repo root, MSYS bash prefers it over local-ai.exe and dies with
# "Exec format error" (exit 126).
if [ ! -f local-ai.exe ]; then
  CGO_ENABLED=0 go build -o local-ai.exe ./cmd/local-ai
fi
mkdir -p backend-images
./local-ai.exe util create-oci-image \
  build/windows/. \
  --output "$OUTPUT_TAR" \
  --image-name "$IMAGE_NAME" \
  --platform "$PLATFORMARCH"

rm -rf build/windows