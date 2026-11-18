#!/usr/bin/env bash
# NAT lab topology on Docker: two peers, each behind its own NAT router, and a
# "public" segment with a relay and four observer peers.
#
#   lab.sh preflight           check that this host can run the lab
#   lab.sh up <natA> <natB>    build the image if needed, create the topology
#   lab.sh nat <a|b> <nat>     change the NAT type of one router while the lab is up
#   lab.sh down                remove every container, network and volume of the lab
#   lab.sh rmi                 remove the lab image
#   lab.sh x <node> <cmd...>   run a command in a node
#
# nat is "cone" (MASQUERADE, endpoint-independent mapping, so hole punching can
# work) or "sym" (MASQUERADE --random-fully: a new source port per destination,
# so hole punching cannot work and the relay carries the traffic).
#
# Segments. The public one uses 11.77.0.0/24, a range that is not RFC 1918, so
# libp2p treats it as public (it needs four distinct observers before it trusts
# an observed address, hence four observer peers). All networks are internal:
# nothing reaches the real internet.
#
#   lana 10.77.1.0/24  peer "a" 10.77.1.10, router "ra" 10.77.1.2 / 11.77.0.2
#   lanb 10.77.2.0/24  peer "b" 10.77.2.10, router "rb" 10.77.2.2 / 11.77.0.3
#   pub  11.77.0.0/24  relay 11.77.0.10 (volume at /data), observers obs1..obs4 11.77.0.11..14
#
# Environment: LAB_BIN (the lab binary) and LAB_RELAY_BIN (the localai-relay
# binary), both required for up; LAB_PREFIX (name prefix, default p2plab),
# LAB_IMAGE, LAB_IPTABLES (iptables or iptables-legacy).
set -euo pipefail

HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
P=${LAB_PREFIX:-p2plab}
IMG=${LAB_IMAGE:-p2plab-lab:local}
IPT=${LAB_IPTABLES:-iptables}
BIN_IN_LAB=/usr/local/bin/p2plab
RELAY_BIN_IN_LAB=/usr/local/bin/localai-relay

down() {
  local ids
  ids=$(docker ps -aq --filter "name=^/${P}-" || true)
  # shellcheck disable=SC2086
  if [ -n "$ids" ]; then
    # Kill first: removing a running container waits for a graceful stop.
    docker kill $ids >/dev/null 2>&1 || true
    docker rm -f -v $ids >/dev/null 2>&1 || true
  fi
  local n
  for n in pub lana lanb; do docker network rm "${P}-$n" >/dev/null 2>&1 || true; done
  docker volume rm -f "${P}-relaydata" >/dev/null 2>&1 || true
}

build_image() {
  docker build -q -t "$IMG" "$HERE" >/dev/null
}

# run <name> <net> <ip> [docker run args...]: start a node that sleeps; work is
# started later with docker exec.
run() {
  local name=$1 net=$2 ip=$3
  shift 3
  docker run -d --name "${P}-$name" --network "${P}-$net" --ip "$ip" --cap-add NET_ADMIN \
    -v "$LAB_BIN:$BIN_IN_LAB:ro" -v "$LAB_RELAY_BIN:$RELAY_BIN_IN_LAB:ro" "$@" "$IMG" sleep infinity >/dev/null
}

# nat_rules <name> <public ip> <cone|sym>: set the NAT type of a router. The
# firewall rules stay; only the source NAT rule is replaced and the tracked
# connections are dropped, so the new type applies to the next connection.
nat_rules() {
  local name=$1 pubip=$2 mode=$3 pubif rnd=""
  pubif=$(docker exec "${P}-$name" sh -c "ip -o -4 addr | awk '/$pubip/{print \$2}'")
  [ "$mode" = sym ] && rnd="--random-fully"
  docker exec "${P}-$name" sh -c "
    $IPT -t nat -F POSTROUTING
    $IPT -t nat -A POSTROUTING -o $pubif -j MASQUERADE $rnd
    conntrack -F >/dev/null 2>&1 || true"
}

# router <name> <lan> <lan ip> <public ip> <cone|sym>
router() {
  local name=$1 lan=$2 lanip=$3 pubip=$4 mode=$5
  run "$name" "$lan" "$lanip" --sysctl net.ipv4.ip_forward=1
  docker network connect --ip "$pubip" "${P}-pub" "${P}-$name"
  local pubif
  pubif=$(docker exec "${P}-$name" sh -c "ip -o -4 addr | awk '/$pubip/{print \$2}'")
  nat_rules "$name" "$pubip" "$mode"
  docker exec "${P}-$name" sh -c "
    $IPT -P FORWARD DROP
    $IPT -A FORWARD -i $pubif -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
    $IPT -A FORWARD ! -i $pubif -j ACCEPT
    $IPT -A INPUT -i $pubif -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
    $IPT -A INPUT -i $pubif -j DROP"
}

preflight() {
  command -v docker >/dev/null || { echo "preflight: docker not found" >&2; return 1; }
  docker info >/dev/null 2>&1 || { echo "preflight: the docker daemon is not reachable" >&2; return 1; }
  build_image
  # NET_ADMIN, a nat table and conntrack must work inside a container.
  if ! docker run --rm --cap-add NET_ADMIN "$IMG" sh -c "
      $IPT -t nat -A POSTROUTING -j MASQUERADE --random-fully &&
      $IPT -A FORWARD -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT"; then
    echo "preflight: NAT rules do not work in a container here (iptables backend: $IPT)." >&2
    echo "preflight: try LAB_IPTABLES=iptables-legacy, and check the nf_nat and xt_conntrack kernel modules." >&2
    return 1
  fi
  echo "preflight: ok (docker $(docker version -f '{{.Server.Version}}'), $IPT)"
}

up() {
  : "${LAB_BIN:?LAB_BIN must name the lab binary}" "${LAB_RELAY_BIN:?LAB_RELAY_BIN must name the localai-relay binary}"
  local b
  for b in "$LAB_BIN" "$LAB_RELAY_BIN"; do
    [ -x "$b" ] || { echo "lab: $b is not an executable file" >&2; return 1; }
  done
  down
  build_image
  docker network create --internal --subnet 11.77.0.0/24 "${P}-pub" >/dev/null
  docker network create --internal --subnet 10.77.1.0/24 "${P}-lana" >/dev/null
  docker network create --internal --subnet 10.77.2.0/24 "${P}-lanb" >/dev/null
  # Container starts are slow on a busy host, so start the nodes together.
  router ra lana 10.77.1.2 11.77.0.2 "$1" &
  router rb lanb 10.77.2.2 11.77.0.3 "$2" &
  run a lana 10.77.1.10 &
  run b lanb 10.77.2.10 &
  run relay pub 11.77.0.10 -v "${P}-relaydata:/data" &
  local i
  for i in 1 2 3 4; do run "obs$i" pub "11.77.0.1$i" & done
  wait
  docker exec "${P}-a" ip route replace default via 10.77.1.2
  docker exec "${P}-b" ip route replace default via 10.77.2.2
}

case "${1:-}" in
  preflight) preflight ;;
  up) up "${2:-cone}" "${3:-cone}" ;;
  down) down ;;
  rmi) docker rmi "$IMG" >/dev/null 2>&1 || true ;;
  nat) case "$2" in a) nat_rules ra 11.77.0.2 "$3" ;; b) nat_rules rb 11.77.0.3 "$3" ;; *) echo "lab: nat <a|b> <cone|sym>" >&2; exit 2 ;; esac ;;
  x) c=$2; shift 2; docker exec "${P}-$c" "$@" ;;
  *) echo "usage: $0 preflight | up <cone|sym> <cone|sym> | down | nat <a|b> <cone|sym> | rmi | x <node> <cmd...>" >&2; exit 2 ;;
esac
