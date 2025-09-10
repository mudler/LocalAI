
+++
disableToc = false
title = "LocalAI binaries"
weight = 26
+++

LocalAI binaries are available for both Linux and MacOS platforms and can be executed directly from your command line. These binaries are continuously updated and hosted on [our GitHub Releases page](https://github.com/mudler/LocalAI/releases). This method also supports Windows users via the Windows Subsystem for Linux (WSL).

### macOS Download

<a href="https://github.com/mudler/LocalAI/releases/latest/download/LocalAI.dmg">
  <img src="https://img.shields.io/badge/Download-macOS-blue?style=for-the-badge&logo=apple&logoColor=white" alt="Download LocalAI for macOS"/>
</a> 

Use the following one-liner command in your terminal to download and run LocalAI on Linux or MacOS:

```bash
curl -Lo local-ai "https://github.com/mudler/LocalAI/releases/download/{{< version >}}/local-ai-$(uname -s)-$(uname -m)" && chmod +x local-ai && ./local-ai
```

Otherwise, here are the links to the binaries:

| OS | Link | 
| --- | --- |
| Linux (amd64)  | [Download](https://github.com/mudler/LocalAI/releases/download/{{< version >}}/local-ai-Linux-x86_64) |
| Linux (arm64)  | [Download](https://github.com/mudler/LocalAI/releases/download/{{< version >}}/local-ai-Linux-arm64) |
| MacOS (arm64)  | [Download](https://github.com/mudler/LocalAI/releases/download/{{< version >}}/local-ai-Darwin-arm64) |


{{% alert icon="⚡" context="warning" %}}
Binaries do have limited support compared to container images:

- Python-based backends are not shipped with binaries (e.g. `bark`, `diffusers` or `transformers`)
- MacOS binaries and Linux-arm64 do not ship TTS nor `stablediffusion-cpp` backends
- Linux binaries do not ship `stablediffusion-cpp` backend
{{% /alert %}}
