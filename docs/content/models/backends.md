---
title: "Backends"
description: "Learn how to use, manage, and develop backends in LocalAI"
weight: 80
aliases:
  - "/backends/"
---


LocalAI supports a variety of backends that can be used to run different types of AI models. There are core Backends which are included, and there are containerized applications that provide the runtime environment for specific model types, such as LLMs, diffusion models, or text-to-speech models.

## Available Backends

LocalAI ships **60+ backends** covering text generation, speech-to-text, text-to-speech, music and sound generation, image and video generation, vision and object detection, audio processing, reranking, fine-tuning, and more. Each one is published as an on-demand OCI image with the appropriate acceleration variants (CPU, CUDA 12/13, ROCm, Intel SYCL, Vulkan, Metal, Jetson L4T).

For the complete list of backends, the model families they support, and their acceleration targets, see the [Backend & Model Compatibility Table]({{%relref "/models/compatibility-table" %}}). The authoritative source is the backend library, [`gallery/backends/index.yaml`](https://github.com/mudler/LocalAI/blob/master/gallery/backends/index.yaml) (see [The backend library and the generated gallery](#the-backend-library-and-the-generated-gallery)), and the same catalog is browsable in the web UI under the **Backends** section.

## Prefer installed development backends

Set `LOCALAI_PREFER_DEV_BACKENDS=true` (legacy alias: `PREFER_DEV_BACKENDS`)
or pass `--prefer-development-backends` to prefer development builds for generic
backend aliases. The default is `false`, which preserves existing selection,
including installations that contain only development builds.

When enabled, LocalAI selects compatible installed builds with an existing,
non-directory run file. It prefers names ending in `-development` (or the
configured `LOCALAI_BACKEND_DEV_SUFFIX`). Within that group, existing hardware
ranking and lexical-name ordering still apply. If no eligible development build
exists, LocalAI selects an eligible non-development build. If neither group has
an eligible build, it does not register the alias.

For example (`my-engine` is a placeholder name, not a real backend), generic `my-engine` selects `cpu-my-engine-development`
over `cpu-my-engine` when both are installed and compatible. A compatible
CPU development build also takes priority over a stable CUDA build.
An incompatible development build cannot take priority over a compatible stable
build. Compatibility uses LocalAI's existing hardware heuristics, including the
`LOCALAI_FORCE_META_BACKEND_CAPABILITY=disable` override; it does not guarantee
that native libraries can load.

Explicit concrete names keep their original paths. Selecting
`cpu-my-engine` does not redirect to its development counterpart.
Installing a development image under a stable name does not rename that entry.
The preference also retains its existing development-first download behavior.

The **Settings** value `prefer_development_backends` supplies the frontend's
preference at its next startup. POST `/api/settings` returns `restart_required: true` when this value changes,
while retaining the existing `success` and `message` response fields. Unchanged
values return `restart_required: false`. This notification does not reconfigure
the watchdog; genuine watchdog setting updates still apply live. Neither API
updates nor edits to `runtime_settings.json` change
registered paths or restart running models. The displayed preference can change
before the resolver adopts it. Startup uses the existing settings merge:
explicit default-valued CLI or environment values, including `false`, can be
indistinguishable from unset and do not necessarily override a saved `true`.

For distributed `local-ai worker`, set the flag or environment variable on each
worker, then restart it. Frontend flags and Settings changes do not configure
remote workers. Legacy specialized worker subcommands do not expose this
worker-local option.

## Managing Backends in the UI

The **Operate → Runtime → Backends** page is the canonical home for the
complete backend lifecycle. It is one list with two views, **Installed** and
**Catalog**, each with a count:

1. **Catalog** browses configured galleries, searches by name or description,
   filters by capability, and installs a backend. Catalog is the default view.
   On a new installation with nothing installed it recommends llama-cpp, which
   runs most text models, with a single install button.
2. **Installed** shows the runtimes present on the host or cluster, those with
   an update first.
3. Target-node links compose with the current view and selection instead of
   opening a separate management page.

Above the list there is one row of controls: the two views, the search, a
**Filters** button and a **More** menu (⋮). The row stays at the top of the
page while you scroll a long list.

- **Filters** opens a menu (a sheet on a phone). In Installed, filter by state:
  user, system, with an update, or on an offline node. In Catalog, filter by
  type: chat, image, video, TTS, STT, or vision. In both, **Variant builds**
  and **Development builds** show the builds that are hidden by default, and,
  when library sources are set up, **Library** narrows the list to one source.
  The button shows how many filters are on.
- The line under the controls shows how many backends are listed, each active
  filter as a chip that you can remove, and, when updates exist, **N updates
  available · Review**. **Review** opens the [Updates page](#the-updates-page)
  on the ready updates.
- **More** holds the actions you need less often: **Updates** opens the
  Updates page, where **Check now** runs a real check,
  **Install from URL…** installs a backend from an OCI image, a URL or a path,
  and **Manage libraries…** opens the library sources panel.

Each row shows the backend, its version and its state: **Current**, **Update
1.2.0**, **Not installed**, **Queued**, **Removing**, or a progress bar while it
installs, with **Cancel** in the row. A failed install says why, with **Retry**.
The one action that matters sits in the row (**Install**, or **Review** when
an update exists). The row menu (⋮) holds **Reinstall backend** and **Delete
backend** in both views, and the chevron opens the row for the rest. This page
starts no update of a gallery backend. **Review** on a row only opens the
Updates page: the page of that backend when nodes report it (it asks before
it updates a node), otherwise the list of ready updates. The update runs there,
as an operation in Activity. A backend that came from a library keeps its
**Update** button in the row, which installs the newer build that library
lists and shows the progress in the row. The same update is also listed on
the Updates page.

The current view, search, filter, open row, and target node are stored in the
URL. Browser Back and shared links therefore restore the same state.

**Removing** a backend asks first. The dialog names the configured models that
ask for that backend by name, and any installed meta backend that points at it
(for example `llama-cpp` pointing at a hardware-specific build), because those
stop working without it. The catalog does not report a size per backend, and
LocalAI keeps no earlier version of a backend, so there is no size column and
no rollback.

**Operate → Runtime → Logs** (`/app/backend-logs`) lists the backend processes
that have printed something. Open one to read its output live, filter by stream
or text, follow the end, show or hide times, export the lines as JSON, or clear
them. Clearing hides the lines at once and wipes them on the server after 6
seconds, unless you press **Undo**.

Installs run in the background. The strip at the top of the app follows the
current one, and **Operate → Activity** lists everything in flight, what needs
attention, and what has finished, and is where a running install is paused,
cancelled or a failed one retried. See [Activity]({{% relref "/administration/activity" %}}).

Each open row displays:
- Backend name and description
- Type of models it supports
- Installation status
- Version, source, digest, placement, and catalog information
- The models that use the backend, a link to its logs, and, in a cluster, the
  per-node controls

## The Updates page

**Operate → Updates** (`/app/updates`) is the one place that answers "what is
out of date, and what is changing". It is for administrators. It lists three
kinds of things: backends of the gallery, backends of a library, and add-ons.

At the top, one sentence says how many updates there are and how many are ready,
updating or need you. Under it a line for each source (the backend gallery, the
backend library and the add-ons) says when it was last checked or why the check
failed, and the two settings **Update backends automatically** and **Update
add-ons automatically** are shown as facts, with a link to Settings. The page
never says "Everything is up to date" while a source failed or was never
checked. A failed source gets a red notice that says what may be missing, and
the list keeps the last good result and marks its rows as from an earlier check.

The rows are in four groups, in this order:

| Group | What is in it |
|---|---|
| **Needs you** | An update that failed (**Retry**), an add-on upgrade that is held until you approve the access it asks for (**Review**), and an update that cannot start, with the reason (an add-on that cannot run on this host, or backend installs turned off on this server). |
| **Updating** | An update in progress, with its phase and progress. **Cancel** shows where the operation can be cancelled. |
| **Ready to update** | An update that you can start. An add-on asks for your confirmation first, in this browser. |
| **Up to date** | One collapsed line. A backend is listed here only when its source answered. |

**Check now** runs a real check of every source and waits for the answer. It
asks the registries (at most 45 seconds), reads each add-on library with your own
browser session, and shows the result under the sentence. The button waits 15
seconds before the next check, as the server answers a check within that time
from the last one.

An update is **done** only when two things hold: the operation ended without an
error, and a new check no longer lists the item. If the operation ended and the
item is still listed, the row says "The update ended but the version did not
change". A throttled check answer, which is seconds old, is asked again before
the page decides.

**Update N ready** runs the checked rows one after another. Rows that are held
or blocked never take part, a failure does not stop the others, and **Retry
failed** runs only the failed ones. The summary ("2 updated, 1 failed") counts a
row as updated only when it is done by the rule above. Closing the page does not
stop the run: the operations are the server's, and the page reads them again when
you return.

In a cluster, a backend that more than one node has shows a **Nodes** action.
It opens `/app/updates/backend/<name>`, which lists each node with its version,
the models that use the backend there, and its state, and updates one node or
every node that needs it. **Update** and **Retry** on such a row, and **Update N ready** when the selection holds one, open the same confirmation. It lists every affected node and the models that stop, and nothing starts until you confirm. The other selected rows start when you confirm.
Every update that touches a node asks first, because a worker stops the models
that use the backend: see [the Nodes page]({{% relref "/administration/distributed-mode" %}}).
On a single machine there is no **Nodes** action and **Update** starts at once.

### Shortcuts from other pages

Other pages show that an update exists and send you here; none of them runs a bulk
update of its own. The Backends page has **Review** in its rows and in the line above
its list. The Operate **Status** page lists the backends with an update as one row,
"N updates available", with **Review**. The Apps page has **Open Updates**. The
**Nodes** page keeps its **Update** button for the nodes you selected, which opens the same
confirmation as this page, and a node's **Backends** tab has **Update** for a backend that
is behind on that node.

### The count on the Updates tab and on Operate

A number on the **Updates** tab and on the **Operate** item of the sidebar (on
every page, for administrators) counts what needs a person: ready updates that
no automatic setting covers, plus updates that failed, are held for approval, or
are blocked by something other than the server setting for backend installs. An
update that an automatic setting will apply is listed on the page but not
counted. The number is red when an update failed. One poll reads the numbers
once a minute while the browser tab is visible, and again when an operation ends.

The badge on the **Operate** item has one number at a time, so it follows an
order: failed operations first (red, counting the failed operations), then the
count above, then the number of operations that are running. The tab always
shows the count above, so the two numbers differ while an operation has failed.

## Updates in the inbox

Administrators get updates in the [inbox]({{% relref "/features/chats-and-agents/chats-agents#inbox" %}}) (the **Notifications** entry in the sidebar), so they see them on any page and, through browser push and a Telegram chat, away from the browser. Three kinds of item exist. Every one has a `code` and `params`, so the app words it in your language. Every one is for administrators only (when authentication is off, for the one local user). With the chats off there are none.

- **The digest**, "N backend updates available". It counts the backend updates (gallery and library) that need a person: the same updates the badge of the Updates tab counts for backends. It does not count a backend that **Update backends automatically** covers, so with that setting on there is no digest, and a stored digest is removed. With the setting off, every update that the last check found is counted, and a server that refuses backend installs counts none. There is one digest for each administrator, kept up to date in place:
  - the first update creates it;
  - a **new update** brings it back as a new, unread item at the top, and pushes it once. A new update is an id that the digest did not show before: a backend and the version or digest it would move to. A newer release of a backend that the digest already listed is a new update;
  - when updates are done, the count goes down quietly: the item keeps its place and its read state, and nothing is pushed;
  - when you delete it, it is hidden, not forgotten. It stays hidden while the updates are the same or fewer, and it comes back, unread, for the next new update;
  - when none is left it is removed. A later update is then new again.
  The digest changes only when every source that can be checked answered in that pass. A failed check does not turn "unknown" into "all done".
- **The result of an automatic pass**, "Backends: 2 updated automatically, 1 failed". When the automatic update installed or failed to install something, the pass writes one item, with the counts and the names. A pass that did nothing writes nothing. The item is a failure (red) when anything failed, and then it links to the updates that need you. A report is written once: the same failure of the same release at the next pass (the pass retries every six hours) does not write it again, also after you deleted the first item. A different release, or a different set of outcomes, is a new item. Add-ons write their own item in their own pass (see [Add-ons]({{% relref "/addons/contract-reference#automatic-upgrade" %}})).
- **The failed check**, "The update check failed". When the automatic check of the backend gallery or the backend library fails in two passes in a row (more than half a day), one item names the sources. It is removed when the check works again, and written again only if a source fails again for two passes. The Updates page shows the same failure on its source line at once.

Each item opens the Updates page on the list it is about: the digest opens **Ready**, a result with a failure or a held update opens **Needs you**.

**What is not in the digest.** Add-on updates are not counted in the digest. The state of an add-on update is read by the add-on owner with the session of an administrator, and the frontend that writes the digest may not be the owner; the digest would then change with whichever frontend holds the lock. The Updates page and its badge show add-ons. With **Update add-ons automatically** on, the add-on result is in the inbox. With it off, an add-on update shows on the page and in the badge only.

**Who writes them.** The digest, the failed-check item and the backend result are written at the end of the automatic pass, by the process that runs it. In [distributed mode]({{% relref "/administration/distributed-mode#automatic-backend-updates" %}}) that is the frontend that holds the update lock (`KeyBackendUpgradeCheck`), so there is one writer for the cluster, and its push and Telegram forward happen once. A standalone server writes after each pass that may install. A check you start with **Check now**, a trigger after an operation, and the first check of a frontend write nothing.

## Checking for backend updates

`GET /backends/updates` (the UI uses the alias `GET /api/backends/updates`)
lists the gallery and library backends that have a newer build. Both routes
need an administrator. The answer comes from the cache of the instance that
serves the request and contacts no registry. It has:

- `items`: one entry per backend with an update. `kind` is `gallery` or
  `library`. Each has `installedVersion`, `availableVersion`, the digests when
  there are any, and a `state`: `update` (a newer version or digest), `stale`
  (a request showed that the backend is older than the server; `reason` says
  how) or `drift` (the nodes of a cluster do not agree on the build). A library
  item also has `sourceId` and `signed`. In a cluster an item has `nodes`, with
  the version of every node that has the backend and `drift` on the ones that
  differ from the majority.
- `sources.gallery` and `sources.library`: the freshness of each source. `state`
  is `ok`, `failed`, `never` (not checked since the server started) or
  `disabled` (with a `reason`). A source also has `checkedAt`, `lastSuccessAt`
  and `error`. When the last check failed, `stale` is true and `items` still
  lists what the last good check found. A client must not say "no updates"
  while a source is `failed` or `never`.
- `autoUpdate.backends`: the value of **Update backends automatically**
  (`auto_upgrade_backends`). It covers gallery and library backends.
- `checkedAt`: the latest check of any source.

`POST /backends/updates/check` (alias `POST /api/backends/updates/check`) checks
now. It refreshes the library index, asks the registries, waits for the check
(at most 45 seconds) and answers with the same body, with fresh times. A check
that fails or times out is an `error` in the state of its source, and the
previous list stays. Callers that arrive during a check share it. A call within
15 seconds of the last check is answered from it, with `throttled` set. A check
installs nothing. The older `POST /backends/upgrades/check` still only starts a
check and returns the cache at once.

Limits:

- **Library backends are those of the host.** The library comparison reads the
  backends installed on the host that answers. On a cluster frontend that holds
  no backends, library backends installed on workers are not listed, and the
  `library` source says so in its `note`.
- **Models that would stop.** A node entry lists the loaded models whose
  recorded backend has the name of the item. The node registry records the
  backend a model was loaded with; it can be empty, or name a meta backend
  instead of the installed build. An empty `models` list does not prove that no
  model would stop.
- **Without a checker** (backend installs are disabled, or no backend gallery is
  configured) both sources are `disabled` and `items` is empty.

In [distributed mode]({{% relref "/administration/distributed-mode#automatic-backend-updates" %}})
the cache is per frontend. The frontend that gets a check request updates its
own cache only, whether or not it holds the lock that runs automatic updates.
`checkedAt` says how old each answer is.

The LocalAI Assistant reads the same data with the `backend_updates` tool
(`action` `list` or `check`).

## Backend Galleries

Backend galleries are repositories that contain backend definitions. They work similarly to model galleries but are specifically for backends.

### Adding a Backend Gallery

You can add backend galleries by specifying the **Environment Variable**  `LOCALAI_BACKEND_GALLERIES`:

```bash
export LOCALAI_BACKEND_GALLERIES='[{"name":"my-gallery","url":"https://raw.githubusercontent.com/username/repo/main/backends"}]'
```
The URL needs to point to a valid yaml file, for example:

```yaml
- name: "test-backend"
  uri: "quay.io/image/tests:localai-backend-test"
  alias: "foo-backend"
```

Where URI is the path to an OCI container image.

To use a backend gallery or backend images that need authentication, such as a private registry, add a matching entry to the credentials file. See [Private Registries and Galleries]({{% relref "/models/private-sources" %}}).

### Backend Gallery Structure

A backend gallery is a collection of YAML files, each defining a backend. Here's an example structure:

```yaml
name: "llm-backend"
description: "A backend for running LLM models"
uri: "quay.io/username/llm-backend:latest"
alias: "llm"
tags:
  - "llm"
  - "text-generation"
```

### Verifying OCI Backends

The default backend gallery tries `https://index.localai.io/backends`, then `github:mudler/LocalAI/backend/index.yaml@master`, then `oci://quay.io/go-skynet/local-ai-backends:gallery-backends`. The OCI fallback is signed by `gallery_publish.yml`. Its `artifact_verification` policy applies only to the gallery artifact; `verification` continues to control backend image signatures. Existing custom gallery lists are not changed. See [gallery publishing]({{% relref "/models#official-gallery-publishing" %}}) for details.

Backend galleries can require keyless Sigstore signatures for every OCI image
they provide. Add a `verification` policy to the gallery configuration, then
enable strict integrity mode:

```bash
export LOCALAI_BACKEND_GALLERIES='[{"name":"localai","url":"https://index.localai.io/backends","mirrors":["github:mudler/LocalAI/backend/index.yaml@master"],"verification":{"issuer":"https://token.actions.githubusercontent.com","identity_regex":"^https://github\\.com/mudler/LocalAI/\\.github/workflows/backend_merge\\.yml@refs/(heads/master|tags/.+)$"}}]'
export LOCALAI_REQUIRE_BACKEND_INTEGRITY=1
local-ai run
```

The policy pins the Fulcio issuer and the GitHub Actions workflow identity that
signed the image. The identity expression covers development images produced
from `master` and release images produced from tags. Use a narrower expression
if your deployment only accepts one release channel.

Without strict mode, an OCI gallery without a verification policy installs
with a warning. With strict mode, LocalAI refuses galleries without a policy,
images without a compatible Sigstore bundle, and signatures that do not match
the configured identity. Existing images published before bundle signing was
enabled must be rebuilt or re-signed before strict deployments can install
them.

An optional `not_before` RFC3339 value revokes signatures logged before that
time. Advance it after a signing-workflow compromise, then rebuild or re-sign
the trusted images:

```json
{
  "verification": {
    "issuer": "https://token.actions.githubusercontent.com",
    "identity_regex": "^https://github\\.com/mudler/LocalAI/\\.github/workflows/backend_merge\\.yml@refs/(heads/master|tags/.+)$",
    "not_before": "2026-08-05T00:00:00Z"
  }
}
```

When one reusable workflow signs images for several repositories, the
certificate identity names the shared workflow, not the repository that called
it, so an identity match alone accepts an image signed for any of those
repositories. Add `source_repository` to pin the repository the signature was
made for. LocalAI compares it exactly with the source-repository extension of
the signing certificate: a trailing slash, a different letter case or a `.git`
suffix does not match. The value must be an `https://` URL, or LocalAI refuses
the policy when it uses it, when it installs a backend or fetches an `oci://`
gallery. LocalAI versions before this field existed ignore it and do not pin
the repository, so upgrade every node, workers included, before you rely on it:

```json
{
  "verification": {
    "issuer": "https://token.actions.githubusercontent.com",
    "identity_regex": "^https://github\\.com/example/signer/\\.github/workflows/release\\.yml@refs/tags/v.+$",
    "source_repository": "https://github.com/acme/backends"
  }
}
```

### Disabling backend installation

Some deployments must not acquire executable code at runtime: production
systems that run only the backend set they were tested with, locked-down or
air-gapped hosts, images that are built once and then promoted unchanged, and
hosts where backends come from a package manager (placed under
`--backends-system-path`, `LOCALAI_BACKENDS_SYSTEM_PATH`). For all of these,
`--disable-backend-install` (`LOCALAI_DISABLE_BACKEND_INSTALL=1`) tells
LocalAI never to install or upgrade a backend itself:

```bash
export LOCALAI_DISABLE_BACKEND_INSTALL=1
local-ai run
```

What is refused:

- installs from the backend gallery, from OCI images, URLs or local paths —
  from the Backends page, `POST /backends/apply`, the `/api/backends/...`
  install routes, the LocalAI Assistant's `install_backend` tool and the
  `LOCALAI_EXTERNAL_BACKENDS` boot-time list;
- upgrades — manual, "update all", the Assistant's `upgrade_backend` tool and
  the background auto-upgrader, which is not started at all (upgrade checks
  stop too, so no registry is polled for versions nobody can apply);
- installing a model whose backend is not present: refused before anything
  is downloaded, naming the backend, on every install path (gallery, import,
  preload, the Assistant's tools).

What still works: backends already installed, backends provided under the
system path, listing and browsing the backend galleries, and deleting a
backend. In distributed mode a model counts as installable when a healthy
worker has its backend, under its own name or an alias — availability is
judged where inference runs, not on the frontend, which keeps no backends of
its own.

How clients can tell: install and upgrade endpoints answer `403` with
`"code": "backend_install_disabled"`; installing a gallery model whose
backend is not present answers `409` with `"code": "backend_not_installed"`
and the backend's name. `GET /api/features` and the capabilities object in
`GET /.well-known/localai.json` report `"backend_install": false`, and the
Backends page shows a notice and offers no install or upgrade actions.

The switch is read at start-up only; it is not a runtime setting. In
[distributed mode]({{% relref "/administration/distributed-mode#disabling-backend-installation-on-workers" %}})
each worker enforces it for its own backends, so set it on every frontend
and every worker. The standalone `local-ai worker ...` subcommands honour
the same variable: they start with the backend already present under their
backends or system path and exit with an error instead of installing one.

## The backend library and the generated gallery

The backend library, `gallery/backends/index.yaml`, is the **source of truth** of the backends, the way the model library is for the models. Edits go there. The backend gallery, `backend/index.yaml`, is a **generated copy** of it. It stays only so that releases from before the cutover, which read the gallery, keep working. Never edit it by hand; CI fails when it differs from the generated copy.

An entry of the library is one backend image for one platform, pinned by a digest. It carries what the gallery carried: the name, `alias`, `description`, `license`, `urls`, `tags`, an `icon` URL, the ordered `mirrors` and the `capabilities` map of a meta backend. A development image (a `master-` tag) keeps its tag and has no digest in the library; the install pins it. The [Add-ons page]({{% relref "/addons/contract-reference#a-backend-entry" %}}) lists every field.

### Which source a build reads

A build reads one of the two by default, and the other stays listed so you can turn it on with `local-ai library enable`:

| Build | Default backend source |
|---|---|
| From master, or a plain `go build` | The library (`official-backends`) |
| From a tag, before the cutover | The gallery (`localai-backends`) |
| From a tag, after the release workflow cuts over | The library |

The value is set at build time with `-X github.com/mudler/LocalAI/core/library.BackendSource=library` (or `gallery`), through `LOCALAI_BACKEND_SOURCE` in the Makefile and GoReleaser. The Makefile picks `gallery` when the commit carries a tag. The release workflow has a step that sets `LOCALAI_BACKEND_SOURCE=library`, so a release build cuts over by itself. The container image `Dockerfile` pins `LOCALAI_MODEL_SOURCE=library` and `LOCALAI_BACKEND_SOURCE=library` as build arguments, so a tagged image reads the same sources as a release binary; pass `--build-arg LOCALAI_BACKEND_SOURCE=gallery` to build an image that reads the gallery. Any other value reads the library, so a build never has no backend source. `--addons-backend-library=false` removes the library and every build reads the gallery.

When the library is the source, LocalAI reads it as a gallery in library format. A backend installs by the tag and the mirrors of its entry, the way the gallery installs it, and the Backends page lists the same backends. If the library cannot be fetched or read, listing the backends fails with an error; LocalAI does not fall back to the gallery, so the problem is never silent. The library has no mirrors of its own. A backend library you add with `local-ai library add` is read by the add-on catalog only, which installs its images by digest.

### Edit the library, generate the gallery

To add a backend, write its entries in the gallery shape in a scratch file (see [Adding a new backend](https://github.com/mudler/LocalAI/blob/master/.agents/adding-backends.md)) and merge them into the library. Then generate the gallery:

```bash
go run ./scripts/backends/cmd/backendcatalog --add my-backend.yaml --library gallery/backends/index.yaml
go run ./scripts/backends/cmd/backendcatalog --library gallery/backends/index.yaml --out backend/index.yaml
```

The first command pins the `latest-` images that are in the registry to the digest of each platform. An image that is not published yet is written unpinned and `mutable`, and installs by its tag until a maintainer pins it with `--repin`. The second command reads the library with the same strict reader the host uses, converts every backend to a gallery entry and back, loads its own output the way the host loads a gallery, and fails if one backend does not come back equal. It does not use the network.

CI (the `backend library` workflow) checks the library file itself. It decodes, every entry passes the checks a host applies, every backend round trips, every meta backend names backends the library has, and the gallery reader loads the generated gallery and sees the same backends as the library. It then generates the gallery again and fails with a message to regenerate, not to edit by hand, when `backend/index.yaml` differs. The tests in `core/gallery` run the same checks on every entry of the real backend gallery from before the library became the source, kept as golden files in `core/gallery/testdata/backend_library`, and log how many entries they covered.

## Installing from a library

A library of the [Add-ons page]({{% relref "/addons/contract-reference" %}}) can list backends too, and the backend library is one of them. A library entry installs with a pinned image digest, the signature check of the library and a size limit (8 GiB by default). The Browse tab of the Add-ons page shows each entry with its kind. See [Backends in the library]({{% relref "/addons/contract-reference#backends-in-the-library" %}}) for the entry format, the signer pattern and the install route.

## Pre-installing Backends

You can pre-install backends when starting LocalAI using the `LOCALAI_EXTERNAL_BACKENDS` environment variable:

```bash
export LOCALAI_EXTERNAL_BACKENDS="llm-backend,diffusion-backend"
local-ai run
```

## Backend Directory Format

Every backend, whether the gallery installed it into the user-managed
location or a system package shipped it, is a directory with one
required file:

- `run.sh` — the entry point LocalAI executes to start the backend.

and one optional file, `metadata.json`:

```json
{
  "name": "rocm-audio-cpp",
  "alias": "audio-cpp"
}
```

- `name` — the concrete backend name (defaults to the directory name).
- `alias` — registers this directory as a *variant* of a backend
  family. When several installed variants share an alias
  (`cpu-audio-cpp`, `rocm-audio-cpp`, ... all aliased to `audio-cpp`),
  a model config using `backend: audio-cpp` resolves to the variant
  best matching the host's capability (CUDA before Vulkan before CPU
  on an NVIDIA host, ROCm first on AMD, and so on). Each variant also
  stays individually addressable by its concrete name, e.g.
  `backend: cpu-audio-cpp` to keep VRAM free for other models.
- `meta_backend_for` — points a meta entry at a concrete backend
  directory installed next to it.

A directory without `metadata.json` is a plain backend under its
directory name. Gallery installs write this metadata automatically
(with additional bookkeeping fields such as `gallery_url` and
`installed_at`); it only needs writing by hand when packaging backends
outside the gallery.

## System-Provided Backends

Backends do not have to come from the gallery: directories under
`LOCALAI_BACKENDS_SYSTEM_PATH` (default `/var/lib/local-ai/backends`)
are discovered on every scan, using the same
[directory format](#backend-directory-format) as user-managed
backends. This is the integration point for distribution packages —
the package manager installs backends there, while gallery installs
keep living in the user-managed `LOCALAI_BACKENDS_PATH`.

One difference in error handling: a system directory with unreadable
metadata is skipped with a warning, while unreadable metadata in the
user-managed location fails the listing — a system package must never
be able to break the discovery of the user's own backends.

### Precedence between the two locations

User-managed backends always win over system-provided ones:

- **Same name in both locations** — the user-managed backend hides the
  system one entirely.
- **Family takeover** — installing *any* variant of an alias family
  into the user-managed location (e.g. from the gallery) replaces the
  whole system family: the alias resolves only among user-managed
  variants, and the system family's concrete names disappear from the
  listing. Variants of one family are versioned together; resolution
  never mixes installations of different origins within a family, and
  a stale system variant is not kept reachable.
- **Names never get hijacked** — a system variant's alias cannot take
  over a name that exists as a user-managed backend (including a meta
  backend): `backend:
  audio-cpp` keeps running the user's `audio-cpp` installation even if
  a system package later ships variants aliased to that name.

## Creating a Backend

To create a new backend, you need to:

1. Create a container image that implements the LocalAI backend interface
2. Define a backend YAML file
3. Publish your backend to a container registry

### Backend Container Requirements

Your backend container should:

1. Implement the LocalAI backend interface (gRPC or HTTP)
2. Handle model loading and inference
3. Support the required model types
4. Include necessary dependencies. Python backends are unpacked from the
   builder path into a runtime directory, so packages must be installed into
   the backend virtualenv with a regular `pip install .` / `uv pip install .`
   — not an editable (`-e`) source install. An editable finder keeps pointing
   at the vanished builder tree, and `import` fails after relocation.
5. Have a top level `run.sh` file that will be used to run the backend
6. Pushed to a registry so can be used in a gallery

{{% callout style="warning" %}}
An already-installed Python backend that was built with an editable install
(for example vllm-omni from v4.0.0) keeps that broken finder until it is
replaced with a rebuilt artifact. Reusing or renaming the unpacked directory
does not rewrite the stale path; delete or upgrade the backend so the new
site-packages copy is what runs.
{{% /callout %}}

### Getting started

For getting started, see the available backends in LocalAI here: https://github.com/mudler/LocalAI/tree/master/backend . 

- For Python based backends there is a template that can be used as starting point: https://github.com/mudler/LocalAI/tree/master/backend/python/common/template . 
- For Golang based backends, you can see the `piper` backend as an example: https://github.com/mudler/LocalAI/tree/master/backend/go/piper
- For C++ based backends, you can see the `llama-cpp` backend as an example: https://github.com/mudler/LocalAI/tree/master/backend/cpp/llama-cpp

### Publishing Your Backend

1. Build your container image:
   ```bash
   docker build -t quay.io/username/my-backend:latest .
   ```

2. Push to a container registry:
   ```bash
   docker push quay.io/username/my-backend:latest
   ```

3. Add your backend to a gallery:
   - Create a YAML entry in your gallery repository
   - Include the backend definition
   - Make the gallery accessible via HTTP/HTTPS

## Backend Types

LocalAI supports various types of backends:

- **LLM Backends**: For running language models (e.g., llama.cpp, vLLM, vllm.cpp, SGLang, transformers, MLX, the experimental [Strata engine]({{% relref "/features/engines/strata" %}}) (NVIDIA CUDA, AMD ROCm, Intel SYCL, Apple Metal, prebuilt engine packages), and [RKLLM on Rockchip NPUs]({{% relref "/features/engines/rkllm" %}}) through the cloud-proxy backend)
- **Speech-to-Text Backends**: For transcription, forced alignment and speaker diarization (e.g., whisper.cpp, parakeet.cpp, moss-transcribe.cpp, [NeMo-Speech.cpp]({{%relref "/features/engines/nemo-speech-cpp" %}}), faster-whisper, [Whisper-Medusa]({{%relref "/features/engines/whisper-medusa" %}}), FunASR/SenseVoice, NeMo, [audio.cpp]({{%relref "/features/engines/audio-cpp" %}}))
- **Text-to-Speech Backends**: For speech synthesis (e.g., piper, Kokoro, VibeVoice, Qwen3-TTS, [NeMo-Speech.cpp]({{%relref "/features/engines/nemo-speech-cpp" %}}), [audio.cpp]({{%relref "/features/engines/audio-cpp" %}}))
- **Sound Generation Backends**: For music and audio generation (e.g., ACE-Step, [audio.cpp]({{%relref "/features/engines/audio-cpp" %}}))
- **Sound Classification Backends**: For sound-event classification / audio tagging - identifying everyday sounds like baby cry, glass breaking, alarms (e.g., ced.cpp)
- **Image & Video Generation Backends**: For diffusion and audio-conditioned avatar models (e.g., stable-diffusion.cpp, diffusers, vLLM-Omni, [MLX-Video on Apple Silicon]({{%relref "/features/vision/video-generation" %}}), [LongCat-Video]({{%relref "/features/vision/video-generation" %}}), [MiniMax-H3 on stable-diffusion.cpp]({{%relref "/features/vision/video-generation" %}}))
- **3D Generation Backends**: For image-to-3D mesh generation ([trellis2.cpp]({{%relref "/features/vision/3d-generation" %}}) — Microsoft TRELLIS.2, producing GLB assets with PBR textures)
- **Vision & Detection Backends**: For object detection, segmentation, depth, and face/voice recognition (e.g., rf-detr.cpp, locate-anything.cpp, sam3.cpp, insightface)
- **Time-Series Backends**: For quantile forecasting of numeric series ([timestral.cpp]({{%relref "/features/text/forecast" %}}), Mistral's Timestral models)
- **Audio Processing Backends**: For voice activity detection and audio enhancement (e.g., Silero VAD, LocalVQE, [audio.cpp]({{%relref "/features/engines/audio-cpp" %}}))
- **Source Separation & Voice Conversion Backends**: For splitting a mix into named stems (vocals, drums, bass) and for converting speech or singing to a target voice (e.g., [audio.cpp]({{%relref "/features/engines/audio-cpp" %}}))
- **Utility Backends**: For reranking, PII/NER token classification, fine-tuning, quantization, and vector storage (e.g., rerankers, privacy-filter.cpp, TRL, local-store, valkey-store, qdrant-store)

See the [Backend & Model Compatibility Table]({{%relref "/models/compatibility-table" %}}) for the full catalog.

### DS4 request cancellation

The DS4 backend stops inference when a client cancels or disconnects, including
when a streaming response can no longer be written. Already-streamed chunks
cannot be retracted; DS4 does not flush incomplete buffered parser state or
persist an abandoned request to the disk KV cache. Cancellation is cooperative:
DS4 checks it at safe prompt-prefill and decode-loop boundaries, so a GPU kernel
already in flight may finish before the request stops.

### llama.cpp request cancellation

The llama.cpp backend stops a streaming generation as soon as the response can
no longer be written to the client, not only when the RPC is formally cancelled.
A stream never recovers once a write fails, so the backend treats the first
failed write as final and returns, which releases the slot the generation held.

This matters most for a model configured without a generation cap. With
`max_tokens: 0` and a large `context_size`, an abandoned request that keeps
decoding occupies its slot until it reaches the context limit — tens of minutes
on a large model — and every other request for that model queues behind it. A
couple of abandoned requests is enough to make a healthy node look wedged.

Cancellation is cooperative and checked between decoded results, so a batch
already in flight may finish before the request stops.

{{% callout style="tip" %}}
A generation cap is still worth setting. Cancellation only helps once a client
has actually gone away; a client that waits receives the full context worth of
tokens. Set `max_tokens` on the model config, and keep `repeat_penalty` above
`1` so a repetition loop terminates on its own.
{{% /callout %}}

### Motion capture

- [gem-x.cpp](https://github.com/localai-org/gem-x.cpp): live SOMA-77 and SMPL-24 human poses on CPU/Vulkan. See [Motion Capture]({{% relref "/features/vision/motion" %}}).

### Remove an installed backend from one node

In distributed mode, expand a row in **Backends → Installed** to see its node
installations. Turn on **Variant builds** and **Development builds** under
**Filters** when needed to reveal concrete builds such as `cpu-my-engine-development`. Removal uses
that exact row name, not a generic alias or the recommended build.

**Remove from worker-alpha…** asks for confirmation naming both the backend and
node. Only that installation is affected; other nodes and sibling builds remain.
Removal can stop models using the backend on that node. Cancel, Escape, or clicking
outside the dialog sends no removal request. A target-node URL restricts the
available actions to that node; it never falls back to a different worker.

The page checks each expanded node's actual inventory. System and meta backends,
unknown protection metadata, absent builds, and unavailable or draining nodes
cannot be removed through these controls. A system installation on one node does
not prevent removal of a user-managed installation on another. Inventory and
node status are checked again when you confirm; server-side protection still
applies. Retry inventory explicitly if it could not be loaded.

A removal error is scoped to the node and backend. Refresh and confirm again
before retrying; an API failure does not guarantee that nothing changed. If
removal succeeds but refresh fails, the page keeps the last successful installed
list and warns that it may be stale. **Retry** refreshes data without repeating
the deletion. Ordinary refresh failures do not report deletion success. Targeted
installation and removal controls block conflicting requests for the same node
and build, including while the post-removal refresh is pending. Generic catalog
removal also stays blocked during targeted installation, scoped removal, and
refresh, even if the URL
target is cleared. Its confirmation rechecks current row conflicts before
deleting. Standalone deletion and its system-backend protection are unchanged.
