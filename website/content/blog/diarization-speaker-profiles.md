---
title: "Remember speakers from your recordings in LocalAI"
date: 2026-10-01
author: "Ettore Di Giacinto"
category: "Engineering"
tags: ["diarization", "transcription", "voice-recognition"]
summary: "Name speakers from an existing conversation and recognize them in later recordings."
extracss: ["blog.css"]
---

LocalAI is adding a way to remember speakers directly from a conversation, alongside speaker turns and transcription. You can upload a recording, read who said what, and name voices for recognition in later recordings without collecting separate samples from each person.

For an interview, a transcript with speakers lets you follow the questions and answers and return to the audio to check a quote. In a recurring meeting, remembered voices can put names on returning participants' contributions. A podcast editor can use speaker turns to locate a host or guest's speech before listening back and choosing a cut.

To try this workflow, use a build containing [PR #12414](https://github.com/mudler/LocalAI/pull/12414) and its updated audio backend.

## Three ways to use a recording

**Speaker turns only** marks when each person speaks, without transcribing the words. It distinguishes people within the recording without knowing their names.

**A transcript with speakers** adds the words to those turns, so you can read the conversation with each contribution attributed to a speaker.

**Remembered names** matches voices against people you have explicitly named and saved. LocalAI can attach a saved name when it recognizes someone in another recording. You choose whom to remember; preparing a recording does not save everyone automatically.

Recognition can mistake one person for another, especially when people talk over each other. Check the audio before relying on an attribution or quoting someone.

## Get started

Ask participants for permission before preparing their voices or naming and remembering them. For the named-transcript workflow in the web interface:

1. Open **Models → Explore** and install the option with diarization, transcription, and speaker recognition. The [setup guide](/docs/features/audio-diarization/) lists the model choices and installation requirements. Wait for installation to finish.
2. Go to **Studio → Diarization**, select that model, and upload your recording.
3. Enable **Prepare speakers to remember**, then select **Diarize**. This includes transcript text and prepares speakers for naming.
4. In **Speakers**, listen to an available **Preview** for the person you want to name. Choose **Name and remember**, enter their name, and select **Remember**. Once saved, the name appears on that person's turns.

If someone has too little clear speech, remembering them may be unavailable. Try a recording where they speak for longer without interruptions.

On a later recording, use the same recognition-capable model to match saved voices. Keep preparation enabled if you want transcript text in Studio; with it off, the current UI returns speaker turns without text.

Saved voices are currently lost when the LocalAI server restarts and are shared across users of the same instance. Agree on whose voices to remember before using this on a shared server.
