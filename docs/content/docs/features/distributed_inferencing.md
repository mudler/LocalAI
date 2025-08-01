+++
disableToc = false
title = "🆕🖧 Distributed Inference"
weight = 15
url = "/features/distribute/"
+++


This functionality enables LocalAI to distribute inference requests across multiple worker nodes, improving efficiency and performance. Nodes are automatically discovered and connect via p2p by using a shared token which makes sure the communication is secure and private between the nodes of the network.

LocalAI supports two modes of distributed inferencing via p2p:

- **Federated Mode**: Requests are shared between the cluster and routed to a single worker node in the network based on the load balancer's decision.
- **Worker Mode** (aka "model sharding" or "splitting weights"): Requests are processed by all the workers which contributes to the final inference result (by sharing the model weights).

A list of global instances shared by the community is available at [explorer.localai.io](https://explorer.localai.io).

## Usage

Starting LocalAI with `--p2p` generates a shared token for connecting multiple instances: and that's all you need to create AI clusters, eliminating the need for intricate network setups. 

Simply navigate to the "Swarm" section in the WebUI and follow the on-screen instructions.

For fully shared instances, initiate LocalAI with --p2p --federated and adhere to the Swarm section's guidance. This feature, while still experimental, offers a tech preview quality experience.

### Federated mode

Federated mode allows to launch multiple LocalAI instances and connect them together in a federated network. This mode is useful when you want to distribute the load of the inference across multiple nodes, but you want to have a single point of entry for the API. In the Swarm section of the WebUI, you can see the instructions to connect multiple instances together.

![346663124-1d2324fd-8b55-4fa2-9856-721a467969c2](https://github.com/user-attachments/assets/19ebd44a-20ff-412c-b92f-cfb8efbe4b21)

To start a LocalAI server in federated mode, run:

```bash
local-ai run --p2p --federated
```

This will generate a token that you can use to connect other LocalAI instances to the network or others can use to join the network. If you already have a token, you can specify it using the `TOKEN` environment variable.

To start a load balanced server that routes the requests to the network, run with the `TOKEN`:

```bash
local-ai federated
```

To see all the available options, run `local-ai federated --help`.

The instructions are displayed in the "Swarm" section of the WebUI, guiding you through the process of connecting multiple instances.

### Workers mode

{{% alert note %}}
This feature is available exclusively with llama-cpp compatible models.

This feature was introduced in [LocalAI pull request #2324](https://github.com/mudler/LocalAI/pull/2324) and is based on the upstream work in [llama.cpp pull request #6829](https://github.com/ggerganov/llama.cpp/pull/6829).
{{% /alert %}}

To connect multiple workers to a single LocalAI instance, start first a server in p2p mode:

```bash
local-ai run --p2p
```

And navigate the WebUI to the "Swarm" section to see the instructions to connect multiple workers to the network.

![346663124-1d2324fd-8b55-4fa2-9856-721a467969c2](https://github.com/user-attachments/assets/b8cadddf-a467-49cf-a1ed-8850de95366d)

### Without P2P

To start workers for distributing the computational load, run:

```bash
local-ai worker llama-cpp-rpc --llama-cpp-args="-H <listening_address> -p <listening_port> -m <memory>" 
```

And you can specify the address of the workers when starting LocalAI with the `LLAMACPP_GRPC_SERVERS` environment variable:

```bash
LLAMACPP_GRPC_SERVERS="address1:port,address2:port" local-ai run
```
The workload on the LocalAI server will then be distributed across the specified nodes.

Alternatively, you can build the RPC workers/server following the llama.cpp [README](https://github.com/ggerganov/llama.cpp/blob/master/examples/rpc/README.md), which is compatible with LocalAI.

## Manual example (worker)

Use the WebUI to guide you in the process of starting new workers. This example shows the manual steps to highlight the process.

1. Start the server with `--p2p`:

```bash
./local-ai run --p2p
# Get the token in the Swarm section of the WebUI
```

Copy the token from the WebUI or via API call (e.g., `curl http://localhost:8000/p2p/token`) and save it for later use.

To reuse the same token later, restart the server with `--p2ptoken` or `P2P_TOKEN`.

2. Start the workers. Copy the `local-ai` binary to other hosts and run as many workers as needed using the token:

```bash
TOKEN=XXX ./local-ai worker p2p-llama-cpp-rpc --llama-cpp-args="-m <memory>" 
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

![output](https://github.com/mudler/LocalAI/assets/2420543/8ca277cf-c208-4562-8929-808b2324b584)


## Environment Variables

There are options that can be tweaked or parameters that can be set using environment variables

{{< table "table-responsive" >}}
| Environment Variable | Description |
|----------------------|-------------|
| **LOCALAI_P2P** | Set to "true" to enable p2p |
| **LOCALAI_FEDERATED** | Set to "true" to enable federated mode |
| **FEDERATED_SERVER** | Set to "true" to enable federated server |
| **LOCALAI_P2P_DISABLE_DHT** | Set to "true" to disable DHT and enable p2p layer to be local only (mDNS) |
| **LOCALAI_P2P_ENABLE_LIMITS** | Set to "true" to enable connection limits and resources management (useful when running with poor connectivity or want to limit resources consumption) |
| **LOCALAI_P2P_LISTEN_MADDRS** | Set to comma separated list of multiaddresses to override default libp2p 0.0.0.0 multiaddresses |
| **LOCALAI_P2P_DHT_ANNOUNCE_MADDRS** | Set to comma separated list of multiaddresses to override announcing of listen multiaddresses (useful when external address:port is remapped) |
| **LOCALAI_P2P_BOOTSTRAP_PEERS_MADDRS** | Set to comma separated list of multiaddresses to specify custom DHT bootstrap nodes |
| **LOCALAI_P2P_TOKEN** | Set the token for the p2p network |
| **LOCALAI_P2P_LOGLEVEL** | Set the loglevel for the LocalAI p2p stack (default: info) |
| **LOCALAI_P2P_LIB_LOGLEVEL** | Set the loglevel for the underlying libp2p stack (default: fatal) |
{{< /table >}}


## Architecture

LocalAI uses https://github.com/libp2p/go-libp2p under the hood, the same project powering IPFS. Differently from other frameworks, LocalAI uses peer2peer without a single master server, but rather it uses sub/gossip and ledger functionalities to achieve consensus across different peers. 

[EdgeVPN](https://github.com/mudler/edgevpn) is used as a library to establish the network and expose the ledger functionality under a shared token to ease out automatic discovery and have separated, private peer2peer networks.

The weights are split proportional to the memory when running into worker mode, when in federation mode each request is split to every node which have to load the model fully.

## Debugging

To debug, it's often useful to run in debug mode, for instance:

```
LOCALAI_P2P_LOGLEVEL=debug LOCALAI_P2P_LIB_LOGLEVEL=debug LOCALAI_P2P_ENABLE_LIMITS=true LOCALAI_P2P_DISABLE_DHT=true LOCALAI_P2P_TOKEN="<TOKEN>" ./local-ai ...
```

## Notes

- If running in p2p mode with container images, make sure you start the container with `--net host` or `network_mode: host` in the docker-compose file.
- Only a single model is supported currently.
- Ensure the server detects new workers before starting inference. Currently, additional workers cannot be added once inference has begun.
- For more details on the implementation, refer to [LocalAI pull request #2343](https://github.com/mudler/LocalAI/pull/2343)


