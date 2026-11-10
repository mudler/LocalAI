+++
disableToc = false
title = "Run with Kubernetes"
description = "Install LocalAI on Kubernetes with the Helm chart or with example manifests."
icon = "rocket_launch"
weight = 90
aliases = ["/basics/kubernetes/"]
keywords = ["kubernetes", "helm", "install", "cluster"]
+++

Use the Helm chart to run LocalAI on Kubernetes. Add the chart repository first, then install the chart:

```bash
helm repo add go-skynet https://go-skynet.github.io/helm-charts/
helm repo update
helm show values go-skynet/local-ai > values.yaml
helm install local-ai go-skynet/local-ai -f values.yaml
```

Edit `values.yaml` before you install. The chart is maintained in the [helm-charts repository](https://github.com/go-skynet/helm-charts). The chart repository is separate from the LocalAI release. Check its `values.yaml` for the image tag it uses.

## Example manifests

You can also apply a plain deployment file from the examples repository:

```bash
kubectl apply -f https://raw.githubusercontent.com/mudler/LocalAI-examples/refs/heads/main/kubernetes/deployment.yaml
```

For NVIDIA GPUs:

```bash
kubectl apply -f https://raw.githubusercontent.com/mudler/LocalAI-examples/refs/heads/main/kubernetes/deployment-nvidia.yaml
```

## Next steps

- For several nodes with a shared database, see [Distributed mode]({{% relref "/administration/distributed-mode" %}}).
- For GPU images, see [GPU acceleration]({{% relref "/models/gpu-acceleration" %}}).
- Before you expose the service, read [Authentication]({{% relref "/administration/authentication" %}}).
