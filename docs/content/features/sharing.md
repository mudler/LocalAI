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
| Saved agents (`agent`) | `use`, `viewer` | `use` chats with the agent as yourself. `viewer` also reads the definition. Only the owner edits, deletes, exports or shares. See [Share an agent]({{%relref "features/chats-agents#share-an-agent" %}}). |
| Other kinds | `use`, `manage` (or the subset the kind declares) | The kind decides. Usually `use` reads and runs the resource and `manage` also changes it. |

A role is a set of actions, not a rank. LocalAI refuses to register a kind whose
table names a role that the kind does not grant. A role that is stored but not
listed for an action is denied, and so is any role that LocalAI does not know.

A kind also decides who may receive a grant. LocalAI asks that rule only about
active named accounts, so a disabled or unknown account never receives a grant. A
kind without the rule cannot be shared with a person at all. The add-on kinds keep
their own rule: the person must be allowed to use add-ons.

## Features

A kind can name the feature (permission) that a person needs to use its resources,
for example a feature that turns a part of LocalAI on or off per account. LocalAI
checks it with the account on every call for a resource of the kind: listing,
sharing, and the service's own work under the grant. A kind that names no feature
needs only an active named account with a valid credential.

Sharing a kind does not need the `addons` feature, unless the kind names it. Only
the add-on resources and their routes under `/api/addons` need it.

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

All routes need an authenticated named account. They do not need the `addons`
feature; a kind that names a feature needs that one (see [Features](#features)).
The grants live in the policy store of the add-on owner (see
[Addon contracts]({{%relref "features/addons" %}})), so the routes exist only while
the add-on owner runs (`--addons-enabled`, on by default), and a frontend of a
distributed deployment forwards each call to the owner.

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

The user picker (`GET .../users?q=`) searches every active named account by a prefix
of the display name or of the address, 2 to 64 characters, and keeps only the
accounts that the kind accepts. It returns at most 20 people, each with the account
ID and the display name only, and never the owner or the caller.

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
| 403 | `addon_error` | Not the owner, not a browser session, a person the kind does not accept or whose account is not active, a feature the kind names and the caller does not hold, an unknown kind or an unknown resource. These get the same answer, so a caller cannot tell whether a resource exists. |
| 409 | `sharing_disabled` | The installation runs in local-owner mode, which has no named accounts to share with. |
| 503 | `outcome_unknown` | A grant change may have run. Repeat the same call. |

## Kinds of LocalAI itself

LocalAI shares two kinds of its own resources with these routes. Their owners get
the resource ID from the page of the resource, or from the route in the table.

| Kind | What it is | Roles | `@everyone` | Feature | Resource ID from |
|------|------------|-------|-------------|---------|------------------|
| `connection` | A [connector binding]({{%relref "features/chats-connectors#sharing-a-connection" %}}) | `use` | No | `connectors` | `POST /api/chats/connectors/{id}/share` |
| `collection` | A [collection]({{%relref "features/collections#sharing-a-collection" %}}) | `viewer`, `editor` | Yes | `collections` | `POST /api/collections/{name}/share` |

- **`connection`**: `use` lets the person's chats and agents run the tools of the
  binding's add-on with its credential. The person sees the name and the state of
  the binding and never its token or settings; every other operation stays with the
  owner and answers `404` to the person. Messages to the bot still reach the owner's
  agent.
- **`collection`**: `viewer` searches and reads, `editor` also adds and removes
  entries. Sources, reset and sharing stay with the owner. The person finds a shared
  collection by its name; their own collection of that name wins, and the shared one
  is then named `shared:<resource ID>`.

The binding and the collection stay where they are. A share is one record of the
policy that names the binding ID or the collection name of its owner, so nothing is
copied. Work that reads or sends through a shared resource is admitted work of that
record: lowering or revoking cancels it as described above, and the next call gets
the answer of a resource that does not exist. Deleting a binding drops its record
after it revoked every grant.

Admission needs the policy, which only the add-on owner holds. With several
frontends, the shared bindings and collections are listed and used only through the
owner process; the other frontends list none and report `sharing: false`. The calls
through a shared binding always run on the owner, which holds the tokens.

## For service authors

A service shares its resources by implementing `policy.KindProvider`
(`core/services/addons/policy`) and passing it to
`Application.RegisterShareKinds` before the add-on owner starts. The provider
registers a `policy.KindSpec`: the grantable roles, the role table, the rule for who
may receive a grant, whether `@everyone` is allowed, the feature a caller needs
(`Feature`, empty for none), who may create a resource and how to check that the
backing of a pending resource exists. The service then creates
its resources with `Policy.CreateRef` and runs its work under `Policy.AdmitRef`, so
that a lowering or a revocation cancels it. A service that already names its
resources keeps its names: `Policy.EnsureRef` makes the record of a name on its
first share, `Policy.FindRef` and `Policy.LocalOf` look it up from either side, and
`Policy.ForgetRef` drops it, after it revoked every grant, when the resource is
deleted. In a process that does not hold the policy, the service uses
`policy.Forwarded` (the same calls, `policy.Sharing`), which asks the owner
through the add-on gateway. Package
`core/services/addons/policy/sharetest` is a complete example that only the tests
use.

## The Share dialog

The web UI has one Share dialog for every resource that can be shared. It shows
who has access and their role, finds people by name, offers `@everyone` where the
kind allows it, asks before it revokes a grant, and says so in local-owner mode.
A search result is only an account ID and a display name: an address is never
shown.

The dialog does not know the routes. It takes a client with four calls:

| Call | Answer |
|------|--------|
| `grants()` | `{ grants: [{ userId, displayName, role }] }` |
| `setGrant(principal, role)` | The grants after the change |
| `revoke(principal)` | The grants after the change |
| `searchUsers(query)` | `{ users: [{ id, displayName }] }` |

### In an add-on

An add-on page gets the dialog as `sdk.ui.ShareDialog` and the client of one of
its own resources from `sdk.resources.shareClient(resourceId)`. The client uses
the add-on routes and is bound to the add-on and its installed generation. Add-on
resources have the roles `viewer` and `editor` and no grant to `@everyone`. Render
the dialog in the page while it is open; it draws its own modal frame, so do not
put it inside `sdk.layers.openDialog`.

```jsx
const ShareDialog = sdk.ui.ShareDialog
const client = R.useMemo(() => sdk.resources.shareClient(db.id), [db.id])

{open && ShareDialog ? (
  <ShareDialog
    client={client}
    resource={{ id: db.id, name: db.name }}
    sharing={listing.sharing}
    roles={[
      { id: 'viewer', label: t('role.viewer'), help: t('role.viewerHelp') },
      { id: 'editor', label: t('role.editor'), help: t('role.editorHelp') },
    ]}
    onChanged={refresh}
    onLost={() => { setOpen(false); showUnavailable() }}
    close={() => setOpen(false)}
  />
) : null}
```

A host that is older than the dialog has no `sdk.ui.ShareDialog`. Check for it and
hide the Share button when it is missing. The SDK version stays 2: the dialog, the
badges and `shareClient` are additions to the SDK 2 surface.

### In a page of the web UI

A page of the React UI imports `ShareDialog` from `components/share/ShareDialog`
and builds the client of a resource of a kind with
`kindShareClient(sharesApi, kind, resourceId)` (`components/share/shareClients.js`
and `utils/api.js`). Take the roles and `everyone` from the answer of
`GET /api/shares/{kind}`; `shareRoles(roles, t)` in `components/share/shareModel.js`
gives the host's words for `use`, `manage`, `viewer` and `editor`. The dialog and
the badges are also in the UI catalog at `/app/ui-catalog`.

### Props

| Prop | Meaning |
|------|---------|
| `client` | The four calls above. |
| `roles` | `[{ id, label, help }]`, the roles the kind grants, in order. The first one is the default role for people you add. `help` is shown under the search field. |
| `allowEveryone` | `true` when the kind may be shared with `@everyone`. |
| `resource` | `{ id, name }`. The name is in the title. |
| `sharing` | `false` in local-owner mode. The dialog then shows that sharing is not available and makes no call. |
| `references` | Optional. Other resources that this one uses, which the owner decides on before sharing: `[{ id, label, kind, options }]`, where `options` is a subset of `view`, `use` and `none`. The dialog does not add anyone until every row has a choice. |
| `onShare` | With `references` only: called after each new grant, in the same step, with `{ principal, role, choices }`, where `choices` maps each reference ID to the chosen option. The page shares the referenced resources from there. |

A reference row with no `options` (or only `none`) is shown read-only with the
line "They need access of their own": the dialog asks nothing for it. The agents
page passes such rows for the folders, skills, add-ons, MCP servers and built-in
grants of an agent, and for collections and connections that cannot be shared
in the build.
| `onChanged` | Called after every change. |
| `onLost` | Called when the answer says that the person can no longer share the resource here (403, 401, or the add-on was withdrawn). |
| `close` | Closes the dialog. |
| `title`, `intro` | Replace the default title and the line under it. |

### Role and "Shared by" on a list row

`sdk.ui.RoleBadge` (`role`, `label`) draws the caller's role as a chip.
`sdk.ui.SharedByBadge` (`entry`, `roleLabel`, `hideRole`) draws the role and one
line from a row of a listing: "Shared by Alice", "Shared with everyone by Alice",
"Shared with 2" or "Private". Pass a row of `GET /api/shares/{kind}` as `entry`:
owned rows have `role: "owner"` and `sharedWith`, shared rows have `role`, `owner`
and `everyone`.

## Distributed mode

The grants, and the records of the bindings and collections that LocalAI shares,
are stored only in the policy store of the add-on owner, the single
process that writes them. Frontends hold no copy and forward every call, so no
frontend answers from stale state. The kinds register on the owner when its
policy opens. Each frontend registers its kinds too, but keeps them and does
not use them: the owner decides.

This applies to the `/api/shares` routes and to the services that use a shared
resource. A frontend that serves a person asks the owner, over the add-on
carrier, to list the resources of a kind that are shared with that person, to
create a resource, and to admit a piece of work, such as a chat turn with a
shared agent. The work runs on the frontend.

### Admitted work on a frontend

The owner holds the admission of work that runs on a frontend under a lease. The
frontend keeps the lease alive with a request that waits on the owner for up to
15 seconds and then starts again.

- When the owner lowers or revokes a grant, the waiting request answers at once.
  The frontend stops the work and keeps the lease alive until the work has
  stopped, then releases it. Only then does the owner's call answer. If that
  takes more than 10 seconds, the answer is the 503 described in
  [Lowering and revoking](#lowering-and-revoking), and the change is in force for
  new work.
- When a frontend stops renewing a lease (it stopped, crashed or lost the
  network), the owner ends the admission 10 seconds after the last request. A
  revocation never waits longer than that for a frontend that is gone.
- When the frontend cannot reach the owner, it stops the work at once. The owner
  could no longer stop it.
- When the owner restarts, it knows no lease. Every frontend stops its admitted
  work at its next request.

The leases are the only state of this feature that is not in the policy store.
They are kept per instance, in the owner's memory. Only the owner holds the
policy, so there is no second copy to diverge from. When the owner cannot be
reached, a frontend lists nothing that is shared with the person, creates nothing
and admits nothing. Each call gets the same typed unavailable answer as the
add-on calls, never a grant.
