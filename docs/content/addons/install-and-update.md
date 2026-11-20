+++
title = "Install and update add-ons"
description = "The Add-ons page, the official library, installing from a file, library sources and the backends in the library."
weight = 12
+++

Find an add-on in a library, install it, and install one from a file. This page also says where libraries come from and which backends a library offers. An administrator does all of this on the **Add-ons** page. For the short version, see the [Add-ons user guide]({{% relref "/addons/user-guide" %}}).

## The Add-ons page

**Installed** lists the add-ons you installed in groups: chat apps, tools and
services, and media and models. The group comes from what the add-on gives
LocalAI. An add-on that serves a messaging connection is a chat app. One that
gives chat tools is a tool. One that has only a page is under media and models.
Each row shows the icon, the name, one state, one line of what the add-on does
and the one action that fits the state: **Open**, **Update** or **Set up**.
**Pin**, **Details** and **Remove** are in the menu of the row.

When something needs setup, one strip above the list says so. It names each
add-on that failed to start and each add-on with a connection that cannot answer
yet, for example because no token is saved. **Set up** opens the details of that
add-on.

The details open beside the list. They show what the add-on can do, its
connections, who made it and how it was checked. **Remove** is the last item.
The connections show the agent that answers and who it accepts, in words.

**Find add-ons** shows the library as cards, with a search field and a category
filter. Each card has one control: **Install**, **Installed** or **Update to**
a version. The category comes from what the library says the add-on gives:
tools, a page, or the id of a first-party add-on. The filter narrows the page
of results that is on screen.

### Add-on icons

Every add-on has an icon. The page picks it in this order:

1. **The icon file of the add-on.** Set `ui.icon` in the manifest to a path in
   the package. The file is a PNG, WebP or JPEG of at most 256 KB, never an
   SVG. Make it 256 by 256 pixels, a rounded tile with the mark drawn on it, so
   that it reads on a light page and on a dark page. First-party add-ons commit
   this file as `ui/icon.png`.
2. **A generated tile.** An add-on built with the shared builder
   (`addons/internal/uibuild`) and without an icon file gets a tile of its own
   colour with a white line glyph. Pass `glyph` to the builder to choose one of
   the glyphs in `addons/internal/uibuild/glyphs.mjs`.
3. **A drawn tile in the page.** The host serves an icon file only for an
   installed package. For an add-on that is not installed, or whose file does
   not load, the page draws a tile with a glyph of its own icon set for the
   first-party add-ons it knows, and the first letter of the name for any other.

The page never loads an icon from another host.

The first-party add-ons for services (Slack, Telegram, Discord, GitHub, Gmail,
Google Drive, Home Assistant and Jellyfin) use the logo mark of the service on their tile, taken
from [Simple Icons](https://simpleicons.org) (CC0 data) and drawn in white on
the add-on colour. The mark identifies the service the add-on connects to. The
trademark stays with its owner, and the mark does not mean that the owner made
or endorses the add-on. Record the source and version in `ui/ICON-SOURCE.md`
next to the icon. When no mark is available, as for PiKVM, ship a generated
tile with a generic glyph. For a third-party add-on, use a company mark only
when you have the right to.

## The official library

On the first start, when the owner has no `sources.json` in the `addons` folder yet,
LocalAI registers one library with the ID `official`. It is a YAML index of the
first-party add-ons (Tables, Voice, Face, Fine-tuning, Quantization, Studio, Talk, Simple Office,
Sandbox and Web, plus the other first-party add-ons including Google Drive and Gmail). The index has one entry for each enabled platform of each add-on, so an
install on Linux (amd64 or arm64), on macOS (Apple silicon or Intel) and on Windows (amd64) finds the add-ons for
its own platform. The Sandbox add-on runs on Linux, and on macOS with Apple silicon through Virtualization.framework. It has no package for macOS on Intel. Its Windows package runs the Linux add-on in WSL 2, and it stays disabled until a Windows runner with WSL shows its native check passing, so the
official library has no Sandbox entry for Windows yet.

Which index the library reads depends on the build:

| Build | Index | Entries | Signer the library requires |
|---|---|---|---|
| A release (the version is a `v<semver>` tag) | `addons-index.yaml`, an asset of that GitHub release | Pinned by digest to the packages the release built | The package workflow run of that tag (`refs/tags/v<semver>`) |
| Any other build (master, a source build, a commit after a tag) | `gallery/addons/index.yaml` on the master branch | The mutable `development` builds | The package workflow run on master (`refs/heads/master`) |

```
https://raw.githubusercontent.com/mudler/LocalAI/master/gallery/addons/index.yaml
https://github.com/mudler/LocalAI/releases/download/v<version>/addons-index.yaml
```

So a release installs the immutable, tag-pinned packages that the same release built and
signed, and never a development build signed by master. The first start writes the index
and signer of the running build into `sources.json`. After an upgrade, the next start moves
the `official` library to the index and signer of the new build; a library that you pointed at
another URL is left as it is.

The index has the same format as the other libraries (see
[Catalog document and limits]({{% relref "/addons/contract-reference/packages-and-runtime#catalog-document-and-limits" %}})). A package that the named
workflow run did not build and sign is refused at install.

`gallery/addons/index.yaml` is generated, never edited by hand:

```bash
sh scripts/addons/catalog.sh --channel development \
  --repository quay.io/go-skynet/local-ai-addons --out gallery/addons/index.yaml
```

CI runs the same command with `--check` and fails when the committed file is stale, for
example when a platform is enabled in `addons/<id>/package/platforms.yaml` or a manifest
changes. The release index is rendered by the add-on package workflow when a `v<semver>` tag
is pushed and attached to the release when it is published (see
[Preparing a release](https://github.com/mudler/LocalAI/blob/master/.agents/preparing-a-release.md)).

### Add-ons that are not built in

Studio, Talk, Voice, Face, Fine-tuning, Quantization, Simple Office and Web are not part of the LocalAI
binary or image. A fresh install has none of them. The old links to the pages they replaced
open the add-on page, which says the add-on is not available
until it is installed.

To get them on a new install:

1. Sign in as an administrator (with authentication off, you already are one) and open
   **Add-ons**, then **Find add-ons**. The list is the official library for this machine's
   operating system and architecture.
2. Pick the add-on and choose **Install and turn on**. The install checks the signature
   against the library policy before anything runs. Studio is the usual first one: it holds the
   image, video, 3D, speech, sound and audio tools.
3. The add-on then appears in **My add-ons**. Use **Show in sidebar** to pin it.

An install needs the network to reach the library and the package registry. A machine
without it can run a private library (see [Private catalog service]({{% relref "/addons/contract-reference/packages-and-runtime#private-catalog-service" %}})) that
mirrors the packages. `--addons-official-library=false` starts without the official library, and
`--addons-enabled=false` turns the add-on owner off, so nothing can be installed.
Installed add-ons live in the `addons` folder under the data path, so keep that path on a
volume in a container, or the add-ons must be installed again after the container is replaced.

The first start writes `sources.json`, and from then on that file is the record of
your choices. If you remove the `official` library (in the Add-ons page or through the
source routes), it stays removed, and a `sources.json` you already have is never
changed. To start without it, set `--addons-official-library=false` before the first
start. To get it back, add a source with the URL above, or enable it again (see
[Library sources](#library-sources)).

### Upgrading: Studio and Talk are add-ons now

Studio (image, video, 3D, speech, sound and audio) and Talk (realtime voice) used
to be pages of LocalAI. They are add-ons now, and two things change at the upgrade:

- **Nothing is installed for you.** No add-on is preinstalled. Studio and Talk
  appear only after an administrator installs them from the library (see
  [Add-ons that are not built in](#add-ons-that-are-not-built-in)). Until then the
  old links open a page that says the add-on is not available.
- **Users need the `addons` feature.** With authentication on, every add-on page is
  behind the `addons` feature, which is off by default for a user. A user who had
  the image, speech or realtime features but not `addons` loses Studio and Talk at
  the upgrade. The upgrade does not grant `addons` to anyone. An administrator
  always has it.

To restore access:

1. Install Studio and Talk: **Add-ons**, **Find add-ons**, then **Install and turn
   on** for each. A machine with no access to the network installs them from a
   private library that mirrors the packages (see
   [Private catalog service]({{% relref "/addons/contract-reference/packages-and-runtime#private-catalog-service" %}}) and
   [Library sources](#library-sources)).
2. Grant the `addons` feature to each user who should keep them, in
   **Settings → Users and keys** (see [Chats permissions]({{% relref "/administration/chats-deployment#permissions" %}})
   for the same screen). The calls the pages make still need the user's own
   features: `images` for an image, `audio_speech` for speech, and so on.

With authentication off there are no user features: the one person is an
administrator and sees every installed add-on.

**Release notes.** The paragraph below is ready to copy into the release notes:

> **Studio and Talk are add-ons.** Studio (image, video, 3D, speech, sound and
> audio) and Talk (realtime voice) moved out of LocalAI into add-ons. Nothing is
> preinstalled: after the upgrade an administrator installs them from **Add-ons →
> Find add-ons** (an air-gapped host uses a private library that mirrors the
> packages). With authentication on, the add-on pages need the `addons` feature,
> which is off by default and is not granted at the upgrade, so users who used
> Studio or Talk lose access until an administrator grants `addons` to them in
> **Settings → Users and keys**.

## Install from a file

You can install an add-on from one zip file, with no library and no registry.
Use it for an add-on you built yourself, for a build you want to try before it
is published, and for a host that cannot reach a library.

A zip holds one add-on for one platform. It holds the files of the add-on
package: `manifest.json`, the executable, the UI files and icon, the skills and
the files the platform needs at run time. It also holds `zip-manifest.json`,
which lists each file with its sha256 and size. The file name is
`<id>-<version>-<os>-<arch>.zip`. A `.sha256` file in `sha256sum` format goes
with it.

### Trust

Nothing signs a local file, so LocalAI cannot tell who made it. The zip checks
the files against the digests it lists. That finds a damaged or mixed-up file.
It does not prove where the file came from. You choose what you install: an
add-on from a file is trusted as far as you trust the file. LocalAI does not try
to protect you from a hostile add-on that you install on purpose.

The install dialog shows, before you confirm:

- the sha256 of the whole file, the add-on ID and version, the platform and the
  host API range of the manifest;
- the host operations the add-on asks for, in the same step as for a library
  add-on. A local file is never official, so the official-only blanket approval
  grants it nothing;
- the notice "Local file, unsigned".

The receipt of the install records the source `local-file` and the origin
"local file, unsigned". The Add-ons page, the Details of the add-on and the
Host access page show that notice for as long as the add-on is installed.
Compare the sha256 with the `.sha256` file that came with the zip, or with
`sha256sum` on the file, when you got it from someone else.

### Install

On the Add-ons page, an administrator selects **Install from file** and picks
the zip. The server checks the file and shows the install dialog. Then:

- A new add-on is installed and turned on, as for a library add-on.
- A file with the same ID as an installed add-on and a higher version is an
  upgrade. It goes through the same generations, backup, migration and recovery
  as every upgrade. It does not change whether the add-on is on or off.
- A file with the same version as the installed one is refused.
- A file with a lower version is refused unless you tick the confirmation in the
  dialog. An older version may not read data that a newer version wrote. The
  data format rules of recovery still apply to it.
- An add-on that came from a library and one that came from a file have
  different trusted identities. When the add-on has retained data, the owner
  refuses a switch between them. Uninstall it first and keep the data, then
  install the other one.

From a terminal, `local-ai addons inspect <file>` checks a zip on your machine
and prints what it holds. `local-ai addons install <file>` checks it, uploads it
to a running server and prints the link of the page that finishes the install:

```bash
local-ai addons install dist/addons/web-0.1.0-linux-amd64.zip \
  --target http://localhost:8080 --api-key "$LOCALAI_API_KEY"
```

The command never installs by itself. The install needs the host access review
and the confirmation of an administrator in their own browser session, like
every change of what an add-on may do on the host. `--target` and `--api-key`
come from `LOCALAI_TARGET` and `LOCALAI_API_KEY` when you leave them out.

### When sign-in is off or the grants are locked

An unsigned file installs only where UI host grants work. While sign-in is off,
and when a deployment locks the UI grants, the owner refuses to stage a file
(`403 locked_by_deployment`) and the dialog says so. Turn the UI grants on
knowingly with `--addon-host-ui-grants-without-auth`, or turn sign-in on. A
package from a file replaces a package from another library or signer only with
the approval of the dialog, like any package from another source.

### What the server does with the file

The routes are administrator-only. `POST /api/addons/files` takes the zip as a
multipart form with one part named `file`. An administrator's session or API key
can stage a file, because staging runs nothing and changes no add-on.
`GET /api/addons/files/{fileId}` reads a staged file and
`DELETE /api/addons/files/{fileId}` removes it. The answer of the upload has the
file ID, the sha256, the add-on ID, title, version, platform and API range, the
host operations the manifest asks for, how the file relates to the installed
add-on (`new`, `upgrade`, `same` or `downgrade`) and the origin.

The owner keeps the file in a private folder of its own storage for one hour.
The file belongs to the user that uploaded it, and no other user can read or
install it. The owner deletes the folder at start and the file after the
install. The install is the host access `preview` and `install` with
`sourceId` set to `local-file` and `version` set to the file ID. They keep every
rule of the host access routes: only the administrator's own browser session can
install, the preview nonce and the digest bind the install to this file, and the
install body has `downgrade: true` to confirm an older version. The plain
`dispatch` install does not take a file. No MCP tool maps to these routes.

The owner refuses a file, with the rule it broke as the error type, when:

| Type | Rule |
|---|---|
| `bad_zip` | It is not a zip, it is damaged, it is empty or it uses an unsupported compression. |
| `unsafe_path` | A path is absolute, has `..`, a backslash, a drive letter or a NUL, is not clean, or repeats (also when only the case differs). |
| `unsupported_entry` | An entry is a link or a special file. |
| `too_large` | The zip, or the files together, are over the add-on size limit (512 MiB). |
| `too_many_files` | It holds more than 10000 files. |
| `compression_ratio` | A file over 1 MiB expands more than 200 to 1. |
| `bad_manifest` | `zip-manifest.json` is missing or invalid, or `manifest.json` does not match it. |
| `digest_mismatch` | A file is missing, extra, or has another size or sha256 than the list. |
| `wrong_platform` | The zip is for another operating system or architecture than the host. |
| `bad_mode` | A file other than the declared executable has an executable bit. |

The add-on package rules apply after that, as for a library package: the
manifest against the host, the executable, the UI files, the icon and the skills.
The owner sets the file modes itself.

In a deployment with a separate owner, you upload to any frontend. The frontend
keeps no copy: it sends the bytes to the owner in chunks of 1 MiB over the
signed carrier, and the owner enforces the size limit while it receives them.
The owner must be a version that knows the file calls, or the page says that the
owner is unavailable.

### Build a zip

`make addons-zip` builds the zip files from the add-ons in the repository. It
calls `scripts/addons/package.sh` to build and verify the OCI package of each
add-on and platform, then writes the zip from that verified package. There is no
second build path: the zip holds the same files as the registry package.

```bash
# One add-on, for the platform of this machine
make addons-zip ADDON=web

# Every add-on, for the platform of this machine
make addons-zip

# Other platforms need the native executables
make addons-zip ADDON=web PLATFORMS=linux/amd64,darwin/arm64 \
  ADDON_EXECUTABLES=/path/to/executables
```

The files go to `dist/addons` (`ADDONS_ZIP_DIR` changes it):

```text
dist/addons/web-0.1.0-linux-amd64.zip
dist/addons/web-0.1.0-linux-amd64.zip.sha256
```

| Variable | Meaning |
|---|---|
| `ADDON` | One add-on ID. Without it, every add-on that has a platform file. |
| `PLATFORMS` | A comma-separated list of `os/arch`. The default is the platform of this machine. |
| `ADDON_EXECUTABLES` | For a platform other than this machine: a folder with `<os>-<arch>/<executable name>`, built natively. |
| `ADDON_VERSION` | The version. The default is the version in the manifest of the add-on. |
| `ADDON_LOCAL_CHECK` | Set it to build a platform that the platform matrix has not enabled yet. |
| `ADDON_ROOT` | The folder of an add-on that lives in another repository. See [Build an add-on in your own repository](/features/addon-sdk/). |
| `ADDON_EXECUTABLE` | A built executable to pack, instead of building one. Use it with one platform. A relative path is relative to the directory where you start `make`. |
| `ADDONS_ZIP_DIR` | The output folder. |

The build needs Go and Node.js, as `package.sh` does; the Tables add-on also
needs a C toolchain. Without `ADDON`, the Sandbox add-on is skipped unless
`SANDBOX_GUEST_IMAGE` is set, because its executable needs the reference of its
guest image (see `scripts/addons/package.sh`). The same layout gives the same
zip bytes, so you can rebuild a file and compare the sha256.

To build and install one add-on:

```bash
make addons-zip ADDON=web
local-ai addons install dist/addons/web-0.1.0-linux-amd64.zip
```

## Library sources

A library source is a place LocalAI reads entries from. There are three kinds:

| Kind | What it provides | Read by |
|---|---|---|
| `addon` | Add-ons, as a library index | The Add-ons catalog |
| `backend` | Backends, as a library index or a backend gallery index | The Backends page, and the add-on catalog for a library |
| `model` | Models, as a library index or a model gallery index | The model gallery |

All sources of all kinds are in one store, `addons/sources.json` under the data path.
`local-ai library` (see the [CLI reference]({{% relref "/reference/cli-reference" %}}#library-sources)), the
HTTP routes below and the Sources tab work on the same store with the same code, so
a change made in one place shows in the others.

A source can be a default: one that LocalAI ships (the official add-on library, the
model library and gallery index, and the backend library and gallery index). Of the two
model sources and the two backend sources only one is on by default, the one the build
reads (see [Which source a build reads]({{% relref "/models/backends#which-source-a-build-reads" %}})). A default is marked `default`. If
you remove a default, LocalAI keeps a record of the removal (a tombstone, `removed:
true`), so the default does not come back at the next start. Turn the source on again
to bring it back. A source you added is deleted when you remove it.

### The Sources tab

The Sources tab of the Add-ons page lists the sources of all three kinds. Use the kind
filter to show only add-ons, backends or models. Each source shows its kind, a Default
badge for a shipped default, and its state: enabled, disabled, removed or, while the
official libraries are off, off. Administrators can add a source (kind, format, name and
address), turn a source on or off, and remove it. A removed default stays in the list
with a Restore button. An add-on source can also be edited, to set its signature policy
and credential reference, and refreshed. Backend and model sources have no signature
policy of their own.

### Conflicts

When two libraries list the same entry, LocalAI does not choose silently:

- A source you added wins over a default. The default is ignored for that entry.
- Two sources you added that list the same entry are a conflict. LocalAI refuses to
  install or resolve that entry until you turn one of the sources off or remove it.

The list shows the message next to each source that clashes. Only sources that LocalAI
fetches as a library index can clash, so gallery-format sources never show a conflict.

### Use official libraries

The setting `official_libraries` (default on) switches all default sources of all
three kinds at once. In the Settings page it is the toggle **Use official libraries**,
in the group Backends and galleries. On the command line it is
`--official-libraries` (`LOCALAI_OFFICIAL_LIBRARIES`).

When it is off, LocalAI does not read the default sources. The default add-on library,
model gallery index and backend gallery index are not used for search, install or
update checks. They stay in the store, still marked `default`, and the Sources tab shows
them as off (`suspended: true` in the routes). Each source keeps its own enabled state, so turning the
setting back on restores exactly what was there. Sources you added are not affected. The
change applies at once, with no restart. On a frontend the switch is read-only: the
owner's value applies, and a change there reaches the frontends within 60 seconds.

### Source routes

The routes are for administrators. A caller who is not an administrator gets `403`, and a
caller who is not signed in gets `401` when authentication is on. They are not add-on
routes, so they do not need the add-ons owner to run. On a frontend they go to the owner
(see [Frontend limits]({{% relref "/addons/contract-reference/owner#frontend-limits" %}})).

| Method and path | What it does |
|---|---|
| `GET /api/library/sources` | List every source of all kinds, tombstones included |
| `POST /api/library/sources` | Add a source: `{"kind": "addon\|backend\|model", "url": "...", "id": "...", "name": "...", "format": "library\|gallery"}`. `id`, `name` and `format` are optional. The same kind and URL twice, or a taken id, is `409`. An add-on library at an `http` address of this machine is `400`: the owner refuses packages that do not come over `https`, so such a library would never list anything |
| `POST /api/library/sources/{id}/enable` | Turn a source on. This also brings back a removed default |
| `POST /api/library/sources/{id}/disable` | Turn a source off without removing it |
| `DELETE /api/library/sources/{id}` | Remove a source. A default leaves a tombstone |

The list answers `{"sources": [...], "officialLibraries": true}`. Each source has `kind`,
`id`, `name`, `url`, `format`, `enabled` (what LocalAI does with it), `default`, `removed`,
`suspended`, `conflict` (the message, when there is one), `policy` and, for an add-on
source, `refresh` (the last fetch). A source that does not exist is `404`. Changes to
model and backend sources apply to the running server. No MCP tool maps to these
routes.

## Backends in the library

A library lists more than add-ons. Every entry has one schema and an optional `kind`
field. The kinds are:

| Kind | What the entry installs | State |
|---|---|---|
| `addon` | A supervised add-on package. It is the default: an entry without `kind` is an add-on, so every index written before kinds existed stays valid. | Supported |
| `backend` | A backend image for the backends directory. | Supported. The backend library is the source of the backends. |
| `model` | A model. | Reserved. No reader installs it yet. |

The backend library (`gallery/backends/index.yaml`) is the source of truth of the
backends, the way the model library is for the models. The backend gallery
(`backend/index.yaml`) is a generated copy of it. A tagged build before the cutover reads
the gallery by default; a build from master reads the library. See
[The backend library](#the-backend-library). A library entry of kind `backend` can also be
installed by digest from the Find add-ons tab, as described below.

### How a reader treats an unknown kind

A reader keeps the entries of the kinds it knows and skips every other entry. It does
not read the fields of a skipped entry, so a newer kind can carry fields this version has
never seen. The index is not refused. A skipped entry is reported in two places:

- the refresh result and the state of the library have `skipped` (a count) and
  `skippedKinds`. The Sources tab shows them;
- the refresh writes a warning to the log.

A kind that is not a plain lowercase name, or that is not text, is a malformed entry
and the index is refused. An unknown field on an entry of a known kind is still refused.
Search lists add-ons unless the caller asks for a kind: `catalog-search` takes an optional
`kind` argument (`addon`, `backend` or `all`).

### A backend entry

```yaml
- kind: backend
  id: cpu-local-store           # the backend name, also its directory name
  alias: local-store            # optional: the name a model uses
  name: cpu-local-store
  description: Local Store is a local-first vector database.
  license: MIT
  icon: https://example.com/local-store.png
  urls:
    - https://github.com/mudler/LocalAI
  tags: [vector-database, CPU]
  version: 0.0.0-latest.0a1056fd8633
  channel: latest
  schemaVersion: 1
  artifact: oci://quay.io/go-skynet/local-ai-backends:latest-cpu-local-store
  digest: sha256:0a1056fd863328adf9d1097808aa2b52f68c4ca2e5385100a754755b3ca6218f
  mirrors:
    - oci://localai/localai-backends:latest-cpu-local-store
  capabilities:
    default: cpu-local-store
    metal: metal-local-store
  os: linux
  arch: amd64
```

One entry is one image for one platform, so a backend built for several platforms has
several entries with the same `id`. The reader is strict: an unknown field on an entry of
kind `backend` is refused. These fields are for a backend only and an add-on entry that sets
one is refused:

| Field | What it holds |
|---|---|
| `schemaVersion` | The version of the shared entry header. Absent means 1. A host refuses a higher one. |
| `license`, `urls`, `tags` | Display text, as in the gallery. `urls` are `http` or `https`. |
| `icon` | The URL of the icon, `https` only. The add-on `ui.icon` stays a path in the package. LocalAI never fetches the icon: the browser loads it. |
| `mirrors` | Fallback images of the same backend, tried in order. Each is an `oci://` image. The install pins a mirror with the digest of the artifact. |
| `capabilities` | A map from a host capability (`default`, `nvidia`, `metal`, ...) to the `id` of the concrete backend that serves it. |
| `mutable` | The artifact is a moving tag, such as a development image. The entry has no `digest`. |

The `digest` is the digest of the image for the platform. The install uses the image by
digest and never by tag. A backend entry must use an `oci://` artifact. It has no main
page or `ui.icon`, and only a backend entry can set `alias`. The backend name is a directory
name: it has no `/` and no `..`.

**Meta backends.** An entry with `capabilities` and no `artifact` is a meta backend, such
as `llama-cpp`. It has no `digest`, `os`, `arch` or `mirrors`, and it is listed for every
platform. Installing it installs the backend its capability names for this host, in the
same library and the same channel, pinned and verified like any other. A meta backend that
names nothing for the host, or names a backend the library does not have, is refused.

**Development images.** A `master-` image is rebuilt all the time, so the library cannot
pin it. Its entry keeps the tag, has `channel: development` and `mutable: true`, and has no
`digest`. The install resolves the tag to a digest, verifies that image, and records the
digest it installed in `metadata.json`.

### Signer identity pattern

The signature policy belongs to the library, as for add-ons. A backend library can name
the signer with `identityRegex` instead of an exact `identity`, so images that the merge
workflow signed on master and on a tag both verify:

```json
{
  "requireSignature": true,
  "issuer": "https://token.actions.githubusercontent.com",
  "identityRegex": "^https://github\\.com/mudler/LocalAI/\\.github/workflows/backend_merge\\.yml@refs/(heads/master|tags/.+)$"
}
```

Set `identity` or `identityRegex`, not both. The pattern must be one expression that
starts with `^` and ends with `$`. A pattern with a top-level alternation, such as
`^a|b$`, is refused, because one branch would match a part of the identity only. An
add-on install refuses a library policy that has a pattern: add-on libraries name the
signer exactly.

### Install a backend from a library

An administrator installs a backend from the Find add-ons tab of the Add-ons page. The entry
shows its kind. The page sends the install to the backend route, not to the add-on
lifecycle. The page installs an add-on and turns it on in one action (Install and turn
on); the install itself leaves an add-on disabled. A backend has no on or off: it is
ready to use when the install ends, and its button says Install.

The route is `POST /backends/library/install`. It is for administrators and exists where
the libraries are: on the instance that runs the add-on owner, and on a frontend, which asks the
owner to resolve the entry and installs the backend on its own node:

```bash
curl -X POST http://localhost:8080/backends/library/install \
  -H 'Content-Type: application/json' \
  -d '{"sourceId":"official-backends","name":"cpu-local-store","version":"latest"}'
```

The request names the entry and nothing else. The library resolves the image, the digest
and the policy, so a request cannot change them. The install:

1. resolves the entry of kind `backend` for this platform;
2. refuses it when `--require-backend-integrity` is on and the library does not require a
   signature;
3. pins the image by digest, verifies the signature before it fetches any layer and refuses
   an image over the size limit (the layers are summed from the manifest);
4. installs with the same code as the gallery, so the backend lands in the same directory
   with the same `metadata.json`, plus `library`, the ID of the library it came from.

The request answers at once with a job, the same kind of job the backend gallery uses:
`{"id": "...", "status_url": ".../backends/jobs/<id>"}`. Read `GET /backends/jobs/<id>` to
follow it. The `phase` of the job is the step it is in, in this order: `resolving`,
`verifying` (only when the library requires a signature), `size`, `fetching` (with
`progress` and the sizes of the fetch) and `writing`. A job that ends has `processed` set.
It ends with the phase `done` and `message` `completed`, or with `error` set and the
`phase` of the step that failed. The Find add-ons tab shows these steps. Refusals that depend on
the entry arrive this way: an unknown entry, a name installed from another origin (a
gallery, another library or a manual install; nothing changes), an image over the size
limit, a library that strict mode refuses. The request itself answers `400` when it does
not name an entry and `409` when the same backend is already being installed. Installing
again from the same library is a reinstall.

### Library backends on the Backends page

The Backends page marks a backend that came from a library with the library ID, and the
detail view shows the digest that `metadata.json` pinned. A backend of a gallery has no
mark, and the gallery update check is the one of the Updates page.

When the cached index of the library lists a different build than the one installed, the
backend shows "Update available" and an Update button in its row. The check is
`GET /backends/library/updates`. It reads the index the last refresh cached, so it does
not use the network, and it installs nothing. The build to compare is the one in the
channel of the installed version. If the library no longer lists that version and has
more than one channel, the check says nothing instead of guessing. The Update button
starts the same install as above, with the same steps, for the version the check names.
There is no automatic upgrade.

The LocalAI Assistant and `local-ai mcp-server` expose this check as the read-only
`list_backend_library_updates` tool. Taking an update stays a confirmed
`install_library_backend` call.

A library install touches only the node that serves the request. In a distributed
deployment the backend is installed on that node only; the per-node routes of the backend
gallery are unchanged.

### Size limits

The limit is on the compressed size of the package or image, by kind:

| Kind | Default | Configure |
|---|---|---|
| `addon` | 512 MiB | No |
| `backend` | 8 GiB | `--addons-backend-max-bytes` |

### The backend library

`gallery/backends/index.yaml` is the backend library. It lists every backend of the
backend gallery: 1088 backends (934 concrete backends and 154 meta backends) in 1240
entries, because a backend built for several platforms has one entry for each. LocalAI
registers it on the first start as the source `official-backends`, next to the backend
gallery `localai-backends`, and a source you remove stays removed. To add it again later,
use the Sources tab with this URL and the policy above:

```
https://raw.githubusercontent.com/mudler/LocalAI/master/gallery/backends/index.yaml
```

The library is the source of truth. To add or change a backend, change the library; see
[Backends]({{% relref "/models/backends#the-backend-library-and-the-generated-gallery" %}}).
The digests are never edited by hand. Pin them again after the backend images are rebuilt;
this is the only command that reads the registry:

```bash
go run ./scripts/backends/cmd/backendcatalog \
  --library gallery/backends/index.yaml --repin
```

A release image that the registry has no image for when the library is rendered is marked
`mutable` and has no digest, and the command lists it. It installs by its tag until it is
pinned.
