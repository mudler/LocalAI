---
title: "Install LocalAI"
description: "Install LocalAI with the install script, the macOS app, a container, Kubernetes or from source."
icon: download
weight: 40
aliases:
  - "/installation/"
keywords: ["install", "script", "docker", "macos", "linux", "kubernetes", "setup"]
---

Start with the install script on Linux, or on a Mac with Apple Silicon. On Windows, on an Intel Mac, in a cluster, or when you want to keep LocalAI apart from the host, use a container. On a Mac you can also use the app.

| You have | Use | Command or guide |
|---|---|---|
| Linux, or a Mac with Apple Silicon | Install script | `curl -sSL https://localai.io/install.sh \| sh` |
| Windows (WSL2), an Intel Mac, a server, or a GPU image | Container | [Containers]({{% relref "/start/install/containers" %}}) |
| A Mac and you want an app | macOS app | [macOS]({{% relref "/start/install/macos" %}}) |
| Linux with a desktop | Launcher or binary | [Linux]({{% relref "/start/install/linux" %}}) |
| A cluster | Helm chart | [Kubernetes]({{% relref "/start/install/kubernetes" %}}) |
| A change to the code | Source | [Build from source]({{% relref "/contributing/build-from-source" %}}) |

## Install script

The script downloads the release binary for your machine, checks it against the checksums of the release, and puts it on your `PATH`. It builds nothing. Running it twice changes nothing.

```bash
curl -sSL https://localai.io/install.sh | sh
local-ai run qwen3-4b
```

The API and the web interface are then at `http://localhost:8080`.

You can set these environment variables before the command:

| Variable | Effect |
|---|---|
| `LOCALAI_VERSION` | Install a given release, for example `v4.7.1`. The default is the latest release. |
| `INSTALL_DIR` | Install into this folder. The default is `/usr/local/bin`, or `~/.local/bin` when `/usr/local/bin` is not writable. |
| `LOCALAI_FORCE` | Set to `1` to install again when the same build is present. |
| `LOCALAI_NO_SUDO` | Set to `1` to never ask for `sudo`. LocalAI then installs into `~/.local/bin`. |

The script supports Linux and macOS on amd64 and arm64. There is no binary for Intel Macs. Use a container there. For a different install, see the other guides.

## Container

```bash
docker run -p 8080:8080 --name local-ai -ti localai/localai:latest
```

Podman works the same way. For GPU images, see [Containers]({{% relref "/start/install/containers" %}}).

## macOS app

Download [LocalAI.dmg](https://github.com/mudler/LocalAI/releases/latest/download/LocalAI.dmg), open it and move LocalAI to Applications. The DMG is signed and notarized. See [macOS]({{% relref "/start/install/macos" %}}) for the details.

## What next

Run a first model with the [quickstart]({{% relref "/start/quickstart" %}}).
