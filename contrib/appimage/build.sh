#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
set -euo pipefail
root=$(cd "$(dirname "$0")/../.." && pwd)
cd "$root"
# Match the existing Linux launcher release (ubuntu x86_64). CGO makes this a
# native build, not a Go-only cross compile.
arch=${APPIMAGE_ARCH:-amd64}
if [[ "$arch" != amd64 ]]; then
    echo 'AppImage supports only amd64; use a native x86_64 Linux builder.' >&2
    exit 1
fi
[[ $(uname -s) == Linux && $(uname -m) == x86_64 ]] || {
    echo 'AppImage requires a native x86_64 Linux builder.' >&2; exit 1;
}
# appimagetool needs file even after the Go compilation succeeds.
command -v file >/dev/null 2>&1 || {
    echo 'Missing file command required by appimagetool; install the file package.' >&2; exit 1;
}
pkg-config --exists gl x11 xcursor xrandr xinerama xi xxf86vm || {
    echo 'Missing Fyne development libraries; see contrib/appimage/README.md.' >&2; exit 1;
}
build="$root/dist/appimage"
tools="$build/tools"
appdir="$build/AppDir"
mkdir -p "$tools" "$build/tmp"
export TMPDIR="$build/tmp"
export SOURCE_DATE_EPOCH=${SOURCE_DATE_EPOCH:-$(git log -1 --format=%ct)}
fetch() {
    local url=$1 file=$2 sha=$3
    if [[ ! -f "$file" ]]; then
        curl --fail --location --retry 3 "$url" -o "$file.part"
        mv "$file.part" "$file"
    fi
    printf '%s  %s\n' "$sha" "$file" | sha256sum --check --status || {
        echo "Checksum mismatch: $file (remove it before retrying)" >&2; exit 1;
    }
    chmod +x "$file"
}
fetch https://github.com/AppImage/appimagetool/releases/download/1.9.1/appimagetool-x86_64.AppImage \
    "$tools/appimagetool" ed4ce84f0d9caff66f50bcca6ff6f35aae54ce8135408b3fa33abfc3cb384eb0
fetch https://github.com/AppImage/type2-runtime/releases/download/20251108/runtime-x86_64 \
    "$tools/runtime-x86_64" 2fca8b443c92510f1483a883f60061ad09b46b978b2631c807cd873a47ec260d
rm -rf "$appdir" "$tools/squashfs-root"
mkdir -p "$appdir/usr/bin"
CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -mod=readonly -trimpath -buildvcs=false \
    -ldflags='-s -w -buildid=' -o "$appdir/usr/bin/local-ai-launcher" ./cmd/launcher
# Do not ship graphics drivers, glibc, or inject a library path into the server.
ldd "$appdir/usr/bin/local-ai-launcher" | tee "$build/ldd.txt"
if grep -q 'not found' "$build/ldd.txt"; then exit 1; fi
install -m755 contrib/appimage/AppRun "$appdir/AppRun"
install -m644 contrib/appimage/local-ai-launcher.desktop "$appdir/"
install -m644 cmd/launcher/logo.png "$appdir/local-ai-launcher.png"
ln -s local-ai-launcher.png "$appdir/.DirIcon"
install -Dm644 LICENSE "$appdir/usr/share/licenses/local-ai-launcher/LICENSE"
find "$appdir" -exec touch -h -d "@$SOURCE_DATE_EPOCH" {} +
# Explicit extraction avoids FUSE even on unprivileged CI builders.
(cd "$tools" && ./appimagetool --appimage-extract >/dev/null)
ARCH=x86_64 "$tools/squashfs-root/AppRun" --runtime-file "$tools/runtime-x86_64" \
    "$appdir" "$root/dist/LocalAI-Launcher-x86_64.AppImage"
(cd dist && sha256sum LocalAI-Launcher-x86_64.AppImage > LocalAI-Launcher-x86_64.AppImage.sha256)
