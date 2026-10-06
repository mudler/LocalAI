+++
disableToc = false
title = "Audio to Text"
weight = 30
url = "/features/audio-to-text/"
+++

Audio to text models are models that can generate text from an audio file.

The transcription endpoint allows to convert audio files to text. The endpoint supports multiple backends:
- **[whisper.cpp](https://github.com/ggerganov/whisper.cpp)**: A C++ library for audio transcription (default)
- **moonshine**: Ultra-fast transcription engine optimized for low-end devices
- **faster-whisper**: Fast Whisper implementation with CTranslate2
- **WhisperX**: Whisper transcription with word alignment and optional speaker diarization. Set `HF_TOKEN` and pass `diarize=true` to load WhisperX's gated pyannote diarization pipeline.
- **[parakeet-cpp](https://github.com/mudler/parakeet.cpp)**: A C++/ggml port of NVIDIA NeMo Parakeet (FastConformer TDT/CTC/RNNT/hybrid). Runs quantized GGUFs on CPU or GPU, emits word-level timestamps, and supports cache-aware streaming (the `realtime_eou` model surfaces end-of-utterance events). The same backend also loads Nemotron-3-Diarization (`/v1/audio/diarization`) and CED sound models (`/v1/audio/classification`), and can attach either as a companion to a transcription model.
- **llama-cpp**: Route transcription to any multimodal-audio GGUF model served by the `llama-cpp` backend (e.g. [Qwen3-ASR](https://huggingface.co/ggml-org/Qwen3-ASR-0.6B-GGUF), Voxtral, Qwen2-Audio). Under the hood the request is converted into a chat completion with the audio attached via the model's audio encoder - the same path the upstream llama.cpp server uses. Set `backend: llama-cpp` in the model YAML and point `mmproj` at the matching audio encoder.
- **voxtral**: Voxtral-family models served by a dedicated backend
- **[NeMo-Speech.cpp](https://github.com/NVIDIA/NeMo-Speech.cpp)**: NVIDIA's C++/ggml runtime for the Nemotron Speech models. Serves offline, streaming and live transcription, with VAD, punctuation, inverse text normalization and Sortformer speaker tags attached through model options, and covers diarization, speech synthesis and translation from the same backend. See the [NeMo-Speech.cpp backend]({{%relref "features/nemo-speech-cpp" %}}) page for the model options.
- **[audio.cpp](https://github.com/0xShug0/audio.cpp)**: Multi-family GGML audio engine. Serves transcription and forced alignment from families such as `nemotron_asr`, `qwen3_asr`, `citrinet_asr`, `higgs_audio_stt` and `voxtral_realtime`, and covers diarization, VAD, TTS and source separation from the same backend. See the [audio.cpp backend]({{%relref "features/audio-cpp" %}}) page for the model options.

The endpoint input supports all the audio formats supported by `ffmpeg`.

> Looking for **"who spoke when"** instead of a flat transcript? See [Speaker Diarization](/features/audio-diarization/) - `/v1/audio/diarization` returns time-stamped speaker segments and supports the `rttm` format used by `pyannote.metrics`.

## Usage

Once LocalAI is started and whisper models are installed, you can use the `/v1/audio/transcriptions` API endpoint.

For instance, with cURL:

```bash
curl http://localhost:8080/v1/audio/transcriptions -H "Content-Type: multipart/form-data" -F file="@<FILE_PATH>" -F model="<MODEL_NAME>"
```

## Example

Download one of the models from [here](https://huggingface.co/ggerganov/whisper.cpp/tree/main) in the `models` folder,
and create a YAML file for your model:

```yaml
name: whisper-1
backend: whisper
parameters:
  model: whisper-en
```

The transcriptions endpoint then can be tested like so:

```bash
## Get an example audio file
wget --quiet --show-progress -O gb1.ogg https://upload.wikimedia.org/wikipedia/commons/1/1f/George_W_Bush_Columbia_FINAL.ogg

## Send the example audio file to the transcriptions endpoint
curl http://localhost:8080/v1/audio/transcriptions -H "Content-Type: multipart/form-data" -F file="@$PWD/gb1.ogg" -F model="whisper-1"
```

Result:

```json
{
  "segments":[{"id":0,"start":0,"end":9640000000,"text":" My fellow Americans, this day has brought terrible news and great sadness to our country.","tokens":[50364,1222,7177,6280,11,341,786,575,3038,6237,2583,293,869,22462,281,527,1941,13,50846]},{"id":1,"start":9640000000,"end":15960000000,"text":" At 9 o'clock this morning, Mission Control and Houston lost contact with our Space Shuttle","tokens":[1711,1722,277,6,9023,341,2446,11,20170,12912,293,18717,2731,3385,365,527,8705,13870,10972,51162]},{"id":2,"start":15960000000,"end":16960000000,"text":" Columbia.","tokens":[17339,13,51212]},{"id":3,"start":16960000000,"end":24640000000,"text":" A short time later, debris was seen falling from the skies above Texas.","tokens":[316,2099,565,1780,11,21942,390,1612,7440,490,264,25861,3673,7885,13,51596]},{"id":4,"start":24640000000,"end":27200000000,"text":" The Columbia's lost.","tokens":[440,17339,311,2731,13,51724]},{"id":5,"start":27200000000,"end":29920000000,"text":" There are no survivors.","tokens":[821,366,572,18369,13,51860]},{"id":6,"start":29920000000,"end":32920000000,"text":" And board was a crew of seven.","tokens":[50364,400,3150,390,257,7260,295,3407,13,50514]},{"id":7,"start":32920000000,"end":39780000000,"text":" Colonel Rick Husband, Lieutenant Colonel Michael Anderson, Commander Laurel Clark, Captain","tokens":[28478,11224,21282,4235,11,28412,28478,5116,18768,11,20857,27270,75,18572,11,10873,50857]},{"id":8,"start":39780000000,"end":50020000000,"text":" David Brown, Commander William McCool, Dr. Cooltna Chavla, and Elon Ramon, a Colonel","tokens":[4389,8030,11,20857,6740,4050,34,1092,11,2491,13,8561,83,629,761,706,875,11,293,28498,9078,266,11,257,28478,51369]},{"id":9,"start":50020000000,"end":52800000000,"text":" in the Israeli Air Force.","tokens":[294,264,19974,5774,10580,13,51508]},{"id":10,"start":52800000000,"end":58480000000,"text":" These men and women assumed great risk in the service to all humanity.","tokens":[1981,1706,293,2266,15895,869,3148,294,264,2643,281,439,10243,13,51792]},{"id":11,"start":58480000000,"end":63120000000,"text":" And an age when Space Flight has come to seem almost routine.","tokens":[50364,400,364,3205,562,8705,28954,575,808,281,1643,1920,9927,13,50596]},{"id":12,"start":63120000000,"end":68800000000,"text":" It is easy to overlook the dangers of travel by rocket and the difficulties of navigating","tokens":[467,307,1858,281,37826,264,27701,295,3147,538,13012,293,264,14399,295,32054,50880]},{"id":13,"start":68800000000,"end":72640000000,"text":" the fierce outer atmosphere of the Earth.","tokens":[264,25341,10847,8018,295,264,4755,13,51072]},{"id":14,"start":72640000000,"end":78040000000,"text":" These astronauts knew the dangers and they faced them willingly.","tokens":[1981,28273,2586,264,27701,293,436,11446,552,44675,13,51342]},{"id":15,"start":78040000000,"end":83040000000,"text":" Knowing they had a high and noble purpose in life.","tokens":[25499,436,632,257,1090,293,20171,4334,294,993,13,51592]},{"id":16,"start":83040000000,"end":90800000000,"text":" Because of their courage and daring and idealism, we will miss them all the more.","tokens":[50364,1436,295,641,9892,293,43128,293,7157,1434,11,321,486,1713,552,439,264,544,13,50752]},{"id":17,"start":90800000000,"end":96560000000,"text":" All Americans today are thinking as well of the families of these men and women who have","tokens":[1057,6280,965,366,1953,382,731,295,264,4466,295,613,1706,293,2266,567,362,51040]},{"id":18,"start":96560000000,"end":100440000000,"text":" been given this sudden shock in grief.","tokens":[668,2212,341,3990,5588,294,18998,13,51234]},{"id":19,"start":100440000000,"end":102400000000,"text":" You're not alone.","tokens":[509,434,406,3312,13,51332]},{"id":20,"start":102400000000,"end":105440000000,"text":" Our entire nation agrees with you.","tokens":[2621,2302,4790,26383,365,291,13,51484]},{"id":21,"start":105440000000,"end":112360000000,"text":" And those you loved will always have the respect and gratitude of this country.","tokens":[400,729,291,4333,486,1009,362,264,3104,293,16935,295,341,1941,13,51830]},{"id":22,"start":112360000000,"end":116600000000,"text":" The cause in which they died will continue.","tokens":[50364,440,3082,294,597,436,4539,486,2354,13,50576]},{"id":23,"start":116600000000,"end":124240000000,"text":" Man kind is led into the darkness beyond our world by the inspiration of discovery and the","tokens":[2458,733,307,4684,666,264,11262,4399,527,1002,538,264,10249,295,12114,293,264,50958]},{"id":24,"start":124240000000,"end":127000000000,"text":" longing to understand.","tokens":[35050,281,1223,13,51096]},{"id":25,"start":127000000000,"end":131160000000,"text":" Our journey into space will go on.","tokens":[2621,4671,666,1901,486,352,322,13,51304]},{"id":26,"start":131160000000,"end":136480000000,"text":" In the skies today, we saw destruction and tragedy.","tokens":[682,264,25861,965,11,321,1866,13563,293,18563,13,51570]},{"id":27,"start":136480000000,"end":142080000000,"text":" As farther than we can see, there is comfort and hope.","tokens":[1018,20344,813,321,393,536,11,456,307,3400,293,1454,13,51850]},{"id":28,"start":142080000000,"end":149800000000,"text":" In the words of the prophet Isaiah, lift your eyes and look to the heavens who created","tokens":[50364,682,264,2283,295,264,18566,27263,11,5533,428,2575,293,574,281,264,26011,567,2942,50750]},{"id":29,"start":149800000000,"end":151640000000,"text":" all these.","tokens":[439,613,13,50842]},{"id":30,"start":151640000000,"end":159960000000,"text":" He who brings out the story hosts one by one and calls them each by name because of his great","tokens":[634,567,5607,484,264,1657,21573,472,538,472,293,5498,552,1184,538,1315,570,295,702,869,51258]},{"id":31,"start":159960000000,"end":163400000000,"text":" power and mighty strength.","tokens":[1347,293,21556,3800,13,51430]},{"id":32,"start":163400000000,"end":166400000000,"text":" Not one of them is missing.","tokens":[1726,472,295,552,307,5361,13,51580]},{"id":33,"start":166400000000,"end":173600000000,"text":" The same creator who names the stars also knows the names of the seven souls we mourn","tokens":[50364,440,912,14181,567,5288,264,6105,611,3255,264,5288,295,264,3407,16588,321,22235,77,50724]},{"id":34,"start":173600000000,"end":175600000000,"text":" today.","tokens":[965,13,50824]},{"id":35,"start":175600000000,"end":183160000000,"text":" The crew of the shuttle Columbia did not return safely to earth yet we can pray that all","tokens":[440,7260,295,264,26728,17339,630,406,2736,11750,281,4120,1939,321,393,3690,300,439,51202]},{"id":36,"start":183160000000,"end":185840000000,"text":" are safely home.","tokens":[366,11750,1280,13,51336]},{"id":37,"start":185840000000,"end":192600000000,"text":" May God bless the grieving families and may God continue to bless America.","tokens":[1891,1265,5227,264,48454,4466,293,815,1265,2354,281,5227,3374,13,51674]},{"id":38,"start":196400000000,"end":206400000000,"text":" [BLANK_AUDIO]","tokens":[50364,542,37592,62,29937,60,50864]}],
  "text":"My fellow Americans, this day has brought terrible news and great sadness to our country. At 9 o'clock this morning, Mission Control and Houston lost contact with our Space Shuttle Columbia. A short time later, debris was seen falling from the skies above Texas. The Columbia's lost. There are no survivors. And board was a crew of seven. Colonel Rick Husband, Lieutenant Colonel Michael Anderson, Commander Laurel Clark, Captain David Brown, Commander William McCool, Dr. Cooltna Chavla, and Elon Ramon, a Colonel in the Israeli Air Force. These men and women assumed great risk in the service to all humanity. And an age when Space Flight has come to seem almost routine. It is easy to overlook the dangers of travel by rocket and the difficulties of navigating the fierce outer atmosphere of the Earth. These astronauts knew the dangers and they faced them willingly. Knowing they had a high and noble purpose in life. Because of their courage and daring and idealism, we will miss them all the more. All Americans today are thinking as well of the families of these men and women who have been given this sudden shock in grief. You're not alone. Our entire nation agrees with you. And those you loved will always have the respect and gratitude of this country. The cause in which they died will continue. Man kind is led into the darkness beyond our world by the inspiration of discovery and the longing to understand. Our journey into space will go on. In the skies today, we saw destruction and tragedy. As farther than we can see, there is comfort and hope. In the words of the prophet Isaiah, lift your eyes and look to the heavens who created all these. He who brings out the story hosts one by one and calls them each by name because of his great power and mighty strength. Not one of them is missing. The same creator who names the stars also knows the names of the seven souls we mourn today. The crew of the shuttle Columbia did not return safely to earth yet we can pray that all are safely home. May God bless the grieving families and may God continue to bless America. [BLANK_AUDIO]"
}
```

---

You can also specify the `response_format` parameter to be one of `lrc`, `srt`, `vtt`, `text`, `json` or `verbose_json` (default):
```bash
## Send the example audio file to the transcriptions endpoint
curl http://localhost:8080/v1/audio/transcriptions -H "Content-Type: multipart/form-data" -F file="@$PWD/gb1.ogg" -F model="whisper-1" -F response_format="srt"
```

Result (first few lines):
```text
1
00:00:00,000 --> 00:00:09,640
My fellow Americans, this day has brought terrible news and great sadness to our country.

2
00:00:09,640 --> 00:00:15,960
At 9 o'clock this morning, Mission Control and Houston lost contact with our Space Shuttle

3
00:00:15,960 --> 00:00:16,960
Columbia.

4
00:00:16,960 --> 00:00:24,640
A short time later, debris was seen falling from the skies above Texas.

5
00:00:24,640 --> 00:00:27,200
The Columbia's lost.

6
00:00:27,200 --> 00:00:29,920
There are no survivors.
```

## Supported request parameters

In addition to `file` and `model`, the endpoint accepts the following multipart form fields, matching the [OpenAI audio transcription API](https://platform.openai.com/docs/api-reference/audio/createTranscription):

| Field | Description |
|---|---|
| `language` | ISO-639-1 language hint (e.g. `en`). Passed through to the backend. |
| `prompt` | Optional context hint to bias the decoder. |
| `temperature` | Sampling temperature (float). Honored by backends that support it. |
| `timestamp_granularities[]` | Multi-value form field: `word` and/or `segment`. Honored when the backend produces the requested granularity. |
| `response_format` | One of `json` (default for backwards-compat), `verbose_json`, `text`, `srt`, `vtt`, `lrc`. |
| `stream` | When `true`, the endpoint emits an SSE stream of `transcript.text.delta` events followed by a final `transcript.text.done` event. |
| `diarize` | LocalAI extension - speaker diarization. WhisperX requires `HF_TOKEN`; requests fail with `FailedPrecondition` when it is missing. |

If speaker diarization fails after transcription succeeded, the WhisperX backend logs the error and returns the transcript without speaker labels. Other transcription failures return an error instead of an empty transcript. Diarization still requires `HF_TOKEN`.

The response body for `verbose_json` includes `text`, `language`, `duration`, and `segments[]` (with `speaker` populated when diarization is enabled).

## Streaming transcriptions

Set `-F stream=true` to receive token-by-token SSE events as the backend produces them. The event shape matches the OpenAI streaming transcription format:

```bash
curl -N http://localhost:8080/v1/audio/transcriptions \
  -H "Content-Type: multipart/form-data" \
  -F file="@sample.wav" \
  -F model="whisper-1" \
  -F stream=true
```

```text
data: {"type":"transcript.text.delta","delta":"And so, my"}

data: {"type":"transcript.text.delta","delta":" fellow Americans..."}

data: {"type":"transcript.text.done","text":"And so, my fellow Americans..."}

data: [DONE]
```

Backends that do not natively stream tokens fall back to emitting one delta plus a done event with the full text - the SSE contract is identical either way.

## Using the llama-cpp backend with an audio-capable model

Any GGUF model whose `mmproj` contains an audio encoder can be used for transcription via the `llama-cpp` backend. This reuses the model's own audio front-end rather than shelling out to whisper.cpp, which is useful when you want a single backend serving both chat-with-audio and transcription.

Example using [`ggml-org/Qwen3-ASR-0.6B-GGUF`](https://huggingface.co/ggml-org/Qwen3-ASR-0.6B-GGUF):

```yaml
name: qwen3-asr
backend: llama-cpp
parameters:
  model: Qwen3-ASR-0.6B-Q8_0.gguf
mmproj: mmproj-Qwen3-ASR-0.6B-Q8_0.gguf
```

Then call `/v1/audio/transcriptions` as usual:

```bash
curl http://localhost:8080/v1/audio/transcriptions \
  -H "Content-Type: multipart/form-data" \
  -F file="@jfk.wav" \
  -F model="qwen3-asr"
```

## Using the parakeet-cpp backend

[parakeet.cpp](https://github.com/mudler/parakeet.cpp) is a C++/ggml port of NVIDIA NeMo Parakeet that matches the upstream PyTorch models on CPU. GGUF weights for every model and quant are published in a single repo, [`mudler/parakeet-cpp-gguf`](https://huggingface.co/mudler/parakeet-cpp-gguf). F16 is the recommended default, and Q4_K stays near-lossless on the small models. The easiest path is to import directly (the GGUFs auto-detect to this backend):

```bash
local-ai models import https://huggingface.co/mudler/parakeet-cpp-gguf/resolve/main/tdt_ctc-110m-f16.gguf
```

Or write a model YAML:

```yaml
name: parakeet-110m
backend: parakeet-cpp
parameters:
  model: tdt_ctc-110m-f16.gguf
```

Then call `/v1/audio/transcriptions` as usual. Pass `timestamp_granularities[]=word` for per-word timings:

```bash
curl http://localhost:8080/v1/audio/transcriptions \
  -H "Content-Type: multipart/form-data" \
  -F file="@jfk.wav" \
  -F model="parakeet-110m" \
  -F "timestamp_granularities[]=word"
```

For real-time use, load a cache-aware streaming model (e.g. `realtime_eou_120m-v1-*.gguf`) and pass `-F stream=true`. Deltas are emitted as the audio is decoded, with end-of-utterance events closing each segment.

### Diarization and sound classification

The same backend also serves the `/v1/audio/diarization` and `/v1/audio/classification` endpoints, and can attach a diarization or sound model to a live transcription session. `options:` accepts paths relative to the models directory, or absolute:

| Option | Allowed on | Used for |
|---|---|---|
| `asr_model:<path>` | a diarization model | `include_text` on `/v1/audio/diarization` |
| `diarization_model:<path>` | an ASR model | a `speaker` on transcript segments (and words), and speaker segments during realtime live transcription |
| `sound_model:<path>` | an ASR model | sound events during realtime live transcription |
| `diarization_latency:<model\|low\|very_low\|ultra_low>` | a model with a diarization companion | latency mode for the live speaker stream; default `low` |
| `speaker_model:<path>` | a model with a diarization model | names registered speakers; a bundle can use `speaker_component:<name>` instead (see [Bundle GGUF files](#bundle-gguf-files-several-models-in-one-file)) (see [Voice Recognition]({{% relref "voice-recognition" %}}#naming-speakers-in-diarization-and-live-transcription)) |
| `speaker_tag:<tag>` | a model with `speaker_component` | extra encoder tag for registered voices that carry only a file-name tag (see [Voice Recognition]({{% relref "voice-recognition" %}}#naming-speakers-from-a-bundle)) |
| `speaker_threshold:<float>` | a model with `speaker_model` | distance (1 minus cosine similarity) under which a speaker is named, in (0, 2); default `0.5` |
| `speaker_margin:<float>` | a model with `speaker_model` | how much the best match must beat the runner-up, in [0, 1); default `0.05` |
| `speaker_strict:<bool>` | a model with `speaker_model` | do not use registered voices that have no encoder fingerprint (see [Voice Recognition]({{% relref "voice-recognition" %}}#encoder-fingerprint)); default `false` |

With a `diarization_model` companion, `/v1/audio/transcriptions` labels each segment with its `speaker` (`"0"`, `"1"`, ... in order of first appearance) and splits segments where the speaker changes; with `timestamp_granularities[]=word` each word carries its speaker too. With `stream=true` the closing `transcript.text.done` event lists the segments with their speakers. Pass `-F diarize=false` to skip diarization for one request. The diarization GGUF can also be imported directly: `local-ai models import https://huggingface.co/mudler/parakeet-cpp-gguf/resolve/main/nemotron-3-diarization-f16.gguf`.

`speaker_model:` needs libparakeet with C-API v10. A wrong setup fails at load time with one of these errors: `parakeet-cpp: speaker_model needs libparakeet.so ABI 10 (parakeet_capi_speaker_registry_add_embedding); the loaded library is older`, `parakeet-cpp: speaker_model needs a diarization model (the primary or diarization_model:)`, `parakeet-cpp: a speaker model cannot be the primary model; use it as speaker_model: next to a diarization model`, `parakeet-cpp: speaker_model "<path>" is a <kind> model, expected a speaker model` (the file is not a speaker encoder GGUF), or `parakeet-cpp: speaker_threshold "<value>" must be a distance in (0, 2) (1 minus cosine similarity)` / `parakeet-cpp: speaker_margin "<value>" must be a number in [0, 1)` for a bad number.

The loader rejects a companion whose role duplicates the primary's own (for example `asr_model:` on an already-ASR primary, or `sound_model:` on a CED primary), and rejects a companion GGUF that does not match the role its option names (for example `sound_model:` pointing at an ASR GGUF fails to load, naming the kind it expected). See [Speaker Diarization]({{% relref "audio-diarization" %}}) for the `Diarize` RPC and [Sound Classification]({{% relref "audio-classification" %}}) for `SoundDetection`, and [Realtime API]({{% relref "openai-realtime" %}}) for the live speaker/sound events emitted during a realtime session.

### Segment timestamps

Transcriptions are split into segments the same way NVIDIA NeMo does: a new segment starts after sentence-ending punctuation (`.`, `?`, `!`), and each segment carries `start`/`end` times. This is the default (NeMo's punctuation-only segmentation) and needs no configuration. While streaming, each end-of-utterance closes a segment, now with timestamps.

You can additionally split on silence by setting `segment_gap_threshold` (NeMo's `segment_gap_threshold`, in **encoder frames**; off by default). When set, a gap between two words wider than the threshold also starts a new segment. The value is in frames to match NeMo exactly; the backend converts it to seconds using the model's frame stride (`frame_sec`, reported by the engine):

```yaml
name: parakeet-110m
backend: parakeet-cpp
parameters:
  model: tdt_ctc-110m-f16.gguf
options:
- segment_gap_threshold:12   # split on silence > 12 encoder frames (default 0 = off, punctuation-only)
```

### Dynamic batching

The backend can coalesce concurrent transcription requests into a single batched engine call, which improves throughput on GPU when many requests arrive at once. Batching is **off by default** (`batch_max_size:1`, one request at a time); raise it to opt in. Two `options:` knobs control it:

```yaml
name: parakeet-110m
backend: parakeet-cpp
parameters:
  model: tdt_ctc-110m-f16.gguf
options:
- batch_max_size:8      # max requests coalesced into one batch (default 1 = off)
- batch_max_wait_ms:15  # how long to wait to fill a batch, in ms (default 15)
```

By default each request runs on its own. Raise `batch_max_size` (for example 4 to 16) to enable batching; it pays off on GPU under concurrent load, where coalescing the per-step decode GEMMs across requests is a large throughput win. Leave it at 1 on CPU and for low-concurrency setups, where batching only adds latency. Batching only affects concurrent unary requests; streaming sessions always run on their own.

### Moondream Ultra and Redux

[Moondream](https://huggingface.co/moondream) publishes two derivatives of NVIDIA parakeet-tdt-0.6b-v3, Ultra and Redux. Both have a voice-activity-detection (VAD) head. The gallery has five entries, built from the GGUFs in [`mudler/parakeet-cpp-gguf`](https://huggingface.co/mudler/parakeet-cpp-gguf):

| Gallery entry | File | Runs on |
|---|---|---|
| `parakeet-cpp-moondream-ultra-f16` | `ultra-f16.gguf` | CPU and GPU |
| `parakeet-cpp-moondream-ultra-q8_0` | `ultra-q8_0.gguf` | CPU and GPU |
| `parakeet-cpp-moondream-redux-packed` | `redux-packed.gguf` | CPU only, offline only |
| `parakeet-cpp-moondream-redux-f16` | `redux-f16.gguf` | any backend, can stream |
| `parakeet-cpp-moondream-redux-q8_0` | `redux-q8_0.gguf` | any backend |

The packed Redux file stores the encoder as ternary weights (213 MB). It cannot load on a GPU backend and cannot stream. If a GPU build fails to load it, check the backend log for the library message and use the `redux-f16` or `redux-q8_0` entry instead. The weights are CC-BY-4.0: credit Moondream and NVIDIA.

#### VAD-only slices

If you only need the VAD head, for the [VAD endpoint]({{%relref "features/voice-activity-detection" %}}) or to cut audio before transcription, the same repository has two small files with the head cut out of the full model. The weights are not retrained, and the files cannot transcribe:

| Gallery entry | File | Size | Cut from |
|---|---|---|---|
| `parakeet-cpp-vad-moondream-redux` | `redux-vad.gguf` | 9.9 MB | Redux (213 MB packed to 1.4 GB) |
| `parakeet-cpp-vad-moondream-ultra` | `ultra-vad-q8_0.gguf` | 6.0 MB | Ultra Q8_0 |

Measured by the parakeet.cpp author against loading a whole Redux or Ultra model: the files are 6 to 10 MB instead of 213 MB to 1.4 GB, load in a few milliseconds instead of 0.1 to 0.7 s, and use about 245 MiB peak memory for a 33 s clip instead of 0.6 to 1.6 GiB. The output is byte-identical to the full parent model, and the speed is the same as the parent's head. A transcription request on a slice fails with an error. The slices load only with a parakeet.cpp build that includes VAD-only GGUF support, so an older backend build fails to load them. The weights are CC-BY-4.0: credit Moondream and NVIDIA.

With `vad:true`, long audio is cut at pauses found by the model's VAD head into pieces of at most 30 seconds, and each piece is transcribed in turn. Word timestamps stay relative to the whole file. Audio of 30 seconds or less gives the same result as without the option. The gallery entries set it. Add it to your own model YAML like this:

```yaml
name: moondream-ultra
backend: parakeet-cpp
parameters:
  model: ultra-q8_0.gguf
options:
- vad:true   # cut long audio at pauses (default false); needs a model with a VAD head
```

`vad:true` applies to offline transcription only and bypasses dynamic batching, because the batched entry point has no VAD variant. Streaming is not affected. A model without a VAD head fails each request with `model has no VAD head`, and a `libparakeet.so` that is too old to export the VAD entry point fails the load. Remove the option for models that have no VAD head.

### Cutting long audio with Silero (`vad_model`)

A model without a VAD head, such as `parakeet-cpp-tdt-0.6b-v3` or a Nemotron model, can cut long audio with [Silero VAD](https://github.com/snakers4/silero-vad) instead. Name a Silero GGUF in the `vad_model` option. The path is resolved against the models directory, like the other companion files. `vad_model` implies `vad`:

```yaml
name: parakeet-v3-silero
backend: parakeet-cpp
parameters:
  model: parakeet-cpp/tdt-0.6b-v3-f16.gguf
options:
- vad_model:parakeet-cpp/silero-vad-f16.gguf   # Silero GGUF that cuts long audio at pauses
- vad_min_pause:0.3                            # optional, seconds
```

The gallery entry `parakeet-cpp-tdt-0.6b-v3-silero-vad` installs both files with this configuration. Audio of 30 seconds or less is transcribed whole and the VAD does not run. `vad:true` alone keeps meaning "use the model's own head". With `vad_model` set, the Silero model is used even if the ASR model has a head.

The segmenter options below apply to both `vad:true` and `vad_model`. Each is optional; an unset value keeps the default of the detector in use, and a bad value fails the load:

| Option | Unit | Meaning |
|---|---|---|
| `vad_threshold` | 0 to 1 | A frame is speech when its probability is at least this |
| `vad_min_pause` | seconds | A silence this long separates two pieces |
| `vad_min_speech` | seconds | Shorter speech runs are dropped |
| `vad_max_segment` | seconds | Cap on the length of a piece (default 30) |
| `vad_trim` | seconds | Each piece shrinks to its first and last speech frame plus this much. Default `0.3`; `0` keeps the whole cuts, as before this option existed |
| `vad_run_gate` | 0 to below 1 | For the Moondream Ultra and Redux heads: a speech run is kept only if the median of its frame probabilities is at least this. Default off (`0`). Try `0.92` to `0.96` on Redux. Music still triggers the head, and Silero does not need it |

`vad_run_gate` is described, with its measured effect and an example for the Redux VAD-only entry, in the [VAD endpoint page]({{%relref "features/voice-activity-detection" %}}#run-gate-for-the-ultra-and-redux-heads). It is offline only and is not sent to plain transcription or to streaming. With `vad:true` it needs a `libparakeet.so` that exports `parakeet_capi_transcribe_path_json_vad_with`, and a set option on an older library fails the load.

`vad_speech_pad` (seconds) pads each region and only affects the [VAD endpoint]({{%relref "features/voice-activity-detection" %}}). `vad_model` needs a `libparakeet.so` that exports `parakeet_capi_transcribe_path_json_vad_with`; an older library fails the load with a message that names it.

### Dropping noise words (`guard_*`)

A decode of noise or silence can contain words that no one said. An opt-in filter removes the words that stand alone or sit among low-confidence words, and the words that are only punctuation. It runs on the finished decode. It is off unless one of these options is set, and a bad value fails the load:

| Option | Unit | Meaning |
|---|---|---|
| `guard_min_local_conf` | 0 to 1 | A word is dropped when the mean confidence of the words that start within `guard_local_radius` seconds of it, itself included, is below this. `0` is off. `0.5` is a good start; higher values also drop real words on some models |
| `guard_local_radius` | seconds, above 0 | The window of that mean. Default `5` |
| `guard_drop_punct_only` | `true` or `false` | Drop words that are only punctuation. A CTC model can emit a lone `.` on noise. Default `false` |

```yaml
options:
- guard_min_local_conf:0.5
- guard_drop_punct_only:true
```

The filter applies to offline transcription, and with `vad:true` or `vad_model` to each piece on its own. It bypasses dynamic batching, like `vad:true`. Streaming is not affected. Speech with confident words comes out the same as without the filter. The number of dropped words is written to the backend log at debug level; the transcription response has no field for it. The options need a `libparakeet.so` that exports `parakeet_capi_transcribe_path_json_with`; an older library fails the load with a message that names it.

### Bundle GGUF files (several models in one file)

A bundle is one GGUF file that holds several models, called components. Each component keeps its own licence. The backend opens the components it needs from the one file, so a single model YAML can serve transcription, VAD, diarization, speaker naming and sound events. A bundle needs a `libparakeet.so` from parakeet.cpp with bundle support (pin `781a973` or newer); the format is described in the [parakeet.cpp bundle documentation](https://github.com/mudler/parakeet.cpp/blob/master/docs/bundle.md). Single-model files and every existing option work as before.

The gallery has three bundles, built from [`mudler/parakeet-cpp-gguf`](https://huggingface.co/mudler/parakeet-cpp-gguf):

| Gallery entry | Size | Components | Serves |
|---|---|---|---|
| `parakeet-cpp-bundle-small` | 338 MB | Parakeet TDT+CTC 110M (Q8_0), Nemotron-3-Diarization (Q8_0), CED-Small (Q8_0), WeSpeaker ResNet34-LM (F32), Silero VAD (F16) | transcription, VAD, diarization, speaker naming, sound events |
| `parakeet-cpp-bundle-standard` | 1.1 GB | Parakeet TDT 0.6B v3 (Q8_0), plus the same four components | the same, with the multilingual 0.6B model |
| `parakeet-cpp-bundle-moondream-redux` | 215 MB | Moondream Redux (packed ternary), Silero VAD (F16) | transcription and VAD; CPU only and offline only |

The component that each role uses, and the option that picks another one:

| Role | Component used | Option |
|---|---|---|
| Transcription | the only `asr` component | `bundle_asr:<name>` picks one when the bundle has several |
| VAD (`/v1/vad`, and `vad:true` for long audio) | the `vad` (Silero) component, loaded with no option; without one the VAD head of the ASR model | `vad_component:<name>` picks one, and implies `vad:true` |
| Diarization | the `diar` component, only when asked for | `diar_component:<name>` |
| Sound events | the `ced` component, only when asked for | `sound_component:<name>` |
| Speaker naming | the `voice` component, only when asked for | `speaker_component:<name>` (needs a diarization component) |
| Voice embedding and verification (`/v1/voice/*`, the realtime `voice_recognition` stage) | the `voice` component, only when asked for | `speaker_component:<name>`; declare `speaker_recognition` in `known_usecases`. Needs a libparakeet.so with `parakeet_capi_speaker_embed_pcm`. See [Voice Recognition]({{% relref "voice-recognition#a-parakeet-cpp-bundle-as-the-embedding-model" %}}) |

`speaker_component:` also names registered speakers: LocalAI sends the voices from `/v1/voice/register` to the bundle's speaker component, as it does for `speaker_model:`. This works in `/v1/audio/diarization` and in realtime live transcription, and it needs no `speaker_model:`. `speaker_threshold`, `speaker_margin` and `speaker_strict` apply too. Only voices that carry an encoder fingerprint (voices enrolled from `speaker_profiles`) and voices with no tag at all are used by default. A voice registered with a tag only matches through `speaker_tag:`. See [Voice Recognition]({{% relref "voice-recognition" %}}#naming-speakers-from-a-bundle).

A `*_component` option without the matching companion option takes the component from the model file itself. The companion options (`diarization_model:`, `sound_model:`, `speaker_model:`, `vad_model:`, `asr_model:`) can also name a bundle file, even the same file as the model: the only component of the wanted kind is used, and the `*_component` option picks one when there are several. This YAML loads the same file for four roles:

```yaml
name: parakeet-bundle
backend: parakeet-cpp
parameters:
  model: parakeet-cpp/parakeet-bundle-small.gguf
options:
- vad:true                  # cut long audio at pauses, with the Silero component
- diar_component:diar       # same as diarization_model:parakeet-cpp/parakeet-bundle-small.gguf
- sound_component:ced
- speaker_component:voice
```

A role that the bundle cannot fill fails with a message that lists the components, for example `parakeet-cpp: diarization_model needs a "diar" component, but the bundle "<path>" has none (components: asr (asr), vad (vad))`. A request for a role the loaded model does not have (diarization from a bundle without a `diar` component) returns `parakeet-cpp: model is not a diarization model (the model file is a bundle without a "diar" component; ...)`. A `*_component` option on a file that is not a bundle, or on a library without bundle support, fails the load.

Licences: a bundle has no single licence, so the gallery entries use `license: other`. The licence, source and credit of every component are in the file header, and the NOTICE file next to each bundle in the repository (`NOTICE-<bundle>.txt`) has the credits and the full licence texts. Keep it with any copy of the file you pass on.

| Component | Licence | Credit |
|---|---|---|
| Parakeet TDT+CTC 110M, Parakeet TDT 0.6B v3 | CC-BY-4.0 | NVIDIA |
| Moondream Redux | CC-BY-4.0 | Moondream, derived from Parakeet TDT 0.6B v3 by NVIDIA |
| Nemotron-3-Diarization | OpenMDW-1.1 | NVIDIA |
| CED-Small | Apache-2.0, as stated on the model card | Heinrich Dinkel et al., Xiaomi (mispeech) |
| WeSpeaker ResNet34-LM | CC-BY-4.0 | the WeSpeaker project |
| Silero VAD | MIT | Silero Team |

The licence of the CED weights is not consistent upstream: the model card says Apache-2.0, the upstream code repository is GPL-3.0 and the original checkpoint records say CC-BY-4.0. The file here is converted, not trained, and follows the model card. The weights of all components were converted to GGUF, and quantised where the table shows it; nothing was retrained.

## See also

- [Audio Transform]({{< relref "audio-transform.md" >}}) - clean up the audio (echo cancellation, noise suppression, dereverberation) before passing it to a transcription model.
