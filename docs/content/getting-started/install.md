---
weight: 1
title: "Install LocalAI"
description: "How to install LocalAI"
icon: download
url: '/installation/'
---

LocalAI can be installed in multiple ways depending on your platform and preferences.

## Video Walkthrough

[![Installation Video](https://img.youtube.com/vi/cMVNnlqwfw4/maxresdefault.jpg)](https://www.youtube.com/watch?v=cMVNnlqwfw4)

## Installation Methods

Choose the installation method that best suits your needs:

1. **[Containers]({{% relref "getting-started/containers" %}})** ⭐ **Recommended** - Works on all platforms, supports Docker and Podman
2. **[macOS]({{% relref "getting-started/macos" %}})** - Download and install the DMG application
3. **[Linux]({{% relref "getting-started/linux" %}})** - Install on Linux using binaries
4. **[Kubernetes]({{% relref "getting-started/kubernetes" %}})** - Deploy LocalAI on Kubernetes clusters
5. **[Build from Source]({{% relref "getting-started/build" %}})** - Build LocalAI from source code

## Quick Start

**Recommended: Containers (Docker or Podman)**

```bash
# With Docker
docker run -p 8080:8080 --name local-ai -ti localai/localai:latest

# Or with Podman
podman run -p 8080:8080 --name local-ai -ti localai/localai:latest
```

This will start LocalAI. The API will be available at `http://localhost:8080`.

For other platforms:
- **macOS**: Download the [DMG]({{% relref "getting-started/macos" %}})
- **Linux**: See the [Linux installation guide]({{% relref "getting-started/linux" %}}) for binary installation.

For detailed instructions, see the [Containers installation guide]({{% relref "getting-started/containers" %}}).
