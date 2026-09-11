---
title: "Linux Installation"
description: "Install LocalAI on Linux using binaries"
weight: 9
url: '/installation/linux/'
---

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

### Run your first model

Starting the binary on its own gives you an empty server. To get a working chat right away, run LocalAI with a model name and it will download and serve it from the gallery:

```bash
./local-ai-* run qwen3-4b
```

Once it is ready, open the WebUI at `http://localhost:8080` or send a request to the API:

```bash
curl http://localhost:8080/v1/chat/completions -H "Content-Type: application/json" -d '{
  "model": "qwen3-4b",
  "messages": [{"role": "user", "content": "Hello!"}]
}'
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

## Start LocalAI on demand with systemd

LocalAI accepts a single TCP listener passed through the systemd socket
activation protocol. This lets systemd listen on the public port and start
LocalAI only when the first client connects.

Create `/etc/systemd/system/local-ai.socket`:

```ini
[Unit]
Description=LocalAI API socket

[Socket]
ListenStream=8080
NoDelay=true

[Install]
WantedBy=sockets.target
```

Create the matching `/etc/systemd/system/local-ai.service`:

```ini
[Unit]
Description=LocalAI

[Service]
Type=simple
User=localai
Group=localai
ExecStart=/usr/local/bin/local-ai run
WorkingDirectory=/var/lib/local-ai
```

Adjust the user, binary path, working directory, and model configuration for
your installation. Then enable the socket, not the service:

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now local-ai.socket
```

The first connection to port 8080 starts `local-ai.service`; systemd holds that
connection until LocalAI is ready to accept it. `LOCALAI_ADDRESS` and
`--address` are ignored while an inherited listener is present. LocalAI
rejects activation with multiple stream listeners so it cannot silently choose
the wrong endpoint.

For a Podman-managed container, configure Podman to preserve and pass the
systemd socket file descriptor into the container. The LocalAI process inside
the container consumes the same activation protocol.

Activation needs both `LISTEN_PID` and `LISTEN_FDS`. If only one of them is set,
LocalAI ignores them and binds `--address` as usual. A container engine started
from a socket-activated system unit can leak a bare `LISTEN_PID` into every
container it spawns, and that is not an activation attempt.

## Next Steps

- [Try it out with examples](/basics/try/)
- [Learn about available models](/models/)
- [Configure GPU acceleration](/features/gpu-acceleration/)
- [Customize your configuration](/advanced/model-configuration/)
