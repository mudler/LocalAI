+++
disableToc = false
title = "Chats: Telegram connector"
weight = 23
url = "/features/chats-connectors/"
+++

A connector binds a Telegram bot to one of your [saved agents](/features/chats-agents/).
A person who messages the bot talks to that agent, in a conversation of their
own, and the answer comes back in Telegram. The conversation is a
[chat](/features/chats/) of yours: it uses your quota and your model allowlist,
and you can open it in LocalAI.

## Set up a binding

1. Make a bot with `@BotFather` in Telegram and copy its token.
2. Make an agent that names a model. A connector has no model of its own, so
   an agent without a model cannot be bound.
3. On the Connectors page, make a binding: a name, the token, the agent, and who
   may use it.
4. Use **Test**. LocalAI asks Telegram which bot the token belongs to.
5. Write to the bot from an account on the allowlist.

```
curl -X POST .../api/chats/connectors -d '{"name": "Support bot", "token": "123:abc", "agent_id": "<id>", "enabled": true, "allow": ["12345678"]}'
curl -X POST .../api/chats/connectors/<id>/test
curl .../api/chats/connectors/<id>/senders
```

`GET`, `PUT` and `DELETE /api/chats/connectors/{id}` read, replace and delete a
binding. `POST .../enable` and `.../disable` switch it. You can have 10 bindings.

## What a message gets

- **The agent's tools only.** A message gets exactly the tools the agent grants,
  intersected with what you may use now. It never gets the tool set of a chat, and
  a message cannot change the agent. A run started by a connector never gets
  `propose_trigger`.
- **Your account.** The chat acts as you, so the feature gate, the model
  allowlist and your quota apply. If the model refuses the request, the sender
  gets one fixed sentence, for example that your quota is used up.
- **One chat for each sender.** Each person who writes to the bot has a chat of
  their own with the agent. They cannot see another person's chat. If you change
  the agent of the binding, the next message of each sender starts a new chat
  with the new agent.
- **Text only.** A photo, voice note or file gets a sentence that says only text
  is read for now. A message longer than 8,000 characters is refused. A sender may
  send 20 messages a minute; more are dropped, with one notice a minute.
- **Private chats only.** A message in a group that the bot was added to is
  ignored.
- **Answers.** The bot sends the final answer of each turn, split at 4,096
  characters (the limit of Telegram), and shows "typing" while it works. A message
  that arrives while the agent works waits and is read at the agent's next request,
  as in any chat; the answer of that turn covers it.

## Who may use the bot

The allowlist decides. It is empty when you make a binding, so the bot accepts
nobody. List chat IDs, user IDs or `@usernames`.

A message from anyone else is ignored: it is not answered, and the sender goes
in the sender log with a count. Open **Senders** on the binding to see who tried,
and use **Allow** to add a person to the allowlist.

"Anyone who can message the bot" lets everyone use the agent. LocalAI asks you
to confirm it, because anyone who can find the bot can then use the agent's tools
as you. Use it for a public bot whose agent grants nothing you would mind sharing.

## Notifications in Telegram

Set a notification chat ID on a binding to receive your run notifications there
too (a trigger that finished or failed, for example). The title and the text are
sent, not the link. Find your chat ID by writing to the bot and reading the sender
log.

## The bot token

The token is a secret. LocalAI seals it with AES-256-GCM and ties it to its
binding. No API returns it: a binding only says `has_token`. To change it, send a
new one with an update; an update without a token keeps the old one. The key is
`connectors.key` (mode `0600`) under the data path, or a row of the database that
every frontend shares. LocalAI refuses a key file that other users can read.

LocalAI only calls `https://api.telegram.org`. It reads updates by long polling,
so it needs no public address.

## When a binding switches itself off

A binding is switched off, and you get a notification, when Telegram refuses the
token, when another process keeps reading the updates of the same bot (Telegram
allows one reader; a new leader in a cluster waits and retries for about a minute
before it gives up), when the agent is deleted or has no model, or when the owner no longer
exists. Fix the cause and switch it on.

## Storage and clusters

Bindings, the read position in Telegram's updates, the sender log and the chat of
each sender are stored with your chats: in the database, in JSON files under the
data path, or in memory when there is no data path (then nothing survives a
restart). The JSON store is for one process.

| State | Mode | How |
|---|---|---|
| Bindings, offsets, sender log, sender chats, the key | Shared | Rows in the database. |
| Which frontend reads the bot | Single runner | The frontend that holds the advisory lock (`KeyChatsConnectors`). When it stops, another takes the lock and continues from the stored offset. |
| The reply being sent for a turn | Per instance | The frontend that took the message sends the answer. If it dies, the answer is lost; the message is not repeated. |

## Other connectors

Only Telegram is built. Slack, Discord, IRC, Matrix, email, GitHub and Twitter
connectors of the old agents are not rebuilt; the release that removes the old
agents lists them as dropped.
