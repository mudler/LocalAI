+++
disableToc = false
title = "Home widgets and live pages"
description = "Put widgets and pages on Home, and update them on a schedule or when a file changes."
weight = 23
+++

Home can show widgets. A widget is a small tile with a list, a number or one of your pages. You can change what Home shows, and a trigger can keep a tile current.

{{% notice note %}}
This needs the chats and their files. The **Pages** feature, and the **Triggers** feature, are off for new users on a server with accounts. An administrator turns them on in the permissions of each user. Administrators always have them. The server option `LOCALAI_DISABLE_TRIGGERS` turns triggers off for everyone.
{{% /notice %}}

## Kinds of tile

| Kind | Shows | Data |
|---|---|---|
| `list` | A list of items, each with a label and an optional note | A JSON file in your home |
| `metric` | A number, a note and a small line chart | A JSON file in your home |
| `chats` | Your last chats | No file needed |
| `page` | One of your [pages]({{% relref "/features/pages" %}}), in compact mode | The page itself |

Data comes from files, not from live services. A page has no network access. A line chart is the only chart type of the data widgets.

## Add, remove and reorder

1. Open Home and choose **Edit layout**.
2. Add a widget, remove one, or change the order.
3. Save.

You can also ask the assistant: "Show my open tasks on Home". It writes the data file and adds the widget to `LocalAI/home.json`. Home is per user. There is no shared Home for a group. Home has at most 12 widgets, and at most 3 of them can be page tiles. You cannot change the size of a tile from Home. A page sets its own size in its `page.json`.

## Make a page a tile

A page can be a tile when its `page.json` has a `widget` block with a `size` of `small`, `medium` or `large`. Pick the page in **Edit layout**, or ask the assistant to put it on Home. See [A page on Home]({{% relref "/features/pages#a-page-on-home" %}}).

## Keep a tile current

A trigger starts a run of the assistant. The run rewrites a file, and the tile reads it.

- A **schedule** trigger runs daily, on weekdays, weekly, monthly, once, or from a cron expression. A schedule trigger runs at most once every 5 minutes, however it is made.
- A **file** trigger runs when a file arrives in a folder. A page can write that file. See [A page that starts a run]({{% relref "/features/pages#a-page-that-starts-a-run" %}}).

There is no trigger that a web request can start from outside.

Example:

1. Ask: "Every morning at 8, list my open tasks from my notes on Home."
2. The assistant writes `data/tasks.json`, adds a `list` widget for it, and proposes a trigger: daily at 08:00, writes `data/tasks.json`.
3. Review the trigger card and choose **Create**.
4. Each morning the trigger runs one chat turn for you and rewrites the file. The tile shows the new list.

Each run uses a model turn, so it costs tokens. See [Saved agents, triggers and inbox]({{% relref "/features/chats-agents" %}}).

## Limits to know

- A tile shows its last valid data and marks it as stale when a file breaks its schema.
- A trigger run is an ordinary chat turn. It can fail like one. Try your own setup with a short schedule first.
