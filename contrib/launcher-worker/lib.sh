#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
run_logged() {
  local output=$1; shift
  local status=0
  (set -o pipefail; "$@" 2>&1 | tee "$output.log") || status=$?
  printf '%s\n' "$status" > "$output.exit" || return 1
  return "$status"
}
prepare_tmp() {
  mkdir -p "$1"
  chmod 1777 "$1"
}
require_space() {
  local available
  available=$(df -PB1 "$1" | awk 'NR==2 {print $4}')
  if [[ ! $available =~ ^[0-9]+$ ]] || (( available < 10737418240 )); then
    echo 'Verification requires at least 10 GiB free in its work directory' >&2
    return 1
  fi
}
