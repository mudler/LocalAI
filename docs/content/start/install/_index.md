---
title: "Install LocalAI"
description: "Install LocalAI with the install script, a binary, a container or Kubernetes. The desktop app is in the 5.0 release."
icon: download
weight: 40
aliases:
  - "/installation/"
keywords: ["install", "desktop", "android", "launcher", "appimage", "script", "docker", "macos", "linux", "kubernetes", "setup"]
---

Install the server with a script, a binary, a container or Helm. Windows has a PowerShell install script and a native binary. The desktop app, which installs and runs the server for you, is in the 5.0 release.

| You have | Use | Command or guide |
|---|---|---|
| Linux, or a Mac with Apple Silicon | Install script | See [below](#install-script) |
| Linux or macOS, and you want to choose the file | Binary | [Binary]({{% relref "/start/install/linux" %}}) |
| Windows on amd64 | PowerShell script or binary | [Windows]({{% relref "/start/install/windows" %}}) |
| An Intel Mac, a server, or a GPU image | Container | [Containers]({{% relref "/start/install/containers" %}}) |
| A cluster | Helm chart | [Kubernetes]({{% relref "/start/install/kubernetes" %}}) |
| An Android phone, and a server to connect to | Android app | [Android app]({{% relref "/start/install/android" %}}) |
| A change to the code | Source | [Build from source]({{% relref "/contributing/build-from-source" %}}) |

## Install script

The script downloads the release binary for your machine, checks it against the checksums of the release, and puts it on your `PATH`. It builds nothing. Running it twice changes nothing.

```bash
curl -sSL https://localai.io/install.sh | sh
```

The script installs the `local-ai` program and does not start it. If the script says the folder is not on your `PATH`, run the `export` line it prints. After the script, run `local-ai`, open `http://localhost:8080` and install a model from the UI. The [quickstart]({{% relref "/start/quickstart" %}}) has the details.

If a release has no checksum file, the script says so and installs without checking.

You can set these environment variables before the command:

| Variable | Effect |
|---|---|
| `LOCALAI_VERSION` | Install a given release, for example `v4.7.1`. The default is the latest release. |
| `INSTALL_DIR` | Install into this folder. The default is `/usr/local/bin`, using `sudo` when the folder is not writable and `sudo` is available. Without `sudo`, or with `LOCALAI_NO_SUDO=1`, the script uses `~/.local/bin`. |
| `LOCALAI_FORCE` | Set to `1` to install again when the same build is present. |
| `LOCALAI_NO_SUDO` | Set to `1` to never ask for `sudo`. LocalAI then installs into `~/.local/bin`. |
| `GITHUB_TOKEN` | A GitHub token, used only to avoid GitHub API rate limits when the script looks up the latest release. |

The script supports Linux on amd64 and arm64, and macOS on arm64 (Apple Silicon). There is no binary for Intel Macs. Use a container there. On Windows, use the PowerShell script in [Windows]({{% relref "/start/install/windows" %}}).

### Install a worker

The script has a worker mode that installs the binary and then a service that runs `local-ai worker` and joins a cluster. You normally do not type this yourself: an admin mints a join token on the Workers page (**Add worker**), and the page gives you a command such as `curl -fsSL 'https://ai.example.com/join.sh?t=...' | sudo sh`, which runs this mode. See [Join tokens]({{% relref "/administration/distributed-mode" %}}#join-tokens).

```bash
LOCALAI_REGISTER_TO=https://ai.example.com LOCALAI_JOIN_TOKEN=ljt_... sudo -E sh install.sh --worker
```

Run it as root. It writes a systemd unit on Linux (a launchd daemon on macOS, not tested on every macOS version), keeps the settings in `/etc/localai/worker.env` (readable by root only), and starts the service. The join token is read from the environment, never from a command line, and the script never prints it. Running it again is safe: the worker keeps its own credential in its state directory and needs the token once.

| Variable | Effect |
|---|---|
| `LOCALAI_REGISTER_TO` | The URL of the frontend. Required. |
| `LOCALAI_JOIN_TOKEN` | The join token. Required the first time. |
| `LOCALAI_WORKER_ROUTES` | `nats`, `websocket` or both. Optional. |
| `LOCALAI_WORKER_NAME` | The node name. The default is the host name. |
| `LOCALAI_WORKER_DIR` | The state directory (models, backends, credential). The default is `/var/lib/local-ai` on Linux. |

To remove the worker: `sudo systemctl disable --now localai-worker`, then remove `/etc/localai/worker.env`, `/etc/systemd/system/localai-worker.service` and the state directory, and delete the worker on the Workers page.

## Container

```bash
curl -fsSL https://localai.io/install/docker-compose.yaml -o docker-compose.yaml
docker compose up -d
```

The compose file runs the CPU image and keeps models, backends, settings and data in named volumes. For GPU images, `docker run`, Podman and file permissions, see [Containers]({{% relref "/start/install/containers" %}}).

## Desktop app

The desktop app installs, updates, starts and stops the LocalAI server on your computer and shows the web interface in a window. It can also connect to a LocalAI server that runs somewhere else.

The 5.0 release has the DMG for a Mac with Apple Silicon and the AppImages for Linux (x86-64 and ARM64). Later releases also have a Windows installer for x86-64. Download them from the [latest release](https://github.com/mudler/LocalAI/releases/latest). See [Desktop app]({{% relref "/start/install/desktop" %}}) for the steps, the first start, the update channels and troubleshooting.

## Android app

The Android app shows the web interface of a LocalAI server on your phone. It runs nothing on the phone. It connects to a server by its address, or over P2P with an invite link. See [Android app]({{% relref "/start/install/android" %}}) for the install steps, the sign-in, P2P and updates.

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
./local-ai-master-linux-amd64
```

On macOS, use `shasum -a 256 --check` instead of `sha256sum --check`. The command must print `OK`. If it does not, delete the files and download them again.

The Development update channel of the [desktop app]({{% relref "/start/install/desktop" %}}#update-channels) downloads these binaries.

## What next

Run a first model with the [quickstart]({{% relref "/start/quickstart" %}}).
