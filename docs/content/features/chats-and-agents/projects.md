+++
disableToc = false
title = "Projects"
description = "Group chats, files, notes and agents around one piece of work."
weight = 4
url = "/features/projects/"
keywords = ["projects", "project memory", "notes", "agents", "lens", "folder"]
devbuild = "feature"
+++

A project groups chats around one folder. Use it for one piece of work: a report, a customer, a trip. The folder holds the material and a set of notes that the chats of the project read. Open it from **Projects** in the sidebar.

## What a project holds

A project is a folder under `Projects/` in your own files, so you can open, edit and undo it like any other file. A project doesn't lock anything: a chat that isn't tied to a project can still read the project's files from your home folder. Keep that in mind before you put sensitive files in one.

It has:

- a `project.json` file with the name, the description and the defaults;
- a `files/` folder for the material of the project;
- a `memory/` folder with notes. The chats of the project read the notes at each turn. A model can suggest a note, but only you can save it. You can also edit the notes yourself.

## How you use it

1. Select **Projects** in the sidebar, then **New project**.
2. On Home, use the **Project** chip next to the model to start a chat in a project. You can also select **Start chat in this project** on the project page.
3. In the chat, the model reads the notes of the project. When it learns something worth keeping, it proposes a note. You confirm it before it's saved.
4. On the **Tools** tab of the project, set the defaults for new chats: skills, add-ons, MCP servers, connectors and collections.

A chat has one home project. It can also pull in up to eight other projects, for reading only.

## Agents and sharing

You can make a saved agent from a project. The agent reads the notes of the project. It can write a note only when you give it the **Remember things in the project** grant, and you still confirm each note. You can share a project folder with one person or with everyone, for reading or for reading and writing. People you share with pull the project into their chats for reading.

## More

The [Projects reference]({{% relref "/features/chats-and-agents/projects-reference" %}}) has the details:

- [In the UI]({{% relref "/features/chats-and-agents/projects-reference#in-the-ui" %}}): the pages, chips and settings.
- [Project memory]({{% relref "/features/chats-and-agents/projects-reference#project-memory" %}}) and [Tool defaults]({{% relref "/features/chats-and-agents/projects-reference#tool-defaults" %}}).
- [Sharing a project]({{% relref "/features/chats-and-agents/projects-reference#sharing-a-project" %}}).
- [The API]({{% relref "/features/chats-and-agents/projects-reference#the-api" %}}): the routes under `/api/chats/projects`.
