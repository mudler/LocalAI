+++
title = "Operate overview"
weight = 1
+++

`/app/operate` is the front door to the Operate console. It answers one
question — is anything wrong — without you having to open four other pages.

## Needs attention

The block the page exists for. It lists only things that want a decision:

- a backend with an update available
- an operation that failed
- a node reporting unhealthy

**When nothing needs attention it says so in one line and renders nothing
else.** There is no green panel: a status page that shouts when everything is
fine teaches you to stop reading it.

## Headline totals

Requests, failed requests and p95 latency over the last 24 hours, each with a
sparkline of the trend. These come from `GET /api/traces/summary`, which counts
the trace buffer server-side:

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
errors count as failures — a 4xx is the caller getting it wrong, not the
installation being unhealthy. `p95_ms` is a nearest-rank percentile, not the
slowest request.

The endpoint exists so a dashboard wanting three numbers does not fetch the
whole trace list to count it. An installation that has served nothing yet says
so rather than showing three zeroes dressed as telemetry.

## Host capacity

The overview also shows the host's current RAM or GPU capacity, utilization,
and model storage. Loading, unavailable, and empty states are explicit. This
uses the same 15-second Operate summary poll as the rail and attention data, so
opening the overview does not start a second resource poller.

Models and backends no longer live under a nested Host page. Use **Models →
Installed** for model runtime and configuration actions, and **Operate →
Backends → Installed** for installed backend actions. The overview links into
the canonical Operate sections rather than duplicating those inventories.

Old `/app/manage` bookmarks remain supported. They redirect with replace
semantics to the matching Installed Models or Installed Backends view while
preserving legacy search, filter, selection, variant, and development flags.

## The rail

The Operate rail groups its destinations under four headings —
Runtime, Cluster, Observability and Administration — and shows a live value
beside several of them: pending backend updates, running operations, healthy
node count, request volume and error count. Host capacity lives on the overview
instead of appearing as a separate destination.

Those values are **orientation, not an alarm**. The rail only exists on Operate
routes and can be collapsed, so anything urgent also appears in Needs attention
and on the operations badge attached to the sidebar entry, which is always
visible.
