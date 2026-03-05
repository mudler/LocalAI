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

## Manual Setup with Hostfile

For setups without P2P, you can provide a hostfile directly.

### Ring Backend (TCP)

Create a JSON hostfile listing all ranks:

```json
["192.168.1.10:5555", "192.168.1.11:5555"]
```

Start rank 0 (the gRPC server):

```bash
python backend.py --addr localhost:50051 --backend ring --hostfile hosts.json --rank 0
```

Start rank 1 (worker):

```bash
python backend.py --worker --backend ring --hostfile hosts.json --rank 1
```

### JACCL Backend (RDMA/Thunderbolt)

Create a JSON device matrix (`null` on diagonal):

```json
[
  [null, "rdma_thunderbolt0"],
  ["rdma_thunderbolt0", null]
]
```

Start with `--backend jaccl` and `--coordinator <coordinator-address>`.

## Environment Variables

| Variable | Description |
|----------|-------------|
| `MLX_DISTRIBUTED_HOSTFILE` | Path to hostfile JSON (auto-set by P2P) |
| `MLX_LISTEN_PORT` | Port for MLX communication (default: 5555) |
| `MLX_DISTRIBUTED_BACKEND` | Backend type: `ring` or `jaccl` (default: ring) |

## Troubleshooting

- **All ranks must have the model downloaded.** MLX distributed does not transfer model weights over the network. Ensure each node has the model in its Hugging Face cache.
- **Timeout errors:** If ranks can't connect, check firewall rules. The Ring backend uses TCP on the ports listed in the hostfile.
- **Rank assignment:** In P2P mode, rank 0 is always the LocalAI server. Worker ranks are assigned by sorting node IDs.
- **Performance:** Pipeline parallelism adds latency proportional to the number of ranks. For best results, use the fewest ranks needed to fit your model in memory.
