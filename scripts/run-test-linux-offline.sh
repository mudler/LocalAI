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
chain="LAI_TEST_$$"

sudo mkdir "$cgroup"
cleanup() {
  echo $$ | sudo tee "$parent_cgroup/cgroup.procs" >/dev/null 2>&1 || true
  sudo iptables -D OUTPUT -m cgroup --path "$group" -j "$chain" 2>/dev/null || true
  sudo iptables -F "$chain" 2>/dev/null || true
  sudo iptables -X "$chain" 2>/dev/null || true
  sudo ip6tables -D OUTPUT -m cgroup --path "$group" -j "$chain" 2>/dev/null || true
  sudo ip6tables -F "$chain" 2>/dev/null || true
  sudo ip6tables -X "$chain" 2>/dev/null || true
  sudo rmdir "$cgroup" 2>/dev/null || true
}
trap cleanup EXIT INT TERM

sudo iptables -N "$chain"
sudo iptables -A "$chain" -d 127.0.0.0/8 -j ACCEPT
sudo iptables -A "$chain" -d 10.0.0.0/8 -j ACCEPT
sudo iptables -A "$chain" -d 172.16.0.0/12 -j ACCEPT
sudo iptables -A "$chain" -d 192.168.0.0/16 -j ACCEPT
sudo iptables -A "$chain" -j REJECT
sudo iptables -I OUTPUT 1 -m cgroup --path "$group" -j "$chain"

sudo ip6tables -N "$chain"
sudo ip6tables -A "$chain" -d ::1/128 -j ACCEPT
sudo ip6tables -A "$chain" -j REJECT
sudo ip6tables -I OUTPUT 1 -m cgroup --path "$group" -j "$chain"
echo $$ | sudo tee "$cgroup/cgroup.procs" >/dev/null

set +e
LOCALAI_TEST_KERNEL_ACTIVE=1 "$root/scripts/run-test-offline.sh" "$@"
command_status=$?
set -e

# The final rule in each private chain is the external-egress REJECT. Reading
# its counter makes even an optional probe that swallows its network error a
# hard, visible test failure. Report aggregate counters only: packet-by-packet
# kernel logging can itself exhaust CI logs when a client retries aggressively.
ipv4_blocked=$(sudo iptables -L "$chain" -v -x -n | awk '$3 == "REJECT" { print $1; exit }')
ipv6_blocked=$(sudo ip6tables -L "$chain" -v -x -n | awk '$3 == "REJECT" { print $1; exit }')
ipv4_blocked=${ipv4_blocked:-0}
ipv6_blocked=${ipv6_blocked:-0}

if (( ipv4_blocked > 0 || ipv6_blocked > 0 )); then
  {
    echo 'offline-test: external network access was attempted and blocked'
    echo "offline-test: blocked packets: ipv4=$ipv4_blocked ipv6=$ipv6_blocked"
    echo 'offline-test: replace real network clients with fixtures or injected test doubles'
  } >&2
  exit 1
fi

exit "$command_status"
