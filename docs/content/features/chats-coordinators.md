+++
disableToc = false
title = "Chats: coordinator agents"
weight = 25
url = "/features/chats-coordinators/"
+++

A coordinator is a saved agent that keeps working on its own. You write what it
does: the instructions, the standing topics and the rules. It wakes on a
schedule and when something happens, does a slice of work, writes down its state
and sleeps. It can start helper agents for large tasks and contact the people
you named. The server does not decide what the coordinator should do. It only
enforces the limits you set: who it may contact, how much it may send, a pause
switch, an audit log and a budget of runs.

## What a coordinator is

A coordinator is two records with the same ID: a [saved agent]({{%relref "features/chats-agents" %}})
and a coordinator config. A saved agent is a coordinator when it has a config;
the agent record itself does not change, so export, import and the agent list
work as before.

The config has:

| Field | Meaning |
|---|---|
| `contacts` | The people and teams the coordinator may contact, at most 50. Each has an `id` (lowercase letters, digits and dashes, up to 40 characters), a `name`, a `description` and a kind. A `user` contact is a user of this server. An `external` contact is a chat of one of your connector bindings: the binding, the chat to write in, and the sender ID that replies come from (the chat when empty). The list is also the list of channels it may write to. |
| `page` | The name of the page folder `pages/<name>/` that holds the coordinator's state. The folder is created with a starter page and is the only folder the agent may write. Empty: no folder fence. |
| `wake_every` | The schedule: empty for events only, otherwise from 5 minutes to 24 hours. |
| `wake_note` | Text added to every scheduled wake, at most 4000 bytes. |
| `send_per_minute`, `send_per_day` | Caps on the messages it sends. Defaults 10 and 100, at most 60 and 5000. |
| `max_helpers`, `max_depth` | Limits on its helpers. Defaults 8 and 2, at most 64 and 4. |

The operator's ceilings for the helper count and depth, the runs a day, the
coordinators that run at once and the audit retention are
[runtime settings]({{%relref "features/runtime-settings" %}}).

When you create a coordinator the server makes the agent with the grant
`act.files` added, so its chat can write its page, and the starter instructions
when you wrote none. It then makes the one long-lived chat of the coordinator.
The chat has the origin `coordinator`. Nobody watches it, so, like the chat of a
trigger or an agent job, it runs with the `act.*` grants of its agent and
nothing more: a write that the agent does not hold is refused with a sentence
that names the missing grant. If you delete that chat, the next wake makes a new
one. If you delete the agent, its config goes with it and nothing wakes the old
chat again.

A contact must be valid when you save it:

- A `user` contact is you, or a user the server can find.
- An `external` contact is on a binding that you own, that keeps one chat per
  sender or per chat (not per thread or time window), and that accepts the
  sender of the contact.

**State mode.** The configs, the counters and the audit log are in the shared
store, so every frontend of a cluster sees the same state. A standalone server
without a database keeps them in files under the data path. The wake-ups run on
one frontend at a time, the one that holds the reaper lock.
