# Launcher worker baseline verification

LW01 supplies verification infrastructure only, not worker-mode product features.
On a Linux amd64 host, invoke Bash explicitly:

```sh
export LAUNCHER_VERIFY_WORKDIR="$PWD/assets/work/launcher-worker-validation"
bash contrib/launcher-worker/runner_test.sh
bash contrib/launcher-worker/verify.sh baseline
```

Host requirements: Bash, Docker, Git, GNU tar/coreutils (including `timeout`),
util-linux `flock`, and Python 3 for contract tests. Nothing is installed on the
host. The optional work directory defaults to the path shown above; an absolute
path outside the checkout is supported. At least **10 GiB free** is required
before starting. This is a minimum preflight, not a guarantee that every future
build fits: allow more space for caches, snapshots and Docker images.

Runs have unique evidence directories and share a serially locked cache. The
runner snapshots tracked working-tree files, including uncommitted modifications,
and its own infrastructure. Stage new product/test files before running: other
untracked files and ignored generated outputs are intentionally excluded. The
checkout is never mounted writable. Logs, snapshots, toolchains and caches stay
under the work directory. Container-created files may be root-owned. Docker
images and testcontainers volumes use the daemon's normal storage.

## Execution

- Ubuntu 22.04 Linux amd64, four CPUs, 12 GiB RAM, 2048 PIDs, one-hour outer
  timeout. No host network, privileged container, GPU access or host installs.
- Exact Go version from `go.mod`, verified against official download checksum
  metadata, with `GOTOOLCHAIN=local` (no automatic version substitution).
- Native Fyne/OpenGL/X11 libraries and Xvfb; no Fyne `ci` stub driver.
- Repository `make protogen-go` and `make generate` prerequisites.
- Serial uncached `go test -count=1 -timeout=20m -tags debug,p2p` runs for
  `cmd/launcher/internal`, `core/cli/workerregistry`, `core/services/worker`
  and `core/services/nodes`. All four are attempted after successful setup,
  even if an earlier package fails. There are no focus or skip filters.
- Each Ginkgo report must contain passing `It` specs, with no skipped, pending
  or failed specs. Missing/empty/malformed reports fail the gate independently
  of the test command's status.

The nodes suite uses real PostgreSQL testcontainers. It requires access to the
local Docker socket (`/var/run/docker.sock`, overridden with
`LAUNCHER_WORKER_DOCKER_SOCKET`). **Socket access grants daemon-level authority**:
this is build isolation, not a security sandbox for untrusted source. Never use a
production daemon. Testcontainers manages its temporary database containers and
reaper; those sibling containers are outside the runner's resource cgroup.
`host.docker.internal:host-gateway` provides access to their mapped ports without
host networking. Network access is required for apt, Go, modules and test images.

The scratch `/tmp` bind is explicitly mode **1777**, independent of caller umask.
Its ancestors in the container are `/`, not restrictive host directory ancestors.
The contract test confirms that `_apt` can create `apt.conf.*` through the direct
bind even beneath a mode-0700 host ancestor. It also tests free-space boundaries,
unsupported modes, report rejection (including under `python -O`), command exit
propagation through `tee`, and failed evidence writes.

## Evidence and boundaries

The runner prints its evidence directory. `logs/verify.exit`, `docker.exit`,
`container.exit`, per-step `.exit` files, package `.log` files, Ginkgo `.json`
reports and `environment.txt` distinguish setup and test failures. `source.txt`
records source HEAD, diff summary, Docker version and starting free space; the
actual tested snapshot is retained. Go may buffer package output until a package
finishes; an empty in-progress log is not evidence of a skipped run or a pass.
Nonzero statuses survive log piping, and failed log writes fail verification.
If wrapping the invocation in another pipeline, use `bash -o pipefail -c '...'`.

`config` runs the actual “Secure launcher configuration” specs followed by the
entire affected launcher suite. It uses the same isolated Ubuntu/Xvfb setup,
without mounting the Docker socket or rerunning worker/nodes suites:

```sh
bash contrib/launcher-worker/verify.sh config
```

The focused report requires every selected configuration spec to pass and only
allows skipped specs outside that exact group. The full launcher report still
rejects all skips. Baseline remains unchanged. Other modes fail with exit 2. Synthetic contract
reports are tests of infrastructure, not substitutes for real baseline evidence.
This does not certify distributed inference, lifecycle changes, race coverage,
AppImage packaging, interactive GUI/tray behavior or native macOS execution.
