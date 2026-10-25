+++
disableToc = false
title = "Time-Series Forecasting"
weight = 42
url = "/features/forecast/"
+++

LocalAI forecasts numeric time series with [timestral.cpp](https://github.com/mudler/timestral.cpp), a C++/ggml port of Mistral's Timestral models. You send one or more series, with optional covariates, to `POST /v1/forecast` and get quantile trajectories for the next steps.

A forecast model is not a chat model. It has its own usecase, `forecast`, and does not appear in the chat model picker. It shows in the gallery filter and in the `/v1/models` capability list.

> **Licence.** The Timestral weights use Mistral's Modified MIT licence. You may not use them if the global consolidated monthly revenue of your company (or your employer) exceeded USD 20 million in the preceding month, unless you get a commercial licence from Mistral AI. The restriction covers derivatives, so it also applies to the GGUF conversions in the gallery. Each gallery entry installs the licence text next to the weights. The timestral.cpp code is MIT.

## Models

| Gallery entry | Parameters | Builds |
|---|---|---|
| `timestral-5m` | 5 million | F32 (default), F16 |
| `timestral-28m` | 28 million | F32 (default), F16 |
| `timestral-123m` | 123 million | F32 (default), F16 |
| `timestral-209m` | 209 million | F32 (default), F16 |

```bash
local-ai models install timestral-28m
```

The default build is F32. Install the F16 entry, for example `timestral-28m-f16`, for a build of about half the size. Quantized builds below F16 are not offered yet.

The engine runs on the CPU only. It is slower than the PyTorch reference at long context lengths, so keep the context short where you can and allow a long request timeout.

How much memory each build needs has not been measured yet, so the gallery sets no size.

## Request

```bash
curl http://localhost:8080/v1/forecast \
  -H "Content-Type: application/json" \
  -d '{
    "model": "timestral-28m",
    "prediction_length": 4,
    "series": [
      { "id": "store-12", "target": [3.1, 3.4, null, 3.9, 4.2, 4.0, 4.4, 4.8] }
    ]
  }'
```

| Field | Type | Meaning |
|---|---|---|
| `model` | string | Name of a forecast model. |
| `prediction_length` | integer, at least 1 | Steps to forecast. |
| `quantiles` | list of numbers | Levels to return. Each must be a level the model was trained on. Default: `0.1`, `0.5`, `0.9`. |
| `max_context` | integer | Most history steps the model uses. `0` or absent keeps the model default. |
| `symmetric_averaging`, `clip_nonnegative` | boolean or `null` | `null` or absent keeps the model default. |
| `series` | list | The series to forecast. |

Each entry of `series` has these fields:

| Field | Shape | Meaning |
|---|---|---|
| `id` | string | Optional. Returned with the result. |
| `target` | `[variates][steps]`, or `[steps]` for one variate | History, oldest step first. `null` marks a missing value. |
| `past_covariates` | `[covariates][steps]` | Numeric covariates known only for the history. |
| `known_covariates` | `[covariates][steps + prediction_length]` | Numeric covariates that are also known for the future. |
| `past_categorical` | `[covariates][steps]` | Integer category codes for the history. `null` marks a missing code. |
| `known_categorical` | `[covariates][steps + prediction_length]` | Integer category codes that are also known for the future. |

Series in one request can have different lengths and different numbers of variates or covariates. LocalAI groups them by shape, runs one forecast for each group, and returns the results in the order you sent the series.

## Response

```json
{
  "object": "forecast",
  "model": "timestral-28m",
  "prediction_length": 4,
  "quantile_levels": [0.1, 0.5, 0.9],
  "data": [
    {
      "index": 0,
      "id": "store-12",
      "variates": [
        { "index": 0, "values": [[4.4, 4.5, 4.6, 4.6], [4.7, 4.8, 4.9, 5.0], [5.0, 5.2, 5.3, 5.4]] }
      ]
    }
  ]
}
```

`values[q][h]` is the value of level `quantile_levels[q]` at step `h`. Step 0 is the first step after the last step of the history. The numbers above are an example of the shape, not real output.

## Errors

The endpoint returns `400` when:

- `prediction_length` is less than 1, or `series` is empty.
- A row has the wrong number of steps, for example a known covariate that stops at the end of the history.
- A value is not a finite number, or a categorical code is not an integer. `NaN` and infinity are not valid JSON; use `null` for a missing value.
- A requested quantile is not a level of the model. The error lists the valid levels.
- The request is larger than the model limits.

## Model options

Set these under `options:` in the model configuration:

```yaml
name: timestral-28m
backend: timestral-cpp
parameters:
  model: timestral-28m-f32.gguf
threads: 4
options:
  - default_quantiles:0.1,0.5,0.9
  - max_context:0
  - max_series:256
  - max_total_points:2000000
```

| Option | Default | Meaning |
|---|---|---|
| `default_quantiles` | `0.1,0.5,0.9` | Levels returned when a request names none. |
| `max_context` | `0` | History cap used when a request sets none. `0` keeps the model default. |
| `max_series` | `256` | Most series in one request. |
| `max_total_points` | `2000000` | Most input values in one request. |

`threads` sets the number of CPU threads for each forecast.

The library is not reentrant, so LocalAI runs one forecast at a time for each loaded model.

## Platforms

The backend image is built for Linux on amd64 and arm64. A build for Apple silicon is wired into the build matrix, but it has not been built or tested yet.
