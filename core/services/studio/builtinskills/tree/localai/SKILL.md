---
name: localai
description: Use when the user wants an image, speech, a cloned voice, sound or music, a video, a transcript, a description of an image, embeddings, object detection, reranking, or voice or face matching, made with the models installed on this LocalAI server.
---

# LocalAI tools

These tools run the models installed here, as the user, within the user's own permissions. They spend the owner's compute. Call one only when the user asks for its result, once per request. Do not loop, retry in a cycle, or batch many calls unless the user asks.

## Files

A file argument is a reference: a path in the user's files, or the `ref` that an earlier tool returned. A tool that makes a file returns its `ref`; give the user that file and pass the `ref` to the next tool. Never put file bytes, base64 or long vectors in a call or in your answer. For a large archive, tell the user to choose Extract here in the Files page menu; it unpacks on the server as one change that Undo takes back.

## Tools

- `generate_image` (`prompt`, `size`, `negative_prompt`, `steps`, `n`, `image`, `mask`, `model`): a new image. To edit one, give `image` and `mask` together (white in the mask is repainted); `mask` alone is an error.
- `speak` (`text`, `voice`, `language`, `format`, `speed`, `instructions`, `model`): text to speech, saved as an audio file.
- `transcribe_audio`: a recording to text. Use the arguments the tool shows.
- `describe_image`: describe an image or answer a question about it, including text in it. Use the arguments the tool shows.
- `generate_sound` (`text`, `duration_seconds`, `lyrics`, `instrumental`, `model`): a sound effect or music from a description. There is no separate music tool. What you get depends on the installed model: a sound model may make effects only, a song model takes `lyrics`. Say so if the result is not what was asked.
- `generate_video` (`prompt`, `size`, `seconds`, `fps`, `negative_prompt`, `start_image`, `model`): a short video, optionally from an image. It can take minutes.
- `embed` (`input`): vectors for texts, saved as a JSON file. Show the count and size, not the numbers.
- `detect` (`image`, `prompt`, `threshold`): objects with boxes, classes and confidence.
- `rerank` (`query`, `documents`, `top_n`): documents ordered by how well they answer the query.
- `voice` (`action`, `audio`, `audio2`, `top_k`, `threshold`): `verify` compares `audio` and `audio2`; `identify` finds who speaks in `audio`.
- `face` (`action`, `image`, `image2`, `top_k`, `threshold`): `verify` compares `image` and `image2`; `identify` finds whose face is in `image`.
- `voice_profile` (`action`, `name`, `audio`, `transcript`, `language`, `id`): voices that `speak` can clone. Actions: `create`, `list`, `delete`. Profiles created by an administrator are visible to all users.

## Clone a voice

1. Ask the person for a short WAV clip of the voice (1 to 120 seconds) and the exact words said in it.
2. Call `voice_profile` with `action` `create`, a `name`, the clip as `audio` and the words as `transcript`. A card asks the person to confirm that the speaker agreed. Nothing exists until they confirm. Do not say the voice is ready before that.
3. After they confirm, call `voice_profile` with `action` `list` and copy the exact `voice` string of the profile.
4. Call `speak` with `text` and that `voice`.

Creating a voice profile is refused in messaging chats and in runs that start by themselves, because nobody can confirm there. Say that the person must do it in a Studio chat.

Never clone a voice without that confirmation, and do not try to get around it. Use `delete` with the `id` when the person asks to remove a voice.

## Chains

- Describe a picture aloud: `describe_image`, then `speak` with the description.
- Summarize a recording: `transcribe_audio`, then summarize the text yourself.
- Edit a picture: `generate_image` for the base, then `generate_image` with that `ref` as `image` and a mask.

## When a call fails

Quote the error to the user in plain words and stop. Do not retry.

- No model installed for the task: say which kind is missing. An administrator can install one from the model gallery.
- 403: this account may not use that feature or model. An administrator can allow it.
- 429: the quota is used up or the server is busy. Suggest trying later.
