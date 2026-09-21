+++
disableToc = false
title = "System Info and Version"
weight = 23
url = "/reference/system-info/"
+++

LocalAI provides endpoints to inspect the running instance, including available backends, loaded models, and version information.

## System Information

- **Method:** `GET`
- **Endpoint:** `/system`

Returns available backends and currently loaded models.

### Response

| Field           | Type     | Description                               |
|-----------------|----------|-------------------------------------------|
| `backends`      | `array`  | List of available backend names (strings) |
| `loaded_models` | `array`  | List of currently loaded models           |
| `loaded_models[].id` | `string` | Model identifier                    |
| `loaded_models[].backend` | `string` | Backend serving the model, from its config. Omitted when the model was loaded without one |
| `loaded_models[].process` | `object` | The backend process serving the model on this host. Omitted when there is no local process (a distributed worker holds the model) or it could not be read |
| `loaded_models[].process.pid` | `integer` | Process ID |
| `loaded_models[].process.rss_bytes` | `integer` | Resident host memory, in bytes. Weights offloaded to a GPU are not included |
| `loaded_models[].process.memory_percent` | `number` | `rss_bytes` as a percentage of host RAM |
| `loaded_models[].process.cpu_percent` | `number` | Share of the whole host's CPU used since the previous call, 0-100. Omitted on the first call that sees the process, because there is no earlier reading to compare against |
| `loaded_models[].process.started_at` | `string` | When the process started (RFC 3339) |

### Usage

```bash
curl http://localhost:8080/system
```

### Example response

```json
{
  "backends": [
    "llama-cpp",
    "huggingface",
    "diffusers",
    "whisper"
  ],
  "loaded_models": [
    {
      "id": "my-llama-model",
      "backend": "llama-cpp",
      "process": {
        "pid": 48213,
        "rss_bytes": 5368709120,
        "memory_percent": 7.8,
        "cpu_percent": 42.5,
        "started_at": "2026-09-21T09:12:44Z"
      }
    },
    {
      "id": "whisper-1",
      "backend": "whisper"
    }
  ]
}
```

`cpu_percent` covers the time since the previous call to this endpoint, so a
dashboard polling every few seconds gets a current reading. The WebUI's
**Operate → This machine** page polls it every five seconds.

---

## Version

- **Method:** `GET`
- **Endpoint:** `/version`

Returns the LocalAI version and build commit.

### Response

| Field     | Type     | Description                                     |
|-----------|----------|-------------------------------------------------|
| `version` | `string` | Version string in the format `version (commit)` |

### Usage

```bash
curl http://localhost:8080/version
```

### Example response

```json
{
  "version": "2.26.0 (a1b2c3d4)"
}
```

## Error Responses

| Status Code | Description                  |
|-------------|------------------------------|
| 500         | Internal server error        |
