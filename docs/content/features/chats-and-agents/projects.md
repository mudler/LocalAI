+++
disableToc = false
title = "Projects"
description = "Group chats, files, notes and agents around one piece of work."
weight = 4
url = "/features/projects/"
keywords = ["projects", "project memory", "notes", "agents", "lens", "folder"]
devbuild = "feature"
+++

A project groups chats around one folder. Use it for one piece of work: a report, a customer, a trip. The folder holds the material of the project and a set of notes that the chats of the project read. Projects is a page in the sidebar of the web interface.

## What a project holds

A project is a folder under `Projects/` in your own files, so you can open, edit and undo it like any other file. It has:

- a `project.json` file with the name, the description and the defaults;
- a `files/` folder for the material of the project;
- a `memory/` folder with notes. The chats of the project read the notes at each turn. A model changes them only by a proposal that you confirm. You can also edit them yourself.

## How you use it

1. Select **Projects** in the sidebar, then **New project**.
2. On Home, use the **Project** chip next to the model to start a chat in a project. You can also select **Start chat in this project** on the project page.
3. In the chat, the model reads the notes of the project. When it learns something worth keeping, it proposes a note. You confirm it before it is saved.
4. On the **Tools** tab of the project, set the defaults for new chats: skills, add-ons, MCP servers, connectors and collections.

A chat has one home project. It can also pull in other projects, up to eight, for reading only.

## Agents and sharing

You can make a saved agent from a project. The agent reads the notes of the project. It can write a note only when you give it the **Remember things in the project** grant, and you still confirm each note. You can share a project folder with one person or with everyone, for reading or for reading and writing. People you share with pull the project into their chats for reading.

{{% callout style="note" %}}
A project is a lens for chats, not a security boundary. A chat that is not restricted to a project can still reach the files of the project through your home folder.
{{% /callout %}}

## More

The reference has the details:

- [In the UI]({{% relref "/features/chat-apps#in-the-ui" %}}): the pages, chips and settings.
- [Project memory]({{% relref "/features/chat-apps#project-memory" %}}) and [Tool defaults]({{% relref "/features/chat-apps#tool-defaults" %}}).
- [Sharing a project]({{% relref "/features/chat-apps#sharing-a-project" %}}).
- [The API]({{% relref "/features/chat-apps#the-api" %}}): the routes under `/api/chats/projects`.
