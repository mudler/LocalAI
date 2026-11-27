---
description: Manage scheduling rules, carrier weights and model memory limits.
---
# Skill: Manage distributed scheduling

Use this when the user asks to inspect, set, or remove distributed per-model scheduling rules.

1. Call `system_info` first. If `distributed` is false, explain that scheduling tools only affect distributed deployments.
2. For inspection, call `list_scheduling` or `get_scheduling` and summarize model name, selector, replica bounds, spread-all, and route policy fields.
3. Before calling `set_scheduling` or `delete_scheduling`, follow safety rule 1 and wait for explicit confirmation.
4. For `set_scheduling`, never combine `spread_all: true` with non-zero `min_replicas` or `max_replicas`. `upgrade_strategy` is `stop_and_start` (default) or `canary`; say that canary is stored but upgrades still stop and start the model.
5. After a confirmed mutation, call `get_scheduling` for that model and summarize the persisted config.
6. For the carrier weights, call `get_carrier_weights`. The weights (`nats`, `websocket`, `p2p`; 0 to 1000, higher wins) only break a tie between nodes that fit a request equally well, and never override fit. 0 means: use that carrier only when no other fits. Before `set_carrier_weights`, follow safety rule 1. A weight left out stays. After a confirmed change, call `get_carrier_weights` and say what is now in force. The change reaches every frontend within seconds.
7. For which models stay in memory (model limit, `model_swap`, idle unload, reclaimer, GPU budget), use `set_model_memory_policy` with only the fields to change; it works in single mode too. `model_swap` on: a load that needs room unloads the least recently used idle model; off: nothing is unloaded for a load, the request waits, then gets 503 with Retry-After. For one node (distributed only), use `set_node_limits` with the node id from `list_nodes`, or `reset: true` to clear its overrides; it returns each value and its source (override, worker, cluster, default). A node override wins over the worker's environment, which wins over the cluster value. Follow safety rule 1 before either tool.
8. To take a node out of service (maintenance, a GPU lease, an update), use `drain_node` with `action: drain` and the node id from `list_nodes`. Say that nothing new goes to the node, that its models keep serving until replacements load on other nodes, and that what is left is removed at the deadline (`timeout`, server default 10m). Use `force: true` only when the user accepts that the node's models stop at once. Then call `list_nodes`: the node is `draining`, and it is empty when `leaving_replicas` is 0; only then may the worker be stopped. `action: resume` cancels the drain. Follow safety rule 1 before either action.
