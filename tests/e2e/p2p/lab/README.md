# The NAT lab

`lab.sh` builds the topology, `run.sh` runs the cases, `lib.sh` has the helpers
and `cases/` has one file per group of cases. How to run it and read its logs:
[`../README.md`](../README.md).

## Topology

```
 lana 10.77.1.0/24        pub 11.77.0.0/24 (not RFC 1918, so libp2p sees it as public)        lanb 10.77.2.0/24
  peer a 10.77.1.10 -- router ra (NAT) --+-- relay 11.77.0.10 (volume at /data)           +-- router rb (NAT) -- peer b 10.77.2.10
                                         +-- observers obs1..obs4 11.77.0.11-14 --------+
```

- Each router masquerades its LAN with iptables. `cone` keeps the source port
  (endpoint-independent mapping), `sym` uses `MASQUERADE --random-fully`, a new
  port per destination. Hole punching cannot work through `sym`, so the cases
  run on `sym sym` and the relay has to carry the traffic.
- All networks are Docker `--internal` networks: nothing reaches the internet.
- The four observer peers let a peer learn its public address (libp2p needs
  four distinct observers). The relay cases do not use them; hole-punch cases
  on `cone` NATs will.
- Every node runs the same small image (`Dockerfile`: Alpine, iptables,
  conntrack) with the lab binary mounted at `/usr/local/bin/p2plab`.

Peers and their keys, created per run: `SRV` (peer b, admin 1 of the relay),
`CLI` (peer a, paired with SRV), `SRV2` (peer a, admin 2), `CLI2` (peer b,
paired with SRV2), `ROGUE` (peer a, paired with nobody).

## Cases

| Case | What it checks | Needs |
|---|---|---|
| `unpaired_refused` | A peer nobody paired is refused at the connection by the relay and cannot reach a paired server. | |
| `paired_relayed_50mib` | Paired peers move 50 MiB through the relay with raised circuit limits (1 GiB, 30 min); streams, SSE, websocket and half-close work over the relay. | |
| `revoke_live_circuit` | Revoking a peer closes its live circuit during a transfer, and it cannot reconnect. | |
| `relay_restart` | After a relay restart the allowlist is loaded from disk, the server holds a reservation again within 70 s and the pair connects. | |
| `non_admin_refused` | A paired non-admin gets "not an admin" for the admin protocol; a stranger cannot open it. | |
| `open_mode` | An open relay shows `mode: OPEN` and a warning, and serves a stranger. | |
| `first_reservation` | A server with a static relay has a relay address at once (under 10 s). | |
| `two_admins` | Two admins on one relay see only their own members; a peer cannot reach the other admin's server; removing a peer that is not yours changes nothing. | |
| `circuit_limit` | A circuit that reaches its byte limit ends the transfer. | |
| `relay_pass` | A one-time relay pass lets a stranger reach the server, a second use of the pass and a stranger without one are refused. | `relay-pass` |
| `bandwidth_cap` | The relay holds a circuit to its bandwidth cap (1 MiB/s here). | `relay-rate-limit` |

A case with a missing capability is reported as skipped, not failed.

## The contract of the lab binary

The harness is generic over the binary. The binary (`../labbin`, built from the
P2P packages) must answer these commands. Flags are the ones the cases use.

| Command | Meaning |
|---|---|
| `keygen --key F` | Create an identity file if missing, print the peer ID. |
| `relay --data DIR [--admin ID]... [--open] [--circuit-max-bytes N] [--circuit-max-duration D] [--rate-limit BYTES_PER_S]` | Run the gated relay. Prints `PEERID <id>`, `RELAY_ADDR <multiaddr>` and a banner with `mode: GATED\|OPEN` and `paired peers: N`. Logs `DENY connection ... (not paired)`, `DENY circuit ... (no common admin)`, `allow reserve <peer>`, `revoked <peer>: closed N connection(s)`. `--rate-limit 0` removes the cap. |
| `serve --key F [--allow ID]... --relay ADDR --only-static-relays --relay-sync --relay-backoff D --revoke-after D --revoke-relay-only` plus the listen flags | A NATed server. Prints `FIRST_RELAY_ADDR <duration> <addr>` and serves a small HTTP test service. `--relay-sync` pushes the `--allow` set to the relay as its admin. |
| `dial PEER --key F --hint ADDR [--mb N] [--quick] [--pass CODE] [--timeout D]` | Connect and run checks. Prints `RESULT connect=ok\|fail ...`, `PATH ...`, `PATH_FINAL ...`, `TEST ...` lines. |
| `relay-admin --key F --relay ADDR status\|list\|add ID\|remove ID\|check [--target ID] [--expect ID] [--no-reserve]\|pass --code C --ttl D` | The admin protocol client. `check` prints `CHECK ...` lines and a `STATE` line. |
| `caps` | Print the capabilities, space separated: `relay-rate-limit`, `relay-pass`. `placeholder` means the lab has nothing to run yet. A binary without `caps` has none of them. |

The first lab binary was a prototype with this command line; its results are
the numbers the cases assert (for example 50 MiB at 58 to 82 MB/s through the
relay, a first relay address after 0.13 s, a reservation 59.5 s after a relay
restart with a 30 s AutoRelay backoff).
