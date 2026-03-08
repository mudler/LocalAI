+++
disableToc = false
title = "Token Metrics"
weight = 21
url = "/features/token-metrics/"
+++

The token metrics endpoint provides performance statistics for a loaded model, including token generation speed and prompt processing counts.

{{% notice note %}}
This endpoint is currently **not wired into the HTTP router** and is not yet available for use. It is implemented but awaiting middleware integration. This documentation is provided for reference and future use.
{{% /notice %}}

## API

- **Method:** `GET`
- **Endpoints:** `/tokenMetrics`, `/v1/tokenMetrics` (planned)

### Request

The request body is JSON:

| Parameter | Type     | Required | Description                         |
|-----------|----------|----------|-------------------------------------|
| `model`   | `string` | Yes      | Name of the model to get metrics for |

### Response

Returns a JSON object with token generation metrics:

| Field                     | Type     | Description                            |
|---------------------------|----------|----------------------------------------|
| `slot_id`                 | `int`    | Slot identifier                        |
| `prompt_json_for_slot`    | `string` | The prompt stored as a JSON string     |
| `tokens_per_second`       | `float`  | Token generation rate                  |
| `tokens_generated`        | `int`    | Total number of tokens generated       |
| `prompt_tokens_processed` | `int`    | Number of prompt tokens processed      |

### Example response

```json
{
  "slot_id": 0,
  "prompt_json_for_slot": "{\"prompt\": \"Hello world\"}",
  "tokens_per_second": 42.5,
  "tokens_generated": 128,
  "prompt_tokens_processed": 12
}
```

### Planned usage

```bash
curl http://localhost:8080/v1/tokenMetrics \
  -H "Content-Type: application/json" \
  -d '{"model": "my-model"}'
```

## Error Responses

| Status Code | Description                                   |
|-------------|-----------------------------------------------|
| 400         | Missing or invalid model name                 |
| 500         | Backend error or model not loaded             |
