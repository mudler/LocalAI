+++
disableToc = false
title = "Build your first agent"
icon = "smart_toy"
weight = 130
aliases = ["/getting-started/first-agent/"]
description = "Make a saved agent with instructions and tool grants, chat with it, and let a schedule run it."
keywords = ["agent","first agent","triggers","tutorial"]
devbuild = "feature"
+++

A saved agent is a chat with fixed instructions and a fixed set of tools and files. You pick it in a chat, schedule it, or reach it from a chat app. It ships inside LocalAI. On a server with accounts, an administrator switches it on first. With authentication off, you are the administrator.

## Before you start: install a model that can call tools

An agent that uses tools needs a model that can call them. Use the model from the [quickstart]({{% relref "/start/quickstart#build-an-agent" %}}), or another one that you know supports tool calling, and check that it answers a plain chat first.

## Make the agent

1. Open **Agents** in the sidebar.
2. Choose **New agent**. A short wizard opens with four steps: **Purpose**, **Abilities**, **Where it works** and **Review**. Use the bar at the top to go back. The summary shows the agent as it will be created.
3. In **Purpose**, start from a template or write your own. Give it a name and the instructions it should always follow, for example: `Answer in French. Cite the file you used.` Only the name and the instructions are required. The model is the one you pick in each chat, unless you choose a fixed one.
4. Optional: in **Abilities**, pick a level (Look only, Assistant or Full) or leave it: the agent then starts with the default tools. Under **Files**, choose **No files**, **All my files** (the default), or **Only these folders** and pick a folder, for example `/Projects/Acme`, with **Can read** or **Can read and change**. You can add up to eight folders, each with its own access. Chats with the agent then see only those folders and their own chat folder, not the rest of your files. See [Folders of an agent]({{% relref "/features/chats-and-agents/chats-agents" %}}#folders-of-an-agent). **Fine-tune individual tools** holds every tool switch.
5. Optional: in **Where it works**, mark the chat apps and the tools and services you want. The step lists what your add-ons declare, and the add-ons you can still install. Marking an app does not connect it.
6. In **Review**, check the summary and choose **Create agent**. If you marked apps, the agent's page opens with a **Finish setup** card: one row for each app with what it needs and one button. A row is done when the server has the connection. **Hide** removes the card.

If you skip every optional step, the agent is the same as one made with only a name and instructions. To edit a saved agent, open it and choose **Edit**: that page keeps the full editor.

## Talk to it

On the Agents page, choose **Chat** on the agent's row and write a message. The chat uses the agent's model when it has one. If you delete something the agent uses, such as a collection, the chat still starts and tells the model what is missing.

## Let it work by itself

Open **Triggers** and make a schedule, for example `weekdays 09:00`, with the agent and a prompt. Each run is a normal chat in your history. The result shows up in **Notifications**. Like saved agents, triggers need their feature switched on on a server with accounts.

To talk to the agent from your phone, add a Telegram connection. The Telegram add-on must be installed first. See [Chat apps and connections]({{% relref "/features/chat-apps" %}}).

## If your agent will not run

- **No model.** An agent without a model needs one when you start the chat. A trigger or a connection needs an agent that names a model.
- **A tool is missing.** The chat lists what was unavailable at the start of the turn. Check that the MCP server is configured on the model, the add-on is enabled, and the collection or skill still exists.
- **An MCP server does not answer.** An MCP server that does not answer makes its tools time out. Open the model's `mcp:` configuration and test the server.
- **Folder missing.** The agent's folder was deleted, or it was imported without a folder that exists here. The agent does not start until you restore the folder from the Trash or choose another one in **Files**.
- **A trigger did not start.** Open it on the **Triggers** page: it shows why a run was skipped or failed.

## Next steps

- [Saved agents, triggers and notifications]({{% relref "/features/chats-and-agents/chats-agents" %}})
- [Chat apps and connections]({{% relref "/features/chat-apps" %}})
- [Moving from the old chat and agents]({{% relref "/features/chats-migration" %}})
