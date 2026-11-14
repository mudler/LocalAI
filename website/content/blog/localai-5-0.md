---
title: "LocalAI 5.0: documents, files and a team, on your own hardware"
date: 2026-11-15
draft: true
author: "Ettore Di Giacinto"
category: "Release"
tags: ["release", "office", "files", "sandbox", "team", "pages", "add-ons", "ui"]
summary: "LocalAI 5.0 can make Word, Excel, PowerPoint and PDF files, keep your files, run Python on them and share folders with your team. It still runs on your own hardware."
extracss: ["blog.css"]
placeholders: ["release date", "release tag and release notes link", "PR, commit and contributor counts", "contributor thanks", "screenshots"]
---

<!-- TODO owner: this post is a draft (draft: true keeps Hugo from publishing it). Publish it with the 5.0 tag: set the final date, remove `draft: true` and `placeholders`, and resolve every TODO comment below. Nothing in the post claims a number we have not counted. -->

LocalAI 5.0 can do work for you, not only answer questions. From a chat you can make a Word, Excel or PowerPoint file, keep it in your own Files, run Python on it, and share the folder with your team. The server, the models and the files stay on your own hardware.

<!-- TODO owner: release date and tag are not decided. The link below assumes the tag is v5.0.0. Check it before publishing. -->
The [full notes](https://github.com/mudler/LocalAI/releases/tag/v5.0.0) list every change. This post covers what you can do with 5.0 that you could not do with 4.x.

<!-- TODO owner: headline numbers (days, merged PRs, commits, authors) are left out on purpose. Count them at the tag and add one sentence here if you want them. Also decide the wording of any "three years" or "since 2023" line; we did not write one. -->

## Hand it over

<figure>
<video src="/media/hand-it-over.mp4" poster="/media/hand-it-over.jpg" controls playsinline preload="none" aria-label="Hand it over: a llama director and a coworker in a dinosaur costume get a premiere ready by five o'clock"></video>
<figcaption>"Hand it over", the 5.0 launch film, 42 seconds with sound. Made with LocalAI: the video, the voices and the music.</figcaption>
</figure>

A coworker in a dinosaur costume has until five o'clock to get a premiere ready, and a llama in sunglasses is in the director's chair. That is the whole plot. We made the film with LocalAI, and the rest of this post is about the parts of 5.0 that the film jokes about.

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

On Linux or macOS:

```bash
curl -sSL https://localai.io/install.sh | sh
local-ai run qwen3-4b
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
