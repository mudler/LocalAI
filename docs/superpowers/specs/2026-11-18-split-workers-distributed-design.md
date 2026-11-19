# Model-split workers on the backend nodes of a cluster

Date: 2026-11-18. Status: draft for review. Each open question has a
recommendation under [Open questions](#open-questions).

## Intent

A model-split worker (`local-ai worker llama-cpp-rpc --join <link>` or
`local-ai worker mlx-distributed --join <link>`) lends its memory to a model
over P2P. Today only a standalone server can use one: the split manager runs on
the server, forwards a loopback port to each worker and hands the addresses to
llama.cpp (`LLAMACPP_GRPC_SERVERS`) and MLX (the ring hostfile).

In distributed mode the models run on the backend nodes, not on the frontend.
A split worker that paired with a frontend is useless there. This note lets
the administrator attach a split worker to one backend node, so that a model
that the node loads shards across the workers attached to it, with the same
rules as a standalone server: the slow path waits for "Use anyway", a public
relay never carries model layers, MLX uses the ring backend only, and a worker
that leaves is dropped from the next load.

## Non-goals

- Placement. The router does not learn about split memory. The administrator
  pins the model to the node with the existing scheduling rules (node
  selector). See [Open questions](#open-questions).
- A split worker shared by several nodes at once. A worker serves one node.
- Links between split workers. MLX ring streams still go through the node
  that is rank 0, as they go through the server today.
- Agent nodes. Only backend nodes load models.

## Decisions

| Decision | Reason |
|---|---|
| The node runs the split manager (`p2psplit.Manager`), on a P2P host of its own. | The forwarders, the slow-path policy, the MLX ring and the path checks must see the path between the node and the worker, because the engine traffic flows there. Running the existing manager on the node reuses all of it unchanged. A route through the frontend would double every hop of the RPC traffic and put it on the frontend. |
| The split worker still pairs with a frontend (a `split` invite, as today). The frontend tells it where to go. | The invite, the confirmation phrase, the revocation and the relay pass stay in one place. The worker needs no new command or flag. |
| The assignment is a field of the split device in the pairing store (`node_id`). Empty means "this server". | One canonical, durable home, shared by every frontend. No process memory holds it. |
| A frontend answers the hello of an attached worker with `moved` (node peer ID and addresses). The worker opens its control session to the node. When that session ends, the worker asks the frontend again. | The frontend stays the authority. A worker that is moved to another node, or back to the server, follows on its next session. A node that is gone sends the worker back to the frontend, which answers `wait` until the node reports again. |
| The node learns its workers from the frontend over a node route, `POST /api/node/{id}/split`, every heartbeat interval. The request carries the node's P2P endpoint and the live state of its workers; the answer carries the workers attached to the node. | The node has no database. The route uses the node authentication that the heartbeat uses (registration token, or the peer ID of a node that joined over P2P), on both carriers. A route of its own leaves the checkpointed heartbeat path alone. |
| The frontend stores the report in table `node_split_reports` (one row per node). The split worker views read it. | State in the database: any frontend shows the same view. A report older than three intervals reads as offline. |
| A node serves split workers only when it is a **split server**: a field of the node record (`split_server`), set by the worker's `--split-server` flag at registration or by the admin (Nodes page, `PUT /api/nodes/{id}/split-server`, MCP `set_node_split_server`). The admin's choice wins over the flag until `DELETE` hands it back. | A standard worker never becomes a split-worker host without consent. The cluster admin already controls what the workers run, so an explicit admin action is consent too; the frontend never declares a node on its own. One durable home, shared by every frontend. |
| The node learns its declaration in the answer of the split exchange (`split_server`) and applies it without a restart: a split server starts its P2P host and the split manager at once; a node that is not one stops them and serves nobody. | The exchange already runs every heartbeat interval on both carriers; no new seam. The host and its ports start only after consent, not as a side effect of an attach. |
| An attach to a node that is not a split server (invite label or `PATCH node_id`) is refused with 409 and an error that names the declare action; nothing changes. Clearing the declaration detaches its workers (`node_id` empty, with `node_note`), drops its relay admission and stops its host. Every frontend also detaches, at its reload, the workers of a node that is no longer declared (a restart without the flag). | No hidden state on the node, and an idempotent repair on every frontend. |
| A split invite that names no node attaches the worker to the only online split server, when there is exactly one; `node: ""` keeps it on the frontend. With several the admin picks; with none the interface says so and offers the quick setup. A worker whose node is offline stays attached and shows `node_offline`; nothing moves it. | Moving memory between machines without the admin is a surprise; a single choice is not. |
| A node that joined over P2P uses its P2P host. A node that registered with the token starts a host of its own when it becomes a split server, with the identity under its data path. | Both kinds of node can lend their models to split workers. The host of a token node admits only the split workers attached to it. |
| The frontends vouch for the split host of a token node on the gated relays they administer, next to the paired devices, while it is a split server, approved, online, registered with the current token, has an active split worker attached and reported the peer it is bound to. | The worker already reports its peer to the frontend over an authenticated route; the admin-op mechanism of paired devices pushes it. A relay is the last path when direct and hole punching fail. |
| A token node is bound to the first peer it reports (`node_split_reports.pinned_peer`); another peer gets 409 until the node registers again. | Every token node shares the registration token, so without the binding a holder of the token could put its own peer on the relays in the name of a node. Re-registration is the re-authentication. |
| The set is derived from the database at every reload of the allowlist (30 s, and at once on the frontend that took the report or the change, which tells the others). Each frontend writes its own list on each relay; a relay whose digest matches gets no write. | Distributed-safe without a leader: idempotent sync, no per-process truth. |
| Paired devices come first on a relay. Node peers that do not fit in the per-admin limit are left out in the order of their peer IDs, and the relay reports its limit to admins (`max_peers`). A paired device that does not fit makes the next sync drop a node. The split view shows `node_relay`: admitted, refused (with the reason), pending or not_needed. | Every frontend leaves out the same nodes, and the admin sees why. |
| The frontend names, in its answer, only the relays that already admit the node; the node starts its host again (same identity) when that list changes, and forgets it when it is empty. | A reservation that a relay refused keeps a host off that relay for minutes (the dial backoff of libp2p), so the node must not reserve before it is admitted. |
| The node's host admits only the peers of the workers attached to it (plus the frontend for a node over P2P). The worker's host admits the node it was moved to, and drops it when it leaves. | Admission stays by peer ID at the handshake, on both ends. The frontend vouches for both peers over channels that are already authenticated. |
| The node sets `LLAMACPP_GRPC_SERVERS` and `MLX_DISTRIBUTED_HOSTFILE` in its own environment, as a standalone server does. Backend processes that the node starts afterwards inherit them. | The engines are unchanged, and a model that is already loaded keeps its workers until it is loaded again, as on a standalone server. |
| "Use anyway" (`allow_slow_path`) and "own relay" are read by the node from the answer. Own relays are the gated relays that the frontend administers. | The slow-path rule needs both, and both are decided by the frontend's settings. |

## Who dials whom

```text
split worker ──hello──▶ frontend            (control, /localai/split/1)
             ◀─moved── (node peer, addrs)
split worker ──hello──▶ node                (control, /localai/split/1)
node ─────── llama RPC streams ──────────▶ split worker   (/localai/llama-rpc/1)
node ◀──────▶ MLX ring streams ◀────────▶ split worker   (/localai/mlx/1, node relays between workers)
node ──POST /api/node/{id}/split──▶ frontend   (report in, assignment out)
```

The node owns the loopback forwarders. The frontend owns no forwarder for an
attached worker.

## Wire

Control messages gain two types (protocol version stays 1; both peers ship in
the same release):

- `moved`: `peer` (node peer ID) and `addrs` (its multiaddrs). The worker
  admits the peer, dials it with the addresses and the relays of its invite,
  and sends its hello there.
- `wait`: `error` says why. The session ends and the worker retries with its
  backoff. A node answers it to a worker that it does not know yet, and a
  frontend answers it while the node has not reported a P2P endpoint.

`workerctl.SplitReport` (node to frontend): `peer_id`, `addrs`, `workers`
(device ID, state, path, memory, name, version, address, rank, since,
error). `workerctl.SplitAssignment` (frontend to node): `workers` (device ID,
label, peer ID, engine, allow_slow_path, created_at) and `own_relays`
(peer IDs).

## API

- `PATCH /api/p2p/split-workers/{id}` takes `node_id` too: a backend node ID,
  or `""` for this server. At least one field is required. Swagger additions
  only.
- `GET /api/p2p/split-workers` adds `node_id` and `node_name` to each worker
  and `distributed` to the list, so the interface can show the node picker and
  say that a worker attached to no node is not used in a cluster.
- MCP: `set_p2p_split_node` (mutating, confirmation) next to
  `set_p2p_split_slow_path`; `list_p2p_split_workers` returns the node.
- Node route `POST /api/node/{id}/split` (node authentication, not admin). It
  is not in the admin API, the capability registry or the MCP tools, as the
  heartbeat is not. The answer adds `split_server` and `relays` (the relays
  that admit the node).
- `PUT /api/nodes/{id}/split-server` (`{split_server}`, answers
  `{split_server, detached}`) and `DELETE` (back to the worker flag). MCP:
  `set_node_split_server` (mutating, confirmation). `GET /api/nodes` shows
  `split_server`.
- Each split worker view adds `node_offline`, `node_note` and `node_relay`
  (`{state, reason, relays}`). Swagger additions only.
- Worker flag `--split-server` (`LOCALAI_SPLIT_SERVER`), sent at registration
  as `split_server`.
- Relay admin protocol: answers to admins add `max_peers`.

## Distributed state

- Assignment: durable, in the pairing store (`p2p_devices.node_id`).
- Live state of attached workers: in `node_split_reports`, written by the
  frontend that took the report. Shared.
- The node's copy of its assignment: process memory of the node, refreshed
  from the frontend every interval. It is a cache of database state; a node
  that restarts asks again. Documented as per node.
- The frontend's own split manager (unattached workers) stays per instance,
  as before.
- Split server declaration: durable, on the node record (`split_server`,
  `split_server_manually_set`). Shared.
- Peer binding of a token node: durable, `node_split_reports.pinned_peer`,
  set with one conditional statement, cleared at registration. Shared.
- Node peers on the relays: derived from the database at every reload, on
  every frontend; idempotent writes to each frontend's own list on each
  relay. The admission state shown is what the relay manager of the
  answering frontend saw (per instance, documented).

## Failure behaviour

- A worker leaves: the node's manager drops it, as on a server. The next load
  uses the remaining workers.
- The node restarts: the worker's session ends, it goes back to the frontend,
  which answers `wait` until the node reports, then `moved`.
- The node's report fails (frontend down, 409 on a frontend that does not
  share): the node keeps its last assignment and tries again.
- The worker is revoked or moved: the node sees it gone from the next answer,
  closes its session and revokes the peer on its host. Delay: one interval
  (10 s by default).

## Open questions

1. **Placement.** The router does not count split memory, so a big model may
   land on a node with no workers. Recommendation: document pinning with a
   node selector for this release; later, add the online split memory of a
   node to the memory the scheduler places against, for llama.cpp and MLX
   backends only.
2. **Worker moved while a model is loaded.** The engine keeps the addresses it
   read at load time. Recommendation: same as on a server; the interface says
   that changes apply to the next model load.

Decided since the first draft: the default assignment (the only online split
server, else the admin picks), relays for token nodes (the frontends admit
them, see Decisions), and the split server declaration.
