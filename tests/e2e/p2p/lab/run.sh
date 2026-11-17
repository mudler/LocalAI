#!/usr/bin/env bash
# Runs the P2P NAT lab cases.
#
#   run.sh [case ...]      run the named cases (default: all)
#   run.sh --list          list the cases
#
# Environment:
#   LAB_BIN     the lab binary to run in the containers (required, linux)
#   LAB_LOGS    where the logs go (default: ./logs next to this script)
#   LAB_CAPS    capabilities of the binary (default: what "<bin> caps" prints;
#               a binary without a "caps" command has just "base")
#   LAB_NATS    the two NAT types, "<a> <b>" with cone or sym (default: sym sym)
#   LAB_KEEP    set to 1 to leave the containers up after the run, for debugging
#   LAB_PREFIX, LAB_IMAGE, LAB_IPTABLES, LAB_RELAY_BACKOFF   see lab.sh and lib.sh
set -u

HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=lib.sh
. "$HERE/lib.sh"

CASES=()
declare -A CASE_DESC CASE_NEEDS
# register <name> <description> [capability]: the case is the function case_<name>.
register() {
  CASES+=("$1")
  CASE_DESC[$1]=$2
  CASE_NEEDS[$1]=${3:-}
}
for f in "$HERE"/cases/*.sh; do
  # shellcheck disable=SC1090
  . "$f"
done

if [ "${1:-}" = "--list" ]; then
  for c in "${CASES[@]}"; do printf '%-22s %s%s\n' "$c" "${CASE_DESC[$c]}" "${CASE_NEEDS[$c]:+ (needs ${CASE_NEEDS[$c]})}"; done
  exit 0
fi

: "${LAB_BIN:?LAB_BIN must name the lab binary (a static linux binary)}"
LAB_LOGS=${LAB_LOGS:-$HERE/logs}
LAB_NATS=${LAB_NATS:-sym sym}
mkdir -p "$LAB_LOGS"

# Capabilities: a binary says what it can do with "caps".
if [ -z "${LAB_CAPS:-}" ]; then
  "$HERE/lab.sh" preflight || exit 1
  LAB_CAPS=$(docker run --rm -v "$LAB_BIN:/usr/local/bin/p2plab:ro" "${LAB_IMAGE:-p2plab-lab:local}" p2plab caps 2>/dev/null) || LAB_CAPS=base
fi
if grep -qw placeholder <<<"$LAB_CAPS"; then
  echo "P2P lab: the lab binary is still the placeholder (tests/e2e/p2p/labbin), so there is nothing to run. Skipping."
  exit 0
fi
"$HERE/lab.sh" preflight || exit 1
echo "P2P lab: binary $LAB_BIN, capabilities: $LAB_CAPS, NATs: $LAB_NATS"

wanted=("$@")
if [ ${#wanted[@]} -eq 0 ]; then wanted=("${CASES[@]}"); fi
for w in "${wanted[@]}"; do
  [[ -v CASE_DESC[$w] ]] || { echo "unknown case: $w (see --list)" >&2; exit 2; }
done

cleanup() {
  if [ "${LAB_KEEP:-0}" = 1 ]; then
    echo "LAB_KEEP=1: the lab is still up; remove it with $HERE/lab.sh down"
  else
    "$HERE/lab.sh" down
  fi
}
trap cleanup EXIT

say "lab up ($LAB_NATS)"
# shellcheck disable=SC2086
"$HERE/lab.sh" up $LAB_NATS || exit 1

# Identities, one key file per name, kept for the whole run.
keygen() { ex "$1" "$X keygen --key /root/$2.key"; }
SRV=$(keygen b SRV)
CLI2=$(keygen b CLI2)
CLI=$(keygen a CLI)
SRV2=$(keygen a SRV2)
ROGUE=$(keygen a ROGUE)
echo "SRV=$SRV CLI=$CLI SRV2=$SRV2 CLI2=$CLI2 ROGUE=$ROGUE" >"$LAB_LOGS/ids.log"
for id in "$SRV" "$CLI2" "$CLI" "$SRV2" "$ROGUE"; do
  [ -n "$id" ] || { echo "lab: keygen failed; is $LAB_BIN the lab binary?" >&2; exit 1; }
done

# reset_nodes: stop every background process and clear the logs of the last case.
reset_nodes() {
  local n
  for n in a b relay; do
    # shellcheck disable=SC2016  # the loop runs in the node
    ex $n 'for f in /root/*.pid; do [ -f "$f" ] && kill "$(cat "$f")" 2>/dev/null; done; sleep 1; rm -f /root/*.pid /root/*.out /root/*.err' >/dev/null 2>&1 || true
  done
  relay_wipe
}

# collect_logs: copy the output of every background process to the case logs.
collect_logs() {
  local n
  for n in a b relay; do
    docker exec "${P}-$n" sh -c 'cd /root && tar cf - *.out *.err 2>/dev/null' | tar xf - -C "$CASE_LOGS" 2>/dev/null || true
    for f in "$CASE_LOGS"/*.out "$CASE_LOGS"/*.err; do
      [ -f "$f" ] && mv "$f" "$CASE_LOGS/$n-$(basename "$f").log"
    done
  done
}

PASSED=() FAILED=() SKIPPED=()
for c in "${wanted[@]}"; do
  say "case $c: ${CASE_DESC[$c]}"
  if [ -n "${CASE_NEEDS[$c]}" ] && ! need "${CASE_NEEDS[$c]}"; then
    echo "  skip (the lab binary lacks the capability ${CASE_NEEDS[$c]})"
    SKIPPED+=("$c")
    continue
  fi
  CASE_LOGS=$LAB_LOGS/$c
  rm -rf "$CASE_LOGS"
  mkdir -p "$CASE_LOGS"
  reset_nodes
  "case_$c" 2>&1 | tee "$CASE_LOGS/case.log"
  # tee hides the function's variables (subshell); recount from the log.
  CASE_FAILS=$(grep -c '^  FAIL ' "$CASE_LOGS/case.log" || true)
  collect_logs
  if [ "$CASE_FAILS" -eq 0 ]; then PASSED+=("$c"); else FAILED+=("$c"); fi
done

say "summary"
echo "passed:  ${PASSED[*]:-none}"
echo "skipped: ${SKIPPED[*]:-none}"
echo "failed:  ${FAILED[*]:-none}"
echo "logs: $LAB_LOGS"
[ ${#FAILED[@]} -eq 0 ]
