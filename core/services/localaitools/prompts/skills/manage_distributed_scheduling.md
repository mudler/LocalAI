---
description: Manage per-model scheduling rules and carrier weights of a distributed setup.
---
# Skill: Manage distributed scheduling

Use this when the user asks to inspect, set, or remove distributed per-model scheduling rules.

1. Call `system_info` first. If `distributed` is false, explain that scheduling tools only affect distributed deployments.
2. For inspection, call `list_scheduling` or `get_scheduling` and summarize model name, selector, replica bounds, spread-all, and route policy fields.
3. Before calling `set_scheduling` or `delete_scheduling`, follow safety rule 1 and wait for explicit confirmation.
4. For `set_scheduling`, never combine `spread_all: true` with non-zero `min_replicas` or `max_replicas`. `upgrade_strategy` is `stop_and_start` (default) or `canary`; say that canary is stored but upgrades still stop and start the model.
5. After a confirmed mutation, call `get_scheduling` for that model and summarize the persisted config.
6. For the carrier weights, call `get_carrier_weights`. The weights (`nats`, `websocket`, `p2p`; 0 to 1000, higher wins) only break a tie between nodes that fit a request equally well, and never override fit. 0 means: use that carrier only when no other fits. Before `set_carrier_weights`, follow safety rule 1. A weight left out stays. After a confirmed change, call `get_carrier_weights` and say what is now in force. The change reaches every frontend within seconds.
