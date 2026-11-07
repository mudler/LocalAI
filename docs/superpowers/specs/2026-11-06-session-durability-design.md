# Sessions that survive a restart: runs, chat events and background responses

Date: 2026-11-06. Status: draft for review. Each open question has a
recommendation under [Open questions](#open-questions).

## Intent

When a server pod or a chat-worker pod is replaced, a long chat turn, an
unattended run or a background response must not be lost or ended in silence.
After a restart the user sees one of two things: the work went on, or a clear
record that it stopped and why, with a way to go on.

Other work covers the incomplete-state display in the UI, the UI auto-retry and
the server drain on SIGTERM. This note covers three server-side gaps:

- **D**: turns of unattended runs (triggers, jobs, loops, connector messages,
  goals) and of chats are marked `interrupted` by the reaper and nothing goes on.
- **E**: the chat event stream is live only. A client that reconnects loses the
  text of the message in progress.
- **F**: background OpenResponses runs live in one process. A restart loses
  them, and a resume can only be served by the replica that generates.

## Current behaviour

Verified in the tree at the date above. Chat turns are the studio chats of
`core/services/studio`. The older agent dispatcher, the jobs queue and the
agent pool no longer exist; there is no JetStream use anywhere (the only match
is a comment, `core/services/messaging/subjects.go:289`).

### D. Turns

- A turn is claimed in the database: `Store.ClaimTurn`, `TouchTurn`,
  `ReleaseTurn`, `ReapTurns` (`core/services/studio/store/store.go:~167-190`).
  The claim is the lease; one turn runs per chat.
- The owner beats every 15 s (`service.go:427`, `heartbeat` at `service.go:1233`).
  A turn that did not beat for one minute is stale (`studioTurnTimeout`,
  `core/application/studio_service.go:250`). One frontend holding
  `advisorylock.KeyChatsReaper` reaps every 30 s (`studio_service.go:263-279`).
  A beat that finds the claim gone stops the local turn (`service.go:1246-1253`).
- `ReapStale` (`service.go:1269`) ends the turn as `interrupted`, expires its
  cards, publishes "The turn was interrupted.", notifies waiters and pauses
  the goal (`GoalPausedByInterrupted`). Nothing resumes it.
- A turn cut short by `Service.Close` is also `interrupted` (`service.go:1411`).
  `Close` ends in `Manager.Close`, which waits on two wait groups with no
  deadline (`session/manager.go:347,354`); the process calls it with
  `context.Background()` (`core/application/application.go:723`).
- The transcript is the checkpoint. Each finished message (assistant text,
  reasoning, tool result) is its own row, appended when it ends
  (`session/chat.go:488,661,816`). `Service.Resume` (`chatedit.go:~100`) starts a
  turn with `session.Message{Resume: true}` that goes on from the last row. It
  accepts only `TurnFailed` (`chatedit.go:121`), and only on a user request.
  Not saved: the text of the message in progress, and a tool call in flight
  (its row is written when the tool ends).
- A trigger run that lost its process is set to `failed`, reason
  "interrupted by a restart" (`triggers/runner.go:241-263`, `triggers/types.go:516`).
  `outcome` maps `TurnInterrupted` to the same failure (`triggersvc.go:628`).
  Connectors answer "interrupted" (`connectors.go:1325`). A job's `Job` record
  keeps the webhooks flag; the chat holds status and result (`jobs/types.go:71`).
- An unattended start goes to a chat worker over `chats.start` in the queue
  group `chat-workers` (`messaging/subjects.go:33`, `nodes/chat_start_nats.go:91`).
  It is request/reply with no buffer; delivery is at most once by repo rule
  (`.agents/distributed-seams.md`, "Rules a carrier must keep"). The turn row is
  already the durable record, so nothing is lost at the transport.

### E. Chat events

- `GET /api/chats/{id}/events` says "Events are live only" (`studio.go:609-623`).
  It uses `streamSSE`, which sends no event ids (`endpoints/studio/sse.go:17`).
- In a cluster the `broadcastBus` publishes each event on `chats.events.<key>`
  (`studio/bus.go:42-48,118`): at most once, no sequence.
- The UI closes the `EventSource` in `onerror`, reads the chat again, then opens
  a new one (`react-ui/src/hooks/useServerChat.js:279-292`). The browser's own
  `Last-Event-ID` is not used, because the object is replaced.
- Precedent in the repo: the workspace feed gives ids, takes `Last-Event-ID` or
  `?after=` and sends `resync` when the point is too old
  (`endpoints/studio/workspacefeed.go:20-60`, `studio/wsfeed/feed.go`). Its
  record is a database journal, polled; no bus message is trusted.
- The OpenResponses buffer: sequence numbers, a cap of 8192 events and 64 MiB,
  oldest evicted, a watermark `droppedThrough`, and `ErrOffsetLost`
  (`openresponses/store.go:17-39,481-617`).

### F. Background OpenResponses

- `background=true` runs in a goroutine on `context.Background()`
  (`responses.go:239-263`). The store is a Go map (`store.go:42`). The TTL is
  off by default (`application_config.go:311`, "0 = no expiration"), so entries
  live until the process ends or a TTL is set.
- Distributed mode replicates metadata only, in a `SyncedMap` with no `Store`
  (`sync.go:~77-100`): a replica that starts later sees nothing older. The
  resume buffer stays local; a resume on another replica gets
  `ErrResponseNotLocal`, HTTP 409 (`responses.go:3140-3150`). Cancel is
  delegated to the owner over the bus (`store.go:620`, `sync.go`).
- A restart loses every run, status and event. This conflicts with
  `.agents/distributed-state.md` mode 1 (Shared) as soon as the state must
  outlive a restart: such state needs a `Store`.

## D. Durable turns

### Decision: database, not JetStream

| | Database (the turn row) | JetStream |
|---|---|---|
| Durable record | Exists (`studio_chats`, `ClaimTurn`) | New stream, retention, replicas |
| Lease and heartbeat | Exists (claim, `TouchTurn`, reaper) | Ack wait plus in-progress acks |
| Standalone (no NATS) | Same code path, JSON store too | Needs an embedded server |
| Ops and auth | None new | Stream limits, new JWT permissions in `pkg/natsauth` |
| Status and history | Same row the UI reads | A second source of truth |

Recommendation: database. The repo already says anything that must survive a
gap belongs in a table, and the turn row is the job. No new queue; `chats.start`
stays a request/reply.

### Design

1. **Resume decision.** When a turn ends `interrupted` (reaper or `Close`), the
   store also decides whether it may resume: a chat gets `resume_attempts`,
   `resume_not_before` and `interrupted_at`. The reaper and `Close` set
   `resume_not_before = now + backoff(attempts)` in the same statement that
   records `interrupted`. This mirrors `PendingBackendOp.Attempts` and
   `NextRetryAt` (`nodes/registry.go:~306`); the load-job lease
   (`nodes/load_job_runner.go:~185-200`) is the model for "lost lease means the
   attempt was replaced". The turn claim already does both jobs here, so no
   second lease is added.
2. **Sweeper.** The reaper tick also selects chats with `last_turn =
   interrupted`, `resume_not_before <= now` and `resume_attempts < max`. For
   each it calls a new `Service.ResumeInterrupted`: take the claim, then start
   with `Resume: true`, through `ChatWorkers.StartChat` when workers are on.
   `ClaimTurn` is the dedupe: two sweepers cannot both win. Same advisory lock
   as the reaper; no new key.
3. **Scope.** All origins are eligible, with limits: `max = 3`, backoff 15 s,
   60 s, 5 min; a window of 30 min from `interrupted_at` for a chat a person
   started and is not watching; a chat whose newest row is a user message sent
   after the interruption is not resumed (that message starts a normal turn).
   A stopped turn (`TurnStopped`) is never resumed.
4. **Run records follow the chat.** The trigger runner (`runner.go:241`) and
   `outcome` (`triggersvc.go:628`) keep the run `running` while a resume is
   pending, and set `failed` only when attempts run out. `ReapStale` publishes
   an `interrupted` error event with `will_resume: true` and does not pause the
   goal when a resume is scheduled. Waiters and parent chats are not notified
   until the chain ends. A job's webhook fires once, at the end of the chain.
5. **Idempotency of effects.** Resume re-runs only what the transcript does not
   hold. Gaps and rules:
   - A tool in flight has no row. Before a tool that is not read-only runs, the
     turn appends a `tool_intent` row (name, input hash, call id). On resume, an
     intent row with no result row is shown to the model as "outcome unknown,
     check before repeating", not re-run. I found no per-tool idempotency flag in
     the studio tool definitions, so the first version treats a tool as
     read-only only when it is on an explicit list (workspace reads, search).
   - Outbound messages and webhooks keep their existing delivered flags; the
     resume path must check them. Lane D3 audits each sender (connector send,
     job webhook, notification) and adds a key where one is missing.
   - Transcript rows keep the unique `(chat, seq)`; a resumed turn truncates
     nothing.
6. **Step checkpoint.** Add a `partial` row: while a message streams, the text
   is saved at most every 2 s or 4 KiB into the chat row (`partial_text`,
   `partial_turn`), cleared when the message row is appended. Resume appends it
   as an assistant row marked `cut` and lets the model continue. Without it,
   the text in progress is lost, which is the common case in long answers.
7. **Bounded stop.** `Service.Close` gets a deadline (default 25 s, option). At
   the deadline the remaining turns are marked `interrupted` with the resume
   fields set, and the process goes on. The drain lane decides when `Close` is
   called; this lane only fixes what `Close` promises: it returns in time, and
   every cut turn is resumable.

### Data model

`studio_chats`: `resume_attempts int`, `resume_not_before timestamptz`,
`interrupted_at timestamptz`, `partial_text text`, `partial_turn int`. New
`Store` methods: `MarkInterrupted` (used by reaper and `Close`),
`ListResumable`, `SavePartial`. A `RoleToolIntent` row kind in the transcript.
GORM auto-migrate adds the columns; the JSON store gets the same fields, and
`storetest/contract.go` gains the cases for both stores.

### Failure cases

- Worker dies again during the resume: attempts grow, backoff applies, at
  `max` the chat stays `interrupted`, the run is `failed`, and the user can
  still press resume (`Resume` accepts `TurnInterrupted` too).
- Two sweepers or a sweeper and a user: `ClaimTurn` returns `ErrTurnRunning`;
  the loser does nothing.
- Store down: no resume, no loss; the sweeper retries on the next tick.
- Model or provider fails on resume: the normal `TurnFailed` path; no second
  counter.
- A resume loop on a poison chat: bounded by `max` and by the window.
- A tool with side effects ran, then the pod died before the row: covered by
  the intent row; without it the effect may repeat once. Documented.

### Rollout and tests

Behind `LOCALAI_CHATS_AUTO_RESUME` (default on after one release with default
off in the first). Columns first, then code. Tests: store contract (both
stores); a service test where a fake turn dies and the sweeper resumes it, with
the clock injected; two-instance test on `testutil.NewFakeBus()` and a shared
store (the repo rule): two sweepers, one resume; trigger run stays `running`
across a resume; `Close` deadline test; poison-chat test.

### Lanes and size: L in total

- D1 (M): store fields, `MarkInterrupted`, `Resume` accepts `Interrupted`,
  contract tests. No dependency.
- D2 (M): sweeper, `ResumeInterrupted`, run-record and goal changes, flag. Needs D1.
- D3 (S-M): `tool_intent` rows and the sender audit. Needs D1.
- D4 (M): partial text save and resume. Needs D1.
- D5 (S): bounded `Close`. Independent; coordinate with the drain lane.

## E. Chat event replay

### Design

1. **Ids.** The turn owner stamps each event with `id = <run id>.<n>` (n from 1
   per run) before it reaches the bus, so every replica forwards the same id.
   `session.Event` gets `ID string`; `streamSSE` becomes `streamSSEWithIDs`.
2. **Ring.** The owner keeps a ring per running chat, bounded by count (2048)
   and bytes (4 MiB), oldest out, with a `droppedThrough` watermark, as in
   `openresponses/store.go`. Consecutive text and reasoning deltas are merged in
   the ring (same turn, same kind), so a 2048-slot ring holds a long answer. The
   ring lives only as long as the run.
3. **Reconnect.** The client sends `Last-Event-ID` or `?after=` (the workspace
   feed accepts both). A replica that is not the owner asks the owner for the
   events after that id with a request/reply on a new `chats.replay.<key>`
   subject (add the root to `broadcastRoots`/`controlRoots` in
   `messaging/subject_rules.go` and its test). The owner is found from a new
   `owner_instance` column set by `ClaimTurn`. It replies with the events or
   `resync`.
4. **Fallbacks, always correct.** Owner gone, run id different, id older than
   the watermark, or no answer in 2 s: send `resync`. The client then reads the
   transcript, as it does today. With D4 the transcript also holds the partial
   text, so a `resync` after a restart loses almost nothing.
5. **UI contract.** The hook keeps the last id and opens the new `EventSource`
   with `?after=`. This is a small change in `useServerChat.js:179-292`; the UI
   lanes own it, this lane defines the contract.

Distributed-state mode (`.agents/distributed-state.md`): per-instance ring,
documented, on purpose. It is a cache of the live tail; the durable source
remains the transcript. Standalone mode: one process, the ring answers directly.

### Failure cases

Owner restarts mid-run: new run id, old id gets `resync`, and D2 starts a new
run. A client behind a replica that lost its NATS link: events are missed, the
next event id shows a gap (`n` jumps), the client asks for the range or takes
`resync`. Two tabs: each tracks its own id. A cap of 8 streams per chat per
user is added (the workspace feed has 8 per user).

### Tests and size: M

Ring unit tests (cap, merge, watermark); handler test for header and query;
two-instance test with a fake bus where replica B serves a replay from A; gap
detection; `resync` cases. Split: E1 (S) ids and ring on the owner and the
handler; E2 (M) cross-replica replay and the `owner_instance` column (needs E1
and D1's migration, or its own). E1 ships alone and fixes single-replica
reconnects.

## F. Durable OpenResponses background runs

### Design

1. **Tables.** `open_responses`: `id`, `owner`, `status`, `request` (json),
   `response` (json), `background`, `stream`, `owner_instance`, `lease_until`,
   `last_seq`, `dropped_through`, `created_at`, `expires_at`.
   `open_response_events`: `response_id`, `seq`, `event_type`, `data`, `bytes`;
   primary key `(response_id, seq)`.
2. **Write path.** The owner assigns `seq` as today and writes events in
   batches: every 250 ms or 64 events, and always on a non-delta event
   (`response.created`, item added or done, completed, failed). Status and
   `response` are written at the same points. Caps match the memory store
   (8192 events, 64 MiB per response): after a batch the owner deletes the oldest
   rows and advances `dropped_through`; `ErrOffsetLost` keeps its meaning.
3. **Read path.** Resume, GET and `previous_response_id` read the database, so
   any replica serves them. A resume of a running response replays rows after
   `starting_after`, then tails by polling every 250 ms (as `wsfeed` does) until a
   terminal status. `ErrResponseNotLocal` and the 409 at `responses.go:3140`
   go away. The in-memory map stays as the owner's cache and for the live
   notification channel.
4. **Lease.** The owner renews `lease_until` every 15 s. A leader loop (new
   advisory key) sets `failed`, error code `server_restarted`, on `in_progress`
   or `queued` rows with an expired lease, and writes a terminal event so a
   tailing client ends. Standalone: one sweep at startup for rows whose
   `owner_instance` is not the current process.
5. **No re-run by default.** A generation cannot continue from a cut point,
   and MCP tools in the run have effects. The client sees `failed` and
   resubmits (with `previous_response_id` to keep the context). See open
   questions.
6. **Cancel.** The bus delegation stays. A `cancel_requested` column is added;
   the owner checks it on its lease tick, so a cancel works when the bus
   message is missed.
7. **Metadata sync.** The `SyncedMap` gets a `Store` backed by the table (mode 1
   with a `Store`), so a new replica starts with the rows. Events are not
   replicated through the bus; they are read from the table.
8. **TTL.** `OpenResponsesStoreTTL` applies to `expires_at`; the leader loop
   deletes expired rows and their events. With TTL 0 the rows would grow
   forever, so a database-backed store applies a default of 7 days when the TTL
   is 0 and documents it. Standalone without a database keeps the memory store.

### Failure cases

Owner dies mid-stream: rows up to the last batch survive, at most 250 ms of
deltas are lost, the sweeper ends the response as `failed`, and a resume gets
every row then the terminal event. Database slow or down: the owner keeps
streaming to its live client from memory, retries batches, and marks the run
`failed` if writes fail for longer than the lease. Large events: the byte cap
and batch write bound memory; one oversize event is stored whole. Two replicas
tail the same response: both read; only the owner writes.

### Rollout and tests: L

Migration adds tables first; the code writes to them with the memory store as
the fallback when no database is configured. Tests: store contract run on the
memory store and the table store; restart test (new store on the same database
serves a resume); two-instance test (resume on B of a response run by A);
lease expiry sweep; cap and `ErrOffsetLost`; batch flush on terminal events;
TTL sweep; owner check on every read path (the `accessAllowed` rule stays).

Lanes: F1 (M) table store behind the existing `ResponseStore` methods, batched
writes, read path from the table, TTL. F2 (M) lease, sweeper, `server_restarted`,
`cancel_requested`. F3 (S) `SyncedMap` `Store`, docs page with the chosen mode.
F2 and F3 need F1.

## Order across the three

D1 first (columns and contract). Then D2, D3, D5 and E1 in parallel. D4 and E2
follow D1. F is independent of D and E and can run in parallel from the start.

## Non-goals

- UI work: incomplete-state display and auto-retry (other lanes).
- Draining on SIGTERM (other lane). D5 only fixes what `Close` promises.
- Resuming a model generation from the middle of a token stream.
- Exactly-once effects for external systems. The target is at-least-once with
  checks, and "outcome unknown" shown to the model otherwise.
- A new queue, JetStream, or a second job store.
- Replaying text deltas after the run ends. The transcript is the record.

## Open questions

1. Auto-resume for chats a person started? Recommendation: yes, within the
   30-minute window and only when the person's last message is unanswered;
   outside it, show resume as a button (UI lane).
2. Resume cap and backoff? Recommendation: 3 attempts, 15 s, 60 s, 5 min,
   per-deployment settings later if needed.
3. Re-run failed OpenResponses runs? Recommendation: no in v1. Opt-in per
   request later, only for runs without tools.
4. Which tools count as read-only for `tool_intent`? Recommendation: an
   explicit allow-list now; a `read_only` field on tool definitions as a
   follow-up.
5. Store events per delta or batched? Recommendation: batched (250 ms), since
   per-token writes would cost more than the model.
6. Ring size per chat? Recommendation: 2048 events or 4 MiB, tune with a
   metric on `resync` rate.
7. Default OpenResponses expiry on the database store? Recommendation: 7 days.
8. Should `Close` default to 25 s? Recommendation: yes, below the common 30 s
   termination grace, and make it an option.
