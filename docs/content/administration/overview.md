+++
title = "Admin overview"
weight = 20
aliases = ["/operations/overview/"]
description = "The Status page of the Admin section shows if anything is wrong with your LocalAI instance."
keywords = ["operate","admin","monitoring"]
+++

**Admin → Status** (`/app/operate`) is the front door to the Admin section. It answers one
question, is anything wrong, without you having to open four other pages.

The page opens with one sentence: **Everything is running.**, **2 things need
you.** or, when nothing needs a decision but a row has something to read,
**Running, with 1 thing to look at.** On a new installation, with no backend, no
model and nothing loaded, it says **Nothing is running yet.** and offers
**Install a backend** and **Browse models**.

Under the sentence are four rows. A row with a problem opens by itself and holds
the button that deals with it. A row with nothing to say stays one line. Click a
row to open or close it.

## Needs you

Only things that want a decision:

- a backend with an update available, with an **Update** button
- an operation that failed, with **Retry** and **Dismiss**
- a node that is not answering, with a link to the workers

**Update** starts the same update as the Backends page. **Retry** installs the
failed model or backend again, after moving the failure into the Activity
record, and **Dismiss** moves it there without retrying. When nothing needs you
the row says so in one line. There is no green panel: a status page that shouts
when everything is fine teaches you to stop reading it.

The attention count on the **Status** tab is the number of items in this row.

## Capacity

One line says how full GPU memory is (system memory on a host without a GPU),
and how much room the models disk has left. On a cluster it adds the memory of
the nodes that are answering, and the row lists each node's memory when opened.

The row opens by itself when memory is 90% full or more, or when the models disk
has less than a tenth free or less than 20 GB. It then lists the models loaded
on this machine, each with an **Unload** button. Unloading asks first, stops the
backend process of that model and frees its memory. The next request that uses
the model loads it again.

Under the rows, a bar shows the memory in use now. **LocalAI keeps no memory
history**, so the chart under the bar is drawn from readings the page took
itself, one with each summary poll (every 15 seconds), while the Status page is
open. It starts with the second reading, keeps at most 240 readings (about an
hour), and is labelled "Since you opened Status". The axis starts at zero, the
capacity is a labelled line, and a data table sits behind the chart. Leaving
the Status page empties it. Temperature and power are not shown because the resources
endpoint does not report them.

## Running now

Operations in progress, and the models loaded on this machine. On a single-node
install the row lists the heaviest five models: backend, resident memory, CPU
share and uptime, with **View logs** and **Stop model…** in the row menu. **Open
this machine** leads to the full list. With distributed mode on, models run on
workers rather than on the controller, so the row links to the Swarm page
instead.

## Recent failures

Requests and failed requests over the last 24 hours, and p95 latency, counted
server-side by `GET /api/traces/summary`:

```bash
curl http://localhost:8080/api/traces/summary?hours=24 \
  -H "Authorization: Bearer <admin-key>"
```

```json
{
  "total": 18402,
  "errors": 37,
  "p95_ms": 842,
  "window_hours": 24,
  "buckets": [{ "start": "2026-08-02T09:00:00Z", "count": 1520, "errors": 3 }]
}
```

`hours` defaults to 24 and is capped at 168. Only 5xx responses and transport
errors count as failures. A 4xx means the caller got the request wrong, so it says nothing about the installation. `p95_ms` is a nearest-rank percentile, not the
slowest request. The endpoint exists so a dashboard wanting three numbers does
not fetch the whole trace list to count it. An installation that has recorded
nothing says so rather than showing zeroes dressed as telemetry. The row opens
when at least one request failed, and links to Traces and Usage.

The page does not report models that failed to load, because LocalAI records no
load failures to read.

## This machine

On a single-node install, **Admin → This machine** (`/app/nodes`) shows the
host and everything loaded on it:

- **GPU memory** as one bar, with what is free and a note that the next model
  must fit in that, or a loaded model has to be stopped first. With several GPUs
  each one gets its own line. A host without a GPU says so instead of drawing
  an empty bar.
- **Models in host memory**: one bar split by running model, so you can see
  which model is holding memory. LocalAI reports the resident memory of each
  backend process, not the GPU memory of each model, so the split is of host
  RAM and no model is shown with a GPU size.
- **The machine**: VRAM, RAM, CPU and the models disk, each with a bar, a
  percentage and what is left. A reading the host does not report is shown as
  "No data", never as zero.
- **Running models**: search, sort by memory, CPU or uptime, open a model's
  logs, or stop it. Stopping asks for confirmation; the model loads again on
  its next request.

The page polls `GET /system` and `GET /api/resources` every five seconds. The
per-model readings come from the `process` block of
[`GET /system`]({{% relref "/reference/system-info" %}}); the host CPU and disk
readings come from the `cpu` and `disk` fields of `GET /api/resources`.

**Add a machine** reveals the command to start LocalAI in distributed mode, and
a link to the steps for adding a worker. Once distributed mode is on, the same
route becomes the Workers page and the **This machine** group gives way to the
**Cluster** group.

Models and backends no longer live under a nested Host page. Use **Models →
Installed** for model runtime and configuration actions, and **Admin →
Runtime → Backends** (Installed view) for installed backend actions. The overview links into
those pages rather than duplicating their inventories.

Old `/app/manage` bookmarks remain supported. They redirect with replace
semantics to the matching Installed Models or Installed Backends view while
preserving legacy search, filter, selection, variant, and development flags.

## The Administration sidebar

Click **Administration** in the sidebar and the sidebar changes: a **LocalAI**
link at the top leads back, and the pages of the Admin section are listed in
groups. There is no tab bar over the pages. The group that holds the page you
are on is always open, and a breadcrumb above the page title says where you are.

- **Overview**: this page.
- **This machine** (single install): **This machine**, and **Sharing** while
  P2P is off.
- **Cluster** (with distributed mode on, or with P2P on): **Workers**,
  **Placement** and **Sharing**. **Placement** is one page with two views,
  **Rules** and **Failover**. An old `/app/failover` address opens the Failover
  view on a cluster. On a single install Failover stays a page of its own.
- **Models and runtime**: **Backends**, **Activity**, **Logs**, **Updates** and,
  on a single install, **Failover**.
- **Traffic** (folded until one of its pages is open): **Overview**, **Usage**,
  **Models**, **GPU and host**, **Traces**, **Middleware** and **Prometheus**.
- **Access**: **Users** and **API keys** (with authentication on) and **Chats of
  all users** (with Chats on).
- **Settings**, and an **API reference** link that opens the API documentation.

Every page keeps its own URL, and a page such as a worker's detail keeps its
item highlighted. A few items carry a live value: the number of things that need
you on **Overview**, pending backend updates on **Updates**, running operations
on **Activity**, workers on **Workers**, running models on **This machine**,
sign-ups waiting for approval on **Users**, and failed requests on the folded
**Traffic** group. On a tablet the sidebar is an icon rail, and on a phone it
is the menu drawer.

Press **Ctrl K** (**Cmd K** on a Mac), or click **Search** at the top of the
sidebar, to open the command palette on any Administration page. It finds
pages and, once you type, settings, users (with authentication on) and
workers (on a cluster). Use the arrow keys and **Enter**; **Escape** closes it.

Those values are **orientation, not an alarm**. The sidebar only shows them
while you are in the Admin section, so anything urgent also appears in the Needs
you row and on the badge on the **Administration** entry of the main sidebar,
which is always visible.
