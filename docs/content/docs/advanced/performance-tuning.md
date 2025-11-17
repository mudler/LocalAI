+++
disableToc = false
title = "Performance Tuning"
weight = 22
icon = "speed"
description = "Optimize LocalAI for maximum performance"
+++

This guide covers techniques to optimize LocalAI performance for your specific hardware and use case.

## Performance Metrics

Before optimizing, establish baseline metrics:

- **Tokens per second**: Measure inference speed
- **Memory usage**: Monitor RAM and VRAM
- **Latency**: Time to first token and total response time
- **Throughput**: Requests per second

Enable debug mode to see performance stats:

```bash
DEBUG=true local-ai
```

Look for output like:
```
llm_load_tensors: tok/s: 45.23
```

## CPU Optimization

### Thread Configuration

Match threads to CPU cores:

```yaml
# Model configuration
threads: 4  # For 4-core CPU
```

**Guidelines**:
- Use number of physical cores (not hyperthreads)
- Leave 1-2 cores for system
- Too many threads can hurt performance

### CPU Instructions

Enable appropriate CPU instructions:

```bash
# Check available instructions
cat /proc/cpuinfo | grep flags

# Build with optimizations
CMAKE_ARGS="-DGGML_AVX2=ON -DGGML_AVX512=ON" make build
```

### NUMA Optimization

For multi-socket systems:

```yaml
numa: true
```

### Memory Mapping

Enable memory mapping for faster model loading:

```yaml
mmap: true
mmlock: false  # Set to true to lock in memory (faster but uses more RAM)
```

## GPU Optimization

### Layer Offloading

Offload as many layers as GPU memory allows:

```yaml
gpu_layers: 35  # Adjust based on GPU memory
f16: true       # Use FP16 for better performance
```

**Finding optimal layers**:
1. Start with 20 layers
2. Monitor GPU memory: `nvidia-smi` or `rocm-smi`
3. Gradually increase until near memory limit
4. For maximum performance, offload all layers if possible

### Batch Processing

GPU excels at batch processing. Process multiple requests together when possible.

### Mixed Precision

Use FP16 when supported:

```yaml
f16: true
```

## Model Optimization

### Quantization

Choose appropriate quantization:

| Quantization | Speed | Quality | Memory | Use Case |
|-------------|-------|---------|--------|----------|
| Q8_0 | Slowest | Highest | Most | Maximum quality |
| Q6_K | Slow | Very High | High | High quality |
| Q4_K_M | Medium | High | Medium | **Recommended** |
| Q4_K_S | Fast | Medium | Low | Balanced |
| Q2_K | Fastest | Lower | Least | Speed priority |

### Context Size

Reduce context size for faster inference:

```yaml
context_size: 2048  # Instead of 4096 or 8192
```

**Trade-off**: Smaller context = faster but less conversation history

### Model Selection

Choose models appropriate for your hardware:

- **Small systems (4GB RAM)**: 1-3B parameter models
- **Medium systems (8-16GB RAM)**: 3-7B parameter models
- **Large systems (32GB+ RAM)**: 7B+ parameter models

## Configuration Optimizations

### Sampling Parameters

Optimize sampling for speed:

```yaml
parameters:
  temperature: 0.7
  top_p: 0.9
  top_k: 40
  mirostat: 0  # Disable for speed (enabled by default)
```

**Note**: Disabling mirostat improves speed but may reduce quality.

### Prompt Caching

Enable prompt caching for repeated queries:

```yaml
prompt_cache_path: "cache"
prompt_cache_all: true
```

### Parallel Requests

LocalAI supports parallel requests. Configure appropriately:

```yaml
# In model config
parallel_requests: 4  # Adjust based on hardware
```

## Storage Optimization

### Use SSD

Always use SSD for model storage:
- HDD: Very slow model loading
- SSD: Fast loading, better performance

### Disable MMAP on HDD

If stuck with HDD:

```yaml
mmap: false  # Loads entire model into RAM
```

### Model Location

Store models on fastest storage:
- Local SSD: Best performance
- Network storage: Slower, but allows sharing
- External drive: Slowest

## System-Level Optimizations

### Process Priority

Increase process priority (Linux):

```bash
nice -n -10 local-ai
```

### CPU Governor

Set CPU to performance mode (Linux):

```bash
# Check current governor
cat /sys/devices/system/cpu/cpu*/cpufreq/scaling_governor

# Set to performance
echo performance | sudo tee /sys/devices/system/cpu/cpu*/cpufreq/scaling_governor
```

### Disable Swapping

Prevent swapping for better performance:

```bash
# Linux
sudo swapoff -a

# Or set swappiness to 0
echo 0 | sudo tee /proc/sys/vm/swappiness
```

### Memory Allocation

For large models, consider huge pages (Linux):

```bash
# Allocate huge pages
echo 1024 | sudo tee /proc/sys/vm/nr_hugepages
```

## Benchmarking

### Measure Performance

Create a benchmark script:

```python
import time
import requests

start = time.time()
response = requests.post(
    "http://localhost:8080/v1/chat/completions",
    json={
        "model": "gpt-4",
        "messages": [{"role": "user", "content": "Hello"}]
    }
)
elapsed = time.time() - start

tokens = response.json()["usage"]["completion_tokens"]
tokens_per_second = tokens / elapsed

print(f"Time: {elapsed:.2f}s")
print(f"Tokens: {tokens}")
print(f"Speed: {tokens_per_second:.2f} tok/s")
```

### Compare Configurations

Test different configurations:
1. Baseline: Default settings
2. Optimized: Your optimizations
3. Measure: Tokens/second, latency, memory

### Load Testing

Test under load:

```bash
# Use Apache Bench or similar
ab -n 100 -c 10 -p request.json -T application/json \
  http://localhost:8080/v1/chat/completions
```

## Platform-Specific Tips

### Apple Silicon

- Metal acceleration is automatic
- Use native builds (not Docker) for best performance
- M1/M2/M3 have unified memory - optimize accordingly

### NVIDIA GPUs

- Use CUDA 12 for latest optimizations
- Enable Tensor Cores with appropriate precision
- Monitor with `nvidia-smi` for bottlenecks

### AMD GPUs

- Use ROCm/HIPBLAS backend
- Check ROCm compatibility
- Monitor with `rocm-smi`

### Intel GPUs

- Use oneAPI/SYCL backend
- Check Intel GPU compatibility
- Optimize for F16/F32 precision

## Common Performance Issues

### Slow First Response

**Cause**: Model loading
**Solution**: Pre-load models or use model warming

### Degrading Performance

**Cause**: Memory fragmentation
**Solution**: Restart LocalAI periodically

### Inconsistent Speed

**Cause**: System load, thermal throttling
**Solution**: Monitor system resources, ensure cooling

## Performance Checklist

- [ ] Threads match CPU cores
- [ ] GPU layers optimized
- [ ] Appropriate quantization selected
- [ ] Context size optimized
- [ ] Models on SSD
- [ ] MMAP enabled (if using SSD)
- [ ] Mirostat disabled (if speed priority)
- [ ] System resources monitored
- [ ] Baseline metrics established
- [ ] Optimizations tested and verified

## See Also

- [GPU Acceleration]({{% relref "docs/features/gpu-acceleration" %}}) - GPU setup
- [VRAM Management]({{% relref "docs/advanced/vram-management" %}}) - GPU memory
- [Model Configuration]({{% relref "docs/advanced/model-configuration" %}}) - Configuration options
- [Troubleshooting]({{% relref "docs/troubleshooting" %}}) - Performance issues

