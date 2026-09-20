# Skill: Throttle a gallery download

1. Ask the user for the operation `job_id` (from `get_job_status` or the install response) and the desired `rate` (e.g. `2mb`, `500kb`, or `0` to remove the limit) when either is missing.
2. Confirm the throttle target and rate with the user per safety rule 1.
3. Call `throttle_operation` with `job_id` and `rate`.
4. On error, surface the message verbatim and wait for instruction. On success, confirm the applied rate and note it applies to in-flight reads immediately.
