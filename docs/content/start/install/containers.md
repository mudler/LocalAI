---
title: Containers
description: Install and use LocalAI with container engines (Docker, Podman)
weight: 50
aliases:
  - "/basics/container/"
  - "/installation/containers/"
  - "/installation/docker/"
  - "/start/install/docker/"
---

LocalAI supports Docker, Podman, and other OCI-compatible container engines. This guide covers the common aspects of running LocalAI in containers.

## Prerequisites

Before you begin, ensure you have a container engine installed:

- [Install Docker](https://docs.docker.com/get-docker/) (Mac, Windows, Linux)
- [Install Podman](https://podman.io/getting-started/installation) (Linux, macOS, Windows WSL2)

Podman might not resolve short image names such as `localai/localai:latest`.
The Podman examples use the `docker.io/` prefix to identify the container
registry explicitly.

## Quick Start

Download the compose file and start it:

```bash
curl -fsSL https://localai.io/install/docker-compose.yaml -o docker-compose.yaml
docker compose up -d
```

Then open `http://localhost:8080` and install a model from the gallery.

The compose file runs the CPU image `localai/localai:latest`. It keeps models,
backends, settings and application data in four named volumes, so they stay
when you recreate the container or upgrade the image. It restarts LocalAI after
a crash or a reboot. To use a GPU, see [Using Compose](#using-compose).

To upgrade to a new release:

```bash
docker compose pull
docker compose up -d
```

`docker compose down` stops LocalAI and keeps the volumes. `docker compose down -v`
also deletes the volumes, with all models and data.

Without Compose, use `docker run` with the same four volumes:

```bash
docker run -d --name local-ai --restart unless-stopped --init -p 8080:8080 \
  -v localai-models:/models \
  -v localai-backends:/backends \
  -v localai-data:/data \
  -v localai-configuration:/configuration \
  localai/localai:latest
# Or with Podman:
podman run -d --name local-ai --restart unless-stopped --init -p 8080:8080 \
  -v localai-models:/models \
  -v localai-backends:/backends \
  -v localai-data:/data \
  -v localai-configuration:/configuration \
  docker.io/localai/localai:latest
```

Docker and Podman create the named volumes when the container starts for the
first time. To follow the logs, run `docker logs -f local-ai`.

## Image Types

LocalAI provides several image types to suit different needs. These images work with both Docker and Podman.

The commands in this section show only the image and the GPU flags. Add the
`-d`, `--restart`, `--init` and `-v` options from the [Quick Start](#quick-start)
to keep your data.

### Standard Images

Standard images don't include pre-configured models. Use these if you want to configure models manually.

#### CPU Image

```bash
docker run -ti --name local-ai -p 8080:8080 localai/localai:latest
# Or with Podman:
podman run -ti --name local-ai -p 8080:8080 docker.io/localai/localai:latest
```

#### GPU Images

Choose the image that matches your hardware and installed drivers:

- **NVIDIA CUDA 12** is the compatibility choice for systems with CUDA 12 drivers. Use **CUDA 13** when your NVIDIA driver and toolkit support CUDA 13.
- **AMD ROCm** is for supported AMD GPUs, while **Intel** is for Intel GPUs with the required device runtime.
- **Jetson** uses the L4T ARM64 image. Choose its CUDA 12 image for Jetson AGX Orin-class devices or CUDA 13 for DGX Spark.
- **Vulkan** works across vendors and is the fallback when no matching CUDA, ROCm, or Intel image is available.

**NVIDIA CUDA 13:**
```bash
docker run -ti --name local-ai -p 8080:8080 --gpus all localai/localai:latest-gpu-nvidia-cuda-13
# Or with Podman:
podman run -ti --name local-ai -p 8080:8080 --device nvidia.com/gpu=all docker.io/localai/localai:latest-gpu-nvidia-cuda-13
```

**NVIDIA CUDA 12:**
```bash
docker run -ti --name local-ai -p 8080:8080 --gpus all localai/localai:latest-gpu-nvidia-cuda-12
# Or with Podman:
podman run -ti --name local-ai -p 8080:8080 --device nvidia.com/gpu=all docker.io/localai/localai:latest-gpu-nvidia-cuda-12
```

**AMD GPU (ROCm):**
```bash
docker run -ti --name local-ai -p 8080:8080 --device=/dev/kfd --device=/dev/dri localai/localai:latest-gpu-hipblas
# Or with Podman:
podman run -ti --name local-ai -p 8080:8080 --device rocm.com/gpu=all docker.io/localai/localai:latest-gpu-hipblas
```

**Intel GPU:**
```bash
docker run -ti --name local-ai -p 8080:8080 --device=/dev/dri localai/localai:latest-gpu-intel
# Or with Podman:
podman run -ti --name local-ai -p 8080:8080 --device gpu.intel.com/all docker.io/localai/localai:latest-gpu-intel
```

**Vulkan:**
```bash
docker run -ti --name local-ai -p 8080:8080 --device=/dev/dri localai/localai:latest-gpu-vulkan
# Or with Podman:
podman run -ti --name local-ai -p 8080:8080 --device=/dev/dri docker.io/localai/localai:latest-gpu-vulkan
```

For an NVIDIA GPU with the Vulkan image, use `--gpus all` instead of `--device=/dev/dri`.

The container adds the groups that own `/dev/kfd` and `/dev/dri/*` to the user
that runs LocalAI, so `--group-add` is not necessary.

**NVIDIA Jetson (L4T ARM64):**

CUDA 12 (for Nvidia AGX Orin and similar platforms):
```bash
docker run -ti --name local-ai -p 8080:8080 --runtime nvidia --gpus all localai/localai:latest-nvidia-l4t-arm64
```

CUDA 13 (for Nvidia DGX Spark):
```bash
docker run -ti --name local-ai -p 8080:8080 --runtime nvidia --gpus all localai/localai:latest-nvidia-l4t-arm64-cuda-13
```

## Using Compose

The [compose file](https://localai.io/install/docker-compose.yaml) of the Quick
Start is the recommended setup. It is the same file as `docker-compose.yaml` in
the repository. It uses no `.env` file and installs no model.

To use a GPU, edit `docker-compose.yaml`:

1. Change `image` to the GPU tag, for example `localai/localai:latest-gpu-nvidia-cuda-12`.
   The comments in the file list the tags.
2. Remove the `#` from the block for your GPU (NVIDIA, AMD, or Intel and Vulkan).
3. Run `docker compose up -d`.

For NVIDIA, the block reserves all GPUs. It needs the
[NVIDIA Container Toolkit](https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/install-guide.html)
on the host:

```yaml
services:
  localai:
    image: localai/localai:latest-gpu-nvidia-cuda-12
    # ...
    deploy:
      resources:
        reservations:
          devices:
            - driver: nvidia
              count: all
              capabilities: [gpu]
```

For AMD, Intel and Vulkan, the block passes the device files:

```yaml
services:
  localai:
    image: localai/localai:latest-gpu-hipblas
    # ...
    devices:
      - /dev/kfd   # AMD only
      - /dev/dri
```

With Podman, use `podman compose up -d` or `podman-compose up -d`, and add the
`docker.io/` prefix to the image name.

To build LocalAI from a source checkout instead, use `docker-compose.dev.yaml`
in the repository: `docker compose -f docker-compose.dev.yaml up -d --build`.

## Persistent Storage

The container exposes the following volumes:

| Volume | Description | CLI Flag | Environment Variable |
|--------|-------------|----------|----------------------|
| `/models` | Model files used for inferencing | `--models-path` | `$LOCALAI_MODELS_PATH` |
| `/backends` | Custom backends for inferencing | `--backends-path` | `$LOCALAI_BACKENDS_PATH` |
| `/configuration` | Dynamic config files (api_keys.json, external_backends.json, runtime_settings.json) | `--localai-config-dir` | `$LOCALAI_CONFIG_DIR` |
| `/data` | Persistent data: collections, chat files, add-ons, JSON stores when there is no database, generated images, audio and video (`/data/generated`), uploads (`/data/upload`), the home folder (`/data/home`) and caches (`/data/cache`). Downloads are staged in `/data/downloading` until they are extracted | `--data-path` | `$LOCALAI_DATA_PATH` |

{{% callout style="warning" %}}
Container files that are not stored in a volume are lost when the container is
recreated during an image upgrade. Mount all four paths if you want to preserve
installed models, backends, settings, and application data.
{{% /callout %}}

The host paths can be anywhere on persistent storage, but the container paths
must be exactly `/models`, `/backends`, `/configuration`, and `/data`. In
UnRAID and other container-template UIs, create one path mapping for each row
in the table above.

Backend OCI images contain symbolic links. When `/backends` is stored on a
filesystem that cannot create links, such as some CIFS/SMB mounts, LocalAI
materializes each link as a regular file so installation can complete. This can
use more disk space than a local filesystem. Prefer a Docker or Podman named
volume for `/backends` when possible.

Named volumes, as in the [Quick Start](#quick-start), need no preparation. To
keep the files in folders of the host instead (bind mounts), create the folders
first as your own user. If Docker creates a missing folder, root owns it. Then
start the container:

```bash
mkdir -p models backends data configuration
docker run -d --name local-ai --restart unless-stopped --init -p 8080:8080 \
  -v "$PWD/models:/models" \
  -v "$PWD/backends:/backends" \
  -v "$PWD/data:/data" \
  -v "$PWD/configuration:/configuration" \
  localai/localai:latest
```

With Compose, use the bind-mount lines in the comments of `docker-compose.yaml`.
To know who owns the files, see [Container user and permissions](#container-user-and-permissions).

## Container user and permissions

LocalAI does not run as root in the container. The image starts as root only to
run `entrypoint.sh`. The entrypoint selects a user, makes the data folders
writable for that user, and then starts LocalAI as that user. An add-on that runs
commands in an isolated sandbox needs this, because it refuses to run as root.
See [the sandbox in the LocalAI container]({{% relref "/addons/catalogue/work/sandbox-settings#the-sandbox-in-the-localai-container" %}})
for the devices and permissions it needs.

The entrypoint selects the user in this order:

1. If `LOCALAI_UID` is set, LocalAI runs as `LOCALAI_UID` and `LOCALAI_GID`.
2. If `LOCALAI_UID` and `LOCALAI_GID` are not set, and `/data` and `/models`
   have the same owner that is not root, LocalAI runs as that owner.
3. In all other cases, LocalAI runs as the user `localai` (uid and gid 10001).

The entrypoint then gives each data folder (`/models`, `/backends`,
`/configuration`, `/data`) that another user owns to the selected user
(`chown -R`). It logs each change. On a large models folder, this can take
some time on the first start. In case 2, `/data` and `/models` already belong
to the selected user, so the entrypoint does not change them. When the image
has no user for the selected uid, the entrypoint adds one.

The entrypoint also adds the groups that own `/dev/kvm`, `/dev/kfd`, `/dev/dri/*`
and `/var/run/docker.sock` when you pass them to the container. It never adds
group 0 (root).

{{% callout style="warning" %}}
Access to the Docker socket is equal to root access on the host. Mount
`/var/run/docker.sock` only into a container that needs it, and only on a host
that you trust.
{{% /callout %}}

When root owns the socket with group `root`, as on Docker Desktop, the
entrypoint cannot give the user access to it. To use the socket there, set
`LOCALAI_RUN_AS_ROOT=true`.

The home folder of the user is `/data/home`. The backends keep their caches
there, for example the caches of the Python backends. LocalAI keeps the
Sigstore trust data, which verifies signed add-ons and backends, in
`/data/cache/sigstore`. These caches stay when you recreate the container.

At startup, LocalAI checks that it can write to its folders: models, backends,
data, configuration, generated content, uploads, download staging, the home
folder and the temporary folder. If it cannot write to one of them, it logs one
line and continues to start:

```
WARN LocalAI cannot write to some of its folders; installs, uploads and caches that use them will fail. Give the folders to the user LocalAI runs as, or run it as their owner. See https://localai.io/docs/start/install/containers/#container-user-and-permissions folders="..." uid=... gid=...
```

`folders` names each folder and its owner. `uid` and `gid` are the user that
LocalAI runs as. If you see this line, use the list below to correct the setup.

Select the setup that matches your installation:

- **Named volumes (recommended).** Nothing to do. The volumes belong to uid
  10001 inside Docker. Use `docker compose cp` or `docker cp` to copy files
  into them.
- **Bind mounts that you created.** Create the four folders as your own user
  before the first start, as shown in [Persistent Storage](#persistent-storage).
  Because `/data` and `/models` then have your uid, LocalAI runs as you, and
  the files on the host stay yours.
- **Bind mounts that root owns.** This includes folders that Docker created
  because they did not exist. The entrypoint gives them to uid 10001. On the
  host, you then need `sudo` to change the files. To use your own user instead,
  give the folders to that user (`sudo chown -R "$(id -u):$(id -g)" models backends data configuration`)
  and start the container again.
- **A fixed user (`LOCALAI_UID`, `LOCALAI_GID`).** Set them when the files must
  belong to a known user, for example the user of a NAS share. This setting
  always has priority. The entrypoint gives the data folders to that user.
- **`docker run --user`, Compose `user:`, Kubernetes `runAsUser`.** The
  entrypoint then changes no owner and adds no group. The data folders must
  already be writable by that user. Add the groups of the GPU devices yourself
  with `--group-add` (Compose `group_add:`). When the home folder of that user
  is missing or not writable, the entrypoint uses `/data/home`. When `/data`
  is not writable either, it uses a folder in `/tmp`, and the caches are lost
  when the container stops. New named volumes belong to uid 10001, so another
  user cannot write them. LocalAI then still starts and serves the models it
  has, but it logs the `WARN` line, turns add-ons off with an `ERROR` line that
  starts with "Add-ons are off", and backend installs fail. Give the volumes
  to that user, or remove `--user`.
- **Rootless Podman.** Map your host user to uid 10001 in the container, so
  that the folders on the host stay owned by you:

  ```bash
  podman run -d --name local-ai --init -p 8080:8080 \
    --userns=keep-id:uid=10001,gid=10001 \
    -v "$PWD/models:/models" -v "$PWD/backends:/backends" \
    -v "$PWD/data:/data" -v "$PWD/configuration:/configuration" \
    docker.io/localai/localai:latest
  ```

  With this option, LocalAI starts as uid 10001 and the entrypoint adds no
  device groups. For a GPU, add `--group-add keep-groups` (it needs the `crun`
  runtime) so that the container keeps the groups of your host user.
- **Rootless Docker.** Use named volumes. With bind mounts, the files on the
  host get a uid from the subordinate range of your user, not your own uid.
- **Read-only root file system.** All data that LocalAI writes goes to the four
  volumes. Backends and some features also need a temporary folder, so mount
  `/tmp` as a `tmpfs`. This setup should work, but it is not tested in CI yet:

  ```yaml
  services:
    localai:
      image: localai/localai:latest
      read_only: true
      tmpfs:
        - /tmp
      # ... init, restart, ports and the four volumes of docker-compose.yaml
  ```

  With `docker run`, use `--read-only --tmpfs /tmp`.
- **Run as root (`LOCALAI_RUN_AS_ROOT=true`).** LocalAI runs as root, as before
  version 5.0.0. Add-ons that run commands in a sandbox are then unavailable.
- **A network or read-only mount that refuses `chown`.** Make it writable for
  the user of LocalAI on the storage side. Or set `LOCALAI_UID` and
  `LOCALAI_GID` to the owner of the files.

{{% callout style="note" %}}
LocalAI 5.0.1 and earlier always run as uid 10001 unless `LOCALAI_UID` is set,
use `/home/localai` as the home folder, keep generated content and uploads
under `/tmp`, do not add the group of the Docker socket, and do not log the
`WARN` line about folders. Later images do all of this as described above.
{{% /callout %}}

## Next Steps

After installation:

1. Access the WebUI at `http://localhost:8080`
2. Check available models: `curl http://localhost:8080/v1/models`
3. [Install additional models]({{% relref "/models/setting-up-models" %}})
4. [Try out examples]({{% relref "/start/try-it-out" %}})

## Troubleshooting

### Container won't start

- Check container engine is running: `docker ps` or `podman ps`
- Check port 8080 is available: `netstat -an | grep 8080` (Linux/Mac)
- View logs: `docker logs local-ai` or `podman logs local-ai`

### GPU not detected

- Ensure Docker has GPU access: `docker run --rm --gpus all nvidia/cuda:12.0.0-base-ubuntu22.04 nvidia-smi`
- For Podman, pass the GPU with the `--device` flags shown in the GPU sections above (for example `--device nvidia.com/gpu=all`)
- For NVIDIA: Install [NVIDIA Container Toolkit](https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/install-guide.html)
- For AMD: Ensure devices are accessible: `ls -la /dev/kfd /dev/dri`

### NVIDIA Container fails to start with "Auto-detected mode as 'legacy'" error

If you encounter this error:
```
Error response from daemon: failed to create task for container: failed to create shim task: OCI runtime create failed: runc create failed: unable to start container process: error during container init: error running prestart hook #0: exit status 1, stdout: , stderr: Auto-detected mode as 'legacy'
nvidia-container-cli: requirement error: invalid expression
```

This indicates a Docker/NVIDIA Container Toolkit configuration issue. The container runtime's prestart hook fails before LocalAI starts. This is **not** a LocalAI code bug.

**Solutions:**

1. **Use CDI mode (recommended)**: Update your docker-compose.yaml to use the CDI driver configuration:
   ```yaml
   deploy:
     resources:
       reservations:
         devices:
           - driver: nvidia.com/gpu
             count: all
             capabilities: [gpu]
   ```

2. **Upgrade NVIDIA Container Toolkit**: Ensure you have version 1.14 or later, which has better CDI support.

3. **Check NVIDIA Container Toolkit configuration**: Run `nvidia-container-cli --query-gpu` to verify your installation is working correctly outside of containers.

4. **Verify Docker GPU access**: Test with `docker run --rm --gpus all nvidia/cuda:12.0.0-base-ubuntu22.04 nvidia-smi`

### Models not downloading

- Check internet connection
- Verify disk space: `df -h`
- Check container logs for errors: `docker logs local-ai` or `podman logs local-ai`
- If the logs show status 401 or 403, the source needs authentication. See [Private Registries and Galleries]({{% relref "/models/private-sources" %}})

### Permission denied on a data folder

- Look for the `WARN` line at startup that starts with `LocalAI cannot write to some of its folders`. It names the folders, their owner, and the uid and gid of LocalAI.
- Select the setup that matches yours in [Container user and permissions](#container-user-and-permissions).

### An add-on install fails with "invalid or unverified addon package"

LocalAI checks the signature of each add-on with Sigstore. It downloads the
Sigstore trust data once and keeps it in a cache in `/data/cache/sigstore`.
The log line after the error names the check that failed.

The causes below occur on LocalAI 5.0.1, which keeps the cache in the home
folder `/home/localai`. They are fixed in later images. On 5.0.1:

- **A failed download of the trust data.** LocalAI 5.0.1 does not try the
  download again until it restarts. Restart the container
  (`docker compose restart localai` or `docker restart local-ai`) and install
  the add-on again.
- **`LOCALAI_UID` or `LOCALAI_GID` is set.** The home folder `/home/localai`
  stays owned by uid 10001, so LocalAI cannot write its cache. Remove the two
  variables and recreate the container. Or, on 5.0.1, chown `/home/localai` to
  your user and restart. This lasts until the container is recreated:

  ```bash
  docker compose exec -u 0 localai chown -R <uid>:<gid> /home/localai
  docker compose restart localai
  ```

  Replace `localai` with the name of your service when it is different.

- **`docker run --user` or Compose `user:`.** The home folder of that user is
  not writable. Set `HOME=/data/home` in the environment of the container.
- **A read-only root file system.** Set `HOME=/data/home` and mount `/tmp` as
  a `tmpfs`, as shown in [Container user and permissions](#container-user-and-permissions).

## Full image reference

The quick-start examples above use the Docker Hub image names. Every image is published to both [Docker Hub](https://hub.docker.com/r/localai/localai) and [Quay](https://quay.io/repository/go-skynet/local-ai?tab=tags). The tables below map the Docker Hub tag to its Quay equivalent for each variant. Replace `{{< version >}}` with a released version to pin a specific build.

{{< tabs >}}
{{% tab title="Vanilla / CPU Images" %}}

| Description | Quay | Docker Hub |
| --- | --- | --- |
| Latest images from the branch (development) | `quay.io/go-skynet/local-ai:master` | `docker.io/localai/localai:master` |
| Latest tag | `quay.io/go-skynet/local-ai:latest` | `docker.io/localai/localai:latest` |
| Versioned image | `quay.io/go-skynet/local-ai:{{< version >}}` | `docker.io/localai/localai:{{< version >}}` |

{{% /tab %}}

{{% tab title="GPU Images CUDA 12" %}}

| Description | Quay | Docker Hub |
| --- | --- | --- |
| Latest images from the branch (development) | `quay.io/go-skynet/local-ai:master-gpu-nvidia-cuda-12` | `docker.io/localai/localai:master-gpu-nvidia-cuda-12` |
| Latest tag | `quay.io/go-skynet/local-ai:latest-gpu-nvidia-cuda-12` | `docker.io/localai/localai:latest-gpu-nvidia-cuda-12` |
| Versioned image | `quay.io/go-skynet/local-ai:{{< version >}}-gpu-nvidia-cuda-12` | `docker.io/localai/localai:{{< version >}}-gpu-nvidia-cuda-12` |

{{% /tab %}}

{{% tab title="GPU Images CUDA 13" %}}

| Description | Quay | Docker Hub |
| --- | --- | --- |
| Latest images from the branch (development) | `quay.io/go-skynet/local-ai:master-gpu-nvidia-cuda-13` | `docker.io/localai/localai:master-gpu-nvidia-cuda-13` |
| Latest tag | `quay.io/go-skynet/local-ai:latest-gpu-nvidia-cuda-13` | `docker.io/localai/localai:latest-gpu-nvidia-cuda-13` |
| Versioned image | `quay.io/go-skynet/local-ai:{{< version >}}-gpu-nvidia-cuda-13` | `docker.io/localai/localai:{{< version >}}-gpu-nvidia-cuda-13` |

{{% /tab %}}

{{% tab title="Intel GPU" %}}

| Description | Quay | Docker Hub |
| --- | --- | --- |
| Latest images from the branch (development) | `quay.io/go-skynet/local-ai:master-gpu-intel` | `docker.io/localai/localai:master-gpu-intel` |
| Latest tag | `quay.io/go-skynet/local-ai:latest-gpu-intel` | `docker.io/localai/localai:latest-gpu-intel` |
| Versioned image | `quay.io/go-skynet/local-ai:{{< version >}}-gpu-intel` | `docker.io/localai/localai:{{< version >}}-gpu-intel` |

{{% /tab %}}

{{% tab title="AMD GPU" %}}

| Description | Quay | Docker Hub |
| --- | --- | --- |
| Latest images from the branch (development) | `quay.io/go-skynet/local-ai:master-gpu-hipblas` | `docker.io/localai/localai:master-gpu-hipblas` |
| Latest tag | `quay.io/go-skynet/local-ai:latest-gpu-hipblas` | `docker.io/localai/localai:latest-gpu-hipblas` |
| Versioned image | `quay.io/go-skynet/local-ai:{{< version >}}-gpu-hipblas` | `docker.io/localai/localai:{{< version >}}-gpu-hipblas` |

{{% /tab %}}

{{% tab title="Vulkan Images" %}}

| Description | Quay | Docker Hub |
| --- | --- | --- |
| Latest images from the branch (development) | `quay.io/go-skynet/local-ai:master-gpu-vulkan` | `docker.io/localai/localai:master-gpu-vulkan` |
| Latest tag | `quay.io/go-skynet/local-ai:latest-gpu-vulkan` | `docker.io/localai/localai:latest-gpu-vulkan` |
| Versioned image | `quay.io/go-skynet/local-ai:{{< version >}}-gpu-vulkan` | `docker.io/localai/localai:{{< version >}}-gpu-vulkan` |

{{% /tab %}}

{{% tab title="Nvidia Linux for tegra (CUDA 12)" %}}

These images are compatible with Nvidia ARM64 devices with CUDA 12, such as the Jetson Nano, Jetson Xavier NX, and Jetson AGX Orin. For more information, see the [Nvidia L4T guide]({{%relref "/start/install/nvidia-l4t" %}}).

| Description | Quay | Docker Hub |
| --- | --- | --- |
| Latest images from the branch (development) | `quay.io/go-skynet/local-ai:master-nvidia-l4t-arm64` | `docker.io/localai/localai:master-nvidia-l4t-arm64` |
| Latest tag | `quay.io/go-skynet/local-ai:latest-nvidia-l4t-arm64` | `docker.io/localai/localai:latest-nvidia-l4t-arm64` |
| Versioned image | `quay.io/go-skynet/local-ai:{{< version >}}-nvidia-l4t-arm64` | `docker.io/localai/localai:{{< version >}}-nvidia-l4t-arm64` |

{{% /tab %}}

{{% tab title="Nvidia Linux for tegra (CUDA 13)" %}}

These images are compatible with Nvidia ARM64 devices with CUDA 13, such as the Nvidia DGX Spark. For more information, see the [Nvidia L4T guide]({{%relref "/start/install/nvidia-l4t" %}}).

| Description | Quay | Docker Hub |
| --- | --- | --- |
| Latest images from the branch (development) | `quay.io/go-skynet/local-ai:master-nvidia-l4t-arm64-cuda-13` | `docker.io/localai/localai:master-nvidia-l4t-arm64-cuda-13` |
| Latest tag | `quay.io/go-skynet/local-ai:latest-nvidia-l4t-arm64-cuda-13` | `docker.io/localai/localai:latest-nvidia-l4t-arm64-cuda-13` |
| Versioned image | `quay.io/go-skynet/local-ai:{{< version >}}-nvidia-l4t-arm64-cuda-13` | `docker.io/localai/localai:{{< version >}}-nvidia-l4t-arm64-cuda-13` |

{{% /tab %}}

{{< /tabs >}}

## See Also

- [Full image reference](#full-image-reference) - Complete Quay and Docker Hub image matrix
- [Install Models]({{% relref "/models/setting-up-models" %}}) - Install and configure models
- [GPU Acceleration]({{% relref "/models/gpu-acceleration" %}}) - GPU setup and optimization
- [Kubernetes Installation]({{% relref "/start/install/kubernetes" %}}) - Deploy on Kubernetes
