+++
disableToc = false
title = "⚙️ Runtime Settings"
weight = 25
url = '/features/runtime-settings'
+++

LocalAI provides a web-based interface for managing application settings at runtime. These settings can be configured through the web UI and are automatically persisted to a configuration file, allowing changes to take effect immediately without requiring a restart.

## Accessing Runtime Settings

Navigate to the **Settings** page from the management interface at `http://localhost:8080/manage`. The settings page provides a comprehensive interface for configuring various aspects of LocalAI.

## Available Settings

### Watchdog Settings

The watchdog monitors backend activity and can automatically stop idle or overly busy models to free up resources.

- **Watchdog Enabled**: Master switch to enable/disable the watchdog
- **Watchdog Idle Enabled**: Enable stopping backends that are idle longer than the idle timeout
- **Watchdog Busy Enabled**: Enable stopping backends that are busy longer than the busy timeout
- **Watchdog Idle Timeout**: Duration threshold for idle backends (default: `15m`)
- **Watchdog Busy Timeout**: Duration threshold for busy backends (default: `5m`)

Changes to watchdog settings are applied immediately by restarting the watchdog service.

### Backend Configuration

- **Single Backend**: Allow only one backend to run at a time
- **Parallel Backend Requests**: Enable backends to handle multiple requests in parallel if supported

### Performance Settings

- **Threads**: Number of threads used for parallel computation (recommended: number of physical cores)
- **Context Size**: Default context size for models (default: `512`)
- **F16**: Enable GPU acceleration using 16-bit floating point

### Debug and Logging

- **Debug Mode**: Enable debug logging (deprecated, use log-level instead)

### API Security

- **CORS**: Enable Cross-Origin Resource Sharing
- **CORS Allow Origins**: Comma-separated list of allowed CORS origins
- **CSRF**: Enable CSRF protection middleware
- **API Keys**: Manage API keys for authentication (one per line or comma-separated)

### P2P Settings

Configure peer-to-peer networking for distributed inference:

- **P2P Token**: Authentication token for P2P network
- **P2P Network ID**: Network identifier for P2P connections
- **Federated Mode**: Enable federated mode for P2P network

Changes to P2P settings automatically restart the P2P stack with the new configuration.

### Gallery Settings

Manage model and backend galleries:

- **Model Galleries**: JSON array of gallery objects with `url` and `name` fields
- **Backend Galleries**: JSON array of backend gallery objects
- **Autoload Galleries**: Automatically load model galleries on startup
- **Autoload Backend Galleries**: Automatically load backend galleries on startup

## Configuration Persistence

All settings are automatically saved to `runtime_settings.json` in the `LOCALAI_CONFIG_DIR` directory (default: `BASEPATH/configuration`). This file is watched for changes, so modifications made directly to the file will also be applied at runtime.

## Environment Variable Precedence

Environment variables take precedence over settings configured via the web UI or configuration files. If a setting is controlled by an environment variable, it cannot be modified through the web interface. The settings page will indicate when a setting is controlled by an environment variable.

The precedence order is:
1. **Environment variables** (highest priority)
2. **Configuration files** (`runtime_settings.json`, `api_keys.json`)
3. **Default values** (lowest priority)

## Example Configuration

The `runtime_settings.json` file follows this structure:

```json
{
  "watchdog_enabled": true,
  "watchdog_idle_enabled": true,
  "watchdog_busy_enabled": false,
  "watchdog_idle_timeout": "15m",
  "watchdog_busy_timeout": "5m",
  "single_backend": false,
  "parallel_backend_requests": true,
  "threads": 8,
  "context_size": 2048,
  "f16": false,
  "debug": false,
  "cors": true,
  "csrf": false,
  "cors_allow_origins": "*",
  "p2p_token": "",
  "p2p_network_id": "",
  "federated": false,
  "galleries": [
    {
      "url": "github:mudler/LocalAI/gallery/index.yaml@master",
      "name": "localai"
    }
  ],
  "backend_galleries": [
    {
      "url": "github:mudler/LocalAI/backend/index.yaml@master",
      "name": "localai"
    }
  ],
  "autoload_galleries": true,
  "autoload_backend_galleries": true,
  "api_keys": []
}
```

## API Keys Management

API keys can be managed through the runtime settings interface. Keys can be entered one per line or comma-separated. 

**Important Notes:**
- API keys from environment variables are always included and cannot be removed via the UI
- Runtime API keys are stored in `runtime_settings.json`
- For backward compatibility, API keys can also be managed via `api_keys.json`
- Empty arrays will clear all runtime API keys (but preserve environment variable keys)

## Dynamic Configuration

The runtime settings system supports dynamic configuration file watching. When `LOCALAI_CONFIG_DIR` is set, LocalAI monitors the following files for changes:

- `runtime_settings.json` - Unified runtime settings
- `api_keys.json` - API keys (for backward compatibility)
- `external_backends.json` - External backend configurations

Changes to these files are automatically detected and applied without requiring a restart.

## Best Practices

1. **Use Environment Variables for Production**: For production deployments, use environment variables for critical settings to ensure they cannot be accidentally changed via the web UI.

2. **Backup Configuration Files**: Before making significant changes, consider backing up your `runtime_settings.json` file.

3. **Monitor Resource Usage**: When enabling watchdog features, monitor your system to ensure the timeout values are appropriate for your workload.

4. **Secure API Keys**: API keys are sensitive information. Ensure proper file permissions on configuration files (they should be readable only by the LocalAI process).

5. **Test Changes**: Some settings (like watchdog timeouts) may require testing to find optimal values for your specific use case.

## Troubleshooting

### Settings Not Applying

If settings are not being applied:
1. Check if the setting is controlled by an environment variable
2. Verify the `LOCALAI_CONFIG_DIR` is set correctly
3. Check file permissions on `runtime_settings.json`
4. Review application logs for configuration errors

### Watchdog Not Working

If the watchdog is not functioning:
1. Ensure "Watchdog Enabled" is turned on
2. Verify at least one of the idle or busy watchdogs is enabled
3. Check that timeout values are reasonable for your workload
4. Review logs for watchdog-related messages

### P2P Not Starting

If P2P is not starting:
1. Verify the P2P token is set (non-empty)
2. Check network connectivity
3. Ensure the P2P network ID matches across nodes (if using federated mode)
4. Review logs for P2P-related errors

