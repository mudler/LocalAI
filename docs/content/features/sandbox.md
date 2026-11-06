+++
disableToc = false
title = "Sandbox: run code safely"
weight = 36
url = "/features/sandbox/"
+++

The Sandbox add-on lets a chat run commands and code in an isolated
environment. The model gets one tool, `bash`, and it runs in a sandbox that
holds your files. Use it to analyse a CSV with Python, convert a file, or
build a document, without giving the model access to the machine that runs
LocalAI.

## Turn it on

1. An administrator installs **Sandbox** from **Add-ons**, **Find add-ons**,
   with **Install and turn on** (see [Use add-ons]({{% relref "features/using-addons" %}})).
2. In a chat, check that **Sandbox** is on in the **Tools** menu. A
   [saved agent]({{% relref "features/chats-agents" %}}) lists it in its add-ons.
3. Ask the model to run something.

Sandbox is Linux only. It needs the Chats and their files to be on, because a
sandbox mounts your files.

## Try it in five minutes

Install a model that supports tool calling and the Sandbox add-on. Then, in a
new chat, type:

> Create a CSV of the numbers 1 to 10 and their squares, then use Python to
> sum the squares and tell me the result.

The model calls `bash`, the chat shows the command and its output, and the
CSV appears in your files. What the command changed in your files is saved as
one change set, so you can undo it in **Files**, **History**.

## What the sandbox has

The sandbox image carries these programs: python3 (pandas, openpyxl, docx,
pptx, pypdf, Pillow), pip, jq, ripgrep (`rg`), sqlite3, zip, unzip, file, tree,
poppler (`pdftotext`, `pdftoppm`), git and curl. It has no Node.js, ImageMagick,
ffmpeg or OCR. `apt` cannot install anything. `pip` can install Python
packages when the network allows it.

## The network switch

The sandbox has no network device. Commands reach HTTP and HTTPS only through
a proxy that asks LocalAI about each connection. Each chat has a **Network**
row in its **Tools** menu with three choices:

| Choice | A command reaches |
|---|---|
| Off | Nothing. This is the default. |
| Package registries | The package registries (for example PyPI, the npm registry, Go modules, crates.io, RubyGems, Maven Central and GitHub releases). |
| All public sites | Every public host. |

The choice that applies is the lower of the chat's choice and an
administrator's limit. With the default limit `off`, the row reads "Off: the
administrator has not allowed network access". An administrator raises the
limit on the **Settings** tab of the Sandbox on the **Add-ons** page.

When a command asks for a host the chat does not allow but the limit would,
the chat shows a card with **Allow once**, **Allow for this chat** and
**Deny**. The command waits up to 60 seconds for your answer. Triggers,
connector runs and sub-agents show no card: the connection is blocked. A host
inside your own network needs an administrator.

## Where the commands run

An administrator chooses the backend on the Sandbox **Settings** tab.

| Choice | What it is |
|---|---|
| Automatic | KVM when the host has it, otherwise gVisor, otherwise nothing. Never the host backend. |
| gVisor | `runsc`. Needs a non-root user and permission to create a user namespace. |
| KVM | Cloud Hypervisor. Needs `/dev/kvm`. |
| Off | No sandbox. |
| Host | Runs commands on the host, with no isolation. |

Only gVisor and KVM isolate. The Sandbox page tells every user which backend
runs the code, and says so plainly when commands run on the host.

## Limits

- One sandbox for each pair of user and chat. A user has at most 4 sandboxes
  and the server at most 16. The oldest idle one gives way.
- A call stops after its timeout: 120 seconds by default and at most 1680
  seconds.
- Output is cut at 64 KiB.
- Without a working backend, nothing runs. The chat is told, and the model
  says it cannot run code instead of trying.

## Security notes

- Isolation covers the commands the model runs. The add-on process itself is
  trusted code like every add-on (see [Use add-ons]({{% relref "features/using-addons" %}})).
- Without the Sandbox, a chat can run commands on the LocalAI machine with no
  isolation only when an administrator allows it (`chats_host_exec`). Install
  the Sandbox instead.
- An agent that works in one folder runs commands only in the Sandbox.
- Document skills for Word, Excel, PowerPoint and PDF files are offered only
  when the Sandbox reports an isolated backend (see
  [Office documents]({{% relref "features/office-documents" %}})).

Full reference, including network rules, the audit and the cluster behaviour:
[Sandbox in Add-on contracts]({{% relref "features/addons#sandbox" %}}) and
[Running commands]({{% relref "features/chats#running-commands" %}}).
