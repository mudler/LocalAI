---
weight: 2
title: "Installation"
description: "How to install LocalAI"
type: chapter
icon: download
---


LocalAI can be installed in multiple ways depending on your platform and preferences. Choose the installation method that best suits your needs:

- **[macOS](macos/)**: Download and install the DMG application for macOS
- **[Docker](docker/)**: Run LocalAI using Docker containers (recommended for most users)
- **[Linux](linux/)**: Install on Linux using the one-liner script or binaries
- **[Kubernetes](kubernetes/)**: Deploy LocalAI on Kubernetes clusters
- **[Build from Source](build/)**: Build LocalAI from source code

## Quick Start

The fastest way to get started depends on your platform:

- **macOS**: Download the [DMG](macos/)
- **Linux**: Use the `curl https://localai.io/install.sh | sh` [one-liner](linux/)
- **Docker**: Run `docker run -p 8080:8080 --name local-ai -ti localai/localai:latest-aio-cpu`
