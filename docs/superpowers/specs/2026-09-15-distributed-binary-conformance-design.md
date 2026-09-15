# Distributed binary conformance suite

Status: approved for implementation
Date: 2026-09-15

## Problem

The distributed tests cover routing, model staging, and selected file transfers.
However, most file-transfer tests use in-process servers and direct test dialers.
They do not prove that a normal LocalAI deployment preserves the complete backend
feature set through worker tunnels and frontend relays.

The existing process tests start real frontend and worker binaries. Those tests
prove chat inference and model artifact transfer. They do not prove deterministic
input and output transfer for each file-bearing backend operation. The mock image
and video methods also return success without writing an output file.

This gap can hide two regression classes:

- A new backend RPC can work in local mode but fail through a distributed wrapper.
- A file-bearing RPC can report success while an input or output remains on the
  wrong host.

The current pull request also has failures in both distributed CI jobs. The new
suite cannot provide a useful signal until those regressions are fixed.

## Goal

Run a binary-level conformance suite that proves the current distributed backend
feature set behaves like local mode. Use deterministic fixture data to prove each
transfer and response.

## Deployment fidelity

The suite starts compiled `local-ai` processes through their public commands. It
does not replace a frontend, worker, tunnel, or backend supervisor with an
in-process substitute.

The harness uses the deployment shape that users run:

- two frontend processes share PostgreSQL state;
- two worker processes connect through the public cluster WebSocket endpoint;
- each worker starts the mock backend through the normal backend supervisor;
- frontend and worker processes use separate model and data directories;
- workers do not expose a backend address to the frontend;
- requests enter through the public frontend HTTP API where an API exists;
- the non-owner frontend exercises the inter-replica relay path;
- the owner frontend exercises the direct tunnel path.

Tests use the same command flags as the documented frontend and worker setup. A
test helper can reduce repeated process code, but it cannot bypass a production
transport boundary.

## Coverage boundary

The suite covers the backend feature set that distributed mode routes. It does
not duplicate unrelated admin, gallery, authentication, or user-management tests.

Coverage has three layers.

### Public API conformance

Reuse the existing mock-backend API expectations where possible. Run requests
through a frontend for these operations:

- non-streaming and streaming text generation;
- embeddings;
- image generation and image inputs;
- video generation and its image and audio inputs;
- 3D generation and its source asset;
- non-streaming and streaming TTS;
- sound generation;
- non-streaming and streaming transcription;
- detection APIs that accept image or audio files;
- reranking, tokenization, detokenization, scoring, and stores operations;
- supported audio analysis and conversion operations;
- model metadata and other path-free backend calls exposed by an API.

The test must compare structured fixture values. A status-only assertion is not
sufficient.

### File-transfer conformance

Exercise each method that `FileStagingClient` overrides:

- `LoadModel` with the primary artifact and companion files;
- `Predict` and `PredictStream` with image, video, and audio paths;
- `GenerateImage` with source and reference images, plus its output;
- `GenerateVideo` with start image, end image, audio, and output;
- `Generate3D` with a source asset and output;
- `TTS` and `TTSStream` with a model path, voice file, and multiple references;
- `SoundGeneration` with source audio and output;
- `SoundDetection` with source audio;
- `AudioTranscription` and `AudioTranscriptionStream` with source audio;
- `ExportModel` with nested output files;
- quantization progress with its output file.

For each input, the mock backend reads the remote file and reports a digest or
fixture marker. For each output, the mock backend writes deterministic bytes on
the worker. The test compares the bytes retrieved by the frontend.

### Protocol conformance

Exercise representative unary, server-streaming, client-streaming, and
bidirectional-streaming calls through both tunnel paths. Include protocol methods
that do not have a public HTTP route.

Keep an explicit inventory beside the test cases. A guard compares that inventory
with `grpc.InferenceBackend`, `grpc.ControlBackend`, and the file-staging override
set. A new method must get one of these classifications:

- covered by a process-level conformance case;
- covered by a representative generic transport case;
- intentionally local-only, with a reason.

Compile-time interface checks continue to protect wrapper completeness. The
inventory protects behavioral coverage.

## Fixture backend

Extend `tests/e2e/mock-backend` instead of creating a second mock protocol.
Existing local E2E tests continue to use this backend.

The backend emits small deterministic fixtures:

- a valid PNG for image output;
- a small video fixture or stable opaque bytes when the API does not decode it;
- a valid WAV for TTS and sound output;
- a small GLB fixture for 3D output;
- nested files for export and quantization output;
- fixed protobuf values for path-free calls and streams.

File-bearing methods fail when a required fixture is absent or has unexpected
content. This behavior proves that the worker received the input. Output tests
compare exact bytes or a digest after the frontend retrieves the file.

The backend keeps fixtures small so transfer coverage does not dominate the job
duration. Existing large-transfer tests continue to cover multiplexing pressure.

## Test organization

Keep process lifecycle helpers in the existing cluster harness. Put the feature
matrix in focused files under `tests/e2e/distributed/`.

Use table-driven cases for methods with the same request and response shape. Use
separate cases for streaming, file transfer, lifecycle, and error behavior.

Start the cluster once for the feature matrix when test isolation permits it.
Reset model and request state between cases. A failure must report the endpoint,
frontend replica, worker, and expected fixture marker.

Run each transport-sensitive group against these paths:

1. A request enters the frontend that owns the worker tunnel.
2. A request enters the other frontend and crosses the peer relay.

Do not duplicate path-independent response checks when one response assertion and
one transport matrix can prove the same behavior.

## Main-branch baseline

Create a temporary worktree at the current `origin/master`. Apply only the test
and fixture-backend changes needed to run the conformance cases. Do not change
production behavior in the baseline worktree.

Classify results as follows:

- A case that passes on `master` must pass on the pull-request branch.
- A case that fails only on the pull-request branch is a regression.
- A case that exposes an existing `master` defect is recorded separately. Fix it
  in this pull request only when distributed tunnel compatibility requires it.
- A branch-only feature has a focused expected result and no false baseline claim.

Delete the temporary worktree after recording the commands and results.

## Current CI regressions

Fix each regression with a focused failing test before changing production code.

### Node health transition

A tunnel departure can mark a node unhealthy while its last heartbeat remains
fresh. The stale-heartbeat branch later skips unhealthy nodes, so automatic
offline handling never completes.

The expected state sequence is `healthy -> unhealthy -> offline` when automatic
offline handling is active and the heartbeat becomes stale.

### Backend stop compatibility

New workers return a truthful `BackendStopReply`. An older worker can return an
empty successful HTTP response. A rolling upgrade must accept the legacy empty
reply while preserving an explicit failure from a new worker.

Tests cover legacy success, explicit success, explicit failure, and transport
failure.

### Restricted database test

The database latency test constructs a node registry with a role that cannot run
schema migrations. Separate schema setup from the runtime fault-injection role.
The production registry keeps its normal migration contract.

## Error behavior

The conformance suite treats these conditions as failures:

- the mock backend cannot read an expected staged input;
- input bytes or fixture markers differ;
- the frontend cannot retrieve an output;
- output bytes differ;
- a stream loses, reorders, or duplicates fixture frames;
- an explicit backend failure becomes success;
- a direct request works but its relayed equivalent fails;
- a backend method has no coverage classification.

Assertions use bounded deadlines. Process logs remain available on failure.

## Verification

Run verification in increasing cost order:

1. Focused unit tests for the three CI regressions.
2. Fast in-process distributed tests.
3. The feature matrix through compiled frontend and worker binaries.
4. Existing cluster lifecycle and relay tests.
5. The same compatible conformance cases in the `origin/master` worktree.
6. The repository targets used by `tests-e2e-distributed` and
   `tests-e2e-cluster` in CI.

Do not lower a timeout or coverage gate to make a failure pass. Ask before a full
repository build if targeted binary builds cannot satisfy the suite.

## Non-goals

- Testing admin APIs that do not route to a backend.
- Testing model quality or backend-specific numerical accuracy.
- Replacing existing unit and in-process tests.
- Adding a second distributed transport or a test-only frontend endpoint.
- Making the mock backend emulate large model runtimes.
