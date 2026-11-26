+++
disableToc = false
title = "Kubernetes"
weight = 90
+++

## At a glance

| Topic | Detail |
|---|---|
| What it adds | Tools to read a Kubernetes cluster, and to change it with a confirmation each time unless you say otherwise. An administrator sets a policy for each cluster. |
| Needs | A connection with a kubeconfig of one context, with its data embedded. |

Read and change a Kubernetes cluster from your [chats]({{% relref "/features/chats-and-agents/chats" %}}) and [saved agents]({{% relref "/features/chats-and-agents/chats-agents" %}}). The add-on calls the API server directly. It does not run `kubectl`.

A chat can answer questions such as:

- "Why does the web pod in team-a restart?"
- "Show the last 100 lines of the api logs."
- "Scale web to 3 replicas." (a change: it waits for your confirmation)
- "Drain node-1 for maintenance." (a change: it waits for your confirmation)

## Set it up

1. Install the **Kubernetes** add-on from **Find add-ons** and open its page.
2. In the cluster, make a service account for LocalAI with only the rights the task needs. The RBAC of the credential is the outer limit of everything the add-on can do.
3. Make a kubeconfig with one context and the data embedded:

   ```bash
   kubectl config view --minify --flatten --context=<name>
   ```

   For a service account, make a token and use the form with the server, the CA and the token instead:

   ```bash
   kubectl -n <namespace> create token <service-account> --duration=720h
   ```

4. On the add-on page, choose a new connection. Paste the kubeconfig and pick the context, or type the server URL, paste the CA and the token.
5. Press **Save**. The page runs the check: it shows the server version and the RBAC rules of the credential in the `default` namespace.

One connection is one cluster and one context. The connection lists the cluster as `https://<host>:<port>#context=<name>`. A chat uses the connections you enable for it in the **Tools** menu. With two or more enabled, the model names the cluster in each call.

### What the kubeconfig may hold

The kubeconfig is the sealed secret of the connection, at most 16 KiB. The add-on uses only the context of the connection. It refuses a kubeconfig that:

- runs a credential plugin (`exec` or `auth-provider`), such as the login helpers of cloud providers;
- names a local file (`certificate-authority`, `client-certificate`, `client-key`, `tokenFile`). Embed the data with the `-data` fields;
- turns off certificate checks (`insecure-skip-tls-verify: true`) or uses a proxy (`proxy-url`);
- uses a user name and a password.

The add-on never reads a file on the LocalAI host because a kubeconfig names it. The kubeconfig is never in a result, an error, a log or a preview.

## What the tools do

| Tool | Kind | What it does |
|---|---|---|
| `list` | read | A table of one kind: name, namespace, status, age and columns of the kind. At most 500 rows. |
| `get` | read | One object as YAML, without managed fields. |
| `describe` | read | A summary of one object, the related objects and the newest events. |
| `logs` | read | The newest lines of a container, cut at 32 KiB. |
| `events` | read | Events, newest first, for a namespace or one object. |
| `top` | read | CPU and memory use of nodes or pods. Needs metrics-server. |
| `api-resources` | read | The kinds the cluster serves, including custom resources. |
| `check` | read | The server version and the RBAC rules of the credential in a namespace. |
| `scale` | write | Set the replicas of a Deployment, StatefulSet or ReplicaSet. |
| `rollout-restart` | write | Restart the pods of a Deployment, StatefulSet or DaemonSet. |
| `rollout-undo` | write | Roll a Deployment back to the previous revision or a named one. |
| `delete-pod` | write | Delete one pod. |
| `cordon`, `uncordon` | write | Mark a node unschedulable or schedulable. |
| `drain` | write | Cordon a node and evict its pods through the Eviction API, so PodDisruptionBudgets hold. |
| `apply` | write | Server-side apply of one namespaced object, with field manager `localai-kubernetes`. |
| `patch` | write | A strategic merge, merge or JSON patch of one namespaced object. |
| `exec` | write | Run one command in a container, with no terminal and no input. Off by default. |

Everything the cluster returns is data, and every result says so. A model must not follow instructions it finds in a log, a label or an event.

## Changes and confirmation

Every write tool declares `approval: always`. By default a person confirms **every call**: the card shows what will change. For `apply` and `patch` the card shows a diff of the live object against the result of a server-side dry run (`dryRun=All`). The API server runs its defaults, validation and admission for the dry run, so the diff is what the write will really do.

When you confirm, the add-on checks the call again. If the object or the policy changed after the card was made, nothing is written and the result says the preview is stale.

### Not asking for some tools

On the add-on page, the section for approvals has one choice for each write tool: ask each time (the default) or always run. The choice is your own user setting. No chat, agent or model can change it.

- It holds only for your own chats. Triggers, jobs, sub-agents and messaging senders that are not paired with you are still asked.
- A call that runs without a card goes through the same checks and the same policy. For `apply` and `patch` the result still carries the diff.
- The write mode **Allow all** of a connection lifts the confirmation of every write tool for your own chats.

## The policy

An administrator sets a policy for each cluster on the add-on page. The add-on checks it on every call, confirmed or not. Skipping a confirmation never widens what a call can touch.

| Rule | Default | What it does |
|---|---|---|
| `namespaces` | `*` | The namespaces the tools may read and change. A namespaced call outside the list is refused. A list across all namespaces returns only the allowed ones, whatever the selectors say. |
| `writeTools` | all except `exec` | The write tools that work. A disabled tool is still offered to the model and answers that the policy disabled it. |
| `readSecretValues` | off | When off, Secret values are hidden everywhere (see below). |

Cluster-scoped objects are always readable. The only cluster-scoped writes are `cordon`, `uncordon` and `drain` of a node. `apply` and `patch` of a cluster-scoped kind (a Namespace, a ClusterRole, a CustomResourceDefinition) are refused.

A refusal names its rule, for example `refused by the connection policy (namespaces)`. A policy file that cannot be read refuses every tool until an administrator saves the policy again.

The policy lives in the data folder of the add-on, keyed by the cluster, like the settings of the other add-ons. Two connections to the same cluster and context share one policy.

## Secret values

With `readSecretValues` off:

- A Secret shows its keys, its type and the length of each value, never the value. Its `last-applied-configuration` annotation is removed.
- An environment variable whose name holds `TOKEN`, `PASSWORD`, `SECRET` or `KEY` (in any case) shows `***` instead of an inline value, in any container list of any object. A `secretKeyRef` stays visible: it names a Secret and a key, and holds no value.
- The diff of `apply` or `patch` on a Secret shows the keys and marks the ones whose value changes.

`exec` output is not filtered: a command can print a Secret that the container holds. That is why `exec` is off by default.

## Errors

API errors keep their status, reason and message, so the model can act on them. A 403 says the RBAC of the credential does not allow the call; a 401 says the cluster refused the credential. `scale` and `rollout-restart` try once more after a 409 conflict. Nothing else is retried. A server-side apply does not force ownership: a conflict with another field manager comes back as an error.

## Limits

- The kubeconfig is at most 16 KiB, with one context and embedded data. No credential plugins and no file references.
- `drain` waits at most 45 seconds per call, because LocalAI stops a write after 60 seconds. A result with `pending` pods means the drain goes on with a second call; the node is already cordoned.
- `exec` runs for at most 45 seconds and its output is not filtered. One `exec` runs per pod at a time.
- No cluster-scoped `apply` or `patch`, no port forwarding, no interactive exec or attach, no file copy, no Helm and no watches.
- `top` needs the `metrics.k8s.io` API (metrics-server).
