+++
url = "/features/chats-event-triggers/"
disableToc = false
title = "Event triggers"
description = "Start a chat run when a service, such as GitHub, sends an event to a webhook."
weight = 14
devbuild = "feature"
+++

An event trigger starts a run of a chat when something outside sends it an event: a pull request on GitHub, a build that fails, a script that calls `curl`. The sender posts to a URL that belongs to the trigger, and the trigger's own secret is the credential. This page shows how to create one, how a sender signs a call, and what limits apply.

For the other kinds of trigger (schedule and file), the run, the plan card and the API, see [Triggers]({{% relref "/features/chats-and-agents/chats-agents" %}}#triggers).

## Create an event trigger

There are two ways.

- **Ask in a Studio chat.** Say what should start the run, for example "When someone opens a pull request on my repository, read it and tell me if it changes the docs." The model proposes an event trigger. It shows a card with the sender, the event type, the matches, and what the run may do. Nothing exists until you confirm. When you confirm, the card shows the hook URL, the secret and a `curl` example once.
- **Use the form.** On the Triggers page, create a trigger and choose **When an event arrives**. Fill in **Sender** (the `source`), **Event type**, **Only when (one field=value per line)**, the prompt and the model. After you create it, the page shows the secret once.

A chat in a messaging app cannot propose an event trigger. Create it in Studio.

Copy the secret into the sender at once. The server does not show it again. If you lose it, rotate it with **Rotate secret** on the trigger (or `POST /api/chats/triggers/{id}/secret`). The old secret stops working at the same moment.

The URL of the hook is the address of your server and the path `/api/chats/triggers/{id}/hook`.

## Send an event

The hook is `POST /api/chats/triggers/{id}/hook`. It needs no login and no API key. The secret of the trigger is the credential. A sender can use one of three ways. The server picks the way from the headers, in this order.

In the examples, `$URL` is the hook URL and `$SECRET` is the secret of the trigger.

### Signed call

Send a Unix timestamp in seconds and a signature. The signature is `sha256=` followed by the hex HMAC-SHA256 of the timestamp, a dot, and the exact body, with the secret as the key.

```bash
BODY='{"type":"build.failed","branch":"main"}'
TS=$(date +%s)
SIG="sha256=$(printf '%s.%s' "$TS" "$BODY" | openssl dgst -sha256 -hmac "$SECRET" -hex | sed 's/^.* //')"
curl -X POST "$URL" \
  -H "Content-Type: application/json" \
  -H "X-Webhook-Timestamp: $TS" \
  -H "X-Webhook-Signature-V2: $SIG" \
  -H "X-Webhook-Event: build.failed" \
  -d "$BODY"
```

The timestamp must be within 5 minutes of the server clock. Use this way when you write the sender yourself.

### GitHub

GitHub signs the body with the secret and sends `X-Hub-Signature-256`. The server accepts that header as it is.

1. Open the repository on GitHub, then **Settings**, then **Webhooks**, then **Add webhook**.
2. In **Payload URL**, enter the hook URL of the trigger, for example `https://example.com/api/chats/triggers/<id>/hook`. The server must be reachable from the internet over HTTPS.
3. Set **Content type** to `application/json`.
4. In **Secret**, paste the secret of the trigger.
5. Under **Which events would you like to trigger this webhook?**, choose the events you need, for example **Pull requests**. Do not choose **Send me everything**: every event that the trigger does not match is a call for nothing.
6. Click **Add webhook**. GitHub sends a `ping`. If the trigger has an event type such as `pull_request`, it answers `202` and starts no run. A trigger with an empty event type takes the ping as an event.

Create the trigger with the source `github`. The server reads the event type from the `X-GitHub-Event` header (`pull_request`, `push`, `issues`) and uses `X-GitHub-Delivery` as the key that removes repeats.

### Bearer secret

For a script or a device that cannot sign, send the secret itself.

```bash
curl -X POST "$URL" \
  -H "Authorization: Bearer $SECRET" \
  -H "X-Webhook-Id: build-1842" \
  -d '{"type":"build.failed","branch":"main"}'
```

Send it only over HTTPS. Never put the secret in the URL (a query string ends up in logs and in browser history).

### What the server answers

| Answer | Meaning |
|---|---|
| `202` with `{"run": "<id>"}` | The event started a run, or joined a run that waits, or was a repeat of an event that was already taken. |
| `202` with `{"run": ""}` | The event is valid but does not match the trigger. No run. |
| `404` | Refused. The body is the same for an unknown id, a trigger that is off, a wrong secret, a bad signature and a timestamp that is too old. The answer does not tell which. |
| `413` | The body is larger than 256 KiB (262144 bytes). |
| `429` | More than 60 authenticated calls in one minute for this trigger. `Retry-After` is 60. |

## How an event matches

A trigger has a source, an event type and up to 8 matches.

- **Source** is the name of the sender, for example `github` or `webhook`. It is required. The server sets it to `github` when the call has an `X-GitHub-Event` header, to the `source` query parameter when there is one, and to `webhook` in all other cases. Letters are not case sensitive.
- **Event type** is checked in the same way. An empty type takes any type. The server reads the type from `X-GitHub-Event`, then from `X-Webhook-Event`, then from a text field `type` in the JSON body, and uses `event` when it finds none.
- **Matches** are `field=value` checks on the JSON body. `field` is a dot path, such as `action` or `pull_request.base.ref`. A number in a path is an index in an array. Each check compares text: the value of the field must be equal to `value`. A body that is not JSON matches only a trigger with no matches.

A match is a fixed check, and it is cheap. For a rule that needs judgment, write the condition in the prompt, for example "Act only if the pull request changes a file under `docs/`." A model that proposes the trigger can add one line like this as `Act only if: ...`. Every run is told that when the condition of the prompt does not hold for the event, it must say so in one line and stop. Such a run uses a few tokens and the model is allowed to end it at once.

## What a run may do

The run starts a chat with your prompt and the event data. The payload goes into the prompt as data between markers, cut to 8000 bytes, and the model is told it is not instructions.

An event run has only the tools that the trigger names, its `acts`, and only within what the agent of the trigger already holds. A trigger with no acts only reads and reports. It cannot write files or call add-ons. Name an act only when the run needs it.

## Limits

| Limit | Value |
|---|---|
| Body of a call | 256 KiB. A larger one gets `413`. |
| Calls for each trigger | 60 authenticated calls in a minute, counted for each server. A wrong secret is not counted. |
| Payload in a run | 8000 bytes of text. A longer payload is cut. |
| Runs in a day | 24 by default, at most 1000 (`daily_cap`). An event over the limit is recorded as a skipped run with the reason. |
| Gap between runs | 1 minute between two starts by default, at most 24 hours (`min_gap_seconds`). |
| Payloads in one waiting run | 8 |

### Events during the gap

A run waits until the gap since the previous start has passed. Events that arrive while a run waits are not lost and do not make more runs. They join the waiting run: it keeps up to 8 payloads, oldest first, and says how many older ones were dropped. Each joined event shows as a skipped row in the run list, with the reason "merged into a waiting run". The run then starts once and sees all of them.

An event that arrives while a run is going makes a new waiting run, unless a run of the trigger is already waiting: then it joins that one.

### Repeats

The sender should give each event a stable key, because the server uses it to take one event only once, even if the sender retries. The key is the first of:

- for a signed call, the signature;
- `X-GitHub-Delivery`;
- `X-Webhook-Id`;
- the hash of the body and the current minute.

A repeat of an event with the same key answers `202` and makes no new run.

## Security

- The secret is shown once, when you create the trigger or rotate the secret. Lists, reads and administrator views never show it. Store it in the secret store of the sender.
- Rotate the secret when it may have been seen. Rotation takes effect at once.
- Treat the payload as untrusted text. Anyone who can make the sender send text, for example the author of a pull request or an issue, controls part of what the model reads. The prompt tells the model it is data, but no prompt makes this safe by itself. Name only the acts that the run needs, so that a bad payload cannot do much.
- A GitHub call and a bearer call can be replayed by anyone who captured one: the first has no timestamp, and the second is the secret itself. The repeat key stops a replay of the same delivery while the run that it made is kept in the list, but it is no protection against a sender that was never yours. A signed call cannot be replayed inside the 5 minute window, because its signature is the key, and it is refused after the window. Use a signed call when you control the sender.
- Do not put the secret in a URL. Use the headers.
- The hook checks the secret before it counts the call or reveals anything, and uses a constant-time check. An attacker who guesses ids learns nothing: every refusal is the same `404`.
