# yue2

YuE2 makes a song with vocals from lyrics and a style. Id: `audio-cpp-yue2-3b` (audio-cpp). Not tested: the backend was not installed on the test server. Licence is non-commercial (CC-BY-NC-4.0).

## Good at
- Full songs with sung lyrics, English and Chinese (upstream).

## Prompt shape
`caption`: comma separated style tags, e.g. `Jazz-funk, warm lead vocal, Rhodes piano, electric bass, tight drums`. The backend sends it as `style`, and it must not be empty. `lyrics`: text with a tag line before each section: `[verse]`, `[chorus]`, `[bridge]`. Put lyrics in the language you want sung.

## Example prompts
From the docs, not tested:
- caption `acoustic folk, warm vocals, gentle guitar`, lyrics `[Verse]\nMorning light across the bay\nCarry every cloud away`
- caption `Jazz-funk, warm lead vocal, Rhodes piano, electric bass, tight drums` (upstream example), your own lyrics

## Parameters to touch
- `caption`, `lyrics`. `duration_seconds`: forwarded, but whether YuE2 follows it is unverified; check the length with `ffprobe`.
- `instrumental`, `bpm`, `keyscale`: not read by this model. Put tempo and instruments in the caption.
- The `cot` mode and `cfg_scale` of upstream cannot be set from the tool.

## Known failure cases
- A 10 s request may return a longer clip (unverified). Cut it with ffmpeg.
- Lyrics without section tags: unverified.
