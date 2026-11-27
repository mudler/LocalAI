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
| `connect` | Connect only | It connects out to remotes over P2P and accepts nobody. It listens on random ports only so that a hole punch can reach it, and it drops every machine that connects. It runs no local network discovery and holds no relay reservation. It has no invites and no devices. It needs no authentication. |
| `share` | Connect and share | It also accepts paired devices: it listens, mints invites, and has the devices and relays pages. |

Mode `share` needs [authentication]({{% relref "/administration/authentication" %}}).
A paired device may reach the API, and without sign-in LocalAI cannot tell the
users apart. LocalAI refuses mode `share` while authentication is off, at start
and in the settings.

P2P also needs a data path, because the server keeps its identity key there. It
does not start when you run LocalAI with `--data-path -`, unless you name the key
file with `--p2p-identity`.

1. Open **Administration**, then **This machine**, **P2P** (on a cluster, or
   when P2P is already on: **Cluster**, **Sharing**), and choose **Settings**.
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

1. Open **Cluster**, then **Sharing**, then **Share this instance**.
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

- **No relay.** The server has no relay and knows no public relay for hole
  punching. A device behind a strict NAT cannot connect. Add a relay, or run
  one.
- **Hole punching only.** The server has no relay of its own, but the link names
  public relays for hole punching. Devices can connect by hole punching. About
  3 in 10 networks block it. Add a relay, or run one, so that every device can
  connect.
- **No relay pass.** The relay did not answer when the invite was made, so no
  pass is registered on it. Make the invite again when the relay is back.
- **Open relay.** The link names a relay that anyone can use. It may limit the
  speed of each connection.

When the server has no public address, the dialog also says so, and what that
means for this invite: with public relays in the link, a device outside your
network connects by hole punching; without them, only a device on the same
network can pair until the server finds a public relay or you add one.

The link is shown once. The list of pending invites does not show it again.

**Public relays in the link.** A server behind a NAT holds reservations on
public relays that it finds through the public DHT. The link names up to two
of them (the dialog shows how many under **Hole punching**). A device uses such
a relay only to reach the server and set up a hole punch. A public relay never
carries your data and never gets the relay pass. Once a link names public
relays for a server, the device sends data to that server only directly or
through the relays that its own links name as relays. This holds also for the
other public relays of the server that the device learns while connected. Your own relays from the
settings are never named this way: they go in the link as relays, with the
pass. The link names public relays also with `"relays": "none"`. Pass
`"omit_relay_hints": true` to `POST /api/p2p/invites` to leave them out, for
example for a shorter QR code. With `p2p_public_dht` off the server finds no
public relay, and the link names none. A public relay learns nothing more than
that your server uses it; it does not see the address of your server in the
link.

**Addresses in the link.** The link carries up to four direct addresses of the
server: public ones first, then the private addresses of its local networks
(RFC 1918 IPv4 and unique local IPv6; loopback and link-local addresses are
never used). With them, a device or a remote on the same network redeems the
link with no relay, no internet access and no multicast discovery, also when it
runs in mode `connect`, which does not run discovery. A paired remote keeps the
addresses of its link and uses them to reconnect.

A link with private addresses tells whoever holds it which networks the server
is on. Pass `"omit_lan_addrs": true` to `POST /api/p2p/invites` to leave them
out when the link will go to someone outside that network. Without public
addresses and without a relay, such a link has only the peer ID, and only
a device that finds the server by local network discovery can redeem it.

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

## Users pair their own phones

A user who has the feature **Pair own devices (P2P)** (`p2p_pair`) can pair
their own phone. The phone then signs in as that user, with no password: see
[Device sign-in](#device-sign-in). The feature is off by default. Turn it on
for one user on the **Users** page, or change its default for every user (see
[Feature defaults]({{% relref "/administration/authentication#feature-defaults" %}})).
Administrators always have it.

In the web UI the user opens **Account > My devices** and presses **Pair a
phone**. The tab shows only to a user with the feature while the server is in
mode `share`. The dialog asks for a name and a life, up to the limit, and shows
the QR code and the link. An administrator who pairs their own phone is told
that the phone gets an admin session. The tab lists the user's phones, with the
path and the last-seen time, and the invites that wait; the user renames or
revokes a phone and cancels an invite there.

The administrator finds the matching controls in three places. On **Users**,
the access sheet of a user has the switch **Pair own devices (P2P)** and, under
it, **On by default for users**, which changes the default at once for every
user whose access does not set the feature. In **Sharing > Settings**, the
section **User pairing** edits `p2p_user_pairing`. In **Sharing > Devices and
invites**, the device table has an **Owner** column, a **Signs in** tag on each
device that signs in, and a filter by owner.

The user mints the invite with `POST /api/p2p/me/invites`. The body takes a
`label` and `ttl_seconds` only. The server fixes the rest: the kind is `device`
with sign-in, the invite grants no models, and the relays, open relays and local
addresses come from the setting `p2p_user_pairing`. A user sees and changes only
their own sign-in invites and devices; any other ID gets `404`. This includes
the invites and devices without sign-in that the user minted as an
administrator (workers, remotes, laptops): they belong to the instance, so a
demoted administrator cannot list, rename or revoke them through these routes. A client key cannot use
these routes, and neither can the API key of `LOCALAI_API_KEY`, which has no
account (`409`, error type `needs_account`).

Limits keep the pairing window small, because every pending invite lets unknown
machines connect to the redeem route:

| Limit | Default | Where |
|---|---|---|
| Active devices of one user | 5 | `max_devices`, checked when the user mints and again when the device redeems |
| Pending invites of one user | 2 | `max_pending` |
| Life of an invite | 1 hour | `max_ttl_seconds`; a longer `ttl_seconds` gets `400` |
| Invites one user mints in an hour | 10 | fixed |
| Pending sign-in invites on the server | 20 | fixed |

A mint over a limit gets `429` with error type `limit_reached` and a message
that names the limit.

The administrator edits `p2p_user_pairing` with `GET` and `PUT
/api/p2p/user-pairing`. It is kept in the authentication database, so every
frontend of a cluster uses the same value. A `PUT` changes only the fields it
names:

```bash
curl -X PUT http://localhost:8080/api/p2p/user-pairing \
  -H "Authorization: Bearer <admin-key>" \
  -H "Content-Type: application/json" \
  -d '{"relays": "auto", "allow_open_relay": false, "omit_lan_addrs": false, "max_devices": 5, "max_pending": 2, "max_ttl_seconds": 3600}'
```

`relays` is `auto` (the relays of this server) or `none`. `max_devices` is 1 to
100, `max_pending` 1 to 20 and `max_ttl_seconds` 60 to 86400. A change applies to
the next invite and the next redeem; devices and invites that exist stay.

The device list of the administrator shows the owner of each device
(`owner_id`, `owner_name`) and whether it signs in (`sign_in`).
`GET /api/p2p/devices?owner=<user ID>` lists the devices of one user. A device of
an administrator also shows in that administrator's own list.

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
| `p2p_public_dht` | on | Finds public relays for hole punching through the public DHT. Invite links name up to two of them, so that a device can reach a server behind a NAT. Off, links name none. The DHT uses about 0.5 to 0.9 MB per minute. |
| `p2p_bootstrap_peers` | empty | Your own list of bootstrap peers, addresses that end in `/p2p/<peer ID>`. Empty uses the public bootstrap peers while the public DHT is on. |
| `p2p_relays` | empty | Your own relays: a list of `{id, addr, label}`, where `addr` ends in `/p2p/<relay peer ID>`. Manage them on the **Relays** page. |
| `p2p_relay_paired` | on | "Via your server": this server relays for its paired devices. Mode `share` only. |
| `p2p_relay_mode` | `auto` | When this server relays for its devices: `auto` while it is reachable from the internet, `on` always, `off` never. |
| `p2p_relay_rate_limit` | 0 | Bandwidth limit of each relayed connection through this server, in bytes per second. 0 is no limit. |
| `p2p_mdns` | on | Finds machines on the local network. Mode `connect` never runs it. |

Command-line flags, read at start:

| Flag | Environment variable | Meaning |
|---|---|---|
| `--p2p-mode` | `LOCALAI_P2P_MODE` | The mode at start. The setting `p2p_mode` wins when the flag keeps its default. |
| `--p2p-listen` | `LOCALAI_P2P_LISTEN_MADDRS` | Addresses to listen on in mode `share`. The default is TCP and QUIC on every interface, on random ports. Mode `connect`, `local-ai p2p pair` and the Android app always use the default. |
| `--p2p-bootstrap` | `LOCALAI_P2P_BOOTSTRAP_PEERS_MADDRS` | Bootstrap peers at start (setting `p2p_bootstrap_peers`). |
| `--p2p-identity` | `LOCALAI_P2P_IDENTITY` | The identity key file. The default is `p2p/identity.key` under the data path. |

## HTTP API

The routes are for administrators, except three groups: the `/api/p2p/me`
routes, for a user who has the feature `p2p_pair` (a client key is refused);
and redeem, routes and device session, which are served only over P2P. The
routes of invites, devices and relays need mode `share`. In another mode they answer 409 with a message that
names the mode.

| Route | What it does |
|---|---|
| `GET /api/p2p/status` | Mode, peer ID, addresses, reachability, relay reservations (`reservations`; `hint_relays` lists the ones on public relays, which invites name for hole punching), whether the server relays for its devices, and the number of pending invites and devices. Works in every mode. |
| `POST /api/p2p/invites` | Creates an invite: `{kind, label, ttl_seconds, models, all_models, relays, allow_open_relay, omit_lan_addrs, omit_relay_hints}`. `kind` is `device`, `remote`, `worker` or `split`. `models` limits the key of a remote. `all_models: true` grants every model a client key may use, explicitly; it is refused (`400`) together with a non-empty `models`. With neither, the key may use every model a client key may use. A model that does not exist or has tools attached gets `400`. A remote invite made without a user account (the API key of `LOCALAI_API_KEY`) gets `409` with error type `needs_account`. `relays` is `"auto"`, `"none"` or a list of relay IDs. `omit_lan_addrs: true` keeps the private addresses of this server out of the link (see above). `omit_relay_hints: true` keeps the public relays for hole punching out of the link (see above). The answer has the link (shown once), whether a relay pass was registered, `hints` (how many public relays the link names), and the warnings: `no_relay`, `punch_only`, `relay_unreachable`, `open_relay` and `no_public_addr`. |
| `GET /api/p2p/invites` | The invites and their state: `pending`, `used`, `expired` or `canceled`, and `relay_pass`, the relay that holds the pass. A used invite also has `device_peer_id` and `phrase`, the confirmation phrase the device was shown. A remote invite has `all_models` or `models`, what it grants. |
| `DELETE /api/p2p/invites/{id}` | Cancels a pending invite and its relay pass. |
| `GET /api/p2p/devices` | The devices, with the path of each one: `direct`, `hole_punched` or `relayed`, and the relay limits. A remote has `all_models` or `models`, what its key may use. Each device has `owner_id`, `owner_name` and `sign_in`. `?owner=<user ID>` lists the devices of one user. |
| `PATCH /api/p2p/devices/{id}` | Renames a device: `{label}`. |
| `DELETE /api/p2p/devices/{id}` | Revokes a device. |
| `GET`, `POST /api/p2p/relays` | Lists the relays, or adds one: `{addr, label, claim_code}`. |
| `DELETE /api/p2p/relays/{id}` | Removes a relay. |
| `POST /api/p2p/relays/{id}/check`, `/sync`, `/claim` | Checks a relay, pushes the paired set to it now, or claims it with its code: `{code}`. |
| `GET /api/p2p/split-workers` | Lists the model-split workers with their state and path. See [Model-split workers over P2P]({{% relref "/administration/p2p-model-split" %}}#http-api-and-assistant). |
| `PATCH /api/p2p/split-workers/{id}` | `{allow_slow_path}`: uses a worker over a relay ("Use anyway"), or stops. |
| `GET`, `PUT /api/p2p/relays/{id}/limits` | Reads the limits of a relay (`circuit_bytes`, `circuit_seconds`, `rate_bps`; 0 is no limit) and whether this server may change them, or changes them: a field left out stays. A new data or time limit restarts the circuit service of the relay (`restarted` is `true`). `409` when this server is not an admin of the relay or the relay is open. |
| `GET`, `PUT /api/p2p/user-pairing` | Reads or changes the setting `p2p_user_pairing`: `{relays, allow_open_relay, omit_lan_addrs, max_devices, max_pending, max_ttl_seconds}`. A field left out stays. See [Users pair their own phones](#users-pair-their-own-phones). |
| `POST /api/p2p/identity/regenerate` | Makes a new identity. The body must be `{"confirm": true}`. Every device must pair again. |
| `GET /api/p2p/me` | For a user with `p2p_pair`: whether a device can pair now (`available`; `reason` is `not_share` in another mode), the limits, and the number of own devices and pending invites. |
| `POST /api/p2p/me/invites` | For a user with `p2p_pair`: mints a sign-in invite for an own device: `{label, ttl_seconds}`. Any other field is ignored. The answer is the one of `POST /api/p2p/invites`. |
| `GET /api/p2p/me/invites` | For a user with `p2p_pair`: the own pending invites. |
| `DELETE /api/p2p/me/invites/{id}` | For a user with `p2p_pair`: cancels an own invite and its relay pass. |
| `GET /api/p2p/me/devices` | For a user with `p2p_pair`: the own devices, with path and last-seen time. |
| `PATCH /api/p2p/me/devices/{id}` | For a user with `p2p_pair`: renames an own device: `{label}`. |
| `DELETE /api/p2p/me/devices/{id}` | For a user with `p2p_pair`: revokes an own device and ends its sessions. |
| `POST /api/p2p/redeem` | Redeems an invite: `{v: 1, code, name, platform}`. Served **only over P2P**; the normal HTTP listener answers 404. |
| `GET /api/p2p/routes` | The current routes to this server, for a paired device: `{server, addrs, relays, hints}`. Served **only over P2P** and only to a paired device; the normal HTTP listener answers 404. |
| `POST /api/p2p/device/session` | Signs a paired sign-in device in as its owner: `{credential}` as JSON, or the form field `credential`. Sets the session cookie; a form post gets `303` to `/`, JSON gets `200` with the user. Served **only over P2P** and only to a paired device; the normal HTTP listener answers 404. See [Device sign-in](#device-sign-in). |

The redeem route takes the peer ID of the caller from the P2P connection. A
wrong, used, canceled or expired code all get the same answer, 410, so the route
does not tell which codes exist. After 5 wrong codes from one peer in an hour,
or 60 from all peers in a minute, it answers 429.

While an invite is pending, a machine that is not paired may connect, but it may
call only the redeem route. Every other request gets 403.

The routes route has the lists of an invite link minted now with `"relays":
"auto"`, without the code and the pass: the direct addresses (public ones, then
local network ones), every gated relay of this server, also when the invite of
the device named fewer or none, and up to two public relays for hole punching
(`hints`). The local network addresses are left out when the invite of the
device was created with `"omit_lan_addrs": true`. A paired device asks
for them while it is connected, so it still finds this server after a relay
reservation moves or an address changes. The Android app and the desktop
launcher (through `local-ai p2p forward`) ask when a connection opens and every
10 minutes after, and keep the new lists for this server. A peer that is not
paired gets 403.

### Device sign-in

A device that redeemed a sign-in invite gets a device credential in the redeem
answer (`device_credential`, shown once), with `sign_in: true` and
`owner_name`, the account it signs in as. The server keeps only a keyed hash of
the credential. The device posts the credential to `POST
/api/p2p/device/session` over P2P and gets a normal web session of its owner,
with the rights of the owner and no more. The route checks, in this order:

1. the peer of the connection is an active device that was paired with sign-in;
2. the credential is the credential of that device;
3. the owner is an active user;
4. the owner still has the feature **Pair own devices (P2P)** (`p2p_pair`).
   Administrators always have it.

Every other case gets the same `403`, so the route does not tell which check
failed. Wrong credentials have their own limit, counted for the peer alone:
after 5 in an hour from one peer the route answers `429` to that peer. The
limit does not count by network source, because the P2P connection proves the
peer, and it does not share the count of wrong invite codes, so peers behind
the same relay or NAT cannot lock each other out. The credential works only
from the peer that redeemed the invite, because the route reads the peer ID
that the P2P connection proved. It is not an API key: the normal HTTP listener
does not accept it.

The session is bound to the device. LocalAI accepts it only on a request that
arrives over P2P from the device that opened it, and only while that device is
active and signs in. The same cookie or `Authorization: Bearer` token on the
normal HTTP listener, or over P2P from another paired peer, gets `401`. The app
on the phone always reaches the server through its P2P forwarder, so this does
not limit normal use. Each request with such a session reads the device row by
its ID from the shared database, so a revoke takes effect on every frontend at
once.

A device keeps one live session. When it signs in again, the new session
replaces the earlier ones of that device.

Revoking a device ends the sessions it opened. If the server cannot delete
those sessions, the revoke answers an error; the device is revoked all the
same, and its sessions no longer work. When an administrator disables
or deletes a user, the server revokes the sign-in devices of that user and ends
their sessions. Every frontend also leaves the sign-in devices of a user who is
not active out of its allowlist at its next reload. Devices without sign-in (an
admission-only phone or laptop, a remote, a worker) stay: an administrator pairs
them for the instance, not for the account that made the invite. Taking
`p2p_pair` away from a user keeps the devices of that user, but they can no
longer open a new session.

The in-process relay ("Via your server") does not let a sign-in device, or a
device of a user who is not an administrator, hold a reservation or open a
circuit: such a device never waits for connections, and it talks to the server
directly, so it has no use for a circuit through the server.

A device that was away too long can have routes that are all out of date. Send
it a new invite: the app sees that it already has this server, dials the routes
of the invite and does not redeem the code. It saves new routes only when this
server gives them over that connection. Then cancel that invite.

The LocalAI Assistant has the tools `get_p2p_status`, `list_p2p_devices`,
`list_p2p_invites`, `list_p2p_relays`, `get_p2p_relay_limits`,
`list_p2p_split_workers`, `revoke_p2p_device`, `cancel_p2p_invite`,
`set_p2p_relay_limits`, `set_p2p_split_slow_path`, `set_p2p_split_node`,
`get_p2p_user_pairing` and `set_p2p_user_pairing`. It cannot create invites: a link is a credential.

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
invite codes and of wrong device credentials, which each frontend keeps for
itself (per-instance state), so an attacker gets the limit once for each
frontend.

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
Open **Cluster**, **Sharing** on the server and check that the server shows a peer ID
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
