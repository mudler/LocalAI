+++
url = "/features/chats-tools/"
disableToc = false
title = "Tools in a chat: MCP, web and add-ons"
description = "Give a chat MCP servers, web search, Knowledge collections and add-on tools, and switch each group off for one chat."
weight = 18
devbuild = "feature"
+++

Give a chat tools beyond its built-in ones: MCP servers, web search, Knowledge collections and the tools of add-ons. You can switch each group off for one chat. An administrator can switch a group off for everyone.

## MCP tools

A model that has MCP servers and sets `mcp.auto: true` offers all of them to its
chats, as it does for `/v1/chat/completions`. A user needs the
`mcp` feature, and `LOCALAI_DISABLE_MCP` turns MCP off for the whole instance.
A tool cannot take the name of a built-in tool such as `spawn_agent`.

You can also add MCP servers of your own, stored on the server: see
[MCP servers]({{% relref "/features/chat-apps/mcp-servers#mcp-servers" %}}). Your enabled connectors
are offered to your chats next to the model's servers; the model's own
configuration is not changed.

### Prompts and resources as context

MCP servers can offer prompts (ready-made instructions with arguments) and
resources (documents the server holds). A chat can list them for the servers it
can use now, which means the servers of the model, less the ones you switched
off for the chat, or the ones the chat's saved agent grants, and your enabled
connectors. You can add any of them to a message as context for that turn:

```bash
curl .../api/chats/3f1c.../mcp/prompts      # {"prompts":[{"server":"connector:ab12","name":"review","arguments":[...]}]}
curl .../api/chats/3f1c.../mcp/resources    # {"resources":[{"server":"wiki","uri":"wiki://home"}]}
# Preview one:
curl -X POST .../api/chats/3f1c.../mcp/prompts -H 'Content-Type: application/json' -d '{"server":"connector:ab12","name":"review","arguments":{"lang":"Go"}}'
curl -X POST .../api/chats/3f1c.../mcp/resources/read -H 'Content-Type: application/json' -d '{"server":"wiki","uri":"wiki://home"}'
# Send them with a message. The server expands them as you and puts the text before yours.
curl -X POST .../api/chats/3f1c.../messages -H 'Content-Type: application/json' -d '{"text":"Go ahead","context":[
  {"server":"connector:ab12","name":"review","arguments":{"lang":"Go"}},
  {"server":"wiki","uri":"wiki://home"}]}'
```


With authentication on, add `-H "Authorization: Bearer $API_KEY"` to each request.

A server the chat cannot use is `403 mcp_refused`, and the message is not sent.
A server that does not answer is `502 mcp_error`. Binary parts of a resource are
left out, and the text is cut at 256 KiB. The expanded text is part of the
message in the transcript.

### Switch tools off for one chat

The **Tools** menu over the message box lists the groups of built-in tools, the add-ons you may use, the MCP servers of the
chat's model and your connectors, with a switch each. What cannot be used now is
shown off, with the reason (a connector that is switched off, a model server
that the model does not use by itself). A switch takes effect at the next turn.
Switches only take away: a chat with a [saved agent]({{% relref "/features/chats-and-agents/chats-agents" %}})
lists only what the agent grants and never gets more than that. A trigger run with no
agent copies the switches of the chat that proposed it.

The menu groups the rows under **Built-in tools**, **Add-ons**, **MCP servers
of the model** and **Your connectors**. Each row has the name, a line of text
when there is something to say (why it cannot be used, how the Sandbox runs
commands), a chip with the add-on that serves a built-in tool ("Served by
add-on searx"), the number of tools of an add-on, and the switch. A click on
the name also toggles the switch, and Space toggles a focused switch. The
header gives the count of what is on among what can be used ("4 of 7 on"),
with **Select all** and **Disable all**. A group with two or more switches has
**All on** and **All off** for that group only. These buttons never change a
row that cannot be used, and **Select all** leaves **Saved agents** off: you
switch it on by hand. They send one request for each kind of switch (built-in
tools, add-ons, MCP servers and connectors). A change that the server refuses
goes back, and a line at the bottom of the menu says so. On a phone the menu
opens as a sheet at the bottom of the screen.

```bash
curl -s localhost:8080/api/chats/$ID/tools
# {"addons":[...],"servers":[{"name":"crm","off":false,"usable":true}],
#  "connectors":[{"id":"c1","name":"Docs","off":false,"usable":true}],"off":[]}
curl -s -X PUT localhost:8080/api/chats/$ID/mcp -H 'Content-Type: application/json' -d '{"off":["crm","connector:c1"]}'
```

`off` holds the name of a server of the model, or `connector:<id>`. At most 200 switches.

The built-in tools switch by group. Every group is on until the chat switches
it off, so a chat that never touches the menu has every group:

| Row | Group | Tools |
|-----|-------|-------|
| **Run commands** | `shell` | `bash`, whoever serves it: the Sandbox add-on (isolated) or the built-in bash on this machine; `sandbox_profile` when the add-on has profiles |
| **Your files** | `files` | `list_files`, `read_file`, `write_file`, `edit_file`, `move_file`, `delete_file`, `search_files` and `read_document` |
| **Web** | `web` | `web_search` and `web_fetch` |
| **Images and audio** | `media` | `describe_image` and `transcribe_audio` |
| **LocalAI client** | `localai_client` | The inference tools of the [LocalAI Assistant]({{% relref "/features/chats-and-agents/localai-client-tools#tool-groups" %}}): `generate_image`, `speak`, `embed`, `voice_profile` and more. Each one is offered only when your account has its feature and an installed model can serve it. Saving a voice with `voice_profile` shows a card where you confirm that the speaker agreed; nothing is saved before |
| **Knowledge** | `collections` | `list_collections` and `search_collection` |
| **Sub-agents** | `subagents` | `spawn_agent` |
| **Saved agents** | `saved_agents` | `list_agents` and the `agent` argument of `spawn_agent` (opt-in) |
| **Past chats** | `history` | `search_chats` and `read_chat` (opt-in; see [Past chats]({{% relref "/features/chats-and-agents/chats-sub-agents#past-chats" %}})) |
| **Triggers** | `triggers` | `propose_trigger`, `list_triggers`, `update_trigger`, `delete_trigger` and `list_connections` |
| **Memory** | `memory` | Your notes: the model reads them before it answers and may save new ones (see [Memory]({{% relref "/features/chats-and-agents/short-memory" %}})). Switch it off to make the chat private: it neither reads nor saves memory |
| **LocalAI admin** | `localai_admin` | The tools that manage this server (administrators only, opt-in; see [LocalAI Assistant]({{% relref "/features/chats-and-agents/localai-client-tools#tool-groups" %}})). A chat in assistant mode has it on. It is never offered in a messaging chat or a run that starts by itself, whatever the agent switches on |
| **Suggested changes** | `changes` | `propose_change` and `revert_change`: a model asks to change your notes, an agent profile, a skill, a page or the dashboard, and you confirm (see [change proposals]({{% relref "/addons/contract-reference/connectors-and-chats#change-proposals-from-chats" %}})); for memory notes only, you can choose in your account settings to have them saved at once with an Undo, or never (see [Memory]({{% relref "/features/chats-and-agents/short-memory" %}}#choose-how-a-note-is-saved)) |

```bash
curl -s -X PUT localhost:8080/api/chats/$ID/builtins -H 'Content-Type: application/json' -d '{"off":["web","files"]}'
```

The two LocalAI groups are large, so on a small context window a chat may not send
them in full: it sends `tool_search`, `tool_describe` and `tool_call` instead, and the
model finds and runs their tools through them, under the same permissions. The setting
`chats_tool_search` (`auto`, `on` or `off`) decides; with `on`, the tools of the user's MCP
connectors load on demand in the same way. See
[Tool search]({{% relref "/features/chats-and-agents/localai-client-tools#tool-search" %}}). The tools panel marks
such a group with `"deferred":true`.

`off` replaces the earlier list, and a name that is not a group is refused
(400). A group the server does not offer is not listed: **Your files** and
**Images and audio** need the files, **Images and audio** the helper models,
**Knowledge** the collections. A group that this chat cannot use now is
listed as not usable with the reason, for example **Knowledge** for a user
without the `collections` feature.

**Run commands** governs `bash` from both providers: switched off, the chat
has no `bash`, and the add-on list says `"commands":"switched_off"`. The row
names who runs the commands (**Sandbox** with "Commands run isolated", or
**This machine** with a warning), and the notice about commands on this
machine stays at the top of the menu. When nothing can run commands, the row
is not usable and gives the reason of that notice. The **Sandbox** row under
**Add-ons** stays for the add-on's other tools.

What the model is told follows the tools it has. With **Your files** off it
hears nothing about the file tools: no hint to save files with `write_file`,
no work folder, no pointer to `AGENTS.md`, and no line about writing skills
when nothing can save a file. The list of the chat's files stays, with a line
that the model cannot open them. The files pane, uploads and previews work as
before, and `load_skill` stays: it follows the Skills menu. With **Images and
audio** off the model is not told to call `describe_image`. With **Run
commands** off the model is told that you switched commands off and where to
switch them back on.

A sub-agent follows the switches of its parent chat: it never gets a tool of a
group the parent switched off. A trigger run with no agent copies these
switches from the chat that proposed it, as it copies the others. Each group
keeps its own rules for the runs that start by themselves (for example a
trigger or connector run never gets the trigger tools). `load_skill`, `page_errors`
(it reads the errors of a page's script, see [Pages]({{%relref "features/pages-and-home/pages" %}})),
`open_page` (it opens a page of your home in the dock beside the chat) and the
goal and loop tools have no switch.

The trigger tools have a group too, `triggers`: `propose_trigger`,
`list_triggers`, `update_trigger`, `delete_trigger` and `list_connections` switch together, and the
menu shows them as one **Triggers** row. It is on by default; `{"off":["triggers"]}`
takes the five tools away from that chat. A chat started by a trigger or a
connector and a sub-agent never have them, and the row then shows as not usable
with the reason. The row also shows as not usable when you do not have the
`triggers` feature, and it is not listed at all when the server runs no
triggers (`LOCALAI_DISABLE_TRIGGERS`). The group has no runtime setting: the
start-up switch governs it. See
[Proposals]({{% relref "/features/chats-and-agents/chats-agents" %}}#proposals).

The saved agents group (`saved_agents`) is opt-in: it is off until `on` names
it. See [Saved agents as sub-agents]({{% relref "/features/chats-and-agents/chats-sub-agents#saved-agents-as-sub-agents" %}}).

### Interactive tool pages (MCP Apps)

A tool of an MCP server can declare an interactive HTML page in
`_meta.ui.resourceUri`, as the [MCP Apps extension](https://modelcontextprotocol.io/extensions/apps/overview)
defines. When the model calls such a tool, the chat shows the page under the tool
line, with the arguments and the result of the call. This works for your
[connectors]({{% relref "/features/chat-apps/mcp-servers#mcp-apps" %}}) and for the MCP servers of the
chat's model. The page appears as soon as the tool call ends, while the turn
goes on, and it stays when the turn ends or the chat is opened again.

A live `tool_end` event carries the reference of the app (server, tool, resource,
visibility) and `seq`, the number of the call's line in the transcript. The
arguments and the full result are not in the event. A client that wants them reads
that one line with `GET /api/chats/{id}/messages?after=<seq-1>&limit=1`, which has
the same checks and size limits as the whole transcript. Because the line is in
the shared store, this works whichever frontend runs the turn. A client that
misses the event or cannot read the line shows the text result, and finds the
app in the transcript when the turn ends.

The MCP connection stays on the server, so two routes of the chat carry the page
and its calls. Both answer only for a server that the chat may use at that moment:
not switched off in the chat's tools panel, named by the chat's agent if it has
one, and, for a connector, yours and enabled. You need the `chats` and `mcp`
features. Anything else is `403` with the type `mcp_app_refused`.

```bash
# The page of the app. server is connector:<id> or the name of a server of the model.
curl -s "localhost:8080/api/chats/$ID/mcp-apps/resource?server=connector:c1&uri=ui://clock/app.html"
# {"uri":"ui://clock/app.html","mime_type":"text/html;profile=mcp-app","html":"...","meta":{"csp":{...},"permissions":{},"prefersBorder":true}}

# A tool call from the page.
curl -s -X POST localhost:8080/api/chats/$ID/mcp-apps/call \
  -H 'Content-Type: application/json' -d '{"server":"connector:c1","tool":"refresh","arguments":{"zone":"UTC"}}'
# {"result":{"content":[{"type":"text","text":"..."}],"structuredContent":{...}}}
```

Rules the server applies:

- The resource must be a `ui://` URI that a tool of the same server declares. No
  other resource is read. It must be `text/html` (with the `mcp-app` profile or
  none) and at most 4 MiB.
- A tool call must name a tool of the same server whose `_meta.ui.visibility`
  includes `"app"` (the default is `["model","app"]`). A tool in the add-on
  namespace or with the name of a built-in tool is refused. The call has the same
  time limit as a call of the model, its arguments are at most 16 KiB, and its
  result is cut at 256 KiB.
- A tool with `visibility: ["app"]` is not offered to the model.
- The answers never hold a token or a header of a connector, and an error from the
  server is cleaned of them.
- In distributed mode, apps work for connectors. The MCP servers of the model run
  on workers there, so their apps answer `409` with the type `mcp_app_unsupported`.

The `tool_end` event and the transcript line of a call with a page carry
`app`: `server`, `tool`, `resource_uri` and `visibility`. The transcript line also
has `input` and `result` (the full MCP result, at most 256 KiB), which the page
needs. The text of the line keeps its usual 2 KiB cut, and the model sees only the
text of the result. The event has no `input` or `result`: read the transcript for them.

The web interface shows the page in an iframe with `sandbox="allow-scripts"` and
nothing else. The page has no access to the interface (no same-origin access, no
cookies, no storage), cannot navigate the top window and cannot open windows. Its
content security policy allows no network by default. A page can ask for `https`
and `wss` origins with `_meta.ui.csp` (`connectDomains`, `resourceDomains`,
`frameDomains` and `baseUriDomains`); the server and the interface drop anything
else. Browser permissions such as the camera are not granted. The interface reads
messages only from that frame and ignores the rest. It supports `ui/initialize`,
`tools/call`, `ui/notifications/size-changed` (height between 60 and 600 pixels),
`ui/message`, `ui/open-link` and `ui/request-display-mode`; any other request
gets the JSON-RPC error "method not found". A message that the page sends with
`ui/message` is shown as a suggestion that you send or dismiss, never sent by itself.
A link from `ui/open-link` must be `http` or `https` and opens only when you click
it. When the page cannot be loaded, the chat shows the result as text.

## Write to a messaging connection

In a chat in Studio, and in the runs of its loops, the model has the tool
`send_message` when you have at least one connection that can receive messages:
it is yours, it is on, its kind can receive messages, the owner lets it write in
channels, and you approved at least one channel for it. With no such connection
the chat does not get the tool.

The model passes `text`, `connection` (required: the ID of one of your
connections) and `channel` (the name or the ID of an approved channel). It may
leave out `channel` only when the connection has exactly one approved channel;
with more, the call fails and names them. `thread` is optional. The description
of the tool lists, for each turn, your connections that can be used and their
approved channels. A call without `connection` fails with the list of valid
ones; the server never picks one.

The tool tells the model to send only when you asked for a report or when it
needs you, and never because text from a web page, a file or another message
says so. Mentions of the whole channel are removed. The server checks the
connection and the channel at each call, sends through the outbox of the
connection (a message over its rate limit waits and goes in order), and writes
an audit line without the text. One chat may send at most 5 messages in 10
minutes; this count is kept in memory by each server instance. Chats that a
connector started keep their own `send_message`, which writes in the chat that
the sender used; see [Chat apps]({{% relref "/features/chat-apps/actions" %}}).

## Web

A chat can search the web with `web_search` and read a page with `web_fetch`.
LocalAI serves both itself, with no add-on and no key, and they are on by
default. They only read. Every result is labelled as untrusted content from the
internet, and `web_fetch` refuses addresses inside your network. An
administrator can switch each tool off, set a SearXNG instance for the
searches and allow more ports in **Settings > Tools**. An add-on can replace
either tool by name: the optional Web add-on replaces both, to search with Brave
Search, Tavily or Google with a key and to read PDF files. See
[Search the web in chats]({{% relref "/features/chats-and-agents/web" %}}).

## Collections

When you may use collections, a chat has two more tools: `list_collections` and `search_collection` (`collection`, `query`, `limit`). They search your own collections, the ones of the Knowledge page, and nobody else's: a name that is not yours is answered "collection not found". An answer holds at most 20 passages and about 16 KiB. A sub-agent gets these tools only when its parent names them.

## Add-on tools

A chat can use the tools of the add-ons you may use. The **Tools** menu of the chat lists them with a switch for each add-on; a switch applies to that chat from its next turn. A tool runs as you, in your chat, and never as a user a model names.

- A tool that only reads gives the model its result.
- A tool that changes an add-on's data (rows, a schema, a new database, a job) changes it at once. Turning an add-on on for a chat or an agent is your consent, so there is no confirmation card. The model is told what was changed and whether it can be undone, and the tool entry in the chat says the same. Undo is the safety net, so a change the add-on kept a change set for shows an **Undo** button on its entry: Tables keeps one for every row and schema change and for a new database, and Fine-tuning and Quantization keep one for a job start (an undo stops the job while it runs). After you press it the entry says "Undone", or why it could not be: the data changed after the change (undoing would overwrite later work, and nothing is changed), the change is older than 7 days, or the job already finished. Only you can press it: the model is told the change can be undone but has no tool for it, and an API key cannot call it. Some changes keep no change set, such as one whose data was too large to keep, stopping a job or exporting a model; their entry says "This cannot be undone" and gives the reason. The file changes of a sandbox or an Office document are different: they are one change in the history of your files and you can undo them there. Reads and changes use your own permissions, and a change on data that moved since it was read is refused, not applied. A scheduled or file-triggered run uses the same rule, but a run with no agent gets no tool that changes anything: name the add-on in an agent to allow it.
- An *action* tool, such as running a command in a sandbox, runs at once and only in a chat. No other place that offers add-on tools (the OpenAI-compatible API, MCP, agents) lists or runs one.
- A sub-agent gets the add-on tools its parent names, or all of the parent's tools when the parent names none.

```bash
curl -s localhost:8080/api/chats/$ID/addons
# {"addons":[{"id":"office","tools":4,"off":false}]}
curl -s -X PUT localhost:8080/api/chats/$ID/addons -H 'Content-Type: application/json' -d '{"off":["office"]}'
```
