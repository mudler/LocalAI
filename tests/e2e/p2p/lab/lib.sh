#!/usr/bin/env bash
# Helpers for the lab cases. Sourced by run.sh; not meant to be run alone.
# shellcheck disable=SC2034  # variables set here are used by the cases

P=${LAB_PREFIX:-p2plab}
X=/usr/local/bin/p2plab
RELAY_BIN=/usr/local/bin/localai-relay
LST="--listen /ip4/0.0.0.0/tcp/4001 --listen /ip4/0.0.0.0/udp/4001/quic-v1"
# The node flags: a peer listens on the port that the relay sees, which is
# what a hole punch through a cone NAT needs. The lab hosts use no DHT and no
# port mapping.
NODE="$LST"
# AutoRelay backoff for static relays (the libp2p default is one hour; the
# relay restart case needs a short one).
SX="--relay-backoff ${LAB_RELAY_BACKOFF:-30s}"

CASE_FAILS=0

say() { echo "== $*"; }

# ex <node> <sh command>: run a command in a node.
ex() { docker exec "${P}-$1" sh -c "$2"; }

# bg <node> <name> <command>: run a command in the background; its pid is in
# /root/<name>.pid, its output in /root/<name>.out and /root/<name>.err.
bg() { docker exec -d "${P}-$1" sh -c "echo \$\$ > /root/$2.pid; exec $3 > /root/$2.out 2> /root/$2.err"; }

# stop <node> <name>: stop a background command started with bg.
stop() { ex "$1" "kill \$(cat /root/$2.pid) 2>/dev/null; true" >/dev/null 2>&1; sleep 1; }

# get <node> <file>: print a file of a node.
get() { ex "$1" "cat /root/$2" 2>/dev/null; }

# waitfor <node> <file> <regex> <seconds>
waitfor() {
  local i
  for i in $(seq 1 $(($4 * 4))); do
    get "$1" "$2" | grep -q -- "$3" && return 0
    sleep 0.25
  done
  return 1
}

# seconds <duration>: a Go duration such as "130ms", "17.09s" or "1m2.5s" in
# seconds. Prints 99999 when it cannot parse the text.
seconds() {
  awk -v v="$1" 'BEGIN {
    if (v ~ /^[0-9.]+ms$/) { sub(/ms$/, "", v); print v / 1000 }
    else if (v ~ /^[0-9.]+s$/) { sub(/s$/, "", v); print v + 0 }
    else if (v ~ /^[0-9]+m[0-9.]+s$/) { split(v, p, "m"); sub(/s$/, "", p[2]); print p[1] * 60 + p[2] }
    else print 99999 }'
}

# num_lt <a> <b>: true when a < b (floating point).
num_lt() { awk -v a="$1" -v b="$2" 'BEGIN{exit !(a < b)}'; }

# ---- assertions -----------------------------------------------------------

pass() { echo "  ok   $*"; }
fail() { echo "  FAIL $*"; CASE_FAILS=$((CASE_FAILS + 1)); }

# expect_match <description> <regex> <text>
expect_match() {
  if grep -Eq -- "$2" <<<"$3"; then pass "$1"; else fail "$1 (wanted /$2/, got: $(head -c 300 <<<"$3" | tr '\n' ' '))"; fi
}

# expect_nomatch <description> <regex> <text>
expect_nomatch() {
  if grep -Eq -- "$2" <<<"$3"; then fail "$1 (did not want /$2/, got: $(head -c 300 <<<"$3" | tr '\n' ' '))"; else pass "$1"; fi
}

# expect_true <description> <command...>
expect_true() {
  local d=$1
  shift
  if "$@"; then pass "$d"; else fail "$d"; fi
}

# ---- peers and relay --------------------------------------------------------

# Peer IDs, set by run.sh: SRV (node b, relay admin 1), CLI (node a, paired with
# SRV), SRV2 (node a, relay admin 2), CLI2 (node b, paired with SRV2), ROGUE
# (node a, paired with nobody).

# relay_start <relay flags...>: (re)start the relay on the public segment with
# the data in /data (kept across restarts). Sets RID and RELAY.
RELAY_GEN=0
relay_start() {
  RELAY_STARTED_AT=$(date +%s.%N)
  RELAY_GEN=$((RELAY_GEN + 1))
  RELAY_LOG=relay$RELAY_GEN
  bg relay "$RELAY_LOG" "$RELAY_BIN --data-dir /data --log-level debug --log-format text $*"
  if ! waitfor relay "$RELAY_LOG.out" '^RELAY_ADDR' 20; then
    fail "relay did not start: $(get relay "$RELAY_LOG.err" | tail -3)"
    return 1
  fi
  RID=$(get relay "$RELAY_LOG.out" | awk '/^PEERID/{print $2; exit}')
  RELAY=/ip4/11.77.0.10/tcp/4001/p2p/$RID
}

# relay_stop: stop the relay process (its logs stay in the node until the case ends).
relay_stop() { stop relay "$RELAY_LOG"; }

# relay_wipe: forget the relay's identity and allowlist.
relay_wipe() { ex relay "rm -rf /data/*"; }

# relay_log: everything the current relay process has printed.
relay_log() { get relay "$RELAY_LOG.out"; get relay "$RELAY_LOG.err"; }

# serve <node> <name> <allow ids,...> [extra]: a NATed server that is admin of
# the relay and pushes its allowlist. <allow> is a space separated list of IDs.
serve() {
  local c=$1 n=$2 allow=$3 a="" id
  shift 3
  for id in $allow; do a="$a --allow $id"; done
  bg "$c" "$n" "$X serve --key /root/$n.key $NODE $BOOT --relay $RELAY$a --relay-sync $SX $*"
  waitfor "$c" "$n.out" FIRST_RELAY_ADDR 60 || fail "$n: no relay address after 60 s"
}

# serve_plain <node> <name> [extra]: a NATed server that admits every peer, for
# the cases of an open relay.
serve_plain() {
  local c=$1 n=$2
  shift 2
  bg "$c" "$n" "$X serve --key /root/$n.key $NODE $BOOT --relay $RELAY --admit-all $*"
  waitfor "$c" "$n.out" FIRST_RELAY_ADDR 60 || fail "$n: no relay address after 60 s"
}

# first_relay_seconds <node> <name>: how long the server took to get a relay address.
first_relay_seconds() { seconds "$(get "$1" "$2.out" | awk '/^FIRST_RELAY_ADDR/{print $2; exit}')"; }

circ() { echo "$RELAY/p2p-circuit/p2p/$1"; }

# dial <node> <key name> <target peer> [extra]: connect through the relay only.
dial() {
  local c=$1 k=$2 t=$3
  shift 3
  ex "$c" "$X dial $t --key /root/$k.key $NODE $BOOT --hint $(circ "$t") $*" 2>&1
}

# admin <node> <key name> <op...>: call the relay admin protocol as that identity.
admin() {
  local c=$1 k=$2
  shift 2
  ex "$c" "$X relay-admin --key /root/$k.key --relay $RELAY $*" 2>&1
}

# transfer_bytes <dial output>: bytes moved by the transfer test.
transfer_bytes() { awk '/^TEST transfer/{split($3, a, "/"); print a[1]; exit}' <<<"$1"; }

# transfer_rate <dial output>: MB/s of the transfer test.
transfer_rate() { awk '/^TEST transfer/{for (i = 1; i <= NF; i++) if ($i == "=") {print $(i + 1); exit}}' <<<"$1"; }

# set_nats <natA> <natB>: change the NAT type of the two routers (cone or sym).
set_nats() {
  "$HERE/lab.sh" nat a "$1"
  "$HERE/lab.sh" nat b "$2"
}

# observers: start the four observer peers and set BOOT to the flags that make
# a node connect to them. A node needs four distinct observers before libp2p
# trusts the public address they report, and a hole punch needs that address.
observers() {
  local i id
  BOOT=""
  for i in 1 2 3 4; do
    bg "obs$i" observer "$X observer $LST"
    waitfor "obs$i" observer.out '^PEERID' 10 || fail "observer $i did not start"
    id=$(get "obs$i" observer.out | awk '/^PEERID/{print $2; exit}')
    # Two over TCP and two over QUIC, so both kinds of address get observed.
    if [ "$i" -le 2 ]; then
      BOOT="$BOOT --bootstrap /ip4/11.77.0.1$i/tcp/4001/p2p/$id"
    else
      BOOT="$BOOT --bootstrap /ip4/11.77.0.1$i/udp/4001/quic-v1/p2p/$id"
    fi
  done
}

# relay_start_open <relay flags...>: start an open relay without the default
# bandwidth limit of an open relay. The cases that are about that limit start
# the relay themselves.
relay_start_open() { relay_start --open --rate-limit 0 "$@"; }
