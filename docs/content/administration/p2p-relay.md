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

Use the `localai/relay` image. It holds one small program and nothing else.

```bash
docker run -d --name localai-relay --restart unless-stopped --network host \
  -v localai-relay:/data \
  localai/relay
docker logs localai-relay
```

You can also run the same relay from the `local-ai` binary. It takes the same
options:

```bash
local-ai p2p relay
```

Use `--network host` on Linux. Docker's port proxy can replace the source address
of a client, and that breaks the limits for each address. If you cannot use host
networking (Docker Desktop, rootless Docker), publish the port instead and set
the public address:

```bash
docker run -d --name localai-relay --restart unless-stopped \
  -p 4001:4001/tcp -p 4001:4001/udp \
  -v localai-relay:/data \
  localai/relay --public-ip <public IPv4 address>
```

The `relay` folder of the source tree has a Dockerfile, a Compose file, a systemd
unit and a cloud-init file. The image is not published yet: build it with
`docker build -f relay/Dockerfile -t localai/relay .`.

### Ports and the firewall

The relay uses one port number, 4001 by default, for TCP and for UDP (QUIC).
Allow both in the firewall and in the security group of your cloud provider.
Nothing else is needed. Use `--port` to change the number.

### The public address

The relay must tell clients which address to use. These ways work, from the most
reliable:

1. `--public-ip <address>` or `--announce <multiaddr>`. Always right. You need
   one of them on AWS, Google Cloud, Oracle Cloud and other providers with 1:1
   NAT, where the network card has only a private address.
2. A public IPv4 address on a network card. The relay finds it by itself. Most
   small virtual machine providers work this way.
3. If the relay finds no public address, it prints a warning and the private
   addresses. Clients outside your network cannot use it.

The relay does not ask an outside service for its address. That would be a call
to a third party, and it would tell that party that your relay exists.

On start the relay prints its peer ID, its addresses and the next step. The
addresses are in this order: public first, loopback last. Without an admin, it
also prints a claim code:

```text
LocalAI relay 1  mode: UNCLAIMED  admins: 0  paired peers: 0  data: /data
limits: 1 GiB / 30m0s per circuit, 128 circuits (16 per peer), 128 reservations (8 per IP), no bandwidth limit
PEERID 12D3KooW...
RELAY_ADDR /ip4/203.0.113.10/tcp/4001/p2p/12D3KooW...
RELAY_ADDR /ip4/203.0.113.10/udp/4001/quic-v1/p2p/12D3KooW...
Firewall: allow inbound 4001/tcp and 4001/udp (or the port you set), also in the security group of your cloud.
CLAIM_CODE A972-F9B0-048A
This relay has no admin yet. The first server that sends the code becomes its admin.
On your LocalAI server run:
  local-ai p2p relays add /ip4/203.0.113.10/tcp/4001/p2p/12D3KooW... --claim A972-F9B0-048A
```

The banner never shows the key of the relay. For scripts, `--json` prints the
same facts as one JSON line.

## Connect your server to the relay

A relay needs an **admin**: a LocalAI server that it trusts. There are two ways.

**Claim the relay.** If you start the relay with no admin, it prints a one-time
claim code on its console. In LocalAI, open the **P2P** settings page, then
**Relays**, and choose **Run a relay**. Paste the relay address and choose
**Check reachability**. The check finds that the relay has no admin and asks
for the claim code. The first server that presents the code becomes the admin. The code stops working after
that. This way you do not copy the peer ID of the server to the relay host.

**Name the admin.** Give the peer ID of your server when you start the relay:

```bash
docker run -d --name localai-relay --network host -v localai-relay:/data \
  localai/relay --admin <peer ID of your server>
```

You can repeat `--admin`, or set `LOCALAI_RELAY_ADMINS=id1,id2`. Find the peer ID
with `local-ai p2p id`, or on the **P2P** page.

The **Run a relay** dialog shows these commands with the peer ID of your server
filled in. Paste the address the relay prints, choose **Check reachability**, and
then **Add relay**. The check shows three steps: a reserved slot, a circuit test
through the relay, and the allowlist push. The dialog stores nothing until you
add the relay. When the address names another peer ID than the relay has (for
example after the relay restarted without its key volume), the dialog shows both
IDs.

Or add the relay with:

```bash
local-ai p2p relays add <relay address> --label <name>
```

The command talks to the running server. It uses `--server` (default
`http://127.0.0.1:8080`) and an admin API key from `--api-key` or
`LOCALAI_API_KEY`. The other commands are `local-ai p2p relays list`, `check`,
`sync`, `claim` and `remove`.

Press **Check** on the **Relays** page, or run `local-ai p2p relays check`. The check connects to the relay, makes a
reservation, reads the status and compares the lists. It reports one of these
states:

| State | Meaning |
|---|---|
| In sync | The relay knows this server as admin and has the same paired devices, and the backend nodes that fit. |
| Out of sync | The lists differ. Choose **Sync now**. |
| Open | The relay is open. See below. |
| Unclaimed | The relay has no admin. Claim it with the code. |
| Not admin | The server is a member, not an admin. Another server added this relay. |
| Denied | The relay does not know this server. Check `--admin`. |
| Unreachable | No connection. Check the address and the firewall (port 4001, TCP and UDP). |

The server keeps the relay up to date. It sends the full list when it connects,
and a change each time you pair or revoke a device. It compares the lists every
minute.

## Backend nodes that serve split workers

In a cluster, a backend node that registered with the registration token is not
a paired device. When it is a split server with
[model-split workers]({{% relref "/administration/p2p-model-split" %}}#relays-for-the-nodes)
attached, the server adds the P2P host of the node to its list on the relay,
after the paired devices, so that the workers can reach the node through the
relay. The server removes it when the node is deleted, stops being a split
server, loses its last worker or registers with a registration token that is no
longer valid.

The paired devices always come first. When the list does not fit in
`--max-peers-per-admin`, nodes are left out and the **Model-split workers**
group on the **Nodes** page says **Relay: refused** with the reason. Raise the
option where the relay runs to make room. The relay tells its admins this
limit in the `max_peers` field of its answers.

## Admins and shared relays

Each admin has its own list of devices and can change only its own list. Several
servers can share one relay. A device of one server cannot reach a device of
another server through the relay.

If you remove an admin from `--admin` and restart, the relay forgets its list.
To forget every admin and start unclaimed, use `--reset-admins` once.

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
| `--max-circuit-bytes` | 8 GiB | Ceiling for the data limit that an admin may set from LocalAI. `0` means no ceiling. |
| `--max-circuit-duration` | 24h | Ceiling for the time limit that an admin may set from LocalAI. `0` means no ceiling. |
| `--max-rate-limit` | none | Ceiling for the speed limit that an admin may set from LocalAI, in bytes per second. `0` means no ceiling. |
| `--max-circuits` | 128 | Connections through the relay at the same time. |
| `--max-circuits-per-peer` | 16 | Connections for each machine. |
| `--max-reservations` | 128 | Machines that wait for a connection. |
| `--max-reservations-per-ip` | 8 | The same, for each IP address. |
| `--max-conns-per-ip` | 32 | Connections for each IP address. |
| `--conn-burst-per-ip` | 128 | New connections that one IP address may open at once. |
| `--conn-rate-per-ip` | 2 | New connections for each second that one IP address may open after the burst. `0` means no limit. |
| `--conn-burst-total` | 1000 | New connections that all addresses together may open at once. |
| `--conn-rate-total` | 200 | The same for each second, for all addresses together. `0` means no limit. |
| `--max-peers-per-admin` | 1024 | Devices in the list of one admin. |
| `--rate-limit` | none (open relay: 64 KiB/s) | Speed limit for each connection and direction, in bytes per second. `0` removes the limit and prints a warning for an open relay. |
| `--total-rate-limit` | none | Speed limit for the whole relay, in bytes per second. |
| `--pending-ttl` | 10m | How long a device that came in with a pass stays a member until its server confirms it. |

When a connection reaches its data or time limit, the relay closes it. LocalAI
connects again by itself.

### Change the limits from LocalAI

An admin server can change the data, time and speed limits of a relay without
starting it again. On the **Relays** page, the limits button of a relay opens a
form with the three values. **No limit** removes all three and shows a warning,
because then one paired device can use all the bandwidth of the relay. The same
change is `PUT /api/p2p/relays/{id}/limits`, and the Assistant has the tools
`get_p2p_relay_limits` and `set_p2p_relay_limits`.

- A new speed limit applies to the next connection. Connections that are open
  keep their old speed.
- Only an operator of the relay can change the limits: a server named with
  `--admin`, or, when you named none, the server that claimed the relay. The
  limits are shared by every admin of a shared relay.
- A second change of the data or time limit within 30 seconds of the last
  one is refused, because each one restarts the circuit service. If the
  circuit service cannot start with the new values, the relay starts it again
  with the old ones and the change is not saved.
- A new data or time limit makes the relay restart its circuit service. The
  reservations are dropped, connections that are open keep their old limits
  until they close, and the relay closes the connections of the other machines
  so that they reserve again. Paired devices connect again through the relay
  within a short time.
- The relay saves the values in `limits.json` in its data directory. At the next
  start, these values win over the options above. Delete the file to use the
  options again.
- Any admin of a shared relay can change these limits, and they apply to
  everyone on the relay. For this reason, you set a ceiling (see below).
- Only a gated relay that lists the server as admin accepts the change. For an
  open relay, or a relay where the server is only a member, the form shows the
  limits and you set them with the options above where the relay runs.

### Set a ceiling for the limits

You, the operator of the relay, set the highest value that an admin may choose
for each of the three limits, with `--max-circuit-bytes`,
`--max-circuit-duration` and `--max-rate-limit`. The relay refuses a change
above the ceiling with an error that names the ceiling, for example
`the circuit data limit of 20 GiB is above the ceiling of 8 GiB set by the relay operator`.
It never lowers a value to the ceiling by itself. A ceiling also forbids
removing the limit, because "no limit" is above every ceiling. The limits
dialog shows the maximum next to each field, checks the values before it
sends them, and shows the error of the relay if the relay refuses them.
The ceiling is part of the answer of `GET /api/p2p/relays/{id}/limits`
(`ceiling`), and of `get_p2p_relay_limits`. In both, `0` means no ceiling.

- The defaults are 8 GiB of data and 24 hours for each connection. They are
  above the default limits (1 GiB and 30 minutes), so admins can raise them
  without a change of the options. The default speed ceiling is none, because a
  gated relay has no speed limit by default.
- An admin cannot change the ceiling. It is an option of the relay, and you
  change it where the relay runs.
- The limit options (`--circuit-max-bytes`, `--circuit-max-duration` and
  `--rate-limit`) must be inside the ceiling. If not, the relay does not
  start and the error names the limit. If you use a ceiling, set each limit
  that has one to a value that is not `0`.
- If `limits.json` from an earlier run is above a new ceiling, the relay
  uses the ceiling for that value when it starts and logs a warning. The file
  stays as it is until an admin changes a limit.
- Set a ceiling to `0` to give admins full control, as before.

The other options in the table are read when the relay starts. The connections per
machine are one of them.

Many devices can share one home or mobile address. Raise `--max-conns-per-ip` and
`--max-reservations-per-ip` if your users are behind one address.

### Connection rate

The relay also limits how fast new connections arrive, before any device has
shown that it is a member. A stranger can open a connection, so this limit
protects the relay from a flood. It has two parts: one for each IP address (an
IPv6 `/56`) and one for all addresses together. Each part is a burst that is
available at once and a rate that refills it. The default burst for one address
is large (128) because the devices of a home, an office or a mobile network
share one address and often connect together, for example when they redeem
invites. Connections from the same machine (loopback) are never limited.

When the limit refuses a connection, the other side sees `failed to negotiate
security protocol: EOF` and the relay logs `rate limit exceeded`. Raise
`--conn-burst-per-ip` if many devices share one address. Keep the total rate:
it is the limit for a flood from many addresses. These are start options. They
are not part of the limits that a server can change.

## Other options

| Option | Environment variable | Meaning |
|---|---|---|
| `--admin <peer ID>` | `LOCALAI_RELAY_ADMINS` | An admin server. Repeat it, or separate IDs with commas. |
| `--data-dir` | `LOCALAI_RELAY_DATA` | Folder for the key and the lists. Default: `localai-relay` in the user config folder, `/data` in the image. |
| `--port` | `LOCALAI_RELAY_PORT` | Port for TCP and QUIC. Default 4001. |
| `--listen <multiaddr>` | `LOCALAI_RELAY_LISTEN` | Exact listen address. Replaces `--port`. |
| `--public-ip <address>` | `LOCALAI_RELAY_PUBLIC_IP` | Public IPv4 address to announce. |
| `--announce <multiaddr>` | `LOCALAI_RELAY_ANNOUNCE` | Exact addresses to announce. |
| `--open` | `LOCALAI_RELAY_OPEN` | Open mode. See below. |
| `--allow-private-addrs` | | Hand out private addresses too. For a relay on a local network. It turns on by itself when the relay has no public address. |
| `--reset-admins` | | Forget the admins and their lists, and start unclaimed. |
| `--json` | | Print the banner as one JSON line. |

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
the speed limit with `--rate-limit`, or remove it with `--rate-limit 0`. The banner then
prints a warning, because one connection can use all the bandwidth of the host.

## Keys and backups

The relay keeps two files in its data directory (`/data` in the image):

- `relay.key`, the identity key of the relay.
- `allowlist.json`, the lists of the admins.
- `limits.json`, the limits an admin set from LocalAI. It exists only after a change.

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
