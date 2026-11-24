+++
title = "Moving from the old P2P network"
toc = true
description = "What replaces the token network, federation, the P2P workers and the Explorer, and how to move an existing setup"
tags = ["P2P", "Administration", "Migration"]
categories = ["Features"]
weight = 147
aliases = ["/features/distribution/", "/features/distribute/", "/features/p2p/", "/administration/p2p-federated-inference/", "/administration/p2p-api/"]
+++

This release replaces the old P2P network of LocalAI. The old network used one
shared token: every machine that had the token was part of the network. The
new [P2P layer]({{% relref "/administration/p2p" %}}) admits each machine on its
own, with a one-time invite link, and you can revoke each machine alone.

The old network is removed in the same release. There is no period where both
work. Read this page before you upgrade a setup that uses one of these:

- `--p2p`, a P2P token or a network ID
- federated mode (`--federated` or `local-ai federated`)
- the P2P workers `local-ai worker p2p-llama-cpp-rpc` or `local-ai worker p2p-mlx`
- the Explorer (`local-ai explorer`)
- the HTTP routes `/api/p2p`, `/api/p2p/token`, `/api/p2p/workers`,
  `/api/p2p/federation` or `/api/p2p/stats`

## What changed

| Before | Now |
|--------|-----|
| One shared token for all machines | One invite link for each machine, used once. The link holds the peer ID of the server. |
| A machine with the token is in the network | A machine is admitted by its peer ID. You revoke each machine alone. |
| `--p2p` turns P2P on | The mode `off`, `connect` or `share` (`--p2p-mode`, setting `p2p_mode`). See [Turn on P2P]({{% relref "/administration/p2p" %}}#turn-on-p2p). |
| Federated mode balances requests across instances | A [federation group]({{% relref "/administration/federation" %}}): other instances are remotes of this server, and a group serves one model name from them. |
| P2P workers find the server with the token | [Model-split workers]({{% relref "/administration/p2p-model-split" %}}) join with an invite: `local-ai worker llama-cpp-rpc --join <invite link>` or `local-ai worker mlx-distributed --join <invite link>`. |
| The Explorer lists public token networks | Nothing. There are no token networks to list. |
| Connections go through the public network of the token | Connections go direct, by hole punching, or through a relay that you run. Public relays only help to set up hole punching; they never carry data. See [Run a relay]({{% relref "/administration/p2p-relay" %}}). |

## LocalAI stops when an old option is set

A server that still sets an old option does not start. It exits with status 1
and prints one line for each old option, with what replaces it. The check reads
the command line, the environment (also `.env` files) and the stored runtime
settings. Remove the old options and start again.

| Removed | Replacement |
|---------|-------------|
| `--p2p`, `$LOCALAI_P2P`, `$P2P` | `--p2p-mode connect` or `--p2p-mode share` (`$LOCALAI_P2P_MODE`) |
| `--p2p-token`, `--p2ptoken`, `$LOCALAI_P2P_TOKEN`, `$P2P_TOKEN`, `$LOCALAI_TOKEN`, `$TOKEN`, setting `p2p_token` | An invite created under **Cluster**, **Sharing** |
| `--p2p-network-id`, `--peer-2-peer-network-id`, `$LOCALAI_P2P_NETWORK_ID`, `$P2P_NETWORK_ID`, setting `p2p_network_id` | An invite created under **Cluster**, **Sharing** |
| `--p2p-dht-interval`, `--p2p-otp-interval` and their variables | None. They applied only to the token. |
| `--p2p-enable-limits`, `$LOCALAI_P2P_ENABLE_LIMITS` | None. Limits are set on the relay (`local-ai p2p relay`). |
| `$LOCALAI_P2P_DISABLE_DHT` | The runtime setting `p2p_public_dht` |
| `$LOCALAI_P2P_DHT_ANNOUNCE_MADDRS` | None. The server announces the addresses it listens on (`--p2p-listen`). |
| `$LOCALAI_P2P_LOGLEVEL`, `$LOCALAI_P2P_LIB_LOGLEVEL` | None. The P2P layer logs at the level of the server (`--log-level`). |
| `--federated`, `$LOCALAI_FEDERATED`, `$FEDERATED`, setting `federated` | A [federation group]({{% relref "/administration/federation" %}}) |
| `local-ai federated` (`--random-worker`, `--target-worker`) | A [federation group]({{% relref "/administration/federation" %}}) on the server that takes the requests |
| `local-ai worker p2p-llama-cpp-rpc` (also `p2p-worker p2p-llama-cpp-rpc`) | `local-ai worker llama-cpp-rpc --join <invite link>`, with a model-split worker invite |
| `local-ai worker p2p-mlx` (also `p2p-worker p2p-mlx`) | `local-ai worker mlx-distributed --join <invite link>`, with a model-split worker invite |
| `local-ai explorer` | None |

`$LOCALAI_P2P_LISTEN_MADDRS` and `$LOCALAI_P2P_BOOTSTRAP_PEERS_MADDRS` keep
their names. They set `--p2p-listen` and `--p2p-bootstrap` of the new layer.

`p2p-worker` still works as the old name of the `worker` command group, so
`local-ai p2p-worker llama-cpp-rpc` and `local-ai p2p-worker vllm` run as
before.

## Move a token network

Before, every machine started with the same token. Now the server makes one
invite for each machine.

1. On the server, remove `--p2p`, the token and the network ID. Set the mode:
   `--p2p-mode share` when other machines connect to this server (this needs
   [authentication]({{% relref "/administration/authentication" %}})), or
   `--p2p-mode connect` when this server only uses other servers.
2. Start the server once. It makes its identity, a key file under the data
   path. Keep this file: every machine that pairs with the server knows it by
   this key.
3. For each machine, make an invite under **Cluster**, **Sharing**, **Share this
   instance**, and use the link on that machine. The kind of invite depends on
   what the machine does: a device, a remote, a worker or a model-split
   worker. See [Pair a device or a remote]({{% relref "/administration/p2p" %}}#pair-a-device-or-a-remote).
4. Compare the confirmation phrase that both sides show.

## Move federated mode

Before, each instance started with `--p2p --federated`, and `local-ai federated`
balanced the requests across them. Now the server that takes the requests
uses the other instances as remotes, and a federation group serves one model
name from them.

1. On each other instance, set `--p2p-mode share` and make an invite of kind
   **Remote** under **Cluster**, **Sharing**, **Share this instance**.
2. On the server that takes the requests, set `--p2p-mode connect` (or
   `share`) and add each instance under **Models**, **Remotes** with its
   invite link.
3. Open **Cluster**, **Sharing**, **Federation**, select **New group**, enter the
   model name, pick the routing (least busy or round robin) and the members.

Clients call the model name of the group as before. A member that fails is
skipped and the next one takes the request. See
[Federation]({{% relref "/administration/federation" %}}).

## Move the P2P workers

Before, a worker started with the token and the server found it on the
network. Now each worker pairs with the server with its own invite.

1. On the server, set `--p2p-mode share`.
2. Open **Workers**, press **Add worker**, choose **Model-split worker**, pick the
   engine (llama.cpp or MLX) and make an invite.
3. On the worker, run the command that the page shows, for example:

   ```bash
   local-ai worker llama-cpp-rpc --join "<invite link>"
   ```

The worker keeps its identity, so a restart with the same command needs no new
invite. See [Model-split workers over P2P]({{% relref "/administration/p2p-model-split" %}}).

Workers that you start by hand still work as before: `local-ai worker
llama-cpp-rpc` without `--join` starts a plain `rpc-server`, and
`local-ai worker mlx-distributed` takes `--hostfile` and `--rank`.

## Removed HTTP routes

These routes are removed. They answer `404`.

| Removed route | Replacement |
|---------------|-------------|
| `GET /api/p2p` | `GET /api/p2p/status`, `GET /api/p2p/devices` |
| `GET /api/p2p/token` | `POST /api/p2p/invites` |
| `GET /api/p2p/workers` | `GET /api/p2p/split-workers` |
| `GET /api/p2p/federation` | `GET /api/federation` |
| `GET /api/p2p/stats` | `GET /api/p2p/status` |

The new routes are described in [P2P]({{% relref "/administration/p2p" %}}#http-api),
[Model-split workers over P2P]({{% relref "/administration/p2p-model-split" %}}#http-api-and-assistant)
and [Federation]({{% relref "/administration/federation" %}}#http-api).
