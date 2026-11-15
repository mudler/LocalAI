---
title: "LocalAI 5.0: an AI engine and platform you run anywhere"
date: 2026-11-15
draft: true
author: "Ettore Di Giacinto"
category: "Release"
tags: ["release", "multimodal", "projects", "desktop", "office", "files", "sandbox", "team", "pages", "add-ons", "ui"]
summary: "LocalAI is an open-source AI engine and platform. 5.0 adds Projects, a desktop app for macOS and Linux, and documents, files and team sharing on top of one OpenAI-compatible API for text, images, video, music, speech and search."
extracss: ["blog.css"]
placeholders: ["release date", "release tag and release notes link", "PR, commit and contributor counts", "contributor thanks", "screenshots"]
---

<!-- TODO owner: this post is a draft (draft: true keeps Hugo from publishing it). Publish it with the 5.0 tag: set the final date, remove `draft: true` and `placeholders`, and resolve every TODO comment below. Nothing in the post claims a number we have not counted. -->

LocalAI is an open-source AI engine and platform. It runs open models for text, images, video, music and speech behind one OpenAI-compatible API, on a laptop, a server, in a container or on Kubernetes. You do not need a GPU. 5.0 adds the work around the models: Projects, a desktop app for macOS and Linux, and from a chat you can make a Word, Excel or PowerPoint file, keep it in your own Files, run Python on it, and share the folder with your team. The server, the models and the files stay on your own hardware.

<!-- TODO owner: release date and tag are not decided. The link below assumes the tag is v5.0.0. Check it before publishing. -->
The [full notes](https://github.com/mudler/LocalAI/releases/tag/v5.0.0) list every change. This post covers what you can do with 5.0 that you could not do with 4.x.

<!-- TODO owner: headline numbers (days, merged PRs, commits, authors) are left out on purpose. Count them at the tag and add one sentence here if you want them. Also decide the wording of any "three years" or "since 2023" line; we did not write one. -->

## Hand it over

<figure>
<video src="/media/hand-it-over.mp4" poster="/media/hand-it-over.jpg" controls playsinline preload="none" aria-label="Hand it over: a llama director and a coworker in a dinosaur costume get a premiere ready by five o'clock"></video>
<figcaption>"Hand it over", the 5.0 launch film, 42 seconds with sound. Made with LocalAI: the video, the voices and the music.</figcaption>
</figure>

A coworker in a dinosaur costume has until five o'clock to get a premiere ready, and a llama in sunglasses is in the director's chair. That is the whole plot. We made the film with LocalAI, and the rest of this post is about the parts of 5.0 that the film jokes about.

## One API for every kind of model

LocalAI is not only a chat. The same server serves many kinds of open model, each with its own endpoint:

- **Text and vision**: chat, completions, tool calls, image understanding and object detection. Besides the OpenAI API, LocalAI answers in the Anthropic format (`/v1/messages`).
- **Images and video**: generate images from a prompt, and video with the `/video` endpoint.
- **Animated video**: a video model is not the only way to get a video. With the Sandbox add-on and its media profile (ffmpeg, sox and ImageMagick), the assistant can build video itself with the built-in `assemble-media` skill: a title card with a fade, a slideshow with a slow zoom and crossfade, a chart that draws itself, text that fades in. The result is an MP4 in your Files. The same skill joins, trims, mixes and subtitles clips that a video model made. The Sandbox has no network, and an administrator installs the add-on.
- **Music and sound**: make music and sound effects from a text prompt with `/v1/sound-generation`.
- **Speech**: text to speech, transcription, voice activity detection and speaker recognition. The Realtime API takes voice in and gives voice back. Text to speech and sound generation also accept the ElevenLabs format.
- **Search**: embeddings and a reranker, for retrieval over your own data.

Each model kind needs a backend that serves it. LocalAI downloads the backend the first time a model needs it, and the library has more than 80 of them. There is a CPU build, so no GPU is required. NVIDIA, AMD, Intel and Apple GPUs make it faster. The [compatibility table](/docs/models/compatibility-table/) lists which backend runs which model family, and [What models can do](/docs/models/capabilities/) has a page for each kind. For several machines, [distributed mode](/docs/administration/distributed-mode/) spreads models over a cluster.

## Projects

A project groups chats around one folder in your own files. The folder holds the material of the project, and a `memory/` folder of notes that the chats of the project read. The model can propose a note, and you confirm it before it is saved. A project can set defaults for new chats (skills, add-ons, MCP servers, connectors and collections), you can make an agent from a project, and you can share the project folder with a teammate as read only or read and write. **Projects** is in the sidebar, and the **Project** chip on Home starts a chat in one. A project is a lens for chats, not a security boundary: a chat can still reach the same files through your home folder. Read the [Projects docs](/docs/features/projects/).

## The desktop app

LocalAI now has a desktop app for macOS and Linux. It installs, updates, starts and stops the LocalAI server on your computer, and shows the web interface in its own window with a status bar for the server. On the first start you choose to run LocalAI on this computer or to connect to a server that runs somewhere else. You can keep several servers and pick a default. It checks for a new server version once a day and never installs one without asking.

<!-- TODO owner: check at the tag that the release has these files: LocalAI.dmg, LocalAI-Launcher-x86_64.AppImage and LocalAI-Launcher-arm64.AppImage, each with a .sha256 file, and that the DMG passes the Gatekeeper check of the release guide. The links below use releases/latest/download. -->
Download [`LocalAI.dmg`](https://github.com/mudler/LocalAI/releases/latest/download/LocalAI.dmg) for a Mac with Apple silicon, or the [x86-64 AppImage](https://github.com/mudler/LocalAI/releases/latest/download/LocalAI-Launcher-x86_64.AppImage) for Linux. The ARM64 AppImage is `LocalAI-Launcher-arm64.AppImage` on the [release page](https://github.com/mudler/LocalAI/releases/latest). The [desktop app guide](/docs/start/install/desktop/) has the first start, update channels and troubleshooting. It reads the same `~/.localai` folder as the older launcher, so your models stay.

## Make documents from a chat

Ask for a one-page summary of the notes in a folder, and the chat reads the notes and saves a Word file. The Simple Office add-on has 11 tools: read, edit and create for Word, Excel and PowerPoint, and read and create for PDF. Files are saved in your Files. If you create a file and the name is taken, the new file is saved next to the old one and the old one is not changed ([#13029](https://github.com/mudler/LocalAI/pull/13029)).

What it does not do: it does not edit PDF files, its PDF writer takes Latin-1 characters only, and the layouts are basic. For charts or exact layout, use the Sandbox below.

## Keep your files in LocalAI

There is now a **Files** page. Upload files and folders, drag rows between folders, preview a document and extract an archive. ZIP and TAR (also with gzip, bzip2, xz, zstd, brotli, lz4 or snappy) work out of the box. 7z and RAR stay off until an administrator turns them on. You can search by name and by the text inside your documents. Deleting moves a file to the Trash, and the History tab undoes a change.

A chat uses the same files. Attach a file to a message, or ask the model to read and write files in your folders. What the model changes goes into the same history as your own changes, so you can undo it.

## Run Python on your files

The Sandbox add-on gives a chat a `bash` tool that runs in an isolated runtime with your files mounted. Ask for "a bar chart of the numbers in sales.csv as a PNG", and the model reads the file, runs a Python script and saves the image in Files. What a command changed is saved as one change that you can undo.

The Sandbox has no network by default, so `pip install` fails until an administrator allows package registries or public sites. A chat can ask for a wider setting, but never for more than the administrator allows. The Sandbox add-on is Linux only.

## Share a folder with your team

With user accounts on, there are admin and user roles. Each person has their own files, chats and saved agents, and nothing is shared until its owner shares it. You can share a folder, a saved agent, a collection or a connection with one person or with everyone. An administrator can also make shared folders and choose who uses them ([#13130](https://github.com/mudler/LocalAI/pull/13130)).

Sharing is never a tool. A model, an agent or an add-on cannot share a folder or take access back. Only the person, in their own browser, can.

## Pages and Home

Ask the assistant to "make me a task board", and it writes a small HTML page that keeps its data in your files. The page runs in a sandboxed frame with no network access. Pin it to the sidebar, or put it on Home as a tile. A Home widget can show a list from a file, and a trigger you confirm can rewrite that file every morning.

For ordinary users on a server with accounts, Pages are off until an administrator switches them on. The same is true for saved agents, triggers and connectors. Each scheduled run is a normal chat turn, so it uses model tokens.

## Add-ons

An add-on is a separate program that gives LocalAI new tools. An administrator installs it from the library, and its tools show up in the chat's Tools menu, where you can switch them off for a chat. The library has Tables, for typed databases that a chat can read and change, and Telegram, Slack and Discord add-ons that let people talk to a saved agent from those apps. There are more, and you can build your own.

One thing to know before you install: an add-on is trusted code. It runs as the operating system user that runs LocalAI. Only the Sandbox isolates what it runs. A bot answers nobody until you add allowed senders.

## Chats that live on the server

Chats are now kept by the server. You can keep typing while the model works, the model can hand work to sub-agents, and a long chat is compacted for you ([#12918](https://github.com/mudler/LocalAI/pull/12918)). The OpenAI-compatible `/v1/chat/completions` endpoint is unchanged.

## A new look, and a first run that helps

5.0 has a new theme, logo and mascot ([#13187](https://github.com/mudler/LocalAI/pull/13187)). On a fresh install, Home recommends models for the hardware it detects ([#13199](https://github.com/mudler/LocalAI/pull/13199)).

<!-- TODO owner: screenshots. Add Home first run, the Files page with the Trash tab, the Office tool card in a chat, a Sandbox chart in Files, the sharing dialog, a page on Home and the add-ons library. Put the images under website/static/media/ and reference them with <figure>. -->

## Try it

The easiest start is the desktop app above. To install a server, on Linux or macOS:

```bash
curl -sSL https://localai.io/install.sh | sh
local-ai
# open http://localhost:8080 and install a model from there
```

Or with a container:

```bash
docker run -p 8080:8080 --name local-ai -ti localai/localai:latest
```

Open `http://localhost:8080`. For your first document, install the Simple Office add-on from **Add-ons** and ask a chat for a Word file. With authentication off you are the administrator and can do all of it. You do not need a GPU, but a small model on a CPU is slow to load and slow to answer.

## Known limits

- A release build reads the add-on library that was built and signed with that release. A source build reads the development channel, which can change.
- The Sandbox needs Linux. No add-on is built for Windows yet.
- The add-on library needs a network connection. Without one, use a private library or an add-on zip file.

<!-- TODO owner: contributors. Thank the people who landed work for 5.0 by name. Compute the list at the tag (prior-merged-count method of the release skill), no bots. -->

If something does not work for you, open an issue or ask in the [Discord](https://discord.gg/uJAeKSAGDy). Have fun!
