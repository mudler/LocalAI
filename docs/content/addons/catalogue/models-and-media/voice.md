+++
disableToc = true
title = "Voice"
description = "Recognise who is speaking, keep the voices that text to speech can use, and speak text with them."
weight = 30
keywords = ["voice", "speaker", "recognition", "tts", "add-on"]
+++

The Voice add-on (ID `voice`) is the page for voice recognition, the voice library and text to speech. It is separate from the Face add-on. In the current development builds, it is in the development channel of the add-on library, so it can change.

## At a glance

| | |
|---|---|
| What it adds | One page, `/app/addons/voice/voice`, with four tabs: **Recognition**, **Library**, **Text to speech** and **From a recording**. The Library tab is for administrators. |
| Who installs it | An administrator, from **Add-ons**, **Find add-ons**, with **Install and turn on**. With authentication off, you are the administrator. |
| Who turns it on | A person needs the `addons` feature (off by default for new users on a server with accounts). The Recognition tab also needs the `voice_recognition` feature, which is on by default. The routes stay in the core and are checked on every call. |
| Guide | [Voice Recognition]({{% relref "/models/capabilities/audio/voice-recognition#the-webui-page" %}}) |
