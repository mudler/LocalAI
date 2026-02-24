---
weight: 2
title: "Installation"
description: "How to install LocalAI"
type: chapter
icon: download
---

LocalAI can be installed in multiple ways depending on your platform and preferences.

## Installation Methods

Choose the installation method that best suits your needs:

1. **[Containers](containers/)** ⭐ **Recommended** - Works on all platforms, supports Docker and Podman
2. **[macOS](macos/)** - Download and install the DMG application
3. **[Linux](linux/)** - Install on Linux using binaries
4. **[Kubernetes](kubernetes/)** - Deploy LocalAI on Kubernetes clusters
5. **[Build from Source](build/)** - Build LocalAI from source code

## Quick Start

**Recommended: Containers (Docker or Podman)**

```bash
# With Docker
docker run -p 8080:8080 --name local-ai -ti localai/localai:latest

# Or with Podman
podman run -p 8080:8080 --name local-ai -ti localai/localai:latest
```

This will start LocalAI. The API will be available at `http://localhost:8080`. For images with pre-configured models, see [All-in-One images](/getting-started/container-images/#all-in-one-images).

For other platforms:
- **macOS**: Download the [DMG](macos/)
- **Linux**: See the [Linux installation guide](linux/) for binary installation.

For detailed instructions, see the [Containers installation guide](containers/).
