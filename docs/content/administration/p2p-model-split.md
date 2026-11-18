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
| `PATCH /api/p2p/split-workers/{id}` | `{allow_slow_path: true}` is **Use anyway**; `false` stops using the worker over a relay. `{node_id: "<node ID>"}` attaches the worker to a backend node; `{node_id: ""}` attaches it to this server. Give at least one of the two. |

A worker invite is `POST /api/p2p/invites` with `kind` `split` and
`labels: {engine: "llama" | "mlx", node: "<node ID>"}` (default engine `llama`,
no node). A worker that connects with another engine than its invite named is
refused. An invite that names a node that is not a backend node is refused. The
routes are for administrators and need mode `share`.

The LocalAI Assistant has the tools `list_p2p_split_workers`,
`set_p2p_split_slow_path` and `set_p2p_split_node`, and `get_p2p_relay_limits`
and `set_p2p_relay_limits` for the limits of a relay of yours.

## Distributed mode

In [distributed mode]({{% relref "/administration/distributed-mode" %}}) the
models load on the backend nodes, not on the frontend. You attach each
model-split worker to one backend node, and the llama.cpp and MLX processes
that this node starts use the worker, with the same rules as on a single
server: the paths and **Use anyway** above, the MLX ring with the node as
rank 0, and a worker that leaves drops out of the next load.

Attach a worker when you make its invite (step 3 of [Add a worker](#add-a-worker)),
or later in the **Attached to** column of **Model-split workers** on the
**Nodes** page. A worker that is attached to the frontend is not used in a
cluster, and the page says so. The router does not count the memory of the
workers when it places a model, so pin the model to the node with a
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
joined with. A node that registered with the token starts a P2P host the first
time a worker is attached to it, with its identity in
`<models path>/../data/p2p/identity.key` (`--p2p-identity`). Set
`--p2p-listen` (`LOCALAI_P2P_LISTEN_MADDRS`) on the node to a fixed, open port
to give the workers a direct path. A node that registered with the token is not
a paired device, so the gated relays of the frontend do not admit it: the
workers reach it directly or by hole punching. When neither works, join the
node over P2P.

State in a cluster: the node a worker is attached to is stored with the device
in the database. The state of an attached worker is what its node last
reported, stored in the database (table `node_split_reports`), so every
frontend shows the same state; a node that has not reported for a minute shows
its workers offline. The node keeps its list of workers in memory and asks for
it again at every interval and after a restart; when the frontend cannot be
reached it keeps the last list. A worker attached to the frontend keeps its
connection to the frontend that it reaches, and that frontend shows the state
of its own connections. A change of node or a revoke reaches the node within
one heartbeat interval (10 seconds by default).
