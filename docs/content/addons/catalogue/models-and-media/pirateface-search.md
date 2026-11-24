+++
disableToc = true
title = "Pirate Face search"
description = "Add Pirate Face as a source of the model search."
weight = 70
keywords = ["pirate face", "model search", "torrent", "magnet", "add-on"]
+++

The Pirate Face search add-on (ID `pirateface-search`) adds [Pirate Face](https://pirateface.co) as a source of the model search on the **Models** page and in the Assistant.

If you searched Pirate Face before, install this add-on to keep doing it. LocalAI doesn't install it for you. Until you do, the **Models** page shows a short hint for a Pirate Face choice that has no add-on.

## At a glance

| Topic | Detail |
|---|---|
| What it adds | One source of the model search: open-weight models that Pirate Face lists, with their size and number of seeders. |
| What stays in LocalAI | The BitTorrent downloader, the `pirateface://` and `magnet:` schemes and the importer. A result installs through the same route as before, `POST /models/import-uri`. |

## Use it

1. Install the add-on.
2. Open the **Models** page, **Explore** tab. A chip with a **p2p** tag appears next to **Hugging Face** with no other step. To turn the source off, press **Sources** and switch **Pirate Face** off.
3. Search by name. The results come from the plain text search page of Pirate Face. The source has no list of new models, so with no search text it shows a note.
4. The source has one filter, **Minimum seeders**. It hides the models that fewer peers share. The slider appears next to the other filters when the Pirate Face chip is picked.

Your choice of sources is saved with your account. If you turned Pirate Face off and then removed the add-on, it stays off when you install the add-on again.

## What the add-on does

The add-on reads `https://pirateface.co/s/<query>`, the plain text search page. It doesn't call the `/api` paths of the site. Its calls go out like the calls of any add-on, under the network rules of your server. It keeps no data.

The host checks every row the add-on returns. A row whose address does not start with a scheme the importer accepts, or that has a field over its size limit, is dropped and counted: the Models page shows "N results skipped". Gated models have no magnet link on the site, so they don't appear.

For the contract of a model search source, see [Model search sources]({{% relref "/addons/contract-reference/host-operations#model-search-sources" %}}).
