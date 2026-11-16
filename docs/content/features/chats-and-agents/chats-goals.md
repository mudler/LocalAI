+++
url = "/features/chats-goals/"
disableToc = false
title = "Goals in chats"
description = "Give a chat an objective and let it work toward it over many turns."
weight = 12
devbuild = "feature"
+++

Give a chat an objective and let it keep working toward it over many turns, without typing "continue". Set it with `/goal` or the API. A goal pauses when you press **Stop**, and at the limits listed below.

## Goals

A goal is an objective that a chat keeps working toward over many turns, for
example "Turn the notes in /Research into a two-page summary and check every
figure". While a goal is active, the chat does not wait for you to say
"continue": when a turn ends by itself, the server starts the next one.

Set a goal with the `/goal` command in the composer, or with the API:

| Command | What it does |
|---|---|
| `/goal <text>` | Sets the goal. It replaces the goal the chat had, and the chat shows what it replaced. With no turn running, a turn starts on the goal at once. |
| `/goal` | Shows the goal: its text, its state and how many turns and steps it used. |
| `/goal clear` | Removes the goal. A running turn goes on, and nothing follows it. |
| `/goal resume` | Resumes a paused or blocked goal. |

While the goal is active:

- Every turn's instructions name the goal, and the model gets two tools:
  `goal_done` (the goal is reached, with a one-line summary) and
  `goal_blocked` (the model cannot go on without you, with the reason and
  what it needs). The tools exist only while a goal is active.
- When a turn ends by itself, the server sends the next turn
  `Continue toward the goal: "<text>"`. The transcript shows it as a goal
  line, not as a message from you.
- Your messages still steer the work. A message you send while a turn runs
  reaches the model at its next step, and messages that wait run before the
  next automatic turn. Turns that you start do not count as automatic turns.
- Above the composer, a line shows the goal with **Pause** (or **Resume**)
  and **Clear**. The status line shows the progress, for example
  `Goal · turn 3 of 20`.

The goal has one of four states:

| State | Meaning |
|---|---|
| active | The chat works toward the goal. |
| paused | Nothing goes on by itself. The goal pauses when you press **Stop** or **Pause**, when a turn fails, when the server stops during a turn, and at the limits below. **Resume** (or `/goal resume`) goes on with new counts. |
| done | The model called `goal_done`. The transcript shows `Goal reached` with its summary. Set a new goal to go on. |
| blocked | The model called `goal_blocked`. Answer it, then resume the goal. |

Two runtime settings in **Settings > Tools** limit a goal:
`chats_goal_max_turns` (20 by default) is the most turns the server starts by
itself for one goal, and `chats_goal_max_steps` (400 by default) is the most
model steps that the turns of one goal make together. A goal that reaches a
limit pauses, and resuming it starts new counts. The step limit of one turn
(`chats_max_iterations`) still applies to each turn.

A goal belongs to a chat that you started. A sub-agent, and a chat that a
trigger, a connector, a job or a page started, cannot have a goal and never
gets the goal tools.

```bash
# Set a goal. With no turn running, a turn starts on it at once.
curl -X PUT http://localhost:8080/api/chats/$ID/goal \
  -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" \
  -d '{"text":"Turn the notes in /Research into a two-page summary"}'
# {"goal":{"text":"Turn the notes in /Research into a two-page summary","state":"active",
#  "turns":0,"max_turns":20,"steps":0,"max_steps":400,"set":"...","updated":"..."}}

# Read it. GET /api/chats/$ID also carries it as "goal".
curl http://localhost:8080/api/chats/$ID/goal -H "Authorization: Bearer $KEY"

# Pause it (Stop pauses the goal), resume it, clear it.
curl -X POST http://localhost:8080/api/chats/$ID/stop -H "Authorization: Bearer $KEY"
curl -X POST http://localhost:8080/api/chats/$ID/goal/resume -H "Authorization: Bearer $KEY"
curl -X DELETE http://localhost:8080/api/chats/$ID/goal -H "Authorization: Bearer $KEY"
```

A paused goal has `paused_by`: `user`, `turn_limit`, `step_limit`, `error` or
`interrupted`. A done goal has the model's summary in `note`, and a blocked
goal has its reason there. The event stream sends a `goal` event when the
goal changes (its `text` is the new state, empty when the goal was cleared),
so every open tab reads the chat again. The transcript has goal lines with
role `goal` and `goal_event` set to `set`, `replaced`, `continue` (with
`goal_turn`), `paused`, `resumed`, `done`, `blocked` or `cleared`. Goal lines
are not exported and not copied by a fork.

**Several frontends.** The goal is stored with the chat record in the shared
store, so every frontend reads the same goal and nothing about it is kept in
memory. The next turn of a goal goes through the same per-chat turn claim as
any other turn: only the frontend that ran the turn starts the next one, and a
message from you, or a resume on another frontend, never starts a second
turn. If a frontend stops during a turn, the turn is reaped as interrupted and
the goal pauses, so a restart never continues a goal twice.
