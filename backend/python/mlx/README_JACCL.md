# MLX JACCL Cluster Support

This document describes the integration of RDMA (Remote Direct Memory Access) support with the MLX backend using mlx-jaccl-cluster.

## Overview

The MLX backend now supports distributed inference across multiple Apple Silicon machines using JACCL (RDMA over Thunderbolt). This enables:

- Running large models that exceed the memory of a single Mac
- Improved inference performance through distributed computation
- High-speed communication between Macs via Thunderbolt connections

## Configuration

### Environment Variables

To enable JACCL cluster mode, set the following environment variables:

```bash
export JACCL_CLUSTER=true
export JACCL_HOSTFILE=hostfiles/hosts.json
export JACCL_MODEL_DIR=/path/to/mlx/model
export JACCL_MODEL_ID=model-id
export JACCL_HTTP_HOST=0.0.0.0
export JACCL_HTTP_PORT=8080
```

### Hostfile Configuration

Create a hostfile at `hostfiles/hosts.json` based on the template:

```json
{
  "hosts": [
    {
      "ssh": "user@mac1.local",
      "ips": ["192.168.1.10"],
      "rdma": {
        "user@mac1.local": ["user@mac1.local"],
        "user@mac2.local": ["user@mac2.local"]
      }
    },
    {
      "ssh": "user@mac2.local",
      "ips": ["192.168.1.11"],
      "rdma": {
        "user@mac1.local": ["user@mac1.local"],
        "user@mac2.local": ["user@mac2.local"]
      }
    }
  ]
}
```

## Prerequisites

1. **RDMA Enabled**: Enable RDMA in macOS Recovery Mode:
   ```bash
   # Boot into Recovery Mode and run:
   rdma_ctl enable
   ```

2. **Thunderbolt Connection**: Connect Macs via Thunderbolt cables (fully connected mesh required for N nodes)

3. **MLX Environment**: Install MLX with JACCL support:
   ```bash
   conda create -n mlxjccl python=3.11
   conda activate mlxjccl
   pip install mlx-lm mlx-jaccl
   ```

4. **Model Availability**: Ensure the model is available on all nodes:
   ```bash
   # Download on rank0
   huggingface-cli download mlx-community/Qwen3-4B-Instruct-4bit \
     --local-dir ~/models_mlx/Qwen3-4B-Instruct-4bit
   
   # Sync to other nodes
   rsync -avz -e ssh ~/models_mlx/Qwen3-4B-Instruct-4bit/ user@mac2.local:/Users/yourusername/models_mlx/Qwen3-4B-Instruct-4bit/
   ```

## Usage

### Starting the Cluster Server

```bash
# On rank0, start the OpenAI-compatible server
MODEL_DIR=/path/to/model JACCL_CLUSTER=true python backend/python/mlx/backend.py
```

### Making Requests

Once the cluster is running, you can make requests via the gRPC backend or HTTP API:

```bash
# HTTP API (from any client)
curl -s http://rank0-host:8080/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "Qwen3-4B-Instruct-4bit",
    "messages": [{"role": "user", "content": "Hello"}],
    "max_tokens": 64
  }'
```

## Performance Considerations

- Set `MLX_METAL_FAST_SYNCH=1` for optimal performance
- Use offline mode (`HF_HUB_OFFLINE=1`) to prevent redundant downloads
- Ensure Thunderbolt connections are properly configured for maximum bandwidth

## Troubleshooting

### RDMA Not Working

Verify RDMA devices are available:
```bash
ibv_devices
```

### Cluster Connection Issues

Use the verification script:
```bash
./scripts/verify_cluster.sh
```

### Model Loading Failures

Ensure the model path is correct and accessible on all nodes. Check that offline mode is configured properly.
