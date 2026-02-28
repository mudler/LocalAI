+++
disableToc = false
title = "Run with Kubernetes"
weight = 6
url = '/basics/kubernetes/'
ico = "rocket_launch"
+++


For installing LocalAI in Kubernetes, the deployment file from the `examples` can be used and customized as preferred:

```
kubectl apply -f https://raw.githubusercontent.com/mudler/LocalAI-examples/refs/heads/main/kubernetes/deployment.yaml
```

For Nvidia GPUs:

```
kubectl apply -f https://raw.githubusercontent.com/mudler/LocalAI-examples/refs/heads/main/kubernetes/deployment-nvidia.yaml
```

Alternatively, the [helm chart](https://github.com/go-skynet/helm-charts) can be used as well:

```bash
helm repo add go-skynet https://go-skynet.github.io/helm-charts/
helm repo update
helm show values go-skynet/local-ai > values.yaml


helm install local-ai go-skynet/local-ai -f values.yaml
```

## Security Context Requirements

LocalAI spawns child processes to run model backends (e.g., llama.cpp, diffusers, whisper). To properly stop these processes and free resources like VRAM, LocalAI needs permission to send signals to its child processes.

If you're using restrictive security contexts, ensure the `CAP_KILL` capability is available:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: local-ai
spec:
  containers:
  - name: local-ai
    image: quay.io/go-skynet/local-ai:latest
    securityContext:
      allowPrivilegeEscalation: false
      capabilities:
        drop:
          - ALL
        add:
          - KILL  # Required for LocalAI to stop backend processes
      seccompProfile:
        type: RuntimeDefault
      runAsNonRoot: true
      runAsUser: 1000
```

Without the `KILL` capability, LocalAI cannot terminate backend processes when models are stopped, leading to:
- VRAM and memory not being freed
- Orphaned backend processes holding GPU resources
- Error messages like `error while deleting process error=permission denied`

## Troubleshooting

### Issue: VRAM is not freed when stopping models

**Symptoms:**
- Models appear to stop but GPU memory remains allocated
- Logs show `(deleteProcess) error while deleting process error=permission denied`
- Backend processes remain running after model unload

**Common Causes:**
- All capabilities are dropped without adding back `CAP_KILL`
- Using user namespacing (`hostUsers: false`) with certain configurations
- Overly restrictive seccomp profiles that block signal-related syscalls
- Pod Security Policies or Pod Security Standards blocking required capabilities

**Solution:**

1. Add the `KILL` capability to your container's security context as shown in the example above.

2. If you're using a Helm chart, configure the security context in your `values.yaml`:

```yaml
securityContext:
  allowPrivilegeEscalation: false
  capabilities:
    drop:
      - ALL
    add:
      - KILL
  seccompProfile:
    type: RuntimeDefault
```

3. Verify the capability is present in the running pod:

```bash
kubectl exec -it <pod-name> -- grep CapEff /proc/1/status
```

4. If running in privileged mode works but the above doesn't, check your cluster's Pod Security Policies or Pod Security Standards. You may need to adjust cluster-level policies to allow the `KILL` capability.

5. Ensure your seccomp profile (if custom) allows the `kill` syscall. The `RuntimeDefault` profile typically includes this.
