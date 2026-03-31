+++
disableToc = false
title = "Distributed Mode"
weight = 14
url = "/features/distributed-mode/"
+++

Distributed mode enables horizontal scaling of LocalAI across multiple machines using **PostgreSQL** for state and node registry, and **NATS** for real-time coordination. Unlike the [P2P/federation approach](/features/distribute/), distributed mode is designed for production deployments and Kubernetes environments where you need centralized management, health monitoring, and deterministic routing.

{{% notice note %}}
Distributed mode requires authentication enabled with a **PostgreSQL** database — SQLite is not supported. This is because the node registry, job store, and other distributed state are stored in PostgreSQL tables.
{{% /notice %}}

## Architecture Overview

```
                    ┌─────────────────┐
                    │   Load Balancer  │
                    └────────┬────────┘
                             │
              ┌──────────────┼──────────────┐
              │              │              │
      ┌───────▼──────┐ ┌────▼─────┐ ┌─────▼──────┐
      │  Frontend #1 │ │ Frontend │ │ Frontend #N│
      │  (LocalAI)   │ │  #2      │ │  (LocalAI) │
      └──────┬───────┘ └────┬─────┘ └─────┬──────┘
             │              │              │
     ┌───────▼──────────────▼──────────────▼───────┐
     │              PostgreSQL + NATS               │
     │  (node registry, jobs, coordination)         │
     └───────┬──────────────┬──────────────┬───────┘
             │              │              │
      ┌──────▼──────┐ ┌────▼─────┐ ┌─────▼──────┐
      │  Worker #1  │ │ Worker   │ │ Worker #N  │
      │  (generic)  │ │ #2       │ │  (generic) │
      └─────────────┘ └──────────┘ └────────────┘
```

**Frontends** are stateless LocalAI instances that receive API requests and route them to worker nodes via the **SmartRouter**. All frontends share state through PostgreSQL and coordinate via NATS.

**Workers** are generic processes that self-register with a frontend. They don't have a fixed backend type — the SmartRouter dynamically installs the required backend via NATS `backend.install` events when a model request arrives.

### Scheduling Algorithm

The SmartRouter uses **idle-first** scheduling with **preemptive eviction**:
1. If the model is already loaded on a node → use it (per-model gRPC address)
2. If no node has the model → prefer nodes with enough free VRAM
3. Fall back to idle nodes (zero models), then least-loaded nodes
4. If no node has capacity → **evict the least-recently-used model with zero in-flight requests** to free a node
5. If all models are busy → wait (with timeout) for a model to become idle, then evict
6. Send `backend.install` NATS event with backend name + model ID → worker starts a new gRPC process on a dynamic port
7. SmartRouter calls gRPC `LoadModel` on the model-specific port, records in DB

Each model gets its own gRPC backend process, so a single worker can serve multiple models simultaneously (e.g., a chat model and an embedding model).

## Prerequisites

- **PostgreSQL** (with pgvector extension recommended for RAG) — used for node registry, job store, auth, and shared state
- **NATS** server — used for real-time backend lifecycle events and file staging
- All services must be on the same network (or reachable via configured URLs)

## Quick Start with Docker Compose

The easiest way to try distributed mode locally is with the provided Docker Compose file:

```bash
docker compose -f docker-compose.distributed.yaml up
```

This starts PostgreSQL, NATS, a LocalAI frontend, and one worker node. When you send an inference request, the SmartRouter automatically installs the needed backend on the worker and loads the model. See the file for details on adding GPU support, shared volumes, and additional workers.

{{% notice tip %}}
Use `docker-compose.distributed.yaml` for quick local testing. For production, deploy PostgreSQL and NATS as managed services and run frontends/workers on separate hosts.
{{% /notice %}}

## Frontend Configuration

The frontend is a standard LocalAI instance with distributed mode enabled. These flags are added to the `local-ai run` command:

| Flag | Env Var | Default | Description |
|------|---------|---------|-------------|
| `--distributed` | `LOCALAI_DISTRIBUTED` | `false` | Enable distributed mode |
| `--instance-id` | `LOCALAI_INSTANCE_ID` | auto UUID | Unique instance ID for this frontend |
| `--nats-url` | `LOCALAI_NATS_URL` | *(required)* | NATS server URL (e.g., `nats://localhost:4222`) |
| `--registration-token` | `LOCALAI_REGISTRATION_TOKEN` | *(empty)* | Token that workers must provide to register |
| `--auto-approve-nodes` | `LOCALAI_AUTO_APPROVE_NODES` | `false` | Auto-approve new worker nodes (skip admin approval) |
| `--auth` | `LOCALAI_AUTH` | `false` | **Must be `true`** for distributed mode |
| `--auth-database-url` | `LOCALAI_AUTH_DATABASE_URL` | *(required)* | PostgreSQL connection URL |

### Optional: S3 Object Storage

For multi-host deployments where workers don't share a filesystem, S3-compatible storage enables distributed file transfer (model files, configs):

| Flag | Env Var | Default | Description |
|------|---------|---------|-------------|
| `--storage-url` | `LOCALAI_STORAGE_URL` | *(empty)* | S3 endpoint URL (e.g., `http://minio:9000`) |
| `--storage-bucket` | `LOCALAI_STORAGE_BUCKET` | `localai` | S3 bucket name |
| `--storage-region` | `LOCALAI_STORAGE_REGION` | `us-east-1` | S3 region |
| `--storage-access-key` | `LOCALAI_STORAGE_ACCESS_KEY` | *(empty)* | S3 access key |
| `--storage-secret-key` | `LOCALAI_STORAGE_SECRET_KEY` | *(empty)* | S3 secret key |

When S3 is not configured, model files are transferred directly from the frontend to workers via **HTTP** — no shared filesystem needed. Each worker runs a small HTTP file transfer server alongside the gRPC backend process. This is the default and works out of the box.

For high-throughput or very large model files, S3 can be more efficient since it avoids streaming through the frontend.

## Worker Configuration

Workers are started with the `worker` subcommand. Each worker is generic — it doesn't need a backend type at startup:

```bash
local-ai worker \
  --register-to http://frontend:8080 \
  --registration-token changeme \
  --nats-url nats://nats:4222
```

| Flag | Env Var | Default | Description |
|------|---------|---------|-------------|
| `--addr` | `LOCALAI_SERVE_ADDR` | `0.0.0.0:50051` | gRPC listen address |
| `--advertise-addr` | `LOCALAI_ADVERTISE_ADDR` | *(auto)* | Address the frontend uses to reach this node (see below) |
| `--http-addr` | `LOCALAI_HTTP_ADDR` | gRPC port - 1 | HTTP file transfer server bind address |
| `--advertise-http-addr` | `LOCALAI_ADVERTISE_HTTP_ADDR` | *(auto)* | HTTP address the frontend uses for file transfer |
| `--register-to` | `LOCALAI_REGISTER_TO` | *(required)* | Frontend URL for self-registration |
| `--node-name` | `LOCALAI_NODE_NAME` | hostname | Human-readable node name |
| `--registration-token` | `LOCALAI_REGISTRATION_TOKEN` | *(empty)* | Token to authenticate with the frontend |
| `--heartbeat-interval` | `LOCALAI_HEARTBEAT_INTERVAL` | `10s` | Interval between heartbeat pings |
| `--nats-url` | `LOCALAI_NATS_URL` | *(required)* | NATS URL for backend installation and file staging |
| `--backends-path` | `LOCALAI_BACKENDS_PATH` | `./backends` | Path to backend binaries |
| `--models-path` | `LOCALAI_MODELS_PATH` | `./models` | Path to model files |

{{% notice tip %}}
**Advertise address:** The `--addr` flag is the local bind address for gRPC. The `--advertise-addr` is the address the frontend stores and uses to reach the worker via gRPC. If not set, the worker auto-derives it by replacing `0.0.0.0` with the OS hostname (which in Docker is the container ID, resolvable via Docker DNS). Set `--advertise-addr` explicitly when the auto-detected hostname is not routable from the frontend (e.g., in Kubernetes, use the pod's service DNS name).

**HTTP file transfer:** Each worker also runs a small HTTP server for file transfer (model files, configs). By default it listens on the gRPC base port - 1 (e.g., if gRPC base is 50051, HTTP is on 50050). gRPC ports grow upward from the base port as additional models are loaded. Set `--advertise-http-addr` if the auto-detected address is not routable from the frontend.
{{% /notice %}}

### Worker Address Configuration

The simplest way to configure a worker's network address is with a single variable:

| Variable | Description |
|----------|-------------|
| `LOCALAI_ADDR` | Reachable address of this worker (`host:port`). The port is used as the base for gRPC backend processes, and `port-1` for the HTTP file transfer server. |

**Example:**
```yaml
environment:
  LOCALAI_ADDR: "192.168.1.100:50051"
  LOCALAI_NATS_URL: "nats://frontend:4222"
  LOCALAI_REGISTER_TO: "http://frontend:8080"
  LOCALAI_REGISTRATION_TOKEN: "my-secret"
```

For advanced networking scenarios (NAT, load balancers, separate gRPC/HTTP ports), the following override variables are available:

| Variable | Description | Default |
|----------|-------------|---------|
| `LOCALAI_SERVE_ADDR` | gRPC base port bind address | `0.0.0.0:50051` |
| `LOCALAI_HTTP_ADDR` | HTTP file transfer bind address | `0.0.0.0:{gRPC port - 1}` |
| `LOCALAI_ADVERTISE_ADDR` | Public gRPC address (if different from `LOCALAI_ADDR`) | Derived from `LOCALAI_ADDR` |
| `LOCALAI_ADVERTISE_HTTP_ADDR` | Public HTTP address (if different from gRPC host) | Derived from advertise host + HTTP port |

### Node Labels

Workers can declare labels at startup for scheduling constraints:

| Variable | Description | Example |
|----------|-------------|---------|
| `LOCALAI_NODE_LABELS` | Comma-separated `key=value` labels | `tier=premium,gpu=a100,zone=us-east` |

Labels can also be managed via the admin API (see [Label Management API](#label-management-api) below).

The system automatically applies hardware-detected labels on registration:
- `gpu.vendor` -- GPU vendor (nvidia, amd, intel, vulkan)
- `gpu.vram` -- GPU VRAM bucket (8GB, 16GB, 24GB, 48GB, 80GB+)
- `node.name` -- The node's registered name

### How Workers Operate

Workers start as generic processes with no backend installed. When the SmartRouter needs to load a model on a worker, it sends a NATS `backend.install` event with the backend name and model ID. The worker:

1. Installs the backend from the gallery (if not already installed)
2. Starts a **new gRPC backend process on a dynamic port** (each model gets its own process)
3. Replies with the allocated gRPC address
4. The SmartRouter calls `LoadModel` via direct gRPC to that address

Workers can run **multiple models concurrently** — each model gets its own gRPC process on a separate port. For example, an embedding model on port 50051 and a chat model on port 50052 can run simultaneously on the same worker.

When the SmartRouter needs to free capacity, it can unload models with zero in-flight requests without affecting other models on the same worker.

## Node Management API

The API is split into two prefixes with distinct auth:

### `/api/node/` — Node self-service

Used by workers themselves (registration, heartbeat, etc.). Authenticated via the registration token, exempt from global auth.

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/api/node/register` | Register a new worker |
| `POST` | `/api/node/:id/heartbeat` | Update heartbeat timestamp |
| `POST` | `/api/node/:id/drain` | Mark self as draining |
| `GET` | `/api/node/:id/models` | Query own loaded models |
| `DELETE` | `/api/node/:id` | Deregister self |

### `/api/nodes/` — Admin management

Used by the WebUI and admin API consumers. Requires admin authentication.

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/nodes` | List all registered workers |
| `GET` | `/api/nodes/:id` | Get a single worker by ID |
| `GET` | `/api/nodes/:id/models` | List models loaded on a worker |
| `DELETE` | `/api/nodes/:id` | Admin-delete a worker |
| `POST` | `/api/nodes/:id/drain` | Admin-drain a worker |
| `POST` | `/api/nodes/:id/approve` | Approve a pending worker node |
| `POST` | `/api/nodes/:id/backends/install` | Install a backend on a worker |
| `POST` | `/api/nodes/:id/backends/delete` | Delete a backend from a worker |
| `POST` | `/api/nodes/:id/models/unload` | Unload a model from a worker |
| `POST` | `/api/nodes/:id/models/delete` | Delete model files from a worker |

The **Nodes** page in the React WebUI provides a visual overview of all registered workers, their statuses, and loaded models.

## Node Approval

By default, new worker nodes start in **pending** status and must be approved by an admin before they can receive traffic. This prevents unknown machines from joining the cluster.

To approve a pending node via the API:

```bash
curl -X POST http://frontend:8080/api/nodes/<node-id>/approve \
  -H "Authorization: Bearer <admin-token>"
```

The **Nodes** page in the WebUI also shows pending nodes with an **Approve** button.

To skip manual approval and let nodes join immediately, set `--auto-approve-nodes` (or `LOCALAI_AUTO_APPROVE_NODES=true`) on the frontend. This is convenient for development and trusted environments.

## Node Statuses

| Status | Meaning |
|--------|---------|
| `pending` | Node registered but waiting for admin approval (when `--auto-approve-nodes` is `false`) |
| `healthy` | Node is active and responding to heartbeats |
| `unhealthy` | Node has missed heartbeats beyond the threshold (detected by the HealthMonitor) |
| `offline` | Node is temporarily offline (graceful shutdown or stale heartbeat). The node row is preserved so re-registration restores the previous approval status without requiring re-approval |
| `draining` | Node is shutting down gracefully — no new requests are routed to it, existing in-flight requests are allowed to complete |

## Agent Workers

Agent workers are dedicated processes for executing agent chats and MCP CI jobs. Unlike backend workers (which run gRPC model inference), agent workers use cogito to orchestrate multi-step conversations with tool calls.

```bash
local-ai agent-worker \
  --register-to http://frontend:8080 \
  --nats-url nats://nats:4222 \
  --registration-token changeme
```

Agent workers:
- Execute agent chat messages dispatched via NATS
- Run MCP CI jobs (with access to MCP servers via docker)
- Handle MCP tool discovery and execution requests from the frontend
- Get auto-provisioned API keys during registration for calling the inference API

In the docker-compose setup, the agent worker mounts the Docker socket so it can run MCP stdio servers (e.g., `docker run` commands):

```yaml
agent-worker-1:
  command: agent-worker
  volumes:
    - /var/run/docker.sock:/var/run/docker.sock
```

## MCP in Distributed Mode

MCP servers configured in model configs work in distributed mode. The frontend routes MCP operations through NATS to agent workers:

- **MCP discovery** (`GET /v1/mcp/servers/:model`): routed to agent workers which create sessions and return server info
- **MCP tool execution** (during `/v1/chat/completions`): tool calls are routed to agent workers via NATS request-reply
- **MCP CI jobs**: executed entirely on agent workers with access to docker for stdio-based MCP servers

## Scaling

**Adding worker capacity:** Start additional `worker` instances pointing to the same frontend. They self-register automatically:

```bash
# Additional workers — no backend type needed
local-ai worker \
  --register-to http://frontend:8080 \
  --node-name worker-2 \
  --nats-url nats://nats:4222 \
  --registration-token changeme

local-ai worker \
  --register-to http://frontend:8080 \
  --node-name worker-3 \
  --nats-url nats://nats:4222 \
  --registration-token changeme
```

**Multiple frontend replicas:** Run multiple LocalAI frontends behind a load balancer. Since all state is in PostgreSQL and coordination is via NATS, frontends are fully stateless and interchangeable.

## Model Scheduling

Model scheduling controls where models are placed and how many replicas are maintained. It combines two optional features:

### Node Selectors

Pin models to nodes with specific labels. Only nodes matching **all** selector labels are eligible:

```bash
# Only schedule on NVIDIA nodes in the us-east zone
curl -X POST http://frontend:8080/api/nodes/scheduling \
  -H "Content-Type: application/json" \
  -d '{"model_name": "llama3", "node_selector": {"gpu.vendor": "nvidia", "zone": "us-east"}}'
```

Without a node selector, models can schedule on any healthy node (default behavior).

### Replica Auto-Scaling

Control the number of model replicas across the cluster:

| Field | Description |
|-------|-------------|
| `min_replicas` | Minimum replicas to maintain (0 = no minimum, single replica default) |
| `max_replicas` | Maximum replicas allowed (0 = unlimited) |

Auto-scaling is **only active** when `min_replicas > 0` or `max_replicas > 0`.

```bash
# Scale llama3 between 2 and 4 replicas on NVIDIA nodes
curl -X POST http://frontend:8080/api/nodes/scheduling \
  -H "Content-Type: application/json" \
  -d '{
    "model_name": "llama3",
    "node_selector": {"gpu.vendor": "nvidia"},
    "min_replicas": 2,
    "max_replicas": 4
  }'
```

The **Replica Reconciler** runs as a background process on the frontend:
- **Scale up**: Adds replicas when all existing replicas are busy (have in-flight requests)
- **Scale down**: Removes idle replicas after 5 minutes of inactivity
- **Maintain minimum**: Ensures `min_replicas` are always loaded (recovers from node failures)
- **Eviction protection**: Models with auto-scaling enabled are never evicted below `min_replicas`

All fields are optional and composable:
- Node selector only: pin model to matching nodes, single replica
- Replicas only: auto-scale across all nodes
- Both: auto-scale on matching nodes only

## Label Management API

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/nodes/:id/labels` | Get labels for a node |
| `PUT` | `/api/nodes/:id/labels` | Replace all labels (JSON object) |
| `PATCH` | `/api/nodes/:id/labels` | Merge labels (add/update) |
| `DELETE` | `/api/nodes/:id/labels/:key` | Remove a single label |

## Scheduling API

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/nodes/scheduling` | List all scheduling configs |
| `GET` | `/api/nodes/scheduling/:model` | Get config for a model |
| `POST` | `/api/nodes/scheduling` | Create/update config |
| `DELETE` | `/api/nodes/scheduling/:model` | Remove config |

## Comparison with P2P

| | P2P / Federation | Distributed Mode |
|---|---|---|
| **Discovery** | Automatic via libp2p token | Self-registration to frontend URL |
| **State storage** | In-memory / ledger | PostgreSQL |
| **Coordination** | Gossip protocol | NATS messaging |
| **Node management** | Automatic | REST API + WebUI |
| **Health monitoring** | Peer heartbeats | Centralized HealthMonitor |
| **Backend management** | Manual per node | Dynamic via NATS backend.install |
| **Best for** | Ad-hoc clusters, community sharing | Production, Kubernetes, managed infrastructure |
| **Setup complexity** | Minimal (share a token) | Requires PostgreSQL + NATS |

## Troubleshooting

**Worker not registering:**
- Verify the frontend URL is reachable from the worker (`curl http://frontend:8080/api/node/register`)
- Check that `--registration-token` matches on both frontend and worker
- Ensure auth is enabled on the frontend (`LOCALAI_AUTH=true`)

**NATS connection errors:**
- Confirm NATS is running and reachable (`nats-server --signal ldm` or check port 4222)
- Check that `--nats-url` uses the correct hostname/IP from the worker's network perspective

**PostgreSQL connection errors:**
- Verify the connection URL format: `postgresql://user:password@host:5432/dbname?sslmode=disable`
- Ensure the database exists and the user has CREATE TABLE permissions (for auto-migration)
- Check that pgvector extension is installed if using RAG features

**Node shows as unhealthy or offline:**
- The HealthMonitor marks nodes offline when heartbeats are missed. Check network connectivity between worker and frontend.
- Verify `--heartbeat-interval` is not set too high
- Offline nodes automatically restore to healthy when they re-register (no re-approval needed)

**Backend not installing:**
- Check the worker logs for `backend.install` events

**Port conflicts on workers:**
- Each model gets its own gRPC process on an incrementing port (50051, 50052, ...)
- The HTTP file transfer server runs on the base port - 1 (default: 50050)
- Ensure the port range is not blocked by firewalls or used by other services
- Verify the backend gallery configuration is correct
- The worker needs network access to download backends from the gallery
