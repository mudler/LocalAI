+++
title = "MITM proxy for Claude Code / Codex CLI"
weight = 29
toc = true
url = "/features/mitm-proxy/"
description = "Redact PII from cloud-AI traffic without LocalAI holding API keys"
tags = ["Proxy", "MITM", "Privacy", "Routing", "Advanced"]
categories = ["Features"]
+++

![MITM proxy: allowlisted hosts are decrypted and scanned, everything else is a blind TCP tunnel](/images/diagrams/mitm-intercept.png)

LocalAI can act as a local HTTPS proxy that **redacts PII from your Claude
Code, OpenAI Codex CLI, or any HTTPS client** without holding their API keys.
The proxy intercepts only the LLM API endpoints you allowlist (default:
`api.anthropic.com`, `api.openai.com`); everything else - OAuth, telemetry,
package fetches - passes through as a plain TCP tunnel.

Use this when:

- You want to use **Claude Code with a Claude Pro/Max subscription** but still
  apply the same PII redaction LocalAI applies to API-key traffic.
- You run Codex CLI on a corporate laptop and need an audit trail of prompts.
- You want LocalAI to enforce egress policies for AI traffic without
  becoming the API endpoint clients talk to.

The proxy is **off by default**. Operators opt in by setting `--mitm-listen`
and distributing the generated CA cert.

## How it works

1. The proxy generates a private CA on first start (persisted to disk).
2. Clients set `HTTPS_PROXY=http://localai:port` and add the CA to their
   trust store (e.g. `NODE_EXTRA_CA_CERTS` for Node-based CLIs like Claude
   Code and Codex).
3. The CLI sends `CONNECT api.anthropic.com:443` to the proxy.
4. For allowlisted hosts, the proxy mints a per-host leaf cert signed by
   the CA, terminates TLS, parses the HTTP request, applies the global
   PII redactor on `/v1/messages` or `/v1/chat/completions`, and forwards
   to the real upstream over its own TLS connection.
5. The streaming SSE response runs through the same `pii.StreamFilter`
   the cloud-proxy backend uses.
6. For non-allowlisted hosts, the proxy is a plain CONNECT tunnel - no
   TLS termination, no inspection, no CA trust required.

The CLI authenticates with its own subscription / API key as it normally
would. LocalAI never holds the credential - it just observes and rewrites
the request body.

## Quick start

Start LocalAI with the MITM listener:

```bash
local-ai run --mitm-listen :8443
```

The first start generates a CA at `<data-path>/mitm-ca/{ca.crt,ca.key}`.
Restarting reloads the same CA so clients keep trusting it.

Download the public CA cert:

```bash
curl -O http://localhost:8080/api/middleware/proxy-ca.crt
```

Configure Claude Code to use the proxy and trust the cert:

```bash
export HTTPS_PROXY=http://localhost:8443
export NODE_EXTRA_CA_CERTS=$(pwd)/proxy-ca.crt
claude
```

Now any `claude` chat session that touches `api.anthropic.com/v1/messages`
gets its prompts and tool inputs scanned by LocalAI's PII filter, and any
PII the model emits in its streaming response is masked before reaching
your terminal. Events appear in the LocalAI middleware admin page under
**Filtering → Recent events**.

The same works for Codex CLI - set `HTTPS_PROXY` and `NODE_EXTRA_CA_CERTS`
and run `codex`.

## Configuration

The proxy is enabled with two startup settings:

| Flag / env | Default | Purpose |
|---|---|---|
| `--mitm-listen` / `LOCALAI_MITM_LISTEN` | empty (disabled) | Address to bind the proxy listener on |
| `--mitm-ca-dir` / `LOCALAI_MITM_CA_DIR` | `<data-path>/mitm-ca` | Where to persist the CA cert + key |

There is no global intercept-hosts flag. The hosts whose TLS is terminated
and scanned are declared **per model**, in the model YAML `mitm.hosts:`
block. Each model that names one or more hosts owns those hosts; everything
not listed by any model tunnels through untouched. A cloud-proxy model that
should intercept Anthropic traffic looks like:

```yaml
name: claude
backend: cloud-proxy
mitm:
  hosts:
    - api.anthropic.com
```

Hostnames are case-insensitive. Add custom upstreams (e.g. an
OpenAI-compatible third-party provider) by adding their hostname to a
model's `mitm.hosts:` list and ensuring their endpoint paths match
`/v1/chat/completions` or `/v1/messages`. You can create these models from
the Add Model UI.

## What gets redacted

The MITM proxy runs the same PII detection as the regular request
middleware. Detection is **NER-based** (a token-classification detector
model), not a fixed regex list: the older pattern tier has been removed.
See {{% relref "operations/middleware" %}} for how detector models, entity
groups, and the `mask` / `block` actions are configured, and for the
instance-wide default detector.

A `block` action returns HTTP 400 with `error.type=pii_blocked` to the
client. The CLI sees the rejection and shows it as a request error.

Events are persisted via the same `pii.EventStore` the rest of LocalAI
uses, so the `/api/pii/events` endpoint and the middleware admin page
include MITM events alongside direct-API events.

## Security notes

- **The CA private key is the master credential.** Anyone with read
  access to `<data-path>/mitm-ca/ca.key` can forge TLS for any host the
  proxy could intercept. The file is mode 0600; keep it that way.
- The proxy listener accepts plaintext HTTP `CONNECT` requests - bind it
  to localhost (`--mitm-listen 127.0.0.1:8443`) unless you've added auth
  in front of the listener. There is no built-in API-key check on this
  port.
- The MITM CA is **separate** from any TLS cert LocalAI's main HTTP API
  uses. Installing the MITM CA grants trust only for traffic that flows
  through this proxy.
- The proxy does not pin upstream certificates; it trusts the system
  certificate store. If your machine's trust store is compromised, the
  proxy is too.
- TLS termination negotiates HTTP/2 by default (ALPN `h2`) and falls
  back to HTTP/1.1 for clients that don't speak h2. Modern CLIs (Claude
  Code, Codex) and the Anthropic / OpenAI APIs all use h2.

## Limitations

- **Only `/v1/messages` and `/v1/chat/completions` get redacted.** Other
  paths on the same host (OAuth, model listing) are forwarded verbatim.
- **No request-shape translation.** The proxy assumes the request body
  matches the host's wire format; cross-shape forwarding is the cloud
  proxy backend's job, not the MITM's.
- **No CA rotation in the MVP.** To rotate, delete `ca.key` and `ca.crt`
  and re-distribute the new cert to every client.
- **Cert pinning kills MITM.** Neither Claude Code nor Codex CLI pins
  certificates today, but a future SDK update could. If a CLI starts
  refusing the proxied handshake, that's the signal.

## Comparison with the cloud-proxy backend

LocalAI ships two cloud-related proxy modes; pick by who holds the credential:

|  | Cloud-proxy backend (`backend: proxy-*`) | MITM proxy (`--mitm-listen`) |
|---|---|---|
| Client config | `localai:8080` as **API endpoint** | `localai:8443` as **HTTPS_PROXY** |
| Holds API key | LocalAI | Client (CLI's own auth) |
| Works with subscription auth | No | Yes (CLI uses its own login) |
| Request rewriting | Yes (handler controls it) | Yes (selective per host+path) |
| CA cert distribution | Not needed | Required on every client |
| Routes through LocalAI's auth/usage tracking | Yes | Yes (per-correlation-id events) |

For shared deployments where LocalAI owns the API key and clients are
unsophisticated (curl, simple webapps), use the cloud-proxy backend. For
"give my Claude Code a privacy filter" use cases, use the MITM proxy.
