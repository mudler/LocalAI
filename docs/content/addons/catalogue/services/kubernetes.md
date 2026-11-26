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
- names a server by a loopback, link-local, unspecified or multicast IP address, such as `127.0.0.1`, `::1` or `169.254.169.254`, or by a cloud metadata address outside those ranges (`fd00:ec2::254`, `100.100.100.200`), also when it is written inside a NAT64 address (`64:ff9b::7f00:1`). The credential would go to a service of the LocalAI host or of its network. There is no setting to allow these addresses.

A host name can resolve to such an address too: `localhost`, a DNS record that points at `127.0.0.1` or at a metadata address, or a form that only the system resolver reads as an address, such as `2130706433`. So the add-on also checks the address of every connection after the name is resolved, and refuses the same addresses. It connects directly: it does not use `HTTP_PROXY`, `HTTPS_PROXY` or another proxy setting of the LocalAI host. The WebSocket connection of `exec` cannot use that check, so the add-on resolves the name and checks its addresses just before `exec` connects; a name whose DNS answer changes in that short time can still pass, and the SPDY fallback of `exec` is checked when it connects.

For a local cluster made by kind, do not use the kubeconfig that names `127.0.0.1` or `localhost`. When LocalAI runs in a container on the `kind` Docker network, use `kind get kubeconfig --internal`, which names the server by its container (`https://<cluster>-control-plane:6443`). Otherwise, set `networking.apiServerAddress` in the kind configuration to an address of the host that is not loopback, such as its LAN address, and use the kubeconfig that kind writes. The certificate of the API server covers that address.

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

The policy of a cluster is keyed by its server URL, written one way: the host in lower case without trailing dots, an IP address in its canonical form (`2001:0db8::0001` is `2001:db8::1`, and `::ffff:10.0.0.1` is `10.0.0.1`), the port read as a number and left out when it is 443 (`:0443` is left out too), and no trailing slash. A server URL with a query or a fragment, even an empty one (a bare `?` or `#`), is refused. A host name in Unicode is not converted to its punycode form (`xn--`): the two forms of one name are two keys, so write the server the same way in every kubeconfig. Every context and every connection of one server share one policy, so renaming a context in the kubeconfig, or writing `:443`, does not change which policy applies.

The add-on does not resolve names to match policies. Another name for the same API server, such as a second DNS name or its IP address instead of its name, is another key, and it gets the default policy. So set a strict default policy (few namespaces, no write, Secret values hidden) and give each cluster its own policy, rather than a broad default.

A cluster with no policy of its own uses the **default policy**. Out of the box the default allows reads in every namespace, Secret values included, and **no write**: the page shows such a cluster as "Not configured: writes are off" until an administrator saves a policy for it. An administrator can also change the default policy itself: choose **Default for clusters with no policy** in the cluster list.

Cluster-scoped objects are always readable. The only cluster-scoped writes are `cordon`, `uncordon` and `drain` of a node. `apply` and `patch` of a cluster-scoped kind (a Namespace, a ClusterRole, a CustomResourceDefinition) are refused.

A refusal names its rule, for example `refused by the connection policy (namespaces)`.

The policy is kept in `policy.json` in the data folder of the add-on. If that file cannot be read, every cluster gets no namespace, no write, and Secret values hidden, until an administrator saves a policy again. The damaged file may have held any policy, so this is the only policy that is never broader than one an administrator set. Cluster-scoped objects, such as nodes, stay readable, as under every policy. The page keeps working, so an administrator can repair the file there.

The next save keeps the damaged file aside as `policy.json.damaged-<time>` and starts a new file. That new file stores the same no-namespace policy as its default policy, and holds only the policy that was saved. Every other cluster keeps the no-namespace policy until an administrator enters its policy again; the old policies are in the file kept aside. A save that names only some rules takes the others from the no-namespace policy, so it must name the namespaces.

Policies saved by an earlier build were keyed by server and context, or by a server written another way. They move to the key of their server by themselves; an entry already under that key wins. When two entries of one server had different policies, neither applies: the page lists them, the server gets the no-namespace policy (no namespace, no write, Secret values hidden) rather than the default policy, which may be broader than both, and saving a policy for that server removes them. The page shows such a server, and every cluster under a no-namespace default policy, as "No namespace: reads and writes are off". An entry whose key is not a server the add-on accepts (for example a loopback address) never applies. The page lists it on its own, and the next save removes it.

With several frontends, the add-on runs on the add-on owner, and every frontend sends its tool calls there. So there is one policy file, on the instance that runs the add-on owner.

## Secret values

The model sees Secret values unless an administrator turns `readSecretValues` off, for one cluster or in the default policy. A value the model reads goes to the model and stays in the chat history.

With `readSecretValues` off, "hidden" means that the read tools (`get`, `describe`, `list`) and the diffs and results of the write tools do not show a value:

- A Secret shows its keys, its type and the length of each value, never the value. Its `last-applied-configuration` annotation is removed, and its other annotations show only their length, except `kubernetes.io/service-account.name` and `kubernetes.io/service-account.uid`.
- An environment variable whose name holds `TOKEN`, `PASSWORD`, `SECRET` or `KEY` (in any case) shows `***` instead of an inline value. This covers every container list (`containers`, `initContainers`, `ephemeralContainers`) at any depth of any object: pods, the pod templates of Deployments, StatefulSets, DaemonSets, ReplicaSets, Jobs and the `jobTemplate` of CronJobs, and custom resources that use the same field names. A `secretKeyRef` stays visible: it names a Secret and a key, and holds no value.
- The diff of `apply` or `patch` marks a Secret key, a Secret annotation or a hidden environment value that the write changes as `(new value)`, without the value.
- A `json` patch of any kind may not use `copy`, `move` or `test`, and may not name an `env` list or a place under one. On a Secret it may also not name `data`, `stringData` or the whole object. Those operations could copy a value to a place that is shown, rename an environment variable so that its value shows, or answer yes or no about a value. Use a `merge` or `strategic` patch.
- `apply` may not set a Secret key under `data` or `stringData` that the live Secret already has, a Secret annotation that the live Secret already has and that is masked, the `kubectl.kubernetes.io/last-applied-configuration` annotation of a Secret, or the inline value of an environment variable with a secret-looking name that the live object already has (matched by container list, container name and variable name). The add-on applies without force, so when another field manager owns the value, a wrong value comes back as a conflict and the right one does not. Leave those values out of the manifest, change them with a `merge` or `strategic` patch that replaces the whole value, or ask an administrator. A new Secret, and new keys and annotations of an existing one, can still be applied.
- While values are hidden, an `apply` to an object that exists carries the `resourceVersion` the add-on read for that check, unless the manifest names one. So if another writer changes the object between the check and the write, the apply fails with a 409 conflict whatever the manifest holds; apply again.
- An API error about a Secret keeps only its status code and reason, because the API server can quote a value in the message.

"Hidden" does not cover:

- the `args` and `command` of a container, environment values whose name does not look secret, and other fields of custom resources;
- what a workload shows. A pod that a chat creates with `apply` can mount a Secret or print it, and its `logs` then show the value. `exec` output is not filtered: a command can print a Secret that the container holds;
- what the cluster itself puts in events or in the status of an object;
- the diff of a `merge` or `strategic` patch that sets a hidden value. It says "no change" when the new value equals the live one and `(new value)` when it differs, so a chat that may patch can test a guess of a value, one guess per preview. Leave `patch` off for a cluster where that matters: with `patch` off and the `apply` checks above, the write tools give no way to test a guess.

To keep a chat from reading Secret values through a workload as well, turn `readSecretValues` off and leave `apply`, `patch` and `exec` off for that cluster, or limit the RBAC of the credential. Out of the box every write tool is off.

## Errors

API errors keep their status, reason and message, so the model can act on them. The message, and the reason a drain could not evict a pod, are quoted and marked as data, not instructions, because an admission webhook can write any text there. An error about a Secret keeps only its status and reason while the policy hides Secret values. A 403 says the RBAC of the credential does not allow the call; a 401 says the cluster refused the credential. `scale` tries once more after a 409 conflict: it writes with the `resourceVersion` it read, so another writer that came first makes it fail instead of being overwritten. Nothing else is retried. A server-side apply does not force ownership: a conflict with another field manager comes back as an error.

## Limits

- The kubeconfig is at most 16 KiB, with one context and embedded data. No credential plugins and no file references.
- One `drain` call ends within 50 seconds, checks included, because LocalAI stops a write after 60 seconds. It evicts at most 50 pods, starts no eviction in the last 5 seconds, and waits for the evicted pods to go at most `timeoutSeconds` (at most 45). The result lists `pending` pods (evicted, not gone yet) and `remaining` pods (not evicted yet); a second call continues, and the node is already cordoned. An error after the cordon ends the call with a partial result and an `error` field, not with an error that says nothing changed. A 429 from API Priority and Fairness is reported as throttling, not as a PodDisruptionBudget; after one, the drain waits the `Retry-After` of the server (1 to 2 seconds) before the next eviction, and stops evicting when that wait would not fit in the 50 seconds.
- `exec` runs for at most 45 seconds and its output is not filtered. One `exec` runs per pod at a time.
- No cluster-scoped `apply` or `patch`, no port forwarding, no interactive exec or attach, no file copy, no Helm and no watches.
- `top` needs the `metrics.k8s.io` API (metrics-server).
- A read ends within 28 seconds, because LocalAI stops a read after 30. A `list` across many allowed namespaces reads them one by one; when the time runs out it returns what it read with `partial: true` and the namespaces in `notRead`.
- `describe` of a Deployment reads at most 100 of its replica sets, by the labels of its selector, and at most 20 of its pods.
