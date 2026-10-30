+++
disableToc = false
title = "Agents"
weight = 20
url = '/features/agents'
+++

LocalAI no longer has a separate agent platform. The agents, the agent jobs and the old chat page were replaced by the [chats](/features/chats/), which keep a conversation on the server and run it with the same loop for every purpose.

| You used to | Now |
|---|---|
| Create an agent on the Agents page | Create a [saved agent](/features/chats-agents/): instructions, a model, and exact lists of the MCP servers, add-ons, collections and skills it may use |
| Chat with an agent | Start a chat with the agent from its row on the Agents page, or send it a message in Telegram through a [connector](/features/chats-connectors/) |
| Run an agent by itself, or a cron task | Make a [trigger](/features/chats-agents/#triggers): a schedule, or a file arriving in a folder |
| Call a task from a script, with parameters and a webhook | The [agent tasks and jobs](/features/agent-jobs/) API: same paths and JSON as before; a job is a background chat |
| Read the output of a job | `GET /api/agent/jobs/{id}`, the chat the job or trigger made, or the [inbox](/features/chats-agents/#inbox) |
| Install skills from a hub or a git repository | Write [skills](/features/chats/) as folders with a `SKILL.md`, personal or shared |
| Keep chat history | The chats are kept on the server for every user |

The [collections](/features/collections/) (the knowledge base) stay. A saved agent lists the collections it may search.

What the server did with your old data at the first start, and what it could not carry over, is on [Moving from the old chat and agents](/features/chats-migration/).
