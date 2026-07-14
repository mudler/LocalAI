+++
disableToc = false
title = "Distribution"
weight = 13
url = "/features/distribution/"
+++

LocalAI supports distributing inference workloads across multiple machines. There are two approaches, each suited to different use cases:

## Distributed Mode (PostgreSQL + NATS)

Production-grade horizontal scaling with centralized management. Frontends are stateless LocalAI instances behind a load balancer; workers self-register and receive backends dynamically via NATS. State lives in PostgreSQL.

**Best for:** production deployments, Kubernetes, managed infrastructure.

[Read more]({{% relref "features/distributed-mode" %}})

## P2P / Federated Inference

Peer-to-peer networking via libp2p. Share a token to form a cluster with automatic discovery — no central server required. Supports federated load balancing and worker-mode weight sharding.

**Best for:** ad-hoc clusters, community sharing, quick experimentation.

[Read more]({{% relref "features/distributed_inferencing" %}})

## Quick Comparison

| | P2P / Federation | Distributed Mode |
|---|---|---|
| **Discovery** | Automatic via libp2p token | Self-registration to frontend URL |
| **State storage** | In-memory / ledger | PostgreSQL |
| **Coordination** | Gossip protocol | NATS messaging |
| **Node management** | Automatic | REST API + WebUI |
| **Setup complexity** | Minimal (share a token) | Requires PostgreSQL + NATS |
