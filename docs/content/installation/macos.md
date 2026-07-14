---
title: "macOS Installation"
description: "Install LocalAI on macOS using the DMG application"
weight: 1
---


The easiest way to install LocalAI on macOS is using the DMG application.

## Download

Download the latest DMG from GitHub releases:

<a href="https://github.com/mudler/LocalAI/releases/latest/download/LocalAI.dmg">
  <img src="https://img.shields.io/badge/Download-macOS-blue?style=for-the-badge&logo=apple&logoColor=white" alt="Download LocalAI for macOS"/>
</a>

## Installation Steps

1. Download the `LocalAI.dmg` file from the link above
2. Open the downloaded DMG file
3. Drag the LocalAI application to your Applications folder
4. Launch LocalAI from your Applications folder

## Verification

The `LocalAI.dmg` (and the app inside it) and the `local-ai` server binary are
signed with an Apple Developer ID and notarized by Apple, so they launch with no
quarantine prompt or workaround. To inspect the signature yourself:

```bash
spctl --assess --type open --context context:primary-signature -v /Applications/LocalAI.app
codesign --verify --deep --strict --verbose=2 /Applications/LocalAI.app
```

## Next Steps

After installing LocalAI, you can:

- Access the WebUI at `http://localhost:8080`
- [Try it out with examples](/basics/try/)
- [Learn about available models](/models/)
- [Customize your configuration](/advanced/model-configuration/)
