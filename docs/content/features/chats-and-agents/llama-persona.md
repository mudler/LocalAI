+++
url = "/features/llama-persona/"
disableToc = false
title = "The LocalAI llama persona"
description = "Download a ready-made agent file that gives the assistant the voice of the LocalAI llama mascot, and learn how to import it and change its tone."
keywords = ["persona","mascot","llama","agent","system prompt"]
weight = 110
devbuild = "feature"
+++

A persona is a way to give an assistant a stable voice. In LocalAI, you do this with a
[saved agent]({{% relref "/features/chats-and-agents/chats-agents" %}}): its
`instructions` are added to the system prompt of every turn. This page gives you a
ready-made agent file for the LocalAI llama, the mascot of LocalAI. Use it as it is, or as an example
of how to write a persona.

## The persona

The llama is friendly, concise and honest. It follows these rules:

- **Voice.** Warm, calm and a little playful. It answers first and adds only the detail you need.
  It does not flatter you and does not use emoji, unless you do.
- **Honesty.** It says what it did and what it did not do. It does not claim that it ran a
  command or read a file unless a tool result in the chat shows it. When it is not sure, it says so.
- **Help.** It asks one short question only when it cannot go on without the answer.
  Otherwise it makes a sensible choice, says what it chose and continues.

The file grants no tools. An agent with empty lists has no add-on tools,
no MCP servers, no collections and no skills (see [Saved agents]({{% relref "/features/chats-and-agents/chats-agents" %}})),
so the persona is only a voice. You can grant tools later.

## Get the files

- Agent file: [localai-llama-agent.json](https://localai.io/docs/downloads/localai-llama-agent.json)
- Mascot picture: [localai-llama-avatar.webp](https://localai.io/docs/downloads/localai-llama-avatar.webp)
  (208 by 208 pixels, 10 KB)

A saved agent has no picture field, so the agent file does not carry the mascot. The picture
is a separate file that you can use where you want it, for example in your own notes or on a
chat page you build. Use the mascot as it is. The rules in
[BRAND-USAGE.md](https://github.com/mudler/LocalAI/blob/master/BRAND-USAGE.md) apply to it: the
art is not under the MIT license.

## Import the agent

You need the `agents` permission. It is off by default for a user; see
[Permissions]({{% relref "/administration/chats-deployment#permissions" %}}).

In the web interface:

1. Open **Agents** in the sidebar (`/app/agents`).
2. Select **Import**.
3. Choose `localai-llama-agent.json`.

The import makes a new agent named `LocalAI Llama` and opens it. A name that you already use gets
" (2)", " (3)" and so on. Then start a chat with it from its row.

With the API:

```
curl -X POST http://localhost:8080/api/chats/agents/import -F file=@localai-llama-agent.json
```

With authentication on, add `-H "Authorization: Bearer $API_KEY"`. To start a chat with the
new agent, send its `id` as `agent_id` to `POST /api/chats`. See
[Export and import]({{% relref "/features/chats-and-agents/chats-agents#export-and-import" %}}).

## Change the voice and the tone

Open the agent and edit **Instructions**, or edit the `instructions` text in the file before you
import it. The text can have up to 32 KiB. These changes are the ones that work best:

- **Make it more formal.** Replace the line "Be warm, calm and a little playful" with
  "Be polite and neutral."
- **Make it shorter.** Add "Answer in at most three sentences unless the user asks for more."
- **Change the language.** Replace "Reply in the language of the user" with "Always reply in
  Italian" (or your language).
- **Change the name.** Edit the first line of the instructions and the `name` of the agent.
- **Remove the llama touches.** Delete the line about the light llama touch.

Keep the **Honesty** rules when you add tools. They are what stops the persona from claiming
work that it did not do. Write each rule as a short, direct sentence. A rule that says what to do
works better than a rule that says what not to do.

## Settings and models

The file sets no model, so a chat uses the model that you choose when you start it. To fix one,
select a model in the agent form, or add `"model": "<name>"` to the `agent` object.
A model name that the server does not have is dropped on import, with a warning.

A persona is only text in the system prompt, so the model must follow instructions. Use a
chat model tuned for instructions. A very small model can ignore some of the rules, for example the
length rule. If the answers drift from the voice, try a larger model, or shorten the instructions.
No model was benchmarked on this persona.
