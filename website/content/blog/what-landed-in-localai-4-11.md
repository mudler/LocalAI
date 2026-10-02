---
title: "What landed in LocalAI 4.11"
date: 2026-10-02
author: "Ettore Di Giacinto"
category: "Release"
tags: ["release", "audio", "diarization", "failover", "decisions", "oci", "operations"]
summary: "Audio scenes can remember speakers, model names can fail over across targets, and single-host installations get an operations page. 243 pull requests in fifteen days."
extracss: ["blog.css"]
---

LocalAI 4.11.0 is out, after fifteen days and 243 merged pull requests. Three changes alter what you do day to day: recordings can carry speaker names instead of anonymous labels, one model name can survive a failed target, and a single LocalAI host now has the same kind of operational visibility that used to require distributed mode.

The [full notes](https://github.com/mudler/LocalAI/releases/tag/v4.11.0) list everything. This post covers those three changes, with the pull request numbers so you can read the diffs.

## A recording can teach LocalAI a speaker's name

Diarization used to stop at `SPEAKER_00` and `SPEAKER_01`. That tells you when the voice changed, but not who came back in the next recording.

The audio scene work in [#12335](https://github.com/mudler/LocalAI/pull/12335) combines speech recognition, diarization and sound-event classification in one parakeet-cpp configuration. A regular transcription can attach speaker labels to segments and words. A realtime session can emit transcript segments and detected sounds. `POST /v1/audio/diarization` can also return text, speaker summaries and, when explicitly requested, versioned speaker profiles.

[#12414](https://github.com/mudler/LocalAI/pull/12414) puts an enrollment workflow around those profiles. In **Studio → Diarization**, upload an ordinary multi-speaker recording and ask LocalAI to prepare speakers. The page finds intervals where one person is speaking clearly enough to preview. Choose **Name and remember**, listen to the interval, enter the name, and confirm. Registration happens only after that action. A later recording can then replace an anonymous label with the saved name.

<figure>
<video src="/media/speaker-enrollment.mp4" muted loop playsinline preload="none" data-lazy aria-label="The Studio diarization page for naming and remembering speakers"></video>
<figcaption>Studio prepares clean intervals from a recording before you choose which speaker to remember.</figcaption>
</figure>

There are limits worth stating plainly. A speaker profile is biometric data, not proof of identity or consent. Recognition can be wrong, especially around overlapping speech. The registry is currently process-local and ephemeral, so names disappear when LocalAI restarts and do not synchronize between frontends. Encoder identity must match between enrollment and recognition. Ask people before registering their voices and check the audio before relying on an attribution.

The gallery includes scene models for realtime transcription and sound detection, named-speaker variants, standalone Nemotron 3 diarization, and combined diarization plus ASR. The [speaker-profile guide](/blog/diarization-speaker-profiles/) walks through the UI flow in more detail.

## One model name can fail over to another target

Before this release, an application had to know which endpoint or model to try next. That moved health checks, retry policy and recovery logic into every client. [#12285](https://github.com/mudler/LocalAI/pull/12285) moves it into LocalAI.

A model configuration can now declare an ordered chain:

```yaml
name: assistant-llm
failover:
  targets:
    - model: preferred-local
    - model: remote-localai
      warm: true
```

Clients keep requesting `assistant-llm`. LocalAI tries the preferred target, moves to the next eligible target when a transport error, server error, OOM or rate limit happens before response commitment, and probes for recovery. Validation errors, ordinary client errors and cancellation do not trip health. Once bytes have been committed to a streaming response, LocalAI does not pretend it can replay the request invisibly.

The selected target is visible rather than hidden. Responses carry `X-LocalAI-Served-Model` and `X-LocalAI-Failover`. Realtime sessions receive `localai.model.failover` events. `GET /api/failover` exposes chains and health, with an SSE endpoint for changes. Administrators can pin and unpin a target through the API, MCP tools, or **Operate → Runtime → Failover**.

<figure>
<video src="/media/failover-runtime.mp4" muted loop playsinline preload="none" data-lazy aria-label="The LocalAI runtime failover page"></video>
<figcaption>Each chain exposes its active target, health state and administrative pin.</figcaption>
</figure>

The new `localai-proxy` backend connects a target to another LocalAI endpoint. It proxies the REST capabilities used by text, embeddings, reranking, audio, image, video, 3D and recognition workloads, and bridges live transcription through the upstream realtime API. This lets a chain mix models on the current host with models served by another LocalAI installation.

## Single-host operation no longer means operating blind

The Nodes page had become a useful cluster workbench, but on a standalone installation it had little to show. [#12189](https://github.com/mudler/LocalAI/pull/12189) gives that route a single-host mode called **This machine**.

The page shows VRAM, RAM, CPU and models-disk gauges, followed by the processes behind loaded models. Each row includes backend, resident memory, CPU share, uptime and PID. You can search and sort the list, open backend logs, and stop a model after confirmation. The Operate overview also shows the five heaviest running models, and the navigation carries a running-model count.

<figure>
<video src="/media/this-machine.mp4" muted loop playsinline preload="none" data-lazy aria-label="The LocalAI This machine operations page"></video>
<figcaption>Host capacity and loaded-model processes are available without enabling distributed mode.</figcaption>
</figure>

This reuses the APIs LocalAI already owns instead of introducing an external monitor. `GET /system` now attaches process telemetry to each loaded model. `GET /api/resources` adds logical cores, current CPU usage, one-minute load and models-disk information. CPU usage needs two samples, so the first reading says **Measuring…** rather than showing a false zero. A CPU-only host says **No GPU detected** instead of drawing an empty gauge.

Distributed installations keep the cluster Nodes workbench. The route detects whether distributed mode is available and chooses the correct view, so there is one operational entry point rather than separate navigation for standalone and cluster deployments.

## The rest, briefly

Decision models are now a declared capability served through `POST /v1/systemone`. The request can ask named choice, assessment and level questions and receive structured answers without parsing prose. LocalAI validates body size, question count, state, IDs and criteria before invoking the backend. Gallery entries include Laya, GLiNER2.5-Decide, Tev1, kev, Nimble and CLM ([#12247](https://github.com/mudler/LocalAI/pull/12247), [#12373](https://github.com/mudler/LocalAI/pull/12373), [#12391](https://github.com/mudler/LocalAI/pull/12391), [#12397](https://github.com/mudler/LocalAI/pull/12397)). The [decision-model guide](/blog/decision-models/) has a complete request example.

Model galleries can be distributed as OCI artifacts now. LocalAI verifies a resolved digest, enforces artifact and extraction bounds, and only promotes a complete, parseable gallery to the cache. A verification policy can require an exact source-repository identity, and cache identity includes that policy ([#12167](https://github.com/mudler/LocalAI/pull/12167)).

Kimodo adds text-to-skeletal-animation through `POST /3d/animate`, with CPU and Vulkan builds and a Studio preview. The output is a binary glTF animation at 30 FPS, not a humanoid mesh ([#12095](https://github.com/mudler/LocalAI/pull/12095), [#12162](https://github.com/mudler/LocalAI/pull/12162), [#12184](https://github.com/mudler/LocalAI/pull/12184)).

The gallery reached 1,926 entries. This cycle added large Qwen3.8 and community batches, NeMo speech and diarization models, four Italian Piper voices, structured-extraction models and the new decision-model family.

To upgrade, pull `localai/localai:latest` or re-run the install script. The [full changelog](https://github.com/mudler/LocalAI/compare/v4.10.0...v4.11.0) has everything this post left out.
