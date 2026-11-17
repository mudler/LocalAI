+++
title = "Run your own relay"
toc = true
description = "Run a gated relay that lets your paired devices connect when direct connections fail"
tags = ["P2P", "Administration", "Relay"]
categories = ["Features"]
weight = 146
+++

A **relay** is a small server on the public internet. It passes the traffic of
two machines that cannot connect directly. Use one when both machines are
behind routers that block direct connections. See [P2P]({{% relref "/administration/p2p" %}}).

The project does not run public relays, so you run your own. A relay needs no
models and no web UI. A small virtual machine is enough.

LocalAI relays follow the libp2p
[circuit relay v2](https://github.com/libp2p/specs/blob/master/relay/circuit-v2.md)
protocol. The traffic is encrypted between the two machines. The relay cannot read
it. It sees the peer IDs, the addresses and the number of bytes.

A LocalAI relay is **gated**. It serves only the machines that your LocalAI
server has paired. A machine that is not on the list is dropped as soon as it
connects.

## Start a relay

Use the `localai/relay` image. The image name and the command are not final.

```bash
docker run -d --name localai-relay --network host \
  -v localai-relay:/data \
  localai/relay
```

Or use the command in the main binary:

```bash
local-ai p2p relay
```

Use `--network host` on Linux. Docker's port proxy can replace the source address
of a client, and that breaks the limits for each address. If you cannot use host
networking (Docker Desktop, rootless Docker), publish the port instead and set
the public address:

```bash
docker run -d --name localai-relay \
  -p 4001:4001/tcp -p 4001:4001/udp \
  -v localai-relay:/data \
  localai/relay --public-ip <public IPv4 address>
```

### Ports and the firewall

The relay uses one port number, 4001 by default, for TCP and for UDP (QUIC).
Allow both in the firewall and in the security group of your cloud provider.
Nothing else is needed. Use `--port` to change the number.

### The public address

The relay must tell clients which address to use. It finds a public IPv4 address
on the machine by itself. On some cloud providers the network card has only a
private address, because the provider maps a public address to it. In that case
set `--public-ip <address>`. To announce exact addresses, use `--announce`.

On start the relay prints its peer ID and its addresses:

```text
LocalAI relay  mode: GATED  admins: 1  paired peers: 0
PEERID 12D3KooW...
RELAY_ADDR /ip4/203.0.113.10/tcp/4001/p2p/12D3KooW...
RELAY_ADDR /ip4/203.0.113.10/udp/4001/quic-v1/p2p/12D3KooW...
```

The text above is an example. The exact output is not final.

## Connect your server to the relay

A relay needs an **admin**: a LocalAI server that it trusts. There are two ways.

**Claim the relay.** If you start the relay with no admin, it prints a one-time
claim code on its console. In LocalAI, open **Swarm**, **P2P**, **Relays**, and
choose **Add relay**. Paste the relay address and the claim code. The first
server that presents the code becomes the admin. The code stops working after
that. This way you do not copy the peer ID of the server to the relay host.

**Name the admin.** Give the peer ID of your server when you start the relay:

```bash
docker run -d --name localai-relay --network host -v localai-relay:/data \
  localai/relay --admin <peer ID of your server>
```

You can repeat `--admin`, or set `LOCALAI_RELAY_ADMINS=id1,id2`. Find the peer ID
with `local-ai p2p id`, or on the **P2P** page.

Then add the relay on the server, in the interface or with:

```bash
local-ai p2p relays add <relay address> --label <name>
```

Press **Check** on the **Relays** page. The check connects to the relay, makes a
reservation, reads the status and compares the lists. It reports one of these
states:

| State | Meaning |
|---|---|
| In sync | The relay knows this server as admin and has the same paired devices. |
| Out of sync | The lists differ. Choose **Push now**. |
| Open | The relay is open. See below. |
| Unclaimed | The relay has no admin. Claim it with the code. |
| Not admin | The server is a member, not an admin. Another server added this relay. |
| Denied | The relay does not know this server. Check `--admin`. |
| Unreachable | No connection. Check the address and the firewall (port 4001, TCP and UDP). |

The server keeps the relay up to date. It sends the full list when it connects,
and a change each time you pair or revoke a device. It compares the lists every
minute.

## Admins and shared relays

Each admin has its own list of devices and can change only its own list. Several
servers can share one relay. A device of one server cannot reach a device of
another server through the relay.

If you remove an admin from `--admin` and restart, the relay forgets its list.

## Invites and the relay

A device invite can include your relay. Then a new device joins through the relay
even if no direct connection works. When you create the invite, the server
registers a one-time **pass** on the relay. The pass is valid for the time of the
invite. The new device shows it to the relay, and the relay lets this one device
connect. After the device redeems the invite, it stays on the list until you
revoke it.

If the server cannot reach the relay when you create the invite, the invite has
no pass. The interface tells you.

## Limits

| Option | Default | Meaning |
|---|---|---|
| `--circuit-max-bytes` | 1 GiB | Data for each connection and direction. `0` means no limit. |
| `--circuit-max-duration` | 30m | Time for each connection. `0` means no limit. |
| `--max-circuits` | 128 | Connections through the relay at the same time. |
| `--max-circuits-per-peer` | 16 | Connections for each machine. |
| `--max-reservations` | 128 | Machines that wait for a connection. |
| `--max-reservations-per-ip` | 8 | The same, for each IP address. |
| `--max-conns-per-ip` | 32 | Connections for each IP address. |
| `--max-peers-per-admin` | 1024 | Devices in the list of one admin. |
| `--rate-limit` | none | Speed limit for each connection, in bytes per second. Not final. |
| `--total-rate-limit` | none | Speed limit for the whole relay. Not final. |

When a connection reaches its data or time limit, the relay closes it. LocalAI
connects again by itself.

Many devices can share one home or mobile address. Raise `--max-conns-per-ip` and
`--max-reservations-per-ip` if your users are behind one address.

## Open mode

```bash
local-ai p2p relay --open
```

An **open relay** admits any machine. Use it only if you accept that strangers can
use your bandwidth. The relay prints a warning at start and every ten minutes,
and it reports the mode `open`. An invite can include an open relay. The device
shows a warning before it connects.

An open relay has a default speed limit of 64 KiB per second for each
connection. The data and time limits stay at the defaults above. You can raise
the speed limit with `--rate-limit`, or remove it with `--rate-limit 0`. This
option is not final.

## Keys and backups

The relay keeps two files in its data directory (`/data` in the image):

- `relay.key`, the identity key of the relay.
- `allowlist.json`, the lists of the admins.

Both files can be read by the relay user only. Keep the volume. If you lose
`relay.key`, the relay gets a new peer ID, and every server and every invite that
uses the old address must be updated. Back the volume up and do not share it. The
key is a secret, but `allowlist.json` holds only peer IDs and labels, and the
relay rebuilds it from its admins.

## Security notes

- Keep the relay software up to date.
- Use a gated relay. Use open mode only for a test, with the default speed limit.
- The relay logs peer IDs, addresses, decisions (allowed or denied) and the
  number of bytes for each connection. It never logs content.
- A relay sees which paired machines talk to each other, and when. If this is a
  problem, run the relay yourself on a machine that you trust.
