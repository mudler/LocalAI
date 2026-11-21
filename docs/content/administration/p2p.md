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
- **Compute nodes over P2P.** A worker of a distributed cluster joins with a
  worker invite (`local-ai worker --join <link>`) and needs no inbound port and
  no registration token. See [Workers over P2P]({{% relref "/administration/distributed-mode#workers-over-p2p" %}}).
- **Federation.** One model name served by this server and other LocalAI
  servers, with least busy or round robin routing and failover. See
  [Federation]({{% relref "/administration/federation" %}}).
- **Model-split workers.** llama.cpp and MLX workers that hold part of a large
  model join with an invite. See [Model-split workers over P2P]({{% relref "/administration/p2p-model-split" %}}).
- **A view of each connection.** You see whether a device is connected directly,
  through a hole punched in the router, or through a relay.

Only paired machines can connect. LocalAI drops every other machine as soon as
it connects.

## Turn on P2P

P2P has three modes. Set the mode on the **P2P** settings page, with the runtime
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

1. Open **Administration**, then **This machine**, **P2P** (on a cluster, or
   when P2P is already on: **Swarm**, **P2P**), and choose **Settings**.
2. Under **Network**, choose **Connect only** or **Connect and share**. The
   change applies at once. Without authentication, **Connect and share** is
   shown but cannot be chosen, and the page says what to turn on.

In mode `connect` the page also offers **Switch to Connect and share** when
authentication is on. The settings that matter only for sharing (relay for paired
devices, the **Relays** section) are shown only in mode `share`.

A change of a P2P setting restarts the P2P layer. Paired devices connect again by
themselves.

The first start creates the identity of the server, a key file in the data
path. Keep this file safe. The **Identity** section of the P2P settings page
shows the peer ID with a copy button. **Regenerate** makes a new key and keeps
the old one next to it as a backup. Every paired device, remote and P2P worker
pinned the old peer ID, so each one must pair again with a new invite. The
dialog asks you to confirm this before the button works.

{{% callout style="note" %}}
The old token network is removed. The flags `--p2p`, `--p2p-token` and
`--p2p-network-id`, and the matching environment variables, no longer work.
LocalAI prints a message that names the replacement and exits. The Explorer
is also removed. Federation returns as [federation groups]({{% relref "/administration/federation" %}})
of remotes over P2P. See [Moving from the old P2P network]({{% relref "/administration/p2p-migration" %}}).
{{% /callout %}}

## Pair a device or a remote

Sharing needs mode `share` (**Connect and share**). In another mode the
**Devices and invites** page says which mode is on and links to the settings.

1. Open **Swarm**, then **P2P**, then **Share this instance**.
2. Choose what to share: a **device** (a launcher, a phone or a laptop) or a
   **remote** (another LocalAI server that will use your models). For a remote,
   choose the **Models** it may use: **All models** (the default) or **Choose
   models**, then tick the models. **Make invite** stays off until you tick at
   least one model.
3. Give it a label and choose how long the invite is valid: 10 minutes, 1 hour
   or 1 day. The default is 10 minutes. Each invite works once.
4. Choose **Make invite**. Copy the link, or scan the QR code. QR codes exist for
   devices and remotes only.
5. On the other machine, paste the link in the launcher (**Add server**, then
   **Invite link**), or on another LocalAI in **Models**, **Connect a remote**,
   **Invite link**. See
   [Remotes over P2P]({{% relref "/administration/remotes#remotes-over-p2p" %}}).

The dialog shows what the link carries: the peer ID of your server, the relay
it names, and whether a relay pass was registered on that relay. It also warns
when the invite can pair only over a direct connection:

- **No relay.** The server has no relay. A device behind a strict NAT cannot
  connect. Add a relay, or run one.
- **No relay pass.** The relay did not answer when the invite was made, so no
  pass is registered on it. Make the invite again when the relay is back.
- **Open relay.** The link names a relay that anyone can use. It may limit the
  speed of each connection.

The link is shown once. The list of pending invites does not show it again.

When the device redeems the link, the dialog shows a **confirmation phrase** of
four words, and the device shows the same phrase. Check that both phrases
match. If they differ, someone else used the invite: choose **It does not
match: revoke**, which revokes the device at once.

A **device** invite gives access to the server only. The person still signs in as
before. A **remote** invite also gives the other server an API key that reaches
the models and nothing else. With **All models**, that key may use every model
of this server that a client key may use (a model with tools attached is left
out), also models you add later. With **Choose models**, it may use only the
models you ticked. A model that does not exist, or has tools attached, is
refused when the invite is made. The invite and the **Devices and invites**
page show what was granted: **All models** or the number of models, with the
names in the tooltip. With the API, `all_models: true` in
`POST /api/p2p/invites` grants every model and `models` limits the key to the
models it lists; the two together get `400`. An invite with neither still
grants every model, as before `all_models` existed.

The key of a remote belongs to your user account, so a remote invite needs a
signed-in user account. The API key set with `LOCALAI_API_KEY` (or
`--api-keys`) has no account: an invite of kind `remote` made with it gets
`409` and the dialog says to sign in with a user account. Device invites work
with that key too.

The **Devices and invites** page lists the pending invites, with the time left
and the relay that holds the pass, and the paired devices, with the path of
each one. **Cancel** cancels an invite and its relay pass. **Revoke** revokes a
device: LocalAI closes its connections at once, removes it from your relays,
and for a remote also revokes its API key. The other devices keep working.

### Connect a remote with an invite link

On the server that will use the models, open **Home** (on the first run, choose
**On another server or service**) or the remotes list, choose **Connect a
remote**, then **Invite link**, and paste the link. LocalAI reads the link
before it sends anything and shows the server, the relay pass, the path it will
try and the expiry. **Connect** redeems the invite and saves the remote; then
choose the models to add.

If P2P is off, **Connect** turns it on in mode `connect` (**Connect only**)
first. In that mode the server dials out and accepts nobody, so it needs no
authentication.

If both networks block direct connections and the relay does not answer, the
page says so and lists what helps: run a relay, forward UDP and TCP port 4001, or
try from another network.

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
- A model-split worker that is reachable only through a relay still pairs and
  shows on the Nodes page, but it is slow. LocalAI shows a warning and you
  choose **Use anyway**. Through a relay of yours that limits each
  connection, the worker runs until the relay cuts the connection; you can
  raise or remove the limits. A public relay never carries model layers. See
  [Model-split workers over P2P]({{% relref "/administration/p2p-model-split" %}}#paths-relays-and-use-anyway).

When a relayed connection ends at its limit, LocalAI connects again and tries to
get a direct connection.

## Settings

These settings are on the **P2P** settings page, under **Network**. A switch
applies at once; a list or a number applies with its **Save** button. They are
runtime settings: the names below are the keys of `/api/settings` and of
`runtime_settings.json`.

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
| `POST /api/p2p/invites` | Creates an invite: `{kind, label, ttl_seconds, models, all_models, relays, allow_open_relay}`. `kind` is `device`, `remote`, `worker` or `split`. `models` limits the key of a remote. `all_models: true` grants every model a client key may use, explicitly; it is refused (`400`) together with a non-empty `models`. With neither, the key may use every model a client key may use. A model that does not exist or has tools attached gets `400`. A remote invite made without a user account (the API key of `LOCALAI_API_KEY`) gets `409` with error type `needs_account`. `relays` is `"auto"`, `"none"` or a list of relay IDs. The answer has the link (shown once), whether a relay pass was registered, and warnings such as `no_relay` or `relay_unreachable`. |
| `GET /api/p2p/invites` | The invites and their state: `pending`, `used`, `expired` or `canceled`, and `relay_pass`, the relay that holds the pass. A used invite also has `device_peer_id` and `phrase`, the confirmation phrase the device was shown. A remote invite has `all_models` or `models`, what it grants. |
| `DELETE /api/p2p/invites/{id}` | Cancels a pending invite and its relay pass. |
| `GET /api/p2p/devices` | The devices, with the path of each one: `direct`, `hole_punched` or `relayed`, and the relay limits. A remote has `all_models` or `models`, what its key may use. |
| `PATCH /api/p2p/devices/{id}` | Renames a device: `{label}`. |
| `DELETE /api/p2p/devices/{id}` | Revokes a device. |
| `GET`, `POST /api/p2p/relays` | Lists the relays, or adds one: `{addr, label, claim_code}`. |
| `DELETE /api/p2p/relays/{id}` | Removes a relay. |
| `POST /api/p2p/relays/{id}/check`, `/sync`, `/claim` | Checks a relay, pushes the paired set to it now, or claims it with its code: `{code}`. |
| `GET /api/p2p/split-workers` | Lists the model-split workers with their state and path. See [Model-split workers over P2P]({{% relref "/administration/p2p-model-split" %}}#http-api-and-assistant). |
| `PATCH /api/p2p/split-workers/{id}` | `{allow_slow_path}`: uses a worker over a relay ("Use anyway"), or stops. |
| `GET`, `PUT /api/p2p/relays/{id}/limits` | Reads the limits of a relay (`circuit_bytes`, `circuit_seconds`, `rate_bps`; 0 is no limit) and whether this server may change them, or changes them: a field left out stays. A new data or time limit restarts the circuit service of the relay (`restarted` is `true`). `409` when this server is not an admin of the relay or the relay is open. |
| `POST /api/p2p/identity/regenerate` | Makes a new identity. The body must be `{"confirm": true}`. Every device must pair again. |
| `POST /api/p2p/redeem` | Redeems an invite: `{v: 1, code, name, platform}`. Served **only over P2P**; the normal HTTP listener answers 404. |

The redeem route takes the peer ID of the caller from the P2P connection. A
wrong, used, canceled or expired code all get the same answer, 410, so the route
does not tell which codes exist. After 5 wrong codes from one peer in an hour,
or 60 from all peers in a minute, it answers 429.

While an invite is pending, a machine that is not paired may connect, but it may
call only the redeem route. Every other request gets 403.

The LocalAI Assistant has the tools `get_p2p_status`, `list_p2p_devices`,
`list_p2p_invites`, `list_p2p_relays`, `get_p2p_relay_limits`,
`list_p2p_split_workers`, `revoke_p2p_device`, `cancel_p2p_invite`,
`set_p2p_relay_limits`, `set_p2p_split_slow_path` and `set_p2p_split_node`. It cannot create invites: a link is a credential.

## Several frontends

In [distributed mode]({{% relref "/administration/distributed-mode" %}}) each frontend
has its own identity and its own P2P host. An invite names the frontend that
created it, so the device pairs with that frontend. Devices and invites are in the
shared database, so every frontend lists them. When a frontend pairs or revokes a
device, it tells the others over the message bus, and they reload their
allowlists at once. Every frontend also reloads its allowlist every 30 seconds,
so a revoke reaches every frontend within 30 seconds even if a message is lost.

The relay pass of an invite is kept in the memory of the frontend that made the
invite, until the invite ends. Only that frontend can cancel the pass: when
another frontend cancels the invite, or after a restart, the pass is not
cancelled and ends with its time limit. The same holds for the count of wrong
invite codes, which each frontend keeps for itself, so an attacker gets the
limit once for each frontend.

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
itself. To raise the limits of your own relay, use the limits button on the **Relays** page, or see
[Run your own relay]({{% relref "/administration/p2p-relay" %}}).

**A model-split worker is slow.**
The worker is reachable only through a relay. Move the worker to a network that
allows direct connections, or use a relay with more bandwidth.

**All devices were disconnected after a restore.**
The identity key changed. The devices pin the identity of the server, so they
refuse the new one. Restore the key file, or pair the devices again.
