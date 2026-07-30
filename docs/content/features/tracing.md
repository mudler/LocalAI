+++
disableToc = false
title = "Tracing"
weight = 83
url = '/features/tracing'
+++

LocalAI can retain recent API exchanges and backend operations for inspection
on the **Traces** page in the management interface. Enable tracing in runtime
settings or with the existing tracing configuration.

API and backend trace histories are persisted in separate directories below
the configured data path. They are restored after a clean service restart,
whether or not authentication is enabled.

Each history remains independently bounded by `tracing_max_items`. When a
history reaches that limit, LocalAI removes its oldest records from memory and
disk. The existing clear actions on the Traces page remove both the in-memory
history and its persisted records.
