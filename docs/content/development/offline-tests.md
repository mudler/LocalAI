---
title: "Offline test resources"
---

LocalAI tests separate resource acquisition from test execution. Resources are
declared by target in `test-resources/manifests/`; files and packed container
images are content-addressed by SHA-256 under
`.cache/test-resources/blobs/sha256/`.

Prepare the resources before running a target:

```sh
make test-resources TARGET=default
```

Preparation verifies every cached blob and fails closed. It never substitutes
a live request for a missing or corrupt entry. Maintainers can populate a
cache from pinned declarations only by explicitly enabling online mode:

```sh
LOCALAI_TEST_RESOURCES_ONLINE=1 make update-test-resources TARGET=default
```

Ordinary test recipes execute through `scripts/run-test-offline.sh`. It denies
public HTTP(S) through a closed proxy while allowing loopback and private
Docker networks. Linux CI may additionally place this runner in a restricted
network namespace; macOS relies on the proxy, declared resources, and guarded
Go transports because it has no equivalent portable kernel-level subprocess
filter.

Real third-party compatibility checks belong in separately named
`external-probe-*` scheduled workflows and must not be part of deterministic
test or coverage gates.
