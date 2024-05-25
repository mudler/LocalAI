+++
disableToc = false
title = "🆕🖧 Distributed inferencing"
weight = 15
url = "/features/distribute/"
+++

{{% alert note %}}
This feature is available only with llama-cpp compatible models.

This feature has landed with https://github.com/mudler/LocalAI/pull/2324 and is based on the upstream work in https://github.com/ggerganov/llama.cpp/pull/6829.
{{% /alert %}}

This feature allows LocalAI to manage the requests while the workload is distributed among workers.

## Usage

### Start workers

To start workers to offload the computation you can run:

```
local-ai llamacpp-worker <listening_address> <listening_port>
```

However, you can also follow the llama.cpp README and building the rpc-server (https://github.com/ggerganov/llama.cpp/blob/master/examples/rpc/README.md), which is still compatible with LocalAI.

### Start LocalAI

When starting the LocalAI server, which is going to accept the API requests, you can set a list of workers IP/address by specifying the addresses with the `LLAMACPP_GRPC_SERVERS` environment variable, for example:

```bash
LLAMACPP_GRPC_SERVERS="address1:port,address2:port" local-ai run
```

At this point the workload hitting in the LocalAI server should be distributed across the nodes!

## Peer to peer

![output](https://github.com/mudler/LocalAI/assets/2420543/8ca277cf-c208-4562-8929-808b2324b584)

The workers can also be connected to each other, creating a peer to peer network, where the workload is distributed among the workers, in a private, decentralized network.

A shared token between the server and the workers is needed to let the communication happen via the p2p network. This feature supports both local network (with mdns discovery) and dht for communicating also behind different networks.

The token is generated automatically when starting the server with the `--p2p` flag, and can be used by starting the workers with `local-ai worker p2p-llama-cpp-rpc` by passing the token via environment variable (TOKEN) or with args (--token).

A network is established between the server and the workers with dht and mdns discovery protocols, the llama.cpp rpc server is automatically started and exposed to the underlying p2p network so the API server can connect on.

When the HTTP server is started, it will discover the workers in the network and automatically create the port-forwards to the service locally. Then llama.cpp is configured to use the services. If you are interested in how it works behind the scenes, see the PR: https://github.com/mudler/LocalAI/pull/2343.


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

A token is displayed, copy it and press enter.

You can re-use the same token later restarting the server with `--p2ptoken` (or `P2P_TOKEN`).

2. Start the workers. Now you can copy the local-ai binary in other hosts, and run as many workers with that token:

```bash
TOKEN=XXX ./local-ai  p2p-llama-cpp-rpc
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

(Note you can also supply the token via args)

At this point, you should see in the server logs messages stating that new workers are found

3. Now you can start doing inference as usual on the server (the node used on step 1)


##  Notes

- Only single model is supported for now
- Make sure that the server sees new workers after usage starts - currently, if you start the inference you can't add other workers later on.