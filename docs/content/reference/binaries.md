+++
disableToc = false
title = "LocalAI binaries"
weight = 30
description = "Release binaries of the local-ai server, their file names and how to download one by hand."
keywords = ["binaries","download","macos","linux","windows"]
+++

The `local-ai` server is one file per platform on the [releases page](https://github.com/mudler/LocalAI/releases). The [install script]({{% relref "/start/install#install-script" %}}) downloads the right one for you and checks it. Download by hand when you want to choose the file yourself. On Windows, use the [PowerShell install script]({{% relref "/start/install/windows" %}}), or download the Windows binary listed below; WSL2 and a [container]({{% relref "/start/install/containers" %}}) also work.

## Files

Each release has these files, with the release tag in the name:

| OS | File |
| --- | --- |
| Linux (amd64) | `local-ai-{{< version >}}-linux-amd64` |
| Linux (arm64) | `local-ai-{{< version >}}-linux-arm64` |
| macOS (Apple Silicon) | `local-ai-{{< version >}}-darwin-arm64` |
| Windows (amd64) | `local-ai-{{< version >}}-windows-amd64.exe` |

The Windows binary is signed with Authenticode when the project has a signing certificate for that release. The checksums cover the signed file. There is no binary for Intel Macs. The checksums of the release are in `LocalAI-{{< version >}}-checksums.txt`. Download from `https://github.com/mudler/LocalAI/releases/download/{{< version >}}/`.

## Download and run

This command picks the file for the machine it runs on, from the latest release shown in these docs:

```bash
os=$(uname -s | tr A-Z a-z)
arch=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
tag={{< version >}}
curl -fLo local-ai "https://github.com/mudler/LocalAI/releases/download/$tag/local-ai-$tag-$os-$arch"
chmod +x local-ai
./local-ai
```

To check the download, get the checksum file and compare:

```bash
curl -fLO "https://github.com/mudler/LocalAI/releases/download/$tag/LocalAI-$tag-checksums.txt"
grep " local-ai-$tag-$os-$arch\$" LocalAI-$tag-checksums.txt
sha256sum local-ai   # on macOS: shasum -a 256 local-ai
```

The two hashes must match.

## Backends

A binary doesn't bundle backends. LocalAI downloads each backend as an OCI image the first time a model needs one. See [Backends]({{% relref "/models/backends" %}}).

The llama-cpp backend also has a native Windows build, installed from a Windows backend image; see the [Windows guide]({{% relref "/start/install/windows" %}}).
