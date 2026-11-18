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
| A node that joined over P2P uses its P2P host. A node that registered with the token starts a host of its own the first time a worker is attached to it, with the identity under its data path. | Both kinds of node can lend their models to split workers. The host of a token node admits only the split workers attached to it. |
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
  heartbeat is not.

## Distributed state

- Assignment: durable, in the pairing store (`p2p_devices.node_id`).
- Live state of attached workers: in `node_split_reports`, written by the
  frontend that took the report. Shared.
- The node's copy of its assignment: process memory of the node, refreshed
  from the frontend every interval. It is a cache of database state; a node
  that restarts asks again. Documented as per node.
- The frontend's own split manager (unattached workers) stays per instance,
  as before.

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
2. **Default assignment in a cluster.** A worker attached to no node is not
   used in distributed mode. Recommendation: no automatic choice; the
   interface asks for a node and the invite can name it. An automatic choice
   would move memory between machines without the administrator noticing.
3. **Relays for a token node.** A node that registered with the token is not a
   paired device, so the gated relays of the frontend do not admit it.
   Recommendation: such a node is reached directly or by hole punching; when
   neither works, join the node over P2P (`worker --join`) so that the relays
   admit it. Pushing token nodes to the relays is a follow-up.
4. **Worker moved while a model is loaded.** The engine keeps the addresses it
   read at load time. Recommendation: same as on a server; the interface says
   that the model must be loaded again.
</content>
</invoke>
