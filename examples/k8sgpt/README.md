# k8sgpt example

This example show how to use LocalAI with k8sgpt

![Screenshot from 2023-06-19 23-58-47](https://github.com/go-skynet/go-ggml-transformers.cpp/assets/2420543/cab87409-ee68-44ae-8d53-41627fb49509)

## Create the cluster locally with Kind (optional)

If you want to test this locally without a remote Kubernetes cluster, you can use kind.

Install [kind](https://kind.sigs.k8s.io/) and create a cluster:

```
kind create cluster
```

## Setup LocalAI

We will use [helm](https://helm.sh/docs/intro/install/):

```
helm repo add go-skynet https://go-skynet.github.io/helm-charts/
helm repo update

# Clone LocalAI
git clone https://github.com/go-skynet/LocalAI

cd LocalAI/examples/k8sgpt

# modify values.yaml preload_models with the models you want to install.
# CHANGE the URL to a model in huggingface.
helm install local-ai go-skynet/local-ai --create-namespace --namespace local-ai --values values.yaml
```

## Setup K8sGPT

```
# Install k8sgpt
helm repo add k8sgpt https://charts.k8sgpt.ai/
helm repo update
helm install release k8sgpt/k8sgpt-operator -n k8sgpt-operator-system --create-namespace --version 0.0.17
```

Apply the k8sgpt-operator configuration:

```
kubectl apply -f - << EOF
apiVersion: core.k8sgpt.ai/v1alpha1
kind: K8sGPT
metadata:
  name: k8sgpt-local-ai
  namespace: default
spec:
  backend: localai
  baseUrl: http://local-ai.local-ai.svc.cluster.local:8080/v1
  noCache: false
  model: gpt-3.5-turbo
  version: v0.3.0
  enableAI: true
EOF
```

## Test

Apply a broken pod:

```
kubectl apply -f broken-pod.yaml
```

## ArgoCD Deployment Example
[Deploy K8sgpt + localai with Argocd](https://github.com/tyler-harpool/gitops/tree/main/infra/k8gpt)
