---
name: media-prompting
description: Use before you write a prompt for generate_image, generate_video, generate_sound or speak, or when a generated image, clip, song or voice came out wrong.
---

# Prompting the media models

Each model family reads prompts a little differently. Read the card of the model you picked before you write the prompt. The cards are in `/workspace/shared-builtin-skills/media-prompting/cards/`: read one with read_file.

## Loop

1. `list_models` with the capability (image, video, sound_generation, tts). Pick by capability and name. Never guess a name.
2. Find the family in the table below and read its card. No card: use the anatomy below and say that the model is untested.
3. Generate once. Do not generate a batch before you have looked at the first result.
4. Inspect. Image: `describe_image`, and look at it. Video: `ffprobe` and one frame grab (load_skill assemble-media). Audio: `ffprobe` for the length; you cannot hear it, so say that you did not listen.
5. Change one thing, then generate again. Keep the `seed` when you compare two prompts or two settings; change it only to get a different take.
6. Stop after a few tries. Say what worked and what did not.

## Cards

| Model id contains | Card |
|---|---|
| `qwen-image` | `cards/qwen-image.md` |
| `minimax-h3` | `cards/minimax-h3.md` |
| `yue2` | `cards/yue2.md` |
| `qwen3-tts` | `cards/qwen3-tts.md` |

## Prompt anatomy

Image: subject and what it does, style or medium, composition and camera, light, then text to render in double quotes. Say what you want, not what to avoid. One scene per prompt.

Video: shot type, subject and its motion, camera move, setting and light, length. For a clip that continues another, set `start_image` to the last frame of the first clip (grab it with ffmpeg) and describe only the new action. `end_image` fixes the last frame. If the model makes sound, say what is heard and put spoken words in quotes.

Music: `caption` is the style: genre, mood, instruments, voice, tempo, comma separated. `lyrics` is the text, with a structure tag on its own line before each section (`[verse]`, `[chorus]`, `[bridge]`, `[outro]`). `instrumental` for no voice. Say the language when it is not English. Which fields a model reads depends on the model: see its card.

Speech: write the text as it should be said. Spell out numbers, dates, units and abbreviations. Break long text into sentences. Set `language` when it is not English. `instructions` describe the voice or the tone. `voice` is a named speaker or a profile from `voice_profile`.

## What the settings do

- `steps`: more steps refine detail and cost time in proportion. Distilled or turbo models are made for a few steps (about 4 to 8): more steps do not help them. Full models use about 20 to 50.
- Guidance (`cfg_scale` on video, set in the model file for images): higher follows the prompt more closely, too high gives burnt colors. Turbo models use 1 or none. Typical for full models: 3 to 7. Check the card.
- `size`: use the sizes of the card. A large size costs time and memory in proportion to its area. Most models need multiples of 32 or 64.
- `seed`: the same seed, model, prompt and size give the same result. It does not carry a subject from one prompt to another.
- `negative_prompt`: only models with guidance use it. Some models ignore it. Keep it short.
- `duration_seconds`, `seconds`: a model has a trained range. A request outside it can be rounded or give poor results.

## What to refuse or redirect

- Exact text, logos, hands, many small objects and counts of more than three are unreliable. Say so, and offer to add text afterwards with ImageMagick (assemble-media).
- A model cannot keep one character the same across separate generations unless it takes reference images. Use `ref_images` or `start_image` where the model supports them, and say it is approximate.
- There is no tool to edit a video, to change a song or to clone a voice without a confirmed profile. Generate new, or cut and mix with assemble-media.
- Long videos and songs are made of pieces: generate each one, then join them.
- Real people, living artists' styles and private voices: do not imitate them. Offer a generic description.
- A request that needs a model that is not installed: say which kind is missing. An administrator can install it from the model gallery.

## Add a card

Copy one card, name it `cards/<family>.md`, and add one row to the table above (a part of the model id, then the card). Keep it under 1.5 KB. Use these headings, in this order:

1. `# <family>`, then one line: what the family is, and the model ids you saw.
2. `Good at`: what it does well.
3. `Prompt shape`: how to write the prompt.
4. `Example prompts`: two, concrete. Mark each as tested or `from the docs, not tested`.
5. `Parameters to touch`: only parameters the tool takes, with ranges you tested or took from the docs.
6. `Known failure cases`: what went wrong. Write `unverified` after a claim that you did not test.
