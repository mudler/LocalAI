---
title: "Know who said what in your recordings"
date: 2026-10-01
author: "Ettore Di Giacinto"
category: "Engineering"
tags: ["diarization", "transcription", "voice-recognition"]
summary: "Follow speakers in a conversation and let LocalAI remember their names from the recording you already have."
extracss: ["blog.css"]
---

If you're going back through an interview, finding the right words is only part of the job. You also need to know who said them. In a meeting recording, a short reply can be hard to place when you've forgotten whose voice it was.

LocalAI's diarization feature marks when each person speaks. You can add a transcript to those turns, then give people names and ask LocalAI to remember their voices for later recordings. You can do that from a group conversation you already have, without collecting separate voice samples from everyone first.

Availability: you'll need a LocalAI build and updated audio backend that support the workflow in [PR #12414](https://github.com/mudler/LocalAI/pull/12414).

## Choose what you need from the recording

There are three ways to use this, depending on how much you want from the audio.

**Speaker turns only** gives you the times when each person talks, without transcribing their words. For a podcast editor, that can help locate a guest's turns before listening back and deciding where to make a cut. The people are distinguished within that recording, but LocalAI doesn't know their names yet.

**A transcript with speakers** adds the words to those turns. When reviewing an interview, you can read the questions and answers with their speakers attached, then return to the audio to check a quote. In a meeting, it helps you follow who raised a question and who responded.

**Remembered names** lets you name someone after listening to them and save their voice for future matching. This is useful for recordings with returning participants, such as a podcast with regular hosts or a recurring team discussion. LocalAI can then attach a saved name when it recognizes that person in another recording.

Recognition can mistake one person for another, especially when people talk over each other. Check the audio before relying on an attribution or quoting someone.

## Remember someone from the conversation

You don't need to arrange another recording session or ask each participant to read a prepared sentence. Upload the conversation, listen to a preview of a person's speech, and decide who to remember. Ask their permission before preparing or saving their voice.

Here's how to try the naming workflow in the web interface:

1. Open the **Models** gallery and install the diarization option that includes transcription and speaker recognition. The [setup guide](/docs/features/audio-diarization/) lists the exact model names for all three modes and covers developer API use. Wait for installation to finish.
2. Go to **Studio → Diarization**, select that model, and upload your recording.
3. Enable **Prepare speakers to remember**, then select **Diarize**. This prepares the speakers for naming and includes transcript text.
4. In **Speakers**, use an available **Preview** to listen to the person you want to name. Listen first (I wouldn't trust my memory of who spoke first either).
5. Choose **Name and remember**, enter their name, and select **Remember**. Once saved, the name appears on that person's turns.

Preparing speakers doesn't save everyone automatically. You choose which people to remember. If a person doesn't have enough clear speech, saving may be unavailable; try a recording where they speak for longer without interruptions.

On a later recording, use the same recognition-capable model to match saved voices. You can leave preparation off when you only want named turns; the current Studio workflow includes transcript text when preparation is on.

For now, saved voices are lost when the LocalAI server restarts. They're also shared across users of the same instance, so treat remembering someone as a shared choice, not a private contact entry.

Try it with a recording whose participants have agreed, and listen through the previews before adding names. If you run into a confusing speaker assignment, feel free to share what happened without posting anyone's private audio or saved voice data.

Cheers!
