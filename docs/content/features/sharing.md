+++
disableToc = false
title = "Sharing resources"
weight = 27
url = "/features/sharing/"
+++

LocalAI keeps one record of who may use a shared resource. Add-on resources,
such as a [Tables]({{%relref "features/tables" %}}) database, are shared through
the add-on routes. Other kinds of resources are shared through the routes on this
page, with the same rules. A kind is a type of resource that a LocalAI service
registers with the host, for example `example:note`.

Sharing is never a tool. No MCP tool, model tool, agent or proposal can share,
lower or revoke anything. The routes that change a grant accept only the owner's
own browser session.

## Roles

Each resource has one owner, who created it. The owner never changes and is never
a grant. The owner may do everything with the resource, including sharing and
deleting it.

The other people hold a grant. A grant gives one role on the whole resource. Each
kind declares which roles it grants and, for each action, which roles may run it:

| Kind | Roles | What a role may do |
|------|-------|--------------------|
| Add-on resources | `viewer`, `editor` | `viewer` reads. `editor` reads and writes rows. Only the owner changes the schema, shares or deletes. |
| Other kinds | `use`, `manage` (or the subset the kind declares) | The kind decides. Usually `use` reads and runs the resource and `manage` also changes it. |

A role is a set of actions, not a rank. LocalAI refuses to register a kind whose
table names a role that the kind does not grant. A role that is stored but not
listed for an action is denied, and so is any role that LocalAI does not know.

A kind also decides who may receive a grant. A kind without that rule cannot be
shared with a person at all.

## Sharing with everyone

A kind can allow the principal `@everyone`. A grant to `@everyone` reaches every
active named account, including accounts created after the grant. It is stored as
one grant. No account can sign in as `@everyone`.

A person can hold a grant of their own and the grant to `@everyone`. The two add
up: the person may run every action that one of them allows. Each one is changed
and revoked by itself. Revoking `@everyone` leaves the grants of named people, and
revoking a person leaves what `@everyone` holds.

## Lowering and revoking

When the owner lowers or removes a grant, LocalAI:

1. records the change and starts a new authority epoch for the resource,
2. cancels the admitted work of the grantee that the change took away, for
   `@everyone` the work of every person that their own grant does not allow,
3. waits for that work to stop, and only then answers.

If the work does not stop within 10 seconds, the answer is a 503 and the change is
already in force for new work. Repeat the same call. LocalAI keeps the change in its
revocation log, so restoring an older backup does not give the access back.

## API

All routes need an authenticated named account with the `addons` feature. The
grants live in the policy store of the add-on owner (see
[Addon contracts]({{%relref "features/addons" %}})), so the routes exist only while
add-ons are on, and a frontend of a distributed deployment forwards each call to the
owner.

| Method and path | Who may call it |
|-----------------|-----------------|
| `GET /api/shares/{kind}` | Anyone. API keys may call it. |
| `GET /api/shares/{kind}/{resourceId}/grants` | The owner, from the owner's browser session |
| `PUT /api/shares/{kind}/{resourceId}/grants/{principal}` | The owner, from the owner's browser session |
| `DELETE /api/shares/{kind}/{resourceId}/grants/{principal}` | The owner, from the owner's browser session |
| `GET /api/shares/{kind}/{resourceId}/users?q=` | The owner, from the owner's browser session |

A browser session call must be a same-origin `fetch`, carry the header
`X-LocalAI-Confirm` with the resource ID, and carry no API key and no tool, agent or
MCP marker. Any other call gets 403.

`{principal}` is the account ID that the user picker returned, or `@everyone`.

### List your resources of a kind

```bash
curl -b session=... http://localhost:8080/api/shares/example:note
```

```json
{
  "kind": "example:note",
  "roles": ["use", "manage"],
  "everyone": true,
  "owned": [{"id": "r0123456789abcdef0123456789abcde", "role": "owner", "sharedWith": 2}],
  "shared": [{"id": "r5555555555555555555555555555555", "role": "use", "everyone": true, "owner": "Alice"}]
}
```

`owned` and `shared` list at most 100 resources each; `truncated` says that more
exist. `everyone` on a shared resource says that your role comes from the grant to
`@everyone`.

### Share a resource

```bash
curl -X PUT -b session=... \
  -H 'Content-Type: application/json' \
  -H 'X-LocalAI-Confirm: r0123456789abcdef0123456789abcde' \
  -H 'Sec-Fetch-Site: same-origin' -H 'Sec-Fetch-Mode: cors' -H 'Sec-Fetch-Dest: empty' \
  -d '{"role":"use"}' \
  http://localhost:8080/api/shares/example:note/r0123456789abcdef0123456789abcde/grants/@everyone
```

The answer lists the grants after the change. A grant to `@everyone` has the user
ID `@everyone` and no display name. The call is safe to repeat. If the answer is a
503 with type `outcome_unknown`, repeat the same call.

### Errors

| Status | Type | Meaning |
|--------|------|---------|
| 400 | `invalid_request` | The kind does not grant the role, or the search text is not 2 to 64 characters. |
| 403 | `addon_error` | Not the owner, not a browser session, a person the kind does not accept, an unknown kind or an unknown resource. These get the same answer, so a caller cannot tell whether a resource exists. |
| 409 | `sharing_disabled` | The installation runs in local-owner mode, which has no named accounts to share with. |
| 503 | `outcome_unknown` | A grant change may have run. Repeat the same call. |

## For service authors

A service shares its resources by implementing `policy.KindProvider`
(`core/services/addons/policy`) and passing it to
`Application.RegisterShareKinds` before the add-on owner starts. The provider
registers a `policy.KindSpec`: the grantable roles, the role table, the rule for who
may receive a grant, whether `@everyone` is allowed, who may create a resource and
how to check that the backing of a pending resource exists. The service then creates
its resources with `Policy.CreateRef` and runs its work under `Policy.AdmitRef`, so
that a lowering or a revocation cancels it. Package
`core/services/addons/policy/sharetest` is a complete example that only the tests
use.

## Distributed mode

The grants are stored only in the policy store of the add-on owner, the single
process that writes them. Frontends hold no copy and forward every call, so no
frontend answers from stale state, and there is no per-instance behavior to
configure. The kinds register on the owner when its policy opens.
