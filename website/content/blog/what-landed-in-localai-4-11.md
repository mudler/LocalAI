---
title: "What landed in LocalAI 4.11"
date: 2026-10-02
author: "Ettore Di Giacinto"
category: "Release"
tags: ["release", "audio", "diarization", "failover", "decisions", "oci", "operations"]
summary: "Remember speakers, configure model failover, and inspect the models running on one LocalAI host."
extracss: ["blog.css"]
---

LocalAI 4.11 is out. You can now remember speakers from a recording, keep one model name available across several targets, and inspect the models running on a single LocalAI host from the web interface.

The release contains 243 merged pull requests. This post covers the changes you can see and use. The [release notes](https://github.com/mudler/LocalAI/releases/tag/v4.11.0) have the complete list.

## Remember speakers from a recording

LocalAI can diarize a recording, add transcript text, and prepare clear samples from each speaker. In **Studio → Diarization**, you can listen to those samples and choose **Name and remember** for the person you want to save.

Preparing a recording does not save every voice. Registration happens only after you select a speaker, enter a name, and confirm it. When the same recognition-capable model finds that voice in a later recording, it can show the saved name instead of an anonymous speaker label.

<figure>
<video src="/media/speaker-enrollment.mp4" muted loop playsinline preload="none" data-lazy aria-label="Naming and remembering speakers from the Studio diarization page"></video>
<figcaption>Preview a clear interval before you choose whose voice to remember.</figcaption>
</figure>

This is useful for recurring meetings, interviews, and podcasts where the same people appear more than once. The audio scene work also combines transcription, speaker turns, and sound-event detection for regular and realtime requests.

Ask people for permission before saving their voices. Recognition can be wrong, especially when people talk over each other, so check the recording before relying on a name. Saved voices are currently lost when LocalAI restarts and are shared by users of the same instance.

The [speaker-profile guide](/blog/diarization-speaker-profiles/) covers the model choices and the complete Studio workflow.

## Keep one model name when a target fails

Applications often need a local model first and a remote fallback when that model is unavailable. LocalAI can now keep those targets behind one public model name:

```yaml
name: assistant-llm
failover:
  targets:
    - model: preferred-local
    - model: remote-localai
      warm: true
```

Your application continues to request `assistant-llm`. If the first target fails before LocalAI starts the response, LocalAI can try the next eligible target. It checks failed targets for recovery and later returns traffic to the preferred one.

**Operate → Runtime → Failover** shows each configured chain, its current state, the active target, and the health of every target. Administrators can also pin a chain to one target while investigating a problem.

<figure>
<video src="/media/failover-runtime.mp4" muted loop playsinline preload="none" data-lazy aria-label="Configured model chains on the LocalAI failover page"></video>
<figcaption>The first chain is serving from its remote fallback while the preferred local target is unavailable.</figcaption>
</figure>

Transport errors, server errors, OOM, and rate limits can move a request to another target. Validation errors, ordinary client errors, and cancellation do not mark a target unhealthy. LocalAI retries only before the response is committed, so it does not restart a stream after content has reached the client.

Responses report the selected target through `X-LocalAI-Served-Model` and `X-LocalAI-Failover`. Realtime sessions receive a failover event when their target changes. The new `localai-proxy` backend lets a chain include models served by another LocalAI endpoint.

## See what is running on this machine

A standalone LocalAI installation now has its own operations page. Open **Operate → This machine** to see VRAM, RAM, CPU, models-disk usage, and the processes behind loaded models.

Each row shows the backend, resident memory, CPU share, uptime, and PID. You can search and sort the list, open backend logs, and stop a model after confirmation.

<figure>
<video src="/media/this-machine.mp4" muted loop playsinline preload="none" data-lazy aria-label="Resource and model process information on the LocalAI This machine page"></video>
<figcaption>The page shows host capacity and the processes used by loaded models.</figcaption>
</figure>

The first CPU sample displays **Measuring…** until LocalAI has enough information to calculate current usage. CPU-only hosts display **No GPU detected**. When distributed mode is enabled, the same route continues to show the cluster Nodes page.

## More in 4.11

Decision models are now a declared capability served through `POST /v1/systemone`. They can answer structured choice, assessment, and level questions without making the caller parse a prose response. The [decision-model guide](/blog/decision-models/) includes a complete request.

Model galleries can now be distributed as OCI artifacts. LocalAI verifies the resolved digest, checks the artifact before extraction, and keeps cached content tied to the verification policy used to accept it.

Kimodo adds text-to-skeletal-animation through `POST /3d/animate`, with CPU and Vulkan builds and a Studio preview. It returns a binary glTF animation at 30 FPS.

The model gallery now contains 1,926 entries. This release adds NeMo speech and diarization models, four Italian Piper voices, decision models, and another batch of Qwen3.8 and community models.

Pull `localai/localai:latest` or run the installation script again to upgrade. If you try the speaker or failover workflows, let us know what you run into. The more the merrier!

Enjoy!
