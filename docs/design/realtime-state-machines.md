# Realtime API state machines — map & re-architecture research

Status: research / design (compaction phase). No code changes implied yet.

The realtime API (`core/http/endpoints/openai/realtime*.go`) grew feature-by-feature
(server_vad → semantic_vad/EOU, streaming pipeline, tool turns, compaction, voice
gate, sound detection, WebRTC). The result is several **implicit** state machines
whose states and transitions are scattered across goroutine-local variables, shared
`Session`/`Conversation` fields under five different mutexes, raw channels, and
`context` cancellation. State is *inferred* from variable combinations rather than
*stored*; several illegal/inconsistent states are reachable.

This document (1) inventories the implicit machines, (2) catalogues the cross-cutting
failure modes, (3) researches how to re-implement them explicitly and verifiably, and
(4) lists the invariants a correct implementation must guarantee.

All line numbers are against the current `feat/realtime-semantic-vad-eou` branch and
will drift; treat them as anchors.

---

## Part 1 — Inventory of the implicit state machines

There is **no `state`/`status` field anywhere** in `Session` or `Conversation`. Every
machine below is reconstructed from variable combinations.

### M1. Connection / transport lifecycle

Two transports implement one `Transport` interface; their lifecycles differ sharply.

- **WebSocket** (`realtime_transport_ws.go`): essentially stateless — a `*websocket.Conn`
  plus a write `sync.Mutex`. No send queue, no send goroutine, no closed flag. "Closed"
  = `ReadEvent` returns an error.
- **WebRTC** (`realtime_transport_webrtc.go`): an explicit-ish machine built from raw
  channels — `dcReady` (closed by `dcDone sync.OnceFunc`), `closed` (closed by
  `closeDone sync.OnceFunc` from *either* `OnConnectionStateChange` or `Close()`),
  `flushed`, `sessionCh` (cap 1), `inEvents`/`outEvents` (cap 256), plus a `sendLoop`
  goroutine and RTP counters under `rtpMu`.

Conceptual states (`connecting → data-channel-open → session-created → active →
closing → closed`) are **not stored**; the only persisted membership state is the
`sessions[sessionID]` map entry (exists `realtime.go:631`→`:1009`). `session-created`
and `session-updated` are *events*, not states.

Teardown order (`realtime.go:989-1010`): `cancelActiveResponse` → `close(decodeDone)`
→ `close(done)` (if VAD running) → `close(soundWindowDone)` → `wg.Wait()` →
`delete(sessions,…)`. Then, WebRTC only, `defer transport.Close()` → `closeDone()` →
`<-flushed` → `pc.Close()`.

### M2. Audio-input / turn-detection (server_vad + semantic_vad + EOU)

One `handleVAD` goroutine (`realtime.go:1322`) on a 300 ms ticker. Mode is
**re-evaluated every tick** under `sessionLock` (`:1350-1357`) so it can flip mid-turn.

- **server_vad** states are encoded by the goroutine-local `speechStarted bool`
  (`:1337`) plus silence *measured* (not timed) as `audioLength - segEndTime >
  silenceThreshold` recomputed each tick (`:1461`). States: idle → inspecting →
  speech-detected → awaiting-commit → committing → transcribing/responding.
  "Holdback" is a byte count (`noSpeechHoldbackSec*rate*2`), not a timer.
- **semantic_vad** adds the `liveTurnState` struct (`realtime_semantic_vad.go`):
  `live` (nil = closed), `unavailable` (sticky degrade → behaves as server_vad),
  `eouAtSec`, `parts`, `itemID` (allocated at turn open so captions can stream),
  `deltasSent`. Extra states: closed, open/streaming-ASR, EOU-pending, EOU-fallback
  (dynamic silence threshold 0 s when EOU pending, else eagerness 8/4/2 s),
  retranscribe-gate, EOU-rejected, finished, discarded.
  The one cross-goroutine edge: the backend recv callback pushes onto `events`
  (buffered 64, **non-blocking — drops on overflow**, `:116-117`); `drainEvents`
  reads it on the tick.
- **Voice gate** (`realtime_voicegate.go`) runs *inside* the commit goroutine:
  resolving → authorized/rejected, with a sticky `voiceVerified` (under `gateMu`) for
  `when:first`.

### M3. Response lifecycle (+ synchronous tool-turn recursion)

A response is "active" iff `Session.activeResponseDone` is non-nil and unclosed
(`responseMu`, `:172`). One goroutine owns it; its lifetime == that channel's. State
is observable only through the `response.*` event stream and `ItemStatus*` on the
assistant item. Logical states: idle → starting → generating-text →
generating-audio → tool-call-pending → tool-executing → awaiting-next-tool-turn →
cancelling → done(completed|cancelled) | failed.

- Cancellation is **cooperative at discrete checkpoints** (`ctx.Err()` at
  `:2172,2364,2394`, `realtime_stream.go:193,202,241,259`).
- The tool loop is **synchronous recursion on the same goroutine**, bounded by
  `maxAssistantToolTurns = 10`; each level mints a fresh `responseID` and emits a full
  `response.created … response.done{Completed}` cycle — so one user turn can emit
  *several* `response.done{Completed}` events under different IDs.
- Terminal events are **not exactly-once**: failed paths `return` with no
  `response.done`; cancelled paths emit `done{Cancelled}`; the completed terminal is
  unconditional at the tail of `emitToolCallItems`.
- **Classifier mode** (`realtime_classifier.go`) is a response-*body* variant, not a
  new machine: at turn 0 it may replace the Predict call with a prefill-only Score
  and canned emission, but it runs inside the same respcoord-issued response, maps
  onto the existing `outcomeCompleted/Cancelled/Failed`, and leaves terminal
  emission with `triggerResponse`. No coordinator states or transitions were added.

### M4. Conversation / compaction

`Conversation`: `Items` + `Memory` (rolling summary) under `Lock`; `compacting
atomic.Bool`. States: normal ↔ compacting. Compaction (`realtime_compaction.go`)
snapshots overflow under `Lock`, summarizes **unlocked**, re-locks and commits guarded
by an optimistic head-`prefixMatches` check. It is launched **only by turn-0
`triggerResponse`** (`:1963`), off the response path — so a long agentic turn
(recursion calls `triggerResponseAtTurn` directly) can append many tool items and
**never compact** until the next user turn (compaction starvation).

### M5. Streaming sub-machines (transcription, chunker, TTS)

Backend LLM/TTS/transcription streams are **synchronous callback recv loops on the
caller's goroutine** — no internal goroutines/channels. The only true concurrent FSM is:

- **TTS pipeline** (`realtime_tts_pipeline.go`): one worker goroutine, an **unbounded**
  mutex-guarded `queue`, a coalesced `wake` chan (cap 1), a `closed` flag, a `done`
  chan closed once by the worker's `defer`, a lock-free `failed atomic.Bool`, and
  worker-owned `audio`/`firstErr` that are safe to read only after `wait()` joins via
  `done`. Idempotent `wait()`; deferred `wait()` backstop guarantees no worker leak.
- **Chunker** (`realtime_chunker.go`): a pure single-buffer FSM (buffering ↔ emitting,
  `flush` = hard boundary). **No concurrency guard** — correctness depends entirely on
  `push`/`flush` being called from one goroutine (the LLM recv loop). On cancel the
  flush is skipped, so the buffered partial clause is intentionally dropped.
- **Transcription** (`realtime_transcription.go`): stateless straight-line function;
  "streaming" is just repeated synchronous callbacks.

---

## Part 2 — Cross-cutting failure modes (why it's a mess)

1. **Shared mutable `Session` config with inconsistent locking (the core problem).**
   `updateSession`/`updateTransSession` mutate `Voice`, `Instructions`, `Tools`,
   `OutputModalities`, `ModelConfig`, **`ModelInterface`**, sample rates, and the
   shared `InputAudioTranscription` pointer under `sessionLock`. But in-flight
   response/speech/transcription goroutines read those same fields **without any
   lock** (`realtime_speech.go:72-79`, `realtime_stream.go:228`, semantic_vad
   `:110`). Reloading `ModelInterface` mid-response is a data race against a running
   Predict/TTS/Transcribe, and the swapped-out model is dropped without Close.
   `sessionLock` actually guards the *global `sessions` map*; it only mutually excludes
   the handful of other sites that happen to also take it (handleVAD tick, the commit
   branch). Response goroutines never take it.

2. **Two writers of the active-response pair.** `startResponse`/`cancelActiveResponse`
   are called from both the main read loop (`:836,973,981,990`) **and** the VAD
   goroutine (barge-in `:1429`, end-of-speech `:1543`). `responseMu` guards only the
   field swap; the `<-done` wait is outside the lock. A read-loop `ResponseCreate`
   racing a VAD `speech_stopped` can have both read the same prior pair, both
   overwrite, and briefly leave **two live response goroutines** both appending to
   `conv.Items`. The "never overlapping" guarantee holds only under the unstated
   assumption that responses are driven from a single goroutine — which is false.

3. **State is inferred, not stored.** Whether a response is active, whether a turn is
   open, whether audio is being buffered — all are derived from combinations of
   booleans, nil-checks, channel state, and `context` error. No single source of truth;
   no place to assert an invariant.

4. **Reachable inconsistent states.** e.g. after a semantic-VAD `discardTurn`,
   `speechStarted` stays true while `lts` is closed, so they disagree and the next
   onset suppresses `SpeechStarted`. Mid-stream cancel leaves the client having seen
   `output_item.added`/`content_part.added` with no matching `…done`. `events`-channel
   overflow silently drops an EOU, degrading EOU-pending to the 2–8 s fallback.

5. **Lifecycle/ownership gaps.** `decodeOpusLoop` is a bare `go` (not in `wg`) and can
   run after `delete(sessions,…)`. `handleIncomingAudioTrack` (pion `OnTrack`
   goroutine) has **no shutdown signal** — it appends to `OpusFrames` until `ReadRTP`
   errors, unjoined by `wg`. WebRTC `outEvents` enqueued before the DC opens are lost
   on early failure.

6. **The `done`-channel/`vadServerStarted` toggle dance.** A single `done` local
   (`:655`) is reassigned to a fresh channel on each VAD start (`:662`) and closed at
   toggle-off (`:670`) and teardown (`:999`). Safe today only because one goroutine
   owns it — one variable name meaning different channels over time is a structural
   fragility, not an explicit lifecycle.

---

## Part 3 — Research: explicit, verifiable re-implementation

The goal the user stated: **transitions cannot lead to an inconsistent state, and we
can verify that.** Four layered techniques, from architecture down to runtime.

### 3.1 Architecture: single-writer session actor (share by communicating)

The root cause of (1) and (2) is *shared mutable state across goroutines*. The most
effective, idiomatic-Go fix is to give each session **one owning goroutine** that holds
all session state with **no locks**, and have every other goroutine communicate with it
over channels:

```
            ┌────────── inbound events ──────────┐
 transport ─┤  client events (ReadEvent)         │
   VAD     ─┤  vad: speech_started/stopped, EOU   ├─►  session actor  ──► outbound
 model I/O ─┤  llm/tts/asr results, errors        │   (owns ALL state,    events
 timers    ─┤  ticks, deadlines                   │    single goroutine)
            └────────────────────────────────────┘
```

- All state mutation happens in one place; `sessionLock`, `responseMu`, `gateMu`,
  `AudioBufferLock`, `OpusFramesLock`, `Conversation.Lock` collapse into "the actor owns
  it." Worker goroutines (Predict/TTS/ASR, opus decode, RTP read) become **stateless
  effects** that take an immutable snapshot in and send results back as events.
- `ModelInterface` reload becomes an event the actor sequences relative to responses
  (e.g. drain/cancel the active response first), eliminating the mid-call swap race.
- Cancellation stays `context`-based but the actor is the only thing that starts/stops
  responses, killing the dual-writer race (2).

This is the actor / CSP model. It does not by itself prove correctness — that's what
3.2–3.4 add — but it makes the state *centralized and explicit*, which is the
precondition for verification.

### 3.2 Make illegal states unrepresentable (type-level)

Inside the actor, model each machine as an explicit state with a **pure transition
function** `next(state, event) (state, []effect, error)`:

- Represent states as a Go **sealed sum type** (interface with an unexported marker
  method, one struct per state carrying only that state's data) so e.g. `EOU-pending`
  data cannot be accessed while `Closed`. This is the Go equivalent of an ADT and is the
  single biggest lever for "inconsistent state unrepresentable."
- The transition function is **total and pure** (no I/O, no goroutines): it returns the
  next state plus a list of *effects* (send event, start Predict, arm timer) that the
  actor executes. Pure transition functions are trivially unit-testable and
  property-testable.
- An unexpected `(state, event)` pair returns an explicit error / stays put and logs —
  never a silent half-transition.

The four machines are **hierarchical** (a statechart): Connection ⊃ Turn(M2) and
Response(M3) ⊃ Tool-turn; Conversation(M4) and the TTS sub-machine(M5) are largely
orthogonal regions. Model them as nested states rather than one flat enum.

Library options (all guard *logic*, none give concurrency safety — that's 3.1's job):
- `qmuntal/stateless` — declarative, hierarchical, guard/entry/exit actions; closest fit.
- `looplab/fsm` — simpler, flat, event-callback based.
- Hand-rolled transition tables — most control, no dep; recommended here given the
  hierarchy and the desire to keep transitions auditable. `go.mod` currently pulls no
  FSM lib.

### 3.3 Design-time formal verification (prove the protocol)

Before/while coding, model the *protocol* (not the Go) in a model checker to prove the
hard concurrency properties exhaustively:

- **FizzBee** (the adopted tool) to specify the actor's event/state space and check: no
  two concurrent active responses; barge-in + ResponseCancel + speech_stopped
  interleavings never deadlock or drop a turn; every `response.created` is eventually
  followed by exactly one terminal; teardown joins all goroutines. The
  cancel/startResponse/barge-in interplay (failure mode 2) is exactly the kind of
  liveness/safety property model checkers exist for.
- Keep the spec small and focused on the M2↔M3 boundary (turn detection ↔ response),
  which is where the real races live.

### 3.4 Implementation-time & runtime verification

- **Exhaustive table-driven transition tests**: since transitions are a pure function,
  enumerate `(state × event)` and assert the result for every cell, including the
  illegal cells (assert they error / no-op). This is the practical stand-in for a proof
  that "no transition leads to inconsistent state."
- **Property-based testing**: feed random event sequences into the actor and assert
  global invariants hold after every step (Part 4). This catches reachable-bad-state
  bugs the example tests miss. (Implemented as Ginkgo/Gomega seeded random-walk specs
  — see Part 6.2 for why not `rapid`.)
- **Race detector under load**: run the property tests with `-race`; with 3.1 there
  should be *zero* shared mutable state, so `-race` cleanliness becomes a meaningful
  signal rather than noise.
- **Runtime invariant assertions + structured transition logging**: log every
  `state --event--> state` with the session ID; assert invariants in dev builds.
  Replace today's silent degradations (dropped EOU, suppressed SpeechStarted) with
  explicit, observable transitions.

### 3.5 Recommended path for LocalAI

1. Specify the M2↔M3 protocol in FizzBee; nail the cancel/barge-in invariants.
2. Introduce a per-session actor (3.1) that owns existing state behind the current
   `Transport` interface — incremental, keeps the event types.
3. Replace each implicit machine with an explicit sealed-state transition function
   (3.2), one at a time: Response first (highest-risk dual-writer), then Turn/VAD, then
   Connection, then leave TTS/Chunker/Compaction (already mostly self-contained) for
   last.
4. Land the table-driven + property-based test suites alongside each machine; gate on
   `-race`.

---

## Part 4 — Invariants a correct implementation must guarantee

These are the "cannot reach inconsistent state" properties to encode as assertions,
property-test oracles, and FizzBee invariants:

1. **At most one active response per session** at any instant (no overlapping response
   goroutines; no two appenders to `conv.Items` from response logic).
2. **Exactly one terminal per `response.created`**: every emitted `response.created` is
   followed by exactly one of `response.done{completed|cancelled}` or a defined failure
   terminal — never zero, never two. (Decide whether agentic tool turns are one
   response or many; make it explicit either way.)
3. **No `response.*` content events after that response's terminal.** No
   `output_item.added`/`content_part.added` without a matching `…done` (even on cancel).
4. **Turn/response coupling**: `speechStarted` ⟺ a live turn is open; barge-in cancels
   the active response *before* a new turn's commit starts.
5. **No config field is read by a worker while being mutated** (reload is sequenced
   against in-flight work; a response uses an immutable snapshot of model/voice/tools).
6. **Audio buffer monotonic & consistent**: commit/clear/append/VAD-drop never lose or
   double-consume bytes; `clear` resets *all* turn state (including `lts`).
7. **No dropped control events**: an EOU/Final is never silently lost (no overflow-drop
   on a bounded channel that changes turn outcome).
8. **Clean teardown**: every spawned goroutine (incl. `decodeOpusLoop`,
   `handleIncomingAudioTrack`) is signalled and joined before the session is deleted; no
   sends after transport close.
9. **Compaction safety & liveness**: compaction never races a reader into a torn
   `Items`; and it actually runs when the trigger is exceeded, including inside long
   agentic turns.
10. **Idempotent close**: every channel/resource closed exactly once on every path.

---

## Implementation status

- **M3 (response coordination) — first vertical slice landed.** Explicit machine in
  `core/http/endpoints/openai/respcoord/` (sealed `State`/`Event`/`Effect` sum types, a
  total pure `Next`, a single-writer `Coordinator`); transition-table + Ginkgo/Gomega
  seeded-property + concurrent conformance tests (green under `-race`); a deterministic
  characterization test pinning the legacy dual-writer race. Authoritative spec:
  `formal-verification/response_lifecycle.fizz`. Gate:
  `scripts/realtime-conformance.sh` (Go layer always; FizzBee when pinned) wired as
  `make test-realtime-conformance` and `.github/workflows/realtime-conformance.yml`. See
  `formal-verification/README.md`.
- **Gate is fail-closed and pinned (done).** `fizzbee.sha256` pins all four platforms;
  the gate hard-fails without FizzBee; CI installs+caches the verified binary with no skip;
  pre-commit runs the gate on `respcoord/**` or `formal-verification/**` changes.
- **M3 wired into the live session (done).** `realtime_respcoord.go` adds `responseSink`
  (the `respcoord.Coordinator` + a goroutine-spawning effect sink) to `Session`. The legacy
  `startResponse`/`cancelActiveResponse` and the dual-writer `activeResponse*`/`responseMu`
  fields are gone; all six call sites (manual commit, `response.create`, VAD speech-stopped,
  `response.cancel`, barge-in, teardown) route through it. Barge-in/cancel are now
  non-blocking (removes the legacy ~300 ms VAD stall); teardown stops input goroutines, then
  cancels + `wait()`s all response goroutines before deleting the session. `EmitTerminal` is
  a no-op for now (the response body still emits its own `response.done`) — coordination is
  fixed without changing wire behavior. Verified: builds, `go vet` clean, all 300 openai
  specs pass under `-race`, and `make test-realtime` (the mock-backend realtime e2e suite,
  12 specs over WS + WebRTC) passes.
- **Single authoritative terminal + populated Output/Usage (done).** One
  `response.created` and one `response.done` per `response.create`, even across the
  server-side agentic tool loop (which is now internal turns of one response, not one
  terminal each). A `liveResponse` accumulator threads through
  `triggerResponse`→`triggerResponseAtTurn`→`emitToolCallItems`/`streamLLMResponse`,
  collecting output items as they complete and summing token usage; `triggerResponse`
  emits the one terminal (completed/cancelled; failed still emits none, matching legacy)
  with `Output` + `Usage` filled in (both were always empty before). Verified: 301 openai
  specs under `-race` (incl. a new `triggerResponse` terminal test) + `make test-realtime`.
  Design note: emission is hoisted to `triggerResponse` (the body owns it) rather than the
  coordinator's `EmitTerminal` effect — at cancel/supersede time the coordinator doesn't
  yet have the body's partial Output, so the body, which does, is the natural emitter. The
  coordinator still guarantees one body run per `response.create`, so "exactly one terminal"
  holds transitively; `EmitTerminal` remains the spec's logical marker (no-op in the sink).
- **M2 (turn detection) — model + spec landed AND wired into the live session.**
  Explicit machine in `core/http/endpoints/openai/turncoord/` (sealed `State` =
  `Idle | Speaking{Turn}`, `Event` = `Onset | Silence | Abort{Reason}`, `Effect` =
  `BargeIn | OpenTurn | EmitSpeechStarted | EmitSpeechStopped | CommitTurn |
  DiscardTurn`, a total pure `Next`, a single-writer `Coordinator`);
  transition-table + Ginkgo/Gomega seeded-property + concurrent conformance tests
  (green under `-race`). The fix it encodes: "speech detected" and "a turn is open"
  — the two legacy variables (`speechStarted` and `lts.open()`) that a `discardTurn`
  could desync (failure mode 4) — become ONE state, so the next-onset suppression
  bug is unrepresentable. Authoritative spec:
  `formal-verification/turn_lifecycle.fizz`, with an `always assertion Coupled`
  (speech ⟺ turn-open), verified non-vacuous (deleting `self.speech = 0` in `Abort`
  makes the checker report `Coupled` violated). The gate
  (`scripts/realtime-conformance.sh`, pre-commit, CI) covers `turncoord` and the
  spec. **Wired (done):** `realtime_turncoord.go` adds `turnSink` (the
  `turncoord.Coordinator` + a loop-local effect sink) to `handleVAD`. The legacy
  `speechStarted` bool is gone; onset/no-speech-clear/commit/teardown route through
  `coord.Apply(Onset|Abort{NoSpeech}|Silence|Abort{Teardown})`. The turn id is
  minted at onset and carried by the coordinator to the committed event (so it
  matches the live captions); `liveTurnState.openTurn` now takes that id instead of
  minting its own. A semantic→server mode switch mid-turn is deliberately NOT an
  abort (it only drops the orphaned live stream and lets the turn continue under
  server_vad), so it stays inline. Verified: builds, `go vet`/`gofmt`/golangci-lint
  clean, all openai specs under `-race`, and `make test-realtime` (12 e2e specs over
  WS + WebRTC) pass.
- **M1 (connection lifecycle) — model + spec landed AND wired.** Explicit machine
  in `core/http/endpoints/openai/conncoord/` (sealed `State` = `Live{VADRunning} |
  Torn`, `Event` = `SetVAD | Close`, `Effect` = `StartVAD | StopVAD | Teardown`, a
  total pure `Next`, a single-writer `Coordinator`); transition-table +
  Ginkgo/Gomega seeded-property + concurrent conformance tests (green under
  `-race`). It replaces the legacy `vadServerStarted` bool + the `done` channel
  reassigned on every turn-detection toggle and closed from two sites (failure
  mode 6): the coordinator owns whether the VAD goroutine runs, so its done channel
  is closed exactly once and never resurrected after teardown; `Close` moves to
  `Torn`, which absorbs every later event so teardown runs exactly once even from
  multiple exit paths (invariants #8, #10). Spec:
  `formal-verification/conn_lifecycle.fizz` (`always assertion TeardownOnce` +
  `NoRunAfterTorn`), verified non-vacuous (deleting `self.torn = 1` in `Close`
  fails `TeardownOnce`). **Wired (done):** `realtime_conncoord.go` adds `connSink`;
  the handler's setup/`toggleVAD`/teardown now route through
  `conn.setVAD(...)`/`conn.close()`; the `done`/`vadServerStarted` locals and the
  manual ordered-teardown block are gone (the Teardown effect performs that
  sequence). Verified: builds, vet/gofmt/golangci-lint clean, openai specs under
  `-race`, `make test-realtime` (12 e2e WS+WebRTC), full conformance gate green
  (3 Go packages + 3 fizz specs PASSED).
- **M4 (conversation compaction) — model + spec landed AND wired.** Explicit
  machine in `core/http/endpoints/openai/compactcoord/` (sealed `State` =
  `Idle | Running`, `Event` = `Trigger | Finished`, `Effect` = `StartCompaction`,
  a total pure `Next`, a single-writer `Coordinator`); transition-table +
  Ginkgo/Gomega seeded-property + concurrent (effect-spawns-work-reports-Finished)
  conformance tests (green under `-race`). It makes the legacy `compacting
  atomic.Bool` single-flight guard explicit: a `Trigger` while `Running` is dropped
  (not superseded — compaction is idempotent work on the same overflow), so at most
  one summarize+evict runs per conversation (invariant #9). Spec:
  `formal-verification/compaction.fizz` (`always assertion SingleFlight`), verified
  non-vacuous (deleting the `if self.active == 0` guard fails `SingleFlight`).
  **Wired (done):** `realtime_compactcoord.go` adds `compactionSink`; the
  `Conversation.compacting atomic.Bool` is replaced by `Conversation.compaction
  *compactionSink` (built at conversation creation with the summarize+evict run
  closure); `maybeCompact` now calls `conv.compaction.trigger()`. The summarizer
  resolution + `compact()` stay in the sink's spawned goroutine (off the response
  path); `compact()` itself (snapshot/summarize-unlocked/optimistic-commit) is
  unchanged. Verified: builds, vet/gofmt/golangci-lint clean, openai specs under
  `-race`, `make test-realtime` (12 e2e), full conformance gate green (4 Go
  packages + 4 fizz specs PASSED).
- **M5 (TTS pipeline lifecycle) — model + spec landed AND wired.** Explicit
  machine in `core/http/endpoints/openai/ttscoord/` (sealed `State` =
  `Open | Closing | Closed`, `Event` = `Close | WorkerExited`, `Effect` = `Wake`, a
  total pure `Next`, a single-writer `Coordinator`); transition-table +
  Ginkgo/Gomega seeded-property + two-writer conformance tests (green under
  `-race`). It is a genuine two-writer machine (producer `Close` from `wait()` vs
  worker `WorkerExited`); it makes the legacy `closed bool` lifecycle explicit and
  monotonic, fixes the latent enqueue-after-close silent drop (enqueue is now gated
  on `Open`), and guarantees idempotent `wait()` (one wake / one worker join). The
  poison `failed` latch stays a lock-free `atomic.Bool` (orthogonal, read per
  clause on the worker's hot path). Spec: `formal-verification/tts_pipeline.fizz`
  (`always assertion WakeOnce` + `Monotonic`), verified non-vacuous (deleting the
  `if self.phase == 0` guard in `Close` fails `WakeOnce`). **Wired (done):**
  `realtime_tts_pipeline.go`'s `ttsPipeline` embeds the coordinator (and is its
  effect sink — `Wake` → `signal()`); `closed bool` is gone; the worker checks
  `closing()` and raises `WorkerExited` on drain, `enqueue` rejects once not
  `Open`, `wait()` raises `Close`. The wake/done channel mechanics are unchanged.
  Verified: builds, vet/gofmt/golangci-lint clean, openai specs under `-race`,
  `make test-realtime` (12 e2e), full conformance gate green (5 Go packages + 5
  fizz specs PASSED).
- **All five mapped machines (M1–M5) are now explicit, wired, and verified.** The
  realtime-conformance gate model-checks all `.fizz` specs and runs all five Go
  conformance suites under `-race`, fail-closed.
- **The machines form a hierarchy, and that relationship is now modeled and
  enforced.** M1 (connection) is the parent region; when it tears down, every child
  must be terminal. Previously this was only an imperative side effect of
  `conncoord`'s teardown ordering, with a real gap (M4 compaction was
  fire-and-forget and could outlive the torn session). Now:
  - `formal-verification/session_lifecycle.fizz` is a **composition spec** that
    models conn + its direct children (vad/M2, resp/M3, compaction/M4) as one
    statechart and asserts `ChildrenDieWithParent` (conn torn ⟹ all children
    terminal) plus "no child starts after teardown". Its non-vacuity reproduces the
    exact M4 gap (drop the compaction-terminate line → assertion fails).
  - `respcoord` (M3) and `compactcoord` (M4) gained an absorbing **`Terminated`**
    state + a `Shutdown` event, so a response/compaction cannot start after
    teardown (structural "no resurrection").
  - `conncoord`'s `Teardown` effect now explicitly drives the children terminal:
    stop+join the VAD goroutine (M2), `respSink.shutdown()` (M3 → Terminated, joins
    response goroutines and their M5 pipelines), and `compaction.shutdown()` for
    every conversation (M4: cancel the in-flight summary via a session-scoped
    context, then join — **closing the gap**). `compact` now takes a `context` so
    teardown can bound the join. M2's terminal is realized by the goroutine join and
    M5's by its existing `Closed`; the persistent coordinators (M3/M4) carry the
    explicit `Terminated` state.

## Part 5 — Library vs hand-rolled (Go ecosystem, verified 2026-06)

Researched against live GitHub/pkg.go.dev data. **Verdict: hand-roll a typed transition
table over sealed sum-type states for the per-connection machines.** No Go library gives
the two properties we most want — *compile-time-illegal states* and a *pure
`next(state,event)->(state,[]effect,error)`*; every library models states as
`string`/`int`/`any` and fires side-effecting callbacks mid-transition. And since the
actor (Part 3.1) drives everything from one goroutine, the libraries' main value-add —
internal locking — is dead weight.

Library landscape:

| Option | Stars / status | Hierarchy | Typed states | Illegal-transition | Viz | Fit |
|---|---|---|---|---|---|---|
| **hand-rolled table + sealed sum types** | — | DIY (parent field / nested switch) | **yes** (sealed iface) | explicit `default:` | ~30 LOC Mermaid emitter | **best** |
| **qmuntal/stateless** (port of .NET Stateless) | 1.36k, v1.8.0 2026-02, maintained | yes (substates, guards, entry/exit, internal/ignored) | `any` | `error` + `OnUnhandledTrigger` + `PermittedTriggers` | DOT | best library fallback if hierarchy grows |
| **looplab/fsm** | 3.4k, v1.0.3 2025-05, maintained | flat | strings | typed errors | **DOT+Mermaid** | only for flat machines wanting free diagrams |
| cocoonspace/fsm | 89, dormant 2021 | flat | int | `bool` no-op | — | lock-free but dead; DIY beats it |
| true Harel statecharts (gstate, statechartx) | ≤10, <1yr, single-author | parallel+history | varies | varies | varies | only if we truly need parallel regions; unproven |
| Temporal / Cadence | large, maintained | n/a | n/a | n/a | n/a | **overkill** — external cluster+DB, durable replay, wrong latency class |

Decision: hand-roll; keep **qmuntal/stateless** as the fallback if one machine grows deep
hierarchy/guards faster than we want to hand-maintain (its `error`-on-illegal-trigger and
`PermittedTriggers()` are the most useful library features for our "reject illegal
transitions" requirement, at the cost of `any`-typed states). Add a tiny Mermaid emitter
over the hand-rolled table so we keep the visualization the libraries advertise.

## Part 6 — Formal design tied to code, and making it authoritative

The user requirement: the formal design is **authoritative** — a coding agent should be
unable to silently change implementation behavior without it being caught against the
spec; the default path is "update the spec and re-verify," not "edit the code and ignore
the spec." This is a *conformance + enforcement* problem, in three layers.

### 6.1 The source of truth & design-time check

Write the concurrency-critical core — the **M2↔M3 boundary** (turn detection ↔ response:
barge-in, ResponseCancel, speech_stopped, the dual-writer race) — as a **FizzBee** spec
and **model-check it in CI**. Keep the spec small and focused on M2↔M3; that is where the
real safety/liveness properties (Part 4 invariants 1–4) live. (FizzBee is the adopted
model checker — see Part 6.4.)

### 6.2 The conformance bridge (code ↔ spec)

The honest finding: design-time model checking is well-supported; the *Go conformance
bridge is thin everywhere* and needs per-spec glue. Two layers, adopted together:

1. **FizzBee MBT** — the authoritative layer. The `.fizz` spec is model-checked, and
   `fizz mbt-scaffold --lang go` generates Go interfaces + a `go test` harness; you
   implement adapters mapping model actions→code and `StateGetter`→state. Conformance
   runs as plain `go test` — the cleanest CI fit. Risk: pre-1.0, essentially one
   maintainer (pin a version + sha256, vendor examples).
2. **Ginkgo/Gomega seeded property tests** — the Go-native floor. A small Go model
   (the test's `open`/`registered` shadow) is the oracle; a fixed-seed random walk
   drives random event sequences against the `Coordinator`, asserting the Part-4
   invariants after each step / per seed. It checks the *implementation* against a Go
   oracle — it complements, but does not replace, the FizzBee check of the *design*.
   (We originally specced `pgregory.net/rapid` here for its `(*T).Repeat` driver and
   automatic shrinking, but LocalAI mandates Ginkgo/Gomega for all tests — its
   `forbidigo` lint forbids stdlib `testing` assertions — and `rapid.Check` needs a
   concrete `*testing.T`/`*rapid.T` that cannot run inside a Ginkgo `It`. Rather than
   weaken the lint gate with an exclusion, the property layer is hand-rolled seeded
   walks: fixed seeds make every failure reproducible, at the cost of `rapid`'s
   automatic shrinking. `rapid` is consequently not a direct dependency.)

These compose: model-check the design (6.1) for "the design is right"; conformance-test
the code (6.2) for "the code matches the design." Add `go test -race` (with `-cpu=1,2,4`,
repeated runs) over the stateful tests for interleaving-bug discovery, and Go native
fuzzing over the *same* harness for coverage-guided sequence exploration + a committable
regression corpus. (`testing/quick` is frozen — do not use.)

There is no viable single-source-of-truth codegen (one spec compiled into both the runtime
Go and the model) for retrofitting existing Go — the candidates are research-grade and
greenfield-only. Our practical substitute is the CI gate below plus a single Go transition
table that emits both the diagram and the test action set.

### 6.3 Enforcement — making the design un-ignorable for agents

Structural enforcement, leveraging this repo's existing non-bypassable gate culture
(pre-commit + monotonic ratchets; `--no-verify` is forbidden, baselines never lowered):

1. **Add a `realtime-conformance` gate** to the pre-commit/CI pipeline that runs (a) the
   model check (6.1) and (b) the conformance bridge (6.2). A behavior change that does not
   conform turns the gate **red**; the only green paths are *make the code conform* or
   *update the spec* — and updating the spec re-triggers the model check, so an illegal
   design is rejected too. This is the actual mechanism that makes "update the design and
   verify" the default rather than optional.
2. **Treat the spec as a ratchet artifact** like coverage: the gate must not be weakened,
   the spec not deleted, the build tag not silently disabled.
3. **Write an `.agents/realtime-state-machines.md` guide** (indexed from `CLAUDE.md`)
   stating the spec is the source of truth: change the spec first, re-run the gate, then
   implement. The doc is secondary; the gate is what enforces it.

### 6.4 Decided stack

- **Implementation:** hand-rolled sealed-state transition functions + single-writer actor
  (Parts 3.1–3.2).
- **Design-time + conformance:** **FizzBee** (decided). `.fizz` spec is model-checked, and
  `fizz`'s Go MBT generator (`mbt/generator/templates/go` → interfaces/adapters/test;
  driven via a gRPC plugin in `mbt/lib/go`) produces a `go test` conformance harness
  whose adapters map model actions → our actor and `StateGetter` → our state. Go is a
  first-class MBT target (Go + Rust are the only two). Verified 2026-06: Apache-2.0,
  v0.5.2, prebuilt linux/macos×x86/arm binaries, ships Claude Code skills
  (`/fizz-spec|check|debug|mbt`) for the spec-authoring loop.
- **Go-native layer:** **Ginkgo/Gomega seeded property tests** run alongside — they
  check the *implementation*, complementing (not substituting for) the FizzBee check
  of the *design*. Skipping FizzBee is NOT "degrading to the Go layer": the design
  authority would be gone. The gate is therefore **fail-closed** (see Enforcement).
  (Originally specced as `rapid`; switched to Ginkgo/Gomega to satisfy LocalAI's
  Ginkgo-only `forbidigo` lint without weakening that gate — see Part 6.2.)
- **Enforcement:** the `realtime-conformance` pre-commit/CI gate + `.agents/` guide
  (Part 6.3).

FizzBee risk mitigations (decided):
- The gate is **fail-closed**: a missing FizzBee is a hard failure, never a silent skip.
  The only bypass is the explicit, loud `REALTIME_CONFORMANCE_SKIP_FIZZBEE=1` (local
  only; CI never sets it; pre-commit runs the gate on any `respcoord/**` or
  `formal-verification/**` change so a pure `.fizz` edit still re-verifies).
- CI **pins the FizzBee release binary by version + sha256** (`formal-verification/fizzbee.sha256`,
  all four platforms, digests from the GitHub release; installer verifies before extract,
  CI caches it). Not go-gettable: `pkg/modelchecker` imports the Bazel-internal `fizz/proto`
  with no committed `.pb.go`, so a plain `go get` won't build — hence the pinned binary.
- Keep the `.fizz` model **portable** (no exotic features) so it stays re-expressible in
  another model checker if FizzBee is ever abandoned — lock-in is at the tooling layer
  only, not the design.

## Open questions (decide before implementing)

- **Scope of the actor refactor**: full single-writer per session, or incrementally
  migrate one machine at a time behind the existing locks? (Suggest: M3 response
  coordination first — it has the load-bearing dual-writer bug.)

Resolved: **FSM library vs hand-rolled** → hand-rolled sealed-state tables,
qmuntal/stateless fallback (Part 5). **Conformance bridge** → FizzBee (model-check + Go
MBT) with a Ginkgo/Gomega seeded-property Go-native floor as hedge (Part 6.4). **Single-source-of-truth codegen**
(PGo/MPCal) → not viable (research-grade, greenfield-only); substitute is the CI
conformance gate (Part 6.3).

**Agentic turn semantics** → invariant #2 is **one `response.done` per `response.create`**
(OpenAI-faithful); the server-side `AssistantExecutor` tool loop becomes internal
sub-states of a single response rather than emitting one terminal per turn. Verified safe
in-tree: the current `response.done` carries only `{id, object, status}` (`Output`/`Usage`
never populated), the React UI (`Talk.jsx:330`) reads only `status`, every unit test
already asserts `ResponseDone == 1` for tool turns, no test expects multiplicity, and the
server-side recursion is untested. Collapsing also fixes a latent "Listening…" flicker
mid-agentic-loop. The client-driven tool loop (fresh `response.create` per round-trip)
legitimately keeps one terminal each — unaffected. Follow-up: actually populate `Output` +
`Usage` in the single terminal (currently always empty).
