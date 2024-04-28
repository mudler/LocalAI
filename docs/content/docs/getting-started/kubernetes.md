+++
disableToc = false
title = "Run with Kubernetes"
weight = 6
url = '/basics/kubernetes/'
ico = "rocket_launch"
+++

For installing LocalAI in Kubernetes, you can use the `go-skynet` helm chart:

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

If you prefer to install from manifest file, you can install from the deployment file, and customize as you like:

```
kubectl apply -f https://raw.githubusercontent.com/mudler/LocalAI/master/examples/kubernetes/deployment.yaml
```