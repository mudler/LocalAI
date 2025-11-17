+++
disableToc = false
title = "Troubleshooting Guide"
weight = 25
icon = "bug_report"
description = "Solutions to common problems and issues with LocalAI"
+++

This guide helps you diagnose and fix common issues with LocalAI. If you can't find a solution here, check the [FAQ]({{% relref "docs/faq" %}}) or ask for help on [Discord](https://discord.gg/uJAeKSAGDy).

## Getting Help

Before asking for help, gather this information:

1. **LocalAI version**: `local-ai --version` or check container image tag
2. **System information**: OS, CPU, RAM, GPU (if applicable)
3. **Error messages**: Full error output with `DEBUG=true`
4. **Configuration**: Relevant model configuration files
5. **Logs**: Enable debug mode and capture logs

## Common Issues

### Model Not Loading

**Symptoms**: Model appears in list but fails to load or respond

**Solutions**:

1. **Check backend installation**:
   ```bash
   local-ai backends list
   local-ai backends install <backend-name>  # if missing
   ```

2. **Verify model file**:
   - Check file exists and is not corrupted
   - Verify file format (GGUF recommended)
   - Re-download if corrupted

3. **Check memory**:
   - Ensure sufficient RAM available
   - Try smaller quantization (Q4_K_S instead of Q8_0)
   - Reduce `context_size` in configuration

4. **Check logs**:
   ```bash
   DEBUG=true local-ai
   ```
   Look for specific error messages

5. **Verify backend compatibility**:
   - Check [Compatibility Table]({{% relref "docs/reference/compatibility-table" %}})
   - Ensure correct backend specified in model config

### Out of Memory Errors

**Symptoms**: Errors about memory, crashes, or very slow performance

**Solutions**:

1. **Reduce model size**:
   - Use smaller quantization (Q2_K, Q4_K_S)
   - Use smaller models (1-3B instead of 7B+)

2. **Adjust configuration**:
   ```yaml
   context_size: 1024  # Reduce from default
   gpu_layers: 20      # Reduce GPU layers if using GPU
   ```

3. **Free system memory**:
   - Close other applications
   - Reduce number of loaded models
   - Use `--single-active-backend` flag

4. **Check system limits**:
   ```bash
   # Linux
   free -h
   ulimit -a
   ```

### Slow Performance

**Symptoms**: Very slow responses, low tokens/second

**Solutions**:

1. **Check hardware**:
   - Use SSD instead of HDD for model storage
   - Ensure adequate CPU cores
   - Enable GPU acceleration if available

2. **Optimize configuration**:
   ```yaml
   threads: 4  # Match CPU cores
   gpu_layers: 35  # Offload to GPU if available
   mmap: true  # Enable memory mapping
   ```

3. **Check for bottlenecks**:
   ```bash
   # Monitor CPU
   top
   
   # Monitor GPU (NVIDIA)
   nvidia-smi
   
   # Monitor disk I/O
   iostat
   ```

4. **Disable unnecessary features**:
   - Set `mirostat: 0` if not needed
   - Reduce context size
   - Use smaller models

5. **Check network**: If using remote models, check network latency

### GPU Not Working

**Symptoms**: GPU not detected, no GPU usage, or CUDA errors

**Solutions**:

1. **Verify GPU drivers**:
   ```bash
   # NVIDIA
   nvidia-smi
   
   # AMD
   rocm-smi
   ```

2. **Check Docker GPU access**:
   ```bash
   docker run --rm --gpus all nvidia/cuda:12.0.0-base-ubuntu22.04 nvidia-smi
   ```

3. **Use correct image**:
   - NVIDIA: `localai/localai:latest-gpu-nvidia-cuda-12`
   - AMD: `localai/localai:latest-gpu-hipblas`
   - Intel: `localai/localai:latest-gpu-intel`

4. **Configure GPU layers**:
   ```yaml
   gpu_layers: 35  # Adjust based on GPU memory
   f16: true
   ```

5. **Check CUDA version**: Ensure CUDA version matches (11.7 vs 12.0)

6. **Check logs**: Enable debug mode to see GPU initialization messages

### API Errors

**Symptoms**: 400, 404, 500, or 503 errors from API

**Solutions**:

1. **404 - Model Not Found**:
   - Verify model name is correct
   - Check model is installed: `curl http://localhost:8080/v1/models`
   - Ensure model file exists in models directory

2. **503 - Service Unavailable**:
   - Model may not be loaded yet (wait a moment)
   - Check if model failed to load (check logs)
   - Verify backend is installed

3. **400 - Bad Request**:
   - Check request format matches API specification
   - Verify all required parameters are present
   - Check parameter types and values

4. **500 - Internal Server Error**:
   - Enable debug mode: `DEBUG=true`
   - Check logs for specific error
   - Verify model configuration is valid

5. **401 - Unauthorized**:
   - Check if API key is required
   - Verify API key is correct
   - Include Authorization header if needed

### Installation Issues

**Symptoms**: Installation fails or LocalAI won't start

**Solutions**:

1. **Docker issues**:
   ```bash
   # Check Docker is running
   docker ps
   
   # Check image exists
   docker images | grep localai
   
   # Pull latest image
   docker pull localai/localai:latest
   ```

2. **Permission issues**:
   ```bash
   # Check file permissions
   ls -la models/
   
   # Fix permissions if needed
   chmod -R 755 models/
   ```

3. **Port already in use**:
   ```bash
   # Find process using port
   lsof -i :8080
   
   # Use different port
   docker run -p 8081:8080 ...
   ```

4. **Binary not found**:
   - Verify binary is in PATH
   - Check binary has execute permissions
   - Reinstall if needed

### Backend Issues

**Symptoms**: Backend fails to install or load

**Solutions**:

1. **Check backend availability**:
   ```bash
   local-ai backends list
   ```

2. **Manual installation**:
   ```bash
   local-ai backends install <backend-name>
   ```

3. **Check network**: Backend download requires internet connection

4. **Check disk space**: Ensure sufficient space for backend files

5. **Rebuild if needed**:
   ```bash
   REBUILD=true local-ai
   ```

### Configuration Issues

**Symptoms**: Models not working as expected, wrong behavior

**Solutions**:

1. **Validate YAML syntax**:
   ```bash
   # Check YAML is valid
   yamllint model.yaml
   ```

2. **Check configuration reference**:
   - See [Model Configuration]({{% relref "docs/advanced/model-configuration" %}})
   - Verify all parameters are correct

3. **Test with minimal config**:
   - Start with basic configuration
   - Add parameters one at a time

4. **Check template files**:
   - Verify template syntax
   - Check template matches model type

## Debugging Tips

### Enable Debug Mode

```bash
# Environment variable
DEBUG=true local-ai

# Command line flag
local-ai --debug

# Docker
docker run -e DEBUG=true ...
```

### Check Logs

```bash
# Docker logs
docker logs local-ai

# Systemd logs
journalctl -u localai -f

# Direct output
local-ai 2>&1 | tee localai.log
```

### Test API Endpoints

```bash
# Health check
curl http://localhost:8080/healthz

# Readiness check
curl http://localhost:8080/readyz

# List models
curl http://localhost:8080/v1/models

# Test chat
curl http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model": "gpt-4", "messages": [{"role": "user", "content": "test"}]}'
```

### Monitor Resources

```bash
# CPU and memory
htop

# GPU (NVIDIA)
watch -n 1 nvidia-smi

# Disk usage
df -h
du -sh models/

# Network
iftop
```

## Performance Issues

### Slow Inference

1. **Check token speed**: Look for tokens/second in debug logs
2. **Optimize threads**: Match CPU cores
3. **Enable GPU**: Use GPU acceleration
4. **Reduce context**: Smaller context = faster inference
5. **Use quantization**: Q4_K_M is good balance

### High Memory Usage

1. **Use smaller models**: 1-3B instead of 7B+
2. **Lower quantization**: Q2_K uses less memory
3. **Reduce context size**: Smaller context = less memory
4. **Disable mmap**: Set `mmap: false` (slower but uses less memory)
5. **Unload unused models**: Only load models you're using

## Platform-Specific Issues

### macOS

- **Quarantine warnings**: See [FAQ]({{% relref "docs/faq" %}})
- **Metal not working**: Ensure Xcode is installed
- **Docker performance**: Consider building from source for better performance

### Linux

- **Permission denied**: Check file permissions and SELinux
- **Missing libraries**: Install required system libraries
- **Systemd issues**: Check service status and logs

### Windows/WSL

- **Slow model loading**: Ensure files are on Linux filesystem
- **GPU access**: May require WSL2 with GPU support
- **Path issues**: Use forward slashes in paths

## Getting More Help

If you've tried the solutions above and still have issues:

1. **Check GitHub Issues**: Search [GitHub Issues](https://github.com/mudler/LocalAI/issues)
2. **Ask on Discord**: Join [Discord](https://discord.gg/uJAeKSAGDy)
3. **Create an Issue**: Provide all debugging information
4. **Check Documentation**: Review relevant documentation sections

## See Also

- [FAQ]({{% relref "docs/faq" %}}) - Common questions
- [Performance Tuning]({{% relref "docs/advanced/performance-tuning" %}}) - Optimize performance
- [VRAM Management]({{% relref "docs/advanced/vram-management" %}}) - GPU memory management
- [Model Configuration]({{% relref "docs/advanced/model-configuration" %}}) - Configuration reference

