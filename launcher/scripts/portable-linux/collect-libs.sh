#!/bin/bash
# Runs inside the pinned Debian container (see pins.json and after-pack.cjs).
# Collects, from the Debian trixie snapshot, the files the Electron binaries
# need on a host that has no glibc:
#   /app/glibc  the glibc loader and the libc family
#   /app/lib    every other shared library the binaries need, and the ones
#               Chromium dlopen()s later (invisible to ldd)
#   /app/etc    a CA bundle and a fontconfig fallback
# /app is the unpacked app (appOutDir). It must still have its original ELF
# interpreter: ldd runs the interpreter of each file.
# Environment: APT_SNAPSHOT (timestamp), HOST_UID, HOST_GID.
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive

rm -f /etc/apt/sources.list.d/debian.sources /etc/apt/sources.list
cat > /etc/apt/sources.list.d/snapshot.sources <<SRC
Types: deb
URIs: http://snapshot.debian.org/archive/debian/${APT_SNAPSHOT}/
Suites: trixie trixie-updates
Components: main
Signed-By: /usr/share/keyrings/debian-archive-keyring.gpg

Types: deb
URIs: http://snapshot.debian.org/archive/debian-security/${APT_SNAPSHOT}/
Suites: trixie-security
Components: main
Signed-By: /usr/share/keyrings/debian-archive-keyring.gpg
SRC
echo 'Acquire::Check-Valid-Until "false"; Acquire::Retries "5";' > /etc/apt/apt.conf.d/99snapshot

apt-get update -qq
apt-get install -y -qq --no-install-recommends \
  libnss3 libgtk-3-0t64 libgbm1 libasound2t64 libxss1 libxtst6 libnotify4 libcups2t64 \
  libatk-bridge2.0-0t64 libdrm2 libxkbcommon0 libx11-xcb1 libxcomposite1 libxdamage1 \
  libxrandr2 libpango-1.0-0 libcairo2 libdbus-1-3 fontconfig libwayland-client0 \
  libxfixes3 libxext6 libexpat1 ca-certificates \
  libxkbcommon-x11-0 libxi6 libxcursor1 libxrender1 libxshmfence1 libpulse0 \
  libxcb-dri3-0 libxcb-randr0 libxcb-xinput0 libxcb-xkb1 libxcb-shm0 libxcb-render0 \
  libxcb-present0 libxcb-sync1 libxcb-xfixes0 libxcb-shape0 libxcb-glx0 >/dev/null

cd /app
rm -rf glibc lib etc
mkdir -p glibc lib etc

# Everything that can load shared libraries: the executables and libraries
# of the app (not the data files).
mapfile -t targets < <(find . -type f \( -perm -u+x -o -name '*.so' -o -name '*.so.*' -o -name '*.node' \) \
  -exec sh -c 'for f; do [ "$(head -c4 "$f" | od -An -c | tr -d " ")" = "177ELF" ] && echo "$f"; done' sh {} +)
[ "${#targets[@]}" -gt 0 ] || { echo "no ELF files found in /app" >&2; exit 1; }

: > /tmp/libs.txt
for f in "${targets[@]}"; do
  out=$(LD_LIBRARY_PATH=/app ldd "$f" 2>&1 || true)
  if grep -q 'not found' <<<"$out"; then
    echo "missing libraries for $f:" >&2
    grep 'not found' <<<"$out" >&2
    exit 1
  fi
  awk '/=> \//{print $3}' <<<"$out" >> /tmp/libs.txt
done
sort -u /tmp/libs.txt | grep -v '^/app' > /tmp/libs-unique.txt

cp -L /lib64/ld-linux-x86-64.so.2 glibc/
while read -r l; do
  b=$(basename "$l")
  case $b in
    libc.so*|libm.so*|libdl.so*|libpthread.so*|librt.so*|libresolv.so*|libutil.so*|libnss_*|libanl*) cp -L "$l" glibc/ ;;
    *) cp -L "$l" lib/ ;;
  esac
done < /tmp/libs-unique.txt

# Name service plugins that glibc loads on demand.
cp -L /lib/x86_64-linux-gnu/libnss_dns.so.2 /lib/x86_64-linux-gnu/libnss_files.so.2 \
      /lib/x86_64-linux-gnu/libresolv.so.2 glibc/
# NSS (certificate store) modules that libnss3 loads on demand.
cp -L /usr/lib/x86_64-linux-gnu/libnss*.so /usr/lib/x86_64-linux-gnu/libsoftokn3.so \
      /usr/lib/x86_64-linux-gnu/libfreebl*.so /usr/lib/x86_64-linux-gnu/libsqlite3.so.0 lib/

# Libraries Chromium dlopen()s at run time. ldd does not list them.
for p in /usr/lib/x86_64-linux-gnu/libX11-xcb.so.1 /usr/lib/x86_64-linux-gnu/libxcb*.so.* \
         /usr/lib/x86_64-linux-gnu/libxkbcommon*.so.* /usr/lib/x86_64-linux-gnu/libXi.so.6 \
         /usr/lib/x86_64-linux-gnu/libXcursor.so.1 /usr/lib/x86_64-linux-gnu/libXrender.so.1 \
         /usr/lib/x86_64-linux-gnu/libxshmfence.so.1 /usr/lib/x86_64-linux-gnu/libpulse*.so.0; do
  cp -L "$p" lib/
done

cp -L /etc/ssl/certs/ca-certificates.crt etc/
cp -r /etc/fonts etc/fonts

# Provenance, so a build can be compared with another one.
{
  echo "debian snapshot ${APT_SNAPSHOT}"
  dpkg-query -W -f '${Package} ${Version}\n' | sort
} > etc/bundled-packages.txt

du -sh glibc lib etc
chown -R "${HOST_UID}:${HOST_GID}" glibc lib etc
