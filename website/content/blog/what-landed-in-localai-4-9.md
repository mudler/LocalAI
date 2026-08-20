---
title: "What landed in LocalAI 4.9"
date: 2026-08-20
author: "Ettore Di Giacinto"
category: "Release"
tags: ["release", "security", "authentication", "ui", "distributed", "vllm.cpp", "tts", "video"]
summary: "Authentication now denies by default, chat can compress its own history, models and backends each got one page, and vllm-cpp builds for cards other than Blackwell. 146 pull requests in thirteen days."
extracss: ["blog.css"]
---

LocalAI 4.9.0 is out, after thirteen days and 146 merged pull requests. No new engines this time. The work went into things LocalAI already did, and one of them is a security fix you should read before upgrading.

The [full notes](https://github.com/mudler/LocalAI/releases/tag/v4.9.0) list everything. This post covers the parts that change what you do day to day, with the pull request numbers so you can read the diffs.

## Read this before you upgrade: routes now deny by default

Until this release the auth middleware decided what to protect by matching path prefixes. A route whose path was not on that list was public.

That is fine right up until a route has an alias. `/mcp/chat/completions`, `/moderations`, `/models`, `/backends` and `/import-model` all exist without the prefix the classifier was looking for, so global authentication did not apply to them. Any route added later inherited the same problem by default, because being absent from a protected-prefix list is the easy state to end up in.

The middleware is now method-aware and denies by default. A route is public only if its method and its path appear in an explicit registry. What stays public is what has to be: API instructions, Swagger GET routes, the LocalAI well-known document, and the health, login, OAuth, SPA, asset, branding and node registration-token bootstrap flows. Whole-router coverage is asserted in tests, so a new route cannot become public by being forgotten ([#11602](https://github.com/mudler/LocalAI/pull/11602)).

If you run with database authentication or legacy API keys configured, two things change. `/version` now requires credentials. So do the URLs LocalAI hands back for generated audio, images, video and 3D assets, which means a client that fetched those URLs anonymously needs to send the key. Embedded deployments can still open narrow prefixes through `ApplicationConfig.PathWithoutAuth`, and the legacy GET exemption flags are still there as explicit compatibility overrides.

Thanks to [Naor Yaacov](https://www.linkedin.com/in/naor-yaacov/) for reporting this class of bypass.

## Chat can compress its own history

A long conversation eventually stops fitting in the context window, and the usual answer is to drop the oldest messages. This release can compress them instead.

Compression is opt-in per model, through a `compression` block in the model config. When it is on, older complete turns are run through a LocalAI model of your choosing and replaced by the result before inference. It is off by default and nothing changes for models that do not configure it ([#11556](https://github.com/mudler/LocalAI/pull/11556)).

Several things are deliberately left alone. Leading system and developer prompts are preserved, so a safety prompt is not summarized away. The newest messages are preserved. Tool-call and tool-result pairs are kept whole, so a compressed history never contains a call whose result went missing. Compression runs after PII filtering and after Assistant and MCP prompt injection, including on later MCP iterations, so compression sees the prompt that would actually have been sent. Tool schemas and the headroom needed for the completion are accounted for with a conservative offline token bound.

Non-streaming responses carry compression metadata, streaming responses carry it in the usage trailer, and compression events, ratios and durations are exported as metrics. A cloud-proxy passthrough configuration rejects compression, because LocalAI cannot safely rewrite an opaque provider payload; translate mode works. If compression fails partway through a stream that has already started, you get an in-band SSE error followed by `[DONE]` rather than a truncated response.

## Models and backends each have one page now

<figure>
<video src="/media/model-lifecycle.mp4" muted loop playsinline preload="none" data-lazy aria-label="Browsing the models gallery and the backend catalog, each with Explore and Installed views on one page"></video>
<figcaption>Same shape on both pages: a searchable list, a detail pane, and Installed as a view rather than a separate destination.</figcaption>
</figure>

Models had a gallery and a separate management surface under Host. Backends had a nested Host view for installed binaries. Which meant that where a resource lived depended on whether you were looking for it or operating it, and the common actions sat a level deeper than they needed to.

Now `/app/models` owns Explore and Installed, and `/app/backends` owns Catalog and Installed. Both keep search, filter state and selection in the URL, so a filtered view is a link you can send someone. Backends keep their variants, development builds and target-node scope. Explore offers capability-aware Open and Manage installation actions, while destructive controls stay in Installed, where you are already looking at things you own. Operate Overview reads host capacity from the shared summary poller it already had, and the nested Host destination is gone ([#11548](https://github.com/mudler/LocalAI/pull/11548)).

The API did not change. Existing `/app/manage` bookmarks still work through a replace redirect that preserves the model and backend query state they carried.

## The import form was hiding the answer behind a chevron

<figure>
<video src="/media/import-model.mp4" muted loop playsinline preload="none" data-lazy aria-label="Pasting a Hugging Face reference into the rebuilt import form, with the format reference visible alongside"></video>
<figcaption>The source field carries its own Import button, and the list of what you can paste sits next to it.</figcaption>
</figure>

The import page had picked up the new palette but kept its old layout: a 760px column holding a URI field, a format guide, ten modality chips, nine preference fields, a key-value repeater and a YAML editor, with the primary button detached from the form it submitted.

Two of its problems were plain bugs. The Import button had no `className` at all, so it fell through to the user-agent button and rendered in system chrome with the wrong font and no focus ring. Next to it, `class="btn btn-primary fas fa-save fa-upload"` set Font Awesome as the button's own font family, which the label text inherited, while two icon classes fought over one `::before`.

The rebuild moves to a wider layout and puts the format reference beside the work column. That reference answers the only question a first-time admin has, which is what you are allowed to paste, and it used to sit behind a chevron that was closed by default. Below 1024px it collapses into a disclosure and stays reachable. The source field is the hero and carries its own Import button, which removed the `aria-hidden` submit button that only existed because the real action sat outside the `<form>`. Simple and Advanced modes are gone: they were about 80% the same surface, and keeping both meant a mode switch, a localStorage key and a Keep/Discard/Cancel dialog whose only job was protecting state the switch would have hidden. What actually differs is the kind of input, so that is now two tabs, a source or YAML ([#11461](https://github.com/mudler/LocalAI/pull/11461)).

The class-string bug was not a one-off. A follow-up found eight header controls across seven pages with two or three elements' classes collapsed into a single string ([#11462](https://github.com/mudler/LocalAI/pull/11462)).

## A 35 GB model that looked permanently broken

On a two-replica frontend, loading a 35.7 GB GGUF onto a newly added Jetson Thor worker made the model unloadable from the operator's seat. Every retry in the UI reproduced it. Staging was progressing normally the whole time.

Replica A had taken the per-model advisory lock and started a transfer of about twenty minutes. Replica B got a request for the same model, blocked on `pg_advisory_lock`, and was killed at sixty seconds by the `statement_timeout` on the `localai` role:

```
routing model llama-cpp/models/Qwen3.6-27B-MTP-GGUF/Qwen3.6-27B-UD-Q8_K_XL.gguf:
loading model Qwen3.6-27B-MTP-GGUF: advisorylock: acquiring lock 9003261067483446873:
ERROR: canceling statement due to statement timeout (SQLSTATE 57014)
```

Two mistakes combined here. `Route` wrapped the entire cold load in `advisorylock.WithLockCtx`, so the lock was held across backend install, multi-gigabyte staging and checkpoint load. The lock exists to stop two replicas loading the same model at once, which is a decision that takes milliseconds. And `WithLockCtx` overrode `lock_timeout` but not `statement_timeout`, although both abort the same blocking call, which made the sixty second death latent for every advisory lock in the codebase rather than just this one.

Cold loads now run as durable jobs, so the lock is taken and released around the dedup check instead of the transfer ([#11514](https://github.com/mudler/LocalAI/pull/11514)).

## vllm-cpp now builds for cards other than Blackwell

The `vllm-cpp` CUDA images were built for `120a;121a` on amd64 and `121a` alone on arm64, out of the ten architectures vllm.cpp's own release archive builds.

An unlisted card does not run slower, it dies at the first request with `no kernel image is available for execution on the device`, long after `local-ai backends install vllm-cpp` reported success. It was found on a Jetson Thor node that had the backend installed and could serve nothing.

<div class="tw">
<table>
<thead><tr><th>Platform</th><th>Before</th><th>After</th></tr></thead>
<tbody>
<tr><td>amd64</td><td><code>120a;121a</code></td><td><code>80;86;89;90a;100a;103a;120a;121a</code></td></tr>
<tr><td>arm64</td><td><code>121a</code></td><td><code>87;90a;100a;110;121a</code></td></tr>
</tbody>
</table>
</div>

The split follows where the silicon exists. Jetson is arm64 only, `87` for Orin and `110` for Thor. Desktop `120a` is amd64 only. `90a` and `100a` are on both because of the SBSA parts. That adds A100, A10 and 3090, L4, 4090 and RTX 6000 Ada, H100 and H200, B200, B300, Jetson Orin and Jetson Thor. The CUDA 13 guard now covers the arm64 branch too, and Triton-AOT stays on ([#11512](https://github.com/mudler/LocalAI/pull/11512)).

## Video with sound, and TTS on the llama.cpp matrix

`vllm-cpp` gained a video modality. vllm.cpp's C ABI grew a video slice at v12, and when a model config declares the MiniMax-H3 checkpoint set, `Load` opens a video engine instead of the text one and `GenerateVideo` serves LocalAI's existing `/video` endpoint. Video and audio are generated jointly, so the MP4 comes back with a real AAC track instead of silence, and if you ask for speech in the prompt the model lip-syncs it ([#11424](https://github.com/mudler/LocalAI/pull/11424)).

There are two details worth knowing if you go looking. H3 is not a model directory: the DiT, the text encoder and two VAEs are separate artifacts, so `parameters.model` is the DiT and the rest of the set is named in `options:`. And the DiT partition has to be declared rather than detected, because the community quantizations strip the release metadata and the FL2VA and Ref2VA DiTs have identical structure on disk. Handing reference conditioning to an FL2VA DiT renders for hours and returns a coloured lattice, so that combination is refused before the engine is called. Gallery entries `minimax-h3-fl2va-q4` and `minimax-h3-ref2va-q4` install the two sets.

Separately, Qwen3-TTS now runs through the `llama-cpp` backend, which puts text-to-speech on the same accelerator matrix already shipped for text generation: CUDA, ROCm, SYCL, Vulkan, Metal and L4T, using upstream's own GGUF conversion. It is implemented as a slot-based `SERVER_TASK_TYPE_TTS` task rather than a gRPC handler driving the generation loop, because `server_context` owns the `llama_context` and runs the slot scheduler on its own thread, and a handler running its own loop would race it. The server-side plumbing is still a draft upstream, so it rides along as a patch that gets deleted when [ggml-org/llama.cpp#26603](https://github.com/ggml-org/llama.cpp/pull/26603) merges. Gallery entries are `qwen3-tts-llamacpp` and `qwen3-tts-llamacpp-q4`. The existing `qwen3-tts-cpp` backend is untouched, so the two run side by side ([#11392](https://github.com/mudler/LocalAI/pull/11392)).

## The rest, briefly

<figure>
<img src="/media/v4-9-0-ui-activity.png" alt="The Activity page with two backend installs in progress and one completed">
<figcaption>Backend operations are represented while they are still running, not only once they finish.</figcaption>
</figure>

HTTP admission control is now process-wide rather than per backend, which also stops the traces list growing without bound, and running backend operations are represented while they are in flight with links straight to their logs ([#11560](https://github.com/mudler/LocalAI/pull/11560)).

The PII middleware can now restore what it masked. With `pii.reverse_in_response` on, a masked value becomes a unique deterministic pseudonym inside the request, so two addresses read as `EMAIL_001` and `EMAIL_002` where they used to be two identical `[REDACTED:EMAIL]` markers, and each is restored if the backend returns it. Restoration handles SSE tokens split across response writes. The substitution map is request-local and never written to disk, and irreversible redaction is still the default ([#11272](https://github.com/mudler/LocalAI/pull/11272)).

Hugging Face snapshot materialization downloads files in parallel now, up to a configured limit, so a many-shard repository no longer goes one file at a time. Only whole files run in parallel, never pieces of one, so `.partial` resume and per-file SHA verification work exactly as before, and the two callers outside the artifact path keep their sequential ordering through a wrapper with a limit of one ([#11162](https://github.com/mudler/LocalAI/pull/11162)).

KNN became a first-class request router rather than a cache for the classifier. `classifier: knn` votes by similarity over labelled example prompts, so it needs no classifier model, and the label knowledge lives in a corpus you seed through the admin API. Entries below `knn.similarity_threshold` cannot vote, and if none clears it the router falls back and does not guess, with `nearest_similarity` recorded on the decision either way. The corpus persists as one JSONL file per router under `<data path>/router-corpus`, and entries recorded under a different embedding model re-embed on load ([#10652](https://github.com/mudler/LocalAI/pull/10652)).

Realtime WebRTC can now be pinned to a single UDP port with `LOCALAI_WEBRTC_UDP_PORT`, so a container or firewall needs one rule instead of a range ([#11436](https://github.com/mudler/LocalAI/pull/11436)). A follow-up fixed an interaction between the two settings. `LOCALAI_WEBRTC_ICE_INTERFACES` was silently ignored whenever the port was set, because a wildcard mux makes pion enumerate interfaces itself with no filter. On a host with docker bridges the browser was being handed `172.18.0.1` and friends, connecting on a good pair, then dropping when ICE consent checks failed on the others ([#11466](https://github.com/mudler/LocalAI/pull/11466)).

Metal was missing from two macOS backends. `stablediffusion-ggml` gated its Metal flags on an `OS=Darwin` variable the Darwin runner does not define, so `BUILD_TYPE=metal` produced a build without the embedded library ([#11531](https://github.com/mudler/LocalAI/pull/11531)). `parakeet-cpp` never forwarded `BUILD_TYPE=metal` to `PARAKEET_GGML_METAL` at all; on an M1 Air the same five-minute sample went from 82.57 seconds to 50.18 seconds, with byte-identical output ([#11492](https://github.com/mudler/LocalAI/pull/11492)).

When a backend dies you now get told why. An unexpected runtime exit used to log its stderr at debug level only, so at the default log level operators saw an exit code and nothing else; the last non-empty stderr line now rides the warning ([#11532](https://github.com/mudler/LocalAI/pull/11532)), and a failed gRPC readiness preserves the process exit code with the last 4 KiB of diagnostic ([#11447](https://github.com/mudler/LocalAI/pull/11447)). Model-level MCP servers no longer vanish from Chat when discovery fails, which was hiding container DNS and VPN reachability problems; they stay listed as error rows with the reason attached ([#11495](https://github.com/mudler/LocalAI/pull/11495)). A post-download checksum mismatch is treated as a transient transfer failure and retried, so a stale CDN response no longer fails the install, and unverified bytes are still refused ([#11536](https://github.com/mudler/LocalAI/pull/11536)).

The web UI speaks two more languages: Portuguese (Brazil), a complete translation across all 14 namespaces ([#11427](https://github.com/mudler/LocalAI/pull/11427)), and Indonesian for the admin, media and navigation strings ([#11493](https://github.com/mudler/LocalAI/pull/11493)).

The gallery went from 1,622 entries to 1,707. Qwen3.8 arrived in 9B, 27B, Ridge and small variants, alongside Gemma 4 agentic and Scotoma 2, DeepSeek V4 Pro 0813, Ling 3.0 Flash, Nemotron 3.5 Lightning 30B, Tess 4 27B, Ornith 1.0 and 1.5, LFM2.5 in 230M and VL 1.6B, HunyuanOCR, OvisOCR2, Higgs Audio v3 and a first genomics family. Ten people contributed to this release, three of them for the first time.

To upgrade, pull `localai/localai:latest` or re-run the install script, and check the authentication section above first if you run with API keys. The [full changelog](https://github.com/mudler/LocalAI/compare/v4.8.2...v4.9.0) has everything this post left out.
