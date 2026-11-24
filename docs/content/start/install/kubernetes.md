+++
disableToc = false
title = "Run with Kubernetes"
description = "Install LocalAI on Kubernetes with the Helm chart or with example manifests."
icon = "rocket_launch"
weight = 90
aliases = ["/basics/kubernetes/"]
keywords = ["kubernetes", "helm", "install", "cluster"]
+++

Install LocalAI on Kubernetes with the Helm chart. LocalAI listens with authentication off, so read [Authentication]({{% relref "/administration/authentication" %}}) before you expose the service.

```bash
helm repo add go-skynet https://go-skynet.github.io/helm-charts/
helm repo update
helm show values go-skynet/local-ai > values.yaml
helm install local-ai go-skynet/local-ai -f values.yaml
```

Edit `values.yaml` before you install. The chart is maintained in the [helm-charts repository](https://github.com/go-skynet/helm-charts) and released separately from LocalAI, so its default image can be older than the latest LocalAI release. Check the image tag in its `values.yaml` and set a newer one if you need it.

To reach the server from your computer, forward its port and check that it answers. Use the service name of your release:

```bash
kubectl port-forward svc/<service-name> 8080:8080
curl http://localhost:8080/readyz
```

## Example manifests

You can also apply a plain deployment file from the examples repository. The links follow the `main` branch, so read the file before you apply it:

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
