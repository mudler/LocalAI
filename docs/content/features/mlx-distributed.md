+++
disableToc = false
title = "MLX Distributed Inference"
weight = 18
url = '/features/mlx-distributed/'
+++

MLX distributed inference allows you to split large language models across multiple Apple Silicon Macs (or other devices) for joint inference. Unlike federation (which distributes whole requests), MLX distributed splits a single model's layers across machines so they all participate in every forward pass.

## How It Works

MLX distributed uses **pipeline parallelism** via the Ring backend: each node holds a slice of the model's layers. During inference, activations flow from rank 0 through each subsequent rank in a pipeline. The last rank gathers the final output.

For high-bandwidth setups (e.g., Thunderbolt-connected Macs), **JACCL** (tensor parallelism via RDMA) is also supported, where each rank holds all layers but with sharded weights.

## Prerequisites

- Two or more machines with MLX installed (Apple Silicon recommended)
- Network connectivity between all nodes (TCP for Ring, RDMA/Thunderbolt for JACCL)
- Same model accessible on all nodes (e.g., from Hugging Face cache)

## Quick Start with P2P

The simplest way to use MLX distributed is with LocalAI's P2P auto-discovery.

### 1. Start LocalAI with P2P

```bash
docker run -ti --net host \
  --name local-ai \
  localai/localai:latest-metal-darwin-arm64 run --p2p
```

This generates a network token. Copy it for the next step.

### 2. Start MLX Workers

On each additional Mac:

```bash
docker run -ti --net host \
  -e TOKEN="<your-token>" \
  --name local-ai-mlx-worker \
  localai/localai:latest-metal-darwin-arm64 worker p2p-mlx
```

Workers auto-register on the P2P network. The LocalAI server discovers them and generates a hostfile for MLX distributed.

### 3. Use the Model

Load any MLX-compatible model. The `mlx-distributed` backend will automatically shard it across all available ranks:

```yaml
name: llama-distributed
backend: mlx-distributed
parameters:
  model: mlx-community/Llama-3.2-1B-Instruct-4bit
```

## Manual Setup with CLI

For setups without P2P, use the `worker mlx-distributed` command. LocalAI handles backend installation automatically.

### Ring Backend (TCP)

The Ring backend uses TCP for pipeline parallelism. Each rank listens on a TCP port for ring communication with its neighbors. The **hostfile** is a JSON array where entry `i` is the `"ip:port"` that **rank `i` binds to and listens on**. All ranks must use the same hostfile so they know how to reach each other.

**Example:** Two Macs on a local network — Mac A is `192.168.1.10`, Mac B is `192.168.1.11`.

Create `hosts.json` (identical on both machines):

```json
["192.168.1.10:5555", "192.168.1.11:5555"]
```

- Entry 0 (`192.168.1.10:5555`) — the address rank 0 (Mac A) listens on
- Entry 1 (`192.168.1.11:5555`) — the address rank 1 (Mac B) listens on

Each rank binds to its own entry and connects to its neighbors for the ring pipeline. Port 5555 is arbitrary — use any available port, but it must be open in your firewall.

Start rank 0 on **Mac A** (`192.168.1.10`):

```bash
local-ai worker mlx-distributed --hostfile hosts.json --rank 0 --addr localhost:50051
```

Start rank 1 on **Mac B** (`192.168.1.11`):

```bash
local-ai worker mlx-distributed --hostfile hosts.json --rank 1
```

Rank 0 starts a gRPC server (on `--addr`) that LocalAI connects to for inference requests. The `--addr` flag is separate from the ring hostfile — it controls where the gRPC API listens, not the ring communication. All other ranks run as workers that participate in each forward pass.

### JACCL Backend (RDMA/Thunderbolt)

For Thunderbolt-connected Macs, JACCL provides tensor parallelism via RDMA for higher throughput.

The **device matrix** is a JSON 2D array describing the RDMA device name used to communicate between each pair of ranks. Entry `[i][j]` is the RDMA device that rank `i` uses to talk to rank `j`. The diagonal is `null` (a rank doesn't talk to itself).

```json
[
  [null, "rdma_thunderbolt0"],
  ["rdma_thunderbolt0", null]
]
```

The **coordinator** is a TCP endpoint where one node (typically rank 0) runs a coordination service that helps all ranks establish their RDMA connections. Rank 0 binds to this address; all other ranks connect to it. Use rank 0's IP address and any available port.

**Example:** Mac A (`192.168.1.10`) is rank 0, Mac B is rank 1, connected via Thunderbolt.

Start rank 0 on **Mac A** (`192.168.1.10`):

```bash
local-ai worker mlx-distributed \
  --hostfile devices.json \
  --rank 0 \
  --backend jaccl \
  --coordinator 192.168.1.10:5555 \
  --addr localhost:50051
```

Start rank 1 on **Mac B**:

```bash
local-ai worker mlx-distributed \
  --hostfile devices.json \
  --rank 1 \
  --backend jaccl \
  --coordinator 192.168.1.10:5555
```

Both ranks point `--coordinator` to rank 0's IP. Rank 0 binds to that address to accept RDMA setup connections from other ranks.

## CLI Reference

### `worker mlx-distributed`

Standalone mode — run with a manual hostfile.

| Flag | Env | Default | Description |
|------|-----|---------|-------------|
| `--hostfile` | `MLX_DISTRIBUTED_HOSTFILE` | *(required)* | Path to hostfile JSON. For Ring: array of `"ip:port"` where entry `i` is rank `i`'s listen address. For JACCL: device matrix of RDMA device names. |
| `--rank` | `MLX_RANK` | *(required)* | Rank of this process (0 = gRPC server + ring participant, >0 = worker only) |
| `--backend` | `MLX_DISTRIBUTED_BACKEND` | `ring` | Backend type: `ring` (TCP pipeline parallelism) or `jaccl` (RDMA tensor parallelism) |
| `--addr` | `MLX_DISTRIBUTED_ADDR` | `localhost:50051` | gRPC API listen address for LocalAI to connect to (rank 0 only, separate from ring communication) |
| `--coordinator` | `MLX_JACCL_COORDINATOR` | | JACCL coordinator `ip:port` — rank 0's IP where it accepts RDMA setup connections (jaccl only, required for all ranks) |

### `worker p2p-mlx`

P2P mode — auto-discovers peers and generates hostfile.

| Flag | Env | Default | Description |
|------|-----|---------|-------------|
| `--token` | `TOKEN` | *(required)* | P2P network token |
| `--mlx-listen-port` | `MLX_LISTEN_PORT` | `5555` | Port for MLX communication |
| `--mlx-backend` | `MLX_DISTRIBUTED_BACKEND` | `ring` | Backend type: `ring` or `jaccl` |

## Troubleshooting

- **All ranks must have the model downloaded.** MLX distributed does not transfer model weights over the network. Ensure each node has the model in its Hugging Face cache.
- **Timeout errors:** If ranks can't connect, check firewall rules. The Ring backend uses TCP on the ports listed in the hostfile.
- **Rank assignment:** In P2P mode, rank 0 is always the LocalAI server. Worker ranks are assigned by sorting node IDs.
- **Performance:** Pipeline parallelism adds latency proportional to the number of ranks. For best results, use the fewest ranks needed to fit your model in memory.

## Acknowledgements

The MLX distributed auto-parallel sharding implementation is based on [exo](https://github.com/exo-explore/exo).
