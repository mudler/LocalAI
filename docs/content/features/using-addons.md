+++
disableToc = false
title = "Use add-ons"
weight = 19
url = "/features/using-addons/"
+++

An add-on is an extension that you install into LocalAI. It can give the model
new tools (run code, read a calendar, search a repository), add a page to the
web UI (Studio, Tables), or connect a chat app (Slack, Telegram, Discord).
This page is the short guide. The full contract for add-on authors and
operators is [Add-on contracts]({{% relref "features/addons" %}}).

## What you can install

The official library lists these first-party add-ons. Each entry is checked
against the library's description in `gallery/addons/index.yaml`.

| Add-on | What it does |
|---|---|
| Sandbox | Runs commands and code in an isolated sandbox. See [Sandbox]({{% relref "features/sandbox" %}}). |
| Simple Office | Reads, edits and creates Word, Excel, PowerPoint and PDF files. See [Office documents]({{% relref "features/office-documents" %}}). |
| Studio | Makes images, video, 3D models, speech, sound and transformed audio. See [Studio]({{% relref "features/studio" %}}). |
| Talk | A spoken conversation with a model. |
| Voice, Face | Speaker recognition and voices for text to speech; face matching in photos. |
| Tables | Databases of tables you own or share. See [Tables]({{% relref "features/tables" %}}). |
| Calendar, Contacts | Your agents read and keep a calendar and an address book inside LocalAI. |
| Web | Web search with Brave Search, Tavily or Google, and reading PDF files on the web. See [Web]({{% relref "features/web" %}}). |
| GitHub, Jellyfin, Home Assistant, PiKVM | Let your agents use these services. Writing needs your permission. |
| Slack, Telegram, Discord | Bind a bot to one of your saved agents. See [Connectors]({{% relref "features/chats-connectors" %}}). |
| Fine-tuning, Quantization | Train an adapter on your data; shrink a model to a smaller file. |

Memory is not an add-on any more. It is built in (see [Short memory]({{% relref "features/short-memory" %}})).

## Who can install and use them

- An administrator installs, updates and removes add-ons. With authentication
  off, you are the administrator.
- With authentication on, the **Add-ons** page is for people who hold the
  `addons` feature. It is off by default for a user and an administrator
  switches it on in **Settings, Users and keys**. See
  [Users and features]({{% relref "features/users-and-features" %}}).
- The add-on's own tools also check the features of the person who uses them.

## Install one in a minute

1. Open **Add-ons** in the sidebar, then **Find add-ons**.
2. Pick an add-on and choose **Install and turn on**. The package is
   downloaded, its signature is checked against the library's policy, and then
   it starts.
3. It now shows in **My add-ons**. Choose **Show in sidebar** in its menu to
   pin it.
4. To use its tools in a chat, switch the add-on on for that chat in the
   chat's **Tools** menu, or list it in a [saved agent]({{% relref "features/chats-agents" %}}).

An install needs the network to reach the library. Keep the `addons` folder
of the data path on a volume in a container, or you must install again when
the container is replaced.

From a terminal you can check and upload a package file with `local-ai addons
inspect <file.zip>` and `local-ai addons install <file.zip>`. The second
command uploads the file to a running server and you finish the install in the
browser. Library sources are managed with `local-ai library list`, `add`,
`remove`, `enable` and `disable`.

## Updates

The **Updates** area of the Add-ons page checks the library and lists the
pending updates with their versions. Review them, select the ones you want and
upgrade. Nothing runs before you confirm.

## Trust and limits

{{% notice warning %}}
An installed add-on is trusted code. It is not sandboxed. It runs as the
operating system user that runs LocalAI, with that user's files and network.
Install only add-ons from a library you trust.
{{% /notice %}}

- The signature check proves where a package came from. It does not make the
  code safe.
- The only add-on that confines other code is Sandbox, and only for the
  commands the model runs through it. The add-on itself is not confined.
- Add-ons never decide who you are or whether you approved a change. LocalAI
  supplies the user and asks you to confirm changes that last (see
  [Work with a chat]({{% relref "features/chat-control" %}})).
- The Sandbox add-on is Linux only. No add-on is built for Windows yet.
- `--addons-enabled=false` turns the add-on owner off, so nothing can be
  installed. `--addons-official-library=false` starts without the official
  library.
