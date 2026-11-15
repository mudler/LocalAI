+++
disableToc = true
title = "Google Drive"
description = "Let agents read the Drive folders you choose, and write text files if you allow it."
weight = 30
keywords = ["google drive", "gdrive", "files", "add-on"]
+++

The Google Drive add-on lets your saved agents read the files in the Drive folders you choose. In the current development builds, it is in the development channel of the add-on library, so it can change.

## At a glance

| Topic | Detail |
|---|---|
| What it adds | Search, list a folder, read the details of a file and read its text. If you allow it, create a text file, replace the text of a file, rename or move a file, and move a file to the trash. It never shares a file. |
| Who installs it | An administrator, from **Add-ons**, **Find add-ons**, with **Install and turn on**. With authentication off, you are the administrator. An administrator also saves a Google OAuth client once. LocalAI ships no client. |
| Who turns it on | A person needs the `addons` feature. It is off by default for new users on a server with accounts. The person switches the add-on on for a chat in the **Tools** menu. A saved agent uses it only when you name it in the agent. Each person connects their own Google account. |
| Guide | [Google Drive in Chat apps and connections]({{% relref "/features/chat-apps#google-drive" %}}) |
