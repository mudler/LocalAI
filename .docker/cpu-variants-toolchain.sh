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
  ca-certificates git cmake make ccache binutils software-properties-common

if [ "${TARGETARCH}" = "arm64" ]; then
  gcc_version=14
  add-apt-repository -y ppa:ubuntu-toolchain-r/test
  sh "$here/apt-mirror" apt-get update -qq
else
  gcc_version=12
fi
sh "$here/apt-mirror" apt-get install -y --no-install-recommends "gcc-${gcc_version}" "g++-${gcc_version}"
ln -s "/usr/bin/gcc-${gcc_version}" /usr/local/bin/gcc
ln -s "/usr/bin/g++-${gcc_version}" /usr/local/bin/g++
