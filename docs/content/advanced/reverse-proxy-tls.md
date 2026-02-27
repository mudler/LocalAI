---
title: TLS Reverse Proxy Configuration
description: Configure LocalAI behind a TLS termination reverse proxy (HAProxy, Apache, Nginx)
weight: 100
---

# TLS Reverse Proxy Configuration

When running LocalAI behind a TLS termination reverse proxy, the Web UI may fail to load static assets (CSS, JS) correctly because the application doesn't automatically detect that it's being served over HTTPS. This guide explains how to properly configure your reverse proxy to work with LocalAI.

## How It Works

LocalAI uses the `X-Forwarded-Proto` HTTP header to determine the protocol used by clients. When this header is set to `https`, LocalAI will generate HTTPS URLs for static assets in the Web UI.

## Required Headers

Your reverse proxy must forward these headers to LocalAI:

| Header | Purpose |
|--------|---------|
| `X-Forwarded-Proto` | Set to `https` when TLS is terminated at the proxy |
| `X-Forwarded-Host` | The original host requested by the client |
| `X-Forwarded-Prefix` | Any path prefix if LocalAI is served under a sub-path |

## HAProxy Configuration

```haproxy
frontend https-in
    bind *:443 ssl crt /path/to/cert.pem
    mode http
    
    # Set the X-Forwarded-Proto header
    http-request set-header X-Forwarded-Proto https
    
    # Pass the original host
    http-request set-header X-Forwarded-Host %[hdr(host)]
    
    # If serving under a sub-path, set the prefix
    # http-request set-header X-Forwarded-Prefix /localai
    
    default_backend localai

backend localai
    mode http
    server localai1 127.0.0.1:8080 check
```

## Apache Configuration

```apache
<VirtualHost *:443>
    ServerName your-domain.com
    SSLEngine on
    SSLCertificateFile /path/to/cert.pem
    SSLCertificateKeyFile /path/to/key.pem
    
    # Enable proxy and headers modules
    ProxyRequests Off
    ProxyPreserveHost On
    
    <Proxy *>
        Require all granted
    </Proxy>
    
    # Set the X-Forwarded-Proto header
    RequestHeader set X-Forwarded-Proto "https"
    
    # Set the X-Forwarded-Host header (optional, usually automatic)
    RequestHeader set X-Forwarded-Host "%{HTTP_HOST}s"
    
    # If serving under a sub-path
    # RequestHeader set X-Forwarded-Prefix "/localai"
    
    ProxyPass / http://127.0.0.1:8080/
    ProxyPassReverse / http://127.0.0.1:8080/
</VirtualHost>
```

## Nginx Configuration

```nginx
server {
    listen 443 ssl;
    server_name your-domain.com;
    
    ssl_certificate /path/to/cert.pem;
    ssl_certificate_key /path/to/key.pem;
    
    # Set the X-Forwarded-Proto header
    proxy_set_header X-Forwarded-Proto $scheme;
    
    # Pass the original host
    proxy_set_header X-Forwarded-Host $host;
    
    # If serving under a sub-path
    # proxy_set_header X-Forwarded-Prefix /localai;
    
    # Other proxy settings
    proxy_pass http://127.0.0.1:8080;
    proxy_http_version 1.1;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection "upgrade";
    proxy_set_header Host $host;
    proxy_cache_bypass $http_upgrade;
}
```

## Serving Under a Sub-Path

If you serve LocalAI under a sub-path (e.g., `https://your-domain.com/localai`), you need to:

1. Configure your reverse proxy to set the `X-Forwarded-Prefix` header
2. Configure LocalAI with the `--base-url` option to match the sub-path

Example with Nginx:

```nginx
proxy_set_header X-Forwarded-Prefix /localai;
```

Then start LocalAI with:

```bash
localai --base-url /localai
```

## Testing Your Configuration

1. Start LocalAI: `localai`
2. Configure your reverse proxy as shown above
3. Access the Web UI through the proxy
4. Check the browser's developer console for any mixed content warnings or failed asset loads
5. Verify that the HTML source contains `https://` URLs for static assets

## Troubleshooting

### Static Assets Not Loading

- Verify the `X-Forwarded-Proto` header is being forwarded
- Check that the header value is exactly `https` (lowercase)
- Inspect the network tab in your browser to see which requests are failing

### Mixed Content Warnings

- Ensure LocalAI is generating HTTPS URLs (check the BaseURL middleware is working)
- Verify the `X-Forwarded-Proto` header is set before LocalAI processes the request

### Redirect Loops

- Check that your proxy is not adding duplicate headers
- Verify `X-Forwarded-Proto` is not being set to both `http` and `https`

## Security Note

When using reverse proxies, ensure your proxy only accepts connections from trusted sources and properly validates SSL certificates. Never expose LocalAI directly to the internet without TLS termination.
