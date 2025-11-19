---
title: "Linux Installation"
description: "Install LocalAI on Linux using the installer script or binaries"
weight: 3
url: '/installation/linux/'
---


## One-Line Installer (Recommended)

The fastest way to install LocalAI on Linux is with the installation script:

```bash
curl https://localai.io/install.sh | sh
```

This script will:
- Detect your system architecture
- Download the appropriate LocalAI binary
- Set up the necessary configuration
- Start LocalAI automatically

## Manual Installation

### Download Binary

You can manually download the appropriate binary for your system from the [releases page](https://github.com/mudler/LocalAI/releases):

1. Go to  [GitHub Releases](https://github.com/mudler/LocalAI/releases)
2. Download the binary for your architecture (amd64, arm64, etc.)
3. Make it executable:

```bash
chmod +x local-ai-*
```

4. Run LocalAI:

```bash
./local-ai-*
```

### System Requirements

Hardware requirements vary based on:
- Model size
- Quantization method
- Backend used

For performance benchmarks with different backends like `llama.cpp`, visit [this link](https://github.com/ggerganov/llama.cpp#memorydisk-requirements).

## Configuration

After installation, you can:

- Access the WebUI at `http://localhost:8080`
- Configure models in the models directory
- Customize settings via environment variables or config files

## Next Steps

- [Try it out with examples](/basics/try/)
- [Learn about available models](/models/)
- [Configure GPU acceleration](/features/gpu-acceleration/)
- [Customize your configuration](/advanced/model-configuration/)
