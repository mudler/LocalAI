+++
disableToc = false
title = "Deploying to Production"
weight = 4
icon = "rocket_launch"
description = "Best practices for running LocalAI in production environments"
+++

This tutorial covers best practices for deploying LocalAI in production environments, including security, performance, monitoring, and reliability considerations.

## Prerequisites

- LocalAI installed and tested
- Understanding of your deployment environment
- Basic knowledge of Docker, Kubernetes, or your chosen deployment method

## Security Considerations

### 1. API Key Protection

**Always use API keys in production**:

```bash
# Set API key
API_KEY=your-secure-random-key local-ai

# Or multiple keys
API_KEY=key1,key2,key3 local-ai
```

**Best Practices**:
- Use strong, randomly generated keys
- Store keys securely (environment variables, secrets management)
- Rotate keys regularly
- Use different keys for different services/clients

### 2. Network Security

**Never expose LocalAI directly to the internet** without protection:

- Use a reverse proxy (nginx, Traefik, Caddy)
- Enable HTTPS/TLS
- Use firewall rules to restrict access
- Consider VPN or private network access only

**Example nginx configuration**:

```nginx
server {
    listen 443 ssl;
    server_name localai.example.com;

    ssl_certificate /path/to/cert.pem;
    ssl_certificate_key /path/to/key.pem;

    location / {
        proxy_pass http://localhost:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
    }
}
```

### 3. Resource Limits

Set appropriate resource limits to prevent resource exhaustion:

```yaml
# Docker Compose example
services:
  localai:
    deploy:
      resources:
        limits:
          cpus: '4'
          memory: 16G
        reservations:
          cpus: '2'
          memory: 8G
```

## Deployment Methods

### Docker Compose (Recommended for Small-Medium Deployments)

```yaml
version: '3.8'

services:
  localai:
    image: localai/localai:latest
    ports:
      - "8080:8080"
    environment:
      - API_KEY=${API_KEY}
      - DEBUG=false
      - MODELS_PATH=/models
    volumes:
      - ./models:/models
      - ./config:/config
    restart: unless-stopped
    healthcheck:
      test: ["CMD", "curl", "-f", "http://localhost:8080/readyz"]
      interval: 30s
      timeout: 10s
      retries: 3
    deploy:
      resources:
        limits:
          memory: 16G
```

### Kubernetes

See the [Kubernetes Deployment Guide]({{% relref "docs/getting-started/kubernetes" %}}) for detailed instructions.

**Key considerations**:
- Use ConfigMaps for configuration
- Use Secrets for API keys
- Set resource requests and limits
- Configure health checks and liveness probes
- Use PersistentVolumes for model storage

### Systemd Service (Linux)

Create a systemd service file:

```ini
[Unit]
Description=LocalAI Service
After=network.target

[Service]
Type=simple
User=localai
Environment="API_KEY=your-key"
Environment="MODELS_PATH=/var/lib/localai/models"
ExecStart=/usr/local/bin/local-ai
Restart=always
RestartSec=10

[Install]
WantedBy=multi-user.target
```

## Performance Optimization

### 1. Model Selection

- Use quantized models (Q4_K_M) for production
- Choose models appropriate for your hardware
- Consider model size vs. quality trade-offs

### 2. Resource Allocation

```yaml
# Model configuration
name: production-model
parameters:
  model: model.gguf
context_size: 2048  # Adjust based on needs
threads: 4  # Match CPU cores
gpu_layers: 35  # If using GPU
```

### 3. Caching

Enable prompt caching for repeated queries:

```yaml
prompt_cache_path: "cache"
prompt_cache_all: true
```

### 4. Connection Pooling

If using a reverse proxy, configure connection pooling:

```nginx
upstream localai {
    least_conn;
    server localhost:8080 max_fails=3 fail_timeout=30s;
    keepalive 32;
}
```

## Monitoring and Logging

### 1. Health Checks

LocalAI provides health check endpoints:

```bash
# Readiness check
curl http://localhost:8080/readyz

# Health check
curl http://localhost:8080/healthz
```

### 2. Logging

Configure appropriate log levels:

```bash
# Production: minimal logging
DEBUG=false local-ai

# Development: detailed logging
DEBUG=true local-ai
```

### 3. Metrics

Monitor key metrics:
- Request rate
- Response times
- Error rates
- Resource usage (CPU, memory, GPU)
- Model loading times

### 4. Alerting

Set up alerts for:
- Service downtime
- High error rates
- Resource exhaustion
- Slow response times

## High Availability

### 1. Multiple Instances

Run multiple LocalAI instances behind a load balancer:

```yaml
# Docker Compose with multiple instances
services:
  localai1:
    image: localai/localai:latest
    # ... configuration
  
  localai2:
    image: localai/localai:latest
    # ... configuration
  
  nginx:
    image: nginx:alpine
    # Load balance between localai1 and localai2
```

### 2. Model Replication

Ensure models are available on all instances:
- Shared storage (NFS, S3, etc.)
- Model synchronization
- Consistent model versions

### 3. Graceful Shutdown

LocalAI supports graceful shutdown. Ensure your deployment method handles SIGTERM properly.

## Backup and Recovery

### 1. Model Backups

Regularly backup your models and configurations:

```bash
# Backup models
tar -czf models-backup-$(date +%Y%m%d).tar.gz models/

# Backup configurations
tar -czf config-backup-$(date +%Y%m%d).tar.gz config/
```

### 2. Configuration Management

Version control your configurations:
- Use Git for YAML configurations
- Document model versions
- Track configuration changes

### 3. Disaster Recovery

Plan for:
- Model storage recovery
- Configuration restoration
- Service restoration procedures

## Scaling Considerations

### Horizontal Scaling

- Run multiple instances
- Use load balancing
- Consider stateless design (shared model storage)

### Vertical Scaling

- Increase resources (CPU, RAM, GPU)
- Use more powerful hardware
- Optimize model configurations

## Maintenance

### 1. Updates

- Test updates in staging first
- Plan maintenance windows
- Have rollback procedures ready

### 2. Model Updates

- Test new models before production
- Keep model versions documented
- Have rollback capability

### 3. Monitoring

Regularly review:
- Performance metrics
- Error logs
- Resource usage trends
- User feedback

## Production Checklist

Before going live, ensure:

- [ ] API keys configured and secured
- [ ] HTTPS/TLS enabled
- [ ] Firewall rules configured
- [ ] Resource limits set
- [ ] Health checks configured
- [ ] Monitoring in place
- [ ] Logging configured
- [ ] Backups scheduled
- [ ] Documentation updated
- [ ] Team trained on operations
- [ ] Incident response plan ready

## What's Next?

- [Kubernetes Deployment]({{% relref "docs/getting-started/kubernetes" %}}) - Deploy on Kubernetes
- [Performance Tuning]({{% relref "docs/advanced/performance-tuning" %}}) - Optimize performance
- [Security Best Practices]({{% relref "docs/security" %}}) - Security guidelines
- [Troubleshooting Guide]({{% relref "docs/troubleshooting" %}}) - Production issues

## See Also

- [Container Images]({{% relref "docs/getting-started/container-images" %}})
- [Advanced Configuration]({{% relref "docs/advanced" %}})
- [FAQ]({{% relref "docs/faq" %}})

