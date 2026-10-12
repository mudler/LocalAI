# Linux launcher AppImage

This packages only the Go/Fyne launcher. It does not bundle the LocalAI server,
models, a worker interface, or an AppImage updater. The existing runtime
downloader remains responsible for installing LocalAI on first use.

## Build

Use native **Linux amd64 (x86_64)**, Go from `go.mod`, GCC, GNU Make, curl,
pkg-config and coreutils. On Ubuntu 22.04 the development packages are:

```sh
sudo apt-get install build-essential pkg-config libgl1-mesa-dev xorg-dev libxkbcommon-dev
make test-launcher-appimage
make build-launcher-appimage APPIMAGE_ARCH=amd64
```

Output: `dist/LocalAI-Launcher-x86_64.AppImage` and its `.sha256` file.
Only amd64 is supported, matching the existing Linux launcher release job.
ARM64 requires a native CGO builder and separate runtime/tool pins; it is not
silently cross-compiled or labelled as supported.

The release builder is Ubuntu 22.04 (glibc 2.35). Fyne uses CGO, OpenGL and X11;
this is **not a static or fully self-contained executable**. We intentionally
use host graphics libraries rather than bundle drivers or glibc, and never set
`LD_LIBRARY_PATH`: the downloaded server must not inherit launcher libraries.
Ubuntu 22.04+ desktop systems are the compatibility target. Other distributions
need glibc >= 2.35, libGL, libX11, libXcursor, libXrandr, libXinerama, libXi and
libXxf86vm plus their dependencies. Wayland needs XWayland. A working tray is
recommended because the existing launcher starts in the tray. The separately
downloaded server/backends have their own system requirements.

`build.sh` pins appimagetool 1.9.1 and type-2 runtime 20251108 by release and
SHA-256. Both hashes come from the upstream release asset digests. Cached files
are verified on every build. To update, review the upstream release and replace
the URL and hash together. The build extracts appimagetool explicitly, so CI
needs no FUSE. No linuxdeploy or unpinned Fyne packaging tool is used.

AppDir contents and timestamps are controlled, Go paths/build IDs are stripped,
and `SOURCE_DATE_EPOCH` defaults to the source commit timestamp. Repeatable
builds require the same Go/compiler/system packages; this does not promise
bit-identical output across arbitrary distributions. `dist/appimage/ldd.txt`
records the actual dynamic dependency tree of each build.

## Verification and release

```sh
make test-launcher-appimage
# After building; requires desktop-file-utils, xvfb and xauth:
bash contrib/appimage/smoke.sh
```

The smoke extracts the real image, validates metadata/dependencies, then runs
its AppRun under Xvfb with an isolated home and server auto-start disabled. It
requires the UI event loop to survive until the timeout; it does not test a
model or GPU workload. Logs are in `dist/appimage/smoke/`.

The reusable workflow builds and smokes on relevant pull requests or manual
dispatch without publishing. Version tags call it after GoReleaser succeeds,
then upload the image and checksum to that existing release. Existing tar.xz,
DMG and server artifacts remain unchanged.
