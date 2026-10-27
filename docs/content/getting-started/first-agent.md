+++
disableToc = false
title = "Build your first agent"
weight = 7
icon = "smart_toy"
+++

A saved agent is a set of instructions and exact lists of what a chat may use. It ships inside LocalAI and is on by default. If LocalAI runs and a model is installed, you have everything you need.

## Before you start: install a tool-calling model

An agent that uses tools needs a model that can call them. Open **Models**, install a model that lists tool calling, and check that it answers a plain chat first.

## Make the agent

1. Open **Chats**, then **Agents**.
2. Choose **New agent**. Give it a name and the instructions it should always follow, for example: `Answer in French. Cite the file you used.`
3. Leave the lists empty for an agent that only talks. Add an MCP server of the model, an add-on, a collection or a skill to give it more. An empty list grants nothing: an agent never has more than you put in it.
4. Save it.

## Talk to it

Back on **Chats**, pick the agent in the list of agents and write a message. The chat uses the agent's model when it has one. If you remove something the agent lists, for example by deleting a collection, the chat still starts without it and tells the model what is missing.

## Let it work by itself

Open **Triggers** and make a schedule, for example `weekdays 09:00`, with the agent and a prompt. Each run is an ordinary chat of yours, and the result arrives in your **Inbox**. Add a [Telegram connector](/features/chats-connectors/) to talk to the agent from your phone.

## If your agent will not run

- **No model.** An agent without a model needs one when you start the chat. A trigger or a connector needs an agent that names a model.
- **A tool is missing.** The chat lists what was unavailable at the start of the turn. Check that the MCP server is configured on the model, the add-on is enabled, and the collection or skill still exists.
- **A server of the model is unreachable.** An MCP server that does not answer makes its tools time out. Open the model's `mcp:` configuration and test the server.
- **A trigger did not start.** Open it on the **Triggers** page: it shows why a run was skipped or failed.

## Next steps

- [Saved agents, triggers and inbox](/features/chats-agents/)
- [Telegram connector](/features/chats-connectors/)
- [Moving from the old chat and agents](/features/chats-migration/)
