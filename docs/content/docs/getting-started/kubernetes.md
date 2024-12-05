+++
disableToc = false
title = "Run with Kubernetes"
weight = 6
url = '/basics/kubernetes/'
ico = "rocket_launch"
+++


For installing LocalAI in Kubernetes, the deployment file from the `examples` can be used and customized as prefered:

```
kubectl apply -f https://raw.githubusercontent.com/mudler/LocalAI-examples/refs/heads/main/kubernetes/deployment.yaml
```

For Nvidia GPUs:

```
kubectl apply -f https://raw.githubusercontent.com/mudler/LocalAI-examples/refs/heads/main/kubernetes/deployment-nvidia.yaml
```

Alternatively, the [helm chart](https://github.com/go-skynet/helm-charts) can be used as well:

```bash
# Install the helm repository
helm repo add go-skynet https://go-skynet.github.io/helm-charts/
# Update the repositories
helm repo update
# Get the values
helm show values go-skynet/local-ai > values.yaml

# Edit the values value if needed
# vim values.yaml ...

# Install the helm chart
helm install local-ai go-skynet/local-ai -f values.yaml
```
