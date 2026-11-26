#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# Builds build/p2pmobile.aar with gomobile and checks that every native
# library in it has 16 KB aligned LOAD segments (Android 15+ devices with
# 16 KB pages, and Google Play, refuse libraries that are not).
#
# Usage: build.sh [abis]
#   abis: comma-separated list of arm64 and amd64 (default: arm64).
#         CI test builds use arm64,amd64 so the x86_64 emulator can run it.
#
# Needs: Go (the version of go.mod), a JDK (17 or newer) and ANDROID_HOME or
# ANDROID_NDK_HOME pointing at an SDK with the NDK.
set -euo pipefail

cd "$(dirname "$0")"

abis="${1:-arm64}"
targets=""
IFS=',' read -r -a list <<<"$abis"
for abi in "${list[@]}"; do
  case "$abi" in
    arm64 | amd64) ;;
    *)
      echo "build.sh: unknown ABI '$abi' (use arm64 and/or amd64)" >&2
      exit 2
      ;;
  esac
  targets="${targets:+$targets,}android/$abi"
done
if [ -z "$targets" ]; then
  echo "build.sh: no ABI given" >&2
  exit 2
fi

ndk="${ANDROID_NDK_HOME:-}"
if [ -z "$ndk" ] && [ -n "${ANDROID_HOME:-}" ]; then
  ndk="$(ls -d "$ANDROID_HOME"/ndk/* 2>/dev/null | sort -V | tail -n 1 || true)"
fi
if [ -z "$ndk" ] || [ ! -d "$ndk" ]; then
  echo "build.sh: set ANDROID_NDK_HOME (or ANDROID_HOME with an NDK installed)" >&2
  exit 2
fi
export ANDROID_NDK_HOME="$ndk"
readelf="$(ls "$ndk"/toolchains/llvm/prebuilt/*/bin/llvm-readelf 2>/dev/null | head -n 1 || true)"
if [ -z "$readelf" ]; then
  echo "build.sh: llvm-readelf not found in $ndk" >&2
  exit 2
fi

mkdir -p build/bin
# gomobile and gobind at the version go.mod pins (tool directive), so the
# binding code matches the golang.org/x/mobile/bind package the AAR links.
go build -o build/bin/ golang.org/x/mobile/cmd/gomobile golang.org/x/mobile/cmd/gobind
export PATH="$PWD/build/bin:$PATH"

# -checklinkname=0: github.com/wlynxg/anet uses go:linkname to reach
# net.zoneCache, which Go 1.23+ refuses at link time. libp2p's WebRTC
# transport imports it through pion/transport, and the default libp2p
# transports always link that. The anet README documents this flag.
gomobile bind \
  -target="$targets" \
  -androidapi 26 \
  -javapkg io.localai.p2p \
  -trimpath \
  -ldflags="-checklinkname=0 -s -w" \
  -o build/p2pmobile.aar \
  .

check="build/aar-check"
rm -rf "$check"
mkdir -p "$check"
unzip -q build/p2pmobile.aar -d "$check"
mapfile -t libs < <(find "$check" -name '*.so' | sort)
if [ "${#libs[@]}" -eq 0 ]; then
  echo "build.sh: no native library in the AAR" >&2
  exit 1
fi
bad=0
for so in "${libs[@]}"; do
  aligns="$("$readelf" -lW "$so" | awk '$1 == "LOAD" { print $NF }')"
  if [ -z "$aligns" ]; then
    echo "build.sh: ${so#"$check"/}: no LOAD segment" >&2
    bad=1
    continue
  fi
  ok=1
  for a in $aligns; do
    if (((a & 0x3fff) != 0 || a < 0x4000)); then
      echo "build.sh: ${so#"$check"/}: LOAD segment aligned to $a, need 0x4000" >&2
      ok=0
      bad=1
    fi
  done
  if [ "$ok" -eq 1 ]; then
    echo "build.sh: ${so#"$check"/}: LOAD segments aligned to $(echo "$aligns" | sort -u | tr '\n' ' ')"
  fi
done
rm -rf "$check"
if [ "$bad" -ne 0 ]; then
  exit 1
fi
echo "build.sh: wrote build/p2pmobile.aar for $targets"
