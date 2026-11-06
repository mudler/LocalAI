# Skill: Manage distributed scheduling

Use this when the user asks to inspect, set, or remove distributed per-model scheduling rules.

1. Call `system_info` first. If `distributed` is false, explain that scheduling tools only affect distributed deployments.
2. For inspection, call `list_scheduling` or `get_scheduling` and summarize model name, selector, replica bounds, spread-all, and route policy fields.
3. Before calling `set_scheduling` or `delete_scheduling`, follow safety rule 1 and wait for explicit confirmation.
4. For `set_scheduling`, never combine `spread_all: true` with non-zero `min_replicas` or `max_replicas`.
5. After a confirmed mutation, call `get_scheduling` for that model and summarize the persisted config.
