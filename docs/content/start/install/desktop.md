---
title: "Desktop app"
description: "Install the LocalAI desktop app on Linux (AppImage), macOS (DMG) or Windows (installer) to run LocalAI on your computer or to connect to a LocalAI server."
icon: desktop_windows
weight: 45
aliases:
  - "/installation/launcher/"
keywords: ["launcher", "desktop", "appimage", "dmg", "macos", "linux", "windows", "installer", "install", "update channel"]
---

The desktop app installs, updates, starts and stops the LocalAI server on your computer and shows the web interface in its own window, with a thin status bar at the bottom. It also has pages for logs, settings and a Connect page that shows how other devices and apps reach the server.

If you want to run LocalAI on a server, in a container or in a cluster, use the server install methods in the [install guide]({{% relref "/start/install" %}}) instead.

{{% callout style="note" %}}
This app replaces the older LocalAI launcher. If you used the older launcher, download this app once, as described below. Your models, backends and settings stay in `~/.localai`. See [Move from the older launcher](#move-from-the-older-launcher).
{{% /callout %}}

## Install on Linux

When the app is released, download the AppImage for your CPU and its checksum file from the [releases page](https://github.com/mudler/LocalAI/releases):

| CPU | File |
|---|---|
| x86-64 | `LocalAI-Launcher-x86_64.AppImage` |
| ARM64 | `LocalAI-Launcher-arm64.AppImage` |

Each file has a checksum file with the same name and the suffix `.sha256`. In the folder with the files, check the download, make the AppImage executable and run it:

```bash
sha256sum --check LocalAI-Launcher-x86_64.AppImage.sha256
chmod +x LocalAI-Launcher-x86_64.AppImage
./LocalAI-Launcher-x86_64.AppImage
```

You can also start the file from your file manager. The file is large (about 120 MB), because it contains Electron and Chromium. It needs the libraries of a normal desktop (X11 or Wayland).

An AppImage needs FUSE 2 to mount itself. Ubuntu 24.04 and later do not install it by default. Install it with `sudo apt install libfuse2t64` (Ubuntu 24.04 and later) or `sudo apt install libfuse2` (Ubuntu 22.04). If you cannot install FUSE 2, start the AppImage with this option. It extracts the image to a temporary folder and runs it from there:

```bash
./LocalAI-Launcher-x86_64.AppImage --appimage-extract-and-run
```

### Sandbox on Ubuntu 24.04 and later

The app uses Chromium, which runs its pages in a sandbox. The sandbox needs unprivileged user namespaces. Ubuntu 23.10 and later, and distributions based on them, set `kernel.apparmor_restrict_unprivileged_userns=1`. With this setting, a program that has no AppArmor profile cannot create a user namespace. An AppImage cannot install a profile by itself.

In this case the app starts with the sandbox turned off (`--no-sandbox`). The window shows the LocalAI web interface, so its pages and model output run without that extra isolation. The status bar shows a **Sandbox off** chip. Select it to open a short guide. LocalAI works the same way with or without the sandbox. Debian, Fedora and Arch do not set this restriction by default, so they show no chip.

For the extra isolation, select **open the WebUI in my browser instead** in the guide, or turn on **Open the WebUI in my browser instead** in Settings. The app then opens the web interface in your browser, which has its own sandbox.

The guide has a **Set up sandbox profile** button, but it is disabled: setup is not available yet. AppArmor attaches a profile to the path of the program that runs. A mounted AppImage runs from a folder with a random name that users can write to, so a profile for that path would weaken the protection for other programs too. Select **Not now** to close the guide. The app remembers this choice for the current app version. The chip stays in the status bar.

You can turn the restriction off for all programs with `sudo sysctl -w kernel.apparmor_restrict_unprivileged_userns=0`.

{{% callout style="warning" %}}
This setting weakens a protection for every program on the computer, not only for the app. Do not use it unless you understand the risk. Open the web interface in your browser instead. The setting lasts until the next restart.
{{% /callout %}}

## Install on macOS

1. When the app is released, download `LocalAI.dmg` from the [releases page](https://github.com/mudler/LocalAI/releases). It is for Macs with Apple Silicon. There is no build for Intel Macs: use a [container]({{% relref "/start/install/containers" %}}) there. The release also has a `.sha256` file for the DMG. Check it with `shasum -a 256 --check`.
2. Open the DMG and drag **LocalAI Launcher** to your Applications folder.
3. Start it from your Applications folder.

The DMG and the app in it are signed with an Apple Developer ID and notarized. The first time a page uses the microphone or the camera, for example for voice input, macOS asks for your permission. If you never use these features, macOS never asks.

To check the signature yourself, run these commands:

```bash
spctl --assess --type execute --verbose "/Applications/LocalAI Launcher.app"
codesign --verify --deep --strict --verbose=2 "/Applications/LocalAI Launcher.app"
```

## Install on Windows

1. When the app is released, download `LocalAI-Launcher-Setup-x64.exe` from the [releases page](https://github.com/mudler/LocalAI/releases). It is for Windows 10 and 11 on x86-64. There is no Windows build for ARM64 CPUs. The release also has a `.sha256` file for the installer.
2. Run the installer. It installs the app for your user account only, in `%LOCALAPPDATA%\Programs\LocalAI Launcher`. It does not ask for administrator rights. It adds a Start menu shortcut and a desktop shortcut.
3. Start **LocalAI Launcher** from the Start menu.

To check the download, run this command in PowerShell in the folder with the files, and compare the result with the first word in the `.sha256` file:

```powershell
(Get-FileHash .\LocalAI-Launcher-Setup-x64.exe -Algorithm SHA256).Hash.ToLower()
```

The release also has `LocalAI-Launcher-win-x64.zip`, a portable copy. Extract it to a folder and run `local-ai-launcher.exe`. The portable copy does not update itself: to update it, download the new zip. Use the installer if you want updates from the app.

{{% callout style="note" %}}
The installer and the app are signed only when the release was built with a code signing certificate. If they are not signed, Windows Defender SmartScreen shows "Windows protected your PC" at the first start. Select **More info**, then **Run anyway**. To see if a file is signed, open its **Properties** and look for the **Digital Signatures** tab.
{{% /callout %}}

When you close the window, the app stays in the notification area of the taskbar and keeps the server running. To quit, right-click the LocalAI icon there and select **Quit**. The local server is the native Windows server. Read [Install on Windows]({{% relref "/start/install/windows" %}}) for what it supports.

## First start

On the first start the app asks you to choose:

- **Run LocalAI on this computer.** The app downloads the latest LocalAI server release into `~/.localai` (`%LOCALAPPDATA%\LocalAI\launcher` on Windows), checks its checksum, and starts it. The first start needs an internet connection. Models and backends are downloaded later, when you use them.
- **Connect to a LocalAI server.** You give the address of a server that runs somewhere else, for example `https://localai.example.com`. The app tests the address and then opens that server in its window.

On Windows the server is `local-ai.exe`, and the app keeps it in `%LOCALAPPDATA%\LocalAI\launcher\bin`.

When the server is ready, the window shows the LocalAI web interface. You can use it as you would in a browser.

### The status bar

The status bar shows the state of the server and a server switcher. To hide or show it, use **Show status bar** in the **View** menu, in the tray menu or in Settings, or press Ctrl+Shift+L (Cmd+Shift+L on macOS).

## Servers

Open **Settings > Servers** to keep several LocalAI servers. One of them is the default.

- **This computer** is the server that the app manages for you.
- A remote server is a server you reach by address. Add it with its address, for example `https://localai.example.com`. The app tests it before it saves it. A server that asks you to sign in counts as reachable. You sign in in the web interface.

If a remote server is the default, the app opens it at once. It does not install, start or update the local server. To keep the local server running in the background anyway, turn on **Keep this computer's server running too**.

To change the server, use the switcher in the status bar. The shortcut is Ctrl+Shift+S, or Cmd+Shift+S on macOS. If you pick a server in the switcher, it becomes the default. If you pick This computer, the app starts it, and installs it first when it is missing.

The app does not save the password or the API key of a remote server. You sign in again in the web interface when the server asks. The API key of the server on this computer is kept encrypted when your system has a keyring. Without a keyring, the app keeps it for the current session only.

If the address is plain `http`, the app shows a warning and a **Remote, not encrypted** chip in the status bar.

## Updates

The app checks for a new LocalAI server version once a day. It never installs an update without asking you. Open **Settings > Updates** to check by hand, to see the installed version and to change the update channel. The app also updates itself from the same releases. On Linux, **Restart to update** replaces the AppImage file. On macOS it installs the update it downloaded. On Windows it runs the new installer, which closes the app, installs the update and starts the app again. The portable zip does not update itself.

### Update channels

| Channel | What it follows |
|---|---|
| Stable | The latest release. This is the default. |
| Release candidates | The newest release, including release candidates. |
| Development | The rolling `dev-master` build of the server, made from the `master` branch. |

The Development channel is not supported. It installs the binaries that are described in [Development builds]({{% relref "/start/install" %}}#development-builds), with names like `local-ai-master-<os>-<arch>`. It also starts the server with the development backend images, by setting `LOCALAI_PREFER_DEV_BACKENDS`. Backends that are already installed stay as they are. The app asks you to confirm the first change to this channel. The app shows a Development chip while you use This computer. If a remote server is the default, the channel applies to This computer only.

When you leave the Development channel with **Back to Stable**, the app keeps the development binary as a rollback copy. The app also keeps the previous binary after each update. If a new server binary does not become ready, the app restores the previous one.

## Development builds for testers

Each change to the app on the `master` branch makes development builds. When the builds pass, the AppImages (x86-64 and ARM64), the DMG, and the Windows installer and zip are attached to the rolling [`dev-master` pre-release](https://github.com/mudler/LocalAI/releases/tag/dev-master), next to the server binaries. Each new build replaces the files of the last one. The builds are also kept as GitHub Actions artifacts, which you can download after you sign in to GitHub. Each artifact contains the AppImage, the DMG, or the Windows installer and zip, each with its `.sha256` file. The file names have `dev` in them, for example `LocalAI-Launcher-dev-x86_64.AppImage`, `LocalAI-Launcher-dev.dmg` and `LocalAI-Launcher-dev-Setup-x64.exe`, so you can tell them from release files. The release files stay until the next master build replaces them. Artifacts of master builds are kept for 14 days and artifacts of pull request builds for 7 days. Pull requests have only the x86-64 AppImage. With the GitHub CLI (run `gh auth login` first):

```bash
# The last runs on master
gh run list --repo mudler/LocalAI --workflow launcher-electron-dev.yaml --branch master --limit 5
# Download the x86-64 AppImage of one run (use -linux-arm64, -macos-arm64 or -windows-amd64 for the others)
gh run download RUN_ID --repo mudler/LocalAI --name localai-launcher-dev-linux-amd64
```

A development build has a version like `5.0.1-dev.42`. It never updates itself. To get a newer one, download it again. A development DMG is normally signed and notarized. If it is not, macOS stops the first start. Open **System Settings > Privacy & Security** and select **Open Anyway** next to the message about LocalAI Launcher.

This is different from the Development update channel above, which updates the LocalAI server.

## Move from the older launcher

The older launcher was a different program with the same purpose. It does not update itself to this app, so download the new file once:

1. Quit the older launcher. If you turned on automatic start in it, turn that off first.
2. Download the new AppImage or the DMG, as described in the install steps above. On Linux, the x86-64 file has the same name as the file of the older launcher, so it replaces that file if you save it in the same folder.
3. On macOS, the new app is **LocalAI Launcher**. Move the older **LocalAI** app from Applications to the Trash.
4. Start the new app.

The new app reads the same `~/.localai` folder. Your models, backends and settings stay. From now on the app updates itself from the releases page.

## Where your data is

On Linux and macOS, the app keeps everything for the local server under `~/.localai`:

- Models and backends. To move them, open Settings and select **Change...** next to the folder, then choose a folder in the dialog.
- `bin`, with the `local-ai` server binary and `local-ai.previous`, the copy that the app keeps for a rollback.
- The server configuration.
- `launcher.json`, the settings of the app, including the saved server addresses.

On Windows, the app uses the same folders as `local-ai.exe` started from a terminal (see [Where LocalAI keeps its files]({{% relref "/start/install/windows" %}}#where-localai-keeps-its-files)), so both find the same models:

- `%LOCALAPPDATA%\LocalAI`: models, backends, the server data and configuration, and the logs.
- `%LOCALAPPDATA%\LocalAI\launcher`: `launcher.json`, the secrets file, and `bin` with `local-ai.exe` and `local-ai.previous.exe`. This is not the `bin` folder of the PowerShell installer, so the two do not replace each other's server.

If an earlier build of the app left a `launcher.json` in `%USERPROFILE%\.localai`, the app keeps using that folder for everything. It does not move files. To move to the standard folders, quit the app, move the `models` and `backends` folders to `%LOCALAPPDATA%\LocalAI`, and move or delete `%USERPROFILE%\.localai`.

On every system, when `LOCALAI_BASE_PATH` is set to a full path, the app uses it as the default data folder instead. Folders that you chose in Settings win.

Do not run the app's server and a `local-ai run` from a terminal at the same time on the same folders.

The app keeps the API key of the local server and other secrets in a separate file next to `launcher.json`. The system keyring encrypts them when one exists. On Windows the app encrypts them for your user account with the Windows data protection API.

To remove the app, delete the AppImage file on Linux, or drag LocalAI Launcher from Applications to the Trash on macOS. On Windows, quit the app, then remove **LocalAI Launcher** in **Settings > Apps > Installed apps** (or delete the folder of the portable copy). This does not delete `~/.localai` (on Windows `%LOCALAPPDATA%\LocalAI`), so your models stay. To remove all data, delete that folder after you quit the app. The app also adds an autostart entry only if you turn on **Launch at login** in Settings. Turn it off before you remove the app.

## Troubleshooting

### The AppImage does not start

If you see an error about FUSE or `libfuse.so.2`, install `libfuse2` (`libfuse2t64` on Ubuntu 24.04 and later), or run the file with `--appimage-extract-and-run` as shown above. Check that the file is executable: `chmod +x LocalAI-Launcher-x86_64.AppImage`.

### Windows asks about the firewall

The app starts the server on `127.0.0.1`, which only this computer can reach, so Windows does not ask. If you let other devices reach the server (the Connect page), the server listens on your network, and Windows Defender Firewall can ask if `local-ai.exe` may accept connections. Allow it for private networks only.

### Stopping the server on Windows

On Windows the app cannot ask the server to shut down. It ends the server and the backends it started at once, with `taskkill`. Requests that run at that time stop. Models, settings and downloads that are complete are not affected.

### The status bar shows "Sandbox off"

This happens on Ubuntu 24.04 and later. See [Sandbox on Ubuntu 24.04 and later](#sandbox-on-ubuntu-2404-and-later). Use **Open in my browser instead** if you prefer a sandboxed browser.

### The remote server does not answer

While the default server does not answer, the app shows a recovery screen and tries again every 30 seconds. It never starts the local server by itself. Check these points:

- The address is correct and includes `https://` or `http://`.
- The server is running. Run `curl https://localai.example.com/readyz` from this computer.
- A firewall, a proxy or a VPN does not block the connection.
- The certificate of the server is valid. The app never skips certificate errors.

Then choose **Retry**, or use the switcher to select This computer.

### The local server does not start

Open the **Logs** page. The last lines show the reason. If an update leaves a server that does not start, the app restores the previous binary.

## Next steps

- [Try it out with examples]({{% relref "/start/try-it-out" %}})
- [Learn about available models]({{% relref "/models" %}})
- [Customize your configuration]({{% relref "/models/model-configuration" %}})
