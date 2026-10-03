+++
disableToc = false
title = "Voice Activity Detection (VAD)"
weight = 35
url = "/features/voice-activity-detection/"
+++

Voice Activity Detection (VAD) identifies segments of speech in audio data. LocalAI provides a `/v1/vad` endpoint powered by the [Silero VAD](https://github.com/snakers4/silero-vad) backend.

The [audio.cpp backend]({{%relref "features/audio-cpp" %}}) also serves this endpoint, and ships the `silero_vad` and `marblenet_vad` assets inside its own package, so VAD works there with nothing to download (`model: bundled:silero_vad` plus the `family:silero_vad` option).

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

Detection parameters can be overridden via model `options` (`key:value` entries):

```yaml
name: silero-vad
backend: silero-vad
options:
  - threshold:0.55
  - min_silence_duration_ms:50
  - speech_pad_ms:450
```

Supported options:

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `threshold` | float | `0.5` | Speech probability threshold |
| `min_silence_duration_ms` | int | `100` | Minimum silence before ending a speech segment |
| `speech_pad_ms` | int | `30` | Padding added around each speech segment |

Thresholds must be greater than 0 and less than 1. Durations must be nonnegative integers.
Malformed values, negative durations, and NaN thresholds are ignored; the default or last valid value remains in use.

Reload the model (or restart LocalAI) after changing these options.

## parakeet-cpp backend

The `parakeet-cpp` backend serves the same endpoint. It runs one of two detectors:

- **Silero VAD** from a GGUF file (gallery entry `parakeet-cpp-silero-vad-f16`, 1.3 MB). One probability per 32 ms.
- **The VAD head** of a Moondream Ultra or Redux model (gallery entries `parakeet-cpp-vad-moondream-ultra-q8_0` and `parakeet-cpp-vad-moondream-redux-packed`). One probability per 80 ms. The packed Redux file runs on CPU only.

The entry `parakeet-cpp-vad` installs Silero. The detectors differ and are not variants of one model, so install the entry of the VAD head by name if you want it. The request is the same as above: `audio` is 16 kHz mono float32 PCM, and the response lists `segments` with `start` and `end` in seconds. An ASR model that has no VAD head fails the request with `model has no VAD head`.

```yaml
name: parakeet-vad
backend: parakeet-cpp
known_usecases:
  - vad
parameters:
  model: parakeet-cpp/silero-vad-f16.gguf
options:
  - vad_threshold:0.5
  - vad_min_pause:0.1
```

All options are optional. An unset value keeps the default of the detector in use (the library defaults differ between Silero and the head):

| Option | Unit | Silero default | Head default | Description |
|--------|------|---------------:|-------------:|-------------|
| `vad_threshold` | 0 to 1 | `0.5` | `0.5` | Speech probability threshold |
| `vad_min_pause` | seconds | `0.1` | `0.2` | A silence this long separates two segments; shorter gaps merge |
| `vad_min_speech` | seconds | `0.25` | `0.1` | Shorter speech runs are dropped |
| `vad_speech_pad` | seconds | `0.03` | `0` | Padding added around each segment |

Option names differ from the Silero backend above (`min_silence_duration_ms` and `speech_pad_ms` are in milliseconds there). The same options tune transcription with `vad:true` or `vad_model`; see [audio to text]({{%relref "features/audio-to-text" %}}). Requests on one loaded model run one at a time.

## Detection Parameters

The Silero VAD backend uses the following internal defaults (overridable via `options` above):

- **Sample rate:** 16kHz
- **Threshold:** 0.5
- **Min silence duration:** 100ms
- **Speech pad duration:** 30ms

## Error Responses

| Status Code | Description                                       |
|-------------|---------------------------------------------------|
| 400         | Missing or invalid `model` or `audio` field       |
| 500         | Backend error during VAD processing               |
