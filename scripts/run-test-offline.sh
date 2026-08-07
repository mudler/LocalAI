#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
set -euo pipefail

if [[ $# -lt 2 ]]; then
  echo "usage: $0 TARGET COMMAND [ARG...]" >&2
  exit 2
fi

target=$1
shift
root=$(cd "$(dirname "$0")/.." && pwd)

if [[ ${LOCALAI_TEST_KERNEL_ENFORCE:-0} == 1 && ${LOCALAI_TEST_KERNEL_ACTIVE:-0} != 1 ]]; then
  exec "$root/scripts/run-test-linux-offline.sh" "$target" "$@"
fi

exec go run "$root/cmd/test-resources" run "$target" \
  "$root/test-resources/manifests" "${TEST_RESOURCE_CACHE:-$root/.cache/test-resources}" -- "$@"
