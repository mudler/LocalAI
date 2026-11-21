# Run a LocalAI relay

A gated relay passes the traffic of paired LocalAI servers and devices that
cannot connect directly. This folder has what you need to run one. The guide is
in [docs/content/administration/p2p-relay.md](../docs/content/administration/p2p-relay.md).

| File | Use |
|---|---|
| `Dockerfile` | Builds the `localai/relay` image: one static binary, no shell, a user without privileges, about 10 MB. |
| `compose.yaml` | Runs the image with Docker Compose and host networking. |
| `localai-relay.service` | A hardened systemd unit for a host without Docker. |
| `cloud-init.yaml` | User data for a new cloud machine that installs Docker and starts the relay. |

## Quick start

```bash
docker build -f relay/Dockerfile -t localai/relay .
docker run -d --name localai-relay --restart unless-stopped --network host \
  -v localai-relay:/data localai/relay
docker logs localai-relay
```

The log shows the relay addresses, a one-time claim code (when you did not pass
`--admin`) and the command to run on your LocalAI server:

```bash
local-ai p2p relays add <relay address> --claim <code>
```

Open inbound port 4001 for TCP and UDP in the firewall and in the security group
of your provider.

## Two binaries, one command line

`cmd/localai-relay` is a small program that has only the relay. The image uses
it, because the `local-ai` binary is hundreds of megabytes and carries a web
interface and model code that a relay never uses. `local-ai p2p relay` runs the
same code with the same flags (both read them from
`core/services/p2prelay/relaycmd`), so a relay can also run from the binary you
already have.

## Publishing the image

This repository does not publish the image yet. A publishing job needs:

- A registry name and a login for it, stored as repository secrets.
- A build for `linux/amd64` and `linux/arm64` (`docker buildx build --platform
  linux/amd64,linux/arm64 -f relay/Dockerfile`). The build needs no cgo, so no
  cross compiler.
- Tags for the release version and `latest`.
- A trigger on a release, and a path filter for `relay/`, `cmd/localai-relay/`
  and `core/services/p2p*/`.
- A smoke test before the push: run the image for a few seconds and check for
  `PEERID` in its log.
