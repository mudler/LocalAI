#!/bin/sh
# Install the toolchain of the cpu-variants stage (see .docker/llama-cpp-cpu-variants.sh)
# on Ubuntu 22.04: gcc-12 on amd64, gcc-14 on arm64 (the armv9.2 SME variants need it,
# and 22.04 only has it in the toolchain PPA). Used by every Dockerfile with that stage.
# Reads TARGETARCH, and APT_MIRROR / APT_PORTS_MIRROR through apt-mirror.sh.
set -eux

here=$(dirname "$0")
export DEBIAN_FRONTEND=noninteractive

sh "$here/apt-mirror" || true
sh "$here/apt-mirror" apt-get update -qq
sh "$here/apt-mirror" apt-get install -y --no-install-recommends \
  ca-certificates curl gpg git cmake make ccache binutils

if [ "${TARGETARCH}" = "arm64" ]; then
  gcc_version=14
  # add-apt-repository is not used: it starts gpg-agent, which cannot run inside a
  # BuildKit RUN. Fetch the signing key of the PPA and register it by hand.
  key_fingerprint=C8EC952E2A0E1FBDC5090F6A2C277A0A352154E5
  curl -fsSL --retry 3 "https://keyserver.ubuntu.com/pks/lookup?op=get&search=0x${key_fingerprint}" \
    | gpg --dearmor -o /usr/share/keyrings/ubuntu-toolchain-r-test.gpg
  . /etc/os-release
  echo "deb [signed-by=/usr/share/keyrings/ubuntu-toolchain-r-test.gpg] https://ppa.launchpadcontent.net/ubuntu-toolchain-r/test/ubuntu ${VERSION_CODENAME} main" \
    > /etc/apt/sources.list.d/ubuntu-toolchain-r-test.list
  sh "$here/apt-mirror" apt-get update -qq
else
  gcc_version=12
fi
sh "$here/apt-mirror" apt-get install -y --no-install-recommends "gcc-${gcc_version}" "g++-${gcc_version}"
ln -s "/usr/bin/gcc-${gcc_version}" /usr/local/bin/gcc
ln -s "/usr/bin/g++-${gcc_version}" /usr/local/bin/g++
