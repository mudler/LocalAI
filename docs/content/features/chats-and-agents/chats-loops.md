+++
url = "/features/chats-loops/"
disableToc = false
title = "Loops in chats"
description = "Repeat a prompt in a chat on a timer or at a pace the model picks."
weight = 13
devbuild = "feature"
+++

Make a chat repeat a prompt every few minutes, or at a pace the model picks, until you stop it. Type `/loop` in the message box or use the API. The limits (the shortest wait, the loops for each chat and user) are in the sections below.

## Loops: repeat a prompt in a chat

A loop sends a prompt to its chat again and again, as a new message, until you
stop it. Type `/loop` in the message box, or use the API.

```text
/loop 5m Check the build and tell me when it is green.   every 5 minutes
/loop Watch the deploy and tell me when it is done.      self-paced
/loop list                                               the loops of this chat
/loop stop                                               stop every loop of this chat
/loop stop 3f9a01                                        stop one loop (the code that /loop list shows)
```

- **With an interval** (`30s`, `5m`, `2h`, `1h30m`) the prompt is sent at each
  interval. The shortest interval is one minute unless an administrator
  changes it (`chats_loop_min_interval`, never below 30 seconds); the longest
  is 24 hours.
- **Without an interval** the loop is *self-paced*: after each run the model
  calls `schedule_next` with how long to wait (from the minimum interval to
  24 hours) and a short reason, or `loop_done` when the task is complete. A
  run in which the model calls neither ends the loop with a note, so a loop
  never goes on silently. The chat has these two tools only while it has a
  self-paced loop.
- **Started by the model.** A chat without a self-paced loop gives the model
  the `start_loop` tool. When the model tells you that it will check, wait or
  retry later, it calls `start_loop` with the prompt of each run, how long to
  wait before the first run (`first_run_after`, such as `2m`) and a short
  reason. The loop starts at once and does not ask you first. You see it as a
  line above the message box with the reason, and you can pause or stop it.
  The limits below apply: the minimum wait, the loops per chat and per user,
  and the run limit. The tool is offered in your chats, in chats of a saved
  agent (whatever tools the agent allows), in sub-agent chats, and in chats
  that a trigger, a job or a page started. It is not offered when loops are
  off. In a chat that a connector started (Telegram, Slack and so on), the
  sender can be someone from outside, so the tools follow the access model of
  the bot: `start_loop`, `schedule_next` and `loop_done` need the capability
  `loops.start` and a verified (paired) sender, and `wait_for` and
  `wait_agents` need the capability `loops.wait` and a listed or paired
  sender. A wait on a command (`script`) needs a paired sender even then. On
  the page of a bot the two capabilities are two switches, **Let the agent
  wait for a change** (`loops.wait`) and **Let the agent run on a timer**
  (`loops.start`). They belong to the bot alone: a role of the platform never
  holds them, even when its kind lists them. `loops.wait` is on by default for
  new bots and is added once to existing bots; `loops.start` is off until the
  owner turns it on (see [Chat apps and connections]({{% relref "/features/chat-apps" %}})).
  A bot that accepts anyone gives neither to a sender who is not paired. The
  server checks the capability again when the model calls the tool. A loop in
  a chat that a trigger, a job, a page or a connector started ends after a fifth
  of the run limit, at least 1 (20 runs by default). The answer of `start_loop`
  tells the model the limit of its loop, and Settings shows the number next to
  the run limit. The runs of a loop have no sender, so
  a connector chat keeps `schedule_next` and `loop_done` while its loop runs.
  The same holds for a wait: when the model arms a wait or loop in a connector chat, the
  server stores the loop capabilities (`loops.wait`, `loops.start`) that the sender held
  at that moment on the wait itself, and whether that sender was paired. The wake-up has no sender, so it continues the
  sender who armed the wait: it is offered `wait_for` again if the sender held
  `loops.wait`, and `start_loop` only if the sender held `loops.start`. A wait on a
  command is allowed in the wake-up only if that sender was paired. Nothing else
  is granted (`triggers.propose` and the other capabilities stay refused), the limits
  still apply, and the stored capabilities end with the wait. A wait that was armed
  before this change has none stored and behaves as before. This is shared state in the
  loops store (the auth database), so any frontend that runs the wake-up sees it.
- **Report to a messaging app.** `start_loop` takes an optional `report`
  object: `connection` (the ID of one of your connections), `channel` (a channel
  that you approved for it) and `mode`. `on_done` sends the summary that
  `loop_done` gives. `on_problem` sends a message when a run fails, when the
  loop stops at its run limit or because the model asked for no further run,
  and when the system pauses or stops it. `never`, or no `report`, sends
  nothing. The destination must pass the same checks as the delivery of a
  trigger: the connection is yours, is on, is of a kind that can receive
  messages (not a service that agents use only as a tool), the owner lets it
  write in channels, and the channel is approved. The checks run again at each
  delivery, so a connection that you switch off later receives nothing. The loop
  shows the destination and the mode in its line above the message box and in
  `/loop list`, and the API gives them in the `report` field of the loop. If a
  delivery fails, the server logs it and the loop goes on. The tool
  `send_message` is separate: a call to it does not depend on `report`.
- **Sub-agents.** A sub-agent can start a loop, end its turn and wait. The
  parent is not told that the sub-agent finished until its loop ends: by
  `loop_done`, the run limit, a pause after failed runs, or a stop. Then the
  parent gets the last result. Meanwhile the sub-agent shows as running, and it
  counts once against the limit of background sub-agents. Stopping the parent
  or the sub-agent ends the loop. You cannot start a loop in a sub-agent chat
  yourself.
- **Waits: `wait_for`.** A model that says "I will wait until the download is
  done" can arm a wait. A wait is a loop whose check is code, not a model call:
  the server polls a source and calls the model once. The source is a
  read-only tool of the same chat with fixed arguments (for example a tool that
  lists downloads; the chat must be allowed to call it, and only tools whose
  name says they read, such as `list_*`, `get_*`, `search_*` or `*_status`, can
  be polled), a URL, which is read like `web_fetch` and so passes the same
  checks, a `script`, a shell command that the server runs for each check
  (see below), or a `job`, a background job of the same chat (see "Waits on a
  job"). The condition is `change` (the output differs from the first
  check), `match` (a regular expression), `json_equals` (a JSON field equals a
  value) or, for a script only, `exit_zero` (the command exits with status 0).
  For a job only, `exit` and `stall` apply. The first check sets a baseline and
  never wakes the model (a job has no baseline); an error
  is not a change. The model wakes when the condition holds, when the source
  fails `max_errors` checks in a row (3 by default), or at the timeout (`timeout_seconds`; 1
  hour by default, 24 hours at most). Failure and timeout wake it too, and the
  wake-up text says plainly that the condition was *not* met. The check
  interval (`interval_seconds`) is at least the loop minimum. The tool is nib's
  own `wait_for`, answered by the server. A wait is listed with the loops (the
  API gives it `kind: "wait"`), counts against the same limits, and you stop it
  like a loop. A sub-agent that arms a wait stays running until it ends, as with
  a loop.
- **Waits on a script.** With `script`, the server runs the command for each
  check and the model is not called. The command runs where the chat's `bash`
  runs: in the Sandbox add-on when one serves the chat, otherwise in the
  built-in bash on the server. It has the same fixed environment, the same
  120 second timeout and about the same 64 KiB output limit as `bash`, and its output
  is the standard output and standard error together. The rules:
  - **A non-zero exit status is a result, not a failure.** The exit status and
    the output feed `change`, `match`, `json_equals` and `exit_zero`; a status
    that changes is a change even when the output does not. Only a command that
    does not finish in time, or that cannot be run, counts toward
    `max_errors`. For `json_equals`, the output of a command that failed is
    read as "not there yet", not as a failed check.
  - **A check changes no file.** It runs read-only: what the command writes is
    not saved to your files and makes no change set. It can read your files.
  - **Arming it needs a chat that can run commands.** `wait_for` refuses a
    script when the chat has no `bash`: bash is switched off for the chat or by
    an administrator, the Shell tool group is off, the server cannot run
    commands and no Sandbox serves the chat, the files are off, the agent works
    in one folder (only the Sandbox can serve that), or the chat is an
    unattended run without an agent. A sub-agent cannot arm a wait on a script.
    In a connector chat the sender needs the `loops.wait` capability, as for
    other waits, and must also be paired (verified): a listed sender who is not
    paired can wait on a URL, a tool or a job, but not on a command.
  - **In the mode `ask` for the built-in bash**, only an administrator can arm
    a script wait. The administrator allows the whole command once, in a card,
    when the wait is armed; a command over 2000 bytes is refused. The checks
    then run with no card. A command that the Sandbox runs needs no card.
  - **The command is shown to you in the chat, not in the list.** The answer of
    `wait_for` contains the command, and the model is told to show it to you.
    The loops list, its API and the interface name the source `script` and show
    a generic line; the command is kept only in the store of the owner.
  - **Budget.** Checks of scripts have their own daily budget per user, apart
    from the 200 model runs: 1500 checks a day (UTC) by default, over all the
    user's waits (`chats_loop_daily_script_checks`, 1 to 100000, in the
    **Advanced** part of the loop settings).
    A check is charged before it runs. When the budget is used up, the wait ends
    and the model is woken with a text that says the condition is not known. A
    wait still ends after 2000 checks, and the limits per chat and per user
    still apply.
  - **If the server cannot run the script** at a check (the server lost its
    host exec, or the Sandbox no longer serves the chat), the wait ends at once
    and the model is woken with a text that says plainly that the script cannot
    run on this node and was not run. The wait is not pinned to a node.
- **Waits on a job.** With `job`, the server reads a background job of the
  same chat that `job start` returned. A check is a read of the job's memory on
  the node that runs it: it starts no process, spends no script budget, asks
  for no approval and never shows a command. The conditions are:
  - `exit` (the default for a job): the job has ended, with any status. The
    wake-up gives the exit status, how long it ran and the last 2000 bytes of
    its output. A non-zero status is stated, never hidden.
  - `stall`, with `idle` seconds (60 to 3600, default 600): the job still runs
    but wrote no output for that long. The wake-up says it may wait for input,
    hang or work quietly, and gives the tail. A job that has already exited
    wakes the wait as exited, never as stalled.
  - `match`: a regular expression over the output the job wrote, read in order
    from the first byte and once each, at every check. A match that falls across
    two checks is missed. When the job exits and its output never matched, the
    wait ends with a plain text that says so and that the condition was not met.
  - `change` and `json_equals` are refused for a job: it has no output to compare.
  A job that has already ended is not armed (read it with `job read`). A job of
  a sub-agent is waited for by the sub-agent. When the job ends, the wait is
  checked at the next tick of the loop host (about 5 seconds) instead of at the
  next interval. A check that cannot reach the node that runs the job changes
  nothing and counts as a failed check (`max_errors`); a job that the node does
  not know ends the wait at once, with a text that says the job is gone with its
  node, that its output and exit status are lost, and that the condition was not
  met. A wait on a job is pinned to the node that runs the job, which it records
  when it is armed. The loops list and its API name the source `job` with the id
  and the label, and never the command.
- **Tools of a sub-agent.** The names of the tools the parent gave a sub-agent
  are stored with its chat. After a restart, or when another frontend runs its
  next loop run, the sub-agent gets back those tools, limited to what the
  parent may use at that time: it never gains a tool, and a tool that is gone is
  removed with a short note to the model.
- **Waiting for sub-agents: `wait_agents`.** A chat that started background
  sub-agents can wait for them with `ids` (empty: all that run now) and a mode,
  `all` or `any_failed_or_all`. The wait is bounded by `timeout_seconds` (30 seconds by
  default, from 10 seconds to 10 minutes); at the timeout the tool returns the
  current statuses and the sub-agents keep running. An unknown id is reported
  at once. `spawn_agent` with `wait` set to true returns after at most two
  minutes of waiting for a sub-agent that ended its turn and holds for a loop
  or a wait; the sub-agent then goes on in the background and its result
  arrives as a message. To be woken when a background sub-agent makes no
  progress, `wait_for` takes the id that `spawn_agent` returned as `job`, with
  `exit` or `stall` (see "Waits on a job" and "Background jobs").
- **The promise guard.** When the last answer of a turn promises a later
  check or action ("I'll check in a few minutes", "Controllo tra poco") and
  nothing is armed in the chat (no active or running loop or wait, no running
  sub-agent, no queued message), the server adds one short note for the model
  and the model gets one more turn: arm a loop, a wait or a background
  sub-agent now, or tell you plainly that it cannot watch. It happens at most
  once per turn. In a chat that a connector started, the note names only the tools that the
  sender may use (see above); with none, it only asks the model to say so
  plainly. Sub-agent reports are
  not checked. The matcher reads English, Italian, Spanish, French and German
  and ignores questions, offers and promises that wait for you.
- **The prompt** is plain text, or a skill: `/<skill-name>` (or
  `/skill <name>`) and its words. Each run sends it as that skill command
  would: the transcript shows the words and the skill's chip. Other slash
  commands cannot repeat. A skill that is gone or switched off at a run makes
  that run fail.
- **The first time in a chat** a card asks *Run this every 5 minutes until you
  stop it?* with **Start** and **Cancel**, because every run costs tokens.
  The card stays with the chat, so it is still there after a reload. Once you
  start one loop in a chat, later loops there start at once.
- **What you see.** Each loop shows as a line above the message box, such as
  *Loop · every 5 min · run 4*, with **Pause** and **Stop**. A message that a
  loop sent has a small *Loop run 4* label, so you can tell it from one you
  typed. A loop that ended by itself stays as a line with the reason until
  you dismiss it.
- **Busy chats.** A run starts only when the chat is idle. When you or
  another run keeps the chat busy at the due time, that run is skipped, and
  the loop waits for its next interval instead of piling runs up. A
  self-paced loop runs as soon as the chat is free.
- **Limits.** Settings > Tools shows three limits first: the shortest
  interval (`chats_loop_min_interval`, 1 minute; also the shortest check
  interval of a wait), the loop runs one user may start in a day
  (`chats_loop_daily_runs`, 200 over all loops, UTC days; after that the loops
  wait for the next day; a wake-up of a wait counts as a run), and the loops
  and waits one chat may have (`chats_loop_max_per_chat`, 3). Waits count
  against the same limits as loops, so a chat with three waits cannot start a
  loop. Under **Advanced** are the loops and waits one user may have
  (`chats_loop_max_per_user`, 10), the runs after which a loop ends
  (`chats_loop_max_runs`, 100; a fifth of it in a chat that a connector, a
  trigger, a job or a page started) and the daily checks of waits on a script
  (`chats_loop_daily_script_checks`, 1500 a day per user). Ended loops and
  waits do not count. A loop pauses itself
  after 3 failed runs in a row; **Resume** starts it again. A run has a time
  limit of 15 minutes.
- **Stop** removes a loop. It does not stop a run that goes on; the chat's
  stop button does that. Deleting a chat removes its loops.

```bash
# Start a loop (no interval: self-paced). The first loop of a chat is pending.
curl -X POST .../api/chats/3f1c.../loops -H 'Content-Type: application/json' -d '{"interval": "5m", "prompt": "Check the build."}'
# {"id":"9b2e...","prompt":"Check the build.","interval":"5m","interval_seconds":300,
#  "state":"pending","runs":0,"running":false,"created":"..."}

# Confirm it (state active), pause it, or resume it.
curl -X PUT .../api/chats/3f1c.../loops/9b2e... -H 'Content-Type: application/json' -d '{"state": "active"}'
curl -X PUT .../api/chats/3f1c.../loops/9b2e... -H 'Content-Type: application/json' -d '{"state": "paused"}'

# List, stop one, stop all.
curl .../api/chats/3f1c.../loops
curl -X DELETE .../api/chats/3f1c.../loops/9b2e...
curl -X DELETE .../api/chats/3f1c.../loops
```


With authentication on, add `-H "Authorization: Bearer $API_KEY"` to each request.

A loop's `state` is `pending` (waits for your yes), `active`, `paused` or
`ended`; `reason` says why it paused, ended or waits, `next` when it runs
next, and `wait` what the model of a self-paced loop waits for.
`GET /api/chats/{id}` carries the same list as `loops`, a `loops` event tells
every open tab that the list changed, and a user line that a loop sent carries
`loop: {id, run}` in the transcript.

### Loop or trigger?

Both run a prompt without you typing it. Choose by where the answers should go
and how often:

| | Loop | [Trigger]({{% relref "/features/chats-and-agents/chats-agents" %}}) |
|---|---|---|
| Where it runs | In the chat where you started it, which keeps its history | A new chat for each run |
| How often | From every minute (or as the model decides) | At most once every 5 minutes, on a schedule, or when a file arrives |
| How long | Until you stop it, it reaches its run limit, or the model says it is done | Until you switch it off |
| Good for | Watching something now: a build, a deploy, a long job | Routine work: a morning summary, filing new documents |

**In a cluster.** State mode: single-runner. The loops live in the shared
store with the chats (the auth database). One frontend at a time fires them:
the one that holds a PostgreSQL advisory lock, as for triggers. A run is
claimed in the store before it starts, and the turn claim of the chat keeps
one turn per chat, so two frontends never start the same run. After a
restart, or when another frontend takes over, the loops go on from the store,
and a run that a dead frontend left behind is finished and counted as failed.
A loop run starts on the frontend that fires it; it is not sent to a chat
worker.
