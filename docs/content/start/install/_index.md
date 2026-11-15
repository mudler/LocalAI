---
title: "Install LocalAI"
description: "Install LocalAI as a desktop app, or as a server with the install script, a binary, a container or Kubernetes."
icon: download
weight: 40
aliases:
  - "/installation/"
keywords: ["install", "desktop", "launcher", "appimage", "script", "docker", "macos", "linux", "kubernetes", "setup"]
devbuild: install
---

There are two ways to run LocalAI. Pick one first.

- **Desktop app.** The LocalAI launcher is an application for your own computer. It installs, updates, starts and stops the LocalAI server for you, and shows the web interface in its window.
- **Server.** You install the `local-ai` binary, a container image or a Helm chart, and you run and update it yourself. Use this on a headless machine, on a server, in a cluster, or when you want full control.

| | Desktop app | Server |
|---|---|---|
| For | A person who uses LocalAI on a laptop or a desktop | An administrator who runs LocalAI for others, on a server or in a cluster |
| Platforms | Linux (x86-64 and ARM64) and macOS (Apple Silicon) | Linux, macOS (Apple Silicon), Windows with WSL2 or a container, Kubernetes |
| What it installs | The launcher. The launcher then installs the `local-ai` server in `~/.localai` | The `local-ai` binary, a container image or a Helm release |
| How it updates | The launcher offers new server and launcher versions and installs them when you agree | You update it: run the install script again, pull a new image or upgrade the chart |
| Start here | [Desktop app]({{% relref "/start/install/desktop" %}}) | [Install script](#install-script), [Binary]({{% relref "/start/install/linux" %}}), [Container](#container) or [Kubernetes]({{% relref "/start/install/kubernetes" %}}) |

The desktop app can also connect to a LocalAI server that runs somewhere else. So you can install the server on one machine and open it from the launcher on another.

## Path A: desktop app

Download the launcher from the [releases page](https://github.com/mudler/LocalAI/releases). Use `LocalAI-Launcher-x86_64.AppImage` or `LocalAI-Launcher-arm64.AppImage` on Linux, or `LocalAI.dmg` on a Mac with Apple Silicon. See [Desktop app]({{% relref "/start/install/desktop" %}}) for the steps, the first start, the update channels and troubleshooting.

## Path B: server

| You have | Use | Command or guide |
|---|---|---|
| Linux, or a Mac with Apple Silicon | Install script | `curl -sSL https://localai.io/install.sh \| sh` |
| Linux or macOS, and you want to choose the file | Binary | [Binary]({{% relref "/start/install/linux" %}}) |
| Windows (WSL2), an Intel Mac, a server, or a GPU image | Container | [Containers]({{% relref "/start/install/containers" %}}) |
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

## Development builds

Every push to the `master` branch that changes the code is built and published to one rolling pre-release, tagged `dev-master`: [github.com/mudler/LocalAI/releases/tag/dev-master](https://github.com/mudler/LocalAI/releases/tag/dev-master). Each new build replaces the files of the last one. The release notes give the commit of the build.

A development build is not supported. It can have bugs and it can change without notice. Use a stable release for anything that matters. The `dev-master` release is a pre-release, so the install script and the "latest" download links never use it.

The release has these files:

| File | Platform |
|---|---|
| `local-ai-master-linux-amd64` | Linux, x86-64 |
| `local-ai-master-linux-arm64` | Linux, ARM64 |
| `local-ai-master-darwin-arm64` | macOS, Apple Silicon |

Each file has a checksum file with the same name and the suffix `.sha256`. To download and verify a binary:

```bash
base=https://github.com/mudler/LocalAI/releases/download/dev-master
curl -fLO "$base/local-ai-master-linux-amd64"
curl -fLO "$base/local-ai-master-linux-amd64.sha256"
sha256sum --check local-ai-master-linux-amd64.sha256
chmod +x local-ai-master-linux-amd64
```

On macOS, use `shasum -a 256 --check` instead of `sha256sum --check`. The command must print `OK`. If it does not, delete the files and download them again.

The Development update channel of the [desktop app]({{% relref "/start/install/desktop" %}}#update-channels) downloads these binaries.

## What next

Run a first model with the [quickstart]({{% relref "/start/quickstart" %}}).
