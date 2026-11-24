---
title: "LocalAI 4.3: signed backends, and the prompt cache that was off"
date: 2026-05-24
author: "Ettore Di Giacinto"
category: "Release"
tags: ["release", "security", "cosign", "prompt-cache", "distributed", "usage"]
summary: "Keyless cosign verification for backend OCI images, the llama.cpp prompt cache enabled by default, per-API-key usage attribution, and the replica-pinning bug that kept a second node idle."
extracss: ["blog.css"]
---

Here is a gap that had been sitting in LocalAI for a while. The gallery YAML tells LocalAI which OCI image to pull for a backend, and then LocalAI pulls it. Nothing checked that the bytes coming back were the bytes we built. A compromised registry, or somebody in the middle, and you would never know.

4.3.0 closes that, and fixes a default that had been quietly costing everybody a lot of prefill time.

## Signed backends

Every backend image merged by CI is now signed with [sigstore](https://www.sigstore.dev/)/cosign, keyless via Fulcio and Rekor, including each per-arch entry under the manifest list ([#9823](https://github.com/mudler/LocalAI/pull/9823)). It uses OCI 1.1 referrers rather than the legacy `:tag.sig` convention.

On your side, verification runs against a policy that the gallery declares:

```yaml
verification:
  issuer_regex: "^https://token\\.actions\\.githubusercontent\\.com$"
  identity_regex: "^https://github\\.com/mudler/LocalAI/\\.github/workflows/backend_merge\\.yml@.*$"
  not_before: "2026-05-22T00:00:00Z"
```

A few details that took some thinking.

`not_before` is the revocation lever. Keyless Fulcio certificates are ephemeral, so there is nothing to revoke on the signing side. Revocation has to be policy side: move the date forward in the gallery YAML and every signature older than it stops validating.

The TUF trusted root is cached process-wide, so installing ten backends from one gallery does one fetch instead of ten.

Digest pinning closes the window between verifying and pulling, which is otherwise a TOCTOU you could drive a truck through.

Strict mode is `--require-backend-integrity`, or `LOCALAI_REQUIRE_BACKEND_INTEGRITY=true`. It turns a missing policy or an empty SHA256 from a warning into a hard failure.

Now the honest part: strict mode is opt-in and off by default, and until a gallery ships a `verification:` block, installs go through with a warning. The default `backend/index.yaml` does not have the blocks populated yet, that is the next step. So today this is machinery that works and is not yet enforcing much. Turn on strict mode in production once your gallery is populated, not before, or you will just break your own installs.

## The prompt cache was off

`llama-cpp` has a server-side prompt cache. LocalAI was not enabling it. So every agent turn, every coding-assistant call, every OpenAI-compatible CLI with a long system prompt, re-prefilled that whole prompt from scratch.

On the reported workload, a repeated system prompt took 5 to 8 minutes per call before this change and seconds after it. Your numbers will depend on how long your prompt is and what hardware you are on.

Two defaults flipped ([#9925](https://github.com/mudler/LocalAI/pull/9925), [#9951](https://github.com/mudler/LocalAI/pull/9951)):

1. `kv_unified` is now `true` in `grpc-server.cpp`. The old `false` was silently force-disabling `cache_idle_slots` at server init, so the host prompt cache got allocated and then never written across requests. That is the one that actually explains the behaviour.
2. `prompt_cache_all` defaults to `true` at the YAML layer, matching upstream llama.cpp's own default in `common.h`. The per-request `cache_prompt` knob is on out of the box.

You can opt out with `options: ["kv_unified:false"]` or `prompt_cache_all: false`, and there are new keys (`cache_idle_slots`, `checkpoint_every_nt`) if you want to tune it. The model configuration docs got a worked example for the repeated-system-prompt case and an explanation of how `kv_unified`, `cache_ram` and `cache_idle_slots` interact, because they interact in ways that are not obvious.

## Who is burning the GPU

The usage page could tell you how many tokens were spent. It could not tell you who spent them ([#9920](https://github.com/mudler/LocalAI/pull/9920)).

`usage_records` gained a `Source` column (`apikey`, `web`, `legacy`) plus the API key id and name, with an idempotent backfill of older rows on `InitDB`. The auth middleware passes the resolved key and the request source through, and usage middleware snapshots the key id and name at write time, so a key you revoke later still reads correctly in history (it renders as `(revoked)` rather than vanishing).

Two new endpoints:

```
GET /api/auth/usage/sources        # your own
GET /api/auth/admin/usage/sources  # everyone, with user_id / api_key_id filters
```

The admin view truncates at 200 keys. The React usage page gained a Sources tab with a source-mix ribbon, a top-7-plus-Other time chart, and a sortable table. Web interface session traffic is split per user instead of being lumped into one global row.

## Distributed v3, and one good bug

This one is worth writing down because the symptom and the cause were far apart.

An operator reported this:

```
dgx-spark1     loaded   in_flight=6
nvidia-thor1   loaded   in_flight=0
```

Two replicas of the same model, one taking everything, one idle forever. The round-robin was there and looked correct.

The cause: `ModelLoader.Load` cached a `*Model` whose embedded `InFlightTrackingClient` was bound to a single `(nodeID, replicaIndex)`. The first request picked a node and got wrapped. Every request after that reused the wrapper, so it kept going to whichever node won the first pick, even after the reconciler scaled the model out. The routing code was fine. It just was not being consulted again!

`SmartRouter.Route` now runs per request ([#9968](https://github.com/mudler/LocalAI/pull/9968)), the `in_flight ASC, last_used ASC, available_vram DESC` ordering actually fires, and replica selection lives in one place (`PickBestReplica`) with a spec asserting the SQL `ORDER BY` and the Go picker agree on a seeded dataset. `probeHealth` is memoized per `(nodeID, addr)` with a 30 second TTL and `singleflight` coalescing, because llama.cpp serializes `HealthCheck` against in-flight `Predict` and a burst of new requests would otherwise stall on it.

Two other distributed changes.

`POST /api/nodes/:id/backends/install` used to block for up to 3 minutes while the worker pulled the image, which froze the Backends picker in the interface. It returns HTTP 202 and a `jobID` immediately now ([#9928](https://github.com/mudler/LocalAI/pull/9928)). Install and upgrade timeouts are configurable via `LOCALAI_NATS_BACKEND_INSTALL_TIMEOUT` and `LOCALAI_NATS_BACKEND_UPGRADE_TIMEOUT`, defaulting to 15 minutes instead of the hardcoded 3. A NATS round-trip timeout while the worker is still pulling reports as `running_on_worker` rather than a hard failure.

Workers also publish debounced install progress (~250ms) that the master forwards into the operations status ([#9958](https://github.com/mudler/LocalAI/pull/9958)), so distributed installs show per-byte progress the same way local ones do. Old workers stay silent and new masters tolerate the silence, so mixed-version clusters keep working.

## Smaller things

`LOCALAI_TRACING_MAX_BODY_BYTES` caps trace payload size, which stops the admin Traces page from trying to render a 40 MB embedding response.

There is a `flake.nix` with a dev shell for NixOS users who do not want to go through Docker.

The `vllm`, `sglang` and `vllm-omni` L4T13 backends are back for Jetson and DGX boxes, switched to PyPI aarch64+cu130 wheels to fix the torch 2.10 ABI mismatch.

A distributed test harness landed in `tests/distributed/`, aimed at catching the class of regression the replica-pinning bug belonged to.

## Thanks

If you run LocalAI in production, the two things to look at here are strict mode (once your gallery has a `verification:` block) and whether the prompt cache change speeds up your workload. I would like to hear numbers from real setups, mine are one data point.

[Full release notes](https://github.com/mudler/LocalAI/releases/tag/v4.3.0).
