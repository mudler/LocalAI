---
weight: 2
title: "Installation"
description: "How to install LocalAI"
type: chapter
icon: download
---

LocalAI can be installed in multiple ways depending on your platform and preferences.

{{% notice tip %}}
**Recommended: Docker Installation**

**Docker is the recommended installation method** for most users as it works across all platforms (Linux, macOS, Windows) and provides the easiest setup experience. It's the fastest way to get started with LocalAI.
{{% /notice %}}

## Installation Methods

Choose the installation method that best suits your needs:

1. **[Docker](docker/)** ⭐ **Recommended** - Works on all platforms, easiest setup
2. **[macOS](macos/)** - Download and install the DMG application
3. **[Linux](linux/)** - Install on Linux using the one-liner script or binaries
4. **[Kubernetes](kubernetes/)** - Deploy LocalAI on Kubernetes clusters
5. **[Build from Source](build/)** - Build LocalAI from source code

## Quick Start

**Recommended: Docker (works on all platforms)**

```bash
docker run -p 8080:8080 --name local-ai -ti localai/localai:latest
```

This will start LocalAI. The API will be available at `http://localhost:8080`. For images with pre-configured models, see [All-in-One images](/getting-started/container-images/#all-in-one-images).

For other platforms:
- **macOS**: Download the [DMG](macos/)
- **Linux**: Use the `curl https://localai.io/install.sh | sh` [one-liner](linux/)

For detailed instructions, see the [Docker installation guide](docker/).
