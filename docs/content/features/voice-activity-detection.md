+++
disableToc = false
title = "Voice Activity Detection (VAD)"
weight = 17
url = "/features/voice-activity-detection/"
+++

Voice Activity Detection (VAD) identifies segments of speech in audio data. LocalAI provides a `/v1/vad` endpoint powered by the [Silero VAD](https://github.com/snakers4/silero-vad) backend.

## API

- **Method:** `POST`
- **Endpoints:** `/v1/vad`, `/vad`

### Request

The request body is JSON with the following fields:

| Parameter | Type       | Required | Description                              |
|-----------|------------|----------|------------------------------------------|
| `model`   | `string`   | Yes      | Model name (e.g. `silero-vad`)           |
| `audio`   | `float32[]`| Yes      | Array of audio samples (16kHz PCM float) |

### Response

Returns a JSON object with detected speech segments:

| Field              | Type      | Description                        |
|--------------------|-----------|------------------------------------|
| `segments`         | `array`   | List of detected speech segments   |
| `segments[].start` | `float`   | Start time in seconds              |
| `segments[].end`   | `float`   | End time in seconds                |

## Usage

### Example request

```bash
curl http://localhost:8080/v1/vad \
  -H "Content-Type: application/json" \
  -d '{
    "model": "silero-vad",
    "audio": [0.0012, -0.0045, 0.0053, -0.0021, ...]
  }'
```

### Example response

```json
{
  "segments": [
    {
      "start": 0.5,
      "end": 2.3
    },
    {
      "start": 3.1,
      "end": 5.8
    }
  ]
}
```

## Model Configuration

Create a YAML configuration file for the VAD model:

```yaml
name: silero-vad
backend: silero-vad
```

## Detection Parameters

The Silero VAD backend uses the following internal defaults:

- **Sample rate:** 16kHz
- **Threshold:** 0.5
- **Min silence duration:** 100ms
- **Speech pad duration:** 30ms

## Error Responses

| Status Code | Description                                       |
|-------------|---------------------------------------------------|
| 400         | Missing or invalid `model` or `audio` field       |
| 500         | Backend error during VAD processing               |
