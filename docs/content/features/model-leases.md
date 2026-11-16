+++
title = "Model preload leases"
+++

Administrators can request an advisory lease when preloading a model:

```http
POST /backend/load
Content-Type: application/json

{"model":"trellis2-4b","lease":true}
```

The response includes `lease_id` and `lease_expires`, including after a partial
load failure. Release the lease when finished with
`POST /backend/leases/{lease_id}/release`, using the same administrator account.
The response reports `unloaded`, `absent`, or `retained` with a reason for each
model. A pipeline preload can cover several sub-models.

Leases do not grant additional permissions. Both preload and release retain
their administrator requirement. Clients without permission should record that
cleanup was unavailable and continue their workflow; failure to unload must not
turn successful inference into a failed generation.

Release is best-effort. It only unloads the exact local instance cold-loaded by
the lease, and retains models that were already loaded, are busy, are pinned,
have another active lease, or belong to distributed workers. A lease is not a
pin and does not prevent normal administrator or memory-pressure eviction.
Lease release has a five-second request budget and never forces shutdown.

Lease bookkeeping is deliberately per-instance and ephemeral. In distributed
deployments it conveys no exclusive worker ownership, so release retains remote
models. A request arriving at another frontend, or after a restart, may return
404; clients treat this as unavailable cleanup. Leases expire after one hour;
expiration forgets the claim without unloading. Normal watchdog policy remains
responsible for abandoned models. No frontend-local lease map is treated as
cluster authority.

The release endpoint is a caller-lifetime cleanup operation and is intentionally
not exposed as a LocalAI Assistant administration tool. Existing model load and
shutdown tools continue to work without leases.
