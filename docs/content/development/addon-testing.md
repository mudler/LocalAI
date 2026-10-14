+++
title = "Focused addon testing"
toc = true
description = "Prepare and verify the toolchain for focused addon tests."
+++

The addon verification scripts separate explicit dependency setup from offline checks.
Run all commands from the repository root. Only the `prerequisites` suite is currently available.
Unknown suites, empty selections, skipped required tests, and failed commands return a nonzero status.

## Required tools

Provide these tools before running setup:

- Go 1.26.x, with `GOTOOLCHAIN=local`. Other minor versions are rejected.
- Node 22.12 or later within Node 22, with npm. The validated setup uses Node 22.22.0.
- `protoc` 26.1. The general Makefile downloader uses a different version.
- A native C compiler (`cc`, or the executable specified by `CC`) with `CGO_ENABLED=1`.
- Python 3, POSIX shell tools, and the runtime libraries required by Chromium.

Setup and integration preparation require at least 20 GiB of free disk space before each major step.
They do not install system packages or fetch Go, Node, or `protoc` automatically.
Place locally provisioned executables in `.cache/addons/bin`, or put them on `PATH`.

For Linux x86-64, the validated tool archives have these SHA-256 checksums:

| Archive | Source | SHA-256 |
| --- | --- | --- |
| `node-v22.22.0-linux-x64.tar.xz` | [Node 22.22.0 distribution](https://nodejs.org/dist/v22.22.0/) | `9aa8e9d2298ab68c600bd6fb86a6c13bce11a4eca1ba9b39d79fa021755d7c37` |
| `protoc-26.1-linux-x86_64.zip` | [Protocol Buffers 26.1 release](https://github.com/protocolbuffers/protobuf/releases/tag/v26.1) | `a7be2928c0454f132c599e25b79b7ad1b57663f2337d7f7e468a1d59b98ec1b0` |

Validate archive checksums before extraction. These archives do not apply to other architectures.

## Explicit setup

Setup downloads Go dependencies, installs the pinned protobuf plugins, and runs `npm ci` against the React lockfile.
It then installs Chromium through the local Playwright CLI, unless you supply an existing browser.

```sh
sh scripts/addons/prepare.sh --prepare
```

The protobuf plugin pins match the Makefile:

- `google.golang.org/protobuf/cmd/protoc-gen-go@v1.34.2`
- `google.golang.org/grpc/cmd/protoc-gen-go-grpc@1958fcbe2ca8bd93af633f11e97d44e567e945af`

Go resolves the second pin to module version `v1.4.0`.
The check validates each plugin's embedded module version and checksum, not its version label alone.
Go downloads use module checksum authentication. npm uses the lockfile's package integrity records.

The lockfile currently selects Playwright 1.58.2 and Chromium revision 1208 (Chrome 145.0.7632.6).
The installed Playwright package selects the browser revision; the scripts never invoke `npx`.
To provision only the browser after npm setup, run:

```sh
export PATH="$PWD/.cache/addons/bin:$PATH"
export PLAYWRIGHT_BROWSERS_PATH="$PWD/.cache/addons/browsers"
core/http/react-ui/node_modules/.bin/playwright install chromium
```

Keep `PLAYWRIGHT_BROWSERS_PATH` set to this worktree-local path for manual installation.
The preparation and verification scripts set it automatically.
A browser installed in Playwright's default user cache does not satisfy this setup.

Alternatively, set `PLAYWRIGHT_CHROMIUM_PATH` to an absolute executable path before setup and verification.
Setup then skips the browser download. Verification still requires a successful browser launch and rendered UI.
Neither option installs OS libraries. If launch fails, inspect the reported library names and the browser executable with `ldd` on Linux.
Provide a compatible runtime environment or report the missing libraries as a blocker; do not skip the browser probe.

## Prepare generated inputs

After explicit setup, generate only the inputs needed by the focused tests:

```sh
sh scripts/addons/prepare.sh --integration
```

This command generates `pkg/grpc/proto`, builds the React distribution, and builds two auth-enabled test binaries:

- `tests/e2e/mock-backend/mock-backend`
- `tests/e2e-ui/ui-test-server`

It does not build inference backends or run a full repository build.
It disables Go dependency downloads and requires the populated setup cache.
Repeat this step when the proto, UI, or test-server sources change.

## Check and verify

Inspect the toolchain, installed dependencies, and generated inputs without downloading or rebuilding them:

```sh
sh scripts/addons/prepare.sh --check
```

This read-only dependency check is not a browser or SQLite runtime test.
Run the complete readiness gate separately:

```sh
sh scripts/addons/verify.sh prerequisites
```

Verification performs these checks:

1. Run isolated shell fixtures for missing tools, incompatible versions, failed children, invalid reports, and empty or skipped selections.
2. Inspect the real toolchain and generated inputs.
3. Compile and execute a SQLite transaction with CGO, Ginkgo v2, and Gomega.
4. Start the real UI test server with the mock backend on an isolated loopback port.
5. Wait for server health, launch Chromium, and require a rendered React root.

The SQLite probe uses `go test -count=1 -tags auth` and a machine-readable Ginkgo report.
Verification rejects missing, empty, failed, skipped, or unexpected test cases.
It records child commands, output, and status in `.cache/addons/logs/verify-*.jsonl`.
Ginkgo reports use separate unique files in the same directory.
Server processes stop after the probe, including when the browser check fails.

Checks and verification never install dependencies or browsers.
They set `GOTOOLCHAIN=local`, `GOPROXY=off`, `GOSUMDB=off`, and `GOFLAGS=-mod=readonly` for verification builds.
The npm dependency check uses `--offline`.
Missing cached modules or browser files are failures, not permission to download them.

## Cache layout and test inventory

The scripts keep caches and transient results under `.cache/addons`:

| Path | Purpose |
| --- | --- |
| `bin` | Local tools and protobuf plugins; prepended to `PATH` |
| `go`, `go/pkg/mod` | `GOPATH` and `GOMODCACHE` |
| `go-build` | `GOCACHE` |
| `npm` | npm cache |
| `browsers` | Playwright browser installations |
| `tmp` | Setup, fixture, and verification scratch data |
| `logs` | Command results and Ginkgo reports |
| `downloads` | Optional archives from explicit local provisioning |

Do not commit caches, generated distributions, or test binaries.
Keep the command and required-case inventory in `scripts/addons/suites.json` synchronized with the runner.
Add future suites only when their actual tests and commands exist.
A successful prerequisite gate demonstrates readiness, not acceptance of addon functionality that has no tests yet.

## Gateway verification

Run `sh scripts/addons/verify.sh gateway` for the owner carrier and real-auth HTTP suites.
The inventory lists each report separately and requires their exact combined case count.
Missing reports, skipped cases, empty reports, and unsuccessful child commands fail the gate.
The tests use local certificate authorities, pinned mTLS peers, two receiving frontends,
and a real SQLite identity database. No external identity service or addon port is exposed.

The disconnect fixture consumes the POST envelope before waiting for cancellation.
Leaving that body unread prevents the HTTP/1 server from observing disconnect while
its handler waits, which can strand test-server cleanup. Production owner handlers decode
the bounded envelope before starting a relay.

## Foundation acceptance gate

The foundation gate is the end-to-end check of everything below the first
add-on: the owner, the carrier, the lifecycle, the policy, the proposals, the
file results, the Apps surface and the UI asset route. It is one integration
fixture, not a set of fakes at the seams.

```sh
sh scripts/addons/verify.sh foundation-acceptance   # the end-to-end suite
sh scripts/addons/foundation_gate.sh                 # every foundation suite
```

Run `sh scripts/addons/prepare.sh --integration` first. The gate builds the
server, the protocol fixture and the cluster command itself, so it never tests a
stale binary.

### The fixture

`tests/addons/foundation/cluster` starts three server processes of
`tests/e2e-ui`: the owner and two frontends (`front-a`, `front-b`). Each has its
own listener, data directory and carrier identity. They share one account
database, and they speak the real pinned mTLS carrier. The frontends open no
add-on storage and start no process. The add-on process is the protocol fixture
(`tests/addons/fixture/cmd`), packaged three ways from a catalog served on
loopback after the host was built:

| Package | Declares |
| --- | --- |
| `found-ui` | a service, the tools `echo` and `export`, and a UI module |
| `found-tool` | a service and the same tools, no UI |
| `found-svc` | a service only: no tool and no UI |

The fixture add-on reads `fixture.json` from its package. A mode changes how it
fails: `exit-start`, `never-ready`, `lose-reply` (commit, then drop the reply)
and `orphan` (ignore the closing of the startup channel). Its commits and every
apply request are written to a durable log, so a spec counts side effects across
restarts. Specs use real API keys and real browser sessions; the only route that
is not product code is the owner's loopback control route that registers a
resource, because the fixture has no add-on that creates one.

### What the gate proves

| Case group | Proves |
| --- | --- |
| install, activate, alternate | An install and an enable through a frontend reach the owner, start one process per add-on under the owner only, and reads alternate through the owner and both frontends |
| host unchanged | The host binary and the shipped UI build have the same digest after the whole lifecycle |
| actor propagation | The add-on sees the actor of the credential; forged actor and authority headers change nothing, and a forged administrator cannot reach lifecycle |
| JSON, progress, files, assets | JSON and file relays and lifecycle progress work through a non-owner frontend, a private file downloads through another frontend, and UI assets are served by digest, immutable and only for declared files |
| denials | A missing feature, a stranger, a stale generation and an undeclared tool give the same typed denial on every node |
| exactly once | A confirmed change reaches the add-on once, even confirmed again through another frontend; a lost reply is `outcome_unknown` with the original operation ID and is settled by receipt lookup with no second apply |
| owner down | Every frontend answers 503 with the same body, starts nothing and creates no storage; a restart brings back the last good package and the retained data |
| no duplicate owner | A second owner on the same storage does not keep running, and each add-on keeps one process |
| orphans | A process that outlived its owner is stopped before the next one starts |
| failed upgrade and enable | A new generation that cannot start leaves the last good one running; killing the owner in the middle of an upgrade or an enable keeps the last good code, the retained data and a usable lifecycle |
| disable, remove, reinstall | Data and grants outlive the removal, and a stale generation and its assets are denied after an upgrade |
| sessions and keys | Ending a session or revoking a key on one frontend refuses it on every node |
| browser | A member sees the Apps list only and the server refuses every management request; Open works through a non-owner frontend; a proxy path prefix works for deep links; a session ended on one frontend loses the assets on the others |
| service layer | An upgrade that cannot start keeps the last good generation running and the data; a request canceled during the start puts the last good generation back, keeps the operation open and the same operation ID finishes it; one broken add-on does not stop the owner |
| regression | The existing model and backend gallery specs (`core/gallery`), the gallery operation specs (`core/services/galleryop`) and the process adapter specs (`pkg/model`) pass |

The regression packages are the narrowest ones that run offline in under two
minutes each and that cover the code the add-on lifecycle sits beside. The
`core/gallery` model install specs run with `FIXTURES=tests/fixtures`. The only
skips the gate accepts are the specs that state they need macOS.

One `pkg/model` spec, "includes the exit code and final stderr line for an
unexpected exit", replaces the process-wide logger to capture a warning. It is
a test of the upstream package and the gate does not change it. Run together
with the other specs of the package, goroutines of those specs can log while
the logger is swapped and fail it at random. The gate therefore runs the
package three times: a dry run that fixes the inventory, a run that skips that
one spec, and a run that focuses on it alone. The case passes only when the
dry run lists exactly one such spec, the second run passes every other spec
(or skips it for macOS) and the third run passes that spec once. A spec that is
not accounted for in exactly one of the runs fails the case. A real failure of
the spec still fails the gate, with no retry.

### Reading the result

`foundation_gate.sh` runs every suite in the gate set, in order, and prints the
inventory: the passed count and the skipped count of each suite, and the total.
It exits nonzero when a suite fails, reports a skipped case, or is missing from
the set. A suite that is added to `suites.json` must be named in the script.
The report of the parent review is this inventory plus the case list of
`foundation-acceptance` in `scripts/addons/suites.json`.
