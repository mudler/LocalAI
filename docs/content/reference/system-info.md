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
| `loaded_models[].size_vram` | `integer` | Optional DRM-accounted resident device memory, in bytes |

### Per-model VRAM

On Linux, `size_vram` reports resident device memory for the local backend
process and its child processes. LocalAI reads `drm-resident-local*` and
`drm-resident-vram*` from `/proc` and counts each DRM client once per GPU.
Host-memory regions are excluded. The reading includes buffers attributed
to the backend, without separating weights, KV cache, and other allocations.
See the [kernel DRM accounting specification](https://docs.kernel.org/gpu/drm-usage-stats.html)
for these counters.

The field is omitted when accounting is unavailable or incomplete. This
includes external and distributed backends, macOS, proprietary NVIDIA
drivers, primary DRM nodes (`/dev/dri/card*`), missing resident counters,
and unreadable process information.
A present value of `0` means the supported counters report zero bytes.
Treat an absent field as unknown.

This is a snapshot of driver accounting, not a memory reservation. Shared
buffers can appear in different clients' counters, and allocations can change
during collection. Do not treat the sum across models as exclusive physical
GPU usage. These readings do not replace capacity checks when scheduling work.

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
      "size_vram": 5368709120,
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
