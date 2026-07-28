+++
disableToc = false
title = "Voice Activity Detection (VAD)"
weight = 35
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

The `/v1/vad` endpoint expects the `audio` field to be an array of raw
16kHz mono PCM samples as `float32` values, so the request body is usually
built from a real audio file rather than typed by hand.

First convert any audio file to 16kHz mono with ffmpeg:

```bash
ffmpeg -i input.mp3 -ar 16000 -ac 1 -f wav speech.wav
```

Then load the samples and POST them (this snippet needs
`pip install soundfile numpy requests`):

```python
import soundfile as sf
import numpy as np
import requests

audio, sample_rate = sf.read("speech.wav")
if audio.ndim > 1:
    audio = audio.mean(axis=1)  # downmix to mono
samples = audio.astype(np.float32).tolist()

response = requests.post(
    "http://localhost:8080/v1/vad",
    json={"model": "silero-vad", "audio": samples},
)
print(response.json())
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
