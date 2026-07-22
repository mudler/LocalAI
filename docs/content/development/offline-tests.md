---
title: "Offline test resources"
---

LocalAI tests separate resource acquisition from test execution. Resources are
declared by resource set in `test-resources/manifests/`; files and packed container
images are content-addressed by SHA-256 under
`.cache/test-resources/blobs/sha256/`.

Prepare the resources before running a target:

```sh
make prepare-offline-test-cache TEST_RESOURCE_SET=default
```

Preparation verifies every cached blob and fails closed. It never substitutes
a live request for a missing or corrupt entry. Maintainers can populate a
cache from pinned declarations only by explicitly enabling online mode:

```sh
LOCALAI_TEST_RESOURCES_ONLINE=1 make update-offline-test-cache TEST_RESOURCE_SET=default
```

The update command records declared responses, files, and digest-pinned images,
then writes a deterministic, low-level gzip-compressed bundle at
`.cache/test-resources/bundles/<resource-set>.tar.gz`. Its SHA-256 is written to the
lock file. Until registry publication is enabled, CI transfers this tar as a
workflow artifact and verifies it after deleting the recording cache.

HTTP declarations may include `request_headers`. `Range` participates in the
cache key, and authorization values participate only through a SHA-256 value;
credentials are never written verbatim to the cache index. Redirect responses
are recorded without following them, so every hop needed by a test must be
declared explicitly.

File and HTTP declarations may list HTTPS `mirrors`. Recording tries the
canonical URL twice, then each mirror twice, and reports the duration of every
attempt. Every candidate must produce the same declared SHA-256; mirrors are
alternate transports, not alternate content.

A digest mismatch is never accepted automatically. The updater prints the
observed failure for every source and directs maintainers to compare upstream
checksums, signatures, release notes, and redirects, then check the GitHub
Advisory Database and OSV before approving a new digest. Repeated mismatches can
mean a legitimate upstream release, a corrupt mirror, or a supply-chain event.

Ordinary test recipes execute through `scripts/run-test-offline.sh`. Its
supervised replay proxy terminates HTTP and HTTPS and returns an immediate
error containing the method and URL for undeclared requests. Linux CI also
runs the command in a cgroup with public IPv4 and IPv6 rejected; macOS relies
on replay, declared resources, guarded Go transports, and static lint because
kernel-level subprocess enforcement is Linux-only.

Testcontainer images must be registry-digest pinned and loaded during
preparation. Container helpers check that an image exists before startup and
attach services to internal-only Docker networks, preventing testcontainers
from silently pulling a missing tag.

The default Linux and macOS suites use separate resource sets because
Docker archives are platform-specific. Backend and hardware resources remain
separate targets so ordinary contributors do not acquire large model fixtures
that their test command does not use.

Real third-party compatibility checks belong in separately named
`external-probe-*` scheduled workflows and must not be part of deterministic
test or coverage gates.
