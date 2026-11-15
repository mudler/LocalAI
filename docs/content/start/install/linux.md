---
title: "Linux Installation"
description: "Install LocalAI on Linux using binaries"
weight: 70
aliases:
  - "/installation/linux/"
---

## Desktop launcher AppImage (x86_64)

Download `LocalAI-Launcher-x86_64.AppImage` and its `.sha256` file from
[GitHub Releases](https://github.com/mudler/LocalAI/releases). In that directory:

```sh
sha256sum --check LocalAI-Launcher-x86_64.AppImage.sha256
chmod +x LocalAI-Launcher-x86_64.AppImage
./LocalAI-Launcher-x86_64.AppImage
```

You can also double-click the executable image in a file manager. Normal
AppImage mounting requires FUSE support (on Ubuntu 22.04, `libfuse2`). If FUSE
is unavailable, extract it into a writable directory and run the launcher:

```sh
./LocalAI-Launcher-x86_64.AppImage --appimage-extract
./squashfs-root/AppRun
```

This is the existing desktop/tray launcher, not the server bundled into an
image. First use needs internet access to download LocalAI. Models and backends
are downloaded separately. The launcher stores configuration in
`~/.localai/launcher.json`, runtime downloads in `~/.localai/bin`, and default
models/backends under `~/.localai/`. Fyne preferences use the user configuration
directory. These remain writable outside the read-only image. Keep your home
writable; replacing or deleting the AppImage does not delete your data.

The supported release target is **x86_64 Ubuntu 22.04 or newer**, with glibc
2.35+, OpenGL and X11 desktop libraries (XWayland on Wayland). A tray-enabled
desktop is recommended. This is not a static executable: other distributions
must provide compatible graphics libraries. This launcher has no ARM64 AppImage;
use the new launcher (preview) below or the server binaries instead. Server/backend requirements
are separate from launcher requirements. Replace the AppImage manually to
update the launcher; there is no AppImage self-updater.

For build dependencies, pinned tools and smoke checks, see
[the AppImage build guide](https://github.com/mudler/LocalAI/blob/master/contrib/appimage/README.md).

### New launcher (preview)

A new launcher is in preview. Its window shows the LocalAI WebUI, with a small
status bar at the bottom. Releases carry both launchers during the preview.
The new launcher has its own file names:

| Computer | File |
|---|---|
| x86_64 (amd64) | `LocalAI-Launcher-Preview-x86_64.AppImage` |
| ARM64 (aarch64) | `LocalAI-Launcher-Preview-arm64.AppImage` |

Each file has a `.sha256` file. Download both from
[GitHub Releases](https://github.com/mudler/LocalAI/releases), then:

```sh
sha256sum --check LocalAI-Launcher-Preview-x86_64.AppImage.sha256
chmod +x LocalAI-Launcher-Preview-x86_64.AppImage
./LocalAI-Launcher-Preview-x86_64.AppImage
```

When the preview ends, the new launcher replaces the older one and takes the
names `LocalAI-Launcher-x86_64.AppImage` and `LocalAI-Launcher-arm64.AppImage`.

**FUSE.** An AppImage needs FUSE 2 to mount itself. Ubuntu 24.04 and later do not
install it by default. Install it with `sudo apt install libfuse2t64` (Ubuntu
24.04 and later) or `sudo apt install libfuse2` (Ubuntu 22.04). If you cannot
install FUSE 2, start the AppImage with this option. It extracts the image to a
temporary folder and runs it from there:

```sh
./LocalAI-Launcher-Preview-x86_64.AppImage --appimage-extract-and-run
```

**Requirements.** The image contains Electron and its Chromium, so it is about
120 MB and needs no graphics libraries of the system other than those of a
normal desktop (X11 or Wayland). The configuration, the downloaded server and
your models are in `~/.localai/`, as with the older launcher.
To move the models or backends folder, open Settings and select **Change…**
next to the folder. Choose a folder in the dialog; you cannot type a path. Your
home folder itself and the top folder of the disk are not accepted. A running
server uses the new folder after a restart.

**Updates.** The new launcher updates itself from GitHub Releases. It tells you
when an update is ready. Select **Restart to update** to install it. It also installs
and updates the LocalAI server, and checks the server download against the
release checksum file. A download without a valid checksum is not installed.
It offers stable releases only, also when you run a release candidate. To get
release candidates too, turn on **Also offer release candidates** in
Settings, under Updates.

#### What changed from the older launcher

- The window shows the LocalAI WebUI. The older launcher lived in the tray and
  opened the WebUI in your browser.
- There is an ARM64 AppImage.
- The launcher updates itself. The older launcher had no self-updater.
- The image is larger (about 120 MB instead of a small Go program), because it
  contains Chromium.
- Chromium runs pages in a sandbox. See the next section for Ubuntu 24.04 and
  later.
- The older launcher cannot update itself to the new one. Download the new
  launcher once. It uses the same `~/.localai/` folder and the same settings.

#### Development builds for testers

Each change to the launcher on the `master` branch makes development builds.
They are GitHub Actions artifacts, not release files, so you must sign in to
GitHub to download them. Each one contains the AppImage (or the DMG) and its
`.sha256` file. Master builds are kept for 14 days and pull request builds for
7 days. Pull requests have only the x86_64 AppImage.

With the GitHub CLI (`gh auth login` first):

```sh
# The last runs on master
gh run list --repo mudler/LocalAI --workflow launcher-electron-dev.yaml --branch master --limit 5
# Download the x86_64 AppImage of one run (use -linux-arm64 or -macos-arm64 for the others)
gh run download RUN_ID --repo mudler/LocalAI --name localai-launcher-dev-linux-amd64
sha256sum --check LocalAI-Launcher-x86_64.AppImage.sha256
```

A development build has a version like `4.12.2-dev.42`. It never updates
itself. To get a newer one, download it again.

### Sandbox on Ubuntu 24.04 and later

Chromium, which the launcher uses for its window, runs its pages in a sandbox. The sandbox needs
unprivileged user namespaces. Ubuntu 23.10 and later (and distributions based on them) set
`kernel.apparmor_restrict_unprivileged_userns=1`. With this setting, a program that has no AppArmor
profile cannot create a user namespace. An AppImage cannot install a profile by itself.

In this case the launcher starts with the sandbox turned off (`--no-sandbox`). The launcher window
shows the LocalAI WebUI, so the WebUI pages, model output and MCP app frames run without that extra
isolation. The status bar shows a **Sandbox off** chip. Select it to open a short guide. This is not a
fault: LocalAI works the same way with or without the sandbox. Debian, Fedora and Arch do not set this
restriction by default, so they show no chip.

If you want the extra isolation now, select **open the WebUI in my browser instead** in the guide, or
turn on **Open the WebUI in my browser instead** in Settings. The launcher then opens the WebUI in your
browser, which has its own sandbox, and the window shows only the launcher.

#### Sandbox profile (not available yet)

The guide shows a **Set up sandbox profile** button, but it is disabled: setup is not available yet.
AppArmor attaches a profile to the path of the program that runs. A mounted AppImage runs from a folder
with a random name that users can write to, so a profile for that path would loosen the protection for
other programs too. The planned fix for Ubuntu is a `.deb` package that installs the profile for a path
that only root can change. Until then, the AppImage has no profile setup.

Select **Not now** to close the guide. The launcher remembers this choice for the current launcher
version. The chip stays in the status bar, and Settings shows the sandbox state.

#### System-wide setting (not recommended)

You can turn the restriction off for all programs:

```sh
sudo sysctl -w kernel.apparmor_restrict_unprivileged_userns=0
```

{{% callout style="warning" %}}
This setting loosens a protection for every program on the computer, not only for the launcher. It is
not recommended. Open the WebUI in your browser instead. The command lasts until the next restart; do
not make it permanent unless you understand the risk.
{{% /callout %}}

## Manual Installation

### Download Binary

You can manually download the appropriate binary for your system from the [releases page](https://github.com/mudler/LocalAI/releases):

1. Go to  [GitHub Releases](https://github.com/mudler/LocalAI/releases)
2. Download the binary for your architecture (amd64, arm64, etc.)
3. Make it executable:

```bash
chmod +x local-ai-*
```

4. Run LocalAI:

```bash
./local-ai-*
```

### Run your first model

Starting the binary on its own gives you an empty server. To get a working chat right away, run LocalAI with a model name and it will download and serve it from the gallery:

```bash
./local-ai-* run qwen3-4b
```

Once it is ready, open the WebUI at `http://localhost:8080` or send a request to the API:

```bash
curl http://localhost:8080/v1/chat/completions -H "Content-Type: application/json" -d '{
  "model": "qwen3-4b",
  "messages": [{"role": "user", "content": "Hello!"}]
}'
```

### System Requirements

Hardware requirements vary based on:
- Model size
- Quantization method
- Backend used

For performance benchmarks with different backends like `llama.cpp`, visit [this link](https://github.com/ggerganov/llama.cpp#memorydisk-requirements).

## Configuration

After installation, you can:

- Access the WebUI at `http://localhost:8080`
- Configure models in the models directory
- Customize settings via environment variables or config files

## Start LocalAI on demand with systemd

LocalAI accepts a single TCP listener passed through the systemd socket
activation protocol. This lets systemd listen on the public port and start
LocalAI only when the first client connects.

Create `/etc/systemd/system/local-ai.socket`:

```ini
[Unit]
Description=LocalAI API socket

[Socket]
ListenStream=8080
NoDelay=true

[Install]
WantedBy=sockets.target
```

Create the matching `/etc/systemd/system/local-ai.service`:

```ini
[Unit]
Description=LocalAI

[Service]
Type=simple
User=localai
Group=localai
ExecStart=/usr/local/bin/local-ai run
WorkingDirectory=/var/lib/local-ai
```

Adjust the user, binary path, working directory, and model configuration for
your installation. Then enable the socket, not the service:

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now local-ai.socket
```

The first connection to port 8080 starts `local-ai.service`; systemd holds that
connection until LocalAI is ready to accept it. `LOCALAI_ADDRESS` and
`--address` are ignored while an inherited listener is present. LocalAI
rejects activation with multiple stream listeners so it cannot silently choose
the wrong endpoint.

For a Podman-managed container, configure Podman to preserve and pass the
systemd socket file descriptor into the container. The LocalAI process inside
the container consumes the same activation protocol.

Activation needs both `LISTEN_PID` and `LISTEN_FDS`. If only one of them is set,
LocalAI ignores them and binds `--address` as usual. A container engine started
from a socket-activated system unit can leak a bare `LISTEN_PID` into every
container it spawns, and that is not an activation attempt.

## Next Steps

- [Try it out with examples]({{% relref "/start/try-it-out" %}})
- [Learn about available models]({{% relref "/models" %}})
- [Configure GPU acceleration]({{% relref "/models/gpu-acceleration" %}})
- [Customize your configuration]({{% relref "/models/model-configuration" %}})
