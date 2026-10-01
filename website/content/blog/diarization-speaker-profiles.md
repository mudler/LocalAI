---
title: "Diarization in LocalAI, from speaker turns to remembering a voice"
date: 2026-10-01
author: "Ettore Di Giacinto"
category: "Engineering"
tags: ["parakeet.cpp", "diarization", "ASR", "voice-recognition"]
summary: "Get speaker turns, add a transcript, and explicitly enroll voices from a conversation with LocalAI's diarization workflow."
extracss: ["blog.css"]
---

If you have a recording with several people talking, a transcript leaves you with another job: working out who said each part. Speaker labels help, but `SPEAKER_00` doesn't tell you whether you're listening to someone you know. And the next recording can assign that person a different label.

The [LocalAI integration in PR #12414](https://github.com/mudler/LocalAI/pull/12414) brings together three workflows through the audio diarization endpoint. You can get speaker turns, add transcribed words, or use a speaker encoder to match voices against people you explicitly register. The profile-enrollment work also lets you start with an ordinary conversation: preview an unknown speaker, give them a name, and remember their voice.

You don't need a separate, clean enrollment recording beforehand. The recording still needs enough usable speech for that person, though. An upload full of people talking over each other doesn't guarantee a usable profile.

This workflow uses the [native profile support in parakeet.cpp](https://github.com/mudler/parakeet.cpp/pull/80). It requires a LocalAI build containing the feature and an updated `parakeet-cpp` backend. Installing a gallery entry alone does not add those capabilities to an older server.

## Start with the speaker turns

Diarization answers “who spoke when” using recording-local labels. Each segment has a start time, an end time, and a speaker label. That's useful for navigating an interview or showing a timeline before you need any text.

The [gallery configurations](https://github.com/mudler/LocalAI/blob/f98a3acc445aefda1169aa172526a505fba66156/gallery/index.yaml) make the three modes explicit:

| Gallery model | What it configures |
|---|---|
| `parakeet-cpp-nemotron-3-diarization` | Nemotron-3-Diarization for speaker turns. |
| `parakeet-cpp-nemotron-3-diarization-asr` | The same diarization model plus Parakeet TDT+CTC 110M for transcription. |
| `parakeet-cpp-nemotron-3-diarization-asr-speakers` | Diarization, transcription, and a WeSpeaker ResNet34 speaker encoder for recognition and profile export. |

All three use the `parakeet-cpp` backend. The last entry includes all three weight files and configures the companion models together. An ASR-only Parakeet gallery model is not the full configuration.

Diarization does not separate the recording into isolated audio tracks. Overlapping voices remain in the original audio. Also, `SPEAKER_00` is a label within one result, not a persistent identity you can carry into another file.

The Nemotron model uses Sortformer, which has a fixed speaker capacity rather than a configurable clustering stage. Speaker-count hints and clustering thresholds do not control this backend. Keep that in mind if you're moving an application from another diarization model.

## Add the words when you need them

The second configuration adds automatic speech recognition, or ASR. With `include_text=true`, the endpoint returns text on the speaker segments. That gives you a speaker-attributed transcript without making your application join two unrelated responses.

Both pieces matter: the model needs its ASR companion, and the request needs to ask for text. Setting the flag on a diarization-only configuration does not load a transcription model. Without the companion, the response contains speaker turns without text.

This is handy for reviewing a conversation. You can follow a person's turns and read the corresponding words. You still need to review transcription mistakes and speaker assignments before treating them as a reliable record.

## Put a name to an unknown voice

The third configuration adds the speaker encoder. It extracts a numerical representation of a voice, called an embedding, for comparison with registered voices. A match can add a name to the speaker's segments. Recognition is probabilistic; a returned name is not proof of identity.

The part I like here is enrollment from the conversation itself. You can begin with an empty registry, discover the speakers, and decide which person to remember after listening. There is no need to ask everyone to record a separate introduction first.

Obtain consent before preparing profiles or remembering a speaker. In **Studio → Diarization**, select the full configuration and upload your recording. Enable **Prepare speakers to remember**, then select **Diarize**. This requests speaker profiles together with transcript text and speaker summaries.

In **Speakers**, use **Preview 1**, **Preview 2**, or another available interval to listen. These previews play retained clean intervals from the original recording. They are not newly separated tracks, and your browser needs to support the uploaded audio format.

For an unknown speaker, choose **Name and remember**, enter a name, and select **Remember**. Listen first (I wouldn't trust my memory of who spoke first either). After the server confirms registration, the UI applies the name to that speaker's turns. A failed save doesn't rename the transcript.

Some speakers have no usable profile. In that case, saving is unavailable; try a recording with longer speech without overlap. Exporting profiles never enrolls anyone automatically. Naming and remembering a person is a separate action.

## The API follows the same steps

Applications use the same diarization endpoint. This compact request asks for the full result, including the sensitive profiles.

Obtain consent before exporting profiles or enrolling anyone. Set `LOCALAI_URL` and `LOCALAI_API_KEY` for your server. Omit the authorization header only if authentication is disabled. With user authentication, profile export and enrollment additionally require **Voice Recognition** permission. Normal diarization requires **Audio Diarization** permission, and model access controls still apply.

Run this from the directory containing `conversation.wav`. The subshell creates a private temporary directory for the response without changing permissions in your current shell:

```bash
(
umask 077
WORK=$(mktemp -d) || exit 1
curl --fail-with-body "$LOCALAI_URL/v1/audio/diarization" \
  -H "Authorization: Bearer $LOCALAI_API_KEY" \
  -F model=parakeet-cpp-nemotron-3-diarization-asr-speakers \
  -F file=@conversation.wav \
  -F include_text=true \
  -F include_speaker_profiles=true \
  -F response_format=verbose_json \
  -o "$WORK/conversation-profiles.json" &&
printf 'Profile response: %s\n' "$WORK/conversation-profiles.json"
)
```

Keep that response private and delete the temporary directory when you no longer need it.

Enrollment sends the complete, unchanged `speaker_profiles` object to `POST /v1/voice/register`, together with `model`, `name`, and an explicit numeric `speaker_slot`. Zero is a valid slot. Match that raw slot to the segment's `label`, not its normalized `SPEAKER_NN` value or array position. Slots can be sparse and arrive in a different order.

The [diarization setup guide](/docs/features/audio-diarization/#api-example-install-export-and-remember) covers setup and the full requests. The [voice registration documentation](/docs/features/voice-recognition/#portable-profile-registration) covers enrollment and validation. Gallery installation is asynchronous: wait for successful completion before using the model.

## Remembering has limits

On later recordings, you can leave profile export off and still match compatible registered voices. You don't need to export biometric vectors every time you want named segments. With preparation off, the UI requests turns without transcript text. Use the API with `include_text=true` for text without profile export.

Portable registrations require the same speaker encoder identity and embedding dimension. The server checks the loaded encoder's identity from its bytes. Renaming a file doesn't change that identity; modifying or quantizing the weights does.

Each enrollment creates a separate registration. Reusing a display name does not merge embeddings or add samples to an existing person. These recognition profiles are also separate from TTS voice-cloning profiles.

The current recognition registry is global within one LocalAI instance and lives in memory. Every user shares that registry; it is not a private address book. Restarting the server loses its registrations, and multiple frontends do not synchronize them. The browser stores only registration metadata for its management list. That list does not make the server registry persistent.

Obtain consent before enrolling someone. Profiles contain sensitive biometric data, and an exported profile does not prove identity or consent. Keep recordings and profile JSON out of application logs, proxy logs, and public bug reports. Restrictive file permissions help, but you still need an appropriate retention policy.

If you try this on a consented recording, feel free to share where speaker assignment or enrollment needs work. Include the model configuration and a description of the failure, without posting anyone's voice profile.

Cheers!
