# Chat status line: critique and design

Date: 2026-10-30. Scope: the line under the chat composer in the React UI
(`<div className="cx-foot">` in `core/http/react-ui/src/pages/Chat.jsx`): the
turn state on the left and the context meter on the right.

## 1. Evidence

Screenshots of the line in 16 states, at 1440x900 and 390x844, in the light
and the dark theme (64 full-page shots and 64 crops of the dock). The states
were held still with the e2e route stubs and a fake `EventSource`, the way
`e2e/chats-apps.spec.js` drives a live turn: idle (new chat, a chat with
history, with a meter, with the composer focused), a message waiting while the
model types, model loading, thinking, a tool running, streaming with a speed,
context at 84 % and at 97 %, failed, interrupted, stopped, stopped with a
message waiting, and no model installed.

## 2. Critique

What works and must stay: one quiet line, muted, tabular numbers, and two
facts that operators want (how full the context is and how fast the model
answered). The owner's call is right: the idea is good, the presentation is
not.

### 2.1 The state is implicit

- While a turn runs the line says `Working…` and nothing else, in every phase:
  the model loading (`loading-model`: a load card in the thread says
  "Loading qwen3-8b", the line still says "Working…"), reasoning, a tool call
  (`tool-running`: the thread shows the step, the line does not) and streaming.
  The line is the one place that is always in view, and it gives the least
  information.
- When nothing runs the left side is empty (`idle-history`, `idle-meter`).
  There is no "Ready": an empty slot reads the same as "the page has not
  loaded" or "the stream dropped".
- A new chat (`idle-empty`) and `no-model` show no line at all. No model is
  the most important state to say and the line is silent (the card under the
  composer says it, which is right; the line could at least agree).
- `stopped-waiting`: the turn stopped and a message waits for **Send now**.
  The line says `Stopped`, which is true but not what matters: the chat is
  waiting for the person. Nothing says so outside the pending card.
- No state has an icon or a dot. Failed and interrupted are told only by the
  amber/red text colour (`data-warn`), and failed and interrupted have the
  same colour, so the difference is in the words only. Colour is the only
  cue that something is wrong.

### 2.2 The keyboard hint competes

- "Enter to send, Shift+Enter for a new line" sits inside the composer on
  every desktop shot, also while a turn runs (`streaming-desk-*`,
  `tool-running-desk-*`), where Enter queues a message instead. It repeats at
  the same weight as the state, two lines above it, and the empty chat repeats
  it a third time under the composer ("Enter sends, Shift+Enter adds a
  line").
- On a phone the hint is already hidden (`chat.css`, below 640 px), which is
  correct: a touch keyboard has no Shift+Enter.

### 2.3 The meter is a number with no way in

- `12.3k / 32.8k tokens (38%) · 41.3 tok/s` in 12 px muted text with a 48 x 4
  px bar (`crop-streaming-desk-light`). It is a `<span>` with a `title`: no
  focus, no click, nothing on a touch screen, nothing for a screen reader
  beyond the text.
- It does not say what "context" is, which model and backend answered, the
  tokens generated, the time to the first token, or whether 41 tok/s is
  normal for this chat. `completion` is in the payload and is thrown away.
- At 84 % (`context-warn`) the bar and the text turn amber; at 97 %
  (`context-full`) the bar turns red. Nothing says what happens next or what to
  do. The server compacts the older part of the chat by itself, and nobody can
  learn that from the UI.
- The speed is the last request's figure and stays the same colour and weight
  after the turn ended (`stopped-*`), so a stale number reads as live.
- After a reload the meter disappears until the next request (the server
  does not keep usage). This is honest and stays.

### 2.4 Failed and interrupted

- `failed`: the thread has the error card ("The reply failed", Retry). The
  line repeats "The last turn failed." in amber with no icon. The line is the
  right place for a short state, not for a second copy of the sentence.
- `interrupted`: the only signal is the line ("The last turn was
  interrupted.") in the same amber as failed. There is no card and no action,
  so the line carries the whole message.
- A separate change (branch `fix/chat-error-cause`) turns the `status` string
  of a failed turn into the cause and a Retry. This design keeps the `status`
  producer as it is and only changes how the line shows it: a state word with
  an icon, then the `status` sentence as muted detail.

### 2.5 Phone width

- 390 px: `Working… 12.3k / 32.8k tokens (38%) · 41.3 tok/s` fills the line
  edge to edge (`crop-streaming-mob-*`). A longer state, such as "The last
  turn was interrupted." (31 characters), next to the meter cannot fit one
  line: the flex row has no truncation, so it wraps to two lines and pushes
  the composer up.
- The bar is hidden below 640 px, so the phone keeps the longest text and
  drops the only glanceable part.

### 2.6 Smaller issues

- `aria-live="polite"` on a span that also changes for every phase would be
  right if the phases were there; today it only announces "Working…".
- The meter does not have `role` or a name; its numbers are read as text with
  "k" abbreviations.
- Mono text: the line uses sans already (good), the meter label used mono in
  the header style (`.cx-ctx` is mono by default and the foot overrides it).

## 3. Design

### 3.1 The line

One line, 24 px high, muted, `--fs-meta`, tabular numbers. Never wraps:
the left side truncates with an ellipsis, the right side keeps its width.

```
[icon] State word · detail…  (hint when focused)        [▬▬▭] 12.3k / 32.8k tokens · 41 tok/s
```

Left: **one** state, made of an icon (or a 6 px dot when idle) and a word.
The detail, when there is one, follows in muted text and is cut first.

| State | When | Icon | Tone | Detail |
|---|---|---|---|---|
| Ready | a chat with no turn running and nothing to decide | 6 px dot | idle | none |
| Sending | the send request is on its way and no turn runs yet | spinner | accent | none |
| Loading model | a turn runs, nothing has streamed, the model is not loaded | spinner | accent | the model id |
| Thinking | a turn runs and the last thing that arrived is reasoning, a finished tool call or nothing yet | spinner | accent | none |
| Running tool | a tool call has started and not ended | spinner | accent | a present-tense verb from the tool-label map ("Reading", "Searching", "Running a command"; "Running <tool>" for an unknown tool) and its argument (`q2.pdf`) |
| Answering | the last thing that arrived is answer text | spinner | accent | none |
| Waiting for you | no turn runs and messages wait for **Send now** | clock | warn | "1 message waiting" |
| Stopped | the person stopped the last turn | stop | idle | none |
| Failed | the last turn failed | alert-circle | error | the `status` sentence |
| Interrupted | the last turn was cut by a restart | warning | warn | the `status` sentence |
| No model | no chat model is installed | 6 px dot | idle | none |

Order when no turn runs: Failed, Interrupted, Waiting for you, Stopped,
Ready.

A new, empty chat keeps the state out of the line (only Sending shows): the
empty state under the composer already says "qwen3-8b is loaded and ready"
or "Chat needs a language model", and a second "Ready" next to it was noise
in the first after-shots. A turn that runs always wins. The state is derived by one pure function
(`chatFootState`) with unit tests.

- `role="status"` and `aria-live="polite"` go on the state word only. The
  live speed and the meter are outside the live region, so speed ticks are
  never announced.
- Under reduced motion the spinner stops (the global rule); the word still
  changes.
- The keyboard hint ("Enter to send · Shift+Enter for a new line") moves from
  the composer to this line. It shows only when the composer has focus, the
  state is Ready or Stopped, and the composer is empty. It is hidden on a phone
  and on a coarse pointer. While a turn runs, Enter queues; the composer's own
  placeholder already says so.

### 3.2 The meter

A compact button (`data-testid="chat-context-meter"`), ghost, 24 px, with a
focus ring: a 40 px bar and `12.3k / 32.8k tokens`, then the speed.

- Above 80 %: amber bar, amber text and a `warning` icon before the bar.
  Above 90 %: red bar, red text and the same icon. The icon is the non-colour
  cue. Below 80 % the meter is muted.
- Speed: the measured `tokens_per_second` of the last request, `41 tok/s`.
  While a turn streams the figure is an estimate from the number of streamed
  pieces per second, shown with `≈` and refreshed at most twice a second. The
  measured figure replaces it when the request ends. After the turn ends the
  last figure stays, dimmer (`--dk-muted-2`), so a stale number does not read
  as live. The number has a fixed minimum width (`ch`), so the line does not
  shift when it changes.
- The button opens details: the existing `Popover` on a desktop (it flips up
  over the composer), a bottom sheet (`SidePane`) below 640 px. Escape and a
  click outside close it and focus returns to the button.

Details (only what the server gives; a row without data is left out):

- **Context**: a full-width bar, "12,345 of 32,768 tokens (38 %)", and one
  sentence: "The context is what the model reads for each request: the
  instructions, this chat so far and the tool results."
- Above 80 %: "When the chat gets close to the limit, the server compacts the
  older part so the model can go on. The transcript keeps every message." No
  action button: there is no manual compact and no "new chat with a summary"
  in the product today. (A plain new chat exists, but loses the history, so it
  is not offered as the fix.)
- **Model**: the model of the last request. **Backend**: the engine serving
  it, from `/system` `loaded_models` (already polled by the page). **Node**:
  the worker that served the request, only when the server exposes it
  (`--expose-node-header`); see the payload change below.
- **Tokens**: in (`used`) and out (`completion`) of the last request.
- **Time to first token**: measured in the browser from `turn_start` to the
  first streamed piece of the turn, so it includes a queue and a model load.
  Shown only for a turn the page saw start. **Prompt processing**: the
  backend's own prompt time, when the backend reports it.
- **Speed**: the last request, and the average of the requests this page
  has seen in this chat (up to the last 10), when there are two or more.
  Labelled "this session", since the server does not keep usage.

### 3.3 Phone (below 640 px)

`[icon] Word …` on the left; on the right the meter shows the percentage only
(`38%`, or the token count when the limit is unknown) with its icon at 80 %
and above. No speed, no hint, no detail text on the line: all of it is in the
bottom sheet. One line at 390 px in every state.

### 3.4 Payload change (minimal, additive)

The `usage` event of `GET /api/chats/{id}/events` gets three optional fields:

- `model`: the model the request used.
- `node`: the worker node that served it, copied from the `X-LocalAI-Node`
  response header. Present only when an administrator turned on
  `--expose-node-header`, so it reveals nothing the server does not already
  choose to reveal.
- `prompt_ms`: the backend's prompt processing time in milliseconds
  (`timing_prompt_processing`), when it reports one.

All `omitempty`; old clients ignore them, and the UI works without them. The
backend id comes from `/system`, which the page already reads, so it is not
added. Docs: `docs/content/features/chats.md` (the event and a short "The
status line" section) and the `/api/instructions` entry.

### 3.5 Motion

Only S0 tokens. The state word cross-fades (`opacity`, `--dur-fast`) when it
changes; the spinner is the kit's `dk-spin`. The meter bar fill changes with a
`transform: scaleX` transition at `--dur-base`. The popover uses the existing
popover entrance; the sheet uses `SidePane`'s. Reduced motion: all off.

### 3.6 Not done here

- The error card in the thread and the wording of a failure (separate branch).
- The fonts: in these screenshots every face renders monospace. That is the
  font-role issue of the polish audit, not part of the footer.

## 4. After

The same 16 states were captured again at both sizes and in both themes,
plus each state with the details open. `e2e/chats-footer.spec.js` holds the
states still with the same stubs and checks them. The meter's accessible name starts with
its visible text (label in name), then the speed, then "Context and speed".
