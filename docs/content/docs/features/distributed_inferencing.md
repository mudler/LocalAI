+++
disableToc = false
title = "🆕🖧 Distributed Inference"
weight = 15
url = "/features/distribute/"
+++

{{% alert note %}}
This feature is available exclusively with llama-cpp compatible models.

This feature was introduced in [LocalAI pull request #2324](https://github.com/mudler/LocalAI/pull/2324) and is based on the upstream work in [llama.cpp pull request #6829](https://github.com/ggerganov/llama.cpp/pull/6829).
{{% /alert %}}

This functionality enables LocalAI to distribute inference requests across multiple worker nodes, improving efficiency and performance.

## Usage

### Starting Workers

To start workers for distributing the computational load, run:

```bash
local-ai worker llama-cpp-rpc <listening_address> <listening_port>
```

Alternatively, you can build the RPC server following the llama.cpp [README](https://github.com/ggerganov/llama.cpp/blob/master/examples/rpc/README.md), which is compatible with LocalAI.

### Starting LocalAI

To start the LocalAI server, which handles API requests, specify the worker addresses using the `LLAMACPP_GRPC_SERVERS` environment variable:

```bash
LLAMACPP_GRPC_SERVERS="address1:port,address2:port" local-ai run
```

The workload on the LocalAI server will then be distributed across the specified nodes.

## Peer-to-Peer Networking

![output](https://github.com/mudler/LocalAI/assets/2420543/8ca277cf-c208-4562-8929-808b2324b584)

Workers can also connect to each other in a peer-to-peer network, distributing the workload in a decentralized manner.

A shared token between the server and the workers is required for communication within the peer-to-peer network. This feature supports both local network (using mDNS discovery) and DHT for communication across different networks.

The token is automatically generated when starting the server with the `--p2p` flag. Workers can be started with the token using `local-ai worker p2p-llama-cpp-rpc` and specifying the token via the environment variable `TOKEN` or with the `--token` argument.

A network is established between the server and workers using DHT and mDNS discovery protocols. The llama.cpp RPC server is automatically started and exposed to the peer-to-peer network, allowing the API server to connect.

When the HTTP server starts, it discovers workers in the network and creates port forwards to the local service. Llama.cpp is configured to use these services. For more details on the implementation, refer to [LocalAI pull request #2343](https://github.com/mudler/LocalAI/pull/2343).

### Usage

1. Start the server with `--p2p`:

```bash
./local-ai run --p2p
# 1:02AM INF loading environment variables from file envFile=.env
# 1:02AM INF Setting logging to info
# 1:02AM INF P2P mode enabled
# 1:02AM INF No token provided, generating one
# 1:02AM INF Generated Token:
# XXXXXXXXXXX
# 1:02AM INF Press a button to proceed
```

Copy the displayed token and press Enter.

To reuse the same token later, restart the server with `--p2ptoken` or `P2P_TOKEN`.

2. Start the workers. Copy the `local-ai` binary to other hosts and run as many workers as needed using the token:

```bash
TOKEN=XXX ./local-ai worker p2p-llama-cpp-rpc
# 1:06AM INF loading environment variables from file envFile=.env
# 1:06AM INF Setting logging to info
# {"level":"INFO","time":"2024-05-19T01:06:01.794+0200","caller":"config/config.go:288","message":"connmanager disabled\n"}
# {"level":"INFO","time":"2024-05-19T01:06:01.794+0200","caller":"config/config.go:295","message":" go-libp2p resource manager protection enabled"}
# {"level":"INFO","time":"2024-05-19T01:06:01.794+0200","caller":"config/config.go:409","message":"max connections: 100\n"}
# 1:06AM INF Starting llama-cpp-rpc-server on '127.0.0.1:34371'
# {"level":"INFO","time":"2024-05-19T01:06:01.794+0200","caller":"node/node.go:118","message":" Starting EdgeVPN network"}
# create_backend: using CPU backend
# Starting RPC server on 127.0.0.1:34371, backend memory: 31913 MB
# 2024/05/19 01:06:01 failed to sufficiently increase receive buffer size (was: 208 kiB, wanted: 2048 kiB, got: 416 kiB). # See https://github.com/quic-go/quic-go/wiki/UDP-Buffer-Sizes for details.
# {"level":"INFO","time":"2024-05-19T01:06:01.805+0200","caller":"node/node.go:172","message":" Node ID: 12D3KooWJ7WQAbCWKfJgjw2oMMGGss9diw3Sov5hVWi8t4DMgx92"}
# {"level":"INFO","time":"2024-05-19T01:06:01.806+0200","caller":"node/node.go:173","message":" Node Addresses: [/ip4/127.0.0.1/tcp/44931 /ip4/127.0.0.1/udp/33251/quic-v1/webtransport/certhash/uEiAWAhZ-W9yx2ZHnKQm3BE_ft5jjoc468z5-Rgr9XdfjeQ/certhash/uEiB8Uwn0M2TQBELaV2m4lqypIAY2S-2ZMf7lt_N5LS6ojw /ip4/127.0.0.1/udp/35660/quic-v1 /ip4/192.168.68.110/tcp/44931 /ip4/192.168.68.110/udp/33251/quic-v1/webtransport/certhash/uEiAWAhZ-W9yx2ZHnKQm3BE_ft5jjoc468z5-Rgr9XdfjeQ/certhash/uEiB8Uwn0M2TQBELaV2m4lqypIAY2S-2ZMf7lt_N5LS6ojw /ip4/192.168.68.110/udp/35660/quic-v1 /ip6/::1/tcp/41289 /ip6/::1/udp/33160/quic-v1/webtransport/certhash/uEiAWAhZ-W9yx2ZHnKQm3BE_ft5jjoc468z5-Rgr9XdfjeQ/certhash/uEiB8Uwn0M2TQBELaV2m4lqypIAY2S-2ZMf7lt_N5LS6ojw /ip6/::1/udp/35701/quic-v1]"}
# {"level":"INFO","time":"2024-05-19T01:06:01.806+0200","caller":"discovery/dht.go:104","message":" Bootstrapping DHT"}
```

(Note: You can also supply the token via command-line arguments)

The server logs should indicate that new workers are being discovered.

3. Start inference as usual on the server initiated in step 1.

## Notes

- If running in p2p mode with container images, make sure you start the container with `--net host` or `network_mode: host` in the docker-compose file.
- Only a single model is supported currently.
- Ensure the server detects new workers before starting inference. Currently, additional workers cannot be added once inference has begun.
