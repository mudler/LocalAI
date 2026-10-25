+++
disableToc = false
title = "Private Registries and Galleries"
weight = 24
url = '/advanced/private-sources'
+++

LocalAI can pull backends, models and gallery indexes from locations that need authentication: private OCI registries (GHCR, Quay, Harbor, Artifactory, ECR), internal HTTP servers, and private GitHub repositories.

You give LocalAI a credentials file. Each entry matches a URL prefix and says how to authenticate. Gallery files never contain secrets, so you can share a gallery and keep the credentials separate.

## Credentials file

Pass the file with `--credentials-file` or `LOCALAI_CREDENTIALS_FILE`. If neither is set, `local-ai run` reads `credentials.yaml` from the data path (`LOCALAI_DATA_PATH`) when that file exists.

```yaml
# Basic auth for a private registry. The password comes from an env var.
- match: ghcr.io/acme
  username: bot
  password_env: GHCR_TOKEN

# Bearer token for an internal model server, read from a mounted secret.
- match: https://models.acme.internal/
  bearer_file: /var/run/secrets/acme/token

# Private GitHub repository used by a gallery (github: URIs).
- match: github.com/acme
  bearer_env: GITHUB_TOKEN

# Artifactory-style API key header.
- match: artifactory.acme.internal
  header:
    name: X-JFrog-Art-Api
    value_env: ART_KEY

# Registry on a private network address. Image pulls from it are matched
# as http://, so the entry needs allow_insecure and no https:// scheme.
- match: 192.168.1.5:5000
  username: ci
  password_file: /run/secrets/registry-password
  allow_insecure: true
```

Each entry has:

| Key | Description |
|-----|-------------|
| `match` | URL prefix: a host, optionally followed by a path, optionally with `http://` or `https://`. Required. It must not contain credentials (`user:token@host`), a query string (`?`) or a fragment (`#`). |
| `username` + `password`, `password_env` or `password_file` | Basic authentication. |
| `bearer`, `bearer_env` or `bearer_file` | Bearer token. For registries it is sent as a registry token. GHCR, Docker Hub and Quay do not accept a bearer entry: use basic auth with any `username` and the token as the `password`. |
| `header.name` + `header.value`, `header.value_env` or `header.value_file` | A custom header. HTTP downloads only; registries ignore it. |
| `allow_insecure` | Allow sending this credential over plain `http://`. Default `false`. Registries on local or private addresses need it, see [Registries on the local network](#registries-on-the-local-network). |

Use exactly one authentication type per entry, and exactly one of the plain, `_env` or `_file` forms per secret.

LocalAI checks the file when it starts and stops with an error if the file is not valid. Unknown keys are errors, so a misspelled key such as `pasword_env` does not load silently. For a value of the wrong type, the error gives the line number but does not show the value.

LocalAI reads `_env` and `_file` values each time it needs them. A rotated Kubernetes secret mount is used without a restart. Trailing newlines in secret files are removed.

## How matching works

- Only `https://` URLs get credentials. `http://` URLs get credentials only from an entry with `allow_insecure: true` (see [Registries on the local network](#registries-on-the-local-network)). Other schemes never get credentials.
- The host must be equal. `ghcr.io` does not match `ghcr.io.evil.net`.
- The path matches whole segments. `ghcr.io/acme` matches `ghcr.io/acme/backend` but not `ghcr.io/acme-tools/backend`.
- A URL whose path has `.` or `..` segments never gets credentials.
- If more than one entry matches, the entry with the longest path wins. If two entries are equal, the first one in the file wins.
- If `match` has a scheme, the request must use that scheme. Without a scheme, the entry matches HTTPS, and also plain HTTP when `allow_insecure: true` is set.
- `github.com/<org>` also matches the `raw.githubusercontent.com/<org>/...` URLs that `github:` URIs download from.
- `docker.io` also matches `index.docker.io` and `registry-1.docker.io`.
- LocalAI checks every redirect separately. If a server redirects a download to a CDN that no entry matches, LocalAI sends no credentials to the CDN.

### Registries on the local network

The registry client treats some registry names as local and uses `http` as their scheme:

- a name that starts with `localhost:` (a port is given, for example `localhost:5000`)
- a name that ends in `.localhost`, with or without a port (for example `registry.localhost:5000`)
- a name that contains `127.0.0.1` or `::1`
- an IPv4 address in `10.0.0.0/8`, `172.16.0.0/12` or `192.168.0.0/16`, with or without a port

For these registries, LocalAI always matches image pulls as `http://<registry>/<repository>`, whatever scheme the connection uses in the end. An entry written as `https://192.168.1.5:5000` never applies to image pulls from that registry.

The entry for such a registry needs `allow_insecure: true`, and its `match` must have no scheme or use `http://`. No scheme is recommended, for example `match: 192.168.1.5:5000` with `allow_insecure: true`, because the same entry then also covers downloads that use HTTPS.

## What uses the credentials

- Gallery indexes and mirrors (`galleries`, `backend_galleries`), including `github:` URLs and galleries published as `oci://` artifacts.
- Model files and model configs downloaded over HTTP(S) or `github:`.
- Backend images and `oci://` / `ollama://` models, including resumed layer downloads and cosign signature checks.

For registries, LocalAI checks the credentials file first. If no entry matches, it uses your docker login (`~/.docker/config.json` or `DOCKER_CONFIG`). Hosts that already use `docker login` do not need a credentials file.

For `ollama://` models, only the blob downloads use credentials. LocalAI fetches the model manifest without credentials, so the manifest must be readable anonymously.

A credential passed directly by LocalAI (for example `HF_TOKEN` for managed Hugging Face artifacts) takes precedence over the file.

## Errors

When a server refuses a download with status 401 or 403, the error shows which case applies:

- `authentication required for <target> (status <code>): no credentials rule matches it`: add an entry that matches this URL. For registries the message continues with `and docker config credentials, if any, were not accepted`, because LocalAI also tried your docker login.
- `credential "<match>" was rejected by <target> (status <code>)`: an entry matched, but the server did not accept it. Check the secret and its permissions.
- `the provided credential was rejected by <target> (status <code>)`: the download carried a credential that LocalAI got from somewhere other than the file (for example `HF_TOKEN`), so the file was not used. Check that credential.

Each message ends with the reason the server gave, when there is one, for example a registry's `DENIED` detail. For HTTP downloads only the status text is shown, because the request URL can contain a signed query string.

If an `_env` variable is not set or a `_file` cannot be read, LocalAI logs a warning at startup and keeps the entry, because a secret mount can appear later. A download that matches the entry fails with an error that names the variable or file. LocalAI does not retry that download.

## Kubernetes

Mount the file and the secrets from a Secret:

```yaml
env:
  - name: LOCALAI_CREDENTIALS_FILE
    value: /etc/localai/credentials.yaml
volumeMounts:
  - name: localai-credentials
    mountPath: /etc/localai
    readOnly: true
```

## Distributed mode

The controller downloads models and sends them to the workers, so model downloads only need credentials on the controller.

Each worker pulls its own backend images. To install backends from a private registry, give every worker the same credentials file. LocalAI does not send credentials over NATS. If a worker has no matching entry and no docker login for that registry, the install fails, and the node's install error says that no credentials rule matches.
