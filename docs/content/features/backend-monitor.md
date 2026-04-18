+++
disableToc = false
title = "Backend Monitor"
weight = 20
url = "/features/backend-monitor/"
+++

LocalAI provides endpoints to monitor and manage running backends. The `/backend/monitor` endpoint reports the status and resource usage of loaded models, and `/backend/shutdown` allows stopping a model's backend process.

## Monitor API

- **Method:** `GET`
- **Endpoints:** `/backend/monitor`, `/v1/backend/monitor`

### Request

The model to monitor is passed as a query parameter:

| Parameter | Type     | Required | Location | Description                    |
|-----------|----------|----------|----------|--------------------------------|
| `model`   | `string` | Yes      | query    | Name of the model to monitor   |

For backwards compatibility, a JSON body with the same field is still accepted when the `model` query parameter is not set, but new clients should use the query parameter.

### Response

Returns a JSON object with the backend status:

| Field                | Type     | Description                                           |
|----------------------|----------|-------------------------------------------------------|
| `state`              | `int`    | Backend state: `0` = uninitialized, `1` = busy, `2` = ready, `-1` = error |
| `memory`             | `object` | Memory usage information                              |
| `memory.total`       | `uint64` | Total memory usage in bytes                           |
| `memory.breakdown`   | `object` | Per-component memory breakdown (key-value pairs)      |

If the gRPC status call fails, the endpoint falls back to local process metrics:

| Field            | Type    | Description                    |
|------------------|---------|--------------------------------|
| `memory_info`    | `object`| Process memory info (RSS, VMS) |
| `memory_percent` | `float` | Memory usage percentage        |
| `cpu_percent`    | `float` | CPU usage percentage           |

### Usage

```bash
curl "http://localhost:8080/backend/monitor?model=my-model"
```

### Example response

```json
{
  "state": 2,
  "memory": {
    "total": 1073741824,
    "breakdown": {
      "weights": 536870912,
      "kv_cache": 268435456
    }
  }
}
```

## Shutdown API

- **Method:** `POST`
- **Endpoints:** `/backend/shutdown`, `/v1/backend/shutdown`

### Request

| Parameter | Type     | Required | Description                     |
|-----------|----------|----------|---------------------------------|
| `model`   | `string` | Yes      | Name of the model to shut down  |

### Usage

```bash
curl -X POST http://localhost:8080/backend/shutdown \
  -H "Content-Type: application/json" \
  -d '{"model": "my-model"}'
```

### Response

Returns `200 OK` with the shutdown confirmation message on success.

## Error Responses

| Status Code | Description                                    |
|-------------|------------------------------------------------|
| 400         | Invalid or missing model name                  |
| 500         | Backend error or model not loaded              |
