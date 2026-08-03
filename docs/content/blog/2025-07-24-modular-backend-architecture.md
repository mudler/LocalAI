+++
title = "Backends move outside the main binary"
date = 2025-07-24
description = "All backends migrate out of the main binary, leaving a lightweight modular core that pulls engines on demand."
url = "/blog/modular-backend-architecture/"
+++

All backends have been migrated outside the main binary. The core stays small, and each backend is an isolated service installed on demand.

This is the architecture LocalAI still runs on: install, update or remove engines independently, and mix CPU, NVIDIA, AMD, Intel, Apple Silicon, Vulkan and Jetson in one deployment.

See [Backends]({{% relref "features/backends" %}}) and the [v3.2.0 release notes](https://github.com/mudler/LocalAI/releases/tag/v3.2.0).
