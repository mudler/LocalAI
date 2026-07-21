#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
set -euo pipefail

if [[ $# -lt 1 ]]; then
  echo "usage: $0 COMMAND [ARG...]" >&2
  exit 2
fi

# A closed loopback proxy fails accidental HTTP(S) immediately while keeping
# existing loopback fixtures and isolated container networks reachable.
export HTTP_PROXY="http://127.0.0.1:1"
export HTTPS_PROXY="$HTTP_PROXY"
export ALL_PROXY="$HTTP_PROXY"
export http_proxy="$HTTP_PROXY"
export https_proxy="$HTTP_PROXY"
export all_proxy="$HTTP_PROXY"
export NO_PROXY="localhost,127.0.0.0/8,::1,172.16.0.0/12,192.168.0.0/16"
export no_proxy="$NO_PROXY"
export TESTCONTAINERS_RYUK_DISABLED=true

exec "$@"
