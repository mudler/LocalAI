---
title: "Install on macOS"
description: "Install LocalAI on a Mac with Apple Silicon with the install script. The desktop app is in the 5.0 release."
weight: 80
aliases:
  - "/getting-started/macos/"
---

On a Mac with Apple Silicon, run the install script. On an Intel Mac, use a [container]({{% relref "/start/install/containers" %}}), because there is no macOS binary for Intel.

```bash
curl -sSL https://localai.io/install.sh | sh
```

The [quickstart]({{% relref "/start/quickstart" %}}) shows how to start the server and run a first model. The [install guide]({{% relref "/start/install#install-script" %}}) lists the options of the script.

## Desktop app

The DMG holds an app named **LocalAI Launcher**. It installs and starts the LocalAI server for you and shows the web interface in its own window. The [desktop app guide]({{% relref "/start/install/desktop#install-on-macos" %}}) has the install steps, the signature checks, the first start and how to move from the older launcher.
