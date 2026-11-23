+++
title = "Sandbox backends, profiles and network"
description = "Choose where the Sandbox add-on runs commands, which profiles it offers, what the network rules are, and how it is packaged and upgraded."
weight = 21
+++

Set up the Sandbox add-on: where it runs commands, which profiles it offers, what its network rules are, and how it is packaged and upgraded. For what the Sandbox is for and how a chat uses it, see [Sandbox]({{% relref "/addons/catalogue/work/sandbox" %}}).

## Sandbox

The Sandbox add-on lets a chat run commands and code in isolation. It has one tool for the model, the action tool `bash`, which replaces the chat's built-in `bash` (`"shadows": "bash"`), so a chat with the Sandbox sees one `bash` and it runs isolated. The model reads and changes files with the chat's own file tools (`read_file`, `write_file`, `edit_file`, `list_files`, `search_files` and the others; see [Chats]({{% relref "/features/chats-and-agents/chats-commands#running-commands" %}})): they work on the same files, so a script written with `write_file` runs with `bash` at once, and what `bash` writes is in the user's files after the call. `bash` runs inside an isolated sandbox of the user and the chat, with the user's files mounted under `/workspace/home`. Each call, and each background job, starts in the chat's work folder (`/workspace/home/Chats/<chat>/work`, made before the call) and gets its path as `$WORK`; a chat with no work folder yet starts in `/workspace/home`. The description of `bash` tells the model to use `$WORK` or relative paths, to quote every path with double quotes, and never to type out a name with spaces or non-ASCII characters. The skill folders are not mounted in the sandbox. User-written scripts must be saved in the home with the file tools before execution. The status tool keeps its add-on name (`addon__sandbox__status`). Without the Sandbox, or with it off or unable to run, a chat may still run commands on the server itself, without isolation, when an administrator allows it; see [Commands on this machine]({{% relref "/features/chats-and-agents/chats-commands#commands-on-this-machine" %}}).

Sandbox also owns the `docx`, `pptx`, `pdf`, and `xlsx` Python/Paper procedures, references, and applicable license notices.
They require the selected Sandbox provider to offer usable isolated commands, with explicit chat or agent grants.
Their packaged skill trees contain text only; executable PDF helpers belong to the guest image.
Invoke `/usr/local/lib/localai/document-skills/pdf/content_qa.py` and
`/usr/local/lib/localai/document-skills/pdf/verify_pdf.py` with `python3` through Sandbox `bash`.
No host skill-folder mount is required. Dependency pins remain part of the guest image.
Simple Office keeps its own tool-based procedures and needs no exec grant.


An older package of the add-on also served `read`, `write`, `edit`, `glob` and `grep`. They are gone: reinstall the package to drop them. A chat's switch for the add-on is kept by add-on ID, so nothing a chat stored changes.

- **One sandbox per user and chat.** The key is the pair of the user and the conversation. The user comes from the host. Nobody else reaches the sandbox, and the model cannot name another user or chat.
- **Changes are one change set.** After every call the add-on commits what the code changed, as one change set in the history of the user's files, so it can be undone there. If someone else changed the same file meanwhile, their version stays and the sandbox's version is saved as a copy; the answer names the file and the copy. A file written outside `/workspace/home` (where `bash` starts) is not saved, and the answer names it too. In a chat whose agent works in [one folder]({{%relref "/features/chats-and-agents/chats-agents#folders-of-an-agent" %}}), `/workspace/home` is that folder (read only when the agent can only read it), and the chat's own folder is at `/workspace/chat`, where files are saved as well; nothing else of the user's files is in the sandbox. The change set belongs to the chat that ran the command, so the chat lists the files it saved (see [Files that a command saved]({{% relref "/features/chats-and-agents/chats-files#files-that-a-command-saved" %}})). A file change by someone else makes the sandbox rebuild its view of the files before the next call.
- **Limits.** At most 4 sandboxes for a user (the oldest idle one gives way to a new one) and 16 in all. When the 16 are used, the oldest suspended sandbox of any user gives way to a new one, so sleeping sandboxes never keep a user out; a call is refused with a busy answer only when every sandbox is running. A sandbox that is idle for 10 minutes is suspended and wakes on the next call. A sandbox is removed 24 hours after it was made. An add-on restart removes every sandbox it left, suspended ones too. When the add-on stops (it is disabled, or the server shuts down) it first stops all its sandboxes, for at most 8 seconds. The supervisor waits for that before it kills the add-on process; a server that is killed with SIGKILL cannot do this, and the next start removes what was left. When a suspended sandbox is removed, the host backend also stops the processes it left running.
- **Background jobs.** The add-on runs the jobs of the chat's `job` tool (see [Execution provider and status payload]({{% relref "/addons/contract-reference/manifest-extensions#execution-provider-and-status-payload" %}})) in the chat's sandbox, in the add-on process. A job's output (stdout and stderr together, the last 256 KiB, with absolute byte offsets) and its exit status come from the process, not from any connection of the host, so they stay when the call that started the job returns. While a job runs, its sandbox is not given way to another chat, not suspended, not refreshed and not removed at its age. Input goes to the job's standard input; a call waits at most 5 seconds for the job to read it. A kill sends SIGTERM (or SIGINT) to the job's process group and SIGKILL 10 seconds later; a job ends after 2 hours the same way. When a job ends, what it wrote is committed as one change set (`Job job-3: build`) before the sandbox is let go; a file it writes is also in the next `bash` call's change set. At most 32 jobs run in the add-on process; the host keeps the limits per chat and per user. A finished job stays readable for 30 minutes, at most 16 per chat. An add-on restart ends every job: the host reads the jobs as gone. A call that must make the sandbox again (a new profile, agent folder or mode) ends the chat's jobs first and says so at the top of its output.
- **The network of a job.** While no call of the chat runs, the proxy lets a connection through under the operation of the oldest live job of the sandbox (the proxy cannot tell which process opened it; during a call, every connection counts against the call). Each job has its own 64 MiB up and 4 GiB down. A host that the chat's preset or an always-allowed rule allows passes. A host that needs the person's yes is blocked, since nobody is there to answer a card: the job's output gets *Blocked by LocalAI: host:port needs the user's approval, and nobody can be asked while no turn of this chat runs (a background job)*, and the audit line has the reason `unattended`. With neither a call nor a job, every connection is refused as before.
- **Output.** A tool answer is cut at 64 KiB, and a `bash` call stops after its timeout (120 seconds by default, 28 minutes at most, so the answer is back before the 30-minute limit of an action call).
- **Without a backend.** When the host has no way to run sandboxes, nothing runs. A chat is not offered the Sandbox tools then: it gets one stand-in tool that tells the model why, so the model answers that it cannot run code instead of trying. Without the add-on, or with it switched off for a chat, the chat's model is told that it cannot run commands, and the chat's Tools menu says so to the user; see [Running commands]({{% relref "/features/chats-and-agents/chats-commands#running-commands" %}}). A tool that is called anyway answers "This host cannot run commands." The same happens when the Files workspace is off, because a sandbox needs the user's files.
- **Where you see the state.** The Sandbox page tells every user which backend runs the code, or that no backend works, or, while the host backend is in use, that commands run directly on this machine with no isolation. An administrator also sees, on that page and in Settings, the reason each backend does not work and what to do. An administrator can choose the host backend there: the page shows the warning that commands then run on this machine without isolation, and asks for the typed confirmation before it saves. A user who is not an administrator is told that only an administrator can change this.
- **Undo.** Each `bash` call that changes files is one change set in the history of the user's files (Files, History), and it is undone there, one call at a time. A whole turn is not one change set: the sandbox runs in the add-on process and the Files service commits each write of a tool on its own. The file tools of the chat itself work the same way, with an Undo note under the call.
- **Shared code.** The Host backend, the manager that keeps one sandbox per user and chat, and `bash` live in `core/services/studio/sandbox` (`host`, `runner`). The add-on runs them over its host operations; the chats' built-in `bash` runs the same code over the workspace store directly.

### Sandbox backends and settings

The add-on runs commands in one of these backends, chosen by an administrator on the Settings tab of the Sandbox details on the Add-ons page (the Sandbox page links to it):

| Choice | What it is |
|---|---|
| Automatic | KVM (Cloud Hypervisor) when the host has it, then gVisor, then a macOS virtual machine (on a Mac with Apple silicon), otherwise nothing. It never picks the host backend, not even as a last resort. |
| gVisor | `runsc` with user namespaces. Needs `runsc` (the package carries it), `taskset`, and on Ubuntu 24.04 and later an AppArmor profile (the package carries `gvisor-profile`, which prints it). |
| KVM | Cloud Hypervisor with `virtiofsd`, `mke2fs`, `debugfs` and `/dev/kvm`. The package carries Cloud Hypervisor and, on amd64, `virtiofsd`. `bwrap` is used when present and not in a container. |
| macOS virtual machine (`vz`) | One Linux VM for each chat, run by the Virtualization.framework of macOS. Needs a Mac with Apple silicon and the add-on package for macOS, whose executable carries the virtualization entitlement. The Settings tab shows it only on a Mac, or when it is the saved choice. |
| Off | No sandbox. Every tool answers that the host cannot run commands. |
| Host | Runs commands on the host as the user LocalAI runs as, with no isolation. |

#### What the Sandbox needs to run isolated

The Sandbox reports a backend as `isolated` or as `host` (see
[Execution provider and status payload]({{% relref "/addons/contract-reference/manifest-extensions#execution-provider-and-status-payload" %}})).
Only gVisor, KVM and the macOS virtual machine report `isolated`. The `host` backend runs the command as a
normal process of the user LocalAI runs as. No kernel boundary separates it from
the rest of the machine, so it is never `isolated`.

- **Document skills.** The `docx`, `xlsx` and `pdf` skills of the Sandbox are offered
  only when the Sandbox reports an isolated backend. With the `host` backend, with
  Off, or when no backend works, the chat does not get them.
- **gVisor.** The add-on starts `runsc` with `--network=none`, a root file system in
  memory (`--overlay2=root:memory`), `--directfs=false` and `--ignore-cgroups`. It
  sets no `--platform` flag, so `runsc` uses its default platform and not the KVM
  platform. gVisor needs no `/dev/kvm`, no cgroup and no extra capability. It needs
  a non-root user, `runsc` and `taskset`, and permission to create a user namespace:
  the container seccomp profile and, on Ubuntu 24.04 and later, AppArmor must allow
  it. The add-on checks this at start with a small test sandbox.
- **KVM.** Cloud Hypervisor needs `/dev/kvm` for the user LocalAI runs as. Pass the
  device to the owner container only when you choose or expect this backend.
- **macOS virtual machine.** The add-on runs each sandbox as a VM of Apple's
  Virtualization.framework, with the same guest image, kernel and guest agent as
  KVM, so the guest has a kernel of its own and no network device. It needs a Mac
  with Apple silicon, the `linux/arm64` variant of the guest image, and the
  `com.apple.security.virtualization` entitlement on the add-on executable (the
  macOS package is signed ad hoc with it). The add-on needs no other program: it
  converts the image and reads and makes the disk images in its own code. The
  limits of a sandbox become the CPU count and the memory of the VM, within what
  the Mac allows. Two differences from KVM matter. First, the VMs belong to the
  add-on process: when the add-on stops or crashes, every VM stops with it, and
  the next start removes the sandboxes it left. Second, a sleeping sandbox keeps
  its files but not its running programs: macOS does not let the add-on save the
  memory of a VM, so a sandbox that sleeps after 10 minutes of idle time (see
  Limits above) wakes with a fresh guest over the same files. A command or a
  background job that was still running in it is gone, as after a refresh. A
  sandbox that runs a background job does not sleep.
  The Mac needs no setting and no manual download for the guest image: the
  add-on pulls the `linux/arm64` image of each profile on its first use, with the
  progress shown in the status of the profile, and keeps it in its data
  folder. The `base` image is about 220 MB to download and needs about 0.8 GB
  of disk once converted; `media` is about 370 MB and 1.1 GB; `office` is about 365 MB and 1.4 GB. The first sandbox
  of a profile also converts the image, which takes a few seconds. After that a
  sandbox is ready in under a second.
- **Automatic.** The add-on tries KVM, then gVisor, then the macOS virtual machine. If
  `/dev/kvm` is missing, it uses gVisor, so an owner container without `/dev/kvm`
  still has an isolated sandbox. On a Mac the first two are not available, so it uses
  the macOS virtual machine.

When the owner runs in a container, the container that runs the add-on must meet
these needs. In a split deployment that container is the owner, not the frontends.

The network limit and the presets (Off, Custom, Allow all) apply to
the commands of the Sandbox because every connection goes through its proxy, which
asks the host with `egress.check`. On current master, the host shows the limit as
applied to an add-on's commands only when the provider reports that it enforces it.
A provider that only reports isolation does not count. This behavior can change in a
later release.

The Settings tab lists the backends of the host, each with its state (in use, ready or unavailable); the reason a backend does not work is under **Details**. When gVisor fails with `exit status 128` and `cannot read client sync file`, the row also says that gVisor needs an AppArmor profile that allows user namespaces and a container that may create them, and links to [The Sandbox in the LocalAI container](#the-sandbox-in-the-localai-container). The add-on asks for the `network-policy`, `tool-providers` and `sandbox-profiles` [host controls]({{% relref "/addons/contract-reference/apps-and-pages#host-controls" %}}), so the same tab also holds the [sandbox profile policy](#sandbox-profiles), the network of the Sandbox commands (the limit, the preset of a new chat, the ports, the extra registries, the always allowed hosts, private networks, the audit retention and the audit) and the `bash` row of Settings > Tools, and saves them like the Settings page. An explicit choice is never replaced by another when it does not work: the tools say they cannot run.

The **Host** backend is for a machine that can run neither of the others. It is not available when the add-on runs in WSL 2 for LocalAI on Windows. Choosing it takes two steps. The page shows a plain warning and asks the administrator to type the sentence *I understand that commands run on this host without isolation*. The add-on then saves the choice. The confirmation is forgotten when another backend is chosen. The backend refuses to start when LocalAI runs as root. Every command is logged with the name of the chat user, in the LocalAI log with the prefix `[sandbox]` (LocalAI writes everything an add-on prints to its output or error into its own log, one line at a time, cut at 4096 bytes), and the Settings tab of the Sandbox details (for administrators) shows a notice for as long as the host backend is the one in use. The model sees it in the answer of the status tool. Treat the machine as one that code can harm.

The choice is kept in the add-on's data directory. Changing it removes the running sandboxes; what they did is already in the users' files.

The guest image, `runsc` and the KVM programs are not part of the add-on's source. A release sets `main.guestImage` (the image of the `base` profile, by digest) at build time. It sets `main.profileImages` (`media=<ref>,office=<ref>`) for the images of the `media` and `office` profiles. A profile without an image is not offered by that release. The checksums of the `runsc` and of the Cloud Hypervisor and `virtiofsd` it was tested with are compiled into the add-on (`runscPins` and `kvmPins` in `addons/sandbox/cmd/sandbox`), and tests keep them equal to `package/runsc.json` and `package/kvm.json`. A build without a guest image reports that on the page, and only the host backend can run.

### Sandbox profiles

A profile is an environment a sandbox can run in: a name, what it is for, the limits of one sandbox (CPUs, memory, disk) and the programs it has. The Sandbox add-on declares its profiles in its manifest, in the `execution.profiles` list of the `bash` tool. An administrator cannot add an image: only the add-on lists profiles, and each has an image the release built and pinned by digest. The release ships these:

<!-- sandbox-profiles:start -->
| Profile | For | Programs | CPUs | Memory | Disk |
|---|---|---|---|---|---|
| `base` | Python, documents, PDF and the usual command-line tools. | python3 (pandas, matplotlib, openpyxl, docx, pptx, pypdf, Pillow), pip, jq, ripgrep (rg), sqlite3, zip, unzip, file, tree, poppler (pdftotext, pdftoppm), git, curl | 2 | 2048 MiB | 10240 MiB |
| `media` | Base plus ffmpeg, sox and ImageMagick to cut and convert audio, video and images. | python3 (pandas, matplotlib, openpyxl, docx, pptx, pypdf, Pillow), pip, jq, ripgrep (rg), sqlite3, zip, unzip, file, tree, poppler (pdftotext, pdftoppm), git, curl, ffmpeg (with ffprobe), sox, ImageMagick (magick, convert), rsvg-convert (renders SVG to PNG, also the SVG reader of ImageMagick) | 2 | 4096 MiB | 20480 MiB |
| `office` | Base plus LibreOffice Impress, Writer and Calc and fonts to render presentations and documents to images and to recalculate workbooks. | python3 (pandas, matplotlib, openpyxl, docx, pptx, pypdf, Pillow), pip, jq, ripgrep (rg), sqlite3, zip, unzip, file, tree, poppler (pdftotext, pdftoppm), git, curl, LibreOffice Impress (soffice, renders .pptx to PDF), LibreOffice Writer (soffice, renders .docx to PDF), LibreOffice Calc (soffice, recalculates .xlsx and renders it to PDF), fonts (DejaVu, Liberation, Carlito (as Calibri), Caladea (as Cambria)) | 2 | 4096 MiB | 20480 MiB |
<!-- sandbox-profiles:end -->

`base` is the fallback and is always allowed. The model is told the profile its chat runs, the programs it has and the programs the other profiles add, in the prompt of each turn, and the description of `bash` names the profile. The text is built from the manifest, so it never differs from the image. The command line runs as an unprivileged user and `apt` is blocked by the proxy, so nothing can be installed from inside the guest with a system package manager. `pip` can install Python packages within the chat's network setting (see [Network access](#network-access)). Adding a program means a new guest image. The build of each image fails when a program of its profile is missing or does not start (`addons/sandbox/images/guest/check-tools.sh <profile>` reads the checks from the manifest).

**The `office` profile.** It is base plus LibreOffice Impress, Writer and Calc (no Java) and the fonts DejaVu, Liberation, Carlito and Caladea, so a presentation or a document can be rendered to a PDF and to images and looked at, and a workbook can be recalculated. Carlito and Caladea have the metrics of Calibri and Cambria, so line breaks match Office. It is opt-in: it is not the default, it is not part of `media`, and it needs no policy entry (with no policy every user may use it). The `pptx`, `docx` and `xlsx` skills tell the model to switch to it before it builds a file, and give it helpers, which every profile has in `/usr/local/lib/localai/document-skills/<format>/`: `pptx/pptx_guard.py`, which checks the sizes, the place and the text fit of the shapes with pure Python; `render.sh` in each of the three folders, which renders the slides or pages to PNG files with a private LibreOffice profile directory; and `xlsx/recalc.sh`, which recalculates a workbook and saves a copy with the cached values (`openpyxl.oracle.recalc` does the same and keeps the original package). A sandbox that runs the profile reserves 2 CPUs, 4 GiB of memory and 20 GiB of disk. Rendering a 10-slide deck, rendering a 20-page document and recalculating a workbook each used under 200 MiB, so a host that needs the room can cap `memory_mib` far lower.

matplotlib draws charts without a display: it writes PNG and SVG files, and the model can save them in the user's files. Its font cache is kept in `/tmp`.

**Choosing a profile.** The profile of a chat is the first of these that exists and that the user may use:

1. The profile the person picked in the chat. The picker sits next to the network switcher in the Tools menu of the chat.
2. The profile of the saved agent of the chat (`sandbox_profile`). Unattended runs of an agent (triggers, jobs, connectors) use it too.
3. The default profile of the administrator (`default` of the policy below), then `media`, then `base`. With media as the default, each sandbox reserves the limits of `media` (2 CPUs, 4 GiB of memory, 20 GiB of disk); an administrator who wants the smaller `base` sets it as the default.

Switching the profile restarts the sandbox at the next command. The user's files stay. Running processes and installed packages are lost, and the picker says so before you switch. The model can switch too with one tool, `sandbox_profile`: the action `list` shows the profiles with their programs, and the action `use` switches. It is in the *Run commands* group of the chat and follows the rules of the picker: the user's own chat and the policy of the administrator. A skill can name the profile it wants with the frontmatter field `sandbox: media` (see [Chats]({{% relref "/features/chats-and-agents/chats" %}})). It is a hint: the model is told, may offer the switch and never switches without the person's yes.

**Policy.** The administrator sets the policy on the Settings tab of the add-on (the owner host setting `sandbox_profiles`, with the same owner rules as the network settings). Everything is open by default: with no policy every user may use every profile.

| Key | Meaning |
|---|---|
| `default` | The profile a new chat gets. Empty: `media`, or `base` when the add-on has no `media` profile. |
| `access` | For a profile, the `roles` (`admin`, `user`) and the `users` who may use it. A profile that is not listed, or has no roles and no users, is open to everyone. |
| `memory_mib`, `disk_mib` | The most memory and disk one sandbox may have, whatever the profile asks. `0` is no cap. |
| `pin_previous` | The profiles that run their previous image. See below. |

**Images.** An image is pulled and converted on the first use of a profile. The picker shows the progress ("Preparing the Media image, 40%") while the first command waits for it. After an add-on upgrade, the add-on pulls the new image of each profile this host used before in the background, so the first command after the upgrade does not wait. A chat keeps its sandbox, and its image, until the sandbox restarts. The picker then shows that a newer image is available. The add-on keeps the current and the previous image of each profile and deletes the older ones, except an image that a running sandbox still uses. If a new image breaks something, an administrator can add the profile to `pin_previous`: new sandboxes of the profile then start from the previous image.

If a profile is missing in the release, or its image cannot be pulled, the command runs in `base`. The output of the command says so ("The media sandbox profile is not available (...), so this command ran in the base profile"), and the picker shows the same notice. The add-on never runs a command in a different profile without saying so. A failed pull is not retried for five minutes, so a broken registry does not stall every command. The images are kept on each machine that runs the add-on: in a cluster every node pulls its own, while the choice of the chat and the policy are shared.

**API.** `GET` and `PUT /api/chats/{id}/sandbox-profile` read and set the profile of a chat, `GET /api/chats/sandbox-profiles` lists the profiles for a saved agent, and `GET /api/chats/admin/sandbox-profiles` is the administrator's view with the state of the images.

### Network access

The guest of a sandbox has no network device: no NIC, no TAP device and no user-mode network stack. A command reaches the network only through one proxy, on guest port 3128, which the guest image sets in `HTTP_PROXY`, `HTTPS_PROXY`, pip, apt, git, npm and `GOPROXY`. The guest agent forwards each connection to that port to the add-on, over vsock (KVM) or a Unix socket (gVisor), and the add-on's proxy asks LocalAI about each connection with the host operation `egress.check`. LocalAI decides; the add-on dials. The proxy never decrypts TLS and holds no certificate the guest trusts.

The guest image has `curl`, `git`, `pip` and the CA certificates, and all of them use the proxy (the programs of each profile are under [Sandbox profiles](#sandbox-profiles)). The model is told so at each turn, with the chat's preset. To read or search web pages it is told to prefer `web_fetch` and `web_search` when the chat has them (the built-in ones or the Web add-on's), and to use `curl` for APIs, downloads and installs. With the network off, the note says that nothing is reachable.

Commands that run on this machine (the host backend, or the chats' built-in `bash`) have the network of the server: none of this applies to them, and the settings say so.

**Presets.** A chat has one of three presets, and an administrator sets the ceiling, the widest preset any chat may use:

| Preset | What a command reaches |
|---|---|
| Off (default) | Nothing. |
| Custom | The HTTPS hosts of the package registries below, on port 443, plus the domains an administrator listed (extra registries and always allowed hosts, see below). The preset is stored as `registries` in chats, agents and the API. |
| Allow all | Every public host on an allowed port. |

The preset of a chat is the lower of its own choice and the ceiling (`chats_egress_ceiling`, default `off`). A new chat starts with `chats_egress_default`, capped by the ceiling. The preset lives in the chat's record on the server, never in a file of the workspace: the sandbox can write the home, so a file there could be changed by the code it is meant to confine. A preset never reaches a private, loopback or link-local address, nor the server's own addresses, nor a cloud metadata address. The server's own addresses are the same list for `web_fetch` and the Sandbox: the interfaces of the server, loopback, and the addresses that the host of the external base URL resolves to (read again every 60 seconds); the add-on also refuses the interfaces of its own machine. With `chats_egress_allow_private` on, a host that an administrator allowed by a rule or a card answer may reach private addresses; the metadata addresses stay refused. See [Runtime settings]({{%relref "/administration/runtime-settings#tools" %}}) for the six `chats_egress_*` settings.

**Package registries.** The list ships with LocalAI (`core/services/studio/egress/registries.json`) and changes only by a reviewed pull request that also changes this table and is named in the release notes. Every entry is an exact host name, HTTPS on port 443, with no pattern. Upload hosts (such as `upload.pypi.org`) are not on it, and neither is `raw.githubusercontent.com`, which serves any user's content. An administrator adds exact hosts with `chats_egress_extra_registries`, for example an internal mirror (which is private and so also needs the private switch and an administrator's rule). Extra registries are registries: they follow the registry rules (HTTPS only, and only with the Custom preset or a card). To allow one more host for any request, use the list below.

**Always allowed hosts.** An administrator can list exact hosts or whole-label patterns in `chats_egress_allowed_hosts` (at most 64; an optional `:port` pins the port). A host on the list is allowed, with no card, in every chat whose effective preset is Custom or Allow all, on any request kind (plain HTTP as well as HTTPS) and on the allowed ports (`chats_egress_ports`), or only on the pinned port. With the preset off in a chat nothing on the list is allowed there, and with the ceiling off the list does nothing at all. The list does not widen the guard: a private, loopback, link-local, metadata or server address is not allowed by it, only by the private switch together with an administrator's rule or card answer, and the TLS server name must still match, on the pinned port as on 443. An administrator's entry also wins over a deny the person stored in one chat before the host was listed. The audit line of a request this way has the reason `allowed_host`. The chat's Network panel shows this list, with the extra registries, to the person under *Custom domains*, as written (patterns such as `*.example.com` and `*` included). An administrator gets an **Edit domains** link there that opens the Settings tab of the add-on at the network section; everyone else sees a note that an administrator manages the list. The list is not a second store: the panel reads `chats_egress_extra_registries` and `chats_egress_allowed_hosts`.

Patterns are `*.example.com` (every descendant, including `a.b.example.com`, but **not** `example.com`) or `*` (any public destination). List the apex separately when needed. Names normalize case, IDNA and a trailing dot. Patterns support an exact port, such as `*.example.com:8443` or `*:443`; the configured allowed ports still apply. Partial-label globs, wildcard ports, URLs and paths are invalid. Extra registries remain exact hosts only.

Wildcard grants remain inside the administrator's ceiling: a **Custom** ceiling (`registries`) requires a listed registry and TLS, **Allow all** permits public destinations, and **Off** blocks them all. For compatibility, existing **exact** entries retain their exception for non-registry public hosts under the Custom ceiling, including plain HTTP. Wildcards never grant private access, even with the private switch enabled. DNS resolution, rebinding protection, metadata/server exclusions and TLS server-name checks still apply; a name match alone never authorizes a dial.


| Registry | Hosts |
|---|---|
| Python (PyPI) | `pypi.org`, `files.pythonhosted.org` |
| JavaScript (npm) | `registry.npmjs.org`, `registry.yarnpkg.com` |
| Go modules | `proxy.golang.org`, `sum.golang.org` |
| Rust (crates.io) | `index.crates.io`, `static.crates.io`, `crates.io` |
| Ruby (RubyGems) | `rubygems.org`, `index.rubygems.org` |
| Java (Maven Central) | `repo.maven.apache.org`, `repo1.maven.org` |
| GitHub releases and archives | `github.com`, `codeload.github.com`, `objects.githubusercontent.com`, `release-assets.githubusercontent.com` |

**What the proxy checks.** The host of a request goes through the same guard as `web_fetch` ([Addresses inside your network]({{%relref "/features/chats-and-agents/web#addresses-inside-your-network" %}})): a canonical name or literal, no address zone, no numeric tricks, no `localhost`. A host the guard refuses is never resolved, so it does not leak through DNS. A name is resolved once; when any answer is not allowed the connection is refused, and the proxy dials the address it checked. The addresses of the node's own interfaces, the whole loopback range and the server's own addresses that LocalAI sends with each allow (see `egress.check`) are refused even when public. Only ports 80 and 443 by default (`chats_egress_ports`; 22 and 25 are always refused). HTTPS goes as `CONNECT host:443`: after the tunnel opens, the first TLS record must be a ClientHello whose server name is the CONNECT host, or the tunnel is closed (this stops a tunnel to one tenant of a CDN that talks to another). A `CONNECT` to another allowed port (an always-allowed `host:8443`, or a port added to `chats_egress_ports`) may carry any protocol, and the server may speak first, so the proxy waits for nothing; but when the first bytes the command sends are a TLS record, they must be a ClientHello whose server name is the CONNECT host too, or the tunnel is closed with the audit reason `sni`. Plain HTTP goes as an absolute `http://` URL: each request of a kept-alive connection is decided on its own, the `Proxy-*` and hop-by-hop headers are dropped, a `Host` header that names another host is refused, and every request gets a new upstream connection. `CONNECT` to port 80, the origin form, `https://` in absolute form and `Upgrade` are refused with 400. The proxy follows no redirect: the guest's client follows it with a new request, which is checked again.

**Cards.** When a command asks for a host that the chat's preset does not admit but the ceiling would, and a person watches the chat, LocalAI stores a card in the chat and the person sees it in the chat: *A command in this chat wants to reach* `host:port` (an international host name is checked and shown in its ASCII form, so a look-alike name such as `pаypal.com` with a Cyrillic `а` reads `xn--pypal-4ve.com`; a name with no valid ASCII form is refused), with **Allow once** (the rest of that command), **Allow for this chat** (a rule) and **Deny** (a deny rule, so the host does not ask again; it can be removed in the network panel). The connection waits for the answer for up to 60 seconds, and never past the call's own timeout, so `pip` goes on by itself after the click; without an answer it gets the 403 line with *the user did not answer*. A late answer still counts: *Allow for this chat* stores the rule, and *Allow once* lets the next call of the chat reach the host within ten minutes. A card nobody answered expires when the chat's turn ends; it stays visible as *Not answered* and can still be answered, with the same buttons, under the call that asked and in the network panel of the **Tools** menu. Ceiling `off` never shows a card; ceiling `registries` shows one only for a registry host (so a chat on Off can approve `pypi.org`); ceiling `all` for any public host. A host inside your network shows a card only with `chats_egress_allow_private` on, and only an administrator can allow it. A chat shows at most three cards at a time and one per host and port; a second connection to the same host waits on the same card. A card names the turn that asked it, and waits only while that turn holds the chat and its 60 seconds have not run out. When the frontend that ran a turn dies, its cards do not stay pending: they read as *Not answered* at once, do not count toward the three, and expire in the chat record when the reaper ends that turn, when the next turn starts, or when the next card is asked, whichever comes first. They can still be answered late, like any expired card. Unattended runs (triggers, connectors) and sub-agents get no card: the block is in their output. The card is written to the chat record before the answer is sent on the chat bus, and the waiting check reads it again every two seconds, so an answer given through any frontend reaches it within two seconds even when the bus message is lost. When the ceiling goes down while a card waits, the card is refused, and every answer to it gets `400 egress_above_ceiling` (the same answer twice to an answered card is `204`; another answer is `409 already_answered`). A card the ceiling cannot admit shows no buttons: it says that network access is off (or limited) on this server, names who can change it, and gives an administrator a link to the **Settings** tab of the add-on that runs the commands (the `network-policy` [host control]({{% relref "/addons/contract-reference/apps-and-pages#host-controls" %}})). The Network row of the chat's Tools menu says the same (*Off: the administrator has not allowed network access.*) when the ceiling is `off` or below `all`.

**What the model reads.** The Sandbox `bash` says that the network is HTTP and HTTPS through the proxy only. When the Sandbox serves `bash`, each turn tells the model the preset in one line, such as *Network for commands: package registries only (Python (PyPI), ...). A blocked host asks the user; do not try to get around the proxy.* When no card can appear (a ceiling no wider than the preset, an unattended run, a sub-agent), the line says instead that a blocked host is refused and nothing asks the user, so the model names the host it needs in its answer. A blocked request gets `403` with one line of text in the tool output, for example *Blocked by LocalAI: example.com:443 is not allowed for this chat (off). Ask the user to allow it.* With ceiling `off`, or a chat preset above the ceiling, the note of the turn says that the server's setting keeps the network narrower, that no approval changes it, and that the model should tell the user instead of waiting or retrying. A request blocked for that reason gets the line *blocked by the server's setting for the Sandbox network, not by a missing approval*; a block that waits on a person keeps the *the user did not answer* line.

**Only while a command runs.** The proxy knows the call in progress of each sandbox. A connection while no call runs (a background process left by an earlier command) is blocked with the reason `idle`, nothing is asked, and it is audited at the next call. When a call ends, every tunnel it opened is closed. The proxy caches no verdicts. Every new upstream connection requires a fresh `egress.check` from the owner, including each HTTP request on a kept-alive guest connection and every CONNECT tunnel. A tightened policy or an unavailable owner blocks the next connection immediately, even for the same host, port and command. Already-established streams may finish under their original authorization; a policy change does not interrupt them. Idle, byte and call-end limits still apply, but no verdict TTL bounds stream lifetime.

**Limits.** These are fixed in this release:

| Limit | Value |
|---|---|
| Concurrent connections of one sandbox | 32 (the guest agent caps all its ports at 64) |
| New connections of one sandbox | 20 per second, burst 40 |
| Concurrent connections of the add-on process | 256 |
| Dial timeout | 10 s |
| Idle tunnel (no bytes either way) | 120 s |
| Bytes from the guest, per call | 64 MiB |
| Bytes to the guest, per call | 4 GiB |

**Audit.** Every verdict is a line of the audit (who, which chat, host, port, kind, verdict, reason), and every allowed connection adds a `done` line with the address it reached, the bytes each way and how long it lasted. The reason names the rule that decided: `preset`, `allowed_host` (the administrator's always-allowed list), `rule`, `once`, `denied`, `ceiling`, `port`, `private` and the others the proxy adds, such as `idle` and `sni`. Lines older than `chats_egress_audit_days` (default 30) are removed daily, with the lines of the audit of the commands run on this machine.

**What fails.** Tools that ignore the proxy variables, SSH remotes (`git@...`), raw TCP and UDP, ICMP and DNS from the guest fail, as before.

**Residual risks.** The presets bound what a command reaches; they do not make every reachable host safe:

- A registry that takes uploads (`npm publish` with a token the command holds) can carry data out under "Custom", and so can any host under "Allow all". The preset is the person's choice of risk; the per-call upload cap bounds the volume, not the risk.
- Inside a TLS tunnel the `Host` header is encrypted and cannot be checked; a CDN that routes by it can still be reached through an allowed name.
- A command that sees the proxy can try many hosts; each one is checked and audited.

**Distributed mode.** Each piece of state has one mode:

| State | Mode |
|---|---|
| Ceiling, private switch, ports, extra registries, allowed hosts, default | owner-authoritative durable host policy when add-ons are enabled; Studio reads it per request. With add-ons disabled, existing local runtime settings apply |
| A chat's preset, rules and cards | shared and durable: the chat record in the chat store (the database in a cluster) |
| A card that waits | stateless per request: the host operation on the owner waits; it wakes on the answer's bus message or on a re-read of the card every two seconds |
| The decision of one connection | stateless per request: the host operation reads the settings and the chat record at each check |
| The map from a call's resource to its chat | per instance: a cache of a fixed mapping (a hash of the chat ID); each frontend fills its own, and a miss only costs a search of the user's chats |
| Audit | shared: the `studio_egress_events` table in a cluster, a JSON file under the data path, or memory without one |
| Audit pruning | single-runner: one frontend at each daily tick, under an advisory lock |
| Rate limits, connection counts | per instance: the add-on process on the owner; verdicts are not cached, so each new upstream connection checks the current owner policy |

With `--addons-role frontend` a frontend runs no proxy; the owner does, and the host operations run on the owner's host.

### Packaging and setting up the Sandbox add-on

The Sandbox package is for Linux on amd64 and arm64, and for macOS on Apple silicon (`darwin/arm64`). For `darwin/arm64`, `scripts/addons/package.sh` signs its executable ad hoc with the virtualization entitlement (`addons/sandbox/package/entitlements.plist`) with `codesign` on a Mac or with `rcodesign` anywhere else, and fails when neither exists; `fetch-runtime.sh` fetches nothing for it, because its guests run in the Virtualization.framework of the system. A Mac that installs the package needs no other program. `darwin/amd64` has no guest backend, so its row stays disabled. `windows/amd64` runs the Linux add-on in WSL 2: its row has `execution: wsl2`, so the package carries the `linux/amd64` executable and runtime files, its manifest has `"osSettings": {"windows": {"execution": "wsl2"}}`, and `package.sh` builds it on a `linux/amd64` host. None of the backends runs on Windows itself (gVisor, KVM and the Virtualization.framework do not exist there, and the host backend needs a POSIX shell and process groups). The row stays disabled until its native check, which starts the executable through `wsl.exe`, passes on a Windows runner with WSL. In WSL 2 the host backend is off, also after its confirmation: there its commands could read the Windows drives under `/mnt` and start Windows programs. The requirements, the setup and the remaining risks are in [Install on Windows]({{% relref "/start/install/windows" %}}#sandbox-add-on). LocalAI never runs the commands of a chat on Windows without isolation.

- **`runsc`.** `addons/sandbox/package/runsc.json` pins one gVisor release (20261012.0) with the URL, the checksum the release publishes for each tarball, and the checksum of the `runsc` inside it. `addons/sandbox/package/fetch-runtime.sh <os/arch> <dir>` downloads it, checks both checksums, and the release workflow puts `runsc` and `gvisor-bin/` in the package. At run time the add-on checks `runsc` against the pinned checksum, copies it with its helpers to `runsc/` in its data directory as executable files, and uses that copy. A `runsc` that does not match is never used. Without one in the package, a `runsc` in a system folder (`/usr/local/sbin`, `/usr/local/bin`, `/usr/sbin`, `/usr/bin`, `/sbin`, `/bin`) is used as it is. The add-on does not read `PATH`: LocalAI starts it with none, so it looks for `runsc`, `taskset` and the KVM tools in those folders only.
- **AppArmor.** On Ubuntu 24.04 and later, a program needs an AppArmor profile to make user namespaces. The package carries `gvisor-profile`, a small program that prints the profile of one `runsc`. The add-on copies it next to `runsc`, and when gVisor fails for this reason the page tells an administrator the exact step, with the real paths. Run it once, as root, on the machine that runs LocalAI (not inside a container):

  ```bash
  /path/to/data/addons/sandbox/runsc/gvisor-profile /path/to/data/addons/sandbox/runsc/runsc \
    | sudo tee /etc/apparmor.d/localai-runsc
  sudo apparmor_parser -r /etc/apparmor.d/localai-runsc
  ```

  The profile names the binary by path, so a new `runsc` at a new path needs a new profile. Inside a container, AppArmor is that of the host: use a profile that allows user namespaces for the container (or `--security-opt apparmor=unconfined`), together with a seccomp profile that allows `unshare` and `clone` with `CLONE_NEWUSER`.
- **The guest images.** The guest runs a Debian userland, the guest kernel and the guest agent. There is one image for each sandbox profile, and the Dockerfile has one target for each: `base` (the default target) `media` (base plus ffmpeg, sox, ImageMagick and `rsvg-convert`) and `office` (base plus LibreOffice Impress, Writer and Calc and fonts). The `sandbox guest image` workflow (`.github/workflows/sandbox-images.yml`) builds `addons/sandbox/images/guest/Dockerfile` for amd64 and arm64, pushes the images to one repository of the project registry (`quay.io/go-skynet/sandbox-guest`, which must allow anonymous pulls) with the tags `sha-<commit>` (base) `media-sha-<commit>` (media) and `office-sha-<commit>` (office), checks that each pushed index has a `linux/amd64` and a `linux/arm64` image, and outputs each reference by digest. Each digest names an index of both architectures, so a Mac with Apple silicon pulls the `linux/arm64` image, and its kernel, by the same reference as an arm64 Linux host. The package build passes them in `SANDBOX_GUEST_IMAGE=<repository>@sha256:<digest>` (base) and, optionally, `SANDBOX_GUEST_IMAGE_MEDIA` and `SANDBOX_GUEST_IMAGE_OFFICE`. `scripts/addons/package.sh` builds the add-on with `-ldflags "-X main.guestImage=... -X main.profileImages=media=...,office=..."`. `package.sh` refuses a Sandbox build without the base variable; `--local-check` builds with a stand-in reference no registry serves, for a check that does not publish. To build an image by hand: `docker buildx build -f addons/sandbox/images/guest/Dockerfile --target <base|media|office> .` from the repository root.
- **The build check of a profile.** The tools of each profile, with the command that proves each one works, are in the add-on manifest (`execution.profiles`). The Dockerfile copies the manifest into the image and runs `check-tools.sh <profile>` in each target, so an image does not build when a listed tool is missing. `sh addons/sandbox/images/guest/check-tools_test.sh` tests the script.
- **KVM.** `addons/sandbox/package/kvm.json` pins Cloud Hypervisor v54.0 (amd64 and arm64) and virtiofsd v1.14.0 (amd64) with checksums, and `fetch-runtime.sh` puts them in the package. The add-on checks each against the checksum compiled into it and copies it, as an executable file, to `kvm-bin/` in its data directory. Upstream publishes no static virtiofsd for arm64, so on arm64 install virtiofsd 1.13 or later in a system folder. The KVM backend also needs `mke2fs` and `debugfs` from e2fsprogs (the LocalAI image installs them) and access to `/dev/kvm` for the user LocalAI runs as. Bubblewrap is optional in a container: the add-on does not use it when it finds `/.dockerenv` or `/run/.containerenv`, or when `SANDBOX_NO_BWRAP=1` is set in its environment (`SANDBOX_NO_BWRAP=0` forces it). `addons/sandbox/images/host/Dockerfile` still builds an image with the same programs for a product that wants them in one place.
- **Tests in CI.** The `sandbox tests` workflow (`.github/workflows/sandbox-tests.yml`) runs the Go tests and the end-to-end suite, the gVisor integration suite on amd64 and arm64 (with the pinned `runsc` and its profile loaded), the KVM integration suite on a runner that has `/dev/kvm` (it skips, and says so, on one without), and the release smoke test.
- **Release smoke test.** `scripts/addons/sandbox_smoke.sh` takes the package that `package.sh` made, starts the add-on from the extracted files under the real supervisor with the default settings, and runs a command through the tool path of a chat. It passes when the command ran in gVisor or KVM, or, on a host that cannot, when every tool answers "This host cannot run commands." and nothing ran. `--require isolated` makes the second case a failure.

### The Sandbox in the LocalAI container

gVisor, KVM and the host backend refuse to run as root, so the sandbox needs the add-on process to run as an unprivileged user. The LocalAI container image starts as root only long enough to run `entrypoint.sh`, which hands the data folders (`/models`, `/backends`, `/configuration`, `/data`, `/tmp/generated`) to the user `localai` (uid and gid 10001) and then runs LocalAI as that user, with the groups that own `/dev/kvm`, `/dev/kfd` and `/dev/dri/*` added. The add-on runs as the same user.

- **Existing volumes.** A volume created by an older image is owned by root. On the first start with the new image the entrypoint changes its owner to uid 10001 (`chown -R`), which takes a while on a large models folder, and logs it. Nothing needs to be done by hand. If the volume cannot be changed (a read-only or network mount that refuses `chown`), set its owner to 10001:10001 on the storage side, or run the container with `--user` set to the owner of the data.
- **Another user.** Set `LOCALAI_UID` and `LOCALAI_GID` to use another id. A container that already starts as a non-root user (`docker run --user`, Kubernetes `runAsUser`) is left as it is; the data folders must then be writable by that user.
- **Keep root.** Set `LOCALAI_RUN_AS_ROOT=true` to run as root as before. The sandbox is then unavailable, and the page says why.
- **Devices.** The KVM backend needs `/dev/kvm`; gVisor does not. For KVM, pass the device to the container (`--device /dev/kvm`); the entrypoint adds the group that owns it. In Kubernetes, request the device through your device plugin and give the pod a `fsGroup` or `supplementalGroups` entry for it.
- **User namespaces.** gVisor needs the container to be allowed to create user namespaces, and on an Ubuntu 24.04 or later host an AppArmor profile that allows them (see **AppArmor** in [Packaging and setting up the Sandbox add-on](#packaging-and-setting-up-the-sandbox-add-on)). Without them `runsc` stops at once, and the backend's reason ends with `exit status 128` and `cannot read client sync file ... EOF`. Docker's default seccomp profile blocks this: use a profile that allows it, or `--security-opt seccomp=unconfined` on a host you trust. KVM needs `/dev/kvm` and no extra privileges.
- **Cluster.** The add-on runs on the add-on owner node only. In Kubernetes, give that pod the settings above; a node pool dedicated to it is the way to keep the isolation requirements off the other nodes.

What the source tree does not prove: the images and the `runsc` download are not built in the test suite, and the gVisor and KVM backends need a host that allows them. Their tests skip with the reason on a host that does not. The host backend and the whole path from a chat to a file in the user's files run in the tests.

### Provider choices and automatic upgrades

Add-on skill switches and tool-provider choices read the owner policy and update only the changed provider keys. Clearing a choice removes that pin without changing other add-ons. Revision conflicts retain edits for an explicit reload and rebase. Uncertain writes retry the same operation; an owner outage disables writes until reconciliation. Native add-on configuration remains separate.

With add-ons enabled, Settings → Backends uses the owner policy for automatic upgrades rather than the frontend runtime file. Combined mode uses the embedded owner through the same API.

### Upgrade execution and polling

Browser upgrades are accepted durably by the add-on owner before the submission
returns. The response identifies the same operation used by subsequent status
reads. Closing a page, refreshing, or losing the acceptance reply does not cancel
accepted work. Accepted upgrades cannot be cancelled from the UI.
The Activity entry has no Cancel control after submission. A retry with the same operation ID, actor and payload observes that
operation; a changed payload conflicts. Only one asynchronous upgrade is admitted
at a time; competing submissions receive a conflict instead of entering a queue.
Other lifecycle service callers retain their synchronous contract.

Execution is single-owner, using the existing storage ownership fence and local
journal, not frontend memory or a message-bus dependency. Standalone uses the same
path. Any frontend can forward polling to the owner. Each read revalidates current
administrator authorization and the recorded actor, including credential identity.
Expired or revoked credentials cannot read the operation, but do not revoke work
already accepted. A different credential does not inherit the original receipt.

Activity shows real stages (package preparation, verified backup, candidate
migration/health, and publication), not estimated byte percentages. Failed
operations retain a safe failure code; detailed underlying causes go to owner
logs. A migration failure can require explicit data recovery; keeping the old
package does not mean the data is safe to run.

Shutdown cancels and waits for the owner worker before releasing storage. On
restart, existing publication and data-recovery journals reconcile before runtime
startup: prepared publication may finish; interrupted work otherwise becomes a
terminal interrupted failure, with recovery-required reported when data is blocked.
Destructive migration is never automatically replayed. An uncertain publication
remains fenced for reconciliation, not presented as a definite rollback.

The browser remembers pending operation IDs in session storage, scoped to the
signed-in user, and resumes observation after a reload without resubmitting.
Logout clears the current user's observer records. Storage restrictions may prevent
observer recovery, but never cancel the owner operation. An unreadable owner or
exhausted polling window is an unknown outcome: keep the ID and read it again,
rather than starting a replacement operation.
