+++
disableToc = false
title = "Runtime Settings"
weight = 82
url = '/features/runtime-settings'
+++

LocalAI provides a web-based interface for managing application settings at runtime. These settings can be configured through the web UI and are automatically persisted to a configuration file, allowing changes to take effect immediately without requiring a restart.

## Accessing Runtime Settings

Open **Operate → Settings** in the web UI (`/app/settings`). The page lists every setting below under eight groups by intent, and keeps your edits in a pending bar until you apply them.

### Groups

The groups reorganise the fields the page used to show under fifteen sections. The mapping:

| Group | Settings it holds | Used to be under |
|---|---|---|
| Memory and models | Watchdog (idle and busy checks, timeouts, interval), eviction (force when busy, largest first, retries, retry interval), free memory automatically and its threshold, models kept loaded, GPU memory budget | Watchdog, Memory Reclaimer, Backend Management, Performance |
| Speed and defaults | Default threads, default context size, artifact download concurrency, F16 | Performance |
| Backends and galleries | Automatic backend upgrades, development backends, gallery loading on boot, the persistent VRAM cache, the model and backend gallery lists | Backend Management, Galleries |
| Access and security | CORS and allowed origins, CSRF protection, shared API keys | API & CORS, API Keys |
| Debugging and traces | Verbose debug logging, API traces and their limits, backend logging | Performance, Tracing |
| Chats and responses | The collections (knowledge base) service, the helper models of the chats, MCP connector addresses, the LocalAI Assistant (the switch for the admin assistant chat that starts from Home; see [LocalAI Assistant]({{% relref "features/localai-assistant" %}})), shared pages (`shared_pages_enabled`: the pages of shared folders; see [Pages]({{% relref "features/pages" %}})), the response store TTL | Collections, Chats, LocalAI Assistant, Open Responses |
| Swarm and sharing | P2P token, network ID and federated mode, the disk headroom check | P2P Network, Distributed |
| Look and feel | Instance name, tagline, the three logos | Branding |

Search at the top of the page matches a setting's name, description and key, the group, and the old section name, so a search for "Watchdog" still finds the watchdog settings and each result says where it used to be.

### Editing, applying and undoing

Edits are not sent as you type. A bar at the bottom of the page counts them and offers **Discard**, **Show diff** and **Apply**. The diff lists each old and new value, and runs the checks the browser can make: a duration parses the way the server parses it, a GPU memory budget is one the server accepts, a gallery list is valid JSON with a `url` in each entry, and warnings repeat what the server says (a restart is needed, an empty P2P token stops P2P, evicting while busy can interrupt requests, CSRF protection off). A check that would make the server refuse the value stops **Apply**.

**Apply** sends only the settings that changed. After it, **Undo** (for ten seconds) saves the previous values again. This is a new save, not a rollback: anything that changed in between stays changed.

A setting shows **Changed** and its built-in default, with a **Reset** link, only when the default is known (the defaults of the `local-ai run` flags). Settings without a stated default, such as threads and the context size, never show the marker. An environment variable or flag can change the value the server starts with, so the marker compares with the built-in default, not with the startup value.

A row says **Applies now** when the save handler applies the setting immediately (the watchdog and the memory reclaimer restart with the new values; so does the P2P stack when its settings change), and **Needs restart** for most collections settings. A setting without either note is saved, and the code does not say when it takes effect.

**History** lists the settings you applied from this browser, newest first, up to the last 50. LocalAI keeps no settings log of its own, so changes made elsewhere do not appear, and a secret (the P2P token, shared API keys, the collections database URL) is listed as changed without its value. **Revert** puts the old value back as an edit.

`POST /api/settings` accepts a partial body and merges it over the saved settings, so the page sends only the keys it changes. In the request, the `csrf` field carries the **disable** flag: the page shows the inverse as "CSRF protection". The galleries are sent as `galleries` and `backend_galleries`, and the shared API keys as `api_keys`, each as a JSON list.

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

- **Max Active Backends**: Maximum number of active backends (loaded models). When exceeded, the least recently used model is automatically evicted. Set to `0` for unlimited, `1` for single-backend mode
- **Force Eviction When Busy**: Allow evicting models even when they have active API calls (default: disabled for safety). **Warning:** Enabling this can interrupt active requests
- **LRU Eviction Max Retries**: Maximum number of retries when waiting for busy models to become idle before eviction (default: 30)
- **LRU Eviction Retry Interval**: Interval between retries when waiting for busy models (default: `1s`)

> **Note:** The "Single Backend" setting is deprecated. Use "Max Active Backends" set to `1` for single-backend behavior.

#### LRU Eviction Behavior

By default, LocalAI will skip evicting models that have active API calls to prevent interrupting ongoing requests. When all models are busy and eviction is needed:

1. The system will wait for models to become idle
2. It will retry eviction up to the configured maximum number of retries
3. The retry interval determines how long to wait between attempts
4. If all retries are exhausted, the system will proceed (which may cause out-of-memory errors if resources are truly exhausted)

You can configure these settings via the web UI or through environment variables. See [VRAM Management]({{%relref "advanced/vram-management" %}}) for more details.

### Performance Settings

- **Threads**: Number of threads used for parallel computation (recommended: number of physical cores)
- **Context Size**: Default context size for models (default: `512`)
- **Concurrent Backend Operations**: How many backend installs, upgrades and deletes run at once. `1` runs them one after the other (default: `1`, minimum `1`). Operations on the same backend never overlap, whatever the value. Model operations run in their own queue and are not held up by backend operations. In distributed mode the same limit bounds how many nodes receive one operation at the same time. The limit is per frontend. A change applies to operations that start after the change
- **Artifact Download Concurrency**: Maximum number of artifact files downloaded at once. `1` downloads sequentially (default: `1`)
- **F16**: Enable GPU acceleration using 16-bit floating point
- **VRAM Budget**: Cap on VRAM used for model allocation (for example `80%` or `12GB`; empty means no cap). See [VRAM Management]({{%relref "advanced/vram-management" %}})

### Debug and Logging

- **Debug Mode**: Enable debug logging (deprecated, use log-level instead)

### API Security

- **CORS**: Enable Cross-Origin Resource Sharing
- **CORS Allow Origins**: Comma-separated list of allowed CORS origins. When CORS is explicitly enabled with a nonempty allowlist, matching origins are also trusted by CSRF protection for cross-site state-changing requests. Explicit wildcard patterns apply to both policies. The default permissive HTTP CORS policy does not bypass CSRF; authentication and permissions still apply.
- **CSRF**: Enable CSRF protection middleware. Outside explicitly trusted CORS origins, cross-site browser requests are exempt only when the server successfully authenticates a credential from `Authorization`, `x-api-key`, or `xi-api-key`. Supplying an arbitrary header when authentication is disabled does not bypass protection; cookie authentication also does not grant this exemption. Requests without `Sec-Fetch-Site` (such as CLI clients) remain allowed by this middleware.
- **API Keys**: Manage API keys for authentication (one per line or comma-separated)
- **Offers from the assistant** (`chats_suggestions`, default on, applies at once): the default for users who did not choose. On, the assistant may offer, at most once a day and once a chat, never in the first turns or during a task, to remember a name or bio, make a shortcut, or update a Home widget on a schedule. A user's own switch (Account, **About you and the assistant**, **Help and suggestions**) wins. See [Make LocalAI yours]({{%relref "features/make-localai-yours" %}}).
- **MCP connectors may reach private networks** (`mcp_connectors_allow_private`, default off, applies at once): lets the [MCP servers that users add as connectors]({{%relref "features/chats-connectors" %}}) use addresses on private networks (this machine, link-local, private ranges, cloud metadata). Off, a connector reaches only public hosts, and the check is made when the connection is opened, on every address and every redirect. Turn it on only if your users are trusted: a connector can then probe the internal network through the server.

For multi-user authentication with roles, OAuth, and usage tracking, see [Authentication & Authorization]({{%relref "features/authentication" %}}).

### P2P Settings

Configure peer-to-peer networking for distributed inference:

- **P2P Token**: Authentication token for P2P network
- **P2P Network ID**: Network identifier for P2P connections
- **Federated Mode**: Enable federated mode for P2P network

Changes to P2P settings automatically restart the P2P stack with the new configuration.

### Gallery Settings

Manage model and backend galleries:

- **Model Galleries**: a read-only list of the model library sources, with name, id, address, state, whether it is a default, and its mirrors. Change them in the **Manage libraries** panel of the Models page (see [Gallery mirrors]({{%relref "features/model-gallery#gallery-mirrors" %}}))
- **Backend Galleries**: the same read-only list for backend library sources; change them in the **Manage libraries** panel of the Backends page
- **Load and pre-warm galleries on boot**: Load model galleries and pre-warm their remote size and VRAM estimates when LocalAI starts. Disable this setting to skip both startup operations.
- **Autoload Backend Galleries**: Automatically load backend galleries on startup

### Chats and Collections Settings

Configure the collections (the knowledge base) and the models the [chats]({{%relref "features/chats" %}}) use for media:

- **Collections**: Enable or disable the collections service
- **Default model for collections**: Default LLM model of the collections service
- **Chat image model** (`chats_vision_model`, default empty, applies at once): the model the chats use to describe an image (`describe_image`). Empty, or a model that is not installed: the chat's own model when it can look at images, else the first installed model, by name, that can.
- **Chat transcription model** (`chats_transcription_model`, default empty, applies at once): the model the chats use to transcribe audio. Empty, or a model that is not installed: the first installed model, by name, that can transcribe.
- **Embedding Model**: Model used for knowledge base embeddings (default: `granite-embedding-107m-multilingual`)
- **Max Chunking Size**: Maximum chunk size for document ingestion (default: `400`)
- **Chunk Overlap**: Overlap between document chunks (default: `0`)
- **Collection DB Path**: Custom path for the collections database
- **Shared pages** (`shared_pages_enabled`, default on): lets users open the [pages]({{%relref "features/pages" %}}) of the shared folders they have access to. Each user still allows each page before it runs. It applies at once: off, the pages are listed but do not open, and an open shared page stops at its next action. Start with `LOCALAI_DISABLE_SHARED_PAGES=true` to have it off from the start.

### Tools

The **Tools** tab lists the built-in chat tools, from `GET /api/chats/admin/tools`: `bash`, `web_search`, `web_fetch` and `list_agents`. An add-on may replace each of the first three. Each row says who serves the tool now (built-in, or an add-on) and has its switch:

- **Commands on this machine** (`chats_host_exec`, `off`, `ask` or `on`, applies at once; `LOCALAI_CHATS_HOST_EXEC`): who gets the chats' built-in `bash` when no Sandbox add-on serves `bash`. It runs commands directly on this machine **with no isolation and with its network**: they can read what LocalAI can read and reach the internet, your network and the LocalAI API. `ask` gives it to administrators, and each command waits for their yes in the chat; `on` gives it to every user with no card. Empty is the default: `off` with accounts, `ask` without. Every command is audited. See [Commands on this machine]({{%relref "features/chats#commands-on-this-machine" %}}).
- **Web search** (`chats_web_search`, default on, applies at the next turn; its row in the tab) and **Read web pages** (`chats_web_fetch`, default on; its row in the tab): offer the built-in `web_search` and `web_fetch` tools to chats. See [Web search and web pages in chats]({{%relref "features/web" %}}).
- **Saved agents in chats** (`chats_saved_agents`, default on, applies at the next turn; its row in the tab): let a chat list your saved agents (`list_agents`) and start one as a sub-agent. Each chat must still switch it on in its Tools menu, and the user needs the `agents` feature. See [Saved agents as sub-agents]({{%relref "features/chats#saved-agents-as-sub-agents" %}}).
- **SearXNG instance** (`chats_web_searxng_url`, default empty): send the searches of `web_search` to this SearXNG instance instead of DuckDuckGo. The instance must allow the JSON format.
- **Web pages may be on private networks** (`chats_web_allow_private`, default off): let `web_fetch` reach addresses inside your network. The cloud metadata addresses stay refused. Turn it on only if you trust every user of chats.
- **Ports for web pages** (`chats_web_ports`, default `80,443`): the ports `web_fetch` may use, comma separated. A value that is not a list of ports is refused.
- **Sandbox network** (applies at the next connection of a command; an allow the proxy cached lives at most 30 seconds). The commands a chat runs in the Sandbox reach the network only through the proxy of the Sandbox add-on, and these settings bound it. They do not apply to commands on this machine (`chats_host_exec`), which have the network of the server. See [Network access]({{%relref "features/addons#network-access" %}}).
  - `chats_egress_ceiling` (`off`, `registries` or `all`, default `off`): the widest preset any chat may use. `registries` admits the package registries only; `all` admits every public host. A chat chooses its own preset up to this.
  - `chats_egress_default` (`off`, `registries` or `all`, default `off`): the preset a new chat starts with, capped by the ceiling.
  - `chats_egress_allow_private` (default off): let a host that an administrator allowed, by a rule or a card answer, reach addresses inside your network. A preset alone never does, and the cloud metadata addresses and the server's own addresses stay refused. Turning it on needs `chats_egress_private_confirm` in the same request, with the phrase *I understand that commands can reach my private network*; the phrase is not stored. Settings > Tools asks for it.
  - `chats_egress_ports` (default `80,443`): the ports a command may reach, comma separated. Ports 22 and 25 are refused.
  - `chats_egress_extra_registries` (default empty): exact host names added to the package registries, comma separated, at most 64; for example an internal mirror (which also needs the private switch, since its address is private). No patterns.
  - `chats_egress_audit_days` (1 to 365, default 30): how long the audit of the network requests keeps a line.
- **Add-ons that replace a built-in tool** (`chats_tool_providers`, a map such as `{"bash": "sandbox", "xlsx": "office"}`, applies at once): when several add-ons replace one tool, the one that serves it. The keys are the replaceable tools (`bash`, `web_search`, `web_fetch`) and the replaceable built-in skills (`xlsx`, `docx`, `pptx`, `pdf`). A chosen add-on that cannot serve a chat (stopped, off for the chat) leaves the name to the built-in, not to another add-on. With none chosen, the add-on installed first serves (ties go by add-on ID), and the tab marks it as the default and says that you should choose. See [Tools that replace a built-in tool]({{%relref "features/addons#tools-that-replace-a-built-in-tool" %}}).

> **Note:** Most collection settings require a restart to take effect. The settings of the removed agent pool, agent jobs and agent hub (`agent_job_retention_days`, `agent_pool_enable_logs`, `agent_pool_agent_hub_url` and the like) are ignored when found in `runtime_settings.json`.

## Configuration Persistence

All settings are automatically saved to `runtime_settings.json` in the `LOCALAI_CONFIG_DIR` directory (default: `BASEPATH/configuration`). This file is watched for changes, so modifications made directly to the file will also be applied at runtime.

## Settings Precedence

Every runtime setting follows a single precedence rule:

1. **Environment variables and CLI flags** (highest priority)
2. **Configuration files** (`runtime_settings.json`, `api_keys.json`)
3. **Default values** (lowest priority)

The same rule is applied identically in all three places where settings can change:

- **At boot**: values persisted in `runtime_settings.json` fill in every setting that was not explicitly set via an environment variable or CLI flag.
- **On `POST /api/settings`** (the web UI Settings page): if a setting is controlled by an environment variable, it cannot be modified through the web interface. The settings page will indicate when a setting is controlled by an environment variable.
- **On manual file edits**: the configuration watcher hot-applies edits to `runtime_settings.json` with the same env-over-file semantics as boot, so editing the file by hand behaves like restarting with that file. For example, hand-editing `vram_budget` installs the new VRAM allocation cap live, without a restart.

### Known Limitations

- An environment variable explicitly set to its default value is indistinguishable from an unset one, so the value from `runtime_settings.json` wins in that case. In particular, an explicit `LOCALAI_THREADS=0` behaves like an unset `LOCALAI_THREADS`.
- A field previously changed via the API (or the web UI) looks environment-set to the file watcher, so a manual file edit of that same field is not hot-applied: it takes effect on the next restart.

## Example Configuration

The `runtime_settings.json` file follows this structure:

```json
{
  "watchdog_enabled": true,
  "watchdog_idle_enabled": true,
  "watchdog_busy_enabled": false,
  "watchdog_idle_timeout": "15m",
  "watchdog_busy_timeout": "5m",
  "max_active_backends": 0,
  "force_eviction_when_busy": false,
  "lru_eviction_max_retries": 30,
  "lru_eviction_retry_interval": "1s",
  "threads": 8,
  "context_size": 2048,
  "artifact_download_concurrency": 4,
  "max_concurrent_backend_operations": 2,
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
      "url": "https://index.localai.io/models",
      "mirrors": ["github:mudler/LocalAI/gallery/index.yaml@master"],
      "name": "localai"
    }
  ],
  "backend_galleries": [
    {
      "url": "https://index.localai.io/backends",
      "mirrors": ["github:mudler/LocalAI/backend/index.yaml@master"],
      "name": "localai"
    }
  ],
  "autoload_galleries": true,
  "autoload_backend_galleries": true,
  "vram_persistent_cache": true,
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

Changes to these files are automatically detected and applied without requiring a restart, using the same precedence rule described in [Settings Precedence](#settings-precedence).

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
