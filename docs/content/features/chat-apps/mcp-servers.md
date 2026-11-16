+++
url = "/features/chats-connectors/mcp-servers/"
disableToc = false
title = "MCP servers"
description = "Add MCP servers to give your chats and agents more tools, and choose which addresses they may reach."
weight = 20
devbuild = "feature"
+++

Add an MCP server to give your chats and agents more tools. You add a server by its address, or as a local command if you are an administrator, and chats use its tools like any other. A local command is refused in a cluster.

## MCP servers

An MCP connector is an MCP server that you define. It is stored on the server,
as one of your records, so every browser and session of yours sees the same list.
(The old chat page kept such servers in the browser and reached them through a
proxy; see [Import from this browser](#import-from-this-browser).)

A connector is yours alone. Another user never sees, edits, tests or uses it.
You need the `mcp` feature, and `LOCALAI_DISABLE_MCP` turns it off for the
whole instance. Both are checked each time a chat uses a connector, not only
when you save it.

### Add one

On the **MCP servers** tab of **Connections** (`/app/mcp-connectors`) choose **New connector**.
A side sheet opens:

1. Administrators choose the kind first: **Remote server** (an address over
   http or https) or **Local command** (see the table below). Other users add
   remote servers only.
2. Give it a name (unique among your connectors) and an address. The sheet
   checks the address as you type: it must start with `http://` or `https://`
   and must not hold a user name or password.
3. Under **Sign-in**, add a bearer token or other headers if the server asks for
   a key. Secret fields are masked; the eye button shows what you typed. They
   are stored encrypted on the server and never shown again.
4. **Save and test** saves the connector switched off, then connects with what
   was saved and lists the tools, each with the description its server gives.
   If the test works, **Turn on** switches it on; **Keep it off** closes the
   sheet. If it fails, the sheet gives the reason (a refused sign-in says to
   check the token or the headers) and offers **Test again**.

Escape, Cancel and the close button ask before they drop typed changes.

```
curl -X POST .../api/chats/mcp-connectors -H 'Content-Type: application/json' -d '{"name": "Docs", "transport": "http", "url": "https://mcp.example/mcp", "token": "...", "headers": {"X-Team": "blue"}, "enabled": false}'
curl -X POST .../api/chats/mcp-connectors/<id>/test
# {"ok": true, "tools": ["search", "fetch"], "tool_details": [{"name": "search", "description": "Search the docs."}, {"name": "fetch"}]}
curl -X POST .../api/chats/mcp-connectors/<id>/enable
```


With authentication on, add `-H "Authorization: Bearer $API_KEY"` to each request.

### The list and the details

Each connector is a row with its state, where it points (the host only, never
the path or the query of the address), its tools, what is saved for it and when
it was last tested. The state is one of:

| State | Meaning |
|---|---|
| **Connected** | The last test connected. |
| **Needs sign-in** | The last test was refused for its credential (for example a `401`). **Replace token** opens the sheet on the token field. |
| **Can't connect** | The last test failed; the row gives the reason. |
| **Not tested** | It is on, but it was not tested in this browser since it last changed. |
| **Off** | It is switched off. Chats and agents do not use it. |

The last test of each connector is kept in this browser only (in
`localStorage`, without the address or any secret), so the page shows the last
known state with its time after a reload. A change of the address or of what is
saved makes it stale. **Test all** in the toolbar tests every connector that is
on.

The switch on a row applies at once. With four or more connectors, a search
(by name or address) and a filter (**All**, **On**, **Needs attention**,
**Off**) appear above the list. A click on a row opens its details beside the
list (over the page on a narrow screen): the switch, the last test with the
reason when it failed, the tools with their descriptions (with a search when
there are many), how it connects, and what is saved. **Remove** asks first; it
deletes the connector with its saved secrets.

`GET`, `PUT` and `DELETE /api/chats/mcp-connectors/{id}` read, replace and
delete one, and `POST .../enable` and `.../disable` switch it. You can have 20.
A test of a server that cannot be used is a `200` with `ok: false` and the
reason.

| Transport | What it is |
|---|---|
| `http` | A remote server. Streamable HTTP is tried first and the legacy SSE transport is the fall back. The address must be `http` or `https`, with no user name or password in it. |
| `stdio` | A command run on the host of the frontend, with `command`, `args` and `env`. Administrators only, and refused in distributed mode. |

OAuth sign-in for remote servers is not supported: use a token or a header.
A tool of a connector can have an interactive page; see [MCP Apps](#mcp-apps).

### Secrets

The token, the headers and the environment of a command are sealed in the
database or the JSON files, with the same key and the same method as the Telegram
tokens, and tied to the connector and its owner. They are write-only: the API
returns only `has_token`, `has_headers` and `has_env`. A `PUT` that leaves them
out keeps the stored values; an empty value clears them. They are never logged,
never put into a tool result or the model's context, and an error message never
repeats them or the query of the address.

### Which addresses a connector may reach

A connector makes the server connect to an address you choose. To stop it being
used to probe the internal network, a connector may reach only public hosts:

- The check is made **when the connection is opened**, on every address the name
  resolves to, and on every redirect. A name whose answer changes between the
  check and the connection (DNS rebinding) is refused too.
- Loopback, link-local, private (RFC 1918 and unique local), shared address space,
  cloud metadata and unspecified addresses are refused, for IPv4 and IPv6.
- An address that is plainly internal (an IP literal or `localhost`) is refused
  when you save or switch on a connector. A disabled connector may be saved with
  one, so that an import or a conversion can make it, but it cannot be switched on.

An administrator can allow private addresses with the runtime setting
`mcp_connectors_allow_private` (Settings, Access and security; default off; no
restart needed). Turn it on only when your users are trusted: with it, a connector
can reach services on the server's own network. Cloud metadata addresses stay
refused even then, and a request does not use a proxy from the environment. Turning it off closes the open
sessions that were made to such addresses: a tool handed out before is refused,
and the next turn connects again under the new rule. Likewise, a user who stops
being an administrator loses their running local commands at their next use.

A local command (`stdio`) runs without a shell, with a minimal environment
(`PATH`, `HOME`, `LANG`, `LC_ALL`, `TMPDIR`, `TZ`) plus the variables you sealed, and with a
time limit to start and to answer. It runs on the frontend, as the LocalAI process.
When a local command fails to start, to answer or to run a tool, LocalAI keeps
the last 4 KB of what it wrote to its error stream. The connection test shows
it in the connector details as **Last output from the command**, apart from the
one-line reason, and the server log has it too. The sealed values are replaced
with `***` first. The API returns it as `output` in the answer of
`POST /api/chats/mcp-connectors/{id}/test`.

### How chats use them

- **A chat with no agent** gets the tools of all your enabled connectors, next to
  the MCP servers its model has.
- **A saved agent** names a connector as `connector:<id>` in its `mcp_servers` list;
  a name without the prefix is a server of the model. The agent form lists both.
  The agent holds the ID, so renaming a connector does not break it.
- **A connector that cannot be used** (switched off, deleted, not yours, the
  address refused or the server down) gives the model a note, "unavailable this
  turn: MCP connector Docs (cannot be reached)", and never fails the turn.
- **Triggers and sub-agents** follow the rules of agents: a trigger with an agent
  gets what the agent names; a sub-agent gets the tools its parent names.
- **Tool names.** A tool cannot take the name of a built-in tool or the add-on
  namespace; it is dropped. When two connectors (or a connector and the model)
  offer a tool with the same name, each gets the connector's name in front:
  `docs__search`, `mail__search` (the connector's name in lower case, two underscores, the tool). The names do not change while the connectors stay as they are.
- **Limits.** A call has the time limit of the model's MCP tools (360 seconds), so a research or document tool that takes minutes finishes, and its output is cut at 64 KiB like the output of the chat's own tools.
- **Per chat switches.** The tools panel of a chat lists the connectors and the
  model's MCP servers with a switch each. See [Chats]({{% relref "/features/chats-and-agents/chats-tools#mcp-tools" %}}).

### MCP Apps

A connector can offer MCP Apps: tools that declare an interactive HTML page in
`_meta.ui.resourceUri`. Chats show the page under the tool line as soon as the
call ends, while the turn goes on. The server
holds the MCP connection, so the page works for a connector like any other tool:
the page is read from the connector, and its tool calls go to the connector,
with the connector's own credential. The credential never reaches the browser.

- Only the owner's chat, with the connector enabled and not switched off in the
  tools panel of that chat, can read a page or call a tool for it. A saved agent
  must name the connector.
- Only a `ui://` resource that a tool of the connector declares is read.
- A tool whose `_meta.ui.visibility` does not include `"app"` cannot be called
  by a page. A tool whose visibility does not include `"model"` is not offered
  to the model.

See [Chats]({{% relref "/features/chats-and-agents/chats-tools#interactive-tool-pages-mcp-apps" %}}) for the routes and
the sandbox.

### Import from this browser

If the old chat page kept MCP servers in this browser (`localStorage`), the
MCP connectors page offers **Import N servers from this browser**. It shows each name and
address, never a header value, and makes a switched-off connector for each one.
A token or header leaves the browser only for a server the API accepted; the
ones that failed stay in the browser with the reason.
