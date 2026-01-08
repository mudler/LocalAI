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

### Installer Configuration Options

The installer can be configured using environment variables:

```bash
curl https://localai.io/install.sh | VAR=value sh
```

#### Environment Variables

| Environment Variable | Description |
|----------------------|-------------|
| **DOCKER_INSTALL** | Set to `"true"` to enable the installation of Docker images |
| **USE_AIO** | Set to `"true"` to use the all-in-one LocalAI Docker image |
| **USE_VULKAN** | Set to `"true"` to use Vulkan GPU support |
| **API_KEY** | Specify an API key for accessing LocalAI, if required |
| **PORT** | Specifies the port on which LocalAI will run (default is 8080) |
| **THREADS** | Number of processor threads the application should use. Defaults to the number of logical cores minus one |
| **VERSION** | Specifies the version of LocalAI to install. Defaults to the latest available version |
| **MODELS_PATH** | Directory path where LocalAI models are stored (default is `/var/lib/local-ai/models`) |
| **P2P_TOKEN** | Token to use for the federation or for starting workers. See [distributed inferencing documentation]({{%relref "features/distributed_inferencing" %}}) |
| **WORKER** | Set to `"true"` to make the instance a worker (p2p token is required) |
| **FEDERATED** | Set to `"true"` to share the instance with the federation (p2p token is required) |
| **FEDERATED_SERVER** | Set to `"true"` to run the instance as a federation server which forwards requests to the federation (p2p token is required) |

#### Image Selection

The installer will automatically detect your GPU and select the appropriate image. By default, it uses the standard images without extra Python dependencies. You can customize the image selection:

- `USE_AIO=true`: Use all-in-one images that include all dependencies
- `USE_VULKAN=true`: Use Vulkan GPU support instead of vendor-specific GPU support

#### Uninstallation

To uninstall LocalAI installed via the script:

```bash
curl https://localai.io/install.sh | sh -s -- --uninstall
```

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
