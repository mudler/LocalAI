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
| `loaded_models[].backend` | `string` | Backend name, when a model configuration is available |
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
      "size_vram": 5368709120
    },
    {
      "id": "whisper-1"
    }
  ]
}
```

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
