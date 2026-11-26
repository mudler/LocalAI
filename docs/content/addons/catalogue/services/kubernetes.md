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
5. Press **Save**. The page runs the check: it shows the server version and the RBAC rules of the credential in the `default` namespace, or in the first namespace the policy allows when `default` is not allowed.

One connection is one cluster and one context. The connection lists the cluster as `https://<host>:<port>#context=<name>`. The host is written in lower case, and the port is left out when it is 443. A chat uses the connections you enable for it in the **Tools** menu. With two or more enabled, the model names the cluster in each call.

### What the kubeconfig may hold

The kubeconfig is the sealed secret of the connection, at most 16 KiB. The add-on uses only the context of the connection. It refuses a kubeconfig that:

- runs a credential plugin (`exec` or `auth-provider`), such as the login helpers of cloud providers;
- names a local file (`certificate-authority`, `client-certificate`, `client-key`, `tokenFile`). Embed the data with the `-data` fields;
- turns off certificate checks (`insecure-skip-tls-verify: true`) or uses a proxy (`proxy-url`);
- uses a user name and a password;
- names a server by a loopback, link-local, unspecified or multicast IP address, such as `127.0.0.1`, `::1` or `169.254.169.254`. The credential would go to a service of the LocalAI host or of its network. Host names are not resolved, so a local cluster (for example one made by kind) works when its kubeconfig names the server `https://localhost:<port>`. There is no setting to allow these addresses.

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

| Rule | Built-in default | What it does |
|---|---|---|
| `namespaces` | `*` | The namespaces the tools may read and change. A namespaced call outside the list is refused. A list across all namespaces returns only the allowed ones, whatever the selectors say. `check` also stays inside the list. |
| `writeTools` | none | The write tools that work. A disabled tool is still offered to the model and answers that the policy disabled it. |
| `readSecretValues` | on | When on, the tools show Secret values to the model. When off, they hide them. See [Secret values](#secret-values) for what hiding covers and what it does not. |

The policy of a cluster is keyed by its server URL, written one way: the host in lower case without a trailing dot, the port left out when it is 443, and no trailing slash. Every context and every connection of one server share one policy, so renaming a context in the kubeconfig, or writing `:443`, does not change which policy applies.

A cluster with no policy of its own uses the **default policy**. Out of the box the default allows reads in every namespace, Secret values included, and **no write**: the page shows such a cluster as "Not configured: writes are off" until an administrator saves a policy for it. An administrator can also change the default policy itself: choose **Default for clusters with no policy** in the cluster list.

Cluster-scoped objects are always readable. The only cluster-scoped writes are `cordon`, `uncordon` and `drain` of a node. `apply` and `patch` of a cluster-scoped kind (a Namespace, a ClusterRole, a CustomResourceDefinition) are refused.

A refusal names its rule, for example `refused by the connection policy (namespaces)`.

The policy is kept in `policy.json` in the data folder of the add-on. If that file cannot be read, every cluster gets reads only: no write, and Secret values hidden, until an administrator saves a policy again. A damaged file never gives more than that. The next save keeps the damaged file aside as `policy.json.damaged-<time>` and starts a new file, and a save that names only some rules takes the others from that reads-only policy.

Policies saved by an earlier build were keyed by server and context. They move to the key of their server by themselves. When two contexts of one server had different policies, neither applies: the page lists them, the server uses the default policy, and saving a policy for that server removes them.

With several frontends, the add-on runs on the add-on owner, and every frontend sends its tool calls there. So there is one policy file, on the instance that runs the add-on owner.

## Secret values

The model sees Secret values unless an administrator turns `readSecretValues` off, for one cluster or in the default policy. A value the model reads goes to the model and stays in the chat history.

With `readSecretValues` off, "hidden" means that the read tools (`get`, `describe`, `list`) and the diffs and results of the write tools do not show a value:

- A Secret shows its keys, its type and the length of each value, never the value. Its `last-applied-configuration` annotation is removed, and its other annotations show only their length, except `kubernetes.io/service-account.name` and `kubernetes.io/service-account.uid`.
- An environment variable whose name holds `TOKEN`, `PASSWORD`, `SECRET` or `KEY` (in any case) shows `***` instead of an inline value. This covers every container list (`containers`, `initContainers`, `ephemeralContainers`) at any depth of any object: pods, the pod templates of Deployments, StatefulSets, DaemonSets, ReplicaSets, Jobs and the `jobTemplate` of CronJobs, and custom resources that use the same field names. A `secretKeyRef` stays visible: it names a Secret and a key, and holds no value.
- The diff of `apply` or `patch` marks a Secret key, a Secret annotation or a hidden environment value that the write changes as `(new value)`, without the value.
- A `json` patch of a Secret may not use `copy`, `move` or `test`, and may not name `data`, `stringData` or the whole object. Those operations could copy a value to a place that is shown, or answer yes or no about a value. Use a `merge` or `strategic` patch.
- An API error about a Secret keeps only its status code and reason, because the API server can quote a value in the message.

"Hidden" does not cover:

- the `args` and `command` of a container, environment values whose name does not look secret, and other fields of custom resources;
- what a workload shows. A pod that a chat creates with `apply` can mount a Secret or print it, and its `logs` then show the value. `exec` output is not filtered: a command can print a Secret that the container holds;
- what the cluster itself puts in events or in the status of an object.

To keep a chat from reading Secret values through a workload as well, turn `readSecretValues` off and leave `apply`, `patch` and `exec` off for that cluster, or limit the RBAC of the credential. Out of the box every write tool is off.

## Errors

API errors keep their status, reason and message, so the model can act on them. The message, and the reason a drain could not evict a pod, are quoted and marked as data, not instructions, because an admission webhook can write any text there. An error about a Secret keeps only its status and reason while the policy hides Secret values. A 403 says the RBAC of the credential does not allow the call; a 401 says the cluster refused the credential. `scale` tries once more after a 409 conflict: it writes with the `resourceVersion` it read, so another writer that came first makes it fail instead of being overwritten. Nothing else is retried. A server-side apply does not force ownership: a conflict with another field manager comes back as an error.

## Limits

- The kubeconfig is at most 16 KiB, with one context and embedded data. No credential plugins and no file references.
- One `drain` call ends within 50 seconds, checks included, because LocalAI stops a write after 60 seconds. It evicts at most 50 pods, starts no eviction in the last 5 seconds, and waits for the evicted pods to go at most `timeoutSeconds` (at most 45). The result lists `pending` pods (evicted, not gone yet) and `remaining` pods (not evicted yet); a second call continues, and the node is already cordoned. An error after the cordon ends the call with a partial result and an `error` field, not with an error that says nothing changed. A 429 from API Priority and Fairness is reported as throttling, not as a PodDisruptionBudget.
- `exec` runs for at most 45 seconds and its output is not filtered. One `exec` runs per pod at a time.
- No cluster-scoped `apply` or `patch`, no port forwarding, no interactive exec or attach, no file copy, no Helm and no watches.
- `top` needs the `metrics.k8s.io` API (metrics-server).
- A read ends within 28 seconds, because LocalAI stops a read after 30. A `list` across many allowed namespaces reads them one by one; when the time runs out it returns what it read with `partial: true` and the namespaces in `notRead`.
- `describe` of a Deployment reads at most 100 of its replica sets, by the labels of its selector, and at most 20 of its pods.
