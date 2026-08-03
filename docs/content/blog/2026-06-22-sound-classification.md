+++
title = "Sound classification with ced.cpp"
date = 2026-06-22
description = "A new /v1/audio/classification endpoint for audio tagging, returning scored AudioSet labels."
url = "/blog/sound-classification/"
+++

`POST /v1/audio/classification` is a new endpoint for audio tagging and sound-event classification. It returns scored [AudioSet](https://research.google.com/audioset/) labels: baby cry, glass breaking, alarms, and several hundred others.

It is backed by [ced.cpp](https://github.com/localai-org/ced.cpp), a 527-class AudioSet tagger ported to ggml by the LocalAI team.

See [Audio classification]({{% relref "features/audio-classification" %}}). Shipped in [PR #10425](https://github.com/mudler/LocalAI/pull/10425).
