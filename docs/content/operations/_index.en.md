---
weight: 6
title: "Operations"
description: "Operator-facing runtime, proxy, and monitoring concerns"
type: chapter
icon: manage_accounts
---

This section collects the operator-facing concerns for running LocalAI in production: request middleware, cloud and MITM proxies, and backend monitoring. These pages are about running and governing a LocalAI instance rather than about a specific inference feature.

## Pages

- [Middleware: PII filtering and intelligent routing]({{% relref "operations/middleware" %}}) - per-model PII redaction and policy-based request routing.
- [Cloud passthrough proxy]({{% relref "operations/cloud-proxy" %}}) - forward requests to OpenAI, Anthropic, or any compatible provider.
- [MITM proxy for Claude Code / Codex CLI]({{% relref "operations/mitm-proxy" %}}) - redact PII from cloud-AI traffic without LocalAI holding API keys.
- [Backend Monitor]({{% relref "operations/backend-monitor" %}}) - monitor, pre-load, and shut down running model backends.
