---
title: "macOS Installation"
description: "Install LocalAI on macOS using the DMG application"
weight: 80
aliases:
  - "/getting-started/macos/"
devbuild: install
---


The easiest way to install LocalAI on macOS is using the DMG application.

## Download

Download the latest DMG from GitHub releases:

[**Download LocalAI.dmg**](https://github.com/mudler/LocalAI/releases/latest/download/LocalAI.dmg)

## Installation Steps

1. Download the `LocalAI.dmg` file from the link above
2. Open the downloaded DMG file
3. Drag the LocalAI application to your Applications folder
4. Launch LocalAI from your Applications folder

## First Launch

The app you installed is a small launcher that manages the LocalAI server for
you. On the first launch it offers to download and install the latest server
release; once that finishes, the server starts automatically and on every
following launch of the app.

The launcher lives in the **menu bar** (look for the LocalAI icon in the top
right of your screen) and does not open a window of its own. From the menu bar
icon you can start and stop the server, open the WebUI, check for updates, and
change settings, including turning off the automatic server start
("Start LocalAI when the launcher opens" under Settings).

Once the server is running, the WebUI is available at
`http://localhost:8080`.

## Verification

The `LocalAI.dmg` (and the app inside it) and the `local-ai` server binary are
signed with an Apple Developer ID and notarized by Apple, so they launch with no
quarantine prompt or workaround. To inspect the signature yourself:

```bash
spctl --assess --type open --context context:primary-signature -v /Applications/LocalAI.app
codesign --verify --deep --strict --verbose=2 /Applications/LocalAI.app
```

## New launcher (preview)

A new launcher is in preview. Its window shows the LocalAI WebUI, with a small
status bar at the bottom. Releases carry both launchers during the preview.
Download `LocalAI-Launcher-Preview.dmg` from
[GitHub Releases](https://github.com/mudler/LocalAI/releases). It is for Apple
Silicon (M1 and later) only, as before. Open the DMG and drag
**LocalAI Launcher** to Applications. The app is signed and notarized.

When the preview ends, the new launcher replaces the older one and takes the
name `LocalAI.dmg`.

**Microphone and camera.** The WebUI has voice features. When you start one,
macOS asks once for access to the microphone (or the camera). If you never use
these features, macOS never asks. The launcher allows access only for the
LocalAI server that it shows.

**Updates.** The new launcher updates itself from GitHub Releases. It tells you
when an update is ready. Select **Restart to update** to install it. It offers
stable releases only. To get release candidates too, turn on **Also offer
release candidates** in Settings, under Updates. The older
launcher cannot update itself to the new one: download the new launcher once.
It uses the same `~/.localai/` folder and the same settings.

### Development builds for testers

Each change to the launcher on the `master` branch makes a development DMG.
It is a GitHub Actions artifact, so you must sign in to GitHub to download it.
It is kept for 14 days. See
[Development builds for testers]({{% relref "/start/install/linux" %}}#development-builds-for-testers)
for the commands, and use the artifact `localai-launcher-dev-macos-arm64`.

A development DMG is normally signed and notarized. If it is not (the
maintainers can turn signing off for development builds), macOS stops the first
start. Open **System Settings > Privacy & Security**, and select **Open Anyway**
next to the message about LocalAI Launcher. A development build never updates
itself.

## Next Steps

After installing LocalAI, you can:

- Access the WebUI at `http://localhost:8080`
- [Try it out with examples]({{% relref "/start/try-it-out" %}})
- [Learn about available models]({{% relref "/models" %}})
- [Customize your configuration]({{% relref "/models/model-configuration" %}})
