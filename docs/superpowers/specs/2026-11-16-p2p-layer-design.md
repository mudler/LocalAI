# A lean P2P layer: connections, gated relays, invites and remotes

Date: 2026-11-16. Status: draft for review. Each open question has a
recommendation under [Open questions](#open-questions).

## Intent

LocalAI P2P today runs on a shared-token overlay network (edgevpn). One token
admits every node, a node cannot be revoked alone, the overlay cannot say how
two nodes are connected, and the Explorer, the federation and the worker
commands all sit on it. This note replaces that layer with a small libp2p host
that admits only paired peers, and builds invites, remotes, model-split workers
and federation on top of it.

The result for a user: pair a laptop, a phone, another LocalAI or a worker with
one link, see how it is connected, and revoke it alone. A server behind a home
router stays reachable, and a relay that the user runs is the last resort.

## Non-goals

- A public relay service. The project runs no relays. Public libp2p relays help
  only with hole punching and never carry data.
- Replacing NATS or the WebSocket tunnel of distributed mode. Carrying the
  distributed-mode tunnel lanes over libp2p is a follow-up (see
  [Out of scope](#out-of-scope)).
- Anonymity. A peer ID is a stable identity. A relay learns which peer IDs talk
  to each other, and when. It never sees content.
- A dual stack or a transition period for the old token network.
- Fixing upstream libp2p. Where the library has no hook (a bandwidth limit per
  circuit), this design wraps it.

## Decisions

| Decision | Reason |
|---|---|
| The new layer is a lean libp2p host, `p2pnet`, not edgevpn. | The overlay has one shared token, no per-peer admission and no path reporting. A direct libp2p host gives per-peer admission at the handshake and reports the path. |
| edgevpn, the Explorer, the old federation, the old P2P workers, the old routes and the old UI are removed in the same change set. No dual stack, no legacy page. | Two stacks double the attack surface and the docs. The Explorer lists token networks, which no longer exist. |
| The path is direct, then hole punching coordinated through public relays, then a relay the user runs. | About 3 in 10 pairs of networks cannot connect directly. Public relays are enough to coordinate a hole punch and are limited to 128 KiB per circuit by default, so they cannot carry data. |
| User-run relays are gated by peer ID. | An open relay is an abuse target. The server already knows which peer IDs it paired, so it is the natural source of the allowlist. |
| Limited connections have no static policy. The dialer reads the reported limits and the application decides. | A pairing request needs a few KiB. A model transfer needs gigabytes. One global rule would be wrong for one of them. |
| Invites are links. The link holds the server peer ID, a one-time code and optional relay addresses. | The peer ID in the link pins the server before the first byte is sent. The user needs no extra step. |
| A confirmation phrase is shown on both sides after redeem. | It lets the user notice a link that was swapped on the way. |
| A device invite gives P2P admission only. The person signs in as today. | Admission and identity stay separate. A paired device does not become a user. |
| P2P cannot be enabled while authentication is off. | Without sign-in, a paired device would get the whole API. |
| A new setting, "P2P enabled", switches the layer on. The old flags exit with a message. | Old configs must fail loudly instead of silently doing nothing. |
| Migration is a startup check only. | Token networks cannot be converted. The check lists what is still set and names the replacement. |
| `p2p-worker` becomes `worker`, and `p2p-worker` stays as an alias. | `worker` already exists as the distributed-mode worker. A group with that command as its default child keeps both working. |
| The relay ships as a separate small image and as `local-ai p2p relay`. | A relay host needs no models and no web UI. The command in the main binary serves people who already have it. |

## Architecture

```text
core/application          starts and stops the layer on the "P2P enabled" setting
core/http/endpoints/p2p   admin HTTP API, plus the redeem route served only over P2P
core/services/p2ppair     invites, devices, link codec, phrase, relay sync, stores
core/services/p2pnet      host, identity, admission gate, dial and listen, path, forwarder
core/services/p2prelay    relay server, ACL, allowlist store, rate limit, admin handler
core/services/p2prelay/relayproto   wire types of the relay admin protocol
```

Import rules, enforced by a test:

- `p2pnet` imports only the standard library, libp2p, multiaddr and the logger.
  It knows nothing about HTTP, settings or the database.
- `p2prelay` imports `p2pnet` and `relayproto`.
- `p2ppair` imports `p2pnet` and the stores. It talks to relays with `relayproto`.

### p2pnet

`p2pnet` owns one libp2p host with a persisted ed25519 identity. Its job is to
move bytes between admitted peers and to report how.

- Streams look like `net.Conn`. `Listen(proto, admit)` returns a `net.Listener`
  and `Dial(ctx, peer, proto, opts...)` returns a `net.Conn`. The rest of
  LocalAI uses ordinary HTTP servers, clients and `io.Copy`.
- Admission is a connection gater on the secured, inbound side. A peer that is
  not allowed is dropped right after the handshake. It cannot open any stream.
  Revoking a peer removes it from the allowlist and closes its connections.
- While invites are pending, the gate also admits unknown peers at connection
  level. Application protocols still check, and an unknown peer may call only the
  redeem route.
- `Path(peer)` reports `none`, `direct`, `hole_punched` or `relayed`, with the
  relay, the reported limits and the round-trip time.
- A forwarder listens on a stable local TCP port and dials the peer for every
  connection. It re-dials after a reset, so a client can use a plain
  `http://127.0.0.1:<port>` address.

### p2prelay

A relay is a libp2p circuit relay v2 with an ACL, a connection gater and an
admin protocol. See [Gated relays](#gated-relays).

### p2ppair

`p2ppair` mints and redeems invites, keeps the device list, and pushes the
paired set to relays. Stores follow the pattern of remotes: a database table
when a database is present, a JSON file under the data path otherwise.

### Relations to the rest of LocalAI

- **Remotes** gain a transport. A remote with the `p2p` transport has the address
  `p2p://<peer ID>` and a forwarder on a local port. See
  [Remotes over P2P](#remotes-over-p2p).
- **The launcher** can add a server by invite and keeps a forwarder under its
  supervisor.
- **Model-split workers and federation** use `p2pnet` for their links. See
  [Model-split workers and federation](#model-split-workers-and-federation).
- **Distributed mode carriers** (NATS and the WebSocket tunnel) are unchanged.

## Connection path

1. **Direct.** The dialer tries the peer's known addresses (TCP and QUIC),
   including addresses from the invite and local network discovery (mDNS).
2. **Hole punching.** Both peers connect to a relay and use it to exchange
   addresses and to time a simultaneous connection (DCUtR). The relay can be a
   public libp2p relay found through the public DHT. This works when the NATs
   allow it.
3. **A relay that the user runs.** If hole punching fails, traffic goes through
   a gated relay. The relayed connection is end-to-end encrypted. The relay sees
   only the peer IDs and the byte counts.

Public relays never carry data. Their circuits are limited to 128 KiB, which is
enough for coordination only. The public DHT is on by default. Users can add
bootstrap peers and static relays in the settings.

A relayed connection is **limited**: the relay reports a data limit and a time
limit. The dialer reads them from the connection (go-libp2p exposes
`StatLimitData` and `StatLimitDuration`). Every dial states what the caller
needs, as a number of bytes and a duration. A need of zero means control
traffic that works on any path. If the path is too small, `Dial` returns a
typed error and the caller decides:

| Traffic | Behaviour on a limited relayed path |
|---|---|
| Pairing and small requests | Allowed on any relay, including public ones. |
| Bulk transfers | Needs a direct path or a relay with a large enough limit. |
| Model sharding | Allowed with a warning that the path is slow. The user can choose "Use anyway". It is never refused. |

When a relayed connection ends at its limit, the application re-dials and tries
hole punching again. A relayed path upgrades to direct when the NATs allow it.

Two libp2p behaviours need handling in `p2pnet`, or static relays do not work
(both were reproduced in a NAT lab):

- AutoRelay starts only after a reachability event. With no AutoNAT server
  answering, no event comes. `p2pnet` emits one itself at start when static
  relays are set.
- AutoRelay backs off for one hour after every reservation attempt. A relay that
  restarts would be unused for an hour. `p2pnet` sets the backoff and the minimum
  interval to 30 seconds.

## Gated relays

A gated relay is a plain circuit relay v2 ([spec](https://github.com/libp2p/specs/blob/master/relay/circuit-v2.md),
[go-libp2p](https://github.com/libp2p/go-libp2p)) plus a gate.

**Admins.** An admin is a LocalAI server, named by its peer ID. The operator
passes the IDs with `--admin`, or claims the relay once with a one-time code
that the relay prints on its console. The secure channel authenticates the
admin, so there is no password.

**Per-admin allowlists.** Each admin owns a set of peer IDs. The server pushes
its paired devices to the relay over the admin protocol
`/localai/relay-admin/1`. An admin can read and change only its own set. The
relay stores the sets in a file that only its user can read.

**Four layers.**

1. A connection gater drops any peer that is neither an admin nor a member of
   some set, right after the handshake.
2. The reservation ACL admits only admins and members.
3. The connect ACL admits a circuit only when one admin vouches for both ends,
   after a global circuit cap. A shared relay therefore isolates its tenants.
4. The admin handler lets only admins change their own set. Members can read the
   public status. Strangers never reach it.

**Relay pass.** A new device may be reachable only through the relay and
unknown to it. When the server mints an invite, it registers a one-time **pass**
on the relay, with the invite TTL. The device presents the pass on
`/localai/relay-pass/1`. The relay then admits it as a pending member of that
admin's set. The relay closes an unknown peer that presents no pass after ten
seconds and limits pass attempts per address. When the device redeems the invite,
the server pushes the device to the set and the membership becomes permanent. If
the relay cannot be reached when the invite is minted, the invite has no pass
and the interface says so.

**Revocation.** The server removes the peer from the relay. The relay closes the
peer's connections, which ends its reservation and circuits. The server's own
gate closes any direct connection that started as a relayed one.

**Sync.** The server pushes the full set when it connects to a relay, adds a peer
when it pairs one, removes a peer when it revokes one, and compares a digest with
the relay every 60 seconds. Each write carries a sequence number, so a reconnect
cannot overwrite a later revoke.

**Limits and the open-relay default.** A relay has limits per circuit (data and
time), per peer, per address and in total. Gated relays have no bandwidth limit by
default. An **open relay** (`--open`) admits anyone, so it prints a warning at
start and every ten minutes, and it has a default bandwidth limit of 64 KiB per
second per circuit. The data and time limits per circuit stay at the relay
defaults. The operator can tune or remove each limit. go-libp2p has no hook for a
bandwidth limit, so the relay wraps the host that the relay service uses and
limits both legs of a circuit. A test must prove that both legs pass through the
wrapper (see [Open questions](#open-questions)).

An invite can include an open relay. The invite lists open relays, and the
client shows a warning before it connects.

## Invites and pairing

### Link

```text
localai://pair?v=1&k=<kind>&p=<server peer ID>&c=<code>&e=<expiry>&n=<label>
    [&a=<direct address>]... [&r=<relay address>]... [&o=<open relay index>] [&x=<relay pass>]
```

- `k` is the kind: `device`, `remote`, `worker` or `split`.
- `c` is a one-time code of 10 characters. The server keeps only a keyed hash.
- `e` is the expiry as a Unix time. The default time to live is 10 minutes, and
  the maximum is 24 hours.
- `a` and `r` give up to two direct addresses and three relay addresses.
  A relay address ends with `/p2p/<relay peer ID>`.
- Readers ignore unknown keys and refuse a version other than 1.
- A link has about 110 characters with no address and about 300 with two relays.
  The interface shows a QR code for device and remote invites only.

### Redeem

The client connects to the server by its peer ID, directly or through the relays
in the link, and calls `POST /api/p2p/redeem` over P2P. The route is served only
on the P2P protocol, and the normal HTTP listener answers 404.

- The server checks the code, marks the invite used in one atomic step, adds the
  client to the gate, and creates the device. A used, expired or unknown code
  gives the same error, so the route is not an oracle for valid codes.
- Wrong codes are limited per peer ID and per relay address.
- A `remote` invite also creates a scoped client key, using the existing client
  pairing code, and returns it.

### Confirmation phrase

After a redeem, both sides show a phrase of four words that both derive from the
two peer IDs and the invite ID. The user compares them. A mismatch means the
link was swapped or the wrong server answered, and the user cancels.

### Device scope

A `device` invite gives admission only. The device may open P2P streams to the
server, and the person signs in as today. Revoking a device removes it from the
gate and from every relay, closes its connections, and revokes its client key.
Devices and invites are listed in the interface under Swarm, P2P.

## Remotes over P2P

A remote has a kind and, now, a transport. A `localai` remote with the `p2p`
transport has the address `p2p://<peer ID>`, the direct addresses and relays from
its invite, and a local port. LocalAI lists and tests the remote through an HTTP
client that dials over P2P. The entries that the remote creates use the existing
proxy backend and point at a forwarder on `http://127.0.0.1:<port>`. The key is
the client key from the invite and is stored sealed, like any remote key.

Adding a remote accepts an invite link in place of an address. Revoking the
device on the other server ends the remote.

The server-side forwarder registry starts at boot. In distributed mode, the proxy
backend can load on a worker that has no forwarder, so this needs a decision
(see [Open questions](#open-questions)).

## Model-split workers and federation

**Model-split workers.** `worker llama-cpp-rpc --join <link>` and
`worker mlx-distributed --join <link>` redeem a `split` invite and listen on a
P2P protocol. The server runs a forwarder for each worker on a stable local port
and feeds the existing llama.cpp and MLX callbacks with the online workers, so
the engines are unchanged. When a worker is reachable only through a relay, the
server warns that the path is slow and lets the user decide with "Use anyway". It
never refuses.

**Federation.** Federation returns as P2P remotes plus a router in front of the
inference routes. The router offers least in-flight, random and pinned selection,
with the local instance as a fallback, and keeps the meaning of the old
`--random-worker` and `--target-worker` options. A node joins with a `federated`
invite.

**Via your server.** The server can relay for its paired devices. It can also
relay between worker nodes, as an opt-in setting that is off by default. A server
with a public address can run the relay in process, with an ACL built from its
paired set and no admin protocol.

## Removal and migration

The change set removes edgevpn, the Explorer, the token-based federation, the old
P2P workers, the old P2P routes (`/api/p2p` and its token, workers, federation
and stats routes), the old P2P pages and the related settings and docs. It keeps
the two tunnel callbacks of the model-split engines and rewires them to `p2pnet`.

**Startup check.** A release with the removal lists, at start, every old flag,
environment variable and setting that is still set, and prints the replacement.

| Removed | Replacement |
|---|---|
| `--p2p`, `LOCALAI_P2P` (token network) | The "P2P enabled" setting, with authentication on |
| `--p2p-token`, `LOCALAI_P2P_TOKEN`, `P2P_TOKEN`, `TOKEN` | Invites (Swarm, P2P) |
| `--p2p-network-id`, `--p2p-dht-interval`, `--p2p-otp-interval` | None. The new layer has no overlay network. |
| `--federated`, `LOCALAI_FEDERATED` | A `federated` invite and P2P remotes |
| `LOCALAI_P2P_ENABLE_LIMITS`, `LOCALAI_P2P_DISABLE_DHT`, `LOCALAI_P2P_DHT_ANNOUNCE_MADDRS` | The P2P settings (public DHT, bootstrap peers, relays) |
| `local-ai p2p-worker p2p-llama-cpp-rpc`, `p2p-worker p2p-mlx` | `local-ai worker llama-cpp-rpc --join <link>`, `local-ai worker mlx-distributed --join <link>` |
| `local-ai federated`, `local-ai explorer` | None. Federation returns as remotes with a router. |

The removed flags are kept as hidden flags that exit with status 1 and print the
replacement. The environment variables are read once at start, because the flag
parser ignores unknown variables. Removed settings go through the existing
removed-settings path.

Users lose the Explorer and, until their nodes join with invites, the token
network. Federation and the model-split workers return in the same release.

## Security considerations

- **Admission.** Peers are admitted by peer ID at the handshake. An unpaired peer
  never gets a stream. While an invite is pending, an unknown peer may reach
  only the redeem route.
- **Codes.** Codes are single use and short lived. The server stores a keyed
  hash. Wrong attempts are limited per peer ID and per relay address. The
  response for a wrong, used or expired code is the same.
- **Identity key.** The private key is a file with mode 0600 under the data path.
  P2P refuses to start without a persistent data path. Regenerating the identity
  invalidates every paired device. The old key is kept as a backup.
- **Relays.** A relay sees peer IDs, addresses and byte counts. It cannot read
  content. Its logs hold metadata only. An open relay is an abuse target and
  carries a warning and a bandwidth limit. The pass protocol exposes the relay
  to unknown peers for ten seconds at a time, with per-address limits.
- **Admin channel.** The secure channel authenticates admins. A sequence number
  orders their writes. The relay refuses stale ones.
- **Authentication.** P2P requires authentication to be on. A device invite gives
  admission only.
- **Peer IDs as remote addresses.** Code that expects an IP in the request's
  remote address (rate limits, logs) must handle a peer ID. Limiters key on the
  peer ID for P2P requests.
- **Revocation latency.** With several frontends, each replica has its own
  identity and reloads its allowlist on a revoke broadcast and periodically.
  The worst-case delay is about 30 seconds.

## Out of scope

Carrying the distributed-mode tunnel lanes over libp2p, worker invites for the
distributed-mode tunnel and a router that mixes carriers depend on a change that
is still open. They are a follow-up on this layer.

## Open questions

1. **Public DHT traffic.** The DHT in client mode costs about 0.5 to 0.9 MB per
   minute. Recommendation: keep it on by default as decided, and show the
   setting next to the status.
2. **Rate-limit wrapper.** The wrapper must see both legs of a circuit.
   Recommendation: prove it with a test first. If it fails, vendor the relay
   package (MIT licence) and put the limiter in its copy loop.
3. **Remotes in distributed mode.** The proxy backend may load on a worker that
   has no forwarder. Recommendation: pin the proxy backend of P2P remotes to the
   frontend. Refuse a P2P remote only if pinning cannot work.
4. **MLX ranks.** MLX ranks may need links to each other, not only to the server.
   Recommendation: run a spike first. Ship llama.cpp sharding first if the spike
   shows that MLX needs worker-to-worker links.
5. **Admin set expiry.** A relay keeps the set of an admin that goes offline.
   Recommendation: keep it, and add an optional expiry after a long silence
   later.
6. **Relay logs.** Relays log metadata at info level. Recommendation: keep peer
   pairs at debug level and add a flag if operators ask for less.
7. **Identity key rotation.** The admin protocol has no operation to move a set
   to a new admin ID. Recommendation: leave it out of version 1. Rotation means
   pairing again.
8. **Phrase word list.** The list needs a licence check. Recommendation: use a
   short list with a permissive licence and four words.
9. **Several frontends.** Each replica has its own identity, and an invite names
   the replica that minted it. Recommendation: accept this for the first
   release.
10. **Launcher without the binary.** The launcher helper needs the `local-ai`
    binary even for remote-only use. Recommendation: verify how the launcher
    ships it before the launcher work starts.
