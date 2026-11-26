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

| Case | What it checks |
|---|---|
| `unpaired_refused` | A peer nobody paired is refused at the connection by the relay and cannot reach a paired server. |
| `paired_relayed_50mib` | Paired peers move 50 MiB through the relay with raised circuit limits (1 GiB, 30 min); streams, SSE, websocket and half-close work over the relay. |
| `revoke_live_circuit` | Removing a peer from the relay closes its live circuit during a transfer, and it cannot reconnect. |
| `relay_restart` | After a relay restart the allowlist is loaded from disk, the server is reachable through the relay again within 70 s (60 s with a 30 s relay backoff) and the pair connects. |
| `non_admin_refused` | A paired non-admin gets "not an admin" for the admin protocol; a stranger cannot open it. |
| `open_mode` | An open relay shows `mode: OPEN` and a warning, and serves a stranger. |
| `first_reservation` | A server with a static relay has a relay address at once (under 10 s). |
| `two_admins` | Two admins on one relay see only their own members; a peer cannot reach the other admin's server; removing a peer that is not yours changes nothing. |
| `circuit_limit` | A circuit that reaches its byte limit ends the transfer. |
| `bandwidth_cap` | The relay holds a circuit to its bandwidth cap (1 MiB/s here). |
| `relay_pass` | A one-time relay pass lets a stranger reach the server; a second use of the pass and a stranger without one are refused. |
| `hole_punch` | Two peers behind cone NATs connect through the relay, then reach each other directly (hole punch). |
| `no_punch_symmetric` | Behind symmetric NATs the hole punch fails, the path stays on the relay and the dialer reads the relay's limits. |
| `revoke_server` | A revoke by the server cuts the live stream, the relay removes the peer, and the peer cannot reconnect. |
| `limits_live` | An admin changes the circuit limits through the admin protocol; a member can read but not change them; the next circuit reports the new limits and a transfer above them is cut. |
| `invite_redeem` | A device with only an invite link redeems it through the relay pass; the link works once. |
| `invite_without_pass` | A link minted without a relay pass does not get a stranger through a gated relay. |
| `invite_public_hint` | Behind cone NATs, an invite whose only route is a public relay (an open relay with 128 KiB per circuit, named as a hint `h=`) pairs over a hole-punched path; 50 MiB then arrive directly, and no stream uses the relay. |
| `invite_public_hint_symmetric` | Behind symmetric NATs the same invite fails with "needs a relay" before the code is sent; the invite stays pending, and the same link works after the NATs change to cone. |

## The commands of the lab binary

The harness runs these commands of `../labbin` (built from the P2P packages) in
the peer containers. The relay is not among them: the relay container runs the
real `localai-relay` with `--data-dir /data`, `--log-level debug` and the flags
of the case, and the harness reads its banner (`PEERID`, `RELAY_ADDR`, `mode:`,
`paired peers:`) and its log lines (`deny connection`, `deny circuit`,
`relay: revoked`, `relay: pass used`).

| Command | Meaning |
|---|---|
| `keygen --key F` | Create an identity file if missing, print the peer ID. |
| `serve --key F [--allow ID]... [--relay ADDR]... [--public-relay ADDR]... [--bootstrap ADDR]... [--listen ADDR]... [--relay-sync] [--relay-backoff D] [--revoke-after D [--revoke-relay-only]] [--invite [--invite-pass=false]] [--pair-dir DIR]` | A server behind a NAT. Prints `PEERID`, `FIRST_RELAY_ADDR <duration> <addr>`, `REVOKED`, `REDEEMED`, `INVITE_WARNINGS <list>` and `INVITE <link>`, and an `INVITE_STATUS <id> <status>` line for each invite when it gets `SIGUSR1`. It serves a small HTTP service (hello, download, SSE, websocket, a half-close protocol) and `POST /api/p2p/redeem`. `--relay-sync` runs the real relay manager: it adds the first `--relay`, pushes the `--allow` set as admin and keeps it in sync. `--public-relay` holds a reservation on a relay that the manager does not own, as AutoRelay does with a public relay. `--invite` mints a link through the real pairing service: the manager's relay is its carrier (with a relay pass unless `--invite-pass=false`), the `--public-relay` relays are its hints. `--pair-dir` keeps the invites and devices on disk, so a restarted server still knows a pending invite. |
| `dial PEER --key F [--hint ADDR]... [--relay-hint ADDR]... [--relay ADDR --pass CODE] [--mb N] [--quick] [--warmup D] [--wait-direct D] [--timeout D]` | Connect and run checks. Prints `RESULT connect=ok\|fail`, `PATH <kind>`, `PATH_FINAL` and `TEST` lines. A limited relayed path shows the limits that the relay reported. `--relay-hint` names a public relay of the peer, like the `h` key of a link: it is used only to set up a hole punch and never carries a stream. |
| `redeem --link L --key F` | Redeem an invite link with the real pairing client, then connect to the server. Prints `REDEEM ok\|fail`. |
| `relay-admin --key F --relay ADDR status\|list\|add ID\|remove ID\|pass --code C --ttl D\|limits [--bytes N --seconds N --rate N]\|check [--expect ID]` | The admin protocol client. `check` runs the manager's relay check and prints `CHECK` lines and a `STATE` line. |
| `observer [--listen ADDR]...` | A public peer that admits every connection. Peers connect to four of them to learn their public address. |

Every command takes `--key`, `--listen` and `--bootstrap` where it starts a host.
The peers listen on port 4001 so that a cone NAT keeps the port the relay sees,
which a hole punch needs. A host can punch only after it knows its public
address, so the hole-punch case connects the observers and the dialer waits 20
s before it dials.

## Things to know when you write a case

- The relay limits how fast one IP address may open connections, and every
  peer of a LAN shares one address at the relay. The default burst is large
  enough for a loop that starts a `dial` process each second. A case that
  tests the limit itself starts the relay with `--conn-burst-per-ip` and
  `--conn-rate-per-ip`; a refused dial fails with `failed to negotiate
  security protocol: EOF`.
- A case that needs other NAT types calls `set_nats <natA> <natB>`; the next
  case starts with `LAB_NATS` again.
- A hole punch needs `observers` (four public peers) and a `--warmup` on the
  dialer, and `dial --wait-direct` to wait for the direct path.
- A redeem or dial through a hint alone waits up to two hole-punch windows of
  30 s before it gives up, so give it a `--timeout` of 90 s or more.
- The pairing, pass and limit calls run in the `serve` process on purpose: the
  real server is the admin of its relay, and a limit change keeps that admin's
  own reservation alive only because the server re-reserves itself.
