# minimax-h3

MiniMax-H3 makes video and sound together (MP4 with audio). Ids start with `minimax-h3`. The gallery entries were dropped from master: this card applies when the model is installed. Not tested: no clip was generated.

## Good at
- A 5 to 15 s clip with speech and ambience.
- A fixed first frame (`start_image`) or last frame (`end_image`).

## Prompt shape
One shot. Who or what, what moves, camera, light, what is heard. For speech: say the subject talks, put the words in double quotes, end with `Audio: ...` describing the voice. Without it you get room tone.

## Example prompts
From the docs, not tested:
- `A cyan llama mascot in a grey office chair, talking to the camera. It says, clearly and audibly: "the printer is down again". Audio: one clear close-miked voice.`
- With `start_image` set: `the subject turns toward the camera and starts speaking`

## Parameters to touch
- `seconds`: trained range 124 to 362 frames at 24 fps; frames round up to 17n+5. `size`: multiples of 32.
- `ref_images`, `ref_videos`, `ref_audios`: only `ref2va` ids; not with `start_image`/`end_image`.
- `negative_prompt`, `cfg_scale`, `fps`: ignored.

## Known failure cases
- Very slow: minutes to hours. One clip, and warn the person.
- Text inside the frame is malformed. Add it afterwards.
- Reference input to a `fl2va` id is refused. Use `start_image`.
