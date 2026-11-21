+++
disableToc = true
title = "Ollama search"
description = "Add the Ollama model library as a source of the model search."
weight = 71
keywords = ["ollama", "model search", "library", "add-on"]
+++

The Ollama search add-on (ID `ollama-search`) adds the public model library of [Ollama](https://ollama.com/library) as a source of the model search on the **Models** page and in the Assistant.

LocalAI doesn't install it for you. Install it when you want to find Ollama models from LocalAI.

## At a glance

| Topic | Detail |
|---|---|
| What it adds | One source of the model search: the models of the Ollama library, with their downloads, last update, capabilities and sizes. |
| What stays in LocalAI | The importer and the `ollama://` scheme. A result installs through the same route as any other, `POST /models/import-uri`. |

## Use it

1. Install the add-on.
2. Open the **Models** page, **Explore** tab. A chip named **Ollama** appears next to **Hugging Face** with no other step. To turn the source off, press **Sources** and switch **Ollama** off.
3. Search by name, or leave the search box empty and pick the **Latest** sort to see the newest models of the library.
4. The source has three filters: **Tools**, **Vision** and **Thinking**. Each one keeps the models that have that capability.
5. To install a model, use the `uri` of the result, `ollama://<name>`, with `POST /models/import-uri`. To pick a size, enter `ollama://<name>:<tag>` yourself, for example `ollama://qwen3:8b`.

**Tag choice is not in the Models page yet.** Opening an Ollama result does not list its tags, and the source does not show the variants hint. The add-on can already read the tags page of a model: its `search` tool takes a `variants` argument that returns one entry per tag with its size and context window. A later change of the host has to call it before the page can show the tags.

The **Downloads** sort gives the most pulled models first. The **Updated** and **Latest** sorts give the most recently updated first. The other sorts of the host are ignored.

Your choice of sources is saved with your account.

## What the add-on does

Ollama has no public search API, so the add-on reads two pages of the site: the search page, `https://ollama.com/search`, and, when the `variants` argument is set, the tags page of one model, `https://ollama.com/library/<name>/tags`. It doesn't call the `/api` or `/v1` paths, which the `robots.txt` of the site disallows. It sends the user agent `LocalAI model search (+https://github.com/mudler/LocalAI)`, waits at most 20 seconds for a page, and reads at most 4 MiB of it. Its calls go out like the calls of any add-on, under the network rules of your server. It keeps no data.

- The add-on asks the site for local models only, so models that run only in the cloud are not listed.
- MLX and other non-GGUF builds are left out of the tags the add-on lists.
- A search page shows the first results only. The add-on doesn't load the rest, so a search gives about a dozen results, fewer than the limit you set when the site shows fewer.
- Because the add-on reads HTML, a change of the site layout can break it. When the layout is not the one it knows, the source shows a failed status that names the page, never an empty list. Update the add-on to fix it.

The host checks every row the add-on returns. A row whose address does not start with a scheme the importer accepts, or that has a field over its size limit, is dropped and counted: the Models page shows "N results skipped".

For the contract of a model search source, see [Model search sources]({{% relref "/addons/contract-reference/host-operations#model-search-sources" %}}).
