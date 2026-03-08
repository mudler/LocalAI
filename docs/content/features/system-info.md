+++
disableToc = false
title = "System Info and Version"
weight = 23
url = "/features/system-info/"
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
      "id": "my-llama-model"
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
