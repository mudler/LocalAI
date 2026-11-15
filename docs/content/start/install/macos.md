---
title: "macOS Installation"
description: "Install LocalAI on macOS using the DMG application"
weight: 80
aliases:
  - "/getting-started/macos/"
devbuild: install
---


The easiest way to install LocalAI on macOS is the DMG application. It is for Macs with Apple Silicon.

## Download

Download the latest DMG from GitHub releases:

[**Download LocalAI.dmg**](https://github.com/mudler/LocalAI/releases/latest/download/LocalAI.dmg)

The app is the LocalAI desktop launcher. It installs and starts the LocalAI server for you and shows the web interface in its own window. See [Desktop app]({{% relref "/start/install/desktop" %}}) for the install steps, the first start, the microphone and camera prompt, updates, and how to move from the older launcher.

## Verification

`LocalAI.dmg`, the app inside it and the `local-ai` server binary are
signed with an Apple Developer ID and notarized by Apple, so they launch with no
quarantine prompt or workaround. To inspect the signature of the DMG and the app yourself:

```bash
spctl --assess --type open --context context:primary-signature --verbose LocalAI.dmg
xcrun stapler validate LocalAI.dmg
spctl --assess --type execute --verbose "/Applications/LocalAI Launcher.app"
codesign --verify --deep --strict --verbose=2 "/Applications/LocalAI Launcher.app"
```

## Next Steps

After installing LocalAI, you can:

- Access the WebUI at `http://localhost:8080`
- [Try it out with examples]({{% relref "/start/try-it-out" %}})
- [Learn about available models]({{% relref "/models" %}})
- [Customize your configuration]({{% relref "/models/model-configuration" %}})
