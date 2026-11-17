+++
title = "P2P"
toc = true
description = "Connect LocalAI servers, devices and workers with invite links, without opening ports"
tags = ["P2P", "Administration", "Remotes"]
categories = ["Features"]
weight = 145
+++

LocalAI P2P connects your own machines. A phone, a laptop, another LocalAI
server or a worker joins with one **invite link**. You do not open a port on
your router, and you can revoke each machine alone.

P2P gives you:

- **Paired devices.** A device pairs once and can then reach this server.
- **Remotes over P2P.** You use the models of another LocalAI server as models
  of this one. See [Remotes]({{% relref "/administration/remotes" %}}).
- **Model-split workers.** Workers that hold part of a large model join with an
  invite. See [MLX distributed]({{% relref "/administration/mlx-distributed" %}}).
- **A view of each connection.** You see whether a device is connected directly,
  through a hole punched in the router, or through a relay.

Only paired machines can connect. LocalAI drops every other machine as soon as
it connects.

## Turn on P2P

P2P has three modes. Set the mode in **Settings**, **P2P**, with the runtime
setting `p2p_mode`, or with `--p2p-mode` (environment variable
`LOCALAI_P2P_MODE`) at start:

| Mode | In the interface | What the server does |
|---|---|---|
| `off` | Off | Nothing. This is the default. |
| `connect` | Connect only | It connects out to remotes over P2P and accepts nobody. It has no listener, no invites and no devices. It needs no authentication. |
| `share` | Connect and share | It also accepts paired devices: it listens, mints invites, and has the devices and relays pages. |

Mode `share` needs [authentication]({{% relref "/administration/authentication" %}}).
A paired device may reach the API, and without sign-in LocalAI cannot tell the
users apart. LocalAI refuses mode `share` while authentication is off, at start
and in the settings.

P2P also needs a data path, because the server keeps its identity key there. It
does not start when you run LocalAI with `--data-path -`, unless you name the key
file with `--p2p-identity`.

1. Open **Settings**, **P2P**, and choose **Connect and share**.
2. Open **Swarm**, then **P2P**. This page is shown whenever P2P is on, also on
   a single server.

A change of a P2P setting restarts the P2P layer. Paired devices connect again by
themselves.

The first start creates the identity of the server, a key file in the data
path. Keep this file safe. If you regenerate it, every paired device must pair
again.

{{% callout style="note" %}}
The old token network is removed. The flags `--p2p`, `--p2p-token` and
`--p2p-network-id`, and the matching environment variables, no longer work.
LocalAI prints a message that names the replacement and exits. The Explorer
is also removed. Federation returns as remotes over P2P with a router.
{{% /callout %}}

## Pair a device or a remote

1. Open **Swarm**, then **P2P**, then **Share**.
2. Choose what to share: a **device** (a phone or a laptop) or a **remote** (a
   LocalAI server that will use your models).
3. Give it a name and choose how long the invite is valid. The default is 10
   minutes. The maximum is 24 hours.
4. Copy the link, or scan the QR code. QR codes exist for devices and remotes
   only.
5. On the other machine, paste the link in the launcher, or, for a remote, in
   **Models**, **Connect a remote**. See
   [Remotes over P2P]({{% relref "/administration/remotes#remotes-over-p2p" %}}).

The link works once. It holds the identity of your server, so the other machine
knows which server it is talking to.

After the link is redeemed, both machines show a **confirmation phrase** of four
words. Check that both phrases match. If they differ, the link was changed on the
way. Cancel the pairing.

A **device** invite gives access to the server only. The person still signs in as
before. A **remote** invite also gives the other server a key that reaches only
the models you chose.

To revoke a machine, open **Swarm**, then **P2P**, then **Devices**, and choose
**Revoke**. LocalAI closes its connections at once. It also removes it from your
relays.

## How machines connect

LocalAI tries three paths in this order:

1. **Direct.** Both machines are on one network, or one of them is reachable from
   the internet.
2. **Hole punching.** Both machines use a relay for a short time to agree on a
   direct connection through their routers. The relay can be a public libp2p
   relay. A public relay only helps to set up the connection. It never carries
   your data.
3. **Relayed.** If the routers block direct connections, the traffic goes through
   a relay that you run. See [Run your own relay]({{% relref "/administration/p2p-relay" %}}).

The **P2P** page shows the path of each device:

| Path | Meaning |
|---|---|
| Direct | A direct connection. No limits. |
| Hole-punched | A direct connection that was set up through a relay. No limits. |
| Relayed | The traffic goes through a relay. See below. |

The project does not run relays. About 3 in 10 pairs of networks need one.

### What "Limited" means

A relay can limit a connection. It may stop a connection after some data or
some time, and it may limit its speed. A connection like this shows **Limited**,
together with its limits. LocalAI reads these limits from the relay and decides
for each request:

- Pairing and small requests use any relay, also a public one.
- Large transfers need a direct connection, or a relay with a large enough limit.
- A model-split worker that is reachable only through a relay still works, but it
  is slow. LocalAI shows a warning and you choose **Use anyway**. LocalAI never
  refuses the worker.

When a relayed connection ends at its limit, LocalAI connects again and tries to
get a direct connection.

## Settings

These settings are in **Settings**, **P2P**. They are runtime settings: the
names below are the keys of `/api/settings` and of `runtime_settings.json`.

| Setting | Default | Meaning |
|---|---|---|
| `p2p_mode` | `off` | `off`, `connect` or `share`. See [Turn on P2P](#turn-on-p2p). |
| `p2p_public_dht` | on | Finds public relays for hole punching through the public DHT. The DHT uses about 0.5 to 0.9 MB per minute. |
| `p2p_bootstrap_peers` | empty | Your own list of bootstrap peers, addresses that end in `/p2p/<peer ID>`. Empty uses the public bootstrap peers while the public DHT is on. |
| `p2p_relays` | empty | Your own relays: a list of `{id, addr, label}`, where `addr` ends in `/p2p/<relay peer ID>`. Manage them on the **Relays** page. |
| `p2p_relay_paired` | on | "Via your server": this server relays for its paired devices. Mode `share` only. |
| `p2p_relay_mode` | `auto` | When this server relays for its devices: `auto` while it is reachable from the internet, `on` always, `off` never. |
| `p2p_relay_rate_limit` | 0 | Bandwidth limit of each relayed connection through this server, in bytes per second. 0 is no limit. |
| `p2p_mdns` | on | Finds machines on the local network. |

Command-line flags, read at start:

| Flag | Environment variable | Meaning |
|---|---|---|
| `--p2p-mode` | `LOCALAI_P2P_MODE` | The mode at start. The setting `p2p_mode` wins when the flag keeps its default. |
| `--p2p-listen` | `LOCALAI_P2P_LISTEN_MADDRS` | Addresses to listen on in mode `share`. The default is TCP and QUIC on every interface, on random ports. |
| `--p2p-bootstrap` | `LOCALAI_P2P_BOOTSTRAP_PEERS_MADDRS` | Bootstrap peers at start (setting `p2p_bootstrap_peers`). |
| `--p2p-identity` | `LOCALAI_P2P_IDENTITY` | The identity key file. The default is `p2p/identity.key` under the data path. |

## HTTP API

All routes but redeem are for administrators. The routes of invites, devices and
relays need mode `share`. In another mode they answer 409 with a message that
names the mode.

| Route | What it does |
|---|---|
| `GET /api/p2p/status` | Mode, peer ID, addresses, reachability, relay reservations, whether the server relays for its devices, and the number of pending invites and devices. Works in every mode. |
| `POST /api/p2p/invites` | Creates an invite: `{kind, label, ttl_seconds, models, relays, allow_open_relay}`. `kind` is `device`, `remote`, `worker`, `split` or `federated`. `relays` is `"auto"`, `"none"` or a list of relay IDs. The answer has the link (shown once), whether a relay pass was registered, and warnings such as `no_relay` or `relay_unreachable`. |
| `GET /api/p2p/invites` | The invites and their state: `pending`, `used`, `expired` or `canceled`. |
| `DELETE /api/p2p/invites/{id}` | Cancels a pending invite and its relay pass. |
| `GET /api/p2p/devices` | The devices, with the path of each one: `direct`, `hole_punched` or `relayed`, and the relay limits. |
| `PATCH /api/p2p/devices/{id}` | Renames a device: `{label}`. |
| `DELETE /api/p2p/devices/{id}` | Revokes a device. |
| `GET`, `POST /api/p2p/relays` | Lists the relays, or adds one: `{addr, label, claim_code}`. |
| `DELETE /api/p2p/relays/{id}` | Removes a relay. |
| `POST /api/p2p/relays/{id}/check`, `/sync`, `/claim` | Checks a relay, pushes the paired set to it now, or claims it with its code: `{code}`. |
| `POST /api/p2p/identity/regenerate` | Makes a new identity. The body must be `{"confirm": true}`. Every device must pair again. |
| `POST /api/p2p/redeem` | Redeems an invite: `{v: 1, code, name, platform}`. Served **only over P2P**; the normal HTTP listener answers 404. |

The redeem route takes the peer ID of the caller from the P2P connection. A
wrong, used, canceled or expired code all get the same answer, 410, so the route
does not tell which codes exist. After 5 wrong codes from one peer in an hour,
or 60 from all peers in a minute, it answers 429.

While an invite is pending, a machine that is not paired may connect, but it may
call only the redeem route. Every other request gets 403.

The LocalAI Assistant has the tools `get_p2p_status`, `list_p2p_devices`,
`list_p2p_invites`, `list_p2p_relays`, `revoke_p2p_device` and
`cancel_p2p_invite`. It cannot create invites: a link is a credential.

## Several frontends

In [distributed mode]({{% relref "/administration/distributed-mode" %}}) each frontend
has its own identity and its own P2P host. An invite names the frontend that
created it, so the device pairs with that frontend. Devices and invites are in the
shared database, so every frontend lists them. When a frontend pairs or revokes a
device, it tells the others over the message bus, and they reload their
allowlists at once. Every frontend also reloads its allowlist every 30 seconds,
so a revoke reaches every frontend within 30 seconds even if a message is lost.

## Command line

These commands are not final.

```bash
local-ai p2p id        # the peer ID and the addresses of this server
local-ai p2p status    # reachability and the path of each device
local-ai p2p relays list|add|remove|check
local-ai p2p pair --invite <link>
```

## Troubleshooting

**The device cannot connect.**
Open **Swarm**, **P2P** on the server and check that the server shows a peer ID
and that the device is listed. If the invite was used or has expired, create a
new one.

**Both networks block direct connections.**
Some routers (for example mobile networks, or routers that map every connection
to a different port) block hole punching. The device then shows **Relayed**, or
it does not connect at all. Add a relay that you run on a public server. See
[Run your own relay]({{% relref "/administration/p2p-relay" %}}). Then create a
new invite. The invite carries the address of the relay and a one-time pass, so
the new device can join through it.

**The invite says "no relay pass".**
The server could not reach the relay when it created the invite. The device can
still pair if a direct connection works. Check the relay on the **Relays** page,
then create a new invite.

**A relayed connection stops after some time.**
The relay limits the data or the time of each connection. Your own relay has
generous limits. An open relay has a low speed limit. LocalAI connects again by
itself. To raise the limits, see [Run your own relay]({{% relref "/administration/p2p-relay" %}}).

**A model-split worker is slow.**
The worker is reachable only through a relay. Move the worker to a network that
allows direct connections, or use a relay with more bandwidth.

**All devices were disconnected after a restore.**
The identity key changed. The devices pin the identity of the server, so they
refuse the new one. Restore the key file, or pair the devices again.
