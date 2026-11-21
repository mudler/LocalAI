+++
title = "Model-split workers over P2P"
toc = true
description = "Run one large model across machines: llama.cpp RPC and MLX workers that join with an invite link"
tags = ["P2P", "Administration", "Distributed"]
categories = ["Features"]
weight = 146
+++

A **model-split worker** lends its memory to a model that runs on your LocalAI
server. With llama.cpp the worker holds some layers of the model; with MLX the
worker is a rank of the MLX ring. One model that does not fit on one machine
then runs across several.

A model-split worker is not a cluster node. It has no backends and no placement
rules, and it works on a single server too: you do not need
[distributed mode]({{% relref "/administration/distributed-mode" %}}). In a
cluster you attach each worker to a backend node, and the models that node
loads use it (see [Distributed mode](#distributed-mode)).

The worker joins over [P2P]({{% relref "/administration/p2p" %}}) with an
invite link. You do not open a port and you do not share a token. You can
revoke each worker alone.

## What you need

- The server runs in the P2P mode **Connect and share** (`p2p_mode` `share`),
  which needs authentication. See [Turn on P2P]({{% relref "/administration/p2p" %}}#turn-on-p2p).
- For llama.cpp: the worker machine runs `local-ai` (binary or container
  image). The worker installs the llama.cpp backend by itself.
- For MLX: the worker is a Mac with Apple Silicon and runs the `local-ai`
  binary or the desktop app. Containers cannot use Metal. Only the `ring` MLX
  backend works over P2P: `jaccl` needs RDMA links between the machines.

## Add a worker

1. Open **Nodes**, then **Add a node**, then **Model-split worker**.
2. Choose the engine: **llama.cpp RPC** or **MLX (Apple Silicon)**.
3. In a cluster, choose the backend node that the worker is attached to.
4. Optionally give the worker a name, then choose **Make invite**. The invite
   is valid for 10 minutes and works once. It has no QR code.
5. Run the command that the page shows on the worker machine. For llama.cpp:

   ```bash
   local-ai worker llama-cpp-rpc --join "localai://pair?v=1&k=split&..."
   ```

   With Docker, keep the worker's state on a volume, so that the same command
   works after the container is created again:

   ```bash
   docker run --net host --gpus all \
     -v localai-llama-cpp-rpc:/p2p \
     -e LOCALAI_P2P_STATE_DIR=/p2p \
     localai/localai:latest-gpu-nvidia-cuda-12 worker llama-cpp-rpc \
     --join "localai://pair?v=1&k=split&..."
   ```

   For MLX:

   ```bash
   local-ai worker mlx-distributed --join "localai://pair?v=1&k=split&..."
   ```

6. The worker pairs, prints a four-word confirmation phrase, and connects. The
   page shows the worker when it arrives. Check that the phrase on the worker
   is the same as the phrase of the used invite under **Swarm**, **P2P**.

The worker keeps its identity and its pairing in `--p2p-state-dir`
(`./p2p/llama-cpp-rpc` or `./p2p/mlx-distributed` by default). The next start
with the same `--join` link uses the saved pairing, even after the link
expired. The server knows the worker by this identity: if you delete the
directory, the worker must pair again with a new invite.

Workers that are in use show on the **Nodes** page under **Model-split
workers**, with the engine, the path to the worker and the memory it adds.

## Use the workers

### llama.cpp

The server forwards a local port to each llama.cpp worker that is in use and
passes the list to llama.cpp in `LLAMACPP_GRPC_SERVERS`, the variable that
static RPC workers use. llama.cpp reads it when it loads a model, so load the
model again after a worker joins or leaves. A worker keeps its local port while
the server runs, also when P2P restarts.

A model can also name its workers itself with the `grpc_servers` option; see
[text generation]({{% relref "/models/capabilities/text/text-generation" %}}).
The image-generation backend reads `LLAMACPP_GRPC_SERVERS` too.

### MLX

The server gives each MLX worker its rank, in the order the workers paired:
rank 0 is the MLX process of the server, the workers follow. The server writes
the hostfile of rank 0 and sets `MLX_DISTRIBUTED_HOSTFILE`; each worker writes
its own hostfile and starts the MLX process for its rank. Load the model with
the `mlx-distributed` backend after the ring changes:

```yaml
name: llama-distributed
backend: mlx-distributed
parameters:
  model: mlx-community/Llama-3.2-1B-Instruct-4bit
```

The ranks of the ring talk to each other. The workers need no connection to
each other: the server relays the ring traffic between two workers. Every
token of a ring of three or more machines therefore crosses the server.

## Paths, relays and "Use anyway"

The layers of a split model talk to each other on every token, so the path to
a worker matters. LocalAI uses each worker by its path:

| Path | What LocalAI does |
|---|---|
| Direct or hole-punched | Uses the worker. |
| A relay without a data limit | Shows **Slow: through a relay** and waits. Choose **Use anyway** to use the worker; every token then waits for the relay. **Stop using** undoes it. |
| A relay of yours with a data or time limit (a gated relay this server administers, for example with the default circuit limit) | Shows **Slow: through a relay**, with the limits after which the relay cuts each connection. **Use anyway** uses the worker until the relay cuts the connection; the model then stops until you load it again. **Relay limits** opens the limits of the relay, where you raise or remove them. |
| A public relay | Shows **Public relay: not used**. A public relay limits each connection to a small amount of data and you cannot change its limits, so it never carries model layers. |

To run longer through a relay of yours, raise or remove the data and time
limits of its circuits with **Relay limits**, on the **Relays** page, or with
`PUT /api/p2p/relays/{id}/limits` (see [Run your own relay]({{% relref "/administration/p2p-relay" %}})).
A direct path is still faster: forward UDP and TCP port 4001 on either side and
start the worker with
`--p2p-listen /ip4/0.0.0.0/tcp/4001 --p2p-listen /ip4/0.0.0.0/udp/4001/quic-v1`.

When a better path opens later, for example after a hole punch, LocalAI uses it
at the next connection.

## Revoke a worker

Choose **Revoke** next to the worker on the **Nodes** page, or revoke its device
under **Swarm**, **P2P**. The connection closes at once and the worker leaves
the list that llama.cpp or MLX reads. A model that is loaded with the worker
fails until you load it again. To join again, the worker needs a new invite.

## Worker flags

`local-ai worker llama-cpp-rpc` and `local-ai worker mlx-distributed` take
these flags with `--join`:

| Flag | Environment variable | Meaning |
|---|---|---|
| `--join` | `LOCALAI_P2P_JOIN` | The invite link of kind model-split worker. |
| `--p2p-state-dir` | `LOCALAI_P2P_STATE_DIR` | Where the worker keeps its identity, its pairing and, for MLX, its hostfile. |
| `--p2p-name` | `LOCALAI_P2P_NAME` | The name of the worker on the server. The default is the host name. |
| `--p2p-allow-open-relay` | `LOCALAI_P2P_ALLOW_OPEN_RELAY` | Connect through an open relay that the invite names. |
| `--p2p-listen` | `LOCALAI_P2P_LISTEN_MADDRS` | Addresses to listen on. The default is TCP and QUIC on every interface, on random ports. |
| `--p2p-bootstrap` | `LOCALAI_P2P_BOOTSTRAP_PEERS_MADDRS` | Bootstrap peers, addresses that end in `/p2p/<peer ID>`. |
| `--p2p-no-public-dht` | `LOCALAI_P2P_NO_PUBLIC_DHT` | Do not use the public DHT, which finds public relays for hole punching. |

`--llama-cpp-args` passes extra arguments to llama.cpp's `rpc-server`. Without
`--join` the commands run as before: `llama-cpp-rpc` starts a standalone
`rpc-server`, and `mlx-distributed` needs `--hostfile` and `--rank`.

The old commands `local-ai worker p2p-llama-cpp-rpc` and `local-ai worker
p2p-mlx`, which joined with a P2P token, were removed. They exit with a message
that names the replacement.

## HTTP API and assistant

| Route | What it does |
|---|---|
| `GET /api/p2p/split-workers` | Lists the workers: `{workers: [{id, label, peer_id, engine, state, allow_slow_path, path, memory_bytes, addr, rank, node_id, node_name, ...}], memory_in_use, distributed}`. `state` is `online`, `offline`, `slow`, `limited` (public relay) or `refused`. A relayed worker carries `relay_id` and `own_relay` (a relay this server administers, whose limits can change). `node_id` is the backend node the worker is attached to; `distributed` is true in a cluster. |
| `PATCH /api/p2p/split-workers/{id}` | `{allow_slow_path: true}` is **Use anyway**; `false` stops using the worker over a relay. `{node_id: "<node ID>"}` attaches the worker to a backend node that is a split server (otherwise `409`); `{node_id: ""}` attaches it to this server. Give at least one of the two. |
| `PUT /api/nodes/{id}/split-server` | `{split_server: true}` declares the node a split server; `false` clears it and detaches its workers. Answers `{split_server, detached}`. |
| `DELETE /api/nodes/{id}/split-server` | Hands the declaration back to the `--split-server` flag of the node, at its next registration. |

In a cluster each worker also carries `node_offline` (its node is offline),
`node_note` (why a worker attached to no node was detached) and `node_relay`
(`{state, reason, relays}`, see [Relays for the nodes](#relays-for-the-nodes)).

A worker invite is `POST /api/p2p/invites` with `kind` `split` and
`labels: {engine: "llama" | "mlx", node: "<node ID>"}` (default engine `llama`).
With no `node` label the worker goes to the only online split server, when
there is exactly one; `node: ""` keeps it on this server. A worker that
connects with another engine than its invite named is refused. An invite that
names a node that is not a backend node is refused, and one that names a node
that is not a split server gets `409`. The
routes are for administrators and need mode `share`.

The LocalAI Assistant has the tools `list_p2p_split_workers`,
`set_p2p_split_slow_path`, `set_p2p_split_node` and `set_node_split_server`,
and `get_p2p_relay_limits` and `set_p2p_relay_limits` for the limits of a relay
of yours.

## Distributed mode

In [distributed mode]({{% relref "/administration/distributed-mode" %}}) the
models load on the backend nodes, not on the frontend. You attach each
model-split worker to one backend node, and the llama.cpp and MLX processes
that this node starts use the worker, with the same rules as on a single
server: the paths and **Use anyway** above, the MLX ring with the node as
rank 0, and a worker that leaves drops out of the next load.

### Split servers

A backend node accepts model-split workers only when it is a **split server**.
A node never becomes one by itself or because a worker was attached to it.
One of these makes it one:

- The operator of the node starts it with `--split-server`
  (`LOCALAI_SPLIT_SERVER=true`). The node sends the flag when it registers.
- An administrator turns it on: on the **Nodes** page, select the node and
  choose **Make split server**, or use **Set up a split server** under
  **Model-split workers**. The dialog says what changes on the node and needs a
  confirm click. The same change is `PUT /api/nodes/{id}/split-server` with
  `{"split_server": true}`, and the assistant tool `set_node_split_server`.

The choice of the administrator wins over the flag until
`DELETE /api/nodes/{id}/split-server` hands it back; the next registration of
the node then sets it from its flag. The declaration is stored with the node in
the database, so every frontend sees the same value.

When a node becomes a split server:

- It starts a P2P host for its model-split workers and opens its ports. This
  happens within one heartbeat interval, without a restart.
- The frontends admit it on the gated relays that you run (see
  [Relays for the nodes](#relays-for-the-nodes)).
- The workers attached to it are used from the **next** llama.cpp or MLX model
  it loads. A model that is loaded keeps its workers until it is loaded again.
  The node sets `LLAMACPP_GRPC_SERVERS` and the MLX hostfile itself; you do not
  wire each worker.

When you turn it off, its P2P host stops, the relays stop admitting it, and its
workers are detached: they wait, attached to no node, with a note that says
why, until you attach them to another split server. The same happens when the
node registers again without `--split-server` and no administrator chose
otherwise.

An attach to a node that is not a split server is refused with `409` and an
error that names the declare action. Nothing changes on the node.

### Attach a worker

Attach a worker when you make its invite (step 3 of [Add a worker](#add-a-worker)),
or later in the **Attached to** column of **Model-split workers** on the
**Nodes** page. The list offers split servers only. When exactly one split
server is online, a new worker goes there by default, also when the invite
names no node; with two or more, choose one; with none, the page says so and
offers **Set up a split server**. A worker that is attached to the frontend is
not used in a cluster, and the page says so. A worker whose node is offline
stays attached and shows **Node offline**; nothing moves it to another node.
The router does not count the memory of the workers when it places a model, so
pin the model to the node with a
[placement rule]({{% relref "/administration/distributed-mode" %}}) (a node
selector), and load it again after a worker joins, leaves or moves.

How the parts find each other:

1. The worker pairs with a frontend, as on a single server, and opens its
   control session there.
2. The frontend sends a worker that is attached to a node to that node: the
   answer names the peer ID and the addresses of the node. The worker admits
   the node and opens its control session there. When that session ends, it
   asks the frontend again, so a worker that moves follows at its next session.
3. The node runs the split manager on its own P2P host. It forwards a local
   port to each worker, sets `LLAMACPP_GRPC_SERVERS` and
   `MLX_DISTRIBUTED_HOSTFILE` for the backends it starts, and relays the MLX
   ring streams. The node admits only the workers attached to it.
4. At every heartbeat interval the node sends the frontend the address of its
   host and the state of its workers (`POST /api/node/{id}/split`, with the
   node credential), and gets the workers attached to it, their **Use anyway**
   choice, and the relays of the frontend.

A node that joined over P2P (`local-ai worker --join`) uses the P2P host it
joined with. A node that registered with the token starts a P2P host when it
becomes a split server, with its identity in
`<models path>/../data/p2p/identity.key` (`--p2p-identity`). Set
`--p2p-listen` (`LOCALAI_P2P_LISTEN_MADDRS`) on the node to a fixed, open port
to give the workers a direct path.

### Relays for the nodes

A worker that reaches a node neither directly nor by hole punching needs a
[relay of yours]({{% relref "/administration/p2p-relay" %}}). A node that
joined over P2P is a paired device, so your relays admit it already. For a node
that registered with the token, every frontend adds the P2P host of the node to
its list on each gated relay that it administers, next to the paired devices,
while all of these are true:

- the node is a split server, approved and online;
- it registered with the current registration token;
- at least one model-split worker that is not revoked is attached to it;
- it reported the peer ID of its host, and that peer is the one the node is
  bound to.

The node is bound to the first peer it reports after it registers: a report of
another peer for the same node is refused (`409`), so a holder of the
registration token cannot put its own peer on your relays in the name of a
node. A node that registers again, for example after it lost its key, may bind
a new peer.

The node leaves the relays when one of these stops being true: at the next
reload of the frontends (every 30 seconds, and at once on the frontend that
sees the change). A deleted node, a node whose registration token was replaced,
a node that is no longer a split server and a node whose last worker was
detached or revoked lose their place.

The frontend names, in its answer to the node, the relays that admit it, and
the node keeps a reservation on them and reports its addresses through them.
A node starts its host again, with the same identity, when that list changes.

The paired devices come first on a relay. When the list does not fit in what
the relay allows one admin (`--max-peers-per-admin`), nodes are left out in the
order of their peer IDs, so every frontend leaves out the same ones. The
**Model-split workers** group shows the relay state of the node of each worker:

| State | Meaning |
|---|---|
| **Relay: admitted** | At least one of your relays admits the node. |
| **Relay: refused** | No relay admits it. The reason says why: a full relay, a node that is not approved, or a registration token that is no longer valid. |
| **Relay: pending** | Not yet: the node has not reported its host, it is offline, or a relay cannot be reached. |
| **Relay: not needed** | The node joined over P2P, or this server administers no gated relay. |

The same state is `node_relay` in `GET /api/p2p/split-workers`.

State in a cluster: the split server declaration is stored with the node, and
the node a worker is attached to with the device, both in the database. The
nodes that the frontends vouch for on the relays are derived from the database
at every reload; each frontend writes its own list on each relay, and a relay
whose list is already right gets no write, so the frontends agree without a
leader. The relay state that the page shows is what the relay manager of the
frontend that answers last saw (state per frontend). The state of an attached worker is what its node last
reported, stored in the database (table `node_split_reports`), so every
frontend shows the same state; a node that has not reported for a minute shows
its workers offline. The node keeps its list of workers in memory and asks for
it again at every interval and after a restart; when the frontend cannot be
reached it keeps the last list. A worker attached to the frontend keeps its
connection to the frontend that it reaches, and that frontend shows the state
of its own connections. A change of node or a revoke reaches the node within
one heartbeat interval (10 seconds by default).
