+++
disableToc = false
title = "Security Best Practices"
weight = 26
icon = "security"
description = "Security guidelines for deploying LocalAI"
+++

This guide covers security best practices for deploying LocalAI in various environments, from local development to production.

## Overview

LocalAI processes sensitive data and may be exposed to networks. Follow these practices to secure your deployment.

## API Key Protection

### Always Use API Keys in Production

**Never expose LocalAI without API keys**:

```bash
# Set API key
API_KEY=your-secure-random-key local-ai

# Multiple keys (comma-separated)
API_KEY=key1,key2,key3 local-ai
```

### API Key Best Practices

1. **Generate strong keys**: Use cryptographically secure random strings
   ```bash
   # Generate a secure key
   openssl rand -hex 32
   ```

2. **Store securely**: 
   - Use environment variables
   - Use secrets management (Kubernetes Secrets, HashiCorp Vault, etc.)
   - Never commit keys to version control

3. **Rotate regularly**: Change API keys periodically

4. **Use different keys**: Different keys for different services/clients

5. **Limit key scope**: Consider implementing key-based rate limiting

### Using API Keys

Include the key in requests:

```bash
curl http://localhost:8080/v1/models \
  -H "Authorization: Bearer your-api-key"
```

**Important**: API keys provide full access to all LocalAI features (admin-level). Protect them accordingly.

## Network Security

### Never Expose Directly to Internet

**Always use a reverse proxy** when exposing LocalAI:

```nginx
# nginx example
server {
    listen 443 ssl;
    server_name localai.example.com;

    ssl_certificate /path/to/cert.pem;
    ssl_certificate_key /path/to/key.pem;

    location / {
        proxy_pass http://localhost:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

### Use HTTPS/TLS

**Always use HTTPS in production**:

1. Obtain SSL/TLS certificates (Let's Encrypt, etc.)
2. Configure reverse proxy with TLS
3. Enforce HTTPS redirects
4. Use strong cipher suites

### Firewall Configuration

Restrict access with firewall rules:

```bash
# Allow only specific IPs (example)
ufw allow from 192.168.1.0/24 to any port 8080

# Or use iptables
iptables -A INPUT -p tcp --dport 8080 -s 192.168.1.0/24 -j ACCEPT
iptables -A INPUT -p tcp --dport 8080 -j DROP
```

### VPN or Private Network

For sensitive deployments:
- Use VPN for remote access
- Deploy on private network only
- Use network segmentation

## Model Security

### Model Source Verification

**Only use trusted model sources**:

1. **Official galleries**: Use LocalAI's model gallery
2. **Verified repositories**: Hugging Face verified models
3. **Verify checksums**: Check SHA256 hashes when provided
4. **Scan for malware**: Scan downloaded files

### Model Isolation

- Run models in isolated environments
- Use containers with limited permissions
- Separate model storage from system

### Model Access Control

- Restrict file system access to models
- Use appropriate file permissions
- Consider read-only model storage

## Container Security

### Use Non-Root User

Run containers as non-root:

```yaml
# Docker Compose
services:
  localai:
    user: "1000:1000"  # Non-root UID/GID
```

### Limit Container Capabilities

```yaml
services:
  localai:
    cap_drop:
      - ALL
    cap_add:
      - NET_BIND_SERVICE  # Only what's needed
```

### Resource Limits

Set resource limits to prevent resource exhaustion:

```yaml
services:
  localai:
    deploy:
      resources:
        limits:
          cpus: '4'
          memory: 16G
```

### Read-Only Filesystem

Where possible, use read-only filesystem:

```yaml
services:
  localai:
    read_only: true
    tmpfs:
      - /tmp
      - /var/run
```

## Input Validation

### Sanitize Inputs

Validate and sanitize all inputs:
- Check input length limits
- Validate data formats
- Sanitize user prompts
- Implement rate limiting

### File Upload Security

If accepting file uploads:
- Validate file types
- Limit file sizes
- Scan for malware
- Store in isolated location

## Logging and Monitoring

### Secure Logging

- Don't log sensitive data (API keys, user inputs)
- Use secure log storage
- Implement log rotation
- Monitor for suspicious activity

### Monitoring

Monitor for:
- Unusual API usage patterns
- Failed authentication attempts
- Resource exhaustion
- Error rate spikes

## Updates and Maintenance

### Keep Updated

- Regularly update LocalAI
- Update dependencies
- Patch security vulnerabilities
- Monitor security advisories

### Backup Security

- Encrypt backups
- Secure backup storage
- Test restore procedures
- Limit backup access

## Deployment-Specific Security

### Kubernetes

- Use NetworkPolicies
- Implement RBAC
- Use Secrets for sensitive data
- Enable Pod Security Policies
- Use service mesh for mTLS

### Docker

- Use official images
- Scan images for vulnerabilities
- Keep images updated
- Use Docker secrets
- Implement health checks

### Systemd

- Run as dedicated user
- Limit systemd service capabilities
- Use PrivateTmp, ProtectSystem
- Restrict network access

## Security Checklist

Before deploying to production:

- [ ] API keys configured and secured
- [ ] HTTPS/TLS enabled
- [ ] Reverse proxy configured
- [ ] Firewall rules set
- [ ] Network access restricted
- [ ] Container security hardened
- [ ] Resource limits configured
- [ ] Logging configured securely
- [ ] Monitoring in place
- [ ] Updates planned
- [ ] Backup security ensured
- [ ] Incident response plan ready

## Incident Response

### If Compromised

1. **Isolate**: Immediately disconnect from network
2. **Assess**: Determine scope of compromise
3. **Contain**: Prevent further damage
4. **Eradicate**: Remove threats
5. **Recover**: Restore from clean backups
6. **Learn**: Document and improve

### Security Contacts

- Report security issues: [GitHub Security](https://github.com/mudler/LocalAI/security)
- Security discussions: [Discord](https://discord.gg/uJAeKSAGDy)

## Compliance Considerations

### Data Privacy

- Understand data processing
- Implement data retention policies
- Consider GDPR, CCPA requirements
- Document data flows

### Audit Logging

- Log all API access
- Track model usage
- Monitor configuration changes
- Retain logs appropriately

## See Also

- [Deploying to Production]({{% relref "docs/tutorials/deploying-production" %}}) - Production deployment
- [API Reference]({{% relref "docs/reference/api-reference" %}}) - API security
- [Troubleshooting]({{% relref "docs/troubleshooting" %}}) - Security issues
- [FAQ]({{% relref "docs/faq" %}}) - Security questions

