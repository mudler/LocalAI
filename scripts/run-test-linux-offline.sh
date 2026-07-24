#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
set -euo pipefail

if [[ $(uname -s) != Linux ]]; then
  echo 'kernel-level test egress enforcement is Linux-only' >&2
  exit 2
fi
if [[ $# -lt 2 ]]; then
  echo "usage: $0 TARGET COMMAND [ARG...]" >&2
  exit 2
fi

root=$(cd "$(dirname "$0")/.." && pwd)
group="localai-test-$$"
cgroup="/sys/fs/cgroup/$group"
parent_cgroup="/sys/fs/cgroup$(awk -F: '$1 == "0" {print $3}' /proc/self/cgroup)"

sudo mkdir "$cgroup"
cleanup() {
  echo $$ | sudo tee "$parent_cgroup/cgroup.procs" >/dev/null 2>&1 || true
  sudo iptables -D OUTPUT -m cgroup --path "$group" -j REJECT 2>/dev/null || true
  sudo iptables -D OUTPUT -m cgroup --path "$group" -d 192.168.0.0/16 -j ACCEPT 2>/dev/null || true
  sudo iptables -D OUTPUT -m cgroup --path "$group" -d 172.16.0.0/12 -j ACCEPT 2>/dev/null || true
  sudo iptables -D OUTPUT -m cgroup --path "$group" -d 10.0.0.0/8 -j ACCEPT 2>/dev/null || true
  sudo iptables -D OUTPUT -m cgroup --path "$group" -d 127.0.0.0/8 -j ACCEPT 2>/dev/null || true
  sudo ip6tables -D OUTPUT -m cgroup --path "$group" -d ::1/128 -j ACCEPT 2>/dev/null || true
  sudo ip6tables -D OUTPUT -m cgroup --path "$group" -j REJECT 2>/dev/null || true
  sudo rmdir "$cgroup" 2>/dev/null || true
}
trap cleanup EXIT INT TERM

sudo iptables -I OUTPUT 1 -m cgroup --path "$group" -j REJECT
sudo iptables -I OUTPUT 1 -m cgroup --path "$group" -d 192.168.0.0/16 -j ACCEPT
sudo iptables -I OUTPUT 1 -m cgroup --path "$group" -d 172.16.0.0/12 -j ACCEPT
sudo iptables -I OUTPUT 1 -m cgroup --path "$group" -d 10.0.0.0/8 -j ACCEPT
sudo iptables -I OUTPUT 1 -m cgroup --path "$group" -d 127.0.0.0/8 -j ACCEPT
sudo ip6tables -I OUTPUT 1 -m cgroup --path "$group" -j REJECT
sudo ip6tables -I OUTPUT 1 -m cgroup --path "$group" -d ::1/128 -j ACCEPT
echo $$ | sudo tee "$cgroup/cgroup.procs" >/dev/null

LOCALAI_TEST_KERNEL_ACTIVE=1 "$root/scripts/run-test-offline.sh" "$@"
