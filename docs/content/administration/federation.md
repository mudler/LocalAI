+++
title = "Federation"
toc = true
description = "Serve one model name from this server and other LocalAI instances, with least busy or round robin routing and failover"
tags = ["P2P", "Administration", "Remotes", "Routing"]
categories = ["Features"]
weight = 146
+++

Federation serves one model name from several LocalAI instances: this server
and other LocalAI servers that it uses as [remotes]({{% relref "/administration/remotes" %}}),
usually [over P2P]({{% relref "/administration/p2p" %}}). Clients call the
name like any model. LocalAI sends each request to one of the instances, and
when an instance fails, the request moves to the next one.

A federation **group** has:

- a **model name**, the name that clients call;
- **members**: at least two models, each served by one instance. A member is a
  model of this server or an entry of a remote;
- a **routing** policy: **Least busy** or **Round robin**.

## Before you start

1. On each other LocalAI, make an invite of kind **Remote**: **Swarm**, **P2P**,
   **Share this instance** (the mode must be **Connect and share**). A server
   that has an address can also be added with its address and key.
2. On this server, add each one as a remote: **Models**, **Remotes**, then
   paste the invite link. Pick the model that the group will serve, so the
   remote has an entry for it. See
   [Connect a remote with an invite link]({{% relref "/administration/p2p#connect-a-remote-with-an-invite-link" %}}).

For a remote over P2P, P2P must be on here: mode **Connect only** is enough.

## Make a group

Open **Swarm**, **P2P**, **Federation** (on a single server with P2P off, the
page is under **This machine**, **P2P**), then **New group**:

1. Enter the model name of the group. It must not be the name of a model that
   exists. The form suggests the model id of the first member you pick.
2. Pick the routing.
3. Pick the members. The form shows each model id with the instances that
   serve it, so the same model on two instances sits together.
4. Select **Save**.

The page shows each group with its members: the instance, the model id there,
the health, and the requests of this server in flight. Below the groups, the
**Instances** table shows this server and each LocalAI remote with how this
server reaches it (**Direct**, **Hole-punched**, **Via a relay**), its share
of the requests now and its number of models.

## Routing

| Routing | Which instance a request starts on |
|---|---|
| **Least busy** (default) | The healthy member with the fewest requests in flight from this server. Ties go to each member in turn. |
| **Round robin** | Each healthy member in turn. |

The routing applies at once when you change it on the page.

With both policies:

- A member that fails is marked **Not answering** and the request moves to the
  next member. The member gets requests again after it passes the health
  checks. See [Model failover]({{% relref "/models/model-failover" %}}) for the
  checks and their settings.
- A member reached through a relay that limits the connection shows **Small
  requests only**. It takes a request only when no other member can, so long
  answers do not hit the relay's limit of data or time.
- When no member answers, the group shows **No instance answers** and each
  request still tries every member.

The old `--random-worker` and `--target-worker` options of the removed
federated server map to **Round robin** and to a pin on the
[Failover page]({{% relref "/models/model-failover#pinning-a-target" %}}).

## Drain an instance

Select **Drain** on a member to take it out of the group, for example before
maintenance. Requests in flight finish, new requests go to the other members,
and the health checks go on. Select **Put back** to give it requests again.
The last member in service cannot be drained.

## How a group is stored

A group is a model config: a [failover chain]({{% relref "/models/model-failover#spread-requests-over-the-targets" %}})
with `policy: least_busy` or `policy: round_robin`. You can also write one by
hand:

```yaml
name: shared-qwen
failover:
  policy: least_busy
  targets:
    - model: qwen3-8b          # a model of this server
    - model: branch-qwen3-8b   # an entry of a remote
    - model: lab-qwen3-8b
      drain: true
```

Because a group is a chain, it also shows on the **Failover** page, and the
pin and the health settings of a chain apply to it. A change on the
Federation page keeps the other keys of the file.

## Several frontends

The group is a model config file, so every frontend of a cluster sees the
same groups, policies and drain flags. Health follows the rules of
[failover in distributed mode]({{% relref "/models/model-failover#distributed-mode" %}}).

The requests in flight that **Least busy** counts, and the turn of **Round
robin**, are kept by each frontend for itself. They are not shared: each
frontend spreads its own requests, and the page shows the counts of the
frontend that answered it.

Remotes over P2P are not available in distributed mode (see
[Remotes]({{% relref "/administration/remotes#remotes-over-p2p" %}})), so a
group in a cluster uses this server's models and remotes reached at an
address.

## HTTP API

All routes are admin only.

| Route | What it does |
|---|---|
| `GET /api/federation` | The instances (`id` is `local` for this server, else the remote id; each with `transport`, `status`, `p2p` with `peer_id`, `state`, `path` and `limited`, `in_flight` and `models`) and the groups (`name`, `policy`, `state`, and `members` with `model`, `instance`, `instance_name`, `upstream_id`, `drained`, `state`, `in_flight`, `constrained` and `last_error`). |
| `POST /api/federation/groups` | Makes a group: `{name, policy, members}`. `policy` is `least_busy` (default) or `round_robin`. `members` are at least two model names. Answers `201` with the group, `400` with a message for a wrong input, `409` when a model has the name. |
| `GET /api/federation/groups/{name}` | One group. `404` for a name that is not a group. |
| `PATCH /api/federation/groups/{name}` | Changes `policy` and/or `members`. A new member list keeps the drain flag of the members that stay. |
| `POST /api/federation/groups/{name}/drain` | `{model, drain}`: `drain: true` stops new requests to the member, `false` starts them again. |
| `DELETE /api/federation/groups/{name}` | Removes the group. The members stay as models. Answers `204`. |

Example:

```bash
curl -X POST http://localhost:8080/api/federation/groups \
  -H "Authorization: Bearer $ADMIN_KEY" -H "Content-Type: application/json" \
  -d '{"name":"shared-qwen","policy":"least_busy","members":["qwen3-8b","branch-qwen3-8b"]}'
```

The LocalAI Assistant and `local-ai mcp-server` have the tools
`list_federation` and `manage_federation_group` (actions `create`, `update`,
`delete`, `drain` and `undrain`).

## Limits

- Groups do not nest, and a group cannot be a member of a failover chain.
- The page does not add instances. Add them as remotes on the **Models**,
  **Remotes** page.
