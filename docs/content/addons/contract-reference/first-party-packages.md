+++
title = "First-party packages and release channels"
description = "The first-party packages and the release channels, with Office and Web."
weight = 110
+++

Look up the first-party packages, their release channels, and the Office and Web add-ons. The Sandbox settings are in [Sandbox backends, profiles and network]({{% relref "/addons/catalogue/work/sandbox-settings" %}}). This page is part of the [Add-on contract reference]({{% relref "/addons/contract-reference" %}}).

## First-party package and release channels

The first-party add-ons (Tables, Voice, Face, Fine-tuning, Quantization, Studio and Talk) are each distributed as an OCI image of one platform for each
supported operating system and architecture, joined by an image index. The host
installs it as any other OCI package: it extracts layers into a private directory and
starts the executable as a managed process. No container runtime runs the add-on.
Docker or ORAS only construct and move the distribution.

### Package format

`sh scripts/addons/package.sh --layout <dir> --version <semver> --platform <os/arch>`
writes a local OCI layout with one image and a `receipt.json` of its digests. The tool
never pushes, tags, signs or logs in.

Add `--config addons/<id>/package/platforms.yaml` to package another add-on. The script
reads the add-on name, the executable and the UI directory from that file and, without
`--executable`, builds `./addons/<id>/cmd/<executable>` for the host platform. Tables is
built with `CGO_ENABLED=1`. The other add-ons are pure Go and are built with
`CGO_ENABLED=0`, so their executables need no C toolchain on any platform. The default
configuration is the one of Tables.

| Part | Value |
|---|---|
| Image manifest | OCI image manifest, `artifactType` `application/vnd.localai.addon.package.v1`. |
| Annotations | `io.localai.addon.id`, `io.localai.addon.version`, `io.localai.addon.platform`, `org.opencontainers.image.version`, and `org.opencontainers.image.revision` when the commit is known. |
| Config | OCI image config with the `os` and `architecture` of the platform. No history, no timestamp. |
| Layer `executable` | The entrypoint (mode 0755) and the runtime files the platform needs (mode 0644), as `application/vnd.oci.image.layer.v1.tar+gzip`. |
| Layer `ui` | Only when the manifest declares a UI: every file under the directory of `ui.entry`. The declared entry, style sheets and icon are required. The layer is absent otherwise. |
| Layer `manifest` | `manifest.json` with `version`, `os` and `arch` set to the release version and platform. |
| Image index | One entry for each enabled platform, `artifactType` `application/vnd.localai.addon.package.index.v1`, with the version and commit as annotations. |

Every part is deterministic: the same inputs give the same digest. A package is rejected,
before it can be published, when:

- the entrypoint, a declared UI asset or a required runtime library is missing;
- the manifest, the image config, the annotations or the receipt name another platform
  or version, or the artifact type differs;
- a layer has an unexpected media type, role, order or file, or a blob does not hash to
  its digest;
- a file has set-id, sticky or group/world-write bits, a non-entrypoint file is
  executable, the entrypoint is not, or a path is absolute, escapes the root, repeats or
  is not a regular file;
- the UI bundles its own React runtime (the host provides React through the UI SDK);
- the icon is not an image of its declared type.

`addonpkg extract` restores a verified layout to a directory with the validated
executable mode. The installer applies the same path rules and ignores the modes in the
archive other than the executable bit.

### Release platform matrix

Each first-party add-on has its own `addons/<id>/package/platforms.yaml`. It lists the
platforms of that add-on. A platform is shipped only
while `enabled: true`, and it needs a native runner and the `executable-start` native
check. Until a platform's native check passes on a runner of that platform, it stays
listed with `enabled: false`. The workflow fails closed on an empty matrix and on a
missing repository input (`ADDON_OCI_REPOSITORY`). There is no default registry.
The add-on repository is `quay.io/go-skynet/local-ai-addons`; every other
backend image repository is refused.

Every platform matrix lists five platforms: `linux/amd64` on `ubuntu-24.04`, `linux/arm64`
on `ubuntu-24.04-arm`, `darwin/arm64` on `macos-15`, `darwin/amd64` on `macos-15-intel` and
`windows/amd64` on `windows-2025`. Every add-on except Sandbox enables all five. Sandbox
enables only the Linux platforms. A test fails when a platform is enabled without a
recorded check.

A Windows package names its executable `<executable>.exe`: Windows has no execute bit,
and only the `.exe` extension makes a file a program there. The build sets the name in
the manifest of the Windows package, so `platforms.yaml` and the manifest template keep
the plain name. The package job runs its steps with the bash of Git for Windows on the
Windows runner. Tables links SQLite through cgo, so its Windows leg installs the MSYS2
UCRT64 gcc and links the C runtime statically.

A package that ships skills names their folder with `skills:` (for example
`skills: addons/office/skills`). The build lays it at the package root as `skills/`,
in a layer of its own, on every platform, and checks it with the rules the owner
applies at install: the folders must be the skills the manifest declares, and every
file must pass the checks of [Skills]({{% relref "/addons/contract-reference/manifest#skills" %}}). A package that the owner would refuse
is never built.

Each package also has a catalog template in `gallery/addons/<id>.yaml.tmpl`, so it can be
listed in Find add-ons once a maintainer renders and publishes its entries. See
`gallery/addons/README.md` for the commands.

All add-ons publish to one OCI repository, the value of `ADDON_OCI_REPOSITORY`. Each
add-on is a set of tags of that repository, and every tag starts with the add-on id:
`<id>-sha-<short sha>` for a commit, `<id>-<version>` for a release, and
`<id>-development` or `<id>-latest` for a channel alias. For example, the Fine-tuning
build of one commit is
`oci://quay.io/go-skynet/local-ai-addons:fine-tuning-sha-99d1dca2c869`.

A release entry of the official library is pinned by digest. A development entry is
mutable, as a development backend is: it names the moving tag
(`oci://quay.io/go-skynet/local-ai-addons:fine-tuning-development`), sets `mutable: true`
and has no digest. At install LocalAI resolves the tag to a digest, verifies the signature
against that digest, installs by it and records it in the receipt. A mutable entry must be
an OCI artifact. The update check compares a mutable entry by the digest its tag names (see
[How the candidate is chosen]({{% relref "/addons/manage-addons#how-the-candidate-is-chosen" %}})).

Set `ADDON_OCI_REPOSITORY` to `quay.io/go-skynet/local-ai-addons`. The value has no tag
and no digest. A value that is a backend image repository makes the add-on tools refuse
it. `addonpkg target --repository <repository> --addon <id> --tag <tag>` prints the
reference one add-on publishes to, without a network request.

The index entries pin each platform image by digest, so the tag only names the
artifact. The host accepts a tag reference (`oci://<repository>:<tag>`) and reads the
repository with any number of path elements, so an index that still names the old
repository-per-add-on form (`oci://<repository>/<id>:<tag>`) keeps working.

Publishing events (a push to `master`, a version tag and a published release) fail closed
for an add-on until at least one of its platforms is enabled in its `platforms.yaml`.
Pull requests and other pushes publish nothing, so an add-on without an enabled platform
only gets no package legs there. A missing or malformed `platforms.yaml` fails the plan on
every event.

### Channels and tags

`scripts/addons/channel.sh --event <name> --ref <full ref> --sha <commit>` is the one
place that decides what an event may publish. Its output is what the workflow uses.

| Event | Immutable tag | Moving tag |
|---|---|---|
| Pull request | none | none: nothing is pushed, no secret or OIDC token is available |
| Push to `master` | `sha-<12 hex of the commit>` | `development` |
| Push to another branch | none | none: a branch push never publishes, whatever its name |
| Push of a `v<semver>` tag | `<semver>` | none: a tag alone never advances `latest` |
| Published, stable, non-draft release | `<semver>` | `latest`, after complete publication |
| Published pre-release | `<semver>` | none |

The names in this table are the part after the add-on id. The registry tag is `<id>-<name>`,
for example `tables-sha-4edf30cda051` or `voice-development`.

Names are in disjoint namespaces: `latest` and `development` are reserved, `<semver>`
and `sha-<12 hex>` are immutable, and every branch channel starts with `branch-`. The
branch channel is `branch-` + a label + `-` + the first 12 hex characters of the SHA-256
of the full original ref (`refs/heads/...`). The label is the branch name in lower case,
every run of characters outside `a-z0-9` replaced by one dash, cut to 40 characters
(`x` when nothing is left). The hash makes `Feature/X`, `feature-x` and `feature/x`
three channels. Only the exact ref `refs/heads/master` is `development`. Nothing built
from another branch is published, and no new branch channel is created. Channels that
an earlier run published stay where they are.

Immutable tags are never overwritten. A second publication of a version with other
content is refused. The one exception is a rebuild of the same version and commit after
an earlier run published it: the published content is kept (`--adopt-existing`), and the
workflow verifies its signature before it promotes anything.

### Publication and signing

The workflow `.github/workflows/addons-package.yml` has these jobs.

| Job | What it does | Credentials |
|---|---|---|
| `validate` | One leg for each add-on. Builds the UI of the add-on, and runs the packaging checks (which cover every add-on) on every event, including pushes and releases. A red `validate` stops `package`, `publish` and `promote`. | none |
| `plan` | Runs `channel.sh` and, for each add-on, checks its platform matrix. It joins the matrices into one list of legs and names the add-ons that have legs. It checks the registry input. | none |
| `package` | Needs `plan` and `validate`. One native runner for each enabled platform of each add-on: builds the executable and UI, runs `package.sh` and the native runtime check, uploads the layout. | none |
| `publish` | One leg for each add-on. Needs `plan`, `validate` and every package leg. Joins the legs of the add-on, pushes platform digests and the index by digest to the repository of the add-on, tags the immutable version, signs by digest (keyless, OCI 1.1 referrers, new bundle format) and verifies with the exact identity of this workflow. | registry login and `id-token: write`, in a protected environment |
| `promote` | One leg for each add-on. Needs `publish`, and through it `validate`. Moves aliases, serialized by add-on and channel. | registry login, in a protected environment |

An add-on with no enabled platform gets no legs. On a pull request or a branch push this
fails nothing. On an event that publishes, the plan fails for that add-on and nothing is
published, so a release never leaves one add-on behind without notice.

A pull request, including one from a fork, never reaches `publish` or `promote`, and
`pull_request_target` is not used. A missing or failed leg, or a failed `validate`,
leaves every alias unchanged: nothing is published from a partial set of legs, `latest`
moves only after complete validation, and aliases move only after signing. The workflow
policy (`addonpkg policy`) requires this chain in the job graph. It rejects a `publish`
that does not need `validate`, and a `validate` job with `continue-on-error` or an
`always()`, `failure()` or `cancelled()` condition.

Promotion is serialized by a concurrency group for each channel and is checked against
the registry: `development` and branch channels do not move to a commit that is an
ancestor of the one they name, and `latest` does not move to a lower version or to a
pre-release. A slow older run therefore ends as a no-op.

A force-pushed branch is the new truth of that branch. When the commit a branch
channel names is no longer in the checkout (git answers "not a valid commit name"), the
promotion succeeds only if the run is a `push` to the exact ref that owns the channel
(`--ref` and `--event` of `addonpkg promote`; the channel name is derived from the ref).
For any other event or ref the alias stays where it is and the command fails with
`E_UNREACHABLE`. `development` follows the strict rule: an unreachable commit is always
refused with `E_UNREACHABLE`, and it needs a manual decision. Any other git failure
fails the promotion. A slow run of a pre-rewrite commit that finds the rewritten tip in
its checkout cannot be told apart from a new diverged commit, so a promotion of the
old commit after a force-push can briefly move the channel back until the next push.
The registry has no atomic
compare-and-swap for tags, so the group, not the check, is what closes the window
between two runs.

The signer identity is the workflow file of this package pipeline, not the backend
image workflows: `https://github.com/<owner>/<repo>/.github/workflows/addons-package.yml@<ref>`
with the issuer `https://token.actions.githubusercontent.com`. The identity contains the
ref of the run, which differs for each release tag.

### Catalog entries

`gallery/addons/tables.yaml.tmpl` and `addonpkg catalog` render one entry for each
platform from a verified merged layout. An entry pins the digest of the platform image,
names the repository and the immutable tag, carries the channel it is current for and
holds the expected signature policy. The catalog resolves a selection by version or by
channel, so a channel is metadata that names the entry and its digest, never a tag that
is read at install time. See [gallery/addons/README.md](https://github.com/mudler/LocalAI/blob/master/gallery/addons/README.md).

An entry may also carry a `requires` object, the compatibility hint of the update check
(see [Update detection and upgrade]({{% relref "/addons/manage-addons#update-detection-and-upgrade" %}})).

The host applies the signature policy of the library (source), not the one of an entry:
a refresh replaces an entry's policy with the policy the administrator configured on the
source. Executable verification (the signature of the package digest against the
workflow identity) and the trust of the catalog document itself (how the library is
fetched) are two separate things, and the host tests them separately.

#### Workspace operations

An add-on that holds `sandbox=workspace.`, `office=workspace.` or `web=workspace.` in the host operation setting can ask for the files of the user it is working for:

- `workspace.materialize` builds a read-only tree of some folders of the user in a directory of the add-on's own data directory.
- `workspace.commit` takes the `upper.tar` layer the add-on left in such a directory and commits it as one change set. It needs an `act` request.
- `workspace.changes` lists the change sets after a revision.
- `workspace.admin` answers only when the user of the request is an administrator, and is refused for anyone else. An add-on uses it to guard a setting; it never decides who is an administrator itself.
- `workspace.export` copies one file of the user into the data directory, and `workspace.import` (an `act` request) writes one file of the data directory into the files of the user, with the version check of the files API.

Every call runs as the user of the live request, never as a user named in the arguments. `dir` is a relative folder inside the add-on's data directory; an absolute path, a parent reference or a symbolic link is refused.

What a call can reach depends on who made the request:

- **A call from a chat.** The host binds every add-on call that a chat makes to that chat, for every tool kind: a read tool that names no resource (such as the Office add-on's `read-docx`) as well as an action or a change. The calls act with the chat's own access, the same access as the chat's file tools:
  - In a chat whose agent has no folder, `/` is the user's home, as before.
  - In a chat whose agent has a folder (see [Folders of an agent]({{%relref "/features/chats-and-agents/chats-agents#folders-of-an-agent" %}})), `/` is the agent's folder. A path names a file inside it: `/Other/x` is `Other/x` inside the folder, not a folder of the home. The add-on can also name the chat's own folder as `/workspace/chat/x`, and the shared folders as `/workspace/shared-<name>/x` (read only). A path with a parent reference, NUL or a control character is refused. An encoded or look-alike separator (`..%2f`, `%2e%2e`, a backslash, U+2024, U+FF0F) is part of a name. A path outside these folders is not found. The agent's mode applies: a read-only folder takes no file.
  - When an add-on mounts its `/` at `home`, the host also mounts the chat's folder at `chat` in a chat whose agent has a folder, so the Sandbox sees `/workspace/home` and `/workspace/chat` as the chat's file tools do. No add-on needs a change for this.
  - `workspace.changes` lists only the change sets inside the chat's folders, and a `workspace.commit` entry outside every mount comes back as an `outside-view` conflict.
  - A call whose chat the host cannot resolve (the chat is gone, its agent's folder is missing, or the binding names another user than the actor) is refused with `bad_request`. It never falls back to the home. An action always works for one chat, so an action request that carries no binding and whose resource names no chat of the user is refused too.
- **A call that no chat made**, such as a call from the add-on's own page or the OpenAI-compatible route, works on the user's whole home, as the Files page does. The person acts with their own rights there.

The binding stays on the host. It is kept next to the envelope of the live request, and between frontends it rides in the signed forwarded request (see [Owner forwarding and HTTP relays]({{% relref "/addons/contract-reference/owner-forwarding#owner-forwarding-and-http-relays" %}})). It is never a field of the envelope the add-on receives, which installed add-ons decode with unknown fields refused, so the add-on contract does not change. The access is resolved again from the stores at each call (stateless per request).

#### Network operations

An add-on that holds `sandbox=egress.` in the host operation setting (the default) can ask about the network connections of the commands it runs (see [Network access]({{% relref "/addons/catalogue/work/sandbox-settings#network-access" %}})):

- `egress.check` (`act`, feature `chats`) takes `{host, port, kind, tls}`, with `kind` `connect` or `http`, and answers `{action, reason, allowPrivate, self}`, with `action` `allow` or `block`. An allow carries `self`: the addresses of the server as CIDR strings (its interfaces, loopback, and the addresses of the host of the external base URL), the same list `web_fetch` refuses. The add-on refuses them on every dial, besides the interfaces of its own machine, so a command cannot reach the LocalAI API through the server's public name or address. When a card asks the person (see the cards under [Network access]({{% relref "/addons/catalogue/work/sandbox-settings#network-access" %}})), the call waits for the answer, at most 60 seconds, before it answers. The chat is the resource of the live envelope and the user is its actor: arguments named `chat`, `user`, `preset` or `allow`, or any other unknown name, are refused. The host writes the audit line of the verdict itself. The operation may wait up to 60 seconds for a card and has a 75-second timeout.
- `egress.audit` (`act`, feature `chats`) takes `{events: [...]}`, at most 500 lines of `{host, port, kind, verdict, reason, addr, bytesUp, bytesDown, durationMs, at}`. `verdict` is `done` (an allowed connection ended; the bytes it moved) or `block` (the add-on refused it itself: an address that is private, the server's own or a metadata address, a TLS name that is not the host, a limit, or a connection while no call ran). A call made under a job lease (see [Execution provider and status payload]({{% relref "/addons/contract-reference/manifest-extensions#execution-provider-and-status-payload" %}})) is unattended: `egress.check` blocks a host that would need a card with the reason `unattended` instead of asking.

## Simple Office

Simple Office is a lightweight option for basic office-file work, without requiring Sandbox. It reads and edits documents in the user's files, from a chat. It is a package like the others (`office` in the add-on library) and uses the `workspace.*` host operations to take a file in and give the result back.

| Tool | Kind | What it does |
|---|---|---|
| `read-docx`, `read-xlsx`, `read-pptx`, `read-pdf` | read | Return the text of a file: paragraphs with their index, cells row by row (a formula that has no stored result shows as the formula, such as `=SUM(A1:A2)`), slides with their shapes, pages. The answer is cut to about 48 KiB and says so. |
| `edit-docx`, `edit-xlsx`, `edit-pptx` | action | Apply a list of changes (at most 200) to a file. All of them are saved, or none. |
| `create-docx`, `create-xlsx`, `create-pptx`, `create-pdf` | action | Make a new file. `create-docx` and `create-pdf` take a `markdown` string; `create-xlsx` takes a single-sheet `csv` string. All three take `path` and optional `title` strings; XLSX also accepts `sheet`. The former `blocks` and `sheets` arguments are no longer supported. PDF writes Latin-1 text with the built-in font. See [Simple Office]({{% relref "/features/files-and-documents/office#simple-office" %}}) for the contract and limits. |

The action tools run at once, as the user of the chat, with no confirmation, and only in a chat. Every tool, a read tool too, works with the chat's own access: in a chat whose agent works in one folder, `/` is that folder (see [Workspace operations](#workspace-operations)). Without accounts, the files are the same ones the chat and the Files page use. With accounts and `local-owner` mode, the `workspace.*` operations are refused, because the local owner is no account and has no files; use `named-user` mode for the Office and Sandbox add-ons. A file that someone else changed after the tool read it is never overwritten: the tool's version is saved as a second file next to it, and the answer says so. A file that is not of the type its name says, or a path outside the user's files, is refused with a reason the model can read.

The add-on also ships `xlsx`, `docx`, `pptx`, and `pdf` skills (see [Document skill providers]({{% relref "/addons/contract-reference/manifest#document-skill-providers" %}})).
These procedures use Office tools to read, edit, and create documents within the tools' supported capabilities.
They require no command runner. Sandbox supplies separate Python/Paper procedures under the same logical names.
Stopping Office does not restore core procedures; another provider serves only when eligible and not blocked by an explicit pin.
A test checks that each tool named by an Office skill belongs to its manifest.

## Web

The Web add-on serves the chat tools `web_search` and `web_fetch` in place of the built-in
ones. Its tools `search` and `fetch` declare `"shadows": "web_search"` and
`"shadows": "web_fetch"`, so a chat with the add-on on and running sees one `web_search` and
one `web_fetch`, served by the add-on. It adds two things the built-in tools do not have:
search providers that need a key (Brave Search, Tavily, Google Programmable Search), tried in
an order the administrator chooses, and the text of PDF files. Both tools run the code of the
built-in tools for the rest: the same SSRF guard, limits and untrusted-content label. See
[Web search and web pages]({{%relref "/features/chats-and-agents/web#the-web-add-on" %}}) for what it does in a
chat and how to set it up.

| Tool | Kind | What it does |
|---|---|---|
| `search` | read | Replaces `web_search`. Arguments `query`, `maxResults`, `freshness`, `site`. |
| `fetch` | read | Replaces `web_fetch`. Arguments `url`, `maxChars`, `format`. Reads PDF too. |
| `status`, `set-settings`, `test-search` | read, page only (`uiOnly`) | The settings of the add-on, on the Settings tab of its details on the Add-ons page: the providers, their order and keys, and a test of each provider. A change and a test need an administrator, which the add-on asks the host with `workspace.admin`. |

The contract allows no underscore in an argument name, so the add-on's tools name
`max_results` and `max_chars` of the built-in tools `maxResults` and `maxChars`. The model
reads the add-on's schema.

The keys are kept in `settings.json` in the add-on's data folder (mode 0600). They are never
returned by a tool, written to a log or put in a model's prompt or a tool result: the
Settings tab shows only whether a key is set. A key reaches its provider in a request header
(`X-Subscription-Token`, `Authorization` or `X-Goog-Api-Key`), never in a URL, and the clients
of the keyed providers follow no redirect, so a key is never sent to another host.

The add-on runs on the add-on owner, like every add-on. Its settings, its search cache and
its rate limits live in that one process, so they are the same for every frontend of a
cluster.
