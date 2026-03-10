+++
disableToc = false
title = "🆕🖧 Distributed Inference"
weight = 15
url = "/features/distribute/"
+++

## Overview

This functionality enables LocalAI to distribute inference requests across multiple worker nodes, improving efficiency and performance. Nodes are automatically discovered and connect via p2p by using a shared token which makes sure the communication is secure and private between the nodes of the network.

LocalAI supports two modes of distributed inferencing via p2p:

- **Federated Mode**: Requests are shared between the cluster and routed to a single worker node in the network based on the load balancer's decision.
- **Worker Mode** (aka "model sharding" or "splitting weights"): Requests are processed by all the workers which contributes to the final inference result (by sharing the model weights).

A list of global instances shared by the community is available at [explorer.localai.io](https://explorer.localai.io).

## Architecture

### Federated Mode Architecture

```plaintext
                    ┌─────────────────────────────────────┐
                    │         Load Balancer              │
                    │     (local-ai federated)           │
                    └──────────────┬──────────────────────┘
                                   │
           ┌───────────────────────┼───────────────────────┐
           │                       │                       │
           ▼                       ▼                       ▼
    ┌──────────┐            ┌──────────┐            ┌──────────┐
    │  Node 1  │◄──────────►│  Node 2  │◄──────────►│  Node N  │
    │         │   p2p      │         │   p2p      │         │
    │ Model A │  gossip    │ Model B │  gossip    │ Model C │
    └──────────┘            └──────────┘            └──────────┘
        │                       │                       │
        └───────────────────────┴───────────────────────┘
                              │
                              ▼
                    ┌─────────────────┐
                    │   libp2p DHT    │
                    │ (discovery)     │
                    └─────────────────┘
```

**Data Flow:**
1. Client sends request to Load Balancer
2. Load Balancer queries available nodes via p2p gossip
3. Request routed to node with available capacity
4. Selected node processes entire request
5. Response returned to client

### Worker Mode (Model Sharding) Architecture

```plaintext
                    ┌─────────────────────────────────────┐
                    │          Main Server               │
                    │     (local-ai run --p2p)           │
                    └──────────────┬──────────────────────┘
                                   │
           ┌───────────────────────┼───────────────────────┐
           │                       │                       │
           ▼                       ▼                       ▼
    ┌──────────┐            ┌──────────┐            ┌──────────┐
    │ Worker 1 │◄──────────►│ Worker 2 │◄──────────►│ Worker N │
    │         │   p2p      │         │   p2p      │         │
    │  Layer  │  gossip    │  Layer  │  gossip    │  Layer  │
    │  1-10   │            │ 11-20   │            │  21-30  │
    └──────────┘            └──────────┘            └──────────┘
        │                       │                       │
        └───────────────────────┴───────────────────────┘
                              │
                              ▼
                    ┌─────────────────┐
                    │   Combined      │
                    │   Output        │
                    └─────────────────┘
```

**Data Flow:**
1. Model weights split proportionally based on worker memory
2. Each worker loads its portion of the model
3. Request forwarded through all workers sequentially
4. Final result aggregated at main server
5. Response returned to client

### Technology Stack

LocalAI uses https://github.com/libp2p/go-libp2p under the hood, the same project powering IPFS. Differently from other frameworks, LocalAI uses peer2peer without a single master server, but rather it uses sub/gossip and ledger functionalities to achieve consensus across different peers.

[EdgeVPN](https://github.com/mudler/edgevpn) is used as a library to establish the network and expose the ledger functionality under a shared token to ease out automatic discovery and have separated, private peer2peer networks.

## Usage

Starting LocalAI with `--p2p` generates a shared token for connecting multiple instances: and that's all you need to create AI clusters, eliminating the need for intricate network setups. 

Simply navigate to the "Swarm" section in the WebUI and follow the on-screen instructions.

For fully shared instances, initiate LocalAI with `--p2p --federated` and adhere to the Swarm section's guidance. This feature, while still experimental, offers a tech preview quality experience.

### Federated Mode

Federated mode allows to launch multiple LocalAI instances and connect them together in a federated network. This mode is useful when you want to distribute the load of the inference across multiple nodes, but you want to have a single point of entry for the API. In the Swarm section of the WebUI, you can see the instructions to connect multiple instances together.

![346663124-1d2324fd-8b55-4fa2-9856-721a467969c2](https://github.com/user-attachments/assets/19ebd44a-20ff-412c-b92f-cfb8efbe4b21)

To start a LocalAI server in federated mode, run:

```bash
local-ai run --p2p --federated
```

This will generate a token that you can use to connect other LocalAI instances to the network or others can use to join the network. If you already have a token, you can specify it using the `TOKEN` environment variable.

To start a load balanced server that routes the requests to the network, run with the `TOKEN`:

```bash
local-ai federated
```

To see all the available options, run `local-ai federated --help`.

The instructions are displayed in the "Swarm" section of the WebUI, guiding you through the process of connecting multiple instances.

### Worker Mode

{{% notice note %}}
This feature is available exclusively with llama-cpp compatible models.

This feature was introduced in [LocalAI pull request #2324](https://github.com/mudler/LocalAI/pull/2324) and is based on the upstream work in [llama.cpp pull request #6829](https://github.com/ggerganov/llama.cpp/pull/6829).
 {{% /notice %}}

To connect multiple workers to a single LocalAI instance, start first a server in p2p mode:

```bash
local-ai run --p2p
```

And navigate the WebUI to the "Swarm" section to see the instructions to connect multiple workers to the network.

![346663124-1d2324fd-8b55-4fa2-9856-721a467969c2](https://github.com/user-attachments/assets/b8cadddf-a467-49cf-a1ed-8850de95366d)

### Without P2P

To start workers for distributing the computational load, run:

```bash
local-ai worker llama-cpp-rpc --llama-cpp-args="-H <listening_address> -p <listening_port> -m <memory>" 
```

And you can specify the address of the workers when starting LocalAI with the `LLAMACPP_GRPC_SERVERS` environment variable:

```bash
LLAMACPP_GRPC_SERVERS="address1:port,address2:port" local-ai run
```
The workload on the LocalAI server will then be distributed across the specified nodes.

Alternatively, you can build the RPC workers/server following the llama.cpp [README](https://github.com/ggerganov/llama.cpp/blob/master/examples/rpc/README.md), which is compatible with LocalAI.


## Step-by-Step Setup Guide

### Prerequisites

#### Hardware Requirements

**Minimum for Single-Node Testing:**
- CPU: 4+ cores
- RAM: 16GB minimum
- Storage: 10GB free space
- Network: 1Gbps Ethernet

**Recommended for Production:**
- CPU: 8+ cores per node
- RAM: 32GB+ per node (or more based on model size)
- GPU: Optional but recommended (NVIDIA with CUDA, AMD with ROCm, Apple Silicon)
- Network: 10Gbps recommended for worker mode

#### Software Requirements

- LocalAI binary (latest version)
- Linux, macOS, or Windows with WSL2
- Docker (optional, for containerized deployment)
- Model files (GGUF format recommended)

### Single-Node Setup (Local Testing)

1. **Start LocalAI with p2p enabled:**

```bash
local-ai run --p2p --models-dir ./models
```

2. **Copy the generated token** from the WebUI "Swarm" section or via API:

```bash
curl http://localhost:8000/p2p/token
```

3. **Start a worker on the same machine:**

```bash
TOKEN="your-token-here" local-ai worker p2p-llama-cpp-rpc --llama-cpp-args="-m 4096"
```

4. **Verify connection** - Check the main server logs for worker discovery messages.

### Multi-Node Setup (3-Node Example)

#### Step 1: Set Up the Main Server (Node 1)

```bash
# On Node 1 (main server)
local-ai run --p2p --federated --models-dir /opt/models
```

Copy the generated token - you'll distribute this to all workers.

#### Step 2: Configure Workers (Nodes 2, 3)

```bash
# On Node 2
TOKEN="your-shared-token" local-ai worker p2p-llama-cpp-rpc --llama-cpp-args="-m 8192"

# On Node 3
TOKEN="your-shared-token" local-ai worker p2p-llama-cpp-rpc --llama-cpp-args="-m 8192"
```

#### Step 3: Start Load Balancer (Optional - on Node 1)

```bash
TOKEN="your-shared-token" local-ai federated --host 0.0.0.0 --port 8080
```

#### Step 4: Test the Setup

```bash
# Query through the load balancer
curl http://node1:8080/completion -d '{
  "prompt": "Hello, world!",
  "model": "llama-3"
}'
```

### Docker Compose Deployment

```yaml
version: '3.8'

services:
  localai-server:
    image: localai/localai:latest
    container_name: localai-main
    ports:
      - "8080:8080"
    volumes:
      - ./models:/models
      - ./config:/config
    environment:
      - LOCALAI_P2P=true
      - LOCALAI_FEDERATED=true
    networks:
      - localai-network
    restart: unless-stopped

  worker-1:
    image: localai/localai:latest
    container_name: localai-worker-1
    command: ["worker", "p2p-llama-cpp-rpc", "--llama-cpp-args=-m 8192"]
    environment:
      - TOKEN=${P2P_TOKEN}
    volumes:
      - ./models:/models
    networks:
      - localai-network
    depends_on:
      - localai-server
    restart: unless-stopped

  worker-2:
    image: localai/localai:latest
    container_name: localai-worker-2
    command: ["worker", "p2p-llama-cpp-rpc", "--llama-cpp-args=-m 8192"]
    environment:
      - TOKEN=${P2P_TOKEN}
    volumes:
      - ./models:/models
    networks:
      - localai-network
    depends_on:
      - localai-server
    restart: unless-stopped

  federated-lb:
    image: localai/localai:latest
    container_name: localai-federated
    command: ["federated"]
    ports:
      - "8081:8080"
    environment:
      - TOKEN=${P2P_TOKEN}
    networks:
      - localai-network
    depends_on:
      - localai-server
    restart: unless-stopped

networks:
  localai-network:
    driver: bridge
```

### Kubernetes Deployment

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: localai-config
data:
  P2P_TOKEN: "your-shared-token"
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: localai-server
spec:
  replicas: 1
  selector:
    matchLabels:
      app: localai-server
  template:
    metadata:
      labels:
        app: localai-server
    spec:
      containers:
      - name: server
        image: localai/localai:latest
        ports:
        - containerPort: 8080
        env:
        - name: LOCALAI_P2P
          value: "true"
        - name: LOCALAI_FEDERATED
          value: "true"
        volumeMounts:
        - name: models
          mountPath: /models
      volumes:
      - name: models
        emptyDir: {}
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: localai-workers
spec:
  replicas: 3
  selector:
    matchLabels:
      app: localai-worker
  template:
    metadata:
      labels:
        app: localai-worker
    spec:
      containers:
      - name: worker
        image: localai/localai:latest
        command: ["local-ai", "worker", "p2p-llama-cpp-rpc"]
        args: ["--llama-cpp-args=-m 8192"]
        env:
        - name: TOKEN
          valueFrom:
            configMapKeyRef:
              name: localai-config
              key: P2P_TOKEN
        resources:
          limits:
            memory: "16Gi"
            cpu: "4"
---
apiVersion: v1
kind: Service
metadata:
  name: localai-service
spec:
  selector:
    app: localai-server
  ports:
  - port: 8080
    targetPort: 8080
  type: LoadBalancer
```


## Configuration Reference

### Command-Line Options

| Option | Description | Default | Example |
|--------|-------------|---------|----------|
| `--p2p` | Enable p2p networking | false | `--p2p` |
| `--p2ptoken` | Specify p2p token | generated | `--p2ptoken mytoken` |
| `--federated` | Enable federated mode | false | `--federated` |
| `--federated-token` | Token for federated mode | from env | `--federated-token tkn` |
| `--federated-host` | Host for federated server | 127.0.0.1 | `--federated-host 0.0.0.0` |
| `--federated-port` | Port for federated server | 8080 | `--federated-port 9090` |

### Environment Variables

| Environment Variable | Description | Default | Type |
|----------------------|-------------|---------|------|
| **LOCALAI_P2P** | Set to "true" to enable p2p | false | boolean |
| **LOCALAI_FEDERATED** | Set to "true" to enable federated mode | false | boolean |
| **FEDERATED_SERVER** | Set to "true" to enable federated server | false | boolean |
| **LOCALAI_P2P_DISABLE_DHT** | Disable DHT for local-only p2p (mDNS) | false | boolean |
| **LOCALAI_P2P_ENABLE_LIMITS** | Enable connection limits and resource management | false | boolean |
| **LOCALAI_P2P_LISTEN_MADDRS** | Custom libp2p listen multiaddresses | 0.0.0.0/* | string |
| **LOCALAI_P2P_DHT_ANNOUNCE_MADDRS** | Override announced listen multiaddresses | auto | string |
| **LOCALAI_P2P_BOOTSTRAP_PEERS_MADDRS** | Custom DHT bootstrap nodes | community | string |
| **LOCALAI_P2P_TOKEN** | Set the p2p network token | generated | string |
| **LOCALAI_P2P_LOGLEVEL** | Log level for p2p stack | info | string |
| **LOCALAI_P2P_LIB_LOGLEVEL** | Log level for libp2p stack | fatal | string |
| **LLAMACPP_GRPC_SERVERS** | Comma-separated worker addresses | none | string |

### Configuration Examples

#### Basic P2P Setup

```bash
export LOCALAI_P2P=true
export LOCALAI_P2P_TOKEN="my-secret-token"
local-ai run
```

#### Advanced Federated Setup

```bash
export LOCALAI_P2P=true
export LOCALAI_FEDERATED=true
export LOCALAI_P2P_TOKEN="federated-token"
export LOCALAI_P2P_ENABLE_LIMITS=true
export LOCALAI_P2P_LOGLEVEL=debug
local-ai run --models-dir /opt/models
```

#### Worker with Custom Memory

```bash
export TOKEN="federated-token"
local-ai worker p2p-llama-cpp-rpc --llama-cpp-args="-m 16384 -t 8"
```

### Performance Tuning Parameters

| Parameter | Description | Recommended Value |
|-----------|-------------|-------------------|
| Memory (`-m`) | Memory allocation for model | 4096-32768 based on model size |
| Threads (`-t`) | CPU threads per worker | Number of physical cores |
| Batch size | Requests processed together | 1-8 depending on latency needs |
| Context window | Token context size | 2048-8192 based on use case |

## Load Balancing Strategies

### Available Strategies

#### 1. Round-Robin (Default)

Requests are distributed evenly across all available nodes.

```bash
local-ai federated --strategy round-robin
```

**Best for:** Similar hardware, uniform model sizes

#### 2. Weighted Distribution

Nodes can be weighted based on capacity.

```bash
# Configure weights via API or config file
{
  "nodes": {
    "node1": {"weight": 2},
    "node2": {"weight": 1}
  }
}
```

**Best for:** Mixed hardware configurations

#### 3. Least Connections

Routes to node with fewest active connections.

```bash
local-ai federated --strategy least-connections
```

**Best for:** Variable request durations

#### 4. Model-Aware Routing

Routes based on which models are available on each node.

```bash
local-ai federated --strategy model-aware
```

**Best for:** Federated deployments with different models per node

### Health Check Configuration

```bash
local-ai federated \
  --health-check-interval 30s \
  --health-check-timeout 5s \
  --unhealthy-threshold 3
```

## Network Requirements

### Required Ports

| Port | Protocol | Purpose | Direction |
|------|----------|---------|-----------|
| 4001 | TCP/UDP | libp2p discovery (DHT) | Inbound/Outbound |
| 4002 | TCP/UDP | libp2p communication | Inbound/Outbound |
| 8080 | TCP | HTTP API | Inbound |
| 50051 | TCP | gRPC (worker mode) | Inbound/Outbound |
| 8545 | TCP | JSON-RPC (optional) | Inbound/Outbound |

### Firewall Rules

#### iptables Example

```bash
# Allow libp2p traffic
iptables -A INPUT -p tcp --dport 4001:4002 -j ACCEPT
iptables -A INPUT -p udp --dport 4001:4002 -j ACCEPT

# Allow HTTP API
iptables -A INPUT -p tcp --dport 8080 -j ACCEPT

# Allow gRPC for worker mode
iptables -A INPUT -p tcp --dport 50051 -j ACCEPT

# Allow outbound for DHT
iptables -A OUTPUT -p tcp --dport 4001:4002 -j ACCEPT
iptables -A OUTPUT -p udp --dport 4001:4002 -j ACCEPT
```

#### ufw Example

```bash
# Enable firewall
ufw enable

# Allow p2p traffic
ufw allow 4001:4002/tcp
ufw allow 4001:4002/udp

# Allow API
ufw allow 8080/tcp

# Allow gRPC
ufw allow 50051/tcp
```

### Network Topology Recommendations

#### LAN Deployment

```
                    ┌─────────────┐
                    │   Router    │
                    └──────┬──────┘
                           │
         ┌─────────────────┼─────────────────┐
         │                 │                 │
    ┌────▼────┐       ┌────▼────┐       ┌────▼────┐
    │ Node 1  │       │ Node 2  │       │ Node 3  │
    │ (LB)    │◄─────►│ Worker  │◄─────►│ Worker  │
    └─────────┘  1Gbps└─────────┘  1Gbps└─────────┘
```

**Recommendations:**
- Use static IPs or DNS names
- Ensure mDNS is enabled (port 5353)
- Use 1Gbps minimum, 10Gbps recommended for worker mode

#### WAN/Cloud Deployment

```
    ┌──────────────────────────────────────────────┐
    │              Cloud Provider                  │
    │  ┌─────────────────────────────────────┐     │
    │  │         VPC / Private Network       │     │
    │  │  ┌─────────┐    ┌─────────┐        │     │
    │  │  │  Node 1 │◄──►│  Node 2 │        │     │
    │  │  └────┬────┘    └─────────┘        │     │
    │  │       │                            │     │
    │  │  ┌────▼────────────────────────┐   │     │
    │  │  │      Load Balancer          │   │     │
    │  │  │    (Application LB)         │   │     │
    │  │  └──────────────┬───────────────┘   │     │
    │  └─────────────────┼───────────────────┘     │
    │                    │                         │
    └────────────────────┼─────────────────────────┘
                         │
                    ┌────▼────┐
                    │ Clients │
                    └─────────┘
```

**Recommendations:**
- Use VPC peering for cross-region setups
- Configure security groups for p2p ports
- Consider NAT gateway for outbound DHT traffic
- Use internal load balancers when possible

### NAT Traversal

LocalAI uses libp2p's built-in NAT traversal. For production setups behind strict NAT:

```bash
# Configure announced addresses
export LOCALAI_P2P_DHT_ANNOUNCE_MADDRS="/ip4/<public-ip>/tcp/4001"
```


## Performance Optimization

### GPU Memory Sharding

In worker mode, model weights are split proportionally to available memory:

```bash
# Worker with 8GB GPU memory allocation
local-ai worker p2p-llama-cpp-rpc --llama-cpp-args="-m 8192 --n-gpu-layers 30"

# Worker with 16GB GPU memory allocation
local-ai worker p2p-llama-cpp-rpc --llama-cpp-args="-m 16384 --n-gpu-layers 50"
```

**Tips:**
- Calculate layers per GPU based on model size
- Use `--n-gpu-layers` to control GPU vs CPU split
- Monitor memory with `nvidia-smi` or `rocm-smi`

### Network Latency Considerations

| Network Type | Recommended Mode | Notes |
|--------------|------------------|-------|
| LAN (1Gbps) | Both | Worker mode viable for medium models |
| LAN (10Gbps) | Worker mode optimal | Near single-node performance |
| WAN (<100ms) | Federated mode | Avoid worker mode |
| WAN (>100ms) | Federated mode only | High latency breaks worker mode |

### Batch Processing

For high-throughput scenarios:

```bash
# Enable batching on server
local-ai run --p2p --federated --max-ctx-size 8192 --num-threads 16
```

### Model Warmup Strategy

Pre-warm workers before accepting traffic:

```bash
# Start workers
local-ai worker p2p-llama-cpp-rpc --llama-cpp-args="-m 8192"

# Wait for discovery, then warmup
curl http://localhost:8080/completion -d '{
  "prompt": "warmup",
  "n_predict": 1
}'

# Now ready for production traffic
```

### Resource Monitoring

```bash
# Check connected workers
curl http://localhost:8080/p2p/peers

# Check federated status
curl http://localhost:8080/federated/status

# System monitoring
watch -n 1 'nvidia-smi --query-gpu=temperature,memory.total,memory.used --format=csv'
```

## Common Use Cases

### Use Case 1: Large Model That Doesn't Fit on Single GPU

**Scenario:** Running Llama-3-70B on 4x 24GB GPUs

```bash
# Node 1 - Main server
local-ai run --p2p --models-dir /models/llama-3-70b

# Workers (each handles ~17.5B parameters)
TOKEN="shared-token" local-ai worker p2p-llama-cpp-rpc \
  --llama-cpp-args="-m 24576 --n-gpu-layers 60"

# Repeat for all workers
```

**Expected Results:**
- Model split across 4 GPUs
- Each GPU uses ~18-20GB VRAM
- Near single-GPU latency

### Use Case 2: High-Throughput Production API

**Scenario:** Serving 100+ concurrent users with Llama-3-8B

```bash
# Load balancer
local-ai federated --strategy least-connections \
  --health-check-interval 10s

# Multiple nodes with same model
local-ai run --p2p --federated \
  --max-ctx-size 4096 \
  --num-threads 32
```

**Expected Results:**
- Requests distributed across nodes
- Horizontal scaling by adding nodes
- Automatic failover

### Use Case 3: Multi-Model Federated Deployment

**Scenario:** Different models on different nodes for cost optimization

```
Node 1 (GPU): Llama-3-70B for complex queries
Node 2 (GPU): Llama-3-8B for general queries  
Node 3 (CPU): Distil models for simple tasks
```

```bash
# Configure model-aware routing
local-ai federated --strategy model-aware

# Each node runs its specialized model
local-ai run --p2p --federated --models-dir /path/to/specific/models
```

**Expected Results:**
- Cost-effective routing
- Model selection based on query complexity
- Optimized resource utilization

### Use Case 4: Multi-Tenant Deployment

**Scenario:** Isolating tenants while sharing infrastructure

```yaml
# docker-compose.yml
version: '3.8'
services:
  tenant-a-server:
    image: localai/localai:latest
    environment:
      - LOCALAI_P2P_TOKEN=tenant-a-token
    networks: [tenant-a-network]
    
  tenant-b-server:
    image: localai/localai:latest
    environment:
      - LOCALAI_P2P_TOKEN=tenant-b-token
    networks: [tenant-b-network]

networks:
  tenant-a-network: {}
  tenant-b-network: {}
```

### Use Case 5: Cost-Effective Mixed Hardware

**Scenario:** Combining high-end and budget hardware

```bash
# High-end node (weighted higher)
local-ai run --p2p --federated

# Budget node (CPU-only, weighted lower)
local-ai worker p2p-llama-cpp-rpc \
  --llama-cpp-args="-m 4096 -t 8"
```

Configure weights:
```json
{
  "load_balancing": {
    "strategy": "weighted",
    "weights": {
      "high-end-node": 3,
      "budget-node": 1
    }
  }
}
```


## Troubleshooting

### Connection Issues

#### Workers Not Being Discovered

**Symptoms:** Main server logs don't show worker discovery messages

**Diagnosis:**
```bash
# Check if p2p is enabled
curl http://localhost:8080/p2p/peers

# Check token consistency
curl http://localhost:8080/p2p/token
```

**Solutions:**
1. Verify same token on all nodes
2. Check firewall allows ports 4001-4002
3. Ensure mDNS is enabled (port 5353)
4. Try disabling DHT for local networks:
   ```bash
   export LOCALAI_P2P_DISABLE_DHT=true
   ```

#### Token Synchronization Issues

**Symptoms:** Workers connect then disconnect

**Solutions:**
```bash
# Use persistent token
export LOCALAI_P2P_TOKEN="your-fixed-token"

# Or use file-based token
echo "mytoken" > /tmp/p2p-token
export LOCALAI_P2P_TOKEN_FILE=/tmp/p2p-token
```

### Common Error Messages

| Error | Cause | Solution |
|-------|-------|----------|
| "failed to connect to peer" | Network/firewall | Check ports 4001-4002 |
| "token mismatch" | Wrong token | Verify TOKEN env var |
| "worker timeout" | Network latency | Reduce timeout or improve network |
| "memory allocation failed" | Insufficient RAM | Reduce `-m` parameter |
| "gRPC connection refused" | Worker not started | Start worker first |

### Log Analysis

```bash
# Enable verbose logging
export LOCALAI_P2P_LOGLEVEL=debug
export LOCALAI_P2P_LIB_LOGLEVEL=debug

# Run with debug
local-ai run --p2p --federated
```

**Key log messages:**
- `"discovered peer"` - Worker found
- `"connected to peer"` - Connection established
- `"peer disconnected"` - Worker offline

### Debugging Commands

```bash
# Check peer connections
curl http://localhost:8080/p2p/peers

# Get network info
local-ai run --p2p --help

# Test worker connectivity
curl -v http://worker-ip:50051

# Check DHT status
curl http://localhost:8080/p2p/dht
```

### Performance Issues

**Slow Inference:**
1. Check network latency between workers
2. Reduce batch size
3. Use worker mode only on low-latency networks

**High Memory Usage:**
1. Reduce context window
2. Lower `-m` parameter on workers
3. Use model quantization

## Security Considerations

### Token Security

**Best Practices:**

```bash
# Generate strong token
openssl rand -hex 32

# Store securely
export LOCALAI_P2P_TOKEN="${P2P_TOKEN_FROM_VAULT}"

# Or use file with restricted permissions
echo "secure-token" > /etc/localai/p2p-token
chmod 600 /etc/localai/p2p-token
```

**Never:**
- Hardcode tokens in scripts
- Commit tokens to version control
- Use weak/predictable tokens

### Network Isolation

#### Docker Network Isolation

```yaml
version: '3.8'
services:
  localai:
    networks:
      - localai-internal

networks:
  localai-internal:
    internal: true  # No external access
    driver: bridge
```

#### Kubernetes Network Policies

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: localai-isolation
spec:
  podSelector:
    matchLabels:
      app: localai
  policyTypes:
  - Ingress
  - Egress
  ingress:
  - from:
    - podSelector:
        matchLabels:
          app: localai
  - from:
    - namespaceSelector:
        matchLabels:
          name: frontend
  egress:
  - to:
    - podSelector:
        matchLabels:
          app: localai
```

### Authentication in Federated Mode

For production deployments:

```bash
# Enable authentication
local-ai run --p2p --federated \
  --api-key YOUR_API_KEY

# Or via environment
export LOCALAI_API_KEY="your-api-key"
```

### Secure Communication Setup

```bash
# Use TLS for external-facing services
local-ai run \
  --tls-cert /etc/ssl/certs/localai.crt \
  --tls-key /etc/ssl/private/localai.key
```

## Manual Example (Worker Mode)

Use the WebUI to guide you in the process of starting new workers. This example shows the manual steps to highlight the process.

1. Start the server with `--p2p`:

```bash
./local-ai run --p2p
```

Copy the token from the WebUI or via API call (e.g., `curl http://localhost:8000/p2p/token`) and save it for later use.

To reuse the same token later, restart the server with `--p2ptoken` or `P2P_TOKEN`.

2. Start the workers. Copy the `local-ai` binary to other hosts and run as many workers as needed using the token:

```bash
TOKEN=XXX ./local-ai worker p2p-llama-cpp-rpc --llama-cpp-args="-m <memory>" 
```

(Note: You can also supply the token via command-line arguments)

The server logs should indicate that new workers are being discovered.

3. Start inference as usual on the server initiated in step 1.

![output](https://github.com/mudler/LocalAI/assets/2420543/8ca277cf-c208-4562-8929-808b2324b584)

## Notes

- If running in p2p mode with container images, make sure you start the container with `--net host` or `network_mode: host` in the docker-compose file.
- Only a single model is supported currently.
- Ensure the server detects new workers before starting inference. Currently, additional workers cannot be added once inference has begun.
- For more details on the implementation, refer to [LocalAI pull request #2343](https://github.com/mudler/LocalAI/pull/2343)

## Additional Resources

- [libp2p Documentation](https://docs.libp2p.io/)
- [EdgeVPN Repository](https://github.com/mudler/edgevpn)
- [llama.cpp RPC Examples](https://github.com/ggerganov/llama.cpp/blob/master/examples/rpc/README.md)
- [LocalAI WebUI Swarm Section](http://localhost:8080/swarm)

