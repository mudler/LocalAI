+++
title = "Administration"
description = "Run, monitor and govern a LocalAI instance."
weight = 7
type = "chapter"
icon = "manage_accounts"
aliases = ["/operations/"]
+++

These pages are for the person who runs a LocalAI instance: users and access, settings, monitoring, proxies, distributed mode and troubleshooting.

## Access and settings

- [Authentication and authorization]({{% relref "/administration/authentication" %}}): API keys, users, roles and per-user features.
- [Runtime settings]({{% relref "/administration/runtime-settings" %}}): change settings in the web interface without a restart.
- [Reverse proxy and TLS]({{% relref "/administration/reverse-proxy-tls" %}}): put LocalAI behind a TLS proxy.

## Watch what the instance does

- [Operate overview]({{% relref "/administration/overview" %}}): where to look first.
- [Traffic]({{% relref "/administration/traffic" %}}): usage, failed requests, models, GPU and host.
- [Activity]({{% relref "/administration/activity" %}}): watch, cancel and retry installs.
- [Backend monitor]({{% relref "/administration/backend-monitor" %}}): list, pre-load and stop running backends.
- [Tracing]({{% relref "/administration/tracing" %}}) and [Profiling and request diagnostics]({{% relref "/administration/diagnostics" %}}).

## Proxies and middleware

- [Middleware: PII filtering and routing]({{% relref "/administration/middleware" %}})
- [Cloud passthrough proxy]({{% relref "/administration/cloud-proxy" %}})
- [MITM proxy for Claude Code and Codex CLI]({{% relref "/administration/mitm-proxy" %}})

## More than one machine

- [Distributed mode]({{% relref "/administration/distributed-mode" %}})
- [P2P and federated inference]({{% relref "/administration/p2p-federated-inference" %}}) and its [API]({{% relref "/administration/p2p-api" %}})
- [MLX distributed]({{% relref "/administration/mlx-distributed" %}})

## When something fails

- [Troubleshooting]({{% relref "/administration/troubleshooting" %}})
- [Routing logs in embedded applications]({{% relref "/administration/embedded-routing-log" %}})
