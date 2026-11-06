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

The grants are stored only in the policy store of the add-on owner, the single
process that writes them. Frontends hold no copy and forward every call, so no
frontend answers from stale state, and there is no per-instance behavior to
configure. The kinds register on the owner when its policy opens.
